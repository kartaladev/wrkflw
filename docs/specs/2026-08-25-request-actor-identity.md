# ADR-0189 — the HTTP transport does not accept a self-asserted actor

**Backlog item closed:** **51** — `transport/http/httpcore` builds `authz.Actor` from the
request body, so any caller can post `{"actor":{"id":"alice","roles":["manager"]}}` and be
believed. The most directly exploitable open item in the repo.

**Status:** ⛔ **RE-CUT to ONE decision after failing rule-#9 audits TWICE.** Not an input to
implementation until re-audited.
- Round 1 (`7fa756d0`, 2 decisions): **48 findings, 7 Criticals** — `audit-0189-adjudication.md`.
- Round 2 (`37d77a34`, 9 decisions): **58 findings, 15 Criticals** — `audit2-0189-adjudication.md`.

⭐ Findings/lens moved 12.0 → 14.5 (inside the repo's 15.14 ± 0.83 band, i.e. a function of lens
count) while **Criticals/lens more than DOUBLED, 1.75 → 3.75.** Three lenses independently
attributed that to the widened scope: *"five of the seven Criticals are holes the revision's own
fixes opened in each other"* · *"two of my four live entirely inside the added decisions"* ·
*"the decisions survived; the **verification layer** failed."* ⇒ scope cut back by owner decision.

**Split out to ADR-0190:** route-group authentication and the admin posture. ⚠ 0190 must argue
against **ADR-0095 §"Admin-by-composition (default-absent)"**, which round 2 found this bundle
contradicting without ever citing it. **Author's REMOVAL grid for the cut:
`docs/plans/sweep-evidence/audit2-0189-removal-grid.md`**, written before it.
**Branch:** `feat/request-actor-identity`. **Base:** `main` at `9789ebcc`.
**Plan:** `docs/plans/2026-08-25-request-actor-identity.md`. **ADR:** `docs/adr/0189-*.md`.
**The author's own pairwise pass over the revision**, written BEFORE it per rule #9's corollary:
`docs/plans/sweep-evidence/audit-0189-author-interaction-grid.md`.

---

## §1 Provenance — what is inherited, what was re-derived, what is dropped

This design is a **re-cut of D1**, the first of the three decisions in
`docs/adr/0185-authorization-identity-is-not-self-asserted.md` — **a bundle that failed its
rule-#9 audit three times** (2026-08-20 five decisions; 2026-08-21 five decisions revised;
2026-08-23 three decisions). Adjudication of that last round:
`docs/plans/sweep-evidence/audit-0185core-adjudication.md`.

**D1 was not why that bundle failed.** Nineteen of its 22 raw Criticals were D3's; D1 had two.
This record inherits **nothing** from D2 (backlog 52, the allow-all default authorizer) or D3
(backlog 53, the empty spec that means allow-all) — both were removed *because their designs were
refuted*, and each needs its own ADR.

### 1.1 Changes relative to ADR-0185 D1

| ADR-0185 D1 said | This record | why |
|---|---|---|
| `httpcore.WithAnonymousActorAllowed()` is the opt-in for demo wiring | **Dropped entirely.** An open deployment writes a three-line `WithRequestActor`. | Its only accepted Critical was that the anonymous opt-in and the empty-`Actor.ID` rejection void each other, forcing the library to invent a sentinel identity that lands in the durable audit record. Removing the mechanism dissolves the interaction instead of designing around it. |
| *"`Actor.Attributes` reaches the authorizer — closing finding 4's second leg for free."* | **Deleted**, and replaced by the measured table in §3.5. | Refuted in ADR-0185's audit, **and** its referent (the old D4 strict-reference rule) is deferred. |
| Endpoint signature unstated | The resolver is a **parameter** on the three task endpoints — §3.2. | Owner decision, over passing `CustomizeConfig[R]` (which would make the endpoints generic over a router type they never use) and over resolving in each adapter (which duplicates a security decision at nine sites). |

### 1.2 Changes forced by ROUND 1's OWN audit — the four owner decisions

| round 1 said | this revision | source |
|---|---|---|
| an empty `Actor.ID` is **refused** (401) | **the rule is REMOVED** | owner D-2. It deleted ADR-0148's *"deliberately legal"* kiosk claimant over HTTP — a shape ADR-0183 explicitly **declined** to supersede — and round 1 cited neither ADR. §3.3. |
| only the three **task** routes are touched | round 2 widened to **all groups except `HealthRoutes`** plus an admin role gate — ⛔ **REVERTED after round 2.** Only the three task routes are touched. | owner D-3, then cut back: Criticals/lens doubled and three lenses attributed it to the widening. → **ADR-0190**. |
| flowing `Attributes` is a **cost** (backlog 103 becomes reachable) | flowing `Attributes` **narrows a live fail-open** (1 of 8 shapes); guarded by a depth bound, a size bound and a typed deep copy | owner D-1 = keep. Round 1 signed the consequence backwards; rounds 2 and 3 each got the guard wrong. §3.5, evidence §2.9. |
| *"fails closed at every entry"* | the auth-behind-body-decode ordering is **stated, not fixed** | owner D-4. §3.6. |

⚠ **Every factual claim below was executed.** Where a prior number survived re-derivation that is
stated as a re-derivation — and §2.6 now pastes the **member set**, because round 1 proved a
matching total is not a re-derivation.

---

## §2 Executed premises

### 2.1 The defect — exactly three self-asserted actor sites, all in one file

```
$ grep -rn "authz\.Actor{" transport/ | grep -v _test.go
transport/http/httpcore/endpoints.go:119:   Actor:  authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles},
transport/http/httpcore/endpoints.go:132:   Actor:   authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles},
transport/http/httpcore/endpoints.go:150:   By:     authz.Actor{ID: in.By.ID, Roles: in.By.Roles},
```

Three, not more. The net is closed **by construction**, not by the grep: `httpcore/dto.go`
declares exactly three Actor-bearing fields — `ClaimInput.Actor`, `CompleteInput.Actor`,
`ReassignInput.By` — and §2.6's compile ablation is the machine check on that.

`CustomizeConfig` (`httpcore/seam.go`) declares **eight** fields (`BasePath`, `Wrap`,
`InstanceMapper`, `MaxBodyBytes`, `BodyReadTimeout`, `Logger`, `TracerProvider`,
`MeterProvider`) and **no identity seam**, so a consumer's authentication middleware has no
supported way to override the body-derived actor. ⚠ ADR-0185 inherited "six fields" from a
pre-ADR-0186 draft; eight is the count at `9789ebcc`.

`authz.Actor.Attributes` exists (`authz/authz.go`) and is **dropped at all three sites**, so
attribute predicates over actor attributes cannot be satisfied over HTTP at all.

### 2.2 fiber propagates via `SetContext`, NOT `Locals` — and this is load-bearing

The whole design rests on the request context reaching `httpcore` unmodified. `stdlib`
passes `req.Context()` and `gin` passes `gc.Request.Context()`, both standard. **fiber v3.4.0
is not standard**: `Ctx.Context()`'s own godoc says it *"returns a non-nil, **empty** context,
if it was not set earlier"*, and `Ctx` separately implements `context.Context` whose `Value`
reads **Locals**. Two different objects. Executed:

```go
// throwaway probe, transport/http/fiber/zzprobe_test.go, deleted after the run
app.Use(func(c fiberlib.Ctx) error {                       // "SetContext" case
    c.SetContext(context.WithValue(c.Context(), probeKey{}, "from-middleware"))
    return c.Next()
})
app.Use(func(c fiberlib.Ctx) error {                       // "Locals" case
    c.Locals(probeKey{}, "from-middleware"); return c.Next()
})
app.Get("/p", func(c fiberlib.Ctx) error {
    // c.Context() is exactly what transport/http/fiber/groups.go hands to httpcore
    t.Logf("PROBE %-11s c.Context().Value=%v   c.Value=%v",
        tc.name, c.Context().Value(probeKey{}), c.Value(probeKey{}))
    ...
})
```

```
PROBE SetContext  c.Context().Value=from-middleware   c.Value=<nil>
PROBE Locals      c.Context().Value=<nil>             c.Value=from-middleware
--- PASS: TestZZProbeFiberContextPropagation (0.00s)
ok  github.com/kartaladev/wrkflw/transport/http/fiber  0.626s
```

⇒ **a consumer following fiber's most idiomatic middleware path (`c.Locals`) gets a silently
unauthenticated request.** Under this design that is a 401 rather than a false identity, so
it fails closed — but it is a trap, and `SECURITY.md` and `examples/authenticated_tasks` must
show `c.SetContext`. This re-confirms the ADR-0185 audit's execution finding F13 at a new base.

### 2.3 A stale `"actor"` body is IGNORED, not rejected — all three adapters

Removing a field from a DTO is only non-breaking for in-flight clients if the decoder
tolerates the now-unknown key. ADR-0167 made *definition* decoding strict; the question is
whether that reaches the DTO decode path. It does not:

```
$ grep -rn "DisallowUnknownFields\|EnableDecoderDisallowUnknownFields" transport/ internal/
(no matches)
```

`stdlib/body.go:143` uses a plain `json.NewDecoder(body).Decode(dst)`; `gin` uses
`gc.ShouldBindJSON`, tolerant unless the global `EnableDecoderDisallowUnknownFields` is set,
which nothing sets. fiber's v3 binder was executed rather than read:

```go
// throwaway probe: ClaimInput with Actor REMOVED, posted a body that still carries it
type claimInputAfter struct{ Nothing string `json:"nothing,omitempty"` }
if err := c.Bind().JSON(&in); err != nil { ...400... }
// body: {"actor":{"id":"alice","roles":["manager"]}}
```

```
PROBE fiber Bind().JSON -> OK, ignored unknown keys, in={Nothing:}
PROBE fiber status=200 (200 => ignored, 400 => rejected)
--- PASS: TestZZProbeFiberUnknownKeyTolerance (0.00s)
```

⇒ *ignored, not rejected* is correct for all three. A 400 would buy no security — the value
is never read — and would break consumers' rollout windows.

### 2.4 The nine adapter call sites

```
$ grep -rn "ClaimTask\|CompleteTask\|ReassignTask" transport/ | grep -v _test.go | grep -v httpcore/
fiber/groups.go:151,168,185      gin/groups.go:172,192,212      stdlib/groups.go:140,154,168
```

All nine pass `cfg.InstanceMapper` as the last argument, so all nine take **one added
argument** under §3.2 and none gains a branch: the resolver's error returns through the
existing `err != nil → writeErr(cfg, …)` path each already has, and `writeErr` calls
`httpcore.ClassifyError`.

⚠ The adapter option-alias sets are **already uneven**: `stdlib` and `gin` export
`WithBasePath`/`WithMaxBodyBytes`/`WithBodyReadTimeout` (gin adds `WithMiddleware`), while
**`fiber` has no `WithBodyReadTimeout`** — fasthttp has read the whole body before the
handler runs, so there is nothing to bound. Do not infer the new alias set from any one
adapter's file.

### 2.5 The three wiring mains never claim a task

ADR-0185's failure-modes finding F5 was framed as *"the three demo mains would be unable to
claim."* That reads as a `go run` break. It is not one:

```
$ grep -in "claim\|complete\|reassign" examples/{production,sqlite,mysql}_wiring/main.go
(no call sites — only the words "complete"/"completes" in comments and log strings)
```

All three are `ListenAndServe` servers that mount routes and wait; `stdlib.Mount(mux, svc)`
at `production_wiring/main.go:264`, `sqlite_wiring/main.go:278`, `mysql_wiring/main.go:262`.
The real exposure is narrower — **a reader who `curl`s the mounted task route gets 401** —
which is what §3.5 addresses, and it is a materially weaker argument for a library-provided
anonymous mode than F5 implied.

⚠ **ASSUMPTION (unverified):** that these three mains still *start* cleanly after the change
is expected but not yet executed; the plan's Task 10 runs each one.

### 2.6 The blast radius — the MEMBER SET, re-derived

⚠⚠ **Round 1 of this spec got this wrong three ways and called the result "exhaustive".** The
correction is not a bigger number; it is a **method change**, and it is the most transferable
thing this delivery produced:

> ⭐ **A count is re-derived only when its MEMBER SET is re-derived. Paste the list, not the
> total.** Two different nets agreeing on a total is not corroboration — here it was coincidence.

**The three round-1 errors, each measured:**

1. **The ablation modelled one of the two breaking changes.** It deleted the DTO fields but
   **stubbed `endpoints.go`** rather than adding the resolver parameter, so every consumer of the
   changed signatures was invisible — the **9 production adapter call sites** and 3 further test
   lines.
2. **Its "29" was a different set from the inherited "29".** The ADR-0185 grep net sees
   `dto_test.go`'s JSON **fixtures**; the ablation sees its **assertions**. Each contributes
   exactly 5 and the totals matched by coincidence. Both must be edited: the fixture and the
   assertion are different lines of the same test.
3. **It missed a 6th package.** `service/instance_test.go:1090,1128` carry comments citing
   `httpcore.Actor` *and* ADR-0147 amendment #5 — the sentence §3 amends.

**The derivation, both changes modelled**, in a detached worktree at the bundle commit:

```
$ go build ./...                                     # production call sites
$ go test -count=1 -gcflags=-e -run '^$' ./...       # test packages, error cap lifted
```

#### Compile-breaking — 23 lines / 5 files / 4 packages

| file | lines | n |
|---|---|---|
| `transport/http/stdlib/groups.go` | 140, 154, 168 | 3 |
| `transport/http/gin/groups.go` | 172, 192, 212 | 3 |
| `transport/http/fiber/groups.go` | 151, 168, 185 | 3 |
| `transport/http/httpcore/dto_test.go` | 47, 62, 73, 84, 153 | 5 |
| `transport/http/httpcore/endpoints_test.go` | 405, 422, **436**, 466, 485, **499**, 531, 560, **575** | 9 |

**Bold** = invisible to round 1's ablation (`not enough arguments in call to httpcore.ClaimTask`).

#### Runtime-only — 23 lines / 7 files / 4 packages

These keep compiling and fail (or silently pass) at assertion time.

| file | lines | n |
|---|---|---|
| `transport/http/httpcore/dto_test.go` | 57, 68, 79, 151, 161 | 5 |
| `transport/http/gin/gin_test.go` | 413, 421, 443, 453 | 4 |
| `transport/http/gin/gin_coverage_test.go` | 192, 218, 244 | 3 |
| `transport/http/fiber/fiber_test.go` | 563, 585, 592, 615, 624 | 5 |
| `transport/http/stdlib/errors_test.go` | 155, 187 | 2 |
| `transport/http/stdlib/stdlib_test.go` | 471 | 1 |
| `transport/http/stdlib/coverage_test.go` | 92, 126 | 2 |
| `transport/http/parity/parity_test.go` | 518 | 1 |

#### Stale documentation — 2 lines / 1 file / 1 package

`service/instance_test.go:1090,1128` — invisible to **both** nets.

#### Third net — the OPTIONAL CLAIM BODY, invisible to both nets above — 2 lines / 2 files

⚠ Both nets above are nets for the **DTO removal**. §3.6's optional claim body is a **third
behavioural change** neither can see: it flips two live, currently-green tests from 400 to 401.

| file | line | test | today | after |
|---|---|---|---|---|
| `transport/http/stdlib/coverage_test.go` | 148 | erroring body reader on claim | 400 | **401** |
| `transport/http/gin/gin_coverage_test.go` | 184 | `not-json` on claim | 400 | **401** |

fiber has no equivalent bad-JSON claim test, so it is exactly two.

#### Total: **50 lines · 13 files · 6 packages**

Packages: `httpcore`, `stdlib`, `gin`, `fiber`, `parity`, `service`.
⚠ The audit's proposed **37** is also wrong — it unions the two *pin* nets and omits the 9
production call sites and the 2 `service` comments.
⚠⚠ **48 was wrong too, and for the third-round-running reason:** a decision was added after the
count and the count was not re-derived. The transferable rule is not "count more carefully" — it is
***every behavioural change needs its own net***, because a compile ablation sees signature
changes, a grep sees literals, and neither sees a status code moving.
⚠ `httpcore/validate_test.go`'s `httpcore.Validate(httpcore.ClaimInput{})` survives field removal
untouched and is deliberately **not** a member.
⚠ `examples/{production,sqlite,mysql}_wiring` report `[build failed]` under the ablation, but
**transitively** (they import the broken `stdlib`), not from a site of their own. Their edits are
design-driven (§3.7), not compile-forced.

#### The two vacuous pins — prediction CONFIRMED

`stdlib/errors_test.go:155` (assertion at **:158**) and `:187` (assertion at **:190**) both assert
403 from a `viewer`. Round 1 predicted, and labelled as a prediction, that dropping the anonymous
opt-in would make them fail **loudly** rather than pass vacuously. The audit's execution lens
built the change and ran them: **`want 403 complete forbidden, got 401`**. ⭐ Confirmed.

⚠ `gin/gin_coverage_test.go:244` asserts **404** on a nonexistent token; **gin carries no 403
assertion at all**. ADR-0185's "one stdlib, one gin" was wrong and stays corrected.

### 2.7 A correctly-migrated client is broken by the naive form of §3.2

`ClaimInput` becomes a **zero-field struct**, so a correctly-updated client sends **no body**.
Executed against a real mounted `stdlib` route:

```
no body at all    -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
empty JSON object -> proceeds to authorization
```

⇒ claiming would require literally sending `{}`. §2.3 reasoned carefully about the **lagging**
client and never considered the **updated** one. ⚠ No test in round 1's plan could have caught
this: the endpoint tests call `ClaimTask(...)` directly and bypass decode entirely. Fixed by
§3.6.

### 2.8 The context trap is NOT fiber-specific

Round 1 called it fiber-specific and gin "standard". Measured, both wrong:

```
gin   gc.Set (canonical idiom)               Request.Context()=<nil>            gc.Get=from-middleware
gin   gc.Request = gc.Request.WithContext    Request.Context()=from-middleware  gc.Get=<nil>
fiber c.SetContext                           c.Context().Value=from-middleware  c.Value=<nil>
fiber c.Locals                               c.Context().Value=<nil>            c.Value=from-middleware
```

Each framework's **most idiomatic** middleware channel is the one that does **not** reach
`httpcore`. Under this design that is a 401 rather than a false identity — fail-closed — but it
is a footgun in both, and §3.7 puts it in runnable code rather than prose.

### 2.9 The `Attributes` exposure is PRE-EXISTING — provenance, executed

The audit filed flowing `Attributes` as creating an unauthenticated data-exposure class. Executed:

```go
svc.ClaimTask(ctx, service.ClaimTaskRequest{TaskID: id, Actor: authz.Actor{
    ID: "alice", Attributes: map[string]any{"home_address": "...", "bearer": "tok_secret"}}})
// -> claim.actor.attributes = map[bearer:tok_secret home_address:...]
```

⇒ **an embedded consumer already persists actor attributes today, with no ADR-0189.** And
`wrkflw_human_task.candidates` is **already** `ClassActor` (`internal/atrest/classification.go:203`),
already `json.Marshal`'d from `[]authz.Actor` (`humantask_store.go:161`), and ADR-0147 Decision 5
says attributes appear there whenever the consumer's `ActorResolver` populated them.

⇒ two consequences, both of which change the audit's adjudication:
- **ADR-0187's at-rest classification needs no amendment** — a sibling `ClassActor` column already
  carries this exact payload. Interaction-lens F11 is **refuted**.
- The unauthenticated read exposure **pre-exists this bundle**; §3.6 closes it as a side effect of
  authenticating `InstanceRoutes`, and the *pre-existing* half is filed as its own backlog item.

## §3 The design

### 3.1 The identity seam lives in `authz`, as plain functions

```go
// authz/context.go — new file. stdlib only; authz's purity comment stays true.
type actorContextKey struct{}

func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a.Clone())
}

func ActorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok {
		return Actor{}, false
	}
	return a.Clone(), true   // clone on the way OUT as well as IN
}
```

⚠ **The isolation guarantee is ONE LEVEL DEEP, and this record says so.** `Actor.Clone`'s own
godoc states `Attributes` are cloned one level: a nested map or slice inside an attribute value
stays shared. Round 1 wrote *"a later mutation by the caller cannot reach the engine"* without
that qualifier — false for exactly the payload §3.5 admits — and its clone test used a flat
attribute and could not have detected it.

### 3.2 The transport reads the actor from the context, and only from the context

`ClaimInput.Actor`, `CompleteInput.Actor`, `ReassignInput.By` and the `httpcore.Actor` type are
**removed**. `ReassignInput` keeps `From`/`To`.

```go
type RequestActorFunc func(context.Context) (authz.Actor, error)

func ClaimTask(ctx context.Context, svc service.Service, token string,
	in ClaimInput, mapper func(engine.InstanceState) any, actor RequestActorFunc,
) (int, any, error)
```

Resolution happens **once, in `httpcore`** for the task verbs; the nine adapter call sites gain
one argument and no branch.

### 3.3 The refusal rules — the target is the ZERO actor

| condition | result |
|---|---|
| nothing authenticated the request | **401** `ErrUnauthenticated` |
| `RequestActorFunc` is nil (a hand-built config) | **401** — forgetting the seam fails CLOSED |
| the resolver returns an error | **503** `ErrIdentityUnavailable`, wrapping it |
| the resolver returns the **zero actor** (no ID, no non-empty role, no attributes) | **401** |
| `Actor{ID: "", Roles: ["kiosk"]}` — the kiosk shape | **passes** |

⚠⚠ **Wrong twice; re-derived from what it must PREVENT.** Round 1 refused any empty `Actor.ID`,
deleting the kiosk claimant. Round 2 accepted everything. Round 3's *"at least one dimension"* was
justified by two properties it **does not deliver** — measured against the real `RoleAuthorizer`,
`{Roles:[""]}` and `{Attributes:{"x":nil}}` pass and are as unattributable and
`AssignedTo("")`-invisible as `Actor{}`, **and so is the blessed kiosk shape**, so unattributability
cannot be the distinguishing harm; and the deny-list fail-open closed in **1 of 8** shapes. It also
refused `Roles:[]string{}` while admitting `Roles:[]string{""}`.

⇒ **the rule prevents exactly ONE thing: the ignored-error signature.** `actor, _ :=
authenticate(r)` yields `Actor{}` with a nil error, and the request would proceed as though
authenticated. The zero value with `err == nil` is that bug's exact fingerprint, and the library
can spot it for free.

**An empty string is not a role** — `strings.Split("", ",")` returns `[""]`, length 1, which is
what the canonical header middleware produces for a header-less request. Empty role strings are
therefore ignored when deciding zero-equivalence. Executed over eight shapes; the predicate matches
intent on every one, and `Roles:[]string{}` / `Roles:[]string{""}` are now treated alike.

⛔ **It does NOT** make the actor attributable, close the attribute fail-open, or otherwise separate
a deliberate anonymous principal from a careless one. Both are separate and filed.

⚠ **Provenance:** `docs/adr/0148-*.md` contains no "kiosk" and no "anonymous". The term and the
blessing are the **repo's own** (`humantask/validate.go:24`, `validate_test.go:45-47`), citing an
ADR-0148 amendment section itself titled *"The interim state fabricates a claim"*. Round 2
inherited that citation and restated it unre-derived.

⇒ this record's promise is *"a resolved, non-zero actor"*. ⛔ Never *"an identified principal"*.

⚠ **A resolver-call timeout is adopted** (default 10 s, non-positive disables), mirroring
`WithCandidateResolveTimeout` (`runtime/task/service.go:139-141`). On fiber the need is real:
`c.Context()` is `context.Background()` when no middleware set one.
⚠⚠ **It bounds only a resolver that HONOURS `ctx`.** Measured: a ctx-ignoring resolver ran 1.5 s
against a 200 ms bound and returned `err == nil`. The cited precedent carries exactly that caveat
in its own godoc; round 2 stripped the hedge when restating it. The hang is **narrowed, not
closed**, and the option's godoc must say which.

### 3.4 Both new arms are classified FIRST in `ClassifyError`

`ErrIdentityUnavailable` wraps **arbitrary consumer code's** error with `%w`, so it can co-match
*any* arm — including the 404 arm that is first today. ⇒ **an arm whose sentinel wraps
caller-supplied errors must precede every arm its payload could match.**

Consequence, deliberate: a resolver returning `authz.ErrNotAuthorized` classifies **503, not
403**. An identity resolver answers *who*, not *may*.

⚠ **The two new arms co-match EACH OTHER** — an `ErrUnauthenticated` wrapped by a resolver error
satisfies both. `errors.go`'s standing invariant demands a test for exactly that pair, and round 1
violated the invariant it cited as its own authority. The test ships here.

### 3.5 `Attributes` flow behind a DEPTH BOUND, a size bound and a deep copy

The whole `authz.Actor` reaches the engine. Measured against `RoleAuthorizer`:

| predicate class | today (attributes dropped) | after this record |
|---|---|---|
| deny-list `actor.Attributes.status != "blocked"` | **ALLOW** ← live fail-open at `main` | **DENY** |
| allow-list `actor.Attributes.status == "active"` | DENY (vacuously) | satisfiable |

⚠ Round 1 called this a **cost**. For the deny-list class it is a **fix**.
⚠⚠ But state it precisely. Two lenses measured this over different shape sets and agree on the
direction: the deny-list predicate still **ALLOWs in 7 of 8 shapes** after the change (round 3's
execution lens; an earlier pass over a 6-shape set gave 5 of 6). ⇒ flowing attributes makes the
predicate *satisfiable*, **not safe**, and closes the fail-open for **one** shape. And *"not backlog 103"*
distinguishes the **root**, not the **mechanism** — `vars.status` over empty vars and
`actor.Attributes.status` with the key absent are byte-identical ALLOWs. The live fail-open is
filed as its own backlog item regardless.

**The guard is an explicit DEPTH BOUND plus a marshal, and the seam DEEP-COPIES.** Wrong twice, by
the same category of error one layer apart:

- **Round 2**: `json.Marshal` alone. The encoder has **no nesting limit**; the decoder caps at
  **10000**. A 20000-deep attribute passed, wrote durably, and broke `HumanTaskStore.Get` forever.
- **Round 3**: round-tripped `Attributes` alone — but the durable document nests them **inside** an
  `Actor`, and there is **no single stored shape** (`claim_actor` = `Actor`; `candidates` =
  `[]authz.Actor`; the snapshot deeper). Measured: guard admitted depth 9999, store admitted 9998,
  reproduced end-to-end on real SQLite with this spec's own verbatim error text. ~20 KB, so no
  size bound helps.

```go
const maxActorAttributeDepth = 64       // 10000-64 = 9936 levels of headroom for ANY wrapper
const maxActorAttributeBytes = 16 << 10 // 16 KiB marshalled  (round 3 never gave this a value)
```

**One walk** computes depth **and produces a typed deep copy**, bailing at the budget — which also
makes it terminate on a cycle. The copy is then marshalled, catching unmarshalable values and
yielding the size. Executed: 64 passes, 65 refused; the store survives a 64-deep attribute at
wrapper depths 1, 10, 100, 1000.

⚠⚠ **The deep copy closes an UNCATCHABLE PROCESS CRASH.** `Actor.Clone` is one level deep by
design, so a consumer's nested attribute map stays shared, and marshalling it per request iterates
a map they may be writing. Executed: `fatal error: concurrent map iteration and map write` —
`recover()` does not catch it. New over HTTP; today's body-derived actor has no attributes at all.
Copying first means the marshal touches only our private copy.

⚠ **Typed copy, NOT marshal/unmarshal.** Executed: a round trip converts `int → float64` and
`time.Time → string`, changing what the `expr` authorizer evaluates. The copy reproduces
`map[string]any` and `[]any` recursively and leaves other values shared; `RequestActorFunc`'s godoc
states the library reads the attributes once and the consumer must not mutate them concurrently.
⚠ Not a new contract — `humantask.ActorResolver` → candidates → task store already reads
consumer-supplied attribute maps identically. Newly *written down*.

Both failures classify **503 `ErrIdentityUnavailable`** — the fault is the consumer's resolver.

⚠ Provenance, per §2.9: this is **not a new exposure class**. Attributes already reach the durable
stores from embedded consumers and from `ActorResolver`-populated candidates, and
**ADR-0187 needs no amendment**.

### 3.6 The claim route accepts an ABSENT body; the ordering residual is stated

`ClaimInput` becomes zero-field ⇒ the claim route decodes an **optional** body (§2.7).
⚠ Scoped to the **claim route alone**. `CompleteInput` and `ReassignInput` keep required content,
and a group-wide helper would make `POST /instances` accept an empty body and fail later with a
worse error. ⚠ Only `stdlib` has a `decodeOptionalRequestBody` helper today
(`body.go:156`, one caller at `groups.go:234`); **gin and fiber need an equivalent.**

✅ **CORRECTED AT THE DELIVERY GATE: authentication now resolves BEFORE the body read.** The text
below describes what was shipped to `/code-review` and refused; it is retained because the reasoning
that justified it is the reasoning that failed. `httpcore.RequestActor` is exported, each adapter
calls it before decoding, and the ordering is **401 → 413 → 400 → 404**.

~~⚠ Authentication on the three task routes resolves BEHIND the adapter's capped body read.~~
ADR-0186's measured read window (1 MiB / 30 s) and its 400/413 responses stay reachable without a
credential. **Unchanged from today, not a
regression** — but stated, because round 1 claimed the transport *"fails closed at every entry"*
and that is false.

⚠⚠ **A malformed claim body answers 401, NOT 400, and that IS a change.** An earlier revision
asserted 400 *"unchanged from today"* a few lines from a residual asserting 401; executed against
the real optional decoder, it is **401** — the optional path swallows the decode error and
resolution refuses first. Stated as 401 everywhere in this bundle, and it moves two live tests
(§2.6's third net).
⚠ The **413 is NOT swallowed**: `stdlib`'s `decodeOptionalRequestBody` preserves it (verified), so
ADR-0186's cap keeps its response contract. gin's and fiber's helpers do not exist yet and must
preserve it too, each with a test.

### 3.7 Examples and documentation

- 🆕 **`examples/authenticated_tasks/`** — middleware → `ContextWithActor` → claim, for **all
  three adapters**, because the trap is not fiber-specific (§2.8). The credential check must be a
  real function of a real secret: an example named for authentication must not teach trusting a
  header.
- The three wiring mains take a constant `demo-user` actor. ⚠ That makes them answer **200** where
  they answer **403** today — *strictly more open* — so it is commented DEMO ONLY, and they do
  **not** mount `AdminRoutes`.
- `SECURITY.md` gains the middleware pattern for all three frameworks, the 401/503 contract, and
  its most important "Scope notes for embedders" entry.
- **`CHANGELOG.md` and `STABILITY.md`** entries are required, per ADR-0186's precedent.
- **`docs/plans/HANDOVER.md` and ADR-0185's banner** must be updated in this bundle, or a fresh
  session is still routed into the deleted D1 design.
- **ADR-0147 amendment #5's first caveat is amended in place** — it states `httpcore.Actor` is
  `{id, roles}` only *"so over HTTP those two slots can never carry attributes"*, which this
  record falsifies while deleting the type it names. `service/instance_test.go:1090,1128` repeat
  the claim in comments and are corrected with it.

---

## §4 Residuals — stated, because a documented residual is still a shipped defect

⚠ Round 2 found four of nine round-2 residuals were **findings dressed as absolutions**. Each entry
below therefore says what is *not* mitigated and where it goes.

1. ⚠⚠ **Actor attributes reach an UNAUTHENTICATED read surface.** `GET /instances/{id}/actionable`
   and `/snapshot` render `Claim.Actor` verbatim with no authorization; `SECURITY.md` classifies
   the column as personal data. Round 2's revision closed this by authenticating `InstanceRoutes`;
   **that decision was split to ADR-0190, so the mitigation left with it.**
   ⚠ The channel pre-exists (`Candidates` already renders attributes; embedded consumers already
   persist them) — **but the old channel needs an opt-in `humantask.ActorResolver` while the new
   one is fed by `RequestActorFunc`, which every HTTP consumer must configure.** Same provenance,
   materially different population rate. ⛔ Do not write "pre-existing, therefore not a cost".
   **Filed as a backlog item on the day this ships.**
2. **`InstanceRoutes`, `MessageRoutes`, `AdminRoutes` are entirely unauthenticated**, so
   `POST /instances`, `/signals`, `/messages` — **state-changing**, not merely read — are open to
   any caller. Unchanged by this record. **ADR-0190.**
   ⚠ Round 2 described this gap as a *read* gap; it is not.
3. **The chain is NOT closed against the engine.** `ProcessDriver.ApplyTrigger`
   (`runtime/processdriver.go:548`) *"bypasses authorization entirely"*; `engine.NewHumanCompleted`
   is exported module-root API.
4. **Backlog 52 and 53 stay open ⇒ a narrowing, not a closure.** From *anyone can be anyone* to
   *anyone authenticated can be anyone the configured authorizer permits*.
5. **The resolver timeout narrows the hang, it does not close it** — a resolver ignoring `ctx` runs
   past the bound and succeeds (§3.3, measured).
6. **The attribute fail-open narrows, it does not close** — 5 of 6 shapes still ALLOW (§3.5).
7. **The clone guarantee is one level deep** (§3.1). Nested attribute values stay shared.
8. **Authentication resolves behind the capped body read** (§3.6); and the optional claim decoder
   swallows every decode error, so a **malformed** claim answers 401 rather than 400 — a real
   change, stated rather than glossed.
9. **Per-instance read authorization is absent** — backlog **62**.
10. **`httpcore.MountGroups` passes no options**, so groups mounted through it rely on the context
    seam. Workable (the default resolver reads the context); its godoc must say so.
11. **gin's ignore-a-stale-body guarantee is conditional** on `EnableDecoderDisallowUnknownFields`,
    a global the **consumer** controls.
12. No `WWW-Authenticate` on the 401, no `Retry-After` on the 503.

---

## §5 Verification and test strategy

**Hot paths first.** The resolution branch and every one of its failure modes is the hot path this
record creates. 85 % is a floor, not a target.

⚠ Round 2 found **five prescribed tests that could not fail**, one of them mutation-proved. Every
row below therefore states **what makes it fail today**, and the load-bearing ones carry a required
mutation.

| # | test | what makes it fail today |
|---|---|---|
| 1 | seam round-trips; clones **in and out** | ⚠ **MUTATION REQUIRED**: round 2's version stayed GREEN when the OUT clone was deleted. Delete each clone separately and observe RED, or the test is vacuous. |
| 2 | the clone is **one level deep** — a nested attribute value IS shared, pinned as the honest contract | undefined; round 2's flat-attribute fixture could not detect it |
| 3 | no actor ⇒ 401 · resolver error ⇒ 503 · nil resolver ⇒ 401 | no resolver parameter exists |
| 4 | `Actor{}` (no dimensions) ⇒ **401** | ⚠ regression guard against round 2, which accepted it |
| 5 | `Actor{ID:"", Roles:["kiosk"]}` ⇒ **passes** | ⚠ regression guard against round 1, which refused it |
| 6 | the 503 arm outranks 404, 403, 400 — one case each | the arms do not exist |
| 7 | **the two new arms co-match each other** and resolve to the intended one | the invariant's own missing test |
| 8 | attributes reach `service.ClaimTaskRequest.Actor.Attributes` — asserted **on the request the endpoint builds** | today they are dropped at all three sites |
| 9 | a `chan int` attribute ⇒ 503 | today it bricks the view |
| 10 | ⭐ a **65-deep** attribute ⇒ 503 | the depth bound is 64 |
| 10b | ⭐⭐ a **9999-deep** attribute ⇒ 503, **and `HumanTaskStore.Get` still works after an `Upsert`** | ⚠ **both earlier guards PASSED this** — `json.Marshal` alone, and an `Attributes`-only round trip. Assert on the STORE, not on the guard's return value: a guard tested against itself is how this was missed twice |
| 10c | a nested attribute map mutated by the caller afterwards leaves the stored actor unchanged; `int` and `time.Time` survive to the authorizer | pins the typed deep copy — a JSON round trip would convert them |
| 11 | an oversize attribute payload ⇒ 503 | no size bound exists |
| 12 | a resolver that **ignores ctx** — pinned as still-succeeding, documenting the narrowed bound | ⚠ round 2's test used a ctx-honouring resolver and could not fail on the real hazard |
| 13 | **a bodyless claim ⇒ 200**, per adapter | today 400 `EOF` — §2.7 |
| 14 | a **malformed** claim body ⇒ 401 (the optional decoder swallows it) | pins the behaviour change §4.8 names |
| 15 | per adapter × 3 verbs: body carries `"actor"`, context carries another ⇒ the **context** actor wins | today the body wins |
| 16 | per adapter: the framework's canonical-but-wrong idiom (`gc.Set`, `c.Locals`) ⇒ **401** | nothing reads the context |
| 17 | an unauthenticated request for a **nonexistent** task ⇒ 401, not 404 | pins the deliberate ordering change |

**Rewrites, not recompiles:** `stdlib/errors_test.go:158` and `:190`, each proved by mutation
(`cp` backup, never `git checkout <path>`).

**Gate:** `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` (Docker
probed first; a container-free subset is labelled **partial**), `go test ./...` repo-wide,
`golangci-lint run ./...` **repo-wide**, then `/code-review` and `/security-review` (owner-invoked).

---

## §6 What IMPLEMENTATION must verify — the audits are closed

⛔ **There is no round 4.** Three rule-#9 rounds ran (48/7C, 58/15C, 59/19C); the owner closed the
audit phase and directed that the seven round-3 Criticals be fixed and the delivery proceed to
`/code-review` and `/security-review`. The adjudications are
`audit-0189-adjudication.md`, `audit2-0189-adjudication.md`, `audit3-0189-adjudication.md`.

⚠ **The scope hypothesis rounds 1–2 acted on was refuted by round 3.** Scope went 2 → 9 → 1
decisions while Criticals/lens went 1.75 → 3.75 → **4.75**. Criticals/lens is contaminated by two
confounds nobody controlled for — each round's lenses were briefed with the previous round's
findings, and the documents grew every round — so **it must not be used as a bundle-health metric
again without controlling for both.** ⚠ The `8.25 → 3.50` series quoted in round 3's pre-registered
rule was **spliced from ADR-0186's rounds**, not this lineage.

Implementation carries the verification burden the fourth audit would have. Each item below is a
round-3 Critical whose fix is designed but **not yet executed in production code**:

1. **The depth bound must be proved against a REAL store**, not against itself (§5 row 10b). Both
   previous guards passed their own fixtures.
2. **The deep copy must be proved to stop the crash** — construct the concurrent mutation and show
   it no longer reaches `fatal error: concurrent map iteration and map write`, and that `int` /
   `time.Time` survive (§5 row 10c).
3. **The zero-actor rule must be proved on all eight shapes** (§3.3), including the two regression
   guards that point in opposite directions.
4. **Task 1's clone test must fail under BOTH mutations.** It has now been prescribed vacuous
   twice; round 3's execution lens proved the OUT-clone deletion still left it GREEN.
5. **The two 400 → 401 tests** (§2.6's third net) must move, and each adapter's optional decoder
   must be shown to **preserve 413**.
6. **`SECURITY.md` must say `InstanceRoutes`/`MessageRoutes`/`AdminRoutes` authenticate NOTHING.**
   The opposite sentence was prescribed while the removed decision existed.
7. **The survivor×survivor pairs S1–S6** (`audit2-0189-removal-grid.md`) must each have a test —
   they were never drawn until after round 3, and three Criticals lived in them.
