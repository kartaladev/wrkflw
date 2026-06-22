# Small API completeness — Implementation Plan

> Executed via superpowers:subagent-driven-development. Strict TDD. Non-engine.

**Goal:** (B) `DefinitionRegistry.Lookup(ctx, defRef)`; (A) opt-in admin-list total-count (REST `?total=true`, gRPC `include_total`). `ended_at`-optional confirmed already-done. Engine/model untouched.

## Global Constraints
- Module `github.com/zakyalvan/krtlwrkflw`; no `pkg/` prefix.
- Strict TDD; RED before GREEN.
- Engine/model production diff ZERO.
- `workflow-` error prefix; black-box tests; table-test assert-closure; `t.Context()`.
- Backward-compat where stated: `IncludeTotal=false` ⇒ exact current list behaviour (no extra query).
- Proto regen reproducible (`go generate ./transport/grpc/...`).
- Gate: `go test -race -p 1 ./...` green; ≥85% touched pkgs; lint clean.
- Spec: docs/specs/2026-06-23-small-api-completeness-design.md. ADR-0038.

## File Structure
- `runtime/definition_registry.go`, `runtime/caching_definition_registry.go` (**modify**) — Lookup ctx.
- `internal/persistence/postgres/definitions.go` (**modify**) — Lookup ctx.
- `runtime/runner.go`, `runtime/call_notifier.go`, `service/service.go` (**modify**) — call sites.
- `runtime/lister.go`, `runtime/memstore.go`, `internal/persistence/postgres/lister.go` (**modify**) — total-count.
- `transport/rest/admin.go`, `transport/grpc/proto/workflow.proto` (+regen), `transport/grpc/server.go` (**modify**).
- tests across the above.

---

### Task 1: DefinitionRegistry.Lookup ctx (mechanical, all sites)

**Files:** modify `runtime/definition_registry.go`, `runtime/caching_definition_registry.go`, `internal/persistence/postgres/definitions.go`, `runtime/runner.go`, `runtime/call_notifier.go`, `service/service.go` + their tests.

**Context:** Port `runtime.DefinitionRegistry.Lookup(defRef string) (*model.ProcessDefinition, error)` (definition_registry.go:21-22). Impls: `MapDefinitionRegistry.Lookup` (:51), `CachingDefinitionRegistry.Lookup` (caching_definition_registry.go:63, calls `backing.Lookup` at :91), Postgres `DefinitionStore.Lookup` (definitions.go:102, uses context.Background()). Callers: runner.go:615/805/996, call_notifier.go:114, service.go:129/175/272. Each caller has a `ctx` in scope.

**Steps (TDD):**
1. Update the port + all impls + all call sites to `Lookup(ctx context.Context, defRef string)`. Postgres impl uses the passed `ctx` (drop `context.Background()`). Map/Caching ignore ctx for the lookup logic (Caching passes it to `backing.Lookup`). Update the test doubles/mocks (search for test `DefinitionRegistry`/`Lookup` implementations). Compile: this is the RED (signature change → build errors across the tree) → fix all sites → GREEN.
2. Add a focused test: Postgres `DefinitionStore.Lookup(cancelledCtx, ref)` returns a context error (or at least uses the ctx — assert it propagates: a cancelled ctx → query fails with ctx.Err()). Mem registry: a normal Lookup still works with `t.Context()`.
3. `go test -race -p 1 ./...` green (the whole tree, since the signature touches many pkgs); lint clean. Commit `refactor(runtime): thread ctx through DefinitionRegistry.Lookup`.

---

### Task 2: admin-list total-count (runtime → service → REST → gRPC)

**Files:** modify `runtime/lister.go` (InstanceFilter+InstancePage), `runtime/memstore.go` (List), `internal/persistence/postgres/lister.go` (List), `service/service.go` (passthrough — likely none), `transport/rest/admin.go` (handleAdminListInstances + response), `transport/grpc/proto/workflow.proto` (+regen), `transport/grpc/server.go` (ListInstances handler) + tests.

**Context:** `InstanceFilter{Status, Limit, Cursor}` and `InstancePage{Items, NextCursor, HasMore}` (lister.go:101). `MemStore.List` (memstore.go:~194-222). Postgres `lister.go` List. REST `handleAdminListInstances` (transport/rest/admin.go) builds `adminListResponse{Items, NextCursor, HasMore}`. gRPC `ListInstances` (server.go:~229) builds `ListInstancesResponse{Items, NextCursor, HasMore}`; `ListInstancesRequest{status,limit,cursor}` + `ListInstancesResponse` in proto.

**Steps (TDD):**
1. `InstanceFilter` gains `IncludeTotal bool`; `InstancePage` gains `TotalCount int` (godoc: meaningful only when IncludeTotal set). Mem `List`: when `filter.IncludeTotal`, set `TotalCount` = count of instances matching the status filter (independent of Limit/Cursor). Postgres `List`: when `filter.IncludeTotal`, run `SELECT count(*) FROM wrkflw_instances WHERE <same status predicate as the list>` and set TotalCount. RED first (field undefined in tests).
2. REST `handleAdminListInstances`: parse `?total=true` (or `total=1`) → `filter.IncludeTotal=true`; add `TotalCount int json:"total_count"` to `adminListResponse` and populate from the page.
3. gRPC: proto `ListInstancesRequest` += `bool include_total = 4;` and `ListInstancesResponse` += `int64 total_count = 4;`. Regen. `ListInstances` handler: `filter.IncludeTotal = req.GetIncludeTotal()`; set `resp.TotalCount = int64(page.TotalCount)`.
4. Tests: mem lister (IncludeTotal true→correct total independent of limit; false→0); postgres lister (testcontainers, status-filtered count independent of limit); REST (`?total=true`→total_count in body; absent→0); gRPC (include_total→total_count). RED→GREEN per layer. Proto regen reproducible.
5. `go test -race -p 1 ./...` green; lint clean. Commit `feat(runtime,transport): opt-in admin-list total-count`.

---

### Task 3: docs + gate (controller)
ADR-0038 written. Controller verifies, updates HANDOVER + memory, full gate, whole-branch review, merge.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% touched pkgs.
- [ ] `golangci-lint run ./...` clean; engine/model diff ZERO.
- [ ] `IncludeTotal=false` path unchanged (existing list tests green); proto regen reproducible.
- [ ] Whole-branch review; merge + push; HANDOVER + memory.

## Spec coverage self-check
- §0 ended_at (already done, no task). §2 total-count → Task 2. §3 Lookup ctx → Task 1. §4 tests → per-task. ✓
