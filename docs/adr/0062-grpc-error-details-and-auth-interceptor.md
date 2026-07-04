# 62. gRPC structured InvalidArgument details + reusable per-method auth interceptor

- Status: Accepted
- Date: 2026-06-25

> **Superseded by ADR-0094.** The gRPC transport is removed;
> `NewMethodAuthInterceptor` and the structured `InvalidArgument` details path
> are gone with it. The HTTP equivalent is ADR-0095's `ClassifyError` +
> `httpcore.Validate`, which attaches stable error codes for all three HTTP
> adapters.

## Context

[ADR-0058](0058-grpc-validation-and-per-method-auth.md) added a transport-boundary
request-validation sweep and a per-method authorization *example*, but explicitly
left two follow-ups open:

1. **Structured details on `InvalidArgument` statuses.** The validation sweep
   rejects malformed requests via the `invalidArg(span, msg)` helper, which built
   a bare `status.Error(codes.InvalidArgument, msg)`. Unlike the
   classified-error path ([ADR-0055](0055-maturity-hardening.md)'s
   `mapToGRPCStatus`, which attaches an `errdetails.ErrorInfo` carrying a
   machine-readable `Reason` and `Domain`), these statuses carried only a
   human-readable message. A client could not branch on a stable reason code for
   bad input without parsing prose, diverging from the REST `{error,message}`
   taxonomy.
2. **A reusable per-method auth interceptor.** ADR-0058 shipped only
   `Example_perMethodAuth` — a runnable documentation example. The *policy* is
   correctly the consumer's choice, but the mechanical glue (call an authorize
   callback keyed on `info.FullMethod`, reject before the handler, else proceed)
   was left for every consumer to re-derive.

This ADR closes both. No proto change is involved: both changes live entirely in
the `transport/grpc` adapter, so `workflowpb` is untouched and there is no
regeneration.

## Decision

### Structured `InvalidArgument` details

The detail-stamping logic that `mapToGRPCStatus` used is extracted into a shared
`statusWithReason(code, reason, msg)` helper that builds a `*status.Status` and
attaches an `errdetails.ErrorInfo{Reason, Domain: errorDomain}` (degrading to the
bare status only if `WithDetails` fails, exactly as before). `mapToGRPCStatus`
now delegates to it.

`invalidArg` is changed to call `statusWithReason(codes.InvalidArgument,
reasonInvalidArgument, msg)`, where `reasonInvalidArgument = "invalid_argument"`.
Because every swept RPC already routes its required-field guards through
`invalidArg`, all `InvalidArgument` validation statuses now carry an identical
`ErrorInfo` shape to the classified-error path — same `Domain`, a stable
`Reason` — so clients branch on the structured code rather than the message
string. The detail shape is produced in exactly one place (`statusWithReason`),
guaranteeing both paths stay identical.

### Reusable per-method auth interceptor

A new exported constructor ships from the package (a real source file, not a
test):

```go
func NewMethodAuthInterceptor(
    authorize func(ctx context.Context, fullMethod string) error,
) grpc.UnaryServerInterceptor
```

It is a thin, unopinionated `grpc.UnaryServerInterceptor`: for every request it
calls `authorize(ctx, info.FullMethod)`; a non-nil result rejects the RPC with
that error (the handler is never reached), otherwise the RPC proceeds. It ships
no policy of its own — the consumer supplies `authorize`, deciding per
`fullMethod` (e.g. `workflowpb.WorkflowService_ListInstances_FullMethodName`).

It composes with `NewSecureServer`: passing
`NewSecureServer(svc, NewMethodAuthInterceptor(policy))` installs a fail-closed,
per-method gate in front of every RPC. The documentation is explicit that the
consumer derives the actor from the authenticated principal in context — never
from the request body — consistent with ADR-0051/0058 guidance; this interceptor
only gates, leaving authentication and principal-stashing to the consumer's
policy (or a wrapping interceptor chained via `grpc.ChainUnaryInterceptor`).

## Consequences

- Every `InvalidArgument` returned by the validation sweep now carries an
  `errdetails.ErrorInfo` with `Reason: "invalid_argument"` and the engine
  `Domain`, matching the classified-error path and the REST taxonomy intent.
  This is additive — the status code and message are unchanged; clients that
  ignored details are unaffected, clients that read them gain a stable reason.
- Consumers no longer hand-roll the per-method gate boilerplate: they supply a
  policy function and wire `NewMethodAuthInterceptor` into `NewSecureServer`. The
  `Example_perMethodAuth` example remains as the canonical policy illustration.
- **No proto regeneration**: both changes are handler/adapter-side; `workflowpb`
  is untouched.
- Engine/model/service diff is **ZERO**; the change lives entirely in
  `transport/grpc` (plus this ADR).
- Closes the two follow-ups deferred by ADR-0058.
