# 51. Fail-closed gRPC server helper + request validation

- Status: Accepted
- Date: 2026-06-25

> **Superseded by ADR-0094.** The gRPC transport and `NewSecureServer` are
> removed. The admin-by-composition design in ADR-0095 replaces the fail-closed
> default with the safer default-absent approach (admin endpoints do not exist
> unless mounted on a consumer-secured router group).

## Context

`RegisterWorkflowServiceServer` registers the WorkflowService onto a
consumer-owned `grpc.Server` and provides no built-in authorization — the
docstring warns that registering without an auth interceptor "exposes
unauthenticated enumeration of all process instances" (plus start/cancel/signal/
claim). This is asymmetric with REST, where admin routes sit behind a default-deny
middleware. A consumer who skims the docstring ships an open control plane. The
review also flagged that gRPC did **no request validation** (REST rejects empty
`def_ref`/`instance_id`), and that handlers read the `actor` from the request body
with nothing tying it to an authenticated principal.

The user chose to keep the existing registration non-breaking and add a
fail-closed helper, rather than make the default registration fail-closed (which
would break current consumers).

## Decision

- **`NewSecureServer(svc, auth grpc.UnaryServerInterceptor, opts ...Option) *grpc.Server`**
  builds a `grpc.Server` with `auth` chained as a unary interceptor and the service
  registered. It **panics if `auth` is nil** — the service can never be exposed
  ungated. Existing `RegisterWorkflowServiceServer` is unchanged (the flexible path
  for consumers who build their own server with TLS/extra interceptors).
- **A runnable example** (`ExampleNewSecureServer`) shows the auth interceptor
  authenticating every RPC and placing the trusted principal in the context, with
  guidance to **derive the actor from the authenticated identity, never from a
  client-supplied field.**
- **Request validation:** `StartInstance` now returns `codes.InvalidArgument` for a
  missing `def_ref`/`instance_id` at the transport boundary, mirroring REST, instead
  of falling through to a less clear `NotFound`.

## Consequences

- Consumers get a one-call safe default that cannot be misconfigured into an
  unauthenticated control plane; the panic surfaces the mistake at startup, not in
  production.
- Non-breaking and additive: `RegisterWorkflowServiceServer` and all existing
  options/handlers are untouched; the only behavioural change is the new
  `InvalidArgument` for empty StartInstance fields (previously `NotFound`).
- The auth/authz logic itself remains the consumer's responsibility (the interceptor
  body); the library supplies the fail-closed wiring and the recommended pattern.
- **Deferred:** request validation on the other mutating RPCs (DeliverSignal,
  CompleteTask, …) and a built-in per-method authorization interceptor; structured
  gRPC error details to match REST's `{error,message}` body.
- Engine/model/service diff is **ZERO**; the change lives in `transport/grpc`.
