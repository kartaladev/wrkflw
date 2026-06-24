package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// ChainEvent is the broker-agnostic input to a chaining decision, projected from
// a terminal outbox event (ADR-0045). The watermill adapter (eventing) builds it
// from the message topic + metadata + body; the runtime core never sees
// watermill.
type ChainEvent struct {
	// PredecessorID is the instance that reached a terminal state.
	PredecessorID string
	// PredecessorDefinitionRef is the "defID:version" of the instance that reached a terminal
	// state, carried end-to-end through the outbox by the built-in publisher/relay
	// (ADR-0047) so a SuccessorPolicy can route on the predecessor's definition. It
	// is empty only for events produced before ADR-0047 or by a consumer pipeline
	// that does not set the "def" metadata key.
	PredecessorDefinitionRef string
	// Outcome is the terminal outcome that fired the event.
	Outcome Outcome
	// Result is the event payload: the terminal variables (completed) or
	// {"error": …} (failed/terminated).
	Result map[string]any
}

// SuccessorDecision is what a SuccessorPolicy decides to start. A nil Def means
// "no successor" (the chain ends).
type SuccessorDecision struct {
	Def  *model.ProcessDefinition
	Vars map[string]any
}

// SuccessorPolicy decides the successor for a terminal predecessor. ok=false (or
// a nil Def) ends the chain. v1 is a Go callback; a declarative (expr-driven)
// ruleset is a deferred follow-up that plugs in here.
type SuccessorPolicy func(ctx context.Context, ev ChainEvent) (SuccessorDecision, bool)

// InstanceStarter is the minimal seam the Chainer core needs to start a
// successor. *Runner satisfies it (Run). Kept narrow so the core is
// unit-testable without a full Runner.
type InstanceStarter interface {
	Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error)
}

// Chainer is the broker-agnostic process-instance chaining core (ADR-0045). It
// applies a SuccessorPolicy to a terminal ChainEvent and, when a successor is
// decided, records the lineage link then starts the successor as a fresh,
// independent root instance. It is idempotent and safe under at-least-once
// delivery.
type Chainer struct {
	starter InstanceStarter
	policy  SuccessorPolicy
	links   ChainLinkStore // optional; nil disables lineage recording (deterministic-id idempotency only)
	clk     clock.Clock
	tel     observability.Telemetry
	started metric.Int64Counter
}

// ChainerOption configures a Chainer.
type ChainerOption func(*chainerConfig)

type chainerConfig struct {
	links   ChainLinkStore
	clk     clock.Clock
	obsOpts []observability.Option
}

// WithChainLinks sets the durable ChainLinkStore. When set, a hop is recorded
// before the successor starts, and an already-recorded (PredecessorID, Outcome)
// is the exactly-once backstop (the start is skipped). When unset, idempotency
// rests on the deterministic successor id + Store.Create's ErrInstanceExists.
func WithChainLinks(links ChainLinkStore) ChainerOption {
	return func(c *chainerConfig) { c.links = links }
}

// WithChainClock sets the clock used to stamp ChainLink.CreatedAt.
// Default: clock.System().
func WithChainClock(clk clock.Clock) ChainerOption {
	return func(c *chainerConfig) { c.clk = clk }
}

// WithChainLogger sets the structured logger. Default: slog.Default().
func WithChainLogger(l *slog.Logger) ChainerOption {
	return func(c *chainerConfig) { c.obsOpts = append(c.obsOpts, observability.WithLogger(l)) }
}

// WithChainTracerProvider sets the OTel TracerProvider for the chain span.
// Default: the OTel global provider.
func WithChainTracerProvider(tp trace.TracerProvider) ChainerOption {
	return func(c *chainerConfig) { c.obsOpts = append(c.obsOpts, observability.WithTracerProvider(tp)) }
}

// WithChainMeterProvider sets the OTel MeterProvider for the chain counter.
// Default: the OTel global provider.
func WithChainMeterProvider(mp metric.MeterProvider) ChainerOption {
	return func(c *chainerConfig) { c.obsOpts = append(c.obsOpts, observability.WithMeterProvider(mp)) }
}

// NewChainer constructs a Chainer over starter and policy. It panics if either
// is nil — a Chainer is unusable without both. Pass WithChainLinks to enable
// durable lineage + the DB-level exactly-once backstop.
func NewChainer(starter InstanceStarter, policy SuccessorPolicy, opts ...ChainerOption) *Chainer {
	if starter == nil {
		panic("workflow-runtime: NewChainer: starter must not be nil")
	}
	if policy == nil {
		panic("workflow-runtime: NewChainer: policy must not be nil")
	}
	cfg := chainerConfig{clk: clock.System()}
	for _, o := range opts {
		o(&cfg)
	}
	tel := observability.New(chainerInstrumentationName, cfg.obsOpts...)
	return &Chainer{
		starter: starter,
		policy:  policy,
		links:   cfg.links,
		clk:     cfg.clk,
		tel:     tel,
		started: tel.Int64Counter("wrkflw_chain_started_total", "Successor process instances started by chaining."),
	}
}

const chainerInstrumentationName = "github.com/zakyalvan/krtlwrkflw/runtime/chainer"

// successorID is the deterministic id of the successor for one (predecessor,
// outcome) hop. Determinism is the first idempotency layer: a redelivered
// terminal event computes the same id, so Store.Create rejects the second start
// with ErrInstanceExists even when no ChainLinkStore is configured.
func successorID(predecessorID string, outcome Outcome) string {
	return predecessorID + "-next-" + string(outcome)
}

// Handle applies the policy to one ChainEvent and, if a successor is decided,
// records the lineage link then starts the successor. It is idempotent and safe
// under at-least-once delivery:
//
//   - policy declines (ok=false / nil Def)        -> nil (chain ends)
//   - link already recorded (ErrChainLinkExists)  -> nil (already chained)
//   - successor already started (ErrInstanceExists) -> nil (already started)
//   - transient store/start failure               -> error (propagated => retry)
func (c *Chainer) Handle(ctx context.Context, ev ChainEvent) error {
	ctx, span := c.tel.Tracer.Start(ctx, "wrkflw.chain.handle", trace.WithAttributes(
		attribute.String("wrkflw.predecessor_id", ev.PredecessorID),
		attribute.String("wrkflw.outcome", string(ev.Outcome)),
	))
	defer span.End()

	dec, ok := c.policy(ctx, ev)
	if !ok || dec.Def == nil {
		span.SetAttributes(attribute.Bool("wrkflw.chain.successor", false))
		return nil
	}

	id := successorID(ev.PredecessorID, ev.Outcome)
	span.SetAttributes(
		attribute.Bool("wrkflw.chain.successor", true),
		attribute.String("wrkflw.successor_id", id),
	)

	if c.links != nil {
		link := ChainLink{
			PredecessorID:            ev.PredecessorID,
			PredecessorDefinitionRef: ev.PredecessorDefinitionRef,
			Outcome:                  ev.Outcome,
			SuccessorID:              id,
			SuccessorDefinitionRef:   defRef(dec.Def),
			StartVars:                dec.Vars,
			CreatedAt:                c.clk.Now(),
		}
		switch err := c.links.Record(ctx, link); {
		case errors.Is(err, ErrChainLinkExists):
			// The link was recorded by a prior delivery, but that delivery's start
			// may have failed AFTER the link was written. Do NOT return here — fall
			// through and (re)attempt the start. Store.Create's ErrInstanceExists
			// makes a genuine duplicate a clean no-op, so this recovers a lost
			// successor without ever double-starting. Recording the link is intent;
			// the successor's existence is the real exactly-once backstop.
			c.tel.Logger.DebugContext(ctx, "chain: hop already recorded; ensuring successor started",
				slog.String("predecessor_id", ev.PredecessorID), slog.String("outcome", string(ev.Outcome)))
		case err != nil:
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("workflow-runtime: chain record link %q: %w", id, err)
		}
	}

	switch _, err := c.starter.Run(ctx, dec.Def, id, dec.Vars); {
	case errors.Is(err, ErrInstanceExists):
		c.tel.Logger.DebugContext(ctx, "chain: successor already started; skipping",
			slog.String("successor_id", id))
		return nil
	case err != nil:
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("workflow-runtime: chain start successor %q: %w", id, err)
	}

	c.started.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", string(ev.Outcome))))
	c.tel.Logger.InfoContext(ctx, "chain: started successor",
		slog.String("predecessor_id", ev.PredecessorID),
		slog.String("outcome", string(ev.Outcome)),
		slog.String("successor_id", id))
	return nil
}

// defRef renders a "defID:version" reference for a definition.
func defRef(def *model.ProcessDefinition) string {
	return fmt.Sprintf("%s:%d", def.ID, def.Version)
}
