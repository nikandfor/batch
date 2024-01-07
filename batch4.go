package batch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type (
	Queue int32

	Coordinator[Res any] struct {
		CommitFunc func(ctx context.Context) (Res, error)

		queue Queue

		mu   sync.Mutex
		cond sync.Cond

		cnt int

		res   Res
		err   error
		ready bool
	}

	PanicError struct {
		Panic interface{}
	}
)

const usage = "Queue.In -> Enter -> defer Exit -> Commit/Cancel"

var Canceled = errors.New("batch canceled")

func New[Res any](f func(ctx context.Context) (Res, error)) *Coordinator[Res] {
	return &Coordinator[Res]{
		CommitFunc: f,
	}
}

func (c *Coordinator[Res]) QueueIn() { c.queue.In() }

func (c *Coordinator[Res]) Enter(blocking bool) int {
	c.mu.Lock()

	c.queue.Out()

	if c.cond.L == nil {
		c.cond.L = &c.mu
	}

	if c.cnt < 0 && !blocking {
		idx := c.cnt

		c.mu.Unlock()
		c.cond.Broadcast()

		return idx
	}

	for c.cnt < 0 {
		c.cond.Wait()
	}

	c.cnt++

	return c.cnt - 1
}

func (c *Coordinator[Res]) Exit() int {
	defer func() {
		c.mu.Unlock()
		c.cond.Broadcast()
	}()

	if c.cnt > 0 {
		p := recover()
		if p == nil { // we just left
			c.cnt--
			return c.cnt
		}

		c.cnt = -c.cnt
		c.err = PanicError{Panic: p}
		c.ready = true

		defer panic(p)
	}

	c.cnt++
	idx := -c.cnt

	if c.cnt == 0 {
		var zero Res
		c.res, c.err, c.ready = zero, nil, false
	}

	return idx
}

func (c *Coordinator[Res]) Commit(ctx context.Context, force bool) (Res, error) {
	return c.commit(ctx, nil, force)
}

func (c *Coordinator[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return c.commit(ctx, err, false)
}

func (c *Coordinator[Res]) commit(ctx context.Context, err error, force bool) (Res, error) {
again:
	if err != nil || force || c.queue.Len() == 0 {
		if c.cnt <= 0 {
			panic("inconsistent state")
		}

		c.cnt = -c.cnt

		if err != nil {
			c.err = err
			c.ready = true

			return c.res, c.err
		}

		func() {
			defer func() {
				c.ready = true

				if p := recover(); p != nil {
					c.err = PanicError{Panic: p}
				}
			}()

			c.mu.Unlock()
			defer c.mu.Lock()

			c.res, c.err = c.CommitFunc(ctx)
		}()
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

	return c.res, c.err
}

func (q *Queue) In() int {
	return int(atomic.AddInt32((*int32)(q), 1))
}

func (q *Queue) Out() int {
	return int(atomic.AddInt32((*int32)(q), -1))
}

func (q *Queue) Len() int {
	return int(atomic.LoadInt32((*int32)(q)))
}

func AsPanicError(err error) (PanicError, bool) {
	var pe PanicError

	return pe, errors.As(err, &pe)
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Panic)
}
