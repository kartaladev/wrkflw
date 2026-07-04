package stdlib

import (
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// WithBasePath returns a [httpcore.CustomizeOption] that prefixes every route
// the group registers. It is an alias for [httpcore.WithBasePath][*http.ServeMux]
// so callers can use stdlib.WithBasePath without importing httpcore directly.
func WithBasePath(p string) httpcore.CustomizeOption[*http.ServeMux] {
	return httpcore.WithBasePath[*http.ServeMux](p)
}
