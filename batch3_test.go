package batch_test

import (
	"context"
	"errors"
	"flag"
	"runtime"
	"sync"
	"testing"
	"time"

	"nikand.dev/go/batch"
)

var jobs = flag.Int("jobs", 10, "parallel workers")

func TestBatch(tb *testing.T) {
	b := batch.Controller[int]{}

	type testCase struct {
		Name        string
		Error       error
		Panic       interface{}
		CommitError error
		CommitPanic interface{}
		SkipCommit  bool
		Rollback    bool
		Index       int
	}

	for _, tc := range []testCase{
		{Name: "smoke"},
		{Name: "error", Error: errors.New("test err"), Index: 3},
		{Name: "rollback", Rollback: true, Error: errors.New("test err"), Index: 3},
		{Name: "panic", Panic: "pAnIc", Index: 3},
		{Name: "commitError", CommitError: errors.New("commit err"), Index: 3},
		{Name: "commitPanic", CommitPanic: "commit PaNiC", Index: 3},
		{Name: "skipCommit", SkipCommit: true, Index: 3},
	} {
		tc := tc

		tb.Run(tc.Name, func(tb *testing.T) {
			ctx := context.Background()

			var sum int

			b.Commit = func(ctx context.Context, _ int) (int, error) {
				if tc.CommitError != nil && sum >= tc.Index {
					return sum, tc.CommitError
				}
				if tc.CommitPanic != nil && sum >= tc.Index {
					panic(tc.CommitPanic)
				}

				tb.Logf("commit %v", sum)

				return sum, nil
			}

			var wg sync.WaitGroup

			wg.Add(*jobs)

			for j := 0; j < *jobs; j++ {
				j := j

				go func() {
					defer wg.Done()

					b, idx := b.Enter(true)

					defer func() {
						p := recover()

						if p != nil {
							tb.Logf("worker %2v | panic %v", j, p)
						}

						if tc.Panic != nil && idx == tc.Index && p != tc.Panic {
							tb.Errorf("worker %2v | panic %v  wanted %v", j, p, tc.Panic)
						}
					}()

					defer b.Exit()

					if idx == 0 {
						tb.Logf("****")
						sum = 0
					}

					sum++

					tb.Logf("worker %2v | index %2v  sum %v", j, idx, sum)

					var res interface{}
					var err error

					switch {
					case tc.Rollback:
						_, err = b.Rollback(ctx, nil)
					case tc.Error != nil && sum == tc.Index:
						_, err = b.Rollback(ctx, tc.Error)
					case tc.Panic != nil && sum == tc.Index:
						panic(tc.Panic)
					case tc.SkipCommit && sum == tc.Index:
					default:
						res, err = b.Commit(ctx)
					}

					tb.Logf("worker %2v | result %v (%v)  %v", j, res, sum, err)

					wantError := func(exp error) {
						if exp != nil && sum >= tc.Index && err != exp {
							tb.Errorf("worker %2v | error %v  wanted %v", j, err, exp)
						}
					}
					wantError(tc.Error)
					wantError(tc.CommitError)

					_ = res
				}()
			}

			wg.Wait()

			tb.Logf("all done")
		})
	}

	tb.Run("doubleTrigger", func(tb *testing.T) {
		ctx := context.Background()

		var reached, reached2 bool

		defer func() {
			if !reached || reached2 {
				tb.Errorf("this is bad: %v %v", reached, reached2)
			}

			if p := recover(); p == nil {
				tb.Errorf("expected panic")
			}
		}()

		b.Commit = func(ctx context.Context, _ int) (int, error) {
			return 1, nil
		}

		b, _ := b.Enter(true)
		defer b.Exit()

		res, err := b.Commit(ctx)
		if err != nil || res != 1 {
			tb.Errorf("res %v %v", res, err)
		}

		reached = true

		_, _ = b.Commit(ctx)

		reached2 = true
	})

	tb.Run("LowerAPI", func(tb *testing.T) {
		b.Commit = func(ctx context.Context, _ int) (int, error) {
			return 0, nil
		}

		tb.Run("BatchExit", func(tb *testing.T) {
			b := b.Batch()
			defer b.Exit()
		})

		tb.Run("AllUsed", func(tb *testing.T) {
			b := b.Batch()
			defer b.Exit()

			b.QueueUp()

			b.Enter(true)

			_, err := b.Commit(context.Background())
			if err != nil {
				tb.Errorf("commit: %v", err)
			}
		})

		tb.Run("QueueSkipped", func(tb *testing.T) {
			b := b.Batch()
			defer b.Exit()

			//	b.QueueUp()

			b.Enter(true)

			_, err := b.Commit(context.Background())
			if err != nil {
				tb.Errorf("commit: %v", err)
			}
		})
	})

	tb.Run("LowerAPIMisuse", func(tb *testing.T) {
		b.Commit = func(ctx context.Context, _ int) (int, error) {
			return 0, nil
		}

		type testCase struct {
			Name           string
			SkipQueue      bool
			DoubleQueue    bool
			DoubleEnter    bool
			NoEnter        bool
			CommitRollback bool
			DoubleExit     bool
		}

		for _, tc := range []testCase{
			//	{Name: "SkipQueueUp", SkipQueue: true}, // it's fine now
			{Name: "DoubleQueue", DoubleQueue: true},
			{Name: "DoubleEnter", DoubleEnter: true},
			{Name: "NoEnter", NoEnter: true},
			{Name: "CommitRollback", CommitRollback: true},
			//	{Name: "DoubleExit", DoubleExit: true}, // it's fine now
		} {
			tc := tc

			tb.Run(tc.Name, func(tb *testing.T) {
				defer func() {
					p := recover()
					if p == nil {
						tb.Errorf("expected panic")
					}
				}()

				b := b.Batch()
				defer b.Exit()

				if !tc.SkipQueue {
					b.QueueUp()
				}
				if tc.DoubleQueue {
					b.QueueUp()
				}

				if !tc.NoEnter {
					b.Enter(true)
				}
				if tc.DoubleEnter {
					b.Enter(true)
				}

				_, err := b.Commit(context.Background())
				if err != nil {
					tb.Errorf("commit: %v", err)
				}

				if tc.CommitRollback {
					_, err = b.Rollback(context.Background(), nil)
					_ = err
				}

				if tc.DoubleExit {
					b.Exit()
				}
			})
		}
	})

	tb.Run("okAfterAll", func(tb *testing.T) {
		const N = 100

		ctx := context.Background()

		var sum int

		b.Commit = func(ctx context.Context, _ int) (int, error) {
			runtime.Gosched()
			return sum, nil
		}

		var wg sync.WaitGroup

		wg.Add(*jobs)

		for j := 0; j < *jobs; j++ {
			go func() {
				defer wg.Done()

				for i := 0; i < N; i++ {
					func() {
						b, idx := b.Enter(true)
						defer b.Exit()

						if idx == 0 {
							sum = 0
						}

						runtime.Gosched()
						sum++
						runtime.Gosched()

						_, _ = b.Commit(ctx)
					}()
				}
			}()
		}

		wg.Wait()
	})
}

func TestBatchNonBlocking(tb *testing.T) {
	ctx := context.Background()

	var sum int

	b := batch.Controller[int]{
		Commit: func(ctx context.Context, c int) (int, error) {
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

			b := b.Batch()

			i := b.Enter(false)
			if i < 0 {
				return
			}

			defer b.Exit()

			if i == 0 { // we are the first entered the new batch. prepare
				sum = 0
			}

			sum++

			if j == 1 {
				_, _ = b.Rollback(ctx, nil)
				return
			}

			_, _ = b.Commit(ctx)
		}()
	}

	wg.Wait()
}

func TestDeadlock(tb *testing.T) {
	ctx := context.Background()

	var sum int

	b := batch.Controller[int]{
		Commit: func(ctx context.Context, c int) (int, error) {
			//	log.Printf("commit sum %d", sum)

			return sum, nil
		},
	}

	var wg sync.WaitGroup

	for j := 0; j < *jobs; j++ {
		j := j
		wg.Add(1)

		go func() {
			defer wg.Done()

			if j == 1 {
				b := b.Batch()
				b.QueueUp()
				time.Sleep(10 * time.Millisecond)
				b.Exit()
				return
			}

			b, _ := b.Enter(true)
			defer b.Exit()

			_, _ = b.Commit(ctx)
		}()
	}

	wg.Wait()
}
