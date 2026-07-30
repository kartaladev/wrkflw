# Instance-listing cursor hardening

- Date: 2026-07-30
- Status: Approved (design); rule-#9 audit complete, findings folded
- ADR: `docs/adr/0160-instance-cursor-hardening.md`
- Plan: `docs/plans/2026-07-30-instance-cursor-hardening.md`

## Problem

`runtime/kernel/lister.go` mints and parses the opaque cursor for
`InstanceLister.List`. It carries the defects ADR-0159 addressed in the
armed-timer cursor. Every observation below was reproduced by direct probe
against `main` at `bfa4a1d` — none is inferred from reading.

### Defect 1 — a foreign cursor is accepted silently

`DecodeCursor` (`runtime/kernel/lister.go:34-44`) uses plain `json.Unmarshal`
into an undiscriminated struct. Unmarshalling into a struct ignores fields it
does not recognise, so an **armed-timer** cursor decodes with **no error**:

```
armed-timer cursor -> DecodeCursor => startedAt=0001-01-01 00:00:00 UTC  instanceID="inst-x"  err=<nil>
```

The predicate becomes `(started_at, instance_id) < (zero, 'inst-x')` under
`ORDER BY started_at DESC, instance_id DESC`. The zero time is the minimum of
the domain, so no real row satisfies it: **the operator gets an empty page with
a 200 and no diagnostic.**

The severity differs from the armed-timer case in a way that matters for how
this is justified. There, listing is **ASC**, so the foreign key matched nearly
every row and the client paged forever. Here it is **DESC**, so the same
corruption yields the opposite extreme: nothing. An empty page is quieter than
an infinite loop, and quieter is worse — nobody reports it.

Verified across backends: all three keyset predicates use `<` under DESC
(`dialect/postgres.go:121`, `mysql.go:125`, `sqlite.go:140`), and
`memstore.go:226-227` agrees. On a live MySQL 8.0 container with
`STRICT_TRANS_TABLES,NO_ZERO_DATE`, the zero-time bind was accepted in the
`WHERE` clause and returned `rows=0, err=<nil>` with no warnings — so the
failure really is a quiet empty page on every backend, not a 500 on one.

### Defect 2 — `EncodeCursor` swallows its marshal error

`EncodeCursor` (`:26-29`) discards the `json.Marshal` error and returns what
base64 makes of a nil slice — the empty string:

```
year-10000 EncodeCursor => "" (len=0, empty == first-page sentinel: true)
```

The empty string **is** the first-page sentinel (`InstanceFilter.Cursor`,
`:75-77`), so a page can answer `HasMore: true` with an empty `NextCursor` and a
conforming client re-requests page one forever.

Reachability, stated plainly rather than overstated: the only way
`time.Time.MarshalJSON` fails is a year outside `[0,9999]`. `StartedAt` is
engine-minted from the clock, unlike `schedule.At`, which took arbitrary user
input. This is a latent hole, not a live incident.

### Defect 3 — an empty identity is accepted

`(zero time, "")` decodes with `err=<nil>`. A cursor is always minted from a
real row and ADR-0152 forbids empty identity keys, so an empty `instance_id`
means the payload was fabricated or truncated.

### Defect 4 — trailing data is accepted by the *shipped* armed-timer cursor

Found by the rule-#9 audit and confirmed by probe. `json.Decoder.Decode` reads
only the first JSON value and ignores whatever follows, where `json.Unmarshal`
rejects it:

```
json.Unmarshal(payload + `{"kind":"evil"}`)          => err=invalid character '{' after top-level value
json.Decoder.Decode(same)                            => err=<nil>  instance_id="i"
SHIPPED DecodeArmedTimerCursor(base64(same))         => err=<nil>  id="i" timerID="tm"
```

This inverts the naive plan. Porting `DisallowUnknownFields` to the instance
cursor as-is would have made it **strictly worse** on this axis than the
`json.Unmarshal` it replaced, while leaving the shipped armed-timer cursor
broken. Goal 3 is only half met without a trailing-data guard.

## Goals

1. A cursor from another family is rejected, not silently reinterpreted.
2. `EncodeCursor` cannot produce the first-page sentinel as a *value*.
3. A fabricated, truncated, or **trailing-padded** payload is rejected — for
   both cursor families.
4. The strict-decoding logic exists once, so the two cannot drift.

Non-goal: pagination semantics, ordering, limits, the SQL predicate.

## Design

### Shared decoder — `runtime/kernel/cursorcodec.go` (new, unexported)

```go
func decodeCursorInto(cursor string, dst any) error
```

Base64-decode → `json.Decoder` with `DisallowUnknownFields()` → decode into
`dst` → **assert `dec.Token()` returns `io.EOF`**, rejecting trailing data.
Returns a **bare** error; each family wraps it in its own sentinel.

Three deliberate choices, each a correction from the audit:

- **Bare error, not a `sentinel error` parameter.** Passing the sentinel in
  would split `ErrBadCursor`-wrapping across two files (codec wraps
  base64/JSON, caller wraps kind/identity), so a reader would need both files to
  know what the sentinel can mean. Caller-side wrapping keeps it in one place,
  and matches how the rest of this repo wraps.
- **No type parameter.** `encoding/json` takes a destination pointer, so `T`
  bought nothing. (`DisallowUnknownFields` does work correctly through a generic
  struct `T` — verified — but is a silent no-op for map/pointer types, a trap
  worth not building.)
- **Decode-only; encode is not shared.** Encoding is `json.Marshal` + base64,
  and both callers wrap the error with their own entity-naming message.
  Extracting it would save roughly one line per caller in exchange for hiding
  which envelope is in play. The decode side, by contrast, now carries three
  interacting guards and a shipped bug fix — that is what earns the extraction.

Each family's `encodeCursor` marshals and returns the `json.Marshal` error
**unwrapped**, so existing wrapped messages stay byte-identical.

### `runtime/kernel/lister.go` — the fix

- `cursorPayload` gains `Kind string \`json:"kind"\``, with
  `const instanceCursorKind = "instance"`.
- `EncodeCursor(startedAt time.Time, instanceID string) (string, error)`,
  failing with `workflow-runtime: encode instance cursor for %q: %w`.
- `DecodeCursor` calls `decodeCursorInto`, then checks kind, then identity,
  then **a non-zero `started_at`** — each wrapped in `ErrBadCursor` with a
  distinct message.

The start-time guard was added after `/code-review`: an absent `started_at`
survives base64, `DisallowUnknownFields`, the trailing check, the kind check
*and* the identity check, decoding to the zero time — the lowest key under DESC,
so it matches nothing. It is the same silent-empty-page failure the delivery
exists to close, reached by the one payload every other guard passes.

Rejecting it is safe, and deliberately **asymmetric with the armed-timer
sibling**: a zero `next_run` there is a legitimate armed value (ADR-0159), so
that family can only guard identity. Here `StartedAt` is minted from
`Trigger.OccurredAt` (`engine/step_triggers.go:30`), and a zero-`StartedAt` row
sorts *last* under DESC — where `hasMore` is false — so a cursor is never
legitimately minted from one.

**What `kind` actually buys, priced honestly.** `DisallowUnknownFields` alone
already rejects an armed-timer cursor here, because `kind`, `next_run` and
`timer_id` are all unknown to `cursorPayload`. So the discriminator is *not*
what fixes Defect 1. It buys two things: symmetry with the shipped sibling, so a
third cursor has one obvious shape to copy; and rejection of a hypothetical
future cursor whose field set is a *subset* of `{started_at, instance_id}`,
which unknown-fields structurally cannot catch. It is deliberate
defence-in-depth, bought with a wire break — not a load-bearing part of the
defect fix. Dropping it would leave the fix non-breaking on the wire; the
recommendation is to keep it while v0.1.0 is untagged, because the same change
costs a migration window later.

### `runtime/kernel/armed_timer_paging.go` — delegate, and gain the fix

Delegates to `decodeCursorInto`, which closes Defect 4 for the armed-timer
cursor at the same time. Public signatures, sentinels and message text are
unchanged; base64/JSON error strings stay byte-identical because both sites
wrap as `fmt.Errorf("%w: %w", sentinel, err)`.

### `runtime/kernel/memstore.go:201` — ordering fix (in scope)

`sort` uses `cmp.Compare(b.StartedAt.UnixNano(), a.StartedAt.UnixNano())`.
`UnixNano` is undefined outside 1678–2262 and overflows negative at year 10000:

```
year10000.After(2026)=true   but UnixNano(10000) > UnixNano(2026) = false
[-4852116231933722624 vs 1785369600000000000]
```

So `MemInstanceStore` sorts a year-10000 instance as the **oldest** while every
SQL backend sorts it newest — a real divergence from the port's contract. It is
in scope because the encode-error test needs exactly such a row, and would
otherwise pass *because of* the overflow. Fix: `b.StartedAt.Compare(a.StartedAt)`.

### Call sites

`runtime/kernel/memstore.go:245` and `internal/persistence/store/lister.go:160`
propagate; the latter keeps its `workflow-store: lister: ...` prefix, matching
`:88`. `transport/http/httpcore/errors.go:36` already maps `ErrBadCursor` → 400,
so decode rejections surface as 400.

An `EncodeCursor` failure carries no sentinel, so `httpcore.ClassifyError`
(`errors.go:50`) falls to the 500 default whose `ErrorBody.Message` is
deliberately empty. Adjudicated as intended: it is an engine-invariant
violation, not operator error, and the detail belongs in logs.

Out of scope, explicitly: `dialect.KeysetCursorArgCount` (`store/lister.go:110`,
`:123`).

## Consequences

1. **`EncodeCursor` returns `(string, error)`.** Two in-repo call sites — but
   `kernel.InstanceLister` is a documented public port with three in-repo
   implementations plus `WithLister` (`service/options.go:67`), so **any
   consumer-implemented lister must update too**.
2. **Already-issued instance cursors are rejected**, via the kind check (not the
   unknown-field check — `started_at` and `instance_id` remain known). A client
   paging across a deploy gets one 400 and restarts. No cursor is persisted in
   any table, migration, cache or config — verified — so the blast radius really
   is one 400.

Both require a `CHANGELOG.md` entry under `Breaking changes (pre-v0.1.0)`;
ADR-0159's entry is also missing and is backfilled here.

## Testing

RED first. Every test that certifies a defect is fixed is mutation-verified.

| Case | Guard that must fire |
|---|---|
| Armed-timer cursor → `DecodeCursor` | unknown-field |
| **Old-format cursor** (`started_at`+`instance_id`, no `kind`) | **kind** |
| `{}` | kind (`Kind:""`) |
| `null` | kind |
| Unknown field in an otherwise-valid payload | unknown-field |
| Correct `kind`, empty `instance_id` | identity |
| **Correct `kind` and identity, no `started_at`** | **start-time** |
| **Valid payload + trailing JSON** | **trailing** |
| Year 10000 → `EncodeCursor` | error returned; string is `""` |
| Round-trip | value-equal, `kind` present |
| Non-base64 | base64 (existing) |
| Instance cursor → `DecodeArmedTimerCursor` | regression guard |

The **old-format row is load-bearing**: it is the only payload that reaches the
kind check, so without it the entire table still passes with the kind comparison
deleted. Assert the distinct message, not just `errors.Is`.

Codec tests stay black-box via `runtime/kernel/export_test.go` (`package
kernel`) re-exporting the unexported helper — the pattern already used at
`internal/persistence/store/export_test.go` — so no in-package test file is
introduced into a directory that is otherwise entirely `package kernel_test`.

`ExampleEncodeCursor` is added, mirroring `ExampleEncodeArmedTimerCursor`
(`example_test.go:67`), per CLAUDE.md Golang rule #6.

**Encode-error branch reachability.** The branch needs `started_at` ≥ year
10000. MySQL `DATETIME(6)` maxes at 9999-12-31 and SQLite's text codec uses
`time.RFC3339Nano` (`time_codec.go:26`, `:56`), which requires a 4-digit year —
so only Postgres reaches it. At `store/lister.go:160` the cursor is minted from
the *last* item under DESC, so the maximum timestamp is item 0 unless the page
holds exactly one row. It is therefore covered directly in `MemInstanceStore`,
and adjudicated as defensive-only at the store call site, with that reasoning
recorded rather than asserted as covered.
