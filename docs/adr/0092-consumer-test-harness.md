# 0092 — Consumer test harness (`processtest`) + supporting seam exports

- **Status:** Accepted
- **Date:** 2026-07-04

## Context

`wrkflw` is a library: the deliverable is the module-root API a consumer embeds.
A consumer testing one of *their* process definitions today must hand-roll the
delivery loop — `Run` parks the instance, then they inspect `state.Tokens` /
`state.Tasks` / `state.Timers` to learn *why* it parked, build the matching
trigger (human `Claim`/`Complete`, `TimerFired`, `SignalReceived`,
`MessageReceived`), call `Deliver`, and repeat until a terminal status. Every
`examples/scenarios/*/main.go` repeats this boilerplate.

The only reusable test doubles that ship are `kernel.MemStore`,
`kernel.MemScheduler`, `authz.AllowAll`, `action.MapCatalog`/`Registry`, and the
in-memory `humantask` store. There is no consumer-facing drive helper, no
capturing spies for the action catalog or authorizer, and no externally
injectable email fake — the `action/email` sender seam (`sender`/`WithSender`) is
unexported, so a consumer cannot capture sent mail without a live SMTP server.

The production-readiness backlog (2026-06-30) names this gap: "No consumer test
harness — ship fakes + a `DriveToCompletion(ctx, runner, def, id)` helper."

## Decision

Ship a new public root package `processtest` and one small additive seam change.

1. **`processtest` package** — a consumer test harness with two entry points over
   one shared drive loop:
   - A stateful `Harness` fixture (`New(opts...)`) that owns an in-memory stack
     (`MemStore`, a `FakeClock`, `MemScheduler`, spy catalog/authorizer, task
     store + `TaskService`, optional signal bus) and exposes `Start` +
     `DriveToCompletion(ctx, def, id, handler)` methods plus accessors for
     assertions.
   - A lower-level free `DriveToCompletion(ctx, driver, def, state, handler)` for
     consumers who built their own `ProcessDriver`.
   The loop classifies each park into a rich `Park` value (a convenience `Reason`
   plus discrete open-tasks / awaiting-signals / awaiting-messages / armed-timers
   / incidents fields) and delegates the decision to a pluggable `ParkHandler`
   returning a `Decision` (`Deliver`, `AdvanceTimers`, `Stop`, `Abort`, `Pass`).
   Ready-made combinators (`AutoTimers`, `CompleteTasks`, `PublishSignal`,
   `DeliverMessage`, `Chain`) keep common flows one-liners. A `DriveLimit`
   (default 1000) plus sentinels (`ErrUnhandledPark`, `ErrNoPendingTimer`,
   `ErrDriveLimitExceeded`, `ErrAdvanceTimersUnsupported`) bound and explain
   non-progress. The package is error-returning and imports neither `testing` nor
   `clockwork`, so it also powers godoc `Example`s. Standalone fakes `SpyCatalog`,
   `SpyAuthorizer`, and `CaptureSender` are exported for direct use.

2. **Add `MemScheduler.NextFireAt() (time.Time, bool)` and
   `MemScheduler.Pending(timerID) (time.Time, bool)`** to `runtime/kernel`.
   `NextFireAt` (earliest pending fire time) drives `AdvanceTimers`;
   `Pending` (fire time of a specific timer id) lets the harness classify a parked
   command-wait token as a timer park precisely — by matching the token's own
   `AwaitCommand` against a scheduled timer id — rather than treating any pending
   timer as this instance's. Both are needed because `InstanceState.Timers`'
   element type is unexported and the scheduler is the only externally visible
   source of intermediate-timer fire times. The `Scheduler` interface is unchanged
   (both methods are concrete on the reference in-memory scheduler).

`engine` and `definition/model` remain zero-diff.

## Consequences

- Consumers can unit-test a definition to completion in a few lines, fully
  in-memory and deterministic — no Docker, no hand-rolled delivery loop.
- `action/email` stays zero-diff: the existing exported `email.SenderFunc` adapter
  already lets an external package inject a capturing sender (the black-box
  `email_test` tests do this), so `CaptureSender` is built on it with no email
  package change.
- `processtest` carries its own `FakeClock` rather than re-exporting `clockwork`,
  keeping the public API vendor-neutral and consistent with the `clock.Clock`
  discipline. The cost is a second (trivial) fake-clock implementation in the
  repo; it reads `Now()` only, which is all `MemScheduler` needs.
- The park classifier returns a single primary `Reason` for ergonomics but also
  exposes discrete fields, so multi-park nodes (e.g. a user task with a boundary
  timer) remain fully drivable by a custom handler. Async call-activity parks are
  surfaced as `ReasonAsyncChild` and are not auto-driven in v1.
- New public surface must be maintained under the project's API-stability
  expectations; it is additive and does not alter engine semantics.

## References

- Spec: `docs/specs/2026-07-04-consumer-test-harness.md`
- Backlog item: `docs/plans/2026-06-30-production-readiness-backlog.md` (P2 DX,
  "No consumer test harness")
- Builds on ADR-0091 (`action.Action` rename), ADR-0067 (message outbox),
  ADR-0003 (`clock.Clock`).
