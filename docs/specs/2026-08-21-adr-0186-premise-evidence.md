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

### 4.4 Plaintext columns at rest — **twelve columns across seven tables**, in three dialects

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
