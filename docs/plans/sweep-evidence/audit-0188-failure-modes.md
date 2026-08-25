# ADR-0188 rule-#9 audit — FAILURE-MODES lens

Worktree `wt88-failure-modes`, detached at `862294ef`. Step 0: all three bundle files present
(`docs/specs/2026-08-24-eligibility-representation.md`, `docs/adr/0188-representations-reconciled-by-machine.md`,
`docs/plans/2026-08-25-representations-reconciled.md`). Docker not used, not needed.

---

### F1 — EXECUTED: the exact ADR-0185 D3 change still ships silently. The three guards go green while `engine/step_nodes.go:724` drops the new field.

**Severity: CRITICAL**

**What the bundle says.** ADR-0188 Consequences/Positive, line 122-123: *"The omission class that
produced this lineage's architectural findings **fails a test** instead of shipping. It cannot recur
silently for eligibility, `ActivityFields` or `WaitFields`."* Spec §1.4 lists site 4 —
`engine/step_nodes.go:724`, `UserTask` → `authz.AuthzSpec` — as one of the four unchecked
conversions, and §3.3 is the only guard that touches it. The plan's own audit brief item 3
(plan:395-397) raises this as a question; this finding answers it with a measurement: **it is not a
theoretical gap, it is the primary scenario the ADR names.**

**Evidence (executed).** I wrote the §3.3 and §3.2 guards verbatim from the plan (plan:79-155 and
:202-271), confirmed both PASS on the unmutated tree, then simulated ADR-0185 D3's "add one boolean"
end-to-end — everywhere **except** `step_nodes.go:724`:

- `authz.AuthzSpec` += `StrictRef bool`
- `model.NodeWire` += `EligibleStrictRef bool` (`json:"eligible_strict_ref,omitempty"`)
- `model.nodeYAML` += the same + its mapping at `yaml.go:112-114`
- `activity.UserTask` += `EligibleStrictRef bool`, carried in **both** `FromWire` (`activity.go:240`)
  and `ToWire` (`:251`)
- the correspondence map += `"EligibleStrictRef": "StrictRef"`
- **`engine/step_nodes.go:722-726` left untouched** — still
  `authz.AuthzSpec{Roles: …, Privileges: …, Attribute: …}`

Results:

```
go build ./...                                                    BUILD_EXIT=0
go test -count=1 -run '^TestEligibilityCorrespondsToAuthzSpec$'    EXIT=0   --- PASS × 1
go test -count=1 -run '^TestNodeWireFieldsAreYAMLAuthorableOrDeclared$'  EXIT=0   --- PASS × 1
go test -count=1 ./engine/...                                      ok  github.com/kartaladev/wrkflw/engine  5.063s
```

The §3.1 round-trip guard would also pass by construction (both `FromWire` and `ToWire` were
updated). **All three of ADR-0188's guards are green while every user task minted by the engine
carries `StrictRef: false`.**

For the record, one *pre-existing* guard did fire —
`definition/model`'s `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` ("declared yaml tag
`eligible_strict_ref` is not exercised by `allFieldsYAML` — strictness makes it load-bearing"). That
is a YAML-fixture guard that predates this bundle; it says nothing about the engine site, and
`engine` stayed green.

**Why it matters.** The dropped field is an *authorization* field. In the D3 shape a false
`StrictRef` is the **permissive** value: a predicate referencing an absent variable is admitted
rather than denied. The delivery's headline claim — "cannot recur silently" — is therefore false for
the single change it was designed to protect, and it is false in the fail-open direction. Worse than
no guard: a reviewer of ADR-0185 D3 will see three green representation guards and conclude the
coordinated edit is complete.

Note the asymmetry the bundle has not noticed: sites 1–3 (`yaml.go`, `FromWire`, `ToWire`) each get
a *value* guard or a *field-set* guard; site 4 gets a field-set guard **whose two sides it does not
connect**. §3.3 proves `UserTask.EligibleX` and `AuthzSpec.X` both exist and are declared partners.
It never proves anything **copies one into the other**.

**Concrete proposed fix.** Add a fourth guard — a value round-trip over site 4 — in `engine`
(`package engine`, internal: the strategy is unexported). It is small because the copy is a pure
function of `ut`:

1. Extract the literal at `step_nodes.go:722-726` into an unexported
   `func eligibilityOf(ut activity.UserTask) authz.AuthzSpec` and call it from `userTaskStrategy.enter`.
   (This is a behaviour-preserving refactor, so it needs no new RED under the TDD rules — existing
   engine tests must stay green before and after.)
2. Guard it with the same shape as §3.1: for each row of a correspondence table, set the `UserTask`
   field to a distinct non-zero value via reflection, call `eligibilityOf`, and assert the paired
   `AuthzSpec` field holds that value. Drive the table off `eligibilityCorrespondence` so the two
   guards cannot disagree — which means that map must move out of `activity_test` into a place both
   packages can read (an unexported var in a shared internal test helper, or simply duplicate it and
   add a third guard asserting the two copies are equal).
3. Mutation-verify it: delete `Attribute: ut.EligibleExpr` from the literal → must go RED. The
   bundle currently prescribes **no** mutation at site 4 at all (plan Task 1 Steps 3-4 mutate only
   the *type declarations*, never a copy site) — that absence is itself the tell.

If the owner declines the fourth guard, ADR-0188's Consequences must be rewritten: replace "It
cannot recur silently" with an explicit statement that **site 4 is covered by name only**, and file
the gap as backlog with the measurement above. Silence is not an adjudication — the same distinction
`/code-review` refused twice (ADR-0186's two documented-but-unmitigated hazards).

---

### F2 — EXECUTED: Task 3's prescribed guard cannot be written where the plan puts it. `FromWire`/`ToWire` are unreachable from `package activity_test`, and every workable substitute changes what the guard proves.

**Severity: MAJOR**

**What the bundle says.** Plan File Structure (plan:64): *"`definition/activity/wire_roundtrip_test.go`
*(create, `package activity_test`)*"*, restated at plan:303. Plan:336 gives the mechanism:
*"The guard: fill `w`; `n := spec.FromWire(base, w)`; `var got NodeWire; spec.ToWire(n, &got)`; then
for every exported `NodeWire` field, either assert `got.F == w.F` …"*

**Evidence (executed).** There is no exported way to obtain a kind's `model.NodeSpec`, and no
exported `FromWire`/`ToWire`:

```
$ grep -rn "func .*NodeWire" definition/model/*.go | grep -v _test
definition/model/node_wire.go:88:  func toWire(n Node) NodeWire          <- unexported
definition/model/node_wire.go:137: func fromWire(w NodeWire) (Node, error) <- unexported
$ grep -n "func specFor" definition/model/registry.go
58:func specFor(k NodeKind) (NodeSpec, bool)                              <- unexported
```

`nodeRegistry` (`registry.go:39`) is unexported too. Writing the plan's own skeleton:

```
$ go vet ./definition/activity/
vet: definition/activity/wire_roundtrip_test.go:15:20: undefined: model.SpecFor
EXIT=1
```

`model.NodeSpec` the *type* is exported; the *values* are not reachable. The only round-trip route an
external package has is `json.Marshal` / `json.Unmarshal` on a whole `ProcessDefinition` — which is
exactly what every existing test in `definition/activity` does
(`activity_test.go:61,69,136,141,163,168,188,193,254,258`).

**Why it matters.** Neither escape hatch is free, and neither is costed in the bundle:

1. **Move the guard into `package model`** (internal, where `fromWire`/`toWire` live). Then it cannot
   name `activity.UserTask` — `definition/activity` imports `definition/model`, so the reverse import
   is a cycle. It also cannot trigger `activity`'s `init()`, so `KindUserTask` is **not registered**
   in that test binary and `fromWire` returns `ErrKindNotRegistered`. The guard would have nothing to
   drive.
2. **Go through JSON from `activity_test`.** This works, but it is a *different* guard:
   - it can no longer start from a filled `NodeWire` (there is no exported way to hand one in), so
     the direction inverts to fill-`UserTask` → marshal → unmarshal → compare-`UserTask`;
   - the round-trip now passes through `omitempty`, JSON tags and `NodeKind` name mapping, so a
     **duplicate or wrong `json:` tag** is silently in the loop — the guard's failures become
     ambiguous between "the conversion dropped it" and "the tag is wrong";
   - it must construct a **definition that validates and marshals**. `MarshalJSON` fails closed with
     `ErrUnserializableValidation` (`node_wire.go:83`), and the plan's filler explicitly fills
     pointer fields "one level" (plan:311) — including `Validation`. The plan never mentions building
     a valid definition;
   - the ownership list stops being "which of `NodeWire`'s 44 fields does `KindUserTask` carry" and
     becomes a statement about `UserTask`'s own fields, which is a **weaker** claim: it can no longer
     detect a `NodeWire` field that no kind reads at all.
3. **Export `SpecFor`** (or an internal test hook). This is a *public API change* on a library whose
   product is its module-root API — and ADR-0188 Consequences line 124 claims **"Zero production
   risk: no type changes, no wire changes, no API changes"**. Route 3 falsifies that sentence.

Whichever route is taken, an implementing subagent handed plan:336 will discover this only after
writing the filler, and the cheap escape is route 3 — silently contradicting the ADR's headline
claim.

**Concrete proposed fix.** Decide the route **in the bundle, before dispatch**, and state its cost:

- Preferred: keep the guard in **`package model`** and drive it off `NodeWire`'s
  `Activity()`/`PutActivity()`/`Wait()`/`PutWait()` pair (all exported methods on `NodeWire`, and
  `node_wire_test.go:11` already tests exactly this pair for one field). That covers sites 2 and 3's
  *shared-field* half — the `ActivityFields`/`WaitFields` shape the ADR says is the real problem
  (ADR:45-47) — with no import cycle, no JSON in the loop, and no API change. It does **not** cover
  the `Eligible*`/`Manual`/`Outcomes` assignments inside `activity.go`'s registered spec.
- Then, for that uncovered remainder, add a **second** small guard in `package activity_test` using
  the JSON route over a minimal valid one-user-task definition, and say in the ADR that it is
  JSON-mediated.
- Update ADR:140-141 and the plan's File Structure table to match, and update §4's "no API change"
  claim if route 3 is chosen instead.

---

### F3 — EXECUTED: the prescribed filler makes `DeadlineTrigger` and `WaitTrigger` mismatch on a correct round-trip. The natural fix seeds the ownership list with FOUR false entries and un-guards the exact mechanism §2 is built on.

**Severity: CRITICAL**

**What the bundle says.** Plan Task 3 Step 1 (plan:307-310): *"Fill every exported `NodeWire` field
with a **distinct non-zero** value: `string` → the field name; `bool` → `true`; `[]string` →
`{"<field>-1"}` …"*. Plan:337-338: *"for every exported `NodeWire` field, either assert
`got.F == w.F` **or** require an entry in `notOwnedByUserTask`"*. Spec §3.1 (:143-154) insists the
ownership list must be *derived*, and plan Step 3 (:340-343) says to populate it by running the guard
empty and *"let the failures enumerate the not-carried fields"*.

**Evidence (executed).** I ran exactly that: a reflective filler over all 44 exported `NodeWire`
fields, through the real registered `KindUserTask` `FromWire`/`ToWire` pair
(`definition/activity/zz_probe_test.go`, JSON-mediated per F2, deleted after). Output:

```
TOTAL exported fields: 44
KEPT (23): [CancelAction CompensateAction CompletionAction DeadlineAction DeadlineFlow
            EligibleExpr EligiblePrivileges EligibleRoles ExposeOutcome ID Kind Label
            Manual ManualImmediate Name OutcomeVariable Outcomes RecoveryFlow RetryPolicy
            Subprocess TimerTrigger Validation WaitAction]
LOST (21):
   …
   DeadlineDuration(want=DeadlineDuration-v got=)
   DeadlineTrigger(want=<nil> got=&{expr 0 <nil> DeadlineDuration-v  0 0 0 [] [] []})
   WaitEvery(want=WaitEvery-v got=)
   WaitTrigger(want=<nil> got=&{everyExpr 0 <nil> WaitEvery-v  0 0 0 [] [] []})
   TimerDuration(want=TimerDuration-v got=)
   …
```

(`RetryPolicy`, `Validation`, `Subprocess`, `TimerTrigger` register as "kept" only because my probe
left pointer fields nil; the plan's filler allocates them (plan:311), which moves `Subprocess` and
`TimerTrigger` into LOST and leaves the count around 23.)

The mechanism: `node_wire.go:117-127` `Wait()` reads **both** the nested trigger and the legacy flat
string — `DeadlineTimer: ReadTrigger(w.DeadlineTrigger, w.DeadlineDuration, false)` — while
`PutWait()` (`:129-135`) writes **only** the nested form,
`w.DeadlineTrigger = PutTrigger(a.DeadlineTimer)`. So filling `DeadlineDuration` non-zero causes
`Wait()` to synthesise an expression trigger, which `PutWait()` then writes into `DeadlineTrigger`.
`DeadlineTrigger` goes `nil → non-nil` and `DeadlineDuration` goes `non-zero → ""`.

**Why it matters.** Following the plan literally, an implementing agent sees `DeadlineTrigger` and
`WaitTrigger` in the failure list and — per Step 3, "justify each one against `activity.go`'s
`FromWire`/`ToWire`" — will write a plausible reason and move on. Those two fields **are**
round-tripped; declaring them "not carried" permanently disarms the guard over
`PutWait`/`Wait`/`PutTrigger`/`ReadTrigger`.

That is not an ordinary loss of coverage. ADR-0188 Decision 1 (ADR:60-66) and spec §2 (:115-125)
rest their entire rejection of restructuring on this exact mechanism: *"That backward-compatible wire
migration is only expressible **because** the wire and domain shapes are decoupled. Embedding would
delete the mechanism."* The bundle argues the mechanism is too valuable to give up, and then
prescribes a guard whose literal execution declares that mechanism out of scope. **The delivery would
ship having un-guarded its own load-bearing justification.**

It also silently falsifies plan Step 3's instruction *"A field that appears here and should not is a
finding — report it rather than papering over it with a plausible reason"*: four fields appear that
should not, and the plan gives the implementer no way to tell them from the ~19 legitimate ones.

**Concrete proposed fix.** Make the filler trigger-aware, and say so in the plan:

1. The filler must **not** fill the three legacy flat fields (`TimerDuration`, `DeadlineDuration`,
   `WaitEvery`) when it also fills the nested `*Trigger` fields — they are a *union*, not independent
   fields (`node_wire.go:36` already says *"legacy flat forms … not written by `ToWire`"*).
2. Those three then land on `notOwnedByUserTask` with the **correct, derivable** reason ("legacy
   flat read-side of a union; `ReadTrigger` decodes it, `PutWait` writes the canonical nested form"),
   and `DeadlineTrigger`/`WaitTrigger` stay under value assertion where they belong.
3. Add a **second, dedicated** assertion for the migration itself, since it is the mechanism §2
   defends: fill only `DeadlineDuration`, round-trip, assert the result carries the equivalent
   expression trigger. That test would fail today if `ReadTrigger`'s flat path were dropped — which
   is precisely spec §6 target 3's question ("is that path actually reachable?"), answered YES by the
   probe above.
4. Add to plan Step 3 an explicit named list of the fields the implementer should expect, so a
   surprise is distinguishable from the baseline. The measured baseline is in this finding.

---

### F4 — The ownership list is seeded with ~23 of 44 entries (over half the wire contract), most of them about kinds other than `UserTask`. That is the condition under which "add an exception entry" becomes the default reflex the guard was built to prevent.

**Severity: MAJOR**

**What the bundle says.** Spec §3.1 (:146-154) declares `notOwnedByUserTask` and asserts *"Adding a
field to `NodeWire` then forces a decision at this guard too, rather than defaulting to unchecked."*
ADR:94-95 makes the same argument for the YAML list: *"It converts silence into a required
decision."*

**Evidence (executed).** Same probe as F3: **21 of 44 fields do not survive**, rising to ~23 once the
plan's pointer-filling rule (plan:311) is applied. The not-carried set is dominated by fields that
have nothing to do with user tasks: `Action`, `AttachedTo`, `BoundaryAction`, `BoundaryErrorExpr`,
`CompensateRef`, `CompensateScopeLocal`, `CorrelationKey`, `DefRef`, `EndBehavior`, `ErrorCode`,
`MessageName`, `MessageStartSingleton`, `NonInterrupting`, `SignalName`, `TerminationOutcome`,
`TerminationReason`, `Subprocess`, `TimerTrigger`.

**Why it matters.** Three compounding effects the bundle does not cost:

1. **Signal-to-noise.** A developer adding a *gateway* or *event* field to `NodeWire` is now forced to
   add an entry to a list called `notOwnedByUserTask`, plus an entry to `yamlUnauthorableWireFields`
   if it is not YAML-authorable. Neither has anything to do with their change. The guard fires most
   often for reasons that are always benign, which is how a guard gets trained out of a team.
2. **The two workflows are indistinguishable.** The intended response is "add the field to the other
   representation"; the cheap response is "add a list entry with a plausible reason". Nothing in
   either guard distinguishes them — the reason string is never validated, never parsed, never
   cross-checked. `runtime/monitor`'s `knownOpenInternalLeaks`, which both guards cite as the
   precedent (spec:96-101, plan:51-52), is a list that is expected to **shrink to zero**; these two
   are expected to grow forever. That is a different pattern wearing the same shape.
3. **F3 shows the reflex already produces wrong entries on day one** — four of them, on the fields
   the ADR's own core argument depends on.

**Concrete proposed fix.**

- **Split the ownership list by cause.** Two maps, not one: `carriedByOtherKinds` (a field this kind
  legitimately ignores because another kind owns it) and `notCarriedByAnyKind` (a genuinely dead
  wire field — a real defect). The first is noise and can be asserted mechanically: a field in it
  must be written by **at least one** registered `ToWire`. That converts ~18 hand-written prose
  reasons into one machine check, and makes a truly orphaned wire field — which today nothing detects
  — fail a test.
- **Make the reason strings load-bearing** so a placeholder is visible: require every reason to be
  non-empty, to be at least N words, and — for the YAML list — to either cite a `file.go:line` or
  start with a declared backlog id. A one-line assertion; it does not stop a determined bypass, but
  it stops the *thoughtless* one, which is the actual failure mode.
- **State the day-one list sizes in the ADR** (~23 ownership entries, 5 YAML entries) under
  Consequences/costs. The ADR currently presents the lists as if they were small and exceptional.

---

### F5 — EXECUTED: §1.4's four-site enumeration is short. `humantask.Clone()` reaches INSIDE `AuthzSpec` field-by-field, so an ADR-0185 D3 slice field would be aliased everywhere — and the function's own doc comment claims the opposite.

**Severity: CRITICAL**

**What the bundle says.** Spec §1.4 (:84-92) is titled *"The conversion sites"* and tabulates
**four**: `yaml.go:112-114`, `activity.go:240` (`FromWire`), `activity.go:251` (`ToWire`),
`engine/step_nodes.go:724`. ADR:31-35 repeats it: *"They are bridged by four hand-written
conversions"*. The plan's own audit brief item 2 (plan:394) flags the risk: *"§1.4 names four. That
enumeration is exactly the kind that has rotted before."* It has.

**Evidence (executed).** `humantask/humantask.go:133-141`:

```go
func (t HumanTask) Clone() HumanTask {
	t.Candidates = authz.CloneActors(t.Candidates)
	t.Eligibility.Roles = slices.Clone(t.Eligibility.Roles)
	t.Eligibility.Privileges = slices.Clone(t.Eligibility.Privileges)
	…
```

This is a fifth hand-written, field-by-field traversal of `authz.AuthzSpec`. It is also the site with
the **most confident false comment in the blast radius** — `humantask.go:130-132`:

> *"This is the single deep-copy definition for a task; the engine's instance-state clone and the
> caching task store both delegate here rather than re-deriving it, **so a newly added mutable field
> is isolated everywhere at once**."*

That sentence is true of a field added to `HumanTask`. It is false of a field added to `AuthzSpec`,
which is what ADR-0185 D3 does. Probe: added `DenyRoles []string` to `authz.AuthzSpec`, cloned a task
and mutated the clone:

```
orig.DenyRoles=[MUTATED]      (aliased? true)
orig.Roles=[mgr]              (aliased? false)
```

and nothing in the repo notices:

```
go test -count=1 ./humantask/... ./engine/... ./runtime/task/... ./service/... ./authz/...
EXIT=0   ok humantask · ok engine · ok runtime/task · ok service · ok authz
```

(`authz.go` restored from `cp` backup; `git status --porcelain` clean apart from the two guard files
I authored for F1.)

**Why it matters.** `Clone()` is the isolation boundary between the engine's live `InstanceState` and
what a library consumer receives — `service/instance.go:74-83` `ActiveTasks()` cites it explicitly
("A consumer mutating a returned task must not reach the engine's audit record"), and the caching
task store delegates to it. A new `AuthzSpec` slice field means **a consumer of the public API can
mutate the engine's live authorization data by appending to a returned task's eligibility list.**
That is a security-relevant aliasing bug, in the delivery whose stated purpose is to make the
ADR-0185 D3 edit safe, and none of ADR-0188's three guards look at `humantask` at all.

Two further sites the enumeration also omits, for completeness:
- `runtime/processdriver_action.go:464` — `Eligibility: cmd.Eligibility` (whole-struct copy, so
  field-safe; listing it costs nothing and closes the enumeration);
- `internal/persistence/store/humantask_store.go:157` / `:398` — `json.Marshal`/`Unmarshal` of an
  **untagged** `AuthzSpec`, i.e. Go field names as keys. A new field is write-compatible, but rows
  written before the field existed decode to its zero value, which for a deny-shaped field is the
  permissive value. This is the upgrade-stranding shape ADR-0185's own audit history has hit before.

**Concrete proposed fix.**

1. **Correct §1.4 and ADR:31-35** to the real site list, and mark it as derived (`grep -rn
   "Eligibility" --include=*.go`), not remembered.
2. **Add a clone-isolation guard** — this is the cheapest of the four guards and covers the worst
   failure: reflectively fill every slice/map/pointer field of `AuthzSpec` (and of `HumanTask`),
   `Clone()`, mutate every mutable field of the clone, assert the original is unchanged. It fails
   today the moment `DenyRoles` is added, and it is self-extending: no list to maintain. Mutation-
   verify by deleting the `t.Eligibility.Privileges = slices.Clone(...)` line.
3. **Fix the false comment at `humantask.go:130-132`** in this bundle — Delivery Gate item 2 requires
   sweeping the diff for over-reaching quantifiers, and this one is pre-existing but directly
   contradicted by the delivery's own findings. Replace "a newly added mutable field is isolated
   everywhere at once" with "a newly added mutable field **on `HumanTask`** is isolated everywhere at
   once; a mutable field added to an embedded value type (`authz.AuthzSpec`, `authz.Actor`) must be
   added to the traversal here."
4. If the owner declines (2), the ADR must say site 5 exists and is unguarded, with this measurement.

---

### F6 — VERIFIED SAFE (negative finding): Task 4's regeneration produces exactly the two-line diff the bundle predicts.

**Severity: INFO — recorded so the claim is not merely asserted.**

**What the bundle says.** ADR:107-111 / spec §3.4: *"Add it; regenerate `SECURITY.md`, whose
published policy-at-rest count goes **3 → 4**."* ADR:138: *"Not purely behaviour-preserving: Decision
5 changes published `SECURITY.md` content."*

**Evidence (executed).** I performed Task 4 (added the `wrkflw_instances.snapshot` entry to
`internal/atrest/classification.go`'s `PolicyAtRestLocations`) and ran `scripts/gen-at-rest.sh`:

```
GEN_EXIT=0
--- PASS: TestSecurityMdInSync
SECURITY.md at-rest block regenerated and verified.

$ diff SECURITY.md.orig SECURITY.md
236c236
< …durable at rest in **three** places, not one…
> …durable at rest in **four** places, not one…
239a240
> - `wrkflw_instances.snapshot` is class `freeform` because it holds the whole serialized
>   instance state — but InstanceState.Tasks[].Eligibility puts each open task's full
>   authz.AuthzSpec INSIDE that JSON.
```

Nothing else moved. The spelled-out numeral ("three" → "four") is generated, not retyped, so the
count cannot drift from the slice. `gen-at-rest.sh` already asserts on the `--- PASS:` line rather
than the exit code, so a rename cannot make it silently no-op. The generation path is sound; the
plan's Step 2 verification is adequate as written. **Restored from `cp` backup afterwards.**

---

### F7 — The `freeform`-carrying-policy class gets a second hand-added member and STILL no machine check. `wrkflw_outbox.payload` is the next one, and it is one domain event away.

**Severity: MAJOR**

**What the bundle says.** ADR:113-116: *"ADR-0187's completeness guard **structurally cannot see the
omission** — it fails only for a `ClassPolicy` column and this one is `ClassFreeform` … **A
`freeform` column carrying policy is now a class with two members**; the bundle either covers the
class or states why not."* Plan Task 4 Step 4 (:185-188) defers the decision to the implementer.
ADR:150-151 leaves it as *"an explicit question for this bundle's audit."* This finding answers it.

**Evidence (executed).** The guard is confirmed structurally blind — `render.go:396-398`:

```go
for key, class := range cls {
	if class != ClassPolicy {
		continue
	}
```

There are **11** `ClassFreeform` columns (`grep -n ClassFreeform internal/atrest/classification.go`):
`wrkflw_instances.snapshot`, `wrkflw_journal.trigger`, `wrkflw_outbox.payload`,
`wrkflw_outbox.last_error`, `wrkflw_definitions.definition`, `wrkflw_call_links.output`,
`wrkflw_call_links.error`, `wrkflw_timers.trigger_payload`, `wrkflw_chain_links.start_vars`,
`wrkflw_human_task.note`, `wrkflw_human_task.vars`.

I checked each for policy content today. A repo-wide `grep -rn "Eligibility" --include=*.go` (non-test)
returns no eventing/outbox payload type, `wrkflw_journal.trigger` holds a `triggerEnvelope`
(`internal/persistence/store/trigger_codec.go:53`), and the rest hold variables/errors/outputs.
**So the ADR's "two members" is correct today.** That is the good news and it is the whole problem:

`wrkflw_outbox.payload` is `json.Marshal(ev.Payload)` over an arbitrary `kernel.OutboxEvent.Payload`
(`store_core.go:387`). The moment any domain event carries a `humantask.HumanTask` — a `TaskCreated`
event is the obvious one, and `HumanTask.Eligibility` is an `authz.AuthzSpec` — the published
"**four** places" sentence becomes a machine-generated, confidently-worded undercount, with no test
failing. The same is true of `wrkflw_instances.snapshot` itself: it became a policy location purely
because `InstanceState` grew a `Tasks []HumanTask` field, and nothing announced that.

This delivery's own thesis is that a hand-maintained enumeration rots. It then hand-adds a second
entry to a hand-maintained enumeration and calls the class closed.

**Concrete proposed fix.** Machine-check the class rather than the columns. The mechanism already
exists in this repo and is cheap:

1. Declare, next to `PolicyAtRestLocations`, a map from each `ClassFreeform` column to the **Go type
   that is marshalled into it** (`wrkflw_instances.snapshot` → `engine.InstanceState`,
   `wrkflw_definitions.definition` → `model.ProcessDefinition`, `wrkflw_outbox.payload` → `any`,
   …). Assert completeness against the classification map, so a new `freeform` column fails until
   classified — the same self-cleaning shape the rest of this bundle uses.
2. For every entry whose type is concrete, walk it reflectively (transitively, through slices, maps
   and pointers) looking for `authz.AuthzSpec`. A column whose type reaches `AuthzSpec` **must** have
   a `PolicyAtRestLocations` entry; one that does not **must not**. That is the `ClassPolicy` check
   `everyPolicyColumnIsLocated` already performs, extended to the class that actually bit.
3. `wrkflw_outbox.payload` is typed `any`, so reflection cannot decide it. Assert instead that no
   type registered as an outbox event payload reaches `AuthzSpec` — or, minimally, add a stated
   `ASSUMPTION (unverified)` line to the classification comment naming `payload` as the one member
   of the class that is decided by convention rather than by machine, so the next person adding a
   task-shaped event has a chance of seeing it.
4. If the owner declines all three, ADR:150-151's "explicit question" must be answered **in the ADR
   text** with this enumeration and this reasoning — not left as a question in a shipped document,
   and not answered only in a plan checkbox. `/code-review` has twice refused the
   documented-vs-mitigated distinction.

---

### F8 — EXECUTED: backlog 143 does not belong in an exception list, and fixing it in-bundle costs four lines plus a fixture key — while proving the guard end-to-end.

**Severity: MAJOR**

**What the bundle says.** Plan:220-232 declares the map as *"`NodeWire` fields **deliberately** absent
from `nodeYAML`, each with the reason it cannot be authored in YAML"*, then puts two entries in it
that say the opposite:

> `"BoundaryAction": "⚠ NOT a deliberate limit — backlog 143. YAML CAN author a boundary … but
> cannot set this, so event.WithBoundaryAction is unreachable from YAML"`

Plan:281-288 is explicit that the delivery *"declares and guards it, it does not fix it"*, and that
fixing it *"means adding two fields to `nodeYAML` plus their mapping — which is exactly the change
this delivery's guard exists to make safe."*

**Evidence (executed).** I made that change — two `nodeYAML` fields plus two lines in the
`yaml.go:112-114` mapping — and measured the fallout in `definition/model`:

```
$ go build ./... && go test -count=1 ./definition/model/
EXIT=1
--- FAIL: TestNodeWireFieldsAreYAMLAuthorableOrDeclared
      NodeWire.BoundaryAction IS authorable in YAML now — remove the stale exception
      NodeWire.BoundaryErrorExpr IS authorable in YAML now — remove the stale exception
--- FAIL: TestAllDeclaredYAMLTagsParseUnderStrictDecoding
      declared yaml tag "boundary_action" is not exercised by allFieldsYAML — strictness makes it load-bearing
      declared yaml tag "boundary_error_expr" is not exercised by allFieldsYAML — strictness makes it load-bearing
```

Two things fall out of this one run:

- **The §3.2 guard's stale-entry direction genuinely goes RED** — that is plan Task 2 Step 4(b),
  observed here, so the guard works as specified.
- **The total cost of closing backlog 143 is 4 production lines and two keys in the existing
  `allFieldsYAML` fixture**, which a *pre-existing* guard
  (`TestAllDeclaredYAMLTagsParseUnderStrictDecoding`) already demands and names. No new test, no
  migration, no API change. (Restored from `cp` backup; `git status --porcelain` clean.)

**Why it matters.** Three reasons the entry is the wrong shape:

1. **The list's name and doc comment are falsified by 2 of its 5 entries.** A variable called
   `yamlUnauthorableWireFields` documented as "deliberately absent" is 40 % populated with a known
   defect. The next reader who needs to know whether YAML *should* be able to author a field will
   read this map and get the wrong answer — which is the exact failure mode (a representation
   claiming something false about another representation) the delivery exists to end.
2. **Listing it satisfies the guard permanently.** Once `BoundaryAction` is in the map, no test will
   ever mention it again until someone independently decides to fix 143. The guard has been used to
   *silence* the finding it produced.
3. **It is the pattern `/code-review` has twice refused.** ADR-0186 documented two hazards instead
   of mitigating them and both became MEDIUM findings; the memory line is explicit that "a residual
   you wrote down is still a defect you shipped". This bundle knows that (it cites it at plan:188)
   and then does it anyway, one section later.

**Concrete proposed fix.** Fix backlog 143 in this bundle. It is 4 lines, the fixture key is already
demanded by an existing guard, and it converts the delivery's central claim from an assertion into a
demonstration: *"the guard found a real capability gap the same day it was written, and the fix it
made safe was landed under it."* That is a materially stronger delivery than one that ships the bug
in a list.

If the owner insists on deferring: **split the map in two** — `yamlUnauthorableByDesign` (3 entries,
the trigger union) and `yamlKnownAuthoringGaps` (2 entries, backlog 143) — assert that every entry in
the second names a filed backlog id, and state in the ADR that the second map is expected to shrink
to empty. Do not let a defect live in a list whose name asserts intent.

---

### F9 — The guards' failure messages tell a contributor what broke but not that a THIRD site exists, and the noisiest one names a package the contributor is probably not editing.

**Severity: MEDIUM**

**What the bundle says.** Plan:262-264: *"`NodeWire.%s` has no `nodeYAML` counterpart: add it to
`nodeYAML` AND its mapping in `yaml.go`, or declare here why YAML cannot author it"* — a good
message. Plan:147-148: *"`authz.AuthzSpec.%s` must appear exactly once in correspondence — a new spec
field with no authoring counterpart cannot be set by any definition"* — also good.

**Evidence.** Two problems, both visible in the messages as written:

1. **Nothing tells the contributor about `engine/step_nodes.go:724`.** Per F1 that is the site the
   guards do not cover, so it is the one the message most needs to name. A contributor who satisfies
   the correspondence guard has been told, in the guard's own words, that the spec field now "can be
   set by a definition" — and per F1 it cannot.
2. **`notOwnedByUserTask`'s failure is misleading in the common case.** Per F4 the list is ~23 of 44
   entries, and most future `NodeWire` fields belong to some other kind. A contributor adding a
   gateway field will see a `definition/activity` test fail, in a package they did not touch, telling
   them a *user task* does not carry their field. The correct action is "add a one-line entry saying
   this belongs to gateways", which is indistinguishable from the bypass F4 describes.

**Concrete proposed fix.**

- Extend the correspondence guard's message to name the copy site explicitly: *"…and copy it in
  `engine/step_nodes.go`'s `userTaskStrategy.enter` (the `authz.AuthzSpec` literal), which no guard
  checks"* — until F1's fourth guard exists, at which point the sentence changes to name that guard.
- Per F4, split `notOwnedByUserTask` so the common case ("another kind owns this") is machine-decided
  and never produces a message at all, leaving the list for the rare, genuinely interesting case.
- Add to each guard's doc comment a one-line "the full set of sites is listed in
  `docs/specs/2026-08-24-eligibility-representation.md` §1.4" pointer, and correct §1.4 per F5.

---

### F10 — Plan ordering: Task 2's Step 2 can turn a design-time assumption into a shipped `SECURITY.md`-adjacent claim, and Task 3's derivation is the only place the ADR's costs are discovered.

**Severity: MEDIUM**

**What the bundle says.** Plan Phase 1 runs Tasks 1 and 4 in parallel, Phase 2 Task 2, Phase 3 Task 3
(plan:69, :193, :299). Spec:188-190 marks four of the five YAML reasons `ASSUMPTION (unverified)`.
Plan:363-366 says *"Expect the derivations in Tasks 2 and 3 to correct the spec — the four
`TODO-DERIVE` reasons and the ownership list are unknowns by construction."*

**Evidence / analysis.** The package-level fan-out is correct — Tasks 1 and 3 are both
`definition/activity` and are correctly serialised (plan:53, :305), Task 2 is `definition/model`,
Task 4 is `internal/atrest`. I found no intermediate state where the repo fails to build: every task
adds a new file or one slice entry, and Task 4's `SECURITY.md` regeneration is self-verifying (F6).
**The ordering itself is sound.** Two sequencing hazards remain:

1. **Task 2 is where the delivery's only genuinely new *findings* are produced** (backlog 143 per
   plan:281-288, backlog 144 per plan:224/226), yet it runs after Task 4 has already regenerated and
   committed the published `SECURITY.md`. If Task 2's derivation turns up a *further* authoring gap
   with at-rest consequences, Task 4's work is already done and the temptation is to leave it. Run
   Task 2 first, or make Phase 4's "Documents describe what shipped" step explicitly re-run
   `scripts/gen-at-rest.sh` after all derivations land.
2. **Task 3's Step 3 derivation is the single point where F3's four false entries and F4's ~23-entry
   list size become visible**, and it is the last task before the gate. An agent that reaches it with
   budget spent will do the cheap thing. Move the *measurement* forward: this audit has already run
   it (F3/F4), so put the expected baseline into the plan and make Step 3 a **comparison** against a
   stated list rather than an open-ended derivation.

**Concrete proposed fix.** Reorder to Task 2 → Tasks 1+4 → Task 3, and paste F3/F4's measured
baselines into plan Steps 2 and 3 so an implementing agent is checking a prediction rather than
discovering one under time pressure. Add a Phase 4 checkbox: *"re-run `scripts/gen-at-rest.sh` and
confirm a clean tree, after all derivations have landed."*

---

### F11 — The value round-trip covers 1 of 17 registered kinds, and the ADR's stated cost of generalising ("a declared owned-field table per kind") understates it: `KindEndEvent` needs a table per BEHAVIOUR, not per kind.

**Severity: MEDIUM**

**What the bundle says.** ADR:140-141: *"The value round-trip is scoped to `KindUserTask`;
generalizing per kind needs a declared owned-field table per kind and is deliberately **not**
attempted here."* Spec §4 (:235-237) repeats it and flags it for the audit. ADR:122-123 nonetheless
claims the omission class *"cannot recur silently for eligibility, `ActivityFields` or
`WaitFields`."*

**Evidence (executed).**

```
$ grep -c "model.RegisterKind(model.Kind" definition/{activity/activity.go,event/event.go,gateway/gateway.go}
definition/activity/activity.go:7
definition/event/event.go:6
definition/gateway/gateway.go:4
```

**17 registered kinds, 17 `FromWire`/`ToWire` pairs.** The guard covers one.

The `ActivityFields`/`WaitFields` half of ADR:122-123's claim does hold transitively — `UserTask`
embeds `ActivityFields`, so a drop inside `PutActivity`/`Activity`/`PutWait`/`Wait`
(`node_wire.go:101-135`) is caught by the `KindUserTask` round-trip, and those are the shared
accessors every kind uses. **That part of the claim is true**, and worth stating explicitly in the
ADR rather than leaving the reader to derive it.

The *per-kind* half does not. Two concrete gaps found while checking:

1. **`KindStartEvent` and `KindIntermediateCatchEvent` carry the same legacy-flat trigger union F3
   describes** — `Timer: model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false)` on the read side,
   `w.TimerTrigger = model.PutTrigger(v.Timer)` on the write side (`event.go:285`, `:296`, `:343`,
   `:352`). `TimerDuration` is never written back. So the mechanism spec §2 calls load-bearing lives
   in **three** kinds, and the guard will (per F3) end up declaring it out of scope in the one kind
   it covers.
2. **`KindEndEvent`'s `ToWire` is a `switch`, not a struct literal** (`event.go:307-338`): it writes
   `TerminationReason`/`TerminationOutcome` only when the behaviour is terminate, and `ErrorCode`
   only when it is error. A generic reflective round-trip over `KindEndEvent` cannot be driven by
   "a declared owned-field table per kind" at all — it needs a table per *(kind, behaviour)* pair,
   and a filler that produces a valid behaviour discriminator. The ADR's one-line cost estimate for
   generalising is therefore optimistic, and an implementer who later tries it will find that out
   the hard way.

**Why it matters.** Not because the scope is wrong — scoping to `KindUserTask` is defensible for a
delivery aimed at ADR-0185 D3. It matters because ADR:122-123's "cannot recur silently" is stated
without the qualifier, and because the ADR's stated *cost of the follow-up* is what a future session
will budget against.

**Concrete proposed fix.**

1. Rewrite ADR:122-123 to say exactly what is covered: *"…cannot recur silently for the shared
   `ActivityFields`/`WaitFields` accessors (every kind uses them, and the `KindUserTask` round-trip
   exercises them), nor for `KindUserTask`'s own fields. **The other 16 kinds' own fields remain
   unguarded**, as does `engine/step_nodes.go`'s `AuthzSpec` copy"* — the last clause pending F1.
2. Correct ADR:140-141's cost estimate to name the `KindEndEvent` behaviour-switch case, so the
   follow-up is budgeted honestly.
3. Record the "17 registered kinds, 1 covered" ratio in the spec §4 scope statement with the grep
   that derived it, so the next enumeration does not have to be re-derived from memory.

---

## Summary — FAILURE-MODES lens

| # | claim | severity |
|---|---|---|
| F1 | the exact ADR-0185 D3 change still ships silently: all three guards green while `engine/step_nodes.go:724` drops the field | **CRITICAL** |
| F3 | the prescribed filler makes `DeadlineTrigger`/`WaitTrigger` mismatch; the natural fix seeds 4 false ownership entries and un-guards the mechanism §2 is built on | **CRITICAL** |
| F5 | §1.4's four-site enumeration is short — `humantask.Clone()` traverses `AuthzSpec` by hand, so a D3 slice field aliases into the engine's live state; the function's doc comment claims the opposite | **CRITICAL** |
| F2 | Task 3's guard cannot be written where the plan puts it; every substitute changes what it proves, and the cheap one falsifies the ADR's "no API changes" | MAJOR |
| F4 | ~23 of 44 ownership entries on day one — the condition under which "add an exception" becomes the reflex | MAJOR |
| F7 | the `freeform`-carrying-policy class gets a second hand-added member and still no machine check; `wrkflw_outbox.payload` is one domain event away | MAJOR |
| F8 | backlog 143 does not belong in an exception list — fixing it costs 4 lines and proves the guard end-to-end | MAJOR |
| F9 | guard messages never name the uncovered site, and the noisiest guard fires in a package the contributor did not touch | MEDIUM |
| F10 | ordering: Task 2 produces the findings but runs after Task 4 publishes `SECURITY.md`; Task 3's derivation is last and under budget pressure | MEDIUM |
| F11 | 1 of 17 kinds covered; the ADR's cost estimate for generalising misses `KindEndEvent`'s behaviour switch | MEDIUM |
| F6 | Task 4's regeneration verified safe — exactly the predicted two-line diff | INFO |

**Counts: 3 Critical · 4 Major · 3 Medium · 1 Info (verified-safe).**

**Single most important finding: F1.** The delivery's headline claim — the omission class "cannot
recur silently" — is false for the one change it was designed to protect, and false in the fail-open
direction. Executed end-to-end: field added to `AuthzSpec`, `NodeWire`, `nodeYAML` + mapping,
`UserTask`, both halves of the conversion pair and the correspondence map, but not to
`engine/step_nodes.go:724`; `go build` EXIT=0, both guards `--- PASS`, `ok github.com/kartaladev/wrkflw/engine`.
Every user task minted by the engine carries the new authorization field at its zero value.

**Worktree state:** all mutations restored from `cp` backups; `git status --porcelain` empty,
`go build ./...` clean. No file outside this findings document was left modified.
