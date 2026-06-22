# Per-node cancel handlers — Implementation Plan

> Executed via superpowers:subagent-driven-development. STRICT TDD. `Step` pure/deterministic; engine import-purity; one additive model field.

**Goal:** Add `model.Node.CancelHandler`; the engine emits its fire-and-forget `InvokeCancelAction` for each active node on `CancelRequested`, beside `def.CancelActions`. No runtime change, no new command, no migration.

## Global Constraints
- Module `github.com/zakyalvan/krtlwrkflw`; no `pkg/` prefix.
- STRICT TDD, RED before GREEN.
- `Step` PURE + DETERMINISTIC; engine import-purity; no `InstanceState`/`cloneState` change. Model diff = ONE additive field (`Node.CancelHandler string`); no `Validate` rule.
- Reuse the ADR-0028 `InvokeCancelAction` fire-and-forget path (no runtime production change).
- Back-compat: no `CancelHandler` set ⇒ byte-for-behaviour identical (existing cancel tests green).
- `workflow-` prefix; black-box tests; table-test assert-closure; `t.Context()`.
- Gate: `go test -race -p 1 ./...` green; ≥85% engine/runtime/model; `golangci-lint` clean.
- Spec: docs/specs/2026-06-23-cancel-handlers-design.md. ADR-0035.

## File Structure
- `model/definition.go` (**modify**) — `Node.CancelHandler` field.
- `engine/step.go` (**modify**) — emit per-node `InvokeCancelAction` in `CancelRequested`.
- `engine/step_cancel_handlers_test.go` (**create**) — engine cases.
- `runtime/cancel_handler_test.go` (**create**) — e2e.

---

### Task 1: model field + engine emission + engine tests

**Files:** modify `model/definition.go`, `engine/step.go`; create `engine/step_cancel_handlers_test.go`.

**Context:** `model.Node` struct (definition.go) — add `CancelHandler string` with godoc (per spec §2.1). The `CancelRequested` handler in engine/step.go (~line 118): it builds `cancelActionCmds` from `def.CancelActions`, then branches (compensation if `len(s.RootCompensations)>0`, else immediate). `beginCompensation` clears tokens, so per-node handlers MUST be collected from `s.Tokens` BEFORE that branch. `defForScope(def, &s, tok.ScopeID)` resolves a token's scope definition (a token may be in a sub-process). `InvokeCancelAction{Name, Input}` is the ADR-0028 fire-and-forget command (no CommandID). `copyVars(s.Variables)` snapshots variables.

**Steps (TDD):**
1. Write `engine/step_cancel_handlers_test.go` (engine_test black-box; use export_test shims if needed): cases per spec §3 — (a) one active node with CancelHandler → cancel emits its InvokeAction-cancel command (assert an `InvokeCancelAction{Name:<handler>}` present); (b) two parallel active tokens, one with handler one without → exactly one node-cancel command; (c) active node in a sub-process scope with handler → emitted (resolved via defForScope); (d) cancel-with-compensation + node handler → both the node-cancel command and the compensation walk's first InvokeAction present, node-cancel before the walk; (e) no CancelHandler anywhere → identical to today (no extra commands); (f) determinism (same (def,state)⇒same commands). Run RED (CancelHandler field undefined).
2. Add `Node.CancelHandler string` (model/definition.go) with godoc. Run — compile RED resolves; tests still RED (engine doesn't emit yet).
3. Implement in `CancelRequested` (engine/step.go): BEFORE the `len(s.RootCompensations)>0` branch, build `nodeCancelCmds` by iterating `s.Tokens`, resolving `tdef,_ := defForScope(def,&s,tok.ScopeID)` (skip on error — cancel must not fail), and appending `InvokeCancelAction{Name: node.CancelHandler, Input: copyVars(s.Variables)}` for each token whose node has a non-empty `CancelHandler`. Prepend `nodeCancelCmds` to the result in BOTH branches: in the compensation branch `res.Commands = append(append(cancelActionCmds, nodeCancelCmds...), res.Commands...)`; in the immediate branch insert after `cancelActionCmds`. Keep ordering [def.CancelActions…, per-node…, rest…]. Run GREEN.
4. `go test -race ./engine/... ./model/...` green; `golangci-lint ./engine/... ./model/...` clean. Commit `feat(engine,model): per-node cancel handlers (Node.CancelHandler)`.

---

### Task 2: runtime e2e + controller docs/merge

**Files:** create `runtime/cancel_handler_test.go`.

**Steps (TDD):**
1. Write a runtime e2e (MemStore + catalog): a definition with a user task node carrying `CancelHandler:"cleanup"`; run → parks at the user task; a catalog "cleanup" action records it ran. `CancelInstance` → assert "cleanup" ran (best-effort) and instance `StatusTerminated`. Reuses the ADR-0028 InvokeCancelAction runtime path. RED→GREEN (should pass against the Task-1 engine change).
2. Commit `test(runtime): per-node cancel handler e2e`.
3. Controller: ADR-0035 verify, HANDOVER + memory, full gate, whole-branch review, merge.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% engine/runtime/model.
- [ ] `golangci-lint run ./...` clean.
- [ ] `Step` determinism + import-purity; no cloneState change; model diff = one field.
- [ ] No-CancelHandler back-compat: existing cancel + cancel-propagation + compensation-on-cancel tests green.
- [ ] Whole-branch review; merge + push; HANDOVER + memory.

## Spec coverage self-check
- §2.1 model field → Task 1. §2.2 engine emission (scope-aware, both branches, ordering) → Task 1. §2.3 determinism → tests. §3 runtime e2e → Task 2. ✓
