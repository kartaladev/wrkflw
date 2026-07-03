# Runtime package decomposition

**Status:** Proposed — 2026-07-03
**Related ADR:** ADR-0087 (to be written)
**Author:** brainstormed with the maintainer

## Context

`runtime/` is the reference driver most library consumers import. It has grown
into a **single flat `package runtime` of 32 non-test `.go` files (~4,866 lines)**
plus 68 test files. No file is individually badly factored, but the flat
namespace hides all structure: to know which file holds what, a maintainer must
hold the whole package in their head. Reading it imposes high cognitive load and
makes it hard to correlate related files.

Two concrete pain points:

1. **`runner.go` is a 1,175-line elephant** — 24% of the package — mixing ~7
   distinct concerns (construction/options, the deliver loop, call-activity
   child execution, waiter/signal/message sync, message delivery, incident
   resolution, cancellation, and action-execution plumbing).
2. **Related files do not sort together.** Natural concept clusters
   (call-activity, chaining, timers, storage, human tasks, read models, ops
   monitoring) are scattered across the flat file list.

The goal is **readability for maintenance**: regroup the package by
functionality into coherent, independently-comprehensible sub-packages with
well-defined one-directional dependencies.

### Constraints discovered during analysis

- **The whole repo imports `runtime.X`.** Roughly 60 call sites across
  `internal/persistence/store` (22 files), `transport/rest` (12),
  `transport/grpc` (11), `persistence` (15), `service` (8), `eventing` (8),
  `scheduling` (3), and every `examples/` binary reference exported `runtime`
  symbols. Sub-packaging that renames types is a **repo-wide breaking change**.
  This is acceptable: v0.1.0 has not been tagged, so now is the cheapest time.
- **`MemStore` is intimately coupled to the in-memory correlation stores.**
  `MemStore.Commit` writes call-link correlation through
  `MemCallLinkStore.record(...)`, an **unexported** method — cross-package
  unexported access is illegal in Go, so `MemStore` and `MemCallLinkStore` must
  share a package. `MemStore` also calls `MemTimerStore.Arm/Cancel`, which are
  not on the `TimerStore` interface, so it needs the concrete type. These
  couplings mean the in-memory reference stores belong together.
- **`ports` originally reached back into impl clusters**, creating dependency
  cycles: `AppliedStep` embeds `*CallLink`, `*CallOutcome`, `[]ArmedTimer`, and
  the lineage-reader interfaces return `*CallLink`/`*ChainLink`. Any
  decomposition must break these cycles.

## Decision

Split the flat `runtime` package into **8 packages** with a strict,
one-directional import graph. The shared value types, port interfaces, and the
**in-memory reference implementation** collapse into a single kernel package
(`runtime/kernel`); the code with real behavioral logic moves into small, focused,
concept-oriented packages; and the `Runner` stays at the module root.

### Target packages

| Package | Contents | Internal imports |
|---|---|---|
| `runtime` (root) | the `Runner`/driver, split by concern (below); `resolve_action.go`, `outbox.go`, `observability.go`; `timerOpsFor` (folded in from timer-ops); `ShutdownGroup` | `kernel`, `signal` |
| `runtime/kernel` | **kernel + in-memory reference implementation.** All value types (`Token`, `AppliedStep`, `OutboxEvent`, `CallLink`, `CallOutcome`, `PendingNotify`, `ArmedTimer`, `ChainLink`, `Outcome` + consts); all port interfaces (`Store`, `JournalReader`, `Scheduler`, `TimerStore`, `CallLinkStore`, `ChainLinkStore`, `DefinitionRegistry`, `InstanceLister`, `Ownership`, `Publisher`, `JitterSource`, `OutboxStatsReader`, `TimerStatsReader`, `CallLineageReader`, `ChainLineageReader`); DTOs (`InstanceFilter/Summary/Page`, `OutboxStats`, `TimerStats`, `CallLinkRef`, `ChainLinkRef`, `InstanceLineage`); cursor helpers (`EncodeCursor`/`DecodeCursor`/`NormalizeLimit`); `AlwaysOwn`; `NewJitterSource`; all sentinel errors; and the reference impls `MemStore`, `CachingStore`, `MemCallLinkStore`, `MemTimerStore`, `MemScheduler`, `MemChainLinkStore`, `MapDefinitionRegistry`, `CachingDefinitionRegistry` | — (external only) |
| `runtime/calllink` | `CallNotifier` (background parent-resume delivery worker) | `kernel` |
| `runtime/chain` | `Chainer`, `SuccessorPolicy`, `InstanceStarter`, `ChainEvent`, `SuccessorDecision` | `kernel` |
| `runtime/signal` | `SignalBus` | `kernel` |
| `runtime/task` | `TaskService` | `kernel` |
| `runtime/view` | `InstanceSnapshot`, `ActionableView` (+ their string helpers) | — (engine/model/humantask) |
| `runtime/monitor` | `LineageReader`, `OutboxStatsCollector`, `TimerStatsCollector`, `DeadLetter`, `ClassifyDeadLetter` | `kernel` |

### Import DAG (cycle-free)

```
kernel  ← calllink, chain, signal, task, monitor
kernel, signal  ← runtime (root)
view          (independent leaf — engine/model only)
```

Cycles are broken because `kernel` owns every shared value type and port
interface **and** the reference impls that were the source of the back-edges;
`kernel` imports nothing internal. The `Runner` uses port *interfaces*
(`kernel.Store`, `kernel.CallLinkStore`, `kernel.TimerStore`, `kernel.DefinitionRegistry`)
rather than the `Mem*` impls, so the root imports only `kernel` + `signal` (the
latter for the concrete `*SignalBus` held via `WithSignalBus`). `monitor`
depends only on `kernel` because the lineage-reader interfaces and their `Mem*`
implementations both live in `kernel`.

### `runner.go` split (all remain `package runtime`)

| New file | Holds |
|---|---|
| `runner.go` | `Runner` struct, `NewRunner`, `Run`, `Deliver`, `deliverLoop` |
| `runner_options.go` | `Option` + all `With*` options + defaults (`defaultActionTimeout`, etc.) |
| `runner_child.go` | `runChild`, `callDepth`/`withCallDepth`, `callDepthKey`, `maxCallDepth` |
| `runner_waiters.go` | `syncWaiters`, `syncSignalBus`, `syncMsgWaiters`, `findMessageWaiter`, `msgKey` |
| `runner_message.go` | `DeliverMessage` |
| `runner_incident.go` | `ResolveIncident` |
| `runner_cancel.go` | `CancelInstance`, `propagateCancel` |
| `runner_action.go` | `actionContext`, `safeActionDo`, `copyVarsForOutcome`, `terminalErr` |
| `timerops.go` | `timerOpsFor` (moved from the timer cluster into the root, its only caller) |

Existing `resolve_action.go`, `outbox.go`, `observability.go` stay as-is.

### Public API migration map

Every `runtime.X` reference across the repo moves to a new package. Summary of
the non-`kernel` relocations (everything not listed maps to `kernel`):

| From | To |
|---|---|
| `runtime.InstanceSnapshot`, `NewInstanceSnapshot`, `ActionableView`, `NewActionableView`, `NextAction`, `ActionableTask`, view sub-DTOs | `view` |
| `runtime.LineageReader`, `NewLineageReader`, `OutboxStatsCollector`, `NewOutboxStatsCollector`, `TimerStatsCollector`, `NewTimerStatsCollector`, `DeadLetter`, `ClassifyDeadLetter` | `monitor` |
| `runtime.SignalBus`, `NewSignalBus`, `DeliverFunc`, `WithSignalBusClock` | `signal` |
| `runtime.TaskService`, `NewTaskService`, `TaskServiceOption`, `With*` | `task` |
| `runtime.Chainer`, `NewChainer`, `ChainEvent`, `SuccessorPolicy`, `SuccessorDecision`, `InstanceStarter`, `ChainerOption`, `WithChain*` | `chain` |
| `runtime.CallNotifier`, `NewCallNotifier`, `CallDeliverFunc`, `CallNotifierOption`, `WithCallNotifier*` | `calllink` |
| `runtime.Runner`, `NewRunner`, `Option`, `With*`, `ShutdownGroup`, `ShutdownFunc` | `runtime` (unchanged) |
| everything else (`Store`, `Token`, `AppliedStep`, `OutboxEvent`, `MemStore`, `CachingStore`, `MemCallLinkStore`, `MemTimerStore`, `MemChainLinkStore`, `MemScheduler`, `Map/CachingDefinitionRegistry`, `CallLink`, `CallOutcome`, `ChainLink`, `Outcome`+consts, `ArmedTimer`, `Ownership`, `AlwaysOwn`, `Publisher`, `JitterSource`, cursor funcs, `InstanceFilter/Summary/Page/Lister`, `OutboxStats/TimerStats`+readers, `CallLinkRef/ChainLinkRef/InstanceLineage`, `CallLineageReader/ChainLineageReader`, `DefinitionRegistry`, all `Err*` sentinels) | `kernel` |

## Consequences

### Positive

- The flat 32-file package becomes 8 packages, each one comprehensible on its
  own. A maintainer navigates by concept, not by scanning a flat list.
- The 1,175-line `runner.go` becomes ~8 focused files under 300 lines each.
- One-directional import graph makes dependencies obvious and prevents future
  tangling; `kernel` is the single, well-known kernel.
- No public interface *method* changes and no cross-package unexported hacks —
  the merge into `kernel` is what makes this possible. The `record()` intimacy
  stays intra-package.
- The `runtime/store` vs `internal/persistence/store` name collision is avoided
  entirely, because there is no `runtime/store` package.

### Negative / trade-offs

- **Repo-wide breaking change.** ~60 call sites across `internal/persistence`,
  `transport`, `persistence`, `service`, `eventing`, `scheduling`, and
  `examples/` must update imports (`runtime.X` → `kernel.X` / `view.X` /
  `monitor.X` / `signal.X` / `task.X` / `chain.X` / `calllink.X`). Mechanical
  but broad. Acceptable pre-v0.1.0.
- **`kernel` is deliberately large** (~2,000 lines): it holds types, ports, and
  the full in-memory reference implementation. This is an accepted trade to
  keep zero API-method changes and honor `MemStore`↔`MemCallLinkStore`
  intimacy. It reads as one cohesive concept ("state + storage + contracts +
  reference impl"). A custom backend (`internal/persistence/store`) imports
  `kernel` only for the ports/types; it transitively compiles the reference impls
  (harmless).
- **Value records live in `kernel`, not their concept package** (e.g.
  `kernel.CallLink` while `calllink.CallNotifier`). The behavioral code — the
  part with real cognitive load — is in the concept package; only trivial
  structs sit in `kernel`.

## Execution notes

- This is a **behavior-preserving refactor**. Per TDD discipline, no new tests
  are required, but the full existing suite must pass **before and after** each
  package extraction. The 68 existing test files move with their subjects;
  black-box `runtime_test` tests split into `core_test`, `view_test`, etc.,
  updating references.
- Extract one package at a time, compiling and running `go test ./...` after
  each, to keep the tree green throughout. Suggested order (leaves first):
  `view` → `monitor` → `signal` → `task` → `calllink` → `chain` → `kernel`
  (the big one) → root `runner.go` file-split → repo-wide call-site updates.
- Record the decision as **ADR-0087** (Nygard template) referencing this spec.
- Update `runtime/README.md` and any package docs to reflect the new layout.

## Verification checklist

- [ ] All 8 packages compile; `go build ./...` clean.
- [ ] `go test ./...` from repo root passes — no regressions anywhere.
- [ ] `go test -race` clean; touched packages keep ≥ 85% line coverage.
- [ ] `golangci-lint run ./...` clean.
- [ ] No import cycles (`go list` / build confirms the DAG above).
- [ ] Every external caller (`transport`, `persistence`, `service`, `eventing`,
      `scheduling`, `examples/`, `internal/persistence/store`) updated and green.
- [ ] `runner.go` split into the files above; none exceeds ~300 lines.
- [ ] ADR-0087 written; `runtime/README.md` updated.
