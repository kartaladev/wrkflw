package rest

import (
	"encoding/json"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// NewHandler constructs a *http.ServeMux exposing the workflow routes.
// All patterns are root-relative so the mux can be mounted under any prefix via
// http.StripPrefix.
//
// Non-admin routes:
//
//	POST   /instances                  — start a new process instance
//	GET    /instances/{id}             — get an existing instance
//	POST   /instances/{id}/signals     — deliver a signal to an instance
//	POST   /messages                   — deliver a message
//	POST   /tasks/{token}/claim        — claim a human task
//	POST   /tasks/{token}/complete     — complete a human task
//	POST   /tasks/{token}/reassign     — reassign a human task
//
// Admin routes (wrapped by the configured admin middleware):
//
//	GET    /admin/instances                                    — keyset-paginated instance monitoring
//	POST   /admin/instances/{id}/incidents/{incidentID}/resolve — resolve an open incident and resume execution
//	POST   /admin/instances/{id}/cancel                        — cancel a running instance (runs cancel actions)
//
// DLQ admin routes (registered only when WithDeadLetterAdmin is supplied):
//
//	GET    /admin/dead-letters                                 — list dead-lettered outbox rows
//	POST   /admin/dead-letters/redrive                         — re-queue dead rows by id
//
// Policy-admin routes (registered only when WithPolicyAdmin is supplied):
//
//	GET    /admin/policies                                     — list casbin policy rules
//	POST   /admin/policies                                     — add a casbin policy rule
//	DELETE /admin/policies                                     — remove a casbin policy rule
//	GET    /admin/role-bindings                                — list casbin role-binding rules
//	POST   /admin/role-bindings                                — add a casbin role binding
//	DELETE /admin/role-bindings                                — remove a casbin role binding
//
// Default-deny: admin routes return 403 Forbidden when no WithAdminMiddleware option
// is supplied. Consumers must explicitly opt in by providing a middleware that
// enforces their authentication and authorisation requirements.
func NewHandler(svc service.Service, opts ...Option) http.Handler {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	// Build telemetry, filtering out any nil observability options.
	cfg.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/transport/rest",
		nonNilOpts(cfg.logOpt, cfg.tpOpt, cfg.mpOpt)...,
	)

	h := &handler{cfg: cfg, svc: svc}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /instances", h.handleStartInstance)
	mux.HandleFunc("GET /instances/{id}", h.handleGetInstance)
	mux.HandleFunc("POST /instances/{id}/signals", h.handleDeliverSignal)
	mux.HandleFunc("POST /messages", h.handleDeliverMessage)
	mux.HandleFunc("POST /tasks/{token}/claim", h.handleClaimTask)
	mux.HandleFunc("POST /tasks/{token}/complete", h.handleCompleteTask)
	mux.HandleFunc("POST /tasks/{token}/reassign", h.handleReassignTask)

	// Admin routes are mounted under the consumer-supplied admin middleware.
	// cfg.adminMiddleware defaults to denyAllMiddleware (set by defaultConfig) so
	// that admin endpoints are never openly accessible without an explicit opt-in.
	mux.Handle("GET /admin/instances", cfg.adminMiddleware(http.HandlerFunc(h.handleAdminListInstances)))
	mux.Handle("POST /admin/instances/{id}/incidents/{incidentID}/resolve",
		cfg.adminMiddleware(http.HandlerFunc(h.handleResolveIncident)))
	mux.Handle("POST /admin/instances/{id}/cancel",
		cfg.adminMiddleware(http.HandlerFunc(h.handleCancelInstance)))

	// DLQ admin routes are registered only when a DeadLetterAdmin is wired via
	// WithDeadLetterAdmin. Absent it (e.g. MemStore-only consumers), the routes do
	// not exist (404) rather than returning a misleading error. Like the other
	// admin routes they sit behind cfg.adminMiddleware (default-deny).
	if cfg.deadLetters != nil {
		mux.Handle("GET /admin/dead-letters",
			cfg.adminMiddleware(http.HandlerFunc(h.handleListDeadLetters)))
		mux.Handle("POST /admin/dead-letters/redrive",
			cfg.adminMiddleware(http.HandlerFunc(h.handleRedriveDeadLetters)))
	}

	// Policy-admin routes are registered only when a PolicyAdmin is wired via
	// WithPolicyAdmin. Absent it, the routes do not exist (404). Like the other
	// admin routes they sit behind cfg.adminMiddleware (default-deny).
	if cfg.policyAdmin != nil {
		mux.Handle("GET /admin/policies",
			cfg.adminMiddleware(http.HandlerFunc(h.handleListPolicies)))
		mux.Handle("POST /admin/policies",
			cfg.adminMiddleware(http.HandlerFunc(h.handleAddPolicy)))
		mux.Handle("DELETE /admin/policies",
			cfg.adminMiddleware(http.HandlerFunc(h.handleRemovePolicy)))
		mux.Handle("GET /admin/role-bindings",
			cfg.adminMiddleware(http.HandlerFunc(h.handleListRoleBindings)))
		mux.Handle("POST /admin/role-bindings",
			cfg.adminMiddleware(http.HandlerFunc(h.handleAddRoleBinding)))
		mux.Handle("DELETE /admin/role-bindings",
			cfg.adminMiddleware(http.HandlerFunc(h.handleRemoveRoleBinding)))
	}

	return h.traceMiddleware(mux)
}

// nonNilOpts returns only the non-nil observability.Option values from opts.
func nonNilOpts(opts ...observability.Option) []observability.Option {
	out := make([]observability.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// handler holds shared state for the route handlers that need cfg.
type handler struct {
	cfg config
	svc service.Service
}

// traceMiddleware wraps the given handler with a per-request OTel span.
// It extracts W3C trace context from the incoming request headers so that
// distributed traces propagate correctly across service boundaries.
func (h *handler) traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := h.cfg.tel.Tracer.Start(ctx, "wrkflw.rest "+r.Method, trace.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.target", r.URL.Path),
		))
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// renderInstance writes a process-instance response through cfg.instanceMapper so that
// every instance-returning endpoint honours the consumer's custom mapper consistently.
func (h *handler) renderInstance(w http.ResponseWriter, r *http.Request, status int, st engine.InstanceState) {
	h.writeJSON(w, r, status, h.cfg.instanceMapper(st))
}

// writeJSON serialises v as JSON with the given HTTP status. If encoding fails after
// the header is flushed, the error is logged through the injected telemetry logger
// so that no package-global slog call is made.
func (h *handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status header is already written, so the HTTP status cannot change.
		// Log the error so it is not silently swallowed.
		h.cfg.tel.Logger.ErrorContext(r.Context(), "rest: encode response", "err", err)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteHTTPError(w, fmt.Errorf("%w: %v", ErrBadInput, err))
		return false
	}
	return true
}

func (h *handler) handleStartInstance(w http.ResponseWriter, r *http.Request) {
	type reqBody struct {
		DefRef     string         `json:"def_ref"`
		InstanceID string         `json:"instance_id"`
		Vars       map[string]any `json:"vars"`
	}
	var req reqBody
	if !decodeBody(w, r, &req) {
		return
	}
	if req.DefRef == "" || req.InstanceID == "" {
		WriteHTTPError(w, fmt.Errorf("%w: def_ref and instance_id are required", ErrBadInput))
		return
	}
	st, err := h.svc.StartInstance(r.Context(), service.StartInstanceRequest{
		DefRef:     req.DefRef,
		InstanceID: req.InstanceID,
		Vars:       req.Vars,
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusCreated, st)
}

func (h *handler) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := h.svc.GetInstance(r.Context(), id)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}

func (h *handler) handleDeliverSignal(w http.ResponseWriter, r *http.Request) {
	type reqBody struct {
		Signal  string         `json:"signal"`
		Payload map[string]any `json:"payload"`
	}
	id := r.PathValue("id")
	var req reqBody
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Signal == "" {
		WriteHTTPError(w, fmt.Errorf("%w: signal is required", ErrBadInput))
		return
	}
	st, err := h.svc.DeliverSignal(r.Context(), service.DeliverSignalRequest{
		InstanceID: id,
		Signal:     req.Signal,
		Payload:    req.Payload,
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}

// handleDeliverMessage handles POST /messages.
//
// Delivery is best-effort fire-and-forget: a 202 Accepted does NOT guarantee
// that an instance was waiting for the message. If no instance matches the given
// name and correlationKey, the message is silently dropped and 202 is still
// returned.
func (h *handler) handleDeliverMessage(w http.ResponseWriter, r *http.Request) {
	type reqBody struct {
		DefRef         string         `json:"def_ref"`
		Name           string         `json:"name"`
		CorrelationKey string         `json:"correlation_key"`
		Payload        map[string]any `json:"payload"`
	}
	var req reqBody
	if !decodeBody(w, r, &req) {
		return
	}
	if req.DefRef == "" || req.Name == "" {
		WriteHTTPError(w, fmt.Errorf("%w: def_ref and name are required", ErrBadInput))
		return
	}
	if err := h.svc.DeliverMessage(r.Context(), service.DeliverMessageRequest{
		DefRef:         req.DefRef,
		Name:           req.Name,
		CorrelationKey: req.CorrelationKey,
		Payload:        req.Payload,
	}); err != nil {
		WriteHTTPError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *handler) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	type actorBody struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	type reqBody struct {
		Actor actorBody `json:"actor"`
	}
	token := r.PathValue("token")
	var req reqBody
	if !decodeBody(w, r, &req) {
		return
	}
	st, err := h.svc.ClaimTask(r.Context(), service.ClaimTaskRequest{
		TaskToken: token,
		Actor:     authz.Actor{ID: req.Actor.ID, Roles: req.Actor.Roles},
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}

func (h *handler) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	type actorBody struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	type reqBody struct {
		Actor  actorBody      `json:"actor"`
		Output map[string]any `json:"output"`
	}
	token := r.PathValue("token")
	var req reqBody
	if !decodeBody(w, r, &req) {
		return
	}
	st, err := h.svc.CompleteTask(r.Context(), service.CompleteTaskRequest{
		TaskToken: token,
		Actor:     authz.Actor{ID: req.Actor.ID, Roles: req.Actor.Roles},
		Output:    req.Output,
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}

func (h *handler) handleReassignTask(w http.ResponseWriter, r *http.Request) {
	type actorBody struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	type reqBody struct {
		From string    `json:"from"`
		To   string    `json:"to"`
		By   actorBody `json:"by"`
	}
	token := r.PathValue("token")
	var req reqBody
	if !decodeBody(w, r, &req) {
		return
	}
	st, err := h.svc.ReassignTask(r.Context(), service.ReassignTaskRequest{
		TaskToken: token,
		From:      req.From,
		To:        req.To,
		By:        authz.Actor{ID: req.By.ID, Roles: req.By.Roles},
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}
