# ADR-0185-core — COUNTING lens audit

Worktree: `wt-counting`, detached at `5ce393f4`. Bundle present (step 0 ✅):
`docs/specs/2026-08-23-authz-identity-core.md`,
`docs/adr/0185-authorization-identity-is-not-self-asserted.md`,
`docs/plans/2026-08-23-authz-identity-core.md`.

Lens: re-derive every enumeration, quantifier, count, inherited citation and line anchor.

---

### C0 — the 29/9/5 pin table is EXACT (confirmed, per-file)

Not a finding. Re-derived independently of the spec's own numbers.

```
$ grep -rn --include='*.go' -E '\"actor\"\s*:|\"by\"\s*:' transport/   # wire-key sites
fiber/fiber_test.go        563 585 592 615 624   -> 5
gin/gin_coverage_test.go   192 218 244           -> 3
gin/gin_test.go            413 421 443 453       -> 4
httpcore/dto_test.go        57  68  79 151 161   -> 5
parity/parity_test.go      518                   -> 1
stdlib/coverage_test.go     92 126               -> 2
stdlib/errors_test.go      155 187               -> 2
stdlib/stdlib_test.go      471                   -> 1
$ grep -rn --include='*.go' -E 'ClaimInput\{|CompleteInput\{|ReassignInput\{' .
httpcore/endpoints_test.go 405 422 465 485 528 557 -> 6  (all six verified to carry Actor:/By:)
httpcore/validate_test.go   61  -> ClaimInput{} , no field named, correctly EXCLUDED
```
Total **29**, 9 files, 5 packages. Per-package: httpcore 11 (5+6), gin 7, fiber 5,
stdlib 5, parity 1 — every number in spec §2.6 matches. The `validate_test.go:61`
exclusion is also correct: `httpcore.Validate(httpcore.ClaimInput{})` names no field
and survives field removal.

Anchors `dto.go:44,50,66` all resolve to exactly the fields the spec names. ✅

---

### F1 — the pin net is closed over the three FIELDS but NOT over the `Actor` TYPE they are the only users of; D1 orphans an exported type and leaves a test for dead API

**Severity: MAJOR** (missed BREAKING surface + a test that becomes vacuous)

**Claim attacked** — spec §2.5 (`docs/specs/2026-08-23-authz-identity-core.md:145-147`):

> `transport/http/httpcore/dto.go:44,50,66` declares **exactly three** Actor-bearing
> fields (`ClaimInput.Actor`, `CompleteInput.Actor`, `ReassignInput.By`), so the pin net
> below is closed **by construction** rather than by a grep. ✅

and spec §3 `:203-205` / ADR `:141-142`: `Actor`/`By` are **removed** from the three inputs.

**Re-derivation.**
```
$ grep -rn --include='*.go' 'httpcore\.Actor' .
service/instance_test.go:1090   (comment)
service/instance_test.go:1128   (comment)
transport/http/httpcore/dto_test.go:47        <-- var got httpcore.Actor
transport/http/httpcore/endpoints_test.go:405 422 466 485 531 560   (the 6 already counted)
$ grep -rn --include='*.go' -E '(^|[^.a-zA-Z])Actor\{' transport/http/httpcore/*.go | grep -v _test.go
(no output — the httpcore.Actor type is constructed NOWHERE in non-test httpcore code)
```

`transport/http/httpcore/dto_test.go:45-53` is a whole dedicated test:
```go
func TestActorJSONTags(t *testing.T) {
	const in = `{"id":"u1","roles":["admin","user"]}`
	var got httpcore.Actor
	...
}
```
Its JSON body is `{"id":…,"roles":…}` — it contains **neither** `"actor"` nor `"by"`,
and it constructs **no** `*Input{`. **The author's net structurally cannot see it.**

**The correct value / what is wrong.** "Closed by construction" is a **scope** error, the
exact class this lens is briefed on: closing the net over *fields tagged `actor`/`by`*
does not close it over the **type** those fields are the only users of. After D1 the three
fields are the *sole* remaining references to `httpcore.Actor` (the six `endpoints_test.go`
uses die with the fields; the two `service/instance_test.go` hits are comments). So the
bundle silently faces a choice it never states:

- **keep `httpcore.Actor`** ⇒ a public exported type in the product API surface with zero
  producers and zero consumers, plus `TestActorJSONTags` testing dead API; or
- **remove `httpcore.Actor`** ⇒ a **fourth** exported-symbol removal absent from every
  BREAKING list in the bundle (spec §3 `:242`, ADR, plan), and `TestActorJSONTags` must be
  **deleted**, making the touched-test count **30 across 9 files**, not 29.

Related second-order effect the count also hides: after D1, `ClaimInput` has **zero
fields** (`Actor` is its only one — `dto.go:43-45`). `validate_test.go:61`
`TestValidateStructWithNoTags` was correctly excluded as a *compile* pin, but it becomes
**vacuous**: "ClaimInput has no required tags" is then trivially true of an empty struct,
so the test no longer exercises `Validate`'s no-tags path. Under §7's own rule ("every
prescribed test states what makes it fail today") this is a test that can no longer fail.

**Concrete proposed fix.**
1. Spec §2.5: replace *"closed by construction"* with the scope it actually has —
   *"closed by construction over the three tagged **fields**; the `httpcore.Actor`
   **type** is a separate surface and is enumerated separately."*
2. Add a decision to §3 and to the ADR's BREAKING list: **`httpcore.Actor` is removed**
   (it has no other user), making the exported-symbol removals four, not three; and
   restate §2.6 as **"29 compile pins + 1 type-level test (`dto_test.go:45`,
   `TestActorJSONTags`) that is deleted ⇒ 30 touched sites in 9 files"**.
3. Repoint `validate_test.go:61` at a DTO that still has non-required fields
   (e.g. `httpcore.ReassignInput{}` or `MessageInput` minus its required tag case), or
   state explicitly that the no-tags path is covered elsewhere and delete it.
4. Fix the two stale comments at `service/instance_test.go:1090,1128`, which describe
   `httpcore.Actor` as the reason the transport is bypassed.

---
### C1 — confirmed counts (re-derived, no finding)

- **FIVE `Authorizer` implementations** (spec §2.3). Re-derived *including* test files —
  `grep -rn --include='*.go' -E 'func \([^)]*\) Authorize\(' .` returns exactly the five
  named, at exactly the anchors given (`authz/authz.go:106`, `:124`,
  `casbinauthz/casbinauthz.go:162`, `internal/authz/casbin/authorizer.go:43`,
  `processtest/spyauthz.go:44`). No mockgen double and no embedding-based implementer
  exists. Visibility labels (3 public root-package, 1 internal, 1 public test harness)
  are correct. ✅
- **`RoleAuthorizer` does not evaluate `Privileges`, documented at `authz/authz.go:119-120`** —
  anchor resolves to exactly the two godoc lines that say it. ✅
- **`.Authorize(` at exactly five non-test sites** (spec §2.4). Re-derived: four in
  `runtime/task/service.go` + `casbinauthz/casbinauthz.go:163`. The **verb labels are
  also correct** — `grep -n '^func '` puts `:199` inside `Claim` (`:194`), `:234` inside
  `Reassign` (`:219`), `:255` inside `Complete` (`:250`), `:306` inside
  `RefreshCandidates` (`:294`). ✅
- **`WithActorResolver` exported exactly three times** (spec §3): `service/options.go:99`,
  `runtime/task/service.go:113`, `processtest/harness.go:104`. ✅
- **`authz.AuthzSpec` has exactly three exported fields at `authz/authz.go:82-86`**; `:82`
  is the `type` line and `:83-85` the three fields. ✅
- **`authz.ErrNotAuthorized` at `authz/authz.go:27` is the package's single sentinel.** ✅
- **`internal/atrest.PolicyAtRestLocations` has exactly three entries and omits
  `wrkflw_instances.snapshot`** (spec §2.2 / backlog 141). Confirmed at
  `internal/atrest/classification.go:63`, and `{wrkflw_instances, snapshot}` is indeed
  `ClassFreeform` in `Classification`. ✅
- **Migrations: exactly one file per dialect**, plus the casbin one (spec §2.7).
  `find . -name '*.sql' -not -path './docs/*'` returns exactly four:
  `store/migrations/{postgres,mysql,sqlite}/0001_init.sql` and
  `internal/authz/casbin/migrations/0001_casbin_rule.sql`. ✅
- **No fourth durable `AuthzSpec` copy beyond the three named.** Checked every JSON column
  the schema declares (`wrkflw_journal.trigger`, `wrkflw_outbox.payload`,
  `wrkflw_human_task.{claim_actor,completion_actor,candidates,vars}`,
  `wrkflw_call_links`, `wrkflw_timers`, `wrkflw_chain_links`) and grepped
  `Eligibility`/`AuthzSpec` repo-wide: no event payload, journal trigger or outbox row
  carries an `AuthzSpec`. The three in §2.1 are the closed set. ✅

---

### F2 — "the mint site" is singular, and it names the copy the four `Authorize` sites do NOT read

**Severity: MAJOR** (wrong anchor on a load-bearing decision; understates the D3 edit surface)

**Claim attacked** — spec §2.5, `docs/specs/2026-08-23-authz-identity-core.md:144`:

> - D3: `engine/step_nodes.go:732` (`Eligibility: spec`, **the mint site**). ✅

**Re-derivation.**
```
$ grep -rn --include='*.go' 'Eligibility' . | grep -v _test.go   # (relevant rows)
engine/step_nodes.go:732:               Eligibility: spec,
engine/step_nodes.go:811:       cmds = append(cmds, AwaitHuman{TaskID: taskID, Eligibility: spec})
runtime/processdriver_action.go:464:            Eligibility: cmd.Eligibility,
```
`spec` is constructed once at **`engine/step_nodes.go:723-727`** and then attached in
**two** places, which feed **different** durable copies:

| attachment | reaches |
|---|---|
| `step_nodes.go:732` (`ht.Eligibility`) | `c.s.Tasks` → `InstanceState.Tasks[]` → **`wrkflw_instances.snapshot`** (copy 2 of §2.1) |
| `step_nodes.go:811` (`AwaitHuman{…}`) | `performAwaitHuman` → `processdriver_action.go:464` → **`wrkflw_human_task.eligibility`** (copy 1 of §2.1) |

Verified from the runtime's own code — `performAwaitHuman`
(`runtime/processdriver_action.go:457-475`) builds the store row from **`cmd.Eligibility`**,
not from `st.TaskByID(...)`; the `if t := st.TaskByID(cmd.TaskID); t != nil` block that
follows copies back only `NodeID`, `CreatedAt` and `Vars`.

**The correct value.** There are **two** attachment sites in `step_nodes.go` and a **third**
projection in `runtime/`. `:732` is the *snapshot* mint, and §2.1's own table says the
copy read by **all four `Authorize` sites** is `wrkflw_human_task.eligibility` — which is
minted from `:811`, the line the anchor does not name. The definite article
("**the** mint site") makes a closed-set claim over a set of two.

**Why it bites, not just cosmetics.** D3 adds `Open bool` to `authz.AuthzSpec`; a plan
step anchored at `:732` edits the struct-literal *field list of `ht`*, whereas the value
must be set where `spec` is **built**, at `:723-727`, so that *both* copies carry it. An
implementer given `:732` who adds `Open:` there sets it on the snapshot copy only and
leaves `AwaitHuman.Eligibility.Open == false` — a **fail-closed on every newly minted
task**, i.e. exactly the stranding §5.2 exists to prevent, introduced by the fix itself.

**Concrete proposed fix.** Restate spec §2.5 D3 as:
*"`engine/step_nodes.go:723-727` — the single `authz.AuthzSpec{…}` construction. It is
attached twice: `:732` (`ht.Eligibility` → snapshot copy) and `:811`
(`AwaitHuman{Eligibility: spec}` → `runtime/processdriver_action.go:464` →
`wrkflw_human_task.eligibility`, the copy all four `Authorize` sites read). The `Open`
value is set at the construction, never at an attachment."* Mirror the same correction in
every plan step that cites `:732`.

---
### F3 — ⛔ CRITICAL. The wire net misses `definition/model/yaml.go`: `nodeYAML` is a SECOND wire struct with its own `yaml:` tags, so `eligible_open` is UNAUTHORABLE IN YAML — and strict decoding REJECTS it

**Severity: CRITICAL** (D3 as planned makes an open user task impossible to author in YAML,
one of the repo's only two authoring forms)

**Claim attacked** — plan `docs/plans/2026-08-23-authz-identity-core.md:696-698`:

> ⚠ **The wire round-trip has TWO mapping sites, not one.** `definition/activity/activity.go:240`
> (NodeWire → UserTask) and `:251` (UserTask → NodeWire) both list the three `Eligible*` fields

and Task 5's file list, plan `:637-638`:

> **Files:** Modify `definition/activity/activity.go`, `definition/activity/options.go`,
> `definition/model/node_wire.go`, `definition/model/validate.go`; tests alongside.

**Re-derivation.** The two `activity.go` anchors are **correct** (`:240` FromWire, `:251`
ToWire — verified, both list the three `Eligible*` fields). The **net** is not.
```
$ grep -rn --include='*.go' 'eligible_' . | grep -v '^./docs'
definition/model/node_wire.go:27-29   json:"eligible_roles|_privileges|_expr,omitempty"
definition/model/yaml.go:25-27        yaml:"eligible_roles|_privileges|_expr,omitempty"   <-- SECOND STRUCT
```
`definition/model/yaml.go:19` declares `nodeYAML`, a **separate** flat struct with its own
`yaml:` tags, mapped into `NodeWire` by `fromNodeYAML` (`yaml.go:85`, the `w := NodeWire{`
at `:106`). So the wire surface for a new key is **five** sites, not two:
`node_wire.go` (JSON decl) · **`yaml.go:19-27` (YAML decl)** · **`yaml.go:106+`
(`nodeYAML`→`NodeWire`)** · `activity.go:240` · `activity.go:251`.

**`yaml.go` / `nodeYAML` appear ZERO times in all three bundle documents:**
```
$ for p in 'yaml.go' 'nodeYAML'; do grep -c -- "$p" <spec> <adr> <plan>; done
yaml.go    spec:0  adr:0  plan:0
nodeYAML   spec:0  adr:0  plan:0
```

**EXECUTED consequence.** Throwaway probe `definition/model/zz_probe_counting_test.go`
(`package model_test`, deleted after the run), parsing a definition whose user task carries
`eligible_open: true`:
```
$ go test -count=1 -run '^TestZZProbeEligibleOpenYAMLKeyToday$' -v ./definition/model/
=== RUN   TestZZProbeEligibleOpenYAMLKeyToday
    ParseYAML(eligible_open: true) err = workflow-definition: parse YAML: yaml: unmarshal errors:
      line 9: field eligible_open not found in type model.nodeYAML
    RESULT: REJECTED by the strict decoder
--- PASS: TestZZProbeEligibleOpenYAMLKeyToday (0.00s)
```
The error **names `model.nodeYAML`** — the struct no document mentions.

**Why this is Critical, not clerical.** ADR-0167 made definition decoding **strict**, so an
unknown key is **rejected, not ignored**. Implementing Task 5 exactly as written yields:

- a YAML user task **with** `eligible_open: true` ⇒ **rejected at parse** (`field
  eligible_open not found in type model.nodeYAML`);
- the same task **without** it ⇒ **rejected by D3's new `model.Validate` gate**
  (`ErrEligibilityNotStated`, plan `:647`).

⇒ **A YAML-authored open user task becomes impossible to express.** CLAUDE.md states
*"there is no BPMN2 XML loader — **YAML and direct Go code are the authoring forms**"*, so
this removes one of the two supported authoring forms for the whole open-eligibility
feature. It is also a silent one: nothing in the plan's verification would notice, because
Task 5's tests are scoped to `definition/activity` and `definition/model/node_wire.go`.

**Concrete proposed fix.**
1. Add `definition/model/yaml.go` to Task 5's file list; add
   `EligibleOpen bool \`yaml:"eligible_open,omitempty"\`` to `nodeYAML` (beside `:25-27`)
   and the `EligibleOpen: ny.EligibleOpen` line to the `NodeWire` literal at `yaml.go:106+`.
2. Correct plan `:696-698` to *"the wire surface has FIVE sites: two tag declarations
   (`node_wire.go:27-29` JSON, `yaml.go:25-27` YAML), one `nodeYAML`→`NodeWire` mapping
   (`yaml.go:106+`), and two `activity.go` mappings (`:240` FromWire, `:251` ToWire)"*.
3. Add a **YAML round-trip** test to Task 5's Step 1 — `ParseYAML` a task carrying
   `eligible_open: true`, assert it builds and `Validate` passes. What makes it fail today
   is executed above: `field eligible_open not found in type model.nodeYAML`.

---

### F4 — ⛔ CRITICAL. `NodeWire.EligibleOpen` breaks ADR-0187's `TestDefinitionEligibilityFieldsAreTheDeclaredSet`, which the bundle never mentions — mutation-verified RED

**Severity: CRITICAL** (an unlisted cross-package guard fails, and its own message says the
published `SECURITY.md` posture must be re-derived)

**Claim attacked.** The plan names exactly one `internal/atrest` edit, plan `:82`:

> | `internal/atrest/classification.go` *(modify)* | backlog 141 — the missing policy-at-rest location | 5 |

and plan `:1137-1139` scopes it to adding `wrkflw_instances.snapshot` (*"the published count
goes from 3 to 4"*). Task 5 (plan `:643`) explicitly produces
**`model.NodeWire.EligibleOpen bool \`json:"eligible_open,omitempty"\``**.

**Re-derivation.** `internal/atrest/classification_test.go:199`:
```go
// What makes this test fail: any field whose name begins with "Eligible" being
// added to, removed from, or renamed in definition/model.NodeWire. Verified by
// mutation (a fourth field added to NodeWire: RED, naming the new field).
func TestDefinitionEligibilityFieldsAreTheDeclaredSet(t *testing.T) { … }
```
It reflects over `model.NodeWire{}` and asserts the `Eligible*` set is **exactly three**.

**MUTATION EXECUTED** (worktree `wt-counting`, `cp` backup of `node_wire.go`, restored
after — `git diff --stat` clean). Added the field Task 5 prescribes verbatim:
```
$ go test -count=1 -run '^TestDefinitionEligibilityFieldsAreTheDeclaredSet$' -v ./internal/atrest/
--- FAIL: TestDefinitionEligibilityFieldsAreTheDeclaredSet (0.00s)
    expected: [EligibleExpr…, EligiblePrivileges…, EligibleRoles…]           (len=3)
    actual  : [EligibleExpr…, EligibleOpen eligible_open,omitempty, …]       (len=4)
    Messages: NodeWire's eligibility fields changed; PolicyAtRestLocations'
              wrkflw_definitions.definition entry names these by JSON key and
              must be re-derived
$ go test -count=1 ./internal/atrest/ ./definition/...
FAIL github.com/kartaladev/wrkflw/internal/atrest   (this one test only)
ok   all 14 definition/... packages
```
Blast radius is exactly one test — but that test's message is a **direct instruction the
bundle does not carry out**: `internal/atrest/classification.go:75-79`'s
`wrkflw_definitions.definition` `Detail` string enumerates the three JSON keys in prose
(*"every node's `eligible_roles`, `eligible_privileges` and `eligible_expr` are serialized
INSIDE that JSON"*), and that prose is what ADR-0187 publishes into `SECURITY.md`.

**The correct value.** The `internal/atrest` edit is **two** changes, not one:
(a) backlog 141's new `wrkflw_instances.snapshot` location, and (b) the
`eligible_open` key added to the `wrkflw_definitions.definition` `Detail` prose **and** to
`classification_test.go:213-216`'s pinned set.

⚠ **Note the ADR-0187 lesson recurring a second time in this bundle.** The prose in
`classification.go:75-79` is exactly the category ADR-0187's post-mortem flagged: text the
generator does not *derive*, compared only against itself. The reflect-based pin at `:199`
is what catches it — but only if someone runs `./internal/atrest/`, and it is not in any
task's verification command.

**Concrete proposed fix.** Extend plan `:82` and Task 11 (`:1137`) to:
*"`internal/atrest` — (1) add the `wrkflw_instances.snapshot` policy location (backlog 141);
(2) add `eligible_open` to the `wrkflw_definitions.definition` Detail prose
(`classification.go:75-79`) and to the pinned set in
`classification_test.go:213-216`, which `NodeWire.EligibleOpen` makes RED (mutation-verified:
`--- FAIL … len=3 vs len=4`). Verification: `go test -count=1 ./internal/atrest/`."*
Add `go test -count=1 ./internal/atrest/` to **Task 5's** verification too — the package
that breaks it is `definition/model`, and Task 5's own commands never touch `internal/atrest`.

---
### F5 — `CustomizeConfig` has EIGHT fields, not six, and the cited range `seam.go:19-33` covers three of them

**Severity: MAJOR** (a false current-behaviour claim in the ADR's Context — Premise
Discipline class; and a textbook "audited bundle decays when its base moves")

**Claim attacked** — ADR `docs/adr/0185-…:56-57`:

> `CustomizeConfig` (`seam.go:19-33`) declares six fields and no identity seam, so a
> consumer's authentication middleware has no supported way to override it.

**Re-derivation.**
```
$ awk '/^type CustomizeConfig\[R any\] struct \{/,/^\}/' transport/http/httpcore/seam.go \
    | grep -nE '^\t[A-Z][A-Za-z0-9]* '
BasePath string · Wrap func(R) R · InstanceMapper func(engine.InstanceState) any
MaxBodyBytes int64 · BodyReadTimeout time.Duration
Logger *slog.Logger · TracerProvider trace.TracerProvider · MeterProvider metric.MeterProvider
$ grep -n 'type CustomizeConfig' transport/http/httpcore/seam.go   ->  20
   (closing brace at line 80)
```
**Correct value: EIGHT fields; the struct spans `seam.go:20-80`.** The cited `:19-33`
starts one line early and ends inside `MaxBodyBytes`'s doc comment, covering only
`BasePath`, `Wrap`, `InstanceMapper` and part of the fourth.

**Provenance — this is an inherited claim that was NOT re-derived.** `MaxBodyBytes` and
`BodyReadTimeout` are **ADR-0186's** additions (merge `13b3bfb0`), which landed *after* the
failed 2026-08-20/21 bundle was written. Spec §2.5 ("Anchors still resolve at the branch
point") lists four re-checked anchors — `endpoints.go`, `service/service.go`,
`step_nodes.go`, `dto.go` — and **`seam.go` is not among them**, so this number rode into
the re-cut untouched while its subject grew by two fields. This is precisely the pattern
this lens is briefed on: *a "re-derived" caption above a set that does not include the
number that rotted.*

The **conclusion** ("no identity seam") survives — none of the eight is an actor — but the
premise is false as written, and the same paragraph is what the plan's Task 10
(`plan:973`, "modify `seam.go` (two new options)") is sized against: the change takes the
struct from 8 to 10 fields, not 6 to 8.

**Concrete proposed fix.** ADR `:56` → *"`CustomizeConfig` (`seam.go:20-80`) declares
**eight** fields — `BasePath`, `Wrap`, `InstanceMapper`, `MaxBodyBytes`,
`BodyReadTimeout` (the last two added by ADR-0186), `Logger`, `TracerProvider`,
`MeterProvider` — and **no identity seam** …"*. Add `seam.go` to spec §2.5's re-checked
anchor list so the next revision cannot inherit it again.

---

### F6 — "three falsified godocs in `authz`" — there are TWO, and the same sentence cites only two

**Severity: MINOR** (self-contradicting count in a remediation checklist, restated four times)

**Claim attacked** — ADR `:291-293`:

> `authz`'s own **three** godocs are falsified too — `authz/authz.go:80-81` (*"An empty spec
> means allow-all"*) and `:111` (*"spec.Roles is empty (open access)"*) — and are corrected here

restated in the plan at `:72` (*"three falsified godocs"*), `:421` (Task 3 title,
*"and the three falsified godocs"*) and `:480` (*"correct the three godocs the change
falsifies"*).

**Re-derivation.**
```
$ grep -rn -i 'allow-all\|allow all\|open access\|empty spec\|is empty' authz/*.go
authz/authz.go:80: // and an optional attribute predicate …. An empty spec
authz/authz.go:81: // means allow-all.
authz/authz.go:111: //  1. spec.Roles is empty (open access), OR the actor shares at least one role
authz/authz_test.go:34: name: "empty spec roles always authorized"          (a TEST, not a godoc)
```
**Correct value: TWO godocs** — the `AuthzSpec` doc comment (`:79-81`, whose claim spans
`:80-81`) and the `RoleAuthorizer` doc comment (`:110-117`, whose claim is at `:111`).
The anchors given are both right; the *count* is wrong. Most likely the author counted the
three *lines* (`:80`, `:81`, `:111`) as three godocs.

The sentence is self-refuting — it says "three" and then lists two — so an implementer
working the checklist either hunts for a nonexistent third or ticks the box at two and
leaves an unexplained discrepancy. Both are cheap to prevent.

**Concrete proposed fix.** In all four places write **"the two falsified `authz` godocs —
`AuthzSpec` (`authz/authz.go:79-81`) and `RoleAuthorizer` (`:110-117`)"**, naming the
symbols rather than a bare count so a later line-shift cannot rot them.
⚠ While there: `authz/authz_test.go:34`'s case name *"empty spec roles always authorized"*
is falsified by the same change and is in no list — see F7.

---

### F7 — the D1 pin count is meticulous; D3 and D2 have NO change-surface count at all, and D3's is larger

**Severity: MAJOR** (asymmetric enumeration — the BREAKING budget is derived for one of the
three decisions and asserted for none of the others)

**Claim attacked** — ADR Consequences `:314-318`:

> **BREAKING in four places**: the three task DTOs lose their actor fields; `NewProcessEngine`
> can now fail where it previously succeeded; a zero `AuthzSpec` changes meaning; and a spec
> naming a dimension its authorizer does not evaluate now denies where it previously passed.
> **29** pin sites and three `examples/` mains change in the same bundle …

The "29" is D1's count (verified exact, see C0). Breaks 2, 3 and 4 in that same list get
**no** number anywhere in the bundle.

**Re-derivation of D3's surface.**
```
$ grep -rn --include='*.go' -E '(authz\.)?AuthzSpec\{' . | grep -v '^./docs' | wc -l
62        (61 in tests, 1 in production: engine/step_nodes.go:723)
```
Of these, the ones D3 flips from allow to deny are the **empty / dimension-less** specs:
```
authz/authz_test.go:35                      spec: authz.AuthzSpec{}      ("empty spec roles always authorized")
internal/authz/casbin/authorizer_test.go:69 spec: authz.AuthzSpec{}
humantask/memory_test.go:388                Candidates(ctx, authz.AuthzSpec{}, nil)
runtime/task/service_test.go:271            Eligibility: authz.AuthzSpec{}
runtime/task/service_test.go:524            Eligibility: authz.AuthzSpec{}, State: humantask.Unclaimed
service/errors_test.go:133                  Eligibility: authz.AuthzSpec{}, // open access — bypasses role check
```
— **six** sites in **five** packages (`authz`, `internal/authz/casbin`, `humantask`,
`runtime/task`, `service`), spanning **four** distinct Go packages the plan's fan-out
assigns to **four different agents**, plus `authz/authz_test.go:34`'s case *name*.

And D3's `model.Validate` gate has a far larger surface again:
```
$ grep -rn --include='*.go' 'NewUserTask(' . | grep -v '^./docs' | wc -l
274
```
**274 `NewUserTask(` call sites.** Every one that supplies no `WithEligibleRoles` /
`WithEligiblePrivileges` / `WithEligibleExpr` becomes an **authoring error** under
`model.ErrEligibilityNotStated`. The plan is aware of the *shape* of this
(`:704-709`: *"Re-derive the affected set across all three forms before changing `Validate`,
and report the number you find"*) but **defers the number to the implementer** — so the
bundle's own BREAKING budget is one decision's count plus two blanks, and the
un-derived one is an order of magnitude larger than the derived one.

**Concrete proposed fix.**
1. Derive the D3 numbers **before** dispatch, exactly as D1's 29 was, and put them in ADR
   Consequences/Negative beside the 29: the six allow→deny `AuthzSpec{}` sites above, and
   the `NewUserTask` fixtures that state no dimension (from 274 call sites).
2. State the per-package distribution — the plan fans out **by package** (CLAUDE.md rule
   #11), and D3's surface crosses `authz`, `internal/authz/casbin`, `humantask`,
   `runtime/task`, `service` and (via `NewUserTask`) most of the test suite. An
   uncounted cross-package break is precisely what breaks a parallel fan-out.
3. If the fixture count is large, say so in the ADR and decide the mitigation there
   (a test-only `authz.OpenSpec()` helper, or `processtest` fixtures defaulting to open)
   rather than discovering it mid-implementation.

---
### F8 — ⛔ CRITICAL. The "two pins that must be REWRITTEN" pair is wrong on BOTH members, and its "both are `by` sites" parenthetical is false

**Severity: CRITICAL** (the one enumeration the bundle singles out as safety-critical, and
the only one it prescribes a mutation for, names a test that does not do what is claimed
and omits the test that does)

**Claim attacked** — spec §2.6 `docs/specs/2026-08-23-authz-identity-core.md:173-176`,
repeated verbatim in the ADR at `:184-187`:

> ⚠ **Two of the 29 must be REWRITTEN, not recompiled**: `stdlib/errors_test.go:187` and
> `gin/gin_coverage_test.go:244` assert **403**, and after D1 they would still return 403
> **from the zero actor** — passing while testing nothing. Confirmed present in the
> enumeration above (**both are `"by"` sites**).

and §7 `:395-396`, which builds the bundle's only prescribed mutation on it:

> The two vacuous-403 pins (§2.6) are **rewritten**, and the rewrite is proved by mutation:
> revert D1's context read and confirm they go RED, which today they would not.

**Re-derivation.**
```
$ grep -rn --include='*_test.go' '403\|StatusForbidden' transport/
transport/http/httpcore/errors_test.go:37   (unit test on the error mapper — not one of the 29)
transport/http/stdlib/errors_test.go:158-159   "want 403 complete forbidden"
transport/http/stdlib/errors_test.go:190-191   "want 403 reassign forbidden"
```
**There is no `403` or `StatusForbidden` anywhere under `transport/http/gin/`.**

`gin/gin_coverage_test.go:241-249` is `TestTaskRoutes_Reassign_ErrorPath`; it posts to
`/tasks/**bad-token**/reassign` and asserts:
```go
if resp.StatusCode != http.StatusNotFound {   // 404, not 403
	t.Fatalf("want 404, got %d", resp.StatusCode)
}
```

The pin that *does* assert 403 and is **not named** is `stdlib/errors_test.go:155` —
`"actor": {"id":"bob","roles":["viewer"]}` → `if rr.Code != http.StatusForbidden` at `:158`.

**The correct value.** The two 403-asserting pins are **`stdlib/errors_test.go:155`** (a
`"actor"` site, complete verb) and **`stdlib/errors_test.go:187`** (a `"by"` site, reassign
verb) — **the same file, the same package**, one of each tag. So:

| bundle says | truth |
|---|---|
| `stdlib/errors_test.go:187` | ✅ correct |
| `gin/gin_coverage_test.go:244` | ❌ asserts **404**, not 403 |
| (missing) | ❌ `stdlib/errors_test.go:155` asserts 403 |
| "both are `"by"` sites" | ❌ one `"actor"`, one `"by"` |
| implied spread: 2 packages | ❌ 1 package, 1 file |

**Second-order — the diagnosis is also inconsistent with D1 itself.** The claim *"after D1
they would still return 403 from the zero actor"* presumes the zero actor reaches the
authorizer. D1's own rule (§3 `:219-220`, ADR `:158`) says `ActorFromContext` returning
`ok == false` ⇒ **401**. Neither `stdlib/errors_test.go` case puts an actor in the request
context, so under D1 as specified both return **401**, i.e. they go RED loudly rather than
passing vacuously. The vacuity risk only appears if the *rewrite* injects an empty actor or
enables `WithAnonymousActorAllowed`. ⚠ Flagged as a derivation, not an execution: D1 does
not exist yet, so I could not run it — the mechanism is named so it is checkable.
`gin_coverage_test.go:244` likewise becomes 401-vs-404 RED, so it needs a context actor,
which is a different remedy from "rewrite the assertion".

**Concrete proposed fix.**
1. Replace the pair in spec §2.6 and ADR `:184-187` with the derived one:
   *"Two of the 29 assert 403 and are the ones that must be REWRITTEN, not recompiled —
   `stdlib/errors_test.go:155` (`"actor"`, complete) and `:187` (`"by"`, reassign); both in
   `transport/http/stdlib/errors_test.go`. Command: `grep -rn --include='*_test.go'
   '403\|StatusForbidden' transport/`."*
2. Delete `gin/gin_coverage_test.go:244` from that list and move it to a new, separate list:
   **pins whose asserted status changes because the request now lacks a context actor** —
   `TestTaskRoutes_Reassign_ErrorPath` asserts 404 and would get 401. Sweep the other 26
   pins for the same class before dispatch; the bundle currently assumes 27 of 29 are
   pure recompiles and has verified that for none of them.
3. Rewrite §7's mutation prescription against the corrected pair, and state what makes it
   fail: today `stdlib/errors_test.go:155` returns 403 from the **body** actor `bob/viewer`;
   after D1 it must return 403 from a **context** actor `bob/viewer`, and reverting the
   context read must turn it 401 (no actor) rather than leaving it green.

---

### F9 — "three authoring forms" is four: `build.Builder.Add(model.Node)` accepts a directly-constructed `activity.UserTask{…}`

**Severity: MINOR** (no in-repo call site today; but it is public API, and it is the same
zero-value argument D3 itself rests on)

**Claim attacked** — plan `:704-709`:

> The failed bundle claimed *"only 5 …"*. Its grep covered **one of three authoring forms**,
> missing `definition/build.Builder.AddUserTask` (`build.go:117`, public API) and YAML
> `kind: userTask` (`activity.go:236`). **Re-derive the affected set across all three forms**

**Re-derivation.** Both named anchors are correct (`build.go:117`, `activity.go:236` ✅).
But `definition/build/build.go:68` exports:
```go
func (b *Builder) Add(n model.Node) *Builder { b.inner.Add(n); return b }
```
— any `model.Node`, so `b.Add(activity.UserTask{Base: model.NewBase("t","")})` is a **fourth**
authoring form that reaches neither `NewUserTask` nor the wire decoder. `activity.UserTask`
is exported with exported `EligibleRoles` / `EligiblePrivileges` / `EligibleExpr`
(`activity.go:26-33`), and Task 5 adds an exported `EligibleOpen bool` (plan `:642`) — so
this is *literally* the argument ADR `:222-229` makes against `Open *bool`, applied to the
node type instead of the spec type.

Also note `AddUserTask` is not an independent *field-setting* form — it delegates
(`return b.Add(activity.NewUserTask(id, opts...))`) — but it **is** an independent set of
call sites for the fixture re-derivation, which is what the plan is asking for.

**Mitigating fact, verified:** all four forms converge on **one** validation chokepoint.
`model.Validate` has exactly one production caller for definitions —
`definition/model/builder.go:133`, inside `Build()` — and `coreFromYAML`'s own comment says
*"Validation is deferred to Build"*, so the YAML path reaches it too. The gate's placement
is therefore sound; only the enumeration is short.

**Concrete proposed fix.** Plan `:706-708` → *"…covered one of **four** authoring forms,
missing `build.Builder.AddUserTask` (`build.go:117`), YAML `kind: userTask`
(`activity.go:236`), and `build.Builder.Add(activity.UserTask{…})` (`build.go:68`, which
accepts any `model.Node` and bypasses the constructor entirely). All four converge on
`model.Validate` via `definition/model/builder.go:133`, which is why the gate belongs
there."*

---

### F10 — the mint site is cited at two DIFFERENT anchors across the bundle, and the plan's is off by one

**Severity: MINOR** (cross-document inconsistency; compounds F2)

**Claim attacked.** Plan `:77`:

> | `engine/step_nodes.go:722-726` *(modify)* | the mint site carries `Open` from the node into the stored spec … |

vs spec §2.5 `:144`: `engine/step_nodes.go:732`. Plan `:790` adds *"builds the spec from
**exactly three** fields"*.

**Re-derivation.**
```
722: 	taskID := c.s.nextTaskID()
723: 	spec := authz.AuthzSpec{
724: 		Roles:      ut.EligibleRoles,
725: 		Privileges: ut.EligiblePrivileges,
726: 		Attribute:  ut.EligibleExpr,
727: 	}
```
**Correct: `:723-727`.** The plan's `:722-726` starts on an unrelated statement
(`taskID := …`) and stops before the closing brace. "Exactly three fields" ✅ is correct.
The spec's `:732` is a *different* line (`ht.Eligibility`) and, per **F2**, feeds the
snapshot copy rather than the `wrkflw_human_task` row.

**Concrete proposed fix.** Use one anchor in both documents — `engine/step_nodes.go:723-727`
— and prefer the symbol: *"the single `authz.AuthzSpec{…}` construction in
`userTaskStrategy.enter`"*. Plan `:828` already tells the implementer *"`AwaitHuman{…,
Eligibility: spec}` at `:811` reuses this `spec` variable — confirm that, do not assume it"*;
**confirmed here** (same variable), so setting `Open` at the construction covers both copies
— but only if the anchor points at the construction, which neither document's does.

---
### F11 — "all four residuals from spec §6" — §6 lists FIVE

**Severity: MINOR** (a delivery-gate checklist item that under-scopes the document it points at)

**Claim attacked** — plan `:1167-1170`, Task 17:

> - [ ] `SECURITY.md`: the fiber `c.SetContext` idiom, the 401/503 contract, and **all four
>   residuals** from spec §6 …

**Re-derivation.**
```
$ awk 'NR>=366 && NR<=383' docs/specs/2026-08-23-authz-identity-core.md | grep -E '^[0-9]+\.'
1. ProcessDriver.ApplyTrigger + engine.NewHumanCompleted bypass authorization by design (:372)
2. a consumer-implemented durable TaskStore receives no migration                        (:376)
3. D1 makes backlog 103 more reachable                                                    (:378)
4. backlog 90 and 124 stay open                                                           (:381)
5. backlog 141 and 142 are pre-existing, found here, filed                                (:382)
```
**Correct value: five.** The checklist's own parenthetical names residuals 1 and 2 only, so
the two it silently drops are exactly the ones a reader would most want in `SECURITY.md`:
**residual 3** (D1 widens the live ABAC surface with backlog 103 deferred and nothing
bounding it — the cost of the re-cut, which spec §1's D1×D4 row calls a hazard the cut
*opens*) and **residual 4** (backlog 90, claim-stealing, still open).

**Concrete proposed fix.** Plan `:1167` → *"…and **all five** residuals from spec §6
(1 ApplyTrigger/NewHumanCompleted bypass · 2 consumer `TaskStore` gets no migration ·
3 D1 makes backlog 103 live while 103 is deferred · 4 backlog 90 and 124 stay open ·
5 backlog 141/142 filed)"* — enumerate them rather than counting them, per Premise
Discipline's "prefer naming a closed set over counting it".

---

## Summary — counting lens

**Verified-correct (no finding):** the 29/9/5 pin table and every per-file and per-package
figure in it, and the `validate_test.go:61` exclusion · five `Authorizer` implementations
and their visibility · exactly five non-test `.Authorize(` sites and all four verb labels ·
`WithActorResolver` exported exactly three times · `AuthzSpec`'s three exported fields at
`authz.go:82-86` · `authz.ErrNotAuthorized` as the package's single sentinel ·
`DurableProvider`'s six methods and `WithDurableStore`'s six leaves (hence "the other five")
· `WithDurableStore` never writing `c.authz` · the five `LogAttrs` attributes at
`service.go:323` (hence "four unrelated") · three durable `AuthzSpec` copies with **no
fourth** (all JSON columns and event/outbox/journal payloads checked) · exactly one
migration file per dialect, so this bundle does land the first `0002_*.sql` · three
`examples/` mains at `:264`/`:278`/`:262` · `atrest.PolicyAtRestLocations`'s three entries
omitting `wrkflw_instances.snapshot` · backlog 141/142 already filed in `HANDOVER.md`
without collision · the plan's hedge that the three adapters' `options.go` differ (fiber
has no `WithBodyReadTimeout`) · `activity.go:240`/`:251` as genuine wire-mapping sites ·
and every one of the ~20 other `file.go:NNN` anchors spot-checked.

**Findings: 11.**

| # | severity | one-line |
|---|---|---|
| F1 | MAJOR | net closed over the three FIELDS, not the `Actor` TYPE — D1 orphans `httpcore.Actor` and leaves `TestActorJSONTags` testing dead API; touched sites are 30, not 29 |
| F2 | MAJOR | "the mint site" is singular over a set of two, and names the snapshot copy rather than the `wrkflw_human_task` row all four `Authorize` sites read |
| **F3** | **CRITICAL** | `definition/model/yaml.go`'s `nodeYAML` is a second wire struct absent from all three documents ⇒ `eligible_open` is **rejected by strict decoding** and an open user task is unauthorable in YAML (executed) |
| **F4** | **CRITICAL** | `NodeWire.EligibleOpen` makes ADR-0187's `TestDefinitionEligibilityFieldsAreTheDeclaredSet` RED (mutation-verified); the bundle's only `internal/atrest` edit is scoped to backlog 141 |
| F5 | MAJOR | `CustomizeConfig` has **eight** fields spanning `seam.go:20-80`, not six at `:19-33` — an inherited claim left out of §2.5's re-derived list, rotted by ADR-0186 |
| F6 | MINOR | "three falsified `authz` godocs" — there are two, and the sentence itself cites two; restated 4× |
| F7 | MAJOR | D1's change surface is derived to the site; **D2's and D3's are not derived at all**, and D3's is larger (six allow→deny `AuthzSpec{}` sites across five packages; 274 `NewUserTask(` call sites) |
| **F8** | **CRITICAL** | the "two pins that must be REWRITTEN" pair is wrong on both members — `gin_coverage_test.go:244` asserts **404**, and the real second one (`stdlib/errors_test.go:155`) is missing; "both are `by` sites" is false |
| F9 | MINOR | four authoring forms, not three — `build.Builder.Add(model.Node)` (`build.go:68`) takes a bare `activity.UserTask{…}` |
| F10 | MINOR | the mint site carries two different anchors across the bundle and the plan's `:722-726` is off by one (`:723-727`) |
| F11 | MINOR | "all four residuals from spec §6" — §6 lists five |

**Single most important finding: F3.** It is the classic scope failure this lens exists for
— the net covered `NodeWire`'s **JSON** tags and the two `activity.go` mappings and stopped
there, while `definition/model/yaml.go` declares a *separate* `nodeYAML` struct with its own
`yaml:` tags. Because ADR-0167 made decoding strict, the consequence is not a missing
feature but a **contradiction**: a YAML user task carrying `eligible_open: true` is refused
at parse (`field eligible_open not found in type model.nodeYAML` — executed), and the same
task without it is refused by D3's new `model.Validate` gate. Implementing the plan as
written removes one of the repo's two stated authoring forms for the whole feature, and no
test in any task's verification would see it.

F3, F4 and F8 all share one root: **the enumerations were derived over the packages the
author was editing** (`transport/`, `definition/activity`, `definition/model/node_wire.go`)
and asserted over the repo.
