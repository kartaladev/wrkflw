package httpcore

import (
	"context"
)

// HealthCheck is one readiness probe a consumer registers with health-probe
// functions. Name identifies the dependency in the JSON response; Check reports
// its health for a single request and MUST honour ctx (the handler passes the
// request context so a probe is bounded by the caller's deadline). A nil error
// means healthy.
//
// Implementations should be cheap and fast — health probes are polled
// frequently by an orchestrator. A pool.Ping-style probe is the canonical
// example.
type HealthCheck interface {
	Name() string
	Check(ctx context.Context) error
}

// healthCheckFunc adapts a name + func into a HealthCheck.
type healthCheckFunc struct {
	name string
	fn   func(ctx context.Context) error
}

func (c healthCheckFunc) Name() string                    { return c.name }
func (c healthCheckFunc) Check(ctx context.Context) error { return c.fn(ctx) }

// HealthCheckFunc adapts a name and a probe function into a HealthCheck, so a
// consumer can register an inline readiness probe without declaring a type.
func HealthCheckFunc(name string, fn func(ctx context.Context) error) HealthCheck {
	return healthCheckFunc{name: name, fn: fn}
}

// EvaluateReady runs every registered check with the given context. It returns:
//   - (200, {"status":"ok","checks":{name:"ok",...}}) when all checks pass.
//   - (503, {"status":"unavailable","checks":{name:"ok"|"unavailable",...}}) when any check fails.
//
// Check names are always present in the response. Failing checks are marked
// "unavailable" rather than exposing the raw error (which may contain sensitive
// details like host/DSN fragments). The check implementation owns logging the
// detail.
func EvaluateReady(ctx context.Context, checks []HealthCheck) (int, any) {
	results := make(map[string]string, len(checks))
	ready := true

	for _, c := range checks {
		if err := c.Check(ctx); err != nil {
			results[c.Name()] = "unavailable"
			ready = false
			continue
		}
		results[c.Name()] = "ok"
	}

	resp := map[string]any{
		"status": "ok",
		"checks": results,
	}

	if ready {
		return 200, resp
	}

	resp["status"] = "unavailable"
	return 503, resp
}

// EvaluateLive returns a static liveness response. It always returns 200
// because liveness probes MUST NOT run expensive checks; they only verify
// the process is up and serving. A slow or unhealthy dependency should never
// make the orchestrator kill the pod.
func EvaluateLive(ctx context.Context) (int, any) {
	resp := map[string]any{
		"status": "ok",
	}
	return 200, resp
}
