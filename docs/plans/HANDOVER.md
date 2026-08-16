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

## State — updated 2026-08-17

**▶ Bundle C (ADR-0179) is IMPLEMENTED on its branch and is waiting on the owner-invoked
delivery gate. Nothing else is in flight.**

The newest code on `main` is still the **ADR-0181/0182 merge `1ac140f6`** (pushed); the two commits
after it are docs-only. ⚠ Do not quote main's head; re-derive with
`git rev-parse --short refs/heads/main`.

| bundle | branch | ADRs | state |
|---|---|---|---|
| ~~A~~ | ✅ SHIPPED — merge `a5b33e4c` | 0177, 0178, 0180 | done |
| ~~B~~ | ✅ SHIPPED — merge `1ac140f6` | 0181, 0182 | done |
| **C** | `feat/compensation-failure-retry-and-visibility` | **0179** | ⏸ **implemented, ONE COMMIT, LOCAL ONLY — awaiting the gate** |

### ▶ NEXT WORK — in order

1. **`/security-review` on bundle C** — the last gate step. `/code-review` is **DONE**: 6 findings,
   1 HIGH, all fixed or adjudicated out-of-scope-with-reason, folded via `--amend`. Then merge
   `--no-ff` and push.
   ⚠ **Eleven consecutive deliveries** have had the real `/code-review` find something every
   adversarial stand-in missed — this one a **permanent walk wedge**. Do not skip `/security-review`
   because the suite is green.
2. Then the backlog below.

⚠ **What `/code-review` caught, because it generalises.** ADR-0175's operator `retry` verb, used
during an ADR-0179 backoff, left the cursor naming an already-cancelled timer; the next failure hit
the new idempotency guard, was read as a redelivery, and the instance sat in `StatusCompensating`
**forever** with nothing armed to move it. Root cause: the guard was built on `RetryTimerID` without
enumerating the writers of the field it keys on (`ActiveCmdID`).
⭐ **`skip` and `abandon` were clean only incidentally** — they own no bespoke cursor logic and
inherit it from two helpers this ADR revised. `retryStalledCompensation` is the one verb path with
bespoke handling and the one this ADR never touched.
**When a feature revises shared helpers, the sites at risk are the ones that BYPASS those helpers.
Enumerate the bypassers, not the callers.**

### What bundle C is, in one paragraph

A compensation action replying `ActionFailed` used to be skipped in **total silence** — no retry, no
incident, and, despite ADR-0034's Consequences claiming otherwise, **no log line**. It now always
emits a WARN and raises an `IncidentCompensationFailed`, and it is re-dispatched after a backoff when
the consumer opts in. Closes backlog **16** and **3g**. Also fixes a **pre-existing** defect: the
cause of death was read positionally from `Incidents[0]`, so a walk-scoped incident already beat the
real error today.

### ⚠ Things a fresh session must not get wrong about bundle C

- **It is BREAKING in three ways, and all three fail SILENTLY — no compile error.** See
  `CHANGELOG.md` ▸ Unreleased ▸ Breaking changes, which is the authoritative list.
- **Two accepted residuals, do NOT present either as fixed**: a retry timer lost at boot still
  strands the walk (un-prunability closes only the retention route); and a *leaked* retry row is now
  **permanent**, because no bulk sweep can delete the kind any more.
- **The ADR was amended BY implementation in six places** — each marked
  `⚠ AMENDED / ADDED AT IMPLEMENTATION` with the measurement that forced it. The two that matter
  most: Decision 2's opt-in was **unreachable** through `runtime.ProcessDriver` (fixed in-bundle),
  and Decision 6's literal wording **deleted the incident at birth** (measured `incidents=0`).

### ⚠ The process lesson this delivery earned

**Two audits, six lenses, and none of them could see the defect that mattered most.** ADR-0179's
retry opt-in was reachable only through `engine.StepOptions`; `runtime.ProcessDriver` never set it
and no option existed, so the feature would have shipped promised-but-unusable. Every lens read *one
design document*; the gap existed only in the **seam between two packages**. A design audit
structurally cannot find it — it took writing the code and asking "can a consumer actually turn this
on?"

⚠ **Corollary for the next rule-#9 audit: give one lens the job of tracing each new option end to
end from the consumer's entry point**, not of reading the decision that introduces it.

Second lesson, quantified in the plan's `▶ Progress`: **8 inherited counts were re-derived during
implementation and 6 were wrong** — and **4 of the 6 were in the controller's own briefs**, not in
the audited documents. Re-derivation is not an audit-time activity; it is a per-edit activity.

**Latest ADR = 0182 shipped; 0179 is implemented-not-merged. Next free = 0183.** ADR numbers
0155–0157 remain reserved by the parked `feat/durable-waiters-delivery-correctness`.

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

⚠ Items **16** and **3g** are closed by bundle C (pending its merge).

**Pre-existing defects (measured, unclaimed):**

3b. **The cancel path flips `s.Timers` from nil to an empty non-nil slice.** Pre-existing shape drift.
3d. **The instance document gains fields** (`incidents[].kind`, `compensating` object) — additive.
3e. **`service.Service` gained a method** (ADR-0175) — BREAKING for a decorator; needs a release note.
3f. **The stall incident's `ScopeID` is empty for a TARGETED compensation throw** — read `NodeID`.

**Deferred from ADR-0175's design:** 4. a per-node `CompensationStallAfter` tier **and a per-node
compensation retry tier** (bundle C ships one engine-wide policy). 5. a bound on repeated `retry`.
6. whether stall detection should default ON.

**From ADR-0174:** 7. a pre-ADR-0171 unpinned cursor keeps ADR-0173's accepted double-run at the
`endInstance` harvest — wants a cursor-migration ADR. 8. records stranded on pre-ADR-0174 rows stay
unreachable. 9. ADR-0164's "eight terminal sites" is stale — ten today. 10.
`compensationRecordsForScope` reads an open scope as a records-exist decision.

**From ADR-0158/0172:** 11. a flow targeting a NON-EXISTENT node parks a permanent wedge. 12.
`PendingCancel=true` survives onto a `Running` instance and **will terminate the NEXT throw or
reverse walk**. 13. micro mode loses a signal delivery. 15. `engine/step_nodes.go`'s nested
arm retirement is uncovered.

**From ADR-0168–0171:** 17. the event-sub-process hole's remaining direction. 18. ADR-0171's two
open bounds. 19. ✅ **`processtest.Classify` has no reason for a compensation-walk park — PARTLY
CLOSED by bundle C**: a compensation-failure park is now drivable, but there is still no
`ReasonCompensation` of its own. 20. repo-wide coverage (⚠ `service` ~52.6 %). 21. **`AUDIT.md`**
on `docs/architecture-audit`, ⚠ NOT on `main` and NOT pushed (public repo, unfixed Critical/High
findings); claims **unverified**.

**From ADR-0176:** 24. a refused arm leaves an in-memory `timerRecord` with no durable row. 26. the
calendar scan is still linear in `interval`. 27. the definition store round-trips semantically
invalid definitions. 28. a weekday set mixing in/out-of-range weekdays changed answer — **release
note**. 29. the arm guard is not atomic with the arm. 30. `weeklyNext`'s `int(interval)*7` overflows.

**From ADR-0177/0178/0180:** 31. three dangling ADR-section citations. 32. **downgrade drops new
state fields** — whole-state `json.Marshal` with no `DisallowUnknownFields`. ⚠ **Bundle C raises the
stakes**: a dropped `RetryAttempts` **resets the retry budget so a poison compensation retries
forever**, and a dropped `IncidentKind` degrades the new incident into a *resolvable, deletable*
`IncidentAction`. 33. **`ProcessDriver.CancelInstance` still answers `err=<nil> status=terminated`**
on an already-terminal instance; `service` guards it with `ErrConflict`, the driver does not.

**From ADR-0181/0182:** 34. `persistence` under the 85 % floor — close it by testing the **advisory
lock**, not the option setters. 35. ADR-0182's gate cannot judge the legacy flat trigger strings.
36. Cron is out of scope for the never-due gate (owner decision).

**🆕 opened by bundle C:**

37. **A compensation retry timer lost at boot still strands the walk.** Un-prunability closes the
    retention-job route; a row skipped by `jobStore.Load` or never rehydrated is not covered.
    Escape is ADR-0175's operator verbs.
38. **`Incidents[0]` is read positionally by FOUR sites**, not three. Bundle C fixed the two
    `runtime` resolvers via an allow-list and de-positionalised `processtest`'s `incidentNode`.
    ⚠ **Still open: `examples/scenarios/admin_monitoring/main.go`**, which feeds the value to
    `ResolveIncident` and will now hit `ErrIncidentNotResolvable` on a walk-scoped incident.
39. **A leaked `TimerCompensationRetry` row is permanent.** `PruneTimers` excludes the kind and
    `ReclaimNeverDueTimers` only matches recurring `trigger_kind`, so no bulk sweep can reach it.
    Intended for a live walk; unbounded accumulation for a row leaked by an instance that died.
40. **`engine.NewActionFailed` gives a consumer a ZERO retry backoff.** The delay is
    `JitterFraction * Backoff(attempt)` and the public constructor defaults `JitterFraction` to 0.
    The shipped `ProcessDriver` passes a real fraction (full-jitter — correct by design), but this
    is **public API of a library** and a direct caller gets an immediate retry. ⚠ `engine/trigger.go`
    also documents zero as "no jitter", which is false — zero means zero *delay*.
41. **No `ReasonCompensation` in `processtest`** — see backlog 19.

## Where things live

| | |
|---|---|
| `main` | ADR-0181/0182 merge `1ac140f6` is the newest shipped code. ⚠ Never quote main's HEAD; re-derive |
| **bundle C** | `feat/compensation-failure-retry-and-visibility` — ⚠ **ONE COMMIT, LOCAL ONLY, NOT PUSHED.** Spec/plan `docs/{specs,plans}/2026-08-13-compensation-failure-retry-and-visibility.md`; ADR `docs/adr/0179-*.md`; evidence `docs/specs/2026-08-13-adr-0179-premise-evidence.md`; audits `docs/specs/2026-08-1{3,4}-adr-0179-*lens-{a,b,c}.md`; adjudication `docs/specs/2026-08-14-adr-0179-audit-adjudication.md` |
| *(merged branches)* | Deleted once pushed. **`origin` carries only `main`** plus dependabot. ⚠ **Every unmerged branch, including bundle C, exists on this machine ONLY** |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only; holds ADR **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main`, NOT pushed |
| worktrees | ✅ **CLEAN** — `git worktree list` shows only the primary checkout |
| Latest ADR | **0182** shipped; **0179** implemented-not-merged. Next free is **0183** |
| v0.1.0 | not tagged |

## Standing constraints

- **Docker: standing permission for the Verification coverage + no-regressions runs** (owner,
  2026-08-11). Probe and run; if unavailable, say so and let the owner start it or skip, labelling
  any container-free subset as the partial result it is. ⚠ Everything else asks, and **a subagent
  brief must say so explicitly**.
- **`golangci-lint`: probe and run; if absent, offer to install or skip** — never substitute
  `go vet`, never claim "lint clean" for a run that did not execute.
- **Container-free packages**: `engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`,
  `transport/http`. ⚠ **`./runtime/...` as a whole is NOT**, and ⚠ `internal/persistence/store` is
  NOT — **but `RunTestSQLite` is pure-Go and starts no container**.
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones — cheap proof that a
  breaking type or symbol change has no hidden consumer. It compiles them; it does not run them.
- **Judge a test run by its exit code**, never a pipeline tail; use `-count=1`.
- **Run the suite on the MERGED tree**, and re-run after any `/code-review` fix.
- `/code-review` and `/security-review` are **owner-invoked only**.
- **Fan out subagents by Go package.** A delivery entirely inside one package runs **strictly
  serial**. ⚠ Bundle C's wave 2 ran three packages concurrently and one agent saw a transient build
  failure from another's mid-edit file — expected, cleared on retry, but do not read a `go build`
  failure as your own during a parallel wave.
- **An agent that must measure against a patched tree gets a `git worktree`**, the brief must say
  so, *and* must require verifying the bundle is present as step 0.
- ⚠ **Brief long-running agents to persist findings per finding, before the next probe.**
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`.**
- ⚠ **`git reset --soft main` on a branch cut from an OLDER main stages a REVERT.** Check
  `git merge-base HEAD main` before any soft reset.
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
