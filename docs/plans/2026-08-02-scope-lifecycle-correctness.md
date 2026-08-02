# Scope-lifecycle correctness — implementation plan (delivery 2a)

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development`
> to implement this plan phase-by-phase. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the engine defects whose shared root cause is that teardown
enumerates one level where the thing being torn down is a tree — a scope tree and
a token→task→incident chain. The eight-terminal-transition half of the original
bundle is **delivery 2b**, parked on `parked/terminal-transitions`.

**Architecture:** Three helpers replace three ad-hoc enumerations.
`descendantScopeIDs` + `cancelScopeSubtree` + `tokensInScopeSubtree` make the
scope **subtree** the unit of teardown (ADR-0162). `cancelTokenWaits` gains the
token's open human task and its incidents, so one edit fixes all four of its call
sites (ADR-0163). The terminal-transition unification and the resume guard are
**delivery 2b** (ADR-0164).

**Tech Stack:** Go 1.25, `engine/` only. No new dependency, no storage, no
transport, no public API **signature** change. Tests are black-box
(`package engine_test`) with white-box shims added to the existing
`engine/export_test.go`.

## ▶ Progress

| | |
|---|---|
| Branch | `feat/scope-lifecycle-correctness` (off `main` @ `17e148b`) |
| Status | **IMPLEMENTED — all six phases landed; ready for the Delivery Gate.** Was: **READY TO IMPLEMENT.** Design bundle written; rule-#9 audit COMPLETE (round 2, all three briefs, 31 findings); **every accepted finding is now folded into the phase steps** (2026-08-03) — B1–B3, C2, C4, T1, T2, T5, D1–D7 here; C1, C3, T3, T4, O1 and C4's resume-guard half routed to delivery 2b, which already carries them. Start at Phase 1 |
| Phases landed | **all six.** 1 (ADR-0163 task+incident cleanup) · 2 (scope-tree helpers) · 3 (both abnormal teardowns route through the subtree) · 4 (drain checks + the unguarded `:283` exit) · 5 (exported godoc) · 6 (verification) |
| Verification | `engine` **91.6%** coverage with `-race` (floor 85; ADR-0161 baseline 90.8% — improved), `golangci-lint run ./engine/...` **0 issues**, **16/16 mutations produced their predicted failure**, `go doc -all ./engine` 108 declarations before and after (no exported signature changed), `grep 'UpdateTask{Task: \*' engine/*.go` → **0 matches** (ADR-0163's own checkable claim) |
| Reviews | six task reviews + one final whole-branch review, all on independent agents. Three tasks needed one fix round each. The final review found **zero code defects** and approved the executable change as-is; its two Important findings were both documentation-record defects |
| Still to run | Step 6.3 (repo-wide suite on the **merged** tree — owner approved Docker for that run), Step 6.4 (this handover), Step 6.5 (owner-run `/code-review` + `/security-review`, then merge `--no-ff` and push) |
| Findings folded | 2026-08-03. Each row of the round-2 tables below now names the step or document section that carries it |
| Pre-implementation scan | 2026-08-03, owner-decided: (a) Phase 4's fourth hand-rolled child-scan loop is replaced by a shared `hasChildScopeWithTokens` predicate produced in **Phase 2** and called at all four exits — ADR-0162 Decision point 5 carries the call table; (b) Step 1.9b keeps 2 deep-copy tests + the grep for the other three emitters, as written |
| Spec | `docs/specs/2026-08-02-scope-lifecycle-correctness.md` |
| ADRs | `0162-scope-teardown-cascades-to-descendants.md`, `0163-cancelling-a-token-cancels-its-task.md`. ADR-0164 moved to delivery 2b |
| Delivery 2b | `parked/terminal-transitions` @ `18f1aa9` — ADR-0164 + the ADR-0109 correction + `docs/plans/2026-08-02-terminal-transitions.md`. Rebase onto the new `main` after 2a merges |
| Reproduced defects | §1.5 (task orphaned by boundary interrupt) and §1.9 (partial rollback resurrects a terminal instance) were confirmed by running throwaway tests against the real engine; their fixtures and captured output are in the spec |

### Phase 4 known gaps and second-order findings — landed

**A second real defect, found by Phase 4's mutation sweep and not named anywhere
in the design bundle.** `exitRootEventSubprocessScope`'s direct-child scan meant
a root-level event sub-process's arms were **silently retired while the instance
was still working** — the scan saw only the empty `outer` scope, fell through to
`removeEventTriggeredSubprocessArmsForScope("")`, and the arm count went 1 → 0.
A later signal would then never re-trigger the event sub-process.
`TestRootEventSubprocessExitSeesGrandchildOfRoot` is its sole observer. The
widened check fixes it as a side effect; nobody predicted it.

**A newly reachable error path, accepted.** With the `:283` guard in place, an
event-sub-process scope `C` can now survive its own end event while a descendant
runs. If that descendant is itself a nested non-interrupting event sub-process,
its exit routes through `exitNestedEventSubprocessScope` with
`parentScopeID == C`, and the resume looks up `C.NodeID` — an event-sub-process
node, which carries no outgoing sequence flows — producing a hard error
("enclosing node %q has no outgoing flows in grandparent definition").
**Pre-fix, that same topology was a silent permanent wedge**, so an error is
strictly an improvement: it is loud, recoverable and diagnosable where the
wedge was none of those. Recorded rather than fixed — a nested non-interrupting
event sub-process inside an event sub-process is not a topology this delivery
set out to support, and inventing resume semantics for it belongs in its own
ADR.

**Two `exceptID` arguments are dead by construction** (`step_nodes.go:317`,
`:361`). Both call sites are reached only *after*
`exitEventSubprocessScope` has already run `closeScope(currentScopeID)`, so the
excepted scope is no longer in `s.Scopes` and the filter can never fire on it.
They are harmless and defensive, and the plan's call table mandates them — but
they are the reason those two sites can never be fully mutation-pinned. Recorded
so a later sweep does not rediscover this as a gap.

### Phase 1 adjudications (ADR-0163) — landed

Four expectation moves, **all category 1 (moved, not regressions)**; no
implementation was changed in response to any of them. All four are in the
compensation family and share one cause: `beginCompensation` already consumed
the token parked on a `UserTask` before this delivery — it just left the task
open behind it, so the new `UpdateTask` is the fix surfacing on a path no test
covered.

| test | move | why it is category 1 |
|---|---|---|
| `TestCompensateRequestedRollsBackInReverseOrder` (`step_compensation_test.go:532`) | 1 → 2 commands: `UpdateTask{cancelled}` then `InvokeAction{c3}` | The command *set* still holds exactly one compensation `InvokeAction`, still `c3`, still reverse order — the invariant the test exists to pin. Rewritten through a new `requireCompensationStart` helper that asserts the shape exactly, so it now pins **strictly more** than the old `require.Len(cmds, 1)` |
| `TestCompensateRequestedFullRollback` (`:589`) | identical | same call site, same helper |
| `TestArchiveCompensationOrderingReversed` (`:728`) | identical | same call site; the archive-consolidation ordering assertion is untouched |
| `TestFailureWithCompensationReconcilesOpenTasks` (`step_fail_tasks_test.go:162`) | the `UpdateTask` moved **one step earlier**, walk-finish → walk-begin | Reconciliation now happens when the walk *begins* rather than when it *finishes*, because `stepCompensationFinish`'s sweep finds nothing open. Verified present in the earlier step rather than assumed; the test now asserts **both** ends — one `UpdateTask` at `r2` **and** none at `r3` — which is what makes "moved, not lost" checkable |

The brief predicted a positional move on `handleCancelRequested`'s compensation
path. `TestCancelWithCompensationReconcilesOpenTasks` did **not** fail, because
it only counts `UpdateTask`s rather than asserting positions — the predicted
move was real but invisible to that assertion. Recorded as a deferred minor:
the ADR-0088 ordering this delivery changes is pinned by no test.

**Step 1.12b — `taskCancelCmds` DELETED.** No expectation moved. Proven
empirically, not reasoned: mutation-disabling the task branch in
`cancelTokenWaits` drops both compensation task tests to zero `UpdateTask`s,
which is what establishes it as the sole producer. `engine/step_triggers.go:231`
was correctly left alone. The review found stronger evidence still — the
walk-end sweep at `engine/step_compensation.go:781` is retained, so even a
persisted state carrying a pre-fix leaked task is reconciled.


## Global Constraints

- **Go 1.25.** `engine/` must stay pure of transport, storage vendor and
  event-bus specifics, and must not import `clockwork` — enforced by
  `engine/purity_test.go`.
- **TDD strict.** No production code before an observed failing test. Every new
  symbol and every behavioural change needs a visible RED state in the
  transcript: a `Bash` call running `go test` that fails, *between* the test
  write and the implementation write. See CLAUDE.md "TDD Operational Discipline".
- **Prefer black-box tests** (`package engine_test`). Unexported helpers are
  reached through the shim pattern already established in
  `engine/export_test.go` — thin named forwarders, no logic.
- **Table tests** follow the project `table-test` skill: `assert` closure form,
  not `want`/`wantErr` fields; `t.Context()` over `context.Background()`.
- **Error sentinels** use the `workflow-<pkg>: …` prefix.
- **Coverage floor 85%** on `engine`. Two commands, two purposes — do not
  conflate them (audit finding D6): the **`engine`-only** measurement at Step 6.2
  is `go test -race -coverprofile=cover.out ./engine/...` plus
  `go tool cover -func`, needs **no Docker**, and is the one you run while
  iterating; CLAUDE.md Verification §1's repo-wide
  `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
  runs once at Step 6.3 **after asking the owner**, because it starts containers.
  It is a floor, not a target: every hot path and its failure branches come
  first.
- **One feature bundle, one commit.** Implementation, tests, spec, ADRs and this
  plan land as a single commit, amended as work proceeds. Never stack fixup
  commits.

## Execution constraint — do NOT fan out

Every file in this delivery is in the **same Go package** (`engine`). Concurrent
subagents sharing one working tree break each other's `go test` compile
mid-edit even when they own disjoint files — this is a recorded lesson from
ADR-0159 Stage 2. Dispatch **one subagent at a time**, review its diff, then
dispatch the next. Fan-out is only correct across packages, and there is no
second package here.

## Phase ordering rationale

Phase 1 (ADR-0163) comes first even though ADR-0162 is the headline. Phase 3
routes both abnormal teardowns through `cancelTokenWaits`; if `cancelTokenWaits`
changes *after* those tests are written, every command-sequence expectation in
Phase 3 has to be rewritten. Stabilising `cancelTokenWaits` first means each
later phase is written against final behaviour exactly once.

| phase | ADR | defects / findings | depends on |
|---|---|---|---|
| 1 | 0163 | 5, 7, 8; C2, D3 | — |
| 2 | 0162 | (helpers) | — |
| 3 | 0162 | 2, 3, 4; D7 | 1, 2 |
| 4 | 0162 | 1, 10; B3 | 2 |
| 5 | 0162 | C4 (godoc) | 3, 4 |
| 5b | 0164 | *moved to delivery 2b* | — |
| 6 | — | verification | all |

## File structure

| file | responsibility | change | phase |
|---|---|---|---|
| `engine/step_cancel.go` | per-token teardown sweep | modify `cancelTokenWaits`; add `cancelScopeSubtree` | 1, 3 |
| `engine/state.go` | instance state + task/incident accessors | `cancelOpenTasks` clones; add `removeIncidentsForToken` | 1 |
| `engine/step_timers.go` | deadline breach | `UpdateTask` clones (`:90`) | 1 |
| `engine/step_triggers.go` | task lifecycle triggers + cancel | `UpdateTask` clones (`:379,411,428,628`); delete the dead `taskCancelCmds` prepend (`:211`) | 1 |
| `engine/state_compensation.go` | scope tree + compensation records | add `descendantScopeIDs`, `closeScopeDescendants`, `tokensInScopeSubtree`, `hasChildScopeWithTokens`; refactor `closeScope` | 2 |
| `engine/step_eventsubprocess.go` | event-sub-process arm firing | interrupting branch routes through `cancelScopeSubtree` + `closeScopeDescendants` | 3 |
| `engine/step_errors.go` | error propagation | `consume` closure routes through `cancelScopeSubtree` | 3 |
| `engine/step_nodes.go` | node strategies + scope exits | 4 scope exits call `hasChildScopeWithTokens` (3 replace hand-rolled loops, `:283` gains the check it never had); `:283` also gains an archive; `:372` archives | 4 |
| `engine/command.go` | `Compensate` godoc | "closes normally" → every scope close archives | 5 |
| `engine/trigger.go` | `CompensateRequested` godoc | same, plus the stale ADR-0013 "hoist" wording ADR-0039 already reversed | 5 |
| `engine/export_test.go` | white-box shims | add forwarders for the new unexported helpers | 1, 2 |
| `engine/step_cancel_tasks_test.go` | *(exists, `engine_test`)* | Phase 1 boundary + error-path task tests | 1 |
| `engine/state_incidents_test.go` | **new** (`engine_test`) | incident helper, deep-copy tests | 1 |
| `engine/state_compensation_test.go` | **new** | Phase 2 helper tests | 2 |
| `engine/step_eventsubprocess_test.go` | **new** | Phase 3 interrupt-teardown tests | 3 |
| `engine/step_scope_drain_test.go` | **new** | Phase 4 wedge tests (both sites) | 4 |

⚠ `engine/step_cancel_test.go` and `engine/state_test.go` are **`package engine`**
(white-box) and are deliberately **not** in this table — see Step 1.1. The
`engine/` directory mixes `engine` and `engine_test` files with no naming signal;
`head -1` every test file before writing into it.

---

## Phase 1 — ADR-0163: cancelling a token cancels its task and incidents

**Files:**
- Modify: `engine/step_cancel.go:12-30` (`cancelTokenWaits`)
- Modify: `engine/state.go:297-306` (`cancelOpenTasks`)
- Create: `removeIncidentsForToken` in `engine/state.go`
- Modify: `engine/export_test.go` (add `RemoveIncidentsForToken` shim)
- Test: `engine/step_cancel_test.go`, `engine/state_test.go`

**Interfaces:**
- Produces: `func (s *InstanceState) removeIncidentsForToken(tokenID string)` —
  drops every incident whose `TokenID == tokenID`, order-preserving.
- Produces: `cancelTokenWaits` now returns `UpdateTask` commands for the token's
  open human task. Phases 3 and 5 depend on this.

- [x] **Step 1.1: Write the failing test for the reproduced boundary leak**

⚠ **Append to `engine/step_cancel_tasks_test.go`, which is `package engine_test`.**
Do **not** append to `engine/step_cancel_test.go` — that file is `package engine`
(white-box; it calls the unexported `cancelTokenWaits` directly at `:45`), so it
cannot see `engine_test` fixtures and the `engine.` qualifier does not resolve
inside it. `engine/step_cancel_tasks_test.go` already imports `humantask` and
already defines a `findUpdateTasks(cmds)` helper — **use it** instead of the
inline loop below.

The fixture `interruptingMessageBoundaryDef()` is at
`engine/step_boundaries_test.go:49-67`, also `package engine_test`, so it is
visible from there.

```go
// TestInterruptingBoundaryCancelsHostTask asserts that an interrupting boundary
// firing on a UserTask host in a LATER step than the one that minted the task
// closes that task: the record goes Cancelled and exactly one UpdateTask is
// emitted. Before ADR-0163 the host token was consumed while the task stayed
// unclaimed and open, leaving an inbox entry on a still-running instance that
// no token could ever complete.
func TestInterruptingBoundaryCancelsHostTask(t *testing.T) {
	t.Parallel()

	def := interruptingMessageBoundaryDef()
	t0 := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tasks, 1, "setup: step 1 mints the task")
	require.True(t, r1.State.Tasks[0].IsOpen(), "setup: the task starts open")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewMessageReceived(t0.Add(time.Hour), "cancel", "", nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.State.Tasks, 1)
	assert.Equal(t, humantask.Cancelled, r2.State.Tasks[0].State,
		"the interrupted host's task must be cancelled")
	assert.False(t, r2.State.Tasks[0].IsOpen())

	updates := findUpdateTasks(r2.Commands) // existing helper in this file
	require.Len(t, updates, 1, "exactly one UpdateTask reconciles the task store")
	assert.Equal(t, humantask.Cancelled, updates[0].Task.State)
}
```

Check `findUpdateTasks`'s actual return type before writing the last line — it
may return `[]engine.UpdateTask` or `[]humantask.HumanTask`. `humantask`,
`require`, `assert` and `engine` are all already imported in that file.

- [x] **Step 1.2: Run it and observe RED**

```bash
go test -run '^TestInterruptingBoundaryCancelsHostTask$' ./engine/ ; echo "EXIT=$?"
```

Expected: FAIL — `Equal: expected cancelled, actual unclaimed`, and
`Len: expected 1 item, got 0` for the `UpdateTask` slice. A non-zero `EXIT` is
the evidence. Do **not** proceed until you have seen this output.

- [x] **Step 1.2b: Write the failing test for defect 7 — the error-boundary path**

Audit finding T2: defect 7 is named in the spec and claimed by this phase in the
self-review, but had no step of its own. It is a **different call site** from
Step 1.1's (`engine/step_errors.go:389`, not `engine/step_boundaries.go:146`)
and a different observable: the task is left open on a **completed** instance,
not a running one. Step 1.1 passing does not imply this passes.

It is also the defect that pins ADR-0161's recorded asymmetry. Collapsed into a
single step, the same topology yields `Cancelled` already, because ADR-0161's
stale-command filter catches it; spread across two steps it does not. The test
must therefore drive the boundary in a **later** step than the one that minted
the task, or it asserts the behaviour that already works.

Append to `engine/step_cancel_tasks_test.go` (`package engine_test`):

```go
// TestErrorBoundaryTeardownCancelsUserTaskAcrossStepBoundary asserts that an
// error boundary tearing down an enclosing sub-process scope cancels a UserTask
// parked on a sibling branch inside that scope. Before ADR-0163 the task stayed
// unclaimed and open on a COMPLETED instance, and whether it did depended on
// step granularity: ADR-0161's stale-command filter cancels it when the mint and
// the teardown collapse into one step, and nothing cancels it when they do not.
// Step granularity is not a property a definition author can see or control.
func TestErrorBoundaryTeardownCancelsUserTaskAcrossStepBoundary(t *testing.T) {
	// 1. Build outer[ fork ⇒ { review: UserTask, work: ServiceTask } ] with an
	//    error boundary on the sub-process. Model the construction on
	//    engine/step_subprocess_test.go.
	// 2. Step 1: start, drive until the UserTask is parked and open.
	//    require the task IsOpen() — this is setup, and if it is already closed
	//    the topology is wrong and the test proves nothing.
	// 3. Step 2 (a SEPARATE Step call — the granularity is load-bearing):
	//    fail "work" with the boundary's error code.
	// 4. Assert the boundary fired, the instance reached its terminal state, and
	//    the task is humantask.Cancelled with exactly one UpdateTask emitted for
	//    it in step 2.
}
```

- [x] **Step 1.2c: Run it and observe RED**

```bash
go test -run '^TestErrorBoundaryTeardownCancelsUserTaskAcrossStepBoundary$' ./engine/ ; echo "EXIT=$?"
```

Expected: FAIL — the task is still `unclaimed`/open and zero `UpdateTask` is
emitted. ⚠ If it passes on the first run, the two actions collapsed into one
`Step` and ADR-0161's filter handled it: split the drive into more steps until
you observe the failure, because a green-from-the-start test here certifies
nothing.

- [x] **Step 1.3: Write the failing test for incident cleanup**

⚠ Create a **new** `engine/state_incidents_test.go` with `package engine_test`.
Do **not** append to `engine/state_test.go` — it is `package engine`, so the
`engine.` qualifier and the `export_test.go` shims below are unusable there.

```go
// TestRemoveIncidentsForTokenDropsOnlyThatToken asserts the helper retires the
// incidents raised against one token and leaves every other record in its
// original relative order.
func TestRemoveIncidentsForTokenDropsOnlyThatToken(t *testing.T) {
	t.Parallel()

	s := &engine.InstanceState{
		InstanceID: "i1",
		Incidents: []engine.Incident{
			{ID: "inc1", TokenID: "tokA", NodeID: "n1"},
			{ID: "inc2", TokenID: "tokB", NodeID: "n2"},
			{ID: "inc3", TokenID: "tokA", NodeID: "n3"},
			{ID: "inc4", TokenID: "tokC", NodeID: "n4"},
		},
	}

	engine.RemoveIncidentsForToken(s, "tokA")

	got := make([]string, 0, len(s.Incidents))
	for _, inc := range s.Incidents {
		got = append(got, inc.ID)
	}
	assert.Equal(t, []string{"inc2", "inc4"}, got,
		"tokA's incidents are dropped; the rest keep their order")
}

// TestRemoveIncidentsForTokenIgnoresEmptyID asserts an empty token ID matches
// nothing, per ADR-0152: an empty key names nothing and must never be treated
// as a wildcard.
func TestRemoveIncidentsForTokenIgnoresEmptyID(t *testing.T) {
	t.Parallel()

	// ⚠ The blank-keyed incident is load-bearing. With only a "tokA" record the
	// test passes with OR without the guard, because slices.DeleteFunc would
	// evaluate "tokA" == "" as false and delete nothing either way — a test that
	// certifies nothing. inc2 is the record the MISSING guard would wipe.
	// (Corrected 2026-08-03 after the Task 1 review; the original fixture here
	// was vacuous.)
	s := &engine.InstanceState{
		InstanceID: "i1",
		Incidents: []engine.Incident{
			{ID: "inc1", TokenID: "tokA"},
			{ID: "inc2", TokenID: ""},
		},
	}

	engine.RemoveIncidentsForToken(s, "")

	assert.Len(t, s.Incidents, 2,
		"an empty token ID matches nothing, not even a blank-keyed incident")
}
```

- [x] **Step 1.4: Run it and observe RED**

```bash
go test -run '^TestRemoveIncidentsForToken' ./engine/ ; echo "EXIT=$?"
```

Expected: build failure — `undefined: engine.RemoveIncidentsForToken`. That
compile error is a valid RED state.

- [x] **Step 1.5: Write the failing test for the aliasing fix**

Append to the same new `engine/state_incidents_test.go` (`package engine_test`):

```go
// TestCancelOpenTasksEmitsDeepCopy asserts the UpdateTask handed to a
// consumer-supplied TaskStore does not alias the record committed as instance
// state. TaskStore is public API; a store that retains the value verbatim would
// otherwise share the Claim pointee and the Vars map with live engine state.
func TestCancelOpenTasksEmitsDeepCopy(t *testing.T) {
	t.Parallel()

	claimedAt := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	s := &engine.InstanceState{
		InstanceID: "i1",
		Tasks: []humantask.HumanTask{{
			TaskID:     "i1-h1",
			InstanceID: "i1",
			NodeID:     "review",
			State:      humantask.Claimed,
			Claim:      &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: claimedAt},
			Vars:       map[string]any{"k": "v"},
		}},
	}

	cmds := engine.CancelOpenTasks(s)
	require.Len(t, cmds, 1)
	emitted, ok := cmds[0].(engine.UpdateTask)
	require.True(t, ok)

	// Mutate through the emitted command, as a retaining store would.
	emitted.Task.Claim.Actor.ID = "mallory"
	emitted.Task.Vars["k"] = "tampered"

	assert.Equal(t, "alice", s.Tasks[0].Claim.Actor.ID, "Claim pointee must not be shared")
	assert.Equal(t, "v", s.Tasks[0].Vars["k"], "Vars map must not be shared")
}
```

If `engine.CancelOpenTasks` does not already exist in `engine/export_test.go`,
add the shim in the same edit as the other shims in Step 1.7.

⚠ The claim-time field is **`At`, not `ClaimedAt`** — `humantask/humantask.go:59-64`
is `type Claim struct { Actor authz.Actor; At time.Time }`. The `HumanTask`
struct is `humantask/humantask.go:89-120` and does not contain `Claim`'s
definition; read both.

- [x] **Step 1.6: Run it and observe RED**

```bash
go test -run '^TestCancelOpenTasksEmitsDeepCopy$' ./engine/ ; echo "EXIT=$?"
```

Expected: FAIL — `expected "alice", actual "mallory"`. If it instead fails to
build on `engine.CancelOpenTasks`, add that shim first, re-run, and confirm you
then see the *assertion* failure before implementing.

- [x] **Step 1.7: Add the export shims**

In `engine/export_test.go`, following the file's existing thin-forwarder
pattern:

```go
// RemoveIncidentsForToken exposes (*InstanceState).removeIncidentsForToken for engine_test.
func RemoveIncidentsForToken(s *InstanceState, tokenID string) {
	s.removeIncidentsForToken(tokenID)
}

// CancelOpenTasks exposes (*InstanceState).cancelOpenTasks for engine_test.
func CancelOpenTasks(s *InstanceState) []Command {
	return s.cancelOpenTasks()
}
```

- [x] **Step 1.8: Implement `removeIncidentsForToken`**

In `engine/state.go`, next to the other `InstanceState` helpers:

```go
// removeIncidentsForToken drops every incident raised against tokenID. An empty
// tokenID matches nothing (ADR-0152: an empty key names nothing, and admitting
// it would make every token with a blank ID wipe the incident list). The
// remaining records keep their relative order so command output stays
// deterministic.
func (s *InstanceState) removeIncidentsForToken(tokenID string) {
	if tokenID == "" {
		return
	}
	s.Incidents = slices.DeleteFunc(s.Incidents, func(inc Incident) bool {
		return inc.TokenID == tokenID
	})
}
```

Add `"slices"` to the import block if it is not already there.

- [x] **Step 1.9: Implement the `cancelOpenTasks` clone**

In `engine/state.go:297-306`, change the emitted value only:

```go
func (s *InstanceState) cancelOpenTasks() []Command {
	var cmds []Command
	for i := range s.Tasks {
		if s.Tasks[i].IsOpen() {
			s.Tasks[i].State = humantask.Cancelled
			// Clone before the record escapes: the command is handed to a
			// consumer-supplied TaskStore while the record it was built from is
			// committed as instance state, so a shallow copy would share the
			// Claim/Completion pointees, the Vars map and the actor slices
			// across that boundary (ADR-0163). HumanTask.Clone is the single
			// deep-copy definition for a task.
			cmds = append(cmds, UpdateTask{Task: s.Tasks[i].Clone()})
		}
	}
	return cmds
}
```

- [x] **Step 1.9b: Sweep the five other shallow `UpdateTask` emitters**

Audit finding C2. ADR-0163 claims *"No `UpdateTask` hands a consumer store a
value aliasing committed engine state, **on any path**"*. Fixing only
`cancelOpenTasks` leaves that claim false at five sites, each of which
dereferences a `*humantask.HumanTask` pointing straight into `s.Tasks`:

| site | path | change |
|---|---|---|
| `engine/step_timers.go:90` | deadline breach | `UpdateTask{Task: *task}` → `UpdateTask{Task: task.Clone()}` |
| `engine/step_triggers.go:379` | task claimed | same |
| `engine/step_triggers.go:411` | task released | same |
| `engine/step_triggers.go:428` | task reassigned | same |
| `engine/step_triggers.go:628` | task completed | same |

`engine/step_timers.go:90` is the sharpest of the five: it is one of only three
places the engine writes `humantask.Cancelled`, so it emits an aliased record on
exactly the kind of teardown path this ADR is about.

RED first, in `engine/state_incidents_test.go` (`package engine_test`), by the
same shape as Step 1.5 — drive one deadline breach and one task claim through
`engine.Step`, mutate `Claim.Actor.ID` and `Vars` through the emitted command,
and assert the committed `r.State.Tasks[0]` is unchanged. Two tests are enough:
the timer path and one `step_triggers.go` path. The other three are the identical
one-token edit and are covered by the grep below rather than by three
near-duplicate tests — a deliberate, stated trade, not an omission.

Verification, which is what makes the ADR's claim checkable rather than asserted:

```bash
grep -rn 'UpdateTask{Task: \*' engine/*.go | grep -v _test.go ; echo "MATCHES=$?"
```

Required: **no matches** (`MATCHES=1`, grep's no-match exit). A later seventh
emitter then fails this grep instead of silently re-introducing the aliasing.

- [x] **Step 1.10: Implement the `cancelTokenWaits` change**

In `engine/step_cancel.go`, between the event-gateway sweep and the token
consumption. Note the ordering requirement: both new steps read `tok` fields, so
they must run **before** `consumeTokenAs`.

```go
	// Cancel any event-gateway arms.
	if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
		cmds = appendCancelTimers(cmds, s.removeArmedEventsForGateway(tok.ID))
	}
	// An open human task is a wait attached to this token (ADR-0163).
	// AwaitCommand is the taskID for a UserTask (step_nodes.go:679) and a
	// command ID otherwise, where TaskByID returns nil — the same assumption
	// cancelTimersByTaskID already makes above, so this is a natural no-op for
	// non-task tokens.
	//
	// Clone before the record escapes: the command is handed to a
	// consumer-supplied TaskStore while the record it was built from is
	// committed as instance state.
	if task := s.TaskByID(tok.AwaitCommand); task != nil && task.IsOpen() {
		task.State = humantask.Cancelled
		cmds = append(cmds, UpdateTask{Task: task.Clone()})
	}
	// An incident names the token that failed (Incident.TokenID). Cancelling
	// that token must retire it, or it stays visible on a completed or
	// terminated instance with nothing left to resolve.
	s.removeIncidentsForToken(tok.ID)

	tokPtr := s.tokenByID(tok.ID)
	if tokPtr != nil {
		s.consumeTokenAs(tokPtr, at, closeKind)
	}
	return cmds
```

Add `"github.com/kartaladev/wrkflw/humantask"` to the import block.

Also update the function's doc comment — it currently enumerates what it cancels
and must now mention the task and incidents.

- [x] **Step 1.11: Run every new test in this phase and observe GREEN**

```bash
go test -run '^TestInterruptingBoundaryCancelsHostTask$|^TestErrorBoundaryTeardownCancelsUserTaskAcrossStepBoundary$|^TestRemoveIncidentsForToken|^TestCancelOpenTasksEmitsDeepCopy$|^TestDeadlineBreach.*DeepCopy$|^TestTaskClaim.*DeepCopy$' ./engine/ ; echo "EXIT=$?"
```

Expected: PASS, `EXIT=0`. Adjust the last two alternatives to whatever you named
the Step 1.9b tests.

- [x] **Step 1.12: Run the whole engine package and triage the fallout**

```bash
go test ./engine/ 2>&1 | tail -40 ; echo "EXIT=$?"
```

Expect failures. Every interrupt path now emits an extra `UpdateTask`, so
command-sequence assertions move. **Inspect each one individually.** For each
failing test, decide and record which it is:

1. *Expectation moved* — the test asserted an exact command slice and the new
   `UpdateTask` is correct. Update the expectation and note it.
2. *Real regression* — the new command is wrong for that path (e.g. a task
   cancelled that should have stayed open). Fix the implementation, not the test.

A mechanical re-baseline defeats the purpose of these tests. Write the
adjudication for each changed expectation into the Progress block of this plan.

⚠ **One expectation move is predicted and is category 1 — do not "fix" it.**
`beginCompensation` (`engine/step_compensation.go:226`) calls `cancelTokenWaits`
per token, so the `UpdateTask` commands now originate inside its `preCmds`
rather than from `handleCancelRequested`'s explicit `taskCancelCmds` at
`engine/step_triggers.go:211`. That caller's own `cancelOpenTasks()` then finds
nothing open and returns nil. The commands are the same and none is lost, but
their **position** in the emitted stream changes for the cancel-with-compensation
path — the ordering ADR-0088 documents. Confirm the set is unchanged and the
new position is `[def cancel actions…, node cancel actions…, <walk preCmds
including UpdateTask>…]`, then update the expectation.

- [x] **Step 1.12b: Delete the now-dead `taskCancelCmds` prepend**

Audit finding D3, and the direct consequence of the expectation move you just
adjudicated. `handleCancelRequested`'s compensation branch prepends an explicit
sweep:

```go
// engine/step_triggers.go:211-212 — DELETE these two lines' taskCancelCmds
taskCancelCmds := s.cancelOpenTasks()
res.Commands = append(append(append(cancelActionCmds, nodeCancelCmds...), taskCancelCmds...), res.Commands...)
```

`beginCompensation` snapshots and cancels **every** token
(`engine/step_compensation.go:218-227`), and each `cancelTokenWaits` now retires
that token's open task, so this sweep can only ever return `nil`. A defensive
call that provably returns nothing is worse than no call: it reads as a live
guarantee. Replace with:

```go
	// Ordering: [def.CancelActions…, per-node CancelActions…, compensation walk…].
	// The explicit task-cancel prepend ADR-0088 documented here is gone: since
	// ADR-0163 beginCompensation cancels every token's open task inside its own
	// preCmds, so this site's cancelOpenTasks() could only ever return nil. The
	// same UpdateTask commands are still emitted, one call site earlier.
	res.Commands = append(append(cancelActionCmds, nodeCancelCmds...), res.Commands...)
```

⚠ **Do not delete `engine/step_triggers.go:231`.** The immediate-termination
branch below (no compensation records) never calls `beginCompensation`, so
nothing has cancelled those tokens' tasks and its `cancelOpenTasks()` is live.

**Prove the deletion empirically, do not reason it green.** Run the full package
after the edit:

```bash
go test ./engine/ 2>&1 | tail -40 ; echo "EXIT=$?"
```

If any expectation moves *because of this deletion*, the call was **not** dead —
revert it, and record the counter-example (the topology that has an open task on
no live token) in the Progress block as an adjudicated finding against D3.

- [x] **Step 1.13: Stage the phase**

The design bundle is **already committed** on this branch as
`fix(engine)!: scope-subtree teardown and token-attached task cleanup
(ADR-0162/0163)`. Amend it — do not create a second commit, and do not
re-introduce ADR-0164 into the subject; it belongs to delivery 2b.

```bash
git add -A && git commit --amend --no-edit
```

Drop the `Docs only in this commit; implementation follows by amend.` line from
the message body once the first phase's code lands.

Every later phase amends this same commit with `git commit --amend --no-edit`. The
commit stays local and unpushed until the Delivery Gate passes, so amending is
safe. Do **not** create a second commit.

---

## Phase 2 — ADR-0162: the scope-tree helpers

**Files:**
- Modify: `engine/state_compensation.go:283-310` (`closeScope`)
- Create: three helpers in `engine/state_compensation.go`
- Modify: `engine/export_test.go`
- Test: `engine/state_compensation_test.go` (**new file**)

**Interfaces:**
- Produces: `func (s *InstanceState) descendantScopeIDs(scopeID string) map[string]bool`
- Produces: `func (s *InstanceState) closeScopeDescendants(scopeID string)`
- Produces: `func (s *InstanceState) tokensInScopeSubtree(scopeID string) int`
- Produces: `func (s *InstanceState) hasChildScopeWithTokens(parentID, exceptID string) bool`
- Consumes: nothing from Phase 1.

⚠ `hasChildScopeWithTokens` was added on the 2026-08-03 pre-implementation
conflict scan (owner-decided). Phase 4 originally hand-rolled a **fourth** copy
of the same child-scan loop; all four sites ask one question, so the predicate
is named once here and called four times there. See ADR-0162 Decision point 5
for the call table.

- [x] **Step 2.1: Write the failing helper tests**

Create `engine/state_compensation_test.go`:

```go
package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// scopeTreeState returns a state whose scope tree is
//
//	root("") ─┬─ s1 ─── s2 ─── s3
//	          └─ s4
//
// with one token in each scope plus one at the root, built through OpenScope so
// the parent-before-child slice order openScope guarantees is real rather than
// hand-arranged.
func scopeTreeState(t *testing.T) *engine.InstanceState {
	t.Helper()
	s := &engine.InstanceState{InstanceID: "i1"}
	s1 := engine.OpenScope(s, "sub1", "")
	s2 := engine.OpenScope(s, "sub2", s1)
	s3 := engine.OpenScope(s, "sub3", s2)
	s4 := engine.OpenScope(s, "sub4", "")
	s.Tokens = []engine.Token{
		{ID: "t-root", NodeID: "n0", ScopeID: ""},
		{ID: "t1", NodeID: "n1", ScopeID: s1},
		{ID: "t2", NodeID: "n2", ScopeID: s2},
		{ID: "t3", NodeID: "n3", ScopeID: s3},
		{ID: "t4", NodeID: "n4", ScopeID: s4},
	}
	return s
}

func TestDescendantScopeIDs(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s2, s3, s4 := s.Scopes[0].ID, s.Scopes[1].ID, s.Scopes[2].ID, s.Scopes[3].ID

	cases := []struct {
		name   string
		scope  string
		assert func(t *testing.T, got map[string]bool)
	}{
		{
			name:  "a mid-tree scope collects itself and its transitive children",
			scope: s1,
			assert: func(t *testing.T, got map[string]bool) {
				assert.True(t, got[s1])
				assert.True(t, got[s2], "child")
				assert.True(t, got[s3], "grandchild — the level closeScope's callers miss")
				assert.False(t, got[s4], "a sibling subtree is untouched")
			},
		},
		{
			name:  "a leaf collects only itself",
			scope: s3,
			assert: func(t *testing.T, got map[string]bool) {
				assert.True(t, got[s3])
				assert.False(t, got[s1])
				assert.False(t, got[s2])
			},
		},
		{
			name:  "the root collects every scope in the instance",
			scope: "",
			assert: func(t *testing.T, got map[string]bool) {
				assert.True(t, got[""], "the root itself, which has no Scope entry")
				assert.True(t, got[s1])
				assert.True(t, got[s2])
				assert.True(t, got[s3])
				assert.True(t, got[s4])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, engine.DescendantScopeIDs(scopeTreeState(t), tc.scope))
		})
	}
}

func TestTokensInScopeSubtree(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s3 := s.Scopes[0].ID, s.Scopes[2].ID

	assert.Equal(t, 3, engine.TokensInScopeSubtree(s, s1),
		"s1 plus its child and grandchild")
	assert.Equal(t, 1, engine.TokensInScopeSubtree(s, s3), "a leaf counts only itself")
	assert.Equal(t, 5, engine.TokensInScopeSubtree(s, ""), "the root counts everything")
	assert.Equal(t, 1, engine.TokensInScope(s, s1),
		"contrast: the exact-match count still sees only the immediate scope")
}

func TestHasChildScopeWithTokens(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s2, s3 := s.Scopes[0].ID, s.Scopes[1].ID, s.Scopes[2].ID

	cases := []struct {
		name   string
		parent string
		except string
		setup  func(s *engine.InstanceState)
		assert func(t *testing.T, got bool)
	}{
		{
			name:   "a child holding a token is seen",
			parent: s1, except: "",
			assert: func(t *testing.T, got bool) { assert.True(t, got) },
		},
		{
			// ⚠ THIS is the row that pins the except filter. It must be a case where
			// the excepted child is the ONLY candidate: s1's sole child is s2, so
			// dropping `sc.ID != exceptID` from the implementation flips this to
			// true. The sibling-style case below cannot do that job — with
			// parent "" and except s1, s4 still holds a token, so the row returns
			// true whether or not the filter works. (Added 2026-08-03 after the
			// Task 2 implementer flagged the original case as mutation-weak.)
			name:   "the excepted child is not counted when it is the only candidate",
			parent: s1, except: s2,
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "s2 is excepted and s1 has no other child")
			},
		},
		{
			name:   "a non-excepted sibling still counts",
			parent: "", except: s1,
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "s4 is still a root child holding a token")
			},
		},
		{
			name:   "a leaf has no children",
			parent: s3, except: "",
			assert: func(t *testing.T, got bool) { assert.False(t, got) },
		},
		{
			name:   "a child whose OWN token is gone but whose GRANDCHILD holds one still counts",
			parent: s1, except: "",
			setup: func(st *engine.InstanceState) {
				// drop s2's own token, keep s3's
				kept := st.Tokens[:0]
				for _, tok := range st.Tokens {
					if tok.ScopeID != s2 {
						kept = append(kept, tok)
					}
				}
				st.Tokens = kept
			},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "subtree, not exact match — this is the whole point")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := scopeTreeState(t)
			if tc.setup != nil {
				tc.setup(st)
			}
			tc.assert(t, engine.HasChildScopeWithTokens(st, tc.parent, tc.except))
		})
	}
}

func TestCloseScopeDescendantsKeepsTheScopeItself(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s4 := s.Scopes[0].ID, s.Scopes[3].ID

	engine.CloseScopeDescendants(s, s1)

	ids := make([]string, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		ids = append(ids, sc.ID)
	}
	assert.Equal(t, []string{s1, s4}, ids,
		"s1 survives so the drain code can still detect its children; s2 and s3 do not")
}

// TestCloseScopeStillGuardsUnknownScope pins the asymmetry that makes this
// delivery safe: descendantScopeIDs has NO existence guard (scopeByID("") is
// always nil because the root scope is implicit, so guarding would make every
// root-level teardown a silent no-op), while closeScope keeps its own — without
// it, closeScope("") would become an instance-wide scope wipe.
func TestCloseScopeStillGuardsUnknownScope(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	before := len(s.Scopes)

	engine.CloseScope(s, "")
	assert.Len(t, s.Scopes, before, `closeScope("") must remain a no-op`)

	engine.CloseScope(s, "no-such-scope")
	assert.Len(t, s.Scopes, before, "an unknown scope is a no-op")
}
```

- [x] **Step 2.2: Run and observe RED**

```bash
go test -run '^TestDescendantScopeIDs$|^TestTokensInScopeSubtree$|^TestHasChildScopeWithTokens$|^TestCloseScopeDescendantsKeepsTheScopeItself$|^TestCloseScopeStillGuardsUnknownScope$' ./engine/ ; echo "EXIT=$?"
```

Expected: build failure — `undefined: engine.DescendantScopeIDs`,
`engine.TokensInScopeSubtree`, `engine.HasChildScopeWithTokens`,
`engine.CloseScopeDescendants`. Valid RED.

- [x] **Step 2.3: Add the export shims**

```go
// DescendantScopeIDs exposes (*InstanceState).descendantScopeIDs for engine_test.
func DescendantScopeIDs(s *InstanceState, scopeID string) map[string]bool {
	return s.descendantScopeIDs(scopeID)
}

// CloseScopeDescendants exposes (*InstanceState).closeScopeDescendants for engine_test.
func CloseScopeDescendants(s *InstanceState, scopeID string) {
	s.closeScopeDescendants(scopeID)
}

// TokensInScopeSubtree exposes (*InstanceState).tokensInScopeSubtree for engine_test.
func TokensInScopeSubtree(s *InstanceState, scopeID string) int {
	return s.tokensInScopeSubtree(scopeID)
}

// HasChildScopeWithTokens exposes (*InstanceState).hasChildScopeWithTokens for engine_test.
func HasChildScopeWithTokens(s *InstanceState, parentID, exceptID string) bool {
	return s.hasChildScopeWithTokens(parentID, exceptID)
}
```

- [x] **Step 2.4: Implement the helpers and refactor `closeScope`**

Replace `closeScope` (`engine/state_compensation.go:283-310`) and add the three
helpers beside it:

```go
// descendantScopeIDs returns scopeID plus every scope transitively nested inside
// it. A single forward pass over s.Scopes suffices because openScope always
// appends a child after its parent (ScopeSeq is monotonically increasing and a
// scope's ParentID must already exist when it is opened), so by the time a scope
// is visited its parent's membership is already known.
//
// It deliberately has NO existence guard, unlike closeScope. scopeByID("") is
// ALWAYS nil because the root scope is implicit — no Scope entry exists for it —
// so guarding here would make every root-level teardown a silent no-op. The
// returned set therefore contains scopeID itself even when no such Scope exists,
// which is exactly what a root-level teardown needs (ADR-0162).
func (s *InstanceState) descendantScopeIDs(scopeID string) map[string]bool {
	ids := map[string]bool{scopeID: true}
	for _, sc := range s.Scopes {
		if ids[sc.ID] || ids[sc.ParentID] {
			ids[sc.ID] = true
		}
	}
	return ids
}

// closeScope removes the Scope with the given scopeID from s.Scopes, along with
// every descendant scope reachable via the ParentID chain. It is a no-op if no
// scope with that ID exists (also covering the case where scopeID was already
// closed). Callers remain responsible for any per-scope cleanup outside s.Scopes
// (cancelling tokens, arms, timers, archiving compensation records) before
// invoking closeScope; this only prunes the scope tree itself (ADR-0130).
//
// The existence guard is load-bearing and must NOT be pushed into
// descendantScopeIDs: removing it here would turn closeScope("") — the implicit
// root — into an instance-wide scope wipe.
func (s *InstanceState) closeScope(scopeID string) {
	if s.scopeByID(scopeID) == nil {
		return
	}
	doomed := s.descendantScopeIDs(scopeID)
	out := make([]Scope, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		if doomed[sc.ID] {
			continue
		}
		out = append(out, sc)
	}
	s.Scopes = out
}

// closeScopeDescendants prunes every scope nested inside scopeID from s.Scopes,
// KEEPING scopeID itself. The interrupting event-sub-process teardown needs
// exactly this shape: the enclosing scope stays open so the drain code can
// detect its children, while its descendants must not survive the interrupt
// (ADR-0162). No existence guard, for the same reason descendantScopeIDs has
// none — the root scope is implicit, and its descendants are real.
func (s *InstanceState) closeScopeDescendants(scopeID string) {
	doomed := s.descendantScopeIDs(scopeID)
	out := make([]Scope, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		if sc.ID != scopeID && doomed[sc.ID] {
			continue
		}
		out = append(out, sc)
	}
	s.Scopes = out
}

// tokensInScopeSubtree counts tokens in scopeID and in every scope nested inside
// it. The sub-process drain checks need this rather than tokensInScope: a
// grandchild scope holding the live token must keep the subtree from being
// declared drained, or closeScope prunes it and leaves a token naming a scope
// that no longer exists — which wedges the instance permanently (ADR-0162).
func (s *InstanceState) tokensInScopeSubtree(scopeID string) int {
	ids := s.descendantScopeIDs(scopeID)
	count := 0
	for i := range s.Tokens {
		if ids[s.Tokens[i].ScopeID] {
			count++
		}
	}
	return count
}

// hasChildScopeWithTokens reports whether any child scope of parentID — other
// than exceptID — still holds a token anywhere in its own subtree. It is the
// single form of the "can this scope exit yet?" question that all four scope
// exits ask (engine/step_nodes.go:283, 304-311, 354-362, 406-413); before
// ADR-0162 three of them hand-rolled it over tokensInScope and the fourth had no
// check at all, which is how the fourth stayed broken.
//
// exceptID == "" means "no exception": the root scope is implicit and has no
// Scope entry, so no real scope ever has an empty ID.
func (s *InstanceState) hasChildScopeWithTokens(parentID, exceptID string) bool {
	for _, sc := range s.Scopes {
		if sc.ParentID == parentID && sc.ID != exceptID && s.tokensInScopeSubtree(sc.ID) > 0 {
			return true
		}
	}
	return false
}
```

- [x] **Step 2.5: Run and observe GREEN**

```bash
go test -run '^TestDescendantScopeIDs$|^TestTokensInScopeSubtree$|^TestHasChildScopeWithTokens$|^TestCloseScopeDescendantsKeepsTheScopeItself$|^TestCloseScopeStillGuardsUnknownScope$' ./engine/ ; echo "EXIT=$?"
go test ./engine/ ; echo "FULL_PKG_EXIT=$?"
golangci-lint run ./engine/... ; echo "LINT_EXIT=$?"
```

Expected: PASS on both, `LINT_EXIT=0`. `closeScope` is a pure refactor here, so
the existing suite must stay green with **no** expectation changes. If anything
moves, the refactor is not behaviour-preserving — fix it rather than the test.

⚠ **The lint run is the point of this step, not a formality** (audit finding
D5). The self-review flags an unresolved risk that `unused` may fire on
`closeScopeDescendants`, `tokensInScopeSubtree` and `cancelScopeSubtree`, which
have **no production call site** until Phase 3 — this is the step that settles
it. `golangci-lint` lints test files by default, so the `export_test.go` shims
and the tests above should count as usage. If `unused` fires anyway:

- **Do not add `//nolint`.** A suppression for a condition that disappears one
  phase later becomes permanent noise.
- Land Phases 2 and 3 **together** as one amend instead, and record that
  decision in the Progress block. Phase 2's RED/GREEN cycle is unaffected —
  only the commit boundary moves.

- [x] **Step 2.6: Amend**

```bash
git add -A && git commit --amend --no-edit
```

---

## Phase 3 — ADR-0162: route both abnormal teardowns through the subtree

**Files:**
- Create: `cancelScopeSubtree` in `engine/step_cancel.go`
- Modify: `engine/step_eventsubprocess.go:184-216`
- Modify: `engine/step_errors.go:377-395`
- Modify: `engine/export_test.go`
- Test: `engine/step_eventsubprocess_test.go` (**new file**)

**Interfaces:**
- Consumes: `descendantScopeIDs` (Phase 2), `cancelTokenWaits` with its Phase 1
  task/incident behaviour.
- Produces: `func cancelScopeSubtree(s *InstanceState, scopeID string, at time.Time, kind CloseKind) []Command`

- [x] **Step 3.1: Write the failing tests**

Create `engine/step_eventsubprocess_test.go` with **five** tests. Audit finding
T1: an earlier draft of this step promised three in prose and declared two in
code, and both of the declared ones were for defect 4 — leaving defect 2,
**ADR-0162's headline decision**, with no test, no RED command and no GREEN
command.

| test | defect / finding |
|---|---|
| `TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken` | 2 |
| `TestRootInterruptingEventSubprocessLeavesNoZombieScopes` | 3 |
| `TestErrorBoundaryTeardownArchivesCompensations` | 4 |
| `TestArchivedRecordIsReachableByTargetedThrow` | 4, second reader surface |
| `TestTeardownArchiveSwitchesCancelToTheCompensationBranch` | D7 |
| `TestErrorBoundaryTeardownArchivesNestedSubtreeCompensations` | the descendant loop itself |

⚠⚠ **The sixth test is the only thing that pins the *subtree* half of
`cancelScopeSubtree`** (added 2026-08-03 on the Task 3 review, which found both
statements in the descendant loop deletable with no test going red — 100% line
coverage, nothing observing the effect). The other five all archive via the
`scopeID`-first block: the error-path tests pass `errScopeID` itself, which the
loop explicitly skips, and the ESP tests have descendants that carry no records
and no arms. Topology: an **outer** sub-process containing an **inner**
sub-process whose activity is compensable, error boundary on the **outer** node.
Drive the inner to completion, fail the outer, then assert
`ArchivedCompensations` keyed by the **inner** sub-process node id holds the
record with `NodeID` = the compensable activity — same wrong-key discipline as
the defect-4 test.

⚠⚠ **The archive key is the sub-process NODE id, not the compensable
activity's** (audit finding T5). `archiveCompensations` keys by `scope.NodeID`
(`engine/state_compensation.go:257`) — the id of the sub-process node that owns
the scope. In the `fulfil` topology below that key is `"fulfil"`, **not**
`"charge"`. Asserting on the wrong key yields `len(records) == 0` on *both*
sides of the fix: the test passes before and after and certifies nothing. This
is the single easiest way to write a dead test in this phase.

**Defects 2 and 3 — the root-level interrupt.** Both drive the same fixture, so
build it once. The topology is the one in ADR-0162's Context: an interrupting
boundary routes to a `SubProcess`, `drive` enters it opening a nested scope, and
a **root-level interrupting event sub-process** then fires on the same signal.
Before ADR-0162 that interrupt cancels root-scope tokens only.

```go
// TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken asserts that a
// root-level interrupting event sub-process cancels a token an earlier arm had
// pushed into a NESTED sub-process scope. Before ADR-0162 the teardown matched
// tokens on exact scope equality, so the nested token survived the interrupt and
// the instance kept running the very activity the interrupt targeted.
func TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken(t *testing.T) {
	// 1. Drive until a token is parked INSIDE the nested sub-process scope.
	//    require that its ScopeID != "" — if it is the root scope the fixture is
	//    wrong and the test proves nothing.
	// 2. Fire the root-level interrupting event sub-process.
	// 3. Assert no surviving token carries the nested scope's ID, and that the
	//    only live tokens are the event sub-process's own.
}

// TestRootInterruptingEventSubprocessLeavesNoZombieScopes asserts a COMPLETED
// instance carries no leftover Scopes entries. Before ADR-0162 the cancelled
// descendant scopes were never closed, so a terminal snapshot was committed with
// open scopes in it.
func TestRootInterruptingEventSubprocessLeavesNoZombieScopes(t *testing.T) {
	// Same fixture; drive the event sub-process to completion.
	// Assert Status is terminal AND len(State.Scopes) == 0.
	// ⚠ Assert on the COMPLETED state, not mid-interrupt: the enclosing scope is
	// deliberately kept open across the interrupt (closeScopeDescendants keeps
	// scopeID itself), so a mid-flight assertion of zero scopes would be wrong.
}
```

The defect-4 tests are the highest-value ones because they also pin the semantic
change ADR-0162 accepts. Build the `fulfil` topology from the spec's §1.4:

```go
// fulfilSubprocessDef returns:
//
//	start → fulfil[ start2 → charge(compensable: refund) → ship → end2 ] → end
//	          ↑ error boundary "OutOfStock" → notify → end3
//
// A sub-process whose completed compensable activity must survive the
// sub-process being torn down by its own error boundary (ADR-0162).
func fulfilSubprocessDef() *model.ProcessDefinition { /* … */ }

// TestErrorBoundaryTeardownArchivesCompensations asserts that when an error
// boundary tears down an enclosing scope, the completed compensable work inside
// it is archived rather than pruned with the scope. Before ADR-0162 the record
// was discarded, so a card charged inside a failed fulfilment could never be
// refunded — while the identical sub-process exiting normally stayed
// compensable.
func TestErrorBoundaryTeardownArchivesCompensations(t *testing.T) {
	// 1. Start, drive "charge" to completion → a record exists in the fulfil scope.
	// 2. Fail "ship" with the OutOfStock error code.
	// 3. Assert the instance routed to "notify" (the boundary fired).
	// 4. Assert s.ArchivedCompensations["fulfil"] holds exactly the refund record.
	//    ⚠ The key is the SUB-PROCESS node id "fulfil", NOT "charge" — see the
	//    warning above. Assert the key exists AND that the record's NodeID is
	//    "charge", so a wrong-key regression cannot pass as an empty map.
	// 5. Assert no Scope remains whose ID was the fulfil scope.
}

// TestArchivedRecordIsReachableByTargetedThrow pins the second reader surface
// ADR-0162 changes: a CompensateThrow naming the torn-down sub-process node used
// to auto-advance on len(records) == 0 (step_nodes.go:1017) and now walks the
// archived records for real.
func TestArchivedRecordIsReachableByTargetedThrow(t *testing.T) { /* … */ }

// TestTeardownArchiveSwitchesCancelToTheCompensationBranch pins the third reader
// surface, and the one most likely to move an existing expectation (audit
// finding D7). handleCancelRequested (step_triggers.go:196) and
// handleUnhandledError (step_errors.go:236) choose their branch on
// len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0. A teardown
// that used to leave the archive empty now populates it, so a later admin cancel
// switches from the SINGLE-STEP immediate-termination branch to a MULTI-STEP
// compensation walk with a different terminal command sequence.
func TestTeardownArchiveSwitchesCancelToTheCompensationBranch(t *testing.T) {
	// 1. Same fulfil topology: drive "charge" to completion, fail "ship" so the
	//    error boundary tears the scope down and (post-fix) archives the record.
	// 2. Assert the instance is still RUNNING at "notify" — the branch choice is
	//    only observable while the instance can still receive a cancel.
	// 3. Deliver engine.NewCancelRequested(...).
	// 4. Assert the compensation branch was taken: an InvokeAction for "refund"
	//    is emitted and Status is Compensating — NOT an immediate
	//    FailInstance{"cancelled"} with StatusTerminated in one step.
}
```

Write these out in full — the fixture definitions, the `engine.Step` calls and
the assertions — before running anything. Model the definition-construction
style on `engine/step_subprocess_test.go`, which already builds nested
sub-process definitions with error boundaries.

- [x] **Step 3.2: Run and observe RED**

```bash
go test -run '^TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken$|^TestRootInterruptingEventSubprocessLeavesNoZombieScopes$|^TestErrorBoundaryTeardownArchivesCompensations$|^TestArchivedRecordIsReachableByTargetedThrow$|^TestTeardownArchiveSwitchesCancelToTheCompensationBranch$' ./engine/ ; echo "EXIT=$?"
```

Expected failures, one per test: the nested token survives the interrupt; the
completed instance carries leftover `Scopes` entries; `ArchivedCompensations` is
empty; the targeted throw auto-advances without emitting an `InvokeAction`; and
the cancel takes the immediate-termination branch instead of the walk.

⚠ Confirm you see **five** distinct failures. A test that fails only because a
fixture panics is not a RED state for the behaviour it names — read each message.

- [x] **Step 3.3: Implement `cancelScopeSubtree`**

In `engine/step_cancel.go`, below `cancelTokenWaits`:

```go
// cancelScopeSubtree cancels every token in scopeID and in all its descendant
// scopes, retires their event-sub-process arms, archives their compensation
// records, and returns the commands produced by the sweep: CancelTimer for
// retired arms and timers, and UpdateTask for human tasks retired with their
// token (ADR-0163 — cancelTokenWaits gained that in Phase 1, after this plan
// was first drafted; corrected 2026-08-03 on the Task 3 review).
//
// It does NOT close the scopes. The caller decides: the interrupting
// event-sub-process path keeps the enclosing scope open so the drain code can
// detect its children (and calls closeScopeDescendants), while the error-boundary
// path calls closeScope on the whole subtree (ADR-0162).
//
// scopeID may be "" — the implicit root scope — in which case the doomed set is
// the entire instance. That is the correct reading for a root-level interrupting
// event sub-process: BPMN interrupting event sub-processes at process level
// terminate all other activity in the process.
func cancelScopeSubtree(s *InstanceState, scopeID string, at time.Time, kind CloseKind) []Command {
	ids := s.descendantScopeIDs(scopeID)

	// Snapshot before cancelling: cancelTokenWaits mutates s.Tokens.
	tokensToCancel := make([]Token, 0, len(s.Tokens))
	for _, tok := range s.Tokens {
		if ids[tok.ScopeID] {
			tokensToCancel = append(tokensToCancel, tok)
		}
	}
	var cmds []Command
	for _, tok := range tokensToCancel {
		cmds = append(cmds, cancelTokenWaits(s, &tok, at, kind)...)
	}

	// scopeID itself first: it may be the implicit root (""), which has no entry
	// in s.Scopes and so would be missed by the slice walk below. Both call
	// sites retire the named scope's arms today, so this preserves that exactly.
	// archiveCompensations("") is a no-op by construction — root records live in
	// s.RootCompensations, which is never pruned.
	cmds = appendCancelTimers(cmds, s.removeEventTriggeredSubprocessArmsForScope(scopeID))
	s.archiveCompensations(scopeID)

	// Then every descendant, in s.Scopes SLICE order — parent before child,
	// because openScope appends children after parents. Never map order: the
	// emitted command sequence and the ArchivedCompensations append order must
	// be deterministic.
	for i := range s.Scopes {
		id := s.Scopes[i].ID
		if id == scopeID || !ids[id] {
			continue
		}
		cmds = appendCancelTimers(cmds, s.removeEventTriggeredSubprocessArmsForScope(id))
		s.archiveCompensations(id)
	}
	return cmds
}
```

⚠ The `scopeID`-first block is not cosmetic. A loop over `s.Scopes` alone would
silently skip the root scope, turning root-level interrupt teardown into a no-op
for arm retirement — the exact class of bug ADR-0162 exists to remove.

- [x] **Step 3.4: Rewire the interrupting event-sub-process path**

In `engine/step_eventsubprocess.go`, replace the `if !ea.NonInterrupting` block's
token loop and arm retirement (currently `:189-207`) with:

```go
	if !ea.NonInterrupting {
		// Interrupting: cancel every token in the enclosing scope AND in all its
		// descendant scopes, retire their arms and archive their compensation
		// records (ADR-0162 — before it, a token an earlier arm had pushed into a
		// nested sub-process survived the interrupt). The enclosing scope itself
		// stays open so the drain code can detect its children; its descendants
		// are closed, or a completed instance would carry zombie Scopes entries.
		cmds = append(cmds, cancelScopeSubtree(s, ea.EnclosingScopeID, at, CloseKindBoundaryInterrupted)...)
		s.closeScopeDescendants(ea.EnclosingScopeID)

		// Open a child scope for the event sub-process, parented to the ENCLOSING
		// scope. This happens AFTER closeScopeDescendants so the new child
		// survives. (…retain the existing explanatory comment about the drain
		// code detecting this scope…)
		childScopeID := s.openScope(ea.EventSubprocessNode, ea.EnclosingScopeID)
		s.placeTokenInScope(innerStart.ID(), childScopeID, at)
	} else {
```

⚠ Ordering is load-bearing: `closeScopeDescendants` must run **before**
`openScope`, or the freshly opened event-sub-process child scope is pruned
immediately.

- [x] **Step 3.5: Rewire the error-boundary teardown**

In `engine/step_errors.go`, replace the `consume` closure body (`:377-395`):

```go
		consume := func(cmds []Command) []Command {
			// Cancel every token in the erroring scope AND in all its descendant
			// scopes, retire their arms, archive their compensation records, then
			// close the whole subtree. closeScope already prunes descendants
			// (ADR-0130); before ADR-0162 it did so while leaving their tokens
			// alive, so those tokens ended up naming a scope that no longer
			// existed and every subsequent Step failed in defForScope.
			cmds = append(cmds, cancelScopeSubtree(s, errScopeID, at, CloseKindBoundaryInterrupted)...)
			s.closeScope(errScopeID)
			return cmds
		}
```

- [x] **Step 3.6: Run and observe GREEN, then triage the package**

```bash
go test -run '^TestRootInterruptingEventSubprocess|^TestErrorBoundaryTeardownArchivesCompensations$|^TestArchivedRecordIsReachableByTargetedThrow$|^TestTeardownArchiveSwitchesCancelToTheCompensationBranch$' ./engine/ ; echo "EXIT=$?"
go test ./engine/ 2>&1 | tail -40 ; echo "FULL_PKG_EXIT=$?"
```

Triage exactly as in Step 1.12: each moved expectation is either a correct
consequence of subtree teardown or a real regression. Record the adjudication.

- [x] **Step 3.7: Amend**

```bash
git add -A && git commit --amend --no-edit
```

---

## Phase 4 — ADR-0162: subtree-aware drain checks (the permanent wedge)

**Files:**
- Modify: `engine/step_nodes.go:304-311`, `:354-362`, `:406-413` (replace three
  hand-rolled loops with Phase 2's `hasChildScopeWithTokens`)
- Modify: `engine/step_nodes.go:283` (`exitEventSubprocessScope` — **add** a
  check it never had, plus an archive) and `:372`
  (`exitNestedEventSubprocessScope` — add an archive)
- Test: `engine/step_scope_drain_test.go` (**new file**)

**Interfaces:**
- Consumes: `hasChildScopeWithTokens` (Phase 2), which is itself built on
  `tokensInScopeSubtree`. This phase adds **no** new loop of its own — all four
  sites call the predicate.

⚠ **Widening the three existing checks is not sufficient** (audit finding B3,
ADR-0162 Context problem 6 / Decision point 6). `exitEventSubprocessScope`
(`engine/step_nodes.go:283`) calls `closeScope` **unconditionally**: it has no
descendant check to widen, and its only upstream gate is the **exact-match**
self check `tokensInScope(currentScopeID) != 0` at `:235`, which cannot see a
descendant scope's tokens. An earlier draft of ADR-0162 declared this site
"unaffected"; it is not. Steps 4.3b–4.3d below close it, and skipping them
leaves the delivery's headline defect alive on a path the ADR used as its own
counter-example.

- [x] **Step 4.1: Write the failing wedge test**

Create `engine/step_scope_drain_test.go`. The topology needs three nesting
levels and a sibling branch that reaches the middle scope's end event while the
middle scope's token has descended into the innermost scope:

```go
// TestGrandchildScopeBlocksSubprocessDrain reproduces the permanent instance
// wedge. The three drain checks used to enumerate DIRECT children only, so a
// grandchild scope holding the live token was invisible: the exit declared the
// subtree drained, closeScope pruned it transitively, and the surviving token
// named a scope absent from s.Scopes. From that commit on, defForScope failed
// for every subsequent Step — the instance could be neither advanced nor
// terminated, because every path runs through drive.
func TestGrandchildScopeBlocksSubprocessDrain(t *testing.T) {
	// 1. Build outer[ fork ⇒ { branchA: inner[ deep[ UserTask ] ], branchB: end } ].
	// 2. Drive until branchA's token is parked inside the DEEPEST scope (deep)
	//    and branchB has reached the OUTER scope's end event. The token must be
	//    in a GRANDCHILD of the scope being exited — a direct child is already
	//    caught by hasActiveChildren (step_nodes.go:405-413), so a two-level
	//    topology does not reproduce the defect.
	// 3. Assert the Step that drains branchB returns no error.
	// 4. Assert every surviving token's ScopeID resolves to a live Scope —
	//    the invariant whose violation is the wedge.
	// 5. Deliver one more trigger and assert Step still succeeds, which is the
	//    property that actually fails before the fix.
}
```

Step 5 is the load-bearing assertion: the pre-fix failure is not in the draining
`Step` itself but in **every subsequent one**. A test that stops at step 3 would
pass before and after the fix and certify nothing.

- [x] **Step 4.2: Run and observe RED**

```bash
go test -run '^TestGrandchildScopeBlocksSubprocessDrain$' ./engine/ ; echo "EXIT=$?"
```

Expected: FAIL on the follow-up `Step`, with an error mentioning `defForScope`
or `scope … not found`.

- [x] **Step 4.3: Implement — replace the three drain loops with the predicate**

All three are the same question with different arguments, so they collapse to
three calls to Phase 2's `hasChildScopeWithTokens` (owner decision 2026-08-03;
ADR-0162 Decision point 5 carries the call table). Delete the hand-rolled loop
**and** its `hasOtherRootChildren` / `hasOtherChildren` / `hasActiveChildren`
boolean at each site — leaving the variable assigned but unused will not
compile.

```go
// engine/step_nodes.go:302-314 (exitRootEventSubprocessScope) — was a loop over
// c.s.Scopes plus a hasOtherRootChildren bool.
	// Subtree, not the immediate scope: a grandchild holding the live token must
	// keep the root from being declared drained (ADR-0162).
	if c.s.hasChildScopeWithTokens("", currentScopeID) {
		return cmds, false, nil
	}
```

| site | function | replacement call |
|---|---|---|
| `:304-311` | `exitRootEventSubprocessScope` | `hasChildScopeWithTokens("", currentScopeID)` |
| `:354-362` | `exitNestedEventSubprocessScope` | `hasChildScopeWithTokens(parentScopeID, currentScopeID)` |
| `:406-413` | `exitRegularSubprocessScope` | `hasChildScopeWithTokens(currentScopeID, "")` |

Each site's existing `if has…Children { return cmds, false, nil }` block already
does the right thing — fold the predicate straight into that `if`.

Leave the three *self*-checks alone — `tokensInScope(currentScopeID)` at `:235`,
`tokensInScope("")` at `:299` and `tokensInScope(parentScopeID)` at `:348` ask
"has THIS scope drained", and child scopes legitimately have their own
`ScopeID`. Widening those to the subtree would make a non-interrupting event
sub-process running alongside block its enclosing scope's exit forever.

- [x] **Step 4.3b: Write the failing tests for the event-sub-process exit (B3)**

Two behaviours, two tests, both in `engine/step_scope_drain_test.go`. They fail
**after** Step 4.3 lands, because `:283` has no check for Step 4.3 to widen.

```go
// TestEventSubprocessExitBlocksOnChildScope reproduces the permanent wedge
// at the one closeScope call site with no descendant check at all. The topology
// is ADR-0162's: an event sub-process whose body is
// start(signal) → fork ⇒ { A: SubProcess "inner"[…UserTask…], B: end }.
// Branch A opens scope D under the event sub-process's child scope C; branch B
// reaches the event sub-process's end event. tokensInScope(C) == 0, so
// exitEventSubprocessScope ran closeScope(C), which cascades and prunes D too —
// leaving the UserTask token naming an absent scope.
func TestEventSubprocessExitBlocksOnChildScope(t *testing.T) {
	// 1. Drive branch A into "inner" so the UserTask parks in scope D.
	// 2. Drive branch B to the event sub-process's end event.
	// 3. Assert that Step returns no error AND that every surviving token's
	//    ScopeID still resolves to a live Scope.
	// 4. Deliver one MORE trigger and assert Step still succeeds. As in
	//    TestGrandchildScopeBlocksSubprocessDrain, this is the load-bearing
	//    assertion — the pre-fix failure is in the NEXT step, not this one.
}

// TestEventSubprocessNormalExitArchivesCompensations pins the archiving half of
// B3. Compensable work completed inside ANY event sub-process was dropped on its
// NORMAL exit, because :283 closes without archiving — defect 4 on the very path
// ADR-0162's defect-4 argument used as its counter-example ("had it exited
// normally, both would refund").
func TestEventSubprocessNormalExitArchivesCompensations(t *testing.T) {
	// 1. Event sub-process containing a completed compensable activity.
	// 2. Drive it to its NORMAL exit (no interrupt, no error).
	// 3. Assert ArchivedCompensations holds the record, keyed by the EVENT
	//    SUB-PROCESS node id — the scope's NodeID, not the activity's.
}
```

- [x] **Step 4.3c: Run and observe RED**

```bash
go test -run '^TestEventSubprocessExitBlocksOnChildScope$|^TestEventSubprocessNormalExitArchivesCompensations$' ./engine/ ; echo "EXIT=$?"
```

Expected: the first fails on the **follow-up** `Step` with a `defForScope` /
`scope … not found` error; the second fails with an empty
`ArchivedCompensations`. ⚠ Run this **after** Step 4.3 is green, so you can see
these two fail for their own reason rather than being masked by the three
widened checks.

- [x] **Step 4.3d: Implement the descendant guard and the two archives**

In `engine/step_nodes.go`, at `exitEventSubprocessScope` (`:282-289`):

```go
func exitEventSubprocessScope(c *stepCtx, currentScopeID, parentScopeID string) ([]Command, bool, error) {
	// A descendant scope holding a live token must keep this scope from being
	// pruned: closeScope cascades (ADR-0130), so closing here would orphan that
	// token and wedge the instance permanently. The self check upstream (:235) is
	// exact-match and cannot see it — this site had NO descendant check at all
	// (ADR-0162 problem 6).
	if c.s.hasChildScopeWithTokens(currentScopeID, "") {
		return nil, false, nil
	}
	// Archive before closing, exactly as the regular sub-process exit does at
	// :422. Without this, compensable work completed inside any event
	// sub-process is dropped on its NORMAL exit.
	c.s.archiveCompensations(currentScopeID)
	c.s.closeScope(currentScopeID)
	…
}
```

⚠ Return `stop=false`, matching the other "not drained yet" returns on this
path — `stop=true` would halt `drive` and park the instance rather than letting
the remaining tokens advance.

And in `exitNestedEventSubprocessScope`, immediately before the
`c.s.closeScope(parentScopeID)` at `:372`:

```go
	// Archive the enclosing scope's records before pruning it: it may have
	// recorded compensable work before the interrupt fired (ADR-0162).
	c.s.archiveCompensations(parentScopeID)
	c.s.closeScope(parentScopeID)
```

After this phase **every** `closeScope` call site archives first — `:283` and
`:372` here, `:422` already, and the two abnormal paths through
`cancelScopeSubtree` in Phase 3. That is the invariant to check by inspection
before moving on:

```bash
grep -n -B4 'closeScope(' engine/step_nodes.go engine/step_errors.go | grep -c 'archiveCompensations\|cancelScopeSubtree'
```

- [x] **Step 4.4: Run and observe GREEN**

```bash
go test -run '^TestGrandchildScopeBlocksSubprocessDrain$|^TestEventSubprocessExitBlocksOnChildScope$|^TestEventSubprocessNormalExitArchivesCompensations$' ./engine/ ; echo "EXIT=$?"
go test ./engine/ ; echo "FULL_PKG_EXIT=$?"
```

- [x] **Step 4.5: Amend**

```bash
git add -A && git commit --amend --no-edit
```

---

## Phase 5 — the exported godoc this delivery falsifies

Audit finding C4. This is a **library-first** project: the exported doc comments
*are* the product, and two of them state, as current behaviour, the exact thing
ADR-0162 changes. Leaving them is shipping documentation that is wrong on the
day it merges.

The `CompensateRequested` / `NewCompensateRequested` / `NewReverseToNode` half of
C4 belongs to **delivery 2b** (it documents the resume guard) and is Step 2.4 of
`docs/plans/2026-08-02-terminal-transitions.md`. Only the archiving half is here.

- [x] **Step 5.1: Update `engine/command.go:206-208`**

Current text, inside the `Compensate` command's "Current state of compensation"
block:

> - When a sub-process scope closes **normally**, its accumulated CompensationRecords
>   are ARCHIVED into InstanceState.ArchivedCompensations keyed by the sub-process
>   node ID via archiveCompensations before closeScope is called (ADR-0039).

Replace "closes **normally**" with the post-ADR-0162 truth: **every** scope close
archives — the normal sub-process exit, both event-sub-process exits, and the two
abnormal teardowns (error boundary, interrupting event sub-process) via
`cancelScopeSubtree`. Name ADR-0162 alongside ADR-0039.

- [x] **Step 5.2: Update `engine/trigger.go:311-315`**

Current text, in the `CompensateRequested` doc comment:

> Sub-process compensation (ADR-0013): when a sub-process scope closes **normally**,
> its accumulated CompensationRecords are hoisted into the parent scope (or root)
> before closeScope is called.

Two problems, one of them pre-existing: the "**normally**" qualifier is what this
delivery falsifies, and "hoisted into the parent scope (or root)" describes
ADR-0013's mechanism, which **ADR-0039 already reversed** to archive-by-scope
(`docs/adr/0039-scope-targeted-compensation.md:29-32`, "Reverses ADR-0013's hoist
destination"). Fix both: records are archived by scope on **every** scope close,
and reach `RootCompensations` through `consolidateArchiveIntoRoot` when a walk
begins. Cite ADR-0013 → ADR-0039 → ADR-0162 in that order so the lineage reads
correctly.

- [x] **Step 5.3: Verify the public surface did not change shape**

```bash
go doc -all ./engine > /tmp/godoc-after.txt ; echo "EXIT=$?"
golangci-lint run ./engine/... ; echo "LINT_EXIT=$?"
```

Doc text changes; no exported **signature** does. If `go doc` shows a new or
removed symbol, something in an earlier phase leaked into the public API and
must be made unexported before the gate.

- [x] **Step 5.4: Amend**

```bash
git add -A && git commit --amend --no-edit
```

---

## Phase 5b — ADR-0164, moved to delivery 2b

ADR-0164 (one terminal transition, and no resurrection) was **split out** on the
round-2 audit's recommendation, adjudicated by the owner on 2026-08-02. It is
parked on `parked/terminal-transitions` (off `main`, commit `18f1aa9`) with its
own plan: `docs/plans/2026-08-02-terminal-transitions.md`.

Rationale, in one line: ADR-0164 shares **zero symbols** with ADR-0162 and
touches a disjoint file set, while ADR-0163 and ADR-0164 each introduce an
independent source of command-stream churn that this delivery requires be
adjudicated **individually** — which one combined diff would make impossible for
the `/code-review` reviewer to separate.

Defects 6 and 9, audit finding C1 (incidents outliving their token on terminal
paths) and audit finding O1 (a stranded compensation `ActionCompleted` returning
`ErrTokenNotFound`) all move with it. They are reachable today, have no
interaction with subtree teardown, and so compound with nothing by waiting one
delivery.


## Phase 6 — Verification and the Delivery Gate

- [x] **Step 6.1: Mutation-verify the load-bearing tests**

For each of these, break the implementation on purpose, confirm the test fails,
then restore from a `/tmp` snapshot and `diff` to prove the restore was exact.
ADR-0159's review found five tests that could not fail and certified nothing.

```bash
cp -r engine /tmp/engine-snapshot
# …mutate, run the named test, confirm FAIL, then:
rm -rf engine && cp -r /tmp/engine-snapshot engine && diff -r /tmp/engine-snapshot engine && echo "restore exact"
```

| test | mutation that must make it fail |
|---|---|
| `TestGrandchildScopeBlocksSubprocessDrain` | revert one drain check to `tokensInScope` |
| `TestEventSubprocessExitBlocksOnChildScope` | delete the `hasChildScopeWithTokens` guard added at `step_nodes.go:283` |
| `TestHasChildScopeWithTokens` | two mutations, both must fail it: (a) use `tokensInScope` instead of `tokensInScopeSubtree`; (b) drop the `sc.ID != exceptID` filter |
| `TestEventSubprocessNormalExitArchivesCompensations` | drop the `archiveCompensations` added at `:283` |
| `TestInterruptingBoundaryCancelsHostTask` | drop the `UpdateTask` append in `cancelTokenWaits` |
| `TestErrorBoundaryTeardownCancelsUserTaskAcrossStepBoundary` | same drop — it must fail from the **error** call site too, not only the boundary one |
| `TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken` | narrow `cancelScopeSubtree` back to exact `ScopeID` equality |
| `TestRootInterruptingEventSubprocessLeavesNoZombieScopes` | drop the `closeScopeDescendants` call |
| `TestErrorBoundaryTeardownArchivesCompensations` | drop the `scopeID`-first `archiveCompensations` in `cancelScopeSubtree` (NOT the descendant loop's — the error path passes `errScopeID`, which the loop skips) |
| `TestErrorBoundaryTeardownArchivesNestedSubtreeCompensations` | drop `archiveCompensations(id)` from `cancelScopeSubtree`'s **descendant loop** |
| `TestArchivedRecordIsReachableByTargetedThrow` | same drop — it must fail on the reader surface too |
| `TestTeardownArchiveSwitchesCancelToTheCompensationBranch` | same drop |
| `TestCloseScopeStillGuardsUnknownScope` | move the existence guard into `descendantScopeIDs` |
| `TestRemoveIncidentsForTokenIgnoresEmptyID` | delete the `if tokenID == ""` guard in `removeIncidentsForToken` |
| `TestCancelOpenTasksEmitsDeepCopy` | revert `Clone()` to the shallow copy |
| the Step 1.9b deep-copy tests | revert `step_timers.go:90` / the `step_triggers.go` site to `*task` |

⚠ `TestPartialRollbackCannotResumeTerminalInstance` and
`TestForceTerminationClearsCompensationCursor` are **not** in this table: they
belong to delivery 2b and are mutation-verified in
`docs/plans/2026-08-02-terminal-transitions.md` Step 4.2.

Three rows share a mutation (`archiveCompensations` dropped from
`cancelScopeSubtree`). That is deliberate, not redundancy — each asserts a
different **reader** of the archive, and a fix that satisfied one reader while
breaking another would show up as a partial failure across the three.

- [x] **Step 6.2: Engine coverage and lint**

`engine` is provably container-free — confirm with
`go list -deps -test ./engine/... | grep -c testcontainers` returning `0` — so
this needs no Docker and no permission.

```bash
go test -race -coverprofile=cover.out ./engine/... ; echo "EXIT=$?"
go tool cover -func=cover.out | tail -1
golangci-lint run ./engine/... ; echo "LINT_EXIT=$?"
```

Required: `EXIT=0`, coverage ≥ 85% (ADR-0161 left `engine` at 90.8% — do not
regress it), `LINT_EXIT=0`. Every new function must be covered including its
failure branches; check them individually with

```bash
go tool cover -func=cover.out | grep -E 'descendantScopeIDs|closeScopeDescendants|tokensInScopeSubtree|hasChildScopeWithTokens|cancelScopeSubtree|removeIncidentsForToken'
```

`endInstance` is **not** in that list — it is delivery 2b's symbol.

This is the `engine`-only, **Docker-free** measurement. It is deliberately not
the same command as CLAUDE.md's Verification §1, which is repo-wide and needs
containers; that one runs at Step 6.3, after the Docker ask (audit finding D6).
`go tool cover -func` reports the per-package total directly, so
`scripts/coverage.sh` — whose job is excluding generated `*_mock.go` files
repo-wide (ADR-0143) — is not needed here: `engine` has no generated files.

- [ ] **Step 6.3: Full suite — ASK FIRST**

⚠ **Ask the owner before running anything that starts Docker containers.** This
is a standing instruction from 2026-07-31: other sessions saturate the daemon.
The ADR-0161 approval was for that run only and does not carry over.

```bash
go test -race -count=1 ./... > /tmp/full.txt 2>&1 ; echo "EXIT=$?"
grep -c '^ok' /tmp/full.txt ; grep -c '^FAIL' /tmp/full.txt ; grep -c 'no test files' /tmp/full.txt
```

Verify by **exit code**, not by a piped tail — a `| grep | head` once reported
green while 14 tests failed.

Watch for `TestPgxNotifierListenDrainsBeforePollInterval`, a known load-flake
under concurrent container boot (`internal/persistence/store/notifier_pgx_test.go:98`).
If it fails, re-run it in isolation before treating it as a regression.

- [ ] **Step 6.4: Handover checkpoint**

Rewrite `docs/plans/HANDOVER.md` **in place** (never append), fill in this
plan's `▶ Progress` block with the branch, the phases landed, the adjudications
from Steps 1.12 / 1.12b / 3.6, and the exact verification numbers. Update
`MEMORY.md` and its topic file to point at `HANDOVER.md`.

- [ ] **Step 6.5: Delivery Gate**

In order, per CLAUDE.md:

1. Verification above — tests, ≥ 85% coverage, no cross-repo regressions, clean lint.
2. `/code-review` on the pending change — **owner-run**; `disable-model-invocation`
   means this session cannot launch it. Fix all findings, folded via
   `git commit --amend`.
3. `/security-review` — fix all findings, folded the same way.

Adversarial Opus stand-in reviews are worth running first to cut rework — they
caught a real defect plus five dead tests on ADR-0159 — but they are **not** the
gate: they missed the Medium that `/code-review` found. Findings adjudicated as
false-positive or out of scope must be stated explicitly with the reason;
silence is not an adjudication.

Then merge `--no-ff` to `main` and **push** (standing cadence is push-on-merge).

---

## Rule-#9 audit — round 1 adjudications

Three Opus audit briefs were dispatched (source-verification, design-holes,
cross-document/executability). All three were killed by an account rate limit
before producing findings; the source-verification brief was resumed. The
design-hole questions were then worked **inline** by the controller. That is a
weaker instrument than an independent agent — the controller wrote the documents
it is auditing — so the findings below are labelled with how they were reached,
and the two un-run briefs remain **outstanding work, not waived**.

| # | finding | verdict | action |
|---|---|---|---|
| A1 | ADR-0109:232-237's defense-in-depth claim is untrue for `WithTargetNode`: `NewReverseToNode` (`engine/trigger.go:369-370`) leaves `ReverseNode` empty, so the engine's terminal guard never fires and the facade pre-check at `runtime/processdriver_reverse.go:100-102` is the only defence — the exact arrangement ADR-0109 says it is not. The sibling in-flight guard at `engine/step_compensation.go:114-119` tests both shapes, showing the narrowness was an oversight. | **ACCEPTED — MAJOR** | Folded into ADR-0164 (Context + Decision point 3) and the spec §1.9; correction note added to ADR-0109. Strengthens the widening: it fixes a documented public API, not just a raw-struct footgun. |
| A2 | Could the widened guard poison a durable trigger queue? `UnmarshalTrigger` reconstructs `ToNode` from the journal (`internal/persistence/store/trigger_codec.go:174`), so a persisted partial rollback can be replayed. | **DISMISSED — no change** | Its only consumer is `Store.Entries` (`store_core.go:293`) via `kernel.Entries` (`runtime/kernel/ports.go:25`) — trigger *history* reads, not a redelivery path that calls `Step`. No queue can be poisoned. Verified, no doc change. |
| A3 | Could widening the sibling drain checks to `tokensInScopeSubtree` deadlock a scope exit? | **VERIFIED SAFE — no change** | The end token is consumed before the check (`engine/step_nodes.go:185`), and `stop=false` only governs whether `drive` keeps advancing *other* tokens, so nothing loops on the consumed token. A descendant scope's own exit path closes it and resumes into its parent, so the chain converges. This also confirms the instruction to leave the three **self**-checks exact-match: widening `tokensInScope(currentScopeID)` at `:235` would make a scope with a live non-interrupting event-sub-process child never take its exit branch at all. |
| A4 | ADR-0163 moves where task-cancel commands originate on the cancel-with-compensation path. | **ACCEPTED — MAJOR** | The plan understated it. Explicit prediction added to Phase 1 Step 1.12 so it is adjudicated as an expected move rather than silently re-baselined. |
| A5 | `endInstance`'s cursor clear is redundant at the `applyTerminate` site, since `stepCompensationFinish` already clears at `engine/step_compensation.go:552-567`. | **ACCEPTED — MINOR; MOVED to delivery 2b with `endInstance`.** | ⚠ The note landed in this plan's old Phase 5, which is now delivery 2b (this plan's Phase 5 is the godoc phase). The substance is preserved in the spec §1.8 table — whose cursor cell was corrected by C3 to say "(upstream, in `stepCompensationFinish:567`)" precisely so the redundancy reads as harmless — and in `docs/plans/2026-08-02-terminal-transitions.md`. The instruction stands: do **not** split `endInstance` into clearing and non-clearing variants to avoid one redundant zeroing. |

## Rule-#9 audit — round 2 (all three briefs completed)

All three Opus briefs were resumed after the rate limit and ran to completion.
They produced **31 findings**. The bundle as written at `419588d` was **not**
implementation-ready: two CRITICALs would have broken the build, one CRITICAL
left the headline defect unfixed at a site the ADR declared safe, and two more
left named defects with no test at all.

### Accepted — blocking, must land before Phase 1 begins

| # | finding | source | action |
|---|---|---|---|
| B1 | **Phase 1's tests target `package engine` files.** `engine/step_cancel_test.go:1` and `engine/state_test.go:1` are both `package engine` (white-box — the former calls unexported `cancelTokenWaits` at `:45`), so every `engine.` qualifier, the `engine_test` fixture `interruptingMessageBoundaryDef()`, and the whole `export_test.go` shim mechanism are unusable there. **Nothing in Phase 1 would compile.** | source-verify | **FOLDED.** Retargeted to `engine/step_cancel_tasks_test.go` (already `engine_test`, already has `findUpdateTasks`) and a new `engine/state_incidents_test.go`. |
| B2 | **`humantask.Claim` has no `ClaimedAt` field** — it is `At` (`humantask/humantask.go:59-64`). The Step 1.5 literal would not compile, and the plan's "source of truth" pointer named the wrong struct. | source-verify | **FOLDED.** Field corrected, pointer now cites `:59-64` for `Claim` and `:89-120` for `HumanTask`. |
| B3 | **The permanent wedge survives Phase 4 at `exitEventSubprocessScope`.** `engine/step_nodes.go:283` calls `closeScope` **unconditionally** — no child check to widen, and its only upstream gate is the exact-match self check at `:235`. ADR-0162 explicitly declared this site "unaffected". An event sub-process containing a sub-process wedges the instance exactly as defect 1 does. The same site never archives, so compensable work inside **any** event sub-process is dropped on its **normal** exit — defect 4 on the path defect 4 uses as its counter-example. | design-holes | **FOLDED.** ADR-0162 Context gains problem 6; Decision gains point 6 (descendant guard + archive at `:283`, archive at `:372`). Every scope close now archives. **Completed 2026-08-03**: the ADR had it but the *phase steps* did not — Phase 4 now carries Steps 4.1b/4.2b/4.3b (two tests, RED, implementation) and names `:283`/`:372` in its Files list, with two mutation rows. |

### Accepted — make an ADR claim true that is currently false

| # | finding | source | action |
|---|---|---|---|
| C1 | **Incidents outlive the token on every terminal path.** `removeIncidentsForToken` is wired only into `cancelTokenWaits`; four terminal sites drop tokens without it, so ADR-0163's "Incidents do not outlive the token they describe" is false. | design-holes | **FOLDED 2026-08-03, split across deliveries.** The *fix* (`s.Incidents = nil` in `endInstance`) is delivery 2b — `docs/plans/2026-08-02-terminal-transitions.md` Phase 1, which already carries it. The *false claim* lives in a 2a document, so it is corrected here: ADR-0163's Consequence is now scoped to "on every path that cancels a token", states why that is narrower, and forward-references ADR-0164. |
| C2 | **Five other `UpdateTask` emitters still shallow-copy**: `step_timers.go:90`, `step_triggers.go:379,411,428,628`. ADR-0163 claims "No `UpdateTask` … on any path". `step_timers.go:90` is the sharpest — it is one of the three places the engine writes `humantask.Cancelled` and dereferences a pointer straight into `s.Tasks`. | design-holes | **FOLDED 2026-08-03** → Phase 1 **Step 1.9b** (table of all five sites, two RED tests, and the grep as the verification), plus ADR-0163 Context (the five-site table) and Decision ("every `UpdateTask` emitter clones"). |
| C3 | **ADR-0164's table attributes the cursor clear to `applyTerminate`**, which contains no such assignment — the only one is `stepCompensationFinish:567`, one function upstream. The row is what justifies "route all eight through `endInstance`" as harmless. | cross-doc | **FOLDED 2026-08-03** → spec §1.8's table cell now reads "(upstream, in `stepCompensationFinish:567`)" with a note explaining why the distinction is what makes the unification harmless. ADR-0164 is delivery 2b's document. |
| C4 | **Exported godoc states behaviour this bundle falsifies.** `engine/command.go:206-208` and `engine/trigger.go:311-315` both say records are archived when a scope closes "**normally**". Worse, `engine/trigger.go:300-320` documents `CompensateRequested` without mentioning that a non-empty `ToNode` against a terminal instance now errors. Library-first project: the public API surface *is* the product. | cross-doc | **FOLDED 2026-08-03, split.** The *archiving* half is here: `engine/command.go` and `engine/trigger.go` are in the File structure table, and **Phase 5** (Steps 5.1–5.4) rewrites both doc comments. The *resume-guard* half (`CompensateRequested` / `NewCompensateRequested` / `NewReverseToNode` rejecting a non-empty `ToNode`) is delivery 2b, Step 2.4 of its plan, which already carries it. |

### Accepted — missing tests for claims the bundle already makes

| # | finding | source | action |
|---|---|---|---|
| T1 | **Defects 2 and 3 have no named test, no RED command, no GREEN command.** Step 3.1's prose promises three tests; the code block declares two, both for defect 4. Defect 2 is ADR-0162's *headline* decision. | cross-doc | **FOLDED 2026-08-03** → Phase 3 **Step 3.1** now declares five tests in a table, including both of these with their assertion sketches; Steps 3.2/3.6 name them in the RED/GREEN commands; both have mutation rows in Step 6.1. |
| T2 | **Defect 7 has no plan step at all**, though the self-review claims 5/7/8 → Phase 1. It is the defect reproduced on a **completed** instance and the one ADR-0161 deferred here. | cross-doc | **FOLDED 2026-08-03** → Phase 1 **Steps 1.2b / 1.2c** (test + RED), with the step-granularity trap called out: collapsed into one step, ADR-0161's filter already cancels the task and the test would be green from the start. Mutation row added. |
| T3 | **The `NewReverseToNode` vector has no test**, yet ADR-0164 calls it "materially stronger" and stakes the ADR-0109 correction on it. | cross-doc | **ROUTED 2026-08-03 → delivery 2b**, which already carries it: `docs/plans/2026-08-02-terminal-transitions.md` Step 2.1, with a mutation row at Step 4.2. No 2a action. |
| T4 | **Both ADR-0164 normalizations are untested** — the completion-site sweeps ("a claim the tests must now re-check rather than inherit") and `handleSubInstanceFailed`'s command reorder. | cross-doc | **ROUTED 2026-08-03 → delivery 2b**, which already carries both: `docs/plans/2026-08-02-terminal-transitions.md` Step 4.1, with mutation rows at 4.2. No 2a action. |
| T5 | **The archive key is the sub-process NODE id**, not the compensable activity's — `archiveCompensations` keys by `scope.NodeID`. Getting it wrong yields a test that asserts `len(records) == 0` on both sides of the fix and certifies nothing. | cross-doc | **FOLDED 2026-08-03** → a ⚠⚠ block at the head of Step 3.1 (`archiveCompensations` keys by `scope.NodeID` — `"fulfil"`, not `"charge"`), plus an inline reminder in the defect-4 assertion sketch requiring the record's `NodeID` be asserted too, so a wrong-key regression cannot pass as an empty map. |

### Verified SAFE — risks the bundle flagged, now discharged with evidence

| # | finding | source |
|---|---|---|
| S1 | **The `walkThrowScopeWide` attack target ADR-0162 could only argue.** After the interrupt the enclosing scope `E` holds zero tokens and never regains one: the event sub-process's child scope opens *under* `E` and its drain resumes in the **grandparent** (`engine/step_nodes.go:368-380`). No throw token can carry `ScopeID == E`; neither `recordCompensation` site can append to `E` afterwards; `StartRecordCount` is captured from the list actually read (`:1070`). **Proven, not argued** — but ADR-0162 declines to claim it proven without a test, so one is added. | design-holes |
| S2 | **All four `cancelTokenWaits` call sites are correct.** ID namespaces are disjoint by construction (`h`/`c`/`tm`/`t`/`s`/`inc`), so `tok.AwaitCommand` can never name another token's task; the `IsOpen()` guard makes a second visit a no-op; both non-interrupting branches never reach it; and `beginCompensation`'s resume outcomes resume at a node the cancelled `UserTask` can no longer reach, so cancelling is the fix rather than the bug. | design-holes |
| S3 | **The three completion sites cannot cancel work that outlives completion.** All are gated on `len(c.s.Tokens) == 0` with the end token already consumed at `:185`; a non-interrupting event sub-process always holds a token in its child scope, which blocks completion — and after Phase 4 even a grandchild does. | design-holes |
| S4 | **Gateway scope-equality matches are not the same defect.** `joinedAt` (`engine/step_gateways.go:62-67`) and the convergence scan (`:132-140`) match exact `ScopeID` deliberately: a token in a nested scope genuinely cannot reach a join in the parent scope, so subtree semantics would be wrong. No change. | controller |
| S5 | **`propagateError`'s direct-boundary `consume` bypasses `cancelTokenWaits`** (`engine/step_errors.go:339-364` uses `consumeTokenAs`), so it inherits none of ADR-0163's cleanup — but the token it consumes is by definition the **failing activity's**, never a parked `UserTask`. Not reachable for an open human task today. Recorded, no change. | design-holes + controller |

### Accepted — documentation and linkage

| # | finding | action |
|---|---|---|
| D1 | ADR-0162 closes the follow-up ADR-0130 explicitly deferred, without saying so | **FOLDED 2026-08-03** → ADR-0162 Context problem 2 quotes ADR-0130's deferral (`0130-closescope-descendant-cascade.md:86-91`) and names this ADR as that follow-up; Consequences gains a matching positive bullet. |
| D2 | ADR-0039's "archive on **normal** exit" becomes incomplete; its single-ownership invariant survives intact | **FOLDED 2026-08-03** → ADR-0162 Consequences, new "Supersession of a documented invariant" block: incomplete-not-wrong, and single ownership survives because `archiveCompensations` *moves* records and nils `scope.Compensations`. |
| D3 | ADR-0088's documented ordering ("task cancels prepended before the walk's commands") no longer describes the cancel-with-compensation path | **FOLDED 2026-08-03** → ADR-0163 Consequences (a bullet covering the ordering change **and** the deletion) + Phase 1 **Step 1.12b**. Two guards added beyond the finding: `engine/step_triggers.go:231`'s sweep is **live** and must not be deleted, and the deletion is proven by running the package, not by reasoning. |
| D4 | "`stepCompensationFinish`'s `walkPartial` branch" names a branch that does not exist — `:719-733` is `applyFinish`'s **shared** resume block (`:683`), common to all four resume modes | **FOLDED 2026-08-03** → both spec occurrences (§1.9 and §3.3) now say `applyFinish`'s shared resume block, reached by `walkPartial` via `stepCompensationFinish`'s plan, and state why sharing is the reason the guard cannot live there. ADR-0164 and the 2b plan already use the corrected phrasing. The 2a plan never used it. |
| D5 | Step 2.5 never runs `golangci-lint`, though the self-review says the `unused` risk is "verified at Step 2.5" | **FOLDED 2026-08-03** → Step 2.5 runs `golangci-lint run ./engine/...` and requires `LINT_EXIT=0`, with the fallback spelled out: no `//nolint`, land Phases 2+3 as one amend and record the decision. |
| D6 | Global Constraints and Step 6.2 give two different coverage commands for the same floor; the Global one needs Docker | **FOLDED 2026-08-03** → Global Constraints now names both commands and assigns each a purpose and a step; Step 6.2 states why `scripts/coverage.sh` is not needed there (`engine` has no generated files — verified: no `engine/*_mock.go`). |
| D7 | Two further reader surfaces change from archiving: root-level scope-wide `CompensationThrowEvent` consolidation (`engine/step_nodes.go:1057-1059`), and **branch selection** in `handleUnhandledError`/`handleCancelRequested`, which gate on `len(s.ArchivedCompensations) > 0` | **FOLDED** into ADR-0162 Consequences; the test it calls for was added 2026-08-03 as `TestTeardownArchiveSwitchesCancelToTheCompensationBranch`, the fifth test in Phase 3 Step 3.1, with its own mutation row. |

### Resolved by owner decision (2026-08-02)

| # | finding | source |
|---|---|---|
| O1 | **RESOLVED — owner elected to FIX, in delivery 2b.** Completion during an in-flight compensation walk returns `ErrTokenNotFound`. A throw consumes only its own token, so a sibling can complete the instance while an `InvokeAction` is in flight; the trailing `ActionCompleted` then matches nothing (`engine/step_triggers.go:84-91`). Pre-existing — `StatusCompleted` already broke the `:84` match — but ADR-0164 claims the invariant "becomes true by construction", which overstates it. Proposed fix: make `handleActionCompleted` treat an unmatched id against a **terminal** instance as a clean no-op, matching `handleResolveIncident`'s existing tolerance. | design-holes |
| O2 | **RESOLVED — owner ACCEPTED the split (2026-08-02).** 2a = ADR-0162 + ADR-0163; 2b = ADR-0164 on the merged 2a base. ADR-0164 shares zero symbols with ADR-0162 and touches a disjoint file set; its only stated dependency on Phase 1 is nominal. Meanwhile ADR-0163 and ADR-0164 each introduce an independent source of command-stream churn, and both insist every moved expectation be adjudicated individually — which one combined diff makes materially harder for the `/code-review` reviewer. | cross-doc |

Finding "HANDOVER.md is stale in three ways" was raised against `419588d` and is
**already resolved**: the handover was rewritten in place at `1d6afae`, before
that brief reported.

## Self-review notes

- **Spec coverage (2a).** 5/7/8 → Phase 1; 2/3/4 → Phase 3; 1 → Phase 4; plus
  the audit-added scope defects at `engine/step_nodes.go:283` and `:372` →
  Phases 3/4. Phase 2 carries no defect of its own — it is the shared machinery
  Phases 3 and 4 consume. Defects 6 and 9 and audit findings C1/O1 → delivery 2b.
  ✅ **All round-2 accepted findings are folded (2026-08-03).** B3, C2, C4
  (archiving half), T1, T2, T5 and D1–D7 landed in the phase steps and the
  documents; C1, C3, T3, T4, O1 and C4's resume-guard half were routed to
  delivery 2b, whose plan was source-checked and already carries every one of
  them. Each row in the round-2 tables above names its landing site.
- **Known gap, deliberately left to the implementer.** Phases 3, 4 and 5 give
  test *shapes* and assertions rather than complete fixture bodies for the
  multi-level nested definitions. Those fixtures are 40–80 lines of
  `model.ProcessDefinition` each and depend on API details
  (`activity.NewSubProcess` options, error-boundary construction) best read from
  `engine/step_subprocess_test.go` at implementation time rather than
  transcribed here and risk going stale. Every *assertion* is specified.
- **Type consistency.** `descendantScopeIDs` returns `map[string]bool` at every
  mention. `endInstance` takes `(Status, time.Time, Command)` and returns
  `[]Command` at every mention. `cancelScopeSubtree` takes
  `(*InstanceState, string, time.Time, CloseKind)` at every mention.
- **Unresolved risk for the audit.** Phase 2's helpers have no production call
  site until Phase 3. If the `unused` linter counts test-only usage as unused
  (it should not — golangci-lint runs on test files by default), Phases 2 and 3
  must land together. Verify at Step 2.5 rather than assuming.
