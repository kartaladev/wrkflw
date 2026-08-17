# Recon B — retry/incident for a FAILED compensation action (backlog 16 + 3g)

Worktree `/Users/zakyalvan/Documents/RND/wrkflw/.claude/worktrees/agent-a40a789b48af9a28b`
HEAD `12c9d7e3 docs: codify that docs/specs holds a delivery's evidence records too`, `git status --porcelain` EMPTY. **STEP 0 OK.**

Everything below marked EXECUTED was run in this worktree with output pasted. Everything
else is labelled `ASSUMPTION (unverified):`.

---

## 0. Two corrections to the brief itself (EXECUTED)

**(a) `engine/failing_action.go` is NOT a failing-action test helper.** It is 30 lines and
holds ONE symbol:

```go
// engine/failing_action.go:16
func FailingActionName(def *model.ProcessDefinition, st InstanceState, commandID string) (string, *model.ProcessDefinition, bool)
```

A LOOKUP resolving which action name a token awaiting `commandID` is parked on, so the
runtime can surface a per-action retry policy via `StepOptions.OverrideRetryPolicy`. It
cannot make an action fail. The way to make a compensation action fail in `engine` is to
feed `engine.NewActionFailed(at, cmdID, err, retryable)` — the engine core has no action
executor at all, it only emits `InvokeAction` commands.

**(b) ADR-0034 `§` count — EXECUTED, confirms the known-false citation.**

```
$ grep -c "§" docs/adr/0034-compensation-on-error-cancel.md
0
```

`engine/step_triggers.go:291` cites `ADR-0034 §2.5`. ADR-0034 has zero `§` characters and
no numbered sections. Backlog item 1 in `HANDOVER.md` is CORRECT.

---

## 1. The compensation dispatch machinery

### The record type — `engine/state_compensation.go:24-34`

```go
type CompensationRecord struct {
	NodeID      string         // BPMN node ID of the completed compensable activity
	Action      string         // name of the compensating service action
	CompletedAt time.Time      // from Trigger.OccurredAt, never time.Now()
	Input       map[string]any // instance-variable snapshot at invocation time
}
```

Record homes, in order of the walk's preference: `Scope.Compensations`
(`state_compensation.go:57`), `InstanceState.RootCompensations`,
`InstanceState.ArchivedCompensations[key]`, and — since ADR-0171 — the cursor's own pinned
`Records` slice.

### The walk state — `engine/state_compensation.go:69-197`

`compensationCursor`. Fields load-bearing for this design:

| field | line | meaning |
|---|---|---|
| `ArchiveKey` | 81 | non-empty ⇒ TARGETED throw; selects `ArchivedCompensations[key]` as source |
| `Records` | 103 | ADR-0171 pinned snapshot; set **only** by `startCompensationWalk` (throw walks) |
| `NextIndex` | 171 | index of the record currently IN FLIGHT; counts DOWN |
| `StartedAt` | 178 | stamped ONCE at walk creation, survives every advance (ADR-0175 `compensating_since`) |
| `ActiveCmdID` | 181 | CommandID of the compensation `InvokeAction` in flight |
| `FinalStatus` / `FinalErr` | 191/196 | ADR-0034 Decision 1's terminal-outcome parametrization |
| `TeardownArchiveKey/Offset/Count` | 165-167 | ADR-0173 teardown window |

`walkMode()` (`state_compensation.go:235`) derives five modes: `walkAdmin`,
`walkThrowTargeted`, `walkThrowScopeWide`, `walkPartial`, `walkReverse`.

### Where ownership "transfers on dispatch" — `engine/state_compensation.go:573-588`

```go
func (s *InstanceState) consumeDispatchedRecord(idx int) {
	cur := s.Compensating
	if len(cur.Records) == 0 {            // ← unpinned cursor: EARLY RETURN, no-op
		return
	}
	if cur.ArchiveKey != "" {             // ← TARGETED throw: drop from its archive slot
		s.dropArchiveRecordAt(cur.ArchiveKey, idx)
		return
	}
	if cur.TeardownArchiveKey == "" || idx >= cur.TeardownArchiveCount {
		return                            // ← no teardown window: EARLY RETURN, no-op
	}
	s.dropArchiveRecordAt(cur.TeardownArchiveKey, cur.TeardownArchiveOffset+idx)
	cur.TeardownArchiveCount = idx
	s.Compensating = cur
}
```

**This is the crux.** Ownership transfers on dispatch for exactly TWO of the five walk
modes' sources: a **targeted** throw (`ArchiveKey != ""`) and a walk whose scope was **torn
down mid-flight** (`TeardownArchiveKey != ""`). On every other walk it is a no-op — measured
in §3 below.

The engine's own comment agrees, at `engine/step_compensation.go:1321-1326`
(`abandonCompensationWalk`):

> *"On a beginCompensation walk the cursor is UNPINNED, so consumeDispatchedRecord
> early-returns and RootCompensations still holds every record — run or not."*

---

## 2. RE-COUNT of the dispatch sites

### 2a. Compensation `InvokeAction` DISPATCH sites — **the count is FOUR (4)**

Derived by `grep -rn "compensationInvoke" --include='*.go' .` and discarding `_test.go`
and the definition itself:

| # | file:line | enclosing func (decl line) | which dispatch | calls `consumeDispatchedRecord`? |
|---|---|---|---|---|
| 1 | `engine/step_compensation.go:412` | `beginCompensation` (306) | FIRST dispatch of an admin / cancel / error / partial / reverse walk | **NO** |
| 2 | `engine/step_compensation.go:574` | `stepCompensationAdvance` (514) | EVERY subsequent dispatch of ANY walk | YES (line 573) |
| 3 | `engine/step_compensation.go:1301` | `retryStalledCompensation` (1273) | ADR-0175 operator `retry` verb — RE-dispatch of the SAME record | **NO, deliberately** (comment 1298-1300) |
| 4 | `engine/step_nodes.go:1134` | `startCompensationWalk` (1117) | FIRST dispatch of a compensation-THROW walk | YES (line 1132) |

**⚠ FALSE ENUMERATION IN SHIPPED CODE.** `engine/step_nodes.go:1135-1136` says:

> `// This is the throw walk's FIRST dispatch and therefore the third of the`
> `// three compensation dispatch sites (ADR-0175). Arming only the other two`

There are **four**, not three — ADR-0175 itself added site 3 (`retryStalledCompensation`),
and this comment was written in the same delivery. The comment is also false under the
narrower reading "the three sites that arm a stall timer": `armCompensationStallTimer` is
called at **four** sites — `step_compensation.go:415`, `:577`, `:1302`, and
`step_nodes.go:1139` (EXECUTED: `grep -n "armCompensationStallTimer" engine/*.go` excluding
tests gives 4 call sites + 2 decl/doc lines). This is the same class of defect the ADR-0175
counting lens caught, re-introduced by the fix.

### 2b. Callers of the two walk-START functions

`beginCompensation` — **4** call sites:
- `engine/step_errors.go:268` — terminal unhandled error (`FinalStatus: StatusFailed`)
- `engine/step_triggers.go:232` — `CancelRequested` (`FinalStatus: StatusTerminated`, `FinalErr: "cancelled"`)
- `engine/step_compensation.go:235` — `stepCompensateRequested` (admin / partial / reverse)
- `engine/step_compensation.go:1090` — a deferred pending cancel/error discharged at a finish

`startCompensationWalk` — **2** call sites, both in `compensationThrowEventStrategy.enter`:
- `engine/step_nodes.go:1174`
- `engine/step_nodes.go:1222`

### 2c. Compensation-action REPLY handling — **3 routes into `stepCompensationAdvance`**

`grep -rn "stepCompensationAdvance(" --include='*.go' engine/ | grep -v _test`:

| # | file:line | trigger | guard |
|---|---|---|---|
| 1 | `engine/step_triggers.go:85` | `ActionCompleted` | `s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID` |
| 2 | `engine/step_triggers.go:293` | `ActionFailed` | same guard |
| 3 | `engine/step_compensation.go:1262` | `ResolveCompensationStall{CompensationSkip}` | cursor + incident checks at 1229-1254 |

Plus **2** further reply handlers on the walk that do NOT advance:
- `engine/step_compensation.go:1258` → `retryStalledCompensation` (`CompensationRetry`)
- `engine/step_compensation.go:1264` → `abandonCompensationWalk` (`CompensationAbandon`)

So **5 total** operations can move a walk; **3** of them funnel through
`stepCompensationAdvance`.

### 2d. `ErrTokenNotFound` on a compensation-action-reply path — **2 sites**

`grep -rn "ErrTokenNotFound" --include='*.go' engine/ | grep -v _test` gives 9 `return`
sites. Filtering to those a *compensation action reply* (`ActionCompleted` / `ActionFailed`)
can reach:

| # | file:line | handler |
|---|---|---|
| 1 | `engine/step_triggers.go:94` | `handleActionCompleted`, reached only after the compensation guard at :84 fails |
| 2 | `engine/step_triggers.go:302` | `handleActionFailed`, reached only after the compensation guard at :292 fails |

The other 7 (`:422 :453 :475 :679 :692` = human-task ids; `:999 :1034` = sub-instance ids)
are on different trigger types and cannot carry a compensation command id.

Sentinel definition — `engine/errors.go:23`:
```go
ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)
```

---

## 3. EXECUTED — the core claim

Probe: `engine/zz_recon_probe_test.go` (`package engine_test`, deleted after the run; full
source in §7). Command and exit code:

```
$ go test -count=1 -run '^TestReconProbe' -v ./engine/... > /tmp/reconprobe.log 2>&1; echo "EXIT=$?"
EXIT=0
```
All subtests confirmed RUN via `-v` (`=== RUN` lines present for each).

### Scenario A — CANCEL-started walk (`beginCompensation`, UNPINNED cursor)

Fixture `threeCompensableDef()` (`engine/step_compensation_test.go:463`):
`start → step1(c1) → step2(c2) → step3(c3) → userTask → end`.
Walk dispatches `c3`, then `c2`, then `c1`.

**FAIL variant — `c3` returns `ActionFailed{"boom: refund gateway down", retryable:true}`:**

```
=== A-cancelWalk-FAIL-c3 ===
  [after cancel (walk starts)] status=compensating cmds=[engine.UpdateTask c3/three-comp-inst-c4]
  [after cancel (walk starts)] RootCompensations=[step1:c1 step2:c2 step3:c3] ArchivedCompensations=map[]
  [after cancel (walk starts)] cursor={... Records:[] ... NextIndex:2 ActiveCmdID:three-comp-inst-c4 FinalStatus:terminated FinalErr:cancelled}
  [after cancel (walk starts)] incidents=NONE
  [after cancel (walk starts)] tokens=0 endedAt=<nil>
  [after c3 outcome] status=compensating cmds=[c2/three-comp-inst-c5]
  [after c3 outcome] RootCompensations=[step1:c1 step2:c2 step3:c3] ArchivedCompensations=map[]
  [after c3 outcome] cursor={... Records:[] ... NextIndex:1 ActiveCmdID:three-comp-inst-c5 ...}
  [after c3 outcome] incidents=NONE
  [after c3 outcome] tokens=0 endedAt=<nil>
  [after completing c2] status=compensating cmds=[c1/three-comp-inst-c6]
  [after completing c2] RootCompensations=[step1:c1 step2:c2 step3:c3] ArchivedCompensations=map[]
  [after completing c1] status=terminated cmds=[engine.FailInstance]
  [after completing c1] RootCompensations=[] ArchivedCompensations=map[]
  [after completing c1] incidents=NONE
  [after completing c1] tokens=0 endedAt=2026-06-21 11:03:00 +0000 UTC
  [A-cancelWalk-FAIL-c3] FINAL status=terminated root=0 incidents=0
```

**CONTROL variant — `c3` returns `ActionCompleted`:** the transcript is **byte-identical**
in every field printed (see `/tmp/reconprobe.log` lines 26-49 vs 2-25 — same statuses, same
command ids `c4→c5→c6`, same `RootCompensations`, same `incidents=NONE`, same
`endedAt`). The only difference between a failed and a successful first compensation action
is *nothing observable in the state*.

### Scenario B — scope-wide THROW walk (`startCompensationWalk`, PINNED cursor)

Fixture `rootSagaWithScopeWideThrow()` (`engine/compensation_throw_test.go:78`) — the
`undoA` / `undoB` fixture the handover's wording refers to.

**FAIL `undoB`:**
```
=== B-throwWalk-FAIL-undoB ===
  [at throw (walk starts)] status=compensating cmds=[undoB/recon-throw-c3]
  [at throw (walk starts)] RootCompensations=[svcA:undoA svcB:undoB] ArchivedCompensations=map[]
  [at throw (walk starts)] cursor={... ArchiveKey: Records:[{svcA undoA} {svcB undoB}] ResumeNode:afterThrow ... StartRecordCount:2 TeardownArchiveKey: ... NextIndex:1 ActiveCmdID:recon-throw-c3 ...}
  [at throw (walk starts)] incidents=NONE
  [after undoB outcome] status=compensating cmds=[undoA/recon-throw-c4]
  [after undoB outcome] RootCompensations=[svcA:undoA svcB:undoB] ArchivedCompensations=map[]
  [after undoB outcome] incidents=NONE
  [after completing undoA] status=running cmds=[engine.AwaitHuman]
  [after completing undoA] RootCompensations=[] ArchivedCompensations=map[]
  [B-throwWalk-FAIL-undoB] FINAL status=running root=0 incidents=0
```
**CONTROL (`undoB` succeeds):** again **byte-identical** (log lines 69-87 vs 50-68).

⚠ Note the pinned `Records:[...]` yet `RootCompensations` UNCHANGED across the dispatch:
`consumeDispatchedRecord` early-returned at its **second** guard (`ArchiveKey == ""` **and**
`TeardownArchiveKey == ""`), not the first.

### Scenario C — does an explicit retry policy change anything? (EXECUTED)

`StepOptions{DefaultRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second}}`,
`c3` fails:

```
=== C: DefaultRetryPolicy{MaxAttempts:5} on a FAILED compensation action ===
  [after c3 FAILED with retry policy in effect] status=compensating cmds=[c2/three-comp-inst-c5]
  [after c3 FAILED with retry policy in effect] incidents=NONE
  re-dispatched c3 again? false
  scheduled a TimerRetry? false
```

The compensation guard at `step_triggers.go:292` returns **before** `effectiveRetryPolicy`
is ever consulted (it lives at `:316`). A retry policy is structurally unreachable on a
compensation action failure.

### Scenario D — the ONE shape where ownership really does transfer (EXECUTED)

Fixture `targetedThrowOnceDef()` (`engine/step_compensation_ownership_test.go:52`), a
TARGETED throw (`ArchiveKey: "sub"`).

```
$ go test -count=1 -run '^TestReconProbeTargetedThrowOwnership$' -v ./engine/... ; echo EXIT=$?
EXIT=0

=== D: targeted-FAIL-undoInner ===
  [targeted walk started] status=compensating cmds=[undoInner/d1-c2]
  [targeted walk started] RootCompensations=[] ArchivedCompensations=map[]      ← ALREADY EMPTY
  [targeted walk started] cursor={... ArchiveKey:sub Records:[{svcInner undoInner}] ResumeNode:end ... NextIndex:0 ActiveCmdID:d1-c2 ...}
  [after undoInner outcome] status=completed cmds=[engine.CompleteInstance]
  [after undoInner outcome] RootCompensations=[] ArchivedCompensations=map[]
  [after undoInner outcome] incidents=NONE
```
CONTROL (succeed) again byte-identical.

`ArchivedCompensations` is **already empty at the walk's first dispatch** — `startCompensationWalk`
→ `consumeDispatchedRecord` → `dropArchiveRecordAt` removed the only record at dispatch time.
So on a targeted walk a failed compensation action is **permanently lost**: the record is
gone from every store, no retry, no incident, and the instance goes on to `completed`.
**This is the real hazard behind backlog 16, and it is narrower than the handover states.**

---

## 4. What the handover got RIGHT and WRONG

`docs/plans/HANDOVER.md:217-221`, verbatim:

> 16. **A retry / incident story for a FAILED compensation action.** Ownership transfers on
>     DISPATCH, so an action returning `ActionFailed` has its record consumed and is never
>     retried, with no incident raised. Measured `re-dispatched=[]` where `main` gave
>     `[undoB undoA]`. ⚠ **Now adjacent to ADR-0175**, which gives compensation an incident
>     kind — but it changes ADR-0034 Decision 4's contract, so it stays its own ADR.

| claim | verdict | evidence |
|---|---|---|
| "is never retried" | **TRUE** | Scenarios A, B, C, D — no re-dispatch on any walk shape, even with `MaxAttempts:5` |
| "with no incident raised" | **TRUE** | `incidents=NONE` in every scenario |
| "changes ADR-0034 Decision 4's contract" | **TRUE** | Decision 4 says *"routes to advance (skip+continue), never back into propagateError/retry"* — see §5 |
| "Now adjacent to ADR-0175 / incident kind" | **TRUE** | `IncidentCompensationStall` at `engine/state.go:128`; `Incident.Kind` at `:166` |
| **"Ownership transfers on DISPATCH, so … has its record consumed"** | ⚠ **FALSE AS A GENERAL CLAIM** | Consumption is a **no-op on 3 of 4 dispatch sites' typical walks**. Measured: on a cancel/error/admin walk (site 1+2, unpinned) `RootCompensations` stayed `[step1:c1 step2:c2 step3:c3]` UNCHANGED through the entire walk; on a scope-wide throw (site 4, pinned but `ArchiveKey==""`) `RootCompensations` stayed `[svcA:undoA svcB:undoB]` UNCHANGED. It is TRUE only for a **targeted** throw (Scenario D: archive empty at first dispatch) or a walk with a live **teardown window**. |
| **"Measured `re-dispatched=[]` where `main` gave `[undoB undoA]`"** | ⚠ **PROVENANCE IS WRONG — that measurement is about ABANDON, not `ActionFailed`** | The string `[undoB undoA]` with that framing appears in `engine/step_compensation.go:1321-1326`, inside `abandonCompensationWalk`, describing what happens if an ABANDONED walk retains its whole record list: *"Retaining the whole list therefore re-dispatches an already-run compensation on the later admin rollback (measured `[undoB undoA]` with undoB already run)"*. That is ADR-0175's abandon-verb measurement. Nothing in my Scenario A/B produced a `re-dispatched` list at all — a failed compensation action does not re-dispatch anything, so `[]` vs `[undoB undoA]` is not the contrast that distinguishes fail from success. |

**Net:** the *conclusion* of backlog 16 (no retry, no incident, a compensation failure is
silently swallowed) is correct and worth an ADR. Its *stated mechanism* and its *cited
measurement* are both wrong, and the design must not be built on them. The correct mechanism
is: **`handleActionFailed` short-circuits to `stepCompensationAdvance` before any retry or
incident machinery is reached** (`engine/step_triggers.go:292-294`) — ADR-0034 Decision 4,
by design. Record loss on top of that is a *targeted-throw-only* aggravation.

### Backlog 3g — CONFIRMED, and it is not compensation-specific

`HANDOVER.md:181-182`: *"A late reply to a superseded compensation command returns
`ErrTokenNotFound` rather than being treated as a benign duplicate."*

**EXECUTED** (§5 probe): after `c3` completes and the walk advances to `c2`, replaying
`c3`'s command id gives, for **both** reply kinds:

```
  c3 dispatched as CommandID="three-comp-inst-c4"
  walk advanced; active command is now c2="three-comp-inst-c5"
  [late ActionCompleted for superseded c3] err=workflow-engine: no token awaiting command: workflow-engine: invalid state transition: "three-comp-inst-c4"
  [late ActionFailed for superseded c3]    err=workflow-engine: no token awaiting command: workflow-engine: invalid state transition: "three-comp-inst-c4"
```

Mechanism, source-verified: the compensation guard (`:84` / `:292`) requires
`s.Compensating.ActiveCmdID == t.CommandID`. A superseded id fails it, falls through to
`s.tokenAwaiting(t.CommandID)` — and a compensation walk holds **no tokens at all**
(`tokens=0` in every probe frame) — so `tok == nil` and the handler returns
`ErrTokenNotFound` at `:94` / `:302`. The comments at both sites reason only about terminal
instances and never mention the superseded-compensation-command route, which is why it is
unhandled.

⚠ Nuance the handover omits: **`ErrTokenNotFound` wraps `ErrInvalidTransition`**
(`engine/errors.go:23`), and per `engine/step_triggers.go:667` that maps to **HTTP 422**,
not 404. A duplicate delivery from an at-least-once worker therefore surfaces to the caller
as a client error on a perfectly healthy walk.

---

## 5. ADR-0034 Decision 4 — the contract that changes

`docs/adr/0034-compensation-on-error-cancel.md:32-33`, **verbatim**:

> 4. **Best-effort compensation:** an `ActionFailed` matching the cursor's `ActiveCmdID` while
>    `StatusCompensating` routes to advance (skip+continue), never back into `propagateError`/retry.

Two supporting sentences, also verbatim:

- Consequences/Positive, `:45` — *"Best-effort compensation prevents a failing compensation action from stranding the instance."*
- Consequences/Negative, `:51-52` — *"Best-effort means a compensation action's failure is logged/skipped, not retried — a compensation that must succeed needs its own internal retry. (Compensation-action retry policy is future work.)"*
- Deferred, `:59` — *"Compensation-action retry/incident on repeated failure."*

**What a retry/incident story would violate or extend:**

1. **"never back into … retry" is violated outright** by any engine-side retry. It must be
   *extended* with an explicit exception, or Decision 4 must be superseded. Note it says
   "never back into `propagateError`/retry" — i.e. it forbids reusing the *normal* error
   path, which is arguably narrower than forbidding all retry. A new ADR should quote and
   adjudicate this ambiguity rather than assume the loose reading.
2. **"skip+continue" is violated by an incident that PARKS the walk.** ADR-0034's whole
   safety argument (`:45`) is that a failing compensation must not strand the instance.
   ADR-0175 has since built exactly the stranding-with-visibility mechanism
   (`IncidentCompensationStall` + three escape verbs), so the argument is now weaker — but a
   new ADR owes an explicit statement of which walks may park. **Load-bearing asymmetry:**
   `abandonCompensationWalk` is refused on a resuming walk
   (`ErrCompensationWalkResumes`, `engine/step_compensation.go:1313-1320`), so a parked
   *throw* walk would have only `retry` and `skip` as escapes.
3. **"logged/skipped" is FALSE — nothing is logged. EXECUTED:**
   ```
   $ awk 'NR>=514 && NR<=579' engine/step_compensation.go | grep -c slog   # stepCompensationAdvance
   0
   $ awk 'NR>=288 && NR<=408' engine/step_triggers.go | grep -c slog       # handleActionFailed
   0
   ```
   A failed compensation action is skipped **silently** — no `slog` call on either the
   dispatching handler or the advance. ADR-0034 `:51` ("*a compensation action's failure is
   logged/skipped*") is a false claim in a shipped ADR; correct it in the same bundle. This
   also makes the operator-visibility case for backlog 16 stronger than the handover states:
   today there is no incident, no retry, **and no log line**.
4. **The post-acceptance fix (`:61-83`) constrains the design.** `RootCompensations` MUST be
   cleared at a full-rollback finish or a re-delivered `CancelRequested` re-runs the entire
   walk (double-refund). Any "keep the record so we can retry it later" design collides with
   this directly.

ADR-0034 is **Accepted**, dated 2026-06-23, and has **no `§` sections** — cite it as
"Decision 4".

---

## 6. Precedent: how the engine already does retry + incident for a NORMAL action

### The policy chain — `engine/step_state.go:292-310`

```go
func effectiveRetryPolicy(node model.Node, opt StepOptions) (model.RetryPolicy, bool) {
	rp := model.RetryPolicyOf(node)
	switch {
	case opt.OverrideRetryPolicy != nil:
		eff := *opt.OverrideRetryPolicy
		if rp != nil {
			// Inherit the node's safety-only fields the action tier can't express.
			eff.MaxElapsed = rp.MaxElapsed
			eff.NonRetryableErrors = rp.NonRetryableErrors
		}
		return eff.Normalize(), true
	case rp != nil:
		return rp.Normalize(), true
	case opt.DefaultRetryPolicy != nil:
		return opt.DefaultRetryPolicy.Normalize(), true
	default:
		return model.RetryPolicy{}, false
	}
}
```
Three tiers: per-Step override (the action-tier seam `FailingActionName` feeds) → node →
engine default.

### The full ladder in `handleActionFailed` — `engine/step_triggers.go:288-407`

1. `:292` **compensation short-circuit** → `stepCompensationAdvance`. *Everything below is
   unreachable for a compensation action.*
2. `:296-303` token lookup, else `ErrTokenNotFound`.
3. `:316-348` **retry**: `effectiveRetryPolicy` → terminality test
   (`!t.Retryable || eff.IsNonRetryable(t.Err) || attempt+1 >= eff.MaxAttempts || elapsed > eff.MaxElapsed`)
   → `ScheduleTimer{Kind: TimerRetry}` + `timerRecord` + `tok.RetryAttempts++` +
   `tok.RetryStartedAt` + `tok.State = TokenWaiting` + `tok.AwaitCommand = timerID`.
4. `:351-384` exhaustion precedence (1) **catch flow** (`recoveryFlowOf`) — injects
   `_errorMessage` / `_errorAttempts`, routes the token.
5. `:388` (2) **error boundary** then (3) **incident**, via
   `propagateError(..., raiseIncident)`.
6. `:403` no policy at all → `propagateError(..., failFast)`.

### The incident-raising path — `engine/step_errors.go:213-236`

```go
if policy == raiseIncident {
	failingTok := s.tokenByID(failingTokenID)
	attempts, cmdID := 1, ""
	if failingTok != nil {
		attempts = failingTok.RetryAttempts + 1
		cmdID = failingTok.AwaitCommand
		failingTok.State = TokenIncident
	}
	s.Incidents = append(s.Incidents, Incident{
		ID: s.nextIncidentID(), TokenID: failingTokenID, NodeID: originatingNodeID,
		ScopeID: scopeID, CommandID: cmdID, Error: errorCode, Attempts: attempts, CreatedAt: at,
	})
	return nil, nil
}
```
(`Kind` is left zero ⇒ `IncidentAction`, `engine/state.go:127`.)

### How closely can a compensation design mirror this? — the honest answer

**It can mirror the SHAPE closely, but NOT the MECHANISM, and the blocker is structural.**

| precedent element | reusable for compensation? | why |
|---|---|---|
| `effectiveRetryPolicy`'s **three-tier chain** | ✅ **YES, and it should** — this is the strongest reuse | `StepOptions.CompensationStallAfter`'s own doc comment (`engine/step.go:62-67`) already names `DefaultRetryPolicy` as the model it fell short of, and backlog item 4 already asks for the per-node tier. A `CompensationRetryPolicy` field on `StepOptions` + a per-node `WithCompensateRetryPolicy` would reuse the exact `override → node → default` switch. |
| `model.RetryPolicy` value type + `Normalize()` / `Backoff(attempt)` / `IsNonRetryable(err)` | ✅ YES, unchanged | pure value logic, no token dependency |
| `ScheduleTimer{Kind: TimerRetry}` + `timerRecord` | ⚠ PARTIAL — needs a **new Kind** | `timerRecord.Token` would be empty; ADR-0175 already established the walk-scoped-timer precedent with `TimerCompensationStall`, whose record carries `Token: ""` deliberately (`engine/step_compensation.go:481-482`), and the **cancel-then-arm** discipline in `armCompensationStallTimer` (`:471-498`) is the pattern to copy verbatim. |
| **`tok.RetryAttempts` / `tok.RetryStartedAt` as the attempt counter** | ❌ **NO — this is the hard blocker** | **A compensation walk holds ZERO tokens.** Measured `tokens=0` at every frame of Scenarios A, B and D — `beginCompensation`'s prologue cancels every token (`step_compensation.go:311-321`) and `startCompensationWalk` calls `consumeToken` (`step_nodes.go:1118`). The counter must live on the **`compensationCursor`**, which is exactly where `HANDOVER.md` backlog item 5 already proposes it ("a `StallRetries` counter stamped into `Incident.Attempts`"). ⚠ The cursor is JSON-persisted; a new scalar field is additive and needs no migration (same argument as `TeardownArchiveKey`, `state_compensation.go:159-161`). |
| `handleUnhandledError`'s `raiseIncident` branch | ❌ NO — reuse the **type**, not the function | It calls `s.tokenByID(failingTokenID)` and sets `failingTok.State = TokenIncident`; with no token it would append an incident with an empty `TokenID`, which ADR-0152 flags as a key that names nothing. The correct model is ADR-0175's walk-scoped incident: `Kind: IncidentCompensationStall`, `TokenID: ""`, keyed by `CommandID`. |
| catch-flow / error-boundary escalation (`recoveryFlowOf`, `propagateError`) | ❌ NO | both route a **token** down a flow. A walk has none, and ADR-0170's in-flight guard (`step_errors.go:246-248`) explicitly forbids re-entering error propagation during a walk. The compensation analogue of "escalation" is ADR-0175's three operator verbs. |
| the **incident-retirement sweep** | ✅ YES, already built | `s.retireCompensationStallIncidents(cur.ActiveCmdID)` is called at `stepCompensationAdvance:524`, `retryStalledCompensation:1287,1294` and `abandonCompensationWalk:1345`. A new incident kind must be retired at the same four points or it strands (`:1338-1344` documents exactly this failure). |

**One-line answer for the design:** mirror `effectiveRetryPolicy`'s *tier chain* and
`model.RetryPolicy`'s *value semantics*; mirror ADR-0175's *walk-scoped incident + timer +
retirement-sweep* plumbing for everything that the normal path hangs off a token. Do **not**
try to route a compensation failure through `propagateError`. There is already a
**fourth dispatch site** (`retryStalledCompensation`) doing manual re-dispatch correctly —
an automatic retry is that function driven by a policy and a cursor counter instead of an
operator verb, which is the cheapest possible implementation.

---

## 7. Probe source (throwaway — deleted after the runs)

Written to `engine/zz_recon_probe_test.go`, `package engine_test`. Reused the existing
fixtures `threeCompensableDef` / `runThreeCompensableActivities`
(`engine/step_compensation_test.go:463,487`), `rootSagaWithScopeWideThrow` /
`driveToScopeWideThrow` (`engine/compensation_throw_test.go:78,102`),
`targetedThrowOnceDef` (`engine/step_compensation_ownership_test.go:52`), and the helpers
`invokeActionNamed` / `anyInvokeAction` (`engine/compensation_throw_test.go:38,49`),
`firstInvokeCmd`, `engine.CompensationCursorView` (`engine/export_test.go:43`).

```go
package engine_test

// THROWAWAY RECON PROBE — delete before any commit.

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

func probeDump(t *testing.T, label string, res engine.StepResult) {
	t.Helper()
	var names []string
	for _, c := range res.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			names = append(names, ia.Name+"/"+ia.CommandID)
		} else {
			names = append(names, fmt.Sprintf("%T", c))
		}
	}
	fmt.Printf("  [%s] status=%v cmds=%v\n", label, res.State.Status, names)
	var root []string
	for _, r := range res.State.RootCompensations {
		root = append(root, r.NodeID+":"+r.Action)
	}
	fmt.Printf("  [%s] RootCompensations=%v ArchivedCompensations=%v\n", label, root, res.State.ArchivedCompensations)
	fmt.Printf("  [%s] cursor=%s\n", label, engine.CompensationCursorView(&res.State))
	if len(res.State.Incidents) == 0 {
		fmt.Printf("  [%s] incidents=NONE\n", label)
	}
	for i, inc := range res.State.Incidents {
		fmt.Printf("  [%s] incident[%d] kind=%v token=%q node=%q cmd=%q err=%q attempts=%d\n",
			label, i, inc.Kind, inc.TokenID, inc.NodeID, inc.CommandID, inc.Error, inc.Attempts)
	}
	fmt.Printf("  [%s] tokens=%d endedAt=%v\n", label, len(res.State.Tokens), res.State.EndedAt)
}

// TestReconProbeCompensationActionFailed is the recon probe for backlog 16.
func TestReconProbeCompensationActionFailed(t *testing.T) {
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)

	// ── Scenario A: CANCEL-started walk (beginCompensation, UNPINNED cursor) ──
	for _, failFirst := range []bool{true, false} {
		name := "A-cancelWalk-FAIL-c3"
		if !failFirst {
			name = "A-cancelWalk-CONTROL-succeed-c3"
		}
		t.Run(name, func(t *testing.T) {
			state := runThreeCompensableActivities(t)
			def := threeCompensableDef()
			opt := engine.StepOptions{}

			fmt.Printf("\n=== %s ===\n", name)
			r1, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(at), opt)
			require.NoError(t, err)
			probeDump(t, "after cancel (walk starts)", r1)

			c3 := invokeActionNamed(r1.Commands, "c3")
			require.NotNil(t, c3, "walk must dispatch c3 first (reverse order)")

			var trig engine.Trigger
			if failFirst {
				trig = engine.NewActionFailed(at.Add(time.Minute), c3.CommandID, "boom: refund gateway down", true)
			} else {
				trig = engine.NewActionCompleted(at.Add(time.Minute), c3.CommandID, nil)
			}
			r2, err := engine.Step(t.Context(), def, r1.State, trig, opt)
			require.NoError(t, err)
			probeDump(t, "after c3 outcome", r2)

			cur := r2
			for i := range 4 {
				ia := anyInvokeAction(cur.Commands)
				if ia == nil {
					break
				}
				nxt, err := engine.Step(t.Context(), def, cur.State,
					engine.NewActionCompleted(at.Add(time.Duration(2+i)*time.Minute), ia.CommandID, nil), opt)
				require.NoError(t, err)
				probeDump(t, fmt.Sprintf("after completing %s", ia.Name), nxt)
				cur = nxt
			}
			fmt.Printf("  [%s] FINAL status=%v root=%d incidents=%d\n",
				name, cur.State.Status, len(cur.State.RootCompensations), len(cur.State.Incidents))
		})
	}

	// ── Scenario B: THROW walk (startCompensationWalk, PINNED cursor) ────────
	for _, failFirst := range []bool{true, false} {
		name := "B-throwWalk-FAIL-undoB"
		if !failFirst {
			name = "B-throwWalk-CONTROL-succeed-undoB"
		}
		t.Run(name, func(t *testing.T) {
			def := rootSagaWithScopeWideThrow()
			opt := engine.StepOptions{}
			fmt.Printf("\n=== %s ===\n", name)

			r3 := driveToScopeWideThrow(t, def, "recon-throw", at)
			probeDump(t, "at throw (walk starts)", r3)

			undoB := invokeActionNamed(r3.Commands, "undoB")
			require.NotNil(t, undoB)

			var trig engine.Trigger
			if failFirst {
				trig = engine.NewActionFailed(at.Add(3*time.Second), undoB.CommandID, "boom: undoB failed", true)
			} else {
				trig = engine.NewActionCompleted(at.Add(3*time.Second), undoB.CommandID, nil)
			}
			r4, err := engine.Step(t.Context(), def, r3.State, trig, opt)
			require.NoError(t, err)
			probeDump(t, "after undoB outcome", r4)

			cur := r4
			for i := range 3 {
				ia := anyInvokeAction(cur.Commands)
				if ia == nil {
					break
				}
				nxt, err := engine.Step(t.Context(), def, cur.State,
					engine.NewActionCompleted(at.Add(time.Duration(4+i)*time.Second), ia.CommandID, nil), opt)
				require.NoError(t, err)
				probeDump(t, fmt.Sprintf("after completing %s", ia.Name), nxt)
				cur = nxt
			}
			fmt.Printf("  [%s] FINAL status=%v root=%d incidents=%d\n",
				name, cur.State.Status, len(cur.State.RootCompensations), len(cur.State.Incidents))
		})
	}
}

// TestReconProbeRetryPolicyIgnoredOnCompensation — Scenario C.
func TestReconProbeRetryPolicyIgnoredOnCompensation(t *testing.T) {
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)
	state := runThreeCompensableActivities(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{DefaultRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second}}

	fmt.Printf("\n=== C: DefaultRetryPolicy{MaxAttempts:5} on a FAILED compensation action ===\n")
	r1, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(at), opt)
	require.NoError(t, err)
	c3 := invokeActionNamed(r1.Commands, "c3")
	require.NotNil(t, c3)
	probeDump(t, "walk start", r1)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(at.Add(time.Minute), c3.CommandID, "boom", true), opt)
	require.NoError(t, err)
	probeDump(t, "after c3 FAILED with retry policy in effect", r2)
	fmt.Printf("  re-dispatched c3 again? %v\n", invokeActionNamed(r2.Commands, "c3") != nil)
	fmt.Printf("  scheduled a TimerRetry? %v\n", func() bool {
		for _, c := range r2.Commands {
			if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerRetry {
				return true
			}
		}
		return false
	}())
}

// TestReconProbeTargetedThrowOwnership — Scenario D: the ONE walk shape where
// consumeDispatchedRecord actually does something (ArchiveKey != "").
func TestReconProbeTargetedThrowOwnership(t *testing.T) {
	for _, failIt := range []bool{true, false} {
		name := "targeted-FAIL-undoInner"
		if !failIt {
			name = "targeted-CONTROL-succeed-undoInner"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			def := targetedThrowOnceDef()
			at := scopeDrainT0
			next := func() time.Time { at = at.Add(time.Second); return at }
			fmt.Printf("\n=== D: %s ===\n", name)

			res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "d1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)
			doInner := firstInvokeCmd(res.Commands, "doInner")
			require.NotEmpty(t, doInner)

			res, err = engine.Step(ctx, def, res.State,
				engine.NewActionCompleted(next(), doInner, nil), engine.StepOptions{})
			require.NoError(t, err)
			probeDump(t, "targeted walk started", res)
			walkCmd := res.State.Compensating.ActiveCmdID
			require.NotEmpty(t, walkCmd)

			var trig engine.Trigger
			if failIt {
				trig = engine.NewActionFailed(next(), walkCmd, "boom: undoInner failed", true)
			} else {
				trig = engine.NewActionCompleted(next(), walkCmd, nil)
			}
			res, err = engine.Step(ctx, def, res.State, trig, engine.StepOptions{})
			require.NoError(t, err)
			probeDump(t, "after undoInner outcome", res)
		})
	}
}

// TestReconProbeLateReplyToSupersededCompensationCommand is backlog 3g.
func TestReconProbeLateReplyToSupersededCompensationCommand(t *testing.T) {
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)
	def := threeCompensableDef()
	opt := engine.StepOptions{}

	fmt.Printf("\n=== 3g: late reply to a SUPERSEDED compensation command ===\n")
	state := runThreeCompensableActivities(t)
	r1, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(at), opt)
	require.NoError(t, err)
	c3 := invokeActionNamed(r1.Commands, "c3")
	require.NotNil(t, c3)
	fmt.Printf("  c3 dispatched as CommandID=%q\n", c3.CommandID)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Minute), c3.CommandID, nil), opt)
	require.NoError(t, err)
	c2 := invokeActionNamed(r2.Commands, "c2")
	require.NotNil(t, c2)
	fmt.Printf("  walk advanced; active command is now c2=%q\n", c2.CommandID)

	for _, tc := range []struct {
		label string
		trig  engine.Trigger
	}{
		{"late ActionCompleted for superseded c3", engine.NewActionCompleted(at.Add(2*time.Minute), c3.CommandID, nil)},
		{"late ActionFailed for superseded c3", engine.NewActionFailed(at.Add(2*time.Minute), c3.CommandID, "late boom", true)},
	} {
		r3, err := engine.Step(t.Context(), def, r2.State, tc.trig, opt)
		fmt.Printf("  [%s] err=%v\n", tc.label, err)
		if err == nil {
			probeDump(t, tc.label, r3)
		}
	}
}
```

---

## 8. Design constraints, distilled

1. **The short-circuit is the mechanism, not record consumption.**
   `engine/step_triggers.go:292-294` returns to `stepCompensationAdvance` before
   `effectiveRetryPolicy` (`:316`) and before `propagateError` (`:388`/`:403`) are reached.
   Any retry/incident feature is an edit *at or above line 292*, not deeper.
2. **A compensation walk holds ZERO tokens** (measured, every frame). Every token-hung
   mechanism in the normal path — `RetryAttempts`, `RetryStartedAt`, `AwaitCommand`,
   `TokenIncident`, catch flows, boundaries — is unavailable. Attempt state belongs on the
   **`compensationCursor`**; incident shape must follow `IncidentCompensationStall`
   (walk-scoped, `TokenID: ""`).
3. **There are FOUR dispatch sites, and any new counter/timer must be wired at all four.**
   ADR-0175 shipped a comment claiming three; the miss class is live in this file.
   Sites 2 and 4 already call `consumeDispatchedRecord`; sites 1 and 3 deliberately do not.
4. **There are FOUR `armCompensationStallTimer` call sites**, and `retryStalledCompensation`
   (`:1273-1303`) is a working, tested template for "re-dispatch this record under a fresh
   command id and re-arm" — an automatic retry is that function, policy-driven.
5. **Incident retirement has FOUR call sites** (`:524`, `:1287`, `:1294`, `:1345`). A new
   incident kind that misses any of them strands on the terminated instance and gets
   published as the cause of death (`engine/step_compensation.go:1338-1344`).
6. **ADR-0034's post-acceptance fix forbids retaining records past a full-rollback finish**
   (double-refund on re-delivered `CancelRequested`). Retry must re-dispatch from the
   cursor, not by keeping records alive.
7. **`abandonCompensationWalk` is refused on a resuming walk** — a parked throw walk has only
   `retry` and `skip`. A design that parks such a walk must say what unwedges it.
8. **3g's fix is a guard, not a mechanism change**: recognise a command id that a compensation
   walk *has already passed* and return a benign no-op instead of `ErrTokenNotFound`
   (currently a 422). ⚠ The cursor keeps no history of consumed command ids, so "already
   passed" is not currently derivable from state — `ASSUMPTION (unverified):` this likely
   needs a new field (e.g. a last-completed command id, or a monotonic dispatch seq).
9. **ADR-0034's own "logged/skipped" claim is FALSE (EXECUTED, §5.3)** — zero `slog` calls on
   the skip path. Today a failed compensation action is invisible in every channel: no
   incident, no retry, no log. Correct the ADR in the same bundle.

---

## 9. Cleanup

`engine/zz_recon_probe_test.go` was deleted after the runs. `git status --porcelain` is
EMPTY and `go build ./engine/...` succeeds — the worktree is back at `12c9d7e3` unmodified.
Nothing was fixed and nothing was committed.
