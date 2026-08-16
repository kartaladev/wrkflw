# Plan — A failed compensation action is retried, and visible (ADR-0179)

> Spec: [`docs/specs/2026-08-13-compensation-failure-retry-and-visibility.md`](../specs/2026-08-13-compensation-failure-retry-and-visibility.md)
> ADR: 0179 · Evidence: `docs/specs/2026-08-13-adr-0179-premise-evidence.md`
> First audit: `…-adr-0179-inherited-audit-lens-{a,b,c}.md`
> **Second audit: `docs/specs/2026-08-14-adr-0179-audit-lens-{a,b,c}.md`**
> **Adjudication (read this first): `docs/specs/2026-08-14-adr-0179-audit-adjudication.md`**

## ▶ Progress

- **Branch**: `feat/compensation-failure-retry-and-visibility`, rebased onto `main` at the
  ADR-0181/0182 merge `1ac140f6`, then onto `63838d0e`. ⚠ Do not quote the branch's SHA — amends move it.
- **State**: **IMPLEMENTED 2026-08-17**. All phases landed. Remaining: the owner-invoked delivery
  gate (`/code-review`, `/security-review`), then merge `--no-ff` + push.
- **Phases**: P1 `engine` (5 serial steps A–E) · P2 `runtime` · **P2b `runtime` (NEW, see below)** ·
  P3 `processtest` (+ a fix round) · P4 `internal/persistence/store` · P5 doc sweep.

### What implementation changed in the design (rule #11)

The audited bundle was wrong in six ways that only became visible once the code existed. Each is
amended **in the ADR**, with the measurement:

1. ⚠⚠ **Decision 2's opt-in was UNREACHABLE.** `engine.StepOptions.CompensationRetryPolicy` had
   **zero** hits in `runtime/`; `ProcessDriver` never set it and no `runtime.Option` existed. The
   feature would have shipped promised-but-unusable for every consumer using the driver — the
   ADR-0162 zombie-scope failure. Closed in-bundle as **P2b**:
   `runtime.WithCompensationRetryPolicy`. ⚠ **Two audits and six lenses missed this because it
   exists only in the seam BETWEEN two packages, and every lens read one design.**
2. ⚠⚠ **Decision 6's literal wording DELETED THE INCIDENT AT BIRTH.** Built as a mutation and
   measured: `incidents=0`, four test functions red including the headline visibility test.
3. **Decision 5's count was wrong** — two reply-path short-circuits, not three.
4. **The `processtest` predicate needed a third disjunct** (`|| (len(Incidents) > 0 &&
   !HasArmedTimers)`), or the ADR-0175 **stall** park regressed `incident → unknown`, retracting
   ADR-0175's own consequence (c).
5. **D3 has a flip side**: no bulk sweep can now delete a retry row at all, so a leaked one is
   permanent.
6. **The breaking surface is three items, not one** — all failing silently, no compile error.

### Delivery gate — `/code-review` round (owner-invoked, 2026-08-17)

**Six findings, one HIGH.** Eleventh consecutive delivery where the real gate found what adversarial
stand-ins missed. Fixed: 1, 2+3, 5, plus one the fixer surfaced. Adjudicated out of scope with
reasons: 4 and 6.

- **HIGH — `retryStalledCompensation` wedged the walk permanently.** ADR-0175's operator `retry`
  verb, used *during* an ADR-0179 backoff, wrote a fresh `ActiveCmdID` but left `RetryTimerID`
  naming a record `cancelCompensationWalkTimers` had already removed. The next `ActionFailed` then
  hit the new idempotency guard, was read as a redelivery, and the instance sat in
  `StatusCompensating` forever with nothing armed. Reachable through the exact escape this bundle's
  own docs prescribe for a lost retry timer.
  ⚠ **Root cause: the guard was built on `RetryTimerID` without enumerating the writers of the field
  it keys on (`ActiveCmdID`).** Five writers; four were already correct. The ADR now carries that
  table and states the reset as a property **of the field**, not of `stepCompensationAdvance` — that
  framing is *why* only one writer got checked.
- **Same omission, second instance**: `retryStalledCompensation` also retired only *stall*
  incidents, so the operator verb accumulated one `IncidentCompensationFailed` per invocation
  (measured: three open after two retries), against Decision 6's bound of one.
- **MEDIUM ×2 → one fix, and NOT either fix the reviewer proposed.** Both findings patched a rung
  whose *position* was the defect. `Classify`'s incident rung is now **split by scope**: token-scoped
  stays high, walk-scoped drops to just above `ReasonUnknown`, and the `HasArmedTimers` yield term is
  deleted as dead weight. ⚠ This **retracts** an accepted residual this bundle had shipped (a
  walk-scoped incident outranking a signal park) and also changes the **pre-existing** ADR-0175 stall
  kind — see `CHANGELOG.md` breaking item 3, rewritten rather than removed.

⭐ **The generalisation worth keeping.** `skip` and `abandon` were verified clean against a live
backoff — and the reason is the lesson. They own **no bespoke cursor logic**; they inherit it from
`stepCompensationAdvance` and `stepCompensationFinish`, both of which this ADR revised.
`retryStalledCompensation` is the **only** verb path with bespoke handling and the **only** one this
ADR never touched. Both defects above are one omission seen twice.
⚠ **When a feature revises shared helpers, the sites at risk are the ones that BYPASS those helpers
with bespoke logic. Enumerate the bypassers, not the callers.**

⚠ **Disclosed test-sensitivity limit** (accepted, not silently taken): the new `abandon` regression
row's assertions *can* fail, but **no single-site mutation reddens them** — the terminate path
guarantees both properties twice (`stepCompensationFinish` then `endInstance`/`cancelAllTimers`),
sequentially, so the first consumes the work. Only the paired mutations C+D and B+E go red. Forcing
single-site sensitivity would mean asserting unreachable intermediate state or deleting a guarantor;
the redundancy is genuine defence in depth. The full mutation matrix is recorded verbatim in the
test's own comment. The `skip` row **is** a single-site pin.

### Plan defects found during execution — fix these before reusing this plan

- **P1 step 10's "filters in THREE places — all three must change" is HALF FALSE.** Two kind
  comparisons plus a **derived** `cmds == nil` short-circuit that needs no edit. The warning is
  sound; the instruction sends someone hunting an edit that does not exist.
- **Steps 13, 14, 15 and step 12's second half needed NO production code** — measured already true.
  They ship as mutation-verified **controls**. The plan presents them as work.
- **Step 11 (exhaustion) was folded into P1-D** by the controller: it is the else-branch of the
  budget check, and step 9's per-record test cannot be written without it.
- ⚠ **A bare `grep -rn compensationInvoke engine/` returns ~29 lines** (test helper
  `compensationInvokeNames`). Anchor on `compensationInvoke(` and exclude `_test.go`.

### Enumeration scorecard — the bundle's signature defect, quantified

**Seven inherited counts were re-derived during implementation; five were WRONG.** Three of the
five were errors in the controller's own briefs, not in the audited documents — including "all four
`TimerKind` kinds" when there are six. The two that survived: the ADR's "this adds a fifth
`compensationInvoke` site", and P4's entire claim sheet.
⚠ **Four consecutive steps found a PRIOR step's test text encoding a claim the later step refutes**
— e.g. P1-D pinned `assert.Len(Incidents, 2, "one per failed dispatch, both kept")`, exactly the
bound Decision 6 refuses. Corrected to 1.

### Verification status — COMPLETE (2026-08-17, Docker up)

All three Verification items pass, on the full tree:

1. `go test -race -coverprofile=cover.out ./...` → **EXIT=0, 64 packages ok, 0 FAIL**.
   `scripts/coverage.sh` total **74.8 %** (repo-wide, excludes generated — pre-existing, backlog 20).
   **Touched packages**: `engine` **93.0 %**, `runtime` **93.7 %**, `internal/persistence/store`
   **87.6 %**, `processtest` **91.5 %** — all over the 85 % floor.
   ⚠ `persistence` **84.1 %**, under the floor — **pre-existing** (backlog 34) and untouched by this
   delivery except one doc comment, which cannot move coverage. Not introduced here, not fixed here.
2. Repo-root `go test ./...` — no regressions (covered by the `-race` run above).
3. `golangci-lint run ./...` **repo-wide** → **EXIT=0, "0 issues"** (v2.12.2, probed on `PATH`).

This supersedes the earlier partial result: the Postgres/MySQL path for the new `PruneTimers`
predicate and the container-backed `runtime` tests, both unrun during implementation, are now
covered — `internal/persistence/store` ran 60.2 s and `runtime` 24.3 s against real containers.

- ⚠ **Accepted residuals, do NOT present as fixed**: a retry timer lost at boot (skipped by
  `jobStore.Load`, or never rehydrated) still strands the walk — un-prunability closes only the
  retention route; and a leaked retry row is now permanent (defect 5 above).

## ⚠ Gate before implementation

This bundle has failed two audits. Read the adjudication record before writing code — in
particular §2, which contains **four decisions the previous text left blank** and this one makes.

## Execution order

Five phases across **five Go packages**. ⚠ Fan out **by package** — concurrent agents inside one
package break each other's `go test` compile even on disjoint files. **P1 is one package and runs
strictly serial within itself.**

| phase | package | depends on |
|---|---|---|
| P1 | `engine` | — |
| P2 | `runtime` | P1 (new `IncidentKind`) |
| P3 | `processtest` | P1 |
| P4 | `internal/persistence/store` | P1 (new `TimerKind`) |
| P5 | doc-only: `engine`, `runtime` | P1 |

**Wave 1**: P1 alone. **Wave 2**: P2 ‖ P3 ‖ P4. **Wave 3**: P5, inline.

⚠ **Docker**: P4 needs none — `dbtest.RunTestSQLite` is pure-Go. `engine` and `processtest` are
container-free. ⚠ **`./runtime/...` as a whole is NOT** — P2 must scope its runs with `-run`.

## P1 — `engine`

Serial. Every step is RED first, with an observable failing run in the transcript.

1. **Visibility.** `IncidentCompensationFailed` + a `slog.WarnContext`, raised at
   **`handleActionFailed`'s** `s.Status == StatusCompensating && ActiveCmdID == t.CommandID`
   short-circuit. **Fails today**: measured `incidents=0 timers=0`, and `grep -c slog` = 0 over both
   `handleActionFailed` and `stepCompensationAdvance`.
2. **The predicate split.** Replace `walkScoped()` with **`firesOnDyingInstance()`** (used by
   ADR-0178's guard) and **`detectionOnly()`** (used by `HasArmedTimers`).
   `TimerCompensationStall` → true/true; `TimerCompensationRetry` → true/**false**.
   ⚠ **Mandatory control test**: `HasArmedTimers() == true` during a compensation-retry backoff.
   Without it the split is untested in the direction that matters — and the pre-fold design got
   exactly this backwards, measured as `AutoTimers fires=false`.
3. **The dispatched-id ring.** `InstanceState.RecentCompensationCmdIDs`, a bounded ring of the last
   **K = 16**, appended at **every** `compensationInvoke` site, **never** cleared at walk finish.
   ⚠ `cloneState` must deep-copy it — the one-liner sits beside
   `s.DeferredCompensationThrows = append([]string(nil), …)`.
   ⚠ **`TestStepDoesNotMutateInput` is NOT a gate for this.** Measured: with the field added and
   left aliased, that test **passes** and so does the entire `./engine` package — its fixture builds
   no compensation cursor. The new `TestCloneStateDeepCopies…` is the **only** gate and must be
   mutation-verified (drop the copy line, observe RED). The pre-fold plan asserted the opposite.
4. **The duplicate predicate, on BOTH reply paths** — `handleActionCompleted` **and**
   `handleActionFailed`. There are three structurally identical `StatusCompensating && ActiveCmdID`
   short-circuits in `step_triggers.go`; the pre-fold text named one.
   ⚠ Assert **forward progress**, not just the absence of an error: the naive membership test makes
   every normal reply a duplicate and the walk never advances.
   ⚠ Fixture: use a **mid-walk** superseded reply. Measured, a post-finish **cancel**-walk fixture
   returns `err=<nil>` today (ADR-0165's terminal guard) and **cannot fail**.
5. **`TestDispatchedCmdIDsAreDerivedFromEverySite`** — the test *derives* the site set by inspection
   rather than hard-coding a count. Today there are **four** `compensationInvoke` sites; this ADR
   makes **five**. Any literal is stale on arrival.
6. **The backoff state machine** (ADR Decision 3). `ActiveCmdID` survives the failure; cursor gains
   `RetryAttempts` (per record, zeroed on `NextIndex` advance) and `RetryTimerID`. A redelivered
   `ActionFailed` for `ActiveCmdID` is idempotent **because `RetryTimerID != ""`**.
   Test: `TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm`.
7. **Cancel the stall timer for the failed command BEFORE arming the retry timer.**
   ⚠ Ordering is load-bearing — widening the sweep to every walk-scoped kind makes cancel-then-arm
   self-cancelling if written the wrong way round.
   Test: `TestNoStallIncidentDuringACompensationRetryBackoff`. **Fails today** against the naive
   design: the stall record survives with a matching `CommandID`, so both of
   `handleCompensationStallFired`'s guards pass and it raises a **false** "stalled" incident.
8. **`StepOptions.CompensationRetryPolicy`**, `TimerCompensationRetry`, `retryFailedCompensation`,
   re-dispatch under a **fresh** command id. ⚠ **Do not reuse `retryStalledCompensation`** — wrong
   incident, wrong timer kind, no policy/counter/exhaustion branch.
9. **Per-record budget**: two failing records, each with its own attempts.
10. **The retry timer is retired at walk finish.** ⚠ **Use a RESUMING (compensation-throw) walk.**
    Measured: on a TERMINATE finish `endInstance`'s `cancelAllTimers` sweeps every record regardless
    of kind, so a cancel-walk fixture measures `leakedTimerRecords=0` **with the fix absent** and
    cannot fail. On a RESUME finish it leaks 2.
    ⚠ `cancelCompensationStallTimers` filters in **three** places — the emit loop, the rebuild loop,
    **and an early `if cmds == nil { return nil }` between them** that short-circuits when no stall
    timer exists, which is the default configuration. All three must change.
11. **Exhaustion skips and continues**; the walk does not park.
12. **Incident lifecycle** (ADR Decision 6): retired when the walk advances past the record; the
    **exhaustion** incident is kept. Test both directions, and that it is **not** resolvable.
13. **The exhaustion incident survives `endInstance`'s two sweeps.**
14. **A compensation retry timer fires on a dying walk.** ⚠ **Use a CANCEL-STARTED walk** — a throw
    walk measures `SpawnsNewWork=true`, so the ADR-0178 guard is not the thing under test.
    ⚠ Opposite fixture to step 10; the pre-fold plan stated the constraint for only one of them.
15. **Late reply to a superseded *retry* command**, both reply kinds. ⚠ The pre-fold plan **dropped
    this test entirely** while spec §4 still listed it — Decision item 4 would have shipped untested.

**Verify**: `go test -count=1 ./engine/ ; echo "EXIT=$?"` (container-free).

## P2 — `runtime`

1. **RED** — `TestCompensationFailedIncidentIsNotPublishedAsCauseOfDeath`, at **both** sites.
   ⚠ Assert the **replacement** string at each site, not merely the absence of the compensation
   error — otherwise the test cannot tell "filtered correctly" from "filtered and lost the real
   cause".
2. **GREEN** — one shared **allow-list** helper (publish only `IncidentAction`) used by
   `terminalEventErr` (`runtime/outbox.go`) and `terminalErr` (`runtime/processdriver_action.go`).
   ⚠ It must exclude **`IncidentCompensationStall` too** — the positional-`Incidents[0]` defect
   ships **today** for that kind, and a fix that covers only the new kind leaves the identical bug
   beside it.
   ⚠ The two resolvers **diverge** after filtering: `terminalEventErr` falls through to the
   `FailInstance{Err}` scan, `terminalErr` does not. That is intended; assert both.

**Verify**: `go test -count=1 -run 'TestCompensationFailed|TestTerminal' -v ./runtime/ ; echo "EXIT=$?"`
⚠ Confirm `=== RUN` lines — a `-run` filter on a nonexistent name exits 0.

## P3 — `processtest`

1. **RED** — a park carrying a walk-scoped `IncidentCompensationFailed` must remain drivable, and a
   compensation-retry backoff must be drivable by the shipped handlers.
   **Fails today**: measured, one `Incident{TokenID: ""}` flips `Classify` `timer → incident`
   (the incident rung sits above the timer rung), and `AutoTimers()` acts only on `ReasonTimer`, so
   `drive` reports `ErrUnhandledPark`.
2. **GREEN** — a rung/handler for a compensation-failure park. ⚠ `harnessEnv.classify`'s promotion
   escape hatch does **not** help: it iterates `state.Tokens`, and a walk holds no token of its own.

**Verify**: `go test -count=1 ./processtest/ ; echo "EXIT=$?"` (container-free).

## P4 — `internal/persistence/store`

1. **RED** — a `TimerCompensationRetry` row with an expired `next_run` **survives**
   `PruneTimers(cutoff)`; a control row of another kind is still deleted.
   **Fails today**: the retry timer is armed with `schedule.AfterDuration`, which is `KindOneTime`
   and therefore inside `nonRecurringTriggerKinds` — an ordinary retention job deletes it.
2. **GREEN** — exclude the kind in `PruneTimers`. ⚠ The `kind` column carries `engine.TimerKind`
   (verified: `timerstore.go` scans it as `engine.TimerKind(kind)`, and this package already imports
   `engine`), so the exclusion is on **`kind`**, not on `trigger_kind`.
   ⚠ Do **not** touch `ReclaimNeverDueTimers`' predicate (ADR-0181) — different sweep, different
   clause; the two must stay disjoint.
3. Use `dbtest.RunTestSQLite` **directly**, not `forEachDialect` (which boots Postgres and MySQL).

**Verify**: `go test -count=1 -run '^TestPruneTimersSparesCompensationRetry$' -v ./internal/persistence/store/ ; echo "EXIT=$?"`

## P5 — doc-only

1. `engine/state_timer_waiters.go` — `walkScoped()`'s comment asserts **"ADR-0179's
   compensation-retry timer is the next such kind."** That shipped to `main` with ADR-0177/0178 and
   is now **measured wrong**; the predicate is being split precisely because the retry is *not* such
   a kind. Rewrite both new predicates' comments.
2. `engine/command.go` — `ScheduleTimer.Kind`'s comment omits `TimerCompensationStall`; add **both**
   missing kinds.

**Verify**: `go build ./... && golangci-lint run ./engine/... ./runtime/...`

## Verification checklist

- [ ] Observable **RED** in the transcript before every new symbol.
- [ ] Mutation-verify the five load-bearing tests: the `cloneState` copy (P1.3 — the *only* gate),
      the `HasArmedTimers` control (P1.2), the stall-cancel ordering (P1.7), the retry-timer
      retirement on a **resuming** walk (P1.10), and the prune exclusion (P4.1).
      ⚠ Restore from a `cp` backup — **never `git checkout <path>`**.
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥ 85 % on
      touched packages, hot paths first. Docker: probe and run.
- [ ] `go test ./...` repo-root — no regressions.
- [ ] `golangci-lint run ./...` **repo-wide** — clean.
- [ ] **Release note**: the always-on incident is a **breaking change for `processtest` consumers**;
      a new incident kind appears in the instance document; an instance cancelled during a backoff
      gets a **422**.
- [ ] Documents describe what shipped; amend in-bundle anything implementation refutes.
- [ ] `HANDOVER.md` rewritten in place; this `▶ Progress` updated; auto-memory updated.
- [ ] **PAUSE** — owner runs `/code-review` and `/security-review`; fold via `--amend`; re-run on the
      merged tree; merge `--no-ff` and push.

## Traps

1. **Extending `walkScoped()` instead of splitting it** → `HasArmedTimers=false`, `AutoTimers` will
   not drive the backoff, every opted-in consumer gets `ErrUnhandledPark`. P1.2.
2. **The active command id inside the dispatched set** → every reply a duplicate → hung walk. P1.4.
3. **The id set on the cursor** → zeroed at finish → covers 1 of 4 duplicate cells. P1.3.
4. **Trusting `TestStepDoesNotMutateInput`** → measured, it cannot observe the aliasing. P1.3.
5. **The retry timer not retired** → fires against a zeroed cursor → ADR-0171's panic shape. P1.10.
6. **A cancel-walk fixture for P1.10** (cannot fail) or **a throw-walk fixture for P1.14** (guard not
   under test). Opposite fixtures, both required.
7. **Arming the retry before cancelling the stall timer** → false "stalled" incident and a
   `CompensationEscape{Retry}` race. P1.7.
8. **Filtering only the new kind in P2** → leaves the identical live bug for the stall kind.
9. **A hard-coded dispatch-site count** — four today, five after this. Derive it. P1.5.
10. **Assuming un-prunability fixes stranding** — it closes the retention route only.
