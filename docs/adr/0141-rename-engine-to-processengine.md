# 141. Rename `service.Engine` → `service.ProcessEngine`

Status: Accepted — 2026-07-27. Follows the hard-rename convention of
[ADR-0098](0098-service-coherent-graph-refactor.md) (which introduced the name
`NewEngine`), [ADR-0107](0107-rename-deliver-to-apply-trigger.md), and
[ADR-0108](0108-rename-newmapcatalog-to-newcatalog.md).

## Context

`service.Engine` (`service/service.go:112`) is the sole concrete implementation
of the public `service.Service` interface — the embedded-consumer facade that
wires the `runtime.ProcessDriver`, task service, registry, stores, clock, and id
generator. Its constructor is `NewEngine(opts ...Option) (*Engine, error)`
(`service/service.go:136`).

The bare name `Engine` is weak in the public API: it collides conceptually with
the repo's own pure `engine` package (the token state machine), which
`service.go` imports and uses as `engine.Trigger`/`engine.InstanceState`. A
consumer reading `service.Engine` alongside `engine.*` and `runtime.ProcessDriver`
has to disambiguate three "engine/driver" concepts. `ProcessEngine` states what
it is — the process-orchestration facade over the driver.

Blast radius (source-verified): the type + constructor are referenced at ~86
source sites, concentrated in `service/` (the definition plus its black-box
`service_test` suite), plus 4 `examples/*_wiring/main.go`, one
`internal/transporttest/harness.go`, one `runtime/driver_shutdown.go` doc comment,
`persistence/durableprovider_test.go`, a `slog` message string
(`service.go:291`), and doc/ADR/spec/plan prose. There are **no** type aliases /
re-exports, and `samber/do` is not a dependency (no DI-container registration to
update). `ProcessEngine`/`NewProcessEngine` are free of collisions.

### Options

1. **Hard rename, no alias** (chosen) — rename the type and constructor
   everywhere in one commit. Matches this repo's established pre-v0.1.0 convention
   (ADR-0098/0107/0108: "no deprecated alias … pre-1.0"); `STABILITY.md` states
   every exported symbol may change without notice in the `v0.y.z` phase.
2. **Add `type Engine = ProcessEngine` alias + `NewEngine` shim, deprecate.**
   Rejected: contradicts the repo's own hard-rename convention, and the
   deprecate-then-remove policy in `STABILITY.md` only applies once versioned
   releases begin. A shim is dead weight pre-1.0.
3. **Leave it.** Rejected: the user requested the rename; the name genuinely
   improves the public API's clarity against `engine`/`ProcessDriver`.

## Decision

Rename the exported type `service.Engine` → `service.ProcessEngine` and its
constructor `service.NewEngine` → `service.NewProcessEngine`, everywhere, in one
feature bundle. No deprecation alias (pre-v0.1.0).

Scope details:
- The compile-time assertion `var _ Service = (*Engine)(nil)` (`service.go:301`)
  and the six `segregation_test.go` interface assertions follow the type.
- The `slog` construction message string `"service.Engine constructed"`
  (`service.go:291`) is updated to `"service.ProcessEngine constructed"` so logs
  are not stale/misleading.
- Private test helpers `newEngine`/`newOwnedEngine` (`service_test` package) are
  renamed to `newProcessEngine`/`newOwnedProcessEngine` for coherence with the
  type they build (test-only, no API impact).
- **Do NOT touch `engine.`-qualified references** (the unrelated root `engine`
  package). Only the `service`-package `Engine` type / `NewEngine` constructor
  and their qualified `service.Engine`/`service.NewEngine` uses are in scope.
- Prose comments in the touched `.go` files that name the type as "Engine"
  (e.g. `service/request.go:93`, several test-helper comments) are swept to
  "ProcessEngine" for coherence, since those files are edited anyway.
- **Current-state doc reference the rename would break:** `service/README.md`'s
  "`Service` is implemented by `*Engine`" (line 35) is corrected to
  `*ProcessEngine` so the rename does not leave a dangling non-existent type in a
  current-state doc.

### Out of scope (explicit)

- **Private "engine"-named symbols stay:** `engineConfig` (`options.go:18`),
  `validateEngineLeaves` (`service.go:261`), `logConstructionSummary(*engineConfig)`
  — they configure the engine internally and are not the exported facade. Renaming
  them is unrelated churn.
- **Pre-existing `service/README.md` + root `README.md` doc-rot is NOT fixed here**
  and is filed as a separate doc-refresh follow-up. Both docs describe the
  ADR-0098-superseded positional `service.New(...)` constructor with a nonexistent
  `EngineOption` type and the ADR-0138-superseded `WithEngineClock(clock.Clock)`
  option. That rot predates this rename by two ADRs; folding a full doc rewrite
  into a mechanical rename would be scope creep, and half-correcting only the
  `service.New` token would be incoherent. This bundle touches `service/README.md`
  only for the one line the rename itself breaks (the `*Engine` type reference).

`ProcessEngine` (a `service`-package facade) and `runtime.ProcessDriver` (the
lower-layer primitive it wires) are intentionally distinct, similar-looking names
one layer apart; the `ProcessEngine` doc comment states the relationship so a
reader does not conflate them.

## Consequences

- **Breaking public-API change** (pre-v0.1.0, no stability promise). Consumers
  using `service.Engine` / `service.NewEngine` migrate to
  `service.ProcessEngine` / `service.NewProcessEngine`. Recorded in CHANGELOG.
- Behavior-preserving: no logic changes; existing tests are the safety net (the
  rename is mechanical). No new tests are warranted beyond the compiler + the
  unchanged suite passing.
- The public API reads more clearly against the `engine` package and
  `runtime.ProcessDriver`.
- Pre-existing README doc-rot (both root and `service/` READMEs describe the
  ADR-0098/0138-superseded constructor + options) is filed as a follow-up, not
  fixed here — keeping the rename bundle mechanical.
