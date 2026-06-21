# Spec: casbin-backed Authorizer (baseline production authz)

- Status: Accepted
- Date: 2026-06-21
- Related: ADR-0010 (casbin Authorizer), ADR-0008 (façade-over-internal),
  ADR-0003 (clock), plan `docs/plans/2026-06-21-authz-casbin.md`

## Goal / scope

The engine ships a single authorization port — `authz.Authorizer` — evaluated at
human-task nodes (`TaskService.Claim/Reassign/Complete`). Today the only
implementations are the pure built-ins `authz.AllowAll` and
`authz.RoleAuthorizer` (role any-match + an `expr` attribute predicate;
`AuthzSpec.Privileges` is reserved/unimplemented).

This sub-project adds the **baseline production authorizer**, backed by
[casbin v2](https://github.com/casbin/casbin) pinned to **v2.135.0**, supporting
all three evaluation modes the requirements mandate:

1. **Role-based, with role hierarchy** — actor roles expand through casbin's `g`
   grouping graph (e.g. `manager` inherits `employee`) before being matched
   against `AuthzSpec.Roles`. This preserves the existing inline-`CandidateRoles`
   any-match semantics and *adds* inheritance.
2. **Resource-privilege** — activates the previously-reserved
   `AuthzSpec.Privileges` field via casbin `Enforce(sub, obj, act)`.
3. **Attribute-based over process/data variables** — the existing `expr-lang`
   predicate in `AuthzSpec.Attribute`, evaluated over `{actor, vars}`, is kept
   verbatim (casbin's `eval()` uses govaluate, not expr-lang — we do **not**
   unify; see ADR-0010 §Decision).

Two non-negotiables from CLAUDE.md frame everything:

- **casbin is reachable only behind `authz.Authorizer`.** It is imported solely
  in `internal/authz/casbin/` (concrete impl) and `casbinauthz/` (root façade).
  The `authz` package itself **stays pure** (stdlib + `expreval` only) — casbin
  is never added to it; `engine`, `runtime`, `model`, `humantask` never import
  casbin.
- **Façade-over-internal (ADR-0008)** — the concrete impl lives in `internal/`,
  the consumer constructs it through a thin module-root package that returns the
  stable port type.

This sub-project also fixes a latent gap: today `TaskService` passes **`nil`**
process variables to `Authorize`, so attribute-based-over-data-variables cannot
actually fire at a claim. We plumb a deterministic variable snapshot through, so
mode (3) genuinely works (see §Vars plumbing).

Non-goals (v1): DB-backed policy adapters (pgx/gorm/sqlx) and moving the
attribute predicate into casbin matchers — both are documented follow-ups
(§Deferred).

## Layout (per ADR-0008)

| Package | Role | casbin imported? |
|---|---|---|
| `authz/` (root) | the port + pure built-ins (`AllowAll`, `RoleAuthorizer`) — unchanged, stays pure | no |
| `internal/authz/casbin/` | the concrete casbin-backed `Authorizer` (the hybrid evaluator) | **yes — only here** |
| `casbinauthz/` (root) | consumer façade: constructors returning `authz.Authorizer` | **yes — only here** |

- `internal/authz/casbin/` — `Authorizer` struct wrapping a
  `*casbin.SyncedEnforcer`; implements `authz.Authorizer`. Consumers never import
  it; it may change freely without a public semver impact.
- `casbinauthz/` — the product surface:
  - `NewCasbinAuthorizer(e *casbin.SyncedEnforcer) authz.Authorizer` — for a
    consumer who already built/owns an enforcer (their own model, adapter,
    policy source, watcher).
  - `NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error)`
    — builds a `SyncedEnforcer` internally from `model.NewModelFromString` plus
    the built-in `persist/string-adapter` `stringadapter.NewAdapter(policyText)`
    (both ship inside the casbin module — **zero extra deps**). If `modelText`
    is empty, the **default model string** (below) is used.
  - The returned value is the `authz.Authorizer` **interface** (the stable port
    type, ADR-0008) — no internal concrete type leaks, no unusable `Option`
    variadics. The concrete façade type additionally exposes
    `ReloadPolicy() error` (delegating to the enforcer's `LoadPolicy`) so a
    consumer who keeps the `*SyncedEnforcer` reference can hot-reload after a
    policy change; type-assert for it.

## The Authorizer design — HYBRID (the core)

casbin owns the **RBAC graph + resource-privilege**; `expr-lang` keeps the
**`Attribute` predicate**. `Authorize(ctx, spec, actor, vars)` runs the three
checks in order, short-circuiting on the first denial. An **empty `AuthzSpec`
allows** (no restriction in any dimension).

### Step 1 — role check, with hierarchy

If `spec.Roles` is non-empty: expand the actor's effective role set through
casbin's grouping graph, then require it to intersect `spec.Roles`. The effective
set is the union, over each seed in `actor.Roles` **and** `actor.ID`, of:

- the seed itself, plus
- `e.GetImplicitRolesForUser(seed)` — casbin's transitive `g`-closure.

```go
func (a *Authorizer) effectiveRoles(actor authz.Actor) (map[string]struct{}, error) {
	eff := make(map[string]struct{})
	seeds := append([]string{actor.ID}, actor.Roles...)
	for _, s := range seeds {
		if s == "" {
			continue
		}
		eff[s] = struct{}{}
		implicit, err := a.enforcer.GetImplicitRolesForUser(s)
		if err != nil {
			return nil, fmt.Errorf("casbin: implicit roles for %q: %w", s, err)
		}
		for _, r := range implicit {
			eff[r] = struct{}{}
		}
	}
	return eff, nil
}
```

We seed with `actor.ID` as well as `actor.Roles` because casbin policies usually
attach grouping to a *subject* (`g, alice, manager`); seeding both lets a
consumer model hierarchy off either the principal or named roles. When no `g`
policy matches a seed, `GetImplicitRolesForUser` returns just the seed (or an
empty slice), so the behaviour **degrades exactly to the existing
`RoleAuthorizer` any-match** — a fixture with no `g` lines must produce identical
allow/deny results. (The implementer verifies this equivalence against a real
model+policy fixture; see the plan's Task 1.)

Empty `spec.Roles` ⇒ this step is skipped (no role restriction).

### Step 2 — resource-privilege

If `spec.Privileges` is non-empty: for each privilege, call casbin
`Enforce(...)` mapping the actor (subject) and the privilege (object/action) per
the model. The privilege string is treated as `"obj act"` if it contains a
space, else as a single object token with action `"*"` — the default model's
matcher accepts both. Allow if **any** privilege enforces true; deny if none do.

```go
for _, priv := range spec.Privileges {
	obj, act := splitPrivilege(priv) // "doc read" → ("doc","read"); "approve" → ("approve","*")
	for _, sub := range subjects(actor) { // actor.ID + actor.Roles
		ok, err := a.enforcer.Enforce(sub, obj, act)
		if err != nil {
			return fmt.Errorf("casbin: enforce %q/%q/%q: %w", sub, obj, act, err)
		}
		if ok {
			goto privilegeOK
		}
	}
}
return authz.ErrNotAuthorized
privilegeOK:
```

Empty `spec.Privileges` ⇒ skipped.

### Step 3 — attribute predicate

If `spec.Attribute != ""`: evaluate via the in-repo `expreval` over
`{"actor": actor, "vars": vars}` — **identical** to `RoleAuthorizer` (we reuse
the same `expreval.New()` shared evaluator pattern). Must be `true`.

```go
if spec.Attribute != "" {
	ok, err := a.attrEval.EvalBool(spec.Attribute, map[string]any{"actor": actor, "vars": vars})
	if err != nil {
		return fmt.Errorf("%w: attribute predicate: %w", authz.ErrNotAuthorized, err)
	}
	if !ok {
		return authz.ErrNotAuthorized
	}
}
return nil
```

### Why expr stays (not unified into casbin)

casbin's ABAC `eval()` matcher uses **govaluate**, a different expression
language from `expr-lang/expr`. Definitions already author `EligibilityExpr`
(→ `AuthzSpec.Attribute`) in expr syntax, and `RoleAuthorizer` evaluates it that
way. Re-expressing those predicates in govaluate would split the expression
dialect across the codebase and break existing definitions. So: casbin handles
the graph + enforce, expr handles the predicate. This is the central decision of
ADR-0010.

### Error mapping

- A casbin **deny** (`Enforce` → `false, nil`) or a failed role-intersection /
  false attribute predicate → **`authz.ErrNotAuthorized`** (a clean denial;
  `errors.Is(err, authz.ErrNotAuthorized)` is true).
- A casbin or expr **internal error** (`GetImplicitRolesForUser` / `Enforce` /
  `EvalBool` returning a non-nil `err`) → a **wrapped error that propagates the
  cause**. Attribute-eval errors are wrapped *with* `ErrNotAuthorized`
  (mirroring `RoleAuthorizer`, so a malformed predicate is treated as a denial);
  genuine casbin engine errors are propagated as plain wrapped errors (not
  denials) so the runtime can distinguish "policy says no" from "policy engine
  broke".

### Concurrency

The authorizer wraps a **`*casbin.SyncedEnforcer`** (casbin's RWMutex-guarded
enforcer). `Authorize` is read-only against it (`Enforce`,
`GetImplicitRolesForUser`) and is therefore safe for concurrent calls — multiple
human-task claims can authorize in parallel. `ReloadPolicy`/`LoadPolicy` take the
write lock. The shared `expreval` evaluator is already concurrency-safe
(referentially-transparent memoization, per `authz/authz.go`).

### Default casbin model string

`NewCasbinAuthorizerFromStrings` uses this when `modelText` is empty — a combined
RBAC (`g`) + resource-privilege (`p`) model:

```ini
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && (r.obj == p.obj || p.obj == "*") && (r.act == p.act || p.act == "*")
```

- `g = _, _` is the role graph used by both `GetImplicitRolesForUser` (Step 1)
  and the `Enforce` matcher (Step 2).
- The matcher resolves the subject through `g`, then matches object and action
  with `*` wildcards, so a policy `p, manager, *, *` grants a manager every
  privilege, and `p, employee, doc, read` is a narrow grant.
- The attribute predicate is **not** in this model — it lives in expr (Step 3) by
  design.

Documented so a consumer can either accept the default or pass their own model
text (e.g. one adding domains/tenants).

## Vars plumbing (makes attribute-over-data-variables actually work)

Today `TaskService.Claim/Reassign/Complete` pass **`nil`** vars to `Authorize`
(see `runtime/taskservice.go` — the store holds only the `HumanTask` record, not
instance `Variables`). So a `spec.Attribute` referencing `vars[...]` cannot be
satisfied at claim. Fix, end to end:

1. Add `Vars map[string]any` to `humantask.HumanTask`.
2. In `runtime/runner.go`, `perform engine.AwaitHuman`: when building/upserting
   the task, **snapshot `st.Variables` into `task.Vars`** as a *copy* (defensive
   clone, so later instance mutation can't retroactively change the task's
   eligibility view).
3. `TaskService.Claim/Reassign/Complete` pass **`task.Vars`** (not `nil`) to
   `Authorize`.

Eligibility is thus evaluated against the **task-creation-time variable
snapshot** — deterministic and auditable (the claim decision can't drift as the
instance evolves). The `TaskService` doc comment claiming "we pass nil vars" is
corrected. Existing taskservice tests asserting nil-vars behaviour are updated.

## Version / deps

- `github.com/casbin/casbin/v2` pinned to **v2.135.0**. Its `model` and
  `persist/string-adapter` subpackages ship within the same module — **no
  separate dependency** is added for the from-strings constructor.
- v1 is **adapter-agnostic**: the consumer supplies a model+policy *string* or a
  pre-built `*casbin.SyncedEnforcer` (their own adapter). No database adapter is
  pulled in.

## Deferred follow-ups

- **DB-backed policy adapter** (pgx/gorm/sqlx casbin adapter) so policies live in
  Postgres alongside engine state, with a watcher for multi-node reload. v1 is
  string/enforcer-only.
- **casbin ABAC in matchers** as an *alternative* to the expr `Attribute`
  predicate (govaluate `eval()` over an ABAC request) — only if a consumer wants
  variable predicates expressed as casbin policy rather than definition expr.
- **Richer resource modeling** — domains/tenants (`g = _, _, _`), object
  hierarchies, action grouping — once a concrete multi-tenant requirement lands.

## Verification gate

- `go test -race ./...` green; ≥85% line coverage on `internal/authz/casbin/`,
  `casbinauthz/`, and the touched `runtime`/`humantask` code.
- `golangci-lint run ./...` clean.
- casbin is imported **only** under `casbinauthz/` and `internal/authz/` — a grep
  asserts it never appears in `engine/`, `runtime/`, `model/`, `humantask/`, or
  `authz/`.
- The `authz` package's import set is unchanged (stdlib + `expreval`).
