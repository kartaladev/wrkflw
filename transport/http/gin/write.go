package gin

import (
	ginlib "github.com/gin-gonic/gin"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// writeErr classifies err using httpcore.ClassifyError, logs 5xx raw errors via
// cfg.Logger, and writes the error response JSON using gc.JSON.
func writeErr[R any](cfg httpcore.CustomizeConfig[R], gc *ginlib.Context, err error) {
	status, body := httpcore.ClassifyError(err)
	if status >= 500 && cfg.Logger != nil {
		cfg.Logger.Error("workflow gin handler internal error", "error", err)
	}
	gc.JSON(status, body)
}
