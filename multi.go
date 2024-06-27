package batch

import (
	"context"
)

type (
	// Multi is a coordinator for multiple parallel batches.
	// Multi can't be created as a literal,
	// it must be initialized either by NewMulti or Init.
	Multi[Res any] struct {
		// CommitFunc is called to commit result of batch number coach.
		//
		// It's already called owning critical section. Enter-Exit cycle must not be called from it.
		CommitFunc func(ctx context.Context, coach int) (Res, error)

		// Balancer chooses replica among available or it can choose to wait for more.
		// bitset is a set of available coaches.
		// Coach n is available <=> bitset[n/64] & (1<<(n%64)) != 0.
		// If returned value >= 0 and that coach is available it proceeds with it.
		// If returned value < 0 or that coach is not available
		// worker acts as there were no available coaches.
		Balancer  func(bitset []uint64) int
		available []uint64

		lock

		cs []coach[Res]
	}
)

// NewMulti create new Multi coordinator with n parallel batches.
func NewMulti[Res any](n int, f func(ctx context.Context, coach int) (Res, error)) *Multi[Res] {
	return &Multi[Res]{
		CommitFunc: f,

		cs: make([]coach[Res], n),
	}
}

// Init initiates zero Multi.
// It can also be used as Reset but not in parallel with its usage.
func (c *Multi[Res]) Init(n int, f func(ctx context.Context, coach int) (Res, error)) {
	c.CommitFunc = f
	c.cs = make([]coach[Res], n)
}

// Queue returns common queue for all coaches.
// Getting into it means already started batches will wait for it not committing yet.
//
// Worker can leave the Queue before Enter, but we must call Notify to wake up waiting workers.
func (c *Multi[Res]) Queue() *Queue {
	return &c.lock.queue
}

// Notify wakes up waiting workers.
//
// Must be called if the worker left the Queue before Enter.
func (c *Multi[Res]) Notify() {
	c.cond.Broadcast()
}

// Enter available batch.
// It will return -1, -1 if blocking == false and no batches available.
// Enter also removes worker from the queue.
//
// See also documentation for Coordinator.Enter.
//
// coach choice can be configured by setting custom Multi.Balancer.
func (c *Multi[Res]) Enter(blocking bool) (coach, idx int) {
	c.mu.Lock()

	c.queue.Out()

	if c.cond.L == nil {
		c.cond.L = &c.mu
	}

again:
	if c.Balancer != nil {
		coach, idx := c.enterBalancer()
		if idx >= 0 {
			return coach, idx
		}
	} else {
		for coach := range c.cs {
			if idx := c.cs[coach].cnt; idx >= 0 {
				c.cs[coach].cnt++

				return coach, idx
			}
		}
	}

	if !blocking {
		c.mu.Unlock()
		c.cond.Broadcast()

		return -1, -1
	}

	c.cond.Wait()

	goto again
}

func (c *Multi[Res]) enterBalancer() (coach, idx int) {
	if c.available == nil {
		c.available = make([]uint64, (len(c.cs)+63)/64)
	}

	for i := range c.available {
		c.available[i] = 0
	}

	for coach := range c.cs {
		if c.cs[coach].cnt < 0 {
			continue
		}

		i, j := coach/64, coach%64

		c.available[i] |= 1 << j
	}

	coach = c.Balancer(c.available)
	if coach < 0 || c.cs[coach].cnt < 0 {
		return -1, -1
	}

	c.cs[coach].cnt++

	return coach, c.cs[coach].cnt - 1
}

// Exit the batch. Should be called with defer.
// It works similar to Mutex.Unlock in the sense it unlocks shared resources.
func (c *Multi[Res]) Exit(coach int) int {
	defer func() {
		c.mu.Unlock()
		c.cond.Broadcast()
	}()

	cc := &c.cs[coach]

	if cc.cnt > 0 {
		p := recover()
		if p == nil { // we just left
			cc.cnt--
			return cc.cnt
		}

		cc.cnt = -cc.cnt
		cc.err = PanicError{Panic: p}
		cc.ready = true

		defer panic(p)
	}

	cc.cnt++
	idx := -cc.cnt

	if cc.cnt == 0 {
		var zero Res
		cc.res, cc.err, cc.ready = zero, nil, false
	}

	return idx
}

// Trigger the batch to commit.
// Must be called inside Enter-Exit section.
//
// Can be used to flush current batch. With our data or without and then we can retry.
// Commit happens when current worker Exits from critical section.
// So you need to Exit and then get into the Queue and Enter again to retry.
func (c *Multi[Res]) Trigger(coach int) {
	c.cs[coach].trigger = true
}

// Commit indicates the current worker is done with the shared state and ready to Commit it.
// Commit blocks until batch is committed. The same Res and error is returned to all the workers in the batch.
// (Res, error) is what the Multi.Commit returns.
func (c *Multi[Res]) Commit(ctx context.Context, coach int) (Res, error) {
	return commit(ctx, &c.lock, &c.cs[coach], nil, func(ctx context.Context) (Res, error) {
		return c.CommitFunc(ctx, coach)
	})
}

// Cancel indicates the current worker is done with shared data but it mustn't be committed.
// All the workers from the same batch receive zero Res and the same error.
//
// It can be used if batch shared state have been spoilt as a result of error or something.
func (c *Multi[Res]) Cancel(ctx context.Context, coach int, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit(ctx, &c.lock, &c.cs[coach], err, nil)
}
