package batch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

/*
Full guide.

	bc.Queue().In() // get in the queue

	// Get data ready for the commit.
	// All the blocking operations should be done here.
	data := 1

	if leave() {
		bc.Queue().Out() // get out of the queue
		bc.Notify()      // notify waiting goroutines we won't come
		return
	}

	idx := bc.Enter(true) // true to block and wait, false for non-blocking return if batch is in the process of commiting
	                     // just like with the Mutex there are no guaranties how will enter the first
	if idx < 0 {        // in non-blocking mode we didn't enter the batch, it's always >= 0 in blocking mode
		return         // no need to do anything here
	}

	defer bc.Exit() // if we entered we must exit
	_ = 0          // calling it with defer ensures state consistency in case of panic

	// We are inside locked Mutex between Enter and Exit.
	// So the shared state can be modified safely.
	// That also means that all the long and heavy operations
	// should be done before Enter.

	if idx == 0 { // we are first in the batch, reset the state
		sum = 0
	}

	if full() {       // if we didn't change the state we can just leave.
		bc.Trigger() // Optionally we can trigger the batch.
		            // Common use case is to flush the data if we won't fit.
		return     // Then we may return and retry in the next batch.
	}

	sum += data // adding our work to the batch

	if spoilt() {
		_, err = bc.Cancel(ctx, causeErr) // cancel the batch, commit isn't done, all get causeErr
		                                 // if causeErr is nil it's set to Canceled
	}

	if failed() {
		panic("we can safely panic here") // panic will be propogated to the caller,
		                                 // other goroutines in the batch will get PanicError
	}

	if urgent() {
		bc.Trigger() // do not wait for the others
	}

	res, err := bc.Commit(ctx) // get ready to commit.
	                          // The last goroutine entered the batch calls the actual commit.
	                         // All the others wait and get the same res and error.
*/

type (
	Queue int32

	Coordinator[Res any] struct {
		CommitFunc func(ctx context.Context) (Res, error)

		lock

		coach[Res]
	}

	lock struct {
		queue Queue

		mu   sync.Mutex
		cond sync.Cond
	}

	coach[Res any] struct {
		cnt int

		res     Res
		err     error
		ready   bool
		trigger bool
	}

	PanicError struct {
		Panic interface{}
	}
)

var Canceled = errors.New("batch canceled")

func New[Res any](f func(ctx context.Context) (Res, error)) *Coordinator[Res] {
	return &Coordinator[Res]{
		CommitFunc: f,
	}
}

func (c *Coordinator[Res]) Init(f func(ctx context.Context) (Res, error)) {
	c.CommitFunc = f
}

func (c *Coordinator[Res]) Queue() *Queue {
	return &c.lock.queue
}

func (c *Coordinator[Res]) Notify() {
	c.cond.Broadcast()
}

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

func (c *Coordinator[Res]) Trigger() {
	c.trigger = true
}

func (c *Coordinator[Res]) Commit(ctx context.Context) (Res, error) {
	return commit[Res](ctx, &c.lock, &c.coach, nil, c.CommitFunc)
}

func (c *Coordinator[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit[Res](ctx, &c.lock, &c.coach, err, nil)
}

func commit[Res any](ctx context.Context, c *lock, cc *coach[Res], err error, f func(ctx context.Context) (Res, error)) (Res, error) {
again:
	if err != nil || cc.trigger || c.queue.Len() == 0 {
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
