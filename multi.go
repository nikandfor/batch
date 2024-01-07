package batch

import (
	"context"
)

type (
	Multi[Res any] struct {
		CommitFunc func(ctx context.Context, coach int) (Res, error)

		Balancer  func(available []uint64) int
		available []uint64

		locs

		cs []coach[Res]
	}
)

func NewMulti[Res any](n int, f func(ctx context.Context, coach int) (Res, error)) *Multi[Res] {
	return &Multi[Res]{
		CommitFunc: f,

		cs: make([]coach[Res], n),
	}
}

func (c *Multi[Res]) Init(n int, f func(ctx context.Context, coach int) (Res, error)) {
	c.CommitFunc = f
	c.cs = make([]coach[Res], n)
}

func (c *Multi[Res]) Queue() *Queue {
	return &c.locs.queue
}

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

func (c *Multi[Res]) Trigger(coach int) {
	c.cs[coach].trigger = true
}

func (c *Multi[Res]) Commit(ctx context.Context, coach int) (Res, error) {
	return commit(ctx, &c.locs, &c.cs[coach], nil, func(ctx context.Context) (Res, error) {
		return c.CommitFunc(ctx, coach)
	})
}

func (c *Multi[Res]) Cancel(ctx context.Context, coach int, err error) (Res, error) {
	if err == nil {
		err = Canceled
	}

	return commit(ctx, &c.locs, &c.cs[coach], err, nil)
}
