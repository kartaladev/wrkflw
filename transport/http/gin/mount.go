package gin

import (
	ginlib "github.com/gin-gonic/gin"

	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// Mount registers InstanceRoutes, TaskRoutes, and MessageRoutes for svc onto r
// with the supplied opts (base path, middleware, observability, etc.).
//
// For admin and health endpoints call AdminRoutes.Customize and MountHealth separately.
func Mount(r ginlib.IRouter, svc service.Service, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	InstanceRoutes{Svc: svc}.Customize(r, opts...)
	TaskRoutes{Svc: svc}.Customize(r, opts...)
	MessageRoutes{Svc: svc}.Customize(r, opts...)
}

// MountHealth registers HealthRoutes (GET /healthz and GET /readyz) onto r.
// Pass optional readiness checks; liveness (/healthz) always returns 200.
func MountHealth(r ginlib.IRouter, checks ...httpcore.HealthCheck) {
	HealthRoutes{Checks: checks}.Customize(r)
}
