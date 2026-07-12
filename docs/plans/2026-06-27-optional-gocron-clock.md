# Plan — H3: Make the gocron/clockwork clock seam optional

Spec: `docs/specs/2026-06-27-optional-gocron-clock-design.md`. ADR: `docs/adr/0069-optional-gocron-clock.md`.
Branch: `refactor/optional-gocron-clock`. Module: `github.com/kartaladev/wrkflw`.

## Global Constraints (binding — copy to reviewers verbatim)

- TDD: add the new option behaviour tests (nil-fallback, fake-clock-drives-firing) FIRST and see
  them fail/compile-fail before implementing. Pure caller re-threading of existing tests needs no
  new red but must stay green before AND after.
- Keep the `clockwork.Clock` TYPE in the signatures (vendor seam) — do NOT switch to `clock.Clock`.
- Default clock when option unset/nil = `clockwork.NewRealClock()`. nil-guard: explicit nil → default.
- Preserve the elector clock-sharing exactly: `NewScheduler` resolves the effective clock once and
  prepends `gocronsched.WithElectorClock(resolvedClk)` to `cfg.electorOpts` (caller opts applied
  after, so a caller-supplied elector clock still wins).
- engine/ and model/ untouched.
- Option names EXACTLY: `scheduling.WithSchedulerClock`, `gocron.WithClock`.
- Gate: `go test -race ./scheduling/... ./internal/scheduling/...` green; touched pkgs ≥85%;
  `golangci-lint run ./...` clean; gofmt clean; `go build ./...` (incl. examples) compiles.
- Project skills: table-test (assert-closure, `t.Context()`), black-box `_test` where practical.

## Task 1 — internal gocron adapter: `WithClock` option

Files: `internal/scheduling/gocron/scheduler.go` (+ its options file if separate), tests in package.
- Red: test `NewGocronScheduler(WithClock(fake))` fires a scheduled job when the fake clock advances;
  and `NewGocronScheduler(WithClock(nil))` (or no option) does not panic and uses a real clock.
  (These won't compile until the signature changes — that's the red.)
- Green: add `WithClock(clk clockwork.Clock) Option` (nil-guarded); change `NewGocronScheduler` to
  `(opts ...Option)`, resolve effective clock (default `clockwork.NewRealClock()`), assign `s.clk`,
  pass `gocron.WithClock(resolved)` to gocron as today.
- Re-thread the package's own tests (`scheduler_test.go`, `scheduler_logger_test.go`,
  `elector_wiring_test.go`, `locker_wiring_test.go`) from positional `clk` → `WithClock(clk)`.

## Task 2 — public façade: `WithSchedulerClock` option

Files: `scheduling/scheduler.go` (+ options file), `scheduling/*_test.go`.
- Red: test `scheduling.NewScheduler(WithSchedulerClock(fake), …)` schedules/fires on fake advance;
  `WithSchedulerClock(nil)` falls back to real clock without panic.
- Green: add `WithSchedulerClock(clk clockwork.Clock) Option` storing on `config` (nil-guarded);
  change `NewScheduler` to `(opts ...Option)`; resolve effective clock once; pass it to
  `NewGocronScheduler(append(internalOpts, gocronsched.WithClock(resolved))...)`; keep the elector
  prepend using the resolved clock.
- Re-thread `scheduling/{scheduler,locker,elector,runner_e2e}_test.go` positional `clk`/`fakeClock`/`fc`
  → `WithSchedulerClock(...)`.

## Task 3 — production example + final sweep

Files: `examples/production_wiring/main.go`.
- Update line ~85: `NewScheduler(clk, WithLogger(logger))` → `NewScheduler(WithSchedulerClock(clk), WithLogger(logger))`.
- Grep the whole tree for any remaining positional callers of either constructor; fix.
- `go build ./...` (including examples) must compile.

## Verification checklist

- [ ] T1 internal `WithClock` red→green; package tests re-threaded + green.
- [ ] T2 façade `WithSchedulerClock` red→green; tests re-threaded + green.
- [ ] T3 example updated; full-tree grep clean; `go build ./...` ok.
- [ ] elector clock-sharing preserved (a distributed-elector test still drives off the fake clock).
- [ ] `go test -race ./scheduling/... ./internal/scheduling/...` green; ≥85%; lint 0; gofmt clean.
- [ ] ADR-0069 + spec committed; HANDOVER updated; whole-branch opus review clean.
