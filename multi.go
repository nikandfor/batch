package batch

import (
	"context"
	"sync"
)

type (
	Multi[Res any] struct {
		cs []*Controller[Res]

		mu   sync.Mutex
		cond sync.Cond
	}
)

func NewMulti[Res any](n int, commit func(ctx context.Context, coach int) (Res, error)) *Multi[Res] {
	c := &Multi[Res]{
		cs: make([]*Controller[Res], n),
	}

	c.cond.L = &c.mu

	for i := range c.cs {
		c.cs[i] = &Controller[Res]{
			Coach:  i,
			Commit: commit,
			mcond:  &c.cond,
		}
	}

	return c
}

func (c *Multi[Res]) Enter(blocking bool) (b Batch[Res], coach, index int) {
	defer c.mu.Unlock()
	c.mu.Lock()

	for {
		for coach := range c.cs {
			b, idx := c.cs[coach].Enter(false)
			if idx >= 0 {
				return Batch[Res]{
					c:     b.c,
					state: b.state,
				}, coach, idx
			}
		}

		if !blocking {
			return Batch[Res]{}, -1, -1
		}

		c.cond.Wait()
	}
}
