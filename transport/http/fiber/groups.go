package fiber

import (
	"fmt"
	"strconv"

	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// InstanceRoutes mounts the process-instance routes onto a fiber.Router:
//
//	POST   {basePath}/instances
//	GET    {basePath}/instances/:id
//	GET    {basePath}/instances/:id/snapshot
//	GET    {basePath}/instances/:id/actionable
//	POST   {basePath}/instances/:id/signals
type InstanceRoutes struct {
	Svc service.Service
}

// Customize implements httpcore.RouteCustomizer[fiber.Router].
func (g InstanceRoutes) Customize(r fiberlib.Router, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)

	rt.Post(cfg.BasePath+"/instances", observed(inst, "POST", cfg.BasePath+"/instances",
		func(c fiberlib.Ctx) error {
			var in httpcore.StartInput
			if err := c.Bind().JSON(&in); err != nil {
				return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			}
			status, body, err := httpcore.StartInstance(c.Context(), g.Svc, in, cfg.InstanceMapper)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	rt.Get(cfg.BasePath+"/instances/:id", observed(inst, "GET", cfg.BasePath+"/instances/:id",
		func(c fiberlib.Ctx) error {
			id := c.Params("id")
			status, body, err := httpcore.GetInstance(c.Context(), g.Svc, id, cfg.InstanceMapper)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	rt.Get(cfg.BasePath+"/instances/:id/snapshot", observed(inst, "GET", cfg.BasePath+"/instances/:id/snapshot",
		func(c fiberlib.Ctx) error {
			id := c.Params("id")
			status, body, err := httpcore.GetInstanceSnapshot(c.Context(), g.Svc, id)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	rt.Get(cfg.BasePath+"/instances/:id/actionable", observed(inst, "GET", cfg.BasePath+"/instances/:id/actionable",
		func(c fiberlib.Ctx) error {
			id := c.Params("id")
			status, body, err := httpcore.GetActionableView(c.Context(), g.Svc, id)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	rt.Post(cfg.BasePath+"/instances/:id/signals", observed(inst, "POST", cfg.BasePath+"/instances/:id/signals",
		func(c fiberlib.Ctx) error {
			id := c.Params("id")
			var in httpcore.SignalInput
			if err := c.Bind().JSON(&in); err != nil {
				return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			}
			status, body, err := httpcore.DeliverSignal(c.Context(), g.Svc, id, in, cfg.InstanceMapper)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))
}

// MessageRoutes mounts the message-delivery route onto a fiber.Router:
//
//	POST   {basePath}/messages
type MessageRoutes struct {
	Svc service.Service
}

// Customize implements httpcore.RouteCustomizer[fiber.Router].
func (g MessageRoutes) Customize(r fiberlib.Router, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)

	rt.Post(cfg.BasePath+"/messages", observed(inst, "POST", cfg.BasePath+"/messages",
		func(c fiberlib.Ctx) error {
			var in httpcore.MessageInput
			if err := c.Bind().JSON(&in); err != nil {
				return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			}
			status, _, err := httpcore.DeliverMessage(c.Context(), g.Svc, in)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.SendStatus(status)
		}))
}

// TaskRoutes mounts the human-task routes onto a fiber.Router:
//
//	POST   {basePath}/tasks/:token/claim
//	POST   {basePath}/tasks/:token/complete
//	POST   {basePath}/tasks/:token/reassign
type TaskRoutes struct {
	Svc service.Service
}

// Customize implements httpcore.RouteCustomizer[fiber.Router].
func (g TaskRoutes) Customize(r fiberlib.Router, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)

	rt.Post(cfg.BasePath+"/tasks/:token/claim", observed(inst, "POST", cfg.BasePath+"/tasks/:token/claim",
		func(c fiberlib.Ctx) error {
			token := c.Params("token")
			var in httpcore.ClaimInput
			if err := c.Bind().JSON(&in); err != nil {
				return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			}
			status, body, err := httpcore.ClaimTask(c.Context(), g.Svc, token, in, cfg.InstanceMapper)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	rt.Post(cfg.BasePath+"/tasks/:token/complete", observed(inst, "POST", cfg.BasePath+"/tasks/:token/complete",
		func(c fiberlib.Ctx) error {
			token := c.Params("token")
			var in httpcore.CompleteInput
			if err := c.Bind().JSON(&in); err != nil {
				return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			}
			status, body, err := httpcore.CompleteTask(c.Context(), g.Svc, token, in, cfg.InstanceMapper)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	rt.Post(cfg.BasePath+"/tasks/:token/reassign", observed(inst, "POST", cfg.BasePath+"/tasks/:token/reassign",
		func(c fiberlib.Ctx) error {
			token := c.Params("token")
			var in httpcore.ReassignInput
			if err := c.Bind().JSON(&in); err != nil {
				return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			}
			status, body, err := httpcore.ReassignTask(c.Context(), g.Svc, token, in, cfg.InstanceMapper)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))
}

// AdminRoutes mounts admin-only routes onto a fiber.Router. Routes for
// optional sub-interfaces (DeadLetters, Policies, RelayStats, Timers, Lineage)
// are only registered when the corresponding field is non-nil.
//
//	GET    {basePath}/admin/instances
//	POST   {basePath}/admin/instances/:id/incidents/:incidentID/resolve
//	POST   {basePath}/admin/instances/:id/cancel
//
// Conditionally (when DeadLetters != nil):
//
//	GET    {basePath}/admin/dead-letters
//	POST   {basePath}/admin/dead-letters/redrive
//
// Conditionally (when Policies != nil):
//
//	GET    {basePath}/admin/policies
//	POST   {basePath}/admin/policies
//	DELETE {basePath}/admin/policies
//	GET    {basePath}/admin/role-bindings
//	POST   {basePath}/admin/role-bindings
//	DELETE {basePath}/admin/role-bindings
//
// Conditionally (when RelayStats != nil):
//
//	GET    {basePath}/admin/relay-stats
//
// Conditionally (when Timers != nil):
//
//	GET    {basePath}/admin/timers
//
// Conditionally (when Lineage != nil):
//
//	GET    {basePath}/admin/instances/:id/lineage
type AdminRoutes struct {
	Svc         service.Service
	DeadLetters service.DeadLetterAdmin
	Policies    service.PolicyAdmin
	RelayStats  service.RelayStatsAdmin
	Timers      service.TimerAdmin
	Lineage     service.LineageAdmin
}

// Customize implements httpcore.RouteCustomizer[fiber.Router].
func (g AdminRoutes) Customize(r fiberlib.Router, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)

	// List instances.
	rt.Get(cfg.BasePath+"/admin/instances", observed(inst, "GET", cfg.BasePath+"/admin/instances",
		func(c fiberlib.Ctx) error {
			q := httpcore.ListInstancesQuery{
				Status: c.Query("status"),
				Cursor: c.Query("cursor"),
			}
			if s := c.Query("limit"); s != "" {
				if lim, err := strconv.Atoi(s); err == nil && lim > 0 {
					q.Limit = lim
				}
			}
			q.IncludeTotal = c.Query("total") == "true"
			status, body, err := httpcore.AdminListInstances(c.Context(), g.Svc, q)
			if err != nil {
				return writeErr(cfg, c, err)
			}
			return c.Status(status).JSON(body)
		}))

	// Resolve incident.
	rt.Post(cfg.BasePath+"/admin/instances/:id/incidents/:incidentID/resolve",
		observed(inst, "POST", cfg.BasePath+"/admin/instances/:id/incidents/:incidentID/resolve",
			func(c fiberlib.Ctx) error {
				id := c.Params("id")
				incidentID := c.Params("incidentID")
				var in httpcore.ResolveIncidentInput
				// Body is optional — ignore decode errors (defaults to zero AddAttempts).
				_ = c.Bind().JSON(&in)
				status, body, err := httpcore.ResolveIncident(c.Context(), g.Svc, id, incidentID, in)
				if err != nil {
					return writeErr(cfg, c, err)
				}
				return c.Status(status).JSON(body)
			}))

	// Cancel instance.
	rt.Post(cfg.BasePath+"/admin/instances/:id/cancel",
		observed(inst, "POST", cfg.BasePath+"/admin/instances/:id/cancel",
			func(c fiberlib.Ctx) error {
				id := c.Params("id")
				status, body, err := httpcore.CancelInstance(c.Context(), g.Svc, id)
				if err != nil {
					return writeErr(cfg, c, err)
				}
				return c.Status(status).JSON(body)
			}))

	// Conditional: dead-letters.
	if g.DeadLetters != nil {
		dl := g.DeadLetters
		rt.Get(cfg.BasePath+"/admin/dead-letters",
			observed(inst, "GET", cfg.BasePath+"/admin/dead-letters",
				func(c fiberlib.Ctx) error {
					q := httpcore.DeadLetterQuery{}
					if s := c.Query("limit"); s != "" {
						if lim, err := strconv.Atoi(s); err == nil && lim > 0 {
							q.Limit = lim
						}
					}
					status, body, err := httpcore.ListDeadLetters(c.Context(), dl, q)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))

		rt.Post(cfg.BasePath+"/admin/dead-letters/redrive",
			observed(inst, "POST", cfg.BasePath+"/admin/dead-letters/redrive",
				func(c fiberlib.Ctx) error {
					var in httpcore.RedriveInput
					if err := c.Bind().JSON(&in); err != nil {
						return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					}
					status, body, err := httpcore.RedriveDeadLetters(c.Context(), dl, in)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))
	}

	// Conditional: policies + role-bindings.
	if g.Policies != nil {
		pa := g.Policies

		rt.Get(cfg.BasePath+"/admin/policies",
			observed(inst, "GET", cfg.BasePath+"/admin/policies",
				func(c fiberlib.Ctx) error {
					status, body, err := httpcore.ListPolicies(c.Context(), pa)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))

		rt.Post(cfg.BasePath+"/admin/policies",
			observed(inst, "POST", cfg.BasePath+"/admin/policies",
				func(c fiberlib.Ctx) error {
					var in httpcore.PolicyRuleInput
					if err := c.Bind().JSON(&in); err != nil {
						return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					}
					status, body, err := httpcore.AddPolicy(c.Context(), pa, in)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))

		rt.Delete(cfg.BasePath+"/admin/policies",
			observed(inst, "DELETE", cfg.BasePath+"/admin/policies",
				func(c fiberlib.Ctx) error {
					var in httpcore.PolicyRuleInput
					if err := c.Bind().JSON(&in); err != nil {
						return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					}
					status, body, err := httpcore.RemovePolicy(c.Context(), pa, in)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))

		rt.Get(cfg.BasePath+"/admin/role-bindings",
			observed(inst, "GET", cfg.BasePath+"/admin/role-bindings",
				func(c fiberlib.Ctx) error {
					status, body, err := httpcore.ListRoleBindings(c.Context(), pa)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))

		rt.Post(cfg.BasePath+"/admin/role-bindings",
			observed(inst, "POST", cfg.BasePath+"/admin/role-bindings",
				func(c fiberlib.Ctx) error {
					var in httpcore.RoleBindingInput
					if err := c.Bind().JSON(&in); err != nil {
						return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					}
					status, body, err := httpcore.AddRoleBinding(c.Context(), pa, in)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))

		rt.Delete(cfg.BasePath+"/admin/role-bindings",
			observed(inst, "DELETE", cfg.BasePath+"/admin/role-bindings",
				func(c fiberlib.Ctx) error {
					var in httpcore.RoleBindingInput
					if err := c.Bind().JSON(&in); err != nil {
						return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					}
					status, body, err := httpcore.RemoveRoleBinding(c.Context(), pa, in)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))
	}

	// Conditional: relay stats.
	if g.RelayStats != nil {
		rs := g.RelayStats
		rt.Get(cfg.BasePath+"/admin/relay-stats",
			observed(inst, "GET", cfg.BasePath+"/admin/relay-stats",
				func(c fiberlib.Ctx) error {
					status, body, err := httpcore.AdminRelayStats(c.Context(), rs)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))
	}

	// Conditional: timers.
	if g.Timers != nil {
		ta := g.Timers
		rt.Get(cfg.BasePath+"/admin/timers",
			observed(inst, "GET", cfg.BasePath+"/admin/timers",
				func(c fiberlib.Ctx) error {
					status, body, err := httpcore.AdminTimers(c.Context(), ta)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))
	}

	// Conditional: lineage.
	if g.Lineage != nil {
		la := g.Lineage
		rt.Get(cfg.BasePath+"/admin/instances/:id/lineage",
			observed(inst, "GET", cfg.BasePath+"/admin/instances/:id/lineage",
				func(c fiberlib.Ctx) error {
					id := c.Params("id")
					status, body, err := httpcore.AdminInstanceLineage(c.Context(), la, id)
					if err != nil {
						return writeErr(cfg, c, err)
					}
					return c.Status(status).JSON(body)
				}))
	}
}

// HealthRoutes mounts health-probe routes onto a fiber.Router:
//
//	GET   {basePath}/healthz  — liveness (always 200)
//	GET   {basePath}/readyz   — readiness (200 / 503, runs checks)
type HealthRoutes struct {
	Checks []httpcore.HealthCheck
}

// Customize implements httpcore.RouteCustomizer[fiber.Router].
func (g HealthRoutes) Customize(r fiberlib.Router, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)

	rt.Get(cfg.BasePath+"/healthz", observed(inst, "GET", cfg.BasePath+"/healthz",
		func(c fiberlib.Ctx) error {
			status, body := httpcore.EvaluateLive(c.Context())
			return c.Status(status).JSON(body)
		}))

	checks := g.Checks
	rt.Get(cfg.BasePath+"/readyz", observed(inst, "GET", cfg.BasePath+"/readyz",
		func(c fiberlib.Ctx) error {
			status, body := httpcore.EvaluateReady(c.Context(), checks)
			return c.Status(status).JSON(body)
		}))
}
