package batch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

type (
	Batch struct {
		queue int32

		Prepare  func(ctx context.Context) error
		Commit   func(ctx context.Context) (interface{}, error)
		Rollback func(ctx context.Context, err error) error
		Panic    func(ctx context.Context, p interface{}) error

		Limit *Semaphore

		sync.Mutex
		sync.Cond

		cnt int

		res   interface{}
		err   error
		panic interface{}
	}

	PanicError struct {
		Panic interface{}
	}
)

func New(commit func(ctx context.Context) (interface{}, error)) *Batch {
	b := &Batch{}

	b.Init(commit)

	return b
}

func (b *Batch) Init(commit func(ctx context.Context) (interface{}, error)) {
	b.Cond.L = &b.Mutex
	b.Commit = commit
}

func (b *Batch) Do(ctx context.Context, f func(ctx context.Context) error) (res interface{}, err error) {
	defer b.Limit.Exit()
	b.Limit.Enter()

	atomic.AddInt32(&b.queue, 1)

	defer b.Unlock()
	b.Lock()

	// wait for all goroutines from the previous batch to exit
	for b.cnt < 0 {
		b.Cond.Wait()
	}

	var p, p2 interface{}

	if b.cnt == 0 && b.Prepare != nil { // the first prepares the batch
		p = b.catchPanic(func() {
			b.err = b.Prepare(ctx)
		})
	}

	// add state to the batch if no errors happened so far
	if p == nil && b.err == nil {
		p = b.catchPanic(func() {
			b.err = f(ctx)
		})
	}

	if p != nil && b.panic == nil { // any goroutine sets panic if it happened
		b.panic = p
		b.err = PanicError{Panic: p} // panic overwrites error
	}

	x := atomic.AddInt32(&b.queue, -1) // will only be 0 if we are the last exiting the batch
	b.cnt++                            // count entered

	if x != 0 { // we are not the last exiting the batch, wait for others
		b.Cond.Wait() // so wait for the last one to finish the job
	} else {
		b.cnt = -b.cnt // set committing mode, no new goroutines allowed to enter

		p2 = b.catchPanic(func() {
			switch {
			case b.panic != nil:
				if b.Panic != nil {
					b.err = b.Panic(ctx, b.panic)
				}
			case b.err == nil:
				b.res, b.err = b.Commit(ctx)
			case b.Rollback != nil:
				b.err = b.Rollback(ctx, b.err)
			}
		})
	}

	b.cnt++ // reset committing mode when everybody left
	b.Cond.Broadcast()

	res, err = b.res, b.err // return the same result to all the entered

	if b.cnt == 0 { // the last turns the lights off
		b.res, b.err, b.panic = nil, nil, nil
	}

	if p2 != nil {
		panic(p2)
	}

	if p != nil {
		panic(p)
	}

	return
}

func (b *Batch) catchPanic(f func()) (p interface{}) {
	defer func() {
		p = recover()
	}()

	f()

	return
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Panic)
}
