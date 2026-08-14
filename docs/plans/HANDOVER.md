# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack of twenty "PREVIOUS RESUME
> POINT" blocks and was silently abandoned for 45 ADRs — see
> `docs/plans/HANDOVER-archive.md`. Per-delivery detail does **not** belong here:
> it belongs in that delivery's plan under a `▶ Progress` block, where it dies
> with the plan. This file carries only: where `main` is, what is in flight, and
> what to do next.

## State — updated 2026-08-14

**▶ ADR-0177/0178/0180 SHIPPED. Two design-only bundles remain on branches.**
The newest code on `main` is the **ADR-0177/0178/0180 merge `a5b33e4c`** (pushed), on top of the
ADR-0176 merge `52bf0f80`. ⚠ Do not quote main's head; re-derive with
`git rev-parse --short refs/heads/main`.

| bundle | branch | ADRs | audited? | state |
|---|---|---|---|---|
| ~~A~~ | ✅ **SHIPPED — merge `a5b33e4c`, pushed, branch deleted** | 0177, 0178, 0180 | 3 lenses (27) + `/code-review` (3) + `/security-review` (0) | done |
| **B** | `feat/never-due-gate-and-orphan-reclamation` | 0181, 0182 | ✅ 3 lenses, ~40 findings — ⚠ **NOT yet folded into the documents** | design **unsound as written**; fold the audit first, then implement |
| **C** | `feat/compensation-failure-retry-and-visibility` | 0179 | ⚠ **failed its first audit; rewritten; NOT re-audited** | audit the rewrite first |

### ⏸ Bundle A — what a fresh session must do next

**Do NOT merge it.** It is one commit on its branch, fully implemented, and its Verification is
green (`go test -race ./...` EXIT=0 no failures; coverage **74.5 %** vs a 74.2 % baseline;
`golangci-lint run ./...` repo-wide 0 issues; `go vet ./...` EXIT=0, Docker up, nothing skipped).
What remains is the part an agent **cannot** do: the owner runs `/code-review` and
`/security-review`, findings are folded via `git commit --amend` (never stacked), the suite is
re-run **on the merged tree**, then `git merge --no-ff` and push.

⚠ Implementation corrected the design **five** times; all are amended in-bundle and recorded in the
plan's `▶ Progress`. Two are worth knowing before reviewing the diff: the audit's prescribed
cancel-started fixture **cannot exist** (that walk holds no tokens and no timer records), and the
dropped-cancel site serves **two** situations where the ADR had generalised from one.

**Latest ADR = 0182. Next free = 0183.** ADR numbers 0155–0157 remain reserved by the parked
`feat/durable-waiters-delivery-correctness`.

### What each bundle closes

- **A** — NEXT WORK items 1 and 4. `TimerWaiters()` + `Token.AwaitTimer` (blocker 9, backlog 3c);
  a dying instance ignores a fired timer (backlog 0); duplicate start and dropped cancel stop
  reporting success (backlog 2, 3). Plus **thirteen** false comments in shipped code.
- **B** — NEXT WORK item 2. Orphan zero-`next_run` row reclamation (backlog 23) and a deploy-time
  never-due gate (part of backlog 22). ⚠ The step-time gate and `StepOptions.SchedulingLocation`
  are **deliberately re-deferred** with ADR-0176's measured reason recorded.
- **C** — NEXT WORK item 3. Retry + visibility for a failed compensation action (backlog 16, 3g).

### ⚠ Ordering constraint

**A must merge before C.** Bundle A introduces `TimerKind.walkScoped()`; bundle C extends it with
`TimerCompensationRetry`. Without that extension ADR-0178's guard refuses every compensation retry
and ADR-0179 **silently never works**. B is independent of both.

### ⚠ Process lessons this session earned

1. **The audit changed the delivery's shape, not just its wording.** It put 4 of 6 Criticals on
   ADR-0179, which was consequently **split out of bundle A into its own delivery**. A design that
   just failed its audit is not an implementation input.
2. **Two Criticals were defects the design's own rationale concealed.** `Token.AwaitTimer` was
   specified set-only — hidden by the "additive, no dispatch change" justification — which would
   have left `HasArmedTimers()` true forever and *inverted* ADR-0177's purpose. And the
   dropped-cancel sentinel was justified by "the child loop logs and continues", which is **true**;
   the loop absorbs the error and the subtree is orphaned anyway, because the parent site returns
   before propagation and `continue` skips the recursion.
3. **A premise can be false in the direction that makes your test vacuous.** "A compensation walk
   by definition spawns no work" is false — *throw* walks measure `SpawnsNewWork = TRUE`. The guard
   is still right, but the convenient in-repo fixture would have produced a test that passes whether
   or not the guard exists.
4. **`StatusRunning` is the zero value, and `StartedAt` is caller-supplied.** Both obvious
   duplicate-start predicates are wrong, and the two obvious test rows pass under the defective one.
5. **Persist audit findings to a file per finding.** The first audit round stalled at 600 s with
   **nothing** written; only one lens had produced 18 lines. Re-run with "write before you probe",
   all three completed.
6. **The step-0 worktree check earned its place again** — all three lenses found the bundle
   **absent** and recovered with the briefed `git merge`.

## ▶ NEXT WORK — in order

1. ✅ **Bundle A is SHIPPED** (merge `a5b33e4c`, pushed). Both gates passed: `/code-review` 3
   findings all folded, `/security-review` **0 findings**.
   ⚠ The HIGH was an **orphaned recurring scheduler job**: the dying-instance refusal retired the
   timer record, which is exactly what stops the terminal sweep from later emitting `CancelTimer` —
   and the delivery's own test asserted `Commands` was empty, pinning the leak as the spec. Three
   audit lenses and implementation all missed it. **Eighth consecutive delivery where the real gate
   found something the stand-ins did not.** ⚠⚠ The controller's adjudicated fix for the MEDIUM was
   itself **refuted by measurement** — see the plan's `▶ Progress` P5.
2. **Fold bundle B's audit into its documents, then implement.** ⚠ **Do not implement it as
   written** — three lenses agree the never-due predicate is **unsound**: `Weekly(1,[Weekday(9)])`
   and `Monthly(1,[-1])` are DUE at every anchor and would be wrongly rejected;
   `Cron("0 0 30 2 *")` — the ADR's own motivating example — *parses*, so "unparseable cron" never
   catches it; `Monthly(0,…)`, `Every(d<0)` and `EveryRandom(min<=0)` are missing; `WaitEvery` is an
   uncovered third trigger field. Also: **MySQL cannot hold a zero `next_run` at all**
   (ADR-0176 measured `Error 1292`), so ADR-0181's assumption is *refuted*, its plan's Docker step is
   unrunnable, and `next_run = <zero>` **equality** misses the pre-ADR-0151 trimmed encoding. And
   "breaking for authoring, not loading" is **false** — the YAML path re-validates at every parse.
   Owner decision already taken: expose the sweep via an **optional-capability interface**
   (`persistence.NeverDueTimerReclaimer` + documented assertion), not by widening `persistence.Pruner`.
   Audit records are **in-repo** on that branch: `docs/specs/2026-08-13-adr-0181-0182-audit-lens-{a,b,c}.md` (commit `0bf19033`).
3. **Audit bundle C's rewrite** (`feat/compensation-failure-retry-and-visibility`) — three lenses,
   one dedicated to re-counting. ⚠ Its dispatch-site count has been wrong **twice**: ADR-0175
   shipped "the third of the three" when there were four, and pre-split ADR-0179 said "all four"
   when its own retry makes five. Then implement. **A must merge before C.**
4. Then the remaining backlog below.

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
   boots (~60s of a ~2min suite). Fix: honour `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN`
   with testcontainers as fallback, plus `scripts/testdb.sh up|down` and CI wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
9. ⏳ **`Park.HasArmedTimers` misses four arm sources — CLOSED BY BUNDLE A once merged.**
   ⚠ It is **four**, not two: boundary, event-gateway, event-sub-process **and plain
   intermediate-catch**, the last of which appears in no enumeration anywhere.

## Backlog

⚠ Items **0, 2, 3, 3c, 16, 3g, 22, 23** are addressed by the three in-flight bundles above and are
not repeated here in full. What remains:

**Pre-existing defects (measured, unclaimed):**

1. ⏳ `engine/step_triggers.go:291`'s `ADR-0034 §2.5` — **fixed in bundle A**. ⚠ Not invented: the
   2026-06-23 spec really has a `### 2.5`. A wrong-*document* attribution.
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
reverse walk**. 13. micro mode loses a signal delivery. 14.
`runtime/processdriver_action.go:485`'s comment is FALSE. 15. `engine/step_nodes.go:501`'s nested
arm retirement is uncovered.

**From ADR-0168–0171:** 17. the event-sub-process hole's remaining direction. 18. ADR-0171's two
open bounds. 19. `processtest.Classify` has no reason for a compensation-walk park. 20. repo-wide
coverage ~74 % (⚠ `service` ~52.6 %). 21. **`AUDIT.md`** on `docs/architecture-audit`, ⚠ NOT on
`main` and NOT pushed (public repo, unfixed Critical/High findings); claims **unverified**.

**From ADR-0176:** 24. a refused arm leaves an in-memory `timerRecord` with no durable row. 26. the
calendar scan is still linear in `interval` (now at MONTH granularity; `Daily`/`Monthly` only). 27.
the definition store round-trips semantically invalid definitions. 28. a weekday set mixing
in/out-of-range weekdays changed answer — **release note**. 29. the arm guard is not atomic with the
arm. 30. `weeklyNext`'s `int(interval)*7` overflows at ≥ ~1.3e18.

**🆕 opened this session:**

31. **Three dangling ADR-section citations** of the same family as backlog 1, out of bundle A's
    scope: `scheduler/trigger.go:382` and `scheduler/trigger_test.go:554` cite "ADR-0176 §4"
    (ADR-0176 has no numbered sections); `engine/state_compensation.go:391` cites "ADR-0174 §5.3".
32. **Downgrade drops new state fields.** Persistence is whole-state `json.Marshal` with **no**
    `DisallowUnknownFields`, so an older build silently drops fields a newer one wrote. Concrete
    consequences after bundles A and C: a parked plain-ICE token becomes invisible again, and a
    dropped `RetryAttempts` **resets the retry budget so a poison compensation retries forever**.
33. **`ProcessDriver.CancelInstance` still answers `err=<nil> status=terminated`** on an
    already-terminal instance (public API). `service` guards it with `ErrConflict`; the driver does
    not. Deliberately out of ADR-0180's scope.

## Where things live

| | |
|---|---|
| `main` | **ADR-0176 is the newest SHIPPED bundle**, merge `52bf0f80`, pushed. ⚠ Never quote main's HEAD; re-derive: `git rev-parse --short refs/heads/main` |
| bundle A | `feat/engine-visibility-and-truthfulness` — spec/plan `docs/{specs,plans}/2026-08-13-engine-visibility-and-truthfulness.md`, audit `docs/specs/2026-08-13-adr-0177-0180-audit-lens-{a,b,c}.md`, evidence `…-adr-0177-premise-evidence.md`, `…-adr-0178-0180-premise-evidence.md` |
| bundle B | `feat/never-due-gate-and-orphan-reclamation` — `…-never-due-gate-and-orphan-reclamation.md` + `…-adr-0181-0182-premise-evidence.md` |
| bundle C | `feat/compensation-failure-retry-and-visibility` — `…-compensation-failure-retry-and-visibility.md`, `…-adr-0179-premise-evidence.md`, `…-adr-0179-inherited-audit-lens-{a,b,c}.md` |
| *(merged branches)* | Deleted once pushed. **`origin` carries only `main`** plus dependabot |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only; holds ADR **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main`, NOT pushed |
| worktrees | ✅ **CLEAN** — `git worktree list` shows only the primary checkout; all seven agent worktrees from this session were removed after verifying `git status --porcelain` empty, and their branches deleted |
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
  NOT — **but `RunTestSQLite` is pure-Go and starts no container**, which is how bundle B's 0-of-4
  measurement was taken without Docker.
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones.
- **Judge a test run by its exit code**, never a pipeline tail; use `-count=1`.
- **Run the suite on the MERGED tree**, and re-run after any `/code-review` fix.
- `/code-review` and `/security-review` are **owner-invoked only**. Adversarial Opus stand-ins
  first anyway — but they are **not** the gate.
- **Fan out subagents by Go package.** A delivery entirely inside `engine` runs **strictly serial**.
- **An agent that must measure against a patched tree gets a `git worktree`**, the brief must say
  so, *and* must require verifying the bundle is present as step 0 — ⚠ **all three lenses this
  session found it absent**. Give them the recovery command
  (`git merge --no-edit <branch>`; they cannot `git checkout` a branch the primary tree holds).
- ⚠ **Brief long-running agents to persist findings per finding, before the next probe.** The first
  audit round stalled with nothing written.
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`.**
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
