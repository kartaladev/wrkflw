# 14. Reject mixed split+join gateways in model.Validate

- Status: Accepted
- Date: 2026-06-21

## Context

`model.Validate` checks a process definition's structural soundness (referential
integrity of sequence flows, node-kind sentinels, a cycle guard) before it is
executed. It does **not** today distinguish a *converging* gateway (a join, >1
incoming flow) from a *diverging* gateway (a split, >1 outgoing flow). A gateway
authored with both multiple incoming and multiple outgoing flows — a "mixed"
gateway — is structurally ambiguous: the engine's gateway logic treats a gateway
as either a split or a join based on flow counts, so a mixed gateway can route in
a way the author did not intend, silently, with no validation error. This is a
tracked follow-up in `docs/plans/HANDOVER.md` ("typed/paired gateway validation").

A full solution would pair every converging join with a matching diverging fork
via reachability analysis and check that branch conditions appear only on
diverging flows. That is a substantial analysis with real false-positive risk on
legitimate loop and multi-merge patterns.

## Decision

Add one focused structural rule to `model.Validate`: **a gateway node with both
more than one incoming sequence flow and more than one outgoing sequence flow is
invalid.** It applies to all gateway kinds (`KindExclusiveGateway`,
`KindInclusiveGateway`, `KindParallelGateway`, `KindEventBasedGateway`).

- A new sentinel `model.ErrMixedGateway` (sibling of the existing `Validate`
  sentinels) is returned, wrapped with the offending node id for diagnostics.
- The rule is enforced recursively into sub-process definitions, reusing the
  existing `Validate` traversal and cycle guard — no separate walk.
- Incoming/outgoing counts use the existing sequence-flow lookups; **no new model
  fields** are added.
- Pure split (1-in / N-out), pure join (N-in / 1-out), and pass-through
  (1-in / 1-out) gateways remain valid.

Reachability/fork-join *pairing* and condition-placement checks are explicitly
**not** part of this rule; they are a deferred follow-up.

## Consequences

**Easier:** the most common silent-misroute authoring mistake — a gateway that
accidentally both joins and splits — is now caught at definition time with a clear,
node-identified error, with near-zero false positives (BPMN best practice is
already that a gateway either splits or joins, not both). The rule is small,
reuses the existing recursive traversal, and adds no model surface beyond one
sentinel.

**Harder / trade-offs:** this is a **partial** check — it does not verify that a
join's gateway *type* matches its fork, nor that every parallel/inclusive join is
reachable from a matching fork, so some structurally unsound definitions still pass
`Validate` and fail (or misbehave) only at execution. That broader reachability
analysis is deferred because it is complex and risks rejecting legitimate loop and
multi-path merge patterns. A definition that legitimately wants a single gateway to
both merge inbound paths and split outbound (uncommon, and better modelled as a
join gateway followed by a split gateway) must now be rewritten as two gateways;
this is the intended nudge toward unambiguous models.
