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

		locs

		coach[Res]
	}

	locs struct {
		queue Queue

		mu   sync.Mutex
		cond sync.Cond
	}

	coach[Res any] struct {
		cnt int

		res   Res
		err   error
		ready bool
	}

	PanicError struct {
		Panic interface{}
	}
)

const usage = "QueueIn -> Enter -> defer Exit -> Commit/Cancel"

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
	return commit[Res](ctx, &c.locs, &c.coach, nil, force, c.CommitFunc)
}

func (c *Coordinator[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit[Res](ctx, &c.locs, &c.coach, err, false, nil)
}

func commit[Res any](ctx context.Context, c *locs, cc *coach[Res], err error, force bool, f func(ctx context.Context) (Res, error)) (Res, error) {
again:
	if err != nil || force || c.queue.Len() == 0 {
		cc.cnt = -cc.cnt

		if err != nil {
			cc.err = err
			cc.ready = true

			return cc.res, cc.err
		}

		func() {
			defer func() {
				cc.ready = true

				if p := recover(); p != nil {
					cc.err = PanicError{Panic: p}
				}
			}()

			c.mu.Unlock()
			defer c.mu.Lock()

			cc.res, cc.err = f(ctx)
		}()
	} else {
	wait:
		c.cond.Wait()

		if cc.cnt > 0 {
			goto again
		}
		if !cc.ready {
			goto wait
		}
	}

	return cc.res, cc.err
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
