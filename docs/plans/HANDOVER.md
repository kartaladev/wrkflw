# wrkflw Engine Core — Execution Handover

This document lets a **fresh session with zero prior context** pick up and execute the remaining engine-core plans. Read it top to bottom before starting.

## What `wrkflw` is

A library-first, BPMN-flavored Go workflow engine (Go 1.25), shipped as an importable module (no daemon we own). Authoritative references — **read these first**:

- `REQUIREMENTS.md` — the original loose requirements.
- `CLAUDE.md` — project rules (TDD discipline, root-package layout, locked tech stack, required Go skills). **Binding.**
- `docs/specs/2026-06-20-engine-core-design.md` — the engine-core design **spec** (the contract): §4 state model + timing, §5 `Trigger`/`Command` taxonomies, §6 `Step` semantics, §8 seam interfaces, §10 decisions, §12 TDD build order. When a plan and the spec disagree, the spec wins.
- ADRs: `docs/adr/0002` (pure stepper returning **Commands** driven by **Triggers**), `0003` (time via in-repo `clock.Clock`, impl by clockwork at the edge), `0004` (public packages at module root, no `pkg/`).
- Brainstorm artifacts (background, dated): `docs/specs/2026-06-20-engine-core-execution-model-*.{html,pdf}`.

## Core invariants (never violate)

1. **Pure core.** `engine` and `model` import only stdlib (+ `model`/`authz`/`humantask`/`expreval` as the spec allows). No transport/storage/bus/time-vendor in the core.
2. **No clock in the engine.** `Step` never calls `time.Now()`. Time enters as `Trigger.OccurredAt`; timer `FireAt` is computed as `OccurredAt + duration`. `clockwork` only enters via the runtime's `clock.Clock`/`Scheduler`.
3. **`Step` is deterministic** — identical `(state, trigger)` ⇒ identical `(state, commands)`. All IDs (command/token/task/timer/scope) come from in-`InstanceState` counters, never randomness or the clock. Flows are evaluated in **definition order**.
4. **`Step` is pure** — it must not mutate its input `InstanceState` (`cloneState` deep-copies; extend it for every new state field).
5. **`Step` public signature is stable:** `Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)`.
6. **Sealed sets:** `Trigger` (`isTrigger()`+`OccurredAt()`) and `Command` (`isCommand()`) are closed; adding a variant is a deliberate edit in `engine`.

## Status

| Plan | Scope | File | Status |
|---|---|---|---|
| 1 | Foundations: model+Validate, clock, Trigger/Command, InstanceState, pure `Step` (linear), action catalog, runtime + fakes | `2026-06-20-engine-core-foundations.md` | ✅ **merged to `main`** (93.4% cov) |
| 2 | Gateways: `expreval`, Exclusive (XOR), Parallel (AND fork+join) | `2026-06-20-engine-core-gateways.md` | 📝 written |
| 3 | Inclusive (OR) gateway: OR-fork + reachability OR-join | `2026-06-20-engine-core-inclusive-gateway.md` | 📝 written |
| 4 | Human tasks: `authz`, `humantask`, AwaitHuman, claim/reassign/complete + audit + bucket | `2026-06-20-engine-core-human-tasks.md` | 📝 written |
| 5 | Timers & SLA: ScheduleTimer/TimerFired, timer intermediate, SLA breach path, in-wait reminders | `2026-06-20-engine-core-timers-sla.md` | 📝 written |
| 6 | Events & event-based gateway: signal/message catch+throw, first-event-wins gateway, boundary timer/signal | `2026-06-20-engine-core-events.md` | 📝 written |
| 7 | Sub-processes & call activity: scope tree, embedded + event sub-process, call activity | `2026-06-20-engine-core-subprocesses.md` | 📝 written |
| 8 | Errors, compensation & micro-step: error end/boundary error, compensation rollback, cancel, real Micro mode | `2026-06-20-engine-core-errors-compensation.md` | 📝 written |

After Plan 8, sub-project #1 (pure engine core + reference runtime) is complete. Subsequent sub-projects (own spec→plan cycles) are productionization: Postgres persistence + hot-path cache, watermill/outbox eventing, gocron scheduler, casbin authorizer, REST/gRPC transports, admin monitoring, `ProcessInstance` response customization.

**Plans 2–3 are near-term and code-complete** (verbatim Go). **Plans 4–8 are detailed task plans** with fixed spec contracts + representative code; their exact `engine/step.go`/`runtime` edits must be **grounded against the then-current code** (read it first) — the SDD review loop catches drift.

## How to execute the next plan (clean session)

1. **Confirm prerequisites merged.** Each plan lists a Prerequisite (e.g. Plan 3 needs Plan 2). `git log --oneline` on `main` should show the prior plan's commits.
2. **Read, in order:** this file → the spec → the plan file → the current `engine/step.go`, `engine/state.go`, `runtime/runner.go` (and any package the plan touches). The plan's "Prerequisite & contracts" section assumes that current state.
3. **Branch.** Never implement on `main`: `git switch -c feat/engine-core-<slug>`.
4. **Execute with SDD.** Invoke `superpowers:subagent-driven-development`. For each task: `scripts/task-brief <plan> N` → dispatch an implementer → `scripts/review-package BASE HEAD` → dispatch a reviewer → fix Critical/Important → re-review → mark done in the ledger. The skill's scripts live under its plugin dir (`.../subagent-driven-development/scripts/`).
5. **Finish.** After all tasks + a final whole-branch review (opus), use `superpowers:finishing-a-development-branch` → merge to `main`, push, delete the branch.

### Model selection (cost/quality)
- Implementers transcribing complete plan code → cheapest tier (haiku). Multi-file/integration tasks (state+Step, runtime) → sonnet. Reviewers → haiku for tiny mechanical diffs, sonnet for logic. **Final whole-branch review → opus.** Always set the model explicitly.

## Conventions & tooling (binding)

- **TDD strict** (CLAUDE.md "TDD Operational Discipline"): every new symbol gets a failing test first, with a **visible RED** (`go test ./<pkg>/...` shows the failure) before the implementation. The SDD per-task flow makes this auditable.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an **`assert` closure per case** (project `table-test` skill — *not* `want`/`wantErr` fields); `t.Context()` over `context.Background()`. Mocks via `use-mockgen` only where a seam needs one.
- **Lint:** `golangci-lint` is **v2** — config is `.golangci.yml` with `version: "2"` and `linters.default: standard`. (v1 config syntax will break.)
- **Verify on completion:** `go test -race ./...` green; `go test -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` ≥ 85% on touched packages; `golangci-lint run ./...` clean.
- **Commits:** Conventional Commits scoped to the area; end with the `Co-Authored-By: Claude Opus 4.8 (1M context)` trailer. Commit per logical change; ask before merging to `main`.
- **gitignored scratch:** `cover.out`, `.superpowers/` (SDD briefs/reports/ledger/diffs), `.claude/settings.local.json`. Don't commit them.

## Hard-won lessons from Plan 1 (apply them)

- **The plan's code can be wrong.** Plan 1's `step.go` had a real bug (`ActionCompleted` re-fired the action because it didn't advance the token past the completed ServiceTask). The implementer caught it via the test's RED state. **Always observe the red state; trust the test, not the plan listing.**
- **`golangci-lint` v2 config** differs from v1 — use the `version: "2"` form (already in `.golangci.yml`).
- **Determinism/purity are tested, not assumed** — keep the Plan-1 `TestStepIsDeterministic`/`TestStepDoesNotMutateInput` green; extend `cloneState` whenever you add an `InstanceState` field (new slices/maps/pointers must be deep-copied).
- **`consumeToken` allocates a fresh slice** (no in-place `s.Tokens[:0]`) — preserve that when editing token lifecycle; parallel/scoped tokens depend on it.
- **Ports return `error`** (reconciled to spec §8 in Plan 1's final fix): `StateStore.Load (…, error)` with `ErrInstanceNotFound`, `Save/Append/Write` return `error`. New ports follow suit.
- **`NewRunner` arity grows** each infra plan (scheduler, actor resolver, task store, …) — update call sites/tests in the same task.

## Quick map of the merged code (Plan 1)

- `model/` — `ProcessDefinition`, `Node` (+`NodeKind` incl. all gateway/event/activity kinds), `SequenceFlow` (+`Condition`,`IsDefault`), lookups, `Validate` (+sentinels).
- `clock/` — `Clock` interface + `System()`.
- `engine/` — `trigger.go` (sealed Triggers + ctors), `command.go` (sealed Commands), `state.go` (`InstanceState`,`Token`,`NodeVisit`,`Status`,`TokenState`,`CmdSeq`/`TokenSeq`), `step.go` (`Step`, `drive`, helpers).
- `action/` — `ServiceAction`, `Catalog`, `MapCatalog`, `Func`.
- `runtime/` — `ports.go` (`StateStore`/`Journal`/`JournalReader`/`OutboxWriter` + `ErrInstanceNotFound`), `memory.go` (`MemStateStore`/`MemJournal`/`MemOutbox`), `runner.go` (`Runner`,`NewRunner`,`Run`).
