# ADR-0189 rule-#9 audit — INTERACTION lens

Worktree `wt-interaction`, detached at `7fa756d0` (the bundle commit; documents only,
implementation not started). Bundle presence verified: spec, ADR and plan all present.

Lens question: *what does this decision assume someone else will hand it, and who agreed
to that?* Changed decisions taken pairwise, plus decision-vs-its-own-mechanism.

Grid labels used throughout:
- **(a)** ADDED the `authz` context seam `ContextWithActor` / `ActorFromContext`.
- **(b)** ADDED `httpcore.RequestActorFunc` as a PARAMETER on the three task endpoints
  (three exported signatures, nine adapter call sites).
- **(c)** ADDED two sentinels `ErrUnauthenticated` (401) and `ErrIdentityUnavailable` (503)
  as the FIRST two arms of `ClassifyError`'s ordered switch.
- **(d)** CHANGED `authz.Actor.Attributes` now flows to the authorizer (today dropped at all
  three endpoint sites).
- **(e)** REMOVED three public DTO fields (`ClaimInput.Actor`, `CompleteInput.Actor`,
  `ReassignInput.By`) and the exported `httpcore.Actor` type.
- **(f)** REMOVED from the inherited ADR-0185 D1 design: `httpcore.WithAnonymousActorAllowed()`.
- **(g)** DEFERRED-BUT-TOUCHED: backlog 52 (`authz.AllowAll` is the default authorizer),
  53 (an empty `AuthzSpec` means allow-all), 103 (deny-list attribute predicates allow when
  the variable is missing), 90 and 124 all stay open while this ships.

---

### F1 — §4.2's backlog-103 adjudication is INVERTED: flowing `Attributes` CLOSES a live fail-open on the HTTP path, it does not open one — [CRITICAL]

**Pair:** (d) `Attributes` now flow × (g) backlog 103 deferred. Also (d) × §1.1's own fix.

**What (d) assumes (g) hands it:** the spec §4.2 and the ADR's Negative bullet both assert —
as the *sole* recorded cost of the one non-refusal behaviour change in this record — that
*"Today all three endpoints drop `Actor.Attributes`, so `actor.Attributes.*` predicates fail
closed **vacuously**. Once the actor arrives whole they go live, with nothing bounding them
until 103 ships."* The whole adjudication ("this is a cost of shipping this record, not a
follow-up") rests on `Attributes`-dropped ⇒ fail-CLOSED.

**Why that assumption fails:** it is false, and false in the direction that matters.
`RoleAuthorizer.Authorize` (`authz/authz.go:130-143`) evaluates the predicate over
`{"actor": actor, "vars": vars}` where `actor` is the **struct**. `actor.Attributes` on a nil
map therefore resolves to nil rather than erroring, so a **deny-list** actor-attribute
predicate compares nil against the banned value and **ALLOWS**. That is backlog 103's exact
defect and it is **live on the HTTP path today**. Flowing the real attributes (d) makes the
same predicate **DENY**. For this class (d) is a *fix*, not a cost.

The only class that moves permissively is the **allow-list** form (`== "gold"`), which today
denies everyone and afterwards admits the actor the consumer's own authenticated middleware
vouched for — the intended behaviour of the record, not an unbounded hazard.

This is the interaction-lens signature exactly: §1.1 **correctly deleted** ADR-0185's
*"`Attributes` reaches the authorizer — closing finding 4's second leg for free"*, citing the
audit refutation that *"`actor` is a struct, so `Attributes` always exists at depth-1"*. That
refutation is the very mechanism measured below. The revision fixed the sentence in §1.1 and
then wrote the **opposite-signed** consequence in §4.2 without re-deriving it against the
same mechanism. One fix, one new hole, same document.

**Evidence:** executed at `7fa756d0`, `authz/zzprobe_test.go` (throwaway, deleted),
`go test -count=1 -v -run '^TestZZProbeActorAttributePredicates$' ./authz/...` → `EXIT=0`,
`--- PASS`:

```
pred=actor.Attributes.tier != "banned"    actor=TODAY-http (Attributes DROPPED)     => ALLOW      <-- fail-OPEN today
pred=actor.Attributes.tier != "banned"    actor=AFTER-0189 attrs flow, tier=banned  => DENY       <-- (d) CLOSES it
pred=actor.Attributes.tier == "gold"      actor=TODAY-http (Attributes DROPPED)     => DENY
pred=actor.Attributes.tier == "gold"      actor=AFTER-0189 attrs flow, tier=gold    => ALLOW      <-- intended
pred=actor.Attributes.suspended != true   actor=TODAY-http (Attributes DROPPED)     => ALLOW
pred=actor.Attributes.suspended != true   actor=AFTER-0189 attrs flow, tier=gold    => ALLOW      <-- 103 unchanged for ABSENT attrs
pred=vars.status != "blocked"             (vars = map[string]any{})                 => ALLOW      <-- 103 over vars, untouched either way
```

Three classes, and the record collapses them into one. Deny-list over a **present** attribute:
today ALLOW → after DENY (fixed). Allow-list: today DENY → after ALLOW (intended). Deny-list
over an **absent** attribute: ALLOW → ALLOW (103, genuinely untouched by this record).

**Why CRITICAL and not a documentation nit:** the ADR is the durable record a later session
re-cuts from. As written, the single stated cost of (d) is a fabricated one, and the real
finding — *`actor.Attributes.*` deny-lists are fail-open over HTTP right now* — is recorded
nowhere in the bundle, so it will not be filed as a backlog item. The realistic failure mode
is a future revision cutting (d) "to reduce risk while 103 is open", which would **preserve**
the live fail-open the measurement above shows.

**Concrete fix:** replace §4.2 and the ADR Negative bullet with the three-class split above,
each with its measured verdict. State plainly that (d) **narrows** backlog 103's HTTP reach
(present-attribute deny-lists move ALLOW→DENY) and leaves it untouched for absent attributes,
and move the residual to the honest one: *a deny-list predicate over an attribute the
resolver did not populate still allows* — which is 103, unchanged and correctly deferred.
Then file the newly-discovered live defect: **`actor.Attributes.*` deny-list predicates
currently allow every HTTP caller, because the transport drops attributes** — as a backlog
item in its own right, since it is true at `main` today whether or not ADR-0189 ships.

### F2 — the seam's stated isolation guarantee is false for exactly the payload (d) newly admits, and `ActorFromContext` clones nothing on the way out — [MAJOR]

**Pair:** (a) the `authz` context seam × (d) `Attributes` now flow. Second leg is
(a) × its own mechanism.

**What (a) assumes (d) hands it:** spec §3.1's godoc and ADR Decision 1 both state flatly
that `ContextWithActor` *"stores `a.Clone()` under an unexported struct key, so a later
mutation by the caller cannot reach the engine"*. That is the entire isolation argument for
putting a mutable struct in a `context.Context` and forwarding it into a **durable** record
(`wrkflw_human_task`, `wrkflw_instances.snapshot` — ADR-0147 passthrough, which this bundle
itself leans on twice).

**Why that assumption fails — two legs:**

1. **`Actor.Clone` is one level deep, and says so.** Its own godoc (`authz/authz.go:41-47`):
   *"Attributes are cloned one level deep: nested maps and slices inside an attribute value
   remain shared."* Every realistic identity middleware puts a **nested** object in
   `Attributes` — JWT/OIDC claims are objects. So the guarantee holds for `Roles` and for
   top-level attribute keys and **fails for nested ones**. Before (d) this was inert: all
   three endpoints dropped `Attributes`, so clone depth on that field could not be observed
   on the HTTP path at all. (d) makes `Attributes` the payload; (a) is the mechanism whose
   documented depth does not cover it. Neither decision is wrong alone.

2. **`ActorFromContext` returns the stored `Actor` uncloned** (`return a, ok`). The clone-in
   is therefore not even symmetric: any holder of the returned value mutates the value every
   later reader sees, including the transport's own read. The record reasons about the
   *writer's* later mutation and not at all about the *reader's*.

**Evidence:** executed at `7fa756d0`, spec §3.1's seam reimplemented verbatim in
`authz/zzprobe2_test.go` (throwaway, deleted).
`go test -count=1 -v -run '^TestZZProbeSeamCloneDepth$' ./authz/...` → `EXIT=0`, `--- PASS`:

```
PROBE after-caller-mutation ok=true roles=[viewer] tier=bronze claims.role=manager
PROBE after-reader-mutation tier=diamond
```

`roles=[viewer]` and `tier=bronze` — the guarantee holds at depth 0 and 1, as claimed.
`claims.role=manager` — the caller mutated `Attributes["claims"]["role"]` **after**
`ContextWithActor` and it **reached the stored actor**. `tier=diamond` — a second reader
mutated what `ActorFromContext` handed it and the context-stored actor changed with it.

Nothing downstream re-deepens the copy: `authz.CloneActors` delegates to the same shallow
`Actor.Clone`, so the nested map stays shared from middleware through the endpoint, the
service, the engine and into the audit record. The authorization decision at time T and the
durable record written at T+1 can therefore disagree, with no attacker involved.

**Concrete fix:** pick one and say which.
(i) **Cheap and honest** — amend the §3.1 godoc and ADR Decision 1 to the depth `Actor.Clone`
actually provides: *"Roles and top-level attribute keys are isolated; values nested inside an
attribute are shared — do not mutate a map you have handed to `ContextWithActor`."* Add
`Clone()` to `ActorFromContext`'s return so the read path is symmetric with the write path
(cost: one allocation per request on a path that already allocates a `context`).
(ii) **Stronger** — deep-copy attributes in `ContextWithActor` only. Costs a new deep-clone
helper in a package whose purity comment the record is careful to preserve.
Either way, the unqualified sentence must not ship: it is the kind of summary claim
CLAUDE.md's Premise Discipline names, and it is now load-bearing for a durable record.

### F3 — putting resolution INSIDE the endpoint puts authentication BEHIND ADR-0186's body read; the rejected alternative had the opposite property and the record never weighs it — [MAJOR]

**Pair:** (b) resolver is a parameter on the `httpcore` endpoints × ADR-0186 (the inbound
body cap + read deadline). Also (b) × (c).

**What (b) assumes ADR-0186 hands it:** the ADR justifies resolving in `httpcore` rather than
in the nine adapter call sites on drift grounds alone — *"resolving there would triple both
the resolver invocation and the 401/503 classification — and a security decision duplicated
at nine sites is a drift surface."* It then claims in Consequences/Positive: *"Forgetting the
seam fails closed at every entry."* Both sentences assume the endpoint **is** the entry.

**Why that assumption fails:** it is not. In all three adapters the body is capped, read to
completion and JSON-parsed **before** the endpoint function is called, so identity resolution
is the *fourth* thing that happens to an anonymous request, not the first:

- `stdlib/groups.go:135-141` — `decodeRequestBody(cfg, w, req, &in)` then `httpcore.ClaimTask(...)`
- `gin/groups.go:161-172` — `capBody(cfg, gc)` → `gc.ShouldBindJSON(&in)` → `httpcore.ClaimTask(...)`
- `fiber/groups.go:145-151` — `oversizeBody(cfg, c)` → `c.Bind().JSON(&in)` → `httpcore.ClaimTask(...)`

Consequences the bundle does not state:

1. **The ADR-0186 slowloris residual stays fully unauthenticated.** ADR-0186's own
   `CustomizeConfig.BodyReadTimeout` godoc records the MEASURED exposure the cap creates: a
   `Content-Length: 400000` body carrying a complete value then stalling produced *"NO RESPONSE
   after 3s, the goroutine held and its buffer growing toward MaxBodyBytes"*, bounded only by
   the 30s default deadline. After ADR-0189 an anonymous caller still gets that primitive on
   the three task routes — one held goroutine and up to 1 MiB for up to 30s per request, with
   no credential. A reader of "fails closed at every entry" would reasonably conclude
   otherwise.
2. **An unauthenticated caller gets a 400/413 error oracle.** Malformed JSON answers 400 and an
   oversize body answers 413 to a caller who was never identified, so body-shape probing of the
   task routes is available pre-auth.
3. **The rejected alternative did not have this property.** Resolving in the adapter — before
   `decodeRequestBody` — would have refused the anonymous request before any body was read.
   The record rejects it purely on drift-surface grounds and never mentions that the rejection
   costs pre-auth refusal. That is the interaction: a correct argument about *maintainability*
   silently decided a *security ordering* question, in the one bundle whose subject is
   authentication.

**Evidence:** the three call-site orderings above, read at `7fa756d0`; ADR-0186's measurement
quoted from `transport/http/httpcore/seam.go`'s `BodyReadTimeout` godoc (in-repo, not
inherited). The spec's own §2.4 confirms the shape — *"all nine take one added argument and
**none gains a branch**"* — which is exactly what forecloses refusing before the decode.

**Concrete fix:** do not restructure — the drift argument for single-site resolution is sound.
But the record must (i) **state the ordering explicitly** in Decision 2 (*"resolution happens
after the adapter's capped body read; the body cap and its 413/400 remain reachable
unauthenticated"*), (ii) **retract or qualify "fails closed at every entry"** to "no path
yields a zero actor" — which is the claim actually proved — and (iii) add the pre-auth DoS
and error-oracle surface to §4 Residuals, since the bundle's own standard is that a documented
residual is still a shipped defect and an *undocumented* one is worse. If pre-auth refusal is
wanted later, the honest mechanism is consumer middleware in front of the mount, which
`CustomizeConfig.Wrap` already supports — say so.

### F4 — (e) empties `ClaimInput` to a zero-field struct while the claim route still demands a body; the bundle only reasons about clients that KEEP sending one — [MINOR]

**Pair:** (e) DTO fields removed × ADR-0186's required-body decode contract. Also (e) × §2.3.

**What (e) assumes the decode path hands it:** §2.3's rollout argument is that *"a body still
carrying `"actor"` or `"by"` is IGNORED, not rejected"*, executed for all three adapters — so
in-flight clients are safe. That reasons entirely about the client that **keeps** its old body.

**Why that assumption fails:** it never considers the client that does the obvious thing.
`ClaimInput` declares exactly one field, `Actor Actor \`json:"actor"\`` (`httpcore/dto.go:41-43`).
Removing it leaves **`type ClaimInput struct{}`** — the claim route's request body becomes
semantically empty. A consumer told "the actor moved to your authentication middleware" will
stop sending a body. But `decodeRequestBody` is documented as decoding a **REQUIRED** JSON body
(`stdlib/body.go:124-126`) and all three adapters use the required form on claim.

Measured: decoding an empty body into a zero-field struct returns `EOF`, which every adapter
wraps in `httpcore.ErrBadInput` ⇒ **400 `bad_request`** with a JSON-decoder message.

**Evidence:** executed at `7fa756d0`, `authz/zzprobe3_test.go` (throwaway, deleted),
`go test -count=1 -v -run '^TestZZProbeEmptyBodyDecode$' ./authz/...` → `EXIT=0`, `--- PASS`:

```
PROBE body=""   decode err=EOF     <-- 400 bad_request
PROBE body="{}" decode err=<nil>   <-- 200
```

So after this record the correct way to claim a task is `POST … -d '{}'` against a DTO with no
fields. The behaviour predates the change; what (e) does is remove the last reason a client
would send a body, converting a dormant wart into the *default* client mistake on the exact
route backlog 51 is named for. Nothing in the spec, ADR or plan mentions it, and the plan's
own §2.3-derived migration note tells consumers only that the stale key is tolerated.

**Concrete fix:** switch the claim route to the **already-existing** `decodeOptionalRequestBody`
(`stdlib/body.go:154-`, built by ADR-0186, ignores decode failures but still refuses an
oversize body with 413) and its gin/fiber equivalents, so a bodyless claim is 200. If that is
judged out of scope, then say so in the migration section in one line — *"`POST /tasks/{token}/claim`
still requires a JSON body; send `{}`"* — rather than leaving consumers to find it as a 400.
Either way `ClaimInput`'s godoc ("No fields are required — an empty actor is allowed") must be
rewritten; after (e) it describes a field that no longer exists.

### F5 — the (f) removal grid was never derived: nine surviving call sites acted with no identity, and the bundle's own budget ("two rewrites") is off by an order of magnitude — [MAJOR]

**Pair:** (f) `WithAnonymousActorAllowed` removed × every survivor that mounts a task route
without one. The brief's core instruction — *derive the survivor×removed pairs explicitly* —
is not done anywhere in the three documents.

**What the survivors assumed (f) would hand them:** under ADR-0185 D1, a mount with no
identity wiring had a supported, named mode. (f) deletes it, so **every** existing caller of
a task route on a bare mount must now supply an identity or be rewritten to expect 401. The
bundle's inventory of that population is §2.6's `29 / 9 / 5` — but that list was derived from
compile ablation **plus a grep for `"actor"`/`"by"` keys**, so it can only see call sites that
*mention the removed thing*. The population that matters for (f) is different: it is every
site that **reaches a task route**, mentioned actor or not.

**Why that assumption fails — the survivors, enumerated:**

| survivor | sites | expects today | after (f) | in the 29? | budgeted? |
|---|---|---|---|---|---|
| `gin/gin_test.go` `TestTaskRoutes_*` | 413, 421, 443, 453 | **200** | 401 | yes | Task 8 Step 2 ✅ |
| `gin/gin_coverage_test.go` `*_ErrorPath` | 192, 218, 244 | **404** | 401 | yes | Task 8 Step 2 ✅ |
| `fiber/fiber_test.go` | 563, 585, 592, 615, 624 | 200 | 401 | yes | Task 9 Step 2 ✅ |
| `stdlib/errors_test.go` | 155/158, 187/190 | **403** | 401 | yes | Task 7 Step 2 ✅ (the only two the ADR names) |
| `stdlib/coverage_test.go` `TestTaskRoutes_{Complete,Reassign}` | 92, 126 | **200** | 401 | yes | **NO** — Task 7 Step 1 deletes the key and Step 2 covers only the two 403 pins |
| `stdlib/stdlib_test.go` | 471 | 200 | 401 | yes | **NO** — same gap |
| `parity/parity_test.go` `TestParity_PostTasksClaim_200` | 518 | **200 ×3** | 401 ×3 | yes | **NO** — Task 10 Step 1 removes the key, Step 2 adds a *new* 401 case, nothing restores the 200 |
| `gin/gin_coverage_test.go` malformed-body rows | 183, 209, 235 | 400 | 400 | **NO** | not needed — survives |
| `stdlib/coverage_test.go` `errReader{}` rows | 148, 172, 196 | 400 | 400 | **NO** | not needed — survives |
| `fiber/bodylimit_test.go` `TestEveryDecodeSiteIsBounded` task rows | 520, 521, 522 | 413 | 413 | **NO** | not needed — survives |

Three separate defects fall out of this grid:

1. **The ADR's recorded cost is wrong.** Consequences/Negative says *"**Two** `stdlib` tests
   are rewritten, not recompiled"*, and spec §5 repeats it. The plan is right where the ADR is
   wrong — plan line 54 says *"18 JSON-body pins in `gin`/`fiber`/`stdlib`/`parity` still send
   `"actor"`/`"by"` and will now get **401**"* — so **all 18** are rewrites, not two. The "two"
   is an inherited artifact of ADR-0185's *vacuous-pin* analysis (which pins asserted a 403 the
   zero actor made meaningless), restated in a slot that asks a different question (which pins
   change behaviour). CLAUDE.md's Premise Discipline names exactly this: *"re-verify claims you
   inherit before restating them."* The ADR is the durable record and it undercounts its own
   blast radius 9×.

2. **Three plan tasks end RED at a step that expects EXIT=0.** Task 7 Step 1 deletes the actor
   key from *all five* stdlib bodies but Step 2 mounts a resolver for only the two 403 pins;
   `coverage_test.go`'s two 200-expecting tests and `stdlib_test.go:471` are left keyless and
   identity-less ⇒ 401 ⇒ Step 5's `expect 0` fails. Tasks 8 and 9 avoid this because they carry
   a **generic** Step 2 (*"mount with `WithRequestActor(...)` wherever a specific identity is
   needed"*); Task 7 does not, and Task 10 does not. The asymmetry is invisible unless the grid
   is written down. A prescriptive subagent brief that terminates in an unexplained RED on a
   security path is where improvisation happens.

3. **Task 10 (parity) is under-budgeted for a structural reason.** `hitStdlib`, `hitGin` and
   `hitFiber` (`parity_test.go:141,156,192`) hardcode `Mount(x, svc)` and take **no options
   parameter at all**. Restoring `TestParity_PostTasksClaim_200` therefore needs each helper to
   accept a per-adapter option — and the three are *different types*
   (`CustomizeOption[*http.ServeMux]`, `CustomizeOption[ginlib.IRouter]`,
   `CustomizeOption[fiberlib.Router]`), so one shared variadic cannot carry them. This is a
   direct consequence of (b) making the seam a per-adapter alias; Task 10 spends one bullet on
   it. The alternative — three different middleware idioms in the parity harness — defeats the
   shared `reqFactory` the suite is built around.

**Evidence:** executed/read at `7fa756d0`. Route-site inventory by
`grep -rn --include='*.go' -E '"/tasks/|/tasks/"' . | grep -v /groups.go`, which finds
**27** task-route references across 9 files against the bundle's 18 pins. Expected statuses
read at each site. The three "survives" rows survive for one reason only — the decode runs
before resolution (see **F3**) — and `fiber/bodylimit_test.go`'s own comment states it:
*"The pre-check runs BEFORE any service call, so the path parameters need not resolve to real
entities — every row 413s on the body alone."* Confirmed by running it:

```
$ go test -count=1 -v -run '^TestEveryDecodeSiteIsBounded$/POST_/tasks' ./transport/http/fiber/...
EXIT=0
=== RUN   TestEveryDecodeSiteIsBounded/POST_/tasks/:token/claim
=== RUN   TestEveryDecodeSiteIsBounded/POST_/tasks/:token/complete
=== RUN   TestEveryDecodeSiteIsBounded/POST_/tasks/:token/reassign
--- PASS
```

⚠ Note what that means: **nine survivors are benign purely because of an ordering the bundle
never states.** Move resolution ahead of the decode — the natural "fix" for F3 — and all nine
flip from 400/413 to 401 at once. The bundle has no record that would warn anyone.

**Concrete fix:**
- Correct the ADR Negative bullet and spec §5 to *"all 18 runtime pins are rewrites; two of
  them (`stdlib/errors_test.go:158`, `:190`) additionally move from vacuous to load-bearing and
  are the ones proved by mutation."*
- Give Task 7 and Task 10 the same generic step Tasks 8/9 have, and name the specific sites:
  `coverage_test.go` 92/126 and `stdlib_test.go` 471 need a mounted resolver, not just a key
  deletion; `TestParity_PostTasksClaim_200` needs the three helpers to gain per-adapter option
  parameters (say so, with the three types spelled out, so the agent does not discover it).
- Add the nine ablation-invisible survivors to §2.6 as an explicit *"reaches a task route but
  never named the actor"* list, with the one-line reason each survives, so the dependency on
  F3's ordering is on the record.

### F6 — (f)'s justification is refuted by (f)'s own replacement: §2.5 argues the removal is cheap BECAUSE the demo mains 401, then §3.6 makes them 200-as-manager — [MAJOR]

**Pair:** (f) `WithAnonymousActorAllowed` removed × Decision 4 / §3.6 (the three wiring mains
and `examples/authenticated_tasks`). Secondary: (f) × (g) backlog 52.

**What (f) assumes §3.6 hands it:** the *entire* cost argument for removing the anonymous mode
is §2.5. It executes that the three wiring mains never claim a task, concludes *"The real
exposure is narrower — **a reader who `curl`s the mounted task route gets 401** — which is what
§3.5 addresses, and it is a materially weaker argument for a library-provided anonymous mode
than F5 implied."* The removal is cheap **because the demo deployments answer 401**.

**Why that assumption fails:** §3.5 and §3.6 then eliminate that 401. They prescribe, for all
three shipped mains, a resolver returning
`authz.Actor{ID: "demo-user", Roles: []string{"manager"}}`. A reader who `curl`s a mounted task
route on `production_wiring` does not get 401 — they get **200, as a manager**. The state §2.5
priced the removal against is a state the design does not ship. The two sections cannot both
be true, and the one that ships is the worse of the two.

Now put (f)'s stated rationale next to its replacement. The ADR kills the library-provided
mode because *"The library never picks the string. That matters because it lands in the durable
audit record, where the library cannot know it does not collide with a real principal."* Every
clause of that applies **more** forcefully to the replacement:

| property | `WithAnonymousActorAllowed()` (removed) | the prescribed demo closure (shipped) |
|---|---|---|
| what the call site says | the option name says "anonymous" | an anonymous `func` literal a reader must open |
| greppable in a consumer's repo | one exported symbol | not greppable — it is a closure |
| identity in the durable record | a library sentinel, recognisable as one | `"demo-user"` — indistinguishable from a real principal, which is the exact hazard cited |
| privileges granted | none inherent to the mechanism | **`Roles: ["manager"]`** |
| deprecation / startup warning | an exported symbol can be deprecated, or log "this mount is unauthenticated" | nothing to deprecate, nothing to warn on |
| where it lives | in `httpcore`, ours | in `examples/`, the most copy-pasted surface in a library repo |

And (f) × (g): the record leaves backlog 52 open, so `service.NewProcessEngine` still defaults
to `authz.AllowAll`. Inside this repo the `["manager"]` role is therefore inert and no test can
notice it. The moment a consumer copies `production_wiring/main.go` — which is what
`examples/` is *for* under CLAUDE.md — and plugs in a real `RoleAuthorizer`, every request is a
manager, silently. The deferral of 52 is precisely what removes the only mechanism that would
have caught the demo constant being over-privileged.

**Evidence:** spec §2.5 (the executed grep showing no claim call sites, and the "gets 401"
conclusion) read against spec §3.5/§3.6 and ADR Decision 2/Decision 4, at `7fa756d0`. The role
string `"manager"` appears in the prescribed snippet in both documents. `authz.AllowAll`
confirmed still the permissive default at `authz/authz.go:100-106`; nothing in the bundle
changes it, and §4.3 says so.

**Concrete fix:** three things, and the first is not optional.
1. **Resolve the contradiction.** Either the mains stay bare and answer 401 — in which case
   §2.5's argument stands, and the mains need a comment saying the task routes are mounted but
   unauthenticated by design — or they carry the demo actor, in which case §2.5's *"the real
   exposure is narrower, they get 401"* must be deleted, and (f) needs a cost argument that
   survives the thing actually shipping. Recommend the former: a 401 from a demo server is
   self-documenting and matches the record's own fail-closed thesis.
2. If the demo constant ships anyway, **drop `Roles: ["manager"]`**. Nothing in the three mains
   claims a task (§2.5 executed this), so the role buys nothing and costs a privileged
   copy-paste. Use `authz.Actor{ID: "demo-user"}` and let backlog 52's default do the
   permitting, so the permissiveness has exactly one source instead of two.
3. Give `examples/authenticated_tasks/` the job the removed symbol used to do: make the
   *unauthenticated* mount an explicit, named, commented case in that example, so the open
   deployment has one canonical shape a consumer can grep for — rather than a three-line
   closure reproduced in four places.

### F7 — "ADR-0117 becomes true rather than changed" equivocates AUTHENTICATION for AUTHORIZATION; 0117's deferral is still unsatisfied, for a new reason — [MAJOR]

**Pair:** (a)+(b) the identity seam × ADR-0117's deferral. Also (c) × ADR-0117.

The bundle explicitly asks the audit to attack this (spec §6), so here it is.

**What ADR-0189 assumes ADR-0117 hands it:** the ADR's Neutral section says *"ADR-0117 needs no
amendment. Its Decision 1 defers the open case to the consumer's transport layer; this record
supplies the transport layer that deferral assumed, so 0117 becomes true rather than changed."*
Decision 3 restates it: *"`Open` eligibility therefore means "any **authenticated** actor",
which is what ADR-0117's deferral to the transport always presumed."*

**Why that assumption fails:** ADR-0117 does not defer *authentication*. Read at
`docs/adr/0117-optional-usertask-eligibility.md`:

- `:15-17` — *"the runtime already treated an **empty** eligibility spec as an open engine gate,
  **deferring authorization** to the consumer's transport layer (e.g. HTTP security
  middleware)."*
- `:47-48` — *"with none set, the engine gate is open and **authorization defers to the
  transport layer**."*

Both sentences say **authorization**, twice, and gloss the transport layer as *"HTTP security
middleware"* — i.e. something that decides *may*, not merely *who*. ADR-0189 supplies *who* and
then **explicitly forbids the seam from carrying *may***: Decision 3 rules that a resolver
returning `authz.ErrNotAuthorized` classifies **503, not 403**, on the stated ground that *"An
identity resolver answers who, not may."* That is a defensible rule (see F8 for its own cost),
but it means the seam this record adds is precisely *not* the thing ADR-0117 deferred to.

So the state of 0117's deferral changes from *unsatisfied because the transport has no
identity* to *unsatisfied because the transport has an identity and is documented not to
authorize with it*. Concretely, after ADR-0189 ships with backlog 52 open (`authz.AllowAll` is
still `service.NewProcessEngine`'s default — `authz/authz.go:100-106`, and §4.3 confirms it is
untouched), an authenticated actor with **zero roles** satisfies an empty eligibility spec on
any open user task. 0117's sentence still describes something that does not happen.

Note the record gets this right elsewhere and wrong only in the recap: §4.3 states the residual
honestly — *"anyone authenticated can be anyone the configured authorizer permits"*. The
Neutral section then compresses that into "0117 becomes true", which is the summary-sentence
failure mode CLAUDE.md's Premise Discipline names by name.

**Concrete fix:** amend ADR-0117 with a one-line pointer, and correct 0189's Neutral section to
what is actually true: *"ADR-0189 supplies the transport-layer **identity** ADR-0117's deferral
presupposed. It does not supply transport-layer **authorization** — 0117's `e.g. HTTP security
middleware` remains the consumer's own middleware in front of the mount, because this record
rules the identity seam answers who and not may (Decision 3). An empty eligibility spec is
therefore open to any authenticated actor, and stays so until backlog 52/53."* Then delete
"becomes true rather than changed"; it is the equivocation in one clause.

### F8 — the supersession is asserted in one document and contradicted by the two a fresh session is told to read first — [MAJOR]

**Pair:** (f) removed × ADR-0185 and `docs/plans/HANDOVER.md`, neither of which the bundle
touches.

**What ADR-0189 assumes those documents hand it:** the header claims
*"**Supersedes-in-part:** ADR-0185 Decision 1 only. ADR-0185 stays Proposed-and-failed for its
D2/D3; this record replaces its D1 and is the one to implement."* That is a statement about
what a *reader arriving from elsewhere* will conclude.

**Why that assumption fails — three ways:**

1. **Neither document is updated.** `git show --stat HEAD` on the bundle commit `7fa756d0`
   lists exactly three files: the ADR, the spec and the plan. `docs/adr/0185-*.md` and
   `docs/plans/HANDOVER.md` are untouched.

2. **`HANDOVER.md` still routes the next session into the superseded design.** It is the
   repo's declared SOURCE OF TRUTH (CLAUDE.md rule #10: *"a fresh session reads first"*), and at
   `:147` it says backlog 51 *"is D1 of the parked ADR-0185"*, at `:151` *"The design below comes
   from `docs/adr/0185-authorization-identity-is-not-self-asserted.md`"*, and at `:155`
   *"Read it before writing anything."* A fresh session following its own instructions lands on
   ADR-0185's D1 — including `WithAnonymousActorAllowed`, the mechanism (f) deletes — with no
   marker that D1 is dead. ADR-0185's banner (`:3-16`) is about its **third audit failure** and
   names 0189 nowhere.

3. **A Proposed record cannot be superseded.** ADR-0185's status is Proposed-and-failed; nothing
   was ever accepted, so nothing is superseded. Under the Nygard template this repo pins
   (`docs/adr/0001-record-architecture-decisions.md`), the states are Proposed → Accepted →
   Superseded/Deprecated. "Supersedes-in-part a Proposed record" is a category error that will
   read, to a later maintainer, as though 0185 D1 was once in force.

This is the (f) removal grid at document level: the mechanism was removed from the design and
left standing in every place a reader is pointed at.

**Evidence:** `git show --stat 7fa756d0` (three files, listed above); ADR-0185 header lines 1-16;
`HANDOVER.md` lines 42, 147, 151, 155, 219 — all read at `7fa756d0`.

**Concrete fix, all three in the same bundle commit (rule #10 says the handover rides with it):**
- Add a banner to `docs/adr/0185-*.md`: *"⛔ **D1 is DEAD.** Replaced by ADR-0189, which drops
  `WithAnonymousActorAllowed` and re-derives the empty-`Actor.ID` rule. Do not implement D1 from
  this record. D2/D3 remain Proposed-and-failed and need their own ADRs."*
- Rewrite `HANDOVER.md`'s NEXT WORK section to point at ADR-0189 as the bundle to implement,
  and demote the ADR-0185 references to provenance.
- Restate the header relation as *"**Replaces** ADR-0185 Decision 1 (never accepted; that record
  stays Proposed-and-failed)"* rather than "Supersedes-in-part", or mark 0185 **Rejected**.

### F9 — (b)'s per-adapter option makes `httpcore.MountGroups`, the documented consumer extension seam, unable to mount a working task API — [MAJOR]

**Pair:** (b) resolver supplied via `CustomizeOption` × the existing `MountGroups` public seam.
Invisible to §2.6's ablation, which can only see code that names the removed fields.

**What (b) assumes `MountGroups` hands it:** the whole (b) design routes the resolver through
`CustomizeConfig.RequestActor`, populated by `WithRequestActor` options passed to a group's
`Customize`. That assumes every supported way of mounting a group can carry options.

**Why that assumption fails:** `httpcore.MountGroups` cannot.

```go
// MountGroups mounts each group onto r at its current position (no extra opts).
// It is also the consumer extension seam: any RouteCustomizer[R] — including a
// consumer's own — can be passed. ...
func MountGroups[R any](r R, groups ...RouteCustomizer[R]) {
	for _, g := range groups {
		g.Customize(r)   // <- zero options, always
	}
}
```
(`transport/http/httpcore/seam.go:204-210`.) It is exported public API, it is named in the gin
package doc as the uniform path — *"Every group struct implements
`httpcore.RouteCustomizer[gin.IRouter]` so that `httpcore.MountGroups` and consumer code can
treat gin and stdlib groups uniformly"* (`transport/http/gin/options.go:6-7`) — and it hardcodes
`Customize(r)` with no opts. So `Customize` runs `ResolveConfig()` with nothing, the post-loop
guard installs `defaultRequestActor`, nothing is on the context unless the consumer also wrapped
the router in middleware, and **every task request 401s** with no supported way to inject a
resolver. The remedy is to stop using the documented seam and call `TaskRoutes{}.Customize`
directly.

It fails **closed**, so this is not a vulnerability — it is a total, silent functional break of a
public API the product is defined as (CLAUDE.md: *"the exported module-root packages ARE the
product"*), and the ADR's BREAKING bullet does not mention it. It enumerates the DTO fields, the
deleted type, the endpoint signature and the 29 pins — every item of which the ablation could
see — and misses the one that only a reader of the seam's godoc would find.

**Evidence:** `transport/http/httpcore/seam.go:204-210` and `transport/http/gin/options.go:1-14`,
read at `7fa756d0`. `grep -rn 'MountGroups' .` finds it referenced only by its own test and that
package doc — i.e. it exists **solely** for consumers, so no in-repo test will notice the break.

**Concrete fix:** give `MountGroups` a variadic — `MountGroups[R any](r R, opts []CustomizeOption[R],
groups ...RouteCustomizer[R])` is source-breaking; the compatible shape is a second function or
`MountGroupsWith(r, opts, groups...)`, with `MountGroups` delegating. Either way it must be a
task in the plan, and the ADR's BREAKING bullet must name it. If the decision is to leave it,
say so explicitly and fix its godoc — *"a task group mounted through `MountGroups` has no
identity resolver and answers 401; mount `TaskRoutes` with `Customize` and
`WithRequestActor` instead"* — because a seam documented as the consumer extension point and
silently unusable for one of the three groups is worse than one that says it.

### F10 — the escape hatch (c) points at is emptied by (g): "return the actor and let the Authorizer decide" cannot deny, because the Authorizer defaults to allow-all — [MAJOR]

**Pair:** (c) the 503-not-403 rule × (g) backlog 52/53 deferred.

This is the ADR-0185-second-audit pattern the interaction brief names explicitly: *"the hoisted
spec gate emptied the escape hatch another decision pointed at."* It has happened again.

**What (c) assumes (g) hands it:** Decision 3 rules that a resolver returning
`authz.ErrNotAuthorized` classifies **503, not 403**, and offers a specific remedy: *"A consumer
who wants to deny a principal they **have** identified returns the actor and lets the
`Authorizer` decide, or refuses in their own middleware before the handler runs."* The first
half of that sentence assumes the `Authorizer` is in a position to deny.

**Why that assumption fails:** construct the consumer §6 item 1 asks for. An OIDC middleware
identifies the caller perfectly — `sub`, `roles`, the lot — and the same token says the caller
lacks the `wrkflw:tasks` scope. The consumer has a **known** principal and a legitimate 403.
Their three options under this record:

1. Return `authz.ErrNotAuthorized` from the resolver → **503**. Wrong status, wrong semantics,
   and the client is told the identity system is down when it is working perfectly.
2. *"Return the actor and let the `Authorizer` decide"* → the `Authorizer` is whatever
   `service.NewProcessEngine` was given, and **backlog 52 leaves that defaulting to
   `authz.AllowAll`** (`authz/authz.go:100-106`; §4.3 confirms 52 is untouched). Even with a real
   authorizer configured, **backlog 53 leaves an empty `AuthzSpec` meaning allow-all**, and the
   spec is authored in the process *definition* — not by the consumer wiring the transport. So
   the consumer cannot express "deny this principal" through this route at all unless they also
   own every definition's eligibility spec. The escape hatch is empty.
3. Refuse in their own middleware before the handler → works, but it means the consumer must
   duplicate route knowledge (which paths are the task routes, under which `BasePath`) outside
   the library, and it makes the identity seam unusable for the one consumer shape that most
   needs it.

Each decision is right alone: 503-for-a-broken-identity-provider is well argued, and deferring
52/53 is right given their designs were refuted. Together they leave a consumer with a known,
unauthorized principal no supported way to get a 403.

**Evidence:** ADR Decision 3 and spec §3.4 read against §4.3 and `authz/authz.go:100-106`, at
`7fa756d0`. `AllowAll.Authorize` returns nil unconditionally; `AuthzSpec`'s own godoc
(`authz/authz.go:79-81`) states *"An empty spec means allow-all."*

**Concrete fix:** the rule can stand, but the remedy sentence must be replaced with one that is
reachable today. Either (i) keep 503-for-`ErrNotAuthorized` and say plainly that **a
transport-level deny belongs in consumer middleware in front of the mount** (with a pointer at
`CustomizeConfig.Wrap`, which already exists for exactly this), deleting the "let the
`Authorizer` decide" half as unreachable while 52/53 are open; or (ii) add a third sentinel,
`ErrRequestForbidden` → 403, that a resolver may return to deny an *identified* principal —
which keeps who/may separate (the resolver is still not authorizing the task, it is refusing the
request) and gives the consumer the status they need. (i) is cheaper and keeps this record
narrow; whichever is chosen, the current sentence must go, because it names an escape hatch that
does not open.

### F11 — (d) changes what two DURABLE columns carry, silently falsifying ADR-0187's just-shipped, machine-checked at-rest classification — [MAJOR]

**Pair:** (d) `Attributes` now flow × ADR-0187 (the at-rest posture, merged `4e2c0af4`, three
days before this bundle was written). The bundle mentions neither ADR-0187 nor `internal/atrest`.

**What (d) assumes ADR-0187 hands it:** nothing — and that is the defect. The record reasons
about `Attributes` reaching *the authorizer* and stops there. Spec §6 item 4 even asks the audit
*"What else observes `Actor.Attributes` — the audit record, the snapshot, the casbin
authorizer's attribute leg — and does any of it now see a shape it did not before?"* The answer
is yes, and the bundle does not contain it.

**Why that assumption fails:** `Actor.Attributes` is persisted verbatim.
`internal/persistence/store/humantask_store.go:552-560`:

```go
// htActorRemainder is the JSON shape of the claim_actor / completion_actor
// columns: everything an [authz.Actor] carries except its ID …
type htActorRemainder struct {
	Roles      []string       `json:"roles,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}
```

ADR-0187 classifies both of those columns (`internal/atrest/classification.go:200,202`):

```go
{Table: "wrkflw_human_task", Column: "claim_actor"}:      ClassActor,
{Table: "wrkflw_human_task", Column: "completion_actor"}: ClassActor,
```

`ClassActor` is *"identifies a human principal"* (`:11-12`); the neighbouring `ClassFreeform` is
*"unstructured, application-authored content: JSON blobs, notes, error text, snapshots. **No
shape is assumed**"* (`:8-10`). **Today the classification is defensible because the transport
drops `Attributes` at all three sites**, so `claim_actor` over HTTP carries only a closed set of
role strings. After (d) it carries an arbitrary consumer-authored `map[string]any` — whatever a
JWT/OIDC middleware puts in `Attributes`: email, employee id, group memberships, raw token
claims. That is `ClassFreeform`'s definition, not `ClassActor`'s.

Two further consequences the bundle owes an answer to:

- **The classification file forbids silent change.** `:106-113`: *"This is a stated judgement,
  not a derivation: transcribed verbatim from `docs/specs/2026-08-22-at-rest-posture.md` … **Do
  not re-derive or second-guess any entry here**"*, with `TestClassificationPerClassCounts`
  pinning per-class totals. So if two columns should move class, the change is not local — it
  touches the spec ADR-0187 generates from, and it moves pinned counts.
- **No guard can catch this.** `TestClassificationCoversTheSchemaExactly` checks which *columns*
  exist; nothing checks that a class still describes a column's *content*. This is the exact
  lesson ADR-0187's own delivery recorded — *a guard can be blind to the category of claim it was
  built to police* — replaying one bundle later, from the other side.
- **`humantask_store.go:420-429` states a maintenance rule that (d) makes fragile**: *"Put it
  above this line if anything filters, authorizes, or routes on it; below only if losing it costs
  nothing but display"*, and it places `claim_actor` below on the ground that it *"hold[s] only
  the actor's roles and attributes … Losing this remainder costs display detail and nothing
  else."* That remains true for the four `runtime/task` verbs today (the reassign guard at
  `runtime/task/service.go:229` reads only `Claim.Actor.ID`, which lives in its own scalar
  column). But (d) makes attributes an authorization **input at claim time**, so the sentence is
  now one deferred backlog item (124, the claimant guard) away from being false, and the comment
  gives no hint of it.

**Concrete fix:** add a section to the ADR — *"What `Attributes` reaches once it flows"* —
covering the authorizer, `wrkflw_human_task.claim_actor` / `completion_actor`, and
`wrkflw_instances.snapshot`. Adjudicate ADR-0187's classification explicitly: either keep
`ClassActor` with a stated reason (*"the column identifies a principal; its remainder is now
consumer-shaped but the column's role is unchanged"*) recorded in `docs/specs/2026-08-22-at-rest-posture.md`
so the next reader is not left to guess, or move both to `ClassFreeform` and take the per-class
count change. Silence is the one option rule #9 forbids. Also add a line to `SECURITY.md`'s new
middleware section warning that **whatever a resolver puts in `Actor.Attributes` becomes durable
audit data**, so consumers do not park raw token claims there.

### F12 — §2.3's "ignored, not rejected" rollout guarantee is conditional on a gin global the CONSUMER controls, and is stated unconditionally — [MINOR]

**Pair:** (e) DTO fields removed × ADR-0167's strict-decoding direction of travel.

**What (e) assumes the decoders hand it:** §2.3 and ADR Decision 1 conclude, from an executed
probe, that *"A body still carrying `"actor"` or `"by"` is IGNORED, not rejected. Executed for
all three adapters"*, and use it to argue *"a 400 would buy no security … and would break
consumers' rollout windows."* The rollout-window argument is the reason (e) is judged tolerable.

**Why that assumption fails:** for gin it is a property of *this repo's configuration*, not of
the adapter. §2.3 says so itself — *"`gin` uses `gc.ShouldBindJSON`, tolerant unless the global
`EnableDecoderDisallowUnknownFields` is set, which nothing sets"* — and then the ADR restates the
conclusion with the condition stripped. `binding.EnableDecoderDisallowUnknownFields` is a
settable package-level variable in the pinned gin v1.10.0
(`$GOMODCACHE/github.com/gin-gonic/gin@v1.10.0/binding/json.go:21`), and it is set by the
**consumer's** process, not by us — a single global that a consumer hardening their own API
turns on once. For that consumer, removing the DTO field turns every in-flight client's request
into a **400**, which is precisely the rollout break §2.3 rules out.

The interaction with ADR-0167 is the uncomfortable part: that record made *definition* decoding
strict as a deliberate repo direction, so a consumer applying the same principle to their gin
binding is following our own lead — and (e)'s safety argument depends on them not having done it.

**Evidence:** gin v1.10.0 `binding/json.go:21` (the global exists and is exported);
`grep -rn "DisallowUnknownFields\|EnableDecoderDisallowUnknownFields" transport/ internal/` at
`7fa756d0` returns no matches, confirming *this repo* does not set it — which is exactly the
scope of the claim that should have been carried forward.

**Concrete fix:** restore the condition in the ADR: *"ignored, not rejected — for `stdlib` and
`fiber` unconditionally, and for `gin` unless the consumer has set
`binding.EnableDecoderDisallowUnknownFields`, in which case a stale `actor` key becomes a 400."*
Put the same sentence in the migration note, since it is the one class of consumer whose rollout
window really does break.

---

## Verdict

**12 findings: 1 CRITICAL, 9 MAJOR, 2 MINOR.** The bundle does not survive this lens as written.

The individual decisions are, on the whole, well argued — better than ADR-0185's were. What is
missing is the pass this lens exists for: **the changed decisions were never taken pairwise.**
The spec's §6 asks the audit to do it (item 6, verbatim: *"(d) is a removal, and a removal
generates its own grid — derive survivor×removed pairs explicitly"*), which is the author
correctly identifying the work and then not doing it. Every finding below F2 is something a
written grid would have surfaced before dispatch.

Three of the twelve are the signature pattern — **a fix in one place opening a hole in another
part of the same document**:

- **F1** — §1.1 correctly deleted ADR-0185's "Attributes closes finding 4's second leg for
  free", citing the refutation that `actor` is a struct so `Attributes` exists at depth-1. §4.2
  then wrote the *opposite-signed* consequence off the same mechanism without re-deriving it.
  Measured: the claim is inverted. This is the only CRITICAL.
- **F6** — §2.5 prices the removal of `WithAnonymousActorAllowed` on the demo mains answering
  **401**; §3.6 then makes them answer **200 as a manager**. The argument for the removal rests
  on a state the design does not ship.
- **F10** — Decision 3's remedy for the consumer who wants a 403 points at the `Authorizer`,
  which (g)'s deferral of backlog 52/53 leaves defaulting to allow-all. The escape hatch is
  empty. Exactly the failure the interaction brief cites from the B3 authz bundle.

Two more are things **no lens but this one is positioned to find**, because they live in the
seam between a changed decision and a *recently shipped* one: **F3** (identity resolves *behind*
ADR-0186's body read, so the "fails closed at every entry" claim overreaches and an
unauthenticated slowloris/error-oracle surface survives unrecorded) and **F11** ((d) changes
what two durable columns carry, silently falsifying ADR-0187's machine-checked at-rest
classification — merged three days before this bundle was written and mentioned nowhere in it).

**F5** and **F9** are the removal grid proper: nine surviving task-route call sites the compile
ablation structurally could not see, three plan tasks that terminate RED at a step expecting
EXIT=0, an ADR that undercounts its own rewrite budget 9×, and a documented public extension
seam (`httpcore.MountGroups`) that after (b) cannot mount a working task API at all.

**F8** is a rule-#10 defect with teeth: the supersession is asserted in ADR-0189 and contradicted
by both `HANDOVER.md` and ADR-0185, neither of which the bundle commit touches — so a fresh
session following the repo's own SOURCE-OF-TRUTH instruction lands on the design this record
deletes.

**Not found — checked and clear.** The 401 body is **static**
(`Message: "the request carries no authenticated actor"`, plan line 334-337), so the consumer
error the `ErrUnauthenticated` arm passes through verbatim does **not** leak to an
unauthenticated client. I went looking for that and the plan had already closed it — though the
*reason* is recorded nowhere, so an editor "restoring the 4xx `err.Error()` convention" would
reopen it. Worth one sentence in `errors.go` beside the standing invariant. Precedence between
`ContextWithActor` and a custom `WithRequestActor` is likewise documented (plan line 454-455):
the custom resolver wholly replaces the default; there are no merge semantics to get wrong.

### The pairwise grid

Rows and columns are the changed decisions. Cell = finding id, or "—" for no interaction found.
Lower triangle only (the relation is symmetric).

| | (a) seam | (b) param | (c) sentinels | (d) attrs | (e) DTO rm | (f) anon rm | (g) deferred |
|---|---|---|---|---|---|---|---|
| **(a) seam** | — | — *(precedence documented)* | — | **F2** | — | F6 | — |
| **(b) param** | | *self:* F3 | F3 | — | **F4** | **F5**, **F9** | — |
| **(c) sentinels** | | | *self:* — | — | — | F5 | **F10** |
| **(d) attrs** | | | | *self:* F2 | — | — | **F1** |
| **(e) DTO rm** | | | | | *self:* F4 | F5 | — |
| **(f) anon rm** | | | | | | *self:* F6 | F6 |
| **(g) deferred** | | | | | | | *self:* — |

Changed decisions × **records outside the bundle** — the axis the grid in §6 omits entirely:

| changed decision | external record | finding |
|---|---|---|
| (b) resolution sited in `httpcore` | **ADR-0186** inbound body cap + read deadline | **F3** — auth resolves behind the capped read; unauthenticated DoS/oracle surface unrecorded |
| (e) DTO fields removed | **ADR-0186** required-body decode | **F4** — `ClaimInput` becomes `struct{}` yet a body is still required |
| (e) DTO fields removed | **ADR-0167** strict decoding | **F12** — "ignored, not rejected" is conditional on a gin global the consumer owns |
| (d) `Attributes` flow | **ADR-0187** at-rest classification | **F11** — two `ClassActor` columns start carrying arbitrary consumer JSON |
| (d) `Attributes` flow | **ADR-0147** audit passthrough | **F2** (shallow clone into a durable record), **F11** (durability of attributes) |
| (a)+(b)+(c) the seam | **ADR-0117** eligibility deferral | **F7** — "becomes true rather than changed" equivocates authn for authz |
| (f) anonymous mode removed | **ADR-0185** + `docs/plans/HANDOVER.md` | **F8** — supersession asserted in one file, contradicted by the two read first |

### What must happen before implementation

**F1** alone changes what the ADR records as the cost of its only non-refusal behaviour change,
and the correction reveals a live fail-open on `main` that is not filed anywhere. **F6**, **F10**
and **F7** each invalidate a stated justification rather than a detail. **F5** and **F9** are
plan-level: dispatching Tasks 7 and 10 as written puts subagents into an unexplained RED on a
security path. None of the twelve is a reason to abandon the design — the core decision (the
actor travels in the context and the transport reads it from nowhere else) survives this lens
intact, and (d) turns out to be a *better* idea than the record claims. But the bundle's
recorded reasoning is wrong in enough load-bearing places that it is not yet an input to
implementation.

### Method note

Executed at `7fa756d0` in worktree `wt-interaction`, all probes written as throwaway
`authz/zzprobe*_test.go` files, run with `-count=1`, judged by `EXIT=`, and deleted;
`git status --porcelain` is empty at the end of this run. Docker was not used and no container
was started. Four probes: actor-attribute predicate evaluation (F1), the §3.1 seam's clone depth
reimplemented verbatim (F2), empty-body decode into a zero-field struct (F4), and
`TestEveryDecodeSiteIsBounded`'s three task rows as corroboration for F3/F5.
