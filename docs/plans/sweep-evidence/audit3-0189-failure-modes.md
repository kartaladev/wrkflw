# ADR-0189 round 3 — FAILURE-MODES lens

Worktree `wt3-failure-modes`, detached at `3e96e836`. Step-0 presence check: **PASSED** — spec, ADR,
plan and `audit2-0189-removal-grid.md` all present.

Findings appended as they are established. "Executed" means a command was run in this worktree and
its real output is pasted.

---

### F1 — the plan tells the implementer to write a FALSE security claim into `SECURITY.md`: that `InstanceRoutes`/`MessageRoutes` "authenticate but do not authorize" — [CRITICAL]

**Bundle text attacked:** `docs/plans/2026-08-25-request-actor-identity.md` Task 13, line 453-455:

> - [ ] **`SECURITY.md`** — the middleware pattern for all three frameworks, the 401/503 contract,
>   the `c.SetContext`/`gc.Request.WithContext` warnings, and a "Scope notes for embedders"
>   entry stating that `InstanceRoutes`/`MessageRoutes` **authenticate but do not authorize**.

**The failure:** that sentence was true of the ROUND-2 bundle, where decision C authenticated every
route group except `HealthRoutes`. **C was removed.** After the cut, `InstanceRoutes` and
`MessageRoutes` authenticate *nothing* — the ADR's own Context says so twice ("No other route group
authenticates anything either, and **this record does not change that**"; Negative bullet
"`InstanceRoutes`, `MessageRoutes` and `AdminRoutes` remain **entirely unauthenticated**").

The consequence is not a doc-rot nit. `SECURITY.md` is the file an embedder reads to decide what
they must put in front of a mount. A "Scope notes for embedders" entry saying those groups
*authenticate* tells the embedder the exact opposite of the truth, on the two route groups that
carry `POST /instances`, `POST /instances/{id}/signals` and `POST /messages` — **state-changing,
unauthenticated endpoints**. An embedder who follows it mounts them bare.

It is also *prescriptive*: Task 13 is a checkbox an implementation subagent executes literally.
Nothing downstream of it re-derives the sentence; §5's verification table has no row for
`SECURITY.md` content, and the Task-13 "premise sweep" bullet polices only *"identified
principal"* and quantifiers added *by this bundle* — this sentence is neither.

**Evidence:** executed in the worktree —

```
$ grep -n "authenticate but do not authorize" docs/plans/2026-08-25-request-actor-identity.md
455:      entry stating that `InstanceRoutes`/`MessageRoutes` **authenticate but do not authorize**.
$ grep -n "remain entirely unauthenticated" docs/adr/0189-*.md
311:- ⚠ **`InstanceRoutes`, `MessageRoutes` and `AdminRoutes` remain entirely unauthenticated**, so
```

The removal grid's own row **3 × C** predicted exactly this class and told the author to hunt the
sentences individually: *"the refusal rules now bind on three routes, not twenty-six. Restate every
'every route'/'all route groups' sentence. ⛔ The spec, ADR and plan each carry such sentences; they
are now false and must be hunted individually, not assumed corrected by the section delete."* The
hunt was run over the ADR and spec and **not over the plan's prescribed doc content.**

**Concrete fix:** replace with *"a 'Scope notes for embedders' entry stating that ONLY the three
human-task verbs authenticate; `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` authenticate
nothing and must be mounted behind the embedder's own middleware — ADR-0190"*. Add a §5 row pinning
it: a `grep` in the delivery gate asserting `SECURITY.md` contains no claim that instance or message
routes authenticate.

---

### F2 — the plan's self-review table still maps two REMOVED decisions onto surviving tasks — [MAJOR]

**Bundle text attacked:** `docs/plans/…` "Self-review against the spec", lines 504-505:

```
| §3.6 group authentication, Health exempt, placement asymmetry | 8, 9–11 |
| §3.7 opt-in admin role gate                                   | 8, 9–11 |
```

**The failure:** spec §3.6 is now *"The claim route accepts an ABSENT body"* and §3.7 is
*"Examples and documentation"*. Group authentication, the `HealthRoutes` exemption, the placement
asymmetry (G) and the admin role gate (D) are the decisions the re-cut **deleted**. The table
asserts they are covered by Tasks 8–11 — which in this plan are the per-adapter test migration and
`parity`. So the bundle's own coverage matrix claims two removed decisions are implemented, and
claims it against tasks whose briefs say nothing about them.

The same table also has **three rows keyed §3.6** and **two keyed §3.7**, with contradictory
subjects — a reader cannot use it to check coverage at all, which is the only thing a self-review
table is for. And the plan's closing paragraph states the opposite of the table 14 lines below it:
*"Tasks 8–11 of the round-2 plan (route-group authentication and the admin role gate) are
**deleted**"*.

**Evidence:** executed —
```
$ grep -n '^### §3\.' docs/specs/2026-08-25-request-actor-identity.md
453:### 3.6 The claim route accepts an ABSENT body; the ordering residual is stated
467:### 3.7 Examples and documentation
$ sed -n '504,505p' docs/plans/2026-08-25-request-actor-identity.md
| §3.6 group authentication, Health exempt, placement asymmetry | 8, 9–11 |
| §3.7 opt-in admin role gate | 8, 9–11 |
```

**Concrete fix:** delete both rows. Re-key the surviving rows to the current section numbers
(`§3.6 optional claim body → 6`, `§3.6 ordering residual → 13`, `§3.7 examples/docs → 11, 12, 13`)
and verify no §-number appears twice with different subjects.

---

### F3 — residual 1 states the exposure but not its REACHABILITY: no credential *and* no prior knowledge is needed, because `POST /instances` is anonymous and the default instance IDs are a monotonic counter — [MAJOR]

**Bundle text attacked:** ADR Consequences/Negative bullet 1 and spec §4 residual 1:

> ⚠⚠ **Actor attributes reach an UNAUTHENTICATED read surface, and this record does not close it.**
> `GET /instances/{id}/actionable` and `/snapshot` render `Claim.Actor` verbatim and are mounted by
> the same `Mount` with no authorization … Same provenance, **materially different population rate.**

**The failure:** the whole Negative is written about *population rate* — how many deployments will
have attributes in the column. It never states **who can reach the column**, and the answer is
"anybody on the network, with no starting information". Two facts combine:

1. `POST /instances` is unauthenticated (the ADR says so itself) and its 200 body returns the new
   `instance_id`. So an anonymous caller gets one valid ID for free.
2. The default generator is `idgen.XID()` (`runtime/processdriver.go:210`), i.e. `rs/xid` —
   timestamp + machine + pid + **monotonic counter**. Consecutive IDs differ by one in the counter.

⇒ an anonymous caller posts one instance, reads its ID, decrements the counter and walks backwards
through every instance the process minted, `GET /instances/{id}/snapshot` on each, harvesting
`claim.actor.attributes` — the column `SECURITY.md` classifies as personal data — for the whole
deployment. No credential, no guessing, no prior knowledge.

**Evidence:** executed in this worktree.

Exposure, unauthenticated, on a default `stdlib.Mount`:
```
ANONYMOUS GET /instances/audit3-exposure-1/actionable -> 200
{"instance_id":"audit3-exposure-1",...,"claim":{"actor":{"id":"alice","roles":["manager"],
 "attributes":{"bearer":"tok_SECRET_abc123","email":"alice@example.com",
 "employee_id":"E-4471","home_addr":"12 Privet Drive"}},...}}
ANONYMOUS GET /instances/audit3-exposure-1/snapshot   -> 200   (same attributes, plus the definition)
ANONYMOUS GET /instances/audit3-exposure-1            -> 200   (default mapper: no tasks — clean)
```
ID predictability (`go test ./runtime/idgen/...`):
```
three consecutive xids: da6rr3183g3q65o88urg da6rr3183g3q65o88us0 da6rr3183g3q65o88usg
counters: 542647 542648 542649 ; machine=281c07 pid=41751
```

⭐ Two things the probe also **confirms in the bundle's favour** and which should be kept:
`GET /instances/{id}` under the **default** mapper renders no tasks, and `GET /admin/instances`
returns `instanceSummaryView`, which `admin_endpoints.go:35` documents as intentionally omitting
tasks. So the residual's **two-route enumeration is correct** for a default mount — I re-derived it
rather than inheriting it.

**Concrete fix:** add one clause to the Negative and to residual 1: *"and the exposure needs no
prior knowledge — `POST /instances` is anonymous and returns an ID, and the default `idgen.XID()`
mints a monotonic counter, so any caller can enumerate every instance in the deployment."* This is
the sentence that makes the owner's decision informed; "population rate" does not convey it. If that
sentence is unacceptable to ship, the alternative is to withhold `Attributes` (Decision 5) until
ADR-0190 lands, which is the trade the owner already priced — but they priced it without this
clause.

---

### F4 — the dimension rule fails OPEN on the single commonest Go middleware idiom, and both hazards it cites against `Actor{}` remain reachable through shapes it ADMITS — [CRITICAL]

**Bundle text attacked:** ADR Decision 3 / spec §3.3 / plan Task 4 Step 3:

> ⇒ **the rule is: refuse an actor with NO DIMENSIONS AT ALL.** `{ID:"", Roles:["kiosk"]}` passes;
> `{}` does not. The kiosk shape survives; the zero-value bug does not.

and its two stated justifications:

> the resulting claim is durably unattributable and invisible to `AssignedTo("")` … And because
> `Actor{}` carries no attributes, a deny-list `actor.Attributes.*` predicate **ALLOWs** ⇒ round 2's
> fix **reopened the fail-open Decision 5 exists to close.**

**The failure — leg A, the gate is defeated by header parsing.** The rule is
`a.ID == "" && len(a.Roles) == 0 && len(a.Attributes) == 0`. The canonical Go identity middleware is

```go
authz.Actor{ID: r.Header.Get("X-User"), Roles: strings.Split(r.Header.Get("X-Roles"), ",")}
```

On a request with **no headers at all** that yields `Actor{ID:"", Roles:[""]}` — `len(Roles) == 1`,
**one dimension, ADMITTED**. The gate that exists to catch "the commonest middleware bug" is
defeated by the commonest middleware *implementation*. This is the exact `strings.Split("", ",") ==
[""]` mechanism round 2's own Critical **A8** found in the admin role gate; that gate left with the
re-cut, and the mechanism was not carried across to the rule that stayed.

**The failure — leg B, the predicate does not discriminate on either stated hazard.** Both reasons
the ADR gives for refusing `Actor{}` hold *identically* for `{ID:"", Roles:["kiosk"]}`, which it
admits:
- empty ID ⇒ `claim.actor.id` is `""` durably, and `AssignedTo("")` returns nothing **by explicit
  contract** (`humantask/humantask.go:205-213`, `memory.go:69`);
- no attributes ⇒ the deny-list `actor.Attributes.*` predicate **ALLOWs**.

So the rule does not close the fail-open it claims to close; it closes it against one literal value
while leaving it reachable through a shape the same paragraph blesses. The ADR's Positive
*"The audit trail records an identity someone vouched for … a data-integrity fix"* is false for
every admitted empty-ID shape.

**Evidence:** executed. `go test -run TestAudit3_DimensionRuleVsCommonMiddleware ./authz/...`:
```
strings.Split("", ",") = []string{""}  (len=1)
ADR-0189 dimension rule admits this actor? true
deny-list "actor.Attributes.status != \"blocked\"" vs Actor{} (refused by the gate)        -> <nil>   ALLOW
deny-list "actor.Attributes.status != \"blocked\"" vs kiosk {ID:"", Roles:[kiosk]} (ADMITTED) -> <nil>   ALLOW
deny-list "actor.Attributes.status != \"blocked\"" vs header-bug {ID:"", Roles:[""]} (ADMITTED) -> <nil>   ALLOW
deny-list "actor.Attributes.status != \"blocked\"" vs blocked alice                          -> not authorized
```
End-to-end through the real service (`go test -run TestAudit3_DimensionRuleShapes ./transport/http/stdlib/...`):
```
CLAIM by {ID:"", Roles:["manager"]}      -> OK; durable claim.actor.ID=""   AssignedTo("") -> 0 tasks; Complete -> nil
CLAIM by {ID:"   ", Roles:["manager"]}   -> OK; durable claim.actor.ID="   " AssignedTo("   ") -> 1 task
CLAIM by {Attributes:{dept:finance}}     -> REFUSED by the authorizer (role check), NOT by the gate
CLAIM by {ID:"   "}                      -> REFUSED by the authorizer, NOT by the gate
```
⇒ the kiosk claim is durably unattributable *and completable by anyone presenting the same shape*.

**Concrete fix (two parts, both needed):**
1. Make the predicate value-aware, not length-aware: an actor has a dimension only if
   `strings.TrimSpace(a.ID) != ""`, **or** some role is non-blank after `TrimSpace`, **or**
   `len(a.Attributes) > 0`. That refuses `{ID:"", Roles:[""]}` and `{ID:"   "}` while still
   admitting `{ID:"", Roles:["kiosk"]}`. Add both as plan Task 4 rows with the `strings.Split`
   fixture spelled out, since a reviewer will not otherwise see why `[""]`-vs-`[]` matters.
2. Stop justifying the rule with the `AssignedTo`/deny-list hazards — they are **not** closed by it.
   State the honest scope: *"this refuses the literal zero value and blank-only actors; an
   empty-ID kiosk claim remains unattributable and still ALLOWs deny-list attribute predicates,
   which is the price of keeping the repo's kiosk shape"*, and list it in §4 as its own residual.

**Classification for the pre-registered rule:** I read leg A and leg B as a **local defect** in
Decision 3's predicate — the re-cut did not create it, and no other surviving decision was broken
*by* it. ⚠ But note the honest complication: leg B's false claim is an **inter-decision** claim
(Decision 3 × Decision 5), so if the adjudicator counts "a fix whose stated interaction with another
decision is false" as an inter-fix hole, this one qualifies. I state both readings rather than
picking the convenient one.

---

### F5 — 503 is the wrong classification for the attribute-guard failures, and the repo's own cited precedent says 500 — [MAJOR]

**Bundle text attacked:** ADR Decision 5 / spec §3.5:

> Both failures classify **503 `ErrIdentityUnavailable`**, not 400: the fault is the *consumer's
> resolver*, which the HTTP caller cannot correct, and `errors.go` documents twice that a
> caller-uncorrectable fault stays 5xx.

**The failure — three parts.**

1. **The precedent says 500, and the bundle restated it as "5xx".** `transport/http/httpcore/errors.go:19-23`,
   verbatim: *"⚠ Not to be confused with `action/httpcall.ErrBodyTooLarge`, a distinct sentinel
   meaning an OUTBOUND response exceeded httpcall's own cap — **a server-side fault the caller
   cannot correct, which correctly stays a 500**."* That is the repo's one explicit ruling on this
   exact question, and its answer is 500. The bundle generalises it to "5xx" and lands on 503. This
   is the lineage's documented failure mode — an inherited citation restated with its specificity
   stripped — for the third time (round 2's A6 ADR-0148, A7 the `WithCandidateResolveTimeout`
   caveat, and now this).

2. **One sentinel, two lifetimes.** `ErrIdentityUnavailable` covers a *transient* fault (the IdP is
   down — 503 is right) and three *permanent* ones (the resolver returns a `chan int` attribute, a
   20000-deep attribute, an oversize payload). The permanent ones are a code defect in the
   consumer's resolver: every request on that path fails identically until they redeploy. 503 tells
   every client, proxy, LB and service mesh in the path to **retry** — Envoy/Istio retry 5xx and
   503 by default — so a permanently broken resolver becomes a self-amplifying retry storm, and
   `Retry-After` is absent (§4 residual 12 records that but does not connect it to this).

3. **The only other 503 in this repo is `GET /readyz`** — verified: `httpcore/health.go:67` is the
   sole non-test producer, and it means *"pull this instance out of rotation"*. Adding a task-route
   503 for a permanent code defect makes the signal ambiguous to exactly the machinery that consumes
   it. And because `ClassifyError` omits `Message` for 5xx, the operator's response body is a bare
   `{"error":"…"}` and the log line is `writeErr`'s generic `"rest: internal error"` — transient and
   permanent are indistinguishable in both.

**Evidence:** executed.
```
$ grep -rn "StatusServiceUnavailable\|503" --exclude-dir=.git --exclude-dir=docs .
  → only transport/http/httpcore/health.go:38,67 (+ per-adapter readyz tests) and README:1402
$ sed -n '19,23p' transport/http/httpcore/errors.go
  // ⚠ Not to be confused with action/httpcall.ErrBodyTooLarge … a server-side fault the caller
  // cannot correct, which correctly stays a 500.
$ grep -rn 'identity_unavailable' docs/…0189… docs/…spec… docs/…plan…   → (no hits)
```

**Concrete fix:** split the sentinel. Keep **503 `ErrIdentityUnavailable`** for a resolver that
*errored* (transient, retry is correct, `Retry-After` optional). Classify the three guard failures —
unmarshalable, non-round-tripping, oversize — as **500** under a distinct
`ErrIdentityMalformed`, matching the `httpcall.ErrBodyTooLarge` ruling verbatim; a permanent
server-side defect must not invite retry. Add the arm-order statement for the new sentinel (the
standing invariant in `errors.go` requires it) and one `ClassifyError` case per sentinel.

⚠ **Secondary, and independently actionable:** the `ErrorBody.Error` discriminator string for the
503 (and for any new sentinel) **appears nowhere in the bundle** — not in the ADR, not in spec §5,
not in any plan assertion. Since `Message` is empty for 5xx, that string is the client's *only*
machine-readable handle. Plan Task 11 pins cross-adapter parity for `"unauthenticated"` and for
nothing else. Name the string in the ADR and add it to the parity assertion.

---

### F6 — the claim route's 413 contract is pinned in ONE adapter of three, and the plan prescribes no test for the two where the helper is newly written — [MAJOR]

**Bundle text attacked:** plan Task 6 Step 3 and spec §3.6:

> ⚠ Only `stdlib` has the helper; gin and fiber need an equivalent that treats an absent/empty body
> as the zero value **but still honours the size cap**.

and §5, whose only claim-route rows are 13 (*bodyless ⇒ 200*) and 14 (*malformed ⇒ 401*).

**The failure:** the bundle correctly *instructs* the implementer to preserve the cap and then
prescribes **no test that it was preserved**, on the one route whose decode path it rewrites, in the
two adapters where the helper does not yet exist. The failure mode is concrete: a subagent writing
gin's/fiber's optional decoder as `_ = c.ShouldBindJSON(&in)` (the obvious form — ignore the error,
the body is optional) silently drops ADR-0186's 413 on `POST /tasks/{token}/claim` and restores the
unbounded read the cap exists to close.

⭐ **What actually saves this, and the bundle names neither:**
- `stdlib`'s existing `decodeOptionalRequestBody` (`body.go:143-160`) **already** separates the two
  errors — reader error ⇒ 413 via `writeErr`, decode error ⇒ ignored — with a comment saying exactly
  why. So stdlib is safe **if the implementer uses the existing helper**, which Task 6 does say.
- `fiber` has `TestEveryDecodeSiteIsBounded` (`bodylimit_test.go:507-563`), a 13-row enumeration
  that includes `POST /tasks/:token/claim`, asserts 413 and `"request_too_large"`, and carries
  `require.Len(t, cases, 13)`. **That test is the only thing in the repo that would catch the
  regression, and only in fiber.**
- **gin has nothing.** Neither does stdlib as a *test*.

So the guard coverage is asymmetric in the exact direction of the change, and the bundle is unaware
of it — `TestEveryDecodeSiteIsBounded` is named in no bundle document.

**Evidence:** executed.
```
$ grep -rn "claim" transport/http/stdlib/maxbody_test.go transport/http/gin/gin_bodycap_test.go
(none)
$ grep -rn "tasks/" transport/http/*/*_test.go | grep -i "large\|413\|cap\|bound"
(none outside fiber's enumeration)
$ go test -count=1 -run '^TestEveryDecodeSiteIsBounded$' ./transport/http/fiber/...
EXIT=0  ok  github.com/kartaladev/wrkflw/transport/http/fiber
$ grep -rn "413\|oversize\|TooLarge\|EveryDecodeSiteIsBounded" <the three bundle docs>
  → only prose about ADR-0186's window; no prescribed test, no mention of the fiber enumeration.
```

**Concrete fix:** add a §5 row and a Task 6 step — *"per adapter: an OVERSIZE claim body ⇒ 413
`request_too_large`, not 200 and not 401"* — with the mutation stated (write the naive
`_ = bind(&in)` form, observe the fiber enumeration go RED, restore). Name
`fiber/bodylimit_test.go:TestEveryDecodeSiteIsBounded` in Task 6 as the existing guard, and add the
equivalent claim-route row to `stdlib/maxbody_test.go` and `gin_bodycap_test.go` so all three
adapters are pinned rather than one.

---

### F7 — the removal grid RETIRED the resolver-DoS finding by declaring it "materially smaller", and the reasoning is wrong: route count does not divide this attack — [MAJOR]

**Bundle text attacked:** `audit2-0189-removal-grid.md`, row **7 × C**:

> ⚠ **the timeout's blast radius shrinks to three routes.** Round-2 failure-modes noted a slow
> resolver holds a request for `timeout × concurrency`; across 26 routes that was a new DoS surface,
> across 3 it is materially smaller. **Do not restate the larger claim.**

**The failure — two parts.**

1. **The reasoning is wrong.** A denial-of-service surface is not proportional to the number of
   routes that carry it; it is proportional to what one unauthenticated request costs and how easily
   it can be repeated. An attacker does not spread load across 26 routes — they hammer the cheapest
   one. `POST /tasks/anything/claim` costs the attacker one request and costs the server: a capped
   body read (up to 1 MiB / 30 s per ADR-0186), then a resolver call held for up to the **10 s**
   default, typically an outbound call to the consumer's IdP. Per-request cost is **identical**
   whether three routes or twenty-six carry the seam. The blast radius did not shrink at all.

   It is in fact *sharper* than round 2's version, because ADR-0189 puts the resolve **before the
   task lookup** (§5 row 17: *"an unauthenticated request for a nonexistent task ⇒ 401, not 404"*).
   So the attacker needs no valid task token, no instance, no database work — an arbitrary path
   segment triggers the consumer's IdP round trip.

2. **The instruction "do not restate the larger claim" was executed as "do not state it".** The
   hazard appears **nowhere** in the ADR or the spec. Executed:
   ```
   $ grep -rn -i "denial\|dos\|concurrenc\|amplif\|goroutine\|resource" \
       docs/adr/0189-*.md docs/specs/2026-08-25-request-actor-identity.md
   (no hits)
   ```
   §4 residual 5 covers only *"the timeout narrows the hang, it does not close it"* — the
   correctness leg — and says nothing about cost. A round-2 finding was downgraded in an author
   grid and then vanished; the brief's warning about pre-emptive residual labelling as a way of
   *retiring* a finding is exactly what happened here, one level up: it was retired in the grid, so
   it never became a residual at all.

**Concrete fix:** add a §4 residual and an ADR Negative: *"the three task routes let an
unauthenticated caller trigger the consumer's resolver — typically an outbound IdP call — for an
arbitrary, nonexistent task token, held up to `RequestActorTimeout` (default 10 s) per in-flight
request. `WithRequestActorTimeout` bounds the hold for a ctx-honouring resolver and not at all for
one that ignores ctx (residual 5). Consumers should rate-limit the task routes and set the timeout
well below their server's connection budget."* Say it in `SECURITY.md` too — it is the one piece of
advice a consumer can act on. Correct the removal grid's 7 × C cell rather than leaving the wrong
reasoning as an input to ADR-0190.

---

### F8 — "BREAKING in three ways" omits the FOURTH break, and it is the only one that hits every consumer, compiles cleanly, and takes production down — [CRITICAL]

**Bundle text attacked:** ADR Consequences/Negative:

> - **BREAKING in three ways.** Three public DTOs lose a field and `httpcore.Actor` is deleted;
>   three exported endpoint functions gain a parameter, breaking consumer-written adapters; and
>   `ClaimInput` becomes zero-field.

and plan Task 13: *"**`CHANGELOG.md`** — mirror ADR-0186's entry shape … what broke, what to add, a
code snippet for the fix."*

**The failure:** all three listed breaks are **compile-time**, and all three are avoidable by a
consumer who never names the DTOs in Go — which is the *normal* case, since `ClaimInput` etc. are
decoded from JSON inside the library. The break that hits **every** HTTP consumer is unlisted:

> after upgrading, `stdlib.Mount(mux, svc)` still compiles, still starts, and every
> claim/complete/reassign answers **401** until the consumer writes middleware calling
> `authz.ContextWithActor`.

No compiler catches it. No test in the consumer's build catches it unless they have HTTP-level
tests. It is discovered in production, on the three verbs that are the product's human-facing path.

The ADR *knows* the behaviour — the Positive bullet *"Forgetting the seam fails closed at every
task-verb entry: no middleware ⇒ 401"* is exactly this — but it is filed as a **benefit**, and the
BREAKING enumeration that Task 13 mirrors into `CHANGELOG.md` does not carry it. A consumer reading
the changelog's "Breaking changes" section sees three items, none of which applies to them,
concludes the upgrade is safe, and ships.

⚠ The precedent the plan tells the implementer to copy makes this worse, not better. `CHANGELOG.md`
lines 19-38 (ADR-0186) is organised as **"Two observable breaks"** — both *behavioural*
(a new 413; requests that succeed today now fail) — plus an explicit **"Opt out or resize"** snippet.
Mirroring "its shape" onto a list of three *compile* breaks silently drops the behavioural half of
the shape being mirrored.

**Evidence:** executed.
```
$ grep -rn "Mount(\|TaskRoutes\|Customize(" examples/ README.md
examples/production_wiring/main.go:264   stdlib.Mount(mux, svc, httpcore.WithMeterProvider…)
examples/sqlite_wiring/main.go:278       stdlib.Mount(mux, svc)
examples/mysql_wiring/main.go:262        stdlib.Mount(mux, svc)
README.md:273  stdlib.Mount(mux, svc)  // instance + task + message routes
README.md:289  gintransport.Mount(g, svc)      README.md:305  fibertransport.Mount(app, svc)
README.md:327  stdlib.TaskRoutes{Svc: svc}.Customize(mux, …)   README.md:342  gintransport.TaskRoutes{…}.Customize(tasks)
```
Every one of these compiles unchanged after the bundle and 401s at runtime. `MountGroups`
(`httpcore/seam.go:208`, `g.Customize(r)` with **no** options) is documented as *"the consumer
extension seam"* and is in the same position — §4 residual 10 notes it needs a godoc line but does
not count it as a break.

**Concrete fix:**
1. Restate as **"BREAKING in four ways"** and make the fourth the *first* item, phrased
   behaviourally: *"every existing HTTP deployment's claim/complete/reassign answers 401 until
   middleware installs an actor. This does not fail to compile."*
2. Prescribe the CHANGELOG entry explicitly rather than by reference: the observable break, the
   before/after status codes, and the three-line middleware snippet per adapter (with `gc.Request =
   gc.Request.WithContext` and `c.SetContext`, never `gc.Set`/`c.Locals`).
3. `STABILITY.md`: state that `RequestActorFunc` defaults to the context seam and that the absence
   of an actor is a refusal, not a downgrade — beside the ADR-0186 subsection, per the plan.

---

### F9 — the widened doc-sweep net STILL cannot reach the README lines the plan itself names as the reason for widening it — [MAJOR]

**Bundle text attacked:** plan Task 13:

> ⚠ **The doc sweep needs a wider net than `grep '"actor"'`.** Round 2's counting and failure-modes
> lenses both found live doc sites that net cannot reach — including `docs/adr/0146:12`'s
> `httpcore.CompleteInput{Actor, Output}` **and the README's headline `stdlib.Mount(mux, svc)`.**
> Run all of: [three greps]

**The failure:** the three greps are `'"actor"\|"by"'`, `'httpcore\.Actor\|ClaimInput{\|CompleteInput{\|ReassignInput{'`
and `'ClaimTask(\|CompleteTask(\|ReassignTask('`. **None of them matches the string
`stdlib.Mount(mux, svc)`** — the very site the sentence above them cites. The widened net catches
`docs/adr/0146:12` (the second grep) and misses the README entirely. An accepted round-2 MAJOR was
answered with a fix that does not perform the function it was accepted for, and the citation of the
miss sits three lines above the greps that still miss it.

**Evidence:** executed — all three nets, run verbatim, over `README.md docs/ examples/ SECURITY.md`:
```
net 1  '"actor"\|"by"'                      → 0 hits in README.md (only docs/plans/, docs/adr/)
net 2  'httpcore\.Actor\|…Input{'           → 0 hits in README.md
net 3  'ClaimTask(\|CompleteTask(\|…'       → 0 hits in README.md
$ grep -n "stdlib.Mount(mux, svc)\|TaskRoutes{Svc: svc}" README.md
273:stdlib.Mount(mux, svc)           // instance + task + message routes
327:stdlib.TaskRoutes{Svc: svc}.Customize(mux, httpcore.WithBasePath…)
342:gintransport.TaskRoutes{Svc: svc}.Customize(tasks)
```
Six live README mount snippets (273, 289, 305, 327, 342, 358) and three `examples/*_wiring`
mount sites become misleading — they show a mount that no longer serves the task verbs — and the
sweep cannot see any of them.

**Concrete fix:** add a fourth net that keys on the *mount*, not the removed field:
```bash
grep -rn '\.Mount(\|MountGroups(\|TaskRoutes{' README.md docs/ examples/ SECURITY.md
```
and require every hit that mounts task routes to gain the `WithRequestActor` line or an explicit
"see SECURITY.md for the identity middleware" pointer. State the expected hit count so the sweep is
falsifiable.

---

### F10 — "a malformed claim answers 401" is true only for the UNAUTHENTICATED half; authenticated, it answers **200** and the claim succeeds — [MAJOR]

**Bundle text attacked:** three places state it as one fact —
ADR Negative: *"the optional claim decoder swallows every decode error, so a **malformed** claim
answers 401 rather than 400. That IS a change, and it is the honest reading of 'fail closed'."*
Spec §4 residual 8, same sentence. Spec §5 row 14: *"a **malformed** claim body ⇒ 401 (the optional
decoder swallows it)"*.

**The failure:** the ordering is decode → resolve. The optional decoder discards the decode error
and the request *proceeds*; the 401 then comes from the **actor resolution**, not from the body. So
401 is what a malformed body produces **only when the request is also unauthenticated**. With an
authenticated request — the normal case — a malformed body produces **200 and a successful claim**,
where today it produces a loud **400**.

That is a strictly worse migration story than the one the bundle tells: a client whose serializer
breaks used to get a 400 it could act on and now silently succeeds. And §5 row 14 as written **can
pass while the real behaviour is untested**: an unauthenticated fixture satisfies the assertion for
the wrong reason — the same "fixture from the half that works" defect the ADR itself calls out twice
(round 2's `chan int`, round 1's ctx-honouring resolver). This is the third instance in one lineage.

**Evidence:** executed today against the helper Task 6 moves the claim route onto —
`/admin/instances/{id}/incidents/{incidentID}/resolve`, the repo's only existing
`decodeOptionalRequestBody` call site (`stdlib/groups.go:234`):
```
optional-decode route, body="{\"add_attempts\":1}" -> 200 {...}
optional-decode route, body="NOT JSON AT ALL"      -> 200 {...}   ← malformed, SWALLOWED, proceeds
optional-decode route, body=""                     -> 200 {...}
REQUIRED-decode claim route, body="NOT JSON AT ALL" -> 400 {"error":"bad_request","message":"…invalid character 'N'…"}
REQUIRED-decode claim route, body=""                -> 400 {"error":"bad_request","message":"…EOF"}
```
⇒ once the claim route uses the optional helper, the malformed body no longer contributes to the
status at all; the actor does.

**Concrete fix:** correct the sentence in all three places to *"a malformed claim body is IGNORED:
unauthenticated it answers 401 (from the actor, not the body), authenticated it answers **200** and
the claim proceeds — where today both answer 400."* Split §5 row 14 into **two** rows,
authenticated and unauthenticated, and state what makes each fail today (both: 400). The
authenticated row is the one that pins the actual behaviour change; without it the row is
satisfiable by the half that already works.

---

### F11 — the one-level clone plus the new round-trip guard lets a consumer's shared nested map be iterated on the request path: `fatal error: concurrent map iteration and map write`, which `recover()` cannot catch — [CRITICAL]

**Bundle text attacked:** §4 residual 7 and ADR Decision 1:

> 7. **The clone guarantee is one level deep** (§3.1). Nested attribute values stay shared.

> ⚠ **The clone guarantee is one level deep, and the record says so.** … Claiming full isolation
> would be false for exactly the payload Decision 5 admits.

**The failure:** the residual is written as an *isolation* nicety — "a later mutation by the caller
is visible". Its actual worst case is a **process-wide, unrecoverable crash**, and this bundle
creates a new site for it.

The realistic middleware ADR-0189 asks consumers to write is:

```go
profile := identityCache.Get(userID)                    // shared, refreshed in the background
ctx = authz.ContextWithActor(ctx, authz.Actor{
    ID: userID, Attributes: map[string]any{"profile": profile.Attrs}})   // nested map: SHARED
```

`ContextWithActor` clones one level, so `profile.Attrs` is still the cache's map. Decision 5 then
**marshals** it — `json.Marshal(a.Attributes)` in `resolveRequestActor`, on **every** claim,
complete and reassign — and the store marshals it again on write. If the cache refreshes that map
concurrently, the marshal iterates a map under write and the Go runtime **throws**:

```
fatal error: concurrent map iteration and map write
goroutine 1 [running]:
internal/runtime/maps.fatal(...)
reflect.(*MapIter).Next(...)
encoding/json.mapEncoder.encode(.../encoding/json/encode.go:788)
...
EXIT=1
```
Executed (`go run` in a scratch module, 3-second loop). Note the deferred `recover()` in that
program **never ran** — this is a runtime throw, not a panic. It takes down the whole server, every
in-flight request with it.

Over HTTP this hazard is **new**. Today `httpcore` builds `authz.Actor{ID, Roles}` from the body —
freshly decoded, unshared, no attributes at all — so no consumer-owned map is reachable. Decision 5
opens the channel and Decision 1's one-level clone declines to close it. The bundle applies exactly
this "pre-existing channel, materially different population rate" reasoning to the *read exposure*
(residual 1) and does not apply it here at all.

**Classification for the pre-registered rule — this one IS an inter-fix hole.** The one-level-clone
honesty was round 2's fix to round 1's over-claim; the round-trip guard is round 3's fix to round
2's Critical A1. Each is correct alone. Together, fix (b) adds a per-request iteration of memory
fix (a) deliberately declined to copy. Neither document derives the pair — the removal grid derives
survivor × removed, not survivor × survivor.

**Concrete fix — pick one, but pick deliberately:**
- **Preferred:** make the guard's round trip the *value the engine uses*. `resolveRequestActor`
  already marshals and unmarshals; assign the decoded `round` map back onto the actor's
  `Attributes`. That gives a genuinely private deep copy for free, closes this crash, and closes
  residual 7 as a bonus — one line, no new API.
  ⚠ It changes the attribute *values* the authorizer sees (see F12), so the two must be decided
  together.
- **Otherwise:** state the hazard explicitly in residual 7 and in `SECURITY.md` —
  *"the resolver must hand the engine attributes no other goroutine will write; a shared or cached
  nested map can crash the process"* — and add it to the ADR's Negative list. A crash mode that is
  documented is survivable; one that is filed as an isolation footnote is not.

---

### F12 — the "ROUND-TRIP guard" proves decodability, not fidelity: the durable audit record can differ from the identity the authorizer evaluated — [MINOR]

**Bundle text attacked:** ADR Decision 5 / spec §3.5 / plan Task 4 Step 3:

> ⇒ the seam **marshals and then unmarshals** the attributes, rejecting on either error

and the Positive: *"The audit trail records an identity someone vouched for … that is a
data-integrity fix as much as an access-control one."*

**The failure:** the guard discards `round` and returns the **original** `a`. So authorization runs
on the in-memory values while the store persists — and every later read returns — the JSON
projection of them. For any value JSON cannot represent exactly, the two diverge, and the guard
passes because both operations *succeeded*.

**Evidence:** executed (`go test -run TestAudit3_RoundTripGuardIsNotFidelity ./authz/...`):
```
guard result: PASSES (both marshal and unmarshal succeed)
in-memory  (what the authorizer evaluates): map[clearance:4611686018427387904 level:9007199254740993]
round-trip (what the store persists/reads): map[clearance:4.611686018427388e+18 level:9.007199254740992e+15]
```
The audit record says the claimant's `level` was `9007199254740992`; the claimant asserted
`9007199254740993`. The ADR's own data-integrity Positive is the claim this contradicts.

**Concrete fix:** the one-line fix in F11 resolves this too — assign `round` back onto the actor so
the authorizer, the engine and the store all see the *same* values, and the divergence cannot exist.
If that is rejected, say plainly in Decision 5 that the guard proves the attributes **decode**, not
that they survive unchanged, and add a residual naming the divergence.

---

### F13 — the 401 without `WWW-Authenticate` violates RFC 9110's MUST, and is filed as a residual rather than fixed — [MINOR]

**Bundle text attacked:** §4 residual 12 / ADR Negative: *"No `WWW-Authenticate` on the 401, no
`Retry-After` on the 503."*

**The failure:** RFC 9110 §15.5.2 makes `WWW-Authenticate` a **MUST** on a 401 response, not an
optional nicety. A conforming HTTP client (and several proxies and API gateways) treats a 401
without it as a protocol error or an opaque failure it cannot retry with credentials. This bundle
*creates* the repo's first 401, so it is not inheriting the defect — it is introducing one and
recording it.

The library cannot know the consumer's scheme, which is a real constraint — but that argues for
letting the consumer supply the value, not for omitting the header. And it interacts with F5: the
503 likewise ships with no `Retry-After` while inviting retry.

**Concrete fix:** either emit a defensible default (`WWW-Authenticate: Bearer`) or add one field —
`CustomizeConfig.WWWAuthenticate string`, emitted on the 401 when non-empty — and document that a
consumer using a non-Bearer scheme should set it. One field, one option alias per adapter, and the
residual disappears instead of shipping.

---

## Verdict

**13 findings: 4 CRITICAL, 7 MAJOR, 2 MINOR.**

| # | claim | severity |
|---|---|---|
| F1 | plan Task 13 prescribes a FALSE `SECURITY.md` claim — that `InstanceRoutes`/`MessageRoutes` "authenticate but do not authorize", left over from the removed decision C | CRITICAL |
| F2 | the plan's self-review table still maps two REMOVED decisions (group authentication, the admin role gate) onto surviving Tasks 8–11 | MAJOR |
| F3 | residual 1 states the anonymous-read exposure but not its reachability: `POST /instances` is anonymous and default `idgen.XID()` IDs are a monotonic counter ⇒ the whole deployment is enumerable | MAJOR |
| F4 | the dimension rule admits `{ID:"", Roles:[""]}` — what `strings.Split("", ",")` produces on a header-less request — and both hazards it cites against `Actor{}` remain reachable through shapes it admits | CRITICAL |
| F5 | 503 for the attribute-guard failures: permanent faults invited to retry, and the repo's own cited precedent (`httpcall.ErrBodyTooLarge`) says **500**; the 503's `ErrorBody.Error` string is unspecified bundle-wide | MAJOR |
| F6 | the claim route's 413 contract is pinned in fiber only, and the plan prescribes no oversize test for the two adapters whose optional-decode helper is newly written | MAJOR |
| F7 | the removal grid retired the resolver-DoS finding as "materially smaller"; route count does not divide this attack, and the hazard then vanished from the ADR and spec entirely | MAJOR |
| F8 | "BREAKING in three ways" omits the fourth — every existing HTTP deployment 401s after upgrade, compiles cleanly, and the CHANGELOG precedent being mirrored is behavioural | CRITICAL |
| F9 | the widened doc-sweep net still cannot reach the README mount snippets the plan itself names as the reason for widening it | MAJOR |
| F10 | "a malformed claim answers 401" holds only unauthenticated; authenticated it answers **200** and the claim succeeds, where today both answer 400 | MAJOR |
| F11 | one-level clone + the new round-trip guard ⇒ a consumer's shared nested map is iterated per request: `fatal error: concurrent map iteration and map write`, unrecoverable | CRITICAL |
| F12 | the "round-trip guard" proves decodability, not fidelity — the durable audit record can differ from the identity the authorizer evaluated | MINOR |
| F13 | the 401 ships with no `WWW-Authenticate`, violating RFC 9110's MUST, filed as a residual rather than fixed | MINOR |

### Criticals — local defect or inter-fix hole (the pre-registered rule turns on this)

- **F1 — INTER-FIX HOLE.** It is a direct artifact of the re-cut: the removal of decision C left a
  sentence prescribing content that C alone made true. The removal grid's row 3 × C predicted the
  class and the hunt was not run over the plan's prescribed doc content.
- **F4 — LOCAL DEFECT**, with an honest complication. The predicate itself is a local error in
  Decision 3. But leg B's false justification is an explicit Decision 3 × Decision 5 claim, so an
  adjudicator who counts "a fix whose stated interaction with another decision is false" as
  inter-fix would classify it that way. I do not claim the convenient reading.
- **F8 — LOCAL DEFECT.** A missing enumeration entry and missing CHANGELOG content; no other
  decision caused it.
- **F11 — INTER-FIX HOLE, unambiguously.** Round 2's fix (state the clone is one level deep, do not
  deep-copy) and round 3's fix (add a round-trip marshal to the request path) are each correct
  alone; together they iterate memory the consumer still owns, on every task request. Nothing in
  either document derives the pair, because both grids derive survivor × *removed*, never
  survivor × survivor.

⇒ **Two of my four Criticals are inter-fix holes.** Under the removal grid's pre-registered rule —
*"Criticals/lens ≥ 3, **or any Critical is again an inter-fix hole** ⇒ stop and escalate to the
owner"* — this lens fires the third row on both clauses independently (4 ≥ 3, and F1 + F11).

### What HELD — do not re-litigate

- **Residual 1's two-route enumeration is CORRECT.** I re-derived it rather than inheriting it:
  `GET /instances/{id}` under the default mapper renders no tasks, and `GET /admin/instances`
  returns `instanceSummaryView`, documented at `admin_endpoints.go:35` as omitting tasks. Only
  `/actionable` and `/snapshot` render `Claim.Actor` on a default mount.
- **`SECURITY.md:125` does classify `actor` as personal data** — *"identifies a human principal.
  Treat as personal data."* The ADR's citation is exact.
- **stdlib's `decodeOptionalRequestBody` already preserves the 413** (`body.go:143-160`), separating
  the reader error from the decode error with a comment saying why. My hypothesis that the optional
  body would swallow ADR-0186's 413 is **REFUTED for stdlib** — the risk is only in the gin and
  fiber helpers that do not yet exist (F6).
- **`ErrIdentityUnavailable` classified before the 404 arm is right**, and `errors.go`'s standing
  invariant does require the co-match test the bundle ships. The ordering analysis survives.
- **The kiosk shape works end to end** — `svc.ClaimTask` with `{ID:"", Roles:["manager"]}` succeeds
  and the claim is completable — so Decision 3 is correct that round 1's empty-ID refusal would have
  deleted a working case. The disagreement in F4 is about the *predicate*, not the intent.
