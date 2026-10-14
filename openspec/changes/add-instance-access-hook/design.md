# Design

## Context

See proposal.md (Why) for the motivation and the re-measurement table. Open-question numbers are kept from the pre-split plan of `define-access-and-disclosure`, so this change holds Q6 and Q7, and its one decision keeps its number, D4. The constraints that shape this design are:

- **Recorded decision, applied and not reopened.** kartaladev/wrkflw#21 call 1: authentication is the consumer's; there is no owner or tenant field; per-instance control is an additive hook (#108). #43: the posture is stated in the `# Trust boundary` section of `doc.go`.
- **The transport resolves identity but never places it in the context.** `httpcore/disclosure.go:13-14` says so. The service layer can read an actor from the context only if the route puts it there.

## Goals / Non-Goals

**Goals:**
- Give a consumer one place to refuse any instance operation, on the in-process API and on every HTTP adapter alike.
- Keep stdlib, gin and fiber in parity for every new behavior.

**Non-Goals:**
- No owner, tenant or ACL data on the instance (#21 call 1).
- No authorization of engine-originated work: call-activity children, chained successors, timers.
- No change to `RequestActorFunc` semantics ("who, never may"), and no change to the two identity predicates (Q7).

## Decisions

### D4. The operation authorizer is consulted by the ProcessEngine, with the actor from the context (pending Q6)

This follows #108's sketch, corrected by measurement. The hook is `authz.OperationAuthorizer` with `AuthorizeOperation(ctx, op, actor, target) error`. One `Operation` name exists per `ProcessEngine` instance method, and #108's list gains `instance.list` for `ListInstances`. `OperationTarget` carries `DefRef` or `InstanceID`. The engine reads the actor with `authz.ActorFromContext`, which yields the zero actor when absent.

The #108 premise "the transport already populates the context" is **stale**: nothing in `transport/http` calls `authz.ContextWithActor`. So each hooked route stores the actor its configured resolver returned in the request context before calling the service. It uses the same once-per-request resolution that `instance-disclosure` already requires, so the resolver is not called twice.

The hook runs before any load or effect. A read refusal therefore returns before any state exists to render.

- Rejected: **thread an actor through the request structs.** #21 call 1 rejected it.
- Rejected: **gate in the transport.** `ProcessEngine` callers outside HTTP would bypass it.
- Rejected: **gate in the driver or engine.** #108 keeps the engine authorization-unaware, and engine-originated starts must not be authorized.

**Coupling:** the new public types live in `authz`. They are shared with `replace-casbin-with-composed-deciders`, which reshapes `authz.Request` and `authz.Operation`. The existing `authz.Operation` names human-task operations, so the hook's type needs a distinct name (working name `authz.InstanceOperation`) unless #115 has merged the two first. The route wrappers are shared with `define-access-and-disclosure` (cache directive, D3 there) and `unify-http-routes-as-data`; either order works, and a 403 written here carries that change's directive once both land.

## Risks / Trade-offs

- [The hook adds a call to every instance operation.] → It is absent by default (zero cost), and when configured it is one interface call. The tests pin its call count at exactly one.
- [A refused read answering 403 tells the caller the ID exists.] → This is Q7. The consumer can return `kernel.ErrInstanceNotFound` from the hook to get 404.

## Migration Plan

There is no persisted-format migration. Upgraders:
1. A consumer that wants instance-level control adds `service.WithOperationAuthorizer`.
2. Rollback is a code revert. No stored row changes shape.

## Open Questions

No maintainer ruling is recorded on these threads beyond #21 call 1. Each question changes a pending-marked requirement, so `Readiness: human`, and apply waits for the rulings.

- **Q6 (#108): the hook contract.**
  - Default when the option is absent: allow, as recommended, which keeps #21's unauthenticated write; or refuse.
  - An explicit nil: refuse at construction with `service.ErrNilDependency`, as recommended; or ignore, like other options.
  - The zero actor: pass it through so the hook decides, as recommended; or refuse before the hook.
  - Operation vocabulary: one name per `ProcessEngine` method plus `instance.list`, as recommended. Does `ResolveCompensationStall`'s abandon need its own name (`service/service.go:95-96`)? Recommend that the target carries the requested resolution, so a consumer can tell abandon from retry or skip without a third name.
- **Q7 (#129, #108): timing and read refusal shape.**
  - (a) Land the hook before the v0.x tag. #108 said post-release, but #129 shows identified reads are unscoped.
  - (b) Keep it post-release. The pre-split plan's "split it out" half of this option is now done: this change exists.
  - No recommendation was recorded on timing. Coordination says `Blocks: nothing`, which is (b); a ruling of (a) adds `cut-v0-release` under Blocks.
  - Separately: a refused read answers 403 (`authz.ErrNotAuthorized`, recommended, consistent with task routes), and a hook may return `kernel.ErrInstanceNotFound` to answer 404.
  - #129's comment item 3 asks whether the two identity predicates (task guard: not wholly zero; disclosure: non-empty ID) stay separate. Recommend keeping them, as `doc.go` documents. The hook receives the actor and applies neither predicate.
