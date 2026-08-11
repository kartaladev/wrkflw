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

**▶ NOTHING IS IN FLIGHT.** The working tree is clean and `main` carries the newest
delivery, pushed.

**▶ Latest shipped: ADR-0174** — *a dying instance harvests its open scopes, then closes
them*. ⚠ **Re-derive every SHA**: `git rev-parse --short main`. Before it: ADR-0173
`cae9971`, ADR-0158 + ADR-0172 `fb60df0`, ADR-0168/0169/0170/0171 `b12bba3`, 0167
`44b3163`, 0166 `9e96112`, 0165 `ec25ffd`.

**▶ Next work: pick from `## ▶ NEXT WORK` below.** Latest ADR is **0174**; next free is
**0175**. ADR numbers 0155–0157 remain reserved by the parked
`feat/durable-waiters-delivery-correctness`.

### What ADR-0174 shipped, and what it BREAKS

`Scope.Compensations` reaches `ArchivedCompensations` only on a **normal** scope exit. A
terminal transition closes no scope, and every dying-instance records-exist reader
consults only the archive and `RootCompensations`. Measured on `main`: an unhandled error
or an operator cancel inside a sub-process holding a compensable record emitted
`FailInstance` and **no compensation action at all**; a later admin rollback was refused
as *"nothing left to compensate"*. `undo-inner` never ran and never could — a booking
made and never undone.

Now: one helper (`harvestOpenScopeCompensations`) reuses the existing
`archiveCompensations` per open scope at **five** sites, and `endInstance` sets
`s.Scopes = nil` — which also discharges the zombie-scope debt ADR-0162 deferred to
ADR-0164 and 0164 shipped without.

⚠⚠ **BREAKING, release-note material.**

- An unhandled error or cancel inside a record-holding sub-process no longer terminates
  immediately; it **compensates first**. In-flight instances change behaviour on their
  next terminal transition — that is the fix working.
- **An emitted event payload changes**: the walk cancels the surviving token, so its
  incident is retired and `runtime/outbox.go`'s `terminalEventErr` reports differently
  (`incidents=1 → 0`).
- The persisted `Scopes` shape changes from `[]` to `null` on **every** terminal
  transition, ordinary completions included. Inert (no reader outside `engine/`), and it
  matches ADR-0173's normalisation — chosen deliberately.
- A rollback on a `failed` instance can now flip it to `terminated`, dropping surviving
  tokens and moving `EndedAt`.

⚠ **Bound accepted, not fixed** (spec §5.3.1): a cursor persisted **before ADR-0171**
(`Records == nil`) bypasses `partitionForLiveWalk`, so at a `forceTerminate` the harvest
re-archives its already-dispatched prefix and a rollback re-runs it. The class is
pre-existing on `main` via `cancelScopeSubtree`; fixing it would reverse ADR-0173's
documented preference (*"losing the record outright is worse"*) on untrusted indices.
`/security-review`: REAL-BUT-NOT-SECURITY, 2/10.

### ⚠ Five things about this delivery worth carrying forward

1. **The rule-#9 audit deleted a whole decision** — legacy-row recovery re-ran
   already-compensated actions (`[undoC undoB undoA]` where `[undoA]` was owed) and the
   dispatch record is unrecoverable. But **deleting it wholesale also cut two harvests
   that were safe and needed**, which `/code-review` then found: the fix was site-level
   harvests with a **terminality gate** (`ToNode == "" && ReverseNode == ""`), and a
   mutation removing that gate reddens the guard test.
2. ⚠⚠ **`git checkout <path>` restores from the INDEX and DESTROYS uncommitted work.**
   It bit **twice in one delivery** — the second time in the very session that had
   written the rule down. `git diff --quiet` then reports *"restored clean"* **because**
   the work is gone, and the next mutation's anchor silently fails. **Commit before
   mutating.**
3. ⚠ **An instrumentation FILTER can manufacture the premise it measures.**
   `if len(s.Scopes) > 0` gave 4 `endInstance` entries and a false universal; unfiltered
   gives **226**, with five live cursors.
4. ⚠ **A measurement can be false as LABELLED** — real numbers, wrong stated conditions
   (a probe seeded state the prose said was excluded). Not caught by "did you execute
   it?".
5. ⚠ **A false MECHANISM in a spec becomes a wrong TEST.** M7 named
   `removeOrphanedIncidents`; the real path is `cancelTokenWaits →
   removeIncidentsForToken`. A subagent asserted the spec's version and went RED.

**Delivery numbers:** 64 packages, 0 races, repo **74.2 %**, engine **92.7 %**, 11 tests
+ 3 gate-fix tests, **15 mutations**, `/code-review` 4 findings (3 fixed, 1 bound),
`/security-review` **0 vulns**.

Full detail: `docs/specs/2026-08-11-dying-instance-harvests-open-scopes.md`,
its audit record `docs/specs/2026-08-11-adr-0174-audit-evidence.md`, and the plan's
`▶ Progress`.

## ▶ NEXT WORK — pick one

Nothing is half-finished, so this is a genuine choice. In rough priority order:

1. **An operator escape from a stalled compensation walk** — a walk whose
   `ActionCompleted` never arrives leaves the instance permanently stuck, and
   `CancelRequested` emits ZERO commands. Own ADR. ⚠ ADR-0174 fixed what a dying
   instance OWES, not what happens when a walk never finishes.
2. **The remaining ADR owed by delivery 2b** — incident-history retention (owner chose
   REVISIT). ⚠ The zombie-scope half is now CLOSED by ADR-0174.
3. **A retry / incident story for a FAILED compensation action** — ownership transfers on
   DISPATCH, so an action returning `ActionFailed` has had its record consumed and is
   never retried, with no incident raised. Measured `re-dispatched=[]` where `main` gave
   `[undoB undoA]`.
4. **The event-sub-process hole's remaining direction** — `applyFinish` terminates a
   *resume* plan when the resume is dropped and no tokens remain; an arm firing there
   places a token and suppresses that recovery completion.
5. **The pre-ADR-0171 cursor migration story**, which would close ADR-0174's §5.3.1
   bound and ADR-0173's equivalent one together.
6. A pre-v0.1.0 blocker from the list below.

## Where things live

| | |
|---|---|
| `main` | **ADR-0174 is the newest SHIPPED bundle**, merged `--no-ff` and pushed. ⚠ Re-derive: `git rev-parse --short main` |
| *(merged branches)* | Merged delivery branches are deleted once pushed; their history is in `main`. **`origin` carries only `main`** plus dependabot branches |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash history, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only. Owner DECIDED not to push it. Holds ADR numbers **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main` and NOT pushed (public repo, open Critical/High findings) |
| ⚠ stale worktrees | THREE from an earlier session remain registered under `…/87601c38-…/scratchpad/wt-{design,premise,tests}`, all at `33e4692`, zero uncommitted files, NOT an ancestor of `main`. Safe to `git worktree remove --force` each; left because they belong to another session. (This session's own audit worktrees were removed.) |
| Latest ADR | **0174** (in flight). Next free is **0175** |
| v0.1.0 | not tagged |

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
9. **`Park.HasArmedTimers` has blocker 6's defect for TIMER arms — still OPEN.**
   `len(state.Timers) > 0` reads one source, so a boundary or event-gateway timer arm is
   invisible. Needs an engine-side `TimerWaiters()`. **Until then, no document may claim
   the ADR-0154 class is closed in `processtest` outright.**

## Backlog

**Opened by ADR-0174:**

0. 🆕 **A pre-ADR-0171 unpinned cursor keeps ADR-0173's accepted double-run at the
   `endInstance` harvest** (spec §5.3.1, measured). Fixing it means reversing ADR-0173's
   documented "losing the record outright is worse" preference on indices that are
   untrustworthy without a pinned snapshot — so it belongs with a **cursor-migration
   ADR** that closes both bounds together, not as a patch here. `/security-review`:
   REAL-BUT-NOT-SECURITY, 2/10.
1. **Records already stranded on pre-ADR-0174 rows stay unreachable** — deliberate;
   recovering them safely needs information the row does not carry. An opt-in admin
   operation is the plausible shape. Own ADR.
2. **ADR-0164's "eight terminal sites" is stale** — ten `endInstance` call sites today.
3. **`compensationRecordsForScope` reads an open scope as a records-exist decision**
   (`step_nodes.go:1204`; `:1160` is a fourth reader on the archive). Invisible to the
   obvious predicate grep — an enumeration hazard, not a defect.
4. **A `go test` cache hit can report `EXIT=0` over panicking code.** Worth adding to
   CLAUDE.md's Common Pitfalls.

**From ADR-0158/0172:**

5. **A flow targeting a NON-EXISTENT node parks a permanent wedge.** Measured: a
   `TokenWaiting` token with `AwaitCommand == ""` that nothing can resume, instance
   `running` forever. ⚠ The parked plan asserted this *errors*; it does not.
6. **Micro mode loses a signal delivery.** `snapshotIDs` is taken over tokens Micro has
   not driven to their park, so an intermediate catch sitting `TokenActive` with
   `AwaitSignal == ""` is missed while the signal is still consumed. Pre-existing.
7. **`PendingCancel=true` survives onto a `Running` instance forever** — the operator's
   cancel is silently lost and **will terminate the NEXT throw or reverse walk**.
8. **`runtime/processdriver_action.go:485`'s comment is FALSE** — it asserts
   `performThrowSignal` excludes the throwing instance; measured, it does not. Doc-only.
9. **`engine/step_nodes.go:501`'s nested arm retirement is entirely uncovered**
   (mutation → suite green). Left in place deliberately.

**From ADR-0168–0171:**

10. **An operator escape from a stalled compensation walk.** A walk whose
    `ActionCompleted` never arrives leaves the instance stuck, and **`CancelRequested`
    emits ZERO commands** (measured; sets `PendingCancel`, stays `compensating`). Own
    ADR. ⚠ ADR-0174 fixed what a dying instance OWES, not what happens when a walk
    never finishes — adjacent, not the same.
11. **A retry / incident story for a FAILED compensation action.** Ownership transfers
    on DISPATCH, so an action returning `ActionFailed` has had its record consumed and
    is never retried, with no incident raised. Measured `re-dispatched=[]` where `main`
    gave `[undoB undoA]`. Consistent with ADR-0034 §2.5's best-effort-skip contract.
12. **The event-sub-process hole's remaining direction** — `applyFinish` terminates a
    *resume* plan when the resume is dropped and no tokens remain; an arm firing there
    places a token and suppresses that recovery completion. `walkTerminates`
    structurally cannot see it.
13. **ADR-0171's two open bounds**, both pinned as falsifiable `KNOWN LIMITATION`
    assertions: the error-boundary teardown route still wedges the walk's finish on the
    destroyed resume scope, and `exitNestedEventSubprocessScope`'s sibling close is not
    held. ⚠ **ADR-0168's conjunct 3 is uncovered** — kept deliberately; *undemonstrated
    is not unreachable*.
14. **`AUDIT.md`** — 747-line adversarial architecture audit on `docs/architecture-audit`,
    ⚠ NOT on `main`, NOT pushed (public repo; unfixed Critical/High findings: self-asserted
    actor identity, fail-open `AuthzSpec`, IDOR/BOLA, SSRF, no crash-recovery path for
    post-commit projections). ⚠ Treat its claims as **unverified**.
15. **`processtest.Classify` has no reason for a compensation-walk park** — measured
    `reason="unknown"` → `ErrUnhandledPark`; ADR-0168 only **pins** it. Wants a
    `ReasonCompensation`.
16. **Repo-wide coverage ~74 %** — long pre-existing, not a regression; untested
    `examples/` and transport adapters are the drag. ⚠ `service` ~52.6 %.
    `scripts/coverage.sh` only REPORTS — its exit code proves nothing.
17. `engine/step_stale_commands.go` cites `runtime/processdriver_action.go:449-461` —
    accurate as of ADR-0173, but it will rot.

## Standing constraints

- **Docker: standing permission for the Verification coverage + no-regressions runs**
  (owner, 2026-08-11 — supersedes the older "ask every time"). Probe the daemon and run;
  if it is unavailable, say so and let the owner start it or skip the step, and label any
  container-free subset as the partial result it is. See CLAUDE.md Verification 1.
  ⚠ Everything else still asks, and **a subagent brief must say so explicitly** — one
  agent spun testcontainers unasked because its brief omitted the constraint.
- **`golangci-lint`: probe and run; if the binary is absent, offer to install it (agent
  or owner) or to skip** — never substitute `go vet` and never claim "lint clean" for a
  run that did not execute (CLAUDE.md Verification 3).
- **Container-free packages**: `engine`, `runtime/calllink`, `runtime/signal`,
  `runtime/task`, `service`, `processtest`, `transport/http`. ⚠ **`./runtime/...` as a
  whole is NOT.**
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones — the cheap
  way to prove a breaking type change has no hidden consumer.
- **Judge a test run by its exit code**, never a pipeline tail; use `-count=1`.
- **Run the suite on the MERGED tree**, and **re-run after any `/code-review` fix**.
- `/code-review` and `/security-review` are **owner-invoked only**. Adversarial Opus
  stand-ins first anyway — but they are **not** the gate.
- **Fan out subagents by Go package.** A delivery entirely inside `engine` runs
  **strictly serial**.
- **An agent that must measure against a patched tree gets a `git worktree`**, and the
  brief must say so — *and* must verify the worktree contains the bundle.
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **ADR-0174's implementation record and mutation table** —
  `docs/plans/2026-08-11-dying-instance-harvests-open-scopes.md` `▶ Progress`.
- **Its audit record** — `docs/specs/2026-08-11-adr-0174-audit-evidence.md`.
- **ADR-0173's audit record** — `docs/specs/2026-08-11-adr-0173-audit-evidence.md`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
