# Tasks

`skip_specs: true` under the recommended reading of kartaladev/wrkflw#221. No task makes a new scenario pass. Each task names the existing scenario it relies on, where one exists. **Do not start group 1 or task 3.2 until design Open Questions 1 and 2 are answered.** If reading 2 is chosen, re-plan group 1 with an `event-delivery` delta spec first.

Run every command from the repository root with `GOTOOLCHAIN=go1.26.9`. `./...` covers only the root module, so the examples module is addressed explicitly.

## 1. Examples stop publishing into a void (kartaladev/wrkflw#221, design D3, pending Open Question 1)

- [ ] 1.1 Re-measure first. List every `NewInProcess` bus in `examples/`, the topics its relay or driver can write, and the topics it subscribes. Confirm that the table in design Context is still complete. Verify: `grep -rn "NewInProcess\|\.Start(ctx\|\.Subscribe(" examples` matches the table.
- [ ] 1.2 `examples/sqlite_wiring/main.go`:
  - Start the three terminal topics with `InProcess.Start` before `relay.Run`, using a minimal logging, acking handler, and defer its stop.
  - Build the bus with `eventing.WithRequireSubscription` naming the three topics.
  - Add a comment explaining both.

  Relies on: event-delivery, *InProcess.Start returns only once the subscription is live* and *Publishing to a topic whose subscriptions have all ended is reported*. Verify: `go build ./examples/...` and `go vet ./examples/...` pass.
- [ ] 1.3 Apply the same wiring to `examples/production_wiring/main.go` and `examples/mysql_wiring/main.go`. Verify: the same commands pass.
- [ ] 1.4 `examples/scenarios/send_task_routing/main.go`: add a comment at the bus construction saying its `instance.completed` rows are deliberately unconsumed, and why. Alternatively, subscribe the topic if the decision says so. Verify: `go run ./examples/scenarios/send_task_routing` (SQLite, no Docker) exits 0 and logs no `eventing: publish failed`.
- [ ] 1.5 Smoke-run `examples/sqlite_wiring` and confirm a terminal event reaches the new handler. Do it locally with a temp `SQLITE_DSN`, start one instance, see the handler's log line, then stop. Record the output in the PR, not in a test.

## 2. README turnkey chaining passage (kartaladev/wrkflw#222, design D1)

- [ ] 2.1 Re-locate the passage in `runtime/README.md`; it was `:622-627` at `9c6dd2c1`. Rewrite it:
  - Recommend `eventing.NewChainerRunner(chainer).Start(ctx, bus)`, which returns only once all three terminal topics are live.
  - State the difference from `Run` as that readiness edge.
  - Say `Run` is for a `Subscriber` that is not a `Starter`, and that a publish racing it is lost behind an outbox relay.
  - Say that `ctx` must be lifetime-scoped.

  Relies on: instance-chaining, *Chainer.Start returns only once all three terminal topics are live*. Verify: the passage names `Start` before `Run`, and `scripts/check-doc-refs.sh` passes.

## 3. eventing test hygiene (kartaladev/wrkflw#190, kartaladev/wrkflw#191, design D2 and D4)

- [ ] 3.1 Red for #191: apply the hypothesised M1 by deleting the `case <-b.done` arm from the backoff `select` in `dispatch` (`eventing/inprocess.go`). Run `TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff` and record that the deadline arm is the one that kills it. Restore the code. If M1 turns out to be a different mutation, record which one and stop for review. Relies on: event-delivery, *Close ends a subscription parked in the redelivery backoff*. Verify: `go test ./eventing -run 'TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff' -count=1` passes on restored code, and `git diff --stat -- ':!*_test.go'` is empty.
- [ ] 3.2 Strengthen the test per the answer to Open Question 2. Under (a), add the unexported timer hook. Then assert that after `Close` and a fired backoff, the handler's attempt count is unchanged and Subscribe returned nil, keeping the deadline only as a backstop. Watch the new assertion go red under M1, then restore. Verify: the same `go test` command passes, and the PR records the red run.
- [ ] 3.3 Red for #190: temporarily drop the `close(entered)` call in `TestInProcessStopJoinsItsDeliveryLoop`. Watch the test hang to `-timeout` rather than fail as itself (use `-timeout=20s`). Then bound the `<-entered` at `:354` with a `select` whose deadline arm calls `t.Fatal` naming the handler. Classify it as a Timeout per `docs/agents/test-deadlines.md`. Watch the sabotaged test now fail as itself, then restore. If Open Question 3 says so, do the same at `:1185`. Verify: `go test ./eventing -run 'TestInProcessStopJoinsItsDeliveryLoop' -count=1` passes, and `bash scripts/check-test-timeout.sh` reports `eventing` as `ok`. Record its row in the PR.
- [ ] 3.4 Run the package under race. Verify: `go test -race ./eventing -count=3` passes.

## 4. Close out

- [ ] 4.1 Verify: `scripts/check-openspec.sh`, `openspec validate clarify-terminal-event-delivery --strict` and `go build $(scripts/modules.sh)` pass.
- [ ] 4.2 The PR names this change, and its last commit runs `openspec archive clarify-terminal-event-delivery`.
