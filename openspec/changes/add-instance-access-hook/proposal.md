# Proposal

## Why

Nothing authorizes an instance operation, so any identified caller reads any instance, and any caller starts, signals or cancels one. kartaladev/wrkflw#21 call 1 decided there is no owner or tenant field on the instance, and that per-instance control goes through an additive hook. kartaladev/wrkflw#108 sketched that hook, and kartaladev/wrkflw#129 showed that identified reads are unscoped today. This change specifies the hook as the recommended option of each open question, marked pending, so the maintainer rules once.

This change was split from `define-access-and-disclosure`, which keeps the posture gaps #176, #199, #127 and #128. Open-question numbers are kept from the pre-split plan, so this change holds Q6 and Q7.

Re-measured on `main` at `9c6dd2c1` (go1.26.9). The probes ran from the scratchpad through `go test -overlay`, so nothing was written to the repository.

| Premise | Measurement on main |
|---|---|
| #129: the read routes call `svc.GetInstance(ctx, id)` with no actor, and `identified()` lifts redaction for any non-empty `Actor.ID` | **Holds** (`transport/http/httpcore/endpoints.go:48,76,103`; `disclosure.go:34-40`). |
| #129 comment: the demo resolver comment does not say it also lifts redaction | **Holds** in `examples/production_wiring/main.go:274`, `sqlite_wiring:287` and `mysql_wiring:271`. |
| #108: "the actor is read from the context the transport already populates via `authz.ContextWithActor`" | **Stale.** Nothing in `transport/http` calls `ContextWithActor` (`httpcore/disclosure.go:13-14`). A consumer's middleware does, and only on the default resolver seam. The hook needs the transport to put the actor it resolved into the context. |
| #108: `ResolveCompensationStall` doc needs "a privilege distinct from resolve-incident" (`service/service.go:94-96`) | **Holds** (`:95-96`). |

## What Changes

Each item below is the recommended option of an open question in design, and each is **pending** until the maintainer rules. One recorded decision is applied as decided, not reopened: kartaladev/wrkflw#21 call 1 (no owner or tenant field; per-instance control goes through an additive hook).

- **New capability `instance-access-control` (pending Q6, Q7).** It adds an additive `service.WithOperationAuthorizer` hook, consulted by the `ProcessEngine` before start, read, list, signal, message, cancel, resolve-incident and resolve-stall. A refusal changes nothing and maps to 403 on all three adapters. A read refusal renders no state. When the option is absent nothing is consulted, which keeps the unauthenticated-write posture of #21. An explicit nil is refused at construction.
- **Disclosure boundary (MODIFIED).** The disclosure decision still never refuses. A read is refused only by a configured operation authorizer.
- **Transport.** Each hooked route puts the actor its configured resolver returned into the request context, reusing the single resolution per request.
- **Docs.** `doc.go` `# Trust boundary` replaces "There is no per-instance access control" with how to add the hook. The demo resolver comment in three examples says that it also lifts redaction.
- Default security posture: **unchanged.** The hook is absent by default and nothing is consulted. Not BREAKING: the option and the types are additive.
- Persisted formats: **no change.**
- Consumer go.mod: no impact. No new dependency.

Absorbed issues:
- kartaladev/wrkflw#108: absorbed. Its "transport already populates the context" premise was found stale. It was filed as post-release, and Q7 asks whether it lands before the tag.
- kartaladev/wrkflw#129: absorbed. Reading 2 (owner field) is superseded by #21 call 1. The read gap is closed through the hook, reading 3. Its comment item 3 (two identity predicates) is folded in as Q7 and needs no spec, because `doc.go` already documents the split.

Decisions the specs cannot be final without (design, Open Questions). No maintainer ruling is recorded on these threads beyond #21's call 1.
- **Q6** (#108) the hook's default, nil, zero-actor and operation vocabulary.
- **Q7** (#129, #108) whether the hook lands before the tag, and whether a refused read answers 403 or 404.

Out of scope:
- An owner or tenant field on the instance: declined by #21 call 1.
- Authorizing engine-originated work (call-activity children, chained successors, timers).
- Human-task authorization and its default: kartaladev/wrkflw#106 and #176, owned by `authorize-task-triggers-in-driver` and `define-access-and-disclosure`.
- The privilege strip and the cache directive on the same routes: kartaladev/wrkflw#199 and #127, owned by `define-access-and-disclosure`.
- Composite decider shape and `authz.Operation`'s reshape: kartaladev/wrkflw#115, owned by `replace-casbin-with-composed-deciders` (casbin removal and #179: `remove-casbin`) (naming coupling in design D4).
- Route registration as data: kartaladev/wrkflw#40 and #205, owned by `unify-http-routes-as-data`.

**Coordination**
- Size: L
- Depends on: `establish-baseline-specs`
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

- `instance-access-control`: who may perform an operation on an instance, through an additive operation authorizer the `ProcessEngine` consults. Pending Q6 and Q7.

### Modified Capabilities

- `instance-disclosure`:
  - MODIFIED: "A caller the resolver does not identify reads the projection" now names the operation authorizer as the only thing that refuses a read (#129).

## Impact

- `service/service.go`, `service/options.go`: `WithOperationAuthorizer`, and the hook call at each `ProcessEngine` instance method.
- `authz`: the `Operation`, `OperationTarget` and `OperationAuthorizer` public types, which the hook introduces (Q6). They share the package with `replace-casbin-with-composed-deciders` and `guard-authz-field-totality`.
- `transport/http/{stdlib,gin,fiber}/groups.go`, `transport/http/httpcore`: actor-to-context on hooked routes and the 403 mapping. `groups.go` is shared with `unify-http-routes-as-data` and `define-access-and-disclosure`.
- `doc.go` and three `examples/*_wiring/main.go` comments. `doc.go` is shared with `fix-doc-rot` and `define-access-and-disclosure`.
