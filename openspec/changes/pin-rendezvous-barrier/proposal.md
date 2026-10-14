# Proposal

## Why

`TestPoolSizingOptions` in `action/httpcall` gets its concurrency from a hand-written rendezvous barrier, `roundBarrier`. That barrier is one long-lived object reused across every round. Its close-once property therefore depends on a guard: an identity check that is only correct while every closing path swaps and closes the channel together under one mutex. The previous version of this construct had a proven double-close. kartaladev/wrkflw#185 records that the barrier shipped with one reviewer's delta pass, and that a smaller construction would make close-once hold by construction. Allocate one barrier per round: then only one path ever closes a channel.

## What Changes

Every premise below was re-measured on `main` (`4763d36e`).

- **kartaladev/wrkflw#185 (absorbed).** Replace the reused, latching `roundBarrier` in `action/httpcall/transport_test.go` with a barrier allocated fresh for each round. The arrival that completes the round is the only code that closes the release channel. A waiter whose deadline fires records that the round failed and returns. It does not close or swap anything. This removes `giveUp`, the `b.release != rel` identity check, the `assembled` latch, `ok()` and the swap-under-lock invariant.
- **Fail fast per round instead of latching.** After each round's `wg.Wait()`, the test checks whether that round assembled and stops with `require` if it did not. The first round that cannot assemble ends the case, so the latch's purpose survives without the latch. Worst-case wall clock on a failing case stays at one deadline (5 s), not the 4 × 5 s = 20 s the issue accepted. See design D2.
- **Cover the `park` and `arrive` routing the current test skips.** `TestRoundBarrierGiveUpUnderManyWaiters` calls `giveUp` directly, so no test parks and no test reaches the deadline branch of `arrive`. It is replaced by a test that parks N−1 real waiters on an N-way round, expires them through an injected expiry channel (so a green run pays no deadline), and asserts three things. Every waiter returns, the round reports failure, and an N-way round that does assemble reports success (the negative control).
- **Prove the rewrite with mutations.** The rewritten test must still pin the `outbound-http-calls` pool-sizing scenarios with exact counts. Each guard is shown red under a recorded mutation (design D4) before it is accepted.

**Measured state**
- **The issue's premise holds; the restructure still applies.** `transport_test.go` has not been touched since `366446a1` (the #161 fix). All five items the issue proposes to delete are present: `giveUp` (line 88), the identity check (line 102), the `assembled` latch, `ok()` (line 132) and the invariant comment (lines 49–55). There are exactly two closing paths, `park` (line 68) and `giveUp` (line 110), as the issue says.
- **The issue's size figure is stale.** It says "+180/−33 on a 236-line file". `git show --stat 366446a1` gives **+275/−17**. The 236-line starting size is correct (494 − 275 + 17). The barrier and its own test take lines 17–192 of the current 494-line file.
- **"The new test never parks" is confirmed by reading.** `TestRoundBarrierGiveUpUnderManyWaiters` builds a barrier and calls only `giveUp` and `ok`. Nothing reaches `park`'s nil-return branch or `arrive`'s `time.After` branch.
- **The test is green today.** `GOTOOLCHAIN=go1.26.9 go test -race -count=20 -run 'TestPoolSizingOptions|TestRoundBarrier' ./action/httpcall` passes (2.3 s). This change is a cleanup, not a defect fix. No failing reproduction exists, and none is claimed.
- **The deadline budget is unchanged.** `scripts/check-test-timeout.sh` reports one raw `time.After` deadline of 5 s for `action/httpcall`. The rewrite keeps exactly one literal `time.After(5 * time.Second)`, still a fixture fallback.

**Out of scope**
- The production pool code (`httpcall.go`) and the `outbound-http-calls` spec. No behavior changes.
- The overlap-versus-reuse limit documented in the ⚠ block (the counts are the minimum of a range). The barrier forces overlap, not reuse, before and after this change. The block's text is kept.
- Re-deriving the deadline taxonomy counts in `docs/agents/test-deadlines.md`, owned by `size-io-test-deadlines`. This change does not alter `action/httpcall`'s count or construct.
- A reusable barrier type for other packages. The issue notes that none exists and that the stdlib and `x/sync` lack one. Nothing outside this file needs one today.

**BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected. **Consumer go.mod:** not affected. Only one test file changes.

**Coordination**
- Size: S
- Depends on: nothing
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Only test code changes, and no library behavior does, so `skip_specs: true` is set. The rewritten test must keep pinning the existing `outbound-http-calls` scenarios *pool sizing across rounds of concurrent requests to one host* and the `WithHTTPClient` transport scenario.

## Impact

- `action/httpcall/transport_test.go` only: `roundBarrier`, `connCountingServer`, `TestPoolSizingOptions`, `TestRoundBarrierGiveUpUnderManyWaiters` (replaced), and the inert-barrier note in `TestNewHTTPCallSurvivesWrappedDefaultTransport`.
- No production code, no API, no dependency.
