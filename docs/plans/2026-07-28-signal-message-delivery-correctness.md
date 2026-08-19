# Signal & Message Delivery Correctness — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development`
> to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **TDD is audited from the transcript** — every task's RED step must be a visible
> `Bash` run of `go test` that FAILS before any implementation is written. See
> CLAUDE.md § "TDD Operational Discipline".

**Goal:** Make signal and message delivery restart-safe and multi-replica-safe, stop
silently dropping wake-ups, and fire every matching signal arm rather than the first.

**Architecture:** Replace two per-replica in-memory waiter maps with one durable
projection of the committed snapshot (`wrkflw_waiters`), written inside the
state-commit transaction and read authoritatively at delivery time. Collapse
`signal.SignalBus` and the inlined message correlation into one `runtime/delivery.Bus`
parameterised by policy (`Broadcast`/`Selective`/`Exclusive`). Add a durable
undelivered-wakeup channel with non-restamping replay. Change `handleSignalReceived`
tiers 1–3 from first-match to snapshot-then-fire-each.

**Tech Stack:** Go 1.25 · PostgreSQL 17 / MySQL 8 / SQLite (`modernc.org/sqlite`) ·
`expr-lang/expr` · `jonboulle/clockwork` · `uber-go/mock` · `stretchr/testify` ·
`testcontainers-go` via `internal/dbtest`

**Source documents:**
`docs/specs/2026-07-28-signal-message-delivery-correctness.md`,
ADR-0155 (durable waiter projection), ADR-0156 (unified delivery bus + message
semantics), ADR-0157 (undelivered-wakeup channel), ADR-0158 (signal fires every
matching arm).

---

## Global Constraints

Every task's requirements implicitly include this section.

- **Go 1.25.** Module `github.com/kartaladev/wrkflw`. One `go.mod` at the repo root.
- **No `pkg/` prefix** (ADR-0004). Public packages live at the module root.
- **The engine core must not import `clockwork`, transports, or storage vendors.**
  Enforced by `engine/purity_test.go` — it must still pass after Task 1.
- **Never import watermill, casbin, or gocron from engine/runtime code.**
- **Tests are black-box** (`package <name>_test`) unless the test genuinely needs
  unexported symbols.
- **Table tests use the project `table-test` form**: an `assert` closure per case
  (`func(t *testing.T, result T, err error)`), **never** `want`/`wantErr` fields;
  `testify/require` for preconditions, `testify/assert` for independent checks;
  `t.Context()` inside subtests, **never** `context.Background()`; a `ctx` modifier
  field `func(context.Context) context.Context` for context-sensitive components,
  with at least one cancelled-context case. Two or more cases exercising the same
  SUT call ⇒ the table form is mandatory.
- **Mocks are generated** with `mockgen` v0.6.0+, `--typed`, `--source` mode, placed
  **in the same package as the interface** as `<file>_mock.go`, driven by a
  `//go:generate` directive on line 3 of the source file. Never hand-write a mock,
  never edit a generated one. Existing examples: `service/deadletter.go` →
  `service/deadletter_mock.go`.
- **Database tests use `internal/dbtest`** — never an ad-hoc container:
  - `dbtest.RunTestDatabase(t, opts...) *pgxpool.Pool` (Postgres)
  - `dbtest.RunTestMySQL(t) *sql.DB` / `dbtest.RunTestMySQLDSN(t) string`
  - `dbtest.RunTestSQLite(t) *sql.DB` (pure-Go, no Docker)
- **SQLite timestamps** go through `timeArg` / `parseTimeText`
  (`internal/persistence/store/time_codec.go`), gated on
  `dialect.Dialect.TimestampsAsText` — **never** compare `dialect.Dialect.Name` to
  `"sqlite"`. Fixed-width 9-digit fraction; `time.RFC3339Nano` does **not** sort
  lexicographically (ADR-0151).
- **SQL is written once with `?` placeholders** and run through
  `dialect.Dialect.Rebind`.
- **Transactional writes join the ambient transaction** with
  `transaction.JoinOrBegin(ctx, conn)`, following the `TimerStore.UpsertJob` shape
  (`internal/persistence/store/timerstore.go`).
- **Exactly one migration file per dialect** (ADR-0132), enforced by
  `TestMigrations_OneFilePerDialect`. New tables are folded into the existing
  `0001_init.sql` of each dialect — do **not** add `0002_*.sql`.
- **Error sentinels** use the `workflow-<pkg>: ...` prefix.
- **Naming: spell identifiers out.** `IDGenerator`, not `IDGen`. One name across
  Go / JSON / SQL (`CorrelationKey` / `correlation_key`).
- **`DeadLetter` is already taken** by `monitor.DeadLetter` (outbound outbox).
  This bundle's inbound concept is `UndeliveredWakeup`. Never reuse `DeadLetter`.
- **Verification per task:** `go test -race ./<pkg>/...` green, `golangci-lint run ./...`
  clean. Coverage floor 85 % on touched packages, but hot paths and their error
  branches come first (Golang rule #8).
- **`${SCRATCH}`** is this session's scratchpad directory. Export it once before
  starting; every verification step writes to its own file under it so parallel
  tasks and successive RED/GREEN runs cannot clobber each other's evidence.
  **Never `/tmp`.**
- **Exit codes are read from `$?` after a redirect, never through a pipe.**
  `go test … | tail -20; echo $?` reports *tail's* status — a green report over a
  failing suite. This project has been burned by it; see the memory note
  *"verify via exit code, not a pipeline"*.
- **Docker must be running** for Tasks 4–7 and 10 (Postgres/MySQL testcontainers).
  Only `dbtest.RunTestSQLite` is pure-Go.
- **Coverage floors, per package:** `runtime/delivery` ≥ 90 %, `runtime/kernel`
  ≥ 87 %, `engine` ≥ 90 %, **`runtime` ≥ 93 %** (its current level — do not
  regress), **`internal/persistence/store` ≥ 85 %**.

---

## Shared Interfaces (defined in Task 2 / Task 3, consumed everywhere)

Copied verbatim so a task implementer reading out of order sees the exact names.

```go
// runtime/kernel/waiterstore.go
type WaiterKind int

const (
	WaiterSignal  WaiterKind = iota // broadcast-by-name, never correlated
	WaiterMessage                   // correlated, optionally keyed
)

type Waiter struct {
	Kind           WaiterKind
	Name           string
	CorrelationKey string
}

// WaiterFilter pages a waiter lookup. Limit ≤0 → 50, >200 → 200 via
// kernel.NormalizeLimit; Cursor "" starts from the beginning. Paging exists so a
// single Publish never materialises an unbounded recipient set (ADR-0156).
type WaiterFilter struct {
	Limit  int
	Cursor string
}

// WaiterPage is one page of instance IDs, ascending, for deterministic fan-out.
type WaiterPage struct {
	InstanceIDs []string
	NextCursor  string
	HasMore     bool
}

type WaiterStore interface {
	SignalWaiters(ctx context.Context, name string, f WaiterFilter) (WaiterPage, error)
	MessageWaiters(ctx context.Context, name, correlationKey string, f WaiterFilter) (WaiterPage, error)
}

type WaiterWriter interface {
	ReplaceWaiters(ctx context.Context, instanceID string, ws []Waiter) error
}

// WaiterProjection is what WithWaiterStore requires: BOTH halves. A read-only
// implementation would leave the projection unwritten and silently disable ALL
// delivery, so construction fails rather than nil-guarding it into a no-op.
type WaiterProjection interface {
	WaiterStore
	WaiterWriter
}
```

```go
// runtime/kernel/undelivered.go
type UndeliveredWakeup struct {
	ID             string
	InstanceID     string
	Kind           WaiterKind
	Name           string
	CorrelationKey string
	Payload        map[string]any
	OccurredAt     time.Time // ORIGINAL publish instant — replay reuses it verbatim
	FailedAt       time.Time
	Attempts       int
	Cause          string
}

type UndeliveredFilter struct {
	InstanceID string // "" = all
	Limit      int    // ≤0 → 50, >200 → 200 (kernel.NormalizeLimit)
	Cursor     string
}

type UndeliveredPage struct {
	Items      []UndeliveredWakeup
	NextCursor string
	HasMore    bool
}

type UndeliveredStore interface {
	Record(ctx context.Context, u UndeliveredWakeup) error
	List(ctx context.Context, f UndeliveredFilter) (UndeliveredPage, error)
	Delete(ctx context.Context, id string) error
}
```

```go
// runtime/delivery/bus.go
type Policy int

const (
	Broadcast Policy = iota // every waiter on name; selector ignored
	Selective               // every waiter whose selector matches
	Exclusive               // exactly one; ErrAmbiguousMessageCorrelation if several
)

// MessageDeliveryMode is the DRIVER-level setting. It is a distinct two-valued
// type, NOT delivery.Policy: with `Broadcast Policy = iota`, an uninitialised
// Policy field would make DeliverMessage resolve recipients via SignalWaiters and
// never consult message waiters at all. Broadcast must be unrepresentable here.
type MessageDeliveryMode int

const (
	ModeSelective MessageDeliveryMode = iota + 1 // default; set explicitly in NewProcessDriver
	ModeExclusive
)

type DeliverFunc func(ctx context.Context, instanceID string, trg engine.Trigger) error

func NewBus(deliver DeliverFunc, waiters kernel.WaiterStore, opts ...Option) (*Bus, error)

// Publish takes payload as its OWN parameter, not captured in mk: the escalation
// ladder must record it on an UndeliveredWakeup, and a closure-captured payload is
// not in scope at the recording site (ADR-0157).
func (b *Bus) Publish(ctx context.Context, k kernel.WaiterKind, name, selector string,
	payload map[string]any, p Policy,
	mk func(at time.Time, payload map[string]any) engine.Trigger) error
```

Options: `WithClock`, `WithUndeliveredStore`, `WithIDGenerator`, `WithMaxAttempts`,
**`WithMaxFanout(n)`** (hard recipient bound; errors past it), `WithLogger`,
`WithWaiterWriter` (for the self-heal path).

---

## File Structure

**Created**

| Path | Responsibility |
|---|---|
| `runtime/kernel/waiterstore.go` | `Waiter`, `WaiterKind`, `WaiterStore`, `WaiterWriter`, `MemWaiterStore` |
| `runtime/kernel/waiterstore_mock.go` | generated |
| `runtime/kernel/undelivered.go` | `UndeliveredWakeup`, `UndeliveredStore`, filter/page, `MemUndeliveredStore` |
| `runtime/kernel/undelivered_mock.go` | generated |
| `runtime/delivery/bus.go` | `Bus`, `Policy`, `Publish`, escalation ladder |
| `runtime/delivery/doc.go` | package doc |
| `runtime/waiterops.go` | `waitersOf`, `RehydrateWaiters` |
| `runtime/undeliveredops.go` | `ListUndelivered`, `ReplayUndelivered`, `DeleteUndelivered` |
| `internal/persistence/store/waiterstore.go` | SQL `WaiterStore` + `WaiterWriter` |
| `internal/persistence/store/undelivered.go` | SQL `UndeliveredStore` |

**Modified**

| Path | Change |
|---|---|
| `engine/step_triggers.go` | tiers 1–3 snapshot-then-fire-each (Task 1) |
| `engine/state_arms.go` | plural `armsBySignal` helpers (Task 1) |
| `internal/persistence/store/migrations/{postgres,mysql,sqlite}/0001_init.sql` | two new tables (Task 4) |
| `internal/persistence/store/pruner.go` | `PruneWaiters`, `PruneUndelivered` (Task 7) |
| `runtime/processdriver.go` | fields, wiring, commit-tx projection write (Task 8) |
| `runtime/processdriver_options.go` | new options, `WithSignalBus` removed (Task 8) |
| `runtime/processdriver_signal.go` | `BroadcastSignal` via bus (Task 8) |
| `runtime/processdriver_message.go` | `DeliverMessage` fan-out + always-start (Task 8) |
| `persistence/{persistence,mysql,sqlite}.go` | facade constructors (Task 7) |
| `processtest/harness.go` | drop `WithSignalBus`, wire `WithWaiterStore` (Task 9) |
| `examples/scenarios/*` | rewire (Task 9) |

**Deleted**

- `runtime/signal/` (whole package), `runtime/processdriver_waiters.go`

---

## Phase map

| Phase | Tasks | Mode |
|---|---|---|
| A — independent engine fix | 1 | one subagent, parallel with B |
| B — foundations | 2, 3 | one subagent each, parallel with A |
| C — persistence | 4, then 5 & 6 in parallel, then 7 | subagents |
| D — bus | 8a | subagent (after B) |
| E — repo-wide rewire | 8b, 9 | **INLINE in the controller** (compile-breaking) |
| F — integration proof | 10, 11 | subagents |

Task 4 (migrations) is serial before 5 and 6 because all three touch the same three
`.sql` files.

---

### Task 1: Signal fires every matching arm per family (ADR-0158)

Fully independent of every other task — no new ports, no storage, no transport.

**Files:**
- Modify: `engine/state_arms.go` (add plural lookups next to `armBySignal`, ~line 219)
- Modify: `engine/step_triggers.go:684-757` (`handleSignalReceived` tiers 1–3)
- Test: `engine/step_triggers_signal_fanout_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing consumed by later tasks. `armsBySignal` stays unexported.

- [ ] **Step 1: Write the failing test**

Create `engine/step_triggers_signal_fanout_test.go`. Two hosts, one signal name,
both boundaries must fire.

```go
package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// TestHandleSignalReceived_FiresEveryMatchingArm pins ADR-0158: a broadcast must
// fire EVERY matching arm in a family, not just the first. Before ADR-0158 tiers
// 1-3 used singular armBySignal lookups, so a parallel fork with two hosts each
// carrying an interrupting signal boundary on the same name interrupted only one.
func TestHandleSignalReceived_FiresEveryMatchingArm(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "two interrupting signal boundaries on the same name both fire",
			def:  twoHostsWithSignalBoundaries(t, "escalate"),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				// Both hosts consumed, both boundary targets now hold a token.
				assert.ElementsMatch(t,
					[]string{"after-boundary-a", "after-boundary-b"},
					activeNodeIDs(res.State),
					"both signal boundaries must fire on one broadcast")
			},
		},
		{
			name: "non-interrupting boundary fires exactly once and stays armed",
			def:  oneHostWithNonInterruptingSignalBoundary(t, "remind"),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, 1, countTokensAt(res.State, "reminder-sent"),
					"a repeatable arm must fire once per delivery, not loop")
				assert.True(t, hasArmedSignalBoundary(res.State, "remind"),
					"ADR-0124: a non-interrupting arm stays armed after firing")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			st := startAndPark(t, tc.def)
			trg := engine.NewSignalReceived(fixedNow, signalNameOf(tc.def), nil)

			res, err := engine.Step(ctx, tc.def, st, trg)
			tc.assert(t, res, err)
		})
	}
}
```

> ⚠ **None of those fixture names exist** (audit F2). The real helpers are
> `parkedAtUserTask` (`engine/close_kind_test.go:31`), `stepToParked`
> (`engine/boundary_error_matching_test.go:148`), `findTokenByNodeID`
> (`engine/retry_test.go:63`), `interruptingMessageBoundaryDef`
> (`engine/step_boundaries_test.go:49`), `nonInterruptingMessageBoundaryDef`
> (`:235`), `closeKindOf` (`engine/close_kind_test.go:20`). Build the two-host
> signal fixture by copying `interruptingMessageBoundaryDef` and swapping the
> message boundary for a signal boundary; assert via `findTokenByNodeID` +
> `closeKindOf`, not the invented `activeNodeIDs`/`countTokensAt`.
> `fullyPopulatedBoundaryArm` is white-box (`package engine`) — prefer black-box
> and assert through `engine.StepResult`.
>
> Call `engine.Step` with **five** arguments: `Step(ctx, def, st, trg, engine.StepOptions{})`.

**Task 1 changes tiers 1, 2 AND 3 — the table must cover all three, plus their
interaction.** The two cases above exercise tier 2 only. These six are mandatory
(audit A25, five of them Critical):

| case | assertion |
|---|---|
| **two event-gateway arms on one signal name both resolve** (tier 1) | both catch nodes advance; neither gateway token is left parked |
| **two event-sub-process arms in sibling scopes both fire** (tier 3) | both sub-process start tokens appear in `res.State` |
| **one broadcast fires a gateway arm, a boundary arm, an event-sub arm AND a parked token** | all four effects present in a single `StepResult` — the cross-family interaction ADR-0158 makes observable |
| **an interrupting boundary removes a snapshotted sibling arm on the same host** | `require.NoError`; the removed arm contributes no commands; a vanished snapshot entry is a skip, never an error |
| **interrupting arms fire before non-interrupting ones** | two boundary arms on one host, declared non-interrupting-first in `def.Nodes`: exactly **one** token results, proving order is by interrupt-ness and not by scan order |
| **a delivery matching nothing merges no variables** | `assert.Equal(t, before.Variables, res.State.Variables)` and `assert.Empty(t, res.Commands)` — pins merge-once-on-first-match |

And one regression guard, because ADR-0158 must **not** change the other two
trigger kinds:

| case | assertion |
|---|---|
| **a `MessageReceived` with two matching boundary arms still fires only the FIRST** | pins `dispatchArmCascade` (`engine/step_arm_dispatch.go`) against accidental widening — message stays point-to-point within an instance |

- [ ] **Step 2: Run the test to verify it FAILS**

```bash
go test -run '^TestHandleSignalReceived_FiresEveryMatchingArm$' ./engine/... > ${SCRATCH}/step-1.txt 2>&1; echo "exit=$?"; tail -30 ${SCRATCH}/step-1.txt
```

Expected: FAIL. The first case asserts two boundary targets but only one fires.
(A compile error from a missing fixture helper is *not* the red state you want —
add the helper, re-run, and get the assertion failure.)

- [ ] **Step 3: Add the plural arm lookups**

In `engine/state_arms.go`, beside `armBySignal`:

```go
// armsBySignal returns pointers to EVERY arm whose embedded signal name equals
// name, in slice order. An empty name matches no arm (ADR-0152) — see armByTimer.
//
// Unlike armBySignal (first match), this is the broadcast form required by
// ADR-0158: a signal is broadcast, so every matching arm in a family fires. The
// caller MUST snapshot arm identities from this result before dispatching,
// because firing one arm can remove others (an interrupting boundary cancels its
// host's siblings) and a non-interrupting arm deliberately stays armed
// (ADR-0124), so re-scanning would never terminate.
func armsBySignal[T any, PT armMatchable[T]](arms []T, name string) []*T {
	if name == "" {
		return nil
	}
	var out []*T
	for i := range arms {
		if PT(&arms[i]).matchPtr().Signal == name {
			out = append(out, &arms[i])
		}
	}
	return out
}
```

Then the three per-family wrappers, returning **identity snapshots** rather than
pointers, so a later mutation cannot invalidate them:

```go
// signalArmIdentity locates one arm of a family after other arms may have been
// removed by an earlier fire in the same delivery.
type gatewayArmID struct{ GatewayToken, CatchNode string }
type boundaryArmID struct{ HostToken, BoundaryNode string }
type eventSubArmID struct{ EnclosingScopeID, EventSubprocessNode string }

func (s *InstanceState) armedEventIDsBySignal(name string) []gatewayArmID {
	var out []gatewayArmID
	for _, ae := range armsBySignal(s.ArmedEvents, name) {
		out = append(out, gatewayArmID{ae.GatewayToken, ae.CatchNode})
	}
	return out
}

func (s *InstanceState) armedEventByID(id gatewayArmID) *armedEvent {
	for i := range s.ArmedEvents {
		if s.ArmedEvents[i].GatewayToken == id.GatewayToken &&
			s.ArmedEvents[i].CatchNode == id.CatchNode {
			return &s.ArmedEvents[i]
		}
	}
	return nil
}
```

Repeat the identical pair for `boundaryArmID` over `s.Boundaries`
(`HostToken`+`BoundaryNode`) and `eventSubArmID` over
`s.EventTriggeredSubprocesses` (`EnclosingScopeID`+`EventSubprocessNode`).

- [ ] **Step 4: Rewrite tiers 1–3 of `handleSignalReceived`**

Replace `engine/step_triggers.go:684-726` (**not** `:689-726` — the block below
re-declares `snapshotIDs`, `signalCmds` and `matched`, which live at `:684-687`;
replacing from `:689` would duplicate all three). Snapshot **before** any dispatch:

```go
	snapshotIDs := s.tokenIDsAwaitingSignal(t.Name)
	// ADR-0158: snapshot arm identities per family BEFORE dispatching, for the
	// same reason the token snapshot exists. Firing one arm can remove others
	// (an interrupting boundary cancels its host's siblings; an interrupting
	// event sub-process cancels every arm in the enclosing scope), and a
	// non-interrupting arm deliberately STAYS armed (ADR-0124) — so a loop that
	// re-scanned for "the next match" would find the same arm forever.
	gatewayIDs := s.armedEventIDsBySignal(t.Name)
	boundaryIDs := s.boundaryArmIDsBySignal(t.Name)
	eventSubIDs := s.eventTriggeredSubprocessArmIDsBySignal(t.Name)

	var signalCmds []Command
	matched := false
	mergeOnce := func() {
		if !matched {
			mergeVars(s, t.Payload)
			matched = true
		}
	}

	// 1) Event-gateway arms — every match, in definition-scan order.
	for _, id := range gatewayIDs {
		ae := s.armedEventByID(id)
		if ae == nil {
			continue // removed by an earlier fire in this delivery
		}
		mergeOnce()
		gwCmds, err := resolveGatewayWin(ctx, def, s, *ae, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		if err != nil {
			return StepResult{}, err
		}
		signalCmds = append(signalCmds, gwCmds...)
	}

	// 2) Boundary arms — every match.
	for _, id := range boundaryIDs {
		ba := s.boundaryArmByID(id)
		if ba == nil {
			continue
		}
		mergeOnce()
		baCmds, err := fireBoundaryArm(ctx, def, s, *ba, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		if err != nil {
			return StepResult{}, err
		}
		signalCmds = append(signalCmds, baCmds...)
	}

	// 3) Event sub-process arms — every match.
	for _, id := range eventSubIDs {
		ea := s.eventTriggeredSubprocessArmByID(id)
		if ea == nil {
			continue
		}
		mergeOnce()
		eaCmds, err := fireEventTriggeredSubprocessArm(ctx, def, s, *ea, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		if err != nil {
			return StepResult{}, err
		}
		signalCmds = append(signalCmds, eaCmds...)
	}
```

Tier 4 (`:731-755`) is unchanged. Update the doc comment above
`handleSignalReceived` to state that all four tiers are now broadcast.

**Do NOT touch** `handleTimerFired` or `handleMessageReceived` — `dispatchArmCascade`'s
first-match is correct for both (unique `TimerID`; point-to-point message).

- [ ] **Step 5: Run the test to verify it PASSES**

```bash
go test -race -run '^TestHandleSignalReceived_FiresEveryMatchingArm$' ./engine/... > ${SCRATCH}/step-2.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-2.txt
```

Expected: PASS, exit 0.

- [ ] **Step 6: Run the whole engine suite + purity guard**

```bash
go test -race ./engine/... > /tmp/t1.txt 2>&1; echo "exit=$?"; grep -c FAIL /tmp/t1.txt
```

Expected: exit 0. If a pre-existing test asserted first-match-only behaviour, it
encoded the bug — update it and note which, so the reviewer sees the intent.

- [ ] **Step 7: Commit**

```bash
git add engine/state_arms.go engine/step_triggers.go engine/step_triggers_signal_fanout_test.go
git commit -m "fix(engine)!: a broadcast signal fires every matching arm per family (ADR-0158)"
```

---

### Task 2: `kernel.WaiterStore` port + `MemWaiterStore`

**Files:**
- Create: `runtime/kernel/waiterstore.go`, `runtime/kernel/waiterstore_test.go`
- Generate: `runtime/kernel/waiterstore_mock.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Waiter`, `WaiterKind`, `WaiterSignal`, `WaiterMessage`, `WaiterStore`,
  `WaiterWriter`, `MemWaiterStore`, `NewMemWaiterStore()` — see Shared Interfaces.

- [ ] **Step 1: Write the failing test**

`runtime/kernel/waiterstore_test.go`, black-box (`package kernel_test`):

```go
func TestMemWaiterStore(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		arrange func(t *testing.T, s *kernel.MemWaiterStore)
		act    func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error)
		assert func(t *testing.T, ids []string, err error)
	}

	cases := []testCase{
		{
			name: "signal waiters returns every subscribed instance, ascending",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				require.NoError(t, s.ReplaceWaiters(t.Context(), "inst-b",
					[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "escalate"}}))
				require.NoError(t, s.ReplaceWaiters(t.Context(), "inst-a",
					[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "escalate"}}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.SignalWaiters(ctx, "escalate")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"inst-a", "inst-b"}, ids, "ascending for deterministic fan-out")
			},
		},
		{
			name: "ReplaceWaiters is wholesale: the previous set is discarded",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				ctx := t.Context()
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-a",
					[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "old"}}))
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-a",
					[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "new"}}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.SignalWaiters(ctx, "old")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Empty(t, ids, "the superseded name must no longer match")
			},
		},
		{
			name: "empty ws deletes every row for the instance",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				ctx := t.Context()
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-a",
					[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "escalate"}}))
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-a", nil))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.SignalWaiters(ctx, "escalate")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Empty(t, ids)
			},
		},
		{
			name: "empty name matches no waiter (ADR-0152)",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				require.NoError(t, s.ReplaceWaiters(t.Context(), "inst-a",
					[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "escalate"}}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.SignalWaiters(ctx, "")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Empty(t, ids, "an empty identity key must never act as a wildcard")
			},
		},
		{
			name: "message waiters match on name AND correlation key",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				ctx := t.Context()
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-a",
					[]kernel.Waiter{{Kind: kernel.WaiterMessage, Name: "approve", CorrelationKey: "ORD-1"}}))
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-b",
					[]kernel.Waiter{{Kind: kernel.WaiterMessage, Name: "approve", CorrelationKey: "ORD-2"}}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.MessageWaiters(ctx, "approve", "ORD-1")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"inst-a"}, ids)
			},
		},
		{
			name: "an empty correlation key is a keyless await, not a wildcard",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				ctx := t.Context()
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-keyed",
					[]kernel.Waiter{{Kind: kernel.WaiterMessage, Name: "approve", CorrelationKey: "ORD-1"}}))
				require.NoError(t, s.ReplaceWaiters(ctx, "inst-keyless",
					[]kernel.Waiter{{Kind: kernel.WaiterMessage, Name: "approve"}}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.MessageWaiters(ctx, "approve", "")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"inst-keyless"}, ids,
					"a keyless lookup must not sweep up keyed awaits")
			},
		},
		{
			name: "several instances may await one (name, key) — multiplicity is representable",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				ctx := t.Context()
				for _, id := range []string{"inst-a", "inst-b"} {
					require.NoError(t, s.ReplaceWaiters(ctx, id,
						[]kernel.Waiter{{Kind: kernel.WaiterMessage, Name: "cancel", CorrelationKey: "ORD-1"}}))
				}
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.MessageWaiters(ctx, "cancel", "ORD-1")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"inst-a", "inst-b"}, ids,
					"ADR-0155: the old map[msgKey]string destroyed the second waiter")
			},
		},
		{
			name: "signal and message namespaces do not collide on the same name",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				require.NoError(t, s.ReplaceWaiters(t.Context(), "inst-a", []kernel.Waiter{
					{Kind: kernel.WaiterMessage, Name: "ping"},
				}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.SignalWaiters(ctx, "ping")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Empty(t, ids, "kind discriminates")
			},
		},
		{
			name: "duplicate waiters within one instance collapse",
			arrange: func(t *testing.T, s *kernel.MemWaiterStore) {
				require.NoError(t, s.ReplaceWaiters(t.Context(), "inst-a", []kernel.Waiter{
					{Kind: kernel.WaiterSignal, Name: "escalate"},
					{Kind: kernel.WaiterSignal, Name: "escalate"},
				}))
			},
			act: func(t *testing.T, ctx context.Context, s *kernel.MemWaiterStore) ([]string, error) {
				return s.SignalWaiters(ctx, "escalate")
			},
			assert: func(t *testing.T, ids []string, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"inst-a"}, ids, "one instance appears once")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			s := kernel.NewMemWaiterStore()
			if tc.arrange != nil {
				tc.arrange(t, s)
			}
			ids, err := tc.act(t, ctx, s)
			tc.assert(t, ids, err)
		})
	}
}
```

Add a second test asserting concurrency safety (the store is read on every
delivery and written on every commit):

```go
func TestMemWaiterStore_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()

	s := kernel.NewMemWaiterStore()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() { defer wg.Done()
			_ = s.ReplaceWaiters(context.Background(), fmt.Sprintf("inst-%d", i),
				[]kernel.Waiter{{Kind: kernel.WaiterSignal, Name: "escalate"}})
		}()
		go func() { defer wg.Done(); _, _ = s.SignalWaiters(context.Background(), "escalate") }()
	}
	wg.Wait()
	ids, err := s.SignalWaiters(context.Background(), "escalate")
	require.NoError(t, err)
	assert.Len(t, ids, 50)
}
```

> `context.Background()` is correct **here only** — these goroutines outlive the
> subtest body and `t.Context()` would be cancelled under them.

- [ ] **Step 2: Run the test to verify it FAILS**

```bash
go test -run '^TestMemWaiterStore' ./runtime/kernel/... > ${SCRATCH}/step-3.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-3.txt
```

Expected: FAIL — `undefined: kernel.NewMemWaiterStore`.

- [ ] **Step 3: Implement `runtime/kernel/waiterstore.go`**

Types exactly as in Shared Interfaces, plus:

```go
//go:generate mockgen -source=waiterstore.go -package=kernel -destination=waiterstore_mock.go -typed
```
on line 3 of the file.

`MemWaiterStore` holds `mu sync.RWMutex` and
`byInstance map[string]map[Waiter]struct{}` (`Waiter` is comparable, so it is a
valid map key and duplicates collapse for free). `SignalWaiters` /
`MessageWaiters` scan and `slices.Sort` the result. Both return `nil, nil`
immediately when `name == ""` (ADR-0152). `ReplaceWaiters` with an empty `ws`
deletes the instance's entry. Add `var _ WaiterStore = (*MemWaiterStore)(nil)` and
`var _ WaiterWriter = (*MemWaiterStore)(nil)`.

- [ ] **Step 4: Run the test to verify it PASSES**

```bash
go test -race -run '^TestMemWaiterStore' ./runtime/kernel/... > ${SCRATCH}/step-4.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-4.txt
```

- [ ] **Step 5: Generate the mock**

```bash
go generate ./runtime/kernel/... && go build ./... && git status --short
```

Expected: `waiterstore_mock.go` created; `go build` clean; re-running `go generate`
produces no further diff.

- [ ] **Step 6: Commit**

```bash
git add runtime/kernel/waiterstore.go runtime/kernel/waiterstore_test.go runtime/kernel/waiterstore_mock.go
git commit -m "feat(kernel): WaiterStore/WaiterWriter port and MemWaiterStore (ADR-0155)"
```

---

### Task 3: `kernel.UndeliveredStore` port + `MemUndeliveredStore`

**Files:**
- Create: `runtime/kernel/undelivered.go`, `runtime/kernel/undelivered_test.go`
- Generate: `runtime/kernel/undelivered_mock.go`

**Interfaces:**
- Consumes: `WaiterKind` (Task 2).
- Produces: `UndeliveredWakeup`, `UndeliveredFilter`, `UndeliveredPage`,
  `UndeliveredStore`, `MemUndeliveredStore`, `NewMemUndeliveredStore()`.

- [ ] **Step 1: Write the failing test**

Table test over `Record` → `List` → `Delete`, covering:

| case | assertion |
|---|---|
| record then list returns it | round-trip preserves `OccurredAt` **to the nanosecond** and `Payload` |
| list orders newest-first | `(FailedAt DESC, ID DESC)` |
| filter by `InstanceID` | only that instance's rows |
| `Limit` normalisation | `0 → 50`, `500 → 200` (use `kernel.NormalizeLimit`) |
| paging | `HasMore` true, `NextCursor` non-empty, second page disjoint from the first |
| delete removes exactly one | siblings survive |
| delete of an unknown id | nil error (idempotent) |
| payload is copied on ingest | mutating the caller's map after `Record` does not change the stored row |

The `OccurredAt` fidelity case is load-bearing, but **as provenance, not as the
replay instant**. ADR-0157 was revised: `ReplayUndelivered` stamps the rebuilt
trigger with `clk.Now()`, because nothing anchors a timer to `Trigger.OccurredAt()`
— it drives `Token.EnteredAt`, `openVisit`/`closeVisit` and `s.EndedAt`, so
replaying at a stale instant backdates the ADR-0144–0151 audit trail. The store
must still round-trip the recorded instant faithfully so an operator can see *when*
the delivery originally failed:

```go
{
	name: "OccurredAt round-trips verbatim as provenance",
	arrange: func(t *testing.T, s *kernel.MemUndeliveredStore) {
		require.NoError(t, s.Record(t.Context(), kernel.UndeliveredWakeup{
			ID: "u1", InstanceID: "inst-a", Kind: kernel.WaiterSignal, Name: "escalate",
			OccurredAt: time.Date(2026, 7, 28, 10, 30, 0, 123456789, time.UTC),
			FailedAt:   time.Date(2026, 7, 28, 10, 30, 5, 0, time.UTC),
			Attempts:   5, Cause: "workflow-runtime: concurrent update",
		}))
	},
	assert: func(t *testing.T, page kernel.UndeliveredPage, err error) {
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.True(t,
			page.Items[0].OccurredAt.Equal(time.Date(2026, 7, 28, 10, 30, 0, 123456789, time.UTC)),
			"the recorded instant is the operator's provenance for when delivery failed; "+
				"replay does NOT reuse it (ADR-0157, revised)")
	},
},
```

⚠ The corresponding **replay** assertion lives in Task 8b and must assert the
opposite of an earlier draft: the delivered trigger carries `clk.Now()`, **not**
the stored `OccurredAt`.

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run '^TestMemUndeliveredStore' ./runtime/kernel/... > ${SCRATCH}/step-5.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-5.txt
```

Expected: FAIL — `undefined: kernel.NewMemUndeliveredStore`.

- [ ] **Step 3: Implement `runtime/kernel/undelivered.go`**

Types as in Shared Interfaces; `//go:generate` directive on line 3. `MemUndeliveredStore`
holds `mu sync.RWMutex` and `rows map[string]UndeliveredWakeup`. `Record` deep-copies
`Payload` with `maps.Clone` before storing (the caller owns the map). `List` sorts
by `(FailedAt, ID)` descending, applies `NormalizeLimit`, and reuses
`kernel.EncodeCursor` / `kernel.DecodeCursor` (`runtime/kernel/lister.go`) — the
cursor payload is `(FailedAt, ID)` in place of `(StartedAt, InstanceID)`.

> If `EncodeCursor`'s field names read wrongly for this use, add a sibling
> `EncodeUndeliveredCursor` rather than widening the existing one — the instance
> lister's cursor format is already persisted in consumer hands.

- [ ] **Step 4: Run to verify PASS**

```bash
go test -race -run '^TestMemUndeliveredStore' ./runtime/kernel/... > ${SCRATCH}/step-6.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-6.txt
```

- [ ] **Step 5: Generate the mock and commit**

```bash
go generate ./runtime/kernel/... && go build ./...
git add runtime/kernel/undelivered.go runtime/kernel/undelivered_test.go runtime/kernel/undelivered_mock.go
git commit -m "feat(kernel): UndeliveredStore port and MemUndeliveredStore (ADR-0157)"
```

---

### Task 4: Migrations — two new tables across three dialects

Serial. Tasks 5 and 6 both depend on this and must not run concurrently with it.

**Files:**
- Modify: `internal/persistence/store/migrations/postgres/0001_init.sql`
- Modify: `internal/persistence/store/migrations/mysql/0001_init.sql`
- Modify: `internal/persistence/store/migrations/sqlite/0001_init.sql`
- Test: `internal/persistence/store/migration_parity_test.go` (extend)

- [ ] **Step 1: Write the failing test**

Extend `TestMigrationParity_LogicalSchemaConverges` (or add a sibling) asserting
both tables exist with the expected columns on all three dialects after `Migrate`.

```go
func TestMigrations_WaiterAndUndeliveredTablesExist(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		open  func(t *testing.T) (*sql.DB, dialect.Dialect) // SQLite/MySQL; Postgres variant uses the pool
		table string
	}
	// One row per (dialect, table) — 6 cases.
	// Assert: SELECT succeeds against every declared column, and inserting two
	// rows differing only in correlation_key does not violate the PK.
}
```

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run '^TestMigrations_WaiterAndUndeliveredTablesExist$' ./internal/persistence/store/... > ${SCRATCH}/step-7.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-7.txt
```

Expected: FAIL — `no such table: wrkflw_waiters`. Docker must be running for the
Postgres/MySQL cases; the SQLite case needs no container.

- [ ] **Step 3: Add both tables to all three `0001_init.sql` files**

Postgres:

```sql
-- Durable waiter projection (ADR-0155). A projection of the instance snapshot,
-- written inside the state-commit transaction by the same single authority
-- (engine.InstanceState.{Signal,Message}Waiters), never a hand-maintained second
-- source of truth. No FK to wrkflw_instances, matching wrkflw_timers: rows are
-- deleted by ReplaceWaiters(id, nil) on the terminal commit, and an FK would add
-- a lock-ordering dependency inside the hot commit transaction.
CREATE TABLE wrkflw_waiters (
    instance_id     TEXT     NOT NULL,
    kind            SMALLINT NOT NULL,          -- 0 = signal, 1 = message
    name            TEXT     NOT NULL,
    correlation_key TEXT     NOT NULL DEFAULT '',
    PRIMARY KEY (instance_id, kind, name, correlation_key)
);
-- Serves both lookups: signal fan-out on (kind, name) as a left prefix, and
-- message correlation on the full triple.
CREATE INDEX wrkflw_waiters_lookup_idx ON wrkflw_waiters (kind, name, correlation_key);

-- Undelivered wake-ups (ADR-0157). The INBOUND counterpart to the outbox's
-- dead-letter rows (wrkflw_outbox status='dead'), deliberately a separate table
-- and a separate concept — see ADR-0157 on the naming.
CREATE TABLE wrkflw_undelivered (
    id              TEXT        PRIMARY KEY,
    instance_id     TEXT        NOT NULL,
    kind            SMALLINT    NOT NULL,
    name            TEXT        NOT NULL,
    correlation_key TEXT        NOT NULL DEFAULT '',
    payload         JSONB,
    occurred_at     TIMESTAMPTZ NOT NULL,       -- ORIGINAL publish instant; replay reuses it
    failed_at       TIMESTAMPTZ NOT NULL,
    attempts        INT         NOT NULL,
    cause           TEXT        NOT NULL
);
CREATE INDEX wrkflw_undelivered_list_idx     ON wrkflw_undelivered (failed_at DESC, id DESC);
CREATE INDEX wrkflw_undelivered_instance_idx ON wrkflw_undelivered (instance_id);
```

MySQL: identical, with `payload JSON`, `occurred_at`/`failed_at` as
`TIMESTAMP(6)` (match the existing columns in that file), and `TEXT` PK columns
declared as `VARCHAR(255)` — **check how `wrkflw_timers.instance_id` is declared
in the MySQL file and match it exactly**; MySQL cannot index unbounded `TEXT`.

SQLite: identical, with `payload TEXT`, `occurred_at TEXT NOT NULL`,
`failed_at TEXT NOT NULL`.

- [ ] **Step 4: Run to verify PASS**

```bash
go test -race -run '^TestMigrations' ./internal/persistence/store/... > ${SCRATCH}/step-8.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-8.txt
```

Expected: exit 0, including `TestMigrations_OneFilePerDialect` (still exactly one
file per dialect — this task edits, never adds).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/migrations internal/persistence/store/migration_parity_test.go
git commit -m "feat(persistence): wrkflw_waiters and wrkflw_undelivered tables (ADR-0155, ADR-0157)"
```

---

### Task 5: SQL `WaiterStore` + `WaiterWriter`

Parallel with Task 6 after Task 4.

**Files:**
- Create: `internal/persistence/store/waiterstore.go`
- Create: `internal/persistence/store/waiterstore_conformance_test.go`

**Interfaces:**
- Consumes: `kernel.Waiter`, `kernel.WaiterStore`, `kernel.WaiterWriter` (Task 2);
  `dialect.Dialect`; `transaction.JoinOrBegin`.
- Produces: `store.WaiterStore`, `store.NewWaiterStore(conn any, d dialect.Dialect) (*WaiterStore, error)`.

- [ ] **Step 1: Write the failing conformance test**

Follow the existing `timerstore_conformance_test.go` shape: one table of behaviours
run against all three dialects. Cases — every one is a hot path or its failure branch:

| case | why it matters |
|---|---|
| `ReplaceWaiters` then `SignalWaiters` returns the instance | the basic write→read cycle, run on every commit and every delivery |
| result is ascending by instance id | deterministic fan-out order is a documented contract |
| `ReplaceWaiters` is wholesale — superseded names stop matching | the projection must not accumulate stale rows |
| empty `ws` deletes every row for the instance | the terminal-commit path |
| re-`ReplaceWaiters` with the identical set is idempotent | ordinary steps re-write an unchanged set |
| `MessageWaiters` matches name **and** key | correlation correctness |
| empty correlation key matches only keyless awaits | ADR-0152 — must not act as a wildcard |
| empty name returns no rows for both kinds | ADR-0152 |
| two instances on one `(name, key)` both returned | ADR-0155's whole point |
| kind discriminates a shared name | signal `ping` ≠ message `ping` |
| duplicate waiters in one `ReplaceWaiters` collapse | PK dedup |
| **`ReplaceWaiters` inside a rolled-back tx leaves no rows** | atomicity — the reason it is in `commitFn` |
| **`ReplaceWaiters` inside a committed tx is visible afterwards** | the same, positive case |
| cancelled context returns an error, writes nothing | the `ctx` modifier case required by `table-test` |

The two transaction cases are the ones a naive implementation passes by accident;
write them explicitly:

```go
{
	name: "ReplaceWaiters joins the ambient transaction and is rolled back with it",
	assert: func(t *testing.T, ids []string, err error) {
		require.NoError(t, err)
		assert.Empty(t, ids, "a rolled-back projection write must leave no rows")
	},
	// arrange: transaction.RunInTx(ctx, conn, func(txCtx) error {
	//     _ = ws.ReplaceWaiters(txCtx, "inst-a", []kernel.Waiter{{...}})
	//     return errors.New("force rollback")
	// })
	// act: ws.SignalWaiters(ctx, "escalate")
},
```

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run '^TestWaiterStoreConformance' ./internal/persistence/store/... > ${SCRATCH}/step-9.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-9.txt
```

Expected: FAIL — `undefined: store.NewWaiterStore`.

- [ ] **Step 3: Implement**

Mirror `internal/persistence/store/timerstore.go` exactly:

- struct `WaiterStore{ conn any; dialect dialect.Dialect }`
- `NewWaiterStore(conn any, d dialect.Dialect) (*WaiterStore, error)` returning
  `ErrNilDependency` for a nil `conn` or `d` (use the existing `isNilDep` helper)
- compile-time checks `var _ kernel.WaiterStore = (*WaiterStore)(nil)` and
  `var _ kernel.WaiterWriter = (*WaiterStore)(nil)`
- `ReplaceWaiters`: `transaction.JoinOrBegin(ctx, s.conn)`, `committed := false`,
  `defer` rollback, `DELETE FROM wrkflw_waiters WHERE instance_id = ?`, then one
  `INSERT` per waiter, then `q.Commit(ctx)`. Guard `name == ""` — such a waiter is
  never written (it could never be matched).
- `SignalWaiters` / `MessageWaiters`: read through the pool (no transaction, like
  `Lister`), `SELECT instance_id FROM wrkflw_waiters WHERE kind = ? AND name = ?
  [AND correlation_key = ?] ORDER BY instance_id`, all through `d.Rebind`. Return
  `nil, nil` for an empty name **before** issuing a query.

No timestamps in this table, so the ADR-0151 codec does **not** apply here.

- [ ] **Step 4: Run to verify PASS**

```bash
go test -race -run '^TestWaiterStoreConformance' ./internal/persistence/store/... > ${SCRATCH}/step-10.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-10.txt
```

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/waiterstore.go internal/persistence/store/waiterstore_conformance_test.go
git commit -m "feat(persistence): dialect-parametrised WaiterStore/WaiterWriter (ADR-0155)"
```

---

### Task 6: SQL `UndeliveredStore`

Parallel with Task 5 after Task 4.

**Files:**
- Create: `internal/persistence/store/undelivered.go`
- Create: `internal/persistence/store/undelivered_conformance_test.go`

**Interfaces:**
- Consumes: `kernel.UndeliveredWakeup`, `kernel.UndeliveredStore` (Task 3).
- Produces: `store.UndeliveredStore`, `store.NewUndeliveredStore(conn any, d dialect.Dialect) (*UndeliveredStore, error)`.

- [ ] **Step 1: Write the failing conformance test**

Same three-dialect table shape. Cases:

| case | why it matters |
|---|---|
| `Record` → `List` round-trips every field | the whole point |
| **`OccurredAt` survives to the nanosecond on all three dialects** | ADR-0157 replay |
| **fractional seconds with trailing zeros sort correctly** | ADR-0151 — see below |
| `List` orders `(failed_at DESC, id DESC)` | stable newest-first |
| keyset paging: page 2 is disjoint from page 1, `HasMore` flips | pagination correctness |
| `InstanceID` filter | per-instance inspection |
| `Limit` normalisation `0→50`, `500→200` | shared contract |
| `Delete` removes one, siblings survive | recovery workflow |
| `Delete` of an unknown id is a nil-error no-op | idempotent operator action |
| `Payload` round-trips including nested maps | JSON codec across 3 dialects |
| nil `Payload` round-trips as nil, not `{}` | avoids a spurious diff on replay |
| cancelled context returns an error | `table-test` requirement |

The ADR-0151 case must use timestamps whose `RFC3339Nano` rendering would sort
wrongly, so a regression is caught rather than assumed:

```go
{
	name: "trailing-zero fractional seconds sort correctly (ADR-0151)",
	// Record three rows at .100000000, .150000000, .200000000 seconds.
	// RFC3339Nano renders these ".1", ".15", ".2" — lexicographically ".1" < ".15" < ".2"
	// happens to hold, so ALSO record .050000000 (renders ".05") which under a
	// naive encoder sorts BEFORE ".1" correctly but AFTER ".05" is compared with
	// a fixed-width ".050000000" — assert the full DESC order is exactly:
	//   .200000000, .150000000, .100000000, .050000000
	assert: func(t *testing.T, page kernel.UndeliveredPage, err error) {
		require.NoError(t, err)
		got := make([]int, 0, len(page.Items))
		for _, it := range page.Items {
			got = append(got, it.FailedAt.Nanosecond())
		}
		assert.Equal(t, []int{200000000, 150000000, 100000000, 50000000}, got,
			"SQLite sorts TEXT lexicographically; a trimmed fraction silently reorders rows")
	},
},
```

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run '^TestUndeliveredStoreConformance' ./internal/persistence/store/... > ${SCRATCH}/step-11.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-11.txt
```

- [ ] **Step 3: Implement**

Mirror `timerstore.go` for structure and `lister.go` for keyset paging.

**Timestamps:** bind with `timeArg(s.dialect, t)` and read back with the
`TimestampsAsText`-gated branch used by `TimerStore` — scan into `string` and
`parseTimeText` when `s.dialect.TimestampsAsText()`, otherwise scan `time.Time`
directly. **Never** compare `s.dialect.Name()` to `"sqlite"`.

**Payload:** `json.Marshal` on write (nil payload → SQL NULL, not `"null"`);
on read, a NULL column yields a nil map.

- [ ] **Step 4: Run to verify PASS**

```bash
go test -race -run '^TestUndeliveredStoreConformance' ./internal/persistence/store/... > ${SCRATCH}/step-12.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-12.txt
```

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/undelivered.go internal/persistence/store/undelivered_conformance_test.go
git commit -m "feat(persistence): dialect-parametrised UndeliveredStore (ADR-0157)"
```

---

### Task 7: Pruner sweeps + `persistence` facade constructors

**Files:**
- Modify: `internal/persistence/store/pruner.go` (after `PruneTimers`, ~line 192)
- Modify: `internal/persistence/store/pruner_conformance_test.go`
- Modify: `persistence/persistence.go`, `persistence/mysql.go`, `persistence/sqlite.go`

**Interfaces:**
- Consumes: Tasks 5, 6.
- Produces: `persistence.NewWaiterStore(pool *pgxpool.Pool)`,
  `NewMySQLWaiterStore(db *sql.DB)`, `NewSQLiteWaiterStore(db *sql.DB)`, and the
  three `…UndeliveredStore` siblings — each returning the `kernel` interface type,
  matching the existing `NewLister` / `NewTimerStore` shape.

- [ ] **Step 1: Write the failing test** — extend the pruner conformance table with
  `PruneUndelivered` (rows older than cutoff deleted, newer kept, returns the count)
  and `PruneWaiters` (rows whose `instance_id` has no row in `wrkflw_instances` are
  deleted; rows for live instances are kept). Add a `persistence` facade test
  asserting each new constructor returns a non-nil value satisfying its interface
  and `ErrNilDependency` on nil input.

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run 'Prune|WaiterStore|UndeliveredStore' ./internal/persistence/... ./persistence/... > ${SCRATCH}/step-13.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-13.txt
```

- [ ] **Step 3: Implement** both pruners (`DELETE … WHERE failed_at < ?` for
  undelivered; an anti-join `DELETE FROM wrkflw_waiters WHERE instance_id NOT IN
  (SELECT instance_id FROM wrkflw_instances)` for waiters — write it as a
  dialect-portable `NOT EXISTS`), and the six facade constructors mirroring
  `NewLister`.

- [ ] **Step 4: Run to verify PASS**, then **Step 5: Commit**

```bash
git commit -m "feat(persistence): PruneWaiters/PruneUndelivered and facade constructors (ADR-0155, ADR-0157)"
```

---

### Task 8a: `runtime/delivery.Bus`

**Files:**
- Create: `runtime/delivery/bus.go`, `runtime/delivery/doc.go`, `runtime/delivery/bus_test.go`

**Interfaces:**
- Consumes: `kernel.WaiterStore`, `kernel.UndeliveredStore`, `kernel.WaiterWriter`,
  the generated mocks from Tasks 2–3, `clockwork.Clock`, `engine.Trigger`.
- Produces: `delivery.Bus`, `delivery.NewBus`, `delivery.Policy`
  (`Broadcast`/`Selective`/`Exclusive`), `delivery.ErrAmbiguousMessageCorrelation`,
  `delivery.WithClock`, `delivery.WithUndeliveredStore`, `delivery.WithIDGenerator`,
  `delivery.WithMaxAttempts` — see Shared Interfaces for `Publish`.

- [ ] **Step 1: Write the failing test**

Use the generated `kernel.MockWaiterStore` / `MockUndeliveredStore` (`--typed`, so
`EXPECT()` is compile-checked). Every case below is a hot path or a failure branch:

| case | assertion |
|---|---|
| `Broadcast` delivers to every waiter | N `DeliverFunc` calls, ascending instance order |
| `Broadcast` ignores the selector | a non-empty selector does not filter |
| `Selective` delivers to every matching waiter | fan-out is the message default |
| `Exclusive` with one match delivers once | strict happy path |
| **`Exclusive` with two matches returns `ErrAmbiguousMessageCorrelation` and delivers to NOBODY** | strict mode must not half-apply |
| no waiters → nil error, zero deliveries | a no-match publish is a clean no-op |
| **the trigger is stamped ONCE — all recipients see the same `OccurredAt`** | ADR-0156; `mk` is called once |
| one recipient failing does not abort the rest | `errors.Join`, remaining deliveries still attempted |
| **`ErrConcurrentUpdate` with 0 committed steps retries, then records undelivered on exhaustion** | the CAS ladder |
| **a CAS conflict that succeeds on attempt 2 records nothing** | retry actually retries |
| **`ErrConcurrentUpdate` with >0 committed steps is NOT retried — recorded immediately** | ADR-0157: re-delivery would re-fire a non-interrupting arm and cannot recover a dropped follow-up trigger |
| **a cancelled context aborts the fan-out and records nothing** | ladder step 0 |
| **`WithMaxFanout(n)` exceeded → error, no partial delivery** | the blast-radius bound |
| **`Selective` with >1 recipient emits the re-sited ADR-0125 WARN carrying name + count** | the only diagnostic for a degenerate key |
| **`ErrInstanceNotFound` self-heals: `ReplaceWaiters(id, nil)` called, nothing recorded** | ADR-0157 step 2 |
| a non-CAS error records undelivered immediately (no retry) | only CAS is retryable |
| the recorded wakeup carries the ORIGINAL `OccurredAt`, `Attempts`, and `Cause` | replay fidelity |
| `Record` itself failing is logged, does not abort remaining recipients | ADR-0157 |
| no `UndeliveredStore` wired → delivery unchanged, nothing recorded | opt-in capability |
| `WaiterStore` read error → error returned, zero deliveries | lookup failure branch |
| cancelled context → error, zero deliveries | `table-test` requirement |

Sketch of the CAS case, showing the mock style:

```go
{
	name: "CAS exhaustion records an undelivered wakeup with the original instant",
	arrange: func(t *testing.T, m *mocks) {
		m.waiters.EXPECT().SignalWaiters(gomock.Any(), "escalate").
			Return([]string{"inst-a"}, nil)
		m.undelivered.EXPECT().
			Record(gomock.Any(), gomock.Cond(func(u kernel.UndeliveredWakeup) bool {
				return u.InstanceID == "inst-a" &&
					u.OccurredAt.Equal(fixedNow) &&
					u.Attempts == 5 &&
					strings.Contains(u.Cause, "concurrent update")
			})).
			Return(nil)
	},
	deliver: func(ctx context.Context, id string, trg engine.Trigger) error {
		return kernel.ErrConcurrentUpdate // always conflicts
	},
	assert: func(t *testing.T, err error) {
		require.Error(t, err)
		assert.ErrorIs(t, err, kernel.ErrConcurrentUpdate,
			"the joined error is still returned — the record is defence in depth, not a replacement")
	},
},
```

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run '^TestBus' ./runtime/delivery/... > ${SCRATCH}/step-14.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-14.txt
```

Expected: FAIL — `no required module provides package .../runtime/delivery`.

- [ ] **Step 3: Implement `runtime/delivery/bus.go`**

`Publish` algorithm:

1. `if name == "" { return nil }` — ADR-0152, a clean no-op.
2. Resolve ids: `Broadcast` → `waiters.SignalWaiters(ctx, name)`; `Selective` /
   `Exclusive` → `waiters.MessageWaiters(ctx, name, selector)`. Read error →
   return wrapped, deliver nothing.
3. `len(ids) == 0` → `return nil`.
4. `Exclusive && len(ids) > 1` → return `ErrAmbiguousMessageCorrelation` **before
   delivering to anyone**.
5. `trg := mk(b.clk.Now())` — **once**, outside the loop.
6. For each id, run the escalation ladder; accumulate errors.
7. `return errors.Join(errs...)`.

Ladder per recipient:

```go
var lastErr error
attempts := 0                       // hoisted: `attempt` is scoped to the for statement
for attempts < b.maxAttempts {
	// Step 0: a cancelled caller aborts the fan-out. store.Load maps ONLY
	// sql.ErrNoRows to ErrInstanceNotFound, so context.Canceled arrives as a
	// wrapped store error and would otherwise be recorded — one failed Record
	// per remaining recipient, each using the same dead ctx (ADR-0157).
	if err := ctx.Err(); err != nil {
		return err
	}
	attempts++
	committed, lastErr = b.deliverTracked(ctx, id, trg)
	if lastErr == nil {
		return nil
	}
	if errors.Is(lastErr, kernel.ErrInstanceNotFound) {
		// Self-heal: an orphan row means an inconsistent projection, not a
		// failed delivery — there is no instance to wake (ADR-0157 step 2).
		b.selfHeal(ctx, id)
		return nil
	}
	// ONLY a CAS conflict that committed NOTHING is retryable. deliverLoop
	// commits once per iteration and performs side effects after each commit,
	// so re-delivering after a partial success re-fires any non-interrupting
	// arm (never consumed — ADR-0124) and cannot recover a follow-up trigger
	// dropped mid-loop. See ADR-0157 "Step 1 in full".
	if !errors.Is(lastErr, kernel.ErrConcurrentUpdate) || committed > 0 {
		break
	}
}
b.recordUndelivered(ctx, id, k, name, selector, payload, trg.OccurredAt(), attempts, lastErr)
return lastErr
```

`deliverTracked` wraps the injected `DeliverFunc` and returns the number of steps
the underlying `deliverLoop` committed. Task 8b must surface that count from
`ProcessDriver.applyTrigger`; a `DeliverFunc` that cannot report it (a test double)
returns 0, which is the safe default — it makes every CAS conflict retryable, which
matches the pre-partial-commit case.

`selfHeal` calls `ReplaceWaiters(ctx, id, nil)` when the store also implements
`kernel.WaiterWriter`, WARNs, and increments a metric. `recordUndelivered` is
best-effort: a nil `UndeliveredStore` degrades to ERROR log + metric, and a failing
`Record` is ERROR-logged without aborting the surrounding loop.

Default `maxAttempts` is 5. NOTE: `timerFireFunc` is **not** the precedent — a
fired one-shot timer is consumed so its retry is genuinely idempotent, whereas a
non-interrupting signal arm is never consumed (ADR-0124). The number matches; the
reasoning does not carry over.

- [ ] **Step 4: Run to verify PASS**

```bash
go test -race ./runtime/delivery/... > ${SCRATCH}/step-15.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-15.txt
```

- [ ] **Step 5: Commit**

```bash
git add runtime/delivery/
git commit -m "feat(runtime): unified delivery.Bus with Broadcast/Selective/Exclusive policies (ADR-0156, ADR-0157)"
```

---

### Task 8b: Driver rewire — **INLINE in the controller**

Compile-breaking across the repo. Do **not** delegate; a subagent cannot keep the
tree green mid-change. Expect `go build ./...` to fail until Task 9 completes.

**Files:**
- Modify: `runtime/processdriver.go`, `runtime/processdriver_options.go`,
  `runtime/processdriver_signal.go`, `runtime/processdriver_message.go`
- Create: `runtime/waiterops.go`, `runtime/undeliveredops.go`
- Delete: `runtime/processdriver_waiters.go`, `runtime/signal/` (whole package)

- [ ] **Step 1: Write the failing tests** for the three new driver behaviours,
  before touching production code:
  - `waitersOf` — table over all four signal sources × all four message sources ×
    terminal/non-terminal, **including the ADR-0124 case** where a repeatable
    non-interrupting root event-sub arm survives into a terminal snapshot and must
    still project to zero waiters.
  - `RehydrateWaiters` — paging across a `>Limit` result set; one unreadable
    instance is skipped, not fatal; returns a descriptive error when no
    `InstanceLister` is configured.
  - `ReplayUndelivered` — rebuilds the trigger with **`clk.Now()`**, NOT the
    stored instant (ADR-0157 revised: nothing anchors a timer to
    `Trigger.OccurredAt()`; it drives `NodeVisit`/`EndedAt`, so replaying at a
    stale instant backdates the ADR-0144–0151 audit trail). Assert the delivered
    trigger carries the fake clock's now, and that the stored `OccurredAt` is
    still returned by `ListUndelivered` as provenance; replay against
    an already-advanced instance is a clean no-op; unknown id returns a descriptive
    error.

  ⚠ **The message-semantics cases belong HERE, not in Task 11.** Task 8b
  implements fan-out, deliver-and-start and strict mode; testing them in a later
  task is CLAUDE.md's forbidden "write the implementation first, intending to add
  tests after" (audit A24). Add these rows to this step:

  | case | assertion |
  |---|---|
  | fan-out default: three instances on one `(name, key)` all resume | `Selective` is the default |
  | strict mode: same setup returns `ErrAmbiguousMessageCorrelation`, **none** resume | no half-application |
  | deliver AND start: a waiter resumes *and* a matching message-start fires | D-A |
  | keyed start: a second delivery dedups (`ErrInstanceExists` → no-op) | ADR-0121 bound |
  | keyless non-singleton start: **each** delivery mints a fresh instance | the accepted amplification — pinned so it is deliberate, not accidental |
  | `ErrAmbiguousMessageStart` is joined; the waiter delivery still happened | no early abort |
  | within one instance a message lands once even with two matching constructs | D9 |
  | empty name is a clean no-op | ADR-0152 |
  | a driver built with **no** mode option calls `MessageWaiters`, never `SignalWaiters` | pins the `MessageDeliveryMode` default (audit A1) |
  | `ReplayUndelivered` refuses with `ErrWaiterSetChanged` when the waiter set moved on; `WithForce()` overrides | C4 |

  And these three driver-level hot paths, which no task covered (audit A25 — the
  first two are Critical):

  | case | assertion |
  |---|---|
  | **`ReplaceWaiters` runs INSIDE the commit tx** — a `commitFn` failing after `store.Commit` leaves **no** waiter rows and the PRE-step snapshot | the atomicity D3 claims; real SQLite + Postgres, inject a failing `WaiterWriter` |
  | **commit WITHOUT `TxRunner`** leaves a stale projection, not a torn one | pins the documented §5.3 degradation instead of leaving it prose |
  | **`waitersOf` dedups** a signal boundary + signal catch on one name to a single `Waiter` | without it the PK rejects the second row and the commit fails (audit A2) |

- [ ] **Step 2: Run to verify FAIL**

```bash
go test -run 'TestWaitersOf|TestRehydrateWaiters|TestReplayUndelivered|TestDeliverMessage|TestCommitTx' ./runtime/... > ${SCRATCH}/step-16.txt 2>&1; echo "exit=$?"; tail -20 ${SCRATCH}/step-16.txt
```

- [ ] **Step 3: Implement**

`runtime/waiterops.go`:

```go
// waitersOf projects st into its durable waiter rows. It is the ONLY mapping
// from state to waiters, so a future construct extends
// engine.InstanceState.{Signal,Message}Waiters and is picked up here for free —
// the single-authority property ADR-0123/0154 established.
//
// A terminal instance awaits nothing: a repeatable non-interrupting root
// event-sub arm can survive into a terminal snapshot (ADR-0124), and leaving its
// row would misroute a later delivery to a dead instance.
func waitersOf(st engine.InstanceState) []kernel.Waiter { /* per spec §3.3 */ }

var nonTerminalStatuses = [...]engine.Status{engine.StatusRunning, engine.StatusCompensating}

func (driver *ProcessDriver) RehydrateWaiters(ctx context.Context) error { /* per spec §3.8 */ }
```

`runtime/processdriver.go`: add `waiters kernel.WaiterStore`,
`waiterWriter kernel.WaiterWriter`, `undelivered kernel.UndeliveredStore`,
`lister kernel.InstanceLister`, `bus *delivery.Bus`, `msgMode delivery.MessageDeliveryMode`
(**not** `delivery.Policy` — an uninitialised `Policy` is `Broadcast`, which would
route every message to SIGNAL waiters). Set `driver.msgMode = delivery.ModeSelective`
BEFORE the option loop.
Remove `msgMu`, `msgWaiters`, `sigbus`. Default `waiters` to a
`kernel.NewMemWaiterStore()` shared as both store and writer. After the option
loop, auto-detect `driver.lister` off `driver.store` and construct
`driver.bus = delivery.NewBus(driver.applyTrigger-closure, driver.waiters, …)`.

Add the construction WARN: durable `InstanceStore` (i.e. `driver.store !=
defaultStore`) paired with a `MemWaiterStore` ⇒ WARN that waiters will not survive
a restart and multi-replica delivery is unsupported, naming `WithWaiterStore`.

In `deliverLoop`'s `commitFn`, after the `store.Create`/`store.Commit` branch and
before the `jobStore.Save` loop:

```go
			// waiterWriter is non-nil by construction: WithWaiterStore requires
			// kernel.WaiterProjection (both halves) and NewProcessDriver fails with
			// ErrNilDependency otherwise, so this is never nil-guarded into a no-op.
			{
				if werr := driver.waiterWriter.ReplaceWaiters(txCtx, st.InstanceID, waitersOf(st)); werr != nil {
					return werr
				}
			}
```

Delete the post-commit `driver.syncWaiters(st)` call and
`runtime/processdriver_waiters.go` entirely.

`BroadcastSignal` and `DeliverMessage` per spec §3.6 — bus call plus the existing
start half, `errors.Join`. `DeliverMessage` no longer returns early on a waiter
hit; `ErrAmbiguousMessageStart` is joined rather than returned bare.

`runtime/processdriver_options.go`: add `WithWaiterStore(kernel.WaiterProjection)`
(both halves required), `WithInstanceLister`,
`WithUndeliveredStore`, `WithMessageDeliveryMode`; delete `WithSignalBus`.

`runtime/undeliveredops.go`: `ListUndelivered`, `ReplayUndelivered`,
`DeleteUndelivered`. Replay reconstructs via
`engine.NewSignalReceived(u.OccurredAt, …)` / `engine.NewMessageReceived(u.OccurredAt, …)`
— stamped with `driver.clk.Now()`, with `u.OccurredAt` preserved on the record.

- [ ] **Step 4: Run to verify PASS** (runtime package only; the repo does not build yet)

```bash
go test -race ./runtime/... > ${SCRATCH}/step-17.txt 2>&1; echo "exit=$?"; tail -30 ${SCRATCH}/step-17.txt
```

- [ ] **Step 5: Do NOT commit yet** — Task 9 completes the same compile unit.

---

### Task 9: Rewire consumers — **INLINE in the controller**

**Files:**
- Modify: `processtest/harness.go` (`WithSignalBus` option and `Bus()` accessor,
  lines ~40, 114, 190, 205, 343)
- Modify: `examples/scenarios/signal_broadcast/main.go`,
  `examples/scenarios/signal_throw/main.go`, and every other example referencing
  `runtime/signal` or `WithSignalBus`
- Modify: `runtime/doc.go:34`, `processtest/doc.go:18`

- [ ] **Step 1: Find every reference**

```bash
grep -rln "runtime/signal\|WithSignalBus\|SignalBus" --include="*.go" . | grep -v '^./.git'
```

- [ ] **Step 2: Rewire each.** `processtest.WithSignalBus()` becomes a no-op
  deprecation or is deleted outright (prefer deletion — the harness now wires a
  `MemWaiterStore` unconditionally, so signals work with no opt-in). The examples
  lose the declare-then-assign dance entirely:

```go
	// Before: bus captured a still-nil driver.
	// After: the driver owns the bus; signals work out of the box.
	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
	)
```

- [ ] **Step 3: Verify the whole repo builds and passes**

```bash
go build ./... ; echo "build=$?"
go vet ./... ; echo "vet=$?"
gofmt -l engine runtime internal persistence processtest examples
go test -race ./... > /tmp/t9.txt 2>&1; echo "test=$?"; grep -c "^FAIL" /tmp/t9.txt
golangci-lint run ./... ; echo "lint=$?"
```

Expected: build 0, vet 0, no gofmt output, test 0, zero `FAIL`, lint 0.

- [ ] **Step 4: Commit Tasks 8a+8b+9 together** (one compile unit)

```bash
git add -A
git commit -m "feat(runtime)!: durable waiter projection and unified delivery bus (ADR-0155, ADR-0156, ADR-0157)"
```

---

### Task 10: Restart and multi-replica integration proof

The two tests that fail today and that no amount of unit testing substitutes for.

**Files:**
- Create: `runtime/delivery_restart_test.go` (or `processtest/` if the harness fits)

- [ ] **Step 1: Write the failing tests**

```go
// TestDelivery_SurvivesRestart pins ADR-0155 R1. A driver is discarded and a
// FRESH one constructed over the SAME durable store; a broadcast and a message
// must still reach the instance parked before the restart.
//
// Run as a table over both waiter backends:
//   - durable (SQL WaiterStore): must pass with NO rehydrate call
//   - in-memory (MemWaiterStore): must pass only AFTER RehydrateWaiters
func TestDelivery_SurvivesRestart(t *testing.T) { /* ... */ }

// TestDelivery_ReachesInstanceParkedByAnotherReplica pins R2. Replica A parks
// the instance; replica B — a second ProcessDriver over the SAME store, which
// has never stepped that instance — broadcasts. The instance must wake.
// This is the test that boot-rehydrate alone could not pass.
func TestDelivery_ReachesInstanceParkedByAnotherReplica(t *testing.T) { /* ... */ }
```

Both use `dbtest.RunTestSQLite(t)` for the fast path (pure-Go, no Docker) plus a
Postgres case via `dbtest.RunTestDatabase(t)`. Cover **both** kinds — a signal
boundary and a keyed message catch — in the same parked instance, so a
regression in either projection is caught.

- [ ] **Step 2: Run to verify FAIL** — the multi-replica case must fail against
  a `MemWaiterStore` configuration, proving the test has teeth.

- [ ] **Step 3–4:** No new implementation should be needed. If a test fails, the
  defect is in Tasks 5/8 — fix there, not in the test.

- [ ] **Step 5: Commit**

```bash
git commit -m "test(runtime): restart and multi-replica delivery proofs (ADR-0155)"
```

---

### Task 11: Message semantics + docs

**Files:**
- Create: `runtime/processdriver_message_semantics_test.go`
- Modify: `README.md`, `runtime/README.md` if it documents `WithSignalBus`

- [ ] **Step 1: Write the failing tests**

| case | assertion |
|---|---|
| fan-out default: three instances on one `(name, key)` all resume | ADR-0156 |
| strict mode: same setup returns `ErrAmbiguousMessageCorrelation`, none resume | opt-in |
| **deliver AND start: a waiter resumes *and* a matching message-start fires** | ADR-0156 |
| keyed start: a second delivery dedups (`ErrInstanceExists` → no-op) | ADR-0121 bound |
| singleton start: at most one instance ever | ADR-0121 bound |
| **keyless non-singleton start: each delivery mints a fresh instance** | the documented amplification — pin it so it is deliberate, not accidental |
| `ErrAmbiguousMessageStart` is joined, delivery still happened | no early abort |
| within one instance, a message lands once even with two matching constructs | D9 |
| empty name is a clean no-op | ADR-0152 |

- [ ] **Step 2–4:** RED, implement only if a gap is found, GREEN.

- [ ] **Step 5: Full gate + commit**

```bash
go test -race -coverprofile=cover.out ./... > /tmp/t11.txt 2>&1; echo "exit=$?"
scripts/coverage.sh cover.out          # READ the number; the exit code proves nothing
go tool cover -func=cover.out | grep -E 'runtime/delivery|runtime/kernel|engine/'
golangci-lint run ./... ; echo "lint=$?"
```

Coverage targets: `runtime/delivery` ≥ 90 %, `runtime/kernel` ≥ 87 %,
`engine` ≥ 90 % (all at or above their current levels — do not regress).

```bash
git commit -m "test(runtime): message delivery semantics; docs refresh (ADR-0156)"
```

---

## Delivery Gate

Before merging to `main` (CLAUDE.md § Git Discipline):

1. `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` —
   read the number, do not trust the exit code.
2. `go test ./...` from the repo root — no regressions elsewhere.
3. `golangci-lint run ./...` clean.
4. `/code-review` on the pending change — fix **all** findings, folded via `--amend`.
5. `/security-review` — fix **all** findings, folded via `--amend`.
6. Squash the task commits into **one feature bundle** carrying implementation,
   tests, the four ADRs, the spec and this plan, then merge `--no-ff`.

Findings adjudicated as false-positive or out-of-scope must be stated explicitly
with the reason — silence is not an adjudication.

---

## Self-review

**Spec coverage.** §3.2→T2, §3.3→T8b, §3.4→T8b, §3.5→T11, §3.6→T8a+T8b, §3.7→T4,
§3.8→T8b, §3.9→T3+T6+T8a, §3.10→ADR prose (no code), §3.11→T1. §4 API table→T8b+T9.
§5 edge cases 1–12→T2/T5/T6/T8a/T10. §6 testing→T1,T2,T3,T5,T6,T8a,T10,T11.

---

## ⚠ AUDIT STATUS: **INCOMPLETE — this bundle has NOT cleared the rule-#9 gate**

Three Opus adversarial auditors were dispatched at `62ff37e` (source verification /
design holes / plan executability). **All three were killed mid-run by a session
limit and returned no findings.** Per CLAUDE.md rule #9, *"a bundle that has not
survived its audit is not an input to implementation"* — so the audit MUST be
re-run before Task 1 starts. The three briefs are saved verbatim in
`docs/plans/2026-07-28-audit-briefs.md`; dispatch them as-is.

### Findings folded so far (controller self-verification only, not the audit)

Verified directly against the tree at `62ff37e`:

| # | Sev | Where | Finding | Status |
|---|---|---|---|---|
| F1 | Critical | T1 Step 1 | `engine.Step` takes **five** params — `Step(ctx, def, st, trg, opt StepOptions)` (`engine/step.go:77`). The sample called it with four. | **fixed below** |
| F2 | Critical | T1 Step 1 | **None** of the seven named fixtures exist. | **fixed below** |
| F3 | Important | T4 Step 3 | MySQL types are `VARCHAR(255)` / `DATETIME(6)` / `JSON` / `SMALLINT` — the plan said `TIMESTAMP(6)`. SQLite `kind` is `INTEGER`, not `SMALLINT`. | **fixed below** |
| F4 | Important | T4 Step 3 | **New risk.** MySQL InnoDB index-key limit is 3072 bytes. A PK of `(VARCHAR(255), SMALLINT, VARCHAR(255), VARCHAR(255))` under `utf8mb4` is 3·255·4 + 2 = **3062 bytes** — 10 bytes of headroom, and it breaks outright if any width or the charset changes. | **fixed below** |
| F5 | Minor | T9 | `processtest` also exposes `Harness.Bus() *signal.SignalBus` (`processtest/harness.go:344`), not just the option. Both are public API that must go. | **fixed below** |
| F6 | Minor | T1 | `tokenIDsAwaitingSignal` lives at `engine/step_state.go:117`, not in `step_triggers.go`. | cite corrected |
| F7 | Important | all tasks | Every verification command used `… 2>&1 \| tail -N; echo "exit=$?"`, which reports **`tail`'s** status — the exact trap recorded in `[[verify-exit-code-not-pipeline]]`. All 17 occurrences rewritten to redirect first, then echo `$?`, then tail the file. | **fixed** |

**Verified sound, no change needed:** `gomock.Cond` uses the generic
`func(x T) bool` form from mock v0.5+, matching the plan's sample (`go.mod` pins
`go.uber.org/mock v0.6.0`). Placing generated mocks in the public `runtime/kernel`
package follows the established `service/deadletter_mock.go` precedent, so gomock
as a non-test dependency of a public package is already the status quo.
`kernel.NormalizeLimit`, `EncodeCursor`, `DecodeCursor`, `transaction.JoinOrBegin`,
`isNilDep`, `dialect.Dialect.TimestampsAsText()` (a **method**), `timeArg`,
`parseTimeText`, `engine.NewSignalReceived(at, name, payload)` and
`engine.NewMessageReceived(at, name, correlationKey, payload)` all exist with the
signatures the plan assumes.

### F1 + F2 + F6 correction — Task 1 Step 1

`engine.Step`'s call must be:

```go
res, err := engine.Step(ctx, tc.def, st, trg, engine.StepOptions{})
```

No fixture helper named in the original sketch exists. What **does** exist in
`engine/*_test.go`, and is the right basis:

| existing helper | use |
|---|---|
| `interruptingMessageBoundaryDef()` | closest template — copy and swap the message boundary for a signal boundary |
| `nonInterruptingMessageBoundaryDef()` | template for the repeatable-arm case |
| `parkedAtUserTask(t, def, at) engine.InstanceState` | drive a def to a parked host |
| `stepToParked(t, def) (engine.InstanceState, engine.InvokeAction)` | parked + the emitted action |
| `findTokenByNodeID(t, tokens, nodeID) engine.Token` | assert token placement |
| `fullyPopulatedBoundaryArm() boundaryArm` | white-box arm construction |
| `closeKindOf(st, nodeID) (engine.CloseKind, bool)` | assert how a token closed |

Task 1 must therefore **write its own** two-host signal-boundary fixture, modelled
on `interruptingMessageBoundaryDef`, and assert via `findTokenByNodeID` +
`closeKindOf` rather than the invented `activeNodeIDs`/`countTokensAt`. Note
`fullyPopulatedBoundaryArm` is white-box (`package engine`), so a test needing it
cannot be `package engine_test` — prefer black-box and assert through
`engine.StepResult`.

### F3 + F4 correction — Task 4 Step 3

MySQL, matching `wrkflw_timers` / `wrkflw_instances` in that file:

```sql
CREATE TABLE wrkflw_waiters (
    instance_id     VARCHAR(191) NOT NULL,
    kind            SMALLINT     NOT NULL,
    name            VARCHAR(191) NOT NULL,
    correlation_key VARCHAR(191) NOT NULL DEFAULT '',
    PRIMARY KEY (instance_id, kind, name, correlation_key)
);
CREATE INDEX wrkflw_waiters_lookup_idx ON wrkflw_waiters (kind, name, correlation_key);

CREATE TABLE wrkflw_undelivered (
    id              VARCHAR(255) PRIMARY KEY,
    instance_id     VARCHAR(255) NOT NULL,
    kind            SMALLINT     NOT NULL,
    name            VARCHAR(255) NOT NULL,
    correlation_key VARCHAR(255) NOT NULL DEFAULT '',
    payload         JSON         NULL,
    occurred_at     DATETIME(6)  NOT NULL,
    failed_at       DATETIME(6)  NOT NULL,
    attempts        INT          NOT NULL,
    cause           TEXT         NOT NULL
);
```

`VARCHAR(191)` **only** on `wrkflw_waiters`, and only because all three string
columns sit in one PK: 3·191·4 + 2 = 2294 bytes, comfortably under the 3072-byte
InnoDB limit, versus 3062 at `VARCHAR(255)`. `wrkflw_undelivered` keeps
`VARCHAR(255)` — its PK is `id` alone.

⚠ **191 is a real constraint on `name`, not a formality.** A signal or message name
longer than 191 characters would be truncated or rejected on MySQL only. The audit
must decide whether to (a) accept and document the limit, (b) add a definition-time
validation rule capping event-name length, or (c) use a generated hash column as
the PK with the natural key as a plain index. **Do not implement Task 4 until this
is resolved** — it is a cross-dialect behaviour difference, exactly the class of
issue ADR-0081's neutral store exists to prevent.

SQLite: `kind INTEGER NOT NULL` (not `SMALLINT`), `payload TEXT`,
`occurred_at TEXT NOT NULL`, `failed_at TEXT NOT NULL`, ids `TEXT`.

### F5 correction — Task 9

The grep in Task 9 Step 1 already finds it, but state it explicitly: `processtest`
exposes **both** `WithSignalBus() Option` (`harness.go:114`) and
`Harness.Bus() *signal.SignalBus` (`harness.go:344`). `Bus()` returns a type from
the deleted package, so it cannot survive; delete both and let the harness wire a
`MemWaiterStore` unconditionally.

### Still open for the audit — do NOT treat as resolved

1. `kernel.EncodeCursor`'s payload fields are `StartedAt`/`InstanceID`
   (`runtime/kernel/lister.go:26`), which do not fit an undelivered cursor. Task 3
   says add a sibling encoder; that is asserted, not validated against consumer
   expectations.
2. Everything the three killed auditors were briefed to find: multi-replica race
   analysis, transaction-interleaving/isolation, the destructive edges of
   deliver-AND-start, whether fan-out breaks a downstream 1:1 assumption,
   ADR-0158 arm-identity-after-mutation soundness, replay-into-a-re-armed-waiter,
   self-heal deleting a legitimate row, missing observability, and whether the
   `service`/transport layers need to expose any of this.
