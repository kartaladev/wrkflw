# Server-Generated Instance ID — Design Spec

- **Date:** 2026-07-06
- **Status:** Approved (pending implementation)
- **ADR:** 0100 (to be written alongside implementation)
- **Slug:** server-generated-instance-id

## Problem

Today a process instance's identifier is **caller-supplied**: the consumer passes
`InstanceID` into the start-instance call, and it is `validate:"required"` on the
HTTP boundary. There is no ID-minting anywhere in the start path — the engine
copies the caller's string verbatim onto `engine.InstanceState`.

We want the engine to **always generate** the InstanceID server-side, using a
**configurable strategy**:

- **Default:** [`github.com/rs/xid`](https://github.com/rs/xid) — compact
  (~20-char, lowercase base32hex, no hyphens), k-sortable, no external
  coordination.
- **Alternative:** UUID v7 via
  [`github.com/google/uuid`](https://github.com/google/uuid) `NewV7()` —
  chronologically sortable, RFC 9562.
- **Pluggable:** consumers can inject their own generator.

This follows the project's **sensible-default** principle: zero-config yields a
working `xid` generator; the strategy is overridable via a functional option, in
the same shape as the existing `clock.Clock` seam.

## Scope

**In scope**

- A new **nested** package `runtime/idgen` exposing a `Generator` interface plus
  `XID()` (default), `UUIDv7()`, and a `Func(...)` adapter/test-seam.
- **Always server-generated InstanceID**: remove `InstanceID` from the
  start-instance inputs (`service.StartInstanceRequest` and the HTTP `StartInput`
  DTO); mint it in the service layer; return it on the response.
- `WithIDGenerator(gen)` functional options on **both** the `service` and
  `runtime` layers (nil-guarded, default `idgen.XID()`), mirroring `WithClock`.
- `runtime.ProcessDriver.Run` generates the ID when the passed `instanceID` is
  empty; a non-empty value is used as-is (keeps direct-runtime callers and
  child-instance creation working).
- New dependency `github.com/rs/xid`. (`github.com/google/uuid v1.6.0` is already
  present — UUID v7 needs no new dep.)
- ADR-0100.

**Out of scope (this iteration)**

- The `DefRef` → `definition.Qualifier` type swap — a **separate** spec/plan.
- Any change to **derived** IDs (command IDs, task tokens, timer IDs, outbox
  dedup keys, child-instance IDs). These are deterministically derived from the
  InstanceID and MUST keep embedding it unchanged.
- A caller-supplied **idempotency key** mechanism (see Consequences — the
  idempotency tradeoff of always-generating).

## Approach

### The `runtime/idgen` package

A neutral, injectable seam analogous to `clock`. It lives nested under `runtime`
(not a new root package): `runtime` owns the `ProcessDriver` and the instance
lifecycle where generation fires, and `service` already depends on `runtime`, so
both import `runtime/idgen` with no cycle. It stays out of `engine` (which must
remain transport/storage/vendor-pure — the engine core *receives* the ID and does
not mint it).

```go
// Package idgen mints process-instance identifiers behind a pluggable strategy.
package idgen

// Generator mints a unique process-instance identifier. NewID returns an error
// so a rare entropy failure (e.g. from UUID v7) surfaces as a clean
// StartInstance error rather than a panic. The xid generator never errors.
type Generator interface {
    NewID() (string, error)
}

// XID returns the DEFAULT generator, backed by github.com/rs/xid.
// xid.New never fails, so NewID always returns a nil error.
func XID() Generator

// UUIDv7 returns a generator backed by github.com/google/uuid NewV7
// (chronologically sortable). NewID propagates the (rare) entropy error.
func UUIDv7() Generator

// Func adapts a plain function into a Generator. Use it in tests to inject a
// deterministic sequence via WithIDGenerator.
func Func(fn func() (string, error)) Generator
```

Concrete impls (sketch):

```go
type xidGen struct{}
func (xidGen) NewID() (string, error) { return xid.New().String(), nil }

type uuidV7Gen struct{}
func (uuidV7Gen) NewID() (string, error) {
    u, err := uuid.NewV7()
    if err != nil {
        return "", fmt.Errorf("workflow-idgen: uuidv7: %w", err)
    }
    return u.String(), nil
}
```

Error sentinels/messages use the `workflow-idgen:` prefix per project convention.

### Always server-generated

`InstanceID` is **removed** from the start-instance inputs:

- `service.StartInstanceRequest` — the `InstanceID` field is deleted.
- HTTP `transport/http/httpcore.StartInput` — the `instance_id` field
  (`validate:"required"`) is deleted; the start-instance endpoint no longer reads
  it.

Generation happens in the service layer, at `service.Engine.StartInstance`, before
the run:

```go
func (e *Engine) StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error) {
    def, err := e.reg.Lookup(ctx, req.DefRef) // (DefRef unchanged in THIS spec)
    if err != nil { ... }
    id, err := e.idgen.NewID()
    if err != nil {
        return ProcessInstance{}, fmt.Errorf("workflow-service: start: generate id: %w", err)
    }
    st, err := e.runner.Run(ctx, def, id, req.Vars)
    ...
}
```

The generated ID already flows back to the caller: `StartInstance` returns a
`ProcessInstance` whose `InstanceID` (from `engine.InstanceState`) carries the
minted value. No response-DTO change is needed — the existing mapper surfaces it.

### Runtime generate-on-empty

`runtime.ProcessDriver.Run(ctx, def, instanceID string, vars)` keeps its
signature. Its behavior gains one rule: when `instanceID == ""`, it mints one via
the driver's generator; a non-empty `instanceID` is used verbatim.

```go
func (r *ProcessDriver) Run(ctx, def, instanceID string, vars) (engine.InstanceState, error) {
    if instanceID == "" {
        id, err := r.idgen.NewID()
        if err != nil { return engine.InstanceState{}, fmt.Errorf("workflow-runtime: run: generate id: %w", err) }
        instanceID = id
    }
    st := engine.InstanceState{InstanceID: instanceID}
    ...
}
```

This keeps two things intact:

- **Direct-runtime callers and existing tests** that pass an explicit
  `instanceID` are unaffected (non-empty → used as-is). Only the *service* and
  *HTTP* layers, which used to require an explicit ID, change.
- **Child instances** (call-activity) are created via `runChild` with an explicit
  structural ID (`<parent>-sub-cN`), NOT via `Run`'s root path — so they never hit
  the generator and their O(depth)-growth naming scheme is preserved.

### Injection (functional options)

Following the exact `WithClock` pattern (nil-guarded, sensible default):

```go
// runtime/processdriver_options.go
func WithIDGenerator(gen idgen.Generator) Option {
    return func(r *ProcessDriver) { if gen != nil { r.idgen = gen } }
}
// field r.idgen idgen.Generator; constructor default: idgen.XID()

// service/options.go
func WithIDGenerator(gen idgen.Generator) Option {
    return func(c *engineConfig) { if gen != nil { c.idgen = gen } }
}
// engineConfig.idgen default idgen.XID(); wired into the driver the service
// builds via runtime.WithIDGenerator(c.idgen), exactly as WithClock does.
```

### Derived IDs — unchanged

All IDs derived from the InstanceID keep embedding it verbatim; they are **not**
touched by this change:

- `nextCommandID` → `<InstanceID>-c<N>`
- `nextTaskToken` → `<InstanceID>-h<N>`
- `nextTimerID` → `<InstanceID>-tm<N>`
- outbox dedup key → `<InstanceID>:<seq>:<i>`
- child instance ID → `<InstanceID>-sub-c<N>`

Because `xid` contains no hyphens, the child-suffix parsing
(`strings.LastIndex(cmdID, "-")`) remains unambiguous with the default generator.
UUID v7's hyphens still work (uniqueness preserved), just noted.

## Components

| Unit | Responsibility | Depends on |
|---|---|---|
| `idgen.Generator` | Interface: mint an instance ID (may error) | stdlib |
| `idgen.XID()` | Default generator | `github.com/rs/xid` |
| `idgen.UUIDv7()` | Sortable UUID generator | `github.com/google/uuid` |
| `idgen.Func` | Function adapter / deterministic test seam | stdlib |
| `runtime.ProcessDriver` | Generate-on-empty at root `Run`; `WithIDGenerator` | `runtime/idgen` |
| `service.Engine` | Always-generate at `StartInstance`; `WithIDGenerator` | `runtime/idgen` |
| HTTP `StartInput` / `service.StartInstanceRequest` | Drop `InstanceID` input field | — |

## Format & storage constraints (verified)

- `instance_id` is a free-form PK/FK column: Postgres/SQLite `TEXT`, MySQL
  `VARCHAR(255)`. Both `xid` (~20 chars) and UUID v7 (36 chars) fit the 255 cap.
- InstanceID is a URL path segment (`/instances/{id}`); both forms are URL-safe.
- InstanceID is a keyset-pagination tiebreaker (`ORDER BY started_at DESC,
  instance_id DESC`); both `xid` and UUID v7 are roughly time-sortable, and
  ordering is dominated by `started_at`, so cursor behavior is unaffected.

## Testing strategy

- **TDD strict** (CLAUDE.md): a failing test precedes each new exported symbol —
  the `Generator` interface usage, `XID`, `UUIDv7`, `Func`, both `WithIDGenerator`
  options, the generate-on-empty branch, and the service always-generate path.
- `idgen` unit tests: `XID().NewID()` returns a valid non-empty xid and a nil
  error; uniqueness across many calls; `UUIDv7().NewID()` returns a v7 UUID,
  monotonic-ish sortable, and propagates a forced error via a seam if feasible;
  `Func` returns exactly what the wrapped function returns (including an error).
- Determinism: tests that need fixed IDs inject
  `idgen.Func(func() (string, error) { ... fixed sequence ... })` via
  `WithIDGenerator` — replacing today's explicit-`InstanceID` inputs at the
  service/HTTP layers. Runtime tests that pass an explicit `instanceID` to `Run`
  stay as-is.
- Service test: `StartInstance` with no caller ID returns a `ProcessInstance`
  whose `InstanceID` equals the injected generator's output; a generator error
  surfaces as a `StartInstance` error.
- HTTP test: a start request body without `instance_id` succeeds (no
  `validate:"required"` failure) and the response carries the generated ID; the
  transport parity suite (stdlib/gin/fiber) is updated in lockstep.
- Coverage ≥ 85% on `runtime/idgen`, and no regression on `runtime`/`service`;
  `golangci-lint run ./...` clean.

## Migration / blast radius

- **New package** `runtime/idgen` + new dep `github.com/rs/xid` (`go get`, `go mod
  tidy`).
- **Breaking (pre-v0.1.0, acceptable):**
  - `service.StartInstanceRequest.InstanceID` removed.
  - HTTP `StartInput.instance_id` removed (wire contract change; `def_ref`
    unchanged in this spec).
  - `runtime.NewProcessDriver` / `service.NewEngine` gain a `WithIDGenerator`
    option and an internal `idgen` field defaulted to `idgen.XID()`.
- **Non-breaking:** `runtime.ProcessDriver.Run` signature is unchanged; only its
  empty-`instanceID` behavior is added, so existing explicit-ID callers/tests keep
  working.
- Test updates: service-layer and HTTP-layer tests drop `instance_id` inputs and,
  where a specific ID is asserted, inject a deterministic `idgen.Func`. Runtime
  tests passing explicit IDs to `Run` are untouched.

## Consequences

**Positive**

- Instance IDs are engine-owned and consistent; consumers can't collide or inject
  malformed IDs.
- Configurable strategy with a sensible default (`xid`), overridable to UUID v7 or
  a custom generator via one option — matching the `WithClock` ergonomics.
- Sortable IDs (both strategies) preserve reasonable keyset-cursor behavior.

**Negative / risks**

- **Idempotency loss.** With always-generate, a client network-retry of "start"
  creates a *second* instance; previously a caller-supplied ID made retries
  idempotent via the PK. This is an accepted tradeoff for this iteration; a
  dedicated idempotency-key mechanism is a possible future follow-up (explicitly
  out of scope).
- Breaking wire/API change (removed `instance_id` input) — acceptable pre-v0.1.0,
  documented in ADR-0100.
- One new dependency (`github.com/rs/xid`) — tiny, pure-Go, no transitive I/O.

## Open questions (resolved)

- Generation trigger → always server-generate; `InstanceID` removed from inputs.
- Strategy → `xid` default, UUID v7 alternative, pluggable via `WithIDGenerator`.
- Interface shape → `NewID() (string, error)`.
- Package location → nested `runtime/idgen` (not a new root package).
