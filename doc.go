/*
batch made easy.

batch is a safe and efficient way to combine results of multiple parallel tasks
into one heavy "commit" (save) operation.
batch ensures each task is either receive successful result if its results
have been committed (possibly as a parth of a batch) or an error in other case.

# Logic for Coordinator

	                    -------------------------
	-> worker0 ------> | common cumulative state |            /--> worker0
	-> worker1 ------> | collected from multiple | -> result  ---> worker1
	-> worker2 ------> | workers and committed   |            \--> worker2
	-> worker3 ------> | all at once             |             \-> worker3
	                    -------------------------

# Logic for Multi

Workers are distributed among multiple coaches and then each coach works the same as in Coordinator case.

	                    -------------------------
	              | -> | workers are ditributed  |            /--> worker0
	-> worker0 -> | -> | among free coaches      | -> result  ---> worker1
	-> worker1 -> |     -------------------------
	-> worker2 -> |
	-> worker3 -> |     -------------------------
	              | -> | state is combined in a  |            /--> worker2
	              | -> | few independent coaches | -> result  ---> worker3
	                    -------------------------

# Full Guide

	bc.Queue().In() // get in the queue

	// Get data ready for the commit.
	// All the independent from other workers operations should be done here.
	data := 1

	if leave() {
		bc.Queue().Out() // Get out of the queue.
		bc.Notify()      // Notify waiting goroutines we won't come.
		return errNoNeed
	}

	idx := bc.Enter(true) // true to block and wait, false for non-blocking return if batch is in the process of commiting.
	                     // Just like with the Mutex there are no guaranties who will enter first.
	if idx < 0 {        // In non-blocking mode we didn't Enter the batch, it's always >= 0 in blocking mode.
		return errBusy // No need to do anything here.
	}

	defer bc.Exit() // if we entered we must exit
	_ = 0          // calling it with defer ensures state consistency in case of panic

	// We are inside locked Mutex between Enter and Exit.
	// So the shared state can be modified safely.
	// That also means that all the long and heavy operations
	// should be done before Enter.

	if idx == 0 { // we are first in the batch, reset the state
		sum = 0
	}

	if full() {            // if we didn't change the state we can just leave.
		bc.Trigger()      // Optionally we can trigger the batch.
		                 // Common use case is to flush the data if we won't fit.
		return errRetry // Then we may return and retry in the next batch.
	}

	sum += data // adding our work to the batch

	if spoilt() {						   // If we spoilt the satate and want to abort commit
		_, err = bc.Cancel(ctx, causeErr) // cancel the batch. Commit isn't done, all workers get causeErr.
		                                 // If causeErr is nil it's set to Canceled.
	}

	if failed() {                          // Suppose some library panicked here.
		panic("we can safely panic here") // Panic will be propogated to the caller,
		                                 // other goroutines in the batch will get PanicError.
	}

	if urgent() {     // If we want to commit immideately
		bc.Trigger() // trigger it.
		            // Workers already added their job will be committed too.
				   // Workers haven't entered the batch will go to the next one.
	}

	res, err := bc.Commit(ctx) // Call Commit.
	                          // The last goroutine entered the batch calls the actual Coordinator.Commit.
	                         // All the others wait and get the same res and error.

Batch is a safe wrapper around Coordinator.
It will call missed methods if that makes sense or panic otherwise.

Multi is a coordinator with N available parallel batches.
Suppose you have 3 db replicas so you can distribute load across them.
Multi.Enter will enter the first free coach (replica in our example) and return its index.
The rest is similar to Coordinator. Custom logic for choosing coach can be used by setting Multi.Balancer.

MultiBatch is a safe wrapper around Multi just like Batch wraps Coordinator.
*/
package batch
