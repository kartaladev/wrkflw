# 0103. Fire-once boundary action (WithBoundaryAction)

- Status: Accepted
- Date: 2026-07-08

## Context

Before this decision, a boundary event's sole purpose was to route a token to an
alternative path when its trigger (timer, message, signal, or error) fired.
There was no way to attach a catalog side-effect action to the boundary fire
itself — any work that had to run *at the moment of boundary firing* had to be
modelled as an additional service task on the boundary's outgoing flow, polluting
the definition graph with single-purpose nodes.

The `activity.WithDeadline(triggerSpec, flowID, actionName)` option already
demonstrated a bundled fire-once action pattern: when a deadline fires it runs
the named action (result discarded, failure non-fatal) and then routes. That
behavior was locked inside the deadline implementation and unavailable to true
boundary-event nodes.

There is no natural place to express "run this action when the boundary fires,
regardless of which path the token subsequently takes" without a dedicated option.

## Decision

We will add `event.WithBoundaryAction(name string)` as a `BoundaryOption` that
sets the `Action string` field on `BoundaryEvent`. When the boundary fires (any
trigger type — timer, message, signal, error; interrupting or non-interrupting)
the engine emits an `InvokeAction{FireAndForget: true}` step before routing.
The action result is discarded; a failure is logged and routing continues
normally.

The field is persisted in the definition wire format (`BoundaryAction` wire key)
so YAML-authored definitions can use it.

`WithDeadline`'s third argument uses the identical `InvokeAction{FireAndForget:
true}` execution path — a deadline can be understood as a timer boundary with a
bundled fire-once action. This consistency is noted for maintainers; no code
unification is made, because `WithDeadline` is an activity option (ADR-0113
bundled options) and boundary action is a boundary-event option.

## Consequences

- **Positive.** Authors can attach a notification, audit-log, or alert action to
  any boundary fire without adding an extra service node to the definition graph.
- **Positive.** The semantics are consistent with `WithDeadline`'s action arg and
  with the `InvokeAction{FireAndForget: true}` pattern already used for deadline
  actions.
- **Positive.** The option is available for all trigger types (timer, message,
  signal, error) and both interrupting and non-interrupting modes.
- **Neutral.** Failure of the boundary action is non-fatal: it is logged and
  routing proceeds. Authors who need the action failure to abort routing should
  model the action as a service task on the boundary outgoing flow instead.
- **Neutral (wire).** The wire field `BoundaryAction` is added; existing
  serialized definitions without the field deserialize with `Action = ""` (no
  action), which is the previous behavior.
