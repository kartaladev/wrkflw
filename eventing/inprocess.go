package eventing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"sync"
	"time"

	"go.opentelemetry.io/otel/propagation"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// ErrBusClosed is returned by [InProcess.Publish] and [InProcess.Subscribe]
// after [InProcess.Close].
var ErrBusClosed = errors.New("workflow-eventing: in-process bus closed")

// defaultRedeliveryBackoff is the pause between a handler nacking an envelope
// and the bus handing it back. Short by design: it is paid on every retry of a
// genuinely failing handler, and this bus is a single process, so there is no
// network to be kind to. Raise it with [WithRedeliveryBackoff] when a handler's
// failures are worth pacing.
const defaultRedeliveryBackoff = 10 * time.Millisecond

// InProcess is a pub/sub bus with no broker: publishes fan out, in memory, to
// every subscription on the topic. It is the zero-dependency path for tests,
// examples and single-process deployments — the thing to reach for before
// standing up Kafka.
//
// It is all three ports at once: a [kernel.OutboxPublisher] for
// persistence.NewRelay, a [Subscriber] for [Chainer.Run] or your own handlers,
// and an [io.Closer].
//
// # Non-persistent, exactly like the GoChannel it replaces
//
// An envelope published to a topic NOBODY IS SUBSCRIBED TO YET IS DROPPED. This
// is deliberate parity with watermill's gochannel.Config{}, whose behaviour this
// type was written to preserve, and it is the one property that surprises
// people: a Publish that races a Subscribe silently delivers to no one. Two ways
// to sequence it — [InProcess.Start], which returns only once the subscription
// is live, or republishing until the effect appears (what a Chainer.Run test
// must do, because Run starts its own subscriptions).
//
// # Delivery
//
// Each subscription is an independent FIFO queue: one slow handler cannot stall
// another subscription, and Publish never blocks on a handler. A handler that
// returns an error nacks, and the SAME envelope is handed back after
// [WithRedeliveryBackoff] until it acks or the subscription ends — so an
// always-failing handler holds up that one subscription's queue, at-least-once
// delivery being the point. Ack poison payloads; see [Handler].
//
// The bus starts no goroutines of its own: [InProcess.Subscribe] runs its
// delivery loop on the caller's goroutine, and [InProcess.Start] runs it on one
// goroutine that its stop function joins.
type InProcess struct {
	pub        kernel.OutboxPublisher
	logger     *slog.Logger
	propagator propagation.TextMapPropagator
	backoff    time.Duration

	mu     sync.Mutex
	subs   map[string][]*subscription
	closed bool
	done   chan struct{}
}

var (
	_ kernel.OutboxPublisher = (*InProcess)(nil)
	_ Subscriber             = (*InProcess)(nil)
	_ io.Closer              = (*InProcess)(nil)
)

// subscription is one live Subscribe call: an unbounded FIFO of envelopes plus a
// one-slot signal channel. Unbounded is what keeps Publish non-blocking, so a
// stuck handler cannot deadlock a publisher — the cost is that a subscription
// wedged on a poison envelope grows its queue, which the doc comment on
// [InProcess] states.
type subscription struct {
	notify chan struct{}

	mu    sync.Mutex
	queue []Envelope
}

func (s *subscription) push(env Envelope) {
	s.mu.Lock()
	s.queue = append(s.queue, env)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default: // a signal is already pending; the loop will drain the whole queue
	}
}

func (s *subscription) pop() (Envelope, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return Envelope{}, false
	}
	env := s.queue[0]
	s.queue = s.queue[1:]
	return env, true
}

// NewInProcess builds an in-memory pub/sub bus. Hand it to persistence.NewRelay
// as the publisher and to [Chainer.Run] (or your own handlers) as the
// subscriber, and Close it on shutdown.
func NewInProcess(opts ...Option) *InProcess {
	o := newOptions(opts...)
	b := &InProcess{
		logger:     o.logger,
		propagator: o.propagator,
		backoff:    o.redeliveryBackoff,
		subs:       make(map[string][]*subscription),
		done:       make(chan struct{}),
	}
	b.pub = NewPublisher(b.fanout, opts...)
	return b
}

// Publish maps ev to an [Envelope] — same mapping, span and counter as
// [NewPublisher] — and fans it out to every current subscription on its topic.
// It does not block on any handler.
func (b *InProcess) Publish(ctx context.Context, ev kernel.OutboxEvent) error {
	return b.pub.Publish(ctx, ev)
}

// fanout is the PublishFunc [NewPublisher] wraps. Each subscription gets its own
// copy of the metadata, so one handler mutating it cannot corrupt another's.
func (b *InProcess) fanout(_ context.Context, env Envelope) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrBusClosed
	}
	subs := append([]*subscription(nil), b.subs[env.Topic]...)
	b.mu.Unlock()

	for _, s := range subs {
		copied := env
		copied.Metadata = maps.Clone(env.Metadata)
		s.push(copied)
	}
	return nil
}

// Subscribe delivers every envelope published to topic to h, on the CALLER's
// goroutine, until ctx is done or the bus is closed. It returns ctx.Err() on
// cancellation and nil on Close. See [InProcess] for the delivery and
// redelivery contract.
func (b *InProcess) Subscribe(ctx context.Context, topic string, h Handler) error {
	s, err := b.register(topic)
	if err != nil {
		return err
	}
	defer b.unregister(topic, s)
	return b.deliver(ctx, s, h)
}

// Start registers a subscription and runs its delivery loop on a new goroutine,
// returning only once the subscription is LIVE — so a publish that immediately
// follows cannot be dropped, which a bare `go bus.Subscribe(…)` does not
// guarantee. The returned stop function ends the subscription and waits for the
// loop to finish, so a test or a shutdown path leaks nothing; it is safe to call
// more than once.
func (b *InProcess) Start(ctx context.Context, topic string, h Handler) (stop func(), err error) {
	s, err := b.register(topic)
	if err != nil {
		return nil, err
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer b.unregister(topic, s)
		_ = b.deliver(loopCtx, s, h)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}, nil
}

// Close ends every live subscription and refuses further publishes. It is
// idempotent.
func (b *InProcess) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.done)
	return nil
}

func (b *InProcess) register(topic string) (*subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBusClosed
	}
	s := &subscription{notify: make(chan struct{}, 1)}
	b.subs[topic] = append(b.subs[topic], s)
	return s, nil
}

func (b *InProcess) unregister(topic string, target *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[topic]
	for i, s := range list {
		if s == target {
			b.subs[topic] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(b.subs[topic]) == 0 {
		delete(b.subs, topic)
	}
}

// deliver is the subscription loop: drain the queue, wait for a signal, repeat.
// It ends on ctx cancellation (returning ctx.Err()) or on Close (returning nil),
// and never on a handler error — a nack is a redelivery, not a reason to stop
// consuming.
func (b *InProcess) deliver(ctx context.Context, s *subscription, h Handler) error {
	for {
		env, ok := s.pop()
		if !ok {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-b.done:
				return nil
			case <-s.notify:
			}
			continue
		}
		if err := b.dispatch(ctx, env, h); err != nil {
			return err
		}
		if b.stopped(ctx) {
			return b.stopErr(ctx)
		}
	}
}

// dispatch hands env to h until it acks, pausing for the redelivery backoff
// between attempts. It returns a non-nil error only when the subscription itself
// must end.
func (b *InProcess) dispatch(ctx context.Context, env Envelope, h Handler) error {
	for {
		// The handler's context is rebuilt from the envelope's metadata rather
		// than inherited from the publisher: a real broker hands a consumer bytes
		// and headers, never a Go value, so parenting has to survive that. What
		// IS inherited is the subscription's own lifetime — cancelling the
		// subscription cancels the handler.
		hctx := b.propagator.Extract(ctx, propagation.MapCarrier(env.Metadata))
		err := h(hctx, env)
		if err == nil {
			return nil
		}
		b.logger.DebugContext(ctx, "eventing: handler nacked; redelivering after backoff",
			slog.String("topic", env.Topic), slog.String("id", env.ID),
			slog.Duration("backoff", b.backoff), slog.Any("error", err))

		timer := time.NewTimer(b.backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-b.done:
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// stopped reports whether the subscription should end even though its queue is
// not empty — checked between envelopes so a Close or a cancel is honoured
// promptly instead of after the whole backlog drains.
func (b *InProcess) stopped(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case <-b.done:
		return true
	default:
		return false
	}
}

func (b *InProcess) stopErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
