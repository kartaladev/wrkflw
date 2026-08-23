# ADR-0188 — the representations of a node's fields are reconciled by machine, not by memory

> **Status: design bundle, pre-audit.** Not an input to implementation until it survives its
> rule-#9 audit.
>
> ⚠ **Judge that audit on Criticals per lens, not the total.** Seven prior four-lens rounds returned
> **15.14 ± 0.83 findings per lens** across a 12× swing in artifact size (CV 5.5 %) — the total
> measures auditor effort. `docs/plans/sweep-evidence/meta-analysis-audit-finding-rate.md`.
>
> **Revised 2026-08-25.** An earlier draft of this spec proposed embedding a shared `Eligibility`
> struct into `model.NodeWire`. **That was wrong and is withdrawn** — see §2. No public type changes.

## Why this exists

`docs/plans/sweep-evidence/meta-analysis-audit-finding-rate.md` classified **193 accepted findings
across 10 audit rounds**. Architectural findings are ~10 % of that corpus and concentrate in one
lineage — ADR-0186 **4.6 %**, ADR-0187 **6.3 %**, **ADR-0185 authz-identity 22.6 % → 35.3 %** — and
**every architectural finding but one traces to a single concept: a node's fields are declared in
several representations that nothing reconciles.**

The concrete damage: ADR-0185-core's D3 needed to add **one boolean**. Its audit found the change
needs edits in several places, and the bundle — written after reading the files, then reviewed by
four adversarial lenses — **missed `model.nodeYAML` entirely**. Under ADR-0167's strict YAML
decoding that would have shipped a user task that is **unauthorable in YAML**, one of two authoring
forms. Separately, the count of durable copies of this concept rotted **1 → 2 → 3** across three
rounds, *each correction itself wrong*.

**This delivery makes that class of omission fail a test.** It changes no authorization semantics
and no public type.

## §1 Executed premises

Run at `93d120a5`.

### 1.1 The defect is NOT specific to eligibility — the same shape guards `ActivityFields` and `WaitFields`

`definition/model/node.go:87` declares `ActivityFields` (embedding `WaitFields`), which
`activity.UserTask` embeds. `model.NodeWire` does **not** embed it: it declares the same fields
**flat**, and `node_wire.go:101` `PutActivity()` / `:109` `Activity()` bridge the two by hand.
`model.nodeYAML` declares them flat a third time (12 matching lines).

⇒ **Adding one field to `ActivityFields` today requires editing `node.go`, `node_wire.go`,
`yaml.go`, `PutActivity` and `Activity()` — five sites, unchecked.** Eligibility is not a special
case; it is where the shape happened to bite.

### 1.2 The existing guards are hand-written, one per field

`definition/model/node_wire_test.go:11` `TestNodeWire_CompletionActionRoundTrip` round-trips
**exactly one field** (`CompletionAction`) through `Activity()`/`PutActivity()`. That is the whole
protection. ⇒ **a new field needs someone to remember to write a new test — the same forgetting
problem, one level up.**

### 1.3 Field-set inventory, derived rather than asserted

| type | exported fields |
|---|---|
| `model.NodeWire` | **44** |
| `model.nodeYAML` | **39** |

The five in `NodeWire` and not in `nodeYAML`, derived by set difference: **`BoundaryAction`,
`BoundaryErrorExpr`, `DeadlineTrigger`, `TimerTrigger`, `WaitTrigger`**. Nothing is in `nodeYAML`
and not in `NodeWire`.

⇒ **the two sets are legitimately unequal, so an exact-equality guard would be wrong.** The correct
shape is a **declared exception list that fails when stale** (§3.2).

**Derived by TWO independent methods, which agree exactly.** (1) `awk` over the struct range +
`comm` set difference. (2) A throwaway reflection probe (`package model`, internal — `nodeYAML` is
unexported), run and deleted: `NodeWire exported=44  nodeYAML exported=39`,
`in NodeWire only (5): [BoundaryAction BoundaryErrorExpr DeadlineTrigger TimerTrigger WaitTrigger]`,
`in nodeYAML only (0): []`. The probe also reported **no anonymous/embedded fields** in either
struct, and neither has a multi-name declaration (`A, B string`) — the two hazards that would have
invalidated method (1).

⚠ **This was done because the method used to avoid bucket D is itself a candidate for bucket D.** A
single regex-derived enumeration would not have deserved belief.

⚠ **`definition/model` mixes `package model` and `package model_test`** (`node_wire_test.go` is the
former, `yaml_test.go` the latter) — the same hazard `engine/` has. Run `head -1` before writing
into any existing test file there. The `nodeYAML` guard **must** be an internal test.

### 1.4 The conversion sites

| # | site | direction |
|---|---|---|
| 1 | `definition/model/yaml.go:112-114` | `nodeYAML` → `NodeWire` |
| 2 | `definition/activity/activity.go:240` (`FromWire`) | `NodeWire` → `UserTask` |
| 3 | `definition/activity/activity.go:251` (`ToWire`) | `UserTask` → `NodeWire` |
| 4 | `engine/step_nodes.go:724` | `UserTask` → `authz.AuthzSpec` |

Every one is a struct literal or a multi-assign that **stays valid when a field is added to one side
and forgotten on the other**. The compiler cannot help.

### 1.5 The repo already has the right guard conventions — both are reused here

- `engine/step_nodes_test.go:32` `intentionallyUnhandledKinds` — a declared list of deliberate
  omissions, asserted against reality.
- `runtime/monitor/internal_leak_test.go:100` `knownOpenInternalLeaks` — a **self-cleaning**
  `map[string]string` of offender → reason, where **a stale entry FAILS the test**.

§3.2 mirrors the second. This is the repo's own pattern, not a new invention.

## §2 Why the obvious fix — one shared struct embedded in the wire — is WRONG

An earlier draft proposed a `model.Eligibility` struct embedded into `NodeWire`, `nodeYAML` and
`UserTask`. Embedding is technically sound: executed, JSON and YAML output are **byte-identical**,
strict decoding works and still rejects unknown keys. **It is nevertheless the wrong design**, on
two independent grounds.

**1. `NodeWire` is a persisted contract, and coupling it to a domain struct destroys a seam the
codebase actively uses.** Its shape *is* the storage format of `wrkflw_definitions.definition`, and
ADR-0167 made it strictly decoded. If a shared domain struct were embedded, a field added to that
struct for domain reasons would **silently change the persisted format** of every stored definition.

The proof that the seam is load-bearing is in the code. `node_wire.go:119`:

```go
DeadlineTimer: ReadTrigger(w.DeadlineTrigger, w.DeadlineDuration, false),
```

with the comment *"The canonical nested `TriggerWire` is preferred; the legacy flat string fields
are decoded as expression triggers for backward compatibility."* That is a backward-compatible wire
migration — legacy flat fields read into a modern nested domain shape — and it is **only expressible
because the wire type and the domain type are decoupled.** Embedding would have removed the
mechanism that made it possible.

**2. It would leave two competing conventions for one problem.** `ActivityFields` and `WaitFields`
already use flat-wire + accessors. Applying a different pattern to eligibility alone is a
consistency defect that outlives whoever introduced it; applying it to all three would delete the
seam repo-wide.

⇒ **When decoupling is deliberate, the correspondence between representations is exactly what a
machine-checked test is for.** It cannot be expressed in the type system *because* the decoupling is
intentional — and `EligibleExpr` ↔ `Attribute` is not even a name match.

## §3 The design — three guards, no production change

### 3.1 Value round-trip through the user-task conversion pair

Fill a `NodeWire` with a **distinct non-zero value in every field `KindUserTask` owns**, run
`FromWire` then `ToWire`, and assert the result equals the input on those fields.

⚠ **"Owns" must NOT be a hand-derived list** — that would be bucket D again, in the guard built to
prevent bucket D. It is a **second self-cleaning classification list**, the same shape as §3.2:

```go
// notOwnedByUserTask are NodeWire fields KindUserTask's FromWire/ToWire pair
// deliberately does not carry, each with the reason. A STALE entry fails.
var notOwnedByUserTask = map[string]string{ /* field: reason */ }
```

The guard asserts every one of `NodeWire`'s 44 fields is **either** round-tripped **or** listed with
a reason, and that every listed field still exists and still is not round-tripped. Adding a field to
`NodeWire` then forces a decision at this guard too, rather than defaulting to unchecked.

⇒ the design has **two self-cleaning classification lists** (§3.1 ownership, §3.2 YAML
authorability) and **one declared correspondence** (§3.3). All three fail when stale.

**What makes it fail:** a field added to `NodeWire` and to `UserTask` but dropped from either
`activity.go:240` or `:251` — the exact miss that would have shipped in ADR-0185-core D3.

⚠ **Value-based, not presence-based.** A guard asserting "every non-zero field survives" cannot
catch a field the *writer* drops, because the dropped field is zero in the result. The assertion
must compare against the filled input.

### 3.2 `nodeYAML` ↔ `NodeWire` field-set correspondence, with a self-cleaning exception list

```go
// yamlUnauthorableWireFields are NodeWire fields deliberately not authorable in
// YAML, each with the reason. Mirrors runtime/monitor's knownOpenInternalLeaks:
// a STALE entry fails this test, so the list cannot rot.
var yamlUnauthorableWireFields = map[string]string{
	"DeadlineTrigger": "canonical nested TriggerWire; YAML authors the legacy flat form (node_wire.go:119)",
	"TimerTrigger":    "…",
	"WaitTrigger":     "…",
	"BoundaryAction":  "…",
	"BoundaryErrorExpr": "…",
}
```

The guard asserts every `NodeWire` field is either present on `nodeYAML` **or** listed here, and
that every listed field still exists and is still absent from `nodeYAML`.

**What makes it fail:** adding a field to `NodeWire` without either adding it to `nodeYAML` or
declaring why YAML cannot author it. **This is the guard that would have caught ADR-0185-core's
`nodeYAML` miss** — it forces a decision instead of permitting silence.

⚠ The five reasons above are **placeholders pending derivation**. Each must be established from the
code and stated, not guessed. `DeadlineTrigger`'s is evidenced (§2); the other four are not.
Marked `ASSUMPTION (unverified)` until the implementer derives them.

### 3.3 `UserTask` eligibility ↔ `authz.AuthzSpec`, exhaustive both ways

```go
// package activity_test — external, so definition/activity keeps its layering.
var eligibilityCorrespondence = map[string]string{
	"EligibleRoles":      "Roles",
	"EligiblePrivileges": "Privileges",
	"EligibleExpr":       "Attribute",
}
```

Asserts: every field of `authz.AuthzSpec` appears exactly once as a value; every `Eligible`-prefixed
field of `activity.UserTask` appears exactly once as a key; and no row names a field that does not
exist on its side.

⚠ **One-way coverage is insufficient and would miss the direction D3 needs.** A guard checking only
"every `UserTask` eligibility field has a partner" passes while `AuthzSpec` grows a field nobody can
author.

### 3.4 Backlog 141

`wrkflw_instances.snapshot` carries the full `AuthzSpec` via `InstanceState.Tasks[].Eligibility`
(executed, `2026-08-23-authz-identity-core.md` §2.1) but is absent from
`atrest.PolicyAtRestLocations`. Add it; regenerate `SECURITY.md`; the published count goes **3 → 4**.

⚠ ADR-0187's guard **structurally cannot see the omission**: `render.go:404-414` fails only for a
`ClassPolicy` column and this one is `ClassFreeform` — the identical case
`wrkflw_definitions.definition` was hand-added for. **A `freeform` column carrying policy is now a
class with two members.** Either the guard covers the class or the bundle states why not.

### 3.5 ADR-0187's `NodeWire` pin is UNCHANGED

Because §2 withdraws the embedding, `NodeWire` keeps its three flat `Eligible*` fields, so
`TestDefinitionEligibilityFieldsAreTheDeclaredSet` keeps working as written. **The earlier draft's
"repoint the pin" decision is withdrawn with the embedding that caused it.**

## §4 What this delivery does NOT do

- **No production code changes** beyond §3.4's one-line list entry. No type changes, no public API
  change, no wire change, no migration, no semantic change.
- **Does not reduce the number of edit sites.** Adding a field still touches several places — it
  simply **fails a test** if one is missed. Reducing the sites would require the restructuring §2
  rejects.
- **Does not extend the value round-trip (§3.1) to all node kinds.** Scoped to `KindUserTask`, the
  kind carrying the concept that caused this. ⚠ Generalizing per kind needs a declared owned-field
  table per kind; **flagged for the audit as an explicit scope question**, not silently omitted.
- **Does not fix the write-back defect** where the instance snapshot overwrites the task row — that
  stays open against ADR-0185 D3.

## §5 Verification

The gate for a guard-only delivery is that the guards **fail when they should**:

- **Mutation-verify each of the three guards, in both directions where they have two** — in a
  `git worktree`, restoring from a `cp` backup, never `git checkout <path>`.
  - §3.1: delete one field copy from `ToWire`; then separately from `FromWire`. Both must go RED.
  - §3.2: add a field to `NodeWire` only. RED. Then add a stale entry to the exception list. RED.
  - §3.3: add a field to `AuthzSpec` only. RED. Then to `UserTask` only. RED.
- ⚠ Assert each new test **ran** (`grep -q '^--- PASS: <Name>'`) — `go test -run` on a nonexistent
  name exits 0, and anchoring the regex does not help.
- Full gate: `go test -race` + `scripts/coverage.sh` ≥ 85 %, repo-wide `go test ./...`, repo-wide
  `golangci-lint run ./...`, then `/code-review` and `/security-review`.

## §6 What the audit must attack

⚠ **Adopt `reaudit-0186`'s never-implemented prescription**: bucket D — an enumeration built with
the wrong grep net — is **25.4 % of all findings ever, non-zero in all ten rounds**, and it is what
made ADR-0185-core miss `nodeYAML`. §1.3's field sets were derived by `awk` + `comm` set difference
rather than by eye. **The counting lens should check whether that derivation is actually sound**
(does the `awk` range capture the whole struct? does the regex miss embedded or multi-name fields?)
— *the method used to avoid the class is itself a candidate for the class.*

Author-flagged targets:

1. **§1.3's field counts and the five-field difference.** Now derived by two independent methods
   that agree (regex+`comm`, and a reflection probe), with both invalidating hazards checked and
   absent. **Attack the remaining gap**: both methods count *fields*, and the guard will compare
   *field names* — a field present in both but with a **different tag value** (so a different wire
   key) would satisfy every check here and still be a real divergence. Is that reachable?
2. **§3.1's ownership list.** It is now a self-cleaning list rather than a hand-derived one, but
   **its initial population is still hand-made** and the audit should derive it independently.
   ⚠ Also attack the mechanism: how does the guard *decide* a field "is round-tripped"? If it infers
   it from the field being non-zero after the round-trip, a field whose test value collides with the
   zero value would be misclassified.
3. **§2's claim that embedding would change the persisted format.** It is argued from the
   `ReadTrigger` legacy path. Is that path actually reachable, and is the argument load-bearing or
   decorative?
4. **§3.2's five reasons**, four of which are explicitly unverified assumptions.
5. **Whether guards-only is sufficient**, or whether declining to reduce the edit-site count means
   ADR-0185 D3 remains as error-prone as before — just noisier when it fails.
