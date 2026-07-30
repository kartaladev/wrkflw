# Plan — Instance-listing cursor hardening (ADR-0160)

- Spec: `docs/specs/2026-07-30-instance-cursor-hardening.md`
- ADR: `docs/adr/0160-instance-cursor-hardening.md`

## ▶ Progress

| | |
|---|---|
| Branch | `feat/instance-cursor-hardening` (off `main` @ `bfa4a1d`) |
| Status | **Implemented. `/code-review` PASSED — 3 findings, 3 folded. Awaiting `/security-review`, then merge.** |
| Phases landed | A, B, C, D — all four |

### `/code-review` findings — all 3 folded via `--amend`

1. **Medium — `DecodeCursor` did not guard a missing `started_at`.** Probe-confirmed:
   `{"kind":"instance","instance_id":"inst-x"}` passed base64,
   `DisallowUnknownFields`, the trailing check, the kind check *and* the identity
   check, decoding to the zero time — the lowest key under DESC, so it matched
   nothing. **The exact silent-empty-page failure this delivery exists to close,
   via the one payload every other guard passes.** Fixed with an `IsZero` guard,
   RED-first and mutation-verified (M7). Deliberately asymmetric with the
   armed-timer family, where a zero `next_run` is legitimate (ADR-0159).
2. **Low — the trailing-data branch discarded `dec.Token()`'s error**, collapsing
   "second JSON value" (err == nil) and "corrupt bytes" (`*json.SyntaxError`
   naming the offending character) into one message. Now wrapped when non-nil;
   `errors.New` for the verb-less case.
3. **Low — stale doc comment on `DecodeArmedTimerCursor`**, which still promised
   only base64/JSON failures while the function also rejects a foreign kind,
   empty identity, and now trailing data. The sibling `DecodeCursor` had been
   updated in this same diff, so the family documented two different contracts
   for identical behaviour.

**Adjudicated by the reviewer, not defects:** the `EncodeCursor`-failure 500
(explicitly chosen in this ADR over clamping) and both breaking changes (in
`CHANGELOG.md` and Consequences).

**Process note — this is the second consecutive delivery where `/code-review`
found a Medium that adversarial Opus stand-ins missed.** Two auditors with
different lenses caught two structural errors between them and still missed the
last vector of the very bug class the delivery targets. Stand-ins reduce rework;
they are not the gate.

**Re-gated after folding:** 64 ok / 0 FAIL / 0 skips `-race`, lint 0,
`runtime/kernel` **88.7%**, `internal/persistence/store` **87.6%**.

**Gate evidence (2026-07-31, Docker up):** `go build` + `go vet` exit 0;
`go test -race -count=1 ./...` exit 0 — **64 ok, 0 FAIL, 0 skips**;
`golangci-lint run ./...` → **0 issues**; `runtime/kernel` **88.6%**,
`internal/persistence/store` **87.6%** (both ≥85%); repo total 73.3%
(unchanged — pre-existing `examples/` drag, not a regression).

**Phase D — all six load-bearing guards mutation-verified.** Each was broken on
purpose, the guarding test confirmed to FAIL, then restored from a snapshot and
`diff`-checked byte-identical:

| Guard | Mutation | Result |
|---|---|---|
| memstore DESC ordering | `Compare` → `cmp.Compare(UnixNano)` | ✅ failed |
| memstore encode-error propagation | drop the error | ✅ failed |
| `DecodeCursor` kind check | `if false` | ✅ failed |
| `DecodeCursor` identity check | `if false` | ✅ failed |
| shared trailing-data guard | `if false` | ✅ failed |
| `EncodeCursor` error propagation | `return "", nil` | ✅ failed |

**`armed_timer_paging_test.go` constraint honoured:** the only edits are the
`EncodeCursor` call-site adaptation (via `mustEncodeInstanceCursor`) and a
comment correction — the old comment credited the *discriminator* for catching
an instance cursor, when `DisallowUnknownFields` is what actually fires. **No
assertion changed.**

Two notes for the reviewer:

- The `memstore.go:201` ordering fix was written **before** its test (a TDD
  lapse). Rather than claim a red state that never happened, the fix was
  reverted to produce a genuine failure, then restored — recorded above as
  mutation M1. Disclosed rather than papered over.
- The Phase A codec test fixture initially carried `"kind":"instance"`, which
  the decoder correctly rejected as an unknown field because `Kind` did not
  exist until Phase B. The *fixture* was wrong, not the implementation; it now
  deliberately carries no discriminator, since those cases test the shared
  decoder and must not depend on one family's envelope.

Probe-verified against `bfa4a1d` (re-run in this session, not inherited):

- Armed-timer cursor → `DecodeCursor` gives `err=<nil>`, `(0001-01-01Z, "inst-x")`.
- `EncodeCursor` at year 10000 returns `""` == the first-page sentinel.
- Zero-time/empty-ID payload accepted with `err=<nil>`.
- **Shipped** `DecodeArmedTimerCursor` accepts trailing JSON with `err=<nil>`;
  the `json.Unmarshal` it would replace rejects it.
- `UnixNano` at year 10000 is negative, so `memstore.go:201` sorts it oldest
  while SQL sorts it newest.
- Exactly 2 production encode + 2 production decode sites, plus **2 test call
  sites** (`lister_test.go:15`, `armed_timer_paging_test.go:121`).

### Audit outcome

Two Opus auditors, both source-verifying. **Accepted and folded:** the phase
reorder (below), the corrected test table, the trailing-data guard, the
`memstore` ordering fix, the bare-error decoder shape, CHANGELOG entries,
`export_test.go` placement, `ExampleEncodeCursor`, honest pricing of `kind`, the
wider `InstanceLister` blast radius, and the split coverage commands.
**Adjudicated differently:** `kind` is *kept* despite not being load-bearing
(reason in the ADR); the decoder stays unexported in `runtime/kernel` rather
than moving to `internal/cursor` (no second package needs it yet); inline
execution is kept but re-justified on *size*, not rule #11's carve-out — that
carve-out describes a repo-wide shared-type change, and this is ~5 lines in two
files, one of them in the same package.

## Execution mode — inline

~5 lines of production change across 2 files. Fan-out by package would cost more
in coordination than it saves, and concurrent agents in one working tree break
each other's `go test`.

## Phases

Each phase is RED → verify red → GREEN → verify green, and **every phase leaves
the package compiling.** The first draft of this plan split the signature change
from its call sites, which left `runtime/kernel` non-compiling across the phase
whose gate was "existing tests pass" — an unrunnable gate. Hence the reorder.

### Phase A — shared decoder + armed-timer delegation + trailing fix

RED: `runtime/kernel/cursorcodec_test.go` (black-box, via a new
`runtime/kernel/export_test.go` re-exporting `decodeCursorInto`) — unknown
field rejected, non-base64 rejected, **trailing JSON rejected**, valid payload
accepted, trailing *whitespace* still accepted.

Plus a RED case on the shipped decoder: `DecodeArmedTimerCursor` must reject a
valid payload with trailing JSON. **This fails today** — it is Defect 4.

```bash
go test ./runtime/kernel/... ; echo "EXIT=$?"   # must FAIL
```

GREEN: add `cursorcodec.go`; `armed_timer_paging.go` delegates.

Gate: `armed_timer_paging_test.go` passes **with no assertion changed** — and it
is runnable here, because nothing has broken the build yet. This is the real
safety net for the refactor.

### Phase B — instance cursor, signature, call sites (atomic)

One phase, because changing `EncodeCursor`'s signature breaks
`armed_timer_paging_test.go:121` (a composite-literal field, single-value
context) and `internal/persistence/store` simultaneously.

RED: extend `lister_test.go` with the spec's table. The **old-format cursor row
is mandatory** — it is the only payload reaching the kind check; without it the
table passes with the kind comparison deleted. Assert distinct messages, not
just `errors.Is`.

GREEN, all together:
- `Kind` + `instanceCursorKind`; `EncodeCursor` → `(string, error)`;
  `DecodeCursor` checks kind then identity
- `memstore.go:245` and `store/lister.go:160` propagate (latter keeps the
  `workflow-store: lister: ...` prefix)
- `armed_timer_paging_test.go:121` adapted via a `mustEncodeInstanceCursor(t, …)`
  helper — **the only permitted edit to that file; no assertion changes**
- `memstore.go:201` → `b.StartedAt.Compare(a.StartedAt)`, with a regression test
  asserting a year-10000 instance sorts newest

```bash
go build ./... && go test ./runtime/... ./internal/persistence/... ; echo "EXIT=$?"
```

### Phase C — docs and example

`ExampleEncodeCursor` mirroring `ExampleEncodeArmedTimerCursor`; `CHANGELOG.md`
entries for both breaks **and** the missing ADR-0159 entry; rewrite
`docs/plans/HANDOVER.md` in place (rule #10 — it still says `main = 9656799`
and "not merged", which is stale as of `bfa4a1d`).

### Phase D — mutation-verify

For each test certifying a defect is fixed — foreign cursor, old-format cursor
(kind guard), empty identity, trailing data, year-10000 encode, memstore
ordering — snapshot the impl, break that specific guard, confirm the test
**fails**, restore, `diff` to prove the restore was clean.

## Verification checklist

- [ ] `go build ./... && go vet ./...` → exit 0
- [ ] `go test -race -count=1 -coverprofile=cover.out ./...` → exit 0, 0 FAIL,
      Docker up (a silent container skip is indistinguishable from a pass)
- [ ] `go test -cover ./runtime/kernel/... ./internal/persistence/store/...` →
      each ≥ 85% (`scripts/coverage.sh` reports only the repo-wide total, ~73%,
      so it cannot answer this)
- [ ] `scripts/coverage.sh cover.out` → recorded as a number, not gated
- [ ] `golangci-lint run ./...` → 0 issues
- [ ] Every defect test mutation-verified (Phase D)
- [ ] `git diff runtime/kernel/armed_timer_paging_test.go` shows only the
      `EncodeCursor` call-site adaptation — no assertion changed
- [ ] CHANGELOG entries present for both breaks + ADR-0159 backfill
- [ ] `/code-review` and `/security-review` — **owner-run**; findings folded via
      `--amend`

## Out of scope

- `dialect.KeysetCursorArgCount` → `KeysetCursorArgs`.
- Promoting the decoder to `internal/cursor`.
- Pagination semantics, ordering, limits, the SQL predicate.
- Correcting ADR-0159's misnamed symbols (`EncodeArmedCursor` vs the shipped
  `EncodeArmedTimerCursor`, `0159:96`) — that ADR is merged and pushed, so it
  takes its own small commit, not an amend.
