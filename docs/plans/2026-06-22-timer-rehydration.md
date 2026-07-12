# Timer Rehydration on Restart — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist pending timers (with their `FireAt`) in a runtime-owned side table written atomically with instance state, and re-arm them on startup via a one-shot `Runner.RehydrateTimers(ctx)` — so a process restart no longer loses parked-on-timer instances.

**Architecture:** Mirror the async-call-activity pattern (ADR-0024/0025) exactly. A new `runtime.TimerStore` read port + `ArmedTimer` value; `AppliedStep` gains `TimerArms`/`TimerCancels` derived per step by a pure helper over the step's commands + trigger; the `Store` writes them in the same commit tx. Rehydration lists armed timers and re-arms each via the existing fire-callback. The engine/model are untouched.

**Tech Stack:** Go 1.25, pgx/Postgres 17, goose migrations, clockwork fake clock, testcontainers, testify, project `table-test` skill.

## Global Constraints

- **TDD strict:** every new symbol/behavior gets a failing test with a visible RED (`go test ./<pkg>/...`) before the impl. (CLAUDE.md "TDD Operational Discipline".)
- **Engine/model purity — ZERO production diff:** `git diff <branch-base> -- engine model` must be empty at the end. This track touches `runtime/`, `internal/persistence/postgres/`, `persistence/` only. No new vendor imports (`gocron`/`clockwork`/`watermill`/`casbin` stay confined).
- **Error sentinel prefix:** new error messages prefix the package segment with `workflow-` (e.g. `workflow-runtime: ...`); assert on sentinels with `errors.Is`, never string-matching.
- **Atomicity:** timer arm/cancel side-effects MUST be applied in the same `Store` Create/Commit transaction as the snapshot/journal/outbox write (the ADR-0025 contract).
- **Opt-in:** absent `WithTimerStore`, behavior is unchanged (timers in-memory only). `NewMemStore`/`NewStore` preserved; new `NewMemStoreWithTimers` / Store works whether or not timer ops are present (nil/empty = no-op).
- **Tests:** black-box (`package <pkg>_test`); table-driven with the **`assert` closure per case** (project `table-test` skill, not want/wantErr); `t.Context()`; pair each `foo.go` with `foo_test.go`. Postgres tests use `database.RunTestDatabase(t)` and **run with `-p 1`**.
- **Lint:** `golangci-lint` v2. **Verify on completion:** `go test -race -p 1 ./...` green; touched pkgs ≥85%; lint clean.
- **Commits:** Conventional Commits scoped to the area; end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Branch:** `feat/timer-rehydration` (never implement on `main`).

**Verified anchor points (from the current code):**
- `AppliedStep` is in `runtime/ports.go` (fields: `State`, `Trigger`, `Events`, `NewCallLink`, `CallOutcome`).
- `MemStore` is in `runtime/memstore.go`: `Create` ~line 57 (call-link record ~64), `Commit` ~line 84 (call-link markTerminal ~99), `NewMemStoreWithCallLinks` ~line 47.
- deliverLoop assembly is `runtime/runner.go` ~line 361 (`events := outboxEventsFor(res.Commands)`) → `appliedStep := AppliedStep{...}` ~line 379 → `Create`/`Commit` ~386/389.
- `perform(ScheduleTimer)` + inline fire callback is `runtime/runner.go` (the `case engine.ScheduleTimer:` block).
- `Runner` fields/options ~`runner.go:74` (`callLinks`), `WithCallLinks` ~`runner.go:164`.
- Postgres `Store.Create` ~`store.go:81` (`insertCallLink` ~123), `Commit` ~166 (`flipCallLink` ~210); helpers `insertCallLink` ~301 / `flipCallLink` ~326. Migration template `migrations/0004_call_links.sql`.
- `engine.ScheduleTimer{TimerID, Token, FireAt, Kind}`; `engine.CancelTimer` (has `TimerID`); `engine.TimerFired` (has `TimerID`, built by `engine.NewTimerFired(now, timerID)`); `engine.TimerKind`.

---

### Task 1: Runtime timer data plumbing (`ArmedTimer`, `TimerStore`, `MemTimerStore`, `AppliedStep` fields, `MemStore` + `WithTimerStore`)

**Files:**
- Create: `runtime/timerstore.go`, `runtime/timerstore_test.go`
- Modify: `runtime/ports.go` (add `TimerArms`/`TimerCancels` to `AppliedStep`)
- Modify: `runtime/memstore.go` (record arms on Create, apply cancels on Commit; `NewMemStoreWithTimers`)
- Modify: `runtime/runner.go` (`timerStore` field + `WithTimerStore` option)
- Modify: `runtime/memstore_test.go` (atomicity test) — or add to `timerstore_test.go`

**Interfaces:**
- Produces: `runtime.ArmedTimer{InstanceID, DefID string; DefVersion int; TimerID string; FireAt time.Time; Kind engine.TimerKind}`; `runtime.TimerStore` interface with `ListArmed(ctx) ([]ArmedTimer, error)`; `runtime.MemTimerStore` (implements `TimerStore`, plus unexported `arm`/`cancel` used by `MemStore`); `AppliedStep.TimerArms []ArmedTimer` + `AppliedStep.TimerCancels []string`; `runtime.NewMemStoreWithTimers(*MemTimerStore) *MemStore`; `runtime.WithTimerStore(TimerStore) Option`; `Runner.timerStore TimerStore` field.

- [ ] **Step 1: Write the failing test** — `runtime/timerstore_test.go`

```go
package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestMemTimerStore(t *testing.T) {
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	mk := func(id string, at time.Time) runtime.ArmedTimer {
		return runtime.ArmedTimer{InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: id, FireAt: at, Kind: engine.TimerIntermediate}
	}
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "arm then ListArmed returns it",
			assert: func(t *testing.T) {
				s := runtime.NewMemTimerStore()
				s.Arm(mk("t1", base))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "t1", got[0].TimerID)
				assert.Equal(t, base, got[0].FireAt)
			},
		},
		{
			name: "re-arm same id upserts FireAt (no duplicate)",
			assert: func(t *testing.T) {
				s := runtime.NewMemTimerStore()
				s.Arm(mk("t1", base))
				s.Arm(mk("t1", base.Add(time.Hour)))
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, base.Add(time.Hour), got[0].FireAt)
			},
		},
		{
			name: "cancel removes it",
			assert: func(t *testing.T) {
				s := runtime.NewMemTimerStore()
				s.Arm(mk("t1", base))
				s.Cancel("i1", "t1")
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
		{
			name: "cancel unknown is a no-op",
			assert: func(t *testing.T) {
				s := runtime.NewMemTimerStore()
				s.Cancel("i1", "nope")
				got, err := s.ListArmed(t.Context())
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t) })
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/... -run TestMemTimerStore`
Expected: FAIL — `undefined: runtime.NewMemTimerStore` / `runtime.ArmedTimer`.

- [ ] **Step 3: Write minimal implementation** — `runtime/timerstore.go`

```go
package runtime

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/kartaladev/wrkflw/engine"
)

// ArmedTimer is one timer currently armed (scheduled, not yet fired or cancelled).
// DefID/DefVersion are stored so RehydrateTimers can resolve the process
// definition via the registry without loading instance state per timer.
type ArmedTimer struct {
	InstanceID string
	DefID      string
	DefVersion int
	TimerID    string
	FireAt     time.Time
	Kind       engine.TimerKind
}

// TimerStore is the read-side port for enumerating armed timers at startup. The
// write side is fused into the transactional Store (AppliedStep.TimerArms /
// TimerCancels), atomically with the state commit — see ADR-0027.
type TimerStore interface {
	// ListArmed returns all timers currently armed, ordered by
	// (FireAt, InstanceID, TimerID) for deterministic re-arm order.
	ListArmed(ctx context.Context) ([]ArmedTimer, error)
}

// MemTimerStore is the in-memory reference TimerStore. It is both the write
// target (MemStore records arms/cancels into it) and the read source.
type MemTimerStore struct {
	mu     sync.Mutex
	armed  map[timerKey]ArmedTimer
}

type timerKey struct{ instanceID, timerID string }

// NewMemTimerStore constructs an empty in-memory TimerStore.
func NewMemTimerStore() *MemTimerStore {
	return &MemTimerStore{armed: make(map[timerKey]ArmedTimer)}
}

// Arm records (or upserts) an armed timer.
func (s *MemTimerStore) Arm(t ArmedTimer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed[timerKey{t.InstanceID, t.TimerID}] = t
}

// Cancel removes an armed timer; a no-op if absent.
func (s *MemTimerStore) Cancel(instanceID, timerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.armed, timerKey{instanceID, timerID})
}

// ListArmed implements TimerStore.
func (s *MemTimerStore) ListArmed(_ context.Context) ([]ArmedTimer, error) {
	s.mu.Lock()
	out := make([]ArmedTimer, 0, len(s.armed))
	for _, t := range s.armed {
		out = append(out, t)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FireAt.Equal(out[j].FireAt) {
			return out[i].FireAt.Before(out[j].FireAt)
		}
		if out[i].InstanceID != out[j].InstanceID {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].TimerID < out[j].TimerID
	})
	return out, nil
}

var _ TimerStore = (*MemTimerStore)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/... -run TestMemTimerStore`
Expected: PASS.

- [ ] **Step 5: Add `AppliedStep` fields** — `runtime/ports.go`

Add to the `AppliedStep` struct (after `CallOutcome`):

```go
	// TimerArms are timers armed by this step (one per ScheduleTimer command).
	// The Store upserts them into the armed-timers table atomically with the
	// state commit (ADR-0027). Empty unless a TimerStore is wired.
	TimerArms []ArmedTimer
	// TimerCancels are timer IDs disarmed by this step (CancelTimer commands and
	// the fired timer on a TimerFired trigger). The Store deletes them atomically
	// with the state commit. Empty unless a TimerStore is wired.
	TimerCancels []string
```

- [ ] **Step 6: Wire `Runner.timerStore` + `WithTimerStore`** — `runtime/runner.go`

Add field to the `Runner` struct (next to `callLinks CallLinkStore`):

```go
	timerStore TimerStore
```

Add the option (next to `WithCallLinks`):

```go
// WithTimerStore wires a [TimerStore] into the Runner. When set, the runtime
// records each armed/cancelled timer into the AppliedStep so the Store persists
// them atomically with state, and [Runner.RehydrateTimers] can re-arm them on
// restart. Absent this option, timers are in-memory only and lost on restart.
func WithTimerStore(store TimerStore) Option {
	return func(r *Runner) { r.timerStore = store }
}
```

- [ ] **Step 7: Add MemStore atomicity test** — `runtime/timerstore_test.go`

```go
func TestMemStoreRecordsTimerOps(t *testing.T) {
	mts := runtime.NewMemTimerStore()
	store := runtime.NewMemStoreWithTimers(mts)
	at := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	st := engine.InstanceState{InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: at}

	// Create with a TimerArm records it.
	tok, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(at, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "i1", DefID: "d", DefVersion: 1, TimerID: "t1", FireAt: at.Add(time.Hour), Kind: engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)
	armed, err := mts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1)

	// Commit with a TimerCancel removes it.
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{
		State:        st,
		Trigger:      engine.NewTimerFired(at.Add(time.Hour), "t1"),
		TimerCancels: []string{"t1"},
	})
	require.NoError(t, err)
	armed, err = mts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed)
}
```

- [ ] **Step 8: Run to verify it fails** — `go test ./runtime/... -run TestMemStoreRecordsTimerOps` → FAIL (`undefined: NewMemStoreWithTimers`).

- [ ] **Step 9: Implement MemStore timer handling** — `runtime/memstore.go`

Add the `timers` field to `MemStore` (next to `callLinks`):

```go
	timers *MemTimerStore // optional; nil means no timer tracking
```

Add the constructor (next to `NewMemStoreWithCallLinks`):

```go
// NewMemStoreWithTimers constructs a MemStore that records armed-timer
// side-effects (AppliedStep.TimerArms / TimerCancels) into mts atomically with
// each Create/Commit.
func NewMemStoreWithTimers(mts *MemTimerStore) *MemStore {
	m := NewMemStore()
	m.timers = mts
	return m
}
```

In `Create`, after the call-link record block, add:

```go
	if m.timers != nil {
		for _, a := range step.TimerArms {
			m.timers.Arm(a)
		}
		for _, id := range step.TimerCancels {
			m.timers.Cancel(step.State.InstanceID, id)
		}
	}
```

In `Commit`, after the call-link markTerminal block, add the identical block:

```go
	if m.timers != nil {
		for _, a := range step.TimerArms {
			m.timers.Arm(a)
		}
		for _, id := range step.TimerCancels {
			m.timers.Cancel(step.State.InstanceID, id)
		}
	}
```

(Both Create and Commit handle arms+cancels because a single deliverLoop may arm on Create — e.g. a process that immediately parks on a timer — or on later Commits.)

- [ ] **Step 10: Run to verify it passes** — `go test ./runtime/... -run 'TestMemTimerStore|TestMemStoreRecordsTimerOps'` → PASS. Then `go test ./runtime/...` → all green (no regressions). `golangci-lint run ./runtime/...` → clean.

- [ ] **Step 11: Commit**

```bash
git add runtime/timerstore.go runtime/timerstore_test.go runtime/ports.go runtime/memstore.go runtime/runner.go
git commit -m "feat(runtime): TimerStore port + MemTimerStore + AppliedStep timer side-effects

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Derive timer ops in deliverLoop (`timerOpsFor` pure helper)

**Files:**
- Modify: `runtime/runner.go` (add `timerOpsFor`; populate `appliedStep.TimerArms/TimerCancels`)
- Create/Modify: `runtime/runner_test.go` or `runtime/timerstore_test.go` (helper test + deliverLoop integration)

**Interfaces:**
- Consumes: `AppliedStep.TimerArms/TimerCancels`, `Runner.timerStore` (Task 1).
- Produces: `timerOpsFor(cmds []engine.Command, trg engine.Trigger, defID string, defVersion int, instanceID string) ([]ArmedTimer, []string)` (unexported).

- [ ] **Step 1: Write the failing test** — append to `runtime/timerstore_test.go`

Note: `timerOpsFor` is unexported, so this test must be in `package runtime` (white-box). Put it in a new file `runtime/timerops_internal_test.go` with `package runtime`.

```go
package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
)

func TestTimerOpsFor(t *testing.T) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		cmds   []engine.Command
		trg    engine.Trigger
		assert func(t *testing.T, arms []ArmedTimer, cancels []string)
	}{
		{
			name: "ScheduleTimer becomes an arm",
			cmds: []engine.Command{engine.ScheduleTimer{TimerID: "t1", FireAt: at, Kind: engine.TimerIntermediate}},
			trg:  engine.NewStartInstance(at, nil),
			assert: func(t *testing.T, arms []ArmedTimer, cancels []string) {
				assert.Len(t, arms, 1)
				assert.Equal(t, "t1", arms[0].TimerID)
				assert.Equal(t, at, arms[0].FireAt)
				assert.Empty(t, cancels)
			},
		},
		{
			name: "CancelTimer becomes a cancel",
			cmds: []engine.Command{engine.CancelTimer{TimerID: "t1"}},
			trg:  engine.NewStartInstance(at, nil),
			assert: func(t *testing.T, arms []ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"t1"}, cancels)
			},
		},
		{
			name: "TimerFired trigger cancels the fired timer",
			cmds: nil,
			trg:  engine.NewTimerFired(at, "t1"),
			assert: func(t *testing.T, arms []ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"t1"}, cancels)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arms, cancels := timerOpsFor(tc.cmds, tc.trg, "d", 1, "i1")
			tc.assert(t, arms, cancels)
		})
	}
}
```

Verify the exact field names first: `grep -n "type ScheduleTimer\|type CancelTimer\|type TimerFired" engine/command.go engine/trigger.go` — confirm `CancelTimer` has `TimerID` and `TimerFired` has `TimerID`. If `CancelTimer`'s field differs, adjust the helper accordingly (do NOT change the engine).

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/... -run TestTimerOpsFor` → FAIL (`undefined: timerOpsFor`).

- [ ] **Step 3: Implement the helper** — `runtime/runner.go` (near `outboxEventsFor`)

```go
// timerOpsFor derives the armed-timer side-effects of one applied step from its
// commands and trigger. ScheduleTimer commands become arms; CancelTimer commands
// and a TimerFired trigger (the fired timer is consumed) become cancels. Pure;
// kind-agnostic so it covers every timer kind uniformly.
func timerOpsFor(cmds []engine.Command, trg engine.Trigger, defID string, defVersion int, instanceID string) ([]ArmedTimer, []string) {
	var arms []ArmedTimer
	var cancels []string
	for _, c := range cmds {
		switch cmd := c.(type) {
		case engine.ScheduleTimer:
			arms = append(arms, ArmedTimer{
				InstanceID: instanceID,
				DefID:      defID,
				DefVersion: defVersion,
				TimerID:    cmd.TimerID,
				FireAt:     cmd.FireAt,
				Kind:       cmd.Kind,
			})
		case engine.CancelTimer:
			cancels = append(cancels, cmd.TimerID)
		}
	}
	if tf, ok := trg.(engine.TimerFired); ok {
		cancels = append(cancels, tf.TimerID)
	}
	return arms, cancels
}
```

- [ ] **Step 4: Wire it into deliverLoop** — `runtime/runner.go` (right after `events := outboxEventsFor(res.Commands)` ~line 361, and set on the appliedStep literal ~line 379)

After computing `outcome`, add:

```go
		var timerArms []ArmedTimer
		var timerCancels []string
		if r.timerStore != nil {
			timerArms, timerCancels = timerOpsFor(res.Commands, t, st.DefID, st.DefVersion, st.InstanceID)
		}
```

Change the `appliedStep` literal to include them:

```go
		appliedStep := AppliedStep{State: st, Trigger: t, Events: events, CallOutcome: outcome, TimerArms: timerArms, TimerCancels: timerCancels}
```

- [ ] **Step 5: Run helper test** — `go test ./runtime/... -run TestTimerOpsFor` → PASS.

- [ ] **Step 6: Add a deliverLoop integration test** — append to `runtime/timerstore_test.go` (black-box)

```go
func TestRunnerPersistsAndClearsTimer(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := runtime.NewMemTimerStore()
	store := runtime.NewMemStoreWithTimers(mts)
	sched := runtime.NewMemScheduler(fc)
	r := runtime.NewRunner(action.NewMapCatalog(nil), fc, store,
		runtime.WithScheduler(sched), runtime.WithTimerStore(mts))

	def := timerIntermediateDef() // reuse the helper in runtime/timer_example_test.go (1h intermediate timer)
	_, err := r.Run(t.Context(), def, "tr-1", nil)
	require.NoError(t, err)

	// Armed after Run parks on the timer.
	armed, err := mts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "the pending timer must be persisted")
	assert.Equal(t, "tr-1", armed[0].InstanceID)

	// Fire it; the armed row clears (consumed via TimerFired).
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched.Tick(t.Context()))
	armed, err = mts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed, "a fired timer must leave the armed set")
}
```

Add imports: `clockwork "github.com/jonboulle/clockwork"`, `"github.com/kartaladev/wrkflw/action"`. If `timerIntermediateDef` is unexported in `timer_example_test.go` (same `runtime_test` package), it is reusable directly; otherwise replicate a minimal 1h-intermediate-timer definition inline.

- [ ] **Step 7: Run** — `go test ./runtime/...` → all green. `golangci-lint run ./runtime/...` → clean.

- [ ] **Step 8: Commit**

```bash
git add runtime/runner.go runtime/timerops_internal_test.go runtime/timerstore_test.go
git commit -m "feat(runtime): derive timer arm/cancel side-effects per applied step

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `RehydrateTimers` + fire-callback extraction

**Files:**
- Modify: `runtime/runner.go` (extract `armTimer`; add `RehydrateTimers`)
- Create: `runtime/rehydrate_test.go`

**Interfaces:**
- Consumes: `Runner.timerStore`, `Runner.sched`, `Runner.defsReg`, `ArmedTimer` (Tasks 1–2).
- Produces: `(*Runner).RehydrateTimers(ctx context.Context) error`; unexported `(*Runner).armTimer(def *model.ProcessDefinition, instanceID, timerID string, fireAt time.Time)`.

- [ ] **Step 1: Write the failing test** — `runtime/rehydrate_test.go` (black-box)

```go
package runtime_test

import (
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestRehydrateTimersResumesAfterRestart(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := runtime.NewMemTimerStore()
	store := runtime.NewMemStoreWithTimers(mts)
	def := timerIntermediateDef()
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"timerdef:1": def, // key format "DefID:DefVersion" — match def.ID/def.Version
	})

	// Original process: arm the timer, then it "crashes" — discard runner + scheduler.
	{
		sched := runtime.NewMemScheduler(fc)
		r := runtime.NewRunner(action.NewMapCatalog(nil), fc, store,
			runtime.WithScheduler(sched), runtime.WithTimerStore(mts), runtime.WithDefinitions(reg))
		_, err := r.Run(t.Context(), def, "rh-1", nil)
		require.NoError(t, err)
	}

	// New process: fresh runner + fresh scheduler, same store + timer store.
	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(action.NewMapCatalog(nil), fc, store,
		runtime.WithScheduler(sched2), runtime.WithTimerStore(mts), runtime.WithDefinitions(reg))

	require.NoError(t, r2.RehydrateTimers(t.Context()))

	// Advance + tick the NEW scheduler: the rehydrated timer fires and resumes.
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched2.Tick(t.Context()))

	final, _, err := store.Load(t.Context(), "rh-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "rehydrated timer must resume the instance")
}

func TestRehydrateTimersRequiresWiring(t *testing.T) {
	store := runtime.NewMemStore()
	r := runtime.NewRunner(action.NewMapCatalog(nil), clockwork.NewFakeClock(), store)
	err := r.RehydrateTimers(t.Context())
	require.Error(t, err, "RehydrateTimers without scheduler/timer-store/registry must error")
}
```

Confirm the registry constructor/key format first: `grep -n "func NewMapDefinitionRegistry\|DefinitionRegistry" runtime/definition_registry.go` and the def's `ID`/`Version` so the registry key `"<ID>:<Version>"` matches what `RehydrateTimers` builds. Adjust the key/def accordingly. If `timerIntermediateDef`'s ID isn't `"timerdef"`, use its real ID.

- [ ] **Step 2: Run to verify it fails** — `go test ./runtime/... -run TestRehydrateTimers` → FAIL (`r2.RehydrateTimers undefined`).

- [ ] **Step 3: Extract `armTimer`** — `runtime/runner.go`

Replace the body of the `case engine.ScheduleTimer:` arming (the `r.sched.Schedule(...)` call with its callback) by a call to a new method, preserving behavior:

```go
	case engine.ScheduleTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ScheduleTimer %q: no Scheduler configured", cmd.TimerID)
		}
		if cmd.Kind == engine.TimerRetry {
			r.obs.actionRetries.Add(ctx, 1)
		}
		r.armTimer(def, st.InstanceID, cmd.TimerID, cmd.FireAt)
		return nil, nil
```

Add the method (move the existing callback verbatim into it):

```go
// armTimer registers timerID on the scheduler with the engine's standard
// fire callback: deliver a TimerFired trigger, retrying on optimistic-CAS
// conflicts. Used by perform(ScheduleTimer) and RehydrateTimers.
func (r *Runner) armTimer(def *model.ProcessDefinition, instanceID, timerID string, fireAt time.Time) {
	r.sched.Schedule(timerID, fireAt, func() {
		fireCtx := context.Background()
		trg := engine.NewTimerFired(r.clk.Now(), timerID)
		const maxAttempts = 5
		var err error
		for range maxAttempts {
			if _, err = r.Deliver(fireCtx, def, instanceID, trg); err == nil {
				return
			}
			if !errors.Is(err, ErrConcurrentUpdate) {
				r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver failed",
					append(r.obs.tel.LogAttrs(fireCtx),
						slog.String("timer_id", timerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
				return
			}
		}
		r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver permanently dropped after CAS conflicts",
			append(r.obs.tel.LogAttrs(fireCtx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Int("attempts", maxAttempts),
				slog.Any("error", err))...)
	})
}
```

- [ ] **Step 4: Add `RehydrateTimers`** — `runtime/runner.go`

```go
// RehydrateTimers re-arms every persisted armed timer on the scheduler. Call it
// once at startup, after constructing the Runner, to recover timers lost when the
// process restarted. Requires WithScheduler, WithTimerStore, and WithDefinitions.
// A timer whose FireAt is already in the past fires immediately; a re-fire of an
// already-consumed timer is an idempotent engine no-op. Timers whose definition
// the registry cannot resolve are skipped and counted in the returned error.
func (r *Runner) RehydrateTimers(ctx context.Context) error {
	if r.sched == nil || r.timerStore == nil || r.defsReg == nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers requires WithScheduler, WithTimerStore, and WithDefinitions")
	}
	armed, err := r.timerStore.ListArmed(ctx)
	if err != nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: list armed: %w", err)
	}
	var unresolved int
	for _, a := range armed {
		ref := fmt.Sprintf("%s:%d", a.DefID, a.DefVersion)
		def, err := r.defsReg.Lookup(ref)
		if err != nil {
			unresolved++
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: rehydrate: definition not found, skipping timer",
				append(r.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", ref),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		r.armTimer(def, a.InstanceID, a.TimerID, a.FireAt)
	}
	if unresolved > 0 {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %d timer(s) skipped (definition not found)", unresolved)
	}
	return nil
}
```

Verify `DefinitionRegistry.Lookup` signature (`grep -n "Lookup" runtime/definition_registry.go`) — if it takes a `defRef string` and returns `(*model.ProcessDefinition, error)`, the above matches; otherwise adapt.

- [ ] **Step 5: Run** — `go test ./runtime/... -run TestRehydrateTimers` → PASS. Then `go test ./runtime/...` → all green (the `armTimer` extraction must not regress existing timer tests). `golangci-lint run ./runtime/...` → clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/runner.go runtime/rehydrate_test.go
git commit -m "feat(runtime): Runner.RehydrateTimers re-arms persisted timers on restart

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Postgres `wrkflw_timers` table + atomic Store side-effects

**Files:**
- Create: `internal/persistence/postgres/migrations/0005_timers.sql`
- Modify: `internal/persistence/postgres/store.go` (apply `TimerArms`/`TimerCancels` in Create + Commit tx; `upsertTimer`/`deleteTimer` helpers)
- Create: `internal/persistence/postgres/timers_test.go`

**Interfaces:**
- Consumes: `runtime.AppliedStep.TimerArms/TimerCancels`, `runtime.ArmedTimer` (Task 1).
- Produces: `wrkflw_timers` table; unexported `upsertTimer(ctx, db DBTX, t runtime.ArmedTimer)` and `deleteTimer(ctx, db DBTX, instanceID, timerID string)`.

- [ ] **Step 1: Write the migration** — `internal/persistence/postgres/migrations/0005_timers.sql`

```sql
-- +goose Up

-- Armed-timer table for timer rehydration on restart (ADR-0027). One row per
-- (instance, timer); written atomically with the instance state commit.
CREATE TABLE wrkflw_timers (
    instance_id TEXT        NOT NULL,
    timer_id    TEXT        NOT NULL,
    fire_at     TIMESTAMPTZ NOT NULL,
    kind        SMALLINT    NOT NULL,
    def_id      TEXT        NOT NULL,
    def_version INT         NOT NULL,
    PRIMARY KEY (instance_id, timer_id)
);

CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (fire_at);

-- +goose Down

DROP INDEX IF EXISTS wrkflw_timers_fire_at_idx;
DROP TABLE IF EXISTS wrkflw_timers;
```

- [ ] **Step 2: Write the failing test** — `internal/persistence/postgres/timers_test.go` (black-box `package postgres_test`)

```go
package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	"github.com/kartaladev/wrkflw/engine"
	pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestStorePersistsTimerOpsAtomically(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	store := pg.NewStore(pool)
	ts := pg.NewTimerStore(pool) // from Task 5; if Task 5 not yet merged, query the table directly in this test

	at := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	st := engine.InstanceState{InstanceID: "pti-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: at}

	tok, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(at, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "pti-1", DefID: "d", DefVersion: 1, TimerID: "t1", FireAt: at.Add(time.Hour), Kind: engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1)
	assert.Equal(t, "t1", armed[0].TimerID)
	assert.True(t, at.Add(time.Hour).Equal(armed[0].FireAt))

	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{
		State:        st,
		Trigger:      engine.NewTimerFired(at.Add(time.Hour), "t1"),
		TimerCancels: []string{"t1"},
	})
	require.NoError(t, err)

	armed, err = ts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed, "fired timer row must be deleted in the commit tx")
}
```

Note: this test depends on `pg.NewTimerStore` (Task 5). Implement Task 4 and Task 5 together if the reviewer prefers, or in this test query `wrkflw_timers` directly with `pool.Query` until Task 5 lands. Keep the assertions identical.

- [ ] **Step 3: Run to verify it fails** — `go test -p 1 ./internal/persistence/postgres/... -run TestStorePersistsTimerOpsAtomically` → FAIL (table/`NewTimerStore` missing). Requires Docker.

- [ ] **Step 4: Add the helpers + wire into Create/Commit** — `internal/persistence/postgres/store.go`

Add helpers (near `insertCallLink`/`flipCallLink`):

```go
// upsertTimer writes (or updates) a wrkflw_timers row inside tx, atomic with the
// state commit (ADR-0027). Re-arming the same (instance, timer) overwrites FireAt.
func upsertTimer(ctx context.Context, db DBTX, t runtime.ArmedTimer) error {
	_, err := db.Exec(ctx, `
		INSERT INTO wrkflw_timers (instance_id, timer_id, fire_at, kind, def_id, def_version)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (instance_id, timer_id)
		DO UPDATE SET fire_at = EXCLUDED.fire_at, kind = EXCLUDED.kind,
		              def_id = EXCLUDED.def_id, def_version = EXCLUDED.def_version`,
		t.InstanceID, t.TimerID, t.FireAt, int16(t.Kind), t.DefID, t.DefVersion)
	if err != nil {
		return fmt.Errorf("workflow-postgres: upsert timer %q/%q: %w", t.InstanceID, t.TimerID, err)
	}
	return nil
}

// deleteTimer removes a wrkflw_timers row inside tx (fired or cancelled). A
// zero-row delete is fine (idempotent / already gone).
func deleteTimer(ctx context.Context, db DBTX, instanceID, timerID string) error {
	_, err := db.Exec(ctx, `DELETE FROM wrkflw_timers WHERE instance_id = $1 AND timer_id = $2`,
		instanceID, timerID)
	if err != nil {
		return fmt.Errorf("workflow-postgres: delete timer %q/%q: %w", instanceID, timerID, err)
	}
	return nil
}

// applyTimerOps applies a step's timer arms and cancels within tx.
func applyTimerOps(ctx context.Context, db DBTX, step runtime.AppliedStep) error {
	for _, a := range step.TimerArms {
		if err := upsertTimer(ctx, db, a); err != nil {
			return err
		}
	}
	for _, id := range step.TimerCancels {
		if err := deleteTimer(ctx, db, step.State.InstanceID, id); err != nil {
			return err
		}
	}
	return nil
}
```

In `Create`, after the `insertCallLink` block (before `tx.Commit`), add:

```go
	if err := applyTimerOps(ctx, tx, step); err != nil {
		return runtime.Token{}, err
	}
```

In `Commit`, after the `flipCallLink` block (before `tx.Commit`), add the identical call:

```go
	if err := applyTimerOps(ctx, tx, step); err != nil {
		return runtime.Token{}, err
	}
```

Match the actual error-return arity/types of the surrounding code in each method (Create vs Commit may return different zero values — copy the local convention).

- [ ] **Step 5: Run** — implement Task 5's `NewTimerStore` first if needed, then `go test -p 1 ./internal/persistence/postgres/... -run TestStorePersistsTimerOpsAtomically` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/migrations/0005_timers.sql internal/persistence/postgres/store.go internal/persistence/postgres/timers_test.go
git commit -m "feat(postgres): wrkflw_timers table + atomic timer side-effects in Store

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Postgres `TimerStore` (`ListArmed`) + `persistence` façade

**Files:**
- Create: `internal/persistence/postgres/timerstore.go`
- Modify: `internal/persistence/postgres/timers_test.go` (ListArmed ordering test)
- Modify: `persistence/persistence.go` (or the file holding `NewCallLinkStore`) — add `NewTimerStore`

**Interfaces:**
- Consumes: `wrkflw_timers` table (Task 4), `runtime.TimerStore`/`ArmedTimer` (Task 1).
- Produces: `postgres.TimerStore` struct + `postgres.NewTimerStore(pool *pgxpool.Pool) *TimerStore` implementing `runtime.TimerStore`; `persistence.NewTimerStore(pool *pgxpool.Pool) runtime.TimerStore` façade.

- [ ] **Step 1: Write the failing test** — append to `internal/persistence/postgres/timers_test.go`

```go
func TestPgTimerStoreListArmedOrdered(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	store := pg.NewStore(pool)
	ts := pg.NewTimerStore(pool)

	base := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	st := engine.InstanceState{InstanceID: "ord-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: base}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(base, nil),
		TimerArms: []runtime.ArmedTimer{
			{InstanceID: "ord-1", DefID: "d", DefVersion: 1, TimerID: "later", FireAt: base.Add(2 * time.Hour), Kind: engine.TimerIntermediate},
			{InstanceID: "ord-1", DefID: "d", DefVersion: 1, TimerID: "sooner", FireAt: base.Add(time.Hour), Kind: engine.TimerIntermediate},
		},
	})
	require.NoError(t, err)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 2)
	assert.Equal(t, "sooner", armed[0].TimerID, "ordered by FireAt ascending")
	assert.Equal(t, "later", armed[1].TimerID)
	assert.Equal(t, "d", armed[0].DefID)
	assert.Equal(t, 1, armed[0].DefVersion)
}
```

- [ ] **Step 2: Run to verify it fails** — `go test -p 1 ./internal/persistence/postgres/... -run TestPgTimerStoreListArmedOrdered` → FAIL.

- [ ] **Step 3: Implement** — `internal/persistence/postgres/timerstore.go`

```go
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

// TimerStore is the Postgres-backed runtime.TimerStore. It reads armed timers
// from wrkflw_timers (written transactionally by Store). See ADR-0027.
type TimerStore struct {
	pool *pgxpool.Pool
}

// NewTimerStore constructs a TimerStore over pool. The pool must already have
// migrations applied (see Migrate).
func NewTimerStore(pool *pgxpool.Pool) *TimerStore {
	return &TimerStore{pool: pool}
}

// ListArmed implements runtime.TimerStore, ordered by (fire_at, instance_id, timer_id).
func (s *TimerStore) ListArmed(ctx context.Context) ([]runtime.ArmedTimer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, def_id, def_version, timer_id, fire_at, kind
		FROM   wrkflw_timers
		ORDER  BY fire_at, instance_id, timer_id`)
	if err != nil {
		return nil, fmt.Errorf("workflow-postgres: list armed timers: %w", err)
	}
	defer rows.Close()

	var out []runtime.ArmedTimer
	for rows.Next() {
		var a runtime.ArmedTimer
		var kind int16
		if err := rows.Scan(&a.InstanceID, &a.DefID, &a.DefVersion, &a.TimerID, &a.FireAt, &kind); err != nil {
			return nil, fmt.Errorf("workflow-postgres: scan armed timer: %w", err)
		}
		a.Kind = engine.TimerKind(kind)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-postgres: iterate armed timers: %w", err)
	}
	return out, nil
}

var _ runtime.TimerStore = (*TimerStore)(nil)
```

- [ ] **Step 4: Add the façade** — `persistence/persistence.go` (mirror `NewCallLinkStore`)

```go
// NewTimerStore returns a runtime.TimerStore backed by Postgres, for
// Runner.RehydrateTimers. The pool must already have migrations applied.
func NewTimerStore(pool *pgxpool.Pool) runtime.TimerStore {
	return postgres.NewTimerStore(pool)
}
```

Confirm the import alias for the internal package matches the file's existing convention (`grep -n "internal/persistence/postgres" persistence/persistence.go`).

- [ ] **Step 5: Run** — `go test -p 1 ./internal/persistence/postgres/... ./persistence/...` → PASS. `golangci-lint run ./internal/persistence/postgres/... ./persistence/...` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/postgres/timerstore.go internal/persistence/postgres/timers_test.go persistence/persistence.go
git commit -m "feat(persistence): Postgres TimerStore.ListArmed + persistence.NewTimerStore facade

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Postgres crash-safety e2e (rehydrate → fire → resume)

**Files:**
- Create: `internal/persistence/postgres/rehydrate_e2e_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5.

- [ ] **Step 1: Write the e2e test** — `internal/persistence/postgres/rehydrate_e2e_test.go` (black-box)

```go
package postgres_test

import (
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/database"
	"github.com/kartaladev/wrkflw/engine"
	pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
	"github.com/kartaladev/wrkflw/model"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestPostgresTimerRehydrationResumesAfterRestart(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	startAt := time.Date(2026, 6, 22, 16, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	def := intermediateTimerDef(t) // a 1h intermediate-timer definition; reuse the pkg's existing test helper if present
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		def.ID + ":1": def,
	})

	// Original "process": arm via store1, then discard store1 + its runner/scheduler.
	store1 := pg.NewStore(pool)
	ts := pg.NewTimerStore(pool)
	{
		sched := runtime.NewMemScheduler(fc)
		r1 := runtime.NewRunner(action.NewMapCatalog(nil), fc, store1,
			runtime.WithScheduler(sched), runtime.WithTimerStore(ts), runtime.WithDefinitions(reg))
		_, err := r1.Run(t.Context(), def, "pgrh-1", nil)
		require.NoError(t, err)
	}
	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "timer persisted to Postgres")

	// "Restart": brand-new Store, scheduler, runner reading the same DB.
	store2 := pg.NewStore(pool)
	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(action.NewMapCatalog(nil), fc, store2,
		runtime.WithScheduler(sched2), runtime.WithTimerStore(ts), runtime.WithDefinitions(reg))
	require.NoError(t, r2.RehydrateTimers(t.Context()))

	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched2.Tick(t.Context()))

	final, _, err := store2.Load(t.Context(), "pgrh-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)

	armed, err = ts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed, "fired timer cleared from Postgres")
}
```

Provide `intermediateTimerDef(t)` (or reuse an existing Postgres-package timer-definition helper — `grep -rn "Intermediate\|TimerIntermediate\|wait1h\|timer" internal/persistence/postgres/*_test.go`). The definition must: start → intermediate timer (1h) → a no-op/end so the instance completes when the timer fires. If the catalog needs an action, register it; otherwise use a definition that completes on timer fire alone.

- [ ] **Step 2: Run to verify it fails first if any wiring is missing, then passes** — `go test -p 1 ./internal/persistence/postgres/... -run TestPostgresTimerRehydrationResumesAfterRestart`. Expected: PASS once Tasks 1–5 are in.

- [ ] **Step 3: Commit**

```bash
git add internal/persistence/postgres/rehydrate_e2e_test.go
git commit -m "test(postgres): timer rehydration crash-safety e2e (restart -> resume)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: ADR-0027 + HANDOVER + final gate

**Files:**
- Create: `docs/adr/0027-timer-rehydration.md`
- Modify: `docs/plans/HANDOVER.md`

- [ ] **Step 1: Write ADR-0027** (Nygard template — Status: Accepted, Date: 2026-06-22)

Follow `docs/adr/0001-record-architecture-decisions.md` format and a recent one (e.g. `0024`/`0026`) for house style. Cover: Context (FireAt not persisted; restart loses in-memory jobs; verified facts); Decision (runtime-owned `wrkflw_timers` side table written atomically in the commit tx via `AppliedStep.TimerArms/TimerCancels`, engine untouched — same shape as ADR-0024/0025; one-shot `Runner.RehydrateTimers`; opt-in via `WithTimerStore`; kind-agnostic derivation; idempotent re-fire); Consequences (timers survive restart; engine purity preserved; one new table + migration; multi-replica exclusivity deferred; pruning/observability follow-ups). Cross-reference ADR-0024/0025 (call-link precedent) and ADR-0009 (scheduling).

- [ ] **Step 2: Run the full verification gate**

```bash
go test -race -p 1 -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
git diff main -- engine model    # MUST be empty (zero engine/model production diff)
```

Expected: all tests green; touched pkgs (runtime, internal/persistence/postgres, persistence) ≥85%; lint 0; empty engine/model diff.

- [ ] **Step 3: Update HANDOVER.md** — add a "Timer rehydration on restart sub-project — ✅ COMPLETE" section (mirror the existing track sections: branch, ADR-0027, what shipped table, gate numbers, deferred follow-ups: multi-replica exclusivity, pruning, rehydration observability). Remove "timer rehydration" from the START-HERE top picks; promote the next pick (`CancelInstance`). Update the "Production-hardening" backlog bullet (drop "timer rehydration").

- [ ] **Step 4: Commit**

```bash
git add docs/adr/0027-timer-rehydration.md docs/plans/HANDOVER.md
git commit -m "docs(adr): 0027 timer rehydration on restart; mark complete in HANDOVER

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §1 `TimerStore` port + `ArmedTimer` → Task 1. ✅
- §2 atomic writes via `AppliedStep` + Store tx → Tasks 1 (fields/Mem), 2 (derivation), 4 (Postgres tx). ✅
- §3 one-shot `RehydrateTimers` + `armTimer` extraction → Task 3. ✅
- §4 wiring/opt-in (`WithTimerStore`, `NewMemStoreWithTimers`, Postgres table/migration/façade) → Tasks 1, 4, 5. ✅
- §5 ADR-0027 + Mem/Postgres e2e → Tasks 3 (Mem e2e), 6 (Postgres e2e), 7 (ADR). ✅
- Testing strategy (MemTimerStore, derivation helper, MemStore atomicity, Mem rehydration e2e, Postgres crash-safety e2e, misconfig) → Tasks 1–3, 6. ✅
- Verification gate (incl. zero engine/model diff) → Task 7. ✅

**Placeholder scan:** All code steps show complete code. Tasks 4/5 note an inter-task ordering nicety (Task 4's test references Task 5's `NewTimerStore`) with an explicit fallback (direct table query) — bounded, not a TODO. Definition-helper reuse (`timerIntermediateDef`/`intermediateTimerDef`) is flagged with a grep to confirm the real name before use — necessary because the helper lives in existing test files this plan can't restate verbatim without risking drift; the shape (start → 1h intermediate timer → complete) is specified.

**Type consistency:** `ArmedTimer`, `TimerStore.ListArmed`, `AppliedStep.TimerArms/TimerCancels`, `WithTimerStore`, `NewMemStoreWithTimers`, `RehydrateTimers`, `armTimer`, `timerOpsFor`, `upsertTimer`/`deleteTimer`/`applyTimerOps`, `postgres.NewTimerStore`, `persistence.NewTimerStore` are used consistently across tasks. `engine.TimerKind` stored as `int16` in Postgres, matching the existing `Status` encoding convention.

**Engine-purity guard:** every task touches only `runtime/`, `internal/persistence/postgres/`, `persistence/`, `docs/`. Task 7 asserts the empty `git diff main -- engine model`.
