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

## State — updated 2026-08-11

**▶ ADR-0173 IS IN FLIGHT: an AUDITED, IMPLEMENTATION-READY design bundle, zero code.**
Branch `feat/compensation-double-run-on-scope-teardown`, commit **`2e276bc`**
(docs only — the engine tree is unmodified). `main` has NOT moved: still
`1618b29`. ⚠ Re-derive both: `git rev-parse --short main` / `git log --oneline -1`.

**▶ What to do next: implement it**, following
`docs/plans/2026-08-11-compensation-walk-record-ownership.md` phase by phase. Its
`▶ Progress` block carries everything a fresh session needs, including which
scratchpad fixtures to reuse and why the validated spike must NOT be pasted in.

**▶ Latest shipped is unchanged: ADR-0158 + ADR-0172**, merge `fb60df0`. Latest ADR
number is now **0173** (in flight); next free is **0174**. 0155–0157 remain
reserved by the still-parked `feat/durable-waiters-delivery-correctness`.

### What ADR-0173 is

Handover backlog item 4 — *double-compensation when a scope is torn down mid-walk*,
found by `/security-review` during the 0168–0171 delivery and PRE-EXISTING on `main`.
One invariant: **a compensation walk's finish consumes exactly the records it
drained — no more, no less.** It was violated in both directions:

- **scope-wide + a teardown that cannot be deferred** → the finish clears LESS than
  it drained. `clearRecordsPrefix` no-ops on the destroyed scope while the teardown
  has copied the same records into `ArchivedCompensations` → a later walk re-runs
  them. Compensation actions are nowhere required to be idempotent.
- **targeted throw + a concurrent re-entry** → the finish deletes MORE (the whole
  archive key), silently discarding a genuinely uncompensated record. Found while
  reproducing the first; it is ADR-0120 review A1's rule missing from the targeted
  branch.

⚠ **The backlog entry understated the defect twice.** With two records EVERY drained
record re-runs, not one; and `CancelRequested` is not the only route — an admin
`CompensateRequested` on a **terminal** instance and an **unhandled error**
(`step_errors.go`, the only automatic one) both reach it.

### ⚠ Three things about this bundle worth carrying forward

1. **The rule-#9 audit changed the design four times, and one finding was Critical.**
   Three Opus lenses, isolated worktrees, all briefed to EXECUTE. All three converged
   independently on the Critical: the design as first written regressed a
   **pre-ADR-0171 cursor** (no pinned snapshot) into permanent LOST compensation —
   verbatim the trade the ADR rejects its simpler alternative for. Fix: a
   `len(cur.Records) > 0` conjunct.
2. **I over-generalised from one fixture, again.** My abandoned-walk probe had 2
   records and abandoned immediately. Lens 2 varied the BODY SHAPE — 3 records, one
   advance into the archived head — and showed the design halved the double-run
   instead of closing it, while that route was the entire justification for choosing
   it over the simpler option. The fix is incremental consumption at each dispatch.
3. **The audit found a whole teardown ROUTE the documents called impossible**:
   `cancelScopeSubtree`'s DESCENDANT loop reaches a nested walk's own scope when an
   ancestor is torn down. Measured on `main`: `[undoB undoA undoOuter2 undoOuter]` —
   two double-runs. The design already handled it; the false belief left it untested.
   ⚠ Enumeration rot again: "four other call sites" was five, "the only drain" was two.

Full detail in the spec's §8 (adjudication) and the ADR's own corrections block.

## ▶ NEXT WORK

**1. Implement ADR-0173** — the bundle is audited and ready. Follow
`docs/plans/2026-08-11-compensation-walk-record-ownership.md`. Strictly serial: it is
entirely in package `engine`, and concurrent agents in one package break each other's
`go test` compile. Ten tests (T1–T5, T8–T12) and ten mutations are specified, each
with what makes it fail. ⚠ Two prescribed tests from the FIRST draft were dropped as
vacuous and one probe cannot fail as written — read the plan's warnings before
promoting any scratchpad probe into a test.

Everything below is untouched, and stays a genuine choice once 0173 lands:

2. **The two ADRs still owed by delivery 2b** — incident-history retention (owner
   chose REVISIT) and **zombie scopes** (ADR-0162 ships a stale sentence claiming
   `endInstance` closes them; it never touches `s.Scopes`). ⚠ Two deliveries have
   now re-measured a zombie scope surviving on a terminated instance.
3. **An operator escape from a stalled compensation walk** — a walk whose
   `ActionCompleted` never arrives leaves the instance permanently stuck, and
   `CancelRequested` emits ZERO commands. Own ADR. ⚠ Related to 0173 but distinct:
   0173 fixes what a walk OWNS, not what happens when it never finishes.
4. **The event-sub-process hole's remaining direction** — `applyFinish`'s second
   counterexample: it terminates a *resume* plan when the resume is dropped and no
   tokens remain, and an arm firing there places a token and suppresses that recovery
   completion. `walkTerminates` structurally cannot see it.
5. A pre-v0.1.0 blocker from the list below.

### Where things live

| | |
|---|---|
| `main` | **ADR-0158 + ADR-0172 is the newest SHIPPED bundle** (merge `fb60df0`), unchanged by the in-flight work. ⚠ Re-derive: `git rev-parse --short main` |
| *(merged branches)* | Merged delivery branches are deleted once pushed; their history is in `main`. **`origin` carries only `main`** plus dependabot branches |
| **`feat/compensation-double-run-on-scope-teardown`** | **`2e276bc` — ADR-0173, IN FLIGHT. Audited design bundle, docs only, engine tree unmodified** |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash history, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input.** Its ADR-0158 draft was refuted on five inherited claims; it also carries a superseded ADR-0162 draft |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only. Owner DECIDED not to push it; do not re-raise. Holds ADR numbers **0155–0157** |
| Latest ADR | **0173** (in flight). Next free is **0174** |
| v0.1.0 | not tagged |

## Pre-v0.1.0 blockers

1. ✅ **Strict definition decoding — CLOSED by ADR-0167.** ⚠ Does **not** close the
   fail-open `AuthzSpec`: an empty spec, `eligible_roles: []`, a bare
   `eligible_roles:` and `eligible_roles: null` all parse cleanly and mean
   allow-all. Own ADR.
   🚨 **Before DEPLOYING ADR-0167**: audit stored definition rows for 5 pre-ADR-0144
   camelCase keys (`compensateAction`, `compensationAction`, `completionAction`,
   `correlationKey`, `messageName`) — rows carrying one stop loading.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go` arms a zero
   `nextRun` when `TriggerSpec.Next` reports `ok == false`; `DATETIME(6) NOT NULL`
   rejects it (Error 1292). Postgres and SQLite are fine. Needs a
   reject-vs-normalise ADR.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.
4. ✅ **ADR-0159's misnamed symbols — CLOSED.** It was **three** symbols, not two.
   *Lesson: the blocker's own enumeration had rotted.*
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
   `len(state.Timers) > 0` reads one source, so a boundary or event-gateway timer
   arm is invisible. Needs an engine-side `TimerWaiters()`. **Until then, no document
   may claim the ADR-0154 class is closed in `processtest` outright.**

## Backlog (newest delivery's items first)

⚠ Items 1–8 were opened by the ADR-0168–0171 delivery; items 9–13 by the newest
(ADR-0158/0172). Each was **executed**, not speculated.

**From ADR-0158/0172 (newest):**

9. **A flow targeting a NON-EXISTENT node parks a permanent wedge.** Measured:
   `WARN token routed to a missing node`, then a `TokenWaiting` token with
   `AwaitCommand == ""` that nothing can ever resume, instance `running` forever.
   ⚠ The parked plan had asserted this *errors*; it does not.
10. **Micro mode loses a signal delivery.** `snapshotIDs` is taken over tokens
    Micro has not driven to their park yet, so an intermediate signal catch sitting
    `TokenActive` with `AwaitSignal == ""` is silently missed while the signal is
    still consumed and the catch is **not** re-armed. Pre-existing.
11. **`PendingCancel=true` survives onto a `Running` instance forever** — after a
    `walkPartial` walk resumes, the operator's cancel is silently lost **and will
    terminate the NEXT throw or reverse walk** instead.
12. **`runtime/processdriver_action.go:485`'s comment is FALSE** — it asserts
    `performThrowSignal` excludes the throwing instance; measured, it does not.
    ADR-0158 relies on the true behaviour. Doc-only fix.
13. **`engine/step_nodes.go:501`'s nested arm retirement is entirely uncovered**
    (mutation → repo suite green). Left in place deliberately; **not** removed on
    the strength of a green suite.

**From ADR-0168–0171:**

1. **An operator escape from a stalled compensation walk.** ADR-0168's deferral means
   a walk whose `ActionCompleted` never arrives leaves the instance permanently stuck,
   and **`CancelRequested` cannot release it** (measured: zero commands, sets
   `PendingCancel`, stays `compensating`). `compensationInvoke` emits a bare
   `InvokeAction` with no timer, and `handleActionFailed` short-circuits compensation
   commands before the retry block. Likely shape: let `CancelRequested` terminate
   immediately when the walk has no live tokens. Behaviour change on an ADR-0039/0109
   path — own ADR.
2. **The event-sub-process hole, which has TWO directions** and whose ADR must cover
   both: (a) a **non-root** ESP arm fires into a completed instance and emits a live
   `InvokeAction` — `fireEventTriggeredSubprocessArm` checks status for the root scope
   only, and `beginCompensation` does not drain ESP arms; (b) the root check **is** the
   refuted `!= StatusRunning`, measured silencing a legitimate signal during a local
   compensation throw. **ADR-0168 lengthens the window in which (b) bites.** It also
   falsifies **ADR-0124 Decision item 4's** "harmless lingering arm" sentence.
   ⚠ **New from this bundle:** ADR-0168's guard means a live cursor now falls past the
   two ESP completion sites into a tail that **retires that scope's ESP arms** while
   the instance stays `Compensating` — measured `EventTriggeredSubprocesses` 2 → 0,
   silent permanent loss. Accepted cost, pinned by test; this ADR should revisit it.
   ⚠ That ADR will likely narrow `cancelAllScheduledWork`'s drain — which is exactly
   what makes ADR-0169's tiers-1–3 guard (and T9) start mattering.
3. **ADR-0171's two open bounds, both measured and pinned as falsifiable
   `KNOWN LIMITATION` assertions.** (a) The **error-boundary teardown** route: the
   record is now compensated, but the walk's finish still wedges on the destroyed
   resume scope. (b) `exitNestedEventSubprocessScope`'s sibling close is not held.
   ⚠ Also: **ADR-0168's conjunct 3 is now uncovered** — 0171's hold returns before
   those fixtures reach it, so reverting it leaves `EXIT=0`. Kept deliberately;
   undemonstrated is not unreachable.
4. ▶ **IN FLIGHT as ADR-0173 — see `## ▶ NEXT WORK` item 1.** ⚠ The entry below
   UNDERSTATES it twice, which the reproduction corrected: with two records EVERY
   drained record re-runs, not one, and there are THREE re-entry routes, not one
   (`CancelRequested`, an admin rollback on a terminal instance, and an unhandled
   error — the only automatic one). A THIRD teardown route also exists, via
   `cancelScopeSubtree`'s descendant loop. Kept verbatim below for provenance:

   **Double-execution of compensation actions when a scope is torn down mid-walk —
   found by `/security-review`, PRE-EXISTING on `main`, not introduced here.** A
   scope-wide throw walk drains a sub-process scope's records but clears the
   drained prefix only at *finish*. If an error boundary on the enclosing
   sub-process fires mid-walk, `archiveCompensations` moves the
   **already-compensated** records into `ArchivedCompensations` and `closeScope`
   removes the scope, so `clearRecordsPrefix` no-ops; any later walk
   (`CancelRequested`, unhandled error) calls `consolidateArchiveIntoRoot` and
   **re-dispatches the same compensation actions**. Measured on `main` **and**
   `HEAD` with 1 record: `undoB1` dispatched twice. ⚠ **This bundle changes the
   ≥2-record shape from a loud panic into a SILENT double-run** — the panic was
   ADR-0171's defect; the double-run survives it. Compensation actions are nowhere
   required to be idempotent, so a double-run is a real integrity impact (a refund
   applied twice). Likely fix: on a scope-wide-throw finish whose scope is gone,
   drop the drained records from `ArchivedCompensations[scope.NodeID]` too, or
   archive with a drained-prefix marker. Own ADR.
5. **`AUDIT.md`** — a 747-line adversarial architecture audit (2026-08-10), **not
   written by this delivery's agents**. Committed as `393e516` on the local branch
   **`docs/architecture-audit`**; ⚠ **deliberately NOT on `main` and NOT pushed**,
   because the repo is **public** and the file details unfixed Critical/High findings
   in enough detail to act on. Merge it to `main` only once those are closed, or
   after redacting the security seam. (Was `FABLE_AUDIT.md`, untracked at the root.)
   Its finding D was verified and fixed here (ADR-0171). Its other Critical/High findings are **untriaged** and mostly
   outside this delivery: post-commit projections have no crash-recovery path
   (waiters/tasks/actions, vs timers which got `RehydrateTimers`); HTTP task
   endpoints trust a self-asserted actor identity; memory-only message/signal
   waiters; the single-module packaging decision whose window closes at v0.1.0.
   ⚠ Treat its claims as **unverified** — the one that was checked was labelled
   `ASSUMPTION (unverified)` in the file itself and turned out real.
6. **`processtest.Classify` has no reason for a compensation-walk park** — measured
   `reason="unknown"` → `ErrUnhandledPark`. This bundle only **pins** it. A real fix
   wants a `ReasonCompensation` surfacing the awaited command id. Same class as
   blocker 9. ⚠ Severity is **lower than the spec originally claimed**: the default
   synchronous drive loop completes the walk inside one `ApplyTrigger` and never
   parks — measured. It bites a consumer classifying a **stored** mid-walk snapshot.
7. **Repo-wide coverage 74.0 %** — long pre-existing, not a regression; untested
   `examples/` and transport adapters are the drag. ⚠ `service` sits at ~52.6 %.
   `scripts/coverage.sh` only REPORTS — its exit code proves nothing.
8b. 🆕 **Two bounds ADR-0173 deliberately does NOT close** (both measured; spec §7).
   (a) **Mixed-version deploys are unsafe**: whole-`InstanceState` JSON round-trips
   without `DisallowUnknownFields` on that path, so new-reads-old is fine but
   **old-reads-new silently DROPS the three new cursor fields and re-serializes
   without them**, reinstating the double-run. Do not run pre-0173 and post-0173
   engine builds against one instance store. (b) **A pre-ADR-0171 cursor keeps
   `main`'s behaviour, double-run included** — it has no pinned snapshot, so nothing
   can dispatch its archived head; the alternative loses the record outright. Same
   old-rows population as the ADR-0167 deployment audit. A test pins it so nobody
   "fixes" it by widening the predicate.

8. `engine/step_stale_commands.go` cites `runtime/processdriver_action.go:449-461`.
   **Currently accurate**, verified this bundle — but it will rot like the citation
   this bundle replaced with symbols (that one had moved twice: 982 → 1035 → 1079).

## Standing constraints

- ⚠ **Ask before using Docker** (owner, 2026-07-31 — other sessions saturate the
  daemon). Per-run approvals do **not** carry over. ⚠ **A subagent brief must say so
  explicitly**: one agent this delivery ran `go test ./...` for its no-regressions
  check and spun testcontainers unasked, because the brief omitted the constraint.
- **Container-free packages**: `engine`, `runtime/calllink`, `runtime/signal`,
  `runtime/task`, `service`, `processtest`, `transport/http`. ⚠ **`./runtime/...` as a
  whole is NOT.**
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones — the cheap
  way to prove a breaking type change has no hidden consumer.
- **Run the suite on the MERGED tree**, and **re-run after any `/code-review` fix**.
- `/code-review` and `/security-review` are **owner-invoked only**. Run adversarial Opus
  stand-ins first anyway — but they are **not** the gate, and they miss what it catches.
- **Fan out subagents by Go package** — concurrent agents inside one package break each
  other's `go test` compile even on disjoint files. **This bundle was entirely in
  `engine`, so it ran strictly serial**, with docs-only work interleaved.
- **An agent that must measure against a patched tree gets a `git worktree`**, and the
  brief must say so.
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **This bundle's implementation record, mutation table (18 rows) and the two refuted
  design claims** —
  `docs/plans/2026-08-08-compensation-walk-and-mid-delivery-terminal.md` `▶ Progress`.
- **Its pre-implementation audit record** —
  `docs/specs/2026-08-08-adr-0168-0170-audit-evidence.md` (three lenses, verbatim
  measurements, four executed test sources).
- **The executed premise evidence behind bugs 1 and 2** —
  `docs/specs/2026-08-08-adr-0158-premise-evidence.md`. ⚠ **Its §Q4(c) fix shape is
  REFUTED** and now carries three correction blocks. It is evidence, not a decision.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template. ADR-0165 carries **six**
  correction blocks; ADR-0168/0169/0170 now carry their own.
- **Designs** — `docs/specs/`.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
