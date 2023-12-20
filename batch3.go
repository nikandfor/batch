package batch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type (
	Controller struct {
		Commit func(ctx context.Context) (interface{}, error)

		queue int32 // number of goroutines waiting at the mutex

		mu   sync.Mutex
		cond sync.Cond

		cnt int // number of goroutines in a critical zone

		res interface{}
		err error
	}

	Batch struct {
		c *Controller
		i int

		noCopy noCopy
		state  int
	}

	PanicError struct {
		Panic interface{}
	}

	noCopy struct{}
)

const (
	stateNew = iota
	stateQueued
	stateEntered
	stateCommitted
	stateExited
)

var (
	ErrRollback = errors.New("rollback")
)

// Enter enters a batch.
func (c *Controller) Enter() Batch {
	atomic.AddInt32(&c.queue, 1)

	return Batch{
		c:     c,
		i:     c.enter(),
		state: stateEntered,
	}
}

func (c *Controller) Batch() Batch {
	return Batch{
		c: c,
		i: -1,
	}
}

func (c *Controller) enter() int {
	c.mu.Lock()

	if c.cond.L == nil {
		c.cond.L = &c.mu
	}

	for c.cnt < 0 {
		c.cond.Wait()
	}

	atomic.AddInt32(&c.queue, -1)
	c.cnt++

	return int(c.cnt) - 1
}

func (c *Controller) exit() {
	c.cnt++

	if c.cnt == 0 {
		c.res, c.err = nil, nil
	}

	c.mu.Unlock()
	c.cond.Broadcast()
}

func (c *Controller) commit(ctx context.Context, err error) (interface{}, error) {
	if err != nil || atomic.LoadInt32(&c.queue) == 0 {
		c.cnt = -c.cnt

		if ep, ok := err.(PanicError); ok {
			c.err = err
			panic(ep.Panic)
		}

		if err != nil {
			c.err = err
		} else {
			func() {
				defer func() {
					if p := recover(); p != nil {
						c.err = PanicError{Panic: p}
					}
				}()

				c.res, c.err = c.Commit(ctx)
			}()
		}
	} else {
		c.cond.Wait()
	}

	res, err := c.res, c.err

	return res, err
}

func (b *Batch) QueueUp() {
	if b.state != stateQueued-1 {
		panic("usage: QueueUp -> Enter -> defer Exit -> Commit/Rollback")
	}

	b.state = stateQueued

	atomic.AddInt32(&b.c.queue, 1)
}

func (b *Batch) Enter() {
	if b.state != stateEntered-1 {
		panic("usage: QueueUp -> Enter -> defer Exit -> Commit/Rollback")
	}

	b.state = stateEntered

	b.i = b.c.enter()
}

func (b *Batch) Index() int {
	return b.i
}

func (b *Batch) Exit() {
	switch b.state {
	case stateNew:
		return
	case stateQueued:
		atomic.AddInt32(&b.c.queue, -1)
		return
	case stateEntered, stateCommitted:
	case stateExited:
		panic("usage: QueueUp -> Enter -> defer Exit -> Commit/Rollback")
	}

	defer func() {
		b.c.exit()

		b.state = stateExited
	}()

	if b.state == stateCommitted {
		return
	}

	err := ErrRollback

	p := recover()
	if p != nil {
		err = PanicError{Panic: p}
	}

	_, _ = b.c.commit(context.Background(), err)
}

func (b *Batch) Commit(ctx context.Context) (interface{}, error) {
	if b.state != stateCommitted-1 {
		panic("usage: QueueUp -> Enter -> defer Exit -> Commit/Rollback")
	}

	b.state = stateCommitted

	return b.c.commit(ctx, nil)
}

func (b *Batch) Rollback(ctx context.Context, err error) (interface{}, error) {
	if b.state != stateCommitted-1 {
		panic("usage: QueueUp -> Enter -> defer Exit -> Commit/Rollback")
	}

	b.state = stateCommitted

	if err == nil {
		err = ErrRollback
	}

	return b.c.commit(ctx, err)
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Panic)
}

func (noCopy) Lock() {}
