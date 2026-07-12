# gRPC `ResolveIncident` + DLQ admin transport — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Honour the project's strict TDD discipline (CLAUDE.md): a visible RED (`go test` failing/not-compiling) must precede every implementation, in its own Bash call.

**Goal:** Expose incident-resolution and outbox dead-letter (DLQ) administration through both library transports — add a gRPC `ResolveIncident` RPC and DLQ admin (list + redrive) on REST and gRPC — behind a new optional `service.DeadLetterAdmin` seam.

**Architecture:** A new `service.DeadLetterAdmin` interface (method set identical to `persistence.Relay`, so the relay satisfies it directly) is wired into each transport as an *optional* dependency via `WithDeadLetterAdmin(...)`. REST registers the DLQ routes only when wired (else 404); gRPC always exposes the RPCs but returns `codes.Unimplemented` when unwired. `ResolveIncident` is a thin gRPC pass-through to the existing `service.ResolveIncident`. Engine/model are untouched.

**Tech Stack:** Go 1.25, `net/http` ServeMux (REST), gRPC + protobuf (`protoc` + `protoc-gen-go`/`protoc-gen-go-grpc`), testify, bufconn for gRPC tests.

## Global Constraints

- Module path: `github.com/kartaladev/wrkflw`. Single Go module at repo root; public packages have **no `pkg/` prefix** (ADR-0004).
- **Strict TDD** (CLAUDE.md): no production code before a failing test; the RED state must be observable in the transcript as a separate `go test` Bash call.
- **Engine/model: ZERO production diff.** This track touches only `service/`, `transport/grpc/`, `transport/rest/`, and a test file under `persistence/`.
- All production error messages carry the **`workflow-`** prefix (e.g. `workflow-grpc:`); assert sentinels via `errors.Is`, never string-matching (ADR-0026).
- Black-box tests (`<package>_test`); table tests follow the project `table-test` skill (assert-closure form, `t.Context()` over `context.Background()`).
- No new forbidden vendor imports (`watermill`/`casbin`/`gocron`/`clockwork`) in production code.
- Gate per touched package: `go test -race ./...` green; ≥85% line coverage on touched packages; `golangci-lint run ./...` clean.
- gRPC proto regen command (verified byte-identical against committed output):
  ```bash
  cd transport/grpc && export PATH="$PATH:$(go env GOPATH)/bin" && \
    protoc --proto_path=proto \
      --go_out=workflowpb --go_opt=paths=source_relative \
      --go-grpc_out=workflowpb --go-grpc_opt=paths=source_relative \
      proto/workflow.proto
  ```
  (equivalently `go generate ./transport/grpc/...`).

---

## File Structure

- `service/deadletter.go` (**create**) — `DeadLetterAdmin` interface.
- `persistence/deadletter_admin_test.go` (**create**) — black-box compile-time satisfaction assertion (`persistence_test`).
- `transport/grpc/proto/workflow.proto` (**modify**) — 3 new RPCs + 5 new messages.
- `transport/grpc/workflowpb/*.pb.go` (**regenerated**) — do not hand-edit.
- `transport/grpc/options.go` (**modify**) — `WithDeadLetterAdmin` + `serverConfig.deadLetters`.
- `transport/grpc/server.go` (**modify**) — `server.deadLetters`, registration threading, `ResolveIncident`/`ListDeadLetters`/`RedriveDeadLetters` handlers, `deadLetterToProto` helper, extended SECURITY doc.
- `transport/grpc/resolve_incident_test.go` (**create**) — `ResolveIncident` RPC tests.
- `transport/grpc/dead_letters_test.go` (**create**) — DLQ RPC tests (+ a `newStubHarnessWithOpts` helper, or extend `newStubHarness`).
- `transport/rest/options.go` (**modify**) — `WithDeadLetterAdmin` + `config.deadLetters`.
- `transport/rest/admin.go` (**modify**) — `handleListDeadLetters`, `handleRedriveDeadLetters`.
- `transport/rest/view.go` (**modify**) — `deadLetterView`.
- `transport/rest/handler.go` (**modify**) — conditional route registration + doc.
- `transport/rest/dead_letters_test.go` (**create**) — REST DLQ tests.

---

### Task 1: `service.DeadLetterAdmin` seam

**Files:**
- Create: `service/deadletter.go`
- Test: `persistence/deadletter_admin_test.go`

**Interfaces:**
- Consumes: `runtime.DeadLetter` (existing value type — `ID int64`, `InstanceID string`, `Topic string`, `RetryCount int`, `LastError string`, `CreatedAt time.Time`); `persistence.Relay` (existing interface, already has `ListDeadLettered`/`Redrive`).
- Produces: `service.DeadLetterAdmin` interface with `ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error)` and `Redrive(ctx context.Context, ids ...int64) (int, error)`.

- [ ] **Step 1: Write the failing test** — `persistence/deadletter_admin_test.go`:

```go
package persistence_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/service"
)

// TestRelaySatisfiesDeadLetterAdmin is a compile-time guard that the persistence
// Relay façade satisfies the service.DeadLetterAdmin seam, so consumers can pass
// their relay straight to WithDeadLetterAdmin with no adapter.
func TestRelaySatisfiesDeadLetterAdmin(t *testing.T) {
	t.Parallel()
	var _ service.DeadLetterAdmin = (persistence.Relay)(nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/ -run TestRelaySatisfiesDeadLetterAdmin`
Expected: FAIL — build error `undefined: service.DeadLetterAdmin`.

- [ ] **Step 3: Write minimal implementation** — `service/deadletter.go`:

```go
package service

import (
	"context"

	"github.com/kartaladev/wrkflw/runtime"
)

// DeadLetterAdmin is the optional admin port for inspecting and redriving
// dead-lettered outbox events. It is intentionally separate from Service: the
// dead-letter queue is an outbox-relay concern, not an engine/runtime one, and
// a consumer without the Postgres outbox relay (e.g. MemStore-only) simply never
// wires it.
//
// Its method set is identical to persistence.Relay's, so persistence.Relay
// satisfies DeadLetterAdmin directly — pass the relay straight to a transport's
// WithDeadLetterAdmin option with no adapter.
type DeadLetterAdmin interface {
	// ListDeadLettered returns up to limit dead-lettered outbox rows, oldest first.
	ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	// Redrive resets the given dead rows back to pending and returns the count
	// re-queued. Passing no ids is a no-op (returns 0, nil).
	Redrive(ctx context.Context, ids ...int64) (int, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/ -run TestRelaySatisfiesDeadLetterAdmin`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add service/deadletter.go persistence/deadletter_admin_test.go
git commit -m "feat(service): DeadLetterAdmin seam (persistence.Relay satisfies it)"
```

---

### Task 2: gRPC proto additions + regen + `ResolveIncident` RPC

**Files:**
- Modify: `transport/grpc/proto/workflow.proto`
- Regenerated: `transport/grpc/workflowpb/workflow.pb.go`, `transport/grpc/workflowpb/workflow_grpc.pb.go`
- Modify: `transport/grpc/server.go` (add `ResolveIncident` handler)
- Create: `transport/grpc/resolve_incident_test.go`

**Interfaces:**
- Consumes: `service.Service.ResolveIncident(ctx, service.ResolveIncidentRequest) (engine.InstanceState, error)`; `service.ResolveIncidentRequest{InstanceID, IncidentID string; AddAttempts int}`; existing `instanceToProto`, `mapToGRPCStatus`, `s.startSpan`, `recordSpanErr`.
- Produces: `workflowpb.ResolveIncidentRequest{InstanceId, IncidentId string; AddAttempts int32}`; `server.ResolveIncident(ctx, *workflowpb.ResolveIncidentRequest) (*workflowpb.InstanceResponse, error)`. Also produces the new DLQ proto messages (consumed by Task 3): `workflowpb.DeadLetter`, `ListDeadLettersRequest/Response`, `RedriveDeadLettersRequest/Response`.

- [ ] **Step 1: Edit the proto** — in `transport/grpc/proto/workflow.proto`, add to the `WorkflowService` service block (after `CancelInstance`):

```protobuf
  // ResolveIncident clears an open incident, grants additional attempts, and resumes execution.
  rpc ResolveIncident(ResolveIncidentRequest) returns (InstanceResponse);

  // ListDeadLetters returns dead-lettered outbox rows. Admin-scoped; requires
  // the server to be registered with WithDeadLetterAdmin, else returns Unimplemented.
  rpc ListDeadLetters(ListDeadLettersRequest) returns (ListDeadLettersResponse);

  // RedriveDeadLetters re-queues dead outbox rows by id. Admin-scoped; requires
  // WithDeadLetterAdmin, else returns Unimplemented.
  rpc RedriveDeadLetters(RedriveDeadLettersRequest) returns (RedriveDeadLettersResponse);
```

And add these messages (after `CancelInstanceRequest`, or near the response messages):

```protobuf
message ResolveIncidentRequest {
  string instance_id = 1;
  string incident_id = 2;
  // add_attempts grants additional execution attempts; ≤ 0 defaults to 1 in the service.
  int32 add_attempts = 3;
}

// DeadLetter is the gRPC projection of runtime.DeadLetter.
message DeadLetter {
  int64 id = 1;
  string instance_id = 2;
  string topic = 3;
  int32 retry_count = 4;
  string last_error = 5;
  google.protobuf.Timestamp created_at = 6;
}

message ListDeadLettersRequest {
  // limit is the page size (default 50, max 200 after normalization).
  int32 limit = 1;
}

message ListDeadLettersResponse {
  repeated DeadLetter items = 1;
}

message RedriveDeadLettersRequest {
  repeated int64 ids = 1;
}

message RedriveDeadLettersResponse {
  int32 redriven_count = 1;
}
```

- [ ] **Step 2: Regenerate the Go stubs**

Run:
```bash
cd transport/grpc && export PATH="$PATH:$(go env GOPATH)/bin" && \
  protoc --proto_path=proto \
    --go_out=workflowpb --go_opt=paths=source_relative \
    --go-grpc_out=workflowpb --go-grpc_opt=paths=source_relative \
    proto/workflow.proto && cd ../..
go build ./transport/grpc/...
```
Expected: builds clean. The `server` struct compiles because `UnimplementedWorkflowServiceServer` provides default (Unimplemented) impls of the three new methods.

- [ ] **Step 3: Write the failing test** — `transport/grpc/resolve_incident_test.go`:

```go
package grpctransport_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/grpc/workflowpb"
)

// resolveStub is a service.Service stub with a configurable ResolveIncident.
type resolveStub struct {
	service.Service
	resolveFn func(ctx context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error)
	gotReq    service.ResolveIncidentRequest
}

func (s *resolveStub) ResolveIncident(ctx context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error) {
	s.gotReq = req
	return s.resolveFn(ctx, req)
}

func TestServerResolveIncident(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		fn     func(ctx context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error)
		assert func(t *testing.T, resp *workflowpb.InstanceResponse, err error)
	}{
		{
			name: "success maps fields and returns instance",
			fn: func(_ context.Context, _ service.ResolveIncidentRequest) (engine.InstanceState, error) {
				return engine.InstanceState{InstanceID: "p1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Now()}, nil
			},
			assert: func(t *testing.T, resp *workflowpb.InstanceResponse, err error) {
				require.NoError(t, err)
				assert.Equal(t, "p1", resp.GetInstance().GetInstanceId())
			},
		},
		{
			name: "not-found maps to NotFound",
			fn: func(_ context.Context, _ service.ResolveIncidentRequest) (engine.InstanceState, error) {
				return engine.InstanceState{}, fmt.Errorf("workflow-service: %w", runtime.ErrInstanceNotFound)
			},
			assert: func(t *testing.T, _ *workflowpb.InstanceResponse, err error) {
				assert.Equal(t, codes.NotFound, status.Code(err))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &resolveStub{resolveFn: tc.fn}
			client := newStubHarness(t, stub)
			resp, err := client.ResolveIncident(t.Context(), &workflowpb.ResolveIncidentRequest{
				InstanceId: "p1", IncidentId: "i1", AddAttempts: 2,
			})
			tc.assert(t, resp, err)
		})
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./transport/grpc/ -run TestServerResolveIncident`
Expected: FAIL — success case gets `codes.Unimplemented` from the embedded default impl.

- [ ] **Step 5: Implement the handler** — in `transport/grpc/server.go`, add after `CancelInstance`:

```go
// ResolveIncident clears an open incident on an instance and resumes execution.
func (s *server) ResolveIncident(ctx context.Context, req *workflowpb.ResolveIncidentRequest) (*workflowpb.InstanceResponse, error) {
	ctx, span := s.startSpan(ctx, "ResolveIncident")
	defer span.End()

	st, err := s.svc.ResolveIncident(ctx, service.ResolveIncidentRequest{
		InstanceID:  req.GetInstanceId(),
		IncidentID:  req.GetIncidentId(),
		AddAttempts: int(req.GetAddAttempts()),
	})
	if err != nil {
		recordSpanErr(span, err)
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		recordSpanErr(span, err)
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./transport/grpc/ -run TestServerResolveIncident`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add transport/grpc/proto/workflow.proto transport/grpc/workflowpb/ transport/grpc/server.go transport/grpc/resolve_incident_test.go
git commit -m "feat(transport/grpc): ResolveIncident RPC + DLQ proto messages"
```

---

### Task 3: gRPC `WithDeadLetterAdmin` + DLQ RPC handlers

**Files:**
- Modify: `transport/grpc/options.go`
- Modify: `transport/grpc/server.go`
- Create: `transport/grpc/dead_letters_test.go`

**Interfaces:**
- Consumes: `service.DeadLetterAdmin` (Task 1); `runtime.DeadLetter`; `runtime.NormalizeLimit`; the Task-2 proto messages; `timestamppb.New`.
- Produces: `grpctransport.WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option`; `server.ListDeadLetters`/`server.RedriveDeadLetters`; `deadLetterToProto(runtime.DeadLetter) *workflowpb.DeadLetter`. New test helper `newStubHarnessWithOpts(t, svc, opts...)`.

- [ ] **Step 1: Write the failing test** — `transport/grpc/dead_letters_test.go`:

```go
package grpctransport_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/service"
	grpctransport "github.com/kartaladev/wrkflw/transport/grpc"
	"github.com/kartaladev/wrkflw/transport/grpc/workflowpb"
)

// dlaStub is a configurable service.DeadLetterAdmin test double.
type dlaStub struct {
	listFn    func(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	redriveFn func(ctx context.Context, ids ...int64) (int, error)
	gotLimit  int
	gotIDs    []int64
}

func (s *dlaStub) ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error) {
	s.gotLimit = limit
	return s.listFn(ctx, limit)
}

func (s *dlaStub) Redrive(ctx context.Context, ids ...int64) (int, error) {
	s.gotIDs = ids
	return s.redriveFn(ctx, ids...)
}

// newStubHarnessWithOpts is newStubHarness with transport options.
func newStubHarnessWithOpts(t *testing.T, svc service.Service, opts ...grpctransport.Option) workflowpb.WorkflowServiceClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	grpctransport.RegisterWorkflowServiceServer(grpcServer, svc, opts...)
	t.Cleanup(func() { grpcServer.Stop() })
	go func() { _ = grpcServer.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return workflowpb.NewWorkflowServiceClient(conn)
}

func TestServerListDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired returns items and normalizes limit", func(t *testing.T) {
		t.Parallel()
		created := time.Now()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
			return []runtime.DeadLetter{{ID: 7, InstanceID: "p1", Topic: "instance.completed", RetryCount: 5, LastError: "boom", CreatedAt: created}}, nil
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
		resp, err := client.ListDeadLetters(t.Context(), &workflowpb.ListDeadLettersRequest{Limit: 0})
		require.NoError(t, err)
		require.Len(t, resp.GetItems(), 1)
		assert.Equal(t, int64(7), resp.GetItems()[0].GetId())
		assert.Equal(t, "p1", resp.GetItems()[0].GetInstanceId())
		assert.Equal(t, 50, dla.gotLimit) // NormalizeLimit(0) == 50
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.ListDeadLetters(t.Context(), &workflowpb.ListDeadLettersRequest{Limit: 10})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestServerRedriveDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired returns count", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, ids ...int64) (int, error) { return len(ids), nil }}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
		resp, err := client.RedriveDeadLetters(t.Context(), &workflowpb.RedriveDeadLettersRequest{Ids: []int64{1, 2, 3}})
		require.NoError(t, err)
		assert.Equal(t, int32(3), resp.GetRedrivenCount())
		assert.Equal(t, []int64{1, 2, 3}, dla.gotIDs)
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.RedriveDeadLetters(t.Context(), &workflowpb.RedriveDeadLettersRequest{Ids: []int64{1}})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestWithDeadLetterAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { grpctransport.WithDeadLetterAdmin(nil) })
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./transport/grpc/ -run 'TestServerListDeadLetters|TestServerRedriveDeadLetters|TestWithDeadLetterAdminNilPanics'`
Expected: FAIL — build error `undefined: grpctransport.WithDeadLetterAdmin`.

- [ ] **Step 3: Implement the option** — in `transport/grpc/options.go`, add the import `"github.com/kartaladev/wrkflw/service"`, add the field, and the option:

```go
// (in serverConfig struct)
	deadLetters service.DeadLetterAdmin
```

```go
// WithDeadLetterAdmin enables the DLQ admin RPCs (ListDeadLetters, RedriveDeadLetters)
// by supplying a service.DeadLetterAdmin (e.g. a persistence.Relay). When this option
// is NOT supplied, those RPCs return codes.Unimplemented.
//
// SECURITY: like ListInstances, the DLQ RPCs have no built-in per-method authorization;
// the consumer MUST gate them with a grpc interceptor.
//
// Panics immediately if dla is nil.
func WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option {
	if dla == nil {
		panic("grpc: WithDeadLetterAdmin: dla must not be nil")
	}
	return func(c *serverConfig) {
		c.deadLetters = dla
	}
}
```

- [ ] **Step 4: Thread it + implement handlers** — in `transport/grpc/server.go`:

Add to the `server` struct:
```go
	deadLetters service.DeadLetterAdmin
```
In `RegisterWorkflowServiceServer`, change the registration to thread it:
```go
	workflowpb.RegisterWorkflowServiceServer(reg, &server{svc: svc, tel: tel, deadLetters: cfg.deadLetters})
```
Extend the SECURITY doc-comment on `RegisterWorkflowServiceServer` to note the DLQ RPCs are also admin-scoped and rely on the consumer's interceptor when `WithDeadLetterAdmin` is supplied.

Add the handlers and helper:
```go
// ListDeadLetters returns dead-lettered outbox rows. Requires WithDeadLetterAdmin.
func (s *server) ListDeadLetters(ctx context.Context, req *workflowpb.ListDeadLettersRequest) (*workflowpb.ListDeadLettersResponse, error) {
	ctx, span := s.startSpan(ctx, "ListDeadLetters")
	defer span.End()

	if s.deadLetters == nil {
		return nil, status.Error(codes.Unimplemented, "workflow-grpc: dead-letter admin not configured")
	}
	rows, err := s.deadLetters.ListDeadLettered(ctx, runtime.NormalizeLimit(int(req.GetLimit())))
	if err != nil {
		recordSpanErr(span, err)
		return nil, mapToGRPCStatus(err)
	}
	items := make([]*workflowpb.DeadLetter, len(rows))
	for i, dl := range rows {
		items[i] = deadLetterToProto(dl)
	}
	return &workflowpb.ListDeadLettersResponse{Items: items}, nil
}

// RedriveDeadLetters re-queues dead outbox rows by id. Requires WithDeadLetterAdmin.
func (s *server) RedriveDeadLetters(ctx context.Context, req *workflowpb.RedriveDeadLettersRequest) (*workflowpb.RedriveDeadLettersResponse, error) {
	ctx, span := s.startSpan(ctx, "RedriveDeadLetters")
	defer span.End()

	if s.deadLetters == nil {
		return nil, status.Error(codes.Unimplemented, "workflow-grpc: dead-letter admin not configured")
	}
	n, err := s.deadLetters.Redrive(ctx, req.GetIds()...)
	if err != nil {
		recordSpanErr(span, err)
		return nil, mapToGRPCStatus(err)
	}
	// n is a row count; safe to narrow to int32 for the wire.
	return &workflowpb.RedriveDeadLettersResponse{RedrivenCount: int32(n)}, nil //nolint:gosec // bounded row count
}

// deadLetterToProto projects a runtime.DeadLetter onto its gRPC message.
func deadLetterToProto(dl runtime.DeadLetter) *workflowpb.DeadLetter {
	return &workflowpb.DeadLetter{
		Id:         dl.ID,
		InstanceId: dl.InstanceID,
		Topic:      dl.Topic,
		RetryCount: int32(dl.RetryCount), //nolint:gosec // bounded retry count
		LastError:  dl.LastError,
		CreatedAt:  timestamppb.New(dl.CreatedAt),
	}
}
```

(`runtime`, `timestamppb`, `codes`, `status` are already imported in `server.go`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./transport/grpc/ -run 'TestServerListDeadLetters|TestServerRedriveDeadLetters|TestWithDeadLetterAdminNilPanics'`
Expected: PASS.

- [ ] **Step 6: Run the full gRPC package + lint**

Run: `go test -race ./transport/grpc/... && golangci-lint run ./transport/grpc/...`
Expected: PASS, lint clean. If the `int32` narrowing trips `gosec`, the `//nolint:gosec` comments above are present; adjust the directive form if the linter wants a different one.

- [ ] **Step 7: Commit**

```bash
git add transport/grpc/options.go transport/grpc/server.go transport/grpc/dead_letters_test.go
git commit -m "feat(transport/grpc): DLQ admin RPCs behind WithDeadLetterAdmin"
```

---

### Task 4: REST DLQ admin endpoints

**Files:**
- Modify: `transport/rest/options.go`
- Modify: `transport/rest/view.go`
- Modify: `transport/rest/admin.go`
- Modify: `transport/rest/handler.go`
- Create: `transport/rest/dead_letters_test.go`

**Interfaces:**
- Consumes: `service.DeadLetterAdmin` (Task 1); `runtime.DeadLetter`; `runtime.NormalizeLimit`; existing `decodeBody`, `WriteHTTPError`, `ErrBadInput`, `h.writeJSON`, `cfg.adminMiddleware`.
- Produces: `rest.WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option`; `config.deadLetters`; `handler.handleListDeadLetters`/`handleRedriveDeadLetters`; `deadLetterView`.

- [ ] **Step 1: Write the failing test** — `transport/rest/dead_letters_test.go`:

```go
package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/rest"
)

type dlaStub struct {
	listFn    func(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	redriveFn func(ctx context.Context, ids ...int64) (int, error)
	gotLimit  int
	gotIDs    []int64
}

func (s *dlaStub) ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error) {
	s.gotLimit = limit
	return s.listFn(ctx, limit)
}
func (s *dlaStub) Redrive(ctx context.Context, ids ...int64) (int, error) {
	s.gotIDs = ids
	return s.redriveFn(ctx, ids...)
}

// allowAdmin is an admin middleware that passes everything through (test-only).
func allowAdmin(next http.Handler) http.Handler { return next }

// restStubService is a no-op service.Service; DLQ routes don't touch it.
type restStubService struct{ service.Service }

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRESTListDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow returns items, normalizes limit", func(t *testing.T) {
		t.Parallel()
		created := time.Now()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
			return []runtime.DeadLetter{{ID: 7, InstanceID: "p1", Topic: "t", RetryCount: 5, LastError: "boom", CreatedAt: created}}, nil
		}}
		h := rest.NewHandler(&restStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := do(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"id":7`)
		assert.Equal(t, 50, dla.gotLimit)
	})

	t.Run("default-deny without admin middleware -> 403", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) { return nil, nil }}
		h := rest.NewHandler(&restStubService{}, rest.WithDeadLetterAdmin(dla))
		rec := do(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&restStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := do(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("bad limit -> 400", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) { return nil, nil }}
		h := rest.NewHandler(&restStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := do(t, h, http.MethodGet, "/admin/dead-letters?limit=abc", "")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestRESTRedriveDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired returns count", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, ids ...int64) (int, error) { return len(ids), nil }}
		h := rest.NewHandler(&restStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := do(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[1,2,3]}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"redriven":3`)
		assert.Equal(t, []int64{1, 2, 3}, dla.gotIDs)
	})

	t.Run("empty ids -> 0", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, ids ...int64) (int, error) { return len(ids), nil }}
		h := rest.NewHandler(&restStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := do(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[]}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"redriven":0`)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&restStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := do(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[1]}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestRESTWithDeadLetterAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { rest.WithDeadLetterAdmin(nil) })
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./transport/rest/ -run 'TestRESTListDeadLetters|TestRESTRedriveDeadLetters|TestRESTWithDeadLetterAdminNilPanics'`
Expected: FAIL — build error `undefined: rest.WithDeadLetterAdmin`.

- [ ] **Step 3: Add the option** — in `transport/rest/options.go`, add import `"github.com/kartaladev/wrkflw/service"`, add the field to `config`:

```go
	deadLetters service.DeadLetterAdmin
```

and the option:

```go
// WithDeadLetterAdmin enables the DLQ admin routes (GET /admin/dead-letters and
// POST /admin/dead-letters/redrive) by supplying a service.DeadLetterAdmin (e.g.
// a persistence.Relay). When NOT supplied, those routes are not registered (404).
// The routes sit behind the configured admin middleware (default-deny).
//
// Panics immediately if dla is nil.
func WithDeadLetterAdmin(dla service.DeadLetterAdmin) Option {
	if dla == nil {
		panic("rest: WithDeadLetterAdmin: dla must not be nil")
	}
	return func(c *config) {
		c.deadLetters = dla
	}
}
```

- [ ] **Step 4: Add the view** — in `transport/rest/view.go`, add:

```go
// deadLetterView is the JSON projection of a runtime.DeadLetter for the DLQ admin API.
type deadLetterView struct {
	ID         int64     `json:"id"`
	InstanceID string    `json:"instance_id"`
	Topic      string    `json:"topic"`
	RetryCount int       `json:"retry_count"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
}
```

(ensure `time` and `runtime` imports are present in `view.go`.)

- [ ] **Step 5: Add the handlers** — in `transport/rest/admin.go`, add (the file already imports `runtime`, `service`, `strconv`, `net/http`, `fmt`):

```go
// dlqListResponse is the JSON envelope for GET /admin/dead-letters.
type dlqListResponse struct {
	Items []deadLetterView `json:"items"`
}

// dlqRedriveResponse is the JSON envelope for POST /admin/dead-letters/redrive.
type dlqRedriveResponse struct {
	Redriven int `json:"redriven"`
}

// handleListDeadLetters handles GET /admin/dead-letters.
//
// Query parameters:
//
//	limit (optional) — page size; clamped by runtime.NormalizeLimit (default 50, max 200).
func (h *handler) handleListDeadLetters(w http.ResponseWriter, r *http.Request) {
	var limit int
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			WriteHTTPError(w, fmt.Errorf("%w: invalid limit %q", ErrBadInput, raw))
			return
		}
		limit = n
	}
	rows, err := h.cfg.deadLetters.ListDeadLettered(r.Context(), runtime.NormalizeLimit(limit))
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	resp := dlqListResponse{Items: make([]deadLetterView, len(rows))}
	for i, dl := range rows {
		resp.Items[i] = deadLetterView{
			ID:         dl.ID,
			InstanceID: dl.InstanceID,
			Topic:      dl.Topic,
			RetryCount: dl.RetryCount,
			LastError:  dl.LastError,
			CreatedAt:  dl.CreatedAt,
		}
	}
	h.writeJSON(w, r, http.StatusOK, resp)
}

// handleRedriveDeadLetters handles POST /admin/dead-letters/redrive.
//
// Body: {"ids":[int64,...]}. Empty/absent ids is a no-op (returns {"redriven":0}).
func (h *handler) handleRedriveDeadLetters(w http.ResponseWriter, r *http.Request) {
	type reqBody struct {
		IDs []int64 `json:"ids"`
	}
	var body reqBody
	if !decodeBody(w, r, &body) {
		return
	}
	n, err := h.cfg.deadLetters.Redrive(r.Context(), body.IDs...)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, dlqRedriveResponse{Redriven: n})
}
```

- [ ] **Step 6: Register the routes conditionally** — in `transport/rest/handler.go`, after the existing admin route registrations and before `return h.traceMiddleware(mux)`:

```go
	// DLQ admin routes are only registered when a DeadLetterAdmin is wired. Absent
	// it (e.g. MemStore-only consumers), the routes do not exist (404) rather than
	// returning a misleading 501. Like the other admin routes they sit behind
	// cfg.adminMiddleware (default-deny).
	if cfg.deadLetters != nil {
		mux.Handle("GET /admin/dead-letters", cfg.adminMiddleware(http.HandlerFunc(h.handleListDeadLetters)))
		mux.Handle("POST /admin/dead-letters/redrive", cfg.adminMiddleware(http.HandlerFunc(h.handleRedriveDeadLetters)))
	}
```

Also extend the `NewHandler` doc-comment's admin-routes list with the two new routes.

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./transport/rest/ -run 'TestRESTListDeadLetters|TestRESTRedriveDeadLetters|TestRESTWithDeadLetterAdminNilPanics'`
Expected: PASS.

- [ ] **Step 8: Run the full REST package + lint**

Run: `go test -race ./transport/rest/... && golangci-lint run ./transport/rest/...`
Expected: PASS, lint clean.

- [ ] **Step 9: Commit**

```bash
git add transport/rest/options.go transport/rest/view.go transport/rest/admin.go transport/rest/handler.go transport/rest/dead_letters_test.go
git commit -m "feat(transport/rest): DLQ admin endpoints behind WithDeadLetterAdmin"
```

---

## Verification Checklist (run after all tasks)

- [ ] `go build ./...` clean.
- [ ] `go test -race ./...` green (run the Postgres package with `-p 1` if Docker contention appears — unrelated to this track).
- [ ] Coverage ≥85% on touched packages:
  ```bash
  go test -race -coverprofile=cover.out ./service/... ./persistence/... ./transport/grpc/... ./transport/rest/... && go tool cover -func=cover.out | tail -1
  ```
- [ ] `golangci-lint run ./...` clean.
- [ ] **Engine/model production diff is ZERO**: `git diff --stat main -- engine/ model/` shows no production (`.go` non-`_test`) changes.
- [ ] No new forbidden vendor imports in production code.
- [ ] Proto regen is reproducible: re-running the regen command leaves `git status` clean on `transport/grpc/workflowpb/`.
- [ ] Opus whole-branch review (requesting-code-review skill); address blocking findings.
- [ ] Update `docs/plans/HANDOVER.md` (mark this track complete, prune from the backlog) and the cross-session memory.

## Spec coverage self-check

- Spec §2 (DeadLetterAdmin seam) → Task 1. ✓
- Spec §3 (not-configured: REST 404 / gRPC Unimplemented) → Tasks 3 (gRPC nil branch) + 4 (REST conditional registration). ✓
- Spec §4 (gRPC proto + handlers) → Tasks 2 + 3. ✓
- Spec §5 (REST endpoints + view) → Task 4. ✓
- Spec §6 (error mapping, no new sentinels) → reuses existing `mapToGRPCStatus`/`classifyError` in Tasks 3/4. ✓
- Spec §7 (testing) → tests embedded in every task. ✓
