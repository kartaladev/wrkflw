# ADR-0189 re-cut — the AUTHOR's REMOVAL grid

**Written BEFORE the re-cut**, per CLAUDE.md rule #9: *"⚠⚠ A REMOVAL is a change and generates its
own grid — when you cut N decisions out, derive the survivor×removed pairs explicitly; it is not
smaller than the grid you deleted."*

⛔⛔ **CORRECTION, added after round 3 — the premise of this file was INVERTED, and it cost three
Criticals.**

I wrote that round 2 found *"every wrong cell involved the one entry that was a REMOVAL"*.
**Round 3's counting lens re-derived it from round 2's own table: it is 2 of 4, not 4 of 4, and
3 of the 4 wrong cells involve decision A — `Attributes` flowing — which SURVIVES the cut.**

Consequence, exactly as you would predict from the wrong premise: this grid derives
survivor×removed and removed×removed **exhaustively** and contains **ZERO survivor×survivor
pairs**. Spec §6 names four changed survivor decisions ⇒ **six undrawn pairs** — and **C1, C2 and
C5 all live in them.** Two round-3 lenses found that structural gap independently.

⚠ Also false in this file as first written: *"re-verified clean by round 2's counting lens"*. That
lens filed **two** findings against §2.6, one accepted and at the time unfolded.

**The six missing pairs are now drawn below.** The transferable rule: *a grid's axes must cover
every changed decision against every other, not only the ones that moved out.*

## What is being REMOVED (owner decision, after round 2)

| id | removed decision | goes to |
|---|---|---|
| **C** | every route group except `HealthRoutes` refuses an unresolved actor | **ADR-0190** |
| **D** | `AdminRoutes` opt-in required-role gate (`WithAdminRoles`) | **ADR-0190** |
| **G** | the two-resolution-placement asymmetry (pre-decode for new groups) | dissolves with C |

## What SURVIVES

**1** the `authz` context seam · **2** `RequestActorFunc` as a parameter on the three task
endpoints · **3** the refusal rules · **4** arms-first in `ClassifyError` · **5** `Attributes` flow
+ guard · **6** optional claim body · **7** the resolver timeout · **8** examples/docs.

## Survivor × Removed — every pair, derived

### 1 × C — **no interaction.** The seam is transport-agnostic and had no dependency on which groups consume it.

### 2 × C — ⭐ **REMOVAL RESOLVES A ROUND-2 CRITICAL.**
Interaction F12 held that C *was* adapter-side resolution at 63 sites — the exact ground Decision 2
rejected it at nine — making Decision 2's rationale self-contradictory. **With C gone the
contradiction disappears** and Decision 2's "resolve once, in `httpcore`" is true again, unqualified.

### 3 × C — ⚠ **the refusal rules now bind on three routes, not twenty-six.** Restate every
"every route"/"all route groups" sentence. ⛔ The spec, ADR and plan each carry such sentences;
they are now false and must be hunted individually, not assumed corrected by the section delete.

### 4 × C — **no interaction.** `ClassifyError` arm order is independent of which handlers produce
the sentinels. ⚠ But `ErrUnauthenticated`'s *reachability* narrows to three routes — any coverage
claim scoped to "all groups" is now wrong.

### 5 × C — ⚠⚠ **THE LOAD-BEARING REMOVAL CONSEQUENCE. C was the mitigation for D-1's exposure leg.**
The ADR's Positive *"The unauthenticated read surface closes"* was **C's doing**: authenticating
`InstanceRoutes` is what stopped `GET /instances/{id}/actionable` and `/snapshot` rendering
`Claim.Actor` and `Candidates` to anonymous callers.

⇒ **with C removed, that Positive is FALSE and the exposure leg REOPENS.** Newly-flowing actor
attributes land on a read surface that stays anonymous.

⚠ And round 2's failure-modes correction applies with full force here: the pre-existing exposure
channel requires an opt-in `humantask.ActorResolver`, whereas the new one is fed by
`RequestActorFunc`, which **every HTTP consumer must configure**. Same provenance, **materially
different population rate.** §2.9's "not our fault, therefore not a cost" ran two claims together;
only the first half ever held, and the second half is now indefensible.

**Resolution — this is the price of the cut and it is paid explicitly, not hidden:**
the ADR must carry a **Negative** stating that actor attributes supplied through
`WithRequestActor` are rendered to unauthenticated readers by two sibling routes, that
`SECURITY.md` classifies them as personal data, and that the mitigation is **deferred to
ADR-0190**. It must be filed as a backlog item **on the day this ships**, not "when 0190 lands".
⛔ It may NOT be described as pre-existing without the population-rate qualifier.

### 6 × C — **no interaction.** The optional claim body is a decode concern on one route.

### 7 × C — ⚠ **the timeout's blast radius shrinks to three routes.** Round-2 failure-modes noted a
slow resolver holds a request for `timeout × concurrency`; across 26 routes that was a new DoS
surface, across 3 it is materially smaller. Do not restate the larger claim.

### 8 × C — ⚠ `examples/authenticated_tasks` was going to demonstrate group-wide authentication.
It now demonstrates the **task-route** seam only. ⚠ And the three wiring mains no longer need any
change **for admin** — but `production_wiring:274`'s `AdminRoutes.Customize` is untouched by this
bundle, so round-2's A5 (401 despite a correct token) **dissolves entirely.**

### 1–8 × D — **D touched only `AdminRoutes`.** With it gone:
- **A4 (the ADR-0095 contradiction) dissolves** — nothing reintroduces default-deny.
- **A8 (`strings.Split("",",")` → `[""]`, and the empty-config silent disable) dissolves** — the
  mechanism is gone.
- ⚠ **The `actor.Roles` membership test — a second, parallel authorization mechanism — leaves the
  repo.** That was the author's own unresolved item #1 in the round-1 grid. It returns as
  **ADR-0190's** central design question, where it must be argued against ADR-0095 §"Admin-by-
  composition" *and* CLAUDE.md's pluggable-`Authorizer` requirement rather than around them.

### C × D (both removed) — the pair leaves together, so no orphan. ⚠ **But ADR-0190 inherits the
pair, not two independent items:** C without D authenticates admin routes while letting any
authenticated caller administer; D without C gates on roles nobody verified. 0190 must design them
together or say why not.

## Blast radius — the row round 2 caught me exempting

⛔ Round-2 A3: I exempted the blast-radius row from the round-1 grid ("G, H, I, J, K … none
interacts") and it was wrong by **186 assertions across 13 test files, 7 of them named nowhere**.
**This grid does not repeat that.**

Derived: **the 186 failing assertions were ALL C's** — they are instance/message/admin route calls
that would newly 401. Removing C removes every one.

⇒ the member set reverts to spec §2.6's **48 lines / 13 files / 6 packages**, which was derived
**exactly for this scope** and re-verified clean by round 2's counting lens.
⚠ **This is a claim, and it must be re-executed after the re-cut, not assumed** — the whole reason
this file exists is that I assumed it last time.

## Interactions I could NOT resolve — flagged, not hidden

1. ~~**Whether shipping D-1 (`Attributes`) without C is net-positive.**~~ **RESOLVED by the owner,
   with the cut in view.** The scope question that produced this re-cut offered three options, and
   *"Cut to one decision **AND** drop `Attributes`"* was one of them — explicitly listing "leaves
   the deny-list fail-open live" as its cost. The owner chose the option that **keeps**
   `Attributes` plus the round-trip guard. So the trade (a measured fail-open closed, an anonymous
   read exposure widened) was decided knowing C would not mitigate it.
   ⇒ **no further owner input needed; the obligation is to state the Negative honestly and file
   the backlog item on the day this ships.**
2. **Whether ADR-0190 should exist at all, or whether `AdminRoutes` needs nothing** beyond
   ADR-0095's default-absent composition. Round 2's A4 makes a serious case that the admin
   "exposure" was never real for a consumer following the documented posture.

---

## ⚖ PRE-REGISTERED decision rule for round 3 — recorded BEFORE the results

Written now so it cannot be moved after the numbers arrive. This is the third audit of one
lineage's fifth bundle; ADR-0185 failed three rounds and was re-cut anyway, and ADR-0186 shipped
under rule #11 as a *recorded exception* to rule #9 once its trend showed the finding rate was a
property of the process rather than the bundle.

**The metric is Criticals per lens.** Total findings per lens is a function of lens count
(r = 0.855 across the repo's ten measured rounds, ~15.14 ± 0.83) and carries no signal about
bundle health. Criticals per lens has moved 8.25 → 3.50 → 1.75 → 3.75 across this lineage, and the
last step up coincided with a 2 → 9 decision widening that three lenses named independently.

| round-3 result | action |
|---|---|
| **Criticals/lens < 1.5**, and no Critical is an inter-fix hole | **adjudicate, fold, implement.** |
| **1.5 ≤ Criticals/lens < 3**, Criticals are local (a guard, a count, a citation) | **fold and implement**, recording the residual Criticals explicitly in the ADR — a fourth round would be measuring the process, not the bundle. |
| **Criticals/lens ≥ 3**, or any Critical is again an *inter-fix* hole | **stop and escalate to the owner.** A one-decision bundle that still produces inter-fix Criticals means the defect is in how this delivery is being designed, not in its scope, and more rounds will not find it. |

⛔ **Round 3 is the last audit for this bundle under any of the three outcomes.** If the third row
fires, the response is an owner conversation, not a round 4.


---

# The SURVIVOR × SURVIVOR grid — added after round 3

The four changed survivor decisions, per spec §6: **(a)** the refusal rule, **(b)** the attribute
guard, **(c)** the size bound, **(d)** the resolver timeout. Plus **(e)** the optional claim body,
which round 3's counting lens showed is a *behavioural* change with its own blast radius.

|      | b | c | d | e |
|------|---|---|---|---|
| **a** | ⚠**S1** | · | · | ⚠**S2** |
| **b** | — | ⚠**S3** | ⚠**S4** | ⚠**S5** |
| **c** |   | — | · | · |
| **d** |   |   | — | ⚠**S6** |

### ⚠S1 — (a) × (b): the attributes-only actor is admitted by one and can be rejected by the other

(a) admits `{Attributes:{…}}` — attributes are its *only* dimension. (b) may then reject those very
attributes for depth or size, turning what (a) accepted as an identity into a **503**.
**Resolved by ordering, and the order is deliberate:** (a) runs first on the actor *as returned*,
so an attributes-only actor that fails (b) yields 503 (*"your resolver produced something we cannot
store"*) rather than 401 (*"you are not authenticated"*). Those are different facts and the codes
must not be swapped. ⚠ The plan's test table must contain both orderings, or a later refactor can
silently reorder them.

### ⚠S2 — (a) × (e): a bodyless claim from a zero-actor caller

(e) makes an absent body legal; (a) refuses the zero actor. Both can fire on one request.
**Resolved:** decode is swallowed, resolution refuses ⇒ **401**, never 400. This is the same
resolution as C5, reached from the other direction, which is why C5 was missable — nothing in the
old grid drew this pair.

### ⚠S3 — (b) × (c): which bound reports first, and does the status differ?

Both classify 503, so the *code* is stable — but the **message** differs, and the depth walk runs
before the marshal, so a payload that is both too deep and too large reports **depth**.
**Resolved:** deliberate and stated, because depth is the one that corrupts the store while size is
merely a quota. The test asserts the message, not just the code.

### ⚠S4 — (b) × (d): the guard runs INSIDE the timeout budget, and it is not free

(d) bounds the resolver call. (b) walks and marshals the attributes *after* it returns — **outside**
the timeout. A 16 KiB, 64-deep payload is walked and marshalled on every request with no bound of
its own.
**Resolved:** the size bound is what makes this safe, so **(c) is load-bearing for (d), not
decoration** — which is precisely why round 3 leaving `maxActorAttributeBytes` undefined was worse
than it looked. ⚠ The bound must be applied to the *marshalled* size, which is only known after the
work; the depth bound is the cheap pre-filter that keeps the walk itself bounded.

### ⚠S5 — (b) × (e): a claim with no body still runs the attribute guard

(e) removes the body; the actor still arrives from the resolver, so (b) still runs. **No
interaction in behaviour, but it kills a tempting optimisation:** "no body ⇒ nothing to validate"
is false, and a future reader might skip the guard on the claim route.
**Resolved:** stated here so it is not discovered by a regression.

### ⚠S6 — (d) × (e): the composed worst case

`BodyReadTimeout` (30 s, default) then the resolver timeout (10 s, default) ⇒ **40 s** worst-case
before a task request is refused, on a route whose body is now optional. Round 3's interaction lens
raised it and nothing composed the budgets.
**Resolved by disclosure, not by change:** both defaults are inherited and consistent with the rest
of the library; the ADR states the composition so an operator sizing timeouts is not surprised.

### The `·` cells, justified rather than assumed

- **(a) × (c)** — the size bound reads attributes; (a) only asks whether any exist. Independent.
- **(a) × (d)** — the timeout governs the resolver call; (a) inspects its result. Independent.
- **(c) × (d)** — see S4: (c) is what makes (d) safe, but the pair itself has no ordering hazard.
- **(c) × (e)** — the body is not the attributes' source. Independent.
