# Design: reachability + conservative fork-join pairing validation

**Date:** 2026-06-22
**Status:** Approved (brainstorming)
**Track:** Consolidated-backlog top pick (Correctness). Follow-up to ADR-0014.
**ADR:** 0030.

## 1. Problem & scope

`model.Validate` already enforces referential integrity, dead-ends (`ErrDeadEnd`),
start/end incidence, condition/default placement (`ErrConditionNotAllowed`/`ErrDefaultNotAllowed`),
event-gateway targets, the ADR-0014 mixed split+join gateway rule, boundary attachment, retry/recovery
field constraints, and recurses into sub-processes (returning a joined error of *all* violations).

Two structural soundness gaps remain (named in ADR-0014 as the deferred follow-up):

1. **Reachability** — a node unreachable from the start event is dead/orphan structure that almost
   always indicates an authoring error; nothing catches it today.
2. **Fork-join pairing** — a **parallel join** whose incoming branches are never concurrently
   activated deadlocks at runtime (the token parks forever waiting for branches that never arrive).
   ADR-0014 deferred this because a naïve pairing analysis has "real false-positive risk on
   legitimate loop and multi-merge patterns."

This track adds both, with a **conservative, false-positive-averse** design: validation runs at
definition load, so a false positive rejects a consumer's *valid* definition — the worst outcome.
We therefore bias every new rule toward **false-negatives** (miss some unsound definitions) rather
than false-positives (reject sound ones).

**In scope:** two new sentinels `ErrUnreachableNode` and `ErrUnpairedJoin`, added to `validate()`
so they recurse into sub-processes; pure `model/` only (no engine dependency).

**Out of scope (deferred):** no-path-to-end / sink detection; inclusive/exclusive join pairing
(they don't deadlock — see §4); full structured-soundness / SESE region decomposition; condition
satisfiability ("does an exclusive split always have a taken branch").

**Engine is untouched** (zero diff). `model/` gains pure validation logic only.

## 2. The `model/` purity constraint

The engine already has an unexported reverse-BFS `nodesThatCanReach` (`engine/step.go`), but `model`
must not import `engine` (engine imports model, not vice-versa). We add **model-local** forward-BFS
helpers. They are pure functions over `*ProcessDefinition` using the existing `Outgoing`/`Incoming`/
`Node` lookups — no new model fields.

```go
// forwardReachable returns the set of node IDs reachable from seed by following
// outgoing sequence flows (BFS, cycle-safe via the visited set). seed is included.
func forwardReachable(d *ProcessDefinition, seed string) map[string]bool
```

## 3. Rule A — reachability (`ErrUnreachableNode`)

A node is **reachable** if execution can ever place a token on it. Beyond plain forward flow from
the start, two model constructs introduce reachability that is *not* a sequence flow and must be
seeded explicitly, or they produce false positives:

- **Boundary events** have no incoming sequence flow — they attach to a host via `AttachedTo` and
  fire while the host is active. A boundary event (and everything its outgoing flow leads to) is
  reachable **iff its host is reachable**.
- **Event-sub-processes** (`KindEventSubProcess`) are event-triggered roots with no incoming flow;
  they can fire whenever the enclosing scope is active. Treated as **always-reachable roots** within
  their definition.

### Algorithm

Run **only when the definition has exactly one start event** (`len(StartNodes()) == 1`). If 0 or >1
starts, `ErrNoStartEvent`/`ErrMultipleStartEvents` already fire and reachability is ill-defined —
skip to avoid cascade noise.

```
R := forwardReachable(d, theStartNode.ID)
// Event-sub-process roots:
for each node n where n.Kind == KindEventSubProcess:
    R ∪= forwardReachable(d, n.ID)
// Boundary-event fixpoint (a boundary's branch may host another activity with a boundary):
repeat until R stops growing:
    for each boundary event b where b.AttachedTo ∈ R and b.ID ∉ R:
        R ∪= forwardReachable(d, b.ID)
// Report:
for each node n where n.ID ∉ R:
    errs append ErrUnreachableNode (node n.ID)
```

Dangling-flow sources/targets are already reported by `ErrDanglingFlow`; reachability uses the node
set as-is and will simply not reach a node wired only by a dangling flow (no double-report beyond the
existing dangling error, which is acceptable and informative).

**False-positive analysis:** the only nodes without an incoming sequence flow are the start event,
boundary events (seeded by host), and event-sub-processes (seeded as roots). Every other legitimate
node is flow-reachable. Catch events targeted by event-based gateways are normal flow targets. Thus
a flagged node is genuinely orphaned. Near-zero false positives.

## 4. Rule B — conservative parallel-join pairing (`ErrUnpairedJoin`)

### Why only parallel joins

| Join kind | Firing semantics | Deadlock risk? |
|---|---|---|
| Exclusive (`KindExclusiveGateway`, N-in/1-out) | fires on **first** arriving token | No |
| Event-based | a fork only (its joins are exclusive-merge-like) | No |
| Inclusive (`KindInclusiveGateway`, N-in/1-out) | engine fires it when **no outside token can still reach it** (runtime `nodesThatCanReach`) — self-adjusting | No |
| **Parallel (`KindParallelGateway`, N-in/1-out)** | waits for a token on **every** incoming flow in-scope | **Yes** |

Only the **parallel join** waits unconditionally for all incoming branches, so only it can deadlock.
Checking the others would *only* generate false positives. The pairing rule therefore targets
parallel joins exclusively (the mixed-gateway rule already guarantees a parallel gateway is either a
pure split or a pure join).

### The rule

A **parallel join** J is a node with `Kind == KindParallelGateway`, `len(Incoming(J)) > 1`, and
`len(Outgoing(J)) == 1`. J is **unpaired** (→ `ErrUnpairedJoin`) iff there is **no** concurrency
source for it:

> There exists **no** split `F` (`F ≠ J`, `F.Kind ∈ {KindParallelGateway, KindInclusiveGateway}`
> with `len(Outgoing(F)) > 1`) having **≥ 2 distinct outgoing branches** `b` whose target can
> forward-reach `J` (`J.ID ∈ forwardReachable(d, b.Target)`).

If such an `F` exists, `F` can place concurrent tokens on ≥2 paths leading to `J` — J is paired
(accepted). Only parallel/inclusive splits create concurrency; exclusive/event-based splits pick a
single branch, so they are not concurrency sources.

This is **maximally lenient on acceptance** (any plausible concurrency source clears the join) and
flags only when *no* fork can ever deliver two concurrent tokens toward J — a provable deadlock.

- Skip J when J is **unreachable** (Rule A already reports it) — avoids double noise.
- Forward reachability is cycle-safe; loop back-edges never cause a flag because a real upstream
  parallel fork (if any) still clears the join, and a loop-merge-via-parallel-gateway with no fork
  *is* a genuine deadlock (correctly flagged, not a false positive).

### False-positive analysis

- **Classic bug caught:** `exclusive-split → A, B → parallel-join`. The split is exclusive (not a
  concurrency source); no parallel/inclusive fork feeds ≥2 of J's branches → flagged. Correct.
- **Legitimate nested concurrency accepted:** `parallel-split F → A, B → parallel-join J`. F has 2
  branches reaching J → accepted.
- **Conditional concurrency accepted (false-negative bias):** a fork feeds branches that *may* be
  pruned by an inner exclusive choice; structurally a fork still reaches J on ≥2 branches → accepted
  even if a runtime path could deadlock. We deliberately do **not** flag — favoring no-false-positive.
- **Loops:** a parallel join used as a loop merge with no concurrency fork is a real deadlock →
  flagged; a loop *inside* a properly forked parallel region still has the fork reaching J → accepted.

## 5. Placement & recursion

Both rules are added inside `validate(d, seen)` (the recursive worker), after the mixed-gateway
block, so they automatically apply to every sub-process definition via the existing recursion and
cycle guard. They append to the same `errs` slice and surface through the existing
`errors.Join(errs...)` (exhaustive, no short-circuit). Nested-definition errors keep their
`subprocess %q: %w` wrapping.

New sentinels (sibling style, wrapped with the offending node id):

```go
// ErrUnreachableNode is returned when a node cannot be reached from the start
// event (directly or via a reachable boundary event / event-sub-process).
var ErrUnreachableNode = errors.New("workflow-model: unreachable node")

// ErrUnpairedJoin is returned when a parallel join gateway has no concurrency
// source — no parallel/inclusive split can deliver two concurrent tokens toward
// it — so it would deadlock at runtime.
var ErrUnpairedJoin = errors.New("workflow-model: unpaired parallel join")
```

(`workflow-` prefix per ADR-0026 / the `error-sentinel-prefix` convention.)

## 6. Testing strategy

Black-box `model_test`, table-driven (existing `TestValidate` style; assert-closure form). New cases:

**Reachability:**
- valid linear / split-join: all reachable → no error.
- orphan node (no incoming, not start/boundary/event-subprocess) → `ErrUnreachableNode`.
- node reachable only via a boundary event whose host is reachable → **no** error (seeded).
- boundary event on an **unreachable** host → the boundary's branch is unreachable → `ErrUnreachableNode`.
- event-sub-process node (no incoming flow) → **no** `ErrUnreachableNode` (root-seeded); a node
  reachable only from inside it is validated in the nested def.
- unreachable node nested in a sub-process → wrapped `ErrUnreachableNode`.
- 0/>1 start events → reachability **not** run (only the start-count error), asserted by absence of
  `ErrUnreachableNode`.

**Fork-join pairing:**
- `parallel-split → a,b → parallel-join → end` → **no** error (paired).
- `exclusive-split → a,b → parallel-join` → `ErrUnpairedJoin`.
- parallel join fed by an **inclusive** split (≥2 branches) → no error (inclusive is a concurrency source).
- **inclusive** join fed by an exclusive split → **no** `ErrUnpairedJoin` (rule targets parallel joins only).
- **exclusive** join (N-in/1-out exclusive) fed by exclusive split → **no** `ErrUnpairedJoin`.
- nested: a parallel split inside one branch still reaching the join on ≥2 branches → no error.
- loop with a proper parallel fork still reaching the join → no error (cycle-safe, no false positive).
- unreachable parallel join → only `ErrUnreachableNode`, **not** `ErrUnpairedJoin` (skipped).

**Gate (model package):** `go test -race ./model/...` green; ≥85% line coverage on `model`;
`golangci-lint run ./...` clean; engine/model purity intact (no new imports beyond stdlib `errors`/
`fmt`); full `go test ./...` green (no regressions — existing valid test fixtures must still pass,
in particular any that rely on previously-unvalidated reachability/pairing).

## 7. Migration / compatibility note

These rules make `Validate` **stricter**: a previously-accepted definition with an orphan node or a
deadlocking parallel join will now be rejected at load. This is the intended correctness nudge
(consistent with ADR-0014 adding `ErrMixedGateway` always-on). The conservative pairing bias keeps
this from rejecting legitimate concurrent models. The repo's own example/test definitions are
verified to still pass (part of the gate).

## 8. ADR

| ADR | Decision |
|---|---|
| **0030** | Add `ErrUnreachableNode` (start-reachability with boundary-host + event-sub-process seeding) and `ErrUnpairedJoin` (conservative **parallel-join-only** pairing: flag iff no parallel/inclusive split delivers ≥2 concurrent tokens toward the join) to `model.Validate`. Inclusive/exclusive/event joins excluded (they don't deadlock). False-positive-averse (false-negative bias). Pure `model/`, recurses into sub-processes; engine untouched. |
