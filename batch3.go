package batch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type (
	Controller[Res any] struct {
		Commit func(ctx context.Context) (Res, error)

		queue int32 // number of goroutines waiting at the mutex

		mu   sync.Mutex
		cond sync.Cond

		cnt int // number of goroutines in a critical zone

		res Res
		err error
	}

	Batch[Res any] struct {
		c *Controller[Res]

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

	usage = "usage: QueueUp -> Enter -> defer Exit -> Commit/Rollback"
)

var (
	ErrRollback = errors.New("rollback")
)

// Enter enters a batch.
func (c *Controller[Res]) Enter() (Batch[Res], int) {
	c.queueUp()
	i := c.enter()

	return Batch[Res]{
		c:     c,
		state: stateEntered,
	}, i
}

func (c *Controller[Res]) Batch() Batch[Res] {
	return Batch[Res]{
		c: c,
	}
}

func (c *Controller[Res]) queueUp() int {
	return int(atomic.AddInt32(&c.queue, 1))
}

func (c *Controller[Res]) enter() int {
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

func (c *Controller[Res]) exit() int {
	c.cnt++
	cnt := -c.cnt

	if c.cnt == 0 {
		var zero Res
		c.res, c.err = zero, nil
	}

	c.mu.Unlock()
	c.cond.Broadcast()

	return cnt
}

func (c *Controller[Res]) commit(ctx context.Context, err error) (Res, error) {
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

func (b *Batch[Res]) QueueUp() int {
	if b.state != stateQueued-1 {
		panic(usage)
	}

	b.state = stateQueued

	return b.c.queueUp()
}

func (b *Batch[Res]) Enter() int {
	if b.state == stateQueued-1 {
		b.QueueUp()
	}

	if b.state != stateEntered-1 {
		panic(usage)
	}

	b.state = stateEntered

	return b.c.enter()
}

func (b *Batch[Res]) Exit() (i int) {
	switch b.state {
	case stateNew:
		return -1
	case stateQueued:
		atomic.AddInt32(&b.c.queue, -1)
		return -1
	case stateEntered, stateCommitted:
	case stateExited:
		panic(usage)
	}

	defer func() {
		i = b.c.exit()

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

	return 0
}

func (b *Batch[Res]) Commit(ctx context.Context) (Res, error) {
	if b.state != stateCommitted-1 {
		panic(usage)
	}

	b.state = stateCommitted

	return b.c.commit(ctx, nil)
}

func (b *Batch[Res]) Rollback(ctx context.Context, err error) (Res, error) {
	if b.state != stateCommitted-1 {
		panic(usage)
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
