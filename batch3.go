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
		Coach  int
		Commit func(ctx context.Context, coach int) (Res, error)

		mcond *sync.Cond

		queue int32 // number of goroutines waiting at the mutex

		mu   sync.Mutex
		cond sync.Cond

		cnt int // number of goroutines in a critical zone

		res   Res
		err   error
		ready bool
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
	//	stateExited

	usage = "usage: [QueueUp ->] Enter -> defer Exit -> Commit/Rollback"
)

var (
	ErrRollback = errors.New("rollback")
)

// Enter enters a batch.
func (c *Controller[Res]) Enter(blocking bool) (Batch[Res], int) {
	state := stateNew

	c.queueUp()
	i := c.enter(blocking)
	if i >= 0 {
		state = stateEntered
	}

	return Batch[Res]{
		c:     c,
		state: state,
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

func (c *Controller[Res]) enter(blocking bool) int {
	c.mu.Lock()

	if c.cond.L == nil {
		c.cond.L = &c.mu
	}

	if c.cnt < 0 && !blocking {
		// reset to status quo
		i := c.cnt

		atomic.AddInt32(&c.queue, -1)
		c.cond.Broadcast()
		c.mu.Unlock()

		return i
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
		c.ready = false
	}

	c.mu.Unlock()
	c.cond.Broadcast()

	if c.mcond != nil {
		c.mcond.Broadcast()
	}

	return cnt
}

func (c *Controller[Res]) commit(ctx context.Context, err error) (Res, error) {
again:
	if err != nil || atomic.LoadInt32(&c.queue) == 0 {
		c.cnt = -c.cnt

		if ep, ok := err.(PanicError); ok {
			c.err = err
			c.ready = true
			panic(ep.Panic)
		} else if err != nil {
			c.err = err
			c.ready = true
		} else {
			func() {
				var res Res
				var err error

				defer func() {
					c.res, c.err = res, err
					c.ready = true

					if p := recover(); p != nil {
						c.err = PanicError{Panic: p}
					}
				}()

				c.mu.Unlock()
				defer c.mu.Lock()

				res, err = c.Commit(ctx, c.Coach)
			}()
		}
	} else {
	wait:
		c.cond.Wait()

		if c.cnt > 0 {
			goto again
		}
		if !c.ready {
			goto wait
		}
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

func (b *Batch[Res]) Enter(blocking bool) int {
	if b.state == stateQueued-1 {
		b.QueueUp()
	}

	if b.state != stateEntered-1 {
		panic(usage)
	}

	i := b.c.enter(blocking)
	if i >= 0 {
		b.state = stateEntered
	} else {
		b.state = stateNew
	}

	return i
}

func (b *Batch[Res]) Exit() (i int) {
	switch b.state {
	case stateNew:
		return -1
	case stateQueued:
		atomic.AddInt32(&b.c.queue, -1)
		b.c.cond.Broadcast()
		return -1
	case stateEntered, stateCommitted:
		//	case stateExited:
		//		panic(usage)
	}

	defer func() {
		i = b.c.exit()

		//	b.state = stateExited
		b.state = stateNew
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

func AsPanicError(err error) (PanicError, bool) {
	var pe PanicError

	return pe, errors.As(err, &pe)
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Panic)
}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
