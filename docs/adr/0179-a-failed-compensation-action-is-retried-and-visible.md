# 179. A failed compensation action is retried, and always visible

- Status: **Accepted — implemented 2026-08-17**, pending the delivery gate (`/code-review` and
  `/security-review` are owner-invoked). **Rewritten twice and then amended by implementation.** Its
  first rule-#9 audit refuted it (4 Criticals); the rewrite **also failed** (2026-08-14, 3 lenses,
  ~30 findings, 9 Criticals); implementation then found more — see the ⚠ AMENDED / ADDED AT
  IMPLEMENTATION blocks below, each carrying the measurement that forced it.
- Date: 2026-08-13, re-folded 2026-08-17, amended at implementation 2026-08-17

> ⚠ **What implementation changed, at a glance** (rule #11: an ADR that ships promising behaviour
> nobody built is the failure this repo has already had once):
> - **Decision 2** — the opt-in was **unreachable** through `runtime.ProcessDriver`. Closed in-bundle.
> - **Decision 5** — its short-circuit count was wrong (two reply paths, not three).
> - **Decision 6** — its literal wording **deleted the incident at birth**; measured, not argued.
> - **Negative** — three additions: the breaking surface is three items not one, D3 makes a leaked
>   retry row permanent, and the in-repo `engine` test fallout this ADR documented only for
>   `processtest`.

> Design and every measurement:
> [`docs/specs/2026-08-13-compensation-failure-retry-and-visibility.md`](../specs/2026-08-13-compensation-failure-retry-and-visibility.md).
> Premise evidence: `docs/specs/2026-08-13-adr-0179-premise-evidence.md`.
> First audit: `docs/specs/2026-08-13-adr-0179-inherited-audit-lens-{a,b,c}.md`.
> **Second audit: `docs/specs/2026-08-14-adr-0179-audit-lens-{a,b,c}.md`.**
> **Adjudication (read this first): `docs/specs/2026-08-14-adr-0179-audit-adjudication.md`.**
> Plan: [`docs/plans/2026-08-13-compensation-failure-retry-and-visibility.md`](../plans/2026-08-13-compensation-failure-retry-and-visibility.md).
>
> Closes backlog **16** and **3g**. **Extends ADR-0034 Decision 4**; does not reverse it.
> Corrects a false claim in ADR-0034's own Consequences.
>
> ⚠ **Navigate by symbol, never by line number.** Every `file:line` in the pre-fold bundle had
> drifted ~+46 lines, and the plan's operative instruction pointed into the wrong function.

## Context

ADR-0034 Decision 4, verbatim:

> **Best-effort compensation:** an `ActionFailed` matching the cursor's `ActiveCmdID` while
> `StatusCompensating` routes to advance (skip+continue), never back into `propagateError`/retry.

Executed with `DefaultRetryPolicy{MaxAttempts: 5}` in effect: `status=compensating incidents=0
timers=0 redispatchedSame=false` — skip and advance, nothing else. The mechanism is the
short-circuit in **`handleActionFailed`** on `s.Status == StatusCompensating &&
s.Compensating.ActiveCmdID == t.CommandID`, which returns before `effectiveRetryPolicy` and
`propagateError` are reached. Any retry feature is an edit at or above that short-circuit.

⚠ **ADR-0034's Consequences claim is false**: it says the failure is "logged/skipped"; `grep -c
slog` over `stepCompensationAdvance` and `handleActionFailed` returns **0** on both, re-derived at
current `main`. A failed compensation action is invisible in all three channels — no retry, no
incident, **no log line**.

⚠ **Corrected premise (second audit, C-1).** The bundle previously asserted *"a compensation walk
holds zero tokens, measured every frame"*. That universal is **false**: a scope-wide throw with a
parallel sibling holds **one** token at every frame while the walk is in flight, and
`startCompensationWalk`'s own comment says so ("sibling branches keep running while the walk is in
flight"). The premise evidence hedged this correctly and the hedge was stripped on restatement. The
closed truth: **a walk holds no token of its own** — the walk is not driven by a token, so
per-attempt state cannot hang off one. That is what the design actually needs, and it survives.

## Decision

Extend Decision 4 while preserving its safety property — a failed compensation still never strands
the instance. ⚠ See *Negative* for the one route where that property is **not** fully restored.

1. **Always visible.** A `slog.WarnContext` line and an `Incident{Kind: IncidentCompensationFailed}`
   in ADR-0175's walk-scoped shape (`TokenID: ""`, keyed by `CommandID`). On for everyone.
   ⚠ This is **not** behaviour-neutral, and the ADR no longer claims it is — see *Negative*.

2. **Retried when configured.** `StepOptions.CompensationRetryPolicy *model.RetryPolicy`, default
   `nil` = today's timing. A failed record is re-dispatched under a **fresh** command id after a
   backoff timer of new kind `TimerCompensationRetry`, by a new `retryFailedCompensation`.
   ⚠ **Not** by reusing `retryStalledCompensation`: source-verified, it retires the *wrong*
   incident, arms the *wrong* timer kind, and has no policy, counter or exhaustion branch.

   ⚠ **ADDED AT IMPLEMENTATION (2026-08-17) — the opt-in was unreachable.** As designed, this
   decision named an `engine.StepOptions` field and stopped there. `runtime.ProcessDriver` — the
   standard way a consumer drives this engine — never set it and no `runtime.Option` existed, so the
   opt-in this decision *promises* could not be exercised by any consumer using the driver. That is
   the ADR-0162 zombie-scope failure and it violates this repo's first principle, that every feature
   is reachable through the module-root public API. **Two audits and six audit lenses missed it,
   because it exists only in the seam BETWEEN two packages and every lens was reading one design.**
   Closed in-bundle by `runtime.WithCompensationRetryPolicy(model.RetryPolicy)`, mirroring
   ADR-0175's `WithCompensationStallTimeout` chain (driver field → `engine.StepOptions` literal at
   the `engine.Step` call site). Taken **by value**, so "unset = nil = today's behaviour" is
   structural — a consumer cannot pass an explicit nil.

   **Retry semantics, all measured at implementation:**
   - the budget is **per record**, zeroed when the walk advances to the next record;
   - `MaxAttempts == 0` means **unlimited**, matching the engine's token-retry convention;
   - `MaxElapsed` is **not honoured** — a walk holds no token of its own, so there is no
     per-attempt start timestamp to measure against;
   - the backoff is **not jittered**, deliberately diverging from the token retry path. `ActionFailed`
     carries a `JitterFraction` that the token path multiplies into the delay, but it defaults to
     **0**, and `engine.NewActionFailed` is public API — a consumer constructing one directly would
     get a zero delay and an immediate retry. Plain `Backoff` is the safer default here;
   - ⚠ an **unset `MaxInterval` is not "no cap"**: `Normalize()` fills it from `DefaultRetryPolicy()`
     (100s) and `Backoff` caps against it. Measured — a requested 2-minute backoff armed at
     `now+100s`. A consumer sizing a long compensation backoff must set `MaxInterval` explicitly.

3. **The backoff window has an explicit state machine.** ⚠ The previous text named this as
   "required" and left it blank, under at least six other decisions. It is now decided:
   - `ActiveCmdID` **survives** the failure and keeps naming the failed command until the retry
     re-dispatches.
   - The cursor gains `RetryAttempts` (per record, zeroed when `NextIndex` advances) and
     `RetryTimerID`.
   - A redelivered `ActionFailed` for `ActiveCmdID` is **idempotent because `RetryTimerID != ""`
     means a retry is already armed**. That single field, not the dispatched-id set, is what closes
     the redelivery window.

   ⚠ **AMENDED AT THE DELIVERY GATE (`/code-review` finding 1, HIGH).** "Zeroed when `NextIndex`
   advances" is necessary but was **not sufficient**, because it enumerated only the writer that
   moves the cursor. `ActiveCmdID` has **five** writers, and each must be checked against both new
   fields:

   | writer | correct? |
   |---|---|
   | `beginCompensation` (`step_compensation.go`) | ✅ builds from the zero cursor `stepCompensationFinish` leaves behind |
   | `startCompensationWalk` (`step_nodes.go`) | ✅ the caller passes a fresh `compensationCursor{…}` literal |
   | `stepCompensationAdvance` | ✅ zeroes both — the per-record reset this decision named |
   | `retryFailedCompensation` | ✅ clears `RetryTimerID`; keeps `RetryAttempts` **by design** (the engine is consuming the budget, not restarting it) |
   | `retryStalledCompensation` (ADR-0175's `retry` verb) | ❌ **cleared neither** |

   The last one wedges the instance. The verb's own `armCompensationStallTimer` call sweeps every
   walk-scoped timer record — the live backoff included — so the cursor was left naming a record that
   no longer exists; the next genuine `ActionFailed` for the re-dispatched command then hit the
   `RetryTimerID != ""` guard above and was answered as a redelivery. Reproduced end to end:

   ```
   after backoff:        ActiveCmdID="…-c3" RetryTimerID="…-tm1" RetryAttempts=1 timers=1
   after operator retry: ActiveCmdID="…-c4" RetryTimerID="…-tm1" RetryAttempts=1 timers=0
   after 2nd failure:    cmds=[]  incidents=1 (unchanged)  timers=0   ← wedged, StatusCompensating forever
   ```

   Fixed by resetting **both** fields alongside the `ActiveCmdID` write in `retryStalledCompensation`.
   `RetryAttempts` goes to zero rather than being preserved: the operator's retry is a fresh attempt
   at the record, and keeping the count would silently shorten the escape hatch's own budget. Both
   lines are independently mutation-verified.

   ⚠ **The rule this decision should have stated:** the retry cursor is not "reset on advance", it is
   **reset by every writer of `ActiveCmdID` except the engine's own backoff re-dispatch**. Stated as
   a property of one writer, it was checked against one writer.
   - **The stall timer for the failed command is cancelled BEFORE the retry timer is armed.** Its
     job is done — the action replied. ⚠ Ordering is load-bearing: widening
     `cancelCompensationStallTimers` to every walk-scoped kind makes a cancel-then-arm sequence
     self-cancelling if written in the wrong order. Without this, a still-armed stall timer fires
     during a healthy backoff and tells the operator "compensation action stalled" about an action
     that already replied — a visibility *regression* in the ADR whose headline is visibility — and
     it opens a `CompensationEscape{Retry}` race against the scheduled retry.

4. **`walkScoped()` is SPLIT, not extended.** ⚠ Two audit lenses independently measured that
   extending it disables the feature. It is one boolean answering two questions:
   - *may this kind fire on a **dying** instance?* (ADR-0178's guard) — stall **yes**, retry **yes**
     → `firesOnDyingInstance()`
   - *is this **forward work** a harness may fire?* (`HasArmedTimers`) — stall **no**, retry **yes**
     → `detectionOnly()`

   Prescribing `walkScoped() == true` for the retry answers the second question backwards. Measured:
   `HasArmedTimers=false`, `processtest.Classify.Reason="unknown"`, `AutoTimers fires=false` — so
   every consumer who opts into `CompensationRetryPolicy` gets `ErrUnhandledPark`, and the shipped
   path is never exercised by the harness this repo ships. A **control test asserting
   `HasArmedTimers()==true` during a retry backoff** is mandatory, or the split is untested in the
   direction that matters.

   ⚠ The justification previously given for this work was also a **false universal**: "ADR-0178's
   guard refuses *every* compensation retry, so this ADR silently never works." The guard is
   `!walkScoped() && !spawnsNewWork()`, and `spawnsNewWork()` is **true** on any resuming walk. The
   work is required for **terminating** walks.

5. **The dispatched-id set lives on `InstanceState`, not on the cursor.** A **bounded ring of the
   last K = 16** command ids, appended at every `compensationInvoke` site, never cleared at walk
   finish. The cursor is zeroed at both finish sites, so a cursor-resident set covers only one of
   four duplicate cells — and the post-finish **resuming**-walk cell it misses is the likeliest in
   production (an at-least-once worker redelivers after a fast walk has finished). Placing it on
   `InstanceState` also keeps the cursor all-scalar and bounds the operator-`retry` term by
   construction.

   ⚠ The duplicate predicate must land on **both** reply paths — `handleActionCompleted` and
   `handleActionFailed`.

   ⚠ **AMENDED AT IMPLEMENTATION (2026-08-17).** This bullet previously read *"there are three
   structurally identical `StatusCompensating && ActiveCmdID` short-circuits in `step_triggers.go`"*.
   Re-derived by grep at implementation: `step_triggers.go` holds **three occurrences of the
   pattern**, but only **two are reply-path short-circuits** (`== t.CommandID`, in
   `handleActionCompleted` and `handleActionFailed`). The third is `!= ""` — `handleCancelRequested`'s
   deferred-cancel guard, a different predicate serving a different purpose, and it is **not** touched.
   The instruction above ("both reply paths") was correct; only the count sentence was false. Three
   consecutive implementation steps re-derived this independently.

6. **The incident has a lifecycle, not just a birth.** Bounded at one per exhausted record, not one
   per attempt. It stays **non-resolvable** (consistent with `IncidentCompensationStall`, and
   `handleResolveIncident` refuses it by whitelist automatically — verified at implementation: no
   new case was needed).

   ⚠⚠ **AMENDED AT IMPLEMENTATION (2026-08-17) — the previous wording deleted the incident at
   birth.** It read: *"Retired when the walk advances past the record (mirroring
   `stepCompensationAdvance`'s existing retirement, keyed on `CommandID`); the **exhaustion**
   incident is kept permanently."* Those two clauses **contradict each other**: on the failure path
   the incident has just been raised with `CommandID == ActiveCmdID`, so a retirement keyed on
   `ActiveCmdID` at advance time deletes precisely the record that must survive. This was not
   reasoned out — it was **built as a mutation and measured**: implementing the literal wording
   yields `incidents=0`, with four test functions red including the visibility test that is this
   ADR's headline. The feature, implemented exactly as its own ADR specified, produced nothing.

   The mechanism that actually delivers the stated bound, and what ships — **THREE** retirement
   sites, each passing the **superseded** command id and never a kind-wide sweep:
   - `retryFailedCompensation` retires the failure incident for the **OLD** command id as the
     backoff's retry re-dispatches — that attempt is superseded;
   - **`retryStalledCompensation` does the same, for ADR-0175's operator `retry` verb** — it
     re-dispatches the same record under a fresh command id, which is the identical superseding
     event;
   - `handleActionCompleted`'s compensation short-circuit retires it for `ActiveCmdID` — the record
     ultimately **succeeded**, so nothing is left behind;
   - the failure/exhaustion path retires **nothing** → exactly **one** survives per exhausted record.

   ⚠⚠ **AMENDED AT THE DELIVERY GATE — the second bullet was MISSING, and this text is what hid
   it.** The original wording named `retryFailedCompensation` as the only retry-path retirement and
   cited `retryStalledCompensation` merely as the *model for the ordering* ("mirroring how
   `retryStalledCompensation` retires the stall incident … before the cursor is overwritten") — so
   the one function the sentence pointed at was the one function nobody checked for the **failure**
   kind. It retired the stall incident only, because it predates `IncidentCompensationFailed` and
   was never revisited when the kind was added. ADR-0175's verb has **no cap**, so the bound this
   decision states was not merely missed, it was unbounded: measured, **three** open
   `IncidentCompensationFailed` records after two operator retries, one naming each superseded
   command, where this decision promises one.

   ⚠ Not mirrored into `retryStalledCompensation`'s vanished-record-source branch: that branch
   routes to the walk's finish without re-dispatching, so nothing supersedes the attempt and its
   failure record must survive. Mutation-verified: deleting the new retirement, and separately
   moving it *after* the cursor write, each turn both new rows red.

   ⚠ **This is the same defect class as the delivery gate's finding 1, in the same function.**
   `retryStalledCompensation` was written against ADR-0175's state and never re-audited against
   ADR-0179's: finding 1 was the cursor-field half (`RetryAttempts`/`RetryTimerID`), this is the
   incident half. The lesson is the one Decision 3's amendment states — a rule expressed as a
   property of one *writer* gets checked against one writer.
   ⚠ As previously designed it was **immortal**: neither `endInstance` sweep can touch it, no
   `CompensationEscape` verb is gated for it, and `runtime/kernel/lister.go` documents an incident as
   meaning "requires operator intervention via `ResolveIncident`" — which would have been false and
   unactionable for this kind.

7. **The walk never parks.** On exhaustion it skips and continues; the incident remains.

One policy tier only. Rejected: parking on exhaustion (reverses ADR-0034's safety argument);
routing through `propagateError` (routes a token down a flow; a walk has none of its own).

## Consequences

**Positive.** The silent-failure hole closes in all three channels. A compensation that must succeed
can be made to retry without every action implementing it. ADR-0034's "logged" claim becomes true
for the first time.

**Negative / accepted.**

- ⚠ **This is a BREAKING change for `processtest` consumers, with retry off.** An incident is not an
  inert document field: `processtest.Classify` has an incident rung **above** the timer rung, so one
  walk-scoped incident flips a park `timer → incident` and `AutoTimers()` — which acts only on
  `ReasonTimer` — stops driving it, reporting `ErrUnhandledPark`. Measured on four rows. The
  previous claim that "default-off means existing consumers see only new WARN lines" is **deleted**.
  Mitigation: `processtest` gains a rung/handler for a compensation-failure park, and a **release
  note**. The test plan gains a `processtest`-level test — the previous twelve never left
  `engine`/`runtime`.

  ⚠ **CONFIRMED AT IMPLEMENTATION**, not merely inherited: with retry off, an engine-built fixture
  drove the harness to `unhandled park: incident at node "charge"`. (Note the shipped `processtest`
  suite passed **unchanged** after the engine phase — not a refutation, simply evidence that no
  shipped test built this shape. The scenario had to be constructed.)

  ⚠ **The breaking surface is THREE items, not one, and all three fail SILENTLY — no compile error:**
  1. the park flips `timer → incident`, as above;
  2. **a consumer fixture hand-building a bare `engine.Incident{}`** (no `TokenID`, no companion
     `TokenIncident` token) loses its `ReasonIncident` classification. That shape is
     **unreachable from the engine** — source-verified, `raiseIncident`'s single call site always
     passes `tok.ID` — so no *reachable* state regresses; but a hand-built test fixture is not a
     reachable state, and one such fixture existed in this repo's own suite;
  3. ~~a walk-scoped incident with **no armed timer** now outranks a **signal/message** park~~ —
     **RETRACTED AT THE DELIVERY GATE, see the amendment below.** This item described the residual
     cost of the narrow yield first shipped here; the yield is gone and so is the residual.

  ⚠ **AMENDED AT THE DELIVERY GATE (`/code-review` findings 2 and 3).** The rung was first shipped as
  ONE predicate carrying a yield term —
  `hasIncidentToken(tokens) || tokenScopedIncident(incidents) != nil || (len(incidents) > 0 && !HasArmedTimers)`
  — and `/code-review` refuted it two ways, both source-verified and then re-measured here:

  - **The yield term could not agree with the rung it yielded to.** It reads `HasArmedTimers`, but
    the timer rung *additionally* requires `primaryTimerPark(state) || !hasCommandWait(state.Tokens)`.
    For a service task with a timer **boundary** and a token parked on `AwaitCommand`, the incident
    rung was skipped *and* the timer rung declined, so the walk-scoped incident vanished from the
    classification. Measured: `reason=async-child`.
  - **Residual item 3 above was not residual, it was the same defect.** A walk-scoped
    `IncidentCompensationFailed` is never retired on an instance the walk **resumed** (throw-targeted
    or partial rollback), so a healthy `Running` instance carries one permanently — and every
    signal/message/command-wait park on it classified `incident`, which `ResolveIncident` refuses by
    design. Measured before the fix: `reason=incident node="charge"` for a signal park, a message
    park and a command wait alike.

  **The rung is now SPLIT BY SCOPE, and the yield term is deleted** — with the walk-scoped rung last
  there is nothing left to yield to:

  ```
  terminal > human-task > TOKEN-SCOPED incident > signal > message > timer > async-child >
      WALK-SCOPED incident > unknown
  ```

  Rationale: `Park.Reason` names *what the harness must do to unblock the instance*. A walk-scoped
  incident is a record that something failed, not a thing the harness can act on, so it must never
  displace a reason that IS actionable. `Park.Incidents` still reports every incident
  unconditionally, so nothing is hidden.

  ⚠ **The TOKEN-SCOPED half keeps its high position, and that is the forbidden reorder.** Moving it
  below the timer rung would let a genuine token-parked `IncidentAction` be masked by any armed
  timer. Mutation-verified at the gate: swapping the two rungs turns **five** rows red across three
  test functions.

  ⚠ **The walk-scoped rung sits ABOVE `ReasonUnknown`, not below it.** Deleting it outright would
  regress an ADR-0175 **stall** park `incident → unknown` (a stall has no armed timer, since
  `TimerCompensationStall` is `detectionOnly()`), retracting ADR-0175's recorded consequence (c) and
  invalidating its shipped `ReasonIncident` documentation. This constraint survives the split
  unchanged — it is simply now expressed by the rung's *position* instead of by a disjunct.

  Net effect on the breaking surface: items 1 and 2 stand; item 3 is **reversed** — a walk-scoped
  incident now yields to signal, message, timer and command-wait parks, so a `Reason`-switching
  consumer handler sees *fewer* `incident` classifications than before ADR-0179, not more.
- ⚠ **A lost retry timer still strands the instance — an accepted residual, not a solved problem.**
  Between the failure and the fire the walk makes no forward progress and holds no token of its own,
  and exhaustion is reachable only *by the timer firing*. Owner decision: make
  `TimerCompensationRetry` **un-prunable**, which closes the retention-job route (`AfterDuration` is
  `KindOneTime`, which `PruneTimers` deletes). It does **not** close a row skipped by
  `jobStore.Load` at boot, nor a timer never rehydrated at all. Those routes remain, the escape is
  ADR-0175's operator verbs, and this is queued as backlog rather than presented as fixed.

  ⚠ **ADDED AT IMPLEMENTATION — D3 has a flip side this ADR did not state.** With `PruneTimers` now
  excluding the kind, and `ReclaimNeverDueTimers` matching only **recurring** `trigger_kind` while a
  retry row is `KindOneTime`, **no bulk sweep can delete a `TimerCompensationRetry` row at all**.
  Only the targeted `DeleteJob` the engine drives via `CancelTimer` can. For a live walk that is
  precisely the intent. But a row **leaked** by an instance that died without the walk-finish sweep
  running is now **permanent** — correctness is preserved and housekeeping regresses into unbounded
  accumulation in a failure mode. Verified by reading both predicates. Queued as backlog beside the
  stranding residual above; not presented as fixed.
- ⚠ **ADDED AT IMPLEMENTATION — the in-repo test fallout, which this ADR documented for `processtest`
  and missed for `engine`.** Two shipped `engine` tests carried assertions that were *over-reaching*
  rather than wrong, and the new always-on incident exposed them:
  `TestStallIncidentRetiredWhenWalkMovesOn` asserted `Incidents` was **empty** after a walk moved on,
  conflating "the stall was retired" (what it tests) with "nothing else may ever be recorded here".
  Narrowed to "no `IncidentCompensationStall` survives", with the `ActionCompleted` row keeping full
  emptiness and the `ActionFailed` row **gaining** a positive assertion — net stronger, not weaker.
  ⚠ The general lesson is the one this bundle keeps re-learning: *an assertion broader than the
  behaviour it names becomes a false claim the moment anything legitimate is added beside it.*

- ⚠ **The cause-of-death defect is PRE-EXISTING and wider than this ADR.** `terminalEventErr` and
  `terminalErr` read `st.Incidents[0].Error` positionally, so a walk-scoped incident at index 0 wins
  over the real cause — measured live today for `IncidentCompensationStall`, and over a genuine
  `IncidentAction` at index 1. The filter must therefore be an **allow-list** (publish only
  `IncidentAction`) covering the stall kind too, or the delivery ships a fix beside the identical
  bug. ⚠ The two resolvers then **diverge**: `terminalEventErr` falls through to the
  `FailInstance{Err}` scan, `terminalErr` has no such scan. That divergence is intended and stated.
  ⚠ `processtest/park.go` is a **third** positional reader, in a shipped root package.
- ⚠ **Downgrade hazard, three terms.** Persistence has no `DisallowUnknownFields`, so an older build
  drops `RecentCompensationCmdIDs` (re-opening the 422), drops `RetryAttempts` (**resetting the
  budget, so a poison compensation retries forever**), and drops `IncidentKind` — degrading this
  incident into a *resolvable, deletable* `IncidentAction` that the cause-of-death allow-list then
  also fails to exclude. Severity is higher than for the stall kind because this incident is
  deliberately long-lived. The general fix is backlog 32.
- ADR-0034's post-acceptance fix forbids retaining records past a full-rollback finish, so retry
  re-dispatches **from the cursor**, never by keeping records alive.
- An instance cancelled during a retry backoff receives a **422** (`ErrInvalidTransition` →
  `StatusUnprocessableEntity`) — ⚠ not the "409" the previous text inherited; and on a cancel-started
  walk `ErrCancelNotApplicable` is not returned at all, because `walkTerminates` gates it.
- The retry budget is **per record**, zeroed when `NextIndex` advances — otherwise the first poison
  record exhausts it for every record after it.
- This ADR adds a **fifth** `compensationInvoke` site. Re-derived at current `main`: there are
  exactly **four** today (`step_compensation.go` ×3, `step_nodes.go` ×1), pairwise adjacent to the
  four `ActiveCmdID` assignments. Any hard-coded "four" in a test is stale on arrival; the test
  derives the set.
