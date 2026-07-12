# Cancellation propagation parent→child — Implementation Plan

> Executed via superpowers:subagent-driven-development. Strict TDD: visible RED before GREEN. Each task ends green + committed.

**Goal:** `runner.CancelInstance` recursively cancels a parent's running async child instances (best-effort), via a new `CallLinkStore.ListRunningChildren`. Engine/model untouched.

**Architecture:** Runtime-layer propagation. New port read method `ListRunningChildren(parentID)` (Mem + Postgres + partial index migration 0007). `runner.CancelInstance` terminates the instance (as today), then — when `WithCallLinks`+`WithDefinitions` are configured — recursively cancels each running child (resolve child def via `store.Load`→registry), best-effort.

**Tech Stack:** Go 1.25, pgx, goose, testcontainers, testify, clockwork (test-only).

## Global Constraints

- Module `github.com/kartaladev/wrkflw`; no `pkg/` prefix.
- **Strict TDD**, RED before GREEN.
- **Engine/model production diff ZERO.** Changes only in `runtime/`, `internal/persistence/postgres/`, `persistence/`.
- `workflow-` error prefix; assert `errors.Is`; black-box tests; table-test assert-closure; `t.Context()`; `clock.Clock` (clockwork test-only).
- **Best-effort propagation:** every propagation error logged via the injected slog logger and swallowed — NEVER fail the parent `CancelInstance` (ADR-0028 contract). Parent terminated FIRST, then children (avoids notifier resume race).
- Backward-compat: with `callLinks==nil` OR `defsReg==nil`, `CancelInstance` behaves exactly as today.
- Postgres tests `-p 1` via `database.RunTestDatabase`.
- Gate: `go test -race -p 1 ./...` green; ≥85% touched pkgs; `golangci-lint` clean.
- Spec: docs/specs/2026-06-22-cancellation-propagation-design.md. ADR-0032.

## File Structure

- `runtime/calllink.go` (**modify**) — add `ListRunningChildren` to the `CallLinkStore` port.
- `runtime/mem_calllink.go` (**modify**) — Mem impl of `ListRunningChildren`.
- `runtime/runner.go` (**modify**) — recursive best-effort propagation in `CancelInstance` + a `propagateCancel` helper.
- `runtime/cancel_propagation_test.go` (**create**) — runtime e2e + mem unit tests.
- `internal/persistence/postgres/migrations/0007_call_link_parent_idx.sql` (**create**).
- `internal/persistence/postgres/call_links.go` (**modify**) — Postgres `ListRunningChildren`.
- `internal/persistence/postgres/call_links_children_test.go` (**create**) — testcontainers tests.

---

### Task 1: port method + Mem impl + runner recursion (runtime)

**Files:** modify `runtime/calllink.go`, `runtime/mem_calllink.go`, `runtime/runner.go`; create `runtime/cancel_propagation_test.go`.

**Context:** `CallLinkStore` port (calllink.go) has `ClaimPending`/`MarkNotified`/`LookupChild`. `MemCallLinkStore` (mem_calllink.go) holds `links map[string]*memLink` (each has `link CallLink`, `terminal bool`, `notified bool`). `runner.CancelInstance(ctx, def, instanceID)` (runner.go ~567) = `Deliver(CancelRequested)`. `r.callLinks` (CallLinkStore), `r.defsReg` (DefinitionRegistry with `Lookup(ref)(*model.ProcessDefinition,error)`), `r.store` (Store with `Load(ctx,id)(InstanceState,version,error)`), `r.obs.tel.Logger` (slog) are available. Read all three before editing. The terminal-step handler at runner.go ~380 already flips a cancelled child's link terminal — rely on it.

**Produces:** `CallLinkStore.ListRunningChildren(ctx, parentInstanceID string) ([]CallLink, error)`; recursion in `CancelInstance`.

**Steps (TDD):**
1. Write `runtime/cancel_propagation_test.go`: (a) parent parks at call activity, async child parks at human task (mirror `TestAsyncCallActivityParentParks` in async_callactivity_test.go) → `CancelInstance(parent)` → parent `StatusTerminated` AND `store.Load(childID)` is `StatusTerminated`; (b) parent→child→grandchild all parked → cancel parent → all three terminated; (c) child def not in registry → `CancelInstance` returns no error (best-effort), parent terminated; (d) mem `ListRunningChildren` unit: returns only running children of a parent, excludes terminal + other-parent. Run → RED (`ListRunningChildren` undefined).
2. Implement: add `ListRunningChildren` to the port; Mem impl (iterate map under mutex; return `!terminal` links whose `link.ParentInstanceID == parentID`, ordered by ChildInstanceID for determinism); a `propagateCancel(ctx, parentID, visited map[string]bool)` helper in runner.go and call it from `CancelInstance` after the parent `Deliver`, gated `if r.callLinks != nil && r.defsReg != nil`. propagateCancel: list running children (log+return on err); for each not-visited child, mark visited, `store.Load`→`defsReg.Lookup`→`CancelInstance(childDef, childID)` recursively, logging+continuing on any error. Run → GREEN. `golangci-lint ./runtime/...`.
3. Commit `feat(runtime): propagate CancelInstance to running child instances`.

---

### Task 2: Postgres ListRunningChildren + migration 0007

**Files:** create `internal/persistence/postgres/migrations/0007_call_link_parent_idx.sql`; modify `internal/persistence/postgres/call_links.go`; create `internal/persistence/postgres/call_links_children_test.go`.

**Context:** `CallLinkStore{pool}` in call_links.go. Mirror the existing scan helpers. Migration format per 0005/0006 (read one).

**Steps (TDD):**
1. Migration `0007_call_link_parent_idx.sql` (goose up/down): Up `CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id) WHERE status = 'running';` Down `DROP INDEX wrkflw_call_links_parent_running_idx;`.
2. Write `call_links_children_test.go` (testcontainers): seed links for parent P (two `running` children, one `completed` child) and a different parent Q (one running child); `ListRunningChildren(ctx, "P")` returns exactly P's two running children (ordered), excludes the completed one and Q's. Run → RED (method undefined).
3. Implement `func (c *CallLinkStore) ListRunningChildren(ctx, parentInstanceID string) ([]runtime.CallLink, error)`: `SELECT child_instance_id, parent_instance_id, parent_command_id, parent_def_id, parent_def_version, depth FROM wrkflw_call_links WHERE parent_instance_id=$1 AND status='running' ORDER BY child_instance_id`, scan into `runtime.CallLink`. `workflow-postgres:` errors. Run → GREEN. Lint.
4. Commit `feat(persistence/postgres): ListRunningChildren + parent index (migration 0007)`.

---

### Task 3: docs + gate (controller)

ADR-0032 already written. Controller verifies it vs implementation, updates `docs/plans/HANDOVER.md` + cross-session memory, runs the full gate, dispatches the whole-branch review, and merges. (No implementer subagent.)

---

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% on runtime, internal/persistence/postgres, persistence.
- [ ] `golangci-lint run ./...` clean.
- [ ] Engine/model production diff ZERO.
- [ ] Backward-compat: existing CancelInstance + async call-activity tests green; no-callLinks path unchanged.
- [ ] Migration 0007 applied by `Migrate`.
- [ ] Whole-branch review; merge + push; HANDOVER + memory updated.

## Spec coverage self-check
- §2.1 ListRunningChildren (port+mem+pg+index) → Tasks 1–2. §2.2 runner recursion (parent-first, best-effort, child-def resolve, visited guard) → Task 1. §2.3 gating → Task 1. §3 correctness → tests. ✓
