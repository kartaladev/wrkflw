# Fluent Builder Node Methods — Implementation Plan

> **For agentic workers:** single cohesive additive change; execute with TDD. Steps use checkbox syntax.

**Goal:** Add 19 fluent `AddX` methods to `model.DefinitionBuilder`, each mirroring its `NewX` constructor and delegating to `Add(NewX(...))`.

**Architecture:** Pure forwarding sugar in a new file `model/builder_fluent.go`; generic `Add` retained; no behavior/validation change. Spec: `docs/specs/2026-06-29-builder-fluent-node-methods-design.md`.

## Global Constraints
- Additive only: do NOT modify `Add`, `Connect`, `Build`, validation, node constructors, or YAML.
- Each `AddX` mirrors the corresponding `NewX` signature EXACTLY (same params incl. the unexported option types) and returns `*DefinitionBuilder`; body is `return b.Add(NewX(<args>))`.
- Naming mirrors constructors: `AddUserTask` (not AddHumanTask). All 19 kinds.
- TDD strict (visible RED→GREEN per the project rules). Black-box `model_test` package. `<file>.go`↔`<file>_test.go` pairing. Table tests use the project `assert`-closure form.
- Verify: `go test -race ./model/...` green, ≥85% coverage on the new file, `golangci-lint run ./model/...` clean, `go build ./...` clean. Run `go test ./...` for no regressions.

## Task 1: Fluent AddX methods + tests

**Files:** Create `model/builder_fluent.go`, `model/builder_fluent_test.go`. Reference (read, don't modify): `model/builder.go` (the `Add` method + chaining), `model/node_constructors.go` & `model/node.go` (the 19 `New*` signatures), `model/builder_test.go` & `model/node_test.go` (test style), `model/accessors.go` (`ActionOf`/`DeadlineOf`/etc. for assertions).

**Methods to add** (exact signatures — mirror constructors; all return `*DefinitionBuilder`):
`AddStartEvent(id string, opts ...startEventOption)`, `AddEndEvent(id string, name ...string)`, `AddTerminateEndEvent(id string, name ...string)`, `AddErrorEndEvent(id, errorCode string, name ...string)`, `AddExclusiveGateway(id string, name ...string)`, `AddParallelGateway(id string, name ...string)`, `AddInclusiveGateway(id string, name ...string)`, `AddEventBasedGateway(id string, name ...string)`, `AddServiceTask(id string, opts ...serviceTaskOption)`, `AddUserTask(id string, roles []string, opts ...userTaskOption)`, `AddReceiveTask(id, messageName string, opts ...receiveTaskOption)`, `AddSendTask(id, messageName string, opts ...sendTaskOption)`, `AddBusinessRuleTask(id string, opts ...businessRuleOption)`, `AddSubProcess(id string, sub *ProcessDefinition, opts ...activityOption)`, `AddCallActivity(id, defRef string, opts ...activityOption)`, `AddEventSubProcess(id string, sub *ProcessDefinition, opts ...eventSubProcessOption)`, `AddIntermediateCatchEvent(id string, opts ...catchOption)`, `AddIntermediateThrowEvent(id string, opts ...throwOption)`, `AddBoundaryEvent(id, attachedTo string, opts ...boundaryOption)`.

Each has a one-line godoc referencing its `New*` constructor.

- [ ] **Step 1 (RED):** Write `builder_fluent_test.go` with: (a) a table test `TestBuilderFluentAddMethods` over each kind — call `AddX(...)`, then `Build()` (wire a minimal valid graph or assert on the builder's nodes pre-Build via Build()+inspect), asserting the added node's `Kind()` and `ID()`; (b) an option-threading check for at least ServiceTask (`WithActionName`→`ActionOf`), UserTask (roles), ReceiveTask/SendTask (messageName); (c) `TestBuilderFluentEquivalentToAdd` — build a multi-node def two ways (`AddX` vs `Add(NewX(...))`) and assert the resulting `[]Node` are structurally equal (same kinds/ids/fields). Save.
- [ ] **Step 2 (RED verify):** `go test ./model/...` → fails to compile (`b.AddStartEvent undefined`, etc.). Capture output.
- [ ] **Step 3 (GREEN):** Write `model/builder_fluent.go` — the 19 forwarding methods. Save.
- [ ] **Step 4 (GREEN verify):** `go test -race ./model/...` → pass.
- [ ] **Step 5:** `golangci-lint run ./model/...` clean; `go build ./...` clean; coverage on `builder_fluent.go` ≥85% (forwarding lines are all hit by the per-kind table).
- [ ] **Step 6 (regression):** `go test ./... ` (or at least the packages importing `model`) green.
- [ ] **Step 7:** Commit `feat(model): fluent per-node-type builder methods (AddStartEvent/AddServiceTask/…)`.

## Optional follow-up (do only if trivially in-scope)
- Update one reference example (e.g. `runtime/example_scoped_action_test.go` or a README snippet) to showcase the fluent form. Keep it additive; do not break existing examples. If it risks churn, skip and note.

## Self-review
- All 19 methods present, signatures match constructors (compile-time forwarding guarantees it).
- Generic `Add` untouched; no constructor/validation/YAML changes.
- Equivalence test proves pure forwarding.
