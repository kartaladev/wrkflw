# Design

## Context

See proposal.md, *Why*, for the measurements. `unify-http-routes-as-data` gives `httpcore` a route table, a six-method `Binding` (`Param`, `Query`, `Context`, `ReadBody`, `Write`, `SetHeader`) and one serve path that does decoding, identity-before-body ordering, encoding and the error envelope. `split-persistence-modules` gives every HTTP binding its own module. This change adds one more binding and one more module; it adds no per-route behavior.

The design was decided on kartaladev/wrkflw#23 and #40. This document records those decisions with their evidence and does not reopen them.

## Goals / Non-Goals

**Goals:**
- A Fiber v2 consumer mounts the same routes and gets the same bytes as every other adapter.
- The v2 context trap cannot be introduced by a later edit.

**Non-Goals:**
- Any per-route behavior. It all lives in the shared serve path.
- Removing the fasthttp `BodyLimit` divergence. It is a framework fact and stays documented.

## Decisions

**D1. Fiber v2 is a binding over `*fiber.Ctx`** (decided on #23 and #40).
In v2, `c.Context()` returns `*fasthttp.RequestCtx`, which satisfies `context.Context`. So a copy of the v3 binding compiles and silently loses every value consumer middleware set (#23: "the one silent trap"). The binding uses `c.UserContext()` and `c.SetUserContext()` only. A guard test in the package fails on any `.Context()` call in its non-test files, so the trap cannot return through a later edit. `Group(prefix, ...Handler)` replaces v3's `[]any` conversion.
- Rejected: copy the v3 adapter and fix call sites by review. Each of the 36 `.Context()` lines in the v3 adapter compiles unchanged on v2, so review is the only defense, and #23 named it the trap.

**D2. No framework decoder** (decided on #23, audit).
The binding hands `c.Body()` bytes to the shared serve path through `ReadBody`. v2's `BodyParser` is never called, so the "one JSON value" and "one message" rules `unify-http-routes-as-data` fixes cannot diverge through it.

**D3. Pin v2.52.15** (#40).
It is the newest published v2 release (measured with `go list -m -versions`). The local module cache tops out at v2.52.13, so the first build fetches it.

**D4. Its own module from birth** (decided on #30; ordering from `unify-http-routes-as-data`'s recommended split).
#40 said to do the table refactor on the single module first and let #39/#30 move finished bindings. With v2 split out, there is nothing to move: this change lands after `split-persistence-modules` and creates `transport/http/fiberv2/go.mod` directly, following that change's rules (requires root at a pseudo-version of a pushed commit, never a `replace`; listed in `go.work`; counted in `MIN_MODULES`).
- Rejected: land in the root module before the split, as the unsplit plan did. It puts `github.com/gofiber/fiber/v2` in every consumer's module graph until the split moves it, which is a go.mod change made twice.

**D5. Parity rows follow `unify-http-routes-as-data`.**
The parity scenario compares fiberv2 to stdlib. Its expected statuses for the trailing-bytes and `?limit=-1` rows are the ones `unify-http-routes-as-data` settles (the latter is its pending OQ2). If that ruling differs from its recommendation, the row here changes to match before this change is applied; it raises no question of its own.

### Cross-change coupling

| Change | Shared surface | Order |
|---|---|---|
| `unify-http-routes-as-data` | `httpcore.Binding`, `Serve`, the route groups, the parity table. | It lands first (Depends on). |
| `split-persistence-modules` | module layout for HTTP bindings, `go.work`, `MIN_MODULES`, the module-dependency guard, and where `transport/http/parity` goes (its Q1). | It lands first (Depends on). Its proposal still lists `transport/http/fiberv2` among the modules it creates; that entry moves here. |
| `define-access-and-disclosure`, `add-instance-access-hook` | disclosure and access behavior on routes. | Independent: both act in the shared serve path, which v2 inherits. |

## Risks / Trade-offs

- [Fiber v2 is in maintenance upstream] → the adapter is a thin binding; dropping it later deletes one module and no shared code.
- [The two fasthttp-based bindings drift] → both are rows of the one parity table, and both carry the `BodyLimit` divergence test.
- [Landing after the split delays v2] → accepted: it avoids a temporary root go.mod change, and no open question of this change blocks it once its dependencies land.

## Migration Plan

Additive. A consumer opts in by requiring the new module. Rollback is a revert.

## Open Questions

None. Every decision is settled on #23, #30 or #40, and the parity statuses follow `unify-http-routes-as-data` (D5).
