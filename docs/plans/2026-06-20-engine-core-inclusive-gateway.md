# Engine Core — Inclusive (OR) Gateway (Plan 3 of 6) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Inclusive (OR) gateway — diverging (activate every branch whose condition holds, else the default) and converging (the OR-join, which fires once no further token can still reach it) — on top of Plans 1–2.

**Architecture:** Routing happens in `engine.drive()` when a token sits on an Inclusive gateway node. OR-fork reuses the Plan-2 `expreval` evaluator to take *all* true branches. The OR-join uses **reachable-incoming analysis**: it parks arriving tokens (`TokenAtJoin`) and fires only when no other token in the instance can still reach the join — so it correctly does *not* wait for branches that were never activated.

**Tech Stack:** Go 1.25, `github.com/expr-lang/expr` (via `expreval`), `github.com/stretchr/testify`.

## Global Constraints

- Go **1.25**; module `github.com/kartaladev/wrkflw`; public packages at module root (no `pkg/`).
- The core (`engine`, `model`, `expreval`) imports only stdlib + `model` + `expreval` (+ `expr-lang/expr` inside `expreval` only). **No** transport/storage/bus/time-vendor; the engine **never calls `time.Now()`** — time enters as `Trigger.OccurredAt`.
- `Step` stays **deterministic** (identical `(state,trigger)` ⇒ identical `(state,commands)`; IDs from in-state counters; flows evaluated in definition order) and **pure** (never mutates input state). Public `Step` signature unchanged.
- Tests are **black-box**, table-driven with an **`assert` closure per case**, `t.Context()` where needed.
- Coverage ≥ 85% on touched packages; `go test -race ./...` green; `golangci-lint run ./...` clean. Conventional Commits; commit after each green step group.

## Prerequisite

**Plan 2 (gateways) must be merged first.** This plan assumes the Plan-2 state of `engine/step.go`:
- `drive(def, *InstanceState, at) ([]Command, error)` with cases for `KindStartEvent`, `KindServiceTask`, `KindEndEvent`, `KindExclusiveGateway`, `KindParallelGateway`, and a `default` (park).
- Helpers: `moveTokenToTarget`, `forkParallel`, `tryParallelJoin`, `selectExclusiveTarget`, `placeToken`, `consumeToken`, `closeVisit`, etc.
- Package-level `conditions = expreval.New()` in `engine/conditions.go`.
- Sentinel `engine.ErrNoMatchingFlow`.
- `model.Validate` already permits conditions/defaults on `KindInclusiveGateway` (Plan-2 Task 2 treats inclusive as a conditional gateway), so **no model changes are needed here.**

> **Roadmap note:** The **event-based gateway** is intentionally NOT here — it routes on which intermediate catch event (timer/signal/message) fires first, so it lands in the events plan (Plan 5). Remaining roadmap: **4** human tasks & timers → **5** events, event-based gateway & sub-processes → **6** errors/compensation/micro-step.

---

## File Structure

```
engine/
  step.go                       # MODIFY: add KindInclusiveGateway case + forkInclusive + tryInclusiveJoin + nodesThatCanReach
  step_inclusive_test.go        # (black-box) OR-fork + OR-join behavior
runtime/
  example_test.go               # MODIFY: add an end-to-end inclusive flow (2-of-3 branches)
```

---

### Task 1: Inclusive (OR) gateway — diverging (fork all true branches)

**Files:**
- Modify: `engine/step.go`
- Test: `engine/step_inclusive_test.go`

**Interfaces:**
- Produces: a `KindInclusiveGateway` case in `drive` and helper `(*InstanceState).forkInclusive(def, *Token, node, at) error`. OR-fork activates **every** non-default outgoing flow whose condition is empty or true (definition order); if none are true it takes the default flow; if none are true and there is no default it returns `ErrNoMatchingFlow`. The incoming token is consumed; one Active token is created per taken branch.
- Note: this task handles the **diverging** case (≤1 incoming). The converging case (>1 incoming) is Task 2; until then a >1-incoming inclusive gateway falls through to `forkInclusive` — Task 1's tests only use diverging gateways.

- [ ] **Step 1: Write the failing test**

Create `engine/step_inclusive_test.go`:
```go
package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/model"
)

// inclusiveForkDef: start -> or -{a>0}-> ta ; -{b>0}-> tb ; -default-> tc ; each -> its end
func inclusiveForkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "or", Kind: model.KindInclusiveGateway},
			{ID: "ta", Kind: model.KindServiceTask, Action: "a"},
			{ID: "tb", Kind: model.KindServiceTask, Action: "b"},
			{ID: "tc", Kind: model.KindServiceTask, Action: "c"},
			{ID: "ea", Kind: model.KindEndEvent},
			{ID: "eb", Kind: model.KindEndEvent},
			{ID: "ec", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "or", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "or", Target: "tc", IsDefault: true},
			{ID: "f5", Source: "ta", Target: "ea"},
			{ID: "f6", Source: "tb", Target: "eb"},
			{ID: "f7", Source: "tc", Target: "ec"},
		},
	}
}

func actionNames(cmds []engine.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			out = append(out, ia.Name)
		}
	}
	return out
}

func TestInclusiveForkTakesAllTrueBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, actionNames(res.Commands))
	require.Len(t, res.State.Tokens, 2)
}

func TestInclusiveForkSingleTrueBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 0}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a"}, actionNames(res.Commands))
}

func TestInclusiveForkFallsBackToDefault(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 0, "b": 0}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"c"}, actionNames(res.Commands))
}

func TestInclusiveForkNoMatchNoDefaultErrors(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "or", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "or", Kind: model.KindInclusiveGateway},
			{ID: "ta", Kind: model.KindServiceTask, Action: "a"},
			{ID: "ea", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "ta", Target: "ea"},
		},
	}
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 0}), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrNoMatchingFlow)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestInclusiveFork -v
```
Expected: FAIL — `KindInclusiveGateway` is handled by `default` (token parked; no `InvokeAction` commands).

- [ ] **Step 3: Modify `engine/step.go` — add the inclusive case + forkInclusive**

1. Add the inclusive case to the `switch node.Kind` in `drive`, before `default:`:
```go
		case model.KindInclusiveGateway:
			if len(def.Incoming(node.ID)) > 1 {
				s.tryInclusiveJoin(def, tok, node, at) // implemented in Task 2
			} else {
				if err := s.forkInclusive(def, tok, node, at); err != nil {
					return cmds, err
				}
			}
```

> If Task 2 has not been implemented yet, temporarily omit the `if/else` and call `forkInclusive` directly; Task 2 reintroduces the split. (If implementing both tasks in one pass, write the `if/else` as shown.)

2. Add `forkInclusive` next to the other `InstanceState` helpers:
```go
// forkInclusive consumes the incoming token and creates an Active token for every
// non-default outgoing flow whose condition is empty or true (definition order).
// If none are true it takes the default flow; if none are true and there is no
// default it returns ErrNoMatchingFlow.
func (s *InstanceState) forkInclusive(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) error {
	var taken []model.SequenceFlow
	var dflt *model.SequenceFlow
	for _, f := range def.Outgoing(node.ID) {
		if f.IsDefault {
			ff := f
			dflt = &ff
			continue
		}
		if f.Condition == "" {
			taken = append(taken, f)
			continue
		}
		ok, err := conditions.EvalBool(f.Condition, s.Variables)
		if err != nil {
			return fmt.Errorf("engine: gateway %q flow %q: %w", node.ID, f.ID, err)
		}
		if ok {
			taken = append(taken, f)
		}
	}
	if len(taken) == 0 {
		if dflt == nil {
			return fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID)
		}
		taken = append(taken, *dflt)
	}
	s.consumeToken(tok, at)
	for _, f := range taken {
		s.placeToken(f.Target, at)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./engine/... -v
```
Expected: PASS (the four inclusive-fork tests + all earlier engine tests).

- [ ] **Step 5: Commit**

```bash
git add engine/step.go engine/step_inclusive_test.go
git commit -m "feat(engine): inclusive gateway fork (diverging, all true branches)"
```

---

### Task 2: Inclusive (OR) gateway — converging join (reachable-incoming sync)

**Files:**
- Modify: `engine/step.go`
- Modify: `engine/step_inclusive_test.go`
- Modify: `runtime/example_test.go`

**Interfaces:**
- Produces: helper `(*InstanceState).tryInclusiveJoin(def, *Token, node, at)` and the pure graph helper `nodesThatCanReach(def *model.ProcessDefinition, target string) map[string]bool`. An OR-join parks the arriving token as `TokenAtJoin`; it fires only when **no token other than those already parked at the join can still reach the join** (reachability over the flow graph). On firing it consumes all tokens parked at the join and creates one Active token per outgoing flow. (Ensure the Task-1 `if len(def.Incoming(node.ID)) > 1` branch calls `tryInclusiveJoin`.)

- [ ] **Step 1: Write the failing tests (add to `engine/step_inclusive_test.go`)**

```go
// orDiamondDef: start -> orsplit -{a>0}-> ta ; -{b>0}-> tb ; -{c>0}-> tc ;
//               ta,tb,tc -> orjoin -> end.  (No default: at least one must be true.)
func orDiamondDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "ord", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "orsplit", Kind: model.KindInclusiveGateway},
			{ID: "ta", Kind: model.KindServiceTask, Action: "a"},
			{ID: "tb", Kind: model.KindServiceTask, Action: "b"},
			{ID: "tc", Kind: model.KindServiceTask, Action: "c"},
			{ID: "orjoin", Kind: model.KindInclusiveGateway},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "orsplit"},
			{ID: "f2", Source: "orsplit", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "orsplit", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "orsplit", Target: "tc", Condition: "c > 0"},
			{ID: "f5", Source: "ta", Target: "orjoin"},
			{ID: "f6", Source: "tb", Target: "orjoin"},
			{ID: "f7", Source: "tc", Target: "orjoin"},
			{ID: "f8", Source: "orjoin", Target: "end"},
		},
	}
}

// Two of three branches active (a,b true; c false). The OR-join must fire after
// a and b complete and must NOT wait for c, which never received a token.
func TestInclusiveJoinDoesNotWaitForUntakenBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := orDiamondDef()

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1, "c": 0}), engine.StepOptions{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a", "b"}, actionNames(r0.Commands))
	cmds := map[string]string{} // action name -> commandID
	for _, c := range r0.Commands {
		ia := c.(engine.InvokeAction)
		cmds[ia.Name] = ia.CommandID
	}

	// Complete a: token waits at the join; b can still reach it, so no firing.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cmds["a"], nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r1.Commands)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Complete b: now no token (other than those at the join) can reach the join,
	// so it fires, reaches end, and the instance completes — without waiting for c.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmds["b"], nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	_, ok := r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
}

func TestNodesThatCanReachIsAccurate(t *testing.T) {
	// White-box-ish behavioral check via a single-branch OR-join that should fire
	// immediately once its only active branch arrives.
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := orDiamondDef()
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 0, "c": 0}), engine.StepOptions{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a"}, actionNames(r0.Commands))
	idA := r0.Commands[0].(engine.InvokeAction).CommandID

	// Only branch a is active; completing it must fire the join immediately.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), idA, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	_, ok := r1.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r1.State.Status)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run 'TestInclusiveJoin|TestNodesThatCanReach' -v
```
Expected: FAIL — without `tryInclusiveJoin`, the >1-incoming inclusive gateway calls `forkInclusive`, so the first arriving token forks straight to `end` and the instance completes after the first branch (wrong), or token bookkeeping is off.

- [ ] **Step 3: Modify `engine/step.go` — add tryInclusiveJoin + nodesThatCanReach**

1. Ensure the inclusive case in `drive` routes >1-incoming to the join (from Task 1):
```go
		case model.KindInclusiveGateway:
			if len(def.Incoming(node.ID)) > 1 {
				s.tryInclusiveJoin(def, tok, node, at)
			} else {
				if err := s.forkInclusive(def, tok, node, at); err != nil {
					return cmds, err
				}
			}
```

2. Add the join helper and the reachability helper next to the other helpers:
```go
// tryInclusiveJoin parks the arriving token at an OR-join and fires only once no
// token other than those already parked at the join can still reach it (so it
// never waits for branches that were never activated). On firing it consumes all
// tokens parked at the join and creates one Active token per outgoing flow.
func (s *InstanceState) tryInclusiveJoin(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) {
	tok.State = TokenAtJoin

	canReach := nodesThatCanReach(def, node.ID)
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.NodeID == node.ID && t.State == TokenAtJoin {
			continue // already arrived at the join
		}
		if canReach[t.NodeID] {
			return // some token can still reach the join; keep waiting
		}
	}

	// Fire: consume all tokens parked at this join, then fork to outgoing flows.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID && t.State == TokenAtJoin {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID) {
		s.placeToken(f.Target, at)
	}
}

// nodesThatCanReach returns the set of node IDs (excluding target) from which
// target is reachable by following sequence flows forward. Implemented as a
// reverse breadth-first search from target over incoming flows; the visited guard
// makes it safe on graphs with cycles that do not pass through target.
func nodesThatCanReach(def *model.ProcessDefinition, target string) map[string]bool {
	canReach := make(map[string]bool)
	var queue []string
	enqueue := func(n string) {
		if n != target && !canReach[n] {
			canReach[n] = true
			queue = append(queue, n)
		}
	}
	for _, f := range def.Incoming(target) {
		enqueue(f.Source)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, f := range def.Incoming(n) {
			enqueue(f.Source)
		}
	}
	return canReach
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./engine/... -v
```
Expected: PASS (OR-join tests + all earlier engine tests).

- [ ] **Step 5: Add an end-to-end inclusive flow through the runtime**

Add to `runtime/example_test.go`:
```go
func TestRunnerExecutesInclusiveTwoOfThree(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "ord", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "orsplit", Kind: model.KindInclusiveGateway},
			{ID: "ta", Kind: model.KindServiceTask, Action: "a"},
			{ID: "tb", Kind: model.KindServiceTask, Action: "b"},
			{ID: "tc", Kind: model.KindServiceTask, Action: "c"},
			{ID: "orjoin", Kind: model.KindInclusiveGateway},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "orsplit"},
			{ID: "f2", Source: "orsplit", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "orsplit", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "orsplit", Target: "tc", Condition: "c > 0"},
			{ID: "f5", Source: "ta", Target: "orjoin"},
			{ID: "f6", Source: "tb", Target: "orjoin"},
			{ID: "f7", Source: "tc", Target: "orjoin"},
			{ID: "f8", Source: "orjoin", Target: "end"},
		},
	}
	mk := func(key string) action.ServiceAction {
		return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{key: true}, nil
		})
	}
	cat := action.NewMapCatalog(map[string]action.ServiceAction{"a": mk("ra"), "b": mk("rb"), "c": mk("rc")})
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), runtime.NewMemJournal(), runtime.NewMemOutbox())

	final, err := r.Run(t.Context(), def, "i1", map[string]any{"a": 1, "b": 1, "c": 0})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Empty(t, final.Tokens)
	assert.Equal(t, true, final.Variables["ra"])
	assert.Equal(t, true, final.Variables["rb"])
	_, ranC := final.Variables["rc"]
	assert.False(t, ranC, "branch c must not run when its condition is false")
}
```

- [ ] **Step 6: Run the full suite + coverage + lint**

Run:
```bash
go test -race ./...
go test -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: all pass; total coverage ≥ 90%; lint clean. (`cover.out` is gitignored — do not commit it.)

- [ ] **Step 7: Commit**

```bash
git add engine/step.go engine/step_inclusive_test.go runtime/example_test.go
git commit -m "feat(engine): inclusive OR-join with reachable-incoming synchronization"
```

---

## Verification Checklist (Plan 3)

- [ ] OR-fork activates every true branch, a single true branch, or the default when none are true; errors (`ErrNoMatchingFlow`) when none are true and there is no default.
- [ ] OR-join fires after exactly the activated branches arrive and does **not** wait for branches that never received a token (`TestInclusiveJoinDoesNotWaitForUntakenBranch`).
- [ ] A single-active-branch OR-join fires immediately on that branch's arrival.
- [ ] End-to-end 2-of-3 inclusive flow completes once, merges both active branches' outputs, and never runs the false branch.
- [ ] `nodesThatCanReach` terminates on graphs with cycles not through the target (visited guard) — no infinite loop.
- [ ] `Step` remains deterministic and pure (Plan-1/2 invariants still pass; flows evaluated in definition order).
- [ ] `engine`/`model`/`expreval` import no transport/storage/bus/time-vendor packages; engine never calls `time.Now()`.
- [ ] `go test -race ./...` green; coverage ≥ 85% on touched packages; `golangci-lint run ./...` clean.

## Self-Review Notes

- **Spec coverage (this slice):** §6 inclusive gateway semantics — OR-fork (all true / default / no-match error) and OR-join via reachable-incoming analysis (design §6 names exactly this). Event-based gateway deferred to Plan 5 (needs catch events).
- **OR-join correctness:** firing on "no other token can still reach the join" is the standard BPMN OR-join semantics and a safe over-approximation — a reachable token *might* arrive, so the join waits; an unreachable/never-activated branch (like `tc` when `c <= 0`) never blocks because no token sits on a node that can reach the join. The single-branch test pins immediate firing; the 2-of-3 test pins both the wait (after `a`) and the fire (after `b`).
- **Determinism & purity:** `nodesThatCanReach` is a pure function of the definition; `def.Outgoing`/`Incoming` keep definition order; forked/merged tokens use counter-derived IDs via `placeToken`; all mutation is on the cloned `*InstanceState`. The shared `conditions` evaluator only memoizes.
- **Acknowledged limitation:** like the Plan-2 parallel join, this targets non-nested, acyclic OR regions (Plan 3 scope). Loops that re-enter an OR-join and nested OR regions are handled when scopes/loops land in a later plan; `nodesThatCanReach`'s reverse-BFS already tolerates unrelated cycles without looping.
- **Type consistency:** reuses Plan-2 `ErrNoMatchingFlow`, `conditions`, `consumeToken`, `placeToken`, `closeVisit`; adds no public API beyond the two unexported helpers; `Step`/`drive` signatures unchanged from Plan 2.
