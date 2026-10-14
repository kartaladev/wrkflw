# Design

## Context

See proposal.md (Why) for the measured current state. These are the facts the approach rests on, each re-measured on `main` at `9c6dd2c1`:

- `ProcessDriver.applyTrigger` (`runtime/processdriver.go:569-589`) does `store.Load` and then `deliverLoop`. It is the worker behind the exported `ApplyTrigger` (`:556`) and behind six internal continuations: cancel (`processdriver_cancel.go:39,103`), compensation stall (`processdriver_compensation_stall.go:36`), message correlate (`processdriver_message.go:71`), incident resolve (`processdriver_incident.go:24`), reverse (`processdriver_reverse.go:103,110`) and timer fire (`timerops.go:390`, through `applyTriggerRetryingCAS`). None of the internal callers delivers a human trigger.
- The loaded snapshot holds every task as a `*humantask.HumanTask` (`engine/state.go:525`, `TaskByID`), the same type the task store returns. The eligibility, the variable snapshot and the claim that `TaskService.authorize` reads from the store (`runtime/task/service.go:325-345`) are therefore all available from the snapshot the driver has already loaded.
- The task store's copy is a projection, written after commit (`performAwaitHuman`, `runtime/processdriver_action.go`) and possibly served through `persistence.CachingTaskStore`. The snapshot is the copy whose version the commit's CAS guards.
- The engine refuses a human trigger for a task it does not hold with `engine.ErrTokenNotFound` (`engine/step_triggers.go:629-631,682-684`), and one for a closed task with `engine.ErrTaskNotOpen`.
- The journal envelope already has a `by` key (`internal/persistence/store/trigger_codec.go:71`, `json:"by,omitempty"`), used today only by `HumanReassigned`.
- The facade calls `TaskService` and then `deliverTaskTrigger` (`service/service.go:410-450`). The facade's own authorizer (`c.authz`, defaulting to `authz.AllowAll{}` at `:199-200`) reaches only `task.NewTaskService` (`:217`). The driver's authorizer comes from whoever built the driver.

## Goals / Non-Goals

**Goals:**
- One authorization site for human operations: the transition, in the driver, covering every path a human trigger can take into an instance.
- An authorization decision is made against the same snapshot the commit is guarded on, so it cannot be split from the commit (time-of-check to time-of-use).
- Fail closed when no authorizer is configured, and say how to opt out.

**Non-Goals:**
- Changing what any authorizer decides. Deciders, `ReassignDecider` and claimant-only tasks are `replace-casbin-with-composed-deciders`.
- Validating the actor (`Actor.Validate`, `ErrInvalidActor`). It slots in at this seam, but `replace-casbin-with-composed-deciders` owns it (#115 amendment, #119 Q4).
- Authorizing anything other than the four human triggers (#21 call 1).

## Decisions

### D1. The gate is in `applyTrigger`, after `Load` and before `deliverLoop`, keyed on trigger type

Options considered:
- **(chosen) `applyTrigger`, by trigger type.** Every external human trigger passes through it. The internal callers never carry one, so the type switch is a no-op for them: measured above, all six internal call sites build non-human triggers.
- *`ApplyTrigger` only (the gated public wrapper).* Rejected. It would leave `applyTrigger` as an unguarded path for a future internal caller. Placing the check at the choke point is #21 call 2's own reasoning ("same reasoning as #78").
- *Inside `engine.Step`.* Rejected. An `Authorizer` may do I/O (a directory lookup, a context deadline), and the engine core performs none. Its only I/O-shaped inputs come back as triggers (`engine/trigger.go:313-317`).
- *Each step of `deliverLoop`.* Rejected. Follow-up triggers are produced by the system. Authorizing them would need a system actor, and #21 call 4 forbids one.
- *Leave it in `TaskService` (status quo).* Rejected. That is the back door #106 exists to close.

A trigger that names a task the snapshot does not hold skips authorization, so the engine reports `ErrTokenNotFound` exactly as it does today (#106 item 1). A closed task is authorized first. An ineligible actor therefore learns `ErrNotAuthorized` and nothing about the task's lifecycle. This matches today's order, where `TaskService` authorizes before the engine runs.

### D2. The request is built from the snapshot, not from the task store

The `humantask.HumanTask` → `authz.TaskView` projection (`runtime/task/service.go:339-344`) moves into `runtime`, and the driver applies it to `st.TaskByID(id)`. `authz` must not import `humantask` (`authz/purity_test.go`), so the projection stays on the runtime side.

Rejected: reading the task store in the driver. That costs an extra round trip per human trigger, it reads a copy that a caching store can serve stale, and it re-opens the window between check and commit that the snapshot closes. The CAS on `Commit` means a decision made against version N either commits at N or fails with `kernel.ErrConcurrentUpdate`. Specified as "A decision made on a stale snapshot cannot commit".

### D3. A nil authorizer is refused per trigger with `runtime.ErrNoAuthorizer`, not at construction

#21 call 3 decides the behavior: fail closed, with a sentinel naming `authz.AllowAll{}`. The sentinel name is #106's own proposal ("`ErrNoAuthorizer` or similar"), and it lives in `runtime` beside `ErrDriverShuttingDown`.

Refusing at construction was rejected on a measurement. The facade's own default driver (`service/service.go:224-242`) and 22 test call sites build drivers that never see a human trigger. A construction-time refusal would break all of them for no protection, because only a human trigger needs an authorizer.

The error does not wrap `authz.ErrNotAuthorized`. It is a configuration error, not a denial, the same disposition as `authz.ErrNoDeciders`. The HTTP classifier's default arm turns it into 500 `internal_error` and drops the message (`transport/http/httpcore/errors.go:181-182`), so no new classifier row is needed. A test pins that the response does not contain "AllowAll".

### D4. The facade: the driver's authorizer decides, and two configurations change behavior

Before this change the facade's authorizer decides. After it, the driver's does:

| facade `c.authz` | driver authorizer | Today | After |
|---|---|---|---|
| X | X (every example: `examples/*_wiring`, `authenticated_tasks`) | X decides | X decides, no change |
| `AllowAll` (default) | X | permit-all | **X decides (tightened)** |
| X | nil | X decides | **`ErrNoAuthorizer` (fail closed, loud)** |
| `AllowAll` (default) | nil | permit-all | **`ErrNoAuthorizer`** |

This is a direct consequence of #21 calls 2 and 3. It is not a new decision. It does move the ground under `define-access-and-disclosure` Q1 (#176). That question recommends changing the facade's default `c.authz`, which after this change feeds only `TaskService` and decides nothing. **Coupling, owned by `define-access-and-disclosure`:** its Q1 must be re-read against the driver once this lands. That is why this change lists it under Blocks.

### D5. `HumanCandidatesResolved.By` reuses the envelope's existing `by` key

`engine.NewHumanCandidatesResolved(at, taskID, candidates)` becomes `(at, taskID, candidates, by)`. That is a compile break, which is cheap on v0.x. The codec writes `env.By` for the candidates kind and reads it back. A row written before the change has no `by` key and decodes to the zero actor. There is no production data before the first tag (#21 call 8), so there is no migration. Rejected: a new JSON key. `by` already means "the actor performing a human operation" in `HumanReassigned`.

`By` is persisted as the actor arrives, the same as `HumanCompleted.Actor` today. **Coupling, owned by `replace-casbin-with-composed-deciders`:** #114 item 5 strips privileges from every persisted actor copy. Its list (claim, completion, candidates) must gain this field.

### D6. `TaskService` keeps its store read and loses only the `Authorize` call and the counter

Claim, Complete and Reassign still `store.Get` the task. As a result an unknown task still fails with `humantask.ErrTaskNotFound` from the service, with the HTTP status it has today, and Reassign's `from == current claimant` check keeps its input (#106 item 4). `RefreshCandidates` keeps `ErrNoActorResolver` and the open-task check, and puts `by` on the trigger.

Rejected: dropping the read from Claim and Complete. That would change their unknown-task error from `humantask.ErrTaskNotFound` to the engine's `ErrTokenNotFound` and move an HTTP status, which is not this change's to move.

### D7. The counter increments in the driver, after the human trigger's step commits

It increments once, with the same `event` labels as today, at the point where `deliverLoop` has committed the step for the externally delivered human trigger. The spec fixes only the unambiguous rows. Whether a trigger that passed authorization but was refused by the engine counts is Q2.

### Landing order and shared surfaces

- `runtime/processdriver.go` `applyTrigger`: this change creates the seam. `replace-casbin-with-composed-deciders` adds `Actor.Validate()` there, before the authorization call. Land this first.
- `authz.Operation`: `add-user-task-updates` (#41) adds `OpProgress` and its trigger, which must take the D1 gate. Land this first.
- `doc.go` `# Trust boundary` / `# Authorization`: also edited by `define-access-and-disclosure`, `replace-casbin-with-composed-deciders` and `fix-doc-rot`. This change owns only the location sentence and the `authz` bullet (#43 assigned that move to #106).
- `internal/persistence/store/trigger_codec.go`: `refuse-unrecognized-wire-values` and `encode-persisted-enums-by-name` also touch this file. The edit here is one case arm, and it merges trivially.

## Risks / Trade-offs

- [Refresh now resolves candidates before authorization] `TaskService.RefreshCandidates` calls the `ActorResolver` and only then returns the trigger. An ineligible but authenticated actor can therefore cause a directory lookup that is refused afterwards. → The lookup is bounded by the candidate-resolve timeout (default 10s), and the route needs an identity. Keeping a pre-check in `TaskService` would bring back the second authorization site that #21 call 2 removes. Accepted, and stated in the `RefreshCandidates` godoc.
- [A hand-built `HumanReassigned` with a false `From`] The `from == claimant` consistency check stays in `TaskService` (#106 item 4), so a direct `ApplyTrigger` can still record a false `From` in the journal. It is still authorized as `By`, so this is an audit-accuracy gap, not an authorization bypass. → Recorded here as a finding. No sibling change owns it, and it is not filed.
- [Test churn] Each of the 22 `WithHumanTasks(..., nil)` sites that delivers a human trigger turns red. → They pass `authz.AllowAll{}` explicitly (#21 call 4: "harnesses pass AllowAll"). Sites that never deliver one are left alone, so the nil path stays covered.
- [Consumers calling `TaskService` directly] An ineligible actor now gets a trigger back and the refusal comes from `ApplyTrigger`. A consumer that treated a nil error from `TaskService.Claim` as "authorized" is wrong after this change. → BREAKING, release-noted through `cut-v0-release`.

## Migration Plan

There is no data migration: the journal change is additive and old rows decode (D5). Consumer steps, for the release notes:
1. Pass a non-nil authorizer to `runtime.WithHumanTasks`. Pass `authz.AllowAll{}` to keep permit-all on purpose.
2. Add `by` to every `engine.NewHumanCandidatesResolved` call.
3. Do not rely on `TaskService` errors for authorization. Rely on `ApplyTrigger`'s.

Rollback is a plain revert. Rows written with `by` decode under the old codec, which ignores the key.

## Open Questions

**Q1. The authorizer parameter on `task.NewTaskService` and `service.WithHumanTasks`.** After this change `TaskService` authorizes nothing, so the parameter decides nothing. It is still required to be non-nil (`runtime/task/service.go:163-165`).
- (a) **Recommended.** Remove it from `task.NewTaskService`, and from `service.WithHumanTasks` (which keeps the task store), so the facade's `AllowAll` default goes with it. This is a compile break, cheap on v0.x. It leaves the driver as the only place an authorizer is configured, which makes D4's table impossible to misconfigure, and it hands `define-access-and-disclosure` Q1 a single target.
- (b) Keep it, deprecated and ignored. Rejected by the project rule "silence is never correct": a parameter that is accepted and silently has no effect is exactly that.
- (c) Keep it, and have the facade pass its authorizer into the driver it builds, refusing a mismatch with a consumer-supplied driver. This needs the facade to arm human tasks on its default driver, which it does not do today (`service/options.go:163-168`). That is scope belonging to `define-access-and-disclosure`.

No spec in this change depends on the answer. Tasks 5.2 and 5.3 change with it.

**Q2. Does `wrkflw_human_tasks_total` count a trigger that passed authorization but was not committed** (the engine refuses it with `ErrTaskNotOpen`, or the commit loses CAS)? #106 item 5 says "after authorization passes"; #21 call 9 says "counts admitted transitions".
- (a) **Recommended.** Count on commit only. No transition happened, and a CAS loser that the caller retries would otherwise count twice.
- (b) Count when authorization passes, which is item 5 read literally.

The spec fixes only the rows both readings agree on.
