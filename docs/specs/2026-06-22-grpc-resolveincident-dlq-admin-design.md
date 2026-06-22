# Design: gRPC `ResolveIncident` RPC + DLQ admin transport surface

**Date:** 2026-06-22
**Status:** Approved (brainstorming)
**Track:** Consolidated-backlog top pick #1 (Resilience follow-up from ADR-0017/0016).
**ADR:** 0029.

## 1. Problem & scope

The Resilience sub-project (ADRs 0015–0018) shipped the load-bearing runtime/persistence
APIs for incident resolution and outbox dead-letter management, but left the transport
surface partly unbuilt (Resilience deferred follow-up #1; resilience spec §8):

- **`ResolveIncident` over gRPC** — exists on REST
  (`POST /admin/instances/{id}/incidents/{incidentID}/resolve`) and on `service.Service`
  (`ResolveIncident(ctx, ResolveIncidentRequest) (engine.InstanceState, error)`), but there
  is **no gRPC RPC** for it.
- **DLQ admin** — `persistence.Relay.ListDeadLettered(ctx, limit)` and
  `Relay.Redrive(ctx, ids...)` exist, but are **not reachable through any transport**, and
  not reachable from `service.Service` at all (the `Relay` is constructed separately by the
  consumer).

This track closes both gaps on **both transports** (REST + gRPC), keeping the library-first
property: every feature reachable and ergonomic through the public root-package API.

**In scope:**
- gRPC `ResolveIncident` RPC (thin pass-through to `service.ResolveIncident`).
- DLQ admin on REST (`GET /admin/dead-letters`, `POST /admin/dead-letters/redrive`) and gRPC
  (`ListDeadLetters`, `RedriveDeadLetters`).
- A new optional `service.DeadLetterAdmin` interface seam + `WithDeadLetterAdmin(...)`
  functional option on both transports.

**Out of scope (deferred):** casbin-gated per-incident/DLQ authz (admin middleware / consumer
interceptor remains the v1 boundary); streaming/watch of dead-letters; pagination cursor for
dead-letters (a simple `limit` matches the persistence API); `wrkflw_processed_message`
pruning. Engine/model are **untouched** (zero diff) — this is a transport + service-interface
track only.

## 2. The DLQ seam (architecture decision → ADR-0029)

The DLQ is a **persistence/relay concern**, distinct from the engine/runtime-backed
`service.Service`. Folding `ListDeadLettered`/`Redrive` into `service.Service` would (a)
couple relay infrastructure into the process-instance facade, (b) change the `service.New(...)`
signature, and (c) force a nil-`Relay` for the common MemStore-only consumer who has no outbox
relay. We therefore introduce a **separate, optional admin seam**:

```go
// service/deadletter.go
package service

import (
    "context"
    "github.com/zakyalvan/krtlwrkflw/runtime"
)

// DeadLetterAdmin is the optional admin port for inspecting and redriving
// dead-lettered outbox events. persistence.Relay satisfies it directly; a
// MemStore-only consumer simply never wires it.
type DeadLetterAdmin interface {
    ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
    Redrive(ctx context.Context, ids ...int64) (int, error)
}
```

- The method names/signatures are **identical to `persistence.Relay`'s**, so `persistence.Relay`
  satisfies `service.DeadLetterAdmin` with **no adapter**. A consumer passes their relay straight
  through: `rest.WithDeadLetterAdmin(relay)`, `grpc.WithDeadLetterAdmin(relay)`.
- It references only `runtime.DeadLetter` (already imported by `service`) — **no new `service`
  imports**, no import cycle.
- The compile-time satisfaction is asserted in a **black-box test** in the `persistence` package
  (`package persistence_test` importing `service`) — a test dependency only, never a production
  `persistence → service` edge.

Decoupling means the transports take it as an **optional** dependency via a functional option,
not a constructor parameter.

## 3. Transport behaviour when DLQ admin is not wired

The two transports differ structurally (REST routes are dynamic; the gRPC service contract is
fixed by the generated interface), so each does the idiomatic thing — documented in ADR-0029:

| Transport | DLQ wired (`WithDeadLetterAdmin`) | DLQ **not** wired |
|---|---|---|
| REST | routes registered behind the admin middleware | routes **not registered** → `404` (no info leak; honest "endpoint absent in this deployment") |
| gRPC | RPC delegates to the admin | RPC returns `codes.Unimplemented` ("dead-letter admin not configured") — the method exists in the generated service contract regardless |

`ResolveIncident` (gRPC) is **always** present and needs no new wiring — it pass-throughs to the
existing `service.Service`.

## 4. gRPC changes (`transport/grpc`)

### 4.1 Proto additions (`proto/workflow.proto`)

Three new RPCs on `WorkflowService`:

```protobuf
  // ResolveIncident clears an open incident, grants additional attempts, and resumes.
  rpc ResolveIncident(ResolveIncidentRequest) returns (InstanceResponse);

  // ListDeadLetters returns dead-lettered outbox rows (admin; requires WithDeadLetterAdmin).
  rpc ListDeadLetters(ListDeadLettersRequest) returns (ListDeadLettersResponse);

  // RedriveDeadLetters re-queues dead outbox rows by id (admin; requires WithDeadLetterAdmin).
  rpc RedriveDeadLetters(RedriveDeadLettersRequest) returns (RedriveDeadLettersResponse);
```

New messages:

```protobuf
message ResolveIncidentRequest {
  string instance_id = 1;
  string incident_id = 2;
  int32  add_attempts = 3;   // ≤ 0 → service defaults to 1
}

message DeadLetter {
  int64                     id = 1;
  string                    instance_id = 2;
  string                    topic = 3;
  int32                     retry_count = 4;
  string                    last_error = 5;
  google.protobuf.Timestamp created_at = 6;
}

message ListDeadLettersRequest  { int32 limit = 1; }       // normalized [1,200], default 50
message ListDeadLettersResponse { repeated DeadLetter items = 1; }

message RedriveDeadLettersRequest  { repeated int64 ids = 1; }
message RedriveDeadLettersResponse { int32 redriven_count = 1; }
```

Regenerated via the existing `//go:generate` directive (`go generate ./transport/grpc/...`,
raw-`protoc` with `protoc-gen-go`/`protoc-gen-go-grpc`). The committed toolchain produces
byte-identical output (verified).

### 4.2 Server wiring (`server.go`, `options.go`)

- `serverConfig` gains `deadLetters service.DeadLetterAdmin`.
- `WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option` (panics if nil, matching
  `WithAdminMiddleware`'s nil-guard convention).
- `server` struct gains `deadLetters service.DeadLetterAdmin`; `RegisterWorkflowServiceServer`
  threads `cfg.deadLetters` into it.
- `ResolveIncident` handler mirrors `CancelInstance`: span → `svc.ResolveIncident` → `instanceToProto`
  → `mapToGRPCStatus` on error.
- `ListDeadLetters` / `RedriveDeadLetters` handlers: span; if `s.deadLetters == nil` →
  `status.Error(codes.Unimplemented, "workflow-grpc: dead-letter admin not configured")`; else
  delegate, mapping the limit via `runtime.NormalizeLimit` and DeadLetter→proto.
- A `deadLetterToProto(runtime.DeadLetter) *workflowpb.DeadLetter` helper (timestamps via
  `timestamppb.New`).
- The `SECURITY` doc-comment on `RegisterWorkflowServiceServer` is extended: the DLQ RPCs are
  also admin-scoped and rely on the consumer's interceptor (same posture as `ListInstances`).

## 5. REST changes (`transport/rest`)

- `config` gains `deadLetters service.DeadLetterAdmin`.
- `WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option` (nil-guard panic, matching
  `WithAdminMiddleware`).
- In `NewHandler`, **only when `cfg.deadLetters != nil`**, register behind `cfg.adminMiddleware`:
  - `GET /admin/dead-letters` → `handleListDeadLetters`
  - `POST /admin/dead-letters/redrive` → `handleRedriveDeadLetters`
- `handleListDeadLetters`: read `?limit=` (atoi; bad value → `ErrBadInput`→400), normalize via
  `runtime.NormalizeLimit`, call `ListDeadLettered`, render `{"items":[...]}` with a fixed
  `deadLetterView` JSON shape (`id`, `instance_id`, `topic`, `retry_count`, `last_error`,
  `created_at`).
- `handleRedriveDeadLetters`: decode `{"ids":[int64,...]}` via `decodeBody`; empty/absent ids →
  call `Redrive` with no ids (its documented no-op, returns 0) → render `{"redriven":0}`; else
  call `Redrive`, render `{"redriven":N}`.
- Errors flow through the existing `WriteHTTPError`/`classifyError` (a relay/DB failure →
  `internal_error` 500, which is correct for an infra fault).

`deadLetterView` lives in `transport/rest/view.go` (alongside the existing instance views) — it
is **not** customizable in v1 (no consumer mapper); YAGNI.

## 6. Error mapping

No new sentinels. Existing mappings already cover the relevant cases:
- gRPC: `mapToGRPCStatus` (`ResolveIncident` reuses the existing instance-error mapping; the
  `Unimplemented` for unwired DLQ is produced directly in the handler).
- REST: `classifyError` (limit parse → 400 via `ErrBadInput`; relay/DB errors → 500).

## 7. Testing strategy

Per CLAUDE.md TDD discipline — visible RED → GREEN for every new symbol/handler.

- **`service`**: `DeadLetterAdmin` is a pure interface declaration; its real exercise is the
  black-box satisfaction assertion (next bullet) plus the transport handler tests below.
- **`persistence` (`persistence_test`)**: compile-time `var _ service.DeadLetterAdmin =
  (persistence.Relay)(nil)` assertion proving the relay satisfies the seam — RED is a build
  failure (`undefined`) until the interface exists.
- **`transport/grpc` (black-box `grpctransport_test`)** using a stub `service.Service` +
  a stub `service.DeadLetterAdmin`:
  - `ResolveIncident` success → maps request fields, returns proto instance; error → mapped status.
  - `ListDeadLetters` wired → returns items, normalizes limit; not wired → `codes.Unimplemented`.
  - `RedriveDeadLetters` wired → returns count; empty ids → 0; not wired → `codes.Unimplemented`.
  - `WithDeadLetterAdmin(nil)` panics.
- **`transport/rest` (black-box `rest_test`)** using a stub `service.Service` + stub
  `DeadLetterAdmin`, driven through `httptest`:
  - `GET /admin/dead-letters` wired + admin-allow middleware → 200 + items; default-deny (no
    middleware) → 403; not wired → 404; bad `limit` → 400.
  - `POST /admin/dead-letters/redrive` wired → 200 + `{"redriven":N}`; empty ids → 200 `{"redriven":0}`;
    not wired → 404.
  - `WithDeadLetterAdmin(nil)` panics.
  - Table tests follow the project `table-test` skill (assert-closure form, `t.Context()`).

**Gate (every touched package):** `go test -race ./...` green; ≥85% line coverage on touched
packages (`service`, `transport/grpc`, `transport/rest`, `persistence`); `golangci-lint run ./...`
clean; **engine/model production diff ZERO** (no engine/model files touched); no forbidden vendor
imports introduced.

## 8. ADR

| ADR | Decision |
|---|---|
| **0029** | Optional `service.DeadLetterAdmin` seam (separate from `service.Service`) + `WithDeadLetterAdmin` transport options for the DLQ admin surface; per-transport not-configured behaviour (REST: route absent → 404; gRPC: `codes.Unimplemented`); gRPC `ResolveIncident` RPC as a thin pass-through. DLQ/ResolveIncident authz stays the consumer's transport-gate responsibility (admin middleware / interceptor). |

## 9. Risks / notes

- **Proto regen toolchain**: de-risked — `protoc-gen-go`/`protoc-gen-go-grpc` installed, raw-`protoc`
  regen of the current proto is byte-identical to committed output.
- **gRPC DLQ auth**: same caveat as `ListInstances` — no built-in per-method gate; consumer MUST
  supply an interceptor. Documented on `RegisterWorkflowServiceServer`.
- **Coverage**: the DLQ handlers' "not wired" branches and the wired branches are both directly
  testable with stubs — no Postgres needed for the transport tests (the `persistence` satisfaction
  test needs no live DB either, being a nil-interface compile-time assertion).
