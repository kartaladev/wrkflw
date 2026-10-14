# Tasks

This is a cleanup, not a defect fix. The current test is green (`-race -count=20`), so no task reproduces a failure. Each group still writes its test before the code it covers.

## 1. Per-round barrier, test first

- [ ] 1.1 Write the new barrier test (design D3) against the per-round barrier API before the barrier exists, and watch it fail to compile. It has three rows. In the first, N−1 waiters park on an N-way round and expire through an injected, closed expiry channel; every waiter returns and the round reports failure. In the second, an N-way round assembles; every waiter returns and the round reports success (the negative control). In the third, N+1 arrivals reach an N-way round and none blocks or panics. Run it in a loop of at least 50 attempts, as today's giveUp test does. Verify: `GOTOOLCHAIN=go1.26.9 go test -race -run TestRoundBarrier ./action/httpcall` fails to compile.
- [ ] 1.2 Implement the per-round barrier (design D1, D3): a mutex-guarded arrival count, one `close` of `release` at the arrival that completes the round, and an expiry branch that only sets `failed`. Delete `giveUp`, the identity check, the `assembled` latch, `ok()`, the swap-under-lock invariant comment and `TestRoundBarrierGiveUpUnderManyWaiters`. Verify: 1.1 passes under `GOTOOLCHAIN=go1.26.9 go test -race -count=20 -run TestRoundBarrier ./action/httpcall`, and reading the barrier's methods finds exactly one `close` call.

## 2. Wire it into the pool test

- [ ] 2.1 Change `connCountingServer` to publish the current round's barrier through an atomic pointer and return a `nextRound(inFlight)` function (design D1). In `TestPoolSizingOptions`, install a fresh barrier before each round and `require` that the round assembled after its `wg.Wait()` (design D2). Update the inert-barrier note in `TestNewHTTPCallSurvivesWrappedDefaultTransport` and the doc comments that cite `roundBarrier` or latching. Keep the ⚠ overlap-versus-reuse block. This keeps the `outbound-http-calls` scenarios *pool sizing across rounds of concurrent requests to one host* and the `WithHTTPClient` transport scenario pinned with exact counts. Verify: `GOTOOLCHAIN=go1.26.9 go test -race -count=20 -run 'TestPoolSizingOptions|TestRoundBarrier|TestNewHTTPCall' ./action/httpcall` passes.
- [ ] 2.2 Confirm the deadline budget is unchanged: exactly one literal `time.After(5 * time.Second)` in the file. Verify: `scripts/check-test-timeout.sh` passes and still reports one 5 s raw deadline for `action/httpcall`.

## 3. Prove the rewrite bites (design D4)

- [ ] 3.1 Mutation: make `WithMaxIdleConnsPerHost` a no-op in `httpcall.go`. The three capped rows must go red (8 where 26 is asserted). Record the output in the PR, then revert. Verify: the test is red under the mutation and green after the revert.
- [ ] 3.2 Mutation: give every 8-way row `inFlight = concurrency + 1`. Each such subtest must fail at its first round with the assembly message, within about one deadline. Record the wall clock of the mutated run on this branch and on `main` (today's latch) for comparison, then revert. Verify: the recorded timings appear in the PR, and the test is green after the revert.
- [ ] 3.3 Mutation: make the barrier never close `release`, then make the expiry branch close it as well. The first must turn the assembling negative-control row red. The second must panic or fail under `-race` in the expiry row. Record both, then revert. Verify: both mutations are red, and the test is green after the revert.

## 4. Integration

- [ ] 4.1 Run the package and the repo checks. Verify: `GOTOOLCHAIN=go1.26.9 go test -race ./action/httpcall/...`, `scripts/lint.sh` and `scripts/check-doc-refs.sh` pass.

## Workflow follow-up

- The PR body names `pin-rendezvous-barrier`; its last commit runs `openspec archive pin-rendezvous-barrier`.
- After merge, comment on and close kartaladev/wrkflw#185 naming the change.
