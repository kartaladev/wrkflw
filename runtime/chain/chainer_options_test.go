package chain_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// TestChainerObservabilityOptionsHandleSuccessor constructs a Chainer with the
// full set of observability/clock/link options and drives one successful Handle
// so the option setters and the happy-path Handle branch are exercised.
func TestChainerObservabilityOptionsHandleSuccessor(t *testing.T) {
	starter := &recordingStarter{}
	links := kernel.NewMemChainLinkStore()
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{Def: fulfillmentDef(), Vars: ev.Result}, true
	}

	c, err := chain.NewChainer(starter, policy,
		chain.WithChainLinks(links),
		chain.WithClock(clock.System()),
		chain.WithChainLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		chain.WithChainTracerProvider(tracenoop.NewTracerProvider()),
		chain.WithChainMeterProvider(metricnoop.NewMeterProvider()),
	)
	require.NoError(t, err)

	ev := chain.ChainEvent{
		PredecessorID:            "p1",
		PredecessorDefinitionRef: model.Version("approval", 1),
		Outcome:                  kernel.OutcomeCompleted,
		Result:                   map[string]any{"orderID": "o-7"},
	}
	require.NoError(t, c.Handle(t.Context(), ev))
	require.Len(t, starter.calls, 1)
	assert.Equal(t, "fulfillment", starter.calls[0].def.ID)
}
