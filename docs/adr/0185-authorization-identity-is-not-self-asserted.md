# 185. Authorization identity is not self-asserted, and eligibility must be stated

> ## ✅ D1 HAS SHIPPED as ADR-0189. ⛔ D2 and D3 remain failed and are NOT inputs to implementation.
>
> **Decision 1 (backlog 51 — the actor must not be self-asserted) was re-cut as
> `docs/adr/0189-the-http-transport-does-not-accept-a-self-asserted-actor.md` and implemented
> 2026-08-26.** ADR-0189 supersedes-in-part this record's D1 only. ⚠ ADR-0189 dropped this
> record's `WithAnonymousActorAllowed`, and its refusal rule targets the **zero actor** rather
> than an empty `Actor.ID` — this record's version of D1 would have deleted ADR-0148's kiosk
> claimant. **Read ADR-0189, not the D1 text below.**
>
> ⛔ **D2 (backlog 52, the allow-all default authorizer) and D3 (backlog 53, the empty AuthzSpec)
> are still refuted and still open.** Each needs its own ADR. Nineteen of this bundle's 22 raw
> Criticals were D3's.
>
> ## ⛔ THIRD AUDIT FAILED — 2026-08-23. NOT an input to implementation.
>
> The re-cut three-decision bundle **failed its rule-#9 audit**: 58 findings across four lenses,
> **22 raw Criticals, 21 accepted**. **Nineteen of the 22 are D3's.**
> Decisive: `checkSpecStated` **fails the plan's own prescribed test case** (the capability
> interface reaches only one of the gate's two legs, so `WithAllowAllAuthorizer()` still stops
> meaning allow-all); the migration **corrupts** `wrkflw_definitions.definition` three ways and is
> **forbidden outright** by `TestMigrations_OneFilePerDialect`; the snapshot copy is **written back
> over** the task row on every transition, so a missed snapshot *reverts* the repair; *"when human
> tasks are configured"* **is not a state that exists**; and D3 breaks **ADR-0118's blessed manual
> task** and makes an open user task **unauthorable in YAML**.
> ⚠ **The finding count did NOT fall when the bundle was cut from five decisions to three** (58 / 38
> / 58) — the fourth consecutive data point for ADR-0186's trend.
> Adjudication: `docs/plans/sweep-evidence/audit-0185core-adjudication.md`.
> **A scope decision is pending before any further revision.**
>
> ## ⚠ RE-CUT 2026-08-23 — this record now covers THREE decisions, not five.
>
> This ADR failed its rule-#9 audit **twice** as a five-decision bundle (2026-08-20:
> 58 findings, 12 Critical; 2026-08-21: 38 findings, ~13 distinct Criticals, of which
> five were holes the first revision's own fixes opened in each other). It has been
> **re-cut by owner decision** to the three decisions the composition argument actually
> covers. The two that blocked it are **deferred, not solved**:
>
> - **Backlog 103** (the old D4, strict attribute references) — *"a syntax problem that
>   cannot be solved with syntax"*: three design rounds produced three **disjoint** hole
>   sets. Needs a different mechanism, not different wording.
> - **Backlog 124** (the old D5, the claimant guard) — needs a **per-verb authorization
>   model that does not exist**: one `Eligibility` spec serves four verbs and
>   `internal/authz/casbin/authorizer.go` applies `Privileges` unconditionally, so the
>   proposed `reassign` token would have bricked Claim, Complete and RefreshCandidates.
>
> ⛔ **Do NOT implement 103 or 124 from this record.** Both were removed *because their
> designs were refuted*. Each gets its own ADR.
>
> **Status: Proposed, but its re-cut bundle's rule-#9 audit has RUN and FAILED — see the banner above.** A bundle whose
> Decisions changed has not been audited, and these have now changed three times.
> Spec + executed evidence + the removal grid:
> `docs/specs/2026-08-23-authz-identity-core.md`.
> Prior adjudications: `docs/plans/sweep-evidence/{audit,reaudit}-b3-adjudication.md`.

- Status: **Proposed — its rule-#9 audit RAN and FAILED (2026-08-23); pending revision, and pending an owner scope decision before any revision**
- Date: 2026-08-20, revised 2026-08-21, **re-cut 2026-08-23**
- Amends: **ADR-0117 Decisions 1 and 3** — Decision 1's *"with none set, the engine gate
  is open"* and Decision 3's *"any combination (**including none**) is valid"*. Both must
  be annotated in place.
- Relates to: ADR-0081 (capability interfaces — the precedent both new capability
  interfaces here follow), ADR-0117, ADR-0118, ADR-0145, ADR-0147, ADR-0182
  (authoring-time gate precedent), ADR-0183 (the claim invariant this must not
  contradict), ADR-0187 (the at-rest classification this bundle corrects — see
  Consequences)
- Backlog: **51, 52, 53** (this record). Deferred to their own ADRs: **103**, **124**.
  Opened/found here: **141**, **142**. Explicitly still open: **90**.

## Context

`wrkflw`'s authorization requirement is role-based, resource-privilege-based and
attribute-based, evaluated at human-task nodes through a pluggable `Authorizer`. The
mechanism exists and works. What does not work is everything that decides **which actor**
and **which spec** reach it.

**Three findings are in scope here.** Each was independently verified, and each neutralises
the next.

### Finding 1 — the principal is whatever the request body says (backlog 51)

`httpcore`'s three task endpoints build `authz.Actor` from `ClaimInput.Actor`,
`CompleteInput.Actor` and `ReassignInput.By` (`transport/http/httpcore/endpoints.go:119,
132,150` — the only three `authz.Actor{…}` constructions in `transport/`, non-test).
`CustomizeConfig` (`seam.go:19-33`) declares six fields and no identity seam, so a
consumer's authentication middleware has no supported way to override it. Any caller can
post `{"actor":{"id":"alice","roles":["manager"]}}`.

`authz.Actor.Attributes` exists but is dropped at all three sites, so attribute predicates
over actor attributes cannot be satisfied over HTTP at all.

### Finding 2 — the default authorizer permits everything (backlog 52)

`service/service.go:200` defaults `c.authz` to `authz.AllowAll{}`. The construction summary
discloses it only at `slog.LevelDebug` — the level is at `service/service.go:323`; the
allow-all *label* is computed at `:316`. There is no standalone `WithAuthorizer` option —
`WithHumanTasks(taskStore, az)` is the only entry — and `DurableProvider`
(`service/durable.go:17-24`) has six methods and no `Authorizer()`, so the natural durable
wiring lands on allow-all silently.

### Finding 3 — an empty spec means allow-all, and so does a privileges-only spec (backlog 53)

`AuthzSpec`'s godoc says so verbatim (`authz/authz.go:79-86`) and `RoleAuthorizer.Authorize`
(`:124`) returns `nil` for the zero spec. Executed: the zero spec, `Roles: []string{}`,
`Roles: nil` **and** a `Privileges`-only spec all admit the **zero actor** with `err=<nil>`.
The privileges leg follows from `:119-120`, which documents `Privileges` as reserved and
**not evaluated** by `RoleAuthorizer`.

⚠ **The mixed case is worse than the privileges-only case.** A spec
`{Roles:["manager"], Privileges:["finance-task approve"]}` carries a dimension
`RoleAuthorizer` *can* evaluate, so it passes the role check and **silently discards the
privilege requirement**: any manager clears a gate the author wrote to require an explicit
grant. That failure *looks configured*, which makes it strictly more dangerous than the
empty spec.

This is not an oversight — **ADR-0117 Decisions 1 and 3 blessed it.** That deferral to
"the transport layer" is only sound if the transport layer has an identity, which finding
1 says it does not.

### The surface is wider than the failed bundle believed

**There are FIVE `Authorizer` implementations, not two** (backlog 142). Derived over
`func (…) Authorize(`, non-test: `authz.AllowAll`, `authz.RoleAuthorizer`,
`internal/authz/casbin.Authorizer`, **`casbinauthz.Authorizer`** (a *public root-package*
delegate — the one a consumer actually wires, since CLAUDE.md makes casbin the baseline)
and **`processtest.SpyAuthorizer`** (public test harness, and it **allows when
`decide == nil`**). A fix confined to `RoleAuthorizer` ships the hole to the production
configuration.

**`AuthzSpec` is durable in THREE places, not one** (executed — spec §2.1):
`wrkflw_human_task.eligibility` (the copy all four `Authorize` sites read),
`wrkflw_instances.snapshot` (via `InstanceState.Tasks[].Eligibility`), and
`wrkflw_definitions.definition` (via `NodeWire`'s three `eligible_*` fields, which is
what **mints** new specs).

### These compose

A resolved principal (1) still meets an allow-all authorizer (2); a real authorizer still
meets an empty spec (3). Fixing any one alone changes nothing an attacker experiences —
which is why these three ship as a set.

### What is deliberately NOT in scope

- **Backlog 103** — deny-list attribute predicates allow on a missing variable. Executed
  with `vars = map[string]any{}`: `vars.status != "blocked"`, `vars.blocked != true` and
  others all allow. Real, unfixed, **deferred**. ⚠ Decision 1 makes it *more* reachable —
  see Consequences.
- **Backlog 124** — completion never checks who claimed. `handleHumanCompleted` copies
  `t.Actor` into the audit record unvalidated; the four `Authorize` sites all evaluate
  `task.Eligibility`, which is set membership and cannot distinguish the claimant from any
  other eligible actor. Real, unfixed, **deferred**.
- **Backlog 90** — an eligible actor stealing another's claim on the *claim* path.
- The **stale-casbin-policy** question (a revoked permission returns `true, err=nil` after
  a failed reload). The logging/metric half shipped with item 102; the fail-closed half is
  an availability/security trade-off recorded as the owner's open decision.

## Decision

### 1. The actor travels in the `context.Context`; the transport never reads it from the body

`authz` gains the identity seam, in a public root package, usable with plain functions and
no DI:

```go
func ContextWithActor(ctx context.Context, a Actor) context.Context
func ActorFromContext(ctx context.Context) (Actor, bool)
```

`httpcore`'s three task endpoints read the actor from the context and **only** from the
context. `Actor`/`By` are **removed** from `ClaimInput`, `CompleteInput` and
`ReassignInput`. A body still carrying `"actor"`/`"by"` is **ignored, not rejected**: the
field is out of the contract and its value is never read, so a 400 buys no security and
breaks consumers' rollout windows.

**The override seam is `httpcore.WithRequestActor`, NOT `WithActorResolver`.**
⚠ `WithActorResolver` is **already taken, three times**, for the opposite concept:
`service/options.go:99`, `runtime/task/service.go:113` and `processtest/harness.go:104`
all export it taking a `humantask.ActorResolver`, which *expands an eligibility spec into
candidate actors* — "who **could** act", not "who **is** acting". `authz/authz.go:34`'s
godoc already links `[ActorResolver]` meaning that one. A fourth with the opposite
data-flow direction would be a trap in the exact API surface CLAUDE.md calls the product.
`WithRequestActor(func(context.Context) (authz.Actor, error))` defaults to
`authz.ActorFromContext`.

**When nothing authenticated the caller the request is refused, never downgraded:**

- `ActorFromContext` reports `ok == false` ⇒ **401**. `Open` (Decision 3) therefore means
  *"any **authenticated** actor"*, which is what ADR-0117's "defers to the consumer's
  transport layer" always presumed.
- `httpcore.WithAnonymousActorAllowed()` is the explicit opt-in for demo and example
  wiring — `examples/production_wiring`, `examples/sqlite_wiring` and
  `examples/mysql_wiring` all mount the task routes via `stdlib.Mount` and have no
  authentication.
- A `WithRequestActor` **error** is a **503**, never a downgrade to the zero actor. A
  transient identity-provider failure must not become an open door.
- An **empty `Actor.ID` is rejected as a claimant identity** in the claim path. ⚠ The
  rationale is **re-derived, not inherited**: the audit trail must not record `""` as an
  actor, and under the 401 rule a caller that reached the handler has an ID. The failed
  bundle justified this rule by the deferred backlog-124 guard's `"" == ""` degeneracy;
  that justification left with the deferral.

**The three HTTP adapters do not resolve the actor.** The request context already reaches
`httpcore` unmodified in all three (`stdlib` `req.Context()`, `gin` `gc.Request.Context()`,
`fiber` `c.Context()`), so resolution happens **once**, in `httpcore`; duplicating it would
triple both the resolver invocation and the error classification. ⚠ **For fiber the
middleware idiom is `c.SetContext`, not `c.Locals`** — `c.Locals` does not propagate into
the context `httpcore` receives, so a consumer following fiber's most idiomatic path gets a
silently unauthenticated request. `SECURITY.md` and the examples must show `c.SetContext`.

**This is BREAKING.** **29** pin sites across **9 files** in **5 packages** assert the
body-derived contract — httpcore 11, gin 7, fiber 5, stdlib 5, parity 1 — plus three
`examples/` mains. The net is closed **by construction**: `dto.go:44,50,66` declares
exactly three Actor-bearing fields. ⚠ Two of the 29 — `stdlib/errors_test.go:187` and
`gin/gin_coverage_test.go:244` — assert a **403** and would still return 403 **from the
zero actor**, passing while testing nothing. They are **rewritten**, not recompiled, and
the rewrite is proved by mutation.

### 2. Constructing a `ProcessEngine` without an authorizer is an error

- `service.WithAuthorizer(az authz.Authorizer)` is added as a standalone option.
- `service.WithAllowAllAuthorizer()` is added as the **explicit** opt-in to the permissive
  posture.
- `NewProcessEngine` returns an error when human tasks are configured and neither option
  supplied an authorizer. Allow-all becomes a thing you say, not a thing you get.
- When allow-all *is* chosen it is logged at **WARN as its own record**. ⚠ It cannot be
  done by changing a constant: `service/service.go:323` emits **one** `LogAttrs` record
  carrying store, definitions, taskStore, authz and a hint, and promoting it would move
  four unrelated attributes to WARN. The summary stays at DEBUG.
- `DurableProvider` gains `Authorizer()` as an **optional capability interface** — a
  separate `AuthorizerProvider` the provider may also implement, type-asserted at wiring
  time, following ADR-0081's `Notifier`/`Locker` precedent. Adding a method to
  `DurableProvider` itself would break every third-party implementer.
- **The `WithDurableStore` ordering trap is narrower than the failed draft claimed, and the
  fix is scoped to match.** ⚠ `WithDurableStore` **never writes `c.authz`** — the only
  writers are `WithHumanTasks` (nil-guarded) and the `AllowAll` default — so the authorizer
  is not lost to option order in either direction. The real trap is
  `WithHumanTasks(myStore, az)` written **before** `WithDurableStore(p)`, whose `myStore`
  is silently replaced. **Only `taskStore` is rescoped to apply-as-default**; the
  documented last-writer-wins precedence for the other five provider leaves
  (`service/options.go:157-160`) stays, because changing it would break
  `WithInstanceStore`-before-`WithDurableStore` for existing consumers and that is not a
  change this bundle is making.

### 3. "Open" must be stated, and the public zero value fails CLOSED

- `authz.AuthzSpec` gains **`Open bool`** — the zero value **denies**.
- `activity.WithOpenEligibility()` is the authoring option; the wire key is `eligible_open`.
- `model.Validate` **rejects** a `UserTask` carrying neither the open marker nor any
  eligibility dimension, mirroring ADR-0182's never-due authoring gate.

**Why `bool` and not the previous revision's `Open *bool`.** The tri-state existed to
distinguish a row written before `Open` existed (grandfather it open) from an explicit
`false`. It cannot. `authz.AuthzSpec` is **exported module-root API with exported fields**
(`authz/authz.go:82-86`), so `authz.AuthzSpec{}` written in ordinary Go — by a consumer, by
a consumer-implemented `TaskStore`, by `MemTaskStore`, in any table test — yields
`Open == nil`, which the tri-state defined as *grandfathered open*. That is precisely the
fail-open this record exists to close, and it refutes the previous revision's claim that
*"nil is never authorable … the population can only shrink"*.

**The trap generalises:** a pre-upgrade row and a consumer's zero value cannot be
distinguished *by the field's own absence*, because in both the field is absent. Any
encoding where absence means open fails open for consumers; any encoding where absence
means deny strands in-flight work. **So provenance is resolved where provenance lives — in
the database, before the new binary reads it.**

**The migration.** A per-dialect `0002_*.sql` backfills `"Open": true` into stored specs
carrying no dimension, across all three durable copies, so no ambiguous row survives into
the new binary. After it runs, Go may safely treat absent as deny.

⚠ **`wrkflw_definitions.definition` is not merely a third target — it is the one that keeps
generating bad rows if skipped.** Stored definitions were validated under the *old* rules,
so a dimension-less `UserTask` inside an already-stored definition would keep **minting**
deny-only tasks forever after the upgrade, stranding work by a route a task-row-only
migration never touches. All three copies are backfilled, and `model.Validate` holds the
line for new authoring.

**The gate is hoisted AND authorizer-aware.** The rule — *a spec that states nothing, or
states a dimension this authorizer cannot evaluate, denies* — lives in `runtime/task`,
above all four `Authorize` sites, so a consumer's own `Authorizer` inherits it. The first
audit killed per-authorizer placement for exactly that reason; the second killed
authorizer-*blind* hoisting, because it denies every `Privileges`-carrying spec **including
under casbin**, emptying this decision's own escape hatch and making
`WithAllowAllAuthorizer()` stop meaning allow-all. So the gate asks:

```go
type Dimension int

const (
	DimensionRoles Dimension = iota
	DimensionPrivileges
	DimensionAttribute
)

// DimensionEvaluator is an optional capability an Authorizer may implement to
// declare which AuthzSpec dimensions it actually evaluates. An Authorizer that
// does not implement it is assumed to evaluate DimensionRoles only.
type DimensionEvaluator interface {
	EvaluatesDimension(d Dimension) bool
}
```

This rests on a fact, not a hypothesis: `RoleAuthorizer` genuinely does not evaluate
`Privileges` and documents that at `:119-120`, while the casbin authorizer evaluates roles
(`:45`), privileges (`:56`) and attributes (`:68`). `AllowAll` declares all three — which
is what keeps `WithAllowAllAuthorizer()` honest; casbin declares all three — which is what
keeps the privileges escape hatch non-empty; `casbinauthz.Authorizer` forwards its inner's
declaration; `processtest.SpyAuthorizer` declares all three. Anything else defaults to
**roles only**, fail-closed, with an error naming the capability.

**Sentinels**, following the `workflow-<pkg>:` convention alongside the single existing
`authz.ErrNotAuthorized` (`authz/authz.go:27`): `authz.ErrSpecStatesNothing` and
`authz.ErrUnevaluatableSpec`, both **wrapped so `errors.Is(err, authz.ErrNotAuthorized)`
keeps holding**, leaving existing callers and the 403 classification unchanged.

This **amends ADR-0117 Decisions 1 and 3**. ADR-0117 is not superseded — its authoring API
stands — but Decision 1's *"the engine gate is open"* and Decision 3's *"any combination
(including none) is valid"* both change and **both** must be annotated in place, along with
the **two** godocs that state the open default as fact:
`definition/activity/activity.go:159` (on `NewUserTask` itself, the one every consumer
reads) and `options.go:221`. `authz`'s own three godocs are falsified too —
`authz/authz.go:80-81` (*"An empty spec means allow-all"*) and `:111` (*"spec.Roles is
empty (open access)"*) — and are corrected here rather than left to a later sweep.

## Consequences

### Positive

- The chain in Context findings 1–3 is closed **at every `Authorizer` implementation and
  above them**: the spec-shape gate sits in `runtime/task` before all four `Authorize`
  calls, so a consumer's own `Authorizer` inherits it, and the capability interface means
  hoisting does not silently disable the dimensions an authorizer legitimately evaluates.
- An unauthenticated caller is refused with a 401 rather than promoted to a zero actor, so
  `Open` means "authenticated" and the audit trail cannot record `""`.
- The identity seam is a plain `context.Context` helper in a root package: any middleware,
  any framework, no DI container, reusable by a future non-HTTP transport. Resolution
  happens once, in `httpcore`.
- "Open" becomes a property of the **definition** — durable, reviewable, identical in every
  deployment — and the zero value of the public struct now fails **closed**.
- Allow-all survives as a supported posture, but only as something a consumer states.

### Negative / costs

- **BREAKING in four places**: the three task DTOs lose their actor fields;
  `NewProcessEngine` can now fail where it previously succeeded; a zero `AuthzSpec` changes
  meaning; and a spec naming a dimension its authorizer does not evaluate now denies where
  it previously passed. **29** pin sites and three `examples/` mains change in the same
  bundle, two of the pins by rewrite rather than recompile.
- A **migration** is required, not a CHANGELOG note, and it spans **three** durable copies
  across Postgres, MySQL and SQLite. ⚠ **A consumer-implemented durable `TaskStore`
  receives no migration**; its pre-upgrade dimension-less rows will deny. This is a
  release-note item.
- ⚠ **Decision 1 makes backlog 103 more reachable, and 103 is deferred.** All three
  endpoints drop `Actor.Attributes` today, so `actor.Attributes.*` predicates fail closed
  *vacuously*; once the actor arrives whole they become live, with nothing bounding them
  until 103 ships. This is a **cost of the re-cut**, not a follow-up.
- ⚠ **The chain is NOT closed against the engine trigger path**, and this record does not
  claim it is. `ProcessDriver.ApplyTrigger`'s own godoc states it *"bypasses authorization
  entirely — the engine core is authorization-unaware by design"*; `engine.NewHumanCompleted`
  is likewise reachable module-root API. The gate covers the four `runtime/task` verbs.
- The default of **roles only** for a non-declaring `Authorizer` is fail-closed but
  breaking for a consumer whose own authorizer evaluates more; they must implement one
  method.
- ⚠ This bundle lands the repo's **first `0002_*.sql`** (there is exactly one migration
  file per dialect today), which **activates ADR-0187's parked latent defect**: a
  `CREATE INDEX` naming a table declared in a different migration file derives no `keyed`
  fact silently. Verified inside this bundle rather than deferred again.

### Neutral / follow-ups opened

- **Backlog 141** 🆕 — `wrkflw_instances.snapshot` carries the full `AuthzSpec` (executed)
  but is absent from `atrest.PolicyAtRestLocations`, and ADR-0187's completeness guard
  structurally cannot see the omission: it fails only for `ClassPolicy` columns, and that
  column is `ClassFreeform` — the identical case `wrkflw_definitions.definition` was
  hand-added for. `SECURITY.md`'s published count undercounts by one. Pre-existing.
- **Backlog 142** 🆕 — five `Authorizer` implementations where this record's earlier drafts
  reasoned over two; two of the five are public.
- **Backlog 103** and **124** are deferred with their designs refuted, each needing its own
  ADR. **Backlog 90** stays open.
- **Backlog 62** (ownership/tenant predicate on `InstanceFilter`) becomes *possible* —
  Decision 1 supplies the principal it needs — and stays out of scope.
- ADR-0117 is amended, not superseded; **Decisions 1 and 3** must both be annotated in
  place so a reader of 0117 alone is not misled.
