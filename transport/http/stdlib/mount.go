package stdlib

import (
	"net/http"

	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// Mount registers the core workflow routes (instances, tasks, messages) onto mux.
// It is a convenience wrapper over [InstanceRoutes], [TaskRoutes], and
// [MessageRoutes].Customize, all called with the same opts.
//
// Admin and health routes are intentionally excluded so consumers can choose
// whether and where to mount them — typically on a separate, access-controlled
// mux. Use [AdminRoutes.Customize] and [MountHealth] to add them.
func Mount(mux *http.ServeMux, svc service.Service, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	InstanceRoutes{Svc: svc}.Customize(mux, opts...)
	TaskRoutes{Svc: svc}.Customize(mux, opts...)
	MessageRoutes{Svc: svc}.Customize(mux, opts...)
}

// MountHealth registers the liveness (/healthz) and readiness (/readyz) probe
// endpoints onto mux. checks are the readiness probes to evaluate on GET /readyz.
// An empty checks slice means all are trivially passing, so /readyz returns 200.
func MountHealth(mux *http.ServeMux, checks ...httpcore.HealthCheck) {
	HealthRoutes{Checks: checks}.Customize(mux)
}
