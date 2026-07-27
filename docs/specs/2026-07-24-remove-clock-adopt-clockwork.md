# Spec: Remove the `clock` package, adopt `clockwork.Clock` directly

- **Date:** 2026-07-24
- **Status:** Approved (design), pending bundle audit
- **ADR:** 0138 (supersedes ADR-0003)
- **Bundle:** spec (this) + ADR-0138 + plan `docs/plans/2026-07-24-remove-clock-adopt-clockwork.md`

## Context

The engine's sole time abstraction today is the in-repo `clock.Clock` interface
(`clock/clock.go`, ADR-0003) — a one-method interface (`Now() time.Time`) that
`clockwork.Clock` satisfies structurally. ADR-0003 and CLAUDE.md Common Pitfall #3
forbid importing `clockwork` from engine/workflow code, routing everything through
`clock.Clock` for vendor-swappability.

Two consequences motivate this change:

1. **Non-deterministic waiting.** Because `clock.Clock` only exposes `Now()`, any
   code needing a ticker/timer/after channel bypasses the abstraction and calls
   `time.*` directly. Those sites cannot be driven by a fake clock, so their tests
   are timing-dependent and flaky.
2. **The abstraction earns little.** `clockwork.Clock` is a strict superset of
   `clock.Clock`; keeping the one-method wrapper buys swappability the project has
   decided it does not need, at the cost of the fake-ticker/timer capability
   clockwork already provides.

**Decision (user-directed):** remove the `clock` package entirely and depend on
`clockwork.Clock` directly across the codebase, unlocking clockwork's fake ticker,
timer, and after primitives for deterministic tests. This reverses ADR-0003 and is
recorded as ADR-0138 superseding ADR-0003; CLAUDE.md is updated in the same bundle.

`clockwork` is already a locked, pinned dependency (`v0.5.0`, `go.mod`). Its `Clock`
interface exposes: `After`, `Sleep`, `Now`, `Since`, `Until`, `NewTicker`,
`NewTimer`, `AfterFunc`, with `Ticker`/`Timer` interfaces and
`NewRealClock()` / `NewFakeClock()` constructors.

## Goals

- Delete `clock/` and all imports of `github.com/kartaladev/wrkflw/clock`.
- Every production time dependency is a `clockwork.Clock` field, defaulting to
  `clockwork.NewRealClock()`.
- **All five** production waiting-primitive sites are driven through the injected
  clock so a `clockwork.FakeClock` makes them deterministic in tests.
- Governance: superseding ADR + CLAUDE.md tech-stack row + Pitfall #3 + CHANGELOG.

## Non-Goals

- No behavioral change to production semantics (poll intervals, timeouts, backoff
  durations stay identical — only the *source* of the wait changes).
- No migration of DB `time.Now().UTC()` timestamp writes or latency-measurement
  `time.Now()` (metrics) to the clock — those are not waiting primitives and are
  out of scope (see Risks for the one nuance on store timestamps).
- Not touching `scheduler/internal/gocron/*`, which already imports clockwork
  directly (vendor-internal) and is unaffected.

## Design

### 1. Package removal & public API flip

Delete `clock/clock.go` and `clock/clock_test.go`.

Replace `clock.Clock` with `clockwork.Clock` and `clock.System()` with
`clockwork.NewRealClock()` everywhere. The defaulting ergonomics are preserved:
a component with no clock supplied uses `clockwork.NewRealClock()`.

Public option/field sites that flip type (breaking for consumers):

| Symbol | File |
|---|---|
| `WithClock(clockwork.Clock) Option` | `runtime/processdriver_options.go:203` |
| `WithClock(clockwork.Clock) Option` | `service/options.go:84` (+ field `:26`) |
| `WithClock(clockwork.Clock) ChainerOption` | `runtime/chain/chainer.go:93` (+ fields) |
| `WithClock(clockwork.Clock) CallNotifierOption` | `runtime/calllink/notifier.go:80` (+ field `:41`) |
| `WithClock(clockwork.Clock) SignalBusOption` | `runtime/signal/signalbus.go:59` (+ field `:47`) |
| `WithClock(clockwork.Clock) TaskServiceOption` | `runtime/task/service.go:68` (+ fields) |
| `WithRelayClock(clockwork.Clock) RelayOption` | `persistence/persistence.go:291` |
| `WithCallLinkClock(clockwork.Clock) CallLinkOption` | `persistence/persistence.go:387` |
| clock fields in `internal/persistence/store` (`relay.go`, `call_links.go`) | store internals |
| clock fields in `runtime/kernel` (`caching_definition_registry.go`, `mem_calllink.go`) | kernel internals |
| `mysql.go` / `persistence.go` internal clock plumbing | persistence |
| `WithMemSchedulerClock(clockwork.Clock)` + field/default (**public, breaking**) | `processtest/memscheduler.go` |
| `Clock clockwork.Clock` field + default | `runtime/internal/runtimetest/doubles.go` |
| clock doc wording | `doc.go:93-95` |

**Narrow-stub break (audit finding):** `runtime/kernel/caching_definition_registry_test.go`
has a hand-rolled `fakeClock` implementing only `Now()`; once the registry field
widens to `clockwork.Clock` it no longer satisfies it, so it is replaced with
`clockwork.NewFakeClockAt`. The plan mandates a post-edit sweep
(`grep "func.*Now() time.Time"`) to catch any other narrow stub.

All doc comments referencing "`clock.Clock` (ADR-0003)" are updated to name
`clockwork.Clock` and cite ADR-0138.

### 2. Route all waiting primitives through the clock

| Site | Current | New | Clock present today? |
|---|---|---|---|
| `runtime/calllink/notifier.go:227` | `time.NewTicker(n.poll)` | `n.clk.NewTicker(n.poll)` | yes |
| `internal/persistence/store/relay.go:482` | `time.NewTicker(r.poll)` | `r.clk.NewTicker(r.poll)` | yes |
| `internal/persistence/store/notifier_pgx.go:155` | `<-time.After(backoff)` | `<-clk.After(backoff)` | **no — inject** |
| `internal/authz/casbin/pg_watcher.go:133` | `<-time.After(delay)` | `<-clk.After(delay)` | **no — inject** |
| `internal/expreval/expreval.go:83` | `time.NewTimer(e.timeout)` | `e.clk.NewTimer(e.timeout)` | **no — inject** |

For each ticker/timer, preserve existing `defer stop()` semantics using the
`clockwork.Ticker`/`clockwork.Timer` `Stop()` method (same shape as stdlib).

**Injection for the three clock-less components:**

- `internal/expreval`: add an unexported `clk clockwork.Clock` field and a
  `WithClock(clockwork.Clock)` option (mirrors existing `WithTimeout`), default
  `clockwork.NewRealClock()`. **Production wiring keeps the real-clock default** —
  the runtime does NOT pass `driver.clk` into `expreval.New` at
  `runtime/processdriver_options.go:175`. Rationale: the evaluation timeout is a
  *wall-clock DoS backstop*, not a replay-relevant value; threading the engine's
  (possibly fake, never-advanced) clock into it would silently disable the guard in
  fake-clock tests and let a runaway expression hang. The `WithClock` option exists
  solely so expreval's **own unit test** can drive the timeout deterministically
  with a fake. Engine usage (`engine/conditions.go`, `WithTimeout(0)`) never takes
  the timer path at all.
- `internal/persistence/store/notifier_pgx`: accept a `clockwork.Clock` at
  construction; the owning store already threads a clock (relay/call_links), reuse
  the same one.
- `internal/authz/casbin/pg_watcher`: accept a `clockwork.Clock` at construction
  from the casbin adapter wiring, default real.

Where a constructor gains a clock parameter that has no existing option surface,
prefer an additive functional option with a real-clock default over a positional
parameter, to keep internal call sites compiling with minimal churn.

### 3. `processtest.FakeClock` migration (public test helper)

`processtest/clock.go` currently defines a hand-rolled `FakeClock` implementing
only `clock.Clock` (Now), shared between a `Harness`'s driver and `MemScheduler`.
With `clock.Clock` gone it must satisfy the wide `clockwork.Clock`.

**Chosen approach:** replace the hand-rolled type with `clockwork`'s fake.
- `processtest.NewFakeClock(start)` returns `clockwork.NewFakeClockAt(start)`
  (type `*clockwork.FakeClock`), or the helper is thinned to a type alias /
  constructor wrapper so existing consumer call sites keep compiling.
- `Advance(d)` maps directly (clockwork `FakeClock.Advance` exists).
- **`Set(t)` (backward jump) has no clockwork equivalent.** Audit callers of
  `FakeClock.Set`; if any jump *backward*, either (a) reformulate as forward
  `Advance`, or (b) retain a tiny `processtest` wrapper adding `Set` over
  `clockwork.FakeClock`. Decide during planning based on actual call sites.
- `MemScheduler` reads `Now()` only — satisfied by `clockwork.FakeClock`.

This is a **breaking change to `processtest`** if `Set` is dropped; documented in
CHANGELOG.

### 3a. Engine-core purity is preserved (important scoping)

The pure engine core (`engine/`, `definition/`) **never imported `clock.Clock`** —
ADR-0003 kept it clock-free, taking time only as `Trigger.OccurredAt` at the
runtime boundary. Every consumer of `clock.Clock` is an *outer* stateful layer
(runtime, persistence, service, internal adapters). Therefore:

- `engine/purity_test.go` `deniedEngineImports` **retains** `"clockwork"` (line 69):
  the core stays clockwork-free and wall-clock-free (`TestCorePurityNoWallClock`).
  These guards are unaffected by the removal and must keep passing.
- Only the stale *comment* wording in `purity_test.go` (lines 95/103, "clock.Clock")
  is cosmetically updated; no behavioral change to the guard.
- Net effect: the reversal is **scoped to the outer layers**, not pervasive. This
  is the key reason it is low-risk despite reversing a locked ADR.
- `scheduler/selfcontainment_guard_test.go` is reviewed during planning to confirm
  it imposes no conflicting constraint (scheduler already uses clockwork directly).

### 4. Governance (mandatory — locked-decision reversal)

- **ADR-0138** (Nygard), `Status: accepted, supersedes ADR-0003`. Records the
  removal, the direct-clockwork decision, and consequences: accepted vendor
  lock-in on clockwork; breaking public API; the CLAUDE.md rule reversal; the
  deterministic-test benefit.
- **CLAUDE.md edits in the same commit:**
  - Tech-stack "Time source" row: stop saying "never import clockwork from
    engine/workflow code"; state that `clockwork.Clock` is now the direct time
    dependency (cite ADR-0138), fake clock still shared engine+scheduler in tests.
  - Common Pitfall #3: remove `clockwork` from the "never import directly" list
    (watermill/casbin/gocron remain).
- **CHANGELOG:** breaking-change entries for the removed `clock` package, the
  flipped option signatures, and any `processtest.FakeClock.Set` removal.

## Testing strategy (TDD, red-first)

Hot-path-first. New deterministic tests, each failing before its impl:

1. **notifier ticker** (`runtime/calllink`): with a `clockwork.FakeClock`, assert a
   poll cycle fires only after `Advance(poll)`, not on wall time.
2. **relay ticker** (`internal/persistence/store`): same, drives the outbox relay
   loop by `Advance`.
3. **pgx-notifier reconnect backoff**: `Advance(backoff)` releases the reconnect
   wait deterministically.
4. **casbin watcher reconnect**: same for the watcher delay.
5. **expr-eval timeout** (`internal/expreval`): with a fake clock, `Advance(timeout)`
   deterministically triggers the evaluation-timeout path (regression for the
   timeout branch).

Existing clockwork-based tests (~60 files) continue to pass; convert any that
constructed `processtest.FakeClock` if its surface changes.

## Verification checklist

- [ ] `clock/` deleted; `grep -r "wrkflw/clock"` returns nothing.
- [ ] All five waiting-primitive sites call the injected `clockwork.Clock`.
- [ ] `expreval`, `notifier_pgx`, `pg_watcher` each own a clock, default real.
- [ ] `processtest` compiles against `clockwork.Clock`; `Set` decision applied.
- [ ] 5 new deterministic tests, each shown red before green.
- [ ] `go test -race ./...` green; ≥85% on every touched package (hot paths first).
- [ ] `golangci-lint run ./...` clean.
- [ ] ADR-0138 written (supersedes ADR-0003); CLAUDE.md tech-stack row + Pitfall #3
      updated; CHANGELOG breaking entries added.
- [ ] Delivery Gate: `/code-review` + `/security-review`, all findings folded via
      `--amend`.
- [ ] One feature-bundle commit (impl + tests + ADR + spec + plan + CLAUDE.md).

## Risks & open questions

- **Breaking public API.** Consumers importing `wrkflw/clock` or passing
  `clock.Clock` must switch to `clockwork.Clock`. This includes the public
  `processtest.WithMemSchedulerClock` option (now `clockwork.Clock`). Mitigated by
  CHANGELOG + ADR; no compatibility shim (per the "remove clock" directive).
- **`FakeClock.Set` backward jumps** — resolved during planning by auditing callers.
- **expreval clock threading** — the exact construction site that must pass the
  clock down is confirmed during planning; if expr evaluation is constructed deep
  in the engine without a clock in scope, the injection may widen. Flagged for the
  audit.
- **Store `time.Now().UTC()` timestamp writes** stay on the wall clock
  (out of scope); note that this leaves store-written `created_at` values
  non-deterministic — acceptable, they are not waiting primitives and no test
  asserts on their exact value via the clock.
