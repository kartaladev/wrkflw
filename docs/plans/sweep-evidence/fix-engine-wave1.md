# Engine wave 1 — fix evidence (2026-08-20)

Agent: engine wave-1 fixer. Working tree `main`, no branch, no commit (controller commits).
Verification command used throughout — **exit code, never a pipeline** (Common Pitfall #4):

```
go test -count=1 ./engine/... > /tmp/eng.log 2>&1; echo "EXIT=$?"
```

---

## Item 74 — a consumer `IDGenerator` emitting an `evtgw:`-prefixed id cross-wires tokens

**Status:** DONE.

**Files changed**

- `engine/step_gateways.go` — `resolveGatewayWin`: the gateway-token lookup is now
  by identity (`s.tokenByID(ae.GatewayToken)`) with the sentinel retained only as a
  *confirmation* (`tok.AwaitCommand == "evtgw:"+ae.GatewayToken`), replacing the
  exact-equality string lookup `s.tokenAwaiting("evtgw:" + ae.GatewayToken)`.
- `engine/step_gateway_identity_test.go` — **new**, `package engine_test`:
  `TestEventGatewayWinResolvesTokenByIdentity` (2 cases) + `scriptedIDGen` +
  `evtgwIdentityDef`.

`engine/step_cancel.go:26`'s `strings.HasPrefix(tok.AwaitCommand, "evtgw:")` was
**not** touched — the brief and the triage both record it as benign.

**Premise probe (EXECUTED, then deleted)** — the mint order was measured, not guessed.
A throwaway `TestZZProbe` over `evtgwIdentityDef` with a plain sequential generator
printed:

```
mint log: [id1 id2 id3 id4 id5]
token[0] id="id2" node="task"  state=1(Waiting) awaitCmd="id4"
token[1] id="id3" node="evtgw" state=1(Waiting) awaitCmd="evtgw:id3"
task[0]  id="id4" node="task"
cmd[0] engine.AwaitHuman  {TaskID:id4 ...}
cmd[1] engine.ScheduleTimer{TimerID:id5 Token:id3 ...}
status=running
```

So: #1 root token, #2 task-branch token, #3 gateway-branch token (`id3`), #4 human-task
id, #5 gateway timer id — and the task token sits at **index 0**, ahead of the real
gateway token, which is what makes the first-match lookup return the wrong one. The
test scripts call #4 → `"evtgw:id3"`.

**What makes the test fail today:** `tokenAwaiting` returns the *first* token whose
`AwaitCommand` equals the sentinel; with the collision that is the user-task token.

**Observed RED** (`EXIT=1`):

```
--- FAIL: TestEventGatewayWinResolvesTokenByIdentity (0.00s)
    --- FAIL: TestEventGatewayWinResolvesTokenByIdentity/colliding_human-task_id_does_not_impersonate_the_gateway_token (0.00s)
        step_gateway_identity_test.go:130:
            Error:      Expected nil, but got: &engine.Token{ID:"id3", NodeID:"evtgw", ScopeID:"", State:1, AwaitCommand:"evtgw:id3", ...}
            Messages:   the gateway token must leave evtgw when its timer arm wins
        step_gateway_identity_test.go:134:
            Error:      Expected value not to be nil.
            Messages:   the user-task token must stay parked on its own node
```

That is the damage the backlog describes, reproduced exactly: the real gateway token
`id3` stays parked at `evtgw` with `arms` consumed, and the user-task token has been
driven off its own node down the gateway's timer branch.

An earlier RED on the same file was a **compile error** — `tokenAtNode redeclared in
this block` (`engine/step_scope_drain_test.go:838` already defines an identical
helper). The duplicate was deleted and the existing helper reused.

**Observed GREEN:** `EXIT=0`, `ok github.com/kartaladev/wrkflw/engine 0.482s`.

**Vacuity check:** the table's second case ("distinct ids route the gateway token
normally") is a **control** and passes both before and after — it is there to prove
the fixture really drives a gateway win, and is documented as such in the test's
doc comment. The load-bearing case (#1) was RED before the fix.

**Backlog statements found false:** none for item 74. Both halves of the triage entry
(the exact-equality lookup is the damage; the prefix match is benign) held under
execution.

---

## Item 75 — the wall-clock purity guard is evadable by import alias

**Status:** DONE.

**Files changed**

- `engine/purity_test.go` only (the guard is test-only):
  - `wallClockCalls` now resolves the file's **local** name for the `"time"` import
    from the **import path** instead of matching the spelling `time`; new helpers
    `timeLocalNames` (alias / dot / blank / plain) and `isWallClockName`.
  - `TestPurity_ASTDetectsWallClock` rewritten as a 5-case table (`assert` closure
    form, per the `table-test` skill).

**What makes each new case fail today** — recorded per case in the table's comments:
alias (`pkg.Name != "time"` short-circuited), dot import (a bare `*ast.Ident` call was
never inspected), foreign package aliased to `time` and a local variable named `time`
(the old matcher keyed on the spelling, so both were false positives).

**Observed RED** (`EXIT=1`) — 4 of 5 cases, the control green:

```
--- FAIL: TestPurity_ASTDetectsWallClock (0.00s)
    --- FAIL: TestPurity_ASTDetectsWallClock/aliased_time_import (0.00s)
        purity_test.go:151: wallClockCalls reported no wall-clock read; want "Now"
    --- FAIL: TestPurity_ASTDetectsWallClock/dot-imported_time (0.00s)
        purity_test.go:151: wallClockCalls reported no wall-clock read; want "Since"
    --- FAIL: TestPurity_ASTDetectsWallClock/unrelated_package_aliased_to_the_name_time (0.00s)
        purity_test.go:151: wallClockCalls reported [Now]; want no wall-clock read
    --- FAIL: TestPurity_ASTDetectsWallClock/local_identifier_named_time_with_no_time_import (0.00s)
        purity_test.go:151: wallClockCalls reported [Now]; want no wall-clock read
```

**Observed GREEN:** `EXIT=0`, `ok github.com/kartaladev/wrkflw/engine 1.981s`.

### End-to-end evasion proof (EXECUTED — both directions)

The table proves the *matcher*. The claim that matters is about the *guard*, so it was
run against a really-patched tree. `engine/idgen.go` was `cp`-backed-up to
`/tmp/idgen.go.bak` and temporarily given

```go
import (
	"fmt"

	chrono "time"
)

func aliasEvasionProbe() int64 { return chrono.Now().Unix() }
```

1. **New matcher, aliased import present** → `EXIT=1`:

   ```
   --- FAIL: TestCorePurityNoWallClock (0.01s)
       purity_test.go:204: idgen.go calls time.Now: the pure engine core must take time from clockwork.Clock, not the wall clock
   ```

2. **Old matcher restored, same aliased import still present** → `EXIT=0`:

   ```
   === RUN   TestCorePurityNoWallClock
   --- PASS: TestCorePurityNoWallClock (0.01s)
   PASS
   ok  	github.com/kartaladev/wrkflw/engine	0.459s
   ```

   This is the direct execution of the backlog's central claim — the evasion was real
   and silent, not inferred from the matcher's shape.

**Restore:** both files restored with `cp` from backups (**never** `git checkout <path>`
— other agents hold uncommitted work in this tree). `diff` reports `purity_test.go`
byte-exact against the backup, and `git diff --stat -- engine/idgen.go` is empty, so
`idgen.go` is byte-identical to `HEAD`. Suite re-run after restore: `EXIT=0`.

**Backlog statements confirmed / found false**

- ✅ **CONFIRMED, and it is the audit that was wrong:** the vendor denylist is **not** a
  second line of defence. `deniedEngineImports` = `{"/transport/",
  "/internal/persistence", "/runtime/", "watermill", "gocron", "clockwork", "casbin",
  "go.opentelemetry.io"}` — `"time"` is not, and never was, an entry, and step 2 above
  shows the whole purity suite green with a live aliased wall-clock read in a non-test
  engine file.
- Nothing in item 75's triage entry was found false.
- **Newly found, beyond the filed item:** the old matcher also produced **false
  positives** — it flagged any `X.Now()` where `X` was merely *spelled* `time`
  (a foreign package aliased to that name, or a local variable). The filed item covers
  only the false-negative half. Both are fixed and both are pinned by the table.

---

## Blocker 8 — the `forceTerminate` → `endInstance` boundary sweep

**Status:** DONE, with the filed defect **REFUTED**.

### ⚠ The filed statement is FALSE

Blocker 8 is filed as *"the `forceTerminate` → `endInstance` boundary sweep is
**entirely uncovered**"*. Measured on this tree (`go test -count=1
-coverprofile=… ./engine/`, EXIT=0):

| symbol | coverage before |
|---|---|
| `engine` package total | **93.0 %** |
| `endInstance` | **100.0 %** |
| `cancelAllScheduledWork` | **100.0 %** |
| `forceTerminate` | **90.0 %** |
| `cancelAllArmsAndBoundaries` | **80.0 %** |

Exactly **three** statement blocks on that path carried a zero count, and they are
the only thing this item legitimately covers:

```
engine/state_arms.go:142.35,143.23 1 0   ┐ the ArmedEvents loop in
engine/state_arms.go:143.23,145.4  1 0   ┘ cancelAllArmsAndBoundaries
engine/step_nodes.go:628.19,630.4  1 0     forceTerminate's empty-reason default
```

**Additional nuance the backlog does not state:** within that same loop the
**`Boundaries`** half was already covered (`state_arms.go:148-151`, count 1). Only the
**`ArmedEvents`** half was cold — i.e. every pre-existing caller reached
`cancelAllArmsAndBoundaries` with an *empty* `s.ArmedEvents`. No test was written for
the boundary half, per the brief.

**Files changed**

- `engine/step_terminal_arms_test.go` — **new**, `package engine_test`:
  `TestForceTerminateCancelsEventGatewayArms` (2 cases),
  `TestEventGatewayArmIsNotATimerRecord`, `forceTerminateWithArmDef`,
  `assertCancelsTimer`. **No production file was changed** — the code was already
  correct; only the coverage was missing.

The fixture forks a live event-gateway timer arm alongside a force-termination end
event, so one `Step` reaches both cold blocks. The gateway branch is declared first so
`drive()` arms the event before the terminating branch runs.

**Coverage after:** `cancelAllArmsAndBoundaries` **100.0 %**, `forceTerminate`
**100.0 %**, package total 93.0 % → **93.1 %**. All three previously-zero blocks now
report count 1.

### Falsifiability — mutation proof (a coverage test cannot go RED on its own)

These tests pin already-correct code, so there is no honest RED from writing them.
Both were proved by mutation instead; each was `cp`-backed-up, mutated, run, and
restored (**never** `git checkout <path>`).

**Mutation 1** — delete the `ArmedEvents` loop from `cancelAllArmsAndBoundaries`,
leaving `s.ArmedEvents = nil`. `EXIT=1`, and **both** cases went RED:

```
--- FAIL: TestForceTerminateCancelsEventGatewayArms/empty_termination_reason_falls_back_to_the_default
    Error:      Not equal: expected: 1  actual  : 0
    Messages:   force-termination must emit exactly one CancelTimer for the event-gateway arm "i1-tm1"
```

**Mutation 2** — remove the `reason = "force-terminated"` default. `EXIT=1`, and it
**discriminates**: only the empty-reason case went RED, the explicit-reason control
stayed green.

```
--- FAIL: TestForceTerminateCancelsEventGatewayArms/empty_termination_reason_falls_back_to_the_default
    Error:      Not equal: expected: "force-terminated"  actual  : ""
```

Both files restored and `diff`-confirmed byte-exact; `git diff --stat -- engine/
step_nodes.go engine/state_arms.go` is empty. Suite re-run after restore: `EXIT=0`.

### Vacuity guard

The `CancelTimer` assertion would be worthless if the gateway arm ALSO produced a row
in `s.Timers`, because `cancelAllTimers` (the sibling sweep, already covered) would
satisfy it without the loop under test ever running. `TestEventGatewayArmIsNotATimerRecord`
pins that it does not — an event-gateway timer arm lives only in `s.ArmedEvents`.
Mutation 1 confirms this empirically: with the loop deleted the count drops to **0**,
not to 1.

---

## Item 114 — `cloneState`'s `History` deep copy was load-bearing and untested

**Status:** DONE. ⚠ **The inherited "a `len == cap` fixture cannot catch it" claim is
PARTIALLY FALSE** — corrected below, and the correction is now in the source comment.

**Files changed**

- `engine/step_clone_history_test.go` — **new**, `package engine_test`:
  `TestCloneStateHistoryIsIndependentlyAllocated` (2 capacity profiles),
  `cloneHistoryDef`, `snapshotHistory`.
- `engine/step_state.go` — a "do not simplify this" comment on the `s.History =
  append([]NodeVisit(nil), st.History...)` line. **No behavioural change.**

The fixture is `start → workA[UserTask] → workB[UserTask] → end`. Completing `workA`
makes one Step do BOTH things that can leak: it **closes** the pre-existing `workA`
visit in place and **appends** a new `workB` visit. Two Steps are driven from one base
four hours apart, so a leaked write shows up as a wrong timestamp rather than a
coincidentally-equal one.

Four assertions: (1) the caller's own base `History` is untouched, (2) the first
result is unchanged after the second Step runs, (3) the two results' backing arrays
are distinct, (4) the clone's array is distinct from the base's.

### Mandatory mutation proof

`engine/step_state.go` `cp`-backed-up, the deep copy replaced by `s.History =
st.History`, then `go test -count=1 -run '^TestCloneStateHistoryIsIndependentlyAllocated$' -v ./engine/`
→ **`EXIT=1`**, 5 failed assertions across the two profiles:

| capacity profile | assertions RED |
|---|---|
| `cap > len` (spare capacity) | **4 of 4** |
| `cap == len` (no spare capacity) | **1 of 4** — only "Step must not write through to the caller's History" |

Representative observed RED text (the cross-Step append collision, `cap > len`):

```
step_clone_history_test.go:145:
    Error:  Not equal:
    expected: …NodeVisit{NodeID:"workB", …, EnteredAt:2026-08-20 10:00:00 +0000 UTC, …}
    actual  : …NodeVisit{NodeID:"workB", …, EnteredAt:2026-08-20 14:00:00 +0000 UTC, …}
    Messages: the second Step wrote into the first result's History backing array
```

and the in-place close leaking to the caller (fires in BOTH profiles):

```
step_clone_history_test.go:141:
    expected: …NodeVisit{NodeID:"workA", …, LeftAt:<nil>, TaskID:"i1-h1"}
    actual  : …NodeVisit{NodeID:"workA", …, LeftAt:2026-08-20 10:00:00 +0000 UTC, TaskID:"i1-h1"}
    Messages: Step must not write through to the caller's History
```

`step_state.go` restored by `cp`, `diff` byte-exact, `git diff --stat` clean before the
comment was added. Suite after restore: `EXIT=0`.

### ⚠ Correction to the inherited claim

The brief (inheriting the triage) states that **a `len == cap` fixture does NOT
reproduce it**. Executed here, that is true of the *corruption the item is named for*
and false as a blanket statement:

- **True:** the **cross-Step append collision** — two Steps from one base overwriting
  each other's newly appended visit — genuinely requires `cap > len`. On a full slice
  `append` reallocates, so the two Steps never share a slot. This is the failure mode
  that matters for the O(entire-state) backlog item, because a cached/reused base state
  is exactly where spare capacity arises.
- **False as worded:** a `len == cap` fixture DOES detect the shallow assignment —
  through a *different* leak, the in-place close of a **pre-existing open visit**
  writing through to the caller's own `History`. That requires the base to carry an
  open visit, which the pre-existing `History` fixtures did not.

So the honest statement — the one now written on the line — is: *a `len == cap`
fixture cannot catch the cross-Step corruption*; it can catch write-through only if
the fixture also carries an open visit. Both profiles are kept in the table so the
distinction is re-derivable rather than asserted.

**Not done (correctly out of scope):** backlog 73's performance fix. This item only
builds the guard that must land first.

---

## Item 40 — `engine.NewActionFailed` gave a consumer a ZERO retry backoff

**Status:** DONE.

**Files changed**

- `engine/step_triggers.go` — `handleActionFailed`: the unconditional
  `delay := time.Duration(t.JitterFraction * float64(eff.Backoff(attempt)))` becomes
  `delay := eff.Backoff(attempt)` scaled by the fraction only when
  `t.JitterFraction > 0`.
- `engine/trigger.go` — the `ActionFailed.JitterFraction` godoc. It claimed *"Zero
  means no jitter"*, which was **false**: zero meant zero *delay*. It now says zero
  means the policy's full backoff interval, and says so as the contract the code
  implements.
- `engine/step_compensation.go` — a **second, stale** claim corrected in the same
  bundle (see below).
- `engine/step_retry_backoff_test.go` — **new**, `package engine_test`:
  `TestActionFailedRetryBackoffDefaultsToFullInterval` (3 cases) + `retryBackoffDef`.

The runtime path is untouched: `runtime/processdriver_action.go` passes
`engine.WithJitter(driver.jitter.Fraction())` (full-jitter, correct by design) and was
never exposed. The defect only ever hit consumers driving the engine library directly
— which is the product.

**Observed RED** (`EXIT=1`), 2 of 3 cases, the `WithJitter(0.5)` control green:

```
--- FAIL: TestActionFailedRetryBackoffDefaultsToFullInterval/no_jitter_yields_the_full_backoff_interval
    Error:  Not equal:
      expected: schedule.TriggerSpec{kind:0x1, dur:1000000000, …}
      actual  : schedule.TriggerSpec{kind:0x1, dur:0, …}
    Messages: retry backoff delay
--- FAIL: TestActionFailedRetryBackoffDefaultsToFullInterval/a_sampled_fraction_of_exactly_zero_yields_the_full_backoff
    (same: dur 0, want 1000000000)
```

**Observed GREEN:** `EXIT=0`.

**Behaviour delta worth carrying into the commit message:** a runtime jitter source
that samples exactly `0.0` now produces a full backoff rather than an immediate retry.
Strictly safer, and pinned by the third table case so it is a decision rather than an
accident.

### Second stale claim, found while fixing this (not in the filed item)

`engine/step_compensation.go` (the compensation retry backoff) carried:

> ⚠ Deliberately NOT the token path's `JitterFraction * Backoff(attempt)` formula.
> `ActionFailed.JitterFraction` defaults to ZERO, so that expression yields a zero
> delay unless the runtime samples a fraction …

The triage cited this as the *correct* reading — and it was, until this fix changed the
very formula it describes. Left alone it would have become a third false comment on the
same subject. Narrowed to what remains true: the compensation path applies **no jitter
at all** (a walk is serialized one-at-a-time per instance, so there is no retry storm to
spread), and the historical note is now explicitly marked as history rather than
current behaviour.

---

## Item 3b — the cancel path flipped `s.Timers` from nil to an empty non-nil slice

**Status:** DONE.

**Files changed**

- `engine/state_timers.go` — `removeTimer` and `cancelTimersWhere` now set
  `s.Timers = nil` when nothing survives, mirroring the invariant
  `cancelCompensationWalkTimers` already states verbatim.
- `engine/state_timers_test.go` — three cases folded into the two existing tables
  (`TestCancelTimersByTaskID`, `TestRemoveTimer`) rather than new test functions, per
  the `table-test` skill. `TestRemoveTimer`'s hard-coded fixture gained a per-case
  override so a case can empty (or start from) a nil table.

`cancelTimersByTaskID` and `cancelTimersForToken` both delegate to
`cancelTimersWhere`, so the single fix covers both.

**Observed RED** (`EXIT=1`), all three new cases:

```
--- FAIL: TestCancelTimersByTaskID/sweeping_the_last_record_leaves_Timers_nil,_not_empty
    Error:      Expected nil, but got: []engine.timerRecord{}
    Messages:   an emptied timer table must be nil so the snapshot keeps persisting null
--- FAIL: TestRemoveTimer/removing_the_last_record_leaves_Timers_nil,_not_empty
    Error:      Expected nil, but got: []engine.timerRecord{}
--- FAIL: TestRemoveTimer/a_miss_against_a_nil_table_leaves_it_nil
    Error:      Expected nil, but got: []engine.timerRecord{}
    Messages:   removing nothing must not materialise a timer table
```

**Observed GREEN:** `EXIT=0`.

**Beyond the filed item:** the triage describes the drift as happening when the sweep
EMPTIES the table. `removeTimer` was worse than that — a call that removed **nothing at
all** (an id that matches no record, against an already-nil table) still materialised an
empty slice, because `make([]timerRecord, 0, 0)` is non-nil. The third case pins that
no-op path specifically.

---

## Item 10 — `compensationRecordsForScope` conflates a closed scope with no records

**Status:** DONE (signature landed) — ⚠ **and the filed HARM was REFUTED by execution.**

**Files changed**

- `engine/step_compensation.go` — `compensationRecordsForScope` now returns
  `([]CompensationRecord, bool)`; the three ignore-`ok` call sites
  (`cursorRecords`, `beginCompensation`, `retainedRecordPrefix`) each carry a
  one-line reason for ignoring it.
- `engine/step_nodes.go` — the compensation-throw call site takes `records, _` with
  the executed reason recorded on the line.
- `engine/step_compensation_closed_scope_test.go` — **new**, `package engine`:
  `TestCompensationRecordsForScopeReportsClosedScope` (5 cases) and
  `TestCompensationThrowInClosedScopeIsRefusedUpstream`.

**Observed RED:** compile error, the valid form for a signature change —

```
engine/step_compensation_closed_scope_test.go:138:19: assignment mismatch: 2 variables
  but compensationRecordsForScope returns 1 value
```

**Observed GREEN:** `EXIT=0`.

### ⚠ Refutation — the cited consumer is UNREACHABLE

The triage flagged reachability as `ASSUMPTION (unverified)`. I ran it. Driving a
token with `ScopeID: "gone"` into the compensation throw does **not** auto-advance
silently; the Step fails first:

```
--- FAIL: TestCompensationThrowInClosedScopeWarns
    Error: Received unexpected error:
           workflow-engine: defForScope: unknown scope "gone"
```

`drive()` resolves `defForScope(def, s, tok.ScopeID)` for **every** active token
*before* dispatching to any node strategy (`engine/step.go`, the first statement of
the loop body), and `defForScope` returns a typed error for any scope absent from
`s.Scopes`. So the compensation-throw branch the item is built on **cannot be
entered** by a token in a closed scope. The item's "auto-advances silently,
indistinguishable from a scope that genuinely had nothing to compensate" is false at
that site.

**Consequence for the fix.** I had written the prescribed WARN at the throw site and
then deleted it: it is provably unreachable, and shipping an untestable branch is
worse than shipping nothing. What landed is the signature change (real, cheap, and
pinned by five direct cases) plus a **regression pin on the upstream gate** — if
`defForScope` is ever relaxed so a closed-scope token parks instead of erroring, that
test goes red and the conflation becomes live and must then be handled.

**Not investigated (out of this item's scope, worth filing):** `cursorRecords` and
`retainedRecordPrefix` read a scope id from the compensation CURSOR, not from a
token, so they are not gated by `drive`'s `defForScope`. Whether a cursor can outlive
its scope is a separate question I did not execute.

**Count correction:** the triage enumerates the conflation at three sites
(`step_nodes.go`, `retainedRecordPrefix`, `beginCompensation`). There are **four**
callers — it omits `cursorRecords` (`engine/step_compensation.go`), which is the one
site above that is *not* gated by `defForScope`.

---

## Item 15 — nested ESP arm retirement is uncovered

**Status:** DONE. Both ablations now produce RED. ⚠ **My first version of test (a) was
VACUOUS and the mutation caught it** — detailed below, because that is the finding.

**Files changed**

- `engine/step_nested_esp_exit_test.go` — **new**, `package engine_test`:
  `TestNestedEventSubprocessExitRetiresEnclosingScopeArms`,
  `TestNestedEventSubprocessRootExitBlockedByLiveCompensationWalk`,
  `nestedESPNonRootGrandparentDef`.
- `engine/export_test.go` — `SetActiveCompensationCmdID` seam. `compensationCursor`
  is unexported, so a black-box test cannot express "a walk is outstanding"; and the
  guard is a claim about the cursor's state, not about how it got there. (No
  production path reaches this exit with a live walk — which is exactly why the
  conjunct measured as uncovered.)

**No production file was changed.** Both were already correct; only the coverage was
missing.

⚠ The backlog's line citation had indeed rotted, as the triage warned. Located by
symbol (`exitNestedEventSubprocessScope`), and by the end of this session the
*triage's* own line numbers had drifted too, from my earlier edits.

### ⚠ Mutation A caught a vacuous test — the actual lesson here

First version of test (a): three scope levels, sibling ESP timer arm on the enclosing
scope, assert a `CancelTimer` for it after the nested exit. It passed. Then the
prescribed ablation — arm-retirement tail replaced by `_ = parentScopeID`:

```
MUTATION A EXIT=0
```

**Green. The test could not fail.** The reason: with `mid` as the only root branch,
the same `Step` that exited the event sub-process also drained `mid`, reached
`root-end` and **completed the instance** — and `endInstance`'s own
`cancelAllScheduledWork` sweeps event-sub-process arms across all scopes. The
`CancelTimer` was real, but it came from the terminal sweep, not from the line under
test. The fixture reproduced the *observation* without exercising the *mechanism*.

Fix: a parallel `fork` at root with a `hold` user task that stays parked, so the
instance survives the exit and `endInstance` never runs. Plus a **vacuity guard**
inside the test — `require.Equal(engine.StatusRunning, r4.State.Status)` — so if
anyone later makes the instance complete here again, the test says so instead of
quietly going hollow. Re-run:

```
MUTATION A EXIT=1
--- FAIL: TestNestedEventSubprocessExitRetiresEnclosingScopeArms (0.00s)
    Error:    []string{} does not contain "i-nested-esp-nonroot-tm1"
    Messages: the enclosing scope's surviving event-sub-process arm must be retired when that scope closes
```

**Mutation B** — delete `&& c.s.Compensating.ActiveCmdID == ""` from the
root-completion branch. ⚠ That exact line appears **three** times in
`engine/step_nodes.go` (`exitRootScope`, `exitRootEventSubprocessScope`,
`exitNestedEventSubprocessScope`); only the third is this item's. Targeted by position
inside the function:

```
MUTATION B EXIT=1
--- FAIL: TestNestedEventSubprocessRootExitBlockedByLiveCompensationWalk (0.00s)
    Error:    Should not be: 1
    Messages: an outstanding compensation walk must block completion at the nested event-sub-process exit
    Error:    Should be false
    Messages: no CompleteInstance may be emitted while a walk is outstanding
```

`engine/step_nodes.go` was `cp`-backed-up and restored byte-exact after each mutation;
`diff` clean both times, suite `EXIT=0` after restore.

`exitNestedEventSubprocessScope` coverage: **87.0 %** (the residual is the defensive
already-closed / still-working early returns, not the two ablated statements).

**Generalisable lesson.** A coverage test whose observable is emitted by *more than
one* code path proves nothing until the other paths are excluded. Here the terminal
sweep and the arm-retirement tail emit the identical `CancelTimer`. Only the mutation
distinguished them — reading the test would not have.

---

## Item 9 — ADR-0164's "eight terminal sites" is stale

**Status:** DONE, count re-derived independently and the invariant made machine-checked.

**Re-derived from source, not inherited** (2026-08-20):

```
grep -rn "endInstance(" engine/*.go | grep -v _test | grep -v 'func (s \*InstanceState) endInstance'  →  10
```

10 call sites: `step_compensation.go` ×2, `step_errors.go` ×1, `step_nodes.go` ×5,
`step_triggers.go` ×2. The triage's figure of 10 is confirmed; ADR-0164's "eight" is
stale. (The triage's *line numbers* had already drifted from my own edits this
session — which is the same rot, one level down.)

Cross-check, also re-derived: `endInstance` is the only writer of a terminal status.
Every other `s.Status =` in the non-test core writes `StatusRunning` or
`StatusCompensating`, neither of which `Status.IsTerminal()` accepts. So the call-site
count *is* the terminal-site count.

**Files changed**

- `engine/terminal_sites_test.go` — **new**, `package engine_test`:
  `TestEndInstanceIsTheSoleTerminalStatusWriter` (the `go/ast` pin) plus
  `TestTerminalStatusWriterDetectorFires` (its liveness guard) and
  `terminalStatusAssignments`.
- `engine/state_arms.go` — "all eight terminal sites" replaced by the property, with
  an explicit instruction not to restate it as a count.
- `docs/adr/0164-terminal-transitions-are-one-path.md` — a dated `⚠ CORRECTION`
  blockquote after the Decision's "All eight sites route through it." The historical
  counts in Context are deliberately LEFT as-is and marked as such: an ADR is a record
  of what was decided, and refreshing its numbers would just restart the rot.

**Stating the count vs stating the invariant.** The brief asked for a test that
re-derives the set rather than a number in prose. What is pinned is the *reason* the
statement is true: **`endInstance` is the sole assigner of a terminal `Status`**. That
holds as sites are added, which a count does not.

**Falsifiability — honestly framed.** This is a **PIN, not a RED-green fix**: it
passes the moment it is written because the property already holds. Two things make it
non-vacuous:

1. A liveness guard runs the same matcher over an in-test fixture containing one
   rogue `s.Status = StatusFailed` and one innocent `s.Status = StatusRunning`,
   asserting 1 and 0 — so a detector that reports nothing ever cannot pass.
2. **Mutation-verified against real files.** Planting `s.Status = StatusFailed` into
   `removeTimer` (`cp` backup, restored byte-exact) produced:

   ```
   --- FAIL: TestEndInstanceIsTheSoleTerminalStatusWriter (0.01s)
       terminal_sites_test.go:67: state_timers.go:58: removeTimer assigns a terminal
       Status directly; every terminal transition must route through endInstance (ADR-0164)
   ```

**Stated scope limit** (in the test's own doc, not glossed): the matcher resolves
terminal-status *constants* by name. `endInstance`'s own `s.Status = status` assigns a
parameter, which no AST-only check can resolve — which is precisely why `endInstance`
must stay the single choke point.

---

## Item 31 (engine half) — dangling `§N` ADR-section citations

**Status:** DONE for `engine/`. `scheduler/` left to its owner, as briefed.

**⚠ The triage's count of "three dangling citations" is an UNDERCOUNT.** A repo-wide
`grep -rnE "ADR-[0-9]{4} §[0-9.]+" --include='*.go'` returns **14** section citations
across `engine`, `scheduler`, `runtime`, `definition` and `persistence`. The triage
checked three. In `engine/` there are **two**, not one — it missed
`engine/step_terminal_dispatch_test.go`.

**Files changed** (each replacement heading verified to exist in the target document
before writing it):

- `engine/state_compensation.go` — *"ADR-0174 §5.3's bound"* **dangled twice over**:
  ADR-0174 has no numbered sections at all (`## Context`, three `###` subsections,
  `## Decision`, `## Consequences`), *and* the bound is not in the ADR — ADR-0174's own
  correction blockquote says it "is now stated as an explicit bound in the spec's
  §5.3.1". Replaced with the heading text `### 5.3.1 BOUND: a pre-ADR-0171 unpinned
  cursor keeps ADR-0173's accepted double-run`, verified present in
  `docs/specs/2026-08-11-dying-instance-harvests-open-scopes.md`, plus a pointer to
  ADR-0174's Consequences correction.
- `engine/step_terminal_dispatch_test.go` — *"ADR-0165 §5.2"*. ADR-0165 **does**
  number its Decision subsections 1–6, so this one is subtler: there is no §5.2, and §5
  ("The payload-dependent carve-out is absorbed by the mechanism") is not what the test
  covers. Replaced with `Decision 2 ("Enforcement is a single check in dispatch")` read
  against `Decision 4 ("The classification")` — both verified present.

**Not done, deliberately:** the triage's suggested `scripts/` citation checker. It
would live outside `engine/`, and this brief scopes me to `engine/` files. It is worth
building — 14 citations, 2 confirmed dangling in one package alone, and the same rot
family as item 9 and backlog 1.

---

## `engine/state_arms.go` de-duplication comment (follow-up from the controller)

**Status:** DONE.

The comment justified the first-wins de-dup rule with *"model.Validate accepts
duplicate node ids, two flows between one pair, and duplicate flow ids"*. Item 115
shipped `ErrDuplicateNodeID`/`ErrDuplicateFlowID`, so two thirds of that enumeration
became false.

**Executed rather than restated** (throwaway probe, deleted; `model.Validate` on four
hand-built definitions):

| shape | `model.Validate` |
|---|---|
| two flows between one node pair | `<nil>` — **accepted** |
| duplicate node ids | `workflow-definition: duplicate node id: node "end"` |
| duplicate flow ids | `workflow-definition: duplicate flow id: flow "dup"` |
| three blank flow ids | `<nil>` — **accepted** (blank ids are exempt) |

All four match what the controller reported, now first-hand.

**Rewritten to resist rot rather than re-enumerated.** Per the controller's own note
that this is the same failure mode as item 9, the comment no longer *rests* on what
`model.Validate` permits — because that list has rotted once and will again, and
because it is not the load-bearing reason. The reason is that **`model.Validate` is not
on this path at all**: its only non-test caller is `definitionCore.build()`, so a
struct-literal definition reaches the arm layer unvalidated, and the de-dup must hold
for definitions no validator ever saw. The surviving accepted shape is kept as a dated,
executed footnote rather than as the justification.

**Not touched:** the de-duplication code itself, per the explicit instruction.

---

## Closing verification

```
go test -count=1 ./engine/... > /tmp/eng-final.log 2>&1; echo "EXIT=$?"   →  EXIT=0
go build ./...                > /tmp/build.log     2>&1; echo "EXIT=$?"   →  EXIT=0
golangci-lint run ./engine/...                                            →  EXIT=0, 0 issues
```

`golangci-lint` was probed on `PATH` (`/opt/homebrew/bin/golangci-lint`) and run
without asking, per the standing permission. ⚠ It was run **package-scoped**
(`./engine/...`), which is a partial result — the repo-wide `golangci-lint run ./...`
and the repo-wide `go test ./...` are the controller's to run, since other agents held
uncommitted work in other packages throughout this session.

`engine` coverage: **93.0 % → 93.1 %**, with every symbol this delivery touched at
100 % except `exitNestedEventSubprocessScope` (87.0 %, residual is defensive early
returns):

| symbol | after |
|---|---|
| `cancelAllArmsAndBoundaries` | 100.0 % |
| `forceTerminate` | 100.0 % |
| `removeTimer` | 100.0 % |
| `cancelTimersWhere` | 100.0 % |
| `compensationRecordsForScope` | 100.0 % |
| `exitNestedEventSubprocessScope` | 87.0 % |

### Files changed, complete list

Production: `engine/step_gateways.go`, `engine/step_triggers.go`,
`engine/trigger.go`, `engine/step_compensation.go`, `engine/step_nodes.go`,
`engine/state_timers.go`, `engine/state_arms.go`, `engine/state_compensation.go`,
`engine/step_state.go` (comment only).

Tests: `engine/purity_test.go`, `engine/state_timers_test.go`,
`engine/export_test.go`, `engine/step_terminal_dispatch_test.go` (comment only), and
seven new files — `step_gateway_identity_test.go`, `step_terminal_arms_test.go`,
`step_clone_history_test.go`, `step_retry_backoff_test.go`,
`step_compensation_closed_scope_test.go`, `terminal_sites_test.go`,
`step_nested_esp_exit_test.go`.

Docs: `docs/adr/0164-terminal-transitions-are-one-path.md`.
(`docs/adr/0082-sqlite-backend.md` is modified in this tree by ANOTHER agent — not
mine, not touched.)

### Backlog statements found FALSE (consolidated)

1. **Blocker 8** — "entirely uncovered" is false. `engine` 93.0 %, `endInstance` and
   `cancelAllScheduledWork` 100 %, `forceTerminate` 90 %,
   `cancelAllArmsAndBoundaries` 80 %; exactly three zero-count blocks. Also, within
   the loop the `Boundaries` half was already covered — only `ArmedEvents` was cold.
2. **Item 114** — "a `len == cap` fixture does NOT reproduce it" is false as worded.
   It cannot reproduce the *cross-Step append collision* (true, and that is the
   corruption that matters), but it DOES catch the shallow assignment via
   write-through of an in-place visit close.
3. **Item 10** — the filed harm is REFUTED. `drive()` runs `defForScope` before any
   strategy and hard-errors on a closed scope, so the compensation-throw branch is
   unreachable. Also a count error: **four** callers, not three (`cursorRecords`
   omitted — and it is the one NOT gated by `defForScope`).
4. **Item 9** — ADR-0164's "eight terminal sites" is stale; **10** today, re-derived
   independently.
5. **Item 31** — "three dangling citations" is an undercount: **14** `§N` citations
   repo-wide in `.go` files, **two** in `engine/` (the triage found one).
6. **Item 3b** — understated. `removeTimer` materialised an empty slice even on a call
   that removed **nothing at all**, not only when the sweep emptied the table.
7. **Item 40** — confirmed, plus a knock-on: `engine/step_compensation.go` cited the
   old formula as the *correct* reading and became false when the formula changed.
8. **Item 15** — the line citation had rotted (as the triage warned), and my own first
   test for it was vacuous until the prescribed mutation exposed it.
9. **`state_arms.go` de-dup comment** — two of its three enumerated `model.Validate`
   permissions are now false; executed and confirmed, including the blank-id exemption.

### Not done / out of scope

- **Backlog 73's performance fix** — deliberately not attempted; item 114 only builds
  the guard that must land first.
- **The `scripts/` ADR-citation checker** (item 31's suggestion) — outside `engine/`.
- **`scheduler/`'s two `§N` citations** — another agent's.
- **`cursorRecords` / `retainedRecordPrefix` closed-scope reachability** — not gated
  by `defForScope`; not executed. Worth filing.
- **`closeScope`'s nil→empty `s.Scopes` drift** — noticed while working item 10.
  `closeScope` does `out := make([]Scope, 0, len(s.Scopes))` then assigns
  unconditionally, exactly the shape item 3b fixes for `s.Timers`, and `endInstance`
  nils `s.Scopes` explicitly, so the same invariant is intended. **Not fixed** — 3b
  was scoped to timers. Recommend filing as 3b's sibling.
