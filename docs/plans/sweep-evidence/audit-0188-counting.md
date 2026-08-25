# ADR-0188 rule-#9 audit — COUNTING lens

Worktree `wt88-counting`, detached at `862294ef`. Bundle present (step 0 PASS):
spec `docs/specs/2026-08-24-eligibility-representation.md`, ADR
`docs/adr/0188-representations-reconciled-by-machine.md`, plan
`docs/plans/2026-08-25-representations-reconciled.md`.

## F0 — §1.3's field counts and difference set are CORRECT (no finding)

Recorded so the audit's negative results are on the record too.

Probe (`package model` internal test, run then deleted):

```
NodeWire: total=44 exported=44 unexported=0 anonymous=0 untagged=0
nodeYAML: total=39 exported=39 unexported=0 anonymous=0 untagged=0
in NodeWire only (5): [BoundaryAction BoundaryErrorExpr DeadlineTrigger TimerTrigger WaitTrigger]
in nodeYAML only (0): []
```

44/39, the five-field difference, "nothing in `nodeYAML` alone", "no embedded/anonymous
fields", "no multi-name declarations" — all four claims hold. **Also newly established
and NOT claimed by the bundle: neither struct has any UNEXPORTED field (0 and 0), and
every exported field on both carries a tag (untagged=0/0)** — so the "both methods count
exported fields" hazard raised in the audit brief is empty *today*.

## F1 — "the only guards are hand-written, one test per field" is FALSE: a second, REFLECTIVE guard over exactly these fields already exists — and it already checks the tag

**Severity: MAJOR** (false load-bearing quantifier in the ADR's Context; plus a missed reuse
that already answers the bundle's own open question §6.1).

**What the bundle claims:**

- ADR `0188-representations-reconciled-by-machine.md:34-35` — "the only guards are
  **hand-written, one test per field** (`node_wire_test.go:11` covers exactly one)."
- Spec `2026-08-24-eligibility-representation.md:46-51`, §1.2 heading "The existing guards are
  hand-written, one per field", and: "`TestNodeWire_CompletionActionRoundTrip` round-trips
  **exactly one field** … **That is the whole protection.**"
- Spec §1.5:49-101 "The repo already has the right guard conventions — **both** are reused
  here", listing exactly two (`intentionallyUnhandledKinds`, `knownOpenInternalLeaks`).

**Command and real output:**

```
$ grep -rn "func Test.*\(RoundTrip\|Parity\|FieldSet\|Fields\)" --include='*_test.go' .
internal/atrest/classification_test.go:199:func TestDefinitionEligibilityFieldsAreTheDeclaredSet(t *testing.T)
```

`internal/atrest/classification_test.go:199-221` (read in full) is a **reflective** guard over
`model.NodeWire`:

```go
wireType := reflect.TypeOf(model.NodeWire{})
for i := range wireType.NumField() {
    field := wireType.Field(i)
    if strings.HasPrefix(field.Name, "Eligible") {
        got = append(got, field.Name+" "+field.Tag.Get("json"))
    }
}
assert.Equal(t, []string{
    "EligibleExpr eligible_expr,omitempty",
    "EligiblePrivileges eligible_privileges,omitempty",
    "EligibleRoles eligible_roles,omitempty",
}, got, …)
```

**The correct value:** the pre-existing protection over `NodeWire`'s eligibility fields is
**two** guards, not one, and the second is machine-checked and reflective — not "hand-written,
one test per field". The bundle *cites the very file it lives in* (§3.4 / plan Task 4 cite
`internal/atrest/classification.go:106`) and cites this test's own name in §3.5, yet §1.2 and
the ADR Context both state that `node_wire_test.go:11` is "the whole protection".

**Two consequences beyond the wrong count:**

1. **§1.5's reusable-convention count is 2 and should be 3.** The third is the closest
   analogue to what §3.2/§3.3 build: an existing reflective `NodeWire` field-set pin.
2. ⚠ **It already answers §6.1's open question.** The bundle asks (§6 target 1, plan audit
   target 1) whether "a field present in both types under the same name but with a DIFFERENT
   TAG VALUE" is a divergence the name-based guards would miss. `TestDefinitionEligibilityFields
   AreTheDeclaredSet` pins `field.Name + " " + field.Tag.Get("json")` — **the repo already has
   the tag-inclusive convention**, and the bundle rediscovered the question without noticing it
   had precedent 90 lines below a line it cites.

**Concrete proposed fix:**

- ADR:34-35 → "the only guards are **hand-written and narrow**: `node_wire_test.go:11`
  round-trips one field (`CompletionAction`), and `internal/atrest/classification_test.go:199`
  reflectively pins `NodeWire`'s three `Eligible*` names **with their JSON tags** — but only that
  prefix, and only on `NodeWire`. Neither reconciles `NodeWire` against `nodeYAML` or against the
  `FromWire`/`ToWire` pair."
- Spec §1.2 → strike "That is the whole protection"; state the two guards and what each does
  **not** cover.
- Spec §1.5 → make it three conventions, and name the third as the precedent for including the
  tag in a field-set assertion (see F2).

## F2 — CRITICAL: the three guards do NOT close the net. Conversion site #1 (`yaml.go`'s literal) has NO guard, and the miss it permits is SILENT — strictly worse than the failure this delivery exists to prevent

**Severity: CRITICAL.** Executed, both halves.

**What the bundle claims:**

- Spec §1.4:84-92 tabulates **four** conversion sites, #1 being
  `definition/model/yaml.go:112-114` (`nodeYAML` → `NodeWire`), and says of all four: "Every one
  is a struct literal or a multi-assign that **stays valid when a field is added to one side and
  forgotten on the other**. The compiler cannot help."
- ADR:31-33 repeats it: "bridged by four hand-written conversions".
- Plan audit target 2:394 asks whether "the three guards actually compose into a closed net".
- The §3.2 guard's own failure message (plan:262-264) tells the implementer: "add it to nodeYAML
  **AND its mapping in yaml.go**".

**The gap:** §3.1's value round-trip covers sites **#2 and #3** (`activity.go:240`/`:251`).
§3.3 covers site **#4**'s field sets. **Site #1 is covered by nothing value-based** — §3.2 checks
only that the field *name* exists on both structs, which says nothing about whether
`yaml.go`'s `w := NodeWire{…}` literal copies it. The bundle names the hazard for all four sites
and then guards three.

**Commands and real output.** I implemented the plan's §3.2 guard **verbatim** (plan:202-271) as
`definition/model/zz_guard_test.go`, baselined it, then mutated.

Baseline:
```
$ go test -count=1 -run '^TestNodeWireFieldsAreYAMLAuthorableOrDeclared$' -v ./definition/model/
EXIT=0
--- PASS: TestNodeWireFieldsAreYAMLAuthorableOrDeclared (0.00s)
```

Mutation — add `EligibleGroups` to **both** `NodeWire` and `nodeYAML` (exactly what ADR-0185 D3
would do), and **forget the one line in `yaml.go`'s literal**:
```
definition/model/node_wire.go:79:  EligibleGroups []string `json:"eligible_groups,omitempty"`
definition/model/yaml.go:67:       EligibleGroups []string `yaml:"eligible_groups,omitempty"`

$ go test -count=1 -run '^TestNodeWireFieldsAreYAMLAuthorableOrDeclared$' -v ./definition/model/
EXIT=0
--- PASS: TestNodeWireFieldsAreYAMLAuthorableOrDeclared (0.00s)
```
**The guard stays GREEN.**

End-to-end consequence, executed through the real strict decoder (`yaml.KnownFields(true)`) and
`fromNodeYAML`:
```
STRICT DECODE: ACCEPTED. nodeYAML.EligibleGroups=[finance-team] nodeYAML.EligibleRoles=[manager]
resulting NodeWire.EligibleGroups=[] (len=0)  NodeWire.EligibleRoles=[manager]
```

**The correct value / why this is Critical rather than Major:**

The bundle's motivating miss — a field absent from `nodeYAML` — fails **loudly**: ADR-0167's
strict decoding rejects the unknown key. The miss this delivery *newly permits* fails **silently**:
the key is accepted, the author sees no error, and the value is discarded between `nodeYAML` and
`NodeWire`. For an **eligibility** field that means an authored restriction is dropped and the
task is **more permissive than the definition says** — a fail-open on the exact field family
this ADR exists to protect.

Worse, the §3.2 guard **actively steers an implementer into it**: it fires when the field is
missing from `nodeYAML`, its message says to do two things, and doing only the first turns it
green. A guard that converts a loud failure into a silent one is a net regression on this path.

I also confirmed the site is **currently correct** — all 39 `nodeYAML` fields are assigned in the
literal at `yaml.go:106-146`, so this is a latent hole, not a live defect.

**Concrete proposed fix (a fourth guard, cheap and in-package):** in the same internal
`package model` test file, add a value round-trip over site #1 — reflectively fill every exported
`nodeYAML` field with a distinct non-zero value, run the `nodeYAML` → `NodeWire` conversion, and
assert each field either arrives on `NodeWire` **or** is listed in a second self-cleaning
exception map (`yamlFieldsNotCarriedToWire`, for `Kind` and `Subprocess`, which are transformed
rather than copied — see F5). This is the *same shape* as §3.1 and reuses its filler. Without it,
ADR-0188's claim to close this class is false for one of the four sites it names.

Alternatively, if a fourth guard is judged out of scope, the ADR **must** say so explicitly under
Negative/costs — "site #1 remains unguarded; a field added to both structs but not to `yaml.go`'s
literal is silently dropped" — because ADR-0186 shipped documented-but-unmitigated hazards and
`/code-review` refused that distinction. Silence here is not an adjudication.

## F3 — CRITICAL: §3.3's guard reflects over `UserTask`, which HAS embedded fields — its two halves use inconsistent reflection semantics, and an eligibility field on the shared `ActivityFields` is invisible to it

**Severity: CRITICAL.** Executed. This is the counting-lens finding proper: the bundle verified
"no anonymous/embedded fields" on the **two structs where embedding is absent**, and restated the
reassurance one level up over a type where embedding is **present**.

**What the bundle claims:**

- Spec §1.3:70-73 — "The probe also reported **no anonymous/embedded fields** in either struct,
  and neither has a multi-name declaration … **the two hazards that would have invalidated method
  (1)**." (Scoped to `NodeWire`/`nodeYAML`; true there — see F0.)
- Spec §1.1:37-38 — "`definition/model/node.go:87` declares `ActivityFields` (embedding
  `WaitFields`), which **`activity.UserTask` embeds**." So the bundle *knows* `UserTask` embeds.
- §3.3 and plan Task 1 then reflect over `activity.UserTask{}` with **no** embedding check, and
  the ADR (Decision 4:99-101) asserts the guard is "exhaustive in both directions".

**Command and real output:**

```
$ go test -count=1 -run '^TestZZUserTaskReflect$' -v ./definition/activity/
UserTask.NumField()=11 (TOP LEVEL ONLY)
  [00] Base                   anon=true  exported=true  type=model.Base
  [01] ActivityFields         anon=true  exported=true  type=model.ActivityFields
  [02] EligibleRoles          anon=false exported=true  type=[]string
  ...
--- FieldByName PROMOTION check ---
  FieldByName("CompletionAction") = true      <- promoted from ActivityFields
  FieldByName("RetryPolicy"     ) = true      <- promoted
  FieldByName("DeadlineTimer"   ) = true      <- promoted from WaitFields
AuthzSpec.NumField()=3: Roles Privileges Attribute
```

**Two concrete defects follow.**

**(a) The guard's two halves disagree about what "a field of `UserTask`" means.** Direction 1's
existence check uses `userTask.FieldByName(k)`, which **traverses embedded structs**; its
completeness loop uses `userTask.NumField()`/`Field(i)`, which **does not**. So a correspondence
row may name a promoted field and pass the existence check, while the completeness loop is
structurally incapable of enumerating that same field.

**(b) Executed exploit.** I implemented plan Task 1's guard **verbatim** (plan:79-154) as
`definition/activity/zz_corr_test.go`, baselined it GREEN, then added a new eligibility dimension
to the **shared** struct — precisely the placement §1.1 says carries "the identical shape":

```
definition/model/node.go:89-90:
    // EligibleTenants is a NEW eligibility dimension added to the SHARED struct.
    EligibleTenants []string

$ go test -count=1 -run '^TestEligibilityCorrespondsToAuthzSpec$' -v ./definition/activity/
EXIT=0
--- PASS: TestEligibilityCorrespondsToAuthzSpec (0.00s)

$ UserTask.FieldByName("EligibleTenants") = true  (promoted from ActivityFields)
$ UserTask.NumField() = 11 (still top-level only)
```

`activity.UserTask` now **has** a promoted `EligibleTenants` field, with no correspondence row and
no `authz.AuthzSpec` counterpart, and the guard the ADR calls "exhaustive in both directions" is
green. Restored from `cp` backup; `git diff --stat` empty.

**The correct value:** the guard is exhaustive over `UserTask`'s **11 top-level** fields, not over
its fields. The ADR's "exhaustive in BOTH directions" is true only of `AuthzSpec` → map; the
`UserTask` → map direction is exhaustive over one struct level.

**Concrete proposed fix:** enumerate promoted fields, so both halves agree with `FieldByName`:

```go
// walk collects exported fields of t, descending into anonymous embedded
// structs so the enumeration agrees with FieldByName's promotion rules.
// UserTask embeds model.Base and model.ActivityFields (which embeds WaitFields);
// an Eligible* field added to any of them must still require a correspondence row.
func walk(t reflect.Type, visit func(reflect.StructField)) {
    for i := range t.NumField() {
        f := t.Field(i)
        if f.Anonymous && f.Type.Kind() == reflect.Struct {
            walk(f.Type, visit)
            continue
        }
        if f.IsExported() {
            visit(f)
        }
    }
}
```

and drive the completeness loop from `walk(userTask, …)`. Add a **fourth required mutation** to
plan Task 1 Step 4: *add `EligibleTenants []string` to `model.ActivityFields` (not to `UserTask`)
and observe RED* — without it the fix is unverified, and with the guard as currently written that
mutation is GREEN today.

Also correct spec §1.3:70-73 to state the embedding check's **scope**: it covers `NodeWire` and
`nodeYAML`; `activity.UserTask` — the type §3.1 and §3.3 both reflect over — **does** embed, two
levels deep.

## F4 — §1.1's "12 matching lines" is `NodeWire`'s count asserted about `nodeYAML`; the correct value is 10

**Severity: MINOR** (wrong number, no design consequence — but it is bucket D in the sentence
introducing the delivery's own motivating example, and the transposition direction matters: it
makes the two representations look more equal than they are).

**What the bundle claims:** spec §1.1:40 — "`model.nodeYAML` declares them flat a third time
(**12 matching lines**)."

**Command and real output** (`/tmp/cnt.py`, parsing the three struct declarations and applying the
trigger-encoding expansion `DeadlineTimer → {DeadlineTrigger, DeadlineDuration}`,
`WaitEvery → {WaitTrigger, WaitEvery}`):

```
domain field names (10): [CancelAction CompensateAction CompletionAction DeadlineAction
                          DeadlineFlow DeadlineTimer RecoveryFlow RetryPolicy WaitAction WaitEvery]
wire-encoded names for the shared fields (12): [... DeadlineTrigger DeadlineDuration ...
                                                    WaitTrigger WaitEvery]
present on NodeWire (12): [CancelAction CompensateAction CompletionAction DeadlineAction
   DeadlineFlow DeadlineTrigger DeadlineDuration RecoveryFlow RetryPolicy WaitAction
   WaitTrigger WaitEvery]
present on nodeYAML (10): [CancelAction CompensateAction CompletionAction DeadlineAction
   DeadlineFlow DeadlineDuration RecoveryFlow RetryPolicy WaitAction WaitEvery]
```

**The correct value: 10.** `nodeYAML` carries neither `DeadlineTrigger` nor `WaitTrigger` — two of
the five fields §1.3 itself lists as `NodeWire`-only. **12 is `NodeWire`'s count**, stated about
`nodeYAML` in the same paragraph that lists them as differing.

**Concrete proposed fix:** §1.1:40 → "`model.nodeYAML` declares them flat a third time (**10
matching lines** — it carries neither `DeadlineTrigger` nor `WaitTrigger`, two of the five
`NodeWire`-only fields in §1.3; `NodeWire` has 12)."

## F5 — "five sites, unchecked" is SIX edits across three files, and the omitted one is exactly the site no guard covers (F2)

**Severity: MAJOR.** Same omission as F2, in a second place — which is why it is worth its own row:
fixing F2's guard without fixing this sentence leaves the bundle's own instructions incomplete.

**What the bundle claims:**

- Spec §1.1:42-44 — "**Adding one field to `ActivityFields` today requires editing `node.go`,
  `node_wire.go`, `yaml.go`, `PutActivity` and `Activity()` — five sites, unchecked.**"
- ADR:45-47 repeats it verbatim: "five unchecked sites".

**Re-derivation.** Adding one field to `model.ActivityFields` requires:

| # | file | edit |
|---|---|---|
| 1 | `definition/model/node.go:87` | declare on `ActivityFields` |
| 2 | `definition/model/node_wire.go:16` | declare on `NodeWire` |
| 3 | `definition/model/node_wire.go:101` | `PutActivity` |
| 4 | `definition/model/node_wire.go:109` | `Activity()` |
| 5 | `definition/model/yaml.go:19` | declare on `nodeYAML` |
| 6 | `definition/model/yaml.go:106-146` | **the `w := NodeWire{…}` literal** |

**Six edits in three files.** The published list is internally inconsistent as well as short: it
mixes file granularity (`node.go`, `yaml.go`) with function granularity (`PutActivity`,
`Activity()`), so `node_wire.go` is counted three times over and `yaml.go` once for two required
edits.

**Omitting #6 is not cosmetic**: F2 proves that skipping it produces a *silent* drop, whereas
skipping #5 produces a loud strict-decode rejection. The bundle's own worked example therefore
teaches the reader to skip the more dangerous edit.

**Concrete proposed fix:** replace both sentences with the six-row table above, and state the
asymmetry: *"#5 omitted ⇒ loud (ADR-0167 rejects the unknown key); #6 omitted ⇒ silent (the key
decodes and the value is discarded)."* Then reconcile with §1.4's site table, which names #6 as
conversion site 1.

## F6 — §1.4's "conversion sites" enumeration omits the Go authoring surface (`definition/activity/options.go`), so no guard covers one of the project's TWO authoring forms

**Severity: MAJOR.**

**What the bundle claims:** spec §1.4:84-92 tabulates the sites bridging the four representations
and the ADR:31 calls them "four hand-written conversions". CLAUDE.md's own framing — quoted in
spirit by the spec's motivating story ("**unauthorable in YAML, one of two authoring forms**",
spec:24-25) — is that "**YAML and direct Go code are the authoring forms**".

**Command and real output** — every non-test site touching an eligibility field:

```
$ grep -rn "EligibleRoles" --include='*.go' . | grep -v _test | grep -v examples/ | grep -v runtimetest | grep -v transporttest
definition/activity/activity.go:29-30    UserTask field declaration
definition/activity/activity.go:240      FromWire            <- §1.4 site 2
definition/activity/activity.go:251      ToWire              <- §1.4 site 3
definition/activity/options.go:215       eligibleRolesOpt.applyUserTask   <- NOT IN ANY TABLE
definition/activity/options.go:223       WithEligibleRoles                <- NOT IN ANY TABLE
definition/model/node_wire.go:27         NodeWire declaration
definition/model/yaml.go:25              nodeYAML declaration
definition/model/yaml.go:112             yaml.go literal     <- §1.4 site 1
engine/step_nodes.go:724                 -> AuthzSpec        <- §1.4 site 4
authz/authz.go:83                        AuthzSpec declaration
```

`definition/activity/options.go` holds `WithEligibleRoles` (:223), `WithEligiblePrivileges`
(:234), `WithEligibleExpr` (:209) and their `applyUserTask` methods — **the entire Go authoring
surface for eligibility**, and the only way a Go-embedding consumer can set these fields.

**The correct value:** the bundle's motivating failure is "a field reachable in one authoring form
but not the other". It guards the YAML form (§3.2) and leaves the **Go** form unguarded. A new
eligibility field added to `NodeWire`, `nodeYAML`, `yaml.go`, `UserTask`, `FromWire`/`ToWire`,
`AuthzSpec` and `step_nodes.go` — passing **all three** proposed guards — is still unsettable from
Go unless someone remembers `options.go`. That is the identical omission class, one surface over,
and §3.3's guard (which reflects over `UserTask` fields, not over the option constructors) cannot
see it.

**Concrete proposed fix:** either

- (preferred) extend §3.3's correspondence to three columns — `UserTask` field → `AuthzSpec` field
  → `With…` option constructor — and assert each named option exists in `definition/activity` (a
  `map[string]func(...)` of declared constructors, or an `activity_test` reference to each by
  symbol so a rename fails the compile); or
- add a row to §4 "What this delivery does NOT do": *"the Go authoring surface
  (`definition/activity/options.go`) is not reconciled; a new eligibility field can pass all three
  guards and still have no `With…` option"* — and file it as backlog alongside 143/144.

Either way §1.4's table must gain the site, and the ADR's "four hand-written conversions" should
read "four conversions **plus the Go option surface**", so the enumeration stops implying closure
it does not have.

## F7 — MAJOR: "every architectural finding but one traces to this shape" widens the source's claim from 8 findings to 19, and from lineage-membership to root-cause

**Severity: MAJOR.** This is the ADR's motivating sentence, and it is an **inherited** claim
restated with the hedge stripped — the exact failure Premise Discipline names.

**What the bundle claims:**

- ADR:39-42 — "architectural findings concentrate in one lineage … and **every one but a single
  finding traces to this shape**."
- Spec:18-19 — "**every architectural finding but one traces to a single concept: a node's fields
  are declared in several representations that nothing reconciles.**"

**What the source actually says** (`meta-analysis-audit-finding-rate.md:503-505`):

> "Every one of the corpus's five **A (parallel representation)** findings and all three **B
> (duplicated persistence)** findings are in the ADR-0185 lineage, **except one A in R8**."

Two independent narrowings the bundle drops:

1. **Population.** The source's "all but one" is over **A + B = 5 + 3 = 8** findings. The bundle
   restates it over "**architectural** findings", which the same document defines
   (`:471-473`) as **A + B + F = 19**:
   ```
   | Architectural | A + B + F | 19 | 9.8 % | the code's own shape generates these
   ```
   The 11 bucket-**F** findings are excluded from the source's claim entirely.
2. **Relation.** The source's claim is *lineage membership* ("are in the ADR-0185 lineage"), not
   *root cause* ("traces to this shape"). The bundle converts a correlation into a causation.

And bucket F is **not** this shape. Its widened definition (`:45-48`) is "collision with any
existing repo artifact — guard, test, exported symbol, or shipped feature — that the new design
breaks, is blocked by, or is defeated by, and that the bundle never mentions", with the three named
examples being `WithActorResolver` colliding with `service.WithActorResolver`, `RedactVariables`
bypassed by `CustomizeConfig.InstanceMapper`, and `WithHTTPClient` colliding with D3's transport.
**None of those is a node-field representation problem.**

**The correct value:** of the 19 architectural findings, **8 are A+B**; of those 8, **7 are in the
ADR-0185 lineage** (the exception being one A in R8, the MySQL `trigger_` reserved-word alias).
The remaining **11 are bucket F**, a different root cause.

**Concrete proposed fix** — ADR:39-42 and spec:18-19 →

> "…and **all but one of the corpus's eight A (parallel representation) + B (duplicated
> persistence) findings sit in the ADR-0185 lineage** (`meta-analysis…:503`); the source names the
> authorization spec as the single concept behind them. The remaining 11 of the 19 architectural
> findings are bucket F (collision with an existing repo artifact), a different root cause this
> delivery does not address."

## F8 — MAJOR: "25.4 % of all findings ever" is 25.4 % of P2 (193 accepted), not of the 554 findings ever; the correct share of "all findings" is 8.8 %

**Severity: MAJOR.** The source opens with an explicit warning against exactly this substitution.

**What the bundle claims:** spec §6:257-259 — "bucket D — an enumeration built with the wrong grep
net — is **25.4 % of all findings ever, non-zero in all ten rounds**". The audit brief inherits it
as "bucket D … is 25.4% of all findings across ten audit rounds".

**What the source says** (`meta-analysis-audit-finding-rate.md:16-30`):

```
| P1 — headline totals        | the verdict line each adjudication publishes | 10 rounds |
| P2 — enumerated accepted    | ... | 193 items | hand-classified, one pass, this document |
| P3 — raw per-lens findings  | every finding in the lens reports, accepted or not | 554 |
```
> "They are **not** interchangeable and mixing them is the easiest way to produce a wrong
> conclusion." … "**The bucket distribution in §3 is over P2, not P1.** … P2 therefore
> over-represents Criticals and under-represents Minors".

**The correct value:** bucket D is **49 / 193 = 25.4 % of accepted, enumerated findings (P2)**.
Against "all findings ever" (P3 = 554, verified as the sum of the ten headline totals
58+38+63+56+65+61+57+64+34+58 = 554) it is **49 / 554 = 8.8 %** — a ~2.9× overstatement.

**Verified as TRUE in the same sentence:** "non-zero in all ten rounds". Column D of the aggregate
table reads 4, 4, 8, 7, 6, 5, 2, 5, 5, 3 — sum 49, no zero. ✓

Also note the source's own reliability caveat on this number, which the bundle drops: the
classification is "**hand-classified, one pass, this document**" (`:22`).

**Concrete proposed fix:** spec §6:258 → "bucket D … is **25.4 % of the 193 *accepted, enumerated*
findings (population P2, which the source flags as Critical-biased and hand-classified in one
pass), non-zero in all ten rounds**". Do not say "of all findings ever" — that population is 554
and D's share of it is 8.8 %.

## F9 — MINOR: "12× swing in ARTIFACT SIZE" misnames the source's "12× SCOPE cut", and the bundle contradicts itself — the plan says "scope"

**Severity: MINOR** (but it is a two-place inconsistency inside one bundle, and it is the number
the bundle uses to license "judge the audit on Criticals per lens, not the total").

**What the bundle claims:**

- Spec:7 — "**15.14 ± 0.83 findings per lens** across a **12× swing in artifact size** (CV 5.5 %)"
- ADR:7 — identical wording, "a 12× swing in **artifact size**"
- Plan:385 — "(15.14 ± 0.83 per lens across a **12× scope** swing)" ← **different word**

**What the source says** (`meta-analysis-audit-finding-rate.md:118-120`):

> "Restrict to the seven **4-lens** rounds … the ones spanning the entire **12× scope cut** from
> **'6 decisions across 6 packages'** to **'one option, one sentinel, one status'**"

**The correct value:** the 12× is a swing in **decisions in scope**, not artifact size. The
document's only size proxy is the "lens-report lines / lens" column, which across those same seven
4-lens rounds runs 1,005 / 929 / 574 / 739 / 576 / 903 / 868 — a spread of **1.75×**, not 12×.

**Verified as TRUE in the same sentences:** 15.14 ± 0.83, CV 5.5 %, seven 4-lens rounds, r = 0.855,
193 accepted findings across 10 rounds, "~10 % of that corpus" (19/193 = 9.8 %), and the lineage
shares 4.6 % / 6.3 % / 22.6 % / 35.3 %. ✓ All match the source exactly.

**Concrete proposed fix:** change "artifact size" → "scope (6 decisions across 6 packages down to
one option, one sentinel, one status)" in spec:7 and ADR:7, matching plan:385.

## F10 — CRITICAL: §3.1's guard as specified CANNOT BE WRITTEN — `specFor` is unexported, and every alternative placement is blocked. Building it requires a production change, which contradicts the ADR's "zero production risk"

**Severity: CRITICAL.** Executed (two compile probes).

**What the bundle claims:**

- Plan:336 — "The guard: fill `w`; `n := spec.FromWire(base, w)`; `var got NodeWire;
  spec.ToWire(n, &got)`".
- Plan File Structure:64 and Task 3:303 — the file is
  `definition/activity/wire_roundtrip_test.go`, "**`package activity_test`**".
- ADR Consequences:124-126 — "**Zero production risk**: no type changes, no wire changes, no API
  changes, no migration. **The only non-test change is Decision 5's one-line list entry.**"
- Spec §4:229-230 — "**No production code changes** beyond §3.4's one-line list entry."

**There is no `spec` obtainable from `package activity_test`.** `definition/model/registry.go:58`:

```go
// specFor returns the registered spec for a kind.
func specFor(k NodeKind) (NodeSpec, bool) { … }     // UNEXPORTED
var nodeRegistry = map[NodeKind]NodeSpec{}          // UNEXPORTED
```

`RegisterKind` (`:45`) is the only exported registry function — write-only. The leaf package builds
its `FromWire`/`ToWire` closures inline inside `init()` (`definition/activity/activity.go:235-263`)
and stores them nowhere else, so they are unreachable by symbol too.

**Probe 1 — the plan's sketch, compiled:**
```
$ go vet ./definition/activity/
EXIT=1
vet: definition/activity/zz_rt_test.go:16:20: undefined: model.SpecFor
```

**Probe 2 — the obvious workaround (put the guard in `package model`, which *can* call `specFor`,
and import `activity` to trigger its `init`):**
```
$ go vet ./definition/model/
EXIT=1
package github.com/kartaladev/wrkflw/definition/model
	imports github.com/kartaladev/wrkflw/definition/activity from zz_cycle_test.go
	imports github.com/kartaladev/wrkflw/definition/model from activity.go: import cycle not allowed in test
```

`package model_test` (external) cannot call `specFor` either. **All three placements are blocked.**

I enumerated `definition/model`'s entire exported surface: the only exported `NodeWire` ↔ `Node`
path is `ProcessDefinition.MarshalJSON` / `UnmarshalJSON` (`node_wire.go:162`, `:184`) — a
**whole-definition** JSON round-trip, not the per-node spec pair the plan describes.
(`PutActivity`/`Activity`/`PutWait`/`Wait` are exported but cover only the shared fields, not
`Eligible*`/`Manual`/`Outcomes`/`Validation` — so they cannot stand in either.)

**A second defect in the same sketch, which survives whichever fix is chosen:** `spec.ToWire(n,
&got)` is **not** the real write path. `definition/model/node_wire.go:87-95`'s `toWire` seeds
`NodeWire{ID, Kind, Name}` and the `rawLabel` before delegating:

```go
w := NodeWire{ID: n.ID(), Kind: n.Kind(), Name: n.Name()}
if lc, ok := n.(interface{ rawLabel() string }); ok { w.Label = lc.rawLabel() }
if s, ok := specFor(n.Kind()); ok && s.ToWire != nil { s.ToWire(n, &w) }
```

Calling `spec.ToWire` directly leaves `ID`, `Kind`, `Name` and `Label` zero, so the guard would
classify **four fields that genuinely do round-trip** as "not owned by `KindUserTask`" and demand
exception entries for them — poisoning the very list §3.1 says must be derived by execution
(plan:340-343: "*A field that appears here and should not is a finding*"). Four would appear, and
all four should not.

**Concrete proposed fix — pick one, and correct the ADR either way:**

- **(a) Export the lookup.** Add `func SpecFor(k NodeKind) (NodeSpec, bool)` to
  `definition/model/registry.go` (one line delegating to `specFor`). Then **ADR:124-126 and spec
  §4:229-230 are false as written** and must say: "one new exported symbol, `model.SpecFor` — a
  read-only registry accessor added so the guard can reach the conversion pair." Note this widens
  the public API of the product, which ADR-0004's library-first framing makes a real (if small)
  decision, not a test detail.
- **(b) Keep zero production change** by driving the round-trip through
  `ProcessDefinition.MarshalJSON`/`UnmarshalJSON` on a built one-node definition. This is
  writable from `package activity_test` today, but it is a **different test** from the one the plan
  specifies: Task 3 Steps 1–6 (the filler, the `spec.FromWire`/`spec.ToWire` calls, and both
  prescribed mutations) must be rewritten, and the fixture must satisfy `Build`'s validation.

Whichever is chosen, add `Label`/`ID`/`Kind`/`Name` handling explicitly (drive the write side
through the real `toWire` path, or seed `got` the way `toWire` does) so the derived
`notOwnedByUserTask` list is not born with four wrong entries.

## F11 — §3.4's backlog-141 counts are CORRECT, including "a class with two members" (no finding)

Recorded because this repo's history on exactly this count is 1 → 2 → 3, each correction wrong.

- `PolicyAtRestLocations` has **3** entries today (`casbin_rule`,
  `wrkflw_human_task.eligibility`, `wrkflw_definitions.definition`) and `SECURITY.md:236` publishes
  "Authorization policy is durable at rest in **three** places". ⇒ **3 → 4 is right.** ✓
- The §3.4 premise holds: `engine/state.go:286` `Tasks []humantask.HumanTask` and
  `humantask/humantask.go:97` `Eligibility authz.AuthzSpec` ⇒ `wrkflw_instances.snapshot` really
  does carry the full `AuthzSpec`. ✓
- `internal/atrest/render.go:404-414` really does fail only for "`policy`-classed column(s)", so
  §3.4's "structurally cannot see the omission" is right. ✓
- **"A `freeform` column carrying policy is now a class with two members" — I tried to refute this
  and could not.** There are **11** `ClassFreeform` columns (`classification.go:106, 115, 127, 128,
  136, 153, 154, 167, 175, 193, 194`). Only two carry policy. The two candidates that could have
  made it three are both clean: `wrkflw_outbox.payload` — no event type declares an
  `Eligibility`/`AuthzSpec` field; and `wrkflw_journal.trigger` — the journal stores triggers
  (`store_core.go:313`, `:372`), and the only other `AuthzSpec` carrier, `engine.AwaitHuman`
  (`engine/command.go:148-150`), is a **Command**, never marshaled (no `json`/`Marshal` reference in
  `engine/command.go`; consumed in-memory by `runtime/processdriver_action.go:333-334`). ✓

**Anchors verified correct:** `node_wire.go:101/:109/:119`, `node.go:87`, `yaml.go:63/:112-114/:141`,
`activity.go:240/:251`, `step_nodes.go:724`, `step_nodes_test.go:32`,
`internal_leak_test.go:100`, `classification.go:106`, `node_wire_test.go:11`,
`event/event.go:388-389/:398`, `event/options.go:268/:283`, `render.go:404-414`. **All 18 resolve to
what the bundle says they are, at `862294ef`.**

**Backlog 143/144 supporting greps verified:** `grep -in "trigger" definition/model/yaml.go` → EXIT=1
(no match); `grep -in "boundary" definition/model/yaml.go` → EXIT=1 (no match). ✓

---

# Summary — COUNTING lens

| severity | count | findings |
|---|---|---|
| **CRITICAL** | 3 | F2, F3, F10 |
| **MAJOR** | 5 | F1, F5, F6, F7, F8 |
| **MINOR** | 2 | F4, F9 |
| confirmed-correct (no finding) | 2 | F0, F11 |

**Tally: 3 Critical, 5 Major, 2 Minor** = 10 findings, plus 2 recorded negative results.

**Most important finding: F10** — §3.1's guard, one of the delivery's three deliverables, **cannot
be written as specified**: `model.specFor` is unexported, and the internal-`package model`
workaround is an import cycle (both compiled and observed). Building it needs a new exported
symbol, which falsifies the ADR's "zero production risk / the only non-test change is Decision 5's
one-line entry". F2 is a close second and is the more interesting one for the ADR's thesis: the
three guards do **not** close the net — conversion site #1 (`yaml.go`'s literal) is unguarded, and
I executed the miss end-to-end (`eligible_groups` accepted by strict decoding, silently dropped,
guard green throughout), which converts the loud failure this delivery was built to catch into a
silent fail-open on eligibility.
