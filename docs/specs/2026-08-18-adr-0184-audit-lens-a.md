# Adversarial audit — LENS A (EXECUTION)

**Bundle:** ADR-0184 / spec + plan `2026-08-18-test-wait-budget-and-conformance-completeness`
**Worktree:** `.../scratchpad/audit-a`, base `142a1a66` (bundle commit), tree clean at start.
**Toolchain observed:** `go version go1.26.4 darwin/arm64` (`go.mod` declares `go 1.25.7`).
**Method:** every claim below was RUN. Anything not run is marked `UNVERIFIED`.

---

## A-01 — CRITICAL: Plan Task 1 Step 7's `writeOnlyTaskStore` count of **2** is WRONG. The real count is **1**, for every case.

**Claim attacked:**
- plan §Task 1 Step 7: `want := 1; if c.listedBy != inboxNone { want = 2 }` for `writeOnlyTaskStore`
- spec §4.3 table: *"`writeOnlyTaskStore` | legal ⇒ `Len(failures, 1)` | the 2 cases with a `listedBy` ⇒ **2**"*
- ADR-0184 Consequences: *"Three of its expectations are re-derived."*

**What I ran:** implemented the plan's Task 1 Steps 1–5 verbatim in the worktree, then ran
`go test -count=1 -run '^TestCheckTaskStoreConformanceCatchesNonConformingStores$' ./processtest/`.
(Full observed output under A-01 EVIDENCE below.)

**Root cause, source-verified in `processtest/taskstoreconformance.go:154-157`:**

```go
got, getErr := store.Get(ctx, c.task.TaskID)
if !assert.NoErrorf(t, getErr, "Get(%s) after an accepted Upsert: the task must be readable", c.name) {
	return          // <-- EARLY RETURN
}
```

`writeOnlyTaskStore.Get` always returns `ErrTaskNotFound`, so the legal leg **returns before**
reaching the new `checkTaskStoreAcceptedTaskIsListed(...)` call that Step 5 appends at the *end* of
the function. The inbox assertion is never executed for this store, so its failure count stays at
**1** for all five legal shapes — it does not become 2 for the two `listedBy` shapes.

**Blast radius:** an implementer following Step 7 verbatim writes an expectation that is false,
sees a RED they were told to expect GREEN for, and the most likely "fix" is exactly the one the spec
forbids (loosening `Lenf`). The spec's §4.3 warning *"⚠ An implementer who 'fixes' a failing pinned
count by relaxing `assert.Lenf`…"* is aimed at a hazard the bundle itself manufactures.

**Also wrong, same root cause:** the spec §4.3 prose reason *"nothing persisted, so the positive
inbox assertion also fails"* is a claim about a code path that is unreachable for this store.

**Proposed fix (choose ONE, deliberately):**

- **(a) Correct the prediction.** Leave `writeOnlyTaskStore`'s expectation at `assert.Lenf(t, failures, 1, …)`
  unchanged, delete the `want`/`listedBy` branch from plan Step 7, and delete the
  `writeOnlyTaskStore` row from spec §4.3's table. Then ADR-0184's *"Three of its expectations are
  re-derived"* becomes **two**, and must be corrected too (see A-02).
- **(b) Change the production code so the claim becomes true** — i.e. do not `return` on a failed
  `Get`, or call `checkTaskStoreAcceptedTaskIsListed` *before* the `Get` round-trip assertions.
  This is a real design question the bundle never asks: **should a store that accepts-but-never-persists
  be told about BOTH breaks in one run, or only the first?** The helper's own doc comment says it
  *"never stops early: a store gets told about all of its contract breaks in one run"*
  (`taskstoreconformance.go:132-134`) — which (b) honours and the current code contradicts.

I recommend **(a) for this delivery + a backlog item for (b)**, because (b) changes what an exported
helper reports and deserves its own decision, but the bundle must at minimum stop asserting a
number that cannot occur.

---

## A-02 — MAJOR: ADR-0184 and spec §4.3 both say **three** expectations are re-derived. Executed, it is **two** (and one of those two is a comment, not an expectation).

**Claim attacked:**
- ADR-0184 Consequences: *"**Three** of its expectations are re-derived."*
- spec §4.3: *"**Two** pinned expectations and one comment become wrong"* — and its own summary line
  in §5.1 says *"the **5** re-derived pinned counts in §4.3"*.
- spec §7 Bad/accepted: *"**Three** pinned expectations in `taskstoreconformance_internal_test.go`
  are re-derived"*.

**What I ran:** the same Task-1 implementation as A-01; observed which stand-in rows actually flip.

**Observed:** exactly **one** stand-in's assertion changes (`inboxFailingTaskStore`), plus **one**
comment (`inboxFailingTaskStore`'s in-case comment). `writeOnlyTaskStore` does not change at all
(A-01). So the bundle carries **four mutually inconsistent counts for the same quantity**:
`three` (ADR), `two + one comment` (spec §4.3), `5` (spec §5.1), `three` (spec §7) — and the
executed answer is **one expectation + one comment**.

**Proposed fix:** after adjudicating A-01, pick the executed number and state it identically in
ADR Consequences, spec §4.3, spec §5.1 and spec §7. If A-01(a) is chosen the number is
**one expectation and one comment**. Never restate it as a bare count without the noun
("expectations" vs "counts" vs "pinned rows") — the `5` in §5.1 is that error already.

---

## A-03 — MAJOR: Plan Task 3 Step 4's first verification command CANNOT reach its "Expected" exit code. It is a guaranteed false alarm.

**Claim attacked:** plan §Task 3 Step 4:

```bash
grep -rn --include='*_test.go' -A6 "Eventually(" scheduler/ | grep -E "[0-9]*\*?time\.Second" ; echo "REMAINING_EXIT=$?"
```
> Expected: `REMAINING_EXIT=1` (grep found nothing …). ⚠ An exit of 0 … means the task is not done.

**What I ran** (verbatim, on the *unmodified* tree, to characterise the matcher):

```
REMAINING_EXIT=0     # expected on an unmodified tree
```

…but the match list contains a line that the conversion **will not remove**:

```
scheduler/internal/gocron/job_schedule_test.go-125-		clk.Advance(time.Second) // second due instant while the first run is still blocked
```

That line is `clk.Advance(time.Second)` — a **fake-clock advance**, not an `Eventually` budget. It
sits 3 lines after the `Eventually` at `job_schedule_test.go:122`, so it is inside the `-A6` window
and matches `[0-9]*\*?time\.Second` (the `[0-9]*` quantifier permits zero digits, so bare
`time.Second` matches). After a perfect conversion the command still exits **0**, and the plan tells
the implementer that means *"the task is not done"*.

The `-A6` window also sweeps in unrelated neighbours generally: `Never` budgets, message strings,
and any `time.Second` in following statements.

**Proposed fix:** replace the context-window grep with one that matches the *call argument
position*. A workable pair (both verified below to behave on the current tree):

```bash
# Any Eventually whose FIRST duration argument is still a literal, single-line or multi-line.
gofmt -l /dev/null >/dev/null; grep -rn --include='*_test.go' -E '(require|assert)\.Eventually\(' -A4 scheduler/ \
  | grep -E '^\S+[-:][0-9]+[-:].*\}, *[0-9]*\*?time\.(Second|Millisecond),' ; echo "REMAINING_EXIT=$?"
```

or, far more robust and with no window heuristics at all, assert on the converted form directly:

```bash
# every Eventually budget must now be the constant; count must equal 40
grep -rn --include='*_test.go' -c 'eventuallyBudget' scheduler/ | awk -F: '{s+=$2} END{print "BUDGET_USES="s}'
```

Whatever is chosen, the plan MUST state **what makes the command fire** (revert one site, observe
exit 0) — the bundle prescribes the command without ever demonstrating it can discriminate.

---

## A-04 — MAJOR: Plan Task 3 Step 4's second verification command is VACUOUS for 4 of the 16 `Never` sites — the multi-line ones, which are exactly where a budget is most likely to be edited by accident.

**Claim attacked:** plan §Task 3 Step 4:

```bash
git diff -U0 -- scheduler/ | grep -E "^[+-].*Never\(" ; echo "NEVER_DIFF_EXIT=$?"
```
> Expected `NEVER_DIFF_EXIT=1` (no `Never` line changed).

**What I ran:**

```
$ grep -rn --include='*_test.go' -E "(require|assert)\.Never\(" scheduler/ | wc -l
16
$ grep -rn --include='*_test.go' -E "(require|assert)\.Never\(.*time\.(Second|Millisecond)" scheduler/ | wc -l
12
```

**Observed:** 16 `Never` sites; only **12** carry their budget on the same physical line as
`Never(`. The other **4** are multi-line:

```
scheduler/internal/gocron/scheduler_test.go:141   require.Never(t, func() bool { return count() > 0 },
scheduler/internal/gocron/scheduler_test.go:161   require.Never(t, func() bool { return n.Load() > 0 },
scheduler/internal/gocron/scheduler_test.go:220   require.Never(t, func() bool { return n.Load() > 1 },
scheduler/internal/gocron/bump_regression_test.go:47  require.Never(t, func() bool {
```

For those four the budget lives on a *later* line that does not contain the substring `Never(`.
A diff hunk changing `}, 150*time.Millisecond, …` to `}, eventuallyBudget, …` therefore produces
**no line matching `^[+-].*Never\(`** — the guard reports "clean" while the exact mutation
ADR-0184 §4 forbids has landed. The guard cannot fail in the case it exists for.

Note the irony: this is the identical defect class the delivery exists to remove — an assertion that
holds for the wrong reason.

**Proposed fix:** guard on the *budgets* rather than on the call token. Either

```bash
# the 16 Never budgets, extracted structurally, must be byte-identical before and after
git stash list >/dev/null; git diff -U6 -- scheduler/ | grep -E '^[+-].*(100|150|200|300)\*time\.Millisecond' ; echo "NEVER_DIFF_EXIT=$?"
```

or, better, snapshot them first and diff the snapshot:

```bash
grep -rn --include='*_test.go' -A4 -E '(require|assert)\.Never\(' scheduler/ | grep -E 'time\.Millisecond,' | sort > /tmp/never-before.txt
# …after the conversion…
grep -rn --include='*_test.go' -A4 -E '(require|assert)\.Never\(' scheduler/ | grep -E 'time\.Millisecond,' | sort > /tmp/never-after.txt
diff /tmp/never-before.txt /tmp/never-after.txt; echo "NEVER_DIFF_EXIT=$?"
```

and, as with A-03, state what makes it fire.

---

## A-05 — INFORMATIONAL (probe 6, no defect found): `-count=25 -race` on `TestGocronScheduleJobTriggers` is cheap and is a reasonable plan step.

**Claim attacked:** plan §Task 3 Step 6, and whether `-count=25 -race` is prohibitively slow.

**What I ran:**

```
$ go test -count=25 -race -run '^TestGocronScheduleJobTriggers$' ./scheduler/internal/gocron/
EXIT=0 ELAPSED=5s
ok  	github.com/kartaladev/wrkflw/scheduler/internal/gocron	3.514s
```

Confirmed the filter actually selects the test (CLAUDE.md pitfall #5):

```
$ go test -count=1 -run '^TestGocronScheduleJobTriggers$' -v ./scheduler/internal/gocron/ | grep -E "^--- (PASS|FAIL)"
--- PASS: TestGocronScheduleJobTriggers (0.08s)
   (12 subtests, all PASS)
```

**Verdict: no defect.** 3.5 s wall for 25 `-race` iterations. The plan's own caveat (*"does not prove
the flake fixed — it passed `-count=25` before the change too"*) is correct and is the right framing.

**Minor nit worth folding in:** the plan does not tell the implementer to confirm the filter matched.
Add `-v` and a "confirm `--- PASS: TestGocronScheduleJobTriggers` appears" line, as Task 2 Step 3
already does for its own filter.
