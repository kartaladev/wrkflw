# 167. Definition decoding rejects unknown fields

- Status: Accepted after the rule-#9 audit of 2026-08-08
- Date: 2026-08-07

> Design: `docs/specs/2026-08-07-strict-definition-decoding.md`.
> Plan: `docs/plans/2026-08-07-strict-definition-decoding.md`.
> Closes pre-v0.1.0 blocker 1.
>
> ADR numbers 0155–0158 remain reserved by parked branches and do not exist on
> `main`; this ADR takes 0167, the next free number after 0166.

## Context

Both process-definition decoders discard field names they do not recognise.
`model.ParseYAML` calls `yaml.Unmarshal`, and `ProcessDefinition.UnmarshalJSON`
calls `json.Unmarshal` — neither is configured to reject unknown fields.

A misspelled eligibility tag therefore leaves a `UserTask` with empty
`EligibleRoles`, and an empty `AuthzSpec` is documented on the type to mean
allow-all: `RoleAuthorizer.Authorize` skips the role check entirely when
`spec.Roles` is empty. The two behaviours compose into a silent authorization
bypass — nothing errors at parse time and nothing errors at authorization time.

Measured on `main` (`9e96112`), a manager-only task and three plausible
misspellings of its tag:

```
tag=eligible_roles   EligibleRoles=[manager] -> clerk DENIED
tag=eligable_roles   EligibleRoles=[]        -> clerk ALLOWED
tag=eligible_role    EligibleRoles=[]        -> clerk ALLOWED
tag=eligibleRoles    EligibleRoles=[]        -> clerk ALLOWED
```

The JSON path behaves identically, including for a field named
`totally_unknown_field`.

**This is not hypothetical — our own README is a live instance of it.** Found by
the rule-#9 audit and confirmed independently: `README.md` uses camelCase keys
that no struct tag declares. Parsed on `main` today:

```
README long parses OK, nodes=4
    serviceTask "charge" CompensateAction="" RetryPolicy=<nil>
    userTask "approve"   EligibleRoles=[]
```

The repo's own published quickstart therefore documents a definition that
silently yields an allow-all task, a compensation action that never runs, and a
nil retry policy. `examples/readme_quickstart/main.go` — named for that README —
uses the *correct* snake_case tags, so the two have drifted apart unnoticed.

This also corrects how the spec first framed the evidence: `eligibleRoles` is not
merely "a misspelling a JSON-minded author would reach for", it is **the spelling
this repository prescribes**. Fixing `README.md` is part of this delivery.

**⚠ Correction, executed at implementation time.** An earlier revision of this
paragraph named the offending lines as "144, 864, 865 and 868" — **four**.
Re-deriving the list mechanically returned **seven**:
`grep -nE '^\s*-?\s*[a-z]+[A-Z][a-zA-Z]*:' README.md` reports 144, 864, 865, 868,
**869, 870 and 871** — `deadlineDuration`, `deadlineFlow` and `deadlineAction`
were missed, as were the nested `maxAttempts`/`initialInterval`/`backoffCoef`
sub-keys inside `retryPolicy`. Following the audited list literally would have
shipped a README that still did not parse. **An enumeration must be re-counted,
never inherited** — the same failure ADR-0159's blocker entry made.

Execution also surfaced a **second, unrelated README defect** the audit never
saw: the "Valid `kind` values" list included `errorEndEvent`, which is not a
registered kind today (`kind: errorEndEvent` yields
`workflow-definition: unknown node kind "errorEndEvent"`, on `main` too).

⚠ **"has never been registered" — the first wording — was false**, caught by the
adversarial review and an over-reaching *never* of exactly the kind Premise
Discipline warns about. It **was** registered until **ADR-0127** retired it:
`git log -S errorEndEvent -- definition/` gives `dcfe3f1 refactor(event)!: delete
ErrorEndEvent kind, folded into EndEvent`, and `definition/event/event.go:362` at
`dcfe3f1^` still carries `Name: "errorEndEvent"`. The README simply kept
documenting it afterwards. This also sharpens the data-migration consequence
below: a definition blob persisted before `dcfe3f1` carries
`"kind":"errorEndEvent"` and already fails to load on `main` — pre-existing, not
caused by this ADR.

The
other 17 documented kinds were each probed and parse. Corrected here, with the
real ADR-0127 form — an `endEvent` carrying `end_behavior: error` or
`end_behavior: terminate`, both executed.

**A consumer cannot mitigate the JSON case themselves.** A caller who
constructs a `json.Decoder` and calls `DisallowUnknownFields()` still gets
`err=<nil>`, because `ProcessDefinition.UnmarshalJSON` performs a plain
`json.Unmarshal` internally and the outer decoder's setting does not survive a
custom unmarshaler. The control case — the same strict decoder against a struct
with no custom unmarshaler — correctly reports `json: unknown field "unknown"`,
so the mechanism works and the custom unmarshaler is what defeats it.

Full probe transcripts are in spec §2.1–2.5.

## Decision

**Both decoders reject unknown fields, unconditionally.**

`ParseYAML` decodes through `yaml.NewDecoder` with `KnownFields(true)`.
`ProcessDefinition.UnmarshalJSON` applies `DisallowUnknownFields()` to its
**internal** decode of `definitionWire` — the fix must live inside the custom
unmarshaler, because that is the only place it survives.

**There is no opt-out.** No `WithLenientDecoding` `LoaderOption`, no
package-level toggle.

This follows the precedent ADR-0159 set for `TimerStore.ArmedTimer`, which chose
a required method over an opt-in capability because "recurrence is not an
optional capability of a timer store the way writing or stats are, and
pre-release breakage is cheap. With a tag in place, the capability route would
be correct."

**The precedent is real but the analogy is weaker than first written**, and the
audit was right to say so. ADR-0159 breaks a **Go interface**: consumers get a
compile error, loudly, at build time. ADR-0167 breaks **data**, including data
already sitting in consumers' databases, and it surfaces at load time. An earlier
revision of this ADR said the reasoning "applies with more force here"; that
sentence is withdrawn. It applies, but with a different and less comfortable
failure mode.

The strongest argument for no-opt-out is a structural one this ADR originally
missed: `LoaderOption` is `func(*definitionCore)` and options are applied
**after** decoding, so `WithLenientDecoding` could not be a `LoaderOption` at all
— it would need a second, parallel configuration mechanism threaded into the
decoder. The escape hatch is not merely undesirable, it is awkward to express.

**⚠ Correction, executed 2026-08-08.** An earlier revision claimed "the YAML
surface has no metadata or extension concept, so no consumer has a sanctioned
reason to carry unknown keys today." **That is false.** Two ordinary YAML
authoring idioms parse today and are rejected after this change:

```
_defaults: &d   (top-level anchor holder block)  baseline err=<nil>  -> rejected
x-owner: platform-team                            baseline err=<nil>  -> rejected
```

A top-level anchor block is the standard way to keep a definition DRY, so the
cost of no-opt-out is real and was understated. See "Open question" below.

**No new error sentinel, and each decoder keeps its existing error shape.**
`ParseYAML` keeps wrapping as `workflow-definition: parse YAML: %w`.
`ProcessDefinition.UnmarshalJSON` returns its **decode** error **unwrapped**, as
it does today — no `workflow-definition:` prefix is added to that path, since
doing so would change every existing JSON decode error, not just the new
unknown-field ones. So an unknown JSON field surfaces as the bare
`json: unknown field "…"`.

⚠ The **trailing-data** error is the one exception, and it is not a decode error:
it is a new condition with no pre-existing shape to preserve, so it carries the
project's `workflow-definition: …` prefix per the sentinel-prefix convention.

`httpcore.ClassifyError` would justify a sentinel if a transport parsed
definitions, but none does — registration runs through
`runtime.RegisterDefinition` and `kernel.MemDefinitionRegistry.Register`, both
called from Go. A sentinel now would be speculative API; it arrives with the
consumer that needs it.

**The JSON decoder must not lose trailing-data rejection.** `json.Unmarshal`
validates the entire input; `json.Decoder.Decode` reads one value and stops. A
naive swap therefore *loosens* a check in a change whose purpose is to tighten
one.

**⚠ Correction, executed by the adversarial review.** That statement is true only
of a **direct `def.UnmarshalJSON(data)` call**, and an earlier revision of this
ADR implied it applied everywhere. It does not: `json.Unmarshal` validates the
whole input *before* it ever dispatches to a custom unmarshaler, and
`json.Decoder.Decode` hands the unmarshaler exactly one value's bytes. So trailing
data cannot reach this method through either — including through
`DefinitionStore.GetDefinition`/`Lookup`, which use `json.Unmarshal`. Measured on
both trees:

```
BASELINE json.Unmarshal `{…} trailing garbage` -> invalid character 't' after top-level value
PATCHED  json.Unmarshal `{…} trailing garbage` -> invalid character 't' after top-level value   (identical)
```

The guard therefore protects one surface: `UnmarshalJSON` is an **exported method
on an exported type**, so a consumer may call it directly, and there the baseline
did reject trailing data. Keeping that rejection is preserving the exported
contract, not defending an internal path. Recorded at its true scope rather than
overstated.

Measured (direct-call path):

```
BASELINE json.Unmarshal  `{…valid…} trailing garbage` -> err=invalid character 't' after top-level value
PATCHED  Decoder.Decode  `{…valid…} trailing garbage` -> err=<nil>
BASELINE empty input -> err=unexpected end of JSON input
PATCHED  empty input -> err=EOF
```

So `UnmarshalJSON` follows its `Decode` with a trailing-token check and returns
an error if anything follows the value. **Trailing whitespace stays accepted** —
it is legal JSON framing, and `json.Marshal` output routinely gains a newline in a
file or a column; rejecting it would make engine-written definitions unloadable.

**⚠ Amended during implementation.** The check keeps the underlying error when
`dec.Token()` returns one: corrupt trailing bytes yield a `*json.SyntaxError`
naming the offending character, while a legitimate second JSON value yields
`err == nil`, and collapsing both into one bare string loses information a
debugger needs. This mirrors `decodeCursorInto`
(`runtime/kernel/cursorcodec.go`, ADR-0160), which is the repo's pre-existing
strict-decoding path and had already solved this. The first cut of this ADR did
not reference it — an omission found by sweeping the repo for sibling code, not
by any review. This is the exact mirror of the `io.EOF`
issue below, on the other decoder — the pattern to take away is that **swapping
`Unmarshal` for a `Decoder` is never behaviour-preserving in either library**,
and both directions had to be measured rather than assumed. **⚠ The empty-input change was worse than "a message change", and is fixed.**
An earlier revision recorded only that the text changes from
`unexpected end of JSON input` to `EOF`, "accepted, since no caller matches on
it". The adversarial review showed the returned error also began to **satisfy
`errors.Is(err, io.EOF)`**, where the baseline `*json.SyntaxError` did not — so a
caller treating `io.EOF` as "clean end of stream" would silently skip an empty or
truncated definition instead of failing. That is a semantic change, not a
cosmetic one, and it contradicts this ADR's own decision to preserve each
decoder's error shape. `UnmarshalJSON` now translates that `io.EOF` into
`workflow-definition: unexpected end of definition JSON`, keeping the EOF
identity inside the method. Verified: `errors.Is(err, io.EOF)` is now **false**
for both `""` and whitespace-only input, as on baseline.

**⚠ YAML strictness stopped at the first document — found by the adversarial
security review, and fixed here.** `yaml.Decoder.Decode` consumes ONE document,
so `KnownFields(true)` never saw anything after a `---`: later documents were
discarded in silence, unknown fields included. That is the exact YAML mirror of
the JSON trailing-data hole above, and it was **a live instance of the bypass
this ADR claims to close**. Executed on both trees:

```
<valid definition>
---
id: overlay
nodes:
  - id: approve
    eligible_roles: ["manager"]      <- plus keys strict decoding would reject

BASELINE  ParseYAML err=<nil>  Build err=<nil>  approve roles=[] -> ALLOWED (bypass)
PATCHED (before this fix)      identical — doc-2 unknown keys never reported
```

A reviewer reading that file sees an eligibility rule; the engine applies none.
`ParseYAML` now drains the decoder after the first document and rejects any
further document **that carries content**. A bare trailing `---`, or one followed
only by whitespace, is legal framing and still parses — verified, along with
termination on 5 000 consecutive separators and detection of content buried after
100 of them.

**Empty input keeps its current meaning.** `yaml.Unmarshal` returns `nil` for an
empty or comment-only document, whereas `yaml.Decoder.Decode` returns `io.EOF` —
measured:

```
input=""                   Unmarshal err=<nil>  | Decoder err=EOF
input="\n"                 Unmarshal err=<nil>  | Decoder err=EOF
input="# just a comment\n" Unmarshal err=<nil>  | Decoder err=EOF
input="id: x\n"            Unmarshal err=<nil>  | Decoder err=<nil>
```

A naive decoder swap would therefore turn empty input from "an empty definition"
into a parse error. That is a second behaviour change, unrelated to unknown
fields, so `ParseYAML` maps `io.EOF` back to today's outcome. Whether an empty
definition *should* be an error is a separate question this ADR does not decide.

### What this does not do

**It does not close the fail-open default**, and the residual is **wider than an
earlier revision of this ADR implied**. The security review executed it: not only
an absent eligibility declaration but **all** of `eligible_roles: []`,
`eligible_roles:` and `eligible_roles: null` parse *cleanly* — they are declared
keys, so strictness has nothing to object to — and every one yields allow-all. An
author writing `[]` to mean "nobody" gets "everybody". Separately,
`RoleAuthorizer` never evaluates `Privileges`, so a task secured **only** by
`eligible_privileges` is allow-all under the default authorizer.

This is deliberate design on the type, not a defect of decoding, and it takes its
own ADR — deliberately separated so a decoder change and an authorization
semantics change do not land in one commit with one blast radius. After this
change, an empty-or-absent spec is the **largest remaining parse-time route** to
an allow-all task.

**It does not make programmatic construction strict.** Building a definition in
Go — `model.NodeWire` / `activity.UserTask` directly, or the builder API without
`WithEligibleRoles` — never passes through either decoder. "Definitions are
strict" is a claim about the **authoring surface**, not about every route into
the engine.

**It does not validate kind-appropriateness.** `nodeYAML` and `NodeWire` are
flat unions carrying the fields of all node kinds, discriminated by `kind`. So
strictness rejects names unknown to the *union*, not names inappropriate to the
*kind*: `timer_duration` on a `userTask` remains legal. Kind-appropriateness is
`model.Validate`'s concern.

**⚠ The two unions are not the same union.** An earlier revision of this ADR read
as though they were. `nodeYAML` is a strict **subset** of `NodeWire` — computed
by diffing the struct tags, five JSON fields have no YAML counterpart:

```
boundary_action  boundary_error_expr  deadline_trigger  timer_trigger  wait_trigger
```

The three `*_trigger` fields are the **canonical persisted encoding** (the flat
`timer_duration`-style forms are marked legacy and are not written by `ToWire`).
So a YAML author writing the canonical nested trigger form gets a hard parse
error after this change, where today it is silently dropped. That silent drop is
the same defect this ADR fixes, one level down; turning it into an error is an
improvement, but it is a **recorded consequence, not a surprise**, and the
YAML/JSON authoring-surface gap it exposes is its own backlog item.

**Strictness makes our own struct tags load-bearing.** `nodeYAML` and
`definitionYAML` declare 43 distinct YAML keys. A missing or misspelled
`yaml:"…"` tag on any of them was previously a silent no-op and becomes a hard
failure on a legitimate definition. The over-strictness guard (spec T8) is the
only defence against that and must enumerate every tag, not a sample.

**⚠ Correction, as shipped 2026-08-08.** An earlier revision of this paragraph
added: "Only 22 appear anywhere in the repo's tests, fixtures or examples; **21
never appear at all**." That was true of the pre-change repo and is **false as
shipped** — `allFieldsYAML` now exercises all 43, and the guard
(`TestAllDeclaredYAMLTagsParseUnderStrictDecoding`) derives the tag list from the
source at test time rather than from a pasted list, so a tag added without a
fixture entry fails the test. The guard also widened past these two structs to
the nested types a definition authors inline — `RetryPolicy` (6 keys),
`ValidationDescriptor` (2), `SequenceFlow` (5) — since strictness makes those
load-bearing by the same argument. See spec §8.

### No extension-key carve-out

Top-level YAML anchor blocks (`_defaults: &d`) and `x-` extension keys parse
today and are rejected after this change — measured, both return `err=<nil>` on
`main`. A **reserved ignored prefix** was considered: it would preserve the
security property, since a typo of `eligible_roles` cannot accidentally begin
with `x-` or `_`, while keeping the DRY anchor idiom available.

**Decided against it (owner, 2026-08-08).** One decoding rule is easier to reason
about than one rule plus an exception, and a reserved prefix is a permanent
addition to the definition format — a second concept every author must learn —
bought for an idiom that has workarounds: anchors and aliases still work between
*declared* fields, and shared values can live in a separate file or be repeated.

The cost is real and recorded rather than minimised: authors using a top-level
holder key must restructure. Pre-v0.1.0 makes adding a prefix later cheap if the
idiom turns out to matter in practice, so this is a reversible decision, not a
closed door.

## Consequences

**Positive.**

- The typo route to a silent authorization bypass is closed at parse time, where
  it is cheap to diagnose, rather than at runtime, where it is invisible.
- Authoring errors surface as errors. YAML in particular reports every offending
  field with a line number.
- The change is contained to two call sites in `definition/model`: no new port
  and no transport.

**⚠ It is a DATA migration, not only a source migration.** An earlier revision of
this ADR claimed "no storage"; that was false, and both auditors found it
independently. `DefinitionStore.GetDefinition` and `DefinitionStore.Lookup`
(`internal/persistence/store/definitions.go`) each `json.Unmarshal` the stored
definition blob into a `model.ProcessDefinition`, so they run through the
newly-strict `UnmarshalJSON`; `Lookup` additionally satisfies
`kernel.DefinitionRegistry`. Consequences:

- A definition row written by an earlier build becomes **unloadable at runtime**
  if it carries any key the wire structs no longer declare — surfacing as a
  decode error from a running engine, not at authoring time.
- ⚠ **The trigger already exists in this repo's history, and `/code-review`
  found it.** ADR-0144 (`8179c0b`) moved the definition wire to snake_case. Five
  tags were camelCase before it — `compensateAction`, `compensationAction`,
  `completionAction`, `correlationKey`, `messageName` — each verified to fail
  now (`json: unknown field "compensateAction"`). Any row written before
  `8179c0b` carrying one stops loading, and every instance of that definition
  becomes unrunnable. **Audit stored definitions for these five keys before
  deploying.** ⚠ The review report generalised this to "*every* `NodeWire` json
  tag" and named `eligibleRoles`/`retryPolicy`/`deadlineFlow`/`timerTrigger`;
  re-derived from `8179c0b^`, only those five ever existed as camelCase. The
  finding was right, its enumeration was not — the third time in this delivery
  that a correct conclusion arrived with a wrong count attached.
- A new standing constraint: **a `NodeWire` field may no longer be removed
  without a migration.**
- `go vet ./...` cannot prove this safe. It compiles; it does not decode.

⚠ **And the failure is silent, not loud** — established by the security review,
and the sharpest consequence of this change. Three of five `Lookup` call sites
treat *any* decode error as not-found: `runtime/jobstore.go` skips the timer and
logs `"definition not found"`, so **every armed timer for that definition is
skipped forever** — deadlines, escalations and in-wait reminders never fire,
behind a misleading log line; `runtime/calllink/notifier.go` `continue`s on a
comment asserting the failure is transient, which is now false, so the pending
queue retries forever; `service/service.go` discards the error and serves a
definition-less view. There is no sentinel, no metric, no fallback decode and no
migration tool.

Historical exposure is currently **nil** — `git log -p --follow` over the wire
struct shows 74 tags added across 26 commits and **zero ever removed** — so the
risk is entirely prospective, which is exactly why the guard is cheap to add now.
A distinct `ErrDefinitionUndecodable`, Error-level logging at those three sites,
and a deploy-time `VerifyAll` check are filed as a follow-up rather than bolted
onto this delivery.

This is affordable only pre-v0.1.0, and only because the marshal/unmarshal pair
is symmetric through the same `definitionWire` — a full
YAML → build → `json.Marshal` → `UnmarshalJSON` round-trip was executed and
returned `err=<nil> nodes=3`.

**Measured cost, not assumed.** `UnmarshalJSON` now allocates a `bytes.Reader`
and a `json.Decoder` per call and makes one extra `Token()` call. Benchmarked
against the baseline on a 3-node definition, 5 runs of 2000 iterations each:

```
BASELINE  ~5100 ns/op   7568 B/op   35 allocs/op
PATCHED   ~5640 ns/op   8360 B/op   40 allocs/op
```

≈ +10 % time, +5 allocs, +0.8 KB per decode. Accepted: definition decoding is a
load/reload path, not the per-token execution hot path, and definitions are
cached in front of the store. Recorded so a future reader can re-measure rather
than re-argue.

**Two measured costs the security review surfaced, one fixed here, one recorded.**

- **Fixed: a strict-decoding error could dwarf its own input.** `yaml.v3` emits
  one message per unknown key, so a document made of unknown keys produced an
  error string ~2.4x the input (measured: 1.9 MB in → 4.7 MB out; the reviewer
  saw 4.2 MB → 15.5 MB), and that error is logged in full server-side. The
  baseline reported nothing, so the cost is entirely new. `ParseYAML` now caps
  the per-field messages at 20 and appends a count. Ordinary syntax errors are
  untouched and keep their exact text and position. ⚠ The **parse time** for such
  a document is *not* a regression — `yaml.v3` is quadratic in key count
  regardless of `KnownFields` (baseline 83.7 s vs 86 s at n=200 000).
  ⚠ Billion-laughs is a **non-issue and slightly improved**: an alias bomb now
  dies on the first unknown top-level key before expansion.

- **Recorded, not fixed: memory amplification on deeply nested subprocesses.**
  Each nesting level constructs a `json.Decoder`, which buffers its input, making
  the cost O(input x depth) where `json.Unmarshal` decoded in place. Measured at
  depth 3000 / 806 KB input: **4.40 GB allocated vs 0.42 GB on baseline (10.5x)**,
  consistent at depths 500 and 1500; wall time unchanged. Depth self-limits at
  ~3300 levels via `encoding/json`'s 10 000-token guard, but input size is
  unbounded and the library imposes no size limit anywhere. Capping subprocess
  nesting is a **semantic** decision about definitions, not a decoding one, so it
  is filed as its own item rather than smuggled into this ADR.

**Negative / accepted costs.**

- **Breaking.** Any definition carrying an unknown key now fails to parse where
  it previously loaded. There is no opt-out and no deprecation period; the
  previous behaviour was a defect, not a configuration. This is affordable only
  because v0.1.0 is untagged — after a tag, this decision would have to be made
  differently.
- **The two decoders report differently.** `yaml.v3` returns a multi-error
  naming every unknown field with its line; `encoding/json` stops at the first
  and gives no position — executed with three unknown fields, it reported only
  `json: unknown field "un1"`. Normalising this would mean parsing
  `encoding/json`'s error text or hand-rolling a field walk, both costing more
  than the inconsistency. Recorded rather than hidden.
- **A stricter decoder is a larger error surface.** Definitions that previously
  loaded with a harmless stray key now fail closed. That is the intent, but it
  means the first upgrade after this change is where such keys are discovered —
  in the repo itself, six files carry definitions that must keep parsing (spec
  §2.7).
- **`Qualifier` and `NodeKind` keep custom unmarshalers.** Both decode scalars
  rather than objects, so they have no fields to be strict about and are
  unaffected. Any future custom unmarshaler over a *struct* would reintroduce
  this class of hole silently — the lesson generalises beyond this ADR.
