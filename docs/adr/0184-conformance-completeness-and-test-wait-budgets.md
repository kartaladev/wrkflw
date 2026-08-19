# 184. A conformance helper must prove the queries it asserts over, and a test budget must not be a bet on machine load

- Status: Accepted (rule-#9 audit passed 2026-08-18: 3 Opus lenses, 14 findings, all accepted.
  Delivery Gate passed 2026-08-19: `/code-review high` — **2 findings, both fixed**, both fallout
  from Decision 6's late `ErrSchedulerClosed` guard; `/security-review` — **0 findings**.)
- Date: 2026-08-18

> Design and every measurement:
> [`docs/specs/2026-08-18-test-wait-budget-and-conformance-completeness.md`](../specs/2026-08-18-test-wait-budget-and-conformance-completeness.md).
> Audit adjudication: `docs/specs/2026-08-18-adr-0184-audit-adjudication.md`.
> Lenses: `docs/specs/2026-08-18-adr-0184-audit-lens-{a,b,c}.md`.
>
> Closes backlog **42** and backlog **43**, both opened by ADR-0183.
> Relates to ADR-0183 (the claim invariant and the exported conformance helper).

## Context

ADR-0183 shipped `processtest.RunTaskStoreConformance` because adopting that ADR is a **silent**
break for a consumer's own `humantask.TaskStore`: the interface signature does not change, so a
non-conforming store keeps compiling and keeps accepting contradictory rows. The helper is how a
consumer finds out. It is exported public API.

Its documentation promises that a rejected write is *"neither readable through `Get` nor listed by
`AssignedTo` or `ClaimableBy`"*, and a thirty-line comment on
`checkTaskStoreRejectedTaskIsNotListed` argues that the two inbox queries are essential and that
neither is redundant — because `Get` alone cannot catch a store that validates after writing and
filters `Get` differently from its list queries.

**Measured: a store that answers both inbox queries with `nil, nil` passes the entire suite.**
Every `assert.NotContains` holds vacuously. The half of the contract the helper argues hardest for
is the half it does not enforce, because the *legal* leg never asks an inbox anything — so nothing
ever establishes that the queries work at all.

Separately, **40** `Eventually` sites in `scheduler/` bound an assertion with a per-site real-time
budget of 1–3 s. These are fake-clock tests whose subject is a *mapping* or a *state transition*,
yet each is gated on whether a goroutine gets scheduled within a hardcoded wall-clock window — a bet
on machine load, tightest under exactly the contended CI run where it matters least. They are not
identically shaped: budgets ranged 1/2/3 s, one call spans 7 lines, and one passes `fired.Load` as a
bare method value rather than a closure. Blocker 5 is the same class in `internal/persistence/store`.

⚠ **Backlog 42 named a specific test as the motivating example, and that attribution was WRONG.**
ADR-0183's handover recorded
`TestGocronScheduleJobTriggers/"At (past-due) fires immediately (time-skew branch)"` as "load-flaky
under `-race` contention"; this ADR's first draft inherited that and restated it as fact, and a
three-lens audit did not catch it — its execution lens ran the test `-count=25` **in isolation**,
saw green, and read that as consistent with a contention-only flake, which it also is. **Nobody
captured the failure text under contention.**

Measured during implementation: the test fails at `trigger_test.go:306` —
`require.False(t, next.IsZero())` — in **0.00 s**. The `require.Eventually` on the following line
never executes, so the budget it uses is irrelevant to this failure, and the identical assertion
exists at the base commit. The real defect is a **race in `ScheduleJob` itself**, described in
Decision 6.

## Decision

### 1. The conformance helper's legal leg must prove the inbox queries answer

Each conformance case declares which inbox, if any, must return it. Two of the five legal shapes
carry a positive expectation — the `Unclaimed` control must reach `ClaimableBy`, and the `Claimed`
control must reach `AssignedTo` — and the assertion fails a store whose queries return nothing.

This requires **no new fixtures**: the existing probe actor is `{ID: "alice", Roles: ["manager"]}`,
the legal `Claimed` control is claimed by *alice*, and the legal `Unclaimed` control carries
`Eligibility.Roles: ["manager"]`. The terminal and anonymous-claimant shapes declare no expectation,
because neither query is contracted to return them.

We assert **presence for shapes an inbox must return**, not absence for the rest. A store is free
to filter more loosely than the contract, and the rejected leg already covers over-listing.

### 2. The two misuse guards are tested through the subprocess harness the package already has

`taskstoreconformance_factory_test.go` already tests a `FailNow`-class path by re-invoking
`go test` on an env-armed helper test and asserting against captured output. The nil-`newStore` and
nil-returned-store guards are tested the same way, reusing that file's `attributedTest`,
`verboseTestName` and `failedSubtests` helpers.

**We considered and rejected an interface seam** (`conformanceRunner` with `Helper`/`Fatal`/`Run`,
which `*testing.T` satisfies — verified to compile). It would have worked, but it adds production
structure to duplicate a capability the package already has, and a recorder's `Fatal` *returns*
where `runtime.Goexit` does not — forcing explicit `return` statements that read as unreachable and
invite a later "cleanup" that silently breaks the tests. The subprocess harness exercises the real
semantics and changes no production code.

### 3. A test wait budget is a failure ceiling, and is shared per package

Each of the four affected test packages under `scheduler/` gains an unexported
`eventuallyBudget = 10 * time.Second`, replacing every per-site `Eventually` timeout literal
(measured: 30 at `time.Second`, 8 at `2*time.Second`, 2 at `3*time.Second`; 40 total, across 13
files).

The budget is documented as a **failure ceiling, not an expected latency**: a green run returns as
soon as the condition holds, so sizing it generously costs nothing on the passing path.

**The budget is sized against the test binary, not the site.** `go test`'s default timeout is 600 s
per binary and these sites are predominantly serial — measured, 2 of `scheduler/internal/gocron`'s
31 sites run under `t.Parallel` (both in `timeskew_test.go`), and 2 of `scheduler` root's 6 do — so
`budget × site count` is an upper bound, not an exact figure: parallelism only lowers the real
wall-clock sum, which is why the `310 s < 600 s` conclusion below still holds. The realistic red
here is *systemic* — every site in `scheduler/internal/gocron` shares one scheduler — and that
package carries **31** of the 40 sites. At the 30 s first drafted, a systemic break costs 930 s: the
binary is killed with `panic: test timed out` and a goroutine dump that prints **no testify
assertion messages at all**, turning a legible mass failure into an undiagnosable one, in CI, on
exactly the contended machine the raise was for. At 10 s it is 310 s — inside the timeout, with
every broken site still naming itself, and still a ~1000× margin over the measured 0.01 s fire.

**The governing rule, for anyone changing a budget or adding sites:** `budget × the densest
package's site count` must stay under `go test`'s 600 s default.

We keep `require.Eventually` and do **not** introduce a channel-based wait primitive. Polling is not
the defect; the one-second literal is. The measured benefit of an edge wait over a 5 ms poll is
about 5 ms per site.

We chose **per-package unexported consts** over a shared `internal/testwait` package: four one-line
constants, no new package, no import churn — accepting that the rationale is duplicated four times
and that blocker 5 will add a fifth copy elsewhere.

### 4. `Never` budgets are deliberately NOT changed

A `Never` budget is an **observation window paid in full on every green run**. Raising it slows
every passing run for no gain. All **16** `Never` sites keep their literals, which vary per site
(100/150/200/300 ms) for reasons this decision has not established.

The 16 are **10 `require.Never` and 6 `assert.Never`** — this decision covers both. Two files
(`scheduler/clock_option_test.go` and `scheduler/internal/gocron/clock_option_test.go`) contain
**only** `Never` sites and are therefore not modified by this delivery at all.

### 5. Declaring an inbox expectation is mandatory, and wrong declarations are rejected loudly

`inboxExpectation` carries an explicit `inboxUnset` sentinel at iota 0, per
`cc-skills-golang:golang-naming`, which requires one and calls it *"not optional"*. `inboxNone` — a
real state meaning "neither query is contracted to return this shape" — must be **declared**, not
inherited from a zero value. A case-set invariant test rejects `inboxUnset`, rejects `inboxAssigned`
on a case with a nil or empty-ID claim, and rejects any `listedBy` on a rejected case (where it
would be a silent no-op).

Without the sentinel, an author adding a legal case and forgetting `listedBy` would silently get the
**weakest** contract — which is the vacuity this ADR exists to remove, reintroduced one layer up.
Without the claim guard, `inboxAssigned` dereferences a nil `*Claim` and **panics inside exported
API**, taking down a consumer's test binary with a message naming neither the case nor the misuse.

### 6. `ScheduleJob` reports a fire-immediately timer's next run deterministically, not by asking gocron

**The defect (measured, fresh re-derivation, 7 runs × 1,000 arms each: ~12% of arms without
`-race`, 848/7,000; ~0.9% under `-race`, 63/7,000 — the two modes differ by roughly 13×, `-race`'s
added synchronization narrows but does not close the window):** `ScheduleJob` returned
`(time.Time{}, nil)` — a zero next-run alongside a **nil error** — for a past-due one-shot that was
armed correctly and did fire. `jobDefinition` maps a past-due absolute time to
`gocron.OneTimeJob(gocron.OneTimeJobStartImmediately())`, and `ScheduleJob` adds
`WithLimitedRuns(1)`; gocron then runs the job on its own goroutine as soon as `NewJob` registers
it, so the job can fire and self-retire **before** `ScheduleJob` reaches `job.NextRun()`. At that
point `NextRun()` truthfully reports "no next run" — and the caller cannot distinguish that from
"never scheduled".

`ScheduleJob` now computes the answer for this case instead of racing for it: when the past-due
branch is taken — reusing the condition the time-skew WARN already evaluates — it returns `now`,
the same clock reading already captured earlier in the function when the past-due decision was
made (not a fresh `s.clk.Now()` call at the return site). The job fires at ~now, not at the elapsed
`at`; reporting `at` would claim a fire time already in the past. This matches gocron's own
bookkeeping, not merely an approximation of it: verified against the vendored
`github.com/go-co-op/gocron/v2 v2.22.0` (pinned by `go.mod`), `scheduler.go:636` sets
`next = s.now()` for a job with `startImmediately` set — the identical value this function now
returns.

This is **unconditional**, not a fallback for when `NextRun()` happens to come back zero. A
zero-check would only narrow the race window, leaving the same wrong answer available at lower
probability — the shape of bug this repository has repeatedly had to fix twice. The regression test
now pins the returned *value* (`next.Equal(clk.Now())` against the fake clock), not merely its
non-zero-ness — mutation-verified: replacing the return with `now.Add(48*time.Hour)` reproduces a
RED that a bare `!next.IsZero()` assertion cannot catch.

⚠ **Severity, stated accurately:** `ScheduleJob`'s only non-test caller is `activateJob`
(`scheduler/scheduler.go`), which **discards** the returned time. So this was an internal-API
contract violation that no production path consumed — not a live production bug. It is fixed
because the contract is load-bearing for anyone who does use the value, and because the test
asserting it was failing intermittently on `main`. `GocronScheduler.NextRun(id)`, the separate
query method, is deliberately **unchanged**: a fired one-shot genuinely has no next run, and
`scheduler.go` relies on that.

⚠ **A review of this decision found a false positive it introduced**: computing `now`
unconditionally for the fire-immediately branch means `ScheduleJob` reported a fire time (with a
nil error) for a past-due one-shot scheduled **after `Close`**, even though gocron's `NewJob`
silently accepts registrations on a shut-down scheduler and the job will never run. Not reachable
through the public `scheduler.NativeScheduler` today (it guards with its own
`scheduler.ErrSchedulerClosed` before ever calling this internal method), but this decision's own
framing is that the internal `ScheduleJob` *contract* is what is being hardened, not only its one
current caller — so `GocronScheduler` now tracks its own `closed bool` (set under `s.mu` by
`Close`/`CloseWithContext`) and `ScheduleJob` returns an error wrapping the package's own
`ErrSchedulerClosed` sentinel (`workflow-scheduler: scheduler is closed`, mirroring
`ErrUnsupportedTrigger`'s no-import-of-parent convention) before doing anything else, including the
past-due branch.

## Consequences

**This is a breaking change to an exported contract.** A consumer `TaskStore` that passes
`RunTaskStoreConformance` today can fail after this ships, with **no signature change to warn
them** — the same silent-break class as ADR-0183 itself. It is a *correct* tightening: a store that
fails the new assertions was never satisfying the documented contract, and the failure it now
reports is a real defect in that store. It requires a `CHANGELOG.md` ▸ Breaking changes entry.

The module's own three bundled stores were verified against the tightened contract and all pass:
`humantask.MemTaskStore`, the neutral SQL store on SQLite, and `persistence.CachingTaskStore` — a
decorator with its own inbox caching, and the most plausible place for a shipped listing bug to
hide. Tightening an exported conformance helper without checking the module's own implementations
would have been this ADR's own defect class applied to itself.

Adding a positive inbox assertion to the legal leg removes an invariant that
`taskstoreconformance_internal_test.go` deliberately pins exact failure counts against — *"the
legal leg queries no inbox"*. Measured, **one** of its expectations is re-derived
(`inboxFailingTaskStore`) and **one** false comment is rewritten. The counts **are** the assertion,
so they must be re-derived rather than loosened to `NotEmpty`.

⚠ A draft of this ADR claimed *three* expectations change, and that `writeOnlyTaskStore` would go
from 1 to 2. Both were wrong, and all three audit lenses caught it independently. The reason is a
control-flow fact the draft never stated: `checkTaskStoreConformance` **returns early** when the
legal read-back fails, so for a store that accepts but never persists the new inbox assertion is
never evaluated. This also surfaces a genuine inconsistency in the shipped helper — its doc comment
promises it *"never stops early"*, and on the legal leg it does. That is recorded as backlog 47 and
deliberately not fixed here: changing what an exported helper reports is its own decision.

A genuinely broken `Eventually` site takes 10 s to fail instead of 1–3 s, and a systemic break in
the densest package costs 310 s against the 600 s default. Paid only on red, and buys raising the
bound at all 40 sites from 1–3 s to 10 s — ~1000× the measured 0.01 s fire time — not removing it:
`require.Eventually(t, cond, 10*time.Second, …)` is still a real-time budget.

The two misuse guards become **executed**, but not **covered**: they run in a child `go test`
process spawned without `-coverprofile`, so `RunTaskStoreConformance` stays at 77.8 % with both
guard blocks at hit-count 0. This is the accepted cost of the subprocess pattern. ⚠ Do not read the
unchanged figure as a missing test — the obvious "fix" is to move the guards in-process, which means
re-introducing the seam Decision 2 rejected.

**Backlog 42 is closed by Decision 6, NOT by Decision 3.** The two are independent, and conflating
them was this ADR's original error.

Decision 3's honest claim is *"the bound at all 40 sites is raised from 1–3 s to 10 s, a ~1000×
margin over the measured 0.01 s fire time"* — not *"the flake is fixed"* and not *"the
load-dependent bound is removed"*; a raised real-time budget is still a real-time budget, and its
value is unprovable by a green suite, since a flake needing full-suite contention will not reappear
on demand.

Decision 6 is what closes the named test, and it **is** provable: fresh re-derivation measured
**~12 % of arms without `-race`** and **~0.9 % under `-race`** (7 runs × 1,000 arms each mode)
against the reverted (pre-fix) code — a real, mode-dependent race, not the single "~20 %" figure an
earlier draft attributed to `-race` alone. ⚠ **"0 of N" after the fix is not a comparable
measurement and must not be read as one**: post-fix the branch returns `now` unconditionally, so it
cannot return zero **by construction** — there is no longer a race to sample. The regression guard
was also raised from 200 to 5,000 iterations per run (200 gave a false-green pass on definitely-
broken code in roughly 1 of 5 runs under `-race`; 5,000 was 6/6 reliably RED against the same
reverted code, at ~0.2-0.4 s per run on the green path).

⚠ **The process lesson, recorded because it cost this delivery a late scope change.** The
"load-flaky `Eventually` budget" diagnosis was inherited from ADR-0183's handover, restated here as
fact, and **survived a three-lens audit** — the execution lens ran the test `-count=25` in
**isolation**, where it passes, and never captured its failure text under contention. The refutation
was a single number: the test fails in **0.00 s**, and a test that fails instantly cannot be waiting
out a timeout. ⭐ **When a symptom is attributed to a timing bound, check how long the failure
actually took before believing the attribution.**

Five follow-ups are opened rather than silently absorbed: the 16 `Never` sites are vacuity-prone
under the same contention that causes backlog 42 (a "did not fire within 150 ms" assertion passes
trivially if the goroutine never ran); blocker 5 and the `runtime/` sites remain unconverted and may
justify promoting the constant to a shared package; a `testing/synctest` spike could remove
real-time budgets from this suite entirely, if gocron's internals tolerate a bubble; the helper's
legal leg stops at the first break despite documenting that it does not (backlog 47); and nothing
enforces the `budget × site-count < 600 s` rule this decision now rests on, since CI passes no
`-timeout` (backlog 48).
