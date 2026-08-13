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

## State — updated 2026-08-13

**▶ NOTHING IN FLIGHT. `main` is clean, pushed, and has no uncommitted or unmerged work.**

**ADR-0176 SHIPPED** — merged `--no-ff` at **`52bf0f80`** and pushed 2026-08-13. Its branch
`feat/never-due-timer-triggers` was deleted on merge, per the standing convention.

Both gates passed: **`/code-review` 5 findings** — 4 fixed, 1 fixed-in-part with the remainder
explicitly declined; **`/security-review` 0 findings**. Detail lives in that delivery's plan
`▶ Progress` (`docs/plans/2026-08-13-never-due-timer-triggers.md`) and in measurements §16–§17.

**Verified ON THE MERGED TREE before pushing** (Docker up, nothing skipped, judged by exit code):
`go test -race ./...` EXIT=0 over **64 packages, no races**; coverage **74.4 %** repo-wide
(baseline 74.2 %), `runtime` **93.4 %**, `scheduler` **93.1 %**; `go build` and `go vet ./...`
EXIT=0; `golangci-lint run ./...` **repo-wide 0 issues**.

### What ADR-0176 shipped

**The runtime never persists or activates a timer the scheduler cannot run**, in two ordered
parts — the order is load-bearing and P1 landed first:

1. **`scheduler.Trigger.Next` now agrees with the live scheduler** (ADR-0140's own contract).
   Empty weekday / day-of-month sets take the substituted Sunday and 1st; a negative
   day-of-month counts back from month end per candidate month; a cron with no findable
   occurrence reports `!ok` instead of `(zero, true)`; and `calendarNext`'s weekly branch is now
   a **transcription of gocron's `weeklyJob.next`** rather than a scan.
2. **Four arm sites guard on `neverDueNextRun` (`!ok || next.IsZero()`)** — `timerJobsFor`,
   `scheduleStartTimerJob`, `jobStore.Load`, and a re-check immediately before `Activate` —
   each refusing with a WARN, no arm, no row, and a `wrkflw_timer_arms_refused_total` increment.

That closes **blocker 2** on all three dialects and the **whole-process livelock**, the latter
on *both* reachable paths: a fresh arm and a boot rehydration. Each has a regression test that
**hung for its full 10 s timeout before the fix and returns in under a second after** —
demonstrated, not asserted.

🚨 **The livelock was a real availability defect in shipped code**: `Activate` on
`Monthly(12,[31])` never returns, because gocron v2.22.0's `monthlyJob.next` has an unbounded
`for next.IsZero()` loop (confirmed in its source at `job.go:1469-1474`) inside gocron's single
`selectNewJob` goroutine — so every arm, cancel and rehydrate in the process blocks behind it.
**Clock-month dependent**: only the five months without a 31st. ⚠ It inverts the dialect
framing — **MySQL was accidentally the SAFE dialect**, because its commit fails before
`Activate` is reached.

### ⚠ Process lessons this delivery earned

1. **Implementation refuted FOUR of the audited bundle's own claims** — an audit that ran three
   Opus lenses and accepted 8 Criticals still shipped four false premises into implementation.
   Two of them would have produced **vacuous or mis-aimed tests**. See the plan's `▶ Progress`
   and measurements §15. The generalisable one: *a guard's justification is a claim about
   current behaviour and needs executing like any other.*
2. **"Executed" is only as good as the call path you executed** — the pre-audit design probed
   `Trigger.Next` when the thing that arms is `Activate`. This delivery repeated the lesson at
   a smaller scale: the start-timer path's "ungated today" claim was true of the *port* and
   false of both *implementations*, and only running it separated them.
3. **A behaviour change shows up as an existing test failing.** Exactly two `scheduler` tests
   moved, both asserting the divergence being fixed. That *no other test in the repo moved* is
   the evidence the weekly rewrite is behaviour-preserving — a stronger signal than any
   argument about the rewrite.
4. **A timing bound measured in one mode is a claim about that mode only.** The new scan-cost
   guard was tuned at 3 s against a 0.39 s measurement, and the full `-race` run failed it:
   `-race` is ~8x slower, so the *passing* case cost 3.17 s there. Retuned only after measuring
   BOTH modes, and mutation-verified RED under `-race`.
5. **`/code-review` earns its place at the gate.** It found two Major defects — including a
   TOCTOU in the very guard this delivery is about — that neither the design audit nor
   implementation caught, and it independently fuzz-verified the parts that were right.

**`main` is clean and pushed. Its head SHA is deliberately NOT quoted here** — every edit to
this file changes it, and quoting it is how this line went stale three times in one hour.
Re-derive it: `git rev-parse --short main`.

The stable anchors instead, all merge commits so they never move: **ADR-0176 at `52bf0f80`**
(the newest code on `main`), on top of **ADR-0175 at `6e4addc8`**, on top of **`5270838`**
(ADR-0174). Anything after `52bf0f80` is documentation follow-ups only.

**Latest ADR = 0176 (SHIPPED); next free = 0177.** ADR numbers 0155–0157 remain reserved by the
parked `feat/durable-waiters-delivery-correctness`.

### ▶ NEXT WORK — pick one and start; nothing is half-done

1. **Blocker 9 / backlog 3c** — an engine-side `TimerWaiters()`. ADR-0175 added
   `engine.InstanceState.HasArmedTimers()` and inherited this gap; closing it should extend
   that method.
2. **The four gates ADR-0176 deliberately deferred** (backlog 22), each for a measured defect —
   a deploy-time `model.Validate` gate, a step-time engine gate, `StepOptions.SchedulingLocation`,
   and migrating the orphan zero-`next_run` rows. Their own ADR.
3. **Backlog 16 + 4k** — the retry/incident story for a compensation action returning
   `ActionFailed`, plus the late-reply `ErrTokenNotFound` shape `/code-review` surfaced.
4. **Backlog 0/1/2 from the ADR-0175 audit** — three pre-existing measured defects,
   including the `TimerInWait` reminder that fires a real `InvokeAction` on a dying instance.
5. 🆕 **Bound a calendar trigger's `interval`** — closes backlog 26 (scan cost still linear in
   `interval`) and 30 (`int(interval)*7` overflow returning a PAST instant with `ok=true`)
   together. Both are consequences of the same unvalidated `uint`, and both are cheap to close
   at the constructor or in `Next`. Small, self-contained, well-measured — a good first task for
   a fresh session.

## Pre-v0.1.0 blockers

1. ✅ **Strict definition decoding — CLOSED by ADR-0167.** ⚠ Does **not** close the
   fail-open `AuthzSpec`: an empty spec, `eligible_roles: []`, a bare `eligible_roles:`
   and `eligible_roles: null` all parse cleanly and mean allow-all. Own ADR.
   🚨 **Before DEPLOYING ADR-0167**: audit stored definition rows for 5 pre-ADR-0144
   camelCase keys (`compensateAction`, `compensationAction`, `completionAction`,
   `correlationKey`, `messageName`) — rows carrying one stop loading.
2. ✅ **A never-due timer arm — CLOSED by ADR-0176, ON `main`.** The zero `next_run` MySQL
   rejects (Error 1292 — ⚠ the *literal*, not a range floor: year-1 and year-999 were measured
   as accepted) is no longer produced, on any dialect.
2b. ✅ **The `scheduler.Activate` livelock on `Monthly(12,[31])` — CLOSED by ADR-0176, ON
   `main`**, on both the fresh-arm and boot-rehydration paths, each proven by a test that hung
   its full timeout before the fix and returns in under a second after.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.
4. ✅ **ADR-0159's misnamed symbols — CLOSED.** It was **three**, not two.
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky**
   (`internal/persistence/store/notifier_pgx_test.go`). Interacts with item 7;
   **do not silence it** — it guards NOTIFY-wakeup vs a 30s poll.
6. ✅ **`processtest` cannot drive an arm-only park — CLOSED by ADR-0166.**
7. **Suite speed.** `internal/dbtest`'s `sync.Once` boot fires per package → 12
   Postgres + 7 MySQL boots (~60s of a ~2min suite). Fix: honour
   `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   fallback, plus `scripts/testdb.sh up|down` and CI service wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
   Cheapest fixture: `engine/step_terminal_test.go`'s force-termination tests.
9. **`Park.HasArmedTimers` has blocker 6's defect for TIMER arms — still OPEN.** It now
   delegates to the new `engine.InstanceState.HasArmedTimers()`, which reads `s.Timers` only,
   so a boundary or event-gateway timer arm is still invisible. Needs an engine-side
   `TimerWaiters()`, which should extend that method. ✅ The ADR-0175 half is DONE:
   `TimerCompensationStall` is excluded, so `processtest.AutoTimers()` no longer fires stall
   timers by itself (measured RED without the fix).

## Backlog

**Opened by the ADR-0175 audit — three PRE-EXISTING defects in shipped code:**

0. ⚠ **A `TimerInWait` reminder fired on a `spawnsNewWork()==false` instance emits a real
   `InvokeAction`** — an ADR-0172 hole in `handleTimerFired` path 4, measured. Reachable
   via a throw walk carrying `PendingCancel`. Own ADR.
1. **`engine/step_triggers.go:291` cites a nonexistent `ADR-0034 §2.5`** — that ADR has no
   numbered sections (`grep -c "§"` → 0); the contract is **Decision 4**. This false
   comment propagated six times into the ADR-0175 bundle before the audit caught it.
2. **`StartInstance` is accepted on a `compensating` instance** and restarts it from the
   top with a stale cursor (measured). Reachable through `engine.Step`/`ApplyTrigger`, not
   `Drive` (which fails with `ErrInstanceExists` — measured). On a resuming walk it also
   leaves `PendingCancel=true` — same defect as item 12 below.
3. **`CancelInstance` reports success for a cancel that did nothing.**

**Opened by ADR-0175's IMPLEMENTATION (new, all measured):**

3b. **With detection OFF, the cancel path already flips `s.Timers` from nil to an empty
    non-nil slice** — `beginCompensation`'s prologue runs `cancelTimersByTaskID` for the
    parked human task. Measured on `main` @ `5270838`: `timersNil=false`. Pre-existing
    stored-shape drift, unrelated to this delivery, not fixed here.
3c. **`engine.InstanceState.HasArmedTimers()` is new and carries blocker 9's gap.** It reads
    `s.Timers` only, so a boundary / event-gateway / event-sub-process timer arm stays
    invisible to it. It exists because `timerRecord.Kind` is unexported and `processtest`
    physically cannot exclude a stall timer on its own. Closing blocker 9 means an
    engine-side `TimerWaiters()` and should extend this method.
3d. **The instance document gains fields**: `incidents[].kind`, and a `compensating` object
    (`active_command_id`, `since`, `scope_id`). Additive, but a consumer pinning the document
    byte-for-byte will see them.

**Adjudicated at ADR-0175's `/code-review`, recorded in that spec §8 4i–4k:**

3e. **`service.Service` gained a method — a BREAKING interface change** for a consumer who
    implements or decorates it. Needs a release note.
3f. **The stall incident's `ScopeID` is empty for a TARGETED compensation throw** — read
    `NodeID` instead; an empty `ScopeID` is ambiguous with the root scope.
3g. **A late reply to a superseded compensation command returns `ErrTokenNotFound`** rather
    than being treated as a benign duplicate. Belongs with item 16.

**Deferred from ADR-0175's design:**

4. A **per-node `CompensationStallAfter`** tier, mirroring `effectiveRetryPolicy`'s
   three-tier chain. A flat engine-wide window cannot fit both a millisecond ledger
   reversal and an hours-long manual-approval refund.
5. A **bound on repeated `retry`** (a `StallRetries` counter stamped into
   `Incident.Attempts`).
6. Whether stall **detection should default ON** — the operators who most need it are the
   ones least likely to enable it.

**From ADR-0174:**

7. 🆕 **A pre-ADR-0171 unpinned cursor keeps ADR-0173's accepted double-run at the
   `endInstance` harvest.** Belongs with a **cursor-migration ADR** closing both bounds
   together. `/security-review`: REAL-BUT-NOT-SECURITY, 2/10.
8. **Records already stranded on pre-ADR-0174 rows stay unreachable** — deliberate. An
   opt-in admin operation is the plausible shape. Own ADR.
9. **ADR-0164's "eight terminal sites" is stale** — ten `endInstance` call sites today.
10. **`compensationRecordsForScope` reads an open scope as a records-exist decision** — an
    enumeration hazard, invisible to the obvious predicate grep.

**From ADR-0158/0172:**

11. **A flow targeting a NON-EXISTENT node parks a permanent wedge** (measured; it does
    **not** error, contrary to the parked plan's claim).
12. **`PendingCancel=true` survives onto a `Running` instance forever** — the operator's
    cancel is silently lost and **will terminate the NEXT throw or reverse walk**.
13. **Micro mode loses a signal delivery.** Pre-existing.
14. **`runtime/processdriver_action.go:485`'s comment is FALSE** — doc-only.
15. **`engine/step_nodes.go:501`'s nested arm retirement is entirely uncovered.**

**From ADR-0168–0171:**

16. **A retry / incident story for a FAILED compensation action.** Ownership transfers on
    DISPATCH, so an action returning `ActionFailed` has its record consumed and is never
    retried, with no incident raised. Measured `re-dispatched=[]` where `main` gave
    `[undoB undoA]`. ⚠ **Now adjacent to ADR-0175**, which gives compensation an incident
    kind — but it changes ADR-0034 Decision 4's contract, so it stays its own ADR.
17. **The event-sub-process hole's remaining direction** — `walkTerminates` structurally
    cannot see it.
18. **ADR-0171's two open bounds**, both pinned as falsifiable `KNOWN LIMITATION`
    assertions. ⚠ **ADR-0168's conjunct 3 is uncovered** — kept deliberately;
    *undemonstrated is not unreachable*.
19. **`processtest.Classify` has no reason for a compensation-walk park** — measured
    `reason="unknown"`; ADR-0168 only **pins** it. ⚠ ADR-0175 changes this to
    `ReasonIncident` once detection is on — see blocker 9.
20. **Repo-wide coverage ~74 %** — long pre-existing, not a regression; untested
    `examples/` and transport adapters are the drag. ⚠ `service` ~52.6 %.
    `scripts/coverage.sh` only REPORTS — its exit code proves nothing.
21. **`AUDIT.md`** — 747-line adversarial architecture audit on `docs/architecture-audit`,
    ⚠ NOT on `main`, NOT pushed (public repo; unfixed Critical/High findings). ⚠ Treat its
    claims as **unverified**.

**From ADR-0176:**

22. **The four gates it deliberately deferred**, each for a *measured* defect in the first
    design — a deploy-time `model.Validate` gate (its prescribed mechanism was an **import
    cycle**; the wire form via `toWire` is the real one, and it must live in
    `validateStructure` or every nested sub-process is exempt), a step-time engine gate (**wedges
    a running instance**), `StepOptions.SchedulingLocation` (only a step-time gate needs it),
    and migrating existing orphan zero-`next_run` rows. Their own ADR.
23. **Orphan rows are not reclaimed.** The rehydration guard stops them wedging boot, but
    nothing deletes them — `PruneTimers` was measured deleting **1 of 5**. Manual remediation
    only. Pairs with item 22.
24. **A refused arm leaves the engine's in-memory `timerRecord` with no durable row**, so
    `HasArmedTimers()` over-reports and the parked token is *less* diagnosable than the poisoned
    row was. Making it visible needs a post-step incident channel that does not exist.
25. ✅ **`EveryRandom(min>max)` — CLOSED** at ADR-0176's `/code-review`: `Next` now refuses
    `min >= max`, matching gocron. It could never have been caught by a `next_run`-keyed guard,
    because its `next_run` is non-zero.
26. **The calendar scan is still linear in a consumer-supplied `interval`** — but at MONTH, not
    day, granularity after ADR-0176's `/code-review` (off-grid months are skipped whole):
    `Monthly(120000,[31])` went 6.34 s → 392 ms. ⚠ Also now a **`Daily`/`Monthly`-only** concern,
    since the weekly branch no longer scans at all. Closing it fully needs day-count arithmetic
    across DST in the highest-risk function.
27. **The definition store round-trips semantically invalid definitions** — a stored definition
    is not re-validated on load.
28. ⚠ **A weekday set mixing in-range and out-of-range weekdays changed answer** in ADR-0176
    (it now reports gocron's instant, not the in-range one). `Trigger.Next` is exported —
    **release note**. The same note covers the eight shapes ADR-0176's `/code-review` moved from
    "reports an instant" to "never due": bad monthly day entries, `EveryRandom(min>=max)`, and
    out-of-range at-times.
29. ⚠ **The arm guard is not atomic with the arm** — `activateJob` re-derives from the trigger at
    gocron's own clock reading, so a trigger that goes never-due between the check and that
    reading can still reach the unbounded search. ADR-0176 narrowed the window from a whole
    commit to a few instructions; closing it needs gocron to honour the instant it is handed.
30. **`weeklyNext`'s `int(interval)*7` overflows** at an interval ≥ ~1.3e18, returning a PAST
    instant with `ok=true` (measured: anchor 2026-08-13 → 2026-08-10). Adjudicated NOT a
    security finding at `/security-review` — no trust boundary is crossed and gocron's own
    algorithm overflows identically, so the two still agree — but it is a real correctness wart.
    Pairs with item 26 (both want `interval` bounded).

## Where things live

| | |
|---|---|
| `main` | **ADR-0176 is the newest SHIPPED bundle**, merged `--no-ff` at `52bf0f80` and pushed; ADR-0175 is `6e4addc8` beneath it. ⚠ Never quote main's HEAD here — re-derive: `git rev-parse --short refs/heads/main` (the bare name `main` has been seen to fail rev-parse; use the full ref) |
| *(merged branches)* | Deleted once pushed; history is in `main`. **`origin` carries only `main`** plus dependabot branches |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash history, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only. Owner DECIDED not to push it. Holds ADR numbers **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main` and NOT pushed |
| worktrees | ✅ **CLEAN** — `git worktree list` shows only the primary checkout. The three ADR-0176 audit worktrees under `.claude/worktrees/agent-*` were removed 2026-08-13 after verifying `git status --porcelain` was empty in each and that their audit records are committed in-repo. The three older ones under `…/87601c38-…/scratchpad/wt-*` went the same way on 2026-08-12. |
| `worktree-agent-{a2454…,a4a2d…,ac702…}` | 🧹 three LOCAL leftover branches from the ADR-0176 audit worktrees (worktrees already removed). Their content — the three audit-lens documents — **is on `main`**, verified with `git ls-files`. Safe to `git branch -D`; left in place because deleting them was not asked for |
| Latest ADR | **0176** (SHIPPED at `52bf0f80`). Next free is **0177** |
| v0.1.0 | not tagged |

## Standing constraints

- **Docker: standing permission for the Verification coverage + no-regressions runs**
  (owner, 2026-08-11). Probe the daemon and run; if unavailable, say so and let the owner
  start it or skip, labelling any container-free subset as the partial result it is.
  ⚠ Everything else still asks, and **a subagent brief must say so explicitly**.
- **`golangci-lint`: probe and run; if absent, offer to install (agent or owner) or to
  skip** — never substitute `go vet`, never claim "lint clean" for a run that did not
  execute.
- **Container-free packages**: `engine`, `runtime/{calllink,signal,task}`, `service`,
  `processtest`, `transport/http`. ⚠ **`./runtime/...` as a whole is NOT**, and ⚠
  **`internal/persistence/store` is NOT** (25 test files import `dbtest`).
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones — the cheap
  way to prove a breaking type change has no hidden consumer.
- **Judge a test run by its exit code**, never a pipeline tail; use `-count=1` (a cache
  hit can report EXIT=0 over panicking code).
- **Run the suite on the MERGED tree**, and **re-run after any `/code-review` fix**.
- `/code-review` and `/security-review` are **owner-invoked only**. Adversarial Opus
  stand-ins first anyway — but they are **not** the gate.
- **Fan out subagents by Go package.** A delivery entirely inside `engine` runs
  **strictly serial**.
- **An agent that must measure against a patched tree gets a `git worktree`**, and the
  brief must say so — *and* must require verifying the worktree contains the bundle.
  ⚠ **All three ADR-0175 audit worktrees were created WITHOUT it.** That step-0
  instruction is what saved the audit; keep it in every brief.
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`** — it restores
  from the INDEX and has destroyed uncommitted work twice here.
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **ADR-0175's build-affecting audit outcomes** —
  `docs/plans/2026-08-12-stalled-compensation-walk-escape.md` `▶ Progress`.
- **ADR-0176 (in flight)** — spec `docs/specs/2026-08-13-never-due-timer-triggers.md`, plan
  `docs/plans/2026-08-13-never-due-timer-triggers.md` (`▶ Progress` first), audit
  `docs/specs/2026-08-13-adr-0176-audit-lens-{a,b,c}.md`, raw measurements
  `docs/specs/2026-08-13-adr-0176-measurements.md` — ⚠ read that file **backwards**: its
  **§15 (what implementation refuted) and §14 (the arming instants) supersede §13, which
  supersedes §1–§10**.
- **ADR-0175's audit record** — `docs/specs/2026-08-12-adr-0175-audit-evidence.md`.
- **ADR-0174's implementation record** —
  `docs/plans/2026-08-11-dying-instance-harvests-open-scopes.md` `▶ Progress`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
