package fiber

import (
	"context"
	"net/http"

	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// fiberHeaders builds a net/http.Header map from fiber's c.GetReqHeaders(),
// which returns map[string][]string — the same shape as http.Header — so we
// can pass it directly to Instrumentation.Observe for trace-context extraction.
func fiberHeaders(c fiberlib.Ctx) http.Header {
	raw := c.GetReqHeaders()
	h := make(http.Header, len(raw))
	for k, vs := range raw {
		h[k] = vs
	}
	return h
}

// observed wraps a fiber handler with OTel span + metric recording via inst.
// method is the HTTP verb (e.g. "POST"); routeTemplate is the static pattern
// (e.g. cfg.BasePath+"/instances/:id") — never read from the request to avoid
// cardinality explosion. The status is read from c.Response().StatusCode()
// after the inner handler returns.
func observed(
	inst *httpcore.Instrumentation,
	method, routeTemplate string,
	inner fiberlib.Handler,
) fiberlib.Handler {
	return func(c fiberlib.Ctx) error {
		hdr := fiberHeaders(c)
		ctx := c.Context()

		var innerErr error
		inst.Observe(ctx, method, routeTemplate, hdr, func(enrichedCtx context.Context) int {
			// Propagate the OTel-enriched context into the fiber Ctx so
			// downstream handlers inherit the span.
			c.SetContext(enrichedCtx)
			innerErr = inner(c)
			return c.Response().StatusCode()
		})
		return innerErr
	}
}
