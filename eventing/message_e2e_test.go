package eventing_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// receiverDef returns a minimal process that parks on a ReceiveTask awaiting "OrderPlaced":
//
//	start → receive("OrderPlaced") → end
func receiverDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "receiver",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("recv-order", "OrderPlaced"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("start"),
			activity.NewSendTask("send-order", "OrderPlaced"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "send-order"},
			{ID: "f2", Source: "send-order", Target: "end"},
		},
	}
}

// TestSendTaskOutboxResumesReceiveTaskViaMessageHandler is the async end-to-end test
// proving the full transactional-SendTask → outbox → relay → handler →
// DeliverMessage loop:
//
//  1. A receiver process parks on a ReceiveTask awaiting "OrderPlaced".
//  2. A sender process runs a SendTask that emits "OrderPlaced" into wrkflw_outbox.
//  3. The relay drains the outbox and publishes to an in-process bus.
//  4. eventing.NewMessageHandler decodes the envelope and calls driver.DeliverMessage.
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

	// ── 2. In-process bus ────────────────────────────────────────────────────
	// bus is the kernel.OutboxPublisher the relay drains into AND the Subscriber
	// the handler is mounted on. Close tears it down at test end.
	bus := eventing.NewInProcess()
	defer func() { require.NoError(t, bus.Close()) }()

	// ── 3. Runner (shared by receiver and sender) ────────────────────────────
	// The driver resolves the correlated instance's definition from its own
	// snapshot via the registry, so both definitions are registered.
	recvDef := receiverDef()
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(recvDef))
	require.NoError(t, reg.Register(senderDef()))
	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store), runtime.WithDefinitions(reg))
	require.NoError(t, err)

	// ── 4. Park the receiver instance ────────────────────────────────────────
	recvState, err := driver.Drive(ctx, recvDef, "recv-inst-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, recvState.Status,
		"receiver must park at the ReceiveTask")
	require.Len(t, recvState.Tokens, 1)
	assert.Equal(t, "OrderPlaced", recvState.Tokens[0].AwaitMessage,
		"receiver token must be parked on OrderPlaced")

	// ── 5. Run the sender: commits a message.OrderPlaced row into the outbox ─
	sendDef := senderDef()
	sendState, err := driver.Drive(ctx, sendDef, "send-inst-1", map[string]any{"orderId": "o-1"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, sendState.Status,
		"sender must complete synchronously (SendTask is fire-and-forget)")

	// Confirm the outbox row was written.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM wrkflw_outbox WHERE topic = 'message.OrderPlaced'`).Scan(&n))
	require.Equal(t, 1, n, "exactly one message.OrderPlaced outbox row expected")

	// ── 6. Mount the message handler on the bus ──────────────────────────────
	// NewMessageHandler decodes the message.OrderPlaced payload and calls deliver,
	// which routes to driver.DeliverMessage to resume the parked receiver. Start
	// returns only once the subscription is LIVE, so the relay's publish below
	// cannot be dropped — the bus is non-persistent, like every broker-less bus.
	stop, err := bus.Start(ctx, eventing.TopicMessagePrefix+"OrderPlaced",
		eventing.NewMessageHandler(driver.DeliverMessage))
	require.NoError(t, err)
	defer stop()

	// ── 7. Relay drains the outbox → publishes to the bus ───────────────────
	relay, err := persistence.NewRelay(pool, bus)
	require.NoError(t, err)
	drained, err := relay.DrainOnce(ctx)
	require.NoError(t, err)
	// At least the message.OrderPlaced row must have been drained.
	// (instance.completed for the sender is also present; drained ≥ 1.)
	require.GreaterOrEqual(t, drained, 1,
		"relay must drain at least the message.OrderPlaced outbox row")

	// ── 8. Assert the receiver advanced past the ReceiveTask ─────────────────
	// The wait below reads the receiver's FINAL state, so that is what it waits
	// for — not the handler having been called, which merely correlates with it.
	require.Eventually(t, func() bool {
		st, _, err := store.Load(ctx, "recv-inst-1")
		return err == nil && st.Status == engine.StatusCompleted
	}, 10*time.Second, 25*time.Millisecond,
		"the receiver must complete once the relayed message.OrderPlaced is delivered")

	final, _, err := store.Load(ctx, "recv-inst-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"receiver must complete after DeliverMessage resumes its parked ReceiveTask id")
	assert.Empty(t, final.Tokens,
		"no tokens must remain after the receiver completes")
}
