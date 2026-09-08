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
// [runtime.ProcessDriver] with [runtime.NewProcessDriver] — zero arguments give
// an in-memory driver backed by [action.DefaultCatalog] and
// [kernel.NewMemInstanceStore]; supply [runtime.WithActionCatalog] and
// [runtime.WithInstanceStore] for durable production wiring;
// (3) call [runtime.ProcessDriver.Drive] to start an instance and [runtime.ProcessDriver.ApplyTrigger]
// to resume it after a human-task claim, timer fire, or signal.
//
//   - definition   Define a process: nodes, gateways, sequence flows, the
//     ProcessDefinition template. Pure data plus validation; imports only stdlib.
//     Two builder surfaces: DefinitionBuilder (NewBuilder, Go-authored)
//     and DefinitionLoader (NewLoader, post-parse action registration).
//   - runtime      Run a process: the reference driver that performs engine
//     commands, persists state, and feeds triggers back. Provides ProcessDriver,
//     MemInstanceStore, TaskService, SignalBus, Chainer, CallNotifier.
//     All stateful constructors return (T, error) and reject nil required deps.
//   - engine       The core token state machine. Pure of transport, storage
//     vendor, and event-bus specifics; depends on interfaces only. Reach for
//     this package directly only when writing deterministic unit tests of
//     process logic or building a custom execution layer.
//
// # Activities and people
//
//   - action       The service-action catalog: named, interface-based actions
//     referenced from definition nodes. Provides DefaultCatalog, MapCatalog,
//     Registry, ActionFunc adapter, and retry-contract helpers (NonRetryable,
//     IsRetryable). Subpackages: httpcall, email, transform, logaction.
//   - humantask    Human-task model and the ports that drive human work (claim,
//     complete, reassign). MemTaskStore for tests; wire a SQL-backed store for
//     production.
//
// # Trust boundary
//
// wrkflw authenticates nobody. Every HTTP route group this library mounts runs
// with whatever identity the surrounding application has already established.
// The boundary is the router group you mount onto, and securing it is the
// consumer's job:
//
//   - Authentication is always yours. Mount the route groups from
//     transport/http/stdlib, transport/http/gin or transport/http/fiber onto a
//     group your own middleware has already authenticated. The library supplies
//     no default and no opt-out; the SECURITY note in the godoc of each
//     route-group type says what a caller reaching it could do. Only TaskRoutes
//     checks that an identity exists at all — it resolves the configured
//     RequestActorFunc and fails closed with 401 or 503. The instance and admin
//     routes DO resolve it, on every request, but only to decide how much to
//     disclose; they never refuse. The message and health routes never ask.
//   - RequestActorFunc answers who, never may. It names the caller and grants
//     nothing. Returning a well-formed actor from it is not an authorization
//     decision, and no route consults it to decide whether an operation is
//     permitted on a particular instance.
//   - Identity LIFTS the redaction on instance reads; it does not gate them.
//     This is the most counter-intuitive thing in this section. The reads apply
//     a projection with TWO inputs — the disclosure set you configured, and
//     whether the transport could identify the caller — and no authorization
//     step on either. Under the default, empty set: an UNIDENTIFIED caller
//     receives a structural skeleton (IDs, status, timestamps, history, token
//     and task state), and an IDENTIFIED caller receives everything,
//     unprojected — variables, start variables, scopes, incidents, compensation
//     records, and each task's claim, completion and candidates. Identity here
//     means the consumer's own RequestActorFunc returned a non-empty Actor.ID.
//     See [github.com/kartaladev/wrkflw/transport/http/httpcore.DisclosingMapper].
//   - The disclosure set widens the projection for EVERYONE, identified or not.
//     Every category you add is disclosed to unidentified callers too: configure
//     [github.com/kartaladev/wrkflw/authz.DiscloseVariables] and any caller
//     reaching the route receives process variables, scopes and compensation
//     records without ever being identified.
//     [github.com/kartaladev/wrkflw/authz.DiscloseAll] goes further — it
//     short-circuits identity resolution entirely, so RequestActorFunc is never
//     called and every caller is treated as identified. The default is the empty
//     set, which discloses nothing beyond the skeleton, and nothing in this
//     library adds to it for you.
//   - There is no per-instance access control, so lifting the redaction lifts it
//     for EVERY instance. GetInstance, the snapshot read and the actionable read
//     take an instance ID and no actor. Mounting behind auth middleware — which
//     is what this section tells you to do — therefore satisfies the identity
//     check for every logged-in caller and hands each of them the full state of
//     ANY instance ID they can name, across tenants. The library has no owner or
//     tenant concept; enforce ownership yourself if you need one.
//   - "Identity" means two different things here, deliberately. The human-task
//     guard refuses only the wholly zero actor, so the kiosk claimant — an actor
//     carrying roles but no ID — may claim and complete tasks. The disclosure
//     decision is stricter and requires a non-empty Actor.ID, so that same
//     caller is UNIDENTIFIED for reads and receives the projection. An actor can
//     therefore act without being able to see.
//   - Instance IDs are identifiers, not secrets. The default generator
//     ([github.com/kartaladev/wrkflw/runtime/idgen.XID]) is time- and
//     counter-ordered, so a caller holding one ID can derive its neighbours —
//     and holding an ID is the only thing the read routes require.
//     Enumerability is by design and is not a protection; put the access control
//     in your middleware.
//   - Authorization inside the library reaches human tasks only. An
//     [github.com/kartaladev/wrkflw/authz.Authorizer] is consulted by
//     [github.com/kartaladev/wrkflw/runtime/task.TaskService] in Claim,
//     Complete, Reassign and RefreshCandidates, against the task's eligibility
//     spec. Nothing else authorizes. The disclosure decision above is the one
//     the transport itself makes, and it governs what a caller may SEE, never
//     what it may DO.
//   - Admin routes are only partly default-absent. Four register
//     unconditionally: GET /admin/instances, the incident-resolve route,
//     POST /admin/instances/{id}/compensation/resolve-stall — which takes a
//     required body and mutates the instance — and
//     POST /admin/instances/{id}/cancel. Only the routes behind the optional
//     DeadLetters, Policies, RelayStats, Timers and Lineage fields stay absent
//     while their field is nil. For the four that are always there, absence is
//     not the protection — AdminRoutes has no authentication of its own.
//
// # Authorization
//
//   - authz        The pluggable Authorizer abstraction (role, resource, and
//     attribute-based). It is evaluated in the human-task service, not in the
//     transports or the engine: runtime/task.TaskService calls it from Claim,
//     Complete, Reassign and RefreshCandidates, and those four are the only
//     calls the library makes. See the Trust boundary section above for what
//     that does and does not protect. Implement this interface to integrate any
//     authorization backend.
//   - casbinauthz  The consumer-facing façade for the casbin-backed authorizer.
//     Single constructor: NewCasbinAuthorizer(opts…) — exactly one source option
//     (FromEnforcer, FromStrings, or FromDB) required.
//
// # Expose it (mount in your server)
//
//   - transport    HTTP transport adapters — pick the subpackage for your framework:
//     transport/http/stdlib (net/http *ServeMux), transport/http/gin (gin.IRouter),
//     transport/http/fiber (fiber.Router). Shared logic lives in transport/http/httpcore:
//     pure-endpoint funcs, DTOs (validated via go-playground/validator/v10),
//     ClassifyError (5xx redaction), Instrumentation.Observe (static route template),
//     and the RouteCustomizer[R] / CustomizeOption[R] generic seam.
//     Mount AdminRoutes on a consumer-secured group; see the Trust boundary
//     section above for which of its routes are default-absent and which are not.
//     The engine core never imports transport packages.
//   - service      The application-layer Service façade consumed by transports:
//     StartInstance, GetInstance, ClaimTask, CompleteTask, ResolveIncident, etc.
//     Also defines optional admin ports (DeadLetterAdmin, TimerAdmin, LineageAdmin).
//
// # Supporting ports and façades
//
//   - persistence  The persistence façade over the neutral SQL store: OpenPostgres,
//     OpenMySQL, and OpenSQLite backends (Postgres/MySQL/SQLite dialects).
//     Provides InstanceStore, CachingInstanceStore, CachingTaskStore, Relay, CallLinkStore,
//     TimerStore, ChainLinkStore, Lister, DefinitionStore, and their constructors.
//     Hot-path caching is default-on on the DurableProvider constructors.
//   - persistence/cache  Neutral cache port: Cache, ValueCache, Provider, Codec[V].
//     Four swappable adapter subpackages: persistence/cache/hotcache (samber/hot, default),
//     persistence/cache/ottercache (maypok86/otter, in-memory), persistence/cache/rediscache
//     (go-redis, distributed), persistence/cache/memcache (gomemcache, distributed).
//     Each adapter is an optional dependency imported only by its subpackage.
//   - eventing     The eventing façade for publishing domain events (outbox) and
//     consuming them back. No messaging library is imported anywhere in the tree:
//     the seams are eventing.PublishFunc and eventing.Handler over an
//     eventing.Envelope, so a consumer supplies their own broker client. Provides
//     NewPublisher, NewInProcess, NewMessageHandler, NewChainerRunner.
//   - scheduler    The façade over the timer/deadline scheduler (gocron v2 behind
//     the abstraction). Provides the gocron-backed Scheduler; the in-memory
//     MemScheduler test double lives in the processtest harness package.
//   - observability Metrics, traces, and slog wiring at the runtime boundary.
//   - (clockwork)  Time abstraction: [github.com/jonboulle/clockwork.Clock] is
//     injected directly into every stateful component — no local wrapper
//     package. Default: clockwork.NewRealClock(); inject a fake clock
//     (clockwork.NewFakeClock) in tests. Engine and runtime never read the wall
//     clock directly.
//
// Implementation details a consumer must not import live under internal/.
// Reference wiring examples live under examples/.
package wrkflw
