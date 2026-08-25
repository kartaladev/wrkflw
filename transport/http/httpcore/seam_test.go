package httpcore_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

func TestResolveConfigDefaults(t *testing.T) {
	cfg := httpcore.ResolveConfig[int]() // R=int is a stand-in router type for the seam test
	if cfg.Wrap == nil {
		t.Fatal("Wrap must default to a non-nil identity")
	}
	if got := cfg.Wrap(7); got != 7 {
		t.Fatalf("default Wrap must be identity, got %d", got)
	}
	if cfg.InstanceMapper == nil {
		t.Fatal("InstanceMapper must default to non-nil")
	}
	if cfg.Logger == nil {
		t.Fatal("Logger must default to slog.Default()")
	}
}

func TestOptionsCompose(t *testing.T) {
	inner := func(x int) int { return x + 1 }
	outer := func(x int) int { return x * 2 }
	cfg := httpcore.ResolveConfig(
		httpcore.WithBasePath[int]("/api"),
		httpcore.WithRouterFunc(inner),
		httpcore.WithRouterFunc(outer), // composes: later wraps earlier
	)
	if cfg.BasePath != "/api" {
		t.Fatalf("BasePath=%q", cfg.BasePath)
	}
	// outer(inner(3)) or inner(outer(3)); assert deterministic composition order.
	if got := cfg.Wrap(3); got != outer(inner(3)) {
		t.Fatalf("Wrap composition = %d, want %d", got, outer(inner(3)))
	}
}

type recordCustomizer struct{ hits *int }

func (c recordCustomizer) Customize(r int, _ ...httpcore.CustomizeOption[int]) { *c.hits++ }

func TestMountGroupsInvokesEach(t *testing.T) {
	hits := 0
	httpcore.MountGroups(0, recordCustomizer{&hits}, recordCustomizer{&hits})
	if hits != 2 {
		t.Fatalf("MountGroups invoked %d customizers, want 2", hits)
	}
}

func TestWithInstanceMapperOverrides(t *testing.T) {
	cfg := httpcore.ResolveConfig(httpcore.WithInstanceMapper[int](func(engine.InstanceState) any { return "x" }))
	if cfg.InstanceMapper(engine.InstanceState{}) != "x" {
		t.Fatal("WithInstanceMapper not applied")
	}
}

// TestMaxBodyBytesDefaultAndDisable pins ADR-0186's inbound body cap, and
// specifically WHERE the default is applied. MaxBodyBytes is a plain int64, not
// a pointer, which works only because ResolveConfig seeds 1 MiB in its STRUCT
// LITERAL — before the option loop runs. An int64 has no nil, so a post-loop
// nil-guard block (where Wrap/InstanceMapper/Logger are repaired) cannot tell
// "consumer never set it" from "consumer set it to 0", and would silently
// re-impose 1 MiB on a consumer who explicitly disabled the cap.
//
// Falsifier: move the 1 MiB default from the literal into the post-loop guard
// block as `if cfg.MaxBodyBytes == 0 { cfg.MaxBodyBytes = 1 << 20 }`. The
// default row stays GREEN; the explicit-zero row turns RED. That asymmetry is
// why both rows exist — the default row alone cannot detect the mistake.
func TestMaxBodyBytesDefaultAndDisable(t *testing.T) {
	t.Parallel()

	const defaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

	type testCase struct {
		name   string
		opts   []httpcore.CustomizeOption[int] // R=int stands in for a router type
		assert func(t *testing.T, cfg httpcore.CustomizeConfig[int])
	}

	cases := []testCase{
		{
			name: "no option yields the 1 MiB default",
			opts: nil,
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Equal(t, defaultMaxBodyBytes, cfg.MaxBodyBytes)
				assert.Equal(t, int64(1048576), cfg.MaxBodyBytes, "1 MiB, spelled out")
			},
		},
		{
			// ⚠ Load-bearing, and NOT a duplicate of the row above. An explicit
			// zero is the documented way to DISABLE the cap; it must survive
			// ResolveConfig untouched. This is the row that fails if the default
			// is applied after the option loop instead of in the literal.
			name: "an explicit zero disables the cap and is not overwritten by the default",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithMaxBodyBytes[int](0)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Zero(t, cfg.MaxBodyBytes,
					"an explicit 0 must disable the cap, not fall back to the default")
			},
		},
		{
			// n <= 0 disables, matching action/httpcall.WithMaxResponseSize's
			// existing convention — one convention across the library for
			// bounding a body. A negative value is passed through unchanged
			// rather than normalised, so the read sites see one predicate.
			name: "a negative value is passed through and also disables the cap",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithMaxBodyBytes[int](-1)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Equal(t, int64(-1), cfg.MaxBodyBytes)
				assert.LessOrEqual(t, cfg.MaxBodyBytes, int64(0), "n <= 0 disables")
			},
		},
		{
			name: "an explicit positive value overrides the default",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithMaxBodyBytes[int](4096)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Equal(t, int64(4096), cfg.MaxBodyBytes)
			},
		},
		{
			// Last option wins, and a later explicit disable must beat an
			// earlier positive cap — the shape a consumer hits when a shared
			// default option list is overridden per-mount.
			name: "the last option wins, including a disable after a cap",
			opts: []httpcore.CustomizeOption[int]{
				httpcore.WithMaxBodyBytes[int](4096),
				httpcore.WithMaxBodyBytes[int](0),
			},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Zero(t, cfg.MaxBodyBytes)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := httpcore.ResolveConfig(tc.opts...)
			tc.assert(t, cfg)
		})
	}
}

// TestBodyReadTimeoutDefaultAndDisable mirrors TestMaxBodyBytesDefaultAndDisable
// for the body-read deadline that bounds how long the capped read may block.
//
// WHY IT EXISTS: the cap reads the body to completion before parsing, which
// converts a fast-returning request into an indefinite handler hold. MEASURED
// 2026-08-21 against a real http.Server on POST /instances, Content-Length
// 400000 with a complete 41-byte value then a stall: cap disabled answered
// 201 Created in 0s; cap enabled (1 MiB default) produced NO RESPONSE after 3s.
//
// Falsifier, exactly as above: move the 30s default from ResolveConfig's STRUCT
// LITERAL into the post-loop guard block as
// `if cfg.BodyReadTimeout == 0 { cfg.BodyReadTimeout = 30 * time.Second }`.
// A time.Duration is an int64 with no nil, so the default row stays GREEN while
// the explicit-zero row turns RED — which is why both rows are here.
func TestBodyReadTimeoutDefaultAndDisable(t *testing.T) {
	t.Parallel()

	// The default is 30s because action/httpcall's default client already uses
	// &http.Client{Timeout: 30 * time.Second}. One number for "how long an HTTP
	// body may take" across the library, not a second invented constant.
	const defaultBodyReadTimeout = 30 * time.Second

	type testCase struct {
		name   string
		opts   []httpcore.CustomizeOption[int] // R=int stands in for a router type
		assert func(t *testing.T, cfg httpcore.CustomizeConfig[int])
	}

	cases := []testCase{
		{
			name: "no option yields the 30s default",
			opts: nil,
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Equal(t, defaultBodyReadTimeout, cfg.BodyReadTimeout)
				assert.Equal(t, 30*time.Second, cfg.BodyReadTimeout,
					"the same 30s action/httpcall's default client uses")
			},
		},
		{
			// ⚠ Load-bearing, and NOT a duplicate of the row above — see the
			// falsifier in the doc comment. An explicit zero is the documented way
			// to opt OUT of the deadline and must survive ResolveConfig untouched.
			name: "an explicit zero disables the deadline and is not overwritten by the default",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithBodyReadTimeout[int](0)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Zero(t, cfg.BodyReadTimeout,
					"an explicit 0 must disable the deadline, not fall back to the default")
			},
		},
		{
			// d <= 0 disables, the SAME convention MaxBodyBytes and
			// action/httpcall.WithMaxResponseSize already use. A negative value is
			// passed through unchanged so the read sites need only one predicate.
			name: "a negative value is passed through and also disables the deadline",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithBodyReadTimeout[int](-time.Second)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Equal(t, -time.Second, cfg.BodyReadTimeout)
				assert.LessOrEqual(t, cfg.BodyReadTimeout, time.Duration(0), "d <= 0 disables")
			},
		},
		{
			name: "an explicit positive value overrides the default",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithBodyReadTimeout[int](2 * time.Second)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Equal(t, 2*time.Second, cfg.BodyReadTimeout)
			},
		},
		{
			name: "the last option wins, including a disable after a deadline",
			opts: []httpcore.CustomizeOption[int]{
				httpcore.WithBodyReadTimeout[int](2 * time.Second),
				httpcore.WithBodyReadTimeout[int](0),
			},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Zero(t, cfg.BodyReadTimeout)
			},
		},
		{
			// The two bounds are INDEPENDENT: disabling the cap must not disturb
			// the deadline field, and vice versa. (The deadline is only ARMED when
			// the cap is active — that is a read-site rule, pinned in the adapter
			// tests, not a ResolveConfig rule.)
			name: "disabling the cap leaves the deadline at its default",
			opts: []httpcore.CustomizeOption[int]{httpcore.WithMaxBodyBytes[int](0)},
			assert: func(t *testing.T, cfg httpcore.CustomizeConfig[int]) {
				assert.Zero(t, cfg.MaxBodyBytes)
				assert.Equal(t, defaultBodyReadTimeout, cfg.BodyReadTimeout)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := httpcore.ResolveConfig(tc.opts...)
			tc.assert(t, cfg)
		})
	}
}

func TestResolveConfig_RequestActor(t *testing.T) {
	t.Parallel()

	custom := httpcore.RequestActorFunc(func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "from-option"}, nil
	})

	tests := map[string]struct {
		opts   []httpcore.CustomizeOption[*http.ServeMux]
		ctx    func(t *testing.T) context.Context
		assert func(t *testing.T, got authz.Actor, err error)
	}{
		"default reads the context seam": {
			ctx: func(t *testing.T) context.Context {
				return authz.ContextWithActor(t.Context(), authz.Actor{ID: "alice"})
			},
			assert: func(t *testing.T, got authz.Actor, err error) {
				require.NoError(t, err)
				assert.Equal(t, "alice", got.ID)
			},
		},
		"default with nothing on the context reports ErrUnauthenticated": {
			ctx: func(t *testing.T) context.Context { return t.Context() },
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, httpcore.ErrUnauthenticated)
			},
		},
		"WithRequestActor overrides the default": {
			opts: []httpcore.CustomizeOption[*http.ServeMux]{
				httpcore.WithRequestActor[*http.ServeMux](custom),
			},
			ctx: func(t *testing.T) context.Context {
				return authz.ContextWithActor(t.Context(), authz.Actor{ID: "ignored"})
			},
			assert: func(t *testing.T, got authz.Actor, err error) {
				require.NoError(t, err)
				assert.Equal(t, "from-option", got.ID)
			},
		},
		// ⚠ nil must RESTORE the fail-closed default, not disable resolution. A func
		// HAS a nil, unlike MaxBodyBytes' int64, so the post-loop guard is safe here.
		"WithRequestActor(nil) restores the fail-closed default": {
			opts: []httpcore.CustomizeOption[*http.ServeMux]{
				httpcore.WithRequestActor[*http.ServeMux](nil),
			},
			ctx: func(t *testing.T) context.Context { return t.Context() },
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, httpcore.ErrUnauthenticated)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := httpcore.ResolveConfig(tc.opts...)
			require.NotNil(t, cfg.RequestActor, "ResolveConfig must always leave a resolver")
			got, err := cfg.RequestActor(tc.ctx(t))
			tc.assert(t, got, err)
		})
	}
}

// TestResolveConfig_RequestActorTimeout pins the default and that the option is
// seeded in the STRUCT LITERAL, so an explicit non-positive value survives as
// "disabled" — the same rule BodyReadTimeout follows.
func TestResolveConfig_RequestActorTimeout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 10*time.Second, httpcore.ResolveConfig[*http.ServeMux]().RequestActorTimeout)
	assert.Equal(t, time.Duration(0),
		httpcore.ResolveConfig(httpcore.WithRequestActorTimeout[*http.ServeMux](0)).RequestActorTimeout,
		"an explicit 0 must survive as disabled, not be re-defaulted")
	assert.Equal(t, 2*time.Second,
		httpcore.ResolveConfig(httpcore.WithRequestActorTimeout[*http.ServeMux](2*time.Second)).RequestActorTimeout)
}
