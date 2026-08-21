# 185. Authorization identity is not self-asserted, and eligibility must be stated

> ## ⛔ RE-AUDIT FAILED — 2026-08-21. NOT an input to implementation.
>
> This is the **second** failed audit for this bundle. The 2026-08-20 draft failed on
> individual decisions; this revision fixed those and **failed on the interactions
> between the decisions it rewrote** — ~13 distinct Criticals across three lenses, two
> of them found independently by two lenses each. D4's strict-reference mechanism is
> the wrong *shape* (three rounds, three disjoint hole sets); D5's privilege token
> bricks all four verbs because one spec serves four; D3 targets the wrong one of two
> durable locations and its `*bool` makes a **public** struct's zero value fail-OPEN;
> ADR-0186 D5's value-free 400 is not implementable where prescribed.
> ⚠ Three findings are in the bundle's own evidence file.
> See `docs/plans/sweep-evidence/reaudit-b3-adjudication.md`.
> **A scope decision is pending before any further revision.**
>
> The 2026-08-20 draft failed its rule-#9 audit (58 findings, 12 Critical —
> `docs/plans/sweep-evidence/audit-b3-adjudication.md`). **Four decisions changed**,
> and this record is the result. Per rule #9 a bundle whose Decisions changed has
> **not** been audited: this is **not yet an input to implementation**.
>
> What changed, and why: **D3** now uses a tri-state `Open *bool` plus a data
> migration, because the boolean stranded every in-flight human task on upgrade.
> **D4**'s escape hatch was rewritten from scratch — `has(vars,"k")` does not exist
> in expr v1.17.8 and denied everyone — and the guard rule gained a *dominance*
> requirement that execution showed was missing. **D5**'s guard now covers
> `Reassign`, which was a two-hop bypass the draft named as its own mitigation.
> **D1** gained a decision for "nothing authenticated the caller", which the draft
> did not have. The spec-shape gate is now **hoisted above the `Authorizer`
> implementations**, because the draft fixed `RoleAuthorizer` while pointing
> consumers at the casbin authorizer it left fail-open.
>
> Executed evidence for every new claim:
> `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`.

- Status: **Proposed** (pending rule-#9 **re**-audit)
- Date: 2026-08-20, revised 2026-08-21
- Amends: **ADR-0117 Decisions 1 and 3** — Decision 1's *"with none set, the engine
  gate is open"* and Decision 3's *"any combination (**including none**) is valid"*.
  Both must be annotated in place; the draft named only Decision 1.
- Relates to: ADR-0081 (capability interfaces), ADR-0118, ADR-0145, ADR-0147,
  ADR-0182 (authoring-time gate precedent), ADR-0183 (the claim invariant this
  guard must not contradict)
- Backlog: 51, 52, 53, 103, 124, and the parked half of 102

## Context

`wrkflw`'s authorization requirement is role-based, resource-privilege-based and
attribute-based, evaluated at human-task nodes through a pluggable `Authorizer`.
The mechanism exists and works. What does not work is everything that decides
**which actor** and **which spec** reach it. Five findings, each independently
verified and each neutralising the next (spec §1, §2):

1. **The principal is whatever the request body says.** `httpcore`'s three
   task endpoints build `authz.Actor` from `ClaimInput.Actor`,
   `CompleteInput.Actor` and `ReassignInput.By`
   (`transport/http/httpcore/endpoints.go:119,132,150` — the only three
   `authz.Actor{…}` constructions in `transport/`, non-test). `CustomizeConfig`
   (`seam.go:19-33`) declares exactly six fields and no identity seam, so a
   consumer's authentication middleware has no supported way to override it. Any
   caller can post `{"actor":{"id":"alice","roles":["manager"]}}`.
   `authz.Actor.Attributes` exists but is dropped at all three sites, so attribute
   predicates over actor attributes can never be satisfied over HTTP at all.

2. **The default authorizer permits everything.** `service/service.go:199-200`
   defaults `c.authz` to `authz.AllowAll{}`. The construction summary discloses it
   only at `slog.LevelDebug` — ⚠ the level is at **`service/service.go:323`**; the
   allow-all *label* is computed at `:315-317`, and the draft cited the label as if
   it were the level. There is no standalone `WithAuthorizer` option —
   `WithHumanTasks(taskStore, az)` is the only entry — and `DurableProvider`
   (`service/durable.go:17-24`) has six methods and no `Authorizer()`, so the
   natural durable wiring lands on allow-all silently.

3. **An empty spec means allow-all — and so does a privileges-only spec.**
   `AuthzSpec`'s godoc says so verbatim (`authz/authz.go:79-86`), and
   `RoleAuthorizer.Authorize` (`:124`) returns `nil` for the zero spec. Executed:
   the zero spec, `Roles: []string{}`, `Roles: nil` **and** a `Privileges`-only
   spec all admit the **zero actor** with `err=<nil>`. The privileges leg follows
   from `:119-120`, which documents `Privileges` as reserved and **not evaluated**
   by `RoleAuthorizer`.
   ⚠ **And the mixed case is worse than the privileges-only case.** A spec
   `{Roles:["manager"], Privileges:["finance-task approve"]}` carries a dimension
   `RoleAuthorizer` *can* evaluate, so it passes the role check and **silently
   discards the privilege requirement**: any manager clears a gate the author wrote
   to require an explicit grant. That failure *looks configured*, which makes it
   strictly more dangerous than the empty spec.
   This is not an oversight: **ADR-0117 Decisions 1 and 3 blessed it.** That
   deferral to "the transport layer" is only sound if the transport layer has an
   identity — which finding 1 says it does not.

4. **Deny-list attribute predicates allow on a missing variable.** Executed with
   `vars = map[string]any{}`: `vars.status != "blocked"`, `vars.blocked != true`,
   `!(vars.blocked == true)`, `vars.tier == nil or …` and
   `… or "manager" in actor.Roles` all allow; the positive control
   `vars.region == "eu"` correctly denies. An evaluation *error* already fails
   closed (`authz/authz.go:136-141`), so the hole is absence-*without*-error.
   ⚠ **The class is unbounded, not five forms wide.** A missing map key returns
   `reflect.Zero` unconditionally (`expr@v1.17.8/vm/runtime/runtime.go:58-70`), so
   the class is *every predicate that is true when a referenced key resolves to
   nil* — `not vars.blocked`, `vars.owner != actor.ID`, `vars.a == vars.b` are all
   members. **Five were sampled and executed**; they are the test table, not the
   class. The draft's "five predicate forms wide" described a sample as a closure.
   ⚠ **The obvious fix does not work.** Compiling without
   `expr.AllowUndefinedVariables()` and supplying `expr.Env(...)` changes nothing
   — executed, identical verdicts — and expr v1.17.8 has no `Fetcher` interface.
   See spec §2.4.1.

5. **Completion never checks who claimed.** `handleHumanCompleted`
   (`engine/step_triggers.go`) copies `t.Actor` into the audit record unvalidated.
   Re-derived over the **whole function body** — `awk '/^func handleHumanCompleted/,/^}/'`
   then `grep -c "Candidates\|Eligibility\|Claim"` → **zero hits**. (The draft
   reported "one hit, and it is a comment"; that was a window artifact from an
   `awk NR>=839 && NR<=960` range that started 10 lines *before* the function and
   ended 23 lines short of it. The conclusion is stronger than the draft claimed,
   and the measurement it rested on was wrong. ⚠ **Do not quote line numbers for
   this file** — cite the symbol.)
   The four `Authorize` sites in `runtime/task/service.go` (`:199` Claim, `:234`
   Reassign, `:255` Complete, `:306` RefreshCandidates) all evaluate
   `task.Eligibility`, which is set membership — it cannot distinguish the claimant
   from any other eligible actor. The engine holds `Claim.Actor` and never
   compares it.

**A sixth finding, which the draft did not have.** There is a **second**
`Authorizer` implementation and a **second** ABAC evaluation site:
`internal/authz/casbin/authorizer.go` builds its own `expreval.New()` (`:30`),
carries its own *"An empty spec allows"* godoc (`:33`), and evaluates
`spec.Attribute` at `:68`. Findings 3 and 4 are unfixed there. Repo-wide there are
**four** `expreval.New(` instances, non-test — `authz/authz.go:23`,
`internal/authz/casbin/authorizer.go:30`, `engine/conditions.go:43`,
`runtime/processdriver_options.go:200` — not the two the draft's "structural
scoping" argument reasoned over. CLAUDE.md makes casbin **the baseline**, so a fix
confined to `RoleAuthorizer` ships the hole to the production configuration.

A seventh, adjacent decision was **deliberately parked** and is closed here: after a
failed cross-node casbin policy reload, `Enforce` keeps answering from the last good
policy, so a **revoked** permission returns `true, err=nil` indefinitely. The
logging/metric half shipped (`internal/authz/casbin/db.go:76-99`,
`wrkflw_authz_policy_reload_failures_total`); the fail-closed question did not.

**These compose.** A resolved principal (1) still meets an allow-all authorizer
(2); a real authorizer still meets an empty spec (3); a non-empty spec still meets
a predicate that allows on absence (4); a correctly authorized *claim* is followed
by an unchecked *completion* (5); and all of it is bypassed by wiring the baseline
authorizer (6). Fixing any one alone changes nothing an attacker experiences.

## Decision

### 1. The actor travels in the `context.Context`; the transport never reads it from the body

`authz` gains the identity seam, in the **public root package**, usable with
plain functions and no DI:

```go
func ContextWithActor(ctx context.Context, a Actor) context.Context
func ActorFromContext(ctx context.Context) (Actor, bool)
```

`httpcore`'s three task endpoints read the actor from the context and **only**
from the context. `Actor`/`By` are **removed** from `ClaimInput`, `CompleteInput`
and `ReassignInput`. A body that still carries `"actor"` or `"by"` is ignored (not
rejected): the field is out of the contract and its value is never read, so a 400
would break consumers' rollout windows for no security gain.

**The override seam is named `httpcore.WithRequestActor`, NOT `WithActorResolver`.**
⚠ `WithActorResolver` is **already taken, three times**, for the opposite concept:
`service/options.go:99`, `runtime/task/service.go:113` and
`processtest/harness.go:104` all export it taking a `humantask.ActorResolver`,
which *expands an eligibility spec into candidate actors* — "who **could** act",
not "who **is** acting". `authz/authz.go:34`'s godoc already links `[ActorResolver]`
meaning that one. Adding a fourth `WithActorResolver` with the opposite data-flow
direction is a trap in the exact API surface CLAUDE.md calls the product.
`WithRequestActor(func(context.Context) (authz.Actor, error))` defaults to
`authz.ActorFromContext`.

**When nothing authenticated the caller, the request is refused — it is not
downgraded to a zero actor.**

- `ActorFromContext` reports `ok == false` ⇒ `httpcore` returns **401**. `Open`
  (Decision 3) therefore means *"any **authenticated** actor"*, which is what
  ADR-0117's "defers to the consumer's transport layer" always presumed.
- `httpcore.WithAnonymousActorAllowed()` is the explicit opt-in for demo and
  example wiring, which needs it (`examples/` has no authentication).
- A `WithRequestActor` **error** is a **503**, never a downgrade to the zero actor.
  A transient identity-provider failure must not become an open door.
- Independently, an **empty `Actor.ID` is rejected as a claimant identity** in the
  claim path (ADR-0183's neighbourhood). Without this, `Claim.Actor.ID == ""` and
  `t.Actor.ID == ""` make Decision 5's guard degenerate to `"" == ""` and any
  anonymous caller may complete any other anonymous caller's task.

**The three HTTP adapters do not resolve the actor.** The request context already
reaches `httpcore` unmodified in all three (`stdlib` `req.Context()`, `gin`
`gc.Request.Context()`, `fiber` `c.Context()`), so resolution happens **once**, in
`httpcore`. Duplicating it three times would triple the resolver invocation and the
error classification. ⚠ **For fiber the middleware idiom is `c.SetContext`, not
`c.Locals`** — `c.Locals` does not propagate into the context `httpcore` receives,
so a consumer following fiber's most idiomatic path gets a silently unauthenticated
request. `SECURITY.md` and the examples must show `c.SetContext`.

Because the actor now arrives whole rather than being re-projected field by field,
`Actor.Attributes` reaches the authorizer — closing finding 4's second leg for free.

**This is BREAKING.** ⚠ **29** test pins across 9 files in 5 packages assert the
body-derived contract — httpcore 11, gin 7, fiber 5, stdlib 5, parity 1 — and
**three** `examples/` mains mount the task routes via `stdlib.Mount`. The
2026-08-20 draft said 23, because its grep matched only `"actor"`; `ReassignInput.By`
is tagged `"by"` (`dto.go:66`), hiding six. ⚠ Two of the six —
`stdlib/errors_test.go:187` and `gin_coverage_test.go:244` — assert a **403**, and
after this decision they still return 403 **from the zero actor**, passing while
testing nothing. They must be rewritten, not merely recompiled.

### 2. Constructing a `ProcessEngine` without an authorizer is an error

- `service.WithAuthorizer(az authz.Authorizer)` is added as a standalone option.
- `service.WithAllowAllAuthorizer()` is added as the **explicit** opt-in to the
  permissive posture.
- `NewProcessEngine` returns an error when human tasks are configured and no
  authorizer was supplied by either option. Allow-all becomes a thing you say,
  not a thing you get.
- When allow-all *is* chosen, it is logged at **WARN as a separate record**.
  ⚠ It cannot be done by changing a constant: `service/service.go:323` emits **one**
  `LogAttrs` record carrying store, definitions, taskStore, authz and a hint, and
  promoting it would move four unrelated attributes to WARN. The summary stays at
  DEBUG; allow-all gets its own line.
- `DurableProvider` gains `Authorizer()` as an **optional capability interface** —
  a separate `AuthorizerProvider` the provider may also implement, type-asserted at
  wiring time — following ADR-0081's `Notifier`/`Locker` precedent. Adding a method
  to `DurableProvider` itself would break every third-party implementer.
- **The `WithDurableStore` ordering trap is narrower than the draft claimed, and
  the fix is scoped to match.** ⚠ `WithDurableStore` **never writes `c.authz`** —
  the only writers are `WithHumanTasks` (nil-guarded) and the `AllowAll` default —
  so the authorizer is not lost to option order in either direction, and the wiring
  the draft named (`WithHumanTasks(nil, az)`) is *already* order-independent. The
  real trap is `WithHumanTasks(myStore, az)` written **before** `WithDurableStore(p)`,
  whose `myStore` is silently replaced. Only `taskStore` is rescoped to
  apply-as-default; the documented last-writer-wins precedence for the other five
  provider leaves (`service/options.go:157-160`) **stays**, because changing it
  would break `WithInstanceStore`-before-`WithDurableStore` for existing consumers
  and that is not a change this bundle is making.

### 3. "Open" must be stated — as a tri-state, so the upgrade does not strand live work

- `authz.AuthzSpec` gains **`Open *bool`**, not `Open bool`.
- `activity.WithOpenEligibility()` is the authoring option; the wire key is `open`.
- `RoleAuthorizer` denies a spec that is neither open nor carries a dimension it
  can evaluate.
- **Denial covers ANY unevaluatable dimension, not only a sole one.** A spec with
  **any** non-empty `Privileges` returns `authz.ErrUnevaluatableSpec` wrapped in
  `ErrNotAuthorized` under `RoleAuthorizer` — including the mixed
  `{Roles, Privileges}` shape of Context finding 3, which the draft's *"whose
  **only** dimension"* wording left fail-open. A consumer who wants privileges
  evaluated wires the casbin authorizer, which does evaluate them.
- `model.Validate` **rejects** a `UserTask` carrying neither an explicit open marker
  nor any eligibility dimension, mirroring ADR-0182's never-due authoring gate.

**Why tri-state, and what it costs.** Eligibility is a **stored** field, frozen into
the task record at creation (`engine/step_nodes.go:723`); all four `Authorize` sites
read the stored spec and never re-derive it from the definition. The instance
snapshot is `json.Marshal`ed (`internal/persistence/store/store_core.go:81`) and
read back with a plain `json.Unmarshal` (`:174`), and `AuthzSpec` has no json tags.
With `Open bool`, a new binary decodes every pre-upgrade row as `Open == false` ⇒
under this decision every human task open at upgrade time becomes **unclaimable,
uncompletable and unreassignable, with no repair verb** — and re-authoring the
definition does not fix them, because the spec was snapshotted at mint time. For an
engine whose human tasks are deliberately long-lived (working-day deadlines, in-wait
reminders), that is close to unshippable.

Executed through the real codec: a pre-upgrade row decodes to `Open == nil`,
distinguishable from an explicit `false`, and `nil`/`true`/`false` all round-trip.
So:

| stored `Open` | meaning | verdict |
|---|---|---|
| `nil` | written before `Open` existed | **grandfathered open** — today's semantics preserved |
| `true` | author said open | open |
| `false` / dimension present | author stated intent | evaluated normally |

`nil` is a **migration state, not a supported authoring state**: `model.Validate`
refuses to *mint* it, so the population can only shrink. A migration phase
backfills `Open: true` into pre-upgrade open-task snapshots so the grandfathered
population is bounded and observable rather than permanent. **The 2026-08-20 plan
had no persistence phase at all**; the revised plan does.

⚠ The *other* direction — an older binary reading a newer row, which silently drops
`Open` — is real (executed: `err=<nil>`, field gone) but requires a mixed-version
deployment, which this repo already declares out of contract. The draft gated only
that direction, which is the one that does not happen on a single-version upgrade.

**Blast radius, counted.** The draft asserted *"**every** existing definition with
no eligibility becomes invalid"* with no number, and the quantifier is false.
Re-derived: **274** `NewUserTask` call sites repo-wide; **128** carry no eligibility
dimension; only **5** reach `model.Validate`, whose single non-test caller is
`definition/model/builder.go`. Definitions built as `model.ProcessDefinition` struct
literals — the dominant idiom in `engine`'s tests — are never validated and are
untouched by the authoring gate. So the two halves have different blast radii: the
**authoring** gate reaches ~5 sites, the **runtime** rule reaches all 128, across
`engine`, `runtime`, `processtest` and `service`. The affected sites concentrate on
**manual tasks**, which are eligibility-free *by design* under ADR-0117, so the
migration is a semantic decision per task, not mechanical `open: true` sprinkling.

This **amends ADR-0117 Decisions 1 and 3**. ADR-0117 is not superseded: its
authoring API stands. Decision 1's *"the engine gate is open"* and Decision 3's
*"any combination (including none) is valid"* both change, and **both** must be
annotated in place — ⚠ along with the **two** godocs that state the open default as
fact: `definition/activity/activity.go:159` (on `NewUserTask` itself, the one every
consumer reads) and `options.go:221`. The draft named one.

### 4. An attribute predicate denies when a variable it references is absent

`authz`'s evaluation of `spec.Attribute` becomes **strict about references**: the
predicate's `vars.*` / `actor.*` references are extracted statically (via
`expr-lang/expr`'s own `parser` + `ast.Walk`, cached alongside the compiled
program), and evaluation denies when a referenced key is absent from the env.

**The escape hatch is `"k" in vars` and `vars?.k`. It is NOT `has(vars,"k")`.**
⚠ The 2026-08-20 draft prescribed `has(vars, "k")`, which **is not a function in
expr v1.17.8**: `AllowUndefinedVariables` resolves `has` to nil, so it *compiles*
and fails at run time as `invalid operation: cannot call nil (1:1)`, and
`RoleAuthorizer` wraps run errors as `ErrNotAuthorized`. A predicate written to the
draft's own prescription **denied everyone, permanently.** Executed replacements:

| form | evaluates | extractable as a guard? |
|---|---|---|
| `"k" in vars` | ✅ | ✅ — and it is the recommended form |
| `vars?.k` | ✅ | ✅ — `MemberNode.Optional` is set |
| `(vars.k ?? d)` | ✅ **only parenthesised** | ✅ |
| `get(vars,"k")` | ✅ | ❌ **extracts ZERO references** |
| `has(vars,"k")` | ❌ run-time error | — |

Two of these carry corrections the audit itself did not have, and both were found
by execution:

- ⚠ **`??` does not parse unmixed.** `vars.tier ?? "none" == "gold"` is a compile
  error (*"Operator (==) and coalesce expressions (??) cannot be mixed"*); only
  `(vars.tier ?? "none") == "gold"` works. Documentation offering `??` must show
  the parentheses.
- ⚠ **`get()` is a bypass, not a guard.** `get(vars,"k") == "x"` extracts **no**
  references at all, so it would skip the strict check entirely. It is handled by
  the zero-reference rule below, and is **not** offered as an escape hatch.

**A guard must DOMINATE its use.** ⚠ This is the most likely place for this decision
to be wrong, and execution already caught one wrong implementation of it. A naive
collector that marks a key optional whenever `"k" in vars` appears *anywhere* in the
tree is **unsound**. Executed with `vars` empty:

```
"tier" in vars and vars.tier == "gold"      evaluates=false   naive collector: "guarded"
"tier" in vars or  vars.tier != "blocked"   evaluates=TRUE    naive collector: "guarded"  <-- HOLE
not ("tier" in vars) or vars.tier == "x"    evaluates=TRUE    naive collector: "guarded"  <-- HOLE
```

Rows 2 and 3 allow on an absent key — exactly the class this decision closes — while
a tree-wide collector calls them guarded. A guard counts only when the existence
test dominates the use: the **left operand of `and`**, or the **condition of a
ternary** whose consequent holds the use. The three rows above are the falsifying
table the implementation must be tested against.

**The closed set the check covers, and the verdict for everything else.** Extraction
is **depth-1**: `vars.order.total` yields `vars.order`. That is not a limitation to
apologise for — `humantask.HumanTask.Vars`' own godoc already states the snapshot is
a shallow `maps.Clone` and that *"eligibility predicates should rely on top-level
scalar variables only"*. Depth-1 is precisely the documented supported surface.
The three residual shapes get an explicit, fail-closed verdict rather than silence:

| shape | example | verdict |
|---|---|---|
| depth-1 member / bracket-literal | `vars.k`, `vars["k"]` | checked |
| nested chain | `vars.order.total` | checked at depth 1 (`vars.order`); deeper absence is **out of scope**, stated |
| dynamic key | `vars[actor.ID]` | **deny** — the extractor cannot prove what it reads |
| zero references | `get(vars,"k")`, `vars \| first()` | **deny** — same reason; this is what closes the `get()` bypass |

**Where the rule lives — three places, not one.** The draft called its scoping
*"structural rather than conventional"* and reasoned over two evaluator instances;
four exist, and the argument does not hold. The honest statement is that strictness
is **opt-in per evaluator instance**, so it must be turned on at every site that
evaluates `spec.Attribute`:

1. `authz.RoleAuthorizer` — `expreval.New(expreval.WithStrictReferences())`.
2. **`internal/authz/casbin.Authorizer`** — the same, on its own `attrEval` (`:30`).
   Without this the baseline authorizer keeps the hole.
3. **Task creation**, in `engine/step_nodes.go`, as the *diagnostic*: minting a task
   whose predicate references a key absent from the creation snapshot fails there,
   with a node-scoped message.

Gateway evaluation is untouched because `engine/conditions.go`'s evaluator is a
different instance that never reads `spec.Attribute` — a fact about which option is
passed where, **not** a guarantee conferred by package boundaries.

**Why the check also runs at task creation.** `HumanTask.Vars` is a snapshot frozen
at mint time; it is never refreshed (`RefreshCandidates` refreshes candidates, not
`Vars`). So a predicate over a variable written *later* — by a parallel branch, a
boundary or a timer path — references a key that is absent for the task's whole
life. Today that silently allows. Under the runtime rule alone it would silently
**deny, forever, with no repair verb**. Failing at creation puts the diagnostic
where the author can act on it, and the predicate could never have worked anyway.
The runtime rule remains the *guarantee* (it covers pre-upgrade tasks and specs the
mint-time check never saw); the creation-time check is what stops a legitimate
predicate from becoming a permanent 403.

### 5. A claimed task may only be completed by its claimant — and `Reassign` is part of the guard

`handleHumanCompleted` compares the completion trigger's actor to
`task.Claim.Actor.ID`:

- task **claimed** and the actors differ ⇒ refuse with a new `engine.ErrNotClaimant`
  sentinel (`workflow-engine:` prefix), classified 403;
- task **claimed** by the same actor ⇒ proceed;
- task **unclaimed** ⇒ proceed on the eligibility check alone (unchanged), so
  direct-completion flows and role-only-eligible tasks keep working.

**`Reassign` is covered, because otherwise it is the bypass.** ⚠ The 2026-08-20
draft named `Reassign` as the *mitigation* for the stranded-claimant risk and did
not notice it is a two-hop escalation — a finding two audit lenses reached
independently. `Reassign` authorizes `by` against `task.Eligibility`, **the same
check as `Claim`** by the repo's own godoc (`runtime/task/service.go:206-217`), and
then overwrites the claim (`engine/step_triggers.go:643`,
`task.Claim = &humantask.Claim{Actor: authz.Actor{ID: t.To}}`). So an actor who is
merely *eligible* — exactly the actor Decision 5 exists to stop — calls
`reassign {"from":"alice","to":"mallory"}` and then completes as claimant. The one
input required, the current claimant's id, is disclosed by design (ADR-0147) and is
**backlog 54, an item in this same bundle**.

Therefore: a reassignment whose `by.ID` differs from the current claimant requires
an explicit **`reassign` privilege token** in the spec — the seam Decision 3's
privileges leg opens — rather than bare eligibility. Self-service reassignment (the
claimant handing their own task on) stays on the eligibility check alone. A
deployment that wants the old behaviour states the privilege on the spec.

`Candidates` is explicitly **not** the comparison target: `runtime/task/service.go`
states in source that *"Candidates are a projection, not an access-control list"*,
and comparing against it would promote a projection to an ACL.

**Residual, stated rather than implied:** backlog **90** (an eligible actor stealing
another's claim on the *claim* path) is **not** closed here. After this decision the
property is *"a non-eligible, unauthenticated, or non-privileged actor cannot
complete a task somebody else holds"* — not the unqualified claim the draft made.

### 6. A stale casbin policy sheds the node before it denies the user

`casbinauthz` tracks the last successful policy load and exposes a `HealthCheck`
reporting staleness, so a readiness probe can drain the node.
`WithStalePolicyBudget(d)` makes `Enforce` deny once staleness exceeds `d`.

**Defaults: health check enabled, deny-budget disabled.** Drain first; deny only if
the operator asks. `SECURITY.md` states plainly that the library exposes the signal
and the consumer must wire it — the library cannot enforce a readiness probe it does
not own.

This closes the decision parked when item 102's logging half shipped.

## Consequences

### Positive

- The chain in Context §1–6 is closed **at both `Authorizer` implementations and
  above them**: the spec-shape gate is evaluated in `runtime/task` before all four
  `Authorize` calls, so a consumer's own `Authorizer` inherits it too. The draft's
  "closed end to end" sentence was false while the baseline authorizer stayed
  fail-open; this is what makes it true.
- An unauthenticated caller is refused with a 401 rather than promoted to a zero
  actor, so `Open` means "authenticated", and the audit trail cannot record `""`.
- The identity seam is a plain `context.Context` helper in a root package: any
  middleware, any framework, no DI container, reusable by a future non-HTTP
  transport. Resolution happens once, in `httpcore`.
- "Open" becomes a property of the **definition** — durable, reviewable, identical
  in every deployment — and the tri-state means the upgrade does not strand live
  work to get there.
- A predicate that could never have worked (referencing a variable absent from the
  task's frozen snapshot) now fails at authoring/mint time instead of allowing
  silently today and denying silently forever afterwards.

### Negative / costs

- **BREAKING, in four places**: the three task DTOs lose their actor fields;
  `NewProcessEngine` can now fail where it previously succeeded; a zero `AuthzSpec`
  changes meaning; a claimed task rejects a non-claimant completer. **29** test pins
  and three `examples/` mains change in the same bundle, and two of those pins must
  be *rewritten* rather than recompiled because they would pass vacuously.
- A **migration** is required, not merely a CHANGELOG note: 128 no-eligibility
  `NewUserTask` sites across four packages that have to be re-authored per task, and
  a data migration for in-flight snapshots. `Open == nil` is a grandfathering state
  that a deployment carries until the migration runs.
- `Reassign` now requires a privilege for the non-claimant case, which is a real
  workflow change for deployments that used reassignment as an escalation path. They
  must state the privilege.
- Strict reference checking will deny predicates that work today, and the
  guard-dominance rule is the most likely place for this ADR to be wrong — one
  implementation of it has already been executed and found unsound.
- **Backlog 90 stays open**, and the completion guard is defeatable by a
  *privileged* reassigner by design. That is a smaller claim than the draft made.

### Neutral / follow-ups opened

- Backlog **90** (claim theft) stays open, now visibly adjacent and with its
  residual path named.
- Backlog **106** (readiness aggregate) gains a concrete consumer in Decision 6.
- Backlog **62** (ownership/tenant predicate on `InstanceFilter`) becomes *possible*
  — Decision 1 supplies the principal it needs — and stays out of scope.
- Deep (depth ≥ 2) reference absence is explicitly out of scope for the strict
  check, consistent with `HumanTask.Vars`' documented top-level-scalar contract.
- ADR-0117 is amended, not superseded; **Decisions 1 and 3** must both be annotated
  in place so a reader of 0117 alone is not misled.
