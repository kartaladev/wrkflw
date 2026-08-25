# ADR-0185-core — authorization identity, required authorizer, and stated eligibility

> **Status: design bundle, pre-audit.** This is the re-cut core of ADR-0185 after that
> bundle failed its rule-#9 audit **twice**. It is **not** an input to implementation
> until it has survived its own audit.
>
> **Scope decision (owner, 2026-08-23):** the bundle is **D1 + D2 + D3** — backlog
> **51** (the principal is whatever the request body says), **52** (the default
> authorizer permits everything), **53** (an empty eligibility spec means allow-all).
> Backlog **103** (deny-list attribute predicates allow on a missing variable, the old
> D4) and **124** (completion never checks the claimant, the old D5) are **deferred to
> their own bundles**.

## Why the cut, and what the evidence actually says about bundle size

The re-audit's root causes named two blockers that are not revisable by wording:

- **103/D4** is *"a syntax problem that cannot be solved with syntax"* — three rounds of
  design produced three **disjoint** hole sets. The last round's dominance rule both
  admitted deny-list predicates (`not ("tier" in vars) and vars.tier != "blocked"` → true
  on empty vars) **and** denied a legitimate one (`and` is left-associative, so the guard
  is not the enclosing `and`'s left operand in a three-term predicate).
- **124/D5** *"needs a per-verb authorization model that does not exist"* — one
  `Eligibility` spec serves four verbs and `internal/authz/casbin/authorizer.go` applies
  `Privileges` unconditionally, so D5's `reassign` token would also be required to Claim,
  Complete and RefreshCandidates.

⚠ **The cut is justified by interaction containment, not by expected finding count.**
ADR-0186's seven-round trend established that the finding *rate* is a property of the
process, not the bundle — round 6 failed at a bundle of exactly **one** decision. The
B3 re-audit's root cause #4 says bundle size multiplies the **interaction** surface, and
five of nine Criticals in that round were holes the revision's own fixes opened in each
other. Both are true. **Expect this bundle's audit to return a high finding count
anyway**; what the cut buys is the removal of a failure *class*, not of findings.

## §1 The removal grid — what cutting D4 and D5 does to the survivors

A removal is a change and generates its own grid; it is not smaller than the grid it
deletes. Derived by the author before dispatch, per the rule-#9 corollary.

| pair | consequence |
|---|---|
| **D1 × D4** | ⚠ **The cut OPENS a hazard.** All three endpoints today drop `Actor.Attributes`, so `actor.Attributes.*` predicates can never be satisfied over HTTP — they fail closed *vacuously*. D1 makes the actor arrive whole, so those predicates become live. D4 was what bounded them. Shipping D1 without D4 **widens the ABAC surface with nothing bounding it**. Recorded in Consequences/Negative; see §6 residual 3. |
| **D1 × D4** (doc) | ADR-0185's sentence *"`Actor.Attributes` reaches the authorizer — closing finding 4's second leg for free"* is **deleted**, not softened. It was already refuted (`actor` is a struct; `Attributes` is a field that always exists, so depth-1 extraction always reports it present), and with D4 gone it has no referent. |
| **D1 × D5** | D1's "reject an empty `Actor.ID` as a claimant identity" was justified *by* D5's `"" == ""` degeneracy. That rationale leaves with D5. The rule **survives on re-derived grounds** — the audit trail must not record `""` as a completer, and under D1 a caller that reached the handler is authenticated and therefore has an ID — and is written that way, not inherited. Inheriting it would leave a dangling citation to a decision not in the bundle (backlog 134's class). |
| **D2 × D4**, **D2 × D5** | None. D2 is genuinely independent of both. |
| **D3 × D4** | ✅ **The cut CLOSES a hazard.** Re-audit F11 — D4's runtime rule re-introducing upgrade-stranding through `Attribute` instead of `Open`, with no migration and no repair verb — disappears entirely. This is the strongest argument for the cut. |
| **D3 × D5** | ⚠ **The cut does NOT close F1.** F1 (a hoisted gate denies every `Privileges`-carrying spec *including under casbin*, emptying the escape hatch D3 itself names) is independent of D5's `reassign` token. It is designed out in §4, not deleted by the cut. |
| **D2 × D3** (in-core) | Not a removal effect, but the grid surfaces it: if the gate is hoisted above the authorizer, **`WithAllowAllAuthorizer()` stops meaning allow-all** — a spec stating nothing would be denied before `AllowAll.Authorize` is ever called. D2 and D3 contradict each other unless the gate is authorizer-aware. Resolved in §4. |

## §2 Executed premises

Per Premise Discipline, every claim below about current behaviour was **run**, at
`d5661d07` (the branch point). Claims that were inherited are marked as re-derived.

### 2.1 `AuthzSpec` is durable in THREE places — and one of them is unlisted

Probe (throwaway `engine/zz_probe_test.go`, `package engine_test`, deleted after the run;
`go test -count=1 -run '^TestZZProbeSnapshotCarriesAuthzSpec$' -v ./engine/` → EXIT=0,
`--- PASS`). Marshalling an `engine.InstanceState` carrying one task whose
`Eligibility` sets all three dimensions emitted:

```json
"Tasks":[{"TaskID":"t1", … "Eligibility":{"Roles":["manager"],
  "Privileges":["finance-task approve"],"Attribute":"vars.amount < 100"} …}]
```

and round-tripped to `{Roles:[manager] Privileges:[finance-task approve]
Attribute:vars.amount < 100}`.

⚠ **Method note, recorded because it nearly produced a false line in this document:** a
naive `strings.Contains(body, "vars.amount < 100")` reports **false** — `encoding/json`
escapes `<` to the six-character sequence `\u003c`, so the observed JSON reads
`"vars.amount \u003c 100"`. The probe printed the round-trip as well, which is what
established the field is intact. A contains-only probe would have recorded
"Attribute is dropped from the snapshot", which is false.

So the three durable `AuthzSpec` copies are:

| copy | path | read by |
|---|---|---|
| `wrkflw_human_task.eligibility` | `internal/persistence/store/humantask_store.go:157` marshal / `:398` unmarshal | **all four `Authorize` sites** |
| `wrkflw_instances.snapshot` | `internal/persistence/store/store_core.go:81` `json.Marshal(capHistory(step.State, …))` → `InstanceState.Tasks[].Eligibility` (`engine/state.go`, `humantask/humantask.go:89`) | instance rehydration |
| `wrkflw_definitions.definition` | `definition/model/node_wire.go` `eligible_roles`/`eligible_privileges`/`eligible_expr` → `internal/persistence/store/definitions.go:120` | task **minting** |

⚠ The failed bundle evidenced its tri-state against `store_core.go` — the copy the four
`Authorize` sites do **not** read — and its migration named no table. Both are corrected
here.

### 2.2 🆕 Defect 141 — ADR-0187's policy-location list is short by one, and its guard cannot see it

`internal/atrest/classification.go:63` `PolicyAtRestLocations` names three locations
(`casbin_rule`, `wrkflw_human_task.eligibility`, `wrkflw_definitions.definition`) and
**omits `wrkflw_instances.snapshot`**, which §2.1 shows carries the full spec. The
completeness guard cannot catch it: `internal/atrest/render.go:404-414` fails only when a
**`ClassPolicy`** column has no entry, and `wrkflw_instances.snapshot` is `ClassFreeform`
(`classification.go:106`) — the *identical* case `wrkflw_definitions.definition` was
hand-added for. So `SECURITY.md`'s published "durable at rest in N places" sentence
undercounts by one.

⚠ This is ADR-0187's own lesson recurring one level up: **a guard blind to the category of
claim it was built to police.** Pre-existing, unrelated to this bundle, filed as backlog
**141**. Fixing it is a candidate for this bundle only because this bundle already
touches the same durable copies — that is a scope question for the audit, not a decision
taken here.

### 2.3 🆕 Defect 142 — there are FIVE `Authorizer` implementations, not two

ADR-0185 reasons over "a **second** implementation". Derived (`func (…) Authorize(`,
non-test):

| # | implementation | visibility | evaluates |
|---|---|---|---|
| 1 | `authz.AllowAll` (`authz/authz.go:106`) | **public** | everything (vacuously) |
| 2 | `authz.RoleAuthorizer` (`authz/authz.go:124`) | **public** | Roles, Attribute — `Privileges` documented reserved and **not** evaluated (`:119-120`) |
| 3 | `internal/authz/casbin.Authorizer` (`:43`) | internal | Roles (`:45`), Privileges (`:56`), Attribute (`:68`) |
| 4 | `casbinauthz.Authorizer` (`casbinauthz/casbinauthz.go:162`) | **public root package** — thin delegate to #3 | as #3 |
| 5 | `processtest.SpyAuthorizer` (`processtest/spyauthz.go:44`) | **public** test harness | configured; ⚠ **allows when `decide == nil`** |

⚠ #4 is the one a consumer actually wires, since CLAUDE.md makes casbin the baseline —
and it is absent from the failed bundle entirely. Filed as backlog **142**.

### 2.4 The engine trigger path bypasses authorization BY DESIGN

Re-audit finding 11, confirmed from the source's own words.
`runtime/processdriver.go` `ApplyTrigger`'s godoc: *"bypasses authorization entirely —
the engine core is authorization-unaware by design. It is the caller's responsibility to
ensure human-task triggers pass through TaskService."* `.Authorize(` appears at exactly
five non-test sites: the four in `runtime/task/service.go` (`:199` Claim, `:234` Reassign,
`:255` Complete, `:306` RefreshCandidates) and `casbinauthz/casbinauthz.go:163`, which is
the delegate of §2.3 #4, not a fifth policy site.

⇒ **This bundle may not claim the chain is closed.** See §6 residual 1.

### 2.5 Anchors still resolve at the branch point

ADR-0186 and ADR-0187 both merged after the failed bundle was written; its citations were
re-checked rather than assumed (an audited bundle decays when its base moves):

- D1: `transport/http/httpcore/endpoints.go:119,132,150` — still the only three
  `authz.Actor{…}` constructions in `transport/`, non-test. ✅
- D2: `service/service.go:200` (`c.authz = authz.AllowAll{}`), `:316` (allow-all label),
  `:323` (`slog.LevelDebug`, the single `LogAttrs` record). ✅
- D3: `engine/step_nodes.go:732` (`Eligibility: spec`, the mint site). ✅
- `transport/http/httpcore/dto.go:44,50,66` declares **exactly three** Actor-bearing
  fields (`ClaimInput.Actor`, `CompleteInput.Actor`, `ReassignInput.By`), so the pin net
  below is closed **by construction** rather than by a grep. ✅

### 2.6 The pin count, RE-DERIVED (not restated)

The 2026-08-20 draft said 23 because its grep matched only `"actor"`, hiding the six
tagged `"by"`. The 2026-08-21 revision said **29 / 9 files / 5 packages**. Re-derived
here over both wire keys *and* the Go literals:

| package | file | pin sites |
|---|---|---|
| httpcore | `dto_test.go` | 5 |
| httpcore | `endpoints_test.go` | 6 |
| gin | `gin_test.go` | 4 |
| gin | `gin_coverage_test.go` | 3 |
| fiber | `fiber_test.go` | 5 |
| stdlib | `errors_test.go` | 2 |
| stdlib | `stdlib_test.go` | 1 |
| stdlib | `coverage_test.go` | 2 |
| parity | `parity_test.go` | 1 |
| | **total** | **29 across 9 files, 5 packages** |

**Confirmed.** ⚠ One occurrence is deliberately **excluded**: `httpcore/validate_test.go:61`
`httpcore.Validate(httpcore.ClaimInput{})` survives field removal unchanged, so it is not a
pin. Excluding it is what makes the number 29 rather than 30 — a count is only trustworthy
if the exclusions are stated.

⚠ **Two of the 29 must be REWRITTEN, not recompiled**: `stdlib/errors_test.go:187` and
`gin/gin_coverage_test.go:244` assert **403**, and after D1 they would still return 403
**from the zero actor** — passing while testing nothing. Confirmed present in the
enumeration above (both are `"by"` sites).

### 2.7 `examples/` and migrations

- Three mains mount the task routes via `stdlib.Mount` and have no authentication:
  `examples/production_wiring/main.go:264`, `examples/sqlite_wiring/main.go:278`,
  `examples/mysql_wiring/main.go:262`. All three need the anonymous opt-in.
- Migrations today: **exactly one file per dialect** —
  `internal/persistence/store/migrations/{postgres,mysql,sqlite}/0001_init.sql`
  (plus `internal/authz/casbin/migrations/0001_casbin_rule.sql`).

⚠ **Cross-delivery consequence: this bundle lands the repo's first `0002_*.sql`**, which
**activates ADR-0187's parked latent defect** — a `CREATE INDEX` naming a table declared in
a *different* migration file derives no `keyed` fact silently, stated in the published
caveat rather than denied by it. It must be verified inside this bundle.

## §3 D1 — the actor travels in the context; the transport never reads it from the body

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
`WithActorResolver` is already exported **three times** for the *opposite* concept —
`service/options.go:99`, `runtime/task/service.go:113`, `processtest/harness.go:104`, all
taking a `humantask.ActorResolver` that expands an eligibility spec into candidate actors
("who **could** act", not "who **is** acting"), and `authz/authz.go:34`'s godoc already
links `[ActorResolver]` meaning that one. A fourth with the opposite data-flow direction
would be a trap in the exact API surface CLAUDE.md calls the product.
`WithRequestActor(func(context.Context) (authz.Actor, error))` defaults to
`authz.ActorFromContext`.

**Unauthenticated is refused, never downgraded:**

- `ActorFromContext` reports `ok == false` ⇒ **401**. `Open` (§5) therefore means *"any
  **authenticated** actor"*, which is what ADR-0117's "defers to the consumer's transport
  layer" always presumed.
- `httpcore.WithAnonymousActorAllowed()` is the explicit opt-in the three `examples/`
  mains need.
- A `WithRequestActor` **error** is a **503**, never a downgrade to the zero actor: a
  transient identity-provider failure must not become an open door.
- An **empty `Actor.ID` is rejected as a claimant identity** in the claim path. ⚠
  Re-derived rationale (§1): the audit trail must not record `""` as an actor, and under
  the 401 rule a caller that reached the handler has an ID. *Not* inherited from D5.

**The three adapters do not resolve the actor.** The request context already reaches
`httpcore` unmodified in all three (`stdlib` `req.Context()`, `gin` `gc.Request.Context()`,
`fiber` `c.Context()`), so resolution happens **once**, in `httpcore`; duplicating it would
triple both the resolver invocation and the error classification. ⚠ **For fiber the
middleware idiom is `c.SetContext`, not `c.Locals`** — `c.Locals` does not propagate into
the context `httpcore` receives, so a consumer following fiber's most idiomatic path gets a
silently unauthenticated request. `SECURITY.md` and the examples must show `c.SetContext`.

`Actor.Attributes` now reaches the authorizer, where all three sites drop it today. ⚠ This
is a **capability change, not a security win** — see §1 (D1 × D4) and §6 residual 3.

**BREAKING**: 29 pins / 9 files / 5 packages (§2.6), two of which must be rewritten rather
than recompiled, and three `examples/` mains.

## §4 D2 — constructing a `ProcessEngine` without an authorizer is an error

- `service.WithAuthorizer(az authz.Authorizer)` — standalone, where today
  `WithHumanTasks(taskStore, az)` is the only entry.
- `service.WithAllowAllAuthorizer()` — the **explicit** opt-in to the permissive posture.
- `NewProcessEngine` returns an error when human tasks are configured and neither option
  supplied one. Allow-all becomes a thing you say, not a thing you get.
- When allow-all *is* chosen it is logged at **WARN as its own record**. ⚠ It cannot be a
  constant change: `service/service.go:323` emits **one** `LogAttrs` record carrying
  store, definitions, taskStore, authz and a hint; promoting it would move four unrelated
  attributes to WARN. The summary stays at DEBUG.
- `DurableProvider` gains `Authorizer()` as an **optional capability interface** — a
  separate `AuthorizerProvider` the provider may also implement, type-asserted at wiring
  time, following ADR-0081's `Notifier`/`Locker` precedent. Adding a method to
  `DurableProvider` itself would break every third-party implementer.
- ⚠ **The `WithDurableStore` ordering trap is narrower than the failed draft claimed.**
  `WithDurableStore` **never writes `c.authz`** — the only writers are `WithHumanTasks`
  (nil-guarded) and the `AllowAll` default — so the authorizer is not lost to option order
  in either direction. The real trap is `WithHumanTasks(myStore, az)` written **before**
  `WithDurableStore(p)`, whose `myStore` is silently replaced. **Only `taskStore` is
  rescoped to apply-as-default**; the documented last-writer-wins precedence for the other
  five provider leaves (`service/options.go:157-160`) stays, because changing it would
  break `WithInstanceStore`-before-`WithDurableStore` for existing consumers and that is
  not a change this bundle is making.

## §5 D3 — "open" must be stated, and the public zero value fails CLOSED

### 5.1 The field

`authz.AuthzSpec` gains **`Open bool`** — the zero value **denies**.
`activity.WithOpenEligibility()` is the authoring option; the wire key is `eligible_open`.
`model.Validate` **rejects** a `UserTask` carrying neither the open marker nor any
eligibility dimension, mirroring ADR-0182's never-due authoring gate.

**Why `bool` and not the failed bundle's `Open *bool`.** The tri-state existed to
distinguish "row written before `Open` existed" (grandfather it open) from an explicit
`false`. It cannot: `authz.AuthzSpec` is **exported module-root API with three exported
fields** (`authz/authz.go:82-86`), so `authz.AuthzSpec{}` written in ordinary Go — by a
consumer, by a consumer-implemented `TaskStore`, by `MemTaskStore`, in any table test —
yields `Open == nil`, which the tri-state defines as *grandfathered open*. That is the
exact fail-open the bundle exists to close (re-audit F4), and it refutes the failed ADR's
*"nil is never authorable … the population can only shrink"*.

**The trap generalises:** you cannot distinguish a pre-upgrade row from a consumer's zero
value *by the field's own absence*, because in both cases the field is absent. Any encoding
where absence means open fails open for consumers; any encoding where absence means deny
strands in-flight work. **So provenance is resolved where provenance actually lives — in
the database, before the new binary reads.**

### 5.2 The migration

A per-dialect `0002_*.sql` backfills `"Open": true` into stored specs that carry no
dimension, across the copies in §2.1, so no ambiguous row survives into the new binary.
After it runs, Go may safely treat absent-as-deny.

⚠ **`wrkflw_definitions.definition` is not merely a third target — it is the one that keeps
generating bad rows if skipped.** Stored definitions were validated under the *old* rules,
so a dimension-less `UserTask` inside an already-stored definition would keep **minting**
deny-only tasks forever after the upgrade, stranding work by a route the task-row migration
never touches. Either it is backfilled too, or the mint site grandfathers pre-upgrade
definitions.

> **⚠ This is the single most likely place for this design to be wrong**, and it is flagged
> for the audit rather than buried. The author's position is: backfill all three copies and
> let `model.Validate` hold the line for new authoring. The audit should attack it.

### 5.3 The gate — hoisted AND authorizer-aware

The rule *"a spec that states nothing, or states a dimension this authorizer cannot
evaluate, denies"* is hoisted into `runtime/task`, above all four `Authorize` sites, so a
consumer's own `Authorizer` inherits it. Audit 1 killed the per-authorizer placement for
exactly that reason.

Hoisting alone is what re-audit **F1** killed: an authorizer-blind gate denies every
`Privileges`-carrying spec **including under casbin**, emptying the escape hatch D3 itself
names, and (per §1's D2×D3 row) makes `WithAllowAllAuthorizer()` stop meaning allow-all.
So the gate asks the authorizer what it evaluates:

```go
// authz
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

This has a basis in fact, not speculation (§2.3): `RoleAuthorizer` genuinely does not
evaluate `Privileges` and says so in its own godoc, while the casbin authorizer evaluates
all three. Declarations:

| implementation | declares |
|---|---|
| `authz.AllowAll` | all three ⇒ **dissolves D2 × D3**; `WithAllowAllAuthorizer()` keeps meaning allow-all |
| `authz.RoleAuthorizer` | Roles, Attribute |
| `internal/authz/casbin.Authorizer` | all three ⇒ **dissolves F1**; the privileges escape hatch stays non-empty |
| `casbinauthz.Authorizer` | forwards its inner's declaration |
| `processtest.SpyAuthorizer` | all three (a spy returns the configured decision for whatever it is asked) |
| anything else | **default: Roles only**, fail-closed, with an error naming the capability |

### 5.4 Sentinels

Following the `workflow-<pkg>:` convention alongside the single existing
`authz.ErrNotAuthorized` (`authz/authz.go:27`):

- `authz.ErrSpecStatesNothing` — the spec declares no dimension and is not open.
- `authz.ErrUnevaluatableSpec` — the spec declares a dimension this authorizer does not
  evaluate.

Both are **wrapped so `errors.Is(err, authz.ErrNotAuthorized)` keeps holding**, so existing
callers and the 403 classification are unchanged.

## §6 Residuals — stated, because a documented residual is still a shipped defect

ADR-0186 documented two hazards it introduced instead of mitigating them and `/code-review`
refused the distinction, making both MEDIUM findings. These are stated as *known limits of
a stated scope*, not as hazards this bundle introduces and shrugs at.

1. **`ProcessDriver.ApplyTrigger` and `engine.NewHumanCompleted` bypass authorization by
   design** (§2.4), and their own godoc says so. The gate covers the four `runtime/task`
   verbs. **The ADR may not claim the chain is closed** — the failed bundle's *"this is
   what makes it true"* is struck.
2. **A consumer-implemented durable `TaskStore` receives no migration** (§5.2). Its
   pre-upgrade dimension-less rows will deny. Must be a release-note item, not a footnote.
3. ⚠ **D1 makes backlog 103 more reachable** (§1, D1 × D4): actor-attribute predicates go
   from vacuously-deny to live, and 103 is deferred. This is a **cost of the cut** and
   belongs in Consequences/Negative, not in a follow-ups list.
4. Backlog **90** (an eligible actor stealing another's claim) and **124** stay open.
5. Backlog **141** and **142** (§2.2, §2.3) are pre-existing, found here, filed.

## §7 Verification and test strategy

- Every prescribed test states **what makes it fail today**; a test whose author cannot say
  is presumed vacuous. This repo has shipped **twelve** tests that could not fail.
- Load-bearing tests get a **mutation ablation in a `git worktree`** — never the shared
  tree. That rule has been violated twice and survived on the agents' discipline rather
  than the briefing.
- ⚠ Assert a test **ran** (`grep -q '^--- PASS: <Name>'`), never infer it from a green
  exit: `go test -run` on a nonexistent name exits 0, and **anchoring the regex does not
  help** — ADR-0187 shipped `-run '^TestSecurityMdInSync$'` in a script and it was called
  rename-safe twice before the gate caught it.
- The two vacuous-403 pins (§2.6) are **rewritten**, and the rewrite is proved by mutation:
  revert D1's context read and confirm they go RED, which today they would not.
- Verification per CLAUDE.md: `go test -race` + `scripts/coverage.sh` ≥ 85 % over
  hand-written code, repo-wide `go test ./...`, repo-wide `golangci-lint run ./...`.
  Docker is needed for the store packages; SQLite is pure Go.

## §8 What the audit must attack

Dispatch is the four-lens form that has worked for six consecutive bundles —
**execution / failure-modes / counting / interaction**, detached worktrees **at the bundle
commit**, a **step-0 bundle-presence check stated in every brief**, and "append findings
per finding, before the next probe". This evidence file is an **input** to the audit, not
a conclusion of it — attack it too; findings have landed inside a bundle's own evidence
file in two separate rounds.

Named targets, author-flagged:

1. **§5.2's definitions-copy decision** — the most likely place this design is wrong.
2. **§5.3's default for non-declaring authorizers** (Roles only) — blast radius across five
   implementations, two of them public.
3. **§1's removal grid** — give the interaction lens the explicit list: D4 and D5 removed,
   D1/D2/D3 surviving. ⚠ A removal generates its own grid and it is not smaller than the
   one deleted.
4. **§2.6's re-derived 29** and every other enumeration here — the counting lens found the
   decisive Critical in six consecutive bundles, and its failure mode is **the net, the
   anchor and the SCOPE, not the arithmetic**.
5. **§2.2 / §2.3** — two brand-new defects found during design, neither of which has ever
   been audited.
