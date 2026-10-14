# Design

## Context

See proposal.md, *Why* and *Measured state*. The barrier lives only in `action/httpcall/transport_test.go`. `connCountingServer` creates it once per subtest, and the HTTP handler calls `arrive()` on every request in every round. Each `TestPoolSizingOptions` case runs `rounds = 4` rounds of `concurrency = 8` goroutines, separated by `wg.Wait()`. One row runs with `inFlight = 1` (`WithMaxConnsPerHost(1)`). `TestNewHTTPCallSurvivesWrappedDefaultTransport` reuses the helper with an inert `inFlight = 1`.

Two constraints from elsewhere in the repo:
- `scripts/check-test-timeout.sh` counts every raw `time.After` against a per-package ceiling and rejects a duration it cannot parse as an integer literal. The barrier's deadline must stay a literal at its `time.After` call site.
- `docs/agents/test-deadlines.md` classifies this deadline as a **fixture fallback**: it never fires on a green run. A test must not make every green run pay it.

## Goals / Non-Goals

**Goals:**
- Exactly one code path closes a release channel, and a given channel is closed at most once by construction, not by a guard.
- The first round that cannot assemble produces a readable failure within one deadline.
- The park, arrive and expiry paths are exercised by a test that can fail.

**Non-Goals:**
- Forcing connection reuse. The ⚠ block's overlap-versus-reuse limit is unchanged.
- A barrier type shared outside this file.

## Decisions

### D1. One barrier per round, published to the handler through an atomic pointer

`connCountingServer` keeps an `atomic.Pointer` to the current round's barrier and returns a `nextRound(inFlight)` function. The test calls it before each round. The handler loads the pointer once, then arrives on that barrier.

Why this is sound: within a round, the handler loads the pointer before `arrive`, writes its response after `arrive`, and the client's `a.Do` returns only after reading that response. Each goroutine's `wg.Done` follows its `a.Do`, and the swap happens after `wg.Wait`. So every round-k handler that produced a response loaded the round-k barrier before round k+1's barrier was installed. A per-round barrier is never re-entered by a later round.

Inside the barrier, `arrive` increments a counter under a mutex. The arrival that makes the count equal `inFlight` closes `release`, and that is the only `close` in the type. A count can equal `inFlight` only once, because the count only grows, so close-once is structural. An arrival beyond `inFlight` finds the channel already closed and returns at once.

Rejected:
- *Keep the reused barrier and add a guard against a third closing path* (the issue's "also unfixed" item). That adds a guard to a construct whose problem is that it needs guards.
- *Have the handler compute its round from a request counter* (`n / inFlight`). This is wrong for the `inFlight = 1` row with 8 requests per round. It also misattributes rounds if the transport retries a request.
- *Pass the barrier per request through a header.* It couples the test to the client under test's header handling, which is the code being measured.

### D2. Fail fast after each round; no latch

A waiter whose deadline fires sets the barrier's `failed` flag and returns. It closes nothing. After `wg.Wait()`, the test reads that round's barrier and calls `require` on it, so the subtest stops at the first round that did not assemble.

The issue accepted a worst case of 4 × 5 s = 20 s for a per-round barrier that keeps running after a failure. With fail-fast, the worst case is one deadline per failing subtest, the same as today's latch. Both figures are **derived from the code, not measured**. Task 3.2 measures both under a mutation that cannot assemble.

Rejected:
- *Latch across rounds* (today's design). It requires shared state across rounds, which D1 exists to remove.
- *Keep running after a failed round* (the issue's 20 s shape). This pays 3 more deadlines without telling the reader anything new. The issue's own reasoning, that the first failing round is the whole diagnosis, argues against it.

### D3. Expiry injected as a channel; the literal deadline stays in one place

The barrier's wait selects on its release channel and on an expiry channel `<-chan time.Time`. The handler path passes `time.After(5 * time.Second)` as a literal, so the guard still counts one 5 s fixture fallback for `action/httpcall`. The barrier's own test passes a channel it closes itself, so N−1 real goroutines park, expire together and return without the test paying the deadline.

This covers the routing kartaladev/wrkflw#185 notes is uncovered (park, then the expiry branch, then the failure report), with real concurrent waiters.

Rejected:
- *A configurable `time.Duration` field.* The guard rejects a non-literal duration at a `time.After` call site.
- *Waiting out a short real deadline in the test.* That turns a fixture fallback into a cost paid on every green run, which the deadlines doc says to avoid.

### D4. Evidence that the rewrite still bites

The rewrite is accepted only once each guard has been shown red under a deliberate mutation, recorded in the task and then reverted:
- The pool cases: reverting `WithMaxIdleConnsPerHost` to a no-op turns the capped rows red (8 instead of 26).
- The fail-fast check: requiring `concurrency + 1` arrivals turns every 8-way row red within about one deadline.
- The barrier test's negative control: a barrier that never closes `release` turns the "assembles" row red. A barrier that closes on expiry and has a racing second expiry panics or fails under `-race`.

## Risks / Trade-offs

- [The transport silently retries an idempotent GET on a reused connection that died, so a stale round-k handler is still parked after round k+1 starts] → It is parked on the round-k barrier, which nothing reads any more. It expires alone and closes nothing, and the round-(k+1) barrier is untouched. Today's shared barrier would count that arrival into the wrong round.
- [The rewrite is concurrency code reviewed only once, which is the situation kartaladev/wrkflw#185 complains about] → The construct is smaller (one close site, no swap), and D4's mutations are recorded. The PR also gets the full review a change gets, not a round-2 delta pass.
- [The counts assume reuse the barrier does not force] → Unchanged and documented in the ⚠ block. This change does not touch that limit.

## Migration Plan

None. Test code only; revert the commit to roll back.

## Cross-change coupling

- `size-io-test-deadlines` re-derives the deadline taxonomy counts in `docs/agents/test-deadlines.md`. This change keeps `action/httpcall` at one literal 5 s fixture-fallback deadline, so the counts are the same whichever change lands first. No ordering is required.
- No other active change touches `action/httpcall/transport_test.go`. `export-refusal-sentinels`, `align-godoc-with-behavior` and `bound-untrusted-input` touch `action/httpcall` production code or docs, not this file.
