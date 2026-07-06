# 0100. Server-generated instance ID via pluggable runtime/idgen

Status: **Accepted — 2026-07-06.**
Spec: `docs/specs/2026-07-06-server-generated-instance-id.md`.
Plan: `docs/plans/2026-07-06-server-generated-instance-id.md`.
Follows: [ADR-0099](0099-persistence-caching-refactor.md) (persistence caching refactor). Mirrors the `clock.Clock` pluggability seam from [ADR-0003](0003-clock-interface.md).

## Context

Before this ADR, every caller was required to supply a non-empty `instance_id` on the HTTP
start-instance request (`validate:"required"` on `StartInput.InstanceID`) and as a field on
the `service.StartInstanceRequest` struct. There was no server-side minting: the engine
trusted whatever the caller provided, including duplicates, structured keys, or empty strings.

This had two consequences:

1. **Consumers did ID generation inconsistently.** Each consumer picked their own UUID/ULID
   library and format. IDs had no guaranteed lexicographic ordering, which makes cursor-based
   pagination and event-stream correlation harder.
2. **No server-controlled strategy.** Unlike the clock (ADR-0003, `clock.Clock` / `WithClock`),
   there was no in-engine seam for an operator or test to swap the ID-generation strategy. A
   consumer who wanted sortable IDs had to do it entirely outside the engine.

The goal is an engine-owned, consistently generated instance ID with a sensible k-sortable
default, pluggable via the same `With*` option pattern as `WithClock`.

## Decision

### D1 — New nested package `runtime/idgen`: `Generator`, `XID`, `UUIDv7`, `Func`

Package `runtime/idgen` is the ID-generation counterpart to the `clock` package: a small,
injectable seam with a sensible default.

```go
// Generator mints a unique process-instance identifier. NewID returns an error
// so a rare entropy failure (e.g. from UUID v7) surfaces as a clean caller error
// rather than a panic; the xid generator never errors.
type Generator interface {
    NewID() (string, error)
}
```

Three constructors:

| Constructor | Backing library | Format | Errors |
|---|---|---|---|
| `XID()` | `github.com/rs/xid` v1.6.0 | ~20-char lowercase base32hex, no hyphens, k-sortable, no coordination | never (`nil`) |
| `UUIDv7()` | `github.com/google/uuid` (already present) | RFC 9562 UUIDv7, chronologically sortable, hyphenated | propagates rare entropy error |
| `Func(fn func()(string,error))` | — | deterministic caller-supplied sequence | caller-defined |

`XID` is the default everywhere. `UUIDv7` is offered as an alternative for consumers who need
standard UUID format. `Func` is the test adapter — inject a counter or fixed sequence via
`WithIDGenerator`.

`NewID` returns an error so that a rare `UUIDv7` entropy failure surfaces as a clean
`StartInstance` error rather than a panic. The `XID` generator never errors (returns `nil`
always); the `error` return is the cost of a uniform interface.

### D2 — `WithIDGenerator` on `runtime.ProcessDriver`: generate on empty `instanceID`

`runtime.ProcessDriver` gains an `idgen idgen.Generator` field, defaulting to `idgen.XID()`.
The option:

```go
// WithIDGenerator sets the strategy used to mint a process-instance ID when
// ProcessDriver.Run is called with an empty instanceID. Default: idgen.XID().
// A nil generator is ignored. Inject idgen.Func in tests for determinism.
func WithIDGenerator(gen idgen.Generator) Option
```

`ProcessDriver.Run` generates the ID only when the caller passes an empty `instanceID`:

```go
if instanceID == "" {
    id, gerr := r.idgen.NewID()
    if gerr != nil {
        return engine.InstanceState{}, fmt.Errorf("workflow-runtime: run: generate id: %w", gerr)
    }
    instanceID = id
}
```

An explicit non-empty `instanceID` is preserved verbatim. This is the low-level seam; the
service layer (D3) always passes empty, so the driver always mints.

### D3 — `WithIDGenerator` on `service.Engine`: always-generate in `StartInstance`

`service.Engine` gains an `idgen idgen.Generator` field, defaulting to `idgen.XID()`. The
option:

```go
// WithIDGenerator sets the strategy used to mint every new process-instance ID.
// Default: idgen.XID(). A nil generator is ignored. It is also threaded into the
// default driver, so runtime and service agree on the strategy.
func WithIDGenerator(gen idgen.Generator) Option
```

`service.Engine.StartInstance` always mints the ID (no empty-string check):

```go
id, err := e.idgen.NewID()
if err != nil {
    return nil, fmt.Errorf("workflow-service: start instance: generate id: %w", err)
}
```

The option also threads `gen` into the default `runtime.ProcessDriver` it builds (via
`runtime.WithIDGenerator(c.idgen)`), so the runtime and service layers agree on the same
strategy without the consumer having to configure both.

### D4 — Removal of `InstanceID` from `StartInstanceRequest` and HTTP `StartInput`

`service.StartInstanceRequest.InstanceID` is removed — the field no longer exists. Callers
supply only `DefRef` and `Vars`; the server mints the ID and returns it in the response.

The HTTP `StartInput` DTO in `transport/http/httpcore` loses its `instance_id` field and
`validate:"required"` tag — the key is absent from the request body entirely. The `instance_id`
KEY remains in all **response** bodies (the server always returns the generated ID).

Other requests that target an existing instance — `DeliverSignal`, `CancelInstance`,
`ResolveIncident`, `ClaimTask`, `CompleteTask`, `ReassignTask`, `DeliverMessage` — are
unchanged; they still carry `InstanceID` / `TaskToken` because they address an instance that
already exists.

### D5 — Derived and child IDs unchanged

All derived identifiers that embed the instance ID verbatim are unchanged:

- Command IDs (step + sequence counter suffixes)
- Task tokens (`<instanceID>-task-<nodeID>`)
- Timer IDs
- Outbox dedup keys
- Child-instance IDs (`<parent>-sub-cN`)

The sortability guarantee provided by XID propagates naturally to all derived IDs.

## Consequences

**Positive**

- **Engine-owned consistent IDs.** Every instance ID is minted with the same strategy; no
  caller divergence.
- **K-sortable by default.** XID-generated IDs are lexicographically ordered by creation
  time, making cursor pagination and event correlation trivial without an explicit timestamp
  column.
- **Pluggable strategy, `WithClock`-parity ergonomics.** Swap to UUIDv7 or a deterministic
  test sequence with a single option; the pattern is identical to `WithClock`.
- **Clean entropy errors.** A rare `UUIDv7` entropy failure surfaces as a returned error
  from `StartInstance` (with a descriptive wrap), not a panic.
- **Test determinism.** `idgen.Func(counter)` gives tests a predictable, reproducible
  instance-ID sequence without mocking the whole driver.

**Negative / risks**

- **Idempotency loss on start retries.** Previously, a caller could retry `StartInstance`
  with the same `instance_id` and rely on the engine's optimistic-concurrency guard to
  detect the duplicate. With server-generated IDs, a network failure after the server mints
  the ID but before the response arrives leaves the caller with no way to recover the ID
  without a list/search query. This is accepted pre-v0.1.0; a future `idempotency_key`
  header on the HTTP start endpoint is the planned mitigation (follow-up).
- **Breaking wire and API change.** `instance_id` is removed from the start-instance
  request body; `StartInstanceRequest.InstanceID` is removed from the Go API. Pre-v0.1.0 —
  no shims required; all call sites updated.
- **One new dependency.** `github.com/rs/xid` v1.6.0 is added to `go.mod`/`go.sum`. It is
  a small, pure-Go library with no transitive dependencies; the footprint is minimal.
  `github.com/google/uuid` was already present.
