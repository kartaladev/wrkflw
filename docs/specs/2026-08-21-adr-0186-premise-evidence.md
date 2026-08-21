# ADR-0186 — executed premise evidence (the REVISION's own, not inherited)

- Date: 2026-08-21
- Anchor: the ADR-0186 bundle commit on `design/authz-security-b3` (docs-only; do not quote
  the SHA — it is amended on every revision).
- Machine: darwin/arm64, Apple M4 Pro, Go 1.26.6. No `-race` unless stated. Container-free.
- **Why this file exists.** The revision folding audit #1 changes five of six decisions. Every
  claim about current behaviour that those changes rest on is executed **here**, by the author,
  before the re-audit — not reasoned from source, and not inherited.

⚠ **This file supersedes nothing.** The B3-era
`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` is a **different delivery's** evidence
and four of its eight sections are defective (spec §6). Nothing in this record is copied from it.

---

## 1. `httpcore.Validate`'s 400 message is VALUE-FREE — the premise under D5's narrowed deny-list

**Why load-bearing.** Audit finding F4 (the static-400 default destroys actionable messages three
prior ADRs deliberately added) is accepted only if `ErrBadInput` — the highest-volume 400, carrying
every DTO on all 26 routes — is provably value-free. If it echoes submitted values, the blanket
blanking audit #1 attacked was right after all.

**Probe** (throwaway `transport/http/httpcore/zzprobe_fold_test.go`, deleted after; a DTO with a
`max=3` string tag fed an 11-character value and a `numeric` tag fed `123-45-6789`):

```
=== RUN   TestProbeValidatorEchoesValue
    VALIDATE ERR = workflow-httpcore: bad input: Key: 'probeDTO.name' Error:Field validation for 'name' failed on the 'max' tag
        Key: 'probeDTO.ssn' Error:Field validation for 'ssn' failed on the 'numeric' tag
--- PASS
```

**Result: value-free, and value-free even for a LENGTH constraint.** `go-playground/validator`
renders `Key: '<Struct>.<jsonName>' Error:Field validation for '<jsonName>' failed on the '<tag>'
tag`. Neither the submitted string nor its length appears — `max=3` against an 11-character value
reports the tag, not `got 11`. (Contrast jsonschema's `maxLength` leaf in §2, which does report the
length.)

⇒ D5 keeps `err.Error()` for `ErrBadInput`. The two `ErrBadInput` wrap sites that DO embed a caller
value are named in §4 and are edited instead.

---

## 2. `jsonschema`: `keywordLocation` is value-free by construction; `instanceLocation` and the vendor's error text are NOT

**Why load-bearing.** Audit finding E4 refuted the bundle's prescribed "value-free" rendering:
`InstanceLocation` is *instance*-derived, so a secret submitted as an object **key** renders
verbatim. D5's replacement rendering must be checked, not assumed — this is the third consecutive
round in which a rendering believed value-free was not.

**Probe** (throwaway `definition/model/validate/jsonschema/zzprobe_fold_test.go`, deleted after;
the **real** in-repo strategy via `vjs.New(schema).NewValidator()`, then
`errors.As` → `*jsonschema.ValidationError` → `BasicOutput().Errors`):

```
=== RUN   TestProbeBasicOutputKeywordLocation
### closed-properties/pattern  errors.As=true
    instanceLocation="/name" keywordLocation="/properties/name/maxLength" error="maxLength: got 9, want 3"
    instanceLocation="/ssn"  keywordLocation="/properties/ssn/pattern"    error="'123-45-6789' does not match pattern '^[0-9]{3}$'"
### caller-chosen-key/propertyNames  errors.As=true
    instanceLocation="/4111-1111-1111-1111" keywordLocation="/additionalProperties/type" error="got string, want number"
    instanceLocation=""                     keywordLocation="/propertyNames/propertyNames" error="invalid propertyName '4111-1111-1111-1111'"
    instanceLocation=""                     keywordLocation="/propertyNames/maxLength"     error="maxLength: got 19, want 8"
### array-item  errors.As=true
    instanceLocation="/items/1" keywordLocation="/properties/items/items/type" error="got string, want integer"
--- PASS
```

**Results, one per column:**

| column | verdict |
|---|---|
| `keywordLocation` | **value-free in all three shapes.** It is a JSON pointer into the *schema*, which is author-supplied: `/properties/ssn/pattern`, `/additionalProperties/type`, `/propertyNames/maxLength`, `/properties/items/items/type`. The caller's card number never appears in it. |
| `instanceLocation` | **leaks.** `/4111-1111-1111-1111` — E4 reproduced independently. |
| `Error.String()` (the vendor's text) | **leaks twice over**: the submitted value (`'123-45-6789'`, `'4111-1111-1111-1111'`) *and* lengths (`got 9, want 3`, `got 19, want 8`). |

⇒ D5 renders **`keywordLocation` only**. Not the instance location, and not the vendor's message
text. `errors.As` before the gate is `true` (audit H2, reproduced by two lenses); the gate flattens
it, which is why the rendering lives in `runtime/validation`.

⚠ **Ergonomics cost, measured rather than asserted.** For a closed-`properties` schema
`keywordLocation` still names the field *and* the constraint (`/properties/ssn/pattern`), so nothing
is lost. For an **array** it loses the index: `/properties/items/items/type` does not say *which*
item failed, where `instanceLocation` said `/items/1`. That is the price of value-freedom by
construction, and D5 states it.

---

## 3. `State.Clone()` is SHALLOW: a redaction hook deleting a NESTED key mutates the source

**Why load-bearing.** Audit finding I-11. D4 prescribed a shallow copy (sized for the *aliasing*
defect at `view.go:31`) and separately added a **mutation hook**. Nobody had derived what the hook
does through that copy. Re-executed here rather than restated, per the inherited-claim rule.

**Probe** (throwaway `engine/zzprobe_fold_test.go`, deleted after; the real `engine.InstanceState.Clone()`):

```
=== RUN   TestProbeNestedRedactionMutatesSource
    after nested delete on the CLONE, SOURCE applicant = map[string]interface {}{"name":"ada"}
    top-level delete isolation: source still has 'tags' = true
--- PASS
```

**Result: CONFIRMED, and the split is exactly nested-vs-top-level.** `delete(clone.Variables["applicant"].(map[string]any), "ssn")` removed `ssn` from the **source**;
`delete(clone.Variables, "tags")` did not. Source: `engine/step_state.go:325`
`func copyVars(in map[string]any) map[string]any { return maps.Clone(in) }` — `maps.Clone` is
shallow, and `State.Clone()` → `cloneState` → `copyVars` is the whole chain.

⇒ Two consequences D4 now carries:
1. The map handed to `RedactVariables` is a **JSON-shaped deep copy**, not `maps.Clone`'s result.
2. `persistence/caching_instance_store.go:72` — *"cloneInstanceEntry **deep-copies** an entry so
   cached live values … can never be aliased by a caller"* — is a **false comment in shipped code**,
   reachable from this bundle's diff. Delivery-gate item 2 requires killing it.

⚠ This also re-opens, for nested values only, the claim ADR-0186 §4 withdrew
(*"anything mutating the view mutates instance state"*). The withdrawal was correct for the
top-level case and is **wrong for the nested case**, and the withdrawal is what removed the reason
anyone would check. Recorded in D4.

---

## 4. Enumerations re-derived at this anchor (the audit found three wrong; a fourth was wrong in the audit itself)

Every count below was re-run in the working tree at the bundle commit. Commands are quoted so the
next reader can re-run them — ⚠ with **bare `|` under `-E`**: `\|` in ERE is a *literal* pipe, which
is how the previous round's "0 existing caps" evidence became unfalsifiable (audit finding C2).

### 4.1 Decode sites — **36 propagate, 3 discard**, not 39/39

```
$ for a in stdlib gin fiber; do printf "%s " $a; done            # idiom per adapter
stdlib 13 (json.NewDecoder)   gin 13 (ShouldBindJSON)   fiber 13 (c.Bind())        = 39
$ grep -rnE "_ = (gc\.ShouldBindJSON|c\.Bind\(\)\.JSON|json\.NewDecoder)" transport/http/*/groups.go
transport/http/fiber/groups.go:255:    _ = c.Bind().JSON(&in)
transport/http/stdlib/groups.go:238:   _ = json.NewDecoder(req.Body).Decode(&in) // body is optional
transport/http/gin/groups.go:265:      _ = gc.ShouldBindJSON(&in)
```

All three are the **same route** — `POST /admin/instances/{id}/incidents/{incidentID}/resolve`,
whose body is deliberately optional. At those three there is **no error path to convert**, so
"convert the existing wrap to a bare sentinel" has nothing to convert and an oversize body is
silently swallowed into a 2xx.

### 4.2 Response paths that project `InstanceState.Variables` — **11**, not 8

```
$ grep -rn "mapInstance(" --include='*.go' transport/http/httpcore/ | grep -v _test
endpoints.go:15  (the definition)
endpoints.go:42, 52, 94, 124, 140, 155                                    <- 6 call sites
$ grep -rn "NewInstanceView(" --include='*.go' . | grep -v _test.go
httpcore/seam.go:42, :54        (the default InstanceMapper)
httpcore/endpoints.go:17        (mapInstance's nil-mapper default)
httpcore/view.go:23             (the definition)
httpcore/admin_endpoints.go:111 <- ResolveIncident          } three DIRECT admin
httpcore/admin_endpoints.go:121 <- CancelInstance           } callers that take
httpcore/admin_endpoints.go:514 <- ResolveCompensationStall } no mapper at all
```

**6** `mapInstance` + **3** direct admin `NewInstanceView` + **2** mapper-less non-admin
(`GetInstanceSnapshot` `endpoints.go:60`, `GetActionableView` `:72`) = **11**.
`AdminListInstances` (`admin_endpoints.go:81-95`) projects `instanceSummaryView` and is genuinely
clean — it is one admin endpoint of four, and the bundle generalised from it.

⚠ The failure was the **net**: `grep mapInstance` is blind to `NewInstanceView`.

### 4.3 `ActionableTask` has **no** `Vars` field — the prescribed test cannot be written

```
$ sed -n '22,42p' runtime/view/instance_actionable.go
type ActionableTask struct {
    TaskID  string            `json:"task_id"`
    NodeID  string            `json:"node_id"`
    State   string            `json:"state"`
    Claim   *humantask.Claim  `json:"claim,omitempty"`
    Candidates []authz.Actor  `json:"candidates,omitempty"`   // "verbatim as {id, roles, attributes} (ADR-0147)"
    AllowedActions []NextAction `json:"allowed_actions,omitempty"`
}
```

Six fields, no `Vars`, and `NewActionableView` never reads `t.Vars`. An auditor's executed probe
with a fixture setting `HumanTask.Vars = {"ssn":"123-45-6789"}` produced JSON containing no such
string. ⇒ `TestActionableViewRedactsTaskVars` is **deleted**, and D4's `HumanTask.Vars` sentence
with it.

What the route *does* disclose, on a **non-admin** path:
`allowed_actions[].condition` — the sequence-flow **expression source**, verbatim — and
`candidates[]` — actor id/roles/attributes. Neither is reachable by a
`func(map[string]any) map[string]any` hook.

### 4.4 Plaintext columns at rest — ⛔ **SUPERSEDED BY §6.1. THIS SECTION IS WRONG.**

⛔⛔ **Do not cite this section.** Its "twelve columns across seven tables" is the **third**
consecutive rot of this enumeration (2 → "at least six" → 12 → the real figure), and it was
written inside a paragraph warning that the enumeration rots. Two defects, both confirmed:

1. **The sentence counted its own markdown ROWS, not columns.** The table below has 12 rows but
   two of them brace-collapse multiple columns (`{claim_actor, completion_actor, note}` is three,
   `{output, error}` is two), so the table itself already lists **15** columns.
2. **It names the actor *remainder* and omits the actor *identifier*** — `claimed_by` /
   `completed_by` hold the id, split out for indexing — plus `outcome`.

⇒ **§6.1 replaces this with a machine derivation over all three dialect files**, and the
delivery's deliverable becomes the *generator + invariant*, not a number in prose. The table
below is retained only so the diff between the hand count and the machine count is legible.

⚠ **The audit said "at least six"; re-derived from the migrations it is twelve.** The counting
lens read `wrkflw_instances`, `wrkflw_journal`, `wrkflw_outbox`, `wrkflw_human_task` and
`wrkflw_definitions` and stopped; `wrkflw_call_links`, `wrkflw_timers` and `wrkflw_chain_links`
carry process data too. Recorded because D6's **deliverable is the enumeration** — a short list
presented as exhaustive is worse than the silence D6 rejects.

| table.column | what it holds |
|---|---|
| `wrkflw_instances.snapshot` | the whole instance state, incl. every process variable |
| `wrkflw_journal.trigger` | trigger payloads (signal/message/completion values) |
| `wrkflw_outbox.payload` | domain-event payloads |
| `wrkflw_outbox.last_error` | raw error strings from failed publishes |
| `wrkflw_definitions.definition` | the definition source — every gateway/flow condition expression |
| `wrkflw_human_task.vars` | the per-task process-variable snapshot |
| `wrkflw_human_task.candidates` | resolved actors, `{id, roles, attributes}` verbatim |
| `wrkflw_human_task.eligibility` | the attribute-**predicate source** |
| `wrkflw_human_task.{claim_actor, completion_actor, note}` | actor records + a free-text completion note |
| `wrkflw_call_links.{output, error}` | a child instance's output map and error string |
| `wrkflw_timers.trigger_payload` | the payload a timer will apply |
| `wrkflw_chain_links.start_vars` | the successor instance's start variables |

Types per dialect: `TEXT` in sqlite, `JSONB`/`TEXT` in postgres, the mysql equivalent — plaintext in
all three. ⚠ The bundle previously cited **only** `migrations/sqlite/0001_init.sql`; there are three
dialect directories.

### 4.5 `action/httpcall.ErrBodyTooLarge` already exists, and means **500**

```
$ grep -n "ErrBodyTooLarge" action/httpcall/httpcall.go
:92-94  var ErrBodyTooLarge = errors.New("workflow-httpcall: body exceeds max size")
```

Exported module-root API, returned when an **outbound response** exceeds `WithMaxResponseSize`
(default **10 MiB**). Opposite direction from the sentinel D1 mints, and it correctly falls to
`ClassifyError`'s default **500**. ⇒ D1's sentinel is named `ErrRequestBodyTooLarge`, and the
existing 10 MiB response cap is acknowledged as prior art for D1's 1 MiB judgement call.

### 4.6 The four caller-supplied variable-map admission sites in `service`

The seam D2's admission bound acts on. Closed set, from the request types:

```
$ grep -n "map\[string\]any" service/request.go
19:  Vars    map[string]any   // StartInstanceRequest
30:  Payload map[string]any   // DeliverSignalRequest
44:  Payload map[string]any   // DeliverMessageRequest
72:  Output  map[string]any   // CompleteTaskRequest
```

**Four, and only four.** No other `service` request type carries a `map[string]any`.

### 4.7 The two `ErrBadInput` wrap sites that embed a caller value

Everything else under `ErrBadInput` is value-free (§1). These two are not, and D5 edits them
rather than blanking the sentinel:

```
transport/http/httpcore/admin_endpoints.go:30   fmt.Errorf("%w: unknown status %q", ErrBadInput, s)
transport/http/httpcore/dto.go:174              fmt.Errorf("%w: disposition must be one of retry, skip, abandon (got %q)", ErrBadInput, s)
```

Both echo a short caller-chosen enum token. Both become value-free by naming the *allowed* set
instead of the rejected input.

---

## 5. What is NOT executed here — labelled, so the re-audit attacks the boundary

- `ASSUMPTION (unverified)`: **the 1 MiB body default** and the **256 KiB variable-byte default**.
  Judgement calls with nothing behind them. §4.5 gives one datapoint (`httpcall`'s own 10 MiB
  response cap) that argues 256 KiB is *low* relative to a first-party action's output.
- `ASSUMPTION (unverified)`: **256 KiB ⇒ ~40–50 k JSON integer elements** (~6 bytes/element with
  separators). Never executed; it supplies the `n` for D2's two largest table rows. An auditor's
  probe measured the exact figure at the defaults as **45 540**, which corroborates the magnitude
  but was computed for a specific element shape.
- `ASSUMPTION (unverified)`: **that a recording OTel span exists at the transport seam.** It
  requires consumer-installed tracing middleware; `CustomizeConfig.TracerProvider` being present
  does not imply it. D5's correlation-id fallback is what makes this non-fatal, and the tests must
  cover **both** legs.
- **Not re-measured here, but measured twice by auditors** (recorded so it is not re-derived a
  fourth time): the O(n²) ladder and the n = 10 000 default — **2.458 s** (execution lens, predicted
  2.442 s, 0.65 % error) and **1.92 s** (failure-modes lens, a ~25 % faster run of the same shape).
  ⚠ Both are plain-mode; `-race` is several times slower. The default's latency claim is a claim
  about **plain mode on an Apple M4 Pro**.
- **Not executed:** the mysql and postgres migration column types were read, not exercised. §4.4's
  claim is "plaintext in all three dialects" from the DDL, which is sufficient for a `SECURITY.md`
  statement but is not a round-trip test.

---

# 6. THE RE-CUT'S OWN EVIDENCE (2026-08-21, after the fold's re-audit failed)

**Why this section exists.** The owner re-cut ADR-0186 into four single-decision deliveries after
its second failed audit. This slice keeps **D1** (body caps), **D5** (what a 4xx body may say) and
**D6** (at-rest posture). Every claim the re-cut *changes* is executed here, by the author, before
the audit. ⚠ **Two of the three results below correct the RE-AUDIT, not the bundle** — an audit
finding is a claim like any other and Premise Discipline's "re-verify claims you inherit" applies
to it.

## 6.1 The at-rest enumeration, derived BY MACHINE — and a per-dialect divergence no lens found

**Why load-bearing.** D6's *deliverable is the enumeration*. It has now rotted three times, the
third time inside the paragraph warning that it rots. The re-audit's prescription was to derive it
from the migration files by machine rather than by hand; that is what this is.

**Probe** (throwaway `python3` walk over
`internal/persistence/store/migrations/{postgres,mysql,sqlite}/0001_init.sql`, parsing every
`CREATE TABLE` and classifying each column by declared type; deleted after, output kept):

```
TABLES per dialect: {'postgres': 9, 'mysql': 9, 'sqlite': 9}
  table set identical across all three: True
  !! COLUMN DIVERGENCE in wrkflw_journal
     postgres: [applied_at, instance_id, kind, occurred_at, seq, trigger]
     mysql:    [applied_at, instance_id, kind, occurred_at, seq, trigger_]
     sqlite:   [applied_at, instance_id, kind, occurred_at, seq, trigger]

PAYLOAD-TYPED COLUMNS (postgres types shown):
  wrkflw_call_links (8): child_instance_id, parent_instance_id, parent_command_id,
                         parent_def_id, status, output:JSONB, error, claimed_by
  wrkflw_chain_links (6): predecessor_instance_id, outcome, successor_instance_id,
                          predecessor_definition_ref, successor_definition_ref, start_vars:JSONB
  wrkflw_definitions (2): def_id, definition:JSONB
  wrkflw_human_task (13): task_id, instance_id, node_id, state, claimed_by, claim_actor:JSONB,
                          completed_by, outcome, note, completion_actor:JSONB,
                          eligibility:JSONB, candidates:JSONB, vars:JSONB
  wrkflw_instances (3): instance_id, def_id, snapshot:JSONB
  wrkflw_journal (3): instance_id, kind, trigger:JSONB
  wrkflw_outbox (7): instance_id, topic, payload:JSONB, dedup_key, status, last_error,
                     definition_ref
  wrkflw_processed_message (2): subscriber, message_id
  wrkflw_timers (4): instance_id, timer_id, def_id, trigger_payload:JSONB

TOTAL payload-typed columns: 48 across 9 tables
TOTAL tables in schema: 9
```

**Result 1 — the schema is 9 tables, and the table set is identical across all three dialects.**
Every prior count in this lineage (7, then 8) was short because it was a hand list.

**Result 2 — ⭐ `wrkflw_journal.trigger` is named `trigger_` in MySQL ONLY.** Confirmed by reading
the DDL directly (`mysql/0001_init.sql:31` vs `postgres/0001_init.sql:30`): `TRIGGER` is a MySQL
reserved word. **Four Opus lenses across two audit rounds did not report this**, because every
round enumerated *columns* from one dialect file and assumed the other two matched.

⚠⚠ **This is a defect in D6's deliverable, not a curiosity.** D6 ships a list a consumer applies
column-level encryption or grants from. A consumer following a single-dialect list encrypts
`wrkflw_journal.trigger` and, on MySQL, **encrypts nothing** — the column does not exist under that
name. The failure is silent: the `ALTER`/grant targets a name that is absent, and the operator's
own audit reports success against the wrong schema.

**Result 3 — a raw count of "payload-typed columns" is the wrong deliverable.** 48 of them are
`TEXT`/`JSON`, but most are identifiers (`instance_id`, `def_id`, `task_id`, `topic`, `dedup_key`).
The security-relevant set is *columns carrying caller-, actor- or process-supplied data*, and that
classification is a judgement no regex makes.

⇒ **The decision this forces:** D6's deliverable is **not a number and not a prose list**. It is a
**generator plus an invariant** — a test that parses all three migration files and asserts every
column of every table is classified either `discloses` (and therefore appears in `SECURITY.md`) or
`structural` (with a stated reason). A new column, or a new per-dialect name divergence, **fails
the test**. This is the `engine/terminal_sites_test.go` pattern the repo already uses for exactly
this class, and it is the only thing that has ever stopped an enumeration in this repo from
rotting. See §6.4 on why the prose warning did not.

## 6.2 `ErrBadCursor` DOES echo caller values — through TWO channels, not one

**Why load-bearing.** D5's exception list keeps `err.Error()` for
`kernel.ErrBadCursor` / `kernel.ErrBadArmedTimerCursor` on the stated ground that their messages are
`": not an instance cursor"` / `": cursor carries no start time"` — *"no caller value"*. The
re-audit (C5) reported one echo channel. **Both the bundle and the re-audit are wrong about the
count.**

**Probe** (throwaway `runtime/kernel/zz_probe_test.go`, deleted after — a caller-supplied base64
cursor fed to the real exported `DecodeCursor`):

```
payload={"ssn-123-45-6789":1}
  -> workflow-runtime: malformed instance cursor: json: unknown field "ssn-123-45-6789"

payload={"kind":"instance","instance_id":"x","started_at":"4111-1111-1111-1111"}
  -> workflow-runtime: malformed instance cursor: parsing time "4111-1111-1111-1111" as
     "2006-01-02T15:04:05Z07:00": cannot parse "11-1111-1111" as "-"

payload={"kind":"instance"} trailing-4111111111111111
  -> workflow-runtime: malformed instance cursor: trailing data after cursor payload:
     invalid character 'a' in literal true (expecting 'u')
```

**Result: two distinct echo channels.**
1. **The caller-supplied field NAME, verbatim** — `decodeCursorInto` sets `DisallowUnknownFields()`
   (`cursorcodec.go:44`), and `encoding/json` renders the unknown key in the error.
2. **The caller-supplied VALUE, verbatim** — a malformed `started_at` reaches `time.Parse`, which
   quotes the input.

**Why the bundle missed it:** `ErrBadCursor` has **four** wrap forms (`lister.go:66,69,77,90`). The
ADR's exception-list row quoted **two of the four** — the two static ones — and generalised. The
echoing form is `lister.go:66`, `fmt.Errorf("%w: %w", ErrBadCursor, err)`, which wraps a decoder
error computed over **caller-controlled bytes**. `armed_timer_paging.go:89` is the identical shape.

## 6.3 The `ErrBadInput` decode wraps DO echo caller values — the re-audit's Critical #4 is CORRECT, and my first attempt to refute it was itself a stand-in probe

> ⚠⚠⚠ **READ THIS SECTION'S HISTORY, IT IS THE POINT.** My first pass at §6.3 concluded the
> opposite — *"value-free in all three adapters, the re-audit's Critical #4 is half wrong"* — and
> that conclusion was **false**. It was produced by probing **type-mismatch** bodies, which
> `encoding/json` rejects *before* any custom unmarshaller runs, and then generalising to the
> decode path as a whole. **That is the exact stand-in failure this repo keeps committing, and I
> committed it in the paragraph congratulating myself for catching it in someone else.** The
> corrected result is below. The wrong version is preserved as §6.3a because the *shape* of the
> mistake is more valuable than the transcript.

**Why load-bearing.** The re-audit's fourth accepted Critical states that *"`ErrBadCursor` **and the
36 `ErrBadInput` decode wraps** do echo caller values"*. §6.2 confirms the first half. This section
confirms the **second half too**.

**The channel: `StartInput.DefRef` is a `model.Qualifier`, which declares a custom
`UnmarshalJSON`.** `POST /instances` — the highest-traffic route — therefore decodes through
hand-written parsing code, and `ParseQualifier` (`definition/model/qualifier.go:42-57`) formats the
caller's string with **`%q`** in **all three** of its error forms.

**Probe** (throwaway `transport/http/httpcore/zz_probe2_test.go`, deleted after — real
`httpcore.StartInput`, real `encoding/json`):

```
body={"def_ref": "4111-1111-1111-1111:0"}
  -> workflow-model: invalid qualifier: version must be >= 1 in "4111-1111-1111-1111:0"

body={"def_ref": ":123-45-6789"}
  -> workflow-model: invalid qualifier: empty id in ":123-45-6789"

body={"def_ref": "kyc:ssn-123-45-6789"}
  -> workflow-model: invalid qualifier: bad version in "kyc:ssn-123-45-6789":
     strconv.Atoi: parsing "ssn-123-45-6789": invalid syntax
```

**Result: the caller's `def_ref` is echoed verbatim, and the third form echoes it TWICE** — once
from `%q` on `s`, once inside the wrapped `strconv.Atoi` error. This is the re-audit's *"echo the
whole `def_ref` twice"*, reproduced exactly.

⇒ **`ErrBadInput` must NOT opt in as a sentinel.** `httpcore.Validate`'s DTO message opts in (§1,
executed value-free even for a length constraint); the **decode wrap does not**. Two producers of
one sentinel, with opposite disclosure properties — which is unanswerable by any list keyed on the
sentinel, and is the strongest possible argument for the producer opt-in.

### 6.3a ⚠ The refuted first attempt, preserved for its shape

The original probe fed three bodies whose failures occur inside `encoding/json`'s reflection layer,
never reaching `Qualifier.UnmarshalJSON`:

| body | result (all three adapters) |
|---|---|
| `{"def_ref": 4111111111111111}` | `json: cannot unmarshal number into Go struct field StartInput.def_ref of type string` |
| `{"vars": "123-45-6789"}` | `json: cannot unmarshal string into Go struct field StartInput.vars of type map[string]interface {}` |
| `{"def_ref": "ok" xx` | `invalid character 'x' after object key:value pair` |

Those messages **are** value-free, and they are also **irrelevant to the claim they were offered
for**: they exercise the one decode branch that never reaches caller-authored parsing. The tell was
visible in the output and I read past it — *"of type **string**"* is `encoding/json` describing the
target's *underlying* kind, which is what a `Qualifier` is; a body that actually reached
`UnmarshalJSON` would have produced a `workflow-model:` error, and none of the three did.

**Two lessons, and the second is the one worth keeping:**

1. **A probe that exercises the wrong branch is worse than no probe**, because it produces a
   transcript that looks like evidence.
2. ⭐⭐ **I inherited an audit finding, disbelieved it, and "refuted" it without checking what my
   own inputs actually exercised.** Premise Discipline's *"re-verify claims you inherit"* is
   symmetric: it applies to **refutations** as much as to restatements. A refutation is a claim.

**What did NOT change:** the design conclusion. §6.4 was written from the wrong §6.3 and is
**strengthened, not weakened**, by the correction — see the note there.

**Probe** (throwaway `zz_probe_test.go` in each of `httpcore`, `gin`, `fiber`, all deleted after —
the same four hostile bodies through each adapter's real decode idiom, decoding into the real
`httpcore.StartInput`):

| body | `stdlib` (`encoding/json`) | `gin` (`ShouldBindJSON`) | `fiber` (`c.Bind().JSON`) |
|---|---|---|---|
| `{"def_ref": 4111111111111111}` | `json: cannot unmarshal number into Go struct field StartInput.def_ref of type string` | identical | `bind "def_ref" from body: ` + identical |
| `{"vars": "123-45-6789"}` | `…cannot unmarshal string into Go struct field StartInput.vars of type map[string]interface {}` | identical | `bind "vars" from body: ` + identical |
| `{"def_ref": "ok" xx` | `invalid character 'x' after object key:value pair` | identical | `bind from body: ` + identical |

**Result: no submitted value appears in any of the nine messages.** `encoding/json` renders the
**field path** and the **Go types**; fiber prefixes the **field name** again. `4111111111111111` and
`123-45-6789` are absent throughout.

⚠⚠ **But the RIGHT conclusion is not "the bundle was correct".** The messages are value-free
because of a property of the **DTO field types**, not of the sentinel:

- No DTO in `transport/http/httpcore/dto.go` declares a `time.Time` field (`grep` → 0 hits), and no
  type in `transport/http/` or `service/` declares a custom `UnmarshalJSON` (`grep` → 0 hits).
- §6.2 shows exactly what happens when one does: `time.Parse` **quotes the caller's input**.

⇒ **Adding one `time.Time` field to any request DTO — an ordinary, unrelated change — silently
converts 36 value-free 400s into value-echoing ones, and no test in the repo fails.**

## 6.4 ⭐ What §6.1–6.3 jointly decide, stated as the design increment

The three probes agree on one thing, and it is the increment D5 needs:

> **Value-freedom is a property of the PRODUCING SITE and the TYPES it renders — never of the
> sentinel.**

The evidence is now stronger than when this section was first written, because the correction to
§6.3 supplied the decisive case:

| sentinel | producer | discloses? |
|---|---|---|
| `ErrBadInput` | `httpcore.Validate` (the DTO validator) | **no** — executed, §1, value-free even for a length |
| `ErrBadInput` | the 36 decode wraps, via `Qualifier.UnmarshalJSON` | **YES** — executed, §6.3, echoes `def_ref` **twice** |
| `ErrBadCursor` | `lister.go:69,77,90` (static forms) | **no** |
| `ErrBadCursor` | `lister.go:66` (`%w: %w` over caller bytes) | **YES** — executed, §6.2, two channels |

⭐ **Two sentinels, four producers, and within EACH sentinel the producers disagree.** A list keyed
on the sentinel cannot express this table — it has one row per sentinel and the answer is
per-producer. That is not a defect in how the list was populated; it is a defect in what the list
is keyed on, and no amount of re-auditing the entries fixes it.

⇒ **The rendering must be opted into by the producing site.** Deny by default; a producer that
knows its message is safe says so. This is the same shape as §6.1 — a hand list standing in for a
machine-checkable property.

⚠⚠ **And note the epistemics, because they cost me an hour.** The first version of §6.3 ran a probe
that **passed**, and the pass was an artefact of the inputs. An execution lens would have
reproduced it and confirmed the bundle. What caught it was not another probe but a **structural
question** — *"which types does this decode path actually run?"* — which surfaced
`model.Qualifier`'s custom unmarshaller and made the original probe obviously off-target.
**Execution answers "what happened"; it does not answer "did I exercise the thing I am claiming
about."** That second question has to be asked separately, and nothing in the current process
forces it.

## 6.6 The concrete error TYPE discriminates the two decode producers exactly

**Why load-bearing.** §6.3 shows one sentinel with two producers of opposite disclosure properties.
D2's opt-in is only implementable at the 36 decode sites if a site can *tell which producer it got*
without knowing the DTO's field types.

**Probe** (throwaway `transport/http/httpcore/zz_probe3_test.go`, deleted after):

```
body={"def_ref": 4111111111111111}      UnmarshalTypeError=true  SyntaxError=false  concrete=*json.UnmarshalTypeError
body={"def_ref": "ok" xx                UnmarshalTypeError=false SyntaxError=true   concrete=*json.SyntaxError
body={"def_ref": "kyc:ssn-123-45-6789"} UnmarshalTypeError=false SyntaxError=false  concrete=*fmt.wrapErrors
```

**Result: a clean three-way split.** `encoding/json` returns a custom unmarshaller's error
**as-is** — it does **not** wrap it in `UnmarshalTypeError` — so `errors.As` against the two
`encoding/json` types is exactly the test for *"was this produced by the standard library's own
reflection layer, whose messages are value-free by construction, or by caller-authored parsing
code, whose messages are not?"*

⚠ **One honest caveat.** `*json.SyntaxError`'s message names the **offending character** — one byte
of caller input. It is included in the vouched set as a diagnostic rather than a disclosure, and
that judgement is flagged in the ADR for the audit to attack. `*json.UnmarshalTypeError` names only
the field path and Go type names and has no such caveat.

⚠ `ASSUMPTION (unverified)`: that gin's `ShouldBindJSON` and fiber's `c.Bind().JSON` preserve these
concrete types through `errors.As`. §6.3a shows both surface `encoding/json`'s messages verbatim,
which is strong circumstantial evidence, but **the `errors.As` behaviour through those two wrappers
was probed only for `encoding/json` directly.** The per-adapter tests must assert it.

## 6.5 What §6 does NOT establish

- `ASSUMPTION (unverified)`: that the nine `encoding/json` messages above are the **complete** set
  of decode-error shapes. They are the shapes reachable from a malformed body against today's DTO
  field types. A DTO gaining a `time.Time`, a custom unmarshaller, or a `json.Number` changes this,
  which is precisely §6.4's point.
- `ASSUMPTION (unverified)`: the classification of the 48 payload-typed columns into
  `discloses` / `structural`. §6.1 derives the **column set** by machine; the classification is a
  judgement, and the invariant test exists to force it to be *stated* rather than to compute it.
- **Not executed:** whether the MySQL `trigger_` divergence has siblings in a future migration.
  There is exactly one migration file per dialect today (`0001_init.sql`), so the invariant test is
  the only thing that will catch the second occurrence.

---

# 7. THE ONE-DECISION RE-CUT'S OWN EVIDENCE (2026-08-21, after the three-decision audit failed)

**Why this section exists.** The owner split slice 1 into one decision each after its audit
(65 findings, 20 Critical). This record is now **request body caps only**. Its central change —
*check the cap BEFORE parsing* — is executed here, by the author, before the audit.

## 7.1 ⭐⭐ Capping DURING the parse does not cap the request. Executed.

**Why load-bearing.** The ADR's headline is *"39 sites, one policy, one status"*. The interaction
lens (I4) reported that stdlib and gin can return **2xx** for an oversize body. This is the
controller's independent reproduction, and it also validates the proposed fix.

**Probe** (throwaway `transport/http/httpcore/zz_rbp_test.go`, deleted after; a 64-byte cap standing
in for 1 MiB, real `httpcore.StartInput`, real `http.MaxBytesReader`):

```
DURING  wellformed-oversize  err=http: request body too large
DURING  oversize-syntaxerr   err=invalid character '@' looking for beginning of object key string
DURING  value+trailing       err=<nil>

BEFORE  wellformed-oversize  READ-REJECT err=http: request body too large
BEFORE  oversize-syntaxerr   READ-REJECT err=http: request body too large
BEFORE  value+trailing       READ-REJECT err=http: request body too large

GIN-RESET decode err=<nil>
```

**Result 1 — the defect is confirmed and is worse than "inconsistent".** Capping during the parse
produces **three different outcomes for three oversize bodies**: 413, 400, and **`err == nil`**.
The third is a **complete JSON value followed by excess bytes**: `json.Decoder.Decode` reads only
the first JSON value, never reads the excess, so `MaxBytesReader` never trips. **The cap applies to
the prefix the decoder consumed, not to the request.**

**Result 2 — read-before-parse fixes all three**, with one code path and one error.

**Result 3 — the gin buffer-and-reset works.** Reading through `MaxBytesReader` into a buffer and
reassigning `r.Body = io.NopCloser(bytes.NewReader(buf))` decodes cleanly, so gin's
`ShouldBindJSON` and its validation are preserved rather than bypassed.

⚠ **Prior art in this repo, missed by four revisions and two audits.**
`runtime/kernel/cursorcodec.go:50-58` carries a trailing-data guard whose comment says
`Decode` *"reads only the FIRST JSON value and silently ignores whatever follows"* — added by
ADR-0160 for this exact behaviour, one package over. **The knowledge was in the repo the whole
time; no document in this lineage cited it until round 5.**

## 7.2 What §7 does NOT establish

- `ASSUMPTION (unverified)`: the memory profile of buffering at a 1 MiB cap under concurrency.
  Reasoned from fiber and fasthttp already buffering; **not measured**.
- `ASSUMPTION (unverified)`: that no handler in the three adapters relies on a streaming `Body`.
  The probe used one route's DTO.
- `ASSUMPTION (unverified)`: that `fiber.Config.BodyLimit` is unreachable from a `fiber.Router`.
  The mount-time WARN's fallback depends on it.
- **Not probed here:** the `*int64` tri-state through the real `ResolveConfig`. The `MaxBytesReader`
  half is executed (§Context of the ADR: limits `0` and `-1` both reject every non-empty body); the
  **defaulting** half is a prescription, and phase 1 test 2 is what discharges it.

---

# 8. THE STRIPPED RE-CUT'S EVIDENCE (2026-08-21, after round 6 failed at ONE decision)

**Why this section exists.** Round 6 produced 61 findings / 24 Critical against a one-decision
bundle, and every Critical was a **scope-boundary** failure. The owner stripped the delivery to the
cap alone. The two claims the stripped design rests on are executed here.

⚠⚠ **Read §6.3a first, and then read this section against the execution lens's verdict on the whole
file:** *"the bundle's probes are narrow in a consistent direction — toward the fixture that
demonstrates the fix."* The fixtures below were therefore chosen to include the cases that would
**embarrass** the design (under-cap trailing bytes; today's behaviour as the control), not only the
ones that vindicate it.

## 8.1 ⭐⭐ Cap the READ, keep the PARSE: over-cap is caught in every shape, under-cap is unchanged

**Why load-bearing.** The previous revision prescribed *"unmarshal from the resulting buffer"*,
which three lenses showed silently swaps the adapters' lenient `json.Decoder` for a strict
`json.Unmarshal` and makes stdlib and gin disagree on under-cap trailing bytes. The stripped design
feeds the buffer to **today's** decoder instead. That must be shown to (a) still catch every
over-cap shape and (b) change nothing under the cap.

**Probe** (throwaway `transport/http/httpcore/zz_min_test.go`, deleted after; 64-byte cap standing
in for 1 MiB, real `httpcore.StartInput`, real `http.MaxBytesReader`; `MINIMAL` =
`io.ReadAll(MaxBytesReader(...))` then `json.NewDecoder(bytes.NewReader(buf))` — the *same* decoder
idiom the stdlib site uses today):

```
overcap-trailing     TODAY=parsed/<nil>   MINIMAL=READ-REJECT/http: request body too large
overcap-wellformed   TODAY=parsed/<nil>   MINIMAL=READ-REJECT/http: request body too large
undercap-trailing    TODAY=parsed/<nil>   MINIMAL=parsed/<nil>
clean                TODAY=parsed/<nil>   MINIMAL=parsed/<nil>
```

**Result 1 — every over-cap shape is rejected**, including `{"def_ref":"a:1"}` followed by 200
bytes of trailing garbage, which is the shape that returns **2xx** today.

**Result 2 — under-cap behaviour is byte-for-byte unchanged**, trailing bytes included. This is the
finding that matters: it means **the strict/lenient question does not arise**, because no decoder is
replaced. Three lenses' worth of findings (E2, C4, F3) are removed by construction rather than
resolved by argument.

⚠ `ASSUMPTION (unverified)`: that gin's `ShouldBindJSON` behaves identically when its
`gc.Request.Body` is reassigned to the buffer. §7.1 executed the *decoder* half of this; the
**binder + validator** half is what the per-adapter test must discharge. Do not infer it from here.

## 8.2 A plain `int64` default survives an explicit `0` — no tri-state is needed

**Why load-bearing.** The previous revision introduced `MaxBodyBytes *int64` because
`ResolveConfig` was believed to clobber an explicit `0`. One lens then showed the prescribed
falsifier for that was **vacuous**, and another found `action/httpcall` already solving the same
problem without a pointer.

**Read from source** (`transport/http/httpcore/seam.go:39-58`): `ResolveConfig` sets its defaults
**in the struct literal**, then applies opts, then applies post-loop guards **only to nil-able
fields** (`Wrap`, `InstanceMapper`, `Logger`). An `int64` has no nil, so nothing overwrites it.

⇒ `MaxBodyBytes int64` with the default in the literal and `n <= 0` disabling is correct, and it
matches `action/httpcall`: `io.ReadAll(io.LimitReader(r, max+1))` at `httpcall.go:194`, `max <= 0`
disables at `:191`, default applied in the constructor at `:214`, documented in six places.

⚠ **Not executed:** the end-to-end path from `WithMaxBodyBytes(0)` through a real adapter to a
decode site. Phase 2's opt-out test is what discharges it.

## 8.3 Derived boundaries — the class of defect that failed round 6

Every Critical in round 6 was a boundary asserted and never derived. These were derived from source
before the stripped design was written:

| boundary | derived value |
|---|---|
| `ResolveConfig` call sites | **15** — exactly **5 per adapter** (`grep`, non-test). A per-mount diagnostic would fire 3–4× per documented mount |
| what can carry an error out of mounting | **nothing** — `ResolveConfig`, `CustomizeOption`, `RouteCustomizer.Customize`, `MountGroups` and all `Mount`/`MountHealth` return nothing |
| how a `MountGroups` consumer configures a group | `MountGroups(r, groups...)` calls `Customize(r)` with **no opts** (`seam.go:108`), so defaults apply. ⭐ Its **own godoc already names the escape**: *"Groups needing distinct base paths or middleware call Customize directly with the relevant options."* |
| who can build an instrument | **only `httpcore`** — `Instrumentation`'s fields are unexported and the three adapters import no otel |
| existing convention for bounding a body | **`action/httpcall`** — plain `int64`, `<= 0` disables, default in the constructor |
| existing handling of trailing data after a JSON value | **`runtime/kernel/cursorcodec.go:50-58`** (ADR-0160) |
| is `fiber.Config.BodyLimit` reachable? | `(*fiber.App).Config()` **is exported** (`app.go:1233`) — the earlier `ASSUMPTION (unverified)` is **REFUTED** — but a mounted `*fiber.Group` is not an `*App` |

## 8.4 What §8 does NOT establish

- `ASSUMPTION (unverified)`: gin's binder+validator through a reassigned body (see §8.1).
- `ASSUMPTION (unverified)`: the **1 MiB** default. A judgement call.
- **Not executed here, but executed by a lens and accepted:** a chunked request with no terminating
  chunk holds the handler indefinitely under read-to-EOF; buffering is ~2 % *faster* and allocates
  ~37 % fewer bytes than streaming at 1 MiB; over-declared `Content-Length` yields `unexpected EOF`
  with `errors.As(*MaxBytesError) == false`.
