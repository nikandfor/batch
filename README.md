[![Documentation](https://pkg.go.dev/badge/nikand.dev/go/batch)](https://pkg.go.dev/nikand.dev/go/batch?tab=doc)
[![Go workflow](https://github.com/nikandfor/batch/actions/workflows/go.yml/badge.svg)](https://github.com/nikandfor/batch/actions/workflows/go.yml)
[![CircleCI](https://circleci.com/gh/nikandfor/batch.svg?style=svg)](https://circleci.com/gh/nikandfor/batch)
[![codecov](https://codecov.io/gh/nikandfor/batch/tags/latest/graph/badge.svg)](https://codecov.io/gh/nikandfor/batch)
[![Go Report Card](https://goreportcard.com/badge/nikand.dev/go/batch)](https://goreportcard.com/report/nikand.dev/go/batch)
![GitHub tag (latest SemVer)](https://img.shields.io/github/v/tag/nikandfor/batch?sort=semver)

# batch

`batch` is a library to make concurrent work batcheable and reliable.
Each worker either has its work committed or gets an error.

> Hope is not a strategy. ([from Google SRE book](https://sre.google/sre-book/introduction/))

No more batch operations that add its data to a batch and go away hoping it would be committed.

This is all without timeouts, additional goroutines, allocations, and channels.

## How it works

* Each worker adds its work to a shared batch.
* If there are no more workers in the queue the last one executes commit, the others wait.
* Every worker in the batch gets the same result and error.

## Usage

```
// General pattern is
// Queue.In -> Enter -> defer Exit -> add work to the batch -> Commit/Cancel/panic/return

var sum int

bc := batch.Coordinator[int]{
	// Required
	Commit: func(ctx context.Context) (int, error) {
		// commit sum
		return sum, err
	},
}

for j := 0; j < N; j++ {
	go func(j int) {
		bc.Queue().In() // let others know we are going to join

		data := 1 // prepare data

		idx := bc.Enter(true)
		defer bc.Exit()

		if idx == 0 { // we are first in the batch, reset it
			sum = 0
		}

		sum += data // add data to the batch

		res, err := bc.Commit(ctx)
		if err != nil { // works the same as we had independent commit in each goroutine
			_ = err
		}

		// batching is transparent for worker
		_ = res // res is the sum returned by Commit
	}(j)
}
```

See the all available options in [the doc](https://pkg.go.dev/nikand.dev/go/batch).

`batch` is error- and panic-proof, meaning that user code can return an error or panic at any point.
However, once all workers have exited the batch, its state is reset.
External state, such as `sum` in this case, remains the caller's responsibility and must be kept consistent.

