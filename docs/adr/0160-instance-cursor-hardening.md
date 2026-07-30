# 160. Instance-listing cursor hardening

- Status: Accepted
- Date: 2026-07-30

> ADR numbers 0155–0158 are reserved by the parked
> `feat/durable-waiters-delivery-correctness` branch and do not exist on `main`.
> This ADR takes 0160, continuing from ADR-0159.

## Context

ADR-0159 introduced a **new** armed-timer cursor
(`runtime/kernel/armed_timer_paging.go`, added whole in `def1e45`) carrying a
`kind` discriminator, `DisallowUnknownFields` decoding, empty-identity
rejection, and an error-returning encoder. It explicitly deferred the
instance-listing cursor.

That deferred cursor, `runtime/kernel/lister.go`, still has the defects. All
were reproduced by probe against `main` at `bfa4a1d`:

1. `DecodeCursor` (`:34-44`) uses plain `json.Unmarshal` into an undiscriminated
   struct, so an armed-timer cursor decodes with **no error** as
   `(zero time, "inst-x")`.
2. `EncodeCursor` (`:26-29`) discards the `json.Marshal` error and returns `""`
   — which *is* the first-page sentinel documented at `:75-77`.
3. A zero-time/empty-`instance_id` payload decodes without error, though
   ADR-0152 forbids empty identity keys and a cursor is always minted from a
   real row.

Severity differs from the armed-timer case, which is why the same reasoning did
not surface it. Armed-timer listing is **ASC**, so a zero-ish foreign key
matched nearly every row and the client paged forever. Instance listing is
**DESC**, so the predicate `(started_at, instance_id) < (zero, 'inst-x')`
matches **nothing**: an empty page with a 200. Verified on every backend,
including a live MySQL 8.0 container under `STRICT_TRANS_TABLES,NO_ZERO_DATE`,
which accepted the zero-time bind and returned `rows=0, err=<nil>`. A silently
empty page is a wrong answer that reports itself as success — and unlike an
infinite loop, nobody files a ticket about it.

**The rule-#9 audit then found a fourth defect, in the shipped code this ADR set
out to copy.** `json.Decoder.Decode` reads only the first JSON value and ignores
trailing bytes, where `json.Unmarshal` rejects them:

```
json.Unmarshal(payload + `{"kind":"evil"}`)   => invalid character '{' after top-level value
SHIPPED DecodeArmedTimerCursor(base64(same))  => err=<nil>  id="i" timerID="tm"
```

So a naive port would have made the instance cursor **strictly worse** than the
`json.Unmarshal` it replaced, while leaving ADR-0159's cursor broken. This
reframes the delivery: it is not only a port, it is a fix to shipped code.

## Decision

Harden the instance cursor, extract only the strict-decoding logic, and close
the trailing-data hole for both families at once.

1. **Add `decodeCursorInto(cursor string, dst any) error`**
   (`runtime/kernel/cursorcodec.go`, unexported): base64 → `json.Decoder` with
   `DisallowUnknownFields` → decode → assert `dec.Token()` yields `io.EOF`. It
   returns a **bare** error; each family wraps it in its own sentinel.

2. **Share the decoder only.** Encoding stays per-family: it is
   `json.Marshal` + base64, and each caller wraps the error with its own
   entity-naming message. Extracting it would save about a line per caller while
   hiding which envelope is in play.

3. **Harden `lister.go`**: add `Kind` with `instanceCursorKind = "instance"`;
   `EncodeCursor` → `(string, error)`; reject mismatched kind, then empty
   `InstanceID`, then a **zero `StartedAt`**.

   The start-time guard came from `/code-review`, after two adversarial audits
   missed it. An absent `started_at` passes every other guard and decodes to the
   zero time — the lowest key under DESC — so it matched nothing and produced
   exactly the silent empty page this ADR's Context describes. It is safe here
   and **asymmetric with the armed-timer family on purpose**: a zero `next_run`
   is a legitimate armed value there (ADR-0159), whereas `StartedAt` comes from
   `Trigger.OccurredAt` and a zero-`StartedAt` row sorts last under DESC, where
   no cursor is minted.

4. **`armed_timer_paging.go` delegates**, gaining the trailing-data fix with no
   change to its public API, sentinels, or message text.

5. **Fix `memstore.go:201`**: `cmp.Compare(b.StartedAt.UnixNano(), …)` →
   `b.StartedAt.Compare(a.StartedAt)`. `UnixNano` is undefined outside
   1678–2262 and goes negative at year 10000, so `MemInstanceStore` sorts such a
   row as *oldest* while every SQL backend sorts it newest. In scope because
   the encode-error test needs exactly that row and would otherwise pass
   *because of* the overflow.

Rejected: **port by copy**. Smaller diff, no risk to shipped code — but it
would have duplicated the trailing-data bug rather than fixing it, which is now
the decisive argument against it.

Rejected: **a `sentinel error` parameter on the decoder** (the first draft of
this ADR). It splits sentinel-wrapping across two files, so a reader needs both
to know what `ErrBadCursor` can mean. Caller-side wrapping keeps it in one
place.

Rejected: **keeping `EncodeCursor` infallible** by clamping the timestamp. It
either alters an operator's data silently or moves the failure somewhere less
visible.

## Consequences

**Breaking — `EncodeCursor` returns `(string, error)`.** Two in-repo call sites
(`runtime/kernel/memstore.go:245`, `internal/persistence/store/lister.go:160`),
both already in functions returning `(page, error)`. But `kernel.InstanceLister`
is a documented public port with three in-repo implementations plus `WithLister`
(`service/options.go:67`), so **any consumer-implemented lister must update
too** — a wider blast radius than the call-site count suggests.

**Breaking — already-issued instance cursors are rejected.** A client paging
across a deploy gets one 400 and restarts from page one. No cursor is persisted
in any table, migration, cache or config, so that is the whole blast radius.

Both go in `CHANGELOG.md` under `Breaking changes (pre-v0.1.0)`. ADR-0159's
entry is missing and is backfilled in the same edit — ADR-0142 made a CHANGELOG
entry an explicit condition of this pre-tag latitude.

**`kind` is defence-in-depth, not the fix — and this ADR should not pretend
otherwise.** `DisallowUnknownFields` alone already rejects an armed-timer cursor
decoded into `cursorPayload`, since `kind`, `next_run` and `timer_id` are all
unknown there. What `kind` buys is symmetry with the sibling and rejection of a
future cursor whose field set is a *subset* of `{started_at, instance_id}`,
which unknown-fields structurally cannot catch. Dropping it would leave this fix
non-breaking on the wire. It is kept because the wire break is cheap while
v0.1.0 is untagged and needs a migration window afterwards.

Note the corollary for tests: an **old-format** cursor is the only payload that
reaches the kind check, so without a test for it the whole suite still passes
with the kind comparison deleted.

**No transport change.** `httpcore/errors.go:36` already maps `ErrBadCursor` →
400. An `EncodeCursor` failure carries no sentinel and falls to the 500 default
with an empty body — adjudicated as intended, since it is an engine-invariant
violation rather than operator error.

**The class closes within `runtime/kernel`.** The decoder is unexported, so a
future cursor in another package could not reuse it; today both cursors live
here, and `grep -rl base64 --include='*.go'` finds no third opaque token in the
module. Promoting it to `internal/cursor` is deferred until a second package
needs it.

**`dialect.KeysetCursorArgCount` remains deferred** — visible at the touched
call site, explicitly out of scope.

**Cursors stay unsigned, and that is correct only while listing is unscoped.**
`/security-review` confirmed a forged cursor grants nothing a caller could not
reach by paging normally: it influences only the keyset position, the status
filter is bound separately, and `ListInstances` has no tenant or actor scoping
to subvert. The decoded values reach SQL exclusively as bind parameters
(`dialect/{postgres,mysql,sqlite}.go` keyset predicates are compile-time
constants), so `instance_id` cannot escape a placeholder. **The day instance
listing gains tenant or actor scoping, that reasoning expires and the cursor
becomes a capability that must be authenticated.**

**`decodeCursorInto`'s struct-pointer requirement is documented, not enforced.**
`DisallowUnknownFields` is silently a no-op for map and interface destinations,
so a future caller passing `*map[string]any` would lose that guard with no
compile or runtime error. All three current call sites pass struct pointers.
Defence-in-depth, not a live defect.
