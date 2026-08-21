# ADR-0186 re-audit — COUNTING lens

**Bundle commit**: `677760d5` (worktree `.../scratchpad/a0186-count`)
**Date**: 2026-08-21
**Lens**: enumerations, quantifiers, explicit counts, inherited citations, line-anchor drift.
**Scope**: ADR-0186 only (untrusted input & disclosure). ADR-0185 identity work is out of scope.

Bundle files verified present at step 0:
- `docs/specs/2026-08-21-untrusted-input-and-disclosure.md` (29060 b)
- `docs/adr/0186-untrusted-input-and-disclosure-posture.md` (62049 b)
- `docs/plans/2026-08-21-untrusted-input-and-disclosure.md` (48353 b)
- `docs/specs/2026-08-21-adr-0186-premise-evidence.md` (15974 b)

Findings are appended one at a time as confirmed.

---
## C1 — CRITICAL — "12 plaintext columns across 7 tables" is wrong on BOTH numbers, against the bundle's OWN list: it is 15 columns across 8 tables

### The claim, verbatim, in every place it appears

- **ADR banner**, `docs/adr/0186-…md:25` — *"plaintext columns (**12**, not 2 — and not the audit's "six" either)"*
- **ADR Context §6**, `:223-228` — *"⚠ **The plaintext set is twelve columns across seven tables, in three dialects — not two, and not the audit's "at least six" either.** Beyond `wrkflw_instances.snapshot` and `wrkflw_journal.trigger`: `wrkflw_outbox.{payload,last_error}`, `wrkflw_definitions.definition`, `wrkflw_human_task.{vars,candidates,eligibility,claim_actor, completion_actor,note}`, `wrkflw_call_links.{output,error}`, `wrkflw_timers.trigger_payload`, `wrkflw_chain_links.start_vars`."*
- **ADR D6**, `:733-739` — *"⚠⚠ **Twelve columns across seven tables, in all three dialects** — …"* (same list repeated verbatim)
- **Spec §1 problem table**, `docs/specs/2026-08-21-untrusted-input-and-disclosure.md:70` — *"| at rest | **12 plaintext columns across 7 tables**, no integrity chain |"*
- **Spec §2 corrections row**, `:109` — *"⚠ **Twelve columns across seven tables, in three dialects.** An audit lens raised it to "at least six" and was itself short by three tables."*
- **Evidence §4.4 heading**, `docs/specs/2026-08-21-adr-0186-premise-evidence.md:191` — *"### 4.4 Plaintext columns at rest — **twelve columns across seven tables**, in three dialects"*
- **Plan ▶ Progress**, `docs/plans/2026-08-21-…md:33` — *"D6's enumeration re-derived (**12** columns, 7 tables, 3 dialects)"*
- **Plan §4 table**, `:659` — *"| plaintext at-rest columns | **12 across 7 tables**, in **3** dialects."*
- **Plan phase 7**, `:601` — *"Evidence §4.4 has **12 columns across 7 tables**"*

Nine restatements. Every one of them is wrong.

### Re-derivation — and why this is a better net than the author's

The author's net was *"count the rows of my own markdown table."* Evidence §4.4's table has **12 rows** — but three rows are **brace-collapsed multi-column entries**. Counting rows is not counting columns. My net expands the braces and counts distinct `table.column` pairs, then counts distinct table names, then checks the migration DDL for columns the list omits.

Expanding the bundle's own list:

| # | column |
|---|---|
| 1 | `wrkflw_instances.snapshot` |
| 2 | `wrkflw_journal.trigger` |
| 3 | `wrkflw_outbox.payload` |
| 4 | `wrkflw_outbox.last_error` |
| 5 | `wrkflw_definitions.definition` |
| 6 | `wrkflw_human_task.vars` |
| 7 | `wrkflw_human_task.candidates` |
| 8 | `wrkflw_human_task.eligibility` |
| 9 | `wrkflw_human_task.claim_actor` |
| 10 | `wrkflw_human_task.completion_actor` |
| 11 | `wrkflw_human_task.note` |
| 12 | `wrkflw_call_links.output` |
| 13 | `wrkflw_call_links.error` |
| 14 | `wrkflw_timers.trigger_payload` |
| 15 | `wrkflw_chain_links.start_vars` |

**15 columns.** Distinct tables: `wrkflw_instances`, `wrkflw_journal`, `wrkflw_outbox`, `wrkflw_definitions`, `wrkflw_human_task`, `wrkflw_call_links`, `wrkflw_timers`, `wrkflw_chain_links` = **8 tables.**

The three collapsed rows are `wrkflw_outbox.{payload,last_error}` (2), `wrkflw_human_task.{claim_actor, completion_actor, note}` (3) and `wrkflw_call_links.{output, error}` (2) — 12 rows + 3 hidden columns = 15.

Table total independently checked against the DDL:

```
$ grep -rn "CREATE TABLE" internal/persistence/store/migrations/ | sed 's/.*migrations\///' | sort
mysql/0001_init.sql:109:CREATE TABLE wrkflw_chain_links (
mysql/0001_init.sql:126:CREATE TABLE wrkflw_human_task (
mysql/0001_init.sql:13:CREATE TABLE wrkflw_instances (
mysql/0001_init.sql:27:CREATE TABLE wrkflw_journal (
mysql/0001_init.sql:38:CREATE TABLE wrkflw_outbox (
mysql/0001_init.sql:55:CREATE TABLE wrkflw_definitions (
mysql/0001_init.sql:63:CREATE TABLE wrkflw_processed_message (
mysql/0001_init.sql:73:CREATE TABLE wrkflw_call_links (
mysql/0001_init.sql:92:CREATE TABLE wrkflw_timers (
… identical set in postgres/ and sqlite/
```

**Nine tables exist**; the bundle's list touches **eight** of them.

### Verdict

**CONFIRMED WRONG.** "Twelve" is a count of the author's own markdown rows, silently substituted for a count of columns. "Seven" is simply a miscount of eight distinct table names in a list the author wrote himself. The one enumeration in the bundle whose *deliverable is the enumeration* is off by 25 % on columns and by one whole table.

⚠ Note the shape: this is the **first** arithmetic error in three audit rounds of this lineage. The standing lesson has been "the arithmetic is always right, the net is always wrong." Here the net is (mostly) right and the arithmetic is wrong — because the author counted a *proxy* (rows) for the thing (columns) and never expanded the braces.

### Damage if acted on

Phase 7 is instructed to *"Derive the column list from `internal/persistence/store/migrations/{postgres,mysql,sqlite}` at implementation time — do not copy it from the ADR"*, which is the right instruction. But the plan **also** tells the implementer the expected answer (*"Evidence §4.4 has 12 columns across 7 tables"*), so an implementer who derives **15 across 8** will read that as a discrepancy to reconcile *toward the ADR* — the classic anchoring failure — and the prescribed invariant test (*"any new column in those tables is either listed or explicitly justified"*) will be seeded with the wrong baseline. D6's own words: *"an incomplete list presented as exhaustive is strictly worse than the silence D6 rejects — it converts a consumer's own audit into a false negative."*

### Proposed replacement wording

Everywhere: **"fifteen columns across eight tables, in all three dialects."** And in evidence §4.4, expand the three brace-collapsed rows into one row per column so the table's row count *is* the column count and cannot drift again. Add to plan §4: *"⚠ Count columns, not table rows — the braces hide three."*

---
## C2 — CRITICAL — the plaintext enumeration names the actor REMAINDER and omits the actor IDENTIFIER: `wrkflw_human_task.{claimed_by, completed_by, outcome}` are missing

### The claim, verbatim

- **ADR D6**, `docs/adr/0186-…md:733-738` — *"⚠⚠ **Twelve columns across seven tables, in all three dialects** — … `wrkflw_human_task.{vars,candidates,eligibility,claim_actor, completion_actor,note}` …"*
- **Evidence §4.4**, `docs/specs/2026-08-21-adr-0186-premise-evidence.md:209` — *"| `wrkflw_human_task.{claim_actor, completion_actor, note}` | actor records + a free-text completion note |"*
- **ADR D6 rationale**, `:744-746` — *"This is the one decision in the bundle whose **deliverable is the enumeration**, and an incomplete list presented as exhaustive is strictly worse than the silence D6 rejects — it converts a consumer's own audit into a false negative."*

### Re-derivation — and why this is a better net than the author's

The author's net was *"read the migrations and pick the JSON/TEXT blob columns."* That net is structurally blind to a **scalar projection deliberately split out of a blob for indexing** — which is exactly the shape the repo uses here. My net was: read the migration DDL **column by column**, then read the store code that decides *what goes in which column*.

DDL, `internal/persistence/store/migrations/postgres/0001_init.sql:138-157` (identical column set in `sqlite/` `:134` and `mysql/` `:126`):

```
CREATE TABLE wrkflw_human_task (
    task_id  TEXT NOT NULL, instance_id TEXT NOT NULL, node_id TEXT NOT NULL, state TEXT NOT NULL,
    claimed_by  TEXT NOT NULL DEFAULT '',      <-- NOT in the enumeration
    claimed_at  TIMESTAMPTZ, claim_actor JSONB,
    completed_by TEXT,                          <-- NOT in the enumeration
    completed_at TIMESTAMPTZ,
    outcome      TEXT,                          <-- NOT in the enumeration
    note         TEXT, completion_actor JSONB,
    eligibility JSONB NOT NULL, candidates JSONB NOT NULL, vars JSONB NOT NULL, …);
```

Then the store code that says what is in them — `internal/persistence/store/humantask_store.go:552-556`:

```go
// htActorRemainder is the JSON shape of the claim_actor / completion_actor
// columns: everything an [authz.Actor] carries except its ID, which lives in its
// own scalar column (claimed_by / completed_by) so it stays indexable and
// queryable.
```

and `:478-489`:

```go
// htClaimantID returns the ID of the task's current claimant … It feeds the
// indexed claimed_by column that backs the AssignedTo lookup.
func htClaimantID(t humantask.HumanTask) string { … return t.Claim.Actor.ID }
```

### Verdict

**CONFIRMED, and it is inverted rather than merely short.** `claim_actor` / `completion_actor` hold *everything except the ID*. The **actor's identity — the single most PII-bearing field in the whole table — lives in `claimed_by` / `completed_by`**, which the enumeration does not name. The bundle enumerated the remainder and omitted the identifier. `outcome` (the human decision the task recorded) is likewise plaintext and unlisted.

Corrected total for this finding + C1: **18 columns across 8 tables** (15 from C1 + `claimed_by`, `completed_by`, `outcome`).

⚠ This is the same net-blindness class as `grep mapInstance` missing `NewInstanceView` and `grep NewUserTask(` missing the builder — a second storage form for the same datum, split out for a mechanical reason (here: indexability), invisible to a grep tuned to the first form.

### Damage if acted on

A consumer reads `SECURITY.md`, encrypts `claim_actor` and `completion_actor`, and believes the human-task audit trail is protected. They have encrypted **roles and attributes** and left **who claimed and who completed every task**, in the clear, in an *indexed* column — the one an operator or a read-replica consumer will actually query. `idx_wrkflw_human_task_claimed_by` exists in all three dialects, so the identifier is additionally replicated into an index. This is precisely the false-negative D6's own text says is worse than silence, and it is worse than a plain omission because the enumeration's mention of `claim_actor` **actively signals that the claim data is covered**.

### Proposed replacement wording

In evidence §4.4, ADR Context §6 and ADR D6, add three rows and correct the total:

| table.column | what it holds |
|---|---|
| `wrkflw_human_task.claimed_by` | **the claimant actor's ID** — split out of `claim_actor` so it stays indexable (`htClaimantID`, `humantask_store.go:484`); `claim_actor` holds only roles + attributes |
| `wrkflw_human_task.completed_by` | the completing actor's ID, same split |
| `wrkflw_human_task.outcome` | the human decision recorded on the task |

and add a standing note: *"⚠ `claim_actor`/`completion_actor` are the actor **remainder** (`htActorRemainder`: roles + attributes, explicitly **not** the ID). Encrypting them alone leaves the actor identity in the clear in `claimed_by`/`completed_by`."*

---
## C3 — CRITICAL — the machine-checked invariant prescribed to stop the read-path enumeration rotting a THIRD time uses the SAME NET THAT LET IT ROT: it is structurally blind to both mapper-less endpoints

### The claim, verbatim

- **Plan phase 3, test 8**, `docs/plans/2026-08-21-…md:416-418` — *"⚠ **Plus the count invariant:** assert that the number of `NewInstanceView`/`mapInstance` call sites routed through the helper equals the number that exist. This enumeration has rotted **twice**; a number in prose will rot again."*
- **ADR D4**, `docs/adr/0186-…md:543-544` — *"**Phase 4 asserts the count as a machine-checked invariant**, because this enumeration has now rotted twice."*
- **Plan §4**, `:668-670` — *"**Assume one more is wrong**, and prefer the two machine-checked invariants prescribed above (phase 3 test 8's call-site count, phase 3 test 3's sentinel-set pin) over any number in this table."*

### Re-derivation — why this net is worse than the prose it replaces

The eleven paths are **6 `mapInstance` + 3 direct-`NewInstanceView` admin + 2 mapper-less**. I enumerated exactly what an invariant keyed on `NewInstanceView`/`mapInstance` call sites can see:

```
$ grep -rn "mapInstance(" --include='*.go' transport/http/httpcore/ | grep -v _test
endpoints.go:15   (definition)
endpoints.go:42, 52, 94, 124, 140, 155                    -> 6 call sites

$ grep -rn "NewInstanceView(" --include='*.go' . | grep -v _test.go
httpcore/seam.go:42, :54          (default InstanceMapper, x2)
httpcore/endpoints.go:17          (mapInstance's nil-mapper default)
httpcore/view.go:23               (definition)
httpcore/admin_endpoints.go:111, :121, :514               -> 3 admin call sites
```

Now the two paths the invariant must cover, read from source:

```
$ sed -n '60,78p' transport/http/httpcore/endpoints.go
func GetInstanceSnapshot(ctx context.Context, svc service.Service, id string) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	…
	return http.StatusOK, pi, nil                                  <-- returns the raw service.ProcessInstance
}
func GetActionableView(ctx context.Context, svc service.Service, id string) (int, any, error) {
	…
	return http.StatusOK, view.NewActionableView(pi.State(), pi.Definition()), nil   <-- a DIFFERENT constructor
}
```

**Neither calls `mapInstance` and neither calls `NewInstanceView`.** `GetInstanceSnapshot` calls **nothing** — it returns `pi` and lets `service.instanceJSON`'s `MarshalJSON` project it. `GetActionableView` calls `view.NewActionableView`, a symbol in a **different package** (`runtime/view`).

⇒ The prescribed invariant can count at most **9** of the 11 paths, and the two it cannot see are precisely the two that the *previous* rot round added (6 → 6+2). Worse, `GetInstanceSnapshot` is the single highest-disclosure path in the bundle — the ADR's own Context §4 says it returns *"five disclosure-bearing fields"* including the whole embedded definition.

### Verdict

**CONFIRMED.** The invariant proposed as the durable fix for an enumeration that rotted twice is keyed on the same two symbols whose blindness caused the second rot (spec §2: *"⚠ The failure was the **net**: `grep mapInstance` is blind to `NewInstanceView`"*). Adding `NewInstanceView` to the net fixes the *last* failure and not the *class*. A third form (`view.NewActionableView`) and a *zero*-form (`return pi`) already exist in the tree today.

### Damage if acted on

Phase 3 test 8 ships an invariant that passes at 9-of-9 while two paths sit outside it. Plan §4 explicitly instructs the reader to **prefer this invariant over the prose count** — so the number a future session trusts is the one that cannot see `GetInstanceSnapshot`. Any later endpoint that returns `pi` directly, or builds a view via a third constructor, joins the blind spot silently. This is the enumeration rotting a **third** time, with a green test asserting it has not.

### Proposed replacement wording

Key the invariant on the **response**, not on the constructor. Concretely, in `transport/http/httpcore`:

> `TestEveryInstanceReturningEndpointIsRoutedThroughTheRedactionHelper` — enumerate the exported endpoint functions in `httpcore` whose success return is derived from a `service.ProcessInstance` (`pi`, `pi.State()`, or any view built from either) via `go/ast` over the package, and assert each appears in the helper's covered set. **Falsifier, stated:** *it fails against an invariant keyed on `mapInstance`/`NewInstanceView` call sites, because `GetInstanceSnapshot` returns `pi` directly and `GetActionableView` calls `view.NewActionableView`.*

If an AST test is judged too heavy, the fallback is a **table of the eleven exported endpoint names** with a companion assertion that the number of exported `httpcore` functions whose signature returns `(int, any, error)` and whose body mentions `svc.GetInstance`/`pi.State()` equals eleven — still response-keyed, not constructor-keyed.

---

## C4 — MAJOR — the ADR assigns the read-path count invariant to **phase 4**, the plan to **phase 3**; phase 4 is a different package and cannot see the call sites

### The claim, verbatim

- **ADR D4**, `docs/adr/0186-…md:543-544` — *"**Phase 4 asserts the count as a machine-checked invariant**, because this enumeration has now rotted twice."*
- **Plan §2 decision→phase map**, `docs/plans/2026-08-21-…md:153` — *"| D4 redaction helper on **all 11** paths + count invariant | 3 | `transport/http/httpcore` |"*
- **Plan phase 3 test 8**, `:412-418` — the invariant is prescribed in phase 3.
- **Plan phase table**, `:181` — *"| 4 | `transport/http/stdlib` \| `gin` \| `fiber` | D1, D5(id + logging) | 3 | **3 agents in parallel** |"*

### Re-derivation

Phase 4's packages are `transport/http/{stdlib,gin,fiber}`. The call sites the invariant counts are all in `transport/http/httpcore` (verified in C3). Phase 4 runs as **three parallel agents**, one per adapter package, none of which owns `httpcore`.

### Verdict

**CONFIRMED contradiction**, and it is the exact failure mode this revision was built to eliminate. The plan's own §2 header reads: *"**Every sentence of ADR-0186's Decision section has a row. A row with no phase is a defect.** Six of audit #1's fifteen Criticals were this one omission"* — and the ADR sentence naming phase 4 has a row naming phase 3.

### Damage if acted on

An implementer following the **ADR** (the authoritative design record) puts the invariant in a phase-4 adapter package, where it must reach across into `httpcore`'s internals; three parallel agents would each try to own it, or all three would skip it as "someone else's". An implementer following the **plan** puts it in phase 3 and the ADR is left describing something that did not ship — the ADR-0162 zombie-scope shape rule #11 names.

### Proposed replacement wording

ADR D4 `:543`: **"Phase 3 asserts the count as a machine-checked invariant"** — and, per C3, restate the invariant as response-keyed rather than constructor-keyed.

---
## C5 — CRITICAL — the 400 exception list admits `ErrBadCursor` / `ErrBadArmedTimerCursor` as "provably value-free BY CONSTRUCTION". EXECUTED: both echo caller-supplied strings verbatim. The evidence covers 2 of 7 wrap sites, and BOTH quoted messages come from the SAME sentinel

### The claim, verbatim

- **ADR D5**, `docs/adr/0186-…md:619-621` — *"**400 is deny-by-default over an OPEN set, with an enumerated exception list** … The 400 arm renders `err.Error()` only for sources whose message is **provably value-free by construction**, enumerated below and pinned by a test."*
- **ADR D5 table**, `:630` — *"| `kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor` | `err.Error()` | messages are `": not an instance cursor"` / `": cursor carries no start time"` — **no caller value** |"*
- **Plan phase 3**, `docs/plans/2026-08-21-…md:338-339` — *"400 → the **exception list** in ADR-0186 D5: `err.Error()` for `ErrBadCursor`, `ErrBadArmedTimerCursor`, …"*
- The author asked for exactly this attack — **plan §0 item 3**, `:54-57`: *"Five sentinels keep `err.Error()` on the strength of an executed claim about *today's* message text. **Attack the claim (does any of the five have a caller-value-bearing wrap site the author missed?)**"*

### Re-derivation — why this net is better than the author's

The author's net was *"read the message text of the arm's sentinels."* That net sees the **`fmt.Errorf` format strings that contain no verb**, and is blind to a wrap site whose format is `"%w: %w"` — where the *second* error is arbitrary and caller-derived. My net enumerated **every** wrap site per sentinel, then executed the ones with a `%w`/`%v` payload.

```
$ for s in ErrBadCursor ErrBadArmedTimerCursor …; do grep -rn "%w" --include='*.go' . | grep -v _test | grep "$s"; done
--- ErrBadCursor ---            (FOUR wrap sites, not two)
runtime/kernel/lister.go:66:  fmt.Errorf("%w: %w", ErrBadCursor, err)                       <-- arbitrary payload
runtime/kernel/lister.go:69:  fmt.Errorf("%w: not an instance cursor", ErrBadCursor)
runtime/kernel/lister.go:77:  fmt.Errorf("%w: cursor carries no instance identity", ErrBadCursor)
runtime/kernel/lister.go:90:  fmt.Errorf("%w: cursor carries no start time", ErrBadCursor)
--- ErrBadArmedTimerCursor ---  (THREE wrap sites)
runtime/kernel/armed_timer_paging.go:89:  fmt.Errorf("%w: %w", ErrBadArmedTimerCursor, err)  <-- arbitrary payload
runtime/kernel/armed_timer_paging.go:92:  fmt.Errorf("%w: not an armed-timer cursor", …)
runtime/kernel/armed_timer_paging.go:99:  fmt.Errorf("%w: cursor carries no timer identity", …)
```

⚠ **Anchor note:** the ADR quotes `": not an instance cursor"` **and** `": cursor carries no start time"` as if one belonged to each sentinel. Both are `ErrBadCursor` (`lister.go:69` and `lister.go:90`). **`ErrBadArmedTimerCursor` has no quoted message at all** — its evidence is inherited from its sibling by analogy, which Premise Discipline names explicitly as a forbidden move (*"this case is analogous to that one, so it behaves the same"*).

The `%w: %w` payload is `decodeCursorInto`'s error (`runtime/kernel/cursorcodec.go:38-64`): base64 error, or a `json.Decoder` error with `DisallowUnknownFields()` set — which renders unknown **field names** verbatim.

### Executed probe (throwaway `runtime/kernel/zzprobe_count_test.go`, deleted after)

```
=== RUN   TestProbeBadCursorEchoesCallerValue
### unknown-field-name   err="workflow-runtime: malformed instance cursor: json: unknown field \"ssn-4111111111111111\""
### syntax-error         err="workflow-runtime: malformed instance cursor: invalid character '@' looking for beginning of object key string"
### wrong-type           err="workflow-runtime: malformed instance cursor: parsing time \"NOT-A-TIME-4111-1111\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"NOT-A-TIME-4111-1111\" as \"2006\""
### trailing-data        err="workflow-runtime: malformed instance cursor: trailing data after cursor payload: invalid character '<' looking for beginning of value"
### armed-timer-unknown  err="workflow-runtime: malformed armed-timer cursor: json: unknown field \"CARD-4111-1111-1111-1111\""
--- PASS
```

The caller controls the whole cursor query parameter; base64 is not a trust boundary. An arbitrary caller-chosen string is reflected verbatim — **twice** in the `wrong-type` row.

### Verdict

**CONFIRMED WRONG, by execution.** Two of the exception list's six rows are admitted on a claim of *value-freedom by construction* that is false by construction: the construction is `"%w: %w"` over an error the caller shapes. This is structurally the same defect ADR-0165's audit shipped — an **inverted predicate** that admits the case it exists to refuse — and the same shape as the D5 rendering the revision already had to withdraw once (*"the prescribed replacement rendering was itself not value-free"*).

### Damage if acted on

D5 ships as a deny-by-default allow-list whose allow-list leaks. Two concrete consequences:

1. **Reflected content in the 400 body.** `ErrorBody.Message` carries an arbitrary caller-controlled string. A consumer rendering the error envelope in a web UI has a reflected-XSS vector delivered by the library's own security decision (the `trailing-data` row above already reflects `<`).
2. **It reaches `slog` too.** D5 widens 4xx logging and routes 400 to the *rendered* message at `WarnContext` (ADR `:710`), on the stated rationale that the rendered message is safe. It is not, for these two sentinels — so the fix relocates a caller-controlled string onto `slog.Default()`, which is precisely the relocation-not-removal Critical D5 §D5×D6 was folded to prevent.
3. Phase 3 test 3 is instructed to write **positive** assertions protecting these messages, so the leak gets pinned by a test as intended behaviour.

### Proposed replacement wording

ADR D5 table, replace the row:

| 400 source | rendering | why |
|---|---|---|
| `kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor` | **static `"malformed cursor"` + correlation id** | ⚠ **Executed:** each has a `fmt.Errorf("%w: %w", …)` wrap site (`lister.go:66`, `armed_timer_paging.go:89`) carrying `decodeCursorInto`'s error, and `json.Decoder` with `DisallowUnknownFields` renders caller-chosen field names verbatim (`json: unknown field "ssn-4111111111111111"`). Value-freedom does **not** hold by construction. The three literal-text wrap sites per sentinel are value-free; the `%w: %w` one is not, and the arm cannot tell them apart |

Alternative, if the diagnostic is judged worth keeping: change `lister.go:66` and `armed_timer_paging.go:89` to `fmt.Errorf("%w: cursor payload is not decodable", Err…)` (dropping the inner error), then the by-construction claim becomes true — but that is a change in `runtime/kernel`, a package **no phase in this plan owns**, so it needs its own row in §2's decision→phase map.

Either way: add to plan §0's re-audit list and to phase 3 test 3 a row asserting a cursor of the form `base64({"kind":"instance","<caller-string>":1})` does **not** reflect `<caller-string>`. **Falsifier:** *it fails today, and against any implementation that keeps `err.Error()` for these two sentinels.*

---
## C6 — CRITICAL — how many of the eight 400-arm sentinels keep `err.Error()` is stated as **four**, **five** and **six** in four places, and the plan's "six" has the WRONG MEMBERSHIP: it omits `ErrBadInput` and counts `ErrInvalidInput`, which does not keep `err.Error()` at all

### The claim, verbatim, in every place it appears

- **ADR D5 table**, `docs/adr/0186-…md:630-636` — six rows render `err.Error()`: `ErrBadCursor`, `ErrBadArmedTimerCursor` (row 1), `ErrEmptyTriggerKey`, `ErrEmptyReassignTarget` (row 2), `ErrBadInput` (row 3), `ErrOutcomeRequired` (row 5). `ErrInvalidOutcome` is **reshaped**; `validation.ErrInvalidInput` gets *"what `runtime/validation` rendered"*. ⇒ **SIX**.
- **ADR Consequences**, `:794-796` — *"**The actionable 400 messages ADR-0146, ADR-0152 and ADR-0183 added survive.** **Five of the eight sentinels** — including `ErrBadInput`, every DTO on all 26 routes — keep their message, because it was executed and shown value-free rather than assumed leaky."* ⇒ **FIVE**.
- **Plan §4**, `docs/plans/2026-08-21-…md:653` — *"| …keeping `err.Error()` after D5 | **6** (4 provably value-free + `ErrOutcomeRequired` + `ErrInvalidInput`'s rendered message); **1** reshaped (`ErrInvalidOutcome`); the open remainder static |"* ⇒ **SIX, with a different membership.**
- **Plan phase 3 test 3**, `:385-387` — *"⚠ And the **four** sentinels that **keep** `err.Error()` need positive assertions — audit finding F4 is that the previous design destroyed messages ADR-0146/0152/0183 deliberately added, and only a positive assertion protects them."* ⇒ **FOUR**.

### Re-derivation

Source of truth, `transport/http/httpcore/errors.go:36-50` — the 400 arm, verbatim, with line numbers:

```
36	case errors.Is(err, kernel.ErrBadCursor), errors.Is(err, kernel.ErrBadArmedTimerCursor),
37		errors.Is(err, ErrBadInput), errors.Is(err, validation.ErrInvalidInput),
42		errors.Is(err, engine.ErrInvalidOutcome), errors.Is(err, engine.ErrOutcomeRequired),
46		errors.Is(err, engine.ErrEmptyTriggerKey),
49		errors.Is(err, engine.ErrEmptyReassignTarget):
50		return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
```

**Eight sentinels, five `errors.Is` lines** — the ADR's "eight across five `errors.Is` groups" is CONFIRMED (a "group" is a source line: 36, 37, 42, 46, 49).

Partitioning them by D5's own table:

| sentinel | D5 disposition | keeps `err.Error()`? |
|---|---|---|
| `kernel.ErrBadCursor` | `err.Error()` | yes |
| `kernel.ErrBadArmedTimerCursor` | `err.Error()` | yes |
| `engine.ErrEmptyTriggerKey` | `err.Error()` | yes |
| `engine.ErrEmptyReassignTarget` | `err.Error()` | yes |
| `httpcore.ErrBadInput` | `err.Error()` | yes |
| `engine.ErrOutcomeRequired` | `err.Error()` | yes |
| `engine.ErrInvalidOutcome` | **reshaped** to `node %q: outcome not declared` | **no** |
| `validation.ErrInvalidInput` | **replaced** by phase 2's `keywordLocation` rendering | **no** |

⇒ **six keep it, two do not.** 8 = 6 + 1 reshaped + 1 re-rendered.

Now decompose the plan's *"6 (4 provably value-free + `ErrOutcomeRequired` + `ErrInvalidInput`'s rendered message)"*. The "4 provably value-free" is ADR Context §5's *"Four of the seven non-validation sentinels echo **no** caller value at all"* = {`ErrBadCursor`, `ErrBadArmedTimerCursor`, `ErrEmptyTriggerKey`, `ErrEmptyReassignTarget`}. Adding `ErrOutcomeRequired` = 5. Adding `ErrInvalidInput` = 6.

⇒ The plan's six is **{ErrBadCursor, ErrBadArmedTimerCursor, ErrEmptyTriggerKey, ErrEmptyReassignTarget, ErrOutcomeRequired, ErrInvalidInput}**. It **omits `ErrBadInput`** and **includes `ErrInvalidInput`**, which by D5 does *not* keep `err.Error()` — its whole point is that phase 2 replaces the message. Two compensating errors landing on the right total with the wrong set.

### Verdict

**CONFIRMED — four different numbers for one closed set of eight, in a bundle whose D5 is explicitly a deny-by-default allow-list.** The correct answer is **six**, and the correct membership is the ADR D5 table's.

⚠ The `ErrBadInput` omission is the one that matters: it is the sentinel the entire D5 revision exists for. ADR Context §5 `:209-214` calls it *"the **highest-volume 400 by a wide margin** (36 decode wraps plus the whole `httpcore.Validate` DTO layer, i.e. every POST/PUT body on all 26 routes)"* and evidence §1 exists solely to prove it value-free. Plan §4 — the table the plan tells implementers to trust — leaves it out of the keep-set.

### Damage if acted on

Plan phase 3 test 3 is the **pin test**, and its instruction is *"the four sentinels that keep `err.Error()` need positive assertions … only a positive assertion protects them."* An implementer follows it literally and writes positive assertions for four — leaving `ErrBadInput` and `ErrOutcomeRequired` unprotected, i.e. exactly the two whose messages ADR-0146 and evidence §1 were written to save. A later refactor blanks them and the pin test stays green. That is audit finding F4 (*"the static-400 default destroys actionable messages three prior ADRs deliberately added"*) shipping anyway, through the test built to prevent it.

Separately, ADR Consequences' *"Five of the eight"* is a **recap sentence over-generalising what it compressed** — the exact class CLAUDE.md's Premise Discipline names as the one that survives review.

### Proposed replacement wording

- **ADR Consequences `:794-796`** → *"**Six of the eight sentinels — `ErrBadCursor`, `ErrBadArmedTimerCursor`, `ErrEmptyTriggerKey`, `ErrEmptyReassignTarget`, `ErrBadInput` and `ErrOutcomeRequired` — keep their message.** `ErrInvalidOutcome` is reshaped and `validation.ErrInvalidInput` is re-rendered by `runtime/validation`."* (⚠ and per C5, the first two must move out of the keep-set, making it **four**: `ErrEmptyTriggerKey`, `ErrEmptyReassignTarget`, `ErrBadInput`, `ErrOutcomeRequired`. Fix C5 first, then this number follows from it.)
- **Plan §4 `:653`** → name the members, never a bare count: *"…keeping `err.Error()` after D5 | **6, named**: `ErrBadCursor`, `ErrBadArmedTimerCursor`, `ErrEmptyTriggerKey`, `ErrEmptyReassignTarget`, `ErrBadInput`, `ErrOutcomeRequired`. **1 reshaped** (`ErrInvalidOutcome`), **1 re-rendered** (`validation.ErrInvalidInput`). 6+1+1 = 8. ⚠ `ErrInvalidInput` does NOT keep `err.Error()`."*
- **Plan phase 3 test 3 `:385`** → *"the **six** sentinels that keep `err.Error()` need positive assertions"*, listed by name so the count cannot drift from the set.

---
## C7 — MAJOR (Critical on the library axis) — "**Four, and only four.** No other `service` request type carries a `map[string]any`" is FALSE: four more do, through `authz.Actor.Attributes` — and that map reaches the ABAC evaluator env, unbounded

### The claim, verbatim

- **Evidence §4.6**, `docs/specs/2026-08-21-adr-0186-premise-evidence.md:230-242` — *"### 4.6 The four caller-supplied variable-map admission sites in `service` … The seam D2's admission bound acts on. **Closed set**, from the request types: `$ grep -n "map\[string\]any" service/request.go` … **Four, and only four. No other `service` request type carries a `map[string]any`.**"*
- **ADR D2**, `docs/adr/0186-…md:366-369` — *"That seam is the **closed set of four request fields** (Evidence §4.6)."*
- **Plan §4**, `docs/plans/2026-08-21-…md:660` — *"| caller-supplied variable-map admission sites in `service` | **4** |"*
- **Plan phase 1**, `:204` — *"Enforced at the four caller-supplied request fields and **nowhere else**"*
- **ADR Consequences (the headline Positive)**, `:776-780` — *"⭐ **The bound acts on the MAP, not on an evaluator, so every expression surface that reads process variables inherits it for the caller-supplied contribution** — **both ABAC evaluators**, the engine's gateway path, `action/httpcall`'s URL expression and `action/transform`."*
- The author asked for exactly this attack — **plan §0 item 1**, `:46-49`: *"**Is that set closed?** A fifth path into `State.Variables` that a caller controls would be a hole straight through Decision 2."*

### Re-derivation — why this net is better than the author's

The author's net was `grep -n "map[string]any" service/request.go`. That net is **structurally blind to a `map[string]any` reached through a named struct type** — the field's declared type is `authz.Actor`, and the string `map[string]any` never appears on the line. My net was: enumerate **every request type** in the package, then follow each field's type.

```
$ grep -rn "^type .*Request struct" --include='*.go' service/ | grep -v _test
service/request.go:14:type StartInstanceRequest struct {            <- Vars map[string]any     (counted)
service/request.go:24:type DeliverSignalRequest struct {            <- Payload map[string]any  (counted)
service/request.go:38:type DeliverMessageRequest struct {           <- Payload map[string]any  (counted)
service/request.go:48:type ClaimTaskRequest struct {                <- Actor authz.Actor       ** NOT counted **
service/request.go:56:type CompleteTaskRequest struct {             <- Output (counted) AND Actor ** NOT counted **
service/request.go:77:type ReassignTaskRequest struct {             <- By    authz.Actor       ** NOT counted **
service/request.go:91:type RefreshTaskCandidatesRequest struct {    <- By    authz.Actor       ** NOT counted **
service/request.go:100:type CancelInstanceRequest struct {
service/request.go:107:type ResolveIncidentRequest struct {
service/request.go:120:type ResolveCompensationStallRequest struct {

$ grep -rn "type Actor struct" -A5 authz/authz.go
authz/authz.go:35:type Actor struct {
	ID         string         `json:"id"`
	Roles      []string       `json:"roles,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`     <-- an unbounded caller-supplied map
}
```

And where it lands — `authz/authz.go:130-136`, the ABAC predicate env:

```go
func (RoleAuthorizer) Authorize(_ context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error {
	…
	if spec.Attribute != "" {
		env := map[string]any{
			"actor": actor,          // <-- carries Attributes, UNBOUNDED
			"vars":  vars,           // <-- the only half D2 bounds
		}
		ok, err := attrEval.EvalBool(spec.Attribute, env)
```

### Verdict

**CONFIRMED on two counts.**

1. **The "closed set of four" is not closed.** Seven `service` request types carry a caller-supplied `map[string]any`, not four: the three variable maps + `CompleteTaskRequest.Output` (counted) **plus** `ClaimTaskRequest.Actor.Attributes`, `CompleteTaskRequest.Actor.Attributes`, `ReassignTaskRequest.By.Attributes`, `RefreshTaskCandidatesRequest.By.Attributes`. The "and only four" quantifier is false as written.
2. **The headline Positive consequence is a half-true quantifier.** The ABAC env is a **two-key** map, `{"actor", "vars"}`. D2's admission bound reaches `vars`. It does **not** reach `actor.attributes`. So *"both ABAC evaluators inherit it for the caller-supplied contribution"* is true of one of the env's two caller-supplied contributions. The O(n²) cost curve D2 was derived from (`vars.items`, 10 000 → 2.458 s) applies unchanged to a predicate over `actor.attributes.<key>` — and `authz`'s evaluator is the one whose 5 s timeout **abandons the running goroutine** (ADR `:431-435`), so the burn survives the timeout.

⚠ **Scope honesty — this is why it is MAJOR and not unqualified Critical.** Over today's **HTTP** transport the hole is not reachable: `httpcore.Actor` (`transport/http/httpcore/dto.go:12-15`) declares only `ID` and `Roles`, and `ClaimTask`/`CompleteTask` build `authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles}` (`endpoints.go:118`), dropping attributes. The exposure is on the axis CLAUDE.md says wins every tie — **the library**: `service.ClaimTaskRequest` is public module-root API, an embedding consumer populates `Actor` directly, and a consumer-installed `ActorResolver` (`humantask/humantask.go:176`) is documented as the intended way to *"populate Attributes"* (`authz/authz.go:34`). ⚠ This finding stands entirely on today's code and introduces **no ADR-0185 symbol**; ADR-0185 is not required for it and does not fix it.

### Damage if acted on

Phase 1's brief is *"Enforced at the four caller-supplied request fields and **nowhere else**"* — an explicit prohibition. The implementer therefore cannot add the actor-attribute bound even if they notice it, and the ADR's Positive consequence ships promising coverage of *"both ABAC evaluators"* that the code does not deliver for half of each evaluator's env. That is the ADR-0162 zombie-scope shape rule #11 names: an ADR promising behaviour nobody built.

### Proposed replacement wording

- **Evidence §4.6** → *"**Four caller-supplied VARIABLE-map fields**, from the request types: … ⚠ **Not the only caller-supplied `map[string]any` in `service`.** `ClaimTaskRequest.Actor`, `CompleteTaskRequest.Actor`, `ReassignTaskRequest.By` and `RefreshTaskCandidatesRequest.By` each carry `authz.Actor.Attributes map[string]any`, which is **not bounded by D2** and which reaches the ABAC env at `authz/authz.go:132`. The grep `map[string]any service/request.go` cannot see it — the field's declared type is `authz.Actor`."*
- **ADR D2** → after the four-field list, add: *"⚠ **What this seam does NOT cover:** `authz.Actor.Attributes`, the other caller-supplied `map[string]any` on four of the same request types. It reaches the ABAC predicate env (`authz/authz.go:132`: `env := {"actor": actor, "vars": vars}`) as the `actor` key. Bounding it is a **new backlog item**, not a silent gap: it needs an actor-shape decision this delivery does not have. Over today's HTTP transport it is unreachable (`httpcore.Actor` has no `Attributes` field); over the **library** API it is reachable directly."*
- **ADR Consequences `:776-780`** → replace *"every expression surface … inherits it"* with the true quantifier: *"every expression surface that reads process **variables** inherits it for the caller-supplied contribution **to `vars`**. ⚠ The ABAC env has a second caller-supplied key — `actor.attributes` — which this bound does not reach."*
- **Plan §0** → move item 1 from "attack this" to "**answered: the set is NOT closed; see D2's new not-covered clause**", so the re-audit does not spend budget re-deriving it.

---
## C8 — CRITICAL — the covered set is **eleven** paths but the source-break list threads the response policy into only **eight** functions. The three direct-`NewInstanceView` admin endpoints have NO WAY to receive it

### The claim, verbatim, in every place it appears

- **ADR D4**, `docs/adr/0186-…md:539-542` — *"⚠⚠ **The covered set is the ELEVEN paths named in Context §4**, applied in a helper **each one calls** … The three direct-`NewInstanceView` admin endpoints (`ResolveIncident`, `CancelInstance`, `ResolveCompensationStall`) **are in the set**."*
- **ADR Negative / BREAKING (source)**, `:809-813` — *"**BREAKING (source)**: **the eight exported `httpcore` endpoint functions that project instance state** gain the response-policy parameter Decision 4 needs — `GetInstanceSnapshot`, `GetActionableView`, and the six taking `mapper func(engine.InstanceState) any`. All are called from the three adapters' `groups.go` … Threaded in **one** edit as a single parameter, not eight ad-hoc ones."*
- **Plan §2 map**, `docs/plans/2026-08-21-…md:155` — *"| D4 endpoint signature thread (8 functions) | 3 | `transport/http/httpcore` |"*
- **Plan phase 3**, `:358` — *"Thread the response policy into the **eight** exported endpoint functions in **one** edit."*
- **Plan phase 7**, `:620-621` — *"(v) the **eight exported endpoint functions** gain the response-policy parameter — a **source** break"*

### Re-derivation — why this net is better than the author's

The author's net for the *signature* list was *"the functions that take a `mapper` parameter, plus the two mapper-less ones I already knew about"* — i.e. it was derived from the **pre-correction** read-path enumeration (6 + 2 = 8), and was never re-derived after the enumeration was corrected to eleven. My net: enumerate the exported `httpcore` functions on the corrected eleven-path list and read each signature.

```
$ grep -n "^func …" transport/http/httpcore/endpoints.go
 25:func StartInstance(…, mapper func(engine.InstanceState) any) (int, any, error)     1
 47:func GetInstance(…, mapper func(engine.InstanceState) any) (int, any, error)       2
 60:func GetInstanceSnapshot(ctx, svc service.Service, id string) (int, any, error)    3
 72:func GetActionableView(ctx, svc service.Service, id string) (int, any, error)      4
 82:func DeliverSignal(…, mapper func(engine.InstanceState) any) (int, any, error)     5
116:func ClaimTask(…, mapper func(engine.InstanceState) any) (int, any, error)         6
129:func CompleteTask(…, mapper func(engine.InstanceState) any) (int, any, error)      7
145:func ReassignTask(…, mapper func(engine.InstanceState) any) (int, any, error)      8

$ grep -n "^func …" transport/http/httpcore/admin_endpoints.go
102:func ResolveIncident(ctx, svc service.Service, instanceID, incidentID string,
                        in ResolveIncidentInput) (int, any, error)                     9  <-- NOT in the eight
116:func CancelInstance(ctx, svc service.Service, instanceID string) (int, any, error) 10  <-- NOT in the eight
496:func ResolveCompensationStall(ctx, svc service.Service, instanceID string,
                        in ResolveCompensationStallInput) (int, any, error)            11  <-- NOT in the eight
```

**Eleven exported functions project instance state. Eight are listed.** The three missing ones are *exactly* the three the read-path enumeration had to be corrected to include — the correction landed in D4's covered set and in Context §4, and never propagated to the Consequences' source-break list or to the plan.

Their signatures take **no `mapper`, no `CustomizeConfig`, no policy** — only `(ctx, svc, ids, input)`. There is no channel through which a `RedactVariables` hook can reach them.

### Verdict

**CONFIRMED.** Two sentences in the same ADR contradict each other: *"the covered set is the ELEVEN paths … applied in a helper each one calls"* and *"**the eight** exported `httpcore` endpoint functions that project instance state gain the response-policy parameter."* Eleven paths cannot each call a helper that needs a policy when only eight functions can receive one.

⚠ This is the **root-cause shape audit #1 named**, recurring: *"a decision stated in the ADR whose realisation lands in a package no phase assigns it to."* Here it is one step subtler — the decision has a phase (3) and a package (`httpcore`), but the **work item under it is sized for the pre-correction count**. The plan's §2 mechanical check verified that every ADR sentence has a *row*; it does not verify that the row's *quantity* matches the sentence's.

### Damage if acted on

The phase-3 agent is briefed with an exact number (*"Thread the response policy into the **eight** exported endpoint functions in **one** edit"*). It threads eight, wires the helper into eight, and phase 3 test 8 (`TestRedactionCoversAllElevenReadPaths`) then fails on the three admin rows — the plan's own falsifier says *"each admin row **fails against a fix confined to `mapInstance`**, which is the whole point."* At that point the agent is in the worst position: an *unlisted* breaking change to three more exported functions, discovered mid-phase, on an ADR that says the source break is exactly eight. Per rule #11 that is an ADR amendment; per the plan it looks like a test to be relaxed. And the ADR's own D4×D5 interaction row (`:182`) exists precisely because *"Both force **unlisted** breaking changes to exported `httpcore` signatures"* — the fix for that row listed 8 and left 3 unlisted.

Alternatively the agent "solves" it by leaving redaction off the three admin endpoints, which is the exact defect D4 was corrected to close.

### Proposed replacement wording

- **ADR Negative / BREAKING (source) `:809-813`** → *"**BREAKING (source)**: **the eleven exported `httpcore` endpoint functions that project instance state** gain the response-policy parameter Decision 4 needs — `GetInstanceSnapshot`, `GetActionableView`, the six taking `mapper func(engine.InstanceState) any`, **and the three admin endpoints that call `NewInstanceView` directly (`ResolveIncident`, `CancelInstance`, `ResolveCompensationStall`)**. Threaded in **one** edit as a single parameter. ⚠ The count is eleven, not eight, and it must stay equal to D4's covered set — if they ever differ, one of them is wrong."*
- **Plan §2 map `:155`** → *"| D4 endpoint signature thread (**11** functions — 8 in `endpoints.go`, **3 in `admin_endpoints.go`**) | 3 | `transport/http/httpcore` |"*
- **Plan phase 3 `:358`** and **phase 7 `:620`** → **eleven**, with the three admin names spelled out.
- Add to phase 3 a stated invariant: *"the number of endpoint functions taking the response-policy parameter == the number of rows in `TestRedactionCoversAllElevenReadPaths`."*

---

## C9 — MAJOR — the plan's §4 evidence row uses `\|` under `-E` — the EXACT defect the plan warns about two paragraphs above it — and its forward-looking "must return 26" is also wrong

### The claim, verbatim

- **Plan §4 preamble**, `docs/plans/2026-08-21-…md:640-642` — *"⚠ Every row was re-run in the working tree. **Bare `|` under `-E`** — `\|` in ERE is a *literal* pipe, which is how the previous revision's "0 existing caps" evidence became a command that returns 0 for **any** repository."*
- **Plan §4, the very next table, row 4**, `:649` — *"| …already capped by us | **0** — `grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/` exits 1. ⚠ After phase 4 this must return **26** (stdlib 13 + gin 13); fiber uses a `BodyRaw()` pre-check and will not match |"*
- (For contrast, the **ADR** got it right — Context §1 `:53`: `grep -rnE "MaxBytesReader|BodyLimit" transport/`, bare pipe.)

### Re-derivation (executed)

```
$ grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/   # the plan's own command
EXIT=1
$ grep -rnE 'MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader' transport/      # correct ERE
EXIT=1
```

Both exit 1, so the *conclusion* ("0 caps today") is true. Control proving the plan's command is nonetheless unfalsifiable:

```
$ grep -rnE 'package\|zzzzz' transport/http/httpcore/errors.go ; echo EXIT=$?
EXIT=1                                    # returns nothing for a file whose line 1 IS "package httpcore"
$ grep -rnE 'package|zzzzz' transport/http/httpcore/errors.go ; echo EXIT=$?
transport/http/httpcore/errors.go:1:package httpcore
EXIT=0
```

### Verdict

**CONFIRMED.** The revision's edit that added the warning did not fix the command the warning is about — in the same table, four lines below it. This is Premise Discipline's named pattern verbatim: *"two were introduced by the very edits removing earlier ones."*

⚠ **And the row's forward-looking count is wrong independently.** *"After phase 4 this must return **26** (stdlib 13 + gin 13); fiber uses a `BodyRaw()` pre-check and will not match."* The pattern's second alternative is **`BodyLimit`**, and ADR D1 `:310-316` **mandates** fiber code referencing `fiber.DefaultBodyLimit` — *"Fiber's `Mount` therefore **logs a WARN at mount time** when `MaxBodyBytes > fiber.DefaultBodyLimit`"*, restated in plan phase 4 `:466-467`. `fiber.DefaultBodyLimit` contains the substring `BodyLimit`, so fiber **will** match, at least once for the comparison. The expected post-phase-4 value is **not 26**.

### Damage if acted on

The plan explicitly offers this row as a **post-implementation verification** (*"After phase 4 this must return 26"*). Run as written, it returns **0** — reading as "no caps were installed" after phase 4 installed 26. An agent chasing that discrepancy loses time; an agent that instead adjusts the expected number to 0 disarms the check permanently. Either way, the one mechanical check on whether the body caps actually landed is broken, and it is broken by the exact defect the paragraph above it exists to prevent.

### Proposed replacement wording

Plan §4 row 4:

> | …already capped by us | **0** — `grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/` — ⚠ **NO.** Write it with a **bare** pipe: `grep -rnE 'MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader' transport/` exits 1. **Control that the command can fail at all:** `grep -rnE 'package|zzzzz' transport/http/httpcore/errors.go` must exit **0**. ⚠ After phase 4 this returns **26 `MaxBytesReader` hits (stdlib 13 + gin 13) plus fiber's `fiber.DefaultBodyLimit` references from the mandated mount-time WARN** — so pin the check on `MaxBytesReader` alone (`grep -rn 'MaxBytesReader' transport/ | wc -l` == 26) and check fiber separately on `BodyRaw`. |

And add to the verification checklist: *"⚠ every `grep -E` in this bundle must be accompanied by a control proving it can match something."*

---
## C10 — MINOR (×2) — two ANCHOR failures: `doc.go:66` resolves to a file in a DIFFERENT package than every other citation around it, and `dto.go:174` does not contain the text it is cited for

The spec's header (`docs/specs/2026-08-21-untrusted-input-and-disclosure.md:16-21`) withdraws the previous revision's *"every citation was re-derived here"* quantifier and states that **line numbers appear only where exact ordering is load-bearing**. Both surviving citations below are load-bearing by that test, and both are off.

### C10a — `doc.go:66` is the **repo-root** `doc.go`, not `transport/http/httpcore/doc.go`

**Claim, verbatim:**
- **ADR D5**, `docs/adr/0186-…md:696-697` — *"Changing that signature breaks an exported function `doc.go:66` advertises as a consumer seam."*
- **Plan phase 3**, `docs/plans/2026-08-21-…md:350` — *"Changing `ClassifyError(err error)` would break an exported function `doc.go:66` advertises as a consumer seam."*

**Re-derivation:**
```
$ find transport -name 'doc.go' | wc -l
0
$ grep -rn "ClassifyError" doc.go
doc.go:66://     ClassifyError (5xx redaction), Instrumentation.Observe (static route template),
$ ls -la doc.go
-rw-r--r--@ 1 zakyalvan wheel 6328 doc.go          <-- MODULE ROOT
```

**Verdict: CONFIRMED anchor failure.** `transport/http/httpcore/doc.go` **does not exist**. Every other bare-filename citation in the same passage — `errors.go:38-49`, `view.go:31`, `admin_endpoints.go:30`, `dto.go:174`, `seam.go:42` — is package-relative to `httpcore`, so a reader resolves `doc.go:66` the same way and finds nothing. The target is the module-root package doc, two directory levels up and in a different Go package.

**Damage:** the sentence is the sole justification for the D5 fold that keeps `ClassifyError`'s signature and mints the correlation id in `writeErr` instead — a decision that moves work from phase 3 to phase 4 and is listed as a deliberate non-break in the Consequences (`:814-815`). An implementer who cannot resolve the citation cannot check the premise, and the cheapest reaction is to conclude the constraint is stale and change the signature after all.

**Proposed replacement:** *"…breaks an exported function the module-root package doc advertises as a consumer seam (`/doc.go:66`, repo root — **not** `httpcore/doc.go`, which does not exist)."*

### C10b — `dto.go:174` is the continuation line; the quoted `got %q` is on `:173`

**Claim, verbatim (three places, all quoting the text):**
- **ADR D5**, `:638-640` — *"⚠ Two `ErrBadInput` **wrap sites** embed a caller value and are edited rather than blanked: `admin_endpoints.go:30` (`unknown status %q`) and **`dto.go:174` (`got %q`)**."*
- **Evidence §4.7**, `docs/specs/2026-08-21-adr-0186-premise-evidence.md:251` — *"`transport/http/httpcore/dto.go:174    fmt.Errorf("%w: disposition must be one of retry, skip, abandon (got %q)", ErrBadInput, s)`"* — presented as a **single line** at `:174`.
- **Plan phase 3**, `:343` — *"`dto.go:174` (`got %q`)"*

**Re-derivation:**
```
$ sed -n '172,175p' transport/http/httpcore/dto.go | cat -n
     1		default:
     2			return 0, fmt.Errorf("%w: disposition must be one of retry, skip, abandon (got %q)",   <- :173
     3				ErrBadInput, s)                                                                    <- :174
     4		}
```

**Verdict: CONFIRMED, off by one, in the same direction as the previous round's four.** The call spans `:173-174`; the cited line `:174` contains only `ErrBadInput, s)` and **not** the `got %q` text quoted alongside it. Evidence §4.7 additionally renders the two-line call as one line, so its "quoted command output" is a reconstruction rather than a paste — see C11.

**Damage:** low in isolation (the sibling citation `admin_endpoints.go:30` is exact, and the call is findable). Recorded because this citation is one of the handful the spec's header explicitly kept on the grounds that its position is load-bearing, and because the last round's four off-by-one citations were the anchor half of this lens's two named failure classes.

**Proposed replacement:** `dto.go:173-174` in all three places, or drop the line number and cite `ParseCompensationDisposition`'s `default` arm by symbol, which is what the spec header says it prefers.

---

## C11 — MAJOR — the evidence file's "pasted probe output" is RECONSTRUCTED, not pasted, in at least two of its sections. The file whose sole purpose is executed evidence contains transcripts no command produces

### The claim, verbatim

The evidence file's charter, `docs/specs/2026-08-21-adr-0186-premise-evidence.md:7-9` — *"**Why this file exists.** The revision folding audit #1 changes five of six decisions. Every claim about current behaviour that those changes rest on is **executed here, by the author** … not reasoned from source, and not inherited."* And §4's preamble `:125-127` — *"Every count below was re-run in the working tree at the bundle commit. **Commands are quoted so the next reader can re-run them.**"*

### Re-derivation

**§4.3** quotes a command and its output:
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

Run at `677760d5`, that command emits:
```
$ sed -n '22,42p' runtime/view/instance_actionable.go

// ActionableTask is the curated view of a single open human task together with
// the allowed next actions derived from the process definition.
type ActionableTask struct {
	// TaskID is the unique task instance identifier.
	TaskID string `json:"task_id"`
	// NodeID is the BPMN node that generated this task.
	NodeID string `json:"node_id"`
	// State is the string representation of the task's lifecycle state.
	State string `json:"state"`
	// Claim records who claimed the task and when; nil when unclaimed.
	Claim *humantask.Claim `json:"claim,omitempty"`
	// Candidates holds the resolved actors eligible to act on this task, rendered
	// verbatim as {id, roles, attributes} (ADR-0147).
	Candidates []authz.Actor `json:"candidates,omitempty"`
	// AllowedActions lists the outgoing sequence flows from this task's node,
	// derived from the process definition. When def is nil, this is nil (no
	// routing information is available).
	AllowedActions []NextAction `json:"allowed_actions,omitempty"`
}
```

The doc comments are stripped, the alignment is re-flowed, and a **doc comment has been converted into a trailing `//` comment with quotation marks around it** — a form that appears nowhere in the source. `sed` cannot produce this.

**§4.7** presents a two-line `fmt.Errorf` call as a single line at `:174` (see C10b) — likewise not what any `grep`/`sed` emits.

**§4.1** quotes a command that is not a command at all:
```
$ for a in stdlib gin fiber; do printf "%s " $a; done            # idiom per adapter
stdlib 13 (json.NewDecoder)   gin 13 (ShouldBindJSON)   fiber 13 (c.Bind())        = 39
```
That loop prints `stdlib gin fiber ` and nothing else. The counts shown are hand-written.

### Verdict

**CONFIRMED.** The *substance* of all three sections is **true** — I verified each independently (see the batch table: `ActionableTask` has exactly six fields and no `Vars`; the per-adapter decode counts really are 13/13/13; the `dto.go` call really does embed `got %q`). The defect is not in the numbers; it is that the file designated as this bundle's executed-evidence record contains **transcripts that were composed rather than captured**, in the sections whose purpose is to be re-runnable by the next reader.

⚠ Severity is MAJOR rather than MINOR for one reason: **a reconstructed transcript is indistinguishable from a fabricated one at review time**, and the entire mechanism CLAUDE.md's Premise Discipline installs — *"paste the real numbers"* — depends on the paste being a paste. The previous round found three findings inside the bundle's own evidence file; this is the mechanism by which a fourth would hide. §2 and §3, by contrast, quote real `go test` output (`=== RUN` / `--- PASS` framing) and are credible on their face.

### Damage if acted on

Low direct damage — the conclusions hold. The cost is to the audit contract: plan §0 item 11 says *"**Attack the evidence file too.** … This delivery has a new one, written by the author, and it is an **input** to the audit, not a conclusion of it."* An auditor who re-runs §4.3's command gets output that does not match the file and must then decide whether the mismatch is drift, a wrong anchor, or an error — spending budget on a discrepancy that is only cosmetic. That is exactly the budget the counting lens needs for the real enumerations.

### Proposed replacement wording

Paste real output, or label the difference. Concretely, in evidence §4:

> ⚠ **Transcript convention.** Blocks framed `=== RUN … --- PASS` are verbatim `go test` output. Blocks framed `$ <command>` are verbatim shell output — **if a listing is abridged for length, mark it `# … doc comments elided`** rather than silently re-flowing it. Never re-type a listing: a composed transcript cannot be distinguished from a fabricated one.

and fix §4.1's non-command to the three commands that actually produce the numbers:
```
$ grep -rn "json.NewDecoder" --include='*.go' transport/http/stdlib/ | grep -v _test | wc -l   # 13
$ grep -rn "ShouldBindJSON"  --include='*.go' transport/http/gin/    | grep -v _test | wc -l   # 13
$ grep -rn "Bind()\.JSON"    --include='*.go' transport/http/fiber/  | grep -v _test | wc -l   # 13
```
(All three verified at `677760d5` — see the batch table.)

---
## C12 — CRITICAL — "`mergeVars` from **three** non-request sources" names EIGHT sites' worth of behaviour with three citations, and **one of the three it names is admission site #4 of D2's own closed set**. Two prescribed phase-1 tests would assert opposite outcomes on the same code path

### The claim, verbatim

- **ADR D2, "What this decision does NOT bound"**, `docs/adr/0186-…md:417-421`:
  > *"⚠⚠ **Runtime growth.** The variable map is also grown by `mergeVars` from **three non-request sources** — a service action's output (`engine/step_triggers.go:161`), **human-task completion output (`:936`)** and the message/callback mirror (`:1208`) — plus the engine's own `_errorMessage`/`_errorAttempts` writes. **None of these is bounded by this decision, and that is deliberate.**"*
- **Spec §3 (Out)**, `docs/specs/2026-08-21-untrusted-input-and-disclosure.md:127-128` — *"**Runtime variable growth** via `mergeVars` from action/task/message output. Deliberately unbounded here."*
- **ADR Consequences**, `:825-827` — *"**Runtime variable growth is not bounded**, by decision. A `mergeVars` from a service action's output can carry the map past either bound with no caller present."*
- **ADR Neutral/follow-ups**, `:844-845` — *"**New item: bound runtime variable growth** (`mergeVars` from action/task/message output)"*
- The author asked for exactly this attack — **plan §0 item 2**, `:50-53`: *"**Runtime growth is out of scope BY DECISION** (ADR D2). Attack that decision, not its absence: is there a caller-reachable path that grows the map without passing an admission point — a signal payload merged after admission, a callback mirror, a chained instance's `start_vars`?"*

### Re-derivation — why this net is better than the author's

The author's net was *"cite the `mergeVars` sites I already had in hand."* Three line numbers, no command. My net: enumerate **every** call site of the function, then resolve each to its enclosing handler and to the trigger field it merges — because the load-bearing question is not *how many sites* but *which side of the admission boundary each one is on*.

```
$ grep -rn "mergeVars(" --include='*.go' . | grep -v _test
engine/step_state.go:314:func mergeVars(s *InstanceState, in map[string]any) {     <- the definition
engine/step_triggers.go:45,161,841,936,1028,1208,1312,1349                <- EIGHT call sites
```

Resolved to enclosing function (AST-free scan over `step_triggers.go`):

| site | enclosing handler | merges | which side of admission? |
|---|---|---|---|
| `:45` | `handleStartInstance` | `t.Vars` | **REQUEST** — `StartInstanceRequest.Vars`, D2 admission site **#1** |
| `:161` | `handleActionCompleted` | `t.Output` | non-request (service action output) — ✅ **named by the ADR** |
| `:841` | `applyOutcomeExposure` | `{name: outcome}` | non-request (engine-authored outcome variable) — ❌ **not named** |
| `:936` | `handleHumanCompleted` | `t.Output` | **REQUEST** — `CompleteTaskRequest.Output`, D2 admission site **#4** — ❌ **named by the ADR as NON-request** |
| `:1028` | `handleSignalReceived` | `t.Payload` | **REQUEST** — `DeliverSignalRequest.Payload`, admission site **#2** — ❌ **not named** |
| `:1208` | `handleSubInstanceCompleted` | `t.Output` | non-request (a **child instance's** output map) — ⚠ **named, but MISLABELLED** as "the message/callback mirror" |
| `:1312` | `handleMessageReceived` | `t.Payload` | **REQUEST** — `DeliverMessageRequest.Payload`, admission site **#3** — ❌ **not named** |
| `:1349` | `handleMessageReceived` | `t.Payload` | **REQUEST** — same, a *second* site in the same handler — ❌ **not named** |

The `:936` = admission-site-#4 identity, verified end to end:
```
service/service.go:432:  engine.CompletionInput{Outcome: req.Outcome, Note: req.Note, Output: req.Output}
runtime/task/service.go:259:  return engine.NewHumanCompleted(s.clk.Now(), taskID, c, actor), nil
engine/trigger.go:399:   func NewHumanCompleted(at time.Time, taskID string, c CompletionInput, actor authz.Actor) HumanCompleted
engine/step_triggers.go:936:  mergeVars(s, t.Output)          // t is HumanCompleted; t.Output IS CompleteTaskRequest.Output
```

### Verdict

**CONFIRMED WRONG in three independent ways** — and this is the ADR-0175 shape exactly (*"three compensation dispatch sites existed where the bundle named two"*), except worse, because here the miscount crosses the decision's own boundary.

1. **The count.** Eight `mergeVars` sites, not three. The genuinely non-request subset happens to number three (`:161`, `:841`, `:1208`) — but they are **not the three the ADR names**. The arithmetic coincidence is what makes this survive a casual read.
2. **`:936` is on the wrong side.** D2's decision text bounds `CompleteTaskRequest.Output` (it is admission site #4, ADR `:368-369`, evidence §4.6). D2's *"what this does NOT bound"* text, **eighty lines later in the same decision**, names the same field as unbounded runtime growth. One decision, both claims.
3. **`:1208` is mislabelled.** It is `handleSubInstanceCompleted` — the call-activity return path, where a **child instance's entire variable map** merges into the parent. The actual message mirror is `handleMessageReceived` (`:1312`, `:1349`), cited nowhere. So the one genuinely alarming unbounded path — cross-instance amplification, where a child that grew at runtime dumps its whole map into a parent, with no caller and no bound at either end — is present in the list under the wrong name and therefore reasoned about as something else.

### Damage if acted on

**The direct hit is two phase-1 tests that assert opposite outcomes on the same code path.** Plan phase 1 prescribes both:

- **Test 1** (`:217-222`) — *"`TestStartInstanceRefusesOversizedVariablesByBytes` and `…ByElementCount` — table over the **four** admission fields"* ⇒ an oversize `CompleteTaskRequest.Output` **must be refused**.
- **Test 6** (`:244-248`) — *"`TestRuntimeVariableGrowthIsNotRefused` — ⚠ **the scope control.** An action output merged via `mergeVars` that carries the map past the bound must **not** be refused … **Falsifier:** *it fails against an implementation that checks at the persist boundary instead of at admission.*"*

An implementer building test 6's fixture goes to the ADR for the `mergeVars` sites, finds three named, and picks one. If they pick `:936` — one of the three, and the most convenient to drive from `service` because it has a public request type — test 6 asserts that an oversize `CompleteTaskRequest.Output` is **accepted**, while test 1 asserts it is **refused**. The phase cannot go green. Worse, the plan gives test 6 an escalation clause it does not give test 1, so the likely resolution is to weaken **test 1** — deleting the bound on admission site #4, the only one of the four that a *task claimant* (rather than a process starter) controls.

Secondary damage: `:841` and the two `handleMessageReceived` sites are absent from every document, so the backlog item the ADR opens (*"bound runtime variable growth (`mergeVars` from action/task/message output)"*) is scoped against a three-item list that is wrong about which three. And the cross-instance amplification at `:1208` never gets stated as what it is.

### Proposed replacement wording

ADR D2, replace `:417-421`:

> ⚠⚠ **Runtime growth.** `mergeVars` (`engine/step_state.go:314`) has **eight** call sites, all in `engine/step_triggers.go`. **Four are the admission sites this decision bounds** — `:45` `handleStartInstance` (`StartInstanceRequest.Vars`), `:936` `handleHumanCompleted` (`CompleteTaskRequest.Output`), `:1028` `handleSignalReceived` and `:1312`/`:1349` `handleMessageReceived` (the two `Deliver*Request.Payload` fields). **Three are non-request growth this decision deliberately does NOT bound:** `:161` `handleActionCompleted` (a service action's output), `:841` `applyOutcomeExposure` (the engine-authored outcome variable), and `:1208` `handleSubInstanceCompleted` — ⚠ **not** the message mirror, but a **child instance's whole output map merging into its parent**, i.e. cross-instance amplification with no caller at either end. Plus the engine's own `_errorMessage`/`_errorAttempts` writes (`:515`, `:517`). **None of the three non-request sources is bounded here, and that is deliberate** (bounding them means refusing a persist after the side effect already happened — see below).

Spec §3, ADR Consequences `:825` and ADR follow-ups `:844` — replace *"action/task/message output"* with *"**a service action's output, the outcome-exposure write, and a child instance's output on the call-activity return path**. ⚠ Human-task completion output and the signal/message payloads are **request** sources and **are** bounded — they are three of D2's four admission fields."*

Plan phase 1 test 6 — pin the fixture: *"⚠ **The fixture must be `handleActionCompleted` (`step_triggers.go:161`) or `handleSubInstanceCompleted` (`:1208`). It must NOT be `handleHumanCompleted` (`:936`) — that is `CompleteTaskRequest.Output`, admission site #4, which test 1 asserts is REFUSED. An earlier draft of ADR D2 listed `:936` as a non-request source; it is not."*

---
## C13 — MAJOR — spec §5 claims it derived "**all 15** D×D pairs plus 8 cross-cutting ones". It has **13** of 15 and **10** cross-cutting. The two missing pairs are **D1×D4** and **D4×D6** — and D4×D6 is the same "reader composes them into a guarantee neither makes" hazard the table catches for D3×D4

### The claim, verbatim

- **Spec §5**, `docs/specs/2026-08-21-untrusted-input-and-disclosure.md:159-162` — *"⚠⚠ **The previous revision's table was wrong in five of its eight rows and omitted two pairs entirely** — the interaction lens derived **all 15 D×D pairs plus 8 cross-cutting ones**. This table is rebuilt from that derivation, and every row states the *resolution now in the ADR*, not a hope."*
- **Plan §0 item 9**, `docs/plans/2026-08-21-…md:74-77` — *"Spec §5 is now a **21-row table** rebuilt from that lens's own derivation — **attack the rows it marks ✅, and find the pairs it still omits.** The changed decisions, for the pairwise brief: **D1, D2, D3, D4, D5 and D6** — that is all six, so the interaction lens has the full grid to re-derive."*

### Re-derivation

Row count first (the two documents disagree with each other before either is checked against the grid):

```
$ awk 'NR>=164 && NR<=189 && /^\|/ && !/^\|---/ && !/^\| pair/ {n++} END{print "ROWS =", n}' \
    docs/specs/2026-08-21-untrusted-input-and-disclosure.md
ROWS = 23
```

**23 rows** — spec §5's own 15 + 8 = 23 is self-consistent; **plan §0's "21-row table" is wrong.** Now the grid. With six decisions, C(6,2) = **15** unordered D×D pairs. Present in the table:

| | D2 | D3 | D4 | D5 | D6 |
|---|---|---|---|---|---|
| **D1** | ✅ | ✅ | ❌ **MISSING** | ✅ | ✅ |
| **D2** | — | ✅ | ✅ | ✅ | ✅ |
| **D3** | — | — | ✅ | ✅ | ✅ |
| **D4** | — | — | — | ✅ (×2 rows) | ❌ **MISSING** |
| **D5** | — | — | — | — | ✅ |

**13 of 15 present.** The remaining 10 rows are self-pairs and cross-cutting (`D2 × itself`, `D4 × itself`, `D4 × the read hot path`, `D2 × ADR-0049 replay`, `D2 × the shipped runtime options`, `D4 × the response-customization feature`, `D5 × the deferred ADR-0185`, `any × ADR-0095`, `any × ADR-0145/0147`, and `D4 × D5 (breaking surface)` as a second row on an existing pair) — **10, not 8**.

So: 23 = **13 D×D + 10 other**, not "15 + 8". Both components are wrong; the total is right because the two errors compensate — which is why it reads as verified.

### Verdict

**CONFIRMED.** A completeness quantifier (*"all 15 D×D pairs"*) asserted over a grid that is short two cells, in the one table CLAUDE.md rule #9's interaction clause exists to make exhaustive, in a bundle whose predecessor *"failed a second audit on the interactions."*

⚠ Note the failure class: this is not a net failure and not an anchor failure — it is a **completeness claim over a closed, trivially enumerable grid that nobody enumerated**. The grid has fifteen cells; drawing it takes a minute; the claim that it was drawn was made without drawing it.

### Damage if acted on

**D4 × D6 is the substantive omission.** D4 establishes that `RedactVariables` runs *"at the `ProcessInstance` → response boundary"* (ADR `:526`) — i.e. **after** persist. D6 enumerates the plaintext columns at rest, headed by `wrkflw_instances.snapshot`, *"the whole instance state, incl. every process variable"*. Both are written into **the same `SECURITY.md`, in the same phase 7, by the same author** (plan `:597-616`). A reader who finds "we redact variables from responses" and "here are the plaintext columns" in one document will compose them into *"redaction reduces what is stored"* — which is false: the hook never touches the persisted snapshot.

That is **exactly** the hazard the table successfully catches one row above, for D3 × D4 (`:178`): *"✅ Not a defect — but the two ship in one `SECURITY.md` and a reader would compose them into a guarantee neither makes. One sentence in D3 and in phase 9."* The identical mitigation is owed to D4 × D6 and is not written anywhere. (⚠ And note that row's own anchor is stale: it says *"phase 9"*; this plan has **seven** phases — see the batch table.)

**D1 × D4** is benign but not free: both add fields to `CustomizeConfig` (`MaxBodyBytes`; `RedactVariables` + `RedactionScope`) and both are wire-breaking, in the same phase-3 edit by the same agent — and D4's is the signature thread that C8 shows is sized wrong. An explicit row would have forced the author to state the combined `CustomizeConfig` delta in one place.

### Proposed replacement wording

- **Spec §5 `:159-162`** → *"…the interaction lens derived **13 of the 15 D×D pairs plus 10 cross-cutting ones — 23 rows**. ⚠ **D1×D4 and D4×D6 were missing and are added below.** Do not restate a completeness claim over this grid without drawing the 15-cell matrix; the previous statement said "all 15" over 13."*
- **Plan §0 item 9 `:75`** → *"a **23-row** table"*.
- Add the two rows:

| pair | interaction | resolved? |
|---|---|---|
| **D1 × D4** | Both extend `CustomizeConfig` in the same phase-3 edit (`MaxBodyBytes`; `RedactVariables` + `RedactionScope`) and both are wire-breaking. D4 additionally threads a response-policy parameter through the exported endpoint functions. | ✅ benign, but the **combined** `CustomizeConfig` delta and the full source-break list must be stated once, in one place — see the eleven-function correction. |
| **D4 × D6** | ⚠ **The same "reader composes them" hazard as D3×D4.** `RedactVariables` runs at the **response** boundary, *after* persist; `wrkflw_instances.snapshot` (and `wrkflw_human_task.vars`) hold the **unredacted** map. Both statements land in one `SECURITY.md`, in one phase, written by one author. | ✅ Requires one explicit sentence in D6 and in phase 7: *"redaction is a **display** control and does **not** reduce what is stored — every plaintext column below holds the unredacted values."* Without it `SECURITY.md` implies a guarantee neither decision makes. |

---
## C14 — CRITICAL — SEVEN stale phase pointers across the spec and ADR after two phases were deleted. Three name phases that DO NOT EXIST — including D6's entire deliverable instruction, which points at "Phase 9"

### The claim, verbatim

The plan's banner announces the renumbering (`docs/plans/2026-08-21-…md:10-12`): *"⭐ Two phases were **deleted** by the admission move (`internal/expreval`, `runtime`), and the phase that was last is now first."* The spec and ADR were not renumbered with it.

### Re-derivation

```
$ grep -n "^### Phase " docs/plans/2026-08-21-untrusted-input-and-disclosure.md
198:### Phase 1 — `service`            259:### Phase 2 — runtime/validation + validate/expr
324:### Phase 3 — httpcore             431:### Phase 4 — {stdlib,gin,fiber}
504:### Phase 5 — action/httpcall      575:### Phase 6 — transport/http/parity
595:### Phase 7 — documents + caveats
```

**Seven phases exist: 1–7.** Every `phase N` reference in the other two documents:

| where | says | what it is about | correct phase | verdict |
|---|---|---|---|---|
| **ADR `:741`** (D6) | **Phase 9** | *"⇒ **Phase 9 derives the list from `internal/persistence/store/migrations/{postgres,mysql,sqlite}` at implementation time rather than copying it from this record, and the invariant is a test**"* | **7** | ❌ **DOES NOT EXIST** |
| spec `:178` (D3×D4) | **phase 9** | the one sentence stopping a reader composing display+destination controls | **7** | ❌ **DOES NOT EXIST** |
| spec `:180` (D3×D6) | **phase 9** | "must not write them as one posture" | **7** | ❌ **DOES NOT EXIST** |
| spec `:187` (any×ADR-0095) | **phase 8** | *"phase 8 is **not** the net"* — the parity suite | **6** | ❌ **DOES NOT EXIST** |
| spec `:169` (D1×D3) | phase 6 | *"in a phase running parallel to phase 6"* — the `httpcall` phase | **5** | ❌ wrong phase |
| spec `:182` (D4×D5) | phase 5 | *"the correlation test moves to phase 5"* | **4** | ❌ wrong phase (5 is `action/httpcall`) |
| spec `:234` (§7) | Phase 5 | *"**Phase 5's** correlation-id tests must cover **both** the span path and the random-hex fallback"* | **4** | ❌ wrong phase |
| ADR `:543` (D4) | Phase 4 | the read-path count invariant | **3** | ❌ wrong phase — recorded separately as C4 |
| ADR `:683` (D5) | Phase 2 | *"Phase 2 must **not** also re-route that package through `expreval`"* | 2 | ✅ correct |
| spec `:179` (D3×D5) | phase 2 | *"phase 2 is explicitly told **not** to re-route the validator"* | 2 | ✅ correct |

**Eight of ten phase pointers outside the plan are wrong. Three name phases that do not exist.**

### Verdict

**CONFIRMED.** This is the ANCHOR half of this lens's two named failure classes, at scale — and it is the *systemic* form of it: a renumbering that happened in one document and in none of the others, where the pointers are the only thing binding a decision to the work that realises it.

⚠ The irony is load-bearing: audit #1's root-cause finding, quoted in all three banners, was *"six Criticals share one root cause: **a decision stated in the ADR whose realisation lands in a package no phase assigns it to**."* The revision's answer was plan §2's mechanical decision→phase map. That map is correct and complete — but the **ADR's and spec's own phase pointers** were never brought into line with it, so the ADR still assigns work to phases 4 and 9 while the map assigns it to 3 and 7.

### Damage if acted on

**ADR `:741` is the worst.** D6 is *"the one decision in the bundle whose **deliverable IS the enumeration**"* (ADR `:744`), and its single implementation instruction — derive the columns from the migrations rather than copying them, and pin an invariant test — is addressed to a phase that does not exist. An implementer working phase 7 from the plan gets the instruction (plan `:598-602` repeats it); an implementer working from the **ADR**, which is the authoritative design record, finds it assigned to nobody. Combined with **C1** (the count is 15/8, not 12/7) and **C2** (`claimed_by`/`completed_by` omitted), D6's deliverable is: a wrong enumeration, with an instruction not to copy it, addressed to a nonexistent phase.

Spec `:178`/`:180` are the two sentences that stop `SECURITY.md` merging a display control with a destination control and a storage posture — the mitigations for D3×D4 and D3×D6, both assigned to "phase 9". Spec `:182`/`:234` send the correlation-id tests to `action/httpcall`, a package with no `writeErr` and no `ErrorBody`; that is the *same* misplacement the revision already had to fix once (plan `:489`: *"⚠ This test was previously prescribed in phase 3, a package that **cannot emit a log record**"*), re-introduced one document over.

### Proposed replacement wording

Mechanical: renumber **9 → 7**, **8 → 6**, and fix the three wrong-but-existing pointers (spec `:169` → 5, spec `:182` → 4, spec `:234` → 4, ADR `:543` → 3).

Then add a standing guard to the plan's verification checklist, because this will recur the next time a phase is added or deleted:

> - [ ] ⚠ **Phase-pointer sweep.** `grep -oniE "phase [0-9]+"` over the spec, the ADR and the evidence file; every hit must name a phase that exists in this plan **and** must agree with §2's decision→phase map. Two phases were deleted in this revision and **eight of ten** external pointers went stale — three of them naming phases 8 and 9, which have never existed in this plan.

---
# Batch table — counts and anchors I checked and **CONFIRMED CORRECT**

Recorded so the next round does not re-derive them. Every row was re-run in the worktree at `677760d5` with a net **different** from the author's where one existed.

| claim | where | my net | verdict |
|---|---|---|---|
| decode sites = **39** (stdlib 13 `json.NewDecoder`, gin 13 `ShouldBindJSON`, fiber 13 `c.Bind().JSON`, httpcore **0**) | ADR `:51-53`, plan `:646` | per-adapter grep **plus** a wider net for other body-read idioms: `io.ReadAll`, `json.Unmarshal`, `req.Body`, `c.Body`, `BodyRaw`, `ioutil` across all of `transport/`, non-test → **EXIT=1, no fourth idiom**; and `Decode\|Unmarshal\|Bind` across `httpcore/` → only type/comment hits, **0 decode sites** | ✅ **CONFIRMED** |
| **36 propagate / 3 discard**; the 3 are `stdlib:238`, `gin:265`, `fiber:255`, all the same optional-body `ResolveIncident` route | ADR `:61-68`, evidence §4.1, plan `:647-648` | read the line **after** every one of the 39 decode branches in all three `groups.go`. All 36 are `fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)` (gin/stdlib bare `writeErr`, fiber `return writeErr`). **No fourth idiom; no site that neither wraps nor discards** | ✅ **CONFIRMED** — the author's headline enumeration is right |
| read paths = **11** = 6 `mapInstance` + 3 direct-`NewInstanceView` admin + 2 mapper-less | ADR `:135-141`, evidence §4.2, plan `:656` | four independent nets: `.State()` call sites in `httpcore`; `.Variables` reads across `transport/`+`runtime/`+`service/`; **every exported `httpcore` func**; and `.Vars` reads. Checked `DeliverMessage` (returns `(202, nil, nil)` — no body), `AdminListInstances` (`instanceSummaryView`, clean), `AdminInstanceLineage` (lineage refs only, clean). **No 12th path** | ✅ **CONFIRMED** — but see **C3** (the invariant meant to protect it is blind to 2 of the 11) and **C8** (only 8 of the 11 get the policy parameter) |
| 400-arm sentinels = **8**, across **5** `errors.Is` groups; "eight, not nine" | ADR `:193-197`, plan `:652` | read `errors.go` in full; the 8 sit on 5 source lines (`:36,:37,:42,:46,:49`) | ✅ **CONFIRMED** |
| `ClassifyError` has **6 ordered arms**: 404 `:28`, 403 `:32`, 409 `:34`, 400 `:36-50`, 422 `:51`, default 500 `:57` | plan `:650` | `cat -n transport/http/httpcore/errors.go` | ✅ **CONFIRMED — every line number exact** |
| **5** classes echo `err.Error()` today: 404 `:31`, 403 `:33`, 409 `:35`, 400 `:50`, 422 `:56`; 500 `:58` blanks | ADR `:164-165`, plan `:651` | same | ✅ **CONFIRMED — every line number exact** |
| ADR-0146/0152/0183 rationale at `errors.go:38-41`, `:43-46`, `:47-49` | ADR `:205-209` | same | ✅ **CONFIRMED — all three ranges exact** |
| `ActionableTask` = **6 fields, none `Vars`**; `NewActionableView` never reads `t.Vars`; already clones at `instance_actionable.go:88` | ADR `:150-156`, evidence §4.3, plan `:658` | read `runtime/view/instance_actionable.go` in full; `grep -rn "\.Vars"` across `transport/`+`runtime/view/`+`service/` non-test → **no HTTP path renders `HumanTask.Vars` at all** | ✅ **CONFIRMED** (⚠ the *transcript* is reconstructed — **C11**) |
| direct `expr-lang/expr` importers = **4** non-test, **3** violators (`action/httpcall`, `action/transform`, `definition/model/validate/expr`) | ADR `:458`, `:846-849`, plan `:661` | `grep -rn '"github.com/expr-lang/expr' --include='*.go' . \| grep -v _test.go` → exactly those 4 files | ✅ **CONFIRMED** |
| routes = **26** = **9** non-admin + **15** admin + **2** health; **no definition-deploy route** | ADR `:235-238`, plan `:663` | AST-ish scan of `stdlib/groups.go` resolving every `handle(r, inst, cfg, http.Method…, "…")` to its enclosing `Customize`: Instance **5** + Message **1** + Task **3** = **9**; Admin **15**; Health **2**; total **26**. No `POST /definitions` | ✅ **CONFIRMED exactly, group by group** |
| `SECURITY:` caveat at exactly **3** non-test sites, all admin: `stdlib/groups.go:189`, `gin:204`, `fiber:209` | ADR `:158-160`, plan `:625-626` | `grep -rn "SECURITY:" --include='*.go' . \| grep -v _test` | ✅ **CONFIRMED — all three line numbers exact** |
| `ErrBadInput` wrap sites embedding a caller value = **2** (`admin_endpoints.go:30`, `dto.go:174`) | ADR `:638-640`, evidence §4.7, plan `:654` | `grep -rn "ErrBadInput" ... \| grep "%q\|%s\|%v"` across `httpcore/` | ✅ **count CONFIRMED** (⚠ `dto.go:174` anchor off by one — **C10b**) |
| the **8** exported endpoint functions in `endpoints.go` (6 mapper-taking + `GetInstanceSnapshot` + `GetActionableView`) | ADR `:809-813` | read all signatures | ✅ **the 8 exist as described** — ⚠ but the *set that needs the parameter* is 11 (**C8**) |
| `wrkflw_journal` is **6** columns, no hash / prev-hash / signature | ADR `:219` | DDL read in all three dialects: `instance_id, seq, kind, trigger, occurred_at, applied_at` | ✅ **CONFIRMED** |
| `grep -rniE "encrypt\|redact"` over `persistence/`, `internal/persistence/`, `engine/` non-test exits 1 | ADR `:218` | re-run with **bare** pipe | ✅ **CONFIRMED (EXIT=1)** |
| `grep -rnE "CheckRedirect\|expreval" action/httpcall/` exits 1 | ADR `:100` | re-run with **bare** pipe | ✅ **CONFIRMED (EXIT=1)** |
| `grep -rnE "MaxBytesReader\|BodyLimit" transport/` exits 1 (**ADR's** version) | ADR `:53` | re-run — the ADR uses a **bare** pipe and is correct | ✅ **CONFIRMED** (⚠ the **plan's** copy re-introduces `\|` — **C9**) |
| 4 caller-supplied **variable-map** fields at `service/request.go:19,30,44,72` | evidence §4.6, plan `:660` | enumerated **all 10** `*Request` types in the package | ✅ **the 4 variable maps are correct** — ⚠ but "and only four" is false (**C7**) |
| `expr@v1.17.8/expr.go:221` — *"If MaxNodes is set to 0, the node budget check is disabled"* | ADR `:77-79` | read the module cache | ✅ **CONFIRMED — exact line, verbatim quote.** The `MaxNodes` inversion is real |
| `fiber/v3@v3.4.0/app.go:585` `DefaultBodyLimit = 4 MiB`; applied in `New()` at `:710` | ADR `:70-71`, `:310-316` | read the module cache | ✅ **CONFIRMED — both exact** |
| `fiber/v3@v3.4.0/req.go:146` — `Body()` *"will decompress the body"*; `:92-96` — `BodyRaw()` | ADR `:249-256` | read the module cache | ✅ **CONFIRMED — both exact.** The `BodyRaw()` mechanism rests on real vendor doc |
| `expreval.go:74-100` — the 5 s timeout **abandons** the goroutine (select returns on the timer; `expr.Run` keeps running) | ADR `:431-435` | read the function | ✅ **CONFIRMED — exact range** |
| `engine/conditions.go:29-43` locks the deterministic-replay/ADR-0003 trade; `:43` is `expreval.New(expreval.WithTimeout(0))` | ADR `:89-90`, spec `:94` | read the range | ✅ **CONFIRMED — exact** |
| `authz/authz.go:23` is a package global; `internal/authz/casbin/authorizer.go:30` is hard-coded in a constructor; **neither ABAC evaluator has an options seam** | ADR `:91-94`, spec `:89` | read both | ✅ **CONFIRMED — both exact.** D2's decisive input holds |
| `runtime/processdriver_options.go:196-197`, `:215-216` state last-writer-wins **verbatim in both godocs** | ADR `:441-444`, spec `:110` | read both | ✅ **CONFIRMED — the draft's "silent" was indeed wrong** |
| `httpcall.go:94` `ErrBodyTooLarge` exists / `:125-134` `WithURLExpr` / `:128-130` `urlExprErr` / `:153` `WithHTTPClient` / `:239-242` rejects non-string | ADR `:98-111`, `:505-515`, evidence §4.5 | read each | ✅ **CONFIRMED — all five exact** |
| `view.go:31` aliases; `engine/step_state.go:325` `copyVars = maps.Clone`; `gate.go:45` is `%w: %s`; `expreval.go:135` `%q`s the code; `expr.go:64,68` `%q` `v.source[i]`; `caching_instance_store.go:72` claims *"deep-copies"* | ADR passim | read each | ✅ **CONFIRMED — all six exact** |
| `service/instance.go:117-144` `instanceJSON`; `variables` at `:125`, assigned `:344` | ADR `:143-145` | read the ranges | ✅ **CONFIRMED — all three exact** |
| "**5** disclosure-bearing snapshot fields, not 1" | ADR `:143-148`, plan `:657` | read all 13 `instanceJSON` fields. `history` (`nodeVisitJSON`: node/token ids + timestamps + close kind) and `compensating` (`compensatingJSON`: command id, since, scope) carry **no** process data — the judgement holds | ✅ **CONFIRMED** |
| ADR banner: "**Four** enumerations were wrong" (decode sites, read paths, plaintext columns, banner sentinels) | ADR `:24-26` | counted the banner's own list | ✅ self-consistent (⚠ three of the four are *still* wrong — C1, C2, C6) |
| spec §5 total = **23** rows | spec `:159-162` (15+8) | `awk` row count | ✅ **23 rows** — ⚠ but the 15/8 split is 13/10 (**C13**), and plan §0 says 21 |

---

# Ranked summary

| # | severity | one-line | class |
|---|---|---|---|
| **C1** | **CRITICAL** | D6's plaintext enumeration is **15 columns across 8 tables**, not "12 across 7" — stated wrongly in **nine** places; "12" counts the author's own markdown **rows**, three of which brace-collapse multiple columns | arithmetic (first in 3 rounds) |
| **C2** | **CRITICAL** | The same enumeration names the actor **remainder** (`claim_actor`/`completion_actor` = roles+attributes) and omits the actor **identifier** (`claimed_by`/`completed_by`) plus `outcome`. Corrected total **18 columns / 8 tables** | **NET** |
| **C5** | **CRITICAL** | The 400 allow-list admits `ErrBadCursor`/`ErrBadArmedTimerCursor` as *"value-free by construction"*. **Executed:** both reflect caller strings verbatim via a `"%w: %w"` wrap site. Evidence covers 2 of 7 wrap sites and **both quoted messages are from the same sentinel** | **NET** + inherited-by-analogy |
| **C12** | **CRITICAL** | *"`mergeVars` from **three** non-request sources"* — there are **8** sites, and one of the three named (`:936`) is **admission site #4 of D2's own closed set**. Two prescribed phase-1 tests would assert opposite outcomes on it. `:1208` is mislabelled ("message mirror" = actually child-instance output) | **NET** (the ADR-0175 shape) |
| **C14** | **CRITICAL** | **Seven** stale phase pointers in the spec/ADR after two phases were deleted; **three name phases 8 and 9, which do not exist** — including D6's entire deliverable instruction | **ANCHOR**, systemic |
| **C8** | **CRITICAL** | Covered set is **11** paths; the source-break list threads the policy into **8** functions. The 3 direct-`NewInstanceView` admin endpoints have no channel to receive it | stale sub-count after a correction |
| **C6** | **CRITICAL** | How many of the 8 sentinels keep `err.Error()` is **4 / 5 / 6 / 6** in four places; the plan's six **omits `ErrBadInput`** (the whole point of D5) and counts `ErrInvalidInput` (which does not keep it) | recap over-generalisation |
| **C3** | **CRITICAL** | The machine-checked invariant prescribed to stop the read-path count rotting a **third** time is keyed on `mapInstance`/`NewInstanceView` — blind to `GetInstanceSnapshot` (`return pi`) and `GetActionableView` (`view.NewActionableView`), the two paths the *last* rot added | **NET**, recursive |
| **C7** | **MAJOR**\* | *"Four, and only four"* is false — 4 more `service` request types carry a caller-supplied `map[string]any` via `authz.Actor.Attributes`, which reaches the ABAC env (`{"actor","vars"}`) **unbounded**. \*Critical on the library axis; unreachable over today's HTTP DTO | **NET** (struct-typed field) |
| **C13** | **MAJOR** | Spec §5 claims *"all 15 D×D pairs plus 8 cross-cutting"*; it is **13 + 10**. Missing **D1×D4** and **D4×D6** — and D4×D6 is the same compose-into-a-false-guarantee hazard the table catches for D3×D4 | completeness claim over an undrawn grid |
| **C9** | **MAJOR** | Plan §4 row 4 uses `\|` under `-E` — the exact defect its own preamble warns about 4 lines earlier — and its forward-looking *"must return 26"* is wrong (`BodyLimit` matches the fiber WARN the same plan mandates) | self-reintroduced defect |
| **C11** | **MAJOR** | The evidence file's §4.1/§4.3/§4.7 "pasted output" is **reconstructed**, not captured. Conclusions all hold; the credibility contract does not | evidence hygiene |
| **C4** | **MAJOR** | ADR assigns the read-path count invariant to **phase 4**, plan to **phase 3**; phase 4 is a different package | **ANCHOR** (subsumed by C14's class) |
| **C10** | **MINOR ×2** | `doc.go:66` is the **repo-root** file, not `httpcore/doc.go` (which does not exist); `dto.go:174` does not contain the `got %q` it is cited for (that is `:173`) | **ANCHOR** |

**Totals: 14 findings — 8 Critical, 4 Major, 2 Minor.**

## Ranking by damage if acted on

1. **C12** — the only finding that **stops a phase dead**: phase-1 tests 1 and 6 assert opposite outcomes on `CompleteTaskRequest.Output`, and the plan gives test 6 the escalation clause, so the likely resolution is deleting the bound on the one admission field a *task claimant* controls.
2. **C1 + C2 together** — D6's deliverable **is** the enumeration, and it ships wrong by 3 columns and 1 table (C1) plus 3 more columns including the actor identity (C2). ADR D6's own words: *"an incomplete list presented as exhaustive … converts a consumer's own audit into a false negative."* A consumer encrypts `claim_actor` and leaves every claimant's identity in the clear, **indexed**.
3. **C5** — ships a security control that leaks: the 400 allow-list reflects caller-controlled strings into `ErrorBody.Message` **and**, via D5's widened logging, onto `slog`. Pinned as intended behaviour by phase 3 test 3. This is the ADR-0165 inverted-predicate shape.
4. **C8** — phase 3 threads 8 signatures, phase 3 test 8 then fails on 3 admin rows; the exits are an unlisted breaking change discovered mid-phase, or dropping redaction from the 3 admin endpoints (the exact defect D4 was corrected to close).
5. **C14** — D6's implementation instruction is addressed to a nonexistent phase, and the D3×D4 / D3×D6 `SECURITY.md` mitigations to another; the correlation-id tests are sent to `action/httpcall`, repeating a misplacement the revision already fixed once.
6. **C6** — phase 3's pin test protects **four** sentinels by name; `ErrBadInput` and `ErrOutcomeRequired` go unprotected, which is audit finding F4 shipping through the test built to prevent it.
7. **C3** — the enumeration rots a **third** time with a green invariant asserting it has not, and plan §4 explicitly tells readers to trust that invariant over the prose.
8. **C7** — the ADR ships promising *"both ABAC evaluators inherit it"* over an env whose `actor` key is unbounded; phase 1's *"nowhere else"* forbids fixing it.
9. **C9** — the one mechanical post-phase-4 check on whether body caps landed returns 0 for any repository.
10. **C13** — `SECURITY.md` implies redaction reduces what is stored; it does not.
11. **C11**, **C4**, **C10** — cost is auditor/implementer budget and traceability, not shipped behaviour.

## What I could NOT check (labelled, not implied)

- `ASSUMPTION (unverified)`: I did not attack the **jsonschema `keywordLocation` value-freedom** claim for a fifth schema shape (`patternProperties`, `$ref`, `$dynamicRef`, `unevaluatedProperties`) — plan §0 item 4. Out of lens and out of budget; it is an execution-lens target.
- `ASSUMPTION (unverified)`: I did not re-measure the **O(n²) ladder** or the 45 540-element figure. Spec §7 lists them as discharged by two lenses; I took that at face value and flag it as an inherited number I did **not** re-derive.
- `ASSUMPTION (unverified)`: I did not verify the **IP deny-list property** (*"not global unicast"*) against a reachable globally-unicast internal address — plan §0 item 5, an execution-lens target.
- I checked `fiber.BodyRaw()` only to its `getBody()` call, not through it. The vendor **doc comments** for `Body()`/`BodyRaw()` are confirmed exact; the *behavioural* claim (a 63.7 KiB gzip yielding `len == 33`) is the execution lens's, and I did not reproduce it.
