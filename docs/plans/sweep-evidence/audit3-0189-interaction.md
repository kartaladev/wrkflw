# Audit 3 — INTERACTION lens — ADR-0189 bundle (post-removal of C, D, G)

Worktree: wt3-interaction @ 3e96e836
Started: in progress. Findings appended as derived.

### F1 — The plan prescribes writing a FALSE security guarantee into `SECURITY.md`: that `InstanceRoutes`/`MessageRoutes` "authenticate but do not authorize" — [CRITICAL]
**Pair:** REMOVED-(C) route-group authentication × survivor 8 (examples **and docs**)

**What survivor 8 assumes (C) hands it:** Task 13's `SECURITY.md` bullet
(`docs/plans/2026-08-25-request-actor-identity.md:455`) was written when (C) authenticated every
group but `HealthRoutes`. It prescribes: *"a 'Scope notes for embedders' entry stating that
`InstanceRoutes`/`MessageRoutes` **authenticate but do not authorize**."* That sentence is only
true if (C) ships.

**Why that assumption fails:** (C) was removed. Those two groups authenticate **nothing**. The
ADR's own Negative (`0189:313`) and spec §4 residual 2 (`spec:502`) both say they are *"entirely
unauthenticated"* and that `POST /instances`, `/signals`, `/messages` — **state-changing** — are
open to any caller. The plan therefore instructs an implementer to publish, in the repo's
**security** document, the exact opposite of what the ADR records as the shipped posture. This is
worse than a stale doc: a reader of `SECURITY.md` who believes it will place instance/message
routes inside a trust boundary they are not in.

**Evidence:**
```
$ grep -n 'authenticate but do not authorize' docs/plans/2026-08-25-request-actor-identity.md
455:      entry stating that `InstanceRoutes`/`MessageRoutes` **authenticate but do not authorize**.
$ grep -n 'remain entirely unauthenticated' docs/adr/0189-*.md
313:- ⚠ **`InstanceRoutes`, `MessageRoutes` and `AdminRoutes` remain entirely unauthenticated**, so
```
The author's removal grid cell **8 × C** exists and covers survivor 8 — but it addresses only
`examples/authenticated_tasks` and the three wiring mains. It never touches the **docs** half of
survivor 8 (`SECURITY.md`, `CHANGELOG.md`, `STABILITY.md`), which is where the false sentence
lives. ⇒ the grid cell is not wrong so much as **incomplete in exactly the half it did not
enumerate**.

**Concrete fix:** rewrite plan:455 to: *"a 'Scope notes for embedders' entry stating that ONLY the
three human-task verbs authenticate; `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` are
unauthenticated and MUST be composed behind the embedder's own guard (ADR-0095's
admin-by-composition pattern, `examples/production_wiring:273-275`), pending ADR-0190."* Add an
explicit plan step: *grep the whole bundle for any sentence asserting authentication on a group
other than the task verbs* — the 8×C cell must be extended to the docs half of survivor 8.

### F2 — The plan's Self-review table still assigns the two REMOVED decisions to live tasks, and the table is the plan's only spec→task completeness check — [CRITICAL]
**Pair:** REMOVED-(C) + REMOVED-(D) × survivor **plan structure** (the self-review mapping)

**What the mapping assumes the decision set hands it:** the self-review table is the plan's
machine-check that every spec §3 decision has an owning task. It was written against the 9-decision
spec, where §3.6 was *group authentication* and §3.7 was *the admin role gate*.

**Why that assumption fails:** the re-cut **renumbered what those section labels mean** — spec §3.6
is now *"the claim route accepts an ABSENT body"* and §3.7 is now *"Examples and documentation"* —
and deleted (C) and (D) entirely. The table was not updated. It now contains, verbatim:

```
| §3.6 group authentication, Health exempt, placement asymmetry | 8, 9–11 |
| §3.7 opt-in admin role gate                                   | 8, 9–11 |
```

alongside the *correct* rows for the same section numbers (`§3.6 optional claim body | 6`,
`§3.7 examples, docs, … | 11, 12, 13`). Tasks 8–11 in the re-cut plan are **per-adapter test
migration**; they contain no group authentication and no role gate. An implementer working the plan
top-down is fine; one working the self-review table (which is what it exists for — closing gaps) is
directed to implement (C) and (D) inside tasks 8–11, i.e. to re-introduce the exact scope whose
removal is the whole point of the re-cut, and to re-introduce the ADR-0095 default-deny
contradiction that removal-grid cell `1–8 × D` claims *"dissolves"*.

**Evidence:** `docs/plans/2026-08-25-request-actor-identity.md:504-505` (quoted above), against
`:395-441` (Tasks 8/9/10 = "per-adapter test migration") and `:518` ("Tasks 8–11 of the round-2
plan … are **deleted**"). The plan **contradicts itself in the same document**: :518 says those
tasks are deleted, :504-505 says tasks 8–11 own them.

The removal grid has **no cell for this at all**. Its survivor list is *1 seam · 2 parameter · 3
refusal · 4 arms-first · 5 attributes · 6 optional body · 7 timeout · 8 examples/docs* — the plan's
own internal consistency is not a survivor it enumerated, so no cell could catch it. That is the
grid's structural blind spot: **it derives survivor×removed over the DESIGN, and the plan is not in
the survivor set.**

**Concrete fix:** delete both rows; add a row `§3.6 optional claim body | 6` (already present) and
verify each of spec §3.1–§3.7 appears **exactly once** in the left column with its post-cut title.
Add to the grid a ninth survivor — *"the plan's own self-review/traceability table"* — and derive
it against every removal, since a removal necessarily orphans rows there.

### F3 — Off-by-one in the same self-review paragraph: "§5 row 7" is the co-match test, not the attributes-reach-the-request test (row 8) — [MINOR]
**Pair:** (b)/(4) arms-first ordering × survivor 5 (attributes) — the two rows the cut re-numbered

**Evidence:** spec §5 row 7 is *"the two new arms co-match each other"*; row 8 is *"attributes reach
`service.ClaimTaskRequest.Actor.Attributes`"*. The plan's closing paragraph says *"§5 row 7
(attributes reach `service.ClaimTaskRequest`) is asserted in Task 4"*. An inherited citation
restated after the row list changed length — this repo's documented oldest failure mode.

**Concrete fix:** `§5 row 7` → `§5 row 8`.

### F4 — Spec §2.9 still asserts the exposure IS CLOSED by this record; §4 residual 1 and the ADR Negative say the mitigation left with (C). The spec contradicts itself on the single load-bearing removal consequence — [CRITICAL]
**Pair:** KEPT survivor 5 (`Attributes` flow) × REMOVED-(C) — the grid's own "load-bearing removal consequence" cell

**What §2.9 assumes (C) hands it:** §2.9 is the *executed premise* section that establishes the
attribute exposure is pre-existing. Its concluding bullet reads (spec:322-324):

> - The unauthenticated read exposure **pre-exists this bundle**; **§3.6 closes it as a side effect
>   of authenticating `InstanceRoutes`**, and the *pre-existing* half is filed as its own backlog item.

**Why that assumption fails:** two ways at once.
1. (C) is removed — **nothing** in this record closes it. §4 residual 1 (spec:493-499) and the
   ADR's first Negative (`0189:274-283`) both state the mitigation "left with it". The document
   asserts P in §2 and ¬P in §4.
2. The cross-reference is *doubly* stale: post-cut **§3.6 is "the claim route accepts an ABSENT
   body"**. So the sentence points a reader at a section about JSON decoding as the closure of a
   personal-data exposure.

This matters more than an ordinary stale sentence because of *where* it sits. The removal grid's
cell **5 × C** is the one it labels **"THE LOAD-BEARING REMOVAL CONSEQUENCE"**, and its prescribed
resolution is *"the ADR must carry a Negative…"*. The author executed that resolution **in the ADR
and in spec §4 only**, and left the premise section that the Negative is derived *from* saying the
opposite. Grid cell 5×C is therefore **half-applied**, and the half that was missed is the half a
reader hits first.

**Evidence:**
```
$ sed -n '322,324p' docs/specs/2026-08-25-request-actor-identity.md
- The unauthenticated read exposure **pre-exists this bundle**; §3.6 closes it as a side effect of
  authenticating `InstanceRoutes`, and the *pre-existing* half is filed as its own backlog item.
$ sed -n '493,496p' docs/specs/2026-08-25-request-actor-identity.md
1. ⚠⚠ **Actor attributes reach an UNAUTHENTICATED read surface.** ... Round 2's revision closed
   this by authenticating `InstanceRoutes`; **that decision was split to ADR-0190, so the
   mitigation left with it.**
```

**Concrete fix:** replace the §2.9 bullet with: *"The unauthenticated read exposure pre-exists this
bundle. **This record does not close it** — the decision that would have (authenticating
`InstanceRoutes`) is ADR-0190's. The population-rate qualifier in §4 residual 1 applies: the
pre-existing channel needs an opt-in `humantask.ActorResolver`; the new one is fed by
`RequestActorFunc`, which every HTTP consumer configures."* Then grep the spec for every other
`§3.6`/`§3.7` cross-reference written under the 9-decision numbering (F5).

### F5 — §2.5's cross-reference `§3.5` points at the attribute guard; the claim it supports is about the demo wiring mains (now §3.7) — [MINOR]
**Pair:** REMOVED-(C)/(D) renumbering × survivor 8 (examples)

**Evidence:** spec:186-189 — *"The real exposure is narrower — a reader who `curl`s the mounted task
route gets 401 — which is what **§3.5** addresses"*. §3.5 is `Attributes` flow behind the round-trip
guard; the section that addresses a curl-er getting 401 on the demo mains is **§3.7** (*"The three
wiring mains take a constant `demo-user` actor"*). Another artifact of section renumbering across
the two re-cuts.

**Concrete fix:** `§3.5` → `§3.7` at spec:188.

### F6 — The dimension rule's own justification does not discriminate: the kiosk shape it PRESERVES has the identical attribute fail-open the rule claims to close — [CRITICAL]
**Pair:** (a) the dimension-based refusal rule × KEPT survivor 5 (`Attributes` flow + the fail-open it closes)

**What (a) assumes survivor 5 hands it:** Decision 3 gives two reasons for refusing `Actor{}`. The
second is stated as decisive: *"because `Actor{}` carries no attributes, a deny-list
`actor.Attributes.*` predicate **ALLOWs** ⇒ round 2's fix **reopened the fail-open Decision 5 exists
to close**."* That argument only works if "carries no attributes" is a property `Actor{}` has and
admitted actors do not.

**Why that assumption fails:** it is a property of **`len(Attributes) == 0`**, not of
`dimensions == 0`. The rule admits three shapes with zero attributes — the kiosk claimant it
deliberately preserves, every ordinary role-only actor, and every ID-only actor — and all of them
ALLOW the deny-list predicate exactly as `Actor{}` does. Executed:

```
PROBE A zero Actor{} (REFUSED 401 by the dimension rule)         deny-list=ALLOW  allow-list=DENY
PROBE B kiosk {ID:"",Roles:[kiosk]} (ADMITTED by the rule)       deny-list=ALLOW  allow-list=DENY
PROBE C ordinary {ID:alice,Roles:[manager]} (ADMITTED)           deny-list=ALLOW  allow-list=DENY
PROBE D attrs-only {Attributes:{team:fin}} (ADMITTED, only dim)  deny-list=ALLOW  allow-list=DENY
PROBE E blocked {ID:alice,Attributes:{status:blocked}}           deny-list=DENY   allow-list=DENY
--- PASS: TestZZProbeDimensionRuleVsAttributeFailOpen (0.00s)
ok github.com/kartaladev/wrkflw/authz 0.432s
```
(throwaway `authz/zzprobe_dimension_test.go`, `authz.RoleAuthorizer{}.Authorize` with
`spec.Attribute = actor.Attributes.status != "blocked"`, deleted after the run.)

⇒ taken seriously, the stated reason would require refusing the **kiosk** shape too — which is the
exact shape round 1 was reverted for refusing. The two decisions the re-cut made in this area are
therefore justified by **mutually incompatible readings of the same fail-open**. The rule is still
defensible on its *first* reason alone (a durably unattributable audit record from
`actor, _ := authenticate(r)`) — but note that reason does not discriminate either: the kiosk shape
also has `ID == ""` and is also invisible to `AssignedTo("")`, the hazard the ADR cites.

**Why the removal grid could not catch it:** the grid has no `(a) × 5` cell at all. Its axes are
*survivor × removed*; **(a) is a CHANGE to a survivor, not a removal**, so a change-to-survivor ×
survivor pair falls outside the grid's shape entirely. That is a second structural blind spot,
distinct from F2's.

**Concrete fix:** delete the attribute-fail-open clause from Decision 3's and §3.3's justification
and rest the rule on the audit-record argument alone, stating plainly: *"this rule does not narrow
the attribute fail-open — measured, the kiosk shape and every role-only actor ALLOW a deny-list
`actor.Attributes.*` predicate identically to `Actor{}`; §3.5's residual 6 governs."* Then reconcile
with residual 6 (*"5 of 6 shapes still ALLOW"*), which is the honest version of the same
measurement and currently sits three sections away from the sentence it refutes.

### F7 — Decision 6 and §3.6 assert a malformed claim returns **400**; residual 8, the ADR's own Negative and test row 14 assert **401**. The bundle pins both, on the one route fix (e) changes — [CRITICAL]
**Pair:** (e) the optional claim body admitted to swallow decode errors × survivor 6's *"unchanged from today, not a regression"* ordering residual

**What survivor 6's residual assumes (e) hands it:** the residual sentence — in ADR Decision 6, in
the ADR's Negative, and in spec §3.6 — reads: *"ADR-0186's measured read window … and its 400/413
responses therefore remain reachable without a credential, and **a malformed-JSON claim still
returns 400 before 401. This is unchanged from today, not a regression.**"* It was written when
`ClaimInput` had a **required** decode, so the decode error fired before the resolver.

**Why that assumption fails:** (e) makes the claim route's decode **optional and error-swallowing**
— that is the whole point of the fix, because `ClaimInput` becomes zero-field. Executed against the
repo's existing optional decoder (`transport/http/stdlib/body.go:156`, the helper Task 6 reuses):

```
PROBE absent body    proceed=true  recorderStatus=200
PROBE malformed      proceed=true  recorderStatus=200
PROBE garbage        proceed=true  recorderStatus=200
PROBE valid but old  proceed=true  recorderStatus=200
--- PASS: TestZZProbeOptionalDecodeSwallowsMalformed (0.00s)
ok github.com/kartaladev/wrkflw/transport/http/stdlib 0.490s
```
`proceed=true` for a malformed body ⇒ the handler continues to `resolveRequestActor` ⇒ **401**, not
400. The sentence is false for the claim route specifically — the very route it names as its
example. It remains true for complete/reassign, which keep required decode.

The bundle already knows this **one bullet later**: ADR Negative *"the optional claim decoder
swallows every decode error, so a malformed claim answers 401 rather than 400. That IS a change"*,
spec §4 residual 8, and §5 test row 14 (*"a malformed claim body ⇒ 401"*). So an implementer has
two contradictory pins for one behaviour and the plan prescribes a test for one of them.

**Why the removal grid could not catch it:** cell **6 × C** says *"no interaction. The optional
claim body is a decode concern on one route."* Wrong twice. (i) Under (C) plus (G) — pre-decode
resolution for the newly-authenticated groups — an unauthenticated malformed request 401'd at the
gate on 23 routes, so "malformed ⇒ 401" would have been the *uniform* behaviour and the residual
sentence would have needed rewriting anyway. (ii) With C and G gone, the claim route becomes the
**only** route in the entire transport where a malformed body yields 401 instead of 400 — a new
one-route anomaly created *by the removal*, which is exactly the category the grid exists for.
⇒ **cell 6 × C is one of the wrong "no interaction" cells.**

**Concrete fix:** in ADR Decision 6, the ADR Negative and spec §3.6, replace *"a malformed-JSON
claim still returns 400 before 401 … unchanged from today"* with: *"on **complete** and
**reassign**, whose bodies stay required, a malformed body still returns 400 before 401 — unchanged.
On **claim** the optional decoder swallows the decode error, so a malformed body reaches the
resolver and answers **401**: a deliberate change, pinned by §5 row 14. 413 is unaffected on all
three — the oversize sentinel comes from the body **reader**, not the decoder
(`stdlib/body.go:156-168`)."*

### F8 — The 401 response body is unspecified, so the neighbouring 4xx convention echoes the consumer's resolver error verbatim to an anonymous caller — [MAJOR]
**Pair:** (3) refusal rules — the resolver-reported-`ErrUnauthenticated` pass-through × (4) arms-first in `ClassifyError`

**What (4) assumes (3) hands it:** the new 401 arm must be given an `ErrorBody`. Every existing 4xx
arm in `ClassifyError` sets `Message: err.Error()` (`httpcore/errors.go:39,41,43,79,85`); the two
arms that deliberately do **not** (413 and the 500 default) each carry an explicit comment saying
why. The bundle specifies the 503 body (*"5xx must never carry the raw error"*, plan Task 2) and
**never specifies the 401 body at all**.

**Why that assumption fails:** (3)'s prescribed implementation passes the **consumer's** error
through unchanged — `case errors.Is(err, ErrUnauthenticated): return authz.Actor{}, err` (plan
Task 4 Step 3). A consumer resolver returning
`fmt.Errorf("%w: JWT signature invalid for kid=%s, subject %s", httpcore.ErrUnauthenticated, kid, sub)`
therefore reaches `ClassifyError` with that whole string, and an implementer copying the arm above
it emits it in the 401 body — to an **unauthenticated** caller, on the one status code an attacker
probes. Note 401 is also **below** the `status >= 500` log threshold in every adapter's `writeErr`
(`stdlib/write.go:32`), so unlike the 503 path there is no compensating operator visibility either.

**Evidence:** `transport/http/httpcore/errors.go:34-88` (arm bodies), `:56-59` (the 413 arm's
explicit static-body rationale: *"echoing it tells an attacker exactly what to stay under"*),
`transport/http/stdlib/write.go:30-36` (5xx-only logging). The bundle's own §5 row 3 asserts only
the status for the 401 cases.

**Concrete fix:** specify the 401 arm as `ErrorBody{Error: "unauthenticated"}` with a **static or
empty** `Message`, carrying the 413 arm's rationale comment; add an assertion
`assert.Empty(t, body.Message)` to §5 row 3 and to plan Task 2's 401 cases, mirroring the 503 case
that already has one. If diagnosability matters, log the wrapped cause at `Warn` in `writeErr` for
401 rather than putting it on the wire.

### F9 — Fix (e) breaks two existing tests that no task owns and §2.6's member set does not contain, because the member set's ablation modelled only two of the bundle's THREE behavioural changes — [CRITICAL]
**Pair:** (e) the optional claim decode × §2.6's blast-radius member set (the count the grid promised to re-execute)

**What the member set assumes (e) hands it:** §2.6 derives 48 lines from a compile+run ablation that
models *"both changes"* — DTO field removal and the endpoint signature change. §2.6's own headline
lesson is that round 1 failed because *"the ablation modelled one of the two breaking changes"*.

**Why that assumption fails:** the bundle now has a **third** behavioural change — (e), the claim
route's decode going optional — and the ablation cannot see it, because it changes neither a type
nor a signature. It changes only a status code, at runtime, in tests that compile fine. Two
existing tests assert the old code:

| file:line | test | today | after (e) |
|---|---|---|---|
| `transport/http/stdlib/coverage_test.go:136` (assert `:157`) | `TestTaskRoutes_BadJSON` — malformed body to `/tasks/{id}/claim`, mounted `stdlib.Mount(mux, svc)` with **no resolver** | 400 | **401** |
| `transport/http/gin/gin_coverage_test.go:173` (assert `:184`) | `TestTaskRoutes_Claim_BadJSON` — posts `"not-json"` to `/tasks/tok/claim` on a bare `TaskRoutes{Svc: svc}.Customize(r)` | 400 | **401** |

Neither line appears in §2.6's tables (`stdlib/coverage_test.go` contributes **92, 126**;
`gin/gin_coverage_test.go` contributes **192, 218, 244** — those are the `"actor"`/`"by"` keys in
the `_ErrorPath` tests, verified by reading 170-250). Neither is in Task 8's *"5 runtime pins"* or
Task 9's *"7 runtime pins"*. ⇒ the plan's per-adapter agents will hit two failures their brief says
should not exist, inside the window the plan labels *"planned RED … do not 'fix' it by restoring
the fields"* — the most dangerous possible place for an unbudgeted failure.

The corresponding **complete** and **reassign** BadJSON tests are safe: their bodies stay required,
so the 400 fires before the resolver. That asymmetry is itself the tell that (e) is scoped to one
route and the member set was never re-derived against it.

**Evidence:** `sed -n '136,159p' transport/http/stdlib/coverage_test.go`;
`sed -n '173,187p' transport/http/gin/gin_coverage_test.go`; §2.6 tables at
`docs/specs/2026-08-25-request-actor-identity.md:223-254`; plan `:397,:412`.

**Concrete fix:** add both lines to §2.6's runtime-only table (**50 lines / 13 files / 6 packages**,
or state the new total after re-execution), assign `stdlib/coverage_test.go:157` to Task 8 and
`gin/gin_coverage_test.go:184` to Task 9 with the instruction *"rewrite to assert 401 and add a
sibling case with a resolver installed asserting the malformed body is ignored and the claim
succeeds"*, and — the transferable part — **change §2.6's ablation recipe to model all three
changes**, adding a run of the full transport suite with the claim route's decode made optional.
Also add a fiber row if Task 6's fiber helper changes any existing fiber assertion.

### F10 — Fix (e) silently reclassifies the claim route from a "propagating" to a "discarding" decode site, falsifying a machine-asserted enumeration in two adapters that the bundle never names — [MAJOR]
**Pair:** (e) the optional claim decode × ADR-0186's decode-site enumeration guard

**What (e) assumes it is handed:** Task 6 says only *"gin and fiber need an equivalent that treats
an absent/empty body as the zero value but still honours the size cap"*. It assumes the repo has no
standing statement about **how many** discarding decode sites exist.

**Why that assumption fails:** it has exactly that, asserted in code:
`transport/http/fiber/bodylimit_test.go:540` —
`require.Len(t, cases, 13, "13 decode sites: 12 propagating + 1 discarding")`, with the same split
restated in its doc comment at `:495-503` (*"the twelve that propagate their bind error plus the
one (resolve-incident) that discards it"*), at `:340`, and again in
`transport/http/gin/gin_bodycap_test.go:232-241` (*"the discarding decode site — POST
/admin/instances/{id}/incidents/{incidentID}/resolve … the twelve propagating sites"*). After (e)
the true split is **11 propagating + 2 discarding**. The `13` total still holds, so **the guard
stays green while its own message becomes false** — the ADR-0187 failure mode (*a guard blind to the
category of claim it was built to police*) recurring verbatim.

Two upsides worth stating: `TestEveryDecodeSiteIsBounded` **is** a live guard that Task 6's gin and
fiber helpers must keep 413 on the claim route, and `stdlib`'s existing `decodeOptionalRequestBody`
already preserves 413 via `requestBodyReader` (read: the oversize sentinel comes from the reader,
before the decoder) — so the ADR's *"413 stays reachable"* claim survives. But the bundle names
none of these three files.

**Concrete fix:** add to Task 6 — *"update the propagating/discarding split to `11 propagating + 2
discarding` at `fiber/bodylimit_test.go:540` and its comments at `:340`, `:495-503`, and at
`gin/gin_bodycap_test.go:232-241`; verify `TestEveryDecodeSiteIsBounded` stays green, which is the
guard that the new gin/fiber optional helpers still 413."* Add `fiber/bodylimit_test.go` and
`gin/gin_bodycap_test.go` to §2.6's runtime-only table.

### F11 — The round-trip guard is defeated by exactly the input class it was written for: it measures depth at a DIFFERENT nesting origin than the store does, leaving a brick window (wrapper depth + 1) values wide — [CRITICAL]
**Pair:** (b) the round-trip guard × KEPT survivor 5's own persistence premise (§2.9: attributes land in `claim.actor`, in `candidates` as `[]authz.Actor`, and in `wrkflw_instances.snapshot`)

**What (b) assumes survivor 5 hands it:** the guard marshals **`a.Attributes` alone** and unmarshals
into `map[string]any` (plan Task 4 Step 3). That is only a valid proxy for the store if the store
also decodes the attributes as a top-level document. Survivor 5's own evidence says it does not: the
store marshals the **whole `authz.Actor`** (`humantask_store.go:161`, `[]authz.Actor`), and the
snapshot nests the claim inside an entire instance document.

**Why that assumption fails:** `encoding/json`'s decoder limit is a **total nesting** budget, so
every enclosing level the store adds shrinks the depth available to the attributes — but the guard
never sees those levels and spends the whole budget on the attributes alone. Executed:

```
PROBE nest= 9997  guard=OK     store=OK     storeErr=<nil>
PROBE nest= 9998  guard=OK     store=FAIL   storeErr=invalid character '{' exceeded max depth
PROBE nest= 9999  guard=FAIL   store=FAIL   storeErr=invalid character '{' exceeded max depth
--- PASS: TestZZProbeGuardVsStoreDepthBoundary (0.07s)
```
`store` here is `json.Marshal(authz.Actor{...})` → `json.Unmarshal(&authz.Actor{})`, i.e. the read
side the ADR quotes. **At depth 9998 the guard PASSES and the store fails with the ADR's own
verbatim error string** — *"fail-open at write, fail-closed forever at read"*, reproduced by the
guard written to stop it, which is word-for-word the sentence Decision 5 uses to condemn round 2's
`json.Marshal`. The correction repeats the defect at a smaller offset.

The window is not one value. Parametrised over the store's enclosing depth `k`:

```
PROBE wrapperDepth= 0  brickable depths that PASS the guard: 1   (range 9998..9998)
PROBE wrapperDepth= 1  brickable depths that PASS the guard: 2   (range 9997..9998)
PROBE wrapperDepth= 5  brickable depths that PASS the guard: 6   (range 9993..9998)
PROBE wrapperDepth=20  brickable depths that PASS the guard: 21  (range 9978..9998)
--- PASS: TestZZProbeBrickWindowWidth (3.17s)
```
⇒ **width = k + 1 exactly.** `candidates` is `[]authz.Actor`, so k ≥ 1 there; `claim.actor` inside
the instance snapshot is many levels deeper, so the exploitable window for the snapshot path is tens
of values wide. And a **size** bound does not help: 9998 nested `{"n":` pairs is ~50 KB, comfortably
under any plausible cap.

Note this is reachable by an unauthenticated party only if the consumer's resolver derives
attributes from caller-controlled input (a JWT claim set, a header) — but that is precisely the
shape `examples/authenticated_tasks` will teach, and the resolver is the *only* attribute source
this record creates.

**Why the removal grid could not catch it:** (b) is a change to a survivor, and the grid derives
only survivor × removed. Same shape as F6.

**Concrete fix:** make the guard round-trip **at the persistence origin, not the attribute origin** —
marshal the whole `authz.Actor` and unmarshal into an `authz.Actor`, then subtract an explicit
headroom for the enclosing document:
```go
blob, err := json.Marshal(a)                       // the WHOLE actor
… size check …
var back authz.Actor
if err := json.Unmarshal(blob, &back); err != nil { … 503 … }
```
plus a declared, tested **maximum attribute nesting depth** well below 10000 (e.g. 64) checked by a
walk, since headroom-by-subtraction is unknowable for the snapshot path. Add to §5 a row: *"an
attribute at the guard-passing / store-bricking boundary depth ⇒ 503, and the persisted record still
reads back"* — the current row 10 uses **20000**, a depth so far past the boundary that it cannot
detect this class, which is the same "fixture from the half that works" error §3.5 records as its
own recurring lesson.

### F12 — The size bound (c) has no value, no configuration seam, and appears in no design document — only as an undefined identifier in the plan's code block — [MAJOR]
**Pair:** (c) the size bound × (2) `CustomizeConfig` as the consumer's only configuration seam

**What (c) assumes (2) hands it:** the bound is enforced as `if len(blob) > maxActorAttributeBytes`
(plan `:270-271`). For that to be a shippable library decision, either a value must be recorded or a
config field must exist.

**Why that assumption fails:** neither does. Grepped across the whole bundle, `maxActorAttributeBytes`
occurs **twice, both inside the same plan code block**, and is never defined. ADR Decision 5 says
only *"additionally bounds the marshalled size"*; spec §3.5 says *"bound the marshalled size"*; §5
row 11 prescribes *"an oversize attribute payload ⇒ 503"* — a test that cannot be written without
the number. Task 3, which produces the config fields, produces `RequestActor` and
`RequestActorTimeout` and **not** a size field, so the bound is a hard-coded constant on a payload
supplied entirely by the **consumer's own resolver**.

Consequences the bundle never states: a consumer whose identity provider legitimately returns a
large claim set gets a **503 on every task request**, with an empty response body (5xx carries no
message), unfixable from configuration — a library-ergonomics failure of the kind CLAUDE.md resolves
in the library's favour. Contrast `MaxBodyBytes`, which ADR-0186 made configurable with a documented
`0 = disabled` convention, and which does **not** bound this payload at all (the actor comes from
the resolver, not the body).

**Concrete fix:** record the value in the ADR with its derivation, and add
`CustomizeConfig.MaxActorAttributeBytes` + `WithMaxActorAttributeBytes` following ADR-0186's
convention (seeded in the **struct literal** so an explicit `0 = disabled` stays distinguishable —
the same rule Task 3 already applies to `RequestActorTimeout`). Then §5 row 11 becomes writable, and
add the alias count to F13's correction.

### F13 — `WithRequestActorTimeout` exists only in the plan; the ADR enumerates "the three REQUIRED aliases" while the plan produces six — [MAJOR]
**Pair:** (d) the resolver timeout × (2) the resolver-as-parameter decision and its alias enumeration

**What (2) assumes (d) hands it:** ADR Decision 2 fixes the new public surface as
```
type RequestActorFunc func(context.Context) (authz.Actor, error)
func WithRequestActor[R any](fn RequestActorFunc) CustomizeOption[R]
// + the three REQUIRED non-generic aliases: stdlib. / gin. / fiber.WithRequestActor
```
i.e. one generic option and three aliases.

**Why that assumption fails:** (d) adds a whole second option. Plan Task 3 produces
`CustomizeConfig.RequestActorTimeout` and `httpcore.WithRequestActorTimeout[R]`; Task 7 produces
*"`WithRequestActor` **and** `WithRequestActorTimeout` in each — **six aliases**, two per adapter"*.
Grepped: `WithRequestActorTimeout` and `RequestActorTimeout` appear **zero times in the ADR and zero
times in the spec**. So five new exported symbols on a public root package are introduced by the
plan alone, and the ADR — the record of what was decided — enumerates half the surface.

This is doubly pointed because Task 7's own warning is *"Round 2's counting lens found the round-2
task titled 'six' while producing nine … **Six is now correct** — verify by counting the funcs you
actually add"*: the bundle re-derived the plan's count and never re-derived the **ADR's**, which is
the authority the plan is checked against.

**Concrete fix:** amend ADR Decision 2's code block to name both options and *"six REQUIRED
non-generic aliases, two per adapter"*, and add the timeout option, its default (10 s), its
non-positive-disables convention and the ctx-honouring caveat to spec §3.3 as API rather than as
prose. If F12 is accepted, the count becomes nine aliases and every one of these enumerations moves
again — state the number in exactly one place and cite it from the others.

### F14 — The resolver timeout and the body-read timeout are never composed: the three task routes gain a 40 s worst-case hold, and on stdlib the resolver runs in a window ADR-0186 deliberately left with NO read deadline — [MAJOR]
**Pair:** (d) the resolver timeout × ADR-0186's `BodyReadTimeout` (inherited, unchanged by this bundle)

**What (d) assumes ADR-0186 hands it:** §3.3 adopts 10 s *"mirroring `WithCandidateResolveTimeout`"*
and reasons about the resolver in isolation. Re-derived and **confirmed** as a faithful precedent:
`runtime/task/service.go:132` `defaultCandidateResolveTimeout = 10 * time.Second`, `:322`
`if s.candidateResolveTimeout <= 0 { … }` (non-positive disables), and its godoc carries the
ctx-honouring hedge the spec correctly restates. So (d)'s premise is sound; its **composition** is
never derived.

**Why that assumption fails:** the resolver runs **after** the capped body read, which the bundle
itself states. `defaultBodyReadTimeout = 30 * time.Second` (`httpcore/seam.go:95`),
`defaultMaxBodyBytes` 1 MiB. Sequentially: **30 s + 10 s = 40 s worst-case handler hold per task
request**, up from 30 s today. That number appears nowhere in the ADR, spec or plan, and the removal
grid's cell **7 × C** discusses only that the blast radius *shrinks* from 26 routes to 3 — it
compares the new hazard to a bigger version of itself and never compares it to **today**.

Worse on stdlib: `armBodyReadDeadline` is armed only while the capped read runs and is then
**cleared outright** (`stdlib/body.go:56, defer clearDeadline()` → `SetReadDeadline(time.Time{})`),
and body.go's own comment records that arming **overwrites** net/http's `Server.ReadTimeout`
whole-request deadline. ⇒ by default the resolver executes in a window with **no connection read
deadline at all**, its only bound being (d)'s 10 s — which §3.3 and residual 5 both admit does not
bind a ctx-ignoring resolver. The ADR names the unbounded-hang hazard for **fiber only**
(*"`c.Context()` is `context.Background()`"*); on stdlib the outer backstop is gone for a different
reason and the bundle does not say so.

**Concrete fix:** add to the ADR's Negatives: *"worst-case task-route hold rises from 30 s to 40 s
(1 MiB / 30 s body read, then a 10 s resolver); a consumer who needs the old ceiling lowers
`BodyReadTimeout` or `RequestActorTimeout`."* Add to `WithRequestActorTimeout`'s godoc that on stdlib
the resolver runs after `BodyReadTimeout`'s deadline has been cleared, so this bound and the
consumer's `Server.WriteTimeout` are the only limits. Extend residual 5 from *"a ctx-ignoring
resolver runs past the bound"* to *"…and on all three adapters nothing else stops it"*.

### F15 — The ADR and spec still instruct that the demo mains "do NOT mount `AdminRoutes`"; the plan says that sentence is false about existing code, and it is — the ADR's own Context cites the mount it denies — [MAJOR]
**Pair:** REMOVED-(D) the admin role gate × survivor 8 (the three wiring mains)

**What survivor 8 assumes (D) hands it:** ADR Decision 7 and spec §3.7 close the demo-actor
paragraph with *"…and the mains do **not** mount `AdminRoutes`."* Under (C)+(D) that was a
meaningful prescription: a constant `demo-user` actor plus route-group authentication plus a role
gate would have let the demo identity reach admin endpoints, so keeping admin unmounted was the
mitigation.

**Why that assumption fails:** (C) and (D) are gone — the demo actor now reaches only the three task
verbs — so the prescription is unnecessary; and it was never true anyway.
`examples/production_wiring/main.go:274` mounts `stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, …)`
behind `requireAdminToken` at `:275`. Plan Task 12 Step 2 has already been corrected —
*"⚠ **Do NOT touch `production_wiring:273-275`** … A round-1 plan sentence claimed the mains 'must
not mount `AdminRoutes`' — **that was false about existing code**"* — but the correction was applied
to the plan **only**. The ADR contradicts itself in two places: its Context says
*"`examples/production_wiring:273-275` composes a fail-closed token guard in front of it"*, and its
Decision 7 says the mains do not mount it.

An implementer following the ADR rather than the plan would **delete a working fail-closed admin
guard** — removing ADR-0095's admin-by-composition exemplar from the repo's own reference wiring, in
a bundle whose stated posture is that ADR-0095 is untouched.

**Evidence:** `sed -n '267,276p' examples/production_wiring/main.go`; `grep -n AdminRoutes
examples/*/main.go` → hits only in `production_wiring` (`:67, :267, :274`), so the sentence is false
for one main of three and vacuous for the other two.

**Concrete fix:** in ADR Decision 7 and spec §3.7 replace *"and the mains do not mount
`AdminRoutes`"* with *"`production_wiring` continues to mount `AdminRoutes` behind its
`requireAdminToken` guard (`main.go:273-275`) — ADR-0095's admin-by-composition posture, untouched
here; the demo actor is scoped to the task verbs and never reaches it."*

### F16 — Stating the Negative is not a mitigation, and the ONE mitigation a consumer could act on appears in no deliverable: nothing tells them to keep personal data out of `Actor.Attributes` until ADR-0190 — [MAJOR]
**Pair:** KEPT survivor 5 (`Attributes` flow) × REMOVED-(C) — the grid's self-declared "load-bearing removal consequence"

**What the grid's resolution assumes:** cell **5 × C** resolves the exposure by requiring *"the ADR
must carry a **Negative** stating that actor attributes supplied through `WithRequestActor` are
rendered to unauthenticated readers … the mitigation is **deferred to ADR-0190**"* plus a backlog
item. It assumes that a stated Negative discharges the obligation.

**Why that assumption fails — the exposure end to end, verified:** `RequestActorFunc` →
`resolveRequestActor` → `service.ClaimTaskRequest.Actor` → `humantask.Claim.Actor` → rendered by
`runtime/view/instance_actionable.go:32-36`, where `Claim *humantask.Claim` and
`Candidates []authz.Actor` are documented as *"rendered verbatim as {id, roles, attributes}
(ADR-0147)"* → returned by `httpcore.GetActionableView` and `GetInstanceSnapshot`
(`httpcore/endpoints.go:60,73`) → mounted by `InstanceRoutes.Customize` at
`GET /instances/{id}/snapshot` and `/actionable` (`stdlib/groups.go:61,71`) with **no
authorization**. Confirmed also that the *default* `NewInstanceView` (`httpcore/view.go:23-33`) does
**not** carry tasks, so "two sibling routes" is the correct count, not an undercount.

The Negative is written and it is accurate. But a Negative is a statement to the *reader of the
ADR*; the population at risk is the **consumer configuring `WithRequestActor`**, and the only action
available to them — *do not put personal data in `Actor.Attributes` until route authentication
lands* — is written **nowhere**: not in `WithRequestActor`'s prescribed godoc (plan Task 3, Task 7),
not in `SECURITY.md`'s prescribed content (plan Task 13, which instead prescribes the *false*
reassurance F1 documents), not in `CHANGELOG.md`'s, not in `examples/authenticated_tasks`, whose
whole purpose is to model a resolver and which will therefore be the copy-paste template for
populating attributes.

⇒ the delivery widens a personal-data channel, removes the mitigation, documents the cost in the
record least likely to be read by the affected party, and prescribes a security document that says
the affected routes are protected. The grid's *"the price of the cut … paid explicitly, not
hidden"* is true of the ADR and false of the shipped artifact.

**Concrete fix:** (i) `WithRequestActor`'s godoc — and each of the three aliases' — must carry:
*"⚠ `Actor.Attributes` is persisted into the human-task audit record and rendered verbatim by the
**unauthenticated** `GET /instances/{id}/actionable` and `/snapshot`. Do not populate it with
personal data until route-group authentication lands (ADR-0190)."* (ii) the same sentence in
`SECURITY.md` under "Scope notes for embedders", replacing F1's false line. (iii) a comment on the
resolver in `examples/authenticated_tasks` showing an attribute-free actor as the default and
naming the exposure if attributes are added. (iv) `CHANGELOG.md`'s entry says it too, since that is
what a consumer reads on upgrade.

### F17 — The pre-registered decision rule classifies "a guard" as a LOCAL Critical, so as written it authorises shipping F11 — the one finding that is a live fail-open — [MAJOR]
**Pair:** the removal grid's appended decision rule × the finding classes this round actually produced

**What the rule assumes it will be handed:** three outcomes keyed on *Criticals per lens* and on
whether *"any Critical is again an **inter-fix hole**"*. Row 2 reads: *"1.5 ≤ Criticals/lens < 3,
Criticals are **local (a guard, a count, a citation)** ⇒ **fold and implement**, recording the
residual Criticals explicitly in the ADR."*

**Why that assumption fails, three ways:**
1. **Its own example list is wrong about this bundle.** "A guard" is offered as a paradigm *local,
   safely-residualised* Critical. F11 **is** a guard — and it is a measured fail-open that admits
   the exact durable brick the guard was written to prevent. Recording it "as a residual in the ADR"
   would ship a guard three documents claim works and that measurably does not. The rule's
   safe-category list contains the unsafe case.
2. **It never says who adjudicates local-vs-inter-fix, or how.** That single predicate gates the
   difference between *implement* and *escalate to the owner*, and the adjudicator is the author of
   the fixes being classified — the exact configuration `controller-cannot-audit-own-documents`
   exists to forbid. Nearly every finding here can be narrated as local ("just fix the sentence")
   or as inter-fix ("fix (e) broke Decision 6's premise") at the adjudicator's discretion.
3. **It has no severity-agnostic floor.** A bundle can satisfy row 1 (`Criticals/lens < 1.5`) while
   carrying a dozen MAJORs that are individually shippable and collectively a false `SECURITY.md`
   (F1), an unspecified public API (F12, F13) and an untold 40 s regression (F14). The metric was
   derived to answer *"is another round worth it?"* and is being used to answer *"is this
   shippable?"* — a different question.

**Concrete fix:** keep the Criticals/lens metric for the *"another round?"* question it was built
for, and add an orthogonal, non-negotiable gate: **no Critical may be residualised if it is a claim
about runtime behaviour that a probe can falsify.** Those are fixed or the bundle does not ship,
independent of the count. Replace row 2's example list with *"(a stale cross-reference, a
mis-numbered citation, a count)"* — documentation defects only — and state explicitly that a guard
finding is never local. And have the local-vs-inter-fix classification made by the reporting lens,
in the report, not by the adjudicator afterwards.

### F18 — The plan's prescribed anti-overreach sentence is itself an overreach: "every route … carries a **resolved** actor" is false for 23 of 26 routes — [MINOR]
**Pair:** (a) the dimension rule × REMOVED-(C) — a correction written for one hazard, not re-derived for the other

**What it assumes:** plan `:43` and `:463`: *"⛔ **Do not write, anywhere, that every route now has
an 'identified principal'.** §3.3 removed the empty-ID rule, so a resolved-but-empty actor passes.
**The true sentence is 'carries a resolved actor'.**"* The correction addresses the (a) hazard — that
"identified" overstates what the dimension rule guarantees — and prescribes a replacement.

**Why that assumption fails:** the prescribed replacement inherits the subject *"every route"*, which
(C)'s removal falsified independently. Only the three task verbs resolve anything; the other 23
routes carry no actor at all. So the plan's own anti-overreach guard hands the implementer a
sentence that is false for 88 % of the surface — a correction that fixed the predicate and left the
quantifier, which is this repo's documented recap failure mode operating inside the guard against
it. Grid cell **3 × C** ordered exactly this hunt (*"restate every 'every route' sentence … hunt
them individually"*) and this is the one it missed, because the sentence is phrased as a
prohibition rather than as a claim.

**Concrete fix:** *"The true sentence is **'the three human-task verbs carry a resolved actor'**.
Neither 'every route' nor 'identified principal' may appear."*

---

## Verdict

### The full pairwise grid I derived

Axes: the **removals** (C route-group auth, D admin role gate, G placement asymmetry), the
**changes to survivors** (a dimension rule, b round-trip guard, c size bound, d resolver timeout,
e error-swallowing optional claim body), and the **survivors** (1 seam · 2 resolver-as-parameter ·
3 refusal rules · 4 arms-first · 5 Attributes flow · 6 optional body · 7 timeout · 8 examples/docs ·
**9 the plan's own traceability table** and **10 §2.6's member set**, the two the author's grid has
no axis for).

| pair | verdict | finding |
|---|---|---|
| 5 (Attributes) × C | ⛔ half-applied: §4 + ADR carry the Negative, §2.9 still says the exposure IS closed; and no consumer-facing mitigation exists | **F4 (CRIT)**, **F16 (MAJ)** |
| 8 (examples/**docs**) × C | ⛔ the docs half was never derived — `SECURITY.md` is prescribed to assert authentication on the two open groups | **F1 (CRIT)** |
| 8 (mains) × D | ⛔ ADR/spec still say "the mains do not mount `AdminRoutes`"; plan corrected, ADR contradicts its own Context | **F15 (MAJ)** |
| 9 (plan traceability) × C, × D | ⛔ both removed decisions still own live tasks 8–11; plan contradicts itself at `:504-505` vs `:518` | **F2 (CRIT)** |
| 10 (member set) × e | ⛔ two existing tests flip 400→401, in neither §2.6 nor any task; the ablation models 2 of 3 changes | **F9 (CRIT)** |
| 6 (optional body) × C | ⛔ **NOT "no interaction"** — removal makes claim the only route where malformed ⇒ 401; Decision 6 still says 400 | **F7 (CRIT)** |
| 4 (arms-first) × C | ⚠ order is unaffected (grid right), but the cell reasoned only about **order**, never the arm's **body** | **F8 (MAJ)** |
| 7 (timeout) × C | ⚠ shrinkage is right; the cell compares the hazard only to its bigger self, never to today's 30 s | **F14 (MAJ)** |
| 3 (refusal) × C | ✅ the "every route" hunt was largely done — one prohibition-shaped sentence missed | **F18 (MIN)** |
| 1 (seam) × C | ✅ genuinely no interaction. Soft caveat: the placement argument leans on ADR-0190 existing, which the grid itself flags as undecided | — |
| 2 (parameter) × C | ✅ correct — round-2 F12's contradiction does dissolve | — |
| 1–8 × D | ✅ correct — verified `docs/adr/0095`'s §"Admin-by-composition (default-absent)" (`:159-165`); nothing in the bundle reintroduces default-deny | — |
| C × D | ✅ correct — 0190 inherits the pair, not two items | — |
| (a) × 5 | ⛔ the dimension rule's attribute-fail-open justification does not discriminate; kiosk + every role-only actor ALLOW identically | **F6 (CRIT)** |
| (b) × 5 | ⛔ the guard measures depth at the attribute origin, the store at the document origin — brick window = wrapper depth + 1 | **F11 (CRIT)** |
| (c) × 2 | ⛔ the bound has no value, no config seam, and exists only as an undefined identifier in a plan code block | **F12 (MAJ)** |
| (c) × (b) | ✅ ordering is marshal→size→unmarshal; both arms are 503, so no status divergence. Note: a payload that is both oversize and non-round-trippable reports the size arm, masking the durability hazard in logs | — |
| (d) × 2 | ⛔ five new exported symbols exist only in the plan; ADR enumerates "the three REQUIRED aliases", plan produces six | **F13 (MAJ)** |
| (d) × ADR-0186 | ⛔ 30 s + 10 s = 40 s never composed; on stdlib the resolver runs after the read deadline is cleared | **F14 (MAJ)** |
| (e) × 6's residual | ⛔ Decision 6 says malformed ⇒ 400 "unchanged"; residual 8 + row 14 say 401 | **F7 (CRIT)** |
| (e) × ADR-0186's 413 | ✅ **confirmed safe** — the oversize sentinel comes from the body **reader**, not the decoder (`stdlib/body.go:139-146,156-168`), so 413 survives the swallow. Task 6 does instruct gin/fiber to preserve it | — |
| (e) × the decode-site enumeration | ⛔ claim moves propagating→discarding; "12 propagating + 1 discarding" is asserted in code and becomes false while staying **green** | **F10 (MAJ)** |
| (a) × (b) | ⚠ an attributes-only actor passes the dimension check and can then 503 on the guard — defensible (the resolver *is* broken) but the asymmetry with the 401 path is unstated | folded into F6/F12 |
| (a) × 3's provenance | ✅ re-verified: `humantask/validate.go:24` and `validate_test.go:45-47` do carry the kiosk blessing; `docs/adr/0148-*.md` contains neither "kiosk" nor "anonymous" | — |
| (d) × its precedent | ✅ re-verified: `runtime/task/service.go:132` (10 s), `:322` (non-positive disables), godoc carries the ctx-honouring hedge | — |
| 5 × the read surface | ✅ re-verified and **not** an undercount: `runtime/view/instance_actionable.go:32-36` renders `Claim` and `Candidates` verbatim; the default `NewInstanceView` (`httpcore/view.go:23-33`) carries no tasks, so "two sibling routes" is right | — |
| 10 (residual 10) × 2 | ✅ `MountGroups` is fine — each group's `Customize` runs `httpcore.ResolveConfig`, so the post-loop nil-guard default applies | — |

### Verdict on the author's removal grid

**Eight survivor×removed cells claimed; five are wrong or materially incomplete.**

| cell | author's claim | my verdict |
|---|---|---|
| 1 × C | "no interaction" | ✅ **right** |
| 2 × C | removal resolves round-2 F12 | ✅ **right** |
| 3 × C | restate every "every route" sentence | ⚠ **mostly done**, one miss (F18) |
| 4 × C | "no interaction" | ⛔ **incomplete** — reasoned about arm ORDER only, not the arm's BODY (F8) |
| 5 × C | state a Negative ⇒ price paid | ⛔ **half-applied** (F4) and **insufficient** (F16) |
| 6 × C | "no interaction" | ⛔ **WRONG** (F7) |
| 7 × C | blast radius shrinks | ⛔ **incomplete** — never compared to today (F14) |
| 8 × C | examples + mains | ⛔ **incomplete** — the **docs** half never derived (F1); and the mains correction landed in the plan only (F15) |
| 1–8 × D | A4 + A8 dissolve | ✅ **right**, verified against ADR-0095 §"Admin-by-composition" |
| C × D | 0190 inherits the pair | ✅ **right** |
| blast-radius row: "the 186 were **ALL** C's ⇒ the member set reverts to **48**" | — | ⛔ **WRONG, and it is the row the grid wrote itself to protect.** Whether or not the 186 were all C's, the member set does **not** revert to 48: fix (e) adds ≥ 2 runtime members invisible to §2.6's ablation (F9) plus ≥ 4 enumeration sites (F10). The grid says *"this is a claim and it must be re-executed"* — it was not, and it is false. |
| unresolved #1 (ship Attributes without C?) | resolved by the owner | ⚠ the **decision** stands; its **discharge** does not (F16) |
| unresolved #2 (should ADR-0190 exist?) | open | ✅ still genuinely open, and the grid's own case is the strongest one in the bundle |

**Three of the three "no interaction" cells are not clean:** 1×C right, 4×C incomplete, 6×C wrong.
Round 2's pattern — *every wrong cell involves a removal* — holds again, and adds a second shape:
**every wrong cell is one the grid marked resolved by writing a sentence rather than by re-deriving
a consequence.**

**Two structural blind spots in the grid's shape, not its content.** (i) Its axes are
*survivor × removed*, so a **change to a survivor** (a, b, c, d, e) can pair with another survivor
and fall outside the grid entirely — that is where F6, F7, F11, F13 and F14 live. (ii) Its survivor
set is the **design's** decisions, so the *plan's own traceability table* (F2) and *§2.6's member
set* (F9, F10) have no cell, and both are broken.

### Counts

| severity | n | ids |
|---|---|---|
| CRITICAL | **7** | F1, F2, F4, F6, F7, F9, F11 |
| MAJOR | **8** | F8, F10, F12, F13, F14, F15, F16, F17 |
| MINOR | **3** | F3, F5, F18 |
| **total** | **18** | |

### Local defect vs inter-fix hole — per Critical

| id | one-line | classification |
|---|---|---|
| F1 | `SECURITY.md` prescribed to claim `InstanceRoutes`/`MessageRoutes` authenticate | **inter-fix hole** — removal of (C) left a doc deliverable asserting (C)'s guarantee |
| F2 | self-review table still assigns removed (C) and (D) to live tasks 8–11 | **inter-fix hole** — the removal orphaned rows in an artifact the grid has no axis for |
| F4 | §2.9 says the exposure IS closed; §4/ADR say the mitigation left | **inter-fix hole** — cell 5×C applied to two of three locations |
| F6 | the dimension rule's fail-open justification does not discriminate | **inter-fix hole** — fix (a) justified by a property of survivor 5 that (a) does not confer |
| F7 | malformed claim: 400 (Decision 6) vs 401 (residual 8, row 14) | **inter-fix hole** — fix (e) falsified Decision 6's premise one bullet above it |
| F9 | two tests flip 400→401, in no member set and no task | **inter-fix hole** — fix (e) invalidated §2.6's count; the ablation models 2 of 3 changes |
| F11 | the round-trip guard admits a store-bricking payload (depth 9998, k+1 wide) | **LOCAL DEFECT** — (b) is wrong on its own terms, independent of every other decision |

**6 of 7 Criticals are inter-fix holes.** Under the pre-registered rule that is row 3 —
*"any Critical is again an inter-fix hole ⇒ **stop and escalate to the owner**"* — and it fires on
the removal round, from the lens the rule was written to weight most. I would add, per F17, that
row 2 must not be reachable for F11 either: it is a measured fail-open in a guard, and the rule's
own example list would have let it ship as a residual.
