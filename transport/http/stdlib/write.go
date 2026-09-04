// Package stdlib provides a stdlib net/http adapter for the workflow HTTP
// transport. Each route group implements [httpcore.RouteCustomizer][*http.ServeMux]
// and registers handlers onto a *http.ServeMux using native pattern syntax
// (method + space + path).
//
// ⚠ Unlike the gin and fiber adapters this one has no WithMiddleware option,
// because *http.ServeMux has no middleware seam: a consumer composes handlers
// around the mux themselves, entirely outside this package. One consequence is
// worth stating rather than discovering. This adapter sets
// X-Content-Type-Options: nosniff on every response IT writes, but a middleware
// you wrap the mux in that SHORT-CIRCUITS — auth answering 401, a rate limiter
// 429, panic recovery 500 — writes its response without reaching this package
// at all, so that response carries no nosniff. Wrap the chain in
// [NosniffMiddleware], outermost, when those responses can embed a
// caller-influenced value.
package stdlib

import (
	"encoding/json"
	"net/http"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// writeJSON serialises v as JSON and writes it to w with the given HTTP status
// code. It sets Content-Type to application/json. Errors during encoding are
// silently swallowed because headers and partial data may already be sent.
func writeJSON(w http.ResponseWriter, status int, v any) {
	if v != nil {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeErr classifies err via httpcore.ClassifyError, logs raw errors for 5xx
// via cfg.Logger, and writes a JSON error body. The raw error message is never
// included in the response for 5xx statuses.
func writeErr(cfg httpcore.CustomizeConfig[*http.ServeMux], w http.ResponseWriter, r *http.Request, err error) {
	status, body := httpcore.ClassifyError(err)
	if status >= 500 {
		cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err)
	}
	writeJSON(w, status, body)
}
