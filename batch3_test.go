package batch_test

import (
	"context"
	"errors"
	"flag"
	"sync"
	"testing"

	"nikand.dev/go/batch"
)

var jobs = flag.Int("jobs", 10, "parallel workers")

func TestBatch(tb *testing.T) {
	b := batch.Controller{}

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

			b.Commit = func(ctx context.Context) (interface{}, error) {
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

					b := b.Enter()
					idx := b.Index()

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

					if b.Index() == 0 {
						tb.Logf("****")
						sum = 0
					}

					sum++

					tb.Logf("worker %2v | index %2v  sum %v", j, b.Index(), sum)

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

		b.Commit = func(ctx context.Context) (interface{}, error) {
			return 1, nil
		}

		b := b.Enter()
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
		b.Commit = func(ctx context.Context) (interface{}, error) {
			return nil, nil
		}

		b := b.Batch()

		b.QueueUp()

		defer b.Exit()
		b.Enter()

		_, err := b.Commit(context.Background())
		if err != nil {
			tb.Errorf("commit: %v", err)
		}
	})

	tb.Run("LowerAPIMisuse", func(tb *testing.T) {
		b.Commit = func(ctx context.Context) (interface{}, error) {
			return nil, nil
		}

		type testCase struct {
			Name           string
			SkipQueue      bool
			DoubleQueue    bool
			NoEnter        bool
			CommitRollback bool
			DoubleExit     bool
		}

		for _, tc := range []testCase{
			{Name: "SkipQueueUp", SkipQueue: true},
			{Name: "DoubleQueue", DoubleQueue: true},
			{Name: "NoEnter", NoEnter: true},
			{Name: "CommitRollback", CommitRollback: true},
			{Name: "DoubleExit", DoubleExit: true},
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
					b.Enter()
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
}