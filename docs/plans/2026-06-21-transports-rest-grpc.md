# Implementation plan — Transports (REST + gRPC)

Spec: `docs/specs/2026-06-21-transports-rest-grpc-design.md`
ADR: `docs/adr/0011-rest-grpc-transports.md`

## Goal

Ship library-provided, **consumer-mounted** REST and gRPC transports over a
single transport-agnostic `service.Service` façade, plus `ProcessInstance`
response customization and admin/superuser monitoring (keyset-paginated
`GET /admin/instances`). Add the missing instance-enumeration query on the
persistence side (`MemStore` + Postgres + façade) without touching the engine
core. We ship no `main` and no server: the consumer mounts our `http.Handler`
and registers our gRPC service on their own `*grpc.Server` (ADR-0004). The REST
surface lands first (independently shippable, zero new deps); gRPC lands last.

## Architecture

- `service/` — `Service` interface + `*Engine` impl wiring
  `Runner`+`TaskService`+`DefinitionRegistry`+`Store`+`InstanceLister`; resolves
  `*model.ProcessDefinition` by the instance's `DefID:DefVersion`; transport-neutral
  request/result value types.
- `transport/rest/` — `NewHandler(svc, ...opts) http.Handler` (`*http.ServeMux`,
  stdlib routing), `WithInstanceMapper` / `WithAdminMiddleware` options, default
  `InstanceView` DTO, keyset-paginated admin listing, `mapToHTTPError`.
- `transport/grpc/` + committed `transport/grpc/workflowpb/` — `.proto` +
  `buf.gen.yaml` + generated code; `WorkflowServiceServer` impl delegating to the
  façade; `RegisterWorkflowServiceServer(reg, svc)`; `mapToGRPCStatus`.
- New `runtime` ports: `InstanceFilter`, `InstanceSummary`, `InstancePage`,
  `InstanceLister`; implemented on `MemStore` and the Postgres store; exposed via
  the persistence façade. Engine core unchanged.

## Tech Stack

- Go 1.25.7 (per `go.mod`). Module path `github.com/zakyalvan/krtlwrkflw`.
- REST: stdlib `net/http`, `encoding/json`, `encoding/base64`, `errors` — **zero
  new dependencies**.
- gRPC: `google.golang.org/grpc` + `google.golang.org/protobuf` (latest stable).
  buf + `protoc-gen-go` + `protoc-gen-go-grpc` are **dev-tooling**, not `go.mod`
  deps; generated `workflowpb` is **committed**.
- Existing: `github.com/zakyalvan/krtlwrkflw/{engine,runtime,model,humantask,authz,clock,action}`.
- Postgres test: `database.RunTestDatabase` (testcontainers).

## Global Constraints

Go 1.25; REST = stdlib `net/http` ONLY (zero router deps); gRPC =
`google.golang.org/grpc` + `google.golang.org/protobuf` (latest stable), buf for
codegen (dev-tooling), generated `workflowpb` COMMITTED; transports are
consumer-mounted (no shipped binary/main, ADR-0004); NEVER import net/http
server, grpc, or protobuf from engine/runtime/model core (only `service/`+
`transport/`); TDD strict VISIBLE red→green (generated code exempt — its
consumers/impl are tested); black-box tests (`package x_test`); table tests
`assert` closure form (not want/wantErr); `t.Context()`; pair foo.go+foo_test.go;
≥85% coverage on touched packages (generated `workflowpb` excluded from the bar);
conventional commits ending with
`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## Task 1 — Instance enumeration query (persistence)

Add the lister port + types in `runtime`, implement on `MemStore` (filter + sort
+ keyset cursor) and on the Postgres store, expose via the persistence façade.
The engine core is untouched. The opaque-cursor codec lives with the port so both
impls share it.

**Files:**

- `runtime/lister.go` / `runtime/lister_test.go` (port + types + cursor codec)
- `runtime/memstore.go` (extend) / `runtime/memstore_lister_test.go`
- `internal/persistence/postgres/lister.go` / `..._test.go` (impl + testcontainers)
- persistence façade file (extend to surface the lister)

**Interfaces / signatures:**

```go
// runtime/lister.go
type InstanceFilter struct {
    Status *engine.Status
    Limit  int
    Cursor string
}

type InstanceSummary struct {
    InstanceID string
    DefID      string
    DefVersion int
    Status     engine.Status
    StartedAt  time.Time
    EndedAt    *time.Time
}

type InstancePage struct {
    Items      []InstanceSummary
    NextCursor string
    HasMore    bool
}

type InstanceLister interface {
    List(ctx context.Context, filter InstanceFilter) (InstancePage, error)
}
```

### Steps

- [ ] **RED — cursor codec round-trip.** Create `runtime/lister_test.go`. Will not
  compile (`undefined: encodeCursor`). Run `go test ./runtime/...`, observe the
  failure.

  ```go
  package runtime_test

  import (
      "testing"
      "time"

      "github.com/zakyalvan/krtlwrkflw/runtime"
  )

  func TestCursorRoundTrip(t *testing.T) {
      ts := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
      enc := runtime.EncodeCursor(ts, "inst-7")
      gotTS, gotID, err := runtime.DecodeCursor(enc)
      if err != nil {
          t.Fatalf("decode: %v", err)
      }
      if !gotTS.Equal(ts) || gotID != "inst-7" {
          t.Fatalf("round-trip mismatch: got (%v,%q)", gotTS, gotID)
      }
  }

  func TestDecodeCursorRejectsGarbage(t *testing.T) {
      tests := []struct {
          name   string
          cursor string
          assert func(t *testing.T, err error)
      }{
          {
              name:   "not base64",
              cursor: "!!!not-base64!!!",
              assert: func(t *testing.T, err error) {
                  if err == nil {
                      t.Fatal("want error for non-base64 cursor")
                  }
              },
          },
          {
              name:   "base64 but not json",
              cursor: "Zm9vYmFy", // "foobar"
              assert: func(t *testing.T, err error) {
                  if err == nil {
                      t.Fatal("want error for non-json cursor")
                  }
              },
          },
      }
      for _, tc := range tests {
          t.Run(tc.name, func(t *testing.T) {
              _, _, err := runtime.DecodeCursor(tc.cursor)
              tc.assert(t, err)
          })
      }
  }
  ```

- [ ] **GREEN — cursor codec + port types.** Create `runtime/lister.go` with the
  types above and:

  ```go
  // ErrBadCursor is returned by DecodeCursor when the cursor is malformed.
  var ErrBadCursor = errors.New("runtime: malformed instance cursor")

  type cursorPayload struct {
      StartedAt  time.Time `json:"started_at"`
      InstanceID string    `json:"instance_id"`
  }

  // EncodeCursor produces an opaque keyset cursor for keyset pagination.
  func EncodeCursor(startedAt time.Time, instanceID string) string {
      b, _ := json.Marshal(cursorPayload{StartedAt: startedAt, InstanceID: instanceID})
      return base64.URLEncoding.EncodeToString(b)
  }

  // DecodeCursor parses an opaque cursor produced by EncodeCursor.
  func DecodeCursor(cursor string) (time.Time, string, error) {
      raw, err := base64.URLEncoding.DecodeString(cursor)
      if err != nil {
          return time.Time{}, "", fmt.Errorf("%w: %v", ErrBadCursor, err)
      }
      var p cursorPayload
      if err := json.Unmarshal(raw, &p); err != nil {
          return time.Time{}, "", fmt.Errorf("%w: %v", ErrBadCursor, err)
      }
      return p.StartedAt, p.InstanceID, nil
  }

  // normalizeLimit clamps a requested limit to [1, 200] with a default of 50.
  func normalizeLimit(n int) int {
      switch {
      case n <= 0:
          return 50
      case n > 200:
          return 200
      default:
          return n
      }
  }
  ```

  Run `go test ./runtime/...` — green.

- [ ] **RED — MemStore.List.** Create `runtime/memstore_lister_test.go`
  (`package runtime_test`). Seed three instances with distinct `StartedAt` and
  statuses via `Create`. Will fail (`MemStore` has no `List`). Table covers:
  no-filter ordering (StartedAt DESC then InstanceID DESC), status filter,
  `limit=1` → `HasMore=true` + a usable `NextCursor`, and second-page fetch with
  that cursor returning the next item and `HasMore=false`.

  ```go
  func TestMemStoreList(t *testing.T) {
      newState := func(id string, st engine.Status, at time.Time) engine.InstanceState {
          return engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: st, StartedAt: at}
      }
      base := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

      tests := []struct {
          name   string
          filter runtime.InstanceFilter
          seed   func(t *testing.T, ms *runtime.MemStore)
          assert func(t *testing.T, page runtime.InstancePage)
      }{
          {
              name:   "orders by started_at desc",
              filter: runtime.InstanceFilter{},
              seed: func(t *testing.T, ms *runtime.MemStore) {
                  for i, id := range []string{"a", "b", "c"} {
                      _, err := ms.Create(t.Context(), runtime.AppliedStep{
                          State: newState(id, engine.StatusRunning, base.Add(time.Duration(i)*time.Minute)),
                      })
                      if err != nil {
                          t.Fatal(err)
                      }
                  }
              },
              assert: func(t *testing.T, page runtime.InstancePage) {
                  if got := []string{page.Items[0].InstanceID, page.Items[1].InstanceID, page.Items[2].InstanceID}; got[0] != "c" || got[2] != "a" {
                      t.Fatalf("want c,b,a got %v", got)
                  }
                  if page.HasMore {
                      t.Fatal("want HasMore=false")
                  }
              },
          },
          // status-filter and pagination cases follow the same shape.
      }
      for _, tc := range tests {
          t.Run(tc.name, func(t *testing.T) {
              ms := runtime.NewMemStore()
              tc.seed(t, ms)
              page, err := ms.List(t.Context(), tc.filter)
              if err != nil {
                  t.Fatalf("list: %v", err)
              }
              tc.assert(t, page)
          })
      }
  }
  ```

  Run `go test ./runtime/...`, observe red.

- [ ] **GREEN — MemStore.List.** Add to `runtime/memstore.go`: snapshot instances
  under `RLock`, project to `InstanceSummary`, filter by `*filter.Status`, sort by
  `(StartedAt DESC, InstanceID DESC)` with `slices.SortFunc`, apply the cursor
  (drop rows `>=` the cursor key under the DESC ordering), take
  `normalizeLimit(filter.Limit)+1`, and set `HasMore`/`NextCursor` from the last
  *returned* row. Add the compile-time assertion
  `var _ InstanceLister = (*MemStore)(nil)`. Run `go test ./runtime/...` — green.

- [ ] **RED — Postgres lister (testcontainers).** Create
  `internal/persistence/postgres/lister_test.go`. Use `database.RunTestDatabase`,
  insert rows through the existing store's `Create`, then assert `List` ordering,
  status filter, and a two-page keyset walk. Fails (no `List` on the pg store).
  Run `go test ./internal/persistence/postgres/...`, observe red (needs Docker).

- [ ] **GREEN — Postgres lister.** Implement `List` with the keyset query:

  ```sql
  SELECT instance_id, def_id, def_version, status, started_at, ended_at
  FROM   instances
  WHERE  ($1::int IS NULL OR status = $1)
    AND  ($2::timestamptz IS NULL OR (started_at, instance_id) < ($2, $3))
  ORDER BY started_at DESC, instance_id DESC
  LIMIT  $4
  ```

  Bind `$4 = normalizeLimit(limit)+1`; decode `filter.Cursor` for `$2/$3` (NULL on
  empty); map `engine.Status` ↔ the stored int. Add the supporting index in the
  migration: `CREATE INDEX ... ON instances (started_at DESC, instance_id DESC)`.
  Run `go test ./internal/persistence/postgres/...` — green.

- [ ] **GREEN — persistence façade.** Surface the lister from the façade
  constructor (return type already exposes the Store; add an
  `InstanceLister()` accessor or have the façade type satisfy `InstanceLister`).
  Add/adjust the façade test. Run the façade package tests — green.

- [ ] **Verify:** `go test -race -coverprofile=cover.out ./runtime/... ./internal/persistence/...`,
  `go tool cover -func=cover.out | tail -1` ≥ 85%; `golangci-lint run ./runtime/... ./internal/persistence/...`.

- [ ] **Commit:** `feat(persistence): add keyset instance-enumeration query (InstanceLister)`.

---

## Task 2 — Service façade (`service/`)

The `Service` interface + `*Engine` impl. Resolve `*model.ProcessDefinition` from
the registry by the instance's `DefID:DefVersion` for signal/task delivery. All
request/result types transport-neutral. TDD with in-memory wiring.

**Files:**

- `service/request.go` / `service/request_test.go` (value types — only if any has behaviour)
- `service/service.go` / `service/service_test.go`

**Interfaces / signatures:**

```go
type Service interface {
    StartInstance(ctx context.Context, req StartInstanceRequest) (engine.InstanceState, error)
    GetInstance(ctx context.Context, instanceID string) (engine.InstanceState, error)
    DeliverSignal(ctx context.Context, req DeliverSignalRequest) (engine.InstanceState, error)
    DeliverMessage(ctx context.Context, req DeliverMessageRequest) error
    ClaimTask(ctx context.Context, req ClaimTaskRequest) (engine.InstanceState, error)
    CompleteTask(ctx context.Context, req CompleteTaskRequest) (engine.InstanceState, error)
    ReassignTask(ctx context.Context, req ReassignTaskRequest) (engine.InstanceState, error)
    ListInstances(ctx context.Context, filter runtime.InstanceFilter) (runtime.InstancePage, error)
}

func New(
    runner *runtime.Runner,
    tasks  *runtime.TaskService,
    reg    runtime.DefinitionRegistry,
    store  runtime.Store,
    lister runtime.InstanceLister,
) *Engine
```

### Steps

- [ ] **RED — StartInstance.** Create `service/service_test.go` (`package service_test`).
  Wire a real in-memory engine: a trivial single-task `model.ProcessDefinition`, a
  `runtime.NewMapDefinitionRegistry`, `runtime.NewMemStore`, a `clockwork` fake
  clock through the existing `clock.Clock` seam, `runtime.NewRunner`, and
  `runtime.NewTaskService(..., authz.AllowAll{}, clk)`. Construct
  `service.New(...)` and assert `StartInstance` returns a running/completed state
  whose `InstanceID` matches. Fails (`undefined: service.New`). Run
  `go test ./service/...`, observe red.

  ```go
  func TestEngineStartInstance(t *testing.T) {
      h := newHarness(t) // builds runner+tasks+reg+store+lister; def ref "greeting"
      svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister)

      st, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
          DefRef:     "greeting",
          InstanceID: "inst-1",
          Vars:       map[string]any{"name": "ada"},
      })
      if err != nil {
          t.Fatalf("start: %v", err)
      }
      if st.InstanceID != "inst-1" {
          t.Fatalf("got instance %q", st.InstanceID)
      }
  }
  ```

- [ ] **GREEN — StartInstance.** Create `service/request.go` (the request/result
  value structs) and `service/service.go` with `Engine`, `New`, and
  `StartInstance` (registry `Lookup(req.DefRef)`, then `runner.Run`). Map a
  `runtime.ErrDefinitionNotFound` straight through (the transport maps it to
  404). Run `go test ./service/...` — green.

- [ ] **RED → GREEN — GetInstance + resolveDefinition helper.** Add a test that
  `GetInstance` returns `runtime.ErrInstanceNotFound` for an unknown ID and the
  state for a started one. Implement via `store.Load`. Add a private
  `resolveDefinition(ctx, instanceID) (*model.ProcessDefinition, engine.InstanceState, error)`
  that loads the instance and looks the definition up by
  `fmt.Sprintf("%s:%d", st.DefID, st.DefVersion)` — TDD its not-found paths.
  (Document on `New` that the registry keys must be `DefID:DefVersion`.) Run reds,
  then greens.

- [ ] **RED → GREEN — DeliverSignal.** Test: start an instance that parks on a
  signal catch event, `DeliverSignal` resumes it. Impl: `resolveDefinition`, then
  `runner.Deliver(ctx, def, id, engine.NewSignalReceived(clk.Now(), req.Signal, req.Payload))`,
  return the new state. (`Engine` holds the `clock.Clock` passed in `New`, or
  reads it off the runner — pass it explicitly in `New` to avoid reaching into the
  runner.) Red → green.

- [ ] **RED → GREEN — ClaimTask / CompleteTask / ReassignTask.** Table test over
  the three. Each: call the matching `TaskService` method to get a trigger, then
  `resolveDefinition` + `runner.Deliver` with it, return the new state. Assert an
  unauthorized actor surfaces `authz.ErrNotAuthorized` (use a `RoleAuthorizer`
  with a non-matching role) and a known actor advances the instance. Red → green.

- [ ] **RED → GREEN — DeliverMessage + ListInstances.** `DeliverMessage` delegates
  to `runner.DeliverMessage` (no definition resolution needed — the runner's
  waiter table owns it). `ListInstances` delegates to the lister. Red → green.

- [ ] **Verify:** coverage ≥ 85% on `./service/...`; lint clean.

- [ ] **Commit:** `feat(service): transport-agnostic Service façade over runner+tasks+lister`.

---

## Task 3 — REST handler (`transport/rest`)

`NewHandler(svc, ...opts) http.Handler` (`*http.ServeMux`), all non-admin routes,
JSON decode/encode, `mapToHTTPError`, `WithInstanceMapper` + default
`InstanceView`. TDD with `net/http/httptest`. Prove mountability under
`http.StripPrefix`.

**Files:**

- `transport/rest/view.go` / `view_test.go`
- `transport/rest/errors.go` / `errors_test.go`
- `transport/rest/options.go` / `options_test.go`
- `transport/rest/handler.go` / `handler_test.go`

**Interfaces / signatures:**

```go
func NewHandler(svc service.Service, opts ...Option) http.Handler
type Option func(*config)
func WithInstanceMapper(fn func(engine.InstanceState) any) Option
type InstanceView struct { /* json-tagged projection of engine.InstanceState */ }
```

### Steps

- [ ] **RED — default InstanceView.** Create `transport/rest/view_test.go`
  (`package rest_test`). Assert `rest.NewInstanceView(state)` JSON-marshals to a
  body containing the instance ID, a string status (`"running"`), and the
  variables. Fails (`undefined`). Run `go test ./transport/rest/...`, observe red.

  ```go
  func TestNewInstanceView(t *testing.T) {
      st := engine.InstanceState{
          InstanceID: "inst-1", DefID: "d", DefVersion: 2,
          Status: engine.StatusRunning, Variables: map[string]any{"n": "ada"},
      }
      b, err := json.Marshal(rest.NewInstanceView(st))
      if err != nil {
          t.Fatal(err)
      }
      if !strings.Contains(string(b), `"status":"running"`) ||
          !strings.Contains(string(b), `"instance_id":"inst-1"`) {
          t.Fatalf("unexpected view: %s", b)
      }
  }
  ```

- [ ] **GREEN — InstanceView.** Create `transport/rest/view.go`: the JSON-tagged
  `InstanceView` struct, a `statusString(engine.Status) string` helper, and
  `NewInstanceView(engine.InstanceState) InstanceView`. Green.

- [ ] **RED — mapToHTTPError.** Create `transport/rest/errors_test.go`. Table over
  the sentinels → status codes from the spec (`ErrInstanceNotFound`→404,
  `ErrNotAuthorized`→403, `ErrConcurrentUpdate`→409, `runtime.ErrBadCursor`→400,
  a wrapped unknown→500), asserting both the status and that the JSON body has an
  `error` code. Fails. Red.

  ```go
  func TestMapToHTTPError(t *testing.T) {
      tests := []struct {
          name   string
          err    error
          assert func(t *testing.T, status int, body map[string]string)
      }{
          {
              name: "instance not found",
              err:  fmt.Errorf("svc: %w", runtime.ErrInstanceNotFound),
              assert: func(t *testing.T, status int, body map[string]string) {
                  if status != http.StatusNotFound {
                      t.Fatalf("want 404 got %d", status)
                  }
                  if body["error"] == "" {
                      t.Fatal("want error code in body")
                  }
              },
          },
          // not-authorized→403, concurrent→409, bad-cursor→400, unknown→500
      }
      for _, tc := range tests {
          t.Run(tc.name, func(t *testing.T) {
              rec := httptest.NewRecorder()
              rest.MapToHTTPError(rec, tc.err)
              var body map[string]string
              _ = json.Unmarshal(rec.Body.Bytes(), &body)
              tc.assert(t, rec.Code, body)
          })
      }
  }
  ```

  (Export `MapToHTTPError` for the black-box test, or keep it unexported and test
  through a handler — prefer testing through a handler so the table-test in the
  handler file covers it; keep `mapToHTTPError` unexported in that case. The plan
  uses the handler-level table as the canonical error test; this standalone test
  is optional and may instead live as `handler_test.go` cases.)

- [ ] **GREEN — mapToHTTPError.** Create `transport/rest/errors.go`: classify via
  `errors.Is` against the sentinels, write `{"error","message"}` JSON with the
  mapped status. Green.

- [ ] **RED — handler routes (table).** Create `transport/rest/handler_test.go`.
  Build a real `service.Service` from the Task-2 harness, `h := rest.NewHandler(svc)`,
  and a table of `(method, path, body)` → `(wantStatus, assertBody)` covering:
  `POST /instances` (201/200), `GET /instances/{id}` (200 + view, 404 unknown),
  `POST /instances/{id}/signals`, `POST /messages` (204/200), the three
  `POST /tasks/{token}/...` (200 + 403 with a denying authorizer + 404 unknown
  token), and a malformed JSON body → 400. Drive with `httptest.NewRequest` +
  `h.ServeHTTP(rec, req)`. Fails (`undefined: rest.NewHandler`). Red.

- [ ] **GREEN — handler.** Create `transport/rest/handler.go`: build the
  `*http.ServeMux`, register the non-admin patterns, decode JSON → `service`
  request, call the façade, `mapToHTTPError` on error, encode the (possibly
  mapper-customized) view on success. Use `r.PathValue`. Green.

- [ ] **RED → GREEN — WithInstanceMapper.** Create `transport/rest/options.go` +
  `options_test.go`. Test: `rest.NewHandler(svc, rest.WithInstanceMapper(func(engine.InstanceState) any { return map[string]string{"custom": "yes"} }))`
  → `GET /instances/{id}` body is the custom shape. Implement the option + a
  `config` with the mapper (default `NewInstanceView`). Red → green.

- [ ] **RED → GREEN — mountable under StripPrefix.** Test: mount under
  `http.NewServeMux()` with `mux.Handle("/api/wf/", http.StripPrefix("/api/wf", rest.NewHandler(svc)))`
  and assert `GET /api/wf/instances/{id}` reaches the handler (200). Should pass
  once routes are root-relative — if it doesn't, that's the red that proves the
  routes weren't root-relative. Green.

- [ ] **Verify:** coverage ≥ 85% on `./transport/rest/...`; lint clean.

- [ ] **Commit:** `feat(transport/rest): stdlib http.Handler over the Service façade`.

---

## Task 4 — Admin monitoring (REST)

`GET /admin/instances` keyset-paginated + `WithAdminMiddleware`. Default-deny when
no admin middleware is configured.

**Files:**

- `transport/rest/admin.go` / `admin_test.go`
- extend `transport/rest/options.go` (+ test) for `WithAdminMiddleware`

**Interfaces / signatures:**

```go
func WithAdminMiddleware(mw func(http.Handler) http.Handler) Option
// GET /admin/instances?status=&limit=&cursor=
// → 200 {"items":[...InstanceView...],"next_cursor":"...","has_more":bool}
```

### Steps

- [ ] **RED — pagination page1→cursor→page2.** Add to `transport/rest/admin_test.go`.
  Seed >limit instances via the harness, configure an allow-all admin middleware,
  `GET /admin/instances?limit=2`, assert 2 items + `has_more=true` + non-empty
  `next_cursor`; then `GET /admin/instances?limit=2&cursor=<that>` returns the next
  page with `has_more=false`. Fails (route not registered). Red.

  ```go
  func TestAdminListPagination(t *testing.T) {
      h := newHarness(t)
      seedInstances(t, h, 3) // started_at staggered
      handler := rest.NewHandler(h.service,
          rest.WithAdminMiddleware(allowAdmin)) // allowAdmin = passthrough

      page1 := doJSON(t, handler, "GET", "/admin/instances?limit=2", nil)
      if items := page1["items"].([]any); len(items) != 2 {
          t.Fatalf("want 2 items got %d", len(items))
      }
      if page1["has_more"] != true {
          t.Fatal("want has_more=true")
      }
      cursor := page1["next_cursor"].(string)

      page2 := doJSON(t, handler, "GET", "/admin/instances?limit=2&cursor="+cursor, nil)
      if page2["has_more"] != false {
          t.Fatal("want has_more=false on last page")
      }
  }
  ```

- [ ] **GREEN — admin list handler.** Create `transport/rest/admin.go`: parse
  `status` (→ `*engine.Status`, unknown → 400 via `mapToHTTPError` of a wrapped
  bad-request sentinel), `limit` (atoi, clamped by the lister), `cursor`
  (pass-through; a malformed cursor surfaces as `runtime.ErrBadCursor` → 400),
  call `svc.ListInstances`, map items through the configured view mapper, write
  `{items,next_cursor,has_more}`. Wire the route into `NewHandler` under the admin
  middleware. Green.

- [ ] **RED → GREEN — status filter.** Table: `?status=running` returns only
  running instances; `?status=bogus` → 400. Red → green.

- [ ] **RED → GREEN — admin middleware gate.** Table over: (a) a denying middleware
  (`http.Error(w, ..., 403)`) → `GET /admin/instances` returns 403 and never hits
  the lister; (b) **no** `WithAdminMiddleware` configured → 403 (default-deny).
  Implement default-deny by mounting admin routes under a built-in deny-all
  middleware when none is supplied. Red → green.

- [ ] **Verify:** coverage ≥ 85% on `./transport/rest/...`; lint clean.

- [ ] **Commit:** `feat(transport/rest): keyset-paginated admin instance monitoring`.

---

## Task 5 — gRPC transport (`transport/grpc` + `workflowpb`)

`.proto` + `buf.gen.yaml` + committed generated `workflowpb`; the
`WorkflowServiceServer` impl delegating to the façade;
`RegisterWorkflowServiceServer(reg, svc)`; `mapToGRPCStatus`. TDD the
service-impl via `bufconn`. Generated messages are not TDD'd.

> **Tooling note (blocking if unavailable):** generating `workflowpb` requires
> `buf` (or `protoc`) plus `protoc-gen-go` and `protoc-gen-go-grpc` on `PATH`
> (`go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and
> `google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`, then `buf generate`).
> If the environment cannot install these, stop and flag it to the controller —
> the committed generated code cannot be produced without them.

**Files:**

- `transport/grpc/proto/workflow.proto`
- `transport/grpc/buf.gen.yaml`
- `transport/grpc/workflowpb/workflow.pb.go` + `workflow_grpc.pb.go` (generated, committed)
- `transport/grpc/errors.go` / `errors_test.go`
- `transport/grpc/server.go` / `server_test.go`

**Interfaces / signatures:**

```go
func RegisterWorkflowServiceServer(reg grpc.ServiceRegistrar, svc service.Service)
func mapToGRPCStatus(err error) error
type server struct {
    workflowpb.UnimplementedWorkflowServiceServer
    svc service.Service
}
```

### Steps

- [ ] **Add gRPC deps.**

  ```bash
  go get google.golang.org/grpc@latest
  go get google.golang.org/protobuf@latest
  go mod tidy
  ```

- [ ] **Author `workflow.proto`.** Define `service WorkflowService` with one RPC
  per façade op and their messages. Set
  `option go_package = "github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb;workflowpb";`.
  Variables/payloads carry `map<string, ...>` or a `google.protobuf.Struct`
  (decide: `Struct` round-trips `map[string]any` faithfully — prefer it).

- [ ] **Author `buf.gen.yaml`** invoking `protoc-gen-go` + `protoc-gen-go-grpc`
  with `paths=source_relative`, output rooted so files land in `workflowpb/`.
  Run `buf generate` (or `protoc ...`). **Commit the generated files.** Run
  `go build ./transport/grpc/...` to confirm the generated package compiles.

- [ ] **RED — mapToGRPCStatus.** Create `transport/grpc/errors_test.go`. Table:
  `ErrInstanceNotFound`→`codes.NotFound`, `ErrNotAuthorized`→`PermissionDenied`,
  `ErrConcurrentUpdate`→`Aborted`, `ErrBadCursor`→`InvalidArgument`, unknown→
  `Internal`, asserting `status.Code(mapToGRPCStatus(err))`. Fails. Red.

  ```go
  func TestMapToGRPCStatus(t *testing.T) {
      tests := []struct {
          name string
          err  error
          want codes.Code
      }{
          {"not found", runtime.ErrInstanceNotFound, codes.NotFound},
          {"denied", authz.ErrNotAuthorized, codes.PermissionDenied},
          {"concurrent", runtime.ErrConcurrentUpdate, codes.Aborted},
          {"bad cursor", runtime.ErrBadCursor, codes.InvalidArgument},
          {"unknown", errors.New("boom"), codes.Internal},
      }
      for _, tc := range tests {
          t.Run(tc.name, func(t *testing.T) {
              if got := status.Code(grpcpkg.ExportedMapToGRPCStatus(tc.err)); got != tc.want {
                  t.Fatalf("want %v got %v", tc.want, got)
              }
          })
      }
  }
  ```

  (Test through the exported mapper, or through the server via bufconn — prefer
  the bufconn server test as canonical and keep `mapToGRPCStatus` unexported; the
  snippet above shows the intended mapping table either way.)

- [ ] **GREEN — mapToGRPCStatus.** Create `transport/grpc/errors.go`: classify via
  `errors.Is`, return `status.Error(code, err.Error())`. Green.

- [ ] **RED — server via bufconn.** Create `transport/grpc/server_test.go`
  (`package grpc_test`). Stand up a `bufconn.Listen`, register the service with
  `grpc.RegisterWorkflowServiceServer(grpcServer, svc)` (svc = Task-2 harness),
  dial it, and table-test: `StartInstance` ok; `GetInstance` unknown →
  `codes.NotFound`; `ClaimTask` with a denying authorizer → `codes.PermissionDenied`;
  `ListInstances` returns seeded items. Fails (`undefined: RegisterWorkflowServiceServer`).
  Red.

- [ ] **GREEN — server impl + register.** Create `transport/grpc/server.go`: the
  `server` struct embedding `UnimplementedWorkflowServiceServer`, one method per
  RPC translating proto ↔ `service` request/result (Struct ↔ `map[string]any`)
  and `mapToGRPCStatus` on error; and
  `RegisterWorkflowServiceServer(reg grpc.ServiceRegistrar, svc service.Service)`
  calling `workflowpb.RegisterWorkflowServiceServer(reg, &server{svc: svc})`. Green.

- [ ] **Verify:** coverage ≥ 85% on `./transport/grpc/...` **excluding**
  `workflowpb` (generated); lint clean (add a `//nolint`-free
  `.golangci.yml` exclude for `workflowpb` or rely on generated-file detection).

- [ ] **Commit:** `feat(transport/grpc): WorkflowService over the Service façade (grpc-go + buf)`.

---

## Task 6 — Verification + HANDOVER

Final gate across the whole sub-project + handover doc.

**Files:**

- `docs/plans/HANDOVER.md` (update)

### Steps

- [ ] **Full race suite:** `go test -race ./...` from repo root — no regressions.

- [ ] **Coverage gate (touched packages):**

  ```bash
  go test -race -coverprofile=cover.out ./service/... ./transport/... ./runtime/... ./internal/persistence/... \
    && go tool cover -func=cover.out | tail -1
  ```

  ≥ 85% on each touched package (`workflowpb` excluded from the bar).

- [ ] **Lint clean:** `golangci-lint run ./...`.

- [ ] **Import-isolation assertion.** Confirm `net/http` *server*, `grpc`, and
  `protobuf` are imported ONLY under `service/` and `transport/`:

  ```bash
  ! grep -rEl 'google\.golang\.org/(grpc|protobuf)|net/http' \
      engine/ runtime/ model/ humantask/ \
      --include='*.go' | grep -v '_test.go'
  ```

  (Tighten the pattern so a legitimate `net/http` *client* use, if any, is not a
  false positive — the rule targets server types: `http.Handler`,
  `http.ServeMux`, `http.ListenAndServe`.) The grep must return nothing.

- [ ] **Generated-code freshness (if buf available).** Re-run `buf generate` and
  `git diff --exit-code transport/grpc/workflowpb` — a non-empty diff means the
  committed generated code is stale. Skip with a noted reason if buf is
  unavailable in the environment.

- [ ] **Update `docs/plans/HANDOVER.md`:** record the transports sub-project as
  complete; list the public entry points (`service.New`, `service.Service`,
  `rest.NewHandler` + options, `grpc.RegisterWorkflowServiceServer`); and the
  deferred follow-ups: auth/observability middleware *examples* under `examples/`,
  OpenAPI/grpc-gateway, streaming/watch endpoints, richer admin filters + total
  count, and a typed wrong-state sentinel set (422 / `FailedPrecondition`).

- [ ] **Commit:** `docs(transports): record REST+gRPC completion and follow-ups in HANDOVER`.

---

## Verification checklist

- [ ] Task 1 — `InstanceLister` port + `MemStore` + Postgres + façade; engine core untouched; cursor codec round-trips; ≥85% cov.
- [ ] Task 2 — `service.Service` façade; definition resolved by `DefID:DefVersion`; all 8 ops; ≥85% cov.
- [ ] Task 3 — `rest.NewHandler` stdlib mux; all non-admin routes; `WithInstanceMapper`; mountable under StripPrefix; ≥85% cov.
- [ ] Task 4 — `GET /admin/instances` keyset pagination + status filter; `WithAdminMiddleware` gate + default-deny; ≥85% cov.
- [ ] Task 5 — `.proto` + committed `workflowpb`; bufconn-tested server; `RegisterWorkflowServiceServer`; `mapToGRPCStatus`; tooling note honoured.
- [ ] Task 6 — `go test -race ./...` green; lint clean; import-isolation grep empty; generated code fresh; HANDOVER updated.
- [ ] Every new exported symbol had a VISIBLE red→green in the transcript (generated `workflowpb` exempt).
