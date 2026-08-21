package stdlib

import (
	"net/http"
	"time"

	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// WithBasePath returns a [httpcore.CustomizeOption] that prefixes every route
// the group registers. It is an alias for [httpcore.WithBasePath][*http.ServeMux]
// so callers can use stdlib.WithBasePath without importing httpcore directly.
func WithBasePath(p string) httpcore.CustomizeOption[*http.ServeMux] {
	return httpcore.WithBasePath[*http.ServeMux](p)
}

// WithMaxBodyBytes returns a [httpcore.CustomizeOption] bounding the INBOUND
// request body to n bytes; a body exceeding n fails with
// [httpcore.ErrRequestBodyTooLarge], which classifies as 413. n <= 0 disables
// the cap. The default, applied when this option is not passed, is 1 MiB.
//
// ⚠ This alias is REQUIRED, not cosmetic. On the generic
// [httpcore.WithMaxBodyBytes] the type parameter R appears only in the RESULT
// type, so Go cannot infer it from the argument — httpcore.WithMaxBodyBytes(0)
// does not compile ("cannot infer R") and callers would have to spell out
// httpcore.WithMaxBodyBytes[*http.ServeMux](0). Same reason [WithBasePath]
// exists.
func WithMaxBodyBytes(n int64) httpcore.CustomizeOption[*http.ServeMux] {
	return httpcore.WithMaxBodyBytes[*http.ServeMux](n)
}

// WithBodyReadTimeout returns a [httpcore.CustomizeOption] bounding how long the
// CAPPED inbound body read may block before the handler proceeds with the bytes
// it already has. d <= 0 disables the deadline. The default is 30s — the same
// 30s action/httpcall's default client uses.
//
// ⚠ It is armed ONLY when the body cap is active. [WithMaxBodyBytes](0) installs
// no read wrapper, so there is nothing to bound and the pre-cap streaming
// behaviour is untouched.
//
// ⚠ This alias is REQUIRED for the same inference reason as [WithMaxBodyBytes]:
// R appears only in the result type of the generic
// [httpcore.WithBodyReadTimeout].
//
// See [httpcore.CustomizeConfig.BodyReadTimeout] for why the deadline exists and
// how it interacts with the consumer's own http.Server.ReadTimeout.
func WithBodyReadTimeout(d time.Duration) httpcore.CustomizeOption[*http.ServeMux] {
	return httpcore.WithBodyReadTimeout[*http.ServeMux](d)
}
