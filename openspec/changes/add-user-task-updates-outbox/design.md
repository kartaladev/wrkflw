# Design

## Context

See proposal.md for motivation and the re-measured premises. The topic set and the reference payload are decided by kartaladev/wrkflw#29 (Q8 (i) a, (ii) a, and the Decision comment's Outbox paragraph). The dedup key it names is the one point a re-measurement contradicts (Q1).

Measured on `b8576841`:
- `deliverLoop` captures `prevStatus` before `Step` (`runtime/processdriver.go:653`) and derives events after it (`:749-750`) from the new state and the step's commands.
- `writeOutbox` assigns `dedup_key = "<instance_id>:<seq>:<index>"` and ignores `OutboxEvent.DedupKey` (`internal/persistence/store/store_core.go:383-392`). The publisher uses the stored key as the envelope ID (`eventing/publisher.go:121`). The in-memory store buffers events as given (`runtime/kernel/memstore.go:122-164`).
- Tasks enter `InstanceState.Tasks` at user-task entry, and are closed at the four sites listed in the proposal. Claim, reassign and completion change the record in their handlers. Updates change it in `add-user-task-updates`'s handlers, and that change's step also carries `TaskUpdates` rows.

## Goals / Non-Goals

**Goals:**
- One event per task transition, derived deterministically from what the step committed, in the same transaction.
- Payloads that are safe to ship to a broker with no disclosure layer.

**Non-Goals:**
- Topic filtering or a consumed-topics declaration (#221).
- Event versioning or schema registry. The payload keys are the contract and are specified.

## Decisions

### D1. Derive from the task records before and after the step, plus the step's update rows

`deliverLoop` captures a small pre-step view per task (ID, state, claim principal ID) beside `prevStatus`. After `Step`, `taskOutboxEvents(prev, st, res)` walks `st.Tasks` in snapshot order:
- absent before → `task.created`;
- claim principal changed, or set from none, without a reassign trigger → `task.claimed`. This covers an explicit claim and the implicit claim of a progress or completion;
- a `HumanReassigned` trigger for the task → `task.reassigned`;
- each `applied` row in `res.TaskUpdates` → `task.progressed`, and each `rejected` row → `task.update_rejected`;
- state became `completed` → `task.completed`, or `cancelled` → `task.cancelled`.

Within one task the order is fixed: created, claimed/reassigned, progressed/update_rejected, completed/cancelled. A `submitted` or `abandoned` row emits nothing of its own. Submission is not a lifecycle transition (#29's topic list has none), and abandonment is reported by `task.cancelled`.
- Rejected: deriving only from the trigger type. `task.created` and three of the four cancel sites have no human trigger, and an implicit claim would be missed.
- Rejected: emitting from the engine. #29 measured, and the code confirms, that every outbox event is runtime-derived, and that keeps the engine free of transport concerns.

### D2. `actor_id` is the principal's ID; engine-driven transitions carry ""

#119 (decided) makes the principal the identity of record (`PrincipalID()`). For created and cancelled events there is no human, so the field is the empty string, not absent. The payload keys are then fixed for every topic.

### D3. No values, by construction

The payload is built from IDs, the state name and the row's `{name, seq}`. The builder takes no map from variables, input, value or reason, so a test cannot pass by luck. The secrets scenario plants the same marker in input, value, reason and note to catch any later widening.

### Cross-change coupling

- `add-user-task-updates`: provides `res.TaskUpdates`, `Progressed`, and the implicit claim. It must land first.
- `clarify-terminal-event-delivery` (#221): owns whether unconsumed topics are written at all. Q2 here follows its ruling. Both edit `runtime/outbox.go` and the outbox section of `runtime/README.md`. The second to land rebases.
- `replace-casbin-with-composed-deciders` (#119): `PrincipalID()`. If it has not landed, `actor_id` is `Actor.ID`, and the on-behalf row is added when it lands.
- `encode-persisted-enums-by-name`: `state` in the payload is the state's name, never its ordinal.

## Risks / Trade-offs

- [Outbox volume grows by up to one row per task transition for every consumer with user tasks] → Q2. The relay's existing batching and dead-letter path apply unchanged.
- [A consumer keys idempotency on the decided `…:topic` format from #29] → Nothing has shipped. Q1 picks the format before release, and the README states it.
- [Derivation by diff could miss a transition that sets and unsets a field within one step] → No handler does so. The topic-per-transition table pins every transition the state machine has.

## Migration Plan

Additive topics. No consumer change is needed unless it subscribes. No data migration.

## Open Questions

- **Q1. Dedup key.** #29 decided `instance_id:journal_seq:topic`.
  - (a) Keep the store's shipped `<instance_id>:<seq>:<index>`. It is unique per event and per step, it is already the envelope ID, and nothing changes in the persisted format.
  - (b) `instance_id:journal_seq:topic:task_id`. It names the event, it is unique per transition per task, and it requires the store to honor `OutboxEvent.DedupKey`. That is a behavior change for every existing topic, and a persisted-format change in what `dedup_key` holds.
  - (c) The decided `instance_id:journal_seq:topic`. It collides when one step writes a topic twice (two tasks created by a parallel split, two cancelled by an instance cancel), so a consumer deduplicating on it would drop a real event.

  **Recommendation: (a).** Specs state uniqueness and the instance-and-seq prefix, which hold under (a) and (b), and are marked pending.
- **Q2. Default on, or opt-in.** #29 decided the topics. It did not weigh that every deployment with user tasks then writes task rows whether or not anything subscribes, which is the concern #221 raises for terminal events.
  - (a) On by default, as decided. Volume control follows #221's ruling.
  - (b) Opt-in through a runtime option, off by default.

  **Recommendation: (a).** It is the decision, and a consumer-topics mechanism, if #221 adopts one, would cover every topic and not only these. The specs describe (a) and are marked pending.
