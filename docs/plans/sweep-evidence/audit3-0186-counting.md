# Audit round 3 (ADR-0186 re-cut) — COUNTING lens

Worktree: wt/counting, detached at bundle commit 6cddb7b1.
Step 0: all five bundle files PRESENT.

### C1 — "39 decode sites = 13 stdlib + 13 gin + 13 fiber + 0 httpcore; 36 propagating, 3 discarding; all in groups.go"
**Severity:** n/a (verification)
**Bundle says:** ADR §Context 1; plan §4 rows 1–3.
**Bundle's net:** line-oriented greps for `json.NewDecoder` / `ShouldBindJSON` / `c.Bind().JSON`.
**My INDEPENDENT net:** `go/parser` walk over EVERY non-test `.go` file under `transport/http/`,
matching by AST call shape (`*.Decode`, all six gin `*Bind*` methods, `Bind().JSON`, plus
`json.Unmarshal`, `io.ReadAll`, `c.Body`, `c.BodyRaw`), and classifying each call by its
*assignment context* (`assign lhs=err` vs `assign lhs=_` vs bare `ExprStmt`) rather than by
matching the `_ =` text.
**Observed:** 13 fiber / 13 gin / 13 stdlib, 0 in `httpcore`, 0 in `parity`; every one in
`groups.go`. Exactly one `lhs=_` per adapter: `fiber/groups.go:255`, `gin/groups.go:265`,
`stdlib/groups.go:238`. No other decode idiom exists in the tree (no `json.Unmarshal`, no
`io.ReadAll`, no other gin bind method).
**Verdict:** BUNDLE-CORRECT. 39 / 36 / 3 and the three discard line numbers all resolve.
**Fix:** none.

### C2 — ⛔ "404, 409 and 422 … each matches a small closed set of engine sentinels with no OPEN EXTENSION POINT" — the residual justification is FALSE for 404 and 422
**Severity:** CRITICAL
**Bundle says:** ADR-0186 Decision 2, "The bounded residual, stated rather than implied":
> "404, 409 and 422 also render `err.Error()` and were **not** proven to leak. They keep it here,
> for a reason that is checkable rather than hopeful: each matches a **small closed set of engine
> sentinels with no open extension point**, whereas the 400 arm matches eight sentinels across five
> groups from four packages **and** an *open* strategy set."
Repeated in Consequences ("404, 409 and 422 keep `err.Error()` … Bounded by the parser invariant")
and in plan §0 item 3, which correctly flags it as the thing to attack.

**Bundle's net:** none given for the *producers* — the sentence reasons about the **sentinel set**
only. That is the identical fallacy the same ADR retires two paragraphs earlier for the 400 arm:
*"the list asserts a property of the SENTINEL when the property belongs to the PRODUCING SITE"*.
The residual paragraph then re-commits it, on the arms nobody enumerated.

**My INDEPENDENT net:** enumerate the **7** residual sentinels from `errors.go` (404: `ErrInstanceNotFound`,
`ErrDefinitionNotFound`, `ErrTaskNotFound`; 409: `ErrConcurrentUpdate`; 422: `ErrConflict`,
`ErrInvalidTransition`, `ErrInvalidTask`), then grep **every non-test producing site** of each and
read the format string — then EXECUTE `ClassifyError` on each shape.

**Observed (executed, `go test -count=1 -run TestZZProbeResidualArmsEchoCallerInput -v ./transport/http/httpcore/`, PASS):**
```
404/ErrDefinitionNotFound (MapDefinitionRegistry.Lookup, definition_registry.go:56):
  status=404 body={Error:not_found Message:workflow-runtime: definition not found in registry: "kyc-ssn-123-45-6789"}
404/ErrTaskNotFound (processdriver_action.go:280 shape):
  status=404 body={Error:not_found Message:workflow-runtime: resolve candidates: no task record for "task-4111-1111-1111-1111": workflow-humantask: task not found}
422/ErrInvalidTask (humantask/validate.go:47):
  status=422 body={Error:conflict_state Message:workflow-humantask: invalid task: task "task-kyc:ssn-123-45-6789": unknown state 99}
422/ErrConflict (service.go:605, the `%w: %w` OPEN form):
  status=422 body={Error:conflict_state Message:workflow-service: conflicting state: json: unknown field "kyc:ssn-123-45-6789"}
422/ErrConflict (service.go:377):
  status=422 body={Error:conflict_state Message:workflow-service: conflicting state: instance "kyc:ssn-123-45-6789" is in a terminal state}
409/ErrConcurrentUpdate (bare):
  status=409 body={Error:conflict Message:workflow-runtime: concurrent update}      <-- the ONLY value-free arm
```

**Producer census (my grep, non-test, `errors.Is` sites excluded):**
- `ErrDefinitionNotFound` — **4 wrapping producers, ALL embedding a caller-controlled string**:
  `runtime/kernel/definition_registry.go:56` `%w: %q` on `q`; `runtime/kernel/mem_definition_registry.go:112`
  `%w: %q` on `q`; `internal/persistence/store/definitions.go:150` `%w: %s:%d` on `defID`;
  `:186` `%w: %s` on `q`. `q`/`defID` are `StartInput.DefRef` — **the caller's own body field**,
  i.e. the exact `POST /instances` value the ADR proves is attacker-chosen for the 400 arm.
- `ErrTaskNotFound` — bare at `humantask/memory.go:56` and `internal/persistence/store/humantask_store.go:206`,
  but **wrapped with a caller task id** at `engine/step_triggers.go:888` (`token %q`) and
  `runtime/processdriver_action.go:280` (`no task record for %q`).
- `ErrInvalidTask` — **3 producers, all `task %q` on the caller's TaskID**: `humantask/validate.go:47,51,53`.
- `ErrConflict` — **6 producers** in `service/service.go`: `:377`, `:540`, `:593`, `:600` embed a
  caller id with `%q`; **`:549` and `:605` are `fmt.Errorf("%w: %w", ErrConflict, err)` over an
  arbitrary downstream error** — `:605` wraps whatever `driver.ApplyTrigger` returns.
- `ErrInstanceNotFound`, `ErrConcurrentUpdate` — bare everywhere. Genuinely value-free.

**Verdict:** CONFIRMED-DEFECT, twice over.
1. **"no open extension point" is false as stated.** `service.go:549` and `:605` are
   `%w: %w` over an arbitrary error from `ProcessDriver.ApplyTrigger` — a **consumer-implementable
   interface**. That is a wider open extension point than `validate.Register`, which the ADR cites as
   the thing that disqualifies the 400 arm. `%w: %w` over an arbitrary error is **literally the
   `lister.go:66` shape** the same ADR calls "the finding that killed the sentinel-keyed design".
2. **"were not proven to leak" is now falsified.** 6 of the 7 residual sentinels have at least one
   producer that echoes caller-controlled bytes; only `ErrConcurrentUpdate` survives. The 404 case is
   the same `def_ref` string, on the same route (`POST /instances`), that Decision 2 blanks at 400 —
   so a caller who sends `def_ref: "kyc:ssn-123-45-6789"` gets it **blanked at 400 and echoed at 404**.

**Why this is Critical and not Major:** the delivery's headline claim is that 4xx bodies say only
what a producer vouched for. It ships that property for 400/403/413 and leaves the *same disclosure*
reachable on the *same input* through 404 and 422 — and it does so on the strength of a justification
this lens has now falsified by execution. Shipping it writes a false security statement into
`SECURITY.md` (phase 7 prescribes documenting "what a 400/403/413 body does and does not contain",
silent on 404/422).

**Fix (concrete, pick one):**
- **Preferred:** extend deny-by-default to 404, 409 and 422 **in this delivery**. The mechanism
  already exists and costs nothing extra — the arms become
  `ClientSafeMessage` or static (`"not found"` / `"conflict"` / `"conflict_state"`). Then opt in the
  producers that are genuinely value-free (`ErrConcurrentUpdate` bare; `ErrInstanceNotFound` bare).
  This *removes* a phase-1 test rather than adding one: `TestFourOhFourNineTwentyTwoResidualIsPinned`
  (plan phase 1 test 6) is replaced by extending `TestFourHundredRendersOnlyAVouchedMessage` to all
  four arms.
- **Minimum, if the residual is kept:** delete the false justification and replace it with the
  executed truth — *"404 and 422 are KNOWN to echo caller-supplied identifiers (`def_ref`, task id,
  instance id) and, at `service.go:549,605`, arbitrary driver errors; this is an accepted, documented
  disclosure for this delivery"* — and say so in `SECURITY.md` and the backlog item. Do **not** ship
  the sentence "were not proven to leak"; it has been proven.
- Either way the ADR's Consequences bullet *"404, 409 and 422 keep `err.Error()` … it is a stated
  gap, not closure"* must state **what** is disclosed, not just that something is.

### C3 — "the FOUR static cursor forms" vs the FIVE line numbers listed beside it
**Severity:** MAJOR (cross-document, in the Decision text phase 3 implements)
**Bundle says:** ADR-0186 Decision 2:
> "Those two sites render static text. **The four static forms**
> (`lister.go:69,77,90`, `armed_timer_paging.go:92,99`) **do** opt in"
`lister.go:69,77,90` is 3 anchors + `armed_timer_paging.go:92,99` is 2 = **five**, under the word
"four". The wrong number appears **5 times**: ADR `:323`, `:481` ("the four static cursor forms opt
in"), `:500`; spec §5 row `D2 × ADR-0146/0152/0183` (`:155`); plan §2 decision→phase map (`:169`).
The right number appears 3 times, all in the plan: `:357`, `:375`, and §4's "2 echoing + 5 static".

**Bundle's net:** none — prose, restated.
**My INDEPENDENT net:** repo-wide grep for both sentinel identifiers over all non-test `.go` files,
then read every producing line.
**Observed (verbatim, non-test):**
```
runtime/kernel/lister.go:66              fmt.Errorf("%w: %w", ErrBadCursor, err)                          <- ECHOES
runtime/kernel/lister.go:69              fmt.Errorf("%w: not an instance cursor", ErrBadCursor)           <- static
runtime/kernel/lister.go:77              fmt.Errorf("%w: cursor carries no instance identity", ErrBadCursor) <- static
runtime/kernel/lister.go:90              fmt.Errorf("%w: cursor carries no start time", ErrBadCursor)     <- static
runtime/kernel/armed_timer_paging.go:89  fmt.Errorf("%w: %w", ErrBadArmedTimerCursor, err)                <- ECHOES
runtime/kernel/armed_timer_paging.go:92  fmt.Errorf("%w: not an armed-timer cursor", …)                   <- static
runtime/kernel/armed_timer_paging.go:99  fmt.Errorf("%w: cursor carries no timer identity", …)            <- static
```
Two files only (`internal/persistence/store/lister.go` exists but produces neither sentinel).
**Verdict:** the plan's §4 row (**7 = 2 echoing + 5 static**) is BUNDLE-CORRECT and every one of its
seven line anchors resolves. The **ADR and spec are CONFIRMED-DEFECT** — "four static forms" is
wrong; it is **five**.
**Provenance (the interesting part):** "four" is the hedge-stripped restatement of ADR `:124`'s
correct sentence *"The row quoted **two of the four** wrap forms"* — which counts **`ErrBadCursor`'s**
four forms (66,69,77,90). Restated across **both** sentinels it silently became "the four static
forms". This is verbatim the Premise Discipline failure mode: the detailed reasoning is right, the
summary sentence appended to it over-generalises, and the number that rotted was **inherited from a
correctly-hedged sentence one page earlier**.
**Fix:** replace "four" with "five" at ADR `:323`, `:481`, `:500`, spec `:155`, plan `:169`. Keep
ADR `:135` ("`ErrBadCursor` | its three static wrap forms") — that row is correct and scoped.
Prefer the plan's phrasing, which names the count *and* the split: "2 echoing + 5 static = 7".

### C4 — ⭐ "**48 columns** carry a free-form type (`TEXT`/`JSON`/`JSONB`)" is a POSTGRES number stated as a schema-wide fact. On SQLite it is **67**.
**Severity:** MAJOR
**Bundle says:** ADR-0186 Context §3 ("Derived by machine this time (Evidence §6.1), over all three
dialect files: … **48 columns** carry a free-form type (`TEXT`/`JSON`/`JSONB`)"); repeated in
Decision 3 ("48 columns carry a free-form type and most are identifiers") and plan §4
("**at-rest: free-form columns** | ⭐ **48** (`TEXT`/`JSON`/`JSONB`)").
**Bundle's net:** Evidence §6.1's own walk — which, per the surrounding text, classifies by the
declared type string.
**My INDEPENDENT net:** a bracket-depth SQL parser (not a line regex) over all three
`0001_init.sql` files, counting columns whose declared type begins TEXT/JSON/JSONB/VARCHAR/CHAR/
CLOB/BLOB/BYTEA, **per dialect**.
**Observed:**
```
postgres  tables=9  totalcols=79  free-form=48
mysql     tables=9  totalcols=79  free-form=48
sqlite    tables=9  totalcols=79  free-form=67
```
**Verdict:** CONFIRMED-DEFECT. 48 is true of postgres and mysql and **false of sqlite**, where
`TIMESTAMPTZ`→`TEXT` and `JSONB`→`TEXT` push 19 more columns into the free-form class.
**Why this matters beyond arithmetic:** it is the **identical failure the same paragraph claims to
have fixed**. ADR `:161-165` says the `trigger_` divergence was missed "because every round
enumerated columns from one dialect and assumed the other two matched" — and the bullet **three
lines below it** states a single-dialect column count as a schema-wide fact. This is the
"celebratory sentence written against a premise a sibling fix already changed" shape: the walk
*was* run over all three files (the table/name comparison is correct), and then the free-form count
was reported from one of them.
**Fix:** state it per dialect — "48 on postgres and mysql, **67 on sqlite**, where `TIMESTAMPTZ` and
`JSONB` are both spelled `TEXT`" — in ADR Context §3, ADR Decision 3 and plan §4. And add the
consequence the number now carries: **a type-based `discloses` heuristic cannot be reused across
dialects**, so phase 6's classification must be keyed on `(table, column)`, never on the declared
type. Nothing in the plan currently says this, and a phase-6 implementer reading "48 free-form
columns" would reasonably build the classifier off the type string.

---

### C5 — the `trigger` / `trigger_` divergence, and whether a SECOND one exists
**Severity:** n/a (verification — closes an open bundle question)
**Bundle says:** ADR Context §3 / Decision 3 / plan §4: "**1 known** — `wrkflw_journal.trigger` vs
MySQL's **`trigger_`** … ⚠ Assume there could be a second"; ADR Consequences opens a backlog item
*"a second per-dialect schema-name divergence, if one exists"*; plan §0 item 5 tells the auditor to
hunt one.
**My INDEPENDENT net:** the same bracket-depth parser, comparing postgres↔mysql and
postgres↔sqlite on **four** axes the bundle does not check — column NAME, column ORDER, declared
TYPE, and NOT NULL.
**Observed:**
```
NAME-DIVERGENCE  wrkflw_journal.trigger   present in postgres, ABSENT in mysql
NAME-DIVERGENCE  wrkflw_journal.trigger_  present in mysql,    ABSENT in postgres
ORDER-DIVERGENCE wrkflw_journal  postgres=[instance_id,seq,kind,trigger,occurred_at,applied_at]
                                 mysql=[instance_id,seq,kind,trigger_,occurred_at,applied_at]
name=2 order=1 type=107 null=0
```
(`mysql/0001_init.sql:31` vs `postgres/0001_init.sql:30` — both anchors resolve.)
**Verdict:** BUNDLE-CORRECT, and the open question is now **answered**: there is **exactly one**
name divergence in the whole schema, the one already found. The single ORDER divergence is the same
column and not independent. **NOT NULL agrees on all 79 columns in all three dialects** — a
non-obvious positive result worth pinning.
**Fix:** downgrade the Consequences backlog item from *"a second divergence, if one exists"* to
*"swept this session over name/order/type/nullability across all three dialects: exactly one name
divergence exists (`trigger`/`trigger_`); nullability agrees on all 79 columns; 107 type spellings
differ by design"* — and record the 107 so phase 6's allow-list is not surprised by them.
Also pin `wrkflw_journal` = **6** columns (ADR Context §3) — CONFIRMED:
`[instance_id, seq, kind, trigger, occurred_at, applied_at]`.

### C6 — ⛔ "**4** validation strategies … the rendering is keyed on strategy KIND" — `callback` HAS NO KIND, and the kind literal for jsonschema is `"json-schema"`
**Severity:** CRITICAL
**Bundle says:**
- plan §4: "validation strategies under `ErrInvalidInput` | **4** in-repo — `jsonschema`, `expr`,
  `avro`, `callback`; only `jsonschema` yields structured leaves. ⚠ The **class is open**
  (`validate.Register` is exported)"
- ADR Decision 2: "`validation.ErrInvalidInput` wraps **four** strategies — `jsonschema`, `expr`,
  `avro`, `callback`"
- plan phase 2: "**The rendering is keyed on strategy kind** (the set is open — `validate.Register`
  is exported): `jsonschema` → … `avro` → static … `callback` → static unless … **unknown kind →
  static**", plus test 6 `TestUnknownStrategyKindRendersStatically` — "register a throwaway kind via
  `validate.Register`. ⚠ This is what makes deny-by-default true over an **open** set."

**Bundle's net:** prose; the four names are treated as a homogeneous, kind-keyed set.
**My INDEPENDENT net:** read the interface declarations rather than the strategy list —
`definition/model/validate/validate.go`, `registry.go`, each strategy's `register.go` — then
EXECUTE.
**Observed (`go test -count=1 -run TestZZProbeStrategyKindIsVisible -v ./runtime/validation/`, PASS):**
```
registry Kind constants: jsonschema="json-schema" expr="expr" avro="avro"
callback implements DescribableStrategy: false (ds=<nil>)
Gate.Validate(callback) err = "workflow-validation: invalid input: consumer message with 4111-1111-1111-1111"
  errors.Is(err, validation.ErrInvalidInput) = true
Gate.Validate(callback returning a sentinel) -> errors.Is(err, sentinel) = false
Registry.Strategy(Kind:"jsonschema")  -> workflow-validation: unknown validation kind: "jsonschema"
Registry.Strategy(Kind:"json-schema") -> <nil>
```
**Verdict:** CONFIRMED-DEFECT, three ways.

1. **There is no kind to key on for `callback`.** `validate.ValidationStrategy` is
   `interface{ NewValidator() (Validator, error) }` — **no `Kind`**. Kind exists only on
   `validate.DescribableStrategy` via `Descriptor().Kind`. `callback.New` returns an unexported
   `strategy` that is **not** describable — and `definition/model/validate/callback/callback_test.go`
   *asserts* it: `if _, isDesc := s.(validate.DescribableStrategy); isDesc { t.Fatal("callback
   strategy must NOT implement DescribableStrategy") }`. So phase 2's "keyed on strategy kind" with a
   `callback` row is **not implementable as written**. The real discriminator is
   `DescribableStrategy` yes/no, and *within* the describable branch, `Descriptor().Kind`.
   The intended *behaviour* still works (non-describable ⇒ static unless it vouches), but the
   prescribed *mechanism* and its table do not describe the code.

2. **"4 strategies" conflates two different memberships.** Three are registry kinds registered by
   `init()` (`expr/register.go:8`, `jsonschema/register.go:8`, `avro/register.go:8`); `callback` is a
   direct-construction strategy with **no `Kind` constant and no `Register` call** — I grepped every
   `Register(` site, non-test, and there are exactly three. State it as **"3 registered kinds +
   `callback`, which is constructed directly and carries no kind"**, or the phase-2 implementer
   builds a `switch kind` with a dead `callback` arm.

3. ⭐ **The kind literal is `"json-schema"`, not `"jsonschema"`.** The bundle writes `jsonschema`
   in the plan §4 row, ADR Decision 2, plan phase 2 and phase 5. An implementer writing
   `case "jsonschema":` gets a **silent fall-through to static text**, which destroys the single
   structured rendering the delivery exists to produce — and **phase 2 test 1 rows 1–3 still pass**,
   because static text contains none of the forbidden values. Only row 4 (the anti-vacuity control,
   "non-empty and names a keyword") would catch it, which is exactly why that row must not be
   dropped. Executed: `Registry.Strategy(Kind:"jsonschema")` → `unknown validation kind`.

**Bonus (independent confirmation of an inherited claim):** the bundle's twice-repeated
`gate.go:45` claim — `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` flattens the typed error —
is **BUNDLE-CORRECT**, anchor and all: line 45 is exactly that statement, and executed,
`errors.Is(err, sentinel)` is **false** after the gate.

**Fix:**
- Re-word plan phase 2 and ADR Decision 2: the rendering keys on **`validate.DescribableStrategy`
  first** (absent ⇒ static-unless-vouched, which covers `callback` *and* every consumer strategy),
  then on `Descriptor().Kind` for the three registered kinds. Say "3 registered kinds + `callback`".
- Replace the literal `jsonschema` with **`jsonschema.Kind` (`"json-schema"`)** everywhere the
  rendering keys on it, and have the phase-2 brief reference the constant, never the string.
- Rewrite plan phase 2 test 6: `validate.Register` only populates `validate.DefaultRegistry()`, which
  **`Gate` never consults** (`Gate.Validate` takes a strategy *value*). As prescribed the test does
  not connect to the SUT. It must construct the strategy through
  `validate.DefaultRegistry().Strategy(ValidationDescriptor{Kind:"throwaway", …})` and pass *that* to
  the gate — otherwise it is the fourth unfailable "invariant" in this lineage.

### C7 — the LINE-ANCHOR sweep (all 56 distinct citations in the five files)
**Severity:** MINOR (2 imprecise anchors; the rest resolve)
**My INDEPENDENT net:** extracted every `<file>.(go|sql|md):<line>` token from all five bundle files
(56 distinct), then `sed -n '<n>p'` each one — including the vendored `fiber/v3@v3.4.0` and
`expr@v1.17.8` citations against the real module cache, with the versions cross-checked against
`go.mod`.
**Observed — RESOLVE CORRECTLY (spot list):**
`httpcore/errors.go:28,31,32,33,34,35,36-50,51,56,57,58` (every `ClassifyError` arm anchor in both
ADR and plan §4) · `admin_endpoints.go:30` = `fmt.Errorf("%w: unknown status %q", ErrBadInput, s)` ·
`runtime/validation/gate.go:45` = `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` ·
`internal/expreval/expreval.go:135` = `run %q: %w` · `definition/model/validate/expr/expr.go:64,68`
= both `%q` on `v.source[i]` · `lister.go:66,69,77,90` · `armed_timer_paging.go:89,92,99` ·
`stdlib/groups.go:238`, `gin/groups.go:265`, `fiber/groups.go:255` (all three `_ =` sites) ·
`action/httpcall/httpcall.go:94` = `ErrBodyTooLarge` · `engine/terminal_sites_test.go:88` = the
`parser.ParseFile(..., "fixture.go", src, 0)` self-test the bundle holds up as the pattern ·
`mysql/0001_init.sql:31` = `trigger_ JSON NOT NULL` · `postgres/0001_init.sql:30` = `trigger JSONB
NOT NULL` · `fiber/v3@v3.4.0/app.go:585` = `DefaultBodyLimit = 4 * 1024 * 1024`, `:710` = applied in
`New()`, `req.go:146` = the "will decompress" comment, `req.go:92-96` = `BodyRaw`. `go.mod` pins
`gofiber/fiber/v3 v3.4.0` and `expr-lang/expr v1.17.8`, so both vendor anchors are version-correct.
**Observed — TWO IMPRECISE:**
1. **`dto.go:174`** (ADR Decision 2 §3, plan phase 1) is the *continuation* line `ErrBadInput, s)`.
   The statement begins at **`:173`**. More useful than the off-by-one: the prescription is
   *"name the allowed set instead of the rejected input"*, but `:173` **already names the allowed
   set** — `"disposition must be one of retry, skip, abandon (got %q)"`. The actual edit is *drop
   the `(got %q)` tail*, and as written an implementer could read the instruction as satisfied.
   (`admin_endpoints.go:30`, `"unknown status %q"`, does **not** name the set — the prescription is
   right for that one, so the two sites need *different* edits, not one.)
2. **`doc.go:66`** (ADR Decision 2, correlation-id paragraph) resolves to the **module-root**
   `./doc.go:66` (`//     ClassifyError (5xx redaction), Instrumentation.Observe …`), not to
   `transport/http/httpcore/doc.go` — **which does not exist**. In a paragraph otherwise entirely
   about `httpcore`, the bare filename reads as the package's own. The claim it supports
   (`ClassifyError` is an advertised consumer seam) is TRUE; only the path is ambiguous.
**Verdict:** BUNDLE-CORRECT on 54 of 56; 2 imprecise, neither load-bearing.
**Fix:** `dto.go:174` → `dto.go:173`, and split the prescription into two per-site edits;
`doc.go:66` → `./doc.go:66` (module root).

### C8 — ⭐⭐ The plan's §4 table reintroduces the LITERAL-PIPE grep **in the row directly below the warning about it** — and prescribes it as a phase-4 verification
**Severity:** MAJOR (third recurrence in this lineage; the claim survives, the *verification* does not)
**Bundle says:** plan §4 header:
> "⚠ Every row was re-run in the working tree. **Bare `|` under `-E`** — `\|` in ERE is a *literal*
> pipe, which is how an earlier revision's '0 existing caps' evidence became a command that returns
> 0 for **any** repository."
…and then, four lines later, plan §4 row 4:
> "…already capped by us | **0** — `grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/`
> exits 1. ⚠ **After phase 4 this must return 26** (stdlib 13 + gin 13)"

**Bundle's net:** that command — which contains `\|` under `-E`, i.e. the exact defect the header
warns about, with **three** literal pipes instead of the earlier revision's one.
**My INDEPENDENT net:** run the plan's command verbatim, run the ADR's command verbatim, and add a
**positive control** — a file whose only content is the literal string
`MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader`.
**Observed:**
```
(A) plan §4 verbatim:  grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/   -> EXIT=1
(B) ADR §Context 1:    grep -rnE "MaxBytesReader|BodyLimit" transport/                                   -> EXIT=1
(C) CONTROL, (A) against a file containing that literal string:
    /tmp/literalpipe.txt:1:MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader                          -> EXIT=0
(D) correct form, repo-wide non-test:
    action/httpcall/httpcall.go:194:  b, err := io.ReadAll(io.LimitReader(r, max+1))                     -> EXIT=0
```
The control is the proof: (A) matches **only** the literal 46-character string and is blind to every
real occurrence. It would exit 1 on this repository whatever `transport/` contained.
**Verdict:** the **claim** ("0 caps in `transport/` today") is BUNDLE-CORRECT — (B), which is
well-formed, independently confirms it, and (D) shows the single pre-existing bound in the repo is
`action/httpcall/httpcall.go:194`'s outbound `io.LimitReader`, correctly out of scope. The
**command** is CONFIRMED-DEFECT.
**Why it is not cosmetic:** the row does not merely cite evidence, it **prescribes a phase-4
verification** — *"after phase 4 this must return 26"*. An implementer who installs all 26
`MaxBytesReader` calls correctly and runs the command as written gets **exit 1**, and the honest
readings are "the caps did not land" or "this check is noise". Both are wrong, and the second is how
the defect survived two previous rounds. The arithmetic behind 26 (stdlib 13 + gin 13, fiber excluded
because it uses a `BodyRaw()` pre-check) is **correct** — I confirmed 13 decode sites per adapter in
C1 — so only the command needs fixing.
**Fix:** replace with `grep -rnE 'MaxBytesReader|MaxBytesHandler|io\.LimitReader' transport/` (drop
`BodyLimit`: it is a fiber *config field name* and will not appear at a site we own), and state the
post-phase-4 expectation as **26 matches across `stdlib` and `gin` only**. ⭐ **And the structural
fix, not the textual one:** this is the third time a prose warning has failed to make the adjacent
prose command reliable. Put the assertion in the phase-4 test as a
`go/parser` walk counting `http.MaxBytesReader` call sites per adapter package — the same remedy
Decision 3 already adopts for the column list, applied to the one row that keeps re-breaking.

### C9 — ⛔⛔ "`httpcore.Validate` (the DTO validator, **every POST/PUT on all 26 routes**)" is FALSE — it runs on **3** routes — and that false count is what makes the ADR believe it has protected ADR-0146/0152/0183. It has not.
**Severity:** CRITICAL — the decisive one. An enumeration error causing a design error.
**Bundle says:**
- ADR Decision 2: "1. **`httpcore.Validate`** (the DTO validator, **every POST/PUT on all 26
  routes**) **opts in.** Executed value-free even for a length constraint (Evidence §1), **so
  ADR-0146/0152/0183's actionable messages survive.**"
- ADR Consequences: "**The actionable 400 messages ADR-0146, ADR-0152 and ADR-0183 added survive**,
  because `httpcore.Validate` and the four static cursor forms opt in".
- spec §5, row **D2 × ADR-0146/0152/0183**, marked **✅ resolved**: "Deny-by-default with a producer
  opt-in: `httpcore.Validate` and the four static cursor forms opt in, **so those messages survive**".
- plan phase 1 test 7 `TestValidateMessageIsVouchedAndUnchanged`: "⚠ Round 3's finding F4 was that
  the previous design destroyed messages ADR-0146/0152/0183 deliberately added; **only a positive
  assertion protects them.**"

**Bundle's net:** none — the parenthetical is asserted.
**My INDEPENDENT net:** (a) grep every call site of `Validate` in `transport/` non-test; (b) grep
every `validate:` struct tag in `dto.go`; (c) EXECUTE `ClassifyError` on each of the four engine
sentinels whose in-code rationale `errors.go:38-49` attributes to ADR-0146/0152/0183, and test
whether the `httpcore.Validate` opt-in could reach them.

**Observed:**
```
call sites of httpcore.Validate (non-test, whole transport/ tree) — THREE, all in endpoints.go:
   endpoints.go:26  StartInstance     endpoints.go:83  DeliverSignal     endpoints.go:101 DeliverMessage
`validate:` tags in dto.go — THREE of the ELEVEN *Input types:
   StartInput.DefRef `validate:"required"` · SignalInput.Signal `validate:"required"` · MessageInput.Name `validate:"required"`
   (ClaimInput, CompleteInput, ReassignInput, PolicyRuleInput, RoleBindingInput, RedriveInput,
    ResolveIncidentInput, ResolveCompensationStallInput carry NO validate tags and never reach Validate)
```
executed (`go test -count=1 -run TestZZProbeWhichFourHundredMessagesSurvive -v ./transport/http/httpcore/`, PASS):
```
httpcore.Validate  status=400
    message="workflow-httpcore: bad input: Key: 'dto.def_ref' Error:Field validation for 'def_ref'
             failed on the 'required' tag\nKey: 'dto.note' Error:Field validation for 'note' failed on the 'max' tag"
ADR-0146 ErrInvalidOutcome       status=400  TODAY message="workflow-engine: completion outcome is not declared by the user task"
                                   errors.Is(err, httpcore.ErrBadInput) = false
ADR-0146 ErrOutcomeRequired      status=400  TODAY message="workflow-engine: user task requires a completion outcome"
                                   errors.Is(err, httpcore.ErrBadInput) = false
ADR-0152 ErrEmptyTriggerKey      status=400  TODAY message="workflow-engine: trigger identity key is empty"
                                   errors.Is(err, httpcore.ErrBadInput) = false
ADR-0183 ErrEmptyReassignTarget  status=400  TODAY message="workflow-engine: reassignment target is empty"
                                   errors.Is(err, httpcore.ErrBadInput) = false
```
**Verdict:** CONFIRMED-DEFECT, in two layers.

**Layer 1 — the count.** `httpcore.Validate` covers **3 routes** (`POST /instances`,
`POST /instances/{id}/signals`, `POST /messages`), not 26 and not "every POST/PUT" — there are
**13** body-bearing routes per adapter (C1). It is invoked from 3 of the 24 exported endpoint
functions and only 3 of the 11 DTOs even carry a `validate:` tag. Evidence §1's *value-freedom*
result is BUNDLE-CORRECT (I reproduced it, including for a `max=` length constraint — the
go-playground message names the **tag**, never the value or the length). Only the **coverage**
claim is false.

**Layer 2 — what the false count causes.** ADR Context §2 states the hazard itself, in bold:
*"⚠⚠ **What the 400 arm must STILL DO** … `errors.go:38-41` (ADR-0146) … `:43-46` (ADR-0152) …
`:47-49` (ADR-0183) … **Blanking the arm wholesale silently retires three ADRs.**"* Deny-by-default
blanks every 400 whose chain carries no `ClientSafeMessage`. Executed above: **none of the four
sentinels those three ADRs added is reachable from `httpcore.Validate` or from a cursor form** —
they are `engine.*` sentinels in their own `errors.Is` groups, and `errors.Is(err, ErrBadInput)` is
**false** for all four. **No phase opts them in**: plan §2's decision→phase map has rows for
`httpcore.Validate`, the two `ErrBadInput` wrap sites, and the cursor forms — and **no row for any
`engine.*` sentinel**. So this delivery ships:
```
  "workflow-engine: completion outcome is not declared by the user task"  ->  "invalid input"
  "workflow-engine: user task requires a completion outcome"              ->  "invalid input"
  "workflow-engine: trigger identity key is empty"                        ->  "invalid input"
  "workflow-engine: reassignment target is empty"                         ->  "invalid input"
```
which is **exactly the outcome the ADR names as unacceptable**, marked ✅ resolved in spec §5, and
guarded by a test (`TestValidateMessageIsVouchedAndUnchanged`) that asserts only
`httpcore.Validate`'s message and therefore **cannot fail when all four are blanked**. A cited test
is not a covering test.

**Fix:**
1. Correct the count everywhere: "`httpcore.Validate` — the DTO validator on **3 routes**
   (`POST /instances`, `/instances/{id}/signals`, `/messages`); only `StartInput`, `SignalInput` and
   `MessageInput` carry `validate:` tags".
2. **Add a phase-1 work item and a §2 map row: the four `engine.*` 400 sentinels opt in.** All four
   messages are executed value-free above — they are fixed strings naming a *node contract*, with no
   caller content — so `SafeMessage` wrapping them is safe and cheap. Decide where: wrapping at the
   `engine` sentinel declaration would make `engine` depend on the interface (legal — it is satisfied
   structurally, same argument as `runtime/validation`), or `ClassifyError` can carry an explicit
   vouched-sentinel set for these four *with a policy-table row each*, which the phase-1 `go/parser`
   invariant then pins.
3. **Fix the test that is supposed to protect them.** `TestValidateMessageIsVouchedAndUnchanged`
   must gain a row per ADR-0146/0152/0183 sentinel asserting the specific message survives.
   Stated falsifier: *it fails against any implementation that blanks the 400 arm except
   `httpcore.Validate`* — i.e. against the bundle as currently written.
4. Change spec §5's D2 × ADR-0146/0152/0183 row from ✅ to ⚠ until (2) lands.

---

### C10 — "There are **12** `*Input` types"
**Severity:** MINOR
**Bundle says:** plan §0 item 2 ("There are 12 `*Input` types; the author found one reaching a custom
unmarshaller … by walking them"); plan §4 ("not reachable from any of the **12** `*Input` types").
**My INDEPENDENT net:** `grep -rn --include='*.go' "^type [A-Za-z0-9_]*Input\b"` repo-wide, non-test,
cross-checked against the decode targets my AST walk found (`var in httpcore.XxxInput` at all 13
stdlib sites).
**Observed:** **11** `*Input` types, all in `transport/http/httpcore/dto.go` (`StartInput`,
`SignalInput`, `MessageInput`, `ClaimInput`, `CompleteInput`, `ReassignInput`, `PolicyRuleInput`,
`RoleBindingInput`, `RedriveInput`, `ResolveIncidentInput`, `ResolveCompensationStallInput`).
The 12th match repo-wide is **`engine.CompletionInput`** (`engine/trigger.go:269`), which is not a
decode target and not reachable from a request body. The 13 decode sites map onto these 11 types
(`PolicyRuleInput` and `RoleBindingInput` each decode at two sites — POST and DELETE).
**Verdict:** CONFIRMED-DEFECT (harmless superset — the author's walk covered the relevant 11).
**Fix:** "**11** `*Input` DTOs in `httpcore/dto.go`, decoded at 13 sites". Note the 11≠13 relation
explicitly, or the next reader treats a mismatch as a missed site.

---

### C11 — the reachability walk itself: "exactly one `*Input` field reaches a custom `UnmarshalJSON`"
**Severity:** n/a (verification — the bundle's highest-risk claim, and it holds)
**Bundle says:** plan §4: "custom `UnmarshalJSON` reachable from a decode target | **1 of 3 in the
repo** — `model.Qualifier` (via `StartInput.DefRef`). `ProcessDefinition` and `NodeKind` are not
reachable from any of the … `*Input` types, because there is **no definition-deploy route**".
Plan §0 item 2 asks the auditor to walk them again because "that walk is the net".
**My INDEPENDENT net:** not a source walk — **`reflect`**, which is what `encoding/json` itself
dispatches on. A test in `httpcore_test` walks all 11 decode-target types transitively (structs,
pointers, slices, arrays, map keys AND values, embedded fields) and reports every type implementing
`json.Unmarshaler` **or `encoding.TextUnmarshaler`** — the latter being the fallback net a source
grep for `UnmarshalJSON` is blind to.
**Observed (`TestZZProbeUnmarshallerReachability`, PASS):**
```
distinct types reached: 20
HIT  json.Unmarshaler   StartInput.DefRef            type=model.Qualifier
HIT  INTERFACE (any)    StartInput.Vars{val}         type=interface {}
HIT  INTERFACE (any)    SignalInput.Payload{val}     type=interface {}
HIT  INTERFACE (any)    MessageInput.Payload{val}    type=interface {}
HIT  INTERFACE (any)    CompleteInput.Output{val}    type=interface {}
TOTAL HITS: 5  (one Unmarshaler, four `any` sinks, ZERO TextUnmarshaler)
```
Cross-checks: exactly **3** custom `UnmarshalJSON` in the repo non-test (`model.Qualifier`
`qualifier.go:64`, `model.ProcessDefinition` `node_wire.go:184`, `model.NodeKind`
`nodekind_json.go:44`) — matching "1 of 3"; and **zero** `UnmarshalText` anywhere non-test, so the
`encoding.TextUnmarshaler` fallback contributes nothing.
**Verdict:** BUNDLE-CORRECT. No second custom unmarshaller is reachable.
**Worth folding in as a strengthening note:** `Qualifier.UnmarshalJSON` (`qualifier.go:64-67`) does
`json.Unmarshal(b, &s)` into a string and **returns that error unmodified**, so a non-string
`def_ref` yields a bare `*json.UnmarshalTypeError` — which the conditional vouch (plan phase 4)
**vouches for**. That is the intended behaviour and it is safe (the message names Go types only),
but the ADR's producer table row 3 says a custom unmarshaller yields "neither (`*fmt.wrapErrors`)".
That is true only for the *ParseQualifier* branch; the *inner-unmarshal* branch yields
`*json.UnmarshalTypeError`. Split the row in two so phase 4's implementer is not surprised that
`{"def_ref": 4111111111111111}` takes the **vouched** path — which is exactly what plan phase 4
test 7 row 2 expects, so the two are consistent; only the ADR table is under-specified.

### C12 — ⭐ "The delivery is **three decisions in FIVE packages**, against six decisions in nine — which is the whole point of the re-cut"
**Severity:** MAJOR (celebratory Consequences sentence; the re-cut's own justification)
**Bundle says:** ADR-0186 Consequences → Positive, final bullet.
**Bundle's net:** none.
**My INDEPENDENT net:** enumerate the distinct Go packages named in the plan's own §2
decision→phase map and phase table — the authoritative statement of what this delivery touches.
**Observed — NINE distinct Go packages:**
```
1 transport/http/httpcore                 (phase 1)
2 runtime/validation                      (phase 2)
3 definition/model/validate/expr          (phase 2)
4 runtime/kernel                          (phase 3)
5 transport/http/stdlib                   (phase 4)
6 transport/http/gin                      (phase 4)
7 transport/http/fiber                    (phase 4)
8 transport/http/parity                   (phase 5)
9 internal/persistence/store              (phase 6)
                                          (phase 7 = docs, not a Go package)
```
Corroborated by plan §1's own container-free list, which names eight of them by hand
(`httpcore`, `stdlib`, `gin`, `fiber`, `parity`, `runtime/validation`, `runtime/kernel`,
`definition/model/validate/*`) plus `internal/persistence/store` as the Docker-needing ninth.
**Verdict:** CONFIRMED-DEFECT. The package count is **nine**, i.e. **unchanged** from the
six-decision bundle the sentence contrasts it with. The re-cut halved the *decisions* (6→3, and the
D×D pair count 15→3, both correct and genuinely the safety property) but did **not** reduce the
package footprint at all — and the sentence claims exactly that reduction as "the whole point".
**Why it is not cosmetic:** CLAUDE.md rule #11 fans out **by Go package**, and this plan schedules
7 phases over 9 packages with a 3-agent parallel wave. "Five packages" understates the coordination
surface a controller is being asked to accept, in the one sentence a reader would use to judge
whether the re-cut is small enough to proceed.
**Fix:** "The delivery is **three decisions** — 3 D×D interaction pairs instead of 15 — across the
**same nine packages**. The re-cut buys interaction safety, not a smaller footprint." Cutting the
honest version is better than the flattering one, and it is still a strong argument.

---

### C13 — three MINOR count/anchor items, batched
**Severity:** MINOR

**(a) "`keywordLocation`'s value-freedom across FIFTEEN schema shapes"** — spec §6 "Discharged —
do not re-derive", and deferred-slices `:285`.
*My net:* trace the number to its source. Evidence §2's probe declares **three** schema shapes
(`closed-properties/pattern`, `caller-chosen-key/propertyNames`, `array-item`) and its own results
table says "value-free in **all three shapes**". Round-4's execution lens
(`reaudit-0186-execution.md:491`) adds "**eleven further** schema shapes" and then footers
*"Fifteen schema shapes have now been executed"* — 15 only if the earlier evidence contributed
**four**, which it did not: the "four" in that lens's quoted claim is four **`keywordLocation`
values**, not four shapes. 3 + 11 = **14**.
*Verdict:* CONFIRMED-DEFECT (off by one), inherited from an audit write-up and restated as plain
fact in the one list that tells the reader **not to check it**. The substantive claim is robust
either way — 14 shapes is overwhelming evidence — so this is severity-Minor, but it is textbook
"restating strips the hedge".
*Fix:* "**fourteen** schema shapes (3 in Evidence §2 + 11 in round 4's E7)", and say where each
half came from so the next restatement cannot re-inflate it.

**(b) "the leak is confined to the eval-error arm"** — ADR Context §2.
*My net:* grep every non-test producer of `authz.ErrNotAuthorized`. **Eight** sites: six return it
bare (`authz/authz.go:127,141`, `internal/authz/casbin/authorizer.go:51,62,73`,
`processtest/spyauthz.go:71`) and **two** wrap an eval error —
`authz/authz.go:138` **and** `internal/authz/casbin/authorizer.go:70`, both
`fmt.Errorf("%w: attribute predicate: %w", ErrNotAuthorized, err)`.
*Verdict:* BUNDLE-CORRECT in substance (the leak really is confined to the eval-error path, and
D2 makes 403 static regardless), but the ADR names only one source (`expreval.go:135`) where there
are **two wrap sites** feeding from it. Also worth stating: `authz.Authorizer` is a **pluggable
consumer interface** (`processtest/spyauthz.go` documents wrapping it), so the 403 arm has an open
extension point too — which *strengthens* D2's "403 static, no opt-in" and should be cited as a
reason for it rather than left unsaid.
*Fix:* "two wrap sites (`authz/authz.go:138`, `internal/authz/casbin/authorizer.go:70`), both over
`expreval.go:135`'s error", and add the pluggable-Authorizer argument to the 403 row.

**(c) Evidence file section order:** `## 6.6` (line 537) precedes `## 6.5` (line 567). Both are
cited by number from the ADR and plan. Harmless but confusing when an implementer is told to read
"§6.5 What §6 does NOT establish" as the boundary statement and finds §6.6 above it.
*Fix:* renumber or reorder.

---

## SUMMARY

| # | finding | severity | verdict |
|---|---|---|---|
| C1 | 39 decode sites / 36 propagating / 3 discarding, all in `groups.go` | — | BUNDLE-CORRECT |
| C2 | 404/409/422 residual: "small closed set … no open extension point" | **CRITICAL** | CONFIRMED-DEFECT |
| C3 | "the FOUR static cursor forms" — there are **five** | MAJOR | CONFIRMED-DEFECT |
| C4 | "48 free-form columns" is postgres/mysql; SQLite is **67** | MAJOR | CONFIRMED-DEFECT |
| C5 | `trigger`/`trigger_`; is there a second divergence? | — | BUNDLE-CORRECT; **exactly one**, question closed |
| C6 | "4 validation strategies … keyed on KIND"; `callback` has no kind; kind is `"json-schema"` | **CRITICAL** | CONFIRMED-DEFECT |
| C7 | 56 line anchors swept | MINOR | 54 correct, 2 imprecise (`dto.go:174`, `doc.go:66`) |
| C8 | plan §4's literal-pipe grep, prescribed as a phase-4 verification | MAJOR | CONFIRMED-DEFECT (claim holds, command cannot) |
| C9 | `httpcore.Validate` "every POST/PUT on all 26 routes" → **3 routes**; ADR-0146/0152/0183 messages are NOT protected | **CRITICAL** | CONFIRMED-DEFECT |
| C10 | "12 `*Input` types" → **11** | MINOR | CONFIRMED-DEFECT |
| C11 | exactly one reachable custom `UnmarshalJSON` | — | BUNDLE-CORRECT (reflect walk, 20 types, 0 `TextUnmarshaler`) |
| C12 | "three decisions in **five** packages" → **nine** | MAJOR | CONFIRMED-DEFECT |
| C13 | fifteen→fourteen shapes; 403 wrap sites; §6.6/§6.5 order | MINOR | mixed |

**Totals: 13 entries — 3 Critical, 4 Major, 3 Minor, 3 verifications that came back clean.**

**Also verified BUNDLE-CORRECT (no finding):** the 26-route split (9 non-admin + 15 admin + 2 health,
machine-derived from the stdlib route table; 22 unique path literals identical across all three
adapters) · "no definition-deploy route" · 8 sentinels in the 400 arm across 5 `errors.Is` groups
from 4 packages · `ClassifyError`'s 6 ordered arms and every one of its line anchors · `wrkflw_journal`
= 6 columns · 9 tables / 79 columns / identical table set across three dialects · NOT NULL agrees on
all 79 columns · 3 `SECURITY:` caveat sites, all admin · 3 custom `UnmarshalJSON` in the repo ·
`grep -rniE "encrypt|redact"` exits 1 (well-formed, no literal-pipe bug) · no pre-existing 413
anywhere · the four lineage audit counts (58/12, 38/~13, 63/33, 56/28) · "no prior lens reported
`trigger_`" (my first grep for `trigger_` hit 12 files — all `trigger_payload`/`trigger_kind`; the
tightened net confirms the bundle) · `gate.go:45` flattening, reproduced independently.

**The lens's own note on nets.** Every defect above came from changing the *net*, never from redoing
arithmetic — consistent with all four previous rounds. Specifically: C1/C11 needed AST and `reflect`
instead of grep; C2 needed *producer* enumeration instead of *sentinel* enumeration; C4/C5 needed a
per-dialect parse instead of a single-dialect one; C6 needed the interface declarations instead of
the strategy list; C9 needed call-site + struct-tag enumeration instead of a parenthetical. And C5
is the counter-example that proves the discipline: a sloppy `trigger_` grep would have produced a
**false** finding against a correct bundle claim.
