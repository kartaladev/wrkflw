# Conformance Completeness + Test Wait Budgets — Implementation Plan

## ▶ Progress

**Status: SHIPPED.** Tasks 1–5 implemented; the Delivery Gate passed on 2026-08-19 —
`/code-review high` returned **2 findings, both fixed** (both fallout from Task 5's late
`ErrSchedulerClosed` guard: a duplicate sentinel that broke `errors.Is`, and a residual close
window now documented as backlog 50); `/security-review` returned **0 findings**.

- **Branch:** `feat/test-wait-budget-and-conformance-completeness` (do not quote a SHA — every step
  amends the single bundle commit). Cut from `main` at merge `a7575ed5` (ADR-0183).
- **Rule-#9 audit: PASSED** before any code — 3 Opus lenses (execution / failure-modes /
  re-counting) in detached worktrees created **at the bundle commit** so the documents were present
  by construction. **14 findings, ALL accepted**, 2 required an adjudicated choice. See
  `docs/specs/2026-08-18-adr-0184-audit-adjudication.md`.
- **Execution: subagent-driven, strictly SERIAL.** The plan originally allowed Task 3 to run
  concurrently with 1–2 (different Go packages), but **every task `--amend`s the same bundle
  commit**, so concurrent agents would race the git index. Package isolation does not protect a
  shared HEAD.

### Per-task outcome

| task | verdict | notes |
|---|---|---|
| 1 — positive inbox assertions | spec ✅, clean after 1 fix round | `writeOnlyTaskStore` held at 1 and only `inboxFailingTaskStore` was re-derived, exactly as the audit predicted. Fix round closed 3 Important + 3 comment Minors. |
| 2 — misuse guards via subprocess | spec ✅, approved, 0 Critical/Important | **Zero production changes** — the rejected interface seam stayed rejected. 4 Minors deferred. |
| 3 — 40 budget conversions | spec ✅, approved, 0 findings in implementer code | 40/40 converted, 16 `Never` sites byte-identical, the 2 `Never`-only files untouched. |

### ⚠ Three corrections implementation forced on this plan (rule #11)

1. **Two of the three `listedBy: inboxNone` justifications were FALSE.** `MemTaskStore.AssignedTo`
   filters solely on `Claim.Actor.ID` and **ignores `State` entirely**, so `AssignedTo("bob")` does
   return the `Cancelled`-but-claimed row — "terminal" excludes nothing. Corrected in Step 4, with
   the probe recorded.
2. **Step 4's verification command was broken — twice.** The audit replaced a `-A6` window that
   matched `clk.Advance(time.Second)`; the `-A4` replacement was *also* broken, reporting **38 of
   40** on a correct conversion (one call spans 7 lines; one passes `fired.Load` as a method value,
   so there is no `}` before the budget). Now paren-balanced. ⭐ **A grep with a fixed context
   window cannot parse a call whose length it does not know.** These variations also falsify the
   ADR's Context claim that the 40 sites are "identically-shaped" — corrected there too.
   ⚠ **Separately, "these sites are SERIAL" (§3, and repeated in this plan's own "four things" list
   below and in `HANDOVER.md`) is FALSE, though conservatively so.** Measured: 2 of
   `scheduler/internal/gocron`'s 31 sites run under `t.Parallel` (`timeskew_test.go:23-24,113` and
   `:155-156`), and 2 of `scheduler` root's 6 do. Parallelism only lowers the real wall-clock sum,
   so the `310 s < 600 s` conclusion survives — but the claim was stated as fact in five places and
   is corrected to "predominantly serial (2 of the 31 run under `t.Parallel`), so `budget × site
   count` is an upper bound."
3. **Step 4a carried a duplicate of that same broken command** and was missed when Step 4 was fixed
   — it promised `39` where the real answer was `37`. Command (b) was imprecise the same way
   (34 literals reported for 16 calls). ⭐ **When you correct a command, grep the document for every
   copy of it.**

### ⚠⚠⚠ Task 5 (unplanned): backlog 42 was MISDIAGNOSED, and the real defect was fixed here

Added after Tasks 1–4, on the owner's decision, once a stress run refuted the premise.

**What the bundle believed:** `TestGocronScheduleJobTriggers/"At (past-due)…"` was load-flaky
because its `require.Eventually` carried a 1 s real-time budget.

**What is true, measured:** it fails at `trigger_test.go:306` — `require.False(t, next.IsZero())` —
in **0.00 s**. The `Eventually` never executes. The identical assertion exists at the base commit,
so the budget change could not have fixed it. The real defect is a race in `ScheduleJob`:
`job.NextRun()` is asked *after* `NewJob` registers a `OneTimeJobStartImmediately` +
`WithLimitedRuns(1)` job, which gocron may already have run and retired — yielding
`(time.Time{}, nil)`, a zero instant with a **nil error**. Measured (fresh re-derivation, 7 runs ×
1,000 arms each, against reverted production code): **~12 % of arms without `-race`** (848/7,000),
**~0.9 % under `-race`** (63/7,000) — roughly 13× apart. ⚠ Post-fix the branch returns the
captured `now` unconditionally, so it cannot return zero by construction — "0 of N after" is not a
comparable rate, since there is no longer a race to sample.

**Fix:** `ScheduleJob` returns `now` — the same clock reading already captured earlier in the
function — for the fire-immediately case, unconditionally, not as a fallback when `NextRun()`
returns zero, which would only narrow the window.

**Severity, stated accurately:** the only non-test caller (`activateJob`) **discards** the return,
so no production path consumed the wrong value. Contract hardening, not a live production bug.

⭐ **The lesson: a test that fails in 0.00 s is not waiting out a timeout.** The diagnosis was
inherited from ADR-0183's handover, restated as fact here, and survived a three-lens audit whose
execution lens ran the test **in isolation** — where it passes — without ever reading its failure
text under contention.

### The four things a fresh session must not get wrong

1. **`writeOnlyTaskStore`'s pinned count does NOT change — it stays 1.** All three lenses refuted
   the original prediction of 2. `checkTaskStoreConformance` **returns early** at the read-back
   guard, so the new inbox assertion is never evaluated for it. Same mechanism protects
   `rejectingTaskStore`. **Only `inboxFailingTaskStore` changes.** If your Task 1 run fails
   `writeOnlyTaskStore`, you placed the new check before the guard instead of after.
2. **The budget is 10 s, NOT 30 s.** Sized against the *binary*: 31 predominantly serial sites (2
   run under `t.Parallel`) in `scheduler/internal/gocron` × 30 s = 930 s would blow `go test`'s 600 s
   default and replace 31 legible failures with a goroutine dump. Rule: `budget × densest package's
   site count < 600 s`.
3. **Coverage of `RunTaskStoreConformance` STAYS at 77.8 %.** Subprocess coverage is not merged.
   Do **not** "fix" it — that means re-introducing the `conformanceRunner` seam ADR-0184 rejected.
4. **13 files get modified, not 15.** The 2 extra carry only `assert.Never` and are forbidden.

### Four Minors from the final whole-branch review (inlined here, not in a gitignored file)

`HANDOVER.md` previously pointed at the SDD ledger (`.superpowers/sdd/…/progress.md`), which is
gitignored and does not survive a lost machine — rule #10's whole reason for `HANDOVER.md` living
in-repo. Inlined here instead:

1. Guard markers (`WRKFLW_PROCESSTEST_NIL_NEWSTORE`, `WRKFLW_PROCESSTEST_NIL_STORE`,
   `WRKFLW_PROCESSTEST_FACTORY_FATAL`) are raw string literals where the file's own convention names
   them as consts and reuses the const — these three are consts, but nothing enforces that a future
   addition follows suit. Deferred.
2. `runConformanceHelperChild`'s doc claimed to be *"the shared body of the parent tests above"*,
   but `TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest` still spawned its child
   process inline instead of calling it. **FIXED in the final review-fix wave**: that test now calls
   `runConformanceHelperChild(t, factoryFatalHelper, factoryFatalEnv)`, re-verified green.
3. `TestRunTaskStoreConformanceRefusesANilStore` has no explicit "every case still got its turn"
   assertion. ⚠ **Still DEFERRED, but its original justification was FALSE and must not be carried
   forward.** The deferral had argued *"a parent-level `FailNow` regression would still pass, so this
   is untested — measured 8 subtest failures normally, 1 under mutation."* Measured instead: the
   test's existing `attributedTest`/`strings.HasPrefix(…, nilStoreHelper+"/")` assertion already
   requires the failure be attributed to a SUBTEST of `nilStoreHelper`, which a parent-level
   `FailNow` cannot produce — so that regression is already caught, and the real split is **8
   normally / 0 under that mutation**, not 8/1. The gap is real (no count of "all 8 ran"), just not
   for the stated reason.
4. `_test.go` comments reference `RunTaskStoreConformance` as bare text rather than the bracketed
   `[RunTaskStoreConformance]` godoc link form used elsewhere — deferred, since godoc does not render
   test-file comments and the link form buys nothing there.

### Verification commands, as executed during design

- vacuity proven: a blind-inbox store passes the current suite — `EXIT=0`
- constructibility proven: `ClaimableBy(alice) n=1`, `AssignedTo("alice") n=1` against `MemTaskStore`
- the flaky test isolated: `-race` PASS in **0.01 s** against a 1 s budget (~100× margin)
- all three bundled stores pass the tightened contract (audit-measured, SQLite is pure-Go)

---


> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close backlog 42 (a real-time `Eventually` budget makes a fake-clock scheduler test
load-flaky) and backlog 43 (the exported `RunTaskStoreConformance` passes a store whose inbox
queries return nothing, and two of its misuse guards are never executed).

**Architecture:** Two independent halves sharing no code and no file. Half A adds one unexported
`eventuallyBudget` const to each of 4 test packages under `scheduler/` and replaces 40 timeout
literals. Half B adds a positive-inbox expectation to the conformance case set, re-derives three
pinned failure counts that depended on the legal leg never touching an inbox, and tests the two
misuse guards through the subprocess harness the package already has.

**Tech Stack:** Go 1.25, testify v1.11.1 (`require.Eventually` retained — no new wait primitive),
`humantask`/`processtest` root packages, `gocron` v2.22.0 behind `scheduler/internal/gocron`.

**Spec:** `docs/specs/2026-08-18-test-wait-budget-and-conformance-completeness.md`
**ADR:** `docs/adr/0184-conformance-completeness-and-test-wait-budgets.md`

## Global Constraints

- **Branch:** `feat/test-wait-budget-and-conformance-completeness`. The spec and ADR are already
  committed on it. **Fold every change into that one commit with `git commit --amend`** — never
  stack `fix:` commits. The branch is unpushed until the Delivery Gate passes.
- **TDD strict.** Half B is behavioural: a visible RED must precede every new assertion, run as its
  own `Bash` call. Half A is a pure refactor with no behavioural change — no new test, but the
  package must be green **before and after** (CLAUDE.md, "What Counts as New Behaviour").
- **Black-box tests** (`package <pkg>_test`) except where the existing file is internal.
  `processtest` mixes both: `taskstoreconformance_internal_test.go` is `package processtest`,
  `taskstoreconformance_test.go` and `taskstoreconformance_factory_test.go` are
  `package processtest_test`. **Run `head -1` before writing into any existing test file.**
- **Fan out by Go package.** Tasks 1 and 2 are both in `processtest` and must run **strictly
  serial**. Task 3 is in `scheduler/...` and may run concurrently with them.
- **Docker:** not needed by any task here. `processtest` and `scheduler` (except the two elector
  packages) are container-free. ⚠ `scheduler/internal/gocron/pgelector` and `.../myelector` **do**
  need Docker; Task 3 must not run their tests without it — compile-check them instead.
- **Judge every test run by its exit code**, never a pipeline tail. Use
  `go test ... > /tmp/out.log 2>&1; echo "EXIT=$?"` then read the log. Always `-count=1`.
- **Restore a mutation from a `cp` backup, never `git checkout <path>`** — that restores from the
  index and destroys uncommitted work.
- **Never change any `require.Never` / `assert.Never` budget.** 16 sites; they are observation
  windows paid on every green run (ADR-0184 §4).

---

## File Structure

**Half B — `processtest` (Tasks 1–2)**

| file | package | responsibility | change |
|---|---|---|---|
| `processtest/taskstoreconformance.go` | `processtest` | the exported helper + case set + checks | **modify** — add `inboxExpectation`, the `listedBy` field, the positive assertions |
| `processtest/taskstoreconformance_internal_test.go` | `processtest` | stand-in stores + pinned failure counts | **modify** — add `blindInboxTaskStore`, re-derive 3 expectations |
| `processtest/taskstoreconformance_factory_test.go` | `processtest_test` | subprocess harness for fatal paths | **modify** — 2 helper tests + 2 parent tests |

**Half A — `scheduler` (Task 3)**

| file | package | change |
|---|---|---|
| `scheduler/waitbudget_test.go` | `scheduler_test` | **create** — the const |
| `scheduler/internal/gocron/waitbudget_test.go` | `gocron_test` | **create** — the const |
| `scheduler/internal/gocron/myelector/waitbudget_test.go` | `myelector_test` | **create** — the const |
| `scheduler/internal/gocron/pgelector/waitbudget_test.go` | `pgelector_test` | **create** — the const |
| **13** existing `_test.go` files | — | **modify** — 40 timeout literals |
| `scheduler/clock_option_test.go`, `scheduler/internal/gocron/clock_option_test.go` | — | ⚠ **DO NOT TOUCH** — `Never`-only files. 13 + these 2 = the 15 that carry either helper |
| `processtest/taskstoreconformance_test.go` | `processtest_test` | ⚠ **unmodified, but MUST be re-run** — the only place the exported helper meets the module's own three stores |

---

## Task 1: The conformance legal leg must prove the inboxes answer (backlog 43b)

**Files:**
- Modify: `processtest/taskstoreconformance.go`
- Modify: `processtest/taskstoreconformance_internal_test.go`

**Interfaces:**
- Produces: `inboxExpectation` (unexported), its three values `inboxNone`, `inboxAssigned`,
  `inboxClaimable`, and a `listedBy inboxExpectation` field on `taskStoreConformanceCase`.
  Task 2 does not use these.
- Consumes: existing `taskStoreConformanceProbe` (`authz.Actor{ID: "alice", Roles: []string{"manager"}}`),
  `taskStoreConformanceIDs`, `conformanceReporter`, `recorderT`.

- [x] **Step 1: Write the failing test — a store with blind inboxes must be CAUGHT**

Add to `processtest/taskstoreconformance_internal_test.go` (`package processtest` — verify with
`head -1`). Place `blindInboxTaskStore` next to the other stand-ins, and the case inside
`TestCheckTaskStoreConformanceCatchesNonConformingStores`'s `cases` slice:

```go
// blindInboxTaskStore is conforming on the write path and answers BOTH inbox
// queries with nothing. It is the store that motivated ADR-0184: every
// not-listed assertion on the rejected leg holds vacuously for it, so before the
// legal leg gained a positive expectation this store passed the whole suite.
type blindInboxTaskStore struct{ *humantask.MemTaskStore }

func (blindInboxTaskStore) AssignedTo(context.Context, string) ([]humantask.HumanTask, error) {
	return nil, nil
}

func (blindInboxTaskStore) ClaimableBy(context.Context, authz.Actor) ([]humantask.HumanTask, error) {
	return nil, nil
}
```

```go
		{
			// The vacuity ADR-0184 closes. This store validates correctly, persists
			// correctly and reads back correctly — it simply never lists anything.
			// Only the legal shapes carrying a positive inbox expectation can catch
			// it; the rejected leg cannot, because "not listed" is exactly what a
			// store that lists nothing does.
			name: "a store whose inboxes never list anything fails only the legal shapes that must be listed",
			newStore: func() humantask.TaskStore {
				return blindInboxTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
			},
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal && c.listedBy != inboxNone {
					assert.Lenf(t, failures, 1,
						"%q declares listedBy=%v, so a store that lists nothing must FAIL exactly once; got %v",
						c.name, c.listedBy, failures)
					return
				}
				assert.Emptyf(t, failures,
					"%q has no positive inbox expectation, so a blind-inbox store must pass it: %v", c.name, failures)
			},
		},
```

- [x] **Step 2: Run it and verify RED**

```bash
go test -count=1 -run '^TestCheckTaskStoreConformanceCatchesNonConformingStores$' ./processtest/ > /tmp/t1-red.log 2>&1; echo "EXIT=$?"; cat /tmp/t1-red.log
```

Expected: **FAIL to compile** — `c.listedBy undefined`, `inboxNone undefined`. That compile error
is a valid RED (CLAUDE.md). Do not proceed until you have seen it.

- [x] **Step 3: Add the expectation type and field**

In `processtest/taskstoreconformance.go`, above `taskStoreConformanceCase`:

```go
// inboxExpectation names the inbox query that MUST return a legal shape.
//
// It is what stops the not-listed assertions on the rejected leg from passing
// vacuously: a store whose AssignedTo and ClaimableBy always return nothing
// satisfies every one of them for the wrong reason, and passed this entire
// suite before this existed (ADR-0184).
type inboxExpectation int

const (
	// inboxUnset is the zero value and is NEVER correct: it means the author did
	// not decide. TestTaskStoreConformanceCasesCoverBothSides rejects it, so a
	// new legal case cannot silently inherit the weakest contract — which would
	// be this ADR's own vacuity, reintroduced one layer up.
	inboxUnset inboxExpectation = iota
	// inboxNone declares, DELIBERATELY, that neither query is contracted to
	// return this shape: the terminal shapes and the anonymous kiosk claimant.
	inboxNone
	// inboxAssigned means AssignedTo(the claimant) must return the task. The case
	// MUST carry a Claim with a non-empty Actor.ID; the invariant test pins it.
	inboxAssigned
	// inboxClaimable means ClaimableBy(the probe actor) must return the task.
	inboxClaimable
)

// String makes a failure message name the query rather than an integer.
func (e inboxExpectation) String() string {
	switch e {
	case inboxAssigned:
		return "AssignedTo"
	case inboxClaimable:
		return "ClaimableBy"
	case inboxNone:
		return "none"
	default:
		return "UNSET"
	}
}
```

Add the field to `taskStoreConformanceCase`, after `legal`:

```go
	// listedBy names the inbox query that MUST return this shape. Only legal
	// shapes carry one; see [inboxExpectation].
	listedBy inboxExpectation
```

- [x] **Step 4: Declare the expectation on each case**

Set `listedBy` on **all eight** cases — including the three rejected ones, which must declare
`inboxNone` explicitly. ⚠ Relying on the zero value is exactly what `inboxUnset` exists to prevent,
and it would make the case set silently depend on iota ordering nobody re-checks.

```go
		{
			name:     "unclaimed_without_a_claim_is_accepted",
			why:      "the shape every task starts in",
			task:     task("wrkflw-conformance-unclaimed", humantask.Unclaimed, nil),
			legal:    true,
			listedBy: inboxClaimable, // Unclaimed + Eligibility.Roles ["manager"], which the probe holds
		},
		{
			name:     "claimed_with_a_claim_is_accepted",
			why:      "the ordinary held shape",
			task:     task("wrkflw-conformance-claimed", humantask.Claimed, claim(authz.Actor{ID: "alice", Roles: []string{"manager"}})),
			legal:    true,
			listedBy: inboxAssigned, // the claimant IS taskStoreConformanceProbe
		},
```

The remaining three legal cases (`..._kiosk`, `..._completed`, `..._cancelled`) get an explicit
`listedBy: inboxNone` with the reason on the line:

```go
			listedBy: inboxNone, // the claimant has no ID; there is no inbox to ask about
			listedBy: inboxNone, // no claim to match AssignedTo, and Completed isn't Unclaimed so ClaimableBy excludes it
			listedBy: inboxNone, // terminal listing is not contracted; this case makes no assertion about presence in either inbox
```

> ⚠ **CORRECTED DURING IMPLEMENTATION (rule #11).** An earlier draft of this plan gave the last two
> reasons as *"terminal: neither query is contracted to return it"* and *"terminal, and claimed by
> bob rather than the probe"*. **Both were false, proven by probe:** `MemTaskStore.AssignedTo`
> filters solely on `Claim.Actor.ID` and **ignores `State` entirely**, so `AssignedTo("bob")` DOES
> return the `Cancelled`-but-claimed row — terminality excludes nothing. And "rather than the probe"
> was a non-reason, since the shipped check asks `AssignedTo(c.task.Claim.Actor.ID)` — bob — not the
> probe; that reason belonged to an earlier probe-only design. The real reasons are per-case and
> narrower, as written above. Caught by the task review, re-verified independently by both the
> implementer and the re-review.

And the three **rejected** cases each get:

```go
			listedBy: inboxNone, // rejected shapes are covered by the not-listed assertions
```

- [x] **Step 4a: Pin the three ways a case can be declared wrong**

Add to the per-case loop in `TestTaskStoreConformanceCasesCoverBothSides`
(`taskstoreconformance_internal_test.go`). Each of these fails today for a different reason, so none
is decorative:

```go
		assert.NotEqualf(t, inboxUnset, c.listedBy,
			"case %q must DECIDE its inbox expectation; inboxNone is the explicit \"neither query returns it\"", c.name)
		assert.Truef(t, c.legal || c.listedBy == inboxNone,
			"case %q is rejected, so listedBy=%v is a silent no-op — the check runs only on the legal leg", c.name, c.listedBy)
		if c.listedBy == inboxAssigned {
			if assert.NotNilf(t, c.task.Claim,
				"case %q declares inboxAssigned, so it MUST carry the Claim naming the claimant", c.name) {
				assert.NotEmptyf(t, c.task.Claim.Actor.ID,
					"case %q declares inboxAssigned, but an empty actor id identifies no actor: AssignedTo(\"\") returns nothing", c.name)
			}
		}
```

- [x] **Step 5: Assert the expectation on the legal leg**

In `checkTaskStoreConformance`, replace the final two `assert.Equalf` lines' trailing position —
keep them, and append a call:

```go
	assert.Equalf(t, c.task.Claim != nil, got.Claim != nil,
		"Get(%s): the presence of a Claim must round-trip — the claim invariant is stated over it", c.name)
	checkTaskStoreAcceptedTaskIsListed(ctx, t, store, c)
```

Add the new check beside `checkTaskStoreRejectedTaskIsNotListed`:

```go
// checkTaskStoreAcceptedTaskIsListed asserts that an accepted task reaches the
// inbox its shape belongs in.
//
// Without it the not-listed assertions on the rejected leg are VACUOUS: a store
// whose AssignedTo and ClaimableBy always return nothing satisfies every one of
// them, and passed this entire suite before ADR-0184. This is the check that
// establishes the queries answer at all, so the negative ones mean something.
//
// Shapes declaring inboxNone are skipped rather than asserted absent: a store may
// filter more loosely than the contract, and over-listing is the rejected leg's
// concern.
func checkTaskStoreAcceptedTaskIsListed(ctx context.Context, t conformanceReporter, store humantask.TaskStore, c taskStoreConformanceCase) {
	t.Helper()

	switch c.listedBy {
	case inboxAssigned:
		// Guard, not an invariant restated: this is EXPORTED API, and a nil
		// dereference here takes down a consumer's whole test binary with a
		// message naming neither the case nor the misuse. The case-set invariant
		// test catches a bad declaration in THIS repo; this catches it anywhere.
		if !assert.NotNilf(t, c.task.Claim,
			"case %q declares listedBy=inboxAssigned but carries no Claim: there is no claimant to ask AssignedTo about", c.name) {
			return
		}
		assigned, err := store.AssignedTo(ctx, c.task.Claim.Actor.ID)
		if assert.NoErrorf(t, err, "AssignedTo(%s) on an accepted task: the query must answer; got %v", c.name, err) {
			assert.Containsf(t, taskStoreConformanceIDs(assigned), c.task.TaskID,
				"AssignedTo(%s): an accepted task must reach its claimant's inbox — a store that lists nothing "+
					"would otherwise satisfy every not-listed assertion vacuously", c.name)
		}
	case inboxClaimable:
		claimable, err := store.ClaimableBy(ctx, taskStoreConformanceProbe)
		if assert.NoErrorf(t, err, "ClaimableBy(%s) on an accepted task: the query must answer; got %v", c.name, err) {
			assert.Containsf(t, taskStoreConformanceIDs(claimable), c.task.TaskID,
				"ClaimableBy(%s): an accepted Unclaimed task the actor is eligible for must reach the claimable "+
					"inbox — a store that lists nothing would otherwise satisfy every not-listed assertion vacuously", c.name)
		}
	case inboxUnset, inboxNone:
	}
}
```

- [x] **Step 6: Run and verify the new case is GREEN**

```bash
go test -count=1 -run '^TestCheckTaskStoreConformanceCatchesNonConformingStores$' ./processtest/ > /tmp/t1-green.log 2>&1; echo "EXIT=$?"; cat /tmp/t1-green.log
```

Expected: the new `blindInboxTaskStore` case PASSES, and **exactly ONE other stand-in fails**
(`inboxFailingTaskStore`) — that is Step 7's work, not a mistake. Record the counts.

⚠ **`writeOnlyTaskStore` must still PASS unchanged.** If it fails, stop: you have placed
`checkTaskStoreAcceptedTaskIsListed` before the read-back guard rather than after it. See Step 7.

- [x] **Step 7: Re-derive the ONE pinned expectation the legal leg just invalidated**

⚠ **Re-derive; do not loosen.** `assert.Lenf` → `assert.NotEmptyf` would destroy the property this
file exists to protect ("Pinning the count keeps every MUST live").

> ⚠⚠ **`writeOnlyTaskStore` is NOT affected — do not change it.** An earlier draft of this plan
> predicted its count would go from 1 to 2 for the two `listedBy` shapes. That was **wrong**, and
> all three audit lenses caught it independently by executing it. `checkTaskStoreConformance`
> **returns early** when the read-back fails:
>
> ```go
> got, getErr := store.Get(ctx, c.task.TaskID)
> if !assert.NoErrorf(t, getErr, …) { return }   // writeOnlyTaskStore ALWAYS lands here
> ```
>
> …so the new inbox assertion is never evaluated for it and the count stays **1 for all five legal
> shapes**. `rejectingTaskStore` is protected by the same mechanism (its `Upsert` refusal returns
> even earlier). Measured before → after: both **1 → 1**.

Only `inboxFailingTaskStore` changes. Its in-case comment is now false and must be rewritten:

```go
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal {
					// Since ADR-0184 the legal leg DOES ask an inbox, but only for a
					// shape declaring one; those report the unanswerable query once.
					want := 0
					if c.listedBy != inboxNone {
						want = 1
					}
					assert.Lenf(t, failures, want,
						"%q must FAIL once iff it declares an inbox (%v) this store cannot answer; got %v",
						c.name, c.listedBy, failures)
					return
				}
				assert.Lenf(t, failures, 2,
					"%q must FAIL once for each unanswerable inbox query; got %v", c.name, failures)
			},
```

⚠ Also update that stand-in's **doc comment**, which currently ends *"a not-listed assertion alone
would pass it while the store is unusable"* — still true — but the case comment inside the test
that says *"The legal leg queries no inbox, so this store passes it"* is now **false**. Replace it
with the reason above.

- [x] **Step 8: Run the whole processtest package and verify GREEN**

```bash
go test -count=1 -race ./processtest/ > /tmp/t1-all.log 2>&1; echo "EXIT=$?"; tail -40 /tmp/t1-all.log
```

Expected: EXIT=0.

- [x] **Step 8a: Run the exported helper against the module's OWN three stores**

This is the only place the tightened exported contract meets real implementations — a bundled store
that failed it would be a shipped bug, and the rest of this task cannot detect one. SQLite is
pure-Go, so **no Docker is needed**.

```bash
go test -count=1 -run '^TestRunTaskStoreConformance$' ./processtest/ -v > /tmp/t1-stores.log 2>&1; echo "EXIT=$?"; grep -E '^(---|    ---) (PASS|FAIL)' /tmp/t1-stores.log | head -20
```

Expected: EXIT=0 **and all three legs visibly `--- PASS`** — `MemTaskStore`,
`CachingTaskStore_over_MemTaskStore`, and `sqlite_HumanTaskStore`. ⚠ A `-run` filter matching
nothing also exits 0, so confirm the three names actually appear. The audit measured all three
passing with this change applied; a FAIL here is a real defect in that store, not in the helper.

- [x] **Step 9: Mutation-verify both new assertions**

Each must be shown load-bearing. **Back up with `cp`, never `git checkout`.**

```bash
cp processtest/taskstoreconformance.go /tmp/tsc.bak
```

Mutation 1 — neuter the `inboxClaimable` arm (make it a no-op `case inboxClaimable:`), then:

```bash
go test -count=1 -run '^TestCheckTaskStoreConformanceCatchesNonConformingStores$' ./processtest/ > /tmp/t1-mut1.log 2>&1; echo "EXIT=$?"; grep -c FAIL /tmp/t1-mut1.log
```

Expected: **FAIL** — the blind-inbox case's `Lenf(failures, 1)` now sees 0 for the `Unclaimed` shape.

```bash
cp /tmp/tsc.bak processtest/taskstoreconformance.go && diff /tmp/tsc.bak processtest/taskstoreconformance.go && echo RESTORED
```

Mutation 2 — same for the `inboxAssigned` arm. Expected FAIL for the `Claimed` shape. Restore and
`diff` again.

- [x] **Step 10: Amend the bundle commit**

```bash
git add processtest/taskstoreconformance.go processtest/taskstoreconformance_internal_test.go
git commit --amend --no-edit
```

---

## Task 2: Execute the two misuse guards through the subprocess harness (backlog 43a)

**Files:**
- Modify: `processtest/taskstoreconformance_factory_test.go` (`package processtest_test` — verify
  with `head -1`)

**Interfaces:**
- Consumes: existing helpers in that file — `attributedTest(output, marker) string` and
  `verboseTestName(line) (string, bool)`, both already generic. **Reuse them; do not write parallel
  copies.**
- Produces: `runConformanceHelperChild(t, helper, env) (string, error)`, and a **widened**
  `failedSubtests`.

> ⚠ **`failedSubtests` must be parameterised first.** It currently hardcodes the helper name:
> `strings.Contains(line, "--- FAIL: "+factoryFatalHelper+"/")`. Called as-is from a new test it
> silently returns **0** for every input, which would make `assert.Zerof(t, failedSubtests(...))`
> pass vacuously — the exact defect class this delivery exists to remove. Change it to
> `failedSubtests(output, helper string) int` matching `"--- FAIL: "+helper+"/"`, and update its one
> existing caller in
> `TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest` to pass `factoryFatalHelper`.

⚠ **Strictly serial after Task 1** — same Go package; concurrent agents break each other's compile.

⚠ **No production code changes in this task.** The guards at `taskstoreconformance.go:249` and
`:256` stay exactly as they are. The spec's earlier `conformanceRunner` seam is **withdrawn**
(spec §4.1) — do not implement it.

- [x] **Step 1a: Widen `failedSubtests` and fix its existing caller**

```go
// failedSubtests counts the `--- FAIL:` lines naming a subtest of helper.
func failedSubtests(output, helper string) int {
	n := 0
	for line := range strings.Lines(output) {
		if strings.Contains(line, "--- FAIL: "+helper+"/") {
			n++
		}
	}
	return n
}
```

Update its one existing call inside
`TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest` to
`failedSubtests(output, factoryFatalHelper)`. Verify the package still builds and that test still
passes before going further:

```bash
go test -count=1 -run '^TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest$' ./processtest/ -v > /tmp/t2-pre.log 2>&1; echo "EXIT=$?"; tail -10 /tmp/t2-pre.log
```

Expected: EXIT=0 and the test visibly `--- PASS` (a `-run` filter matching nothing also exits 0).

- [x] **Step 1: Add the two env-armed helper tests**

Add alongside `TestConformanceFactoryFatalHelper`, plus their env/marker consts next to the existing
ones:

```go
const (
	// nilNewStoreEnv arms [TestConformanceNilNewStoreHelper], which FAILS by
	// design and so must stay skipped in an ordinary run.
	nilNewStoreEnv    = "WRKFLW_PROCESSTEST_NIL_NEWSTORE"
	nilNewStoreHelper = "TestConformanceNilNewStoreHelper"
	// nilStoreEnv arms [TestConformanceNilStoreHelper], likewise.
	nilStoreEnv    = "WRKFLW_PROCESSTEST_NIL_STORE"
	nilStoreHelper = "TestConformanceNilStoreHelper"
)

// TestConformanceNilNewStoreHelper is a fixture, not an assertion: it hands
// RunTaskStoreConformance a nil factory, which the helper must refuse BEFORE
// running any case. It fails on purpose and runs only in a child process.
func TestConformanceNilNewStoreHelper(t *testing.T) {
	if os.Getenv(nilNewStoreEnv) != "1" {
		t.Skipf("armed only by the child process of TestRunTaskStoreConformanceRefusesANilFactory (%s=1)", nilNewStoreEnv)
	}
	processtest.RunTaskStoreConformance(t, nil)
}

// TestConformanceNilStoreHelper hands RunTaskStoreConformance a factory that
// returns a nil store — a consumer whose constructor silently yields nothing.
// It fails on purpose and runs only in a child process.
func TestConformanceNilStoreHelper(t *testing.T) {
	if os.Getenv(nilStoreEnv) != "1" {
		t.Skipf("armed only by the child process of TestRunTaskStoreConformanceRefusesANilStore (%s=1)", nilStoreEnv)
	}
	processtest.RunTaskStoreConformance(t, func(*testing.T) humantask.TaskStore { return nil })
}
```

- [x] **Step 2: Add the two parent tests**

```go
// TestRunTaskStoreConformanceRefusesANilFactory pins the guard at the top of
// RunTaskStoreConformance. A nil factory must be refused BEFORE any subtest
// runs — the alternative is a nil-func panic inside the first case, which names
// the case rather than the misuse.
func TestRunTaskStoreConformanceRefusesANilFactory(t *testing.T) {
	if os.Getenv(nilNewStoreEnv) == "1" {
		t.Skip("already inside the child process; running this again would spawn another")
	}
	t.Parallel()

	output, err := runConformanceHelperChild(t, nilNewStoreHelper, nilNewStoreEnv)

	require.Error(t, err, "the helper must FAIL: a nil factory is refused:\n%s", output)
	assert.Containsf(t, output, "requires a non-nil newStore",
		"the guard's own message must reach the output:\n%s", output)
	assert.Zerof(t, failedSubtests(output, nilNewStoreHelper),
		"the refusal must happen before any case runs, so no SUBTEST may fail:\n%s", output)
	assert.NotContainsf(t, output, "nil pointer dereference",
		"the guard must report the misuse, not let a nil func value panic:\n%s", output)
}

// TestRunTaskStoreConformanceRefusesANilStore pins the per-case guard: a factory
// that returns nil is reported against the case it broke, not as a panic.
func TestRunTaskStoreConformanceRefusesANilStore(t *testing.T) {
	if os.Getenv(nilStoreEnv) == "1" {
		t.Skip("already inside the child process; running this again would spawn another")
	}
	t.Parallel()

	output, err := runConformanceHelperChild(t, nilStoreHelper, nilStoreEnv)

	require.Error(t, err, "the helper must FAIL: a nil store is refused:\n%s", output)
	assert.Containsf(t, output, "returned a nil humantask.TaskStore",
		"the guard's own message must reach the output:\n%s", output)
	assert.Truef(t, strings.HasPrefix(attributedTest(output, "returned a nil humantask.TaskStore"), nilStoreHelper+"/"),
		"the guard runs per case, so its message must be attributed to a SUBTEST, not to %q:\n%s",
		attributedTest(output, "returned a nil humantask.TaskStore"), output)
	assert.NotContainsf(t, output, "nil pointer dereference",
		"the guard must report the misuse, not let a nil store panic on first use:\n%s", output)
}

// runConformanceHelperChild runs one env-armed helper test in a child `go test`
// and returns its combined output. It is the shared body of the parent tests
// above, which differ only in what they assert about that output.
func runConformanceHelperChild(t *testing.T, helper, env string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-run", "^"+helper+"$", ".")
	cmd.Env = append(os.Environ(), env+"=1")
	out, err := cmd.CombinedOutput()
	output := string(out)

	require.NotContains(t, output, "no tests to run",
		"the -run filter selected nothing, so this test proves nothing:\n%s", output)
	require.NotContains(t, output, "SKIP", "the helper must be armed by %s, not skipped:\n%s", env, output)
	return output, err
}
```

- [x] **Step 3: Run and verify GREEN**

The guards already exist, so these tests should pass immediately. That is expected for a
**retroactive** coverage addition, and it must be disclosed rather than presented as a red-green
cycle (CLAUDE.md, "Self-Audit Before Committing"). The RED evidence is Step 4's mutation.

```bash
go test -count=1 -run 'TestRunTaskStoreConformanceRefuses' ./processtest/ -v > /tmp/t2.log 2>&1; echo "EXIT=$?"; tail -30 /tmp/t2.log
```

Expected: EXIT=0, and **both tests appear as `--- PASS`** in the log. ⚠ A `-run` filter that
matches nothing exits 0 — confirm both names actually ran.

- [x] **Step 4: Mutation-verify both guards**

```bash
cp processtest/taskstoreconformance.go /tmp/tsc2.bak
```

Mutation 1 — delete the `if newStore == nil { t.Fatal(...) }` block. Run Step 3's command.
Expected: `TestRunTaskStoreConformanceRefusesANilFactory` **FAILS** (a nil func panics instead).
Restore with `cp /tmp/tsc2.bak ...` and `diff`.

Mutation 2 — delete the `if store == nil { t.Fatal(...) }` block. Expected:
`TestRunTaskStoreConformanceRefusesANilStore` **FAILS**. Restore and `diff`.

- [x] **Step 5: Amend**

```bash
git add processtest/taskstoreconformance_factory_test.go
git commit --amend --no-edit
```

---

## Task 3: Share the Eventually budget across scheduler's 4 test packages (backlog 42)

**Files:**
- Create: `scheduler/waitbudget_test.go`, `scheduler/internal/gocron/waitbudget_test.go`,
  `scheduler/internal/gocron/myelector/waitbudget_test.go`,
  `scheduler/internal/gocron/pgelector/waitbudget_test.go`
- Modify: the **13** test files enumerated in Step 3
- ⚠ **Do NOT modify** `scheduler/clock_option_test.go` or
  `scheduler/internal/gocron/clock_option_test.go` — they contain **only** `assert.Never` sites.
  13 + those 2 = the 15 files that carry either helper; an earlier draft said "15 files to modify",
  which would send you hunting for two files you must not touch.

**Interfaces:**
- Produces: `eventuallyBudget` (unexported const) in each of the 4 test packages. Nothing outside
  `scheduler/` consumes it.

⚠ **This is a pure refactor with no behavioural change.** There is no test that can go red for it,
and the plan deliberately prescribes none — a test asserting "the constant is 30s" would be
vacuous. Verification is: green before, green after, and the enumeration complete.

- [x] **Step 1: Confirm GREEN before touching anything**

```bash
go test -count=1 ./scheduler/ ./scheduler/internal/gocron/ > /tmp/t3-before.log 2>&1; echo "EXIT=$?"; tail -5 /tmp/t3-before.log
```

Expected: EXIT=0. (The two elector packages need Docker; they are compile-checked in Step 5.)

- [x] **Step 2: Create the four const files**

Same body in each; only `package` differs (`scheduler_test`, `gocron_test`, `myelector_test`,
`pgelector_test`):

```go
package gocron_test

import "time"

// eventuallyBudget is the ceiling every require.Eventually in this package waits
// before declaring failure. It is a FAILURE ceiling, not an expected latency: a
// green run returns as soon as its condition holds — typically microseconds — so
// sizing this for the worst contended CI machine costs nothing on the passing
// path, and only a test that was already going to fail waits it out.
//
// It replaces per-site literals of 1–3 s. Those made
// TestGocronScheduleJobTriggers/"At (past-due) fires immediately" fail under
// full-suite -race contention while passing -count=25 in isolation (backlog 42):
// that job fires via gocron.OneTimeJobStartImmediately on a REAL-time goroutine,
// so the fake clock does not bound it and one second of wall time is a bet on
// machine load rather than an assertion about the code.
//
// ⚠ Deliberately NOT used for Never (require.Never OR assert.Never). A Never
// budget is an observation window paid in full on every GREEN run, so raising it
// is pure cost (ADR-0184 §4).
//
// ⚠ SIZED AGAINST THE BINARY, NOT THE SITE. go test's default timeout is 600s
// per binary and these sites are predominantly SERIAL (2 of the 31 run under
// t.Parallel), so budget × site count is an UPPER BOUND, not an exact figure.
// scheduler/internal/gocron carries 31 of the 40 sites, and the realistic red is
// systemic (every site shares one scheduler), so a mass failure costs
// budget × 31. At 30s that is 930s — the binary dies with "panic: test timed
// out" and a goroutine dump printing NO assertion messages. At 10s it is 310s,
// inside the timeout, with every broken site still naming itself. Rule:
// budget × the densest package's site count must stay under 600s.
const eventuallyBudget = 10 * time.Second
```

> ⚠ **NOTE — reworded during the final review-fix wave, after this Step originally shipped.** The
> shipped `pgelector`/`myelector` copies also reword the `TestGocronScheduleJobTriggers` attribution
> paragraph, since that test lives in the SIBLING `scheduler/internal/gocron` package, not in either
> of theirs — see the shipped `.../pgelector/waitbudget_test.go` and `.../myelector/waitbudget_test.go`
> for the exact wording, not this template.

- [x] **Step 3: Replace the 40 timeout literals**

Replace **only the first duration argument** (the timeout) of each `Eventually` call with
`eventuallyBudget`. Leave every tick interval (`5*time.Millisecond`, `10*time.Millisecond`)
untouched. The complete enumeration — re-derive it with the command in Step 4 rather than trusting
this list, then reconcile:

| file | lines | current |
|---|---|---|
| `scheduler/elector_test.go` | 141 | `3*time.Second` |
| `scheduler/scheduler_surface_test.go` | 227 | `2*time.Second` |
| `scheduler/scheduler_test.go` | 123 | `time.Second` |
| `scheduler/start_test.go` | 48, 74 | `2*time.Second` |
| `scheduler/timeskew_test.go` | 109 | `2*time.Second` |
| `scheduler/internal/gocron/bump_regression_test.go` | 55 | `2*time.Second` |
| `scheduler/internal/gocron/job_schedule_test.go` | 57, 91, 122, 131, 158 | `time.Second` |
| `scheduler/internal/gocron/monitor_test.go` | 136, 154, 172, 186 | `time.Second` |
| `scheduler/internal/gocron/scheduler_test.go` | 324 | `2*time.Second` |
| `scheduler/internal/gocron/timeskew_test.go` | 138, 179 | `2*time.Second` |
| `scheduler/internal/gocron/trigger_test.go` | 40, 61, 64, 88, 109, 130, 171, 212, 283, 285, 308, 324, 341, 359, 377, 395, 413, 448 | `time.Second` |
| `scheduler/internal/gocron/myelector/mysql_elector_heartbeat_test.go` | 46, 58 | `time.Second` |
| `scheduler/internal/gocron/pgelector/elector_heartbeat_test.go` | 55 | `3*time.Second` |

Totals to reconcile: **40 sites = 30 × `time.Second` + 8 × `2*time.Second` + 2 × `3*time.Second`**,
split by package **6 / 31 / 2 / 1**.

- [x] **Step 4: Verify the enumeration is exhausted and no `Never` was touched**

> ⚠⚠ **The two commands an earlier draft prescribed here were BOTH broken**, and the audit proved it
> by running them. The first used a `-A6` context window that also matches
> `clk.Advance(time.Second)` — a fake-clock advance three lines below an `Eventually` that the
> conversion will never remove — so it reported "not done" **after a perfect conversion**. The
> second was vacuous for the multi-line `Never` calls, i.e. exactly the ones most likely to be
> edited by accident. Both are replaced with positive, count-based checks.

```bash
# (a) Every Eventually budget is now the constant. Must print 40.
#     ⚠ Paren-balanced, NOT a fixed context window — see the correction note below.
python3 - <<'PY'
import re, glob
sites = conv = 0
for f in glob.glob('scheduler/**/*_test.go', recursive=True):
    s = open(f).read()
    for m in re.finditer(r'(?:require|assert)\.Eventually\(', s):
        d = 0
        for j in range(m.end()-1, len(s)):
            if s[j] == '(': d += 1
            elif s[j] == ')':
                d -= 1
                if d == 0: break
        sites += 1
        conv += 'eventuallyBudget' in s[m.start():j+1]
print(f"sites={sites} converted={conv}")   # expect sites=40 converted=40
PY

# (b) No Never BUDGET changed. Paren-balanced, same reason as (a): a fixed -A4
#     window sweeps in each call's TICK argument and its neighbours (measured: it
#     reports 34 literals for 16 calls), so it does not check what it claims to.
python3 - <<'PY'
import re, glob, collections
budgets = collections.Counter()
for f in glob.glob('scheduler/**/*_test.go', recursive=True):
    s = open(f).read()
    for m in re.finditer(r'(?:require|assert)\.Never\(', s):
        d = 0
        for j in range(m.end()-1, len(s)):
            if s[j] == '(': d += 1
            elif s[j] == ')':
                d -= 1
                if d == 0: break
        call = s[m.start():j+1]
        # the FIRST ms literal after the condition is the budget; the second is the tick
        budgets[re.findall(r'[0-9]+\s*\*\s*time\.Millisecond', call)[0]] += 1
print(sorted(budgets.items()))
PY
# Expected, before AND after, byte-identical:
#   [('100*time.Millisecond', 1), ('150*time.Millisecond', 10),
#    ('200*time.Millisecond', 4), ('300*time.Millisecond', 1)]   → 16 sites
```

Expected: **(a)** prints `sites=40 converted=40`; **(b)** prints the `Counter` shown above, unchanged
before and after — the 16 `Never` budgets are byte-identical. (Command (b) prints a count summary,
not a diff; there is no `NEVER_DIFF_EXIT` variable to check.)

> ⚠⚠ **CORRECTED DURING IMPLEMENTATION (rule #11) — this is the THIRD version of command (a), and
> the second one that could not discriminate.** The original used a `-A6` context window that also
> matched `clk.Advance(time.Second)`; the audit caught that and prescribed a `-A4` window requiring
> `}, ` before the budget. **That replacement was also broken**, and the implementer caught it by
> executing it: it reports **38 of 40** on a perfectly correct conversion, for two independent
> reasons — one `Eventually` call spans **7 lines** (outside a 4-line window), and one passes
> `fired.Load` directly as a method value rather than a closure, so there is no `}` before the
> budget. The implementer correctly **refused to reshape test code to satisfy the command**.
>
> ⭐ **The lesson: a grep with a fixed context window cannot parse a call whose length it does not
> know.** Any check over Go call arguments must be paren-balanced. Both broken versions were written
> by someone reasoning about what the code *probably* looks like instead of parsing it.

- [x] **Step 4a: Prove command (a) can actually discriminate**

A verification command whose failure mode has never been observed is not verification. Revert one
site by hand, confirm the count drops, restore:

```bash
cp scheduler/internal/gocron/trigger_test.go /tmp/trig.bak
# change ONE eventuallyBudget back to time.Second, then re-run the SAME
# paren-balanced check from Step 4 (a) above:
#   Expected: sites=40 converted=39
cp /tmp/trig.bak scheduler/internal/gocron/trigger_test.go && diff /tmp/trig.bak scheduler/internal/gocron/trigger_test.go && echo RESTORED
#   Expected after restore: sites=40 converted=40
```

> ⚠ **This step ALSO carried the broken grep until Task 3's review caught it.** Step 4 was corrected
> and its sibling here was not — so the plan simultaneously documented that the command undercounts
> and then told the reader to run it, promising `39` where the real answer would have been `37`.
> ⭐ **When you correct a command, grep the document for every other copy of it.** A fix applied to
> one instance of a duplicated snippet reads as done and leaves the duplicate armed.

- [x] **Step 5: Verify GREEN, and compile-check the two Docker-only packages**

```bash
go test -count=1 -race ./scheduler/ ./scheduler/internal/gocron/ > /tmp/t3-after.log 2>&1; echo "EXIT=$?"; tail -5 /tmp/t3-after.log
go vet ./scheduler/... > /tmp/t3-vet.log 2>&1; echo "VET_EXIT=$?"; cat /tmp/t3-vet.log
```

Expected: EXIT=0 and VET_EXIT=0. `go vet` compiles every test file including the Docker-only
elector packages, which is how their conversion is proven to build without a daemon.

- [x] **Step 6: Confirm the originally-flaky test still passes repeatedly**

```bash
go test -count=25 -race -run '^TestGocronScheduleJobTriggers$' ./scheduler/internal/gocron/ > /tmp/t3-flaky.log 2>&1; echo "EXIT=$?"; tail -5 /tmp/t3-flaky.log
```

Expected: EXIT=0. ⚠ This does **not** prove the flake fixed — it passed `-count=25` before the
change too. It only proves no regression. Do not report it as proof.

- [x] **Step 7: Amend**

```bash
git add scheduler/
git commit --amend --no-edit
```

---

## Task 4: Documents and backlog (controller, inline)

**Files:**
- Modify: `CHANGELOG.md`, `docs/plans/HANDOVER.md`, this plan (a `▶ Progress` block)

- [x] **Step 1: `CHANGELOG.md` ▸ Unreleased ▸ Breaking changes**

Record that `processtest.RunTaskStoreConformance` now asserts that an accepted task reaches its
inbox, so a consumer store whose list queries return nothing — and which passed before — now fails.
No signature change, so nothing recompiles differently: the same silent-break class as ADR-0183.

- [x] **Step 2: Fix the two stale lines in `HANDOVER.md`**

Line 121 and blocker 3 (line ~162) still say ADR-0183 is "implemented, not merged", contradicting
the file's own header. Correct both.

- [x] **Step 3: Add backlog items 44–46**

Exactly as enumerated in the spec §8: the 16 `Never` sites' vacuity risk; blocker 5 / `runtime/`
sites unconverted; the `testing/synctest` spike.

- [x] **Step 4: Add the `▶ Progress` block to the top of this plan**

Branch name (not a SHA — the amend moves it), which tasks landed, the adjudicated findings, and the
exact verification commands that were run with their real numbers.

- [x] **Step 5: Rewrite `HANDOVER.md`'s State + NEXT WORK sections in place** (never append).

---

## Verification (controller, before the Delivery Gate)

- [ ] `docker info` — probe. Standing permission covers the next two runs; if the daemon is down,
      say so and let the owner start it or skip, and **label any container-free subset as partial**.
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥ 85 %.
      ⚠ **`processtest`'s `RunTaskStoreConformance` STAYS at 77.8 % — that is the correct outcome,
      not a missing test.** The guards run in a **child** `go test` spawned without `-coverprofile`,
      so their counters are discarded; the audit measured both guard blocks still at hit-count 0.
      **Do not "fix" it** by moving them in-process — that re-introduces the seam ADR-0184 rejected.
      Expect `scheduler/` **unchanged**.
      ⚠ `persistence` is known-red at 84.1 % **pre-existing** (backlog 34) — do not attribute it here.
- [ ] `go test -count=1 ./...` from the repo root — EXIT=0, no regressions.
- [ ] `command -v golangci-lint` then `golangci-lint run ./...` — **repo-wide**, not package-scoped.
      If absent, offer to install or skip; never substitute `go vet`; never claim "lint clean" for a
      run that did not execute.
- [ ] Re-read the ADR and spec against the built code; correct every divergence, especially any
      behaviour the ADR promises that implementation changed.
- [ ] Sweep the diff's own comments for unexecuted claims and over-reaching quantifiers.
- [ ] `/code-review` (owner-invoked) — fix all findings, fold via `--amend`.
- [ ] `/security-review` (owner-invoked) — fix all findings, fold via `--amend`.
- [ ] Merge `--no-ff` to `main`, push, delete the branch.

⚠ **The completion claim for backlog 42 is "the bound at all 40 sites is raised from 1–3 s to 10 s,
a ~1000× margin over the measured 0.01 s fire time", NOT "the flake is fixed" and NOT "the
load-dependent bound is removed"** — `require.Eventually(t, cond, 10*time.Second, …)` is still a
real-time budget, just a much larger one. A flake that needed full-suite contention will not
reappear on demand, so "the flake is fixed" is unfalsifiable in one run. `HANDOVER.md` must carry
the accurate claim.
