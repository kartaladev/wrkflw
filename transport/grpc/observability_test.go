package grpctransport_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// newObsGRPCHarness is a focused bufconn harness that accepts variadic grpctransport.Option
// values, allowing tests to inject a custom TracerProvider.
func newObsGRPCHarness(t *testing.T, opts []grpctransport.Option, defs ...*model.ProcessDefinition) workflowpb.WorkflowServiceClient {
	t.Helper()

	fc := clockwork.NewFakeClock()
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})
	az := authz.RoleAuthorizer{}
	store := runtime.NewMemStore()
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": serverTestGreetAction{},
	})
	runner := runtime.NewRunner(cat, fc, store, runtime.WithHumanTasks(resolver, taskStore, az))

	defsMap := make(map[string]*model.ProcessDefinition, len(defs)*2)
	for _, d := range defs {
		defsMap[defRefFor(d)] = d
		defsMap[d.ID] = d
	}
	reg := runtime.NewMapDefinitionRegistry(defsMap)
	tasks := runtime.NewTaskService(taskStore, az, fc)
	svc := service.New(runner, tasks, reg, store, store, taskStore, fc)

	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	grpctransport.RegisterWorkflowServiceServer(grpcServer, svc, opts...)

	t.Cleanup(func() { grpcServer.Stop() })
	go func() { _ = grpcServer.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return workflowpb.NewWorkflowServiceClient(conn)
}

// TestGRPCRequestSpan verifies that a per-RPC OTel span is emitted whose name
// starts with "wrkflw.grpc". The server is registered with a real TracerProvider
// backed by a SpanRecorder so we can inspect recorded spans.
func TestGRPCRequestSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	def := serverLinearDef()
	client := newObsGRPCHarness(t, []grpctransport.Option{
		grpctransport.WithTracerProvider(tp),
	}, def)

	vars, err := structpb.NewStruct(map[string]any{"name": "world"})
	require.NoError(t, err)

	_, err = client.StartInstance(t.Context(), &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "span-test-grpc-1",
		Vars:       vars,
	})
	require.NoError(t, err)

	var sawSpan bool
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.grpc") {
			sawSpan = true
		}
	}
	if !sawSpan {
		t.Fatalf("expected a wrkflw.grpc span; got %d spans: %v",
			len(sr.Ended()), grpcSpanNames(sr.Ended()))
	}
}

// TestGRPCRequestSpanAttributes verifies that a per-RPC span carries the
// rpc.system and rpc.method attributes.
func TestGRPCRequestSpanAttributes(t *testing.T) {
	type testCase struct {
		name   string
		assert func(t *testing.T, spans []sdktrace.ReadOnlySpan)
	}

	cases := []testCase{
		{
			name: "StartInstance span carries rpc attributes",
			assert: func(t *testing.T, spans []sdktrace.ReadOnlySpan) {
				t.Helper()
				var found bool
				var gotSystem, gotMethod string
				for _, s := range spans {
					if strings.HasPrefix(s.Name(), "wrkflw.grpc") {
						found = true
						for _, attr := range s.Attributes() {
							switch string(attr.Key) {
							case "rpc.system":
								gotSystem = attr.Value.AsString()
							case "rpc.method":
								gotMethod = attr.Value.AsString()
							}
						}
					}
				}
				if !found {
					t.Fatalf("expected a wrkflw.grpc span; got %d spans: %v",
						len(spans), grpcSpanNames(spans))
				}
				if gotSystem != "grpc" {
					t.Errorf("rpc.system = %q, want %q", gotSystem, "grpc")
				}
				if gotMethod != "StartInstance" {
					t.Errorf("rpc.method = %q, want %q", gotMethod, "StartInstance")
				}
			},
		},
	}

	def := serverLinearDef()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			client := newObsGRPCHarness(t, []grpctransport.Option{
				grpctransport.WithTracerProvider(tp),
			}, def)

			vars, err := structpb.NewStruct(map[string]any{"name": "attrs"})
			require.NoError(t, err)

			_, err = client.StartInstance(t.Context(), &workflowpb.StartInstanceRequest{
				DefRef:     "greeting",
				InstanceId: "span-attr-grpc-1",
				Vars:       vars,
			})
			require.NoError(t, err)

			tc.assert(t, sr.Ended())
		})
	}
}

func grpcSpanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name()
	}
	return names
}

// TestGRPCTraceContextPropagation verifies that when a client sends W3C trace
// context via gRPC metadata, the server extracts it and the resulting RPC span
// is a child of the incoming trace (i.e. shares the same trace ID). This test
// installs a TraceContext propagator as the global propagator so that
// otel.GetTextMapPropagator() (used by the server's mdCarrier extraction) can
// decode the incoming traceparent header.
func TestGRPCTraceContextPropagation(t *testing.T) {
	// Install the W3C TraceContext propagator globally for this test.
	// The server's startSpan calls otel.GetTextMapPropagator(), so the global
	// must be set before the server processes any request.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator()) })

	// Build a parent span to inject as incoming trace context.
	parentSR := tracetest.NewSpanRecorder()
	parentTP := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(parentSR))
	parentCtx, parentSpan := parentTP.Tracer("parent").Start(t.Context(), "parent-span")
	defer parentSpan.End()

	// Record what trace ID the parent belongs to.
	wantTraceID := parentSpan.SpanContext().TraceID()

	// Build the server with its own recording TP.
	serverSR := tracetest.NewSpanRecorder()
	serverTP := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(serverSR))

	def := serverLinearDef()
	client := newObsGRPCHarness(t, []grpctransport.Option{
		grpctransport.WithTracerProvider(serverTP),
	}, def)

	// Inject W3C traceparent into a temporary map[string]string, then copy into gRPC metadata.
	prop := propagation.TraceContext{}
	mc := propagation.MapCarrier{}
	prop.Inject(parentCtx, mc)

	// Convert map[string]string → metadata.MD (MD values are []string).
	md := metadata.New(nil)
	for k, v := range mc {
		md.Set(k, v)
	}

	// Attach the metadata to the outgoing context.
	outCtx := metadata.NewOutgoingContext(t.Context(), md)

	vars, err := structpb.NewStruct(map[string]any{"name": "propagate"})
	require.NoError(t, err)

	_, err = client.StartInstance(outCtx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "propagate-test-grpc-1",
		Vars:       vars,
	})
	require.NoError(t, err)

	// The server-side span must share the same trace ID as the parent.
	var gotTraceID [16]byte
	for _, s := range serverSR.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.grpc") {
			gotTraceID = s.SpanContext().TraceID()
		}
	}
	if gotTraceID == ([16]byte{}) {
		t.Fatalf("no wrkflw.grpc span found; spans: %v", grpcSpanNames(serverSR.Ended()))
	}
	if gotTraceID != wantTraceID {
		t.Errorf("trace ID = %s, want %s (parent's trace ID)", gotTraceID, wantTraceID)
	}
}
