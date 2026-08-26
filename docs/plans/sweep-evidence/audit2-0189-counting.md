# ADR-0189 round 2 — COUNTING lens

Worktree `wt2-counting`, detached at `37d77a34`. Step 0: all four bundle documents present.
Docker unavailable; every probe below is container-free (`transport/http/...`, `authz`, `service`).

**Method note.** Per the brief, for every enumeration I first asked *"what net would find a member
this net misses?"* and then ran THAT net. The author's §2.6 net is a **two-change compile ablation**
(DTO field removal + resolver parameter) plus a `"actor"`/`"by"` grep. Both nets are scoped to the
**round-1 change**. The revision added scope — four route groups authenticate, `AdminRoutes` gains a
role gate — and **no net in the bundle was re-run over that**.

---
### F1 — §2.6's member set enumerates ONLY the round-1 change; the revision's NEW scope (four route groups authenticate) breaks **186 further assertion lines in 13 files**, seven of which appear in no bundle document at all — [CRITICAL]

**Bundle text attacked:**
- spec §2.6: *"#### Total: **48 lines · 13 files · 6 packages**"*, introduced as *"### 2.6 The blast radius — the MEMBER SET, re-derived"* and as the machine check on the net being *"closed by construction"*.
- plan, *Task order*: *"⚠ **Between Task 5 and Task 14 the adapter suites are RED by design** — the 23 runtime pins still send `"actor"`/`"by"` and now get 401. Planned red."*
- plan, Tasks 12/13/14: *"`stdlib` — 5 runtime pins"*, *"`gin` — 7 runtime pins"*, *"`fiber` — 5 runtime pins"*.
- plan, Tasks 9/10/11 Step 4: *"**GREEN.**"*

**The net the author used:** a two-change compile ablation (delete the three DTO `Actor`/`By` fields
+ add the resolver parameter to the three task endpoints), plus a `"actor"`/`"by"` grep over
`transport/`. **Both nets are scoped to the TASK routes.** Round 1's decision was *"only the three
task routes are touched"*; owner decision D-3 changed that to *"every route group except
`HealthRoutes`"* — and no net in the bundle was re-run against the enlarged decision.

**The net that finds more:** ablate what §3.6 actually specifies — an unauthenticated 401 in the
`InstanceRoutes` / `MessageRoutes` / `AdminRoutes` handlers **before decode** — and run the suites.
No existing test installs a resolver, so under §3.6 every one of these requests is unauthenticated.
Patch applied in the worktree (restored afterwards; `git status --porcelain` clean):

```
stdlib  transport/http/stdlib/groups.go   handle(): 401 when pattern has prefix /instances|/messages|/admin
gin     transport/http/gin/observe.go     observe(): 401 when routeTemplate contains those
fiber   transport/http/fiber/observe.go   observed(): same
$ go build ./transport/... && for p in stdlib gin fiber parity; do go test -count=1 ./transport/http/$p/... ; done
```

**Observed:**

```
stdlib EXIT=1   top-level FAIL: 42   FAIL lines: 72
  distinct failing assertion lines by file:
     2 bodydeadline_test.go   17 coverage_test.go    9 errors_test.go
     6 maxbody_test.go       12 stdlib_test.go                       = 46
gin    EXIT=1   top-level FAIL: 44   FAIL lines: 73
     5 gin_admin_errors_test.go  13 gin_admin_test.go  13 gin_bodycap_test.go
     2 gin_bodydeadline_test.go   8 gin_coverage_test.go  14 gin_test.go = 55
fiber  EXIT=1   top-level FAIL: 35   FAIL lines: 69
    26 bodylimit_test.go     31 fiber_test.go                        = 57
parity EXIT=1   top-level FAIL: 13   FAIL lines: 29
    28 parity_test.go                                                = 28
```

Representative failures (real text):

```
--- FAIL: TestInstanceRoutes_Signal_NotFound
    errors_test.go:118: want 404 signal not-found, got 401
--- FAIL: TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute
    maxbody_test.go:213: expected: 413   actual: 401
--- FAIL: TestParity_PostInstances_201
    parity_test.go:293: stdlib: want 201 got 401
--- FAIL: TestAbortedUploadIsNotA413/truncated_mid-value_is_a_400,_not_a_413
    gin_bodycap_test.go:183: expected {"error":"bad_request","message":"workflow-httpcore: bad input: unexpected EOF"}
                              actual   {"error":"unauthenticated","message":""}
```

**Correct value:** **186 distinct failing assertion lines across 13 test files in 4 packages**,
and the sets are **DISJOINT from §2.6's** — §2.6's runtime pins are all on task routes
(`errors_test.go:155,187`; `stdlib_test.go:471`; `coverage_test.go:92,126`; …), none of which is in
the list above. **Seven of these files are named nowhere in the spec, the ADR, the plan or the
author grid:**

| file | failing assertion lines | named in bundle? |
|---|---|---|
| `transport/http/stdlib/maxbody_test.go` | 6 | ❌ |
| `transport/http/stdlib/bodydeadline_test.go` | 2 | ❌ |
| `transport/http/gin/gin_admin_test.go` | 13 | ❌ |
| `transport/http/gin/gin_admin_errors_test.go` | 5 | ❌ |
| `transport/http/gin/gin_bodycap_test.go` | 13 | ❌ |
| `transport/http/gin/gin_bodydeadline_test.go` | 2 | ❌ |
| `transport/http/fiber/bodylimit_test.go` | 26 | ❌ |

Three consequences, each independently blocking:

1. **The plan's "planned red" sentence is FALSE as a quantifier.** It says the red between Task 5
   and Task 14 is *"the 23 runtime pins"*. It is 23 + 186. A dispatched agent told the red is 23
   lines and finding 209 will not know which failures are planned and which are its own bug — the
   exact condition under which "planned red" stops being a safety net.
2. **Tasks 9/10/11 cannot reach their own Step 4 ("GREEN").** They are the tasks that *install* the
   refusal; the moment they do, their package suite has 42/44/35 failing tests that Tasks 12–14 do
   not enumerate and whose repair is described nowhere.
3. **Tasks 12/13/14 are under-scoped by ~10×** (5/7/5 pins claimed vs 46/55/57 assertion lines
   actually broken in those packages), and they are the tasks dispatched to **parallel subagents**
   with *"you may not touch any file outside your package"* — so nobody owns the gap.

**Concrete fix:** re-derive §2.6 over the **revision's** decision set, not round 1's. Add a fourth
sub-table, *"Route-group authentication — runtime, 186 lines / 13 files / 4 packages"*, produced by
the ablation above and pasted the way §2.6 pastes the others. Then re-scope plan Tasks 12/13/14 to
the real per-package lists (and rename them: they are no longer "test migration", they are
"install a resolver at every mount site in the package"), correct the "planned red" sentence to the
real figure, and state in Tasks 9–11 that Step 4 GREEN is only reachable **after** the mount-site
sweep — i.e. merge 9/12, 10/13, 11/14 into one task per package rather than two.

---
### F2 — §2.6's **runtime-only sub-header contradicts its own table**: "7 files / 4 packages" over a table listing 8 files in 5 packages — [MAJOR]

**Bundle text attacked:** spec §2.6, *"#### Runtime-only — 23 lines / **7 files** / **4 packages**"*,
followed by an eight-row table.

**The net the author used:** the sub-headers were written per-table; only the **48 / 13 / 6** grand
total was reconciled.

**The net that finds more:** count the table's own rows and their packages, then check the two
sub-headers use the same convention.

**Observed** — the runtime table's rows, verbatim from §2.6:

```
httpcore/dto_test.go 5 | gin/gin_test.go 4 | gin/gin_coverage_test.go 3 | fiber/fiber_test.go 5
stdlib/errors_test.go 2 | stdlib/stdlib_test.go 1 | stdlib/coverage_test.go 2 | parity/parity_test.go 1
⇒ 8 rows, lines 5+4+3+5+2+1+2+1 = 23 ✓ ; packages {httpcore, gin, fiber, stdlib, parity} = 5
```

**Correct value:** **23 lines / 8 files / 5 packages**. Neither claimed figure is defensible:

- read as *distinct members of this table*: 8 files, 5 packages — the header says 7 and 4.
- read as *new members not already counted in the compile table*: 7 files (dto_test.go dedups) —
  which makes the "7" work but the "4" **worse**, since the only new package is `parity` ⇒ 1.
  The compile sub-header does **not** use that dedup convention (its 5 files and 4 packages are
  raw counts), so the two tables silently count by different rules.

The 23 sums, the 48 sums, the 13 dedups correctly — and the sub-headers still misdescribe the
tables they sit on. This is exactly the shape the brief names: *the arithmetic is right, the
enumeration is wrong*, in the artifact built to be the corrective for a counting failure.

**Concrete fix:** make both sub-headers raw counts of their own table (`23 lines / 5 files /
4 packages` and `23 lines / 8 files / 5 packages`), and state the dedup explicitly in the total
line: *"Total: 48 lines · 13 distinct files (`dto_test.go` appears in both tables) · 6 packages."*

---

### F3 — the author's interaction grid states the blast radius as **"37 lines / 6 packages"**, the figure the spec and ADR both call WRONG — a flat cross-document contradiction on the headline number — [MAJOR]

**Bundle text attacked:**
- `audit-0189-author-interaction-grid.md`, "The changed decisions" table, row **G**:
  *"blast radius corrected to **37 lines / 6 packages**; counting method changed to member-set"*.
- spec §2.6: *"⚠ The audit's own proposed figure of **37** is also wrong — it unions the two *pin*
  nets and omits the 9 production call sites and the 2 `service` comments."*
- ADR Negative: *"**48 lines across 13 files in 6 packages** change."*

**The net the author used:** the grid was written **before** the revision (correctly, per rule #9's
corollary) and its row G records the *audit's* proposed correction. The revision then re-derived
37 → 48 and never went back to the grid — which the spec §6 nevertheless designates *"an INPUT to
the audit, not a conclusion. **Attack it too**."*

**The net that finds more:** diff the numbers each of the four bundle documents states for the
same quantity.

**Observed:**

| document | blast radius stated |
|---|---|
| spec §2.6 | **48** lines / 13 files / 6 packages — and "37 is also wrong" |
| ADR, Negative | **48** lines / 13 files / 6 packages |
| plan, self-review | **48** (23 + 23 + 2), split 28 / 17 / 3 |
| **author grid, row G** | **37** lines / 6 packages |

**Correct value:** neither, per F1 — but within the bundle as written, 48. The grid is stale by a
whole revision on the one figure this delivery exists to get right.

**Concrete fix:** rewrite grid row G to *"blast radius re-derived to 48 lines / 13 files /
6 packages; the audit's proposed 37 was itself wrong (see spec §2.6)"* — and add a dated
"amended after the revision" note, since the grid's value is that it was written first.

---

### F4 — the grid **explicitly exempts the blast-radius count from its own pairwise pass** — and C × G is where the Critical is — [CRITICAL]

**Bundle text attacked:** `audit-0189-author-interaction-grid.md`, immediately under the grid:

> *"**G, H, I, J, K are documentation/test changes with no decision surface; each was checked
> against A–F and none interacts.** That check is itself recorded so a re-auditor need not redo it."*

where **G** is *the blast-radius count* and **C** is *"every route group except `HealthRoutes`
refuses an unresolved actor"*.

**The net the author used:** G was classified as *"documentation/test, no decision surface"* and
dropped out of the 6×6 grid. The grid's live cells are A–F only.

**The net that finds more:** ask the question the exemption forecloses — *what does C do to G's
premises?* C is a **behaviour change on 12 route-group implementations that no member of G's set
touches**. Run it (F1's ablation).

**Observed:** C alone breaks **186 assertion lines across 13 test files in 4 packages**, disjoint
from G's 48. See F1 for the full measurement.

**Correct value:** **C × G is a live interaction and the largest one in the revision.** The grid's
sentence *"none interacts"* is false, and it is false for the one item whose exemption removed the
only mechanism that would have caught it. The grid even flags the general hazard correctly —
*"An unwritten interaction is the cheapest possible Critical"* — and then writes the exemption that
guarantees this one stays unwritten.

⚠ Note the shape: G is not *"documentation"*. A count of what a change touches is a **derived
quantity of every other decision in the set**; it can never be exempt from a pairwise pass, because
every changed decision is by definition an input to it. The same argument disqualifies the blanket
exemption of **K** (`CHANGELOG` + `STABILITY.md`), whose content is likewise a function of A–F.

**Concrete fix:** delete the blanket exemption. Add a mandatory rule to the grid's method: **the
blast-radius row is graded against every other changed decision, always**, and is the last row
re-derived. Then re-run C × G, C × K, D × G, D × K and F × G, and fold the results into §2.6 per
F1.

---
### F5 — plan Task 7 is titled *"the **six** adapter option aliases"* and produces **nine** — [MINOR]

**Bundle text attacked:** plan, *"### Task 7: the six adapter option aliases"* /
*"**Produces:** `WithRequestActor`, `WithRequestActorTimeout`, `WithAdminRoles` in each."*

**The net the author used:** the title was carried forward and re-scaled once. Round 1's plan
(`7fa756d0`) had *"### Task 6: **the three** adapter option aliases"* with a single symbol
(`WithRequestActor`) × 3 adapters = 3. The revision added `WithRequestActorTimeout` **and**
`WithAdminRoles`, and the title moved 3 → 6 — i.e. it tracked the second symbol and not the third.

**The net that finds more:** multiply the Produces list by the Files list instead of editing the
title.

**Observed:** 3 symbols × 3 files (`stdlib/options.go`, `gin/options.go`, `fiber/options.go`) = **9**.

**Correct value:** **nine**. `WithAdminRoles` is required in all three adapters because §3.7's
example is written `stdlib.WithAdminRoles("platform-admin")   // + gin. / fiber. aliases`, and
because the generic `httpcore.WithAdminRoles[R]` cannot be called without an explicit type
argument (`R` appears only in the result type) — the plan's own justification for the aliases.

**Concrete fix:** *"### Task 7: the nine adapter option aliases (3 symbols × 3 adapters)"*.

---

### F6 — round-1 counting F9 was **never adjudicated and never folded**: §2.4's option-alias sentence still mis-states fiber's set, and the plan restates the error — [MINOR]

**Bundle text attacked:**
- spec §2.4: *"`stdlib` and `gin` export `WithBasePath`/`WithMaxBodyBytes`/`WithBodyReadTimeout`
  (**gin adds `WithMiddleware`**), while **`fiber` has no `WithBodyReadTimeout`** … Do not infer
  the new alias set from any one adapter's file."*
- plan Task 7: *"⚠ Do not infer the set from one file — `fiber` has no `WithBodyReadTimeout` and
  **that asymmetry** is deliberate."* (singular)

**The net the author used:** describe two adapters and subtract to get the third.

**The net that finds more:** enumerate all three.

```
$ for p in stdlib gin fiber; do echo "--- $p"; grep -n "^func With" transport/http/$p/options.go; done
--- stdlib   WithBasePath:13  WithMaxBodyBytes:28  WithBodyReadTimeout:47                     (3)
--- gin      WithBasePath:28  WithMaxBodyBytes:41  WithBodyReadTimeout:59  WithMiddleware:69  (4)
--- fiber    WithBasePath:23  WithMaxBodyBytes:41  WithMiddleware:49                          (3)
```

**Correct value:** `WithMiddleware` is exported by **gin AND fiber** (`fiber/options.go:49`), so
fiber's set is `{BasePath, MaxBodyBytes, **Middleware**}`, not the described set minus one. There
are **two** asymmetries, not one: fiber lacks `WithBodyReadTimeout`, and **stdlib lacks
`WithMiddleware`**.

⚠ **The aggravating fact is procedural.** This is verbatim round-1 counting-lens **F9**
(`audit-0189-counting.md:364`), with the same command and the same numbers. It appears **nowhere**
in `audit-0189-adjudication.md`:

```
$ grep -n -i "WithMiddleware\|option-alias\|alias set" docs/plans/sweep-evidence/audit-0189-adjudication.md
(no output)
```

The adjudication explicitly disposes of counting F1, F3, F4, F5, F6, F7, F10, F11, F12; F8 and F13
were folded into the revision without a row. **F9 was silently dropped** — CLAUDE.md's Delivery
Gate: *"Findings you adjudicate as false-positive or out-of-scope must be stated explicitly with
the reason — silence is not an adjudication."* And it survives inside the one paragraph in the
bundle whose entire purpose is to warn against inferring an enumeration from one file.

**Concrete fix:** replace both prose sentences with the three-row table above, and add an F9 row to
the adjudication recording that it was missed in round 1 and fixed in round 2.

---

### F7 — §2.5 and plan Task 16 enumerate **three** example mount sites; there are **four**, and the fourth mounts `AdminRoutes` — which the plan simultaneously says must not happen — [MAJOR]

**Bundle text attacked:**
- spec §2.5: *"All three are `ListenAndServe` servers that mount routes and wait;
  `stdlib.Mount(mux, svc)` at `production_wiring/main.go:264`, `sqlite_wiring/main.go:278`,
  `mysql_wiring/main.go:262`."*
- plan Task 16 Step 2: *"the three wiring mains get the constant `demo-user` actor …
  ⚠ `production_wiring` already passes `httpcore.WithMeterProvider[...]` — **append**. …
  They must **not** mount `AdminRoutes`."*
- ADR Decision 9: *"the mains do **not** mount `AdminRoutes`."*

**The net the author used:** `grep` for `stdlib.Mount(` — which finds the `Mount` helper and
nothing else.

**The net that finds more:** grep for every way a route group can be attached, i.e. `.Customize(`
and `MountHealth` too.

```
$ grep -rn "Mount\|Customize\|Routes{" examples/ | grep -v _test
examples/production_wiring/main.go:264: stdlib.Mount(mux, svc, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
examples/production_wiring/main.go:265: stdlib.MountHealth(mux, readyChecks...)
examples/production_wiring/main.go:274: stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
examples/sqlite_wiring/main.go:278:     stdlib.Mount(mux, svc)
examples/sqlite_wiring/main.go:279:     stdlib.MountHealth(mux, readyChecks...)
examples/mysql_wiring/main.go:262:      stdlib.Mount(mux, svc)
examples/mysql_wiring/main.go:263:      stdlib.MountHealth(mux, readyChecks...)
```

**Correct value:** **four** sites need the new option (`Mount` ×3 + `AdminRoutes.Customize` ×1);
`MountHealth` ×3 is correctly exempt under §3.6. And `production_wiring` **already mounts
`AdminRoutes`**, on a dedicated `adminMux` — which is *precisely the pattern ADR-0189 Decision 7
cites as the reason its role gate must be opt-in* (*"typically on a separate, access-controlled
mux"*). Consequences:

1. **The instruction "they must **not** mount `AdminRoutes`" describes a state that is already
   false**, so it silently prescribes **deleting** `production_wiring:270-278` — a scope item the
   plan never states and Task 16 does not budget for. If instead it is read as "leave it", the
   sentence is a false claim in the ADR's Decision 9.
2. **Whichever reading wins, `production_wiring:274` gets no resolver** if Task 16 only touches the
   three `stdlib.Mount` calls it names — so the example that follows the repo's own documented
   admin pattern is the one whose admin mux answers 401 to everything after this ships.
3. Task 16 Step 3 (*"run each main and confirm a clean start; `curl` the claim route"*) would not
   catch it: a clean start is not a `curl` of `/admin/*`.

**Concrete fix:** correct §2.5's enumeration to four sites (naming `production_wiring:274`), decide
explicitly whether `production_wiring` keeps its admin mux (recommended: keep it, and use it as the
demonstration of `WithAdminRoles` — it is the only wiring in the repo that already does the right
thing), and delete or rescope the "must not mount `AdminRoutes`" sentence in **both** the ADR and
the plan.

---
### F8 — §3.8's *"unchanged from today, not a regression"* is scoped to the **three task routes**; on the other three groups the ordering **inverts** and ADR-0186's `400`/`413` contract becomes `401` — stated nowhere — [MAJOR]

**Bundle text attacked:** spec §3.8 / ADR Decision 8:

> *"⚠ **Authentication on the three task routes resolves BEHIND the adapter's capped body read.**
> ADR-0186's measured read window (1 MiB / 30 s) and its 400/413 responses stay reachable without a
> credential … **Unchanged from today, not a regression** — but stated, because round 1 claimed the
> transport 'fails closed at every entry' and that is false."*

**The net the author used:** the residual was derived for the routes the endpoint-parameter shape
covers (claim / complete / reassign) — correctly. The complementary set was never derived, even
though §3.6 puts its refusal **before** decode, which is the opposite ordering.

**The net that finds more:** apply §3.6's pre-decode refusal to `InstanceRoutes`/`MessageRoutes`/
`AdminRoutes` and run ADR-0186's own body-cap suites, which are the tests that own the 400/413
contract. (Same ablation as F1.)

**Observed:**

```
--- FAIL: TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute        (stdlib/maxbody_test.go:213)
        expected: 413   actual: 401
--- FAIL: TestAbortedUploadIsNotA413/truncated_mid-value_is_a_400,_not_a_413   (gin_bodycap_test.go:183)
        expected {"error":"bad_request","message":"workflow-httpcore: bad input: unexpected EOF"}
        actual   {"error":"unauthenticated","message":""}
--- FAIL: TestParity_MaxBodyBytes_UnderCap/a_clean_under-cap_body_succeeds_identically_on_all_three
--- FAIL: TestStalledBodyIsBoundedByTheReadDeadline                     (stdlib/bodydeadline_test.go)
```

Whole ADR-0186 suites are affected: `stdlib/maxbody_test.go` (6 lines), `stdlib/bodydeadline_test.go`
(2), `gin/gin_bodycap_test.go` (13), `gin/gin_bodydeadline_test.go` (2), `fiber/bodylimit_test.go` (26).

**Correct value:** the residual is **asymmetric and the bundle states only half of it**:

| group | ordering after ADR-0189 | consequence |
|---|---|---|
| task ×3 | 401 **behind** the capped read | ADR-0186's 400/413 stay reachable unauthenticated — *stated* |
| instance / message / admin | 401 **before** the read | ADR-0186's 400/413 become **unreachable** unauthenticated — **not stated anywhere** |

That second row is a **behaviour change to a shipped ADR's documented contract**, and it is the
better half of the trade (an unauthenticated caller can no longer probe parse validity or body
size on those routes) — which is exactly why it should be claimed deliberately rather than
discovered by a subagent watching `bodylimit_test.go` go red.

**Concrete fix:** add the table above to §3.8 and to ADR Decision 8; add ADR-0186 to the ADR's
`Relates to` line as *"whose 400/413 contract this record narrows on the three pre-decode groups"*;
and add a plan step re-pinning the affected ADR-0186 tests with an authenticated resolver so the
cap behaviour is still exercised (not deleted).

---

### F9 — spec §5 row 10 prescribes **nine** test cases (3 adapters × 3 verbs); the plan prescribes **one**, in one adapter — [MINOR]

**Bundle text attacked:** spec §5, row 10: *"**per adapter × 3 verbs**: body carries `"actor"`,
context carries another ⇒ the **context** actor wins | today the body wins"*. The plan's self-review
maps *"§5 rows 1–15"* to *"1, 2, 3, 4, 5, 6, 8, 9–14, 15"*, claiming full coverage.

**The net the author used:** the self-review checked that each row has *a* task, not that the task
delivers the row's **cardinality**.

**The net that finds more:** read what Tasks 12/13/14 actually prescribe for the body-vs-context
conflict.

**Observed:**

```
Task 12 (stdlib): "Add the seam test: middleware authenticates a viewer, body claims a manager,
                   expect 403 — the middleware's actor wins. Plus a bare-mount 401."   ← 1 verb (complete)
Task 13 (gin):    "Seam test using gin's working idiom: gc.Request = gc.Request.WithContext(...)"  ← verb unspecified,
                   conflict unspecified
Task 14 (fiber):  "Seam test using c.SetContext."                                       ← same
```

**Correct value:** row 10 is **9 cases**; the plan describes the conflict explicitly for **1**
(stdlib × complete) and leaves the verb and the conflict unstated for gin and fiber. Since the
whole point of the row is that the **body loses**, and the body key differs per verb
(`"actor"` for claim/complete, `"by"` for reassign), a gin/fiber "seam test" that merely
authenticates and succeeds does not test it at all — it would pass identically if the body were
still read.

**Concrete fix:** either cut row 10 to *"per adapter, one verb; plus `reassign`'s `"by"` key once,
on stdlib"* (4 cases) and say so, or spell the 9 out in Tasks 12–14. Do not leave the spec claiming
9 and the plan delivering 1.

---

### F10 — plan Task 17's documentation sweep uses a net (`grep '"actor"'`) that cannot find the doc references it exists to find — [MINOR]

**Bundle text attacked:** plan Task 17: *"`grep -rn '"actor"' README.md docs/ examples/` — fix
anything documenting the removed field."*

**The net the author used:** the JSON **key** spelling `"actor"`.

**The net that finds more:** the Go **symbol** spellings too — `httpcore.Actor`,
`CompleteInput{Actor…}`, `ClaimInput{…}`.

**Observed:**

```
$ grep -rn '"actor"' README.md docs/ examples/ | grep -v <this bundle>   → 26 hits
$ grep -rnE 'CompleteInput\{|ClaimInput\{|httpcore\.Actor' README.md SECURITY.md CHANGELOG.md \
      STABILITY.md docs/adr/ examples/ | grep -v <this bundle>
docs/adr/0147-humantask-audit-model.md:195   `httpcore.Actor` is `{id, roles}` only …      ← IN the bundle (Task 15)
docs/adr/0146-usertask-outcomes-and-completion-api.md:12  `httpcore.CompleteInput{Actor, Output}` → …   ← NOT in the bundle
$ grep -c '"actor"' docs/adr/0146-usertask-outcomes-and-completion-api.md
0
```

**Correct value:** ADR-0146 (*user-task outcomes and the completion API*) names
`httpcore.CompleteInput{Actor, Output}` at `:12` as the head of the completion chain, and repeats
`httpcore.CompleteInput` at `:43`. The prescribed grep returns **zero** hits in that file, so the
sweep would report clean over a shipped ADR whose API sketch this record falsifies.

⚠ Deliberately **not** raised: `docs/adr/0051-grpc-fail-closed-server.md:20` (*"handlers read the
`actor` from the request body"*) — 0051 is banner-superseded by ADR-0094 and its Context is
historical narrative, so it is correctly out of scope. Recording it so the exclusion is an
adjudication rather than a silence.

**Concrete fix:** widen Task 17's sweep to
`grep -rnE '"actor"|"by"|httpcore\.Actor|(Claim|Complete|Reassign)Input\{' README.md SECURITY.md docs/ examples/`
and add ADR-0146 to Task 15's list of documents annotated in place (a one-line *"⚠ `Actor` was
removed from `CompleteInput` by ADR-0189"* footnote — annotate, do not rewrite).

---

### F11 — the plan's *"Tasks 1–4 and 6–7 are additive; each is independently green"* is false for Task 6, which depends on Tasks 3, 5 **and 7** — [MINOR]

**Bundle text attacked:** plan, *"## Task order"*: *"Tasks 1–4 and 6–7 are **additive**; each is
independently green. **Task 5 is the compile-breaking wave** …"*

**The net the author used:** a task is "additive" if it adds symbols. Task 6 does — but its **test**
does not.

**The net that finds more:** read each task's Step 1 and list the symbols the test needs.

**Observed:** Task 6 Step 1: *"POST the claim route with **no body at all** ⇒ 200 (**with an
authenticated resolver**)."* An authenticated resolver over a mounted route requires
`stdlib.WithRequestActor` (**Task 7**, which is ordered *after* Task 6) and the endpoint's resolver
parameter (**Task 5**, the breaking wave) and `CustomizeConfig.RequestActor` (**Task 3**).

**Correct value:** the additive-and-independently-green set is **Tasks 1–4 and 7**. Task 6 is
downstream of 3, 5 and 7 and must be re-ordered after 7 (or its test must be written against
`httpcore` directly rather than a mounted route).

**Concrete fix:** reorder to `1,2,3,4,7,5,6,8,…` and restate: *"Tasks 1–4 and 7 are additive; each
is independently green. Task 5 is the compile-breaking wave; Task 6 depends on 3, 5 and 7."*

---

### F12 — CONFIRMATION: every `file.go:NNN` anchor in all four documents resolves and says what the bundle says — [CONFIRMATION]

Round 1's anchors were clean; the revision's **new** anchors are too. Machine-checked at
`37d77a34` (54 anchors, python line extraction, no `grep`-by-content shortcut):

```
dto_test.go 47 var got httpcore.Actor · 57/68/79/151/161 JSON fixtures · 62/73/84/153 assertions   ✓ all
endpoints_test.go 405,422,466,485,531,560 httpcore.Actor{…} literals · 436,499,575 the 3 call sites ✓
{stdlib,gin,fiber}/groups.go 140/154/168 · 172/192/212 · 151/168/185  = the 9 Claim/Complete/Reassign ✓
gin_test.go 413,421,443,453 · gin_coverage_test.go 192,218,244 · fiber_test.go 563,585,592,615,624   ✓
stdlib errors_test.go 155/187 (keys) 158/190 (the two `!= http.StatusForbidden` guards)              ✓
stdlib_test.go 471 · coverage_test.go 92,126 · parity_test.go 518 · service/instance_test.go 1090,1128 ✓
endpoints.go 119,132,150 · dto.go 12-15 · authz/authz.go 38 · humantask/validate.go 24               ✓
runtime/processdriver.go 548 ("bypasses authorization entirely") · atrest/classification.go 203      ✓
stdlib/body.go 143 (plain json.NewDecoder) · 156 (decodeOptionalRequestBody) · groups.go 234 (its 1 caller) ✓
examples {production 264, sqlite 278, mysql 262}  ✓  (but see F7 — 274 is missing from the set)
```

⚠ The **one** anchor-adjacent defect is F7's *omission* (`production_wiring:274`), not a rotted
anchor. Anchor hygiene in this bundle remains the best in the repo; the failures are all in **scope**
and **nets**, never in the pointers.

---

### F13 — CONFIRMATION: seven restated/inherited figures re-derived independently, all exact — [CONFIRMATION]

Per the brief's item 7, each was re-run rather than read:

| restated claim | where | re-derived | ✓ |
|---|---|---|---|
| three self-asserted `authz.Actor` sites, all in `endpoints.go` | §2.1, ADR Context | `grep -rn "authz\.Actor{" transport/ \| grep -v _test` → 119, 132, 150 | ✓ |
| `httpcore/dto.go` declares exactly three Actor-bearing fields | §2.1 | `ClaimInput.Actor:44`, `CompleteInput.Actor:50`, `ReassignInput.By:66`; `PolicyRuleInput.Subject:76` and `RoleBindingInput.User:85` are policy **operands**, not the requester | ✓ |
| `CustomizeConfig` declares **eight** fields, no identity seam | §2.1 | BasePath, Wrap, InstanceMapper, MaxBodyBytes, BodyReadTimeout, Logger, TracerProvider, MeterProvider = 8 | ✓ |
| **nine** adapter call sites, all passing `cfg.InstanceMapper` last | §2.4, ADR D2 | 3 files × 3 verbs, verified line-by-line | ✓ |
| **14** httpcore test lines + **5** JSON fixtures (Task 5) | plan Task 5 | 5+9=14 compile-breaking; 57/68/79/151/161 = 5 fixtures; disjoint | ✓ |
| `WithActorResolver` "already exported three times" | ADR D2 | `service/options.go:99`, `runtime/task/service.go:113`, `processtest/harness.go:104` | ✓ |
| `WithCandidateResolveTimeout`, default **10 s**, non-positive disables | §3.3, plan Task 3 | `runtime/processdriver_options.go:72` and `runtime/task/service.go:132`, both `10 * time.Second`; both godocs say non-positive disables | ✓ |
| the audit's "37" = union of the two pin nets, omitting 9 + 2 | §2.6 | 37 + 9 + 2 = 48; round-1 report `audit-0189-counting.md:234` states "37 distinct lines, 9 files, 5 packages" | ✓ (arithmetic; but see F1 — 48 is still the wrong universe) |
| ADR-0183:69-76 calls the empty claimant "deliberately legal" and declines to supersede ADR-0148 | §3.3, ADR D3 | verbatim at `:71-76`; `humantask/validate.go:24` agrees | ✓ |
| CHANGELOG.md:20-38 is ADR-0186's entry shape; STABILITY.md has `### Request body caps (ADR-0186…)` | plan Task 17 | `CHANGELOG.md:20`, `STABILITY.md:52` | ✓ |

**The only inherited figure I could not clear** is §1's *"Nineteen of its 22 raw Criticals were
D3's; D1 had two"* — the source (`audit-0185core-adjudication.md:182`) says *"Nineteen … D1 and D2
have two each"*, i.e. 19+2+2 = **23** against a stated total of 22. Round-1 counting **F10** raised
this and the adjudication **explicitly rejected it as out of scope** — so this is a recorded
adjudication, not a silence, and I do not re-raise it. Noting only that the sentence still carries
an over-count into the justification for shipping D1 alone.

---
## Verdict

**2 CRITICAL · 4 MAJOR · 5 MINOR · 2 CONFIRMATION** (11 actionable).

Worktree restored and verified clean (`git status --porcelain` empty, `go build ./...` EXIT=0)
after the three ablations.

### The one-sentence version

**The revision fixed round 1's counting *method* and then applied it to round 1's *scope*.** §2.6
is a genuinely excellent member-set derivation — of the DTO-removal-plus-resolver-parameter change.
Owner decision D-3 (*every route group except `HealthRoutes` authenticates*) landed **after** that
derivation, the author's grid **explicitly exempted the blast-radius row from the pairwise pass**
(F4), and so the count was never re-run. Measured: the new scope breaks **186 further assertion
lines in 13 test files across 4 packages, disjoint from the 48**, seven of those files named
nowhere in the bundle (F1). The plan's *"planned red = the 23 runtime pins"* is really 209, and
three subagent tasks are under-scoped by roughly 10×.

⭐ The transferable lesson, one level up from round 1's: **a count is a DERIVED quantity of every
decision in the set, so it can never be exempt from the interaction pass.** Round 1 taught *"paste
the member set, not the total"*; round 2's failure is that the member set was pasted correctly and
then not re-derived when a decision changed underneath it.

### Enumerations in the bundle — claimed vs re-derived

| # | enumeration | claimed | re-derived | verdict |
|---|---|---|---|---|
| 1 | self-asserted `authz.Actor` sites in `transport/` | 3 | 3 (`endpoints.go:119,132,150`) | ✅ |
| 2 | Actor-bearing DTO fields in `dto.go` | 3 | 3 (`:44,:50,:66`) | ✅ |
| 3 | `CustomizeConfig` fields | 8 | 8 | ✅ |
| 4 | adapter call sites to the three task endpoints | 9 | 9, all passing `InstanceMapper` last | ✅ |
| 5 | **total blast radius** | **48 lines / 13 files / 6 pkg** | 48 for the DTO+signature change; **+186 lines / 13 files / 4 pkg** for D-3's route-group auth | ❌ **F1 (CRITICAL)** |
| 6 | compile-breaking sub-table | 23 / 5 files / 4 pkg | 23 / 5 / 4 | ✅ |
| 7 | runtime-only sub-table | 23 / **7 files** / **4 pkg** | 23 / **8 files** / **5 pkg** (its own rows) | ❌ **F2 (MAJOR)** |
| 8 | stale-documentation members | 2 lines / 1 file | 2 + `docs/adr/0146:12,43` | ⚠ **F10 (MINOR)** |
| 9 | Task 5's share | 28 (9+14+5) | 28 | ✅ |
| 10 | Tasks 12/13/14 pins | 17 (5 / 7 / 5) | 17 as listed; **46 / 55 / 57** lines actually break in those packages | ❌ **F1** |
| 11 | Task 15's share | 3 (parity 1 + service 2) | 3 | ✅ |
| 12 | route groups per adapter | 5 | 5, and all three adapters expose the identical set (`Instance`, `Message`, `Task`, `Admin`, `Health`); `Mount`/`MountHealth` symmetric too | ✅ |
| 13 | "every route group except `HealthRoutes`" | 4 groups | 4 groups × 3 adapters = 12 implementations | ✅ (scope correct; blast radius not — F1) |
| 14 | existing adapter option aliases | *"gin adds `WithMiddleware`"*, one asymmetry | **fiber exports `WithMiddleware` too**; **two** asymmetries | ❌ **F6 (MINOR)** — round-1 F9, never adjudicated |
| 15 | new adapter aliases (Task 7 title) | **six** | **nine** (3 symbols × 3 adapters) | ❌ **F5 (MINOR)** |
| 16 | example mount sites needing the resolver | 3 | **4** (`production_wiring:274`) | ❌ **F7 (MAJOR)** |
| 17 | wiring mains mounting `AdminRoutes` | 0 (*"must not"*) | **1**, already, on a separate mux — the very pattern D7 cites | ❌ **F7** |
| 18 | §5 test rows | 15 rows | 15 rows; **row 10 claims 9 cases, the plan delivers 1** | ⚠ **F9 (MINOR)** |
| 19 | author grid, blast radius (row G) | **37 lines / 6 pkg** | spec and ADR both say 48 and call 37 wrong | ❌ **F3 (MAJOR)** |
| 20 | author grid, *"G,H,I,J,K … none interacts"* | none | **C × G is the largest interaction in the revision** | ❌ **F4 (CRITICAL)** |
| 21 | *"Tasks 1–4 and 6–7 are additive; each independently green"* | 6 tasks | **Tasks 1–4 and 7**; Task 6 needs 3, 5 and 7 | ❌ **F11 (MINOR)** |
| 22 | *"planned red between Task 5 and Task 14"* | 23 runtime pins | **209** | ❌ **F1** |
| 23 | `WithActorResolver` prior exports | 3 | 3 (`service`, `runtime/task`, `processtest`) | ✅ |
| 24 | `WithCandidateResolveTimeout` default | 10 s, non-positive disables | 10 s at both of its 2 declaration sites; godocs agree | ✅ |
| 25 | round-1 audit totals | 48 findings / 7 Criticals; counting 11 (+2 INFO) | consistent with the four reports | ✅ (but F9 of them unadjudicated — F6) |
| 26 | ADR-0185's Criticals split | 19 D3 / 2 D1, of 22 | source itself sums to 23 | ⚠ round-1 F10, **explicitly rejected** — not re-raised |
| 27 | `file.go:NNN` anchors across all 4 documents | — | **54/54 resolve and say what the bundle says** | ✅ **F12** |
| 28 | *"BREAKING, in four ways"* | 4 | 4 as listed; ADR-0186's 400/413 narrowing is an unlisted 5th | ⚠ **F8 (MAJOR)** |
| 29 | *"the net is closed by construction"* (dto.go) | — | holds — for the DTO net only | ✅ |
| 30 | *"a stale body key is ignored, not rejected — all three adapters"* | 3 | 3 (gin conditional on a consumer global — already stated as residual 8) | ✅ |
| 31 | packages the delivery touches | 6 | 7 (`authz` gains `context.go`) — defensible, since §2.6 counts *changed existing lines*, not new files | ✅ w/ note |
| 32 | ordering residual scope (§3.8) | "the three task routes" | correct for those 3; **inverts** on the other 3 groups, unstated | ❌ **F8** |

### What I would block on

**F1 and F4 are blocking.** F4 is the process defect (the count was exempted from the interaction
pass); F1 is its measured consequence. Fixing F1 without F4 leaves the exemption in the grid's
method for the next revision.

F2, F3, F7 and F8 are all cheap document edits with measured correct values. F5, F6, F9, F10 and
F11 are one-line corrections. Nothing in this lens argues against the *design* — §2.6's method is
right and the anchors are the cleanest in the repo; the failure is entirely that the method was
run against a decision set that had already moved.
