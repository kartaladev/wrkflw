// Package wrkflw is the documentation landing for the wrkflw workflow engine —
// an importable Go library (not a daemon) that a consumer embeds in their own
// application. This package exports nothing; it exists only as a "start here"
// map of the public packages.
//
// # Architecture in one paragraph
//
// The engine ([engine]) is a pure token state machine: [engine.Step] maps
// (definition, state, trigger) → (commands, new state) with no I/O and no clock
// reads. [runtime.ProcessDriver] is the reference driver that executes commands
// (schedule timers, invoke service actions, create human tasks), persists each
// applied step atomically, and feeds the resulting triggers back through the loop.
// All domain events are written into the transactional outbox alongside the state
// change and relayed at-least-once via a background [persistence] relay.
//
// # Start here
//
// For most consumers the entry sequence is: (1) author a [model.ProcessDefinition]
// with [definition.NewBuilder] (Go) or [definition.NewLoader] (YAML),
// calling [model.DefinitionLoader.Build] in both cases; (2) construct a
// [runtime.ProcessDriver] with [runtime.NewProcessDriver](catalog, store, opts) — all required
// deps are non-nil interfaces; passing nil returns [kernel.ErrNilDependency];
// (3) call [runtime.ProcessDriver.Run] to start an instance and [runtime.ProcessDriver.Deliver]
// to resume it after a human-task claim, timer fire, or signal.
//
//   - definition   Define a process: nodes, gateways, sequence flows, the
//     ProcessDefinition template. Pure data plus validation; imports only stdlib.
//     Two builder surfaces: DefinitionBuilder (NewBuilder, Go-authored)
//     and DefinitionLoader (NewLoader, post-parse action registration).
//   - runtime      Run a process: the reference driver that performs engine
//     commands, persists state, and feeds triggers back. Provides Runner,
//     MemStore, CachingStore, TaskService, SignalBus, Chainer, CallNotifier.
//     All stateful constructors return (T, error) and reject nil required deps.
//   - engine       The core token state machine. Pure of transport, storage
//     vendor, and event-bus specifics; depends on interfaces only. Reach for
//     this package directly only when writing deterministic unit tests of
//     process logic or building a custom execution layer.
//
// # Activities and people
//
//   - action       The service-action catalog: named, interface-based actions
//     referenced from definition nodes. Provides MapCatalog, Registry, Func
//     adapter, and retry-contract helpers (NonRetryable, IsRetryable).
//     Subpackages: httpcall, email, transform, logaction.
//   - humantask    Human-task model and the ports that drive human work (claim,
//     complete, reassign). MemTaskStore for tests; wire a SQL-backed store for
//     production.
//
// # Authorization
//
//   - authz        The pluggable Authorizer abstraction (role, resource, and
//     attribute-based) evaluated at human-task nodes. Implement this interface
//     to integrate any authorization backend.
//   - casbinauthz  The consumer-facing façade for the casbin-backed authorizer.
//     Single constructor: NewCasbinAuthorizer(opts…) — exactly one source option
//     (FromEnforcer, FromStrings, or FromDB) required.
//
// # Expose it (mount in your server)
//
//   - transport    REST http.Handler factories and gRPC service registrations
//     a consumer mounts in their own server (transport/rest,
//     transport/grpc). Transport adapters are thin translators over service.Service;
//     the engine core never imports them.
//   - service      The application-layer Service façade consumed by transports:
//     StartInstance, GetInstance, ClaimTask, CompleteTask, ResolveIncident, etc.
//     Also defines optional admin ports (DeadLetterAdmin, TimerAdmin, LineageAdmin).
//
// # Supporting ports and façades
//
//   - persistence  The persistence façade over the neutral SQL store: OpenPostgres,
//     OpenMySQL, and OpenSQLite backends (Postgres/MySQL/SQLite dialects, ADR-0081/0082).
//     Provides Store, Relay, CallLinkStore, TimerStore, ChainLinkStore, Lister,
//     DefinitionStore, and their constructors.
//   - eventing     The eventing façade for publishing domain events (outbox).
//     Keeps watermill confined: runtime/engine never import it. Provides
//     NewGoChannelPublisher, NewMessageHandler, NewChainerRunner.
//   - scheduling   The façade over the timer/deadline scheduler (gocron v2 behind
//     the abstraction). Provides Scheduler, MemScheduler for tests.
//   - observability Metrics, traces, and slog wiring at the runtime boundary.
//   - clock        The clock.Clock time abstraction; supply clock.System() in
//     production; inject a fake clock (clockwork.NewFakeClock) in tests.
//     Engine and runtime never read the wall clock directly.
//
// Implementation details a consumer must not import live under internal/.
// Reference wiring examples live under examples/.
package wrkflw
