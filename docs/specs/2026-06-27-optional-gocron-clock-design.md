# Spec — Make the gocron/clockwork clock seam optional via options

Date: 2026-06-27
Status: Accepted (autonomous backlog program, track H3)
Relates to: ADR-0066 (clock.Clock optional via With<Component>Clock), ADR-0003 (clock seam)

## Problem

ADR-0066 moved every positional `clock.Clock` parameter onto a `With<Component>Clock`
functional option (default `clock.System()`, nil-guarded) — but **explicitly excluded** the
gocron/clockwork seam because those constructors take `clockwork.Clock` (the vendor type),
not the in-repo `clock.Clock`. So two public/internal constructors remain positional-clock
outliers, inconsistent with the rest of the API:

- `scheduling.NewScheduler(clk clockwork.Clock, opts ...Option)` (public façade)
- `internal/scheduling/gocron.NewGocronScheduler(clk clockwork.Clock, opts ...Option)` (adapter)

(The elector already uses an option, `WithElectorClock(clk clockwork.Clock)`, nil-guarded.)

## Goal

Complete the ADR-0066 pattern for the gocron seam: move the positional `clockwork.Clock` onto
a `With…Clock` option defaulting to a real clock, nil-guarded. API consistency; no behaviour
change when a clock is supplied.

## Non-goals

- Removing `clockwork.Clock` from the scheduling signatures (it is the gocron vendor seam; the
  in-repo `clock.Clock` is deliberately not used here — out of scope).
- Touching `WithElectorClock` (already an option) beyond confirming the nil-guard.

## Design

### Public façade — `scheduling`

- `NewScheduler(opts ...Option) (*Scheduler, error)` — drop the positional `clk`.
- New `WithSchedulerClock(clk clockwork.Clock) Option` — stores the clock on `config`; nil is
  ignored (falls back to default). Default when unset: `clockwork.NewRealClock()`.
- In `NewScheduler`, resolve the effective clock once (`cfg.clk` or `clockwork.NewRealClock()`),
  then (a) pass it to the internal scheduler via the new internal clock option, and (b) keep the
  existing elector clock-sharing: prepend `gocronsched.WithElectorClock(resolvedClk)` to
  `cfg.electorOpts` exactly as today (caller-supplied elector clock still wins, applied after).

### Adapter — `internal/scheduling/gocron`

- `NewGocronScheduler(opts ...Option) (*GocronScheduler, error)` — drop the positional `clk`.
- New `WithClock(clk clockwork.Clock) Option` (package-local; clear within `package gocron`) —
  nil-guarded. Default when unset: `clockwork.NewRealClock()`.
- `NewGocronScheduler` resolves the effective clock (default real), assigns `s.clk`, and passes
  `gocron.WithClock(resolvedClk)` to gocron exactly as today.

### Determinism footgun (same as ADR-0066, document it)

Omitting the clock option now silently uses **real time** (no compile error). Determinism-
sensitive tests MUST pass `WithSchedulerClock(fake)` / `WithClock(fake)`. Every existing test
that passed a fake clock positionally must be re-threaded through the option.

## Affected callers (must all be updated)

- Production: `examples/production_wiring/main.go:85` → `WithSchedulerClock(clk)`.
- Internal wiring: `scheduling/scheduler.go` calls `NewGocronScheduler(clk, …)` → option form.
- Tests (~30 sites): `scheduling/{scheduler,locker,elector,runner_e2e}_test.go` and
  `internal/scheduling/gocron/{scheduler,scheduler_logger,elector_wiring,locker_wiring}_test.go`
  — all pass `clk`/`fakeClock`/`fc` positionally; convert to the option. These re-threadings are
  the safety net for the footgun above.

## Testing

- New: `WithSchedulerClock(nil)` falls back to real clock (no panic); supplying a fake clock makes
  scheduled jobs fire on fake-clock advance (mirror an existing fake-clock scheduler test).
- New: `gocron.WithClock(nil)` falls back to real; fake clock drives firing.
- All existing scheduler/elector/locker tests still green after re-threading.

Gate: `go test -race ./scheduling/... ./internal/scheduling/...` green (testcontainers for
elector/locker), touched pkgs ≥85% coverage, `golangci-lint run ./...` clean, gofmt clean,
`go build ./...` (incl. examples) compiles.

## Risk

Breaking constructor signature change (consistent with the many ADR-0066 breaking ctor changes).
Mechanical; the comprehensive caller list above bounds it. The elector clock-sharing logic is the
one non-mechanical spot — preserve it exactly.
