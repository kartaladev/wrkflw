package eventing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// receiverDef returns a minimal process that parks on a ReceiveTask awaiting "OrderPlaced":
//
//	start → receive("OrderPlaced") → end
func receiverDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "receiver",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewReceiveTask("recv-order", "OrderPlaced"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "recv-order"},
			{ID: "f2", Source: "recv-order", Target: "end"},
		},
	}
}

// senderDef returns a minimal process that sends "OrderPlaced" via a SendTask:
//
//	start → send("OrderPlaced") → end
func senderDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "sender",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewSendTask("send-order", "OrderPlaced"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "send-order"},
			{ID: "f2", Source: "send-order", Target: "end"},
		},
	}
}

// TestSendTaskOutboxResumesReceiveTaskViaMessageHandler is the async end-to-end test
// for ADR-0067. It proves the full transactional-SendTask → outbox → relay → handler →
// DeliverMessage loop:
//
//  1. A receiver process parks on a ReceiveTask awaiting "OrderPlaced".
//  2. A sender process runs a SendTask that emits "OrderPlaced" into wrkflw_outbox.
//  3. The relay drains the outbox and publishes to an in-process GoChannel broker.
//  4. eventing.NewMessageHandler decodes the message and calls runner.DeliverMessage.
//  5. The receiver instance advances past the ReceiveTask and reaches StatusCompleted.
//
// The test uses a real PostgreSQL container (via testcontainers) for the Postgres store
// and relay so that the outbox write is truly transactional, proving the full seam.
func TestSendTaskOutboxResumesReceiveTaskViaMessageHandler(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// ── 1. Postgres store (real relay path) ──────────────────────────────────
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(ctx, pool))
	store, err := persistence.OpenPostgres(ctx, pool)
	require.NoError(t, err)

	// ── 2. In-process GoChannel broker ───────────────────────────────────────
	// pub is a kernel.Publisher backed by a GoChannel; sub is the matching
	// message.Subscriber; closer tears the GoChannel down at test end.
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { require.NoError(t, closer.Close()) }()

	// Subscribe BEFORE running the sender so the GoChannel buffers the message
	// (GoChannel is non-persistent; a publish before Subscribe drops the message).
	msgs, err := sub.Subscribe(ctx, "message.OrderPlaced")
	require.NoError(t, err)

	// ── 3. Runner (shared by receiver and sender) ────────────────────────────
	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	require.NoError(t, err)

	// ── 4. Park the receiver instance ────────────────────────────────────────
	recvDef := receiverDef()
	recvState, err := r.Run(ctx, recvDef, "recv-inst-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, recvState.Status,
		"receiver must park at the ReceiveTask")
	require.Len(t, recvState.Tokens, 1)
	assert.Equal(t, "OrderPlaced", recvState.Tokens[0].AwaitMessage,
		"receiver token must be parked on OrderPlaced")

	// ── 5. Run the sender: commits a message.OrderPlaced row into the outbox ─
	sendDef := senderDef()
	sendState, err := r.Run(ctx, sendDef, "send-inst-1", map[string]any{"orderId": "o-1"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, sendState.Status,
		"sender must complete synchronously (SendTask is fire-and-forget)")

	// Confirm the outbox row was written.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM wrkflw_outbox WHERE topic = 'message.OrderPlaced'`).Scan(&n))
	require.Equal(t, 1, n, "exactly one message.OrderPlaced outbox row expected")

	// ── 6. Relay drains the outbox → publishes to GoChannel ─────────────────
	relay, err := persistence.NewRelay(pool, pub)
	require.NoError(t, err)
	drained, err := relay.DrainOnce(ctx)
	require.NoError(t, err)
	// At least the message.OrderPlaced row must have been drained.
	// (instance.completed for the sender is also present; drained ≥ 1.)
	require.GreaterOrEqual(t, drained, 1,
		"relay must drain at least the message.OrderPlaced outbox row")

	// ── 7. Read from GoChannel and call the message handler ──────────────────
	// NewMessageHandler decodes the message.OrderPlaced payload and calls deliver,
	// which routes to runner.DeliverMessage to resume the parked receiver.
	deliver := eventing.NewMessageHandler(func(hCtx context.Context, name, key string, vars map[string]any) error {
		return r.DeliverMessage(hCtx, recvDef, name, key, vars)
	})

	// Drain the GoChannel until we see and process the message.OrderPlaced message.
	// Other outbox events (instance.completed for the sender) land on different
	// topics and are not in this subscription channel, so we read until the channel
	// is empty or we process the target message.
	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	delivered := false
	for !delivered {
		select {
		case msg, ok := <-msgs:
			if !ok {
				t.Fatal("subscription channel closed unexpectedly")
			}
			// Only process message.OrderPlaced; ignore anything else on this topic.
			topic := msg.Metadata.Get("topic")
			if topic != "message.OrderPlaced" {
				msg.Ack()
				continue
			}
			require.NoError(t, deliver(msg), "NewMessageHandler must not error on a valid OrderPlaced message")
			msg.Ack()
			delivered = true
		case <-ctx2.Done():
			t.Fatal("timed out waiting for message.OrderPlaced to arrive in GoChannel subscription")
		}
	}

	// ── 8. Assert the receiver advanced past the ReceiveTask ─────────────────
	final, _, err := store.Load(ctx, "recv-inst-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"receiver must complete after DeliverMessage resumes its parked ReceiveTask token")
	assert.Empty(t, final.Tokens,
		"no tokens must remain after the receiver completes")
}
