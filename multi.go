package batch

import (
	"context"
)

type (
	// Multi manages multiple parallel batches, like multiple coaches on a cable car.
	// It allows workers to join and commit to different batches concurrently,
	// improving throughput when the underlying commit operation supports parallelism
	// (e.g., multiple database replicas).
	// It is not beneficial for systems where the final commit operation is inherently serial
	// (e.g., a single embedded database).
	// Multi instances should be created with NewMulti or initialized with Init, not as literals.
	Multi[Res any] struct {
		// Committer is invoked to commit the `coach` state.
		//
		// Called within a critical section. Do not call other Multi methods here.
		//
		// Set Committer OR use Multi.CommitFunc.
		Committer CommitMultiFunc[Res]

		lock // protects everything below

		// Balancer selects an available batch (coach) or signals that none are available.
		// bitset represents the set of available batches.
		// Batch `n` is available if bitset has the n-th bit set (bitset & (1<<n) != 0).
		// The Multi code interprets the Balancer's return value:
		// If non-negative, it attempts to use that batch.
		// If negative OR the selected batch is not actually available,
		// Multi treats it as if no batches are available,
		// either returning immediately in non-blocking mode or blocking until a batch gets free.
		Balancer  func(bitset uint64) int
		available uint64

		// BigBalancer is used when more than 64 batches (coaches) are needed.
		// Batch `n` is available if the n-th bit is set in the bitset slice
		// (bitset[n/64] & (1<<(n%64)) != 0).
		BigBalancer  func(bitset []uint64) int
		bigavailable []uint64

		cs []coach[Res]
	}

	CommitMultiFunc[Res any] = func(ctx context.Context, coach int) (Res, error)
)

// NewMulti creates a new Multi controller managing `n` parallel batches.
func NewMulti[Res any](n int, f CommitMultiFunc[Res]) *Multi[Res] {
	c := &Multi[Res]{}

	c.Init(n, f)

	return c
}

// Init initializes the Multi controller with `n` parallel batches.
// Can also be used to reset the Multi, but not concurrently with active operations.
func (c *Multi[Res]) Init(n int, f CommitMultiFunc[Res]) {
	c.Committer = f
	c.cs = make([]coach[Res], n)
	c.cond.L = &c.mu
}

// Queue returns a shared queue for all batches (coaches).
// Joining this queue means that currently running batches will wait
// for this worker before finalizing their commit.
//
// Workers can leave the Queue before entering a batch, but Notify must be called
// to signal waiting workers in that case.
func (c *Multi[Res]) Queue() *Queue {
	return &c.lock.queue
}

// Notify signals the Multi controller that it can proceed with the commit,
// as this worker that intended to join will not.
// Must be called if a worker leaves the Queue before entering a batch.
func (c *Multi[Res]) Notify() {
	c.cond.Broadcast()
}

// Enter attempts to join an available batch.
// Returns (-1, -1) if blocking is false and no batches are available.
// Enter also removes the worker from the shared queue.
//
// Must be paired with a call to Exit. See the documentation for Controller.Enter for more details.
//
// The batch (coach) selection can be customized by setting a custom Multi.Balancer.
func (c *Multi[Res]) Enter(blocking bool) (coach, idx int) {
	c.mu.Lock()

	c.queue.Out()

	for {
		coach = c.selectCoach()

		if coach >= 0 && c.cs[coach].cnt >= 0 {
			c.cs[coach].cnt++

			return coach, c.cs[coach].cnt - 1
		}

		if !blocking {
			c.mu.Unlock()
			c.cond.Broadcast()

			return -1, -1
		}

		c.cond.Wait()
	}
}

func (c *Multi[Res]) selectCoach() (coach int) {
	switch {
	case c.Balancer != nil && len(c.cs) <= 64:
		return c.balancerCoach()
	case c.BigBalancer != nil:
		return c.bigBalancerCoach()
	case c.Balancer != nil:
		panic("balancer only supports up to 64 coaches")
	default:
		return c.firstCoach()
	}
}

func (c *Multi[Res]) firstCoach() (coach int) {
	for coach := range c.cs {
		if c.cs[coach].cnt >= 0 {
			return coach
		}
	}

	return -1
}

func (c *Multi[Res]) balancerCoach() (coach int) {
	c.available = 0

	for coach := range c.cs {
		if c.cs[coach].cnt < 0 {
			continue
		}

		c.available |= 1 << coach
	}

	return c.Balancer(c.available)
}

func (c *Multi[Res]) bigBalancerCoach() (coach int) {
	if c.bigavailable == nil {
		c.bigavailable = make([]uint64, (len(c.cs)+63)/64)
	}

	for i := range c.bigavailable {
		c.bigavailable[i] = 0
	}

	for coach := range c.cs {
		if c.cs[coach].cnt < 0 {
			continue
		}

		i, j := coach/64, coach%64

		c.bigavailable[i] |= 1 << j
	}

	return c.BigBalancer(c.bigavailable)
}

// Exit the specified batch. Should be called with defer.
// Similar to Mutex.Unlock, it releases access to the shared resources of that batch.
func (c *Multi[Res]) Exit(coach int) int {
	defer func() {
		c.mu.Unlock()
		c.cond.Broadcast()
	}()

	return c.cs[coach].exit()
}

// Trigger the specified batch to commit.
// Must be called inside Enter-Exit section.
//
// Can be called before or after adding data to the batch.
// The commit is initiated when this worker calls Commit or Exit.
// If calling Exit, the worker may get into the Queue and Enter again to retry the batch.
func (c *Multi[Res]) Trigger(coach int) {
	c.cs[coach].trigger = true
}

// Commit waits for all pending workers in the specified batch to contribute to its state.
// It then triggers the Multi's Committer function once for that batch with the complete state
// and returns the resulting shared value to all waiting workers in that batch.
func (c *Multi[Res]) Commit(ctx context.Context, coach int) (Res, error) {
	return commit(ctx, &c.lock, &c.cs[coach], nil, func(ctx context.Context) (Res, error) {
		return c.Committer(ctx, coach)
	})
}

// CommitFunc for a specific batch behaves like Commit for that batch
// but uses the provided function `f` instead of the Multi's Committer.
// All workers participating in the same batch must consistently use
// either Commit or CommitFunc with the same `f`.
func (c *Multi[Res]) CommitFunc(ctx context.Context, coach int, f CommitMultiFunc[Res]) (Res, error) {
	return commit(ctx, &c.lock, &c.cs[coach], nil, func(ctx context.Context) (Res, error) {
		return f(ctx, coach)
	})
}

// Cancel aborts the current batch for the specified coach and returns the provided error
// to all workers that have already added their data to that batch.
// The Multi's Committer is not called for this batch.
// Workers waiting to Enter the critical section are not affected.
func (c *Multi[Res]) Cancel(ctx context.Context, coach int, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit(ctx, &c.lock, &c.cs[coach], err, nil)
}
