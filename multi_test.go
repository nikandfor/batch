package batch_test

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"nikand.dev/go/batch"
)

func TestMulti(tb *testing.T) {
	ctx := context.Background()

	sum := make([]int, *coaches)

	b := batch.NewMulti[int](len(sum), func(ctx context.Context, coach int) (int, error) {
		runtime.Gosched() // do long blocking work here

		return sum[coach], nil
	})

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			blocking := j%2 == 0

			b, coach, i := b.Enter(blocking)
			if coach < 0 {
				return
			}

			defer b.Exit()

			if i == 0 { // we are the first entered the new batch. prepare
				sum[coach] = 0
			}

			sum[coach]++
			runtime.Gosched() // you shouldn't do long blocking work here, but you can

			_, _ = b.Commit(ctx)
		}()
	}

	wg.Wait()
}
