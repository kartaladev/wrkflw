# 0066. Make clock.Clock optional via a With<Component>Clock option

- Status: Accepted
- Date: 2026-06-26

## Context

Per ADR-0003 stateful components depend on the in-repo `clock.Clock` interface. Many
constructors took the clock as a required positional parameter. The consumer wants the
clock to be optional — when not provided, default to the system clock (`clock.System()`).
Several components already expose the clock as a `With<Component>Clock` option defaulting
to `clock.System()`; this decision makes that pattern uniform across the codebase.

## Decision

Move every positional `clock.Clock` parameter to a `With<Component>Clock(clk clock.Clock)`
functional option. The constructor initialises the clock field to `clock.System()` before
applying options; the option setter ignores a nil clock (an explicit nil falls back to the
system clock). Naming follows `With<Component>Clock` because Go forbids two `WithClock`
functions in one package and several packages host multiple clock-bearing constructors.

The `clockwork.Clock` gocron-adapter constructors are excluded — they take a different type
at the deliberately-confined vendor seam.

## Consequences

- The clock argument becomes optional and self-documenting at the call site; production
  wiring can omit it.
- This relaxes ADR-0003's compile-time guarantee that every caller passes a clock. A test
  that forgets to inject its fake clock will no longer fail to compile; it will run against
  wall time and may be non-deterministic. Mitigation: determinism-sensitive tests continue
  to inject a fake via `With<Component>Clock(fake)`; reviewers must ensure new deterministic
  tests do the same.
- The engine core (`engine/`, `model/`) is unaffected — it receives time via triggers/
  commands and never constructs a clock, so its wall-clock-free determinism is preserved.
- Amends the injection convention of ADR-0003 (does not supersede it).
