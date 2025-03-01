package batch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type (
	// Queue of workers going to Enter the batch.
	// It's returned from Coordinator to worker for it to get in.
	Queue struct {
		c atomic.Int32
	}

	// Coordinator coordinates workers to update shared state,
	// commit it, and deliver result to all participated workers.
	Coordinator[Res any] struct {
		// CommitFunc is called to commit shared state.
		//
		// It's already called owning critical section. Enter-Exit cycle must not be called from it.
		//
		// Required.
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
		// Batch state.
		// - If cnt >= 0, the batch is being filled up, and cnt is the number of workers have entered.
		// - If cnt < 0, the batch has been committed/canceled, and -cnt is the number of workers still in the batch.
		cnt int

		// Batch result.
		res Res
		err error

		ready   bool // res and err are set.
		trigger bool // Commit has been triggered externally.
	}

	// PanicError is returned to all the workers in the batch if one panicked.
	// The panicked worker gets panic, not an error.
	PanicError struct {
		Panic interface{}
	}
)

// Canceled is the default error returned to workers when Cancel is called with a nil error.
var Canceled = errors.New("batch canceled")

// New creates new Coordinator.
func New[Res any](f func(ctx context.Context) (Res, error)) *Coordinator[Res] {
	return &Coordinator[Res]{
		CommitFunc: f,
	}
}

// Init initiates zero Coordinator.
// It can also be used as Reset but not in parallel with its usage.
func (c *Coordinator[Res]) Init(f func(ctx context.Context) (Res, error)) {
	c.CommitFunc = f
}

// Gets the queue of waiting workers.
//
// Worker can leave the Queue before Enter,
// but we must call Notify to wake up waiting workers.
func (c *Coordinator[Res]) Queue() *Queue {
	return &c.lock.queue
}

// Notify wakes up waiting workers.
//
// Must be called if the worker left the Queue before Enter.
func (c *Coordinator[Res]) Notify() {
	c.cond.Broadcast()
}

// Enter enters the batch.
// When the call returns we are in the critical section.
// Shared resources can be used safely.
// It's similar to Mutex.Lock.
// Pair method Exit must be called if Enter was successful (returned value >= 0).
// It returns index of entered worker.
// 0 means we are the first in the batch and we should reset shared state.
// If blocking == false and batch is not available negative value returned.
// Enter also removes the worker from the queue.
func (c *Coordinator[Res]) Enter(blocking bool) int {
	c.mu.Lock()

	q := c.queue.Out()
	if q < 0 {
		panic("batch misuse: negative queue length. c.Queue().In() before c.Enter()")
	}

	if c.cond.L == nil {
		c.cond.L = &c.mu
	}

	if c.cnt < 0 && !blocking {
		c.mu.Unlock()
		c.cond.Broadcast()

		return -1
	}

	for c.cnt < 0 {
		c.cond.Wait()
	}

	c.cnt++

	return c.cnt - 1
}

// Exit exits the critical section.
// It should be called with defer just after we successfully Entered the batch.
// It's similar to Mutex.Unlock.
// Returns number of workers have not Exited yet.
// 0 means we are the last exiting the batch, state can be reset here.
// But remember the case when worker have panicked.
func (c *Coordinator[Res]) Exit() int {
	defer func() {
		c.mu.Unlock()
		c.cond.Broadcast()
	}()

	return c.coach.exit()
}

func (c *coach[Res]) exit() int {
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
		c.trigger = false
	}

	return idx
}

// Trigger batch to Commit.
// We can call both Commit or Exit after that.
// If we added our data to the batch or if we didn't respectively.
// So we will be part of the batch or not.
func (c *Coordinator[Res]) Trigger() {
	c.trigger = true
}

// Commit waits for the waiting workes to add their data to the batch,
// calls Coordinator.Commit only once for the batch,
// and returns the same shared result to all workers.
func (c *Coordinator[Res]) Commit(ctx context.Context) (Res, error) {
	return commit[Res](ctx, &c.lock, &c.coach, nil, c.CommitFunc)
}

// Cancel aborts current batch and returns the same error to all workers already added their data to the batch.
// Coordinator.Commit is not called.
// Waiting workers but not Entered the critical section are not affected.
func (c *Coordinator[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit[Res](ctx, &c.lock, &c.coach, err, nil)
}

func commit[Res any](ctx context.Context, c *lock, cc *coach[Res], err error, f func(ctx context.Context) (Res, error)) (Res, error) {
	for {
		if cc.cnt >= 0 && (err != nil || cc.trigger || c.queue.Len() == 0) {
			return finalize[Res](ctx, c, cc, err, f)
		}

		c.cond.Wait()

		if cc.ready {
			break
		}
	}

	return cc.res, cc.err
}

func finalize[Res any](ctx context.Context, c *lock, cc *coach[Res], err error, f func(ctx context.Context) (Res, error)) (Res, error) {
	if cc.cnt < 0 {
		panic("batch: inconsistent state")
	}

	cc.cnt = -cc.cnt

	if err != nil {
		cc.err = err
		cc.ready = true

		return cc.res, cc.err
	}

	defer func() {
		cc.ready = true

		if p := recover(); p != nil {
			cc.err = PanicError{Panic: p}
		}
	}()

	c.mu.Unlock()
	defer c.mu.Lock()

	cc.res, cc.err = f(ctx)

	return cc.res, cc.err
}

// In gets into the queue.
func (q *Queue) In() int {
	return int(q.c.Add(1))
}

// Out gets out of the queue.
func (q *Queue) Out() int {
	return int(q.c.Add(-1))
}

// Len is the number of workers in the queue.
func (q *Queue) Len() int {
	return int(q.c.Load())
}

// AsPanicError unwraps PanicError.
func AsPanicError(err error) (PanicError, bool) {
	var pe PanicError

	return pe, errors.As(err, &pe)
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Panic)
}
