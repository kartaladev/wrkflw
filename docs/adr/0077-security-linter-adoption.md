# 0077. Security-linter adoption (gosec / bodyclose / errorlint)

Status: **Accepted — 2026-06-30.**
Design doc: `docs/specs/2026-06-30-action-safety-limits-design.md`.
Plan: `docs/plans/2026-06-30-action-safety-limits.md`.
Relates to: ADR-0076 (action-execution safety limits); the CI pipeline (release-foundation, 2026-06-30).

## Context

The 2026-06-30 production-readiness audit noted that `.golangci.yml` enabled only the `standard`
linter set (errcheck, govet, ineffassign, staticcheck, unused) plus the gofmt formatter. No
security/correctness-specific linters were enforced, so issues such as integer-overflow conversions,
weak RNG usage, unclosed HTTP bodies, and non-wrapping error formatting were invisible to CI. A raw
`gosec` run reported 36 findings.

## Decision

Enable three additional linters in `.golangci.yml` and triage every finding to zero:

- **`gosec`** — SAST (integer overflow, weak RNG, SQL string building, hardcoded creds, etc.).
- **`bodyclose`** — ensure HTTP response bodies are closed.
- **`errorlint`** — require `errors.Is`/`As` and `%w`-wrapping.

Two configuration choices:

1. **Uncap output** (`issues.max-issues-per-linter: 0`, `max-same-issues: 0`). golangci's defaults
   cap repeated findings at 3, which masked 9 additional `int -> int16` conversions behind the first 3.
   A security linter must surface every finding so none re-appears later when unrelated code shifts
   line attribution.

2. **Triage policy — fix real findings; document false positives at the decision point.** Each
   suppression carries a one-line rationale (inline `//nolint:gosec // Gxxx: <reason>` for one-off
   sites; a documented `exclusions.rules` entry in `.golangci.yml` for structural/repeated families):
   - **errorlint** (3) — real fix: `%v` → `%w` on the `ErrBadCursor`/`ErrBadInput` wraps so the
     underlying error is inspectable.
   - **G115 `int -> int16`** across persistence (Status / timer-Kind enums → smallint columns) —
     path exclusion: these enums are bounded (< 10 values) and cannot overflow int16.
   - **G201/G202** mysql SQL formatting/concat — path exclusion: the only interpolated values are
     `LIMIT` integers bounded by `NormalizeLimit` (1..201) and generated `?` placeholder lists (a
     placeholder is impossible for `LIMIT` alongside a `FOR UPDATE`/locking clause in MySQL 8); all
     row values are bound as query args.
   - **G115 `int -> int32`** (definition version), **uint/uint64 -> int64** (duration variable),
     **G404** (retry-backoff jitter — intentionally `math/rand`, not security-sensitive), **G101**
     (ephemeral testcontainers DSN) — inline `//nolint` with per-site rationale.
   - **G204 / G705** in `_test.go` files — test-path exclusion (test-only patterns, not attack surface).
   - **bodyclose** — 0 findings (response bodies already closed).

The audit's suggestion to add explicit non-negative validation to the mysql `LIMIT` constructors was
**not** taken: the values are already bounded by `NormalizeLimit` and constructor defaults, a negative
would produce a malformed (erroring) query rather than an injection, and adding validation tests for a
non-vulnerability is unnecessary (YAGNI). The bounded-int invariant is documented at the exclusion rule.

## Consequences

- CI's `golangci-lint` job now enforces gosec/bodyclose/errorlint; the lint gate is clean.
- Every security suppression is documented and auditable (inline reason or a commented `.golangci.yml`
  rule). A reviewer can see *why* each finding was accepted.
- Uncapped output means new gosec findings are never silently masked by repetition.
- Future genuinely-dangerous `int -> int16` conversions inside `internal/persistence/` would also be
  excluded by the path rule; this is accepted because int16 conversions there are exclusively the
  bounded smallint-enum columns. A reviewer adding a new narrowing conversion there must reconsider.
