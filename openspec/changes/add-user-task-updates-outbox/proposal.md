# Proposal

## Why

A consumer that keeps a task list, notifies an assignee, or reacts to a rejected update has nothing to subscribe to. The outbox carries only `instance.completed`, `instance.failed`, `instance.terminated` and `message.<name>` (`runtime/outbox.go:33-62` on `b8576841`). Every task transition is visible only by polling. kartaladev/wrkflw#29 (decided, Q8 (i) a and (ii) a) put the whole task lifecycle on the outbox with reference payloads. A consumer that subscribes only to update events cannot build a task view, so shipping those alone "invites the next ticket immediately".

## What Changes

This is part 4 of 4 of kartaladev/wrkflw#41 ("Build the UserTask updates feature"): #41 section 5 (outbox), plus the outbox half of section 6's tests. The design is kartaladev/wrkflw#29 (decided), applied and not reopened, except where a re-measurement contradicts it (Q1).

- **Seven task topics,** derived in the runtime from each applied step, next to `terminalOutboxEvent`, and committed in `kernel.AppliedStep.Events` with the step: `task.created`, `task.claimed`, `task.reassigned`, `task.progressed`, `task.update_rejected`, `task.completed` and `task.cancelled`.
- **Reference payloads only:** `{task_id, instance_id, node_id, state, update: {name, seq} | null, actor_id}`. They carry no input, value, variables, reason or note. A consumer that wants a value reads `GET /tasks/{token}/updates`, where disclosure applies. With kartaladev/wrkflw#119 (decided), `actor_id` is the principal's ID when the actor acts on behalf of one.
- **Docs: one line for `instance.completed`.** It ships the whole variables map unprojected, which is an existing exposure that is documented here and not extended (#29, Q8 (ii) rationale and A10).

Re-measured on main (`b8576841`):
- **Holds:** events are derived in the runtime, never emitted by the engine. The derivation runs at `runtime/processdriver.go:749-750`, with the pre-step status captured at `:653`. The outbox has no disclosure projection.
- **Stale / defective, found on re-measurement: the decided dedup key `instance_id:journal_seq:topic`.** (1) The SQL store ignores `OutboxEvent.DedupKey`. It writes `"<instance_id>:<seq>:<index>"` itself (`internal/persistence/store/store_core.go:383-392`), which is unique per event of a step. (2) The decided format is not unique: one step can emit the same topic twice, for example a parallel split creating two tasks (two `task.created`) or an instance cancel closing two tasks (two `task.cancelled`). This is Q1.
- **New:** today `task.created` has no trigger of its own. A task appears in the snapshot when a token enters a user task (`engine/step_nodes.go`, `AwaitHuman` at `engine/command.go:148`). It is cancelled at four sites (`engine/state.go:726`, `step_cancel.go:39`, `step_stale_commands.go:170`, `step_timers.go:95`). So the deriver works from the task records before and after the step, not from the trigger alone (design D1).

Impact flags:
- **Persisted format: additive.** New topics in `wrkflw_outbox` rows, with no column change. Once a consumer subscribes, these topics and payload keys are a wire contract.
- **Behavior change for existing consumers:** every instance with user tasks now writes task rows to the outbox, and the relay publishes them. A consumer that relays to a broker with no subscription for `task.*` sees new traffic. This compounds kartaladev/wrkflw#221's "rows nobody consumes" (owned by `clarify-terminal-event-delivery`). This is Q2.
- **BREAKING:** none in the Go API.
- **Default security posture:** unchanged by design. Payloads carry IDs and states only, and the one existing exposure (`instance.completed`) is documented, not widened.
- **Consumer go.mod:** none.

Out of scope:
- The state machine and the rows these events reference: `add-user-task-updates` (part 2).
- The read routes a consumer follows from an event: `add-user-task-updates-http` (part 3).
- Whether the runtime declares or filters consumed topics: kartaladev/wrkflw#221, owned by `clarify-terminal-event-delivery`.
- Projecting `instance.completed`'s payload: not filed. #29 recorded it as an exposure to document.

Decisions this change cannot be fully specified without (design, Open Questions):
- **Q1.** The dedup key. Recommended: keep the store's existing `<instance_id>:<seq>:<index>`, which is unique and already shipped, and drop the decided `:topic` form. The uniqueness rows hold under the recommendation and are marked *pending Q1*.
- **Q2.** Whether task events are written by default or only when a consumer opts in. Recommended: by default, as #29 decided, with volume control following the ruling on #221. Specs are written for the default and marked *pending Q2*.

**Coordination**
- Size: M
- Depends on: add-user-task-updates
- Blocks: cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `event-delivery`: ADDED, the task lifecycle topics and when each is written, reference-only payloads, commit atomicity with the step, and unique dedup keys within a step.

## Impact

- `runtime/outbox.go`: `taskOutboxEvents(prev, next, step)` next to `terminalOutboxEvent`. `runtime/processdriver.go`: capture the pre-step task view and append the events.
- `runtime/kernel/memstore.go`: parity of the dedup key, if Q1 is ruled to make it observable there.
- `internal/persistence/store`: no change under Q1's recommendation, plus one test asserting uniqueness for repeated topics in a step.
- Docs: `runtime/README.md` outbox section (the topic table, the payload shape, the `instance.completed` line), and the `eventing` package doc if it lists topics.
