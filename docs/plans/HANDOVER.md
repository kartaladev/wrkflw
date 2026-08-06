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

**▶ Pick up here: ADR-0165 (structural terminal-trigger guard) has PASSED ITS
FULL DELIVERY GATE and is UNMERGED on `feat/terminal-trigger-guard`. Suite green
on the merged tree, `/code-review` 5-of-5 fixed and re-verified,
`/security-review` 0 vulnerabilities. The ONLY thing left is step 3:
squash → merge `--no-ff` → push.**

`main` has not moved — the branch is unmerged.

| | |
|---|---|
| `main` | `8832021` — delivery 2b merged and pushed 2026-08-04, clean |
| **`feat/terminal-trigger-guard`** | **ADR-0165, implemented through Phase 6.3. Ten commits ahead of `main`: eight `wip(engine):`, one per phase, plus two `docs(engine):` — deliberately NOT squashed.** ⚠ No SHA quoted: this file rides in the bundle, so any amend would stale it (rule #10) |
| `parked/scope-and-fanout-design` | ADR-0158 draft (delivery 3) + a superseded ADR-0162 draft. **Do not read its 0162.** ⚠ ~4 deliveries stale |
| `parked/terminal-transitions`, `feat/scope-lifecycle-correctness` | merged; delete or ignore |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept only for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only |
| Latest ADR on `main` | **0164**. 0165 is on the feature branch. 0158 lands with delivery 3; 0155–0157 reserved by the older parked branch. Next free is **0166** |
| v0.1.0 | not tagged |

## The immediate next step: run ADR-0165's delivery gate

Steps 1 and 2 are done and the gate has passed. Only step 3 remains.

1. ~~**Full repo suite**~~ — ✅ **DONE and GREEN** (owner-approved Docker run, 2026-08-06), and **re-run green after `/code-review`'s fixes**. `go test -race -coverprofile=cover.out ./...` → EXIT=0, 64 packages, zero failures, zero data races; repo total **73.6 %**, `engine` **91.9 %**. Ran on the merged tree (`main` verified unmoved at `8832021` locally and at `origin`; branch is a straight descendant). ⚠ **Re-run again after any `/security-review` fix.**
2. ~~**`/code-review`**, then **`/security-review`**~~ — ✅ **BOTH DONE** (2026-08-06). `/code-review`: 5 findings, 5 fixed, folded via `--amend`, suite + lint re-run green on the fixed tree. `/security-review`: **0 vulnerabilities** — nothing to fold, so the tree is unchanged since the suite re-run. Detail in the plan's `▶ Progress`.
3. **Squash all ten branch commits into one feature bundle** (step 6.7) — verify the count with `git log --oneline main..HEAD | wc -l` rather than trusting this number — using the commit-message template at the bottom of the plan, then merge `--no-ff` and push.

⚠ **Run the suite on the MERGED tree, and re-run after any review-driven fix** —
2b's first run certified a tree that no longer existed. This has now been honoured
once (post-`/code-review`); `/security-review` gets the same treatment.

### What is verified, and what is not

The **full repo suite** is green under `-race` (EXIT=0, 64 packages, zero
failures, zero data races) and `golangci-lint run ./...` reports 0 issues.
`engine` coverage **91.9 %** (pre-delivery baseline 91.8 %, floor held); repo
total **73.6 %**, unchanged from the known pre-existing figure.

⚠ `service` sits at **52.6 %**, below the 85 % floor. It is **not** this
delivery's regression — the branch adds only a test file there and touches no
`service` production file, so this delivery could only have raised it. It is
backlog item 4, and it is worth a decision of its own rather than silence.

⚠ **`./runtime/...` is NOT container-free** — `main_test.go`,
`rehydrate_durable_test.go`, `jobstore_rehydrate_durable_test.go` and
`timer_txflow_test.go` import `internal/dbtest`. The list above is the whole
container-free set.

The two items previously owed here are now **discharged** (phase 6.3, this
session — detail in the plan's `▶ Progress` block):

- **Both cross-layer pins are mutation-verified.** Flipping
  `SubInstanceCompleted`/`SubInstanceFailed` to `rejectWithError` reddens
  `TestCallNotifierMarksLinkNotifiedWhenParentIsTerminal`; flipping
  `SignalReceived` reddens `TestSignalBusPublishToleratesATerminalFanOutTarget`.
  In both, the control subtest stayed PASS, so the pins discriminate.
  `engine/trigger.go` was restored byte-identical to `HEAD` after each.
- **Phase 6.2's three godoc nits are fixed** — the `[Symbol]` doc-link style is
  now consistent through `engine/errors.go`'s sentinel block,
  `ErrInstanceTerminal` no longer names `httpcore.ClassifyError` by a bare
  selector, and `CancelRequested`'s first paragraph now covers the
  compensation-first branch it used to skip. A throwaway `go/doc` parser over
  `./engine` reports **87 resolved doc links, zero unresolved** across var and
  type comments. Note for future edits: cross-package links resolve against the
  **package's** imports, not the file's.

Phase 6.2's godoc sweep still has had **no independent review** — it landed
after code work was paused, so the gate's `/code-review` is its first. It
carries a self-verification record worth reading: it cut **four of its own draft
sentences** that failed source-verification (the worst claimed
`HumanCandidatesResolved` has "no caller to inform", when
`runtime/task.TaskService.RefreshCandidates` is a synchronous API that builds
exactly that trigger) and fixed **two pre-existing false claims** —
`NewReverseToStart` and `NewReverseToNode` each said the constructor "always
sets a non-empty" node, which an empty argument falsifies.

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

## After ADR-0165 — delivery 3

**ADR-0158 — a broadcast signal fires every matching arm per family.** Draft on
`parked/scope-and-fanout-design`. **Still needs its own rule-#9 audit in split
form**, and its bundle is now ~4 deliveries stale: it predates 2a, 2b and 0165
entirely, still carries superseded 0161/0162 drafts, and every engine file it
touches (`step_triggers.go`, `step_cancel.go`, `state.go`) has moved under it —
0165 in particular deleted eight guards and reshaped `dispatch`.
**Re-verify every premise against source, by execution, before acting on it.**

ADR-0165 was its prerequisite: delivery 3 multiplies traffic through
`handleSignalReceived` and `handleMessageReceived`, both of which are now
guarded.

⚠ Its headline scenario is still **untestable by consumers** — see blocker 6.

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
