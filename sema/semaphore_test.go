package sema_test

import (
	"flag"
	"sync"
	"testing"

	"nikand.dev/go/batch/sema"
)

var jobs = flag.Int("jobs", 5, "parallel jobs in tests")

func TestSemaphore(tb *testing.T) {
	s := sema.NewSemaphore(4)

	var wg sync.WaitGroup
	wg.Add(*jobs)

	for j := 0; j < *jobs; j++ {
		go func() {
			defer wg.Done()

			defer s.Exit()
			n, b := s.Enter()

			tb.Logf("%d routines in a zone, was blocked %v", n, b)
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
	var s *sema.Semaphore

	s.Reset(2)

	s.Enter()
	s.Exit()

	s.Len()
	s.Cap()
}
