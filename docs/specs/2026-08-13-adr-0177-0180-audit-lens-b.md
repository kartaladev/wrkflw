# Audit lens B — FAILURE MODES & CROSS-DOCUMENT COHERENCE

Bundle: engine visibility & truthfulness (ADR-0177..0180 + spec + plan)
Started: 2026-08-13

## Findings (append as established)

### B1 CRITICAL — `DispatchedCmdIDs` contains the ACTIVE command id, so the "benign duplicate" rule swallows every LEGITIMATE compensation reply
Docs: spec §3 "Backlog 3g"; ADR-0179 Decision 4; plan P2 step 9.
All three say the same unqualified thing: appended at each dispatch; "a reply whose `CommandID` is
in that set returns no error, no state change, no commands." The four dispatch sites are exactly
where `ActiveCmdID` is set, so the in-flight command's id is A MEMBER of the set the moment it is
dispatched. If the duplicate check runs before/instead of the `ActiveCmdID` match, EVERY normal
compensation `ActionCompleted` is treated as a duplicate → walk never advances → permanent stall.
No document states the active id must be excluded, or that the check must run after the
ActiveCmdID match. This is worse than the 422 it replaces (task brief anticipated exactly this).
FIX: predicate must be `cmdID != cur.ActiveCmdID && slices.Contains(cur.DispatchedCmdIDs, cmdID)`,
stated in ADR-0179 Decision 4 + spec §3 + plan P2 step 9; and a test
`TestActiveCompensationCommandIsNotTreatedAsDuplicate` whose RED is the naive predicate.

### B2 CRITICAL — a new slice field on `compensationCursor` is ALIASED by `cloneState`
Docs: spec §10 "Persisted-shape changes (all additive, no migration)"; ADR-0179 Consequences;
plan P2 step 6/9. None mentions `cloneState`.
`engine/step_state.go:361` `cloneState` starts `s := st` (shallow struct copy) and then deep-copies
field by field. `Compensating` is carried by that struct copy; the ONLY cursor field given a deep
copy is `Compensating.Records` (`:415`). `RetryAttempts`/`RetryStartedAt` are scalars (fine), but
`DispatchedCmdIDs []string` would share its backing array between clone and original. The repo has
already paid for this class twice in this very function — see its own comments at `:384-386` and
`:421-423` ("a non-nil zero-length source still shares its backing array … a later append on either
side would write the same slot"). `compensationCursor`'s doc comments repeatedly justify new fields
as "plain scalars, keeping this struct value-copyable by cloneState" (`:141`, `:163-164`) — that
invariant is broken by this bundle and no document notices.
FIX: plan P2 must add `s.Compensating.DispatchedCmdIDs = append([]string(nil),
st.Compensating.DispatchedCmdIDs...)` to `cloneState`, with a RED test
`TestCloneStateDeepCopiesDispatchedCmdIDs` modelled on `TestCloneStateDeepCopiesCompensatingRecords`
(`engine/state_test.go:124`); spec §10 must list it as a clone-surface change, not merely a
persisted-shape one.

**EXECUTED (not reasoned).** Probe: temporarily added `DispatchedCmdIDs []string` to
`compensationCursor`, ran `go test -count=1 -v -run '^TestAuditProbeCursorSliceAliasing$' ./engine/`,
`EXIT=0`, `--- PASS`, then restored from a `cp` backup (`git status --porcelain` empty, `go build`
OK). Observed:
```
after clone write: orig[0]="MUTATED" clone[0]="MUTATED"  ALIASED=true
two clones append: a=[MUTATED c2 fromB] b=[MUTATED c2 fromB]  A_LOST_ITS_APPEND=true
CONTROL Records:  orig="n1" clone="MUTATED"                  ALIASED=false
```
The CONTROL row is what makes this non-vacuous: `Compensating.Records` is deep-copied today and
does NOT alias, so the difference is the missing clone line and nothing else. The second row is the
consequence that matters: two clones of one base append into the same slot, so clone A's recorded
command id is silently overwritten by clone B's — a dispatched id vanishes from the set and the
HTTP 422 this whole feature exists to remove comes back non-deterministically.

### B3 CRITICAL — a `TimerCompensationRetry` record is never retired at walk finish, and ADR-0178's exemption then ADMITS it against a ZEROED cursor
Docs: ADR-0179 Decision 2; ADR-0178 Decision + Consequences; spec §5.1; plan P2 step 6 / P3 step 3.
Source-verified: `stepCompensationFinish` (`engine/step_compensation.go:851`) zeroes the cursor
(`s.Compensating = compensationCursor{}`) and then retires the walk guard via
`cancelCompensationStallTimers(s)` — which filters **strictly** on `tr.Kind == TimerCompensationStall`
in both its emit loop and its rebuild loop. A new `TimerCompensationRetry` record is invisible to it.
The four RESUME finishes (throw-targeted, throw-scope-wide, partial rollback, full reverse) "never
touch s.Timers" per that function's own comment, so the retry record survives onto a Running
instance. ADR-0178 then **deliberately exempts** `walkScoped()` kinds from the dying-instance guard,
so this leaked timer is admitted on both a running and a terminated instance and dispatched against
`Compensating == compensationCursor{}` (nil `Records`, `NextIndex` 0, `ActiveCmdID` ""). That is the
same shape ADR-0171 documents as having PANICKED inside the pure engine core in the consumer's
process (`engine/state_compensation.go:90-93`).
Note the asymmetry the bundle missed: the stall timer is safe here for TWO reasons — the sweep
retires it, AND `handleCompensationStallFired` re-checks `rec.CommandID == Compensating.ActiveCmdID`
(`engine/state_timers.go:27-30`). ADR-0179 prescribes NEITHER for the retry kind.
FIX: (a) rename/extend the sweep to retire every `walkScoped()` kind at finish — one predicate, as
§5.1 already argues for the guard; (b) `handleCompensationRetryFired` must carry the stall handler's
late-fire check (`rec.CommandID != cur.ActiveCmdID` → no-op) and a `cur.ActiveCmdID == ""` /
`len(cur.Records)==0` bail; (c) plan P2 gains a RED test
`TestCompensationRetryTimerIsRetiredOnWalkFinish` and `TestLateCompensationRetryFireOnAFinishedWalkIsANoOp`;
(d) spec §5 gains this as interaction 5 — it currently reasons only about the guard, not the sweep.

### B4 MAJOR — `RetryAttempts` is cursor-scoped but the walk has MANY records; no document says it resets per record
Docs: ADR-0179 Decision 2 ("Attempt state lives on `compensationCursor` (`RetryAttempts`,
`RetryStartedAt`), which is forced by the zero-token constraint"); spec §3.2; plan P2 step 6.
A compensation walk drains N records in reverse (`NextIndex` counts down). With
`CompensationRetryPolicy{MaxAttempts:5}` and a cursor-scoped counter that is never reset when the
walk advances to the next record, record N's failures consume the budget for records N-1…0: the
first poison record exhausts the policy and every subsequent record gets **zero** retries — the
opposite of what a consumer setting `MaxAttempts:5` expects. The mirror bug (never resetting in the
other direction) is unbounded retrying. No document states which happens.
FIX: ADR-0179 Decision 2 must state the reset point explicitly ("`RetryAttempts` and
`RetryStartedAt` are zeroed whenever the walk advances `NextIndex`, i.e. the budget is per-record"),
and plan P2 needs a test `TestCompensationRetryBudgetIsPerRecordNotPerWalk` with a fixture of ≥2
failing records — RED today because the counter would be shared.

### B5 MAJOR — "bounded by the walk's dispatch count (records + retries), not unbounded" is false for the operator retry verb
Docs: spec §3 "Backlog 3g" last sentence; ADR-0179 Consequences bullet 5 (restated there without
its hedge — the Premise-Discipline restatement defect).
Two independent unbounded sources: (1) ADR-0175's operator verb `retryStalledCompensation`
(`engine/step_compensation.go:1228-1297`) sets a fresh `ActiveCmdID` on every invocation and is an
operator-driven verb with no attempt cap in the cursor — an operator can invoke it arbitrarily
often on one walk, each appending an id; (2) the cursor is only zeroed at
`step_compensation.go:853` / `state.go:476`, so the slice lives for the whole walk, and per E5 the
whole state is persisted by `json.Marshal` on every step — so this is unbounded growth of the
persisted row, re-marshalled every step, on exactly the walk an operator is already hammering.
FIX: state the real bound. Either cap the slice (keep the last K ids, K ≥ max in-flight
supersessions — a ring, documented) or bound it by `len(Records) × MaxAttempts + operatorRetries`
and say the operator term is unbounded. Spec §3 and ADR-0179 Consequences must both carry the
corrected sentence; ADR-0179's version must not restate it as a flat reassurance.

### B6 MAJOR — the redelivered `ActionFailed` during the retry backoff window is unmodelled (double incident, double timer, double attempt-count)
Docs: ADR-0179 Decision 1+2; spec §3; plan P2 steps 1/5.
Between the first `ActionFailed(c3)` and the retry re-dispatch there is a backoff window. No
document says whether `ActiveCmdID` is cleared on failure. Both branches are broken and the bundle
picks neither:
- If `ActiveCmdID` stays `c3`: a redelivered `ActionFailed(c3)` (at-least-once is the premise of
  backlog 3g!) is NOT a duplicate under B1's corrected predicate (active is excluded), so it raises
  a SECOND `IncidentCompensationFailed`, arms a SECOND `TimerCompensationRetry`, and increments
  `RetryAttempts` twice — two retry timers then both fire and dispatch the same record twice, which
  is the double-compensation (double-refund) hazard ADR-0034's post-acceptance fix exists to prevent.
- If `ActiveCmdID` is cleared: the retry timer has no active command to validate against and the
  late-fire check of B3(b) cannot be written.
FIX: ADR-0179 must state the window's state machine — recommended: keep `ActiveCmdID = c3` and make
the FIRST `ActionFailed` transition the cursor into a `RetryPending` condition
(`RetryAttempts > 0 && a TimerCompensationRetry record exists for ActiveCmdID`), with a second
`ActionFailed` for the same id treated as a benign duplicate. Plan P2 gains
`TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm`.

### B7 CRITICAL — "the load-bearing edit of this ADR" is prescribed in the WRONG PACKAGE, and the plan cannot even run its test
Docs: spec §3 last ⚠ ("it must instead be **excluded from the cause-of-death publication**. That
exclusion is the load-bearing edit"); ADR-0179 Consequences bullet 2 (same words); plan P2 step 3/4
+ the phase table + P2's Verify line.
Both documents cite `engine/step_compensation.go:1338-1344` as the exclusion site. Source-verified:
that range is a **comment** describing behaviour that lives elsewhere. The actual publication is two
functions in package **`runtime`**, both taking `st.Incidents[0].Error` unconditionally:
`runtime/outbox.go:81-84` `terminalEventErr` and `runtime/processdriver_action.go:31-34` `terminalErr`.
Consequences:
(a) The plan's phase table lists P2's package(s) as **`engine`** only, and P2's Verify is
`go test -count=1 ./engine/... ; echo "EXIT=$?"` — which never compiles or runs `runtime`. The
prescribed test `TestCompensationFailedIncidentIsNotPublishedAsCauseOfDeath` cannot live in
`engine` at all, because `engine` has no publication to assert against. As written, P2 step 4 is
the ADR-0162 zombie-scope shape: an ADR promising behaviour the plan does not build.
(b) `Incidents[0]` is positional, not kind-filtered, so on a **cancel** walk (no prior error
incident) `IncidentCompensationFailed` becomes `Incidents[0]` and is published as the instance's
cause of death — the exact failure `engine/state.go:462-470` and `step_compensation.go:1338-1344`
were written to prevent for the stall kind.
(c) `./runtime/...` as a whole is not container-free in this repo, which P2's plan entry does not
account for at all (no Docker note, unlike the Verification checklist).
FIX: P2's package column becomes `engine`, `runtime`; both resolvers select the first incident whose
`Kind != IncidentCompensationFailed` (one shared helper, not two divergent filters) and fall through
to the existing `FailInstance.Err` / status-generic fallbacks when that leaves none; P2's Verify
becomes `go test -count=1 ./engine/... ./runtime/... ; echo "EXIT=$?"` with the Docker note; spec §3
and ADR-0179 replace the `:1338-1344` citation with the two `runtime` symbol names (per this repo's
"prefer symbol names over line numbers" lesson — ADR-0179's four retirement sites are likewise cited
as bare `:524, :1287, :1294, :1345` with no filename).

### B8 MAJOR — the incident-retirement enumeration is FIVE sites, not four; the missed one is `endInstance`, the terminal path the whole hazard lives on
Docs: spec §3 last ⚠ ("must be retired at all **FOUR** retirement sites (`:524`, `:1287`, `:1294`,
`:1345`)"); ADR-0179 Consequences bullet 2 (same four). Also refutes the stalled round's E7.
Re-derived by grep of the real call sites of `retireCompensationStallIncidents` (definition at
`engine/state.go:421`):
```
engine/state.go:475            (inside endInstance)   <-- MISSING from the bundle
engine/step_compensation.go:524
engine/step_compensation.go:1287
engine/step_compensation.go:1294
engine/step_compensation.go:1345
```
Five. The bundle lists only the four in `step_compensation.go` — every site outside that one file
was dropped, which is the signature of a single-file grep. The missed site is `endInstance`'s
REMAINDER sweep, i.e. exactly the terminal transition on which a non-retired kind "strands on the
terminated instance and is published as the cause of death" — the sentence the enumeration is
supporting. The bundle's own §6 corrections table is a list of six enumerations that rotted in this
codebase; this is the seventh, introduced by the document fixing them.
FIX: correct both documents to five sites, cite them by file (`engine/state.go:475` +
`engine/step_compensation.go:{524,1287,1294,1345}`), and re-assert the `IncidentCompensationFailed`
deliberate-non-retirement decision against the corrected set — in particular against
`endInstance`, where B7's publication hazard actually fires.

### B9 (verification of the stalled round's E5/E6, plus a consequence neither states)
E5 CONFIRMED: `internal/persistence/store/store_core.go:78` `json.Marshal(capHistory(step.State, …))`,
`:163-164` plain `json.Unmarshal`, no `DisallowUnknownFields`. The downgrade consequence the stalled
round derived stands and is **absent from spec §10**, which calls the shape changes "all additive,
no migration" with no downgrade paragraph: losing `DispatchedCmdIDs` re-opens the 422; losing
`RetryAttempts` resets the retry budget so a poison compensation retries forever (compounded by B4
if the budget is per-walk).
E6 CONFIRMED and REFRAMED: `IncidentKind` (`engine/state.go:117-134`) already exists with exactly two
constants, `IncidentAction` (`iota`, the ZERO value) and `IncidentCompensationStall`. Spec §10 lists
`Incident.Kind = IncidentCompensationFailed` under "Persisted-shape changes", which reads as a new
persisted FIELD; it is a new CONSTANT on an existing field, and therefore not a shape change at all.
FIX: spec §10 gains a short "downgrade" paragraph naming both consequences, and moves the
`Incident.Kind` row out of the persisted-shape list into a "new enum member" note.

### B10 CRITICAL — `Token.AwaitTimer` is dual-WRITTEN at one site and never CLEARED anywhere; the enumeration becomes permanently, silently wrong after the first timer fires
Docs: spec §1 ("⚠ **Dual-write, not a move.** `AwaitCommand` continues to be set … `AwaitTimer` is
written *alongside* it. … leaving that untouched keeps this decision **additive with no dispatch
change**"); ADR-0177; plan P1 step 4 ("write it at the plain-intermediate-catch arm site … ⚠ leave
`AwaitCommand` exactly as it is. Do **not** touch `handleTimerFired`'s path-5 fall-through").
Every document specifies the SET side (one site, `engine/step_nodes.go:831`) and no document
mentions the CLEAR side. Re-derived by grep, `AwaitCommand` is cleared at **seven** production
sites: `step_gateways.go:243`, `step_timers.go:83`, `step_triggers.go:112`, `:376`, `:569`, `:741`,
`:1002`. The plain-ICE resume is `step_triggers.go:569` — inside precisely the path-5 fall-through
the plan forbids touching.
Consequence: the first time a plain intermediate-catch timer fires, `AwaitCommand` is cleared and
the token resumes, but `AwaitTimer` stays set for the rest of the token's life. `TimerWaiters()`
then reports a waiter for a timer that has already fired and whose record was removed
(`s.removeTimer(t.TimerID)` at `:568`), and `HasArmedTimers()` returns `true` forever. That inverts
ADR-0177's entire purpose: `processtest.Classify` returns `ReasonTimer` for a park that has nothing
armed, and `AutoTimers()` fires an id that path 5 then treats as "stale/already-moved: clean no-op"
(`step_triggers.go:545-548`) — a harness that loops until progress spins. The spec's own
justification for the dual-write ("additive, no dispatch change") is exactly what conceals this.
FIX: `AwaitTimer` must be cleared wherever `AwaitCommand` is — introduce one
`func (t *Token) clearAwait()` setting both, and replace all seven sites (a mechanical, testable
change; leaving six of seven is the same rot the bundle's §6 documents). Plan P1 gains a RED test
`TestAwaitTimerIsClearedWhenThePlainIntermediateCatchTimerFires` — RED today because nothing clears
it — and P1 step 8's KNOWN-LIMITATION pin (an absent `AwaitTimer`) must not be mistaken for
covering this: it asserts the field is empty on a snapshot where it was never written, so it cannot
fail for the stale-set case.

### B11 MAJOR — "reusing `retryStalledCompensation` — an existing, tested template" is an unexecuted analogy, and the function does three things ADR-0179 must not do
Docs: ADR-0179 Decision 2 (verbatim "reusing `retryStalledCompensation` — an existing, tested
're-dispatch this record and re-arm' template, driven by a policy instead of an operator verb");
spec §3.2 (same claim); plan P2 step 6 ("re-dispatch modelled on `retryStalledCompensation`").
No document executes it. Source-verified (`engine/step_compensation.go:1273-1303`), the function:
(1) calls `s.retireCompensationStallIncidents(cur.ActiveCmdID)` on BOTH branches — retiring a
`IncidentCompensationStall` a policy retry never raised, and doing nothing about the
`IncidentCompensationFailed` the new decision DOES raise (which by design must survive — so a
policy retry leaves one incident per failed attempt accumulating on the walk, unmentioned anywhere);
(2) arms `armCompensationStallTimer` — a `TimerCompensationStall`, **not** the
`TimerCompensationRetry` kind the ADR requires, so the "reuse" supplies the wrong timer for the one
mechanism the whole decision hangs on;
(3) contains no policy, no attempt counter and no exhaustion branch — its out-of-bounds branch
routes to `stepCompensationFinish`. Every part of Decision 2 that is actually new (backoff, attempt
budget, exhaustion→skip) is outside the template, so "reusing" understates the work and hides B4's
reset question and B3's retirement question.
This is Premise Discipline's named pattern: *"this case is analogous to that one, so it behaves the
same."* It is unlabelled and load-bearing (it is the ADR's argument that the retry is cheap).
FIX: either execute the reuse and record what it actually produces, or replace the sentence with
"a NEW `retryFailedCompensation`, structurally parallel to `retryStalledCompensation` but arming
`TimerCompensationRetry`, retiring no incident, and gated by `CompensationRetryPolicy`" — and say
explicitly whether each failed attempt raises its own `IncidentCompensationFailed` or one is updated.

### B12 MAJOR — plan/spec cross-document divergences (four, all concrete)
(a) **P2's dependency is wrong.** The phase table gives P2 `depends on: —`, and the prose asserts
only "P3 depends on P2". But P1 step 6 *introduces* `TimerKind.walkScoped()` and P2 step 6 says
"**Extend** `walkScoped()` to cover the new kind" — P2 cannot extend what P1 has not created. FIX:
`P2 depends on P1`, or move `walkScoped()`'s introduction into a P0 shared step.
(b) **P4's verify omits `runtime`.** The phase table lists P4's packages as
`engine`, `runtime`, `service`, `transport/http`, and P4 step 6 is an EXECUTE requirement against
`propagateCancel`'s child loop, which lives in `runtime/processdriver_cancel.go:80-88`. P4's Verify
line is `go test -count=1 ./engine/... ./service/... ./transport/http/...` — `runtime` is missing,
so the one step the spec flags as "assume-at-your-peril" (§4b) has no test command behind it. FIX:
add `./runtime/...` and the Docker note (it is not container-free).
(c) **Spec §5 and the plan's "Cross-phase traps" do not match.** §5 has four entries; the plan has
four; only two correspond (§5.1↔trap 1, §5.2↔trap 2). Plan trap 3 (the `StatusRunning`-zero-value
predicate) and trap 4 (incident retirement) appear nowhere in §5, and §5.3/§5.4 (both "no
interaction") appear nowhere in the plan. Since §5 is advertised as "the first thing an auditor
should attack", a reader attacking §5 alone misses half the real traps. FIX: make §5 the single
list and have the plan reference it by number rather than restating a different four.
(d) **P2 step 4 has no runnable home** — see B7(a). Combined with (b), the plan's package columns
and its verify commands disagree in two of five phases.

### B13 MINOR — three failure modes named in the audit brief that no document addresses
- **`TimerWaiters()` on a terminal instance.** Source-verified benign for four of five sources:
  `endInstance` (`engine/state.go:482`) calls `cancelAllScheduledWork`, which nils `Timers`
  (`state_timers.go:128`), `ArmedEvents` (`state_arms.go:147`), `Boundaries` (`:153`) and
  `EventTriggeredSubprocesses` (`:542`). The FIFTH source is not: `endInstance` does **not** clear
  `s.Tokens` (its own comment says only "the two sites that drop tokens do so BEFORE this call"), so
  a terminated instance can retain a token — and with B10 unfixed, one carrying a stale `AwaitTimer`.
  The bundle states no contract for `TimerWaiters()` on a terminal instance. FIX: state it
  ("returns the arms still recorded; a terminal instance has none from the four record sources"),
  and either clear `AwaitTimer` in `endInstance` or fix B10.
- **An instance cancelled while a compensation retry is armed.** `handleCancelRequested`'s dropped
  site (`:210`) — the one ADR-0180 turns into `ErrCancelNotApplicable` — now fires while a retry
  backoff is pending. The operator gets a 409 for a walk that is *not* stalled but merely waiting on
  a backoff, with no way to tell the two apart. FIX: ADR-0180 §4b should say whether a retry-pending
  walk is "not applicable" or should defer; §9.5 should add the case.
- **Two decisions' state changes in one step.** A single `ActionFailed` under a policy now mutates
  `Incidents`, `Compensating.{RetryAttempts,RetryStartedAt,DispatchedCmdIDs,ActiveCmdID}` and
  `s.Timers` in one step, and `Step` must remain non-mutating on its input (`TestStepDoesNotMutateInput`,
  `engine/step_test.go:94`). With B2 unfixed this is the step that trips it. No document sequences
  these mutations. FIX: ADR-0179 should state the order and the plan should assert
  `TestStepDoesNotMutateInput` still passes as an explicit P2 gate.

