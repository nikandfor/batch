package batch

import (
	"context"
)

type (
	// Multi is a coordinator to choose and access multiple coaches.
	Multi[Res any] struct {
		// CommitFunc is called to commit result of coach number coach.
		CommitFunc func(ctx context.Context, coach int) (Res, error)

		// Balancer chooses replica among available or it can choose to wait for more.
		// bitset is a set of available coaches.
		// Coach n is available <=> bitset[n/64] & (1<<(n%64)) != 0.
		// If n is >= 0 and the coach is available it proceeds with it.
		// If it's not Enter blocks or returns -1, -1 according to Enter argument.
		Balancer  func(bitset []uint64) int
		available []uint64

		lock

		cs []coach[Res]
	}
)

// NewMulti create new Multi coordinator with n parallel coaches.
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

// Queue returns common queue for entering coaches.
// Getting into it means already started batches will wait for it not committing yet.
//
// Worker can leave the Queue, but it must call Notify to wake up waiting workers.
func (c *Multi[Res]) Queue() *Queue {
	return &c.lock.queue
}

// Notify wakes up waiting workers. Can be used if you were waited,
// but now situation has changed.
func (c *Multi[Res]) Notify() {
	c.cond.Broadcast()
}

// Enter enters available batch.
// It will return -1, -1 if no coaches available and blocking == false.
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

// Exit exits the batch. Should be called with defer.
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

// Trigger triggers the batch to commit.
// Must be called inside Enter-Exit section.
//
// Can be used to flush current batch and retry.
// Commit only happens when current worker Exits from the section.
// So you need to Exit and then get into the Queue and Enter again to retry.
func (c *Multi[Res]) Trigger(coach int) {
	c.cs[coach].trigger = true
}

// Commit indicates the current worker is done with the shared data and ready to Commit it.
// Commit blocks until batch is committed. The same Res is returned to all the workers in the batch.
// (Res, error) is what the Multi.Commit returns.
func (c *Multi[Res]) Commit(ctx context.Context, coach int) (Res, error) {
	return commit(ctx, &c.lock, &c.cs[coach], nil, func(ctx context.Context) (Res, error) {
		return c.CommitFunc(ctx, coach)
	})
}

// Cancel indicates the current worked is done with shared data but it can't be committed.
// All the workers from the same batch receive zero Res and the same error.
//
// It can be used if batch shared state have been spoilt as a result of error or something.
func (c *Multi[Res]) Cancel(ctx context.Context, coach int, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit(ctx, &c.lock, &c.cs[coach], err, nil)
}
