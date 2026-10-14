# Proposal

## Why

The one authorization wrkflw owns, a human-task operation against the task's eligibility spec, runs in `runtime/task.TaskService`, one layer above the transition. A caller that hands a human trigger it built itself to the exported `ProcessDriver.ApplyTrigger` skips it entirely. The driver already receives an authorizer through `runtime.WithHumanTasks`, and nothing reads it. kartaladev/wrkflw#21 (decided) moved this authorization to the transition, and kartaladev/wrkflw#106 is the ticket that builds it.

Re-measured on `main` at `9c6dd2c1` (go1.26.9):

| Premise | Measurement on main |
|---|---|
| #106: `driver.authz` is written once and read nowhere (`runtime/processdriver_options.go:97`) | **Holds.** The field is declared at `runtime/processdriver.go:41` and written at `processdriver_options.go:97`. Nothing reads it. |
| #106 / #21: `Authorize` is called only by `TaskService`, in Claim, Reassign, Complete and RefreshCandidates | **Holds.** One call site, the `authorize` helper (`runtime/task/service.go:325-333`), reached from `:199`, `:234`, `:255` and `:306`. |
| #106 item 6: the "MUST originate from TaskService" paragraph is at `runtime/processdriver.go:545-550` | **Holds** at `:545-550`. `WithHumanTasks`'s doc still says the authorizer is "used by TaskService, not by the engine core" (`processdriver_options.go:91-92`). |
| #106 item 3: `HumanCandidatesResolved` is the only human trigger with no actor, and `TaskService.RefreshCandidates` is its only non-test producer | **Holds.** Candidates resolved when a task is created are written straight onto the snapshot before commit (`runtime/processdriver_action.go:262-289`), and no trigger is involved. The journal codec carries no actor for it (`internal/persistence/store/trigger_codec.go:120,175`). |
| #106 item 5: the `wrkflw_human_tasks_total{event}` counter is incremented in `TaskService` | **Holds** (`service.go:184,202,237,258,313`). It counts triggers produced, including ones that are never delivered. |
| #106 header: "Blocked by #115: the port becomes `Authorize(ctx, authz.Request)`" | **Stale.** The port landed with 115a (PR #174). `authz.Request{Operation, Spec, Actor, Vars, Task}` and `authz.TaskView` are on main (`authz/authz.go:117-133`). Nothing this change needs waits on #115's remaining parts. |
| #106 item 7: "check all eleven `ApplyTrigger` callers under `examples/`" | **Stale, as the #106 comment says.** There are 19 call sites (21 mentions, two of them in comments). None hand-builds a human trigger: `grep engine.NewHuman examples` returns nothing. Every example that wires human tasks passes a non-nil authorizer. |
| New: 22 test call sites build a driver with `runtime.WithHumanTasks(resolver, tasks, nil)` | **Measured.** Each one that delivers a human trigger turns red under the nil-authorizer refusal and must pass `authz.AllowAll{}`. |
| New: the facade has two authorizers | **Measured.** `service.WithHumanTasks(store, az)` reaches only `task.NewTaskService` (`service/service.go:217`). The driver the facade builds itself arms no human tasks (`:224-242`), so a consumer that runs user tasks supplies a driver through `service.WithProcessDriver`, with its own `runtime.WithHumanTasks(..., az)`. Once this change lands, the driver's authorizer is the one that decides. See design Q1. |

## What Changes

The design is decided by kartaladev/wrkflw#21 (decided), calls 2, 3, 4, 8 and 9. kartaladev/wrkflw#114 (decided) fixes the request shape. These are applied as decided and not reopened.

- **The driver authorizes human triggers at the transition.** `ProcessDriver.ApplyTrigger` authorizes `HumanClaimed`, `HumanCompleted`, `HumanReassigned` and `HumanCandidatesResolved` before the engine steps. It uses the configured `authz.Authorizer`, with an `authz.Request` built from the task in the loaded instance snapshot (eligibility, variable snapshot, claim state). A refusal returns `authz.ErrNotAuthorized` (or the authorizer's error), and nothing is written. Non-human triggers, and a trigger naming a task the snapshot does not hold, pass through to the engine unchanged. The CAS-guarded commit means a decision made on a stale snapshot cannot be committed.
- **BREAKING. A driver with no authorizer refuses human triggers** with a new `runtime.ErrNoAuthorizer`. Its message names `authz.AllowAll{}` as the explicit opt-out. There is no allow-all default, no bypass and no system actor.
- **BREAKING. `engine.HumanCandidatesResolved` gains `By authz.Actor`**, and `engine.NewHumanCandidatesResolved` gains a `by` parameter. A refresh is then authorized like the other three operations.
- **BREAKING. `TaskService.Claim/Complete/Reassign/RefreshCandidates` no longer authorize.** They only produce triggers, so an ineligible actor receives a trigger, and the refusal arrives when that trigger is applied. The checks that are not authorization stay where they are: Reassign's empty-target and `from == current claimant` checks, and RefreshCandidates' `ErrNoActorResolver` and open-task checks.
- **The `wrkflw_human_tasks_total` counter moves into the driver.** It counts admitted transitions, not requests. A denied trigger, and a trigger that is produced but never delivered, no longer count.
- **Docs.** The "MUST originate from TaskService" paragraph on `ApplyTrigger` is replaced by one sentence saying the driver authorizes human triggers. The `WithHumanTasks` doc is corrected. In `doc.go`, the location sentence in `# Trust boundary` and the `# Authorization` `authz` bullet move from `TaskService` to the driver. #43 assigned that move to this ticket.
- **Persisted format: additive journal change.** A `HumanCandidatesResolved` journal row gains a `by` actor. A row written before the change decodes with the zero actor, so no migration is needed. The row is identical on Postgres, MySQL and SQLite.
- **Default security posture: TIGHTENED.** A direct `ApplyTrigger` call can no longer skip authorization. A driver built with a nil authorizer now refuses human triggers it used to admit. A facade whose own authorizer was left at the `AllowAll` default, while its driver holds a real authorizer, now enforces eligibility (design D4).
- **Consumer go.mod:** no impact.

Absorbed issues:
- kartaladev/wrkflw#106: absorbed. Its "blocked by #115" premise is stale, because the port is on main. Item 7's count ("eleven") is stale (19), and the example sweep is moot: no example builds a human trigger by hand. What survives of item 7 is the nil-authorizer test harnesses.

Out of scope:
- The facade's default authorizer (`service/service.go:199-200`) and whether an unconfigured engine enforces eligibility: kartaladev/wrkflw#176, owned by `define-access-and-disclosure` (its Q1). This change moves where the decision is made, and that question has to follow it (design D4).
- `authz.Actor.Validate()` at this seam (`ErrInvalidActor`), the single-role actor and `on_behalf`, claimant-only tasks with implicit claim, `ReassignDecider` / `reassign_eligibility`, kartaladev/wrkflw#115, owned by `replace-casbin-with-composed-deciders`; removing casbin (#115 in part, and #179) is owned by `remove-casbin`. The #115 amendment puts `Validate` "in applyTrigger before authorization (#106's seam)".
- An `OpProgress` operation for in-progress updates: kartaladev/wrkflw#41, owned by `add-user-task-updates`. Its trigger goes through this gate.
- Instance-level and per-definition authorization (start, signal, message, admin): declined for this release by #21 call 1. The additive hook is kartaladev/wrkflw#108, owned by `add-instance-access-hook`.
- Composite field totality and SQL privilege columns: kartaladev/wrkflw#202, #198 and #201, owned by `guard-authz-field-totality`.
- The release-note lines for the breaking items: `cut-v0-release`.

Decisions the specs cannot be final without (design, Open Questions):
- **Q1**: what happens to the authorizer parameter of `task.NewTaskService` and `service.WithHumanTasks`, which no longer decide anything. The specs here do not depend on the answer.
- **Q2**: whether a trigger that passes authorization and is then refused by the engine (task closed, CAS lost) counts on `wrkflw_human_tasks_total`. Only the unambiguous rows are specified.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: replace-casbin-with-composed-deciders, add-user-task-updates, define-access-and-disclosure, `cut-v0-release`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `human-task-authorization`:
  - MODIFIED: "Task operations are refused for an actor the task's eligibility does not admit". The refusal now holds however the operation's trigger reaches the instance, including a trigger built by hand and passed to `ApplyTrigger`, and it happens when the trigger is applied.
  - ADDED: the driver authorizes every actor-carrying human trigger at the transition, against the snapshot's task.
  - ADDED: a driver with no authorizer refuses human triggers with `runtime.ErrNoAuthorizer`.
  - ADDED: a candidate refresh is authorized as the actor who requested it.
  - ADDED: the human-task counter counts only admitted transitions.
- `instance-store`:
  - ADDED: a candidate resolution's requesting actor round-trips through the journal on every dialect, and a pre-change row decodes with the zero actor.

## Impact

- `runtime/processdriver.go` (`applyTrigger`, the `ApplyTrigger` doc), `runtime/processdriver_options.go` (`WithHumanTasks` doc), and a new `runtime.ErrNoAuthorizer` with its HTTP classification (500 `internal_error`).
- `engine/trigger.go`: `HumanCandidatesResolved.By` and its constructor. `internal/persistence/store/trigger_codec.go`: the envelope's `by` for the candidates kind.
- `runtime/task/service.go`: authorization and counter removed, `RefreshCandidates` passes `by` into the trigger. The fate of the `az` parameter is Q1.
- `service/service.go` and `service/options.go`: affected only through Q1 and through `define-access-and-disclosure`.
- `processtest/harness.go`, `internal/transporttest/harness.go`, and the 22 `WithHumanTasks(..., nil)` test sites.
- `doc.go` `# Trust boundary` and `# Authorization`, which are also edited by `fix-doc-rot`, `define-access-and-disclosure` and `replace-casbin-with-composed-deciders`.
