package batch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type (
	// Queue for workers entering the batch.
	// Returned by the Controller to allow workers to join.
	Queue struct {
		c atomic.Int32
	}

	// Controller manages workers updating and committing a shared state,
	// then delivering the result to all participating workers.
	Controller[Res any] struct {
		// Committer is called to commit the shared state.
		//
		// Called within a critical section. Do not invoke other Controller methods here.
		//
		// Set Committer OR use Controller.CommitFunc.
		Committer CommitFunc[Res]

		lock

		coach[Res]
	}

	CommitFunc[Res any] = func(ctx context.Context) (Res, error)

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
		trigger bool // Commit has been triggered by the user.
	}

	// PanicError is returned as an error to all workers in the batch if one of the workers panicked.
	// The worker that panicked will have the original panic value re-raised, not this error.
	PanicError struct {
		Panic any
	}
)

// Canceled is the default error returned to workers when Cancel is called with a nil error.
var Canceled = errors.New("batch canceled")

// New creates a new Controller with the given commit function.
func New[Res any](f CommitFunc[Res]) *Controller[Res] {
	return &Controller[Res]{
		Committer: f,
	}
}

// Init initializes the Controller with the given commit function.
// Can also be used to reset the Controller, but not concurrently with active operations.
func (c *Controller[Res]) Init(f CommitFunc[Res]) {
	c.Committer = f
}

// Queue returns the queue for workers waiting to enter the batch.
//
// Workers can leave the queue before entering, but Notify must be called
// to signal waiting workers in that case.
func (c *Controller[Res]) Queue() *Queue {
	return &c.lock.queue
}

// Notify unblocks waiting workers.
//
// This must be called if a worker leaves the Queue before entering the batch.
func (c *Controller[Res]) Notify() {
	c.cond.Broadcast()
}

// Enter attempts to join the batch.
// Returns the worker's index within the batch upon success (>= 0), entering the critical section.
// Shared resources can be safely accessed after a successful return.
// This is analogous to Mutex.Lock or TryLock, and must be paired with a call to Exit.
// An index of 0 indicates this worker is the first in the batch and should initialize shared state.
// If blocking is false and the batch is not ready, a negative value is returned.
// Enter also removes the worker from the waiting queue.
func (c *Controller[Res]) Enter(blocking bool) int {
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

// 0 means we are the last exiting the batch, state can be reset here.
// But remember the case when worker have panicked.

// Exit leaves the critical section.
// Should be called with defer immediately after a successful Enter.
// Analogous to Mutex.Unlock.
func (c *Controller[Res]) Exit() {
	defer func() {
		c.mu.Unlock()
		c.cond.Broadcast()
	}()

	c.coach.exit()
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
// Must be called inside Enter-Exit section.
//
// Can be called before or after adding data to the batch.
// The commit is initiated when this worker calls Commit or Exit.
// If calling Exit, the worker may get into the Queue and Enter again to retry the batch.
func (c *Controller[Res]) Trigger() {
	c.trigger = true
}

// Commit waits for all pending workers to contribute to the batch.
// It then calls the Controller.Committer function once with the complete batch
// and returns the resulting shared value to all waiting workers.
func (c *Controller[Res]) Commit(ctx context.Context) (Res, error) {
	return commit(ctx, &c.lock, &c.coach, nil, c.Committer)
}

// CommitFunc behaves like Commit but uses the provided function `f`
// instead of the Controller.Committer.
// All workers participating in the same batch must consistently use
// either Commit or CommitFunc with the same `f`.
func (c *Controller[Res]) CommitFunc(ctx context.Context, f CommitFunc[Res]) (_ Res, err error) {
	return commit(ctx, &c.lock, &c.coach, nil, f)
}

// Cancel aborts the current batch and returns the provided error
// to all workers that have already added their data to the batch.
// Controller.Committer is not called.
// Workers waiting to Enter the critical section are not affected.
func (c *Controller[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit(ctx, &c.lock, &c.coach, err, nil)
}

func commit[Res any](ctx context.Context, c *lock, cc *coach[Res], err error, f CommitFunc[Res]) (Res, error) {
	for {
		if cc.cnt >= 0 && (err != nil || cc.trigger || c.queue.Len() == 0) {
			return finalize(ctx, c, cc, err, f)
		}

		c.cond.Wait()

		if cc.ready {
			break
		}
	}

	return cc.res, cc.err
}

func finalize[Res any](ctx context.Context, c *lock, cc *coach[Res], err error, f CommitFunc[Res]) (Res, error) {
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
