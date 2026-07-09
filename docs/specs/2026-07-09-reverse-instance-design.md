# `ProcessDriver.ReverseInstance` — reverse/rollback facade (revised)

Date: 2026-07-09
Status: Approved (design) — pending implementation plan
Scope: new public facade + a targeted engine enhancement (compensate-all-then-resume-at-start)

Supersedes `docs/specs/2026-07-08-reverse-instance-design.md` (same feature; this revision
re-grounds the spec against `main` at `7b90cd0` after the input-validation and
option-consolidation/completion-action merges, and folds in the confirmed design decisions).

## Context

Rolling back a running instance today requires the consumer to hand-build an engine-internal
trigger: `driver.ApplyTrigger(ctx, def, id, engine.NewCompensateRequested(clk.Now(), toNode))`
(still done in `examples/scenarios/compensation_saga/main.go:119-120`). That leaks
`engine.NewCompensateRequested` into consumer code, exposes the raw `toNode string` (with a magic
`""`), and offers no ergonomic, safe surface — unlike the sibling facades `DeliverMessage`,
`BroadcastSignal`, `CancelInstance`, `ResolveIncident`.

**Compensation model facts (re-confirmed against `7b90cd0`):**

- Compensation records are **appended per execution** (`recordCompensation`, `engine/state.go:899`
  — never keyed by node). A node visited N times in a loop yields N records; a reverse walk replays
  all N **LIFO (newest visit first)** — `RootCompensations` walked `len-1 → 0`. So cyclic processes
  (reject / re-escalate loops) reverse correctly today — there is just no test or example proving it.
- `CompensateRequested{ToNode string}`: `ToNode=="X"` compensates records completed *after* X
  (exclusive, reverse order — `step_compensation.go:137-171,229-234`), drops a token at X, resumes
  `StatusRunning` (`step_compensation.go:322-336`). `ToNode==""` compensates everything and
  **terminates** `StatusTerminated` (`step_compensation.go:338-348`; admin path enters with
  `finalStatus=0`). This terminate path is shared with the cancel/error flow.
- `CancelInstance` already compensates **and** terminates (`TestCancelHandler_WithCompensateAction`,
  `engine/step_cancel_handlers_test.go:238`).
- The compensation-finish path `stepCompensationFinish` (`engine/step_compensation.go:268-377`)
  already has a **resume-after-compensation** branch (ADR-0039 throw-walk): when
  `compensationCursor.ResumeNode != ""` it deletes the archive, sets `StatusRunning`,
  `placeTokenInScope(resumeNode, resumeScope, at)`, and drives forward
  (`step_compensation.go:285-319`). This is the machinery the resume-at-start mode reuses.

**Item-4 interaction (new since the prior spec — must be honored):**

- **UserTask/ReceiveTask compensation now flows exclusively through the completion-action
  round-trip.** A UserTask/ReceiveTask records compensation only when its completion action
  completes: `parkOnCompletionAction` (`engine/step_triggers.go:446-465`) parks the token, and
  `handleActionCompleted` (`step_triggers.go:68-72`) records `compensateActionOf(node)` against the
  still-at-node token. Net effect on the reverse walk: **unchanged shape** — one record per node
  visit in `RootCompensations`, replayed LIFO, no new engine logic. But a UserTask that merely
  parks (no completion action driven) produces **no** compensation record to reverse.
- **Build guard `ErrCompensateActionWithoutForwardAction`** (`definition/model/validate.go:150,472`)
  rejects `Build()` for any UserTask/ReceiveTask carrying a compensate action but no completion
  action. So any UserTask/ReceiveTask used to demonstrate/test reversal must have **both** a
  completion action and a compensate action. ServiceTasks are exempt.

## Decision

Add `ProcessDriver.ReverseInstance` with functional options. **Reverse never terminates** — it
undoes work but keeps the instance alive and running. Termination remains `CancelInstance`'s job.

```go
func (d *ProcessDriver) ReverseInstance(ctx context.Context, def *model.ProcessDefinition,
	instanceID string, opts ...ReverseOption) (engine.InstanceState, error)

type ReverseOption // opaque; constructed by the two functions below
func WithFullReverse() ReverseOption             // compensate ALL, reset vars to start, resume at start
func WithTargetNode(nodeID string) ReverseOption // compensate back to nodeID (exclusive), resume at nodeID
```

**Confirmed decisions:**
- **API shape: functional options** (variadic `opts ...ReverseOption`). No existing facade uses
  functional options — all four siblings are positional — so ReverseInstance *introduces* the
  pattern to the facade layer (it does not mirror one). `processdriver_cancel.go` is the structural
  mirror for the thin facade→`ApplyTrigger` body only.
- **Default (no option) = full reverse.** `ReverseInstance(ctx, def, id)` ⇒ `WithFullReverse()`.
- **Mutual exclusion:** `WithFullReverse()` + `WithTargetNode(...)` together ⇒ error, returned
  before any state change.
- **Terminal instance ⇒ clean error.** Reversing an already-terminal instance (Completed /
  Terminated / Failed) returns a `workflow-runtime:`-prefixed error ("cannot reverse a terminal
  instance"). (Deliberate deviation from the prior spec's "match CancelInstance" — reversing a
  terminal instance is a caller mistake and should fail loudly. Confirm CancelInstance's actual
  terminal behavior during implementation and deviate deliberately, documenting why.)

### Semantics

| Call | Compensates | Variables | Resumes at | Status |
|---|---|---|---|---|
| `ReverseInstance(id)` (default) | all, LIFO | **reset to start vars** | start node | Running |
| `ReverseInstance(id, WithFullReverse())` | all, LIFO | reset to start vars | start node | Running |
| `ReverseInstance(id, WithTargetNode("X"))` | back to X (exclusive), LIFO | keep current | node X (most-recent visit) | Running |
| `WithFullReverse()` **and** `WithTargetNode(...)` | — | — | — | **error** (mutually exclusive) |

- The reversed instance keeps its **same instance id** (it is rewound, not recreated).
- **Cycles:** `WithTargetNode("X")` targets X's most-recent visit (existing engine behavior);
  `WithFullReverse()` unwinds everything back to start regardless of how many loop iterations ran.

### The operation trio (clean, non-overlapping)

| Operation | Compensates? | End state |
|---|---|---|
| `CancelInstance` | yes | Terminated |
| `ReverseInstance` + `WithFullReverse()` | yes (all) | Running — fresh at start |
| `ReverseInstance` + `WithTargetNode(X)` | yes (back to X) | Running — at X |

## Engine enhancements required

`WithTargetNode` uses the **existing** partial-rollback path — no engine change. `WithFullReverse`
needs new engine behavior, because today full compensation terminates:

1. **Snapshot start variables.** Add `InstanceState.StartVariables map[string]any` (`engine/state.go`,
   beside `Variables`), captured once in `handleStartInstance` (`engine/step_triggers.go:15-37`, right
   after `mergeVars(s, t.Vars)`), as an **immutable deep-enough copy** (`copyVars`) of the variables
   the instance began with. Needed so full reverse can restore a fresh slate. It participates in
   serialization (persisted with the instance state) so a reverse survives durable reload.

2. **Compensate-all-then-resume-at-start mode.** Extend `CompensateRequested` with a resume-at-start
   form: add `ResumeNode string` + `ResetVars bool` fields. Keep `NewCompensateRequested(at, toNode)`
   for back-compat; add a constructor variant `NewReverseToStart(at, startNode)` (name TBD in plan)
   that sets `ResumeNode=startNode, ResetVars=true, ToNode=""`. The **full-compensation finish
   branch** in `stepCompensationFinish` (`engine/step_compensation.go:338-377`) honors it: when the
   trigger requested resume-at-start, instead of terminating it places a token at `ResumeNode`, sets
   `StatusRunning`, and (if `ResetVars`) resets `Variables = copyVars(StartVariables)`, then drives
   forward — reusing the existing `ResumeNode`/`placeTokenInScope` mechanism (thread the request's
   resume intent onto the `compensationCursor` the same way the throw-walk does). **The cancel/error
   terminate path (`ToNode==""`, no resume request) is left byte-for-byte unchanged** — the new
   behavior is gated entirely on the new fields being set.

3. **Start-node discovery.** The facade resolves the definition's single start event via
   `def.StartNodes()` (`definition/model/definition.go:108`). Zero or multiple start events → a clear
   `workflow-runtime:` error (single-start is the engine-enforced common case — `handleStartInstance`
   already errors on `len(starts) != 1`).

## Components / files

- `runtime/processdriver_reverse.go` — `ReverseInstance`, `ReverseOption`, `WithFullReverse`,
  `WithTargetNode`; option validation (mutual-exclusion, default = full), terminal-instance guard,
  start-node resolution. Thin facade → `ApplyTrigger`, structurally like `processdriver_cancel.go`.
- `engine/state.go` — `StartVariables` field (+ serialization) + capture in `handleStartInstance`.
- `engine/trigger.go` — `CompensateRequested` new fields (`ResumeNode`, `ResetVars`) + constructor
  variant (keep `NewCompensateRequested(at, toNode)` for back-compat).
- `engine/step_compensation.go` — resume-at-start finish branch + var reset (gated on the new fields).
- `examples/scenarios/reverse_rollback/main.go` — a **realistic UserTask reject/re-escalate approval
  loop** (each UserTask has BOTH a completion action and a compensate action, per the Item-4 guard),
  then `ReverseInstance` demonstrated both ways (`WithTargetNode` and `WithFullReverse`). This also
  exercises the Item-4 completion-action compensation path end-to-end.

## Error handling

- Unknown `WithTargetNode` node (not in compensation records) → the engine's existing
  `"compensation target node %q not found in scope records"` error, surfaced through the facade.
- Both options / no start event / multiple start events / terminal instance → `workflow-runtime:`-
  prefixed errors, returned before any state change.

## Testing (TDD)

- **Engine:** `StartVariables` captured on start (+ survives round-trip serialization);
  full-reverse finish resumes at start with reset vars and `StatusRunning` (not Terminated);
  partial-reverse unchanged; cancel/error terminate path regression-unchanged (byte-for-byte).
- **Cycle regression (the confirmed gap):** loop a compensable node 3×, then reverse — assert 3
  compensations fire newest-first (LIFO), for both `WithFullReverse` and `WithTargetNode`. Use
  ServiceTasks for this test (cleanest — no completion-action requirement).
- **Item-4 interaction test:** a UserTask with BOTH a completion action and a compensate action is
  reversible — driving its completion action to completion creates a compensation record that the
  reverse walk replays. (Proves compensation flows through the completion-action round-trip.)
- **Runtime facade:** option validation (default = full, mutual exclusion), start-node resolution
  errors, terminal-instance error, and end-to-end reverse (full → running-at-start with reset vars;
  target → running-at-X, current vars kept).
- **Example** runs (`go run`) and demonstrates the reject/re-escalate loop reversal both ways.

## Non-goals

- No change to `CancelInstance` (stays compensate + terminate).
- No "reverse to an arbitrary non-compensable node" — `WithTargetNode` targets a completed
  compensable-activity node (a compensation record); full reverse targets the start node.
- No new instance id on reverse (same instance rewound).

## ADR

ADR-0109 records the facade + the compensate-all-then-resume-at-start engine enhancement, the
functional-options facade decision (and that it introduces the pattern to the facade layer), the
default-full + terminal-error decisions, and the Item-4 compensation-path interaction.
