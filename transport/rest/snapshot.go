package rest

import (
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

// handleGetInstanceSnapshot serves GET /instances/{id}/snapshot.
//
// It fetches the current instance state together with its process definition
// (via svc.GetInstanceWithDefinition) and encodes a runtime.InstanceSnapshot —
// a consumer-safe full projection of the instance that deliberately omits all
// engine-internal bookkeeping fields (timers, armed events, scopes, etc.).
func (h *handler) handleGetInstanceSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, def, err := h.svc.GetInstanceWithDefinition(r.Context(), id)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, view.NewInstanceSnapshot(st, def))
}

// handleGetActionableView serves GET /instances/{id}/actionable.
//
// It fetches the current instance state together with its process definition
// and encodes a runtime.ActionableView — a curated projection that lists only
// open human tasks and the allowed next-step actions derived from the
// definition's outgoing sequence flows.
func (h *handler) handleGetActionableView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, def, err := h.svc.GetInstanceWithDefinition(r.Context(), id)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, view.NewActionableView(st, def))
}
