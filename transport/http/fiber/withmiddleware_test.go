// Package fiber_test — WithMiddleware's memo and the routers it has to survive.
package fiber_test

import (
	"testing"

	fiberlib "github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fiberadapter "github.com/kartaladev/wrkflw/transport/http/fiber"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// noncomparableRouter is a consumer-authored fiber.Router whose dynamic type is
// a STRUCT VALUE containing a slice, and therefore not comparable. Embedding the
// interface satisfies every method without implementing any of them, which is
// exactly how a real test double or wrapper would be written.
//
// ⚠ This is reachable from outside the module: Mount, MountHealth and every
// Customize take fiber.Router, an exported interface, so any consumer may hand
// the adapter their own implementation. Nothing constrains it to a pointer.
type noncomparableRouter struct {
	fiberlib.Router
	tag []string
}

// comparableRouter is the same wrapper as a POINTER, which is comparable — the
// shape fiber's own *App and *Group have.
type comparableRouter struct {
	fiberlib.Router
	tag []string
}

// TestWithMiddleware_MemoAcrossRouterTypes covers the memo's one sharp edge.
//
// WithMiddleware memoises the group it creates, keyed by the router it was
// given, so that the registration side effect happens once however many times
// Customize calls Wrap. The key is fiber.Router — an INTERFACE — and Go panics
// at runtime ("hash of unhashable type") when the dynamic type behind an
// interface map key is not comparable.
//
// MEASURED before the guard: passing a non-comparable fiber.Router panicked on
// the FIRST Wrap call — the map write, not merely a later lookup — so a
// consumer with a struct-valued router double crashed at mount time with no
// compile-time warning. That is a library panicking because someone implemented
// its own exported interface, which is not an acceptable failure mode.
//
// ⚠ The non-comparable row asserts the fallback HONESTLY: it returns a fresh
// group each call, which is the un-memoised behaviour this PR otherwise fixes.
// That is a deliberate trade — degrade to the older, noisier registration for an
// exotic router rather than crash — and asserting it keeps the trade visible
// instead of letting a reader assume the memo covers every router.
//
// No ctx modifier: WithMiddleware builds a router option, and nothing on this
// path observes a context.
func TestWithMiddleware_MemoAcrossRouterTypes(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// mkRouter wraps app in the router shape under test.
		mkRouter func(app *fiberlib.App) fiberlib.Router
		assert   func(t *testing.T, first, second fiberlib.Router, recovered any)
	}

	cases := []testCase{
		{
			name:     "comparable router is memoised to one group",
			mkRouter: func(app *fiberlib.App) fiberlib.Router { return app },
			assert: func(t *testing.T, first, second fiberlib.Router, recovered any) {
				require.Nil(t, recovered, "must not panic on a plain *fiber.App")
				assert.Same(t, first, second,
					"two Wrap calls on one router must yield the SAME group — "+
						"a second group means a second registration at \"/\"")
			},
		},
		{
			name: "comparable pointer wrapper is memoised too",
			mkRouter: func(app *fiberlib.App) fiberlib.Router {
				return &comparableRouter{Router: app, tag: []string{"x"}}
			},
			assert: func(t *testing.T, first, second fiberlib.Router, recovered any) {
				require.Nil(t, recovered, "must not panic on a pointer-typed consumer router")
				assert.Same(t, first, second,
					"a consumer's pointer router is comparable and must be memoised")
			},
		},
		{
			name: "non-comparable router falls back instead of panicking",
			mkRouter: func(app *fiberlib.App) fiberlib.Router {
				return noncomparableRouter{Router: app, tag: []string{"x"}}
			},
			assert: func(t *testing.T, first, second fiberlib.Router, recovered any) {
				require.Nil(t, recovered,
					"a struct-valued consumer router must not panic the memo")
				require.NotNil(t, first)
				assert.NotSame(t, first, second,
					"the documented fallback: un-memoised, a fresh group per call")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := fiberlib.New()
			r := tc.mkRouter(app)
			opt := fiberadapter.WithMiddleware(func(c fiberlib.Ctx) error { return c.Next() })
			cfg := httpcore.ResolveConfig(opt)

			var first, second fiberlib.Router
			recovered := func() (rec any) {
				defer func() { rec = recover() }()
				first = cfg.Wrap(r)
				second = cfg.Wrap(r)
				return nil
			}()

			tc.assert(t, first, second, recovered)
		})
	}
}
