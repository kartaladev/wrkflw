# action

Package `action` defines the **service-action catalog** — the interface a service
task invokes by name, the registries that resolve names to implementations, and
the retry contract that classifies action errors. Its `action/*` subpackages ship
a small set of dependency-free built-in actions (HTTP, email, transform, log).

Import path: `github.com/zakyalvan/krtlwrkflw/action`

## Contents

1. [The `Action` interface](#the-action-interface)
2. [Catalogs and registration](#catalogs-and-registration)
3. [Retry contract](#retry-contract)
4. [Built-in actions](#built-in-actions)
   - [`action/httpcall`](#actionhttpcall)
   - [`action/email`](#actionemail)
   - [`action/transform`](#actiontransform)
   - [`action/logaction`](#actionlogaction)

---

## The `Action` interface

A service task references an action **by name**; the runtime resolves that name to
a `Action` and calls `Do`:

```go
type Action interface {
    Do(ctx context.Context, in map[string]any) (out map[string]any, err error)
}
```

`in` is the current process variables; the returned `out` map is merged back into
the process variables. Use the `ActionFunc` adapter to make a plain function an action:

```go
type Func func(ctx context.Context, in map[string]any) (map[string]any, error)
```

`action.ActionFunc(fn)` satisfies `Action` (its `Do` calls `fn`).

---

## Catalogs and registration

The catalog is split into a read side and a write side:

| Type | Method(s) | Purpose |
|---|---|---|
| `Catalog` | `Resolve(name string) (Action, bool)` | Read side: resolve a name to an action. |
| `Registrar` | `Register(name, a) error`, `RegisterFunc(name, fn) error` | Write side: register actions by name after construction. |

Two implementations ship:

### `MapCatalog` — read-only, map-backed

| Function / method | Signature | Notes |
|---|---|---|
| `NewMapCatalog` | `NewMapCatalog(m map[string]Action) MapCatalog` | Wraps `m`; the caller must not mutate `m` afterward. `nil` map is allowed (empty catalog). |
| `MapCatalog.Resolve` | `Resolve(name) (Action, bool)` | Map lookup. Safe for concurrent reads. |

Use `action.NewMapCatalog(nil)` (not Go `nil`) when constructing a runner for
processes with no service tasks — `runtime.NewProcessDriver` requires a non-nil catalog.

### `Registry` — concurrency-safe, satisfies both `Catalog` and `Registrar`

Post-construction registration guarded by a `sync.RWMutex` (contains a `noCopy` —
never copy a `Registry`; pass `*Registry`). Suitable for dynamic wiring where
actions are registered incrementally after startup.

| Method | Signature | Notes |
|---|---|---|
| `NewRegistry` | `NewRegistry() *Registry` | Empty, ready-to-use registry. |
| `Register` | `Register(name string, a Action) error` | Adds `a` under `name`. **First registration wins** — a duplicate returns `ErrActionExists` and does not overwrite. |
| `RegisterFunc` | `RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error` | Wraps `fn` as a `ActionFunc` and delegates to `Register`; nil `fn` → `ErrNilAction`. |
| `MustRegister` / `MustRegisterFunc` | `MustRegister(name, a)` / `MustRegisterFunc(name, fn)` | Panic-on-error variants for init-time wiring. |
| `Resolve` | `Resolve(name) (Action, bool)` | RLock-guarded lookup. |

**Sentinel errors** (compare with `errors.Is`):

| Sentinel | Meaning |
|---|---|
| `ErrEmptyActionName` | The `name` argument was empty. |
| `ErrNilAction` | A nil `Action` (or nil func) was registered. |
| `ErrActionExists` | A different action is already registered under that name (wrapped with the name). |

(A resolution *miss* is reported by the `bool` return, not an error — there is no `ErrActionNotFound`.)

### `Resolve` — three-tier precedence

```go
func Resolve(scoped, global Catalog, name string) (Action, bool)
```

The runtime resolves actions in three tiers, outermost first:

1. **Inline action** — an `Action` embedded directly in the node via
   `activity.WithAction` or `activity.WithActionFunc`. The engine sets
   `InvokeAction.Inline` when present; the runner calls it directly, bypassing both
   catalogs. No `name` is involved.
2. **Scoped (definition-local) catalog** — a `Catalog` registered on the
   `ProcessDefinition` via `DefinitionBuilder.RegisterAction`. The engine sets
   `InvokeAction.Scoped` when available. Checked first in `action.Resolve`.
3. **Global catalog** — the `action.Catalog` passed to `runtime.NewProcessDriver`. The
   fallback when neither inline nor scoped resolves the name.

`action.Resolve(scoped, global, name)` implements tiers 2 and 3. Either catalog
may be nil (treated as an empty catalog). A total miss across all tiers causes the
runner to surface an action-not-found error as a non-retryable `ActionFailed`.

---

## Retry contract

Action errors are **retryable by default**; an action marks an error permanent by
wrapping it.

| Symbol | Signature | Behaviour |
|---|---|---|
| `Retryabler` | `interface { error; Retryable() bool }` | An error that declares whether the runtime should retry it, overriding the retry-by-default policy. |
| `NonRetryable` | `NonRetryable(err error) error` | Wraps `err` so the runtime will **not** retry. The wrapper unwraps to `err` (so `errors.Is`/`errors.As` see through it); `NonRetryable(nil)` returns `nil`. |
| `IsRetryable` | `IsRetryable(err error) bool` | **Default true**: a nil error and any plain error are retryable. Uses `errors.As` to find a `Retryabler` anywhere in the chain and, if found, returns its `Retryable()`. |

---

## Built-in actions

Each subpackage exposes a `New*` constructor returning an `action.Action`,
configured with functional options. All are dependency-free (standard library +
the in-repo `expr-lang`).

### `action/httpcall`

`NewHTTPCall(opts ...Option) action.Action` — performs one HTTP request per `Do`.

| Option | Effect |
|---|---|
| `WithBaseURL(u string)` | Static request URL. |
| `WithURLExpr(expr string)` | expr-lang expression evaluated against input vars at `Do` time; takes precedence over `WithBaseURL`. |
| `WithMethod(m string)` | HTTP method. Default: POST when a body source is set, else GET. |
| `WithHeader(k, v string)` | Add a static request header (repeatable). |
| `WithHeaderFunc(fn HeaderFunc)` | Programmatic header setter run after static headers (repeatable, registration order). |
| `WithHTTPClient(c *http.Client)` | Custom client. Default: a client with a 30s timeout. |
| `WithBodyKey(k string)` | Input var holding the request body (JSON-encoded). Ignored if `WithBodyFunc` is set. |
| `WithBodyFunc(fn BodyFunc)` | Programmatic body builder; precedence over `WithBodyKey`; does **not** auto-set `Content-Type`. |
| `WithBodyValidator(v BodyValidator)` | Validate the body bytes before send; any error is wrapped `NonRetryable`. A `WithBodyFunc` reader is fully buffered when a validator is set. |
| `WithOutputKeys(status, body, headers string)` | Override the three output keys. |
| `WithMaxResponseSize(n int64)` | Cap the response body (and any buffered request body) read into memory. Default **10 MiB**; `n <= 0` disables; over-cap → `NonRetryable(ErrBodyTooLarge)`. |

**Hook types:**

| Type | Signature |
|---|---|
| `HeaderFunc` | `func(ctx context.Context, h http.Header, vars map[string]any) error` |
| `BodyFunc` | `func(ctx context.Context, vars map[string]any) (io.Reader, error)` |
| `BodyValidator` | `func(ctx context.Context, body []byte, vars map[string]any) error` |

**Output variables** (defaults; override via `WithOutputKeys`):

| Key | Type | Value |
|---|---|---|
| `httpStatus` | `int` | `resp.StatusCode`. |
| `httpBody` | `any` / `string` | Decoded JSON when the response is `application/json`, else the raw string, else `nil` for an empty body. |
| `httpHeaders` | `map[string]string` | Response headers (multi-value collapsed to the first). |

**Retry classification:**

| Condition | Result |
|---|---|
| 4xx **except** 408 and 429 | `NonRetryable` error |
| 408, 429, all 5xx | retryable error |
| transport / timeout error | retryable error |
| status `< 400` | success (output, nil error) |

`ErrBodyTooLarge` is returned (NonRetryable) when a body exceeds the configured cap.

### `action/email`

`NewEmail(opts ...Option) action.Action` — sends one **individual** email per recipient per `Do`.

| Option | Effect |
|---|---|
| `WithSMTPAddr(addr string)` | SMTP server address (`"host:port"`). |
| `WithAuth(user, pass string)` | PLAIN SMTP auth; the host is derived from the SMTP address at send time. |
| `WithFrom(addr string)` | Envelope/From address. |
| `WithTo(addrs ...string)` | Static recipient addresses (each gets an individual message). |
| `WithRecipientResolver(r RecipientResolver)` | Resolver called at send time for additional recipients (appended after `WithTo`). |
| `WithSubjectTemplate(t string)` | Subject as a `text/template`; CRLF in the rendered value → `NonRetryable` (header-injection guard). |
| `WithBodyTemplate(t string)` | Body as a `text/template` over the variables. |
| `WithHTML()` | `Content-Type: text/html` (default `text/plain`). |
| `WithTLS()` | Implicit-TLS mode (`tls.Dial`, e.g. port 465). Mutually exclusive with `WithStartTLS` (last wins). |
| `WithStartTLS()` | STARTTLS mode — enforced; errors if the server does not advertise STARTTLS (no plaintext fallback). |
| `WithTLSConfig(cfg *tls.Config)` | Override the `*tls.Config`; an empty `ServerName` is filled from the SMTP host (the config is cloned). |
| `WithSender(s sender)` | Override the SMTP sender (test seam; highest precedence). The `sender` type is unexported — usable only from within the package's tests via `SenderFunc`. |

**Types:**

| Type | Signature | Purpose |
|---|---|---|
| `Recipient` | `struct { Address string; Data map[string]any }` | One destination plus per-recipient template data overlaid over the instance vars (recipient data wins on conflict). |
| `RecipientResolver` | `func(ctx, vars) ([]Recipient, error)` | Loads recipients at send time; may do I/O; must honour ctx. |

**Behaviour:** static + resolver recipients are combined (static first); each recipient
gets a message rendered against `vars` overlaid with its `Data` (recipients never see
each other); templates use `missingkey=error`. Delivery is best-effort per recipient
(failures collected, loop continues). On full success `Do` returns
`{"emailSent": true, "recipientCount": <n>}`; on any failure it returns a **retryable**
aggregate error and no output (at-least-once — a retry may re-send to already-notified
recipients); zero recipients is a `NonRetryable` error.

### `action/transform`

`NewTransform(opts ...Option) (action.Action, error)` — projects/enriches
variables. **Returns an error** (unlike the others).

| Option | Effect |
|---|---|
| `WithExpr(outKey, exprStr string)` | Evaluate the expr-lang expression against the current variables and project the result under `outKey`. Only `WithExpr` results are returned and persisted. |
| `WithMapper(m Mapper)` | An I/O-capable enrichment stage (`Mapper` = `func(ctx, vars) (map[string]any, error)`). Its outputs merge into the internal env for later stages but are **never** returned/persisted. |

**Scratch-vs-persisted invariant:** `WithMapper` enrichment is action-local scratch —
available for chaining into later stages but never written to `out` or the process
variables. To persist a mapper-fetched field, follow it with a `WithExpr` that projects
it. Expressions are compiled **eagerly**: a malformed expression (or a nil mapper) fails
`NewTransform` at wiring time, not `Do`. Stages run in registration order; `Do` checks
`ctx` before each stage.

### `action/logaction`

`NewLog(opts ...Option) action.Action` — logs selected variables as one
structured `slog` record and passes the variables through unchanged. **Never errors**
(fire-and-forget safe).

| Option | Default | Effect |
|---|---|---|
| `WithLogger(l *slog.Logger)` | `slog.Default()` | The logger to use. |
| `WithLevel(lvl slog.Level)` | `slog.LevelInfo` | The log level. |
| `WithMessage(m string)` | `"workflow action"` | The log message. |
| `WithKeys(keys ...string)` | all variables | Restrict logged variables to the named keys (missing keys are skipped). |
