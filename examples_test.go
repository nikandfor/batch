package batch_test

import (
	"context"
	"log"
	"sync"

	"nikand.dev/go/batch"
)

func ExampleController() {
	ctx := context.Background()

	var sum int

	b := batch.Controller[int]{
		Commit: func(ctx context.Context) (int, error) {
			log.Printf("commit sum %d", sum)

			return sum, nil
		},
	}

	var wg sync.WaitGroup

	for j := 0; j < 3; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			b, i := b.Enter()
			defer func() {
				i := b.Exit()

				log.Printf("job %2d  exit  index %2d", j, i)

				if i == 0 { // we are the last exited the batch. clean up
					sum = 0
				}
			}()

			log.Printf("job %2d  enter index %2d", j, i)

			if i == 0 { // we are the first entered the new batch. prepare
				sum = 0
			}

			sum++

			res, err := b.Commit(ctx)

			log.Printf("job %2d  res %v %v", j, res, err)
		}()
	}

	wg.Wait()

	// Output:
}
