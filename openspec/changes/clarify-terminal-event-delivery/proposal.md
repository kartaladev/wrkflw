## Why

Three shipped wirings run an outbox relay into an `eventing.NewInProcess` bus that nothing subscribes, so every terminal event they produce is published to nobody and marked published. Nothing in those examples says so. The turnkey chaining passage in `runtime/README.md` still points readers at `Chainer.Run`, which has no readiness signal. Two `eventing` tests also prove their claim weakly: one can hang until the package timeout, and one is killed only by a deadline expiring. Each of these makes the delivery contract look stronger than it is, either to a reader copying an example or to a reviewer reading a green test.

## What Changes

Every premise below was re-measured on `main` (`9c6dd2c1`) before it was listed.

- **kartaladev/wrkflw#221 (folded in; the decision is open).** The runtime still writes terminal outbox rows on every terminal edge (`runtime/processdriver.go:749-750`, `terminalOutboxEvent` at `runtime/outbox.go:33`, `outboundMessageEvents` at `:54`), with no notion of which topics have a consumer. `examples/production_wiring/main.go` (bus `:127`, `persistence.NewRelay` `:172`, `relay.Run` `:177`), `examples/sqlite_wiring/main.go` (`:129`, `:136`, `:141`) and `examples/mysql_wiring/main.go` (`:113`, `:117`, `:122`) still contain zero `Subscribe` or `Start` calls. `examples/scenarios/send_task_routing/main.go` subscribes only `message.OrderPlaced` (`bus.Start` at `:146`), so its `instance.completed` rows also reach nobody. The never-subscribed topic stays silent by the bus's own documented contract, so `Publish` returns nil and the relay marks the row published. The change makes the four examples stop modelling a lossy wiring. **Which of the issue's four readings to take is not decided** (proposal, *Decisions*; design, Open Questions). The recommendation is that the examples subscribe what they publish and escalate those topics with `eventing.WithRequireSubscription` (readings 1 and 3), with no runtime change.
- **kartaladev/wrkflw#195 is a duplicate of #221** (maintainer decision). It is not absorbed. Its text is #221's original draft and carries no detail #221 lacks; #221 adds the re-verification and the coverage argument.
- **kartaladev/wrkflw#222 (folded in).** `runtime/README.md:622-627` still offers `eventing.NewChainerRunner(chainer).Run(ctx, sub)` as *the* turnkey path and never names `Chainer.Start`. Rewrite it to recommend `Start`, state the difference as the readiness edge, and say when `Run` is still the right call. The issue left "is `Run` legacy?" to #134's authors. Their answer is already recorded in the shipped godoc on `eventing.Chainer` and `Chainer.Run`: prefer `Start` where the subscriber is also a `Starter`, and `Run` remains the entry point for a broker that offers only `Subscribe`. The README follows that godoc. No new decision is made.
- **kartaladev/wrkflw#190 (folded in).** `TestInProcessStopJoinsItsDeliveryLoop` still waits on a bare `<-entered` at `eventing/inprocess_test.go:354`, so a handler that never runs hangs the package until `-timeout` instead of failing this test. Bound it and classify the new deadline per `docs/agents/test-deadlines.md`. The issue's budget figures are **stale**: the `eventing` package now counts 10 raw deadlines costing 21s, and 79s in total (issue: 8, 18s and 70s). It still passes `scripts/check-test-timeout.sh`.
- **kartaladev/wrkflw#191 (folded in; the approach is open).** `TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff` (`eventing/inprocess_test.go:575`) still ends in a `select` whose only failing arm is a 2-second deadline (`:602-603`). Make a value assertion establish the claim and keep the deadline as a hang backstop. The mutation the issue calls "M1" is not recorded in the repo. Design reads it, as a **hypothesis**, as removing the `b.done` arm from the redelivery-backoff `select` in `eventing/inprocess.go`. How to observe a value without a deadline is open (design, Open Questions).

**Stale premises found on re-measurement**
- #221's "one reason not to leave this": examples are compiled only by a root `go build ./...`, and #39 would make `examples/` its own module and drop that guard. Stale. `examples/` became its own module in `affd6ae3` (#224), and CI's Build and Test steps iterate every workspace module through `scripts/modules.sh`. The examples are still guarded only by compilation; `examples/migrate` is the only package with a test.
- #221's reading 4 says the drop is "currently undocumented". Partly stale. The `InProcess`, `ErrNoSubscription` and `WithRequireSubscription` godoc now document the never-subscribed silent drop. The three examples still do not say so.
- #221's `chaining_wiring` row: that example now uses `Chainer.Start` (`examples/chaining_wiring/main.go:343`), not `Subscribe` at `:284`. The finding about the other three is unaffected.

**Decisions this change cannot be planned without (unresolved, Readiness: human)**
1. #221: which reading to take: examples only, a runtime "consumed topics" declaration, `WithRequireSubscription` in the examples, or document and leave. The recommendation (readings 1 and 3) changes no library behavior. Reading 2 would add a runtime option and an ADDED requirement in `event-delivery`, and `skip_specs` would have to be removed.
2. #191: whether a value assertion may use an unexported test seam (a clock or backoff hook) in `eventing`, or must stay black-box.
3. #190: whether the four other bare `<-entered` receives in the same file (`:756`, `:815`, `:882`, `:1185`) are bounded too. The first three were ruled on in #170's review (per #190's text, not re-read here). `:1185` arrived later with `e9ad4b36` (#196) and has no recorded ruling.

**Out of scope**
- Widening the bus to refuse every never-subscribed topic by default. It was measured breaking `send_task_routing` under #132 and rejected on that evidence. The `WithRequireSubscription` godoc records the rejection.
- The at-least-once framing of delivery that kartaladev/wrkflw#45 (closed) set out. This change does not revisit it.
- Moving the examples' imports when the persistence module splits. Owned by `split-persistence-modules` (kartaladev/wrkflw#39, #207, #220).
- Sizing real-I/O test deadlines. Owned by `size-io-test-deadlines` (kartaladev/wrkflw#98).
- CI guard wiring generally, including `check-test-timeout.sh`. Owned by `harden-ci-guard-wiring`.
- Chainer godoc on link-skip behavior. Owned by `align-godoc-with-behavior`.

**BREAKING:** none under the recommended reading. **Persisted formats:** not affected under the recommended reading. Reading 2 would change which outbox rows are written, so it would need its own flag. **Default security posture:** not affected. **Consumer go.mod:** not affected.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None under the recommended reading. The examples, the README and two tests change, and no library behavior does, so `skip_specs: true` is set. If reading 2 of #221 is chosen, this section must list `event-delivery` with an ADDED requirement for the consumed-topics declaration, and `skip_specs` must be removed.

## Impact

- **Examples:** `examples/production_wiring/main.go`, `examples/sqlite_wiring/main.go`, `examples/mysql_wiring/main.go`, `examples/scenarios/send_task_routing/main.go`.
- **Docs:** `runtime/README.md` (the turnkey chaining passage, currently `:622-627`).
- **Tests:** `eventing/inprocess_test.go` (`TestInProcessStopJoinsItsDeliveryLoop`, `TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff`). Possibly `eventing/inprocess_internal_test.go`, if decision 2 allows a seam.
- **Not affected:** exported identifiers, runtime behavior, persisted formats, the default security posture, any consumer's `go.mod`. All of this holds under the recommended reading.
