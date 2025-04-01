package singleflight

import (
	"sync"

	"nikand.dev/go/batch"
)

type (
	Group[Res any] struct {
		mu sync.Mutex

		flight[Res]
	}

	KeyedGroups[Key comparable, Res any] struct {
		mu sync.Mutex

		active map[Key]*flight[Res]

		pool map[*flight[Res]]struct{}
	}

	flight[Res any] struct {
		cond sync.Cond

		res Res
		err error

		cnt int
	}

	Func[Res any] = func() (Res, error)

	PanicError = batch.PanicError
)

func (g *Group[Res]) Do(f Func[Res]) (res Res, err error) {
	defer g.mu.Unlock()
	g.mu.Lock()

	return g.do(f, &g.mu)
}

func (g *flight[Res]) do(f func() (Res, error), mu *sync.Mutex) (res Res, err error) {
	if g.cond.L == nil {
		g.cond.L = mu
	}

	for g.cnt < 0 {
		g.cond.Wait()
	}

	defer func() {
		g.cond.Broadcast()

		g.cnt++

		if g.cnt != 0 {
			return
		}

		var zero Res

		g.res = zero
		g.err = nil
	}()

	g.cnt++

	if g.cnt != 1 {
		g.cond.Wait()

		return g.res, g.err
	}

	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}

			g.err = PanicError{Panic: p}

			panic(p)
		}()

		defer mu.Lock()
		mu.Unlock()

		g.res, g.err = f()
	}()

	if g.cnt < 0 {
		panic("singleflight: inconsistent state")
	}

	g.cnt = -g.cnt

	return g.res, g.err
}

func (g *KeyedGroups[Key, Res]) Do(key Key, f Func[Res]) (res Res, err error) {
	defer g.mu.Unlock()
	g.mu.Lock()

	if g.active == nil {
		g.active = map[Key]*flight[Res]{}
		g.pool = map[*flight[Res]]struct{}{}
	}

	s, ok := g.active[key]
	if !ok {
		s = g.get()
		g.active[key] = s
	}

	defer func() {
		if s.cnt != 0 {
			return
		}

		delete(g.active, key)
		g.pool[s] = struct{}{}
	}()

	return s.do(f, &g.mu)
}

func (g *KeyedGroups[Key, Res]) get() *flight[Res] {
	for s := range g.pool {
		delete(g.pool, s)

		return s
	}

	return &flight[Res]{}
}

func AsPanicError(err error) (PanicError, bool) {
	return batch.AsPanicError(err)
}
