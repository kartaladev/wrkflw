# Round 2 audit — ADR-0189 request actor identity — FAILURE-MODES lens

Worktree: wt2-failure-modes, detached at `37d77a34`.
Step 0: PASS — spec, ADR, plan, author interaction grid all present.

Findings appended below as they are established.

### F1 — the adopted resolver timeout does NOT remove the "hang" state it was adopted to remove, and past the deadline the request proceeds with err==nil — [CRITICAL]

**Bundle text attacked:**
- ADR §Decision 9: *"On fiber, `c.Context()` is `context.Background()` when no middleware set one, so the consumer's resolver runs with **no deadline and no cancellation** — the '503, never an open door' promise has an unnamed third state there: **hang**. The repo already answers this hazard twice (`WithCandidateResolveTimeout`, default 10s); **this record adopts the same bound for the resolver**."*
- spec §3.3: *"⚠ **A resolver-call timeout is adopted** … Without it the promise *'503, never an open door'* has an unnamed third state — **hang**"* (⇒ with it, the state is gone).
- plan Task 3: *"a resolver that blocks past the timeout ⇒ `ErrIdentityUnavailable`"*, and *"⚠ The timeout case is not decoration."*

**The failure.** `resolveRequestActor` (plan Task 3/4, code given verbatim) calls `resolve(ctx)` **synchronously** after wrapping ctx in `context.WithTimeout`. `context.WithTimeout` cancels a context; it cannot interrupt a function. A resolver that does not select on `ctx.Done()` — `http.Get` on the default client, a `database/sql` call without the `…Context` variant, an LDAP/OIDC client that takes no ctx, a `time.Sleep`, a `sync.Mutex` contention — blocks the **request goroutine** for its full natural duration. The hang state named in Decision 9 is **untouched**.

Worse, and unstated anywhere in the bundle: when the natural duration exceeds the deadline, `resolve` still returns `(actor, nil)`. The `switch` sees `err == nil`, so the request **proceeds with the actor** — after the deadline the design says produces a 503. The 503 arm fires only for resolvers that voluntarily report `ctx.Err()`.

⚠ **This is a hedge stripped off an inherited claim** — CLAUDE.md's named failure. The precedent the bundle cites carries the caveat *in its own godoc*: `runtime/task/service.go:154` — *"**The resolver's Candidates must honour ctx cancellation for the timeout to take effect**; a timed-out resolution returns an error and no trigger."* The bundle restates the precedent ("adopts the same bound") and drops that sentence, converting a conditional bound into an unconditional promise.

**Evidence.** The plan's own `resolveRequestActor` shape, executed (`transport/http/httpcore/zzprobe_timeout_test.go`, deleted after the run):

```
PROBE honours-ctx   elapsed=200ms err=context deadline exceeded
PROBE ignores-ctx   elapsed=1.5s   err=<nil>  (timeout was 200ms)
--- PASS: TestZZProbeResolverTimeoutDoesNotBound (1.70s)
```

⇒ against a 200 ms bound, the ctx-ignoring resolver ran 1.5 s **and returned success**.

⚠ **The plan's prescribed test cannot fail in the direction that matters.** Task 3's "a resolver that blocks past the timeout ⇒ `ErrIdentityUnavailable`" is only satisfiable by writing the *honours-ctx* resolver (case 1) — the case that already works. The ctx-ignoring resolver, which is the realistic consumer, produces GREEN on a test asserting nothing about it. This is the repo's "a fixture can be as vacuous as an assertion" shape (ADR-0181/0182).

**Concrete fix (three parts, all required):**
1. **State the bound's precondition where the precedent states it**: godoc on `WithRequestActorTimeout` and the ADR must say the resolver MUST honour ctx cancellation, and that the library cannot enforce it. Remove "the hang state is closed" framing; the honest sentence is *"the hang state is closed for resolvers that honour cancellation, and the library cannot close it for those that do not."*
2. **Re-check the deadline after the call.** After `a, err := resolve(ctx)`, if `err == nil && ctx.Err() != nil`, return `ErrIdentityUnavailable` wrapping `ctx.Err()`. Cheap, and it makes the deadline mean something even for a ctx-ignoring resolver's *result*.
3. **Add the missing test**: a resolver that ignores ctx and returns success after the deadline ⇒ `ErrIdentityUnavailable`, not a 200. Today's plan has no such case; it is what makes fix (2) falsifiable.

⚠ Do **not** "fix" this by running the resolver in a goroutine and selecting on ctx.Done() — that leaks a goroutine per timed-out request and is a worse failure mode than the one it replaces. (2) is the correct shape.

### F2 — the marshalability pre-check returns 400 for a CONSUMER-RESOLVER fault, contradicting two documented conventions in the very file it edits — [MAJOR]

**Bundle text attacked:** ADR §Decision 5 / spec §3.5: *"**The pre-check:** the seam rejects an actor whose `Attributes` do not `json.Marshal`, with `ErrBadInput` (400)."* Plan Task 4: *"`Attributes` containing `chan int` ⇒ `ErrBadInput`."*

**The failure.** The attributes are minted by the **consumer's `RequestActorFunc`**, not by the caller. A caller sending a perfectly valid request receives `400 bad_request` and can do nothing whatsoever to make the request succeed — no header, no body, no retry helps. The status code tells them to fix their request; there is nothing to fix. Every request from every caller 400s until an operator redeploys. 400 also excludes the failure from 5xx-based alerting and SLO error budgets, so the outage is invisible on exactly the dashboards that would catch it.

`transport/http/httpcore/errors.go` states the governing rule **twice**, in the file Task 2 edits:

- `errors.go:20-23` — *"⚠ Not to be confused with `action/httpcall.ErrBodyTooLarge` … **a server-side fault the caller cannot correct, which correctly stays a 500**."*
- `errors.go:83-85` — `humantask.ErrInvalidTask` is 422 because *"A contradictory task shape is **engine-authored — editing the request cannot fix it**"*.

A non-marshalable attribute is squarely the first category: server-side, caller cannot correct.

**Secondary leak.** Every 4xx arm in `ClassifyError` echoes `err.Error()` into `ErrorBody.Message` (only 5xx is blanked — the 413 arm is the sole 4xx exception and says so). So the 400 body returns the consumer's internal error text to the caller, e.g. `workflow-httpcore: bad input: actor attributes are not JSON-serialisable: json: unsupported type: chan int` — leaking the resolver's internal Go types. Under a 500 the message is blank by construction.

**Evidence.** `errors.go` read in full (quoted above); the `default:` arm returns `ErrorBody{Error:"internal_error"}` with no Message, and every listed 4xx arm carries `Message: err.Error()`.

**Concrete fix.** Classify the marshalability failure as **500**, not 400 — either by leaving it unwrapped (it falls to `default:`) or with a dedicated `ErrIdentityUnavailable`-style sentinel if a 503 is preferred for retryability. Log it at ERROR with the attribute key names (not values). Update spec §3.5, ADR Decision 5 and plan Task 4's table together. If the owner insists on a 4xx, 422 is defensible under the `ErrInvalidTask` precedent — 400 is not.

### F3 — the admin role gate has two silent fail-opens, one of which LOOKS enabled, and the bundle prescribes no test for either — [CRITICAL]

**Bundle text attacked:** ADR §Decision 7 / spec §3.7: *"Declared, `AdminRoutes` returns **403** unless the actor holds one of the named roles. Undeclared, it inherits Decision 6's 401 and nothing more."* plan Task 8 Step 1: *"`AuthorizeAdmin` with **no roles declared** ⇒ **nil** (opt-in!)"*, and the §5 test table rows 13–14, which cover only "wrong role ⇒ 403 / right role ⇒ 200 / 401 before 403" and "without `WithAdminRoles` ⇒ 200".

**The failure — two distinct paths, both realistic, both undetected by every prescribed test.**

**(a) `WithAdminRoles()` with zero arguments silently disables the gate.** The option is variadic, so the natural consumer idiom is `stdlib.WithAdminRoles(cfg.AdminRoles...)`. When the operator's config is empty — unset env var, absent YAML key, a typo'd key name — the slice is empty, `len(AdminRoles) == 0` is true, and the gate the consumer explicitly asked for evaluates to **allow**. The consumer's code reads as though the gate is on. Nothing warns, nothing logs, no test covers it.

**(b) The `strings.Split` gotcha makes the gate look ENABLED while admitting everyone.** `strings.Split("", ",")` returns `[]string{""}` — length **1**, not 0. An operator whose `WRKFLW_ADMIN_ROLES` is unset and who writes `WithAdminRoles(strings.Split(os.Getenv("WRKFLW_ADMIN_ROLES"), ",")...)` declares a gate containing one empty-string role, so `len(AdminRoles) > 0` and the gate is live. A caller whose middleware builds `Roles: strings.Split(r.Header.Get("X-Roles"), ",")` — the same idiom, the same gotcha — presents `[""]` when the header is absent. The two empty strings match, and **an anonymous-but-resolved caller clears the admin gate**. The gate reports as configured on both ends and admits everyone.

**Evidence.** The plan's rule implemented verbatim and executed (`transport/http/httpcore/zzprobe_admin_test.go`, deleted after the run):

```
PROBE zero-args     WithAdminRoles([]string(nil)...) vs actor{} -> ALLOW (gate not declared)
PROBE split-empty   strings.Split("", ",") = []string{""}  (len=1)
PROBE split-empty   declared=[]string{""} actor.Roles=[]string{""} -> ALLOW (role held)
PROBE empty-role    declared=[""] actor.Roles=nil -> DENY 403
PROBE case          declared=["platform-admin"] actor=["Platform-Admin"] -> DENY 403
PROBE whitespace    declared=["platform-admin"] actor=["viewer"," platform-admin"] -> DENY 403
```

The last two rows are the milder half: case differences and a space after a comma fail **closed**, which is safe but produces an unexplainable 403 with no diagnostic — a support burden the bundle also never mentions.

⚠ **Interaction with F5 below:** path (b) composes with the removal of the empty-`Actor.ID` rule. `Actor{ID: "", Roles: [""]}` — a wholly anonymous principal — clears Decision 6's 401 *and* Decision 7's 403, and Decision 7's own residual (ADR Negative bullet: *"An empty-ID actor passes every gate, including the admin role gate if it carries the role"*) describes exactly this without noticing that the role it needs is one an absent header manufactures for free.

**Concrete fix:**
1. **Reject empty-string roles at the option**, not at the check: `WithAdminRoles` drops `""` entries, and if that leaves the slice empty **after the consumer passed at least one argument**, panic or return a configuration error rather than silently disabling. The distinction the config must carry is three-state — *never declared* / *declared empty* / *declared* — not `len() == 0`.
2. **Refuse an empty-string role on the actor side too**, in `AuthorizeAdmin`: skip `""` when building the membership set, so a `[""]`-roled actor can never match anything.
3. **Add both cases to plan Task 8 Step 1 and spec §5** as their own rows: `WithAdminRoles()` with zero args, and `WithAdminRoles("")` vs `Actor{Roles: [""]}`. What makes them fail today: neither symbol exists; after implementation, the naive `len()==0` + raw membership form makes both ALLOW.
4. Document case-sensitivity and no-trimming on the option's godoc, since both fail closed and will otherwise be diagnosed as a library bug.

### F4 — the sole justification for making the admin gate opt-in is refuted by Decision 6 in the same bundle, and it generalises one adapter's godoc to three — [MAJOR]

**Bundle text attacked:** ADR §Decision 7: *"⚠ **Opt-in, not fail-closed, and the reason is a fact about the existing API:** `stdlib.Mount` excludes `AdminRoutes` and directs consumers to mount them on *'a separate, access-controlled mux'*. **A fail-closed default would return 403 to the consumer who followed that advice and already secured the surface.**"* Author grid ⚠5: *"**No existing correct wiring breaks**, and the admin surface is still strictly better than today."*

**The failure — three separate defects in one argument.**

**(a) Decision 6 breaks that exact consumer anyway, harder.** The protected consumer being protected here mounts `AdminRoutes` behind an nginx auth\_request, mTLS, a VPN ACL or an SSO proxy — protection that does **not** call `authz.ContextWithActor`. Decision 6 makes `AdminRoutes` return **401 when the resolver reports no actor**, and the default resolver is `authz.ActorFromContext`, which reports `ok == false` for that consumer. ⇒ their entire admin API returns 401 on every route. The bundle spends Decision 7 avoiding a 403 for a consumer whom Decision 6 already 401s. The grid's *"No existing correct wiring breaks"* is false, and *"the admin surface is still strictly better than today"* is false for that consumer — it is strictly dead.

The ADR's own Negative section contradicts the grid (*"every mounted route group except Health starts answering 401 without authentication"*), so the bundle already knows the fact that voids ⚠5's reasoning; the reasoning was simply never re-derived after C was fixed. This is precisely the pairwise-interaction failure rule #9's corollary exists to catch, occurring **inside the document written to satisfy that corollary**.

**(b) The quoted godoc is truncated in a way that changes what it says.** `transport/http/stdlib/mount.go:14-16` reads *"**Admin and health routes** are intentionally excluded so consumers can choose whether and where to mount them — typically on a separate, access-controlled mux."* The ADR renders it as *"its godoc says **admin routes** are 'intentionally excluded…'"*. The same sentence governs health, which this bundle exempts from authentication entirely on a completely different rationale (a load balancer has no credential). One sentence is being used to support two incompatible treatments while being quoted as though it addressed only one.

**(c) It generalises one adapter's godoc to an option exported in all three.** Executed — the phrase exists in `stdlib` only:
- `stdlib/mount.go:14` — *"Admin and health routes are intentionally excluded … typically on a separate, access-controlled mux."*
- `gin/mount.go:13` — *"For admin and health endpoints call AdminRoutes.Customize and MountHealth separately."* — no security advice at all.
- `fiber/mount.go:13-14` — *"For more control — e.g. admin routes, a different base path per group, or group-level middleware — call each RouteGroup's Customize method directly."* — no security advice at all.

`WithAdminRoles` is specified for all three adapters (ADR Decision 7, plan Task 7). For two of the three, the "consumers were told to secure it themselves" premise is simply absent from the documentation those consumers read.

**Evidence.** `sed`/`grep` over `transport/http/{stdlib,gin,fiber}/mount.go`, quoted verbatim above; `httpcore/seam.go:208` confirms `MountGroups` passes no options so a group mounted through it resolves the default `ResolveConfig()`.

**Concrete fix:** re-derive Decision 7's opt-in/fail-closed choice against Decision 6 as it now stands, and write the derivation down. Since C already breaks the "already secured" consumer, the "don't break them" argument no longer distinguishes the options, and the choice must be made on its merits — my read is that fail-closed-by-default is now the better default *because* that consumer is already forced to touch the wiring. If opt-in survives anyway, state the real reason (fewer moving parts, no synthesized policy) rather than the refuted one. Correct the truncated quote, and either add the access-controlled-mux advice to gin's and fiber's `Mount` godocs in this bundle or stop citing it as a cross-adapter fact.

### F5 — an ACCEPTED round-1 finding (unbounded attribute size reaching durable storage) was dropped without adjudication, and it is not among the nine residuals — [MAJOR]

**Bundle text attacked:** ADR §Decision 5 and spec §3.5 present exactly one mitigation — *"the seam rejects an actor whose `Attributes` do not `json.Marshal`, with `ErrBadInput` (400)"* — and spec §4's nine residuals list none about size.

**The failure.** The round-1 adjudication accepted A4 as a **five-finding cluster** and enumerated its legs, one of which is: *"**Unbounded durable writes (fm F4, ex F6).** The actor is validated on exactly one property (`ID != ""`). **ADR-0186's body cap no longer bounds this path** — the attributes arrive from the consumer's resolver, not the body — and they land in `wrkflw_human_task` and `wrkflw_instances.snapshot`."* The owner then chose D-1 = keep the flow, which makes **mitigating the cluster in-bundle** the stated obligation (*"must all be mitigated in-bundle"*, adjudication D-1 table). The revision mitigates the view-poisoning leg (pre-check) and argues the exposure leg away via Decision 6 and the provenance probe — and says **nothing at all** about size.

Executed over the whole revised bundle:

```
$ grep -rniE "size (bound|cap|limit)|maxattr|attribute.*(bound|cap|limit)|unbounded" \
    docs/specs/2026-08-25-request-actor-identity.md docs/adr/0189-*.md docs/plans/2026-08-25-request-actor-identity.md
docs/plans/2026-08-25-request-actor-identity.md:303:      honours the size cap.
```

The single hit is Task 6's *request-body* cap for the optional-decode helper — a different subject. There is no size bound on actor attributes anywhere in the bundle, and no residual admitting its absence.

CLAUDE.md's Delivery Gate rule is explicit: *"Findings you adjudicate as false-positive or out-of-scope must be stated explicitly with the reason — silence is not an adjudication."* This leg was accepted, not adjudicated away, and then vanished.

**Concrete inputs it leaves open.** A consumer resolver enriching the actor from a directory (group memberships, entitlement lists, a cached profile blob) can attach megabytes. Every claim/complete/reassign then (a) `json.Marshal`s it in the pre-check on the request path, (b) writes it into `wrkflw_human_task.claim_actor`, and (c) writes it again into `wrkflw_instances.snapshot`, which grows monotonically with instance history. Nothing anywhere bounds it. Note the pre-check makes this *worse* on the hot path than it was: it adds a full marshal of an unbounded structure per request, purely to discard the result.

**Concrete fix.** Either (i) add an explicit bound at the seam — reuse the pre-check's marshal (`len(b) > cfg.MaxActorAttributeBytes`, default sized against `MaxBodyBytes`) and classify the overflow as a **server-side** fault per F2, or (ii) record it as residual #10 in spec §4 with the reason it is out of scope and file it as a backlog item **naming the two columns**. (i) is nearly free because the marshal already happens. Silence is not an option.

### F6 — the 503 path logs the consumer's resolver error at ERROR, creating a credential-logging channel the bundle never warns about; and the `ErrUnauthenticated` pass-through contract exists only in the plan — [MAJOR]

**Bundle text attacked:** ADR §Decision 3's matrix — *"the resolver returns an error | **503** `ErrIdentityUnavailable`, wrapping it"* — and spec §3.3's identical four-row table. Neither table has a row for a resolver that reports `ErrUnauthenticated`; only plan Task 4's code does (`case errors.Is(err, ErrUnauthenticated): return authz.Actor{}, err`).

**The failure — two legs.**

**(a) Credential material reaches the log.** `stdlib/write.go:30-35`:
```go
status, body := httpcore.ClassifyError(err)
if status >= 500 {
    cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err)
}
```
`ErrIdentityUnavailable` wraps the consumer's error with `%w`, so `err.Error()` contains the resolver's full text. A resolver author naturally writes `fmt.Errorf("invalid bearer token %q: %w", tok, err)` or lets a JWT library echo the token in its parse error. That string is then written at **ERROR** on the request path, labelled *"rest: internal error"*. The bundle introduces this channel and warns nobody: there is no godoc requirement on `RequestActorFunc` that its error must not carry credential material, and no `SECURITY.md` line (Task 17 lists the middleware pattern and the 401/503 contract, not this).

**(b) The pass-through contract is invisible where consumers read it.** A consumer implementing `RequestActorFunc` reads the ADR or the godoc, not the plan. Both decision tables say *any* error ⇒ 503. So the natural implementation returns a plain error for a **bad credential**, and every rejected credential becomes a 503 plus an ERROR log line — attacker-paced log amplification, mislabelled as an internal error, with an availability status code that will page an on-call. Round 1 filed this as fm F5, the adjudication accepted it as B9 (*"an invalid credential takes the 503 + ERROR-log path, not 401"*), and the revision put the mechanism in the plan's code while leaving both decision tables unchanged — so the fix is unreachable by the person who needs it.

**Evidence.** `transport/http/stdlib/write.go:30-35` read verbatim (above); `httpcore/errors.go` confirms every 5xx blanks the response body but nothing blanks the log; ADR Decision 3 and spec §3.3 tables read in full — four rows each, no `ErrUnauthenticated` row.

**Concrete fix.**
1. Add the fifth row to **both** decision tables: *"the resolver returns an error wrapping `ErrUnauthenticated` | **401**, not 503 — a rejected credential is not an outage."*
2. Put it on `RequestActorFunc`'s godoc, with the sentence *"Return an error wrapping `httpcore.ErrUnauthenticated` for a credential that was presented and rejected; reserve other errors for an identity provider that is unreachable."*
3. Add a second godoc sentence: *"The returned error is logged at ERROR when it classifies 5xx. Do not include the presented credential, or any part of it, in the error text."*
4. Add a `SECURITY.md` line under Task 17 for (3), and a plan Task 4 test case asserting a bad-credential resolver yields 401 and no ERROR log.

### F7 — the empty-ID rule was removed WHOLESALE where ADR-0148 blesses only the roles-carrying shape; the fully-zero `Actor{}` is not the kiosk claimant, and admitting it converts a common middleware bug from fail-closed to fail-open — [CRITICAL]

**Bundle text attacked:** ADR §Decision 3 — *"⚠⚠ **Round 1 refused an empty `Actor.ID`; this record does not.** `humantask/validate.go:24` and ADR-0183:69-76 call the empty-claimant ('kiosk') shape **'deliberately legal'** … A transport-level refusal would have deleted that shape over HTTP."* Author grid ⚠3 — *"A consumer's buggy middleware that authenticates and then stores a **zero actor** now satisfies C's refusal on **every** route group … **Resolved by wording, deliberately.**"* spec §4 residual 3.

**The failure.** The rule that was removed and the shape that was protected are **not the same shape**, and the bundle never separates them.

The blessed shape is `Actor{ID: "", Roles: [...]}` — *"the kiosk claimant, **with roles and no ID**"* (ADR-0183:72-73) and *"anonymous but **carrying roles**"* (`humantask/validate.go:25`). Its own regression fixture carries a role: `humantask/validate_test.go:47` — `Claim: &humantask.Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}, At: at}`.

The shape newly admitted is `Actor{}` — no ID, **no roles**, no attributes. That is not the kiosk claimant; it is the zero value of the type, and it is what a consumer's middleware produces from the single most common Go mistake at an authentication seam:

```go
actor, _ := authenticate(r)                      // error dropped, or a not-found path
ctx = authz.ContextWithActor(r.Context(), actor) // stores authz.Actor{}
```

`ContextWithActor` stores it, `ActorFromContext` reports `ok == true`, and **every route group in Decision 6 passes**, plus Decision 7's admin gate if the same bug also yields a `[""]` role (see F3(b)). A design whose entire purpose is that the transport must not believe a principal it did not establish now believes the zero value.

Refusing `Actor{}` — empty ID **and** no roles **and** no attributes — would not have deleted the kiosk shape at all. The bundle collapsed "refuse an empty ID" and "refuse the zero Actor" into one rule and then rejected both because the first was wrong. This is CLAUDE.md's ADR-0188 lesson verbatim: *a true principle generalised to a case that does not exhibit it.*

⚠ **"Resolved by wording" is not a resolution.** The grid's own text identifies the fail-open, and the resolution recorded is that the ADR must not *say* the stronger sentence. The hazard is unchanged; only the prose about it changed. Per CLAUDE.md, *a residual you wrote down is still a defect you shipped* — and this one is the delivery's central promise.

**Evidence.** Executed (`humantask/zzprobe_zeroactor_test.go`, deleted after the run):

```
PROBE validate kiosk{Roles:[kiosk]} -> <nil>
PROBE validate zero {}             -> <nil>
PROBE AssignedTo("")  -> n=0 err=<nil>   (both tasks ARE claimed)
PROBE stored claim    -> State=claimed Claim.Actor=authz.Actor{ID:"", Roles:[]string(nil), Attributes:map[string]interface {}(nil)}
```

Two further consequences the bundle does not state:

- **The claim is durably unattributable AND unqueryable.** `AssignedTo("")` returns 0 rows for a task that IS claimed — deliberately, per `humantask/memory.go:61-68`: *"An empty actorID identifies no actor and always returns an empty result: it is not a wildcard … treating '' as a match would turn **an unauthenticated or unresolved actor ID** into a dump of every task nobody is holding."* The repo's store layer already names *"an unauthenticated or unresolved actor ID"* as the hazard it defends against; this bundle removes the layer that would have stopped such an ID being minted. (Also `humantask/humantask.go:212` and `internal/persistence/store/humantask_store.go:223`, same reasoning.)
- **The zero-actor claim and the kiosk claim are indistinguishable at rest** — both store `Claim.Actor.ID == ""` — so no operator, log or query can separate "kiosk terminal in the warehouse" from "our SSO middleware silently degraded three weeks ago".

**Concrete fix (pick one; (a) is the cheap one and preserves both ADRs).**
(a) **Refuse only the fully-zero actor** at the seam: `if a.ID == "" && len(a.Roles) == 0 && len(a.Attributes) == 0 { return ErrUnauthenticated }`. The kiosk shape (`{ID:"", Roles:["kiosk"]}`) passes untouched, so neither ADR-0148 nor ADR-0183 is contradicted, and the middleware-bug class fails closed again. Add the two paired tests: `Actor{}` ⇒ 401; `Actor{Roles:["kiosk"]}` ⇒ pass. What makes them fail today: neither symbol exists; with the plan's current body, both pass.
(b) Keep Decision 3 as written but make the degraded case **observable**: log at WARN once per resolution and emit a counter when a resolved actor has an empty ID, so a silently-degraded middleware is visible. Weaker — it detects rather than prevents — and it must be stated as a detection, not a fix.

Whichever is chosen, spec §4 residual 3 must stop reading as though the only consequence were the admin gate; it must name the durable-audit and `AssignedTo` consequences above.

### F8 — five live documentation sites become false, three of them godocs on the type being changed, and Task 17's sweep is scoped so it cannot find any of them — [MAJOR]

**Bundle text attacked:** plan Task 17 — *"`grep -rn '"actor"' README.md docs/ examples/` — fix anything documenting the removed field"* — and the ADR's Negative section, which enumerates **"BREAKING, in four ways"** and lists no documentation obligation beyond CHANGELOG + STABILITY.md + ADR-0147.

**The failure.** After Decision 6, these five statements are false, and nothing in the plan touches any of them:

| site | text that becomes false |
|---|---|
| `SECURITY.md:37-39` | *"Admin endpoints … **carry no built-in authentication**."* |
| `transport/http/stdlib/groups.go:181-183` | *"SECURITY: these routes have **NO built-in authentication** … otherwise the admin endpoints are exposed unauthenticated."* |
| `transport/http/gin/groups.go:229-231` | identical text |
| `transport/http/fiber/groups.go:227-229` | identical text |
| `docs/observability.md:240` | *"…they carry no built-in authentication — mount…"* |

Task 17's sweep cannot reach them for two independent reasons: it greps for the literal string `"actor"`, which appears in none of them, and its path list is `README.md docs/ examples/` — which excludes `transport/` and `SECURITY.md` entirely. Run as written, the sweep returns ~20 hits, nearly all inside **historical** specs and plans that must not be edited, and zero of the five live sites.

**And the README's headline HTTP example stops working.** `README.md:273` — `stdlib.Mount(mux, svc)` — is the mount every reader copies; after this bundle it produces an API where every route answers 401. Same for `README.md:288-289` (gin), `:304-305` (fiber) and `:326-330` (the per-group customization block, which also mounts `AdminRoutes`). None is reachable by the sweep, and the ADR's four-way BREAKING enumeration does not mention the README at all.

**Evidence.**
```
$ grep -rn "no built-in auth\|NO built-in auth\|unauthenticated" --include="*.go" --include="*.md" .  | grep -v _test.go
SECURITY.md:39 · transport/http/{stdlib:181,183, gin:229,231, fiber:227,229}/groups.go · docs/observability.md:240 …
$ grep -rn '"actor"' README.md docs/ examples/    # Task 17's sweep, as written
… 20 hits, all in docs/{plans,specs}/ history + docs/specs/assets/process-instance-sample.json …
```

**Concrete fix.** Replace Task 17's sweep with two greps that match the *subject*, not one incidental string, and widen the paths:
```
grep -rn "no built-in auth\|NO built-in auth\|exposed unauthenticated" . --include='*.go' --include='*.md' | grep -v docs/plans/ | grep -v docs/specs/
grep -rn "Mount(mux, svc)\|Mount(r, svc)\|Mount(app, svc)\|AdminRoutes{" README.md docs/*.md examples/
```
Add the five sites and the four README snippets to Task 17 as an explicit checklist, and add "documentation that asserts the absence of authentication" as a fifth item to the ADR's BREAKING enumeration. ⚠ Exclude `docs/plans/` and `docs/specs/` from the sweep deliberately and say why — they are historical records and editing them would be doc-rot in the other direction.

### F9 — the `HealthRoutes` exemption is justified by naming `/healthz` while it actually covers `/readyz`, the one endpoint with a body and a side effect — [MAJOR]

**Bundle text attacked:** ADR §Decision 6 — *"`HealthRoutes` is **exempt and calls no resolver** — **a load balancer probing `/healthz` has no credential**."* spec §3.6 and §5 row 12 (*"`HealthRoutes` ⇒ 200"*) repeat it. That is the bundle's entire analysis of the exemption.

**The failure.** `HealthRoutes` mounts **two** routes, and the justification names the one that does not matter.

- `/healthz` → `httpcore.EvaluateLive` — a static `{"status":"ok"}`, no side effect, discloses nothing. The godoc says so: *"liveness probes MUST NOT run expensive checks."* Exempting it is uncontroversial.
- `/readyz` → `httpcore.EvaluateReady` — **runs every consumer-registered `HealthCheck`** with the request context (`health.go:44-56`, `for _, c := range checks { c.Check(ctx) }`) and returns a body naming each check and its state.

After this bundle, `/readyz` is the **only** unauthenticated route in the entire transport, which concentrates attention on it rather than diffusing it. It gives an anonymous caller two things:

1. **An infrastructure oracle.** Check names are consumer-chosen and conventionally name components — the repo's own canonical example is *"A `pool.Ping`-style probe"* — so the body enumerates the deployment's dependencies and reports each one up or down, plus a 503 when any is down.
2. **A dependency-round-trip amplifier.** One anonymous HTTP request drives one round-trip per registered check, at whatever rate the attacker chooses, against the database the rest of the engine depends on. ADR-0186's body cap does not bound it (there is no body) and no rate limit exists in this repo.

**Evidence.** Executed (`transport/http/stdlib/zzprobe_readyz_test.go`, deleted after the run), two checks registered, both hit anonymously:

```
PROBE anon GET /healthz  status=200 body={"status":"ok"}
PROBE anon GET /readyz   status=503 body={"checks":{"kafka-eu-west-1":"unavailable","postgres-primary":"ok"},"status":"unavailable"}
PROBE dependency round-trips driven by 2 anonymous requests = 2
```

⚠ To be exact about the leg of my brief that asks whether the exempt route leaks what the authenticated ones now protect: **it does not.** No instance, task, claim or actor data passes through `HealthRoutes`. The finding is that the exemption is granted on an unexamined premise, not that it leaks process data.

**Concrete fix.** Not "authenticate `/readyz`" — that breaks orchestrator probes, which is the whole point of the exemption. Instead:
1. **Correct the ADR's justification to name both routes** and state what `/readyz` discloses and costs, so the exemption is a decision rather than an oversight. One sentence: *"`/healthz` is static; `/readyz` runs the consumer's checks and names them, and is deliberately left anonymous so an orchestrator can probe it — the consumer is responsible for scoping it to their probe network."*
2. **Add it to `SECURITY.md`'s "Scope notes for embedders"** (Task 17 already opens that section) — *"`/readyz` is the only route with no identity requirement; bind it to your probe interface or a separate listener, and keep check names free of host, DSN or region detail."*
3. Add spec §5 row 12a: `/readyz` with a failing check answers 503 **without** a resolver being invoked, pinning the exemption as intentional in both directions.

### F10 — "The unauthenticated read surface closes" is the forbidden overreach in the ADR's own Positive section, and residuals 4 and 6 describe a READ gap where the exposed surface is state-CHANGING — [MAJOR]

**Bundle text attacked:**
- ADR §Consequences/Positive: *"**The unauthenticated read surface closes.** `GET /instances/{id}/actionable` and `/snapshot` render `Claim.Actor` and `Candidates` verbatim … and Decision 6 **authenticates** them."*
- spec §4 residual 4: *"**`InstanceRoutes`/`MessageRoutes` resolve and discard.** Authentication without authorization."* residual 6: *"**Per-instance read authorization is absent.** §3.6 turns *'anyone can read any instance'* into *'any authenticated caller can read any instance'*."*
- Against: ADR §Decision 3 — *"⇒ **Decision 6's promise is therefore 'the request carries a resolved actor', NOT 'the request carries an identified principal.'** … **this record may not state the stronger sentence anywhere**."* plan Global Constraints: *"⛔ **Do not write, anywhere, that every route now has an 'identified principal'.**"*

**The failure — two legs.**

**(a) The bundle breaks its own prohibition, in bold, in the section a reader quotes.** *"The unauthenticated read surface closes"* and *"Decision 6 authenticates them"* assert that these routes are no longer unauthenticated. Per Decision 3 + F7 above, what they actually require is that *some* `authz.Actor` value was placed in the context — including `authz.Actor{}`, which is what a degraded middleware supplies. A surface reachable by the zero value has not stopped being unauthenticated in any sense a security reader gives the word. The bundle's ⛔ was written against the phrase *"identified principal"*; the overreach arrived in a synonym, which is exactly the mechanism CLAUDE.md's recap rule describes (*the false claim is the summary sentence appended to correct reasoning*). The following ⚠ clause hedges *scope* ("any authenticated caller can read any instance") but not *strength* — it inherits the word "authenticated" rather than correcting it.

**(b) Residuals 4 and 6 are framed as a READ gap; the actual gap includes three WRITE endpoints.** Source-verified, `InstanceRoutes` and `MessageRoutes` mount five routes, three of which change state:

| route | handler | effect |
|---|---|---|
| `POST /instances` | `httpcore.StartInstance` | **starts a process instance** |
| `POST /instances/{id}/signals` | `httpcore.DeliverSignal` | **advances any instance** |
| `POST /messages` | `httpcore.DeliverMessage` | **advances or starts an instance** (ADR-0121 message-start) |
| `GET /instances/{id}`, `/snapshot`, `/actionable` | read | read |

and none of `StartInstanceRequest`, `DeliverSignalRequest`, `DeliverMessageRequest` carries an `Actor` field (`service/request.go:14-45`), so no authorization occurs anywhere on those paths — confirmed: `grep "authz\." service/*.go` shows the only actor-bearing requests are the four task verbs. ⇒ after this bundle, **any caller holding any valid credential can start instances and drive any other tenant's instance to completion**, and can do so with an `Actor{}`. Residual 6's sentence — *"Per-instance read authorization is absent"* — names none of that; residual 4's five words *"Authentication without authorization"* are literally true and materially misleading about which verbs are involved.

**Evidence.** `transport/http/stdlib/groups.go:33-118` read in full (route table above); `service/request.go:14-45` read in full (no `Actor` field on the three request types); `grep -rn "authz\." service/*.go | grep -v _test` returns only the view type, the four `Actor`/`By` request fields, the `AllowAll` default and the option plumbing.

**Concrete fix.**
1. Rewrite the Positive bullet without the word "authenticated": *"Anonymous access to the instance and message routes ends: a request that carries no resolved actor is refused. This is a credential requirement, not an authorization decision, and Decision 3 means a resolved-but-empty actor satisfies it."*
2. Rewrite residuals 4 and 6 to name the three state-changing routes explicitly and to say **cross-tenant write**, not "read". Merge them — they are one residual about the same gap — and file the backlog item naming `StartInstance`, `DeliverSignal` and `DeliverMessage`, not just backlog 62's read-ownership framing.
3. Add the phrase-level prohibition to plan Task 17's premise sweep: it currently bans *"identified principal"* only. Ban the claim, not the wording — *"no sentence may assert that a route is authenticated, authorized, or protected; the only true predicate is 'refuses a request with no resolved actor'."*

### F11 — the repo's OWN reference wiring is the consumer Decision 7 claims not to break, and Decision 6 breaks it; the plan neither fixes it nor can detect it — [CRITICAL]

**Bundle text attacked:**
- ADR §Decision 7 / spec §3.7: *"A fail-closed default would return 403 to **the consumer who followed that advice and already secured the surface**."*
- Author grid ⚠5: *"**No existing correct wiring breaks**, and the admin surface is still strictly better than today."*
- plan Task 16 Step 2: *"the three wiring mains … **must not mount `AdminRoutes`**."*
- plan Task 16 Step 3: *"**run each main and confirm a clean start**."*

**The failure.** `examples/production_wiring/main.go` **already is** that consumer, in-tree, today:

```go
// examples/production_wiring/main.go:268-276
// AdminRoutes has NO built-in authentication (ADR-0095: admin-by-composition).
// It is mounted on its own mux so the whole /admin/ subtree can be wrapped in one guard…
adminMux := http.NewServeMux()
stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
mux.Handle("/admin/", requireAdminToken(adminMux, os.Getenv("ADMIN_TOKEN"), logger))
```

and `requireAdminToken` (`:75-88`) does a constant-time compare of `X-Admin-Token` and then calls `next.ServeHTTP(w, r)` — it **never calls `authz.ContextWithActor`**. Under Decision 6 the default resolver (`authz.ActorFromContext`) reports `ok == false`, so every admin route behind that correctly-presented token answers **401**. The example's admin API is dead.

That single file falsifies three separate claims:

1. Decision 7's premise — the consumer who followed the advice **is** broken, by C, not by D, so "avoiding a 403 for them" cannot justify D's opt-in default (compounding F4).
2. The grid's *"No existing correct wiring breaks"* — one does, and it is the repo's own flagship example.
3. plan Task 16 Step 2's premise that the three mains do not mount `AdminRoutes` — `production_wiring` does. Taken literally, Task 16 requires **deleting** that mount, which would remove the repo's only worked example of the separate-access-controlled-mux pattern that Decision 7 cites as its foundation. The plan never says so and no reviewer reading Task 16 would realise it.

⚠ **No prescribed step can detect this.** Task 16 Step 3 verifies *"a clean start"* — the break is at request time, not startup, so the main starts fine and the step passes. Tasks 9–11 pin `AdminRoutes` ⇒ 401 unauthenticated, which is the *intended* library behaviour, and nothing connects that pin to the example that relies on the opposite. Task 16 Step 3 does prescribe a `curl` — but only against `authenticated_tasks`, never against `production_wiring`'s admin mux.

**Evidence.** `examples/production_wiring/main.go:75-88` and `:262-276` read in full and quoted above; `httpcore/seam.go:208` (`MountGroups` passes no options) and `ResolveConfig`'s default confirm the resolver a bare `Customize(adminMux, …)` gets.

**Concrete fix (all three).**
1. **Fix the example, do not delete the mount.** `requireAdminToken` should, on a successful compare, call `r = r.WithContext(authz.ContextWithActor(r.Context(), authz.Actor{ID: "admin-token", Roles: []string{"platform-admin"}}))` before `next.ServeHTTP`. That keeps the pattern, demonstrates the seam at the exact place a real consumer meets it, and pairs naturally with `stdlib.WithAdminRoles("platform-admin")` on the `Customize` call — turning the example into the delivery's best documentation.
2. **Rewrite plan Task 16 Step 2** to say *"`production_wiring` already mounts `AdminRoutes` at :274 and its guard must be updated as above; `sqlite_wiring` and `mysql_wiring` mount none and must not add one."*
3. **Add a verification step**: `curl -H "X-Admin-Token: …" localhost:8080/admin/...` against a running `production_wiring`, expecting 200 — the only step that would have caught this. Record the output, replacing spec §2.5's still-open `ASSUMPTION (unverified)`.
4. Re-derive Decision 7's default with F4, now that the "already secured" consumer is known to be broken either way.

### F12 — the two resolution placements make 401-vs-400 ordering differ BETWEEN route groups of the same mount, and the parity test checks the wrong axis — [MAJOR]

**Bundle text attacked:** ADR §Decision 6 — *"That asymmetry is deliberate"*; spec §4 residual 5 — *"**Two resolution placements** in one transport"*; spec §6 item 2 — *"§3.6's two resolution placements. **Derive what breaks when a future reader unifies them.**"*; plan Task 15 — *"add a parity case asserting **all three adapters** answer an unauthenticated claim identically."*

**The failure.** The bundle derives what breaks if the placements are *unified*, and never derives what breaks because they are *split*. Concretely, from one mount, with one anonymous client:

| request | placement | answer |
|---|---|---|
| `POST /instances` with malformed JSON, no credential | handler, pre-decode | **401** |
| `POST /messages` with malformed JSON, no credential | handler, pre-decode | **401** |
| `POST /tasks/{t}/claim` with malformed JSON, no credential | endpoint, post-decode | **400** |
| any of the above oversize, no credential | pre-decode vs post-cap | **401** vs **413** |

A client library that implements the standard "on 401, refresh the credential and retry" rule gets it right on three routes and wrong on three others — a request with an *expired token* and a slightly malformed body returns 400, so the client never refreshes and the user sees a permanent "bad request" for what is an expired session. The reverse case is worse for an operator: an unauthenticated scanner produces a 400/413 mix in the access log on task routes and clean 401s elsewhere, so "unauthenticated traffic" is not a countable signal.

Residual 5 records the *existence* of two placements; it does not record that they are **client-observable** and produce contradictory status codes for the same class of request on the same mount. Residual 5 as written reads like an internal tidiness concern.

⚠ And the parity test prescribed in Task 15 pins the wrong axis: it asserts the three **adapters** agree, which they will (all three share the same split). Nothing asserts the route **groups** agree, which is where the divergence is. That is a test that cannot fail on the defect it is nearest to.

**Evidence.** Placement is stated in ADR Decision 6 (*"For `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` the refusal runs in the route handler, BEFORE the body is decoded. For the three task routes it runs inside the endpoint, after decode"*); the current decode-first order is source-verified at `transport/http/stdlib/groups.go:36,105,133` (`decodeRequestBody(cfg, w, req, &in)` precedes every `httpcore.*` call), and `errors.go`'s 413/400 arms give the status codes.

**Concrete fix.** Either (a) accept the split and **document the ordering table above** — in `SECURITY.md`, in the `RequestActorFunc` godoc, and in the CHANGELOG entry, so a client author can implement retry correctly; or (b) resolve once per request in each adapter's shared `handle` wrapper and pass the result down (adjudication D-4 option (c)), which removes the divergence and the double-resolve objection together. Whichever is chosen, **add a parity case across route GROUPS** — one unauthenticated malformed-body request per group, asserting the documented matrix — because that is the assertion that can fail.

### F13 — Decision 6 puts consumer-owned, possibly-blocking I/O on the transport's read hot path, in a bundle that elsewhere rejects a design for costing one extra resolver call — [MAJOR]

**Bundle text attacked:** ADR §Decision 6 — *"⛔ **Rejected:** adding a pre-decode check to the task routes as well — it would invoke the consumer's **possibly-I/O-performing** resolver **twice per request**."* And the same decision, two paragraphs earlier: *"`InstanceRoutes` … return **401** when the resolver reports no actor."*

**The failure.** The bundle establishes that one extra resolver invocation per task request is expensive enough to reject a security improvement — and in the same decision adds a **first** resolver invocation to `GET /instances/{id}`, `GET /instances/{id}/snapshot` and `GET /instances/{id}/actionable`, which are pure reads today and are the routes a polling UI hits most. There is no memoisation, no per-request cache, and no measurement anywhere in the bundle.

Consequences, none stated:
- **Read latency now includes the consumer's resolver.** A resolver doing token introspection (an HTTP round-trip to an IdP) adds that round-trip to every instance poll. CLAUDE.md's architecture section is explicit that hot read paths must be cached; this adds an uncached dependency to three of them.
- **F1 compounds directly.** The 10 s bound does not bind a ctx-ignoring resolver, and `net/http`'s `ReadTimeout` bounds reading, not the handler — `examples/production_wiring/main.go:298` sets `ReadTimeout: 30 * time.Second` and it will not release a goroutine stuck in a resolver. A slow IdP therefore parks one goroutine and one connection per in-flight read, unbounded, on routes that previously could not block on anything but the database.
- **The rejected alternative is cheaper than what shipped.** Adjudication D-4 option (c) — resolve once in each adapter's shared `handle` wrapper — is exactly one resolution per request for *every* route, which is fewer than what Decision 6 now performs on a mount serving both groups, and it removes F12's ordering split as a side effect.

**Evidence.** Route inventory source-verified at `transport/http/stdlib/groups.go:33-92` (five `InstanceRoutes` routes, three of them `GET`); `httpcore/seam.go:98-125` shows `ResolveConfig` has no cache and is called once at mount, so nothing memoises a per-request resolution; the F1 probe above shows the timeout does not release a blocked resolver.

**Concrete fix.** Add a Consequences bullet quantifying it honestly — *"three read routes gain one consumer-resolver invocation per request; the library does not cache it and cannot bound a resolver that ignores cancellation"* — put the same warning on `RequestActorFunc`'s godoc (*"called on every request to every non-health route, including reads; keep it cheap and make it honour ctx"*), and re-open D-4 option (c) with this cost on the table, since it now looks cheaper than the shipped shape rather than more expensive.

### F14 — a hand-built `CustomizeConfig` fails CLOSED on the resolver and fails OPEN on the resolver timeout, and the plan's own field-placement note stops one step short of noticing — [MINOR]

**Bundle text attacked:** plan Task 3 Step 3 — *"⚠ The `RequestActor` default belongs in `ResolveConfig`'s **post-loop nil-guard block** …; `RequestActorTimeout` belongs in the **struct literal** … — the same rule `BodyReadTimeout` follows."* ADR §Decision 3 — *"`RequestActorFunc` is nil (a hand-built `CustomizeConfig`) | **401** — forgetting the seam fails CLOSED."*

**The failure.** The two fields' placement is correct for its stated purpose (nil-distinguishability) and produces **opposite** safety properties on the path the ADR explicitly designs for. `ResolveConfig` seeds literal defaults and then nil-guards (`seam.go:99-124`), so a config constructed by hand — never passing through `ResolveConfig` — carries the **zero** value of every field:

| hand-built `CustomizeConfig{RequestActor: myResolver}` | value | effect |
|---|---|---|
| `RequestActor` | set by the consumer | resolves |
| `RequestActorTimeout` | **0** | *"non-positive disables"* ⇒ **no bound at all** |

So the consumer who hand-builds a config *and* wires the seam — the diligent one — gets an unbounded resolver, silently, while the one who forgets the seam gets the documented 401. The ADR names the hand-built config only to praise its fail-closed behaviour on one axis and never checks the other.

**Evidence.** `transport/http/httpcore/seam.go:98-125` read in full: `MaxBodyBytes`/`BodyReadTimeout` are seeded in the literal, `Wrap`/`InstanceMapper`/`Logger` in the post-loop guard, and nothing re-seeds a hand-built struct. Plan Task 4's body gates on `if timeout > 0`.

**Concrete fix.** Document it on `CustomizeConfig`'s godoc — *"a hand-built config is not defaulted; construct via `ResolveConfig` or set `RequestActorTimeout` explicitly"* — and add a plan Task 3 test case: `CustomizeConfig{RequestActor: blockingFn}` used directly ⇒ no bound applied, pinning it as the known contract rather than a surprise. What makes it fail today: the field does not exist.

### F15 — the admin gate re-creates backlog 53's "empty means allow" semantics at a new site and adds a third hand-rolled role-membership test, in the delivery that lists backlog 53 as still open — [MINOR]

**Bundle text attacked:** ADR §Decision 7 (*"Undeclared, it inherits Decision 6's 401 and nothing more"*), the ADR's Backlog line (*"Explicitly still open: 52, 53…"*) and spec §4 residual 2 (*"§3.7's 'let the `Authorizer` decide' escape hatch is correspondingly weak while the default authorizer is `AllowAll`"*).

**The failure — two small ones with the same root.**

**(a) Same defect, new site.** Backlog 53 is *"an empty `AuthzSpec` means allow-all"* — source-verified at `authz/authz.go:126`: `if len(spec.Roles) > 0 && !hasAnyRole(...)`. Decision 7's gate is `len(cfg.AdminRoles) == 0 ⇒ allow`, which is the identical rule at a new site. The bundle lists 53 as *explicitly still open* and does not notice it is instantiating it again. That matters because F3(a)/(b) are the exploitable form of exactly this semantic.

**(b) A third copy of role membership.** The repo already has two hand-rolled implementations — `authz.hasAnyRole` (`authz/authz.go:149-159`) and `humantask.hasRoleOverlap` + `roleSet` (`humantask/memory.go:172-186`). Neither is exported, so `httpcore.AuthorizeAdmin` will hand-roll a third, with its own normalisation behaviour (none — see F3). Plan Task 8 Step 3 says only *"implement"* and points at no precedent. CLAUDE.md's ADR-0186 lesson — *"search the repo for an existing convention BEFORE writing a new symbol"* — applies literally.

**Evidence.** `authz/authz.go:120-159` and `humantask/memory.go:172-186` read in full; `grep -rn "hasAnyRole\|hasRoleOverlap"` shows both unexported and no shared helper.

**Concrete fix.** Export a single `authz.HasAnyRole(actorRoles, want []string) bool` in this bundle (with the empty-string normalisation F3 needs), have `AuthorizeAdmin` and, over time, the other two call sites use it — and add one sentence to spec §4 residual 2 recording that Decision 7's opt-in default is the same "empty means allow" shape as backlog 53, so the two are fixed together rather than diverging.

### F16 — Decision 2 forbids the library from picking an identity string that lands in a durable audit record; Decision 9 prescribes one into the four most-copied files in the repo — [MAJOR]

**Bundle text attacked:**
- ADR §Decision 2: *"**There is no `WithAnonymousActorAllowed`.** An open deployment states its own identity in three lines. **The library never picks a sentinel that would land in a durable audit record.**"*
- ADR §Decision 9: *"The three wiring mains take a constant demo actor. ⚠ That makes them answer **200** where they answer **403** today — strictly more open — so **the actor is named `demo-user`**, the comment says DEMO ONLY, and the mains do not mount `AdminRoutes`."* plan Task 16 Step 2 repeats it.

**The failure.** The two decisions state opposite rules about the same object. The library's own reference wiring — `examples/{production,sqlite,mysql}_wiring/main.go`, plus the new `examples/authenticated_tasks/` — is where a consumer starts a project by copying. Prescribing `authz.Actor{ID: "demo-user", …}` into all four **is** the library picking a sentinel identity string, and a claim made with it writes `claim.actor.id = "demo-user"` into `wrkflw_human_task.claim_actor` and `wrkflw_instances.snapshot` — precisely the durable audit record Decision 2 refuses to pollute. A copied-and-forgotten `demo-user` is *worse* than a library-provided anonymous mode, because a library-provided mode can be searched for, deprecated and warned on; a string copied into a consumer's repo cannot.

Round 1 filed this (B6/F6/F8) and the adjudication **accepted** it, noting *"the argument used to kill the library-chosen sentinel … applies harder to a string prescribed into the most copy-pasted files in the repo."* The revision's response is a comment (`DEMO ONLY`) and not mounting `AdminRoutes` — and F11 above shows the second half is already false for `production_wiring`. An accepted finding was answered with prose.

The *"strictly more open"* property is also unmitigated: three reference mains that answer 403 today will answer 200 after this bundle, in a delivery whose entire subject is closing an authorization hole. A reader who copies `production_wiring` gets a task API that grants every caller a `manager`-equivalent identity.

**Evidence.** ADR Decision 2 and Decision 9 quoted verbatim above; `authz/authz.go:30-38` shows `Actor.ID` is the wire contract rendered by *"faithful passthrough (ADR-0147)"* into the audit records; `examples/production_wiring/main.go:264` is the mount a copier inherits.

**Concrete fix.** Do not hardcode an identity in the wiring mains. Instead:
1. Wire the mains to a resolver that reads an **env-provided** identity and **refuses to start** when it is unset — `WRKFLW_DEMO_ACTOR_ID`, mirroring the existing `ADMIN_TOKEN` pattern at `production_wiring/main.go:77-81`, which already logs a warning and refuses rather than defaulting. That keeps the mains runnable, makes the identity the operator's choice, and cannot be copied into production unnoticed.
2. Keep `examples/authenticated_tasks/` as the one place with a worked constant, since it exists to demonstrate the seam and its credential check is prescribed to be a real function of a real secret.
3. Reconcile the two decisions in the ADR with one sentence saying which rule governs examples, or delete the absolutist clause from Decision 2 — it cannot stand as written while Decision 9 exists.

---

## Confirmations — what I attacked and found sound

- **CONFIRMATION 1 — Decision 4's arms-first ordering and the 503-not-403 rule are right, and the generalisation is right.** *"An arm whose sentinel wraps caller-supplied errors must precede every arm its payload could match"* is the correct rule, and `errors.go`'s existing 413-before-400 comment is the same reasoning already in the file. The co-match test the revision adds (plan Task 2's fifth case) closes the standing-invariant violation round 1 committed. My only adjacent finding is F6, which is about a *missing row* in the decision table, not about the ordering.
- **CONFIRMATION 2 — §3.1's clone-depth honesty is exactly right, source-verified.** `authz/authz.go:41-47`: *"Attributes are cloned one level deep: nested maps and slices inside an attribute value remain shared."* The spec states the limit instead of over-promising, and plan Task 1's test asserts the *shared* nested value as the contract — a test that pins the true behaviour rather than a wish. This is the bundle's best piece of epistemics.
- **CONFIRMATION 3 — §2.7's optional claim body is a real break correctly caught.** `ClaimInput` becoming zero-field genuinely breaks the correctly-migrated client, and Task 6 is the right scope (claim route only). ⚠ One caveat for the execution lens, not a finding of mine: §2.3's fiber tolerance probe used a **one-field stand-in** (`claimInputAfter struct{ Nothing string }`), not the zero-field `struct{}` that actually ships. The premise is probably fine but was not measured on the shape it is about.
- **CONFIRMATION 4 — §2.9's provenance argument holds on the facts.** `service.ActionableTask.Candidates []authz.Actor` (`service/instance.go:237`) renders `authz.Actor`, whose `Attributes map[string]any` carries `json:"attributes,omitempty"` (`authz/authz.go:36`), so consumer-supplied attributes already reach an anonymous reader of `/instances/{id}/actionable` today. See the verdict below for the one qualification I would add.

---

## Verdict

**THE BUNDLE DOES NOT SURVIVE THIS LENS.**

| severity | n | findings |
|---|---|---|
| CRITICAL | 4 | **F1** (the adopted resolver timeout does not bound a ctx-ignoring resolver, and past the deadline the request proceeds with `err == nil`) · **F3** (the admin role gate has two silent fail-opens, one of which reports as enabled) · **F7** (the empty-ID rule was removed wholesale where ADR-0148 blesses only the roles-carrying kiosk shape, so the zero `Actor{}` now passes every gate) · **F11** (`examples/production_wiring` IS the consumer Decision 7 claims not to break, and Decision 6 breaks it) |
| MAJOR | 10 | **F2** (marshalability failure classified 400 against two documented conventions in the file being edited) · **F4** (Decision 7's opt-in justification refuted by Decision 6; godoc quote truncated; one adapter generalised to three) · **F5** (the accepted "unbounded durable writes" leg dropped without adjudication) · **F6** (503 path logs the resolver's error at ERROR — a credential channel; the `ErrUnauthenticated` pass-through exists only in the plan) · **F8** (five live doc sites falsified, README's headline mount broken, Task 17's sweep cannot reach any of them) · **F9** (`HealthRoutes` exemption justified by naming `/healthz` while covering `/readyz`) · **F10** ("the unauthenticated read surface closes" is the forbidden overreach; residuals 4/6 call a state-changing gap a read gap) · **F12** (401-vs-400 diverges between route groups; the parity test pins the wrong axis) · **F13** (consumer I/O added to three read hot paths in a bundle that rejects a design for one extra resolver call) · **F16** (Decision 2 forbids a library-picked audit identity; Decision 9 prescribes `demo-user` into four copy-pasted files) |
| MINOR | 2 | **F14** (hand-built config fails closed on the resolver, open on its timeout) · **F15** (backlog 53's "empty means allow" re-created at a new site; a third hand-rolled role-membership test) |
| **total** | **16** | |

**Three of the four Criticals are fail-opens in the mechanism the bundle exists to build** (F1, F3, F7); the fourth (F11) is an in-tree, executed refutation of the reasoning behind Decision 7 and of the author's own interaction grid.

⚠ **The structural observation.** The adjudication's D-1 recommended cutting `Attributes` and D-3 recommended keeping the route groups out of scope, explicitly on the grounds that *"the repo's own meta-analysis says the failures are interaction failures, which a one-decision bundle cannot have."* The owner overrode both, taking the bundle from a proposed **one** decision to **nine**. Two of my four Criticals (F3, F11) live entirely inside the decisions that expansion added, and F4 is a pairwise interaction between two of them that the author's grid resolved in the wrong direction. That is the predicted outcome, arriving on schedule. It is the owner's call to make, but the cost has now been measured rather than forecast.

⚠ **On §4's nine residuals, per my brief.** Treating each as a candidate finding: residual **3** (empty-ID passes) is F7 and is materially understated — it names only the admin gate, and omits the durable-audit and `AssignedTo` consequences; residual **4** (resolve-and-discard) and **6** (per-instance authorization) are F10 and mis-describe a state-changing gap as a read gap; residual **5** (two placements) is F12 and omits that the split is client-observable; residual **9** (no `WWW-Authenticate`, no `Retry-After`) is genuinely minor and correctly parked. Residuals **1**, **2**, **7** and **8** I attacked and found honestly scoped. So: **four of nine residuals are findings, not absolutions**, and one accepted round-1 finding never made it into the residual list at all (F5).

---

## On round 1's F2 rejection — was the controller right?

**Yes on the fact; the rejection was correct, and I would keep it.** I re-executed the provenance independently rather than inheriting it: `service.ActionableTask.Candidates` is `[]authz.Actor` (`service/instance.go:237`), `authz.Actor.Attributes` carries `json:"attributes,omitempty"` (`authz/authz.go:36`), and `GetActionableView` is mounted by the same `Mount` with no authorization (`transport/http/stdlib/groups.go:69-77`). So an anonymous reader can already receive consumer-supplied actor attributes today, and my predecessor's framing — *"the defect being fixed was also the mitigation"* — did overstate it. Partial rejection on provenance, exposure finding retained: right call, and the controller was right to execute rather than adjudicate on the document.

**But I would attach one qualification the ADR does not carry, and it is not cosmetic.** The pre-existing channel is fed by `humantask.ActorResolver` — an **opt-in** dependency a consumer must configure before `candidates` carries anything at all. The channel this bundle adds is fed by `RequestActorFunc`, which after Decision 2 **every HTTP consumer must configure to use the transport at all**. Provenance is identical; *population rate* is not. Today the exposure exists for consumers who deliberately wired a directory resolver; after this bundle it exists for every consumer whose resolver enriches the actor, on every claim. §2.9's conclusion — *"the HTTP path stops being **accidentally narrower** than the embedded one"* — is true and is also the whole finding restated as a benefit.

⇒ **the rejection stands; the ADR's use of it does not.** §2.9 and Decision 5 should say: *the exposure class pre-exists via `candidates` and needs no ADR-0187 amendment, AND this record turns a channel that required an opt-in resolver into one every consumer feeds by default — which is why Decision 6's authentication of `InstanceRoutes` is load-bearing rather than incidental.* As written, the provenance argument is doing double duty as both "not our fault" and "therefore not a cost", and only the first half is earned.
