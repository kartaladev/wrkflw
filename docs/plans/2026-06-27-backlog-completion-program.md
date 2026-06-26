# Backlog Completion Program — 2026-06-27

Autonomous multi-track program kicked off 2026-06-27. The user authorized completing the
remaining discretionary backlog (top picks → hygiene → larger track → known pre-existing),
**excluding CI** and **skipping items that need major issue approvals**, AFK, via
spec → ADR → plan → subagent-driven-development → review → merge-to-main per track.

This is the program index. Each track gets its own spec (`docs/specs/`), ADR (`docs/adr/`),
and plan (`docs/plans/`). Starting state: `main` HEAD `0b4da71`, suite green, lint 0,
next free ADR **0068**.

## Triage

### ✅ WILL DO (autonomous — safe, additive, or bug-fix)

| # | Track | Bucket | Notes |
|---|---|---|---|
| T1 | Surface def-scoped action catalog in `InstanceSnapshot`/`ActionableView` DTOs + gRPC snapshot (incl. deferred `GetInstanceSnapshot` RPC) | Top pick | ADR-0063 follow-up, additive |
| H1 | Repo-wide `gofmt -w` hygiene sweep | Hygiene | Mechanical, no behavior change |
| H2 | Drop redundant `TestCachingDefinitionRegistry_SystemClock`; raise `service` coverage (`deadletter.go`/`policyadmin.go`) | Hygiene | Test-only |
| H3 | Make the `clockwork`/gocron clock seam optional via `With<Component>Clock` options | Hygiene | Completes ADR-0066 pattern |
| L1 | Observability nits: CallNotifier batch span, `instances_active` gauge, REST/relay meters emitting, route-template span naming | Larger (subset) | Additive |
| P1 | Fix Macro-mode parallel compensation-throw cursor overwrite + `recordCompensation` dedup | Pre-existing bug | Engine core; ADR justifies diff |
| P2 | Harden flaky `TestListenLoopExitsOnContextCancellation` | Pre-existing | Test-only |

### ⏸️ DEFERRED — spec/proposal only (needs explicit approval before implementation)

| # | Track | Why deferred |
|---|---|---|
| T2 | Multi-replica TIMER exclusivity (distributed claim-renew-failover scheduler) | Major architectural change. Double-fire is **already correct** via the engine CAS — this is an optimization, not a correctness gap. Writing an ADR-proposal; not implementing without sign-off. |

### ⛔ SKIPPED — major new dependencies / large new public surface (need approval)

- Broker-specific eventing constructors (Kafka/NATS/SNS) — new external deps + infra.
- Streaming/watch + OpenAPI/grpc-gateway — new dep + large new surface.
- casbin ABAC-in-matchers, richer Privilege modeling, `FilteredAdapter`/`WatcherEx` — feature/design decisions.
- Performance/scale items (per-def history-cap, cross-machine child exec, tunable backoff) — design-heavy.
- CI pipeline — explicitly excluded by the user.

## Execution order (user's stated order)

Top picks → hygiene → larger → pre-existing:
**T1 → H1 → H2 → H3 → L1 → P1 → P2**, then write the **T2** proposal.

Each track: branch off `main`, strict TDD (visible red→green per symbol), opus whole-branch
review, merge + push to `main`, update `docs/plans/HANDOVER.md` resume point.

## Status log

- 2026-06-27: program defined; triage above; tasks #1–#8 created; codebase mapped via Explore agents.
