package singleflight_test

import (
	"flag"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"nikand.dev/go/batch/singleflight"
)

var jobs = flag.Int("jobs", 5, "parallel jobs in tests")

func TestSingleFlight(tb *testing.T) {
	var wg sync.WaitGroup
	var sf singleflight.Group[int]

	defer wg.Wait()

	worker := func(j int) {
		defer wg.Done()

		res, err := sf.Do(func() (int, error) {
			tb.Logf("doing work")

			runtime.Gosched()

			return 1, nil
		})

		tb.Logf("worker %3d got: %v %v", j, res, err)
	}

	wg.Add(*jobs)

	for j := range *jobs {
		go worker(j)
	}
}

func TestKeyedSingleFlight(tb *testing.T) {
	var wg sync.WaitGroup
	var sf singleflight.KeyedGroups[int, int]

	defer wg.Wait()

	worker := func(k, j int) {
		defer wg.Done()

		res, err := sf.Do(k, func() (int, error) {
			tb.Logf("doing work for key %3d", k)

			runtime.Gosched()

			return k, nil
		})

		tb.Logf("worker %3d got: %v %v", j, res, err)
	}

	N := 3 * *jobs

	wg.Add(N)

	for j := range N {
		go worker(j%3, j)
	}
}

func BenchmarkSingleFlight(tb *testing.B) {
	tb.ReportAllocs()

	var sf singleflight.Group[int]

	do := func() {
		res, err := sf.Do(func() (int, error) {
			return 1, nil
		})

		_, _ = res, err
	}

	tb.RunParallel(func(tb *testing.PB) {
		for tb.Next() {
			do()
		}
	})
}

func BenchmarkKeyedSingleFlight(tb *testing.B) {
	tb.ReportAllocs()

	var sf singleflight.KeyedGroups[int, int]

	do := func(key int) {
		res, err := sf.Do(key, func() (int, error) {
			runtime.Gosched()

			return key, nil
		})

		_, _ = res, err
	}

	tb.SetParallelism(10 * *jobs)

	var keys atomic.Int32

	tb.RunParallel(func(tb *testing.PB) {
		key := int(keys.Add(1)) % *jobs

		for tb.Next() {
			do(key)
		}
	})
}
