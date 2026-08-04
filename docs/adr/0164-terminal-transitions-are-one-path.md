# 164. Terminal transitions are one path, and a terminal instance is never resumed

- Status: Accepted
- Date: 2026-08-02

> Third ADR of the scope-lifecycle-correctness delivery, alongside
> [ADR-0162](0162-scope-teardown-cascades-to-descendants.md) and
> [ADR-0163](0163-cancelling-a-token-cancels-its-task.md).
> Design: `docs/specs/2026-08-02-scope-lifecycle-correctness.md`.

## Context

### Eight terminal transitions, eight different sweeps

Each site that ends an instance re-implements the same idea, and they agree on
neither the work nor the order:

Sites are named by **symbol**, not line number: this bundle was audited against
`main` @ `17e148b`, delivery 2a then rewrote four of these files, and the
premise sweep (`docs/plans/2026-08-04-delivery-2b-premise-sweep.md`) found every
line citation shifted. Line numbers below are informative, as of `main` @
`85fbb38`; the symbol is authoritative.

| site (symbol) | file | status | `EndedAt` | clears cursor | `cancelOpenTasks` | `cancelAllScheduledWork` | drops tokens |
|---|---|---|---|---|---|---|---|
| `exitRootScope` completion block | `step_nodes.go:216-221` | Completed | ✓ | ✗ | ✗ | ✗ | — (empty already) |
| `exitRootEventSubprocessScope` completion block | `step_nodes.go:326-331` | Completed | ✓ | ✗ | ✗ | ✗ | — (empty already) |
| `exitNestedEventSubprocessScope` completion block | `step_nodes.go:407-412` | Completed | ✓ | ✗ | ✗ | ✗ | — (empty already) |
| `forceTerminate` | `step_nodes.go:523-550` | Terminated/Completed | ✓ | ✗ | ✓ | ✓ | **✓ `s.Tokens = nil`** |
| `handleUnhandledError` immediate branch | `step_errors.go:245-255` | Failed | ✓ | ✗ | ✓ | ✓ | ✗ (tokens stay) |
| `handleCancelRequested` immediate branch | `step_triggers.go:225-243` | Terminated | ✓ | ✗ | ✓ | ✓ | **✓ `s.Tokens = nil`** |
| `handleSubInstanceFailed` tail | `step_triggers.go:847-855` | Failed | ✓ | ✗ | ✓ | ✓ | ✗ (tokens stay) |
| `applyTerminate` | `step_compensation.go:766-787` | plan's | ✓ | (upstream, in `stepCompensationFinish`) | ✓ | ✓ | ✗ (tokens stay) |

⚠ The "drops tokens" column is **exactly two rows**, not four. `grep 's.Tokens =
nil' engine/*.go` returns `step_nodes.go:531` and `step_triggers.go:233` and
nothing else. Shipped ADR-0163 states it correctly — *"four terminal transitions
end an instance without going through `cancelTokenWaits` — two drop every token
wholesale, two leave the tokens in place"* — and this column is what makes the
incident decision below implementable rather than cosmetic.

⚠ The last row's cursor cell says **upstream** deliberately (round-2 audit
finding C3). `applyTerminate` contains no cursor assignment of its own; the only
clear on that path is `s.Compensating = compensationCursor{}` in
`stepCompensationFinish` (`engine/step_compensation.go:552-567`), one function
earlier. This row is what makes "route all eight through `endInstance`"
harmless, so the distinction is load-bearing: `endInstance`'s clear is
**redundant** here, not conflicting — it re-zeroes a cursor an upstream caller
already zeroed. Read as "`applyTerminate` clears it", the row would argue for
splitting `endInstance` into clearing and non-clearing variants, which is
exactly the complication this ADR exists to remove.

The three completion sites sweep neither tasks nor scheduled work. That is how a
non-interrupting root event-sub-process arm survives into a terminal snapshot —
an outcome the comment at `engine/step_eventsubprocess.go:221-228` argues is
harmless because "the runtime refuses to hold correlation waiters for terminal
instances, and this fire path is status-guarded". `handleSubInstanceFailed`
emits `FailInstance` **before** the task cancels; every other site emits it
after.

⚠ **That survival is a deliberate ADR-0124 consequence, not an oversight**, and
this ADR narrows it. ADR-0124 ("Repeatable non-interrupting boundary events and
event sub-processes") reasoned that a lingering arm in a terminal snapshot is
harmless, and `cancelAllScheduledWork`'s own godoc encodes it as a prohibition:

```go
// engine/state_arms.go:171-176
// It is the sweep for ABNORMAL termination: cancel, unhandled error, child
// failure, force-terminate, and the compensation-walk terminate. Normal
// completion does NOT run it — exitRootScope ends the instance without a sweep,
// and a repeatable non-interrupting root event-sub arm is deliberately allowed
// to survive into a terminal snapshot (ADR-0124). Do not wire this into the
// normal-completion path.
```

Routing the three completion sites through `endInstance` **is** wiring it into
the normal-completion path. That is intended — see Decision 1 — but it means the
godoc above and the `step_eventsubprocess.go` comment become false the moment
this ships, so both are updated as part of this change rather than left to
contradict the code. ADR-0124's decision (arms are repeatable, not one-shot) is
untouched; only its *"and it may survive into a terminal snapshot"* corollary is
withdrawn, because this ADR removes the condition that made it observable.

### No terminal transition retires an orphaned incident

Delivery 2a (ADR-0163) established that **incidents do not outlive the token
they describe**, and implemented it per-token: `cancelTokenWaits` calls
`removeIncidentsForToken(tok.ID)` (`engine/state.go:297`). But two terminal
sites drop every token *without* going through `cancelTokenWaits` —
`forceTerminate` and `handleCancelRequested`'s immediate branch, per the "drops
tokens" column above — so an incident raised against a dropped token survives
into the terminal snapshot describing a token that no longer exists.

Shipped, pushed code already forward-references **this ADR** as the fix:

- `engine/step_cancel.go:53-55` — *"Closing that is `endInstance`'s job in
  ADR-0164 (delivery 2b, audit finding C1), which clears `s.Incidents` at the
  single terminal site."*
- `docs/adr/0163-cancelling-a-token-cancels-its-task.md:188-191` — the same
  claim, in a shipped ADR.

Until this revision, ADR-0164 did not mention incidents anywhere, so those two
citations pointed at a decision that did not exist (premise sweep finding F-1).
Decision 3 below records it.

### No terminal transition clears the compensation cursor

`startCompensationWalk` (`engine/step_nodes.go:1027-1039`) commits a token to a
walk, stamping `cursor.ResumeNode` at `:1031` and `cursor.ActiveCmdID` at `:1034`
into `s.Compensating`. Nothing clears it on the way out — not `forceTerminate`,
`handleUnhandledError`, `handleSubInstanceFailed`, nor `handleCancelRequested`'s
immediate tail. This contradicts the invariant `beginCompensation`'s own comment
asserts (`engine/step_compensation.go:301`: "s.Compensating is the zero
cursor here"), and ADR-0161's `liveAwaiters` comment already names it as "a
separate defect, queued in `docs/plans/HANDOVER.md`"
(`engine/step_stale_commands.go:40-44`), where ADR-0161's terminal exclusion
exists as defence against it.

A step can therefore commit with `Status == StatusTerminated` **and** a live
cursor carrying a stale `ResumeNode`. A later plain `CompensateRequested` passes
the terminal guard, `beginCompensation` inherits the stale cursor at `:306`, and
`applyFinish` sets `Status = StatusRunning`, clears `EndedAt` and places a token
at the stale node. Repro: a fork whose first branch reaches a `CompensateThrow`
and whose second reaches an `End(WithForceTermination)`, then deliver
`CompensateRequested`.

### A partial rollback resurrects a terminal instance, with no stale cursor at all

The terminal guard is scoped strictly to reverse intent:

```go
// engine/step_compensation.go:130-133
if t.ReverseNode != "" && s.Status.IsTerminal() {
    return StepResult{}, fmt.Errorf("workflow-engine: cannot reverse a terminal instance (status %v)", s.Status)
}
s.Status = StatusCompensating
```

A **partial rollback** — `NewCompensateRequested(at, toNode)` with a non-empty
`toNode`, a public constructor at `engine/trigger.go:352` — has `ReverseNode == ""`
and slips past. `stepCompensationFinish` then takes the `walkPartial` branch:
`s.Status = StatusRunning` (`:719`), `s.EndedAt = nil` (`:724`), a token placed at
the target (`:733`).

Reproduced against `main` @ `17e148b`. Definition
`start → svc(charge/refund) → after(ship/unship) → End(WithForceTermination("abort", OutcomeAbort))`;
drive both actions to completion so the token reaches the force-termination end
with both records intact, then deliver `NewCompensateRequested(at, "svc")`:

```
after force-terminate: status=terminated terminal=true endedAt=2026-08-02T10:00:02Z tokens=0 records=2
partial rollback on terminal instance: err=<nil>
  RESULT status=compensating terminal=false endedAt=... tokens=0
    cmd engine.InvokeAction
```

The terminated instance is `compensating` with a live compensation action in
flight; completing it resumes execution at `svc` with `EndedAt` cleared.

**This is a distinct vector from the stale cursor.** The topology contains no
`CompensateThrow`, so the cursor was the zero value — nothing stale was
inherited. Clearing `s.Compensating` does not close it. `forceTerminate` is the
right setup precisely because it deliberately does not run compensation
(`engine/step_nodes.go:521-522`), leaving the records intact; a probe built on
`NewCancelRequested` does **not** reproduce, because the cancel path's own walk
consumes the records first.

The comment at `engine/step_compensation.go:120-129` records that a plain
full rollback against an already-terminal instance is **deliberate** — internal
cancel and error paths re-deliver `CompensateRequested` that way, and
compensating a completed instance whose records are still present is a
legitimate admin action. So the rule being violated is not "a terminal instance
is immutable".

### The same hole is open in the documented `ReverseInstance` API

Found during this bundle's design audit, and materially stronger than the
raw-struct footgun above.

`ProcessDriver.ReverseInstance(…, WithTargetNode(n))` routes through
`engine.NewReverseToNode` (`runtime/processdriver_reverse.go:104`), and that
constructor sets `ToNode` and `RestoreTargetVars` while leaving `ReverseNode`
**empty**:

```go
// engine/trigger.go:373-374
func NewReverseToNode(at time.Time, toNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ToNode: toNode, RestoreTargetVars: true}
}
```

So `t.ReverseNode != "" && s.Status.IsTerminal()` never fires for a targeted
reverse. The facade's own pre-check does reject a terminal instance
(`runtime/processdriver_reverse.go:99-101`) — but that is a `Load`ed snapshot,
and the engine guard exists precisely to cover the window between it and `Step`.

**This makes a claim in shipped ADR-0109 untrue.** ADR-0109:232-237 states the
terminal-instance guard "closes the TOCTOU race in point 4 above: the facade's
terminal check runs against a `Load`ed snapshot, and an instance could complete
between that `Load` and the engine's `Step` — without this guard, such a race
would silently resurrect a terminal instance into `StatusRunning`." For
`WithTargetNode` that window is **unguarded**, so the facade's pre-check is the
only line of defence, which is exactly the arrangement ADR-0109 says it is not.

That the narrowness is an oversight rather than a decision is visible one guard
up: the in-flight-walk check at `engine/step_compensation.go:114-119` tests
`t.ReverseNode != "" || t.RestoreTargetVars` — both reverse shapes. Only the
terminal guard was left at one.

## Decision

**1. One terminal transition.**

```go
// endInstance performs the terminal transition: status, EndedAt, a cleared
// compensation cursor, the orphaned-incident sweep, and the projection sweeps
// every terminal path owes.
//
// The terminal command is threaded through rather than appended by the caller so
// the emitted order stays [task cancels…, terminal, scheduled-work cancels…] —
// exactly what applyTerminate, handleUnhandledError, forceTerminate and
// handleCancelRequested emit today. Pass nil where a site emits no terminal
// command of its own.
func (s *InstanceState) endInstance(status Status, at time.Time, terminal Command) []Command {
    s.Status = status
    ended := at
    s.EndedAt = &ended
    s.Compensating = compensationCursor{}
    s.removeOrphanedIncidents()
    cmds := s.cancelOpenTasks()
    if terminal != nil {
        cmds = append(cmds, terminal)
    }
    return append(cmds, s.cancelAllScheduledWork()...)
}
```

All eight sites route through it. Two normalizations are deliberate:

- The three completion sites gain the task and scheduled-work sweeps they lack.
  The "harmless" argument at `engine/step_eventsubprocess.go:221-228` becomes
  moot: the arm is retired at completion rather than surviving into the snapshot
  and relying on the runtime to ignore it. ADR-0124's corollary is withdrawn and
  both in-code statements of it are updated (see Context).
- `handleSubInstanceFailed` moves `FailInstance` into the canonical position,
  after the task cancels.

`applyTerminate` keeps its `applyPlanRecordClearing(s, plan)` between the status
assignment and the sweeps: record clearing is walk-plan-specific and does not
belong in a shared helper.

**2. A terminal instance may be compensated, never resumed.**

```go
// ToNode joins ReverseNode because stepCompensationFinish's walkPartial branch
// resumes at ToNode (:713-733), which resurrects the instance just as a full
// reverse would.
if (t.ReverseNode != "" || t.ToNode != "") && s.Status.IsTerminal() {
    return StepResult{}, fmt.Errorf("workflow-engine: cannot resume a terminal instance (status %v)", s.Status)
}
```

A plain full rollback — both fields empty — keeps working, so the deliberate
internal re-delivery path is untouched. The guard rejects **up front**, before
any compensation `InvokeAction` is emitted. Guarding instead at the resume site
inside `stepCompensationFinish` was rejected for exactly that reason: the
rollback's side effects would already have fired.

Testing `ToNode` rather than `RestoreTargetVars` is deliberate: it covers the
targeted reverse (`NewReverseToNode`, which sets both), the raw partial rollback
(`NewCompensateRequested(at, toNode)`, which sets only `ToNode`), and any
hand-built `CompensateRequested` carrying a resume target. `RestoreTargetVars`
alone would miss the raw partial rollback that was actually reproduced.

**3. Incidents are retired by token linkage, not wholesale.**

`endInstance` clears **only incidents whose token no longer exists**. It must
not do `s.Incidents = nil`.

```go
// removeOrphanedIncidents drops every incident whose TokenID names a token that
// is no longer present. It is the terminal-site counterpart of
// removeIncidentsForToken (ADR-0163): the two token-dropping terminal paths
// never route through cancelTokenWaits, so without this an incident outlives
// the token it describes. Incidents whose token survives the terminal
// transition are KEPT — see ADR-0164 for why.
func (s *InstanceState) removeOrphanedIncidents() {
    s.Incidents = slices.DeleteFunc(s.Incidents, func(inc Incident) bool {
        return s.tokenByID(inc.TokenID) == nil
    })
}
```

This is what shipped ADR-0163 actually promises — *"incidents do not outlive
**the token they describe**"* — and the wholesale clear over-delivers on that
invariant at a real cost, because `s.Incidents` is read **after** the instance
is terminal in four places outside `engine/`:

| consumer | effect of a wholesale clear |
|---|---|
| `runtime/outbox.go:71-93` `terminalEventErr` | preference order 1 (`st.Incidents[0].Error`) becomes unreachable, so `instance.failed` / `instance.terminated` payloads report `FailInstance.Err` — e.g. `"cancelled"` instead of the concrete failure |
| `runtime/processdriver_action.go:31-41` `terminalErr` | the same degradation for a child-instance failure message — and this one **crosses the instance boundary**: `terminalErr(st)` is what `runtime/processdriver.go:678` puts in `kernel.CallOutcome.Err`, which a child hands its **parent** through `SubInstanceFailed`, where it lands in the parent's own `FailInstance.Err` and its `instance.failed` payload. The concrete text is lost one level **up the call tree**, not just in one event |
| `service/instance.go:253-264` | the ProcessInstance audit view (ADR-0144–0151) renders `incidents: []` on every terminal instance; an operator loses the record of why it ended |
| `dialect.{postgres,mysql,sqlite}` `incident_count`, `runtime/kernel/memstore.go:195` | terminal instances always report `IncidentCount: 0`, so listing filters on it silently exclude them |

Consequences of the narrowing, which are **behavioural and must be tested, not
assumed**:

- The **two token-dropping sites** (`forceTerminate`,
  `handleCancelRequested`'s immediate branch) end with no tokens, so every
  incident is orphaned and every incident is cleared. At those two sites the
  narrow sweep and a wholesale clear coincide *by construction*.
- The **three sites that leave tokens in place** (`handleUnhandledError`'s
  immediate branch, `handleSubInstanceFailed`'s tail, and `applyTerminate`, whose
  `applyPlanRecordClearing` clears compensation records only) keep the incidents
  whose token survived, and `terminalEventErr` still finds the concrete error.
  Only `forceTerminate` and `handleCancelRequested`'s immediate branch nil
  `s.Tokens` — `grep 's.Tokens = nil' engine/*.go` returns exactly those two.
  **This
  is the only place the two implementations differ, so it is where the
  distinction must be pinned by test** — a wholesale `s.Incidents = nil` must
  fail that test.
- **Ordering.** The sweep reads `s.Tokens`, so it observes whatever the caller
  has already done. Each site keeps `endInstance` at its existing terminal
  position, which at the two dropping sites is *after* `s.Tokens = nil` — that
  is what produces the clear those sites are supposed to perform. Hoisting
  `endInstance` above the token drop would silently retain their incidents, and
  is the inverse of the mistake the wholesale clear makes.
- An accepted residue: an instance parked on an incident that is then cancelled
  (`CancelRequested`, no compensation records) drops its tokens, so its incident
  is cleared and `terminalEventErr` reports `"cancelled"` rather than the
  original error. That follows from token linkage being the rule; recording the
  pre-terminal error in the terminal event is a separate concern from incident
  lifetime, and is not solved by keeping a dangling incident.
- **That residue propagates across the instance boundary.** The same preference
  order lives in `terminalErr`, and `terminalErr` is not only a diagnostic: it
  feeds `kernel.CallOutcome.Err` (`runtime/processdriver.go:678`), which a child
  instance hands its parent via `SubInstanceFailed`, and which the parent then
  records as its **own** `FailInstance.Err` and publishes in its own
  `instance.failed` payload. So a **child** cancelled while parked on an
  incident reports `"cancelled"` to its parent — or, on the `forceTerminate`
  path with `OutcomeComplete`, which emits no `FailInstance` at all, the generic
  `"instance terminated"`. This is accepted for the same reason as the residue
  above, but it is a wider blast radius than a single event payload and is
  called out here so nobody rediscovers it as a defect. Pinned end-to-end by the
  `runtime/terminal_incident_events_test.go` pair, which drives the real engine
  rather than hand-building states the way `runtime/outbox_test.go` does.

**4. A resumption trigger stranded by a terminal transition is a no-op, not an error.**

`handleActionCompleted` had no status guard at all: it dispatched the
compensation cursor, then looked up `s.tokenAwaiting(t.CommandID)` and returned
`ErrTokenNotFound` when nothing matched. Both halves are wrong once the instance
is terminal.

- **Token gone.** `startCompensationWalk` commits **only its own token** to the
  walk, so a sibling branch can complete or terminate the instance while the
  walk's `InvokeAction` is still in flight. The trailing `ActionCompleted` then
  matches nothing and surfaces as an error for what is really a stale straggler.
- **Token surviving.** Three terminal paths keep `s.Tokens`
  (`handleUnhandledError`'s immediate-fail branch, `handleSubInstanceFailed`'s
  tail, `applyTerminate`), so a sibling still holds its `AwaitCommand`. `tokenAwaiting` matches on
  `AwaitCommand` **alone, with no status check**, and `drive` has no status
  guard either — so that token is merged and driven forward on a dead instance.
  It can reach an end event as the last remaining token and `exitRootScope`
  flips a **Failed** instance to **Completed**, while `terminalOutboxEvent`
  suppresses the second event (`prevStatus.IsTerminal()`) — leaving the
  persisted status silently disagreeing with the `instance.failed` event already
  published. ADR-0161's `liveAwaiters` filter does not cover this: it drops
  commands emitted in the **same** step, whereas this one was dispatched in an
  earlier step and is already in flight.

The guard therefore sits at the **top of the handler, before any lookup** —
the same position its two siblings `handleTimerFired` and
`handleHumanCandidatesResolved` already use. Position is load-bearing: a guard
inside the `tok == nil` branch closes only the first half and cannot see the
second at all.

```go
func handleActionCompleted(...) (StepResult, error) {
	if s.Status.IsTerminal() {
		// stale straggler: log at Warn (ADR-0129) and drop
		return StepResult{State: *s}, nil
	}
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
		...
```

Hoisting above the compensation-cursor dispatch is safe because
`StatusCompensating` is **not** terminal, so an in-flight compensation walk is
unaffected. With the guard hoisted, the `tok == nil` branch is unreachable for a
terminal instance and collapses back to the plain `ErrTokenNotFound` return.

**`handleActionFailed` gets the identical hoist.** It carries the same unguarded
`tokenAwaiting` lookup, and the symmetric route was **proven reachable by
execution**, not argued: against an already-`StatusFailed` instance whose
surviving sibling node carries an error boundary routed to a normal end, the
stray `ActionFailed` returned `err=nil`, `status=completed`,
`cmds=[CompleteInstance{}]` — the same **Failed → Completed** flip. With no
boundary on that node it instead emits a **duplicate `FailInstance`** and re-runs
`endInstance` on an already-terminal instance. Both are closed by one guard at
the top of the handler, with the same Warn shape (message adapted to the
trigger noun). The safety argument transfers unchanged — `StatusCompensating`
is not terminal, so the best-effort compensation advance at the head of that
handler stays reachable — and this was **verified rather than assumed**: a
positive-control mutation widening the guard to `|| s.Status == StatusCompensating`
fails `TestBestEffortCompActionFailure`, proving that branch is genuinely
traversed and genuinely pinned.

With both handlers guarded, the thesis *a terminal instance is never resumed*
holds across those two handlers, which is where the analysis stopped.

**Three more handlers carry the same defect, and are guarded here too.** The
owner-run `/code-review` found that `handleActionCompleted` and
`handleActionFailed` were not the only unguarded `tokenAwaiting` lookups. All
three additions take the identical shape — an `IsTerminal()` early return at the
top of the handler with a matching Warn — and each is pinned by a test that went
RED before its guard:

| handler | how it is reachable | damage when unguarded |
|---|---|---|
| `handleSubInstanceCompleted` | `runtime/calllink`'s `CallNotifier.DrainOnce` delivers it **asynchronously with no parent-status check**, and the call-activity token survives the three token-keeping terminal paths | merges the child's output into a dead parent and drives it to its end as the last token — **Failed → Completed**, with the second event suppressed by `terminalOutboxEvent` |
| `handleSubInstanceFailed` | same delivery path, same surviving token | with a matching error boundary, routes to recovery and drives (the same status flip); with none, re-runs its own `endInstance` tail on an already-terminal instance — **overwriting `EndedAt`** with a later timestamp and emitting a **duplicate `FailInstance`** |
| `handleResolveIncident` | **this ADR's own Decision 3 opens it**: `removeOrphanedIncidents` deliberately KEEPS an incident whose token survived, which is exactly the state (`StatusFailed` + live `TokenIncident` token + live incident) an admin can still address | deletes the incident, flips the token to `TokenActive` and re-invokes the stalled action, so a **side-effecting action really runs** against a dead instance; its `ActionCompleted` is then swallowed by the guard above, stranding the token `TokenActive` with its incident gone — neither re-raisable nor re-resolvable |

The last row is the one worth dwelling on: Decision 3's rationale enumerated only
the **read** consumers of `s.Incidents` (`terminalEventErr`, `terminalErr`, the
audit view, `incident_count`). `ResolveIncident` is a **write** consumer, and
nobody considered it. A decision that deliberately preserves state must
enumerate who can *act* on that state, not only who displays it.

**The two sub-instance guards return `nil`, not `ErrTokenNotFound`, and that
crosses a layer.** `CallNotifier.DrainOnce` keys its idempotency off the
delivery error: it retries only when the error is non-nil **and** not
`engine.ErrTokenNotFound`, and marks the link notified on success **or**
`ErrTokenNotFound` alike. A nil return therefore lands on the same branch and
still retires the link. That was verified in source **and pinned by a test**
(`TestCallNotifierRetiresLinkWhenParentIsTerminal`) asserting both failure modes
are absent: the link is marked notified (no infinite redelivery loop) and the
parent stays Failed (no resurrection). The inference alone was not accepted as
evidence.

### The class is NOT closed

Five of the fifteen triggers now carry the guard: `ActionCompleted`,
`ActionFailed`, `SubInstanceCompleted`, `SubInstanceFailed`, `ResolveIncident` —
plus `TimerFired` and `HumanCandidatesResolved`, which already had theirs. **The
remaining handlers are protected by convention only.** Nothing in the type
system, the dispatch in `Step`, or a test prevents the next handler — or the next
`tokenAwaiting` caller added to an existing one — from being resurrectable in
exactly the same way. This defect has now been found three separate times, by
three separate reviews, in three separate handlers; the pattern is the finding.

The structural fix — classifying all fifteen triggers as resumption vs
administrative vs terminal-tolerant and enforcing the guard **centrally in
`Step`'s dispatch** rather than per handler — is **deferred to its own ADR by
owner decision**, so that this delivery stays attributable. It is not done here,
and this ADR does not claim the class is closed.

A stray command against a **running** instance is still a real error. The drop
is logged at Warn, matching `handleTimerFired` and the four other silent-drop
sites ADR-0129 governs — an unlogged drop was the headline finding of ADR-0161's
gate one delivery earlier.

One consequence is accepted deliberately:

- **The tolerance is broader than the rationale.** It also covers an instance
  terminated by an admin `CancelRequested`, where the tokens were dropped
  wholesale rather than consumed by a sibling. The narrow form is **not
  implementable**: Decision 1's `endInstance` zeroes the compensation cursor,
  and `CompensationRecord` carries no command id, so nothing survives the
  terminal transition to key the check on. Neither `ActionCompleted` nor
  `ActionFailed` has an external caller — both reach the engine only from
  `runtime/processdriver_action.go` and the replay codec — so the widening
  breaks no consumer contract. Contrast `HumanCompleted`, which arrives from
  `service.CompleteTask` and rightly keeps erroring.
- **The symmetric case is closed here, not deferred.** An earlier revision of
  this ADR deferred `handleActionFailed` on the reasoning that it "errors rather
  than resurrecting". That reasoning was **wrong**, and the final review proved
  it wrong by execution: the same surviving-sibling setup drives an
  `ActionFailed` through boundary routing to a normal end and flips a Failed
  instance to Completed. It gets the same hoist in this delivery, so the two
  handlers stay symmetric rather than drifting apart between deliveries.

**5. ADR-0109 is corrected, not superseded.** Its decision stands; only its
claim about the terminal guard's coverage was overstated. A note is added to
that ADR pointing here. Because ADR-0109 is already merged and pushed it cannot
be amended — but the correction rides in *this* bundle's commit rather than a
separate `docs:` commit, because this bundle is what makes the claim true.

## Consequences

**Positive.**

- A terminated instance cannot be resurrected by any of the **seven** routes
  found so far: a stale compensation cursor (Decision 1), a resuming
  `CompensateRequested` (Decision 2), an in-flight `ActionCompleted` or
  `ActionFailed` — whether the token is gone **or still alive** — and a late
  `SubInstanceCompleted`, `SubInstanceFailed` or `ResolveIncident` (Decision 4).
  Every one was reproduced by a test that went RED before its fix, not argued
  from reading. The invariant `beginCompensation` documents becomes true by
  construction for these routes.
  ⚠ **This is a list of routes closed, not a proof that none remain.** Three of
  the seven were found only after this ADR had already claimed completeness
  twice. The guard is per-handler and enforced by nothing; see "The class is NOT
  closed" in Decision 4 and the deferred structural ADR.
- A ninth terminal transition added later inherits the cursor clear, the task
  sweep and the scheduled-work sweep, instead of being one more row that has to
  be audited.
- A completed instance no longer carries live arms, timers or open tasks.
  ADR-0161's terminal exclusion in `liveAwaiters` stays correct and is now
  defence in depth rather than the only defence.
- ADR-0163's stated invariant — *"incidents do not outlive the token they
  describe"* — becomes true on the terminal paths too, and the forward
  references to this ADR in `engine/step_cancel.go` and ADR-0163 become
  truthful. Note the promise kept is **token linkage**, not "a terminal
  instance has no incidents": a terminal instance whose tokens survive keeps
  them, deliberately.
- The emitted command order becomes uniform across every terminal path.

**Negative / accepted costs.**

- **Breaking behaviour on the resume guard.** A consumer calling
  `NewCompensateRequested(at, someNode)` against a terminal instance now gets an
  error where it previously got a silently resurrected instance. That is the
  fix, and there is no opt-out.
- **Breaking behaviour on the completion paths.** Normal completion now emits
  task-cancel and scheduled-work-cancel commands it did not emit before, so
  completion-path tests must have their expectations updated. The "harmless"
  claim about the surviving root arm becomes a claim the tests must now re-check
  rather than inherit.
- `handleSubInstanceFailed`'s command order changes. Any consumer depending on
  `FailInstance` arriving before the task cancels on that one path is affected;
  every other terminal path already emits the other order, so this makes the
  odd one out consistent.
- `endInstance` takes a `Command` interface parameter that is nil at some call
  sites. A nil-typed-interface footgun is possible if a caller passes a typed
  nil; call sites pass either a concrete command literal or an untyped `nil`,
  and the guard is `terminal != nil`.
- **ADR-0124's terminal-snapshot corollary is withdrawn**, and three in-code
  statements of it are rewritten: `cancelAllScheduledWork`'s godoc
  (`engine/state_arms.go`), which currently says *"Do not wire this into the
  normal-completion path"*, the non-interrupting comment at
  `engine/step_eventsubprocess.go`, and — outside `engine/` —
  `runtime/terminal_waiter_test.go`'s file header and inline comment, whose
  *"even when a repeatable root event-sub arm is still armed in its snapshot"*
  premise is no longer exercised. That test keeps passing either way, so the
  withdrawal is converted into a positive pin there
  (`assert.Empty(t, final.EventTriggeredSubprocesses)`) rather than left as a
  silently vacuous premise. ADR-0124's actual decision — non-interrupting arms
  are repeatable rather than one-shot — is unaffected.
- **A stranded `ActionFailed` is now a no-op too.** Decision 4 closes **both**
  halves of the stale-straggler defect. A consumer that today relies on
  `ErrTokenNotFound` from a late `ActionFailed` gets a silent (Warn-logged) drop
  instead — the same behaviour change `ActionCompleted` takes, and the same
  justification: neither trigger has an external caller. Nothing about this is
  deferred any more; the earlier revision's deferral entry is withdrawn.
- ⚠ **This ADR does NOT close the zombie-scope gap, despite ADR-0162 saying it
  would.** Shipped, pushed ADR-0162 states: *"[a force-termination end] still
  commits a terminal snapshot carrying open `Scopes`. Closing that is
  `endInstance`'s job in ADR-0164 (delivery 2b). Until 2b lands, this ADR claims
  the narrower thing that is true."* `endInstance` as delivered sweeps status,
  `EndedAt`, the compensation cursor, orphaned incidents, open tasks and
  scheduled work — it does **not** prune `s.Scopes`.

  That is a deliberate scope decision, not an oversight. Pruning scopes at a
  terminal transition is materially larger than the sweeps above: scopes carry
  compensation records, and the pruning interacts with `archiveCompensations`,
  the persisted snapshot shape and the `service/` audit view — none of which this
  ADR analysed. Adding it at the delivery gate would push an unaudited behaviour
  change through the single code path every terminal transition now takes.

  **Consequence: ADR-0162's sentence becomes inaccurate the moment this delivery
  merges**, and the zombie-scope gap stays open. It needs its own ADR. Recorded
  here so the forward reference resolves to a stated decision rather than
  dangling — which is precisely the defect the 2026-08-04 premise sweep called
  non-negotiable when it found ADR-0164 documenting no incident decision at all
  while shipped code cited it for one.
- **An instance cancelled while parked on an incident loses that incident**, and
  its `instance.terminated` event reports `"cancelled"` rather than the original
  error, because the cancel path drops its tokens. Accepted as a direct
  consequence of token linkage; see Decision 3. When that instance is a **child**,
  the degraded string is also what its parent records as its own failure, via
  `terminalErr` → `CallOutcome.Err` → `SubInstanceFailed`.
