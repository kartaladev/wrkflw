# Audit — Lens B: failure modes, cross-document consistency, unstated assumptions

Bundle:
- `docs/specs/2026-08-18-test-wait-budget-and-conformance-completeness.md`
- `docs/adr/0184-conformance-completeness-and-test-wait-budgets.md`
- `docs/plans/2026-08-18-test-wait-budget-and-conformance-completeness.md`

Worktree: `.../scratchpad/audit-b` @ `142a1a66`. Docker NOT available (no container tests run).

---
## B1 — CRITICAL — `writeOnlyTaskStore`'s re-derived count is FALSE; plan Step 7 makes the suite permanently RED

**Attacked:** spec §4.3 table row `writeOnlyTaskStore` — *"legal ⇒ Len(failures, 1) today; after 43b: the 2
cases with a `listedBy` ⇒ **2**; the other 3 legal ⇒ **1** … nothing persisted, so the positive inbox
assertion also fails"*; ADR-0184 Consequences *"Three of its expectations are re-derived"*; spec §5.1 row
*"the 5 re-derived pinned counts in §4.3"*; **plan Task 1 Step 7**, which prescribes the `want = 2` edit
verbatim.

**Why it is false.** `checkTaskStoreConformance` (`processtest/taskstoreconformance.go:154-157`, pre-change)
**returns early** when the read-back fails:

```go
got, getErr := store.Get(ctx, c.task.TaskID)
if !assert.NoErrorf(t, getErr, "Get(%s) after an accepted Upsert: the task must be readable", c.name) {
    return                      // <-- writeOnlyTaskStore ALWAYS lands here
}
```

`writeOnlyTaskStore.Get` returns `humantask.ErrTaskNotFound` unconditionally
(`taskstoreconformance_internal_test.go:223-225`). So the new
`checkTaskStoreAcceptedTaskIsListed` call — which plan Step 5 appends **after** the two `assert.Equalf`,
i.e. after that `return` — is **never reached** for this stand-in. The count stays **1**, not 2.

**Evidence (executed in this worktree).** Applied plan Task 1 Steps 3–5 (type, field, two `listedBy`
declarations, the new check appended exactly where Step 5 says), then plan Step 7's `writeOnlyTaskStore`
edit verbatim:

```
go test -count=1 -run '^TestCheckTaskStoreConformanceCatchesNonConformingStores$' ./processtest/ -v
EXIT=1
--- FAIL: .../a_store_whose_accepted_writes_never_persist_fails_every_legal-shape_case/claimed_with_a_claim_is_accepted
--- FAIL: .../a_store_whose_accepted_writes_never_persist_fails_every_legal-shape_case/unclaimed_without_a_claim_is_accepted
        Error:  "[... Get(claimed_with_a_claim_is_accepted) after an accepted Upsert: the task must be readable ]"
                should have 2 item(s), but has 1
```

Note the failure list contains **only** the read-back error — the inbox assertion produced nothing.

Crucially, **before** Step 7 was applied (Steps 3–5 only), `writeOnlyTaskStore` was **not** among the
failing subtests at all; the only breakage was `inboxFailingTaskStore`:

```
EXIT=1
--- FAIL: .../a_store_whose_inbox_queries_error_fails_every_invalid-shape_case/claimed_with_a_claim_is_accepted
--- FAIL: .../a_store_whose_inbox_queries_error_fails_every_invalid-shape_case/unclaimed_without_a_claim_is_accepted
```

So the true blast radius is **one** stand-in expectation (`inboxFailingTaskStore`'s legal branch) plus its
comment — not three, and not "5 re-derived pinned counts".

**Consequences if shipped as written.** An implementer following Step 7 literally reaches Step 8
(`go test ./processtest/` must be EXIT=0) and cannot get there. The two escape hatches both damage the
delivery: (a) loosen `assert.Lenf` → the spec/plan explicitly forbid it, or (b) delete the early return in
`checkTaskStoreConformance` so the inbox check always runs — a **behavioural change to exported public API
that no document authorises**, and one that would make every store failing the read-back report a second,
derivative failure. This is the ADR-0183 B8 pattern repeating: a normalization (the early return) recorded
nowhere in the bundle immunizes a path the bundle claims is defective.

**Concrete fix (pick one, and record it):**

1. **Preferred — correct the documents, change no behaviour.** Delete the `writeOnlyTaskStore` row from
   spec §4.3; correct ADR-0184 Consequences to *"one of its expectations plus one comment are re-derived"*;
   correct spec §5.1's *"the 5 re-derived pinned counts"* to *"the 1 re-derived pinned count"*; **delete plan
   Task 1 Step 7's `writeOnlyTaskStore` block entirely**. Add to spec §6 an executed note: *"a legal shape
   whose read-back fails returns before the inbox check, so `writeOnlyTaskStore` keeps `Len(failures, 1)` —
   measured."*
2. If the delivery genuinely wants the inbox failure reported alongside the read-back failure, that is a
   **separate decision** requiring an ADR paragraph, a restructure of `checkTaskStoreConformance` (the
   `Get` failure must stop the *round-trip* assertions but not the inbox one), and re-derivation of every
   stand-in count again. Do not smuggle it in as Step 7.

**Also:** plan Step 6 tells the implementer *"Two other cases are expected to FAIL now"* — correct as
measured, but it names no store, so an implementer who sees `inboxFailingTaskStore` fail and
`writeOnlyTaskStore` pass will assume they mis-applied Step 5 and "fix" it by moving the check above the
return. State the store names explicitly.

---
## B2 — MAJOR — the bundle never names the three in-repo stores that run through the tightened helper

**Attacked:** spec §6.3 (*"The two positive assertions are constructible against **the reference store**"* —
`MemTaskStore` only); the plan's **File Structure** table, which lists only
`taskstoreconformance.go`, `..._internal_test.go` and `..._factory_test.go`; ADR-0184 Decision 1, which
discusses "a store" abstractly.

**The gap.** `processtest/taskstoreconformance_test.go:27-74` (`TestRunTaskStoreConformance`) drives the
**exported** helper against **three** bundled stores, none of which appears anywhere in the spec, ADR or
plan:

- `humantask.NewMemTaskStore()`
- `store.NewHumanTaskStore(dbtest.RunTestSQLite(t), dialect.NewSQLite())` — the real SQL store
- `persistence.NewCachingTaskStore(humantask.NewMemTaskStore(), hotcache.New())` — a **decorator** with its
  own inbox caching

Tightening an exported conformance helper without checking the module's own implementations is the exact
class the delivery exists to close, applied to itself. A `CachingTaskStore` that served a stale/empty inbox
list, or a SQL `ClaimableBy` whose role predicate differed, would have been a shipped-code bug this bundle
had not noticed.

**Evidence (executed, with plan Steps 3–5 applied and Step 7 reverted).** Docker not needed — SQLite is
pure-Go:

```
go test -count=1 -run '^TestRunTaskStoreConformance$' ./processtest/ -v
EXIT=0
--- PASS: TestRunTaskStoreConformance/MemTaskStore                       (8/8 subtests PASS)
--- PASS: TestRunTaskStoreConformance/CachingTaskStore_over_MemTaskStore (8/8 subtests PASS)
--- PASS: TestRunTaskStoreConformance/sqlite_HumanTaskStore              (8/8 subtests PASS)
```

**Verdict:** no shipped-code bug — all three pass. But the bundle asserted nothing about this and the plan
gives the implementer no step that would have run it.

**Concrete fix:**
1. Add `processtest/taskstoreconformance_test.go` to the plan's **File Structure** table as
   *"unmodified — but must be re-run: it is the only place the exported helper meets the module's own
   stores."*
2. Add a step to plan Task 1 after Step 8:
   `go test -count=1 -run '^TestRunTaskStoreConformance$' ./processtest/ -v` — EXIT=0 **and** all three
   store legs visibly `--- PASS` (a `-run` filter matching nothing exits 0).
3. Replace spec §6.3's *"the reference store"* with the measured three-store result above, naming each.
4. State in ADR-0184 Consequences that the module's own bundled stores (`MemTaskStore`, the neutral SQL
   store on SQLite, and `CachingTaskStore`) were verified to satisfy the tightened contract.

---
## B3 — MAJOR — `checkTaskStoreAcceptedTaskIsListed` panics on a `listedBy: inboxAssigned` case with a nil Claim; no guard, no invariant test

**Attacked:** plan Task 1 **Step 5**, the `inboxAssigned` arm:

```go
assigned, err := store.AssignedTo(ctx, c.task.Claim.Actor.ID)
```

`c.task.Claim` is `*humantask.Claim`. Nothing in the bundle constrains `listedBy: inboxAssigned` to a case
whose `Claim != nil`, and three legal shapes in the current set carry a nil Claim
(`unclaimed_without_a_claim_is_accepted`, `completed_without_a_claim_is_accepted`, plus every rejected
shape). The declaration is a plain struct field; the compiler cannot help.

**Evidence (executed).** Throwaway probe in `package processtest` with Steps 3–5 applied, one legal case
declaring `inboxAssigned` and no Claim:

```
go test -count=1 -run '^TestZZAudB' ./processtest/ -v
EXIT=1
--- FAIL: TestZZAudBNilClaimAssigned (0.00s)
panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
    .../processtest/taskstoreconformance.go:193      <- the AssignedTo line
    .../processtest/taskstoreconformance.go:185      <- checkTaskStoreConformance
```

A panic here is materially worse than an assertion failure: it is in **exported public API**, it takes down
the whole `go test` binary of a *consumer's* suite, and the message names neither the case nor the misuse.
Note that plan Task 2's own new assertion `assert.NotContainsf(output, "nil pointer dereference", "the guard
must report the misuse, not let a nil func value panic")` shows the bundle already holds this standard —
and then introduces a fresh nil-deref one task earlier.

**Concrete fix (do both — one is defence, one is detection):**

1. **Guard in the check.** Make the arm report the misuse instead of dereferencing:

```go
case inboxAssigned:
    if !assert.NotNilf(t, c.task.Claim,
        "case %q declares listedBy=inboxAssigned but carries no Claim: there is no claimant to ask AssignedTo about", c.name) {
        return
    }
    assigned, err := store.AssignedTo(ctx, c.task.Claim.Actor.ID)
```

2. **Pin the invariant in `TestTaskStoreConformanceCasesCoverBothSides`** (the case-set invariant test that
   already exists at `taskstoreconformance_internal_test.go:300`), inside its per-case loop:

```go
if c.listedBy == inboxAssigned {
    assert.NotNilf(t, c.task.Claim, "case %q declares inboxAssigned, so it MUST carry the Claim naming the claimant", c.name)
    assert.NotEmptyf(t, c.task.Claim.Actor.ID, "case %q declares inboxAssigned, but an empty actor id identifies no actor", c.name)
}
assert.Truef(t, c.legal || c.listedBy == inboxNone,
    "case %q is rejected, so listedBy=%v is a silent no-op — the check runs only on the legal leg", c.name, c.listedBy)
```

The empty-ID clause matters independently: `AssignedTo("")` is contractually a nil result
(`internal/persistence/store/humantask_store.go:227-233` and `MemTaskStore`), so `inboxAssigned` on the
kiosk shape would fail with a confusing "not contained" rather than the real reason.

---

## B4 — MAJOR — a `listedBy` declared on a rejected case is silently ignored, and `inboxNone` at iota 0 makes a forgotten declaration silent too

**Attacked:** plan Task 1 Step 3's enum, and Step 4's *"The three rejected cases are left alone — their zero
value is `inboxNone`, which is correct"*; spec §4.2's *"`inboxNone` means no positive expectation"*.

**4a — the declaration is a no-op on the rejected leg, with no diagnostic.** `checkTaskStoreConformance`
returns at `taskstoreconformance.go:172` on the `!c.legal` branch, before the new call site. Executed probe
(same tree, Steps 3–5 applied): a rejected case declaring `listedBy: inboxClaimable`

```
go test -count=1 -run '^TestZZAudBRejectedWithListedBy$' ./processtest/ -v
EXIT=0
    zzaudb_probe_test.go:34: failures=0 []
--- PASS
```

The declaration was accepted, compiled, and did nothing. A future contributor adding a rejected shape and
"declaring its inbox for symmetry" gets no warning at all.

**4b — `inboxNone` at iota 0 violates the project's always-on naming skill.**
`cc-skills-golang:golang-naming` (a **Required Go skill** in CLAUDE.md) SKILL.md:108 states:

> **Enum zero values:** Always place an explicit `Unknown`/`Invalid` sentinel at iota position 0. A
> `var s Status` silently becomes 0 — if that maps to a real state like `StatusReady`, code can behave as
> if a status was deliberately chosen when it wasn't.

and `references/types-errors.md:109`: *"Either place an explicit `Unknown` sentinel at iota 0, or start at
`iota + 1`. **This is not optional** — uninitialized enums are a common source of silent bugs."*

`inboxNone` is a **real state** ("this shape has no positive expectation"), not an unknown sentinel. So an
author who adds a legal case and forgets `listedBy` gets the *weakest* possible contract silently — which is
precisely the vacuity failure mode ADR-0184 exists to remove, reintroduced one layer up. The plan's own
comment even anticipates the misuse (*"not a way to opt a shape out"*) without doing anything to prevent it.
Reordering the iota later would silently re-map every un-declared case; the bundle's *"the zero value is
`inboxNone`, which is correct"* is load-bearing on source ordering nobody will re-check.

**Concrete fix — make the omission loud, not the value safe.** Cheapest change that satisfies both the skill
and the vacuity concern:

```go
const (
	// inboxUnset is the zero value and is NEVER correct on a legal shape: it means
	// the author did not decide. The case-set invariant test rejects it, so a new
	// legal case cannot silently inherit the weakest contract (ADR-0184).
	inboxUnset inboxExpectation = iota
	// inboxNone declares, deliberately, that neither query is contracted to return
	// this shape — the terminal and anonymous-kiosk controls.
	inboxNone
	inboxAssigned
	inboxClaimable
)
```

Then in `TestTaskStoreConformanceCasesCoverBothSides`:

```go
assert.NotEqualf(t, inboxUnset, c.listedBy,
    "case %q must DECIDE its inbox expectation; inboxNone is the explicit 'neither query returns it'", c.name)
```

and add `case inboxUnset, inboxNone:` to the switch in `checkTaskStoreAcceptedTaskIsListed`. This also forces
plan Step 4 to declare `listedBy` on **all eight** cases rather than relying on a zero value for three of
them — killing the "reordered iota" hazard at the same time. If the owner rejects the extra constant, the
invariant test from B3 (`c.legal || c.listedBy == inboxNone`) plus an explicit `listedBy: inboxNone` on the
three rejected cases is the minimum acceptable substitute, and the ADR must say why iota 0 is a real state.

---
## B5 — MAJOR — spec §4.3's "unaffected" list is right by accident: two of its five reasons are false

**Attacked:** spec §4.3, final paragraph — *"Unaffected, **verified by reading each stand-in's inbox
implementation**: `MemTaskStore` (conforms), `permissiveTaskStore` (…), `leakyRollbackTaskStore` (…),
`rejectingTaskStore` (**legal cases are already asserted `NotEmpty`, so a extra failure does not flip
it**), and `kioskHostileTaskStore` (**the kiosk case carries `inboxNone`**)."*

**Measured** (executed probe over every legal case × every stand-in, Steps 3–5 applied, then re-run with
`checkTaskStoreAcceptedTaskIsListed` commented out to get the baseline):

| stand-in | legal case | `listedBy` | baseline failures | after failures | Δ |
|---|---|---|---|---|---|
| `mem` | all 5 | — | 0 | 0 | 0 |
| `permissive` | all 5 | — | 0 | 0 | 0 |
| `leakyRollback` | all 5 | — | 0 | 0 | 0 |
| `rejecting` | all 5 | — | **1** | **1** | **0** |
| `writeOnly` | all 5 | — | **1** | **1** | **0** ← spec says 2 for the `listedBy` pair (see B1) |
| `kioskHostile` | kiosk | none | 1 | 1 | 0 |
| `kioskHostile` | other 4 | Claimable/Assigned/none | 0 | 0 | 0 |
| `inboxFailing` | `unclaimed_…_accepted` | ClaimableBy | **0** | **1** | **+1** |
| `inboxFailing` | `claimed_…_accepted` | AssignedTo | **0** | **1** | **+1** |
| `inboxFailing` | other 3 | none | 0 | 0 | 0 |

**Two reasons are false, though the conclusions hold:**

- **`rejectingTaskStore`** — the spec's reason is *"an extra failure does not flip `NotEmpty`"*. There **is
  no extra failure**. `checkTaskStoreConformance:175-177` returns immediately when the legal `Upsert` is
  refused, so `checkTaskStoreAcceptedTaskIsListed` is never reached; the count is 1 before and 1 after.
  The attack angle's suspicion about `(nil, nil)` inboxes is moot for the same reason — those methods are
  never called on the legal leg. The spec's stated mechanism ("verified by reading each stand-in's inbox
  implementation") is the wrong mechanism: what protects this store is the **control-flow early return**,
  which the spec never mentions. This is the same blind spot that produced B1.
- **`kioskHostileTaskStore`** — the spec's reason (*"the kiosk case carries `inboxNone`"*) covers exactly
  **one** of its five legal cases. Its assert branch demands `Empty(failures)` for the other four, two of
  which now carry a positive expectation; they stay green only because the embedded `MemTaskStore` answers
  both inboxes conformingly. If `MemTaskStore` had had a listing defect, `kioskHostileTaskStore` would have
  broken too and the spec's reasoning would not have predicted it.

**Concrete fix.** Rewrite spec §4.3's final paragraph to state the two real mechanisms and label the
evidence as executed, e.g.:

> Unaffected, **measured** (per legal case, before → after): `MemTaskStore`, `permissiveTaskStore`,
> `leakyRollbackTaskStore` 0→0 — all three answer both inboxes conformingly. `rejectingTaskStore` and
> `writeOnlyTaskStore` 1→1 — their legal leg **returns before the new check** (the Upsert refusal and the
> read-back miss respectively both `return` in `checkTaskStoreConformance`), so their inbox
> implementations are irrelevant. `kioskHostileTaskStore` kiosk 1→1, other four 0→0 — the kiosk control is
> refused at Upsert; the rest are carried by the embedded `MemTaskStore`'s conforming inboxes.

And add the early-return fact to spec §6 as its own numbered premise: *"§6.10 — a legal shape whose Upsert
or read-back fails returns before the inbox check (measured: `rejecting` 1→1, `writeOnly` 1→1)."* It is the
single fact that both B1 and B5 turn on and the bundle states it nowhere.

---
## B6 — MAJOR — 30 s × 31 serial sites in one package exceeds Go's default 10 m test timeout, converting a legible mass failure into a diagnostic-free panic

**Attacked:** ADR-0184 Decision 3 and Consequences — *"A genuinely broken `Eventually` site now takes 30 s to
fail instead of 1 s. This is paid only on red"*; spec §3.2's *"sizing this for the worst contended CI
machine costs nothing on the passing path"*; spec §7 Bad/accepted, same sentence. The bundle costs the
change **per site** and never costs it **per test binary**.

**The unstated assumption:** that failures are isolated. Every mechanism in scope is *shared* —
`GocronScheduler`, gocron's own goroutine, the elector — so the realistic red is **systemic**, and a
systemic red fails many sites in the same binary.

**Measured facts (executed):**

- `go help testflag` → *"-timeout d … **The default is 10 minutes (10m)**."*
- `.github/workflows/ci.yml:33` → `go test -race -coverprofile=cover.out ./...` — **no `-timeout` flag**,
  so CI runs at the 10 m default. `scripts/coverage.sh:23` is identical.
- `scheduler/internal/gocron` carries **31** of the 40 `Eventually` sites (re-derived below, B7).
- **None** of the high-density test functions calls `t.Parallel()` in its subtests, so the sites are
  **serial**:

  ```
  scheduler/internal/gocron/trigger_test.go:23    TestGocronNativeTriggers          Eventually=8   t.Parallel_calls=0
  scheduler/internal/gocron/trigger_test.go:262   TestGocronScheduleJobTriggers     Eventually=10  t.Parallel_calls=0
  scheduler/internal/gocron/job_schedule_test.go  TestGocronScheduler_ScheduleJob   Eventually=5   t.Parallel_calls=0
  scheduler/internal/gocron/monitor_test.go       TestGocronScheduler_MonitorStatus Eventually=4   t.Parallel_calls=0
  ```
- Current whole-package runtime under `-race`: `ok … scheduler/internal/gocron 3.292s`.

**Arithmetic:** a systemic break in `scheduler/internal/gocron` costs **31 × 30 s = 930 s = 15.5 min**
against a **600 s** binary timeout. The binary is killed with `panic: test timed out after 10m0s` plus a
full goroutine dump — which prints **no testify assertion messages at all**. Today the same break costs
31 × 1–3 s ≈ 45 s and prints 31 legible failures naming each broken site. `TestGocronScheduleJobTriggers`
alone goes from ~10 s to **300 s**.

So the change does not only make red slower; past the timeout it makes red **undiagnosable**, and it does
so exactly in CI, exactly on the contended machine the budget was raised for. `require.Eventually` is
`FailNow`-class, but that only ends the *subtest* — the table's remaining rows each pay their own 30 s.

**Concrete fix (any one, but the ADR must state the choice and the arithmetic):**

1. **Size the budget to the binary, not the site.** 30 s × 31 is the number that matters. `10 * time.Second`
   is still a ~1000× margin over the measured 0.01 s fire time (spec §6.7) and caps the gocron package at
   310 s, inside the default timeout. State the derivation in the ADR: *"budget × the densest package's
   site count must stay under `go test`'s 600 s default."*
2. **Or raise the timeout with the budget**, in the same commit: add `-timeout 30m` to
   `.github/workflows/ci.yml:33` and `scripts/coverage.sh:23`, and to the plan's Verification commands.
   Leaving CI at the default while raising the budgets 30× is the divergence.
3. Either way, **add the per-binary cost to ADR-0184 Consequences** — replace *"takes 30 s to fail instead
   of 1 s"* with the measured worst case per package (`scheduler/internal/gocron`: 31 sites, 930 s vs a
   600 s default timeout), and to spec §7.

**Related, cheap:** the four const files are identical apart from `package`, so a per-package budget is
*already* the natural place to encode "this package has 31 sites, so its ceiling is lower". The bundle
chose per-package consts for import hygiene and then used the same value in all four — losing the only
benefit that shape offers.

---
