# Spec — a contended machine must not fail a green test, and a conformance helper must not pass a blind store

**Date:** 2026-08-18
**Status:** design, pre-audit
**ADR:** 0184 (to be written)
**Closes:** backlog **42**, backlog **43**
**Delivery shape:** two independent halves — `scheduler/` test budgets, and `processtest`'s
exported `RunTaskStoreConformance`. They share no code and no file.

---

## 1. Why this exists

Two items opened by ADR-0183. They were filed as small cleanups. Execution showed one of them
is a defect in **shipped public API**, so the delivery is a little larger than the backlog text
implies — but still small.

### 1a. Backlog 42 — a real-time budget on a fake-clock test

`TestGocronScheduleJobTriggers/"At (past-due) fires immediately (time-skew branch)"`
(`scheduler/internal/gocron/trigger_test.go:298`) fails under full-suite `-race` contention and
passes `-count=25` in isolation. It was **proven pre-existing** during ADR-0183 (it reproduces on
unmodified `main`), so it is not a regression — it is a latent flake that contention finally
surfaced.

The mechanism, source-verified:

- The fixture builds a **fake** clock (`clockwork.NewFakeClockAt`) and schedules a one-shot at
  `clk.Now().Add(-10 * time.Minute)`.
- `jobDefinition` (`scheduler/internal/gocron/trigger.go:182-184`) maps a past-due absolute time to
  `gocron.OneTimeJob(gocron.OneTimeJobStartImmediately())`.
- That fires on **gocron's own real-time goroutine**. The fake clock does not bound it, and the
  test deliberately does not advance the clock — "without a clock advance" is the assertion.
- The wait is `require.Eventually(..., time.Second, 5*time.Millisecond)` — a **real-time** budget.

So the test asserts a fact about *mapping* but bounds it with a bet on *machine load*. Under a
full-suite `-race` run with testcontainers booting, one second of wall clock can pass without that
goroutine being scheduled.

The failing site is not special. It is one of **40** `Eventually` sites in `scheduler/` with the
same shape.

> ⚠⚠⚠ **EVERYTHING ABOVE ABOUT *THIS SPECIFIC TEST* IS FALSE, AND WAS REFUTED BY IMPLEMENTATION.**
> The reasoning is sound for the *class* — 40 sites really do bound a fake-clock assertion with a
> real-time budget, and Decision 3 still addresses that. But the named test does **not** fail for
> this reason.
>
> **Measured under contention:** it fails at `trigger_test.go:306` —
> `require.False(t, next.IsZero())` — in **0.00 s**. The `require.Eventually` on the next line never
> executes. The identical assertion exists at the base commit, so raising the budget 1 s → 10 s
> cannot fix it and never could have.
>
> **The real defect:** `ScheduleJob` returns `(time.Time{}, nil)` — a zero next-run with a **nil
> error** — for a past-due one-shot, because `job.NextRun()` races the immediate fire of a
> `WithLimitedRuns(1)` job. Measured (fresh re-derivation, 7 runs × 1,000 arms each, against
> reverted production code): **~12 % of arms without `-race`** (848/7,000), **~0.9 % under
> `-race`** (63/7,000) — the two modes differ by roughly 13×. ⚠ Post-fix the branch returns the
> clock's current time unconditionally, so it **cannot return zero by construction** — "0 of N
> after the fix" is not a comparable measurement against the pre-fix rate, since there is no
> longer a race to sample. See ADR-0184 Decision 6.
>
> **How it got here:** ADR-0183's handover called it "load-flaky under `-race` contention"; this
> spec inherited that and restated it as fact; the rule-#9 audit's execution lens ran the test
> `-count=25` **in isolation** — where it passes — and read that as consistent. Nobody ran it under
> contention and read the failure text. ⭐ **A test that fails in 0.00 s is not waiting for a
> timeout; check the failure's duration before believing a timing diagnosis.**

### 1b. Backlog 43 — the conformance helper's inbox assertions are vacuous

`processtest.RunTaskStoreConformance` is exported, and ADR-0183 shipped it precisely because
adopting that ADR is a **silent** break for a consumer's own `TaskStore`. Its doc comment promises
that a rejected write is *"neither readable through `Get` nor listed by `AssignedTo` or
`ClaimableBy`"*, and a 30-line comment on `checkTaskStoreRejectedTaskIsNotListed` argues at length
that the two inbox queries are essential and non-redundant.

**Measured (§6.2): a store that answers both inbox queries with `nil, nil` passes the entire
suite.** Every `assert.NotContains` holds vacuously. The half of the contract the helper argues
hardest for is the half it does not enforce.

Backlog 43 also names two unreachable misuse guards (the nil-`newStore` guard and the nil-returned-store guard in `RunTaskStoreConformance`),
which is why the helper sits at 77.8 % coverage.

---

## 2. Non-goals

Recorded here so the audit does not read them as omissions. Each becomes a backlog item.

- **The 16 `Never` sites in `scheduler/` are not touched.** Their budgets are *observation windows*
  paid in full on every green run, they vary per site (100/150/200/300 ms) for reasons this
  delivery has not established, and normalising them would both slow the suite and weaken the
  300 ms one. Separately, they are **vacuity-prone under exactly the contention that causes
  backlog 42** — "did not fire within 150 ms" passes trivially if the scheduler goroutine never
  ran at all. That is the mirror image of this bug and deserves its own item, not a smuggled fix.
- **No `testing/synctest` spike.** Go 1.25's bubble could make these genuinely deterministic with
  no budget at all, but gocron's internals are outside our control and a bubble forbids real I/O.
  That is a research question, not a flake fix.
- **Blocker 5 and the `runtime/` `Eventually` sites are out of scope.** Blocker 5
  (`TestPgxNotifierListenDrainsBeforePollInterval`) is the same class in a different package.
- **No change to `Eventually` tick intervals.** Only the timeout argument changes.
- **No new fixtures** in the conformance suite. §6.3 shows none are needed.

---

## 3. Design — backlog 42

### 3.1 Decision: keep testify, share the budget

An earlier draft proposed a channel-based `Signal` primitive to replace polling. **It was rejected
by the owner and the rejection is correct**: polling is not the defect, the one-second literal is.
The measured benefit of an edge wait over a 5 ms poll is ~5 ms per site — noise. testify v1.11.1
offers `Eventually`, `EventuallyWithT` and `Never`, all polling, all taking an explicit `waitFor`
(§6.4). It is already the project's assertion library. We use it.

This also dissolves a distinction an earlier draft raised — "edge waits" (`fired.Load() >= N`)
versus "state-convergence waits" (`NextRun` absence, `running.Load() == 0`, `IsLeader`). The
classification is real but **changes nothing**: both shapes stay `Eventually` with a longer budget.
It is recorded here only so the audit does not rediscover it and assume it was missed.

### 3.2 The change

One unexported const per test package, replacing the per-site timeout literal:

```go
// eventuallyBudget is a FAILURE ceiling, not an expected latency. A green run
// returns as soon as the condition holds — typically microseconds — so sizing
// this for the worst contended CI machine costs nothing on the passing path;
// only a test that was already going to fail waits it out.
//
// It replaces the per-site literals of 1-3 s. Those made
// TestGocronScheduleJobTriggers/"At (past-due) fires immediately" fail under
// full-suite -race contention (backlog 42) while passing -count=25 in
// isolation: that job fires via gocron.OneTimeJobStartImmediately on a REAL-time
// goroutine, so the fake clock does not bound it and one second of wall time is
// a bet on machine load rather than an assertion about the code.
//
// Deliberately NOT applied to Never (require.Never AND assert.Never): a Never
// budget is an observation window paid in full on every GREEN run, so raising it
// is pure cost.
//
// ⚠ SIZED AGAINST THE BINARY, NOT THE SITE. `go test`'s default timeout is 600s
// per test binary, and these sites are PREDOMINANTLY serial (2 of the 31 in
// scheduler/internal/gocron run under t.Parallel), so budget x site count is an
// upper bound. The densest package, scheduler/internal/gocron, carries 31 sites,
// so a systemic break — the realistic red, since every site shares one scheduler
// — costs budget × 31. At 30s that is 930s: the binary is killed with
// "panic: test timed out" and a goroutine dump, printing NO testify messages at
// all. At 10s it is 310s, inside the default, and every broken site still names
// itself. The rule for anyone changing this: budget × the densest package's site
// count must stay under 600s.
const eventuallyBudget = 10 * time.Second
```

**The budget is 10 s, not 30 s.** The audit measured the per-binary cost the first draft never
computed: the change is priced per *site*, but `go test`'s timeout is per *binary*. 10 s keeps a
~1000× margin over the measured 0.01 s fire time (§6.7) while keeping a total failure legible.

Placed in **each of the 4 test packages** (§6.5). Per-package unexported consts were chosen over a
shared `internal/testwait` package: 4 one-line consts, no new package, no import churn. The cost —
the rationale above is duplicated, and blocker 5 will later add a 5th copy elsewhere — is accepted.

### 3.3 Which sites change

Every `Eventually` timeout argument in `scheduler/`. Measured distribution
(§6.1): **30 at `time.Second`, 8 at `2*time.Second`, 2 at `3*time.Second` — 40 total, fully
accounted for**, and all 40 are real calls (0 of the matches are comments).

`EventuallyWithT` does **not** appear in `scheduler/` at all (measured: 0 sites), so it is not part
of this change. It is named in §6.4 only as part of testify's API surface.

> ⚠ **The plan must re-derive this enumeration, not inherit it.** A first pass at this count
> returned "26 of 40" because the grep was single-line and missed multi-line calls. The number
> above is the corrected one, but per Premise Discipline a restated count is exactly the kind of
> claim that rots. The rule-#9 counting lens owns re-deriving it.

The `Never` sites keep their literals untouched (§2).

---

## 4. Design — backlog 43

### 4.1 43a — reuse the established subprocess pattern; NO production change

> **⚠ This section replaced an earlier design during planning.** The first draft proposed a
> `conformanceRunner` interface seam (`Helper`/`Fatal`/`Run`) so a recorder could drive the guards.
> That design is **withdrawn**: `processtest/taskstoreconformance_factory_test.go` already
> establishes a subprocess harness for precisely this problem, so the seam would have added
> production complexity to duplicate a capability the package already has. The withdrawn seam was
> verified to compile (§6.6) — it was workable, just unnecessary. Recorded here so the audit does
> not "rediscover" the seam and read its absence as an oversight.

`TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest` tests a `FailNow`-class path by
re-invoking `go test` on an **env-armed helper test** that fails by design, then asserting against
the captured output. It ships three reusable helpers in that file: `attributedTest(output, marker)`,
`verboseTestName(line)` and `failedSubtests(output, helper string)`.

The two misuse guards are tested the same way, in the same file, reusing those helpers:

| guard | helper test (env-armed, fails by design) | parent assertion |
|---|---|---|
| `RunTaskStoreConformance`'s nil-`newStore` guard | calls `RunTaskStoreConformance(t, nil)` | child FAILs; output contains `requires a non-nil newStore`; **no** subtest ran |
| `RunTaskStoreConformance`'s nil-returned-store guard | factory returns `nil, nil` | child FAILs; output contains `returned a nil humantask.TaskStore`; attributed to a **subtest** |

Why this beats the seam:

- **No production code changes at all.** `RunTaskStoreConformance` keeps its body as well as its
  signature.
- It exercises the **real** `t.Fatal` → `runtime.Goexit` semantics. A recorder's `Fatal` returns,
  which is precisely why the withdrawn design needed explicit `return` statements that look
  unreachable — a trap for the next reader.
- It matches the pattern already in the file, so there is one way to test a fatal path here, not two.

Cost, accepted: each parent test spawns a real `go test` (the existing one budgets 5 minutes and is
`t.Parallel()`). Two more child processes in `processtest`'s suite.

⚠ Both helper tests **must** carry the env guard and the `t.Skip` when unarmed, exactly as
`TestConformanceFactoryFatalHelper` does — an unarmed helper that runs in an ordinary invocation
fails the suite by design.

### 4.2 43b — make the legal leg prove the inbox queries work

`taskStoreConformanceCase` gains a field naming the inbox that must return the shape. The
enumeration carries an explicit **`inboxUnset` sentinel at iota 0**, because
`cc-skills-golang:golang-naming` — an always-on skill for this project — requires it and calls it
*"not optional"*: an author who adds a legal case and forgets to declare `listedBy` would otherwise
silently inherit the **weakest** contract, which is the vacuity ADR-0184 exists to remove,
reintroduced one layer up. A case-set invariant test rejects `inboxUnset`, so the omission is loud:

```go
const (
	// inboxUnset is the zero value and is NEVER correct: it means the author did
	// not decide. The case-set invariant test rejects it.
	inboxUnset inboxExpectation = iota
	// inboxNone declares, deliberately, that neither query is contracted to
	// return this shape — the terminal and anonymous-kiosk controls.
	inboxNone
	inboxAssigned
	inboxClaimable
)
```

⚠ A `listedBy` on a **rejected** case is a silent no-op — the rejected leg returns before the new
check. The invariant test pins `c.legal || c.listedBy == inboxNone` so that a contributor declaring
one "for symmetry" is told rather than ignored.

⚠ `inboxAssigned` **dereferences `c.task.Claim`**. A case declaring it with a nil claim panics —
in *exported* API, taking down a consumer's whole test binary with a message naming neither the case
nor the misuse. Both a guard in the check and an invariant on the case set are required; an empty
`Claim.Actor.ID` must be rejected too, since `AssignedTo("")` contractually returns nothing.

The field:

```go
// listedBy names the inbox query that MUST return this shape. It is what stops
// the negative assertions on the rejected leg from being vacuous: a store whose
// AssignedTo and ClaimableBy always return nil satisfies every NotContains check
// for the wrong reason, and passed this entire suite before this field existed.
// inboxNone means no positive expectation — the terminal and anonymous-claimant
// shapes, which neither query is contracted to return.
listedBy inboxExpectation
```

Assignment over the existing cases:

| case | `listedBy` | why |
|---|---|---|
| `unclaimed_without_a_claim_is_accepted` | `inboxClaimable` | probe holds `manager`; fixture's `Eligibility.Roles` is `["manager"]` |
| `claimed_with_a_claim_is_accepted` | `inboxAssigned` | the fixture's claimant **is** the probe (`alice`) |
| `claimed_by_an_empty_kiosk_claimant_is_accepted` | `inboxNone` | claimant has no ID; there is no inbox to ask about |
| `completed_without_a_claim_is_accepted` | `inboxNone` | terminal; neither query is contracted to return it |
| `cancelled_retaining_its_claim_is_accepted` | `inboxNone` | terminal, and claimed by `bob`, not the probe |
| *(the 3 rejected shapes)* | `inboxNone` | the negative assertions already cover them |

**No fixture changes.** The existing probe actor `{ID: "alice", Roles: ["manager"]}` and the
existing legal fixtures already satisfy both positive assertions — measured in §6.3.

The assertion mirrors the negative leg's shape, so an unanswerable query reports once rather than
twice:

```go
if assert.NoErrorf(t, err, "ClaimableBy(%s) on a legal shape: the query must answer; got %v", c.name, err) {
	assert.Containsf(t, taskStoreConformanceIDs(claimable), c.task.TaskID,
		"ClaimableBy(%s): an accepted Unclaimed task the actor is eligible for MUST reach the claimable inbox — "+
			"without this the not-listed assertions on the rejected leg pass vacuously for a store that lists nothing", c.name)
}
```

### 4.3 ⚠ The legal leg starts touching the inboxes, which breaks ONE pinned expectation and one comment

`taskstoreconformance_internal_test.go` pins **exact failure counts** per stand-in store, and does
so deliberately — *"Pinning the count keeps every MUST live"*. Today those counts rest on an
invariant this change removes: **the legal leg queries no inbox.**

**Measured** (every stand-in × every legal case, before → after; the audit implemented §4.2 and
diffed the recorder counts):

| stand-in | legal case | `listedBy` | before | after |
|---|---|---|---|---|
| `inboxFailingTaskStore` | the 2 declaring an inbox | Claimable / Assigned | 0 | **1** |
| `inboxFailingTaskStore` | the other 3 | none | 0 | **0** |
| `writeOnlyTaskStore` | **all 5** | — | 1 | **1** |
| `rejectingTaskStore` | all 5 | — | 1 | **1** |
| `kioskHostileTaskStore` | kiosk | none | 1 | **1** |
| `kioskHostileTaskStore` | other 4 | mixed | 0 | **0** |
| `MemTaskStore`, `permissiveTaskStore`, `leakyRollbackTaskStore` | all 5 | — | 0 | **0** |

So exactly **one expectation changes** (`inboxFailingTaskStore`'s legal branch) plus **one false
comment**. They must be **re-derived, not loosened**.

> ⚠⚠ **An earlier draft of this section predicted `writeOnlyTaskStore` would go to 2, and was
> WRONG — caught by all three audit lenses independently.** The reason matters more than the number:
> `checkTaskStoreConformance` **returns early** when the read-back fails
> (`if !assert.NoErrorf(t, getErr, …) { return }`), so for a store that accepts but never persists
> the new inbox check is **never evaluated**. The draft reasoned *"nothing persisted, so the inbox
> assertion also fails"* — an analogy about a code path that is unreachable. Had it shipped, the
> implementer would have been told to expect a count that cannot occur, and the obvious way out is
> the one this very section forbids.

Unaffected, **measured**, and for two genuinely different reasons — the first draft gave the wrong
mechanism for two of these and was right by accident:

- `MemTaskStore`, `permissiveTaskStore`, `leakyRollbackTaskStore` — 0 → 0. All three answer both
  inboxes conformingly, so the new assertion passes.
- `rejectingTaskStore` and `writeOnlyTaskStore` — 1 → 1. **Control flow, not inbox behaviour**: the
  legal leg returns before the new check (the `Upsert` refusal and the read-back miss respectively).
  Their inbox implementations are irrelevant, and both return `(nil, nil)` — which would otherwise
  have failed. See §6.10.
- `kioskHostileTaskStore` — kiosk 1 → 1 (refused at `Upsert`); the other four 0 → 0, carried by the
  embedded `MemTaskStore`'s conforming inboxes.

⚠ An implementer who "fixes" a failing pinned count by relaxing `assert.Lenf` to `assert.NotEmptyf`
has destroyed the property that file exists to protect. The counts are the assertion.

---

## 5. Testing

### 5.1 What must fail, and why it fails today

Per CLAUDE.md, a prescribed test must state what makes it go red. Both are established by
execution, not argument.

| new assertion | fails today against | evidence |
|---|---|---|
| `ClaimableBy(probe)` contains the legal `Unclaimed` row | the blind-inbox store (§6.2) | §6.2 + §6.3 |
| `AssignedTo("alice")` contains the legal `Claimed` row | the blind-inbox store (§6.2) | §6.2 + §6.3 |
| nil `newStore` → child `go test` FAILs with the guard message, no subtest ran | current code: the guard is never executed by any test | §4.1 |
| factory returning nil → child `go test` FAILs, attributed to a subtest | current code: the guard is never executed by any test | §4.1 |
| `inboxFailingTaskStore`'s legal-branch count, re-derived from `Empty` to 1-iff-`listedBy` (2 literals) | current code: the legal leg asks no inbox, so `Empty` holds | §4.3 |
| a legal case left at `inboxUnset` is rejected by the case-set invariant test | current code: the field does not exist | §4.2 |
| a case declaring `inboxAssigned` with a nil or empty-ID claim is rejected | current code: the field does not exist; unguarded it **panics** | §4.2 |

The blind-inbox store of §6.2 is **kept as a permanent test** in `processtest`'s own suite — it is
the regression guard for the vacuity, and it is the only thing that can catch the assertions being
weakened back.

⚠ **Backlog 42's change is not directly testable.** Raising a timeout has no assertion that can go
red: the test passes before and after. Its verification is (a) the mutation described in §5.2, and
(b) the enumeration being complete. The plan must not prescribe a fake test that "proves" the
budget, and the audit should reject one if it appears.

### 5.2 Mutation obligations

- **43b**: revert each positive assertion in turn against the blind-inbox store; observe RED;
  restore from a `cp` backup; `diff` to confirm. (Never `git checkout <path>` — it restores from
  the index and destroys uncommitted work.)
- **43a**: with the recorder, assert `Run` was **not** invoked after the nil-`newStore` `Fatal`.
  Deleting the `return` added in §4.1 must produce an observable failure (a nil-func panic), which
  is the proof the `return` is load-bearing rather than decorative.
- **42**: confirm the converted sites still compile and pass, and that **no `Never` site's literal
  changed** — a `git diff` filtered to `Never(` must be empty.

### 5.3 Verification

Per CLAUDE.md Verification, in order:

1. `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥ 85 %.
   Docker: standing permission for this run; probe the daemon first, and if it is down say so
   rather than substituting a narrower run.
2. `go test ./...` from the repo root — no regressions.
3. `golangci-lint run ./...` — repo-wide, not package-scoped.
4. `/code-review` then `/security-review` — owner-invoked; fix all findings, fold via `--amend`.

⚠ **Coverage expectation: `processtest`'s helper STAYS at 77.8 %, and that is correct.** The guards
execute in a **child** `go test` process spawned without `-coverprofile`, so its counters are
discarded; the parent only ever *skips* the env-armed helper. Measured by the audit after
implementing Task 2: still 77.8 %, with both guard blocks at hit-count **0**. The guards are
*tested*, not *covered* — that is the accepted cost of the subprocess pattern (§4.1).

⚠⚠ **Do not treat the unchanged figure as a missing test, and do not "fix" it** by moving the guards
in-process: that means re-introducing the `conformanceRunner` seam ADR-0184 explicitly rejected.
An earlier draft of this spec promised the number would rise, which would have failed the gate for a
delivery that is working correctly.

`scheduler/` coverage should be **unchanged** — a budget constant executes the same lines.

⚠ **Backlog 42's own acceptance cannot be proven by a green suite.** A flake that needed full-suite
`-race` contention to appear will not reappear on demand. The honest claim after this delivery is
*"the load-dependent bound is raised from 1-3 s to 10 s at all 40 sites"*, **not** *"the flake is fixed"* — the
latter is unfalsifiable in a single run. The handover must say the former.

---

## 6. Premise evidence — executed, with observed output

Every factual claim above about current behaviour was run. Probes were throwaway and deleted; the
tree was confirmed clean afterwards.

### 6.1 `Eventually` budget distribution in `scheduler/`

Multi-line-aware extraction over all files containing `Eventually(`:

```
30 time.Second
 8 2*time.Second
 2 3*time.Second
--- total Eventually call sites --- 40
```

Accounts for all 40 sites. ⚠ A first, single-line-only grep reported "26" — superseded.

`Never` sites, counted separately: **16** — of which **10 are `require.Never` and 6 are
`assert.Never`**, so any sentence quantifying only over `require.Never` under-describes them.
`EventuallyWithT` sites in `scheduler/`: **0**.

⚠ An earlier draft claimed *"matches that are comments rather than calls: 0"*. That is
pattern-dependent and was asserted from a grep whose own filter could not have found a comment in
the shapes that occur here; treat 40 as the count of `Eventually(` **call sites** enumerated
line-by-line in the plan's Task 3 table (independently re-derived by the audit's counting lens,
all 40 line numbers correct), not as a claim about comment scanning.

### 6.2 The conformance suite passes a store with blind inboxes

A store delegating `Upsert`/`Get` to a conforming `humantask.NewMemTaskStore()` while returning
`nil, nil` from both `AssignedTo` and `ClaimableBy`:

```
go test -count=1 -run '^TestZZProbeVacuousInbox$' ./processtest/
EXIT=0
ok  github.com/kartaladev/wrkflw/processtest  0.608s
```

**Passes.** This is the defect 43b closes.

### 6.3 The two positive assertions are constructible against the reference store

```
ClaimableBy(alice) -> n=1 err=<nil>
   id=wrkflw-conformance-unclaimed state=unclaimed
AssignedTo(alice)  -> n=1 err=<nil>
   id=wrkflw-conformance-claimed  state=claimed
```

So the new assertions pass against `MemTaskStore` and fail against §6.2's store. Both discriminate.

⚠ **`MemTaskStore` is not the only in-repo store that meets this helper.**
`processtest/taskstoreconformance_test.go` drives the **exported** helper against **three** bundled
implementations, and an earlier draft named none of them — tightening an exported conformance
contract without checking the module's own implementations would be this delivery's own defect class
applied to itself. Measured by the audit with §4.2 applied (SQLite is pure-Go, no container):

```
go test -count=1 -run '^TestRunTaskStoreConformance$' ./processtest/ -v   → EXIT=0
  --- PASS: .../MemTaskStore                        (8/8)
  --- PASS: .../CachingTaskStore_over_MemTaskStore   (8/8)
  --- PASS: .../sqlite_HumanTaskStore                (8/8)
```

All three satisfy the tightened contract — including `CachingTaskStore`, a decorator with its own
inbox caching, which was the most plausible place for a shipped listing bug to hide.

### 6.10 A legal shape whose Upsert or read-back fails RETURNS before the inbox check

The single control-flow fact that both §4.3's corrected table and the withdrawn `writeOnlyTaskStore`
prediction turn on, and which the first draft stated nowhere. In `checkTaskStoreConformance` the
legal leg returns early both when `Upsert` is refused and when `Get` misses. Measured, before →
after applying §4.2: `rejectingTaskStore` **1 → 1**, `writeOnlyTaskStore` **1 → 1** — for both, the
new inbox assertion is never evaluated, so their `(nil, nil)` inboxes never get the chance to fail
it.

### 6.4 testify's API surface (v1.11.1)

```
assert/assertions.go:1988  func Eventually(t TestingT, condition func() bool, waitFor, tick time.Duration, ...) bool
assert/assertions.go:2083  func EventuallyWithT(t TestingT, condition func(collect *CollectT), waitFor, tick time.Duration, ...) bool
assert/assertions.go:2135  func Never(t TestingT, condition func() bool, waitFor, tick time.Duration, ...) bool
```

All polling; no edge/channel variant; every one requires an explicit `waitFor`. Confirms §3.1.

### 6.5 The affected test packages

All black-box. **13 files carry an `Eventually` site** — those are the files this delivery modifies.
**15** carry `Eventually` **or** `Never`; the 2 extra (`scheduler/clock_option_test.go` and
`scheduler/internal/gocron/clock_option_test.go`) hold **only `assert.Never` sites** and must
therefore **not** be modified. ⚠ An earlier draft said "15 files" in both places, which would have
sent an implementer hunting for two missing files and landing exactly on the two the delivery
forbids touching. **4** packages:

```
scheduler                            : scheduler_test
scheduler/internal/gocron            : gocron_test
scheduler/internal/gocron/myelector  : myelector_test
scheduler/internal/gocron/pgelector  : pgelector_test
```

### 6.6 `*testing.T` satisfies the WITHDRAWN seam

```go
var _ conformanceRunner = (*testing.T)(nil)
```

**Compiled** as part of the §6.3 probe — compile-time proof, not inference. Retained only as
provenance: §4.1 withdrew this design in favour of the subprocess harness the package already has.
The seam was workable; it was not needed. **Do not implement it.**

### 6.8 The subprocess harness for fatal paths already exists

`processtest/taskstoreconformance_factory_test.go` runs a child `go test` against an env-armed
helper (`WRKFLW_PROCESSTEST_FACTORY_FATAL`, `TestConformanceFactoryFatalHelper`) and asserts on the
captured output, with `attributedTest`, `verboseTestName` and `failedSubtests` as reusable helpers.
Its own doc comment records the measured Go 1.25 behaviour of a cross-goroutine `FailNow`. This is
the pattern §4.1 adopts.

### 6.9 The legal leg asks no inbox today

Source-verified in `checkTaskStoreConformance`: the `c.legal` branch asserts `Upsert`, `Get`,
`State` round-trip and `Claim` presence, then returns — `checkTaskStoreRejectedTaskIsNotListed` is
called only on the `!c.legal` branch. `taskstoreconformance_internal_test.go` states the same fact
in its own words: *"The legal leg queries no inbox, so this store passes it."* That invariant is
what §4.3's pinned counts rest on, and what 43b removes.

### 6.7 The flaky test passes in isolation

```
go test -count=1 -race -run '.../At_\(past-due\)' ./scheduler/internal/gocron/ -v
EXIT=0
--- PASS: .../At_(past-due)_fires_immediately_(time-skew_branch) (0.01s)
ok  github.com/kartaladev/wrkflw/scheduler/internal/gocron  1.503s
```

Fires in **0.01 s** unloaded against a 1 s budget — a ~100× margin that contention erases. This is
what makes a raised ceiling the right fix rather than a slower assertion.

---

## 7. Consequences

**Good**

- 40 load-dependent bounds in `scheduler/` become one tunable constant per package.
- The exported conformance helper enforces what its own documentation claims.
- Two misuse guards become **executed for the first time**. (Their *coverage* does not change —
  §5.3.)
- The module's own three bundled stores are verified against the tightened contract (§6.3).

**Bad / accepted**

- A genuinely broken `Eventually` site takes 10 s to fail instead of 1–3 s, and a **systemic** break
  in `scheduler/internal/gocron` costs 31 × 10 s = **310 s** against `go test`'s 600 s default. That
  is inside the timeout deliberately (§3.2): at the originally-drafted 30 s it would have been 930 s
  and the binary would die with a goroutine dump printing **no assertion messages at all**. Paid
  only on red.
- The `eventuallyBudget` rationale is duplicated 4×; blocker 5 will add a 5th copy elsewhere. The
  per-package shape now also carries a real obligation: if a package's site count grows, its budget
  must be re-derived against the 600 s ceiling.
- Two more child `go test` processes in `processtest`'s suite (§4.1). Both `t.Parallel()`.
- **One** pinned expectation in `taskstoreconformance_internal_test.go` is re-derived and **one**
  false comment rewritten (§4.3) — churn in a file whose whole purpose is pinning counts, and it
  must be done by derivation.
- The conformance case set gains an invariant test it did not need before, because `listedBy`
  introduces three ways to declare a case wrong (unset, `inboxAssigned` with no claim, `listedBy`
  on a rejected case).

**Breaking**

- **43b tightens an exported contract.** A consumer `TaskStore` that passes `RunTaskStoreConformance`
  today can fail after this ships, with **no signature change to warn them** — the same silent-break
  class as ADR-0183 itself. This is why the delivery needs ADR-0184 and a `CHANGELOG.md` ▸ Breaking
  changes entry. It is a *correct* tightening: a store that fails it was never satisfying the
  documented contract.

---

## 8. New backlog items opened by this spec

44. The 16 `Never` sites in `scheduler/` are vacuity-prone under contention — "did not fire within
    150 ms" passes trivially if the goroutine never ran. Mirror image of backlog 42.
45. Blocker 5 and the `runtime/` `Eventually` sites are the same class, unconverted; adopting the
    pattern there adds a 5th copy of the constant and may justify promoting it to `internal/`.
46. A `testing/synctest` spike for the `scheduler/` suite — could remove real-time budgets
    entirely, but gocron's internals may not tolerate a bubble.
47. **`checkTaskStoreConformance` stops at the first break on the legal leg**, contradicting the
    helper's own doc comment: *"It never stops early: a store gets told about all of its contract
    breaks in one run."* Measured (§6.10): a store that accepts but never persists is told about the
    read-back miss and **not** about its broken inboxes. Surfaced by this delivery's audit as the
    root cause of a wrong prediction; deliberately **not** fixed here, because changing what an
    exported helper reports is its own decision. Fixing it would make `writeOnlyTaskStore`'s count
    genuinely become 2 — i.e. the first draft described a helper we do not have.
48. **CI runs `go test` at the 600 s default timeout** (`.github/workflows/ci.yml`,
    `scripts/coverage.sh` — neither passes `-timeout`). Nothing enforces the
    `budget × densest-package site count < 600 s` rule that §3.2 now depends on; a future budget
    raise or a new batch of `Eventually` sites can silently cross it. Consider a `-timeout` flag or
    a guard test that counts sites per package.
