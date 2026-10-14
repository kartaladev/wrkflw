# Design

## Context

Measured on `main` at `9c6dd2c1`.

**The bus contract this change works inside** (already in `event-delivery`; not changed here):
- A topic that has had a subscription and now has none returns `eventing.ErrNoSubscription` (*Publishing to a topic whose subscriptions have all ended is reported*).
- A topic never subscribed returns nil and the envelope is dropped (the same requirement's negative control). `eventing.WithRequireSubscription(topic, more...)` escalates named topics to `ErrNoSubscription` from the first publish. Its godoc records why an "every topic" mode was rejected: it was measured breaking `send_task_routing` under kartaladev/wrkflw#132.
- The relay marks a row published only on a nil `Publish` (*The relay publishes pending outbox rows and marks them published*). A never-subscribed topic is therefore marked published and lost.

**The four examples:**

| Example | bus | relay | subscriptions at `9c6dd2c1` | topics written, unconsumed |
| --- | --- | --- | --- | --- |
| `production_wiring` | `:127` | `persistence.NewRelay` `:172`, `Run` `:177` | 0 | all three terminal topics |
| `sqlite_wiring` | `:129` | `persistence.NewSQLiteRelay` `:136`, `Run` `:141` | 0 | all three terminal topics |
| `mysql_wiring` | `:113` | `persistence.NewMySQLRelay` `:117`, `Run` `:122` | 0 | all three terminal topics |
| `scenarios/send_task_routing` | `:138` | `NewSQLiteRelay` `:178`, `DrainOnce` | `message.OrderPlaced` (`:146`) | `instance.completed` |

`chaining_wiring` subscribes all three terminal topics through `Chainer.Start` (`:343`) and is the model the others should match.

**Coverage of the examples.** `examples/` is its own module (`affd6ae3`, #224). CI's Build and Test steps expand `scripts/modules.sh` to every workspace module, so the examples are compiled on every run. Only `examples/migrate` has a `_test.go`. #221's worry that the module split would drop the examples' only guard is stale.

**The README passage** (`runtime/README.md:622-627`) names only `Chainer.Run`. The godoc on `eventing.Chainer` (`eventing/chaining.go`) already says to prefer `Start` and when `Run` still applies. The `instance-chaining` spec pins `Start`'s readiness edge (*Chainer.Start returns only once all three terminal topics are live*).

**The two tests.**
- `TestInProcessStopJoinsItsDeliveryLoop` (`eventing/inprocess_test.go:335`) receives `<-entered` at `:354` with no deadline. The same bare receive appears at `:756`, `:815`, `:882` and `:1185`.
- `TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff` (`:575`) proves its claim only through the `time.After(2 * time.Second)` arm at `:602-603`. `InProcess.Close` does not join delivery loops (its godoc says so), so after `Close` returns, `done` may still be empty. A non-blocking receive straight after `Close` would be racy and is not an option.
- `scripts/check-test-timeout.sh` reports `eventing` at 10 raw deadlines costing 21s, and 79s in total, which is `ok`.

## Goals / Non-Goals

**Goals:**
- No shipped example publishes a terminal or message topic into a bus that never subscribes it without saying so.
- The README's turnkey chaining passage recommends the readiness-safe entry point and matches the shipped godoc.
- `TestInProcessStopJoinsItsDeliveryLoop` fails as itself, not as a package timeout, when its handler never runs.
- The Close-in-backoff test establishes its claim by an observed value, with the deadline kept only as a hang backstop.

**Non-Goals:**
- Changing `InProcess`, `Relay`, `ProcessDriver` or any other exported behavior, under the recommended reading of #221.
- Adding tests that boot Postgres or MySQL for the wiring examples.
- Re-litigating the never-subscribed default that #132 settled on evidence.

## Decisions

### D1. The README follows the shipped godoc; no new decision about `Run`

#222 deferred "is `Run` legacy?" to #134's authors. Their answer shipped as godoc. `Chainer` says to prefer `Start`, and says it is the only one of the two that is safe behind an outbox relay. `Chainer.Run` says it "remains the entry point for a broker that offers only Subscribe". The README is rewritten to that position: `Start` first, the readiness edge as the reason, and `Run` only for a `Subscriber` that is not a `Starter`, where a publish that races it is lost.

Rejected:
- **Demote `Run` as legacy.** That contradicts the shipped godoc and would need its own decision.
- **Keep `Run` and add a cross-reference only.** That leaves the racy path as the README's first suggestion, which is the defect.

### D2. #190's new deadline is a Timeout construct

The added wait guards a signal (`entered`), and its deadline clause fails the test. That is the **Timeout** row of `docs/agents/test-deadlines.md`, paid only on failure, so it can be generous. It still counts against the per-package ceiling. Use a literal duration, because the guard rejects non-literal ones. Re-run `scripts/check-test-timeout.sh` and record the new `eventing` row in the PR. `require.Eventually` was rejected for the same reason that document gives: this is a channel signal, not a polled condition.

### D3. Example wirings, under the recommended reading of #221 (pending decision)

Recommended, not decided:
- Each of the three wirings starts a subscription on the three terminal topics with `InProcess.Start` before `relay.Run` begins, so the readiness edge precedes the first drain. The handler is a minimal consumer that logs the event and acks, and its stop is deferred.
- The bus is built with `eventing.WithRequireSubscription` naming those three topics. A later edit that removes the subscription then shows up as retrying outbox rows and an ERROR log line, not silence.
- `send_task_routing` either subscribes `instance.completed` or carries a comment saying its terminal rows are deliberately unconsumed. Prefer the comment: the example is about message routing, and the comment is exactly the unconsumed-by-design case the bus cannot tell apart.

Why this reading over the others:
- **Reading 1 alone (examples only, no escalation).** It fixes today's examples but leaves the next edit free to remove the subscription silently.
- **Reading 2 (a runtime consumed-topics declaration).** It is the only reading that touches `runtime/`. It changes which outbox rows are written, which is a persisted-format consideration. It also cannot know about consumers on an external broker, where the outbox is the durable hand-off and the broker, not wrkflw, knows who consumes. No consumer has asked for it. Deferred, not rejected.
- **Reading 4 (document only).** The bus-level drop is already documented in godoc. Documenting it again in the examples while they keep modelling it is the weakest option, and #221 argues against it.

Evidence: the never-subscribed silent drop is pinned by `TestPublishToATopicNobodyEverSubscribedStaysSilent`. The escalation is pinned by `TestPublishWithRequireSubscriptionReportsNoSubscription` and by `TestRequireSubscriptionErrorReachesTheOutboxPublisherBoundary` at the relay seam. The recommendation relies only on that pinned behavior.

### D4. #191: a value assertion, pending the seam decision

The claim is that `Close` ends a subscription parked in the backoff. The hypothesised M1 deletes the `case <-b.done` arm of the backoff `select` in `dispatch`. Under that mutant the subscription wakes after the 30s backoff and calls the handler again. Under correct code the handler is never called after `Close`. The discriminating value is therefore **the handler's attempt count after the backoff would have elapsed**. Reading it without waiting 30s of wall clock needs control of the backoff timer.

Recommended (pending Open Question 2): an unexported clock or timer hook in `eventing`, set only from `inprocess_internal_test.go`, so the test can:
1. `Close`.
2. Fire the backoff.
3. Wait for `done`, with the deadline kept as a backstop.
4. Assert the attempt count is still the pre-Close value and Subscribe returned nil.

The first task reproduces M1 against the current test and records which arm kills it.

## Cross-change coupling

- **`split-persistence-modules`** (kartaladev/wrkflw#39, #207, #220) moves `persistence` and so rewrites imports in the same example files. Either order works. Whichever change lands second rebases the examples, and neither owns the other's lines.
- **`export-refusal-sentinels`** modifies the `event-delivery` requirement *Publish failures are returned to the caller*. This change modifies no `event-delivery` requirement under the recommended reading, so there is no spec overlap. If reading 2 is chosen, this change ADDs a new requirement and touches none of that change's.
- **`align-godoc-with-behavior`** edits `runtime/chain/chainer.go` godoc. This change edits `runtime/README.md`. The files are disjoint.
- **`harden-ci-guard-wiring`** owns `scripts/check-test-timeout.sh`. D2 only adds a site that the script counts.

## Risks / Trade-offs

- [The examples now log every terminal event] → Use a DEBUG or INFO line per event. These are demo wirings, not production defaults.
- [`WithRequireSubscription` makes a mis-ordered example dead-letter rows] → D3 orders `Start` before `relay.Run`. That is the point of choosing `Start` over `Subscribe`.
- [A test-only seam in `eventing` widens the internal surface] → Keep it unexported and set it only from an `_internal_test.go`. Open Question 2 can still reject it.
- [The M1 reading is a hypothesis] → Task 3.1 reproduces it before anything is changed.

## Migration Plan

None. No consumer-visible behavior changes under the recommended reading.

## Open Questions

1. **#221: which reading?** The options are 1 (examples only), 2 (a runtime consumed-topics declaration), 3 (`WithRequireSubscription` in the examples) and 4 (document and leave). **Recommendation: 1 and 3 together, as D3 describes.** If 2 is chosen, this change gains an `event-delivery` ADDED requirement and loses `skip_specs`, and the persisted-format flag must be re-assessed.
2. **#191: may the test use an unexported timer seam in `eventing`?** Options: (a) an unexported hook set only from an internal test (recommended); (b) black-box only, so the deadline remains the kill and a comment records why; (c) shorten the backoff and assert the attempt count after a negative window, which turns the cost into a per-green-run window and is still timing-shaped. (c) is not recommended.
3. **#190: bound the other bare `<-entered` receives?** The sites are `:756`, `:815`, `:882` (#170's, ruled on there) and `:1185` (#196's, with no recorded ruling). **Recommendation:** bound `:1185` as well, since it has the same blind spot and no ruling, and leave the #170 sites to that ruling. The alternative is to keep #190's scope at `:354` only.
