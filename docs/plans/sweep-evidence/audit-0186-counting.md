# Audit — ADR-0186 bundle, COUNTING lens (enumerations, quantifiers, counts, inherited citations)

- Date: 2026-08-21
- Anchor: `32f4e3e5` (detached worktree `a186-count`)
- Bundle: `docs/specs/2026-08-21-untrusted-input-and-disclosure.md`,
  `docs/adr/0186-untrusted-input-and-disclosure-posture.md`,
  `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`
- Step 0: **all three bundle files present at the anchor.** Proceeding.

Findings are appended one at a time, as confirmed.

---

## C1 — CRITICAL · "every one of the 39 decode sites already wraps in `ErrBadInput`" is FALSE: **3 of the 39 discard the decode error entirely**

**Claim as written** (three documents):

- ADR `0186-…md:165-169`:
  > "⚠⚠ **And the oversize error must NOT carry `ErrBadInput`, or it ships as 400.**
  > Re-audit #2 caught this and it was confirmed against source. **Every one of the 39
  > decode sites *already* double-wraps** — `writeErr(cfg, gc, fmt.Errorf("%w: %w",
  > httpcore.ErrBadInput, err))`"
- Spec `…untrusted-input-and-disclosure.md:79`:
  > *"oversize bodies return 413"* | ⚠ **They would return 400.** **All 39 decode sites
  > already wrap in `httpcore.ErrBadInput`** …
- Plan `…untrusted-input-and-disclosure.md:264-267` (phase 5 brief):
  > "⚠ **The oversize path returns the BARE `httpcore.ErrBodyTooLarge`.** **Every decode
  > site today wraps in `fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)`** — keep that
  > for **decode** failures only."

**Re-derived** (the count 39 is right; the quantifier over it is not):

```
$ grep -rn "json.NewDecoder" transport/http/stdlib/ --include='*.go' | grep -v _test.go | cut -d: -f1 | sort | uniq -c
  13 transport/http/stdlib/groups.go
$ grep -rn "ShouldBindJSON" transport/http/gin/ --include='*.go' | grep -v _test.go | cut -d: -f1 | sort | uniq -c
  13 transport/http/gin/groups.go
$ grep -rn "Bind()" transport/http/fiber/ --include='*.go' | grep -v _test.go | cut -d: -f1 | sort | uniq -c
  13 transport/http/fiber/groups.go
```
39 sites: CONFIRMED. Now the **net** — what does the `%w: %w` pattern fail to match?

```
$ grep -n -A3 "json.NewDecoder" transport/http/stdlib/groups.go
...
238:			_ = json.NewDecoder(req.Body).Decode(&in) // body is optional
239-			status, body, err := httpcore.ResolveIncident(req.Context(), c.Svc, instanceID, incidentID, in)

$ grep -rnE "_ = (gc\.ShouldBindJSON|c\.Bind\(\)\.JSON|json\.NewDecoder)" transport/http/*/groups.go
transport/http/fiber/groups.go:255:				_ = c.Bind().JSON(&in)
transport/http/gin/groups.go:265:			_ = gc.ShouldBindJSON(&in)
transport/http/stdlib/groups.go:238:			_ = json.NewDecoder(req.Body).Decode(&in) // body is optional
```

**Verdict: WRONG.** **36** of the 39 sites wrap in `ErrBadInput`; **3 discard the decode
error to the blank identifier** — one per adapter, all three on the *same* route
(`ResolveIncident`, whose body is optional). They neither wrap nor report.

**Damage if acted on.** The phase-5 agent is briefed to "keep the `ErrBadInput` wrap for
decode failures only" and convert the oversize signal to a bare sentinel. At these three
sites there is **no error path to convert** — the `*http.MaxBytesError` (stdlib/gin) or
the pre-check's error (fiber) is assigned to `_` and thrown away. The handler proceeds with
a zero-valued/partially-decoded `in` and returns the endpoint's **success** status. Result:
**the body cap is silently unenforced on the incident-resolve route in all three adapters**,
and the prescribed test `TestOversizedBodyReturns413` — which exercises one capped route —
stays green over the hole. This is exactly the "ships as 400 two phases downstream where the
failure is hardest to attribute" failure the ADR paragraph was *written to prevent*,
one route over.

Note the sub-claim is also self-undermining: the ADR uses "every one of the 39 already
double-wraps" as the *reason* the bare sentinel is needed. The reason is sound for 36 sites
and the remaining 3 need the **opposite** instruction (add an error path where none exists).

**Proposed replacement wording** (name the closed set, and give phase 5 the second
instruction):

> **36 of the 39 decode sites** double-wrap (`fmt.Errorf("%w: %w", httpcore.ErrBadInput,
> err)`); the remaining **three — `stdlib/groups.go:238`, `gin/groups.go:265`,
> `fiber/groups.go:255`, all the optional-body `ResolveIncident` route — discard the decode
> error to `_`.** For those three the oversize signal must be handled explicitly (check for
> the oversize condition *before* the optional decode and `writeErr` the bare
> `ErrBodyTooLarge`), because "convert the existing wrap" has nothing to convert there.
> Phase 5's per-adapter test needs a second case: `TestOversizedBodyReturns413OnOptionalBodyRoute`,
> whose falsifier is *it fails against an implementation that only edits the 12 wrapping sites.*

---

## C2 — MAJOR · The plan's "0 existing caps" grep is **still vacuous** — the `-E` fix was applied but the BRE escape `\|` was left in, so it searches for a literal `MaxBytesReader|BodyLimit`

**Claim as written.** ADR `0186:42-45` warns about exactly this class:

> `grep -rnE "MaxBytesReader|BodyLimit" transport/` → **0**.
> ⚠ Note the `-E`: the draft wrote this and one other grep **without** it, so `|`
> was a literal and the command returned 0 for *any* repository — evidence that
> could not falsify the claim it was offered for. Re-run correctly, the claims hold.

The plan restates the same evidence, plan `…:360`:

> | …already capped by us | **0** (`grep -rnE "MaxBytesReader\|BodyLimit" transport/`) |

**Re-derived.** In ERE, `\|` is an *escaped* pipe = a **literal** `|`:

```
$ printf 'MaxBytesReader\nBodyLimit\nMaxBytesReader|BodyLimit\n' > /tmp/gtest.txt
$ grep -nE "MaxBytesReader\|BodyLimit" /tmp/gtest.txt      # the PLAN's form
3:MaxBytesReader|BodyLimit
$ grep -nE "MaxBytesReader|BodyLimit" /tmp/gtest.txt        # the ADR's form
1:MaxBytesReader
2:BodyLimit
3:MaxBytesReader|BodyLimit
```

So the plan's recorded command matches only the literal 24-character string and **returns 0
for any repository** — the identical vacuity the ADR paragraph was written to retire, re-created
by a fix that added the `-E` and forgot to un-escape the pipe.

**Verdict on the command: UNFALSIFIABLE-AS-WRITTEN. Verdict on the underlying claim: CONFIRMED**
(the correctly-written grep also returns nothing):

```
$ grep -rnE "MaxBytesReader|BodyLimit" transport/ ; echo EXIT=$?
EXIT=1
$ grep -rnE "MaxBytesHandler|LimitReader|MaxRequestBodySize|BodyLimit|MaxBytesReader|MaxHeaderBytes|ReadLimit" --include='*.go' .
action/httpcall/httpcall.go:194:	b, err := io.ReadAll(io.LimitReader(r, max+1))
```

**Damage if acted on.** Low direct damage (the claim is true) but high process damage: the plan
is the document a fresh session re-runs, and this row is the *only* recorded evidence for
"0 existing caps". A future reader re-runs it, sees `EXIT=1`, and concludes the caps are still
absent **even after phase 5 has added all 39 of them** — the command cannot observe the change
it is supposed to gate. It is also the third occurrence of this exact bug in the B3 lineage.

**Proposed replacement wording** (plan §4 row):

> | …already capped by us | **0** — `grep -rnE 'MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader' transport/` exits 1. ⚠ **Bare `|` with `-E`; `\|` in ERE is a literal pipe and returns 0 for any repo.** After phase 5 this command must return 26 hits (stdlib 13 + gin 13); fiber uses a `len(c.Body())` pre-check and will not match. |

Also worth adding to the ADR: the repo **does** already own one request-size idiom —
`action/httpcall/httpcall.go:194` `io.ReadAll(io.LimitReader(r, max+1))` caps the *response*
body of an outbound call. Phase 6 touches that file and the ADR never mentions the precedent.

---

## C3 — MAJOR · The ADR's own banner says the 400 arm carries **nine** sentinels; its Decision 5, its Consequences, the plan and the source all say **eight**

**Claim as written.**

- ADR `0186:16-18` (the RE-CUT banner, the first thing a reader trusts):
  > "the 400 arm turns out to carry **nine sentinels and four validation strategies**, one of
  > which leaks a predicate source"
- ADR `0186:357-360` (Decision 5):
  > "Re-derived: the arm matches **eight sentinels across five `errors.Is` groups**
  > (`kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor`, `ErrBadInput`,
  > `validation.ErrInvalidInput`, `engine.ErrInvalidOutcome`, `engine.ErrOutcomeRequired`,
  > `engine.ErrEmptyTriggerKey`, `engine.ErrEmptyReassignTarget`)"
- ADR `0186:430` (Consequences): "static text for the other three strategies and the **seven
  non-validation sentinels**" (⇒ 7 + `ErrInvalidInput` = 8)
- ADR `0186:374` (Decision 5 table): "`avro`, `callback`, and **the other seven sentinels**"
- Plan `…:364`: "| sentinels in the 400 arm | **8**, across 5 `errors.Is` groups |"

**Re-derived** — `transport/http/httpcore/errors.go:36-50`, the 400 arm, verbatim:

```
$ sed -n '36,50p' transport/http/httpcore/errors.go | grep -c 'errors.Is'
8
```
The eight, in source order: `kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor`,
`ErrBadInput`, `validation.ErrInvalidInput`, `engine.ErrInvalidOutcome`,
`engine.ErrOutcomeRequired`, `engine.ErrEmptyTriggerKey`, `engine.ErrEmptyReassignTarget`.
Distributed over five source lines (`:36` 2, `:37` 2, `:42` 2, `:46` 1, `:49` 1) — which is
what "five `errors.Is` groups" means, and it is correct.

**Verdict: the banner is WRONG (nine); everything else is CONFIRMED (eight).**

**Damage if acted on.** This is the repo's signature failure — a number corrected in one place
and not the others — sitting in the *summary banner*, the highest-authority sentence in the
record. Decision 5 makes the allow-list **deny-by-default over an enumerated set**; an
implementer or reviewer who takes "nine" as the size of that set concludes one sentinel is
missing from the enumeration and either hunts for a ninth that does not exist, or (worse)
adds a speculative arm. The count is also cited in the *plan's* Progress lineage as a folded
audit finding, so it propagates.

**Proposed replacement wording** (ADR banner, name the set's *shape* rather than re-quoting a
count that must agree with three other places):

> "the 400 arm turns out to carry **every non-validation 4xx sentinel the transport owns plus
> `validation.ErrInvalidInput`, all rendered by one `Message: err.Error()`** — and
> `ErrInvalidInput` fans out to four validation strategies, one of which leaks a predicate
> source — so D5 becomes **allow-list** rendering with a static default."

---

## Batch table — counts re-derived and CONFIRMED

| claim | where | re-derived | verdict |
|---|---|---|---|
| 39 decode sites, 13/13/13/0 | ADR `:39-42`, plan `:359` | stdlib 13 `json.NewDecoder`, gin 13 `ShouldBindJSON`, fiber 13 `c.Bind().JSON`, httpcore 0 — non-test | ✅ (but see **C1** for the quantifier over them) |
| all in each package's `groups.go` | ADR `:42` | every hit is in `groups.go`; no other decode idiom (`io.ReadAll`/`json.Unmarshal`/`GetRawData`/`BodyParser`) exists non-test in any adapter | ✅ |
| `fiber.DefaultBodyLimit` = 4 MiB, `app.go:585`, applied in `New()` at `:710` | ADR `:46-47`, spec `:152-155` | `go doc fiber/v3.DefaultBodyLimit` → `4 * 1024 * 1024`; `app.go:585` is the const; line 710 is inside `func New(config ...Config) *App` (opens `:666`) | ✅ |
| `BodyParser` name rotted → `c.Bind().JSON`, zero hits repo-wide | spec `:71` | `grep -rn BodyParser` → 0 hits | ✅ |
| `ClassifyError` has exactly 6 arms, ordered 404 `:28`, 403 `:32`, 409 `:34`, 400 `:36-50`, 422 `:51`, default 500 `:57` | ADR `:170-171`, plan `:362` | exact line-for-line match at `32f4e3e5` | ✅ |
| 5 arms echo `err.Error()`: 404 `:31`, 403 `:33`, 409 `:35`, 400 `:50`, 422 `:56`; 500 `:58` blanks | ADR `:96-99`, plan `:363` | exact match | ✅ |
| "the switch has exactly six arms and the set is closed" | ADR `:98-99` | `ClassifyError` is the sole error classifier; 3 callers (`stdlib/write.go:31`, `gin/write.go:12`, `fiber/write.go:12`); no other `http.Status4xx/5xx` producer exists non-test in `transport/` | ✅ |
| 8 sentinels / 5 `errors.Is` groups in the 400 arm | ADR `:357`, plan `:364` | 8 `errors.Is` calls on 5 source lines | ✅ (banner contradicts — **C3**) |

---

## C4 — CRITICAL · The read-path enumeration is **6 + 2 = 8**; source has **6 + 2 + 3 = 11**. Three admin endpoints call `NewInstanceView` **directly**, bypassing `mapInstance` *and* the bundle's covered set

**Claim as written.**

- Plan `…:367`: "| `mapInstance` call sites | **6** — and **2** further read endpoints take no mapper at all |"
- ADR `0186:433-437` (Consequences):
  > "Redaction is applied at the `ProcessInstance` → response boundary, so it covers the
  > two mapper-less non-admin read endpoints (`GetInstanceSnapshot`, `GetActionableView`)
  > **as well as the six that go through `mapInstance`**."
- ADR `0186:320-322` (Decision 4): "`AdminListInstances` was checked and is clean — it
  projects no variables (`admin_endpoints.go:81-95`)."

**Re-derived.** The `mapInstance` grep — the bundle's net — is right, and blind:

```
$ grep -rn "mapInstance" --include='*.go' .
transport/http/httpcore/endpoints.go:15:func mapInstance(...)
transport/http/httpcore/endpoints.go:42,52,94,124,140,155     <- 6 call sites  ✅
```

Now widen the net to the thing that actually renders variables — `NewInstanceView`,
whose `view.go:31` is `Variables: st.Variables`:

```
$ grep -rn "NewInstanceView" --include='*.go' . | grep -v _test.go
transport/http/httpcore/seam.go:42,54          (the default mapper)
transport/http/httpcore/admin_endpoints.go:111  <- ResolveIncident
transport/http/httpcore/admin_endpoints.go:121  <- CancelInstance
transport/http/httpcore/admin_endpoints.go:514  <- ResolveCompensationStall
transport/http/httpcore/endpoints.go:17         (mapInstance's nil-mapper default)
transport/http/httpcore/view.go:23              (the definition)
```

Three endpoints — `ResolveIncident` (`admin_endpoints.go:111`), `CancelInstance` (`:121`),
`ResolveCompensationStall` (`:514`) — each `return http.StatusOK, NewInstanceView(pi.State()), nil`
**directly**. They take no mapper, never call `mapInstance`, and are not
`AdminListInstances` (which is the one admin path the ADR *did* check, and which is
genuinely clean — it builds `instanceSummaryView`, no variables). Their routes, present
in all three adapters:

```
POST /admin/instances/{id}/incidents/{incidentID}/resolve
POST /admin/instances/{id}/cancel
POST /admin/instances/{id}/compensation/resolve-stall
```

**Verdict: WRONG.** The set of response paths that project process variables is **eleven**,
not eight. Decision 4's own check of the admin surface sampled one endpoint of four and
generalised.

**Damage if acted on.** D4 deliberately moves redaction **out of** `NewInstanceView` (to
avoid `InstanceMapper` bypassing it) into "a helper every read path calls". The
implementer's list of read paths is the ADR's list of eight. These three call neither the
helper nor a mapper, so after D4 ships **three admin endpoints return unredacted process
variables in their 200 body** while the Consequences section asserts the covered set is
closed — the identical shape of the *"'cannot be bypassed' was true of the mapper and false
of the endpoints"* defect this very ADR congratulates itself on having caught one level up.
Phase 4's six prescribed tests contain no case for any of the three, so the gap is
undetectable by the bundle's own suite. Note also that these are the routes the ADR itself
flags as having **no built-in authentication** (`stdlib/groups.go:189`).

**Proposed replacement wording** (name the closed set by the symbol that actually renders,
not by the helper):

> The response paths that project `InstanceState.Variables` are the **eleven** callers of
> `NewInstanceView`/`mapInstance`: the **six** `mapInstance` sites
> (`endpoints.go:42,52,94,124,140,155`), the **three** direct `NewInstanceView` admin sites
> (`admin_endpoints.go:111` `ResolveIncident`, `:121` `CancelInstance`, `:514`
> `ResolveCompensationStall`), and the **two** mapper-less read endpoints
> (`GetInstanceSnapshot` `endpoints.go:60`, `GetActionableView` `:72`).
> `AdminListInstances` (`admin_endpoints.go:81-95`) projects `instanceSummaryView` and is
> the only instance-returning endpoint that is clean.
> ⚠ **Verification invariant for phase 4:** `grep -c 'NewInstanceView(' transport/http/httpcore/*.go`
> must equal the number of call sites routed through the redaction helper — assert it in a
> test, because this enumeration has now rotted twice.

---

## C5 — CRITICAL · `TestActionableViewRedactsTaskVars` **cannot be written**: `ActionableView` renders **no** task variables. What it *does* disclose (flow condition source, actor attributes) is enumerated nowhere in the bundle

**Claim as written.**

- ADR `0186:314-316` (Decision 4):
  > "and `GetActionableView` (`endpoints.go:72`) renders open human tasks, **whose
  > `HumanTask.Vars` is the per-task variable snapshot**."
- Plan `…:238-244` (phase 4 test 5), listed as one of "**the controls that decide D4's
  placement**":
  > "`TestSnapshotEndpointRedactsVariables` and **`TestActionableViewRedactsTaskVars`** …
  > `GetActionableView` (`:72`) **renders task vars**. … **Fails today:** no redaction
  > exists — and each **fails against a fix confined to `mapInstance`**, which is the whole
  > point."
- ADR `0186:434-435` repeats it in Consequences: redaction "covers the two mapper-less
  non-admin read endpoints (`GetInstanceSnapshot`, `GetActionableView`)".

**Re-derived.** `GetActionableView` returns `view.NewActionableView(pi.State(), pi.Definition())`
(`endpoints.go:72,78`). The DTO is `runtime/view/instance_actionable.go`:

```
$ grep -n 'json:' runtime/view/instance_actionable.go
14:	FlowID string `json:"flow_id"`
16:	Target string `json:"target"`
18:	Condition string `json:"condition,omitempty"`        <-- routing EXPRESSION SOURCE
20:	IsDefault bool `json:"is_default,omitempty"`
27:	TaskID string `json:"task_id"`
29:	NodeID string `json:"node_id"`
31:	State string `json:"state"`
33:	Claim *humantask.Claim `json:"claim,omitempty"`
36:	Candidates []authz.Actor `json:"candidates,omitempty"`   <-- "verbatim as {id, roles, attributes}"
40:	AllowedActions []NextAction `json:"allowed_actions,omitempty"`
50:	InstanceID string `json:"instance_id"`
52:	Status string `json:"status"`
54:	OpenTasks []ActionableTask `json:"open_tasks,omitempty"`
```

`ActionableTask` has **no `Vars` field**, and `NewActionableView` (`:62-104`) never reads
`t.Vars` — it copies `TaskID, NodeID, State, Claim, Candidates, AllowedActions` only.
`HumanTask.Vars` does exist (`humantask/humantask.go:119`) but carries **no JSON tag** and
is not projected here. Independently:

```
$ grep -rnE 'Variables|\.Vars\b' transport/ --include='*.go' | grep -v _test.go
transport/http/httpcore/view.go:19:	Variables  map[string]any `json:"variables,omitempty"`
transport/http/httpcore/view.go:31:		Variables:  st.Variables,
transport/http/httpcore/endpoints.go:37:		Vars:   in.Vars,          # an INPUT field
```
No transport response path renders task vars at all.

**Verdict: WRONG**, in both directions — the asserted disclosure does not exist, and the
actual ones are unlisted. `NewActionableView` also **already clones** (`:88` `audit := t.Clone()`,
with a comment saying exactly why), so it is not even an instance of the aliasing defect.

**Damage if acted on.** (1) Phase 4's implementer is told to write a test named
`TestActionableViewRedactsTaskVars` that is one of the two "controls that decide D4's
placement". There is nothing to redact, so the test either does not compile or is written
to pass vacuously — and per this repo's own lesson, *a fixture can be as vacuous as an
assertion*. The bundle's evidence that D4's placement is right then rests on one control,
not two. (2) The real disclosure on this **non-admin** route is
`allowed_actions[].condition` — the **sequence-flow routing expression source, verbatim** —
which is precisely the class of leak Decision 5 exists to stop in the 403 arm
(`internal/expreval/expreval.go:135` `%q`) and the 400 `expr` arm
(`definition/model/validate/expr/expr.go:64,68` `%q`). ADR-0186 therefore stops echoing
predicate source on two error paths while leaving it served on a success path it believed
it had covered. `Candidates []authz.Actor` "rendered verbatim as {id, roles, attributes}"
is a second, and `RedactVariables func(map[string]any) map[string]any` is structurally
incapable of touching either.

**Proposed replacement wording:**

> `GetActionableView` (`endpoints.go:72`) renders `runtime/view.ActionableView`. It carries
> **no process or task variables** — `ActionableTask` projects
> `{task_id, node_id, state, claim, candidates, allowed_actions}` and `NewActionableView`
> already clones (`instance_actionable.go:88`). It is therefore **out of D4's variable
> scope**, and the prescribed `TestActionableViewRedactsTaskVars` is **deleted**.
> ⚠ It does disclose two things this bundle does not otherwise bound:
> `allowed_actions[].condition` (the flow's expression source — the same disclosure D5
> removes from the 403 and 400 arms) and `candidates[]` (actor id/roles/attributes
> verbatim, ADR-0147). **Decide explicitly**: either bring them under D4/D5 with a
> `RedactActionableView` hook, or record them as a knowingly accepted, out-of-scope
> disclosure. Silence here is what made the mapper enumeration wrong twice.

---

## C6 — CRITICAL · The snapshot endpoint's disclosure surface is enumerated as **{variables}**; `instanceJSON` also projects token payloads, raw incident error strings, and **the entire process definition** — none reachable by a `func(map[string]any) map[string]any` hook

**Claim as written.**

- Spec `…:50` (§1 problem table): "| the instance read path | variables **aliased not copied**, and **no redaction hook** anywhere | **54** |"
- ADR `0186:310-314` (Decision 4):
  > "`GetInstanceSnapshot` (`endpoints.go:60`) returns the raw `service.ProcessInstance`,
  > **whose JSON projection carries variables verbatim** (`service/instance.go:125`
  > `json:"variables,omitempty"`, assigned at `:344`)"
- ADR `0186:317-318`: "A fix confined to `mapInstance` leaves `GET …/snapshot` returning
  **unredacted process variables**."
- ADR `0186:301`: the hook is `RedactVariables func(map[string]any) map[string]any`.

**Re-derived.** Both citations resolve exactly at `32f4e3e5` (`service/instance.go:125` is
the `Variables map[string]any \`json:"variables,omitempty"\`` field; `:344` is its
assignment in the `instanceJSON` literal). The **net** is the failure: `Variables` is one
field of fourteen in `instanceJSON` (`service/instance.go:117-144`), and the snapshot route
serves all of them:

```
$ sed -n '117,144p' service/instance.go     # instanceJSON
  Variables    map[string]any            `json:"variables,omitempty"`   <- the only one enumerated
  Tokens       []tokenJSON               `json:"tokens,omitempty"`
  History      []nodeVisitJSON           `json:"history,omitempty"`
  Tasks        []taskJSON                `json:"tasks,omitempty"`
  Incidents    []incidentJSON            `json:"incidents,omitempty"`
  Compensating *compensatingJSON         `json:"compensating,omitempty"`
  Definition   *model.ProcessDefinition  `json:"definition,omitempty"`

$ sed -n '146,154p;212,219p' service/instance.go
  tokenJSON.Payload   map[string]any `json:"payload,omitempty"`   <- arbitrary token data
  incidentJSON.Error  string         `json:"error"`               <- the RAW error string of a failed action
```

`Definition` is the whole template, "**EMBEDDED rather than summarized (ADR-0144)**"
(`service/instance.go:110`) — i.e. every gateway/sequence-flow **condition expression
source** in the process, served on the **non-admin** `GET /instances/{id}/snapshot`.

**Verdict: WRONG as an enumeration** (the individual citations are CONFIRMED; the set they
stand for is one of at least four).

**Damage if acted on.** Two compounding failures:

1. **The hook's type cannot express the fix.** `RedactVariables func(map[string]any) map[string]any`
   can redact `variables`. It cannot touch `tokens[].payload`, `incidents[].error`,
   `tasks[]`, or `definition` — and `instanceJSON` is **unexported** with a custom
   `MarshalJSON`, so there is no seam for a consumer to reach them either. Phase 4 ships a
   redaction control that the ADR's own §Context §4 heading ("The instance read path
   aliases, **and discloses**") claims closes backlog 54, while the largest disclosures on
   that exact route remain.
2. **It re-opens D5's own hazard on the success path.** `incidents[].error` is
   `err.Error()` of a failed service action, verbatim — the *same* value D5 blanks at 5xx
   because "callers log the raw error instead of exposing it" (`errors.go:24-25`). And
   `definition` carries the expression sources D5 stops echoing in the 403/400 arms.
   ADR-0186 therefore closes two error-path leaks and leaves the identical data on an
   unauthenticated-by-default success path it enumerated as carrying only "variables".

**Proposed replacement wording** (Decision 4, replacing the "carries variables verbatim"
sentence):

> `GetInstanceSnapshot` (`endpoints.go:60`) returns the raw `service.ProcessInstance`, whose
> `instanceJSON` projection (`service/instance.go:117-144`) carries **five disclosure-bearing
> fields, not one**: `variables` (`:125`, assigned `:344`), `tokens[].payload` (`:151`),
> `incidents[].error` (`:216`, the raw `err.Error()` of a failed action — the value
> `ClassifyError` blanks at 5xx), `tasks[]` (actor id/roles/attributes verbatim, ADR-0147),
> and `definition` — the whole template embedded per ADR-0144, i.e. **every gateway and
> flow condition expression source in the process**.
> ⇒ `RedactVariables func(map[string]any) map[string]any` covers exactly the first.
> **Decide explicitly** whether backlog 54 closes on `variables` alone (then say so, and
> say the other four stay open), or whether the redaction seam must be
> `func(service.ProcessInstance) any` at the response boundary. Do not let Consequences
> claim "the instance read path stops disclosing".
> Note `WithoutEmbeddedDefinition` already exists (`service/instance.go:141`) and is the
> only lever today for the `definition` field; it is a construction-time engine option, not
> a per-response control.

---

## C7 — CRITICAL · `SECURITY.md` will name "**the two** plaintext columns"; there are at least **six**, including `wrkflw_human_task.vars` — the same process variables D4 redacts on the wire

**Claim as written.**

- ADR `0186:117-121` (Context 6): "`wrkflw_instances.snapshot` and `wrkflw_journal.trigger` are
  plaintext `TEXT NOT NULL` (`…/migrations/sqlite/0001_init.sql:25,40`)."
- ADR `0186:396-398` (Decision 6): "`SECURITY.md` says so explicitly, **names the two plaintext
  columns** (`wrkflw_instances.snapshot`, `wrkflw_journal.trigger`), and states what the consumer
  owns (database-level encryption, grants, backup handling)."
- Plan `…:337-338` (phase 9): "`SECURITY.md`: the at-rest posture (D6) **naming
  `wrkflw_instances.snapshot` and `wrkflw_journal.trigger`**."

**Re-derived.** Both cited lines resolve exactly (`0001_init.sql:25` = `snapshot TEXT NOT NULL`,
`:40` = `trigger TEXT NOT NULL`). The **net** is the failure — the grep stopped at two tables of
nine:

```
$ grep -n "CREATE TABLE" internal/persistence/store/migrations/sqlite/0001_init.sql
20:CREATE TABLE wrkflw_instances      36:CREATE TABLE wrkflw_journal
47:CREATE TABLE wrkflw_outbox         64:CREATE TABLE wrkflw_definitions
72:CREATE TABLE wrkflw_processed_message   80:CREATE TABLE wrkflw_call_links
99:CREATE TABLE wrkflw_timers        116:CREATE TABLE wrkflw_chain_links
134:CREATE TABLE wrkflw_human_task
```
Plaintext `TEXT` columns carrying process data, actor data or expression source:

| table.column | what it holds |
|---|---|
| `wrkflw_instances.snapshot` | ✅ named by the ADR |
| `wrkflw_journal.trigger` | ✅ named by the ADR |
| **`wrkflw_human_task.vars`** `TEXT NOT NULL` (`:148`) | **the per-task process-variable snapshot** — the *same* data D4 redacts on the wire |
| **`wrkflw_human_task.candidates`** `TEXT NOT NULL` (`:147`) | resolved actors, `{id, roles, attributes}` verbatim (ADR-0147) |
| **`wrkflw_human_task.eligibility`** `TEXT NOT NULL` (`:146`) | the **attribute-predicate source** |
| **`wrkflw_outbox.payload`** `TEXT NOT NULL` (`:51`) | the domain-event payload |
| **`wrkflw_outbox.last_error`** `TEXT` (`:57`) | raw error strings |
| `wrkflw_definitions` | the definition source, i.e. every condition expression |

Also unstated: the citation is to the **sqlite** dialect only; `migrations/{postgres,mysql}` are
not cited at all (`ls internal/persistence/store/migrations/` → `mysql postgres sqlite`).

**Verdict: WRONG.** "The two plaintext columns" is a sample presented as a class.

**Damage if acted on.** D6's entire justification is that *"Recording 'we do not do this, and here
is why doing it badly is worse' is a decision a consumer can act on. Silence is not."* The artifact
that ships is `SECURITY.md`, and it will name two columns as **the** unprotected set. A consumer
with a regulatory at-rest requirement reads it, applies column-level encryption or restricted
grants to `wrkflw_instances.snapshot` and `wrkflw_journal.trigger`, and leaves the human-task
variable snapshot, the resolved actor attributes, the eligibility predicate and the outbox payload
in the clear — having *followed our documentation*. An incomplete enumeration presented as
exhaustive is strictly worse than the silence D6 rejects, because it converts the consumer's own
audit into a false negative. This is the one decision in the bundle whose deliverable **is** the
enumeration.

**Proposed replacement wording** (Decision 6 + phase 9):

> `SECURITY.md` names the tables that store process data in plaintext rather than a column pair:
> `wrkflw_instances.snapshot`, `wrkflw_journal.trigger`, `wrkflw_outbox.{payload,last_error}`,
> `wrkflw_human_task.{vars,candidates,eligibility}`, and `wrkflw_definitions` — in **all three
> dialects** (`internal/persistence/store/migrations/{postgres,mysql,sqlite}`). ⚠ Phase 9 must
> derive the list from the migration files at implementation time, not copy it from this record;
> and the invariant is worth a test — any new `TEXT` column in these tables is either listed or
> justified.

---

## C8 — CRITICAL · "Three options writing one field is last-writer-wins, **silently**" is FALSE — both existing options **document** last-wins in their godoc, twice. The plan hands the audit a false dichotomy

**Claim as written.**

- ADR `0186:266-273`:
  > "⚠ **`runtime.WithMaxEvalElements` collides with two existing options.**
  > `runtime.WithExpressionTimeout` (`runtime/processdriver_options.go:198`) and
  > `runtime.WithConditionEvaluator` (`:217`) both assign `driver.conditionEval` — the same
  > field. **Three options writing one field is last-writer-wins, silently.** The option must
  > **compose** with a consumer-supplied evaluator (wrap it) or **refuse** the combination at
  > construction with a named error; it must not quietly overwrite."
- Spec `…:120` (§5 interaction table): "A third writer is **silent** last-writer-wins."
- Plan `…:35-37` (§0 item 3) and `…:185-189` (phase 3): "**Compose, or refuse at construction?**
  Spec §5 lists it **open**. Pick one. … **The audit picks; the implementer does not.**"

**Re-derived.** Both line citations are exact (`:198` `func WithExpressionTimeout`, `:217`
`func WithConditionEvaluator`). The word "silently" is not:

```
$ sed -n '196,197p;215,216p' runtime/processdriver_options.go
// WithExpressionTimeout and [WithConditionEvaluator] set the same field; the last
// option wins.
// WithConditionEvaluator and [WithExpressionTimeout] set the same field; the last
// option wins.
```

The collision is **documented in both directions, in the exported godoc of both options**, as a
deliberate contract. `grep -rn conditionEval --include='*.go' . | grep -v _test.go` confirms the
field has exactly the two writers the ADR names, plus the two readers
(`processdriver.go:440` a log attr, `:674` `Evaluator: driver.conditionEval`).

**Verdict: WRONG.** The premise "silently" is refuted by the source it cites; there is a third,
already-established answer the bundle never considers — *last option wins, documented like its
two siblings.*

**Damage if acted on.** The plan makes this the audit's decision and forbids the implementer from
revisiting it, so a wrong premise here is load-bearing:

- If the audit picks **"refuse at construction with a named error"**, then
  `WithExpressionTimeout(5*time.Second)` + `WithMaxEvalElements(10_000)` — a consumer who wants
  *both* a wall-clock guard and an input bound, which is the most obvious combination the two
  decisions invite — becomes a **hard construction error**. That breaks the shipped godoc contract
  on `WithExpressionTimeout`/`WithConditionEvaluator` asymmetrically (those two may still be
  combined; only the new one refuses) and is a **breaking behavioural change absent from the ADR's
  breaking-change list**, which names only `ErrorBody`.
- If the audit picks **"compose"**, the third option behaves unlike the two it joins, and both
  shipped godocs become false the moment it lands — a doc-rot defect the Delivery Gate's item 2
  is supposed to catch and won't, because nothing in the plan points at those two comments.
- Either way `TestOptionCollisionIsNotSilent` (plan `:198`) is a test whose name asserts the
  refuted premise.

**Proposed replacement wording:**

> ⚠ **`runtime.WithMaxEvalElements` joins two existing writers of `driver.conditionEval`.**
> `WithExpressionTimeout` (`:198`) and `WithConditionEvaluator` (`:217`) already share the field,
> and **the collision is documented, not silent** — both godocs say *"set the same field; the last
> option wins"* (`:196-197`, `:215-216`). The default and cheapest resolution is therefore to
> **join the existing contract**: `WithMaxEvalElements` is a third last-wins writer, and all
> **three** godocs are updated to name all three options.
> **Decide explicitly** whether that is acceptable given `WithMaxEvalElements` is the only one of
> the three that is **on by default** (10 000) — a consumer calling `WithExpressionTimeout(d)`
> would silently *lose* the default bound, which is the one genuinely new hazard and is not
> what "silent overwrite" was describing. If it is not acceptable, composing is the answer and the
> two existing godocs must be corrected in the same commit.
> Rename the test `TestExpressionTimeoutDoesNotDiscardTheElementBound`; its falsifier is
> *it fails against a plain last-wins assignment.*

---

## C9 — MAJOR · The spec's anchor claim *"Every citation below was re-derived there"* is false, and *"where a file is volatile, the citation is a symbol"* is not upheld — **four** line citations are off by one at `32f4e3e5`

**Claim as written.** Spec `…:7-8`:

> "- **Anchor:** this bundle's own commit on `design/authz-security-b3`. **Every citation
>   below was re-derived there.** Where a file is volatile, the citation is a **symbol**."

**Re-derived** at `32f4e3e5`:

| cited | as | actual | note |
|---|---|---|---|
| ADR `:311`, D4 draft-placement note | `endpoints.go:124,140,156` | `124, 140, **155**` | `:156` is the closing `}`; `grep -n mapInstance` → `42,52,94,124,140,155` |
| ADR `:303-304` | `seam.go:41` (the default `InstanceMapper`) | `seam.go:**42**` | `:41` is `Wrap: func(r R) R { return r },` |
| ADR `:384` | `seam.go:30-31` (`TracerProvider`/`MeterProvider`) | `seam.go:**31-32**` | `:30` is `Logger *slog.Logger` |
| ADR `:73-74` | `httpcall.go:119-123` ("the hazard **is** documented in the godoc") | `httpcall.go:**120-124**` | `:119` is blank, and the cited range **truncates the sentence that names SSRF** — `"…an allowlist or a restricted *http.Client transport (SSRF risk)."` is on `:124` |

Three are low by one, one high by one, so this is not a uniform commit shift — it is per-citation
slop. Every *other* line citation in the bundle resolves exactly (see the batch table below), which
is what makes the universal quantifier the defect rather than the practice.

**Verdict: the quantifier is WRONG; the practice is 4 bad out of ~30.**

**Damage if acted on.** Low individually, structural collectively. This repo's standing lesson is
*"an audited bundle decays when its base moves — prefer symbol names over line numbers"*, and the
spec asserts compliance it does not have. The `httpcall.go:119-123` case has real content damage:
the ADR's argument that D3 is *"a posture question rather than an oversight"* rests on the hazard
already being documented, and the range it cites as the documentation **excludes the line that says
SSRF**. A reader checking the premise at the cited range does not find it.

**Proposed replacement wording** (and the general fix):

> "**Anchor:** commit `32f4e3e5` on `design/authz-security-b3`. Citations into `transport/http/**`,
> `runtime/**` and `action/**` name a **symbol** (`func`, `var`, struct field); line numbers appear
> only for the two files whose exact ordering is load-bearing — `httpcore/errors.go` (the arms are
> order-dependent) and the SQL migrations."
> Then replace the four: `mapInstance` call sites → *"the six `mapInstance(mapper, …)` returns in
> `endpoints.go`"*; `seam.go:41` → *"`ResolveConfig`'s `InstanceMapper` default"*; `seam.go:30-31`
> → *"`CustomizeConfig.TracerProvider` / `.MeterProvider`"*; `httpcall.go:119-123` → *"`WithURLExpr`'s
> godoc, whose last line reads `…without an allowlist or a restricted *http.Client transport (SSRF risk)`"*.

---

## C10 — CRITICAL · The spec's header and its own §6 state **contradictory** splits of the inherited evidence file: the header says §2 and §6 **hold**; §6 says both are **defective**

**Claim as written.** Spec `…:11-13` (the header, the sentence a reader acts on first):

> "- Inherited evidence (**executed, survived two audits**):
>   `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` — ⚠ **§5 and §7 of that
>   file are known-defective**, see §6 below. **§1–4 and §6 hold.**"

Spec `…:126-146` (§6, "⚠ Known defects in the inherited evidence file"):

> "audit #2 found **three defects** in it…
>  1. **§6 (jsonschema)** — the probe called the vendor directly… **but not** that it is
>     constructible at `ClassifyError`. **It is not.**
>  2. **§2 (the `??` guard form)** — the rows were run with an **empty** `vars` map while
>     sitting under a section declaring `vars = {"tier":"gold"}`… the transcript is mislabelled.
>  3. **§5 and §7** … **defective**.
>  §1 …, §3 … and §4 … hold and were re-confirmed"

**Re-derived.** The header's set of defective sections is `{5,7}`; §6's is `{6,2,5,7}`. The
header's set of holding sections is `{1,2,3,4,6}`; §6's is `{1,3,4}`. **§2 and §6 are asserted
both ways inside one document.** And §6's characterisation is the correct one — verified against
the evidence file itself:

```
$ sed -n '26,29p;51,62p' docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md
## 1. Guard forms that exist in expr v1.17.8
Compiled exactly as internal/expreval does … with vars = {"tier": "gold"}.
## 2. The `??` form does not parse …
(vars.tier ?? "none") == "gold"      out=false  err=<nil>
```
With `tier == "gold"`, `(vars.tier ?? "none") == "gold"` must be **true**; the transcript records
`false` ⇒ the rows were run against an empty env, exactly as §6 says. Evidence §2 is defective.
Evidence §6's probe is likewise self-describing: it renders `*jsonschema.ValidationError` leaves
directly, while `runtime/validation/gate.go:45` is `fmt.Errorf("%w: %s", ErrInvalidInput,
err.Error())` — `%s`, so `errors.As` cannot reach that type downstream. Evidence §6 is defective.

Two further gaps in the same sentence: the caption **"executed, survived two audits"** describes a
file the very next clause says is defective in four of its eight sections; and **§8** of the
evidence file ("What is still NOT executed") is assigned to neither set.

**Verdict: WRONG.** The header is the incorrect half.

**Damage if acted on.** Evidence §6 is the *sole recorded evidence* for the bundle's most load-
bearing executed claim — that a jsonschema 400 body echoes `'123-45-6789'` verbatim, which is what
makes Decision 5 necessary and what phase 2's `TestJSONSchema…WithoutTheSubmittedValue` is written
against. The header tells the next reader **"§6 holds"**. That reader cites §6 for "a value-free
rendering is available at `ClassifyError`" — the exact refuted claim that spec §2's own corrections
table lists as *"NOT IMPLEMENTABLE there"* and that forced phase 2 to exist. This is
verbatim the repo's documented failure mode: *a caption asserting the material is clean, over
material that is not, so the next reader re-derives a refuted claim.* It is also the second-order
version of the round-2 defect the header is trying to warn about.

**Proposed replacement wording** (spec header — make the sets agree, and drop the false caption):

> "- Inherited evidence: `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`.
>   ⚠ **Four of its eight sections are known-defective — §2, §5, §6, §7 — see §6 below for each.**
>   **§1, §3 and §4 hold and were re-confirmed** (they serve the deferred backlog-103 delivery,
>   not this one). §8 is its assumption list, superseded by §7 of this spec.
>   ⚠ **§6 in particular must not be cited for D5**: its probe bypassed `runtime/validation.Gate`,
>   which is why D5's rendering moved into that package."

---

## C11 — MAJOR · The "**80**-character predicate" is unverifiable at this anchor — the predicate is never quoted, the ladder was never re-measured, and `wc -c` counts the trailing newline

**Claim as written.**

- ADR `0186:57-60`: "Measured with an **80-character** predicate (⚠ the draft said 44, three times;
  `wc -c` says 80 — the argument that it is far under a 1e4-node budget is unaffected, which is
  why nobody checked it): 25 ms → 98 ms → 391 ms → 1.563 s at n = 1 000 / 2 000 / 4 000 / 8 000."
- Spec `…:73` (corrections table): "*'the probe predicate is 44 characters'* | ⚠ **80** (`wc -c`)."

**Re-derived — it cannot be.** The predicate text appears **nowhere** in the bundle, and nowhere in
the inherited evidence file, whose §8 states the ladder itself is not re-measured:

```
$ grep -rn "80-character\|44 characters\|80 (`wc -c`)" docs/specs/2026-08-21-*.md docs/adr/0186-*.md docs/plans/2026-08-21-*.md
(only the two assertions above; no predicate source)
$ tail -4 docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md
- The env-cardinality bound's chosen number is extrapolated from the 2026-08-20
  O(n²) ladder, not re-measured here.
```
Two independent problems: (a) a claim of the form *"X is 80 characters"* whose X is not recorded is
**unfalsifiable-as-written** — the very shape Premise Discipline forbids, offered *as a correction
to a previous count*; (b) `wc -c` reports **bytes including the trailing newline**, so an 80-byte
`wc -c` on an echoed string is a **79-character** predicate. The document's own instrument
disagrees with its own unit.

**Verdict: UNFALSIFIABLE-AS-WRITTEN.**

**Damage if acted on.** Direct damage is nil — the ADR says so itself (*"the argument … is
unaffected"*), and that is precisely the danger: it is the class of number nobody re-checks, which
is how it survived at 44 through three documents. Plan §0 item 7 orders the audit to
**re-measure the ladder at n = 10 000**; an auditor cannot reproduce a measurement whose subject is
not written down, so item 7 is unexecutable as briefed and will either be skipped or silently
re-run against a *different* predicate — producing a number that looks like confirmation and is
not. The 10 000 default rests on this ladder.

**Proposed replacement wording:**

> "Measured with the predicate
> `<PASTE THE EXACT SOURCE HERE>` (`len()` = N characters — ⚠ not `wc -c`, which counts the
> newline), against `vars.items` of n JSON integers: 25 ms → 98 ms → 391 ms → 1.563 s at
> n = 1 000 / 2 000 / 4 000 / 8 000 (`ASSUMPTION (unverified)`: inherited from the 2026-08-20 run,
> not re-executed at this anchor — **plan §0 item 7 must re-run it, quoting this predicate**)."

---

## C12 — MINOR · Inherited evidence §7's `expreval.New(` row records **4** for a command that outputs **5**

**Claim as written.** `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md:186`:

> | `expreval.New(` instances | `grep -rn "expreval\.New(" --include='*.go' . \| grep -v _test` | **4** — `authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`, `engine/conditions.go:43`, `runtime/processdriver_options.go:200` |

**Re-derived:**

```
$ grep -rn "expreval\.New(" --include='*.go' . | grep -v _test | wc -l
5
runtime/processdriver_options.go:200 | internal/authz/casbin/authorizer.go:30
authz/authz.go:23 | engine/conditions.go:43
engine/step.go:41:	// timeout-capable evaluator (e.g. expreval.New(expreval.WithTimeout(d)),   <-- a COMMENT
```

**Verdict: the four named call sites are CONFIRMED; the recorded output of the stated command is
WRONG** (5, the fifth being a godoc mention in `engine/step.go:41`).

**Damage if acted on.** Small but exactly the lens's subject: the row is presented as a *re-derived*
enumeration under a preamble promising reproducibility, and it is not reproducible. A reader
re-running it sees 5, cannot tell whether a fifth constructor appeared or the row was wrong, and
has no basis to decide. Relevant to this bundle because D2 changes `expreval`'s constructor and
this is the list of everything that calls it.

**Proposed replacement:** append `| grep -v ':.*//'` to the command, or state
*"**4** constructor calls (a fifth match, `engine/step.go:41`, is inside a godoc)."*

---

## C13 — MINOR · Spec §6 attributes the "274 / 128 / 5" triple to evidence **§7**, which contains only the 274 — and that one reproduces exactly

**Claim as written.** Spec `…:139-142`:

> "3. **§5 and §7** — the tri-state `Open` codec evidence and the **`274/128/5` enumeration**.
>    Both belong to the **identity** delivery and are **defective**; §7's triple was inherited
>    verbatim from audit #1 under a caption claiming nothing was inherited, and **all three numbers
>    are wrong**."

**Re-derived.**

```
$ grep -n "128" docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md   -> no match
$ grep -rn "274 / 128 / 5\|274/128/5" docs/plans/sweep-evidence/
reaudit-b3-counting.md:258: ADR-0185:278-283 … ; :262 spec:576-580 — same 274 / 128 / 5.
$ grep -rn "NewUserTask(" --include='*.go' . | wc -l
274
```
Evidence §7 holds **one** of the three (274). The other two live in **ADR-0185** and the **B3
spec** — documents this delivery has excised. And re-audit #2's own verdict on the 274
(`reaudit-b3-counting.md:76`, R-3) is **MINOR — "274 `NewUserTask(` sites" is 273 call sites +
1 declaration**: the *number* reproduces exactly, only the noun "sites" was wrong.

**Verdict: WRONG** — "§7's triple" and "all three numbers are wrong" are both over-statements of
what §6's own source says.

**Damage if acted on.** This is a *restating-strips-the-hedge* instance in the very section written
to warn about it: re-audit #2 graded one off-by-one label MINOR, and the spec restates it as three
wrong numbers in a defective section. A reader deciding whether any of evidence §7's *other* seven
rows (which include `expreval.New(` — see **C12** — and the `stdlib.Mount` and
`runtime.WithHumanTasks` counts this delivery's phase 9 could reasonably reuse) are usable is told
the whole section is defective, when six of its nine rows re-derive correctly. Over-condemning
evidence is a cheaper failure than over-trusting it, but it is still a false claim about current
state.

**Proposed replacement wording:**

> "3. **§5** (tri-state `Open` codec evidence) is defective and belongs to the identity delivery.
>    **§7** is a re-derived enumeration table for ADR-0185; its `274` row reproduces exactly but is
>    mislabelled ('274 sites' = 273 call sites + the declaration, `reaudit-b3-counting.md` R-3), and
>    its `expreval.New(` row records 4 for a command that outputs 5. The `128` and `5` figures the
>    re-audit refuted (R-9) are in **ADR-0185 and the B3 spec, not in §7**. **None of this material
>    is used here.**"

---

## C14 — MAJOR · D3's SSRF deny-list is a **sample presented as a class**: "cloud metadata addresses" is already inside a range it lists separately, and four standard bypass ranges are unnamed

**Claim as written** (identically in two documents):

- ADR `0186:290-292`: "the default transport refuses loopback, link-local (`169.254.0.0/16`,
  `fe80::/10`), RFC1918/ULA **and cloud metadata addresses** via a `net.Dialer.Control` hook"
- Plan `…:281-283`: "a `net.Dialer.Control` hook refusing loopback, link-local
  (`169.254.0.0/16`, `fe80::/10`), RFC1918/ULA **and cloud metadata**"

**Re-derived** against the ranges themselves (no repo code exists yet — this is an enumeration
review, and `grep -rnE "CheckRedirect|expreval" action/httpcall/` → 0 confirms nothing is
implemented):

- **"cloud metadata addresses" is not a fifth category.** The canonical endpoint —
  `169.254.169.254`, used by AWS IMDS, Azure IMDS and GCP — is **inside `169.254.0.0/16`**, which
  the same sentence already lists. Alibaba's `100.100.100.200` is not, and GCP's
  `metadata.google.internal` is a *name*, not an address, so a `Dialer.Control` hook sees only what
  it resolves to. The phrase therefore names a category that is either redundant or unimplementable
  as written, which is what disguises the gaps.
- **Unnamed, and standard in every SSRF filter list:** `0.0.0.0/8` (and bare `0.0.0.0`, which
  reaches localhost on Linux), `100.64.0.0/10` (CGNAT — Alibaba metadata), `192.0.0.0/24`,
  `198.18.0.0/15`, and **IPv4-mapped IPv6** (`::ffff:127.0.0.1`) — a `net.IP` comparison against
  `127.0.0.0/8` misses the mapped form unless `To4()` is applied first. `::` and `fc00::/7` (ULA)
  is named; `fe80::/10` is named; loopback `::1` is presumably under "loopback".

**Verdict: WRONG as an enumeration** — five named categories standing for an open set, with one of
the five subsumed by another.

**Damage if acted on.** Phase 6's brief is this list, and its four prescribed tests are
`TestURLExprRefusesLinkLocalAddress`, `TestURLExprRefusesRedirectToLoopback`,
`TestBaseURLIsUnrestricted`, `TestAllowedHostsOptsBackIn` — **every one of which passes against an
implementation that blocks only `169.254.0.0/16` and `127.0.0.0/8`**. A consumer then gets a
control the ADR's Consequences describes as *"`httpcall` stops being an SSRF primitive on its
untrusted axis"*, which `http://0.0.0.0:8080/` and `http://[::ffff:127.0.0.1]/` walk straight
through. Because `Dialer.Control` sees the **resolved** address, the correct framing is also
"deny by default over a resolved-IP allow-list", not "deny these five categories".

**Proposed replacement wording:**

> "the default transport refuses any **resolved** address that is not global unicast:
> `net.IP.IsLoopback()`, `IsLinkLocalUnicast()`, `IsLinkLocalMulticast()`,
> `IsInterfaceLocalMulticast()`, `IsUnspecified()`, `IsPrivate()` (RFC1918 + ULA `fc00::/7`), plus
> the explicit ranges Go's helpers do not cover — `100.64.0.0/10`, `192.0.0.0/24`,
> `198.18.0.0/15` — evaluated **after `ip.To4()`** so IPv4-mapped IPv6 (`::ffff:127.0.0.1`) is
> normalised. `169.254.169.254` needs no separate rule: it is link-local. The check runs in
> `net.Dialer.Control`, so it sees the resolved address and DNS rebinding cannot bypass it, and
> `CheckRedirect` re-applies it per hop.
> ⚠ Phase 6 adds table rows for **`0.0.0.0`, `::ffff:127.0.0.1`, `100.64.0.1`** whose falsifier is
> *each fails against an implementation that blocks only `169.254.0.0/16` and `127.0.0.0/8`* — i.e.
> against the four tests currently prescribed."

---

## C15 — MAJOR · Three unlabelled assumptions belong on spec §7 / plan §0, and one entry on §7 is an **orphan** referring to a claim this bundle no longer makes

**Claim as written.** Spec `…:148-165` (§7 "What is still NOT executed") lists four items; plan
`…:23-57` (§0) lists eleven attack points. The lists are offered as the boundary — *"Labelled so
the audit attacks the boundary rather than re-deriving it."*

**Re-derived — asserted elsewhere in the bundle, on neither list:**

1. **"256 KiB of JSON integers admits ~40–50 k elements"** — ADR `:148-149`, `:240-241` (the
   `43 000 → ~45 s` / `50 000 → ~61 s` rows), spec `:76`. This bytes→elements conversion is what
   **refutes** the 256 KiB CPU framing and is quoted as settled fact in the D1×D2 interaction row
   (spec `:118`). Spec §7 labels *"the element-bound extrapolations beyond n = 8 000"* as
   arithmetic-not-measurement, which covers the seconds column but **not** the bytes→elements
   conversion that produces the `n` in the first place. 262 144 / 43 000 = 6.1 bytes per element —
   an assumption about JSON integer width and separator overhead, never executed.
2. **"Measured on this machine: ~84 ns/op … and ~19 µs at the 10 000 default"** — ADR `:255-257`.
   `WithMaxEnvElements` does not exist at `32f4e3e5` (`grep -rn WithMaxEnvElements --include='*.go' .`
   → 0 hits), so this is a measurement of a **prototype counter**, not of the shipped mechanism.
   The parenthetical concedes a second lens got **~52 µs "with a different implementation"** —
   2.7× apart — which is precisely the *"a measured rate is a claim about the MODE it was measured
   in"* hazard. It is presented as measurement and is load-bearing: it is the whole basis for
   "counting per evaluation would be 20–60× worse". (The arithmetic checks: 19 µs / 866 ns = 21.9×,
   52 µs / 866 ns = 60.0×.)
3. **"the correlation id … is the OTel span id when a span is recording"** — ADR `:382-385`. Whether
   a span is recording at the transport seam depends on the **consumer** installing tracing
   middleware; `CustomizeConfig.TracerProvider` being present is not the same as a recording span
   existing. Unstated, and it decides whether `TestCorrelationIDInBodyMatchesTheLogRecord`
   (plan `:245`) exercises the span path or only the random-hex fallback.

**And one orphan.** Spec `…:162-165`:

> "- **Never executed:** the claim that stdlib and gin currently **return 201 for a 256 MiB body**,
>   and its **heap figures**. Inherited from a 2026-08-20 run, never re-derived."

```
$ grep -n "256 MiB\|heap" docs/specs/2026-08-21-*.md docs/adr/0186-*.md docs/plans/2026-08-21-*.md
(no match outside this §7 entry itself)
```
No document in this bundle makes that claim. It is residue from the excised B3 record, and it is
the one §7 entry an auditor would spend time on.

**Verdict: three WRONG omissions, one orphan.**

**Damage if acted on.** Rule #9's brief tells auditors to attack the boundary these lists draw.
Item 2 is the number that decides whether D2 is a mitigation or a regression, and it sits **inside
the Decision text as a measurement** rather than on the assumption list, so no lens is directed at
it — the same placement error that let ADR-0165's inverted predicate through. Item 1 supplies the
`n` for the two largest rows of the table that *refutes* D1's rationale. The orphan spends an
auditor's budget on a claim nobody makes.

**Proposed replacement wording** (spec §7; mirror into plan §0):

> - `ASSUMPTION (unverified)`: **256 KiB ⇒ ~40–50 k elements.** A JSON-integer width estimate
>   (~6 bytes/element incl. separator), not executed. It supplies the `n` for the 43 000 / 50 000
>   rows; the seconds are then arithmetic on the ladder.
> - `ASSUMPTION (unverified)`: **the ~84 ns/op and ~19 µs env-counting costs.** Measured against a
>   *prototype* counter — `WithMaxEnvElements` does not exist at this anchor — and a second
>   implementation measured ~52 µs, 2.7× apart. **Phase 1's benchmark is what settles it**, and
>   D2 is wrong, not the code, if the shipped counter lands outside 19–52 µs at n = 10 000.
> - `ASSUMPTION (unverified)`: **that a recording OTel span exists at the transport seam.** It
>   requires consumer-installed tracing middleware; `CustomizeConfig.TracerProvider` alone does not
>   imply it. Phase 4's correlation-id test must cover **both** the span path and the random-hex
>   fallback.
> - ~~the 201-for-256-MiB claim~~ — **deleted**: no document in this bundle asserts it.

---

## Batch table — remaining counts and citations re-derived and CONFIRMED

| claim | where | re-derived at `32f4e3e5` | verdict |
|---|---|---|---|
| `SECURITY:` caveat at exactly **3** non-test sites, all admin | ADR `:91-94`, plan `:346`, §4 `:366` | `grep -rn "SECURITY:" --include='*.go' .` → `stdlib/groups.go:189`, `gin/groups.go:204`, `fiber/groups.go:209`; all immediately above `AdminRoutes.Customize`; case-insensitive `security` finds no others in `transport/` | ✅ |
| **26** routes = 9 non-admin + 15 admin + 2 health; **no definition-deploy route** | ADR `:126-128`, plan `:368` | 22 unique paths × methods = 26. Non-admin 9 (`/instances`, `/instances/{id}`, `…/snapshot`, `…/actionable`, `…/signals`, `/messages`, `/tasks/{t}/{claim,complete,reassign}`); admin 15 (11 paths, `/admin/policies` and `/admin/role-bindings` ×3 methods); health 2. Identical in all three adapters. No `/definitions` route | ✅ |
| `DefaultMaxNodes = 1e4`; `MaxNodes(0)` **disables** the check | ADR `:51-56`, spec `:69` | `expr@v1.17.8/conf/config.go:18` `DefaultMaxNodes uint = 1e4`, `config.go:51` applies it; `expr.go:221` verbatim: *"If MaxNodes is set to 0, the node budget check is disabled"* | ✅ |
| the O(n²) ladder and every extrapolation | ADR `:60`, `:234-249`, spec `:77` | k = 1.563/8000² = 2.4422e-8. n=1000→24.4 ms (25 ✓), 2000→97.7 ms (98 ✓, "~100 ms" ✓), 4000→390.8 ms (391 ✓), 5000→610 ms ✓, 10000→2.442 s ("~2.4 s" ✓), 43000→45.2 s ✓, 50000→61.1 s ✓. "wrong by ~15×": 610/40 = 15.3, 2442/150 = 16.3 ✓ | ✅ |
| the ctx-path benchmark deltas | ADR `:205`, `:255-258`, spec `:75` | 965.20 − 99.43 = 865.8 ("866 ns" ✓); 965.20/99.43 = 9.71 ("~9.7×" ✓); 19 µs/866 ns = 21.9 and 52 µs/866 ns = 60.0 ("20–60×" ✓) | ✅ arithmetic |
| `expreval.run` synchronous when `timeout <= 0` | ADR `:202` | `internal/expreval/expreval.go:74-76` — `func (e *Evaluator) run`, `if e.timeout <= 0 { return expr.Run(p, env) }` | ✅ |
| 403 eval-error echoes the source once from `%q` at `expreval.go:135` | ADR `:101-102` | `:135` = `fmt.Errorf("workflow-expreval: run %q: %w", code, err)` | ✅ |
| `engine/conditions.go:43` `WithTimeout(0)`; `:29-43` states the invariant | ADR `:64`, `:196-201`, spec `:70`, `:75` | `:43` = `var conditions = expreval.New(expreval.WithTimeout(0))`; `:29-42` is the comment, quoted **verbatim** incl. "SIDE-EFFECT-FREE" and "TRADING THE DETERMINISTIC-REPLAY GUARANTEE" | ✅ |
| `expr` strategy `%q`s the predicate source at `expr.go:64,68` | ADR `:365`, plan `:150`, `:164` | `:64` `predicate %q: %w`; `:68` `predicate %q not satisfied` — both on `v.source[i]` | ✅ |
| `gate.go:45` is `fmt.Errorf("%w: %s", …)` — `%s`, not `%w` | ADR `:348-350`, spec `:78`, plan `:140` | exact | ✅ |
| **4** validation strategies: `jsonschema`, `expr`, `avro`, `callback` | ADR `:361-363`, plan `:365` | `ls definition/model/validate/` → exactly those four adapter dirs | ✅ *as the in-repo set* — ⚠ the class is **open** (`validate.Register(kind, factory)` is exported and `callback` takes an arbitrary `fn`), so the allow-list must key on **kind**, defaulting unknown kinds to static. The bundle never says this; D5's deny-by-default makes it safe, but "the other three strategies" (ADR `:430`) is a closed-set phrasing over an open set |
| `service/instance.go:125` / `:344` (`variables` field + assignment) | ADR `:312-313` | exact | ✅ (see **C6** for the net) |
| `endpoints.go:60` `GetInstanceSnapshot`, `:72` `GetActionableView`, both mapper-less and non-admin | ADR `:310-316`, spec `:80` | exact; both registered outside `AdminRoutes` | ✅ |
| `AdminListInstances` clean, `admin_endpoints.go:81-95` | ADR `:321` | builds `instanceSummaryView` with no variables | ✅ (but see **C4** — three *other* admin endpoints are not) |
| `caching_instance_store.go:73-76` → `State.Clone()`; `cloneState` → `copyVars` | ADR `:83-85`, spec `:74` | `persistence/caching_instance_store.go:74-76` is `cloneInstanceEntry` returning `e.State.Clone()`; `engine/step_state.go:362` `s.Variables = copyVars(st.Variables)` (func opens `:360`, cited range `:361-363` contains it) | ✅ |
| `view.go:31` aliases (`Variables: st.Variables`) | ADR `:78-79`, plan `:214` | exact | ✅ |
| `httpcall.go:125-134` `WithURLExpr` calls raw `expr.Compile`; default client `&http.Client{Timeout: 30s}`; `grep -rnE "CheckRedirect\|expreval" action/httpcall/` → 0 | ADR `:68-73` | `:125-134` exact; `:209` `&http.Client{Timeout: 30 * time.Second}`; grep exits 1 (**bare `\|` here, correct**) | ✅ |
| `grep -rniE "encrypt\|redact"` over `persistence/`, `internal/persistence/`, `engine/` → 0 | ADR `:118-119` | exits 1 (bare `\|`, correct) | ✅ |
| `wrkflw_journal` is **6** columns, no hash/prev-hash/signature | ADR `:122-123` | `instance_id, seq, kind, trigger, occurred_at, applied_at` + composite PK | ✅ |
| `runtime/processdriver_options.go:198` / `:217` | ADR `:267-268`, plan `:186-187` | exact | ✅ (but see **C8** on "silently") |
| `CustomizeConfig.Logger` godoc: *"receives 5xx raw error details (never sent to clients)"* | ADR `:385-386` | `seam.go:29`, verbatim | ✅ |
| fiber's `c.Bind().JSON` is the live idiom; `BodyParser` has 0 hits | spec `:71` | confirmed | ✅ |

---

# Summary

| ID | severity | one line |
|---|---|---|
| **C1** | **CRITICAL** | *"every one of the 39 decode sites already wraps in `ErrBadInput`"* — **3 discard the decode error to `_`**; the body cap ships silently unenforced on the incident-resolve route in all three adapters |
| **C4** | **CRITICAL** | the redaction-covered set is **6 + 2**; source has **6 + 2 + 3** — `ResolveIncident`, `CancelInstance`, `ResolveCompensationStall` call `NewInstanceView` directly and keep returning raw variables |
| **C5** | **CRITICAL** | `TestActionableViewRedactsTaskVars` **cannot be written** — `ActionableView` renders no task vars; what it *does* leak (flow `condition` source, actor attributes) is enumerated nowhere |
| **C6** | **CRITICAL** | the snapshot endpoint's disclosure surface is enumerated as `{variables}`; `instanceJSON` also ships `tokens[].payload`, `incidents[].error`, `tasks[]` and **the whole definition** — unreachable by a `func(map[string]any) map[string]any` hook |
| **C7** | **CRITICAL** | `SECURITY.md` will name "**the two** plaintext columns"; there are **≥ 6**, incl. `wrkflw_human_task.vars` — the consumer-facing artifact of D6 is the enumeration, and it is short |
| **C8** | **CRITICAL** | *"last-writer-wins, **silently**"* — both existing options **document** last-wins in their godoc, twice; the plan's compose-or-refuse dichotomy omits the shipped third answer and "refuse" is an unlisted breaking change |
| **C10** | **CRITICAL** | the spec's header and its own §6 give **contradictory** splits of the inherited evidence file — the header says §2 and §6 hold; §6 says both are defective, and §6 is the sole evidence for D5's premise |
| **C2** | MAJOR | the plan's "0 existing caps" grep is **still vacuous** — `-E` added, BRE `\|` left in, so it matches a literal string and returns 0 for any repo |
| **C3** | MAJOR | the ADR **banner** says the 400 arm carries **nine** sentinels; its own Decision 5, its Consequences, the plan and the source all say **eight** |
| **C9** | MAJOR | *"Every citation below was re-derived there"* is false — **4** line citations are off by one, incl. one that truncates the sentence naming SSRF |
| **C11** | MAJOR | the "**80**-character predicate" is unverifiable — the predicate is never quoted, so plan §0 item 7 (re-measure at n = 10 000) is unexecutable as briefed |
| **C14** | MAJOR | D3's SSRF list is a sample-as-class — "cloud metadata" is inside a range already listed, and `0.0.0.0/8`, `100.64.0.0/10`, IPv4-mapped IPv6 are unnamed; **all four prescribed tests pass against a two-range implementation** |
| **C15** | MAJOR | three unlabelled assumptions (256 KiB⇒40–50 k elements; the ~84 ns/~19 µs prototype measurement; "a recording span exists") belong on §7/§0, and one §7 entry is an orphan |
| **C12** | MINOR | inherited evidence §7 records **4** for an `expreval.New(` command that outputs **5** |
| **C13** | MINOR | spec §6 attributes the "274/128/5" triple to evidence §7, which holds only the 274 — and that number reproduces exactly |

**Totals: 7 Critical, 6 Major, 2 Minor.**

## Ranking by damage if acted on

1. **C7** — ships a *false security statement to consumers*. `SECURITY.md` is the deliverable; a
   consumer follows it, encrypts two columns, and leaves the human-task variable snapshot and the
   outbox payload in plaintext. Worst because the damage lands outside the repo and is invisible here.
2. **C6** — closes backlog 54 while `GET /instances/{id}/snapshot` keeps serving the raw incident
   error strings and the entire definition's expression source. The hook's *type* cannot express the
   fix, so this is a design change, not a test addition.
3. **C4** — three admin endpoints keep returning unredacted variables after D4 ships, and the
   bundle's own six prescribed tests cannot see it. Same shape as the defect the ADR is proud of
   having caught one level up.
4. **C8** — an audit decision the plan forbids the implementer from revisiting, made on a premise
   the cited source refutes; the "refuse" branch is an unlisted breaking change that makes
   `WithExpressionTimeout` + `WithMaxEvalElements` a construction error.
5. **C1** — the body cap is silently unenforced on 3 of 39 sites; the prescribed test stays green.
6. **C10** — routes the next reader to the one evidence section that cannot support the claim it
   would be cited for.
7. **C14** — an SSRF control that four green tests certify and `http://0.0.0.0/` walks through.
8. **C5** — one of two "controls that decide D4's placement" is unwritable, so D4's placement
   evidence halves; plus an unenumerated expression-source leak on a non-admin success path.
9. **C3**, **C11**, **C9**, **C15**, **C2**, **C12**, **C13** — documentation-integrity defects.
   **C11** ranks highest of these because it makes a prescribed audit action unexecutable.

## What this lens checked and found clean

Every sum and every ratio in the bundle is **arithmetically correct** — the ladder extrapolations,
the 866 ns delta, the 9.7×, the 20–60×, the 15× refutation, 13+13+13+0 = 39, 9+15+2 = 26,
8 sentinels − 1 = 7 non-validation. Consistent with the standing lesson: *the failure is never the
arithmetic; it is the net and the anchor.* Six of the seven Criticals above are **net** failures
(`grep mapInstance` blind to `NewInstanceView`; `grep '%w: %w'` blind to `_ =`; two tables of nine;
one field of fourteen); one (**C10**) is an internal contradiction, and the Majors split between
net (**C14**), anchor (**C9**) and cross-document disagreement (**C3**).
