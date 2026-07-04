package fiber

import (
	fiberlib "github.com/gofiber/fiber/v3"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// writeErr classifies err, logs 5xx details via cfg.Logger (never sends raw
// detail to the client), and writes the appropriate HTTP status + JSON body.
func writeErr(cfg httpcore.CustomizeConfig[fiberlib.Router], c fiberlib.Ctx, err error) error {
	status, body := httpcore.ClassifyError(err)
	if status >= 500 {
		cfg.Logger.ErrorContext(c.Context(), "fiber: internal error", "err", err)
	}
	return c.Status(status).JSON(body)
}
