// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package kernel

import (
	"context"
	"errors"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ErrInstanceNotFound is returned by Store.Load when no instance exists for the id.
var ErrInstanceNotFound = errors.New("workflow-runtime: instance not found")

// ErrInstanceExists is returned by Store.Create when an instance with the same
// id already exists. It lets a caller distinguish a duplicate start from a real
// failure — process-instance chaining treats it as "already started"
// (a clean no-op ack) under at-least-once terminal-event delivery.
var ErrInstanceExists = errors.New("workflow-runtime: instance already exists")

// JournalReader exposes the recorded trigger history for replay/audit.
type JournalReader interface {
	Entries(ctx context.Context, id string) ([]engine.Trigger, error)
}

// Version is an opaque optimistic-concurrency token (Postgres: a bigint version).
type Version int64

// OutboxEvent is one domain event to relay. DedupKey and InstanceID are
// populated when the event is read back from a persisted outbox row; they let a
// publisher set a stable message identity (DedupKey) and a per-instance
// partition/ordering key (InstanceID). They are empty for events not sourced
// from a persisted row.
type OutboxEvent struct {
	Topic      string
	Payload    map[string]any
	DedupKey   string
	InstanceID string
	// DefinitionRef is the id:version reference of the instance that produced the
	// event, carried through to a consumer (e.g. chaining's PredecessorDefinitionRef).
	// It is best-effort routing context — the zero Qualifier when the producing
	// path supplies no definition reference.
	DefinitionRef model.Qualifier
}

// AppliedStep is the atomic persistence unit for exactly one applied trigger:
// the new snapshot, the trigger that produced it, and the outbox events derived
// from the resulting commands.
type AppliedStep struct {
	State   engine.InstanceState
	Trigger engine.Trigger
	Events  []OutboxEvent
	// NewCallLink, when non-nil, records a parent↔child call link atomically with
	// this step (set on the child's first Create).
	NewCallLink *CallLink
	// CallOutcome, when non-nil, flips THIS instance's call link to terminal
	// atomically with this step (set on the child's terminal Commit).
	CallOutcome *CallOutcome
}

// ErrConcurrentUpdate is returned by Store.Commit when the expected token is
// stale (a concurrent writer advanced the instance first).
var ErrConcurrentUpdate = errors.New("workflow-runtime: concurrent update")

// InstanceStore is the transactional persistence port the ProcessDriver depends on. Commit
// persists snapshot + journal + outbox atomically per applied trigger.
type InstanceStore interface {
	Create(ctx context.Context, step AppliedStep) (Version, error)
	Load(ctx context.Context, id string) (engine.InstanceState, Version, error)
	Commit(ctx context.Context, expected Version, step AppliedStep) (Version, error)
}

// PendingCommandWriter is an OPTIONAL [InstanceStore] capability: it rewrites an
// instance's [engine.InstanceState.PendingCommands] mark in place, without
// applying a step.
//
// The runtime uses it for all three of the mark's lifecycle transitions, which
// is why it is one method and not three:
//
//   - CLEAR, when every command of a committed step has been performed:
//     cmds=nil, at=zero.
//   - PROGRESS, when a recovery pass performed some of a mark's commands and
//     failed on one: cmds=the unperformed remainder, at=now. Without this a
//     failing re-drive re-runs its already-succeeded siblings on every pass,
//     forever — measured at eleven charges for one committed step.
//   - DEFER, which is the same call: re-stamping `at` is what lets the sweep's
//     grace window space out a repeatedly-failing instance instead of retrying
//     it on every tick.
//
// It is probed by type assertion, exactly like [TxRunner], so an existing
// InstanceStore implementation keeps compiling and keeps working. A store that
// does not implement it leaves the mark exactly as the last commit wrote it:
// recovery still runs and is still correct, but it cannot record progress, so a
// parked instance's commands are re-performed repeatedly until something else
// advances it.
// ⚠ Without it the rate is once per sweep TICK, not once per grace window. The
// window is measured against engine.InstanceState.PendingCommandsAt, and this
// capability is the ONLY thing that ever rewrites that stamp — so a store lacking
// it leaves the stamp frozen at commit time, the age test passes on every tick,
// and at defaults that is every 1m rather than every 5m. Measured: five re-drives
// over five passes against one, with the port. It applies only to the two commands
// alreadyPerformed cannot gate, ThrowSignal and UpdateTask, and ThrowSignal is the
// worse of the two because its duplicate fans out to OTHER instances.
//
// Three properties are contractual, and each is load-bearing:
//
//   - It does NOT advance the optimistic-concurrency token. The mark records
//     what has and has not been performed; it applies no trigger, and bumping
//     the version would invalidate the token the caller is still holding.
//   - It records NO journal entry and NO outbox event. The journal is the replay
//     log of applied triggers, and this applies none.
//   - A STALE expected is not an error. A later step has already rewritten the
//     snapshot, mark included, so there is nothing to write and nothing to
//     report. Implementations return nil.
//
// Implementations MUST preserve every other byte of the snapshot, including keys
// they do not recognise: this runs on every replica against every listed
// instance, so a decode-and-re-encode through a stale struct definition would
// strip newer fields fleet-wide with no CAS to detect it.
type PendingCommandWriter interface {
	WritePendingCommands(ctx context.Context, id string, expected Version, cmds []engine.PendingCommand, at time.Time) error
}
