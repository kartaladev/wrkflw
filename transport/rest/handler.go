package rest

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// NewHandler constructs a *http.ServeMux exposing the non-admin workflow routes.
// All patterns are root-relative so the mux can be mounted under any prefix via
// http.StripPrefix.
//
// Routes:
//
//	POST   /instances                  — start a new process instance
//	GET    /instances/{id}             — get an existing instance
//	POST   /instances/{id}/signals     — deliver a signal to an instance
//	POST   /messages                   — deliver a message
//	POST   /tasks/{token}/claim        — claim a human task
//	POST   /tasks/{token}/complete     — complete a human task
//	POST   /tasks/{token}/reassign     — reassign a human task
func NewHandler(svc service.Service, opts ...Option) http.Handler {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /instances", handleStartInstance(svc, cfg))
	mux.HandleFunc("GET /instances/{id}", handleGetInstance(svc, cfg))
	mux.HandleFunc("POST /instances/{id}/signals", handleDeliverSignal(svc))
	mux.HandleFunc("POST /messages", handleDeliverMessage(svc))
	mux.HandleFunc("POST /tasks/{token}/claim", handleClaimTask(svc))
	mux.HandleFunc("POST /tasks/{token}/complete", handleCompleteTask(svc))
	mux.HandleFunc("POST /tasks/{token}/reassign", handleReassignTask(svc))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteHTTPError(w, fmt.Errorf("%w: %v", ErrBadInput, err))
		return false
	}
	return true
}

func handleStartInstance(svc service.Service, cfg config) http.HandlerFunc {
	type reqBody struct {
		DefRef     string         `json:"def_ref"`
		InstanceID string         `json:"instance_id"`
		Vars       map[string]any `json:"vars"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req reqBody
		if !decodeBody(w, r, &req) {
			return
		}
		if req.DefRef == "" || req.InstanceID == "" {
			WriteHTTPError(w, fmt.Errorf("%w: def_ref and instance_id are required", ErrBadInput))
			return
		}
		st, err := svc.StartInstance(r.Context(), service.StartInstanceRequest{
			DefRef:     req.DefRef,
			InstanceID: req.InstanceID,
			Vars:       req.Vars,
		})
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, cfg.instanceMapper(st))
	}
}

func handleGetInstance(svc service.Service, cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		st, err := svc.GetInstance(r.Context(), id)
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg.instanceMapper(st))
	}
}

func handleDeliverSignal(svc service.Service) http.HandlerFunc {
	type reqBody struct {
		Signal  string         `json:"signal"`
		Payload map[string]any `json:"payload"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req reqBody
		if !decodeBody(w, r, &req) {
			return
		}
		if req.Signal == "" {
			WriteHTTPError(w, fmt.Errorf("%w: signal is required", ErrBadInput))
			return
		}
		st, err := svc.DeliverSignal(r.Context(), service.DeliverSignalRequest{
			InstanceID: id,
			Signal:     req.Signal,
			Payload:    req.Payload,
		})
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, NewInstanceView(st))
	}
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

func handleClaimTask(svc service.Service) http.HandlerFunc {
	type actorBody struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	type reqBody struct {
		Actor actorBody `json:"actor"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		var req reqBody
		if !decodeBody(w, r, &req) {
			return
		}
		st, err := svc.ClaimTask(r.Context(), service.ClaimTaskRequest{
			TaskToken: token,
			Actor:     authz.Actor{ID: req.Actor.ID, Roles: req.Actor.Roles},
		})
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, NewInstanceView(st))
	}
}

func handleCompleteTask(svc service.Service) http.HandlerFunc {
	type actorBody struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	type reqBody struct {
		Actor  actorBody      `json:"actor"`
		Output map[string]any `json:"output"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		var req reqBody
		if !decodeBody(w, r, &req) {
			return
		}
		st, err := svc.CompleteTask(r.Context(), service.CompleteTaskRequest{
			TaskToken: token,
			Actor:     authz.Actor{ID: req.Actor.ID, Roles: req.Actor.Roles},
			Output:    req.Output,
		})
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, NewInstanceView(st))
	}
}

func handleReassignTask(svc service.Service) http.HandlerFunc {
	type actorBody struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	type reqBody struct {
		From string    `json:"from"`
		To   string    `json:"to"`
		By   actorBody `json:"by"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		var req reqBody
		if !decodeBody(w, r, &req) {
			return
		}
		st, err := svc.ReassignTask(r.Context(), service.ReassignTaskRequest{
			TaskToken: token,
			From:      req.From,
			To:        req.To,
			By:        authz.Actor{ID: req.By.ID, Roles: req.By.Roles},
		})
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, NewInstanceView(st))
	}
}
