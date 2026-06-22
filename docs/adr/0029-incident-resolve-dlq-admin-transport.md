# 29. gRPC `ResolveIncident` RPC + optional `DeadLetterAdmin` seam for the DLQ admin surface

- Status: Accepted
- Date: 2026-06-22

## Context

The Resilience sub-project (ADRs 0015–0018) shipped the load-bearing runtime and persistence
APIs for incident resolution and outbox dead-letter (DLQ) management, but left the transport
surface partly unbuilt (Resilience deferred follow-up #1; resilience spec §8):

- **`ResolveIncident`** is reachable on REST (`POST
  /admin/instances/{id}/incidents/{incidentID}/resolve`) and on `service.Service`, but there is
  **no gRPC RPC** for it. gRPC admin parity is otherwise complete (`ListInstances`,
  `CancelInstance`).
- **DLQ admin** — `persistence.Relay.ListDeadLettered(ctx, limit)` and `Relay.Redrive(ctx,
  ids...)` exist, but are reachable from **no transport** and from **no service facade**. The
  `Relay` is constructed independently by the consumer (it is a Postgres-outbox concern); the
  engine-backed `service.Service` has never depended on it.

CLAUDE.md's library-first property requires every feature to be reachable and ergonomic through
the public root-package API, on both library-provided transports. This ADR records the structural
decisions for closing those two gaps.

### The seam question

The DLQ API could be folded into the existing `service.Service` interface (adding
`ListDeadLetters`/`RedriveDeadLetters`), with a `Relay` injected into `service.New(...)`. That
was rejected because:

1. It couples a **persistence/relay infrastructure** concern into the engine/runtime-backed
   process-instance facade — different lifecycle, different ownership.
2. It changes the `service.New(...)` constructor signature.
3. It forces a nil-`Relay` for the common **MemStore-only consumer**, who has no outbox relay at
   all — a degenerate dependency every such consumer must thread.

### The not-configured question

The DLQ admin is genuinely optional (only meaningful with the Postgres outbox relay). REST routes
are registered dynamically, so an unwired DLQ can simply have no route. The gRPC service contract,
by contrast, is fixed by the generated `WorkflowServiceServer` interface — the methods exist
whether or not a relay is wired, so the handlers must return *something* when unconfigured.

## Decision

1. **Separate optional `service.DeadLetterAdmin` seam.** A new interface in `service/`:

   ```go
   type DeadLetterAdmin interface {
       ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
       Redrive(ctx context.Context, ids ...int64) (int, error)
   }
   ```

   Its method set is **identical to `persistence.Relay`'s**, so the relay satisfies it with no
   adapter. It references only `runtime.DeadLetter` (already a `service` import) — no new imports,
   no cycle. Compile-time satisfaction is asserted in a black-box `persistence_test` (test-only
   `persistence → service` edge; no production dependency).

2. **Optional dependency via `WithDeadLetterAdmin(...)`** on both transports — never a constructor
   parameter. `persistence.Relay` is passed straight through. Nil argument panics immediately
   (matching `WithAdminMiddleware`'s nil-guard convention) so a misconfiguration fails at wiring
   time, not request time.

3. **Per-transport not-configured behaviour** (each idiomatic to its structure):
   - **REST**: the `/admin/dead-letters` routes are **registered only when wired**; unwired → the
     mux returns `404` (honest "this endpoint is absent in this deployment", no information leak).
   - **gRPC**: the RPCs are always present in the contract; unwired → `codes.Unimplemented` with
     a clear message.

4. **gRPC `ResolveIncident`** is added as a thin pass-through to the existing
   `service.ResolveIncident`, mirroring `CancelInstance`. It is always present (no new wiring).

5. **Authorization stays the consumer's transport-gate responsibility.** REST DLQ routes sit
   behind the default-deny `adminMiddleware`; the gRPC DLQ RPCs (like `ListInstances`) have no
   built-in per-method gate and rely on the consumer's interceptor — documented on
   `RegisterWorkflowServiceServer`. casbin-gated per-incident/DLQ authz remains future work.

6. **Engine/model are untouched** (zero production diff). This is a transport + service-interface
   track only.

## Consequences

**Positive**

- DLQ admin and `ResolveIncident` are reachable on both transports; library-first parity restored.
- The DLQ seam is decoupled: MemStore-only consumers are unaffected; `service.New(...)` is
  unchanged; the relay is wired exactly where it is constructed.
- `persistence.Relay` satisfying `DeadLetterAdmin` for free keeps consumer wiring a one-liner.
- Nil-guard panics surface misconfiguration at startup.

**Negative / trade-offs**

- **REST/gRPC asymmetry** for the unwired case (404 vs `Unimplemented`). Inherent to the
  transports' structures (dynamic routes vs fixed contract); documented rather than papered over.
- **gRPC DLQ RPCs lack a built-in authz gate** — same posture and risk as `ListInstances`.
  Mitigated by the explicit `SECURITY` doc-comment requiring a consumer interceptor.
- A new (test-only) `persistence → service` import edge exists in `persistence_test`. Acceptable:
  it never appears in a production build and documents the contract.

**Neutral**

- DLQ listing uses a simple `limit` (normalized via `runtime.NormalizeLimit`), not a keyset
  cursor like `ListInstances`. Matches the persistence API; a cursor is a future follow-up if the
  dead-letter volume warrants it.
- No new error sentinels: REST reuses `classifyError` (limit parse → 400; relay/DB fault → 500);
  gRPC reuses `mapToGRPCStatus` for `ResolveIncident` and emits `Unimplemented` directly for the
  unwired DLQ case.
