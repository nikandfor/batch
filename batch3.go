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
		noCopy noCopy

		c *Controller
		i int

		triggered bool
	}

	PanicError struct {
		Panic interface{}
	}

	noCopy struct{}
)

var (
	ErrRollback = errors.New("rollback")
)

func (c *Controller) Enter() Batch {
	atomic.AddInt32(&c.queue, 1)
	c.mu.Lock()

	if c.cond.L == nil {
		c.cond.L = &c.mu
	}

	for c.cnt < 0 {
		c.cond.Wait()
	}

	atomic.AddInt32(&c.queue, -1)
	c.cnt++

	return Batch{
		c: c,
		i: int(c.cnt) - 1,
	}
}

func (b *Batch) Index() int {
	return b.i
}

func (b *Batch) Exit() {
	defer func() {
		b.c.cnt++

		if b.c.cnt == 0 {
			b.c.res, b.c.err = nil, nil
		}

		b.c.mu.Unlock()
		b.c.cond.Broadcast()
	}()

	if b.triggered {
		return
	}

	err := ErrRollback

	p := recover()
	if p != nil {
		err = PanicError{Panic: p}
	}

	_, _ = b.commit(nil, err)
}

func (b *Batch) Commit(ctx context.Context) (interface{}, error) {
	return b.commit(ctx, nil)
}

func (b *Batch) Rollback(ctx context.Context, err error) (interface{}, error) {
	if err == nil {
		err = ErrRollback
	}

	return b.commit(ctx, err)
}

func (b *Batch) commit(ctx context.Context, err error) (interface{}, error) {
	if b.triggered {
		panic("usage: Enter -> defer Exit -> optional Commit with err or nil")
	}

	b.triggered = true

	if err != nil || atomic.LoadInt32(&b.c.queue) == 0 {
		b.c.cnt = -b.c.cnt

		if ep, ok := err.(PanicError); ok {
			b.c.err = err
			panic(ep.Panic)
		}

		if err != nil {
			b.c.err = err
		} else {
			func() {
				defer func() {
					if p := recover(); p != nil {
						b.c.err = PanicError{Panic: p}
					}
				}()

				b.c.res, b.c.err = b.c.Commit(ctx)
			}()
		}
	} else {
		b.c.cond.Wait()
	}

	res, err := b.c.res, b.c.err

	return res, err
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Panic)
}

func (noCopy) Lock() {}
