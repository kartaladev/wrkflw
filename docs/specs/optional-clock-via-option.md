# Optional `clock.Clock` via a `With<Component>Clock` option

- Status: Approved (brainstorming), pending implementation plan
- Date: 2026-06-26
- Amends the injection convention of ADR-0003 (clock abstraction)
- ADR to record on implementation: next free number

## Problem

Per ADR-0003 every stateful component depends on the in-repo `clock.Clock` interface
rather than `time.Now()` / a vendored clock. Many constructors take the clock as a
**required positional parameter**, e.g.:

```go
func NewRunner(cat action.Catalog, clk clock.Clock, store Store, opts ...Option) *Runner
func NewSignalBus(clk clock.Clock, deliver DeliverFunc) *SignalBus
func NewMemScheduler(clk clock.Clock) *MemScheduler
```

The consumer wants the clock to be **optional**: when not provided, the component
defaults to the system clock (`clock.System()`, which already exists). Several
components already express the clock as an optional `With<Component>Clock` functional
option that defaults to `clock.System()` (e.g. `WithClock`, `WithMemCallLinkClock`,
`WithRelayClock`, `WithCallLinkClock`). This refactor makes that pattern **uniform**.

## Decision

**Approach B (chosen): move every `clock.Clock` positional parameter to a functional
option that defaults to `clock.System()`. Repo-wide.**

- A `clock.Clock`-bearing constructor no longer takes the clock positionally; it takes a
  `With<Component>Clock(clk clock.Clock)` option instead.
- When the option is absent, the constructor initialises the field to `clock.System()`.
- An explicit `With<Component>Clock(nil)` is also tolerated and falls back to
  `clock.System()` (nil-guard in the setter), so "provided but nil" behaves like
  "not provided".

### Naming convention (load-bearing)

Go forbids two `WithClock` functions in one package, and several packages host multiple
clock-bearing constructors. The repo already uses **`With<Component>Clock`** precisely to
avoid this collision (`WithClock`, `WithMemCallLinkClock`, `WithRelayClock`,
`WithCallLinkClock`, `WithElectorClock`). All new options follow it.

### Why not Approach A (nil-tolerant positional)

A nil-tolerant positional `clk` was the lower-churn alternative but was rejected in favour
of the more idiomatic option form, which also makes the clock argument self-documenting at
the call site and matches the components that already use options.

## Scope

### In scope — convert positional `clock.Clock` → option

| Package | Constructor | New option | Notes |
|---|---|---|---|
| `runtime` | `NewRunner(cat, clk, store, opts...)` | `WithClock` | **highest call-site churn** (many tests/examples) |
| `runtime` | `NewCachingDefinitionRegistry(backing, ttl, clk)` | `WithCachingDefinitionRegistryClock` | add variadic `...Option` (none today) |
| `runtime` | `NewCallNotifier(cl, deliver, reg, clk, opts...)` | `WithClock` | already has `CallNotifierOption` |
| `runtime` | `NewCachingStore(backing, owner, clk, opts...)` | `WithCachingStoreClock` | already has `CachingStoreOption` |
| `runtime` | `NewTaskService(store, az, clk, opts...)` | `WithClock` | already has `TaskServiceOption` |
| `runtime` | `NewSignalBus(clk, deliver)` | `WithClock` | add variadic `...Option` (none today) |
| `runtime` | `NewMemScheduler(clk)` | `WithMemSchedulerClock` | add variadic `...Option` (none today) |
| `persistence` | `NewCachingDefinitionRegistry(backing, ttl, clk)` | `WithCachingDefinitionRegistryClock` | facade mirror |
| `persistence` | `NewCallNotifier(pool, deliver, reg, clk, opts...)` | (reuses `runtime.WithClock`) | facade mirror |
| `service` | `New(runner, tasks, reg, store, lister, taskStore, clk)` | `WithEngineClock` | add variadic `...Option` (none today) |

### In scope — already option-based, harden only

Ensure the absent-default is `clock.System()` (already true) **and** add the nil-guard so an
explicit nil falls back to system: `runtime.WithClock`, `runtime.WithMemCallLinkClock`,
`internal/persistence/postgres` relay `WithClock` + `WithCallLinkClock`,
`persistence.WithRelayClock` + `persistence.WithCallLinkClock`.

### Out of scope — the `clockwork.Clock` vendor seam

`scheduling.NewScheduler(clk clockwork.Clock, ...)`,
`internal/scheduling/gocron.NewGocronScheduler(clk clockwork.Clock, ...)`, and
`WithElectorClock(clk clockwork.Clock)` take **`clockwork.Clock`**, not the in-repo
`clock.Clock`, because they are the gocron adapter where the clockwork import is
deliberately confined (CLAUDE.md). The instruction targets `clock.Clock`, so these are
left as-is. (They could be made optional with a `clockwork.NewRealClock()` default in a
follow-up if desired — flagged for the user.)

## Design details

- Each newly-optional constructor drops the positional `clk` and initialises the field
  before applying options:

  ```go
  func NewSignalBus(deliver DeliverFunc, opts ...SignalBusOption) *SignalBus {
  	b := &SignalBus{deliver: deliver, waiters: map[string]map[string]struct{}{}, clk: clock.System()}
  	for _, o := range opts {
  		o(b)
  	}
  	return b
  }

  // WithClock sets the time source. Default: clock.System().
  func WithClock(clk clock.Clock) SignalBusOption {
  	return func(b *SignalBus) {
  		if clk != nil {
  			b.clk = clk
  		}
  	}
  }
  ```

- New option **types** are introduced where a constructor currently has no variadic:
  `SignalBusOption`, `MemSchedulerOption`, `CachingDefinitionRegistryOption`, and a
  `service` option type (e.g. `EngineOption`).
- **Every call site updates**: drop the positional clock; where a fake clock was passed
  (tests, deterministic examples), pass `With<Component>Clock(fake)` instead. This is the
  bulk of the diff — dozens of `_test.go` files and `examples/` wiring across `runtime`,
  `persistence`, `service`.

## Architectural consequence & the determinism footgun

This **relaxes ADR-0003's compile-time guarantee**. Today a required positional `clk`
forces every caller — including tests — to pass a clock, which is how ADR-0003 keeps the
engine deterministic and tests reproducible. After this change, omitting the clock silently
yields `clock.System()` (wall time). A test that *forgets* to inject its fake will no longer
fail to compile; it will run non-deterministically.

Mitigations, to be recorded in the ADR:

- The determinism-sensitive tests already inject a fake clock explicitly and will continue
  to (the refactor converts them to `With<Component>Clock(fake)`, not to omission).
- The engine **core** (`engine/`, `model/`) is unaffected — it never constructs a clock; it
  receives time via triggers/commands (`engine.NewStartInstance(now, …)`), so its
  wall-clock-free determinism is untouched. This refactor only relaxes the *application
  layer* constructors.
- Reviewers must ensure any new deterministic test passes a fake clock rather than relying
  on the default.

## Testing (TDD, strict red → green per symbol)

For each converted constructor:

- **Default path:** construct with no clock option → the component uses a system clock
  (a timestamp it produces is within `[before, after]` of a real `time.Now()` bracket;
  no panic). New behaviour ⇒ test first.
- **Injected path:** construct with `With<Component>Clock(fake)` → the component uses the
  fake (assert a known fake `Now()` flows through, e.g. into a stamped trigger / TTL /
  armed-timer time).
- **Nil-guard:** `With<Component>Clock(nil)` → falls back to system clock (no panic).

Existing tests that passed a positional fake are migrated to the option form (behaviour
preserved; they must stay green before and after).

## Verification

- `go test -race ./...` green (testcontainers / Docker for the persistence + service tests).
- Touched packages ≥ 85% line coverage.
- `golangci-lint run ./...` clean; `gofmt` clean.
- `go build ./...` and all `examples/` compile with the new signatures.

## Out of scope / follow-ups

- Making the `clockwork.Clock` gocron constructors optional (different type / vendor seam).
- This refactor is independent of the transactional-SendTask-outbox change
  (`docs/specs/transactional-sendtask-outbox.md`); the two can land in either order.
