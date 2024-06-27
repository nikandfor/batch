package batch

import "sync"

type (
	// Semaphore is a classical semaphore synchronization primitive.
	// All methods can be safely called on nil Semaphore.
	// They will do nothing like you have unlimited semaphore.
	Semaphore struct {
		mu   sync.Mutex
		cond sync.Cond

		n, lim int
	}
)

// NewSemaphore creates a new semaphore with capacity of n.
func NewSemaphore(n int) *Semaphore {
	b := &Semaphore{}

	b.Reset(n)

	return b
}

// Reset resets semaphore capacity.
// But not the current value, which means it can be used
// to update limit on the fly, but it can't be used to reset
// inconsistent semaphore.
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

// Enter critical section.
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

// Exit from critical section.
func (b *Semaphore) Exit() {
	if b == nil {
		return
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	b.n--
	b.cond.Signal()
}

// Len is a number of tasks in the critical section.
func (b *Semaphore) Len() int {
	if b == nil {
		return 0
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	return b.n
}

// Cap is a semaphore capacity.
func (b *Semaphore) Cap() int {
	if b == nil {
		return 0
	}

	defer b.mu.Unlock()
	b.mu.Lock()

	return b.lim
}
