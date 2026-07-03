# Runtime Package Decomposition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the flat 32-file `runtime` package into 8 concept-oriented packages with a strict one-directional import graph, split the 1,175-line driver by concern, and rename `Runner` → `ProcessDriver`.

**Architecture:** A `runtime/kernel` package owns all value types, port interfaces, and the in-memory reference implementation (the leaf everything imports). Focused behavioral packages (`calllink`, `chain`, `signal`, `task`, `view`, `monitor`) hold the code with real logic. The `ProcessDriver` stays at the module root. Import graph is strictly one-directional: `kernel ← {calllink, chain, signal, task, monitor}`; `kernel, signal ← runtime (root)`; `view` is an independent leaf.

**Tech Stack:** Go 1.25. Behavior-preserving refactor — the existing 68 test files are the safety net. Tooling: `git mv`, `goimports`, `gofmt -r`, `go build`, `go test`, `golangci-lint`.

## Global Constraints

- **Go 1.25** — hard requirement.
- **Behavior-preserving.** No production logic changes. Per TDD discipline, no new tests; the full existing suite must pass **before and after** every task. A "red" state here is a **build failure** (undefined identifier after a move); "green" is `go build ./... && go test ./...` clean.
- **No `pkg/` prefix** (ADR-0004). New packages are `runtime/<name>` at their natural path.
- **Breaking change is intended** (pre-v0.1.0). No compatibility shim / no root type aliases. Every `runtime.X` caller migrates to the new package.
- **Error sentinel prefix** stays `workflow-runtime:` — moving a sentinel does not change its message.
- **Kernel package name is `kernel`** (chosen); driver type is **`ProcessDriver`**.
- Commit per task with Conventional Commits scoped `refactor(runtime)`. End every commit message with the `Co-Authored-By` trailer.
- Working branch: `refactor/runtime-decomposition` (already created; the spec is committed there).

## Package → source-file map (reference for all tasks)

| Target package | Production files moved in (with their same-named `_test.go`) |
|---|---|
| `runtime` (root, stays) | `processdriver*.go` (from split `runner.go`), `resolve_action.go`, `outbox.go`, `observability.go`, `timerops.go`, `shutdown.go` |
| `runtime/kernel` | `ports.go`, `ownership.go`, `lister.go`, `opsstats.go`, `jitter.go`, `publisher.go`, `errors_construct.go`, `definition_registry.go`, `caching_definition_registry.go`, `memstore.go`, `caching_store.go`, `timerstore.go`, `scheduler.go`, `calllink.go`, `mem_calllink.go`, `chainlink.go` |
| `runtime/signal` | `broadcast.go` → `signalbus.go` |
| `runtime/calllink` | `call_notifier.go` → `notifier.go` |
| `runtime/chain` | `chainer.go` |
| `runtime/task` | `taskservice.go` → `service.go` |
| `runtime/view` | `instance_snapshot.go`, `instance_actionable.go` |
| `runtime/monitor` | `lineage.go`, `stats_collector.go`, `dlq.go`, `dlq_category.go` |

**Cross-cutting test files stay in root `runtime`** (they exercise the driver + multiple collaborators and will gain imports as symbols move): all `*_e2e_test.go`, `runner_test.go`, `runner_metrics_test.go`, `runner_retry_test.go`, `runner_action_timeout_test.go`, `cancel_test.go`, `cancel_handler_test.go`, `cancel_propagation_test.go`, `compensation_oncancel_test.go`, `scope_compensation_test.go`, `rehydrate_test.go`, `action_panic_test.go`, `expression_timeout_test.go`, `sendtask_outbox_test.go`, `outbox_test.go`, `terminal_events_test.go`, `resolve_action_internal_test.go`, `timerops_internal_test.go`, `perform_fireforget_test.go`, `subprocess_*_test.go`, and the `*_example_test.go` files that construct a driver. Unit tests paired with a moved production file move with it.

## Mechanical procedure (used by every extraction task)

For each package extraction:

1. `git mv` the production file(s) into the new directory; change `package runtime` → `package <pkg>` in each.
2. **Re-qualify external callers** (files outside `runtime/` that used `runtime.Sym`): these always use the qualified form, so a safe AST rewrite works — for each moved exported symbol `Sym`, run
   `gofmt -w -r 'runtime.Sym -> <pkg>.Sym' <files>` then `goimports -w <files>` to fix the import line.
3. **Re-qualify root callers** (files still in `runtime/` that used the bare `Sym`): run `go build ./runtime/...`; the compiler lists each `undefined: Sym`. Add the `<pkg>.` qualifier at each site and the import. (Do not blind-sed bare identifiers — they collide with struct fields.)
4. Move paired `_test.go` files; black-box tests change `package runtime_test` → `package <pkg>_test` and re-qualify; internal (white-box) tests move only if their subject moved.
5. `go build ./... && go test ./...` → must be green.
6. Commit.

---

### Task 1: Split and rename the driver (`Runner` → `ProcessDriver`)

Pure intra-`runtime` work: no package moves, no import-path changes. Split `runner.go` (1,175 lines) into per-concern files and rename the type.

**Files:**
- Delete: `runtime/runner.go` (content redistributed below)
- Create: `runtime/processdriver.go`, `runtime/processdriver_options.go`, `runtime/processdriver_child.go`, `runtime/processdriver_waiters.go`, `runtime/processdriver_message.go`, `runtime/processdriver_incident.go`, `runtime/processdriver_cancel.go`, `runtime/processdriver_action.go`
- Modify (repo-wide rename): every file referencing `runtime.Runner` / `runtime.NewRunner` — `transport/grpc/server.go`, `transport/rest/*.go`, `scheduling/scheduler.go`, `service/*.go`, `persistence/*.go`, all `examples/**/main.go`, and root test files.

**Interfaces:**
- Produces: `type ProcessDriver struct { ... }` (was `Runner`, fields unchanged); `func NewProcessDriver(...) (*ProcessDriver, error)` (was `NewRunner`, same signature); methods unchanged: `Run`, `Deliver`, `DeliverMessage`, `ResolveIncident`, `CancelInstance`. `type Option func(*ProcessDriver)` and all `With*` options keep their names.

- [ ] **Step 1: Split `runner.go` by concern.** Move the declarations into the new files per this grouping (receiver stays `*Runner` for now — renamed in Step 3):
  - `processdriver.go`: `Runner` struct, `NewRunner`, `Run`, `Deliver`, `deliverLoop`
  - `processdriver_options.go`: `Option`, `defaultActionTimeout`, all `With*` option funcs
  - `processdriver_child.go`: `callDepthKey`, `maxCallDepth`, `callDepth`, `withCallDepth`, `runChild`
  - `processdriver_waiters.go`: `msgKey`, `syncWaiters`, `syncSignalBus`, `syncMsgWaiters`, `findMessageWaiter`
  - `processdriver_message.go`: `DeliverMessage`
  - `processdriver_incident.go`: `ResolveIncident`
  - `processdriver_cancel.go`: `CancelInstance`, `propagateCancel`
  - `processdriver_action.go`: `actionContext`, `safeActionDo`, `copyVarsForOutcome`, `terminalErr`

- [ ] **Step 2: Build to confirm the split alone compiles.**

Run: `go build ./runtime/...`
Expected: PASS (no rename yet — pure code motion within one package).

- [ ] **Step 3: Rename the type repo-wide.** Within `runtime/`, rename identifier `Runner` → `ProcessDriver` and `NewRunner` → `NewProcessDriver` (use `gofmt -w -r 'Runner -> ProcessDriver' runtime/*.go` then `gofmt -w -r 'NewRunner -> NewProcessDriver' runtime/*.go` — AST-aware, safe). For external callers:

```bash
FILES=$(grep -rl 'runtime\.\(Runner\|NewRunner\)' --include='*.go' . | grep -v '^./runtime/')
for f in $FILES; do
  gofmt -w -r 'runtime.NewRunner -> runtime.NewProcessDriver' "$f"
  gofmt -w -r 'runtime.Runner -> runtime.ProcessDriver' "$f"
done
```

- [ ] **Step 4: Rename internal helper names for consistency (optional, cosmetic).** `runnerObs` → `driverObs`, `newRunnerObs` → `newDriverObs`, `runnerInstrumentationName` string value unchanged (keep the OTel instrumentation name stable: `github.com/zakyalvan/krtlwrkflw/runtime`).

- [ ] **Step 5: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS. If a test references `Runner`, the AST rewrite in Step 3 already covered `runtime/*_test.go`; fix any stragglers the compiler names.

- [ ] **Step 6: Commit.**

```bash
git add -A
git commit -m "$(printf 'refactor(runtime): split driver by concern, rename Runner->ProcessDriver\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: Extract `runtime/kernel` (types + ports + reference impl)

The largest task. Move the 16 kernel files out of root into `runtime/kernel`; every remaining root file and every external caller re-qualifies to `kernel.X`.

**Files:**
- Create dir `runtime/kernel/`; `git mv` the 16 production files (and their paired `_test.go`) listed in the map above.
- Modify: all root `runtime/*.go` referencing moved symbols (add `kernel.` qualifier), plus external callers in `internal/persistence/store/*` (22), `persistence/*` (15), `transport/*` (23), `service/*` (8), `eventing/*` (8), `scheduling/*` (3), `examples/**`.

**Interfaces:**
- Produces (now `kernel.`-qualified): value types `Token`, `AppliedStep`, `OutboxEvent`, `CallLink`, `CallOutcome`, `PendingNotify`, `ArmedTimer`, `ChainLink`, `Outcome` (+`OutcomeCompleted/Failed/Terminated`); ports `Store`, `JournalReader`, `Scheduler`, `TimerStore`, `CallLinkStore`, `ChainLinkStore`, `DefinitionRegistry`, `InstanceLister`, `Ownership`, `Publisher`, `JitterSource`, `OutboxStatsReader`, `TimerStatsReader`, `CallLineageReader`, `ChainLineageReader`; DTOs `InstanceFilter`, `InstanceSummary`, `InstancePage`, `OutboxStats`, `TimerStats`, `CallLinkRef`, `ChainLinkRef`, `InstanceLineage`; funcs `EncodeCursor`, `DecodeCursor`, `NormalizeLimit`, `NewJitterSource`, `NewMemStore`, `NewCachingStore`, `NewMapDefinitionRegistry`, `NewCachingDefinitionRegistry`, `NewMemScheduler`, `NewMemTimerStore`, `NewMemCallLinkStore`, `NewMemChainLinkStore`; impls `MemStore`, `CachingStore`, `MemTimerStore`, `MemScheduler`, `MemCallLinkStore`, `MemChainLinkStore`, `MapDefinitionRegistry`, `CachingDefinitionRegistry`, `AlwaysOwn`; sentinels `ErrInstanceNotFound`, `ErrInstanceExists`, `ErrConcurrentUpdate`, `ErrNilDependency`, `ErrBadCursor`, `ErrNoCallLink`, `ErrChainLinkExists`, `ErrDefinitionNotFound`; all `With*` options for the moved constructors.

- [ ] **Step 1: Move production files + set package.**

```bash
cd runtime && mkdir kernel
git mv ports.go ownership.go lister.go opsstats.go jitter.go publisher.go \
       errors_construct.go definition_registry.go caching_definition_registry.go \
       memstore.go caching_store.go timerstore.go scheduler.go \
       calllink.go mem_calllink.go chainlink.go kernel/
for f in kernel/*.go; do perl -i -pe 's/^package runtime$/package kernel/' "$f"; done
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | head -40`
Expected: FAIL — many `undefined: Store`, `undefined: CallLink`, `runtime.Store` errors across root and external packages. This is the expected red state.

- [ ] **Step 3: Re-qualify external callers (safe AST rewrite).** For every moved symbol, rewrite `runtime.Sym -> kernel.Sym` in non-`runtime/` files, then fix imports:

```bash
SYMS="Token AppliedStep OutboxEvent CallLink CallOutcome PendingNotify ArmedTimer ChainLink Outcome \
OutcomeCompleted OutcomeFailed OutcomeTerminated Store JournalReader Scheduler TimerStore CallLinkStore \
ChainLinkStore DefinitionRegistry InstanceLister Ownership Publisher JitterSource OutboxStatsReader \
TimerStatsReader CallLineageReader ChainLineageReader InstanceFilter InstanceSummary InstancePage \
OutboxStats TimerStats CallLinkRef ChainLinkRef InstanceLineage EncodeCursor DecodeCursor NormalizeLimit \
NewJitterSource NewMemStore NewCachingStore NewMapDefinitionRegistry NewCachingDefinitionRegistry \
NewMemScheduler NewMemTimerStore NewMemCallLinkStore NewMemChainLinkStore MemStore CachingStore \
MemTimerStore MemScheduler MemCallLinkStore MemChainLinkStore MapDefinitionRegistry CachingDefinitionRegistry \
AlwaysOwn ErrInstanceNotFound ErrInstanceExists ErrConcurrentUpdate ErrNilDependency ErrBadCursor \
ErrNoCallLink ErrChainLinkExists ErrDefinitionNotFound MemStoreOption CachingStoreOption \
CachingDefinitionRegistryOption MemCallLinkOption MemSchedulerOption WithCacheTTL WithCacheMaxEntries \
WithCacheLogger WithCachingStoreClock WithCallLinks WithTimers WithMemCallLinkLease WithMemCallLinkClock \
WithMemSchedulerClock WithCachingDefinitionRegistryClock"
EXT=$(grep -rl '"github.com/zakyalvan/krtlwrkflw/runtime"' --include='*.go' . | grep -v '^./runtime/')
for f in $EXT; do for s in $SYMS; do gofmt -w -r "runtime.$s -> kernel.$s" "$f"; done; goimports -w "$f"; done
```

- [ ] **Step 4: Re-qualify root callers (compiler-driven).** In files still in `runtime/`, prefix bare references with `kernel.` and add the import. Iterate:

```bash
go build ./runtime/... 2>&1 | grep 'undefined:' | sort -u
```
For each `undefined: Sym`, add `kernel.` at the reported sites and `import "github.com/zakyalvan/krtlwrkflw/runtime/kernel"`. Run `goimports -w runtime/*.go` after. Repeat until the root package compiles.

- [ ] **Step 5: Move + fix kernel unit tests.** `git mv` each paired unit test (`ports_test.go`, `lister_test.go`, `jitter_test.go`, `ownership_test.go`, `publisher_test.go`, `memstore_test.go`, `memstore_lister_test.go`, `memstore_helper_test.go`, `caching_store_test.go`, `caching_store_example_test.go`, `caching_store_alwaysown_test.go`, `definition_registry_test.go`, `caching_definition_registry_test.go`, `timerstore_test.go`, `scheduler_test.go`, `mem_calllink_test.go`, `mem_calllink_export_test.go`, `mem_calllink_lease_test.go`, `chainlink_test.go`) into `kernel/`. For black-box ones change `package runtime_test` → `package kernel_test` and drop the now-unneeded `kernel.` prefix on in-package symbols; `mem_calllink_export_test.go` (white-box `package runtime` → `package kernel`) moves as-is. Root cross-cutting tests that used bare kernel symbols gain the `kernel.` qualifier + import (compiler-driven, same as Step 4).

- [ ] **Step 6: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add -A
git commit -m "$(printf 'refactor(runtime): extract kernel package (types, ports, reference impl)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: Extract `runtime/signal`

**Files:**
- `git mv runtime/broadcast.go runtime/signal/signalbus.go`; `git mv runtime/broadcast_test.go runtime/signal/signalbus_test.go`
- Modify: root `processdriver*.go` (the driver holds `*SignalBus` via `WithSignalBus`) → `signal.SignalBus`; external callers of `runtime.SignalBus`/`NewSignalBus`/`DeliverFunc`.

**Interfaces:**
- Consumes: `kernel.*` (Publisher, sentinels, clock via existing imports).
- Produces: `signal.SignalBus`, `signal.NewSignalBus`, `signal.DeliverFunc`, `signal.SignalBusOption`, `signal.WithSignalBusClock`.

- [ ] **Step 1: Move file + set package `signal`.**

```bash
cd runtime && mkdir signal
git mv broadcast.go signal/signalbus.go && git mv broadcast_test.go signal/signalbus_test.go
perl -i -pe 's/^package runtime$/package signal/' signal/signalbus.go
perl -i -pe 's/^package runtime_test$/package signal_test/' signal/signalbus_test.go
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | grep -E 'undefined: SignalBus|runtime.SignalBus' | head`
Expected: FAIL referencing `SignalBus`/`NewSignalBus`.

- [ ] **Step 3: Re-qualify callers.**

```bash
for s in SignalBus NewSignalBus DeliverFunc SignalBusOption WithSignalBusClock; do
  for f in $(grep -rl "runtime\.$s" --include='*.go' . | grep -v '^./runtime/signal/'); do
    gofmt -w -r "runtime.$s -> signal.$s" "$f"; goimports -w "$f"; done
done
go build ./runtime/... 2>&1 | grep 'undefined:'   # add signal. + import at each root site
```
Fix each root site with `signal.` + import; `goimports -w runtime/*.go`.

- [ ] **Step 4: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'refactor(runtime): extract signal package (SignalBus)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: Extract `runtime/calllink`

**Files:**
- `git mv runtime/call_notifier.go runtime/calllink/notifier.go` + paired tests `call_notifier_test.go`, `call_notifier_observability_test.go` → `runtime/calllink/`.
- Modify: external callers of `runtime.CallNotifier`/`NewCallNotifier`/`CallDeliverFunc`/`CallNotifierOption`/`WithCallNotifier*`. (Root driver does NOT reference `CallNotifier` — no root change expected.)

**Interfaces:**
- Consumes: `kernel.CallLinkStore`, `kernel.DefinitionRegistry`, `kernel.CallLink`, `kernel.ErrNilDependency`.
- Produces: `calllink.CallNotifier`, `calllink.NewCallNotifier`, `calllink.CallDeliverFunc`, `calllink.CallNotifierOption`, `calllink.WithCallNotifierBatchSize/PollInterval/Clock/Logger/TracerProvider/MeterProvider`.

- [ ] **Step 1: Move files + set package `calllink`.**

```bash
cd runtime && mkdir calllink
git mv call_notifier.go calllink/notifier.go
git mv call_notifier_test.go calllink/notifier_test.go
git mv call_notifier_observability_test.go calllink/notifier_observability_test.go
perl -i -pe 's/^package runtime$/package calllink/' calllink/notifier.go
for f in calllink/*_test.go; do perl -i -pe 's/^package runtime_test$/package calllink_test/' "$f"; done
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | grep -E 'CallNotifier' | head`
Expected: FAIL referencing `CallNotifier`/`NewCallNotifier`.

- [ ] **Step 3: Re-qualify callers.**

```bash
for s in CallNotifier NewCallNotifier CallDeliverFunc CallNotifierOption WithCallNotifierBatchSize \
         WithCallNotifierPollInterval WithCallNotifierClock WithCallNotifierLogger \
         WithCallNotifierTracerProvider WithCallNotifierMeterProvider; do
  for f in $(grep -rl "runtime\.$s" --include='*.go' . | grep -v '^./runtime/calllink/'); do
    gofmt -w -r "runtime.$s -> calllink.$s" "$f"; goimports -w "$f"; done
done
```

- [ ] **Step 4: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'refactor(runtime): extract calllink package (CallNotifier)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: Extract `runtime/chain`

**Files:**
- `git mv runtime/chainer.go runtime/chain/chainer.go` + paired tests `chainer_test.go`, `chainer_example_test.go` → `runtime/chain/`.
- Modify: external callers of `runtime.Chainer`/`NewChainer`/`ChainEvent`/`SuccessorPolicy`/`SuccessorDecision`/`InstanceStarter`/`ChainerOption`/`WithChain*`.

**Interfaces:**
- Consumes: `kernel.ChainLinkStore`, `kernel.ChainLink`, `kernel.Outcome`, `kernel.ErrInstanceExists`, `kernel.ErrNilDependency`.
- Produces: `chain.Chainer`, `chain.NewChainer`, `chain.ChainEvent`, `chain.SuccessorPolicy`, `chain.SuccessorDecision`, `chain.InstanceStarter`, `chain.ChainerOption`, `chain.WithChainLinks/WithChainClock/WithChainLogger/WithChainTracerProvider/WithChainMeterProvider`.

- [ ] **Step 1: Move files + set package `chain`.**

```bash
cd runtime && mkdir chain
git mv chainer.go chain/chainer.go
git mv chainer_test.go chain/chainer_test.go
git mv chainer_example_test.go chain/chainer_example_test.go
perl -i -pe 's/^package runtime$/package chain/' chain/chainer.go
for f in chain/*_test.go; do perl -i -pe 's/^package runtime_test$/package chain_test/' "$f"; done
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | grep -E 'Chainer|ChainEvent|InstanceStarter' | head`
Expected: FAIL.

- [ ] **Step 3: Re-qualify callers.**

```bash
for s in Chainer NewChainer ChainEvent SuccessorPolicy SuccessorDecision InstanceStarter ChainerOption \
         WithChainLinks WithChainClock WithChainLogger WithChainTracerProvider WithChainMeterProvider; do
  for f in $(grep -rl "runtime\.$s" --include='*.go' . | grep -v '^./runtime/chain/'); do
    gofmt -w -r "runtime.$s -> chain.$s" "$f"; goimports -w "$f"; done
done
```

- [ ] **Step 4: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'refactor(runtime): extract chain package (Chainer)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: Extract `runtime/task`

**Files:**
- `git mv runtime/taskservice.go runtime/task/service.go` + `taskservice_test.go` → `runtime/task/service_test.go`.
- Modify: external callers of `runtime.TaskService`/`NewTaskService`/`TaskServiceOption`/`WithTaskService*`.

**Interfaces:**
- Consumes: `kernel.*` (none directly beyond sentinels), plus external `humantask`, `authz`, `engine`.
- Produces: `task.TaskService`, `task.NewTaskService`, `task.TaskServiceOption`, `task.WithTaskServiceMeterProvider`, `task.WithTaskServiceClock`.

- [ ] **Step 1: Move files + set package `task`.**

```bash
cd runtime && mkdir task
git mv taskservice.go task/service.go && git mv taskservice_test.go task/service_test.go
perl -i -pe 's/^package runtime$/package task/' task/service.go
perl -i -pe 's/^package runtime_test$/package task_test/' task/service_test.go
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | grep -E 'TaskService' | head`
Expected: FAIL.

- [ ] **Step 3: Re-qualify callers.**

```bash
for s in TaskService NewTaskService TaskServiceOption WithTaskServiceMeterProvider WithTaskServiceClock; do
  for f in $(grep -rl "runtime\.$s" --include='*.go' . | grep -v '^./runtime/task/'); do
    gofmt -w -r "runtime.$s -> task.$s" "$f"; goimports -w "$f"; done
done
```

- [ ] **Step 4: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'refactor(runtime): extract task package (TaskService)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 7: Extract `runtime/monitor`

**Files:**
- `git mv` `lineage.go`, `stats_collector.go`, `dlq.go`, `dlq_category.go` → `runtime/monitor/`; paired tests `lineage_test.go`, `mem_lineage_test.go`, `stats_collector_test.go`, `dlq_category_test.go` → `runtime/monitor/`.
- Modify: external callers of `runtime.LineageReader`/`NewLineageReader`/`OutboxStatsCollector`/`NewOutboxStatsCollector`/`TimerStatsCollector`/`NewTimerStatsCollector`/`DeadLetter`/`ClassifyDeadLetter`.

**Interfaces:**
- Consumes: `kernel.CallLineageReader`, `kernel.ChainLineageReader`, `kernel.InstanceLineage`, `kernel.CallLinkRef`, `kernel.ChainLinkRef`, `kernel.CallLink`, `kernel.ChainLink`, `kernel.OutboxStatsReader`, `kernel.TimerStatsReader`, `kernel.ErrNilDependency`.
- Produces: `monitor.LineageReader`, `monitor.NewLineageReader`, `monitor.OutboxStatsCollector`, `monitor.NewOutboxStatsCollector`, `monitor.TimerStatsCollector`, `monitor.NewTimerStatsCollector`, `monitor.DeadLetter`, `monitor.ClassifyDeadLetter`.

- [ ] **Step 1: Move files + set package `monitor`.**

```bash
cd runtime && mkdir monitor
git mv lineage.go stats_collector.go dlq.go dlq_category.go monitor/
git mv lineage_test.go mem_lineage_test.go stats_collector_test.go dlq_category_test.go monitor/
for f in monitor/*.go; do perl -i -pe 's/^package runtime$/package monitor/; s/^package runtime_test$/package monitor_test/' "$f"; done
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | grep -E 'LineageReader|StatsCollector|DeadLetter|ClassifyDeadLetter' | head`
Expected: FAIL.

- [ ] **Step 3: Re-qualify callers.**

```bash
for s in LineageReader NewLineageReader OutboxStatsCollector NewOutboxStatsCollector \
         TimerStatsCollector NewTimerStatsCollector DeadLetter ClassifyDeadLetter; do
  for f in $(grep -rl "runtime\.$s" --include='*.go' . | grep -v '^./runtime/monitor/'); do
    gofmt -w -r "runtime.$s -> monitor.$s" "$f"; goimports -w "$f"; done
done
```

- [ ] **Step 4: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'refactor(runtime): extract monitor package (lineage, stats, dead-letter)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 8: Extract `runtime/view`

**Files:**
- `git mv runtime/instance_snapshot.go runtime/instance_actionable.go runtime/view/`; paired tests `instance_snapshot_test.go`, `instance_actionable_test.go` → `runtime/view/`.
- Modify: external callers of `runtime.InstanceSnapshot`/`NewInstanceSnapshot`/`ActionableView`/`NewActionableView`/`NextAction`/`ActionableTask` and the snapshot sub-view types (`TokenView`, `NodeVisitView`, `IncidentView`, `TaskView`, `ActionBindingView`).

**Interfaces:**
- Consumes: external `engine`, `model`, `humantask` only (independent leaf — does not import `kernel`).
- Produces: `view.InstanceSnapshot`, `view.NewInstanceSnapshot`, `view.ActionableView`, `view.NewActionableView`, `view.NextAction`, `view.ActionableTask`, `view.TokenView`, `view.NodeVisitView`, `view.IncidentView`, `view.TaskView`, `view.ActionBindingView`.

- [ ] **Step 1: Move files + set package `view`.**

```bash
cd runtime && mkdir view
git mv instance_snapshot.go instance_actionable.go view/
git mv instance_snapshot_test.go instance_actionable_test.go view/
for f in view/*.go; do perl -i -pe 's/^package runtime$/package view/; s/^package runtime_test$/package view_test/' "$f"; done
```

- [ ] **Step 2: Build to see the red state.**

Run: `go build ./... 2>&1 | grep -E 'InstanceSnapshot|ActionableView' | head`
Expected: FAIL.

- [ ] **Step 3: Re-qualify callers.**

```bash
for s in InstanceSnapshot NewInstanceSnapshot ActionableView NewActionableView NextAction ActionableTask \
         TokenView NodeVisitView IncidentView TaskView ActionBindingView; do
  for f in $(grep -rl "runtime\.$s" --include='*.go' . | grep -v '^./runtime/view/'); do
    gofmt -w -r "runtime.$s -> view.$s" "$f"; goimports -w "$f"; done
done
```

- [ ] **Step 4: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'refactor(runtime): extract view package (snapshot, actionable)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 9: ADR-0087, README, and final verification

**Files:**
- Create: `docs/adr/0087-runtime-package-decomposition.md`
- Modify: `runtime/README.md` (new package layout + `ProcessDriver` name); any package-level doc comment in `runtime/processdriver.go` referencing old names.

- [ ] **Step 1: Write ADR-0087 (Nygard template).** Sections: Status (Accepted, 2026-07-03), Context (flat 32-file package, cognitive load, ~60 call-site coupling, `MemStore`↔`MemCallLinkStore` intimacy, cycle constraints), Decision (the 8-package split + `kernel` merge + `ProcessDriver` rename; reference the spec at `docs/specs/runtime-package-decomposition.md`), Consequences (positive: navigability, thin root, one-directional DAG, no API-method changes; negative: repo-wide breaking change, deliberately large `kernel`, value records in `kernel`). Follow `docs/adr/0001-record-architecture-decisions.md` formatting.

- [ ] **Step 2: Update `runtime/README.md`.** Replace `Runner`→`ProcessDriver` throughout; add a "Package layout" section documenting `kernel`, `calllink`, `chain`, `signal`, `task`, `view`, `monitor` and the import direction.

- [ ] **Step 3: Full verification gate.**

```bash
go build ./...
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: build clean; tests pass under `-race`; touched packages ≥ 85% line coverage; lint clean.

- [ ] **Step 4: Confirm no import cycles / stray old refs.**

```bash
go list ./runtime/... >/dev/null            # cycle check: must print no error
grep -rn 'runtime\.\(Runner\|NewRunner\)' --include='*.go' . | grep -v '_test' || echo "no stray Runner refs"
```
Expected: clean.

- [ ] **Step 5: Commit.**

```bash
git add -A && git commit -m "$(printf 'docs(adr): ADR-0087 runtime package decomposition; update README\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

## Self-review notes

- **Spec coverage:** every spec section maps to a task — 8-package layout (Tasks 2–8 + root), `runner.go` split (Task 1), `ProcessDriver` rename (Task 1, added post-spec per maintainer), migration map (Tasks 2–8 re-qualify steps), ADR + README (Task 9), verification checklist (Task 9 Steps 3–4).
- **`ProcessDriver` rename** was decided after the spec was written; Task 9 Step 1 records it in the ADR so the spec/ADR stay consistent. (Optionally back-port the rename note into the spec before starting.)
- **Ordering rationale:** `kernel` must extract before any behavioral package, otherwise a behavioral package importing root `runtime` for types while root imports it for the driver would form a transient import cycle that fails to compile mid-refactor. `view` is last only for convenience (independent leaf).
- **`Option` type** deliberately keeps its name (`runtime.Option`) — it is the root driver's functional-option type and does not stutter.
