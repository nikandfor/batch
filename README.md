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
* If there are no more workers ready to commit the last one runs commit, the others wait.
* Every worker in the batch gets the same result and error.

## Usage

```go
var tx int

b := batch.New(func(ctx context.Context) (interface{}, error) {
	// commit tx
	return res, err
})

// Optional hooks
b.Prepare = func(ctx context.Context) error { tx = 0; return nil }     // called in the beginning on a new batch
b.Rollback = func(ctx context.Context, err error) error { return err } // if any worker returned error
b.Panic = func(ctx context.Context, p interface{}) error {             // any worker panicked
	return batch.PanicError{Panic: p} // returned to other workes
	                                  // panicked worker gets the panic back
}

// only one of Panic, Rollback, and Commit is called (in respective priority order; panic wins, then error, commit is last)

for j := 0; j < N; j++ {
	go func(j int) {
		ctx := context.WithValue(ctx, workerID{}, j) // can be accessed in Commit and other hooks

		res, err := b.Do(ctx, func(ctx context.Context) error {
			tx += j // add work to the batch

			return nil // commit
		})
		if err != nil { // works the same as we had independent commit in each goroutine
			_ = err
		}

		// batching is transparent for worker
		_ = res
	}(j)
}
```

Batch is error and panic proof which means any callback (Do, Commit, and friends) may return error or panic,
but as soon as all workers left the batch its state is restored.
But not the external state, it's callers responsibility to keep it consistent.
