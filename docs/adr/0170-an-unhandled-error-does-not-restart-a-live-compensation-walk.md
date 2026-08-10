# 170. An unhandled error does not restart an in-flight compensation walk

- Status: Accepted
- Date: 2026-08-08

> Third ADR of the bundle with [ADR-0168](0168-a-compensation-walk-blocks-completion.md) and
> [ADR-0169](0169-a-delivery-stops-at-a-mid-delivery-terminal.md). Added **during the bundle's
> rule-#9 audit**, which found this defect while attacking ADR-0168's dependency on the
> compensation cursor's lifetime.
> Design: `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md` §5.4.
> Plan: `docs/plans/2026-08-08-compensation-walk-and-mid-delivery-terminal.md`.

## Context

`stepCompensateRequested` guards against re-entering a live compensation walk
(`engine/step_compensation.go:150`):

```go
if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" { … }
```

`handleUnhandledError`'s compensate branch (`engine/step_errors.go:236-243`) has **no such
guard**. It sets `StatusCompensating` and calls `beginCompensation` unconditionally whenever
compensation records exist — including when a walk is already running — and
`beginCompensation` installs a fresh cursor over the live one.

Reproduced by execution on `main` @ `7180114`:

```
start → saga(doA/undoA) → fork ⇒
    A: taskA(user) [bndA "s1"] → rb(CompensateThrow) → endA
    B: taskB(user) [bndB "s2"] → errEnd(EndError "boom", uncaught)
```

```
AFTER s1 (walk starts)     status=compensating activeCmd="i1-c2" resumeNode="endA"
                           finalStatus=running rootComps=1  cmds=[UpdateTask, InvokeAction{i1-c2 undoA}]
AFTER s2 (uncaught error)  status=compensating activeCmd="i1-c3" resumeNode="endA"
                           finalStatus=failed finalErr="boom" rootComps=1
                           cmds=[UpdateTask, InvokeAction{i1-c3 undoA}]
CURSOR CLOBBERED: true ("i1-c2" -> "i1-c3")
AFTER completing the current cursor command
                           status=completed  cmds=[CompleteInstance]
```

**Three defects, not one:**

1. **`undoA` is dispatched twice for one `saga` record** — silently, with no error and no
   WARN. Compensation actions are not required to be idempotent anywhere in this repo's
   contract.
2. **The first walk's command is orphaned.** `i1-c2` was already handed to the runtime; its
   `ActionCompleted` later lands on an instance whose cursor no longer names it.
3. **The uncaught error is silently swallowed and the process reports success.** This is the
   worst of the three and was not visible until the walk was driven to its end. The new
   cursor records `FinalStatus=failed, FinalErr="boom"`, but it *also* inherits
   `ResumeNode="endA"` from the throw walk, and `stepCompensationFinish` takes the
   **resume** branch whenever `ResumeNode` is set, regardless of `FinalStatus`. The instance
   ends `completed` with `CompleteInstance{}` — the error never reaches the caller, the
   terminal event, or the audit view.

**Why it is in this bundle rather than the backlog.** ADR-0168 promotes
`Compensating.ActiveCmdID` from a bookkeeping field into the **instance-completion gate**. A
gate whose field can be silently replaced by an unrelated code path is not a gate. The owner
adjudicated this as fold-in rather than follow-up.

## Decision

**When an unhandled error reaches an instance whose compensation walk is already in flight,
the engine does not start a second walk. The walk in flight *is* the rollback; the error is
recorded as the terminal outcome that walk's finish must apply, and the finish then walks any
records the in-flight walk did not cover.**

```go
// UNCONDITIONAL — above the compensation-records test, mirroring handleCancelRequested.
if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
    // cancel remaining tokens / timers / arms, as beginCompensation would
    …
    s.PendingCancel = true
    s.PendingFinalStatus = StatusFailed
    s.PendingFinalErr = errorCode
    return cmds, nil
}
if len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0 { … }
```

🛑 **AMENDED AT THE DELIVERY GATE (2026-08-10). The originally decided shape was WRONG and is
recorded here rather than quietly replaced.** It read:

```go
if len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0 {   // ← nested
    if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
        …
        s.Compensating.ResumeNode = ""      // ← converted the live cursor in place
        s.Compensating.ResumeScope = ""
        s.Compensating.FinalStatus = StatusFailed
        s.Compensating.FinalErr = errorCode
```

with Decision 1 asserting *"`ResumeNode`/`ResumeScope` are cleared, and that clearing is the
load-bearing part."* **Two defects, both found by the gate's adversarial code review, neither
by the design audit nor by implementation:**

1. **The conversion inherits the in-flight walk's NARROW record source.** The cursor carries
   the walk's `ArchiveKey` (targeted throw) or sub-process `ScopeID` (nested scope-wide throw),
   so every compensation record *outside* that source is never compensated — and for a targeted
   throw is **erased**, because the cursor's `ScopeID` is `""` and `walkAdmin`'s `clearScope`
   nils `RootCompensations`.
2. **The guard was nested inside the records test.** A walk over a sub-process scope's records
   leaves `RootCompensations` and `ArchivedCompensations` both empty, so control fell through
   to `endInstance` and the live walk was **abandoned mid-flight** with its dispatched
   compensation action orphaned.

Measured, three fixtures (before → after):

| fixture | originally-decided shape | reworked shape |
|---|---|---|
| **targeted** throw + root record | `[]`, `rootComps` **1 → 0** — `undoRoot` never ran, record erased | `[undoRoot]` |
| **nested** scope-wide + root record | `[undoB]`, `rootComps=1` stranded uncompensated | `[undoB undoRoot]` |
| **nested** scope-wide, no root record | walk abandoned: `status=failed activeCmd=""`, action orphaned | walk completes, then `FailInstance{boom}` |

⚠ **This was not a regression against working behaviour.** Controller-verified: on unpatched
`main` the same targeted fixture **panics** (`index out of range [0] with length 0` in
`stepCompensationAdvance`). The first shape turned a panic into silent loss; this one turns it
into correct behaviour.

⚠ **Hoisting the guard alone is NOT sufficient — measured**, and independently re-verified by
the controller: un-hoisting the reworked guard fails **only** the third fixture. Hoisting merely
converts the abandonment into the narrow-source bug. The two defects are one root cause — *the
deferred termination inherits whatever record source the in-flight walk had* — and take one fix.

**Why the design missed it:** ADR-0170 was derived from a **single fixture**, the root
scope-wide throw, whose record source happens to be all of `RootCompensations`. The shape was
correct there and wrong for every other throw shape. All four of the audit's mutations reused
that fixture, so none could see the boundary. **A fix derived from one fixture inherits that
fixture's shape as an unstated precondition.**

**1. The error is DEFERRED, not converted. Nothing on the live cursor is touched.** The engine
already ships this protocol for the sibling case: `handleCancelRequested` sets `PendingCancel`
when a cancel arrives mid-resuming-walk, and `applyFinish` consumes it — clearing this walk's
already-compensated records and re-entering `beginCompensation` over the **remainder**. This
decision generalizes that bool into a pending *outcome* (`PendingFinalStatus`/`PendingFinalErr`
on `InstanceState`).

🛑 **CORRECTED AT THE GATE (2026-08-10) — this paragraph originally read "with the zero value
meaning today's cancel outcome so `handleCancelRequested` is byte-identical", and that shape
was a defect.** Leaving the cancel path to the zero value only reproduces its old outcome while
the fields *are* zero, and this very decision gives another writer the right to stamp them.
Measured on the shipped bundle, fixture `walkVsUncaughtErrorDef`, `ActionFailed(doFail,"boom")`
then `CancelRequested` then the walk's `ActionCompleted`:

```
deferred error  → PendingFinalStatus=failed      PendingFinalErr="boom"
CancelRequested → PendingFinalStatus=failed      PendingFinalErr="boom"   ← unchanged
walk finish     → status=failed  FailInstance{Err:"boom"}
```

The operator's cancel silently inherited the superseded error's outcome. `handleCancelRequested`
now stamps `StatusTerminated` + `"cancelled"` **explicitly**, making the two deferral sites
symmetric and last-writer-wins in the same way Decision 3 already documents for successive
errors. `applyFinish`'s zero-value default survives for exactly one input — a snapshot persisted
by a build predating that stamp — and is pinned by
`TestDeferredCancelFromLegacySnapshotTerminatesAsCancelled`; the new behaviour is pinned by
`TestCancelAfterDeferredErrorTerminatesAsCancelled`. Both were mutation-verified.

Rejected alternative: widening the cursor's record source in place. That requires re-basing
`NextIndex` — a bare index with **no record identity** — from the throw's slice onto
`RootCompensations` after consolidation. Any off-by-one **re-dispatches an already-run
compensation**, i.e. it buys the fix with the exact defect this ADR exists to prevent.

⚠ **`PendingCancel` is consumed only where `plan.consumePendingCancel` is set** —
`walkThrowTargeted`, `walkThrowScopeWide`, `walkReverse`. The two walk modes that do not set it
(`walkPartial`, `walkAdmin`) are begun by `beginCompensation`, whose prologue cancels **every**
token; measured, all three admin shapes leave `tokens=0`. No token means no unhandled error, so
those modes can never be live at this guard. Recorded as a **comment naming the closed set
rather than a defensive `else`**: an unreachable branch could carry no falsifiable test, and this
repo's rule against tests that cannot fail applies equally to branches that cannot run.

**2. The token, timer and arm cancellation is kept.** An earlier shape that simply returned
without cancelling was measured and rejected: with a third parallel branch parked at a user
task, it left that branch **live and completable by a human while the instance was already
doomed** — a window that does not exist on `main`, where `beginCompensation` cancels
everything. Measured, three-branch fixture, at the moment of the error:

| | `main` | stamp-only | **decided shape** |
|---|---|---|---|
| cursor | clobbered `c2`→`c3` | preserved | **preserved** |
| `undoA` dispatches | **2** | 1 | **1** |
| branch C at error time | cancelled | **still live** | **cancelled** |
| final status | **`completed`** | `failed` | **`failed`** |

Decided shape, **re-measured after the gate rework** (the `resumeNode=""`/`finalStatus=failed`
line below previously read as the *converted cursor*; under deferral the cursor is untouched):

```
AFTER s2 (uncaught error, walk live)  status=compensating tokens=0 activeCmd="i9-c2"
                                      resumeNode="endA" finalStatus=running finalErr=""
                                      pendingCancel=true pendingFinalStatus=failed
                                      pendingFinalErr="boom"
                                      cmds=[UpdateTask, UpdateTask]      ← no second undoA
AFTER walk finishes                   status=failed tokens=0 cmds=[FailInstance]
```

⚠ The original claim here — *"`go test ./engine/` → EXIT=0, zero failures, so no existing test
pins the old behaviour in either direction"* — was true of the **first** shape and is **false**
of the reworked one: `TestUncaughtErrorDoesNotRestartInFlightCompensationWalk`, written by this
same bundle, pinned the conversion and had to be rewritten to assert the deferral. Blast radius
measured, not assumed: exactly that one test.

**3. The last error wins.** A second unhandled error arriving while the walk is still in
flight overwrites `PendingFinalErr` with the later code. (Heading corrected at the gate: the
body always described last-writer-wins, and the heading said "first". The field is now
`PendingFinalErr`; the behaviour is unchanged.) This is left as-is rather than made
first-wins: it matches `beginCompensation`'s existing last-writer behaviour, and no evidence
was gathered on which is preferable. Recorded as a known, deliberate imprecision rather than
an invariant.

## Consequences

**Positive.**

- 🛑 **ADDED AT THE GATE (2026-08-10): this decision also closes a PANIC on `main`, which is a
  stronger argument than anything else in this section.** Measured against unpatched
  `engine/step_errors.go` with a root-scope compensable service task plus a live throw walk:

  ```
  panic: runtime error: index out of range [0] with length 0
        at stepCompensationAdvance
  ```

  `beginCompensation` copy-and-mutates a cursor it assumes is **zero**. Re-entered mid-walk it
  inherits the live cursor's `ArchiveKey` while `consolidateArchiveIntoRoot` has just nil'd the
  archive that key points into, so the next advance indexes an empty slice.

  ⚠ **The latent invariant is still unguarded.** ADR-0170 removes the only *known* route that
  re-enters `beginCompensation` with a non-zero cursor; it does not make `beginCompensation`
  defend itself. A future caller can reintroduce the panic. `beginCompensation` asserting its
  own precondition is follow-up work, deliberately not done here — recorded so the next author
  does not read "closed" as "impossible".

- A compensation action runs **once** per record, as the compensate-once semantics ADR-0120
  and ADR-0071 describe already intend.
- An uncaught error raised while a compensation throw is in flight is no longer swallowed:
  the instance ends `failed` carrying the error code, so the terminal event, the audit view
  and `terminalEventErr` all report it.
- No orphaned in-flight compensation command.
- ADR-0168's completion gate now rests on a field with a defined lifetime: once a walk is in
  flight, its `ActiveCmdID` is replaced only when that walk finishes.

**Negative / accepted.**

- **The error's ordering relative to the rollback changes what the operator sees.** On `main`
  a mid-walk unhandled error produced a second, immediately-visible `InvokeAction`; now it
  produces none, and the failure surfaces only when the walk ends. An operator watching
  command traffic sees *less* on error than before. The trade is deliberate: the previous
  extra traffic was a duplicate side effect.
- **Last-writer-wins on `FinalErr`** (Decision 3) — and, since the gate correction above, on
  the whole deferred outcome: a cancel arriving after an error terminates the instance
  `terminated`/`"cancelled"`, an error arriving after a cancel terminates it
  `failed`/`<code>`. Neither is "more right"; the rule is simply that the last operator or
  engine decision to reach the instance is the one applied.
- 🛑 **ADDED AT THE GATE (2026-08-10): a stalled walk now has NO engine-level route to a
  terminal state, and for one shape that IS a regression.** If the in-flight compensation
  action is never acked — an unregistered action name, a dead worker — the deferral waits
  forever. Measured on the same fixture family, walk in flight and the compensation action
  never acked, comparing this decision's guard disabled (`main`'s behaviour) against shipped:

  | records live in | `main` (guard disabled) | shipped |
  |---|---|---|
  | a **sub-process scope** (both root lists empty) | `status=failed`, `FailInstance{boom}`, cursor cleared — TERMINAL, and the live walk's `InvokeAction` orphaned | `status=compensating`, no terminal command, cursor live, `PendingCancel=true` |
  | **`RootCompensations`** (root list non-empty) | `status=compensating`, cursor REPLACED (`c2`→`c4`), a **second** `undoA` dispatched — NOT terminal | `status=compensating`, no terminal command, cursor live |

  In both trees `CancelRequested` on the stalled instance returns **zero commands** and no
  terminal (it takes `handleCancelRequested`'s own deferral branch, unchanged since ADR-0039).
  So: for the **root-records** shape ADR-0170 closed no escape that existed — the stall is
  pre-existing. For the **sub-process-scope** shape it converted an immediate `failed` into a
  deferral, and that terminal outcome is genuinely lost. It is the deliberate price of not
  abandoning a live walk and orphaning its compensation action, which is what produced the
  immediate `failed` in the first place.

  An operator escape — a forced termination that abandons a stalled walk on purpose — is a
  **new trigger-level decision** (which trigger? a force flag? what happens to the outstanding
  command?) and is NOT taken here. It is backlog item 13(a), and it is the same escape
  [ADR-0168](0168-a-compensation-walk-blocks-completion.md) already owes under its own escape
  matrix — one decision covers both.
- `handleUnhandledError` now has a **third top-level exit** (not a branch inside the records
  test) whose command shape differs — cancellation commands and no `InvokeAction`. Measurably
  less uniform than before; preferred over duplicating `beginCompensation`.
- **Two new fields on the exported `engine.InstanceState`.** Checked rather than assumed:
  `internal/persistence/store` marshals the whole struct with `json.Marshal` and decodes
  **without** `DisallowUnknownFields`, so the scalars round-trip automatically, a pre-0170
  snapshot decodes them as zero (= today's cancel outcome), and a new snapshot read by an older
  binary is ignored rather than rejected. `service.ProcessInstance`'s JSON projection is an
  explicit allowlist that already omits `Compensating`/`PendingCancel`, so the new fields are
  absent from it with no code change. `cloneState` copies the struct, so scalars need no
  deep-copy work.
- **`pendingKind` is pinned by no test, on either path** — measured: forcing it either way
  leaves the suite `EXIT=0`, because `CloseKind` reaches nothing here except
  `beginCompensation`'s per-token `cancelTokenWaits`, and this re-entry runs with zero tokens.
  It is set for correctness of the value, not for an observable. Recorded rather than papered
  over with a test that could not fail.

**Relationship to the rest of the bundle.**

- **ADR-0168** depends on this. Without it, the completion gate reads a field another path can
  overwrite mid-flight.

  ✅ **Both directions measured during implementation (2026-08-09)**, by execution rather than
  argument. *0170 does not depend on 0168*: with all three of ADR-0168's conjuncts reverted and
  0170 kept, both new tests still pass — the error path returns early from
  `handleUnhandledError` and never reaches the completion sites. *0168 does depend on 0170*:
  a temporary panic inserted at `beginCompensation`'s walking branch, firing when
  `s.Compensating != compensationCursor{}` on entry, **panics without 0170** (printing
  `ResumeNode:"endA" ActiveCmdID:"i-walk-err-c2"`) and does **not** fire with it across
  `engine`, `processtest` and `service`. The probe therefore discriminates — it is not a
  vacuous green.

  ⚠ **A premise this uncovered:** `beginCompensation`'s own comment
  (`engine/step_compensation.go`, the *"`s.Compensating` is the zero cursor here
  (`stepCompensationFinish` always resets it…)"* note) was **false on `main`** for this path —
  exactly the assumption this ADR exists to repair. It becomes true with ADR-0170 for every
  path the container-free suite covers. **Bounded claim: covered paths only**, not a proof over
  all paths; `handleActionFailed` remains unswept (see "Not addressed" below).
- **ADR-0169** is independent — it guards a different predicate (`IsTerminal()`) on a
  different axis (mid-delivery dispatch).
- This is a **pre-existing defect on `main`**, verified by re-running both probes on unpatched
  code and observing identical output. It is not caused by ADR-0168 or ADR-0169.

**Not addressed.** `handleActionFailed` reaches compensation through
`stepCompensationAdvance` rather than `handleUnhandledError`, and was not swept for the same
shape. Stated as a bound, not a claim of completeness — ADR-0164 twice claimed a class closed
and was twice wrong.

⚠ **The other `beginCompensation` call sites were source-verified, NOT swept by execution**
(recorded 2026-08-09 so the bound is not read as wider than it is). `handleCancelRequested`
carries its own in-flight branch (`PendingCancel` deferral / no-op) and `applyFinish`'s call
runs after the cursor was cleared. No fixture was built for either; the panic probe above
reaches them only as far as the existing suite does.
