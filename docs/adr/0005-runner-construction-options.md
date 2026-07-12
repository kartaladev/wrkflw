# 5. NewRunner functional options

- Status: Accepted
- Date: 2026-06-21

## Context

`NewRunner` is the public constructor for the runtime's reference driver. Its
signature grew with each infrastructure plan:

- **Plans 1–3** (engine core, timing port, persistence): 5 positional parameters
  (`cat`, `clk`, `store`, `jnl`, `out`).
- **Plan 4** (human tasks): +3 positional parameters (`resolver`, `tasks`, `authz`)
  → 8 total.
- **Plan 5** (scheduler + timer e2e): +1 (`sched`) → 9 total.

Eight or nine consecutive interface-typed parameters create a *positional swap
hazard*: swapping two adjacent arguments of compatible types compiles silently
but causes a runtime failure. This is especially dangerous for a **public library
constructor** (`github.com/kartaladev/wrkflw/runtime.NewRunner`), where
callers cannot rely on IDE type-checking alone and where a breaking signature
change would force a semver bump.

`CLAUDE.md` makes **library API ergonomics the load-bearing constraint** ("when
a design choice trades library ergonomics for server convenience, library
ergonomics win"). A 9-argument positional constructor is clearly unergonomic,
and every future capability (gocron-backed scheduler, compensation hooks, retry
policies) would add yet another positional argument.

The five *always-required* core ports (catalog, clock, state store, journal,
outbox) are well-understood and stable. The remaining capabilities (human-task
support, scheduler, future additions) are *optional*: many valid process
definitions need neither human tasks nor timers.

## Decision

We will adopt a **required positional + functional options** pattern for
`NewRunner`:

```go
func NewRunner(
    cat   action.Catalog,
    clk   clock.Clock,
    store StateStore,
    jnl   Journal,
    out   OutboxWriter,
    opts  ...Option,
) *Runner
```

where `Option` is `func(*Runner)`. Provided option constructors:

- `WithHumanTasks(resolver humantask.ActorResolver, tasks humantask.TaskStore, az authz.Authorizer) Option`
- `WithScheduler(sched Scheduler) Option`

The five positional parameters remain because they are always required: the
runner cannot make any forward progress without all five. Optional capabilities
are opt-in via explicit named options.

A `Runner` with no optional capabilities wired returns descriptive errors (not
panics) when execution reaches a node that requires a missing capability (e.g.
`"runtime: perform AwaitHuman: no ActorResolver configured"`). This maintains
the fail-fast-with-context contract established in Plan 4.

## Consequences

**Easier:**

- The public constructor signature is stable: adding future capabilities
  (gocron-backed scheduler, compensation hooks, retry policies, observability
  middleware) requires only a new `WithXxx` option, never a breaking positional
  change.
- Positional swap hazards among optional parameters are eliminated: named option
  constructors make the intent explicit at the call site.
- Consumer call sites are self-documenting: `WithHumanTasks(…)` and
  `WithScheduler(…)` make optional capabilities visible without reading the
  constructor signature.
- Simple process runners stay concise: `NewRunner(cat, clk, store, jnl, out)`
  without trailing `nil, nil, nil` noise.

**Harder / trade-offs:**

- Slight indirection: optional fields are set via closures rather than directly
  in a struct literal, adding one abstraction layer.
- Compile-time completeness checking for optional capabilities is lost: a caller
  who forgets `WithScheduler` only gets a runtime error when the process reaches
  a timer node. The fail-fast error message is designed to be descriptive enough
  that the root cause is immediately obvious.
- Existing call sites (all inside this repository's own tests) required a
  one-time migration from the 8-argument form to the options form; external
  consumers on a released v1 would need the same migration (mitigated here
  because the module has not been released publicly with the 8-argument API).
