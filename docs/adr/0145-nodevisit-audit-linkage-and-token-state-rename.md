# 145. NodeVisit task linkage + close_kind; token-state rename

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-audit-view-and-idgen.md](../specs/2026-07-27-processinstance-audit-view-and-idgen.md).
Part of the ADR-0144…0149 delivery.

## Context

`engine.NodeVisit` (`engine/state.go:134`) records one token traversal of one node:
`{NodeID, TokenID, EnteredAt, LeftAt *time.Time, ActorID *string}`. `ActorID` is
stamped on human completion by `setVisitActor` (`step_state.go:150`), matched by
`(tokenID, nodeID)`. There is **no** link from a visit to its human-task record
(the correlation key today is `node_id`, which is ambiguous under loops /
multi-visit), and **no** record of *why* a visit closed — a cancelled, terminated,
compensated, or normally-completed visit all look identical (only `left_at` is
set).

Tokens are consumed via `consumeToken` (`step_state.go:191`) and terminal paths
(`s.Tokens = nil` on terminate — `step_triggers.go:225`, `step_nodes.go:492`); each
such site knows the reason but discards it.

Separately, the `TokenState` names are inconsistent: `TokenWaitingCommand`
over-narrows (a parked token awaits a command *or* signal *or* message — see the
`AwaitCommand`/`AwaitSignal`/`AwaitMessage` fields on `Token`), and `TokenAtJoin`
is location-phrased where the others are activity/lifecycle-phrased. The wire
strings `waitingCommand`/`atJoin` are produced solely by `tokenStateString`
(`service/instance.go`).

## Decision

1. **Task linkage.** Add `NodeVisit.TaskToken string`, set when the human task is
   minted in `userTaskStrategy.enter` (so it is present on the *open* visit too,
   not only after completion). The instance view renders it as `task_id` on
   userTask visits; consumers resolve the actor and the rest from the linked
   `tasks` entry. **Remove** `actor_id` from the history projection.
2. **Close reason.** Add a `NodeVisit` close-reason field, stamped **only at named
   terminal sites** (never inside `consumeToken`, which also fires on normal
   gateway/join/subprocess closes), with one of: `instance_cancelled`,
   `boundary_interrupted` (interrupting boundary *and* error caught by a boundary),
   `terminated` (terminate end), `errored` (uncaught error end), `compensated`,
   `reversed`, `deadline_expired` (human-task deadline-breach reroute). A
   non-interrupting boundary does **not** close the host visit (no stamp). Normal
   completion leaves it unset. The view renders it as `close_kind`, **omitted for
   normal closes**. The name uses the library's `kind` discriminator convention
   (`NodeWire.Kind`, `TriggerWire.Kind`), not `type`. (Value set and per-site
   mapping expanded per the rule-#9 audit.)
3. **Token-state rename** (Go constants + wire values), breaking, pre-release:
   `TokenWaitingCommand` → `TokenWaiting` (wire `waiting`), `TokenAtJoin` →
   `TokenJoining` (wire `joining`). `TokenActive`/`active` and
   `TokenIncident`/`incident` unchanged.

`NodeVisit` is persisted only inside the untagged `snapshot` blob, so the new
fields are additive and safe.

## Consequences

- **Positive:** every history entry for a user task links unambiguously to its
  task (by token, not node id); the audit reason for every non-normal visit close
  is captured; the token-state vocabulary is accurate and consistent
  (`waiting` covers all park kinds).
- **Negative / breaking:** `TokenWaiting`/`TokenJoining` renames touch ~28 files
  (10 prod + 18 test for the command state, 4 prod + 2 test for the join state) —
  mechanical. Any external consumer switching on the old constant names or the old
  wire strings breaks.
- **Risk:** `close_kind` correctness depends on stamping the *right* reason at each
  terminal site (cancel, interrupting boundary, terminate end, compensation,
  reverse, error). Each site is enumerated and tested; a miss defaults to unset
  (renders as a normal close) rather than a wrong value — fail-soft.
- The `(tokenID, nodeID)` visit lookup in `setVisitActor` is retired in favour of
  stamping `TaskToken` at creation; the actor is no longer stamped on the visit.

## Implementation amendments (2026-07-27, Phase 3)

- **`close_kind` is per-VISIT, not per-instance-outcome.** The stamp answers "why
  did THIS visit end", so an error end event's own visit closes as `errored` even
  when a boundary later catches that error; `boundary_interrupted` marks the HOST
  activity whose token an interrupting boundary consumed. This keeps every stamp
  decidable at its own site, with no restamping after propagation resolves.
- **Interrupting event sub-process** reuses `boundary_interrupted` for the tokens
  it cancels in the enclosing scope — it is the same "an event interrupted this
  activity" shape, and inventing a seventh value would not tell a consumer
  anything actionable.
- **Compensation walks carry the reason in.** `compensationOutcome` gained a
  `CloseKind` field: cancel paths pass `instance_cancelled`, the terminal-error
  path `errored`, and an administrative `CompensateRequested` derives
  `reversed` (it names a rollback target or resumes) vs `compensated` (it only
  compensates and terminates).
- **Mechanics:** `closeVisitAs`/`consumeTokenAs`/`moveTokenToTargetAs` are the
  stamping forms; the plain `closeVisit`/`consumeToken`/`moveTokenToTarget` now
  delegate to them with an empty kind, so a NORMAL close cannot accidentally
  acquire a reason. `cancelTokenWaits` takes the reason as a parameter.
- `NodeVisit.ActorID` and `setVisitActor` are **removed** (no reader survives the
  history `actor_id` removal); `setVisitTask` replaces them.

## Implementation amendments (2026-07-28, code review)

### 1. `CloseKind` is a defined type, not a bare string

The close reasons shipped as untyped string constants assigned to a plain
`CloseKind string` field on `NodeVisit`. Code review flagged the asymmetry with
the sibling discriminators in the same package — `TokenState` and `Status` are
both named types — and the practical consequence: `v.CloseKind = "cancelled"`
compiled, shipped, and surfaced only as a wrong value in a consumer's rendered
history.

**Amendment:** `type CloseKind string` with typed constants and a `String()`
method. `NodeVisit.CloseKind`, `closeVisitAs`, `consumeTokenAs`,
`moveTokenToTargetAs`, and `compensationOutcome.CloseKind` all carry the named
type, so the compiler — not review — is what rejects an undeclared reason.

The zero value still means "normal close", which is the load-bearing half of the
contract and is unchanged. The wire representation is unchanged too: the
`service` DTO keeps a plain `string` field and converts via `String()`, because
that projection is the wire shape rather than the domain type.
