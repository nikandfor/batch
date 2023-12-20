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

```
var tx int

bc := batch.Controller{
	// Required
	Commit: func(ctx context.Context) (interface{}, error) {
		// commit tx
		return res, err
	},
}

for j := 0; j < N; j++ {
	go func(j int) {
		ctx := context.WithValue(ctx, workerID{}, j) // can be accessed in Controller.Commit

		b := bc.Enter()
		defer b.Exit()

		if b.Index() == 0 { // we are first in batch, reset it
			tx = 0
		}

		tx++ // add work to the batch

		res, err := b.Commit(ctx)
		if err != nil { // works the same as we had independent commit in each goroutine
			_ = err
		}

		// batching is transparent for worker
		_ = res
	}(j)
}
```

Lower level API allows queue up in advance, before actually entering batch.
This can be used instead of waiting for timeout for other workers to come.
Instead workers declare itself and now they may be a bit late.

```
b := bc.Batch()
defer b.Exit() // should be called with defer to outlive panics

b.QueueUp() // now we will be waited for.

// prepare data
x := 3

b.Enter() // enter syncronized section

// add data to a common batch
tx += x

_, _ = b.Commit(ctx)

// we are still in syncronized section until Exit is called
```

`Controller.Enter` is a shortcut for `Controller.Batch, Batch.QueueUp, Batch.Enter`.

Batch is error and panic proof which means the user code can return error or panic in any place,
but as soon as all workers left the batch its state is reset.
But not the external state, it's callers responsibility to keep it consistent.
