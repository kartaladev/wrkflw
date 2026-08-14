# Plan — A never-due gate and orphan reclamation (ADR-0181, ADR-0182)

> Spec: [`docs/specs/2026-08-13-never-due-gate-and-orphan-reclamation.md`](../specs/2026-08-13-never-due-gate-and-orphan-reclamation.md)
> ADRs: 0181, 0182 · Evidence: `docs/specs/2026-08-13-adr-0181-0182-premise-evidence.md`
> Audit: `…-audit-lens-{a,b,c}.md` · **Adjudication: `…-audit-adjudication.md`**

## ▶ Progress

- **Branch**: `feat/never-due-gate-and-orphan-reclamation`, rebased onto `main` at the
  ADR-0177/0178/0180 merge. One commit, unpushed. ⚠ Do not quote its SHA — the `--amend` moves it.
- **State**: design folded, fully implemented, **`/code-review` passed (3 findings, all folded)**
  and **`/security-review` passed with 0 findings**, 2026-08-14. Both Delivery Gate reviews are
  done. **Remaining: `git merge --no-ff` to `main` and push.**

### `/security-review` — 0 findings

Traced: the new `DELETE` is a compile-time literal with all 8 positions bound (`timeArg` + seven
`schedule.Kind` package constants), the same construction shape as the adjacent `PruneTimers` — a
pattern match, not a deviation. `ReclaimNeverDueTimers` has **no call site outside tests**, so no
`transport/`, `service/` or `engine/` path reaches the destructive sweep; a consumer must
deliberately type-assert and invoke it. The sweep is repo-wide, matching every sibling `Prune*`
(no scoping regression). The validation rule only **appends** to `errs` — fail-closed, weakening
nothing — and its message is a fixed sentinel plus `n.ID()`, interpolating no trigger values,
process variables or connection details. `NeverDue` is a pure predicate over decoded fields.
Nothing new is logged or persisted; the `runtime`/`scheduler` hunks are comment-only.

### `/code-review` — 3 findings, all accepted and folded

1. **MEDIUM — the destructive `DELETE` had zero coverage on Postgres**, the primary production
   backend and the one where the orphan population actually lives. The test file asserted SQLite was
   "the only backend that can hold the fixture at all"; the reviewer **measured the opposite** on a
   real PG17 container (`next_run` is `TIMESTAMPTZ`, no `CHECK`, range back to 4713 BC). ⚠ That
   false comment **contradicted this bundle's own spec §2.2**, which quotes ADR-0176's
   "postgres accepted" — and it argued against ever adding the coverage. Fixed:
   `TestPrunerReclaimNeverDueTimersPostgres`, mutation-verified to discriminate (widening the
   IN-list kills `control-suboneshot` there too). ✅ **This also discharges the bundle's last open
   `ASSUMPTION`** — Postgres is now measured, not assumed.
2. **LOW — the public doc said "no-op on MySQL" unconditionally** while the internal doc explained
   the threshold was chosen partly to sit *above* a non-strict MySQL's coerced `'0000-00-00'`, i.e.
   such rows **are** deleted. An operator reading only the exported doc would skip reviewing a
   destructive call. Qualified in the public doc, the ADR and the spec.
3. **LOW — `runtime/jobstore.go`'s rewritten comment justified only one of the two skip reasons.**
   A row skipped as *unconvertible* (`KindUnset`/`KindExpr`) is non-recurring, so an ordinary
   `PruneTimers` pass **does** delete it — the opposite of what the paragraph promised. Scoped.

Re-verified on the amended tree: `go test -race ./...` EXIT=0, `golangci-lint run ./...` 0 issues,
coverage 74.6 %, `internal/persistence/store` 87.5 %.

### Verification actually run (2026-08-14, Docker up, `golangci-lint` 2.12.2 present)

| item | result |
|---|---|
| `go test -race -coverprofile=cover.out ./...` | **EXIT=0, zero failures** — this is the repo-root run, so it satisfies checklist items 1 and 2 together |
| `golangci-lint run ./...` **repo-wide** | **0 issues** |
| coverage, total (generated files excluded) | **74.6 %**, up from the 74.5 % bundle-A baseline |
| touched-package coverage | `definition/schedule` 98.1 · `definition/model` 95.0 · `runtime` 93.4 · `scheduler` 93.1 · `internal/persistence/store` 87.5 · **`persistence` 84.1** |

⚠ **`persistence` is below the 85 % floor and this delivery did not close it.** Measured on `main`
in a throwaway worktree: **83.9 %** before, 84.1 % after — pre-existing, and this bundle raised it.
It is **not** closed here deliberately: the 13 uncovered functions are 8 trivial `MySQLWith*` option
setters, `NewCallNotifier`, and `scheduler_locker`'s `NewPostgresSchedulerLocker` /
`NewMySQLSchedulerLocker` / `Lock` / `Unlock`. Testing option setters to move the number is exactly
what Golang rule #8 forbids while a real path stays uncovered — and the real path here, the
distributed advisory lock, is unrelated to this bundle. Queued as backlog instead.

### Implementation corrected the design four times (all amended in-bundle)

1. **The prescribed ADR-0134 regression guard could not fail.** P1.3 named `armed-past-recurring` as
   the row that dies if the `trigger_kind` IN-list is widened. Measured, twice independently: it
   does not — at 2020 the row is not sub-epoch, so the threshold clause rejects it before the kind
   clause is consulted. **As prescribed, no seeded row observed the `trigger_kind` clause at all.**
   Fixed by adding `control-suboneshot` (a sub-epoch `KindOneTime` row), the only row that satisfies
   one half of the predicate and not the other.
2. **The in-wait decode changed accessor.** `WaitActionOf(n)` rather than the prescribed
   `ReadTrigger(w.WaitTrigger, w.WaitEvery, true)` — same decode, but it is what
   `engine.armWaitReminder` itself reads, so gate and arm path cannot disagree about which spec is
   judged.
3. **`convertTrigger` returns an error**, and `KindUnset`/`KindExpr`/`KindEveryExpr` never convert
   at all — the cross-check implication is vacuous for them by a *second* mechanism, which the plan
   did not mention and which a literal implementer would have swallowed in a nil-check.
4. **The generated sweep is not uniformly the stronger half.** It generates no cron specs, so the
   fixed corpus is the **sole** detector for the cron scope decision being "improved" into
   unsoundness (measured: the `KindCron → true` mutation is caught only there).

Plus one gap closed on review: no test pinned the threshold as strictly-less-than, so a `<`→`<=`
mutation survived everything. `control-at-epoch` now pins it.
- **The audit changed the design, not just its wording.** Do not read the pre-fold documents: the
  rejection list was unsound in two classes and silent on four; the sweep predicate missed the
  orphans it targeted; the sweep had no reachable API; the cross-check test could not test the
  production chain and had no home after the scheduler split. All adjudicated in
  `…-audit-adjudication.md` §1–§2.
- **Owner decisions taken at the fold**: cron is **out of scope** for the gate (declines a
  `robfig/cron` import in `definition/model`); `WaitEvery` is **in scope**; the sweep is reached
  through an optional-capability interface, not by widening `persistence.Pruner`; ships as **one
  bundle**.
- **Sequencing**: bundle A (ADR-0177/0178/0180) has **merged** (`a5b33e4c`). B is independent of
  bundle C.

---

## Execution order

Six phases across **six Go packages**. ⚠ Fan out **by package** — concurrent agents inside one
package break each other's `go test` compile even on disjoint files.

| phase | ADR | package | depends on |
|---|---|---|---|
| P1 | 0181 | `internal/persistence/store` | — |
| P2 | 0181 | `persistence` | P1 |
| P3 | 0182 | `definition/schedule` | — |
| P4 | 0182 | `definition/model` | P3 |
| P5 | 0182 | `runtime` | P4 |
| P6 | doc-only | `runtime`, `scheduler` | P1 (for the wording) |

**Wave 1** (concurrent): P1, P3. **Wave 2**: P2 (after P1), P4 (after P3). **Wave 3**: P5, and P6
inline in the controller. ⚠ P5 and P6 both touch `runtime` — run them **serially**, never as
concurrent agents.

⚠ **Docker**: P1 needs **none** — `dbtest.RunTestSQLite` is pure-Go. Its cross-dialect claim is the
owner's Docker-backed run at Verification time, **Postgres only** (spec §2.2: MySQL cannot seed the
fixture). A subagent brief must say explicitly that Docker is *not* available to it; the standing
permission covers only the Verification runs.

## P1 — ADR-0181: the orphan sweep (`internal/persistence/store`)

1. **RED** — `TestPrunerReclaimNeverDueTimers`, in `package store_test`, using
   `dbtest.RunTestSQLite(t)` **directly**. ⚠ **Not** under `forEachDialect` and **not** named so
   `-run '^TestPrune'` picks it up alongside `TestPruner`, which boots Postgres + MySQL with no skip
   guard (audit B-F11/C52). Seed rows by **raw `INSERT`**, so the TEXT encoding is under the test's
   control:
   - orphans in the **current fixed-width** encoding (`textTimeLayout`), several recurring
     `trigger_kind`s;
   - **one orphan in the pre-ADR-0151 trimmed encoding** (`time.RFC3339Nano` of the zero time);
   - controls: a recurring row with a **future** `next_run`, a recurring row with a **past**
     `next_run`, a healthy expired one-shot, and — ⚠ **added during implementation** — a
     **sub-epoch one-shot** (`KindOneTime`), the only row that satisfies one half of the predicate
     and not the other.

   Assert every orphan is reclaimed and **all three controls survive**.
   **Fails today**: the method does not exist — a compile error is a valid RED.
   ⚠ Seed `next_run` **directly**; do not arm through a trigger. The trigger's schedulability is
   irrelevant to this predicate, and the pre-audit fixture named `Weekly(1,nil)` as "never-due" when
   it is measurably **due** at every anchor (audit B-F10/C30). Pick `trigger_kind` values only for
   the column value they produce.

2. **GREEN** — `func (p *Pruner) ReclaimNeverDueTimers(ctx context.Context) (int64, error)`: a
   **single-statement** `DELETE FROM wrkflw_timers WHERE next_run < ? AND trigger_kind IN (…)`,
   bound with `timeArg(p.dialect, time.Unix(0,0).UTC())` and the **seven recurring** kinds.
   ⚠ **Do not widen `PruneTimers`' IN-list.** ⚠ **Do not use `next_run = <zero>`** — measured, it
   misses the trimmed-encoding orphan (4 of 5, not 5 of 5).
   Add a `recurringTriggerKinds` var beside `nonRecurringTriggerKinds`, documented as its exact
   complement, so the two lists cannot silently drift.

3. **RED/GREEN — the clause guards.** ⚠ **Corrected during implementation: this step named the
   wrong control.** It said the past-recurring row "is what fails if anyone implements this by
   widening the IN-list". Measured, it is not: at 2020 the row is not sub-epoch, so the threshold
   clause rejects it before `trigger_kind` is consulted, and it survives the IN-list widened to all
   ten kinds. As prescribed, **no row exercised the `trigger_kind` clause at all**. Each control
   pins a different clause and all three are required:
   - **past-recurring** → the **epoch threshold** (dies if the threshold becomes `time.Now()`);
   - **sub-epoch one-shot** → the **`trigger_kind` clause** (dies if the IN-list is widened — the
     ADR-0134 hazard, and the only row that can observe it);
   - **trimmed-encoding orphan** → the **threshold-vs-equality** choice (survives the equality
     design, which is how that design reports success while leaving the orphans).

4. **RED/GREEN — `Stats.NextFireAt` is freed** (audit B-F7). Seed one orphan + one healthy future
   row; assert `Stats().NextFireAt` is `0001-01-01` **before** and the future instant **after**.
   **Fails today**: the sweep does not exist; fails again if the predicate reverts to equality.

**Verify**: `go test -count=1 -run '^TestPrunerReclaimNeverDueTimers$|^TestPrunerNeverDueStats$' -v ./internal/persistence/store/ ; echo "EXIT=$?"`
⚠ Confirm a `=== RUN` line for each — pitfall #5: `-run` on a name that does not exist exits 0.

## P2 — ADR-0181: the capability interface (`persistence`)

1. **RED** — a test asserting `persistence.NewSQLitePruner(db)`'s result satisfies
   `persistence.NeverDueTimerReclaimer`. **Fails today**: the interface does not exist.
2. **GREEN** — declare `NeverDueTimerReclaimer` in the public `persistence` package with the
   documented type-assertion usage, plus `var _ NeverDueTimerReclaimer = (*store.Pruner)(nil)`.
   ⚠ **Do not add the method to `persistence.Pruner`** — source-breaking for implementors; the
   owner declined it.

**Verify**: `go build ./persistence/... && go test -count=1 -run '^TestNeverDueTimerReclaimer' -v ./persistence/... ; echo "EXIT=$?"`

## P3 — ADR-0182: the predicate (`definition/schedule`)

1. **RED** — a table test over the §3.1 corpus asserting `TriggerSpec.NeverDue()`. **Fails today**:
   the method does not exist. The table is **one corpus with both directions** (audit A-F4): the
   REJECT rows and the MUST-NOT-REJECT rows, in one table.
2. **GREEN** — `func (s TriggerSpec) NeverDue() bool`, implementing spec §3.1 exactly:

   ```
   Duration      : dur <= 0
   DurationRand  : min <= 0 || min >= max
   Daily/Weekly/Monthly:
                   interval == 0
                || any at-time Hour>23 || Minute>59 || Second>59
                || (Monthly) any day == 0 || day > 31 || day < -31
                || (Weekly)  len(weekdays) > 0 && every weekday < 0
   everything else (OneTime, Cron, Expr, EveryExpr): false
   ```

   ⚠ `Weekly` is `len>0 && ALL negative`, **never** `ANY negative` — a mixed set is measurably due.
   ⚠ Negative days-of-month down to `-31` are **legal**. ⚠ Cron returns **false** (out of scope).
   Document the method as **sound, not complete**, and name the three classes it deliberately does
   not judge: anchor-dependent calendar specs, cron, and the expression kinds.

**Verify**: `go test -count=1 -run '^TestTriggerSpecNeverDue$' -v ./definition/schedule/ ; echo "EXIT=$?"`

## P4 — ADR-0182: the gate (`definition/model`)

1. **RED** — the corpus rejected by `model.Validate`, **root and nested**, for `TimerTrigger` **and**
   `WaitEvery`. **Fails today**: measured, all validate clean.
   ⚠ The **nested** case is the one that fails if the rule is placed in `Validate` rather than
   `validateStructure` — placement is the decision, so the test must be able to catch it.
   ⚠ Include the MUST-NOT-REJECT rows here too: `Monthly(12,[31])`, `Monthly(1,[-1])`,
   `Weekly(1,[Weekday(9)])`, `Weekly(1,[Weekday(-1),Monday])` must all still validate clean.
2. **GREEN** — inside `validateStructure`, decode the timer via
   `model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false)` and the in-wait trigger via
   **`model.WaitActionOf(n)`**, then call `spec.NeverDue()`. ⚠ `toWire(n).TimerTrigger` is a
   `*TriggerWire`, **not** a `TriggerSpec` (audit C39) — following the pre-fold ADR text literally
   gives a compile error. **No `scheduler` import in production code.**
   ⚠ **Corrected during implementation**, twice:
   - the in-wait half was prescribed as `ReadTrigger(w.WaitTrigger, w.WaitEvery, true)`. That is the
     same decode, but `WaitActionOf` is what `engine.armWaitReminder` itself reads
     (`engine/step_nodes.go:690`), so gate and arm path cannot disagree about which spec is judged.
   - the error text was prescribed as `workflow-definition: timer trigger can never fire: node %q`.
     Shipped: one sentinel `ErrTriggerNeverDue` = `workflow-definition: trigger can never fire`,
     wrapped `": timer trigger on node %q"` / `": in-wait trigger on node %q"`. Folding the field
     name into the sentinel would force two sentinels and split every consumer's `errors.Is`.
3. Skip the recurring-`DeadlineTimer` half: `validate.go:656` already covers it, and spec §3.4
   records the executed reason the non-recurring half needs none.

**Verify**: `go test -count=1 ./definition/... ; echo "EXIT=$?"`

## P5 — ADR-0182: the cross-check (`runtime`)

1. **RED/GREEN** — in **`package runtime`** (internal test file, so the unexported `convertTrigger`
   is in scope), drive the §3.1 corpus through the **production chain**
   `spec → convertTrigger → scheduler.Trigger.Next` at fixed anchors, asserting the **one direction**
   soundness permits:

   > `spec.NeverDue() == true` ⟹ `Next` is `!ok` at **every** anchor.

   ⚠ Do **not** assert the converse — it is false by ADR-0176 measurements §9, so a
   "verdict == verdict" assertion is unsatisfiable, not merely strict (audit A-F4).
   ⚠ Do **not** put this in `package model_test`: it would have to hand-roll the conversion, a third
   copy blind to conversion drift, and it would have no home after the scheduler spin-out
   (audit A-F8/A-F10/B-F4).
   Anchors must include a February, a 31-day month, a month-end, a Sunday and a leap day.
2. Add a **deterministic generated sweep** — fixed grids over interval / day / weekday / clock
   fields × the same anchors — asserting the same implication. ⚠ No `go test -fuzz` in CI.
   ⚠ **Corrected during implementation**: `convertTrigger` returns `(scheduler.Trigger, error)`, and
   `KindUnset` / `KindExpr` / `KindEveryExpr` hit its `default:` branch — they never convert, so the
   implication is vacuous for them by a *second* mechanism. Assert the refusal with
   `require.ErrorIs(err, scheduler.ErrUnsupportedTrigger)` and skip the row with its reason; do not
   nil-check and swallow it. Also: the sweep generates **no cron specs**, so the fixed corpus — not
   the sweep — is the only thing that catches the cron scope decision turning unsound.

**Verify**: `go test -count=1 -run '^TestNeverDueAgreesWithScheduler' -v ./runtime/ ; echo "EXIT=$?"`

## P6 — doc-only corrections (controller, inline)

Four stale comments in shipped code, all load-bearing for this bundle:

1. `runtime/timerops.go:86-91` — `neverDueNextRun`'s comment enumerates **three** guard sites; there
   are **four** (the post-commit re-check added at ADR-0176's `/code-review`).
2. `runtime/jobstore.go:48-49` — "Nothing reclaims a skipped row … and `Pruner.PruneTimers` does not
   delete it" becomes half-false once ADR-0181's sweep exists (audit C48).
3. `scheduler/trigger.go:111` and `:231-232` — both claim `EveryRandom` bounds "are not validated
   here (e.g. min>max)"; the code returns `!ok` for `min <= 0 || min >= max` (audit A-F7).
4. `scheduler/trigger.go:200` — the `ok=false` bullet list names only "a non-positive min for
   `EveryRandom`" and omits `min >= max`.

**Verify**: `go build ./... && golangci-lint run ./runtime/... ./scheduler/...`

---

## Verification checklist

- [ ] Observable **RED** in the transcript before every new symbol.
- [ ] Load-bearing tests **mutation-verified** (break, observe RED, restore from a `cp` backup —
      ⚠ never `git checkout <path>`, `diff` to confirm). The five that matter: the **sub-epoch
      one-shot** control (P1.3 — the only row that can observe the `trigger_kind` clause), the
      past-recurring control (P1.3), the trimmed-encoding orphan (P1.1), the nested-node rejection
      (P4.1), and a MUST-NOT-REJECT row (P4.1).
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥ 85 % on
      touched packages, hot paths first. Docker: probe and run (standing permission for this run).
- [ ] `go test ./...` repo-root — no regressions.
- [ ] `golangci-lint run ./...` **repo-wide** — clean.
- [x] ✅ **Postgres is COVERED BY A TEST, not by an assumption.** `/code-review` measured that
      Postgres accepts a zero `next_run`, refuting the test file's claim that SQLite was the only
      backend that could hold the fixture — a claim that contradicted spec §2.2 and argued against
      covering the primary production backend. `TestPrunerReclaimNeverDueTimersPostgres` now runs
      the destructive `DELETE` on a real container and is mutation-verified. MySQL remains a
      documented no-op **under default strict mode only**.
- [ ] **Release note**: a consumer parsing YAML at process start gets the new rejection **at boot**
      (spec §3 Consequences), and `WaitEvery` is newly gated.
- [ ] Documents describe what shipped; amend in-bundle anything implementation refuted.
- [ ] `HANDOVER.md` rewritten in place; this `▶ Progress` updated; auto-memory updated.
- [ ] **PAUSE** — owner runs `/code-review` and `/security-review`; fold via `--amend`; re-run on
      the merged tree; merge `--no-ff` and push.

## Traps

1. **Widening the IN-list instead of adding a predicate** — reintroduces ADR-0134's bug. P1.3.
2. **`next_run = <zero>` instead of a threshold** — measured to miss the pre-ADR-0151 trimmed
   encoding, reporting success while leaving the orphans. P1.1.
3. **Adding the method to `persistence.Pruner`** — source-breaking; the owner declined. P2.2.
4. **Placing the gate in `Validate`** — exempts every nested sub-process. P4.1.
5. **Rejecting `Monthly(12,[31])`, a negative day-of-month, or an out-of-range weekday** — all three
   are measurably due; this is the ADR-0165 inverted-predicate shape. P3.2, P4.1.
6. **`ANY negative weekday` instead of `ALL negative`** — `Weekly(1,[-1,Monday])` is due.
7. **Asserting the cross-check in both directions** — unsatisfiable, not strict. P5.1.
8. **Putting the cross-check in `package model_test`** — cannot reach `convertTrigger`. P5.1.
9. **Re-scoping the already-closed items**: the shared calendar package is forbidden by the
   self-containment guard; `EveryRandom(min>max)` closed at ADR-0176's `/code-review`.
   ⚠ **A sentinel named `ErrTriggerNeverDue` in `scheduler` still does not exist** (audit C43 — the
   claim that it did was inherited with its hedge stripped; what exists there is the refusal at
   `scheduler/scheduler.go:580` wrapping `ErrUnsupportedTrigger`). P4 introduces a *different*
   symbol, `definition/model.ErrTriggerNeverDue`, for the authoring gate. Do not conflate them.
