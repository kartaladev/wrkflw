# Production-Readiness Backlog — 2026-06-30

Source: deliberate cross-checked audit (6 parallel code audits + manual verification) of the
`wrkflw` engine on `main` at `a933ef4`. The **engine core is production-grade** (token execution,
compensation, retry/DLQ, dual Postgres/MySQL backends, transports, authz, eventing, observability
primitives — 75 ADRs, all merged). The gaps below are the **shell around the engine**: release/CI
plumbing, operational visibility, broker reach, and consumer-facing convenience. None require an
architectural redesign; each is an additive track in the usual cadence
(brainstorm → spec → ADR → plan → branch → SDD → opus review → merge). **Next free ADR: 0076.**

Two audit false-positives were caught and excluded: `STABILITY.md` **does exist** (added ADR-0055);
the README prose (lines 26–29) **does** disclaim BPMN compatibility — only a stray feature-table row
(line 210, "BPMN2 XML | loadable") contradicts it.

Each item: **what's missing · evidence (file ref) · why it matters**. Severity drives ordering.

---

## 🔴 P0 — Blocks "production-ready" / "released"

### P0-1 — No CI pipeline ✅ IN PROGRESS (release-foundation track, branch `chore/release-foundation`)
- **Missing:** any `.github/workflows/`, Makefile, or automated gate.
- **Why:** zero automated test/race/lint/coverage/vuln gate. The HANDOVER repeatedly calls CI
  "the only intentionally-deferred item, highest-value."
- **Scope:** GitHub Actions — `go build`, `go test -race` (Docker-capable runner for testcontainers),
  coverage gate, `golangci-lint`, `govulncheck`, CodeQL, dependency-review on PRs.

### P0-2 — No release/legal foundation ✅ IN PROGRESS (release-foundation track)
- **Missing:** `LICENSE`, `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, git tags / semver.
- **Evidence:** `git tag` empty; no license file tracked.
- **Why:** an importable module with **no license is legally unusable** by consumers; with no semver
  they must pin `main` forever and get no breaking-change signal.

### P0-3 — No concrete broker adapters (eventing is in-process only)
- **Missing:** only `eventing.NewGoChannelPublisher` ships (`eventing/eventing.go:64`).
- **Why:** defeats the stated "no watermill lock-in" goal — reaching Kafka/NATS/SNS forces a consumer
  to import watermill directly. Cross-process eventing is not usable out of the box.
- **Scope:** thin `eventing.NewKafkaPublisher` / `NewNATSPublisher` / `NewSNSPublisher` constructors
  (or, at minimum, a prominently-documented `eventing.NewPublisher(watermillPub)` wrapping pattern +
  partition-key-by-`instance_id` guidance for ordering).

### P0-4 — Unbounded HTTP response buffering in httpcall ✅ DONE (ADR-0076, branch `feat/action-safety-limits`)
- **Evidence:** `action/httpcall/httpcall.go:289` — `io.ReadAll(resp.Body)` with no cap.
- **Why:** a large/malicious upstream OOMs the replica. Add `io.LimitReader` + `WithMaxResponseSize`.
- **Shipped:** `httpcall.WithMaxResponseSize(n)` (default 10 MiB, `n<=0` unlimited) + `ErrBodyTooLarge`.

### P0-5 — No action-execution timeout ✅ DONE (ADR-0076, branch `feat/action-safety-limits`)
- **Evidence:** `runtime/runner.go:727` `safeActionDo` recovers panics only; action ctx carries no deadline.
- **Why:** one hung action (blocking HTTP/SMTP) stalls the instance and ties up goroutines/connections
  indefinitely. Add a `WithActionTimeout` option that wraps the action context.
- **Shipped:** `runtime.WithActionTimeout(d)` (default-on 30s, `d<=0` disables) wrapping both
  safeActionDo sites via `context.WithTimeout`.

---

## 🟠 P1 — Required for real operations & scale

### P1-A — Ops visibility (weakest cluster; highest operational ROI)
- **Missing SLI metrics:** DLQ depth, outbox pending-backlog, action-failure counter, timer-fire count.
  - Evidence: `runtime/observability.go`, `internal/persistence/postgres/relay.go` emit duration/published
    counts but none of the above.
- **No relay/scheduler health probes:** `/readyz` returns ready when the DB is up even if the relay is
  backlogged or timer leadership is lost (`transport/rest/health.go` + `persistence/health.go` cover DB ping only).
- **No shipped dashboards/alerts/runbooks:** no `docs/dashboards/`, no Prometheus alert rules, no
  `docs/runbooks/`. Operators reinvent every threshold and escalation path.
- **Admin API can't drill down:** no relay-stats / timer-visibility / parent-child lineage endpoints;
  DLQ rows expose only a raw `LastError` string with no failure categorization (`service/deadletter.go`).
- **Minor:** `TaskService` is meter-only, no logging/spans (`runtime/taskservice.go`); no secret redaction
  in slog; relay empty-drain logs at debug.

### P1-B — Transport hardening
- **gRPC admin RPCs ungated by default** while REST defaults to deny-all — asymmetric, easy to expose
  unauthenticated (`transport/grpc/server.go:34-61`). Either ship a default-deny interceptor or make
  `NewSecureServer` the only registration path.
- **REST 500s leak `err.Error()`** to clients (`transport/rest/errors.go:28`) — return a generic message,
  log detail server-side only.
- **Missing middleware:** request-ID/correlation propagation, rate limiting, context deadlines, CORS,
  panic recovery. None built-in or shown in `examples/`.
- gRPC has no health-check service; no server-hardening options (MaxConcurrentStreams, keepalive).
- ListInstances filtering is status-only — no date-range / definition-id / incident-count filters.

### P1-C — Persistence / migration ops
- **Postgres (9 incremental migrations) vs MySQL (2 bundled)** diverge — different index strategies
  (partial vs full), no 1:1 mapping, future drift risk
  (`internal/persistence/{postgres,mysql}/migrations/`).
- **No migration-version introspection API, no rollback, no standalone migration CLI**
  (`persistence/persistence.go:170`, `*/migrate.go` call `Up` only).
- **Connection pool / statement timeout / isolation level** fully delegated to consumer with no guidance.
- **Opt-in-but-silently-unsafe-if-forgotten:** call-link lease (multi-replica exactly-once),
  `WithHistoryCap` (JSONB bloat), pruning cron (unbounded growth). Document as production MUST-DOs; consider
  safer defaults or startup warnings.
- MySQL `Pruner.PruneTimers` is not in the public `Pruner` interface (`persistence/pruner.go` vs
  `internal/persistence/mysql/pruner.go:115`) — MySQL timer rows leak if consumer relies on the interface.

### P1-D — Lint / security baseline ✅ DONE (ADR-0077, branch `feat/action-safety-limits`)
- `.golangci.yml` runs only the `standard` set — **add `gosec`, `bodyclose`, `errorlint`** (then fix the
  findings they surface; this is its own branch because it will not be clean on first run).
- **Shipped:** three linters enabled, output uncapped, all findings triaged to zero (errorlint `%w`
  fixes; documented gosec nolints/exclusions). bodyclose 0 findings. Expr-timeout doc + mysql LIMIT
  validation items below remain open.
- Expr-eval DoS timeout is opt-in; document that untrusted-definition consumers MUST inject a
  timeout-capable evaluator (`internal/expreval/expreval.go:24`, ADR-0049/0056).
- Validate `batch`/`limit`/`fetch` > 0 in MySQL constructors that format `LIMIT %d`
  (`internal/persistence/mysql/{relay,call_links,lister}.go`) — currently safe (int-only) but undefended.

---

## 🟡 P2 — Convenience / developer experience

### Missing capabilities (consumers hit these immediately)
- **No BPMN2 XML loader** — REQUIREMENTS §1 expects it; only YAML + Go builder exist. Either implement a
  basic loader or fix the contradictory README table row (line 210).
- **Thin built-in action catalog** — only http/email/transform/log. Realistic consumers need:
  **delay/sleep, DB query, gRPC call, Kafka/webhook publish, Slack, sub-workflow start-and-wait,
  script/expr** actions. Plus resilience wrappers (timeout/circuit-breaker/rate-limit `ServiceAction` decorators).
- **No consumer test harness** — only `MemStore` is exported. Ship fakes (action-catalog spy, fake
  authorizer, fake email sender) + a **`DriveToCompletion(ctx, runner, def, id)` helper** so consumers can
  unit-test definitions without hand-rolling delivery loops.
- **No definition validation CLI / structured lint API** — `model.Validate` returns a joined error string;
  CI can't consume it cleanly. Add `ValidationResult{severity, node, message}` + a `validate` CLI.
- **No YAML serialization** — `model/yaml.go` `ParseYAML` is read-only; can't round-trip a Go-built
  definition back to YAML for review / source control.
- **No definition versioning / hot-redeploy / instance-migration** story (REQUIREMENTS §21 "minimize
  migration effort" from the v1 engine), and **no v1 migration guide**.
- **No admin UI** — REST/gRPC admin routes only; operators use curl.

### Ergonomic rough edges
- Builder conflict errors (`ErrActionInlineAndNameConflict`) don't name the offending node.
- No godoc `Example` on the core `runtime.Runner`.
- `examples/scenarios/` cover ~half of the 19 node kinds (missing Terminate/Error end events, ReceiveTask,
  SendTask, BusinessRuleTask, IntermediateThrow+compensate, non-interrupting EventSubProcess).
- Observability wiring lives only in a test, not the README; no definition-storage best-practices doc.

---

## Recommended sequencing

1. **Release foundation** (P0-1, P0-2) — CI + LICENSE + CHANGELOG + Dependabot + first `v0.x` tag.
   Small, self-contained, unblocks everything. **← started 2026-06-30.**
2. **Safety quick-wins** (P0-4, P0-5, P1-D) — `io.LimitReader`, action timeout, stricter linters + fixes.
3. **Ops visibility** (P1-A) — missing metrics + relay/scheduler health probes + admin drill-down +
   reference Grafana dashboard & runbooks. Highest operational ROI.
4. **Broker reach** (P0-3) — thin broker publisher constructors / documented wrapping.
5. **DX/convenience** (P2) — test harness + `DriveToCompletion`, structured validation + CLI, more
   built-in actions, BPMN2 loader (or drop the claim).

Notes:
- CI should run on the **current** (clean) linter set so it's green from day one; the stricter linters
  (P1-D) land in their own branch with the findings fixed.
- Tagging is outward-facing — confirm the version/string with the maintainer before creating/pushing a tag.
