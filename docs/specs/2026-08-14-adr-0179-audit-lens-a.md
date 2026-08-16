# Lens A (EXECUTION) — adversarial audit of bundle C (ADR-0179)

Worktree: `.../scratchpad/audit-c-a`, detached at `caf0bdc8` (bundle) rebased on `main` `954c2a05`.
STEP 0: bundle PRESENT — all five files confirmed.

Method: every claim below was RUN in this worktree. Verbatim output pasted. Probes deleted after.

---

(findings appended as established)
## F1 — MINOR (but pervasive): every line-number citation in the bundle is stale

**Attacks**: ADR-0179 Context; spec §1, §3.2, §3.4, §3.5; plan P1.2, P1.5, P3.

**Claim**: "the short-circuit at `engine/step_triggers.go:292-294` … returns before
`effectiveRetryPolicy` (`:316`)".

**Probe** (on the rebased worktree, `main` = `954c2a05`):
```
$ grep -n "handleActionFailed" engine/step_triggers.go
332:// handleActionFailed processes an ActionFailed trigger: ...
334:func handleActionFailed(...)
$ grep -n "StatusCompensating && s.Compensating.ActiveCmdID" engine/step_triggers.go
109:	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
188:	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
338:	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
$ grep -n "effectiveRetryPolicy" engine/step_triggers.go
362:	if eff, hasPolicy := effectiveRetryPolicy(node, opt); hasPolicy {
```
**Verdict**: the *construct* is CONFIRMED (the short-circuit exists and returns before
`effectiveRetryPolicy`), the *coordinates* are REFUTED — it is now `:338-340`, and
`effectiveRetryPolicy` is `:362`. Drift is +46 lines. Same for `engine/state.go:475`,
`engine/step_state.go:361`, `engine/step_test.go:94`, `engine/command.go:66`,
`runtime/outbox.go:81`, `runtime/processdriver_action.go:31`,
`step_compensation.go:{524,1287,1294,1345}` — all authored against `12c9d7e3`, two deliveries ago.

**Fix**: replace every `file:line` in the bundle with a symbol name + the enclosing function
(this repo's own lesson: *audited-bundle-decays-when-base-moves*). Notably §3.4's "that range is a
comment" argument is itself line-based and is now unverifiable as written.

⚠ Secondary: there are **three** `StatusCompensating && ActiveCmdID` short-circuits in
`step_triggers.go` (`:109` in `handleActionCompleted`, `:188` the in-flight-walk guard, `:338` in
`handleActionFailed`). The bundle names only the `ActionFailed` one as the site to edit, but §2.4's
duplicate predicate must land on the `ActionCompleted` path (`:109`) **as well** — the spec says
"both reply kinds" in §4.10 but the plan's P1 step 4 says only "duplicate predicate … checked after
the `ActiveCmdID` match" without naming two call sites. See F-dup below.

---

## F2 — ⚠⚠ CRITICAL: extending `walkScoped()` to `TimerCompensationRetry` makes the retry UNREACHABLE in every consumer test harness, and makes the instance lie about being idle

**Attacks**: ADR-0179 Consequences bullet 6; spec §3.11 first bullet; plan P1 step 8
("Extend `walkScoped()` to the new kind") and plan Trap 6.

`walkScoped()` on current `main` is ONE boolean serving TWO different questions:

- **Q1 (ADR-0178, `step_triggers.go:596`)** — *may this timer fire on a **dying** instance?*
  `if !rec.Kind.walkScoped() && !s.spawnsNewWork() { … refuse … }`
- **Q2 (ADR-0175/0177, `state_timers.go:152` `HasArmedTimers`)** — *is this **work the instance
  is waiting on**, i.e. may a harness fire it?*
  `for _, w := range s.TimerWaiters() { if !w.Kind.walkScoped() { return true } }`

`TimerCompensationStall` answers **YES to Q1, NO to Q2** — one boolean suffices for it.
`TimerCompensationRetry` answers **YES to Q1 and YES to Q2** — it *is* work; firing it is exactly
how the walk resumes. The bundle prescribes `walkScoped() == true`, which silently answers **NO to
Q2** and disables the feature in `processtest`.

**Probe** (`engine/zz_lensa_probe_test.go`, `package engine_test`, drives a real cancel-started
walk with a walk-scoped record armed — the closest executable proxy to the proposed retry record):

```
$ go test -count=1 -v -run 'TestZZProbe' ./engine/ ; echo EXIT=$?
=== RUN   TestZZProbeWalkScopedTimerParkClassification
P1 status=compensating tokens=0 records=3
P1 timer kind=TimerCompensationStall id=three-comp-inst-tm1
P1 HasArmedTimers=false
P1 TimerWaiters=1
P1 Park.Reason="unknown" Park.HasArmedTimers=false Park.OpenTasks=0 Park.Incidents=0
P1 AutoTimers decision on this park: fires=false
--- PASS
EXIT=0
```

**Verdict: CONFIRMED, and the bundle's prescription is harmful.** With `walkScoped()==true` a
consumer whose instance is in a compensation-retry **backoff** measures:

- `HasArmedTimers() == false` — the public API says the instance has nothing armed, while it is
  in fact waiting on exactly one timer and cannot progress without it;
- `processtest.Classify(...).Reason == "unknown"` — `park.go`'s documented **KNOWN GAP**
  (compensation walks have no reason of their own);
- `processtest.AutoTimers()` acts **only** on `ReasonTimer`, so it returns `Pass()` → the harness
  reports `ErrUnhandledPark`. **Every consumer test that opts into `CompensationRetryPolicy`
  hangs or errors**, and the ADR's own §4 test 7
  (`TestCompensationRetryReDispatchesUnderAFreshCommandID`) has to hand-fire the timer to pass,
  which means the *shipped* path is never exercised by the harness the repo ships.

⚠ **This is the ADR-0177 lesson repeating, this time as a deliberate prescription.** MEMORY records
that on 0177 the controller prescribed keying the timer rung on `Token.AwaitTimer` and the fix agent
*refuted it by measurement*: "`TimerRetry` is [a record], so retry-backoff parks had classified
`ReasonTimer` all along and the 'fix' would have silently stopped `AutoTimers` advancing every one
of them." ADR-0179 proposes precisely that outcome for the compensation-retry backoff.

⚠ Secondary: `engine/state_timer_waiters.go`'s `walkScoped()` doc comment already asserts
**"ADR-0179's compensation-retry timer is the next such kind."** That sentence shipped to `main` in
ADR-0177/0178 and is, by the measurement above, wrong. It must be corrected in this bundle
(rule #11: implementation corrects design, in-bundle).

**Proposed fix — split the predicate.** `walkScoped()` must become two:

```go
// firesOnDyingInstance reports whether ADR-0178's guard must EXEMPT this kind.
func (k TimerKind) firesOnDyingInstance() bool {
    return k == TimerCompensationStall || k == TimerCompensationRetry
}

// detectionOnly reports whether HasArmedTimers must EXCLUDE this kind — a
// deadline that exists to DETECT a hang, which firing would manufacture.
func (k TimerKind) detectionOnly() bool { return k == TimerCompensationStall }
```

`step_triggers.go:596` uses the first; `HasArmedTimers` uses the second. Then a compensation-retry
backoff classifies `ReasonTimer` (via `HasArmedTimers && !hasCommandWait(tokens)` — a walk holds
zero tokens, so `hasCommandWait` is false, measured P2 below) and `AutoTimers()` advances it, which
is the behaviour a consumer already gets for an ordinary `TimerRetry` backoff.

⚠ The bundle must then also add a **control** test asserting `HasArmedTimers()==true` during a
compensation-retry backoff — otherwise the split is untested in the direction that matters.

---

## F3 — MAJOR: the cause-of-death defect §3.4 describes ALREADY SHIPS today for `IncidentCompensationStall`; the bundle frames it as one this ADR introduces

**Attacks**: ADR-0179 Consequences bullet 1; spec §3.4; plan P2.

**Claim** (spec §3.4): "`Incidents[0]` is **positional**, so on a cancel walk with no prior incident
the new kind *is* index 0 and becomes the published cause of death." — framed as a consequence of
adding `IncidentCompensationFailed`.

**Probe** (`runtime/zz_lensa_probe_test.go`, `package runtime`, using only the **existing**
ADR-0175 kind):

```
$ go test -count=1 -v -run 'TestZZProbe' ./runtime/ ; echo EXIT=$?
=== RUN   TestZZProbeTerminalErrPositional
P7 terminalEventErr = "compensation walk stalled"  (want 'cancelled')
P7 terminalErr      = "compensation walk stalled"  (want 'instance terminated')
P7 outbox topic=instance.terminated payload=map[error:compensation walk stalled]
P7 CONTROL terminalEventErr = "cancelled"
P7 CONTROL terminalErr      = "instance terminated"
P7 TWO terminalEventErr = "compensation walk stalled"  (real cause is at index 1)
--- PASS
EXIT=0
```

**Verdict: CONFIRMED and WIDENED.** `terminalEventErr` reads `st.Incidents[0].Error` **before** it
scans `cmds` for `FailInstance{Err}`, so a walk-scoped incident at index 0 wins over the real
cause on *both* resolvers, and — third line — over a genuine `IncidentAction` sitting at index 1.
This is not a new hazard: it is live today for `IncidentCompensationStall`, whenever such an
incident survives to the terminal edge.

Consequences the bundle does not state:

1. The "shared kind-filtering helper" must exclude **`IncidentCompensationStall` too**, not only
   the new kind, or the delivery ships a fix that leaves the identical bug in place next to it.
2. Filtering changes the two resolvers **asymmetrically**: `terminalEventErr` falls through to the
   `FailInstance{Err}` scan (→ `"cancelled"`), while `terminalErr` has **no such scan** and falls
   straight to `"instance terminated"`. The bundle says "one shared kind-filtering helper used by
   both" as if the outputs then agree; measured, they do not. Decide whether that divergence is
   intended, and say so.
3. The bundle's §4 test 5 ("not published as the cause of death, at both sites") must assert the
   *replacement* string at each site, not merely the absence of the compensation error — otherwise
   it cannot distinguish "filtered correctly" from "filtered and lost the real cause".

**Fix**: restate §3.4 as *"a pre-existing positional-`Incidents[0]` defect that this ADR makes
reachable on the common path"*; make the helper kind-**allow**-list based (publish only
`IncidentAction`), and add the `terminalEventErr` vs `terminalErr` divergence to Consequences.

---

## F4 — MAJOR: the new `IncidentKind` constant inherits an unstated, worse downgrade hazard than the one §3.11 lists

**Attacks**: spec §3.11 "Downgrade hazard" bullet; ADR-0179 Consequences bullet on downgrade.

The bundle's downgrade bullet covers only `DispatchedCmdIDs` and `RetryAttempts`. `IncidentKind`
is an **iota** whose zero value is `IncidentAction`, and `engine/state.go`'s `Incident.Kind` doc
comment already states the hazard verbatim:

```
	// ⚠ Kind enters the persisted snapshot. An OLD build round-trips a NEW
	// snapshot with Kind dropped, degrading an IncidentCompensationStall into a
	// resolvable IncidentAction that the shipped resolve-incident endpoint will
	// then delete — the exact data loss the refusal exists to prevent.
```

`IncidentCompensationFailed` inherits this exactly: on an old build it degrades to
`IncidentAction`, which (a) the resolve-incident endpoint will happily **delete**, and (b) the
F3 kind-filter will **not** filter, so it is republished as cause of death. Severity is *higher*
than for the stall kind, because ADR-0179 deliberately never retires this incident — it is the
long-lived durable record, so it is far likelier to be present when a mixed-version deployment
round-trips the row.

**Fix**: add the `IncidentKind` term to the downgrade bullet in both the ADR and spec §3.11, and
state explicitly whether `handleResolveIncident` must refuse `IncidentCompensationFailed` the way
it refuses `IncidentCompensationStall` — the ADR is **silent on resolvability of the new kind**,
which is a decision, not an omission that implementation may make on its own.

---

## F5 — ⚠⚠ CRITICAL (false claim, and it is the *inherited audit's own fix*): `TestStepDoesNotMutateInput` is NOT "the existing gate this trips". Nothing in `./engine` trips.

**Attacks**: spec §3.2 last line; plan P1 step 5 ("⚠ `TestStepDoesNotMutateInput`
(`engine/step_test.go:94`) is the existing gate this trips").

**Probe** — I actually added the proposed field and left it aliased, exactly as the design would
if the copy line were forgotten:

```go
// engine/state_compensation.go, inside compensationCursor, after ActiveCmdID:
DispatchedCmdIDs []string      // cloneState NOT extended
```

```
$ go test -count=1 -v -run 'TestZZProbeCloneStateAliasesDispatchedCmdIDs|TestStepDoesNotMutateInput' ./engine/ ; echo EXIT=$?
=== RUN   TestZZProbeCloneStateAliasesDispatchedCmdIDs
P8 DispatchedCmdIDs orig[0]="MUTATED" clone[0]="MUTATED" ALIASED=true
P8 CONTROL Records   orig="n1" clone="MUTATED" ALIASED=false
P8 CONTROL Deferred  orig="t1" clone="MUTATED" ALIASED=false
P8 a=[c0 fromB] b=[c0 fromB] A_LOST_ITS_APPEND=true
--- PASS
=== RUN   TestStepDoesNotMutateInput
--- PASS: TestStepDoesNotMutateInput (0.00s)
EXIT=0

$ go test -count=1 ./engine/ ; echo EXIT=$?     # WHOLE package, field still aliased
ok  	github.com/kartaladev/wrkflw/engine	0.293s
EXIT=0
```
(source restored from a `cp` backup; `git diff --stat engine/state_compensation.go` → empty,
`go build ./engine/` OK)

**Verdict on the aliasing itself: CONFIRMED** — `ALIASED=true`, with two working controls
(`Compensating.Records` and `DeferredCompensationThrows` both `ALIASED=false`), and the
two-clones-one-slot corruption reproduces (`A_LOST_ITS_APPEND=true`).

**Verdict on "the existing gate this trips": REFUTED.** `TestStepDoesNotMutateInput` **PASSES**
with the field aliased, and so does the **entire `engine` package**. Reading its fixture explains
why: it builds an instance with `Variables`, one `Scope` and one `CompensationRecord`, drives a
`StartInstance` trigger, and asserts only on `in.Tokens`, `in.Variables` and `in.Scopes`. It
constructs **no compensation cursor at all** (`Compensating` is the zero value) and contains **zero
assertions naming `Compensating`**. It is structurally incapable of observing this aliasing.

⚠ This is the repo's own named defect — *"a matching line of test text proves nothing about whether
an assertion can fail; check the **fixture**, not the line"* — committed **inside the very bundle
whose §4 opens with `⚠ Check the fixture, not the assertion line.`** And it arrived as an
*inherited audit fix* (§3.2), which is precisely the "an audit's own fix falsified another claim in
the same bundle" pattern.

**Why it matters, not just that it is wrong**: an implementer told "the existing gate trips this"
will reasonably treat step 5's new test as belt-and-braces and may weaken or skip it. Measured,
there is **no** safety net: the delivery can ship a fully aliased `DispatchedCmdIDs` with a green
`./engine` run.

**Fix**: delete the `TestStepDoesNotMutateInput` sentence from spec §3.2 and plan P1 step 5, and
replace it with the measurement above:

> Measured: with `DispatchedCmdIDs` added and `cloneState` **not** extended, `go test -count=1
> ./engine/` exits 0 — no existing test observes the aliasing. `TestCloneStateDeepCopiesDispatchedCmdIDs`
> is therefore the **only** gate, and it must be mutation-verified (drop the copy line, observe RED).

Optionally *make* it a gate: extend `TestStepDoesNotMutateInput`'s fixture with a populated
`Compensating` cursor so it covers the cursor generally — but that is a second, separate change and
must not be assumed.

---

## F6 — ⚠⚠ CRITICAL: `DispatchedCmdIDs` lives on a cursor that is **ZEROED at walk finish**, so the headline backlog-3g fix does not cover the window where a duplicate is most likely — and on a *terminating* walk the 422 it claims to close is **already benign today**

**Attacks**: ADR-0179 Decision 4; spec §2.4, §3.1, §3.6, §3.8; plan P1 step 4; §4 test 10.

Two source facts (grep, current `main`):
```
$ grep -n "Compensating = compensationCursor{}" engine/*.go | grep -v _test
engine/state.go:517:	s.Compensating = compensationCursor{}          # endInstance
engine/step_compensation.go:853:	s.Compensating = compensationCursor{}   # stepCompensationFinish
```
A field on `compensationCursor` therefore **ceases to exist the instant the walk finishes**.

**Probe A — terminating (cancel) walk, late duplicate AFTER finish:**
```
P12 walk finished status=terminated dispatched=[three-comp-inst-c4 three-comp-inst-c5 three-comp-inst-c6]
2026/08/14 16:04:43 WARN trigger rejected on terminal instance instance_id=three-comp-inst
    trigger=engine.ActionCompleted status=terminated outcome=dropped command_id=three-comp-inst-c4
P12 late ActionCompleted after finish err=<nil>
```

**Probe B — resuming (compensation-throw) walk, late duplicate AFTER finish:**
```
P13 frame=0 status=compensating tokens=0 spawnsNewWork=true
P13 frame=1 status=compensating tokens=0 spawnsNewWork=true
P13 frame=2 status=running     tokens=1 spawnsNewWork=true
P13 finished status=running dispatched=[thr-inst-c3 thr-inst-c4]
P13 late ActionCompleted AFTER a RESUMING walk finished: err=workflow-engine: no token awaiting command: workflow-engine: invalid state transition: "thr-inst-c3"
P13 late ActionFailed    AFTER a RESUMING walk finished: err=workflow-engine: no token awaiting command: workflow-engine: invalid state transition: "thr-inst-c3"
```

**Probe C — MID-walk superseded reply (the case the design does cover):**
```
P10 walk started, first cmd=three-comp-inst-c4 status=compensating
P10 advanced,   second cmd=three-comp-inst-c5 status=compensating
P10 late ActionCompleted err=workflow-engine: no token awaiting command: … "three-comp-inst-c4" | TokenNotFound=true InvalidTransition=true
P10 late ActionFailed   err=workflow-engine: no token awaiting command: … "three-comp-inst-c4" | TokenNotFound=true InvalidTransition=true
```

**Verdict: the 422 claim is CONFIRMED but its SCOPE is REFUTED.** Partitioning the duplicate space:

| when the duplicate arrives | terminating walk (cancel/error) | resuming walk (compensation throw) |
|---|---|---|
| **mid-walk**, superseded id | 422 today → **fixed** by `DispatchedCmdIDs` | 422 today → **fixed** |
| **after finish** | **already benign today** — ADR-0165's terminal guard drops it, `err=<nil>` (Probe A) | **422 today, and STILL 422 after this ADR** — the cursor holding the set was zeroed (Probe B) |

So the mechanism buys exactly one of the four cells, and the ADR's Decision 4 — *"A late reply to a
superseded command is a benign duplicate"* — is an unqualified quantifier over all four. The
post-finish resuming-walk cell is arguably the *likeliest* one in production: an at-least-once
worker redelivers seconds later, by which time a fast walk has finished and resumed.

⚠ Note also that §4 test 10's "**Fails today**: measured 422 for both" is true only for the
**mid-walk** fixture. Built with a post-finish **cancel-walk** fixture the test **cannot fail** —
Probe A returns `err=<nil>` on today's code. That is a fixture trap of exactly the kind §4's own
preamble warns about.

**Proposed fix (single change, closes three findings at once):** put the id set on
**`InstanceState`**, not on the cursor — e.g. `RecentCompensationCmdIDs []string`, a **bounded ring
of the last K** (K = 16 say), appended at every `compensationInvoke` site and **never cleared by
the walk finish**. This:

- covers the post-finish resuming-walk cell (F6);
- makes the cursor stay **all-scalar**, so §3.2's `cloneState` cursor-invariant breakage
  disappears — `InstanceState` already deep-copies its own `[]string`
  (`s.DeferredCompensationThrows = append([]string(nil), …)`, measured `ALIASED=false`), and the new
  field gets the identical one-liner beside it;
- answers §3.8's boundedness question by construction (a ring is bounded regardless of how many
  times the operator `retry` verb runs).

If the cursor placement is kept instead, the ADR **must** state that the post-finish resuming-walk
duplicate still 422s, and §4 test 10 must specify a **mid-walk** fixture explicitly.

---

## F7 — CONFIRMED premises (recorded so the bundle can cite an executed measurement)

- **"`grep -c slog` over `stepCompensationAdvance` and `handleActionFailed` returns 0"** —
  CONFIRMED, re-derived on current `main` by extracting each function body with `sed` and counting:
  both `0`. A failed compensation action is invisible in all three channels.
- **"With `DefaultRetryPolicy{MaxAttempts:5}`: no re-dispatch, no timer"** — CONFIRMED:
  ```
  P11 after ActionFailed: status=compensating incidents=0 timers=0 nextCmd=three-comp-inst-c5 redispatchedSame=false
  P11   cmd engine.InvokeAction
  ```
  i.e. skip+advance to the next record; zero incidents, zero timer records.
- **"A compensation walk holds ZERO tokens"** — CONFIRMED for **both** walk shapes, at every frame
  where `Status == StatusCompensating`:
  ```
  P2  cancel frame=0..2 tokens=0   (frame=3 status=terminated tokens=0)
  P13 throw  frame=0..1 tokens=0   (frame=2 status=running    tokens=1  ← post-finish resume token)
  ```
  ⚠ **REFINED**: the quantifier "measured every frame" is safe only if scoped to
  `Status == StatusCompensating`. On a throw walk the very next frame after the finish carries
  **one** token. Say so, or a reader building the dying-walk fixture reads "a walk has no tokens"
  and mis-designs around it.
- **`SpawnsNewWork`** — cancel walk `false`, throw walk `true` (measured, both above). The plan's
  Trap 7 / §4 test 12 warning is CONFIRMED.
- **`cancelCompensationStallTimers` filters strictly on `TimerCompensationStall` in BOTH loops** —
  CONFIRMED by source; the emit loop and the rebuild loop each test `tr.Kind == TimerCompensationStall`.
  ⚠ It also short-circuits `if cmds == nil { return nil }` **before** the rebuild loop, so if the
  §3.3 fix widens only the *rebuild* filter the function still returns early when no stall record
  exists — a retry-only walk would leak. Widen **both** loops and the early return.
- **`removeOrphanedIncidents` deletes only `TokenID != ""`** — CONFIRMED by source:
  `inc.TokenID != "" && s.tokenByID(inc.TokenID) == nil`. A `TokenID: ""` incident survives.
  (§3.5's survival claim holds; the prescribed test remains worth writing.)

---

## F8 — ⚠⚠ CRITICAL fixture trap: §4 tests **4** and **12** need OPPOSITE walk fixtures, and the bundle names the constraint for only one of them

**Attacks**: spec §3.3, §4 tests 4 and 12; plan P1 steps 10 and 14, Traps 3 and 7.

§3.3's hazard — *a leaked retry timer fires against a zeroed cursor* — can only occur on a
**RESUME** finish. `stepCompensationFinish`'s own comment says so ("Only the TERMINATE mode reaches
`cancelAllTimers`, via `endInstance`; the four RESUME finishes … never touch `s.Timers`"), and I
**measured** it by mutating `cancelCompensationStallTimers` into a no-op — simulating exactly the
state ADR-0179 creates for a kind the sweep does not cover:

```go
func cancelCompensationStallTimers(s *InstanceState) []Command {
	var cmds []Command
	if true { return nil }   // PROBE: simulate a kind this sweep does not cover
```
```
$ go test -count=1 -v -run 'TestZZProbeLeakByFinishMode' ./engine/ ; echo EXIT=$?
P14 TERMINATE finish: status=terminated leakedTimerRecords=0
P14 RESUME    finish: status=running     leakedTimerRecords=2
P14   leaked kind=TimerCompensationStall id=thr2-tm1
P14   leaked kind=TimerCompensationStall id=thr2-tm2
--- PASS
EXIT=0
```
(restored from a `cp` backup; `git diff --stat engine/` empty; `go test -count=1 ./engine/` EXIT=0)

**Verdict: §3.3 CONFIRMED, and REFINED with a constraint the bundle omits.**

- **§4 test 4 / P1 step 10** ("the retry timer is retired at walk finish") built on the natural
  **cancel-started** fixture (which every other compensation test in `engine` uses, and which §4
  test 12 explicitly *requires*) measures `leakedTimerRecords=0` **with the fix absent** —
  `endInstance`'s `cancelAllTimers` sweeps it. **The test cannot fail.** It must use a **RESUMING**
  walk (`rootSagaWithScopeWideThrow` / `driveToScopeWideThrowWithOptions`).
- **§4 test 12 / P1 step 14** ("fires on a dying walk") must use a **cancel-started** walk, because
  a throw walk measures `SpawnsNewWork=true` (F7). The bundle states this one.

So the two prescribed tests demand *opposite* fixtures and the bundle names the constraint for only
one — leaving the reader to build both on the fixture the bundle put in front of them.

**Fix**: add to §4 test 4 and plan step 10: *"⚠ Use a RESUMING (compensation-throw) walk. Measured:
on a TERMINATE finish `endInstance`'s `cancelAllTimers` sweeps every record regardless of kind, so
a cancel-walk fixture measures `leakedTimerRecords=0` with the fix absent and cannot fail."*

⚠ Additional mechanical detail for §3.3's fix: `cancelCompensationStallTimers` has **three** places
that filter, not one — the emit loop, the rebuild loop, **and an early
`if cmds == nil { return nil }` between them**. Widening only the loops leaves the early return
short-circuiting a walk that armed a retry timer but no stall timer (the exact shape when
`CompensationStallAfter` is unset, which is the **default**). All three must change.

---

## F9 — MAJOR: the rewrite RECORDS four load-bearing decisions and MAKES none of them

**Attacks**: spec §3.8, §3.9, §3.11 (last bullet); plan P1 step 15; ADR-0179 Consequences.

An audited bundle is the input to implementation; a decision deferred to implementation is a hole
that ships as whatever the subagent guesses. The rewrite leaves **four** open:

1. **§3.8 boundedness** — *"Required: **either** cap it (ring of the last K ids) **or** state
   plainly that the operator-driven term is unbounded."* Plan step 15 repeats it verbatim
   ("Decide and record"). Undecided.
2. **§3.9 the backoff-redelivery window** — *"Required: specify the window's state machine
   explicitly."* Not specified anywhere in the bundle. ⚠ This one **blocks §3.3**: §3.9's two horns
   are *keep `ActiveCmdID`* (→ double-arm) vs *clear it* (→ §3.3's late-fire check "cannot be
   written"). §3.3's Required text then simply assumes the check **can** be written. The two
   sections are in direct contradiction and the bundle never resolves it.
3. **§3.11 last bullet** — an instance cancelled during a retry backoff hits ADR-0180's new 409:
   *"Decide or document."* Undecided.
4. **Resolvability of the new incident** — not raised at all. `IncidentCompensationStall` is
   explicitly refused by `handleResolveIncident`; `IncidentCompensationFailed`, being deliberately
   never retired, is a *permanent* entry in the instance document, so whether an operator may
   resolve it is a first-order API decision. The bundle is silent, so implementation will default
   to "resolvable" simply by not writing the refusal.

**Fix**: decide all four in the ADR before implementation. Concretely:

- **(1)** the F6 fix (an `InstanceState`-level ring of the last K ids) settles it by construction.
- **(2)** the contradiction resolves if `ActiveCmdID` is **retained** through the backoff and the
  double-arm is prevented by **idempotent arming** instead: `armCompensationRetryTimer` returns
  early when `s.Timers` already holds a `TimerCompensationRetry` whose `CommandID` equals the
  cursor's `ActiveCmdID`. That keeps §3.3's late-fire check writable —
  `handleCompensationStallFired`'s existing guard is exactly
  `if s.Status != StatusCompensating || rec.CommandID != s.Compensating.ActiveCmdID { … no-op }`,
  which the retry handler can mirror verbatim — while making
  `TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm` a test of one named function rather
  than of an unspecified state machine.
- **(3)** state the chosen behaviour in Consequences (documenting the 409 is acceptable; leaving it
  to be discovered is not).
- **(4)** state whether `handleResolveIncident` refuses the new kind, and add the test either way.

---

## F10 — MINOR: two more premises re-derived on current `main` (both CONFIRMED)

- **"Four `compensationInvoke` dispatch sites today, exactly where `ActiveCmdID` is set"** —
  CONFIRMED on `954c2a05`:
  ```
  $ grep -rn "compensationInvoke" engine/ | grep -v _test      # call sites, excluding the definition
  engine/step_compensation.go:412 / :574 / :1301
  engine/step_nodes.go:1139
  $ grep -rn "ActiveCmdID = " engine/*.go | grep -v _test
  engine/step_compensation.go:405 / :567 / :1296
  engine/step_nodes.go:1131
  ```
  4 and 4, pairwise adjacent. §3.6's "adds a fifth" premise survives the base move.
- **`engine/command.go`'s `ScheduleTimer.Kind` comment** — CONFIRMED still wrong (now ~`:66-67`,
  not `:66`): *"Kind distinguishes intermediate, deadline, in-wait, and retry timers"*, omitting
  `TimerCompensationStall`. P3's doc-only fix is still needed, and must add **both** missing kinds.

---

## Summary of verdicts

| # | severity | claim | verdict |
|---|---|---|---|
| F1 | MINOR (pervasive) | every `file:line` citation | REFUTED (coordinates), constructs CONFIRMED |
| F2 | **CRITICAL** | "`walkScoped()` must gain `TimerCompensationRetry`" | **CONFIRMED-AND-HARMFUL** — disables the feature in `processtest`; predicate must be split |
| F3 | MAJOR | `Incidents[0]` cause-of-death | CONFIRMED **and pre-existing** for the stall kind; fix must cover both, resolvers diverge |
| F4 | MAJOR | downgrade hazard | INCOMPLETE — `IncidentKind` iota term missing; resolvability undecided |
| F5 | **CRITICAL** | "`TestStepDoesNotMutateInput` is the existing gate" | **REFUTED** — whole `./engine` package passes with the field aliased |
| F6 | **CRITICAL** | "a late reply … is a benign duplicate" | SCOPE REFUTED — cursor zeroed at finish; terminate-walk case already benign today |
| F7 | — | slog=0 / no retry / zero tokens / `SpawnsNewWork` / sweep filters | CONFIRMED (one quantifier REFINED) |
| F8 | **CRITICAL** | §4 tests 4 and 12 fixtures | test 4 **cannot fail** on the natural fixture; opposite fixtures required |
| F9 | MAJOR | four decisions deferred to implementation | not implementation-ready as written |
| F10 | MINOR | dispatch-site count, `command.go` comment | CONFIRMED on current `main` |

Probes deleted; numbers kept above.
