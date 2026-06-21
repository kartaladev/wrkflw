package runtime_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

type fakePub struct{ got []runtime.OutboxEvent }

func (f *fakePub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	f.got = append(f.got, ev)
	return nil
}

func TestPublisherInterface(t *testing.T) {
	var _ runtime.Publisher = (*fakePub)(nil)
}
