# Plan: casbin-backed Authorizer + vars plumbing

## Goal

Ship a **casbin v2.135.0**-backed `authz.Authorizer` — the baseline production
authorizer — supporting **role-based (with role hierarchy)**,
**resource-privilege**, and **attribute-based (over process/data variables)**
evaluation, reachable **only** behind the `authz.Authorizer` port. Keep
`AllowAll`/`RoleAuthorizer` as the pure built-ins. Plumb a deterministic process-
variable snapshot to the `Authorize` call site so attribute-based-over-data-
variables actually fires at a human-task claim (today `vars` is `nil` there).

Source of truth: `docs/specs/2026-06-21-authz-casbin-design.md` and
ADR-0010. Read both before starting.

## Architecture

- `authz/` (root) — the port + pure built-ins. **Unchanged; stays pure**
  (stdlib + `expreval`). casbin is NOT added here.
- `internal/authz/casbin/` — the concrete hybrid `Authorizer` over a
  `*casbin.SyncedEnforcer`. The **only** package (besides the façade) importing
  casbin. Implements `authz.Authorizer`. Three short-circuiting checks: casbin
  role-hierarchy expansion → casbin resource-privilege `Enforce` → expr
  `Attribute` predicate. Maps casbin deny / failed checks → `authz.ErrNotAuthorized`;
  propagates genuine engine errors wrapped.
- `casbinauthz/` (root) — consumer façade: `NewCasbinAuthorizer` (consumer-owned
  enforcer) and `NewCasbinAuthorizerFromStrings` (builds a `SyncedEnforcer` from
  model+policy strings via `model.NewModelFromString` + `persist/string-adapter`;
  default model when model text empty). Both return the `authz.Authorizer`
  interface (ADR-0008). Façade type adds `ReloadPolicy() error`.
- Vars plumbing: `humantask.HumanTask.Vars` ← copy of `st.Variables` at
  `AwaitHuman` upsert in `runtime/runner.go`; `runtime/taskservice.go` passes
  `task.Vars` to `Authorize`.

## Tech Stack

Go 1.25.7 · `github.com/casbin/casbin/v2 v2.135.0` (with bundled `model` and
`persist/string-adapter`) · existing `github.com/zakyalvan/krtlwrkflw/expreval`
for the attribute predicate.

## Global Constraints

Go 1.25; casbin pinned `github.com/casbin/casbin/v2 v2.135.0`; NEVER import
casbin from engine/runtime/model/workflow code (only `internal/authz/` +
`casbinauthz/`); the `authz` package stays pure (stdlib + expreval only — do NOT
add casbin to it); TDD strict VISIBLE red→green; black-box tests
(`package x_test`); table tests `assert` closure form (not want/wantErr);
`t.Context()`; pair foo.go+foo_test.go; ≥85% coverage on touched packages;
conventional commits ending with
`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## Task 1 — casbin Authorizer (`internal/authz/casbin`)

The bulk. Add the dependency and implement the hybrid evaluator over a real
model+policy fixture, TDD.

**Files**
- `internal/authz/casbin/authorizer.go`
- `internal/authz/casbin/authorizer_test.go` (`package casbin_test`)
- `go.mod` / `go.sum` (add casbin)

**Interfaces / symbols**
- `type Authorizer struct { enforcer *casbin.SyncedEnforcer; attrEval *expreval.Evaluator }`
- `func New(e *casbin.SyncedEnforcer) *Authorizer`
- `func (a *Authorizer) Authorize(ctx context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error`
- compile assert: `var _ authz.Authorizer = (*Authorizer)(nil)`

**Steps**

- [ ] Add the dependency: `go get github.com/casbin/casbin/v2@v2.135.0`, then
      `go mod tidy`. Confirm `go.mod` pins `v2.135.0`.
- [ ] **RED**: write `authorizer_test.go` with a fixture model+policy string and a
      table covering role-hierarchy allow/deny, resource-privilege allow/deny,
      attribute allow/deny (over actor AND vars), combined, and deny→`ErrNotAuthorized`
      mapping. Run `go test ./internal/authz/casbin/...` — must fail to compile
      (`undefined: New`).
- [ ] **GREEN**: implement `authorizer.go` (role expansion via
      `GetImplicitRolesForUser`, privilege via `Enforce`, attribute via
      `attrEval.EvalBool`, error mapping). Re-run — green.
- [ ] **REFACTOR**: extract `effectiveRoles`, `splitPrivilege`, `subjects` helpers;
      re-run.
- [ ] Coverage ≥85%: `go test -race -coverprofile=cover.out ./internal/authz/casbin/... && go tool cover -func=cover.out | tail -1`.

**Test (real, no placeholders)** — `internal/authz/casbin/authorizer_test.go`:

```go
package casbin_test

import (
	"errors"
	"testing"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	casbinauthz "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
)

const testModel = `
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
`

// alice is a manager; manager inherits employee; manager may approve anything.
const testPolicy = `
p, manager, approve, *
p, employee, doc, read
g, alice, manager
g, manager, employee
`

func newEnforcer(t *testing.T) *casbinv2.SyncedEnforcer {
	t.Helper()
	m, err := casbinmodel.NewModelFromString(testModel)
	require.NoError(t, err)
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(testPolicy))
	require.NoError(t, err)
	return e
}

func TestAuthorizer_Authorize(t *testing.T) {
	alice := authz.Actor{ID: "alice", Roles: []string{"manager"}, Attributes: map[string]any{"region": "EU"}}
	bob := authz.Actor{ID: "bob", Roles: []string{"employee"}}

	cases := map[string]struct {
		spec   authz.AuthzSpec
		actor  authz.Actor
		vars   map[string]any
		assert func(t *testing.T, err error)
	}{
		"role hierarchy: manager satisfies employee": {
			spec:   authz.AuthzSpec{Roles: []string{"employee"}},
			actor:  alice,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"role deny: employee does not satisfy manager": {
			spec:  authz.AuthzSpec{Roles: []string{"manager"}},
			actor: bob,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"empty roles: no restriction": {
			spec:   authz.AuthzSpec{},
			actor:  bob,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"privilege allow: manager may approve": {
			spec:   authz.AuthzSpec{Privileges: []string{"approve"}},
			actor:  alice,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"privilege deny: employee may not approve": {
			spec:  authz.AuthzSpec{Privileges: []string{"approve"}},
			actor: bob,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"attribute allow over actor": {
			spec:   authz.AuthzSpec{Attribute: `actor.Attributes["region"] == "EU"`},
			actor:  alice,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"attribute deny over vars": {
			spec:  authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
			actor: alice,
			vars:  map[string]any{"region": "US"},
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"attribute allow over vars": {
			spec:   authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
			actor:  alice,
			vars:   map[string]any{"region": "EU"},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"combined role+privilege+attribute allow": {
			spec: authz.AuthzSpec{
				Roles:      []string{"employee"},
				Privileges: []string{"approve"},
				Attribute:  `vars["region"] == "EU"`,
			},
			actor:  alice,
			vars:   map[string]any{"region": "EU"},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"malformed attribute predicate maps to ErrNotAuthorized": {
			spec:  authz.AuthzSpec{Attribute: `this is not (valid expr`},
			actor: alice,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a := casbinauthz.New(newEnforcer(t))
			err := a.Authorize(t.Context(), tc.spec, tc.actor, tc.vars)
			tc.assert(t, err)
		})
	}
}

func TestAuthorizer_ImplementsPort(t *testing.T) {
	var _ authz.Authorizer = casbinauthz.New(newEnforcer(t))
	assert.True(t, errors.Is(authz.ErrNotAuthorized, authz.ErrNotAuthorized))
}
```

**Implementation (real, no placeholders)** — `internal/authz/casbin/authorizer.go`:

```go
// Package casbin provides the casbin-backed implementation of authz.Authorizer.
// It is internal: consumers construct it through the module-root casbinauthz
// package. This is the only package (besides casbinauthz) that imports casbin.
package casbin

import (
	"context"
	"fmt"
	"strings"

	casbinv2 "github.com/casbin/casbin/v2"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/expreval"
)

// Authorizer is a hybrid authz.Authorizer: casbin owns the RBAC role graph and
// resource-privilege enforcement; expr-lang (via expreval) owns the attribute
// predicate. It wraps a *casbin.SyncedEnforcer so concurrent Authorize calls are
// race-safe.
type Authorizer struct {
	enforcer *casbinv2.SyncedEnforcer
	attrEval *expreval.Evaluator
}

var _ authz.Authorizer = (*Authorizer)(nil)

// New constructs an Authorizer over the given synced enforcer.
func New(e *casbinv2.SyncedEnforcer) *Authorizer {
	return &Authorizer{enforcer: e, attrEval: expreval.New()}
}

// Authorize evaluates the three checks in order, short-circuiting on the first
// denial. An empty spec allows. A casbin deny or a failed role/attribute check
// returns authz.ErrNotAuthorized; a genuine casbin/expr engine error is wrapped
// and propagated.
func (a *Authorizer) Authorize(_ context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error {
	// Step 1: role check with hierarchy.
	if len(spec.Roles) > 0 {
		eff, err := a.effectiveRoles(actor)
		if err != nil {
			return err
		}
		if !intersects(eff, spec.Roles) {
			return authz.ErrNotAuthorized
		}
	}

	// Step 2: resource-privilege.
	if len(spec.Privileges) > 0 {
		ok, err := a.anyPrivilege(actor, spec.Privileges)
		if err != nil {
			return err
		}
		if !ok {
			return authz.ErrNotAuthorized
		}
	}

	// Step 3: attribute predicate (expr-lang).
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
}

// effectiveRoles expands the actor's roles and ID through the casbin g graph.
func (a *Authorizer) effectiveRoles(actor authz.Actor) (map[string]struct{}, error) {
	eff := make(map[string]struct{})
	for _, seed := range subjects(actor) {
		eff[seed] = struct{}{}
		implicit, err := a.enforcer.GetImplicitRolesForUser(seed)
		if err != nil {
			return nil, fmt.Errorf("casbin: implicit roles for %q: %w", seed, err)
		}
		for _, r := range implicit {
			eff[r] = struct{}{}
		}
	}
	return eff, nil
}

// anyPrivilege reports whether any (subject, privilege) pair enforces true.
func (a *Authorizer) anyPrivilege(actor authz.Actor, privileges []string) (bool, error) {
	for _, priv := range privileges {
		obj, act := splitPrivilege(priv)
		for _, sub := range subjects(actor) {
			ok, err := a.enforcer.Enforce(sub, obj, act)
			if err != nil {
				return false, fmt.Errorf("casbin: enforce %q/%q/%q: %w", sub, obj, act, err)
			}
			if ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// subjects returns the casbin subjects for an actor: its ID followed by its roles.
func subjects(actor authz.Actor) []string {
	subs := make([]string, 0, 1+len(actor.Roles))
	if actor.ID != "" {
		subs = append(subs, actor.ID)
	}
	subs = append(subs, actor.Roles...)
	return subs
}

// splitPrivilege parses "obj act" into (obj, act); a single token gets act "*".
func splitPrivilege(priv string) (obj, act string) {
	if obj, act, ok := strings.Cut(priv, " "); ok {
		return obj, act
	}
	return priv, "*"
}

// intersects reports whether any value in want is present in have.
func intersects(have map[string]struct{}, want []string) bool {
	for _, w := range want {
		if _, ok := have[w]; ok {
			return true
		}
	}
	return false
}
```

> Note: the implementer verifies the exact casbin call surface against
> `v2.135.0` — `GetImplicitRolesForUser(name) ([]string, error)` and
> `Enforce(...interface{}) (bool, error)` are the v2 signatures; adjust only if
> the pinned version differs.

---

## Task 2 — `casbinauthz` root façade

The consumer-facing constructors. Returns the `authz.Authorizer` interface
(ADR-0008): no internal-type leak, no unusable Option variadics.

**Files**
- `casbinauthz/casbinauthz.go`
- `casbinauthz/casbinauthz_test.go` (`package casbinauthz_test`)

**Interfaces / symbols**
- `const DefaultModel = "..."` (the combined RBAC + resource-privilege model)
- `func NewCasbinAuthorizer(e *casbin.SyncedEnforcer) authz.Authorizer`
- `func NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error)` — empty `modelText` ⇒ `DefaultModel`
- `type Authorizer struct{...}` wrapping the internal one + the enforcer, with `ReloadPolicy() error`

**Steps**

- [ ] **RED**: write `casbinauthz_test.go` — a from-strings round-trip (construct
      with `DefaultModel`+policy, assert an allow and a deny end-to-end through the
      returned `authz.Authorizer`), an empty-model defaulting case, and a
      `ReloadPolicy` type-assert case. Run — fails to compile.
- [ ] **GREEN**: implement `casbinauthz.go` delegating to
      `internal/authz/casbin`. Re-run — green.
- [ ] Coverage ≥85%.

**Test (real)** — `casbinauthz/casbinauthz_test.go`:

```go
package casbinauthz_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/casbinauthz"
)

const policy = `
p, manager, approve, *
g, alice, manager
`

func TestEndToEnd_AllowDeny(t *testing.T) {
	a, err := casbinauthz.NewCasbinAuthorizerFromStrings("", policy) // "" ⇒ DefaultModel
	require.NoError(t, err)

	alice := authz.Actor{ID: "alice"}
	bob := authz.Actor{ID: "bob"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}

	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
	assert.ErrorIs(t, a.Authorize(t.Context(), spec, bob, nil), authz.ErrNotAuthorized)
}

func TestReloadPolicy_ViaTypeAssertion(t *testing.T) {
	a, err := casbinauthz.NewCasbinAuthorizerFromStrings(casbinauthz.DefaultModel, policy)
	require.NoError(t, err)

	reloader, ok := a.(interface{ ReloadPolicy() error })
	require.True(t, ok)
	assert.NoError(t, reloader.ReloadPolicy())
}
```

**Implementation (real)** — `casbinauthz/casbinauthz.go`:

```go
// Package casbinauthz is the consumer-facing façade for the casbin-backed
// authz.Authorizer. It is the only module-root package that imports casbin; the
// concrete evaluator lives in internal/authz/casbin.
package casbinauthz

import (
	"fmt"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"

	"github.com/zakyalvan/krtlwrkflw/authz"
	internalcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
)

// DefaultModel is a combined RBAC (g) + resource-privilege (p) casbin model used
// when NewCasbinAuthorizerFromStrings receives an empty model text. The attribute
// predicate is NOT modeled here — it is evaluated by expr-lang (see ADR-0010).
const DefaultModel = `
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
`

// Authorizer is the façade type. It satisfies authz.Authorizer and additionally
// exposes ReloadPolicy for consumers who want to hot-reload after a policy change.
type Authorizer struct {
	inner    *internalcasbin.Authorizer
	enforcer *casbinv2.SyncedEnforcer
}

var _ authz.Authorizer = (*Authorizer)(nil)

// Authorize delegates to the internal casbin evaluator.
func (a *Authorizer) Authorize(ctx context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error {
	return a.inner.Authorize(ctx, spec, actor, vars)
}

// ReloadPolicy reloads the enforcer's policy from its adapter.
func (a *Authorizer) ReloadPolicy() error {
	if err := a.enforcer.LoadPolicy(); err != nil {
		return fmt.Errorf("casbinauthz: reload policy: %w", err)
	}
	return nil
}

// NewCasbinAuthorizer wraps a consumer-built synced enforcer.
func NewCasbinAuthorizer(e *casbinv2.SyncedEnforcer) authz.Authorizer {
	return &Authorizer{inner: internalcasbin.New(e), enforcer: e}
}

// NewCasbinAuthorizerFromStrings builds a SyncedEnforcer from model + policy
// strings (using casbin's bundled string adapter) and returns the authorizer.
// An empty modelText uses DefaultModel.
func NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error) {
	if modelText == "" {
		modelText = DefaultModel
	}
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return nil, fmt.Errorf("casbinauthz: parse model: %w", err)
	}
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(policyText))
	if err != nil {
		return nil, fmt.Errorf("casbinauthz: build enforcer: %w", err)
	}
	return NewCasbinAuthorizer(e), nil
}
```

> `Authorize` needs `context` imported — add `"context"` to the import block.

---

## Task 3 — Vars plumbing

Make attribute-based-over-data-variables fire at a claim. `HumanTask` gains
`Vars`; the runner snapshots `st.Variables` (copy) at `AwaitHuman`; `TaskService`
passes `task.Vars`.

**Files**
- `humantask/humantask.go` (+ `humantask/humantask_test.go` if a behavioural assert is added)
- `runtime/runner.go`
- `runtime/runner_test.go` (or a focused new test) — black-box `runtime_test`
- `runtime/taskservice.go`
- `runtime/taskservice_test.go`

**Interfaces / symbols**
- `humantask.HumanTask.Vars map[string]any`
- runner: snapshot `st.Variables` → `task.Vars` (defensive copy) at `AwaitHuman`
- `TaskService.Claim/Reassign/Complete` pass `task.Vars` (not `nil`)

**Steps**

- [ ] **RED**: in `taskservice_test.go`, add a case: a task whose
      `Eligibility.Attribute` is `vars["region"] == "EU"` and `task.Vars` =
      `{"region":"EU"}` claims successfully with a `RoleAuthorizer`; with
      `{"region":"US"}` it returns `ErrNotAuthorized`. This fails today because
      `Claim` passes `nil` (the predicate errors / denies). Run — red.
- [ ] **GREEN (taskservice)**: change the three `Authorize` calls to pass
      `task.Vars`; fix the misleading "we pass nil vars" doc comments. Re-run — green.
- [ ] **RED (runner)**: add a runner test asserting that after an `AwaitHuman`
      step, the upserted task's `Vars` equals a copy of the instance variables
      (and mutating the instance afterward does not change `task.Vars`). Red.
- [ ] **GREEN (runner)**: add `Vars` field to `HumanTask`; in `perform
      engine.AwaitHuman`, set `task.Vars = maps.Clone(st.Variables)` (defensive
      copy). Re-run — green.
- [ ] Update any existing taskservice/runner tests that asserted nil-vars behavior.
- [ ] Coverage ≥85% on `runtime` and `humantask` touched code.

**Test sketch (real)** — addition to `runtime/taskservice_test.go`:

```go
func TestTaskService_Claim_AttributeOverVars(t *testing.T) {
	cases := map[string]struct {
		vars   map[string]any
		assert func(t *testing.T, err error)
	}{
		"matching region claims": {
			vars:   map[string]any{"region": "EU"},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"non-matching region denied": {
			vars: map[string]any{"region": "US"},
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := humantask.NewMemoryStore() // existing memory store
			require.NoError(t, store.Upsert(t.Context(), humantask.HumanTask{
				TaskToken:   "tok-1",
				Eligibility: authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
				Vars:        tc.vars,
				State:       humantask.Unclaimed,
			}))
			svc := runtime.NewTaskService(store, authz.RoleAuthorizer{}, clockwork.NewFakeClock())
			_, err := svc.Claim(t.Context(), "tok-1", authz.Actor{ID: "alice"})
			tc.assert(t, err)
		})
	}
}
```

> Adjust `humantask.NewMemoryStore` / clock constructor to the actual helpers in
> the repo (`humantask/memory.go`, the shared `clock`/`clockwork` test clock).

**Runner change** — in `runtime/runner.go`, `case engine.AwaitHuman`, after
building `task`:

```go
task.Vars = maps.Clone(st.Variables) // defensive snapshot for deterministic eligibility
```

(add `"maps"` to imports). `maps.Clone(nil)` returns `nil`, which is fine.

---

## Task 4 — Verification + HANDOVER

**Files**
- `docs/plans/HANDOVER.md` (Authorization section + deferred follow-ups)

**Steps**

- [ ] `go test -race ./...` — green, no regressions.
- [ ] Per-package coverage ≥85% on `internal/authz/casbin`, `casbinauthz`, and
      the touched `runtime`/`humantask` code:
      `go test -race -coverprofile=cover.out ./internal/authz/casbin/... ./casbinauthz/... ./runtime/... ./humantask/... && go tool cover -func=cover.out | tail -1`.
- [ ] `golangci-lint run ./...` — clean.
- [ ] **Vendor-isolation assertion** — confirm casbin is imported ONLY under the
      two allowed trees:
      ```bash
      grep -rl "casbin/casbin" --include=*.go . | grep -vE '(^|/)casbinauthz/|(^|/)internal/authz/' \
        && echo "LEAK: casbin imported outside allowed packages" && exit 1 || echo "OK: casbin contained"
      ```
      Also confirm `authz/` is untouched: `grep -rl casbin authz/` returns nothing.
- [ ] Update `docs/plans/HANDOVER.md`:
  - Authorization section → "casbin baseline authorizer shipped behind
    `authz.Authorizer`: `internal/authz/casbin` (impl) + `casbinauthz`
    (`NewCasbinAuthorizer`, `NewCasbinAuthorizerFromStrings`, `DefaultModel`,
    `ReloadPolicy`); hybrid role-hierarchy + resource-privilege (casbin) +
    attribute predicate (expr). Vars now plumbed: `HumanTask.Vars` snapshot at
    `AwaitHuman`, passed to `Authorize`."
  - Deferred follow-ups → DB-backed casbin policy adapter (pgx/gorm/sqlx) +
    watcher; casbin ABAC-in-matchers as an alternative to the expr predicate;
    richer resource modeling (domains/tenants, object hierarchies).

## Verification checklist

- [ ] casbin pinned `v2.135.0` in `go.mod`.
- [ ] `internal/authz/casbin` + `casbinauthz` are the ONLY packages importing casbin.
- [ ] `authz` package import set unchanged (stdlib + expreval).
- [ ] Hybrid evaluator: role-hierarchy, resource-privilege, expr attribute, error
      mapping all covered by table tests (assert form).
- [ ] Façade returns `authz.Authorizer`; `ReloadPolicy` via type assertion.
- [ ] `HumanTask.Vars` populated (defensive copy) and passed at Claim/Reassign/Complete.
- [ ] `go test -race ./...` green; touched packages ≥85%; lint clean.
- [ ] HANDOVER updated.
```
