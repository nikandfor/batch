//go:build ignore

package batch_test

import (
	"context"
	"fmt"
	"sync"

	"nikand.dev/go/batch"
)

type (
	DB struct {
		b  batch.Batch
		tx *Tx

		mu sync.Mutex // for unbatched operations
	}

	Tx struct {
		updates []int
	}
)

func New() *DB {
	d := &DB{}

	d.b.Prepare = d.prepare
	d.b.Commit = d.commit

	return d
}

func (d *DB) prepare(ctx context.Context) error {
	if d.tx == nil {
		d.tx = &Tx{}
	}

	d.tx.Reset()

	return nil
}

func (d *DB) commit(ctx context.Context) (interface{}, error) {
	// commit changes
	response := fmt.Sprintf("%v", d.tx.updates)

	return response, nil // each goroutine in the batch get this response and error
}

func (d *DB) SaveUnbatchedParallel(ctx context.Context, data int) error {
	tx := &Tx{} // new tx/connection

	tx.updates = append(tx.updates, data) // add one value

	// commit: make heavy work for each portion of data
	// discard allocated resources

	return nil
}

func (d *DB) SaveUnbatchedSyncronized(ctx context.Context, data int) error {
	defer d.mu.Unlock()
	d.mu.Lock()

	err := d.prepare(ctx)
	if err != nil {
		return err
	}

	d.tx.updates = append(d.tx.updates, data) // add one value

	res, err := d.commit(ctx) // commit: make heavy work for each portion of data
	if err != nil {
		return err
	}

	_ = res

	return nil
}

func (d *DB) SaveBatched(ctx context.Context, data int) error {
	// the same result, but all goroutines committed their data in a single shared batch

	response, err := d.b.Do(ctx, func(ctx context.Context) error {
		// access to common resources is syncronized
		d.tx.updates = append(d.tx.updates, data)

		return nil
	})
	// each goroutine only returns after commit (or rollback) is finished

	_ = response // only one commit is done: the same result is shared

	return err // shared error
}

func (tx *Tx) Reset() {
	tx.updates = tx.updates[:0]
}

func ExampleBatch() {
	ctx := context.Background()
	svc := New()

	const M = 3

	var wg sync.WaitGroup

	wg.Add(M)

	for j := 0; j < M; j++ {
		go func() {
			defer wg.Done()

			err := svc.SaveBatched(ctx, 2)
			_ = err
		}()
	}

	wg.Wait()
}
