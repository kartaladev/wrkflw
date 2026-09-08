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
//
// SECURITY: Mount registers InstanceRoutes, TaskRoutes and MessageRoutes in
// one call, so every warning on those three types applies here -- read them. In
// particular it mounts unauthenticated start, read and signal routes reaching
// ANY instance, and identity lifts rather than gates the redaction on those
// reads. Mount only onto a router group your own auth middleware already
// protects.
func Mount(r ginlib.IRouter, svc service.Service, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	InstanceRoutes{Svc: svc}.Customize(r, opts...)
	TaskRoutes{Svc: svc}.Customize(r, opts...)
	MessageRoutes{Svc: svc}.Customize(r, opts...)
}

// MountHealth registers HealthRoutes (GET /healthz and GET /readyz) onto r.
// Pass optional readiness checks; liveness (/healthz) always returns 200.
//
// SECURITY: MountHealth registers the liveness and readiness probes with NO
// authentication. The /readyz body names every configured check and reports
// which of them is unavailable, so it discloses your dependency topology to any
// caller that reaches it. Mount it on an internal listener, or onto a protected
// group, whenever that disclosure matters.
func MountHealth(r ginlib.IRouter, checks ...httpcore.HealthCheck) {
	HealthRoutes{Checks: checks}.Customize(r)
}
