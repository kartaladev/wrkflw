# 10. casbin-backed Authorizer behind the pure authz port

- Status: Accepted
- Date: 2026-06-21

## Context

CLAUDE.md mandates **casbin as the baseline authorization engine**, supporting
role-based, resource-privilege-based, **and attribute-based** (over process/data
variables) evaluation. Two structural rules constrain how it lands:

- casbin must **never be imported from engine/workflow code** — it is reached only
  behind the in-repo `authz.Authorizer` port, so the vendor stays swappable.
- ADR-0008 established the **façade-over-internal** layout as the template for the
  watermill, gocron, and casbin sub-projects: the concrete impl lives in
  `internal/`, the consumer constructs it through a thin module-root package that
  returns the stable port type.

The `authz` package already defines the port and two pure built-ins:
`AllowAll` and `RoleAuthorizer` (role any-match + an `expr-lang` attribute
predicate over `{actor, vars}`). `AuthzSpec` carries `Roles`, a **reserved**
`Privileges` field, and an `Attribute` expr string. The `authz` package imports
only stdlib and the in-repo `expreval` — it is intentionally pure.

Two facts shape the design:

1. **Expression-language mismatch.** casbin's ABAC matcher uses `eval()` over
   **govaluate**, a different language from `github.com/expr-lang/expr`. The
   engine already authors eligibility predicates in expr (`EligibilityExpr` →
   `AuthzSpec.Attribute`), and `RoleAuthorizer` evaluates them that way. Pushing
   those predicates into casbin matchers would fork the expression dialect and
   break existing definitions.
2. **vars is nil at the call site.** `TaskService.Claim/Reassign/Complete`
   currently pass `nil` process variables to `Authorize`, because the `TaskStore`
   holds only the `HumanTask` record, not instance `Variables`. So
   attribute-based-over-data-variables cannot actually fire at a claim today —
   the capability is contractually present but inert.

## Decision

Add a **casbin v2.135.0-backed `authz.Authorizer`** as the baseline production
authorizer, following the ADR-0008 façade/internal split, and plumb a variable
snapshot to the call site so attribute evaluation works end to end.

- **`internal/authz/casbin/`** holds the concrete `*Authorizer` wrapping a
  `*casbin.SyncedEnforcer`. It owns the **only** casbin imports in the codebase
  (besides the façade). Consumers never import it.
- **`casbinauthz/`** (module root) is the consumer façade:
  `NewCasbinAuthorizer(e *casbin.SyncedEnforcer) authz.Authorizer` for a
  consumer-owned enforcer, and
  `NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error)`
  which builds a `SyncedEnforcer` internally from `model.NewModelFromString` plus
  the built-in `persist/string-adapter` (both ship inside the casbin module — no
  extra dependency). Both **return the `authz.Authorizer` interface** (the stable
  port type) — no internal-concrete leak, no unusable `Option` variadics
  (heeding the persistence-façade lessons). When `modelText` is empty, a
  documented **default combined RBAC + resource-privilege model string** is used.
  The concrete façade type additionally offers `ReloadPolicy() error` for
  hot-reload; consumers type-assert for it.

- **The evaluator is HYBRID.** casbin owns the RBAC graph and resource-privilege;
  `expr-lang` keeps the `Attribute` predicate. `Authorize` runs three
  short-circuiting checks (empty spec ⇒ allow):
  1. **Role check with hierarchy** — if `spec.Roles` is non-empty, expand the
     actor's roles (and ID) through casbin's `g` graph via
     `GetImplicitRolesForUser`, then require the expanded set to intersect
     `spec.Roles`. With no `g` policy, this degrades **exactly** to the existing
     `RoleAuthorizer` any-match.
  2. **Resource-privilege** — if `spec.Privileges` is non-empty (this
     **activates the previously-reserved field**), `Enforce(sub, obj, act)` for
     each privilege over the actor's subjects; allow if any enforces true.
  3. **Attribute predicate** — if `spec.Attribute != ""`, evaluate via the
     in-repo `expreval` over `{actor, vars}`, identical to `RoleAuthorizer`.
  Error mapping: a casbin deny or failed role/attribute check →
  `authz.ErrNotAuthorized`; a malformed attribute predicate → wrapped *with*
  `ErrNotAuthorized` (treated as denial, as `RoleAuthorizer` does); a genuine
  casbin engine error → propagated as a plain wrapped error (not a denial), so
  the runtime distinguishes "policy says no" from "policy engine broke".

- **`*casbin.SyncedEnforcer`** (not `Enforcer`) is used so concurrent human-task
  authorizations are race-safe; `Authorize` is read-only against it.

- **The `authz` package stays pure** — casbin is **not** added to it.
  `AllowAll`/`RoleAuthorizer` remain the pure built-ins; the casbin authorizer is
  an additional implementation behind the same port.

- **Vars plumbing.** Add `Vars map[string]any` to `humantask.HumanTask`;
  `runtime/runner.go`'s `perform engine.AwaitHuman` snapshots a **copy** of
  `st.Variables` into the task at creation; `TaskService` passes `task.Vars` (not
  `nil`) to `Authorize`. Eligibility is evaluated against the
  task-creation-time variable snapshot — deterministic and auditable.

- **v1 is adapter-agnostic.** The consumer supplies a model+policy string or a
  pre-built `*SyncedEnforcer`. A **DB-backed policy adapter** (pgx/gorm/sqlx) and
  **casbin ABAC-in-matchers** as an alternative to the expr predicate are
  **deferred** follow-ups.

## Consequences

**Easier:** the baseline production authorizer is a drop-in `authz.Authorizer`,
constructed through one root package, with casbin fully encapsulated — `engine`,
`runtime`, `model`, `humantask`, and `authz` never see casbin, honouring the
vendor-isolation rule and reusing the ADR-0008 template verbatim. The hybrid keeps
existing expr eligibility predicates working unchanged while adding role
hierarchy and resource-privilege enforcement. With no `g` policy the role check
is behaviourally identical to `RoleAuthorizer`, so adopting casbin is a
non-breaking superset. The from-strings constructor needs **zero extra
dependencies** (model + string-adapter ship inside casbin). The vars-snapshot fix
finally makes attribute-based-over-data-variables fire at a real claim, closing a
latent contract gap, and the snapshot semantics make the claim decision
deterministic.

**Harder / trade-offs:** the codebase now carries two expression dialects — expr
(attribute predicates) and govaluate (only if a consumer authors a casbin ABAC
matcher) — a deliberate split that must be documented so contributors don't try
to unify them. `GetImplicitRolesForUser` seeds on both `actor.ID` and
`actor.Roles`, which a consumer must understand when authoring `g` policies.
`SyncedEnforcer` adds RWMutex contention under heavy reload, and `ReloadPolicy`
is surfaced only via type assertion (the port stays minimal). Until the DB
adapter lands, policies must be supplied as strings or via a consumer-built
enforcer, and multi-node policy reload is the consumer's responsibility — the
known v1 caveat and a tracked follow-up.
