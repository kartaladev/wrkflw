package httpcore

// ContentTypeOptionsHeader and NoSniff name the response header that tells a
// browser to trust the declared Content-Type instead of inferring one from the
// bytes.
//
// Every endpoint here answers application/json, and MIME sniffing is what turns
// a JSON response into something else: a body whose leading bytes read as HTML
// can be rendered as HTML by a sniffing browser, and these responses embed
// caller-supplied values — process variables, task payloads, the `message` on a
// 4xx envelope — so an attacker has influence over exactly the bytes a sniffer
// inspects. Declaring the type and then letting it be second-guessed is the
// whole gap this closes.
//
// It is set in each adapter's per-route wrapper (stdlib and gin `observe`,
// fiber `observed`), which every route is registered through — all 27 stdlib
// routes, 26 gin, 26 fiber, health probes included — so a route added later
// cannot be registered without it.
//
// ⚠ SCOPE, stated precisely because it is narrower than it looks: this covers
// every response THIS PACKAGE'S HANDLERS WRITE, and nothing else. A consumer
// middleware registered through the adapters' WithMiddleware (or any
// [CustomizeConfig.Wrap] composition) runs OUTSIDE the route wrapper, so a
// middleware that short-circuits — auth returning 401, a rate limiter
// returning 429, panic recovery returning 500 — answers without ever reaching
// this code, and its response carries no nosniff. Measured on gin: a
// WithMiddleware calling AbortWithStatusJSON yields `X-Content-Type-Options: ""`.
//
// That boundary is SETTLED, not open (#71): a consumer's middleware writes a
// consumer's response, and this library does not insert its own handler into a
// router it was handed. [CustomizeConfig.Wrap] is the consumer's composition
// point; silently mutating it would be its own surprise. The remedy is opt-in
// — stdlib.NosniffMiddleware, gin.NosniffMiddleware, fiber.NosniffMiddleware,
// each of which a consumer places ahead of their own middleware.
//
// ⚠ The rejected alternative was setting it in Wrap as well. It would cover
// more, not less — the two are not alternatives, and Set is idempotent — but it
// could not be done cleanly on two of the three adapters. *http.ServeMux has no
// middleware seam at all, so a stdlib consumer wraps the mux beyond this
// package's reach; and on fiber, Group("", mw) is path-scoped to "/" rather
// than object-scoped, so MEASURED on fiber v3.4.0 it also runs for routes the
// CONSUMER registered on the same app. The opt-in middleware is the form that
// works identically on all three.
//
// Both halves are pinned by
// TestParity_NosniffMiddleware_ShortCircuitingConsumerMiddleware: the absence
// of the header without the middleware, and its presence with it.
const (
	ContentTypeOptionsHeader = "X-Content-Type-Options"
	NoSniff                  = "nosniff"
)
