# ADR-0187 at-rest posture — COUNTING lens audit

Worktree: `/private/tmp/wt-0187-counting` detached at `ebafdf0f`
Started 2026-08-22. Findings appended AS THEY ARE FOUND.

## C1 — MAJOR — spec's evidence-range citation says "E1–E13"; the evidence file holds E1–E15, and the spec itself cites E14 and E15

**Verbatim**, `docs/specs/2026-08-22-at-rest-posture.md` line 5:
> **Evidence:** `docs/specs/2026-08-22-adr-0187-measurements.md` (E1–E13, every claim below executed)

Re-derivation:
```
$ grep -n "^## E" docs/specs/2026-08-22-adr-0187-measurements.md   # 15 headings, E1..E15
$ grep -o "E1[0-9]\|E[1-9]\b" docs/specs/2026-08-22-at-rest-posture.md | sort -u
E1 E10 E11 E12 E13 E14 E15 E2 E3 E5 E6 E7 E8 E9
```
The spec cites **E14** (goose directives — the assumption it says was "executed rather than
shipped") and **E15** (the one foreign key, which RESOLVES open question 3) in its own body.
An inherited range that was correct when written (E1–E13) was not re-derived when E14/E15 were
appended. This is precisely the "restated number strips its hedge" failure the bundle is about.

**Corrected value:** E1–E15.
**Fix:** change the header to `(E1–E15, every claim below executed)`. Better: state the closed set
by naming what each covers, or drop the range and say "every claim below is executed and carries
its own E-reference".

⚠ Second-order: **E4 (the 79-column census) is never cited by an E-label anywhere in the spec**,
although the spec's whole "9 tables / 79 columns / 87 rows" backbone rests on it. The spec's per-
table counts (9/6/12/4/3/13/8/7/17) appear in "The classification" section with no E-citation at
all. Fix: cite E4 at the classification heading.

## C2 — CRITICAL (counting) — the spec says "Ten decisions ship together" and reasons about a "D10" that does not exist

**Verbatim**, `docs/specs/2026-08-22-at-rest-posture.md`, "Author's interaction pass":
> Ten decisions ship together. Below is what each does to the others' premises, derived pairwise.

and, in the same section:
> **D8 × D10 (no mechanism ships).** Telling a consumer which columns are safe to encrypt while
> shipping no codec is coherent …

Re-derivation:
```
$ grep -c "^### D" docs/specs/2026-08-22-at-rest-posture.md
9
$ grep -n "^### D" ...   -> D1 D2 D3 D4 D5 D6 D7 D8 D9 only
```
There is **no D10**. "No mechanism ships" is the **Non-goals** section, which is not numbered as a
decision. So the interaction pass — the artefact rule #9's corollary specifically demands — reasons
about a decision label that has no definition, and its own headline count is off by one.

**Why this is Critical for THIS bundle, not pedantry:** the interaction pass is the grid handed to
the audit's interaction lens "as its starting grid". A grid that names D10 sends that lens looking
for a decision that is not there, and the pass claims completeness ("what each does to the others")
over a set it miscounted. It also means the pairwise matrix is being asserted over C(10,2)=45 pairs
while only C(9,2)=36 exist — and only **8** pairs are actually written down.

**Corrected value:** nine decisions (D1–D9).
**Fix:** either (a) promote "no mechanism ships" to a real `### D10 — No mechanism ships` heading in
Decisions (the honest option, since the Non-goals section says "the deferral is the decision"), or
(b) say "Nine decisions" and rename the pair to `D8 × Non-goals`. Also state explicitly that the
pass covers 8 of the 36 pairs, rather than implying it covers all of them.

## C3 — ⚠⚠ CRITICAL — "zero `policy` columns are keyed" is FALSE over the 87-row set. `casbin_rule.ptype` IS INDEXED. The bundle's single most actionable published sentence is wrong, and wrong in exactly the way the ADR was written to prevent.

**Verbatim**, evidence file E9 (the `keyed` lower bound):
> ⭐ **Zero `freeform` and zero `policy` columns are keyed.** All 11 free-form columns and both
> policy locations are index-free in Postgres.

**Verbatim**, spec D8:
> ⭐ **zero `freeform` and zero `policy` columns carry an index**, so all 11 free-form columns and
> both policy locations can be encrypted without breaking a single index;

**Verbatim**, ADR-0187 decision 8:
> Measured on Postgres: **27 of 79** columns are keyed, of which **zero are `freeform` and zero are
> `policy`** — so all 11 free-form columns and both policy locations can be encrypted without
> breaking an index

**Verbatim**, spec "What `SECURITY.md` gains" — i.e. this is *published to consumers*:
> the two sentences that fall out of the classification and are the actionable content:
> **every `freeform` and `policy` column is index-free and can be encrypted without breaking an
> index**, and …

### Re-derivation

```
$ grep -n "CREATE INDEX" internal/authz/casbin/migrations/0001_casbin_rule.sql
12:CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);
```
```
$ sed -n '1,12p' internal/authz/casbin/migrations/0001_casbin_rule.sql
-- +goose Up
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,
    ...
);
CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);
```

The spec's own classification section reads:
> **`casbin_rule`** (8) — scalar: `id` · policy: `ptype`, `v0`, `v1`, `v2`, `v3`, `v4`, `v5`

So `casbin_rule.ptype` is class **`policy`** and carries a dedicated single-column index.
`casbin_rule.id` is class `scalar` and is a `BIGSERIAL PRIMARY KEY` — also keyed.

**The mechanism is the NET.** E9's number is *"27 of 79"* — 79 is the `wrkflw_*` count, so the
measurement's set **excludes `casbin_rule` entirely**, and the exclusion is never stated at the
point where the ⭐ conclusion is drawn. The ⭐ conclusion is then restated in the spec, the ADR, and
the prescribed `SECURITY.md` output over the bundle's **87**-row set — which *does* include
`casbin_rule`. A count over 79 answering a question posed over 87.

### Why this is Critical rather than a nit

1. **It is the delivery's actionable content.** The spec names it as one of "the two sentences …
   that are the actionable content". A consumer who reads "every `policy` column is index-free" and
   applies non-deterministic column encryption to `casbin_rule.ptype` destroys `casbin_rule_ptype_idx`
   — the index over the discriminator column that casbin's Postgres adapter loads policy by. That
   is the exact harm the spec's own opening warns about: *"a consumer who encrypts the columns we
   name and leaves the rest in the clear has been harmed by our documentation."*
2. **It re-commits the very failure ADR-0187 exists to close.** The ADR's Context: *"all three
   schema-introspection helpers the repo already owns filter `table_name LIKE 'wrkflw_%'`, and
   `casbin_rule` does not match (E2). Any design reusing that machinery inherits the same blind
   spot."* E9's `keyed` derivation, run over 79 columns, **is** that machinery's blind spot — this
   time in a hand-written probe rather than in a helper.
3. **It also falsifies the "both policy locations" phrase**, used verbatim in all three documents.
   D5's whole point is that policy is durable in TWO places; the ⭐ claim then asserts a property of
   both while having measured only one.

### Corrected values

- Over the 87 classified columns on a Postgres deployment, `policy` columns keyed = **1**
  (`casbin_rule.ptype`), not 0.
- `freeform` keyed = **0** — that half survives, over all 87 (`casbin_rule` has no `freeform` column).
- Postgres keyed total over 87 = **29**, not 27: the 27 `wrkflw_*` columns plus `casbin_rule.id`
  (PK) and `casbin_rule.ptype` (index).

### Fix

1. Replace the ⭐ sentence in E9, spec D8, ADR decision 8 and the `SECURITY.md` output text with the
   accurate form: *"On Postgres, all 11 `freeform` columns and `wrkflw_human_task.eligibility` are
   index-free; `casbin_rule.ptype` is indexed by `casbin_rule_ptype_idx`, so encrypting it
   non-deterministically costs casbin's policy load."*
2. Re-run the `keyed` derivation over **87**, and state the set the number is over every time it is
   quoted ("29 of 87 on Postgres", never a bare 27).
3. Add a prescribed guard assertion that the `keyed` derivation input set is the SAME set as the
   classification set — i.e. a test that fails if any classified `table.column` was not offered to
   the `keyed` deriver. Without it the two sets can silently diverge again.
4. Add `casbin_rule.ptype` to the plan's expected-keyed fixtures.

## C4 — ⚠⚠ CRITICAL — "79 columns, identical across all three dialects" is FALSE as a SET claim. MySQL's journal payload column is `trigger_`, not `trigger`, and the bundle never mentions it once.

**Verbatim**, evidence E4 heading:
> ## E4 — Column census: 79 `wrkflw_*` columns, identical across all three dialects

**Verbatim**, E6:
> 87 classification rows, checked against the 87 machine-dumped `table.column` keys with `comm`:
> `unclassified (in schema, not classified): <empty>` / `stale (classified, not in schema): <empty>`
> `EXACT MATCH: yes`

**Verbatim**, spec D2:
> Each `table.column` gets exactly one class … The same class holds on Postgres, MySQL and SQLite.

**Verbatim**, spec classification section:
> **`wrkflw_journal`** (6) — reference: `instance_id` · scalar: `seq`, `kind` · freeform: `trigger` …

### Re-derivation

```
$ grep -n "trigger" internal/persistence/store/migrations/mysql/0001_init.sql | head -3
10:-- payload column is named "trigger_" because "trigger" is a reserved word
31:    trigger_    JSON         NOT NULL,
$ grep -n "trigger " internal/persistence/store/migrations/postgres/0001_init.sql
30:    trigger     JSONB       NOT NULL,
$ grep -n "trigger " internal/persistence/store/migrations/sqlite/0001_init.sql
40:    trigger     TEXT    NOT NULL,
```
```
$ grep -rn "JournalTriggerColumn" internal/persistence/dialect/*.go
mysql.go:67:    func (mysql) JournalTriggerColumn() string { return "trigger_" }
postgres.go:74: func (postgres) JournalTriggerColumn() string { return "trigger" }
sqlite.go:70:   func (sqliteDialect) JournalTriggerColumn() string { return "trigger" }
```
```
$ grep -rn "trigger_\b" <all four bundle files>
EXIT=1        <- the bundle does not mention trigger_ anywhere
```

The count 79 is right in all three dialects. **The SET is not identical** — this is the brief's
"anchor" failure exactly: `identical count` restated as `identical set`.

### What breaks in the shipped design

- **D6 guard rule 1 (an unclassified schema column fails)** fires on MySQL for
  `wrkflw_journal.trigger_`, which no classification row names.
- **D6 guard rule 2 (a classification entry naming an absent column fails, self-cleaning)** fires
  for `wrkflw_journal.trigger` if the schema set is taken per dialect from MySQL.
- **D6 guard rule 4 (no column's class differs across dialects)** does NOT catch it — this is an
  *absence*, not a *divergence*, so the pin the spec calls "the D2 invariance pin" is blind to the
  one real cross-dialect asymmetry the schema actually has.
- **D7 cross-check against `introspectMySQL`** returns `trigger_` and disagrees with a parse that
  emitted `trigger`, or agrees with a parse that emitted `trigger_` and then disagrees with the
  classification. Either way one of the five guard rules is RED on day one.
- **E6's `comm` "EXACT MATCH: yes"** therefore cannot have been run against the MySQL schema. It was
  run against one dialect (or against a union that silently normalised), and the result was
  restated as a property of all three. ⚠ **This is a measurement that is false as labelled** — the
  same defect class E13 correctly identifies in the inherited `grep … | grep -v _test` premise, one
  section earlier in the same file.

### The repo already solved this, and the bundle missed the convention

`internal/persistence/store/migration_parity_test.go` — the very file D7 says the cross-check
reuses — carries `normalizeMySQLTriggerColumn`, which renames `trigger_` → `trigger` *before* its
equality assertion, sourcing both names from `dialect.NewMySQL()/NewPostgres().JournalTriggerColumn()`
"so this function stays in sync with the migration automatically". The new guard must apply the
same normalization, and the bundle prescribes nothing of the sort. Per the ADR-0186 lesson recorded
in memory (*search the repo for an existing convention BEFORE writing a new symbol*), this is the
fifth instance of that pattern.

### Corrected value

79 columns per dialect, but **80 distinct `table.column` keys across the three dialects**
(`wrkflw_journal.trigger` ∪ `wrkflw_journal.trigger_`) unless normalised. Grand total 88 raw / 87
after normalisation.

### Fix

1. Correct E4's heading and E6's claim: say "79 columns per dialect; the column NAME SET is
   identical after the one documented reserved-word normalisation `trigger_` → `trigger`".
2. Add an explicit decision (or a clause in D3) that the classification key is the **canonical**
   `table.column`, and that the parser normalises MySQL's `trigger_` via
   `dialect.NewMySQL().JournalTriggerColumn()` — reusing, not re-implementing, the existing helper.
3. Add the normalisation as a prescribed parser test case alongside E11's two traps and E15's third
   ("parser trap 4").
4. Re-run E6's `comm` **per dialect**, three times, and paste all three outputs — the current single
   output cannot distinguish "all three match" from "one matched".
5. ⚠ Guard rule 4 must be widened from "class differs across dialects" to "the canonical column key
   set differs across dialects", otherwise a future second reserved-word rename is invisible again.

### C3 addendum — the plan PRESCRIBES the filter that immunizes the false claim

`docs/plans/2026-08-22-at-rest-posture.md`, Task 5 step 1, verbatim:
```go
	for k, col := range schemas["postgres"].Columns {
		if k.Table == "casbin_rule" {
			continue // not part of the 79-column wrkflw_* census (E4)
		}
		...
	}
	assert.Equal(t, 27, keyed, "27 of the 79 wrkflw_* columns are keyed on postgres (E9)")
	assert.Equal(t, map[atrest.Class]int{
		atrest.ClassReference: 15, atrest.ClassScalar: 7,
		atrest.ClassTimestamp: 4,  atrest.ClassActor: 1,
	}, byClass, "zero freeform and zero policy columns are keyed — the actionable finding (E9)")
```
and the plan's own gloss two paragraphs later:
> ⚠ **The `assert.Equal` on `byClass` has exactly four entries and no `freeform`/`policy` keys.**
> That is the "zero freeform, zero policy" claim expressed as an equality rather than as prose, so
> it **cannot rot**: `assert.Equal` on the whole map fails if a fifth class appears.

It **cannot rot and it also cannot be right**: `casbin_rule` is `continue`d out of the loop three
lines above, so the one keyed `policy` column in the schema is removed from the set before the
equality is taken. The plan's claim that this assertion makes the finding rot-proof is therefore
false — the assertion is a machine-checked restatement of a filter, not of the schema. **No task in
the plan asserts anything about `casbin_rule`'s `Keys` at all**, and Task 7's casbin cross-check
compares column NAMES only, never key membership.

**Additional fix for C3:** Task 5 must gain a second assertion over the **unfiltered** 87 —
`keyed == 29` on Postgres with `byClass` including `ClassPolicy: 1` — and the prose in step 1's
comment must stop calling the filtered result "the actionable finding".

## C5 — ⚠⚠ CRITICAL — the glob stated in ADR decision 1 and spec D1 finds ONE of the four migration files, and the one it finds is `casbin_rule` — the inverse of the omission it exists to close

**Verbatim**, ADR-0187 decision 1:
> **1. Migrations are discovered by glob (`**/migrations/*.sql`), never listed.**

**Verbatim**, spec D1:
> The schema walk globs `**/migrations/*.sql` from the module root. It does not hardcode a directory
> list.

**Verbatim**, plan "File structure" table:
> | `internal/atrest/discover.go` | glob `**/migrations/*.sql`; the migration-set **declaration** … |

### Re-derivation — executed three ways

```
$ zsh -c 'cd /private/tmp/wt-0187-counting; setopt nullglob; a=( **/migrations/*.sql ); print -r "MATCHES=${#a[@]}"; print -l -- $a'
MATCHES=1
internal/authz/casbin/migrations/0001_casbin_rule.sql
```
```
$ python3 -c "import glob; m=sorted(glob.glob('**/migrations/*.sql',recursive=True)); print(len(m)); [print(' ',x) for x in m]"
1
  internal/authz/casbin/migrations/0001_casbin_rule.sql
```
```
$ go run  # filepath.Glob("**/migrations/*.sql")
**/migrations/*.sql            n=0 err=<nil> []
```
(Go's `path/filepath` has **no `**` support at all** — `**` is two `*`s, neither of which crosses a
separator — so the literal pattern in the ADR would match nothing in Go, the language this ships in.)

Correct pattern:
```
$ python3 -c "import glob; m=sorted(glob.glob('**/migrations/**/*.sql',recursive=True)); print(len(m))"
4
```

### Why this is Critical

The three `wrkflw_*` migrations live at `…/migrations/<dialect>/0001_init.sql` — **one level below**
`migrations/` — while the casbin one lives at `…/migrations/0001_casbin_rule.sql`, directly inside
it. `**/migrations/*.sql` requires the file's immediate parent to be named `migrations`, so it finds
only the casbin file. A reader implementing ADR decision 1 literally would discover `casbin_rule`
and lose all 79 `wrkflw_*` columns — a mirror-image of the F5 failure the decision exists to close,
in the sentence that closes it.

The **plan** silently corrects this in prose without flagging it:
> `DiscoverMigrationDirs` walks `root` and returns every directory named `migrations` **or whose
> parent is named `migrations`**, containing at least one `.sql` file …

so the plan is right and both the ADR and the spec are wrong — an ADR that promises behaviour
nobody would build, which rule #11 names explicitly.

**Corrected value:** the pattern must be `**/migrations/**/*.sql` (or, as the plan describes,
a `filepath.WalkDir` for directories named `migrations` or whose parent is), matching **4** files.

### Fix

1. Change ADR decision 1 and spec D1 to state the actual rule the plan implements — a WalkDir for
   directories named `migrations` or whose parent is named `migrations` — and stop quoting a glob
   literal that no Go standard-library function accepts.
2. If a glob literal is kept for readability, use `**/migrations/**/*.sql` and add the parenthetical
   "(Go's `filepath.Glob` has no `**`; the implementation is a `WalkDir`)".
3. Add to the plan's Task 2 a test case pinning that **both** directory shapes are found — one
   nested under a dialect subdirectory and one directly inside `migrations` — since that asymmetry
   is the whole reason the naive pattern fails.

## C6 — MAJOR — the 48 / 48 / 67 "TEXT-ish" counts silently EXCLUDE `casbin_rule`. The NET is never stated, in a bundle whose headline set is 87.

**Verbatim**, E5:
> ```
> $ # count of columns whose declared type matches TEXT|VARCHAR|JSONB?
> postgres TEXT-ish columns: 48
> mysql    TEXT-ish columns: 48
> sqlite   TEXT-ish columns: 67
> ```

Neither E5 nor ADR decision 2 nor spec D2 says what file set that count is over.

### Re-derivation (independent Python DDL parser, depth-aware body split, clause skipping)
```
postgres  columns= 79  indexes=11  TEXT-ish (wrkflw only) = 48
          types={'TEXT':36,'JSONB':12,'TIMESTAMPTZ':19,'INT':6,'SMALLINT':3,'BIGINT':2,'BIGSERIAL':1}
mysql     columns= 79  indexes= 8  TEXT-ish (wrkflw only) = 48
          types={'VARCHAR(255)':30,'VARCHAR(50)':2,'VARCHAR(64)':1,'TEXT':3,'JSON':12,'DATETIME(6)':19,'INT':6,'SMALLINT':3,'BIGINT':3}
sqlite    columns= 79  indexes=11  TEXT-ish (wrkflw only) = 67   types={'TEXT':67,'INTEGER':12}
casbin    columns=  8  indexes= 1  TEXT-ish = 7 of 8
```
Every number E5 asserts reproduces **exactly** — 48/48/67, the Postgres histogram 36+12+19+6+3+2+1=79,
and the 19-timestamp delta. **The arithmetic is right; the NET is unstated.**

- Over a real Postgres deployment (the 87 columns this bundle classifies), TEXT-ish is **55**
  (48 + `casbin_rule`'s 7 `TEXT` columns), not 48.
- The 48-vs-67 comparison is only meaningful over the 79 `wrkflw_*` columns, because `casbin_rule`
  has no SQLite counterpart. That is the *right* scope — it is simply never declared.
- ⚠ On the MySQL regex question the brief raised: the author's `TEXT|VARCHAR|JSONB?` **does** match
  `VARCHAR(255)` / `VARCHAR(50)` / `VARCHAR(64)` and `JSON`, and does **not** match `DATETIME(6)`.
  Independently reproduced at 48. No defect there.

**Fix:** state the set on every occurrence — "48 of the 79 `wrkflw_*` columns on Postgres; 67 on
SQLite; `casbin_rule`'s 8 are Postgres-only and excluded from this comparison because SQLite has no
counterpart." Add the exclusion to ADR decision 2 as well, which restates 48/67 with no scope.

## C7 — MAJOR — "free-form" names three different sets in this bundle, and two of them appear one section apart

- **48** — E5, ADR context, ADR decision 2: *"48 free-form columns"* = columns whose PHYSICAL type is
  TEXT/VARCHAR/JSON(B).
- **11** — D4's `freeform` class = *"arbitrary process data — assume it carries secrets"*.
- **19** — E5 again: *"reports 19 engine-written instants as 'free-form' on SQLite"*.

E9 puts two of them in one sentence:
> ⭐ **Zero `freeform` and zero `policy` columns are keyed.** All 11 free-form columns …

while E5's own heading four sections earlier reads *"48 free-form columns"*. A reader who carries
"free-form = 48" into E9 reads "all 11 free-form columns" as a contradiction; a reader who carries
"freeform = 11" into E5's heading reads the whole justification for D2 as nonsense.

This is the **anchor** failure the brief names: the label stays fixed while the set underneath it
changes. It matters here because the bundle's entire thesis is that the enumeration rotted by being
restated — and the restatements are being made in a vocabulary that silently re-points.

**Fix:** reserve the word `freeform` (one word, code font) for the class, and rename the physical-type
set throughout to **"TEXT-ish"** or **"string-typed"** — including inside the quoted historical
"48 free-form columns", which should be written as *"the historical '48 free-form columns' figure,
which counted string-TYPED columns, not the `freeform` class"*.

## C8 — MAJOR — ADR-0187's Context calls `casbin_rule`'s policy columns "free-form", contradicting its own decision 4

**Verbatim**, ADR-0187 Context:
> `internal/authz/casbin/migrations/0001_casbin_rule.sql` creates a tenth table with **seven
> free-form `TEXT` columns** holding the deployment's casbin policy

**Verbatim**, ADR-0187 decision 4 and the spec's classification:
> **`casbin_rule`** (8) — scalar: `id` · policy: `ptype`, `v0`, `v1`, `v2`, `v3`, `v4`, `v5`

Re-derivation:
```
$ cat internal/authz/casbin/migrations/0001_casbin_rule.sql
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,
    v0..v5 TEXT NOT NULL DEFAULT '',
```
Seven `TEXT` columns is right as a *type* count. Calling them "free-form" in a document that
defines `freeform` as one of six mutually exclusive classes, and assigns all seven to `policy`, is
an internal contradiction — and the same word-overload as C7, now inside a single file.

**Fix:** ADR Context → "a tenth table with seven `TEXT` columns (classed `policy` by decision 4)
holding the deployment's casbin policy".

## C9 — MINOR — ADR-0187 cites **E4** (the column census) for a claim E4 does not contain

**Verbatim**, ADR-0187 Context:
> `wrkflw_journal` carries no hash, prev-hash or signature column (**E4**).

E4 is a table of per-table column **counts** only; it lists no column names. The claim's actual
evidence is **E13**, whose closing paragraph reads: *"`wrkflw_journal` is 6 columns (E4):
`instance_id`, `seq`, `kind`, `trigger`, `occurred_at`, `applied_at`. No hash column, no prev-hash
column, no signature column."*

**Fix:** cite **E13** (or E13+E4).

## C10 — MINOR — E14's `uniq -c` output cannot distinguish "one `Up` per file" from "several `Up`s at the same line number"

**Verbatim**, E14:
```
$ grep -rhn "^-- +goose" ... | sort | uniq -c
   4 1:-- +goose Up
   1 14:-- +goose Down
   ...
```
The `-h` suppresses filenames while `-n` keeps line numbers, so identical lines dedupe **by line
number**. The conclusion drawn — *"Four files, each with exactly one `-- +goose Up` at line 1 and
one `-- +goose Down`"* — is not derivable from that output: a file with two `Up`s on different lines
would appear as two rows of count 1 and read the same.

Re-derived properly:
```
$ grep -rn "^-- +goose" internal/persistence/store/migrations/*/*.sql internal/authz/casbin/migrations/*.sql
mysql/0001_init.sql:1:-- +goose Up      mysql/0001_init.sql:150:-- +goose Down
postgres/0001_init.sql:1:-- +goose Up   postgres/0001_init.sql:162:-- +goose Down
casbin/0001_casbin_rule.sql:1:-- +goose Up   casbin/0001_casbin_rule.sql:14:-- +goose Down
sqlite/0001_init.sql:1:-- +goose Up    sqlite/0001_init.sql:157:-- +goose Down
$ grep -rh "^-- +goose" ... | sort | uniq -c
   4 -- +goose Down
   4 -- +goose Up
$ grep -rn "StatementBegin\|StatementEnd" .   # only doc-file hits, no .sql hits
```
**The conclusion is TRUE** — 4 `Up` at line 1, 4 `Down`, no `StatementBegin`/`StatementEnd` in any
`.sql`. Only the pasted command fails to demonstrate it.

**Fix:** replace E14's command with the `-rn` + filename form above, so the next reader can audit it.

## C11 — MAJOR — `keyed` is declared per-dialect but only Postgres is ever counted; MySQL and SQLite are **28**, and no document or test says so

**Verbatim**, spec D8 / ADR decision 8:
> Measured on Postgres (**E9**): **27 of 79** columns are keyed
> ⚠ **`keyed` is dialect-DEPENDENT, unlike `class`** (**E10**)

E10 gives per-dialect **index counts** (postgres 11 / mysql 8+3 / sqlite 11) but the bundle never
states the per-dialect **keyed column** counts, which is the number `SECURITY.md` will actually
publish per dialect. The plan's Task 5 asserts `27` for Postgres and asserts nothing for MySQL or
SQLite; its only per-dialect assertion is one `NotContains` on `wrkflw_outbox.status`.

### Re-derivation (independent parser; PK membership + inline/table-level UNIQUE + `CREATE INDEX` key columns + partial-index `WHERE` predicate columns)

```
--- postgres: wrkflw_* keyed = 27 of 79   by class: reference 15, scalar 7, timestamp 4, actor 1
--- mysql:    wrkflw_* keyed = 28 of 79   by class: reference 15, scalar 7, timestamp 5, actor 1
--- sqlite:   wrkflw_* keyed = 28 of 79   by class: reference 15, scalar 7, timestamp 5, actor 1
--- casbin_rule: keyed = 2 of 8 -> casbin_rule.id (PK, scalar), casbin_rule.ptype (index, POLICY)
POSTGRES DEPLOYMENT (87 cols incl casbin): keyed = 29 of 87
    by class: reference 15, scalar 8, timestamp 4, actor 1, policy 1
```
(⚠ my first parser run reported 26 for Postgres; the shortfall was **my** predicate regex failing on
`WHERE status = 'pending'` while matching `IS NULL` / `IN (…)`. Corrected, Postgres is **27** and
E9's 27 / 15 / 7 / 4 / 1 breakdown reproduces **exactly**, as does the identification of
`wrkflw_human_task.claimed_by` as the single keyed `actor` column, on all three dialects.)

The divergence: `wrkflw_instances.ended_at` is keyed on Postgres only (it is the `WHERE ended_at IS
NULL` predicate of `wrkflw_instances_status_idx`), while `wrkflw_call_links.notified_at`,
`wrkflw_call_links.claimed_at` and `wrkflw_outbox.status` are keyed on MySQL/SQLite as ordinary key
columns of the composite indexes that replace Postgres's partial ones.

**Fix:**
1. Record 27 (Postgres, `wrkflw_*`) / 28 (MySQL) / 28 (SQLite) / 29 (Postgres deployment incl.
   `casbin_rule`) in E9, and never quote a bare "27".
2. Add MySQL and SQLite `byClass` assertions to plan Task 5, mirroring the Postgres one — otherwise
   the per-dialect derivation D8 exists for is machine-checked in one dialect out of three.
3. Add the three-column divergence above as a named fixture, so a future index change that
   accidentally re-converges the dialects is visible rather than silent.

## C12 — MINOR — "27 of 79" is quoted in three documents; only E9's heading carries the dialect, and none carries the exclusion

Occurrences:
- `docs/specs/2026-08-22-adr-0187-measurements.md` E9 heading: *"The `keyed` lower bound: 27 of 79
  Postgres columns"* — dialect stated, `casbin_rule` exclusion not stated.
- `docs/specs/2026-08-22-at-rest-posture.md` D8: *"Measured on Postgres (**E9**): **27 of 79**"* —
  dialect stated.
- `docs/adr/0187-at-rest-classification-is-machine-checked.md` decision 8: *"Measured on Postgres:
  **27 of 79** columns are keyed"* — dialect stated.
- `docs/plans/2026-08-22-at-rest-posture.md` Task 5: `assert.Equal(t, 27, keyed, "27 of the 79
  wrkflw_* columns are keyed on postgres (E9)")` — **the only place the `wrkflw_*` restriction is
  written down**, and it is inside a test comment, not in the ADR or the spec.

The dialect survived every restatement; the **set** did not. Per C3 that is the restatement that
produces the false published sentence. **Fix:** write it as "27 of the 79 `wrkflw_*` columns on
Postgres (29 of 87 with `casbin_rule`)" everywhere the number appears.

## C13 — MAJOR — the plan's self-review table maps a "D10" to a task; the spec has no D10 (see C2)

**Verbatim**, plan "Self-review against the spec":
> | D10 no mechanism ships | — nothing in any task builds one; asserted by the reviewer |

The plan's table has **12 rows** claiming to map "spec decision → task": D1–D10 plus
"non-goals restated in `SECURITY.md`" and "open questions 1 and 2". The spec defines **D1–D9**.
So the miscount in C2 has already propagated into the second document, and the row that would
otherwise be the audit's one hook for "does any task build a mechanism the spec forbids?" hangs off
a label with no definition.

⚠ The second half of the brief's mapping question — *does any task implement something no decision
authorises?* — I checked directly rather than through the table:
- Task 2's `MigrationSets` **declaration** is authorised by no spec decision. D1 says "discovered by
  glob, **never listed**"; the plan then adds a hand-maintained map of the four directories. The
  spec's interaction pass admits this (*"Resolved by the plan's `MigrationSets` declaration … a
  reviewer who reads only ADR-0187 decision 1 will not expect the declaration to exist"*), but
  **the ADR itself was never amended**, so the shipped artifact of record says the opposite of what
  ships. Rule #11 requires the ADR to be amended in-bundle.
- Task 4's `ClassDivergencesPerDialect` (fixture-driven) exists only to make the pin non-vacuous and
  is authorised by the spec's falsifiability section. OK.
- No task builds a codec or a hash chain. D10-as-written (the Non-goals section) holds.

**Fix:** renumber per C2, and add a decision to the ADR — call it D1b or fold it into decision 1 —
that states discovery finds **directories** while a checked-both-ways **declaration** says what each
one is, because dialect cannot be inferred from a path.

## C14 — INFO / VERIFIED — inherited claims that survive independent re-derivation

Recorded so the controller knows these were attacked and held, not skipped.

| claim | source | re-derived | verdict |
|---|---|---|---|
| four migration directories / four `go:embed` sets | E1 | `find . -name '*.sql'` → 4 files in 4 dirs; the only other `CREATE TABLE` text in Go is 16 ephemeral test tables + 1 `examples/` demo table, none durable schema | **HOLDS** |
| **four** `Authorize` call sites, all `runtime/task/service.go` 199/234/255/306, all passing `task.Eligibility` | E8 | `grep -rn "\.Authorize(" --include='*.go' .` **with no filters at all** → 16 hits: 4 production call sites in `runtime/task/service.go`, 1 delegation `casbinauthz.go:163`, 11 in `_test.go`. No production `Authorize` call outside `runtime/task/`. The `grep -v "func "` did not hide an implementation. | **HOLDS** |
| exactly ONE foreign key, declared three ways | E15 | `grep -rn "REFERENCES\|FOREIGN KEY"` over **all four** files (casbin included, which E15's command omitted) → 3 declarations, all `wrkflw_journal.instance_id → wrkflw_instances`; `casbin_rule` has none | **HOLDS** (⚠ E15's own command globbed only `store/migrations/*/*.sql`, so it could not have seen a casbin FK; re-run it over all four) |
| exactly ONE column NAME carries two classes (`claimed_by`) | E7 | grouped the spec's 87 parsed rows by bare column name: `claimed_by` is the only name with >1 distinct class | **HOLDS** |
| `scripts/` holds four shell entry points | E12 | `ls scripts/` → `check-extraction.sh`, `check-test-timeout.sh`, `coverage.sh`, `testdb.sh` = 4 | **HOLDS** |
| no `go:generate` convention beyond mockgen | E12 | `grep -rn go:generate --include='*.go' .` → 4 lines, all `mockgen --typed`; `grep -v mockgen` exits 1 | **HOLDS** |
| three policy sources `FromEnforcer` 59 / `FromStrings` 75 / `FromDB` 92; only `FromDB` writes a table | E3 | `grep -n "^func " casbinauthz/casbinauthz.go` → exact line numbers confirmed | **HOLDS** |
| nine `wrkflw_*` tables; `casbin_rule` is the tenth | E4 / spec D1 | `CREATE TABLE` names over all four files → 9 names × 3 dialects + `casbin_rule` × 1 | **HOLDS** |
| the six per-class counts 27/19/17/11/8/5 = 87 | D4, E6, ADR decision 4, plan Task 3 | mechanically parsed the spec's own § "The classification" prose: 10 tables, every declared per-table count matches its parsed list (9/6/12/4/3/13/8/7/17/8), 87 rows, no duplicate `table.column`, per-class `{reference:27, timestamp:19, scalar:17, freeform:11, policy:8, actor:5}` | **HOLDS as a description of the spec's prose** — see C4 for whether that set equals the schema |
| Postgres type histogram 36 TEXT + 12 JSONB + 19 TIMESTAMPTZ + 6 INT + 3 SMALLINT + 2 BIGINT + 1 BIGSERIAL = 79 | E5 | independent parser reproduces every bucket exactly | **HOLDS** |
| index counts postgres 11 / mysql 8 + 3 inline / sqlite 11; postgres partial = 5 | E10 | independent parser: pg 11, mysql 8 standalone, sqlite 11; `grep -nE "^\s*(KEY\|INDEX\|UNIQUE)"` mysql → 3 inline; pg `WHERE` clauses → 5 | **HOLDS** (⚠ excludes `casbin_rule_ptype_idx`; a Postgres deployment has **12** indexes) |
| E9's Postgres keyed breakdown 15 reference / 7 scalar / 4 timestamp / 1 actor = 27 | E9 | independent parser, after fixing my own predicate-regex bug, reproduces all five numbers | **HOLDS** |
| the enumeration "rotted four times": 2 → "at least six" → 12 → "48 columns" | spec, ADR | `docs/specs/2026-08-21-untrusted-input-deferred-slices.md:335` carries the identical sentence; the restatement is faithful to its source. The pre-lineage values themselves are **not independently re-derivable from this repo** | **INHERITED, faithfully restated — mark `ASSUMPTION (unverified)` for the first three values** |

### C4 addendum — verbatim per-dialect `comm`, which E6 ran once and reported as three

Re-ran E6's exact check **three times, one per dialect**, against the spec's own parsed
classification prose:

```
classification rows: 87
--- postgres(+casbin): schema=87
    unclassified (in schema, not classified): <empty>
    stale (classified, not in schema):        <empty>
--- mysql: schema=79
    unclassified (in schema, not classified): ['wrkflw_journal.trigger_']
    stale (classified, not in schema):        ['wrkflw_journal.trigger']
--- sqlite: schema=79
    unclassified (in schema, not classified): <empty>
    stale (classified, not in schema):        <empty>

UNION of the three dialects (what plan Task 3 actually builds): 88
  union minus classification: ['wrkflw_journal.trigger_']
```

E6's *"EXACT MATCH: yes"* is a **Postgres-only result reported as a schema-wide one**. The plan's
Task 3 builds `inSchema` as the **union** over `schemas` — 88 keys — so:
- `assert.Len(t, atrest.Classification, 87)` **passes**;
- `assert.Empty(t, unclassified)` **fails** with `wrkflw_journal.trigger_`;
- `assert.Empty(t, stale)` **passes** (union semantics hide the stale side).

⇒ Task 3 step 4 ("GREEN") is unreachable as written. This is a design defect that surfaces on the
first real run, not a documentation nit.

⚠ **Knock-on to Task 4's mutation (plan step 5):** its final gate is
`AFTER RESTORE EXIT=0`. That will be **RED before the mutation is even applied**, for the
`trigger_` reason, so the ablation cannot discriminate. The mutation must be re-planned after C4 is
fixed.

## C15 — MAJOR — Task 4's "real mutation" cannot make the dialect-invariance pin fail; it exercises Task 3's guards instead

**Verbatim**, spec Falsifiability section (what makes the pin non-vacuous):
> 2. a **real mutation** against a migration file on disk (`cp` backup, restore, `diff` byte-exact),
>    with the observed failure text recorded in the plan.

**Verbatim**, plan Task 4 step 5 — the mutation and its own stated expectation:
> ```
> sed -i '' 's/^    note         TEXT,/    note_text    TEXT,/' …/sqlite/0001_init.sql
> ```
> Expected: `MUTATION EXIT=1` with **`wrkflw_human_task.note_text` unclassified** and
> **`wrkflw_human_task.note` stale** — i.e. the completeness and staleness guards both fire.

The mutation renames a column in one dialect. That produces an **absent** column, which
`ClassDivergences` — whose contract is "one string per column whose class is not identical across
every dialect that declares it" — cannot report: `note_text` is declared by exactly one dialect, so
there is nothing to compare. `TestNoColumnChangesClassAcrossDialects` stays **GREEN throughout the
mutation**. The plan's own expected output names only the completeness and staleness guards, i.e.
Task 3's assertions.

⇒ the pin the spec calls out as needing mutation verification receives none. Its only non-vacuity
evidence is the in-test fixture (liveness guard), which the spec itself says is not sufficient
alone: *"Steps 3–5 are what make it non-vacuous, and they are **not optional**."*

**Verified applicable:** `grep -c '^    note         TEXT,$' …/sqlite/0001_init.sql` → **1**, so the
`sed` does match — the mutation applies, it just tests something else.

**Fix:** the mutation that actually drives `ClassDivergences` must change a column's **class-relevant
identity in one dialect while keeping the name**, which the current design cannot express (class is
a hand-written Go literal, not derived from the file). Two workable options:
(a) mutate `internal/atrest/classification.go` instead of the migration — plant a per-dialect class
    override and confirm `ClassDivergences` reports exactly it; or
(b) accept that the pin is fixture-verified only, say so explicitly in the spec, and re-label plan
    step 5 as the mutation proving the **completeness/staleness** guards (which is what it is).
Either way, delete the spec's claim that the pin has a real-file mutation behind it.

## C16 — MAJOR — the spec's own Falsifiability recap miscounts its own five-row table: "Three of the five are real RED; the fourth is a pin"

**Verbatim**, spec:
> Per Premise Discipline, a prescribed test must state what makes it fail. **Three of the five are
> real RED; the fourth is a pin and is treated as one.**

Re-derivation — the table immediately below that sentence, all five rows:

| # | assertion | what makes it fail today (verbatim) | kind |
|---|---|---|---|
| 1 | every schema column has a classification | "the classification map starts **empty** — real RED" | real RED |
| 2 | no entry names an absent column | "real RED once the map is deliberately seeded with one bogus row" | real RED (seeded) |
| 3 | `SECURITY.md` matches the generator | "**no such block exists** — real RED" | real RED |
| 4 | the parser agrees with live introspection | "the parser does not exist — **compile-error RED**" | real RED |
| 5 | no column's class differs across dialects | "⚠ **passes the moment it is written**" | the pin |

**FOUR** are real RED, and the pin is the **FIFTH**, not the fourth. Both halves of the recap
sentence are wrong.

This is precisely the failure mode the repo's own `Premise Discipline` section names — *"the false
claims that survive review are almost never in the detailed reasoning — they are the summary
sentence appended to it"* — occurring in a bundle whose entire subject is enumerations rotting, in
the section that invokes Premise Discipline by name.

**Fix:** "Four of the five are real RED — one of them a compile error, one requiring the map to be
seeded with a bogus row. The fifth, the dialect-invariance assertion, is a pin and is treated as
one." Then re-check it against C15, which changes what "treated as one" is worth.

## C17 — MAJOR — the spec names ONE file and ONE test; the plan builds a NEW PACKAGE of eleven files and a differently-named test. Neither the ADR nor the spec was updated.

**Verbatim**, spec D6:
> The invariant lives in `internal/persistence/store/atrest_test.go` (`package store_test`) and fails
> when: …

**Verbatim**, spec D9:
> `scripts/gen-at-rest.sh` runs
> `go test ./internal/persistence/store/ -run **TestAtRest_SecurityMdInSync** -update`

**Verbatim**, plan Architecture + File structure:
> A new module-level **`internal/atrest`** package parses every discovered migration file …

| plan file | spec's stated home |
|---|---|
| `internal/atrest/schema.go` + `schema_test.go` | — |
| `internal/atrest/discover.go` + `discover_test.go` | — |
| `internal/atrest/classification.go` + `classification_test.go` | — |
| `internal/atrest/render.go` + `render_test.go` | — |
| `internal/persistence/store/atrest_crosscheck_test.go` | (only D7's cross-check) |
| `scripts/gen-at-rest.sh` → `go test **./internal/atrest/** -run **TestSecurityMdInSync** -update` | `./internal/persistence/store/ … TestAtRest_SecurityMdInSync` |

Divergences that will bite implementation:
1. **Package**: nine of the plan's ten Go files live in a package the spec never mentions. D6's
   "the invariant lives in `internal/persistence/store/atrest_test.go`" is false for guard rules
   1–4; only rule 5 (the Docker-gated cross-check) lands where the spec says.
2. **Test name**: `TestAtRest_SecurityMdInSync` (spec D9) vs `TestSecurityMdInSync` (plan Task 6 and
   `scripts/gen-at-rest.sh`). ⚠ A `-run` filter naming a test that does not exist **exits 0**
   (CLAUDE.md pitfall #5), so `scripts/gen-at-rest.sh` written against the spec's name would print
   "SECURITY.md at-rest block regenerated and verified." while regenerating **nothing**. This is a
   silent-success path in the generator whose whole purpose is preventing silent drift.
3. **ADR "Neutral"** claims *"it lives in test-adjacent code and adds no production surface"* — but
   `internal/atrest/{schema,discover,classification,render}.go` are ordinary non-test files that
   compile into the module, and the plan sets an **85 % coverage floor** on the package, which is a
   floor for production code. `internal/` keeps it off the *public* surface; "test-adjacent" it is
   not.

**Fix:** amend spec D6 and D9 and the ADR's Neutral bullet to the shipped shape — a new
`internal/atrest` package holding parser, discovery, classification and renderer, with only the
Docker-gated cross-check in `internal/persistence/store` — and settle the test name to ONE spelling
across the spec, the plan and `scripts/gen-at-rest.sh`. Per rule #11, this is exactly an ADR that
promises behaviour the plan changed; it must be amended in-bundle rather than left to implementation.

---

# Summary table

Glosses: **D1**=discover migrations by glob · **D2**=classification is by logical role and
dialect-invariant · **D4**=six classes with their counts · **D6**=the guard's five failure modes ·
**D8**=the machine-derived per-dialect `keyed` annotation · **E4**=the 79/87 column census ·
**E5**=the 48/48/67 TEXT-ish measurement · **E6**=the `comm` completeness check · **E9**=the
Postgres `keyed` derivation · **E14**=the goose-directive survey · **E15**=the single foreign key.

| ID | severity | claimed | actual | verdict |
|---|---|---|---|---|
| **C3** | **CRITICAL** | "zero `policy` columns are keyed" / "both policy locations can be encrypted without breaking an index" — published to consumers | `casbin_rule.ptype` is indexed by `casbin_rule_ptype_idx`; policy-keyed = **1**; Postgres keyed = **29 of 87**, not 27 of 79 | **CONFIRMED FALSE** — the NET (E9 ran over 79, the claim is made over 87). Plan Task 5 `continue`s `casbin_rule` out, immunizing the assertion |
| **C4** | **CRITICAL** | E4: "79 columns, **identical across all three dialects**"; E6: "EXACT MATCH: yes" | MySQL's journal column is `trigger_`; the union is **88** keys. Per-dialect `comm`: MySQL has 1 unclassified + 1 stale; pg and sqlite clean | **CONFIRMED FALSE** — count identical, SET not. Plan Task 3 cannot go GREEN as written |
| **C5** | **CRITICAL** | ADR decision 1 + spec D1 + plan file table: glob `**/migrations/*.sql` | Finds **1 of 4** (only `casbin_rule`); Go's `filepath.Glob` has no `**` and finds **0**. Correct: `**/migrations/**/*.sql` → 4 | **CONFIRMED FALSE** — inverse of the omission it closes; plan silently uses a different rule |
| **C2** | **CRITICAL** | "Ten decisions ship together"; interaction pass reasons about "D10" | Nine (`D1`–`D9`); no D10 exists; 8 of 36 pairs are written | **CONFIRMED FALSE**, propagated into the plan's self-review table |
| C1 | MAJOR | spec header "(E1–E13, every claim below executed)" | E1–**E15**; the spec cites E14 and E15 in its own body; E4 is never cited | CONFIRMED |
| C6 | MAJOR | "postgres TEXT-ish 48 / mysql 48 / sqlite 67" | Reproduces exactly — but over the 79 `wrkflw_*` only; a Postgres deployment is **55**. NET unstated | ARITHMETIC HOLDS, SCOPE UNSTATED |
| C7 | MAJOR | the word "free-form" | names **three** sets: 48 (string-typed), 11 (the class), 19 (timestamps-as-TEXT) | CONFIRMED — anchor shift |
| C8 | MAJOR | ADR Context: `casbin_rule` has "seven **free-form** `TEXT` columns" | seven `TEXT` columns, classed **`policy`** by the same ADR's decision 4 | CONFIRMED contradiction |
| C11 | MAJOR | `keyed` is per-dialect; only "27 of 79 Postgres" is ever stated | MySQL **28**, SQLite **28**, Postgres deployment **29 of 87**. Plan asserts one dialect of three | CONFIRMED gap |
| C12 | MAJOR | "27 of 79" restated in 4 places | dialect survived every restatement; the `wrkflw_*` restriction appears **only** in a plan test comment | CONFIRMED |
| C13 | MAJOR | plan self-review: "every spec decision maps to a task" | maps a nonexistent D10; and Task 2's `MigrationSets` **declaration** is authorised by no ADR decision (the ADR says "never listed") | CONFIRMED both directions |
| C15 | MAJOR | spec: the invariance pin has "a **real mutation** against a migration file on disk" | the prescribed `sed` renames a column ⇒ absent, not divergent ⇒ `ClassDivergences` stays GREEN; the plan's own expected output names Task 3's guards | CONFIRMED — pin is fixture-verified only |
| C16 | MAJOR | "**Three** of the five are real RED; the **fourth** is a pin" | **Four** are real RED; the **fifth** is the pin | CONFIRMED — both halves wrong |
| C17 | MAJOR | spec D6/D9: one file `internal/persistence/store/atrest_test.go`, test `TestAtRest_SecurityMdInSync` | plan builds a new `internal/atrest` package (8 Go files) and names the test `TestSecurityMdInSync`; ⚠ a `-run` on the spec's name exits 0 silently | CONFIRMED divergence |
| C9 | MINOR | ADR cites **E4** for "`wrkflw_journal` carries no hash/prev-hash/signature column" | E4 has no column names; the evidence is **E13** | CONFIRMED miscitation |
| C10 | MINOR | E14's `grep -rhn … \| uniq -c` output | conclusion TRUE (4 `Up` at line 1, 4 `Down`, no `StatementBegin/End` in any `.sql`) but the pasted command cannot demonstrate it | CONCLUSION HOLDS, COMMAND UNSOUND |
| C14 | INFO | 12 inherited claims (four migration dirs, four `Authorize` sites, one FK, one dual-class column name, four scripts, mockgen-only `go:generate`, three policy sources, nine tables, the six class counts, the Postgres type histogram, the index counts, E9's 15/7/4/1) | all independently re-derived | **ALL HOLD** — see table in C14 |

**Not verified (needs Docker):** nothing in this lens required it. The SQLite path is pure Go; all
counting was done over SQL text and Go source. Task 7's cross-check assertions themselves are
`UNVERIFIED (needs Docker)`.
