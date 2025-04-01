/*
Package batch makes batched operations easy.

Batch provides a safe and efficient mechanism to aggregate the results
of multiple parallel tasks into a single, potentially expensive, "commit" operation.
Batch ensures that each participating task either receives a successful result
if its contribution has been committed (possibly as part of a larger batch)
or an appropriate error otherwise.

# Logic for Controller

	                    -------------------------
	-> worker0 ------> | common cumulative state |            /--> worker0
	-> worker1 ------> | collected from multiple |           /---> worker1
	-> worker2 ------> | workers and committed   | -> result ----> worker2
	-> worker3 ------> | all at once             |           \---> worker3
	                    -------------------------

# Logic for Multi

Workers are distributed among multiple parallel batches (coaches), similar to multiple
coaches on a cable car. Each batch then operates independently in the same way as a
single Controller. This allows for concurrent commit operations across different batches,
improving throughput when the underlying commit operation supports parallelism (e.g.,
multiple database replicas). However, it is not beneficial for systems where the final
commit operation is inherently serial (e.g., a single embedded database).

	                    -------------------------
	              | -> | workers ditributed      |            /--> worker0
	-> worker0 -> | -> | among free coaches      | -> result  ---> worker1
	-> worker1 -> |     -------------------------
	-> worker2 -> |
	-> worker3 -> |     -------------------------
	              | -> | state is combined in a  |            /--> worker2
	              | -> | few independent coaches | -> result  ---> worker3
	                    -------------------------

# Full Example for Controller.

	bc.Queue().In() // Get in the queue to participate in the batch.

	// Prepare data for the commit operation.
	// Any operations independent of other workers in the batch should be done here.
	data := 1

	if leave() {
		bc.Queue().Out()  // Leave the queue if we don't need to participate.
		bc.Notify()      // Unblock waiting workers.
		return errNoNeed
	}

	idx := bc.Enter(true) // Set 'blocking' to true to wait, or false for non-blocking behavior.
	                     // Like Mutex.Lock, the order of entry for waiting goroutines is not guaranteed.
	if idx < 0 {        // If non-blocking Enter failed (batch is busy),
		return errBusy // the caller might retry later.
	}

	defer bc.Exit() // Ensure Exit is called to release the batch's resources.
	               // Using defer with Exit guarantees state consistency even if a panic occurs.

	// Critical section: Code between Enter and Exit has exclusive access to the batch's shared state.
	// Modify shared data safely within this block.
	// It's generally recommended to perform any long or computationally intensive
	// operations *before* calling Enter to minimize the time spent holding the lock.

	if idx == 0 { // If this worker is the first to enter the batch, initialize or reset the shared state.
		sum = 0
	}

	if full() {             // If the batch is full and we cannot add our data.
		bc.Trigger()       // Optionally trigger the commit of the current batch.
				          // This is a common pattern to flush the batch even if the current
				         // worker didn't add anything, allowing others to commit.
		return errRetry // Return and attempt to join a new batch later to retry.
	}

	sum += data // Add the current worker's contribution to the batch's shared data.

	if spoilt() {                          // If the shared state has become invalid and the commit should be aborted.
		_, err = bc.Cancel(ctx, causeErr) // Cancel the current batch. The Committer function will not be executed.
						                 // All workers currently in the Enter-Exit section for this batch
						                // will receive the provided `causeErr`. If `causeErr` is nil,
						               // a default `Canceled` error will be returned.
						              // The first worker to enter a new batch will typically be responsible
					                 // for any necessary cleanup of the shared state.
	}

	if calledSomeLib() {
		panic(p) // If a panic occurs, the batch recovers, re-raises it for this worker,
			    // and signals other waiting workers in the batch with a PanicError
			   // when their Commit call returns.
	}

	if urgent() {     // If an immediate commit of the current batch is desired.
		bc.Trigger() // Signal the batch to initiate the commit process.
				    // Workers that have already entered and contributed to this batch
				   // will have their work included in the current commit.
				  // Workers that have not yet entered will join a subsequent batch.
	}

	res, err := bc.Commit(ctx) // Commit and wait.
	                          // Last worker to Enter triggers Committer.
				             // All batch workers get the same `res` and `err`.

Batch is a safe wrapper around Controller.
It handles certain edge cases and ensures proper synchronization,
calling underlying Controller methods where appropriate and panicking
in scenarios that would lead to undefined behavior or data corruption.

Multi extends the Controller concept by providing N independent, parallel batches.
Consider a scenario with 3 database replicas; Multi allows distributing commit operations
across these replicas concurrently. Multi.Enter attempts to join the first available
batch (coach, representing a replica in this example) and returns the index of that batch.
The subsequent workflow (Exit, Trigger, Commit, Cancel) operates similarly to the Controller
but is scoped to the specific batch (coach) the worker entered. You can customize the
batch selection logic by providing a custom Multi.Balancer.

MultiBatch is a safe wrapper for Multi, similar to Batch for Controller.
*/
package batch
