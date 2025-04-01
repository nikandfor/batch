package batch

import "context"

type (
	MultiBatch[Res any] struct {
		c *Multi[Res]

		coach int

		noCopy noCopy //nolint:unused
		state  byte
	}
)

func ByMulti[Res any](c *Multi[Res]) MultiBatch[Res] {
	return MultiBatch[Res]{
		c: c,
	}
}

func QueueInMulti[Res any](c *Multi[Res]) MultiBatch[Res] {
	c.queue.In()

	return MultiBatch[Res]{
		c:     c,
		state: stateQueued,
	}
}

func (b *MultiBatch[Res]) QueueIn() int {
	if b.state != stateNew {
		panic(usage)
	}

	b.state = stateQueued

	return b.c.queue.In()
}

func (b *MultiBatch[Res]) Enter(blocking bool) (coach, idx int) {
	switch b.state {
	case stateNew:
		b.QueueIn()
	case stateQueued:
	default:
		panic(usage)
	}

	coach, idx = b.c.Enter(blocking)
	if idx >= 0 {
		b.state = stateEntered
	} else {
		b.state = stateNew
	}

	b.coach = coach

	return coach, idx
}

func (b *MultiBatch[Res]) Trigger() {
	b.c.Trigger(b.coach)
}

func (b *MultiBatch[Res]) Cancel(ctx context.Context, err error) (Res, error) {
	if b.state != stateEntered {
		panic(usage)
	}

	b.state = stateCommitted

	return b.c.Cancel(ctx, b.coach, err)
}

func (b *MultiBatch[Res]) Commit(ctx context.Context) (Res, error) {
	return b.CommitFunc(ctx, b.c.Committer)
}

func (b *MultiBatch[Res]) CommitFunc(ctx context.Context, f CommitMultiFunc[Res]) (Res, error) {
	if b.state != stateEntered {
		panic(usage)
	}

	b.state = stateCommitted

	return b.c.CommitFunc(ctx, b.coach, f)
}

func (b *MultiBatch[Res]) Exit() int {
	idx := -1

	switch b.state {
	case stateNew:
	case stateQueued:
		b.c.queue.Out()
		b.c.Notify()
	case stateEntered, stateCommitted:
		idx = b.c.Exit(b.coach)
	default:
		panic(usage)
	}

	b.state = stateExited

	return idx
}
