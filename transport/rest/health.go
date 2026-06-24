package rest

import (
	"context"
	"encoding/json"
	"net/http"
)

// HealthCheck is one readiness probe a consumer registers with
// [NewHealthHandler] (ADR-0054). Name identifies the dependency in the JSON
// response; Check reports its health for a single /readyz request and MUST honour
// ctx (the handler passes the request context so a probe is bounded by the
// caller's deadline). A nil error means healthy.
//
// Implementations should be cheap and fast — /readyz is polled frequently by an
// orchestrator. A [persistence.NewPingCheck]-style probe (pool.Ping) is the
// canonical example.
type HealthCheck interface {
	Name() string
	Check(ctx context.Context) error
}

// healthCheckFunc adapts a name + func into a [HealthCheck].
type healthCheckFunc struct {
	name string
	fn   func(ctx context.Context) error
}

func (c healthCheckFunc) Name() string                    { return c.name }
func (c healthCheckFunc) Check(ctx context.Context) error { return c.fn(ctx) }

// HealthCheckFunc adapts a name and a probe function into a [HealthCheck], so a
// consumer can register an inline readiness probe without declaring a type.
func HealthCheckFunc(name string, fn func(ctx context.Context) error) HealthCheck {
	return healthCheckFunc{name: name, fn: fn}
}

// healthResponse is the JSON body returned by both probes.
type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// NewHealthHandler returns an [http.Handler] exposing two probes a consumer
// mounts in their own server (ADR-0054), mirroring the http.Handler-factory idiom
// of [NewHandler]:
//
//	GET /healthz — liveness: always 200 {"status":"ok"}. It answers "the process
//	               is up and serving"; it deliberately runs NO checks so a slow or
//	               unhealthy dependency never makes the orchestrator kill the pod.
//	GET /readyz  — readiness: runs every registered check with the request
//	               context. 200 {"status":"ok"} when all pass; 503
//	               {"status":"unavailable", "checks": {name: "ok"|"unavailable"}}
//	               naming each failing check when any fails (the raw probe error is
//	               not exposed; the check implementation owns logging the detail).
//
// The patterns are root-relative; mount under any prefix with http.StripPrefix.
func NewHealthHandler(checks ...HealthCheck) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, healthResponse{Status: "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(checks))
		ready := true
		for _, c := range checks {
			if err := c.Check(r.Context()); err != nil {
				// Name the failing check but do NOT leak the raw error: a probe
				// error can carry host/DSN fragments and /readyz may be reachable
				// by untrusted callers. The check implementation owns logging the
				// detail (the handler stays dependency-free, no telemetry seam).
				results[c.Name()] = "unavailable"
				ready = false
				continue
			}
			results[c.Name()] = "ok"
		}
		if ready {
			writeHealth(w, http.StatusOK, healthResponse{Status: "ok", Checks: results})
			return
		}
		writeHealth(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable", Checks: results})
	})
	return mux
}

// writeHealth encodes resp as JSON with the given status. Probes use the standard
// library encoder directly; a health endpoint has no telemetry handler to log
// through and must stay dependency-free so it can answer even when the engine is
// degraded.
func writeHealth(w http.ResponseWriter, status int, resp healthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
