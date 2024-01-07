package batch_test

import (
	"context"
	"math/bits"
	"runtime"
	"sync"
	"testing"

	"nikand.dev/go/batch"
)

func TestMulti(tb *testing.T) {
	ctx := context.Background()

	var sum [2]int
	var commitPanics [2]bool

	bc := batch.NewMulti(len(sum), func(ctx context.Context, coach int) (int, error) {
		if commitPanics[coach] {
			tb.Logf("commit PANICS")
			panic("commit PaNiC")
		}

		runtime.Gosched()

		tb.Logf("coach %2d  commit %2d", coach, sum[coach])
		return sum[coach], nil
	})

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < 7; i++ {
				i := i

				func() {
					if j == 1 && i == 3 {
						defer func() {
							_ = recover()
						}()
					}

					bc.Queue().In()

					runtime.Gosched()

					coach, idx := bc.Enter(j == 0)
					if idx < 0 {
						tb.Logf("worker %2d  iter %2d  didn't enter %2d/%2d", j, i, coach, idx)
						return
					}

					defer bc.Exit(coach)

					runtime.Gosched()

					if idx == 0 {
						tb.Logf("coach %2d  * * * ", coach)
						sum[coach] = 0
					}

					tb.Logf("coach %2d  worker %2d  iter %2d  enters %2d", coach, j, i, idx)

					if j == 1 && i == 1 {
						tb.Logf("coach %2d  worker %2d  iter %2d  LEFT", coach, j, i)
						return
					}

					sum[coach] += i

					if j == 1 && i == 2 {
						_, err := bc.Cancel(ctx, coach, nil)
						tb.Logf("coach %2d  worker %2d  iter %2d  CANCEL %v", coach, j, i, err)
						return
					}

					if j == 1 && i == 3 {
						tb.Logf("coach %2d  worker %2d  iter %2d  PANICS", coach, j, i)
						panic("pAnIc")
					}

					if j == 1 {
						commitPanics[coach] = i == 4
					}

					if j == 1 && i == 5 {
						bc.Trigger(coach)
					}

					res, err := bc.Commit(ctx, coach)
					if err != nil {
						_ = err
					}

					if pe, ok := batch.AsPanicError(err); ok {
						tb.Logf("coach %2d  worker %2d  iter %2d  panic  %v", coach, j, i, pe)
					} else {
						tb.Logf("coach %2d  worker %2d  iter %2d  res %2d %v", coach, j, i, res, err)
					}
				}()
			}
		}()
	}

	wg.Wait()
}

func BenchmarkMulti(tb *testing.B) {
	const N = 8

	ctx := context.Background()

	var sum [N]int
	var bc batch.Multi[int]

	bc.Init(N, func(ctx context.Context, coach int) (int, error) {
		return sum[coach], nil
	})

	run := func(tb *testing.PB) {
		for tb.Next() {
			func() {
				bc.Queue().In()

				//	runtime.Gosched()

				coach, idx := bc.Enter(true)
				defer bc.Exit(coach)

				//	tb.Logf("worker %2d  iter %2d  enters %2d", j, i, idx)

				if idx == 0 {
					sum[coach] = 0
				}

				sum[coach] += 1

				res, err := bc.Commit(ctx, coach)
				if err != nil {
					//	tb.Errorf("commit: %v", err)
					_ = err
				}

				//	tb.Logf("worker %2d  iter %2d  res %2d %v", j, i, res, err)

				_ = res
			}()
		}
	}

	tb.Run("Default_8", func(tb *testing.B) {
		tb.ReportAllocs()
		bc.Balancer = nil

		tb.RunParallel(run)
	})

	tb.Run("Balancer_8", func(tb *testing.B) {
		tb.ReportAllocs()
		bc.Balancer = func(x []uint64) int {
			return bits.Len64(x[0]) - 1 // choose the highest number
		}

		tb.RunParallel(run)
	})
}
