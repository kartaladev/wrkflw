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

## State — updated 2026-08-12

**▶ NOTHING IS IN FLIGHT.** `main` is clean and pushed; there is no half-finished work.

**`main` is clean and pushed. Its head SHA is deliberately NOT quoted here** — every edit to
this file changes it, and quoting it is how this line went stale three times in one hour.
Re-derive it: `git rev-parse --short main`.

The stable anchors instead: **ADR-0175 merged at `6e4addc8`** (a merge commit, so it never
moves), on top of **`5270838`** (ADR-0174). Anything after `6e4addc8` on `main` is
documentation follow-ups only — no code.

**Latest ADR = 0175; next free = 0176.** ADR numbers 0155–0157 remain reserved by the parked
`feat/durable-waiters-delivery-correctness`.

### What ADR-0175 shipped

Opt-in detection of a stalled compensation walk — one whose dispatched compensation action
never reports back, which was measured to be **permanently stuck AND permanently invisible**
(`tokens=0 timers=0 incidents=0`, both waiter sets empty, every operator lever a silent no-op).

- `StepOptions.CompensationStallAfter` — **zero disables, and zero is the default**, so nothing
  changes for a consumer who does not opt in. Fed by `runtime.WithCompensationStallTimeout(d)`.
- A `TimerCompensationStall` armed at **all three** compensation dispatch sites; on breach it
  raises a walk-scoped `IncidentCompensationStall` (`TokenID` empty) and emits **no commands**.
- Three operator verbs on one cursor-matched trigger — **retry / skip / abandon** — through
  `ProcessDriver.ResolveCompensationStall` → `service` → `POST
  /admin/instances/{id}/compensation/resolve-stall` (mounted in all three adapters'
  `AdminRoutes`, which are deliberately NOT part of `Mount`).
- `resolve-incident` now REFUSES a stall incident instead of silently eating it.
- `service.ProcessInstance` projects `compensating.{active_command_id,since,scope_id}` and
  `incidents[].kind`.

**Gates:** `/code-review` 9 findings — 6 fixed, 3 adjudicated in the spec (§8, 4d–4k);
⚠ the record originally listed only FIVE of the six fixes — the `processtest.ReasonIncident`
doc fix shipped unlisted and was added in a follow-up. *Recount your own summary numbers
against the list they summarise.*
`/security-review` **0 findings**. `go test -race ./...` EXIT=0 over 64 packages, no races;
repo coverage 74.2 % (baseline unchanged); `golangci-lint run ./...` clean. Verified on the
MERGED tree before pushing.

### ⚠ Process lessons this delivery earned (they cost real rework)

1. **An audit's own fix can invalidate another claim in the same bundle.** C3 restricted
   `abandon` to `walkAdmin`, which silently falsified the ADR's separate claim that abandon
   discharges the deferred-cancel deadlock — `PendingCancel` is only ever stamped on walks that
   RESUME, exactly the set C3 refuses. **`skip` is that verb.** When a finding changes a
   decision, re-verify every other sentence that mentions it.
2. **"Deleting a line left the suite green" means UNTESTED before it means REDUNDANT.** I drew
   the wrong conclusion and wrote it into the ADR, spec, plan and a code comment;
   `/code-review` caught it. `TestAbandonRetiresTheStallIncident` now exists and is RED without
   the line.
3. **Two PRESCRIBED mutations were the wrong mutation.** Both named an ORDERING as
   load-bearing; reordering stayed green because `Step` returns the zero `StepResult` on error,
   so the caller discards the mutated clone. A mutation that cannot discriminate is evidence
   the CLAIM is wrong, not that the test is weak.

### ▶ NEXT WORK — pick from the blockers and backlog below

No delivery is queued. The strongest candidates, in rough order:

1. **Blocker 2** — a zero `next_run` cannot be armed on MySQL (`Error 1292`). Small, self-
   contained, needs a reject-vs-normalise ADR.
2. **Blocker 9 / backlog 3c** — an engine-side `TimerWaiters()`. ADR-0175 added
   `engine.InstanceState.HasArmedTimers()` and inherited this gap; closing it should extend
   that method.
3. **Backlog 16 + 4k** — the retry/incident story for a compensation action returning
   `ActionFailed`, now adjacent to ADR-0175's incident kind, plus the late-reply
   `ErrTokenNotFound` shape `/code-review` surfaced.

## Pre-v0.1.0 blockers

1. ✅ **Strict definition decoding — CLOSED by ADR-0167.** ⚠ Does **not** close the
   fail-open `AuthzSpec`: an empty spec, `eligible_roles: []`, a bare `eligible_roles:`
   and `eligible_roles: null` all parse cleanly and mean allow-all. Own ADR.
   🚨 **Before DEPLOYING ADR-0167**: audit stored definition rows for 5 pre-ADR-0144
   camelCase keys (`compensateAction`, `compensationAction`, `completionAction`,
   `correlationKey`, `messageName`) — rows carrying one stop loading.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go` arms a zero
   `nextRun` when `TriggerSpec.Next` reports `ok == false`; `DATETIME(6) NOT NULL`
   rejects it (Error 1292). Postgres and SQLite are fine. Reject-vs-normalise ADR.
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

## Where things live

| | |
|---|---|
| `main` | **ADR-0175 is the newest SHIPPED bundle**, merged `--no-ff` at `6e4addc8` and pushed. Commits after it are docs-only. ⚠ Never quote main's head here — re-derive: `git rev-parse --short main` |
| *(merged branches)* | Deleted once pushed; history is in `main`. **`origin` carries only `main`** plus dependabot branches |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash history, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only. Owner DECIDED not to push it. Holds ADR numbers **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main` and NOT pushed |
| worktrees | ✅ **CLEAN** — `git worktree list` shows only the primary checkout. The three stale ones under `…/87601c38-…/scratchpad/wt-{design,premise,tests}` were removed 2026-08-12 with the owner's approval, after re-verifying `git status --porcelain` was empty in each immediately beforehand. Their commit `33e4692` (superseded ADR-0168/0169 pre-merge history) remains in the object store; its content shipped via `b12bba3`. |
| Latest ADR | **0175** (SHIPPED). Next free is **0176** |
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
- **ADR-0175's audit record** — `docs/specs/2026-08-12-adr-0175-audit-evidence.md`.
- **ADR-0174's implementation record** —
  `docs/plans/2026-08-11-dying-instance-harvests-open-scopes.md` `▶ Progress`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
