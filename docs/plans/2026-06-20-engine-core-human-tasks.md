# Engine Core — Human Tasks (Plan 4 of 8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.
>
> **Handover note:** This plan targets the design spec (`docs/specs/2026-06-20-engine-core-design.md`, §4/§5/§8) and assumes Plans 1–3 are merged. The Trigger/Command and port *contracts* are fixed by the spec; the exact `engine/step.go` edits must be grounded against the then-current code (read it first). The SDD review loop is the safety net for any drift — and Plan 1 already proved the per-task red-state check catches plan-code bugs.

**Goal:** Add the User (human) task: on entry the engine emits `AwaitHuman` (carrying the eligibility spec); a pluggable `ActorResolver` produces candidates; a queryable `TaskStore` bucket exposes "my tasks" / "claimable by me"; claim/reassign/complete flow back through `Step` as triggers carrying the `Actor`, giving one journal-based audit trail. **SLA/timers are out of scope here — they arrive in Plan 5.**

**Architecture:** New pure packages `authz` (Actor/AuthzSpec/Authorizer) and `humantask` (HumanTask/TaskState/ActorResolver/TaskStore). The engine emits `AwaitHuman` data and records an authoritative `HumanTask` in `InstanceState.Tasks`; the runtime resolves candidates and maintains the `TaskStore` projection via `UpdateTask` commands; a `TaskService` authorizes actor actions and injects `HumanClaimed`/`HumanReassigned`/`HumanCompleted` triggers.

**Tech Stack:** Go 1.25, `expr-lang/expr` (via `expreval`, for attribute predicates), `testify`.

## Global Constraints

- Go **1.25**; module `github.com/zakyalvan/krtlwrkflw`; root packages (no `pkg/`).
- Core stays pure: `engine`/`model` import no transport/storage/bus/time-vendor; engine never calls `time.Now()` (time via `Trigger.OccurredAt`). `engine` may import `authz` + `humantask` (both pure data + interfaces) per spec §2.
- Authorization is a **runtime** concern (the `Authorizer` does I/O); the core records already-authorized actions. `Step` stays deterministic + pure; public signature unchanged.
- Black-box tests, `assert`-closure tables, `t.Context()`. Coverage ≥ 85% on touched packages; `-race` green; lint clean. Conventional Commits; commit per green step.

## Prerequisite & current contracts (from the design spec)

- Trigger (sealed, `isTrigger()`+`OccurredAt()`): add `HumanClaimed`, `HumanReassigned`, `HumanCompleted`.
- Command (sealed, `isCommand()`): add `AwaitHuman`, `UpdateTask`.
- `InstanceState` gains `Tasks []humantask.HumanTask`.
- `Token` correlation: a user-task token parks with its `TaskToken` stored in the existing `AwaitCommand` field (it is just a correlation key); `tokenAwaiting(taskToken)` finds it.

---

## File Structure

```
authz/
  authz.go            # Actor, AuthzSpec, Authorizer + AllowAll
  authz_test.go
humantask/
  humantask.go        # HumanTask, TaskState, ActorResolver, TaskStore
  memory.go           # StaticActorResolver, MemTaskStore (AssignedTo/ClaimableBy/Upsert/Get)
  humantask_test.go
  memory_test.go
model/
  definition.go       # MODIFY: Node user-task eligibility fields
engine/
  trigger.go          # MODIFY: HumanClaimed/HumanReassigned/HumanCompleted + constructors
  command.go          # MODIFY: AwaitHuman, UpdateTask
  state.go            # MODIFY: InstanceState.Tasks; helpers for task lookup
  step.go             # MODIFY: UserTask case; HumanClaimed/Reassigned/Completed handlers; task helpers
  step_human_test.go
runtime/
  taskservice.go      # TaskService: Claim/Reassign/Complete (authorize → inject trigger)
  runner.go           # MODIFY: perform AwaitHuman (resolve + create task) and UpdateTask (upsert)
  human_example_test.go
```

---

### Task 1: `authz` package (Actor, AuthzSpec, Authorizer)

**Files:** Create `authz/authz.go`, `authz/authz_test.go`.

**Interfaces (Produces):**
```go
package authz

import "context"

// Actor is a principal that can act on human tasks.
type Actor struct {
	ID         string
	Roles      []string
	Attributes map[string]any
}

// AuthzSpec describes who may act: any-of roles, any-of resource privileges, and
// an optional attribute predicate (expr over {actor, vars}). Empty spec = allow.
type AuthzSpec struct {
	Roles      []string // actor authorized if it has any of these roles
	Privileges []string // reserved for resource-privilege checks
	Attribute  string   // expr predicate over {"actor": Actor, "vars": map} (optional)
}

// Authorizer decides whether an actor satisfies a spec given process variables.
// Implementations may do I/O (e.g. casbin); the engine never calls this.
type Authorizer interface {
	Authorize(ctx context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error
}
```

- [ ] **Step 1 (RED):** Write `authz/authz_test.go` testing an `AllowAll` authorizer returns nil, and a role-based test fake (define a small `RoleAuthorizer` that errors with `ErrNotAuthorized` when the actor lacks every required role; assert both branches via a table with an `assert` closure). Run `go test ./authz/...` → fails (undefined).
- [ ] **Step 2 (GREEN):** Implement `authz.go` with the types above, `ErrNotAuthorized = errors.New("authz: not authorized")`, `AllowAll` (an `Authorizer` whose `Authorize` always returns nil), and `RoleAuthorizer` (authorizes if `len(spec.Roles)==0` or the actor shares a role; else `ErrNotAuthorized`). Attribute-predicate evaluation may be stubbed as "empty Attribute = skip" here; full expr attribute eval is added in this package only if a test needs it (keep it minimal: if `spec.Attribute != ""`, evaluate via `expreval` against `{"actor":..., "vars":...}` and require bool true). Run tests → pass.
- [ ] **Step 3:** Commit `feat(authz): Actor, AuthzSpec, Authorizer with AllowAll and RoleAuthorizer`.

---

### Task 2: `humantask` package (HumanTask, TaskState, ActorResolver, TaskStore + fakes)

**Files:** Create `humantask/humantask.go`, `humantask/memory.go`, `humantask/humantask_test.go`, `humantask/memory_test.go`.

**Interfaces (Produces):**
```go
package humantask

import (
	"context"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
)

type TaskState int
const (
	Unclaimed TaskState = iota
	Claimed
	Completed
	Cancelled
)

type HumanTask struct {
	TaskToken   string
	InstanceID  string
	NodeID      string
	Eligibility authz.AuthzSpec
	Candidates  []string // actor IDs (resolver output; filled by the runtime)
	State       TaskState
	ClaimedBy   string
	CreatedAt   time.Time
	DueAt       *time.Time // set in Plan 5 (SLA); nil here
}

// ActorResolver expands an eligibility spec (+ process vars) into candidate actors.
type ActorResolver interface {
	Candidates(ctx context.Context, spec authz.AuthzSpec, vars map[string]any) ([]authz.Actor, error)
}

// TaskStore is the queryable task bucket, maintained from UpdateTask commands.
type TaskStore interface {
	Upsert(ctx context.Context, t HumanTask) error
	Get(ctx context.Context, taskToken string) (HumanTask, error)
	AssignedTo(ctx context.Context, actorID string) ([]HumanTask, error)   // claimed by / assigned to
	ClaimableBy(ctx context.Context, actor authz.Actor) ([]HumanTask, error) // unclaimed + eligible
}
```

- [ ] **Step 1 (RED):** `memory_test.go` — table tests for `MemTaskStore`: Upsert+Get round-trip; `AssignedTo` returns tasks with `ClaimedBy==actorID`; `ClaimableBy` returns `Unclaimed` tasks where the actor's ID is in `Candidates` (or shares an eligibility role). `StaticActorResolver` (role→actorIDs map) returns the right candidates. Add `ErrTaskNotFound`. Run → fails.
- [ ] **Step 2 (GREEN):** Implement `humantask.go` (types above) and `memory.go`:
  - `MemTaskStore` (map keyed by TaskToken) with the four methods; `Get` returns `ErrTaskNotFound` on miss; deterministic ordering in queries (sort by TaskToken).
  - `StaticActorResolver` built from `map[string][]authz.Actor` (role → actors), returning the union of actors for the spec's roles (deduped, sorted by ID for determinism).
  - Compile-time `var _ TaskStore = (*MemTaskStore)(nil)`, `var _ ActorResolver = (*StaticActorResolver)(nil)`.
  Run → pass.
- [ ] **Step 3:** Commit `feat(humantask): HumanTask, TaskState, ActorResolver, TaskStore + in-memory fakes`.

---

### Task 3: model user-task eligibility fields

**Files:** Modify `model/definition.go` (+ a small `model/definition_test.go` case).

**Produces:** `Node` gains user-task fields (plain data; `model` stays stdlib-only — it does **not** import `authz`):
```go
	// User-task eligibility (KindUserTask). The engine maps these to authz.AuthzSpec.
	CandidateRoles []string
	EligibilityExpr string // optional attribute predicate (expr)
```

- [ ] **Step 1 (RED):** add a `definition_test.go` case constructing a `KindUserTask` node with `CandidateRoles` and asserting the field round-trips via `d.Node("...")`. Run → fails (unknown field).
- [ ] **Step 2 (GREEN):** add the two fields to `Node`. Run → pass.
- [ ] **Step 3:** Commit `feat(model): user-task eligibility fields on Node`.

---

### Task 4: engine triggers, commands, and state for human tasks

**Files:** Modify `engine/trigger.go`, `engine/command.go`, `engine/state.go`.

**Produces:**
- Triggers (with constructors stamping `OccurredAt`): `HumanCompleted{TaskToken string; Output map[string]any; Actor authz.Actor}`, `HumanClaimed{TaskToken string; Actor authz.Actor}`, `HumanReassigned{TaskToken string; From, To string; By authz.Actor}`.
- Commands: `AwaitHuman{TaskToken string; Eligibility authz.AuthzSpec}`, `UpdateTask{Task humantask.HumanTask}`.
- `InstanceState.Tasks []humantask.HumanTask`; helper `(*InstanceState).task(taskToken) *humantask.HumanTask`.

- [ ] **Step 1 (RED):** extend `engine/command_test.go`/`trigger_test.go` to assert the new types satisfy `Command`/`Trigger`, the constructors set `OccurredAt`, and `AwaitHuman.Eligibility`/`UpdateTask.Task` round-trip. Run → fails.
- [ ] **Step 2 (GREEN):** add the types/constructors/`isCommand()`/`isTrigger()` impls; add `Tasks` to `InstanceState` and the `task()` lookup; extend `cloneState` to deep-copy `Tasks` (and each task's `Candidates`/`Eligibility.Roles` slices). Run → pass.
- [ ] **Step 3:** Commit `feat(engine): human-task triggers, commands, and task state`.

---

### Task 5: engine `Step` — UserTask entry + claim/reassign/complete handlers

**Files:** Modify `engine/step.go`; create `engine/step_human_test.go`.

**Behavior:**
- `drive` `KindUserTask` case: generate a deterministic `TaskToken` (`<instanceID>-h<seq>` via a new `nextTaskToken()` counter or reuse `CmdSeq`-style counter — add `TaskSeq`), build `authz.AuthzSpec{Roles: node.CandidateRoles, Attribute: node.EligibilityExpr}`, append a `humantask.HumanTask{TaskToken, InstanceID, NodeID:node.ID, Eligibility, State:Unclaimed, CreatedAt:at}` to `s.Tasks`, emit `AwaitHuman{TaskToken, Eligibility}`, and park the token (`TokenWaitingCommand`, `AwaitCommand=TaskToken`).
- `Step` `HumanClaimed`: find the task by `TaskToken`; set `ClaimedBy`, `State=Claimed`; emit `UpdateTask{task}`. Token does not move. (No drive.)
- `Step` `HumanReassigned`: update `ClaimedBy` (To); `State=Claimed`; emit `UpdateTask`. No token move.
- `Step` `HumanCompleted`: find the parked token via `tokenAwaiting(TaskToken)`; merge `Output` into vars; set the completing `NodeVisit.ActorID` for the user-task node; set task `State=Completed`; advance the token past the user task (`moveAlongSingleFlow`); emit `UpdateTask{task}`; then `drive`.

Key tests (`step_human_test.go`):
- `TestUserTaskEmitsAwaitHumanAndParks` — entering a user task emits `AwaitHuman` with the role spec and creates an `Unclaimed` task; token parked.
- `TestHumanClaimedUpdatesTask` — `HumanClaimed` sets `ClaimedBy`/`Claimed` and emits `UpdateTask`; token unchanged; instance still running.
- `TestHumanCompletedAdvancesAndAudits` — `HumanCompleted` advances to End → `CompleteInstance`; the user-task `NodeVisit.ActorID` equals the actor; task `State=Completed`.
- `TestHumanCompletedUnknownTaskTokenErrors` — `ErrTokenNotFound`.

- [ ] **Step 1 (RED):** write `step_human_test.go` (above). Run `go test ./engine/... -run 'Human|UserTask'` → fails (UserTask handled by `default`; human triggers unknown).
- [ ] **Step 2 (GREEN):** implement the `drive` `KindUserTask` case and the three `Step` trigger handlers + helpers (`nextTaskToken`, set-visit-actor). Reuse `moveAlongSingleFlow`. Run → pass.
- [ ] **Step 3:** Commit `feat(engine): user task entry and claim/reassign/complete handlers with actor audit`.

---

### Task 6: runtime — TaskService + perform AwaitHuman/UpdateTask + e2e

**Files:** Create `runtime/taskservice.go`, `runtime/human_example_test.go`; modify `runtime/runner.go`.

**Behavior:**
- `runner.perform` handles `AwaitHuman` (call `ActorResolver.Candidates(spec, vars)`, build the `humantask.HumanTask` with candidates + `Unclaimed`, `TaskStore.Upsert`; returns no follow-up trigger — the instance parks) and `UpdateTask` (`TaskStore.Upsert(cmd.Task)`; no trigger). `NewRunner` gains `ActorResolver` + `TaskStore` (+ `Authorizer`) dependencies (extend the constructor; update Plan-1 call sites/tests).
- `TaskService{store, authz, clk}` with `Claim(ctx, taskToken, actor)`, `Reassign(ctx, taskToken, from, to, by)`, `Complete(ctx, taskToken, actor, output)` — each loads the task, **authorizes** the actor (`Authorizer.Authorize` against the task's `Eligibility`), and returns the corresponding engine `Trigger` (stamped via `clk.Now()`) for the caller to feed into the engine. (Driving the trigger through `Step` + applying resulting commands is the Runner's job; expose a `Runner.Deliver(ctx, def, instanceID, trg)` that loads state, steps, saves, performs commands — refactor the Plan-1 `Run` loop to reuse a shared `deliver` step.)
- e2e (`human_example_test.go`): start→userTask("approve", role "manager")→end. `Run` parks at the task; `TaskStore.ClaimableBy(manager)` lists it; `TaskService.Claim` then `Complete` → deliver `HumanCompleted` → instance completes; journal shows StartInstance + HumanClaimed + HumanCompleted; the task's final `State==Completed` and `ClaimedBy==manager`.

- [ ] **Step 1 (RED):** write `human_example_test.go`. Run → fails (`undefined: runtime.TaskService`, `NewRunner` arity).
- [ ] **Step 2 (GREEN):** refactor `Runner` to expose `Deliver`; add `perform` cases; implement `TaskService`. Run `go test -race ./...`, coverage, `golangci-lint run ./...` → all green/clean.
- [ ] **Step 3:** Commit `feat(runtime): TaskService claim/complete + human-task perform + e2e`.

---

## Verification Checklist (Plan 4)

- [ ] Entering a user task emits `AwaitHuman` with the role/attribute spec and records an `Unclaimed` `HumanTask`; the token parks.
- [ ] `ActorResolver` candidates populate the `TaskStore`; `ClaimableBy(role)` and `AssignedTo(actor)` return the correct buckets.
- [ ] Claim/reassign/complete each pass through `Step` as triggers carrying `authz.Actor`; the journal records all three with `OccurredAt`.
- [ ] Completion advances the token, sets the user-task `NodeVisit.ActorID`, sets task `State=Completed`, and emits `UpdateTask`.
- [ ] `TaskService` rejects an ineligible actor (via `Authorizer`) before injecting a trigger.
- [ ] `Step` deterministic + pure (Plan 1–3 invariants hold); engine imports only stdlib + model + authz + humantask + expreval; no `time.Now()` in engine.
- [ ] `-race` green; coverage ≥ 85% touched packages; lint clean.

## Self-Review Notes

- **Spec coverage:** §4 actor audit (NodeVisit.ActorID, journal), §5 human triggers + AwaitHuman/UpdateTask, §8 authz/humantask ports + fakes, §10 "everything through the core as triggers". SLA/reminders (timers) deferred to Plan 5 — `DueAt` is present but unset here.
- **Purity/determinism:** TaskToken from `TaskSeq` counter; resolver/store are runtime-side; `cloneState` extended to copy `Tasks`. Authorization is runtime I/O, never in `Step`.
- **Model boundary:** `model` stays stdlib-only — user-task eligibility is primitive fields; the engine builds `authz.AuthzSpec` from them.
- **Grounding required:** read the merged `engine/step.go`, `runtime/runner.go`, and `runtime/example_test.go` (Plan-1 `Run`) before editing; `NewRunner` arity changes — update existing call sites/tests in the same task.
