package fiber

import (
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// Mount is the convenience entrypoint for the common case: instance, task, and
// message routes all mounted on r with the same options.
//
// For more control — e.g. admin routes, a different base path per group, or
// group-level middleware — call each RouteGroup's Customize method directly.
func Mount(r fiberlib.Router, svc service.Service, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	InstanceRoutes{Svc: svc}.Customize(r, opts...)
	TaskRoutes{Svc: svc}.Customize(r, opts...)
	MessageRoutes{Svc: svc}.Customize(r, opts...)
}

// MountHealth mounts the health-probe routes (/healthz and /readyz) onto r.
// Checks are the readiness probes; pass none for a trivially-healthy /readyz.
func MountHealth(r fiberlib.Router, checks ...httpcore.HealthCheck) {
	HealthRoutes{Checks: checks}.Customize(r)
}
