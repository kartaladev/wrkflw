# ADR-0188 rule-#9 audit — EXECUTION lens

Worktree: `.../scratchpad/wt88-execution`, detached at `862294ef`.
Bundle presence (step 0): **all three files present** — spec `docs/specs/2026-08-24-eligibility-representation.md`,
ADR `docs/adr/0188-representations-reconciled-by-machine.md`, plan `docs/plans/2026-08-25-representations-reconciled.md`.

Method: type the plan's prescribed guards in verbatim, run them, apply every prescribed mutation,
record real output. Docker not needed (all packages container-free).

---

### F1 — Task 3's guard as prescribed CANNOT COMPILE: `package activity_test` has no access to `spec.FromWire`/`spec.ToWire`

**Severity: Critical**

**What the bundle says.** Plan `docs/plans/2026-08-25-representations-reconciled.md:303`:
"**Files:** Create `definition/activity/wire_roundtrip_test.go` (`package activity_test`)", and :336:

> The guard: fill `w`; `n := spec.FromWire(base, w)`; `var got NodeWire; spec.ToWire(n, &got)`; then
> for every exported `NodeWire` field, either assert `got.F == w.F` ...

Spec `docs/specs/2026-08-24-eligibility-representation.md:139-141` and ADR `:76` say the same
("run `FromWire` then `ToWire`").

**What I ran / read.** `definition/model/registry.go:40,58`:

```go
var nodeRegistry = map[NodeKind]NodeSpec{}   // unexported
func specFor(k NodeKind) (NodeSpec, bool)    // unexported
```

`grep -rn "NodeWire" --include='*.go' . | grep -v '^./definition/model/'` returns exactly three
consumers: `internal/atrest/classification_test.go` (reflection over the type only),
`definition/activity/activity.go` (the spec closures themselves), and nothing else. There is **no
exported accessor** returning a `NodeSpec`, and `model.toWire`/`model.fromWire` are unexported too.

⇒ From `package activity_test` the identifier `spec` in the plan's sketch does not exist and cannot
be obtained. The only exported route from a `model.Node` to a `NodeWire` is
`ProcessDefinition.MarshalJSON` / `UnmarshalJSON`, which is a *different* function (it also passes
through `toWire`'s ID/Kind/Name/Label prologue and through JSON encoding, so `omitempty` erases
zero values and `Validation` round-trips through the registry).

**Why it matters.** Task 3 is the guard the whole delivery is named for (the ADR-0185-core D3 miss).
As written it is not implementable in the prescribed package. An implementer will improvise, and the
two obvious improvisations have different semantics from the one that was designed and audited.

**Concrete proposed fix.** Choose one, and state it in the plan:
(a) put the guard in `package model` (internal) as `definition/model/wire_roundtrip_test.go`, using
    `specFor(KindUserTask)` — but `model` must not import `definition/activity` (it would cycle), so
    the test needs a blank import of `definition/kinds`, which **also cycles** (kinds → activity →
    model). ⇒ (a) is only viable as `package model_test` with `specFor` exposed via an
    `export_test.go` shim, which `model_test` can reach.
(b) keep it in `activity_test` and round-trip through the **public** path:
    `model.NewDefinition(...)` → `json.Marshal` → `json.Unmarshal` → `json.Marshal`, comparing the
    two `NodeWire` JSON objects. This changes what is guarded (JSON-level, `omitempty`-lossy) and
    the ownership list's contents; the spec's §3.1 text must be rewritten to match.
(c) add a small exported test hook in `model` (e.g. `func SpecFor(NodeKind) (NodeSpec, bool)`),
    which is a **public API change** and therefore contradicts ADR-0188's headline "no public type
    changes" / "Zero production risk" (ADR `:124`).

---

### F2 — Task 1's guard PASSES on the mutation the ADR says it prevents: `NumField()` does not see promoted fields, `FieldByName` does

**Severity: Major**

**What the bundle says.** ADR `docs/adr/0188-representations-reconciled-by-machine.md:122-123`:

> The omission class that produced this lineage's architectural findings **fails a test** instead of
> shipping. It cannot recur silently for eligibility, **`ActivityFields` or `WaitFields`**.

Plan `:126-134` implements direction 1 as `for i := range userTask.NumField()` filtered by
`strings.HasPrefix(f.Name, "Eligible")`.

**What I ran.** Typed the plan's Task-1 guard in verbatim. It compiles and passes today:
`go test -count=1 -run '^TestEligibilityCorrespondsToAuthzSpec$' -v ./definition/activity/` →
`EXIT=0`, `--- PASS: TestEligibilityCorrespondsToAuthzSpec (0.00s)` (confirmed the test RAN).

Both prescribed mutations DO go red — recorded here so the fix is not overcorrected:
- `Foo string` added to `authz.AuthzSpec` (`authz/authz.go:86`) → `EXIT=1`,
  *"authz.AuthzSpec.Foo must appear exactly once in correspondence"*.
- `EligibleFoo []string` added to `activity.UserTask` → `EXIT=1`,
  *"UserTask.EligibleFoo is an eligibility field with no correspondence row"*.

**The third mutation, not prescribed, is the one that matters.** I added
`EligibleGroups []string` to `model.ActivityFields` (`definition/model/node.go`) — which
`UserTask` **embeds**, so `UserTask{}.EligibleGroups` is a real, settable, `Eligible`-prefixed
eligibility field on the type:

```
go test -count=1 -run '^TestEligibilityCorrespondsToAuthzSpec$' -v ./definition/activity/
EXIT=0
--- PASS: TestEligibilityCorrespondsToAuthzSpec (0.00s)
```

**GREEN.** A reflection probe explains why:

```
NumField = 11
  [0] Base            anon=true
  [1] ActivityFields  anon=true
  [2] EligibleRoles  [3] EligiblePrivileges  [4] EligibleExpr  ... [10] OutcomeVariable
FieldByName(EligibleGroups) found = true
FieldByName(CompletionAction) found = true
```

`reflect.Type.NumField()` returns only *declared* fields (the two embeds count as one field each);
`FieldByName` traverses embeds. The guard uses `NumField()` for the "did you declare it?" direction
and `FieldByName` for the "does the row's field exist?" direction — **asymmetric on exactly the
embedding the spec §1.1 identifies as the shared-field mechanism.**

**Why it matters.** `ActivityFields`/`WaitFields` are the two types §1.1 and ADR `:45-47` say have
"the identical shape" and are named in the Positive consequence as covered. They are not covered:
a shared field added to the embed is invisible to this guard. It is also the plausible landing
place if eligibility is ever generalised beyond `UserTask` — which is precisely the shape §1.1 says
is coming.

**Concrete proposed fix.** Walk the type's *promoted* field set, not its declared one. Cheapest
correct form:

```go
var visit func(reflect.Type)
visit = func(tp reflect.Type) {
    for i := range tp.NumField() {
        f := tp.Field(i)
        if f.Anonymous && f.Type.Kind() == reflect.Struct {
            visit(f.Type)
            continue
        }
        ...existing body...
    }
}
visit(userTask)
```

and correct ADR `:122-123` — either implement the walk, or delete `ActivityFields`/`WaitFields`
from the sentence, because as written it is a false claim about what shipped.

---

### F3 — ADR Decision 4 over-quantifies what Task 1 asserts on the `UserTask` side

**Severity: Minor**

**What the bundle says.** ADR `:99-101`:

> A declared `map[string]string` pairing `UserTask`'s `Eligible*` fields with `authz.AuthzSpec`'s,
> asserting **every field of each side** is covered exactly once …

**What is actually built.** The `AuthzSpec` side is every exported field (plan `:141-149`). The
`UserTask` side is only fields matching `strings.HasPrefix(f.Name, "Eligible")` (plan `:128`) —
`Manual`, `Outcomes`, `CompletionValidation` etc. are deliberately and correctly excluded.
Executed: the guard is green today with 9 of `UserTask`'s 11 declared fields uncovered.

**Why it matters.** The spec (`:203-205`) states the prefix restriction correctly; the ADR's
restatement drops it. Per Premise Discipline this is exactly the summary-sentence over-generalisation
class. It also matters concretely for ADR-0185-core D3: the field it adds is `authz.AuthzSpec.Open`
(spec `docs/specs/2026-08-23-authz-identity-core.md:272` — "`authz.AuthzSpec` gains **`Open bool`**",
wire key `eligible_open`). Direction 2 catches it, but if the `UserTask`-side field is named
without the `Eligible` prefix, direction 1 never would.

**Concrete proposed fix.** ADR `:99-101` → "…asserting every exported field of `authz.AuthzSpec`
and every `Eligible`-prefixed field of `activity.UserTask` is covered exactly once".

---

### F4 — Task 2's guard goes GREEN on a change that silently DROPS a YAML-authored value, and its own failure message invites exactly that change

**Severity: Critical**

**What the bundle says.** Plan `:262-264` — the guard's failure text:

> "NodeWire.%s has no nodeYAML counterpart: add it to nodeYAML **AND its mapping in yaml.go**, or
> declare here why YAML cannot author it"

Spec `:184-186`: *"**This is the guard that would have caught ADR-0185-core's `nodeYAML` miss** — it
forces a decision instead of permitting silence."*

**What I ran.** I typed Task 2's guard in verbatim (`definition/model/yaml_field_coverage_test.go`,
`package model`) and confirmed it green and RUN:
`--- PASS: TestNodeWireFieldsAreYAMLAuthorableOrDeclared (0.00s)`, `EXIT=0`.

Then I performed the *fix* the guard demands, but only its first half — added
`BoundaryAction string \`yaml:"boundary_action,omitempty"\`` to `nodeYAML`
(`definition/model/yaml.go:67`) and removed its now-stale row from
`yamlUnauthorableWireFields` — **without** adding `BoundaryAction: ny.BoundaryAction` to the
`fromNodeYAML` literal at `yaml.go:106-146`:

```
go test -count=1 -run '^TestNodeWireFieldsAreYAMLAuthorableOrDeclared$' -v ./definition/model/
EXIT=0
--- PASS: TestNodeWireFieldsAreYAMLAuthorableOrDeclared (0.00s)
```

**GREEN.** I then parsed a real YAML definition through the public `model.ParseYAML` + `Build`
(`package model_test`, blank-importing `definition/kinds`) with a boundary node carrying
`boundary_action: notify-overdue`:

```
boundary node: event.BoundaryEvent{Base:model.Base{id:"bnd", ...}, AttachedTo:"review",
  Timer:schedule.TriggerSpec{... expr:"dueExpr" ...}, Action:"", ErrorExpr:"", ...}
```

`Action:""`. The authored value **parsed clean and was silently discarded** — no error, no warning,
and the guard stayed green.

**Why it matters — this is a net REGRESSION, not just a gap.** On the pristine tree the same YAML
fails LOUD (see F5): `field boundary_action not found in type model.nodeYAML`. Completing only the
half of the fix that the guard checks converts a loud rejection into a silent drop. So the guard,
whose stated purpose is to convert silence into a decision, actively rewards the change that
manufactures silence — and it does so on `BoundaryAction`, the very field the plan (`:281-288`)
tells the implementer to go fix under backlog 143. The guard checks *field-set presence*; the
omission class it is named for (`ADR-0185-core` D3) is a *missing assignment in a struct literal*,
which is the half it does not check.

**Concrete proposed fix.** Add a second assertion that the field is actually **carried**, not merely
declared. Cheapest form, still `package model` and still reflection-only — round-trip a
`nodeYAML` through `fromNodeYAML`'s literal:

```go
// fill every exported nodeYAML field with a distinct non-zero value, then
// w := wireFromYAML(ny)  // extract yaml.go:106-146 into a named helper
// and assert, for every field name present on BOTH types, that w.<F> == ny.<F>
// (skipping the type-mismatched pair Kind/Subprocess, declared in a second list).
```

This requires extracting the `NodeWire{...}` literal in `fromNodeYAML` into a small pure function
so a test can call it without building a whole definition — a *test-visibility* refactor, not a
public API change, so ADR `:124`'s "zero production risk" survives. Without it, spec `:184-186` and
ADR `:94` ("This is the guard that would have caught ADR-0185-core's miss") are **only half true**:
it catches the missing struct *field*, never the missing struct *assignment*.

---

### F5 — Backlog 143 CONFIRMED by execution, with the detail the bundle omits: today it fails LOUD

**Severity: Minor (confirmation), but it is load-bearing for F4**

**What the bundle says.** Plan `:227-231` and `:281-288`: `event.WithBoundaryAction` /
`WithBoundaryErrorExpr` are "unreachable from YAML", filed as backlog 143.

**What I ran.** On the pristine worktree, `model.ParseYAML` over a definition whose boundary node
carries `boundary_action: notify-overdue`:

```
PARSE ERROR: workflow-definition: parse YAML: yaml: unmarshal errors:
  line 13: field boundary_action not found in type model.nodeYAML
```

and the same node with `timer_duration: "dueExpr"` DOES reach the domain type
(`Timer:schedule.TriggerSpec{... expr:"dueExpr" ...}`), so the boundary node itself is fully
authorable — only the two fields are not. **The claim is true.**

**Why it matters.** The bundle records the gap but not its *current failure mode*. Under ADR-0167's
`KnownFields(true)` the author gets a precise, actionable rejection today. That is the baseline any
backlog-143 fix must not regress — which is exactly what F4 shows the guard permits.

**Concrete proposed fix.** Add the observed error text to the exception-list reason and to spec
`:279`, so whoever fixes 143 knows they are replacing a loud failure and must not leave a silent
one.

---

### F6 — Plan Step 4(c) for Task 2 is not executable as written: the rename produces a COMPILE failure, not a RED

**Severity: Minor**

**What the bundle says.** Plan `:293`: "(c) rename a declared field on `NodeWire` → FAIL as **no
longer exists**."

**What I ran.** Renamed `NodeWire.BoundaryErrorExpr` → `BoundaryErrorExprX` (the naive reading):

```
EXIT=1
# github.com/kartaladev/wrkflw/definition/event [github.com/kartaladev/wrkflw/definition/model.test]
definition/event/event.go:389:18: w.BoundaryErrorExpr undefined (type model.NodeWire has no field or method BoundaryErrorExpr)
definition/event/event.go:398:24: w.BoundaryErrorExpr undefined ...
FAIL	github.com/kartaladev/wrkflw/definition/model [build failed]
```

Build failure — and per this repo's own standing lesson, *a mutation that fails to compile is not a
RED*. Only after also renaming both `definition/event/event.go` references does the real RED appear,
and it is a **double** failure:

```
Messages: NodeWire.BoundaryErrorExprX has no nodeYAML counterpart: add it to nodeYAML AND its mapping in yaml.go, or declare here why YAML cannot author it
Messages: exception names NodeWire.BoundaryErrorExpr, which no longer exists
--- FAIL: TestNodeWireFieldsAreYAMLAuthorableOrDeclared (0.00s)
```

Mutations (a) and (b) DO work exactly as prescribed:
- (a) `Zzz string` on `NodeWire` only → `EXIT=1`, *"NodeWire.Zzz has no nodeYAML counterpart…"*
- (b) `BoundaryAction` added to `nodeYAML` → `EXIT=1`, *"NodeWire.BoundaryAction IS authorable in
  YAML now — remove the stale exception"*

**Why it matters.** Every one of the five exception-list fields is referenced from
`definition/event` or `definition/model` itself, so *no* rename of a declared field compiles
without a companion edit. An implementer following (c) literally will see a build failure, may
record it as the RED, and will have verified nothing.

**Concrete proposed fix.** Restate (c) as: "rename a declared field on `NodeWire` **and its two
`definition/event/event.go` references** (`:389`, `:398` for `BoundaryErrorExpr`) so the tree still
compiles → expect TWO failures: the renamed field has no `nodeYAML` counterpart, and the exception
row names a field that no longer exists."

---

### F7 — Task 3's ownership list, DERIVED BY EXECUTION as the plan instructs, contains FOUR fields that ARE carried: `ID`, `Kind`, `Name`, `Label`

**Severity: Critical**

**What the bundle says.** Plan `:336`: "fill `w`; `n := spec.FromWire(base, w)`; `var got NodeWire;
spec.ToWire(n, &got)`", and `:340-343` Step 3: "Populate `notOwnedByUserTask` by DERIVATION — run
the guard with an empty list and let the failures enumerate the not-carried fields, then justify
each one against `activity.go`'s `FromWire`/`ToWire`. ⚠ **A field that appears here and should not
is a finding**."

**What I ran.** I did exactly that. (Because of F1 I had to add a one-line `export_probe_test.go`
shim exposing `specFor`; every other line is the plan's own recipe.) Filler per the plan's Step-1
rules, `reflect.DeepEqual` comparison, `w.Kind = KindUserTask`:

```
total NOT round-tripped = 27 of 44
  ID     in=ID        out=
  Kind   in=userTask  out=unspecified
  Name   in=Name      out=
  Label  in=Label     out=
  ... (23 more)
```

**Those four are carried.** `definition/model/node_wire.go:88-97`:

```go
func toWire(n Node) NodeWire {
	w := NodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
	if lc, ok := n.(interface{ rawLabel() string }); ok { w.Label = lc.rawLabel() }
	if s, ok := specFor(n.Kind()); ok && s.ToWire != nil { s.ToWire(n, &w) }
	return w
}
```

and `:142` `fromWire` builds `Base{id: w.ID, name: w.Name, label: w.Label}`. Executed through the
real public path (`json.Marshal`/`Unmarshal` of a `ProcessDefinition` holding one `UserTask`):

```
marshalled node: {"id":"p","version":1,"nodes":[{"id":"review","kind":"userTask","label":"Review It","eligible_roles":["mgr"],...}]}
after round-trip: id="review" name="" label="Review It" kind=userTask eligibleRoles=[mgr]
```

⇒ `ID`, `Kind` and `Label` demonstrably survive. The plan's harness calls `spec.ToWire` **directly**,
which skips `toWire`'s four-field prologue, so the guard measures a function that is never called in
isolation in production.

**Why it matters.** Step 3 tells the implementer to write a justifying *reason* for every field that
appears. Four of those reasons will be false statements committed into the repo — the exact defect
class ADR-0188 exists to stop, manufactured by ADR-0188's own guard. And the guard then becomes
**blind** to a real regression in `toWire`'s prologue (drop `w.Label = lc.rawLabel()` and nothing
fails, because `Label` sits on the exception list with a reason saying it is not carried).

**Concrete proposed fix.** Drive the round-trip through the **whole** pair, not the spec closures:
compare `toWire(fromWire(w))` — i.e. expose `toWire`/`fromWire` (or `MarshalJSON`/`UnmarshalJSON`)
rather than `NodeSpec.ToWire`/`FromWire`. This also resolves F1's package problem, because the
guard then belongs in `definition/model` (as `package model_test` + `export_test.go`), not in
`definition/activity`. Update spec `:139-141`, ADR `:76` and plan `:336` to say `toWire`/`fromWire`.

---

### F8 — The plan's filler produces SEMANTICALLY INVALID `*TriggerWire` values, which misclassifies `DeadlineTrigger` and `WaitTrigger` as not-carried and permanently disarms the guard on the deadline/wait path

**Severity: Critical**

**What the bundle says.** Plan `:307-313` Step 1 fill rules: "`string` → the field name; `bool` →
`true`; `[]string` → `{"<field>-1"}`; `int`/`int64` → a distinct positive number; **pointer →
allocate and fill the target one level**."

**What I ran.** Applying those rules to `*TriggerWire` gives `&TriggerWire{Kind: "Kind", Nanos: 23,
Expr: "Expr", Cron: "Cron", …}` — `Kind` is filled with the *field name*, `"Kind"`.
`model.ReadTrigger` (`definition/model/trigger_wire.go:70-100`) switches on `w.Kind` over the nine
legal strings and **returns the zero `TriggerSpec` for anything else**; `PutTrigger` then returns
`nil` for a zero spec. Observed:

```
  TimerTrigger     in=&{Kind 23 <nil> Expr Cron 27 28 29 [{0 0 0}] [33] [35]}  out=<nil>
  DeadlineTrigger  in=&{Kind 39 ...}                                            out=<nil>
  WaitTrigger      in=&{Kind 55 ...}                                            out=<nil>
total NOT round-tripped = 27 of 44
```

Re-running with the single change `TriggerWire.Kind = "expr"` (a legal discriminator):

```
  TimerTrigger     in=&{expr 0 <nil> Expr  0 0 0 [] [] []}  out=<nil>
total NOT round-tripped = 25 of 44
```

`DeadlineTrigger` and `WaitTrigger` **dropped off the list** — they are carried, via
`NodeWire.Activity()`→`Wait()`→`ReadTrigger` and `PutActivity()`→`PutWait()`→`PutTrigger`
(`node_wire.go:117-134`). `TimerTrigger` stays, correctly: only `BoundaryEvent` reads it
(`definition/event/event.go:387`).

**Why it matters.** Following the plan literally, an implementer derives a list containing
`DeadlineTrigger` and `WaitTrigger` with a plausible-sounding but false reason, and the guard is
then blind to a deletion of `w.DeadlineTrigger = PutTrigger(a.DeadlineTimer)` from `PutWait` — the
deadline/wait-trigger carrying path, which is the exact area ADR-0176/0181/0182 kept finding bugs
in. This is the "value that coincidentally misclassifies" hazard spec `:273-275` flags, but the
mechanism is not zero-value collision — it is a **discriminator field filled with nonsense**, which
no amount of "pick a distinct non-zero value" avoids.

**Concrete proposed fix.** The filler cannot be purely structural for discriminated types. Add an
explicit **seed table** for the types whose values are interpreted rather than copied, and state in
the plan that it exists:

```go
var wireSeeds = map[reflect.Type]any{
    reflect.TypeOf(&model.TriggerWire{}): model.PutTrigger(schedule.AfterExpr("seedExpr")),
}
```

and assert in the guard that every seeded type still appears in `NodeWire` (self-cleaning, same
shape as the other two lists). Also add a **second-order guard**: any field whose round-trip result
is the *zero value* while the input was non-zero must be either carried or listed — and any listed
field must fail the round-trip **for a semantically valid input**, not merely for the filler's.

---

### F9 — The plan's prescribed comparison `got.F == w.F` does not compile for four `NodeWire` fields, and is WRONG for `*TriggerWire` even where it does

**Severity: Major**

**What the bundle says.** Plan `:336-338`: "for every exported `NodeWire` field, either assert
`got.F == w.F` **or** require an entry in `notOwnedByUserTask`". Spec `:141` and ADR `:77` say
"assert the result equals the input on those fields".

**What I ran.**

1. *Does not compile.* `NodeWire` has four `[]string` fields (`EligibleRoles`,
   `EligiblePrivileges`, `Outcomes`, `CancelActions`-bearing `Subprocess` aside). A literal
   `got.EligibleRoles == w.EligibleRoles` is `invalid operation: slice can only be compared to nil`.
   Observed a sibling instance directly while writing the probe:
   `probe_ptr_and_public_test.go:23:33: invalid operation: *got.DeadlineTrigger == *w.DeadlineTrigger
   (struct containing []schedule.ClockTime cannot be compared)`.
2. *Wrong where it does compile.* For pointer fields `==` is address comparison:

```
DeadlineTrigger  ptr-equal(==)=false  value-equal=true  (in=0x77c8a852e280 out=0x77c8a852e320)
RetryPolicy      ptr-equal(==)=true
```

`PutTrigger` allocates a fresh `&TriggerWire{…}` (`trigger_wire.go:29`), so `DeadlineTrigger`
compares unequal by address while being value-identical. `RetryPolicy` compares equal only by the
accident that `PutActivity` copies the pointer (`node_wire.go:102`) — so the guard would be
**stricter than intended for one pointer field and vacuous for the other**.

**Why it matters.** The two pointer fields land on opposite sides of a distinction the bundle never
states, and one of them (`DeadlineTrigger`) is silently pushed onto the exception list by it —
compounding F8 from a second, independent direction.

**Concrete proposed fix.** State `reflect.DeepEqual` explicitly in plan `:336`, spec `:141` and ADR
`:77`, and add: "pointer fields are compared by **value**; a guard comparing by address would list
`DeadlineTrigger` as not-carried because `PutTrigger` allocates."

---

### F10 — The plan's `*ProcessDefinition` recursion warning guards a hazard reflection cannot reach, and its prescribed mitigation ("depth 1") would hollow out the guard

**Severity: Minor**

**What the bundle says.** Plan `:311-313`: "⚠ **`*ProcessDefinition` is recursive** — fill it to
depth 1 and stop, or the filler will not terminate."

**What I ran.** `model.ProcessDefinition` (`definition/model/definition.go:38-53`) reaches a node
only through `Nodes []Node`, where `Node` is an **interface**. `reflect` cannot construct an
interface value, so the filler returns at that field and the walk terminates on its own. My probe
ran with `depth=2` and completed in 0.00 s, filling `Subprocess` to
`&{ID 101 [] [{    false}] [CancelActions-1] <nil> []}` — the `Nodes` slice is empty, as expected.

Meanwhile the prescribed mitigation is actively harmful: a global depth-1 budget is consumed by the
pointer allocation itself, so `*RetryPolicy`, `*TriggerWire` and `*validate.ValidationDescriptor`
would be allocated **zero-valued**. A zero `*RetryPolicy` in and a zero `*RetryPolicy` out compares
equal, so the field would pass the guard while asserting nothing.

**Why it matters.** Small on its own, but it is a wrong premise stated as a hazard, and its
mitigation silently weakens three fields including `Validation` — the one field with a non-trivial
conversion (`PendingValidation`/`PutValidation`, `validation_wire.go:48,68`).

**Concrete proposed fix.** Replace the warning with the true statement: "recursion terminates on its
own because `ProcessDefinition.Nodes` is `[]Node`, an interface slice reflection cannot construct;
depth must nevertheless be at least 2 so pointer targets (`*RetryPolicy`, `*TriggerWire`,
`*ValidationDescriptor`) are filled non-zero rather than allocated empty."

---

### F11 — ALL THREE guards stay GREEN through a half-implemented ADR-0185-core D3, so the ADR's "Unblocks: ADR-0185 D3" is refuted at the site that matters

**Severity: Critical**

**What the bundle says.** ADR `:17`: "**Unblocks: ADR-0185** D3 (backlog 53)". ADR `:94`: the YAML
guard "is the guard that would have caught ADR-0185-core's miss". Plan `:8-9`: "Make *a field was
added to one representation and forgotten in another* **fail a test** instead of shipping".
Plan audit brief `:395-397` self-flags the risk; this finding is its answer, by execution.

**What I ran.** I implemented ADR-0185-core D3 exactly as its spec designs it
(`docs/specs/2026-08-23-authz-identity-core.md:272` — "`authz.AuthzSpec` gains **`Open bool`**…
the wire key is `eligible_open`"):

- `authz.AuthzSpec.Open bool`
- `activity.UserTask.EligibleOpen bool`
- `model.NodeWire.EligibleOpen bool` (`json:"eligible_open,omitempty"`)
- `model.nodeYAML.EligibleOpen bool` + its mapping in `fromNodeYAML`
- both `FromWire` and `ToWire` in `definition/activity/activity.go`
- the `eligibilityCorrespondence` row `"EligibleOpen": "Open"`

…and deliberately **did not** update `engine/step_nodes.go:723`, which mints the runtime spec:

```go
spec := authz.AuthzSpec{
	Roles:      ut.EligibleRoles,
	Privileges: ut.EligiblePrivileges,
	Attribute:  ut.EligibleExpr,
}
```

Result — `go build ./...` clean, and:

```
--- PASS: TestEligibilityCorrespondsToAuthzSpec (0.00s)     ok  .../definition/activity
--- PASS: TestNodeWireFieldsAreYAMLAuthorableOrDeclared (0.00s)  ok  .../definition/model
```

Task 3 would also pass (I carried `EligibleOpen` in both `FromWire` and `ToWire`). **All three new
guards green with the open marker never reaching the authorization spec** — i.e. under D3's
"zero value denies" semantics, every task denies, and nothing fails.

Two *pre-existing* guards did fire — `TestAllDeclaredYAMLTagsParseUnderStrictDecoding`
(`definition/model/strict_decoding_test.go:519`) and ADR-0187's
`TestDefinitionEligibilityFieldsAreTheDeclaredSet` (`internal/atrest/classification_test.go:199`)
— but both complain about the *added field*, not about the *missing copy at site 4*, and both are
satisfied by mechanical edits that leave the defect in place.

**Why it matters.** `engine/step_nodes.go:723` is conversion site 4 of the four the bundle
enumerates (spec `:89`, ADR `:32`), and it is the **only** site where D3's field changes behaviour.
The bundle guards sites 1–3 by field-set and value, and site 4 by field-set only. So the delivery
does not unblock the delivery it names.

**Concrete proposed fix.** Either (a) add a fourth guard — a value round-trip
`UserTask → authz.AuthzSpec` asserting every `AuthzSpec` field is non-zero after minting from a
fully-filled `UserTask`, which requires factoring the literal at `step_nodes.go:723-727` into a
named exported-for-test function (e.g. `engine.eligibilityOf(ut)`); or (b) delete "Unblocks:
ADR-0185 D3" from ADR `:17` and say plainly in Consequences/Negative that the mint site remains
unguarded and D3 must bring its own test. Silence is not an option here — plan `:395-397` already
asked the question, so an unanswered audit finding would be a documented-but-unmitigated hazard of
the kind `/code-review` refused on ADR-0186.

---

### F12 — F4 survives the pre-existing YAML-tag guard: the whole `definition/model` package is GREEN while an authored value is silently dropped

**Severity: Critical (this is F4 at full strength — read them together)**

**What I ran.** F4's mutation, then the two follow-on edits an implementer would naturally make
when the suite complains:

1. `BoundaryAction string \`yaml:"boundary_action,omitempty"\`` added to `nodeYAML`;
2. its now-stale row removed from `yamlUnauthorableWireFields`;
3. `boundary_action: notify-overdue` added to the `allFieldsYAML` fixture
   (`definition/model/strict_decoding_test.go:399-403`), because
   `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` demands it:
   *"declared yaml tag \"boundary_action\" is not exercised by allFieldsYAML — strictness makes it
   load-bearing"*;

…while **still** omitting `BoundaryAction: ny.BoundaryAction` from `fromNodeYAML`'s literal:

```
go test -count=1 ./definition/model/
EXIT=0
ok  	github.com/kartaladev/wrkflw/definition/model	0.469s
```

and the same YAML parsed through `ParseYAML`+`Build`:

```
boundary node: event.BoundaryEvent{..., AttachedTo:"review", Timer:{expr:"dueExpr"}, Action:"", ...}
```

**Green package, dropped value.** Note that step 3 is not optional — the existing guard forces it,
so the path from "loud rejection today" to "silent drop, everything green" is three edits long and
every one of them is demanded by a test.

**Why it matters.** ADR `:94` and spec `:184-186` claim this guard "would have caught ADR-0185-core's
miss". It catches the *declaration* half of that miss and not the *assignment* half, and the
delivery's own worked example (backlog 143) walks straight into the half it misses.

**Concrete proposed fix.** As F4 — assert the `nodeYAML`→`NodeWire` *values*, not just the names.

---

### F13 — §1.2's "That is the whole protection" is FALSE: a derived, self-cleaning `nodeYAML` guard already exists

**Severity: Major**

**What the bundle says.** Spec `:46-51` §1.2 "The existing guards are hand-written, one per field":

> `definition/model/node_wire_test.go:11` `TestNodeWire_CompletionActionRoundTrip` round-trips
> **exactly one field** (`CompletionAction`) … **That is the whole protection.**

**What I ran.** `definition/model/strict_decoding_test.go:519`
`TestAllDeclaredYAMLTagsParseUnderStrictDecoding` **derives the tag list from source**:

```go
tags := declaredYAMLTags(t, "yaml.go", "nodeYAML", "definitionYAML")
tags = append(tags, declaredYAMLTags(t, "retry.go", "RetryPolicy")...)
tags = append(tags, declaredYAMLTags(t, "validate/validate.go", "ValidationDescriptor")...)
tags = append(tags, declaredYAMLTags(t, "../flow/flow.go", "SequenceFlow")...)
```

and fails on any declared-but-unexercised tag. Executed (adding `eligible_open` to `nodeYAML`):

```
declared yaml tag "eligible_open" is not exercised by allFieldsYAML — strictness makes it load-bearing
```

`internal/atrest/classification_test.go:199` `TestDefinitionEligibilityFieldsAreTheDeclaredSet` is a
second derived guard over `NodeWire`'s `Eligible*` set. Neither is "hand-written, one per field".

**Why it matters.** Three concrete consequences, not just tidiness:
1. §1.2 is the stated motivation for §3.2's existence; a false motivation invites the wrong shape.
2. Task 2 **overlaps** the existing guard on one axis and misses the axis the existing one covers
   (fixture exercise). The bundle should say how they compose — see F12, where they compose badly.
3. Per rule #11's "search the repo for an existing convention BEFORE writing a new symbol": the
   right move may be to **extend `declaredYAMLTags`' fixture-exercise check to a value check**
   rather than add a third reflection guard.

**Concrete proposed fix.** Rewrite spec §1.2 with the two derived guards named and their coverage
stated, and add a paragraph to §3.2 on how it composes with
`TestAllDeclaredYAMLTagsParseUnderStrictDecoding`.

---

### F14 — §2's rejection argument is CONFIRMED reachable but is a NON-SEQUITUR: `ReadTrigger` is untouched by the embedding it is used to reject

**Severity: Major**

**What the bundle says.** ADR `:61-66` (the "load-bearing decision"):

> **The decoupling is load-bearing, and the codebase proves it.** `node_wire.go:119` reads
> `DeadlineTimer: ReadTrigger(w.DeadlineTrigger, w.DeadlineDuration, false)` … That
> backward-compatible wire migration is only expressible **because** the wire and domain shapes are
> decoupled. **Embedding would delete the mechanism.**

Spec `:115-125` says the same.

**What I ran — part 1, the path IS live.** YAML authoring the legacy flat form:

```
YAML legacy flat -> DeadlineTimer={kind:8 ... expr:dueExpr ...}  WaitEvery={kind:9 ... expr:everyExpr ...}
re-marshalled: {... "deadline_trigger":{"kind":"expr","expr":"dueExpr"},"wait_trigger":{"kind":"everyExpr","expr":"everyExpr"} ...}
```

and stored JSON carrying `"deadline_duration":"dueExpr"` decodes identically. `kind:8` is
`schedule.KindExpr`. **The path is reachable from both authoring forms and is a live one-way
read migration** (writes always emit the nested form). The premise is TRUE.

**What I ran — part 2, the inference is FALSE.** I applied the withdrawn design to the **real**
44-field `NodeWire`: replaced the three flat `Eligible*` fields with an embedded
`model.Eligibility` struct carrying the identical `json:` tags, and fixed `fromNodeYAML`'s literal.

```
go build ./...            → clean, with ZERO changes to definition/activity/activity.go
                            or definition/event/event.go (promoted fields read AND write)
go test -count=1 ./definition/... ./internal/atrest/... ./authz/...
  ok definition, definition/activity, definition/build, definition/event, definition/flow,
     definition/gateway, definition/kinds, definition/model/validate{,/avro,/callback,/expr,/jsonschema},
     definition/schedule, authz
  FAIL definition/model     — only TestNodeWireFieldsAreYAMLAuthorableOrDeclared (THIS bundle's own new guard)
  FAIL internal/atrest      — only TestDefinitionEligibilityFieldsAreTheDeclaredSet (ADR-0187's pin)
--- PASS: TestGoldenRoundTrip
--- PASS: TestProcessDefinitionUnmarshalJSONRejectsUnknownFields
--- PASS: TestParseYAMLRejectsUnknownFields
--- PASS: TestDefinitionStorePathIsStrict
--- PASS: TestNestedSubprocessDecodingIsStrict
--- PASS: TestPersistedDefinitionRoundTripsThroughStrictJSON
marshalled node: {"id":"p","version":1,"nodes":[{"id":"review","kind":"userTask","label":"Review It","eligible_roles":["mgr"],...}]}
```

`ReadTrigger` is **still there, unchanged, still exercised** — it concerns `DeadlineTrigger` /
`DeadlineDuration` / `WaitTrigger` / `WaitEvery`, four fields the eligibility embed never touches.
Embedding a shared struct for `EligibleRoles`/`EligiblePrivileges`/`EligibleExpr` — none of which
has any legacy form or any domain-side transformation — removes no mechanism at all.

**Why it matters.** This is the ADR's self-declared load-bearing decision (`:53`), and the evidence
offered for it does not support it. The honest arguments for rejecting the embed are the *other*
two (a domain-motivated field silently entering the persisted format; two competing conventions),
plus a **new one this probe supplies**: embedding breaks ADR-0187's `NodeWire` pin **and this
bundle's own Task-2 guard**, because both walk `NumField()` and neither traverses embeds (F2).

Note this also means the withdrawn design was rejected for a stated reason that is wrong, while a
real reason (F2's blind spot) went unstated.

**Concrete proposed fix.** Rewrite ADR `:61-66` and spec `:115-125`. Keep the executed
`ReadTrigger` evidence — it correctly shows *some* wire fields need decoupling — but state the
inference honestly: "the eligibility triple carries no legacy form, so embedding it would not
remove `ReadTrigger`; the argument against embedding is that it establishes a precedent under which
a domain-motivated field silently changes the persisted format, and that the repo's reflection pins
(ADR-0187's, and this bundle's) do not traverse embeds." Also record the executed result that
embedding the real type is byte-identical and suite-green except those two guards — the bundle
currently claims feasibility from a two-field stand-in.

---

### F15 — Task 4's Step 3 ("the drift and completeness guards must be green") is unachievable as written: it omits `internal/atrest/render_test.go`, an undeclared edit site

**Severity: Major**

**What the bundle says.** Plan `:174-184`, Task 4 File list: "Modify `internal/atrest/classification.go`;
regenerate `SECURITY.md` via `scripts/gen-at-rest.sh`", then Step 3: "Run
`go test -count=1 ./internal/atrest/...` — the drift and completeness guards **must be green**."

**What I ran.** Added the `wrkflw_instances.snapshot` entry to `PolicyAtRestLocations`
(`internal/atrest/classification.go:63-81`) exactly as Step 1 describes, then:

```
go test -count=1 ./internal/atrest/...
EXIT=1
--- FAIL: TestRender (0.02s)
    --- FAIL: TestRender/real_repository_data:_structure,_hazards,_and_the_forbidden_blanket_claim (0.00s)
--- FAIL: TestSecurityMdInSync (0.01s)
```

`TestSecurityMdInSync` is expected (regenerate). `TestRender` is not: `render_test.go:227` asserts
a **hardcoded numeral**:

```go
// Authorization policy is durable at rest in THREE places, not two.
assert.Contains(t, result, "durable at rest in **three** places")
assert.NotContains(t, result, "durable at rest in **two** places")
```

The generator itself is fine — the rendered document contains "durable at rest in **four** places"
(3 occurrences in the failure diff), so the count really is derived from the slice. But the *test*
pins the word, and its comment block ("in THREE places, not two", plus the two backticked names it
enumerates) must be rewritten too.

**Why it matters.** Task 4 is a one-line change per the plan and the ADR ("The only non-test change
is Decision 5's one-line list entry", ADR `:124-125`). It is not: it is a list entry, a regenerated
`SECURITY.md`, and three assertion/comment edits in `render_test.go`. In a delivery whose entire
subject is *undeclared parallel edit sites*, shipping with an undeclared edit site in its own
smallest task is a self-inflicted instance of the class.

**Concrete proposed fix.** Add `internal/atrest/render_test.go` to plan `:65`'s File Structure row
for Task 4, and make Step 3 read: "`go test -count=1 ./internal/atrest/...` — expect
`TestRender/real_repository_data…` RED on `durable at rest in **three** places` and
`TestSecurityMdInSync` RED before regeneration; update `render_test.go:218-230` (the numeral, the
`NotContains` guard for the previous numeral, and the comment enumerating the locations), add the
new backticked `\`wrkflw_instances.snapshot\`` assertion, then regenerate."

---

### F16 — `DeadlineTrigger`'s exception reason is inconsistent with `TimerTrigger`/`WaitTrigger`'s: all three are equally reduced, so backlog 144's scope is understated

**Severity: Minor**

**What the bundle says.** Plan `:220-226`:

```go
"DeadlineTrigger": "canonical nested TriggerWire; YAML authors the legacy flat DeadlineDuration form, which NodeWire.Wait() decodes (node_wire.go:119)",
"TimerTrigger":    "... YAML authors the legacy flat TimerDuration form. ⚠ REDUCED EXPRESSIVENESS, not parity — backlog 144",
"WaitTrigger":     "... YAML authors the legacy flat WaitEvery form. ⚠ REDUCED EXPRESSIVENESS, not parity — backlog 144",
```

and spec `:189`: "`DeadlineTrigger`'s is evidenced (§2); the other four are not."

**What I ran.** `model.ReadTrigger`'s flat path (`definition/model/trigger_wire.go:61-69`) produces
**only** `AfterExpr` (or `EveryExpr` when `recurringFlat`). Executed via YAML:

```
YAML legacy flat -> DeadlineTimer={kind:8 ... expr:dueExpr ...}
```

`kind:8` is `schedule.KindExpr`. So from YAML a deadline can be an expr trigger and **nothing else**
— no `Cron`, `Daily`, `Weekly`, `Monthly`, `AfterDuration`, `At`, or `EveryRandom`, all of which
`TriggerWire` encodes and all of which `PutTrigger` writes. `DeadlineTrigger` is reduced in exactly
the same way and to exactly the same degree as `TimerTrigger` and `WaitTrigger`.

**Why it matters.** The exception list is the delivery's durable record of *why* each field is
unauthorable; two of the three trigger rows carry the "backlog 144 / reduced expressiveness" flag
and the third does not, so a reader concludes deadlines have YAML parity when they do not, and
backlog 144's scope reads as two fields when it is three.

**Concrete proposed fix.** Give `DeadlineTrigger` the same `⚠ REDUCED EXPRESSIVENESS … backlog 144`
clause, and correct spec `:189` — `DeadlineTrigger`'s reason is *partly* evidenced (the
`ReadTrigger` mechanism) and its expressiveness claim is not.

---

## Appendix — the DERIVED `notOwnedByUserTask` list (plan Task 3 Step 3), run for real

Filler per plan Step-1 rules, `reflect.DeepEqual`, `w.Kind = KindUserTask`,
`spec.FromWire`→`spec.ToWire`. **25 of 44** with semantically valid trigger wires (27 with the
plan's literal rules — see F8). Classification is mine, not the bundle's:

| field | verdict |
|---|---|
| `ID`, `Kind`, `Name`, `Label` | ❌ **MISCLASSIFIED — carried by `toWire`/`fromWire`** (F7) |
| `TimerTrigger` | ✅ genuinely not carried (only `BoundaryEvent` reads it, `event.go:387`) |
| `DeadlineTrigger`, `WaitTrigger` | ❌ **MISCLASSIFIED under the plan's filler** (F8); carried via `Activity()`/`PutActivity()` |
| `TimerDuration`, `DeadlineDuration`, `WaitEvery` | ✅ legacy flat, read-only — `PutWait` never writes them back (`node_wire.go:37` comment, confirmed) |
| `Action` | ✅ `ServiceTask`/`BusinessRuleTask` only |
| `CompensateRef`, `CompensateScopeLocal` | ✅ `CompensationThrowEvent` only |
| `SignalName`, `MessageName`, `CorrelationKey`, `MessageStartSingleton`, `ErrorCode` | ✅ event/receive/send kinds |
| `EndBehavior`, `TerminationReason`, `TerminationOutcome` | ✅ `EndEvent` only |
| `AttachedTo`, `NonInterrupting`, `BoundaryAction`, `BoundaryErrorExpr` | ✅ `BoundaryEvent` only |
| `Subprocess` | ✅ `SubProcess` only |
| `DefRef` | ✅ `CallActivity` only |

Round-tripped (and therefore guarded): `EligibleRoles`, `EligiblePrivileges`, `EligibleExpr`,
`Manual`, `ManualImmediate`, `Outcomes`, `ExposeOutcome`, `OutcomeVariable`, `DeadlineFlow`,
`DeadlineAction`, `WaitAction`, `RetryPolicy`, `RecoveryFlow`, `CompensateAction`, `CancelAction`,
`CompletionAction`, `Validation` — **17**, plus `DeadlineTrigger`/`WaitTrigger` once F8 is fixed
⇒ **19 of 44**.

⚠ **Six of the 25 rows the plan's Step 3 would have the implementer justify are wrong.** Step 3's
own warning — "A field that appears here and should not is a finding" — is therefore correct and
triggered: this is that finding, six times over.

---

## Summary — EXECUTION lens

**Criticals 5 · Majors 5 · Minors 4 (14 findings).**

| # | severity | one line |
|---|---|---|
| F1 | Critical | Task 3 as prescribed cannot compile — `activity_test` cannot reach `NodeSpec.FromWire/ToWire` (`specFor` is unexported) |
| F2 | Major | Task 1 PASSES on an `Eligible*` field added to the embedded `ActivityFields` — `NumField()` skips embeds, `FieldByName` does not |
| F3 | Minor | ADR Decision 4 says "every field of each side"; the `UserTask` side is `Eligible`-prefixed only |
| F4 | Critical | Task 2 goes GREEN on a change that silently DROPS a YAML-authored value, and its own message invites that change |
| F5 | Minor | Backlog 143 confirmed by execution; today it fails LOUD, which F4 regresses |
| F6 | Minor | Plan Step 4(c) yields a COMPILE failure, not a RED |
| F7 | Critical | The derived ownership list misclassifies `ID`/`Kind`/`Name`/`Label` as not-carried |
| F8 | Critical | The filler's invalid `*TriggerWire` misclassifies `DeadlineTrigger`/`WaitTrigger`, disarming the deadline/wait path |
| F9 | Major | `got.F == w.F` does not compile for slice fields and is address-comparison for pointers |
| F10 | Minor | The `*ProcessDefinition` recursion warning is a non-hazard; its "depth 1" mitigation hollows out three pointer fields |
| F11 | **Critical** | **All three guards stay GREEN through a half-implemented ADR-0185-core D3 — the ADR's "Unblocks: ADR-0185 D3" is refuted at site 4 (`engine/step_nodes.go:723`)** |
| F12 | Critical | F4 at full strength: whole `definition/model` package GREEN while the authored value is dropped |
| F13 | Major | §1.2's "That is the whole protection" is false — a derived self-cleaning `nodeYAML` guard already exists |
| F14 | Major | §2's `ReadTrigger` argument is reachable but a non-sequitur; embedding proven feasible on the REAL 44-field type |
| F15 | Major | Task 4 omits `internal/atrest/render_test.go`, an undeclared edit site, so Step 3 cannot be green |
| F16 | Minor | `DeadlineTrigger`'s reason omits the reduced-expressiveness flag its two siblings carry |

(F11 and F12 are both Critical and both about *closure of the net*; F4/F12 and F7/F8 are paired.)

**The single most important finding is F11.** The delivery's stated purpose is to make ADR-0185-core
D3's omission class fail a test. I implemented D3 exactly as its own spec designs it, omitting only
the mint site at `engine/step_nodes.go:723` — and every guard in this bundle passed.

**What DID work, verified:** Task 1 and Task 2 compile, pass, and go RED on the prescribed
mutations (a) and (b); `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` and ADR-0187's `NodeWire`
pin still hold; the `ReadTrigger` legacy path is live from both authoring forms; embedding is
feasible on the real 44-field `NodeWire` (byte-identical JSON, strict decoding intact, suite green
except the two `NumField()` reflection pins).

Worktree left with tracked files at `862294ef` (`git diff --stat` empty); probe files removed.
