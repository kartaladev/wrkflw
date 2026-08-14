# Recon A — engine-side `TimerWaiters()` gap (blocker 9 / backlog 3c)

Worktree: `/Users/zakyalvan/Documents/RND/wrkflw/.claude/worktrees/agent-ae327fe37e4d61b82`
HEAD verified: `12c9d7e3 docs: codify that docs/specs holds a delivery's evidence records too`, `git status --porcelain` empty. Base is post-ADR-0176.

## 1. The real shape (source, not paraphrase)

### `engine/state_timers.go:11-32` — `timerRecord` (UNEXPORTED type, `Kind` field is exported but the TYPE is not)
```go
type timerRecord struct {
	TimerID string
	Kind TimerKind
	Token string
	TaskID string
	NodeID string
	ScopeID string
	CommandID string
}
```

### `engine/state_timers.go:132-153` — `HasArmedTimers`
```go
// ⚠ KNOWN GAP, pre-existing and deliberately not closed here: it reads
// s.Timers only, so a timer armed as a BOUNDARY, event-gateway or event
// sub-process arm is invisible to it. Closing that needs an engine-side
// TimerWaiters() enumerating all four sources, mirroring SignalWaiters and
// MessageWaiters.
func (s *InstanceState) HasArmedTimers() bool {
	for _, tr := range s.Timers {
		if tr.Kind != TimerCompensationStall {
			return true
		}
	}
	return false
}
```

### `engine/command.go:17-41` — the FIVE `TimerKind` constants
`TimerIntermediate` (iota 0), `TimerDeadline`, `TimerInWait`, `TimerRetry`, `TimerCompensationStall`.

### `engine/state.go:231` — `Timers []timerRecord` on `InstanceState` (exported field, unexported element type)

### `processtest/park.go:92-100,141,175` — `Park.HasArmedTimers`
Set at park.go:141 `HasArmedTimers: state.HasArmedTimers()`, used at park.go:175 to pick `ReasonTimer`.
`processtest/harness.go:300-320` `harnessEnv.classify` OVERRIDES it to true when a parked token's
`AwaitCommand` matches a pending scheduler timer (scheduler-knowledge enrichment).

### `processtest/handlers.go:22-29` — `AutoTimers`
```go
func AutoTimers() ParkHandler {
	return func(_ context.Context, p Park) (Decision, error) {
		if p.Reason == ReasonTimer {
			return AdvanceTimers(), nil
		}
		return Pass(), nil
	}
}
```
(acts on `p.Reason`, NOT on `p.HasArmedTimers` directly.)

## 2. RE-COUNT — every site that ARMS a timer

`grep -rn "ScheduleTimer{" --include='*.go' . | grep -v '_test.go'` → **EIGHT** production sites.
`grep -rn "nextTimerID()"` → the same eight allocation sites (+ the definition at `engine/step_state.go:158`).
Cross-checked against writers of the four state structures.

| # | file:line | construct | `ScheduleTimer.Kind` | where the arm is RECORDED | visible to `HasArmedTimers()`? |
|---|---|---|---|---|---|
| 1 | `engine/step_nodes.go:699` (`armWaitReminder`) | in-wait reminder (UserTask / ReceiveTask / ICE) | `TimerInWait` | `s.Timers` (append at :705) | **YES** |
| 2 | `engine/step_nodes.go:772` (`userTaskStrategy.enter`) | user-task deadline | `TimerDeadline` | `s.Timers` (append at :778) | **YES** |
| 3 | `engine/step_nodes.go:831` (`intermediateCatchEventStrategy.enter`) | plain intermediate catch timer | `TimerIntermediate` | **NOTHING** — only `tok.AwaitCommand = timerID` | **NO** |
| 4 | `engine/step_nodes.go:988` (event-based-gateway entry) | event-gateway timer arm | `TimerIntermediate` | `s.ArmedEvents` (append at :1005) | **NO** |
| 5 | `engine/step_boundaries.go:54` (`armBoundaries`) | boundary timer | `TimerIntermediate` | `s.Boundaries` (append at :70) | **NO** |
| 6 | `engine/step_eventsubprocess.go:97` (`armEventTriggeredSubprocesses`) | event-sub-process timer start | `TimerIntermediate` | `s.EventTriggeredSubprocesses` (append at :112) | **NO** |
| 7 | `engine/step_triggers.go:328` (retry scheduling) | action retry backoff | `TimerRetry` | `s.Timers` (append at :334) | **YES** |
| 8 | `engine/step_compensation.go:493` (`armCompensationStallTimer`) | compensation-stall guard | `TimerCompensationStall` | `s.Timers` (append at :478) | **NO** (deliberately excluded, ADR-0175) |

**Writers of `s.Timers`: FOUR** (`step_nodes.go:705`, `step_nodes.go:778`, `step_triggers.go:334`, `step_compensation.go:478`).
**Timer-arm sources NOT in `s.Timers`: FOUR** (sites 3, 4, 5, 6) — the handover names only three of them
(boundary, event-gateway, event sub-process). **Site 3, the plain intermediate catch event, is a fourth
and is missed by both `HasArmedTimers`'s own doc comment and by `HANDOVER.md`.**

### Timer KIND constants — all FIVE (`engine/command.go:20-41`)

| kind | produced by site(s) | lands in |
|---|---|---|
| `TimerIntermediate` (iota 0) | 3, 4, 5, 6 | **never `s.Timers`** — `Token.AwaitCommand` / `s.ArmedEvents` / `s.Boundaries` / `s.EventTriggeredSubprocesses` |
| `TimerDeadline` | 2 | `s.Timers` |
| `TimerInWait` | 1 | `s.Timers` |
| `TimerRetry` | 7 | `s.Timers` |
| `TimerCompensationStall` | 8 | `s.Timers` |

Confirmed by `engine/step_triggers.go:524-526`:
> `s.Timers` holds deadline (TimerDeadline), in-wait/reminder (TimerInWait), and retry
> (TimerRetry) records. Intermediate timers (TimerIntermediate) are never appended to
> `s.Timers`; for those, the token parks on the TimerID as its AwaitCommand.

(That comment predates ADR-0175 and now under-counts: `TimerCompensationStall` is in `s.Timers` too —
`handleTimerFired` does dispatch it at `step_triggers.go:537`. The prose list at :524 was not updated.)

⚠ **`engine/state.go:225` says "Timers is the auxiliary bookkeeping table for ALL scheduled timers." That
quantifier is FALSE** — four of the eight arm sites never touch it. Committed-comment defect.

### Readers of `s.Timers` — SEVEN production sites, all in `engine`
1. `engine/state_timers.go:36` `timerByID`
2. `engine/state_timers.go:55` `removeTimer`
3. `engine/state_timers.go:73` `cancelTimersWhere` (→ `cancelTimersByTaskID`, `cancelTimersForToken`)
4. `engine/state_timers.go:121-128` `cancelAllTimers`
5. `engine/state_timers.go:147` `HasArmedTimers`
6. `engine/step_compensation.go:434-456` `cancelCompensationStallTimers`
7. `engine/step_state.go:395` `cloneState` (deep-copy)
Plus the dispatch consumer `engine/step_triggers.go:529-539` (via `timerByID`).
**No reader outside package `engine`** (`grep -rn "\.Timers" runtime/ service/ transport/ internal/ processtest/` → none in production code; the only external reads are in my throwaway probes).

## 3. EXECUTED — the load-bearing claim

### Probe 1 — engine level (`engine/zzz_recon_timerwaiters_probe_test.go`, package `engine_test`)
Command: `go test -count=1 -run '^TestReconTimerArmVisibility$' -v ./engine/` → **EXIT=0**, `--- PASS`.

```
=== A: boundary TIMER on parked USER task ===
  len(s.Timers)                   = 0
  s.HasArmedTimers()              = false
  len(s.Boundaries)               = 1
    Boundaries[0] = {BoundaryNode:bnd HostToken:i1-t1 TimerID:"i1-tm1" Signal:"" Message:""}
    Tokens[0] = {ID:i1-t1 Node:work State:1 AwaitCommand:"i1-h1" ...}
    ScheduleTimer{TimerID:i1-tm1 Token:"i1-t1" Kind:TimerIntermediate}
=== B: boundary TIMER on parked RECEIVE task ===
  len(s.Timers) = 0   s.HasArmedTimers() = false   len(s.Boundaries) = 1
    Boundaries[0] = {BoundaryNode:bnd HostToken:i1-t1 TimerID:"i1-tm1" ...}
    Tokens[0] = {ID:i1-t1 Node:recv State:1 AwaitCommand:"" AwaitMessage:"go"}
=== C: EVENT-BASED GATEWAY timer arm ===
  len(s.Timers) = 0   s.HasArmedTimers() = false   len(s.ArmedEvents) = 2
    ArmedEvents[0] = {CatchNode:timer-catch GatewayToken:i1-t1 TimerID:"i1-tm1" ...}
    ArmedEvents[1] = {CatchNode:signal-catch GatewayToken:i1-t1 TimerID:"" Signal:"approved"}
    Tokens[0] = {ID:i1-t1 Node:evtgw State:1 AwaitCommand:"evtgw:i1-t1"}
=== D: root EVENT SUB-PROCESS timer start ===
  len(s.Timers) = 0   s.HasArmedTimers() = false   len(s.EventTriggeredSubprocesses) = 1
    ESP[0] = {Node:esp Scope:"" TimerID:"i1-tm1" ...}
    ScheduleTimer{TimerID:i1-tm1 Token:"" Kind:TimerIntermediate}
=== E: plain INTERMEDIATE CATCH timer ===
  len(s.Timers) = 0   s.HasArmedTimers() = false
  len(s.Boundaries) = 0   len(s.ArmedEvents) = 0   len(s.EventTriggeredSubprocesses) = 0
    Tokens[0] = {ID:i1-t1 Node:wait State:1 AwaitCommand:"i1-tm1"}
=== F: CONTROL user task with DEADLINE timer ===
  len(s.Timers) = 1
    Timers[0] = {TimerID:i1-tm1 Kind:TimerDeadline Token:i1-t1 TaskID:i1-h1 NodeID:work}
  s.HasArmedTimers()              = true
```
Control F proves the assertions are not vacuous: the same predicate returns `true` when the arm
really is in `s.Timers`.

**VERDICT: the handover's load-bearing claim is CONFIRMED and UNDER-STATED.**
`HasArmedTimers()` returns `false` for a boundary timer, an event-gateway timer arm, an
event-sub-process timer arm **AND a plain intermediate-catch-event timer** (case E — a source the
handover does not list).

### Probe 2 — SHIPPED harness level (`processtest/zzz_recon_timerpark_probe_test.go`)
Command: `go test -count=1 -run '^TestReconHarnessTimerParkVisibility$' -v ./processtest/` → **EXIT=0**, `--- PASS`.

```
=== B: boundary TIMER on RECEIVE task ===
  Park.Reason = message   Park.HasArmedTimers = false   State.HasArmedTimers() = false
  len(State.Boundaries) = 1   AwaitingMessages = [{go }]   AutoTimers would act? = false
=== C: EVENT-GATEWAY timer arm racing a message arm ===
  Park.Reason = message   Park.HasArmedTimers = false   State.HasArmedTimers() = false
  len(State.ArmedEvents) = 2   AutoTimers would act? = false
=== D: root EVENT SUB-PROCESS timer start over a RECEIVE task ===
  Park.Reason = message   Park.HasArmedTimers = false   State.HasArmedTimers() = false
  len(State.EventTriggeredSubprocesses) = 1   AutoTimers would act? = false
=== E: plain INTERMEDIATE CATCH timer ===
  Park.Reason = timer     Park.HasArmedTimers = TRUE    State.HasArmedTimers() = false
  AutoTimers would act? = true
```

**Case E REFUTES "Park.HasArmedTimers delegates to it and therefore inherits the defect" as an
unqualified statement.** `processtest/park.go:141` does delegate, but `harnessEnv.classify`
(`processtest/harness.go:300-320`) OVERRIDES the field to `true` whenever a parked token's
`AwaitCommand` matches a pending scheduler timer. That compensates for source 3 (plain ICE) and
for nothing else — B/C/D still classify as `message` and `AutoTimers` passes forever.
The inheritance is therefore **partial**, and only under a `Harness`; the free `processtest.Classify`
inherits it fully.

### Probe 3 — the stall-exclusion claim
`go test -count=1 -run '^TestStallTimerIsExcludedFromHasArmedTimers$' -v ./processtest/` → **EXIT=0**, `--- PASS` (test observed RUNNING under `-v`).

Probe 4 measured the same state directly (`processtest/zzz_recon_kind_probe_test.go`, EXIT=0):
```
len(st.Timers) = 1, st.HasArmedTimers() = false
Timers[0].Kind = TimerCompensationStall TimerID=i1-tm1
stall state: Reason=unknown HasArmedTimers=false len(Tokens)=0 status=compensating incidents=0
stall state: AutoTimers would act? = false
```
**TRUE**: `AutoTimers()` does not fire a stall timer. Mechanism is one step removed from the
handover's phrasing — `AutoTimers` gates on `p.Reason == ReasonTimer`, not on `HasArmedTimers`
directly; the exclusion keeps `Reason` at `unknown`.

### Probe 4 — "processtest physically cannot exclude a stall timer on its own": **FALSE**
`processtest_test` is an EXTERNAL package to `engine`. Go permits selecting an EXPORTED field of a
value whose TYPE is unexported. Measured, compiled and ran:
```go
st := stalledCompensationState(t)          // real engine-built mid-walk snapshot
for i := range st.Timers {
    if st.Timers[i].Kind == engine.TimerCompensationStall { continue }   // COMPILES + RUNS
}
```
Output: `Timers[0].Kind = TimerCompensationStall TimerID=i1-tm1` → `-> Timers[0] IS
TimerCompensationStall, excluded by consumer code`.

So the doc comment at `engine/state_timers.go:138` — *"That distinction is invisible outside this
package — timerRecord.Kind is unexported"* — is **wrong**. `TimerKind` is exported
(`engine/command.go:18`) and the field `Kind` is exported; only the STRUCT TYPE `timerRecord` is
unexported. What a consumer genuinely cannot do is **CONSTRUCT** a `timerRecord` (no composite
literal for an unexported type) — which is what `park_compensation_stall_test.go:85` and
`park_test.go:271` actually claim, and both of those are correct.
`HasArmedTimers` is therefore justified as an *ergonomics/authority* API, not as the only
mechanically possible one.

## 4. The design surface for `TimerWaiters()`

### Existing precedent to FOLLOW, not reinvent

`engine/state_waiters.go` is the whole pattern, and it is a **2-tier** one:

```go
// engine/state_waiters.go:7-13
type MessageWaiter struct {
	Name string
	CorrelationKey string
}
```
Per-source accessors (SIX of them, three per family):
`MessageBoundaryWaiters()`, `MessageArmedEventWaiters()`, `MessageEventSubprocessWaiters()`,
`SignalBoundaryNames()`, `SignalArmedEventNames()`, `SignalEventSubprocessNames()` —
each a one-liner over the generic `messageWaitersOf[T]` / `signalNamesOf[T]`
(`engine/state_arms.go`, which already scan the `triggerMatch` embed).
Then the single authorities `MessageWaiters()` / `SignalWaiters()` composing
**token awaits + boundaries + gateway arms + event-subs**, documented as:
> It is the single authority a runtime mirrors into its correlation table — a future
> message construct extends only this method, not every runtime call site (ADR-0123).

Naming asymmetry to decide on: signals return `[]string` (bare names), messages return
`[]MessageWaiter`. Timers need a struct, because the payload is `TimerID` **plus `Kind`**.

`engine/state_arms.go` already has the generic scanning helpers to extend:
`messageWaitersOf[T, PT]` and `signalNamesOf[T, PT]` — a `timerWaitersOf[T, PT]` returning
`m.TimerID != ""` entries is a 6-line sibling. **But** the three arm families carry NO `Kind` in
`triggerMatch`; every arm-borne timer is armed as `TimerIntermediate` (sites 4, 5, 6 — verified in
§2), so the kind would have to be supplied per-family by the wrapper, not read off the arm.

### Existing exported timer view (different layer, do NOT confuse)
`runtime/kernel/timerstore.go:24-32`:
```go
type ArmedTimer struct {
	InstanceID string
	DefID      string
	DefVersion int
	TimerID    string
	Trigger    schedule.TriggerSpec
	NextRun    time.Time
	Kind       engine.TimerKind
}
```
This is the RUNTIME store row (what the scheduler persisted), not an engine-state view. It is the
right precedent for *carrying `engine.TimerKind` on an exported view type*, and the wrong one to
copy wholesale (it carries scheduler/persistence fields the engine core must not know).

### What `TimerWaiters()` must return to be useful to `processtest`

1. **`TimerID`** — so the harness can correlate against `MemScheduler.Pending(id)` and fire a
   specific timer rather than "the next one globally". `AdvanceTimers()` today is global.
2. **`Kind`** — the ONLY thing that lets a consumer drop a `TimerCompensationStall` entry. Exported
   already; no new API needed for the enum.
3. **Deterministic order, nil when empty** — matches the two siblings' documented contract.
4. Probably a **source/owner discriminator** (`NodeID` or an arm-kind tag), because `Park.Node`
   currently guesses the node via `firstNodeWhere(state.Tokens, TokenWaiting)` — measured wrong-ish
   for arm parks (case D has no token at the ESP node at all).

### ⚠ THE HARD CONSTRAINT nobody has written down

`SignalWaiters` / `MessageWaiters` can enumerate the TOKEN source because a token carries an
explicit `AwaitSignal` / `AwaitMessage` field. **There is no `Token.AwaitTimer`** (`engine/state.go:86-113`
— the await fields are `AwaitCommand`, `AwaitSignal`, `AwaitMessage`, `AwaitMessageKey`).
Source 3 (plain intermediate catch timer) parks on the OVERLOADED `AwaitCommand`, which measured
holds, across my probes:
- `"i1-h1"` — a human-task id (case A/D)
- `"evtgw:i1-t1"` — an event-gateway sentinel (case C)
- `"i1-tm1"` — a timer id (case E)
- `""` — nothing (case B, parked on `AwaitMessage`)
plus action command ids on service-task paths.

So an `InstanceState.TimerWaiters()` **cannot tell from state alone** that `AwaitCommand` names a
timer. The three ways out, none free:
- **(a) id-prefix sniffing** — ids are `<instanceID>-tm<N>` (`nextTimerID` = `nextID("tm", &s.TimerSeq)`,
  `engine/step_state.go:158`). Parsing an identity contradicts the ADR-0152 identity discipline the
  arm lookups are built on, and would silently break if the id scheme is ever made opaque.
- **(b) a new `Token.AwaitTimer` field (or a `TimerIntermediate` record in `s.Timers`)** — the
  structurally honest fix, but it changes the persisted `InstanceState` JSON and needs a migration
  story for in-flight instances; it also makes site 3 a fifth writer of `s.Timers` and would change
  `handleTimerFired`'s step-5 fall-through (`engine/step_triggers.go:543-546`).
- **(c) scope `TimerWaiters()` to the four RECORDED sources** (`s.Timers` + 3 arm families) and leave
  source 3 to the harness's existing scheduler-based promotion, which measurably already handles it
  (probe 2 case E: `Park.HasArmedTimers = true`). Cheapest, and closes exactly the gap that is open.

### Where the stall filter belongs
The siblings enumerate EVERYTHING and let the caller decide (`SignalWaiters` explicitly does not even
dedup). Keeping `TimerWaiters()` unfiltered and redefining
`HasArmedTimers() = any(TimerWaiters(), w.Kind != TimerCompensationStall)` preserves both contracts
and keeps `HasArmedTimers`'s existing semantics ("timers a harness may legitimately fire") intact.

## 5. Blast radius

- `engine` is a **module-root public package**; `InstanceState`, `HasArmedTimers`, `MessageWaiter`,
  `TimerKind` and its five constants are all exported → part of the public API per `STABILITY.md`
  ("the compatibility promise covers only the exported, module-root packages").
- `STABILITY.md` also states the module is **pre-1.0 with no released tag**: *"every exported symbol
  in every root package is subject to change without notice."* A signature change is technically
  breaking but carries no released-version cost today.
- **Call sites of `engine.InstanceState.HasArmedTimers()` repo-wide** (`grep -rn "\.HasArmedTimers()"`,
  excluding my probes): **TWO**.
  - production: **ONE** — `processtest/park.go:141`.
  - test: **ONE** — `engine/step_compensation_stall_test.go:378`.
- Callers of the `Park.HasArmedTimers` FIELD (distinct from the method): `processtest/park.go:175`,
  `processtest/harness.go:312` (write), plus 4 test reads
  (`park_test.go:261`, `park_compensation_stall_test.go:91`, `harness_test.go:38`,
  `harness_internal_test.go:77,87`).
- **`runtime/`, `service/`, `transport/`, `internal/`, `examples/`: ZERO callers.** The runtime uses
  the sibling authorities instead — `runtime/processdriver_waiters.go:42` (`st.SignalWaiters()`) and
  `:76` (`st.MessageWaiters()`). An added `TimerWaiters()` would be a purely ADDITIVE change with
  one existing production consumer to update.
- Nobody outside `engine` reads `InstanceState.Timers` in production code (the `transport/http/*/groups.go`
  `.Timers` hits are a route-group config field, unrelated).

## 6. Committed-comment defects found while re-counting (fix candidates, not in scope)
1. `engine/state.go:225` — *"Timers is the auxiliary bookkeeping table for **all** scheduled timers"* — FALSE (4 of 8 arm sites bypass it).
2. `engine/state_timers.go:138` — *"timerRecord.Kind is unexported"* — FALSE (probe 4 compiled and ran external access).
3. `engine/state_timers.go:141-145` — the KNOWN GAP note names **three** invisible sources; there are **four** (plain intermediate catch is missing), and it says "all four sources" where the real total including `s.Timers` is five.
4. `engine/step_triggers.go:524-526` — *"s.Timers holds deadline, in-wait/reminder and retry records"* — under-counts: `TimerCompensationStall` is in `s.Timers` too and is dispatched twelve lines below at :537.

## 7. Probe files (throwaway, left in the worktree, NOT committed)
- `engine/zzz_recon_timerwaiters_probe_test.go` — `TestReconTimerArmVisibility`
- `processtest/zzz_recon_timerpark_probe_test.go` — `TestReconHarnessTimerParkVisibility`
- `processtest/zzz_recon_kind_probe_test.go` — `TestReconExternalPackageCanReadTimerKind`
Logs: `probe1.log`, `probe2.log`, `probe3.log`, `probe4.log` in this scratchpad.
