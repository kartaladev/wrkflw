# engine/step.go Decomposition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decompose the 3251-line `engine/step.go` into cohesive files: a node-kind **strategy registry**, extracted **trigger handlers**, and collaborator files for the cross-cutting algorithms — a behavior-preserving pure refactor, sequenced **before** the Node-interface redesign (②).

**Architecture:** `Step()` stays a thin trigger type-switch delegating to `handleXxx` funcs; `drive()` becomes a thin dispatcher over `map[model.NodeKind]nodeStrategy`; algorithms move to `step_*.go` files in the same `engine` package. A `stepCtx` param-object carries the repeated inputs to the new dispatch layer; relocated helpers keep their current signatures.

**Tech Stack:** Go 1.25; no new dependencies. Gate is the existing `engine` test suite (some tests need Postgres via testcontainers/Docker; the pure-unit engine tests do not).

## Global Constraints

- Go 1.25; single `go.mod`; module `github.com/zakyalvan/krtlwrkflw`; flat root layout (ADR-0004).
- **Pure refactor:** no behavior change, no change to `Step`'s `(StepResult, error)` contract or any exported signature. Per CLAUDE.md, pure refactors need no new behavioral tests, but the existing suite MUST be green before AND after every task. The single *new* test is the strategy-registry completeness test (TDD'd in Task 2).
- **ADR-0002 purity:** `Step` stays a pure function — no I/O, no clock reads (time arrives as `at time.Time`), no goroutines. Strategies are **stateless zero-size structs**; the registry is built once at package init and never mutated.
- **Preserve the non-exhaustive fall-through:** the registry holds ONLY kinds with a `drive()` arm today; all other kinds must keep falling through to the existing post-switch logic identically.
- Error sentinels keep the `workflow-engine:` prefix; do not rename.
- Conventional Commits `refactor(engine):`. Ask before committing.
- **ADR-0044** authored in the final task. Executes **before** sub-project ② (Node interface).
- Engine/model import-purity must stay intact (no transport/vendor imports introduced).

## Current `step.go` inventory (move map)

| Destination file | Functions moved (from `step.go`) |
|---|---|
| `step.go` (kept) | `Step` (49) → trigger type-switch; `drive` (764) → registry dispatch; `stepCtx` (new) |
| `step_nodes.go` (new) | `nodeStrategy` interface + registry + 13 per-kind strategies (extracted from `drive`'s arms) |
| `step_triggers.go` (new) | the ~18 trigger handlers extracted from `Step`'s cases |
| `step_gateways.go` (new) | `forkParallel` (1559), `forkInclusive` (1572), `tryParallelJoin` (1617), `tryInclusiveJoin` (1656), `selectExclusiveTarget` (1714), `resolveGatewayWin` (1754), `nodesThatCanReach` (1689) |
| `step_boundaries.go` (new) | `armBoundaries` (2134), `fireBoundaryArm` (2194) |
| `step_eventsubprocess.go` (new) | `armEventSubprocesses` (2287), `fireEventSubprocessArm` (2365) |
| `step_compensation.go` (new) | `compensationRecordsForScope` (2738), `cursorRecords` (2753), `stepCompensateRequested` (2769), `beginCompensation` (2802), `stepCompensationAdvance` (2926), `stepCompensationFinish` (2986) |
| `step_errors.go` (new) | `propagateError` (1873) |
| `step_timers.go` (new) | `handleSLAFired` (2490), `handleReminderFired` (2594), `reinvokeServiceAction` (2674), `handleRetryFired` (2714) |
| `step_state.go` (new) | `defForScope` (729), token/id/visit/var utils (1394–1556), `effectiveRetryPolicy` (3085), `mergeVars` (3098), `copyVars` (3110), `serviceActionInput` (3131), `cloneState` (3140) |

The node-kind arms today (from `drive` at step.go:792): `KindStartEvent`, `KindServiceTask`, `KindUserTask`, `KindIntermediateCatchEvent`, `KindErrorEndEvent`, `KindEndEvent`, `KindSubProcess`, `KindExclusiveGateway`, `KindParallelGateway`, `KindInclusiveGateway`, `KindEventBasedGateway`, `KindCallActivity`, `KindIntermediateThrowEvent` — **13 strategies**. Intentionally NOT arms (must keep falling through): `KindTerminateEndEvent`, `KindBusinessRuleTask`, `KindReceiveTask`, `KindSendTask`, `KindBoundaryEvent`, `KindEventSubProcess`, `KindUnspecified`.

---

### Task 1: Establish the green baseline + extract the pure-move collaborator files

Start with the **zero-risk** moves: relocating cohesive helper groups that don't touch the two dispatch switches. Pure file moves within one package — no signature or behavior change.

**Files:** Create `step_gateways.go`, `step_boundaries.go`, `step_eventsubprocess.go`, `step_compensation.go`, `step_errors.go`, `step_timers.go`, `step_state.go`; shrink `step.go`.

- [ ] **Step 1: Capture the green baseline**

Run: `go test ./engine/ -count=1` (pure-unit engine tests; no Docker needed for these)
Expected: PASS. Record the pass as the "before" state.

- [ ] **Step 2: Move the algorithm helpers, one file at a time**

For each destination file in the move map (gateways, boundaries, eventsubprocess, compensation, errors, timers, state), cut the listed functions from `step.go` and paste them into the new file with the same `package engine` clause and the same imports they need. Do NOT change any signature or body. After EACH file move, run `go build ./engine/` to confirm it still compiles.

- [ ] **Step 3: Verify behavior unchanged**

Run: `go test ./engine/ -count=1`
Expected: PASS, identical to the baseline. (No tests changed; functions only relocated.)

- [ ] **Step 4: Lint**

Run: `golangci-lint run ./engine/...`
Expected: no findings (watch for `goimports` ordering in the new files).

- [ ] **Step 5: Commit**

```bash
git add engine/
git commit -m "refactor(engine): split step.go cross-cutting helpers into topic files (no behavior change)"
```

---

### Task 2: Introduce `stepCtx` + the `nodeStrategy` registry; convert ONE kind as a vertical slice

Prove the dispatch pattern end-to-end on a single kind before migrating the rest. Add the completeness test now (it will fail until all kinds are registered — keep it failing-but-tracked, or assert only the migrated set and tighten in Task 4; see Step 4).

**Files:** Create `step_nodes.go`, `engine/step_nodes_test.go`; modify `step.go` (`drive`).

**Interfaces:**
- Produces: `type stepCtx struct{ def *model.ProcessDefinition; s *InstanceState; at time.Time; mode StepMode; opt StepOptions }`; `type nodeStrategy interface{ enter(c *stepCtx, tok *Token, node model.Node) ([]Command, error) }`; `var nodeStrategies map[model.NodeKind]nodeStrategy`.

- [ ] **Step 1: Write the failing completeness test**

Create `engine/step_nodes_test.go`:

```go
package engine

import "testing"

// arm-bearing kinds today; keep in sync with drive()'s registry.
var armBearingKinds = []NodeKindAlias{} // placeholder; replaced below

func TestNodeStrategyRegistryCoversArmBearingKinds(t *testing.T) {
	want := []nodeKindForTest{
		kindStart, kindServiceTask, kindUserTask, kindIntermediateCatch,
		kindErrorEnd, kindEnd, kindSubProcess, kindExclusive, kindParallel,
		kindInclusive, kindEventBased, kindCallActivity, kindIntermediateThrow,
	}
	for _, k := range want {
		if _, ok := nodeStrategies[k.kind]; !ok {
			t.Errorf("no nodeStrategy registered for %v", k.kind)
		}
	}
}
```

(Use the real `model.Kind*` constants directly — the helper names above are illustrative; write the test as a white-box `package engine` test referencing `model.KindServiceTask` etc. so it can read the unexported `nodeStrategies`.)

- [ ] **Step 2: Run it red**

Run: `go test ./engine/ -run TestNodeStrategyRegistryCovers`
Expected: FAIL — `undefined: nodeStrategies`.

- [ ] **Step 3: Add `stepCtx`, the interface, an empty registry, and the ServiceTask strategy**

In `step_nodes.go`:

```go
package engine

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/model"
)

type stepCtx struct {
	def  *model.ProcessDefinition
	s    *InstanceState
	at   time.Time
	mode StepMode
	opt  StepOptions
}

// nodeStrategy executes node-entry for one NodeKind. Stateless; registered once.
type nodeStrategy interface {
	enter(c *stepCtx, tok *Token, node model.Node) ([]Command, error)
}

var nodeStrategies = map[model.NodeKind]nodeStrategy{
	model.KindServiceTask: serviceTaskStrategy{},
	// remaining kinds added in Task 3
}

type serviceTaskStrategy struct{}

func (serviceTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, error) {
	// EXACT body lifted from drive()'s `case model.KindServiceTask:` arm,
	// with: def→c.def, s→c.s, at→c.at, mode→c.mode, opt→c.opt, and `node` as-is.
	// Return the commands the arm appended for this token (not the whole slice).
}
```

Lift the ServiceTask arm's body verbatim, rewiring the captured locals to `c.*`. The arm currently appends to a shared `cmds` slice; the strategy returns the commands it produced and `drive()` appends them (see Step 4).

- [ ] **Step 4: Convert `drive()` to dispatch ServiceTask via the registry, keep the switch for the rest**

In `drive()`, replace just the ServiceTask arm with a registry call; leave the other `case` arms untouched for now:

```go
node, ok := tdef.Node(tok.NodeID)
if !ok {
	tok.State = TokenWaitingCommand
	continue
}
if strat, ok := nodeStrategies[node.Kind]; ok {
	c := &stepCtx{def: def, s: s, at: at, mode: mode /*, opt: opt if in scope */}
	produced, err := strat.enter(c, tok, node)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, produced...)
	continue
}
switch node.Kind {
// ... the remaining not-yet-migrated arms stay here ...
}
```

Note: `drive`'s current signature is `drive(def, s, at, mode)` — `opt` may not be in scope there; if a strategy needs `opt` (e.g. ServiceTask retry defaults via `effectiveRetryPolicy`), thread `opt` into `drive` or resolve it the same way the arm does today. Match today's data flow exactly. Pre-migration the registry guard must produce **byte-identical** command output and token mutations to the old arm — diff the engine tests to confirm.

Adjust the completeness test (Step 1) to assert only `{KindServiceTask}` for now, with a `// TODO: tighten as kinds migrate (Task 3)` — or keep the full list and accept it red until Task 3. Prefer asserting the migrated-so-far set to keep every task green.

- [ ] **Step 5: Run green**

Run: `go test ./engine/ -count=1`
Expected: PASS (ServiceTask now flows through the registry; all behavior identical).

- [ ] **Step 6: Commit**

```bash
git add engine/
git commit -m "refactor(engine): nodeStrategy registry + stepCtx; migrate ServiceTask arm"
```

---

### Task 3: Migrate the remaining 12 node-kind arms to strategies

One commit per 1–3 strategies; engine tests green after each. Each strategy lifts its arm body verbatim into a zero-size struct and registers it; the arm is removed from `drive()`'s switch.

**Files:** Modify `step_nodes.go`, `step.go`.

- [ ] **Step 1:** For each remaining kind — `KindStartEvent`, `KindEndEvent`, `KindUserTask`, `KindIntermediateCatchEvent`, `KindErrorEndEvent`, `KindSubProcess`, `KindExclusiveGateway`, `KindParallelGateway`, `KindInclusiveGateway`, `KindEventBasedGateway`, `KindCallActivity`, `KindIntermediateThrowEvent` — create `type <kind>Strategy struct{}`, lift the arm body into `enter` (rewiring locals to `c.*`, returning produced commands), register it, and delete the arm from `drive()`'s switch. After each kind (or small batch), run `go test ./engine/ -count=1` and confirm PASS.

- [ ] **Step 2:** When all 13 are migrated, `drive()`'s residual `switch` should contain only the intentionally-unhandled kinds' fall-through (or no switch at all if the fall-through is uniform). Confirm the post-dispatch logic for unhandled kinds (`KindTerminateEndEvent`, `KindBusinessRuleTask`, `KindReceiveTask`, `KindSendTask`, `KindBoundaryEvent`, `KindEventSubProcess`, `KindUnspecified`) is unchanged.

- [ ] **Step 3:** Tighten the completeness test to assert the full 13-kind set (from Task 2 Step 1) and add a second assertion pinning the documented unhandled set so a future stray registration or omission fails.

- [ ] **Step 4:** Run: `go test ./engine/ -count=1` and `golangci-lint run ./engine/...`. Expected: PASS, clean.

- [ ] **Step 5:** Commit per batch, e.g. `refactor(engine): migrate gateway node arms to strategies`, etc.

---

### Task 4: Extract the trigger handlers from `Step()`

`Step()` keeps a thin type-switch; each case body moves to a `handleXxx(c *stepCtx, t <ConcreteTrigger>) (StepResult, error)` in `step_triggers.go`. Pure extraction; no behavior change.

**Files:** Create `step_triggers.go`; modify `step.go` (`Step`).

- [ ] **Step 1:** For each trigger case in `Step()` (StartInstance, ActionCompleted, ActionFailed, CancelRequested, CompensateRequested, TimerFired, HumanClaimed, HumanReassigned, HumanCompleted, SignalReceived, MessageReceived, SubInstanceCompleted, SubInstanceFailed, ResolveIncident), create `handle<Trigger>(c *stepCtx, t <Type>) (StepResult, error)` lifting the case body verbatim (rewire locals to `c.*`). The `Step` case becomes `case T: return handleT(c, v)` where `v` is the type-switched value. Build `c` once near the top of `Step` after `cloneState`. Move one handler per step and run `go test ./engine/ -count=1` after each.

- [ ] **Step 2:** Preserve the `TimerFired` sub-dispatch (`TimerSLA`/`TimerInWait`/`TimerRetry`) inside `handleTimerFired` exactly as today.

- [ ] **Step 3:** Run: `go test ./engine/ -count=1` and `golangci-lint run ./engine/...`. Expected: PASS, clean. `step.go` is now the thin `Step` type-switch + `drive` dispatcher + `stepCtx`.

- [ ] **Step 4:** Commit `refactor(engine): extract Step trigger handlers into step_triggers.go`.

---

### Task 5: Full verification + ADR-0044

**Files:** Create `docs/adr/0044-engine-step-decomposition.md`.

- [ ] **Step 1: Author ADR-0044** (Nygard): Context (3251-line god file; two dispatch switches + cross-cutting machinery; owner chose a strategy registry over a switch for the closed kind-set). Decision (node-kind `map[NodeKind]nodeStrategy` registry of stateless strategies; trigger type-switch → extracted typed handlers; collaborator files; `stepCtx` scoped to the dispatch layer; helpers relocated unchanged). Consequences (smaller focused files; lost compiler-exhaustiveness bought back by the completeness test; sets up ② to change only strategy internals; ADR number 0044 > 0042/0043 but executes before them — chronological-ID note; `stepCtx`-into-helpers deferred).

- [ ] **Step 2: Full gate**

```bash
go test -race -coverprofile=cover.out ./engine/... && go tool cover -func=cover.out | tail -1
go test ./... -count=1   # full suite incl. Postgres (Docker)
golangci-lint run ./...
```
Expected: engine coverage ≥ 85% (should be unchanged from baseline — behavior preserved); full suite green; lint clean.

- [ ] **Step 3: Purity check**

Run: `go list -deps ./engine/ | grep -E 'transport|watermill|gocron|casbin|clockwork' || echo PURE`
Expected: `PURE` (no transport/vendor leaks introduced).

- [ ] **Step 4: Commit** `docs(adr): engine/step.go decomposition via strategy registry (ADR-0044)`.

---

## Verification checklist (whole sub-project)

- [ ] `engine/step.go` is reduced to `Step` (trigger type-switch), `drive` (registry dispatch), and `stepCtx`; algorithms live in `step_*.go` files.
- [ ] `nodeStrategies` registers exactly the 13 arm-bearing kinds; the completeness test passes and pins the unhandled set.
- [ ] Every relocated function is byte-for-byte behavior-identical (no signature/body changes beyond `c.*` rewiring in the extracted dispatch handlers).
- [ ] The existing `engine` test suite passes unchanged; `go test ./... -count=1` green; `golangci-lint run ./...` clean; engine import-purity `PURE`; coverage ≥ 85%.
- [ ] ADR-0044 recorded with the chronological-ID-vs-execution-order note.

## Sequencing

Executes **after** sub-project ① (layout hygiene) and **before** sub-project ②
(Node interface). ②'s `drive()` migration then edits the small per-kind strategy
files instead of the monolith. No dependency on ③ or ④.

## Out of scope

- Threading `stepCtx` into the relocated helper functions (future follow-up).
- Any trigger registry / `Trigger`-interface discriminator.
- Any behavior change, bug fix, or new node/trigger handling.
