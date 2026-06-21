package rest

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
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
//	GET    /admin/instances            — keyset-paginated instance monitoring
//
// Default-deny: admin routes return 403 Forbidden when no WithAdminMiddleware option
// is supplied. Consumers must explicitly opt in by providing a middleware that
// enforces their authentication and authorisation requirements.
func NewHandler(svc service.Service, opts ...Option) http.Handler {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	h := &handler{cfg: cfg, svc: svc}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /instances", h.handleStartInstance)
	mux.HandleFunc("GET /instances/{id}", h.handleGetInstance)
	mux.HandleFunc("POST /instances/{id}/signals", h.handleDeliverSignal)
	mux.HandleFunc("POST /messages", handleDeliverMessage(svc))
	mux.HandleFunc("POST /tasks/{token}/claim", h.handleClaimTask)
	mux.HandleFunc("POST /tasks/{token}/complete", h.handleCompleteTask)
	mux.HandleFunc("POST /tasks/{token}/reassign", h.handleReassignTask)

	// Admin routes are mounted under the consumer-supplied admin middleware.
	// cfg.adminMiddleware defaults to denyAllMiddleware (set by defaultConfig) so
	// that admin endpoints are never openly accessible without an explicit opt-in.
	adminHandler := cfg.adminMiddleware(http.HandlerFunc(h.handleAdminListInstances))
	mux.Handle("GET /admin/instances", adminHandler)

	return mux
}

// handler holds shared state for the route handlers that need cfg.
type handler struct {
	cfg config
	svc service.Service
}

// renderInstance writes a process-instance response through cfg.instanceMapper so that
// every instance-returning endpoint honours the consumer's custom mapper consistently.
func (h *handler) renderInstance(w http.ResponseWriter, status int, st engine.InstanceState) {
	writeJSON(w, status, h.cfg.instanceMapper(st))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status header is already written, so the HTTP status cannot change.
		// Log the error so it is not silently swallowed.
		slog.Error("rest: encode response", "err", err)
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
	h.renderInstance(w, http.StatusCreated, st)
}

func (h *handler) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := h.svc.GetInstance(r.Context(), id)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, http.StatusOK, st)
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
	h.renderInstance(w, http.StatusOK, st)
}

func handleDeliverMessage(svc service.Service) http.HandlerFunc {
	type reqBody struct {
		DefRef         string         `json:"def_ref"`
		Name           string         `json:"name"`
		CorrelationKey string         `json:"correlation_key"`
		Payload        map[string]any `json:"payload"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req reqBody
		if !decodeBody(w, r, &req) {
			return
		}
		if req.DefRef == "" || req.Name == "" {
			WriteHTTPError(w, fmt.Errorf("%w: def_ref and name are required", ErrBadInput))
			return
		}
		if err := svc.DeliverMessage(r.Context(), service.DeliverMessageRequest{
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
	h.renderInstance(w, http.StatusOK, st)
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
	h.renderInstance(w, http.StatusOK, st)
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
	h.renderInstance(w, http.StatusOK, st)
}
