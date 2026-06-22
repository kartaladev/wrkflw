# 38. Admin total-count + DefinitionRegistry.Lookup ctx

- Status: Accepted
- Date: 2026-06-23

## Context

Two small API-completeness items remained from the backlog:

1. The admin instance-list response carries `items`/`next_cursor`/`has_more` but **no total count** of
   matching instances, so a UI cannot render "N total" / "page X of Y".
2. `runtime.DefinitionRegistry.Lookup(defRef string)` takes **no `ctx`**; the Postgres
   `DefinitionStore.Lookup` uses `context.Background()`, so a caller's cancellation/deadline does not
   propagate to the definition query.

A third item, "`ended_at` optional in proto", was investigated and found **already satisfied**: the
gRPC handlers set `EndedAt` only when non-nil, the proto `Timestamp` field is inherently nullable, and
the REST view uses `*time.Time,omitempty` — a running instance already serializes `ended_at` as absent.

## Decision

Two non-engine changes (engine/model untouched):

**A. Opt-in admin-list total-count.** `runtime.InstanceFilter` gains `IncludeTotal bool` (default
false) and `runtime.InstancePage` gains `TotalCount int`. Listers compute `count(*)` over the
(status-filtered, cursor-less) set **only when `IncludeTotal` is set** — keeping the common list path
free of the extra query. Surfaced as REST `GET /admin/instances?total=true` → `total_count` in the
envelope, and gRPC `ListInstancesRequest.include_total` → `ListInstancesResponse.total_count`.

**B. `DefinitionRegistry.Lookup(ctx, defRef)`.** Thread `ctx` through the port, all three impls
(`MapDefinitionRegistry`, `CachingDefinitionRegistry`, Postgres `DefinitionStore`), and every call
site (runner ×3, call-notifier, service ×3). The Postgres impl uses the passed ctx instead of
`context.Background()`. A breaking change to the public `runtime.DefinitionRegistry` port.

## Consequences

**Positive**
- UIs can paginate with a true total (opt-in, so non-paginating callers pay nothing).
- Definition lookups honour caller cancellation/deadlines end-to-end; idiomatic ctx propagation.
- `ended_at` confirmed correct on both transports (no change needed).

**Negative / trade-offs**
- `DefinitionRegistry.Lookup` is a **breaking port-signature change** — external implementers must add
  `ctx`. Acceptable for a young library; the in-repo impls/callers are all updated. The Mem/Map
  registry ignores `ctx` (documented).
- `IncludeTotal=true` issues one extra `count(*)` per list call (gated; admins opt in).
- gRPC proto gains two fields (additive, backward-compatible on the wire).
