# ADR-0189 — rule-#9 audit adjudication

**Date:** 2026-08-25 · **Bundle audited:** `7fa756d0` (spec + ADR-0189 + plan)
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four
**detached worktrees created AT the bundle commit**, so the documents were present by
construction; **step-0 presence check passed in all four**.
**Reports:** `audit-0189-{execution,failure-modes,counting,interaction}.md` beside this file.

## ⛔ VERDICT: THE BUNDLE FAILS ITS AUDIT. Not an input to implementation.

**48 actionable findings across four lenses; 7 raw Criticals.**

| lens | findings | C | M | Mi |
|---|---|---|---|---|
| execution | 14 (+5 confirmations) | 2 | 11 | 1 |
| failure-modes | 11 | 1 | 7 | 2 |
| counting | 11 (+2 INFO) | 3 | 5 | 3 |
| interaction | 12 | 1 | 9 | 2 |
| **total** | **48** | **7** | **32** | **8** |

### ⚠ Read the count as an instrument reading, not a quality signal

Per `meta-analysis-audit-finding-rate.md`: seven four-lens rounds returned **15.14 ± 0.83
findings per lens**, correlating with lens count (r = 0.855) and **not at all** with scope.
This round is **12.0 per lens** — below the band, consistent with ADR-0188's 11.0 and with a
genuinely smaller bundle. ⛔ Do not report 48 as "no progress".

**The number that moved is Criticals per lens: 8.25 → 3.50 → 1.75.** That is the fourth
consecutive fall and the first round below 2. It is the only metric this process has produced
that tracks bundle health rather than lens count.

### What SURVIVED — do not re-litigate

- ⭐ **The core decision is intact and all four lenses say so.** The actor travels in the
  `context.Context`; the transport reads it from nowhere else. Interaction states it verbatim:
  *"The core decision survives intact."* No lens attacked the seam, the `WithRequestActor`
  naming, or the fail-closed-by-default posture.
- ⭐ **Five spec premises re-executed TRUE**, independently, by the execution lens: §2.2 fiber
  `SetContext`-not-`Locals`; §2.3 unknown-key tolerance (re-run on **real mounted routes**, not
  just decoders); §3.3 rule 3's durable-record premise (`{"actor":{"id":""}}` does reach both
  stores); §3.5's "cannot infer R", error text and all; §2.5.
- ⭐ **§2.6's starred PREDICTION is CONFIRMED.** Both `stdlib` 403 pins fail loudly:
  `want 403 complete forbidden, got 401`. The prediction was labelled as a prediction and it
  held — the one place this bundle's epistemics worked as intended.
- ⭐ **§3.4's arms-first ordering and the 503-not-403 choice are correctly derived.**
  Failure-modes went looking and reported them clean. (⚠ but see A6 — the arms co-match *each
  other*, which is a different defect in the same decision.)
- ⭐ **counting F2: three actor sites, eight `CustomizeConfig` fields, three `WithActorResolver`,
  nine adapter call sites — all exact.** And **counting F0: zero base drift** between the
  `9789ebcc` claims and `7fa756d0`.
- ⭐ **Every `file.go:NNN` anchor in all three documents resolves and says what the bundle says
  it says.** Not one rotted anchor — the counting lens called it the best it has seen here.

---

## A. Accepted Criticals

### A1 — §4.2's backlog-103 consequence is INVERTED (counting F11 · interaction F1 · controller probe)

**Three independent confirmations, plus my own.** The spec and ADR both say `actor.Attributes.*`
predicates "fail closed **vacuously**" today and "go live with nothing bounding them" after the
change — filed as a **cost**. Executed against `RoleAuthorizer` at `7fa756d0`:

| predicate class | today (attributes dropped) | after ADR-0189 |
|---|---|---|
| deny-list `actor.Attributes.status != "blocked"` | **ALLOW** ← live fail-open | **DENY** |
| allow-list `actor.Attributes.status == "active"` | DENY (vacuously) | satisfiable |

⇒ for the deny-list class — **the exact class §4.2 is about** — the bundle **CLOSES a live
fail-open on the HTTP path**. The consequence is signed backwards.

**Root cause, and it is the instructive part:** §1.1 correctly *deleted* ADR-0185's claim that
attributes reach the authorizer "closing finding 4's second leg for free", citing the audit's
refutation (`actor` is a struct, `Attributes` exists at depth-1) — and then §4.2 wrote the
**opposite-signed** consequence off that same mechanism. A measurement inherited from a `vars`-root
probe, restated about the `actor` root. This is the repo's recurring failure exactly.

**Adjudicated fix:** rewrite §4.2 and the ADR's matching Negative to state both classes with the
measured table. **File the deny-list fail-open as a NEW backlog item** — it is live at `main`
today, filed nowhere, and is not the same thing as backlog 103 (which was executed over `vars`).

### A2 — the blast radius is undercounted, and the "re-derivation" was satisfied by a matching TOTAL (counting F4 + F5 · execution F12)

**Two lenses, converging from different directions. Accepted, and this is the headline process failure.**

- **F4/F12 — the ablation modelled only HALF the change.** It deleted the DTO fields but
  **stubbed `endpoints.go` instead of changing its signatures**, so it never modelled the second
  breaking change. Re-run with the real signatures: **three more compile-breaking lines** —
  `endpoints_test.go:436,499,575`, `not enough arguments in call to httpcore.ClaimTask/...`.
  ⇒ compile-breaking is **14, not 11**, and the word **"exhaustive"** is false.
- **F5 — the "29" is a DIFFERENT SET of 29 from the inherited "29".** ADR-0185's grep net and
  ADR-0189's ablation both return 29 and both give httpcore 11, and they are **disjoint on five
  `dto_test.go` members**: the grep sees the JSON body strings (57/68/79/151/161), the ablation
  sees the Go assertions (47/62/73/84/153). Each contributes exactly 5, so both totals land on
  29 **by coincidence**. The union — the lines that must actually be edited — is **37**.

⇒ **the bundle's stated defence against inherited numbers ("re-derived, not restated") was
satisfied by a matching total and never by a matching set.** The spec's §2.6 boast that the net
is "closed by construction… the compile ablation is the machine check" is the sentence that
failed.

⭐ **Process rule to adopt, and it generalises past this delivery:**
**a count is re-derived only when its MEMBER SET is re-derived — paste the list, not the total.**
Two nets agreeing on a total is not corroboration; it can be coincidence, and here it was.

### A3 — the correctly-migrated client is broken (execution F11) — controller-confirmed

`ClaimInput` becomes a **zero-field struct**, so a correctly-updated client sends **no body**.
Executed at `7fa756d0` against a real mounted `stdlib` route:

```
no body at all    -> status=400 body={"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
empty JSON object -> status=403 (proceeds to authz)
```

⇒ after this ships, claiming a task **requires literally sending `{}`**. The migration story in
§2.3 reasons carefully about the **lagging** client (a body still carrying `actor` — confirmed
ignored) and never considers the **updated** one.

⚠ **No prescribed test could have caught it**: the endpoint tests call `ClaimTask(...)` directly
and bypass decode; the adapter tests all leave `map[string]any{}` behind.

**Adjudicated fix:** the claim route must decode an **optional** body. ⚠ Only `stdlib` has a
`decodeOptionalRequestBody` helper (`body.go:156`, used once at `groups.go:234`); **gin and fiber
have no equivalent and need one**. That is real added scope this bundle did not budget.

### A4 — the newly-flowing `Attributes` are a five-finding cluster, not a free Positive

Converged on by **three lenses**: failure-modes F2 (CRITICAL) + F4, execution F6 + F7,
interaction F11.

- **Exposure (fm F2).** `ActionableTask.Claim *humantask.Claim` renders `Claim.Actor` whole, and
  `GET /instances/{id}/actionable` and `/snapshot` are mounted by the **same `Mount`** with **no
  authorization at all**. `SECURITY.md` already classifies `claim_actor` as `actor` — *"personal
  data"*.
  ⚠ **Provenance corrected by the controller:** `ActionableTask.Candidates []authz.Actor` **already
  renders `{id, roles, attributes}` verbatim today** by ADR-0147's explicit decision. So the
  unauthenticated leak channel **pre-exists this bundle**; ADR-0189 *widens* it from candidates to
  the claimant. F2's *"the defect being fixed was also the mitigation"* **overstates it** and is
  **partially rejected** on that point — the finding stands, its provenance does not.
  ⇒ and the pre-existing candidate exposure is **its own backlog item**, filed here.
- **Unbounded durable writes (fm F4, ex F6).** The actor is validated on exactly one property
  (`ID != ""`). ADR-0186's body cap no longer bounds this path — the attributes arrive from the
  consumer's resolver, not the body — and they land in `wrkflw_human_task` and
  `wrkflw_instances.snapshot`.
- **View poisoning (ex F7).** A non-JSON-marshalable attribute (`json: unsupported type: chan int`,
  executed) **permanently bricks the instance view** — fail-open at write, fail-closed forever at
  read, inside an otherwise fail-closed design.
- **ADR-0187 classification drift (int F11).** ADR-0187 merged `4e2c0af4` **three days before this
  bundle** and classifies `claim_actor`/`completion_actor` as `ClassActor`, not `ClassFreeform`.
  Flowing arbitrary consumer JSON falsifies that. ⚠ **No guard can catch it**: the drift guard
  checks which columns *exist*, not what a class *describes* — ADR-0187's own lesson #2 recurring.
  The bundle mentions neither ADR-0187 nor `internal/atrest`.

**Adjudicated: this cluster is why the `Attributes` flow should be CUT from this bundle — see §D.**

---

## B. Accepted Majors, grouped

### B1 — the context trap is NOT fiber-specific (failure-modes F9) — controller-confirmed

Executed: gin's `gc.Set` — its **canonical** auth-middleware idiom — does **not** reach
`gc.Request.Context()`, which is exactly what `gin/groups.go` hands `httpcore`.

```
gc.Set (gin's canonical idiom)      Request.Context()=<nil>            gc.Get=from-middleware
gc.Request = gc.Request.WithContext Request.Context()=from-middleware  gc.Get=<nil>
```

The spec calls the trap "fiber-specific" and gin "standard". **Both are wrong**, and Task 8
prescribes no test for it. Fix: state it for both, and pin gin's with a test like fiber's.

### B2 — authentication runs BEHIND the body read (interaction F3)

Resolution is *inside* the endpoints; all three adapters decode the body **before** calling them.
So ADR-0186's measured slowloris primitive (1 MiB / 30 s per request) and the 400/413 error oracle
stay **fully unauthenticated**. The ADR's *"fails closed at every entry"* overreaches.

⚠ **This is a real cost of the endpoint-parameter shape chosen at the owner's Q1 decision, and my
options write-up weighed only drift surface — never ordering.** The rejected adapter-side
alternative has the opposite property. Owner decision — see §D.

### B3 — the empty-ID rule collides with two shipped ADRs (failure-modes F1 · execution F4)

`humantask/validate.go:24` and ADR-0183:69-76 call the empty-claimant ("kiosk") shape
**"deliberately legal"**, and ADR-0183 explicitly **declined** to supersede ADR-0148 on it. The
bundle pre-empts an ADR-0147 objection and cites **neither** 0148 nor 0183 — a dangling citation
in the very rule §1.1 congratulates itself for de-dangling. Owner decision — see §D.

### B4 — the ADR's own "BREAKING" has no CHANGELOG / STABILITY entry (failure-modes F6)

The repo maintains both and **ADR-0186 set the precedent three deliveries ago**. Accepted; folds
into plan Task 12.

### B5 — `WithRequestActor` reaches one route group of four (failure-modes F7 · execution F19 · interaction F10)

`InstanceRoutes`, `MessageRoutes` and `AdminRoutes` accept the option and **silently ignore** it.
After this ships the default `Mount` still **starts instances, delivers messages, administers
policy, redrives dead letters and cancels instances with no identity** — while the task routes
401. The ADR's *"specs stop being satisfiable by typing a role name"* is **false** while
`POST /admin/role-bindings` is reachable. Accepted: weaken the claim, state the residual, file the
rest. Scope question in §D.

### B6 — the removal grid for `WithAnonymousActorAllowed` was never derived (interaction F5 · F6 · failure-modes F8)

spec §6 item 6 **names** the removal grid as required work and **nobody does it**.
- **F6/F8, self-contradiction:** §2.5 prices the removal as cheap **because** the demo mains 401;
  §3.6 then hardcodes `Actor{ID:"demo-user", Roles:["manager"]}` into all three so they answer
  **200 as a manager**. Measured today: that curl is **403**. ⇒ the three reference mains become
  **strictly more open** in a bundle about closing a self-assertion hole.
  ⚠ And the argument used to kill the library-chosen sentinel — *don't let the library pick a
  colliding identity string* — **applies harder** to a string prescribed into the most
  copy-pasted files in the repo.
- **F5, budget:** the ADR budgets **"two rewrites"** where all **18** runtime pins now 401 — a
  **9× undercount**, inherited from ADR-0185's *vacuous-pin* analysis and restated into a slot
  that asks a different question. Tasks 7 and 10 terminate RED at a step expecting EXIT=0.

### B7 — the two new arms co-match EACH OTHER, untested (execution F16)

`ErrIdentityUnavailable` wraps arbitrary consumer errors; a resolver returning
`ErrUnauthenticated` **wrapped in something else** matches both new arms. The ADR cites
`errors.go`'s standing invariant as its authority and then **violates it for its own pair**.
Accepted: add the co-match case to Task 2.

### B8 — the seam's isolation guarantee is false for the payload it newly admits (execution F5 · interaction F2)

`Actor.Clone` clones `Attributes` **one level deep** — its own godoc says so. A nested map or
slice inside an attribute value stays shared, so *"a later mutation by the caller cannot reach the
engine"* is false for exactly the payload (d) newly admits. **And `ActorFromContext` clones
nothing on the way out.** The plan's own clone test uses a flat attribute and cannot detect it.

### B9 — remaining accepted Majors, one line each

- **ex F1** — ADR-0147 amendment #5 contains a claim this bundle falsifies while ADR-0189 says
  *"Amends: nothing"*.
- **ex F9** — on fiber `c.Context()` is literally `context.Background()`: the consumer's resolver
  runs with **no deadline and no cancellation**. *"503, never an open door"* has an unnamed third
  state — **hang**. Pairs with **fm F3**: the repo already solved this hazard twice
  (`defaultCandidateResolveTimeout = 10s`) with the reasoning in its own comment.
- **ex F14** — the 401 now precedes the task lookup, so an unauthenticated request for a
  **nonexistent** task returns 401 instead of 404. Unstated, and it breaks a gin test the plan
  explicitly tells that agent not to worry about.
- **ex F18 / counting F12** — spec §5 row 8 (*attributes reach `service.ClaimTaskRequest`*) is
  **tested nowhere**, and the plan's self-review **claims it was closed**. Row 8 as written also
  observes the wrong object.
- **counting F1** — the blast radius is **6 packages, not 5**: `service/instance_test.go:1090,1128`
  comment-cite `httpcore.Actor` and assert the exact limitation this bundle deletes. Invisible to
  both nets, and Task 12's sweep grep does not search `service/`.
- **counting F6** — the plan's `dto_test.go` line set leaves **three tests that cannot fail**.
- **counting F7** — *"the four `runtime/task` verbs as reached over HTTP"*: only **three** are
  HTTP-routed. A wrong count inside the residual section.
- **fm F5** — an **invalid** credential takes the 503 + ERROR-log path, not 401: attacker-paced log
  amplification labelled *"internal error"*.
- **int F7** — *"ADR-0117 becomes true rather than changed"* **equivocates authentication for
  authorization**; 0117 says *authorization* twice, and its deferral is still unsatisfied.
- **int F8** — the supersession is asserted **only** in ADR-0189, while `HANDOVER.md` and ADR-0185
  — the two documents a fresh session is told to read first — still route to the deleted D1.
- **int F9** — `httpcore.MountGroups`, documented as **the consumer extension seam**, passes zero
  options, so after the signature change it **cannot mount a working task API**.

## C. Rejected / partially rejected — stated, because silence is not an adjudication

- **failure-modes F2, PARTIALLY REJECTED on provenance.** *"The defect being fixed was also the
  mitigation"* is wrong: `Candidates` already renders attributes verbatim today per ADR-0147, so
  the unauthenticated channel pre-exists. The exposure finding stands; its framing does not.
- **counting F10, REJECTED as out of scope.** That ADR-0185's inherited Criticals breakdown does
  not sum is a defect in **ADR-0185's** adjudication, not in this bundle. Worth a footnote there;
  it changes nothing here.
- **counting F3, ACCEPTED but downgraded to editorial.** The "struct literal vs post-loop guard"
  contrast is loosely worded (`Wrap`, `InstanceMapper` and `Logger` are in the literal too) but
  the *reasoning it supports* — nil-distinguishability — is correct and load-bearing. Reword; do
  not restructure.
- **failure-modes F11's "panic" leg, REJECTED.** A panicking consumer resolver is not this
  library's failure to handle; the repo does not recover panics at any other consumer seam and
  starting here would be inconsistent. The "mutation-visible read" leg is **accepted** as B8.

---

## D. ⚠ OPEN DECISIONS — the owner's, not an agent's

The accepted findings do not all fold into a wording change. Four are scope decisions, and this
lineage's history says the wrong move is to fix everything at once and re-audit: ADR-0185 did
that twice and its **second** audit found that **five of nine Criticals were holes the revision's
own fixes opened in each other**.

### D-1 — ⭐ Cut the `Attributes` flow from this bundle?

**The single highest-leverage decision.** A4's entire five-finding cluster — unauthenticated
exposure, unbounded durable writes, permanent view poisoning, ADR-0187 classification drift — and
A1's inverted consequence **all trace to one sub-decision: that the whole `authz.Actor` flows
rather than `{ID, Roles}`.**

Backlog 51 is *"the actor must not be self-asserted."* That is fully satisfied by flowing **ID and
Roles** from the context. `Attributes` is an **additional capability** riding along.

| | flow `Attributes` | flow `{ID, Roles}` only |
|---|---|---|
| closes backlog 51 | yes | **yes** |
| A4 cluster (5 findings, 1 CRITICAL) | must all be mitigated in-bundle | **dissolves** |
| A1 inverted consequence | must be rewritten | **not applicable** |
| ADR-0187 at-rest drift | must be reconciled | **none** |
| ABAC over actor attributes via HTTP | becomes possible | stays unreachable |
| the live deny-list fail-open (A1) | **closed** | **stays open — must be filed** |
| decisions in this bundle | 2 | **1** |

**Recommendation: CUT IT**, and give the `Attributes` flow its own ADR carrying its own
mitigations (a size bound, a marshalability pre-check, the ADR-0187 reclassification, and a
position on the unauthenticated read routes). Rationale: it reduces this bundle to **one**
decision, which is the only structural change this lineage has not yet tried; and the repo's own
meta-analysis says the failures are interaction failures, which a one-decision bundle cannot have.

**The cost is real and must be recorded, not glossed:** the deny-list fail-open A1 measured is
**live at `main` today** and stays live. It gets filed as a new backlog item the same day.

### D-2 — does the empty-`Actor.ID` rule stand? (B3)

- **(a) Drop it.** The bundle stops colliding with ADR-0148/0183's "deliberately legal" kiosk
  claimant. Cost: `""` can reach the durable audit record via HTTP — though ADR-0148 already
  permits exactly that by other routes, so the transport would merely stop being stricter than
  the engine.
- **(b) Keep it and amend ADR-0148 + ADR-0183 in this bundle.** Honest, but it makes this a
  three-ADR delivery and re-opens a decision ADR-0183 deliberately left alone.
- **(c) Keep it, scoped to the claim path only**, as ADR-0185 originally had it.

**Recommendation: (a) drop it.** Its *original* justification left with backlog 124's deferral;
the ADR-0147 replacement is sound in isolation but is an argument for an **engine-level**
invariant, and execution F4 is right that enforcing it at the transport only is the wrong layer.
Dropping it also removes one refusal rule from the matrix, which is one less interaction.

### D-3 — do the admin/instance/message routes come into scope? (B5)

They are **entirely unauthenticated** today and stay that way. `POST /admin/role-bindings` grants
roles; the task routes then honour them.

- **(a) Out of scope.** Weaken the ADR's overreaching claim, state the residual precisely, file
  it. This delivery stays one decision.
- **(b) In scope.** Coherent security story, but it triples the surface and re-creates the
  multi-decision bundle that has failed four times.

**Recommendation: (a)**, with the residual stated in the ADR in the same blunt terms as the
`ApplyTrigger` one, and filed as a backlog item naming `AdminRoutes` specifically.

### D-4 — does B2 reopen the Q1 endpoint-seam decision?

Authentication currently resolves **behind** the adapters' body decode, so the body-cap slowloris
window and the 400/413 oracle stay unauthenticated. I recommended the endpoint-parameter shape on
drift-surface grounds and **never weighed ordering** — that was an incomplete options analysis,
and the owner chose on it.

- **(a) Keep the parameter shape, document the ordering honestly.** Cheapest. The exposure is
  pre-existing (those routes are unauthenticated today regardless) and is not a regression.
- **(b) Hoist resolution ahead of decode in each adapter.** Closes it, but is the Option 3 shape
  that duplicates the security decision at nine sites — and would now be a *third* signature
  change to the same three functions.
- **(c) Keep the parameter, and additionally resolve early in the adapters' shared `handle`
  wrapper.** Resolves once per adapter rather than nine times; the endpoint's resolve becomes a
  cheap re-read.

**Recommendation: (a) for this bundle**, because the exposure is not a regression and (c) is a
design increment that deserves its own measurement. Record it as a stated residual, not as a
silent one.

---

## E. Root causes, stated once

1. **A count was treated as re-derived because its TOTAL matched.** (A2) The member set was never
   compared. Two different nets agreeing is not corroboration.
2. **An ablation modelled one of the change's two halves.** (A2) The spec then called the result
   *"exhaustive"* and *"the machine check"*. A machine check is only as good as the mutation it
   applies.
3. **A measurement was carried across roots.** (A1) A `vars`-root probe restated about the
   `actor` root, producing a consequence with the wrong sign — in the same document that had
   correctly deleted the *previous* bundle's error about the same mechanism.
4. **The migration story covered the lagging client and not the updated one.** (A3) Both
   directions of a breaking change need a probe; only one got one.
5. **The removal grid was named as required work and then skipped.** (B6) spec §6 item 6 asked for
   it explicitly. Writing down that a check is needed is not performing it — and the two documents
   that would have caught the contradiction (§2.5 vs §3.6) sit 200 lines apart in the same file.
