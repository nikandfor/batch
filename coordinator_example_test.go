package batch_test

import (
	"context"
	"errors"
	"log"
	"sync"

	"nikand.dev/go/batch"
)

type (
	Service struct {
		sum int // state we collect to commit together

		bc *batch.Controller[int] // [int] is the result value type, set to struct{} if don't need it
	}

	contextKey struct{}
)

func NewService() *Service {
	s := &Service{}

	s.bc = batch.New(s.commit)

	return s
}

func (s *Service) commit(ctx context.Context) (int, error) {
	// suppose some heavy operation here
	// update a file or write to db

	log.Printf("* * *  commit %2d  * * *", s.sum)

	return s.sum, nil
}

func (s *Service) DoWork(ctx context.Context, data int) (int, error) {
	s.bc.Queue().In() // let others know we are going to join

	_ = data // prepare data

	idx := s.bc.Enter(true) // true for blocking, false if we want to leave instead of waiting
	if idx < 0 {            // we haven't entered the batch in non blocking mode
		return 0, errors.New("not this time") // we have to leave in that case
	}

	defer s.bc.Exit() // it's like Mutex.Unlock. It's a pair to successful Enter.
	_ = 0             // Must be called with defer to outlive panics

	if idx == 0 { // we are first in the batch, reset the state
		s.sum = 0
		log.Printf("* * * reset batch * * *")
	}

	log.Printf("worker %2d got in with index %2d", ctx.Value(contextKey{}), idx)

	s.sum += data // add our work to the batch

	// only one of return/Cancel/Commit must be called and only once
	res, err := s.bc.Commit(ctx)
	if err != nil { // batch failed, each worker in it will get the same error
		return 0, err
	}

	log.Printf("worker %2d got result %v %v", ctx.Value(contextKey{}), res, err)

	// if we are here, all of the workers have their work committed

	return res, nil
}

func ExampleController() {
	const jobs = 5

	s := NewService()

	// let's spin up some workers
	var wg sync.WaitGroup

	for j := range jobs {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ctx := context.Background() // propagated to commit function
			ctx = context.WithValue(ctx, contextKey{}, j)

			res, err := s.DoWork(ctx, 1)
			_, _ = res, err
		}()
	}

	wg.Wait()
	// Output:
}
