/*
batch made easy.

batch is a safe and efficient way to combine results of multiple parallel tasks
into one heavy "commit" (save) operation.
batch ensures each task is either receive successful result if its results
have been committed (possibly as a parth of a batch) or an error in other case.

Logic for Coordinator

	                    -------------------------
	-> worker0 ------> | common cumulative state |            /--> worker0
	-> worker1 ------> | collected from multiple | -> result  ---> worker1
	-> worker2 ------> | workers and committed   |            \--> worker2
	-> worker3 ------> | all at once             |             \-> worker3
	                    -------------------------

# Logic for Multi

Workers are distributed among multiple coaches and then each coach works the same as in Coordinator case.

	                    -------------------------
	              | -> | state is combined in a  |            /--> worker0
	-> worker0 -> | -> | few independent coaches | -> result  ---> worker1
	-> worker1 -> |     -------------------------
	-> worker2 -> |
	-> worker3 -> |     -------------------------
	              | -> | each is committed as an |            /--> worker2
	              | -> | independent part        | -> result  ---> worker3
	                    -------------------------

Full Guide

	bc.Queue().In() // get in the queue

	// Get data ready for the commit.
	// All the blocking operations should be done here.
	data := 1

	if leave() {
		bc.Queue().Out() // get out of the queue
		bc.Notify()      // notify waiting goroutines we won't come
		return
	}

	idx := bc.Enter(true) // true to block and wait, false for non-blocking return if batch is in the process of commiting
	                     // just like with the Mutex there are no guaranties how will enter the first
	if idx < 0 {        // in non-blocking mode we didn't enter the batch, it's always >= 0 in blocking mode
		return         // no need to do anything here
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

	if full() {       // if we didn't change the state we can just leave.
		bc.Trigger() // Optionally we can trigger the batch.
		            // Common use case is to flush the data if we won't fit.
		return     // Then we may return and retry in the next batch.
	}

	sum += data // adding our work to the batch

	if spoilt() {
		_, err = bc.Cancel(ctx, causeErr) // cancel the batch, commit isn't done, all get causeErr
		                                 // if causeErr is nil it's set to Canceled
	}

	if failed() {
		panic("we can safely panic here") // panic will be propogated to the caller,
		                                 // other goroutines in the batch will get PanicError
	}

	if urgent() {
		bc.Trigger() // do not wait for the others
	}

	res, err := bc.Commit(ctx) // get ready to commit.
	                          // The last goroutine entered the batch calls the actual commit.
	                         // All the others wait and get the same res and error.

Batch is a safe wrapper around Coordinator.
It will call missed methods if that makes sense or it will panic otherwise.

Multi is a coordinator with N available parallel batches.
Suppose you have 3 db replicas so you can distribute load across them.
Multi.Enter will enter the first free coach (replica in our example) and return its index.
The rest is similar. Custom logic for choosing coach can be used by setting Multi.Balancer.

MultiBatch is a safe wrapper around Multi just like Batch wraps Coordinator.
*/
package batch
