package batch_test

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"nikand.dev/go/batch"
)

func TestCoordinatorSmoke(tb *testing.T) {
	const N = 3
	ctx := context.Background()

	var sum, total int

	bc := batch.Coordinator[int]{
		CommitFunc: func(ctx context.Context) (int, error) {
			tb.Logf("commit %v", sum)
			total += sum

			return sum, nil
		},
	}

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < N; i++ {
				i := i

				func() {
					bc.Queue().In()

					runtime.Gosched()

					idx := bc.Enter(true)
					defer bc.Exit()

					tb.Logf("worker %2d  iter %2d  enters %2d", j, i, idx)

					if idx == 0 {
						sum = 0
					}

					sum += i

					res, err := bc.Commit(ctx)
					if err != nil {
						tb.Errorf("commit: %v", err)
					}

					tb.Logf("worker %2d  iter %2d  res %2d %v", j, i, res, err)
				}()
			}
		}()
	}

	wg.Wait()

	if exp := *jobs * N * (N - 1) / 2; exp != total {
		tb.Errorf("expected total %v  got %v", exp, total)
	}
}

func TestCoordinatorAllCases(tb *testing.T) {
	ctx := context.Background()

	var sum int
	var commitPanics bool

	bc := batch.Coordinator[int]{
		CommitFunc: func(ctx context.Context) (int, error) {
			if commitPanics {
				tb.Logf("commit PANICS")
				panic("commit PaNiC")
			}

			tb.Logf("commit %v", sum)
			return sum, nil
		},
	}

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

					idx := bc.Enter(j == 0)
					if idx < 0 {
						tb.Logf("worker %2d  iter %2d  didn't enter %2d", j, i, idx)
						return
					}

					defer bc.Exit()

					runtime.Gosched()

					if idx == 0 {
						tb.Logf(" * * * ")
						sum = 0
					}

					tb.Logf("worker %2d  iter %2d  enters %2d", j, i, idx)

					if j == 1 && i == 1 {
						tb.Logf("worker %2d  iter %2d  LEFT", j, i)
						return
					}

					sum += i

					if j == 1 && i == 2 {
						_, err := bc.Cancel(ctx, nil)
						tb.Logf("worker %2d  iter %2d  CANCEL %v", j, i, err)
						return
					}

					if j == 1 && i == 3 {
						tb.Logf("worker %2d  iter %2d  PANICS", j, i)
						panic("pAnIc")
					}

					if j == 1 {
						commitPanics = i == 4
					}

					if j == 1 && i == 5 {
						bc.Trigger()
					}

					res, err := bc.Commit(ctx)
					if err != nil {
						_ = err
					}

					if pe, ok := batch.AsPanicError(err); ok {
						tb.Logf("worker %2d  iter %2d  panic  %v", j, i, pe)
					} else {
						tb.Logf("worker %2d  iter %2d  res %2d %v", j, i, res, err)
					}
				}()
			}
		}()
	}

	wg.Wait()
}

func BenchmarkCoordinator(tb *testing.B) {
	tb.ReportAllocs()

	ctx := context.Background()

	var sum int
	var bc batch.Coordinator[int]

	bc.Init(func(ctx context.Context) (int, error) {
		return sum, nil
	})

	tb.RunParallel(func(tb *testing.PB) {
		for tb.Next() {
			func() {
				bc.Queue().In()

				//	runtime.Gosched()

				idx := bc.Enter(true)
				defer bc.Exit()

				//	tb.Logf("worker %2d  iter %2d  enters %2d", j, i, idx)

				if idx == 0 {
					sum = 0
				}

				sum += 1

				res, err := bc.Commit(ctx)
				if err != nil {
					//	tb.Errorf("commit: %v", err)
					_ = err
				}

				//	tb.Logf("worker %2d  iter %2d  res %2d %v", j, i, res, err)

				_ = res
			}()
		}
	})
}
