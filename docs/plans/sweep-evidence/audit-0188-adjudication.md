# ADR-0188 — rule-#9 audit adjudication

**Date:** 2026-08-25 · **Bundle:** `862294ef` (spec + ADR-0188 + plan)
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four detached
worktrees created **at the bundle commit**, step-0 presence check passed in all four.
**Reports:** `audit-0188-{execution,failure-modes,counting,interaction}.md`.

## ⛔ VERDICT: THE BUNDLE FAILS. Not an input to implementation.

**44 findings raw; 15 raw Criticals.**

**Reported the right way (per the meta-analysis): 11.0 findings per lens, 3.75 Criticals per lens.**

⚠ **11.0/lens is materially BELOW the historical 15.14 ± 0.83** — the first round in this corpus to
fall outside that band, and it fell *below*. Two readings are possible: the bundle is genuinely
smaller and tighter (3 test-only guards vs a multi-decision feature), or these lenses counted more
coarsely. **Do not treat one round as refuting the instrument model.** What matters is that
3.75 Criticals/lens is squarely in the normal range and the Criticals are severe.

## Cross-lens convergence — every one controller-verified

| # | finding | lenses | check |
|---|---|---|---|
| **C1** | **The mint site is unguarded, so ADR-0185 D3 still ships silently, fail-OPEN** | failure-modes F1, execution F11 | ✅ both independently implemented the *entire* D3 change minus `engine/step_nodes.go:724` and observed all guards GREEN |
| **C2** | **Task 3's guard cannot compile as prescribed** | execution F1, failure-modes F2, counting F10, interaction F2 — **four lenses** | ✅ `specFor` is unexported (`registry.go:58`); an internal-package workaround is an import cycle |
| **C3** | **The derived ownership list is wrong and oversized** — 23–27 of 44 entries, 6 of them provably wrong | execution F7/F8, counting F10, interaction F3, failure-modes F4 | accepted on four concurring executions |
| **C4** | **Guards pass while a value is dropped at `yaml.go`'s mapping** | counting F2, execution F4/F12 | ✅ structural: both guards compare field *sets*, never copies |

Four-lens convergence on C2 is the strongest signal this process has ever produced here.

## Accepted Criticals

### A. The delivery does not do the thing it exists to do (C1)

Two lenses, independently, implemented ADR-0185-core D3 exactly as its own spec designs it — the
field on `authz.AuthzSpec`, `activity.UserTask`, `model.NodeWire` + tag, `model.nodeYAML` + its
`yaml.go` mapping, **both** halves of `FromWire`/`ToWire`, and the correspondence row — omitting
only the `authz.AuthzSpec{…}` literal at `engine/step_nodes.go:724`. **`go build` clean, all three
guards `--- PASS`, package green.** Every minted task carries the new authorization field at its
**zero value** — the permissive value for a deny-oriented field.

⇒ **ADR-0188's "Unblocks: ADR-0185 D3" and "the omission class cannot recur silently" are both
FALSE**, at the one site where D3 changes behaviour, in the fail-open direction.

**Root cause: I guarded field NAMES where the defect is field COPIES.** §3.3 proves both fields
exist and are declared partners; it never proves anything copies one into the other.

**Accepted fix** (proposed by the failure-modes lens, and it is a real design improvement): extract
the literal into `eligibilityOf(ut)` and **value-guard it off the same correspondence table**,
converting site 4 from name-checked to value-checked. The same treatment is owed to site 1
(`yaml.go`'s mapping — C4).

### B. Task 3 cannot be built as written (C2)

`model.specFor` is unexported and `nodeRegistry` is private; the leaf package builds its
`FromWire`/`ToWire` closures inline in `init()` and stores them nowhere reachable.
`package activity_test` cannot reach them; an internal `package model` test importing `activity` is
an **import cycle** (compiled, observed). Building it needs a **new exported symbol**, which
falsifies the ADR's *"Zero production risk… the only non-test change is Decision 5's one-line list
entry."*

### C. The ownership list is largely wrong, and its errors are harness artifacts (C3)

Derived by execution as the plan instructs, it lands at **23–27 of 44** fields, of which at least
six are wrong:

- `ID`/`Kind`/`Name`/`Label` are misclassified "not carried" because the harness calls
  `spec.ToWire` directly, skipping `toWire`'s prologue that seeds them.
- `DeadlineTrigger`/`WaitTrigger` land on the list because the plan's filler rule sets
  `TriggerWire.Kind = "Kind"`, which `ReadTrigger` rejects. Correcting it to `"expr"` drops them
  (27 → 25).

⚠ **Worse than wrong: the trigger entries would permanently un-guard the backward-compatible wire
migration that ADR §2 uses as its load-bearing reason to reject restructuring.** The bundle would
have exempted the very thing it argues is precious.

### D. Backlog 141 turns `internal/atrest` red, and the generator reports success over it

`internal/atrest/render_test.go:227` **hardcodes** the count:
`assert.Contains(t, result, "durable at rest in **three** places")` plus a `NotContains` on
`"**two**"`. Plan Task 4 never touches it. Executed end-to-end: entry added → 2 failures →
`scripts/gen-at-rest.sh` prints *"regenerated and verified"*, **EXIT=0** → package still red.
**Plan Task 4 Step 3 asserts a green that cannot happen**, and the generator's success message is
itself untrustworthy.

### E. The guard would encourage a NET REGRESSION on backlog 143

Adding `BoundaryAction` to `nodeYAML` **without** the `fromNodeYAML` assignment leaves the whole
`definition/model` package green while `boundary_action: notify-overdue` parses clean and arrives as
`Action: ""`. **Today that same YAML fails loudly** (unknown field under `KnownFields(true)`). So
completing only the half the guard checks — which is exactly the fix the plan tells the implementer
to make — converts a loud failure into a silent one.

### F. The correspondence guard is not exhaustive, because I verified the hazard on the wrong types

`activity.UserTask` embeds `model.Base` **and** `model.ActivityFields` (→ `WaitFields`). The guard
iterates `NumField` in one half and calls `FieldByName` in the other; **`FieldByName` promotes,
`NumField` does not** (executed). A field added to the shared `ActivityFields` is invisible to the
exhaustiveness half.

⚠ **Spec §1.3 states the reflection probe "reported no anonymous/embedded fields in either struct."
That was `NodeWire` and `nodeYAML` — the two structs where embedding is ABSENT — and the guard was
then written over `UserTask`, where it is PRESENT.** A scope error of exactly the class this
delivery exists to eliminate, inside the fix for it.

## Accepted Majors — the two that change what I told the owner

### G. §2's rejection of embedding is a NON-SEQUITUR for eligibility (execution F14)

The premise is true and was verified: `ReadTrigger`'s legacy path **is** live, from both YAML and
stored JSON. **The inference is not.** The eligibility triple has **no legacy form**, and embedding
it on the **real 44-field `NodeWire`** builds clean with zero changes to `activity.go`/`event.go`,
emits byte-identical JSON, and keeps strict decoding and the golden round-trip green. It breaks
exactly two things: ADR-0187's pin and *this bundle's own Task-2 guard* — both because `NumField()`
does not traverse embeds, the same root as F.

⇒ **The "wire/domain decoupling forbids embedding" argument, as stated, does not carry.** It
generalises a real principle to a case that does not exhibit it. The consistency argument
(`ActivityFields`/`WaitFields` already use flat-wire + accessors) survives untouched and is now the
*only* surviving reason to prefer guards over embedding. **The owner was given the stronger version
of this argument and is owed the correction.**

### H. The bundle claimed a gap the repo had already filled — FIFTH instance

Spec §1.2: *"the existing guards are hand-written, one per field… That is the whole protection."*
**False.** `definition/model/strict_decoding_test.go:519`
`TestAllDeclaredYAMLTagsParseUnderStrictDecoding` **parses `yaml.go`'s source** to extract declared
tags across four files and requires each to be exercised — a derived, self-cleaning guard in
precisely the style this bundle claims to introduce. Its own comment anticipates the anchored-regexp
subtlety.

⇒ the right design **extends that guard**, rather than adding a parallel one. And "search the repo
for an existing convention before writing a new symbol" failed again, at instance five.

## Findings inside the bundle's own evidence

- **interaction F6 — I stripped my own hedge.** The withdrawn draft carried my warning that the wire
  parity was *"proven on a model of the structs, not the real ones… the 'evidenced against a
  stand-in' failure that has landed twice in this repo."* The withdrawal deleted the warning and
  kept the claim; ADR line 54 now reads *"technically feasible (executed: …)"* unhedged, in the
  first clause of the load-bearing decision. `grep stand-in|two-field|scratch module` across the
  bundle: **no match.** Recovered only from the branch reflog, because the bundle commit is
  `--amend`ed. ⚠ Premise Discipline's named failure, on my own hedge, in a bundle about
  machine-checking claims.
- **counting F7/F8 — inherited meta-analysis claims restated with hedges stripped.** *"every
  architectural finding but one"* widens the source's **8** (buckets A+B) to **19** (A+B+F) and
  converts lineage-membership into root-cause. *"25.4 % of all findings ever"* is 25.4 % of the
  **193 accepted**, not of the **554 total** — true share **8.8 %** — against a source that opens by
  warning about that exact substitution.
- **counting F4/F9** — *"12 matching lines"* in `nodeYAML` is **10** (12 is `NodeWire`'s); *"12×
  swing in artifact size"* is the source's *"12× **scope** cut."*

## What HELD

- ⭐ **G2 and G3 genuinely work at their stated job.** The interaction lens reproduced ADR-0185-core
  D3's real `nodeYAML` miss and both guards fired with actionable messages naming the exact field
  and the remedy. **The delivery's premise is sound; its execution is not.**
- ⭐ §1.3's **44 / 39 / five-field difference** is exact — the counting lens attacked the derivation
  method and could not break it. All **18** line anchors resolve.
- ⭐ **Backlog 143 and 144 are confirmed true** and correctly filed.
- ⭐ Backlog 141's `SECURITY.md` diff is correctly bounded (2+/1−), `"three"→"four"` is generated not
  retyped, and `englishNumber` handles 4.
- ⭐ G3's layering is clean; `Classification`'s own guards are undisturbed.

## Root causes, stated once

1. **I guarded NAMES where the defect is COPIES.** Sites 1 and 4 — the two that actually move data —
   are name-checked only. This is the whole of C1 and C4.
2. **I verified a hazard on the wrong types** (F), then generalised the result.
3. **I did not search hard enough for the existing guard** (H) — fifth instance of this class.
4. **A mid-authoring withdrawal silently promoted a hedged claim to an unhedged one** — and was
   invisible to review because the bundle commit is amended.
5. **A true principle was generalised to a case that does not exhibit it** (G).

## ▶ Next

The design is salvageable and the audit produced the fixes: value-guard sites 1 and 4 off the
correspondence table; extend `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` rather than paralleling
it; drop or rebuild the ownership list; update `render_test.go:227`; restore the stand-in hedge or
re-derive the claim on the real types; and correct §2's argument down to the consistency ground that
survives.

⚠ **But that is another revision of another bundle, and this lineage's history is now three failed
audits on ADR-0185 plus one here.** A scope decision belongs to the owner before any further
revision.
