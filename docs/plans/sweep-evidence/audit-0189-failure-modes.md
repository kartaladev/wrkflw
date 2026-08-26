# ADR-0189 audit — FAILURE-MODES lens

Worktree: `wt-failure-modes`, detached at `7fa756d0` (the bundle commit).
Bundle present: spec, ADR, plan — all three verified at step 0.
Base of the bundle's evidence: `9789ebcc` (= `7fa756d0^`).

Findings appended in discovery order. Severity: CRITICAL = ships a security hole,
data loss, or a broken build. MAJOR = a real unhandled failure mode. MINOR = polish.

---
### F1 — the empty-ID refusal silently deletes the ADR-0148 "kiosk claimant", a shape ADR-0183 REFUSED to supersede, and the bundle cites neither ADR — [MAJOR]

**Bundle text attacked:** ADR-0189 §Decision 3 and spec §3.3 rule 3:
> "**Why an empty ID is refused.** ADR-0147 Decision 5 states that the human-task audit model
> **"guarantees nothing beyond `id`"** … Admitting `Actor{ID: ""}` breaks that single guarantee"

and its pre-emptive defence:
> "⚠ ADR-0147's Caveat that actor fields may be *"empty … inherent to passthrough"* is about
> `roles` and `attributes` being omitted when empty, **not** about `id`."

**The failure:** the bundle anticipates an objection from ADR-0147 and answers it, while being
blind to a **stronger and more recent** one that is specifically about `id`. `humantask/validate.go:24`
(shipped, on `main`):

```
// A Claim whose Actor.ID is empty is deliberately accepted: that is ADR-0148
// amendment 1 §4's kiosk claimant, anonymous but carrying roles.
```

and ADR-0183 (`docs/adr/0183-...md:69-76`, merged `a7575ed5`), which considered rejecting exactly
this shape and **decided not to**:

> "⚠ **An empty claimant is deliberately legal.** A revision of this ADR rejected it; the re-audit
> established that the shape is **blessed by ADR-0148 amendment 1 §4** — the kiosk claimant, with
> roles and no ID — … This ADR therefore does **not** supersede ADR-0148 on that point."

Concretely: today a kiosk terminal posts `{"actor":{"id":"","roles":["kiosk"]}}` to
`/tasks/{token}/claim` and gets the ADR-0148 claim shape the engine, the store schema
(`claimed_by` = `''` with a non-NULL `claimed_at` presence discriminator, ADR-0148 Decision table)
and `humantask.Validate` were all deliberately built to accept. After ADR-0189 there is **no path
to it over HTTP at all** — rule 3 refuses `Actor{ID: ""}` from the resolver too, so a consumer
cannot even opt back in with their own `RequestActorFunc`. `WithAnonymousActorAllowed`, which was
the one mechanism that could have served this case, is removed in the same bundle (§1.1).

**Evidence:**
```
$ grep -rn "0148" docs/specs/2026-08-25-request-actor-identity.md docs/adr/0189-*.md \
      docs/plans/2026-08-25-request-actor-identity.md
(no matches — EXIT=1)
$ grep -rn -i "kiosk" docs/adr/*.md
docs/adr/0183-...:72:  established that the shape is **blessed by ADR-0148 amendment 1 §4** — the kiosk claimant, with
docs/adr/0183-...:76:  one, an empty-ID **claim**, is left alone. That asymmetry is deliberate: a kiosk claim is anonymous
$ sed -n '24,25p' humantask/validate.go
// A Claim whose Actor.ID is empty is deliberately accepted: that is ADR-0148
// amendment 1 §4's kiosk claimant, anonymous but carrying roles.
```

The bundle's `Relates to:` line names ADR-0117, 0147, 0186, 0094/0095 — not 0148, not 0183.

**Concrete fix:** three options, pick one and say so in the ADR:
(a) keep rule 3 and add an explicit `Supersedes-in-part: ADR-0148 amendment 1 §4 (over HTTP only)`
    paragraph stating that the kiosk claimant is now an embedded-consumer-only shape, with the
    Negative consequence "a kiosk deployment must mint a non-empty pseudo-ID and choose the
    string itself";
(b) relax rule 3 to *"empty ID refused **unless** the resolver also returned at least one role"*,
    which is precisely the ADR-0148 kiosk discriminator and keeps the fail-closed property for
    the misconfigured-resolver case rule 3's secondary rationale actually targets;
(c) keep rule 3 but re-derive its primary justification — the ADR-0147 argument is now
    **contradicted by a later ADR** and is a second dangling citation of exactly the kind §1.1
    congratulates itself for having removed.
### F2 — the newly-flowing `Actor.Attributes` are rendered VERBATIM to UNAUTHENTICATED callers by two sibling routes the same `Mount` registers — [CRITICAL]

**Bundle text attacked:** ADR-0189 §Consequences/Positive:
> "**`Actor.Attributes` reaches the authorizer**, so attribute-based authorization over actor
> attributes becomes possible over HTTP for the first time."

and spec §4.2, which names **one** cost of that change (backlog 103, deny-list predicates
allowing on a missing variable) and describes the residual set as complete:
> "This is a **cost of shipping this record**, and it belongs in Consequences/Negative — not in
> a follow-ups list."

**The failure:** the bundle treats "Attributes now flow" as a one-way improvement into the
`Authorizer`. It is not one-way. The claimant actor is stored whole
(`humantask.Claim.Actor authz.Actor` `json:"actor"`, `humantask/humantask.go:61`) and rendered
whole by `runtime/view.ActionableView` (`runtime/view/instance_actionable.go:33`,
`Claim *humantask.Claim json:"claim,omitempty"`) and by the raw snapshot. Both are served by
routes `stdlib.Mount` registers alongside the task routes and that **ADR-0189 leaves completely
unauthenticated** — `InstanceRoutes.Customize` takes no actor before or after this change.

So the realistic consumer wiring the bundle actively invites — middleware decodes a JWT or
queries the directory, puts the claims in `Actor.Attributes` so ABAC works, calls
`authz.ContextWithActor` — publishes those attributes to anyone who can name an instance ID.
Before ADR-0189 this was impossible over HTTP for the claimant, because the three endpoints
dropped `Attributes` (spec §2.1). The defect being fixed was also, accidentally, the mitigation.

**Evidence:** executed in the worktree at `7fa756d0`, `transport/http/stdlib/zzprobe_test.go`
(throwaway, deleted after the run). It claims a task with the actor shape ADR-0189 §3.2 will
deliver, then `GET`s the routes on a bare `stdlib.Mount(mux, svc)` — no middleware, no
`WithRequestActor`, no credential on the request:

```
PROBE GET /instances/leak-probe-1/actionable status=200 body={"instance_id":"leak-probe-1",
 "status":"running","open_tasks":[{"task_id":"…","node_id":"approve","state":"claimed",
 "claim":{"actor":{"id":"alice","roles":["manager"],"attributes":{
   "email":"alice@corp.example","employee_id":"E-99213","home_address":"12 Privacy Lane",
   "raw_jwt":"eyJhbGciOiJIUzI1NiJ9.SECRET-BEARER-TOKEN.sig","salary_band":"L7"}},…

PROBE GET /instances/leak-probe-1/snapshot   status=200 body={… "tasks":[{… "claim":{"actor":
 {"id":"alice","roles":["manager"],"attributes":{"email":…,"raw_jwt":"…SECRET-BEARER-TOKEN…"}}…
--- PASS (0.00s)
```

`GET /instances/{id}` (the default `InstanceView`) does **not** leak — it projects only
`instance_id/def_id/def_version/status/started_at/ended_at/variables`
(`transport/http/httpcore/view.go:12-20`). The leak is via `/actionable` and `/snapshot` only.
Both are registered by the same `Mount` (`transport/http/stdlib/groups.go:61,72`).

**Honest scoping — what is NOT new:** the `candidates` array in the same response already renders
`authz.Actor` including attributes, and candidates come from the consumer's eligibility
`ActorResolver`, so the *rendering mechanism* predates this bundle. What ADR-0189 newly does is
route **authentication-derived** attributes — the most sensitive source a consumer has — into that
same renderer, and advertise doing so as a Positive with no counterweight.

**Concrete fix:** the ADR cannot ship "Attributes now flow" as an unqualified Positive. Minimum:
1. Add to Consequences/Negative, in the same register as the backlog-103 paragraph: *"the
   claimant's attributes become readable through `GET /instances/{id}/actionable` and
   `/snapshot`, which this record leaves unauthenticated. A consumer must not place secrets or
   PII in `Actor.Attributes` unless those routes are mounted behind their own authorization."*
2. Put the same warning in the **godoc of `RequestActorFunc`** and in `authz.Actor.Attributes`'
   godoc — that is where a consumer decides what to put in the map, and it currently reads as
   an invitation (`authz/authz.go:34`: *"populate Attributes from your ActorResolver if you
   need them"*).
3. Preferred, if the owner will accept the scope: redact `claim.actor.attributes` and
   `completion.actor.attributes` in `runtime/view.ActionableView` (the *curated* projection —
   its whole point is to be a projection), leaving the raw `/snapshot` as the
   mount-it-yourself surface. That is a small, contained change in one file.
### F3 — `RequestActorFunc` is an unbounded consumer I/O call on the per-request hot path; the repo already established the opposite convention TWICE for the identical hazard — [MAJOR]

**Bundle text attacked:** spec §3.3 / plan Task 4, the whole of `resolveRequestActor`:
```go
a, err := resolve(ctx)
switch { … }
```
No deadline. The ADR describes the resolver's expected work as exactly the hazardous kind —
> "A **transient identity-provider failure** must not become an open door"
> "a resolver whose failure wraps `kernel.ErrInstanceNotFound` (say, its **user-directory lookup**
> returns a repo sentinel)"

— i.e. network I/O against a directory, invoked synchronously on **every** claim/complete/reassign.

**The failure:** an identity provider that hangs rather than errors holds one goroutine per
in-flight request for as long as the client keeps the connection open. The `err != nil ⇒ 503`
rule handles the *fail-fast* outage and does nothing for the *hang*, which is the more common
outage shape and the cheaper attack: an attacker who can induce slow directory misses (unknown
subject lookups, a cache-miss path) converts each cheap request into a held goroutine plus one
upstream directory query. This is the same primitive ADR-0186 explicitly refused to ship — that
record added `BodyReadTimeout` because *"the cap trades a memory-exhaustion primitive for a
cheaper slowloris one"* (`transport/http/httpcore/seam.go:47-60`).

**Evidence:** the repo already has the convention, with the hazard spelled out in its own words.
`runtime/task/service.go:121-132`:
```
// defaultCandidateResolveTimeout bounds the single [humantask.ActorResolver]
// lookup performed by [TaskService.RefreshCandidates] unless overridden via
// [WithCandidateResolveTimeout]. Without a bound, an unresponsive directory
// service holds the calling goroutine for as long as the caller's context allows
// — indefinitely for a caller that passes a background context.
//
// It duplicates the ProcessDriver's default for the identical lookup (see
// runtime.WithCandidateResolveTimeout) …
const defaultCandidateResolveTimeout = 10 * time.Second
```
and `runtime/task/service.go:34-40` carries the field on `TaskService` for the same reason. So a
consumer-supplied actor resolver doing directory I/O is bounded at 10s in **two** places already
(`runtime.WithCandidateResolveTimeout` and `task.WithCandidateResolveTimeout`, "deliberately
equal"). ADR-0189 adds a **third** consumer-supplied resolver, on a hotter path than either, and
bounds it with nothing — while §3.5 goes to some length to explain that its name must not collide
with those two. The bundle looked straight at `WithActorResolver` and did not carry its timeout
convention across.

Searched the bundle: no occurrence of "timeout", "deadline" or "context.WithTimeout" anywhere in
the three documents in connection with the resolver.
```
$ grep -rn -i "timeout\|deadline" docs/adr/0189-*.md
(no matches)
```

**Concrete fix:** add `RequestActorTimeout time.Duration` to `CustomizeConfig` with
`WithRequestActorTimeout`, defaulting to the same 10s the two existing resolvers use, seeded in
`ResolveConfig`'s **struct literal** (a `time.Duration` has no nil — exactly the reasoning §3.5
already applies to `BodyReadTimeout`), non-positive disabling the bound. `resolveRequestActor`
then wraps: `ctx, cancel := context.WithTimeout(ctx, d); defer cancel()`. A deadline-exceeded
resolve returns `ErrIdentityUnavailable` ⇒ 503, which is already the right answer. Add the
matching test row to spec §5. If the owner rejects the option, the ADR must state the residual
explicitly and say why this resolver is exempt from a convention its own §3.5 cites by name.
### F4 — the resolved actor is validated on exactly ONE property, and the ADR-0186 body cap no longer bounds what reaches durable storage through these routes — [MAJOR]

**Bundle text attacked:** ADR-0189 §Decision 3's refusal matrix (four rows) and spec §3.3's
"Four rules, each with its own reason", plus ADR-0189's `Relates to:` line, which cites
> "ADR-0186 (the per-adapter option-alias convention this reuses)"

— citing ADR-0186 for its *cosmetic* convention while missing its actual subject.

**The failure:** the only property `resolveRequestActor` checks is `a.ID == ""`. Everything else
about the actor — the ID's length and character set, the size of `Roles`, and the size, depth and
JSON-marshallability of `Attributes` — is unchecked at the boundary and is next examined **inside
the commit transaction, three layers down**, where a failure classifies as **500**.

Two concrete inputs:

1. **A non-marshallable `Attributes` value.** A consumer resolver that puts a closure, a channel,
   or a self-referential map into `Attributes` (e.g. a directory client handing back its own
   struct) makes `json.Marshal` fail at `internal/persistence/store/store_core.go:81`
   (`marshal snapshot`) and `humantask_store.go:157-175` (`marshal claim_actor`). The tx rolls
   back, the error matches no arm in `ClassifyError`, and the default arm returns **500 with an
   empty body**. Observable: that principal can never claim any task, with no diagnostic the
   consumer can act on from the response.
2. **An unbounded `Attributes` map.** ADR-0186 capped the inbound body at 1 MiB precisely so
   unbounded input could not reach the engine through these handlers. The actor no longer comes
   from the body, so `MaxBodyBytes` does not bound it at all. On MySQL `claimed_by` is
   `VARCHAR(255)` (`internal/persistence/store/migrations/mysql/0001_init.sql`), so an over-long
   resolved ID is a commit-time `Data too long` in strict mode — again a 500.

**Evidence:** executed in the worktree, `humantask/zzprobe_test.go` (throwaway, deleted):
```
PROBE func-attr        err=json: unsupported type: func()
PROBE cyclic-attr      err=json: unsupported value: encountered a cycle via map[string]interface {}
PROBE 4MiB-attr        err=<nil> marshalled_bytes=4194628
PROBE Validate(func)   err=<nil>
PROBE Validate(4MiB)   err=<nil>
--- PASS (0.01s)
```
`humantask.Validate` — the repo's write-contract check, ADR-0183 — accepts both, by design: it
polices the claim/state invariant only (`humantask/validate.go:39-56`). Nothing between the
resolver and the SQL bind looks at the actor's payload. `ClassifyError`'s default arm is
`return http.StatusInternalServerError, ErrorBody{Error: "internal_error"}`
(`transport/http/httpcore/errors.go`, last case).

**Concrete fix:** the cheap, contained version — extend `resolveRequestActor` with the two checks
that turn a 500 into an actionable refusal, and state them as rows 5 and 6 of the §Decision 3
matrix:
```go
case len(a.ID) > maxActorIDBytes:          // 255, the MySQL claimed_by width
        return authz.Actor{}, fmt.Errorf("%w: resolved actor id exceeds %d bytes", ErrIdentityUnavailable, maxActorIDBytes)
```
and a marshallability probe of `Attributes` (a single `json.Marshal` on the resolver's result,
error ⇒ `ErrIdentityUnavailable`) so a broken resolver reports as a resolver fault at the
boundary rather than as an engine 500 at commit. If the owner judges the probe too costly per
request, the ADR must say so and record the residual explicitly — with the 500 named as the
observable, because "a documented residual is still a shipped defect" and this one currently
is not documented at all.

---

### F5 — an INVALID credential is the default 503+ERROR-log path, not the 401 path, and it is attacker-triggerable log amplification labelled "internal error" — [MAJOR]

**Bundle text attacked:** ADR-0189 §Decision 3 row 3 and spec §3.3 rule 2:
> "**A resolver ERROR ⇒ 503, never a downgrade.** A transient identity-provider failure must not
> become an open door."

and §3.4's disposal of the logging question:
> "`ErrIdentityUnavailable` is 5xx, so `ErrorBody.Message` is empty and the raw error is logged by
> the adapter's `writeErr`, never sent to the client — it may name the consumer's identity
> provider."

**The failure:** the design reasons about the resolver's error as if it were always *"the identity
system itself failed"*. It is not. The single most natural line a resolver author writes is

```go
claims, err := jwt.Parse(tok, key)
if err != nil { return authz.Actor{}, fmt.Errorf("parse token: %w", err) }   // ← a BAD CREDENTIAL
```

which under this contract is **503**, not 401 — and 503 is `>= 500`, so all three adapters log it
at **ERROR** with the raw error and the message `"…: internal error"`:

```
transport/http/stdlib/write.go:32   if status >= 500 { cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err) }
transport/http/gin/write.go:13      if status >= 500 { cfg.Logger.ErrorContext(gc.Request.Context(), "gin: internal error", "err", err) }
transport/http/fiber/write.go:13    if status >= 500 { cfg.Logger.ErrorContext(c.Context(), "fiber: internal error", "err", err) }
```

Three consequences the bundle states none of:

- **Attacker-controlled ERROR-log amplification.** An unauthenticated client posting garbage
  credentials at `/tasks/{token}/claim` produces one ERROR line per request, at a rate it
  chooses, on a route that needs no valid token and no valid task id. (The 401 path is clean —
  4xx is not logged — so this is specifically the 503 path.) On any deployment where ERROR-level
  logs page someone, that is a remote alert-storm primitive.
- **The log message is actively wrong.** `"internal error"` is what an operator will grep and
  alert on; the event is a *consumer resolver* fault, frequently a client-supplied bad token.
  Every such request will read in the operator's dashboard as an engine internal error.
- **The raw error may carry the credential.** §3.4 anticipates that it "may name the consumer's
  identity provider" and stops there. `fmt.Errorf("validating token %s: %w", tok, err)` is an
  ordinary thing to write, and this contract puts its output at ERROR level in the consumer's log
  aggregator — precisely the class `cc-skills-golang:golang-security` calls secret-in-logs.

**Evidence:** the three `writeErr` bodies quoted above, read at `7fa756d0`. Confirmed that 4xx is
never logged (the guard is `status >= 500` in all three), so the 401 flood has no logging cost and
the 503 flood has a per-request one. Confirmed the metric labels are bounded — `Observe` labels on
`http.method` / `http.route` (STATIC) / `http.status_code`
(`transport/http/httpcore/observability.go:96-104`) — so there is **no** metric-cardinality angle
here; that part of the design is clean.

**Concrete fix:**
1. Give the identity 503 its own log call rather than inheriting `"internal error"`: in each
   adapter's `writeErr`, `if errors.Is(err, httpcore.ErrIdentityUnavailable) { cfg.Logger.WarnContext(ctx, "identity resolution failed", "err", err) }`
   before the generic branch. Cheap, three lines, and it fixes both the wrong-label and the
   paging-severity problems at once.
2. In `RequestActorFunc`'s godoc (plan Task 3), state the amplification consequence directly and
   not only the mapping: *"a MALFORMED or REJECTED credential is an absent credential — return
   [ErrUnauthenticated]. Returning any other error makes every bad token a 503 and an ERROR log
   line an unauthenticated caller controls the rate of."* The current draft states the mapping
   but not why getting it wrong is expensive.
3. Add the warning about not embedding the credential in the returned error to the same godoc.
4. Add a spec §5 test row: a resolver returning a non-sentinel error produces 503 **and** the
   response body carries no message (row 4 covers the body; nothing covers the log).
### F6 — the bundle's own "BREAKING" change has no CHANGELOG entry and no STABILITY.md note, both of which the repo maintains and both of which ADR-0186 set the precedent for three deliveries ago — [MAJOR]

**Bundle text attacked:** ADR-0189 §Consequences/Negative:
> "**BREAKING.** Three public DTOs lose a field and `httpcore.Actor` is deleted; the three task
> endpoints gain a parameter, which breaks any consumer-written adapter calling them; and a
> deployment that mounts the task routes without authentication starts answering **401** where it
> answered 200."

and plan Task 12's file list:
> "**Files:** `SECURITY.md`, `README.md` (if it shows a task-route body carrying `actor`),
> `docs/specs/…`, `docs/plans/HANDOVER.md`, this plan's `▶ Progress` block."

**The failure:** the bundle correctly identifies its change as the most breaking in recent history
and then routes the disclosure nowhere a consumer looks. `CHANGELOG.md` and `STABILITY.md` are
both maintained in this repo and both are absent from the bundle — no mention in any of the three
documents:
```
$ grep -rn "STABILITY\|CHANGELOG\|INTERACTIONS" docs/specs/2026-08-25-*.md \
      docs/adr/0189-*.md docs/plans/2026-08-25-*.md
(no matches — EXIT=1)
```

The obligation is not inferred, it is written down, and ADR-0186 — which this ADR cites by name —
discharged it:

- `STABILITY.md` carries a per-delivery section **"### Request body caps (ADR-0186, pre-v0.1.0)"**
  recording the option convention, the new sentinel's status code, and the `ClassifyError`
  ordering invariant. ADR-0189 is the same shape of change (new `CustomizeConfig` field, three
  adapter aliases, **two** new sentinels, **two** new ordered `ClassifyError` arms with an
  ordering invariant of their own) and adds no such section.
- `STABILITY.md` §"Deprecation taxonomy" states: *"We do not silently change the behaviour of a
  non-deprecated symbol; a behaviour change to an existing symbol is treated as breaking"*, and
  prescribes Mark → Keep working → Remove. The bundle removes three exported fields and one
  exported type with no `// Deprecated:` step and never argues why the pre-1.0 latitude is being
  exercised instead. (It may well be the right call — but silence is not an adjudication.)
- `CHANGELOG.md` has a live **"### Breaking changes (pre-v0.1.0 — no stability promise)"** section
  whose ADR-0186 entry is a model of what is owed: the observable break, an example of the
  opt-out, and the per-route-group caveat. `STABILITY.md` even points at it —
  *"one of the pre-1.0 breaking changes flagged in the CHANGELOG (ADR-0081)"*.

**Evidence:** `ls` at `7fa756d0` shows `CHANGELOG.md`, `STABILITY.md`, `SECURITY.md` and
`INTERACTIONS.md` at the module root; `grep -c -i breaking CHANGELOG.md` = 10; `git tag` shows only
`audit/*` tags, confirming the pre-1.0 "no released version" posture STABILITY.md describes.

**Concrete fix:** add to plan Task 12 Step 1, as named files:
- `CHANGELOG.md` — a "Breaking changes (pre-v0.1.0)" entry modelled on the ADR-0186 one, stating
  (a) `httpcore.Actor` and the three DTO fields are removed and a stale body key is *ignored, not
  rejected*; (b) the three `httpcore` task endpoints gain a parameter, breaking consumer-written
  adapters; (c) **a deployment with no authentication middleware starts answering 401 on all three
  task routes**, with the three-line `WithRequestActor` opt-out spelled out; (d) `Actor.Attributes`
  now reach the engine and the audit record (see F2).
- `STABILITY.md` — a "### Request actor identity (ADR-0189, pre-v0.1.0)" section beside the
  ADR-0186 one, recording the two sentinels' statuses and the "an arm whose sentinel wraps
  caller-supplied errors must precede every arm its payload could match" rule the bundle
  generalises. That rule is a *policy* statement and belongs where the last one was put.
- Consider `INTERACTIONS.md`: it documents the package seams for "maintainers and embedding
  consumers who need to understand the seams", and `authz.ContextWithActor` → `httpcore` is a new
  cross-package seam.

---

### F7 — `WithRequestActor` is silently accepted and ignored by three of the four route groups, and the ADR's "specs stop being satisfiable by typing a role name" is false where `AdminRoutes` is reachable — [MAJOR]

**Bundle text attacked:** ADR-0189 §Consequences/Positive:
> "The principal is supplied by the consumer's authentication, not by the attacker. **Role-based
> and privilege-based specs stop being satisfiable by typing a role name.**"

and §Decision 2, which names the scope only as "the three task endpoints".

**The failure, two halves:**

**(a) The option is a no-op on three route groups, with no compile error and no warning.**
`CustomizeOption[R]` is one type shared by every group. `InstanceRoutes.Customize`,
`MessageRoutes.Customize` and `AdminRoutes.Customize` each call `httpcore.ResolveConfig(opts...)`
(`transport/http/stdlib/groups.go:34,106,197`) and then never read `cfg.RequestActor`. So
```go
stdlib.AdminRoutes{Svc: svc, Policies: pa}.Customize(adminMux, stdlib.WithRequestActor(fn))
```
compiles, runs, and authenticates nothing — while reading exactly like it does. The same applies
to `stdlib.Mount`, which forwards one `opts` slice to all three core groups
(`transport/http/stdlib/mount.go:18-20`): after this change `POST /tasks/{token}/claim` 401s and
`POST /instances`, `POST /instances/{id}/signals` and `POST /messages` do not.

The repo already knows this hazard needs stating: the CHANGELOG's ADR-0186 entry says
> "⚠ **The cap is per route group.** `Mount` covers 6 of the 13 decode sites per adapter; pass the
> same option to `AdminRoutes{…}.Customize(...)` to cover the rest."

ADR-0189 introduces an option with the identical scoping shape and says nothing — and unlike the
body cap, passing it to the other groups does **not** "cover the rest"; there is nothing there to
cover.

**(b) The Positive claim's quantifier is false in a reachable configuration.**
`POST /admin/role-bindings` → `httpcore.AddRoleBinding` (`transport/http/httpcore/admin_endpoints.go:272`)
→ `service.PolicyAdmin.AddRole` → `casbinauthz.policyAdmin.AddRole`
(`casbinauthz/policyadmin.go:65`), a casbin role-inheritance rule — the same mechanism a
role-based `AuthzSpec` is evaluated against. It takes **no actor and performs no authorization**.
So on any deployment where the admin routes are reachable, an attacker types the role name into
`{"user":"attacker","role":"manager"}` at a different URL and is a manager. "Stop being
satisfiable by typing a role name" is the load-bearing sentence of the whole record and it needs a
qualifier.

**Evidence:** read at `7fa756d0`. `grep -n "^func " transport/http/httpcore/admin_endpoints.go`
shows all 17 admin endpoint functions; **none takes an `authz.Actor` or a resolver**.
`transport/http/stdlib/groups.go:180-186` carries the mitigating godoc —
> "SECURITY: these routes have NO built-in authentication. Mount AdminRoutes only onto a router
> group already protected by your auth middleware (admin-by-composition, ADR-0095)"

— and `examples/production_wiring/main.go:267,274` does the right thing, mounting them on a
separate `adminMux`. **So this is not a claim that admin is broken** — ADR-0095's
admin-by-composition posture is deliberate and documented. It is a claim that the bundle's
unqualified Positive contradicts it, and that the new option's silence across route groups is a
fresh trap the ADR-0186 precedent shows the repo would normally call out.

**Concrete fix:**
1. Requalify the Positive: *"Role-based and privilege-based specs stop being satisfiable by typing
   a role name **in a task-route body**. They remain satisfiable through `AdminRoutes`
   (`POST /admin/role-bindings`), which by ADR-0095 has no built-in authentication and must be
   mounted behind the consumer's own — see `SECURITY.md`."*
2. State the option's scope in `WithRequestActor`'s godoc and in all three adapter aliases:
   *"Honoured by the TASK routes only. `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` accept
   this option and ignore it — they resolve no actor."* This is the same disclosure
   `BodyReadTimeout`'s godoc already makes for fiber (`seam.go:78-81`).
3. Add the scope caveat to the CHANGELOG entry F6 asks for, mirroring the ADR-0186 one.
### F8 — the prescribed "constant demo actor" makes the three reference mains STRICTLY MORE OPEN (403 → 200), and the spec misstates the exposure it is fixing — [MAJOR]

**Bundle text attacked:** spec §2.5, the premise that justifies dropping `WithAnonymousActorAllowed`:
> "The real exposure is narrower — **a reader who `curl`s the mounted task route gets 401** — which
> is what §3.5 addresses, and it is a materially weaker argument for a library-provided anonymous
> mode than F5 implied."

and ADR-0189 §Decision 4 / spec §3.5's remedy:
```go
stdlib.WithRequestActor(func(context.Context) (authz.Actor, error) {
    return authz.Actor{ID: "demo-user", Roles: []string{"manager"}}, nil
})
```
> "The three existing wiring mains … take the constant demo actor above, commented DEMO ONLY."

**The failure:** §3.5's remedy does not turn the 401 back into today's behaviour. It turns it into
**200**. Today a `curl` against a bare `stdlib.Mount` demo main is **403 Forbidden** — the zero
actor holds no `manager` role, so the approval task's `AuthzSpec` rejects it. ADR-0189 hands those
same three mains an identity that is *always a manager*, so the anonymous `curl` that is refused
today succeeds and claims the task.

So the net effect of this bundle on `examples/production_wiring`, `examples/sqlite_wiring` and
`examples/mysql_wiring` — the only runnable artifacts in the repo, and the thing a reader copies
first — is to convert a refusal into a success. In a delivery whose stated purpose is that "the
principal is supplied by the consumer's authentication, not by the attacker", the repo's own
reference wiring ships an authenticator that returns `manager` to everyone. A `DEMO ONLY` comment
is a documented residual, not a mitigation.

**Evidence:** executed in the worktree at `7fa756d0`,
`transport/http/stdlib/zzprobe2_test.go` (throwaway, deleted). `stdlib.Mount(mux, svc)` with no
options — exactly `examples/{production,sqlite,mysql}_wiring/main.go:264/278/262` — POSTing to
`/tasks/{token}/claim` on `transporttest.ApprovalProcess()` (a user task with
`WithEligibleRoles("manager")`):

```
PROBE no body at all           -> status=403 body={"error":"forbidden","message":"workflow-service: claim task: … workflow-authz: not …
PROBE zero actor (today)       -> status=403 body={"error":"forbidden","message":"workflow-service: claim task: … workflow-authz: not …
PROBE the ADR-0189 demo actor  -> status=200 body={"instance_id":"demo-…","def_id":"approval","def_version":1,"status":"running", …
--- PASS: TestZZProbeDemoMainCurlToday (0.00s)
```

⇒ spec §2.5's "a reader who `curl`s the mounted task route gets 401" describes the *un-remedied*
state correctly and then attributes the fix to §3.5, which produces 200. The sentence reads as
though §3.5 restores the status quo. It does not: the status quo is 403.

This also undercuts the §1.1 argument for dropping `WithAnonymousActorAllowed`. The claim was that
ADR-0185's failure-mode finding F5 overstated the cost because the mains never claim a task. True —
but the replacement is not cost-free either; it is a *behaviour change in the opposite direction*
from the one the bundle is selling, and §1.1 does not weigh it.

**Concrete fix:** pick one and state it in the ADR:
(a) **Do not give the three wiring mains a resolver at all.** Let them answer 401 and put a
    two-line comment on the mount pointing at `examples/authenticated_tasks` and `SECURITY.md`.
    401-on-an-unauthenticated-demo is the honest demonstration of what the bundle does, and it is
    *closer* to today's 403 than a 200 is. This is also the smaller diff.
(b) Keep the constant actor but give it a role the demo definitions do **not** grant (e.g.
    `Roles: []string{"demo-anonymous"}`), so the curl still refuses and the reader still sees the
    seam wired.
(c) If (a)/(b) are rejected, ADR-0189 §Consequences/Negative must state plainly: *"the three
    wiring mains change from answering 403 to answering 200 on an unauthenticated task claim"* —
    and spec §2.5's sentence must be corrected, because as written it is false about what §3.5
    achieves.
### F9 — the "silently unauthenticated middleware" trap is NOT fiber-specific: gin has it too, via `gc.Set`, and the bundle tells the reader gin is "standard" — [MAJOR]

**Bundle text attacked:** spec §2.2:
> "`stdlib` passes `req.Context()` and `gin` passes `gc.Request.Context()`, **both standard**.
> **fiber v3.4.0 is not standard**"

ADR-0189 §Decision 4:
> "⚠ It exists because of an executed trap. fiber v3.4.0's `Ctx.Context()` … Measured:
> `c.SetContext` reaches `httpcore` (`from-middleware`); **`c.Locals` does not** (`<nil>`). A
> consumer following fiber's **most idiomatic middleware path** gets a request that is silently
> unauthenticated"

spec §5 test row 11: *"**fiber-specific**: middleware using `c.Locals` ⇒ **401**"*.
Plan Task 9: *"⚠ **This is the adapter with the trap.**"* — and plan Task 8 (gin) prescribes only
the working idiom, with **no** trap test.

**The failure:** the trap is not a fiber quirk. It is the general property that a framework's
**native per-request key/value channel is not the request `context.Context`**, and gin has exactly
such a channel — `gc.Set` / `gc.Get`, backed by `Context.Keys`. `transport/http/gin/groups.go`
hands `httpcore` `gc.Request.Context()`, which `gc.Set` never touches. A gin auth middleware
written the way gin auth middleware is almost always written —
```go
r.Use(func(gc *ginlib.Context) { gc.Set("actor", a); gc.Next() })
```
— produces a request `httpcore` sees as unauthenticated ⇒ **401**. Fail-closed, exactly like the
fiber case, and exactly as surprising. Arguably worse: `gc.Set`/`gc.Get` is more widely used than
fiber's `c.Locals`, and the bundle actively tells the reader gin is "standard", which is an
invitation not to check.

**Evidence:** executed in the worktree at `7fa756d0`, `transport/http/gin/zzprobe_test.go`
(throwaway, deleted after the run), reading the value the way `transport/http/gin/groups.go` does:

```
PROBE gc.Set (gin's idiom)     gc.Request.Context().Value(structKey)=<nil>  .Value("actor")=<nil>   gc.Get=from-middleware
PROBE gc.Request.WithContext   gc.Request.Context().Value(structKey)=from-middleware  .Value("actor")=<nil>   gc.Get=<nil>
--- PASS: TestZZProbeGinContextPropagation (0.00s)
ok  github.com/kartaladev/wrkflw/transport/http/gin  0.832s
```

Same shape as the bundle's own fiber measurement, on the adapter the bundle calls standard. Note
`gc.Set` and `gc.Request.Context()` are *mutually invisible* in both directions — the second row
shows `gc.Get` returning `<nil>` for a value placed on the request context.

**Concrete fix:**
1. Correct spec §2.2. The sentence to write is not "fiber is not standard" but *"two of the three
   adapters expose a framework-native per-request store that does NOT reach the request context —
   fiber's `c.Locals` and gin's `gc.Set`. Only `stdlib` has one channel."* Re-derive the claim
   rather than restating it; §2.2 is currently the premise the whole example package rests on.
2. Promote spec §5 row 11 from "fiber-specific" to two rows, and give plan **Task 8** the gin twin
   of Task 9's `TestTaskRoutes_LocalsDoesNotAuthenticate` — e.g.
   `TestTaskRoutes_GinSetDoesNotAuthenticate`, asserting 401 for a `gc.Set`-based middleware, with
   the same "if this ever returns 403, gin changed" comment.
3. `examples/authenticated_tasks/` (ADR §Decision 4) must carry the warning in its **gin** section
   too, not only its fiber one — the ADR currently justifies the whole example package on the
   fiber trap alone.
4. The `gin.WithRequestActor` alias godoc (plan Task 6 Step 1: *"Adjust the godoc's middleware
   sentence per framework — and for **fiber** it must say `c.SetContext`, never `c.Locals`"*) needs
   the same instruction for gin: it must say `gc.Request = gc.Request.WithContext(...)`, never
   `gc.Set`.

---

### F10 — `SECURITY.md`'s "Scope notes for embedders" gains its most important entry and the plan does not name it — [MINOR]

**Bundle text attacked:** plan Task 12 Step 1:
> "`SECURITY.md` gains the middleware pattern for all three frameworks, the 401/503 contract, and
> the `c.SetContext`-not-`c.Locals` warning."

**The failure:** `SECURITY.md` has a dedicated, enumerated section — **"## Scope notes for
embedders"** (`SECURITY.md:32-44`) — listing the responsibilities that "sit with the embedder and
are documented rather than enforced by default". It has exactly three entries today: admin-route
authorization, TLS, and untrusted definitions. After ADR-0189, **authenticating task requests** is
the newest and most consequential member of that list, and the plan's instruction ("gains the
middleware pattern") does not say to add it there — a subagent will reasonably append a new
free-standing section instead, leaving the canonical enumeration incomplete.

Related, and worth telling the implementing agent explicitly: `SECURITY.md`'s entire tail from line
112 to the end of the file is the **generated, machine-checked** at-rest block
(`<!-- BEGIN at-rest (generated) -->` … `<!-- END at-rest -->`, ADR-0187), guarded by
`internal/atrest/render_test.go:419 TestSecurityMdInSync`. New prose must go **above** line 112.
I read the guard: `atrest.ReplaceBlock` substitutes only between the markers and the assertion then
compares whole files, so an edit outside the block is safe and an edit inside it fails the build.
The plan should say which half is which rather than leaving an agent to discover it via a red suite.

**Evidence:** `grep -n "BEGIN at-rest\|END at-rest" SECURITY.md` → `112` / `244`; `tail -5
SECURITY.md` ends at the END marker, so "append to SECURITY.md" lands after the generated block.
`sed -n '32,44p' SECURITY.md` shows the three-item embedder list. Also confirmed for F2's benefit:
`SECURITY.md:125` classifies class `actor` as *"identifies a human principal. **Treat as personal
data.**"*, and `:174` assigns `wrkflw_human_task.claim_actor` — the `{roles, attributes}` column —
that class.

**Concrete fix:** name the four SECURITY.md edits in Task 12 Step 1: (a) a new bullet in **Scope
notes for embedders**; (b) the middleware pattern per framework (with F9's gin correction);
(c) the 401/503 contract; (d) explicitly, "insert above line 112 — the at-rest block below the
`BEGIN at-rest` marker is generated and machine-checked by `TestSecurityMdInSync`."
### F11 — the refusal matrix is presented as exhaustive and omits two resolver behaviours: a panic and a mutation-visible read — [MINOR]

**Bundle text attacked:** ADR-0189 §Consequences/Positive:
> "Forgetting the seam fails closed at **every** entry: no middleware ⇒ 401; nil resolver ⇒ 401;
> resolver error ⇒ 503; empty ID ⇒ 401. **There is no path that yields a zero actor.**"

**(a) A panicking `RequestActorFunc` is unhandled, with adapter-dependent blast radius.**
`RequestActorFunc` is arbitrary consumer code called on the hot path, and there is no `recover()`
anywhere in the transport:
```
$ grep -rn "recover()" transport/ | grep -v _test
(no matches — EXIT=1)
```
Consequences differ per adapter and the bundle names none: under `stdlib`, `net/http`'s Server
recovers per connection (the request dies, the server lives); under `gin`, only if the consumer
mounted `gin.Recovery()` — `ginlib.New()` installs none, and the repo's own adapter does not add
it; under `fiber` v3 the recover middleware is likewise opt-in. So on a `gin.New()` deployment a
resolver panic takes the process down.

The literal claim survives (a panic is not a zero actor), which is exactly why this is MINOR rather
than a contradiction — but a matrix printed as four exhaustive rows should say what the fifth row
does. In fairness this is a **pre-existing class**: `Wrap`, `InstanceMapper` and the `slog.Handler`
are consumer callbacks with the same exposure. The bundle joins the class rather than creating it.

**(b) `ActorFromContext` clones on the way IN but not on the way OUT.** Plan Task 1 Step 3:
```go
func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a.Clone())     // cloned
}
func ActorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorContextKey{}).(Actor)                   // NOT cloned
	return a, ok
}
```
`authz.Actor.Clone` is documented as one level deep (`authz/authz.go:41-49`: *"Attributes are
cloned one level deep: nested maps and slices …"*), so the isolation guarantee the godoc states —
*"a later mutation by the caller cannot reach the engine"* — holds only for (i) the direction it
was written for and (ii) the top level of `Attributes`. A caller who does
`a, _ := authz.ActorFromContext(ctx); a.Roles[0] = "admin"` mutates the context's stored actor for
every later reader in that request, and a nested `map[string]any` inside `Attributes` is shared in
both directions regardless. For a request-scoped value the practical blast radius is one request,
which is why this is MINOR — but the godoc as drafted over-promises, and the prescribed test
(`TestContextWithActorClonesTheActor`) only exercises the write direction, so nothing would catch a
future refactor that dropped the read-side assumption.

Related and worth one sentence in the ADR: `ContextWithActor` is exported and last-writer-wins, so a
middleware mounted *after* the authenticating one can replace the actor with no way for `httpcore`
to tell. That is ordinary `context` semantics and almost certainly the right design — but the ADR
sells the seam on "the transport reads it from nowhere else", and the complement ("anyone in the
consumer's own chain may write it, and the last write wins") is not stated anywhere.

**Concrete fix:** (a) add a fifth matrix row — *"the resolver panics ⇒ unhandled; mount your
framework's recovery middleware"* — and say it in `RequestActorFunc`'s godoc. (b) either clone on
read too (`return a.Clone(), ok` — one call, and it makes the godoc true), or narrow the godoc to
the direction it actually guarantees and add the read-direction case to the prescribed test.
Cloning on read is the smaller change and the safer default for a security seam.

---

## Verified clean (checked, no finding — recorded so the controller knows the ground was covered)

- **The `%w` payload cannot reach any other consumer of the error.** The only `errors.Is` on the
  transport path outside `ClassifyError` is gin's body-cap check
  (`transport/http/gin/groups.go:293`, `errors.Is(err, httpcore.ErrRequestBodyTooLarge)`), which the
  identity sentinels cannot match. No retry, idempotency or dedup logic inspects an error returned
  from `ClaimTask`/`CompleteTask`/`ReassignTask`. §3.4's arms-first ordering is therefore sufficient
  as well as necessary.
- **No metric-cardinality or label-injection angle.** `Instrumentation.Observe` labels on
  `http.method`, `http.route` (static — "never reads `r.Pattern`") and `http.status_code`
  (`transport/http/httpcore/observability.go:96-104`). The two new statuses add two bounded label
  values and nothing actor-derived reaches a label.
- **A resolver returning `(validActor, someError)` fails closed.** The `switch` in §3.3 tests `err`
  before `a.ID`, and both error arms return `authz.Actor{}` explicitly, discarding the actor. The
  ordering is correct as drafted.
- **401 floods are not a logging DoS.** All three `writeErr` implementations guard on
  `status >= 500`, so the 401 path writes no log line. (The 503 path does — that is F5.)
- **`ClassifyError`'s default arm is 500 with an empty body**, so nothing this bundle adds can leak
  an unclassified error's text to a client.

---

## Verdict

**The bundle does NOT survive as an input to implementation in its current form.** One finding is a
security regression the bundle advertises as a benefit, and it must be adjudicated before any code
is written; two more (F8, F9) are premises in the spec that execution shows to be false, and a
plan built on a false premise produces the wrong tests.

| severity | count | findings |
|---|---|---|
| CRITICAL | 1 | F2 — newly-flowing `Actor.Attributes` are rendered verbatim to unauthenticated callers by `GET /instances/{id}/actionable` and `/snapshot` |
| MAJOR | 7 | F1 (empty-ID rule deletes ADR-0148's kiosk claimant, citing neither 0148 nor 0183) · F3 (unbounded resolver I/O; repo convention ignored twice over) · F4 (actor validated on one property; ADR-0186's cap no longer bounds this path) · F5 (bad credential defaults to 503 + attacker-paced ERROR log labelled "internal error") · F6 (no CHANGELOG/STABILITY entry for the bundle's own BREAKING change) · F7 (`WithRequestActor` silently no-ops on 3 of 4 route groups; the "typing a role name" Positive is false where AdminRoutes is reachable) · F8 (the demo-actor remedy takes the three reference mains from 403 to 200) |
| MINOR | 2 | F10 (SECURITY.md "Scope notes for embedders" entry unnamed; generated-block boundary unstated) · F11 (matrix omits panic; clone is asymmetric and the godoc over-promises) |

**Blocking, in order:**

1. **F2** — the ADR cannot ship "`Actor.Attributes` reaches the authorizer" as an unqualified
   Positive when the same change publishes those attributes to anonymous callers on two sibling
   routes the same `Mount` registers, and when `SECURITY.md:125` already classifies that column as
   *"personal data"*. Adjudicate before implementation: redact in the curated view, or state the
   hazard in the ADR, the CHANGELOG and the `RequestActorFunc` godoc. Do not leave it as an
   undocumented residual — it is not currently documented at all.
2. **F8 and F9** — both are executed refutations of spec premises (§2.5 "gets 401, which §3.5
   addresses"; §2.2 "gin … standard"). Fix the premises before Tasks 8, 9 and 11 are dispatched,
   because each currently prescribes the wrong test or the wrong example.
3. **F1** — decide, in writing, whether the kiosk claimant survives over HTTP. Rule 3's stated
   rationale is contradicted by ADR-0183, which is *newer* than the ADR-0147 sentence the bundle
   rests on. This is the second dangling citation in a rule §1.1 congratulates itself for having
   de-dangled once already.
4. **F3, F4, F5, F6, F7** — each is a concrete, small, additive fix. None requires re-cutting a
   decision; all require the ADR to say something it currently does not.

**What the bundle gets right, and I want on the record:** the arms-first ordering in §3.4 is
correctly derived and its "arbitrary consumer payload ⇒ first" generalisation is stronger than the
403-only framing it replaced; the 503-not-403 choice for `authz.ErrNotAuthorized` is right for the
reason given (a 403 is an audited verdict about a known principal); the fail-closed nil-resolver
rule and the `WithRequestActor(nil)`-restores-the-default reading are both correct; and the
`WithActorResolver` name collision was caught and is real. My findings are about what happens
*around* those decisions, not to them.

**One process note, per CLAUDE.md rule #9's interaction clause:** this revision changes more than
one decision relative to ADR-0185 D1 (the endpoint signature, `Attributes` flowing, two new
sentinels, and the **removal** of `WithAnonymousActorAllowed`). F1 and F8 are both interaction
findings — each is a hole the *removal* opened in a decision that survived. That grid is the
interaction lens's job, but two of its cells fell out of a failure-mode read, which suggests the
grid is worth deriving explicitly rather than trusting to overlap between lenses.
