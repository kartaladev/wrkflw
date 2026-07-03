# 87. Decompose the flat `runtime` package into concept-oriented sub-packages

- Status: Accepted
- Date: 2026-07-03

## Context

The `runtime` package had grown into a flat directory of ~32 production files
plus their tests: the token-driving loop, all persistence/scheduler/call-link
port interfaces, their in-memory reference implementations, the signal bus, the
call notifier, the process chainer, the human-task service, the read-model
snapshot/actionable DTOs, and the admin lineage/stats/dead-letter helpers — all
in `package runtime`. Several problems followed:

- **Cognitive load.** A newcomer opening `runtime/` faced 60+ files with no
  grouping; unrelated concerns (a read-model DTO and the retry-backoff jitter
  source) sat side by side.
- **Coupling by proximity.** Because everything shared one package, any type
  could reach any other's unexported internals. The `MemStore` ⇄
  `MemCallLinkStore` intimacy and the driver's direct use of value records blurred
  the seam between "ports + reference data" and "behaviour".
- **A 1,175-line `runner.go`.** The single driver file mixed the option
  functions, the child/call-activity recursion, the message/signal waiter
  bookkeeping, incident resolution, cancellation propagation, and the action
  `perform` switch.
- **Import-cycle constraints were implicit.** Nothing recorded that the value
  types and ports must not depend on the behavioural code, so the layering could
  erode silently.

The spec `docs/specs/runtime-package-decomposition.md` explored the split and
its constraints. This is a **pre-v0.1.0** module, so a repo-wide breaking rename
is acceptable; there are no external consumers pinned to the old paths.

## Decision

Split the flat package into **eight** concept-oriented units with a strictly
one-directional import graph, preserving all behaviour and every public
method/signature (only import paths and one type name change):

- **`runtime/kernel`** — the leaf everything else imports. Owns all value types
  (`Token`, `AppliedStep`, `OutboxEvent`, `CallLink`, `CallOutcome`,
  `ArmedTimer`, `ChainLink`, `Outcome`, …), all port interfaces (`Store`,
  `Scheduler`, `TimerStore`, `CallLinkStore`, `ChainLinkStore`,
  `DefinitionRegistry`, `InstanceLister`, `Publisher`, the stats/lineage readers,
  …), the sentinel errors, and the **in-memory reference implementations**
  (`MemStore`, `CachingStore`, `MemScheduler`, `MemTimerStore`,
  `MemCallLinkStore`, `MemChainLinkStore`, the map/caching definition
  registries). It deliberately merges types + ports + reference fakes into one
  package because they form one tightly-cohesive substrate and splitting them
  would only reintroduce cross-package intimacy.
- **`runtime/signal`** — the `SignalBus`.
- **`runtime/calllink`** — the async-call-activity `CallNotifier`.
- **`runtime/chain`** — the process-instance `Chainer`.
- **`runtime/task`** — the human-task `TaskService`.
- **`runtime/monitor`** — the admin `LineageReader`, outbox/timer stats
  collectors, and dead-letter classification.
- **`runtime/view`** — the `InstanceSnapshot` / `ActionableView` read-model DTOs
  and `StatusString`; an **independent leaf** that imports only
  `engine`/`model`/`humantask` (never `kernel`).
- **`runtime` (root)** — a thin driver: the `ProcessDriver` (split by concern
  across `processdriver_*.go`), action resolution, outbox derivation,
  observability, timer ops, and shutdown.

Import direction: `kernel ← {calllink, chain, signal, task, monitor}`;
`kernel, signal ← runtime (root)`; `view` is independent. `kernel` must extract
before any behavioural package, otherwise a behavioural package importing the
root for types while the root imports it for the driver forms a transient cycle.

Additionally, rename the reference driver type **`Runner` → `ProcessDriver`**
(and `NewRunner` → `NewProcessDriver`). The OTel span names and instrumentation
scope string (`github.com/zakyalvan/krtlwrkflw/runtime`) are kept verbatim so
the observability contract does not change; `task` uses a package-local const
holding that same string.

Two small, additive, non-breaking test-support methods —
`MemCallLinkStore.Seed` / `SeedTerminal` — replace the former same-package
`export_test` seeding helpers, because seeding the reference store now happens
from *other* packages' tests (calllink, monitor, root), and Go `export_test`
symbols do not cross package boundaries.

## Consequences

Positive:

- **Navigability.** Each package name states its concern; the root is now a thin
  driver rather than a 60-file catch-all.
- **Enforced layering.** The one-directional DAG is now a compiler-checked fact,
  not a convention. `view` provably does not depend on `kernel`.
- **No public API-method changes.** Every method signature is identical; only
  import paths move and `Runner`→`ProcessDriver` is renamed. Migration for a
  consumer is a mechanical qualifier change.
- **Smaller review surface per file** — the 1,175-line driver became eight
  concern-scoped files.

Negative / trade-offs:

- **Repo-wide breaking change.** Every `runtime.X` reference for a moved symbol
  becomes `kernel.X` / `signal.X` / `view.X` / etc., and `Runner` is gone.
  Acceptable pre-v0.1.0; there are no pinned external consumers.
- **`kernel` is deliberately large** (types + ports + reference impls in one
  package). This is an intentional cohesion choice; splitting it further would
  reintroduce the cross-package intimacy the reference fakes rely on.
- **Value records live in `kernel`** alongside the interfaces, so a consumer that
  only wants the port interfaces still imports the reference impls' package. The
  cost is a slightly wider import, not extra binary weight (the fakes are small).
- **Package-scoped test helpers are duplicated** across the test packages that
  need them (the `must*` constructors, the `recordingScheduler`/`fixedJitter`
  doubles, a few definition fixtures). Go test helpers cannot be shared across
  packages without an exported support package; duplication was preferred over
  dragging `testing`/`testify` into a production import graph.

See the spec `docs/specs/runtime-package-decomposition.md` and the plan
`docs/plans/2026-07-03-runtime-package-decomposition.md` for the full migration
map and task-by-task procedure.
