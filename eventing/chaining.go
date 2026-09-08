package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/chain"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// isBenignDriverShutdown reports whether err is (or wraps) [kernel.ErrDriverShuttingDown]
// — the ProcessDriver refusing new work during graceful shutdown. Such a chain handler
// failure is benign: the nack correctly redelivers the terminal event so the successor
// starts once the driver is back, so it is logged at DEBUG rather than ERROR.
func isBenignDriverShutdown(err error) bool {
	return errors.Is(err, kernel.ErrDriverShuttingDown)
}

// envelopeTopic resolves the topic an envelope was routed on. Envelope.Topic is
// the field for it; MetaTopic is the same value repeated inside the metadata, so
// a consumer whose broker flattens many topics onto one subscription — and who
// therefore builds envelopes without a Topic — still routes correctly.
func envelopeTopic(env Envelope) string {
	if env.Topic != "" {
		return env.Topic
	}
	return env.Metadata[MetaTopic]
}

// NewChainHandler adapts the broker-agnostic runtime.Chainer core to a [Handler].
// A consumer mounts it on their own broker subscription (their retry/poison/DLQ
// middleware wraps it), registering it for the three terminal topics. It projects
// each envelope to a runtime.ChainEvent:
//
//   - the topic ([TopicInstanceCompleted] / [TopicInstanceFailed] /
//     [TopicInstanceTerminated]) → Outcome
//   - [MetaInstanceID] → PredecessorID
//   - [MetaDefinitionRef] → PredecessorDefinitionRef (set by the built-in
//     publisher from the source instance's "defID:version"; empty for events
//     written by an older version)
//   - the JSON body → Result
//
// Ack/Nack discipline (a returned error nacks for re-delivery):
//
//   - success / no-op (no successor, duplicate) → nil (ack)
//   - non-terminal / unknown topic              → nil (ack, ignored)
//   - malformed JSON body                        → nil (ack + log; never loop)
//   - transient core failure                     → error (nack → re-delivered)
func NewChainHandler(core *chain.Chainer, opts ...Option) Handler {
	logger := newOptions(opts...).logger
	return func(ctx context.Context, env Envelope) error {
		topic := envelopeTopic(env)
		outcome, ok := chainTopics[topic]
		if !ok {
			return nil // not a terminal chaining topic; ack and ignore
		}
		var result map[string]any
		if len(env.Body) > 0 {
			if err := json.Unmarshal(env.Body, &result); err != nil {
				logger.WarnContext(ctx, "chain: malformed event payload; acking",
					slog.String("topic", topic),
					slog.String("instance_id", env.Metadata[MetaInstanceID]),
					slog.Any("error", err))
				return nil // poison payload: ack so the broker does not loop on it
			}
		}
		// Best-effort: an empty/malformed definition_ref yields the zero Qualifier
		// (the metadata is routing context, not authoritative — see ChainEvent).
		predDefRef, _ := model.ParseQualifier(env.Metadata[MetaDefinitionRef])
		ev := chain.ChainEvent{
			PredecessorID:            env.Metadata[MetaInstanceID],
			PredecessorDefinitionRef: predDefRef,
			Outcome:                  outcome,
			Result:                   result,
		}
		return core.Handle(ctx, ev)
	}
}

// Chainer is the turnkey convenience wrapper around NewChainHandler for consumers
// who do not run their own broker subscriptions. Run subscribes the three terminal
// topics and drives the chaining core until ctx is cancelled (mirrors
// runtime.CallNotifier.Run). Consumers who want their own retry/poison/DLQ
// middleware should mount NewChainHandler on their own subscription instead.
type Chainer struct {
	handler Handler
	logger  *slog.Logger
}

// NewChainerRunner builds a Chainer runner over the chaining core. Pass
// WithLogger to set the structured logger (default slog.Default()).
func NewChainerRunner(core *chain.Chainer, opts ...Option) *Chainer {
	logger := newOptions(opts...).logger
	return &Chainer{handler: NewChainHandler(core, opts...), logger: logger}
}

// handle runs the chaining handler and reports the ack/nack decision at the
// right level. A benign driver shutdown is not an operational error — the nack
// correctly redelivers the terminal event so the successor starts once the
// driver is back — so it is logged at DEBUG to avoid alarm spam. Everything else
// is an ERROR. The error itself is returned either way, so the envelope is
// nacked identically.
func (c *Chainer) handle(ctx context.Context, env Envelope) error {
	err := c.handler(ctx, env)
	if err == nil {
		return nil
	}
	if isBenignDriverShutdown(err) {
		c.logger.DebugContext(ctx, "chain: driver shutting down; nacking successor start for retry",
			slog.String("instance_id", env.Metadata[MetaInstanceID]))
	} else {
		c.logger.ErrorContext(ctx, "chain: handler failed; nacking",
			slog.String("instance_id", env.Metadata[MetaInstanceID]),
			slog.Any("error", err))
	}
	return err
}

// Run subscribes the three terminal topics on sub and drives the chaining core
// for each delivered envelope until ctx is cancelled. A handler error nacks the
// envelope (re-delivery); success acks it. Run returns ctx.Err() on cancellation
// after all three subscriptions have returned.
//
// Subscribe BLOCKS and owns its own loop, so the three run on goroutines Run
// starts. The invariant the old channel-based shape got from subscribing
// everything up front — a failure on one subscription strands none of the
// others — is re-established here instead: the first non-cancellation error
// cancels its siblings, and Run does not return until every one of them has.
// TestChainerRunSubscribeError and TestChainerRunLeaksNoGoroutines are the two
// halves of that property.
func (c *Chainer) Run(ctx context.Context, sub Subscriber) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, len(chainTopicOrder))
	for _, topic := range chainTopicOrder {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := sub.Subscribe(runCtx, topic, c.handle)
			if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return // an orderly stop, not a subscription failure
			}
			errs <- fmt.Errorf("workflow-eventing: chain subscribe %q: %w", topic, err)
			cancel()
		}()
	}
	wg.Wait()
	close(errs)

	if err := <-errs; err != nil {
		return err
	}
	return ctx.Err()
}
