// Package wrkflw is the documentation landing for the wrkflw workflow engine —
// an importable Go library (not a daemon) that a consumer embeds in their own
// application. This package exports nothing; it exists only as a "start here"
// map of the public packages. See REQUIREMENTS.md for the full intent.
//
// # Start here
//
//   - model        Define a process: nodes, gateways, sequence flows, the
//                  ProcessDefinition template. Pure data plus validation.
//   - runtime      Run a process: the reference driver that performs engine
//                  commands and resolves definitions.
//   - engine       The core token state machine. Pure of transport, storage
//                  vendor, and event-bus specifics; depends on interfaces only.
//
// # Activities and people
//
//   - action       The service-action catalog: named, interface-based actions
//                  referenced from definition nodes.
//   - humantask    Human-task model and the ports that drive human work.
//
// # Authorization
//
//   - authz        The pluggable Authorizer abstraction (role, resource, and
//                  attribute-based) evaluated at human-task nodes.
//   - casbinauthz  The consumer-facing façade for the casbin-backed authorizer.
//
// # Expose it (mount in your server)
//
//   - transport    REST http.Handler factories and gRPC service registrations
//                  a consumer mounts in their own server (transport/rest,
//                  transport/grpc).
//
// # Supporting ports and façades
//
//   - persistence  The persistence façade over the SQL/Postgres store.
//   - eventing     The eventing façade for publishing domain events (outbox).
//   - scheduling   The façade over the timer/SLA scheduler.
//   - observability Metrics, traces, and slog wiring at the runtime boundary.
//   - clock        The clock.Clock time abstraction; supply a fake in tests.
//   - service      The service facade and error classification.
//
// Implementation details a consumer must not import live under internal/.
package wrkflw
