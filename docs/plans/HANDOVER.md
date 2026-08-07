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

## State — updated 2026-08-07

**▶ Pick up here: ADR-0166 is SHIPPED. The next delivery is delivery 3 —
ADR-0158, "a broadcast signal fires every matching arm per family" — whose bundle
is parked, ~6 deliveries stale, and has NEVER had its rule-#9 audit. Do not
implement it from the parked draft; re-derive its premises first (see below).**

ADR-0166 (`processtest` sees every signal/message waiter source) shipped
2026-08-07, closing pre-v0.1.0 blocker 6. ADR-0165 shipped 2026-08-06.

| | |
|---|---|
| `main` | ADR-0166 is the newest bundle. ⚠ **Do not trust a `main` SHA written here** — this file is committed onto `main`, so any SHA it quotes for `main` is stale the moment it lands. Re-derive: `git rev-parse --short main` |
| `feat/processtest-waiter-enumeration` | merged; safe to delete |
| `feat/terminal-trigger-guard` | merged (ADR-0165); safe to delete |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165's ten pre-squash commits, provenance only. Delete when you no longer want the phase-by-phase history |
| **`parked/scope-and-fanout-design`** | **delivery 3's draft ADR-0158 lives here** (`docs/adr/0158-signal-fires-every-matching-arm.md`, plan `docs/plans/2026-07-31-signal-arm-fanout.md`). ⚠ It also carries a **superseded ADR-0162 draft — do not read it.** ⚠ Its diff vs `main` is now **~18,000 deleted lines** of tests that `main` has since gained: the branch predates 2a, 2b, 0165 and 0166 entirely |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept only for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only. Owner DECIDED not to push it; do not re-raise |
| Latest ADR | **0166**. 0158 lands with delivery 3; 0155–0157 are reserved by the older parked branch. Next free is **0167** |
| v0.1.0 | not tagged |

## ▶ The immediate next work: delivery 3 (ADR-0158)

**A broadcast signal must fire every matching arm per family, not just the first.**
The draft is on `parked/scope-and-fanout-design`. Its prerequisites are now both
shipped: ADR-0165 (terminal-trigger guard) and ADR-0166 (the harness can finally
drive an arm-only park, so delivery 3's headline scenario is testable at all).

**Before you act on that bundle, re-derive it.** It has never survived a rule-#9
audit, and every engine file it touches has moved under it:

- `engine/step_triggers.go`, `engine/trigger.go`, `engine/trigger_validate.go`
  were reshaped by **ADR-0165**, which deleted eight hand-copied terminal guards
  and moved enforcement into `dispatch` via `terminalPolicy()` on the sealed
  `Trigger` interface.
- `engine/state.go` and the scope lifecycle moved under **ADR-0162/0163** and
  **ADR-0164**.
- The parked branch's own test files are ~18k lines behind `main`.

So: re-read the draft, **execute** every claim it makes about current behaviour
(Premise Discipline — it predates that section of CLAUDE.md entirely), rewrite the
spec/ADR/plan against today's code, then run the rule-#9 audit on the rebuilt
bundle before any implementation.

⚠ **ADR-0154 left "first match per family" OPEN deliberately** — that is exactly
the gap 0158 closes. See `docs/adr/0154-*` and the signal-waiters topic memory.

### Then, in priority order

1. The **two ADRs still owed by delivery 2b** — incident-history retention (owner
   chose REVISIT; likely carry the incident error into the terminal-event payload)
   and **zombie scopes** (ADR-0162 ships a stale sentence claiming `endInstance`
   closes them; it never touches `s.Scopes`).
2. A pre-v0.1.0 blocker from the list below.

## How ADR-0166 shipped, and the one lesson worth carrying

`Classify` now delegates to `engine.InstanceState.SignalWaiters()`/
`MessageWaiters()` instead of re-deriving source 1 from token fields, so boundary,
event-gateway and event-subprocess arms are finally visible to `PublishSignal`/
`DeliverMessage`. `Park.AwaitingMessages` became `[]engine.MessageWaiter`
(breaking), `Park.Node` falls back for arm-derived parks, and the `ReasonTimer`
promotion widened so `AutoTimers()` keeps working beside a live arm.

Gate 4/4: `processtest` **90.2 %** (baseline 88.0), full suite **EXIT=0 / 64
packages / 0 races / repo 73.8 %** run **twice** (the first run certified a tree
`/code-review`'s fixes then changed), **`/code-review` 4 findings → 4 fixed**,
**`/security-review` 0 vulnerabilities**. 20 tests; 15 mutations, 15 caught.

### ⚠⚠ The delivery bound was refuted FOUR times, always by execution

This is the transferable lesson. Four successive rounds each produced a bound that
looked obviously correct, and each was falsified by *running* it, never by review:

1. **rule-#9 audit** killed "deliver each name at most once" — two sequential token
   catches of one name is ordinary BPMN.
2. **implementation** killed the token-id fingerprint — a token **keeps its id** as
   it advances; only its node changes.
3. **adversarial stand-in review** killed the fingerprint idea outright — a loop
   re-enters the *same* node; the arm-slice counts are instance-wide, so the arm's
   own branch arming anything re-authorises delivery forever; and one last-key slot
   *displaces* across instances (an arm fired 4–28 times under concurrency).
4. **`/code-review`** killed the waiter COUNT that replaced it — two sequential
   *arms* of one name each report a single waiter, so the second was silently
   suppressed. **That is the audit's own finding (1) reproduced one level up, on
   the arm side.**

What ships: a token catch is **never** bounded (it is consumed when it fires and
cannot re-match); an **arm** is bounded per instance per **parked node**, because
an arm's real identity is unreachable — the arm slices have unexported element
types — and parked nodes are the closest observable proxy.

**If you touch this, execute the shapes in spec §2.6–2.7 before believing any
argument about it.** And the meta-lesson: *when a review kills a bound for
construct A, immediately check the same shape for construct B.*

### Two smaller lessons from the same delivery

- **A mutation that fails to COMPILE is not a RED**, and **a mutation that cannot
  DISCRIMINATE is not verification.** Three of this delivery's mutation attempts
  were invalid on the first try; each would otherwise have been recorded as proof.
  One test asserted `ErrUnhandledPark` + `"human-task"`, which the mutated build
  also produced — only asserting *the clock did not move* separated them.
- **One of this delivery's own added tests could not fail**, and it was written
  during a *coverage* round — the situation where a vacuous test is easiest to
  write. Assert the returned value, not a downstream error both branches reach.

### Verification facts worth keeping

- **Container-free packages**: `engine`, `runtime/calllink`, `runtime/signal`,
  `runtime/task`, `service`, `processtest`, `transport/http`. ⚠ **`./runtime/...`
  as a whole is NOT** — `main_test.go`, `rehydrate_durable_test.go`,
  `jobstore_rehydrate_durable_test.go` and `timer_txflow_test.go` import
  `internal/dbtest`.
- ⚠ **`go vet ./...` compiles every test file**, including the Docker-only ones.
  For a breaking type change it is the cheap way to prove no consumer is hiding
  behind the container gate — it caught nothing here only because nothing was wrong.
- **Baselines to hold**: `processtest` **90.2 %**, `engine` **91.9 %**, repo
  **73.8 %**, lint 0.
- ⚠ **`service` sits at 52.6 %**, below the 85 % floor — long pre-existing, not a
  regression. Backlog item 4; worth a decision rather than silence.
- **Cross-package godoc links resolve against the PACKAGE's imports, not the
  file's** — verified by running `go/doc` over `./engine`.

## Known gaps still owed by delivery 2b (ADR-0164)

ADR-0165 discharged the first of three. Two remain, plus a small third it added:

- **Incident-history retention.** `forceTerminate` and cancel's immediate branch
  still erase every incident, so the audit view renders empty, `incident_count`
  drops to 0, and `terminalEventErr` degrades to `"cancelled"`. Owner-decided to
  REVISIT in its own ADR — most likely carrying the incident error into the
  **terminal event payload**.
- **Zombie scopes.** Four terminal transitions set a terminal `Status` without
  pruning `s.Scopes`. ADR-0162 ships a **stale sentence** claiming `endInstance`
  closes them; it never touches `s.Scopes`. ⚠ ADR-0165 sharpened this: a terminal
  instance with a still-**open** scope holding compensation records now has those
  records unreachable by any walk. Correct behaviour, but it is this gap's shadow.
- **Third, small:** `stepCompensateRequested`'s surviving-records check stays a
  hand-written in-handler guard because it reads state, not the trigger. Any
  future state-dependent terminal policy faces the same choice.

## Pre-v0.1.0 blockers

1. **Strict definition decoding** (`DisallowUnknownFields` / `KnownFields(true)`).
   Lenient decode plus a fail-open `AuthzSpec` means future `eligible_*` tag drift
   silently degrades to allow-all.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go` arms a
   zero `nextRun` when `TriggerSpec.Next` reports `ok == false`;
   `DATETIME(6) NOT NULL` rejects `'0000-00-00'` under strict mode. Postgres and
   SQLite store it fine. Needs a reject-vs-normalise ADR.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.
4. **ADR-0159 names two symbols that do not exist** (`EncodeArmedCursor` /
   `DecodeArmedCursor`; shipped names are `EncodeArmedTimerCursor` /
   `DecodeArmedTimerCursor`). Merged and pushed, so it takes its own `docs:` commit.
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky.**
   `require.Eventually(..., 5s, 25ms)` at
   `internal/persistence/store/notifier_pgx_test.go:98` waits on a NOTIFY-driven
   relay drain while a dozen containers boot. Interacts with item 7; **do not
   silence it**.
6. ✅ **`processtest` cannot drive an arm-only park — CLOSED by ADR-0166.**
   Kept as a pointer because blocker 9 below is its unclosed twin.
7. **Suite speed.** `internal/dbtest`'s `sync.Once` container boot fires per
   package → 12 Postgres + 7 MySQL boots (~60s of a ~2min suite). Fix: honour
   `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   fallback, plus `scripts/testdb.sh up|down` and CI service wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
   No test combines `WithForceTermination` with a boundary-arming host, and
   `s.Boundaries = nil` has no semantic cover anywhere. Cheapest real fixture:
   `engine/step_terminal_test.go:669`, which already arms an error boundary on a
   sibling branch that survives the transition.
9. **`Park.HasArmedTimers` has blocker 6's defect, for TIMER arms — still OPEN.**
   ⚠ Found by ADR-0166's audit and deliberately left out of its scope.
   `HasArmedTimers: len(state.Timers) > 0` reads one source, so a boundary or
   event-gateway timer arm is invisible. Measured: a user task with a boundary
   timer gives `len(Timers)=0, len(Boundaries)=1, HasArmedTimers=false`.
   Consequence: after ADR-0166 ships, a definition parked purely on a **timer**
   arm is still undriveable through the harness, and `AutoTimers()` cannot see
   it. Closing it needs an engine-side `TimerWaiters()`/`ArmedTimerNodes()`
   authority mirroring `SignalWaiters` — its own ADR. **Until then, no document
   may claim the ADR-0154 class is closed in `processtest` outright.**

## Standing constraints

- ⚠ **Ask before using Docker** (owner, 2026-07-31 — other sessions saturate the
  daemon). Per-run approvals do **not** carry over. See the container-free list above.
- **Run the suite on the MERGED tree**, and **re-run after any `/code-review` fix**.
- `/code-review` and `/security-review` are **owner-invoked only**. **Run
  adversarial Opus stand-ins first anyway** — on ADR-0166 two stand-ins found five
  substantive issues (three executed regressions vs `main`) and replaced a whole
  decision before the gate ever ran. They still miss what the real gate catches:
  `/code-review` then found four more, including the headline defect. Brief
  stand-ins to **execute against a `main` baseline**, in a `git worktree`.
- Push on merge (standing preference).
- Fan out subagents **by Go package** — concurrent agents inside one package break
  each other's `go test` compile even on disjoint files.
- **An agent that must measure against a patched tree gets a `git worktree`**, and
  the brief must say so. "Clean up afterwards" is not isolation: an ADR-0166
  auditor patched the live tree while another was measuring against it.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **ADR-0166's full record** — every refuted form of the delivery bound, the
  mutation table, and what each review round found — the `▶ Progress` block of
  `docs/plans/2026-08-07-processtest-waiter-enumeration.md`, plus spec §2.5–2.7.
- **ADR-0165's task-by-task record** — every mutation, adjudication and false
  claim — `.superpowers/sdd/2026-08-05-structural-terminal-trigger-guard/progress.md`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template. ADR-0165 carries **six**
  correction blocks added during implementation and the gate; read them, they
  supersede the original text.
- **Designs** — `docs/specs/`. ADR-0165's spec §9 carries its audit record;
  ADR-0166's spec §2.3 carries what its audit refuted and §7 the audit summary.
- **Conventions and gates** — `CLAUDE.md`, including the new **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
