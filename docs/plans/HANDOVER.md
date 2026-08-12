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

**▶ ONE BUNDLE IS IN FLIGHT: ADR-0175 — IMPLEMENTED, NOT pushed, awaiting the two
owner-invoked review gates.**

**`main` is unchanged at `5270838`** (ADR-0174, pushed). ⚠ Re-derive every SHA:
`git rev-parse --short main`.

**Branch `feat/stalled-compensation-walk-escape`** — one commit,
`feat(engine): an operator escape from a stalled compensation walk (ADR-0175)`.
⚠ Re-derive its head yourself; it is amended on every change. 57 files, ~4.7k insertions:
the spec, ADR-0175, the plan, the audit record, and the full implementation across
`engine`, `internal/persistence/store`, `runtime`, `service`, `processtest` and all three
HTTP adapters.

**Latest ADR = 0175; next free = 0176.** ADR numbers 0155–0157 remain reserved by the
parked `feat/durable-waiters-delivery-correctness`.

### ▶ NEXT WORK — the delivery gate, items 4 and 5 only

Verification items 1–3 already PASS on the branch:

- `go test -race -count=1 ./...` → **EXIT=0 over 64 packages, no races**
- repo coverage **74.2 %** (the long-standing baseline, unchanged), engine **92.6 %**,
  runtime **93.3 %**, `transport/http/httpcore` **94.2 %**, `processtest` **90.2 %**,
  `service` **53.5 %** (up from ~52.6 %)
- `golangci-lint run ./...` → **0 issues**; `go vet ./...` clean

✅ **`/code-review` has RUN. Nine findings; six accepted and FIXED, three adjudicated
out-of-scope and documented in the spec (§8, items 4d–4k).** The suite was re-run green after
the fixes and they are folded into the feature commit.

⚠ **The most important finding was a correction to this session's own work**: I had claimed
abandon's incident sweep was redundant "because `endInstance` covers it". It does not —
`stepCompensationFinish` clears the cursor BEFORE `applyFinish`, so `endInstance`'s sweep
early-returns. I inferred "redundant" from a green suite after deleting the line, when the
truth was that NOTHING ASSERTED IT. `TestAbandonRetiresTheStallIncident` now exists and is RED
without it, and the ADR/spec text has been restored to its original (correct) form.

✅ **`/security-review` has RUN: ZERO findings.** The new admin endpoint's authorization
posture is identical to its siblings (no admin operation in this repo passes through
`authz.Authorizer` — the known pre-existing IDOR/BOLA, inherited not widened); `abandon` adds
no privilege class beyond the existing `/admin/instances/{id}/cancel`, and `retry` none beyond
`/admin/instances/{id}/incidents/{id}/resolve`; neither verb lets a caller choose WHAT runs
(the action name and input come from the stored record). `ParseCompensationDisposition` fails
closed and the zero value is `retry`, not `abandon`. `UnmarshalTrigger` is reached only by the
read-only history reader, so the codec change is not a replay surface.

⚠ It also corrected one of MY comments: `compensating.active_command_id` is **not** an
`<instance>-cN` sequence oracle in a product deployment — that form is only the fallback used
with no `IDGenerator`; the runtime always injects xid (ADR-0149), and the same generator mints
the token/task ids the document already exposes. Fixed in `service/instance.go`, the ADR and
the spec.

What remains is **owner-invoked**: ⚠ This delivery adds an admin endpoint whose `retry` verb is a
   remote **re-execution** primitive and whose `abandon` verb is **destructive and
   irreversible**, on a surface with two known-open authz defects (self-asserted actor
   identity, fail-open `AuthzSpec`). It also projects an internal `<instance>-cN` sequence
   oracle as `compensating.active_command_id` — a deliberate trade, flagged for the reviewer.
3. Then merge `--no-ff` to `main` and push.

### ⚠ What this delivery corrected about its own audited design

Six claims died on execution. All are amended in the ADR and spec (marked **CORRECTED BY
IMPLEMENTATION**) and listed with measurements in the plan's `▶ Progress`. The two that
matter most to a reader of ADR-0175:

1. **`abandon` does NOT discharge the deferred-cancel deadlock — `skip` does.** `PendingCancel`
   is only ever stamped on a walk that RESUMES, and the audit's C3 finding made abandon refuse
   exactly those walks. The ADR's two halves were incompatible as written and nobody
   re-checked the sentence when C3 landed.
2. **A prescribed mutation was the wrong one, twice** (Phase 1's finish-cancel ordering and
   Phase 4's `handleResolveIncident` guard position). In both cases the claimed-load-bearing
   ORDERING turned out not to be, and the real protection lay elsewhere. Both are now stated
   accurately, with the mutation that actually reddens named.

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
| `main` | **ADR-0174 is the newest SHIPPED bundle**, merged `--no-ff` and pushed. ⚠ Re-derive: `git rev-parse --short main` |
| **`feat/stalled-compensation-walk-escape`** | **The ADR-0175 bundle: IMPLEMENTED and verified, NOT pushed. Awaiting `/code-review` + `/security-review`.** ⚠ Re-derive the head SHA; it is amended on every change |
| *(merged branches)* | Deleted once pushed; history is in `main`. **`origin` carries only `main`** plus dependabot branches |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash history, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only. Owner DECIDED not to push it. Holds ADR numbers **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main` and NOT pushed |
| ⚠ stale worktrees | THREE from an earlier session remain under `…/87601c38-…/scratchpad/wt-{design,premise,tests}`, all at `33e4692`, zero uncommitted files, NOT an ancestor of `main`. Safe to `git worktree remove --force` each; left because they belong to another session. (This session's three audit worktrees were removed.) |
| Latest ADR | **0175** (in flight, implemented, unpushed). Next free is **0176** |
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
