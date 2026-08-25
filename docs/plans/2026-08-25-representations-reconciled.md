# ADR-0188 Implementation Plan — reconcile the parallel representations by machine

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development`.
> Steps use checkbox (`- [ ]`) syntax.
>
> ⛔ **THIS PLAN FAILED ITS RULE-#9 AUDIT (2026-08-25). Do not execute any task from it.**
> Known wrong: **Task 3 does not compile** (`model.specFor` is unexported; the internal-package
> workaround is an import cycle); its ownership list derives **23–27 of 44** entries with **at least
> six provably wrong**, two of which would un-guard the legacy wire path the ADR calls precious;
> **Task 4 Step 3 asserts a green that cannot happen** (`render_test.go:227` hardcodes "three
> places"); and **Task 2's guard passes on a change that is a net regression** (completing only the
> half it checks turns a loud YAML failure into a silent dropped value).
> Read `docs/plans/sweep-evidence/audit-0188-adjudication.md` first.

**Goal:** Make "a field was added to one representation and forgotten in another" **fail a test**
instead of shipping — the omission class behind the ADR-0185 lineage's architectural findings.

**Architecture:** Three guards and no restructuring. Two **self-cleaning classification lists**
(which `NodeWire` fields `KindUserTask` round-trips; which are not authorable in YAML) and one
**declared two-way correspondence** (`UserTask` eligibility ↔ `authz.AuthzSpec`). All three fail
when stale. Plus backlog 141's one-line list entry.

**Tech Stack:** Go 1.25, `reflect`, `stretchr/testify`.

**Spec:** `docs/specs/2026-08-24-eligibility-representation.md` — read it *with* this plan; it holds
the executed premises and, in §2, the argument for why the obvious restructuring is rejected.

**ADR:** `docs/adr/0188-representations-reconciled-by-machine.md`.

## ▶ Progress

**Branch: `design/authz-identity-core`** (carries the parked ADR-0185 bundle as an earlier commit;
this is a separate bundle/commit on top). ⚠ The bundle commit is `--amend`ed — **name the branch,
never quote the SHA**.

| stage | state |
|---|---|
| Spec (revised: embedding withdrawn) | ✅ committed |
| ADR-0188 | ✅ written |
| This plan | ✅ written |
| Rule-#9 audit | ⬜ not yet dispatched |
| Implementation | ⬜ blocked on the audit |

## Global Constraints

- **TDD is a deliverable.** ⚠ **This delivery is unusual: the tests ARE the product.** The RED state
  for a guard is therefore *not* "the symbol does not exist" — it is the **mutation** that the guard
  must catch. Every task's red step is a deliberate breakage, observed RED, then restored.
- **Restore a mutation from a `cp` backup, never `git checkout <path>`** (it restores from the index
  and destroys uncommitted work). **Run ablations in a `git worktree`, never the shared tree.**
- **Judge runs by exit code**, never a pipeline tail: `go test … > /tmp/x.log 2>&1; echo "EXIT=$?"`.
  Always `-count=1`.
- ⚠ **`go test -run` on a nonexistent name exits 0, and anchoring does not help.** Confirm a test
  ran: `grep -q '^--- PASS: <Name>'`.
- ⚠ **`definition/model` mixes `package model` and `package model_test`** (`node_wire_test.go` is
  internal, `yaml_test.go` external) — **`head -1` before writing into any existing test file.**
- **Search the repo for an existing convention before writing a new symbol.** The two list guards
  here deliberately mirror `runtime/monitor/internal_leak_test.go:100` `knownOpenInternalLeaks` and
  `engine/step_nodes_test.go:32` `intentionallyUnhandledKinds`. **Read both before writing either.**
- **Fan out by Go package.** Tasks 1 and 3 are both in `definition/activity` → **strictly serial**.
- **No Docker needed.** Every package here is container-free.

---

## File Structure

| file | responsibility | task |
|---|---|---|
| `definition/activity/eligibility_correspondence_test.go` *(create, `package activity_test`)* | §3.3 two-way `UserTask`↔`AuthzSpec` correspondence | 1 |
| `definition/model/yaml_field_coverage_test.go` *(create, `package model`)* | §3.2 `nodeYAML` vs `NodeWire`, self-cleaning exception list | 2 |
| `definition/activity/wire_roundtrip_test.go` *(create, `package activity_test`)* | §3.1 value round-trip + self-cleaning ownership list | 3 |
| `internal/atrest/classification.go` *(modify)*, `SECURITY.md` *(regenerate)* | §3.4 backlog 141 | 4 |

---

## Phase 1 — two independent packages, two agents in parallel

### Task 1: the eligibility correspondence guard

**Files:** Create `definition/activity/eligibility_correspondence_test.go` (`package activity_test`).

**Interfaces:** Produces `TestEligibilityCorrespondsToAuthzSpec`. Consumes nothing from other tasks.

- [ ] **Step 1: Write the guard**

```go
package activity_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
)

// eligibilityCorrespondence pairs each UserTask eligibility field with its
// authz.AuthzSpec counterpart. The mapping is DECLARED because it is not a name
// match: EligibleExpr carries what AuthzSpec calls Attribute.
//
// Adding, removing or renaming a field on EITHER side without updating this map
// fails TestEligibilityCorrespondsToAuthzSpec.
var eligibilityCorrespondence = map[string]string{
	"EligibleRoles":      "Roles",
	"EligiblePrivileges": "Privileges",
	"EligibleExpr":       "Attribute",
}

// TestEligibilityCorrespondsToAuthzSpec is exhaustive in BOTH directions.
//
// What makes it fail:
//   - a field added to authz.AuthzSpec with no map row  (the direction ADR-0185
//     D3 needs, and the one a coverage-only guard would miss);
//   - an Eligible*-prefixed field added to activity.UserTask with no map row;
//   - a row naming a field that does not exist on either side.
func TestEligibilityCorrespondsToAuthzSpec(t *testing.T) {
	t.Parallel()

	userTask := reflect.TypeOf(activity.UserTask{})
	spec := reflect.TypeOf(authz.AuthzSpec{})

	// Direction 1: every declared key exists on UserTask, and every
	// Eligible*-prefixed UserTask field is declared.
	declaredKeys := map[string]bool{}
	for k := range eligibilityCorrespondence {
		_, ok := userTask.FieldByName(k)
		assert.True(t, ok, "correspondence names UserTask.%s, which does not exist", k)
		declaredKeys[k] = true
	}
	for i := range userTask.NumField() {
		f := userTask.Field(i)
		if !f.IsExported() || !strings.HasPrefix(f.Name, "Eligible") {
			continue
		}
		assert.True(t, declaredKeys[f.Name],
			"UserTask.%s is an eligibility field with no correspondence row — add it, "+
				"and add the matching authz.AuthzSpec field", f.Name)
	}

	// Direction 2: every AuthzSpec field is a declared value, exactly once.
	declaredValues := map[string]int{}
	for _, v := range eligibilityCorrespondence {
		declaredValues[v]++
	}
	for i := range spec.NumField() {
		f := spec.Field(i)
		if !f.IsExported() {
			continue
		}
		assert.Equal(t, 1, declaredValues[f.Name],
			"authz.AuthzSpec.%s must appear exactly once in correspondence — a new spec "+
				"field with no authoring counterpart cannot be set by any definition", f.Name)
	}
	for _, v := range eligibilityCorrespondence {
		_, ok := spec.FieldByName(v)
		assert.True(t, ok, "correspondence names authz.AuthzSpec.%s, which does not exist", v)
	}
}
```

- [ ] **Step 2: Confirm it passes today** — `go test -count=1 -run '^TestEligibilityCorrespondsToAuthzSpec$' -v ./definition/activity/`, and `grep -q '^--- PASS: TestEligibilityCorrespondsToAuthzSpec'`.

- [ ] **Step 3: RED via mutation — direction 2** *(worktree; `cp` backup)*. Add
      `Foo string` to `authz.AuthzSpec`. Expected: FAIL naming `authz.AuthzSpec.Foo`. Restore, diff
      to confirm clean.

- [ ] **Step 4: RED via mutation — direction 1.** Add `EligibleFoo []string` to
      `activity.UserTask`. Expected: FAIL naming `UserTask.EligibleFoo`. Restore, diff.

⚠ **Both mutations are required.** Direction 2 is the one a coverage-only guard would miss, and it
is the direction ADR-0185 D3 actually needs. A task that observes only one RED has not verified this
guard.

- [ ] **Step 5: Commit.**

### Task 4: backlog 141

**Files:** Modify `internal/atrest/classification.go`; regenerate `SECURITY.md` via
`scripts/gen-at-rest.sh`.

- [ ] **Step 1:** Add the `wrkflw_instances.snapshot` entry to `PolicyAtRestLocations`, with a
      `Detail` stating that `InstanceState.Tasks[].Eligibility` puts the full `AuthzSpec` inside
      that `freeform` column. Mirror the wording style of the existing
      `wrkflw_definitions.definition` entry, which exists for the identical reason.
- [ ] **Step 2:** `scripts/gen-at-rest.sh`; confirm it round-trips with a clean tree and that
      `SECURITY.md`'s published count goes **3 → 4**.
- [ ] **Step 3:** Run `go test -count=1 ./internal/atrest/...` — the drift and completeness guards
      must be green.
- [ ] **Step 4: Adjudicate the class question, in writing.** A `ClassFreeform` column carrying
      policy now has **two** members. Either extend the completeness guard to cover that class, or
      **state in the ADR why not**. ⚠ Silence is not an adjudication — ADR-0186 shipped two
      documented-but-unmitigated hazards and `/code-review` refused the distinction.
- [ ] **Step 5: Commit.**

---

## Phase 2 — `definition/model` (one agent)

### Task 2: the YAML coverage guard

**Files:** Create `definition/model/yaml_field_coverage_test.go` — **`package model`** (internal:
`nodeYAML` is unexported). ⚠ `head -1` an existing file to confirm the convention before writing.

- [ ] **Step 1: Write the guard**

```go
package model

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// yamlUnauthorableWireFields are NodeWire fields deliberately absent from
// nodeYAML, each with the reason it cannot be authored in YAML.
//
// It mirrors runtime/monitor's knownOpenInternalLeaks: the list is SELF-CLEANING,
// so a stale entry — a field that has since been added to nodeYAML, or removed
// from NodeWire — fails this test rather than rotting silently.
//
// ⚠ Each reason must be DERIVED from the code, not guessed.
var yamlUnauthorableWireFields = map[string]string{
	"DeadlineTrigger": "canonical nested TriggerWire; YAML authors the legacy flat " +
		"DeadlineDuration form, which NodeWire.Wait() decodes (node_wire.go:119)",
	"TimerTrigger": "canonical nested TriggerWire; YAML authors the legacy flat " +
		"TimerDuration form. ⚠ REDUCED EXPRESSIVENESS, not parity — backlog 144",
	"WaitTrigger": "canonical nested TriggerWire; YAML authors the legacy flat " +
		"WaitEvery form. ⚠ REDUCED EXPRESSIVENESS, not parity — backlog 144",
	"BoundaryAction": "⚠ NOT a deliberate limit — backlog 143. YAML CAN author a " +
		"boundary (nodeYAML.AttachedTo, yaml.go:63/:141) but cannot set this, so " +
		"event.WithBoundaryAction is unreachable from YAML",
	"BoundaryErrorExpr": "⚠ NOT a deliberate limit — backlog 143, same as " +
		"BoundaryAction: event.WithBoundaryErrorExpr is unreachable from YAML",
}

// TestNodeWireFieldsAreYAMLAuthorableOrDeclared is the guard that would have
// caught ADR-0185-core's nodeYAML miss: it converts silence into a decision.
//
// What makes it fail:
//   - a field added to NodeWire but not to nodeYAML and not declared here;
//   - a declared field that no longer exists on NodeWire;
//   - a declared field that HAS since been added to nodeYAML (stale entry).
func TestNodeWireFieldsAreYAMLAuthorableOrDeclared(t *testing.T) {
	t.Parallel()

	wire := reflect.TypeOf(NodeWire{})
	yamlType := reflect.TypeOf(nodeYAML{})

	onYAML := func(name string) bool {
		_, ok := yamlType.FieldByName(name)
		return ok
	}

	for i := range wire.NumField() {
		f := wire.Field(i)
		if !f.IsExported() {
			continue
		}
		if onYAML(f.Name) {
			assert.NotContains(t, yamlUnauthorableWireFields, f.Name,
				"NodeWire.%s IS authorable in YAML now — remove the stale exception", f.Name)
			continue
		}
		assert.Contains(t, yamlUnauthorableWireFields, f.Name,
			"NodeWire.%s has no nodeYAML counterpart: add it to nodeYAML AND its mapping "+
				"in yaml.go, or declare here why YAML cannot author it", f.Name)
	}

	for name := range yamlUnauthorableWireFields {
		_, ok := wire.FieldByName(name)
		assert.True(t, ok, "exception names NodeWire.%s, which no longer exists", name)
	}
}
```

- [ ] **Step 2: Re-derive the five reasons — do NOT trust the ones above.** They were derived at
      design time and are recorded so the guard can be written, but ⚠ **two of them assert a
      DEFECT** (backlog 143) and one asserts reduced expressiveness (144). Confirm both against the
      code before shipping the wording. Executed at design time: `grep -in "trigger" yaml.go` and
      `grep -in "boundary" yaml.go` each return **nothing**, while `nodeYAML.AttachedTo` exists at
      `yaml.go:63` and is mapped at `:141`.

      ⚠⚠ **The guard earned its keep before it was written.** Deriving this list found that
      `event.WithBoundaryAction` and `event.WithBoundaryErrorExpr` — public options with wire
      support (`event/event.go:388-389`, `:398`) and a dedicated example
      (`examples/scenarios/boundary_action/`) — are **unreachable from YAML**, even though YAML can
      declare the boundary node itself. That is a **capability gap, filed as backlog 143**, and it
      is deliberately **out of scope here**: this delivery declares and guards it, it does not fix
      it. Fixing it means adding two fields to `nodeYAML` plus their mapping — which is exactly the
      change this delivery's guard exists to make safe.
- [ ] **Step 3: Confirm green**, and confirm the test ran by name.
- [ ] **Step 4: RED via mutation ×3** *(worktree; `cp` backup)*:
      (a) add `Zzz string` to `NodeWire` only → FAIL naming `NodeWire.Zzz`;
      (b) add `BoundaryAction` to `nodeYAML` → FAIL as a **stale exception**;
      (c) rename a declared field on `NodeWire` → FAIL as **no longer exists**.
      Restore after each and `diff` to confirm.
- [ ] **Step 5: Commit.**

---

## Phase 3 — `definition/activity` again (serial after Task 1)

### Task 3: the value round-trip guard

**Files:** Create `definition/activity/wire_roundtrip_test.go` (`package activity_test`).

⚠ **Serial with Task 1** — same package; concurrent agents break each other's compile.

- [ ] **Step 1: Write the reflective filler.** Fill every exported `NodeWire` field with a
      **distinct non-zero** value: `string` → the field name; `bool` → `true`; `[]string` →
      `{"<field>-1"}`; `int`/`int64` → a distinct positive number; pointer → allocate and fill the
      target one level. ⚠ **`Kind` must be set to `KindUserTask`**, not a generated value, or
      `FromWire` will not dispatch. ⚠ **`*ProcessDefinition` is recursive** — fill it to depth 1 and
      stop, or the filler will not terminate.

- [ ] **Step 2: Write the ownership list and the guard**

```go
// notOwnedByUserTask are NodeWire fields KindUserTask's FromWire/ToWire pair
// deliberately does not carry, each with the reason. SELF-CLEANING: a field that
// starts round-tripping, or stops existing, fails this test.
// Populated by EXECUTION, not by reading: see Step 3 — run the guard with this
// map empty and let the failures enumerate the not-carried fields, then justify
// each against activity.go's FromWire/ToWire. Do not pre-fill it from this plan.
var notOwnedByUserTask = map[string]string{}

// TestUserTaskWireRoundTripCarriesEveryOwnedField.
//
// What makes it fail: a field added to NodeWire and to UserTask but dropped from
// EITHER activity.go's FromWire or its ToWire — the exact miss that would have
// shipped in ADR-0185-core D3.
//
// ⚠ The comparison is VALUE-based against the filled input, not presence-based:
// a field the WRITER drops is zero in the result, so "every non-zero field
// survived" would pass while the field was silently lost.
```

The guard: fill `w`; `n := spec.FromWire(base, w)`; `var got NodeWire; spec.ToWire(n, &got)`; then
for every exported `NodeWire` field, either assert `got.F == w.F` **or** require an entry in
`notOwnedByUserTask`; and assert every listed field is genuinely not carried and still exists.

- [ ] **Step 3: Populate `notOwnedByUserTask` by DERIVATION** — run the guard with an empty list and
      let the failures enumerate the not-carried fields, then justify each one against
      `activity.go`'s `FromWire`/`ToWire`. ⚠ **A field that appears here and should not is a
      finding** — report it rather than papering over it with a plausible reason.
- [ ] **Step 4: Confirm green**; confirm the test ran by name.
- [ ] **Step 5: RED via mutation ×2** *(worktree; `cp` backup)*: delete `w.EligibleExpr` from
      `ToWire` (`activity.go:251`) → FAIL; restore; delete `EligibleExpr: w.EligibleExpr` from
      `FromWire` (`:240`) → FAIL; restore; `diff` clean.
      ⚠ **Both directions required** — a guard that only catches the reader is half a guard.
- [ ] **Step 6: Commit.**

---

## Phase 4 — Verification and the Delivery Gate

- [ ] `docker info` (standing permission for the two Verification runs; probe first, and if it is
      down say so and label any container-free subset as partial).
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` ≥ 85 %.
      ⚠ `scripts/coverage.sh` only **reports**; its exit code proves nothing.
- [ ] `go test ./... > /tmp/all.log 2>&1; echo "EXIT=$?"` — read the log.
- [ ] `command -v golangci-lint && golangci-lint run ./...` — **repo-wide**, not package-scoped. If
      absent: offer to install or skip; never substitute `go vet`; never claim "lint clean" for a
      run that did not execute.
- [ ] **Documents describe what shipped.** ⚠ Expect the derivations in Tasks 2 and 3 to correct the
      spec — **the four `TODO-DERIVE` reasons and the ownership list are unknowns by construction**.
      Per rule #11, fold the corrections into the ADR and spec in this same bundle, with the
      measurement.
- [ ] `/code-review`, then `/security-review` — owner-invoked. Fix all findings; fold with
      `--amend`. ⚠ **A review finding is a claim needing execution**: reproduce before fixing, and
      if one is a false positive, say so with the measurement.
- [ ] Merge `--no-ff`; push.

## Commit discipline

One feature bundle = one commit, built up by `--amend` while the branch is local.
⚠⚠ **`git reset --soft <base> && git commit --amend` AMENDS `<base>` ITSELF** — it silently replaced
main's tip during ADR-0187. Recover with `git commit-tree <tree> -p <parent> -F msg`.

## Audit brief

Four Opus lenses — **execution / failure-modes / counting / interaction** — detached worktrees **at
the bundle commit**, a **step-0 bundle-presence check in every brief**, findings appended **per
finding**.

⚠ **Report the result as Criticals per lens, not as a total.** The total is an instrument reading
(15.14 ± 0.83 per lens across a 12× scope swing).

Targets, author-flagged, in spec §6 — plus these, specific to a guard-only delivery:

1. ⚠ **The counting lens should attack the DERIVATION METHOD, not just the numbers.** §1.3's field
   sets were derived twice (regex+`comm`, and reflection) and agree — but both count *field names*,
   and a field present in both types with a **different tag value** would pass every check here and
   still be a real divergence. Is that reachable?
2. **Do the three guards actually compose into a closed net, or is there a fourth conversion path
   nobody listed?** §1.4 names four. That enumeration is exactly the kind that has rotted before.
3. **`engine/step_nodes.go:724` (`UserTask` → `AuthzSpec`) is guarded only by §3.3's *field-set*
   correspondence, not by any value round-trip.** A field present on both sides but not *copied* at
   that site would pass. Is that a gap that matters?
4. **Is a guard-only delivery sufficient**, or does declining to reduce the edit-site count leave
   ADR-0185 D3 as error-prone as before — merely noisier when it fails? The ADR claims "safe, not
   smaller"; attack that claim.
