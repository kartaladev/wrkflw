# Spec — definition decoding rejects unknown fields

- Date: 2026-08-07
- ADR: [0167](../adr/0167-definition-decoding-rejects-unknown-fields.md)
- Plan: [2026-08-07-strict-definition-decoding](../plans/2026-08-07-strict-definition-decoding.md)
- Closes: pre-v0.1.0 blocker 1

## 1. Problem

Both process-definition decoders are lenient. A field name the decoder does not
recognise is silently discarded, so a misspelled authorization tag leaves the
node with an empty `AuthzSpec` — and an empty `AuthzSpec` is documented to mean
**allow-all**. The failure is silent in both directions: nothing errors at parse
time, and nothing errors at authorization time. The task simply becomes open to
everyone.

This is recorded as pre-v0.1.0 blocker 1. It is a breaking change, so the window
to make it closes when v0.1.0 is tagged.

## 2. Measured ground truth

Every number below was produced by a throwaway probe run against `main`
(`9e96112`) on 2026-08-07 and then deleted. Commands are `go test -v -run
'^TestZZProbe' ./definition/model/`.

### 2.1 A typo'd tag degrades a UserTask to allow-all

Definition: one `userTask` whose eligibility tag is spelled four different ways.
The parsed `EligibleRoles` is fed to `authz.RoleAuthorizer` as
`AuthzSpec{Roles: …}` and evaluated for an actor holding only role `clerk`.

```
tag=eligible_roles   parse=OK EligibleRoles=[manager] spec.Roles=[manager] -> clerk DENIED
tag=eligable_roles   parse=OK EligibleRoles=[]        spec.Roles=[]        -> clerk *** ALLOWED (fail-open) ***
tag=eligible_role    parse=OK EligibleRoles=[]        spec.Roles=[]        -> clerk *** ALLOWED (fail-open) ***
tag=eligibleRoles    parse=OK EligibleRoles=[]        spec.Roles=[]        -> clerk *** ALLOWED (fail-open) ***
```

Three plausible misspellings — a transposition, a missing plural, and the
camelCase form a JSON-minded author would reach for — each turn a
manager-only task into an everyone task, with no diagnostic anywhere.

The second half of that mechanism is deliberate and is **not** changed here:
`AuthzSpec`'s doc comment states "An empty spec means allow-all", and
`RoleAuthorizer.Authorize` skips the role check entirely when `spec.Roles` is
empty (`authz/authz.go`, `if len(spec.Roles) > 0 && !hasAnyRole(...)`). See §4.

### 2.2 The JSON path is equally lenient

```
json={"id":"p",…,"eligible_roles":["manager"]}    -> err=<nil>  EligibleRoles=[manager]
json={"id":"p",…,"eligable_roles":["manager"]}    -> err=<nil>  EligibleRoles=[]
json={"id":"p",…,"totally_unknown_field":42}      -> err=<nil>  EligibleRoles=[]
```

### 2.3 ⚠ A consumer cannot fix the JSON side themselves

This is the finding that decides where the fix must live. A caller who does
everything right — constructs a `json.Decoder` and calls
`DisallowUnknownFields()` — gets **no protection at all**, because
`ProcessDefinition.UnmarshalJSON` performs a plain `json.Unmarshal` internally
and the outer decoder's setting does not survive the custom unmarshaler:

```
OUTER DisallowUnknownFields -> err=<nil>          ← on ProcessDefinition
plain json.Unmarshal        -> err=<nil>
plain struct, strict        -> err=json: unknown field "unknown"   ← mechanism itself works
```

The third line is the control: `DisallowUnknownFields` works correctly on a
struct with no custom unmarshaler. The mechanism is fine; the custom
unmarshaler is what discards it. Therefore the JSON fix **must** be applied
inside `ProcessDefinition.UnmarshalJSON`, not offered as caller advice.

### 2.4 The YAML mechanism works, and reaches into slice elements

`yaml.v3`'s `KnownFields(true)` was exercised against a mirror of the
`definitionYAML` shape, with a typo nested inside a node **and** a junk key at
the top level:

```
KnownFields(true)             -> err=yaml: unmarshal errors:
      line 9: field eligable_roles not found in type ...nodeMirror
      line 15: field bogus_top_level not found in type ...defMirror
KnownFields, nested typo only -> err=yaml: unmarshal errors:
      line 9: field eligable_roles not found in type ...nodeMirror
```

It reaches inside a slice element, reports **every** offending field, and gives
line numbers.

### 2.5 The two decoders report differently — accepted, not papered over

`encoding/json` stops at the first unknown field. Executed with three:

```
JSON, three unknown fields -> err=json: unknown field "un1"
```

So YAML yields a multi-error with line numbers and JSON yields one field name
and no position. This asymmetry is inherent to the two standard libraries and is
documented rather than normalised (see §4).

### 2.6 What strictness does NOT catch

`nodeYAML` and `NodeWire` are **flat unions**: one struct carrying the fields of
all node kinds, discriminated by `kind`. Strict decoding therefore rejects field
names unknown to the *union*, not names inappropriate to the *kind*. A
`timer_duration` on a `userTask` is a known field of the union and stays legal
after this change.

This is stated explicitly so the ADR cannot be read as promising kind-aware
validation. Kind-appropriateness is `model.Validate`'s concern, and is out of
scope.

### 2.7 Blast radius

The only definition-decoding entry points in the repo are `model.ParseYAML` and
`ProcessDefinition.UnmarshalJSON`. A sweep of `runtime/`, `service/` and
`transport/` for other definition-decode paths returned nothing — the one hit
was an unrelated comment in `runtime/kernel/armed_timer_paging.go` about cursor
decoding.

Definitions that must keep parsing after the change:

- `definition/model/testdata/order.yaml`
- inline YAML in `definition/example_test.go`, `definition/model/yaml_test.go`,
  `definition/model/validation_wire_test.go`, `definition/build/build_test.go`
- `examples/readme_quickstart/main.go`

No HTTP endpoint accepts a definition — registration goes through
`runtime.RegisterDefinition` / `kernel.MemDefinitionRegistry.Register`, both
called from Go. This is why no new error sentinel is introduced (§4).

## 3. Decisions

### D1 — Both decoders reject unknown fields, unconditionally

`ParseYAML` decodes via `yaml.NewDecoder` with `KnownFields(true)`.
`ProcessDefinition.UnmarshalJSON` decodes via a `json.Decoder` with
`DisallowUnknownFields()` applied to its **internal** unmarshal of
`definitionWire`.

### D2 — No opt-out

There is no `WithLenientDecoding` `LoaderOption` and no package-level toggle.

Rationale, and the precedent it follows: ADR-0159 faced the same
required-versus-optional choice for `TimerStore.ArmedTimer` and chose required,
reasoning that "recurrence is not an optional capability of a timer store the
way writing or stats are, and pre-release breakage is cheap. With a tag in
place, the capability route would be correct." The same holds here. An escape
hatch on a guard whose whole purpose is to prevent a silent authorization
bypass is the mechanism by which the bypass returns, and it would be permanent
API to support past 1.0.

Supporting fact: the YAML surface has no metadata or extension concept, so no
consumer has a sanctioned reason to carry extra keys today.

### D3 — Existing error shapes, no new sentinel

`ParseYAML` keeps wrapping as `workflow-definition: parse YAML: %w`.
`ProcessDefinition.UnmarshalJSON` returns its decode error **unwrapped** — there
is no `workflow-definition:` prefix on that path today (`return err`, verified in
source), and adding one would change every existing JSON decode error, not just
unknown-field ones. No `ErrUnknownField` sentinel is added.

`httpcore.ClassifyError` maps `ErrBadInput` and friends to 400, so a sentinel
would be justified if a transport parsed definitions — but none does (§2.7).
Adding one now would be speculative API on the guess that a definition-upload
endpoint arrives later. If one does, it introduces the sentinel with the
consumer that needs it.

### D3a — Empty input keeps its current meaning

`yaml.Unmarshal` returns `nil` for an empty or comment-only document;
`yaml.Decoder.Decode` returns `io.EOF`. Measured:

```
input=""                   Unmarshal err=<nil>  | Decoder err=EOF
input="\n"                 Unmarshal err=<nil>  | Decoder err=EOF
input="# just a comment\n" Unmarshal err=<nil>  | Decoder err=EOF
input="id: x\n"            Unmarshal err=<nil>  | Decoder err=<nil>
```

So a naive swap silently converts empty input from "an empty definition" into a
parse error — a second behaviour change riding along with the intended one.
`ParseYAML` maps `io.EOF` back to today's outcome. Whether an empty definition
*should* be rejected is a separate decision, deliberately not taken here.

### D4 — The reporting asymmetry stands

YAML reports all offending fields with line numbers; JSON reports the first
field with no position (§2.5). Normalising them would mean either parsing
`encoding/json`'s error text or hand-rolling a field walk, both of which cost
more than the inconsistency does. The ADR records the asymmetry so a reader is
not surprised by it.

## 4. Out of scope

- **The fail-open `AuthzSpec` default (Route B).** An empty spec meaning
  allow-all is deliberate design, documented on the type. It is the second half
  of §2.1's mechanism and this delivery does not change it: a hand-authored
  empty spec still fails open after this ships. Owner-decided to take its own
  ADR, so that a decoder change and an authorization-semantics change do not
  land in one commit with one blast radius.
- **Kind-appropriate field validation** (§2.6) — `model.Validate`'s concern.
- **A definition-upload transport** and the 400/500 mapping it would need (D3).
- **Strictness for anything other than process definitions** — cursors, event
  payloads and process variables are unrelated decode paths.

## 5. Testing

Black-box (`package model_test`), table-driven per the project's `table-test`
skill (assert closures, not `want`/`wantErr` fields; `t.Context()`).

**What makes these fail today.** T1–T7 are falsifiable against current `main`:
each returns `err=nil` today, executed in §2.1–2.2, so the RED is the observed
absence of an error.

⚠ **T8, T9 and T10 pass today. They are regression guards, not RED cases**, and
must never be counted among the falsifiable ones. Their falsifiability comes from
mutation duty below, not from today's behaviour. (An earlier revision of this
section claimed "there is no vacuous case in this set" and, twenty lines later,
that T10 was "the one row that does not fail today" — two false statements
contradicting each other and the table between them. Found by the rule-#9 audit,
which ran every row rather than reading them.)

| # | input | decoder | asserts |
|---|---|---|---|
| T1 | unknown key at top level | YAML | error mentioning the field |
| T2 | unknown key inside a node | YAML | error mentioning the field **and its line** |
| T3 | unknown key inside a flow | YAML | error |
| T4 | `eligable_roles` (the §2.1 typo) | YAML | error, **not** a silent empty `EligibleRoles` |
| T5 | multiple unknown keys | YAML | all of them reported (§2.4) |
| T6 | unknown key at top level | JSON | error |
| T7 | unknown key inside a node | JSON | error |
| T8 | valid definition, every supported field | both | parses clean — guards over-strictness |
| T9 | `timer_duration` on a `userTask` | both | still parses (§2.6 is pinned, not accidental) |
| T10 | empty, `"\n"`, comment-only | YAML | **no error** — D3a, guards the `io.EOF` regression |

T8, T9 and T10 are the ones that could rot into vacuity, so they assert
positively on the built `*ProcessDefinition`, not merely on `err == nil`.

⚠ T10 is the one row here that does **not** fail today — today's `ParseYAML`
already returns nil for empty input. It is a **regression guard** for D3a, not a
RED-first case, and the plan must label it as such rather than let it be counted
among the falsifiable ones. Its mutation is the inverse: return the raw `io.EOF`
and confirm T10 goes RED.

**Mutation duty.** T1–T7 must each be shown RED by reverting the decoder to the
lenient call, observed, then restored from a snapshot and `diff`ed. A mutation
that fails to compile is not a RED; a mutation both branches survive is not
verification.

## 6. Verification

1. `go test ./definition/... > /tmp/def.log 2>&1; echo "EXIT=$?"` — judge by exit
   code, never a pipeline.
2. `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
   — `definition/model` ≥ 85 %, no repo-wide regression.
3. `go vet ./...` — compiles every test file including Docker-gated ones; the
   cheap proof that no hidden consumer breaks.
4. `golangci-lint run ./...` clean.
5. The §2.7 definitions all still parse.
6. Delivery Gate: documents match what shipped, then `/code-review` and
   `/security-review` (owner-invoked), all findings fixed and folded via
   `--amend`.

## 7. Audit record (rule #9)

To be completed after the bundle's adversarial audit, before implementation.

## 8. Implementation record (2026-08-08)

Implemented on `feat/strict-definition-decoding` in one package (`definition/model`),
so no subagent fan-out. Every claim below was executed; the numbers are observed
output, not estimates.

### What shipped

| test | file | kind |
|---|---|---|
| `TestParseYAMLRejectsUnknownFields` | `strict_decoding_test.go` | RED-first (4 falsifiable cases + 1 guard) |
| `TestParseYAMLEmptyDocumentIsNotAnError` | same | regression guard (D3a) |
| `TestProcessDefinitionUnmarshalJSONRejectsUnknownFields` | same | RED-first (3 falsifiable + trailing-data + 1 guard) |
| `TestStrictDecodingDoesNotRejectKindInappropriateFields` | same | limitation guard (§2.6) |
| `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` | same | over-strictness guard (T8) |
| `TestPersistedDefinitionRoundTripsThroughStrictJSON` | same | data-migration guard (T9) |
| `TestREADMEYAMLBlocksParseUnderStrictDecoding` | same | **added beyond the plan** |
| `TestProcessDefinitionUnmarshalJSONEmptyInputIsNotEOF` | same | **added by adversarial review** (F2) |
| `TestNestedSubprocessDecodingIsStrict` | same | **added beyond the plan** — the recursive case |

Production change is 24 inserted lines across `yaml.go` (10) and `node_wire.go` (16).

### Four ways implementation corrected the design

1. **The audit's README enumeration was incomplete — 7 lines, not 4.** The plan
   named `README.md:144,864,865,868`. Re-deriving with
   `grep -nE '^\s*-?\s*[a-z]+[A-Z][a-zA-Z]*:' README.md` returned **seven** lines:
   the audit missed `deadlineDuration`, `deadlineFlow` and `deadlineAction`
   (869–871), plus the nested `maxAttempts`/`initialInterval`/`backoffCoef`
   inside `retryPolicy`. This is the ADR-0159 lesson repeating exactly: *the
   enumeration inherited from an upstream document had rotted; re-count, never
   inherit.* Had the plan been followed literally, the delivery would have
   shipped a README that still did not parse.

2. **A second, independent README defect, found by execution.** `README.md`
   listed `errorEndEvent` among valid `kind` values. It is **not a registered
   kind** — `kind: errorEndEvent` yields
   `workflow-definition: unknown node kind "errorEndEvent"`, on `main` too, so it
   is pre-existing and unrelated to strict decoding. All other 17 documented
   kinds were probed and parse. Fixed, with the real ADR-0127 form documented and
   executed: an `endEvent` carrying `end_behavior: error` (+ `error_code`) or
   `end_behavior: terminate` — both verified to parse and build, node kind
   `endEvent` in each case.

3. **T8 shipped self-maintaining rather than hand-built, and wider than specified.**
   The plan had the tag list derived by `awk` at authoring time and pasted into a
   fixture — which rots the moment a tag is added. Instead `declaredYAMLTags`
   derives the list **from the source at test time** and asserts each tag appears
   in `allFieldsYAML`, so a new `yaml:"…"` tag without a fixture entry fails the
   test. Scope also widened past `nodeYAML`/`definitionYAML` (43 keys) to the
   nested structs a definition authors inline — `RetryPolicy` (6),
   `ValidationDescriptor` (2), `SequenceFlow` (5) — because strictness makes
   those load-bearing too. The match is anchored (`^\s*-?\s*tag:`) so that
   checking `name` cannot be satisfied by `signal_name`.

4. **T9 was strengthened to cover the one genuine asymmetry.** A plain round trip
   is structurally symmetric — `MarshalJSON` and `UnmarshalJSON` share
   `definitionWire` — so it could pass without testing anything. The real hazard
   is `scoped_actions`, the **marshal-only** key. The test now registers a
   definition-scoped action, asserts the key is emitted, and requires the reload
   to succeed.

### ⚠ Stale claim in ADR-0167, corrected there

The ADR said 43 YAML keys exist, "only 22 appear anywhere in the repo's tests,
fixtures or examples; **21 never appear at all**." That described the pre-change
repo. `allFieldsYAML` now exercises all 43 (plus 13 nested), so the sentence is
false as shipped and is corrected in the ADR rather than left to rot.

### 5. Aligned with the repo's existing strict-decoding path

Found during the delivery's own doc sweep, not by any reviewer: this repo already
had a strict JSON decoder — `decodeCursorInto` in `runtime/kernel/cursorcodec.go`
(ADR-0160) — carrying the *same* `DisallowUnknownFields` + trailing-token shape.
It does one thing the first cut of `UnmarshalJSON` did not: when `dec.Token()`
returns a **non-EOF error** it keeps that error, because corrupt trailing bytes (a
`*json.SyntaxError` naming the offending character) and a legitimate second JSON
value (`err == nil`) are different debugging problems. The first cut collapsed
both into one bare string.

`UnmarshalJSON` now mirrors it, and the test asserts the cause survives with
`assert.ErrorAs(t, err, &syn)`. Two cases were added with it: a **second JSON
value** (the `err == nil` branch, previously uncovered) and **trailing
whitespace**, which is legal JSON framing and must keep parsing — `json.Marshal`
output routinely gains a newline in a file or column, and rejecting it would make
engine-written definitions unloadable. Trailing whitespace was verified to pass
both before and after.

This is the *compare each changed handler against its siblings* lesson: the defect
was not in the design, it was in not looking at the code that already solved the
same problem.

### ⚠ The over-strictness guard was itself defective, and self-probing found it

`declaredYAMLTags` first used `yaml:"([a-z_]+)` to pull tag names out of the
source. That character class silently **truncates** any tag it does not fully
match. Adding a field tagged `yaml:"schemaV2"` to `nodeYAML` produced the captured
name `schema` — which `allFieldsYAML` already contains under `validation:` — so
the guard **passed (EXIT=0) while leaving the real tag completely unguarded**. A
false negative in the one test whose entire job is to prevent false negatives.

Fixed to `yaml:"([^",]+)`, capturing the whole name up to the option comma.
Verified in both directions: the same `schemaV2` field now fails with
*declared yaml tag "schemaV2" is not exercised by allFieldsYAML*, and the clean
tree stays green.

Worth stating plainly because it generalises: **mutation 5 (delete a tag) passed
and proved nothing about this.** Deleting a tag and adding one are different
mutations, and only the *adding* direction exercises the derivation regex. When a
test derives its own expectations, mutate the DERIVATION, not just the subject.

### 6. What the adversarial stand-in reviews changed

Two Opus reviewers ran in separate `git worktree`s against a `main` baseline,
both briefed to EXECUTE. Every finding below was independently reproduced by the
controller before being acted on.

**Fixed in this delivery:**

| # | finding | action |
|---|---|---|
| B1 | **HIGH — YAML strictness stopped at the first document.** Everything after `---` was silently discarded, unknown fields included. A `---` overlay declaring `eligible_roles` parsed clean and built a task with none: a live instance of the bypass this ADR closes | `ParseYAML` now drains the decoder and rejects any later document carrying content; empty trailing separators stay legal |
| A-F3 | **`UnmarshalJSON` returned a bare `io.EOF` for empty input**, so `errors.Is(err, io.EOF)` flipped false → true and a stream caller would read a truncated definition as a clean end | translated to `workflow-definition: unexpected end of definition JSON`; parity with baseline re-verified |
| A-F1 | **The all-tags guard missed prefix-colliding tags.** `yaml:"([a-z_]+)` truncated, so `action2` (vs exercised `action`) and `idX` (vs `id`) were **never checked** — EXIT=0 | regex → `yaml:"([^",]+)`; all four reviewer cases now caught with correct names; `yaml:"-"` skipped so it cannot false-fail |
| A-F4 | **Two false claims in the shipped `CHANGELOG.md`** | corrected — see below |
| A-F5 | trailing-data error discarded its cause and used `fmt.Errorf` with no verb | already fixed independently; `errors.New` + `%w` |
| B4 | **A parse error could dwarf its own input** — ~2.4x (1.9 MB → 4.7 MB), logged in full | per-field messages capped at 20 plus a count |
| A-F7 | README fence regex missed ` ```yml ` and indented fences | widened; the "full definitions only" constraint documented |

**⚠ A-F4 is the one worth re-reading, because both false claims were mine and
both were written *while correcting someone else's* enumeration:**

1. "**seven camelCase keys**" — seven is the number of *lines*. The diff carries
   **10 occurrences of 9 distinct names**. I had counted lines correctly and then
   restated the number as a count of keys.
2. "`errorEndEvent` … **has never been registered**" — false, and an
   over-reaching *never*. It **was** registered until ADR-0127 retired it:
   `dcfe3f1 refactor(event)!: delete ErrorEndEvent kind, folded into EndEvent`,
   with `Name: "errorEndEvent"` still present at `dcfe3f1^`. This also sharpens
   the data-migration consequence: a blob persisted before `dcfe3f1` carries
   `"kind":"errorEndEvent"` and already fails to load on `main`.

So the delivery's headline lesson recurred *inside the fix for itself*: the
correction to a rotted enumeration introduced two new false claims. Verify the
recap sentence, not just the analysis it summarises.

**Recorded, not fixed** (each executed; all filed in `HANDOVER.md`): the silent
degradation of an undecodable stored definition (armed timers skipped forever);
the still-lenient `authz.AuthzSpec` decode in `humantask_store.go`;
`eligible_privileges` never evaluated by `RoleAuthorizer`; the wider fail-open
`AuthzSpec` residual (`[]`, bare, and `null` all parse clean and mean allow-all);
and 10.5x memory amplification on deeply nested subprocesses.

**Cleared by the reviews, with evidence:** no vacuous test in the file; the
`io.EOF` tolerance cannot swallow a real error (12 malformed inputs, `errors.Is`
false for every one); duplicate keys still rejected; anchors, aliases and `<<:`
merge keys between declared fields unaffected; no transport parses definitions,
and `ClassifyError`'s default arm emits no message, so strict-decoding text never
reaches a client; `Qualifier`/`NodeKind` are scalar wire forms as the ADR says;
the 43-tag count and the five JSON-only fields recomputed exact.

### 7. What the owner-invoked `/code-review` found — after both stand-ins

The standing note that stand-ins "still miss what the real gate catches" held
again: six findings, all reproduced by execution before being acted on, **all
fixed**.

1. **MEDIUM — `ParseYAML` silently lost its godoc.** Inserting
   `maxReportedFieldErrors`/`boundFieldErrors` *between* the doc comment and the
   function bound the comment to the const instead. `go doc ./definition/model
   ParseYAML` printed the signature and nothing else — the module-root public API,
   the actual product, undocumented. Helper moved above the doc block; `go doc`
   restored. **Neither stand-in looked at godoc output at all.**
2. **MEDIUM — the data migration has a real, already-existing trigger.** See the
   ADR: five camelCase tags retired by ADR-0144 (`8179c0b`). The CHANGELOG had
   named only `errorEndEvent`, understating the blast radius.
3. **LOW — the over-strictness guard had a second hole, and its comment claimed a
   check that could not fire.** A field with **no yaml tag** is invisible to the
   tag regex, yet yaml.v3 still exposes its lowercased Go name as an authorable
   key: adding `Priority int` to `nodeYAML` makes `priority: 5` parse cleanly
   while the guard stayed at EXIT=0. The comment asserting it would "fail loudly"
   was false. The helper now scans **fields**, not just tags.
4. **LOW — the JSON tests never exercised the store's actual path.** Every case
   called `UnmarshalJSON` directly; `DefinitionStore` calls `json.Unmarshal`.
   `TestDefinitionStorePathIsStrict` added.
5. **LOW — the error bound made the concrete type input-dependent.**
   `*yaml.TypeError` survived `errors.As` for ≤20 unknown fields and was
   destroyed above it, so a consumer enumerating `te.Errors` for a field-level
   400 got the full list for a small file and nothing for a large one. Now
   returns a truncated `*yaml.TypeError`, so the type is stable.
6. **LOW — the bound was not applied to the multi-document loop.** Fixed;
   `---\nnull` / `---\n~` documented as counting empty.

⚠ **Finding 3 is the sharpest lesson of the whole delivery.** The over-strictness
guard was defective **three times**, each hole found by a different reviewer,
each after the previous fix was declared complete:
character-class truncation (self-probing) → prefix collision (stand-in A) →
untagged fields entirely (`/code-review`). **A guard is not verified by mutating
its subject; it must be mutated in the dimension it derives from.**

⚠ And finding 2's report repeated the delivery's signature failure a third time:
a correct conclusion carrying a wrong enumeration ("*every* `NodeWire` json tag";
it was five). Re-derived before restating.

### Mutation table — 15 mutations, 15 caught, all compiled

| # | mutation | test | result |
|---|---|---|---|
| 1 | `KnownFields(true)` → `(false)` | T1 table | 4 RED, guard case green |
| 2 | delete `dec.DisallowUnknownFields()` | JSON table | 3 RED, valid + trailing green |
| 3 | trailing check `io.EOF` → `io.ErrClosedPipe` | JSON table | **only** trailing RED |
| 4 | `errors.Is(err, io.EOF)` → `io.ErrUnexpectedEOF` | empty-document | 3 RED, `parse YAML: EOF` |
| 5 | drop `yaml:"outcome_variable"` tag | T8 | RED: `field outcome_variable not found` |
| 6 | `MarshalJSON` appends `"extra_key":1` | T9 | RED: `json: unknown field "extra_key"` |
| 7 | README `eligible_roles` → `eligibleRoles` | README guard | block 2 RED, block 1 green |
| 8 | second-value branch `return errors.New(…)` → discard | JSON table | **only** the second-JSON-value case RED |
| 9 | drop the empty-input `io.EOF` translation | empty-input guard | both cases RED on `errors.Is(err, io.EOF)` |
| 10 | `KnownFields(false)` + drop `DisallowUnknownFields` | nested-subprocess guard | 3 RED, clean nested case green |
| 11 | ADD `yaml:"brand_new_field"` to `nodeYAML` | all-tags guard | RED — the self-maintaining property holds |
| 12 | ADD `yaml:"schemaV2"` to `nodeYAML` | all-tags guard | **passed before the regex fix (the defect above), RED after** |
| 13 | remove the multi-document drain loop | extra-documents guard | 2 RED, the three legal-framing cases green |
| 14 | `boundFieldErrors(err)` → `err` | error-size bound | RED |
| 15 | ADD `yaml:"-"` field | all-tags guard | **false failure before the skip, green after** |

Mutation 4 deliberately **swaps the sentinel instead of deleting the guard**: the
plan's original instruction to delete `&& !errors.Is(err, io.EOF)` leaves
`"errors" imported and not used`, a compile error whose exit code 1 is
indistinguishable from a RED. Mutations 3 and 6 were likewise shaped to keep the
build valid. Each was checked for `build failed` in the log before being counted,
and each restored from a snapshot and confirmed with `diff`.
