# Plan — stale-command filtering (ADR-0161)

## ▶ Progress

| | |
|---|---|
| Branch | `feat/stale-command-filter` (off `main` @ `f7b4884`) |
| Status | **implemented; awaiting the owner-run `/code-review` + `/security-review` gate** |
| Phases landed | **1, 2, 3 + mutation-verification** |

**Evidence.**

- TDD cycle observable for both new symbols: Phase 1 RED `undefined: liveAwaiters`
  (exit 1) → GREEN; Phase 2 RED `undefined: dropStaleTokenCommands` (exit 1) →
  GREEN. Phase 3 adds no symbol, so its RED is an **assertion** failure — and it
  landed the right way: all three positive controls passed
  (`closeKindOf == CloseKindTerminated`) while the three absence assertions
  failed with the stale command present, proving the fixtures genuinely emit the
  commands.
- `go build ./... && go vet ./...` clean. `golangci-lint run ./...` → **0 issues**.
- `go test -race -count=1 ./...` → exit 0, 0 FAIL, **0 skips**.
- `engine` coverage **90.8%**; `liveAwaiters` and `dropStaleTokenCommands` both
  **100%**. Repo total 73.4% (baseline 73.3%).
- **No pre-existing test broke** — the whole `engine` suite passed unchanged
  despite ~140 in-repo `.Commands` assertions, so the triage rule below was never
  invoked.
- **All 9 load-bearing guards mutation-verified**: filter call deleted (e2e FAIL);
  drop branch no-op (unit + e2e FAIL); cursor source removed (both FAIL);
  `IsTerminal` dropped (FAIL); FaF filtered (FAIL); empty id treated as stale
  (FAIL); `IsOpen` dropped (FAIL); `UpdateTask` emit removed (FAIL);
  `ScheduleTimer` filtered (FAIL). Both files restored byte-identical
  (`diff` clean).

⚠ One transient, cause confirmed by the owner: a first full-suite run failed
`action/email`, `casbinauthz` and `internal/authz/casbin` with
`docker info: 500 Internal Server Error`. **A concurrent session on the same
machine was saturating the Docker daemon.** All three passed in isolation, and
the later full `-race -coverprofile` run went green end to end (exit 0, 0 FAIL,
0 skips). Environmental and external — unrelated to this change (which touches
only `engine/`), and **not** evidence for the suite-speed backlog item, which
this run says nothing about either way.

**Phase-3 rows 6 and 9 added** as their own tests — the two cursor guards, which
the ADR calls the highest-risk detail in the delivery, now have end-to-end
coverage and not just unit coverage:

- `TestStaleCommandFilterKeepsCompensationInvoke` — a compensation `InvokeAction`
  whose throw token `startCompensationWalk` already consumed **survives**.
  Controls: `Compensating.ActiveCmdID` non-empty and `Status == StatusCompensating`.
- `TestStaleCommandFilterDropsCompensationInvokeWhenTerminal` — the same command
  is **dropped** once a sibling branch force-terminates. Two controls: the walk
  started **and** the instance went terminal, since the state under test is the
  combination.

Both were written against existing code, so both were mutation-verified rather
than taken on trust: removing the cursor source fails the first, dropping the
`IsTerminal` condition fails the second, and the file restored byte-identical.

### Stand-in review round (adversarial Opus, pre-gate)

One **High** and it was a real defect, not a nit:

- **The filter was a no-op on the unhandled-error path.** The terminal exclusion
  was scoped to the compensation cursor only, but
  `handleUnhandledError`'s immediate-fail branch (`engine/step_errors.go:244-255`)
  sets `StatusFailed` without clearing `s.Tokens`, so every sibling was re-admitted
  to the live set and kept its command — all three headline symptoms survived from
  a bare `StartInstance`. Fixed by returning an empty set whenever
  `s.Status.IsTerminal()`. RED first:
  `TestStaleCommandFilterDropsOnUnhandledError` failed with the control passing
  (instance terminal) and the action still present.
- **Two allocations added to every `Step`.** The filter built an awaiter map and
  copied the command slice even when the delivery contained nothing filterable.
  Added a `filterableCommand` pre-scan: the fast path is now **0 allocs/op**
  (65.8 ns/op vs 237.5 ns/op with 1 alloc on the rare filtering path).
- ⚠ **That optimization silently un-pinned a guard, and I caught it by
  re-running the mutation.** The empty-id row began passing through the fast path
  without ever reaching `stale("")`, so deleting the empty-id guard left the suite
  green. Fixed by pairing the empty-id command with a stale one; the mutation now
  fails again and `dropStaleTokenCommands` is back to 100%.
- Doc/test hygiene: corrected a doc comment that overstated the `AwaitHuman`
  cancel (only *open* records) and mis-cited `handleDeadlineFired` as its
  precedent when the real one is `cancelOpenTasks`; a comment naming the wrong
  function; `string(rune('1'+i))` id arithmetic; import grouping; two assertions
  that could not fail; added `Claimed`-record and order-preservation rows.
- Queued, not fixed: the cross-step orphaned-task leak in `propagateError`
  (`HANDOVER.md` item 7), recorded in ADR-0161's Consequences so the step-boundary
  asymmetry is not read as intentional.

### Owner-run `/code-review` round (gate step 2)

Three findings, **all three folded** into the feature commit via `--amend`:

- **Medium — a suppressed side effect left no trace anywhere.** The filter took no
  `ctx` and emitted nothing, so a dropped compensation `InvokeAction` (the
  delivery's own `TestStaleCommandFilterDropsCompensationInvokeWhenTerminal`
  fixture) discarded a refund with no log, metric or history entry for an operator
  to find. Fixed: `ctx` threaded from `Step`, one `slog.WarnContext` per drop
  carrying `instance_id` / `command_kind` / `correlation_id`, matching `drive`'s
  convention at `engine/step.go:187` (ADR-0129). RED first — the five-row log
  table failed to build (`too many arguments in call to dropStaleTokenCommands`),
  each row carrying a positive control so an absent record cannot pass by the
  branch simply never running.
- **Low — the escaping `UpdateTask` aliased live engine state.** `UpdateTask{Task:
  *task}` shared the `Claim`/`Completion` pointees, the `Vars` map and the actor
  slices with the record about to be committed. Latent only because both in-repo
  stores copy on ingest, but `TaskStore` is public API. Fixed with
  `task.Clone()`. RED first: the new row failed on both the `Claim` pointee and
  the `Vars` map before the change.
- **Low — the fast-path benchmark measured a shape the engine rarely produces,
  and the comment claiming otherwise was wrong.** Every ServiceTask entry, retry
  re-invoke, UserTask entry, CallActivity entry and completion-action park emits
  exactly one filterable command, so the *typical* step misses the pre-scan.
  Fixed the comment and added `BenchmarkDropStaleTokenCommandsLiveAwaiter` (the
  real typical shape). Numbers on an M4 Pro: pre-scan hit **6.1 ns/op, 0 allocs**;
  typical live-awaiter step **57.5 ns/op, 1 alloc**; drop path **74.0 ns/op, 2
  allocs** (measured with a discarding handler, so production pays its handler on
  top).

**Re-running the mutations after the fixes found a guard nothing pinned** — the
same class of miss as the earlier pre-scan lapse, caught the same way. Deleting
the switch's `!cmd.FireAndForget` exemption left the suite **green**: the only
fire-and-forget row carried that command *alone*, so the pre-scan returned before
the switch was ever reached. Added a row pairing it with a live sibling to force
the slow path; the mutation now fails. **12 mutations run this round** (terminal
early-return, empty-id guard, `IsOpen` guard, `Clone`, the FaF exemption, the
cursor source, all three `logDrop` calls, the `AwaitHuman` drop branch, and the
pre-scan forced both ways) — all killed, file restored byte-identical.

Post-fix state: `engine` **90.8%**, all three filter functions **100%**,
`go test -race ./engine/...` exit 0, `golangci-lint run ./engine/...` **0
issues**, `go build ./...` clean. Adjudicated but **not** changed here: the
identical shallow copy in `cancelOpenTasks` (`engine/state.go:302`), which
predates this ADR and belongs to whichever delivery touches that sweep — queued.

**Still not done from the plan:** Phase-3 rows 4, 5, 5b, 7, 10 and 11 (the
non-terminal destroyer, intra-handler destroyer, fire-and-forget survival,
injected-`IDGenerator` and destroyer-#4/#5 shapes) and the fuzz post-condition.
Every one of those behaviours is covered by the 18-row unit table and is
mutation-verified there; what is missing is e2e breadth, not a guard.
| Spec | `docs/specs/2026-07-31-stale-command-filter.md` |
| ADR | `docs/adr/0161-stale-token-command-filtering.md` |
| Delivery | **1 of 3** — see the spec's header for the ordering and why |
| Prior audits | 5 Opus briefs over 2 rounds against the superseded 3-ADR bundle (tags `audit-signal-arm-fanout-r1`, `-r2`); the findings that apply to ADR-0161 are folded here and listed at the bottom |

Deliveries 2 (ADR-0162 scope lifecycle) and 3 (ADR-0158 signal fan-out) are
drafted on `parked/scope-and-fanout-design` and are **not** part of this branch.

## Source-verified facts

Verified against `main` @ `f7b4884`. Two prior audit rounds checked 80 citations
across the superseded bundle; these are the ones that survive into this delivery,
re-confirmed.

- **Insertion point.** `Step` is `engine/step.go:77-102`. `dispatch` returns at
  `:92`; the id-error check is `:96-98`; the seam scrub is `:100`. The filter
  goes between `:98` and `:100`. `dispatch` (`:107`) is the single funnel — every
  handler returns through it.
- **Every non-error handler return is `StepResult{State: *s, …}`** — a shallow
  struct copy. The filter must therefore take `&res.State`, not the working
  clone `sp`.
- **Destroyers.** `forceTerminate` `engine/step_nodes.go:478-504` (`Tokens = nil`
  at `:486`, `halt=true`); `drive` returns accumulated cmds at
  `engine/step.go:199-204`. `cancelTokenWaits` `engine/step_cancel.go:12-30`.
  Interrupting ESP `engine/step_eventsubprocess.go:189-207`.
- **All seven `tok.AwaitCommand` assignment sites:**

  | line | value | command |
  |---|---|---|
  | `step_nodes.go:58` | `cmdID` | `InvokeAction` (`emitActionInvoke`, `:51-58`) |
  | `step_triggers.go:541` | `cmdID` | completion-action `InvokeAction` (`:534-541`) |
  | `step_nodes.go:679` | `taskID` | `AwaitHuman` (`:677-679`) |
  | `step_nodes.go:904` | `cmdID` | `StartSubInstance` (`:898-904`) |
  | `step_nodes.go:709` | `timerID` | `ScheduleTimer`, intermediate catch (`:700-709`) |
  | `step_triggers.go:304` | `timerID` | `ScheduleTimer`, retry backoff |
  | `step_nodes.go:838` | `"evtgw:"+tok.ID` | none — gateway sentinel |

- **The compensation false-positive is reachable from `drive`.**
  `startCompensationWalk` `engine/step_nodes.go:982-993` consumes the throw token
  at `:983`, sets `cursor.ActiveCmdID` at `:989`, appends `compensationInvoke` at
  `:991`. `compensationInvoke` `engine/step_compensation.go:323-329` sets no
  `FireAndForget`. Consumed at `engine/step_triggers.go:84`.
- **`InvokeAction` has exactly four construction sites**: `step_cancel.go:38`
  (fire-and-forget), `step_compensation.go:325`, `step_nodes.go:51`,
  `step_triggers.go:534`. The taxonomy is complete.
- **`cancelOpenTasks`** `engine/state.go:297-306` marks open tasks `Cancelled`
  **and emits `UpdateTask` for each**. `forceTerminate` calls it at
  `step_nodes.go:489`. So in a **force-terminate** fixture the task record is
  already `Cancelled` before the filter runs, and an `UpdateTask` **is** present.
  Test fixtures must account for this — see Phase 3b rows.
- **`cancelTokenWaits` never touches `s.Tasks`**, so a **non-terminal** destroyer
  (interrupting boundary / interrupting ESP) leaves the record genuinely open.
  That is the only fixture shape in which the cancel is load-bearing.
- **No new task helper is needed.** `InstanceState.TaskByID`
  (`engine/state.go:279`) exists; the idiom
  `if t := s.TaskByID(id); t != nil { t.State = humantask.Cancelled }` is already
  used at `engine/step_timers.go:38,88-89`.
- **Projections** filter on `IsOpen()`: `service/instance.go:75`,
  `runtime/view/instance_actionable.go:65`. `NodeVisit.TaskID` is a documented
  wire link (`service/instance.go:154-156`, ADR-0145).
- `nextID` returns an injected generator's value **verbatim, without a prefix**
  (`engine/idgen.go:50-58`); the per-kind prefixes are `engine/step_state.go:154-162`.
- **Constructors** (all verified, with the signatures a fresh session will need):
  `activity.NewServiceTask(id, activity.WithTaskAction(name))`,
  `activity.NewUserTask(id, ...)`,
  `activity.NewCallActivity(id, model.Qualifier, ...)` — **takes a `Qualifier`,
  not a string** — `activity.NewSubProcess(id, *model.ProcessDefinition, ...)`,
  `activity.WithCompensateAction(action)`, `gateway.NewParallel(id)`,
  `gateway.NewEventBased(id)`,
  `event.NewEnd(id, event.WithForceTermination(reason, outcome))`,
  `event.NewCompensateThrow(id, ...)`, `event.NewBoundary(id, host,
  event.WithSignalName(n))`, `event.WithBoundaryAction(name)`
  (`definition/event/options.go:268` — needed by P3 row 7, the only e2e witness
  for the fire-and-forget exclusion), `event.WithErrorCode(code)`.
- **Termination outcomes** are `event.OutcomeComplete` (iota 0) and
  `event.OutcomeAbort` (`definition/event/event.go:53-57`). `OutcomeAbort` ⇒
  `StatusTerminated` **plus a `FailInstance` command**
  (`engine/step_nodes.go:492-498`); `OutcomeComplete` ⇒ `CompleteInstance`. Rows
  must name one, not hedge between them.
- **There is no event-sub-process constructor.** An ESP is an
  `activity.NewSubProcess(id, innerDef)` whose nested definition has an
  **event-triggered start** — `event.NewStart(sid, event.WithSignalName("sig"))`;
  `eventSubprocessNested` (`engine/step_eventsubprocess.go:39-52`) is what
  recognises it. `event.WithNonInterrupting()` (`definition/event/options.go:139`)
  is a `StartOption` on that inner start, not a sub-process option. Working
  fixtures: `engine/step_subprocess_eventstart_test.go`,
  `engine/step_eventsubprocess_multistart_test.go`.
- Fixtures that exist: `parkedAtUserTask` (`close_kind_test.go:31`),
  `closeKindOf(st engine.InstanceState, nodeID string) (engine.CloseKind, bool)`
  — **two args, two returns** (`close_kind_test.go:20`), `stepToParked`
  (`boundary_error_matching_test.go:148`), `findTokenByNodeID`
  (`retry_test.go:63`), `interruptingMessageBoundaryDef`
  (`step_boundaries_test.go:49`).
- `engine.Step` takes **five** arguments.

## Conventions

- **TDD strict.** One RED/GREEN cycle **per symbol**, each with its own `Bash`
  invocation, so the transcript shows a red state for every new symbol
  (CLAUDE.md's Forbidden Patterns are per-symbol, not per-phase).
- `SCRATCH=/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-wrkflw/3f7ff08e-5a60-4e64-a51f-588dc6a890ae/scratchpad`,
  one file per phase.
- `liveAwaiters` is unexported with no black-box path ⇒ its test is
  `package engine`. Everything else is `package engine_test`.
- Table tests per the `table-test` skill: `assert` closure per case, optional
  `ctx` modifier, `t.Context()`.
- Single package ⇒ implemented **inline**, not fanned out to subagents.
- `slices`/`maps` over `sort` (Go 1.25, `golang-modernize`).

> ### ⚠ Positive controls are mandatory
>
> Most rows in Phase 3 assert that a command is **absent**. Absence is satisfied
> two ways: the filter dropped it, **or it was never emitted**. A fixture whose
> parking branch never ran passes every one of those rows against an empty
> implementation.
>
> **Every "dropped" row MUST additionally assert that the node was entered and
> abnormally closed in this step**, via
> `closeKindOf(res.State, "<node id>")` → `(engine.CloseKindTerminated, true)` for
> force-terminate fixtures, `(engine.CloseKindBoundaryInterrupted, true)` for
> interrupt fixtures. That is the positive control proving the command existed
> before the filter ran.
>
> **Branch declaration order is load-bearing.** `forkParallel` places tokens in
> `Outgoing()` order, `drive` takes `s.firstActive()` (`engine/step_state.go:64-71`),
> and `forceTerminate` returns `halt = true` so `drive` returns immediately
> (`engine/step.go:199-204`). In every fork fixture the **parking branch's
> sequence flow must be declared before the destroying branch's**, or the parking
> branch is never driven at all.

---

## Phase 1 — `liveAwaiters`

**Files:** new `engine/step_stale_commands.go`, `engine/step_stale_commands_test.go`
(`package engine`).

RED table:

| # | case | assertion |
|---|---|---|
| 1 | two parked tokens with distinct `AwaitCommand` | both ids present |
| 2 | an **active** token with empty `AwaitCommand` | `""` is **not** in the set |
| 3 | no tokens, no cursor | empty set, not nil-deref |
| 4 | `Compensating.ActiveCmdID` set, `Status == StatusCompensating` | the cursor id **is** present |
| 5 | `Compensating.ActiveCmdID` set, `Status == StatusTerminated` | the cursor id is **absent** |
| 6 | `Compensating.ActiveCmdID` set, `Status == StatusCompleted` | absent |
| 6b | `Compensating.ActiveCmdID` set, `Status == StatusFailed` | absent — the third `IsTerminal` branch (`state.go:53-58`) |
| 7 | cursor empty, tokens present | only token ids |
| 8 | tokens with `AwaitCommand = "evtgw:t1"` and a timer id | **both present** — pins the ADR's "harmless under-filtering" claim, which is currently asserted but untested |

```bash
go test -run 'TestLiveAwaiters' ./engine/... > $SCRATCH/p1-red.txt 2>&1; echo "exit=$?"; tail -20 $SCRATCH/p1-red.txt
```

GREEN:

```go
// liveAwaiters returns the ids something in s is still waiting on: every
// surviving token's non-empty AwaitCommand, plus Compensating.ActiveCmdID when
// the instance is not terminal.
//
// The cursor is NOT optional. startCompensationWalk (step_nodes.go:982-993)
// consumes the throw token at :983 before emitting a non-FireAndForget
// InvokeAction, so a token-only set would drop that command and hang the walk.
// The terminal exclusion is the mirror image: nothing clears the cursor on a
// terminal transition. See ADR-0161.
func liveAwaiters(s *InstanceState) map[string]struct{}
```

---

## Phase 2 — `dropStaleTokenCommands`

**Files:** same pair. Still `package engine` for the unit rows; the end-to-end
rows land in Phase 3.

RED table (unit, over a hand-built state + command slice):

| # | case | assertion |
|---|---|---|
| 1 | `InvokeAction{FireAndForget:false}` with no awaiter | dropped |
| 2 | `InvokeAction{FireAndForget:true}` with no awaiter | **kept** |
| 3 | `InvokeAction` whose `CommandID` is a live token's `AwaitCommand` | kept |
| 4 | `InvokeAction` whose `CommandID` equals `Compensating.ActiveCmdID` | **kept** |
| 5 | `AwaitHuman` with no awaiter, record present in `s.Tasks` | dropped **and** that record's `State == humantask.Cancelled` |
| 6 | `AwaitHuman` with a live awaiter | kept, record untouched |
| 7 | `StartSubInstance` with no awaiter | dropped |
| 8 | `StartSubInstance` with a live awaiter | kept |
| 9 | `ScheduleTimer` for a cancelled token | **kept** |
| 10 | one command of each of the nine never-filtered kinds, no awaiters | all nine kept |
| 11 | nothing stale | returned slice equals the input slice element-for-element |
| 12 | two stale `AwaitHuman`s, two records | both dropped, **both** records `Cancelled`, **two** `UpdateTask`s emitted — multiple-drop guard: the loop must not stop at the first stale command |
| 13 | stale `AwaitHuman` whose `TaskID` has no record in `s.Tasks` | dropped, no panic, no `UpdateTask` |
| 14 | stale `AwaitHuman` whose record is already `humantask.Completed` | dropped; record **still `Completed`**; **no** `UpdateTask` — pins the `IsOpen()` guard, which is otherwise deletable with the suite green |
| 15 | stale `InvokeAction{CommandID: ""}` with an active token whose `AwaitCommand == ""` | **kept** — a malformed id must keep failing loudly, not park the instance silently |
| 16 | `cmds` is nil | returns nil/empty, no panic |
| 17 | state has no tokens at all, one stale non-FaF `InvokeAction` | dropped, no panic |
| 18 | `s.Tasks` is nil, one stale `AwaitHuman` | dropped, no panic |

```bash
go test -run 'TestDropStaleTokenCommands' ./engine/... > $SCRATCH/p2-red.txt 2>&1; echo "exit=$?"; tail -20 $SCRATCH/p2-red.txt
```

GREEN:

```go
// dropStaleTokenCommands returns cmds without the commands whose awaiter s no
// longer holds, and cancels the human-task record of any dropped AwaitHuman
// (emitting an UpdateTask for it). It mutates s; callers pass &res.State so the
// change is visible in the returned StepResult. A command whose correlation id
// is EMPTY is always kept — see ADR-0161. See the ADR for the kinds
// deliberately untouched.
func dropStaleTokenCommands(s *InstanceState, cmds []Command) []Command
```

Task cancellation uses the existing accessor — no new helper — and mirrors
`handleDeadlineFired` (`engine/step_timers.go:88-91`) by emitting the command:

```go
if t := s.TaskByID(ah.TaskID); t != nil && t.IsOpen() {
    t.State = humantask.Cancelled
    kept = append(kept, UpdateTask{Task: *t})
}
```

---

## Phase 3 — wire into `Step`, end-to-end

**Files:** `engine/step.go`, new `engine/step_stale_commands_e2e_test.go`
(`package engine_test`).

RED table (black-box through `engine.Step`):

Every row below carries its positive control (see Conventions). Fixtures are
spelled out because three of the first draft's rows were unconstructible.

| # | fixture | assertion |
|---|---|---|
| 1 | `Start → Fork ⇒ { f1: A=ServiceTask ; f2: End(WithForceTermination("kill", event.OutcomeAbort)) }`, **f1 declared first**. `StartInstance` | **no signal involved.** No `InvokeAction` for A; `FailInstance` present (Abort ⇒ Fail, not Complete); **control:** `closeKindOf(res.State, "A") == (CloseKindTerminated, true)` |
| 2 | same, A = `CallActivity(id, model.Qualifier{...})` | no `StartSubInstance`; same control |
| 3 | same, A = `UserTask` | `AwaitHuman` dropped; **exactly one** `UpdateTask` for it (from `cancelOpenTasks`, not a second from the filter — the `IsOpen()` guard makes the filter a no-op here); `res.State.Tasks[0].State == Cancelled`; same control |
| 4 | **non-terminal destroyer.** Interrupting signal boundary on host `H` → `user`(UserTask); plus a **root-level interrupting event sub-process** on the same signal whose body **must park** (`Start(WithSignalName) → ServiceTask → End`). One `SignalReceived` | `AwaitHuman` for `user` dropped; its record `Cancelled`; **one `UpdateTask` for it, emitted by the filter**; **`res.State.Status == engine.StatusRunning`** — the guard that no terminal path ran `cancelOpenTasks`, which would make the row vacuous; **control:** `closeKindOf(res.State, "user") == (CloseKindBoundaryInterrupted, true)` |
| 5 | same shape, boundary target is a `ServiceTask` | its `InvokeAction` dropped; same status guard and control |
| 5b | **intra-handler destroyer, single construct.** Event-based gateway with a signal arm on `"go"`; the winning branch target is `svc`(ServiceTask) carrying an interrupting signal boundary **also** named `"go"`. One `SignalReceived{"go"}` | tier 1's drive parks `svc` and emits its `InvokeAction`; tier 2 then finds the brand-new arm and cancels the host ⇒ that `InvokeAction` is **absent**; `res.State.Tokens` holds only the boundary-routed token. No ESP, no terminate |
| 6 | **three steps.** `Start → S(ServiceTask, WithTaskAction, WithCompensateAction) → H(ServiceTask) → End`; boundary `b` on `H` `WithSignalName("sig")` → `t`=`CompensateThrow` → `End2` (**the throw MUST have an outgoing flow**, or `enter` auto-advances and no walk starts). `StartInstance`, `ActionCompleted(cmd(S))`, `SignalReceived("sig")` | **guard first:** `require.NotEmpty(res.State.Compensating.ActiveCmdID)`; then the `InvokeAction` with that `CommandID` is **present** |
| 7 | boundary carrying `event.WithBoundaryAction("notify")` whose routed token is then cancelled | the FaF `InvokeAction` **survives** — the only e2e witness for the FaF exclusion |
| 8 | ordinary `StartInstance` on a linear definition, nothing cancelled | `res.Commands` equals an explicitly enumerated expected slice |
| 9 | **fork, not ESP.** `Start → S(ServiceTask, WithCompensateAction) → Fork ⇒ { f1: CompensateThrow T → End ; f2: End(WithForceTermination) }`, **f1 declared first**. `StartInstance`, `ActionCompleted(cmd(S))` | `res.State.Compensating.ActiveCmdID != ""` **and** `res.State.Status.IsTerminal()` (positive controls that the state under test was reached), **and** no `InvokeAction` with that `CommandID` — the terminal cursor exclusion. ⚠ The first draft built this on row 4's ESP, which cannot work: tier 2 sets `StatusCompensating` and tier 3's guard is `Status != StatusRunning` (`step_eventsubprocess.go:155-159`), so the ESP silently no-ops |
| 10 | row 1 with `StepOptions{IDGenerator: <stub returning a FRESH unique id per call>}` | same outcome — pins the no-prefix injected-generator configuration the runtime ships. A constant-returning stub would make every id equal and invert the test |
| 11 | **destroyers #4/#5.** `Start → S(ServiceTask, WithCompensateAction) → Fork ⇒ { f1: A=ServiceTask (parks) ; f2: End(WithErrorCode) unhandled }`, f1 first; one completed compensable record | A's `InvokeAction` **absent**; the `compensationInvoke` matching `res.State.Compensating.ActiveCmdID` **present**; `res.State.Status == engine.StatusCompensating`. One row covering both the error-propagation and `beginCompensation` destroyers, and a second independent witness for the cursor guard |

**Impact assessment (do this before writing GREEN).** There are ~140 in-repo
sites touching `.Commands`, some asserting by index or exact length —
`engine/step_timers_test.go:200,396`, `engine/step_gateways_test.go:140-141,156,335`,
`engine/step_compensation_throw_test.go:97,246,345,448,549,560`,
`engine/step_compensation_error_cancel_test.go:126,184,193` among them. Enumerate
the tests whose fixture contains a same-`Step` destroyer **and** an index/length
assertion, and record the expected delta per test. Two were checked and are
**unaffected**: `engine/end_force_termination_test.go` (uses the `firstCommand[T]`
helper at `:34-42` and counts only `UpdateTask`s) and
`runtime/processdriver_test.go:143-151` (parks normally, no destroyer).

**Triage rule for newly-failing tests.** For every pre-existing test that goes red
after wiring, record in the Progress block: the test name, the command that
disappeared, and the one-line justification that its awaiter was genuinely
cancelled in the same step. **A failure you cannot justify that way is a defect in
the filter, not a stale assertion** — do not weaken the assertion.

**RED for this phase is an assertion failure, not a compile failure** — it adds no
new symbol. Write `step_stale_commands_e2e_test.go` in full and run it **before**
touching `step.go`; rows 1–3 must fail with the stale command still present. A
session that adds the one-liner first (it is one line — the temptation is
obvious) produces a green-only transcript and no audit trail.

```bash
go test -run 'TestStaleCommandFilterE2E' ./engine/... > $SCRATCH/p3-red.txt 2>&1; echo "exit=$?"; tail -30 $SCRATCH/p3-red.txt
```

GREEN: the one line in `Step`, between `:98` and `:100`:

```go
res.Commands = dropStaleTokenCommands(&res.State, res.Commands)
```

Then the suite-wide invariant, which is the only artefact here that can catch a
**new** parking mechanism added by delivery 2 or 3 — thirty example rows cannot.
Wire a post-condition into the existing `engine/step_fuzz_test.go` harness: after
every successful `Step`, every `InvokeAction` with `FireAndForget == false`, every
`AwaitHuman` and every `StartSubInstance` in `res.Commands` must have its id in
`liveAwaiters(&res.State)` — or be empty. ~15 lines.

---

## Phase 4 — mutation-verify, then the gate

### 4.1 Mutation-verify

Snapshot the file, break the guard, confirm the test **fails**, restore, `diff`
to prove byte-identical.

| guard | mutation | must fail |
|---|---|---|
| **the filter runs at all** | delete the `dropStaleTokenCommands` line from `Step` | **P3 rows 1, 2, 3, 4, 5, 5b, 11** |
| **the drop branch** | make `dropStaleTokenCommands` `return cmds` unchanged | **P2 rows 1, 5, 7; P3 rows 1, 2, 3** |
| compensation cursor in the live set | drop the cursor source from `liveAwaiters` | P1 row 4, P3 rows 6 and 11 |
| terminal exclusion | drop the `IsTerminal` condition | P1 rows 5, 6, 6b; P3 row 9 |
| fire-and-forget exclusion | filter FaF too | P2 row 2, P3 row 7 |
| empty-id skip in the set | include `""` in the live set | P1 row 2 |
| **empty-id keep on the command side** | drop empty-id commands too | **P2 row 15** |
| `AwaitHuman` record cancel | delete the cancel line | P2 row 5, P3 row 4 |
| **`IsOpen()` guard** | drop `&& t.IsOpen()` | **P2 row 14, P3 row 3** (a second `UpdateTask` appears) |
| **`UpdateTask` emit** | cancel the record but emit nothing | **P2 rows 5, 12; P3 row 4** |
| `&res.State` call site | pass `sp` **and** make the cancel rebuild-and-reassign `s.Tasks` | P3 row 4 |
| `ScheduleTimer` exclusion | filter it | P2 row 9 |

The first two rows are the ones that matter most: without them, 15 of the 30 rows
were never proven capable of failing, including every row covering the ADR's
headline behaviour. If either mutation leaves a listed row green, that row is a
vacuous pass and must be repaired **before** the gate.

A test that still passes with its guard broken certifies nothing — five such
tests shipped in ADR-0159 and were caught only at review.

### 4.2 Verification checklist

- [ ] `go build ./... && go vet ./...` clean
- [ ] `go test -race -count=1 ./engine/...` exit 0
- [ ] `go test -race -count=1 ./...` from repo root, exit 0, 0 FAIL, 0 skips (Docker up)
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — `engine` ≥ 85%, every listed hot path and failure branch covered
- [ ] `golangci-lint run ./...` — 0 issues
- [ ] every new symbol has an observable RED state in the transcript
- [ ] mutation-verify evidence recorded in this Progress block
- [ ] adversarial Opus stand-in review run first, to cut rework
- [ ] **owner** runs `/code-review` and `/security-review` (both are
      `disable-model-invocation`; the session cannot invoke them); all findings
      fixed and folded with `--amend`
- [ ] `docs/plans/HANDOVER.md` updated **in place**
- [ ] `MEMORY.md` index line + topic file updated
- [ ] merge `--no-ff` to `main`, then **push**

### 4.3 Commit

One feature bundle: implementation + tests + spec + ADR + this plan, in a single
commit with the final message. The docs are committed first on this branch, so
stage the implementation and `git commit --amend` (**not** `--no-edit` — the
first message says "design bundle only" and must be rewritten to describe the
whole delivery).

## Findings folded from the superseded bundle's audits

Five Opus briefs over two rounds attacked the earlier 3-ADR packaging. These are
the findings that apply to ADR-0161 and how each is resolved here.

| finding | resolution |
|---|---|
| Filter scoped to `handleSignalReceived` on a false "cannot arise" premise (found by all 3 round-1 briefs; `forceTerminate` repro verified by the controller) | Hoisted to `Step`. Owner decision. |
| Call site contradicted itself — "before the `StepResult` literal is built" vs "in `Step`, after `dispatch`" — and the prescribed form worked only by backing-array aliasing (both round-2 briefs) | `res.Commands = dropStaleTokenCommands(&res.State, res.Commands)`. Mutation row added. |
| Compensation cursor survives a terminal transition, so a compensation `InvokeAction` outlives a force-termination in the same step | Terminal exclusion in `liveAwaiters` + P1 rows 5–6 + P3 row 9. |
| Deleting the `AwaitHuman` record dangles `NodeVisit.TaskID` (ADR-0145) and is unnecessary (`IsOpen()` filtering) | Mark `Cancelled` instead. |
| ADR claimed "no `UpdateTask` is emitted" while its own fixture triggers `cancelOpenTasks`, which emits one — making the test row unpassable and the `Cancelled` assertion vacuous (both round-2 briefs) | ADR reworded: the **filter** emits none; a sibling terminal path may, and that composes. Force-terminate fixtures moved to rows 1–3; the load-bearing cancel is pinned by row 4's non-terminal destroyer. |
| A new `cancelTaskByID` duplicated the existing `TaskByID` idiom and was one of three symbols making the phase a batching violation | Dropped; use `TaskByID` inline. |
| Phases batched multiple symbols into one RED | One symbol per phase, each with its own `Bash` verification line. |
| "Four `AwaitCommand` parking sites" | There are **seven**; table corrected, the extra three shown harmless. |
| Citations `:51-57` / `:676-678` off by one | `:58` / `:679`. |
| "Id namespaces are disjoint under the fallback generator **and** an injected `IDGenerator`" — false: `nextID` returns an injected value verbatim with no prefix | Reworded; disjointness under injection comes from the generator's uniqueness contract. P3 row 10 pins the injected configuration. |
| `ScheduleTimer` "self-correcting" is false for an intermediate timer catch (no `timerRecord`) | Rationale corrected; recorded as an out-of-scope gap. |
| ADR-0161 never labelled itself breaking although the other two did | Breaking consequence added. |
| Row asserting an empty-id command was not observable black-box | Moved to the `package engine` unit table (P1 row 2). |
| Rule-#10 handover stale and scheduled post-implementation | `HANDOVER.md` updated with this bundle; checklist keeps a second post-implementation update. |
| `/code-review` treated as session-runnable | Marked owner-only. |
| Delivery had grown to 3 ADRs / ~45 test rows / 8 symbols in one gate | **Split into 3 deliveries, fixes first.** Owner decision. This is delivery 1. |

Findings that belong to deliveries 2 and 3 — the `descendantScopeIDs` root-guard
asymmetry, the sub-process drain-check wedge, zombie scopes, compensation
archiving, cross-step orphaned tasks, dangling incidents, the arm-identity ABA
guard, interrupting-first ordering, and the fan-out test table — are recorded in
the drafts on `parked/scope-and-fanout-design` and must be re-audited with those
bundles.
