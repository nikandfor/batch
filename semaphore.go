package batch

import "sync"

type (
	Semaphore struct {
		mu   sync.Mutex
		cond sync.Cond

		n, lim int
	}
)

func NewSemaphore(n int) *Semaphore {
	b := &Semaphore{}

	b.Reset(n)

	return b
}

func (b *Semaphore) Reset(n int) {
	if b == nil {
		return
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	if b.cond.L == nil {
		b.cond.L = &b.mu
	}

	b.lim = n
}

func (b *Semaphore) Enter() int {
	if b == nil {
		return 0
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	for b.n >= b.lim {
		b.cond.Wait()
	}

	b.n++

	return b.n
}

func (b *Semaphore) Exit() {
	if b == nil {
		return
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	b.n--
	b.cond.Signal()
}

func (b *Semaphore) Len() int {
	if b == nil {
		return 0
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	return b.n
}

func (b *Semaphore) Cap() int {
	if b == nil {
		return 0
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	return b.lim
}
