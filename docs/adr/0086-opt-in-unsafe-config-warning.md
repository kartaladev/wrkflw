# 86. Opt-in unsafe-config warning via `WarnUnsafeConfig`

- Status: Accepted
- Date: 2026-07-03

## Context

Several wrkflw configuration items have safe-for-development defaults but produce
correctness or capacity failures in production when left unconfigured:

1. **Call-link lease in multi-replica deployments.** When more than one engine replica runs
   concurrently and call activities are in use, only one replica must act on each completed
   child notification. Without an advisory-lock ownership wiring (`NewAdvisoryLockOwnership`),
   every replica races to notify the parent, potentially delivering the child result more than
   once and forking the parent process into an invalid state.
2. **`WithHistoryCap` on the instance store.** The inline snapshot history grows with every
   node visit. Without a cap, long-running or loop-heavy processes produce ever-growing JSONB
   rows, causing TOAST bloat, autovacuum stalls, and degraded index scans.
3. **Consumer-owned pruning job.** The library never deletes rows. Four tables
   (`wrkflw_outbox`, `wrkflw_processed_message`, `wrkflw_call_links`, `wrkflw_chain_links`)
   grow without bound until disk pressure, relay slowdown, or dedup-scan degradation occurs.

Each item is intentionally opt-in: forcing it at construction time would break legitimate
single-node or test setups. The library does not know the consumer's deployment topology
(number of replicas, whether call activities are used, whether a pruner is already
scheduled externally).

A pure documentation approach risks being overlooked at initial embedding time.

## Decision

We will provide:

1. **`docs/production-checklist.md`** — a standalone checklist with concrete failure-mode
   descriptions for each item, copy-pasteable configuration snippets, and a reference to
   `docs/retention.md` for pruning details.
2. **`persistence.DeploymentProfile`** — a plain struct the consumer populates to declare
   their own deployment topology. The library does not introspect the live system.
3. **`persistence.WarnUnsafeConfig(logger *slog.Logger, p DeploymentProfile)`** — a
   consumer-invoked function, called once at startup, that emits one `slog.Warn` per
   known-risky combination found in the profile. It is a no-op for a fully safe profile and
   never panics on a nil logger (falls back to `slog.Default()`). It does not inspect live
   infrastructure; it warns based solely on what the consumer declares.
4. **Exported message constants** (`WarnMsgCallLinkLease`, `WarnMsgHistoryCap`,
   `WarnMsgPruning`) so consumers and tests can match on warning text without string
   literals.

The function is never called automatically by any constructor or option. It is a reminder
tool; the consumer opts in by calling it.

## Consequences

### What becomes easier

- A consumer embedding wrkflw for the first time gets a machine-readable, startup-time
  checklist that flags missed items in their log output without reading documentation.
- Tests can match on exported constants rather than fragile string literals.
- The production checklist document becomes the single authoritative reference for
  operational correctness; cross-links from `README.md` and `docs/retention.md` direct
  operators to it.

### What becomes harder

- The function is opt-in. A consumer who never calls `WarnUnsafeConfig` gets no warning.
  This is a deliberate trade-off (see "Rejected alternative" below).
- The consumer must accurately self-describe their topology in `DeploymentProfile`. A
  misreported profile (e.g. `MultiReplica: false` when replicas > 1) produces false
  silences. No automated verification is possible from the library side.

### Rejected alternative: automatic constructor-time `slog.Warn`

The obvious alternative is to emit warnings from `OpenPostgres`, `OpenMySQL`, or
`OpenSQLite` whenever a risky combination is detected. This was rejected because:

- **The library cannot know deployment topology.** At construction time the store does not
  know how many replicas will run, whether call activities will be used, or whether a
  pruning cron already exists outside the current process. A store constructed in a unit
  test with a single in-process connection looks identical from the constructor's
  perspective to one constructed in a 10-replica fleet.
- **False-positive noise.** Automatic warnings on every single-node or test instantiation
  pollute logs in contexts where the "unsafe" configuration is entirely correct. Library
  code that logs `WARN` on normal usage breaks the principle of least surprise and erodes
  log signal for operators.
- **Coupling.** Wiring topology knowledge into a low-level store constructor couples layers
  that should be independent. The store concerns itself with persistence; deployment shape
  is a consumer-level concern.
