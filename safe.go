package batch

import "context"

type (
	Batch[Res any] struct {
		c *Controller[Res]

		noCopy noCopy
		state  byte
	}

	noCopy struct{}
)

const (
	stateNew = iota
	stateQueued
	stateEntered
	stateCommitted
	stateExited = stateNew

	usage = "By -> defer Exit -> [QueueIn] -> Enter -> Cancel/Commit/return"
)

func MakeBatch[Res any](c *Controller[Res]) Batch[Res] {
	return Batch[Res]{
		c: c,
	}
}

func QueueIn[Res any](c *Controller[Res]) Batch[Res] {
	c.queue.In()

	return Batch[Res]{
		c:     c,
		state: stateQueued,
	}
}

func (b *Batch[Res]) QueueIn() int {
	if b.state != stateNew {
		panic(usage)
	}

	b.state = stateQueued

	return b.c.queue.In()
}

func (b *Batch[Res]) Enter(blocking bool) int {
	switch b.state {
	case stateNew:
		b.QueueIn()
	case stateQueued:
	default:
		panic(usage)
	}

	idx := b.c.Enter(blocking)
	if idx >= 0 {
		b.state = stateEntered
	} else {
		b.state = stateNew
	}

	return idx
}

func (b *Batch[Res]) Trigger() {
	b.c.Trigger()
}

func (b *Batch[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if b.state != stateEntered {
		panic(usage)
	}

	b.state = stateCommitted

	return b.c.Cancel(ctx, err)
}

func (b *Batch[Res]) Commit(ctx context.Context) (Res, error) {
	return b.CommitFunc(ctx, b.c.Committer)
}

func (b *Batch[Res]) CommitFunc(ctx context.Context, f CommitFunc[Res]) (Res, error) {
	if b.state != stateEntered {
		panic(usage)
	}

	b.state = stateCommitted

	return b.c.CommitFunc(ctx, f)
}

func (b *Batch[Res]) Exit() int {
	return b.ExitErr(nil)
}

func (b *Batch[Res]) ExitErr(errp *error) (idx int) {
	idx = -1
	s := b.state
	b.state = stateExited

	switch s {
	case stateNew:
	case stateQueued:
		b.c.queue.Out()
		b.c.Notify()
	case stateEntered, stateCommitted:
		idx = b.c.ExitErr(errp)
	default:
		panic(usage)
	}

	return idx
}

func (noCopy) Lock()   {}
func (noCopy) Unlock() {}
