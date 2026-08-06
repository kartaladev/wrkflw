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

## State — updated 2026-08-06

**▶ ADR-0165 (structural terminal-trigger guard) is SHIPPED — merged `--no-ff`
and pushed 2026-08-06. Nothing is in flight. Pick the next delivery from
"What's next" below; delivery 3 (ADR-0158) is the intended one, but its bundle
is ~5 deliveries stale and needs re-verification before you act on it.**

| | |
|---|---|
| `main` | **`ec25ffd`** — ADR-0165 merged and PUSHED 2026-08-06, clean, in sync with `origin/main` |
| `feat/terminal-trigger-guard` | merged into `main`; safe to delete |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — the ten pre-squash commits, kept as provenance for the phase-by-phase history. Delete once you are confident you will not need it |
| `parked/scope-and-fanout-design` | ADR-0158 draft (delivery 3) + a superseded ADR-0162 draft. **Do not read its 0162.** ⚠ ~5 deliveries stale |
| `parked/terminal-transitions`, `feat/scope-lifecycle-correctness` | merged; delete or ignore |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept only for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only |
| Latest ADR on `main` | **0165**. 0158 lands with delivery 3; 0155–0157 reserved by the older parked branch. Next free is **0166** |
| v0.1.0 | not tagged |

### How ADR-0165 shipped

One squashed feature bundle (`d8854e5`) under merge `ec25ffd`. The squashed tree
was verified byte-identical to the ten-commit branch tree, and the merge tree
byte-identical to the squashed one (`818d47f4…` both sides), so the green suite
run certifies exactly the content on `main`.

Gate 3/3: full suite on the merged tree **EXIT=0**, 64 packages, zero failures,
zero data races under `-race`, repo **73.6 %**, `engine` **91.9 %** (baseline
91.8 % held), lint 0 — re-run green after the review fixes.
**`/code-review` 5 findings → 5 fixed.** **`/security-review` 0 vulnerabilities**,
assessed a net tightening.

⚠ **Two design claims died on execution in this delivery, not on review.**
Decision 5's predicate shipped INVERTED and reddened four already-reviewed tests.
Then `/code-review`'s Medium **reversed twice**: converging the four human-task
handlers on 404 followed the ADR's own stated rationale and was backwards, because
`service.deliverTaskTrigger` reads the task store on its FIRST line, so an unknown
id never reaches the engine — the branch fires only for a **ghost**, which is a
state conflict. The proof was `TestErrConflict_EngineWrongStateClassified`
failing, not an argument: it must *seed a synthetic task* to reach the branch at
all. ADR-0165 carries **seven** correction blocks; read them before trusting any
sentence in it.

## What's next

Nothing is in flight. Pick one:

1. **Delivery 3 — ADR-0158, a broadcast signal fires every matching arm per
   family.** The intended next delivery. Draft on `parked/scope-and-fanout-design`.
   ⚠ **Still needs its own rule-#9 audit in split form**, and its bundle is now
   ~5 deliveries stale: it predates 2a, 2b and 0165 entirely, still carries
   superseded 0161/0162 drafts, and every engine file it touches
   (`step_triggers.go`, `step_cancel.go`, `state.go`) has moved under it — 0165 in
   particular deleted eight guards and reshaped `dispatch`.
   **Re-verify every premise against source, by execution, before acting on it.**
   ADR-0165 was its prerequisite and is now shipped: delivery 3 multiplies traffic
   through `handleSignalReceived` and `handleMessageReceived`, both now guarded.
   ⚠ Its headline scenario is still **untestable by consumers** — see blocker 6.
2. **The two ADRs still owed by delivery 2b** — incident-history retention and
   zombie scopes (below).
3. **A pre-v0.1.0 blocker** from the list below.

### Verification facts worth keeping

- **Container-free packages**: `engine`, `runtime/calllink`, `runtime/signal`,
  `runtime/task`, `service`, `processtest`, `transport/http`. ⚠ **`./runtime/...`
  as a whole is NOT** — `main_test.go`, `rehydrate_durable_test.go`,
  `jobstore_rehydrate_durable_test.go` and `timer_txflow_test.go` import
  `internal/dbtest`.
- **Baselines to hold**: `engine` **91.9 %**, repo **73.6 %**, lint 0.
- ⚠ **`service` sits at 52.6 %**, below the 85 % floor — long pre-existing, not a
  regression from any recent delivery. Backlog item 4; worth a decision rather
  than silence.
- **Cross-package godoc links resolve against the PACKAGE's imports, not the
  file's** — verified by running `go/doc` over `./engine` (87 links resolved,
  zero unresolved), after nearly reverting them on the opposite assumption.

## What this delivery changed about how we work

**CLAUDE.md gained a `## Premise Discipline` section.** One finding drove it:
**ADR-0165's Decision 5 shipped with an inverted predicate.** It said to refuse a
plain full compensation rollback on a terminal instance when records *survive*.
Running it showed the opposite — with no records there is no walk at all, but the
status flips `Failed`→`Terminated`, a surviving token is discarded and `EndedAt`
is rewritten; with records surviving it is a genuine walk ADR-0164 protects.
Implementing it as written reddened four already-reviewed tests.

That sentence survived design authorship, a 42-finding adversarial audit, and
every later read. **Design review structurally cannot establish what code
currently does.** So: no claim about current behaviour may enter a spec, ADR or
plan until it has been executed and the output recorded; rule #9 auditors must
now *run* the bundle's load-bearing claims; rule #11 expects implementation to
correct the design and requires the ADR to be amended in the same bundle; the
Delivery Gate gained a "documents describe what shipped" step.

Related, and worth knowing before you write any comment: the same delivery
produced **six** over-claiming summary sentences, **two of them introduced by the
edits removing earlier ones**, and the worst inherited from a controller brief
where it had been correctly hedged. Watch every *all*, *none*, *only*, *every*.

**The rule then proved itself twice more at the gate.** `/code-review`'s two Low
findings both rested on a comment asserting that `rejectWithError` is never
logged — `dispatch` logs *both* refusal flavours, and one probe settled it. And
its Medium was resolved the wrong way first, by following a rationale the ADR
itself stated; the correction came from a test going RED, not from re-reading.
**When a fix's two halves guard different cases, mutate BOTH ways** — collapsing
`handleHumanCompleted`'s ghost/corruption disambiguation reddens *different*
tests in each direction, and that complementary pair is what proves neither half
is decorative.

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
6. **`processtest` cannot drive a boundary-arm-only park.** `Classify` derives
   `AwaitingSignals` from `Token.AwaitSignal` only (`processtest/park.go`), not
   `state.SignalWaiters()`, so `Harness.PublishSignal` passes forever on a
   definition parked purely on signal boundary arms. Still live in the **public**
   harness, and it blocks delivery 3's headline scenario.
7. **Suite speed.** `internal/dbtest`'s `sync.Once` container boot fires per
   package → 12 Postgres + 7 MySQL boots (~60s of a ~2min suite). Fix: honour
   `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   fallback, plus `scripts/testdb.sh up|down` and CI service wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
   No test combines `WithForceTermination` with a boundary-arming host, and
   `s.Boundaries = nil` has no semantic cover anywhere. Cheapest real fixture:
   `engine/step_terminal_test.go:669`, which already arms an error boundary on a
   sibling branch that survives the transition.

## Standing constraints

- ⚠ **Ask before using Docker** (owner, 2026-07-31 — other sessions saturate the
  daemon). Per-run approvals do **not** carry over. See the container-free list above.
- **Run the suite on the MERGED tree**, and **re-run after any `/code-review` fix**.
- `/code-review` and `/security-review` are **owner-invoked only**. Adversarial
  Opus stand-ins cut rework but miss what the real gate finds.
- Push on merge (standing preference).
- Fan out subagents **by Go package** — concurrent agents inside one package break
  each other's `go test` compile even on disjoint files.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **ADR-0165's task-by-task record** — every mutation, adjudication and false
  claim — `.superpowers/sdd/2026-08-05-structural-terminal-trigger-guard/progress.md`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template. ADR-0165 carries four
  correction blocks added during implementation; read them, they supersede the
  original text.
- **Designs** — `docs/specs/`. ADR-0165's spec §9 carries the full audit record.
- **Conventions and gates** — `CLAUDE.md`, including the new **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
