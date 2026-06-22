# Design: small API completeness — admin total-count + DefinitionRegistry.Lookup ctx

**Date:** 2026-06-23
**Status:** Approved (user-chosen high-value subset)
**Track:** Backlog (API / feature completeness). Follow-up to ADR-0011/0007.
**ADR:** 0038.

## 0. `ended_at` optional in proto — already satisfied (no change)

The deferred item "`ended_at` optional in proto" is **already correct**: `instanceToProto` /
`summaryToProto` (transport/grpc/server.go) set `EndedAt` only `if st.EndedAt != nil`, and a proto3
message-typed field (`google.protobuf.Timestamp`) is inherently nullable (the getter returns nil when
unset). The REST `InstanceView.EndedAt` is `*time.Time` with `omitempty`. So a still-running instance
already serializes `ended_at` as absent on both transports. Recorded here, closed — no code change.

## 1. Problem & scope

Two real polish items remain:

- **A. Admin list total-count.** The admin instance-list response
  (`runtime.InstancePage{Items, NextCursor, HasMore}` → REST `{items,next_cursor,has_more}` / gRPC
  `ListInstancesResponse`) gives no total count of matching instances, so a UI can't show "page 1 of
  N" / a total. Add an optional total.
- **B. `DefinitionRegistry.Lookup` lacks `ctx`.** `runtime.DefinitionRegistry.Lookup(defRef string)`
  takes no context; the Postgres `DefinitionStore.Lookup` uses `context.Background()` internally, so a
  caller's cancellation/deadline doesn't propagate to the definition query. Thread `ctx`.

**In scope:** both, across their (non-engine) ripple. **Out of scope:** richer admin filters,
streaming/watch (separate items). Engine/model untouched.

## 2. Part A — admin list total-count

Add an opt-in total to the lister chain:

- `runtime.InstanceFilter` gains `IncludeTotal bool` (default false → no extra query, exact current
  behaviour). `runtime.InstancePage` gains `TotalCount int` (meaningful only when `IncludeTotal` was
  set; 0 otherwise).
- `runtime.InstanceLister.List` impls compute the count **only when `filter.IncludeTotal`**:
  - **MemStore:** count instances matching the status filter (in-memory).
  - **Postgres lister:** a `SELECT count(*) FROM wrkflw_instances WHERE <status filter>` (reusing the
    list's status predicate, no cursor) — one extra query, gated on `IncludeTotal`.
- `service.ListInstances` passes the filter through unchanged (it already forwards `runtime.InstanceFilter`).
- **REST** (`GET /admin/instances?total=true`): when `total=true`, set `filter.IncludeTotal`; the
  response envelope gains `"total_count"` (omitempty / always present — emit always, 0 when not requested).
- **gRPC** (`ListInstancesRequest.include_total`): a new `bool include_total = N` request field +
  `int64 total_count = N` on `ListInstancesResponse`. Regen.

Opt-in keeps the common list path free of the COUNT cost; admins paginating a UI request it explicitly.

## 3. Part B — `DefinitionRegistry.Lookup(ctx, defRef)`

Mechanical ctx-threading (a port signature change; we own all impls/callers):

- Port: `runtime.DefinitionRegistry.Lookup(ctx context.Context, defRef string) (*model.ProcessDefinition, error)`.
- Impls: `MapDefinitionRegistry.Lookup`, `CachingDefinitionRegistry.Lookup` (passes ctx to
  `backing.Lookup`), Postgres `DefinitionStore.Lookup` (use the passed ctx instead of
  `context.Background()` for its query).
- Call sites thread their existing ctx: `runner.go` (×3: child def resolution, StartSubInstance,
  ResolveIncident), `call_notifier.go` (DrainOnce), `service.go` (×3: StartInstance/DeliverMessage/
  ResolveIncident — all have `ctx`). Each already has a `ctx` in scope.

Breaking change to the public `runtime.DefinitionRegistry` port (external implementers add `ctx`) —
acceptable for a young library; documented in the ADR.

## 4. Testing strategy

- **Part A:** runtime mem lister test — `IncludeTotal=true` returns the correct `TotalCount`
  (independent of page size); `IncludeTotal=false` → `TotalCount==0` and no behaviour change. Postgres
  lister test (testcontainers) — seed N instances across statuses, `IncludeTotal=true` + a status
  filter → `TotalCount` = matching count, independent of `Limit`. REST test: `?total=true` →
  `total_count` in body; absent flag → 0. gRPC test: `include_total` → `total_count` in response.
- **Part B:** the existing registry tests updated for the new signature; a test that a cancelled
  `ctx` propagates (Postgres `DefinitionStore.Lookup` with a cancelled ctx returns a ctx error). Mem
  registry ignores ctx (documented). All existing callers compile + pass (the ripple is mechanical).

**Gate:** `go test -race -p 1 ./...` green; ≥85% touched pkgs; `golangci-lint` clean; engine/model
diff ZERO; proto regen reproducible.

## 5. ADR

| ADR | Decision |
|---|---|
| **0038** | (A) Opt-in admin-list total-count: `InstanceFilter.IncludeTotal` + `InstancePage.TotalCount`; listers compute `count(*)` only when requested; surfaced as REST `?total=true`→`total_count` and gRPC `include_total`→`total_count`. (B) `DefinitionRegistry.Lookup` gains `ctx` (threaded through all impls + call sites; Postgres uses it instead of `context.Background()`) — a breaking port change for external implementers. `ended_at`-optional-proto closed as already-satisfied (nullable message field; handlers already conditional). Engine/model untouched. |
