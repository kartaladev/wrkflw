# 55. Maturity hardening: testing rigor, structured gRPC errors, and stability docs

- Status: Accepted
- Date: 2026-06-25

## Context

This is the final track of the production-hardening program (ADRs 0048–0054).
Earlier tracks added distributed timer locking, a fail-closed gRPC server, data
lifecycle pruners, message boundary events, and graceful shutdown/health. A
maturity audit of the remaining surface flagged several gaps that are individually
small but together separate a "works in tests" library from a "safe to embed in
production" one:

- **No goroutine-leak detection.** The packages that spawn or drive background
  goroutines — the gocron scheduler, the Postgres relay's `LISTEN` loop, and the
  runtime orchestrator — had no guard that those goroutines actually exit on
  context cancellation. A leaked listen loop or scheduler executor would survive a
  test silently and could survive a `ShutdownGroup.Shutdown` in production unnoticed.
- **No fuzzing of the core state machine.** `engine.Step` is a pure
  `(definition, state, trigger) → (state, commands)` function and the single most
  important invariant holder in the engine, but it was only exercised by
  hand-written example-shaped tests. Nothing asserted it never panics on
  adversarial inputs or that it never produces a structurally impossible state.
- **gRPC errors were unstructured.** REST returned a machine-readable
  `{error, message}` body (the `not_found`/`forbidden`/… taxonomy in
  `transport/rest/errors.go`), but gRPC returned only a status code plus a
  human-readable message string. A gRPC client could not branch on a stable
  reason code without parsing prose.
- **No stated stability or operational policy.** The module had no SemVer/stability
  statement, no deprecation taxonomy, no pgxpool tuning guidance, no explicit
  single-tenant statement, and no secrets-handling guidance — all of which a
  consumer embedding the library needs before depending on it.

## Decision

Make the following additive, low-risk changes. No engine/model production code is
touched (zero diff there); the changes are tests, a transport-adapter error
detail, and documentation.

### Testing rigor

- **goleak.** Add `go.uber.org/goleak` as a direct dependency and a
  `goleak.VerifyTestMain(m)` `TestMain` to the three goroutine-spawning packages
  (`internal/scheduling/gocron`, `internal/persistence/postgres`, `runtime`). No
  real engine leak surfaced — the scheduler's executor, the relay's listen loop,
  and the runtime workers all exit cleanly on context cancel. Two **scoped**
  `goleak.IgnoreTopFunction` waivers are applied with comments for unavoidable
  third-party background goroutines that live for the process lifetime:
  testcontainers' Ryuk reaper connection and pgxpool's per-pool health-check.
- **Fuzzing.** Add `engine.FuzzStep`, which derives a flat sequential process
  definition and a trigger sequence from the fuzz bytes, validates the definition
  with `model.Validate`, and drives it through `engine.Step`. It asserts three
  invariants: Step never panics; every non-nil error wraps an exported engine
  sentinel (`ErrUnknownTrigger` / `ErrInvalidTransition` / `ErrNoMatchingFlow`),
  never a bare error; and no result places a token on a node absent from the root
  definition. The seed corpus lives in `f.Add` calls and runs as a normal test on
  every `go test`; a 15s `-fuzz` run over ~2.9M executions found no crashes.

### Structured gRPC errors (no proto change)

Attach a `google.golang.org/genproto/googleapis/rpc/errdetails.ErrorInfo` detail to
every classified gRPC status via `status.WithDetails`, with `Reason` set to the
**same** machine-readable code as the REST taxonomy (`not_found`, `forbidden`,
`conflict`, `bad_request`, `conflict_state`, `internal_error`) and `Domain` set to
the engine module path. A client now branches on `ErrorInfo.Reason` regardless of
transport.

This deliberately takes the **no-proto-regen path**: `errdetails.ErrorInfo` is a
standard Google rpc type already available transitively (promoted to a direct
dependency). A bespoke proto error message would have required regenerating
`workflowpb` — heavier and riskier than the audit benefit warranted — so we did
**not** change the proto. The gap noted in the audit is closed in code, not merely
documented.

### Stability and operational documentation

- **`STABILITY.md`** — SemVer 2.0.0 policy, the honest pre-1.0/unreleased status of
  the public root packages, the public-API boundary (root packages only; not
  `internal/` or `examples/`), and a `// Deprecated:` deprecation taxonomy
  (mark → keep working for a release → remove only in MAJOR / flagged v0 MINOR).
- **README additions** — recommended pgxpool settings (MaxConns sizing against
  `max_connections`, MinConns floor, lifetimes, the PgBouncer/`LISTEN/NOTIFY`
  caveat) and the consumer-owns-the-pool contract; an explicit **single-tenant**
  statement (the engine has no built-in tenant isolation; multi-tenant consumers
  must enforce it); and **secrets** guidance for process / `HumanTask.Vars` (the
  engine does not log variables in cleartext — verified across the codebase, no
  production code logs them — and consumers must redact in their own resolvers and
  actions).

### Explicitly deferred

- **CI pipeline** (`.github/workflows`) — deferred by the user; not added in this track.

## Consequences

- The three background-worker packages now fail their suites if any test leaks a
  goroutine, locking in the clean-shutdown guarantee that ADR-0054 established. The
  two third-party waivers are narrow (top-function matches) and commented, so a
  genuine engine leak is not masked.
- `engine.Step` has a standing panic-freedom and invariant guard. The fuzz target
  is opt-in for long runs (`-fuzz`) but its seed corpus runs on every test
  invocation, so regressions in the sentinel-wrapping or token-placement invariants
  are caught without a special CI step.
- gRPC and REST clients see the same reason-code taxonomy. The detail is additive:
  clients that ignore status details are unaffected; the status code is unchanged.
  `genproto/googleapis/rpc` becomes a direct dependency.
- Consumers have a written stability promise, a deprecation contract, and the
  operational guidance (pool sizing, tenancy boundary, secret handling) needed to
  embed the engine responsibly.
- Engine/model production diff for this track is **ZERO**: the only non-test code
  change is the `transport/grpc` error-detail attachment in the adapter layer.
