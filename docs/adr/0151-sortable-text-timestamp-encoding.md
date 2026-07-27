# 151. Fixed-width TEXT timestamp encoding for lexicographic ordering

Status: Accepted — 2026-07-28. Amends the TEXT-timestamp encoding chosen in
[ADR-0080](0080-utc-time-discipline.md) (UTC time discipline) and relied upon by
[ADR-0081](0081-store-unification-dialect.md) (neutral store + dialect) and
[ADR-0082](0082-sqlite-backend.md) (SQLite backend).

## Context

SQLite stores every timestamp column as `TEXT` (`dialect.TimestampsAsText() == true`);
Postgres (`TIMESTAMPTZ`) and MySQL (`DATETIME(6)`) bind `time.Time` natively. On the
TEXT path, `store.timeArg` encoded values with `time.RFC3339Nano`.

**`time.RFC3339Nano` trims trailing zeros from the fractional second.** The Go standard
library documents this explicitly: *"The RFC3339Nano format removes trailing zeros from
the seconds field and thus may not sort correctly once formatted."* Encoded widths
therefore vary from 20 bytes (`2026-07-28T01:41:34Z`) to 30 bytes
(`2026-07-28T01:41:34.123456789Z`).

SQLite compares `TEXT` **lexicographically**. Every timestamp predicate and ordering in
the store is consequently only correct while string order matches chronological order —
and with a trimmed fraction it does not:

```
seed next_attempt_at = 2026-07-28T01:41:34.1Z
relay now            = 2026-07-28T01:41:34.15Z
truth:  now.After(seed)      = true   (row IS due)
sqlite: 'next_attempt_at<=?' = false  (row SKIPPED)
```

`.1Z` sorts *after* `.15Z` because `Z` (0x5A) > `5` (0x35) at the first differing byte.

The defect was latent from ADR-0080 until a full-suite run surfaced it as an
intermittent failure in `TestRelayBatchDurationHistogram/sqlite` — `DrainOnce` returned
0 for a row that was genuinely due. It reproduced only under load, because it requires
the relay's `now` and the seeded `next_attempt_at` to land on fraction widths that
invert. Two comments in the codebase asserted the invariant that was in fact broken:
`pruner.go` ("the lexicographic TEXT comparison is apples-to-apples") and
`dialect/sqlite.go` ("lexicographic `<` and `=` comparisons work correctly when
timestamps are normalised to UTC" — UTC normalisation is necessary but not sufficient).

Affected SQLite sites (all silently wrong, none loud):

| Site | Predicate | Impact |
|---|---|---|
| `relay.go` ×2 | `next_attempt_at <= ?` | outbox events skipped → delayed delivery |
| `call_links.go` ×5 | `claimed_at <= ?` | expired call-link leases not reclaimed |
| `pruner.go` ×4, `dedup.go` ×1 | `<col> < ?` | retention rows not pruned |
| `lister.go`, `dialect/sqlite.go` | `ORDER BY started_at DESC` + keyset cursor | instance pagination mis-ordered / rows skipped across pages |

The failures are silent and mostly self-healing within one second, which is precisely
why they survived: nothing errors, a row simply is not seen this pass.

## Decision

**Encode TEXT timestamps with a fixed-width nine-digit fraction**, so lexicographic
order equals chronological order by construction:

```go
const textTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func timeArg(d dialect.Dialect, t time.Time) any {
	if d.TimestampsAsText() {
		return t.UTC().Format(textTimeLayout)
	}
	return t
}
```

Three properties make this the whole fix:

1. **Fix at the source, not per query.** `timeArg` is the single write-side encoder for
   the TEXT path (33 call sites). Correcting it repairs all 12 inequality predicates and
   both `ORDER BY` sites at once. No query was changed.
2. **Reads stay backward compatible.** `parseTimeText` continues to parse with
   `time.RFC3339Nano`, whose *parsing* accepts any number of fractional digits. Rows
   written by the previous trimmed encoding remain readable; only ordering changed.
3. **UTC is already guaranteed** by ADR-0080, so `Z07:00` always renders as `Z` and the
   width is genuinely constant.

`time.RFC3339Nano` is now **banned on the write path** and this is stated in the
`Dialect.TimestampsAsText` contract doc, since that interface is what any future dialect
implementor reads.

### Rejected alternatives

- **Store epoch integers.** Correct and compact, but a schema change across every
  timestamp column, and it would forfeit human-readable rows and `julianday()`
  compatibility for a bug that a format change fixes completely.
- **Compare via SQL `datetime()`/`julianday()`.** Wrapping the column in a function makes
  every predicate non-sargable, defeating the indexes these hot paths rely on.
- **Patch only the relay claim.** Treats the symptom. The same latent defect sits in the
  lease, pruning, and pagination paths.

## Consequences

**Positive**

- Relay outbox claim, call-link lease reclaim, retention pruning, and keyset pagination
  become correct on SQLite. The intermittent `TestRelayBatchDurationHistogram/sqlite`
  failure is resolved at its root.
- The invariant is now pinned by tests (`time_codec_test.go`) rather than asserted in
  prose: encoded width is constant, and chronological order implies string order —
  including the two cases RFC3339Nano got wrong (trailing-zero fraction, whole second).
- Postgres and MySQL are untouched: `timeArg` returns `time.Time` for them, so the
  native-binding path and its behaviour are byte-identical.

**Negative / accepted**

- **Stored width grows** from ≤30 to exactly 30 bytes per TEXT timestamp. Negligible, and
  SQLite is scoped to single-node/test/embedded use (ADR-0082).
- **Rows written before this change keep the trimmed encoding** and still mis-compare
  against a fixed-width bound until rewritten. No data migration ships: the project is
  pre-1.0 with no released schema, and ADR-0132 keeps one migration file per dialect
  edited in place. A consumer with a pre-existing SQLite file should rebuild it. This is
  recorded rather than automated deliberately — a normalising `UPDATE` across every
  timestamp column in every table is a larger change than the bug warrants pre-1.0.
- **The `strftime('%Y-%m-%dT%H:%M:%SZ','now')` column DEFAULTs remain second-precision**
  and so still sort before a fractional value within the same second. This is unchanged
  by this ADR and is not a live path: `wrkflw_outbox.next_attempt_at`
  (`store_core.go:379`) and `wrkflw_processed_message.processed_at` (`dedup.go`) are both
  written explicitly via `timeArg`; the DEFAULTs are fallbacks only. Normalising them is
  queued as a follow-up.
