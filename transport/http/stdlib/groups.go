package stdlib

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// handle registers a single handler on mux using stdlib's "METHOD path" pattern
// syntax. The handler is wrapped by observe so every route records OTel spans
// and metrics against the STATIC routeTemplate.
func handle(
	mux *http.ServeMux,
	inst *httpcore.Instrumentation,
	cfg httpcore.CustomizeConfig[*http.ServeMux],
	method, pattern string,
	h http.HandlerFunc,
) {
	routeTemplate := cfg.BasePath + pattern
	mux.HandleFunc(method+" "+routeTemplate, observe(inst, method, routeTemplate, h))
}

// InstanceRoutes mounts the core workflow-instance endpoints onto a *http.ServeMux.
// It implements [httpcore.RouteCustomizer][*http.ServeMux].
type InstanceRoutes struct {
	Svc service.Service
}

// Customize resolves cfg from opts, applies cfg.BasePath as a pattern prefix,
// and registers all instance-related routes.
func (c InstanceRoutes) Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	r := cfg.Wrap(mux)

	handle(r, inst, cfg, http.MethodPost, "/instances", func(w http.ResponseWriter, req *http.Request) {
		var in httpcore.StartInput
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.StartInstance(req.Context(), c.Svc, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodGet, "/instances/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		status, body, err := httpcore.GetInstance(req.Context(), c.Svc, id, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodGet, "/instances/{id}/snapshot", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		status, body, err := httpcore.GetInstanceSnapshot(req.Context(), c.Svc, id)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodGet, "/instances/{id}/actionable", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		status, body, err := httpcore.GetActionableView(req.Context(), c.Svc, id)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodPost, "/instances/{id}/signals", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		var in httpcore.SignalInput
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.DeliverSignal(req.Context(), c.Svc, id, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})
}

// MessageRoutes mounts the message-delivery endpoint onto a *http.ServeMux.
// It implements [httpcore.RouteCustomizer][*http.ServeMux].
type MessageRoutes struct {
	Svc service.Service
}

// Customize registers the POST /messages route.
func (c MessageRoutes) Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	r := cfg.Wrap(mux)

	handle(r, inst, cfg, http.MethodPost, "/messages", func(w http.ResponseWriter, req *http.Request) {
		var in httpcore.MessageInput
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.DeliverMessage(req.Context(), c.Svc, in)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})
}

// TaskRoutes mounts the human-task lifecycle endpoints onto a *http.ServeMux.
// It implements [httpcore.RouteCustomizer][*http.ServeMux].
type TaskRoutes struct {
	Svc service.Service
}

// Customize registers claim, complete, and reassign routes.
func (c TaskRoutes) Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	r := cfg.Wrap(mux)

	handle(r, inst, cfg, http.MethodPost, "/tasks/{token}/claim", func(w http.ResponseWriter, req *http.Request) {
		token := req.PathValue("token")
		var in httpcore.ClaimInput
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.ClaimTask(req.Context(), c.Svc, token, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodPost, "/tasks/{token}/complete", func(w http.ResponseWriter, req *http.Request) {
		token := req.PathValue("token")
		var in httpcore.CompleteInput
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.CompleteTask(req.Context(), c.Svc, token, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodPost, "/tasks/{token}/reassign", func(w http.ResponseWriter, req *http.Request) {
		token := req.PathValue("token")
		var in httpcore.ReassignInput
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.ReassignTask(req.Context(), c.Svc, token, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})
}

// AdminRoutes mounts the admin endpoints onto a *http.ServeMux.
// Optional dep fields (DeadLetters, Policies, RelayStats, Timers, Lineage) are
// guarded: if nil, their conditional routes are not registered.
// It implements [httpcore.RouteCustomizer][*http.ServeMux].
// SECURITY: these routes have NO built-in authentication. Mount AdminRoutes only
// onto a router group already protected by your auth middleware (admin-by-
// composition, ADR-0095); otherwise the admin endpoints are exposed unauthenticated.
type AdminRoutes struct {
	Svc         service.Service
	DeadLetters service.DeadLetterAdmin
	Policies    service.PolicyAdmin
	RelayStats  service.RelayStatsAdmin
	Timers      service.TimerAdmin
	Lineage     service.LineageAdmin
}

// Customize registers all admin routes. Conditional routes are only registered
// when their corresponding dep field is non-nil.
//
//nolint:cyclop // route registration is intentionally verbose for clarity
func (c AdminRoutes) Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	r := cfg.Wrap(mux)

	// --- Always-present admin routes ---

	handle(r, inst, cfg, http.MethodGet, "/admin/instances", func(w http.ResponseWriter, req *http.Request) {
		q := httpcore.ListInstancesQuery{
			Status: req.URL.Query().Get("status"),
			Cursor: req.URL.Query().Get("cursor"),
		}
		if lv := req.URL.Query().Get("limit"); lv != "" {
			if n, err := strconv.Atoi(lv); err == nil {
				q.Limit = n
			}
		}
		if tv := req.URL.Query().Get("total"); tv == "true" || tv == "1" {
			q.IncludeTotal = true
		}
		status, body, err := httpcore.AdminListInstances(req.Context(), c.Svc, q)
		if err != nil {
			writeErr(cfg, w, req, err)
			return
		}
		writeJSON(w, status, body)
	})

	handle(r, inst, cfg, http.MethodPost, "/admin/instances/{id}/incidents/{incidentID}/resolve",
		func(w http.ResponseWriter, req *http.Request) {
			instanceID := req.PathValue("id")
			incidentID := req.PathValue("incidentID")
			var in httpcore.ResolveIncidentInput
			_ = json.NewDecoder(req.Body).Decode(&in) // body is optional
			status, body, err := httpcore.ResolveIncident(req.Context(), c.Svc, instanceID, incidentID, in)
			if err != nil {
				writeErr(cfg, w, req, err)
				return
			}
			writeJSON(w, status, body)
		})

	handle(r, inst, cfg, http.MethodPost, "/admin/instances/{id}/cancel",
		func(w http.ResponseWriter, req *http.Request) {
			instanceID := req.PathValue("id")
			status, body, err := httpcore.CancelInstance(req.Context(), c.Svc, instanceID)
			if err != nil {
				writeErr(cfg, w, req, err)
				return
			}
			writeJSON(w, status, body)
		})

	// --- Conditional: DeadLetters ---

	if c.DeadLetters != nil {
		dl := c.DeadLetters // capture for closure

		handle(r, inst, cfg, http.MethodGet, "/admin/dead-letters",
			func(w http.ResponseWriter, req *http.Request) {
				q := httpcore.DeadLetterQuery{}
				if lv := req.URL.Query().Get("limit"); lv != "" {
					if n, err := strconv.Atoi(lv); err == nil {
						q.Limit = n
					}
				}
				status, body, err := httpcore.ListDeadLetters(req.Context(), dl, q)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})

		handle(r, inst, cfg, http.MethodPost, "/admin/dead-letters/redrive",
			func(w http.ResponseWriter, req *http.Request) {
				var in httpcore.RedriveInput
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					return
				}
				status, body, err := httpcore.RedriveDeadLetters(req.Context(), dl, in)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})
	}

	// --- Conditional: Policies ---

	if c.Policies != nil {
		pa := c.Policies // capture for closure

		handle(r, inst, cfg, http.MethodGet, "/admin/policies",
			func(w http.ResponseWriter, req *http.Request) {
				status, body, err := httpcore.ListPolicies(req.Context(), pa)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})

		handle(r, inst, cfg, http.MethodPost, "/admin/policies",
			func(w http.ResponseWriter, req *http.Request) {
				var in httpcore.PolicyRuleInput
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					return
				}
				status, body, err := httpcore.AddPolicy(req.Context(), pa, in)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})

		handle(r, inst, cfg, http.MethodDelete, "/admin/policies",
			func(w http.ResponseWriter, req *http.Request) {
				var in httpcore.PolicyRuleInput
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					return
				}
				status, body, err := httpcore.RemovePolicy(req.Context(), pa, in)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})

		handle(r, inst, cfg, http.MethodGet, "/admin/role-bindings",
			func(w http.ResponseWriter, req *http.Request) {
				status, body, err := httpcore.ListRoleBindings(req.Context(), pa)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})

		handle(r, inst, cfg, http.MethodPost, "/admin/role-bindings",
			func(w http.ResponseWriter, req *http.Request) {
				var in httpcore.RoleBindingInput
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					return
				}
				status, body, err := httpcore.AddRoleBinding(req.Context(), pa, in)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})

		handle(r, inst, cfg, http.MethodDelete, "/admin/role-bindings",
			func(w http.ResponseWriter, req *http.Request) {
				var in httpcore.RoleBindingInput
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
					return
				}
				status, body, err := httpcore.RemoveRoleBinding(req.Context(), pa, in)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})
	}

	// --- Conditional: RelayStats ---

	if c.RelayStats != nil {
		rs := c.RelayStats

		handle(r, inst, cfg, http.MethodGet, "/admin/relay-stats",
			func(w http.ResponseWriter, req *http.Request) {
				status, body, err := httpcore.AdminRelayStats(req.Context(), rs)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})
	}

	// --- Conditional: Timers ---

	if c.Timers != nil {
		ta := c.Timers

		handle(r, inst, cfg, http.MethodGet, "/admin/timers",
			func(w http.ResponseWriter, req *http.Request) {
				status, body, err := httpcore.AdminTimers(req.Context(), ta)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})
	}

	// --- Conditional: Lineage ---

	if c.Lineage != nil {
		la := c.Lineage

		handle(r, inst, cfg, http.MethodGet, "/admin/instances/{id}/lineage",
			func(w http.ResponseWriter, req *http.Request) {
				instanceID := req.PathValue("id")
				status, body, err := httpcore.AdminInstanceLineage(req.Context(), la, instanceID)
				if err != nil {
					writeErr(cfg, w, req, err)
					return
				}
				writeJSON(w, status, body)
			})
	}
}

// HealthRoutes mounts the liveness and readiness health-probe endpoints.
// It implements [httpcore.RouteCustomizer][*http.ServeMux].
type HealthRoutes struct {
	Checks []httpcore.HealthCheck
}

// Customize registers GET /healthz and GET /readyz.
func (c HealthRoutes) Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	r := cfg.Wrap(mux)

	checks := c.Checks

	handle(r, inst, cfg, http.MethodGet, "/healthz",
		func(w http.ResponseWriter, req *http.Request) {
			status, body := httpcore.EvaluateLive(req.Context())
			writeJSON(w, status, body)
		})

	handle(r, inst, cfg, http.MethodGet, "/readyz",
		func(w http.ResponseWriter, req *http.Request) {
			status, body := httpcore.EvaluateReady(req.Context(), checks)
			writeJSON(w, status, body)
		})
}
