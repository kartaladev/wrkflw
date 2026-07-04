# 0090 — Rename `model` to `definition` and relocate node kinds into family packages

- **Status:** Accepted
- **Date:** 2026-07-04

## Context

The process-definition authoring layer lived in a single flat package, `model`.
It had accumulated a high cognitive and maintenance cost:

- **The "add-a-kind tax."** Adding one of the 19 node kinds required coordinated
  edits across ~7 files with no compile-time coupling forcing them to agree: the
  `NodeKind` iota, the struct + `Kind()` method, the JSON discriminator name, the
  `toWire`/`fromWire` switch arms, the constructor + option plumbing, the fluent
  `AddX` builder method, and — for activities — five parallel accessor switches.
- **Parallel switch duplication.** `RetryPolicyOf`, `DeadlineOf`, `ReminderOf`,
  and `recoveryFlowOf` each re-enumerated the same seven activity kinds.
- **Serialization struct duplication** (`nodeYAML` mirroring `nodeWire`) and
  **flat namespace** — all 19 kinds, ~30 options, and the package name `model`
  (which did not describe what it holds) in one package.
- **Abbreviations leaking into the public API** (`WithICEDeadline`,
  `WithESPNonInterrupting`) and an inconsistent option (`BoundaryNonInterrupting`
  missing the `With` prefix).

The package is the product's public authoring API. The goal was a deliberate
reduction of cognitive load for readers and maintainers, with functional
correctness as a hard requirement. The project is pre-v0.1.0, so backward
compatibility was explicitly not required — the whole repo is migrated in one
change.

A naive split hits an import cycle: the container/serialization/validation code
needs the concrete node types, while the node types need the container types
(`Node`, `ProcessDefinition`) and shared embeds.

## Decision

1. **Rename `model` → `definition`.** The package is named for what it holds.
2. **Group node kinds into BPMN-family leaf packages** a consumer imports
   directly: `definition/event` (start, end, terminate-end, error-end,
   intermediate catch/throw, boundary, event sub-process), `definition/gateway`
   (exclusive, parallel, inclusive, event-based), and `definition/activity`
   (service/user/receive/send/business-rule task, sub-process, call-activity).
   Constructors drop the family suffix where redundant (`event.NewStart`,
   `gateway.NewExclusive`) but keep `Task` (`activity.NewServiceTask`).
3. **True relocation** — the concrete structs, constructors, and options
   physically live in the leaves. `definition` keeps `Node`, `ProcessDefinition`,
   the builder, `Validate`, JSON/YAML (de)serialization, and the shared
   embeddable field-groups (`Base`, `ActivityFields`, `WaitFields`, `TaskAction`).
4. **Break the cycle with a driver-registration pattern** (the `image/png`,
   `database/sql` idiom). `definition` owns a per-kind registry keyed by
   `NodeKind` (`NodeSpec{Name, FromWire, ToWire}` + `RegisterKind`); each leaf
   registers its kinds in `init()`. `definition` imports **no** leaf; the leaves
   import `definition`. Serialization and the kind-name maps resolve through the
   registry. `Validate` reads kind-specific fields via `toWire` (the flat
   `NodeWire` projection) so it, too, needs no concrete types.
5. **Guarantee correctness on deserialization.** A `definition/kinds` package
   blank-imports every leaf; deserialization paths (the persistence store,
   transport decoders) import it so the registry is always populated. An
   unregistered kind returns a loud, actionable `ErrKindNotRegistered` rather than
   a silent zero value. A round-trip test asserts all 19 kinds and a frozen
   golden fixture re-encodes byte-identically.
6. **Keep fluent authoring** in a separate `definition/build` package (it may
   import the leaves, which `definition` may not); the in-package `AddX` methods,
   which had no external callers, were removed. The generic `.Add(node)` remains.
7. **Collapse the parallel accessor switches** into interface assertions on the
   shared embeds' carrier methods, and **rename the leaked-abbreviation options**
   natively in the leaves: `WithICEDeadline`→`event.WithCatchDeadline`,
   `WithICEReminder`→`event.WithCatchReminder`,
   `WithTimerDuration`→`event.WithCatchTimer`,
   `WithSignalName`→`event.WithCatchSignal`,
   `WithMessageNameAndKey`→`event.WithCatchMessage`,
   `WithESPNonInterrupting`→`event.WithEventSubProcessNonInterrupting`,
   `BoundaryNonInterrupting`→`event.WithBoundaryNonInterrupting`.

The wire/JSON/YAML format, validation rules, retry math, and engine/runtime
behaviour are unchanged.

## Consequences

- **Adding a node kind is now largely single-site**: define the struct +
  constructor + options in its leaf and register one `NodeSpec` there. The
  central `NodeKind` iota is the only remaining shared edit; the parallel
  accessor and serialization switches are gone.
- **Discoverability improves**: `activity.`/`event.`/`gateway.` autocomplete
  surfaces each family's palette; the package name states its purpose.
- **A new invariant to respect**: any code that deserializes a definition without
  otherwise importing a leaf must blank-import `definition/kinds`. Forgetting it
  yields a loud error, not silent data loss. The persistence store already
  imports it.
- **Breaking, repo-wide change** (~1,450 call sites, 158 files rewritten to the
  family packages). Acceptable pre-v0.1.0; no compatibility shim is carried.
- **A slightly larger public surface**: `Base`, `ActivityFields`, `WaitFields`,
  `TaskAction`, `NodeWire`, `NodeSpec`, and `RegisterKind` are now exported so
  leaves (and third-party node packages, in principle) can embed and register.
- **`WithName` is duplicated per family** (`activity.WithName`, `event.WithName`)
  because the option interfaces are family-scoped; this is the one place the
  universal option split.
- **Deferred:** the `nodeYAML`/`NodeWire` struct duplication was left in place —
  `yaml.go` already routes through `NodeWire` + the registry and survives the
  relocation; deduping it would require adding `yaml.Unmarshaler` to `NodeKind`/
  `ProcessDefinition` (behaviour risk) for marginal gain.
