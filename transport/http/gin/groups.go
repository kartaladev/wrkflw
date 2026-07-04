package gin

import (
	"fmt"
	"net/http"
	"strconv"

	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// ─── InstanceRoutes ───────────────────────────────────────────────────────────

// InstanceRoutes mounts the five instance-lifecycle endpoints onto a gin.IRouter.
// It implements httpcore.RouteCustomizer[gin.IRouter].
type InstanceRoutes struct {
	// Svc is the application service. Must not be nil.
	Svc service.Service
}

// Customize registers instance routes on r, applying the given opts.
func (ir InstanceRoutes) Customize(r ginlib.IRouter, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)
	bp := cfg.BasePath

	// POST /instances → StartInstance
	rt.POST(bp+"/instances", observe(inst, http.MethodPost, bp+"/instances", func(gc *ginlib.Context) {
		var in httpcore.StartInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.StartInstance(gc.Request.Context(), ir.Svc, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// GET /instances/:id → GetInstance
	rt.GET(bp+"/instances/:id", observe(inst, http.MethodGet, bp+"/instances/:id", func(gc *ginlib.Context) {
		id := gc.Param("id")
		status, body, err := httpcore.GetInstance(gc.Request.Context(), ir.Svc, id, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// GET /instances/:id/snapshot → GetInstanceSnapshot
	rt.GET(bp+"/instances/:id/snapshot", observe(inst, http.MethodGet, bp+"/instances/:id/snapshot", func(gc *ginlib.Context) {
		id := gc.Param("id")
		status, body, err := httpcore.GetInstanceSnapshot(gc.Request.Context(), ir.Svc, id)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// GET /instances/:id/actionable → GetActionableView
	rt.GET(bp+"/instances/:id/actionable", observe(inst, http.MethodGet, bp+"/instances/:id/actionable", func(gc *ginlib.Context) {
		id := gc.Param("id")
		status, body, err := httpcore.GetActionableView(gc.Request.Context(), ir.Svc, id)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// POST /instances/:id/signals → DeliverSignal
	rt.POST(bp+"/instances/:id/signals", observe(inst, http.MethodPost, bp+"/instances/:id/signals", func(gc *ginlib.Context) {
		id := gc.Param("id")
		var in httpcore.SignalInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.DeliverSignal(gc.Request.Context(), ir.Svc, id, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))
}

// ─── MessageRoutes ────────────────────────────────────────────────────────────

// MessageRoutes mounts the message-delivery endpoint onto a gin.IRouter.
// It implements httpcore.RouteCustomizer[gin.IRouter].
type MessageRoutes struct {
	// Svc is the application service. Must not be nil.
	Svc service.Service
}

// Customize registers message routes on r, applying the given opts.
func (mr MessageRoutes) Customize(r ginlib.IRouter, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)
	bp := cfg.BasePath

	// POST /messages → DeliverMessage
	rt.POST(bp+"/messages", observe(inst, http.MethodPost, bp+"/messages", func(gc *ginlib.Context) {
		var in httpcore.MessageInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.DeliverMessage(gc.Request.Context(), mr.Svc, in)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		if body == nil {
			gc.Status(status)
			return
		}
		gc.JSON(status, body)
	}))
}

// ─── TaskRoutes ───────────────────────────────────────────────────────────────

// TaskRoutes mounts the three human-task action endpoints onto a gin.IRouter.
// It implements httpcore.RouteCustomizer[gin.IRouter].
type TaskRoutes struct {
	// Svc is the application service. Must not be nil.
	Svc service.Service
}

// Customize registers task routes on r, applying the given opts.
func (tr TaskRoutes) Customize(r ginlib.IRouter, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)
	bp := cfg.BasePath

	// POST /tasks/:token/claim
	rt.POST(bp+"/tasks/:token/claim", observe(inst, http.MethodPost, bp+"/tasks/:token/claim", func(gc *ginlib.Context) {
		token := gc.Param("token")
		var in httpcore.ClaimInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.ClaimTask(gc.Request.Context(), tr.Svc, token, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// POST /tasks/:token/complete
	rt.POST(bp+"/tasks/:token/complete", observe(inst, http.MethodPost, bp+"/tasks/:token/complete", func(gc *ginlib.Context) {
		token := gc.Param("token")
		var in httpcore.CompleteInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.CompleteTask(gc.Request.Context(), tr.Svc, token, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// POST /tasks/:token/reassign
	rt.POST(bp+"/tasks/:token/reassign", observe(inst, http.MethodPost, bp+"/tasks/:token/reassign", func(gc *ginlib.Context) {
		token := gc.Param("token")
		var in httpcore.ReassignInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.ReassignTask(gc.Request.Context(), tr.Svc, token, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))
}

// ─── AdminRoutes ──────────────────────────────────────────────────────────────

// AdminRoutes mounts the administrative endpoints onto a gin.IRouter.
// Optional fields (DeadLetters, Policies, RelayStats, Timers, Lineage) are only
// registered when non-nil, so consumers can compose only the capabilities they
// have wired.
//
// It implements httpcore.RouteCustomizer[gin.IRouter].
type AdminRoutes struct {
	// Svc provides ListInstances, ResolveIncident, and CancelInstance.
	// Must not be nil.
	Svc service.Service

	// DeadLetters enables GET /admin/dead-letters and POST /admin/dead-letters/redrive
	// when non-nil.
	DeadLetters service.DeadLetterAdmin

	// Policies enables the policy and role-binding admin endpoints when non-nil.
	Policies service.PolicyAdmin

	// RelayStats enables GET /admin/relay-stats when non-nil.
	RelayStats service.RelayStatsAdmin

	// Timers enables GET /admin/timers when non-nil.
	Timers service.TimerAdmin

	// Lineage enables GET /admin/instances/:id/lineage when non-nil.
	Lineage service.LineageAdmin
}

// Customize registers admin routes on r, applying the given opts.
func (ar AdminRoutes) Customize(r ginlib.IRouter, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)
	bp := cfg.BasePath

	// GET /admin/instances
	rt.GET(bp+"/admin/instances", observe(inst, http.MethodGet, bp+"/admin/instances", func(gc *ginlib.Context) {
		q := httpcore.ListInstancesQuery{
			Status: gc.Query("status"),
			Cursor: gc.Query("cursor"),
		}
		if lim := gc.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				q.Limit = n
			}
		}
		if tot := gc.Query("total"); tot == "true" || tot == "1" {
			q.IncludeTotal = true
		}
		status, body, err := httpcore.AdminListInstances(gc.Request.Context(), ar.Svc, q)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))

	// POST /admin/instances/:id/incidents/:incidentID/resolve
	rt.POST(bp+"/admin/instances/:id/incidents/:incidentID/resolve",
		observe(inst, http.MethodPost, bp+"/admin/instances/:id/incidents/:incidentID/resolve", func(gc *ginlib.Context) {
			instanceID := gc.Param("id")
			incidentID := gc.Param("incidentID")
			var in httpcore.ResolveIncidentInput
			// Body is optional; ignore parse error for an empty body.
			_ = gc.ShouldBindJSON(&in)
			status, body, err := httpcore.ResolveIncident(gc.Request.Context(), ar.Svc, instanceID, incidentID, in)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

	// POST /admin/instances/:id/cancel
	rt.POST(bp+"/admin/instances/:id/cancel",
		observe(inst, http.MethodPost, bp+"/admin/instances/:id/cancel", func(gc *ginlib.Context) {
			instanceID := gc.Param("id")
			status, body, err := httpcore.CancelInstance(gc.Request.Context(), ar.Svc, instanceID)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

	// Dead-letters (conditional).
	if ar.DeadLetters != nil {
		dlDep := ar.DeadLetters

		rt.GET(bp+"/admin/dead-letters", observe(inst, http.MethodGet, bp+"/admin/dead-letters", func(gc *ginlib.Context) {
			q := httpcore.DeadLetterQuery{}
			if lim := gc.Query("limit"); lim != "" {
				if n, err := strconv.Atoi(lim); err == nil {
					q.Limit = n
				}
			}
			status, body, err := httpcore.ListDeadLetters(gc.Request.Context(), dlDep, q)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

		rt.POST(bp+"/admin/dead-letters/redrive", observe(inst, http.MethodPost, bp+"/admin/dead-letters/redrive", func(gc *ginlib.Context) {
			var in httpcore.RedriveInput
			if err := gc.ShouldBindJSON(&in); err != nil {
				writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
				return
			}
			status, body, err := httpcore.RedriveDeadLetters(gc.Request.Context(), dlDep, in)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))
	}

	// Policies + role-bindings (conditional).
	if ar.Policies != nil {
		polDep := ar.Policies

		rt.GET(bp+"/admin/policies", observe(inst, http.MethodGet, bp+"/admin/policies", func(gc *ginlib.Context) {
			status, body, err := httpcore.ListPolicies(gc.Request.Context(), polDep)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

		rt.POST(bp+"/admin/policies", observe(inst, http.MethodPost, bp+"/admin/policies", func(gc *ginlib.Context) {
			var in httpcore.PolicyRuleInput
			if err := gc.ShouldBindJSON(&in); err != nil {
				writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
				return
			}
			status, body, err := httpcore.AddPolicy(gc.Request.Context(), polDep, in)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

		rt.DELETE(bp+"/admin/policies", observe(inst, http.MethodDelete, bp+"/admin/policies", func(gc *ginlib.Context) {
			var in httpcore.PolicyRuleInput
			if err := gc.ShouldBindJSON(&in); err != nil {
				writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
				return
			}
			status, body, err := httpcore.RemovePolicy(gc.Request.Context(), polDep, in)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

		rt.GET(bp+"/admin/role-bindings", observe(inst, http.MethodGet, bp+"/admin/role-bindings", func(gc *ginlib.Context) {
			status, body, err := httpcore.ListRoleBindings(gc.Request.Context(), polDep)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

		rt.POST(bp+"/admin/role-bindings", observe(inst, http.MethodPost, bp+"/admin/role-bindings", func(gc *ginlib.Context) {
			var in httpcore.RoleBindingInput
			if err := gc.ShouldBindJSON(&in); err != nil {
				writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
				return
			}
			status, body, err := httpcore.AddRoleBinding(gc.Request.Context(), polDep, in)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))

		rt.DELETE(bp+"/admin/role-bindings", observe(inst, http.MethodDelete, bp+"/admin/role-bindings", func(gc *ginlib.Context) {
			var in httpcore.RoleBindingInput
			if err := gc.ShouldBindJSON(&in); err != nil {
				writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
				return
			}
			status, body, err := httpcore.RemoveRoleBinding(gc.Request.Context(), polDep, in)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))
	}

	// Relay stats (conditional).
	if ar.RelayStats != nil {
		rsDep := ar.RelayStats
		rt.GET(bp+"/admin/relay-stats", observe(inst, http.MethodGet, bp+"/admin/relay-stats", func(gc *ginlib.Context) {
			status, body, err := httpcore.AdminRelayStats(gc.Request.Context(), rsDep)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))
	}

	// Timers (conditional).
	if ar.Timers != nil {
		tmDep := ar.Timers
		rt.GET(bp+"/admin/timers", observe(inst, http.MethodGet, bp+"/admin/timers", func(gc *ginlib.Context) {
			status, body, err := httpcore.AdminTimers(gc.Request.Context(), tmDep)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))
	}

	// Lineage (conditional).
	if ar.Lineage != nil {
		linDep := ar.Lineage
		rt.GET(bp+"/admin/instances/:id/lineage", observe(inst, http.MethodGet, bp+"/admin/instances/:id/lineage", func(gc *ginlib.Context) {
			instanceID := gc.Param("id")
			status, body, err := httpcore.AdminInstanceLineage(gc.Request.Context(), linDep, instanceID)
			if err != nil {
				writeErr(cfg, gc, err)
				return
			}
			gc.JSON(status, body)
		}))
	}
}

// ─── HealthRoutes ─────────────────────────────────────────────────────────────

// HealthRoutes mounts the /healthz and /readyz probes onto a gin.IRouter.
// It implements httpcore.RouteCustomizer[gin.IRouter].
type HealthRoutes struct {
	// Checks are evaluated by /readyz. An empty slice means always healthy.
	Checks []httpcore.HealthCheck
}

// Customize registers health routes on r, applying the given opts.
func (hr HealthRoutes) Customize(r ginlib.IRouter, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)
	bp := cfg.BasePath

	// GET /healthz → EvaluateLive
	rt.GET(bp+"/healthz", observe(inst, http.MethodGet, bp+"/healthz", func(gc *ginlib.Context) {
		status, body := httpcore.EvaluateLive(gc.Request.Context())
		gc.JSON(status, body)
	}))

	// GET /readyz → EvaluateReady
	rt.GET(bp+"/readyz", observe(inst, http.MethodGet, bp+"/readyz", func(gc *ginlib.Context) {
		status, body := httpcore.EvaluateReady(gc.Request.Context(), hr.Checks)
		gc.JSON(status, body)
	}))
}
