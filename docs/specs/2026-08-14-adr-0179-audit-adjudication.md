# Audit adjudication — ADR-0179 (bundle C, the rewrite)

**Date**: 2026-08-17 · **Adjudicated by**: controller session, against
`2026-08-14-adr-0179-audit-lens-{a,b,c}.md` (committed at `c4a4b511`).

Rule #9 requires findings be *adjudicated*, not auto-applied. This is that record. The accepted
fixes are folded into the ADR, spec and plan in the same commit.

⚠ **This is the bundle's SECOND failed audit.** The first refuted the original design (4 Criticals)
and the rewrite was supposed to answer it. The rewrite failed too — ~30 findings, 9 Criticals — and
the most instructive one is that **the rewrite dropped half of a fix the first audit had already
prescribed**. See §4.

## §0 — Owner decisions taken at the fold

| # | decision | consequence |
|---|---|---|
| **D1** | **One bundle**, not split into visibility-first + retry-later | the visibility fix ships behind the harder retry half; churn attribution stays coarse |
| **D2** | **Always raise the incident**, and fix `processtest.Classify` so a walk-scoped incident does not strand a harness; ship a release note | the ADR's "default-off = no behaviour change" claim is **deleted**, not defended |
| **D3** | **Make the retry timer un-prunable** (exclude the kind from `PruneTimers`' reachable set) | ⚠ covers the prune route **only** — see §3 A3, an accepted residual |
| **D4** | **Move the dispatched-id set to `InstanceState`** as a bounded ring | dissolves three findings at once; costs a state-schema field and a documented cap |

## §1 — Dispositions: the nine Criticals

| finding | lens | disposition |
|---|---|---|
| `walkScoped()` extension makes the retry unreachable | **A-F2 + B-B12 (converged independently)** | **ACCEPTED.** Split the predicate into `firesOnDyingInstance()` (ADR-0178's guard) and `detectionOnly()` (`HasArmedTimers`). One boolean was answering two questions: *may this fire on a dying instance?* — retry **yes**, stall **yes**; *is this forward work a harness may fire?* — retry **yes**, stall **no**. Measured: as prescribed, `HasArmedTimers=false`, `Classify.Reason="unknown"`, `AutoTimers fires=false`, so every consumer opting in gets `ErrUnhandledPark`. ⚠ Two lenses reaching this independently is the strongest signal in the audit. |
| "default-off means no behaviour change" is false | B-B2 | **ACCEPTED** — owner decision **D2**. `processtest.Classify` has an incident rung above the timer rung, so one `Incident{TokenID: ""}` flips a park `timer → incident` and `AutoTimers()` stops driving it. Measured on four rows. The gate needs a `processtest`-level test: §4 had **twelve** tests and not one left `engine`/`runtime`. |
| the bundle defers its own central decision | B-B5 + A-F9 | **ACCEPTED, and the decision is now MADE** (§2 A6). §3.9 said "Required: specify the window's state machine explicitly" and never did; §3.3 and §3.9 were in direct contradiction over whether `ActiveCmdID` survives a failure. At least six other decisions hang off it. |
| a false stall incident during the backoff | B-B4 | **ACCEPTED.** Nothing on the failure path cancels the `TimerCompensationStall` record for the failed command, and both of `handleCompensationStallFired`'s guards pass — so the operator is told "compensation action stalled" about an action that already replied. It also opens a `CompensationEscape{Retry}` race against the scheduled retry. Fix: cancel the stall timer for the failed command **before** arming the retry timer, with the ordering stated. |
| "`TestStepDoesNotMutateInput` is the existing gate" | A-F5 | **ACCEPTED — the claim is REFUTED.** Measured: field added, left aliased, that test **passes** and so does the whole `./engine` package. Its fixture builds no compensation cursor and asserts nothing about `Compensating`. ⚠ This was an **inherited-audit fix**, sitting in a bundle whose §4 opens with *"Check the fixture, not the assertion line."* |
| the headline backlog-3g fix covers 1 of 4 cells | A-F6 | **ACCEPTED** — owner decision **D4** resolves it. The cursor is zeroed at both finish sites, so post-finish duplicates on a **resuming** walk still 422 after the ADR; on a terminating walk the 422 is **already benign today** (ADR-0165's guard, measured `err=<nil>`). Moving the set to `InstanceState` covers the post-finish cell, keeps the cursor all-scalar (dissolving the `cloneState` Critical) and bounds it by construction (dissolving §3.8). |
| §4 tests 4 and 12 need opposite fixtures | A-F8 | **ACCEPTED.** Test 4 (retry timer retired at finish) on the natural **cancel** fixture measures `leakedTimerRecords=0` with the fix absent — it **cannot fail**; it needs a **resuming** walk. Test 12 (fires on a dying walk) needs **cancel-started**, because a throw walk measures `SpawnsNewWork=true`. Only the second was stated. ⚠ Also: `cancelCompensationStallTimers` filters in **three** places — both loops *and* an early `if cmds == nil { return nil }` that short-circuits when no stall timer exists, which is the default config. |
| "a compensation walk holds ZERO tokens (measured every frame)" | **C-1** | **ACCEPTED — the universal is FALSE.** A scope-wide throw with a parallel sibling holds **1 token at every frame** while the walk is in flight, and shipped code says so in `startCompensationWalk`'s own comment. The premise-evidence hedged correctly ("Scenarios A, B and D"); the ADR and spec **stripped the hedge**. The design conclusion (attempt state off the token) survives; the premise is rewritten to the closed set. |
| "ADR-0178's guard refuses EVERY compensation retry" | **C-2** | **ACCEPTED — the universal is FALSE.** The guard is `!walkScoped() && !spawnsNewWork()`, and `spawnsNewWork()` is **true** on any resuming walk (measured on all five frames of a throw walk). The `walkScoped()` work is still required — for **terminating** walks only. See §4: this is the finding that indicts the rewrite process itself. |

## §2 — The four decisions the bundle deferred, now made

The rewrite *recorded* four load-bearing decisions and *made* none. An audited bundle is an
implementation input; a decision left blank ships as whatever the implementing agent guesses.

- **A6 — the backoff window state machine.** `ActiveCmdID` **survives** the failure and continues to
  name the failed command until the retry re-dispatches. The cursor gains `RetryAttempts` and
  `RetryTimerID`. A redelivered `ActionFailed` for `ActiveCmdID` is idempotent **because
  `RetryTimerID != ""` means a retry is already armed** — one field, checkable, and it is what
  actually closes §3.9 rather than the duplicate-id set. This keeps §3.3's late-fire check writable
  (it mirrors `handleCompensationStallFired`'s existing guard verbatim), and it is what makes B4's
  cancel-then-arm ordering expressible.
- **A7 — boundedness.** Settled by construction under **D4**: a ring of the last K ids on
  `InstanceState`, K stated in the ADR. The operator-`retry`-verb term is bounded regardless.
- **A8 — the incident's lifecycle.** ⚠ The bundle specified only its *birth*. Neither sweep can ever
  touch it (`retireCompensationStallIncidents` filters on the stall kind; `removeOrphanedIncidents`
  deletes only `TokenID != ""`), `handleResolveIncident` refuses it by whitelist, and no
  `CompensationEscape` verb is gated for it — so as designed it is **immortal, one per failed
  attempt**. Decision: **retire it when the walk advances past the record** (mirroring
  `stepCompensationAdvance`'s existing retirement, keyed on `CommandID`), and keep the **exhaustion**
  incident permanently — that is the durable record the ADR actually wants, bounded at one per
  exhausted record rather than one per attempt. It stays **non-resolvable**, consistent with the
  stall kind, and the ADR says which operator surface clears it (none — it is the record).
- **A9 — cancellation during a backoff.** Documented, with the corrected status code: it is a
  **422** (`ErrInvalidTransition` → `StatusUnprocessableEntity`), not the "409" the bundle
  inherited; and on a cancel-started walk `ErrCancelNotApplicable` is not returned at all, because
  `walkTerminates` gates it.

## §3 — Accepted residuals (recorded, not solved)

- **A3 — a lost retry timer still strands the instance.** Owner decision **D3** makes the kind
  un-prunable, which closes the retention-job route. It does **not** close the other two the audit
  found: a row `jobStore.Load` skips at boot, and a timer that is never rehydrated at all. Between
  the failure and the fire the walk makes no forward progress and holds no token, and exhaustion is
  reachable only *by the timer firing* — so those routes still break the ADR-0034 property this ADR
  claims to preserve. ⚠ Recorded in the ADR as an **accepted cost with a named escape** (ADR-0175's
  operator verbs) and queued as backlog. It is not presented as fixed.
- **A4 — the `IncidentKind` downgrade term.** An old build drops `Kind`, degrading the new incident
  into a **resolvable, deletable** `IncidentAction` which the cause-of-death filter then also fails
  to exclude. Severity is *higher* than for the stall kind because this incident is deliberately
  long-lived. Documented in the ADR's downgrade bullet; the general fix belongs to backlog 32.

## §4 — What this audit says about the process

1. **The rewrite dropped half of a fix its own first audit prescribed.** Lens A of the inherited
   audit explicitly demanded that *"the sentence justifying it is a false universal and must be
   rewritten to name the closed set"*. The rewrite took the **fixture** half of that fix and dropped
   the **quantifier** half — then restated the universal in four places, while §4 test 12 of the same
   document contradicts it. A correction pass is not automatically a correct pass.
2. **Two independent lenses converging is worth more than either lens's confidence.** A and B reached
   the `walkScoped()` Critical by different routes (A by executing a park classification, B by
   reasoning about the two consumers). Neither cited the other.
3. **The counting lens again found what the other two could not** — and this time it *refuted a
   premise the other two had partly accepted*. Its value is not redundancy; it is a different
   failure mode.
4. **A bundle two merges stale is a different document.** Every line citation had drifted ~+46 lines,
   and the plan's operative instruction — "edit at or above `step_triggers.go:292`" — now points
   into `handleCancelRequested`, the wrong function. Rebasing before auditing is what made this
   visible; the rule going forward is **symbols, never line numbers**.
