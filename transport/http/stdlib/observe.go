package stdlib

import (
	"context"
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// observe wraps h with OTel instrumentation via inst.Observe. routeTemplate is
// the STATIC route template (e.g. "GET /instances/{id}") — it is never read
// from r.Pattern so it remains stable across Go versions.
func observe(
	inst *httpcore.Instrumentation,
	method, routeTemplate string,
	h http.HandlerFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		inst.Observe(r.Context(), method, routeTemplate, r.Header, func(ctx context.Context) int {
			h(rw, r.WithContext(ctx))
			return rw.code
		})
	}
}

// statusRecorder captures the first status code written by a handler so that
// observe can report it to Instrumentation.Observe.
type statusRecorder struct {
	http.ResponseWriter
	code    int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.code = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped ResponseWriter so http.NewResponseController (and
// middleware that type-asserts for http.Flusher / http.Hijacker) can reach the
// underlying writer's optional interfaces through the recorder.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
