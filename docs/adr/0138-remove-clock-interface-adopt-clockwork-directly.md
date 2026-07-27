# 138. Remove the in-repo Clock interface; depend on clockwork directly

- Status: Accepted — **supersedes [ADR-0003](0003-clockwork-as-time-source.md)**
- Date: 2026-07-24

## Context

ADR-0003 defined a one-method in-repo `clock.Clock` interface (`Now() time.Time`)
as the engine's sole time abstraction, with `github.com/jonboulle/clockwork` as its
implementation confined to wiring and tests. The goal was vendor-swappability,
mirroring the watermill/casbin/gocron rule, and a single fake clock driving both
engine logic and the gocron scheduler in tests.

Two facts have since made that abstraction net-negative:

1. **It cannot express waiting.** `clock.Clock` exposes only `Now()`. Any component
   needing a ticker, timer, or after-channel bypasses the interface and calls
   `time.NewTicker` / `time.NewTimer` / `time.After` directly. Five production sites
   do exactly this (`runtime/calllink/notifier.go`, `internal/persistence/store/relay.go`,
   `internal/persistence/store/notifier_pgx.go`, `internal/authz/casbin/pg_watcher.go`,
   `internal/expreval/expreval.go`). Their tests are timing-dependent and flaky
   because no fake can drive the wait.
2. **`clockwork.Clock` is a strict superset** of `clock.Clock` (it adds `After`,
   `Sleep`, `Since`, `Until`, `NewTicker`, `NewTimer`, `AfterFunc`) and ships a
   fully deterministic `FakeClock` for all of them. `clockwork` is already a locked,
   pinned dependency (`v0.5.0`). The one-method wrapper buys a swappability the
   project has decided it does not need, while withholding the fake-ticker/timer
   capability we do need.

The project owner has directed removal of the wrapper in favor of depending on
`clockwork.Clock` directly. This reverses ADR-0003's "never import clockwork from
engine/workflow code" rule and the corresponding CLAUDE.md tech-stack constraint,
so it is recorded here as a superseding decision and the governing documents are
updated in the same change bundle.

Critically, ADR-0003's other pillar is retained: the **pure engine core never held
a clock at all** — it reads no wall clock and receives time only as
`Trigger.OccurredAt`. Removing `clock.Clock` therefore touches only the *outer*
stateful layers (runtime, persistence, service, internal adapters), never the core.

## Decision

We will **delete the `clock` package** and depend on `clockwork.Clock` directly in
every stateful component that needs time.

- `clock.Clock` fields and `With…Clock` option parameters become `clockwork.Clock`.
  The real default becomes `clockwork.NewRealClock()` (was `clock.System()`).
- All five production waiting-primitive sites take their ticker/timer/after from the
  injected `clockwork.Clock`, so a `clockwork.FakeClock` drives them deterministically
  in tests. Three components that hold no clock today (`expreval`, the pgx
  notifier, the casbin watcher) gain one via an additive functional option defaulting
  to the real clock.
- The public `processtest.FakeClock` is reconciled with `clockwork`'s fake
  (it must now satisfy the wide interface); `clockwork.NewFakeClockAt` backs it.
- **Engine-core purity is unchanged.** `engine/purity_test.go` keeps `"clockwork"`
  in its denied-import list and keeps the no-wall-clock guard: the core stays
  clockwork-free and time still enters it only as `Trigger.OccurredAt`. The reversal
  applies to the outer layers exclusively.
- CLAUDE.md is amended in the same bundle: the "Time source" tech-stack row and
  Common Pitfall #3 no longer forbid importing `clockwork` from outer workflow code
  (watermill/casbin/gocron remain forbidden-direct). The shared-fake-drives-both
  property (engine + scheduler) is retained and restated in terms of `clockwork.Clock`.

## Consequences

- **Deterministic waiting.** Tickers, timers, and reconnect backoffs are now
  fast-forwardable with `FakeClock.Advance`, removing a class of flaky, wall-clock
  tests. This is the primary benefit.
- **Vendor lock-in on clockwork is accepted** for the outer layers. Swapping the
  time vendor would now touch every stateful component rather than one adapter. The
  owner has judged this acceptable given clockwork's stability and pinned status.
  The pure core, however, remains vendor-free and swappable by construction.
- **Breaking public API.** Consumers importing `github.com/kartaladev/wrkflw/clock`
  or passing a `clock.Clock` must switch to `clockwork.Clock`; the
  `With…Clock` signatures change type. No compatibility shim is provided (per the
  removal directive). Recorded in the CHANGELOG as breaking.
- **`processtest` surface may change.** `clockwork.FakeClock` has `Advance` but no
  backward-jump `Set`; any test relying on `Set` to move time backward is migrated
  during implementation (reformulate as forward `Advance`, or a thin wrapper retains
  `Set`). Any resulting `processtest` change is documented as breaking.
- **Governance is now consistent.** ADR-0003 is superseded rather than contradicted;
  CLAUDE.md matches the code, so the "never import clockwork" rule no longer conflicts
  with the outer layers that now do.
- **Store timestamp writes stay on the wall clock** (`time.Now().UTC()` for
  `created_at` etc.): they are not waiting primitives and are out of scope; their
  values remain non-deterministic, which no test asserts on via a clock.
