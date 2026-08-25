# ADR-0185-core Implementation Plan — authorization identity, required authorizer, stated eligibility

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development`
> to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> ⛔ **THIS PLAN FAILED ITS RULE-#9 AUDIT (2026-08-23) AND IS NOT AN INPUT TO IMPLEMENTATION.**
> 58 findings, 22 raw Criticals, 21 accepted. Several tasks below are **known wrong**: Task 8's
> `checkSpecStated` fails its own case 5; Task 14's migration corrupts the definitions copy and is
> forbidden by an existing guard; Task 4 case 4 pins backlog 52 open; Task 3 Step 4's compile-breakage
> list is empty; Task 5's blast radius is wrong in both directions; and no task touches
> `definition/model/yaml.go`, `internal/atrest`, or the migration-count guard.
> **Do not execute any task from this plan.** Read
> `docs/plans/sweep-evidence/audit-0185core-adjudication.md` first.

**Goal:** Close the three composing authorization holes — a self-asserted principal, a
silently permissive default authorizer, and an eligibility spec that means allow-all when
it states nothing — without stranding in-flight human tasks on upgrade.

**Architecture:** Three seams. (1) The actor travels in `context.Context` through a public
`authz` helper, resolved **once** in `httpcore`; the three task DTOs lose their actor
fields. (2) `service` requires an authorizer and makes allow-all an explicit, WARN-logged
choice. (3) `AuthzSpec` gains `Open bool` whose zero value **denies**, with a per-dialect
data migration resolving pre-upgrade ambiguity in the database, and a spec-shape gate
hoisted into `runtime/task` that asks each `Authorizer` which dimensions it evaluates.

**Tech Stack:** Go 1.25, `expr-lang/expr`, goose migrations (Postgres 17 / MySQL 8 /
`modernc.org/sqlite`), `stretchr/testify`, `uber-go/mock`, testcontainers-go.

**Spec:** `docs/specs/2026-08-23-authz-identity-core.md` — read it *with* this plan. It
carries the executed premises, the removal grid, and the residuals. This plan argues from
it and does not restate its evidence.

**ADR:** `docs/adr/0185-authorization-identity-is-not-self-asserted.md` (re-cut 2026-08-23).

## ▶ Progress

**Branch: `design/authz-identity-core`.** ⚠ The bundle is ONE commit that gets `--amend`ed, so its
SHA moves — **name the branch, never quote the SHA here**.

| stage | state |
|---|---|
| Scope decision (D1+D2+D3; 103 and 124 deferred) | ✅ owner, 2026-08-23 |
| Spec `docs/specs/2026-08-23-authz-identity-core.md` | ✅ written, committed |
| ADR-0185 re-cut in place | ✅ written, committed |
| This plan | ✅ written, committed |
| **Rule-#9 audit (4 lenses)** | ⏳ **RUNNING** — `docs/plans/sweep-evidence/audit-0185core-*.md` |
| Adjudication + fold | ⬜ blocked on the audit |
| Phases 0–7 | ⬜ blocked on the adjudication |

⛔ **No implementation may start until the audit is adjudicated and accepted fixes are folded.**
A bundle whose Decisions changed has not been audited; this one's have changed **three** times.

### Source-verified facts (executed at the branch point, `d5661d07`)

- `AuthzSpec` is durable in **three** copies: `wrkflw_human_task.eligibility`,
  `wrkflw_instances.snapshot`, `wrkflw_definitions.definition`. Spec §2.1 carries the probe.
- **Five** `Authorizer` implementations, two of them public beyond `authz` itself. Spec §2.3.
- **29** pin sites / 9 files / 5 packages — re-derived, matching the inherited figure, with the
  one exclusion stated. Spec §2.6.
- `RoleAuthorizer` evaluates Roles + Attribute, **not** Privileges (`authz/authz.go:119-120`);
  the casbin authorizer evaluates all three (`:45`, `:56`, `:68`). This is the capability
  interface's entire basis.
- `ProcessDriver.ApplyTrigger` **bypasses authorization by design** and says so in its own godoc.
- Exactly **one** migration file per dialect today ⇒ this bundle lands the first `0002_*.sql`.

### Corrections made during planning (before any code)

1. **The plan's own self-review found a would-be shipped bug**: no task carried `Open` from the
   node into the minted spec at `engine`'s `userTaskStrategy.enter`. Every open task would have
   been minted **closed** — compiling, round-tripping, passing `model.Validate`, then denying
   every actor. Added as **Task 8a**.
2. **Wire key corrected `open` → `eligible_open`** across spec, ADR and plan, to match its three
   siblings `eligible_roles` / `eligible_privileges` / `eligible_expr`. ADR-0144 fixed snake_case
   keys and ADR-0167 made definition decoding strict, so an off-convention key is a decode hazard.
3. **`NewUserTask` takes `(id, opts...)`, not `(id, name, opts...)`, and returns `model.Node`**;
   the option type is `UserTaskOption`. The plan's first draft had all three wrong.
4. **The wire round-trip has TWO mapping sites** (`definition/activity/activity.go:240` and `:251`),
   not one — missing either loses `Open` on the first save/reload.
5. **ADR-0186's option-alias convention reused rather than reinvented**: the generic form cannot
   infer `R`, so both new options need a non-generic per-adapter alias — 2 × 3 = 6.

---

## Global Constraints

Every task's requirements implicitly include this section.

- **TDD is a deliverable, not a means.** For every new symbol and every behavioural change:
  write the test → **run it and observe RED** in a `Bash` call → implement → run GREEN.
  A `Write` of `foo_test.go` followed immediately by a `Write` of `foo.go` with no
  `go test` between them is a **plan violation**, not a shortcut.
- **Judge a test run by its exit code**, never a pipeline tail:
  `go test ./pkg/... > /tmp/out.log 2>&1; echo "EXIT=$?"`, then read the log. Use
  `-count=1`.
- ⚠ **`go test -run` on a nonexistent name exits 0, and anchoring the regex does not
  help.** Confirm a test *ran* with `-v` and `grep -q '^--- PASS: <TestName>'`.
- **Error sentinels** use the `workflow-<pkg>: ` prefix (e.g.
  `errors.New("workflow-authz: spec states nothing")`).
- **Black-box tests** (`package <pkg>_test`) are preferred. ⚠ `engine/` mixes
  `package engine` and `package engine_test` — run `head -1` on any existing test file
  before writing into it.
- **Table tests** use the project's `table-test` skill form (an `assert` closure, not
  `want`/`wantErr` fields; `t.Context()` over `context.Background()`).
- **Mocks** are generated with `mockgen --typed` per the `use-mockgen` skill; never
  hand-write a double for an interface that has one.
- ⚠ **A mutation ablation runs in a `git worktree`, never the shared tree.** A previous
  session lost ~40 minutes to a "hang" that was another agent's live ablation. Restore
  from a `cp` backup — **never `git checkout <path>`**, which restores from the index and
  destroys uncommitted work.
- **Docker**: standing permission for the two Verification runs only (probe first). SQLite
  is pure Go and needs nothing. Container-free packages: `engine`, `runtime/{calllink,
  signal,task}`, `service`, `processtest`, `transport/http`. ⚠ `internal/persistence/store`
  is **not** container-free, but `dbtest.RunTestSQLite` starts nothing.
- **Search the repo for an existing convention BEFORE writing a new symbol.** ADR-0186's
  lineage claimed a gap the repo had already filled **four** times.
- **Fan out by Go package.** Concurrent agents inside one package break each other's
  `go test` compile even on disjoint files.

---

## File Structure

| file | responsibility | phase |
|---|---|---|
| `authz/context.go` *(create)* | `ContextWithActor` / `ActorFromContext` — the identity seam | 0 |
| `authz/dimension.go` *(create)* | `Dimension`, `DimensionEvaluator`, the capability the gate asks | 0 |
| `authz/authz.go` *(modify)* | `AuthzSpec.Open`, two sentinels, `RoleAuthorizer` denial, declarations, three falsified godocs | 0 |
| `service/options.go`, `service/service.go`, `service/durable.go` *(modify)* | `WithAuthorizer`, `WithAllowAllAuthorizer`, constructor error, WARN record, `AuthorizerProvider`, `taskStore` rescope | 1 |
| `definition/activity/activity.go`, `options.go`; `definition/model/node_wire.go`, `validate.go` *(modify)* | `WithOpenEligibility`, wire key `eligible_open`, the authoring gate | 1 |
| `internal/authz/casbin/authorizer.go`, `casbinauthz/casbinauthz.go` *(modify)* | dimension declarations (inner + public delegate) | 1 |
| `processtest/spyauthz.go` *(modify)* | spy declares all three dimensions | 1 |
| `engine/step_nodes.go:722-726` *(modify)* | the mint site carries `Open` from the node into the stored spec — WITHOUT this every open task is minted closed | 2 |
| `runtime/task/gate.go` *(create)*, `service.go` *(modify)* | the hoisted, authorizer-aware spec-shape gate | 2 |
| `transport/http/httpcore/dto.go`, `endpoints.go`, `seam.go`, `errors.go` *(modify)*; `actor.go` *(create)* | DTO field removal, `WithRequestActor`, `WithAnonymousActorAllowed`, 401/503 | 3 |
| `transport/http/{stdlib,gin,fiber,parity}` *(modify, tests)* | 29 pin rewrites; fiber's `c.SetContext` documentation | 4 |
| `internal/persistence/store/migrations/{postgres,mysql,sqlite}/0002_authz_open.sql` *(create)* | backfill `"Open": true` into dimension-less stored specs, three copies | 5 |
| `internal/atrest/classification.go` *(modify)* | backlog 141 — the missing policy-at-rest location | 5 |
| `examples/{production,sqlite,mysql}_wiring/main.go` *(modify)* | `WithAnonymousActorAllowed` | 6 |
| `SECURITY.md`, `docs/adr/0117-*.md` *(modify)* | the fiber idiom, the residuals, ADR-0117 Decisions 1 and 3 annotated | 6 |

---

## Phase 0 — `authz` shared types (INLINE in the controller, strictly serial)

⚠ **Do NOT fan this out.** Adding a field to `authz.AuthzSpec` is a compile-breaking,
repo-wide shared type change that every other phase blocks on. Per CLAUDE.md rule #11 it
stays inline in the controller. All three tasks are in **one package** and run serially.

### Task 1: The identity seam

**Files:**
- Create: `authz/context.go`
- Test: `authz/context_test.go` (`package authz_test`)

**Interfaces:**
- Produces: `authz.ContextWithActor(ctx context.Context, a Actor) context.Context`,
  `authz.ActorFromContext(ctx context.Context) (Actor, bool)`

- [ ] **Step 1: Write the failing test**

```go
package authz_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

func TestActorFromContext(t *testing.T) {
	t.Parallel()

	t.Run("absent reports not ok and a zero actor", func(t *testing.T) {
		t.Parallel()

		got, ok := authz.ActorFromContext(t.Context())

		assert.False(t, ok)
		assert.Equal(t, authz.Actor{}, got)
	})

	t.Run("round-trips a whole actor including Attributes", func(t *testing.T) {
		t.Parallel()

		want := authz.Actor{
			ID:         "alice",
			Roles:      []string{"manager"},
			Attributes: map[string]any{"tenant": "acme"},
		}

		got, ok := authz.ActorFromContext(authz.ContextWithActor(t.Context(), want))

		require.True(t, ok)
		assert.Equal(t, want, got)
	})

	t.Run("the stored actor is defensively copied", func(t *testing.T) {
		t.Parallel()

		orig := authz.Actor{ID: "alice", Roles: []string{"manager"}}
		ctx := authz.ContextWithActor(t.Context(), orig)

		orig.Roles[0] = "admin" // mutate the caller's slice after storing

		got, ok := authz.ActorFromContext(ctx)

		require.True(t, ok)
		assert.Equal(t, []string{"manager"}, got.Roles,
			"the context copy must not alias the caller's slice")
	})
}
```

**What makes this fail today:** `authz.ContextWithActor` and `authz.ActorFromContext` do
not exist — the package fails to compile with `undefined: authz.ContextWithActor`. The
third subtest is the non-trivial one: it fails even after a naive implementation that
stores the `Actor` without cloning, because `Actor.Roles` is a slice. `Actor.Clone()`
already exists (`authz/authz.go`, used by the actor-slice helper above line 70) — **use
it rather than writing a second copier**.

- [ ] **Step 2: Run and observe RED**

```bash
go test -count=1 -run '^TestActorFromContext$' -v ./authz/ > /tmp/red1.log 2>&1; echo "EXIT=$?"; cat /tmp/red1.log
```
Expected: build failure, `undefined: authz.ContextWithActor`.

- [ ] **Step 3: Implement**

```go
// Package-level, in authz/context.go.

// actorContextKey is the unexported key type for the actor value, so no other
// package can collide with or overwrite it.
type actorContextKey struct{}

// ContextWithActor returns a copy of ctx carrying a. The actor is cloned, so a
// later mutation of the caller's slices or maps cannot change what an
// authorization decision sees.
//
// This is the supported identity seam: a consumer's authentication middleware
// calls it, and [ActorFromContext] — the default [httpcore.WithRequestActor]
// resolver — reads it back. It needs no DI container and no framework.
func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a.Clone())
}

// ActorFromContext returns the actor stored by [ContextWithActor]. ok reports
// whether one was present: a false ok means nothing authenticated the caller,
// which the HTTP transport turns into 401 rather than a zero actor.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok {
		return Actor{}, false
	}
	return a.Clone(), true
}
```

- [ ] **Step 4: Run and observe GREEN**

```bash
go test -count=1 -run '^TestActorFromContext$' -v ./authz/ > /tmp/green1.log 2>&1; echo "EXIT=$?"; grep -c '^    --- PASS' /tmp/green1.log
```
Expected: `EXIT=0`, three subtest PASS lines.

- [ ] **Step 5: Verify `Actor.Clone` deep-copies `Attributes`**

`Clone` is pre-existing and this task now depends on it for a security property. Confirm
rather than assume — add to the round-trip subtest:

```go
		got.Attributes["tenant"] = "evil"
		again, _ := authz.ActorFromContext(authz.ContextWithActor(t.Context(), want))
		assert.Equal(t, "acme", again.Attributes["tenant"])
```

If this fails, `Clone` shallow-copies the map: **stop and report it** — it is a
pre-existing defect that this decision would otherwise silently depend on, and it needs
its own backlog entry.

- [ ] **Step 6: Commit** (into the bundle commit; see §Commit discipline)

### Task 2: The dimension capability and the two sentinels

**Files:**
- Create: `authz/dimension.go`
- Modify: `authz/authz.go` (sentinels only)
- Test: `authz/dimension_test.go` (`package authz_test`)

**Interfaces:**
- Produces: `authz.Dimension` (`DimensionRoles`, `DimensionPrivileges`,
  `DimensionAttribute`), `authz.DimensionEvaluator` interface,
  `authz.EvaluatesDimension(az Authorizer, d Dimension) bool` (the default-applying
  helper — **the gate must call this, never the interface directly**),
  `authz.ErrSpecStatesNothing`, `authz.ErrUnevaluatableSpec`

- [ ] **Step 1: Write the failing test**

```go
package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/authz"
)

// nonDeclaring implements Authorizer but NOT DimensionEvaluator.
type nonDeclaring struct{}

func (nonDeclaring) Authorize(context.Context, authz.AuthzSpec, authz.Actor, map[string]any) error {
	return nil
}

func TestEvaluatesDimension(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		az     authz.Authorizer
		assert func(t *testing.T, evaluates func(authz.Dimension) bool)
	}{
		{
			name: "a non-declaring Authorizer defaults to roles only",
			az:   nonDeclaring{},
			assert: func(t *testing.T, evaluates func(authz.Dimension) bool) {
				assert.True(t, evaluates(authz.DimensionRoles))
				assert.False(t, evaluates(authz.DimensionPrivileges))
				assert.False(t, evaluates(authz.DimensionAttribute))
			},
		},
		{
			name: "AllowAll declares every dimension",
			az:   authz.AllowAll{},
			assert: func(t *testing.T, evaluates func(authz.Dimension) bool) {
				assert.True(t, evaluates(authz.DimensionRoles))
				assert.True(t, evaluates(authz.DimensionPrivileges))
				assert.True(t, evaluates(authz.DimensionAttribute))
			},
		},
		{
			name: "RoleAuthorizer declares roles and attribute but NOT privileges",
			az:   authz.RoleAuthorizer{},
			assert: func(t *testing.T, evaluates func(authz.Dimension) bool) {
				assert.True(t, evaluates(authz.DimensionRoles))
				assert.False(t, evaluates(authz.DimensionPrivileges))
				assert.True(t, evaluates(authz.DimensionAttribute))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, func(d authz.Dimension) bool {
				return authz.EvaluatesDimension(tc.az, d)
			})
		})
	}
}

func TestSpecSentinelsWrapErrNotAuthorized(t *testing.T) {
	t.Parallel()

	for _, err := range []error{authz.ErrSpecStatesNothing, authz.ErrUnevaluatableSpec} {
		assert.ErrorIs(t, err, authz.ErrNotAuthorized,
			"every caller and the 403 classification test for errors.Is(err, ErrNotAuthorized)")
	}
}
```

**What makes this fail today:** none of `Dimension`, `DimensionEvaluator`,
`EvaluatesDimension`, `ErrSpecStatesNothing` or `ErrUnevaluatableSpec` exist — build
failure. `TestEvaluatesDimension`'s **third** case is the load-bearing one: it is the
executed fact from spec §2.3 that `RoleAuthorizer` documents `Privileges` as reserved and
unevaluated, which is the entire basis of the capability interface. If that case ever
flips, the gate's design premise is gone.

- [ ] **Step 2: Run and observe RED** — `go test -count=1 -run 'TestEvaluatesDimension|TestSpecSentinels' -v ./authz/`

- [ ] **Step 3: Implement `authz/dimension.go`**

```go
// Dimension names one of the three axes an [AuthzSpec] may state. The
// spec-shape gate in runtime/task denies a spec that states a dimension the
// configured [Authorizer] does not evaluate, because such a requirement would
// otherwise be silently discarded — a spec that looks configured and is not.
type Dimension int

const (
	// DimensionRoles is AuthzSpec.Roles. Every Authorizer in this module
	// evaluates it; it is the default for an Authorizer that does not
	// implement [DimensionEvaluator].
	DimensionRoles Dimension = iota
	// DimensionPrivileges is AuthzSpec.Privileges. RoleAuthorizer does NOT
	// evaluate it (see its godoc); the casbin-backed authorizers do.
	DimensionPrivileges
	// DimensionAttribute is AuthzSpec.Attribute, the expr predicate.
	DimensionAttribute
)

// DimensionEvaluator is an OPTIONAL capability an [Authorizer] may implement to
// declare which [Dimension]s it actually evaluates.
//
// It exists because the spec-shape gate sits ABOVE the Authorizer — so that a
// consumer's own implementation inherits the gate — and an authorizer-blind
// gate would have to guess. Guessing "everything" re-opens the hole the gate
// closes; guessing "nothing" would deny every spec under the casbin authorizer,
// which does evaluate privileges.
//
// An Authorizer that does not implement this is assumed to evaluate
// [DimensionRoles] only. Use [EvaluatesDimension] rather than asserting the
// interface directly, so that default is applied in exactly one place.
type DimensionEvaluator interface {
	EvaluatesDimension(d Dimension) bool
}

// EvaluatesDimension reports whether az evaluates d, applying the roles-only
// default for an Authorizer that does not implement [DimensionEvaluator].
func EvaluatesDimension(az Authorizer, d Dimension) bool {
	if de, ok := az.(DimensionEvaluator); ok {
		return de.EvaluatesDimension(d)
	}
	return d == DimensionRoles
}
```

Then in `authz/authz.go`, beside `ErrNotAuthorized`:

```go
// ErrSpecStatesNothing is returned when an [AuthzSpec] declares no dimension
// and is not explicitly open. Before ADR-0185 such a spec allowed everyone.
// It wraps [ErrNotAuthorized] so existing callers and the 403 classification
// are unchanged.
var ErrSpecStatesNothing = fmt.Errorf("%w: spec states nothing (set Open, or state a dimension)", ErrNotAuthorized)

// ErrUnevaluatableSpec is returned when an [AuthzSpec] states a dimension the
// configured [Authorizer] does not evaluate — the requirement would otherwise
// be silently discarded. It wraps [ErrNotAuthorized].
var ErrUnevaluatableSpec = fmt.Errorf("%w: spec states a dimension this authorizer does not evaluate", ErrNotAuthorized)
```

And the declarations on the two in-package authorizers:

```go
// EvaluatesDimension implements [DimensionEvaluator]: AllowAll permits every
// actor, so it vacuously "evaluates" every dimension. Declaring all three is
// what keeps service.WithAllowAllAuthorizer meaning allow-all once the
// spec-shape gate is hoisted above the Authorizer.
func (AllowAll) EvaluatesDimension(Dimension) bool { return true }

// EvaluatesDimension implements [DimensionEvaluator]. RoleAuthorizer evaluates
// Roles and Attribute; it does NOT evaluate Privileges — see the type godoc.
func (RoleAuthorizer) EvaluatesDimension(d Dimension) bool {
	return d == DimensionRoles || d == DimensionAttribute
}
```

- [ ] **Step 4: Run and observe GREEN**, confirming both test names actually ran:

```bash
go test -count=1 -run 'TestEvaluatesDimension|TestSpecSentinelsWrapErrNotAuthorized' -v ./authz/ > /tmp/g2.log 2>&1; echo "EXIT=$?"
grep -q '^--- PASS: TestEvaluatesDimension' /tmp/g2.log && grep -q '^--- PASS: TestSpecSentinelsWrapErrNotAuthorized' /tmp/g2.log && echo BOTH_RAN
```

- [ ] **Step 5: Commit**

### Task 3: `AuthzSpec.Open`, and the three falsified godocs

**Files:**
- Modify: `authz/authz.go`
- Test: `authz/authz_test.go` (check `head -1` first)

**Interfaces:**
- Produces: `authz.AuthzSpec.Open bool`

- [ ] **Step 1: Write the failing test**

```go
func TestAuthzSpecZeroValueFailsClosed(t *testing.T) {
	t.Parallel()

	// The whole point of Open being a bool and not a *bool: a consumer, a
	// consumer-implemented TaskStore, MemTaskStore, or any table test can write
	// this literal, and it must NOT mean allow-all.
	spec := authz.AuthzSpec{}

	assert.False(t, spec.Open,
		"the zero value of a PUBLIC struct must fail closed; a *bool would be nil here "+
			"and the tri-state design read nil as grandfathered-open")
}

func TestAuthzSpecOpenRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     authz.AuthzSpec
		assert func(t *testing.T, out authz.AuthzSpec, raw string)
	}{
		{
			name: "explicit open survives",
			in:   authz.AuthzSpec{Open: true},
			assert: func(t *testing.T, out authz.AuthzSpec, raw string) {
				assert.True(t, out.Open)
			},
		},
		{
			name: "a pre-upgrade row (no Open key) decodes CLOSED",
			in:   authz.AuthzSpec{Roles: []string{"manager"}},
			assert: func(t *testing.T, out authz.AuthzSpec, raw string) {
				assert.False(t, out.Open,
					"absence decodes to false; the migration — not the decoder — is what "+
						"grandfathers pre-upgrade rows")
			},
		},
	}
	// ... marshal tc.in, unmarshal into out, call tc.assert(t, out, string(b))
}
```

**What makes this fail today:** `authz.AuthzSpec` has no `Open` field, so both tests fail
to compile (`unknown field Open`).

- [ ] **Step 2: Run and observe RED**

- [ ] **Step 3: Implement** — add the field and **correct the three godocs the change
  falsifies** in the same edit (they are wrong the moment the field lands):

```go
// AuthzSpec describes who may act: any-of roles, any-of resource privileges,
// and an optional attribute predicate (expr over {actor, vars}).
//
// A spec that states no dimension and is not Open DENIES (ADR-0185). Before
// ADR-0185 such a spec allowed everyone; the zero value now fails closed, which
// is why Open is a bool and not a *bool — see the ADR's Decision 3.
type AuthzSpec struct {
	Roles      []string // actor authorized if it has any of these roles
	Privileges []string // resource-privilege tokens evaluated by a casbin-backed Authorizer (e.g. "finance-task claim")
	Attribute  string   // expr predicate over {"actor": Actor, "vars": map} (optional)
	// Open states that any AUTHENTICATED actor may act. It is the explicit
	// form of what an empty spec used to mean implicitly. Authored via
	// activity.WithOpenEligibility(); wire key "open".
	Open bool
}
```

⚠ **Then grep for the OLD wording and fix every CONSUMING site, not only the definition.**
ADR-0187's round 1 fixed each corrected value where it was *defined* and left every place
it was *consumed*:

```bash
grep -rn "empty spec\|allow-all\|open access\|including none" --include='*.go' . | grep -v '/.git/'
```
At minimum: `authz/authz.go:80-81` (*"An empty spec means allow-all"*), `:111`
(*"spec.Roles is empty (open access)"*), and `internal/authz/casbin/authorizer.go:33`.

- [ ] **Step 4: Run and observe GREEN**, then `go build ./...` to enumerate the repo-wide
  compile breakage this field introduces. **Record the list** — it is the work of phases
  1–4 and the audit will ask whether it matched the plan.

- [ ] **Step 5: Commit**

---

## Phase 1 — fan out: four independent packages, four agents

Dispatch **one fresh general-purpose subagent per task**, all four concurrently — they
touch disjoint packages. Review each returned diff before dispatching phase 2.

### Task 4: `service` — an authorizer is required

**Files:** Modify `service/options.go`, `service/service.go`, `service/durable.go`;
test `service/service_test.go`, `service/options_test.go`.

**Interfaces:**
- Consumes: `authz.AllowAll` (Task 2's `EvaluatesDimension`).
- Produces: `service.WithAuthorizer(az authz.Authorizer) Option`,
  `service.WithAllowAllAuthorizer() Option`, `service.AuthorizerProvider` interface.

- [ ] **Step 1: Write the failing tests**

```go
func TestNewProcessEngineRequiresAnAuthorizer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		opts   []service.Option
		assert func(t *testing.T, eng *service.ProcessEngine, err error)
	}{
		{
			name: "human tasks with no authorizer is an error",
			opts: []service.Option{service.WithHumanTasks(newMemTaskStore(t), nil)},
			assert: func(t *testing.T, eng *service.ProcessEngine, err error) {
				require.Error(t, err)
				assert.Nil(t, eng)
				assert.ErrorIs(t, err, service.ErrAuthorizerRequired)
			},
		},
		{
			name: "an explicit allow-all is accepted",
			opts: []service.Option{
				service.WithHumanTasks(newMemTaskStore(t), nil),
				service.WithAllowAllAuthorizer(),
			},
			assert: func(t *testing.T, eng *service.ProcessEngine, err error) {
				require.NoError(t, err)
				assert.NotNil(t, eng)
			},
		},
		{
			name: "a real authorizer is accepted",
			opts: []service.Option{
				service.WithHumanTasks(newMemTaskStore(t), nil),
				service.WithAuthorizer(authz.RoleAuthorizer{}),
			},
			assert: func(t *testing.T, eng *service.ProcessEngine, err error) {
				require.NoError(t, err)
				assert.NotNil(t, eng)
			},
		},
		{
			name: "no human tasks configured needs no authorizer",
			opts: nil,
			assert: func(t *testing.T, eng *service.ProcessEngine, err error) {
				require.NoError(t, err)
				assert.NotNil(t, eng)
			},
		},
	}
	// ... eng, err := service.NewProcessEngine(tc.opts...); tc.assert(t, eng, err)
}
```

**What makes each case fail today:** case 1 — `service/service.go:200` defaults
`c.authz = authz.AllowAll{}`, so construction **succeeds**; the assertion on
`ErrAuthorizerRequired` fails (and the sentinel does not exist, so it will not compile
first). Cases 2–3 — `WithAllowAllAuthorizer` and `WithAuthorizer` do not exist. Case 4 is
the **regression guard** for the narrowing: it passes today and must keep passing.

- [ ] **Step 2: Write the WARN-record test**

```go
func TestAllowAllIsLoggedAtWarnAsItsOwnRecord(t *testing.T) {
	// Capture slog output with a slog.NewJSONHandler over a bytes.Buffer,
	// installed via slog.SetDefault and restored with t.Cleanup.
	//
	// Assert BOTH:
	//   1. exactly one record at LevelWarn mentioning allow-all;
	//   2. the construction SUMMARY record is still at LevelDebug and still
	//      carries its five attributes (store, definitions, taskStore, authz,
	//      hint).
	//
	// (2) is the point: promoting service.go:323's single LogAttrs call would
	// drag four unrelated attributes to WARN. The ADR forbids that, and only
	// this assertion can catch an implementer who takes the shortcut.
}
```

**What makes it fail today:** there is no WARN record at all — allow-all is disclosed only
inside the DEBUG summary at `service/service.go:323`.

- [ ] **Step 3: Run both, observe RED. Step 4: Implement. Step 5: GREEN.**

Implementation notes, all from the ADR:
- `ErrAuthorizerRequired` sentinel, `workflow-service: ` prefix.
- `AuthorizerProvider` is a **separate optional interface**, type-asserted at wiring time —
  do **not** add a seventh method to `DurableProvider` (six today; it would break every
  third-party implementer).
- ⚠ **Only `taskStore` is rescoped to apply-as-default.** `WithDurableStore` never writes
  `c.authz` today — verify that before touching it — so the ordering trap is exactly
  `WithHumanTasks(myStore, az)` before `WithDurableStore(p)` silently replacing `myStore`.
  The last-writer-wins precedence for the other five provider leaves
  (`service/options.go:157-160`) **stays**.

- [ ] **Step 6: Mutation-verify the constructor guard** *(in a worktree)*: delete the
  `ErrAuthorizerRequired` return and confirm case 1 goes RED. Restore from a `cp` backup.

- [ ] **Step 7: Commit**

### Task 5: `definition` — authoring an open eligibility

**Files:** Modify `definition/activity/activity.go`, `definition/activity/options.go`,
`definition/model/node_wire.go`, `definition/model/validate.go`; tests alongside.

**Interfaces:**
- Produces: `activity.WithOpenEligibility() UserTaskOption`; the field it sets,
  **`activity.UserTask.EligibleOpen bool`** (Task 8a reads it, so the name is binding);
  **`model.NodeWire.EligibleOpen bool `json:"eligible_open,omitempty"`** — the key is
  `eligible_open`, matching its three siblings `eligible_roles` / `eligible_privileges` /
  `eligible_expr`, ⚠ **not** a bare `open`: ADR-0144 fixed snake_case keys and ADR-0167
  made definition decoding strict, so an off-convention key is both a readability break
  and a decode hazard. Also produces `model.ErrEligibilityNotStated`.

- [ ] **Step 1: Write the failing tests** — three behaviours:

```go
// 1. The authoring option sets Open on the minted spec.
// 2. The wire key round-trips: NodeWire gains `Open bool \`json:"open,omitempty"\``
//    and a definition authored with WithOpenEligibility marshals to `"open":true`.
// 3. model.Validate REJECTS a UserTask stating neither open nor any dimension.
func TestValidateRejectsUnstatedEligibility(t *testing.T) {
	cases := []struct {
		name   string
		task   model.Node
		assert func(t *testing.T, err error)
	}{
		{
			name: "no dimension and not open",
			task: activity.NewUserTask("t1"),
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, model.ErrEligibilityNotStated)
			},
		},
		{
			name: "explicitly open",
			task: activity.NewUserTask("t1", activity.WithOpenEligibility()),
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			name: "roles stated",
			task: activity.NewUserTask("t1", activity.WithEligibleRoles("manager")),
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def := singleNodeDefinition(t, tc.task) // start -> task -> end
			tc.assert(t, model.Validate(def))
		})
	}
}
```

**What makes case 1 fail today:** `model.Validate` accepts a dimension-less `UserTask` —
ADR-0117 Decision 3 blessed *"any combination (including none)"*. It returns nil, so the
`require.Error` fails.

⚠ **The wire round-trip has TWO mapping sites, not one.** `definition/activity/activity.go:240`
(NodeWire → UserTask) and `:251` (UserTask → NodeWire) both list the three `Eligible*`
fields explicitly. `EligibleOpen` must be added to **both**, or an open task silently
loses its openness on the first save/reload — and a test that only round-trips in memory
would not see it. Re-derive the site list with
`grep -n "EligibleRoles" definition/activity/activity.go` and report what you find; do not
trust this count.

⚠ **Blast-radius warning for the implementer.** The failed bundle claimed *"only 5
`NewUserTask` call sites reach `model.Validate`"* and the re-audit **falsified it**: that
grep covered **one of three authoring forms**, missing `definition/build.Builder.AddUserTask`
(`build.go:117`, public API) and YAML `kind: userTask` (`activity.go:236`). **Re-derive the
affected set across all three forms before changing `Validate`**, and report the number you
get — do not inherit `5` or `≥13`. Expect to fix fixtures in `engine/`, `runtime/`,
`processtest/` and `service/`.

- [ ] **Steps 2–5:** RED → implement → GREEN → commit.

### Task 6: the casbin authorizers declare their dimensions

**Files:** Modify `internal/authz/casbin/authorizer.go`, `casbinauthz/casbinauthz.go`;
tests alongside. **Two packages, one agent, strictly serial** (the public one delegates to
the internal one).

- [ ] **Step 1: Write the failing test** — for **both** types:

```go
func TestCasbinAuthorizerDeclaresEveryDimension(t *testing.T) {
	// internal: a := casbin.New(...); assert all three true.
	// public:   b := casbinauthz.NewCasbinAuthorizer(...); assert all three true,
	//           and assert it FORWARDS rather than hard-codes — see step 3.
}
```

**What makes it fail today:** neither type implements `EvaluatesDimension`, so
`authz.EvaluatesDimension(a, authz.DimensionPrivileges)` returns **false** via the
roles-only default — which would empty this bundle's own privileges escape hatch. That is
re-audit finding F1 reproduced as a test.

- [ ] **Step 3: Implement.** The internal type declares all three (it evaluates roles at
  `:45`, privileges at `:56`, attributes at `:68`). The public delegate **forwards**:

```go
// EvaluatesDimension implements [authz.DimensionEvaluator] by forwarding to the
// inner evaluator, so the two can never drift.
func (a *Authorizer) EvaluatesDimension(d authz.Dimension) bool {
	return authz.EvaluatesDimension(a.inner, d)
}
```

⚠ **Forward; do not re-declare.** A hard-coded `return true` here would silently diverge
the day the inner authorizer stops evaluating a dimension. Prove the forwarding with a
mutation: change the **inner** declaration to return false for `DimensionPrivileges` and
confirm the **public** test goes RED. Restore from a `cp` backup.

- [ ] **Steps 2, 4, 5:** RED → GREEN → commit.

### Task 7: `processtest.SpyAuthorizer` declares its dimensions

**Files:** Modify `processtest/spyauthz.go`; test alongside.

- [ ] **Step 1: Failing test** — `authz.EvaluatesDimension(spy, d)` is true for all three.

**What makes it fail today:** `SpyAuthorizer` does not implement the interface, so the
roles-only default applies and every harness test using a privileges or attribute spec
would start failing the gate.

- [ ] **Step 3: Implement** — declare all three; a spy returns the configured decision for
  whatever it is asked, so it evaluates whatever it is given.

⚠ **While here, note but do NOT change:** `SpyAuthorizer.Authorize` **allows when
`decide == nil`** (`processtest/spyauthz.go:44-52`). That is a public allow-by-default
authorizer and sits oddly with Decision 2's posture. It is **backlog 142's second half** —
report it, do not fix it in this bundle.

- [ ] **Steps 2, 4, 5:** RED → GREEN → commit.

---

## Phase 2 — the mint site and the gate (two agents, disjoint packages)

`engine` and `runtime/task` are different packages, so Task 8a and Task 8 may run
concurrently. Adding a struct field breaks neither's compile.

### Task 8a: `engine` — the mint site carries `Open`

**Files:** Modify `engine/step_nodes.go` (the `userTaskStrategy.enter` spec literal, at
`:722-726` today — ⚠ **cite the symbol, not the line**, and re-derive it); test in
`engine` (⚠ `head -1` first: this package mixes `package engine` and `package engine_test`).

**Interfaces:**
- Consumes: `activity.UserTask.EligibleOpen` (Task 5), `authz.AuthzSpec.Open` (Task 3).

⚠⚠ **This task is why the plan's first self-review existed.** `userTaskStrategy.enter`
builds the spec from **exactly three** fields:

```go
spec := authz.AuthzSpec{
	Roles:      ut.EligibleRoles,
	Privileges: ut.EligiblePrivileges,
	Attribute:  ut.EligibleExpr,
}
```

Adding `Open` to `AuthzSpec` and to the authoring API but **not here** means every minted
task carries `Open == false` no matter what the author declared — so
`WithOpenEligibility()` would compile, round-trip through the wire, pass `model.Validate`,
and then **deny every actor at runtime**. The authoring gate would not catch it; only a
task-level test does.

- [ ] **Step 1: Write the failing test**

```go
// Drive a definition whose user task is authored WithOpenEligibility through
// node entry, then assert on the MINTED task, not on the definition:
//
//   require.Len(t, state.Tasks, 1)
//   assert.True(t, state.Tasks[0].Eligibility.Open,
//       "an open-authored node must mint an open spec; otherwise the task is "+
//           "unclaimable and only a task-level assertion can see it")
//
// Add the negative case in the same table: a roles-authored node mints
// Open == false, so the test discriminates rather than asserting a constant.
```

**What makes it fail today:** the spec literal has no `Open` field, so after Task 3 the
minted spec's `Open` is the zero value `false` and the first assertion fails. ⚠ The
negative case is what stops this being a test that cannot fail — without it, `assert.True`
on a field nobody sets would be caught, but `assert.False` alone would pass vacuously.

- [ ] **Step 2: RED. Step 3:** add `Open: ut.EligibleOpen` to the literal. **Step 4: GREEN.**
- [ ] **Step 5: Check for a SECOND mint path.** `grep -n "authz.AuthzSpec{" engine/` and
      report every hit. `AwaitHuman{…, Eligibility: spec}` at `:811` reuses this `spec`
      variable — confirm that, do not assume it. An enumeration rotted **eleven** times in
      one prior session.
- [ ] **Step 6: Commit.**

### Task 8: the spec-shape gate

**Files:** Create `runtime/task/gate.go`; modify `runtime/task/service.go` (four call
sites: `:199` Claim, `:234` Reassign, `:255` Complete, `:306` RefreshCandidates); test
`runtime/task/gate_test.go`.

**Interfaces:**
- Consumes: `authz.EvaluatesDimension`, `authz.ErrSpecStatesNothing`,
  `authz.ErrUnevaluatableSpec`, `authz.AuthzSpec.Open`.
- Produces: `checkSpecStated(az authz.Authorizer, spec authz.AuthzSpec) error` (unexported).

- [ ] **Step 1: Write the failing test** — this is the decision's control:

```go
func TestSpecShapeGate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		az     authz.Authorizer
		spec   authz.AuthzSpec
		assert func(t *testing.T, err error)
	}{
		{
			name: "states nothing, not open => denied",
			az:   authz.RoleAuthorizer{},
			spec: authz.AuthzSpec{},
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrSpecStatesNothing)
				assert.ErrorIs(t, err, authz.ErrNotAuthorized) // 403 classification preserved
			},
		},
		{
			name: "explicitly open => allowed through the gate",
			az:   authz.RoleAuthorizer{},
			spec: authz.AuthzSpec{Open: true},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		{
			name: "MIXED roles+privileges under RoleAuthorizer => denied, NOT silently passed",
			az:   authz.RoleAuthorizer{},
			spec: authz.AuthzSpec{Roles: []string{"manager"}, Privileges: []string{"finance-task approve"}},
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrUnevaluatableSpec)
			},
		},
		{
			name: "the same mixed spec under a privileges-evaluating authorizer => allowed",
			az:   allDimensions{}, // a local double declaring all three
			spec: authz.AuthzSpec{Roles: []string{"manager"}, Privileges: []string{"finance-task approve"}},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		{
			name: "AllowAll is not broken by the gate",
			az:   authz.AllowAll{},
			spec: authz.AuthzSpec{},
			assert: func(t *testing.T, err error) {
				assert.NoError(t, err,
					"WithAllowAllAuthorizer must keep meaning allow-all: AllowAll declares "+
						"every dimension, and an empty spec under it is the documented posture")
			},
		},
	}
}
```

⚠ **Cases 3 and 4 together are the whole decision.** Case 3 is Context finding 3's most
dangerous shape — a spec that *looks configured*, passes the role check, and silently
discards the privilege requirement. Case 4 is re-audit **F1**: the same spec must still
work under casbin, or the gate has emptied the escape hatch the ADR points consumers at.
A gate that passes 3 and fails 4 is the exact defect that killed the previous revision.

⚠ **Case 5 is re-audit's D2×D3 interaction.** If it fails, `WithAllowAllAuthorizer()` has
stopped meaning allow-all.

**What makes each fail today:** `checkSpecStated` does not exist (build failure). After a
naive implementation, case 4 fails for any authorizer-**blind** gate and case 5 fails for
any gate that does not consult `EvaluatesDimension`.

- [ ] **Step 2: RED. Step 3: Implement.**

```go
// checkSpecStated enforces ADR-0185 Decision 3 ABOVE the Authorizer, so a
// consumer's own implementation inherits it: a spec that states nothing, or
// states a dimension az does not evaluate, denies.
//
// It is authorizer-AWARE by design. A blind version would deny every
// Privileges-carrying spec including under casbin, emptying the escape hatch
// the ADR names, and would make service.WithAllowAllAuthorizer stop meaning
// allow-all.
func checkSpecStated(az authz.Authorizer, spec authz.AuthzSpec) error {
	stated := false
	for _, d := range []struct {
		dim   authz.Dimension
		given bool
	}{
		{authz.DimensionRoles, len(spec.Roles) > 0},
		{authz.DimensionPrivileges, len(spec.Privileges) > 0},
		{authz.DimensionAttribute, spec.Attribute != ""},
	} {
		if !d.given {
			continue
		}
		if !authz.EvaluatesDimension(az, d.dim) {
			return fmt.Errorf("workflow-runtime: taskservice: %w", authz.ErrUnevaluatableSpec)
		}
		stated = true
	}
	if spec.Open || stated {
		return nil
	}
	return fmt.Errorf("workflow-runtime: taskservice: %w", authz.ErrSpecStatesNothing)
}
```

- [ ] **Step 4: Wire all FOUR call sites** — `:199`, `:234`, `:255`, `:306` — each calling
  `checkSpecStated(s.authz, task.Eligibility)` **before** `s.authz.Authorize`.

⚠ **Count them again.** The enumeration "four `Authorize` sites" is inherited; re-derive it
with `grep -n '\.Authorize(' runtime/task/service.go` and report the number. An enumeration
rotted **eleven** times in one prior session, and this exact class of miss (three
compensation dispatch sites where the bundle named two) shipped a feature that could not
detect the deadlock it was built for.

- [ ] **Step 5: Prove the wiring, not just the function.** A unit test calling
  `checkSpecStated` directly **cannot fail when the wiring is reverted** — that lesson cost
  a prior delivery a re-do. Add one seam-level test per verb that drives
  `TaskService.Claim/Complete/Reassign/RefreshCandidates` with an unstated spec and asserts
  the denial, then mutation-verify by removing the gate call from **one** site and
  confirming **only that verb's** test goes RED. Worktree; `cp` backup.

- [ ] **Step 6: GREEN, commit.**

---

## Phase 3 — `transport/http/httpcore` (serial, one agent)

### Task 9: the actor comes from the context

**Files:** Create `transport/http/httpcore/actor.go`; modify `dto.go` (remove three
fields), `endpoints.go:119,132,150`, `seam.go` (two new options), `errors.go` (401/503);
tests in `httpcore`.

**Interfaces:**
- Consumes: `authz.ActorFromContext`.
- Produces: `httpcore.WithRequestActor[R any](fn func(context.Context) (authz.Actor, error)) CustomizeOption[R]`,
  `httpcore.WithAnonymousActorAllowed[R any]() CustomizeOption[R]`,
  `httpcore.ErrNoAuthenticatedActor`, `httpcore.ErrActorResolutionFailed`.

- [ ] **Step 1: Write the failing tests**

```go
func TestTaskEndpointsTakeTheActorFromTheContextOnly(t *testing.T) {
	cases := []struct {
		name   string
		ctx    func(t *testing.T) context.Context
		assert func(t *testing.T, status int, err error, seen authz.Actor)
	}{
		{
			name: "actor in context is used",
			ctx:  func(t *testing.T) context.Context {
				return authz.ContextWithActor(t.Context(), authz.Actor{ID: "alice", Roles: []string{"manager"}})
			},
			assert: func(t *testing.T, status int, err error, seen authz.Actor) {
				require.NoError(t, err)
				assert.Equal(t, "alice", seen.ID)
			},
		},
		{
			name: "no actor in context => 401, NOT a zero actor",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			assert: func(t *testing.T, status int, err error, seen authz.Actor) {
				require.Error(t, err)
				assert.Equal(t, http.StatusUnauthorized, status)
				assert.Equal(t, authz.Actor{}, seen, "the service must not have been called at all")
			},
		},
		{
			name: "Attributes reach the authorizer",
			ctx: func(t *testing.T) context.Context {
				return authz.ContextWithActor(t.Context(), authz.Actor{
					ID: "alice", Attributes: map[string]any{"tenant": "acme"},
				})
			},
			assert: func(t *testing.T, status int, err error, seen authz.Actor) {
				require.NoError(t, err)
				assert.Equal(t, "acme", seen.Attributes["tenant"])
			},
		},
	}
}
```

**What makes each fail today:** case 1 — the endpoint reads `in.Actor`, so a context actor
is ignored and `seen.ID` is `""`. Case 2 — today an absent actor yields the **zero actor**
and the call proceeds; there is no 401. Case 3 — `endpoints.go:119` constructs
`authz.Actor{ID: …, Roles: …}` and **drops `Attributes` entirely**, so
`seen.Attributes` is nil.

⚠ Case 3 is the one that also proves the spec's **D1 × D4** hazard is live: once it passes,
actor-attribute predicates are reachable over HTTP with backlog 103 still open. Leave the
comment in the test saying so.

- [ ] **Step 2: Write the resolver-error test**

```go
// A WithRequestActor error is 503, never a downgrade to the zero actor:
// a transient identity-provider failure must not become an open door.
```
**What makes it fail today:** `WithRequestActor` does not exist.

- [ ] **Step 3: RED. Step 4: Implement.**
  - Remove `Actor` from `ClaimInput`/`CompleteInput` and `By` from `ReassignInput`
    (`dto.go:44,50,66`). A body still carrying the keys is **ignored, not rejected** —
    ⚠ verify the decoder does not use `DisallowUnknownFields`; if it does (ADR-0167 made
    definition decoding strict), an ignored key would become a **400** and silently break
    every existing client. **Check, and report what you find.**
  - Resolve **once**, in `httpcore`; the three adapters pass their context through
    unchanged.
  - `ok == false` ⇒ `ErrNoAuthenticatedActor` ⇒ **401**, unless
    `WithAnonymousActorAllowed()`. Resolver error ⇒ `ErrActorResolutionFailed` ⇒ **503**.
  - ⚠ **Arm the 401/503 arms relative to the existing ordered arms.** ADR-0186 put the 413
    arm *before* the ordered 400 arm for exactly this reason. Read `errors.go`'s existing
    order before inserting.

- [ ] **Step 5: Reuse ADR-0186's option convention — do NOT invent a second one.**
      ⚠ **The generic form cannot infer `R`.** ADR-0186 hit exactly this and the repo
      already carries the answer: a generic `httpcore.WithX[R any](…) CustomizeOption[R]`
      (`seam.go:158`, `:180`) **plus a non-generic per-adapter alias** in
      `stdlib/options.go`, `gin/options.go` and `fiber/options.go` (e.g.
      `stdlib.WithMaxBodyBytes(n int64) httpcore.CustomizeOption[*http.ServeMux]`).
      Both new options need the same treatment: **2 options × 3 adapters = 6 aliases**.
      ⚠ Read each adapter's existing `options.go` first — they do not all carry the same
      set today, and matching each file's local pattern beats imposing a uniform one.
      The aliases are written in Phase 4 by the agent that owns each adapter package; this
      step only fixes the generic pair and the contract they alias.

- [ ] **Step 6: GREEN. Step 7: Commit.**

---

## Phase 4 — the four adapter packages (fan out, four agents)

Each agent owns **one** package. **29 pin sites, 9 files, 5 packages** (spec §2.6) — the
per-file counts are there; re-derive rather than trusting them, and **report your number**.

### Task 10: `stdlib` (5 pins) · Task 11: `gin` (7) · Task 12: `fiber` (5) · Task 13: `parity` (1)

**Common brief for all four:**

- [ ] **Step 1:** Rewrite each pin to put the actor in the **context** instead of the body.
- [ ] **Step 2:** ⚠ **Two pins must be REWRITTEN, not recompiled** —
  `stdlib/errors_test.go:187` and `gin/gin_coverage_test.go:244`. Both assert **403**, and
  after this change they would still return 403 **from the zero actor** — passing while
  testing nothing. Rewrite them to assert the *reason*: a 401 for no actor, or a 403 for a
  present-but-unauthorized actor. **Prove the rewrite by mutation**: revert the endpoint's
  context read, confirm the rewritten test goes RED where the old one stayed green.
- [ ] **Step 3 (fiber only):** ⚠ document and test `c.SetContext`, **not** `c.Locals`.
  `c.Locals` does not propagate into the context `httpcore` receives, so a consumer
  following fiber's most idiomatic path gets a **silently unauthenticated** request. Add an
  example test showing the correct middleware.
- [ ] **Step 4 (stdlib, gin, fiber — not parity):** add this adapter's **two non-generic
      aliases** for `WithRequestActor` and `WithAnonymousActorAllowed`, following the
      `WithMaxBodyBytes` precedent already in that package's `options.go`. Without them a
      consumer cannot call the option at all — the generic form cannot infer `R`.
- [ ] **Step 5:** RED → GREEN → commit; `go test -count=1 ./transport/http/<pkg>/...`.

---

## Phase 5 — persistence (serial, one agent — Docker required, state it in the brief)

### Task 14: the `0002` migration, three dialects, three copies

**Files:** Create `internal/persistence/store/migrations/{postgres,mysql,sqlite}/0002_authz_open.sql`;
tests in `internal/persistence/store`.

- [ ] **Step 1: Write the failing test** — seed a pre-upgrade row (a task whose
  `eligibility` JSON has no dimension and no `open` key), run migrations, read it back
  through the real store, assert `Open == true`. Repeat for the instance snapshot and a
  stored definition. Use `dbtest.RunTestSQLite` (pure Go) for the fast loop and
  `dbtest.RunTestDatabase` / `RunTestMySQL` for the real dialects. **Never spin an ad-hoc
  container.**

**What makes it fail today:** no `0002` migration exists, so the row comes back with
`Open == false` and the task is unclaimable — which is precisely the stranding this
migration prevents.

- [ ] **Step 2: RED. Step 3: Implement**, per dialect: Postgres `jsonb` operators, MySQL
  `JSON_SET`/`JSON_CONTAINS_PATH`, SQLite `json1`. Backfill **only** rows whose spec states
  no dimension — a spec that states one must keep its `Open == false`.
- [ ] **Step 4:** ⚠ **`wrkflw_definitions.definition` is the copy that keeps minting bad
  rows if skipped** (ADR Decision 3). Cover it, and add a test that mints a task from a
  migrated pre-upgrade definition and asserts it is claimable.
- [ ] **Step 5: GREEN, commit.**

### Task 15: the ADR-0187 interactions this bundle activates

- [ ] **Step 1:** ⚠ This is the repo's **first `0002_*.sql`** (exactly one migration file
  per dialect today). It **activates ADR-0187's parked latent defect**: a `CREATE INDEX`
  naming a table declared in a *different* migration file derives no `keyed` fact
  silently. Run `scripts/gen-at-rest.sh`, confirm it round-trips with a clean tree, and if
  the new file trips the defect, **fix it here** — it stops being latent the moment this
  merges.
- [ ] **Step 2: Backlog 141.** Add `wrkflw_instances.snapshot` to
  `atrest.PolicyAtRestLocations` with a `Detail` explaining that `InstanceState.Tasks[]`
  carries the full `AuthzSpec` (spec §2.2 has the executed evidence). Regenerate
  `SECURITY.md`; the published count goes from 3 to 4.
- [ ] **Step 3:** The guard could not catch this omission because it only checks
  `ClassPolicy` columns and this one is `ClassFreeform`. **Strengthen the guard or state
  plainly why you did not** — a `freeform` column carrying policy is now a known class with
  two members, and silence is not an adjudication.
- [ ] **Step 4:** RED → GREEN → commit.

---

## Phase 6 — examples and documentation (one agent)

### Task 16: the three example mains

- [ ] Add `httpcore.WithAnonymousActorAllowed[*http.ServeMux]()` to
  `examples/production_wiring/main.go:264`, `examples/sqlite_wiring/main.go:278`,
  `examples/mysql_wiring/main.go:262` — none has authentication.
- [ ] ⚠ `production_wiring` is the one a reader copies. Give it the **real** shape as a
  commented alternative: an authentication middleware calling `authz.ContextWithActor`,
  with the anonymous opt-in marked clearly as demo-only.
- [ ] `go build ./examples/...` must pass.

### Task 17: the documents the code falsifies

- [ ] **ADR-0117 Decisions 1 AND 3** annotated in place — ⚠ **both**; the first draft named
  only Decision 1.
- [ ] The **two** godocs stating the open default as fact:
  `definition/activity/activity.go:159` (on `NewUserTask`, the one every consumer reads)
  and `options.go:221`. ⚠ The draft named one.
- [ ] `SECURITY.md`: the fiber `c.SetContext` idiom, the 401/503 contract, and **all four
  residuals** from spec §6 — including that `ProcessDriver.ApplyTrigger` bypasses
  authorization by design, and that a consumer-implemented durable `TaskStore` gets no
  migration.
- [ ] ⚠ **Sweep the diff's own comments** for unexecuted claims and over-reaching
  quantifiers before the gate. False claims in committed comments have reached the Delivery
  Gate repeatedly and are cheapest to kill here. **Thirteen** were found in one prior
  delivery, on a count that grew 6→8→9→13.

---

## Phase 7 — Verification and the Delivery Gate

- [ ] `docker info` (probe; standing permission for these two runs only). If down: say so,
      let the owner start it or skip, and **label any container-free subset as partial**.
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` —
      ≥ 85 % over hand-written code. ⚠ The floor is **not** the target: every hot path and
      its failure branches first. ⚠ `scripts/coverage.sh` only **reports**; its exit code
      proves nothing.
- [ ] `go test ./... > /tmp/all.log 2>&1; echo "EXIT=$?"` — no regressions. Read the log.
- [ ] `command -v golangci-lint && golangci-lint run ./...` — repo-wide, **not**
      package-scoped. If absent: offer to install or skip; never substitute `go vet`, never
      claim "lint clean" for a run that did not execute.
- [ ] `go vet ./...` — compiles the Docker-only test packages; cheap proof the `AuthzSpec`
      change has no hidden consumer.
- [ ] **Documents describe what shipped.** Re-read spec + ADR against the built code and
      correct every divergence — most importantly anything the ADR *promises* that
      implementation changed. Per rule #11, **an implementation that contradicts the ADR
      amends the ADR in the same bundle**, with the measurement that refuted it.
- [ ] `/code-review` — owner-invoked. Fix **all** findings; fold with `--amend`.
      ⚠ **A review finding is a claim and needs execution**: reproduce before fixing, and
      if one is a false positive say so **with the measurement**.
- [ ] `/security-review` — owner-invoked. Fix all findings; fold with `--amend`.
- [ ] Merge `--no-ff` to `main`; push. Delete the branch.

## Commit discipline

**One feature bundle = one commit.** The per-task "commit" steps build up that single
commit via `git commit --amend` while the branch stays local and unpushed. Do **not** stack
`fix:` commits. ⚠⚠ **`git reset --soft <base> && git commit --amend` AMENDS `<base>`
ITSELF** — it silently replaced main's tip during ADR-0187. Recover with
`git commit-tree <tree> -p <parent> -F msg`.

## Audit — the gate this plan must pass before any of it runs

Four Opus lenses — **execution / failure-modes / counting / interaction** — in **detached
worktrees at the bundle commit**, with a **step-0 bundle-presence check stated explicitly
in every brief** (worktrees are created at the base commit, so the design documents are
typically *absent*; all three of ADR-0175's audit worktrees were, and only that instruction
turned it into a recovery). Brief every lens to **append findings per finding, before the
next probe**.

Targets, in the spec's §8, author-flagged: the definitions-copy decision (§5.2, the most
likely place this is wrong), the roles-only default for non-declaring authorizers, the
removal grid, every enumeration, and the two brand-new defects (141, 142) which have never
been audited.

⚠ Brief the **counting** lens that its failure mode is **the net, the anchor and the
SCOPE — not the arithmetic**: every sum across seven prior rounds was right, and that lens
found the decisive Critical in six consecutive bundles.
⚠ Brief the **interaction** lens with the explicit changed-set: **D4 and D5 removed; D1,
D2, D3 surviving and each revised**. A removal generates its own grid.
⚠ The spec's evidence sections are an **input** to the audit, not a conclusion of it —
attack them too. Findings have landed inside a bundle's own evidence file twice.
