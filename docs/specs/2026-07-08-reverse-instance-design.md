# `ProcessDriver.ReverseInstance` — reverse/rollback facade

Date: 2026-07-08
Status: Approved (design) — pending implementation plan
Scope: new public facade + a targeted engine enhancement (compensate-all-then-resume-at-start)

## Context

Rolling back a running instance today requires the consumer to hand-build an engine-internal
trigger: `driver.ApplyTrigger(ctx, def, id, engine.NewCompensateRequested(clk.Now(), toNode))`
(see `examples/scenarios/compensation_saga`). That leaks `engine.NewCompensateRequested` into
consumer code, exposes the raw `toNode string` (with a magic `""`), and offers no ergonomic,
safe surface — unlike the sibling facades `DeliverMessage`, `BroadcastSignal`, `CancelInstance`,
`ResolveIncident`.

Compensation model facts (confirmed in `engine/step_compensation.go`, `engine/state.go`):

- Compensation records are **appended per execution** (`recordCompensation`, `state.go:896` — never
  keyed by node). A node visited N times in a loop yields N records; a reverse walk replays all N
  **LIFO (newest visit first)**. So cyclic processes (reject / re-escalate loops) reverse correctly
  today — there is just no test or example proving it.
- `CompensateRequested{ToNode string}`: `ToNode=="X"` compensates records completed *after* X
  (exclusive, reverse order), drops a token at X, resumes `StatusRunning`. `ToNode==""` compensates
  everything and **terminates** (`StatusTerminated`). This terminate path is shared with the
  cancel/error flow.
- `CancelInstance` already compensates **and** terminates (`TestCancelHandler_WithCompensation`).

## Decision

Add `ProcessDriver.ReverseInstance` with functional options. **Reverse never terminates** — it
undoes work but keeps the instance alive and running. Termination remains `CancelInstance`'s job.

```go
func (d *ProcessDriver) ReverseInstance(ctx context.Context, def *model.ProcessDefinition,
	instanceID string, opts ...ReverseOption) (engine.InstanceState, error)

type ReverseOption // opaque; constructed by the two functions below
func WithFullReverse() ReverseOption            // compensate ALL, reset vars to start, resume at start
func WithTargetNode(nodeID string) ReverseOption // compensate back to nodeID (exclusive), resume at nodeID
```

### Semantics

| Call | Compensates | Variables | Resumes at | Status |
|---|---|---|---|---|
| `ReverseInstance(id)` (no option ⇒ default) | all, LIFO | **reset to start vars** | start node | Running |
| `ReverseInstance(id, WithFullReverse())` | all, LIFO | reset to start vars | start node | Running |
| `ReverseInstance(id, WithTargetNode("X"))` | back to X (exclusive), LIFO | keep current | node X (most-recent visit) | Running |
| `WithFullReverse()` **and** `WithTargetNode(...)` together | — | — | — | **error** (mutually exclusive) |

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

1. **Snapshot start variables.** Add `InstanceState.StartVariables map[string]any`, captured once
   when the instance is created (on the `StartInstance` trigger), as an immutable copy of the
   variables the instance began with. Needed so full reverse can restore a fresh slate.

2. **Compensate-all-then-resume-at-start mode.** Extend `CompensateRequested` so a full compensation
   can, on finish, place a token at the process's start node and resume (`StatusRunning`) instead of
   terminating — resetting `Variables` to `StartVariables`. Reuse the existing `ResumeNode` machinery
   (ADR-0039 throw-walk already resumes after a compensation finish). Concretely: add a
   `ResumeAtStart bool` (or `ResumeNode string` + `ResetVars bool`) to `CompensateRequested`;
   the compensation-finish branch honors it. **The cancel/error terminate path
   (`ToNode==""`, no resume) is left byte-for-byte unchanged.**

3. **Start-node discovery.** The facade resolves the definition's single start event and passes it
   as the resume target. A definition with zero or multiple start events → a clear
   `workflow-runtime:` error (single-start is the common, supported case).

## Components / files

- `runtime/processdriver_reverse.go` — `ReverseInstance`, `ReverseOption`, `WithFullReverse`,
  `WithTargetNode`; option validation (mutual-exclusion, default = full). Mirrors
  `processdriver_cancel.go`.
- `engine/state.go` — `StartVariables` field + capture on start.
- `engine/trigger.go` — `CompensateRequested` new field(s) + constructor variant (keep
  `NewCompensateRequested(at, toNode)` for back-compat; add the resume-at-start form).
- `engine/step_compensation.go` — resume-at-start finish branch + var reset.
- `examples/scenarios/reverse_rollback/main.go` — reject/re-escalate loop, then `ReverseInstance`
  (both `WithTargetNode` and `WithFullReverse`).

## Error handling

- Unknown `WithTargetNode` node (not in compensation records) → the engine's existing
  `"compensation target node %q not found in scope records"` error, surfaced through the facade.
- Both options / no start event / multiple start events → `workflow-runtime:`-prefixed errors,
  returned before any state change.
- Reverse of an already-terminal instance → clean error or no-op (match `CancelInstance`'s behavior
  for terminal instances).

## Testing (TDD)

- **Engine:** `StartVariables` captured on start; full-reverse finish resumes at start with reset
  vars and `StatusRunning` (not Terminated); partial-reverse unchanged; cancel/error terminate path
  regression-unchanged.
- **Cycle regression (the confirmed gap):** loop a compensable node 3×, then reverse — assert 3
  compensations fire newest-first (LIFO), for both `WithFullReverse` and `WithTargetNode`.
- **Runtime facade:** option validation (default = full, mutual exclusion), start-node resolution
  errors, and end-to-end reverse (full → running-at-start with reset vars; target → running-at-X).
- **Example** runs and demonstrates the reject/re-escalate loop reversal.

## Non-goals

- No change to `CancelInstance` (stays compensate + terminate).
- No "reverse to an arbitrary non-compensable node" — `WithTargetNode` targets a completed
  compensable-activity node (a compensation record); full reverse targets the start node.
- No new instance id on reverse (same instance rewound).

## ADR

ADR-0109 records the facade + the compensate-all-then-resume-at-start engine enhancement.

## Parallelism note

This feature is independent of the separately-tracked **external-input validation** feature
(start / human-task-completion / message-delivery validation residing in the definition). They
touch different code and can proceed as parallel spec → plan → implementation cycles.
