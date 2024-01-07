package batch_test

import (
	"context"
	"flag"
	"sync"

	"nikand.dev/go/batch"
)

var jobs = flag.Int("jobs", 5, "parallel jobs")

type Service struct {
	sum int // state we collect to commit together

	bc *batch.Coordinator[int] // result value type, set to struct{} if don't need it
}

func NewService() *Service {
	s := &Service{}

	s.bc = batch.New(s.commit)

	return s
}

func (s *Service) commit(ctx context.Context) (int, error) {
	// suppose some heavy operation here
	// update the file or write to db

	return s.sum, nil
}

func (s *Service) DoWork(ctx context.Context, data int) (int, error) {
	s.bc.Queue().In() // let others know we are going to join

	_ = data // prepare data

	idx := s.bc.Enter(true) // true for blocking, false if we want to leave instead of waiting
	// if idx < 0 // we haven't entered the batch in non blocking mode
	defer s.bc.Exit() // it's like Mutex.Unlock. Must be called with defer to outlive panics

	if idx == 0 { // we are first in the batch, reset the state
		s.sum = 0
	}

	// return // leave the batch if we changed our mind

	s.sum += data // add our work to the batch

	// s.bc.Cancel(ctx, err) // cancel the whole batch if we spoilt it
	// return

	// only one of Leave/Cancel/Commit must be called and only once
	res, err := s.bc.Commit(ctx, false) // true to force batch to commit now, false to wait for others
	if err != nil {                     // batch failed, each worker in it will get the same error
		return 0, err
	}

	// if we are here, all of the workers have their work committed

	return res, nil
}

func ExampleCoordinator() {
	s := NewService()

	// let's spin up some workers
	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ctx := context.Background() // passed to commit function

			res, err := s.DoWork(ctx, 1)
			_, _ = res, err
		}()
	}

	wg.Wait()

	// Output:
}
