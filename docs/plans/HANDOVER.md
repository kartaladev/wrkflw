# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack of twenty "PREVIOUS RESUME
> POINT" blocks and was silently abandoned for 45 ADRs — see
> `docs/plans/HANDOVER-archive.md`. Per-delivery detail does **not** belong here:
> it belongs in that delivery's plan under a `▶ Progress` block, where it dies
> with the plan. This file carries only: where `main` is, what is unmerged, and
> what to do next.

## State — updated 2026-08-14

**▶ Bundle B (ADR-0181/0182) SHIPPED. Bundle C is the only thing in flight, and it is still
design-only and unaudited.**

The newest code on `main` is the **ADR-0181/0182 merge `1ac140f6`** (pushed), on top of the
ADR-0177/0178/0180 merge `a5b33e4c`. ⚠ Do not quote main's head; re-derive with
`git rev-parse --short refs/heads/main`.

| bundle | branch | ADRs | audited? | state |
|---|---|---|---|---|
| ~~A~~ | ✅ **SHIPPED — merge `a5b33e4c`, pushed, branch deleted** | 0177, 0178, 0180 | 3 lenses (27) + `/code-review` (3) + `/security-review` (0) | done |
| ~~B~~ | ✅ **SHIPPED — merge `1ac140f6`, pushed, branch deleted** | 0181, 0182 | 3 lenses (~40) + `/code-review` (3) + `/security-review` (0) | done |
| **C** | `feat/compensation-failure-retry-and-visibility` | 0179 | ✅ **audited TWICE** — first audit 4 Criticals, rewrite's own audit **9 more**; both folded 2026-08-17 | ⏸ **design-only, but NOW an implementation input** |

### ⏸ Bundle C — what a fresh session must do next

**Implement it.** ADR-0179 has now survived a second audit (3 lenses, ~30 findings, 9 Criticals,
all adjudicated and folded 2026-08-17) and IS an implementation input. Read
`docs/specs/2026-08-14-adr-0179-audit-adjudication.md` first — §2 holds four decisions the previous
text named as "required" and left blank.

Five phases across five packages: **P1 `engine`** (serial, the bulk), then **P2 `runtime` ‖
P3 `processtest` ‖ P4 `internal/persistence/store`**, then P5 doc-only.

⚠ **`walkScoped()` is SPLIT, not extended.** Two lenses converged independently: extending it
measures `HasArmedTimers=false`, `Classify.Reason="unknown"`, `AutoTimers fires=false` — every
consumer opting into `CompensationRetryPolicy` would get `ErrUnhandledPark`. The old instruction
("must extend it, or the guard refuses every retry and the ADR silently never works") was **also a
false universal**: the guard is `!walkScoped() && !spawnsNewWork()`, and `spawnsNewWork()` is true
on any resuming walk. The work is needed for **terminating** walks.

⚠ **This is a BREAKING change for `processtest` consumers even with retry off** — the always-on
incident re-classifies a park `timer → incident` and `AutoTimers()` stops driving it. Release note.

⚠ **Accepted residual, do not present as fixed**: making the retry timer un-prunable closes the
retention route only. A timer skipped at boot or never rehydrated still strands the walk — the
ADR-0034 property the ADR claims to preserve. Backlog 37.

⚠ Its dispatch-site count has been wrong **twice** before; re-derived this audit as **4 today, 5
after the retry**, and correct. Derive it in the test, never hard-code it.

**Latest ADR = 0182. Next free = 0183.** ADR numbers 0155–0157 remain reserved by the parked
`feat/durable-waiters-delivery-correctness`.

### What each bundle closes

- **A** — NEXT WORK items 1 and 4. Shipped.
- **B** — NEXT WORK item 2. Orphan zero-`next_run` row reclamation (backlog 23) and an
  authoring-time never-due gate (part of backlog 22). ⚠ The step-time gate and
  `StepOptions.SchedulingLocation` are **deliberately re-deferred** with ADR-0176's measured reason.
- **C** — NEXT WORK item 3. Retry + visibility for a failed compensation action (backlog 16, 3g).

### ⚠ Ordering constraint — still open for C

A merged before C, as required. `TimerKind.walkScoped()` is on `main`; bundle C must **extend** it
with `TimerCompensationRetry`, or ADR-0178's guard refuses every compensation retry and ADR-0179
**silently never works**. That extension is bundle C's job and is listed in its plan. B is
independent of both.

### ⚠ Process lessons this session earned

1. **A fixture can be as vacuous as an assertion.** The bundle's own audit was thorough — three
   lenses, ~40 findings — and it still prescribed a "regression guard" test whose control row could
   not fail on the axis it was named for. The lesson generalises: *check which clause each fixture
   row can actually discriminate*, not just that the test asserts something.
2. **Both audit lenses measured the wrong surface.** Lens A and lens B both drove
   `scheduler.Trigger.…` constructors **directly**, which is not a path a definition can reach —
   lens A retracted its own F9 on discovering this. The corpus had to be re-measured through
   `convertTrigger` before it could be trusted. *Measure the chain production uses, not the nearest
   reachable proxy.*
3. **A prose enumeration of a code path's branches rots, in both directions at once.** ADR-0182's
   reject list was unsound on two classes it named and silent on four it did not. It is now a table
   of executed verdicts.
4. **An in-repo measurement refuted an "unverified" assumption nobody re-read.** MySQL's
   `Error 1292` was measured during ADR-0176 and sat in `docs/specs/`; bundle B labelled the same
   claim "needs Docker" and prescribed a run that cannot execute.

## ▶ NEXT WORK — in order

1. ✅ **Bundle B is SHIPPED** (merge `1ac140f6`, pushed, branch deleted). Both gates passed:
   `/code-review` 3 findings all folded, `/security-review` **0 findings**.
   ⚠ **Nine consecutive deliveries** have now had the real `/code-review` find something every
   stand-in missed — here, a destructive `DELETE` with **zero coverage on the primary production
   backend**, defended by a test comment that **contradicted the bundle's own spec §2.2**. Do not
   skip the gate because the suite is green.
2. ✅ **Bundle C's rewrite has been audited** (2026-08-17, 3 lenses, ~30 findings, 9 Criticals) and
   the findings are **adjudicated and folded**. **Implement it** — see the section above and the
   plan's phase table. ⚠ Do not carry forward the old "extend `walkScoped()`" instruction; it is
   both harmful and justified by a false universal.
3. Then the remaining backlog below.

## Pre-v0.1.0 blockers

1. ✅ **Strict definition decoding — CLOSED by ADR-0167.** ⚠ Does **not** close the fail-open
   `AuthzSpec`: an empty spec, `eligible_roles: []`, a bare `eligible_roles:` and
   `eligible_roles: null` all parse cleanly and mean allow-all. Own ADR.
   🚨 **Before DEPLOYING ADR-0167**: audit stored definition rows for 5 pre-ADR-0144 camelCase keys
   (`compensateAction`, `compensationAction`, `completionAction`, `correlationKey`, `messageName`).
2. ✅ **A never-due timer arm — CLOSED by ADR-0176, ON `main`.**
2b. ✅ **The `scheduler.Activate` livelock on `Monthly(12,[31])` — CLOSED by ADR-0176, ON `main`.**
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the invariant, the
   write path does not.
4. ✅ **ADR-0159's misnamed symbols — CLOSED.**
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky**
   (`internal/persistence/store/notifier_pgx_test.go`). Interacts with item 7; **do not silence it.**
6. ✅ **`processtest` cannot drive an arm-only park — CLOSED by ADR-0166.**
7. **Suite speed.** `internal/dbtest`'s `sync.Once` boot fires per package → 12 Postgres + 7 MySQL
   boots. Fix: honour `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   fallback, plus `scripts/testdb.sh up|down` and CI wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
9. ✅ **`Park.HasArmedTimers` missed four arm sources — CLOSED by ADR-0177, ON `main`.**

## Backlog

⚠ Items **16, 3g** are addressed by in-flight bundle C. Items **0, 2, 3, 3c** closed by A;
**22, 23** closed by B (pending its merge). What remains:

**Pre-existing defects (measured, unclaimed):**

3b. **The cancel path flips `s.Timers` from nil to an empty non-nil slice.** Pre-existing shape drift.
3d. **The instance document gains fields** (`incidents[].kind`, `compensating` object) — additive.
3e. **`service.Service` gained a method** (ADR-0175) — BREAKING for a decorator; needs a release note.
3f. **The stall incident's `ScopeID` is empty for a TARGETED compensation throw** — read `NodeID`.

**Deferred from ADR-0175's design:** 4. a per-node `CompensationStallAfter` tier (and, after bundle
C, a per-node compensation **retry** tier). 5. a bound on repeated `retry`. 6. whether stall
detection should default ON.

**From ADR-0174:** 7. a pre-ADR-0171 unpinned cursor keeps ADR-0173's accepted double-run at the
`endInstance` harvest — wants a cursor-migration ADR. 8. records stranded on pre-ADR-0174 rows stay
unreachable. 9. ADR-0164's "eight terminal sites" is stale — ten today. 10.
`compensationRecordsForScope` reads an open scope as a records-exist decision.

**From ADR-0158/0172:** 11. a flow targeting a NON-EXISTENT node parks a permanent wedge. 12.
`PendingCancel=true` survives onto a `Running` instance and **will terminate the NEXT throw or
reverse walk**. 13. micro mode loses a signal delivery. 15. `engine/step_nodes.go:501`'s nested
arm retirement is uncovered.

**From ADR-0168–0171:** 17. the event-sub-process hole's remaining direction. 18. ADR-0171's two
open bounds. 19. `processtest.Classify` has no reason for a compensation-walk park. 20. repo-wide
coverage ~74.6 % (⚠ `service` ~52.6 %). 21. **`AUDIT.md`** on `docs/architecture-audit`, ⚠ NOT on
`main` and NOT pushed (public repo, unfixed Critical/High findings); claims **unverified**.

**From ADR-0176:** 24. a refused arm leaves an in-memory `timerRecord` with no durable row. 26. the
calendar scan is still linear in `interval` (now at MONTH granularity; `Daily`/`Monthly` only). 27.
the definition store round-trips semantically invalid definitions. 28. a weekday set mixing
in/out-of-range weekdays changed answer — **release note**. 29. the arm guard is not atomic with the
arm. 30. `weeklyNext`'s `int(interval)*7` overflows at ≥ ~1.3e18.

**From ADR-0177/0178/0180:** 31. three dangling ADR-section citations (`scheduler/trigger.go:382`,
`scheduler/trigger_test.go:554` cite a nonexistent "ADR-0176 §4"; `engine/state_compensation.go:391`
cites "ADR-0174 §5.3"). 32. **downgrade drops new state fields** — whole-state `json.Marshal` with no
`DisallowUnknownFields`, so an older build silently drops what a newer one wrote; after bundle C a
dropped `RetryAttempts` **resets the retry budget so a poison compensation retries forever**. 33.
**`ProcessDriver.CancelInstance` still answers `err=<nil> status=terminated`** on an already-terminal
instance (public API); `service` guards it with `ErrConflict`, the driver does not.

**🆕 opened this session:**

37. **A compensation retry timer lost at boot still strands the walk** (ADR-0179 accepted residual).
    Un-prunability closes the retention-job route; a row skipped by `jobStore.Load` or never
    rehydrated is not covered, and exhaustion is reachable only by the timer firing. Escape is
    ADR-0175's operator verbs.
38. **`Incidents[0]` is read positionally by THREE sites**, and the defect ships today for
    `IncidentCompensationStall`: `runtime/outbox.go` (`terminalEventErr`),
    `runtime/processdriver_action.go` (`terminalErr`), and `processtest/park.go`. ADR-0179's P2
    fixes the first two with an allow-list; the third is untouched.
34. **`persistence` is 84.1 % covered, under the 85 % floor** — pre-existing (83.9 % on `main`).
    The real gap is `scheduler_locker`'s `NewPostgresSchedulerLocker` / `NewMySQLSchedulerLocker` /
    `Lock` / `Unlock`, entirely uncovered; the rest is 8 trivial `MySQLWith*` option setters and
    `NewCallNotifier`. ⚠ Close it by testing the **advisory lock**, not the option setters —
    Golang rule #8 exists precisely to stop the latter.
35. **ADR-0182's gate cannot judge the legacy flat trigger strings.** `ReadTrigger` decodes a nil
    wire with a non-empty flat string to `AfterExpr`/`EveryExpr` — engine-resolved expression kinds.
    Never-due values there reach the arm guard only. Documented in the ADR, not fixed.
36. **Cron is out of scope for the never-due gate** (owner decision — declines a `robfig/cron`
    import in `definition/model`). Both an unparseable cron and a parseable-but-impossible one
    (`"0 0 30 2 *"`) still validate clean. Measured; reopen only with the dependency decision.

## Where things live

| | |
|---|---|
| `main` | **ADR-0181/0182 is the newest SHIPPED bundle**, merge `1ac140f6`, pushed. ⚠ Never quote main's HEAD; re-derive: `git rev-parse --short refs/heads/main` |
| bundle B | ✅ shipped — its spec/plan/ADRs/audit/adjudication are all on `main` under `docs/` |
| bundle C | `feat/compensation-failure-retry-and-visibility` — `…-compensation-failure-retry-and-visibility.md`, `…-adr-0179-premise-evidence.md`, `…-adr-0179-inherited-audit-lens-{a,b,c}.md` |
| *(merged branches)* | Deleted once pushed. **`origin` carries only `main`** plus dependabot |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only; holds ADR **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main`, NOT pushed |
| worktrees | ✅ **CLEAN** — `git worktree list` shows only the primary checkout |
| Latest ADR | **0182**. Next free is **0183** |
| v0.1.0 | not tagged |

## Standing constraints

- **Docker: standing permission for the Verification coverage + no-regressions runs** (owner,
  2026-08-11). Probe and run; if unavailable, say so and let the owner start it or skip, labelling
  any container-free subset as the partial result it is. ⚠ Everything else asks, and **a subagent
  brief must say so explicitly**.
- **`golangci-lint`: probe and run; if absent, offer to install or skip** — never substitute
  `go vet`, never claim "lint clean" for a run that did not execute. (Present this session: 2.12.2.)
- **Container-free packages**: `engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`,
  `transport/http`. ⚠ **`./runtime/...` as a whole is NOT**, and ⚠ `internal/persistence/store` is
  NOT — **but `RunTestSQLite` is pure-Go and starts no container**, which is how bundle B's entire
  store phase was built and measured without Docker.
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones.
- **Judge a test run by its exit code**, never a pipeline tail; use `-count=1`.
- **Run the suite on the MERGED tree**, and re-run after any `/code-review` fix.
- `/code-review` and `/security-review` are **owner-invoked only**. Adversarial Opus stand-ins
  first anyway — but they are **not** the gate.
- **Fan out subagents by Go package.** A delivery entirely inside `engine` runs **strictly serial**.
  Bundle B fanned out across six packages and it worked cleanly — P1‖P3, then P2‖P4, then P5.
- **An agent that must measure against a patched tree gets a `git worktree`**, the brief must say
  so, *and* must require verifying the bundle is present as step 0.
- ⚠ **Brief long-running agents to persist findings per finding, before the next probe.**
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`.**
- ⚠ **`git reset --soft main` on a branch cut from an OLDER main stages a REVERT of everything
  merged in between.** Hit this session; recovered with `git reset --mixed <branch-tip>` and a
  rebase. Check `git merge-base HEAD main` before any soft reset.
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
