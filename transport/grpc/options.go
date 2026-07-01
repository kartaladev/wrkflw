package grpctransport

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// serverConfig holds the resolved gRPC server configuration for telemetry.
type serverConfig struct {
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	// deadLetters, when non-nil, enables the DLQ admin RPCs.
	deadLetters service.DeadLetterAdmin

	// policyAdmin, when non-nil, enables the policy-admin RPCs.
	policyAdmin service.PolicyAdmin

	// relayStats, when non-nil, enables the GetRelayStats RPC.
	relayStats service.RelayStatsAdmin

	// timerAdmin, when non-nil, enables the ListTimers RPC.
	timerAdmin service.TimerAdmin

	// lineageAdmin, when non-nil, enables the GetInstanceLineage RPC.
	lineageAdmin service.LineageAdmin
}

// Option is a functional option for [RegisterWorkflowServiceServer].
type Option func(*serverConfig)

// WithLogger sets the structured logger used by the gRPC service handlers.
// A nil value is ignored and slog.Default() is kept.
func WithLogger(l *slog.Logger) Option {
	return func(c *serverConfig) { c.logOpt = observability.WithLogger(l) }
}

// WithTracerProvider sets the OTel tracer provider used by the gRPC service
// handlers for per-RPC spans. A nil value is ignored and the OTel global
// provider is used.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *serverConfig) { c.tpOpt = observability.WithTracerProvider(tp) }
}

// WithMeterProvider sets the OTel meter provider used by the gRPC service
// handlers. A nil value is ignored and the OTel global provider is used.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *serverConfig) { c.mpOpt = observability.WithMeterProvider(mp) }
}

// WithDeadLetterAdmin enables the DLQ admin RPCs (ListDeadLetters,
// RedriveDeadLetters) by supplying a [service.DeadLetterAdmin] (e.g. a
// persistence.Relay). When this option is NOT supplied, those RPCs return
// codes.Unimplemented.
//
// SECURITY: like ListInstances, the DLQ RPCs have no built-in per-method
// authorization; the consumer MUST gate them with a grpc interceptor.
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

// WithPolicyAdmin enables the policy-admin RPCs (AddPolicy, RemovePolicy,
// ListPolicies, AddRole, RemoveRole, ListRoles) by supplying a
// [service.PolicyAdmin] (e.g. a casbinauthz.PolicyAdminFor adapter). When
// this option is NOT supplied, those RPCs return codes.Unimplemented.
//
// SECURITY: like ListInstances and the DLQ RPCs, the policy-admin RPCs have
// no built-in per-method authorization; the consumer MUST gate them with a
// grpc interceptor.
//
// Panics immediately if pa is nil.
func WithPolicyAdmin(pa service.PolicyAdmin) Option {
	if pa == nil {
		panic("grpc: WithPolicyAdmin: pa must not be nil")
	}
	return func(c *serverConfig) {
		c.policyAdmin = pa
	}
}

// WithRelayStatsAdmin enables the GetRelayStats RPC by supplying a
// [service.RelayStatsAdmin] (e.g. the Postgres Relay). When this option is NOT
// supplied, GetRelayStats returns codes.Unimplemented.
//
// SECURITY: like the other admin RPCs, GetRelayStats has no built-in per-method
// authorization; the consumer MUST gate it with a grpc interceptor.
//
// Panics immediately if rsa is nil.
func WithRelayStatsAdmin(rsa service.RelayStatsAdmin) Option {
	if rsa == nil {
		panic("grpc: WithRelayStatsAdmin: rsa must not be nil")
	}
	return func(c *serverConfig) {
		c.relayStats = rsa
	}
}

// WithTimerAdmin enables the ListTimers RPC by supplying a [service.TimerAdmin]
// (e.g. the Postgres TimerStore). When this option is NOT supplied, ListTimers
// returns codes.Unimplemented.
//
// SECURITY: like the other admin RPCs, ListTimers has no built-in per-method
// authorization; the consumer MUST gate it with a grpc interceptor.
//
// Panics immediately if ta is nil.
func WithTimerAdmin(ta service.TimerAdmin) Option {
	if ta == nil {
		panic("grpc: WithTimerAdmin: ta must not be nil")
	}
	return func(c *serverConfig) {
		c.timerAdmin = ta
	}
}

// WithLineageAdmin enables the GetInstanceLineage RPC by supplying a
// [service.LineageAdmin] (e.g. *runtime.LineageReader). When this option is NOT
// supplied, GetInstanceLineage returns codes.Unimplemented.
//
// SECURITY: like the other admin RPCs, GetInstanceLineage has no built-in
// per-method authorization; the consumer MUST gate it with a grpc interceptor.
//
// Panics immediately if la is nil.
func WithLineageAdmin(la service.LineageAdmin) Option {
	if la == nil {
		panic("grpc: WithLineageAdmin: la must not be nil")
	}
	return func(c *serverConfig) {
		c.lineageAdmin = la
	}
}

// nonNilOpts returns only the non-nil observability.Option values from opts.
func nonNilOpts(opts ...observability.Option) []observability.Option {
	out := make([]observability.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}
