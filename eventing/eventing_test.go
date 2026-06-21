package eventing_test

import (
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

type fakePub struct{ published int }

func (f *fakePub) Publish(_ string, _ ...*message.Message) error { f.published++; return nil }
func (f *fakePub) Close() error                                  { return nil }

func TestNewPublisherReturnsWorkingRuntimePublisher(t *testing.T) {
	fp := &fakePub{}
	var pub runtime.Publisher = eventing.NewPublisher(fp)
	err := pub.Publish(t.Context(), runtime.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, DedupKey: "i:1:0",
	})
	require.NoError(t, err)
	require.Equal(t, 1, fp.published)
}
