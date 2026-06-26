# 0069. Make the gocron/clockwork clock seam optional via options

Status: Accepted — 2026-06-27
Follow-up to: ADR-0066 (clock.Clock optional via With<Component>Clock), ADR-0003 (clock seam).

## Context

ADR-0066 made every `clock.Clock` parameter a `With<Component>Clock` functional option
(default `clock.System()`, nil-guarded) for API consistency and test ergonomics — but
**explicitly excluded** the gocron seam, because `scheduling.NewScheduler` and
`internal/scheduling/gocron.NewGocronScheduler` take the vendor `clockwork.Clock`, not the
in-repo `clock.Clock`. Those two constructors were left as the only positional-clock outliers.
The elector already exposed `WithElectorClock(clockwork.Clock)`.

## Decision

Complete the ADR-0066 pattern for the gocron seam, keeping the `clockwork.Clock` type (it is the
gocron vendor seam):

- `scheduling.NewScheduler(opts ...Option)` — positional `clk` removed; new
  `WithSchedulerClock(clk clockwork.Clock)` option, nil-guarded, default `clockwork.NewRealClock()`.
- `internal/scheduling/gocron.NewGocronScheduler(opts ...Option)` — positional `clk` removed; new
  `WithClock(clk clockwork.Clock)` option, nil-guarded, default `clockwork.NewRealClock()`.
- The scheduler still shares its effective clock with the leader elector (prepends
  `WithElectorClock(resolvedClk)` to caller elector opts) — unchanged behaviour.

All callers (production example + scheduler internal wiring + ~30 test sites) are re-threaded onto
the options.

## Consequences

- **BREAKING** constructor signatures: `NewScheduler`/`NewGocronScheduler` no longer take a
  positional clock. Advise against the old positional form. Consistent with ADR-0066's breaking
  ctor changes.
- **Footgun (same as ADR-0066):** omitting the clock option silently uses real time — no compile
  error. Determinism-sensitive tests MUST pass `WithSchedulerClock(fake)` / `WithClock(fake)`.
  Every prior positional fake-clock test was re-threaded through the option (the re-threading is
  the verification that the footgun was not triggered).
- The `clockwork.Clock` type stays in the scheduling signatures (vendor seam, by design). The
  in-repo `clock.Clock` continues to be unused at this layer.
- No behaviour change when a clock is supplied; engine/model untouched.
