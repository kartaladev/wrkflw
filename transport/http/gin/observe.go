package gin

import (
	"context"
	"net/http"

	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// observe wraps handler with httpcore.Instrumentation.Observe using the given
// static route template. The status is read from gc.Writer.Status() after the
// handler returns (gin buffers the status until written).
//
// The returned gin.HandlerFunc is safe to register directly on a router.
func observe(inst *httpcore.Instrumentation, method, routeTemplate string, handler ginlib.HandlerFunc) ginlib.HandlerFunc {
	return func(gc *ginlib.Context) {
		inst.Observe(gc.Request.Context(), method, routeTemplate, gc.Request.Header, func(ctx context.Context) int {
			// Replace request context with the instrumented (span-enriched) one.
			gc.Request = gc.Request.WithContext(ctx)
			handler(gc)
			// gin sets the response status after gc.JSON; if nothing was written yet
			// Status() returns 200 (default). We read it after handler returns.
			s := gc.Writer.Status()
			if s == http.StatusOK && !gc.Writer.Written() {
				// Handler did not write anything — treat as 200.
				return http.StatusOK
			}
			return s
		})
	}
}
