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

## State — updated 2026-08-10

**▶ ADR-0168/0169/0170/0171 are SHIPPED — merged to `main` and pushed
(merge `b12bba3`, bundle `adf3977`). Nothing is in flight.**

**▶ Next work: delivery 3 (ADR-0158)** — see the section below. Re-derive it from
scratch; it has never survived a rule-#9 audit and predates Premise Discipline.

⚠ **Do not trust any `main` SHA written in this file** — re-derive:
`git rev-parse --short main`.

### What the shipped bundle did

**Four** ADRs — 0168/0169/0170 were the designed bundle; **0171 was added at the
delivery gate**, because 0168 must not ship without it (below). All defects
reproduced by execution; none previously covered by any test in either direction.

1. **ADR-0168** — a rolled-back process was reported `completed`.
   `startCompensationWalk` consumes its own throw token, so `len(Tokens)==0` read
   true while a walk was in flight; a sibling branch then completed the instance
   and the compensation action's `ActionCompleted` was refused `outcome=dropped`.
   The three normal-completion guards now also require
   `Compensating.ActiveCmdID == ""`.
2. **ADR-0169** — a signal delivery kept dispatching after its own drive turned
   the instance terminal: zombie token, task record minted after `FailInstance`,
   and a live `ScheduleTimer` escaping to the runtime. Tiers 1–3 became a slice of
   lookup-and-fire closures with an `IsTerminal()` re-check per iteration **and
   inside the tier-4 loop**.
3. **ADR-0170** — `handleUnhandledError` restarted a live compensation walk: the
   compensation action was dispatched **twice** and the uncaught error was
   **silently swallowed so the process reported success**. It now converts the
   in-flight walk instead of starting a second one — **deferring** the error onto
   the walk via `PendingCancel` + a pending outcome, so `applyFinish` walks any
   records the in-flight walk did not cover.
4. **ADR-0171** — a compensation walk read its records **live** from the scope. A
   sibling draining that scope mid-walk left the cursor naming a dead scope, and
   the walk's next `ActionCompleted` **panicked in the pure engine core**
   (`index out of range [0]`) — or, with one record, wedged permanently. The walk
   now pins its record source at start and the scope exit is held while a walk
   names it as its resume target.

### Gate record (all steps passed)

| step | state |
|---|---|
| `go test -race -coverprofile ./...` + `scripts/coverage.sh` | ✅ EXIT=0, 64 pkgs, **0 races**, repo **74.0 %** (floor 73.9 %) |
| `engine` coverage | ✅ **92.4 %** (baseline was 91.9 %) · `processtest` **90.2 %** |
| `golangci-lint run ./...` · `go vet ./...` · `gofmt` | ✅ clean |
| Documents describe what shipped | ✅ **four** refuted claims amended in-bundle (below) |
| Mutation duty | ✅ **18 + 9 + 13 mutations** across the three waves, each RED observed, each restore `diff`-verified. Non-catching mutations recorded as such, not hidden |
| Adversarial Opus stand-ins | ✅ 2 run (code-correctness, docs-vs-code). **Both found real defects; the code lens found a Critical one** |
| **`/code-review`** | ✅ **RUN — 5 findings (3 Med, 2 Low), ALL resolved.** ⚠ Its FIRST invocation died on a session limit after one tool call and returned zero findings — an absent review, not a clean one. Re-run |
| **`/security-review`** | ✅ **RUN — ZERO reportable vulnerabilities.** It did surface a real double-compensation integrity defect, proved **pre-existing on `main`** by execution → backlog item 4 |
| Suite on the **merged** tree | ✅ re-run before pushing: EXIT=0, 64 pkgs, 0 races, repo 74.0 %, `engine` 92.4 %, lint + vet clean |
| Merged `--no-ff` and pushed | ✅ merge `b12bba3` |

### ⚠⚠ FOUR design claims that execution refuted

All amended in-bundle (rule #11) with the measurement, rather than left in a
transcript. **All four had survived the rule-#9 audit's three Opus auditors.**
Two died during implementation; **two more died at the delivery gate** — the gate
is not a formality on this codebase.

0. **ADR-0170's decided shape was wrong, and ADR-0168 alone turns a silent wrong
   answer into a PANIC.** See the two blocks at the end of this section — these are
   the two the gate caught, and they are the important ones.

1. **ADR-0168's two event-sub-process conjuncts were documented as "provably
   non-discriminating today", their sites as reachability "not demonstrated".
   Both are reachable; all three conjuncts discriminate.** The supporting
   measurement — *"patch `exitRootScope` alone → suite `EXIT=0`"* — was correct,
   and the inference from it was not: `EXIT=0` is evidence about the **suite**,
   never about the engine. Reproductions built, then re-verified independently by
   reverting only those two conjuncts → both tests RED.
   ⚠ These two sites are **strictly worse** than the original defect: the
   compensation `InvokeAction` is dropped *inside the same step*, so the rollback
   never reaches the runtime at all.
2. **ADR-0169's tiers-1–3 guard closes no observable defect today.** Deleting it
   leaves the whole `engine` package `EXIT=0`; deleting the **tier-4 in-loop**
   guard gives `EXIT=1`. `endInstance` → `cancelAllScheduledWork` drains every arm
   family on the way to terminal, so tiers 2–3 find nothing. It ships as
   deliberate **defence in depth**; Decision 2's structural argument is unaffected.
   Consequently **T9 cannot fail** and ships as a labelled pin.

3. **ADR-0170 (as designed) inherited the in-flight walk's NARROW record source.**
   With a targeted throw the root records were never compensated **and were
   erased**; with a nested throw the guard was bypassed entirely and the walk was
   abandoned mid-flight. Reworked to **defer** the error onto the live walk
   (`PendingCancel` + pending outcome), reusing the protocol the engine already
   ships for cancel-mid-walk. ⚠ **Why the design missed it: ADR-0170 was derived
   from a single fixture** — the root scope-wide throw, whose record source happens
   to be all of `RootCompensations`. All four of the audit's mutations reused it.
   *A fix derived from one fixture inherits that fixture's shape as an unstated
   precondition.*
4. **ADR-0168 without ADR-0171 panics.** A sibling draining the throwing scope
   mid-walk destroys the record source the cursor names. Pre-0168 the instance
   silently went `completed`; with 0168's conjuncts it survives to the next
   `ActionCompleted` and **panics inside the pure engine core, in the consumer's
   process**. Surfaced by the adversarial architecture audit that appeared mid-session
(now `AUDIT.md` on branch `docs/architecture-audit`, see backlog item 5)
   (finding D, marked there as *unverified* — it was executed, and it reproduces).
   ⚠ **This also retired claim 1's own "accepted cost".** The
   `EventTriggeredSubprocesses` **2 → 0** arm loss recorded as a measured accepted
   cost was **not a cost** — the two fixtures stopped exactly **one `Step` short**
   of a permanent wedge. With 0171 the arms stay 2 and the instance completes.

### ⚠ The transferable lessons

- **An audit that executes the headline claims can still ship a false supporting
  claim.** A sentence of the form *"X is provably non-discriminating / unreachable"*
  is a behavioural claim and needs its own execution.
- **A test prescribed by an audit inherits no credibility from the audit's other
  findings.** T9 came from audit-evidence §7.2 — the one recommendation in that
  section never built and run. It is the same defect class the audit itself caught
  in T4 (a prescribed test that could not pass), one level down.
- **Three tests in this delivery could not fail as first written** (T4 and T9 from
  the design; one T2 draft during implementation, which *passed unpatched* because
  it counted an event across two steps where the total was invariant). Counting
  assertions are the recurring shape — **pin which step emits it**.
- **A mutation can "verify" something it never tested.** T4′ is protected by
  *either* ADR-0169 guard independently, so it does **not** discriminate guard
  *placement*; only T6 does. A table running just the T4′ mutation would have
  claimed a placement it never exercised.
- **An "accepted cost" is a claim about behaviour and earns no exemption from
  execution — and the fixture must be driven to TERMINATION.** A fixture that stops
  at the first surprising observation will certify that surprise as the design's
  price. This one hid a permanent wedge one `Step` away.
- **A fix derived from one fixture inherits that fixture's shape as an unstated
  precondition.** ADR-0170 was correct for the throw shape it was built from and
  wrong for every other.
- **`undemonstrated` is not `unreachable`.** This delivery amended that error once
  (claim 1) and then had to record the same shape again as an open bound
  (ADR-0168's conjunct 3, now uncovered after 0171's hold).
- **The adversarial gate reviews earn their cost.** Two stand-ins found four real
  defects between them, including a Critical one, in a bundle that had already
  passed a three-auditor design audit and full implementation. **Then the real
  `/code-review` found five more that both stand-ins had missed** — the fourth
  delivery running where that held. Stand-ins cut rework; they are not the gate.
- **A review that errored out is an ABSENT review, not a clean one.** `/code-review`'s
  first invocation died on a session limit after one tool call and returned zero
  findings. Re-run it; never read an empty result from a crashed run as a pass.
- **A finding from the real gate is a lead, not a verdict.** Two of this gate's five
  were corrected by execution — one severity overstated, one proposed fix measured
  *unsafe* (it would have resurrected an already-fired interrupting arm). Apply the
  same discipline to the reviewer that the reviewer applies to the code.

### Where things live

| | |
|---|---|
| `main` | **ADR-0171 is the newest shipped bundle** (merge `b12bba3`). ⚠ Re-derive: `git rev-parse --short main` |
| *(merged branches)* | **All merged delivery branches were DELETED 2026-08-10** — 0159/0161/0162/0163/0164/0165/0166/0167/0168–0171. Their history is in `main`; nothing is lost. **`origin` now carries only `main`** plus dependabot branches |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash history, provenance only |
| **`parked/scope-and-fanout-design`** | delivery 3's draft ADR-0158. ⚠ Also carries a **superseded ADR-0162 draft — do not read it.** ⚠ Its diff vs `main` is ~18,000 deleted lines of tests `main` has since gained |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only. Owner DECIDED not to push it; do not re-raise |
| Latest ADR | **0171**. 0155–0157 reserved by the older parked branch. Next free is **0172** |
| v0.1.0 | not tagged |

## ▶ NEXT WORK: delivery 3 (ADR-0158)

**A broadcast signal must fire every matching arm per family, not just the first.**
Draft on `parked/scope-and-fanout-design`. Prerequisites ADR-0165 and ADR-0166 are
shipped; **ADR-0168/0169/0170/0171 are now also inputs** — 0158 multiplies the dispatch
points inside one delivery, which multiplies 0169's exposure.

**Re-derive it before acting.** It has never survived a rule-#9 audit, it predates
the Premise Discipline section entirely, and every engine file it touches has moved
under ADR-0165 and 0162/0163/0164 — and now under this bundle.

⚠ **0158 must NOT re-derive the predicate from `!= StatusRunning`** — refuted by
execution and marked as such in both
`docs/specs/2026-08-08-adr-0158-premise-evidence.md` (three correction blocks) and
this bundle's spec §6.

⚠ **ADR-0169's no-hoist requirement is aimed squarely at 0158.** The three tier
lookups survive today only because all three fire functions happen to re-validate
before acting — an accident of three independent implementations, not an invariant,
and exactly what the fan-out will disturb. Hoisting them leaves the suite `EXIT=0`.

⚠ **ADR-0154 left "first match per family" OPEN deliberately** — that is 0158's gap.

### Then, in priority order

1. The **two ADRs still owed by delivery 2b** — incident-history retention (owner
   chose REVISIT) and **zombie scopes** (ADR-0162 ships a stale sentence claiming
   `endInstance` closes them; it never touches `s.Scopes`).
2. A pre-v0.1.0 blocker from the list below.

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

## Backlog opened by this bundle

Each was **executed**; each is deliberately out of the bundle's scope.

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
4. **Double-execution of compensation actions when a scope is torn down mid-walk —
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
   after redacting the security seam. (Was `FABLE_AUDIT.md`, untracked at the root.) Its finding D was verified and fixed here
   (ADR-0171). Its other Critical/High findings are **untriaged** and mostly
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
