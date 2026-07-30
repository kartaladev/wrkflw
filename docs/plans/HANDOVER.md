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

## State — 2026-07-31

| | |
|---|---|
| `main` | **`bfa4a1d`**, pushed, clean — ADR-0159 merged `--no-ff` and pushed |
| `feat/instance-cursor-hardening` | **ADR-0160 — implemented, full local gate green, NOT merged** |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked design bundle, **docs only, zero code**, local-disk only |
| Latest ADR | **0160**. Next free number is **0161** — 0155–0158 are reserved by the parked branch |
| v0.1.0 | not tagged |

## Next work — run these in order

### 1. Finish delivering ADR-0160 (instance-cursor hardening)

The branch is code-complete and locally gated: build/vet clean, `go test -race
-count=1 ./...` exit 0 (64 packages, **0 FAIL, 0 skips**, Docker up),
`golangci-lint` 0 issues, `runtime/kernel` 88.6% and
`internal/persistence/store` 87.6% (both ≥85%), repo total 73.3% (unchanged,
pre-existing `examples/` drag).

**What remains is the Delivery Gate, and it needs the owner:**

```bash
/code-review        # disable-model-invocation — only the owner can run it
/security-review
```

Fold all findings into the feature commit with `git commit --amend` — never
stack fixups. Then merge `--no-ff` to `main` and push.

Plan (with the full `▶ Progress` block, audit adjudications and mutation-verify
evidence): `docs/plans/2026-07-30-instance-cursor-hardening.md`.

### 2. Restart the parked delivery bundle at ADR-0158

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
4. **ADR-0159 names two symbols that do not exist** (`0159:96` says
   `EncodeArmedCursor` / `DecodeArmedCursor`; the shipped names are
   `EncodeArmedTimerCursor` / `DecodeArmedTimerCursor`). That ADR is merged and
   pushed, so it takes its own small `docs:` commit, not an amend.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Designs** — `docs/specs/`.
- **Conventions and gates** — `CLAUDE.md`.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
