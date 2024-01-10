package batch

import "context"

type (
	Batch[Res any] struct {
		c *Coordinator[Res]

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

	usage = "BatchIn -> defer Exit -> [QueueIn] -> Enter -> Cancel/Commit/return"
)

func By[Res any](c *Coordinator[Res]) Batch[Res] {
	return Batch[Res]{
		c: c,
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
	if b.state != stateEntered {
		panic(usage)
	}

	b.state = stateCommitted

	return b.c.Commit(ctx)
}

func (b *Batch[Res]) Exit() int {
	idx := -1

	switch b.state {
	case stateNew:
	case stateQueued:
		b.c.queue.Out()
		b.c.Notify()
	case stateEntered,
		stateCommitted:
		idx = b.c.Exit()
	default:
		panic(usage)
	}

	b.state = stateExited

	return idx
}
