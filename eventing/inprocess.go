package eventing

import (
	"bytes"
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
// # Non-persistent, at BOTH ends
//
// An envelope published to a topic with no live subscription IS DROPPED, and
// Publish still returns nil. That is a deliberate choice, not an oversight — it
// is the behaviour of the in-memory bus this replaced, and buffering instead
// would mean an unbounded queue with no consumer to bound it — and it is the one
// property that surprises people. It has TWO windows, not one:
//
//   - STARTUP: a Publish that races a Subscribe delivers to no one. Sequence it
//     with [InProcess.Start], which returns only once the subscription is live,
//     or by republishing until the effect appears (what a [Chainer.Run] test must
//     do, because Run starts its own subscriptions).
//   - SHUTDOWN: a Publish that races a stop function, or that lands after the
//     last subscription on the topic has ended, is dropped just as silently.
//     Stop publishing before you stop subscribing. This matters most behind
//     persistence.Relay, which treats a nil from Publish as delivered and marks
//     the outbox row published — so an envelope lost in this window is lost for
//     good. [InProcess.Close] is the safe order: it refuses later publishes with
//     [ErrBusClosed] rather than accepting and dropping them.
//
// # Delivery
//
// Each subscription has its own FIFO queue, so one slow handler cannot stall
// another subscription and Publish never blocks on a handler. The Envelope a
// [Handler] receives is a fresh copy, cloned per DELIVERY ATTEMPT rather than
// per subscriber, so no handler can observe another's mutations — or its own
// previous attempt's. A handler that returns an error nacks, and the same
// envelope is handed back after [WithRedeliveryBackoff] until it acks or the
// subscription ends.
//
// At-least-once delivery is the point of that, and it has a cost worth stating
// plainly: an always-failing handler blocks its own subscription's queue AND
// that queue GROWS WITHOUT BOUND, because Publish keeps appending to it and
// never blocks. There is no cap, no drop policy and no dead-letter path here.
// A poison envelope that a handler nacks forever is therefore a memory leak, not
// merely a stalled consumer — ack poison payloads rather than nacking them, and
// see [Handler] for that discipline.
//
// The context a handler receives is rebuilt from the envelope's metadata, so the
// trace parent it carries is REMOTE and UNVERIFIED — whatever the publisher
// wrote, exactly as a trace context arriving over a real broker would be, and
// forgeable by anyone who can publish. It is observability context, not
// authoritative: never an input to identity, authorization or any decision the
// system acts on, in the same sense that [MetaDefinitionRef] is routing context
// rather than proof of provenance.
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
// wedged on a poison envelope grows its queue without bound, which the doc
// comment on [InProcess] states as part of the delivery contract.
//
// cancel is set only for a subscription created by [InProcess.Start], which owns
// the context it derived; it is nil for [InProcess.Subscribe], whose context
// belongs to the caller. [InProcess.Close] calls the ones it has, so closing the
// bus without calling a stop function releases the context registration rather
// than stranding it until the parent is cancelled.
type subscription struct {
	notify chan struct{}
	cancel context.CancelFunc

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

// fanout is the PublishFunc [NewPublisher] wraps. It queues the SAME Envelope
// value on every matching subscription and copies nothing: isolation is owned
// entirely by dispatch, which clones per delivery attempt. Doing it here as well
// would be both redundant and insufficient — see the comment there.
func (b *InProcess) fanout(_ context.Context, env Envelope) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrBusClosed
	}
	subs := append([]*subscription(nil), b.subs[env.Topic]...)
	b.mu.Unlock()

	for _, s := range subs {
		s.push(env)
	}
	return nil
}

// Subscribe delivers every envelope published to topic to h, on the CALLER's
// goroutine, until ctx is done or the bus is closed. It returns ctx.Err() on
// cancellation and nil on Close. See [InProcess] for the delivery and
// redelivery contract.
func (b *InProcess) Subscribe(ctx context.Context, topic string, h Handler) error {
	s, err := b.register(topic, nil)
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
// concurrently and more than once, and later callers block until the first has
// joined.
//
// DO NOT CALL stop FROM INSIDE THE HANDLER IT STOPPED. Waiting for the loop to
// finish means waiting for the handler to return, so a handler that calls its
// own stop deadlocks itself — and because the wait is a channel receive, it
// deadlocks silently rather than panicking. To end a subscription from within
// its own handler, cancel the context passed to Start (or call [InProcess.Close])
// and let the handler return normally; stop then joins from wherever it is
// called next.
func (b *InProcess) Start(ctx context.Context, topic string, h Handler) (stop func(), err error) {
	loopCtx, cancel := context.WithCancel(ctx)
	s, err := b.register(topic, cancel)
	if err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer b.unregister(topic, s)
		_ = b.deliver(loopCtx, s, h)
	}()

	// MEASURED: removing this Once is an EQUIVALENT MUTATION, and it is recorded
	// here so the next reader does not file it as a coverage gap. cancel is
	// idempotent, and done is CLOSED rather than sent on, so without the Once a
	// second or concurrent stop still runs cancel harmlessly and still blocks on
	// <-done until the loop has joined. Every promise the doc comment above makes
	// — safe twice, safe concurrently, later callers block until the first has
	// joined — is delivered by those two facts, not by the Once. No test can
	// distinguish the two versions, so none tries to:
	// TestInProcessStopIsSafeToCallTwiceAndConcurrently pins the CONTRACT, which
	// a future implementation could break for real. The Once is kept as a
	// statement of intent — stop's body runs once — not as the guard the contract
	// rests on.
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}, nil
}

// Close ends every live subscription and refuses further publishes with
// [ErrBusClosed]. It is idempotent, and it is safe to call without having called
// any stop function: for a subscription created by [InProcess.Start] it also
// cancels the context that subscription derived, so the registration on the
// parent context is released rather than stranded. A handler in flight sees that
// cancellation on its own ctx.
//
// A subscription created by [InProcess.Subscribe] returns nil from Subscribe,
// but its context belongs to the caller and is NOT cancelled here — the bus
// never cancels a context it did not create.
//
// Close does not wait for delivery loops to finish. Use a stop function from
// [InProcess.Start] when you need to join them.
func (b *InProcess) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	close(b.done)
	// Collect before unlocking and cancel after: a cancel can wake a delivery
	// loop that immediately calls unregister, which takes this same mutex.
	var cancels []context.CancelFunc
	for _, list := range b.subs {
		for _, s := range list {
			if s.cancel != nil {
				cancels = append(cancels, s.cancel)
			}
		}
	}
	b.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func (b *InProcess) register(topic string, cancel context.CancelFunc) (*subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBusClosed
	}
	s := &subscription{notify: make(chan struct{}, 1), cancel: cancel}
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
		// A FRESH COPY PER DELIVERY ATTEMPT — not per subscriber — is what makes
		// "each subscription is independent" true. Both are needed and one place
		// owns both:
		//
		//   - across subscribers, because a delivered Envelope is the handler's
		//     to do as it likes, exactly as a real broker hands each consumer its
		//     own bytes off the wire;
		//   - across ATTEMPTS, because this loop re-extracts the handler context
		//     from Metadata below on every retry. A handler that stamps a key in,
		//     or decodes Body in place, would otherwise corrupt its own next
		//     attempt — and the trace context that attempt is built from.
		//
		// Cloning in fanout instead covers only the first case, which is why the
		// copy lives here. watermill's gochannel put it in the same place, inside
		// its retry loop, naming retries in the comment.
		//
		// On the happy path this is one clone, the same cost as cloning per
		// subscriber. It only multiplies for a handler that is already failing,
		// where a timer and a four-attribute log line dominate anyway.
		attempt := env
		attempt.Metadata = maps.Clone(env.Metadata)
		attempt.Body = bytes.Clone(env.Body)

		// The handler's context is rebuilt from the envelope's metadata rather
		// than inherited from the publisher: a real broker hands a consumer bytes
		// and headers, never a Go value, so parenting has to survive that. What
		// IS inherited is the subscription's own lifetime — cancelling the
		// subscription cancels the handler.
		hctx := b.propagator.Extract(ctx, propagation.MapCarrier(attempt.Metadata))
		err := h(hctx, attempt)
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
