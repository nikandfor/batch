package batch_test

import (
	"sync"
	"testing"

	"nikand.dev/go/batch"
)

func TestSemaphore(tb *testing.T) {
	s := batch.NewSemaphore(4)

	var wg sync.WaitGroup
	wg.Add(*jobs)

	for j := 0; j < *jobs; j++ {
		go func() {
			defer wg.Done()

			defer s.Exit()
			n := s.Enter()

			tb.Logf("%d routines in a zone", n)
		}()
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		s.Reset(2)
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()

		s.Reset(1)
	}()

	tb.Logf("%d of %d in zone", s.Len(), s.Cap())

	wg.Wait()
}

func TestSemaphoreNil(tb *testing.T) {
	var s *batch.Semaphore

	s.Reset(2)

	s.Enter()
	s.Exit()

	s.Len()
	s.Cap()
}
