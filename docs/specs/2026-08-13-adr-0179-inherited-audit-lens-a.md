# Audit lens A (EXECUTION) — engine-visibility-and-truthfulness bundle

Date: 2026-08-13. Worktree HEAD after recovery merge: 9b404709 (bundle present).

Targets:
- T1 ErrCancelNotApplicable vs propagateCancel child loop (ADR-0180)
- T2 StartedAt.IsZero() predicate (ADR-0180)
- T3 spawnsNewWork() during compensation walk (spec 5.1)
- T4 vacuous prescribed tests (plan)

## Log
- [step0] Bundle was ABSENT at base commit 12c9d7e3; recovered via `git merge --no-edit feat/engine-visibility-and-truthfulness` -> 9b404709. All 9 bundle files present.

## FINDING 1 (CRITICAL) — T1: the child loop is the wrong place to look. A new error from `:210` orphans the whole child subtree.

Documents: ADR-0180 Consequences bullet 3; spec 4b / 9.5 / 10 risk table row 5; plan P4 step 6.

The bundle asks only "is the error absorbed by `propagateCancel`'s child loop?". It is. That is
not the hazard. Two other control-flow effects, both measured:

MUTATION: `handleCancelRequested` returns an error for DefID `prop-parent` / `prop-child-gc`
(simulating the new `ErrCancelNotApplicable` from `:210`), then existing runtime fixtures run.

(a) PARENT site — `runtime/processdriver_cancel.go:26-29` returns the error BEFORE the
`callLinks != nil && defsReg != nil` propagation block at `:30-33`:

    child status BEFORE parent cancel = running
    CancelInstance err = workflow-runtime: step: workflow-engine: AUDIT PROBE cancel not applicable
    child status AFTER  parent cancel = running   (IsTerminal=false)

Today: child terminated. After the change: child left RUNNING forever. The parent's own walk
still drives it to a terminal end, so the operator ends up with a terminated parent and orphaned
async children — a strictly worse outcome than today's silent-nil.

(b) CHILD loop — the error IS absorbed (`continue`, `:89`), but `continue` skips the RECURSION at
`:92`, so the child's entire subtree is skipped:

    WARN runtime: propagateCancel: cancel child instance failed child_id=prop-parent-gc-i1-sub-8664270d error="... AUDIT PROBE cancel not applicable"
    child must be Terminated:      expected 4 (terminated) actual 0 (running)
    grandchild must be Terminated: expected 4 (terminated) actual 0 (running)

`EXIT=1` on `go test -count=1 -run '^TestCancelPropagation(ParentAndChild|Grandchild)$' -v ./runtime`
with the mutation; both tests are green without it. So "logs-and-continues" is TRUE and still
loses the subtree.

`CancelRequested.terminalPolicy() == rejectSilently` is irrelevant here: that policy governs
triggers on TERMINAL instances, and the `:210` site is reached only on a NON-terminal
(compensating) instance. Citing it as the reason the error is safe is a category error.

FIX (concrete): `ErrCancelNotApplicable` must be a *reporting* outcome, not a propagation-halting
error.
  - In `ProcessDriver.CancelInstance`, treat it specially: `if err != nil && !errors.Is(err,
    engine.ErrCancelNotApplicable) { return st, err }` — i.e. still run `propagateCancel`, then
    return the sentinel to the caller. Same in `propagateCancel`'s child loop: on
    `errors.Is(cancelErr, engine.ErrCancelNotApplicable)` log and **recurse anyway** rather than
    `continue`.
  - Add both to §9.5 as required tests: "parent's dropped cancel still terminates its children"
    and "a child's dropped cancel still reaches the grandchild". Both fail today under the naive
    implementation (proved above).
  - Delete the ADR-0180 Consequences bullet claiming the loop "should absorb it by design" — it
    absorbs it and still regresses.

## FINDING 2 (CONFIRMED-OK) — T2: the `!StartedAt.IsZero()` predicate holds. `StatusRunning` really is the zero value.

Executed:
    zero InstanceState.Status   = running (String="running") IsTerminal=false
    StatusRunning               = running (int 0)
    zero InstanceState.StartedAt= 0001-01-01 IsZero=true

The prescribed guard was PATCHED IN (`if !s.StartedAt.IsZero() { return StepResult{}, err }` at the
top of `handleStartInstance`) and the whole container-free surface run:
    go test -count=1 ./engine/... ./runtime ./runtime/calllink ./runtime/signal ./runtime/task \
        ./service/... ./processtest/... ./transport/http/...   -> ALL ok, EXIT=0
No legitimate start path regressed. Source-verified the three runtime start paths all build a
FRESH `engine.InstanceState{InstanceID: id}` (`processdriver.go:447` Drive, `:483` createAtNode,
`processdriver_child.go:59-60` runChild) -> StartedAt zero at entry.
Journal replay is a NON-issue: `UnmarshalTrigger`'s only consumer is `store_core.go:310`
(`Store.Entries`, a history read) — never fed back through `Step`. Same conclusion the
2026-08-02 plan reached at its finding A2. NOT executed (Docker-only package); source + prior
adjudication only.

## FINDING 3 (MAJOR) — T2 corollary: a ZERO `OccurredAt` defeats the guard entirely, on the exact entry point ADR-0180 is closing.

Documents: ADR-0180 Decision 1; spec §4a; plan P4 steps 1-2; §9.4.

`s.StartedAt = t.OccurredAt()` is the ONLY writer. `engine.Step` is public API and the caller
supplies `at`. With the prescribed guard in place:

    CONTROL   start#1 err=<nil> tokens=1 StartedAt=2026-06-20 10:00:00 UTC
    CONTROL   start#2 err=<AUDIT PROBE instance already started> tokens=0     <- refused
    ZERO-TIME start#1 err=<nil> tokens=1 StartedAt=0001-01-01 IsZero=true
    ZERO-TIME start#2 err=<nil> tokens=2                                       <- SUPERIMPOSED

So the "two unguarded entry points" the ADR names (`engine.Step`, `ProcessDriver.ApplyTrigger`)
remain unguarded for any consumer that passes a zero time — and a consumer hand-driving `Step`
(the documented embedded-library usage, `engine/README.md:236`) is precisely who might.

FIX (pick one, state it in ADR-0180 Decision 1):
  (a) Guard on `s.StartedAt.IsZero() && len(s.Tokens) == 0 && len(s.History) == 0` — belt and
      braces; or better
  (b) Add an explicit `Started bool` / reuse an existing non-time marker, so the predicate does
      not depend on caller-supplied time; or
  (c) Accept it and say so: add to Consequences "ASSUMPTION/LIMIT: a `StartInstance` stamped with
      the zero time leaves the guard inert", plus a test pinning that limitation so nobody later
      believes the guard is total.
Whichever is chosen, §9.4's test matrix needs a third row (zero-time start) — today's two rows
(refusal + control) both pass under the defective predicate.

## FINDING 4 (MAJOR) — T3: "a compensation walk is, by definition, an instance no longer spawning forward work" is FALSE. The hazard is real, its justification is not, and the prescribed test can be built vacuous.

Documents: spec §5.1 (verbatim quantifier); ADR-0178 Decision; plan P3 step 3; spec §9.2.

Executed (`engine.SpawnsNewWork` on two mid-walk fixtures):

    (A) THROW walk  status=compensating ActiveCmdID="probe-throw-c3" ResumeNode="afterThrow"
        ReverseNode="" ToNode="" PendingCancel=false  => SpawnsNewWork=TRUE
    (B) CANCEL walk status=compensating ActiveCmdID="three-comp-inst-c4" ResumeNode=""
        ReverseNode="" ToNode="" PendingCancel=false  => SpawnsNewWork=FALSE

`state.go:534-546`: for `StatusCompensating` it returns `!s.Compensating.walkTerminates(s.PendingCancel)`
— a RESUMING walk (compensation throw ADR-0039 B1, admin partial rollback ToNode, full reverse
that resumes) spawns new work; only a TERMINATING walk does not.

Two consequences:

1. §5.1's hazard SURVIVES (do not delete it): on a cancel/error/full-reverse walk
   `spawnsNewWork()==false`, so a blanket path-4 guard really would kill ADR-0179's retry. The
   `walkScoped()` exemption is still required. But the sentence justifying it is a false
   universal and must be rewritten to name the closed set: "a TERMINATING compensation walk
   (cancel / unhandled error / full reverse, or any walk with PendingCancel) has
   spawnsNewWork()==false; a RESUMING walk (throw, admin partial rollback) has it TRUE —
   measured".
2. ⚠ The prescribed cross-ADR test is VACUOUS IF BUILT ON THE OBVIOUS FIXTURE. Plan P3 step 3
   ("a compensation retry timer still fires on a dying walk") and §9.2 do not pin the walk KIND.
   The most convenient in-repo mid-walk fixture is `driveToScopeWideThrow` — a THROW walk, where
   `spawnsNewWork()==true`, so the retry fires **whether or not** the `walkScoped()` exemption
   exists. The test would pass against a blanket refusal — exactly the failure mode the plan says
   it is there to catch.

FIX: in plan P3 step 3 and spec §9.2, NAME the fixture and assert the premise —
"build the retry on a CANCEL-initiated walk (`runThreeCompensableActivities` + `CancelRequested`,
measured `SpawnsNewWork=false`) and `require.False(t, engine.SpawnsNewWork(&st))` in the test
BEFORE firing the timer". Add the same premise assertion to ADR-0178's stall-exemption test.
Rewrite §5.1's universal as the closed set above.

## FINDING 5 (MAJOR) — T4: ADR-0179's own retry adds a FIFTH `compensationInvoke` site, so P2 step 9 ("all four") and the test named `TestAllFourDispatchSitesRecordTheirCommandID` are obsolete the moment P2 lands.

Documents: plan P2 steps 6, 9, 10; spec §9.3(e).

Re-counted, `grep -rn "compensationInvoke(" engine | grep -v _test.go` — the four the plan names
are correct TODAY:
    step_compensation.go:412   (beginCompensation first dispatch)
    step_compensation.go:574   (stepCompensationAdvance)
    step_compensation.go:1301  (retryStalledCompensation — operator stall retry, ADR-0175)
    step_nodes.go:1134         (throw walk first dispatch)
    [503 is the constructor itself, not a site]

But P2 step 6 says the automatic retry is "re-dispatch **modelled on** `retryStalledCompensation`"
— i.e. a NEW function that calls `compensationInvoke` a fifth time. P2 step 9 then appends to
`DispatchedCmdIDs` "at **all four** dispatch sites", which would MISS the retry's own dispatch:
a late reply to a superseded AUTOMATIC-retry command still 422s, which is precisely the defect
ADR-0179 exists to close. And P2 step 10's test name hard-codes the stale count.

This is the ADR-0175 counting failure repeating inside the very bundle that documents it (spec §6
row 5 corrects "the third of the three" → four).

FIX: P2 step 9 → "every `compensationInvoke` call site, re-derived by `grep` AFTER step 6 lands
(four today, five after the automatic retry)". Rename the test
`TestEveryCompensationDispatchSiteRecordsItsCommandID` and have it derive the set instead of
hard-coding it — or at minimum assert the count against a named constant updated in step 6.

## FINDING 6 (MINOR) — T4: P5's comment sweep misses a comment that THIS bundle falsifies.

Documents: spec §6 (the seven-row table); plan P5.

`engine/step_compensation_stall_incident_test.go:79-88` (comment on
`TestStallIncidentIsRaisedOnADyingWalk`) states: "path 4 sits AHEAD of the `!spawnsNewWork()`
guard, so a dying walk still reports" (also repeated as the `require.Len` message at :99). After
ADR-0178, path 4 HAS a guard; the stall record survives only via the `walkScoped()` exemption.
The sentence becomes false in the same commit. §6's table lists six shipped-code comments and
none is this one.

FIX: add row 8 to §6 and to P5 — "after ADR-0178 the stall record survives path 4's guard via
`Kind.walkScoped()`, not by sitting ahead of it".

## FINDING 7 (MINOR) — T4: the ADR-0177 KNOWN-LIMITATION pin (P1 step 8) is close to vacuous as written.

Documents: spec §1 "KNOWN LIMITATION (pinned, falsifiable)"; plan P1 step 8.

Prescribed: "a rehydrated pre-change snapshot (no `AwaitTimer`) reports no token-source waiter …
fails the day someone closes it". A `Token` with an empty `AwaitTimer` yielding no waiter is the
accessor's own definition; the test can only fail if the backfill is implemented INSIDE
`TimerTokenWaiters` (by sniffing `AwaitCommand`). A backfill done where it would actually be done
— a persistence migration or the rehydrate path — leaves this test green and the limitation
silently closed with the "pin" still claiming otherwise.

FIX: state the falsifier explicitly in §1 and P1 step 8: "fails only if `TimerTokenWaiters` starts
inferring from `AwaitCommand`", and add the complementary assertion that the SAME token WITH
`AwaitTimer` set does produce the waiter (that one really does fail before P1 step 4).

## GOOD (no finding) — the stall exemption's existing pin is sound.

`TestStallIncidentIsRaisedOnADyingWalk` (`engine/step_compensation_stall_incident_test.go:89`)
already does exactly what FINDING 4 asks of the retry test: it uses a cancel-started walk via
`startedStallWalk` and asserts its own premise with
`require.False(t, engine.SpawnsNewWork(&state))`. Its `require.Len(res.State.Incidents, 1)` WOULD
catch a blanket path-4 guard. Use it as the template for §9.2's retry sub-case.
