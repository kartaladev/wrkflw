# ADR-0186 slice 1 (the three-decision re-cut) — audit adjudication

**Date:** 2026-08-21 · **Bundle audited:** `6cddb7b1` on `design/authz-security-b3` — the re-cut to
three decisions (D1 body caps · D2 what a 4xx body may say · D3 at-rest posture), five files:
spec + ADR-0186 + plan + `2026-08-21-adr-0186-premise-evidence.md` +
`2026-08-21-untrusted-input-deferred-slices.md`.
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four detached
worktrees at the bundle commit. **Step-0 presence check passed in all four.**
**Reports:** `audit3-0186-{execution,failure-modes,counting,interaction}.md` (2,295 lines,
rescued into the repo immediately on completion).

## ⛔ VERDICT: FAILS. 65 findings, 20 Critical.

| lens | findings | Critical | Major | Minor |
|---|---|---|---|---|
| execution | 16 (+7 confirmations) | 6 | 5 | 5 |
| failure-modes | 16 | 4 | 9 | 3 |
| counting | 13 (+3 clean) | 3 | 4 | 3 |
| interaction | 20 | 7 | 10 | 3 |
| **total** | **65** | **20** | **28** | **14** |

⚠ **This is slice 1's FIRST audit and the lineage's FIFTH.** The re-cut did what it was supposed to
do — it made the *interaction grid* small — and then failed on things the smaller grid does not
protect against. The distribution is the finding that matters; see "What the re-cut proved" below.

## ⭐⭐ Six findings were reached by two or more lenses independently

That is the strongest signal this process produces.

| # | finding | lenses |
|---|---|---|
| 1 | **The 404/409/422 "bounded residual" is not bounded, and its justification is sentinel-keyed** — the exact fallacy the same ADR retires two paragraphs earlier | **E22 + C2** |
| 2 | **`*json.UnmarshalTypeError` is in the vouched set and is NOT value-free** — it embeds the caller's numeric literal | **E9 + F2** |
| 3 | **The 413 log row demands an "observed size" that does not exist** — `http.MaxBytesError` carries only `Limit` | **F16 + I19** |
| 4 | **"48 free-form columns" is a POSTGRES number** — SQLite has 67 | **E20 + C4** |
| 5 | **The four `engine.*` sentinels have no opt-in and no phase**, so deny-by-default blanks the messages ADR-0146/0152/0183 added | **F4 + C9** |
| 6 | **`ClientSafeMessage`'s structural satisfaction has no compile-time check** — a rename silently blanks every vouched message with a green suite | **E6 + F7 + I10** |

## The decision-level Criticals, grouped by which decision they kill

### A. D2 (what a 4xx body may say) — 12 of the 20 Criticals. **The mechanism is not ready.**

- ⭐⭐ **C9 — an enumeration error CAUSING a design error, and it is the decisive finding.** ADR
  Decision 2 calls `httpcore.Validate` *"the DTO validator, every POST/PUT on all 26 routes"*.
  **Re-derived by the controller: 3 call sites** (`endpoints.go:26,83,101`) and **3** of 11 DTOs
  carry a `validate:` tag. That false count is the entire basis for believing the opt-in protects
  ADR-0146/0152/0183. Executed by the lens: `errors.Is(err, ErrBadInput)` is **false** for all four
  `engine.*` sentinels, so deny-by-default renders
  `"user task requires a completion outcome"` → `"invalid input"` — the outcome ADR Context §2
  flags **in bold as unacceptable**, marks ✅ resolved in spec §5, and guards with a test that
  asserts only `Validate`'s message and therefore **cannot fail when all four are blanked**.
  ⚠ `engine` appears in **no package list, no phase and no fan-out plan** in the bundle (F4).
- ⭐ **E22 + C2 — the residual justification is false.** Enumerated and executed: of the 7 sentinels
  in the 404/409/422 arms, **6 echo caller-controlled bytes**; only `ErrConcurrentUpdate` survives.
  `ErrDefinitionNotFound` has **4** wrap forms all formatting the caller's qualifier;
  `service.ErrConflict` has **6**, including `service.go:549` and `:605`
  `fmt.Errorf("%w: %w", ErrConflict, err)` — *literally the `lister.go:66` shape the ADR calls "the
  finding that killed the sentinel-keyed design"*. Executed: a `def_ref` of `"kyc:ssn-123-45-6789"`
  is **blanked at 400 and echoed at 404, on the same route**.
- **E9 + F2 — the vouched type set is wrong.** `{"add_attempts": 99999999999999999999}` →
  `json: cannot unmarshal number 99999999999999999999 into Go struct field …`. `encoding/json` sets
  `Value = "number " + <caller literal>` when a number does not fit. Live on two routes today.
  ⚠ **The type-keyed vouch re-commits the structural mistake the re-cut exists to abolish** — it
  asserts a property of a *type* that belongs to a *rendering*.
- **C6 — phase 2's mechanism does not describe the code.** `callback` has **no `Kind`** and is
  deliberately not a `DescribableStrategy` (an existing test asserts it), so a rendering "keyed on
  strategy kind" has nothing to key on; **3** strategies are registered kinds, not 4. And the kind
  literal is **`"json-schema"`**, not `"jsonschema"` as written throughout the bundle — an
  implementer's `case "jsonschema":` falls through to static text **silently**, and phase 2 test 1
  rows 1–3 still pass.
- **I5 (executed) — "403 stops leaking the deployment's own policy source" is FALSE.** The ABAC
  predicate is a marshalled field, `definition.nodes[].eligible_expr`
  (`definition/model/node_wire.go:29`), inside the definition embedded in every instance view by
  default (ADR-0144), shipped verbatim on a **non-admin** read route. ⚠⚠ **The read path is exactly
  what the re-cut REMOVED — so the removal falsified the survivor's celebration**, and
  `SECURITY.md` would have told consumers the predicate is not disclosed (I8).
- **I6 — two fixes in one decision, each correct alone.** The new "outermost `ClientSafeMessage`
  wins" rule makes the `callback` consumer opt-in — the celebrated F12 fix — **unreachable**,
  because the gate's own message shadows it (E2 independently).
- **E6 + F7 + I10 — the cross-package contract has no enforcement.** Both prescribed "does not
  import transport" tests **cannot fail** (the import would be a compile-time cycle — executed),
  and `ClientSafeMessage` will have three implementations agreeing only by method-name coincidence.
- **F6 — D2's own logging table contradicts itself on 403**: row 1 logs the raw error by default,
  row 3 gates it behind default-off `WithVerboseErrorLogging`.
- **E15** — `keywordLocation`-only renders a missing required field as `at '/required'`, discarding
  a property name that is author-derived and safe.

### B. D1 (body caps) — 3 Criticals, all mechanism, none needing a new decision

- ⭐ **F1 — `MaxBodyBytes = 0`, the documented "unbounded" opt-out, rejects every non-empty body.**
  **Re-derived by the controller:** `http.MaxBytesReader(w, body, 0)` → `read="" err=http: request
  body too large`; `-1` behaves identically. ⚠⚠ **And `0` is the mode the re-cut's own corrected
  migration story MANDATES** (*"set `MaxBodyBytes = 0`, observe, then choose a cap"*), so the fix
  for the histogram would brick all 39 sites. No phase prescribes a single test at `0`.
  F9 is its pre-condition: `ResolveConfig`'s post-loop defaulting idiom would clobber an explicit
  `0` before it reached the reader.
- ⭐ **I4 (executed) — "39 sites, one policy, one status" is false, and stdlib/gin can return 2xx
  for an oversize body.** The `BodyRaw()` correction put fiber's cap *before* parsing while
  stdlib/gin cap *during* it. At a 1 MiB cap: a well-formed 3 MiB body → 413 everywhere; a 3 MiB
  body with a syntax error at byte 3 → **400** on stdlib/gin, 413 on fiber; **a complete JSON value
  followed by 3 MiB of trailing bytes → `err == nil`, 2xx on stdlib/gin**, 413 on fiber. The parity
  case pins fixture *size*; the outcome turns on fixture *shape*.
- **E12 — plan phase 4 test 6's stated falsifier is INVERTED.** With the gzip bomb the bundle
  narrates four times, `len(c.Body())` sees 33, which is *under* the cap — so the wrong
  implementation returns 400 exactly like the right one. The discriminating fixture is the reverse
  (wire 2 KiB, decompressed 2 MiB).
- Majors: **F10** (oversize-but-malformed is 400 on stdlib/gin, 413 on fiber), **F16 + I19** (the
  413 log row's "observed size" does not exist), **I20** (the histogram sits at `json.Decoder`,
  which measures what it *consumed*, not the body), **F8** (the correlation id makes
  `TestParity_ErrorEnvelopes`'s byte-for-byte guarantee impossible and the plan never names it),
  **F12** (fiber's mount WARN sits in a function the documented admin path never calls, and
  compares against a constant rather than the app's limit), **F11** (widened 4xx logging has no off
  switch → attacker-driven log volume).

### C. D3 (at-rest posture) — 2 Criticals, both SCOPE, neither needing a new decision

- ⭐⭐ **F5 — the enumeration walks three migration files; there is a FOURTH.**
  `internal/authz/casbin/migrations/0001_casbin_rule.sql` creates a **tenth table** with seven
  free-form `TEXT` columns holding the deployment's **casbin policy**, applied by the module-root
  public `casbinauthz.MigrateCasbin`. ⚠⚠ **So the record blanks the 403 body BECAUSE policy source
  is sensitive, and then omits the table storing that same policy at rest** — the precise harm D3's
  own opening paragraph forbids. Fix: **discover** migrations, do not hardcode a directory list.
- **E20 + C4 — "48 free-form columns" is a POSTGRES number; SQLite has 67** (counting says 79
  columns schema-wide). ⚠ **The single-dialect blind spot the re-cut claims to have fixed,
  reappearing in the very number that fixes it.**
- **E21 — the open question is CLOSED by measurement, in the bundle's favour**: sweeping all 9
  tables × 3 dialects, **no second column-NAME divergence exists**, nullability agrees on all 79
  columns, and `trigger_` is the only one. ✅ But a systematic **type** divergence does exist
  (`JSONB`/`JSON`/`TEXT`), and the prescribed invariant is names-only.
- **F13** — `SECURITY.md` "cannot disagree" with the classification, but **no generator, command or
  drift check exists** anywhere in the plan.

### D. Meta

- ⭐⭐⭐ **I18 — spec §5's *"this table is complete at three, and that is the re-cut's main safety
  property"* is FALSE, and the controller's own brief to the interaction lens repeated it as fact.**
  The change set is not `{D1, D2, D3}` — it is those three **plus three removals**, so the grid is
  3 survivor×survivor **plus 3×3 = 9 survivor×removed**. The bundle derives **one** of the nine.
  I5 is one of the missing eight and is a live Critical. **Followed literally, that instruction
  suppresses all of them.**

## ⭐⭐⭐ What the re-cut PROVED, and what it did not

**It worked on the axis it was chosen for.** The owner split the bundle because *bundle size is the
multiplier* on **interaction** failures. Round 4 had five Criticals that were holes the revision's
own fixes opened in each other. **This round has essentially one of that shape (I6)** — the
survivor×survivor grid really did get small and really did hold.

**It did not protect against three other things, and this is the round's lesson:**

1. ⭐⭐ **A REMOVAL is a change, and it generates its own interaction grid.** Cutting three decisions
   out created **nine** survivor×removed pairs. The bundle derived one, congratulated itself on
   completeness, and shipped the false quantifier into its own audit brief. **I5 — the 403
   celebration falsified by the removed read path — is the exact round-4 shape, produced by the fix
   for round 4.**
2. ⭐⭐ **Scope-boundary failures are not interaction failures and splitting does nothing about
   them.** The failure-modes lens's own observation: three of its four Criticals sit **one step
   outside a boundary the bundle drew and never re-derived** — a directory glob (F5, the fourth
   migration dir), a package set (F4, `engine`), a config sentinel (F1, `MaxBodyBytes = 0`).
   Reasoning *inside* each boundary was sound. ⇒ **"The failure was the grep's NET" generalises
   from enumerations to SCOPES.**
3. ⭐⭐ **A new mechanism carries new risk that has nothing to do with bundle size.** D2's producer
   opt-in is the re-cut's own invention, one round old, and it collected 12 of the 20 Criticals.
   The previous design was refuted; **this one was never validated** — it was reasoned into
   existence in the same session that shipped it.

⚠ **And the counting lens's method note, fifth round running:** *"every defect came from changing
the **net**, never from redoing arithmetic."* Its C5 is the counter-example that proves the
discipline — a sloppy `trigger_` grep hit 12 files (all `trigger_payload`/`trigger_kind`) and would
have produced a **false finding against a correct claim**.

⚠ **The execution lens's method note is its twin:** *"the bundle's probes are not wrong — they are
**narrow**. Widening the fixtures (nine schema shapes not three, three gzip shapes not one, numeric
DTO fields not string ones, all three dialects not postgres) produced all six Criticals."*

## What HELD — do not re-litigate

- **39 decode sites / 36 propagating / 3 discarding** — re-derived by AST walk, not grep.
- **26 routes = 9 + 15 + 2**; **8 sentinels × 5 `errors.Is` groups**; all **6** `ClassifyError`
  arms; **54 of 56** line anchors exact.
- **Exactly ONE reachable custom `UnmarshalJSON`** — via a `reflect` walk over 20 types including
  the `TextUnmarshaler` fallback a grep is blind to. The controller's §6.3 finding stands.
- **9 tables, identical table SETS across three dialects, exactly one name divergence
  (`trigger_`), nullability agrees on all 79 columns.** The open question is closed.
- **Evidence §6.6's undischarged assumption is now DISCHARGED** (E7): `errors.As` reaches both
  `encoding/json` types through gin's and fiber's binders.
- **All four 413 mechanics** (E11); **`keywordLocation` value-freedom across nine adversarial
  schema shapes**, three times the bundle's coverage (E14); the `LocalizedString` panic and the
  empty root `KeywordPath` (E17); the 403 double-echo, bare-deny cleanliness, `Validate`'s
  value-freedom, the "0 caps" grep and the 3 discard sites (E23).

## Required next steps

1. ⛔ **Do not implement.** Nothing here has been folded.
2. **The scope question is back, and it is the owner's.** D2 carries **12 of 20 Criticals**, needs a
   package (`engine`) no phase lists, needs 404/409/422 moved to deny-by-default, and needs a
   compile-time-checkable contract. D1 and D3's defects are **mechanism and scope corrections, not
   design increments** — every one has a stated fix.
3. **Whatever is kept must add the survivor×removed interaction grid** (I18) — 9 pairs, 1 derived.
4. **Re-derive the three boundaries that failed** by discovery rather than by list: migration
   directories (`**/migrations/*.sql`), the packages producing 4xx sentinels, and every config
   sentinel value (`0`, `-1`, absent).
5. ⚠ **`SECURITY.md`'s generator does not exist.** Either build it or stop claiming the document
   and the classification cannot disagree.
