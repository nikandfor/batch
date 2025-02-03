package batch_test

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"nikand.dev/go/batch"
)

type (
	ServiceMulti struct {
		sum []int // state we collect to commit together

		bc *batch.Multi[int] // [int] is the result value type, set to struct{} if don't need it
	}
)

func NewServiceMulti(coaches int) *ServiceMulti {
	s := &ServiceMulti{
		sum: make([]int, coaches),
	}

	s.bc = batch.NewMulti(coaches, s.commit)

	return s
}

func (s *ServiceMulti) commit(ctx context.Context, coach int) (int, error) {
	// suppose some heavy operation here
	// update a file or write to db

	log.Printf("* * *  coach: %2d, commit %2d  * * *", coach, s.sum[coach])

	time.Sleep(time.Millisecond) // let other workers to use another coach

	return s.sum[coach], nil
}

func (s *ServiceMulti) DoWork(ctx context.Context, data int) (int, error) {
	s.bc.Queue().In() // let others know we are going to join

	_ = data // prepare data

	coach, idx := s.bc.Enter(true) // true for blocking, false if we want to leave instead of waiting
	if idx < 0 {                   // we haven't entered the batch in non blocking mode
		return 0, errors.New("not this time") // we have to leave in that case
	}

	defer s.bc.Exit(coach) // it's like Mutex.Unlock. It's a pair to successful Enter.
	_ = 0                  // Must be called with defer to outlive panics

	if idx == 0 { // we are first in the batch, reset the state
		s.sum[coach] = 0
		log.Printf("* * * coach %2d, reset batch * * *", coach)
	}

	log.Printf("worker %2d got into coach %2d with index %2d", ctx.Value(contextKey{}), coach, idx)

	if data == 0 { // if we didn't spoil the batch state
		return 0, nil // we can leave freely
	}

	s.sum[coach] += data // add our work to the batch

	if s.sum[coach] >= 3 { // if batch is already big
		s.bc.Trigger(coach) // trigger it
	}

	// only one of return/Cancel/Commit must be called and only once
	res, err := s.bc.Commit(ctx, coach)
	if err != nil { // batch failed, each worker in it will get the same error
		return 0, err
	}

	log.Printf("worker %2d got result for coach %2d: %v %v", ctx.Value(contextKey{}), coach, res, err)

	// if we are here, all of the workers have their work committed

	return res, nil
}

func ExampleMulti() {
	const jobs = 5

	s := NewServiceMulti(2)

	// let's spin up some workers
	var wg sync.WaitGroup

	for j := 0; j < jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			ctx := context.Background() // passed to commit function
			ctx = context.WithValue(ctx, contextKey{}, j)

			res, err := s.DoWork(ctx, 1)
			_, _ = res, err
		}()
	}

	wg.Wait()
	// Output:
}
