# Performance / Caching Track — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the three deferred performance follow-ups — an owned-instance single-writer state cache, an open-visit-preserving history cap, and an optional LISTEN/NOTIFY relay trigger — each opt-in and behavior-preserving by default.

**Architecture:** Pure pieces (the `CachingStore` `Store` decorator, the `Ownership` port, the in-process `AlwaysOwn`) live in `runtime/` beside `CachingDefinitionRegistry`. Postgres pieces (the `capHistory` projection, transactional `NOTIFY`, the advisory-lock `Ownership`, the relay listener) live in `internal/persistence/postgres` and are reached through the `persistence/` façade as stable interface types. `engine/` and `model/` are untouched.

**Tech Stack:** Go 1.25, pgx v5 + pgxpool, `golang.org/x/sync/singleflight` (already a dep), `container/list` (stdlib), testcontainers-go Postgres 17 via `database.RunTestDatabase`.

## Global Constraints

- **Go 1.25**; module path `github.com/kartaladev/wrkflw`.
- **TDD strict** (CLAUDE.md): every new symbol gets a failing test with a *visible RED* (`go test ./<pkg>/...`) before implementation. Never write impl before observing the red.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an **`assert` closure per case** (project `table-test` skill — NOT `want`/`wantErr` fields); use `t.Context()` not `context.Background()`; pair each `foo.go` with `foo_test.go`.
- **Postgres tests:** use `database.RunTestDatabase(t)` (returns `*pgxpool.Pool`); never mock the DB; run the Postgres package with limited parallelism (`go test -p 1 ./internal/persistence/postgres/...`) — high container concurrency surfaces spurious testcontainers startup failures.
- **Purity:** no `watermill`/`casbin`/`gocron`/`clockwork` import in production code; `clockwork` only in test files. `engine`/`model` gain no new imports — the `engine/purity_test.go` guard must stay green.
- **Lint:** `golangci-lint` is v2 (`.golangci.yml`, `version: "2"`); `golangci-lint run ./...` must be clean.
- **Coverage:** ≥85% line coverage on every touched package.
- **Commits:** Conventional Commits scoped `perf` (or `feat`/`refactor` as fits), ending with the `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer. Commit per task.
- **Façade discipline (ADR-0008):** `persistence` constructors/options return stable interface/value types, never internal concrete structs.

---

## Task order

History cap (Task 1–2) → LISTEN/NOTIFY (Task 3–4) → state cache (Task 5–8) → verification + HANDOVER (Task 9). Smallest/lowest-risk first; the architecturally heavy cache last with full attention.

---

### Task 1: `capHistory` open-visit-preserving projection (pure)

**Files:**
- Create: `internal/persistence/postgres/history_cap.go`
- Create: `internal/persistence/postgres/history_cap_test.go`
- Modify: `internal/persistence/postgres/export_test.go` (add the test seam)

**Interfaces:**
- Produces: `func capHistory(st engine.InstanceState, n int) engine.InstanceState` — returns a copy whose `History` keeps every open visit (`LeftAt == nil`) plus at most the most recent `n` closed visits, preserving order; `n <= 0` returns `st` unchanged. Exposed to tests as `postgres.CapHistory`.
- Consumes: `engine.InstanceState`, `engine.NodeVisit` (`NodeID`, `TokenID`, `EnteredAt`, `LeftAt *time.Time`, `ActorID *string`).

- [ ] **Step 1: Add the test seam**

Append to `internal/persistence/postgres/export_test.go`:

```go
// CapHistory exposes the unexported capHistory helper for black-box tests.
var CapHistory = capHistory
```

- [ ] **Step 2: Write the failing test**

Create `internal/persistence/postgres/history_cap_test.go`:

```go
package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/postgres"
)

func ptrTime(t time.Time) *time.Time { return &t }

// closed builds a closed visit (LeftAt set); open builds an open visit.
func closed(node string, at time.Time) engine.NodeVisit {
	return engine.NodeVisit{NodeID: node, TokenID: node + "-tok", EnteredAt: at, LeftAt: ptrTime(at.Add(time.Second))}
}
func open(node string, at time.Time) engine.NodeVisit {
	return engine.NodeVisit{NodeID: node, TokenID: node + "-tok", EnteredAt: at}
}

func TestCapHistory(t *testing.T) {
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		hist   []engine.NodeVisit
		n      int
		assert func(t *testing.T, got []engine.NodeVisit)
	}{
		{
			name: "n<=0 is a no-op",
			hist: []engine.NodeVisit{closed("a", base), closed("b", base)},
			n:    0,
			assert: func(t *testing.T, got []engine.NodeVisit) {
				assert.Len(t, got, 2)
			},
		},
		{
			name: "old open visit behind many closed visits survives the cap",
			hist: []engine.NodeVisit{
				open("human", base), // the long-parked open visit, oldest
				closed("c1", base.Add(1 * time.Minute)),
				closed("c2", base.Add(2 * time.Minute)),
				closed("c3", base.Add(3 * time.Minute)),
			},
			n: 1, // keep only 1 closed visit
			assert: func(t *testing.T, got []engine.NodeVisit) {
				// open visit retained + most-recent 1 closed (c3); order preserved.
				assert.Len(t, got, 2)
				assert.Equal(t, "human", got[0].NodeID)
				assert.Nil(t, got[0].LeftAt)
				assert.Equal(t, "c3", got[1].NodeID)
			},
		},
		{
			name: "closed visits trimmed to most-recent n, all opens kept",
			hist: []engine.NodeVisit{
				closed("c1", base.Add(1 * time.Minute)),
				closed("c2", base.Add(2 * time.Minute)),
				open("o1", base.Add(3 * time.Minute)),
				closed("c3", base.Add(4 * time.Minute)),
			},
			n: 2,
			assert: func(t *testing.T, got []engine.NodeVisit) {
				// keep c2, o1, c3 (drop c1 — oldest closed beyond the 2 most recent).
				assert.Len(t, got, 3)
				assert.Equal(t, []string{"c2", "o1", "c3"}, []string{got[0].NodeID, got[1].NodeID, got[2].NodeID})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := engine.InstanceState{InstanceID: "i1", History: tc.hist}
			got := postgres.CapHistory(st, tc.n)
			tc.assert(t, got.History)
			// capHistory must not mutate the input slice.
			assert.Len(t, st.History, len(tc.hist))
		})
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/persistence/postgres/... -run TestCapHistory`
Expected: FAIL — `undefined: postgres.CapHistory` (compile error).

- [ ] **Step 4: Write the minimal implementation**

Create `internal/persistence/postgres/history_cap.go`:

```go
package postgres

import "github.com/kartaladev/wrkflw/engine"

// capHistory returns a copy of st whose History retains every OPEN visit
// (LeftAt == nil) plus at most the most recent n CLOSED visits, preserving the
// original relative order. n <= 0 means "no cap" and returns st unchanged.
//
// Safety (ADR-0021): engine.Step reads History only via setVisitActor and
// closeVisit, both of which match ONLY open visits. Open visits are never
// dropped, so a capped snapshot drives identical execution on reload; closed
// visits are pure audit (the wrkflw_journal table remains the full record).
func capHistory(st engine.InstanceState, n int) engine.InstanceState {
	if n <= 0 {
		return st
	}
	// Count closed visits to compute the keep-threshold for the most-recent n.
	closedTotal := 0
	for i := range st.History {
		if st.History[i].LeftAt != nil {
			closedTotal++
		}
	}
	if closedTotal <= n {
		return st // nothing to trim
	}
	dropClosed := closedTotal - n // number of oldest closed visits to drop
	kept := make([]engine.NodeVisit, 0, len(st.History)-dropClosed)
	dropped := 0
	for i := range st.History {
		v := st.History[i]
		if v.LeftAt != nil && dropped < dropClosed {
			dropped++
			continue // skip an old closed visit
		}
		kept = append(kept, v)
	}
	st.History = kept
	return st
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/persistence/postgres/... -run TestCapHistory`
Expected: PASS (all three subtests).

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/history_cap.go internal/persistence/postgres/history_cap_test.go internal/persistence/postgres/export_test.go
git commit -m "$(printf 'feat(persistence): open-visit-preserving capHistory helper (ADR-0021)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: Wire the history cap into the Store + façade option

**Files:**
- Modify: `internal/persistence/postgres/store.go` (add `StoreOption`, `historyCap` field, apply `capHistory` before each `json.Marshal`)
- Modify: `persistence/persistence.go` (add `Option`, `WithHistoryCap`, thread options through `OpenPostgres`)
- Create: `internal/persistence/postgres/history_cap_store_test.go` (testcontainers round-trip)

**Interfaces:**
- Consumes: `capHistory` (Task 1); existing `NewStore(pool)`, `Store.Create`, `Store.Commit`.
- Produces: `postgres.StoreOption func(*Store)`; `postgres.WithHistoryCap(n int) StoreOption`; `postgres.NewStore(pool, opts ...StoreOption) *Store`; `persistence.Option = postgres.StoreOption`; `persistence.WithHistoryCap(n int) Option`; `persistence.OpenPostgres(ctx, pool, opts ...Option) (Store, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/persistence/postgres/history_cap_store_test.go`:

```go
package postgres_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/postgres"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestStoreHistoryCapTrimsClosedVisitsOnLoad(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	st := postgres.NewStore(pool, postgres.WithHistoryCap(1))

	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	state := engine.InstanceState{
		InstanceID: "cap-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning,
		StartedAt: base,
		History: []engine.NodeVisit{
			open("human", base),                            // long-parked open visit
			closed("c1", base.Add(1*time.Minute)),
			closed("c2", base.Add(2*time.Minute)),
		},
	}
	_, err := st.Create(t.Context(), runtime.AppliedStep{State: state, Trigger: startTrigger("cap-1")})
	require.NoError(t, err)

	got, _, err := st.Load(t.Context(), "cap-1")
	require.NoError(t, err)

	// Open visit retained; closed trimmed to the most recent 1 (c2).
	assert.Len(t, got.History, 2)
	assert.Equal(t, "human", got.History[0].NodeID)
	assert.Nil(t, got.History[0].LeftAt)
	assert.Equal(t, "c2", got.History[1].NodeID)

	// And the raw JSONB column was capped (not just the read path).
	var snap []byte
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT snapshot FROM wrkflw_instances WHERE instance_id = $1`, "cap-1").Scan(&snap))
	var raw engine.InstanceState
	require.NoError(t, json.Unmarshal(snap, &raw))
	assert.Len(t, raw.History, 2)
}
```

> Note: reuse the `open`/`closed` helpers from `history_cap_test.go` (same `postgres_test` package). `startTrigger` is an existing test helper in this package — confirm its name in `store_test.go` and adjust if different (it constructs a valid `engine.Trigger` for an instance id).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestStoreHistoryCap`
Expected: FAIL — `too many arguments in call to postgres.NewStore` / `undefined: postgres.WithHistoryCap`.

- [ ] **Step 3: Implement the Store option + apply the cap**

In `internal/persistence/postgres/store.go`, change the struct and constructor and apply `capHistory` before each marshal:

```go
// Store is the Postgres-backed runtime.Store + JournalReader. ...
type Store struct {
	pool       *pgxpool.Pool
	historyCap int // <= 0 means no cap (full inline history)
	notify     bool // emit NOTIFY wrkflw_outbox on outbox insert (Task 3)
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithHistoryCap bounds the inline History retained in the snapshot to every
// open visit plus at most n most-recent closed visits (ADR-0021). n <= 0 (the
// default) keeps full inline history. The wrkflw_journal table is unaffected
// and remains the complete audit source.
func WithHistoryCap(n int) StoreOption { return func(s *Store) { s.historyCap = n } }

// NewStore constructs a Store over the given pool. The pool must already have
// migrations applied (see Migrate).
func NewStore(pool *pgxpool.Pool, opts ...StoreOption) *Store {
	s := &Store{pool: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}
```

In `Create`, replace the marshal line:

```go
	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		return 0, fmt.Errorf("postgres: create: marshal snapshot: %w", err)
	}
```

In `Commit`, replace the marshal line identically:

```go
	snap, err := json.Marshal(capHistory(step.State, s.historyCap))
	if err != nil {
		return 0, fmt.Errorf("postgres: commit: marshal snapshot: %w", err)
	}
```

> `capHistory` returns `st` unchanged when `historyCap <= 0`, so the default path is a no-op pass-through — existing behavior preserved.

- [ ] **Step 4: Thread the option through the façade**

In `persistence/persistence.go` add (near the other re-exports/options):

```go
// Option configures the Postgres Store returned by OpenPostgres
// (alias of postgres.StoreOption).
type Option = postgres.StoreOption

// WithHistoryCap bounds the inline instance History persisted in the snapshot
// to every open visit plus at most n most-recent closed visits (ADR-0021).
// Unset / n <= 0 keeps full inline history (current behavior). The journal
// table remains the complete audit source.
func WithHistoryCap(n int) Option { return postgres.WithHistoryCap(n) }
```

And change `OpenPostgres` to accept options:

```go
func OpenPostgres(_ context.Context, pool *pgxpool.Pool, opts ...Option) (Store, error) {
	return postgres.NewStore(pool, opts...), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestStoreHistoryCap`
Run: `go test ./persistence/...`
Expected: PASS; the broader `go test -p 1 ./internal/persistence/postgres/...` still green (no regression).

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/store.go persistence/persistence.go internal/persistence/postgres/history_cap_store_test.go
git commit -m "$(printf 'feat(persistence): WithHistoryCap option on the Store + facade (ADR-0021)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: Transactional NOTIFY on outbox insert (write side)

**Files:**
- Modify: `internal/persistence/postgres/store.go` (emit `NOTIFY wrkflw_outbox` inside the tx when events were inserted and `notify` is on)
- Modify: `persistence/persistence.go` (add `WithOutboxNotify`)
- Create: `internal/persistence/postgres/notify_test.go` (testcontainers: a LISTEN connection receives the notification)

**Interfaces:**
- Consumes: existing `Store.Create`/`Commit`, the `notify` field + `StoreOption` from Task 2.
- Produces: `postgres.WithOutboxNotify() StoreOption`; `persistence.WithOutboxNotify() Option`. Channel name constant `outboxNotifyChannel = "wrkflw_outbox"`.

- [ ] **Step 1: Write the failing test**

Create `internal/persistence/postgres/notify_test.go`:

```go
package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/postgres"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestStoreOutboxNotifyWakesListener(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	// A dedicated LISTEN connection.
	lconn, err := pool.Acquire(t.Context())
	require.NoError(t, err)
	defer lconn.Release()
	_, err = lconn.Exec(t.Context(), "LISTEN wrkflw_outbox")
	require.NoError(t, err)

	st := postgres.NewStore(pool, postgres.WithOutboxNotify())

	// Create an instance whose first step emits an outbox event.
	_, err = st.Create(t.Context(), runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "n1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Now().UTC()},
		Trigger: startTrigger("n1"),
		Events:  []runtime.OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"id": "n1"}}},
	})
	require.NoError(t, err)

	// The notification must arrive promptly.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	n, err := lconn.Conn().WaitForNotification(ctx)
	require.NoError(t, err)
	require.Equal(t, "wrkflw_outbox", n.Channel)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestStoreOutboxNotify`
Expected: FAIL — `undefined: postgres.WithOutboxNotify`.

- [ ] **Step 3: Implement the option + the in-tx NOTIFY**

In `internal/persistence/postgres/store.go` add the channel constant and option:

```go
// outboxNotifyChannel is the Postgres NOTIFY channel the relay listens on
// (ADR-0022). The notification carries no payload — it is a bare wakeup; the
// relay still claims rows via FOR UPDATE SKIP LOCKED.
const outboxNotifyChannel = "wrkflw_outbox"

// WithOutboxNotify makes Create/Commit emit NOTIFY wrkflw_outbox inside the
// committing transaction whenever the step inserted at least one outbox row, so
// a listening relay (WithListenNotify) wakes immediately instead of waiting for
// its next poll tick. Steps that produce no events emit no notification.
func WithOutboxNotify() StoreOption { return func(s *Store) { s.notify = true } }

// maybeNotify issues a transactional NOTIFY when notify is enabled and the step
// produced outbox events. Errors propagate so the whole step rolls back.
func (s *Store) maybeNotify(ctx context.Context, db DBTX, events []runtime.OutboxEvent) error {
	if !s.notify || len(events) == 0 {
		return nil
	}
	// Channel name cannot be parameterized; it is a fixed constant.
	if _, err := db.Exec(ctx, "NOTIFY "+outboxNotifyChannel); err != nil {
		return fmt.Errorf("postgres: notify outbox: %w", err)
	}
	return nil
}
```

In `Create`, after the `writeOutbox(...)` call and before `tx.Commit`:

```go
	if err := s.maybeNotify(ctx, tx, step.Events); err != nil {
		return 0, err
	}
```

In `Commit`, after its `writeOutbox(...)` call and before `tx.Commit`:

```go
	if err := s.maybeNotify(ctx, tx, step.Events); err != nil {
		return 0, mapConflict(err)
	}
```

- [ ] **Step 4: Add the façade option**

In `persistence/persistence.go`:

```go
// WithOutboxNotify makes the Store emit a transactional NOTIFY wrkflw_outbox
// when a step inserts outbox rows, so a relay started with WithListenNotify
// drains with sub-poll-interval latency (ADR-0022). Opt-in; default off.
func WithOutboxNotify() Option { return postgres.WithOutboxNotify() }
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestStoreOutboxNotify`
Expected: PASS — the listener receives a `wrkflw_outbox` notification within the timeout.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/store.go persistence/persistence.go internal/persistence/postgres/notify_test.go
git commit -m "$(printf 'feat(persistence): transactional outbox NOTIFY on event insert (ADR-0022)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: Relay LISTEN wakeup with poll fallback (read side)

**Files:**
- Modify: `internal/persistence/postgres/relay.go` (add `listen` field + `WithListenNotify`, listener goroutine, `drainUntilEmpty`, new `select` case in `Run`)
- Modify: `persistence/persistence.go` (add `WithListenNotify`)
- Create: `internal/persistence/postgres/relay_listen_test.go` (testcontainers: relay with a long poll interval drains a freshly-inserted event quickly via NOTIFY)

**Interfaces:**
- Consumes: existing `Relay` struct, `RelayOption`, `Run`, `DrainOnce`; `outboxNotifyChannel` (Task 3).
- Produces: `postgres.WithListenNotify() RelayOption`; `persistence.WithListenNotify() RelayOption`.

- [ ] **Step 1: Write the failing test**

Create `internal/persistence/postgres/relay_listen_test.go`:

```go
package postgres_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/postgres"
	"github.com/kartaladev/wrkflw/runtime"
)

// countingPublisher records how many events it has published.
type countingPublisher struct{ n atomic.Int64 }

func (p *countingPublisher) Publish(_ context.Context, _ runtime.OutboxEvent) error {
	p.n.Add(1)
	return nil
}

func TestRelayListenDrainsBeforePollInterval(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	pub := &countingPublisher{}
	// A deliberately long poll interval: only a NOTIFY wakeup can drain in time.
	relay := postgres.NewRelay(pool, pub,
		postgres.WithPollInterval(30*time.Second),
		postgres.WithListenNotify(),
	)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = relay.Run(runCtx) }()

	// Give the listener a moment to LISTEN, then write an event WITH a NOTIFY.
	time.Sleep(200 * time.Millisecond)
	st := postgres.NewStore(pool, postgres.WithOutboxNotify())
	_, err := st.Create(t.Context(), runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "lr1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Now().UTC()},
		Trigger: startTrigger("lr1"),
		Events:  []runtime.OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"id": "lr1"}}},
	})
	require.NoError(t, err)

	// Must be published well before the 30s poll tick.
	require.Eventually(t, func() bool { return pub.n.Load() == 1 }, 5*time.Second, 25*time.Millisecond)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestRelayListen`
Expected: FAIL — `undefined: postgres.WithListenNotify`.

- [ ] **Step 3: Implement the listener + drain-until-empty + Run wiring**

In `internal/persistence/postgres/relay.go`, add the field to the `Relay` struct:

```go
	listen bool // wake the poll loop on NOTIFY wrkflw_outbox (ADR-0022)
```

Add the option (near the other `RelayOption`s):

```go
// WithListenNotify makes Run LISTEN on wrkflw_outbox (on a dedicated pool
// connection) and drain immediately when a Store with WithOutboxNotify announces
// new events, instead of waiting for the next poll tick. The poll interval stays
// as a fallback for missed notifications, restarts, and multi-worker fan-out
// (ADR-0022). Default: off (pure polling).
func WithListenNotify() RelayOption { return func(r *Relay) { r.listen = true } }
```

Add a drain-until-empty helper and a listener loop:

```go
// drainUntilEmpty repeatedly drains batches until DrainOnce reports an empty
// batch (coalescing a burst of notifications into one sweep) or an error.
func (r *Relay) drainUntilEmpty(ctx context.Context) error {
	for {
		n, err := r.DrainOnce(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// listenLoop holds a dedicated pool connection, LISTENs on the outbox channel,
// and signals wake on each notification. It reconnects on transient failures;
// the poll fallback covers any gap. It returns when ctx is cancelled.
func (r *Relay) listenLoop(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := r.pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "persistence: relay listen acquire failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", err))...)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+outboxNotifyChannel); err != nil {
			conn.Release()
			if ctx.Err() != nil {
				return
			}
			r.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "persistence: relay LISTEN failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", err))...)
			continue
		}
		for ctx.Err() == nil {
			if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
				break // connection lost or ctx done; outer loop reconnects
			}
			select {
			case wake <- struct{}{}:
			default: // a wake is already pending; coalesce
			}
		}
		conn.Release()
	}
}
```

Rewrite `Run` to add the wake case and use `drainUntilEmpty`:

```go
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	var wake chan struct{}
	if r.listen {
		wake = make(chan struct{}, 1)
		go r.listenLoop(ctx, wake)
	}

	// Attempt an immediate drain before waiting for the first signal.
	if err := r.drainUntilEmpty(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return ctx.Err()
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.drainUntilEmpty(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return ctx.Err()
				}
				return err
			}
		case <-wake:
			if err := r.drainUntilEmpty(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return ctx.Err()
				}
				return err
			}
		}
	}
}
```

> A `nil` `wake` channel (when `listen` is off) blocks forever in the `select`, so the `case <-wake` is inert — pure-poll behavior is unchanged when the option is off.

- [ ] **Step 4: Add the façade option**

In `persistence/persistence.go`:

```go
// WithListenNotify makes the relay LISTEN on wrkflw_outbox and drain on each
// NOTIFY (emitted by a Store configured with WithOutboxNotify), keeping the poll
// interval as a fallback (ADR-0022). Opt-in; default off.
func WithListenNotify() RelayOption { return postgres.WithListenNotify() }
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestRelayListen`
Run: `go test -p 1 ./internal/persistence/postgres/... -run TestRelay` (no regression in existing relay tests)
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/relay.go persistence/persistence.go internal/persistence/postgres/relay_listen_test.go
git commit -m "$(printf 'feat(persistence): LISTEN/NOTIFY relay wakeup over poll fallback (ADR-0022)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: `Ownership` port + `AlwaysOwn` in-process impl (pure)

**Files:**
- Create: `runtime/ownership.go`
- Create: `runtime/ownership_test.go`

**Interfaces:**
- Produces: `runtime.Ownership` interface (`Acquire(ctx, id) (bool, error)`, `Release(ctx, id) error`); `runtime.AlwaysOwn` (value type) satisfying it; compile-time `var _ Ownership = AlwaysOwn{}`.

- [ ] **Step 1: Write the failing test**

Create `runtime/ownership_test.go`:

```go
package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/runtime"
)

func TestAlwaysOwnAlwaysAcquires(t *testing.T) {
	var o runtime.Ownership = runtime.AlwaysOwn{}

	owned, err := o.Acquire(t.Context(), "any-instance")
	require.NoError(t, err)
	assert.True(t, owned)

	assert.NoError(t, o.Release(t.Context(), "any-instance"))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./runtime/... -run TestAlwaysOwn`
Expected: FAIL — `undefined: runtime.Ownership` / `undefined: runtime.AlwaysOwn`.

- [ ] **Step 3: Write the implementation**

Create `runtime/ownership.go`:

```go
package runtime

import "context"

// Ownership decides whether THIS process is the single writer for an instance,
// and therefore whether its mutable state may be cached and served from memory
// by a CachingStore (ADR-0020).
//
// Caching mutable instance state is safe only under a single-writer-per-instance
// guarantee: a stale cached read would otherwise drive a routing decision and
// fire side-effects before the version-CAS could reject the write. Ownership is
// that guarantee; the CAS is the backstop.
//
// Implementations MUST be sticky: Acquire is idempotent and O(1) for an
// already-owned instance (it must not cost a round-trip on the hot path), and
// ownership changes only on explicit Release (or process death).
type Ownership interface {
	// Acquire reports whether this process owns instanceID, taking ownership if
	// it is free. owned=false means another process owns it: do not cache.
	Acquire(ctx context.Context, instanceID string) (owned bool, err error)
	// Release relinquishes ownership of instanceID (triggers cache eviction).
	Release(ctx context.Context, instanceID string) error
}

// AlwaysOwn is the in-process Ownership for single-replica or sticky-routed
// deployments where this process is guaranteed to be the sole writer of every
// instance it touches. Acquire always returns true; Release is a no-op. It is
// correct and free for single-process embedding; multi-process deployments need
// a real lease (e.g. persistence.NewAdvisoryLockOwnership).
type AlwaysOwn struct{}

// Compile-time assertion.
var _ Ownership = AlwaysOwn{}

// Acquire always grants ownership.
func (AlwaysOwn) Acquire(context.Context, string) (bool, error) { return true, nil }

// Release is a no-op.
func (AlwaysOwn) Release(context.Context, string) error { return nil }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./runtime/... -run TestAlwaysOwn`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/ownership.go runtime/ownership_test.go
git commit -m "$(printf 'feat(runtime): Ownership port + AlwaysOwn in-process impl (ADR-0020)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: `CachingStore` decorator core

**Files:**
- Create: `runtime/caching_store.go`
- Create: `runtime/caching_store_test.go`

**Interfaces:**
- Consumes: `Store`, `AppliedStep`, `Token`, `ErrConcurrentUpdate`, `JournalReader`, `Ownership` (Task 5), `engine.InstanceState` (+ its `Clone()`), `clock.Clock`.
- Produces: `runtime.CachingStore` (satisfies `Store` and `JournalReader`); `runtime.NewCachingStore(backing Store, owner Ownership, clk clock.Clock, opts ...CachingStoreOption) *CachingStore`; `runtime.CachingStoreOption`; `runtime.WithCacheTTL(time.Duration) CachingStoreOption`; `runtime.WithCacheMaxEntries(int) CachingStoreOption`. Defaults: TTL 5m, maxEntries 1024.

- [ ] **Step 1: Write the failing tests**

Create `runtime/caching_store_test.go`:

```go
package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

// countingStore wraps a backing Store and counts Load calls (cache-miss proxy).
type countingStore struct {
	backing runtime.Store
	loads   atomic.Int64
}

func (c *countingStore) Create(ctx context.Context, s runtime.AppliedStep) (runtime.Token, error) {
	return c.backing.Create(ctx, s)
}
func (c *countingStore) Load(ctx context.Context, id string) (engine.InstanceState, runtime.Token, error) {
	c.loads.Add(1)
	return c.backing.Load(ctx, id)
}
func (c *countingStore) Commit(ctx context.Context, e runtime.Token, s runtime.AppliedStep) (runtime.Token, error) {
	return c.backing.Commit(ctx, e, s)
}

// neverOwn is an Ownership that never grants ownership (forces cache bypass).
type neverOwn struct{}

func (neverOwn) Acquire(context.Context, string) (bool, error) { return false, nil }
func (neverOwn) Release(context.Context, string) error         { return nil }

func runningState(id string) engine.InstanceState {
	return engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Unix(0, 0).UTC()}
}

func startTrg(id string) engine.Trigger { return engine.StartProcess{InstanceID: id, At: time.Unix(0, 0).UTC()} }
```

> Confirm the concrete start-trigger type name in `runtime`/`engine` (the existing tests construct one — match it; `engine.StartProcess` is illustrative). Adjust `startTrg` accordingly.

Append the behavior tests:

```go
func TestCachingStoreServesOwnedLoadFromCache(t *testing.T) {
	cs := &countingStore{backing: runtime.NewMemStore()}
	clk := clockwork.NewFakeClock()
	store := runtime.NewCachingStore(cs, runtime.AlwaysOwn{}, clk)

	id := "c1"
	_, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)

	// First owned Load after a write-through Create is a cache hit — no backing Load.
	st, _, err := store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, id, st.InstanceID)
	assert.Equal(t, int64(0), cs.loads.Load(), "owned Load should be served from the write-through cache")

	// A second Load is also a hit.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load())
}

func TestCachingStoreBypassesWhenNotOwned(t *testing.T) {
	cs := &countingStore{backing: runtime.NewMemStore()}
	store := runtime.NewCachingStore(cs, neverOwn{}, clockwork.NewFakeClock())

	id := "c2"
	_, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)

	// Not owned ⇒ every Load hits the backing.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cs.loads.Load())
}

func TestCachingStoreEvictsOnConcurrentUpdate(t *testing.T) {
	mem := runtime.NewMemStore()
	cs := &countingStore{backing: mem}
	store := runtime.NewCachingStore(cs, runtime.AlwaysOwn{}, clockwork.NewFakeClock())

	id := "c3"
	tok, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)

	// Advance the backing out-of-band so the cached token is stale.
	_, err = mem.Commit(t.Context(), tok, runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)

	// Commit via the cache with the stale token ⇒ ErrConcurrentUpdate ⇒ evict.
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)

	// Next owned Load must re-read the backing (entry was evicted).
	before := cs.loads.Load()
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./runtime/... -run TestCachingStore`
Expected: FAIL — `undefined: runtime.NewCachingStore`.

- [ ] **Step 3: Write the implementation**

Create `runtime/caching_store.go`:

```go
package runtime

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/engine"
)

// Compile-time assertions.
var (
	_ Store         = (*CachingStore)(nil)
	_ JournalReader = (*CachingStore)(nil)
)

const (
	defaultCacheTTL        = 5 * time.Minute
	defaultCacheMaxEntries = 1024
)

// CachingStore is a write-through, single-writer cache in front of a Store
// (ADR-0020). It is correct ONLY when each cached instance has exactly one
// writing process, which the Ownership port guarantees: only owned instances are
// cached/served; a non-owned instance bypasses the cache and reads the backing
// Store every time. The cache is bounded (LRU on entry count + TTL) and evicts an
// entry on ErrConcurrentUpdate (a stale token). Per-instance keyed serialization
// keeps the cache coherent under concurrent Load/Commit for the same instance.
type CachingStore struct {
	backing    Store
	owner      Ownership
	clk        clock.Clock
	ttl        time.Duration
	maxEntries int

	mu      sync.Mutex
	entries map[string]*cacheNode
	lru     *list.List // front = most recently used; Value = *cacheNode

	klMu     sync.Mutex
	keyLocks map[string]*keyLock
}

type cacheNode struct {
	id        string
	state     engine.InstanceState
	token     Token
	expiresAt time.Time // zero when ttl <= 0 (never expires)
	elem      *list.Element
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// CachingStoreOption configures a CachingStore.
type CachingStoreOption func(*CachingStore)

// WithCacheTTL sets the maximum age of a cached instance entry before it is
// reloaded from the backing Store. <= 0 disables TTL expiry. Default: 5m.
func WithCacheTTL(d time.Duration) CachingStoreOption { return func(c *CachingStore) { c.ttl = d } }

// WithCacheMaxEntries caps the number of cached instances (LRU eviction beyond
// the cap). <= 0 means unbounded. Default: 1024.
func WithCacheMaxEntries(n int) CachingStoreOption {
	return func(c *CachingStore) { c.maxEntries = n }
}

// NewCachingStore wraps backing with a single-writer, write-through cache gated
// by owner. clk drives TTL (use clock.System() in production, a fake clock in
// tests).
func NewCachingStore(backing Store, owner Ownership, clk clock.Clock, opts ...CachingStoreOption) *CachingStore {
	c := &CachingStore{
		backing:    backing,
		owner:      owner,
		clk:        clk,
		ttl:        defaultCacheTTL,
		maxEntries: defaultCacheMaxEntries,
		entries:    make(map[string]*cacheNode),
		lru:        list.New(),
		keyLocks:   make(map[string]*keyLock),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// lockFor returns an unlock func after taking a refcounted per-instance lock.
func (c *CachingStore) lockFor(id string) func() {
	c.klMu.Lock()
	kl := c.keyLocks[id]
	if kl == nil {
		kl = &keyLock{}
		c.keyLocks[id] = kl
	}
	kl.refs++
	c.klMu.Unlock()

	kl.mu.Lock()
	return func() {
		kl.mu.Unlock()
		c.klMu.Lock()
		kl.refs--
		if kl.refs == 0 {
			delete(c.keyLocks, id)
		}
		c.klMu.Unlock()
	}
}

// get returns a fresh cached node (moving it to the LRU front) or false.
func (c *CachingStore) get(id string) (*cacheNode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.entries[id]
	if !ok {
		return nil, false
	}
	if c.ttl > 0 && !c.clk.Now().Before(n.expiresAt) {
		c.removeLocked(n) // expired
		return nil, false
	}
	c.lru.MoveToFront(n.elem)
	return n, true
}

// put upserts an entry, refreshing TTL and evicting the LRU tail if over cap.
func (c *CachingStore) put(id string, state engine.InstanceState, token Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if c.ttl > 0 {
		exp = c.clk.Now().Add(c.ttl)
	}
	if n, ok := c.entries[id]; ok {
		n.state, n.token, n.expiresAt = state, token, exp
		c.lru.MoveToFront(n.elem)
		return
	}
	n := &cacheNode{id: id, state: state, token: token, expiresAt: exp}
	n.elem = c.lru.PushFront(n)
	c.entries[id] = n
	if c.maxEntries > 0 {
		for c.lru.Len() > c.maxEntries {
			c.removeLocked(c.lru.Back().Value.(*cacheNode))
		}
	}
}

func (c *CachingStore) evict(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.entries[id]; ok {
		c.removeLocked(n)
	}
}

// removeLocked drops a node; caller holds c.mu.
func (c *CachingStore) removeLocked(n *cacheNode) {
	c.lru.Remove(n.elem)
	delete(c.entries, n.id)
}

// Create delegates to the backing Store, then write-through caches the new state
// when this process owns the instance.
func (c *CachingStore) Create(ctx context.Context, step AppliedStep) (Token, error) {
	tok, err := c.backing.Create(ctx, step)
	if err != nil {
		return 0, err
	}
	if owned, oerr := c.owner.Acquire(ctx, step.State.InstanceID); oerr == nil && owned {
		c.put(step.State.InstanceID, step.State.Clone(), tok)
	}
	return tok, nil
}

// Load serves owned instances from cache (populating on a miss under the
// per-instance lock so a concurrent Commit cannot interleave a stale write).
// Non-owned instances bypass the cache entirely.
func (c *CachingStore) Load(ctx context.Context, id string) (engine.InstanceState, Token, error) {
	owned, err := c.owner.Acquire(ctx, id)
	if err != nil || !owned {
		return c.backing.Load(ctx, id) // bypass; do not populate
	}
	unlock := c.lockFor(id)
	defer unlock()
	if n, ok := c.get(id); ok {
		return n.state.Clone(), n.token, nil
	}
	st, tok, lerr := c.backing.Load(ctx, id)
	if lerr != nil {
		return engine.InstanceState{}, 0, lerr
	}
	c.put(id, st.Clone(), tok)
	return st, tok, nil
}

// Commit delegates under the per-instance lock; on success it write-through
// caches the new state, on ErrConcurrentUpdate it evicts the stale entry.
func (c *CachingStore) Commit(ctx context.Context, expected Token, step AppliedStep) (Token, error) {
	id := step.State.InstanceID
	unlock := c.lockFor(id)
	defer unlock()
	tok, err := c.backing.Commit(ctx, expected, step)
	if err != nil {
		if errors.Is(err, ErrConcurrentUpdate) {
			c.evict(id)
		}
		return 0, err
	}
	if owned, oerr := c.owner.Acquire(ctx, id); oerr == nil && owned {
		c.put(id, step.State.Clone(), tok)
	}
	return tok, nil
}

// Entries forwards to the backing Store's JournalReader if it implements one;
// the journal is never cached. Returns an error if the backing is not a reader.
func (c *CachingStore) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
	jr, ok := c.backing.(JournalReader)
	if !ok {
		return nil, errors.New("runtime: backing store is not a JournalReader")
	}
	return jr.Entries(ctx, id)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./runtime/... -run TestCachingStore`
Expected: PASS (serve-from-cache, bypass-when-not-owned, evict-on-CAS).

- [ ] **Step 5: Commit**

```bash
git add runtime/caching_store.go runtime/caching_store_test.go
git commit -m "$(printf 'feat(runtime): CachingStore single-writer write-through Store decorator (ADR-0020)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 7: CachingStore bounding (TTL + LRU) and concurrency coherence

**Files:**
- Modify: `runtime/caching_store_test.go` (add TTL, LRU, and concurrent-coherence tests — no production change expected; if a test surfaces a bug, fix `caching_store.go` under TDD)

**Interfaces:**
- Consumes: everything from Task 6 + `WithCacheTTL`, `WithCacheMaxEntries`.

- [ ] **Step 1: Write the failing tests**

Append to `runtime/caching_store_test.go`:

```go
func TestCachingStoreTTLExpiryForcesReload(t *testing.T) {
	cs := &countingStore{backing: runtime.NewMemStore()}
	clk := clockwork.NewFakeClock()
	store := runtime.NewCachingStore(cs, runtime.AlwaysOwn{}, clk, runtime.WithCacheTTL(time.Minute))

	id := "ttl1"
	_, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)

	_, _, err = store.Load(t.Context(), id) // hit (write-through)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load())

	clk.Advance(2 * time.Minute) // expire the entry
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cs.loads.Load(), "expired entry must reload from backing")
}

func TestCachingStoreLRUEvictsBeyondMax(t *testing.T) {
	cs := &countingStore{backing: runtime.NewMemStore()}
	store := runtime.NewCachingStore(cs, runtime.AlwaysOwn{}, clockwork.NewFakeClock(),
		runtime.WithCacheMaxEntries(2), runtime.WithCacheTTL(time.Hour))

	for _, id := range []string{"a", "b", "c"} { // 3 instances, cap 2
		_, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
		require.NoError(t, err)
	}
	// "a" was the least-recently-used after inserting c ⇒ evicted ⇒ its Load misses.
	before := cs.loads.Load()
	_, _, err := store.Load(t.Context(), "a")
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
	// "c" is still cached ⇒ hit.
	_, _, err = store.Load(t.Context(), "c")
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
}

func TestCachingStoreConcurrentLoadCommitStayCoherent(t *testing.T) {
	mem := runtime.NewMemStore()
	store := runtime.NewCachingStore(mem, runtime.AlwaysOwn{}, clockwork.NewFakeClock(), runtime.WithCacheTTL(time.Hour))

	id := "race1"
	tok, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)

	// Hammer Load while a single Commit advances the token; the cache must never
	// serve a token greater than what the backing holds (no torn write-through).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			st, ltok, lerr := store.Load(t.Context(), id)
			require.NoError(t, lerr)
			require.Equal(t, id, st.InstanceID)
			_ = ltok
		}
	}()
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{State: runningState(id), Trigger: startTrg(id)})
	require.NoError(t, err)
	<-done
}
```

- [ ] **Step 2: Run the tests to verify they fail / pass**

Run: `go test -race ./runtime/... -run TestCachingStore`
Expected: the TTL and LRU tests FAIL only if Task 6 logic is wrong; with the Task 6 implementation they should PASS. The race test must pass under `-race` with no data race. If any fails, fix `caching_store.go` (write the failing assertion first, observe red, then fix).

- [ ] **Step 3: Commit**

```bash
git add runtime/caching_store_test.go runtime/caching_store.go
git commit -m "$(printf 'test(runtime): CachingStore TTL, LRU, and concurrent-coherence coverage (ADR-0020)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 8: Postgres advisory-lock `Ownership` + façade constructor

**Files:**
- Create: `internal/persistence/postgres/ownership.go`
- Create: `internal/persistence/postgres/ownership_test.go` (testcontainers contention)
- Modify: `persistence/persistence.go` (add `NewAdvisoryLockOwnership`)

**Interfaces:**
- Consumes: `runtime.Ownership` (Task 5), `*pgxpool.Pool`, `*pgxpool.Conn`.
- Produces: `postgres.AdvisoryLockOwnership` (satisfies `runtime.Ownership` + `io.Closer`); `postgres.NewAdvisoryLockOwnership(ctx, pool) (*AdvisoryLockOwnership, error)`; `persistence.NewAdvisoryLockOwnership(ctx, pool) (runtime.Ownership, io.Closer, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/persistence/postgres/ownership_test.go`:

```go
package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	"github.com/kartaladev/wrkflw/internal/persistence/postgres"
)

func TestAdvisoryLockOwnershipContention(t *testing.T) {
	pool := database.RunTestDatabase(t)

	// Two independent "processes", each with its own dedicated session connection.
	procA, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procA.Close()
	procB, err := postgres.NewAdvisoryLockOwnership(t.Context(), pool)
	require.NoError(t, err)
	defer procB.Close()

	id := "owned-instance"

	ownedA, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedA, "A acquires the free instance")

	// Sticky: A re-acquiring is true with no contention.
	again, err := procA.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, again)

	// B cannot acquire while A holds the lock.
	ownedB, err := procB.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.False(t, ownedB, "B is blocked while A owns")

	// After A releases, B can acquire.
	require.NoError(t, procA.Release(t.Context(), id))
	ownedB2, err := procB.Acquire(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, ownedB2, "B acquires after A releases")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestAdvisoryLockOwnership`
Expected: FAIL — `undefined: postgres.NewAdvisoryLockOwnership`.

- [ ] **Step 3: Write the implementation**

Create `internal/persistence/postgres/ownership.go`:

```go
package postgres

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/runtime"
)

// Compile-time assertion.
var _ runtime.Ownership = (*AdvisoryLockOwnership)(nil)

// AdvisoryLockOwnership implements runtime.Ownership for multi-process
// deployments using Postgres session-level advisory locks (ADR-0020). It holds
// one dedicated pool connection for its whole lifetime; every owned instance is
// a pg_advisory_lock on that session. If the process dies the connection drops
// and Postgres auto-releases all its locks (natural fencing); the version-CAS
// rejects any stale in-flight Commit from the usurped owner.
//
// Acquire is sticky: an already-held instance returns true from an in-memory set
// without a round-trip.
type AdvisoryLockOwnership struct {
	conn *pgxpool.Conn

	mu   sync.Mutex
	held map[string]bool
}

// NewAdvisoryLockOwnership acquires a dedicated session connection from pool and
// returns an Ownership backed by advisory locks on it. Call Close to unlock all
// held instances and return the connection to the pool.
func NewAdvisoryLockOwnership(ctx context.Context, pool *pgxpool.Pool) (*AdvisoryLockOwnership, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: ownership: acquire session conn: %w", err)
	}
	return &AdvisoryLockOwnership{conn: conn, held: make(map[string]bool)}, nil
}

// Acquire takes a session advisory lock for instanceID (sticky: an already-held
// id returns true without a round-trip). owned=false means another session holds
// the lock.
func (o *AdvisoryLockOwnership) Acquire(ctx context.Context, instanceID string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.held[instanceID] {
		return true, nil
	}
	var ok bool
	if err := o.conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, instanceID,
	).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: ownership: try lock %q: %w", instanceID, err)
	}
	if ok {
		o.held[instanceID] = true
	}
	return ok, nil
}

// Release drops the session advisory lock for instanceID (no-op if not held).
func (o *AdvisoryLockOwnership) Release(ctx context.Context, instanceID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.held[instanceID] {
		return nil
	}
	if _, err := o.conn.Exec(ctx,
		`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, instanceID,
	); err != nil {
		return fmt.Errorf("postgres: ownership: unlock %q: %w", instanceID, err)
	}
	delete(o.held, instanceID)
	return nil
}

// Close releases every held lock and returns the dedicated connection to the pool.
func (o *AdvisoryLockOwnership) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for id := range o.held {
		_, _ = o.conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, id)
		delete(o.held, id)
	}
	o.conn.Release()
	return nil
}
```

- [ ] **Step 4: Add the façade constructor**

In `persistence/persistence.go` add the import `"io"` if not present, and:

```go
// NewAdvisoryLockOwnership constructs a multi-process runtime.Ownership backed by
// Postgres session advisory locks (ADR-0020), for use with runtime.NewCachingStore
// across multiple replicas sharing one database. It holds a dedicated pool
// connection for its lifetime; close the returned io.Closer at shutdown to release
// every held lock and return the connection.
//
//	owner, closer, _ := persistence.NewAdvisoryLockOwnership(ctx, pool)
//	defer closer.Close()
//	store := runtime.NewCachingStore(pgStore, owner, clock.System())
func NewAdvisoryLockOwnership(ctx context.Context, pool *pgxpool.Pool) (runtime.Ownership, io.Closer, error) {
	o, err := postgres.NewAdvisoryLockOwnership(ctx, pool)
	if err != nil {
		return nil, nil, err
	}
	return o, o, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -p 1 ./internal/persistence/postgres/... -run TestAdvisoryLockOwnership`
Run: `go test ./persistence/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/ownership.go internal/persistence/postgres/ownership_test.go persistence/persistence.go
git commit -m "$(printf 'feat(persistence): advisory-lock Ownership for multi-process caching (ADR-0020)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 9: Track verification gate + HANDOVER update

**Files:**
- Modify: `docs/plans/HANDOVER.md` (mark the Performance/caching track complete; record deferred follow-ups)
- Create: `runtime/caching_store_example_test.go` (a testable `ExampleNewCachingStore` showing consumer wiring: `NewCachingStore(memStore, AlwaysOwn{}, clock.System())` passed to a `Runner`, a park→resume cycle served from cache)

**Interfaces:** none new — this task verifies and documents.

- [ ] **Step 1: Write the testable example**

Create `runtime/caching_store_example_test.go` with an `ExampleNewCachingStore` that wires `runtime.NewCachingStore` over a `MemStore` with `AlwaysOwn{}`, drives one instance through a park-and-resume (e.g. a timer or human-task definition already used by existing runtime examples), and prints a deterministic terminal status. Model it on an existing `runtime` example test for the definition/runner setup; keep `// Output:` exact.

- [ ] **Step 2: Run the full verification gate**

```bash
go test -race ./runtime/... ./persistence/... && \
go test -race -p 1 ./internal/persistence/postgres/... && \
go test -coverprofile=cover.out ./runtime/... ./persistence/... ./internal/persistence/postgres/... && go tool cover -func=cover.out | tail -1 && \
golangci-lint run ./...
```

Expected: all green; coverage ≥85% on `runtime`, `persistence`, `internal/persistence/postgres`; lint 0 issues.

- [ ] **Step 3: Verify engine/model purity is intact**

```bash
go test ./engine/... -run TestCorePurity
go list -f '{{.Deps}}' ./engine/... ./model/... | tr ' ' '\n' | grep -E 'watermill|casbin|gocron|clockwork' || echo "PURE: no forbidden vendor in engine/model deps"
```

Expected: purity test green; the grep prints the `PURE:` line (no matches).

- [ ] **Step 4: Update HANDOVER.md**

Add a "Performance/caching sub-project — ✅ COMPLETE" section to `docs/plans/HANDOVER.md` mirroring the prior track sections: what shipped (CachingStore + Ownership/AlwaysOwn/advisory-lock, open-visit history cap, LISTEN/NOTIFY), ADRs 0020–0022, the gate result, and the deferred follow-ups (lease-column ownership alternative; per-worker push fairness; `Store` Load/Commit spans — Observability follow-up #7; history-cap per-definition granularity). Flip the resume-point bullet #4 to ✅ and set the next track.

- [ ] **Step 5: Commit**

```bash
git add docs/plans/HANDOVER.md runtime/caching_store_example_test.go cover.out
git restore --staged cover.out 2>/dev/null || true
git commit -m "$(printf 'docs(perf): mark Performance/caching track complete + wiring example\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

## Verification checklist (whole track)

- [ ] Task 1 — `capHistory` keeps all open visits + last N closed; `n<=0` no-op; input not mutated.
- [ ] Task 2 — `WithHistoryCap` caps the persisted JSONB; default (unset) preserves full history; façade `OpenPostgres(...Option)` threads it.
- [ ] Task 3 — `WithOutboxNotify` emits transactional `NOTIFY` only when events were inserted; a LISTEN connection receives it.
- [ ] Task 4 — `WithListenNotify` relay drains a freshly-inserted event well before a 30s poll tick; poll fallback preserved; existing relay tests green.
- [ ] Task 5 — `Ownership` port + `AlwaysOwn` (always owns, no-op release).
- [ ] Task 6 — `CachingStore` serves owned Loads from the write-through cache (0 backing Loads), bypasses when not owned, evicts on `ErrConcurrentUpdate`.
- [ ] Task 7 — TTL expiry forces reload; LRU evicts beyond cap; concurrent Load/Commit coherent under `-race`.
- [ ] Task 8 — advisory-lock ownership: A acquires, B blocked, B acquires after A releases; Acquire sticky.
- [ ] Task 9 — full gate green (race, ≥85% coverage, lint 0); engine/model purity intact; HANDOVER updated; testable example passes.
- [ ] No new import of `watermill`/`casbin`/`gocron`/`clockwork` in production code; `engine`/`model` unchanged.

## Self-review notes (plan author)

- **Spec coverage:** §3 cache → Tasks 5–8; §4 history cap → Tasks 1–2; §5 NOTIFY → Tasks 3–4; §6 layout honored (pure in `runtime/`, Postgres in `internal/` + façade); §8 verification → Task 9. All covered.
- **Type consistency:** `Ownership.Acquire/Release`, `CachingStore`, `NewCachingStore`, `WithCacheTTL`/`WithCacheMaxEntries`, `WithHistoryCap`, `WithOutboxNotify`, `WithListenNotify`, `NewAdvisoryLockOwnership` are named identically across producing and consuming tasks.
- **Known confirm-points (flagged inline for the implementer):** the concrete start-trigger type name (`engine.StartProcess` is illustrative) and the existing `startTrigger` test helper name in the postgres package — verify against current code before writing tests; the engine grew a lot, so trust the test/red state over the listing (HANDOVER "hard-won lesson").
