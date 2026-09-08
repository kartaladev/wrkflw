package fiber

import (
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// Mount is the convenience entrypoint for the common case: instance, task, and
// message routes all mounted on r with the same options.
//
// For more control — e.g. admin routes, a different base path per group, or
// group-level middleware — call each RouteGroup's Customize method directly.
//
// SECURITY: Mount registers InstanceRoutes, TaskRoutes and MessageRoutes in
// one call, so every warning on those three types applies here -- read them. In
// particular it mounts unauthenticated start, read and signal routes reaching
// ANY instance, and identity lifts rather than gates the redaction on those
// reads. Mount only onto a router group your own auth middleware already
// protects.
func Mount(r fiberlib.Router, svc service.Service, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	InstanceRoutes{Svc: svc}.Customize(r, opts...)
	TaskRoutes{Svc: svc}.Customize(r, opts...)
	MessageRoutes{Svc: svc}.Customize(r, opts...)
}

// MountHealth mounts the health-probe routes (/healthz and /readyz) onto r.
// Checks are the readiness probes; pass none for a trivially-healthy /readyz.
//
// SECURITY: MountHealth registers the liveness and readiness probes with NO
// authentication. The /readyz body names every configured check and reports
// which of them is unavailable, so it discloses your dependency topology to any
// caller that reaches it. Mount it on an internal listener, or onto a protected
// group, whenever that disclosure matters.
func MountHealth(r fiberlib.Router, checks ...httpcore.HealthCheck) {
	HealthRoutes{Checks: checks}.Customize(r)
}
