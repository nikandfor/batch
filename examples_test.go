package batch_test

import (
	"context"
	"flag"
	"log"
	"runtime"
	"sync"

	"nikand.dev/go/batch"
)

var coaches = flag.Int("coaches", 2, "parallel coaches")

func ExampleControllerBlocking() {
	ctx := context.Background()

	var sum int

	b := batch.Controller[int]{
		Commit: func(ctx context.Context, c int) (int, error) {
			log.Printf("commit sum %d", sum)

			runtime.Gosched() // do long blocking work here

			return sum, nil
		},
	}

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			var res int
			var err error

			b, i := b.Enter(true)
			defer func() {
				i := b.Exit()

				log.Printf("job %2d  exit  index %2d  res %v %v", j, i, res, err)
			}()

			log.Printf("job %2d  enter index %2d", j, i)

			if i == 0 { // we are the first entered the new batch. prepare
				sum = 0
			}

			sum++

			res, err = b.Commit(ctx)
		}()
	}

	wg.Wait()

	// Output:
}

func ExampleControllerNonBlocking() {
	ctx := context.Background()

	var sum int

	b := batch.Controller[int]{
		Commit: func(ctx context.Context, c int) (int, error) {
			log.Printf("commit sum %d", sum)

			runtime.Gosched() // do long blocking work here

			return sum, nil
		},
	}

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			var res int
			var err error

			b, i := b.Enter(false)
			if i < 0 {
				log.Printf("job %2d  left %2d", j, i)
				return
			}

			defer func() {
				i := b.Exit()

				log.Printf("job %2d  exit  index %2d  res %v %v", j, i, res, err)
			}()

			log.Printf("job %2d  enter index %2d", j, i)

			if i == 0 { // we are the first entered the new batch. prepare
				sum = 0
			}

			sum++

			if j == 1 {
				res, err = b.Rollback(ctx, nil)
				return
			}

			res, err = b.Commit(ctx)
		}()
	}

	wg.Wait()

	// Output:
}

func ExampleMultiBlocking() {
	ctx := context.Background()

	sum := make([]int, *coaches)

	b := batch.NewMulti[int](len(sum), func(ctx context.Context, coach int) (int, error) {
		log.Printf("commit coach %2d  sum %d", coach, sum[coach])

		runtime.Gosched() // do long blocking work here

		return sum[coach], nil
	})

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			var res int
			var err error

			b, coach, i := b.Enter(true)
			defer func() {
				i := b.Exit()

				log.Printf("job %2d  exit  index %2d/%2d  res %v %v", j, coach, i, res, err)
			}()

			log.Printf("job %2d  enter index %2d/%2d", j, coach, i)

			if i == 0 { // we are the first entered the new batch. prepare
				sum[coach] = 0
			}

			sum[coach]++
			runtime.Gosched() // you shouldn't do long blocking work here, but you can

			res, err = b.Commit(ctx)
		}()
	}

	wg.Wait()

	// Output:
}

func ExampleMultiNonBlocking() {
	ctx := context.Background()

	sum := make([]int, *coaches)

	b := batch.NewMulti[int](len(sum), func(ctx context.Context, coach int) (int, error) {
		log.Printf("commit coach %2d  sum %d", coach, sum[coach])

		runtime.Gosched() // do long blocking work here

		return sum[coach], nil
	})

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			var res int
			var err error

			b, coach, i := b.Enter(false)
			if coach < 0 {
				log.Printf("job %2d  left", j)

				return
			}

			defer func() {
				i := b.Exit()

				log.Printf("job %2d  exit  index %2d/%2d  res %v %v", j, coach, i, res, err)
			}()

			log.Printf("job %2d  enter index %2d/%2d", j, coach, i)

			if i == 0 { // we are the first entered the new batch. prepare
				sum[coach] = 0
			}

			sum[coach]++
			runtime.Gosched() // you shouldn't do long blocking work here, but you can

			res, err = b.Commit(ctx)
		}()
	}

	wg.Wait()

	// Output:
}
