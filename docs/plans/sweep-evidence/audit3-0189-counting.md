# Audit 3 — COUNTING lens — ADR-0189 (re-cut to 1 decision)
Worktree: wt3-counting @ 3e96e836
Step 0: PASSED — all four bundle documents present.

### F1 — §2.6's 48-line member set OMITS the two live tests that ADR-0189's OWN §4 residual 8 says it breaks — the claim-route malformed-body pins — [CRITICAL]

**Bundle text attacked:**
spec §2.6 *"#### Total: **48 lines · 13 files · 6 packages**"*, whose `stdlib/coverage_test.go`
row is `92, 126` and whose `gin/gin_coverage_test.go` row is `192, 218, 244`.
plan Task 8 *"`stdlib` — 5 runtime pins: `errors_test.go:155,187` · `stdlib_test.go:471` ·
`coverage_test.go:92,126`"* … *"Each: `go test -count=1 ./transport/http/<pkg>/... ⇒ EXIT=0`"*.
plan Task 9 *"`gin` — 7 runtime pins: `gin_test.go:413,421,443,453` ·
`gin_coverage_test.go:192,218,244`"*.

Meanwhile spec §4 residual 8 states the behaviour change explicitly:
> *"the optional claim decoder swallows every decode error, so a **malformed** claim answers
> **401** rather than 400 — a real change, stated rather than glossed."*
and §5 row 14 prescribes a NEW test for it (*"a malformed claim body ⇒ 401"*).

**The net the author used:** two nets — (a) the two-change compile ablation, (b) a
`"actor"`/`"by"` JSON-key grep. Both are nets for the **DTO removal**. Neither is a net for
the **optional-body decision (§3.6)**, which is a third breaking change with its own blast
radius. A test that posts a malformed body carries no `"actor"` key and still compiles, so it
is invisible to both — the exact failure class §2.6 congratulates itself for having fixed
(*"invisible to **both** nets"*, said there of `service/instance_test.go` only).

**The net that finds more:** enumerate task-route call sites, not actor-bearing ones.
```
$ grep -rn --include='*_test.go' -e '/claim' -e '/complete' -e '/reassign' transport/ | wc -l
37          # vs 23 in the runtime member set
$ grep -rn --include='*_test.go' -i 'badjson' transport/http/{stdlib,gin,fiber}/
```

**Observed:**
```
transport/http/stdlib/coverage_test.go:136:func TestTaskRoutes_BadJSON(t *testing.T) {
transport/http/stdlib/coverage_test.go:148:  req, err := http.NewRequest(http.MethodPost, "/tasks/"+taskID+"/claim", errReader{})
transport/http/stdlib/coverage_test.go:158:  if rr.Code != http.StatusBadRequest {   // want 400 for bad JSON

transport/http/gin/gin_coverage_test.go:173:func TestTaskRoutes_Claim_BadJSON(t *testing.T) {
transport/http/gin/gin_coverage_test.go:183:  resp := post(t, srv, "/tasks/tok/claim", "not-json")
transport/http/gin/gin_coverage_test.go:184:  if resp.StatusCode != http.StatusBadRequest {  // want 400
```
Both are live and green at the bundle base:
```
$ go test -count=1 -v -run 'TestTaskRoutes_BadJSON$' ./transport/http/stdlib/   ; EXIT=0
--- PASS: TestTaskRoutes_BadJSON (0.00s)
$ go test -count=1 -v -run 'TestTaskRoutes_Claim_BadJSON$' ./transport/http/gin/ ; EXIT=0
--- PASS: TestTaskRoutes_Claim_BadJSON (0.00s)
```
`fiber` has **no** claim-badjson test (checked: no `not-json` fixture in `transport/http/fiber/`),
so the count is exactly two, not three.

Note the asymmetry that makes this a *claim-route-only* member: `CompleteInput`/`ReassignInput`
keep the **required** decoder (§3.6 scopes the optional decode to the claim route alone), so
`TestTaskRoutes_Complete_BadJSON` and `TestTaskRoutes_Reassign_BadJSON` in **both** files keep
returning 400 and are correctly non-members. Only the claim halves flip.

**Correct value:** the runtime set is **25 lines**, and the total is **50 lines · 13 files ·
6 packages** (no new file, no new package — both live in files already in the set, which is
precisely why the file/package totals do not move and the omission is silent).
Per-task: Task 8 is **6** stdlib pins, not 5; Task 9 is **8** gin pins, not 7. Tasks 8–10 own
**19**, not 17. Task 5 = 28, Tasks 8–10 = 19, Task 11 = 3 ⇒ **50**.

**Concrete fix:**
1. Add to §2.6's runtime table: `stdlib/coverage_test.go` → `92, 126, **148**`;
   `gin/gin_coverage_test.go` → `**184**, 192, 218, 244`; retotal to 25 / 50.
2. Add a fourth §2.6 sub-net paragraph naming the third breaking change explicitly —
   *"the optional claim decoder (§3.6) breaks tests that assert 400 on a malformed CLAIM body;
   neither the ablation nor the `"actor"` grep sees them"* — and state the net
   (`grep -i badjson`, plus task-route call-site enumeration).
3. Plan Task 8: add a step rewriting `TestTaskRoutes_BadJSON` to assert **401** with a comment
   naming §4 residual 8, and note its sibling Complete/Reassign BadJSON tests stay **400** —
   otherwise an agent "fixing" the red will flip all three and silently delete the
   required-decode coverage on complete/reassign.
4. Plan Task 9: same for `TestTaskRoutes_Claim_BadJSON`.
5. Task 6 (the optional-body task) currently only prescribes the *new* bodyless⇒200 test. It
   must also carry "and the pre-existing malformed-claim pins now expect 401" — otherwise
   Task 6 leaves `stdlib` and `gin` RED and the plan's own "Tasks 1–4 and 7 are additive; Task 6
   follows Task 5" ordering note gives no warning that 6 breaks two more tests.
6. Spec §5 row 14 should cite the two EXISTING tests being rewritten rather than reading as a
   purely new test — a rewritten pin and a new pin are different work.

**Why this is the round-1 error repeating, not a new one:** §2.6's headline lesson is
*"⭐ A count is re-derived only when its MEMBER SET is re-derived."* The member set here WAS
re-derived — but only for the two changes the author had nets for. The third change (§3.6's
optional body) was added to the design and the member set was never re-derived against it.
That is round 2's Critical restated: *a count is a derived quantity of every decision in the
set.* The removal grid re-derived the count against the **removed** decisions (C, D, G) and
never against the **surviving** one that changed (§3.6 optional body).

### F2 — the removal grid's load-bearing justification, *"re-verified clean by round 2's counting lens"*, is FALSE about the very lens it cites — round 2's counting lens filed §2.6 as an accepted MAJOR — [CRITICAL]

**Bundle text attacked:** `audit2-0189-removal-grid.md` §"Blast radius", lines 96–97:
> *"⇒ the member set reverts to spec §2.6's **48 lines / 13 files / 6 packages**, which was derived
> **exactly for this scope** and **re-verified clean by round 2's counting lens**."*

This sentence is the entire warrant for the re-cut's central quantitative claim. The next line
(*"⚠ This is a claim, and it must be re-executed after the re-cut, not assumed"*) hedges the
*reversion*, but not the *cleanliness* — and the cleanliness is what makes "revert to 48" mean
"revert to a correct 48".

**The net the author used:** memory of round 2's counting report's headline (F1, the 186-assertion
Critical), restated as if it were the whole report. Classic inherited-claim restatement: the hedge
is stripped and nobody re-reads the source.

**The net that finds more:** read what round 2's counting lens actually filed against §2.6, and
what the adjudication did with it.

**Observed:** `audit2-0189-counting.md` contains **two** findings against §2.6, not zero:
```
line  13: ### F1 — §2.6's member set enumerates ONLY the round-1 change … — [CRITICAL]
line 105: ### F2 — §2.6's runtime-only sub-header CONTRADICTS ITS OWN TABLE:
          "7 files / 4 packages" over a table listing 8 files in 5 packages — [MAJOR]
```
and `audit2-0189-adjudication.md:180-182` **ACCEPTED** it:
> *"**§2.6 internal contradiction:** the runtime sub-header says *7 files / 4 packages* over a
> table listing **8 files / 5 packages**."*

The adjudication's own lens table (`audit2-0189-adjudication.md:13`) records
`| counting | 11 (+2 confirmations) | 2 | 4 | 5 |` — i.e. the counting lens filed 11 findings, of
which 2 were Critical. "Clean" is not a description of that report under any reading.

**And the accepted fix was never folded.** At the round-3 bundle commit `3e96e836`:
```
$ grep -n "Compile-breaking\|Runtime-only\|Stale documentation\|#### Total" \
        docs/specs/2026-08-25-request-actor-identity.md
223:#### Compile-breaking — 23 lines / 5 files / 4 packages
235:#### Runtime-only — 23 lines / 7 files / 4 packages      <-- table below it has 8 rows
250:#### Stale documentation — 2 lines / 1 file / 1 package
254:#### Total: **48 lines · 13 files · 6 packages**
```
Re-derived from the table's own rows (verified against the live tree by the grep in F1):
```
httpcore/dto_test.go 5 · gin/gin_test.go 4 · gin/gin_coverage_test.go 3 · fiber/fiber_test.go 5
stdlib/errors_test.go 2 · stdlib/stdlib_test.go 1 · stdlib/coverage_test.go 2 · parity/parity_test.go 1
⇒ 8 files; packages {httpcore, gin, fiber, stdlib, parity} = 5.
```

**Correct value:** the runtime sub-header is **23 lines / 8 files / 5 packages** (or, with F1's
correction folded, **25 lines / 8 files / 5 packages**). "7 / 4" is defensible under no single
convention: the *new-members* reading rescues the 7 (8 minus the already-counted `dto_test.go`)
but makes the 4 worse, since the only new package is `parity` ⇒ 1 — and the 6-package grand total
requires exactly that new-members reading (4 + 1 + 1 = 6). The compile sub-header uses raw counts.
Two tables, two silently different conventions, one of them internally impossible.

**Concrete fix:**
1. Correct the sub-header to raw counts and state the dedup on the total line, exactly as round 2
   prescribed: *"Total: 48 (→50) lines · 13 distinct files (`dto_test.go` appears in both tables)
   · 6 packages."*
2. Strike *"re-verified clean by round 2's counting lens"* from the removal grid. Replace with the
   truth: *"round 2's counting lens filed two findings against §2.6 — F1 (the scope Critical, which
   the cut dissolves) and F2 (the sub-header contradiction, ACCEPTED and still unfolded at
   `3e96e836`). The 48 is the correct TOTAL for the round-1 scope; its sub-headers are not."*
3. **Process fix, and it is the transferable one:** the re-cut re-derived the count against the
   REMOVED decisions and never swept the round-2 adjudication for accepted-but-unfolded items. Add
   a step to the plan's Task 13 documentation sweep: `grep` the previous round's adjudication for
   every accepted finding and confirm each is folded or explicitly re-adjudicated as dissolved by
   the cut. F2 here dissolves for **no** reason — it is orthogonal to C, D and G.

---
### F3 — the author interaction grid, still shipped in the bundle and still cited by spec §6 as an INPUT, states the blast radius as **"37 lines / 6 packages"** — the figure the spec explicitly calls wrong — [MINOR]

**Bundle text attacked:** `audit-0189-author-interaction-grid.md:20`:
> `| **G** | blast radius corrected to **37 lines / 6 packages**; counting method changed to member-set | audit A2 |`
vs spec §2.6: *"⚠ The audit's own proposed figure of **37** is also wrong — it unions the two *pin*
nets and omits the 9 production call sites and the 2 `service` comments."*

**Observed:** round 2's counting lens filed this as its F3 (MAJOR); the adjudication accepted it
(`audit2-0189-adjudication.md:181`: *"The author grid still states **37 lines** — the exact figure
the spec and ADR both call wrong"*); at `3e96e836` the grid still says 37.

**Correct value:** 48 → 50 with F1 folded.

**Concrete fix:** annotate the grid row in place (do not rewrite history in an evidence file):
*"⚠ SUPERSEDED — 37 was refuted by spec §2.6's member-set derivation; the figure is 48, and
round 3 raises it to 50."* Two consecutive rounds have now filed this; a third occurrence in a
document spec §6 tells the next auditor to treat as an input is a citation the bundle keeps
feeding to its own readers.

### F4 — the plan's self-review table still claims the two REMOVED decisions are implemented, mapping them to tasks that exist but do something else — the cut renumbered the tasks and never re-derived the table — [CRITICAL]

**Bundle text attacked:** plan, "Self-review against the spec", lines 504–505:
```
| §3.6 group authentication, Health exempt, placement asymmetry | 8, 9–11 |
| §3.7 opt-in admin role gate                                    | 8, 9–11 |
```

**The net the author used:** the "Changed by the round-2 re-cut" paragraph immediately below the
table (*"Tasks 8–11 of the round-2 plan … are **deleted**"*) — i.e. the author recorded the change
in prose and did not re-derive the table the prose contradicts. Exactly the shape the removal grid
warns about at §3 × C: *"⛔ The spec, ADR and plan each carry such sentences; they are now false
and must be hunted individually, **not assumed corrected by the section delete**."* The section was
deleted; the table was assumed corrected.

**The net that finds more:** resolve every `§N.M` in the self-review table against the spec's
CURRENT section headers, and every task number against the plan's CURRENT task headers.
```
$ grep -n '^### 3\.' docs/specs/2026-08-25-request-actor-identity.md
453:### 3.6 The claim route accepts an ABSENT body; the ordering residual is stated
467:### 3.7 Examples and documentation
$ grep -n '^### Task\|^#### Task' docs/plans/2026-08-25-request-actor-identity.md
371:#### Task 8: `stdlib` — 5 runtime pins
383:#### Task 9: `gin` — 7 runtime pins
396:#### Task 10: `fiber` — 5 runtime pins
407:### Task 11: `parity`, the `service` comments, and ADR-0147
```

**Observed:** BOTH rows are doubly dangling.
- Their **left column** cites deleted content: "group authentication, Health exempt, placement
  asymmetry" and "opt-in admin role gate" are removals **C**, **G** and **D** respectively, split
  to ADR-0190. §3.6 today is the optional claim body; §3.7 is examples and documentation. So both
  rows name a §-anchor that resolves to a section saying something entirely different — the
  precise "anchor now points at a section that was deleted" case, and the FIRST anchor failure in
  three rounds after 54 clean ones.
- Their **right column** is *worse than dangling*: `8, 9–11` resolves to tasks that **still
  exist**, so nothing errors. An implementer reading the self-review table — the artifact whose
  entire job is "is every spec decision owned by a task?" — is told route-group authentication and
  the admin role gate ARE owned, by Tasks 8–11. They are not: Tasks 8/9/10 delete `"actor"` keys
  from three adapter suites and Task 11 fixes `parity` plus two comments. A renumbering that leaves
  a stale reference pointing at a *live* number is invisible to every check except reading both
  columns, which is why it survived.

Two further stale cells in the same table, from the same edit:
- `| §3.5 marshalability pre-check | 4 |` — §3.5 is now *"`Attributes` flow behind a **ROUND-TRIP
  guard**"*, and the spec explicitly refutes the marshal-only form (*"Round 2 prescribed
  `json.Marshal` alone … that **fails to prevent the brick it exists to prevent**"*). The
  self-review row still carries round 2's refuted label for the very row that changed.
- `**§5 row 7** (attributes reach `service.ClaimTaskRequest`)` in the Gaps paragraph is **row 8**.
  Re-derived from §5's own table: row 7 is *"the two new arms co-match each other"*; row 8 is
  *"attributes reach `service.ClaimTaskRequest.Actor.Attributes`"*. Off-by-one, and it points the
  reader at the one row whose whole point is a DIFFERENT missing test.

**Correct value:** delete the two removed-decision rows outright and add a single explicit
**out-of-scope** row so the absence is asserted rather than silent:
```
| §3.6/§3.7 route-group authentication + admin role gate | ⛔ REMOVED → ADR-0190; NO task |
```
Retarget `§3.5` to *"ROUND-TRIP guard + size bound"*, and renumber the Gaps reference to §5 row 8.

**Concrete fix:** beyond the four edits, add to the plan's Global Constraints the check the cut
skipped: *"after any scope change, re-resolve every `§N.M` in the self-review table against the
spec's current headers and every task number against the current task list — a stale reference to
a live task number is silent."* This is the counting-lens analogue of the removal grid's own rule,
applied to the one table nobody re-derived.

**Local defect or inter-fix hole?** **Inter-fix.** The removal of C/D/G and the renumbering of the
task list are two fixes; each is individually correct; the self-review table is the artifact that
sits on their intersection and neither fix owned it.

### F5 — the trend the PRE-REGISTERED DECISION RULE is calibrated on, "8.25 → 3.50 → 1.75 → 3.75 across this lineage", SPLICES two ADR-0186 data points onto two ADR-0189 ones — 8.25 and 3.50 are a different delivery, and all three ADR-0185 rounds (incl. one at 5.50) are missing — [CRITICAL]

**Bundle text attacked:** `audit2-0189-removal-grid.md:125-127`, the paragraph that sets the
threshold the round-3 verdict is decided by:
> *"**The metric is Criticals per lens.** … Criticals per lens has moved **8.25 → 3.50 → 1.75 →
> 3.75 across this lineage**, and the last step up coincided with a 2 → 9 decision widening that
> three lenses named independently."*

and its inherited source, `audit-0189-adjudication.md:28`:
> *"**The number that moved is Criticals per lens: 8.25 → 3.50 → 1.75.** That is the **fourth
> consecutive fall** and the first round below 2."*

**The net the author used:** restating a figure from the round-1 adjudication, which itself
restated the meta-analysis's headline. No re-derivation at either hop. This is the exact failure
CLAUDE.md names — *"Re-verify claims you inherit before restating them. Restating strips the hedge"*
— applied to the one number a pre-registered decision rule is measured against.

**The net that finds more:** open the source table and read what 8.25 and 3.50 are rows OF.

**Observed** — `docs/plans/sweep-evidence/meta-analysis-audit-finding-rate.md:100-110`:
```
| # | round          | lenses | findings | findings/lens | Critical | Crit/lens |
| 3 | audit-0186     |   4    |    63    |    15.75      |    33    |   8.25    |
| 7 | audit5-0186    |   4    |    57    |    14.25      |    14    |   3.50    |
|10 | audit-0185core |   4    |    58    |    14.50      |    22    |   5.50    |
```
and `:162`:
> *"Criticals per lens fell from **8.25 (round 3)** to **3.50 (round 7)** — a 2.4× improvement"*
and `:719`:
> *"Criticals per lens fell **8.25 → 3.50** across the **ADR-0186 scope cut**"*

**8.25 and 3.50 are ADR-0186's rounds 3 and 7 — a different delivery, and not adjacent rounds
even within it** (rounds 4, 5 and 6 sit between them at 7.00, 5.00 and 6.00). They are the two
endpoints the meta-analysis selected to demonstrate ADR-0186's 2.4× improvement. The removal grid
concatenates them with ADR-0189's own 1.75 and 3.75 and calls the result *"this lineage"*.

**And the actual lineage is missing entirely.** Spec §1 defines it: ADR-0185 failed
*"2026-08-20 five decisions; 2026-08-21 five decisions revised; 2026-08-23 three decisions"* —
**three** rounds — then ADR-0189 rounds 1 and 2. That is **five** rounds; the series lists **four**
points, none of which is an ADR-0185 round. The one ADR-0185 round that IS measured in the
meta-analysis is row 10, `audit-0185core` at **5.50 Criticals/lens** — corroborated by spec §1's
own *"22 raw Criticals"* (22 ÷ 4 = 5.50). Inserted where it belongs the series becomes
`… → 5.50 → 1.75 → 3.75`, which is not a monotone fall and does not support *"the fourth
consecutive fall"*.

**That inherited sentence is independently impossible.** Round 1's adjudication calls
`8.25 → 3.50 → 1.75` — **three points, two transitions** — *"the fourth consecutive fall."*

**What I DID re-derive, and it holds:** the two ADR-0189 anchors are correct, and they are RAW
filed Criticals, counted from the lens reports themselves, not adjudicated-accepted ones:
```
$ for f in audit-0189-{counting,execution,failure-modes,interaction}.md; do …severity histogram… ; done
round 1: counting 3C/5M/4Mi(12) · execution 2C/11M/1Mi(14) · failure-modes 1C/8M/2Mi(11) · interaction 1C/9M/2Mi(12)
         ⇒ 7 Criticals / 4 lenses = 1.75      ✓ (adjudication's own table: 3+2+1+1 = 7)
round 2: counting 2C/4M/5Mi(11) · execution 2C/9M/3Mi(14) · failure-modes 4C/10M/2Mi(16) · interaction 7C/8M/2Mi(17)
         ⇒ 58 findings, 15 Criticals / 4 lenses = 3.75   ✓ exact match to audit2-0189-adjudication.md:13
```
(`audit2-0189-execution.md` contains a NUL byte and `file(1)` reports it as `data`; plain `grep`
silently returns nothing on it — use `grep -a`. Worth knowing before a future sweep concludes that
file has no findings.)

**Correct value:** the within-lineage series is `ADR-0185 R1 ? → R2 ? → R3 5.50 → ADR-0189 R1 1.75
→ R2 3.75`. Only the last two points are established; two are unmeasured; none of 8.25 or 3.50
belongs to it.

**Why this is CRITICAL rather than a citation nit:** the decision rule's three thresholds
(<1.5 / 1.5–3 / ≥3 Criticals per lens) are presented as calibrated by that series — *"recorded
BEFORE the results so it cannot be moved after the numbers arrive."* A rule whose calibration set
is half a different bundle's numbers is not pre-registered in any sense that constrains the author;
it is a threshold chosen against an unrelated distribution. And the rule's third row escalates to
the owner, so the arithmetic decides whether this delivery ships.

⚠ Note what this does NOT overturn: the **1.75 → 3.75 step is real, within-lineage, and
independently re-derived above.** The scope-widening inference the re-cut acted on stands. What
does not stand is the "fourth consecutive fall" framing that makes 3.75 look like a departure from
a long improving trend rather than one step in a two-point series.

**Concrete fix:**
1. Rewrite the paragraph: *"Criticals per lens for THIS bundle: round 1 = 1.75, round 2 = 3.75
   (re-derived from the lens reports, not restated). The comparators 8.25 → 3.50 come from
   **ADR-0186's** rounds 3 and 7 (`meta-analysis-audit-finding-rate.md:105,109`) and are a
   cross-delivery reference point, not this lineage. The one measured ADR-0185 round is
   `audit-0185core` at 5.50."*
2. Delete *"the fourth consecutive fall"* from the round-1 adjudication, or correct it in place
   with a dated annotation — it is a false claim in an evidence file that two later documents have
   already inherited from.
3. Re-derive the thresholds against whatever series is actually used, and say which rounds are in
   it. If the intent is "compare against the repo's 4-lens population", the population is the ten
   rounds in the meta-analysis (Crit/lens 4.00, 4.33, 8.25, 7.00, 5.00, 6.00, 3.50, 4.25, 5.50,
   5.50 — mean ≈ 5.33), against which **both** 1.75 and 3.75 are already below average and a
   "< 1.5" threshold has been met by no round in the repo's recorded history. That is a materially
   different calibration and the owner should be told which one is being applied.
4. State explicitly that the metric counts **raw filed** Criticals. The rule never says, and the
   round-2 adjudication accepted far fewer than 15 — under an "accepted" reading the same round
   scores differently and the thresholds mean something else.

**Local defect or inter-fix hole?** **Local**, but it is the one Critical that acts on the *audit
process* rather than the code: it is the rule that decides what happens to every other finding here.

### F6 — §2.4's option-alias enumeration is STILL wrong about fiber, three rounds running, in the paragraph whose job is to warn against exactly that — and round 2's adjudication already recorded it as "never adjudicated and never folded" — [MINOR]

**Bundle text attacked:** spec §2.4:
> *"⚠ The adapter option-alias sets are **already uneven**: `stdlib` and `gin` export
> `WithBasePath`/`WithMaxBodyBytes`/`WithBodyReadTimeout` (**gin adds `WithMiddleware`**), while
> **`fiber` has no `WithBodyReadTimeout`** … **Do not infer the new alias set from any one
> adapter's file.**"*
restated in plan Task 7: *"⚠ Do not infer the set from one file — `fiber` has no
`WithBodyReadTimeout` and that asymmetry is deliberate."*

**The net the author used:** describe two adapters and subtract for the third.

**The net that finds more:** enumerate all three.
```
$ for p in stdlib gin fiber; do echo "=== $p ==="; \
  grep -rhoE '^func With[A-Za-z]+' transport/http/$p/*.go | grep -v _test | sort -u; done
=== stdlib ===  WithBasePath  WithBodyReadTimeout  WithMaxBodyBytes                  (3)
=== gin ===     WithBasePath  WithBodyReadTimeout  WithMaxBodyBytes  WithMiddleware  (4)
=== fiber ===   WithBasePath  WithMaxBodyBytes     WithMiddleware                    (3)
```

**Correct value:** `WithMiddleware` is exported by **gin AND fiber**. There are **two**
asymmetries, not one: fiber lacks `WithBodyReadTimeout`, and **stdlib lacks `WithMiddleware`**.
The sentence implies fiber's set is `{BasePath, MaxBodyBytes}`; it is
`{BasePath, MaxBodyBytes, Middleware}`.

**Provenance — this is its third round.** Round 1's counting lens filed it as F9. Round 2's
adjudication recorded (`audit2-0189-adjudication.md:185-186`):
> *"**Round-1 counting F9 was never adjudicated and never folded** — the false sentence about
> fiber's option set still stands, inside the paragraph warning against exactly that error."*
It still stands at `3e96e836`. Together with F2 (round-2 counting F2, accepted, unfolded) and F3
(round-2 counting F3, accepted, unfolded) that is **three accepted counting findings the re-cut
carried forward unrepaired** — the pattern being that counting-lens findings about *the bundle's
own citations* are the ones that fall through adjudication.

**Concrete fix:** replace the prose with the three-row table above, in both §2.4 and Task 7. And
add the sweep from F2's fix #3 to the plan: before dispatch, `grep` the previous rounds'
adjudications for accepted findings and confirm each is folded.

**Does it move the "six aliases" count?** No — `WithRequestActor` + `WithRequestActorTimeout` × 3
adapters = **6**, and I confirm the count independently: `R` appears only in the generic result
type of `httpcore.WithBasePath`-style options, so each adapter needs its own concrete-`R` alias
for both new options. Task 7's "six, two per adapter" is **correct** and correctly notes that
round 2's version said six while producing nine (it also carried `WithAdminRoles`, removed with D).

---
### F7 — "5 of 6 attribute shapes still ALLOW" is CORRECT — but the six are named in NO document, including the round-2 report it is inherited from, and it is presented as "Measured" by an author who did not measure it — [MINOR / CONFIRMATION]

**Bundle text attacked:** three restatements, none carrying the set —
- spec §3.5: *"measured, the deny-list predicate still **ALLOWs in 5 of 6 attribute shapes**"*
- spec §4 residual 6: *"5 of 6 shapes still ALLOW (§3.5)"*
- ADR:285: *"Measured: the deny-list predicate still ALLOWs in **5 of 6** attribute shapes"*

**The net the author used:** inherited verbatim from `audit2-0189-adjudication.md:162`, itself from
round-2 execution F3. Neither source enumerates six shapes: F3's prose names **three**
(*"absent, empty, or missing the key"*) and the cross-product it cites (F2) is
`{dropped, blocked, active} × {nil, empty, blocked} vars` — a 3-shape actor axis. **The denominator
6 appears in no pasted output anywhere in the lineage.** CLAUDE.md: *"Prefer naming a closed set
over counting it."*

**The net that finds more:** construct the six and run them.

**Observed** (`authz/zzprobe_shapes_test.go`, throwaway, deleted after the run):
```
$ go test -count=1 -v -run '^TestZZProbeAttributeShapes$' ./authz/   ; EXIT=0
PROBE deny-list 1 nil map                          -> ALLOW
PROBE deny-list 2 empty map                        -> ALLOW
PROBE deny-list 3 key absent, other keys present   -> ALLOW
PROBE deny-list 4 key present, empty string        -> ALLOW
PROBE deny-list 5 key present, non-blocked value   -> ALLOW
PROBE deny-list 6 key present, blocked             -> DENY (workflow-authz: not authorized)
PROBE TOTAL ALLOW = 5 of 6
--- PASS: TestZZProbeAttributeShapes (0.00s)
```
(predicate `actor.Attributes.status != "blocked"` in `AuthzSpec.Attribute`; actor carries
`ID: "alice"` so §3.3's new no-dimensions refusal does not eliminate any row.)

**Correct value:** **5 of 6 — CONFIRMED**, and the six are: nil map · empty map · key absent ·
key present but empty · key present non-matching · key present matching. Note this also confirms
the count SURVIVES §3.3's decision change (the dimension rule): every shape stays reachable via a
non-empty `ID`. That was not obvious in advance and is the kind of check the round-2 lesson
demands, since §3.3 is a survivor decision that CHANGED in the re-cut (spec §6 item 6 lists it).

**Concrete fix:** paste the six-row table into §3.5 under the figure, and relabel the two
restatements as *"re-derived (§3.5)"* rather than *"Measured"* wherever the author did not run
it — or, now that it has been run, cite this table.

---
### F8 — the plan's citation for the resolver-timeout caveat, `runtime/task/service.go:154`, points 15 lines past the sentence — the FIRST broken `file.go:NNN` anchor in three rounds, and it broke in the edit that RE-cited it — [MINOR]

**Bundle text attacked:** plan Task 3:
> *"Measured in round 2: a ctx-ignoring resolver ran **1.5 s against a 200 ms bound and returned
> `err == nil`**. The precedent carries that caveat in its own godoc
> (`runtime/task/service.go:154`) and round 2 **stripped the hedge when restating it**."*

**Observed:**
```
$ sed -n '154p' runtime/task/service.go
// and optional [TaskServiceOption] values. The clock defaults to [clockwork.NewRealClock];
```
That is `NewTaskService`'s godoc, unrelated. The caveat is at **`runtime/task/service.go:139-141`**:
```
139: // The resolver's Candidates must honour ctx cancellation for the timeout to take
140: // effect; a timed-out resolution returns an error and no trigger, so the task's
141: // stored candidate list is left untouched.
```
`WithCandidateResolveTimeout` itself is at `:147`, its godoc opens at `:134`.

**The regression is in the re-cite.** Round 2's execution lens cited the *other* precedent —
`runtime/processdriver_options.go:79-81` — which resolves exactly:
```
$ sed -n '78,80p' runtime/processdriver_options.go
// The resolver's Candidates must honour ctx cancellation for the timeout to take
// effect; a timed-out resolution fails the step before anything is committed, so
// no instance is left parked on a task the task store never received.
```
So the bundle moved the citation from a correct anchor in one file to a wrong anchor in another
while restating the finding — and the sentence's whole point is that round 2 *"stripped the hedge
when restating it"*. The re-cite strips the reader's ability to check the hedge.

**Correct value:** `runtime/task/service.go:139-141` (or the original
`runtime/processdriver_options.go:78-80`). Both files carry the caveat; cite whichever, and note
that **two** precedents carry it, not one — that strengthens the argument the sentence is making.

**Anchor sweep result, for the record:** I resolved every `file:NNN` anchor I could reach across
the four bundle documents — §2.1's three endpoint sites, §2.4's nine adapter sites, all 46 §2.6
member lines, `httpcore/seam.go`'s eight `CustomizeConfig` fields, `humantask/validate.go:24`,
`humantask/validate_test.go:45-47` (fixture confirmed NON-vacuous: it really declares
`Actor{Roles: ["kiosk"]}` with an empty ID), `internal/atrest/classification.go:203`,
`internal/persistence/store/humantask_store.go:161`, `runtime/processdriver.go:548`,
`stdlib/body.go:143`, `body.go:156`, `groups.go:234`, `service/instance_test.go:1090,1128`,
`stdlib/errors_test.go:158,190`, `gin/gin_coverage_test.go:244`, `CHANGELOG.md:20`,
`docs/adr/0146-usertask-outcomes-and-completion-api.md:12`,
`examples/{production:264, sqlite:278, mysql:262}` and `production_wiring/main.go:274`
(`AdminRoutes{}.Customize(adminMux, …)` — the plan's "do NOT touch" target, correct).
**All resolve and say what the bundle says, except `runtime/task/service.go:154`.** The record is
therefore 3 rounds / ~75 anchors / **1 miss**, not the clean sweep the prior rounds established —
worth saying plainly so the next round does not inherit "anchor hygiene is perfect here".

### F9 — the removal grid's "the 186 were ALL C's" is TRUE for 6 of the 7 unnamed files and CONDITIONAL for the 7th — `fiber/bodylimit_test.go` carries three TASK-route rows, so its reversion depends on §3.6's untested ordering residual, and the file is still named nowhere — [MAJOR]

**Bundle text attacked:** `audit2-0189-removal-grid.md`, "Blast radius":
> *"Derived: **the 186 failing assertions were ALL C's** — they are instance/message/admin route
> calls that would newly 401. Removing C removes every one."*

Two of its three clauses are checkable and one is not stated as the conditional it is.

**The net the author used:** the category label *"instance/message/admin route calls"*, applied to
the 13-file list without re-opening the files.

**The net that finds more:** ask of each of the seven previously-unnamed files whether it touches a
**task** route — the surface round 3 still changes.
```
$ for f in stdlib/maxbody_test.go stdlib/bodydeadline_test.go gin/gin_admin_test.go \
           gin/gin_admin_errors_test.go gin/gin_bodycap_test.go gin/gin_bodydeadline_test.go \
           fiber/bodylimit_test.go; do
    printf "%-34s tasks=%s actor-keys=%s\n" $f \
      $(grep -c 'tasks' transport/http/$f) \
      $(grep -c -e '"actor"' -e '"by"' transport/http/$f); done
```
**Observed:**
```
stdlib/maxbody_test.go             tasks=0  actor-keys=0
stdlib/bodydeadline_test.go        tasks=0  actor-keys=0
gin/gin_admin_test.go              tasks=0  actor-keys=0
gin/gin_admin_errors_test.go       tasks=0  actor-keys=0
gin/gin_bodycap_test.go            tasks=0  actor-keys=0
gin/gin_bodydeadline_test.go       tasks=0  actor-keys=0
fiber/bodylimit_test.go            tasks=3  actor-keys=0     <-- NOT purely C's
```
`fiber/bodylimit_test.go:520-522` are three rows of ADR-0186's `TestEveryDecodeSiteIsBounded`,
which posts a 2 MiB body to `/tasks/x/claim`, `/complete`, `/reassign` and asserts **413** — and
the test hard-asserts its own enumeration (`require.Len(t, cases, 13, "13 decode sites")`).

**Correct value — the claim needs one qualifier:** *"186 of 186 revert, of which three rows in
`fiber/bodylimit_test.go` revert only because §3.6's ordering residual holds: on the three task
routes the body cap is evaluated BEFORE the actor is resolved."* Source-verified:
```
transport/http/fiber/groups.go:143  if oversizeBody(cfg, c) { return writeErr(cfg, c, ErrRequestBodyTooLarge) }
transport/http/fiber/groups.go:147  if err := c.Bind().JSON(&in); err != nil { … ErrBadInput … }
transport/http/fiber/groups.go:150  status, body, err := httpcore.ClaimTask(c.Context(), …)   <-- resolution lands HERE
transport/http/stdlib/groups.go:137 if !decodeRequestBody(cfg, w, req, &in) { return }
transport/http/stdlib/groups.go:141 status, body, err := httpcore.ClaimTask(req.Context(), …)
```
So the 413 does precede the 401 — the rows survive. **But that is a design property, not an
accident**, and it is the one §4 residual 8 flags as unsatisfying. If any implementer acts on the
instinct residual 8 invites — *"authenticate before we read the body"* — these three rows flip
413→401, and the file appears in **no** bundle document, so nobody is watching. That is the
identical structure as round 2's Critical: an unnamed file whose behaviour a design decision
silently governs.

**Concrete fix:**
1. Amend the grid sentence to the qualified form above.
2. Name `transport/http/fiber/bodylimit_test.go:520-522` in §2.6 as an explicit **non-member with a
   stated reason** — the same treatment §2.6 already gives `httpcore/validate_test.go` — so the
   next reader knows it was considered.
3. Add to §5 a row: *"an oversize body on each of the three task routes still answers 413, not 401,
   with no resolver configured — pins §3.6's ordering."* What makes it fail today: nothing (it
   passes today); what makes it fail **after a plausible wrong fix**: moving resolution above the
   cap check. That is the falsifiability statement §5 requires, and it is honest that the row is a
   regression guard rather than a new-behaviour test.

**Confirmation half:** the other **six** files touch no task route and carry no actor key, so their
round-2 exposure was entirely C's and reverts completely. The grid's headline is substantially
right — it is the unqualified *"ALL"* that fails, which is the quantifier class CLAUDE.md names.

---
### F10 — §2.6 singles out `gin_coverage_test.go:244` as "the 404 on a nonexistent token" when THREE of gin's members are that same shape, and plan Task 9 propagates the singular — [MINOR]

**Bundle text attacked:**
- spec §2.6: *"⚠ `gin/gin_coverage_test.go:244` asserts **404** on a nonexistent token; **gin
  carries no 403 assertion at all.**"*
- plan Task 9: *"⚠ `gin_coverage_test.go:244` asserts **404** on a nonexistent token. **gin has no
  403 assertion at all** — do not go hunting for one … ⚠ **The 401 now precedes the task lookup**,
  so an unauthenticated request for a nonexistent task returns **401, not 404**. Expect that
  assertion to move."*

**The net the author used:** the ADR-0185 correction being made was about the *403* claim, so only
the one line that motivated the correction was examined.

**The net that finds more:** read all three gin members.

**Observed** (`transport/http/gin/gin_coverage_test.go`):
```
:191 TestTaskRoutes_Claim_ErrorPath      post "/tasks/bad-token/claim"     want 404
:217 TestTaskRoutes_Complete_ErrorPath   post "/tasks/bad-token/complete"  want 404
:243 TestTaskRoutes_Reassign_ErrorPath   post "/tasks/bad-token/reassign"  want 404
```
All **three** gin `gin_coverage_test.go` members (`:192, :218, :244` are their body lines) assert
**404 on a nonexistent token**, and all three move to 401 under the ordering change — not one.

**Correct value:** three assertions move, not one. `gin/gin_test.go:413,421,443,453` are the
happy-path 200s and are unaffected by the ordering change (they use a real token).

**Concrete fix:** in both places write *"**all three** `gin_coverage_test.go` `*_ErrorPath` tests
assert 404 on a nonexistent token and **all three** move to 401"*, and keep the separate — and
correct — statement that gin carries no 403 assertion. The two facts were fused into one sentence
about one line; splitting them makes both checkable. **The "gin carries no 403 assertion at all"
half is CONFIRMED**: no `StatusForbidden` appears in gin's test files for a task route.

### F11 — "every wrong cell involved the one entry that was a REMOVAL" is FALSE — it is 2 of 4, and 3 of 4 involve a SURVIVING decision — so the removal grid was built on an inverted diagnosis and contains ZERO survivor×survivor pairs — [CRITICAL]

**Bundle text attacked:** the removal grid's opening justification, `audit2-0189-removal-grid.md:9-11`:
> *"Round 2's interaction lens found **4 of my 7 `·` ("no interaction") cells wrong, and every
> wrong cell involved the one entry that was a REMOVAL.** That is the precise reason this file
> exists."*
restated in ADR-0189:12:
> *"…because round 2 found 4 of 7 "no interaction" cells wrong and ***every*** wrong cell involved
> a removal."*
and again in spec §6: *"round 2 found 4 of 7 "no interaction" cells wrong in the first one, and
**every wrong cell involved a removal**, which is exactly what the second one is about."*

**The net the author used:** the summary sentence at the foot of round 2's interaction verdict —
*"Note the pattern: **every wrong cell involves (a), the REMOVAL.**"* — restated three times
without opening the table two lines above it. This is the CLAUDE.md recap failure exactly: *"the
false claims that survive review are the summary sentence appended to the detailed reasoning."*

**The net that finds more:** resolve the four cells against the author grid's own axis legend.

**Observed** — `audit-0189-author-interaction-grid.md:13-20` (the axes):
```
A = Attributes KEEP flowing + a marshalability pre-check   (owner D-1)   <-- SURVIVES the cut
B = the empty-Actor.ID refusal is REMOVED                  (owner D-2)   <-- "the removal"
C = every route group except Health refuses                (owner D-3)   <-- CUT to ADR-0190
D = AdminRoutes opt-in required-role gate                  (owner D-3)   <-- CUT to ADR-0190
E = endpoint-parameter shape kept; ordering documented     (owner D-4)   <-- SURVIVES
F = the claim route must accept an ABSENT body             (audit A3)    <-- SURVIVES
```
and `audit2-0189-interaction.md:758-766`, the four wrong cells, with that lens's own (x) mapping:
```
| A×B = (d)×(a) | ❌ WRONG — F6  |   involves B ✔
| A×D = (d)×(c) | ❌ WRONG — F18 |   involves B ✘
| A×F = (d)×(e) | ❌ WRONG — F7  |   involves B ✘
| B×F = (a)×(e) | ❌ WRONG — F19 |   involves B ✔
```

**Correct value:** **2 of 4** wrong cells involve B, the removal. **3 of 4 involve A** — the
`Attributes`-keep-flowing decision, which **survives the cut untouched and is Decision 5 of the
round-3 bundle.** The correct pattern statement is the opposite of the one three documents carry:
*the author's blind spot was around the ATTRIBUTES decision, not around the removal.*

**And the grid the false diagnosis produced has the matching hole.** Enumerate its sections:
```
1×C  2×C  3×C  4×C  5×C  6×C  7×C  8×C      (survivor × removed)
1–8×D                                        (survivor × removed)
C×D                                          (removed × removed)
```
**Zero survivor × survivor pairs.** Yet spec §6 item 6 lists **four** survivor decisions that
CHANGED in this same revision:
> *"Changed: (a) the refusal rule is now dimension-based; (b) the guard is a round trip classifying
> 503; (c) a size bound was added; (d) the timeout's caveat is stated."*
Four changed decisions ⇒ **6 pairs**, none derived anywhere in the bundle. CLAUDE.md: *"When a
revision changes MORE THAN ONE decision, an INTERACTION PASS is a separate, mandatory piece of
work"* — and *"Fixing N decisions and re-auditing is not the same as auditing N fixed decisions."*
The removal grid discharges the removal half of rule #9's corollary and silently drops the change
half, on the strength of a quantifier that is false.

At least one of those six pairs is live and I can name it from my own F7 above: **(a) dimension-
based refusal × (b)/(c) the round-trip guard + size bound.** §3.3 refuses an actor with no
dimensions *before* the guard runs; the guard then rejects some attribute payloads with 503. An
actor whose ONLY dimension is an attribute map that fails the guard therefore takes a different
path (503) from one with no dimensions (401), and the ordering between the two is stated in the
plan's Task 4 code but derived in no grid. My F7 probe shows the 5-of-6 figure survives that
ordering — but that is a result I had to execute, not one the bundle derived.

**Concrete fix:**
1. Correct the sentence in all three places to *"2 of the 4 wrong cells involved the removal;
   **3 of the 4 involved decision A, `Attributes` flowing — which survives this cut.**"*
2. Add the missing half to the grid: a survivor×survivor pass over the four changed decisions
   §6 item 6 names, 6 pairs, each derived. Start with the dimension-rule × guard pair above.
3. Weight it by the corrected evidence: the pairs most likely to be wrong are the ones touching
   **§3.5 `Attributes`**, since that is where three of four of round 2's misses actually were.
   The grid currently gives §3.5 one row (`5 × C`) and no survivor pairing at all.

**Local defect or inter-fix hole?** **Inter-fix, and it is the load-bearing one.** The cut (fix 1)
and the four survivor-decision changes (fixes 2–5) were made in the same revision; the grid built
to catch their interactions was scoped by a false reading of where the last round's interactions
were, and so covers the removal axis exhaustively and the changed-survivor axis not at all.

---
### F12 — the ADR names six still-open backlog items; two of them (**90**, **124**) appear nowhere else in the bundle, with no gloss and no residual — [MINOR]

**Bundle text attacked:** ADR-0189:37-38:
> *"Backlog: closes **51** for the three human-task verbs. Explicitly still open: **52**, **53**,
> **62**, **90**, **103**, **124**."*

**The net that finds more:** resolve each label against the rest of the bundle.
```
$ grep -n "90\|124" docs/specs/2026-08-25-request-actor-identity.md    # → no hits for either
```
**Observed:** the spec's §4 residual list accounts for **52 and 53** (residual 4), **62**
(residual 9) and **103** (§3.5 and residual 6). **90** and **124** are named only in the ADR, as
bare numbers. Their meaning exists in the repo but not in this bundle — from
`docs/specs/2026-08-23-authz-identity-core.md:381`: *"Backlog **90** (an eligible actor stealing
another's claim) and **124** [completion never checks the claimant] stay open."*

**Correct value:** four of the six are glossed and residualised; two are neither. CLAUDE.md rule 13
binds here explicitly — *"never emit a bare `backlog 103` without a one-clause gloss … this binds
hardest in findings tables, audit adjudications, handover documents and progress reports."*

**Concrete fix:** gloss all six inline in the ADR — *"**90** (an eligible actor can steal another
actor's claim), **124** (completion never checks that the completer is the claimant)"* — and add a
§4 residual for each, or state why the two need none. Both are *claim-ownership* gaps on exactly
the surface this bundle authenticates, so a reader is entitled to know they remain open after 0189
closes 51.

### F13 — the planned-red check the controller must run at Task 5 is unperformable: "23 + parity" double-counts parity, includes 5 lines Task 5 repairs itself, and compares a FAILING-TEST count to a MEMBER-LINE count — [MAJOR]

**Bundle text attacked:** plan, *Task order*:
> *"⚠ **Between Task 5 and Task 11 the adapter suites are RED by design** — the **23 runtime pins**
> still send `"actor"`/`"by"` and now get 401, and `parity` is in that range too.
> ⚠ **Record the failing count at Task 5 Step 5 and check it against 23 + parity.** Round 2's
> planned-red figure was wrong by ~10× because a decision added after the count was never
> re-derived."*
and Task 5 Step 5: *"**confirm the PLANNED red and record it.** Expect EXIT=1 in
`stdlib,gin,fiber,parity`."*

This is the bundle's own tripwire against the failure it is most afraid of. It cannot fire.

**Three defects in one sentence:**

1. **`parity` is inside the 23, not additional.** §2.6's runtime table lists
   `parity/parity_test.go:518` as one of its 23 rows. The sentence says *"`parity` is in that range
   too"* and then instructs a check against *"23 **+** parity"* = 24. Round 1's counting F13 filed
   the opposite error (*"leaves `parity` out of its own range"*); the fix added the "in that range
   too" clause and left an addition that now double-counts it.
2. **Five of the 23 are repaired by Task 5 itself**, so they cannot be red at Task 5 Step 5.
   Task 5's own file list claims *"**5 httpcore JSON fixtures**: `dto_test.go:57,68,79,151,161`"* —
   which are exactly the httpcore rows of the runtime table.
   ```
   $ grep -v httpcore /tmp/netA.txt | wc -l       # runtime members outside httpcore
   18
        5 fiber/fiber_test.go   3 gin/gin_coverage_test.go   4 gin/gin_test.go
        1 parity/parity_test.go 2 stdlib/coverage_test.go    2 stdlib/errors_test.go
        1 stdlib/stdlib_test.go
   ```
3. **The units do not match.** *"Record the failing count"* from a `go test` run yields **failing
   tests**; 23 is a count of **member lines**. Derived, the 18 remaining lines sit in **10** tests:
   ```
   TestParity_PostTasksClaim_200 · TestTaskRoutes_Claim_ErrorPath · TestTaskRoutes_ClaimCompleteReassign
   TestTaskRoutes_Complete · TestTaskRoutes_Complete_ErrorPath · TestTaskRoutes_Complete_ServiceError
   TestTaskRoutes_Customize · TestTaskRoutes_Reassign · TestTaskRoutes_Reassign_ErrorPath
   TestTaskRoutes_Reassign_ServiceError
   ```
   A controller comparing a `--- FAIL` count against 23 or 24 sees neither and has no rule for what
   to do — and Task 5 Step 5's only stop condition is about the two 403 pins, so the mismatch does
   not halt anything. The tripwire reports a wrong number and keeps going, which is worse than
   having no tripwire.

**Correct value:** *"At Task 5 Step 5 expect **18** runtime member lines red, in **10** tests,
across `stdlib`(5 lines/4 tests), `gin`(7/3), `fiber`(5/2), `parity`(1/1); `httpcore` must be
**GREEN**, because Task 5 repairs its 10 lines itself. Task 6 then adds **2** more red lines
(F1: `stdlib/coverage_test.go:158`, `gin/gin_coverage_test.go:184`) in 2 further tests."*

**Concrete fix:** replace the sentence with the per-package line/test table above, state the unit
explicitly, and give Step 5 a real stop condition: *"if `httpcore` is red, or the per-package
counts differ from the table, STOP — the member set has drifted."* That is the check the sentence
was reaching for.


---

## Verdict

Worktree restored and clean (`git status --porcelain` empty, `go build ./...` EXIT=0); all probes
and the two-change ablation were reverted from `cp` backups, never `git checkout <path>`.

### Counts by severity

| severity | n | findings |
|---|---|---|
| **CRITICAL** | **5** | F1, F2, F4, F5, F11 |
| **MAJOR** | **2** | F9, F13 |
| **MINOR** | **6** | F3, F6, F7, F8, F10, F12 |
| **CONFIRMATIONS** (no defect) | 6 | see below |
| **total actionable** | **13** | |

### Confirmations — re-derived independently, do NOT re-litigate

1. **§2.6's compile-breaking table is EXACT.** The two-change ablation reproduces all 23 lines /
   5 files / 4 packages at the stated line numbers, including the three "invisible to round 1"
   bolded lines (`endpoints_test.go:436,499,575`).
2. **§2.6's runtime table is EXACT on lines.** The `"actor"`/`"by"` net returns exactly 23 lines in
   exactly the 8 listed files at the listed line numbers. (Its *sub-header* is not — F2.)
3. **The per-task assignment property HOLDS.** Every one of §2.6's 48 members is assigned to
   exactly one task (Task 5 = 28, Tasks 8–10 = 17, Task 11 = 3; sum 48), and no task claims a line
   outside §2.6. This is the property the brief asked me to check and it is clean — the omission
   is *outside* §2.6 (F1), not inside the assignment.
4. **"5 of 6 attribute shapes still ALLOW" is CORRECT** — executed, six-shape set now named (F7).
5. **The ADR-0189 trend anchors 1.75 and 3.75 are CORRECT** and are RAW filed Criticals —
   re-derived from the eight lens reports independently of the adjudications (F5). The 1.75 → 3.75
   scope inference the re-cut acted on **stands**.
6. **Six small enumerations re-derive exactly:** three `authz.Actor{` construction sites; nine
   adapter call sites; eight `CustomizeConfig` fields; zero `DisallowUnknownFields` in
   `transport/`+`internal/`; three `WithActorResolver` exports (`service`, `runtime/task`,
   `processtest`); "gin carries no 403 assertion at all". Task 7's "six aliases, two per adapter"
   is correct by construction. §5 has exactly 17 rows; §4 has exactly 12 residuals.
   `humantask/validate_test.go:45-47` is a **non-vacuous** fixture — it really declares
   `Actor{Roles:["kiosk"]}` with an empty ID.

### Every enumeration, [claimed | re-derived | verdict]

| # | enumeration | claimed | re-derived | verdict |
|---|---|---|---|---|
| 1 | self-asserted actor sites | 3 | 3 (`endpoints.go:119,132,150`) | ✅ |
| 2 | `CustomizeConfig` fields | 8 | 8 | ✅ |
| 3 | adapter call sites | 9 | 9 (ablation) | ✅ |
| 4 | compile-breaking members | 23 lines / 5 files / 4 pkg | 23 / 5 / 4 | ✅ |
| 5 | runtime members — **lines** | 23 | 23 | ✅ |
| 6 | runtime members — **files / packages** | 7 / 4 | **8 / 5** (or 7 / **1** new) | ❌ **F2** |
| 7 | stale-doc members | 2 / 1 / 1 | 2 / 1 / 1 | ✅ |
| 8 | **TOTAL blast radius** | **48 / 13 / 6** | **50 / 13 / 6** | ❌ **F1** |
| 9 | per-task pins | 5(28) · 8–10(17) · 11(3) | 5(28) · 8–10(**19**) · 11(3) | ❌ **F1** |
| 10 | Task 8 stdlib pins | 5 | **6** | ❌ **F1** |
| 11 | Task 9 gin pins | 7 | **8** | ❌ **F1** |
| 12 | Task 10 fiber pins | 5 | 5 | ✅ |
| 13 | planned red at Task 5 | "23 + parity" | **18 lines / 10 tests**, httpcore green | ❌ **F13** |
| 14 | adapter option aliases | 6, two per adapter | 6 | ✅ |
| 15 | fiber's existing option set | `{BasePath, MaxBodyBytes}` implied | `{BasePath, MaxBodyBytes, **Middleware**}` | ❌ **F6** (3rd round) |
| 16 | attribute shapes that ALLOW | 5 of 6 | 5 of 6 (set now named) | ✅ **F7** |
| 17 | §5 test rows | 17 | 17 | ✅ |
| 18 | §4 residuals | 12 | 12 | ✅ |
| 19 | `WithActorResolver` exports | 3 | 3 | ✅ |
| 20 | `DisallowUnknownFields` in repo | 0 | 0 | ✅ |
| 21 | gin 403 assertions | 0 | 0 | ✅ |
| 22 | gin `gin_coverage` 404-on-bad-token | 1 (`:244`) | **3** (`:192,:218,:244`) | ❌ **F10** |
| 23 | example `stdlib.Mount` sites | 3 | 3 | ✅ (the round-2 "four" dissolved with the cut) |
| 24 | round-1 Criticals / lens | 1.75 | 1.75 (7/4) | ✅ |
| 25 | round-2 Criticals / lens | 3.75 | 3.75 (15/4) | ✅ |
| 26 | round-2 total findings | 58 | 58 | ✅ |
| 27 | **the "lineage" trend** | 8.25→3.50→1.75→3.75 | 8.25/3.50 are **ADR-0186 rds 3,7**; ADR-0185's 3 rounds absent (one measured at **5.50**) | ❌ **F5** |
| 28 | "fourth consecutive fall" | 4 falls | 3 points ⇒ **2** transitions | ❌ **F5** |
| 29 | author-grid `·` cells wrong | 4 of 7 | 4 of 7 | ✅ |
| 30 | …that involved the REMOVAL | **every** (4 of 4) | **2 of 4**; 3 of 4 involve a **survivor** | ❌ **F11** |
| 31 | removal-grid pair coverage | complete | 0 survivor×survivor pairs, 6 undrawn | ❌ **F11** |
| 32 | 186 round-2 assertions "ALL C's" | all | 6 of 7 files yes; `fiber/bodylimit_test.go` conditional | ⚠ **F9** |
| 33 | still-open backlog items | 6 (52,53,62,90,103,124) | 6 real; **2 unglossed, 2 without a residual** | ❌ **F12** |
| 34 | `file.go:NNN` anchors | ~75 across 4 docs | 74 resolve, **1 broken** (`runtime/task/service.go:154`) | ❌ **F8** |
| 35 | author grid blast radius | 37 | 48 → 50 | ❌ **F3** (2nd round) |

### Criticals — local defect or inter-fix hole

| # | Critical | class | why |
|---|---|---|---|
| **F1** | member set omits the 2 claim-badjson pins | **local** | the ablation's net was never built for §3.6's optional-body decision, in any round; the cut did not create the gap, it re-asserted the total over it |
| **F2** | "re-verified clean by round 2's counting lens" is false; the accepted sub-header fix is unfolded | **local** | an accepted finding dropped between rounds; orthogonal to C/D/G |
| **F4** | the self-review table still owns the two removed decisions | ⚠ **INTER-FIX** | the removal of C/D/G and the task renumbering are two fixes; the table sits on their intersection and neither owned it |
| **F5** | the decision rule's trend splices ADR-0186's numbers | **local** | an inherited citation restated at two hops without re-derivation |
| **F11** | "every wrong cell involved a removal" is 2 of 4; the grid has no survivor×survivor axis | ⚠ **INTER-FIX**, and the load-bearing one | the cut and the four survivor-decision changes were made together; the grid built to catch their interactions was scoped by a false reading of where the last round's interactions actually were |

### ⚖ On the pre-registered decision rule — read F5 BEFORE applying it

My lens alone files **5 Criticals**, and **two are inter-fix holes** (F4, F11). Taken at face value
the rule's **third row** fires — *"Criticals/lens ≥ 3, **or any Critical is again an inter-fix
hole** ⇒ stop and escalate to the owner"* — on the second clause, independently of the rate.

⚠ **But the rule is circular with F5 and the adjudicator must not apply it mechanically.** Its
thresholds are presented as calibrated by a trend whose first half belongs to ADR-0186. Against the
repo's actual 4-lens population (`meta-analysis-audit-finding-rate.md`, ten rounds, Crit/lens
4.00 · 4.33 · 8.25 · 7.00 · 5.00 · 6.00 · 3.50 · 4.25 · 5.50 · 5.50, **mean ≈ 5.33**) a "< 1.5"
threshold has been met by **no round in this repo's recorded history**, so the rule's first row is
effectively unreachable and its third row is close to the population mean. That is a materially
different instrument from the one the rule's prose implies.

My recommendation, stated as a lens and not as the adjudicator:

- **F11 is the finding that should decide this**, not the count. It says the grid built to satisfy
  rule #9's corollary was aimed at the wrong axis, and that six survivor×survivor pairs from the
  same revision are underived. That is a **cheap, bounded** piece of work — six pairs — not another
  round. Rule #11's *"expect implementation to correct the design"* does not cover it, because an
  undrawn interaction is exactly what implementation does not surface.
- **F1, F13, F2, F3, F6, F8, F10, F12 are all mechanical folds** — line numbers, sub-headers,
  a citation, two glosses. None needs a decision.
- **F4** is a delete-two-rows fix.
- **F5** needs an owner sentence, not an audit round: *which* series is the rule measured against.

⇒ **Fold everything, draw the six missing pairs, correct the rule's calibration statement, and
ship. A fourth round would measure the process, not the bundle** — which is the conclusion the
grid's own second row reaches, and which F5 shows was argued from the wrong numbers but is
nonetheless supported by the right ones (this lens's 5 Criticals are 8 mechanical corrections and
one real design gap, not five design failures).
