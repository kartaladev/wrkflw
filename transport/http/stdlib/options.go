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

// WithRequestActor returns a [httpcore.CustomizeOption] overriding how the
// AUTHENTICATED principal is resolved for the human-task routes (claim, complete,
// reassign). fn == nil restores the default.
//
// The default reads the actor an authentication middleware placed on the REQUEST
// CONTEXT with authz.ContextWithActor — under stdlib, by deriving the request
// with r.WithContext:
//
//	func authenticate(next http.Handler) http.Handler {
//		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			a, err := verifyCredential(r) // the consumer's own check
//			if err != nil {
//				http.Error(w, "unauthorized", http.StatusUnauthorized)
//				return
//			}
//			next.ServeHTTP(w, r.WithContext(authz.ContextWithActor(r.Context(), a)))
//		})
//	}
//
// Pass this option instead when the identity is not on the context — e.g. it must
// be derived from a header or a token store per request. fn must return
// [httpcore.ErrUnauthenticated] when the request carries no credential (⇒ 401) and
// any other error when the identity source itself failed (⇒ 503). It is never
// asked to authorize; that stays with the engine's authz.Authorizer.
//
// ⚠ The actor is NEVER read from the request body. A body still
// carrying an "actor" or "by" key is ignored.
//
// ⚠ This alias is REQUIRED, not cosmetic, for the same inference reason as
// [WithMaxBodyBytes]: on the generic [httpcore.WithRequestActor] the type
// parameter R appears only in the RESULT type, so httpcore.WithRequestActor(fn)
// does not compile ("cannot infer R").
func WithRequestActor(fn httpcore.RequestActorFunc) httpcore.CustomizeOption[*http.ServeMux] {
	return httpcore.WithRequestActor[*http.ServeMux](fn)
}

// WithRequestActorTimeout returns a [httpcore.CustomizeOption] bounding how long
// the configured [httpcore.RequestActorFunc] may take. d <= 0 disables the bound,
// matching [WithMaxBodyBytes] and [WithBodyReadTimeout]. The default is 10s.
//
// ⚠ It bounds only a resolver that HONOURS ctx cancellation — see
// [httpcore.CustomizeConfig.RequestActorTimeout] for the measurement.
//
// ⚠ This alias is REQUIRED for the same inference reason as [WithRequestActor]:
// R appears only in the result type of the generic
// [httpcore.WithRequestActorTimeout].
func WithRequestActorTimeout(d time.Duration) httpcore.CustomizeOption[*http.ServeMux] {
	return httpcore.WithRequestActorTimeout[*http.ServeMux](d)
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
