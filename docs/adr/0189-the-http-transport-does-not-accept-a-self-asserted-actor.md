# 189. The HTTP transport does not accept a self-asserted actor

- Status: **Accepted — implemented 2026-08-26** on `feat/request-actor-identity`. Verification
  passed repo-wide (`go test -race ./...`, `golangci-lint run ./...`, touched packages ≥ 85 %).
  ⚠ **Its lineage failed THREE rule-#9 audits before this record was cut to one decision**
  (48/7C, 58/15C, 59/19C — `audit{,2,3}-0189-adjudication.md`). The owner closed the audit phase and
  directed the seven round-3 Criticals be fixed and the delivery proceed to the review gates.
  ⭐ **Do not reuse that lineage's Criticals-per-lens as a health metric**: scope went 2 → 9 → 1
  decisions while the number went 1.75 → 3.75 → 4.75, so it tracked the *instrument*, not the
  bundle. Each round's lenses were briefed with the previous round's findings, and the documents
  grew every round.
  - Author's grids: `audit-0189-author-interaction-grid.md`, `audit2-0189-removal-grid.md`.
- **Split out to ADR-0190 (not designed here):** route-group authentication for
  `InstanceRoutes`/`MessageRoutes`/`AdminRoutes`, and the admin authorization posture. ⚠ 0190 must
  argue against **ADR-0095 §"Admin-by-composition (default-absent)"**, which states that
  default-absent *"replaces the old default-deny (403) … this is safer"* — a decision round 2 found
  this bundle contradicting without ever citing it.
- Date: 2026-08-25
- **Amends: ADR-0147 amendment #5**, first caveat. It states *"the HTTP transport's
  `httpcore.Actor` is `{id, roles}` only — so over HTTP those two slots can never carry
  attributes … Phase 8's whole-document test must therefore build its fixture through the Go
  API, not the transport."* This record **falsifies that sentence and deletes the type it
  names**. The caveat must be annotated in place. (Round 1's ADR said *"Amends: nothing"*;
  execution-lens F1 caught it.)
- **Relates to:** ADR-0117 (its Decision 1 defers the open case *"to the consumer's transport
  layer"*), ADR-0148 amendment 1 §4 **as the repo reads it** — the *kiosk claimant* blessing lives
  in `humantask/validate.go:24` and `validate_test.go:45-47`, **not** in ADR-0148's own text, which
  contains neither "kiosk" nor "anonymous" (see Decision 3), ADR-0183 (which declined to supersede
  that shape), ADR-0186 (the option-alias convention, and the body cap whose ordering Decision 6
  records), ADR-0187 (the at-rest classification — **unchanged by this record**, verified twice),
  **ADR-0095** (admin-by-composition — why the admin posture is ADR-0190's problem, not this
  record's), ADR-0094 (mountable transports).
- **Supersedes-in-part:** ADR-0185 **Decision 1 only**. ADR-0185 stays Proposed-and-failed for
  its D2/D3. ⚠ `docs/plans/HANDOVER.md` and ADR-0185's own banner must be updated in this
  bundle, or a fresh session is still routed into the deleted D1 design (interaction-lens F8).
- Backlog: closes **51** for the three human-task verbs. Explicitly still open: **52**, **53**,
  **62**, **90**, **103**, **124**. Opened here as new items, all to be filed **on the day this
  ships**: (i) the deny-list actor-attribute fail-open measured in Decision 5; (ii) actor
  attributes rendered to unauthenticated readers by `GET /instances/{id}/actionable` and
  `/snapshot` — see Consequences; (iii) route-group authentication and the admin posture, which
  become **ADR-0190**.
- Spec, with the executed evidence: `docs/specs/2026-08-25-request-actor-identity.md`.
  Plan: `docs/plans/2026-08-25-request-actor-identity.md`.

## Context

`transport/http/httpcore` builds the `authz.Actor` that reaches the authorization layer
**from the request body**. Executed — these are the only three `authz.Actor` constructions
anywhere in `transport/`, non-test:

```
transport/http/httpcore/endpoints.go:119   Actor:  authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles},
transport/http/httpcore/endpoints.go:132   Actor:   authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles},
transport/http/httpcore/endpoints.go:150   By:     authz.Actor{ID: in.By.ID, Roles: in.By.Roles},
```

⇒ any caller can post `{"actor":{"id":"alice","roles":["manager"]}}` and be believed by
`ClaimTask`, `CompleteTask` and `ReassignTask`. Roles are self-asserted, so a role-based
`AuthzSpec` is satisfied by **typing the role name**. `CustomizeConfig` declares eight fields
and **no identity seam**, so a consumer's authentication middleware has no supported way to
override it. The authorization mechanism CLAUDE.md calls load-bearing is evaluated against a
principal the attacker chose.

**No other route group authenticates anything either**, and **this record does not change that.**
`InstanceRoutes`, `MessageRoutes` and `AdminRoutes` continue to serve any caller; that is
**ADR-0190's** subject. ⚠ For `AdminRoutes` the posture is deliberate, not accidental: **ADR-0095
§"Admin-by-composition (default-absent)"** chose absence over a built-in default-deny and calls it
*"safer"*, `stdlib.Mount` excludes the group, and `examples/production_wiring:273-275` composes a
fail-closed token guard in front of it. An earlier revision of this record reintroduced the
default-deny ADR-0095 removed, without citing it.

Two further consequences of the body-derived design:

- **`Actor.Attributes` is dropped at all three sites**, so actor-attribute predicates cannot be
  satisfied over HTTP. ⚠ That is **not** the same as failing closed — see Decision 5.
- **The audit trail records the asserted identity.** ADR-0147 renders `claim.actor` and
  `completion.actor` by faithful passthrough into `wrkflw_human_task` and
  `wrkflw_instances.snapshot`, so a forged actor becomes a durable false record.

This is an unclosed hand-off: ADR-0117 Decision 1 deferred *who the actor is* to "the
consumer's transport layer", and that is only sound if the transport layer has an identity.

## Decision

### 1. The actor travels in the `context.Context`; the transport reads it from nowhere else

```go
func ContextWithActor(ctx context.Context, a Actor) context.Context
func ActorFromContext(ctx context.Context) (Actor, bool)
```

Plain functions in `authz`, stored under an unexported struct key. `ClaimInput.Actor`,
`CompleteInput.Actor`, `ReassignInput.By` and the `httpcore.Actor` type are **removed**;
`ReassignInput` keeps `From`/`To`, which name task participants rather than the requester.

**A body still carrying `"actor"`/`"by"` is IGNORED, not rejected** — executed on real mounted
routes for all three adapters; no `DisallowUnknownFields` exists in `transport/` or `internal/`.
⚠ For gin this is **conditional on a global the consumer controls**
(`EnableDecoderDisallowUnknownFields`); nothing in this repo sets it, but the guarantee is the
library's only where the library owns the setting (counting-lens F8).

⚠ **The clone guarantee is one level deep, and the record says so.** `Actor.Clone` clones
`Attributes` shallowly by its own godoc, so a nested map inside an attribute value stays shared.
`ContextWithActor` clones on the way **in** and `ActorFromContext` clones on the way **out**;
neither makes nested values private. Claiming full isolation would be false for exactly the
payload Decision 5 admits.

### 2. Resolution happens once, in `httpcore`, through a consumer-supplied function

```go
type RequestActorFunc func(context.Context) (authz.Actor, error)
func WithRequestActor[R any](fn RequestActorFunc) CustomizeOption[R]
// + the three REQUIRED non-generic aliases: stdlib. / gin. / fiber.WithRequestActor
```

The three task endpoints take the resolver as a **parameter**; the nine adapter call sites each
gain one argument and no branch. Default is `authz.ActorFromContext`, installed in
`ResolveConfig`'s post-loop nil-guard block, so `WithRequestActor(nil)` restores the fail-closed
default.

**The name is `WithRequestActor`, not `WithActorResolver`** — the latter is already exported
three times (`service`, `runtime/task`, `processtest`) for the opposite concept, *"who could
act"*.

**There is no `WithAnonymousActorAllowed`.** An open deployment states its own identity in three
lines. The library never picks a sentinel that would land in a durable audit record.

### 3. An unresolved identity is refused, never downgraded — and the rule targets the zero actor

| condition | result |
|---|---|
| nothing authenticated the request (`ok == false`) | **401** `ErrUnauthenticated` |
| `RequestActorFunc` is nil (a hand-built `CustomizeConfig`) | **401** — forgetting the seam fails CLOSED |
| the resolver returns an error | **503** `ErrIdentityUnavailable`, wrapping it |
| the resolver returns the **zero actor** (no ID, no non-empty role, no attributes) | **401** |
| `Actor{ID: "", Roles: ["kiosk"]}` — the kiosk shape | **passes** |

⚠⚠ **This rule has now been wrong TWICE and is re-derived from what it must PREVENT.**

- **Round 1** refused any empty `Actor.ID`, deleting the *kiosk claimant* — a shape
  `humantask/validate.go:24` calls *"deliberately accepted … anonymous but carrying roles"*, pinned
  at `validate_test.go:45-47`.
- **Round 2** accepted everything, including `Actor{}`.
- **Round 3's "at least one dimension"** was justified by two properties **it does not deliver**.
  Measured against the real `RoleAuthorizer`: `{Roles:[""]}` and `{Attributes:{"x":nil}}` pass and
  are exactly as unattributable and `AssignedTo("")`-invisible as `Actor{}` — **and so is the
  blessed kiosk shape**, so unattributability cannot be the distinguishing harm. The deny-list
  fail-open closed in **1 of 8** shapes. It also refused `Roles:[]string{}` while admitting
  `Roles:[]string{""}`.

⇒ **the rule prevents exactly ONE thing and claims nothing else: the ignored-error signature.**
`actor, _ := authenticate(r)` yields `Actor{}` with a nil error, and the request would otherwise
proceed as though authenticated. That is a silent failure of the consumer's authentication, and the
library can spot it for free because the zero value with `err == nil` is its exact fingerprint.

**An empty string is not a role.** `strings.Split("", ",")` returns `[""]` — length 1, what the
canonical header middleware yields for a header-less request — so empty role strings are ignored
when deciding zero-equivalence. Executed across eight shapes: the predicate matches intent on every
one, and `Roles:[]string{}` and `Roles:[]string{""}` are now treated alike.

⛔ **What this rule does NOT do, stated so no later reader infers it:** it does not make the actor
attributable, it does not close the attribute fail-open, and beyond that single fingerprint it does
not distinguish a deliberate anonymous principal from a careless one. Both are separate and filed.

⚠ **Provenance correction, carried forward:** `docs/adr/0148-*.md` contains **no** "kiosk" and no
"anonymous". The term and the blessing are the **repo's own**, in `humantask/validate.go` and its
test, citing an ADR-0148 amendment section itself titled *"The interim state fabricates a claim"*.
Round 2 inherited that citation from round 1's audit and restated it without re-deriving.

⇒ this record's promise is *"the request carries a resolved, non-zero actor"*. ⛔ It is **not**
*"an identified principal"*, and no sentence here may say so.

**A resolver-call bound is adopted**, `RequestActorTimeout` / `WithRequestActorTimeout`, default
**10 s**, non-positive disables — mirroring the engine's `WithCandidateResolveTimeout`
(`runtime/task/service.go:139-141`), which bounds the sibling "expand an eligibility spec into
actors" call. On fiber the need is not hypothetical: `c.Context()` is `context.Background()` when no
middleware set one, so the resolver would otherwise run with no deadline and no cancellation at all.

⚠ **The bound is COMPOSED INTO the resolver by `ResolveConfig`, not carried beside it.**
🔧 **Corrected during implementation (rule #11), and the audit had predicted it.** The first
implementation put the timeout on `CustomizeConfig` and passed a hardcoded `0` at all three endpoint
call sites — because Decision 2 gives the endpoints the resolver as their *only* added argument and
no sight of the config. The option was therefore **inert at every adapter**: configured, defaulted,
aliased three times, and read by nothing. **Two implementers found it independently by
source-verification**, and round 2's interaction lens had already named it (F5: *"either
`WithRequestActorTimeout` is silently dead on the three task routes, or the thrice-repeated 'one
added argument, no branch' claim is false"*) — accepted, then not folded.

⇒ `ResolveConfig` wraps `cfg.RequestActor` in the deadline **after** the option loop, so the two
options compose in either order and the endpoints stay at one added argument; `resolveRequestActor`
takes no timeout parameter at all. Pinned by four tests, one of which — *"the 10 s default is
composed, not just recorded"* — is exactly the test the original code would have failed.

⚠⚠ **It bounds only a resolver that HONOURS `ctx` cancellation.** Measured: a ctx-ignoring resolver
ran **1.5 s against a 200 ms bound and returned `err == nil`**, so the request proceeded with an
actor obtained after the deadline. The cited precedent carries that caveat in its own godoc and an
earlier revision **stripped the hedge when restating it**. The hang is **narrowed, not closed**, and
`WithRequestActorTimeout`'s godoc says which.

⚠ A test for a deadline must not fail by *hanging*. Removing the composition originally made these
tests block forever (`go test` EXIT=124) rather than fail; the ctx-honouring fixture now gives up
after a bounded wait and **succeeds**, so the assertion fails readably in ~2 s. A test whose failure
mode is a hang stalls CI and reports nothing.

### 4. Both new sentinels are classified FIRST in `ClassifyError`

`ErrIdentityUnavailable` wraps **arbitrary consumer code's** error with `%w`, so it can
co-match *any* arm — including the 404 arm that is currently first. The general rule, pinned by
test: **an arm whose sentinel wraps caller-supplied errors must precede every arm its payload
could match.** For an arbitrary payload, that means first.

Consequence, deliberate: a resolver returning `authz.ErrNotAuthorized` classifies **503, not
403**. An identity resolver answers *who*, not *may*; a 403 is an audited decision about a
**known** principal.

⚠ The two new arms can also co-match **each other** (an `ErrUnauthenticated` wrapped by a
resolver error). `errors.go`'s standing invariant demands a test for exactly that pair, and
round 1 violated it for its own arms — the test ships here.

### 5. `Actor.Attributes` flows, behind a DEPTH BOUND, a size bound and a deep copy

The whole `authz.Actor` reaches the engine. ⚠ **This CLOSES a live fail-open**; round 1
recorded it as a cost, with the sign inverted. Measured against `RoleAuthorizer`:

| predicate class | today (attributes dropped) | after this record |
|---|---|---|
| deny-list `actor.Attributes.status != "blocked"` | **ALLOW** ← live fail-open at `main` | **DENY** |
| allow-list `actor.Attributes.status == "active"` | DENY (vacuously) | satisfiable |

⚠⚠ **State the size of this correctly — the table is one predicate, not the class.** Measured
across eight attribute shapes, flowing attributes closes the deny-list fail-open in **1 of 8**;
the other seven still ALLOW. ⇒ this makes the predicate *satisfiable*, **not safe**.
⚠ And *"distinct from backlog 103"* distinguishes the **root**, not the **mechanism**:
`vars.status` over empty vars and `actor.Attributes.status` with the key absent are byte-identical
ALLOWs. The fail-open is **live today**, is narrowed rather than closed, and is filed as its own
backlog item.

**The guard is an explicit DEPTH BOUND plus a marshal, and the seam DEEP-COPIES.** This guard has
now been wrong twice, by the same category of error one layer apart:

- **Round 2** used `json.Marshal` alone. `encoding/json`'s encoder has **no nesting limit** while
  its decoder caps at **10000**, so a 20000-deep attribute passed, wrote durably, and broke
  `HumanTaskStore.Get` forever.
- **Round 3** round-tripped `Attributes` alone — but the durable document nests them **inside** an
  `Actor`, and there is **no single stored shape**: `claim_actor` marshals an `Actor`, `candidates`
  marshals `[]authz.Actor`, the instance snapshot is deeper still. Measured: the guard admitted
  depth 9999 where the store admitted 9998, reproduced end-to-end on a real SQLite store with this
  record's own verbatim error text. A size bound cannot help — the cheap payload is ~20 KB.

⇒ **bound the depth explicitly.** That is independent of every storage shape, which matching "the"
stored shape is not:

```go
const maxActorAttributeDepth = 64       // leaves 10000-64 = 9936 levels for ANY wrapper
const maxActorAttributeBytes = 16 << 10 // 16 KiB marshalled
```

⚠ Round 3 referenced `maxActorAttributeBytes` twice and **never gave it a value**; it has one now.

**One walk** over the attributes computes the depth **and produces a typed deep copy**, bailing at
the budget — which also makes it terminate on a cyclic structure. The copy is then marshalled,
which catches unmarshalable values (`chan`, `func`, cycles) and yields the size to bound. Executed:
depth 64 passes and 65 is refused, while the store survives a 64-deep attribute at wrapper depths
of 1, 10, 100 and 1000.

⚠⚠ **The deep copy closes an uncatchable process crash.** `Actor.Clone` is one level deep by
design, so a consumer's nested attribute map stays **shared**, and marshalling it on every request
iterates a map the consumer may still be writing. Executed:
`fatal error: concurrent map iteration and map write` — which **`recover()` does not catch**, so
the whole process dies. New over HTTP, because today's body-derived actor carries no attributes at
all. Copying first means the marshal reads only our private copy.

⚠ **The copy is TYPED, not a marshal/unmarshal round trip.** Executed: a round trip silently
converts `int → float64` and `time.Time → string`, changing what the `expr` authorizer evaluates.
So the copy reproduces `map[string]any` and `[]any` recursively and leaves other values shared.
`RequestActorFunc`'s godoc states that the library reads the returned actor's attributes once and
that a consumer must not mutate them concurrently. ⚠ That contract is **not new** — the existing
`humantask.ActorResolver` → candidates → task-store path already reads consumer-supplied attribute
maps the same way. It is newly *written down*.

Both failures classify **503 `ErrIdentityUnavailable`**: the fault is the consumer's *resolver*,
which the HTTP caller cannot correct.

⚠ **Ordering is deliberate: Decision 3 runs BEFORE this guard.** An actor whose *only* dimension is
its attributes is admitted by Decision 3 and can then be rejected here. It must yield **503**
(*"your resolver produced something we cannot store"*), never 401 (*"you are not authenticated"*) —
two different facts. Both orderings are pinned by test.

⚠ **The guard runs OUTSIDE the resolver timeout**, because it walks the actor the resolver already
returned. ⇒ **the size bound is load-bearing, not decoration**: without it, a 16 KiB, 64-deep
payload would be walked and marshalled on every request with no bound of its own. This is why
leaving `maxActorAttributeBytes` undefined was worse than it looked.
cannot correct, and `errors.go` documents twice that a caller-uncorrectable fault stays 5xx.

⭐ **Lesson recorded because it recurred twice in this bundle: a guard tested with a fixture from
the half that works is not tested.** Round 2's only fixture was `chan int` — the arm that works.

⚠ **This is NOT a new exposure class, and the record must not claim credit or blame for one.**
Executed: an embedded consumer calling `svc.ClaimTask` with attributes **already** persists them
into `claim.actor.attributes` today, and `wrkflw_human_task.candidates` is **already**
`ClassActor` carrying resolver-supplied attributes (ADR-0147). ⇒ **ADR-0187's at-rest
classification is unchanged and needs no amendment** — a sibling `ClassActor` column already
carries this exact payload. What changes is that the HTTP path stops being *accidentally
narrower* than the embedded one.

### 6. The claim route accepts an ABSENT body; the ordering residual is stated, not fixed

`ClaimInput` becomes a **zero-field struct**, so a correctly-migrated client sends no body.
Executed on a real mounted route: a bodyless POST returns **`400 bad input: EOF`**. ⇒ the claim
route must decode an **optional** body. ⚠ Scoped to the claim route alone: `CompleteInput` and
`ReassignInput` keep required content, and a group-wide helper would make `POST /instances`
accept an empty body and fail later with a worse error.

✅ **Authentication resolves BEFORE the body is read.**
🔧 **Corrected at the delivery gate (rule #11).** Earlier revisions of this record resolved identity
*inside* the endpoint, after the adapter had decoded the body, and stated that as an accepted
residual: an unauthenticated caller could force a full `MaxBodyBytes` read (1 MiB default) and hold
a handler for `BodyReadTimeout` (30 s default) before receiving its 401 — an unauthenticated
resource-consumption primitive **on the only routes that authenticate**. `/code-review` refused that
residual, correctly: this repo's own rule is that a documented residual is still a shipped defect,
and the premise the residual rested on ("those routes are unauthenticated anyway") stopped holding
the moment this record authenticated them.

⇒ `httpcore.RequestActor(ctx, resolve)` is **exported** and each adapter calls it at the top of the
task handler, before any decode; the three endpoints now take an already-resolved `authz.Actor`.
⚠ **The 401/503 policy still lives in exactly one place** — the nine adapter sites duplicate a
two-line *call*, never the decision, which was the sole objection to resolving adapter-side.
⚠ The endpoints keep a **zero-actor guard** as defence in depth, so a consumer-written adapter that
skips the call cannot pass an unauthenticated request through.

**⇒ The ordering on the three human-task routes is `401 → 413 → 400 → 404`.** It was
`413 → 400 → 401 → 404`. Both are pinned per adapter, and inverting the resolution point in one
handler makes exactly that handler's row fail (mutation-verified in all three adapters).
⚠ Tests that are ABOUT the body (`TestEveryDecodeSiteIsBounded`, the `*_BadJSON` pair) must now
mount an actor to reach the arm they test — the actor's identity is incidental, its presence is not.

⚠⚠ **A malformed claim body now answers 401, NOT 400 — and that IS a change.** An earlier revision
of this record asserted 400 *"unchanged from today"* within a few lines of a residual asserting
401. Executed against the real optional decoder: the optional path swallows the decode error, so
resolution refuses first and the answer is **401**. It is stated as 401 everywhere in this bundle.
⚠ The **413 is NOT swallowed** — `stdlib`'s `decodeOptionalRequestBody` preserves it, verified — so
ADR-0186's cap keeps its response contract here. gin's and fiber's equivalents do not exist yet and
must preserve it too, with a test each.

### 7. The seam is demonstrated in runnable code

`examples/authenticated_tasks/` shows middleware → `ContextWithActor` → claim, for **all three
adapters**, because the context trap is **not** fiber-specific:

| adapter | idiom that REACHES `httpcore` | idiom that does NOT |
|---|---|---|
| stdlib | `r.WithContext(...)` | — |
| gin | `gc.Request = gc.Request.WithContext(...)` | **`gc.Set`** ← gin's canonical idiom |
| fiber | `c.SetContext(...)` | **`c.Locals`** ← fiber's canonical idiom |

Both misses are measured. Round 1 called the trap fiber-specific and gin "standard"; both were
wrong. ⚠ On fiber, `c.Context()` is `context.Background()` when no middleware set one, so the
consumer's resolver runs with **no deadline and no cancellation** unless the bound in Decision 3 is
armed — which is why that bound exists.

The three wiring mains take a constant demo actor. ⚠ That makes them answer **200** where they
answer **403** today — *strictly more open* — so the actor is named `demo-user`, the comment says
DEMO ONLY, and the mains do **not** mount `AdminRoutes`.

## Consequences

### Positive

- The principal on the three human-task verbs is supplied by the consumer's authentication, not by
  the attacker. A role-based `AuthzSpec` stops being satisfiable by **typing the role name**.
- The audit trail records an identity someone vouched for. Given ADR-0147's passthrough into two
  durable copies, that is a data-integrity fix as much as an access-control one.
- **A live fail-open closes** on the actor root: deny-list `actor.Attributes.*` predicates
  currently ALLOW over HTTP. ⚠ Stated precisely — see the first Negative.
- Attribute-based authorization over actor attributes becomes reachable over HTTP for the first
  time; the HTTP path stops being lossier than the embedded API.
- Forgetting the seam fails closed at every task-verb entry: no middleware ⇒ 401; nil resolver ⇒
  401; resolver error ⇒ 503; the zero actor ⇒ 401.
- The seam is a plain `context.Context` helper in a root package — no DI, no interface, reusable by
  a future non-HTTP transport, and by ADR-0190 for the remaining groups.

### Negative / costs

- ⚠⚠ **Actor attributes reach an UNAUTHENTICATED read surface, and this record does not close it.**
  `GET /instances/{id}/actionable` and `/snapshot` render `Claim.Actor` verbatim and are mounted by
  the same `Mount` with no authorization; `SECURITY.md` classifies that column as **personal
  data**. An earlier revision closed this by authenticating `InstanceRoutes`; **that decision has
  been split out to ADR-0190, so the mitigation left with it.**
  ⚠ The channel is *pre-existing* — `ActionableTask.Candidates` already renders attributes verbatim
  per ADR-0147, and an embedded consumer already persists them — **but "pre-existing" does not make
  it costless here**: the old channel needs an opt-in `humantask.ActorResolver`, whereas the new
  one is fed by `RequestActorFunc`, which **every** HTTP consumer must configure. Same provenance,
  **materially different population rate.** Filed as a backlog item on the day this ships.
- ⚠ **The fail-open closes for the deny-list class, it does not close attribute predicates
  generally.** Measured: the deny-list predicate still ALLOWs in **5 of 6** attribute shapes after
  the change — this makes it *satisfiable*, not *safe*. And *"distinct from backlog 103"*
  distinguishes the **root**, not the **mechanism**: `vars.status` over empty vars and
  `actor.Attributes.status` with the key absent are byte-identical ALLOWs.
- **BREAKING in three ways.** Three public DTOs lose a field and `httpcore.Actor` is deleted; three
  exported endpoint functions gain a parameter, breaking consumer-written adapters; and
  `ClaimInput` becomes zero-field. **50 lines across 13 files in 6 packages** — the **member set**
  is enumerated in spec §2.6, not merely totalled.
  ⚠ **50, not 48.** §2.6's two nets — the compile ablation and the `"actor"`/`"by"` grep — are
  *both* nets for the **DTO removal**. The optional claim body (Decision 6) is a **third
  behavioural change invisible to both**, and it flips two live green tests from 400 to 401:
  `stdlib/coverage_test.go:148` and `gin/gin_coverage_test.go:184`. fiber has no equivalent, so it
  is exactly two. **This is the third round running in which a decision was added after the count
  and the count was not re-derived** — the lesson is not "count more carefully" but *every
  behavioural change needs its own net*.
  ⚠ Round 1 said 29/9/5 and called it "exhaustive": its ablation modelled one of two breaking
  changes, and its 29 was a *different set* from the inherited 29 that matched only in total.
  ⚠ Round 2's 9-decision scope would have added **~186 further failing assertions across 13 files,
  7 of them named nowhere**; the re-cut removes all of them, and that reversion is **re-executed,
  not assumed** — assuming it is what produced the round-2 Critical.
- **A CHANGELOG entry and a `STABILITY.md` note are required**, per ADR-0186's precedent.
- **Two `stdlib` tests are rewritten, not recompiled** (`errors_test.go:158`, `:190`). ⭐ Round 1
  predicted they would fail loudly rather than pass vacuously, labelled it a prediction, and the
  audit confirmed it by execution: `want 403 complete forbidden, got 401`.
- ⚠ **The 401 precedes the task lookup**, so an unauthenticated request for a *nonexistent* task
  returns 401 rather than 404. Deliberate — a 404 would confirm task existence to an
  unauthenticated caller — but it moves an existing gin assertion.
- ⚠ **The resolver timeout narrows the hang, it does not close it** (Decision 3): a resolver that
  ignores `ctx` still runs past the bound and succeeds.
- ⚠ **The timeouts compose to a 40 s worst case.** `BodyReadTimeout` (30 s default) then the
  resolver timeout (10 s default) both elapse before a task request is refused. Both defaults are
  inherited rather than invented, but an operator sizing timeouts should not have to derive this.
- ✅ **Authentication on the task routes resolves BEFORE the capped body read.**
  ✅ **now resolved BEFORE the body read** — see Decision 6. The unauthenticated
  read-window primitive is closed, and the ordering is `401 → 413 → 400 → 404`.
  ⚠⚠ **A malformed claim body answers 401, not 400.** That IS a change: the optional decoder
  swallows the decode error and resolution refuses first (executed). Two live tests assert the old
  400 and must move — see the member set.
- ⚠ **`InstanceRoutes`, `MessageRoutes` and `AdminRoutes` remain entirely unauthenticated**, so
  `POST /instances`, `/signals` and `/messages` — **state-changing** operations — are open to any
  caller, as they are today. This record narrows the human-task verbs only. **ADR-0190.**
- ⚠ **The chain is NOT closed against the engine.** `ProcessDriver.ApplyTrigger`
  (`runtime/processdriver.go:548`) *"bypasses authorization entirely"*; `engine.NewHumanCompleted`
  is exported module-root API.
- Backlog **52** (the `AllowAll` default) and **53** (an empty `AuthzSpec` means allow-all) remain
  open ⇒ this is a **narrowing, not a closure**: from *anyone can be anyone* to *anyone
  authenticated can be anyone the configured authorizer permits*.
- ⚠ **The clone guarantee is one level deep** (Decision 1), and `httpcore.MountGroups` passes no
  options so groups mounted through it rely on the context seam — its godoc must say so.
- No `WWW-Authenticate` on the 401, no `Retry-After` on the 503.

### Neutral

- **ADR-0187's at-rest classification is unchanged** — `ClassActor` already describes a column
  carrying arbitrary consumer attributes (`wrkflw_human_task.candidates`). Round 1's audit
  predicted drift here; the controller refuted it by execution and round 2's execution lens
  **independently re-verified the refutation**.
- ADR-0117 needs no amendment, but for a narrower reason than an earlier revision claimed: 0117
  defers **authorization** to the transport, and this record supplies **authentication**. ⚠ Its
  deferral stays unsatisfied until backlog 52/53 land. The earlier *"0117 becomes true rather than
  changed"* equivocated the two and is withdrawn.
- **ADR-0095 is untouched.** Its admin-by-composition posture stands; ADR-0190 must engage with it
  rather than around it.
