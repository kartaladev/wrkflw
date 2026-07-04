# 0094 — HTTP-only transport; remove gRPC

- **Status:** Accepted
- **Date:** 2026-07-05

## Context

`wrkflw` shipped two transport surfaces:

- `transport/rest/` — a stdlib `net/http` handler factory (`NewHandler`).
- `transport/grpc/` — a gRPC service registration helper
  (`RegisterWorkflowServiceServer`) backed by generated protobuf stubs in
  `transport/grpc/workflowpb/` plus `buf.gen.yaml`.

Two pressures drove a re-evaluation:

**Maintenance surface.** Every new feature had to be expressed on both
surfaces: REST and gRPC handlers, REST and gRPC tests, REST and gRPC error
mapping, REST and gRPC observability. ADRs 0051, 0058, and 0062 each closed a
gap that existed because gRPC validation/auth/error-detail parity with REST had
been deferred. This duplication will grow with every future endpoint.

**Dependency weight.** gRPC pulls three heavyweight transitive sub-trees:
`google.golang.org/grpc`, `google.golang.org/protobuf`, and
`google.golang.org/genproto/googleapis/rpc`. A stdlib-only consumer who
imports `transport/rest` (or, after ADR-0095, `transport/http/stdlib`) still
carried the entire gRPC + protobuf tree in their module graph as long as
`transport/grpc` existed in the same module. Pre-v1.0 is the right time to
remove it.

**Consumer demand.** The target consumers are service authors who embed the
engine in their own application. None of the target consumers have required
gRPC — HTTP is universal and the service.Service façade (ADR-0011) keeps both
transports parity-equivalent anyway.

**Generated code in the repo.** The committed `workflowpb/*.pb.go` /
`*_grpc.pb.go` needed to be re-generated and re-committed whenever the `.proto`
changed, which adds a contributor step and a stale-detection CI job that is
overhead with no consumer upside.

The concurrent HTTP refactor (ADR-0095) re-expressed `transport/rest` as three
composable native-framework adapters (`transport/http/{stdlib,gin,fiber}`) over
a shared `transport/http/httpcore` root, which makes gRPC the only remaining
holdout with no composable equivalent to maintain.

## Decision

We remove gRPC from the module entirely. HTTP is the sole transport surface
going forward.

Concretely:

- **`transport/grpc/` is deleted** — the service implementation, the generated
  `workflowpb/` stubs, the `.proto` source, `buf.gen.yaml`, and the
  `//go:generate` directive.
- **`google.golang.org/grpc`, `google.golang.org/protobuf`, and
  `google.golang.org/genproto/googleapis/rpc`** are removed from `go.mod` and
  `go.sum` (via `go mod tidy` after deletion).
- The `golangci-lint` protobuf exclusions for `workflowpb` are dropped from
  `.golangci.yml`.
- The `grpc-protobuf` update group is dropped from `.github/dependabot.yml`.
- The `"./transport/grpc/..."` confinement target in
  `internal/authz/casbin/confinement_test.go` is removed.
- Documentation (`doc.go`, `README.md`, `CHANGELOG.md`) is updated to remove
  all gRPC references.

The HTTP transport refactor (ADR-0095) is the replacement: it provides a
fully composable, multi-framework `transport/http/{httpcore,stdlib,gin,fiber}`
surface that replaces both the old `transport/rest` and `transport/grpc` paths.

The non-transport seams introduced alongside the gRPC transport survive
unchanged: `service.DeadLetterAdmin`, `service.PolicyAdmin`,
`service.RelayStatsAdmin`, `service.TimerAdmin`, `service.LineageAdmin`, and
`service.ResolveIncident` are transport-neutral interfaces and remain part of
the public API.

## Consequences

**Positive**

- The module graph loses the grpc/protobuf/genproto sub-trees. A stdlib consumer
  who imports `transport/http/stdlib` pulls no third-party transport dependency.
  A gin consumer adds only gin; a fiber consumer adds only fiber (ADR-0095).
- Every future endpoint is written and tested exactly once in `httpcore`, then
  adapted per framework — no parallel gRPC handler to maintain.
- The generated-code commit step is eliminated; there is no protobuf to
  regenerate.
- Pre-1.0 breakage: the public API is not yet stable; removing the gRPC surface
  now costs less than removing it after a v1.0 tag.

**Negative / trade-offs**

- Consumers who currently use `RegisterWorkflowServiceServer` must migrate to
  the HTTP surface. The admin seams (`DeadLetterAdmin`, etc.) are unchanged;
  only the gRPC wire encoding must be replaced.
- If a future consumer requires gRPC they will need to write their own adapter
  over `service.Service` — the pure-façade design (ADR-0011) makes this
  straightforward (a thin delegate), but it will not be provided by this module.
- buf/protoc tooling installed for gRPC stub generation is no longer needed.

## References

- Spec: `docs/specs/2026-07-04-http-only-transport-refactor.md`
- Supersedes the gRPC portions of ADR-0011 (REST+gRPC transports), ADR-0051
  (fail-closed `NewSecureServer`), ADR-0058 (gRPC validation sweep + per-method
  auth), ADR-0062 (structured `InvalidArgument` details + `NewMethodAuthInterceptor`),
  and the gRPC `ResolveIncident` RPC portion of ADR-0029.
- Replaced by: ADR-0095 (composable multi-framework HTTP adapters).
