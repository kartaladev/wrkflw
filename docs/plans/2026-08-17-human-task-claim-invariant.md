# Human-task claim invariant enforced before commit — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans`. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make a contradictory human-task claim shape impossible to commit, and impossible to
write through any `TaskStore` — without a rejection ever stranding a process instance.

**Architecture:** `humantask.Validate` (R1/R2/R3) is the single definition. It runs **pre-commit**
over the step's emitted `UpdateTask` commands (primary seam — a rejection aborts the step before
this iteration's commit), and again inside all three `Upsert` implementations (defence-in-depth, for a
consumer calling `Upsert` directly). An empty reassignment target is refused earlier still, in
`Step`, before `cloneState`. Both new sentinels get HTTP classifications, and consumers get an
exported conformance helper.

**Tech Stack:** Go 1.25, `testify`, `dbtest.RunTestSQLite` (pure-Go), testcontainers for the
Postgres/MySQL dialect legs.

**Bundle:** spec `docs/specs/2026-08-17-human-task-claim-invariant-on-write.md` · ADR
`docs/adr/0183-a-human-tasks-claim-invariant-is-enforced-on-write.md` · adjudication
`docs/specs/2026-08-17-adr-0183-audit-adjudication.md` · lenses `…-audit-lens-{a,b,c}.md` ·
evidence `…-premise-evidence.md`. Branch: `feat/human-task-claim-invariant`.

## ▶ Progress

**Status: ✅ IMPLEMENTED — all 8 tasks landed, folded into ONE feature commit on branch
`feat/human-task-claim-invariant`. NOT merged, NOT pushed. Awaiting the owner-only gates.**

Do not quote the commit SHA here — the `/code-review` fixes will amend it. Name the branch.

- **Phases landed: all.** Task 1 `humantask` · 2 `engine` · 3 `runtime` · 4 `internal/persistence/store`
  · 5 `persistence` · 6 `transport/http/httpcore` · 7 `processtest` · 8 CHANGELOG + fold.
- Wave 1 = Tasks 1–2, Wave 2 = Tasks 3–7 (five agents, five packages, concurrent), Wave 3 = controller.

### Verification — executed, with the real numbers

| gate | result |
|---|---|
| `go test -race -count=1 -coverprofile ./...` | **EXIT=1**, solely from a **pre-existing load-flaky** test — see below |
| `go test -count=1 ./...` | **EXIT=0**, clean |
| `golangci-lint run ./...` **(repo-wide, not package-scoped)** | **EXIT=0**, `0 issues.` |
| touched-package coverage | `humantask` 100 % · `httpcore` 94.6 % · `runtime` 93.8 % · `engine` 93.0 % · `processtest` 91.6 % · `store` 87.5 % · `persistence` **84.1 %** |
| repo-wide total | 74.8 % (pre-existing; `service` 53.9 % is backlog 20) |

⚠ **`persistence` at 84.1 % is BELOW the 85 % floor — and it is NOT a regression.** Measured on
unmodified `main` in a throwaway worktree: **also exactly 84.1 %**. This is backlog item 34, which
prescribes closing it by testing the advisory lock rather than the option setters. Verified, not
inherited.

⚠ **`TestGocronScheduleJobTriggers/At_(past-due)_fires_immediately_(time-skew_branch)` is
PRE-EXISTING and load-flaky.** Not excused as "not ours" — established by execution:
this delivery touches **zero** files under `scheduler/`; both trees pass `-race -count=25` in
isolation (0 failures each); and the **identical failure reproduces on unmodified `main`** under the
same full-suite `-race ./...` contention. It is a second instance of blocker 5's class. **Filed as a
new backlog item; do not silence it.**

### Adjudicated at implementation

- ⚠⚠ **Audit finding B8 REFUTED — the terminal-sweep drop cannot occur.** Both audit rounds accepted
  it and this plan prescribed a test for it; the prescribed test **could not have failed**.
  `cancelOpenTasks` normalizes every swept task to `Cancelled`, which is unconstrained on the claim
  axis. Measured (`validate=<nil>` for both tasks, `CancelInstance err=<nil>`), re-derived over all
  eight `UpdateTask` emit sites. ADR amended in-bundle with the measurement (rule #11). B8's
  substance ships as two mutation-verified replacements — see Task 3 Step 6.
- **Six stale round-1 fragments** were still telling implementers to reject an empty claimant, in
  this plan, the spec and the ADR, after round 2 reversed that decision. Corrected before dispatch.
  Task 1's agent found a **seventh** inside the plan's own `Validate` doc-comment block, where the
  prose contradicted the code directly beneath it.
- **The plan's own row count was wrong**: prose said "eight rows / PASS=9", its code block had
  **nine** (`t-1`…`t-9`), giving PASS=10. Corrected in three places.
- **`containsTask` (Task 4 Step 1) does not exist** anywhere in the package — replaced with a local
  closure rather than adding a package-level symbol.
- **The plan's positive control could not catch over-rejection of the kiosk shape** (it claims as
  `alice`). A second control pinning `Claimed` + empty claimant as legal was added in the store
  conformance group, and `processtest` ships a **kiosk-hostile store** row that fails only that
  control.
- **My own brief carried a false quantifier**: "every rejection fixture MUST declare Candidates +
  Eligibility or the inbox assertions cannot fail". Unachievable for `Claimed`+nil, where neither
  inbox can fire regardless of fixture — `Get`→`ErrTaskNotFound` is the sole discriminator. Recorded
  in the test rather than left to overclaim.
- **Two false comments corrected beyond the two the bundle planned.** The `resolveHumanCandidates`
  call-site comment claimed a resolver outage "can no longer leave a committed instance parked" —
  measured false by lens D. And the store's ADR-0148 sentence was false in **both** halves, not one;
  Task 4 ran a three-row SQLite probe to confirm before rewriting.
- **Task 5 caught itself pre-commit** about to write "MemTaskStore and the SQL HumanTaskStore both
  validate" while Task 4 was still in flight — grepped, found no guard yet, reworded to reference the
  *contract* instead of a sibling's present state.

### Mutation verification (all `cp`-restored, never `git checkout <path>`)

Task 2 guard removed → only the empty-target row fails, control still passes. Task 3 ×3: call site
removed → integration fails on `Version expected 3, actual 4`; hook made to over-reject
`Cancelled`+claim → sweep test fails; command loop truncated to `cmds[:1]` → the second-UpdateTask
row fails. Task 5 guard removed → byte-identical `expected: 0 actual: 1` on the `upserts` counter.
Task 7 `Get`-assertion deleted → the permissive-store row fails.

### `/code-review high --fix` — 4 findings, ALL closed

| # | finding | verdict |
|---|---|---|
| 1 | `TaskService.Reassign` never checked `to`, so an empty target paid a store read + authz round-trip **and incremented `humanTasks{event="reassigned"}`** before `Step` refused it — the metric counted reassignments that never happened | **FIXED** by the reviewer: guard ahead of the store read, same sentinel so the 400 classification and `errors.Is` are unchanged |
| 2 | **Seven ADR citations rotted — by this bundle's OWN insertions.** `processdriver_action.go:236` now lands on the *new* `validateTaskCommands`, so the ADR read as if the precedent it cites were the hook itself | **FIXED** by the reviewer |
| 3 | The **exported** conformance helper verified "persists nothing" with `Get` alone, while the internal suite also asserts neither inbox lists the row — the discriminators for the **double-listing** shape this ADR exists to close | **ACCEPTED, FIXED** (reviewer deferred to owner; controller accepted — new public API, unmerged, cheapest moment) |
| 4 | `RunTaskStoreConformance`'s factory got no `*testing.T`, so the README's own pattern captures the parent `T` inside a child subtest → cross-goroutine `FailNow` | **ACCEPTED, FIXED**: signature is now `newStore func(t *testing.T) humantask.TaskStore`. Free pre-merge; a breaking change after |

Finding 4's measured symptom, corrected from the brief: the message is **not lost** — it is re-attributed to the parent via `=== NAME`, and the real damage is that the run **truncates at the first shape (1 of 8)**, so a consumer sees one case instead of a suite. Five mutations (M1–M5) pin finding 3's new assertions; a `leakyRollbackTaskStore` and an `inboxFailingTaskStore` were added so they can fail.

⚠⚠⚠ **THE CONTROLLER'S BRIEF WAS WRONG THREE TIMES — third delivery running.** (a) "the message is lost" — no, re-attributed. (b) "for an out-of-range state only `AssignedTo` fires" — true of the *internal* fixture, but the *exported* out-of-range fixture carried a **nil claim**, so **neither** query could reach it and the new assertion would have been **unfailable for that shape**; the agent fixed the fixture and pinned it with mutation M4. (c) "rework the pinned counts" — with the `nil, nil` inbox stubs the counts would not have moved *at all*; the real work was making `permissiveTaskStore` answer its inboxes for real. ⭐ **A brief that inherits a measurement from a SIBLING context must re-derive it in the target context — the internal suite's fixtures are not the exported helper's fixtures.**

Optional follow-up, flagged not done: the exported helper still makes no *positive* inbox assertion on the legal leg (the internal suite has one).

### `/security-review` — 0 findings, and its caveat is now CLOSED

**Zero High/Medium findings.** The review judged the change net security-**positive**, on three axes it
checked specifically because human tasks carry authorization data:

- **The new empty-`to` early return in `TaskService.Reassign` is NOT an oracle.** It returns a
  constant string with no task-, actor- or caller-derived data, unconditionally. It **removes** a
  pre-existing existence oracle: `to=""` used to answer 404 for a missing task vs 403 for an
  unauthorized one; both are now an identical 400.
- **No data exposure via the new 422.** Every `Validate` format string carries only `TaskID` and the
  state — no actor IDs, `Claim.Actor`, `AuthzSpec`, `Candidates` or process `Vars`. `ClassifyError`
  evaluates `authz.ErrNotAuthorized` → 403 **before** the new 422 arm, so it cannot shadow a denial.
- **Task visibility tightens, never loosens.** Every shape now rejected was previously *more* visible.
  The `AssignedTo("")` wildcard guard is untouched in both stores, and no legitimate engine shape is
  newly rejected — the dangerous failure mode, since a skipped write would leave a stale, more
  permissive row.
- SQL injection: none — the `Upsert` statement is a compile-time constant and `t.State.String()` is a
  **bind parameter**, not interpolated.

⚠ **The review labelled itself PARTIAL** — it ran only the container-free subset and verified the SQL
guard by source inspection. **That caveat is now closed by execution.** All container-backed packages
pass `-race` (`DOCKER_TESTS_EXIT=0`: `internal/persistence/store` 72.4 s, `persistence` 34.3 s,
`internal/database` 35.7 s, `runtime` 21.9 s, `scheduler` 23.6 s, the casbin/eventing/elector legs),
and the new conformance group is proven to run on **all three dialects** — sqlite / postgres (1.83 s)
/ mysql (6.35 s), 2 controls + 3 rejections each, `no tests to run` = 0.

⚠ The pre-existing load-flaky `TestGocronScheduleJobTriggers` **passed** in this narrower run (5.2 s),
consistent with backlog 42's load-dependence rather than contradicting it.

### Remaining

**Both gates are DONE.** `/code-review high --fix` — 4 findings, all closed (table above).
`/security-review` — **0 findings**, its partial-run caveat closed by the Docker-backed run above.
⇒ **Ready to merge `--no-ff` and push**, then delete the branch. File the deferred items exactly as
the spec's canonical `## Deferred` section lists them (five items). Deferred items: the spec's `## Deferred` section is canonical (five items).

## Global Constraints

- **Go 1.25.** Module `github.com/kartaladev/wrkflw`.
- **TDD strict.** A `Write` of a test followed immediately by a `Write` of the implementation, with
  no `go test` between them, is **forbidden** — the red state must be visible in the transcript.
- **Table tests use the `table-test` skill's `assert`-closure form**; never `want`/`wantErr`. Use
  `t.Context()`. Load the skill before writing tests.
- **Black-box tests** (`package <pkg>_test`) where the package already does this.
- **Error sentinel prefix** `workflow-<pkg>: …`.
- **Judge runs by EXIT CODE**, never a pipeline tail. Always `-count=1`.
- ⚠ **`go test -run` on a nonexistent name exits 0.** Confirm a test *ran*: assert a `--- PASS`
  count, and grep for `no tests to run`. Do **not** rely on "no `---` lines means nothing ran" —
  the parent test body emits `--- PASS` lines even when every child is filtered out.
- **Fan out by Go package.** Wave 1: Tasks 1 and 2 (different packages, no dependency). Wave 2:
  Tasks 3–7 (all depend on Task 1; all different packages). Wave 3: Task 8.
- ⚠ **An agent that must mutate to measure gets its own `git worktree`** — during the first audit two
  lenses patched the same two files in the shared tree concurrently.
- Docker: needed for Task 4's Postgres/MySQL legs and the final Verification. Standing permission
  covers the Verification coverage + no-regressions runs: probe `docker info` and go; if it is down,
  say so and label any SQLite-only result as partial.

## File Structure

| file | responsibility |
|---|---|
| `humantask/validate.go` *(create)* | `ErrInvalidTask` + `Validate` — R1/R2/R3, the single definition |
| `humantask/validate_test.go` *(create)* | `TestValidate` table |
| `humantask/memory.go` · `memory_test.go` *(modify)* | `MemTaskStore.Upsert` guard |
| `humantask/humantask.go` *(modify)* | `TaskStore.Upsert` contract doc |
| `engine/errors.go` *(modify)* | the new empty-reassign-target sentinel |
| `engine/step.go` *(modify)* | refuse `HumanReassigned{To: ""}` before `cloneState` |
| `engine/step_nodes.go` *(modify)* | correct the false `ManualImmediate` comment |
| `runtime/processdriver_action.go` *(modify)* | `validateTaskCommands` — the pre-commit hook |
| `runtime/processdriver.go` *(modify)* | call it pre-commit, beside `resolveHumanCandidates` |
| `internal/persistence/store/humantask_store.go` *(modify)* | `Upsert` guard + correct the inherited ADR-0148 doc sentence |
| `internal/persistence/store/humantask_store_conformance_test.go` *(modify)* | rejection cases + the legal-shape positive control |
| `persistence/caching_task_store.go` · `_test.go` *(modify)* | validate before delegating; `upserts` counter |
| `transport/http/httpcore/errors.go` *(modify)* | 422 for `ErrInvalidTask`, 400 for the reassign sentinel |
| `processtest/taskstoreconformance.go` *(create)* | the exported consumer-facing helper |
| `CHANGELOG.md` *(modify)* | three breaking entries |

---

### Task 1 — `humantask`: the rule, the sentinel, the in-memory store *(Wave 1)*

**Files:** create `humantask/validate.go`, `humantask/validate_test.go`; modify
`humantask/memory.go` (`Upsert`, ~line 33), `humantask/memory_test.go`, `humantask/humantask.go`
(`TaskStore.Upsert` doc, ~line 186).

**Produces — Tasks 3–7 depend on these exact names:** `humantask.ErrInvalidTask`,
`humantask.Validate(t HumanTask) error`.

**Why this fails today:** `Validate` does not exist; `MemTaskStore.Upsert` ends in an unconditional
`return nil` (measured: `State: Claimed, Claim: nil` succeeds).

- [ ] **Step 1: Write the failing test.** Create `humantask/validate_test.go`. No package doc
comment (two sibling files already carry one). **Nine rows** (`t-1` … `t-9`). Two pin the deferred
completion/cancelled silences as deliberate, one pins the
ADR-0148 kiosk shape as legal, and R3's two rows close the bypass round 1 found.

```go
package humantask_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	claim := func(id string) *humantask.Claim {
		return &humantask.Claim{Actor: authz.Actor{ID: id}, At: at}
	}

	type testCase struct {
		name   string
		task   humantask.HumanTask
		assert func(t *testing.T, err error)
	}

	valid := func(t *testing.T, err error) { require.NoError(t, err) }
	invalid := func(id, want string) func(*testing.T, error) {
		return func(t *testing.T, err error) {
			require.ErrorIs(t, err, humantask.ErrInvalidTask)
			require.Contains(t, err.Error(), `task "`+id+`"`, "must name the task")
			require.Contains(t, err.Error(), want, "must name the contradiction")
		}
	}

	cases := []testCase{
		// R1
		{"claimed with a claim is valid",
			humantask.HumanTask{TaskID: "t-1", State: humantask.Claimed, Claim: claim("alice")}, valid},
		{"claimed without a claim is rejected",
			humantask.HumanTask{TaskID: "t-2", State: humantask.Claimed},
			invalid("t-2", "requires a claim")},
		// NOTE: `Claimed` + an EMPTY claimant is deliberately NOT rejected — it is
		// ADR-0148 amendment 1 §4's kiosk shape. Pinned as legal by the row below.
		{"claimed with an empty claimant is ACCEPTED (ADR-0148 kiosk shape)",
			humantask.HumanTask{TaskID: "t-3", State: humantask.Claimed,
				Claim: &humantask.Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}, At: at}}, valid},
		// R2
		{"unclaimed without a claim is valid",
			humantask.HumanTask{TaskID: "t-4", State: humantask.Unclaimed}, valid},
		{"unclaimed carrying a claim is rejected",
			humantask.HumanTask{TaskID: "t-5", State: humantask.Unclaimed, Claim: claim("alice")},
			invalid("t-5", "must not carry a claim")},
		// R3 — closes the bypass: an out-of-range state is neither Claimed nor Unclaimed,
		// so without this rule it sails through and reads back R2-violating.
		{"an out-of-range state is rejected",
			humantask.HumanTask{TaskID: "t-6", State: humantask.TaskState(99), Claim: claim("alice")},
			invalid("t-6", "unknown state")},
		{"a negative state is rejected",
			humantask.HumanTask{TaskID: "t-7", State: humantask.TaskState(-1)},
			invalid("t-7", "unknown state")},
		// DELIBERATE silences — see ADR-0183. ManualImmediate mints Completed with
		// neither claim nor completion; a task cancelled while held keeps its claim.
		{"completed with neither claim nor completion is accepted",
			humantask.HumanTask{TaskID: "t-8", State: humantask.Completed}, valid},
		{"cancelled retaining a claim is accepted",
			humantask.HumanTask{TaskID: "t-9", State: humantask.Cancelled, Claim: claim("alice")}, valid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, humantask.Validate(tc.task))
		})
	}
}
```

- [ ] **Step 2: Verify RED.**
`go test -count=1 ./humantask/... > /tmp/red1.log 2>&1; echo "EXIT=$?"; cat /tmp/red1.log`
Expected: non-zero, `undefined: humantask.Validate` / `undefined: humantask.ErrInvalidTask`.

- [ ] **Step 3: Implement.** Create `humantask/validate.go`:

```go
package humantask

import (
	"errors"
	"fmt"
)

// ErrInvalidTask reports a [HumanTask] whose fields contradict each other. Every
// [TaskStore] implementation in this module returns it (wrapped, naming the task
// and the contradiction) from Upsert, and the runtime refuses to commit a step
// that would project such a task. Match it with errors.Is.
var ErrInvalidTask = errors.New("workflow-humantask: invalid task")

// Validate reports whether t is internally consistent, returning an error
// wrapping [ErrInvalidTask] when it is not.
//
// It enforces the claim invariant documented on [HumanTask.Claim] — a claim is
// present, and names someone, exactly when an actor holds the task:
//
//   - a Claimed task must carry a Claim whose Actor.ID is non-empty;
//   - an Unclaimed task must not carry a Claim;
//   - State must be one of the four declared constants.
//
// Completed and Cancelled are deliberately unconstrained on the claim axis: a task
// cancelled while held keeps its claim as audit, and an immediate manual task
// completes without one. The completion axis (Completed implies a Completion) is
// NOT enforced — see ADR-0183.
//
// Note that Unclaimed is the zero value of [TaskState], so the Unclaimed rule also
// rejects a task carrying a Claim whose State was never set — including a decode
// that dropped only State. That is deliberate: such a record is exactly as
// contradictory as an explicitly Unclaimed one.
//
// Validate is a TaskStore-WRITE contract, not a whole-model invariant: the engine
// deliberately holds a Completed task with neither claim nor completion in instance
// state for an immediate manual task, and never writes it to a store.
func Validate(t HumanTask) error {
	// R3 first, as an ENUMERATED switch rather than a range check: a range check is
	// exact today but silently coupled to iota contiguity, so a fifth constant would
	// change its coverage with no test failing. TaskState.String() enumerates too.
	switch t.State {
	case Unclaimed, Claimed, Completed, Cancelled:
	default:
		return fmt.Errorf("%w: task %q: unknown state %d", ErrInvalidTask, t.TaskID, int(t.State))
	}
	switch {
	case t.State == Claimed && t.Claim == nil:
		return fmt.Errorf("%w: task %q: state %s requires a claim", ErrInvalidTask, t.TaskID, t.State)
	case t.State == Unclaimed && t.Claim != nil:
		return fmt.Errorf("%w: task %q: state %s must not carry a claim", ErrInvalidTask, t.TaskID, t.State)
	}
	return nil
}
```

⚠ Keep R3 first for readability, but NOT for the reason an earlier revision gave. "R3 must be
first or an out-of-range value falls through" is **false and mutation-disproved**: R1/R2 test
`== Claimed`/`== Unclaimed`, which an out-of-range value matches neither way, and moving R3 last
produced byte-identical output for all shapes. No test pins the ordering, so do not add a comment
claiming it is load-bearing.

- [ ] **Step 4: Verify GREEN and that all nine ran.** *(Observed at implementation: EXIT=0, PASS=10,
`no tests to run`=0.)*
`go test -count=1 -v -run '^TestValidate$' ./humantask/... > /tmp/g1.log 2>&1; echo "EXIT=$?"; echo "PASS=$(grep -cE '^\s*--- PASS' /tmp/g1.log)"; grep -c "no tests to run" /tmp/g1.log`
Expected: EXIT=0, **PASS=10** (9 subtests + the parent), and `0` for "no tests to run".

- [ ] **Step 5: Write the failing `MemTaskStore` test.** Append to `humantask/memory_test.go`.
Verified: that file already imports `time`, `errors`, `authz`, `humantask`, `require`, `assert` —
**add no imports**. It defines `makeTask`, which widens a bare `claimedBy` string into a `*Claim`;
do **not** use it, these cases need shapes it cannot express.

```go
func TestMemTaskStoreUpsertRejectsAnInvalidTask(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		task humantask.HumanTask
	}

	cases := []testCase{
		{"claimed without a claim", humantask.HumanTask{TaskID: "m-1", State: humantask.Claimed}},
		{"unclaimed carrying a claim", humantask.HumanTask{
			TaskID: "m-3", State: humantask.Unclaimed,
			Claim:  &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at}}},
		{"out-of-range state", humantask.HumanTask{TaskID: "m-4", State: humantask.TaskState(99)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := humantask.NewMemTaskStore()
			require.ErrorIs(t, store.Upsert(t.Context(), tc.task), humantask.ErrInvalidTask)

			_, getErr := store.Get(t.Context(), tc.task.TaskID)
			require.ErrorIs(t, getErr, humantask.ErrTaskNotFound,
				"a rejected Upsert must not store the task")
		})
	}
}
```

- [ ] **Step 6: Verify RED.**
`go test -count=1 -v -run '^TestMemTaskStoreUpsertRejectsAnInvalidTask$' ./humantask/... > /tmp/red2.log 2>&1; echo "EXIT=$?"; grep -cE '^\s*--- FAIL' /tmp/red2.log; grep -c "no tests to run" /tmp/red2.log`
Expected: non-zero exit, 4 FAIL subtests, and `0` "no tests to run".

- [ ] **Step 7: Implement.** In `humantask/memory.go`, first statement of `Upsert`:

```go
// Upsert inserts or replaces the task identified by t.TaskID. It rejects a task
// whose state contradicts its claim with [ErrInvalidTask]; see [Validate].
func (s *MemTaskStore) Upsert(_ context.Context, t HumanTask) error {
	// Validate before storing: this fake backs the reference wiring and much of the
	// suite, so staying permissive would let a test green-light a shape the SQL
	// store rejects (ADR-0183).
	if err := Validate(t); err != nil {
		return err
	}
	// Defensive copy of mutable fields before storing.
	t = copyTask(t)
	…
```

- [ ] **Step 8: Verify GREEN, whole package.**
`go test -count=1 ./humantask/... > /tmp/g2.log 2>&1; echo "EXIT=$?"; tail -5 /tmp/g2.log`
Expected EXIT=0. Zero churn was **measured** pre-implementation; if anything else fails here that
measurement was wrong — report it, do not relax the guard.

- [ ] **Step 9: State the contract on the interface.** Replace the `TaskStore.Upsert` doc
(`humantask/humantask.go` ~186):

```go
	// Upsert inserts or replaces the task identified by t.TaskID.
	//
	// Implementations MUST reject a task that fails [Validate] — a Claimed task with
	// no claim, an Unclaimed task carrying one, or an out-of-range State — by
	// returning an error wrapping [ErrInvalidTask]. Call
	// [Validate] rather than re-deriving the rule; the read path relies on the
	// invariant holding, and the runtime refuses to commit a step that would project
	// a task violating it (ADR-0183). Verify your implementation with
	// processtest.RunTaskStoreConformance.
	Upsert(ctx context.Context, t HumanTask) error
```

- [ ] **Step 10: Commit.**
`go build ./... && go test -count=1 ./humantask/... ; git add humantask/ && git commit -m "feat(humantask): reject an internally contradictory task on write (ADR-0183)"`

---

### Task 2 — `engine`: refuse an empty reassignment target, and one false comment *(Wave 1)*

**Files:** modify `engine/errors.go`, `engine/step.go` (~line 156), `engine/step_nodes.go` (~750),
and an `engine` test file (⚠ run `head -1` first — this package mixes `package engine` and
`package engine_test`).

**Produces:** the new sentinel name, consumed by Task 6.

**Why this fails today:** `NewHumanReassigned(at, taskID, from, "", by)` succeeds and
`handleHumanReassigned` mints `State: Claimed` with `Claim{Actor{ID: ""}}` — a row invisible to every
inbox query. Nothing validates `To`: `engine/trigger_validate.go:65` validates only `TaskID`,
`runtime/task/service.go:214-231` checks `from` but never `to`, and
`transport/http/httpcore/dto.go:60-67` states the REST handler carries no required-field validation.

- [ ] **Step 1: Write the failing test.** A reassignment naming nobody must be refused **before any
state is touched** — assert the returned state is the zero value, not a mutated clone.

```go
func TestStepRejectsAReassignmentWithNoTarget(t *testing.T) {
	t.Parallel()
	// Build any definition parked on a user task with a claimed task in state, then:
	trg := engine.NewHumanReassigned(at, "tk-1", "alice", "", authz.Actor{ID: "admin"})
	res, err := engine.Step(t.Context(), def, st, trg, engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrEmptyReassignTarget)
	require.Zero(t, res.State.InstanceID,
		"a refused trigger must return no state — validation runs before cloneState")
	require.Empty(t, res.Commands, "a refused trigger must emit no commands")
}
```

⚠ Also add a row asserting a **non-empty** `To` still works, so the guard cannot pass by refusing
everything.

- [ ] **Step 2: Verify RED.** `go test -count=1 -run '^TestStepRejectsAReassignmentWithNoTarget$' -v ./engine/... > /tmp/red3.log 2>&1; echo "EXIT=$?"; grep -c "no tests to run" /tmp/red3.log`
Expected: non-zero (`undefined: engine.ErrEmptyReassignTarget`), and `0` "no tests to run".

- [ ] **Step 3: Implement.** In `engine/errors.go`, beside `ErrEmptyTriggerKey`:

```go
	// ErrEmptyReassignTarget reports a HumanReassigned trigger whose To names no
	// actor. Reassignment moves a task's claim from one actor to another; the empty
	// string names none, so the result would be a Claimed task nobody holds —
	// invisible to AssignedTo (no claimant to match) and to ClaimableBy (not
	// Unclaimed), i.e. reachable only by ID.
	//
	// Deliberately NOT [ErrEmptyTriggerKey]: that sentinel is documented as an
	// *identity key* naming one specific record, and To is a required field rather
	// than the trigger's identity — TaskID already is. Like it, this is not wrapped
	// in [ErrInvalidTransition]: the instance state is irrelevant, the trigger
	// itself is malformed. Transports classify it 400. See ADR-0183.
	ErrEmptyReassignTarget = errors.New("workflow-engine: reassignment target is empty")
```

In `engine/step.go`, immediately after the existing `validateTriggerKey` block (~156), i.e. still
**before `cloneState`**:

```go
	// A reassignment naming nobody would mint a Claimed task no inbox can see
	// (ADR-0183). Rejected here, beside the identity-key check and ahead of
	// cloneState, so a malformed trigger has no side effects at all.
	if r, ok := trg.(HumanReassigned); ok && r.To == "" {
		return StepResult{}, fmt.Errorf("%w: %T.To", ErrEmptyReassignTarget, trg)
	}
```

- [ ] **Step 4: Verify GREEN.** `go test -count=1 ./engine/... > /tmp/g3.log 2>&1; echo "EXIT=$?"`

- [ ] **Step 5: Mutation-verify.** `cp engine/step.go /tmp/step.bak` (⚠ `cp`, never
`git checkout <path>`), delete the new block, confirm the test FAILS, then
`cp /tmp/step.bak engine/step.go && diff /tmp/step.bak engine/step.go && echo RESTORED`.

- [ ] **Step 6: Correct the false comment** in the `ManualImmediate` branch of
`engine/step_nodes.go`:

```go
	if ut.Manual && ut.ManualImmediate {
		// Immediate manual task: no actor acts on it, so it never parks. Record the
		// task as Completed for audit and advance the token along its single outgoing
		// flow immediately. No eligibility check, no payload, no deadline/reminder/
		// boundary arming — none are meaningful without a wait period.
		//
		// The record carries NEITHER a Claim NOR a Completion: no actor claimed it and
		// none completed it. This deliberately does NOT mirror handleHumanCompleted,
		// which records a Completion from the completing actor's trigger. It is why
		// humantask.Validate leaves the completion axis unconstrained (ADR-0183), and
		// why the deferred completion rule must carve this path out.
		ht.State = humantask.Completed
```

- [ ] **Step 7: Verify and commit.** `go test -count=1 ./engine/... ; echo "EXIT=$?"`
⚠ A comment-only edit must move no test. Note honestly in the commit body: `ManualImmediate` appears
in **no** engine or runtime test, so "no test moved" is true but is **not** evidence of coverage.
`git add engine/ && git commit -m "feat(engine): refuse a reassignment that names no actor (ADR-0183)"`

---

### Task 3 — `runtime`: the pre-commit hook *(Wave 2 — the load-bearing task)*

**Files:** modify `runtime/processdriver_action.go` (add `validateTaskCommands` near
`resolveHumanCandidates`, ~236), `runtime/processdriver.go` (call it at ~668); test in
`runtime/`.

**Consumes:** `humantask.Validate`, `humantask.ErrInvalidTask` (Task 1).

**Why this fails today — measured.** A rejected `Upsert` runs post-commit, so the state commits, the
remaining commands are dropped, no incident is raised, and the trigger cannot be retried:

```
PROBE PERSISTED state: status=running incidents=0 tokens=1
PROBE   ptoken[0] node=act await="…kos0"
PROBE RETRY same trigger err = … human task is not open: invalid state transition
```

- [ ] **Step 1: Write the failing UNIT test** for the pure function (it cannot fail vacuously):

```go
func TestValidateTaskCommands(t *testing.T) {
	// Table: an UpdateTask carrying a Claimed+nil task -> ErrInvalidTask;
	// a valid UpdateTask -> nil; a command slice with no UpdateTask -> nil;
	// an AwaitHuman alone -> nil (it cannot be claim-invalid; performAwaitHuman
	// hardcodes State: Unclaimed and never copies a Claim).
}
```

- [ ] **Step 2: Write the failing INTEGRATION test** — the one that proves *nothing commits*. The
fixture is constructible: `kernel.MemInstanceStore` exposes
`Load(ctx, id) (engine.InstanceState, Version, error)` and `Commit(ctx, expected, AppliedStep)`.

```go
func TestPreCommitRejectionCommitsNothing(t *testing.T) {
	// 1. Drive an instance to a parked user task and claim it normally.
	// 2. Corrupt the SNAPSHOT the way a downgrade would (backlog 32): Load it,
	//    nil the Claim on the Claimed task, Commit it back at the loaded Version.
	// 3. Re-emit that task's shape verbatim via the ONE pass-through emitter:
	//    engine.HumanCandidatesResolved (engine/step_triggers.go:612 touches
	//    neither State nor Claim).
	// 4. Assert: ApplyTrigger returns an error wrapping humantask.ErrInvalidTask,
	//    AND the persisted snapshot is BYTE-FOR-BYTE what step 2 wrote —
	//    same Version, incidents=0, no token parked on a new command.
}
```

⚠ **Verify this fixture is constructible before writing the assertions.** A previous delivery's
audit prescribed a fixture that turned out impossible to build. Check `kernel.AppliedStep`'s fields
(it needs at least `State` and `Trigger`) and confirm the corrupted snapshot survives the round-trip
— `Load` returns `state.Clone()`, so confirm `Clone` preserves a nil `Claim`. **If it is not
constructible, STOP and report** rather than substituting a weaker test.

- [ ] **Step 3: Verify RED for both.** Expected: unit test fails `undefined: validateTaskCommands`;
integration test fails because today the step **commits** — assert on the Version having advanced,
which is the discriminating observation.

- [ ] **Step 4: Implement.** In `runtime/processdriver_action.go`:

```go
// validateTaskCommands rejects a step whose task projections are internally
// contradictory, BEFORE any of the step's state is committed.
//
// This is the primary enforcement seam for the invariant [humantask.Validate]
// defines. It cannot live in perform(): perform runs AFTER the commit, and a
// perform error aborts the remaining command queue — so a rejection there commits
// the state, drops the later commands, raises no incident, and leaves the token
// parked on a command that will never be answered. Measured; see ADR-0183.
//
// It mirrors [ProcessDriver.resolveHumanCandidates], which moved pre-commit for
// this same reason. Only UpdateTask is inspected: AwaitHuman has a single emit
// site and performAwaitHuman builds its task with State: Unclaimed and no Claim,
// so it cannot be claim-invalid.
func validateTaskCommands(cmds []engine.Command) error {
	for _, c := range cmds {
		ut, ok := c.(engine.UpdateTask)
		if !ok {
			continue
		}
		if err := humantask.Validate(ut.Task); err != nil {
			return fmt.Errorf("workflow-runtime: reject step: %w", err)
		}
	}
	return nil
}
```

In `runtime/processdriver.go`, **immediately before** the existing `resolveHumanCandidates` block
(~668):

```go
		// Reject a contradictory task projection before the commit. Pure and cheap,
		// so it runs ahead of resolveHumanCandidates' resolver I/O: there is no point
		// paying for a group lookup on a step that cannot be committed (ADR-0183).
		if verr := validateTaskCommands(res.Commands); verr != nil {
			span.RecordError(verr)
			span.SetStatus(codes.Error, verr.Error())
			span.End()
			return st, verr
		}
```

- [ ] **Step 5: Verify GREEN.** `go test -count=1 ./runtime/... > /tmp/g4.log 2>&1; echo "EXIT=$?"`
⚠ `./runtime/...` is **not** container-free; this needs Docker.

- [x] **Step 6: ⚠ B8 REFUTED AT IMPLEMENTATION — replaced, not dropped.** The prescribed test
(N=2 open tasks, task 1 invalid, assert neither reconciliation is lost) **could not fail**:
`cancelOpenTasks` assigns `Cancelled` to every task it sweeps and `Cancelled` is unconstrained on the
claim axis, so the sweep emits no rejectable command. Measured — `validate=<nil>` for both tasks,
`CancelInstance err=<nil>`. `step_triggers.go:612` is the only emitter that can produce an invalid
command and it never coexists with a sweep. Shipped instead: two unit rows pinning that an invalid
command refuses the **whole** step rather than dropping tasks 2..N, and
`TestTerminalSweepReconcilesEveryTaskDespiteACorruptOne` guarding that the hook does **not** block
the sweep. Both mutation-verified. See the ADR's AMENDED-AT-IMPLEMENTATION block.

- [ ] **Step 7: Mutation-verify** the hook: `cp` the file, remove the call site, confirm the
integration test FAILS on the Version having advanced, restore, `diff`.

- [ ] **Step 8: Commit.** `git add runtime/ && git commit -m "feat(runtime): refuse to commit a step that projects a contradictory task (ADR-0183)"`

---

### Task 4 — `internal/persistence/store`: the SQL guard, conformance, and a false doc line *(Wave 2)*

**Files:** modify `internal/persistence/store/humantask_store.go` (`Upsert` ~131, and the doc block
at ~129-130), `internal/persistence/store/humantask_store_conformance_test.go`.

**Why this fails today — measured.** `Upsert(State: Claimed, Claim: nil)` returns `<nil>` and reads
back `state=claimed claim=<nil>`; an `Unclaimed` row carrying a claim yields `AssignedTo("alice")=1`
**and** `ClaimableBy(alice)=1`.

- [ ] **Step 1: Write the failing conformance group** as a sibling of
`upsert_rejects_an_audit_record_with_no_timestamp` (`t.Run` at `:532`). ⚠ **Use `assert.*`, not `require.*`, for
the follow-on checks, and include the legal-shape positive control** — audit A2/B4 showed the
first version's assertions were unreachable as evidence in *both* branches (`require` is `FailNow`:
guard working ⇒ tautology; guard broken ⇒ aborts first).

```go
		t.Run("upsert_rejects_an_invalid_task", func(t *testing.T) {
			at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

			// POSITIVE CONTROL FIRST: the same fixture in a LEGAL shape must be
			// returned by both inbox queries. Without this the rejection cases below
			// prove only that a row that was never written cannot be listed — which
			// no implementation could fail.
			legal := humantask.HumanTask{
				TaskID: "tok-legal-" + b.name, InstanceID: "inst-inv", NodeID: "approve",
				State:       humantask.Claimed,
				Eligibility: authz.AuthzSpec{Roles: []string{"mgr"}},
				Candidates:  []authz.Actor{{ID: "alice"}},
				Claim:       &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at},
				CreatedAt:   at,
			}
			require.NoError(t, ts.Upsert(t.Context(), legal), "%s: legal shape must persist", b.name)
			assigned, err := ts.AssignedTo(t.Context(), "alice")
			require.NoError(t, err)
			assert.True(t, containsTask(assigned, legal.TaskID),
				"%s: control — a legally Claimed task MUST appear in AssignedTo", b.name)

			// … then the rejection cases (Claimed+nil, Unclaimed+claim,
			// out-of-range state), each asserting:
			//   assert.ErrorIs(err, humantask.ErrInvalidTask)
			//   Get -> ErrTaskNotFound
			//   the id appears in NEITHER AssignedTo NOR ClaimableBy
			// Each rejection fixture MUST declare Candidates + Eligibility.Roles
			// matching the queried actor, or the inbox assertions cannot fail on the
			// axis they name.
		})
```

- [ ] **Step 2: Verify RED (SQLite leg).**
`go test -count=1 -v -run 'TestHumanTaskStoreConformance/sqlite/upsert_rejects_an_invalid_task' ./internal/persistence/store/ > /tmp/red4.log 2>&1; echo "EXIT=$?"; grep -E '^\s*--- (PASS|FAIL)' /tmp/red4.log; grep -c "no tests to run" /tmp/red4.log`
Expected: non-zero, the rejection cases FAIL, and **`0`** for "no tests to run" — that grep, not the
absence of `---` lines, is the real proof the filter selected something.
This command boots **no containers**: element 0 of a `-run` pattern filters top-level names too,
and `forEachDialect` puts each backend behind `t.Run(b.name)`. (The "20 top-level Postgres/MySQL
tests still run" caveat applies to `-run '.*/sqlite'`, whose element 0 is `.*` — not to this
command. An earlier revision of this plan had that warning backwards.)

- [ ] **Step 3: Implement.** First statement of `Upsert`, and fix the inherited doc sentence in the
same block:

```go
// The claim/completion audit (ADR-0148 amendment 2) is normalized across typed
// columns: … The timestamps are the presence discriminators: claimed_at is NULL
// exactly when the task carries no Claim — which, by [humantask.Validate], is
// exactly when it is not Claimed; a Completed or Cancelled task may go either way.
// (The older wording "NULL exactly when the task is unclaimed" was false:
// htClaimBinds keys on the Claim pointer, never on State.)
//
// Upsert rejects a task failing [humantask.Validate] with
// [humantask.ErrInvalidTask]. For direction R1 the invariant cannot be enforced on
// read — a state='claimed' row with claimed_at NULL is indistinguishable from one
// never claimed — so the write path is the only seam (ADR-0183).
func (s *HumanTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	if err := humantask.Validate(t); err != nil {
		return err
	}
	…
```

⚠ Return it **unwrapped** by the store's `workflow-store: upsert task %s: %w` prefix — it already
names the task and the contradiction, and double-prefixing reads badly. This is a deliberate
inconsistency with the neighbouring `errZeroAuditTime` path; recorded, not accidental.

- [ ] **Step 4: Verify GREEN, all three dialects.**
`docker info > /dev/null 2>&1 && echo DOCKER=up || echo DOCKER=down; go test -count=1 ./internal/persistence/store/ > /tmp/g5.log 2>&1; echo "EXIT=$?"; tail -5 /tmp/g5.log`
If Docker is down, report that only SQLite was verified and label it partial.

- [ ] **Step 5: Commit.** `git add internal/persistence/store/ && git commit -m "feat(store): reject an invalid task on write (ADR-0183)"`

---

### Task 5 — `persistence`: the caching decorator *(Wave 2)*

Unchanged in substance from the audited version — the reasoning was **confirmed correct** by lens A.

**Files:** modify `persistence/caching_task_store.go` (~98), `persistence/caching_task_store_test.go`.

⚠ **The trap:** after Task 1 `MemTaskStore` is strict and `countingTaskStore` embeds it, so asserting
only that `Upsert` returns `ErrInvalidTask` **cannot distinguish** "the decorator validated" from
"the backing store validated" — it passes with `caching_task_store.go` untouched. Lens A confirmed
this by construction. The test must prove the backing store was never reached.

- [ ] **Step 1: Add an `upserts` counter** to `countingTaskStore` (~19) alongside `gets`, with an
`Upsert` override that increments and delegates.
- [ ] **Step 2: Add the failing row** to the existing `TestCachingTaskStore` table (match its
`assert func(t, cs, backing)` closure shape — do not add a second table): assert
`errors.Is(err, humantask.ErrInvalidTask)`, that `backing.upserts` did **not** advance, and that
`cs.Get` then returns `ErrTaskNotFound` (nothing cached).
- [ ] **Step 3: Verify RED.** `go test -count=1 -v -run '^TestCachingTaskStore$' ./persistence/ > /tmp/red5.log 2>&1; echo "EXIT=$?"` — the new case fails on `upserts went 0 -> 1`; the *error* already arrives, inherited from the strict backing store, which is exactly why the counter is the discriminator.
- [ ] **Step 4: Implement** — `if err := humantask.Validate(t); err != nil { return err }` as the
first statement, with a comment recording that it is redundant with our own stores but not with a
consumer's.
- [ ] **Step 5: Verify GREEN.** `go test -count=1 ./persistence/... ; echo "EXIT=$?"`
- [ ] **Step 6: Mutation-verify.** `cp persistence/caching_task_store.go /tmp/cts.bak`, delete the
single three-line guard block added in Step 4 leaving the rest intact, re-run Step 5 and confirm the
new case FAILS on `upserts went N -> N+1`. A mutation that fails to *compile* is not a RED; if the
case still passes, the assertion does not discriminate. Then
`cp /tmp/cts.bak persistence/caching_task_store.go && diff … && echo RESTORED`.
- [ ] **Step 7: Commit.**

---

### Task 6 — `transport/http/httpcore`: classify both sentinels *(Wave 2)*

**Consumes:** `humantask.ErrInvalidTask` (Task 1), `engine.ErrEmptyReassignTarget` (Task 2).

**Why this fails today:** neither has an arm in `ClassifyError`, so both hit `default:` → **500 with
an empty body**, and `Message` is dropped for 5xx, discarding the text naming the task and the
contradiction.

- [ ] **Step 1: Write the failing table test** over `ClassifyError`: `ErrInvalidTask` → `422` /
`conflict_state` with a non-empty `Message`; `ErrEmptyReassignTarget` → `400` / `bad_request` with a
non-empty `Message`. Include a control row asserting an unclassified error still maps to `500` /
`internal_error` with an **empty** `Message`, so the test cannot pass by classifying everything.
- [ ] **Step 2: Verify RED** — both new rows fail with `500`/`internal_error`.
- [ ] **Step 3: Implement.** Add `humantask.ErrInvalidTask` to the existing 422 `conflict_state`
arm beside `service.ErrConflict` / `engine.ErrInvalidTransition`, and
`engine.ErrEmptyReassignTarget` to the 400 arm beside `engine.ErrEmptyTriggerKey`, each with a
one-line comment giving the reason (an engine-authored shape the caller cannot fix by editing the
request vs. a caller-correctable missing field).
- [ ] **Step 4: Verify GREEN**, then check the parity suite: `go test -count=1 ./transport/http/... ; echo "EXIT=$?"`
- [ ] **Step 5: Commit.**

---

### Task 7 — `processtest`: the exported conformance helper *(Wave 2)*

**Files:** create `processtest/taskstoreconformance.go` (+ its test).

**Why:** the contract is a documented MUST on a public interface and nothing lets a consumer verify
their own store — `humantask_store_conformance_test.go` is locked inside `internal/`. `processtest`
is already the public home of `SpyAuthorizer`, `SpyCatalog`, `CaptureSender`.

- [ ] **Step 1: Write the failing test** — `RunTaskStoreConformance` must PASS for
`humantask.NewMemTaskStore` and FAIL for a deliberately permissive in-test store. Use a
`*testing.T` recorder so "fails for a permissive store" is asserted rather than asserted-by-crashing.
- [ ] **Step 2: Verify RED** (`undefined: processtest.RunTaskStoreConformance`).
- [ ] **Step 3: Implement:**

```go
// RunTaskStoreConformance verifies that a [humantask.TaskStore] implementation
// upholds the contract documented on TaskStore.Upsert (ADR-0183): every task
// failing [humantask.Validate] is rejected with [humantask.ErrInvalidTask], and a
// rejected write persists nothing.
//
// newStore must return a fresh, empty store on each call. Consumers embedding
// wrkflw with their own TaskStore should call this from their own test suite:
//
//	func TestMyStoreConformance(t *testing.T) {
//	    processtest.RunTaskStoreConformance(t, func(t *testing.T) humantask.TaskStore { … })
//	}
func RunTaskStoreConformance(t *testing.T, newStore func(t *testing.T) humantask.TaskStore) {
	t.Helper()
	// One subtest per invalid shape: Claimed+nil, Unclaimed+claim, out-of-range
	// state. Each asserts ErrInvalidTask and then Get -> ErrTaskNotFound. Plus
	// legal-shape controls that MUST persist and be readable — including the
	// ADR-0148 kiosk shape (Claimed + empty claimant) — so a store that rejects
	// everything cannot pass, and one that over-rejects the kiosk shape fails.
}
```

- [ ] **Step 4: Verify GREEN**, and that it passes for all three bundled stores (`MemTaskStore`,
a SQLite-backed `HumanTaskStore`, and a `CachingTaskStore` wrapping one).

⚠ **AMENDED AT REVIEW (`/code-review` finding 4).** The factory takes the CASE's `*testing.T`
(`func(t *testing.T) humantask.TaskStore`), shown above as amended. A parameterless factory forces
the documented `newTestDB(t)` pattern to capture the parent `T`; its `FailNow` then crosses
goroutines and surfaces as `test executed panic(nil) or runtime.Goexit`, truncating the run at the
first shape. See ADR-0183 Decision 6 for the measurement.

⚠ **AMENDED AT REVIEW (`/code-review` finding 3).** The rejected-write leg asserts the row reached
neither `AssignedTo` nor `ClaimableBy` in addition to `Get`, mirroring the internal conformance
group; the negative leg gained a `leakyRollbackTaskStore` (rejects, hides from `Get`, leaks into
the inboxes) so the new assertions are provably discriminating, and an `inboxFailingTaskStore` so
the "the query must still answer" halves are too. The stand-ins that stubbed both queries as
`nil, nil` were reworked and every pinned failure count re-derived per shape.
- [ ] **Step 5: Commit.**

---

### Task 8 — CHANGELOG and delivery *(Wave 3, controller)*

- [ ] **Step 1: Three breaking entries** under the existing
`### Breaking changes (pre-v0.1.0 — no stability promise)` heading (`CHANGELOG.md:18`):
`Upsert` now rejects contradictory tasks (naming `ErrInvalidTask`, all three bundled stores, the
interface contract, and `processtest.RunTaskStoreConformance`); a reassignment with an empty `to` now
fails at `Step` with 400 (`ErrEmptyReassignTarget`); `ErrInvalidTask` reaching HTTP is 422 not 500.
Must additionally state: **`processtest/harness.go:345` exposes `Tasks() *humantask.MemTaskStore`**,
so consumer fixtures seeded through it break; for a consumer's **own** `TaskStore` the break is
**silent** (no signature change, so nothing recompiles differently); zero churn was measured over
*this* repo only; and `Unclaimed` being the zero value means a decode that dropped only `State` is
now rejected. ⚠ State plainly that an **empty claimant remains legal** (ADR-0148's kiosk shape) and
that the empty-ID **claim** route is deliberately untouched — only the empty *reassignment target*
is refused.

- [ ] **Step 2: Fold into one feature commit.**
`git log --oneline main..HEAD` then ⚠ **check `git merge-base HEAD main` before any soft reset** — a
`git reset --soft main` on a branch cut from an older `main` stages a REVERT.
`git reset --soft $(git merge-base HEAD main) && git commit -m "feat(humantask): a human task's claim invariant is enforced before commit (ADR-0183)"`

- [ ] **Step 3: Verification 1 — tests + coverage.**
`docker info > /dev/null 2>&1 && echo DOCKER=up || echo DOCKER=down; go test -race -coverprofile=cover.out ./... > /tmp/cov.log 2>&1; echo "EXIT=$?"; scripts/coverage.sh cover.out`
≥ 85% over hand-written code. ⚠ `scripts/coverage.sh` only **reports** — read the number, its exit
code proves nothing. Hot paths and their failure branches before the percentage.

- [ ] **Step 4: Verification 2 — no regressions.** `go test ./... ; echo "EXIT=$?"`

- [ ] **Step 5: Verification 3 — lint, repo-wide.**
`command -v golangci-lint && golangci-lint run ./... ; echo "EXIT=$?"` — never substitute `go vet`;
never claim "lint clean" for a run that did not execute.

- [ ] **Step 6: Documents describe what shipped.** Re-read spec, ADR and this plan against the built
code; amend the ADR in-bundle for anything implementation refuted, with the measurement (rule #11).
Sweep the diff's comments for unexecuted claims and over-reaching quantifiers. Update this
`▶ Progress` block and `docs/plans/HANDOVER.md`.

- [ ] **Step 7: Owner-only gates.** Ask the owner to run `/code-review` and `/security-review`. Fix
all findings, folding via `--amend`. State every adjudication explicitly — silence is not one.

- [ ] **Step 8: Merge and push**, delete the branch, and file the deferred items **exactly as the spec's canonical `## Deferred` section lists them**
(five items) — do not re-derive the list here.

## Verification Checklist

- [ ] `Validate` enforces R1 (nil claim only), R2, R3; 9 rows pass (`PASS=10` incl. the parent); the
      two deferral rows pin `Completed`/`Cancelled` as deliberately unconstrained, and the kiosk row
      pins `Claimed`+empty claimant as legal.
- [ ] **A pre-commit rejection commits NOTHING** — same Version, `incidents=0`, no token parked.
      Mutation-verified.
- [x] ⚠ **B8 refuted**: the sweep normalizes every task to `Cancelled`, so no rejection can arise there. Replaced by a test that the hook does not BLOCK the sweep, plus whole-step-refusal unit rows.
- [ ] All three `Upsert` implementations reject every invalid shape and persist nothing.
- [ ] `CachingTaskStore` rejects **before delegating**, proven via `backing.upserts`.
- [ ] The conformance group has a **legal-shape positive control** and uses `assert.*`, so the
      inbox assertions are reachable as evidence.
- [ ] `Step` refuses `HumanReassigned{To: ""}` before `cloneState`, and still accepts a real target.
- [ ] `ClassifyError`: 422 for `ErrInvalidTask`, 400 for `ErrEmptyReassignTarget`, 500 control intact.
- [ ] `processtest.RunTaskStoreConformance` passes for all three bundled stores and fails for a
      permissive one.
- [ ] Both false comments corrected (`step_nodes.go:755`, the ADR-0148 doc sentence).
- [ ] One feature commit; `merge-base` checked before any soft reset.
- [ ] Verification 1–3 executed and reported as such.
- [ ] Owner ran both gates; all findings fixed or adjudicated with reasons.
