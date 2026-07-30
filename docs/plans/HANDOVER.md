# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack of twenty "PREVIOUS RESUME
> POINT" blocks and was silently abandoned for 45 ADRs — see
> `docs/plans/HANDOVER-archive.md`. Per-delivery detail does **not** belong here:
> it belongs in that delivery's plan under a `▶ Progress` block, where it dies
> with the plan. This file carries only: where `main` is, what is in flight, and
> what to do next.

## State — 2026-07-30

| | |
|---|---|
| `main` | `9656799`, pushed, clean |
| `feat/bounded-armed-timer-reads` | **`def1e45`** — ADR-0159, gated and complete, **not merged, not pushed** |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked design bundle, **docs only, zero code**, local-disk only |
| Latest ADR | **0159**. Next free number is **0160** — 0155–0158 are reserved by the parked branch |
| v0.1.0 | not tagged |

**ADR-0159 (bounded armed-timer reads) is done and fully gated:** build/vet clean,
`go test ./...` exit 0 (64 packages, 0 fail), `-race` clean, `golangci-lint` 0
issues, every touched package ≥85%, `/security-review` 0 findings, `/code-review`
4 findings → 4 folded. Plan: `docs/plans/2026-07-30-bounded-armed-timer-reads.md`.

## Next work — run these in order

### 1. Merge ADR-0159 to `main`

```bash
git checkout main
git merge --no-ff feat/bounded-armed-timer-reads
go build ./... && go test ./... ; echo "EXIT=$?"   # must be 0
golangci-lint run ./...                             # must be 0 issues
```

Docker must be up, or the Postgres/MySQL conformance tests skip silently — and a
silent skip is indistinguishable from a pass. Check the exit code, never a
`| grep | head` tail. **Ask before pushing:** the owner said "no need to push"
for the session that produced this.

### 2. Harden the instance-listing cursor (needs ADR-0160)

`runtime/kernel/lister.go` has both defects ADR-0159 fixed in the armed-timer
cursor. Probe-verified on 2026-07-30, not inferred:

- `DecodeCursor` has no discriminator and uses plain `json.Unmarshal`, which
  ignores unknown fields — so an armed-timer cursor decodes into it with **no
  error** as `(zero, "inst-x")`. Instance listing is DESC, so that predicate
  matches nothing and the operator gets a silently empty page with a 200. Less
  severe than the armed-timer case's infinite loop; still a wrong answer.
- `EncodeCursor` swallows its marshal error and returns `""` — which *is* the
  first-page sentinel, so a page can answer `has_more: true` with an empty
  `next_cursor`. Less reachable than the timer case, because `StartedAt` is
  engine-minted rather than user-supplied like `schedule.At`.

Fix is a direct port of `runtime/kernel/armed_timer_paging.go`: a `kind`
discriminator, `DisallowUnknownFields`, empty-identity rejection, and
`EncodeCursor` returning `(string, error)` with its two call sites updated
(`internal/persistence/store/lister.go`, `runtime/kernel/memstore.go`). Write the
RED cases first — a rejected foreign cursor, `{}`, `null`, and year-10000. It
changes a public signature, so it needs its own ADR and one rule-#9 audit.

### 3. Restart the parked delivery bundle at ADR-0158

The signal/message delivery-correctness bundle was split into four deliveries;
ADR-0158 (first-match-per-family) is delivery #1. All three auditors rejected the
original monolith — do not resurrect it. Freeze the tree at a tag before
re-auditing: last round the bundle mutated under an auditor mid-run. The decisive
design point is that the restart- and multi-replica-safety requirement is met by
the durable projection **alone**, with no semantic change; every dangerous
finding came from the fan-out semantics layered on top.

## After that — pre-v0.1.0 blockers

1. **Strict definition decoding** (`DisallowUnknownFields` / `KnownFields(true)`).
   Lenient decode plus a fail-open `AuthzSpec` means any future `eligible_*` tag
   drift silently degrades to allow-all. Harmless while untagged; a genuine
   security finding the moment v0.1.0 exists.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go:156-159`
   arms with a zero `nextRun` when `TriggerSpec.Next` reports `ok == false`;
   `DATETIME(6) NOT NULL` rejects the `'0000-00-00'` the driver emits under strict
   mode, and `jobStore.Save` propagates it, so the step fails. Postgres and SQLite
   store it fine. Needs a reject-vs-normalise decision, so it needs its own ADR.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Designs** — `docs/specs/`.
- **Conventions and gates** — `CLAUDE.md`.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
