package kernel_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

type fakePub struct{ got []kernel.OutboxEvent }

func (f *fakePub) Publish(_ context.Context, ev kernel.OutboxEvent) error {
	f.got = append(f.got, ev)
	return nil
}

func TestPublisherInterface(t *testing.T) {
	var _ kernel.Publisher = (*fakePub)(nil)
}
