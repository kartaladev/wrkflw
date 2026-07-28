# Empty Identity Keys Must Match No Record — Implementation Plan

## ▶ Progress

**Status: ✅ IMPLEMENTED AND DELIVERED. All 9 tasks complete.**

| | |
|---|---|
| Branch | `fix/empty-identity-key-matches-nothing` |
| Bundle commit | `3dbb9e2` — `feat(engine)!: empty identity keys match no record (ADR-0152)` |
| Base | `bfad428` = `main` |
| Shape | 23 files, +4577/−31, squashed from 12 per-task commits into ONE feature bundle |

### What landed

All three layers of the ADR:

1. **16 state-layer guards** — `engine/state_timers.go` (4), `engine/state_arms.go` (5),
   `engine/step_state.go` (6), `engine/state.go` (1).
2. **`Step`-boundary validation** — `engine.ErrEmptyTriggerKey` + `validateTriggerKey`
   (`engine/trigger_validate.go`), wired as the first statement of `Step` before
   `cloneState`; 11 of 15 sealed `Trigger` variants validated, 4 exempt, exhaustiveness
   test-pinned. Classified HTTP 400 in `transport/http/httpcore/errors.go`.
3. **Authoring-time validation** — `definition/model`: `ErrEmptyMessageName` (a
   `ReceiveTask` needs a message name) and `ErrBlankEventName` (a *declared*
   `SignalName`/`MessageName` must not be whitespace-only).

### Scope change vs the original design

Mid-implementation the user asked whether the guards should use
`strings.TrimSpace(key) == ""`. Adjudicated and approved: **no in the engine, yes at the
authoring layer.** Id-shaped keys are engine-minted (`nextID` → `h1`, `tm3`) so nothing
can hold `" "`; name-shaped keys are definition-authored (`step_nodes.go:98`, `:724`), so
trimming in the engine would strand a token legitimately parked on a whitespace-named
message. `ErrBlankEventName` and the widened `ErrEmptyMessageName` predicate are the
result. **This is authoring hygiene, not a live-bug fix** — a whitespace name is
self-consistent at runtime today. ADR-0152 gained a "Whitespace names" subsection
recording this. ⚠ The event-kind discriminators (`validate.go:299-300`, `:531`, `:777`)
deliberately keep exact `""` comparisons: trimming them would reclassify a
`SignalName: " "` boundary as an **error boundary**.

### Verification (on the squashed bundle)

```
BUILD=0   VET=0   go test -race ./... =0   LINT=0
```
64 packages, **zero SKIP, zero FAIL**. Docker was up, so the Postgres/MySQL testcontainer
suites genuinely ran (`internal/persistence/store` 54.3s).

Coverage — `engine` **90.6%**, `definition/model` **93.6%**,
`transport/http/httpcore` **94.1%**; all above the 85% per-touched-package floor.
⚠ Repo-wide total is **73.1%**, below 85% — **verified pre-existing, not a regression**:
`main` measures **73.0%**, so this change improves it by 0.1pp. Note `scripts/coverage.sh`
only *reports*; it never enforces a floor, so its exit code proves nothing.

### Review record

Each of the 8 implementation tasks passed its own task-scoped review. Task 7 needed one
fix round (a doc comment overclaiming the whitespace case as a token leak). The final
whole-branch review returned **zero Critical, zero Important** and 4 Minor, all fixed in
one wave and re-reviewed clean. The final reviewer specifically hunted for a call path
where an empty key was load-bearing and now silently breaks, and found none.

### Deferred / known-open

- **Engine `" "` and sentinel residual.** Guards match `""` exactly. `model.Validate` now
  catches whitespace event names, but `Step` does not *require* a definition to have been
  validated, so a consumer skipping validation can still feed one in. A `"none"`-style
  sentinel is uncaught at every layer. Accepted, recorded in ADR Consequences.
- **No route-level test** drives an empty trigger key end-to-end through
  fiber/gin/stdlib/parity — the 400 is pinned at the classifier only. Deferred as
  consistent with ADR-0146's identical precedent (one shared classifier, unit-tested once).
- **The redundant `cancelTimersByTaskID` call** in `cancelTokenWaits` (all three
  `timerRecord` sites set `Token: tok.ID`, so `cancelTimersForToken` already covers it).
  Deleting it is viable but reorders emitted `CancelTimer` commands.
- **Repo-wide coverage 73.1%** — worth its own backlog item; untested `examples/`
  reference wiring and transport adapters are the drag.

### Verification commands

⚠ Never `cmd | tail ; echo $?` — that reports *`tail`'s* status and is always 0.

```bash
LOG="${TMPDIR:-/tmp}/wrkflw.log"
go build ./... ; echo "BUILD=$?"
go test -race -coverprofile=cover.out ./... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
scripts/coverage.sh cover.out      # REPORTS only — does not enforce
golangci-lint run ./... ; echo "LINT=$?"
```

### Source-verified facts (true as of this commit — do not re-derive)

- `TimerRetry` is the **only** timer-record kind created without a `TaskID`
  (`engine/step_triggers.go:302`). The other two sites always set it
  (`engine/step_nodes.go:586,659`).
- All three `timerRecord` sites set `Token: tok.ID`, so `cancelTimersByTaskID` inside
  `cancelTokenWaits` is **wholly redundant**, not just when the key is empty. Deleting it
  is a viable alternative; deferred because it reorders emitted `CancelTimer` commands.
- Arms may carry **no** non-empty match field: `armBoundaries`
  (`engine/step_boundaries.go:38-70`) is an `if / else if / else if` chain with no else,
  so an **error boundary** yields an all-empty `triggerMatch`. The comment at
  `state_arms.go:11-12` is wrong; do not cite it as authority.
- ⚠ `TimerFired.TimerID` must **NOT** be validated —
  `engine/step_timers_test.go:113-157` pins an empty `TimerID` as a clean no-op.
- ⚠ `excludeTimerID` (2nd param of both cancel helpers) must **NOT** be guarded —
  `""` means "exclude nothing" and five of seven call sites pass it.
- `service.DeliverSignal` (`service/service.go:362`) has **no** empty-name guard, unlike
  `runtime.BroadcastSignal` and `runtime.DeliverMessage`
  (`processdriver_message.go:57-60`). That is why the signal vector is live.
- `definition/model` **cannot** import `definition/activity` (import cycle) — Task 7's
  rule must use `toWire(n).MessageName`.
- `transport/http/httpcore/errors_test.go` is `package httpcore_test` — qualify
  `httpcore.ClassifyError`.
- Empirically confirmed by an auditor that applied all 16 guards + the validator: the
  full tree builds, `golangci-lint` is clean, and every package passes **except** the
  `TimerFired` case above. `engine/step_fuzz_test.go` cannot generate empty-key triggers.

### Verification commands

⚠ Never `cmd | tail ; echo $?` — that reports `tail`'s status and is always 0.

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-eng.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"

# full gate (Task 9)
go build ./... ; echo "BUILD=$?"
go test -race -coverprofile=cover.out ./... > "$LOG" 2>&1 ; echo "GO_TEST_EXIT=$?"
scripts/coverage.sh cover.out          # >= 85% excluding generated files
golangci-lint run ./... ; echo "LINT=$?"
```

Postgres/MySQL suites need Docker. If it is unavailable, say so — do not report green.

### Deferred (record in Task 9's Progress update, do not fix here)

- The redundant `cancelTimersByTaskID` call in `cancelTokenWaits`.
- The guard covers `""` only; a record minted with `" "` or a `"none"` sentinel would
  reintroduce the wildcard with no test failing.
- Scope-key exemption tests (`removeEventTriggeredSubprocessArmsForScope("")`,
  `tokensInScope("")`) are unpinned — neither helper is modified.

---

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make an empty identity key match no record in the `engine` package, closing a
cross-scope timer-cancellation bug, three arm cross-matching bugs (one of which can
interrupt a live activity), and the consumer-reachable trigger vectors.

**Architecture:** Three layers. (1) Sixteen state-layer lookup/sweep helpers in four
files gain an early `if key == "" { return <zero> }` guard. (2) `Step` gains a
`validateTriggerKey` pre-check rejecting an empty identity key on 11 inbound triggers
with a new `ErrEmptyTriggerKey` sentinel. (3) `model.Validate` rejects an unnamed
`ReceiveTask`, which layer 1 would otherwise strand permanently. Seven documented
"empty means something" keys are exempt and protected by their own tests.

**Tech Stack:** Go 1.25, `stretchr/testify`. No new dependencies.

**Spec:** `docs/specs/2026-07-28-empty-identity-key-matches-nothing.md`
**ADR:** `docs/adr/0152-empty-identity-key-matches-nothing.md` — **already written and
committed** (`7183463`). Do not recreate it.

**This plan was revised after a three-agent adversarial audit.** Findings that changed
the design are marked ⚠ in the tasks below. Do not "simplify" them back.

## Global Constraints

- **TDD is mandatory and audited.** RED → verify red → GREEN → verify green. A `Write`
  of an implementation file with no `go test` failure observed in between is a process
  failure. See CLAUDE.md "TDD Operational Discipline".
- ⚠ **Every empty-key test fixture MUST contain a record that holds the empty value.**
  The audit found five tests that swept with `""` over fixtures whose records all had
  non-empty keys — they passed *before and after* the guard and would keep passing if
  the guard were deleted. A fixture without a planted empty-keyed record proves nothing.
- **Table tests use the `assert` closure form**, never `want`/`wantErr` fields.
  `t.Parallel()` on outer test and subtests; `t.Context()` never `context.Background()`.
  Project skill `table-test` overrides `cc-skills-golang:golang-testing`. **Fold into an
  existing table** when one already covers the same SUT rather than adding a third
  `TestXxx`.
- **Engine core purity:** no new imports in `engine`. `engine/purity_test.go` stays green.
- These helpers are unexported, so tests are white-box in `package engine` (see
  `engine/state_test.go:1`). **Exception:** `transport/http/httpcore` tests are
  `package httpcore_test` — qualify as `httpcore.ClassifyError`.
- ⚠ **Never guard `scopeID`** (`""` = root scope), **`correlationKey`** (`""` =
  uncorrelated), or **`excludeTimerID`** (`""` = exclude nothing — the *second*
  parameter of `cancelTimersByTaskID` / `cancelTimersForToken`, where five of seven
  call sites pass `""`).
- **Do not modify** `engine/target_node.go`, `engine/close_kind.go`, or
  `engine/failing_action.go` — their helpers inherit guards by delegation.
- ⚠ **Exit codes, never a pipeline.** `cmd | tail ; echo $?` reports *`tail`'s* status
  and is always 0. Always:
  ```bash
  LOG="${TMPDIR:-/tmp}/wrkflw-$$.log"
  go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
  ```
- Conventional Commits, scope `engine` / `definition` / `transport`.

## Task Dependency Graph

```
Task 1 (state_timers.go) ──────────────┬─→ Task 8 (Step-level wedge) ─┐
Task 2 (state_arms.go)   ──────────────┤                              │
Task 3 (step_state.go)   ─→ Task 4 (state.go) ────────────────────────┼─→ Task 9
Task 5 (trigger validate) ─→ Task 6 (httpcore 400) ───────────────────┤   (ADR + gate)
Task 7 (ReceiveTask validation) ──────────────────────────────────────┘
```

⚠ **Task 3 → Task 4 is a hard edge** (audit M5): Task 4's inheritance test is resolved
by `tokenAwaitingMessage`, which Task 3 guards. Task 4 cannot go green before Task 3.

⚠ **Dispatch each parallel task in its own git worktree** (audit M6). Tasks 1, 2, 3, 5,
7 touch disjoint *files* but share the `engine` *package*: Task 5's RED state
(`undefined: ErrEmptyTriggerKey`) breaks the package build for Tasks 1–3's verification.
Use `isolation: "worktree"`, or run them serially.

---

### Task 1: Guard the timer helpers (the live bug)

**Files:**
- Modify: `engine/state_timers.go:30-95`
- Create: `engine/state_timers_test.go`
- Modify: `engine/state_test.go` (fold one existing test — see Step 1)

**Interfaces:**
- Consumes: nothing.
- Produces: no signature changes. `timerByID(timerID string) *timerRecord`,
  `removeTimer(timerID string)`,
  `cancelTimersByTaskID(taskID, excludeTimerID string) []string`,
  `cancelTimersForToken(tokenID, excludeTimerID string) []string`.

**Background:** `timerRecord.TaskID` is `""` for non-human-task timers
(`state_timers.go:18`). Only the `TimerRetry` site (`step_triggers.go:300-307`) leaves it
unset, so `cancelTimersByTaskID("")` selects every retry timer in the instance,
cross-scope.

⚠ **Only `cancelTimersByTaskID`'s empty case is genuinely RED here.** `timerByID`,
`removeTimer`, and `cancelTimersForToken` are tier-3 — their guards fix nothing today.
The fixtures below plant empty-keyed records specifically so those tests still fail
without the guard.

- [ ] **Step 1: Write the failing tests**

Create `engine/state_timers_test.go`:

```go
package engine

// state_timers_test.go — white-box tests for the timer lookup/sweep helpers.
// Covers ADR-0152: an empty identity key matches no record.
//
// Every empty-key case plants a record HOLDING the empty value, so the test
// genuinely reproduces the wildcard and fails if the guard is removed.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelTimersByTaskID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timers  []timerRecord
		taskID  string
		exclude string
		assert  func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name: "cancels every timer for the named task",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerDeadline, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm2", Kind: TimerInWait, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm3", Kind: TimerDeadline, Token: "tokB", TaskID: "h2"},
			},
			taskID: "h1",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.ElementsMatch(t, []string{"tm1", "tm2"}, cancelled)
				require.Len(t, s.Timers, 1)
				assert.Equal(t, "tm3", s.Timers[0].TimerID)
			},
		},
		{
			// EXEMPTION: excludeTimerID "" means "exclude nothing".
			name: "empty excludeTimerID excludes nothing",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerDeadline, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm2", Kind: TimerInWait, Token: "tokA", TaskID: "h1"},
			},
			taskID:  "h1",
			exclude: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.ElementsMatch(t, []string{"tm1", "tm2"}, cancelled,
					"an empty excludeTimerID must not suppress any cancellation")
				assert.Empty(t, s.Timers)
			},
		},
		{
			name: "honours a populated excludeTimerID",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerDeadline, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm2", Kind: TimerInWait, Token: "tokA", TaskID: "h1"},
			},
			taskID:  "h1",
			exclude: "tm1",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"tm2"}, cancelled)
				require.Len(t, s.Timers, 1)
				assert.Equal(t, "tm1", s.Timers[0].TimerID)
			},
		},
		{
			// ADR-0152, the live defect. TimerRetry records carry no TaskID, so an
			// empty key swept every retry in the instance — including retries owned
			// by tokens in sibling scopes, wedging them in TokenWaiting forever.
			name: "empty task key cancels nothing",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerRetry, Token: "tokA", NodeID: "svcA", ScopeID: "sc1"},
				{TimerID: "tm2", Kind: TimerRetry, Token: "tokB", NodeID: "svcB", ScopeID: "sc2"},
				{TimerID: "tm3", Kind: TimerDeadline, Token: "tokC", TaskID: "h1"},
			},
			taskID: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled, "an empty task key must cancel no timer")
				assert.Len(t, s.Timers, 3, "every record must survive an empty-key sweep")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: tc.timers}
			cancelled := s.cancelTimersByTaskID(tc.taskID, tc.exclude)
			tc.assert(t, cancelled, s)
		})
	}
}

func TestTimerByID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timerID string
		assert  func(t *testing.T, rec *timerRecord)
	}

	cases := []testCase{
		{
			name:    "returns the matching record",
			timerID: "tm1",
			assert: func(t *testing.T, rec *timerRecord) {
				require.NotNil(t, rec)
				assert.Equal(t, "tm1", rec.TimerID)
			},
		},
		{
			name:    "returns nil for an unknown id",
			timerID: "nope",
			assert: func(t *testing.T, rec *timerRecord) {
				assert.Nil(t, rec)
			},
		},
		{
			// The fixture plants a record WITH an empty TimerID, so this fails
			// without the guard.
			name:    "returns nil for an empty id",
			timerID: "",
			assert: func(t *testing.T, rec *timerRecord) {
				assert.Nil(t, rec, "an empty timer id names no record")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerRetry},
				{TimerID: "", Kind: TimerRetry, Token: "tokGhost"},
			}}
			tc.assert(t, s.timerByID(tc.timerID))
		})
	}
}

func TestRemoveTimer(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timerID string
		assert  func(t *testing.T, s *InstanceState)
	}

	cases := []testCase{
		{
			name:    "removes the matching record",
			timerID: "tm1",
			assert: func(t *testing.T, s *InstanceState) {
				require.Len(t, s.Timers, 2)
				assert.Equal(t, "tm2", s.Timers[0].TimerID)
			},
		},
		{
			// Without the guard this deletes the planted empty-TimerID record.
			name:    "empty id removes nothing",
			timerID: "",
			assert: func(t *testing.T, s *InstanceState) {
				assert.Len(t, s.Timers, 3, "an empty timer id names no record")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerRetry},
				{TimerID: "tm2", Kind: TimerRetry},
				{TimerID: "", Kind: TimerRetry, Token: "tokGhost"},
			}}
			s.removeTimer(tc.timerID)
			tc.assert(t, s)
		})
	}
}
```

⚠ **Fold, do not duplicate** (audit m12): `engine/state_test.go:270` already has
`TestCancelTimersForToken`. Move it into `state_timers_test.go` as a table, delete the
original, and add `t.Parallel()` plus the empty-key row:

```go
func TestCancelTimersForToken(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		tokenID string
		exclude string
		assert  func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name:    "cancels the token's timers except the excluded one",
			tokenID: "tokA",
			exclude: "t2",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"t1"}, cancelled)
				remaining := []string{s.Timers[0].TimerID, s.Timers[1].TimerID, s.Timers[2].TimerID}
				assert.ElementsMatch(t, []string{"t2", "t3", "t4"}, remaining)
			},
		},
		{
			// Without the guard this sweeps the planted empty-Token record t4.
			name:    "empty token key cancels nothing",
			tokenID: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled)
				assert.Len(t, s.Timers, 4, "every record must survive an empty-key sweep")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: []timerRecord{
				{TimerID: "t1", Kind: TimerInWait, Token: "tokA"},
				{TimerID: "t2", Kind: TimerIntermediate, Token: "tokA"},
				{TimerID: "t3", Kind: TimerInWait, Token: "tokB"},
				{TimerID: "t4", Kind: TimerRetry, Token: ""},
			}}
			tc.assert(t, s.cancelTimersForToken(tc.tokenID, tc.exclude), s)
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t1.log"
go test -run 'TestCancelTimersByTaskID|TestCancelTimersForToken|TestTimerByID|TestRemoveTimer' ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -40 "$LOG"
```

Expected `EXIT=1`, with four failing subtests: `TestCancelTimersByTaskID/empty_task_key_cancels_nothing`,
`TestCancelTimersForToken/empty_token_key_cancels_nothing`,
`TestTimerByID/returns_nil_for_an_empty_id`, `TestRemoveTimer/empty_id_removes_nothing`.

- [ ] **Step 3: Add the four guards**

```go
// timerByID returns a pointer to the timerRecord with the given timerID, or nil
// if no such record exists. An empty timerID names no record (ADR-0152).
func (s *InstanceState) timerByID(timerID string) *timerRecord {
	if timerID == "" {
		return nil
	}
	for i := range s.Timers {
		if s.Timers[i].TimerID == timerID {
			return &s.Timers[i]
		}
	}
	return nil
}

// removeTimer removes the timerRecord with the given timerID from the Timers
// slice. It is a no-op if no record with that timerID exists, and an empty
// timerID names no record (ADR-0152).
func (s *InstanceState) removeTimer(timerID string) {
	if timerID == "" {
		return
	}
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TimerID != timerID {
			out = append(out, tr)
		}
	}
	s.Timers = out
}

// cancelTimersByTaskID removes all timer records associated with the given
// taskID (excluding the one already being handled), returning their TimerIDs
// so the caller can emit CancelTimer commands.
//
// An empty taskID cancels NOTHING (ADR-0152). A task id is an identity; the empty
// string names no task. TimerRetry records carry no TaskID, so without this guard
// an empty key matched every retry timer in the instance — including retries owned
// by tokens in sibling scopes that were not being cancelled, leaving those tokens
// parked in TokenWaiting forever with their timer cancelled in the scheduler.
//
// excludeTimerID is deliberately NOT guarded: an empty value means "exclude
// nothing", and five of the seven call sites rely on that.
func (s *InstanceState) cancelTimersByTaskID(taskID, excludeTimerID string) []string {
	if taskID == "" {
		return nil
	}
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.TaskID == taskID && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	s.Timers = out
	return toCancel
}

// cancelTimersForToken removes all timer records whose Token matches the given
// parked-token id (excluding excludeTimerID), returning their TimerIDs so the
// caller can emit CancelTimer commands. It is the token-keyed counterpart of
// cancelTimersByTaskID, used to cancel a parked token's in-wait reminder when
// its wait resolves or its scope is interrupted (ReceiveTask / IntermediateCatchEvent
// have no human-task correlation token).
//
// An empty tokenID names no token (ADR-0152). excludeTimerID is NOT guarded.
func (s *InstanceState) cancelTimersForToken(tokenID, excludeTimerID string) []string {
	if tokenID == "" {
		return nil
	}
	var toCancel []string
	out := make([]timerRecord, 0, len(s.Timers))
	for _, tr := range s.Timers {
		if tr.Token == tokenID && tr.TimerID != excludeTimerID {
			toCancel = append(toCancel, tr.TimerID)
			continue
		}
		out = append(out, tr)
	}
	s.Timers = out
	return toCancel
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Same command as Step 2. Expected `EXIT=0`.

- [ ] **Step 5: Run the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t1-pkg.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
```
Expected `EXIT=0`. A pre-existing failure means some path relied on the wildcard — stop
and report rather than weakening a test.

- [ ] **Step 6: Commit**

```bash
git add engine/state_timers.go engine/state_timers_test.go engine/state_test.go
git commit -m "fix(engine): empty task/token key cancels no timer (ADR-0152)"
```

---

### Task 2: Guard the arm lookups (cross-kind matching)

**Files:**
- Modify: `engine/state_arms.go` — the three generics (`:175-206`),
  `removeArmedEventsForGateway` (`:250`), `removeBoundaryArmsForHost` (`:279`)
- Create: `engine/state_arms_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: no signature changes to
  `armByTimer[T any, PT armMatchable[T]](arms []T, timerID string) *T`,
  `armBySignal[T any, PT armMatchable[T]](arms []T, name string) *T`,
  `armByMessage[T any, PT armMatchable[T]](arms []T, name, correlationKey string) *T`,
  `removeArmedEventsForGateway(gatewayToken string) []string`,
  `removeBoundaryArmsForHost(hostToken string) []string`.

⚠ **Background (audit finding 5 — the premise is stronger than the source comment).**
`state_arms.go:11-12` claims "at most one of the four fields is non-empty". That is
understated: `armBoundaries` (`step_boundaries.go:38-70`) assigns
`TimerID`/`Signal`/`Message` in an `if / else if / else if` chain, so an **error
boundary** is appended at `:70` with **all four fields empty**. An empty signal name
therefore matches an error-boundary arm, and `fireBoundaryArm` interrupts a live host
activity. The fixture below **must** include such an arm.

Guarding the three generics covers all nine wrappers — **do not edit the wrappers**, and
**do not touch** `removeEventTriggeredSubprocessArmsForScope` (scope key, exempt).
`armByMessage` guards **only `name`**; `correlationKey` stays unguarded.

- [ ] **Step 1: Write the failing tests**

Create `engine/state_arms_test.go`:

```go
package engine

// state_arms_test.go — white-box tests for the generic arm lookups. Covers
// ADR-0152: an empty identity key matches no arm, while an empty correlationKey
// keeps its documented "uncorrelated" meaning.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// armsFixture returns one timer arm, one signal arm, one message arm, and — the
// case that makes an empty key dangerous — one ERROR-boundary-shaped arm whose
// four triggerMatch fields are all empty (see step_boundaries.go:38-70).
func armsFixture() []armedEvent {
	return []armedEvent{
		{GatewayToken: "gw1", CatchNode: "catchTimer", triggerMatch: triggerMatch{TimerID: "tm1"}},
		{GatewayToken: "gw1", CatchNode: "catchSignal", triggerMatch: triggerMatch{Signal: "sig"}},
		{GatewayToken: "gw1", CatchNode: "catchMsg", triggerMatch: triggerMatch{Message: "msg", MessageKey: "k1"}},
		{GatewayToken: "gw1", CatchNode: "catchErr"},
	}
}

func TestArmBySignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		signal string
		assert func(t *testing.T, arm *armedEvent)
	}

	cases := []testCase{
		{
			name:   "returns the signal arm",
			signal: "sig",
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm)
				assert.Equal(t, "catchSignal", arm.CatchNode)
			},
		},
		{
			name:   "returns nil for an unknown signal",
			signal: "other",
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm)
			},
		},
		{
			// Before ADR-0152 this returned the TIMER arm, and in production an
			// ERROR-boundary arm — which fireBoundaryArm then uses to interrupt a
			// live host activity.
			name:   "empty signal name matches no arm",
			signal: "",
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm, "an empty signal name must not match a timer, message, or error arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, armBySignal(armsFixture(), tc.signal))
		})
	}
}

func TestArmByTimer(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timerID string
		assert  func(t *testing.T, arm *armedEvent)
	}

	cases := []testCase{
		{
			name:    "returns the timer arm",
			timerID: "tm1",
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm)
				assert.Equal(t, "catchTimer", arm.CatchNode)
			},
		},
		{
			name:    "empty timer id matches no arm",
			timerID: "",
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm, "an empty timer id must not match a signal, message, or error arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, armByTimer(armsFixture(), tc.timerID))
		})
	}
}

func TestArmByMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		message string
		key     string
		arms    []armedEvent
		assert  func(t *testing.T, arm *armedEvent)
	}

	cases := []testCase{
		{
			name:    "returns the message arm on a matching key",
			message: "msg",
			key:     "k1",
			arms:    armsFixture(),
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm)
				assert.Equal(t, "catchMsg", arm.CatchNode)
			},
		},
		{
			name:    "returns nil when the key does not match",
			message: "msg",
			key:     "other",
			arms:    armsFixture(),
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm)
			},
		},
		{
			// EXEMPTION: an empty correlationKey means "uncorrelated" and must keep
			// matching an arm that is itself uncorrelated.
			name:    "empty correlation key still matches an uncorrelated arm",
			message: "msg",
			key:     "",
			arms: []armedEvent{
				{GatewayToken: "gw1", CatchNode: "catchMsg", triggerMatch: triggerMatch{Message: "msg"}},
			},
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm, "an uncorrelated message must still match an uncorrelated arm")
				assert.Equal(t, "catchMsg", arm.CatchNode)
			},
		},
		{
			name:    "empty message name matches no arm",
			message: "",
			key:     "",
			arms:    armsFixture(),
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm, "an empty message name must not match a timer or error arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, armByMessage(tc.arms, tc.message, tc.key))
		})
	}
}

func TestRemoveArmedEventsForGateway(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		owner  string
		assert func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name:  "removes the named gateway's arms",
			owner: "gw1",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"tm1"}, cancelled)
				require.Len(t, s.ArmedEvents, 1, "the orphan arm must remain")
				assert.Empty(t, s.ArmedEvents[0].GatewayToken)
			},
		},
		{
			// Without the guard this removes the planted empty-GatewayToken arm.
			name:  "empty gateway token removes nothing",
			owner: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled)
				assert.Len(t, s.ArmedEvents, 2, "an empty gateway token names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{ArmedEvents: []armedEvent{
				{GatewayToken: "gw1", CatchNode: "catchTimer", triggerMatch: triggerMatch{TimerID: "tm1"}},
				{GatewayToken: "", CatchNode: "orphan", triggerMatch: triggerMatch{TimerID: "tm2"}},
			}}
			tc.assert(t, s.removeArmedEventsForGateway(tc.owner), s)
		})
	}
}

func TestRemoveBoundaryArmsForHost(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		host   string
		assert func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name: "removes the named host's arms",
			host: "tokA",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"tm9"}, cancelled)
				assert.Len(t, s.Boundaries, 1)
			},
		},
		{
			name: "empty host token removes nothing",
			host: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled)
				assert.Len(t, s.Boundaries, 2, "an empty host token names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Boundaries: []boundaryArm{
				{HostToken: "tokA", triggerMatch: triggerMatch{TimerID: "tm9"}},
				{HostToken: "", triggerMatch: triggerMatch{TimerID: "tm10"}},
			}}
			tc.assert(t, s.removeBoundaryArmsForHost(tc.host), s)
		})
	}
}
```

**Note:** if `boundaryArm`'s field names differ, read `engine/state_arms.go` and adjust
the literal — never the assertions. `triggerMatch` is embedded anonymously and this
literal form is already used at `engine/export_test.go:63`.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t2.log"
go test -run 'TestArmBySignal|TestArmByTimer|TestArmByMessage|TestRemoveArmedEventsForGateway|TestRemoveBoundaryArmsForHost' ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -40 "$LOG"
```
Expected `EXIT=1` — five failing subtests (three arm cross-matches, two orphan removals).

- [ ] **Step 3: Add the guards**

```go
// armByTimer returns a pointer to the first arm whose embedded TimerID equals
// timerID, or nil. An empty timerID matches no arm (ADR-0152): arms of other
// kinds — and error-boundary arms, which carry NO non-empty match field at all
// (step_boundaries.go:38-70) — would otherwise all match.
func armByTimer[T any, PT armMatchable[T]](arms []T, timerID string) *T {
	if timerID == "" {
		return nil
	}
	for i := range arms {
		if PT(&arms[i]).matchPtr().TimerID == timerID {
			return &arms[i]
		}
	}
	return nil
}

// armBySignal returns a pointer to the first arm whose embedded signal name
// equals name, or nil. An empty name matches no arm (ADR-0152) — see armByTimer.
// See armByTimer for the pointer-aliasing contract.
func armBySignal[T any, PT armMatchable[T]](arms []T, name string) *T {
	if name == "" {
		return nil
	}
	for i := range arms {
		if PT(&arms[i]).matchPtr().Signal == name {
			return &arms[i]
		}
	}
	return nil
}

// armByMessage returns a pointer to the first arm whose embedded Message equals
// name and MessageKey equals correlationKey, or nil. An empty name matches no arm
// (ADR-0152). correlationKey is deliberately NOT guarded: an empty key means
// "uncorrelated" and must keep matching an arm whose MessageKey is also empty.
// See armByTimer for the pointer-aliasing contract.
func armByMessage[T any, PT armMatchable[T]](arms []T, name, correlationKey string) *T {
	if name == "" {
		return nil
	}
	for i := range arms {
		m := PT(&arms[i]).matchPtr()
		if m.Message == name && m.MessageKey == correlationKey {
			return &arms[i]
		}
	}
	return nil
}
```

```go
func (s *InstanceState) removeArmedEventsForGateway(gatewayToken string) []string {
	// An empty gateway token names no arm (ADR-0152).
	if gatewayToken == "" {
		return nil
	}
	kept, cancelTimerIDs := removeArmsWhere(s.ArmedEvents, func(ae *armedEvent) bool {
		return ae.GatewayToken == gatewayToken
	})
	s.ArmedEvents = kept
	return cancelTimerIDs
}

func (s *InstanceState) removeBoundaryArmsForHost(hostToken string) []string {
	// An empty host token names no arm (ADR-0152).
	if hostToken == "" {
		return nil
	}
	kept, cancelTimerIDs := removeArmsWhere(s.Boundaries, func(ba *boundaryArm) bool {
		return ba.HostToken == hostToken
	})
	s.Boundaries = kept
	return cancelTimerIDs
}
```

- [ ] **Step 4: Verify they pass** — same command as Step 2, expect `EXIT=0`.

- [ ] **Step 5: Run the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t2-pkg.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
```
Expected `EXIT=0`. Watch `state_arms_wire_test.go` and the boundary / event-gateway /
event-sub-process suites.

- [ ] **Step 6: Commit**

```bash
git add engine/state_arms.go engine/state_arms_test.go
git commit -m "fix(engine): empty arm key matches no arm across kinds (ADR-0152)"
```

---

### Task 3: Guard the token helpers

**Files:**
- Modify: `engine/step_state.go` — `tokenAwaiting` (`:72`), `tokenByID` (`:82`),
  `tokenIDsAwaitingSignal` (`:96`), `tokenAwaitingMessage` (`:110`),
  `removeToken` (`:192`), `openVisitFor` (`:144`)
- Create: `engine/step_state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: no signature changes. **Task 4 depends on `tokenAwaitingMessage`'s guard.**

**Background:** a `Token` parks on exactly one of `AwaitCommand`/`AwaitSignal`/
`AwaitMessage` (`state.go:89-101`); the others stay `""`. So `tokenAwaiting("")` returns
any token not parked on a command, and `tokenIDsAwaitingSignal("")` returns every token
not awaiting a signal.

⚠ `openVisitFor` takes a `(tokenID, nodeID)` **pair** — guard both. Its callers
`setVisitTask` (`:136`), `closeVisit` (`:206`), and `closeVisitAs`
(`close_kind.go:54`) inherit; **do not** guard them, and never guard `setVisitTask`'s
third argument `taskID`, which is the value written, not a key.

- [ ] **Step 1: Write the failing tests**

Create `engine/step_state_test.go`:

```go
package engine

// step_state_test.go — white-box tests for the token and visit lookup helpers.
// Covers ADR-0152. Each empty-key case plants a record holding the empty value.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parkedTokens returns one command-parked token, one signal-parked token, one
// message-parked token, and one plain active token whose Await* fields are empty.
func parkedTokens() []Token {
	return []Token{
		{ID: "tokCmd", State: TokenWaiting, NodeID: "nCmd", AwaitCommand: "c1"},
		{ID: "tokSig", State: TokenWaiting, NodeID: "nSig", AwaitSignal: "sig"},
		{ID: "tokMsg", State: TokenWaiting, NodeID: "nMsg", AwaitMessage: "msg", AwaitMessageKey: "k1"},
		{ID: "tokActive", State: TokenActive, NodeID: "nActive", ScopeID: "sc1"},
	}
}

func TestTokenAwaiting(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		cmdID  string
		assert func(t *testing.T, tok *Token)
	}

	cases := []testCase{
		{
			name:  "returns the token parked on the command",
			cmdID: "c1",
			assert: func(t *testing.T, tok *Token) {
				require.NotNil(t, tok)
				assert.Equal(t, "tokCmd", tok.ID)
			},
		},
		{
			// Before ADR-0152 this returned tokActive, whose AwaitCommand is "".
			name:  "empty command id matches no token",
			cmdID: "",
			assert: func(t *testing.T, tok *Token) {
				assert.Nil(t, tok, "an empty command id must not match an unparked token")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: parkedTokens()}
			tc.assert(t, s.tokenAwaiting(tc.cmdID))
		})
	}
}

func TestTokenIDsAwaitingSignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		signal string
		assert func(t *testing.T, ids []string)
	}

	cases := []testCase{
		{
			name:   "returns tokens awaiting the signal",
			signal: "sig",
			assert: func(t *testing.T, ids []string) {
				assert.Equal(t, []string{"tokSig"}, ids)
			},
		},
		{
			// Before ADR-0152 this returned every token NOT awaiting a signal —
			// a SignalReceived{Name: ""} resumed them all.
			name:   "empty signal name matches no token",
			signal: "",
			assert: func(t *testing.T, ids []string) {
				assert.Empty(t, ids, "an empty signal name must not broadcast to unparked tokens")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: parkedTokens()}
			tc.assert(t, s.tokenIDsAwaitingSignal(tc.signal))
		})
	}
}

func TestTokenAwaitingMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		message string
		key     string
		tokens  []Token
		assert  func(t *testing.T, tok *Token)
	}

	cases := []testCase{
		{
			name:    "returns the token on a matching name and key",
			message: "msg",
			key:     "k1",
			tokens:  parkedTokens(),
			assert: func(t *testing.T, tok *Token) {
				require.NotNil(t, tok)
				assert.Equal(t, "tokMsg", tok.ID)
			},
		},
		{
			// EXEMPTION: an empty correlationKey means "uncorrelated".
			name:    "empty correlation key still matches an uncorrelated token",
			message: "msg",
			key:     "",
			tokens: []Token{
				{ID: "tokPlain", State: TokenWaiting, AwaitMessage: "msg"},
			},
			assert: func(t *testing.T, tok *Token) {
				require.NotNil(t, tok, "an uncorrelated message must still match an uncorrelated token")
				assert.Equal(t, "tokPlain", tok.ID)
			},
		},
		{
			name:    "empty message name matches no token",
			message: "",
			key:     "",
			tokens:  parkedTokens(),
			assert: func(t *testing.T, tok *Token) {
				assert.Nil(t, tok, "an empty message name must not match an unparked token")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: tc.tokens}
			tc.assert(t, s.tokenAwaitingMessage(tc.message, tc.key))
		})
	}
}

func TestTokenByIDAndRemoveToken(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		tokenID string
		tokens  []Token
		assert  func(t *testing.T, tok *Token, afterRemove int)
	}

	// ghostTokens plants a token WITH an empty ID so the empty-key case is real.
	ghostTokens := func() []Token {
		return append(parkedTokens(), Token{ID: "", State: TokenActive, NodeID: "nGhost"})
	}

	cases := []testCase{
		{
			name:    "finds and removes the named token",
			tokenID: "tokCmd",
			tokens:  ghostTokens(),
			assert: func(t *testing.T, tok *Token, afterRemove int) {
				require.NotNil(t, tok)
				assert.Equal(t, "tokCmd", tok.ID)
				assert.Equal(t, 4, afterRemove)
			},
		},
		{
			name:    "empty token id finds and removes nothing",
			tokenID: "",
			tokens:  ghostTokens(),
			assert: func(t *testing.T, tok *Token, afterRemove int) {
				assert.Nil(t, tok, "an empty token id names no token")
				assert.Equal(t, 5, afterRemove, "an empty token id must remove nothing")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: tc.tokens}
			tok := s.tokenByID(tc.tokenID)
			s.removeToken(tc.tokenID)
			tc.assert(t, tok, len(s.Tokens))
		})
	}
}

func TestOpenVisitFor(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		tokenID string
		nodeID  string
		assert  func(t *testing.T, v *NodeVisit)
	}

	cases := []testCase{
		{
			name:    "returns the open visit for the pair",
			tokenID: "tokA",
			nodeID:  "n1",
			assert: func(t *testing.T, v *NodeVisit) {
				require.NotNil(t, v)
				assert.Equal(t, "n1", v.NodeID)
			},
		},
		{
			name:    "empty token id matches no visit",
			tokenID: "",
			nodeID:  "n1",
			assert: func(t *testing.T, v *NodeVisit) {
				assert.Nil(t, v)
			},
		},
		{
			name:    "empty node id matches no visit",
			tokenID: "tokA",
			nodeID:  "",
			assert: func(t *testing.T, v *NodeVisit) {
				assert.Nil(t, v)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Plant visits holding the empty value on each component.
			s := &InstanceState{History: []NodeVisit{
				{TokenID: "tokA", NodeID: "n1", EnteredAt: time.Unix(0, 0).UTC()},
				{TokenID: "", NodeID: "n1", EnteredAt: time.Unix(0, 0).UTC()},
				{TokenID: "tokA", NodeID: "", EnteredAt: time.Unix(0, 0).UTC()},
			}}
			tc.assert(t, s.openVisitFor(tc.tokenID, tc.nodeID))
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t3.log"
go test -run 'TestTokenAwaiting|TestTokenIDsAwaitingSignal|TestTokenAwaitingMessage|TestTokenByIDAndRemoveToken|TestOpenVisitFor' ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -40 "$LOG"
```
Expected `EXIT=1` — six failing subtests.

- [ ] **Step 3: Add the guards**

```go
func (s *InstanceState) tokenAwaiting(cmdID string) *Token {
	// An empty command id names no token (ADR-0152). A token parks on exactly one
	// of AwaitCommand/AwaitSignal/AwaitMessage, so an empty key would otherwise
	// match any token not parked on a command.
	if cmdID == "" {
		return nil
	}
	for i := range s.Tokens {
		if s.Tokens[i].AwaitCommand == cmdID {
			return &s.Tokens[i]
		}
	}
	return nil
}

func (s *InstanceState) tokenByID(tokenID string) *Token {
	// An empty token id names no token (ADR-0152).
	if tokenID == "" {
		return nil
	}
	for i := range s.Tokens {
		if s.Tokens[i].ID == tokenID {
			return &s.Tokens[i]
		}
	}
	return nil
}

func (s *InstanceState) tokenIDsAwaitingSignal(name string) []string {
	// An empty signal name names no signal (ADR-0152): it would otherwise select
	// every token NOT awaiting a signal and broadcast-resume them all.
	if name == "" {
		return nil
	}
	var ids []string
	for i := range s.Tokens {
		if s.Tokens[i].AwaitSignal == name {
			ids = append(ids, s.Tokens[i].ID)
		}
	}
	return ids
}

func (s *InstanceState) tokenAwaitingMessage(name, correlationKey string) *Token {
	// An empty message name names no message (ADR-0152). correlationKey is
	// deliberately NOT guarded: an empty key means "uncorrelated" and must keep
	// matching a token whose AwaitMessageKey is also empty.
	if name == "" {
		return nil
	}
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.AwaitMessage == name && t.AwaitMessageKey == correlationKey {
			return t
		}
	}
	return nil
}

func (s *InstanceState) removeToken(id string) {
	// An empty token id names no token (ADR-0152).
	if id == "" {
		return
	}
	out := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.Tokens = out
}

func (s *InstanceState) openVisitFor(tokenID, nodeID string) *NodeVisit {
	// Both components are identities; an empty either side names no visit (ADR-0152).
	if tokenID == "" || nodeID == "" {
		return nil
	}
	for i := len(s.History) - 1; i >= 0; i-- {
		v := &s.History[i]
		if v.TokenID == tokenID && v.NodeID == nodeID && v.LeftAt == nil {
			return v
		}
	}
	return nil
}
```

- [ ] **Step 4: Verify they pass** — same command, expect `EXIT=0`.

- [ ] **Step 5: Run the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t3-pkg.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
```
Expected `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add engine/step_state.go engine/step_state_test.go
git commit -m "fix(engine): empty token/signal/message key matches no token (ADR-0152)"
```

---

### Task 4: Guard TaskByID and pin the inherited guards

⚠ **Depends on Task 3.** The inheritance test is resolved by `tokenAwaitingMessage`.

**Files:**
- Modify: `engine/state.go:278-285`
- Modify: `engine/state_test.go` (append)
- Modify: `engine/target_node_test.go` (append)

**Interfaces:**
- Consumes: `tokenAwaitingMessage`'s guard from **Task 3**.
- Produces: no signature change to `TaskByID(taskID string) *humantask.HumanTask`.

⚠ **Background (audit m10):** `messageTargetNodeScoped("", "")` today resolves
**`tokActive`** — the first token whose `AwaitMessage` *and* `AwaitMessageKey` are both
empty — not the message-parked token. `scopeOfToken("")` already returns `""` unguarded
(no token ever has an empty `ID`), so asserting on it proves nothing; the test below
pins `messageTargetNodeScoped` only.

- [ ] **Step 1: Write the failing tests**

Append to `engine/state_test.go` (add `"github.com/kartaladev/wrkflw/humantask"` to its
imports if absent — check `engine/state.go`'s import block for the exact path):

```go
// TestTaskByID covers ADR-0152: a task id is an identity, so an empty one names
// no task. The fixture plants a task with an empty TaskID so the empty-key case
// fails without the guard.
func TestTaskByID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		taskID string
		assert func(t *testing.T, task *humantask.HumanTask)
	}

	cases := []testCase{
		{
			name:   "returns the named task",
			taskID: "h1",
			assert: func(t *testing.T, task *humantask.HumanTask) {
				require.NotNil(t, task)
				assert.Equal(t, "h1", task.TaskID)
			},
		},
		{
			name:   "returns nil for an unknown id",
			taskID: "nope",
			assert: func(t *testing.T, task *humantask.HumanTask) {
				assert.Nil(t, task)
			},
		},
		{
			name:   "empty task id names no task",
			taskID: "",
			assert: func(t *testing.T, task *humantask.HumanTask) {
				assert.Nil(t, task)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tasks: []humantask.HumanTask{
				{TaskID: "h1", NodeID: "n1"},
				{TaskID: "", NodeID: "nGhost"},
			}}
			tc.assert(t, s.TaskByID(tc.taskID))
		})
	}
}
```

Append to `engine/target_node_test.go`:

```go
// TestMessageTargetNodeScopedEmptyName pins ADR-0152's delegation contract:
// messageTargetNodeScoped carries NO guard of its own and inherits one from the
// message lookups in step_state.go. Before the fix it resolved tokActive — the
// first token whose AwaitMessage AND AwaitMessageKey are both empty.
func TestMessageTargetNodeScopedEmptyName(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		Tokens: []Token{
			{ID: "tokActive", State: TokenActive, NodeID: "nActive", ScopeID: "sc1"},
			{ID: "tokMsg", State: TokenWaiting, NodeID: "nMsg", ScopeID: "sc2", AwaitMessage: "msg"},
		},
	}

	nodeID, scopeID, ok := s.messageTargetNodeScoped("", "")

	assert.False(t, ok, "an empty message name resolves no target")
	assert.Empty(t, nodeID)
	assert.Empty(t, scopeID)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t4.log"
go test -run 'TestTaskByID|TestMessageTargetNodeScopedEmptyName' ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -30 "$LOG"
```
Expected `EXIT=1`: `TestTaskByID/empty_task_id_names_no_task` (the planted empty-TaskID
task is returned) and `TestMessageTargetNodeScopedEmptyName` (`ok` true,
`nodeID` "nActive", `scopeID` "sc1").

- [ ] **Step 3: Add the guard**

```go
// TaskByID returns a pointer to the HumanTask with the given task id, or nil if
// no such task exists. An empty taskID names no task (ADR-0152).
func (s *InstanceState) TaskByID(taskID string) *humantask.HumanTask {
	if taskID == "" {
		return nil
	}
	for i := range s.Tasks {
		if s.Tasks[i].TaskID == taskID {
			return &s.Tasks[i]
		}
	}
	return nil
}
```

Keep the existing doc comment's first line if it differs; only append the ADR sentence.
**Do not** add guards to `engine/target_node.go`.

- [ ] **Step 4: Verify they pass** — same command, expect `EXIT=0`.

- [ ] **Step 5: Run the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t4-pkg.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
```
Expected `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add engine/state.go engine/state_test.go engine/target_node_test.go
git commit -m "fix(engine): empty task id names no task; pin inherited guards (ADR-0152)"
```

---

### Task 5: Reject empty identity keys at the trigger boundary

**Files:**
- Modify: `engine/errors.go` (add sentinel inside the existing `var (...)` block)
- Create: `engine/trigger_validate.go`
- Create: `engine/trigger_validate_test.go`
- Modify: `engine/step.go:77-79`

**Interfaces:**
- Consumes: nothing.
- Produces: `engine.ErrEmptyTriggerKey` (exported, consumed by Task 6);
  `validateTriggerKey(trg Trigger) error` (unexported);
  `validatedTriggerKinds` / `exemptTriggerKinds` (unexported, for the exhaustiveness test).

⚠ **`TimerFired.TimerID` is NOT validated** (audit finding 1).
`TestTimerFiredStaleTokenIsNoop` (`engine/step_timers_test.go:113-157`) pins an explicit
empty-`TimerID` case asserting `require.NoError`, documented at `:109-112` as deliberate:
timers are racy and a stale `TimerFired` must never error. The helper guards already make
an empty `TimerID` a clean no-op. **Adding `TimerFired` here breaks that test — do not.**

Also excluded: `StartInstance.StartNodeID` (manual start), `CompensateRequested.ToNode`/
`.ReverseNode` (full rollback), `MessageReceived.CorrelationKey` (uncorrelated — validate
`Name` only), `CancelRequested` (no key).

- [ ] **Step 1: Write the failing test**

Create `engine/trigger_validate_test.go`:

```go
package engine

// trigger_validate_test.go — ADR-0152: Step rejects a trigger whose identity key
// is empty, rather than letting it reach the state lookups.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

func TestValidateTriggerKey(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	type testCase struct {
		name    string
		trigger Trigger
		assert  func(t *testing.T, err error)
	}

	rejects := func(t *testing.T, err error) {
		require.ErrorIs(t, err, ErrEmptyTriggerKey)
	}
	accepts := func(t *testing.T, err error) {
		require.NoError(t, err)
	}

	cases := []testCase{
		{name: "empty ActionCompleted CommandID", trigger: NewActionCompleted(at, "", nil), assert: rejects},
		{name: "empty ActionFailed CommandID", trigger: NewActionFailed(at, "", "boom", true), assert: rejects},
		{name: "empty SubInstanceCompleted CommandID", trigger: NewSubInstanceCompleted(at, "", nil), assert: rejects},
		{name: "empty SubInstanceFailed CommandID", trigger: NewSubInstanceFailed(at, "", "boom"), assert: rejects},
		{name: "empty HumanCompleted TaskID", trigger: NewHumanCompleted(at, "", CompletionInput{}, authz.Actor{ID: "a"}), assert: rejects},
		{name: "empty HumanClaimed TaskID", trigger: NewHumanClaimed(at, "", authz.Actor{ID: "a"}), assert: rejects},
		{name: "empty HumanReassigned TaskID", trigger: NewHumanReassigned(at, "", "a", "b", authz.Actor{ID: "c"}), assert: rejects},
		{name: "empty HumanCandidatesResolved TaskID", trigger: NewHumanCandidatesResolved(at, "", nil), assert: rejects},
		{name: "empty SignalReceived Name", trigger: NewSignalReceived(at, "", nil), assert: rejects},
		{name: "empty MessageReceived Name", trigger: NewMessageReceived(at, "", "k", nil), assert: rejects},
		{name: "empty ResolveIncident IncidentID", trigger: NewResolveIncident(at, "", 0), assert: rejects},

		{name: "populated SignalReceived is accepted", trigger: NewSignalReceived(at, "sig", nil), assert: accepts},
		{name: "populated HumanClaimed is accepted", trigger: NewHumanClaimed(at, "h1", authz.Actor{ID: "a"}), assert: accepts},

		// EXEMPTIONS — empty here is documented, meaningful, and must be accepted.
		// TimerFired is exempt because TestTimerFiredStaleTokenIsNoop pins an
		// empty TimerID as a clean no-op (ADR-0152).
		{name: "TimerFired with empty TimerID stays a clean no-op", trigger: NewTimerFired(at, ""), assert: accepts},
		{name: "StartInstance with empty StartNodeID resolves the manual start", trigger: NewStartInstance(at, nil), assert: accepts},
		{name: "CompensateRequested with empty ToNode means full rollback", trigger: NewCompensateRequested(at, ""), assert: accepts},
		{name: "CancelRequested carries no key", trigger: NewCancelRequested(at), assert: accepts},
		{name: "MessageReceived with empty CorrelationKey is uncorrelated", trigger: NewMessageReceived(at, "msg", "", nil), assert: accepts},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, validateTriggerKey(tc.trigger))
		})
	}
}

// TestValidateTriggerKindsAreExhaustive fails when a Trigger variant is added and
// classified neither as validated nor exempt, so a new variant cannot silently
// fall through validateTriggerKey's default arm. Modelled on AllTriggerKinds in
// internal/persistence/store/trigger_codec_test.go.
func TestValidateTriggerKindsAreExhaustive(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	// Every variant of the sealed Trigger interface. Adding a variant without
	// adding it here fails the codec's own exhaustiveness test too.
	all := []Trigger{
		NewStartInstance(at, nil),
		NewActionCompleted(at, "c", nil),
		NewActionFailed(at, "c", "e", false),
		NewHumanCompleted(at, "h", CompletionInput{}, authz.Actor{}),
		NewHumanClaimed(at, "h", authz.Actor{}),
		NewHumanCandidatesResolved(at, "h", nil),
		NewHumanReassigned(at, "h", "a", "b", authz.Actor{}),
		NewTimerFired(at, "tm"),
		NewSignalReceived(at, "s", nil),
		NewMessageReceived(at, "m", "", nil),
		NewSubInstanceCompleted(at, "c", nil),
		NewSubInstanceFailed(at, "c", "e"),
		NewCompensateRequested(at, ""),
		NewCancelRequested(at),
		NewResolveIncident(at, "i", 0),
	}

	for _, trg := range all {
		name := triggerTypeName(trg)
		_, validated := validatedTriggerKinds[name]
		_, exempt := exemptTriggerKinds[name]
		assert.True(t, validated != exempt,
			"trigger %s must be classified exactly once: validated=%v exempt=%v", name, validated, exempt)
	}
	assert.Len(t, all, len(validatedTriggerKinds)+len(exemptTriggerKinds),
		"every Trigger variant must appear in exactly one classification set")
}

// TestStepRejectsEmptyTriggerKey proves the validator is wired into Step and that
// a rejected trigger drives nothing.
func TestStepRejectsEmptyTriggerKey(t *testing.T) {
	t.Parallel()

	// A token parked on a signal WOULD be resumed by an empty-name broadcast
	// before ADR-0152. Assert it did not move.
	before := InstanceState{
		Status: StatusRunning,
		Tokens: []Token{{ID: "tokActive", State: TokenActive, NodeID: "n1"}},
	}

	res, err := Step(t.Context(), nil, before, NewSignalReceived(time.Unix(0, 0).UTC(), "", nil), StepOptions{})

	require.ErrorIs(t, err, ErrEmptyTriggerKey)
	assert.Equal(t, InstanceState{}, res.State,
		"a rejected trigger must return the zero StepResult, not a partially driven state")
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t5.log"
go test -run 'TestValidateTriggerKey|TestValidateTriggerKindsAreExhaustive|TestStepRejectsEmptyTriggerKey' ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -30 "$LOG"
```
Expected `EXIT=1` — build failure: `undefined: ErrEmptyTriggerKey`,
`undefined: validateTriggerKey`, `undefined: validatedTriggerKinds`,
`undefined: exemptTriggerKinds`, `undefined: triggerTypeName`. A compile error is a valid
red state.

- [ ] **Step 3: Add the sentinel**

In `engine/errors.go`, inside the existing `var (...)` block, after `ErrOutcomeRequired`:

```go
	// ErrEmptyTriggerKey is returned when an inbound trigger's identity key is
	// empty. An identity key names one specific record; the empty string names
	// none, so the trigger cannot be dispatched.
	//
	// It is deliberately NOT wrapped in ErrInvalidTransition: the instance state is
	// irrelevant here, the trigger itself is malformed. Transports classify it 400,
	// alongside the other caller-correctable input sentinels. See ADR-0152.
	ErrEmptyTriggerKey = errors.New("workflow-engine: trigger identity key is empty")
```

- [ ] **Step 4: Add the validator**

Create `engine/trigger_validate.go`:

```go
package engine

import "fmt"

// validatedTriggerKinds maps a trigger's type name to the identity field
// validateTriggerKey requires to be non-empty.
//
// exemptTriggerKinds lists the variants deliberately NOT validated, each with the
// reason. Together the two sets must cover every variant of the sealed Trigger
// interface; TestValidateTriggerKindsAreExhaustive enforces that, so a variant
// added later cannot silently fall through validateTriggerKey's default arm.
var (
	validatedTriggerKinds = map[string]string{
		"engine.ActionCompleted":         "CommandID",
		"engine.ActionFailed":            "CommandID",
		"engine.SubInstanceCompleted":    "CommandID",
		"engine.SubInstanceFailed":       "CommandID",
		"engine.HumanCompleted":          "TaskID",
		"engine.HumanClaimed":            "TaskID",
		"engine.HumanReassigned":         "TaskID",
		"engine.HumanCandidatesResolved": "TaskID",
		"engine.SignalReceived":          "Name",
		"engine.MessageReceived":         "Name",
		"engine.ResolveIncident":         "IncidentID",
	}

	exemptTriggerKinds = map[string]string{
		// A stale TimerFired must stay a clean no-op: timers are inherently racy
		// with other completion paths and can arrive late. Pinned by
		// TestTimerFiredStaleTokenIsNoop. The state-layer guards already give an
		// empty TimerID exactly that behaviour.
		"engine.TimerFired": "an empty TimerID is a documented stale-timer no-op",
		// An empty StartNodeID resolves the definition's manual start.
		"engine.StartInstance": "an empty StartNodeID selects the manual start",
		// An empty ToNode means full rollback; an empty ReverseNode means terminate.
		"engine.CompensateRequested": "empty ToNode/ReverseNode mean full rollback",
		// Carries no identity key at all.
		"engine.CancelRequested": "carries no identity key",
	}
)

// triggerTypeName returns the trigger's concrete type name (e.g.
// "engine.SignalReceived"), the key used by the classification sets above.
func triggerTypeName(trg Trigger) string { return fmt.Sprintf("%T", trg) }

// validateTriggerKey rejects a trigger whose identity key is empty.
//
// An identity key names one specific record; the empty string names none. Before
// ADR-0152 an empty key reached the state-layer lookups, where it matched every
// record whose corresponding field was also empty — a SignalReceived with no name
// resumed every token not awaiting a signal, and an empty name matched an
// error-boundary arm, interrupting a live activity. The state helpers now refuse an
// empty key on their own; this is the outer layer, so a consumer that builds a
// malformed trigger gets a named error instead of a silent no-op.
//
// MessageReceived validates Name only — an empty CorrelationKey means "uncorrelated".
func validateTriggerKey(trg Trigger) error {
	field, ok := validatedTriggerKinds[triggerTypeName(trg)]
	if !ok {
		return nil
	}
	var key string
	switch t := trg.(type) {
	case ActionCompleted:
		key = t.CommandID
	case ActionFailed:
		key = t.CommandID
	case SubInstanceCompleted:
		key = t.CommandID
	case SubInstanceFailed:
		key = t.CommandID
	case HumanCompleted:
		key = t.TaskID
	case HumanClaimed:
		key = t.TaskID
	case HumanReassigned:
		key = t.TaskID
	case HumanCandidatesResolved:
		key = t.TaskID
	case SignalReceived:
		key = t.Name
	case MessageReceived:
		key = t.Name
	case ResolveIncident:
		key = t.IncidentID
	}
	if key == "" {
		return fmt.Errorf("%w: %T.%s", ErrEmptyTriggerKey, trg, field)
	}
	return nil
}
```

- [ ] **Step 5: Wire it into Step**

In `engine/step.go`, make validation the first statement of `Step`, before `cloneState`:

```go
func Step(ctx context.Context, def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
	// Reject a malformed trigger before any work: an empty identity key names no
	// record, so there is nothing to dispatch it to (ADR-0152). Running before
	// cloneState keeps a rejected trigger free of side effects.
	if err := validateTriggerKey(trg); err != nil {
		return StepResult{}, err
	}
	s := cloneState(st)
	// ... rest unchanged ...
}
```

- [ ] **Step 6: Verify they pass** — same command as Step 2, expect `EXIT=0`.

- [ ] **Step 7: Run the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t5-pkg.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -30 "$LOG"
```
Expected `EXIT=0`. ⚠ **`TestTimerFiredStaleTokenIsNoop` must pass UNMODIFIED.** If it
fails, `TimerFired` was wrongly added to `validatedTriggerKinds` — remove it. Never edit
that test to accommodate the validator.

- [ ] **Step 8: Commit**

```bash
git add engine/errors.go engine/trigger_validate.go engine/trigger_validate_test.go engine/step.go
git commit -m "feat(engine): reject triggers with an empty identity key (ADR-0152)"
```

---

### Task 6: Classify ErrEmptyTriggerKey as HTTP 400

⚠ **Depends on Task 5.**

**Files:**
- Modify: `transport/http/httpcore/errors.go:36-42`
- Modify: `transport/http/httpcore/errors_test.go`

**Interfaces:**
- Consumes: `engine.ErrEmptyTriggerKey` from Task 5.

⚠ `errors_test.go` is **`package httpcore_test`** — call `httpcore.ClassifyError`,
not `ClassifyError` (audit C1). It already has `TestClassifyErrorOutcomeSentinels`
(`:80`), a proper `assert`-closure table — **add rows to it** rather than writing a third
`TestXxx` (audit m12).

**Why this matters:** the shipped HTTP signal route already rejects an empty name
upstream (`SignalInput.Signal` carries `validate:"required"`, `dto.go:22`), so this arm
mainly serves the **human-task and incident routes**, plus defence in depth for consumers
mounting the handlers directly.

- [ ] **Step 1: Write the failing test**

Add two rows to the existing `TestClassifyErrorOutcomeSentinels` table in
`transport/http/httpcore/errors_test.go`. Match the surrounding rows' field names — read
`:80-120` first. The rows to add:

```go
		{
			// ADR-0152: a malformed trigger is a caller-correctable input error,
			// not a server fault.
			name: "empty trigger key is a bad request",
			err:  fmt.Errorf("apply trigger: %w", engine.ErrEmptyTriggerKey),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.NotEmpty(t, body.Message, "a 4xx body must carry an actionable message")
			},
		},
		{
			name: "wrapped empty trigger key is still a bad request",
			err:  fmt.Errorf("service: %w", fmt.Errorf("engine: %w", engine.ErrEmptyTriggerKey)),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
			},
		},
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t6.log"
go test -run TestClassifyErrorOutcomeSentinels ./transport/http/httpcore/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -30 "$LOG"
```
Expected `EXIT=1` — got `500` / `internal_error` with an empty `Message`.

- [ ] **Step 3: Add the classification arm**

```go
	case errors.Is(err, kernel.ErrBadCursor), errors.Is(err, ErrBadInput), errors.Is(err, validation.ErrInvalidInput),
		// Both outcome sentinels describe a completion payload the caller can
		// correct — an outcome outside the node's declared set, or none supplied
		// where the node declares one (ADR-0146). Without these arms they fall to
		// the 500 default, which hides an actionable 4xx behind an empty body.
		errors.Is(err, engine.ErrInvalidOutcome), errors.Is(err, engine.ErrOutcomeRequired),
		// An empty trigger identity key is a malformed request the caller can fix
		// by supplying the id (ADR-0152), not a server fault. This changes the
		// human-task routes from 404/422 and the incident route from 200.
		errors.Is(err, engine.ErrEmptyTriggerKey):
		return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
```

- [ ] **Step 4: Verify it passes** — same command, expect `EXIT=0`.

- [ ] **Step 5: Run the transport tree**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t6-pkg.log"
go test ./transport/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
```
Expected `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add transport/http/httpcore/errors.go transport/http/httpcore/errors_test.go
git commit -m "feat(transport): classify ErrEmptyTriggerKey as 400 (ADR-0152)"
```

---

### Task 7: Reject an unnamed ReceiveTask at authoring time

**Files:**
- Modify: `definition/model/validate.go` (sentinel near `:142`, rule near `:566-573`)
- Modify: `definition/model/validate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `model.ErrEmptyMessageName` (exported sentinel).

⚠ **Why this is in scope** (audit finding 7): `receiveTaskStrategy.enter`
(`engine/step_nodes.go:97-99`) sets `tok.AwaitMessage = rt.MessageName`
**unconditionally**, unlike the catch-event (`:720,:725`) and boundary (`:60,:62`) paths
which guard `!= ""`. `NewReceiveTask(id, "")` is accepted and nothing validates it, so
such a token parks on `AwaitMessage == ""`. It is resumable today only via
`Step(NewMessageReceived(at, "", "", …))` — which Task 5 now rejects, stranding the token
permanently. Shipping Task 5 without this knowingly introduces a token leak.

⚠ **`definition/model` CANNOT import `definition/activity`** (import cycle). Match on
`n.Kind() == KindReceiveTask` and read `toWire(n).MessageName`, exactly as the
neighbouring `ErrPayloadValidationRequiresMessage` rule does. A type assertion to
`activity.ReceiveTask` will not compile.

- [ ] **Step 1: Write the failing test**

Append to `definition/model/validate_test.go`, matching the file's existing style (read a
neighbouring validation test first for the builder helpers it uses):

```go
// TestValidateReceiveTaskMessageName covers ADR-0152: a ReceiveTask waits on a
// named message, and an empty name parks a token that no MessageReceived can
// resume once empty identity keys stop matching.
func TestValidateReceiveTaskMessageName(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		messageName string
		assert      func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:        "a named receive task validates",
			messageName: "order.paid",
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:        "an unnamed receive task is rejected",
			messageName: "",
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEmptyMessageName)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := model.NewProcessDefinition("p", "1",
				event.NewStartEvent("start"),
				activity.NewReceiveTask("recv", tc.messageName),
				event.NewEndEvent("end"),
				model.NewSequenceFlow("f1", "start", "recv"),
				model.NewSequenceFlow("f2", "recv", "end"),
			)
			tc.assert(t, model.Validate(def))
		})
	}
}
```

**Adjust the construction helpers to whatever `validate_test.go` already uses** — the
assertions are the contract, the scaffolding is not.

- [ ] **Step 2: Run the test to verify it fails**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t7.log"
go test -run TestValidateReceiveTaskMessageName ./definition/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -30 "$LOG"
```
Expected `EXIT=1` — `undefined: model.ErrEmptyMessageName` (compile error, a valid red
state).

- [ ] **Step 3: Add the sentinel and the rule**

Sentinel, beside `ErrPayloadValidationRequiresMessage` (`definition/model/validate.go:142`):

```go
	// ErrEmptyMessageName reports a ReceiveTask whose MessageName is empty. Such a
	// node parks its token on an empty AwaitMessage, which no MessageReceived can
	// resume once an empty identity key stops matching (ADR-0152).
	ErrEmptyMessageName = errors.New("workflow-definition: receive task requires a message name")
```

Rule, beside the `ErrPayloadValidationRequiresMessage` loop (`:566-573`):

```go
	// ReceiveTask: the node waits for a NAMED message. An empty name parks the
	// token on AwaitMessage "" (engine/step_nodes.go:97-99 assigns it
	// unconditionally, unlike the catch-event and boundary paths), and an empty
	// identity key matches no record (ADR-0152) — so the token could never be
	// resumed. Reject the shape at authoring time. model cannot import the leaf
	// activity package, so the name is read from the node's wire form.
	for _, n := range d.Nodes {
		if n.Kind() != KindReceiveTask {
			continue
		}
		if toWire(n).MessageName == "" {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEmptyMessageName, n.ID()))
		}
	}
```

- [ ] **Step 4: Verify it passes** — same command, expect `EXIT=0`.

- [ ] **Step 5: Run the definition tree and the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t7-pkg.log"
go test ./definition/... ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -30 "$LOG"
```
Expected `EXIT=0`. If an existing fixture builds a `ReceiveTask` with no message name,
give it one — that fixture was exercising the shape this rule now forbids.

- [ ] **Step 6: Commit**

```bash
git add definition/model/validate.go definition/model/validate_test.go
git commit -m "feat(definition): reject a receive task with no message name (ADR-0152)"
```

---

### Task 8: Step-level regression — the cross-scope wedge

⚠ **Depends on Task 1.**

**Files:**
- Create: `engine/step_cancel_test.go`

**Interfaces:**
- Consumes: Task 1's `cancelTimersByTaskID` guard. **Adds no production code.**

**Background:** the end-to-end proof of the headline bug, exercised through
`cancelTokenWaits` (`engine/step_cancel.go:12`), which is called per-token from the
error-propagation sweep (`engine/step_errors.go:393`) over every token in the erroring
scope. A swept token whose `AwaitCommand` is empty must not disturb a retry timer owned
by a token in a **different** scope.

- [ ] **Step 1: Write the failing test**

Create `engine/step_cancel_test.go`:

```go
package engine

// step_cancel_test.go — white-box tests for cancelTokenWaits.
//
// ADR-0152 regression: cancelTokenWaits sweeps a token's waits by task key
// (tok.AwaitCommand). A token parked on a signal/message, or simply active, has an
// empty AwaitCommand. Because TimerRetry records carry no TaskID, an unguarded sweep
// matched every retry timer in the INSTANCE — including retries owned by tokens in
// sibling scopes that were not being cancelled. Those tokens then sat in TokenWaiting
// forever with their timer cancelled in the scheduler: the instance neither completed
// nor failed.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelTokenWaitsLeavesSiblingScopeRetriesIntact(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	// tokSwept is being cancelled: it is parked on a MESSAGE, so its AwaitCommand
	// is empty — the exact shape that triggered the bug.
	// tokSibling lives in a DIFFERENT scope, is mid-retry, and must survive.
	s := &InstanceState{
		Status: StatusRunning,
		Tokens: []Token{
			{ID: "tokSwept", State: TokenWaiting, NodeID: "recv", ScopeID: "scopeA", AwaitMessage: "msg"},
			{ID: "tokSibling", State: TokenWaiting, NodeID: "svcB", ScopeID: "scopeB", AwaitCommand: "tmRetry"},
		},
		Timers: []timerRecord{
			{TimerID: "tmRetry", Kind: TimerRetry, Token: "tokSibling", NodeID: "svcB", ScopeID: "scopeB"},
		},
		History: []NodeVisit{
			{TokenID: "tokSwept", NodeID: "recv", EnteredAt: at},
			{TokenID: "tokSibling", NodeID: "svcB", EnteredAt: at},
		},
	}

	swept := s.Tokens[0]
	cmds := cancelTokenWaits(s, &swept, at, CloseKindBoundaryInterrupted)

	// The sibling's retry timer must survive in state...
	require.Len(t, s.Timers, 1, "the sibling scope's retry timer must not be swept")
	assert.Equal(t, "tmRetry", s.Timers[0].TimerID)

	// ...and the sweep must emit nothing at all: the swept token owns no waits of
	// its own, so every one of the three sweeps inside cancelTokenWaits is empty.
	assert.Empty(t, cmds, "a swept token with no waits of its own emits no commands")

	// The swept token itself is consumed, and the sibling still exists.
	assert.Nil(t, s.tokenByID("tokSwept"), "the swept token is consumed")
	require.NotNil(t, s.tokenByID("tokSibling"), "the sibling token must survive")
	assert.Equal(t, TokenWaiting, s.tokenByID("tokSibling").State)
}
```

- [ ] **Step 2: Demonstrate the red state**

⚠ Task 1 **committed** its guard, so `git stash` stashes nothing (audit M8). Revert the
guarded file to its pre-Task-1 state instead:

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t8.log"
git log --oneline -- engine/state_timers.go | head -3          # find the Task 1 commit
git checkout <task1-commit>~1 -- engine/state_timers.go
go test -run TestCancelTokenWaitsLeavesSiblingScopeRetriesIntact ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
git checkout HEAD -- engine/state_timers.go
```

Expected during the revert: `EXIT=1` with `"[]" should have 1 item(s), but has 0` — the
sibling's timer was swept. Record both runs; this is the plan's primary regression proof.

- [ ] **Step 3: No implementation change**

This task adds no production code. If the test fails with the guard in place, stop and
report — the guard is incomplete.

- [ ] **Step 4: Run the engine package**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-t8-pkg.log"
go test ./engine/... > "$LOG" 2>&1 ; echo "EXIT=$?" ; tail -20 "$LOG"
```
Expected `EXIT=0`.

- [ ] **Step 5: Commit**

```bash
git add engine/step_cancel_test.go
git commit -m "test(engine): cross-scope retry survives a token sweep (ADR-0152)"
```

---

### Task 9: Verification, ADR refresh, and the single feature bundle

**Files:**
- Verify (do **not** recreate): `docs/adr/0152-empty-identity-key-matches-nothing.md`
- Modify: this plan (add `▶ Progress`)

- [ ] **Step 1: Refresh, do not recreate, the ADR**

⚠ ADR-0152 is already written and committed (`7183463`) and was revised after the
adversarial audit. **Read it and confirm it still matches what shipped** — in particular
that `TimerFired` is listed as exempt, that the exemption table includes `excludeTimerID`,
that the status-change table (404/422/200 → 400) is present, and that the journal-replay
consequence is recorded. Amend only if the implementation diverged.

- [ ] **Step 2: Run the full verification gate**

```bash
LOG="${TMPDIR:-/tmp}/wrkflw-gate.log"
go build ./... ; echo "BUILD=$?"
go test -race -coverprofile=cover.out ./... > "$LOG" 2>&1 ; echo "GO_TEST_EXIT=$?"
tail -40 "$LOG"
scripts/coverage.sh cover.out
golangci-lint run ./... ; echo "LINT=$?"
```

Expected `BUILD=0`, `GO_TEST_EXIT=0`, `LINT=0`, coverage ≥ 85% excluding generated files.
Check exit codes, never a filtered tail. Postgres/MySQL suites need Docker — if it is
unavailable, say so explicitly rather than reporting a green suite.

- [ ] **Step 3: Squash into one feature bundle**

CLAUDE.md requires one feature = one commit bundling implementation, tests, and
documents. Per-task commits exist so work survives an agent dying mid-task; collapse now:

```bash
git reset --soft "$(git merge-base main HEAD)"
git add -A
git commit -F - <<'MSG'
feat(engine)!: empty identity keys match no record (ADR-0152)

An identity key names one specific record; the empty string names none. Sixteen
state-layer lookup/sweep helpers now refuse an empty key, Step rejects an inbound
trigger whose identity key is empty with ErrEmptyTriggerKey (HTTP 400), and
model.Validate rejects a ReceiveTask with no message name.

Closes three defect classes:
- cancelTimersByTaskID("") swept every TimerRetry in the instance, including
  retries owned by tokens in sibling scopes, wedging them in TokenWaiting.
- The arm generics returned an arm of the wrong kind. Error-boundary arms carry
  no non-empty match field at all, so an empty signal name could match one and
  interrupt a live host activity.
- Consumer-built triggers reached tokenAwaiting("") and tokenIDsAwaitingSignal(""),
  resuming unrelated tokens. TargetNode and FailingActionName bypass Step and are
  covered by the helper layer.

TimerFired.TimerID is deliberately NOT validated: TestTimerFiredStaleTokenIsNoop
pins an empty TimerID as a clean no-op, and the helper guards already deliver it.

scopeID, correlationKey, excludeTimerID, StartInstance.StartNodeID, and
CompensateRequested.ToNode/.ReverseNode are exempt; dedicated tests pin that.

BREAKING CHANGE: a trigger carrying an empty identity key is now rejected with
engine.ErrEmptyTriggerKey (HTTP 400). Previously HumanCompleted{TaskID:""}
returned 404, the other Human* triggers 422, and ResolveIncident{IncidentID:""}
was a documented idempotent no-op returning 200. A definition with an unnamed
ReceiveTask is now rejected by model.Validate. Replaying a journal that contains
a historical empty-key SignalReceived will fail on that entry.
MSG
```

- [ ] **Step 4: Add the Progress block to this plan**

Prepend a `▶ Progress` section recording branch and commit SHA, which tasks landed, the
verification numbers from Step 2, and anything deferred (the redundant
`cancelTimersByTaskID` call in `cancelTokenWaits`; the `" "`/sentinel-key gap). This is
the CLAUDE.md rule-#10 handover checkpoint.

- [ ] **Step 5: Delivery Gate**

Run `/code-review`, then `/security-review`. Fix **all** findings, folding each into the
single commit with `git commit --amend`. State explicitly, with reasons, any finding
adjudicated as false-positive or out-of-scope. Only then merge to `main` with `--no-ff`
and push.

---

## Self-Review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| Tier 1 — `cancelTimersByTaskID` guard | 1 |
| Tier 1b — the three arm generics (incl. error-boundary shape) | 2 |
| Tier 2 helpers — token/signal/message lookups | 3 |
| Tier 3 — remaining timer, arm, token, task helpers | 1, 2, 3, 4 |
| Sixteen guards in four files | 1 (4), 2 (5), 3 (6), 4 (1) |
| Inherited guards not double-guarded | 4 (test), 2 & 3 (explicit "do not touch") |
| `excludeTimerID` exemption | 1 (dedicated table row) |
| `ErrEmptyTriggerKey` + `validateTriggerKey`, `TimerFired` exempt | 5 |
| Exhaustiveness pin | 5 (`TestValidateTriggerKindsAreExhaustive`) |
| 400 classification | 6 |
| `ReceiveTask` authoring rule | 7 |
| Test 1 — tier-1 wedge regression | 8 (Step level), 1 (helper level) |
| Test 2 — tier-2 per-trigger regressions | 5 |
| Test 3 — tier-1b arm cross-matching | 2 |
| Test 4 — invariant lock with planted empty-keyed records | 1, 2, 3, 4 |
| Test 6 — exemption tests | 1 (`excludeTimerID`), 2 (`armByMessage`), 3 (`tokenAwaitingMessage`), 5 (`TimerFired`, `StartInstance`, `CompensateRequested`) |
| Test 7 — `ReceiveTask` validation | 7 |
| Test 8 — purity | 9 (full suite) |
| ADR-0152 | already committed; verified in 9 |

**Known gaps, stated rather than hidden:**
- The spec's scope-key exemption tests (`removeEventTriggeredSubprocessArmsForScope("")`,
  `tokensInScope("")`) get no new test. Neither helper is modified, and Tasks 2–3 carry
  explicit "do not touch" instructions; the full-suite run in Task 9 covers existing
  behaviour. Add rows to `engine/state_esp_test.go` / `engine/scope_test.go` if a
  reviewer wants them pinned.
- Spec Testing §2 asks for a `Step`-level "no unrelated token advanced" assertion per
  trigger; Task 5 delivers 11 validator-level rows plus one `Step`-level case
  (`SignalReceived`). The remaining triggers are covered at validator level only.

**Placeholder scan:** no TBD/TODO. Every implementation block is a complete function body
— the audit found two `// ... rest unchanged ...` stubs in the previous revision and both
are now spelled out.

**Type consistency:** `validateTriggerKey`, `validatedTriggerKinds`, `exemptTriggerKinds`,
`triggerTypeName`, and `ErrEmptyTriggerKey` are defined in Task 5 and consumed by name in
Tasks 5 and 6. `ErrEmptyMessageName` is defined and consumed in Task 7. `armsFixture()`,
`parkedTokens()`, and `ghostTokens()` are each defined once and used only within their
own file.
