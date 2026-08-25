# ADR-0188 rule-#9 audit — INTERACTION lens

Worktree: `.../wt88-interaction`, detached at `862294ef`.
Step 0: PASS — all three bundle files present (spec 15965 B, ADR 8985 B, plan 19527 B).

Scope: the pairwise consequences of the changed set (G1 value round-trip, G2 YAML coverage,
G3 eligibility correspondence, G4 backlog-141 at-rest entry) **plus the withdrawal grid**
(the embedded `model.Eligibility` struct and the ADR-0187 pin repoint, both withdrawn).

---

### F1 — G4's list entry makes `internal/atrest` RED at a test the plan never touches, and `gen-at-rest.sh` reports "verified" while it is red

**Severity: CRITICAL**

**The two things that interact:** **G4** (ADR-0188 Decision 5 / plan Task 4 — add
`wrkflw_instances.snapshot` to `atrest.PolicyAtRestLocations`, regenerate `SECURITY.md`, published
count 3→4) × **ADR-0187's `TestRender`** (`internal/atrest/render_test.go:218-230`, the subtest
"real repository data: structure, hazards, and the forbidden blanket claim", which **hardcodes**
`assert.Contains(t, result, "durable at rest in **three** places")` and
`assert.NotContains(t, result, "durable at rest in **two** places")`).

**What each assumes the other provides.** G4 assumes ADR-0187's at-rest guards are *derivation*
guards that follow `PolicyAtRestLocations` — the plan's Task 4 Step 3 says only *"Run
`go test -count=1 ./internal/atrest/...` — the drift and completeness guards **must be green**."*
ADR-0187's `TestRender` assumes the opposite of a derivation: it is a **literal fixture pin on the
English number**, deliberately absolute (its sibling census pin at the same site carries a comment
explaining that a derived comparison would be a tautology and so an absolute number was chosen).
Neither document mentions the other.

**Evidence (executed, in this worktree, `cp`-backed, restored, `git status` clean afterwards).**
Added the fourth entry exactly as Task 4 Step 1 prescribes:

```
go test -count=1 ./internal/atrest/...          EXIT=1
--- FAIL: TestRender/real_repository_data:...   render_test.go:227
--- FAIL: TestSecurityMdInSync
```

Then ran the prescribed remedy, `bash scripts/gen-at-rest.sh`:

```
EXIT=0
--- PASS: TestSecurityMdInSync      (regeneration pass)
--- PASS: TestSecurityMdInSync      (verification pass)
SECURITY.md at-rest block regenerated and verified.
```

and re-ran the package:

```
go test -count=1 ./internal/atrest/...          EXIT=1
--- FAIL: TestRender/real_repository_data:...
```

⇒ **after the plan's Task 4 Steps 1 and 2 are executed exactly as written, Step 3 fails**, and the
script the plan tells the implementer to trust prints *"regenerated and verified"* over a red
package, because `gen-at-rest.sh` scopes itself to `-run '^TestSecurityMdInSync$'` and never runs
`TestRender`. The `SECURITY.md` diff is otherwise exactly the bounded change the ADR claims (1
insertion + the count line; verified by `git diff`).

**Why it matters.** This is the delivery's only production change, and the plan's verification step
for it asserts a green that cannot happen. An implementer who trusts `gen-at-rest.sh`'s success line
ships a red package; one who does not, hits a failure the bundle gives no guidance on and may
"fix" it by reverting the entry or by weakening ADR-0187's pin. The bundle's own §3.4/Decision 5
text discusses only the *completeness* guard's `ClassPolicy` blindness — it identifies the guard
that **won't** fire and misses the guard that **will**.

**Concrete fix.** Add to plan Task 4, between Steps 2 and 3, an explicit step:

> **Step 2b:** `render_test.go:227/230` pins the English count as a literal
> (`"durable at rest in **three** places"` / NotContains `"**two**"`). Update it to
> `"**four**"` / NotContains `"**three**"`, and add
> `assert.Contains(t, result, "`+"`wrkflw_instances.snapshot`"+`")` alongside the two existing
> backticked table-qualified location assertions. ⚠ Do **not** replace the literal with
> `englishNumber(len(atrest.PolicyAtRestLocations))` — that is the tautology
> `TestRenderStructureIsDerivedNotRetyped`'s sibling census pin was deliberately written to avoid.

and amend Step 3 to `go test -count=1 ./internal/atrest/...` **must be green after 2b**, noting
explicitly that `scripts/gen-at-rest.sh` verifies only `TestSecurityMdInSync` and its success line
is **not** evidence the package is green. Mirror the correction in ADR-0188 Decision 5 and spec
§3.4 (the "stated exception" currently names only published `SECURITY.md` content; the pin edit is
a second consequence).


### F2 — G1 as prescribed cannot compile: its mechanism needs `definition/model` internals that the package placement it inherited from the WITHDRAWN draft denies it

**Severity: CRITICAL**

**The two things that interact:** **G1's mechanism** (plan Task 3 Step 2 — *"fill `w`;
`n := spec.FromWire(base, w)`; `var got NodeWire; spec.ToWire(n, &got)`"*) × **G1's package
placement** (plan File-Structure table and Task 3 header: `definition/activity/wire_roundtrip_test.go`,
**`package activity_test`**, external).

**What each assumes the other provides.** The mechanism assumes a `model.NodeSpec` for
`KindUserTask` is obtainable at test time. The placement — external `activity_test` — assumes the
guard needs nothing unexported from `definition/model`. Both cannot hold: `definition/model`
exports **`RegisterKind`** but **no retrieval symbol**. `specFor` (`definition/model/registry.go:58`),
`toWire` (`node_wire.go:88`) and `fromWire` (`node_wire.go:137`) are **all unexported**, and there
is no exported wrapper — the only exported route from a `NodeWire` to a `Node` is
`ProcessDefinition.UnmarshalJSON`.

**Evidence (executed).** Wrote the prescribed shape into `definition/activity/zz_probe_test.go`
(`package activity_test`) and ran `go vet ./definition/activity/`:

```
vet: definition/activity/zz_probe_test.go:12:13: undefined: model.SpecFor
```

and `grep -rn "func specFor\|func RegisterKind\|func SpecFor" definition/model/*.go` returns
`RegisterKind` (exported) and `specFor` (unexported) only.

The existing precedent the spec cites as the current protection,
`TestNodeWire_CompletionActionRoundTrip` (`definition/model/node_wire_test.go:11`, **`package model`**),
sidesteps this entirely: it calls `w.Activity()` / `back.PutActivity(...)` directly and **never
touches `KindUserTask`'s registered `FromWire`/`ToWire` at all**. So the repo has no precedent for
the mechanism G1 prescribes, in either package.

**Why this is an interaction and not a typo.** The external placement is a **residue of the
withdrawn design**. The withdrawn draft's §1.4 established the layering rule *"`definition/model`
does not import `authz`… the correspondence guard lives in an **external test package** that imports
both"* — a rule that was correct **for the correspondence guard** (G3, which needs `authz`). When
the embedding was withdrawn and the guards were re-homed into `definition/activity`, the external-test
constraint travelled with them and was applied to **G1**, which has the opposite requirement: it
needs `model` internals and no `authz` at all. Nothing in the current spec or ADR re-derives why G1
sits where it sits — §3.1 states the mechanism and the plan states the package, and the two were
never checked against each other. This is precisely the "a withdrawal generates its own grid" case.

⚠ The fallback route also has a trap: reaching the spec via `ProcessDefinition` JSON changes what is
being tested. `UnmarshalJSON` (`node_wire.go:184`) applies `DisallowUnknownFields` and then
`reconcileNodeValidationLenient(n, validate.DefaultRegistry())`, which **rewrites the node's
validation slot**, so a fully-filled `Validation` descriptor would not compare equal on the way back
out — through no fault of `FromWire`/`ToWire`.

**Concrete fix.** Decide the placement from the mechanism, and state the decision:

1. **Preferred:** move G1 to **`definition/model/wire_roundtrip_test.go`, `package model`** (internal —
   matching `node_wire_test.go`'s `head -1`), and have it call `specFor(KindUserTask)`. ⚠ This
   requires a stated dependency: `KindUserTask`'s spec is registered by `definition/activity`'s
   `init()`, and `definition/model` **does not import it**, so an internal `model` test would find
   `specFor(KindUserTask)` unregistered. The test must therefore blank-import
   `_ "github.com/kartaladev/wrkflw/definition/activity"` **from the `_test.go` file only** (legal:
   an internal test file may import a package that imports its own package, as long as the
   non-test package does not). Verify this compiles before adopting it — it is the load-bearing
   assumption of this option.
2. **Alternative:** export a narrow retrieval symbol, e.g.
   `func SpecFor(k NodeKind) (NodeSpec, bool)` in `definition/model/registry.go`, and keep G1 in
   `activity_test`. ⚠ This is a **public API addition**, which contradicts the ADR's Consequences
   ("no type changes, no public API change… the only non-test change is Decision 5's one-line list
   entry") — so if it is chosen, that sentence must be amended in the same bundle.

Either way, plan Task 3 must name the resolution explicitly, and the ADR/spec's "no public API
change" claim must be re-checked against it.


### F3 — G1 and G2 will exempt 23 of `NodeWire`'s 44 fields between them, overlap on two, and label two more contradictorily; neither list can see the other

**Severity: CRITICAL**

**The two things that interact:** **G1's `notOwnedByUserTask`** (spec §3.1 / plan Task 3 — the
self-cleaning list of `NodeWire` fields `KindUserTask`'s `FromWire`/`ToWire` does not carry) ×
**G2's `yamlUnauthorableWireFields`** (spec §3.2 / plan Task 2 — the self-cleaning list of `NodeWire`
fields `nodeYAML` does not declare).

**What each assumes the other provides.** Both are presented as small, reviewable declarations of
deliberate omission, modelled on `runtime/monitor`'s `knownOpenInternalLeaks`. G2's list is
**stated in the bundle** at five entries. G1's list is **not stated at all** — the plan says
*"Populated by EXECUTION… run the guard with this map empty and let the failures enumerate the
not-carried fields"* — and the bundle nowhere estimates its size. The spec's summary sentence
(*"the design has two self-cleaning classification lists … and one declared correspondence"*) treats
them as symmetric peers.

**Evidence (executed, `package model` internal throwaway probe, deleted; tree restored clean).**
Filled every exported `NodeWire` field with a distinct non-zero value, ran the `Activity()` /
`PutActivity()` bridge that `KindUserTask`'s spec delegates to plus the explicit copies in
`activity.go:240/:251`, and diffed:

```
PROBE not-carried (23): [Action AttachedTo BoundaryAction BoundaryErrorExpr CompensateRef
 CompensateScopeLocal CorrelationKey DeadlineDuration DeadlineTrigger DefRef EndBehavior ErrorCode
 MessageName MessageStartSingleton NonInterrupting SignalName Subprocess TerminationOutcome
 TerminationReason TimerDuration TimerTrigger WaitEvery WaitTrigger]
```

⇒ **`notOwnedByUserTask` will carry ~23 of 44 fields — over half of `NodeWire`, against G2's five.**
(Two or three of the 23 may be filler artefacts of pointer handling — `DeadlineTrigger`,
`WaitTrigger`, `Subprocess` — which the implementer must resolve; that resolution is itself part of
the cost this finding is about, and does not change the order of magnitude.)

Three concrete cross-list consequences follow:

1. **Overlap.** `DeadlineTrigger`, `TimerTrigger` and `WaitTrigger` appear in **both** lists. In G2
   they are declared *"canonical nested TriggerWire; YAML authors the legacy flat form"*. In G1 they
   are declared not-carried. Meanwhile the **other half of each pair** — `DeadlineDuration`,
   `TimerDuration`, `WaitEvery` — is in G1's list and **not** in G2's (those three *are* on
   `nodeYAML`). So the legacy flat-trigger path is split across the two lists such that **neither
   reader sees the whole pair**, and the actual behaviour — YAML authors the flat form, `Wait()`
   reads it (`node_wire.go:119`), `PutWait()` writes back only the nested form and drops the flat
   one — is stated by neither. ⚠ **This is the exact path §2 and ADR Decision 1 cite as the
   load-bearing proof that the wire/domain seam must not be collapsed.** The bundle's central
   argument rests on a path that both of its new guards declare as an exception.

2. **Contradictory labels for the same field.** `BoundaryAction` and `BoundaryErrorExpr` are in G2's
   list flagged *"⚠ NOT a deliberate limit — backlog 143"* (a known defect). They are **also** in
   G1's not-carried set, where the truthful reason is the unremarkable *"not a user-task field"*.
   Per the plan, Tasks 2 and 3 are written by **different agents in different phases** (Phase 2 and
   Phase 3). The repo then holds the same field declared "a known bug" in one guard and "fine" in
   the other, with no cross-reference — and the plan's Task 3 Step 3 instruction *"a field that
   appears here and should not is a finding — report it"* gives that agent no way to know which of
   the 23 are already adjudicated elsewhere.

3. **Review cost the bundle does not budget.** The plan asks one agent to derive and justify ~23
   entries in one sitting (Task 3 Step 3). A 23-entry hand-justified enumeration is itself a
   bucket-D artefact of the kind the spec's own §6 warns about — produced by the guard built to
   eliminate bucket D. The ADR's Consequences call this delivery "zero production risk" and frame
   the cost purely as "edit sites do not go down"; the real cost is 23 judgement calls.

**Why it matters.** A self-cleaning list is only a control if a human reads it. At 5 entries
`yamlUnauthorableWireFields` is reviewable; at 23 `notOwnedByUserTask` becomes a list people
regenerate rather than read — and it will then absorb a genuine regression as one more plausible
row. Worse, the two lists jointly exempt the one path the ADR's rejection argument depends on, so
the bundle's strongest claim is the least guarded thing in it.

**Concrete fix.** Three changes, all cheap:

1. **State G1's list size in the spec and ADR before implementation**, with the executed probe
   output above. A reader deciding whether guards-only is the right shape needs to know the exemption
   is ~52 % of the struct, not five fields.
2. **Give `notOwnedByUserTask` structured reasons instead of free prose**, so 23 rows stay
   reviewable and the overlap is visible: e.g. a small enum
   `reasonOtherKind` / `reasonLegacyFlatForm` / `reasonKnownGap` and a rule that any field also
   present in `yamlUnauthorableWireFields` must carry a pointer to it. Add a **cross-list assertion**
   in G1: for every field in both lists, require the G1 reason to name the G2 entry — that is the
   machine check that makes the pair visible without coupling the packages (both lists' key sets are
   plain strings; G1 can hold a literal copy of the shared names and assert against it, or the
   check can live in whichever package ends up holding both after F2's placement decision).
3. **Add the legacy flat-trigger asymmetry as an explicit declared fact somewhere it will be read** —
   at minimum a sentence in spec §3.1 and in `notOwnedByUserTask`'s doc comment: *"`DeadlineDuration`,
   `TimerDuration` and `WaitEvery` are read on the way in (`node_wire.go:119`) and never written on
   the way out (`PutWait`), so a YAML-authored flat trigger is re-serialized in nested form on the
   first persist round-trip."* ⚠ **Verify that sentence before writing it** — it is a behavioural
   claim and this lens derived it from the probe above plus reading `PutWait`; it has not been
   round-tripped end-to-end through a stored definition.


### F4 — the D3 walk fires FOUR guards in three packages, one of them a known-Critical the bundle mentions only to say "unchanged"; a fifth pre-existing guard the bundle never names

**Severity: MAJOR** (the delivery's stated purpose survives, but its handover to D3 is misleading)

**The two things that interact:** **ADR-0188's three guards + G4** × **ADR-0185 D3's prescribed
change** (backlog 53 — add `Open` to `authz.AuthzSpec` and `EligibleOpen` to the eligibility concept),
whose safety is the entire justification for this delivery ("Unblocks: **ADR-0185** D3").

**Evidence (executed end-to-end in this worktree; `cp`-backed, restored, tree verified clean).**
Applied D3's change exactly as ADR-0185 describes it and **deliberately reproduced its known miss**
(`Open` on `AuthzSpec`, `EligibleOpen` on `activity.UserTask` and on `model.NodeWire` carried through
`activity.go:240/:251`, **nothing added to `model.nodeYAML`**), wrote G2 and G3 as the plan
prescribes them verbatim, and ran `./definition/... ./internal/atrest/... ./authz/... ./engine/...`.

**Round 1 — three failures, in three packages:**

| guard | package | message |
|---|---|---|
| **G3** `TestEligibilityCorrespondsToAuthzSpec` | `definition/activity` | `UserTask.EligibleOpen is an eligibility field with no correspondence row` **and** `authz.AuthzSpec.Open must appear exactly once in correspondence` |
| **G2** `TestNodeWireFieldsAreYAMLAuthorableOrDeclared` | `definition/model` | `NodeWire.EligibleOpen has no nodeYAML counterpart: add it to nodeYAML AND its mapping in yaml.go, or declare here why YAML cannot author it` |
| **ADR-0187's pin** `TestDefinitionEligibilityFieldsAreTheDeclaredSet` | `internal/atrest` | expected 3 `Eligible*` entries, actual 4 (`EligibleOpen eligible_open,omitempty`) |

⇒ **G2 and G3 work.** Both fire, both messages are actionable, and G2 catches the precise
`nodeYAML` omission the delivery exists to catch. That part of the bundle's claim is **confirmed by
execution**, and should be recorded as such.

**Round 2 — satisfying all three surfaces a fourth guard the bundle never mentions.** Adding
`eligible_open` to `nodeYAML` (as G2's message instructs) and updating the two maps made
`definition/model` red again on **`TestAllDeclaredYAMLTagsParseUnderStrictDecoding`**
(`definition/model/strict_decoding_test.go:519`) — a pre-existing guard that derives every `yaml:`
tag from `nodeYAML` by regex over the source and requires the strict-decoding fixture to exercise
each one. It correctly catches the *half-done* remediation (field added, `yaml.go` mapping and
fixture not).

**Why it matters.**

1. **The bundle's premise §1.2 is falsified as a quantifier.** It says
   `TestNodeWire_CompletionActionRoundTrip` round-trips one field and *"**That is the whole
   protection.**"* It is not: `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` is existing,
   machine-checked protection over exactly the `nodeYAML` surface G2 targets — and it already
   enforces the *"every field carries a yaml tag"* invariant that spec §1.3 verified by hand as a
   one-off ("neither has a multi-name declaration"). The bundle does not cite it anywhere. Per the
   memory lesson *"four times ADR-0186 claimed a gap the repo had already filled"*, this is the same
   shape.
2. **ADR-0187's pin is a load-bearing part of D3's path and the bundle actively downplays it.**
   Spec §3.5 and the ADR's Neutral section mention `TestDefinitionEligibilityFieldsAreTheDeclaredSet`
   **only** to say it is *"UNCHANGED"*, *"keeps working as written"*, and that the withdrawn repoint
   is no longer needed. A D3 implementer reading ADR-0188 to learn what will fire concludes there is
   nothing to do in `internal/atrest`. In fact this pin is **C3 of the ADR-0185-core audit
   adjudication** — an *accepted, cross-lens-confirmed Critical* (counting F4 + interaction F7,
   `docs/plans/sweep-evidence/audit-0185core-adjudication.md`): *"adding `EligibleOpen` breaks
   ADR-0187's `TestDefinitionEligibilityFieldsAreTheDeclaredSet`"*. ADR-0188's "unchanged" framing is
   literally true and **operationally the opposite of what D3 needs to know.**
3. **Ordering is unhelpful but not harmful.** Nothing serialises the four; `go test ./...` surfaces
   them together. There is no misleading order — but there is a misleading *count*: the bundle
   implies D3 will meet the new guards, and D3 will actually meet **five** checks across **three**
   packages (G2, G3, ADR-0187's pin, the strict-decoding tag guard, and — after F2 is resolved —
   G1).

**Concrete fix.** Add to ADR-0188 a short, explicit **"what ADR-0185 D3 will have to satisfy"**
section (Consequences → Neutral/follow-ups is the right home), listing all five checks by test name,
package, and what each will say — sourced from the executed run above, not reasoned. In particular
rewrite spec §3.5 and the ADR's Neutral bullet so the pin is described as *"a fourth guard D3 must
update, in `internal/atrest`; it is unchanged by THIS delivery but it is not inert — it is
ADR-0185-core audit Critical C3"*, rather than as a decision that was withdrawn. Also correct spec
§1.2's *"That is the whole protection"* to name
`TestAllDeclaredYAMLTagsParseUnderStrictDecoding` as existing coverage, and re-derive what G2 adds
**over** it (answer, from the run: G2 catches the field that never reaches `nodeYAML` at all; the
existing guard catches only a field that reaches `nodeYAML` without its mapping/fixture — they
compose, and saying so strengthens rather than weakens the case for G2).

### F5 — plan Task 2 Step 4(b)'s prescribed mutation produces two failures, not the one it names

**Severity: MINOR**

**The two things that interact:** **plan Task 2 Step 4(b)** (*"add `BoundaryAction` to `nodeYAML` →
FAIL as a **stale exception**"*) × **`TestAllDeclaredYAMLTagsParseUnderStrictDecoding`**, the
pre-existing same-package guard from F4.

**Evidence (executed; `cp`-backed, restored).** Added
`BoundaryAction string `+"`yaml:\"boundary_action,omitempty\"`"+` to `nodeYAML` only, with G2 in place:

```
go test -count=1 ./definition/model/     EXIT=1
--- FAIL: TestNodeWireFieldsAreYAMLAuthorableOrDeclared
--- FAIL: TestAllDeclaredYAMLTagsParseUnderStrictDecoding
```

**Why it matters.** A mutation step is evidence only if the observed failure is the one predicted.
An agent following Step 4(b) sees a second unexplained red in the same package and must decide
whether the mutation was malformed. Per the repo's own lesson *"a mutation that fails to compile is
not a RED"*, an unpredicted co-failure deserves the same scepticism. It is genuinely benign here,
which is exactly why it should be written down rather than discovered.

**Concrete fix.** Amend Step 4(b) to: *"→ **two** failures, both expected:
`TestNodeWireFieldsAreYAMLAuthorableOrDeclared` as a stale exception, and
`TestAllDeclaredYAMLTagsParseUnderStrictDecoding` because the strict-decoding fixture does not
exercise the new `boundary_action` key. Assert on the FIRST by name."*


### F6 — the withdrawal stripped the hedge off the feasibility claim: "executed: byte-identical" was flagged by the author as stand-in evidence, and is now stated as plain fact in a committed ADR

**Severity: CRITICAL**

**The two things that interact:** the **withdrawn embedding decision** × the **evidence written to
support it**. This is the withdrawal grid's central case: a decision was removed, but a claim
authored *for* that decision survived into the current text — **and was promoted from hedged to
unhedged in the move.**

**What each assumed the other provided.** The withdrawn draft's §1.1 asserted embedding preserves
both wire formats byte-for-byte, and its own §5 audit-target list said, in the author's words:

> **§1.1's wire parity.** It was proven on a *model* of the structs, not the real ones. Prove it on
> the real `NodeWire`/`nodeYAML` with every node kind, not a **two-field stand-in**. ⚠ This is the
> "evidenced against a stand-in where the decision acts on the repo's own type" failure that has
> **landed twice in this repo**.

The draft therefore carried the claim *and* its disclaimer as a pair, with the audit tasked to close
the gap. The withdrawal deleted the audit target (there is no longer an embedding to audit) but
**kept the claim** — and the claim, restated, lost the qualifier.

**Evidence (executed).** Recovered the withdrawn draft from the branch reflog
(`git reflog design/authz-identity-core` → `93d120a5`, the pre-amend spec-only commit;
`git show 93d120a5:docs/specs/2026-08-24-eligibility-representation.md`) and diffed the survival of
the claim.

Current bundle, unhedged in **both** documents:

- `docs/specs/2026-08-24-eligibility-representation.md:106` — *"Embedding is technically sound:
  **executed**, JSON and YAML output are **byte-identical**, strict decoding works and still rejects
  unknown keys."*
- `docs/adr/0188-representations-reconciled-by-machine.md:55` — *"It is technically feasible
  (**executed**: JSON and YAML output stay byte-identical, strict decoding still works and still
  rejects unknown keys), and it is still wrong."*

And the qualifier is gone:

```
grep -rniE "stand-in|scratch module|two-field|model of the structs" <all three bundle files>
→ no match
```

(The single `throwaway` hit at spec:68 is the *unrelated* §1.3 field-count reflection probe.)

**Why it matters.** This is CLAUDE.md Premise Discipline's named failure mode, verbatim:
*"**Re-verify claims you inherit before restating them.** Restating strips the hedge; the sentence
stops looking contingent and nobody checks it again."* The claim is not decorative — it is the first
clause of the ADR's **load-bearing decision** ("It is technically feasible … and it is still wrong"),
and it is the sentence that makes the rejection look like a considered trade-off rather than an
avoidance. It will ship in a committed ADR, on `main`, as an executed measurement, and the next
person who wants to revisit embedding will read it as settled. The repo's own memory records this
exact class landing twice already, and the author had *identified it in this very document* one
draft earlier.

⚠ The same paragraph carries a second inherited claim in the same condition: *"strict decoding …
still rejects unknown keys."* Same probe, same stand-in, same missing hedge.

**Concrete fix — pick one, do not leave it as is:**

1. **Cheapest and sufficient:** downgrade the claim to what was actually observed. Replace both
   sentences with, e.g., *"Embedding is technically feasible — a throwaway two-field stand-in
   reproducing `NodeWire`'s and `nodeYAML`'s tag shapes emitted byte-identical JSON and YAML and
   still rejected unknown keys under strict decoding. `ASSUMPTION (unverified): the same holds for
   the real 44-field `NodeWire` across every node kind.` We reject the design regardless, on the two
   grounds below, so the gap is not load-bearing."* This is honest, keeps the rhetorical structure,
   and costs one edit.
2. **Or execute it for real** on `model.NodeWire`/`nodeYAML` across all node kinds and record the
   numbers — but this is work spent proving a design the bundle rejects, so option 1 is the better
   trade.

⚠ Whichever is chosen, **re-read §2 and ADR Decision 1 for other survivors of the withdrawal**, not
just this one. The two documents were re-authored around a decision reversal, and this lens found
this instance by diffing against the recovered draft rather than by reading the current text — which
is the only method that finds it.

### F7 — §2's rejection principle, taken as stated, condemns an existing embedded-into-persisted pattern the bundle does not mention

**Severity: MINOR**

**The two things that interact:** **§2 / ADR Decision 1's principle** (*"`NodeWire` is a persisted
contract… embedding a shared domain struct means a field added for domain reasons silently changes
the persisted format"*, plus *"it would leave two competing conventions"*) × **the rest of the
repo's serialized types**.

**Evidence (executed).** Wrote a throwaway `go/ast` walker over every non-test `.go` file in the
repo, reporting structs that have at least one `json:`/`yaml:`-tagged field **and** at least one
embedded (anonymous) field. Exactly one hit:

```
engine/trigger.go: ActionFailed  EMBEDS [baseTrigger]
```

`baseTrigger` (`engine/trigger.go:87`) is `struct{ at time.Time }` — a **shared** struct embedded
into ~15 trigger types (`grep -c baseTrigger` shows embeds at `:103 :127 :159 :295 :335 :360 :380`
and more), and triggers are persisted: `wrkflw_journal.trigger` is a classified column
(`internal/atrest/classification.go`, `ClassFreeform`).

**Why it matters, and why it is only MINOR.** Today `baseTrigger`'s sole field is **unexported**, so
it contributes nothing to the persisted JSON and the hazard is latent, not live. But that is exactly
the shape §2 rejects: **add one exported field to `baseTrigger` and ~15 persisted trigger encodings
change at once, with no guard.** Two consequences the bundle has not drawn:

1. If the principle is general, `baseTrigger` is a second, unguarded instance and deserves at least
   a line — the delivery is *about* representations reconciled by machine, and this is the same
   class one package over.
2. §2's second ground — *"it would leave two competing conventions"* — is weaker than presented.
   The repo already runs both: **node** fields use flat-wire + accessors
   (`ActivityFields`/`WaitFields`), while **trigger** fields embed a shared struct directly into the
   serialized type. The ADR's Positive consequence is correctly hedged (*"one convention for shared
   **node** fields"*), but §2's argument is not, and reads as a repo-wide claim it does not support.

**Concrete fix.** Two sentences, no scope change:

- In §2, narrow ground 2 to what is true: *"…for shared **node** fields, where `ActivityFields` and
  `WaitFields` already use flat-wire + accessors. (`engine`'s `baseTrigger` embeds into its
  serialized trigger types, so the repo is not uniform overall — the consistency argument is about
  node fields specifically.)"*
- In the ADR's Neutral/follow-ups, add: *"`engine.baseTrigger` is embedded into ~15 trigger types
  persisted into `wrkflw_journal.trigger`. Its only field is unexported today, so Decision 1's
  hazard is latent there rather than live; **filed as a backlog item**, not fixed here."*

### F8 — ADR-0188 cites §2.1 of a bundle that FAILED its audit, without noting that §2.1 is under an accepted correction order

**Severity: MINOR** (the specific cited fact is sound; the citation form is not)

**The two things that interact:** **G4's evidence citation** (spec §3.4 and ADR Decision 5:
*"`wrkflw_instances.snapshot` carries the full `AuthzSpec` via `InstanceState.Tasks[].Eligibility`
(executed, `2026-08-23-authz-identity-core.md` §2.1)"*) × **the ADR-0185-core audit adjudication**
(`docs/plans/sweep-evidence/audit-0185core-adjudication.md`), which returned **⛔ THE BUNDLE FAILS
ITS AUDIT** on 58 findings / 22 raw Criticals.

**Is it sound to build on that evidence?** For the specific fact — **yes, and this lens confirms
it.** §2.1's snapshot claim is an *executed* premise (throwaway `engine/zz_probe_test.go`, EXIT=0,
`--- PASS`, with a recorded method note about `<` being escaped to `<`), and none of the
adjudicated findings touch it. Backlog 141 itself is raised in §2.2 of the same document and was
adjudicated as *"pre-existing, unrelated to this bundle"*.

**But the citation is to a section carrying its own accepted Critical.** Adjudication section **C**
(*"D3's copy-priority model is backwards for WRITES"*, failure-modes F10, **Accepted**) reads:
*"Spec §2.1 calls `wrkflw_human_task.eligibility` the copy that matters and the snapshot a
projection 'read by instance rehydration'. For **reads** that is right. For **writes** it is
inverted"* — and its adjudicated fix begins **"correct §2.1"**. So ADR-0188 points a future reader
at a section that is (a) known-defective in an adjacent paragraph and (b) **not yet corrected** on
this branch.

To ADR-0188's credit, its §4 already states the write-back inversion independently (*"the instance
snapshot is written back **over** the task row — so a missed snapshot reverts a repair — stays open
against ADR-0185 D3"*), so the bundle is not *unaware*. The defect is purely in the citation's
form: a bare `§2.1` with no provenance marker.

**Why it matters.** CLAUDE.md General rule 13 requires a first-use gloss, and Premise Discipline
requires inherited claims to be re-verified before restating. A future reader chasing `§2.1` for
ADR-0188's evidence lands in a section under a standing correction order and has no way to tell
which sentence is the sound one.

**Concrete fix.** Change both citations to name the provenance and the scope:

> executed in `docs/specs/2026-08-23-authz-identity-core.md` §2.1 (the parked ADR-0185-core bundle —
> **that bundle failed its rule-#9 audit**; §2.1's *snapshot-carries-the-full-`AuthzSpec`* probe is
> not among the refuted claims, but §2.1's copy-**priority** model is, and is under
> adjudication-section-C's "correct §2.1" order)

and add a one-line note in the ADR's Relates-to block that ADR-0188 sits on the same branch as, and
does not depend on, the failed ADR-0185-core bundle.


### F9 — G2's exception list mirrors `knownOpenInternalLeaks`'s mechanism but inverts its meaning, then holds both meanings at once with nothing to tell them apart

**Severity: MAJOR**

**The two things that interact:** **G2's `yamlUnauthorableWireFields`** (spec §3.2, plan Task 2 —
declared as *"NodeWire fields **deliberately** not authorable in YAML, each with the reason"*) ×
**the convention it is declared to mirror**, `runtime/monitor`'s `knownOpenInternalLeaks`
(`runtime/monitor/internal_leak_test.go:93-104`), plus **backlog 143/144**, the two defects the
author found while deriving the list.

**What each assumes the other provides.** The bundle cites the monitor list three times as the
pattern being reused — spec §1.5 (*"§3.2 mirrors the second. This is the repo's own pattern, not a
new invention"*), ADR Decision 3, plan Global Constraints. But read the model's own doc comment:

> `knownOpenInternalLeaks` are **offenders this test reports but tolerates, each tracked as its own
> backlog item and owned by another package's delivery.**

It is a **defect register**, and every entry is by definition a known bug awaiting a fix. G2's list
is introduced as the opposite — a register of **deliberate design limits**. What actually lands is
**both, in one map, distinguished only by free prose**:

| entry | actual category | how a reader can tell |
|---|---|---|
| `DeadlineTrigger` | deliberate limit (evidenced, §2) | the prose |
| `TimerTrigger` | ⚠ reduced expressiveness — **backlog 144** | the prose says so, in a `⚠` |
| `WaitTrigger` | ⚠ reduced expressiveness — **backlog 144** | the prose says so, in a `⚠` |
| `BoundaryAction` | ⚠ **a defect — backlog 143** | the prose says *"NOT a deliberate limit"* |
| `BoundaryErrorExpr` | ⚠ **a defect — backlog 143** | the prose says *"NOT a deliberate limit"* |

**⇒ four of the five entries are not what the list says it holds**, and the only discriminator is a
`⚠` inside a string value that no test reads. The doc comment and the contents disagree on the first
day.

**Why it matters.**

1. **A green test now certifies a known-broken capability.** `event.WithBoundaryAction` and
   `event.WithBoundaryErrorExpr` are public options with wire support and a shipped example
   (`examples/scenarios/boundary_action/`) that **cannot be reached from YAML** — one of the two
   authoring forms CLAUDE.md names. After this delivery that fact lives inside a passing test, in a
   map whose name asserts it is deliberate. The bundle's own framing (*"this delivery declares and
   guards it, it does not fix it"*) is exactly the *"a residual you wrote down is still a defect you
   shipped"* pattern the repo recorded on ADR-0186, where `/code-review` **refused the
   documented-but-unmitigated distinction** and made both hazards MEDIUM findings.
2. **The list will attract more parking.** The next person who adds a `NodeWire` field and finds the
   `nodeYAML` mapping awkward has a sanctioned, green-passing place to put it, with an example of
   how to word a defect so it looks like a limit. That is the failure mode the exception list was
   built to prevent, one level up — and it composes with **F3**, where the sibling list
   `notOwnedByUserTask` will hold ~23 entries and the same two boundary fields with a *different*
   label.
3. It also interacts with **F1's severity ordering**: the delivery's own defect-registration work
   (backlog 143/144 **are** properly filed in `docs/plans/HANDOVER.md:366` and `:375` — verified,
   good hygiene) is undermined by burying the same facts in a map called
   `yamlUnauthorableWireFields`.

**Concrete fix.** Make the category machine-readable and let the guard enforce the difference:

```go
type yamlExceptionKind int

const (
    yamlDeliberateLimit yamlExceptionKind = iota // by design; no backlog item
    yamlKnownGap                                 // a DEFECT; must name a backlog item
)

type yamlException struct {
    kind    yamlExceptionKind
    backlog string // required and non-empty when kind == yamlKnownGap
    reason  string
}

// yamlWireFieldsNotOnNodeYAML holds BOTH deliberate limits and known gaps.
// ⚠ It is NOT a list of design decisions: 4 of its 5 entries are defects
// (backlog 143, 144). SELF-CLEANING in both directions.
var yamlWireFieldsNotOnNodeYAML = map[string]yamlException{ … }
```

and add one assertion to `TestNodeWireFieldsAreYAMLAuthorableOrDeclared`:
`for every entry where kind == yamlKnownGap, require.NotEmpty(t, e.backlog)`. Rename the map so it
stops asserting intent it does not have (`yamlUnauthorableWireFields` → `yamlWireFieldsNotOnNodeYAML`).
Then correct spec §1.5 and ADR Decision 3, which currently claim the monitor pattern is being
mirrored: say instead that the **mechanism** (self-cleaning map) is reused while the **semantics**
are broader, because this list holds deliberate limits *and* open defects — and that the `kind`
field is what keeps the two from being confused. ⚠ Also drop or requalify the spec §3.2 sentence
*"The five reasons above are placeholders pending derivation… `DeadlineTrigger`'s is evidenced; the
other four are not"* — the plan's Task 2 Step 2 has since derived all five and states them as fact;
the two documents disagree about whether the derivation has happened.


---

## Verified sound — no finding (recorded so the adjudicator does not re-derive them)

These were probed and came back clean. Negative results, each executed.

1. **G2 and G3 do what the bundle claims.** Applying ADR-0185 D3's change with its known
   `nodeYAML` miss reproduced, both guards fire with actionable messages naming the exact field and
   the exact remedy (full transcript in **F4**). The delivery's stated purpose is confirmed by
   execution, not just by reading.
2. **G3's package placement is clean.** `definition/activity` does not import `authz` today, `authz`
   does not import `definition/*`, and `authz`'s only in-repo import is `internal/expreval` — so
   `package activity_test` importing both creates no cycle and no layering violation. The guard
   compiled and ran. (Contrast **F2**, which is about G1, not G3.)
3. **G4 does not disturb the `Classification` map's own guards.** With the fourth
   `PolicyAtRestLocations` entry added, `TestClassificationCoversTheSchemaExactly`,
   `TestClassificationPerClassCounts` (the per-class census, E6),
   `TestNormalizedKeySetAgreesAcrossDialects` and `TestClaimedByIsTwoDifferentColumns` all stayed
   green — the locations slice and the classification map are properly decoupled. Only `TestRender`
   and `TestSecurityMdInSync` reacted (**F1**).
4. **`englishNumber` handles 4.** `render.go:202-211` maps 0–9; the count renders as "four", not
   "4". Verified in the regenerated `SECURITY.md`.
5. **The `SECURITY.md` regeneration is correctly bounded.** `git diff SECURITY.md` after
   `scripts/gen-at-rest.sh` = **2 insertions, 1 deletion**: the count line and one bullet. Nothing
   else in the generated block moved, so the ADR's "one deliberate exception" is accurate *as to
   `SECURITY.md`* (it is incomplete as to the test fixture — see **F1**).
6. **Plan Task 2 Step 2's executed claims are exactly right.** `grep -in "trigger"
   definition/model/yaml.go` → EXIT=1 (no match); `grep -in "boundary" …` → EXIT=1;
   `nodeYAML.AttachedTo` is at `yaml.go:63` and mapped at `:141`. All four as written.
7. **Backlog 143 and 144 are properly filed** in `docs/plans/HANDOVER.md:366` and `:375`, not only
   inside the guard's map. Good hygiene; **F9** is about the map's framing, not about registration.
8. **`ProcessDefinition.UnmarshalJSON` applies no structural validation**, so a hand-crafted
   fully-filled node would decode — relevant only as background to **F2**'s fallback route, whose
   real obstacle is `reconcileNodeValidationLenient`, not validation.

---

## Summary — INTERACTION lens

**9 findings: 4 CRITICAL, 2 MAJOR, 3 MINOR.**

| # | severity | one line |
|---|---|---|
| **F1** | CRITICAL | G4's list entry turns `internal/atrest` red at ADR-0187's hardcoded "three places" pin (`render_test.go:227`), which the plan never touches — and `gen-at-rest.sh` prints "regenerated and verified" over the red package |
| **F2** | CRITICAL | G1 as prescribed cannot compile: `spec.FromWire`/`ToWire` needs `model.specFor`, which is unexported, from `package activity_test` — a placement inherited from the withdrawn draft's layering rule |
| **F3** | CRITICAL | G1's exemption list will hold **23 of 44** fields (executed) against G2's five; they overlap on the nested triggers, split the legacy flat-trigger pair across both lists, and label `BoundaryAction` contradictorily |
| **F6** | CRITICAL | the withdrawal stripped the author's own "**two-field stand-in**, not the real structs" hedge off the "executed: byte-identical" feasibility claim, which now ships unhedged in the ADR's load-bearing decision |
| **F4** | MAJOR | the D3 walk fires **five** checks in three packages; ADR-0188 names ADR-0187's pin only as "unchanged" although it is accepted Critical **C3** of the ADR-0185 audit, and never names `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` at all — falsifying §1.2's "that is the whole protection" |
| **F9** | MAJOR | G2's list mirrors `knownOpenInternalLeaks`'s mechanism but inverts its meaning, then holds deliberate limits **and** two known defects (backlog 143) with only free prose to tell them apart |
| **F5** | MINOR | plan Task 2 Step 4(b)'s mutation produces two failures, not the one it predicts |
| **F7** | MINOR | §2's rejection principle condemns `engine.baseTrigger` (embedded into ~15 persisted trigger types); consequence undrawn, and §2's "two competing conventions" ground overstates repo uniformity |
| **F8** | MINOR | ADR-0188 cites `2026-08-23-authz-identity-core.md` §2.1 without noting that bundle FAILED its audit and §2.1 is under an accepted "correct §2.1" order (the specific fact cited is sound) |

**The single most important finding is F6.** F1, F2 and F3 are fixable with edits the bundle's own
structure anticipates — a missing plan step, a placement decision, a list that turns out larger than
assumed. F6 is different in kind: it is the failure the withdrawal grid exists to catch, it landed
**inside the ADR's load-bearing decision**, and it was invisible from the current text — this lens
found it only by recovering the withdrawn draft from the branch reflog (`93d120a5`) and diffing.
The author had identified this exact risk one draft earlier, in this very document, and the edit
that withdrew the design deleted the warning while promoting the warned-about claim to plain fact.
Per CLAUDE.md Premise Discipline: *"Restating strips the hedge; the sentence stops looking
contingent and nobody checks it again."*

**Worktree state:** all probes `cp`-backed and restored; `git status --porcelain` empty;
`go test -count=1 ./definition/model/ ./internal/atrest/` green at exit.
