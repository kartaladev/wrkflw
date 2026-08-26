# ADR-0189 round-2 audit — INTERACTION lens

Worktree: `.../scratchpad/wt2-interaction`, detached at `37d77a34`.
Step 0: all four bundle files present. ✔

Lens question: **"what does this decision assume someone else will hand it, and who agreed
to that?"** Changed decisions (a)–(h) per the brief; the author's own grid
(`docs/plans/sweep-evidence/audit-0189-author-interaction-grid.md`) is an INPUT, attacked here.

Naming: **(a)** removal of the empty-`Actor.ID` refusal · **(b)** all groups except
`HealthRoutes` refuse an unresolved actor (401) · **(c)** opt-in `WithAdminRoles` role gate
(403) · **(d)** `Attributes` keep flowing + a `json.Marshal` pre-check returning `ErrBadInput`
(400) · **(e)** the claim route accepts an absent body · **(f)** a 10s resolver timeout ·
**(g)** two resolution placements (task routes post-decode, others pre-decode) · **(h)** the
"which other ADRs are amended" claims. The author's grid letters A–K map: A=(d), B=(a),
C=(b), D=(c), E=(g), F=(e). ⚠ **The grid has no letter for (f), the resolver timeout** — see F7.

---

### F1 — The grid's "no existing correct wiring breaks" is refuted by this repo's own reference wiring: (b)'s 401 breaks the ADR-0095-compliant admin consumer that (c) was made opt-in to protect — [CRITICAL]
**Pair:** (b) × (c)  — the author's grid cell ⚠5 (C×D)

**What (c) assumes (b) hands it:** the grid's ⚠5 makes `WithAdminRoles` **opt-in** on one
argument only: *"a fail-closed default would have returned 403 to the consumer who followed the
documented advice and already secured that mux"*, concluding **"No existing correct wiring
breaks, and the admin surface is still strictly better than today."** That conclusion assumes
(b) leaves the ADR-0095-compliant consumer working.

**Why that assumption fails:** (b) is **not** opt-in. The same grid sentence concedes it —
*"undeclared, `AdminRoutes` inherits C's 401 refusal"* — and then, in the next clause, asserts
nothing breaks. Both halves cannot be true. A consumer who did exactly what ADR-0095 and
`SECURITY.md` instruct (mount `AdminRoutes` behind their own auth middleware) has middleware
that authenticates but never calls `authz.ContextWithActor` — it cannot, the function does not
exist yet. So after (b) **100 % of their admin traffic returns 401**, and (c)'s opt-in-ness buys
them nothing, because the ADR itself orders **401 before 403**. The one property opt-in D was
purchased to preserve is destroyed by C before D is ever reached.

**Evidence (executed reads, this worktree at `37d77a34`):**
- `examples/production_wiring/main.go:273-275` — the repo's OWN reference wiring does precisely
  this: `stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, …)` then
  `mux.Handle("/admin/", requireAdminToken(adminMux, os.Getenv("ADMIN_TOKEN"), logger))`. Its
  comment at :266 reads *"AdminRoutes has NO built-in authentication (ADR-0095:
  admin-by-composition)."*
- `requireAdminToken` (`main.go:75-88`) is a real constant-time `X-Admin-Token` check. It sets
  **no context value**. After (b) it becomes decorative: every request it lets through 401s.
- `SECURITY.md:37-39` states the shipped contract: *"Admin endpoints are default-absent by
  composition (ADR-0095): they exist only when you mount `AdminRoutes` on a router group that
  your own auth middleware already protects. **They carry no built-in authentication.**"*
- `docs/adr/0095-multiframework-mount-adapters.md:161-165`: *"**Default-absent** replaces the
  old default-deny (403) … **This is safer than a built-in default-deny gate** and idiomatic per
  framework."* And its friction-point #1 (:14-17) names as a defect the very shape (c) restores:
  *"The admin group … was protected by a single `WithAdminMiddleware` option that had to apply
  equally to every admin endpoint."*

**Concrete fix (three parts, all required):**
1. Delete the sentence *"No existing correct wiring breaks"* from the grid and do not let it
   reach the ADR. The true sentence is: *every* ADR-0095-compliant admin mount breaks and must
   adopt the seam.
2. **Re-derive the opt-in decision on the corrected premise.** If the ADR-0095 consumer must
   adopt `WithRequestActor` regardless, "declaring `WithAdminRoles` too" is a marginal extra
   step, and the argument for opt-in evaporates. Either keep opt-in for a *different*, stated
   reason, or make the gate fail-closed — but not on this reason.
3. **ADR-0095 must be added to the `Amends:` list** and its Decision annotated in place. This
   record reverts "default-absent, not default-deny" to a built-in default-deny (401), and
   reinstates the single-option-for-all-admin-endpoints shape ADR-0095 removed. `SECURITY.md`
   :37-39 becomes false and must be rewritten in the same bundle (plan Task 17 currently adds a
   *new* scope note and never touches this one).

---

### F2 — Claim (h) "which ADRs need amendment" omits ADR-0095, the one ADR this bundle actually contradicts — [CRITICAL]
**Pair:** (h) × (b) and (h) × (c)

**What (h) assumes:** the bundle enumerates its ADR obligations exhaustively — ADR-0147 #5 is
amended, ADR-0187 is *"unchanged … needs no amendment"*, ADR-0117 *"needs no amendment"*, and
ADR-0095 appears only in the Relates-to line as *"(mountable transports)"*.

**Why that assumption fails:** ADR-0095 is the record that **decided** admin protection posture,
and (b)+(c) contradict its Decision and two of its stated Positive consequences (quoted in F1).
The bundle reaches for `stdlib.Mount`'s **godoc** — a derived artifact — as its authority on
admin posture and never opens the ADR the godoc summarizes. The godoc happens to support the
opt-in choice; the ADR does not, and says the opposite ("safer than a built-in default-deny
gate"). This is an inherited-citation failure of exactly the class CLAUDE.md's Premise
Discipline warns about: the hedge lives in the ADR, the restatement lives in the godoc, and only
the restatement was consulted.

**Evidence:** `grep -n "0095" docs/adr/0189-*.md docs/specs/2026-08-25-*.md
docs/plans/2026-08-25-*.md docs/plans/sweep-evidence/audit-0189-author-interaction-grid.md`
→ ADR-0095 appears **once**, in the ADR's Relates-to line, glossed "(mountable transports)".
The grid never mentions it. See F1 for the contradicting text.

**Concrete fix:** add `**Amends: ADR-0095**` with the specific sentences (friction point #1;
"Admin-by-composition (default-absent)"; the Positive bullet *"Admin endpoints are absent (not
403) unless explicitly mounted on a secured group, which is safer and clearer"*), annotate them
in place per the ADR-0147 precedent this bundle already follows, and state in ADR-0189 why the
posture is being reversed.

---

### F3 — (b) is a THIRD breaking change the §2.6 member-set ablation structurally cannot see; the "48 lines / 13 files / 6 packages" blast radius omits it entirely — [CRITICAL]
**Pair:** (b) × G (the blast-radius / member-set method)

**What G assumes (b) hands it:** the grid states *"G, H, I, J, K are documentation/test changes
with no decision surface; each was checked against A–F and none interacts. That check is itself
recorded so a re-auditor need not redo it."* G is the blast radius. It interacts with C=(b) by
construction, because (b) changes the runtime behaviour of every route group.

**Why that assumption fails:** §2.6 derives the member set with
`go build ./...` + `go test -count=1 -gcflags=-e -run '^$' ./...` — a **compile** ablation, run
against a tree where *"both changes [are] modelled"*, i.e. the DTO field removal and the
endpoint-signature change. (b) changes **no signature**. It is invisible to both commands by
construction. So §2.6's headline — presented as the delivery's most transferable lesson,
*"A count is re-derived only when its MEMBER SET is re-derived"* — repeats round 1's error one
level up: round 1 modelled one of two breaking changes; round 2 models two of **three**.

**Evidence (counted in this worktree):**
- Path literals for instance / message / admin routes in the four adapter test packages:
  `stdlib` 69, `gin` 74, `fiber` 63, `parity` 20 = **226**. Every request they drive returns
  401 after (b) unless its mount site installs a resolver.
- Mount sites in those tests that must gain a `WithRequestActor` option:
  `AdminRoutes{` **82**, `InstanceRoutes{` 6, `MessageRoutes{` 1, plus **79**
  `Mount(`/`MountGroups(`/`MountHealth(` calls.
- The §2.6 member set contains **zero** admin, instance or message test lines. Its five `stdlib`
  runtime members (`errors_test.go:155,187`, `stdlib_test.go:471`, `coverage_test.go:92,126`)
  are all task-route actor pins.
- The ADR's own Consequences names this as breaking — *"every mounted route group except Health
  starts answering 401 without authentication"* — so the bundle knows the change is breaking and
  still reports a member set that excludes it.
- Plan Task 5's note *"Between Task 5 and Task 14 the adapter suites are RED by design — the 23
  runtime pins still send `"actor"`/`"by"` and now get 401"* accounts only for the task pins.
  Tasks 12–14 migrate only task pins. **No task in the plan migrates the instance / message /
  admin pins**, and Task 16's verification is the first place the omission would surface.

**Concrete fix:** re-run the ablation with (b) modelled — the cheapest faithful stub is to make
`RequireActor` return `ErrUnauthenticated` unconditionally and insert it in the three groups —
then `go test -count=1 ./transport/... ` and paste the failing member set. Publish the corrected
total. Add explicit plan tasks for the instance / message / admin test migration, sized from that
set, and state up front that they are **runtime-only** failures no compile check will catch.

---

### F4 — The author's grid has no axis for (f), the resolver timeout, yet claims its coverage is complete — [MAJOR]
**Pair:** (f) × everything

**What the grid assumes:** its axes are A–F plus a declared-inert G–K, and it states the G–K
check *"is itself recorded so a re-auditor need not redo it."* That is a completeness claim.

**Why that assumption fails:** the resolver timeout is a **changed decision** — the spec's own
§6 re-audit brief lists it as change **(f)**, *"a resolver timeout is adopted"* — and it appears
nowhere in the grid: not as an axis, not among G–K, not in the unresolved list. A whole decision
was omitted from a pass whose entire purpose is exhaustiveness over the changed set. Its live
interactions are real (see F7 for (f) × (g)).

**Evidence:** `grep -in "timeout" docs/plans/sweep-evidence/audit-0189-author-interaction-grid.md`
→ **0 matches**. The spec §6 item 6 enumerates six changes (a)–(f); the grid's table enumerates
eleven (A–K) and none is the timeout.

**Concrete fix:** add the timeout as a grid axis and derive it against A–F before the ADR is
declared audited. At minimum record (f)×(g) (F7 below) and (f)×(b) (the timeout now applies to
every route group, not only the three task routes it was justified on).

---

### F5 — The resolver timeout cannot reach the three task endpoints: (f) lives on `CustomizeConfig`, and Decision 2 deliberately denies those endpoints any sight of it — [CRITICAL]
**Pair:** (f) × (g) / Decision 2's parameter shape

**What (f) assumes (g) hands it:** that whoever calls `resolveRequestActor` can supply
`cfg.RequestActorTimeout`. Plan Task 4 makes the helper
`resolveRequestActor(ctx context.Context, resolve RequestActorFunc, timeout time.Duration)`, and
Task 3 puts `RequestActorTimeout` on `CustomizeConfig` behind `WithRequestActorTimeout[R]`.

**Why that assumption fails:** the three task endpoints receive **only the resolver**, by an
explicit design choice. ADR Decision 2 and spec §3.2 fix the signature as
`ClaimTask(ctx, svc, token, in, mapper, actor RequestActorFunc)` — no timeout — and spec §1.1
records *why* passing the config was rejected: it *"would make the endpoints generic over a
router type they never use."* So inside `ClaimTask` there is no `cfg` and no `timeout` in scope.
The implementer's only options are:
  (i) hardcode the 10 s constant ⇒ **`WithRequestActorTimeout` is silently ignored on the three
      task routes** — an option that appears to work and does nothing, the fail-open shape;
  (ii) add a **second** parameter ⇒ falsifies the claim repeated three times across the ADR,
      spec §2.4 and plan Task 5 that *"the nine adapter call sites each gain **one** argument and
      no branch"*; or
  (iii) pre-bind the timeout into the closure in each of the nine adapter call sites ⇒ nine
      places to get it wrong, and the "no branch" claim goes with it.
None of the three is chosen anywhere in the bundle, and the plan's task decomposition assumes
(ii)-without-admitting-it in Task 4 and (i)-without-admitting-it in Task 5.

⚠ Worse: the routes that cannot see the timeout are **exactly the routes (f) was justified on**.
The hang hazard is argued from fiber's `c.Context() == context.Background()`, and fiber's task
routes are three of the nine call sites.

**Evidence:** ADR Decision 2 + spec §3.2 (signature, six params) vs. plan Task 3 (config field)
and Task 4 (three-arg helper). Precedent shape, read in this worktree: both existing timeouts
are **fields on the struct that owns the resolve site** —
`runtime/processdriver.go:70 candidateResolveTimeout` used at
`runtime/processdriver_action.go:298`, and `runtime/task/service.go:39` used at `:322-325`.
Neither is a free function, so the precedent the ADR cites does not transfer to `httpcore`'s
free-function endpoints without exactly the parameter the design refuses.

**Concrete fix:** pick one and write it down. Cleanest: make the endpoint parameter carry the
bound rather than the raw func — e.g. pass a small `RequestActorResolver struct{ Fn
RequestActorFunc; Timeout time.Duration }`, or have the adapters pass an already-bounded closure
built once at `Customize` time. Then correct the "one argument, no branch" sentence in all three
documents, and add a test that `WithRequestActorTimeout` actually bounds a **task-route**
resolver — the current plan's only timeout test is in `seam_test.go`/`resolve_actor_internal_test.go`
and would pass under option (i) while the option is dead on the task routes.

---

### F6 — (a) re-opens, for the `Actor{}` shape, the exact deny-list fail-open (d) is sold as closing — and the grid marked this pair "·, no interaction" — [MAJOR]
**Pair:** (a) × (d)  — the author's grid cell **A×B, marked `·`**

**What (d) assumes (a) hands it:** ADR Decision 5 / spec §3.5 present a class-level table —
deny-list `actor.Attributes.status != "blocked"` goes from **ALLOW** (*"live fail-open at
`main`"*) to **DENY**. That table is unqualified, and the ADR's first Positive bullet restates
it as *"**A live fail-open closes**: deny-list actor-attribute predicates currently ALLOW over
HTTP."* It assumes every actor reaching the authorizer carries the attributes the predicate
names.

**Why that assumption fails:** (a) makes `Actor{}` a legal, gate-passing shape. `Actor{}` has
nil `Attributes`, so the deny-list predicate evaluates on a missing field and **allows** —
byte-identical to today's dropped-attributes behaviour. Round 1's empty-ID refusal made that
shape unreachable over HTTP; removing the refusal makes it reachable again. The two changes are
not independent: (d) closes the hole for actors the resolver populated, (a) guarantees a
populated-by-nobody actor that passes every gate.

**Evidence — executed** (`authz/zzprobe_interaction_test.go`, run then deleted;
`go test -count=1 -v -run '^TestZZProbeDenyListAgainstActorShapes$' ./authz/...`, EXIT=0):
```
PROBE deny-list  zero actor (post-(a) legal shape)             -> ALLOW
PROBE deny-list  empty ID, no attributes                       -> ALLOW
PROBE deny-list  empty ID, blocked                             -> DENY (workflow-authz: not authorized)
PROBE deny-list  alice, attributes DROPPED (today's HTTP path) -> ALLOW
PROBE deny-list  alice, blocked                                -> DENY (workflow-authz: not authorized)
PROBE deny-list  alice, empty attr map                         -> ALLOW
```
Note there is no fail-closed rescue: a missing attribute does **not** raise an expr error that
`RoleAuthorizer` would wrap in `ErrNotAuthorized`; `nil != "blocked"` is simply true.
(The allow-list row behaves as the ADR says: every attribute-less shape denies.)

**Concrete fix:** qualify the §3.5 / Decision 5 table — the DENY cell holds **only when the
resolver populates the named attribute**; for an attribute-less actor, including the `Actor{}`
that (a) legalises, the deny-list class still ALLOWs. Say so in the same table, not in a residual
three sections away. Then re-file the new backlog item accordingly: it is *"deny-list actor
predicates allow when the attribute is absent, and (a) guarantees an absent-everything actor is
reachable over HTTP"*, not merely *"the deny-list fail-open"*. Un-mark grid cell A×B.

---

### F7 — A fault inside the consumer's resolver is reported to the client as **400 bad_request**, indistinguishable from a malformed body, and leaks the internal error the sibling 503 path deliberately suppresses — [MAJOR]
**Pair:** (d) × Decision 4 (and (d) × (e))

**What (d) assumes:** that `ErrBadInput` (400) is the right classification for an actor whose
`Attributes` will not `json.Marshal`.

**Why that assumption fails:** Decision 4 spends its whole rationale establishing the opposite
principle for the same seam — a resolver returning `authz.ErrNotAuthorized` classifies **503,
not 403**, because *"an identity resolver answers who, not may; a 403 is an audited decision
about a **known** principal."* The marshalability failure is produced by the **same arbitrary
consumer code**, on data the client never supplied and cannot change. Classifying it 400 tells
the client "fix your request" for a fault entirely inside the deployment's authentication path,
and every request from every client fails identically — an outage rendered as client noise, which
is also how it will be triaged (4xx dashboards, not 5xx alerting).

Two further consequences the bundle does not state:
- (d) × (e): with the claim route now accepting an absent body, a caller sending **no body at
  all** can still receive `400 bad_request` — a status that, by its envelope, asserts something
  about a body that does not exist. Nothing in the response distinguishes the two causes.
- The 400 arm carries `Message: err.Error()`. The 503 arm carries none, and the plan's own Task 2
  test asserts `assert.Empty(t, body.Message, "5xx must never carry the raw error")`. So the
  consumer's internal error text is suppressed on one resolver-fault path and echoed on the other.

**Evidence — executed** (`transport/http/httpcore/zzprobe_classify_test.go`, run then deleted,
EXIT=0), classifying exactly the error plan Task 4's helper returns beside a real malformed-body
error:
```
PROBE resolver produced a non-marshalable actor  -> status=400 error="bad_request" message="workflow-httpcore: bad input: actor attributes are not JSON-serialisable: json: unsupported type: chan int"
PROBE client sent malformed JSON                 -> status=400 error="bad_request" message="workflow-httpcore: bad input: unexpected end of JSON input"
```
Identical `status` and identical `error` discriminator; the fault attribution exists only in
free text, and the resolver-internals leak is in the half that should be hidden.

**Concrete fix:** classify the pre-check failure as a **server-side identity fault**, not client
input — wrap it in `ErrIdentityUnavailable` so it lands on the 503 arm with a suppressed message,
which is both the correct attribution and consistent with Decision 4's own stated rule. If the
owner prefers a distinct status, give it its own sentinel and its own `error` discriminator so a
client can tell the two apart, and drop `err.Error()` from the body. Either way, remove the
`ErrBadInput` classification: it is the one choice that is wrong on all three counts.

---

### F8 — The removal (a) rests on an INHERITED citation that does not hold, and it admits a wider shape than the one the repo actually blesses — [CRITICAL]
**Pair:** (a) × (h)  — and it re-opens grid cells ⚠3 (B×C), ⚠4 (B×D)

**What (a) assumes someone else agreed to:** ADR Decision 3 and spec §3.3 justify removing the
empty-`Actor.ID` refusal entirely on two citations —
*"`humantask/validate.go:24` and ADR-0183:69-76 call the empty-claimant ('kiosk') shape
**'deliberately legal'**, and ADR-0183 explicitly **declined** to supersede ADR-0148 on it."*
Both documents attribute the blessing upstream: ADR-0183:72 says the shape is
*"**blessed by ADR-0148 amendment 1 §4** — the kiosk claimant, with roles and no ID"*, and
`humantask/validate.go:25` restates it as *"ADR-0148 amendment 1 §4's kiosk claimant, anonymous
but carrying roles."*

**Why that assumption fails — two independent failures.**

**(i) The upstream citation is false.** ADR-0148's amendment 1 is
`## Implementation amendments (2026-07-27, rule-#9 re-audit)` (`docs/adr/0148-*.md:59`), a
six-item list. Its **§4** (`:72-79`) is titled *"The interim state fabricates a claim"* and is
about `scanTask` rebuilding `Claim{Actor: {ID: claimed_by}}` with a **zero `At`** — *"That is
fabricated audit data, not missing data … Closing this is the whole point of this ADR."* It is a
**defect being fixed**, not an endorsement of any actor shape. Executed:
`grep -n -i "anonymous|empty id|Actor.ID|empty claim|kiosk" docs/adr/0148-*.md` returns **no**
occurrence of "kiosk" and none of "anonymous"; the only other reference to §4 in ADR-0148
(`:128-129`) uses it correctly and unrelatedly (*"keying on `claimed_by != ''` would resurrect
the fabricated-claim bug that amendment 1 §4 recorded"*). The word "kiosk" appears in exactly
three places repo-wide — ADR-0183, `humantask/validate.go:25`, `humantask/validate_test.go:44-47`
— and **nowhere in ADR-0148**.
⇒ ADR-0189 inherited a citation, restated it as plain fact, and made it the sole authority for
the revision's most consequential change. This is verbatim the failure CLAUDE.md's Premise
Discipline names: *"Re-verify claims you inherit before restating them. Restating strips the
hedge."* Round 1 was faulted for *"citing neither ADR"*; the revision cites both and re-derived
neither.

**(ii) The blessed shape is narrower than what (a) admits.** Every place that describes the
shape describes it as **roles-bearing**: ADR-0183:76 *"a kiosk claim is **anonymous but carries
roles**"*; `validate.go:25` *"anonymous but carrying roles"*; and the only fixture that pins it,
`humantask/validate_test.go:47`, is `authz.Actor{Roles: []string{"kiosk"}}` — empty ID,
**non-empty Roles**. Nothing in the repo blesses `authz.Actor{}`, the all-zero actor. Yet (a)
admits it, and the grid's own ⚠3 identifies it as the hazard (*"a consumer's buggy middleware
that authenticates and then stores a **zero actor** now satisfies C's refusal on every route
group"*) and then resolves it **by wording** rather than by narrowing the rule.

The bundle presents a binary — refuse an empty ID, or refuse nothing — and never considers the
rule that fits the evidence: **refuse an actor that is empty in every dimension**
(`ID == "" && len(Roles) == 0 && len(Attributes) == 0`). That rule preserves the kiosk shape
exactly as pinned, kills the buggy-middleware zero actor, and kills F6's deny-list shape.
⚠ Note the store-side invariant does not stand in its way: `humantask.Validate`
(`humantask/validate.go:49-55`) never inspects `Claim.Actor` at all — the empty claimant is
accepted by **omission**, not by a positive rule the transport would be contradicting.

**Concrete fix:**
1. Re-derive the premise and write the executed result into spec §2: the authority is
   **ADR-0183's own Decision plus `validate_test.go:45-47`**, not ADR-0148 §4. Fix ADR-0183:72
   and `humantask/validate.go:25` in this bundle (both are false citations on `main` today) —
   the bundle already sets the precedent of amending a cited ADR in place for ADR-0147 #5.
2. Put the **all-dimensions-empty refusal** on the owner's desk as the third option. If the
   owner still prefers refusing nothing, record *that* as the reason, not the false citation.
3. If option 2 is adopted, grid ⚠3 and ⚠4 stop being "resolved by wording" and become resolved
   by design, and F6 closes with them.

---

### F9 — `HealthRoutes`' exemption is structurally guaranteed; every OTHER route's gate is not. The failure direction is inverted, and 11 of 15 admin handlers are untestable without an optional dep — [CRITICAL]
**Pair:** (b) × `HealthRoutes`, and (b) × (c)

**What (b) assumes:** ADR Decision 6 speaks at **group** granularity — *"`InstanceRoutes`,
`MessageRoutes`, `TaskRoutes` and `AdminRoutes` all return 401 … `HealthRoutes` is exempt and
calls no resolver."* Plan Tasks 9–11 implement it *"in the route handler"*, and spec §5 row 12
tests it with one request per group.

**Why that assumption fails:** the refusal is **per handler**, not per group. Counted in this
worktree, each adapter registers **26 routes** through one shared wrapper
(`stdlib.handle`→`observe`, `gin.observe`, `fiber.observed` — 26 registrations in each of the
three `groups.go`), split as **15 admin + 5 instance + 1 message + 3 task + 2 health**. So (b)+(c)
require a gate at `24 × 3 = 72` handler sites, and the exemption is achieved by *omission*.

That makes the failure direction wrong in exactly the way this repo has already been burned:
forgetting the gate on one handler is **silent and fail-open**, while the thing that is
structurally guaranteed is the one case where openness is intended. `HealthRoutes` is safe
because nobody will add a resolver call to it by accident; `POST /admin/role-bindings` is unsafe
because somebody must remember to add one, 72 times, across three packages implemented by
**three concurrent subagents** (plan Tasks 9/10/11 fan out).

⚠ **And the prescribed tests cannot detect the miss.** 11 of the 15 admin handlers are
*conditionally registered* — `if c.DeadLetters != nil` (2 routes), `if c.Policies != nil` (6,
including both `/admin/role-bindings` verbs), `RelayStats` (1), `Timers` (1), `Lineage` (1). A
test that mounts `AdminRoutes{Svc: svc}` with nil deps exercises **4 of 15**, and spec §5 row 12
is a single request. A missing gate on the six policy/role-binding routes — the highest-value
admin surface in the repo, the one that **grants roles** — would leave every prescribed test
GREEN. This is the ADR-0188 lesson repeating: *guards that check the wrong granularity stay green
while a site is omitted, fail-open.*

**Evidence:** `awk '/Customize\(/{n=$0} /handle\(r, inst, cfg/{print n}' transport/http/stdlib/groups.go
| sort | uniq -c` → AdminRoutes 15, InstanceRoutes 5, MessageRoutes 1, TaskRoutes 3, HealthRoutes 2.
`grep -c "observe(inst" transport/http/gin/groups.go` → 26; `grep -c "observed(" transport/http/fiber/groups.go` → 26.
Conditional blocks read directly in `transport/http/stdlib/groups.go` (DeadLetters, Policies,
RelayStats, Timers, Lineage).

**Concrete fix:** move the refusal into the **shared per-adapter wrapper** that all 26 routes
already pass through, and make the exemption the thing that must be *declared* —
`handle(..., httpcore.RouteAnonymous)` or an `observeAnonymous` variant used only by the two
health routes. Three insertions instead of 72, the miss becomes a compile-visible omission at a
single site, and `HealthRoutes`' exemption becomes explicit rather than accidental. Then add the
test that the current plan cannot express: **enumerate every registered route** (both adapters
already know their static route templates for observability labelling) and assert that each one
except `/healthz` and `/readyz` answers 401 unauthenticated — mounting `AdminRoutes` with **all
five optional deps non-nil** so the 11 conditional handlers exist. Without that enumeration test,
"every route group refuses" is an unverified quantifier over 72 sites.

---

### F10 — `WithAdminRoles` is unreachable through both mount conveniences, and passing it to `Mount` is a silent no-op — [MAJOR]
**Pair:** (c) × `MountGroups` / `Mount`  — the grid's **self-declared unresolved item 2**

**What (c) assumes someone hands it:** that a consumer who wants the admin gate can declare it.
The grid resolves the `MountGroups` question **entirely in terms of the resolver** — *"`MountGroups`
passes no options, so each group runs `ResolveConfig()` with none — which installs the default
`RequestActor` … a fully working task API through `MountGroups` with zero options"* — and
downgrades the item to MINOR on that basis. The ADR's Consequences bullet repeats only the
resolver half.

**Why that assumption fails:** (c) was added *after* the sentence the grid is reusing, and it is
an **option**, not a default. Read in this worktree:
- `httpcore.MountGroups` (`seam.go:208-212`) calls `g.Customize(r)` with no opts ⇒
  `cfg.AdminRoles` is always empty ⇒ **`AdminRoutes` mounted via `MountGroups` can never have the
  role gate**, whatever the consumer wants.
- `stdlib.Mount` / `gin.Mount` / `fiber.Mount` **exclude `AdminRoutes`** and forward `opts...` to
  Instance/Task/Message. So `stdlib.Mount(mux, svc, stdlib.WithAdminRoles("platform-admin"))`
  **compiles, runs, and does nothing at all** — a security option accepted and silently discarded.
  Nothing in the type system or the godoc warns.
⇒ the only path that can reach the gate is `AdminRoutes{…}.Customize(r, WithAdminRoles(…))`
directly. The grid's "resolved / downgraded to MINOR" verdict was reached against the pre-(c)
bundle and was not re-derived after (c) landed — which is precisely the interaction failure this
lens exists to catch.

**Concrete fix:** state the reachability explicitly in `WithAdminRoles`' godoc (*"has no effect
unless passed to `AdminRoutes.Customize`; `Mount` does not mount admin routes and `MountGroups`
passes no options"*), and extend the `MountGroups` godoc fix the grid already prescribes to cover
the gate as well as the resolver. Consider making the silent no-op loud — e.g. have `Mount` log
at WARN when `cfg.AdminRoles` is non-empty, since by construction it can only be a wiring mistake.

---

### F11 — The plan's Goal makes the exact claim the plan's own Global Constraint forbids, 26 lines later — [MAJOR]
**Pair:** (a) × the plan's headline

**What the Goal assumes (a) hands it:** `docs/plans/2026-08-25-request-actor-identity.md:12-14` —
*"**Goal:** the HTTP transport … **refuses rather than downgrades when no identity is
established** — across every route group except health probes."*

**Why that assumption fails:** after (a), when **no identity is established** the transport does
not refuse. `authz.Actor{}` — no ID, no roles, no attributes — is exactly "no identity
established", and it passes every gate by design (ADR Decision 3, spec §3.3). The true sentence
is the one §3.3 uses: *the request carries a **resolved** actor*.

The plan itself legislates against this at `:40`: *"⛔ **Do not write, anywhere, that every route
now has an 'identified principal'.** §3.3 removed the empty-ID rule, so a resolved-but-empty
actor passes. The true sentence is *'carries a **resolved** actor'*. **This is the bundle's most
likely recap-overreach.**"* — and again at `:452`. The prohibition is correct, is stated twice,
and is violated **26 lines above its first statement**, in the sentence a dispatched subagent
reads first and paraphrases into every task brief.

**Evidence:** executed
`grep -n -i "identified principal|identity is established|no identity" docs/plans/2026-08-25-*.md`
→ `:14` (the violation), `:40` and `:452` (the prohibition). The spec's equivalent sentence
(`:373`) is correct; only the plan's Goal is wrong.

**Concrete fix:** rewrite the Goal to *"…refuses rather than downgrades when the consumer's
identity seam resolves **nothing** — across every route group except health probes"*, and add
"the Goal sentence" to Task 17's premise sweep, which currently sweeps only the **diff's**
comments and would not have caught this.

---

### F12 — (b) adopts adapter-side resolution at 21 sites per adapter, which is the exact ground on which Decision 2 rejected adapter-side resolution — and the alternative that dissolves three residuals was never enumerated — [CRITICAL]
**Pair:** (b) × Decision 2 / (g)

**What Decision 2 assumes:** spec §1.1 records the endpoint-parameter shape as chosen *"over
resolving in each adapter (**which duplicates a security decision at nine sites**)"*. That
rationale is the whole reason the three task endpoints take a `RequestActorFunc` parameter — and
therefore the whole reason for (g)'s asymmetry and for F5's orphaned timeout.

**Why that assumption fails:** (b) **is** adapter-side resolution. It puts a resolve-and-refuse
call in the route handler of `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` — counted in
this worktree, **21 handlers per adapter, 63 across the three** (15 admin + 5 instance + 1
message). The bundle rejected duplicating a security decision at 9 sites and then, in the same
revision, duplicated it at 63. The premise that produced Decision 2's shape did not survive (b),
and nothing in the bundle re-derives it.

⚠ **And the option that resolves all of this was never on the table.** The ADR rejects exactly
one alternative — *"adding a pre-decode check to the task routes **as well** … would invoke the
consumer's … resolver **twice per request**"*. That is a straw option. The real alternative is:
**resolve once in the handler, pre-decode, and pass the resulting `authz.Actor` (a value) to the
endpoint** — `ClaimTask(ctx, svc, token, in, mapper, actor authz.Actor)`. It:
- resolves **once**, so the rejection ground does not apply;
- gives the task endpoints the actor **as a value**, which spec §3.6 says is their only
  requirement;
- puts the resolve call where `cfg` is in scope, which is the only place
  `RequestActorTimeout` can be read — **dissolving F5 outright**;
- makes the placement **uniform**, dissolving (g)'s asymmetry and residual §4.5, and with it the
  ADR's own worry that *"the next reader collapses it into one shape and silently re-widens the
  ordering window"*;
- moves the task-route 401 **in front of** the capped body read, which retires the ordering
  residual in Decision 8 (*"authentication resolves BEHIND the adapter's capped body read"*)
  instead of documenting it;
- and still keeps the endpoints non-generic — `authz.Actor` carries no router type.

⚠ Note the ADR's asymmetry warning is also **direction-blind**: unifying *toward pre-decode* is
the safe direction and is the option above; only unifying *toward post-decode* re-widens the
window. As written the warning tells the next reader not to do either.

**Evidence:** spec §1.1 (rejection ground, "nine sites"); ADR Decision 6 / spec §3.6 (the
rejected alternative, verbatim); handler counts as in F9; ADR Decision 2 + spec §3.2 for the
signature that has no `cfg` and no timeout.

**Concrete fix:** put the value-passing alternative to the owner as a first-class option before
implementation. It is strictly better on five stated residuals and its only cost is that the
endpoint parameter becomes `authz.Actor` rather than `RequestActorFunc` — the same "one added
argument, no branch" the bundle already claims. If it is rejected, record a reason that survives
(b), because "duplicates a security decision at nine sites" no longer does.

---

### F13 — (b) makes every unauthenticated request invoke the consumer's identity backend on 24 of 26 routes; (f) bounds each call but nothing bounds the aggregate — [MAJOR]
**Pair:** (b) × (f)

**What (f) assumes (b) hands it:** the timeout is justified solely against a **hang** —
*"'503, never an open door' has an unnamed third state there: hang"* — a per-request liveness
concern, argued from fiber's `c.Context() == context.Background()`.

**Why that assumption fails:** (b) changes *who* can reach the resolver, not just how long it
may take. Before (b), an anonymous request to `POST /messages`, `GET /instances/{id}` or any
admin route touched no identity system at all. After (b), the resolver is invoked **before the
body is even decoded** on those groups — so the cheapest possible request (empty POST, no
credential) triggers a call into whatever the consumer wired: an OIDC introspection endpoint, an
LDAP directory, a session store. Across the three adapters that is 24 of 26 routes reachable
without any credential.

(f) bounds a single call at 10 s. It bounds nothing in aggregate: N concurrent anonymous requests
hold N goroutines and N in-flight backend lookups for up to 10 s each. The ADR notes there is no
`Retry-After` on the 503 and no rate limiting anywhere. The design has converted "unauthenticated
traffic hits our route table" into "unauthenticated traffic hits the consumer's identity
provider" — an amplification vector against a **third-party** system, which is materially
different from the hazards the bundle enumerates.

**Evidence:** ADR Decision 6 (pre-decode placement for the three non-task groups); Decision 9 and
spec §3.3 for the timeout's stated justification (hang only); ADR Consequences, final bullet:
*"No `WWW-Authenticate` on the 401, no `Retry-After` on the 503."* Route counts as in F9.
⚠ The default resolver (`authz.ActorFromContext`) is free and cannot amplify — the exposure
exists only for a consumer-supplied `WithRequestActor`, i.e. every non-trivial deployment.

**Concrete fix:** state it in the ADR beside the timeout, not as a residual: *the resolver is now
on the unauthenticated path of every non-health route; it must be cheap and should be cached by
the consumer.* Say so in `WithRequestActor`'s godoc and in `SECURITY.md`'s new scope note — a
consumer who reads "supply a function that resolves the actor" will reasonably write a network
call. Consider recommending, in the example, a resolver that reads an already-verified value the
middleware put in the context (which is the design's own default) rather than one that performs
I/O.

---

### F14 — The grid's row **G** carries a blast radius the ADR explicitly refutes — [MINOR]
**Pair:** grid × ADR (stale input)

**What it assumes:** the grid's changed-decision table row **G** reads *"blast radius corrected
to **37 lines / 6 packages**; counting method changed to member-set"*, and the grid declares G
non-interacting and already checked.

**Why that assumption fails:** the ADR states *"⚠ **'37' — the figure the audit itself proposed —
is also wrong**, being the union of the two *pin* nets only; it omits the 9 production call sites
and the 2 `service` comments"*, and spec §2.6 lands on **48 lines · 13 files · 6 packages**. The
grid is committed in the bundle and the spec §6 brief tells the re-audit to treat it as an input;
an implementer reading row G gets a number the bundle refutes 20 lines away in another file.
(Per F3, both numbers are in any case short of (b)'s footprint.)

**Concrete fix:** update row G to the corrected member set, or mark the grid with a dated banner
saying it was written pre-revision and its numeric rows are superseded by spec §2.6.

---

### F15 — "Admin operations have no audit record at all today" is a quantifier that overshoots the claim it needs — [MINOR]
**Pair:** (a) × (c)  — the grid's ⚠4, and ADR Consequences

**What it assumes:** the grid's ⚠4 and the ADR's Consequences both dismiss the empty-ID-clears-
the-admin-gate residual with *"admin operations have **no audit record at all** today
(`admin_endpoints.go` has zero `authz.` references), so nothing is lost that existed."*

**Why that assumption fails:** the parenthetical proves something narrower than the sentence.
Executed: `grep -c "authz\." transport/http/httpcore/admin_endpoints.go` → **0**, confirming no
*actor* is recorded. But admin verbs that mutate an instance do leave engine-side records —
`CancelInstance` (`admin_endpoints.go:116-122`) and `ResolveIncident` (`:102-112`) both go
through `svc` and return a changed `InstanceState`, so the *operation* is recorded even though
the *operator* is not. "No audit record at all" is the kind of summary sentence CLAUDE.md's
Premise Discipline flags: the reasoning below it is right, the compression above it is not.

**Concrete fix:** say what is actually true and is sufficient for the argument — *"no admin
operation records **the acting principal** today, so no existing attribution is lost"*. Same
dismissal, survivable sentence.

---

### F16 — CONFIRMATION: (d) × ADR-0187 holds. The at-rest classification genuinely needs no amendment — [CONFIRMATION]
**Pair:** (d) × (h)

The bundle refutes round 1's predicted ADR-0187 drift by execution, and the refutation stands.
Verified independently here:
- `internal/atrest/classification.go:199-203` classifies `wrkflw_human_task.{claimed_by,
  claim_actor, completed_by, completion_actor, candidates}` **all** as `ClassActor`.
  `claim_actor` — the JSONB column that receives the actor remainder, i.e. roles **and
  attributes** — is already `ClassActor`, so (d) adds payload to a column whose class already
  covers it. `candidates` corroborates via a second, independent path (`ActorResolver`-populated).
- `SECURITY.md:173-179` renders the same rows; `:125` defines `actor` as *"identifies a human
  principal. Treat as personal data."* — unchanged by (d).
- `wrkflw_instances.snapshot` is `freeform`, defined at `SECURITY.md:124` as *"may hold arbitrary
  consumer data, including PII the consumer put there"* — also unchanged by (d).
⇒ no new column, no class change, no generator input change. **Accept the bundle's claim.**

⚠ One caveat worth a line in the ADR: ADR-0187's guard is a **generator-vs-artifact** drift check,
so it would not have detected a prose claim of the form "over HTTP these columns carry no
attributes" had one existed. None does (`SECURITY.md` makes no HTTP-scoped narrowing of the actor
class) — but that is a fact worth recording as *checked*, since it is exactly the blind spot
ADR-0187's own delivery discovered.

---

### F17 — CONFIRMATION: (h) × ADR-0117 is correctly and narrowly stated — [CONFIRMATION]
**Pair:** (h) × ADR-0117

`docs/adr/0117-optional-usertask-eligibility.md:15-17` reads: *"the runtime already treated an
**empty** eligibility spec as an open engine gate, **deferring authorization to the consumer's
transport layer** (e.g. HTTP security middleware)."* ADR-0189's Neutral bullet — *"0117 defers
**authorization** to the transport, and this record supplies **authentication** … 0117's deferral
remains unsatisfied until backlog 52/53 land — round 1's *'0117 becomes true rather than changed'*
equivocated the two and is withdrawn"* — is accurate against that text and correctly hedged.
**No amendment needed. Accept.**

⚠ Adjacent tension worth one sentence in the ADR, not a finding: ADR-0117 defers authorization to
*the consumer's* transport layer, and (c) implements an authorization decision inside *the
library's* transport layer. That is the same tension Decision 7 already records against CLAUDE.md's
pluggable-`Authorizer` requirement; ADR-0117 is a second, independent witness to it.

---

### F18 — The marshalability pre-check's SCOPE is unspecified: "the seam" rejects, but (b) created a second seam that may or may not run it — and both answers are bad in a different way — [MAJOR]
**Pair:** (d) × (b)  — the author's grid cell **A×C**, resolved only for the exposure leg

**What (d) assumes (b) hands it:** ADR Decision 5 and spec §3.5 say *"**the seam** rejects an
actor whose `Attributes` do not `json.Marshal`"* — singular, as if there were one. When (d) was
written there was: `resolveRequestActor`, on the three task routes. (b) then created a **second**
resolution site, `httpcore.RequireActor`, on 21 handlers per adapter.

**Why that assumption fails:** nothing in the bundle says whether `RequireActor` runs the
pre-check. Plan Task 4 builds `resolveRequestActor(ctx, resolve, timeout)` **with** it; plan Task
8 introduces `RequireActor(ctx, cfg) error` as *"the refusal-only helper"* and lists its test
cases as *"401 paths as Task 4"* — the 401 paths only. Both readings are live, and they differ
materially:
- **If `RequireActor` runs the pre-check:** a consumer whose resolver attaches one
  non-marshalable attribute takes down **the entire read API** — `GET /instances/{id}` returns
  400 — for a value that route never persists and never reads. The pre-check's own justification
  (*"a `chan int` attribute permanently bricks the instance view"*) does not apply to a route that
  writes nothing.
- **If it does not:** the same resolver yields **400 on `/tasks/{t}/claim` and 200 on
  `/instances`**, so the fault surfaces on one route family and not the other, with no stated
  rule a consumer could predict.

**Evidence:** ADR Decision 5 / spec §3.5 ("the seam", singular, written pre-(b)); plan Task 4
step 3 (pre-check inside `resolveRequestActor`); plan Task 8 step 1 (`RequireActor` test list
names only the 401 cases). The grid's cell A×C (⚠1) considers only the *exposure* leg of A×C and
never the pre-check leg.

**Concrete fix:** decide and write it down. The defensible rule is **write-gated**: the pre-check
guards a durable write, so it belongs only where an actor is persisted — the three task routes —
and `RequireActor` should refuse-only. Say that in Decision 5 (replacing "the seam" with the
route family), and add the negative test: a non-marshalable actor ⇒ **200** on
`GET /instances/{id}`, **400** on claim. Combined with F7's reclassification, that test also pins
the fault attribution.

---

### F19 — (a) + (e) together make the emptiest possible HTTP request mint a durable, contentless claim — a shape round 1 made unreachable and no ADR blesses — [MAJOR]
**Pair:** (a) × (e)  — the author's grid cell **B×F, marked `·`**

**What (e) assumes (a) hands it:** (e) makes `POST /tasks/{token}/claim` legal with **no body at
all**, on the reasoning that `ClaimInput` is now zero-field so a correctly-migrated client sends
nothing. It assumes the identity half of the request carries the content the body no longer does.

**Why that assumption fails:** (a) removed the rule that guaranteed it. Compose the two: a
request with **no body** and a context holding `authz.Actor{}` passes decode (by (e)), passes the
401 gate (by (a)), and reaches `svc.ClaimTask` with a zero actor. Per the ADR's own Context —
*"ADR-0147 renders `claim.actor` … by faithful passthrough into `wrkflw_human_task`"* — that
writes a durable audit row asserting the task was claimed, at time T, by
`{"id":"","roles":null,"attributes":null}`. Nothing anywhere refuses it: `humantask.Validate`
(`humantask/validate.go:49-55`) never inspects `Claim.Actor`.

Under round 1's empty-ID refusal this request was a 401. Under (a) alone it is a claim by an
unidentified-but-role-bearing kiosk operator, which ADR-0183 does bless. Under (a)+(e) it is a
claim carrying **no information whatsoever** — not the kiosk shape (`authz.Actor{Roles:
["kiosk"]}`, the only fixture the repo pins, `humantask/validate_test.go:47`), and not a shape any
ADR has ever considered. The two changes were assessed separately and each is defensible alone;
the composition is what produces the contentless record, which is exactly the class of hole this
lens exists to find.

**Evidence:** spec §2.7 (executed: bodyless claim is `400 EOF` today, so (e) is what makes it
reachable); ADR Decision 3 (the empty actor passes); ADR Context (ADR-0147 faithful passthrough);
`humantask/validate.go:49-55` (no actor inspection, read in this worktree);
`humantask/validate_test.go:44-47` (the pinned shape carries roles).

**Concrete fix:** F8's narrower rule closes this at the right layer — refuse an actor empty in
**every** dimension, which admits the pinned kiosk shape and refuses the contentless one. If the
owner declines it, then (e) must not ship without a stated consequence: *"a bodyless claim from a
zero actor writes a contentless durable audit record"*, plus a test pinning that this is
intended. Do not leave the composition undocumented in both decisions.

---

## Verdict

**19 findings: 7 CRITICAL, 8 MAJOR, 2 MINOR, 2 CONFIRMATION.**
Baseline sanity: `go test -count=1 ./transport/... ./authz/...` at `37d77a34` → **EXIT=0**, all
six packages `ok`; both throwaway probes removed, `git status --porcelain` clean.

⛔ **The bundle is NOT implementation-ready.** Five of the seven Criticals (F1, F5, F8, F9, F12)
are holes the revision's own fixes opened in each other — the ADR-0185 pattern repeating, at the
same ratio.

### The full pairwise grid, as re-derived

`·` = derived, no interaction found. **Bold** = a finding this lens raises. Author's cell marks
in parentheses where they differ.

|            | **(a)** removal | **(b)** 401 groups | **(c)** admin gate | **(d)** attrs+pre-check | **(e)** absent body | **(f)** timeout | **(g)** placements |
|---|---|---|---|---|---|---|---|
| **(a)** removal        | — | **F8** (⚠3: resolved by wording where a design fix existed) | **F15** (⚠4: dismissal overshoots) | **F6** (author: `·`) | **F19** (author: `·`) | · | · (author: `·` — agreed) |
| **(b)** 401 groups     |   | — | **F1, F9** (⚠5: its key sentence is false) | **F18** (⚠1: only the exposure leg was derived) | · | **F13** | **F12** (⚠6: its rationale is invalidated) |
| **(c)** admin gate     |   |   | — | **F18** (author: `·`) | · (author: `·` — agreed) | · | · (author: `·` — agreed) |
| **(d)** attrs+pre-check|   |   |   | — | **F7** (author: `·`) | · | ⚠2 wording — **accepted** |
| **(e)** absent body    |   |   |   |   | — | · | ⚠8 residual — **accepted**, but see F7 |
| **(f)** timeout        |   |   |   |   |   | — | **F5** |
| **(g)** placements     |   |   |   |   |   |   | — |

Cross-cutting rows the author's grid has no axis for:

| pair | finding |
|---|---|
| **(f)** × the grid itself | **F4** — the timeout is a changed decision with **no axis at all**; `grep -in "timeout"` on the grid returns nothing |
| **(b)** × **G** (blast radius) | **F3** — (b) is a third breaking change the compile ablation structurally cannot see; 226 route literals / 82 `AdminRoutes{` mounts absent from the 48-line member set |
| **(h)** × ADR-0095 | **F2** — the one ADR this bundle contradicts is the one ADR it never opened |
| **(h)** × ADR-0148 / ADR-0183 | **F8** — the citation authorising (a) is false at the ADR-0148 link |
| **(h)** × ADR-0187 | **F16** — CONFIRMED correct |
| **(h)** × ADR-0117 | **F17** — CONFIRMED correct |
| **(c)** × `MountGroups` / `Mount` | **F10** — the grid's unresolved item 2 was resolved pre-(c) and is stale |
| the plan's Goal | **F11** — violates the plan's own ⛔ constraint, 26 lines above it |
| the grid's row **G** | **F14** — carries `37`, which the ADR refutes |

### Verdict on the author's own grid

The grid is a genuine piece of work — it found ⚠3 and ⚠5, the two sharpest live interactions,
and its instinct to write down the unresolved items is what made two of them attackable. But as a
completeness claim it fails, in three distinct ways.

**1. An entire changed decision is missing from the axes.** (f), the resolver timeout, appears
nowhere — not as an axis, not among the declared-inert G–K, not in the unresolved list. It has a
Critical interaction (F5). The spec's own §6 brief lists it as change (f); the grid predates that
list and was never reconciled with it.

**2. Four of its seven `·` ("derived, no interaction") cells are wrong.** This is where an
author's blind spot lives, exactly as briefed:

| cell | author | verdict |
|---|---|---|
| **A×B** = (d)×(a) | `·` | ❌ **WRONG** — F6. (a) legalises `Actor{}`, whose deny-list evaluation is ALLOW; executed. |
| **A×D** = (d)×(c) | `·` | ❌ **WRONG via F18** — whether the pre-check runs on admin routes is unspecified and both answers are harmful. |
| **A×F** = (d)×(e) | `·` | ❌ **WRONG** — F7's second leg: a bodyless claim can still return `400 bad_request`, a status asserting something about a body that does not exist. |
| **B×F** = (a)×(e) | `·` | ❌ **WRONG** — F19, the contentless durable claim. |
| **B×E** = (a)×(g) | `·` | ✔ agreed |
| **D×E** = (c)×(g) | `·` | ✔ agreed |
| **D×F** = (c)×(e) | `·` | ✔ agreed |

Note the pattern: **every wrong cell involves (a), the REMOVAL.** The grid's own header says
*"Every fix below was correct alone"*, and (a) is the only entry that is not a fix but a deletion.
CLAUDE.md's brief for this lens says a removal generates its own grid; the author's grid treated
(a) as just another row and marked three of its five off-diagonal cells `·`.

**3. Its eight ⚠ cells, adjudicated:**

| cell | author's resolution | verdict |
|---|---|---|
| ⚠1 A×C | "C closes A's exposure leg", correctly qualified to *"any authenticated caller"* | ✔ **correct but incomplete** — it derives only the exposure leg of A×C; the pre-check leg is F18 |
| ⚠2 A×E | "the pre-check is not an input gate" — wording | ✔ **accept** |
| ⚠3 B×C | "resolved by wording, deliberately" | ⚠ **the identification is excellent; the resolution is the weakest available.** A design fix exists (F8's all-dimensions-empty rule) and was never enumerated, and the authority for choosing wording is a false citation (F8) |
| ⚠4 B×D | accepted as residual, "nothing is lost that existed" | ⚠ **accept the residual, fix the sentence** — F15 |
| ⚠5 C×D | "⇒ D is opt-in. **No existing correct wiring breaks**" | ❌ **FALSE, and it is the grid's central error.** Refuted by `examples/production_wiring/main.go:273-275` in this repo (F1); the ADR that decided the posture, ADR-0095, was never opened (F2) |
| ⚠6 C×E | "No — because C is new code … E's residual stays exactly three routes" | ❌ **the rationale is invalidated by C itself** — F12. C adopts adapter-side resolution at 63 sites, the exact ground on which Decision 2 rejected it at 9 |
| ⚠7 C×F | "F is scoped to the claim route alone" | ✔ **accept** |
| ⚠8 E×F | "400 before 401 on malformed JSON — accepted, not a regression" | ✔ **accept as filed**, but F7 adds a leg it does not cover |

**Its two self-declared unresolved items:**
- **1 — the admin role gate as a second authorization mechanism.** The author was right to leave
  it open, and it is **worse than filed**: this is not merely an absent precedent (*"the library
  has no other example"*) but a **reversal of a decided one**. ADR-0095 removed exactly this shape
  (a single option applying equally to every admin endpoint) and recorded default-absent as
  *"safer than a built-in default-deny gate"*. F1/F2.
- **2 — `MountGroups`.** Marked ~~struck through~~ as RESOLVED and downgraded to MINOR. **Stale:**
  the resolution reasons only about the default resolver and was reached before (c) existed. (c)
  is an option, and `MountGroups` passes none. F10.

### What must happen before this bundle is dispatched

1. **F1/F2** — re-derive the opt-in decision against ADR-0095, add ADR-0095 to `Amends:`, fix
   `SECURITY.md:37-39`, and put `examples/production_wiring` in the migration story.
2. **F8** — re-derive (a)'s premise; put the all-dimensions-empty rule to the owner. It closes F6
   and F19 with it.
3. **F12** — put the "resolve pre-decode, pass `authz.Actor` by value" option to the owner. It
   closes F5 and retires two named residuals.
4. **F9** — move the refusal into the shared per-adapter wrapper and add the route-enumeration
   test with all five optional admin deps wired.
5. **F3** — re-run the ablation with (b) modelled and republish the member set; add the
   instance/message/admin test-migration tasks the plan is missing.
6. **F7, F18** — settle the pre-check's status code and its scope.
7. **F4, F10, F11, F13, F14, F15** — document/wording fixes, each with its own sentence.
