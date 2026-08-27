# ADR-0190 revision 2 — round-2 adversarial audit, RE-COUNTING lens

Worktree `wt-count2`, detached at `a161f347` ("docs(authz,transport): ADR-0190 disclosure
posture — design bundle, revision 2"). Step 0 passed: spec, ADR and plan all present.

Findings are appended as they are confirmed. Severity: CRITICAL / MAJOR / MINOR / CLEAN.

---

## CLEAN-1 — the classification table is TOTAL and EXACT over all four structs

**Attacked:** the plan's "The classification — derived mechanically, and it IS the design"
section: `engine.InstanceState` 31 exported = 11 public + 20 withheld; `engine.Token` 13 = 7+6;
`humantask.HumanTask` 11 = 6+5; `engine.NodeVisit` 6 all public.

**Command** (throwaway `runtime/view/zz_probe_test.go`, reflection over the real types,
deleted after):

```
go test -count=1 -run '^Test(SetEquality|PublicFieldTypes)$' -v ./runtime/view/
InstanceState  real=31 claimed=31(pub 11 + wh 20) UNCLASSIFIED=[] NOTAFIELD=[] DUP=[]
Token          real=13 claimed=13(pub  7 + wh  6) UNCLASSIFIED=[] NOTAFIELD=[] DUP=[]
HumanTask      real=11 claimed=11(pub  6 + wh  5) UNCLASSIFIED=[] NOTAFIELD=[] DUP=[]
NodeVisit      real= 6 claimed= 6(pub  6 + wh  0) UNCLASSIFIED=[] NOTAFIELD=[] DUP=[]
```

**Actual member sets, pasted in declaration order** (`reflect`, exported only):

- `engine.InstanceState` (31): InstanceID, DefID, DefVersion, Status, Variables,
  StartVariables, Tokens, StartedAt, EndedAt, History, Tasks, Timers, ArmedEvents, Boundaries,
  Scopes, RootCompensations, ArchivedCompensations, EventTriggeredSubprocesses, Compensating,
  Incidents, PendingCancel, PendingFinalStatus, PendingFinalErr, DeferredCompensationThrows,
  RecentCompensationCmdIDs, CmdSeq, TokenSeq, TaskSeq, TimerSeq, ScopeSeq, IncidentSeq
- `engine.Token` (13): ID, NodeID, ScopeID, State, AwaitCommand, AwaitSignal, AwaitMessage,
  AwaitMessageKey, AwaitTimer, Payload, EnteredAt, RetryAttempts, RetryStartedAt
- `humantask.HumanTask` (11): TaskID, InstanceID, NodeID, Eligibility, Candidates, State,
  Claim, Completion, CreatedAt, DueAt, Vars
- `engine.NodeVisit` (6): NodeID, TokenID, EnteredAt, LeftAt, TaskID, CloseKind

No field is missing from the table, none is misspelled (a misspelling would have shown as
both an UNCLASSIFIED real field and a NOTAFIELD claim), none is duplicated across columns, and
every column count is right. **This is the one thing this lens most expected to break, and it
holds.** The plan's claim that it was "enumerated by sed/grep over the struct, not from
memory" is credible.

## CLEAN-2 — no PUBLIC field's TYPE smuggles sensitive members

**Attacked:** mandate 2 — "if a public field's TYPE has sensitive members, the guard never
looks at it."

Every field classified public resolves (after peeling ptr/slice/map) to `string`, `int`,
`bool`, `time.Time`, or one of the three structs that ARE in the table:

- `InstanceState.Status` / `PendingFinalStatus` -> `engine.Status`, kind=int (scalar enum)
- `Token.State` -> `engine.TokenState`, kind=int (scalar enum)
- `HumanTask.State` -> `humantask.TaskState`, kind=int (scalar enum)
- `NodeVisit.CloseKind` -> `engine.CloseKind`, kind=string (scalar enum)
- `HumanTask.DueAt`, `InstanceState.EndedAt`, `NodeVisit.LeftAt` -> `*time.Time`
- `Tokens`/`Tasks`/`History` -> `engine.Token` / `humantask.HumanTask` / `engine.NodeVisit`,
  all three projected element-wise by their own table.

The four structs ARE the right closure over the public frontier. `authz.AuthzSpec` (3 fields),
`authz.Actor` (3), `humantask.Claim` (2), `humantask.Completion` (4), `engine.Incident` (9),
`engine.Scope` (4), `engine.CompensationRecord` (4) are all reachable only through **withheld**
fields, so they are absent by construction and correctly outside the guard.

---

## F1 — **CRITICAL** — `DiscloseAll` cannot restore the pre-ADR-0190 wire shape: **20 of the 31 withheld fields are restorable by NO category**, including `Incidents` — the exact field the ADR's own Residuals section says `DiscloseAll` restores

**Claims attacked (three, in two documents, mutually contradictory):**

1. ADR-0190 Decision 6: *"`httpcore.DiscloseAll` restores the **exact** pre-ADR-0190 wire
   shape in one call."*
2. ADR-0190 Residuals, first bullet: *"`incidents[].error` … Withheld by default (`Incidents`
   is not structural), **but a consumer setting `DiscloseAll` gets it** and no category
   isolates it."*
3. Plan Task 5 Step 2: *"a byte-comparison test that `WithDisclosure(DiscloseAll…)` reproduces
   the pre-change body **exactly**, on `/snapshot` especially."*

**Derivation.** Take the plan's own `PublicState` pseudocode (plan lines 363–428) and count
which withheld fields any `d.Has(...)` branch assigns:

```
RESTORED, by category (11 of 31 withheld fields):
  DiscloseVariables -> InstanceState.Variables, .StartVariables, .RootCompensations,
                       .Scopes, .ArchivedCompensations, Token.Payload, HumanTask.Vars   (7)
  DiscloseActors    -> HumanTask.Candidates, .Claim, .Completion                        (3)
  DisclosePolicy    -> HumanTask.Eligibility                                            (1)
  DiscloseNotes     -> (blanks Completion.Note; restores no field of its own)           (0)

RESTORED BY NOTHING (20 of 31):
  engine.InstanceState (15): Incidents, PendingFinalErr, Compensating, Timers,
      ArmedEvents, Boundaries, EventTriggeredSubprocesses, DeferredCompensationThrows,
      RecentCompensationCmdIDs, CmdSeq, TokenSeq, TaskSeq, TimerSeq, ScopeSeq, IncidentSeq
  engine.Token (5): AwaitCommand, AwaitSignal, AwaitMessage, AwaitMessageKey, AwaitTimer
```

**Two of those 20 are on the wire today.** `service/instance.go`'s `instanceJSON` — the
`/snapshot` document, which is entry point `endpoints.go:65` — carries:

```
service/instance.go:129   Incidents    []incidentJSON    `json:"incidents,omitempty"`
service/instance.go:136   Compensating *compensatingJSON `json:"compensating,omitempty"`
```

So after this change `GET /instances/{id}/snapshot` loses `incidents` and `compensating`
for an unidentified caller **permanently**, with no opt-out. Claim 2 is false in its second
half (no category restores `Incidents`); claim 1 is false; and claim 3 prescribes a test that
**cannot pass as designed** — the plan's one prescribed byte-comparison test is unimplementable.

**Why this is Critical and not bookkeeping.** `compensatingJSON` is not decoration.
`service/instance.go:130-135` documents it: *"It is what makes a WEDGED instance findable: a
stalled walk never dispatches again, so an instance already stalled before ADR-0175 shipped
raises no incident and would otherwise be invisible. **It also carries the command id the
three escape verbs require.**"* ADR-0175's operator escape from a stalled compensation walk
is driven off this field. Silently and irreversibly deleting it from the unidentified-caller
snapshot is a functional regression the bundle nowhere acknowledges — and the failure is
**silent at design time and loud at Task 5 Step 2**, where an implementer under a green-suite
obligation will widen `PublicState` ad hoc. That ad-hoc widening is precisely the
deny-list-drift failure mode the whole allow-list design exists to remove.

**Concrete fix.** Choose one and state it:
- (a) Add `Incidents` and `Compensating` to `DiscloseAll`'s reach — either a fifth category
  (`DiscloseDiagnostics`) covering `Incidents`, `PendingFinalErr` and `Compensating`, or make
  `DiscloseAll` a distinct sentinel meaning "no projection at all" rather than "every
  category". The sentinel is cleaner and makes claim 1 true by construction.
- (b) If they are meant to stay withheld unconditionally, then **delete claim 1's word
  "exact"**, delete the Residuals' *"a consumer setting `DiscloseAll` gets it"* clause, rewrite
  Task 5 Step 2 to compare against a *stated* expected-diff rather than byte-equality, and add
  `Compensating`'s loss to the ADR's Negative consequences with the ADR-0175 impact named.
- Either way, add to the plan's classification table a **third column: "restored by"**, so the
  20-with-no-category set is visible in the same table that is already asserted total. The
  classification guard should additionally assert that every `withheld` field names a
  restoring category or is explicitly listed as `neverRestored`.

## F2 — **CRITICAL** — the plan prescribes a mutation of the caller's state, one `⚠` after asserting `PublicState` "never mutates st"; and *"a test asserts it"* names a test the plan does not contain

**Claims attacked.**
- Plan line 376, in `PublicState`'s own doc comment: *"⚠ It **never mutates st**: callers hold
  state obtained from `ProcessInstance.State()`, which in-process consumers rely on for full
  fidelity."*
- Plan line 433-435, ~55 lines later: *"⚠ `DiscloseActors` restores `Completion` **including
  its `Note`**. If `DiscloseNotes` is not also set, **blank the note after the assignment** —
  categories are independent and **a test asserts it**."*

**The contradiction, from the reflection dump (CLEAN-1) plus the pseudocode.**
`humantask.HumanTask.Completion` is `*humantask.Completion` — a **pointer**. The prescribed
line is `out.Tasks[i].Completion = tk.Completion`, which copies the pointer, not the struct.
"Blank the note after the assignment" therefore writes through to the caller's
`st.Tasks[i].Completion.Note`. `PublicState` mutates its input, on the exact path the doc
comment says it never does, and the in-process fidelity the comment invokes is what breaks.

The same aliasing exists on every other restore: `out.Variables = st.Variables` shares the
map; `out.Tasks[i].Claim = tk.Claim` shares a `*humantask.Claim`; `slices.Clone(st.History)`
is shallow, so each `NodeVisit.LeftAt *time.Time` is shared. Those are latent (nothing writes
through them today); the `Note` blanking is not latent — the plan instructs it.

**And the guard against it cannot fire.** `TestPublicState_DoesNotMutateInput` (plan lines
322-332) calls `view.PublicState(st, authz.DisclosureSet{})` — the **zero** disclosure set. The
`d.Has(authz.DiscloseActors)` branch that contains the mutation never executes. The test is
vacuous for the one mutation the plan prescribes.

**Worse: no prescribed test touches `DiscloseActors` or `DiscloseNotes` at all.** All four
Task-2 tests use either `authz.DisclosureSet{}` or `NewDisclosureSet(authz.DiscloseVariables)`.
`TestPublicState_WidensOnDisclosure` asserts `got.Tasks[0].Claim == nil` under
`DiscloseVariables` — it tests that actors are *not* restored, never that they are, and never
the Note rule. So *"categories are independent and **a test asserts it**"* is a claim about a
test that **does not exist in this plan**. This is the CLAUDE.md Premise-Discipline failure by
name: a prescribed test whose falsifiability was never stated, plus a citation of a covering
test that is not covering.

**Concrete fix.**
1. Deep-copy on restore: `if tk.Completion != nil { c := *tk.Completion; if
   !d.Has(authz.DiscloseNotes) { c.Note = "" }; out.Tasks[i].Completion = &c }`. Same for
   `Claim`, and `maps.Clone` for `Variables`/`StartVariables`/`Vars`/`Payload`.
2. Add the missing case to `TestPublicState_DoesNotMutateInput` — run it **once per category
   and once with all four set**, table-driven, and assert `st.Tasks[0].Completion.Note` still
   holds its fixture value. State what makes it fail today: without the deep copy it fails on
   the `DiscloseActors` row.
3. Add `TestPublicState_NoteRequiresDiscloseNotes`: `DiscloseActors` alone ⇒
   `got.Tasks[0].Completion.Note == ""`; `DiscloseActors|DiscloseNotes` ⇒ the note survives.
4. Fix the doc comment to say what is actually true: *"never mutates st, and never aliases it —
   every restored map, slice and pointer is copied."*

## F3 — **MAJOR** — *"`c.authz` is written in exactly one place, inside `WithHumanTasks`"* is false: there are **two** writers, and the one the bundle misses is a fail-open `AllowAll{}` default

**Claim attacked**, stated twice, once with an explicit count:
- ADR-0190 Decision 8, last bullet: *"`service` has no `WithAuthorizer`; `c.authz` is written
  **only** inside `WithHumanTasks`."*
- Spec §3 D2 item 8: *"`service` has **no `WithAuthorizer`**; `c.authz` is written in **exactly
  one place**, inside `WithHumanTasks`. Any construction-time rule about the authorizer needs
  that option first."*

**Command run:**
```
$ grep -rn "c\.authz *=" --include='*.go' service/ | grep -v _test
service/service.go:199:	if c.authz == nil {
service/service.go:200:		c.authz = authz.AllowAll{}
service/options.go:83:			c.authz = az
$ awk '/^func /{f=$0} /c\.authz *=/{print FILENAME":"NR"  <- "f}' service/*.go
service/options.go:83   <- func WithHumanTasks(taskStore humantask.TaskStore, az authz.Authorizer) Option
service/service.go:200  <- func NewProcessEngine(opts ...Option) (*ProcessEngine, error)
```

**Actual member set: 2 writers.**
- `service/options.go:83`, inside `WithHumanTasks` — the one the bundle names.
- `service/service.go:200`, inside `NewProcessEngine`'s default resolution —
  `if c.authz == nil { c.authz = authz.AllowAll{} }`. **The bundle names neither this site nor
  the default it installs.**

**Why it is load-bearing rather than a nitpick.** The claim's stated purpose is to constrain
phase 2: *"Any construction-time rule about the authorizer needs that option first."* A phase-2
author who believes `WithHumanTasks` is the only writer will reason "no `WithHumanTasks` ⇒
`c.authz` is nil ⇒ I can detect an unconfigured engine and refuse". In fact it is never nil —
it is `authz.AllowAll{}`, which **allows everything**. A gate built on that false premise is
fail-open in exactly the deployment that never configured authorization. This is also the
premise sitting one bullet above the ADR's own *"the `AllowAll` type check is defeated by
wrapping"* constraint, which is about the same value from the other direction — the bundle
knows the sentinel exists and still says `c.authz` has one writer.

**Concrete fix.** In both documents: *"`service` has no `WithAuthorizer`. `c.authz` has two
writers: `WithHumanTasks` (`service/options.go:83`) and `NewProcessEngine`'s default
(`service/service.go:200`), which installs `authz.AllowAll{}` when no option supplied one — so
an unconfigured engine is authorizer-**present** and allow-all, never nil. A phase-2 gate must
therefore key on a capability declaration, not on nil-ness or on the concrete type."* This also
strengthens the adjacent `AllowAll`-type-check bullet rather than sitting beside it.

## F4 — **MAJOR** — *"six of them mutate"* survives verbatim in the spec after round 1 flagged it as **five** (C9) and the ADR half was fixed

**Claim attacked.** Spec §1, line 76: *"The 12 admin operations have no authorization and no
audit record; **six of them** mutate authorization policy or process state."*

**Round-1 finding C9** (`docs/specs/2026-08-26-adr-0190-audit-counting.md:547-575`) attacked
this exact sentence, said *"ADR-0190 Context and spec §1, **identically**"*, pasted the member
set, and prescribed the fix *"in both documents"*.

**Re-derived independently here.** The 12 admin operations, partitioned:
```
MUTATES (5): DeadLetterAdmin.Redrive, PolicyAdmin.AddPolicy, .RemovePolicy,
             .AddRole, .RemoveRole
READ-ONLY (7): DeadLetterAdmin.ListDeadLettered, LineageAdmin.Lineage,
             RelayStatsAdmin.OutboxStats, TimerAdmin.Stats, TimerAdmin.ListArmedPage,
             PolicyAdmin.ListPolicies, PolicyAdmin.ListRoles
```
⇒ **five**, confirming C9.

**What revision 2 did.** The ADR's Context was rewritten wholesale for revision 2, which
removed the sentence — so the ADR half is fixed **by accident, not by adjudication**. The spec
half was left untouched. Neither document's "Findings adjudicated as NOT defects" section
mentions C9, so this is not an adjudicated rejection; it is a fix applied to one of the two
places the finding named.

This is the round-1 pattern repeating: *a correction that does not sweep for the other copies*
(cf. the three stale "two render paths" recaps round 1 found, and the ADR-0187 lesson *"after
writing a corrected value, `grep` for the OLD one"*).

**Concrete fix.** Spec §1: *"…and no audit record; **five** of them mutate authorization policy
or process state — the four `PolicyAdmin` mutators plus `DeadLetterAdmin.Redrive`."* Naming the
closed set rather than counting it is what Premise Discipline prescribes. Then `grep -rn "six
of them\|12 admin" docs/` before the next commit.

## F5 — **MAJOR** — *"The seven variables sites"* is total only under an unstated restriction; the reachable set is **nine**, and the two it omits are the `actor.attributes` the spec's own §2.1 probe lists as part of the disclosure being fixed

**Claim attacked.** Spec §2.5: *"The **seven** variables-bearing sites confirmed by
execution: `Variables`, `StartVariables`, `Tokens[].Payload`, `Tasks[].Vars`,
`RootCompensations[].Input`, `Scopes[].Compensations[].Input`,
`ArchivedCompensations[k][].Input`."* Echoed by the plan's fixture comment *"populates ALL
SEVEN measured variables sites"* and by the 2→3→4→7 narrative in the ADR, spec §0.1 and the
commit message.

**Command run** (throwaway reflective type-graph walk from `engine.InstanceState`, depth 8,
reporting every `map[string]any` / `interface{}` leaf):

**Actual member set — 9 exported sites, not 7:**
```
InstanceState.Variables
InstanceState.StartVariables
InstanceState.Tokens[].Payload
InstanceState.Tasks[].Vars
InstanceState.Tasks[].Candidates[].Attributes      <- NOT in the seven
InstanceState.Tasks[].Claim.Actor.Attributes       <- NOT in the seven
InstanceState.Scopes[].Compensations[].Input
InstanceState.RootCompensations[].Input
InstanceState.ArchivedCompensations[k][].Input
(+ 2 unexported: InstanceState.ids.gen, InstanceState.ids.err)
```

**Why the omission matters even though the design is safe.** `authz.Actor.Attributes` is
free-form consumer-supplied `map[string]any` — the same shape and the same trust level as
process variables. The spec's own §2.1 probe table lists `claim.actor.attributes` and
`candidates` among the things disclosed unauthenticated, and the ADR gives them their own
category (`DiscloseActors`). So the document simultaneously treats actor attributes as
sensitive free-form data **and** excludes them from its enumeration of where free-form data
lives, without saying it is excluding them. The count is presented as an executed total
(*"confirmed by execution"*) when it is a total over an unstated subset.

**Not a security hole in revision 2** — `Claim` and `Candidates` are withheld wholesale by the
allow-list, so both sites are closed by construction. It is a **premise defect**: the number 7
is quoted in four documents including the commit message, and the next reader who needs "where
does free-form data live" will inherit a number that is short by two. Given this bundle's
history is *literally* a chain of four wrong answers to this question, shipping a fifth
unhedged one is the failure mode the bundle claims to have removed.

**Concrete fix.** Spec §2.5: *"Nine reachable `map[string]any` sites, derived by a reflective
walk over the type graph: seven carry **process variables** (…listed…) and two carry
**actor attributes** (`Tasks[].Candidates[].Attributes`, `Tasks[].Claim.Actor.Attributes`),
which are equally free-form and are withheld under `DiscloseActors` rather than
`DiscloseVariables`."* Keep the 2→3→4→7 narrative but label it *"…and the mechanical walk finds
nine"* — which strengthens §0.1's argument rather than weakening it. The plan's
`stateWithSecretEverywhere` helper should populate **all nine**, so
`TestPublicState_WithholdsEverySnapshotOfVariables` is non-vacuous for the two actor sites too.

## F6 — **MAJOR** — the render enumeration is total over `mapInstance`/`NewInstanceView`/`ProcessInstance`/`ActionableView`, but **four more `AdminRoutes` handlers render instance-derived data**, and one of them carries the same consumer-error text the ADR names as a residual

**Claim attacked.** ADR Context and spec §2.2: *"The render surface, derived mechanically
rather than counted: **4 mechanisms / 11 entry points**"*, and ADR Consequences: *"**All eleven**
render entry points close at once."* Plus the single asserted exemption, *"`AdminListInstances`
is exempt."*

**Command run:**
```
$ grep -n "^func \|return http.Status" transport/http/httpcore/admin_endpoints.go
```
**Actual member set — `admin_endpoints.go` has 15 handlers returning a body; 3 are in the 11:**
```
IN THE ELEVEN (3):  ResolveIncident:111, CancelInstance:121, ResolveCompensationStall:514
EXEMPTED BY NAME (1): AdminListInstances:96  -> instanceSummaryView (IDs, status, timestamps,
                      incident COUNT) — verified structural-only, exemption HOLDS
NEITHER — never mentioned by any bundle document (4):
  AdminInstanceLineage:484 -> lineageView       (instance ids, def ids, depths, outcome)
  AdminTimers:406          -> timerListResponse (instance id, def id, timer id, fire_at, kind)
  AdminRelayStats:307      -> relayStatsResponse (aggregate counts)
  ListDeadLetters:165      -> dlqListResponse   *** carries `last_error` ***
```

Three of the four are structural and would be sound exemptions — but they are **unstated**
exemptions, where `AdminListInstances` got an asserted one. The spec's §0 explicitly elevated
`AdminListInstances` to *"an asserted exemption, not an assumption"*; its three siblings did
not get the same treatment and nobody looked.

**The one that is not merely bookkeeping.** `ListDeadLetters` renders
`deadLetterView.LastError` (`admin_endpoints.go:139,162`) — the raw error string from a failed
outbox dispatch. That is the **same category** as the residual the ADR states in full:

> *"**Error text.** `incidents[].error` is `err.Error()` from the consumer's action verbatim
> and may embed variables. Withheld by default … but a consumer setting `DiscloseAll` gets it
> and no category isolates it."*

`deadLetterView.LastError` has the identical property and is **not** withheld by default —
it never passes through `PublicState`, because `ListDeadLetters` never touches
`engine.InstanceState`. So the bundle names a residual for the error-text disclosure it *does*
close and is silent about the structurally identical one it does *not*. (Note the interaction
with F1: `incidents[].error` is in fact **not** restorable by `DiscloseAll` either, so the
residual is wrong in its own terms — see F1.)

**Concrete fix.** Add to spec §2.2, immediately under the 4-mechanism table:

> **Asserted exemptions — `AdminRoutes` handlers that render no instance state.** Four more
> handlers in `admin_endpoints.go` return instance-derived bodies and are deliberately outside
> the eleven, because none reads `engine.InstanceState`: `AdminListInstances` (`:96`,
> `instanceSummaryView`), `AdminInstanceLineage` (`:484`, ids/depths/outcome), `AdminTimers`
> (`:406`, ids/fire_at/kind) and `AdminRelayStats` (`:307`, aggregate counts).
> ⚠ `ListDeadLetters` (`:165`) is the exception: `deadLetterView.LastError` is consumer error
> text with the same variable-leak property as `incidents[].error`, reaches the wire on a
> different path, and is **not** closed by this ADR. Recorded as a residual, scoped to
> deployments that mount `AdminRoutes`.

and add that last sentence to the ADR's Residuals beside the `incidents[].error` bullet.

## F7 — **MINOR** — the baseline *"59 ok / 2 FAIL"* does not add up to this module: **65 packages have tests and all 65 compile**

**Claim attacked.** Plan Global Constraints line 50 and Phase-1 verification checklist line
664: *"**Baseline is not green:** `internal/database` and `internal/dbtest` fail pre-existing on
MySQL/testcontainers. **59 ok / 2 FAIL.** Do not report those as regressions."*

**Commands run** (in the worktree at `a161f347`):
```
$ go list ./... | wc -l                                                        -> 112
$ go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | wc -l  -> 65
$ go vet ./... ; echo EXIT=$?                                                  -> EXIT=0
```
`go test ./...` emits one `ok`/`FAIL` line per package that has tests. 65 such packages exist
and `go vet ./...` (which compiles every test package) exits 0, so none is `[build failed]`.
**59 + 2 = 61 ≠ 65 — four result lines are unaccounted for.**

**Provenance.** The number is inherited from
`docs/specs/2026-08-26-adr-0190-audit-execution.md:892`: *"Baseline for every run: `98382afd`,
Docker up, `go test -count=1 ./...` = 59 ok / 2 pre-existing"*. `98382afd` is **revision 1's
bundle commit** — docs-only, therefore the same 65 test packages. So the discrepancy is not
drift between commits; the original figure was already short by four, and revision 2 restated
it without re-deriving. (Restating strips the hedge — CLAUDE.md Premise Discipline, *"re-verify
claims you inherit"*.)

**Why it matters operationally.** The plan tells subagents *"do not report those as
regressions"*. An agent that sees 63 ok / 2 FAIL against a stated 59 will either hunt four
phantom packages or — worse — treat four genuinely-new failures as part of the blessed
baseline.

**Concrete fix.** Re-run `go test -count=1 ./... 2>&1 | grep -cE '^(ok|FAIL|---)'` with Docker
up at the actual base and paste the real numbers, or replace the count with the **member set**:
*"the two known-red packages are `internal/database` and `internal/dbtest` (MySQL/testcontainers,
pre-existing). Every other package must be green; do not inherit a total — re-derive it."*
Naming the closed set instead of counting it is the prescribed remedy and is immune to this
whole class of error.

## F8 — **MINOR** — *"measured 18 failures in 3 packages"* is a measurement of **revision 1's** posture, which revision 2 strictly widens; the number is a floor, not an expectation

**Claim attacked.** Plan Phase-1 verification checklist: *"`go test ./...` — **re-derive the
breakage net; do not inherit revision 1's.** Its 8-file prediction was wrong both ways:
measured **18 failures in 3 packages**, 6 of 8 predicted files unaffected, and
`transport/http/stdlib/maxbody_test.go` breaks and was unnamed."*

**Verified as round-1 evidence** — `docs/specs/2026-08-26-adr-0190-audit-execution.md:773-786`
records `EXIT=1`, *"18 failing test functions/subtests in exactly 3 packages"* (`runtime/view`,
`service`, `transport/http/stdlib`), with the per-file table. The number is real and correctly
attributed.

**The defect is the mixed message.** That measurement ablated **revision 1's deny-list
posture**: `Variables=nil`, `Tokens[].Payload=nil`, `Candidates=nil`, `Claim.Actor`/
`Completion.Actor` **zeroed**, `Completion.Note=""`, `Definition=nil`. Revision 2's allow-list
is **strictly broader** — it additionally drops `Tasks[].Claim` and `.Completion` *wholesale*
(not zeroed-actor-inside-a-kept-claim), `Tasks[].Eligibility`, all five `Token.Await*` fields,
`Token.RetryStartedAt`, `Incidents` and `Compensating` (F1). So revision 2 must break **at
least** 18 and near-certainly more, in at least those 3 packages and plausibly others. A
sentence that says "re-derive, do not inherit" and then hands over a definite number invites
exactly the inheritance it forbids.

Cross-check confirming how narrow the InstanceView-path net actually is:
```
$ grep -rn '"variables"' --include='*_test.go' transport/ service/ runtime/
transport/http/stdlib/maxbody_test.go:151
runtime/outbox_test.go:157      (an outbox payload, unrelated)
```
Exactly one test in the repo asserts `resp["variables"]` on an HTTP body — so the `mapInstance`
nine-of-eleven sites have almost no pinning, and the breakage will concentrate in `/snapshot`
and `/actionable`, which is a different distribution from revision 1's.

**Concrete fix.** *"Revision 1's ablation measured 18 failures in 3 packages
(`runtime/view`, `service`, `transport/http/stdlib`) against a **strictly narrower** posture —
it zeroed `Claim.Actor` where revision 2 drops `Claim` entirely, and it never touched
`Incidents`, `Compensating` or `Token.Await*`. **Expect ≥ 18 and a different member set.** Do
not treat 18 as a target; re-derive the net by ablation and adjudicate every break as
*correct* rather than patching it away."*

## F9 — **MINOR** — *"`NewProcessInstance` always embeds a non-nil definition"* is contradicted by its own next clause and by an existing call site

**Claim attacked.** Plan Task 4, immediately above Step 4: *"⚠ `NewProcessInstance` **always**
embeds a non-nil definition, so passing `nil` is the only way to withhold policy on this path.
Verify the marshalled document omits `definition` entirely rather than emitting
`"definition":null`."*

**Source.** `service/instance.go:44-49`: *"`NewProcessInstance` fuses a definition (**may be
nil**) and instance state…"*, signature `func NewProcessInstance(def *model.ProcessDefinition,
st engine.InstanceState) ProcessInstance`. And the repo already calls it with nil:
```
transport/http/gin/gin_bodycap_test.go:228:
    return service.NewProcessInstance(nil, engine.InstanceState{InstanceID: "inst-1"}), nil
```
`instanceJSON.Definition` is `*model.ProcessDefinition` with `json:"definition,omitempty"`
(`service/instance.go:143`), so a nil definition is omitted, not rendered as null — the very
property the ⚠ asks the implementer to "verify" is already demonstrated by an existing test.

**Fix.** *"⚠ `NewProcessInstance` takes `def *model.ProcessDefinition` and accepts nil
(`service/instance.go:44`); `instanceJSON.Definition` is `omitempty`, so nil omits the
`definition` key rather than emitting `"definition":null` — already exercised by
`transport/http/gin/gin_bodycap_test.go:228`. Passing nil is therefore how policy is withheld
on this path, and no `MarshalJSON` change is needed."* This turns an unexecuted "verify this"
into a cited, executed premise.

## F10 — **MINOR / OBSERVATION** — the kiosk-claimant residual attributes the rule to ADR-0189; the cited source comment attributes it to ADR-0148, whose file does not contain the word

**Claim attacked.** ADR Residuals: *"ADR-0189 blesses a kiosk claimant `{ID:"", Roles:["kiosk"]}`
at `humantask/validate.go:24`."*

**Citation verified — the line resolves exactly.** `humantask/validate.go:24` is:
```
// A Claim whose Actor.ID is empty is deliberately accepted: that is ADR-0148
// amendment 1 §4's kiosk claimant, anonymous but carrying roles.
```
So the bundle's *line number* is right and its *paraphrase* is right. Two notes:
- The attribution to **ADR-0189** is defensible (`grep -c kiosk docs/adr/0189-*.md` -> 6), but
  the source comment credits **ADR-0148 amendment 1 §4**, and
  `grep -c kiosk docs/adr/0148-human-task-audit-persistence-columns.md` -> **0**. The code
  comment's own citation appears to be dangling. Out of this bundle's scope, but worth a
  backlog line — and the ADR should cite both records so the next reader is not sent to a file
  that never mentions the concept.
- Substantively the residual is correct and correctly flagged for this audit: a permissive
  `RequestActorFunc` manufacturing `{ID:"", Roles:["kiosk"]}` yields "actor present" ⇒ **full
  fidelity**, which is the whole disclosure re-opened by one line of consumer wiring. Under F1's
  fix this becomes the *only* way to get `Incidents`/`Compensating` back, which sharpens rather
  than softens it. Recommend the ADR state the mitigation explicitly: `RequestActor`'s contract
  should say that returning a zero-ID actor grants full disclosure, so a consumer choosing the
  kiosk shape is making a disclosure decision, not only an authentication one.

---

## Verified CLEAN — do not re-audit

| # | claim | how verified | result |
|---|---|---|---|
| 1 | classification table: 31=11+20, 13=7+6, 11=6+5, 6=6+0 | reflection, set equality | **exact**, no unclassified / misspelled / duplicated field (CLEAN-1) |
| 2 | the four structs are the right closure | every public field's type peeled to its leaf | **exact** — all scalars, `time.Time`, or the three tabled structs (CLEAN-2) |
| 3 | `endpoints.go:42,52,65,77,94,133,158,182` | `sed -n` on each | **all 8 exact**: 42/52/94/133/158/182 = `mapInstance`, 65 = `return …, pi, nil` (self-marshalling), 77 = `view.NewActionableView` |
| 4 | `admin_endpoints.go:111,121,514` | grep | **all 3 exact** — `NewInstanceView(pi.State())` in `ResolveIncident` / `CancelInstance` / `ResolveCompensationStall` |
| 5 | "4 mechanisms / 11 entry points" | 6+3+1+1 over the sites above | **11 exact** (see F6 for what is *outside* the 11) |
| 6 | "identical 29-member dispatch set" across stdlib/gin/fiber | round-1's command re-run verbatim | **29, and `diff` IDENTICAL both ways.** Member set pasted below |
| 7 | `runtime/task/service.go:199,234,255,306` = the four `Authorize` sites | `grep -rn "\.Authorize("` repo-wide, non-test | **exact and total** — claim(199), reassign(234), complete(255), refresh-candidates(306); the only other hit is the `casbinauthz` delegation |
| 8 | ADR-0095:159-165 quotes | `sed -n '155,170p'` | **verbatim** — heading at :159; *"Default-absent replaces the old default-deny (403)"* and *"This is safer than a built-in default-deny gate"* both present |
| 9 | `humantask/validate.go:24` | `sed -n '18,30p'` | **exact line** (see F10 for the attribution nuance) |
| 10 | "~71 findings, **17 Critical**" | ID enumeration per audit file | **17 Critical exact** (execution F1/F1b/F2/F4/F25 = 5; counting C1–C4 = 4; failuremode F1/F2/F3/F6 = 4; interaction I-1–I-4 = 4). Findings total is **72** (execution has 26: F1–F25 plus F1b); "~71" is within its tilde but prefer the exact 72 |
| 11 | "12 admin operations across 5 interfaces" | member set re-derived | **exact**: DeadLetterAdmin 2, PolicyAdmin 6, TimerAdmin 2, LineageAdmin 1, RelayStatsAdmin 1 |
| 12 | "8 ungated `Service` operations" / "20 ungated operations" | 8 + 12 | **20 exact** |
| 13 | `service/service.go:316`, `service/options.go:83` | `sed -n` | **exact** — the `AllowAll` type assertion; the `WithHumanTasks` authorizer write |
| 14 | spec §2.6 — a fresh cross-package literal compiles and zeroes | `engine.InstanceState{InstanceID:"i1"}` built from a `runtime/view` test file | **holds** — compiles, `Variables` nil |
| 15 | commit message vs bundle | read in full | **no stale number**; every figure it quotes (71, 17, 2→3→4→7, 11/4) matches the documents |
| 16 | container-free package list | `go test -count=1` on the six named groups | **10 ok, 0 fail, EXIT=0** — `engine`, `service`, `transport/http/{httpcore,stdlib,gin,fiber,parity}`, `runtime/view`, `authz`, `humantask` |

**The 29-member dispatch set**, verbatim (`grep -oE 'httpcore\.[A-Z][A-Za-z]+\(' transport/http/$a/groups.go | sort -u`, byte-identical for stdlib/gin/fiber):

AddPolicy, AddRoleBinding, AdminInstanceLineage, AdminListInstances, AdminRelayStats,
AdminTimers, CancelInstance, ClaimTask, CompleteTask, DeliverMessage, DeliverSignal,
EvaluateLive, EvaluateReady, GetActionableView, GetInstance, GetInstanceSnapshot,
ListDeadLetters, ListPolicies, ListRoleBindings, NewInstrumentation, ReassignTask,
RedriveDeadLetters, RemovePolicy, RemoveRoleBinding, RequestActor,
ResolveCompensationStall, ResolveConfig, ResolveIncident, StartInstance. **= 29.**

⚠ **Scope, so it is not over-restated (round 1 warned about exactly this).** The net is scoped
to `groups.go`. Widening it to the whole adapter package adds symbols that are **not** identical
across the three: `ClassifyError` and `HealthCheckFunc` (all three, outside `groups.go`),
`WithMaxBodyBytes` (**stdlib only**), `WithRequestActor` (stdlib + gin, **not fiber**),
`WithRouterFunc` (gin + fiber, **not stdlib**). Round 1 hedged accordingly: *"It does not prove
identical body-decode, error-write or middleware ordering — those live in each adapter's own
helpers (`decodeRequestBody`, `writeErr`), which this net does not inspect."*
✅ Revision 2 **preserves that hedge** (spec §0: *"⚠ It also generalises every disclosure
finding to all three transports"* — scoped to disclosure, not to decode/error paths), and the
plan's Task 5 Step 1 turns the residual into a falsifiable parity test rather than an
assumption. No over-restatement found. ⚠ For **F1**'s fix, note that `DiscloseAll` will be a
*new* symbol reaching all three adapters, so it must be added to `groups.go` in each — the
29-member parity is the reason one implementation suffices, and also the reason a miss in one
adapter is silent.

---

## Round-1 restatement check (mandate 5) — what revision 2 got right and wrong

| round-1 fact | revision 2's restatement | verdict |
|---|---|---|
| 17 Critical | "17 Critical" | ✅ exact |
| 71/72 findings | "~71 findings" | ✅ hedged, within tolerance |
| execution F4: seven variables sites | "seven variables sites … confirmed by execution" | ⚠ **F5** — total only over process variables; nine reachable |
| counting C4: 4 mechanisms / 11 entry points | "4 mechanisms / 11 entry points" | ✅ exact; ⚠ **F6** for what lies outside |
| counting C1: no-instance op set wrong both ways | ADR Decision 8 restates it accurately | ✅ |
| counting **C9: "six" is five** | spec §1 still says **six** | ❌ **F4 — not fixed** |
| counting C7 / execution: 29-member set, hedged | hedge preserved | ✅ |
| execution F18: baseline 59 ok / 2 FAIL | restated verbatim | ⚠ **F7** — arithmetic does not close |
| execution F24: 18 failures in 3 packages | restated as a measured number | ⚠ **F8** — measured against a narrower posture |
| interaction I-10: "preserved exactly" over-claimed | ADR now splits route-mounting from delivered posture, correctly | ✅ **well done** — this is the model correction |
| interaction I-13: `json.Marshal(pi)` for embedded consumers | drove Decision 2; correctly stated | ✅ |
| execution F25 / plan "Fixture traps": harness actor is `{ID:"alice"}`, no `Attributes`; `f2` has no `Condition` | restated in plan Global Constraints | ✅ preserved, and the plan commits every test to a named fixture |

**Bottom line for this lens.** The thing revision 2 was most likely to get wrong — the
classification table its entire design rests on — is **correct, total and exactly counted**.
The counting failures moved elsewhere: to what the table's columns *lead to*
(**F1**: 20 withheld fields no category can restore, contradicting the ADR's own residual and
making its one prescribed byte-comparison test unpassable), to a prescribed operation that
violates a stated invariant with a test that cannot catch it (**F2**), and to three inherited
numbers restated without re-derivation (**F4**, **F7**, **F8**).
