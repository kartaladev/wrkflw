# 165. Triggers declare their own terminal policy, enforced once in dispatch

- Status: Accepted
- Date: 2026-08-05

> First of the three follow-up ADRs owed by
> [ADR-0164](0164-terminal-transitions-are-one-path.md).
> Design: `docs/specs/2026-08-05-structural-terminal-trigger-guard.md`.
> Plan: `docs/plans/2026-08-05-structural-terminal-trigger-guard.md`.

## Context

ADR-0164 established the invariant **"a terminal instance is never resumed"** and
enforced it by hand-copying a guard into individual trigger handlers:

```go
if s.Status.IsTerminal() { slog.WarnContext(...); return StepResult{State: *s}, nil }
```

That enforcement strategy failed on its own terms. **Three successive review
passes found 1 → 2 → 5 instances of the same defect, and every increase arrived
after an ADR had claimed the class was closed.** The design audits (rule #9 plus
a dedicated premise sweep) found **zero**; the whole-branch review found two; and
`/code-review` — run at the Delivery Gate, on code the ADR already described as
complete — found three more. ADR-0164 added five guards (`TimerFired` and `HumanCandidatesResolved` already
had theirs), bringing the total to **7 of 15** handlers plus a conditional eighth
in `stepCompensateRequested` — and recorded in its own **Decision** section that
the class was not closed.

The failure is structural, not a lapse in diligence. A convention that must be
re-applied by hand at fifteen sites has no mechanism to notice when it is not.
Handler #16 gets nothing by construction, and nothing in the type system, the
build, or the test suite objects.

### Five more triggers, reproduced across four cases

Designing this ADR reproduced four cases covering five currently-unguarded
triggers, by execution
against `main` @ `8832021` (fixture and captured output in §0–§1 of the spec):

- **`StartInstance`** resets `Status` to `Running` without clearing `EndedAt`,
  producing a running instance that carries an end timestamp.
- **`HumanClaimed` / `HumanReassigned`** overwrite a `Cancelled` task back to
  `Claimed` and emit `UpdateTask`, returning a dead instance's task to the
  consumer's inbox projection where it can never be completed.
- **`HumanCompleted`** completes a cancelled task, stamps a `Completion` naming an
  actor an hour after the instance died, advances the token, and appends a
  **post-mortem history visit** to the audit trail `service/` renders.
- **`SignalReceived`** merges the trigger payload into a dead instance's
  `Variables` and advances parked tokens. ⚠ **`MessageReceived` was NOT
  reproduced** — it is classified from code shape (the same `dispatchArmCascade`
  and standalone branch). The reproduced set is four cases covering **five**
  triggers; `MessageReceived` is a sixth by analogy.

Reproduction also **corrected the premise this ADR started from**. The 2b
handover records a carve-out that "`HumanCompleted` must keep erroring". It does
not error by design — it errors only when `tokenAwaiting` returns nil, i.e. only
when the token happened to be swept. `endInstance` never clears `s.Tokens`, and
ADR-0164 Decision 3 *deliberately* keeps incident-holding tokens, so the carve-out
is **incidental rather than guaranteed**. A design audit could not have found
this; only running it could.

### Why the ordering forces the issue now

Delivery 3 (ADR-0158) makes one broadcast signal fire **every** matching arm per
family, multiplying traffic through the two unguarded event-delivery handlers
above. Guarding structurally is a prerequisite for that delivery, not a cleanup
that can follow it.

### Why the obvious fix is rejected

Clearing `s.Tokens` at every terminal transition would kill the class at its
root. It is rejected: ADR-0164 Decision 3 deliberately preserves incident-holding
tokens so an admin can still act on them, and pruning interacts with
`archiveCompensations`, the persisted snapshot shape and the `service/` audit
view. This ADR makes token survival **safe** rather than removing it.

## Decision

**Every trigger declares its own terminal policy, and `Step` enforces that
declaration in exactly one place.**

### 1. The policy is a method on the sealed `Trigger` interface, with no default

`Trigger` is sealed by `isTrigger()`, and every trigger embeds `baseTrigger`
which supplies it. A policy method **defaulted on `baseTrigger`** would be
inherited silently by a new type — the same convention-only failure in a new
costume. So the method is added to the interface and deliberately left
undefaulted:

```go
type terminalPolicy int

const (
    rejectSilently   terminalPolicy = iota // log; return state unchanged
    rejectWithError                        // return ErrInstanceTerminal
    allowOnTerminal                        // handler runs; it expects a terminal instance
)
```

**A new trigger type does not compile until its author has made this decision.**
That compile error is the entire point of the ADR; every other property is
secondary.

`rejectSilently` takes `iota` so the zero value is the safe one.

### 2. Enforcement is a single check in `dispatch`

`dispatch` (`engine/step.go`) is the one choke point every trigger already
passes through. The check goes there — after `validateTriggerKey` and
`cloneState`, preserving today's precedence in which a malformed trigger
(ADR-0152) is rejected before any state-dependent check.

**All eight existing guard sites are removed** — the seven per-handler guards in
`step_triggers.go` plus the conditional resume guard in
`stepCompensateRequested`. Leaving them would
recreate the drift this ADR exists to remove: two places stating one policy,
free to disagree.

`StatusCompensating` is not terminal, so in-flight compensation walks are
untouched.

### 3. The rejection outcome is three-valued, on a principled axis

The existing split (six silent no-ops, one error) is preserved and promoted to a
deliberate rule: **does the delivering caller distinguish success from a
no-op?**

- **Async, engine-originated echoes cannot** — and must stay silent. This is
  forced by shipped code, not preference. `calllink.CallNotifier` retries any
  error that is not `ErrTokenNotFound`, so a new sentinel would make a call link
  **redeliver forever**; `signalbus.Publish` `errors.Join`s a fan-out, so one
  terminal target would fail the whole broadcast.
- **Synchronous external API calls must be told** — `service.CompleteTask`
  reporting success having done nothing is a defect, not a courtesy.

A new sentinel carries the error case, **wrapping `ErrInvalidTransition`** in the
established house form (`ErrTokenNotFound` is defined the same way):

```go
var ErrInstanceTerminal = fmt.Errorf(
    "workflow-engine: instance is terminal: %w", ErrInvalidTransition)
```

The wrapping is load-bearing, not cosmetic. `ErrInvalidTransition` is already
documented as classifying "a trigger that cannot be applied because the targeted
instance/token is not in a state that accepts it", `service/` already
re-classifies it as `ErrConflict`, and `transport/http/httpcore` already maps it
to **422**. So the newly-visible `ResolveIncident` failure reaches the admin
through machinery that exists — **this ADR needs no `service/` or `transport/`
change**, which is what keeps the bundle to one package.

### 4. The classification

`rejectWithError`: `StartInstance`, `HumanClaimed`, `HumanReassigned`,
`HumanCompleted`, `ResolveIncident`.

`rejectSilently`: `ActionCompleted`, `ActionFailed`, `CancelRequested`,
`HumanCandidatesResolved`, `TimerFired`, `SignalReceived`, `MessageReceived`,
`SubInstanceCompleted`, `SubInstanceFailed`.

`allowOnTerminal`: `CompensateRequested` **conditionally** — see below.

⚠ **`CancelRequested` was reclassified from `allowOnTerminal` to `rejectSilently`
by the rule-#9 audit.** The original carve-out rested on a false claim (that
`propagateCancel`'s child loop aborts on error — its doc comment says every error
is "logged and swallowed"), and `rejectSilently` satisfies that loop identically.
Left tolerant, it kept a live route: `forceTerminate` never clears
`RootCompensations`, so `handleCancelRequested` would set `StatusCompensating` on
an already-**Completed** instance, re-fire every compensation `InvokeAction`
against a dead instance, overwrite the terminal status and `EndedAt`, and publish
a **second terminal event** (`terminalOutboxEvent` suppresses only when
`prevStatus.IsTerminal()`, and at that point it is `Compensating`).

Seven policies preserve shipped behaviour; eight change it. Of the eight: **six**
close **reproduced** routes (`StartInstance`, `HumanClaimed`, `HumanReassigned`,
`HumanCompleted`, `SignalReceived`, `MessageReceived`), one is an owner decision
on admin visibility (`ResolveIncident`), and one closes a route the audit found
(`CancelRequested`). `CompensateRequested` is preserved on policy but its error
*message* changes.

> **Correction (2026-08-05, implementation).** This paragraph originally counted
> `MessageReceived` as "an analogous route argued from code shape — not
> reproduced". Phase 1 reproduced it: on a message-catch fixture it behaves
> identically to `SignalReceived` — tokens 2→1, history 4→5 with a post-mortem
> `end-a` visit, and `Variables` `map[]` → `map[x:1]` merged into a dead
> instance. The count is six reproduced, none by analogy. Independently
> re-derived by the task reviewer before being written here.

### 5. The payload-dependent carve-out is absorbed by the mechanism

`CompensateRequested` must reject **resume intent** while allowing a plain full
rollback on a terminal instance — a distinction drawn from the trigger's
*payload*, not its type. A method on the concrete type reads its own receiver, so
no special case is needed:

```go
func (t CompensateRequested) terminalPolicy() terminalPolicy {
    if t.ReverseNode != "" || t.ToNode != "" { return rejectWithError }
    return allowOnTerminal
}
```

This is the property that decided the mechanism. A per-type policy table cannot
express it.

**One condition stays in the handler, deliberately.** A plain full rollback must
still be refused when the instance is terminal *and* the rollback would do harm
without doing any compensation work. That predicate reads **state**, not the
trigger, so `stepCompensateRequested` keeps a narrow guard for it. The
alternative — giving `terminalPolicy` an `*InstanceState` parameter — was
rejected: it stops the policy being a property of the trigger, forces a parameter
on all 15 declarations, and invites policies to reach arbitrarily into state,
which is how the per-handler sprawl began.

> **Correction (2026-08-05, implementation) — this decision's predicate was
> stated backwards, and was refuted by execution before it shipped.**
>
> As originally written, this section said the guard should refuse a plain full
> rollback when compensation records **survive**, and allow it when none do. That
> rests on an assumption that was never measured: that surviving records mean a
> walk re-fires `InvokeAction`s against a dead instance (harm), and that no
> records mean nothing happens (harmless). Both halves are false.
>
> Driven through `Step` on three separate fixtures — two by the implementer, a
> third built independently by the reviewer — the real behaviour is:
>
> - **Terminal WITH surviving records:** a genuine walk. Status goes
>   `terminated → compensating`, `EndedAt` is left alone, and the walk emits a
>   real compensation `InvokeAction`. This is exactly the legitimate admin action
>   [ADR-0164](0164-terminal-transitions-are-one-path.md)'s carve-out #1 protects.
> - **Terminal with NO records:** *no walk at all.* `beginCompensation` finishes
>   immediately — and on the way it flips a **`Failed` instance to
>   `Terminated`**, discards a surviving token, and rewrites `EndedAt`. Zero
>   compensation benefit, and the recorded outcome of a finished process is
>   falsified.
>
> So the predicate as first written would have **refused the useful case and
> admitted the harmful one**. Implementing it verbatim turns four tests red,
> including `TestTerminalResumeGuard/plain_full_rollback_allowed` and
> `TestTerminalDispatchOutcomes/engine.CompensateRequested`, and would leave
> `allowOnTerminal` with no observable positive case anywhere.
>
> **The intent stands; the predicate is inverted.** The guard refuses a plain full
> rollback on a terminal instance when there is nothing left to compensate, and
> allows it when there is. Carve-out #1 is preserved unchanged. The refusal
> returns `ErrInstanceTerminal`, so it classifies 422 alongside the partial and
> targeted rollback rejections; a silent refusal was considered and rejected
> because the "internal re-delivery" rationale for silence no longer applies —
> `CancelRequested` is now `rejectSilently` at `dispatch` and can never reach this
> path.
>
> **"Nothing left to compensate" is not `len(RootCompensations) == 0`.**
> `beginCompensation` calls `consolidateArchiveIntoRoot` *before* it counts, so a
> completed sub-process whose records are still sitting in the archive is a walk
> waiting to happen even while `RootCompensations` is empty. The naive predicate
> would therefore have refused a genuine compensation walk — the very failure this
> correction exists to prevent, reintroduced one layer down. The guard asks
> `hasCompensationRecordsToWalk` (root **or** any non-empty archive slice), and
> asks it without mutating state. Found the same way as the inversion itself: by
> constructing the state and running it, not by reading the code.
>
> Worth recording as a process point: the rule-#9 design audit accepted this
> predicate, and so did every subsequent design-level read. Only running it found
> the inversion. This is the third time in this bundle that reproduce-by-execution
> corrected a decision that review had already blessed.

### 6. A second key: task lifetime

Independent of instance status, `handleHumanClaimed`, `handleHumanReassigned` and
`handleHumanCompleted` gain the `!task.IsOpen()` early-return their sibling
`handleHumanCandidatesResolved` already has. ADR-0163 cancels a task via an
interrupting boundary while the **instance keeps running**, so a closed task
exists on a live instance and the status guard cannot see it. Claim and reassign
**all three return an error**, because they share one synchronous caller
(`deliverTaskTrigger`) and the axis in Decision 3 applies to both keys — an
earlier draft had claim and reassign no-op silently, which the audit flagged as
self-contradictory.

⚠ Two audit corrections. First, `handleHumanCompleted` resolves the **token**
before the task, so a guard placed after its `task == nil` check is unreachable —
the handler is reordered to look the task up first, which also upgrades a vague
`ErrTokenNotFound` to a precise error on the deadline-breach path. Second,
**`ErrTaskNotOpen` already exists** (`runtime/task/service.go:46`) for the
identical condition; rather than ship a second same-named sentinel, the engine
defines it and `runtime/task` aliases it. That alias incidentally fixes a latent
defect: the runtime sentinel is unwrapped today, so it falls through
`httpcore.ClassifyError` to a **500 with an empty body** rather than 422.

> **Correction (2026-08-05, implementation).** An earlier revision of this
> paragraph said the alias fixes a **live** defect, "so `RefreshCandidates` on a
> closed task returns 500 instead of 422". Verified during Phase 6.1: **there is
> no shipped HTTP route for `RefreshTaskCandidates`** — `grep` over `transport/`
> and `examples/` returns nothing, unlike `ResolveIncident`. The status
> improvement is a property of the sentinel plus `ClassifyError`, realised only by
> a consumer who mounts such a route. Still worth doing, and still a fix; just not
> one any shipped route exercises.

It stays deliberately distinct from `humantask.ErrTaskNotFound`, which means a
genuinely absent record.

> **Correction (2026-08-06, `/code-review`).** Phase 5's reorder also moved
> `handleHumanCompleted`'s **no-record** branch ahead of the token lookup, and
> that branch was given `humantask.ErrTaskNotFound` (→ **404**) on the stated
> grounds that this is "what the service layer already returned for an unknown
> id — the engine and the layer above it stop disagreeing". `/code-review` found
> the resulting split: the three sibling handlers still answered
> `ErrTokenNotFound` (→ **422**) for the identical condition, so one missing id
> produced two statuses across four routes.
>
> Resolving it showed the original reasoning was **backwards**, and the fix went
> the other way. `service.deliverTaskTrigger` reads the task store on its FIRST
> line, so a genuinely unknown id is answered there and **never reaches the
> engine**. The engine's `task == nil` branch can therefore only fire for a
> **ghost** — an id the store holds and instance state does not — which is a
> state conflict, not a missing task; 404 would deny a task that demonstrably
> exists. Evidence, not argument: `TestErrConflict_EngineWrongStateClassified`
> has to **seed a synthetic task into the store** to reach the branch at all, and
> converging on 404 turned that test RED by breaking the
> `ErrInvalidTransition` → `service.ErrConflict` chain.
>
> **All four handlers now converge on `ErrTokenNotFound`** (→ 422), which is what
> three of them always did. `HumanCompleted` keeps `humantask.ErrTaskNotFound`
> for the one case only it can see — a token still parked on a vanished record,
> an invariant violation — by re-checking `tokenAwaiting` inside the nil branch.
> That preserves both pre-reorder behaviours instead of collapsing them.
> `TestHumanTaskTriggersAgreeOnUnknownTaskID` pins the convergence.

> **Implementation notes (2026-08-05).**
>
> **`handleHumanCandidatesResolved` keeps a silent no-op, and that asymmetry is
> intentional.** It has the same `!task.IsOpen()` early return as the three
> handlers above, but returns unchanged state rather than `ErrTaskNotOpen`,
> because a closed task's candidate list is frozen audit — restating it is
> pointless, not a caller error, and the runtime feeds candidates back on its own
> schedule rather than in response to a synchronous call. Recorded here so a later
> reader does not "fix" the inconsistency.
>
> **The reorder forces a second error change**, beyond the deadline-breach
> upgrade named above: `HumanCompleted` carrying a task id that was **never
> minted** now returns `humantask.ErrTaskNotFound` rather than
> `engine.ErrTokenNotFound`. This makes the engine agree with the layer above it —
> `service.deliverTaskTrigger` reads the task store first and already returned
> exactly that error for an unknown id, so the two stop disagreeing and no
> transport mapping moves.
>
> **The reorder is load-bearing, proven by mutation.** Reverting only the reorder
> while keeping the guard leaves both `HumanCompleted` cases failing: every path
> that closes a task on a live instance also detaches its token, so `tokenAwaiting`
> would reach `ErrTokenNotFound` first and the guard would be dead code.
>
> `service.deliverTaskTrigger`'s own `!task.IsOpen()` check is now redundant but
> deliberately retained: it reads the **task store** while the engine guard reads
> the **instance snapshot**, and the two can disagree under a concurrent write.

## Consequences

### Good

- **The class closes by construction.** Handler #16 cannot exist without a
  policy. This is the first mechanism in the 1 → 2 → 5 sequence that does not
  depend on a reviewer noticing.

  ⚠ One qualification, stated so the claim is honest: **one condition remains a
  hand-written in-handler guard** — `stepCompensateRequested`'s surviving-records
  check (Decision 5), because it reads state rather than the trigger. Everything
  keyed on instance status alone is structural; that one predicate is not, and a
  future state-dependent policy would face the same choice.
- Policy becomes readable as one table instead of seven scattered `if` bodies
  plus eight silent absences.
- The `HumanCompleted` carve-out becomes a guarantee rather than a side effect of
  whether a token survived.
- **Six routes close** — five reproduced, one (`MessageReceived`) by analogy —
  including the two that delivery 3 would amplify, plus the `CancelRequested`
  route the audit found.
- An admin `ResolveIncident` against a terminal instance now reports failure
  instead of silently succeeding.

### Bad / accepted

- **Breaking change to the exported `Trigger` interface.** Acceptable only
  because `isTrigger()` seals it: no external implementation can exist. A
  consumer who type-switches over triggers is unaffected.
- **Seven behaviour changes.** Their consumer-visible blast radius is **narrower
  than it first appears, and was measured rather than assumed**:
  `service.ClaimTask` / `CompleteTask` / `ReassignTask` / `RefreshTaskCandidates`
  all route through `deliverTaskTrigger`, which already rejects both a closed task
  and a terminal instance ahead of the engine. The genuinely new exposure is
  (a) direct `engine.Step` and `ProcessDriver.ApplyTrigger` consumers — i.e. the
  library-first product surface — and (b) admin `ResolveIncident`, which now
  returns 422 instead of silently succeeding.
- **The service-layer pre-check is TOCTOU** and always was: it tests a snapshot
  loaded at `service/service.go:552`, then applies at `:559` under a fresh CAS
  read. An instance terminating in that window passes the check. The engine guard
  is the authoritative one; the service check remains useful defence-in-depth.
- **Fifteen new methods** (fourteen one-liners plus `CompensateRequested`'s
  payload-dependent one) — real boilerplate, deliberately paid to buy the
  compile-time guarantee.
- **Removing the eight guard sites is the load-bearing risk.** A guard whose removal
  breaks no test was never pinned. Every removal is mutation-verified: delete the
  `dispatch` check, confirm RED, restore. Delivery 2a found exactly this failure
  — a shipped call site whose removal broke zero tests.
- **`CancelRequested`'s godoc is currently false** and must be rewritten:
  `engine/trigger.go:396-401` claims "no harmful side effects occur since there
  are no live tokens or timers to cancel". Three terminal paths keep `s.Tokens`,
  and `InvokeCancelAction` is emitted unconditionally before any token
  inspection.
- Policy now lives in `trigger.go`, away from the handler it governs. Accepted:
  it is a property of the trigger, and co-locating it with the type is what makes
  the payload-dependent case work.
- **One error message changes.** The compensation guard's
  `"workflow-engine: cannot resume a terminal instance (status %v)"` is retired in
  favour of the shared `ErrInstanceTerminal` text, because `dispatch` cannot know
  which of two messages a trigger warrants. `TestTerminalResumeGuard` asserts on
  that string today and moves to `errors.Is`. Consumers matching on the message
  rather than the sentinel would break — they were already told to use
  `errors.Is`.

### Amendments to shipped ADRs

This ADR **replaces the enforcement mechanism ADR-0164 describes** and **deletes
the guard ADR-0109's correction block documents**. Both must carry an
`Amended by ADR-0165` note; without it their text silently goes stale, which is
exactly the failure this delivery inherited from ADR-0162 (whose zombie-scope
sentence went stale the moment 2b merged, and which the spec cites as a caution).
Specifically stale after this lands: ADR-0164's Consequences claim that the guard
is "per-handler and enforced by nothing", ADR-0109's terminal-guard correction,
and four `engine/` godoc sites (`state.go`'s `IsTerminal` note and the
`CompensateRequested` / `NewReverseToStart` / `NewReverseToNode` paragraphs).

### An error-sentinel change the design did not anticipate

Found during implementation, not design. A trigger classified `rejectWithError`
delivered to a terminal instance now returns `ErrInstanceTerminal` where it
previously returned **`ErrTokenNotFound`** — or, for `ResolveIncident`, no error
at all. `ErrTokenNotFound` was a true-but-useless statement about a dead
instance ("no token is awaiting that id"), indistinguishable from a genuinely
mistyped task id.

Both sentinels wrap `ErrInvalidTransition`, so a consumer classifying by that —
and therefore the whole 422 HTTP mapping — is unaffected. A consumer matching
`errors.Is(err, ErrTokenNotFound)` specifically, to detect a dead instance, must
switch to `ErrInstanceTerminal`. The in-repo blast radius was verified to be
zero: the only sentinel-specific consumer is `calllink.CallNotifier`'s
idempotency branch, which sees it solely for the `SubInstance*` triggers, and
those are `rejectSilently` — they return `nil`, never either sentinel.

The two sentinels are deliberately **siblings**, not parent and child, and the
rewritten tests assert `errors.Is(ErrInstanceTerminal, ErrTokenNotFound)` is
**false** so that a later refactor cannot quietly re-merge two conditions this
ADR keeps apart.

> **Correction (2026-08-06, `/code-review`).** The paragraph above says these
> triggers "previously returned `ErrTokenNotFound`". That over-generalises, and
> the CHANGELOG carried the same sentence until this review. It holds only where
> the terminal transition **consumed the tokens** (cancel, force-terminate), so
> no token was awaiting and the old code said so. Where a token **survived** the
> transition there was no error at all: the trigger was applied to the dead
> instance — a post-mortem completion, a re-seeded start, a re-opened task — which
> is precisely the damage this ADR's own before/after table describes. Both the
> ADR and the CHANGELOG now defer to that table rather than compressing it into
> one clause.

### The single enforcement point must not cost diagnosability

Also found during implementation. Collapsing eight guards into one collapses
eight log lines into one, and the per-trigger identity field (`timer_id`,
`command_id`, `task_id`, …) went with them — an operator asking "why did my timer
do nothing" cannot answer it from `instance_id`/`trigger`/`status` alone. The
guard therefore emits the trigger's identity attribute under the key the existing
trigger-key registry already uses, so there is no second hand-maintained mapping,
and degrades to the generic line for triggers that carry no identity key.

This required widening `exemptTriggerKinds` from `map[string]string` to a struct
holding the exemption reason **plus an optional accessor**: *exempt* means the
key may legitimately be empty, not that the trigger has no identity.
`validateTriggerKey`'s behaviour is unchanged.

The one deliberate loss: the guard emits a single constant message rather than
seven trigger-specific ones. The alternative — a per-trigger prose map — is
exactly the second hand-maintained list this ADR exists to avoid, and structured
attributes carry the same information to any log query.

> **Correction (2026-08-06, `/code-review`).** Two diagnosability gaps survived
> to the gate, both from the same false premise — that `rejectWithError` is not
> logged. It is: `dispatch` logs **both** refusal flavours, told apart by
> `outcome`. Executed to settle it, since two comments asserted otherwise.
>
> 1. **`CompensateRequested` carried no identity accessor**, justified in
>    `exemptTriggerKinds` by "its `terminalPolicy` is never `rejectSilently`, so
>    the guard never logs it". A rollback carrying resume intent IS
>    `rejectWithError`, so the line was emitted with **no node on it** — an
>    operator could not tell which rollback was refused. It now registers a
>    `rollback_target` accessor, `ToNode` first, mirroring `walkMode`'s
>    precedence.
> 2. **The third refusal logged nothing at all.** The state-dependent guard in
>    `stepCompensateRequested` returned `ErrInstanceTerminal` silently, making it
>    the one refusal that regressed against this very section's rule that
>    "erroring is not a substitute for the record". It now emits the same message
>    with a `reason` attribute distinguishing it from `dispatch`'s two.
>
> The registry's exempt rows are therefore not merely *allowed* an accessor —
> every variant `dispatch` can log wants one, and `rejectWithError` is no
> exemption from that.

### Release notes

`CHANGELOG.md` has a live `[Unreleased] → Breaking changes (pre-v0.1.0)` section
that ADR-0160 — the last comparable breaking library change — wrote into. This
ADR gets an entry naming the `Trigger` interface method, both sentinels, and the
eight behaviour changes. `v0.1.0` being untagged removes the **SemVer**
obligation, not the release-note one: `STABILITY.md` promises breaking changes
"will be called out explicitly". (ADR-0161–0164 skipped the file. That is a
reason to fix it, not a precedent.)

### Persistence and replay — unaffected, and here is why

Adding an unexported method to `Trigger` needs no codec change: every
implementation lives in `package engine`, and
`internal/persistence/store/trigger_codec.go` type-switches on concrete types.
`Store.Entries` is a read-only audit projection — nothing re-executes journalled
triggers through `Step` — so deterministic replay is not affected. One
consequence worth naming: `writeJournal` runs only on a successful step, so a
`HumanClaimed`/`ResolveIncident` that would previously have been journalled
against a terminal instance now produces no row. Historical journals can therefore
contain entries current code would reject; the journal is append-history, not a
replay log.

### Deliberately not addressed

- **Incident-history retention** and **zombie scopes** — 2b's other two owed
  ADRs, still open.
- **A `service/` or `transport/` change.** Not deferred — *not needed*, because
  `ErrInstanceTerminal` wraps `ErrInvalidTransition` and both layers already
  handle that sentinel. The claim is pinned by a test rather than asserted: a
  single `%v` instead of `%w` anywhere on the path from `handleResolveIncident`
  to the transport mapper would silently downgrade the response to 500.
- **Whether a status flip `Failed → Completed` is reachable** through the
  `HumanCompleted` or `SignalReceived` routes. It is topology-dependent and was
  **not** reproduced; this ADR claims only what was measured. The guard makes the
  question moot.
