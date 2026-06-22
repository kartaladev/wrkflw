# Reachability + conservative fork-join validation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Honour strict TDD (CLAUDE.md): a visible RED (`go test` failing/not-compiling) must precede every implementation, in its own Bash call.

**Goal:** Add two structural-soundness rules to `model.Validate` — `ErrUnreachableNode` (start-reachability with boundary-host + event-sub-process seeding) and `ErrUnpairedJoin` (conservative parallel-join-only pairing) — biased to never produce false positives.

**Architecture:** Pure `model/` validation; a model-local forward-BFS helper (no engine import). Both rules live inside the recursive `validate()` worker so they apply to sub-processes automatically and surface through the existing `errors.Join`. Engine untouched.

**Tech Stack:** Go 1.25, stdlib `errors`/`fmt` only, testify (black-box `model_test`).

## Global Constraints

- Module path `github.com/zakyalvan/krtlwrkflw`; no `pkg/` prefix (ADR-0004).
- **Strict TDD**: RED before GREEN, observable as a separate `go test` Bash call.
- **`model` must not import `engine`** (engine imports model). New code uses only stdlib + existing model helpers (`Outgoing`/`Incoming`/`Node`/`StartNodes`).
- **Engine production diff: ZERO.** Only `model/validate.go` (+ its test) change.
- New error sentinels carry the **`workflow-`** prefix (ADR-0026); wrap with the node id; assert via `errors.Is`.
- Both rules are **false-positive-averse** (bias to false-negatives) per ADR-0030.
- **Key fact:** `model.Validate` has **no production callers** (engine assumes pre-validated input; no test outside `model/validate_test.go` calls it). Blast radius is the `model` test fixtures only.
- Gate: `go test -race ./model/...` green, `model` ≥85% coverage, `golangci-lint run ./...` clean, full `go test ./...` green.

## File Structure

- `model/validate.go` (**modify**) — 2 new sentinels, the `forwardReachable` + `hasConcurrencySource` helpers, and 2 new rule blocks inside `validate()`.
- `model/validate_test.go` (**modify**) — new table cases; fix the latently-unsound "pure join gateway is valid" fixture.

---

### Task 1: reachability (`ErrUnreachableNode`)

**Files:**
- Modify: `model/validate.go`
- Test: `model/validate_test.go`

**Interfaces:**
- Consumes: `ProcessDefinition.{Outgoing,Incoming,Node,StartNodes}`, `NodeKind` constants (`KindBoundaryEvent`, `KindEventSubProcess`), `Node.AttachedTo`.
- Produces: `var ErrUnreachableNode`; unexported `forwardReachable(d *ProcessDefinition, seed string) map[string]bool`.

- [ ] **Step 1: Write failing tests** — add to the `TestValidate` table in `model/validate_test.go`:

```go
		"unreachable orphan node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "orphan", Kind: model.KindServiceTask, Action: "o"},
					{ID: "orphan-end", Kind: model.KindEndEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "orphan", Target: "orphan-end"}, // orphan unreachable from start
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
		"node reachable via boundary on reachable host is valid": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "bnd", Kind: model.KindBoundaryEvent, AttachedTo: "task", TimerDuration: "PT1M"},
					{ID: "handler", Kind: model.KindServiceTask, Action: "h"},
					{ID: "hend", Kind: model.KindEndEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "bnd", Target: "handler"}, // reachable only via boundary
					{ID: "f4", Source: "handler", Target: "hend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"node reachable only via boundary on unreachable host is unreachable": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "end", Kind: model.KindEndEvent},
					{ID: "ghost", Kind: model.KindServiceTask, Action: "g"}, // unreachable host
					{ID: "bnd", Kind: model.KindBoundaryEvent, AttachedTo: "ghost", TimerDuration: "PT1M"},
					{ID: "handler", Kind: model.KindServiceTask, Action: "h"},
					{ID: "hend", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "ghost", Target: "end"},
					{ID: "f4", Source: "bnd", Target: "handler"},
					{ID: "f5", Source: "handler", Target: "hend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
		"zero start events does not run reachability": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{{ID: "end", Kind: model.KindEndEvent}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrNoStartEvent)
				require.NotErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./model/ -run 'TestValidate$'`
Expected: FAIL — build error `undefined: model.ErrUnreachableNode`.

- [ ] **Step 3: Implement** — in `model/validate.go`:

Add the sentinel near the others (after `ErrEmptyCancelAction`):
```go
// ErrUnreachableNode is returned when a node cannot be reached from the start
// event — directly via sequence flows, or via a reachable boundary event or an
// event-sub-process (an event-triggered root). It signals dead/orphan structure.
var ErrUnreachableNode = errors.New("workflow-model: unreachable node")
```

Add the helper (unexported, file-level):
```go
// forwardReachable returns the set of node IDs reachable from seed by following
// outgoing sequence flows (BFS, cycle-safe via the visited set). seed is included.
func forwardReachable(d *ProcessDefinition, seed string) map[string]bool {
	reached := map[string]bool{seed: true}
	queue := []string{seed}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, f := range d.Outgoing(n) {
			if !reached[f.Target] {
				reached[f.Target] = true
				queue = append(queue, f.Target)
			}
		}
	}
	return reached
}
```

Add the reachability rule inside `validate()`, after the mixed-gateway block (~line 184) and before the boundary block. Compute `reached` here and keep it in scope for Task 2:
```go
	// Reachability (ErrUnreachableNode). Runs only with exactly one start event;
	// with 0 or >1 starts the start-count error already fires and reachability is
	// ill-defined, so we skip to avoid cascade noise. Boundary events have no
	// incoming flow (reachable iff their host is reachable, to a fixpoint) and
	// event-sub-processes are event-triggered roots.
	var reached map[string]bool
	if starts := d.StartNodes(); len(starts) == 1 {
		reached = forwardReachable(d, starts[0].ID)
		for _, n := range d.Nodes {
			if n.Kind == KindEventSubProcess {
				for id := range forwardReachable(d, n.ID) {
					reached[id] = true
				}
			}
		}
		for {
			grew := false
			for _, n := range d.Nodes {
				if n.Kind != KindBoundaryEvent || reached[n.ID] || !reached[n.AttachedTo] {
					continue
				}
				for id := range forwardReachable(d, n.ID) {
					if !reached[id] {
						reached[id] = true
						grew = true
					}
				}
			}
			if !grew {
				break
			}
		}
		for _, n := range d.Nodes {
			if !reached[n.ID] {
				errs = append(errs, fmt.Errorf("%w: node %q", ErrUnreachableNode, n.ID))
			}
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./model/ -run 'TestValidate$'`
Expected: PASS. (If an existing valid fixture with a boundary/event-based gateway trips, inspect: a legitimately-reachable node must pass; fix the rule's seeding, not the fixture, unless the fixture is genuinely unsound.)

- [ ] **Step 5: Commit**

```bash
git add model/validate.go model/validate_test.go
git commit -m "feat(model): reject unreachable nodes in Validate (ErrUnreachableNode)"
```

---

### Task 2: conservative parallel-join pairing (`ErrUnpairedJoin`)

**Files:**
- Modify: `model/validate.go`
- Test: `model/validate_test.go`

**Interfaces:**
- Consumes: `reached` (from Task 1), `forwardReachable`, `KindParallelGateway`, `KindInclusiveGateway`.
- Produces: `var ErrUnpairedJoin`; unexported `hasConcurrencySource(d *ProcessDefinition, joinID string) bool`.

- [ ] **Step 1: Fix the latently-unsound existing fixture.** The current "pure join gateway is valid" case wires `start → a, b → parallel-join`, but a start event follows only its **first** outgoing flow (`moveAlongSingleFlow` takes `out[0]`), so `b` is never activated and the join would deadlock — it is correctly flagged by the new rule. Give it a real parallel fork so it is genuinely paired. Replace that case's body with:

```go
		"pure join gateway is valid": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "fork"},
					{ID: "f1", Source: "fork", Target: "a"},
					{ID: "f2", Source: "fork", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
```

- [ ] **Step 2: Write failing tests** — add to the `TestValidate` table:

```go
		"parallel join fed by exclusive split is unpaired": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnpairedJoin)
			},
		},
		"parallel join fed by inclusive split is paired": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindInclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"inclusive join fed by exclusive split is not flagged (rule is parallel-only)": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindInclusiveGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrUnpairedJoin)
			},
		},
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./model/ -run 'TestValidate$'`
Expected: FAIL — build error `undefined: model.ErrUnpairedJoin`.

- [ ] **Step 4: Implement** — in `model/validate.go`:

Add the sentinel:
```go
// ErrUnpairedJoin is returned when a parallel join gateway has no concurrency
// source — no parallel/inclusive split can deliver two concurrent tokens toward
// it — so it would deadlock at runtime waiting for branches that never arrive.
var ErrUnpairedJoin = errors.New("workflow-model: unpaired parallel join")
```

Add the helper:
```go
// hasConcurrencySource reports whether some parallel or inclusive split (a
// gateway with >1 outgoing flow) has at least two distinct outgoing branches
// whose targets can each forward-reach joinID. Only parallel/inclusive splits
// create concurrency; exclusive and event-based splits take a single branch.
func hasConcurrencySource(d *ProcessDefinition, joinID string) bool {
	for _, f := range d.Nodes {
		if f.ID == joinID {
			continue
		}
		if f.Kind != KindParallelGateway && f.Kind != KindInclusiveGateway {
			continue
		}
		out := d.Outgoing(f.ID)
		if len(out) <= 1 {
			continue // a join/pass-through, not a split
		}
		count := 0
		for _, b := range out {
			if forwardReachable(d, b.Target)[joinID] {
				count++
				if count >= 2 {
					return true
				}
			}
		}
	}
	return false
}
```

Add the rule inside `validate()`, right after the reachability block (so `reached` is in scope):
```go
	// Parallel-join pairing (ErrUnpairedJoin). Only KindParallelGateway joins can
	// deadlock: they wait for a token on every incoming flow unconditionally.
	// Exclusive/event-based joins fire on first arrival, and inclusive joins
	// self-adjust via runtime reachability — none deadlock, so they are excluded.
	// A parallel join is flagged iff no parallel/inclusive split can deliver two
	// concurrent tokens toward it (a provable deadlock). Conservative: any plausible
	// concurrency source clears the join (favouring no false positives). Unreachable
	// joins are skipped — ErrUnreachableNode already reports them.
	for _, n := range d.Nodes {
		if n.Kind != KindParallelGateway {
			continue
		}
		if len(d.Incoming(n.ID)) <= 1 || len(d.Outgoing(n.ID)) != 1 {
			continue // not a pure parallel join (mixed already rejected; split is fine)
		}
		if reached != nil && !reached[n.ID] {
			continue
		}
		if !hasConcurrencySource(d, n.ID) {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrUnpairedJoin, n.ID))
		}
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./model/ -run 'TestValidate$'`
Expected: PASS (including the fixed "pure join gateway is valid" and the existing "pure split gateway is valid").

- [ ] **Step 6: Run the full model package + lint + full suite**

Run:
```bash
go test -race -coverprofile=cover.out ./model/... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
go test ./... 2>&1 | grep -vE "^ok |no test files" || echo "all green"
```
Expected: model ≥85%, lint 0 issues, full suite green. (No production caller of `model.Validate` exists, so engine/runtime/example tests are unaffected; if any other `model_test` valid fixture trips, inspect it — fix the fixture only if it is genuinely unsound, otherwise the rule.)

- [ ] **Step 7: Commit**

```bash
git add model/validate.go model/validate_test.go
git commit -m "feat(model): reject deadlocking parallel joins in Validate (ErrUnpairedJoin)"
```

---

## Verification Checklist (after all tasks)

- [ ] `go test -race ./model/...` green; `model` coverage ≥85%.
- [ ] `go test ./...` green (no regressions; recall `Validate` has no production callers).
- [ ] `golangci-lint run ./...` clean.
- [ ] **Engine production diff is ZERO**: `git diff --stat main -- engine/` shows nothing.
- [ ] `model` imports only stdlib `errors`/`fmt` (no engine import) — `go list -deps` unchanged.
- [ ] Opus whole-branch review (requesting-code-review); address blockers.
- [ ] Update `docs/plans/HANDOVER.md` (mark complete, prune backlog) + cross-session memory.

## Spec coverage self-check

- Spec §2 (model-local forwardReachable helper) → Task 1. ✓
- Spec §3 (reachability + boundary/event-subprocess seeding, single-start guard) → Task 1. ✓
- Spec §4 (parallel-join-only conservative pairing) → Task 2. ✓
- Spec §5 (placement inside validate(), recursion, joined errors, sentinels) → Tasks 1+2. ✓
- Spec §6 (test matrix) → cases in Tasks 1+2. ✓
- Spec §7 (stricter Validate; fix latent fixture) → Task 2 Step 1. ✓
