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
// ⚠ It is set in each adapter's per-route wrapper (stdlib and gin `observe`,
// fiber `observed`), NOT via [CustomizeConfig.Wrap]. Wrap is the consumer's
// composition point — WithRouterFunc and the adapters' WithMiddleware build on
// it — so a header set there could be reordered or displaced by consumer
// options. A security header must not be defeatable by option ordering, and the
// route wrapper is unconditional: every route in all three adapters is
// registered through it, health probes included.
const (
	ContentTypeOptionsHeader = "X-Content-Type-Options"
	NoSniff                  = "nosniff"
)
