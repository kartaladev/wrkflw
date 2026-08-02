# 163. Cancelling a token cancels its open task and its incidents

- Status: Accepted
- Date: 2026-08-02

> Second ADR of the scope-lifecycle-correctness delivery (2a), alongside
> [ADR-0162](0162-scope-teardown-cascades-to-descendants.md) and
> ADR-0164 (terminal transitions) was split out into **delivery 2b** on the
> round-2 audit's recommendation and is parked on `parked/terminal-transitions`
> with its own plan, `docs/plans/2026-08-02-terminal-transitions.md`.
> Design: `docs/specs/2026-08-02-scope-lifecycle-correctness.md`.

## Context

`cancelTokenWaits` (`engine/step_cancel.go:12-30`) is the engine's single
per-token teardown. Its doc comment says it "cancels every wait attached to tok".
It cancels deadline and reminder timers, the token-keyed in-wait reminder,
boundary arms on the token's node, and — for an event-based-gateway token — its
armed events, then consumes the token.

It does not touch `s.Tasks` or `s.Incidents`.

`humantask.Cancelled` is written in exactly three places in the engine:
`cancelOpenTasks` (`engine/state.go:301`, the terminal sweeps), the deadline
breach (`engine/step_timers.go:89`), and ADR-0161's stale-command filter
(`engine/step_stale_commands.go:165`), which only reaches tasks whose
`AwaitHuman` was minted **in the same step** that dropped it. None of the three
is `cancelTokenWaits`, and no test asserts task state on any boundary path.

`cancelTokenWaits` has four call sites, and the most reachable one is not a scope
teardown at all:

| call site | path |
|---|---|
| `engine/step_boundaries.go:146` | an interrupting boundary consumes its host |
| `engine/step_compensation.go:226` | `beginCompensation` cancels in-flight tokens |
| `engine/step_errors.go:389` | error-boundary enclosing-scope teardown |
| `engine/step_eventsubprocess.go:201` | interrupting event-sub-process teardown |

Reproduced against `main` @ `17e148b`, over the existing
`interruptingMessageBoundaryDef()` fixture (`engine/step_boundaries_test.go:49-67`)
— `Start → UserTask("work") → End` with an interrupting message boundary — firing
the boundary in a **later step** than the one that minted the task:

```
after step 1: task state=unclaimed open=true
after step 2: tokens=1 tasks=1
  task id=i1-h1 state=unclaimed open=true
  cmd engine.InvokeAction
UpdateTask commands emitted by step 2: 0
```

The host token is consumed, a token is placed on the boundary's outgoing flow,
and **zero `UpdateTask` commands are emitted**. The task stays `unclaimed` and
open in the consumer's `TaskStore`, still served by `ClaimableBy` and
`AssignedTo`, on a **still-running** instance, with no token left that could
complete it. "A human task with an escalation boundary" is the flagship topology
in this project's own architecture notes, which makes this the most reachable
defect in the delivery.

The error-boundary path produces the same outcome one step further out: a
sub-process containing `fork ⇒ {review: UserTask, work: ServiceTask}` with an
error boundary leaves `task … state=unclaimed open=true` on a **completed**
instance. Collapsing that topology into a single step yields `Cancelled` instead,
because ADR-0161's filter catches it — so the outcome today depends on **step
granularity**, which is not a property any definition author can see or control.
ADR-0161 recorded that asymmetry in its Consequences precisely so it would not be
read as intentional.

Incidents are the same shape one field over. `Incident.TokenID`
(`engine/state.go:122`) names the token that failed, and nothing removes an
incident when that token is cancelled. `handleResolveIncident`
(`engine/step_triggers.go:919-954`) already tolerates a missing token — it clears
the record and returns without re-invoking — so a dangling incident is a
projection defect rather than a crash: it stays visible on a terminated or
completed instance, with nothing left to resolve.

Separately, `cancelOpenTasks` emits a **shallow copy** into the command stream:

```go
// engine/state.go:297-306
if s.Tasks[i].IsOpen() {
    s.Tasks[i].State = humantask.Cancelled
    cmds = append(cmds, UpdateTask{Task: s.Tasks[i]})   // shares Claim/Completion/Vars/actors
}
```

The emitted value shares the `Claim` and `Completion` pointees, the `Vars` map
and the candidate/eligibility slices with the record committed as instance state.
This is latent only because both in-repo stores copy on ingest
(`humantask/memory.go:35`; the SQL store marshals to JSON) — but `TaskStore` is
public API, and a consumer store that retains the value verbatim would share
mutable actor state across that boundary.

`cancelOpenTasks` is not alone. The design audit found **five further emitters**
that dereference a pointer straight into `s.Tasks`:

| site | path |
|---|---|
| `engine/step_timers.go:90` | deadline breach — one of the three places the engine writes `humantask.Cancelled` |
| `engine/step_triggers.go:379` | task claimed |
| `engine/step_triggers.go:411` | task candidates resolved (`handleHumanCandidatesResolved`) |
| `engine/step_triggers.go:428` | task reassigned |
| `engine/step_triggers.go:628` | task completed |

Every one is `UpdateTask{Task: *task}` where `task` is a `*humantask.HumanTask`
into the live slice. ADR-0161's stale-command filter already took the `Clone()`
fix at `engine/step_stale_commands.go:166` after `/code-review` flagged it, so
the repository currently holds one correct emitter and six aliasing ones.

## Decision

Treat an open human task and an open incident as **waits attached to the token**,
and retire them where every other wait is retired.

```go
func cancelTokenWaits(s *InstanceState, tok *Token, at time.Time, closeKind CloseKind) []Command {
    // …existing timer / in-wait reminder / boundary-arm / event-gateway sweep…

    // AwaitCommand is the taskID for a UserTask (engine/step_nodes.go:679) and a
    // command ID otherwise, where TaskByID returns nil — the same assumption
    // cancelTimersByTaskID already makes at :15, so this is a natural no-op for
    // non-task tokens.
    //
    // Clone before the record escapes: the command is handed to a
    // consumer-supplied TaskStore while the record it was built from is
    // committed as instance state.
    if task := s.TaskByID(tok.AwaitCommand); task != nil && task.IsOpen() {
        task.State = humantask.Cancelled
        cmds = append(cmds, UpdateTask{Task: task.Clone()})
    }
    s.removeIncidentsForToken(tok.ID)

    // …consume the token…
}
```

Both run **before** `consumeTokenAs`, so `tok.AwaitCommand` and `tok.ID` are read
while the token is still coherent.

One new state helper:

```go
// removeIncidentsForToken drops every incident raised against tokenID,
// order-preserving over the remaining records for deterministic output.
func (s *InstanceState) removeIncidentsForToken(tokenID string)
```

And **every** `UpdateTask` emitter clones. `cancelOpenTasks`
(`engine/state.go:302`) emits `task.Clone()` instead of the shallow copy — the
same fix ADR-0161's filter took after `/code-review` flagged it there
(`engine/step_stale_commands.go:157-166`) — and so do the five sites tabulated
above (`engine/step_timers.go:90`, `engine/step_triggers.go:379,411,428,628`).
`HumanTask.Clone` stays the single deep-copy definition for a task.

Sweeping all six rather than only the one this ADR would otherwise touch is what
makes the Consequence below checkable instead of aspirational: after the change,
`grep -c 'UpdateTask{Task: \*' engine/*.go` returns **0**, and a seventh emitter
added later fails that grep rather than silently re-introducing the aliasing.

Placement inside `cancelTokenWaits` rather than at the call sites is deliberate.
An explicit per-site helper would need four call sites to be correct today and a
fifth to be remembered later — the same structural argument that motivates
`cancelScopeSubtree` in ADR-0162. Placing it in the shared sweep makes a new
teardown correct by construction.

The change is self-deduplicating: `cancelOpenTasks` only touches `IsOpen()`
records, so a caller that cancels tokens and then sweeps terminally — for example
`handleCancelRequested`'s compensation branch — emits nothing twice.

## Consequences

**Positive.**

- An interrupted human task is closed. The `TaskStore` projection no longer
  advertises inbox entries that nothing can complete, on running or completed
  instances alike.
- The outcome no longer depends on step granularity. The single-step and
  multi-step versions of the same topology now agree, and ADR-0161's recorded
  asymmetry is resolved rather than documented.
- **Incidents do not outlive the token they describe — on every path that
  cancels a token.** The scope is exactly the four `cancelTokenWaits` call sites
  tabulated above. It is deliberately *not* "on every path", and the audit was
  right to attack the unqualified wording: four terminal transitions end an
  instance **without** going through `cancelTokenWaits` — two drop every token
  wholesale, two leave the tokens in place — so an incident can still outlive
  its token when an instance is force-terminated or fails outright.
  Closing that is `endInstance`'s job in
  [ADR-0164](0164-terminal-transitions-are-one-path.md) (delivery 2b, audit
  finding C1), which clears `s.Incidents` at the single terminal site. Until 2b
  lands, this ADR claims the narrower thing that is true.
- No `UpdateTask` hands a consumer store a value aliasing committed engine
  state, on any path — and this is a **checked** claim, not an asserted one:
  `grep -c 'UpdateTask{Task: \*' engine/*.go` → `0`.
- One edit fixes four call sites; a fifth inherits it.

**Negative / accepted costs.**

- **The widest behavioural change in the delivery.** Every interrupt path now
  emits `UpdateTask` where it previously emitted none, so existing
  command-sequence assertions move. Each moved expectation must be inspected
  individually — a mechanical re-baseline would defeat the purpose of the tests
  that catch this class of defect.
- More commands per interrupt step, bounded by the number of open tasks on the
  cancelled tokens (at most one per token, since `AwaitCommand` names one task).
- `removeIncidentsForToken` is `O(len(s.Incidents))` per cancelled token. Open
  incidents are operator-visible and expected to be few; this is not a hot path.
- Cancelling a token now performs a `HumanTask.Clone` per open task, an
  allocation on a path that previously made none. It is paid only when a task is
  actually cancelled, and correctness across the public `TaskStore` boundary is
  worth strictly more than the allocation. The five swept emitters add the same
  per-command clone on the claim / release / reassign / complete / deadline-breach
  paths — one allocation per task transition, on paths that already write state.
- **ADR-0088's documented command ordering no longer describes the
  cancel-with-compensation path.** That ADR records the order as
  `[def.CancelActions…, per-node CancelActions…, task cancels…, compensation
  walk…]`, and `handleCancelRequested` implements it by prepending an explicit
  `taskCancelCmds := s.cancelOpenTasks()` at `engine/step_triggers.go:211`.
  After this change `beginCompensation` has already cancelled each token's task
  inside its own `preCmds` (`engine/step_compensation.go:226`), so that later
  sweep finds nothing open and returns `nil`: the same commands are emitted,
  from one call site earlier in the stream. The prepend becomes **dead code and
  is deleted**, naming this ADR — a defensive sweep that can only ever return
  `nil` is worse than no sweep, because it reads as a live guarantee. The
  *immediate-termination* branch's `cancelOpenTasks()` at
  `engine/step_triggers.go:231` is **not** dead and stays: that path never calls
  `beginCompensation`, so nothing has cancelled those tokens' tasks.
  ⚠ The deletion is only sound if `beginCompensation` cancels *every* token —
  it does (it snapshots all of `s.Tokens` at `engine/step_compensation.go:218-227`).
  The implementation step verifies this empirically rather than by reading:
  delete, run the package, and if any expectation moves, the call was not dead
  and the deletion is reverted with the counter-example recorded.
