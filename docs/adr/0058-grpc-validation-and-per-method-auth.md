# 58. gRPC request-validation sweep + per-method authorization example

- Status: Accepted
- Date: 2026-06-25

## Context

[ADR-0051](0051-grpc-fail-closed-server.md) added transport-boundary request
validation to `StartInstance` (returning `codes.InvalidArgument` for empty
`def_ref`/`instance_id`) and a fail-closed `NewSecureServer`. It explicitly
**deferred** two follow-ups:

1. Request validation on the **other** mutating RPCs. Without it, a request with
   a required field empty fell through to a deeper, less clear status (e.g.
   `DeliverSignal` with an empty signal returned `Internal` instead of
   `InvalidArgument`), diverging from the REST transport — which rejects empty
   `def_ref`/`instance_id`/`signal`/`name` with `400 bad_request` at the handler
   boundary.
2. A built-in or sample **per-method authorization interceptor**. ADR-0051's
   `ExampleNewSecureServer` showed authenticating every RPC uniformly, but not
   how to authorize differently per method (admin RPCs vs. task RPCs) or how a
   handler should derive the actor from the authenticated principal rather than a
   client-supplied field.

This ADR closes both.

## Decision

### Request-validation sweep (handler-side, no proto change)

A shared `invalidArg(span, msg)` helper builds a `codes.InvalidArgument` status,
records it on the OTel span, and returns it. Each mutating RPC now guards its
genuinely-required-empty fields *before* delegating to the service. Optional
fields (payloads, outputs, attributes, `add_attempts`, correlation key) are left
unvalidated to avoid over-rejecting. The validations added:

| RPC | Fields validated (rejected when empty) |
|---|---|
| `DeliverSignal` | `instance_id`, `signal` |
| `DeliverMessage` | `def_ref`, `name` |
| `ClaimTask` | `task_token`, `actor.id` |
| `CompleteTask` | `task_token`, `actor.id` |
| `ReassignTask` | `task_token`, `from`, `to`, `by.id` |
| `CancelInstance` | `instance_id` |
| `ResolveIncident` | `instance_id`, `incident_id` |
| `AddPolicy` / `RemovePolicy` | `rule.subject`, `rule.object`, `rule.action` (nil rule rejected) |
| `AddRole` / `RemoveRole` | `binding.user`, `binding.role` (nil binding rejected) |
| `RedriveDeadLetters` | non-empty `ids` |

The actor-identity guards (`actor.id`/`by.id`) reflect that these RPCs act on
behalf of a principal: an empty actor cannot identify who is acting. For the
DLQ/policy-admin RPCs the existing `codes.Unimplemented` "admin not configured"
check runs first (a configuration concern), then the request validation.
`StartInstance` was refactored to use the same `invalidArg` helper (behaviour
unchanged).

### Per-method authorization example

`per_method_auth_example_test.go` adds `Example_perMethodAuth`, a runnable,
compiling documentation example of the recommended pattern. A single
`grpc.UnaryServerInterceptor` authorizes by `grpc.UnaryServerInfo.FullMethod`:

- admin-scoped methods (`ListInstances`, the DLQ and policy-admin RPCs) require
  an `admin` claim, else `codes.PermissionDenied`;
- task methods (`ClaimTask`, `CompleteTask`, `ReassignTask`) stash the
  authenticated principal in the context so the handler derives the actor from
  identity, **never** from the client-supplied actor field;
- all other methods require only authentication.

It is mounted via `NewSecureServer`, reinforcing the fail-closed default. The
example has a deterministic `// Output:` block asserting the per-method routing
decision, so it is verified, not merely compiled.

## Consequences

- Every mutating gRPC RPC now returns `codes.InvalidArgument` for required-empty
  fields at the transport boundary, consistent with REST and with the existing
  `StartInstance` guard. Clients see a clear, machine-checkable code instead of a
  deeper `Internal`/`NotFound`/`PermissionDenied` for malformed input.
- This is a **behavioural change** for the swept RPCs (previously these empty
  fields produced other codes). It is additive validation, not a contract break
  in the proto.
- Consumers get a copy-pasteable per-method authorization pattern and the
  explicit "derive the actor from the authenticated principal" guidance.
- **No proto regeneration**: all validation is handler-side; `workflowpb` is
  untouched.
- Engine/model/service diff is **ZERO**; the change lives entirely in
  `transport/grpc` (plus this ADR).
- **Deferred (still open):** structured gRPC error details on the new
  `InvalidArgument` statuses to match REST's `{error,message}` body shape (the
  validation statuses currently carry only a human-readable message, not an
  `errdetails.ErrorInfo`); and a *built-in* (non-example) per-method authorization
  interceptor shipped from the package — this ADR provides the recommended
  pattern as documentation, leaving the policy itself the consumer's choice.
