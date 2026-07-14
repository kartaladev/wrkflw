package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ThreeDotsLabs/watermill/message"

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

// chainTopics are the three status-accurate terminal topics a chaining consumer
// subscribes (ADR-0046). The map also drives topic→Outcome projection.
var chainTopics = map[string]kernel.ChainOutcome{
	"instance.completed":  kernel.OutcomeCompleted,
	"instance.failed":     kernel.OutcomeFailed,
	"instance.terminated": kernel.OutcomeTerminated,
}

// NewChainHandler adapts the broker-agnostic runtime.Chainer core to a watermill
// no-publish handler. A consumer mounts it on their own message.Router (their
// retry/poison/DLQ middleware wraps it), registering it for the three terminal
// topics. It projects each message to a runtime.ChainEvent:
//
//   - topic (msg.Metadata "topic", set by eventing.NewPublisher) → Outcome
//   - msg.Metadata "instance_id" → PredecessorID
//   - msg.Metadata "definition_ref" → PredecessorDefinitionRef (set by the built-in
//     publisher from the source instance's "defID:version", ADR-0047; empty for
//     pre-ADR-0047 events)
//   - the JSON body → Result
//
// Ack/Nack discipline (a returned error nacks for re-delivery):
//
//   - success / no-op (no successor, duplicate) → nil (ack)
//   - non-terminal / unknown topic              → nil (ack, ignored)
//   - malformed JSON body                        → nil (ack + log; never loop)
//   - transient core failure                     → error (nack → re-delivered)
func NewChainHandler(core *chain.Chainer) message.NoPublishHandlerFunc {
	logger := slog.Default()
	return func(msg *message.Message) error {
		outcome, ok := chainTopics[msg.Metadata.Get("topic")]
		if !ok {
			return nil // not a terminal chaining topic; ack and ignore
		}
		var result map[string]any
		if len(msg.Payload) > 0 {
			if err := json.Unmarshal(msg.Payload, &result); err != nil {
				logger.WarnContext(msg.Context(), "chain: malformed event payload; acking",
					slog.String("topic", msg.Metadata.Get("topic")),
					slog.String("instance_id", msg.Metadata.Get("instance_id")),
					slog.Any("error", err))
				return nil // poison payload: ack so the broker does not loop on it
			}
		}
		// Best-effort: an empty/malformed definition_ref yields the zero Qualifier
		// (the metadata is routing context, not authoritative — see ChainEvent).
		predDefRef, _ := model.ParseQualifier(msg.Metadata.Get("definition_ref"))
		ev := chain.ChainEvent{
			PredecessorID:            msg.Metadata.Get("instance_id"),
			PredecessorDefinitionRef: predDefRef,
			Outcome:                  outcome,
			Result:                   result,
		}
		return core.Handle(msg.Context(), ev)
	}
}

// Chainer is the turnkey convenience wrapper around NewChainHandler for consumers
// who do not run their own message.Router. Run subscribes the three terminal
// topics and drives the chaining core until ctx is cancelled (mirrors
// runtime.CallNotifier.Run). Consumers who want their own retry/poison/DLQ
// middleware should mount NewChainHandler on their Router instead.
type Chainer struct {
	handler message.NoPublishHandlerFunc
	logger  *slog.Logger
}

// NewChainerRunner builds a Chainer runner over the chaining core. Pass
// WithLogger to set the structured logger (default slog.Default()).
func NewChainerRunner(core *chain.Chainer, opts ...Option) *Chainer {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Chainer{handler: NewChainHandler(core), logger: logger}
}

// Run subscribes the three terminal topics on sub and drives the chaining core
// for each delivered message until ctx is cancelled. A handler error nacks the
// message (re-delivery); success acks it. Run returns ctx.Err() on cancellation
// after all per-topic loops drain.
func (c *Chainer) Run(ctx context.Context, sub message.Subscriber) error {
	// Subscribe ALL topics before starting any goroutine, so a failure on a later
	// Subscribe cannot leak the goroutines of earlier ones.
	channels := make([]<-chan *message.Message, 0, len(chainTopics))
	for topic := range chainTopics {
		msgs, err := sub.Subscribe(ctx, topic)
		if err != nil {
			return fmt.Errorf("workflow-eventing: chain subscribe %q: %w", topic, err)
		}
		channels = append(channels, msgs)
	}

	var wg sync.WaitGroup
	for _, ch := range channels {
		wg.Add(1)
		go func(ch <-chan *message.Message) {
			defer wg.Done()
			for msg := range ch {
				if err := c.handler(msg); err != nil {
					if isBenignDriverShutdown(err) {
						// Driver is draining (graceful shutdown): the nack correctly
						// redelivers the terminal event so the successor starts once the
						// driver is back. Not an error — log at DEBUG to avoid alarm spam.
						c.logger.DebugContext(msg.Context(), "chain: driver shutting down; nacking successor start for retry",
							slog.String("instance_id", msg.Metadata.Get("instance_id")))
					} else {
						c.logger.ErrorContext(msg.Context(), "chain: handler failed; nacking",
							slog.String("instance_id", msg.Metadata.Get("instance_id")),
							slog.Any("error", err))
					}
					msg.Nack()
					continue
				}
				msg.Ack()
			}
		}(ch)
	}
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}
