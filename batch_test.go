package batch_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sync"
	"testing"

	"nikand.dev/go/batch"
)

var jobs = flag.Int("jobs", 10, "parallel workers")

func TestBatch(tb *testing.T) {
	ctx := context.Background()

	var commits, rollbacks, panics int
	var bucket string

	b := batch.New(func(ctx context.Context) (interface{}, error) {
		commits++

		return bucket, nil
	})

	b.Prepare = func(ctx context.Context) error {
		bucket = ""

		return nil
	}

	b.Rollback = func(ctx context.Context, err error) error {
		rollbacks++

		return err
	}

	b.Panic = func(ctx context.Context, p interface{}) error {
		panics++

		return batch.PanicError{Panic: p}
	}

	var fail func() error
	var expErr error
	var expPanic, prepPanic, panicPanic interface{}

	body := func(tb *testing.T) {
		commits, rollbacks, panics = 0, 0, 0
		bucket = ""

		var wg sync.WaitGroup
		wg.Add(*jobs)

		for j := 0; j < *jobs; j++ {
			j := j

			go func() {
				defer wg.Done()
				defer func() {
					p := recover()

					if p != nil {
						tb.Logf("worker %2x got panic %v", j, p)
					}

					if j == 1 && p != expPanic && p != prepPanic && p != panicPanic {
						tb.Errorf("worker %2x panicked %v, expected %v", j, p, expPanic)
					}
				}()

				//ctx := context.WithValue(ctx, "j", j)

				res, err := b.Do(ctx, func(ctx context.Context) error {
					if j == 1 && fail != nil {
						return fail()
					}

					bucket += fmt.Sprintf(" %x", j)

					return nil
				})

				if err != nil {
					tb.Logf("worker %2x got common error %v", j, err)
				} else {
					tb.Logf("worker %2x got common result %v", j, res)
				}

				switch {
				case expPanic != nil && err == batch.PanicError{Panic: expPanic}:
				case prepPanic != nil && err == batch.PanicError{Panic: prepPanic}:
				case panicPanic != nil && err == batch.PanicError{Panic: panicPanic}:
				case j == 1 && err != expErr:
					tb.Errorf("got error %v, expected %v", err, expErr)
				}
			}()
		}

		wg.Wait()

		tb.Logf("commits %v  rollbacks %v  panics %v", commits, rollbacks, panics)
	}

	tb.Run("ok", body)

	expErr = errors.New("test fail")

	fail = func() error {
		return expErr
	}

	tb.Run("fail", body)

	expPanic = "test panic"
	expErr = nil

	fail = func() error {
		panic(expPanic)
	}

	tb.Run("panic", body)

	expPanic = nil
	prepPanic = "prepare panic"

	b.Prepare = func(ctx context.Context) error {
		panic(prepPanic)
	}

	tb.Run("PreparePanic", body)

	expPanic = "before panic panic"
	panicPanic = "panic panic"

	b.Prepare = nil
	b.Panic = func(ctx context.Context, p interface{}) error {
		panics++
		panic(panicPanic)
	}

	tb.Run("PanicPanic", body)

	fail = nil
	expPanic = nil

	tb.Run("okAfter", body)
}

func BenchmarkBatch(tb *testing.B) {
	tb.ReportAllocs()

	var commits, sum int

	b := batch.New(func(ctx context.Context) (interface{}, error) {
		commits++
		sum = 0

		return nil, nil
	})

	ctx := context.Background()

	tb.RunParallel(func(tb *testing.PB) {
		for tb.Next() {
			_, _ = b.Do(ctx, func(context.Context) error {
				sum++
				return nil
			})
		}
	})
}
