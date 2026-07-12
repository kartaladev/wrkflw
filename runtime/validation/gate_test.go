package validation_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model/validate"
	"github.com/kartaladev/wrkflw/runtime/validation"
)

// funcValidator adapts a func to the validate.Validator port for tests.
type funcValidator func(ctx context.Context, input map[string]any) error

func (f funcValidator) Validate(ctx context.Context, input map[string]any) error {
	return f(ctx, input)
}

// describableStrategy is a DESCRIBABLE strategy whose Descriptor is settable so
// tests can prove the Gate keys its compile-once cache by descriptor. It counts
// how many times NewValidator is invoked.
type describableStrategy struct {
	builds *int32
	desc   validate.ValidationDescriptor
	fail   bool
}

func (s describableStrategy) NewValidator() (validate.Validator, error) {
	atomic.AddInt32(s.builds, 1)
	return funcValidator(func(_ context.Context, _ map[string]any) error {
		if s.fail {
			return errors.New("bad input detail")
		}
		return nil
	}), nil
}

func (s describableStrategy) Descriptor() validate.ValidationDescriptor { return s.desc }

// plainStrategy is NOT describable (no Descriptor): the Gate has no stable key
// for it and must build a fresh validator each call.
type plainStrategy struct {
	builds *int32
}

func (s plainStrategy) NewValidator() (validate.Validator, error) {
	atomic.AddInt32(s.builds, 1)
	return funcValidator(func(_ context.Context, _ map[string]any) error { return nil }), nil
}

func TestGate_WrapsValidationFailure(t *testing.T) {
	t.Parallel()
	g := validation.NewGate()
	var builds int32
	s := describableStrategy{builds: &builds, desc: validate.ValidationDescriptor{Kind: "expr", Schema: "a"}, fail: true}

	err := g.Validate(t.Context(), s, map[string]any{})
	require.ErrorIs(t, err, validation.ErrInvalidInput)
}

func TestGate_CachesDescribableByDescriptor(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		run    func(t *testing.T, g *validation.Gate) int32 // returns total builds
		assert func(t *testing.T, builds int32)
	}

	cases := []testCase{
		{
			name: "same descriptor across two strategy instances shares one build",
			run: func(t *testing.T, g *validation.Gate) int32 {
				var builds int32
				d := validate.ValidationDescriptor{Kind: "expr", Schema: "a == 1"}
				s1 := describableStrategy{builds: &builds, desc: d}
				s2 := describableStrategy{builds: &builds, desc: d}
				require.NoError(t, g.Validate(t.Context(), s1, map[string]any{}))
				require.NoError(t, g.Validate(t.Context(), s2, map[string]any{}))
				return atomic.LoadInt32(&builds)
			},
			assert: func(t *testing.T, builds int32) {
				assert.Equal(t, int32(1), builds, "same descriptor must reuse the compiled validator")
			},
		},
		{
			name: "distinct descriptors do not collide (build one each)",
			run: func(t *testing.T, g *validation.Gate) int32 {
				var builds int32
				sA := describableStrategy{builds: &builds, desc: validate.ValidationDescriptor{Kind: "expr", Schema: "A"}}
				sB := describableStrategy{builds: &builds, desc: validate.ValidationDescriptor{Kind: "expr", Schema: "B"}}
				require.NoError(t, g.Validate(t.Context(), sA, map[string]any{}))
				require.NoError(t, g.Validate(t.Context(), sB, map[string]any{}))
				return atomic.LoadInt32(&builds)
			},
			assert: func(t *testing.T, builds int32) {
				assert.Equal(t, int32(2), builds, "different schemas must compile independently")
			},
		},
		{
			name: "non-describable strategy builds each call",
			run: func(t *testing.T, g *validation.Gate) int32 {
				var builds int32
				s := plainStrategy{builds: &builds}
				require.NoError(t, g.Validate(t.Context(), s, map[string]any{}))
				require.NoError(t, g.Validate(t.Context(), s, map[string]any{}))
				return atomic.LoadInt32(&builds)
			},
			assert: func(t *testing.T, builds int32) {
				assert.Equal(t, int32(2), builds, "no stable key: build fresh each call")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := validation.NewGate()
			tc.assert(t, tc.run(t, g))
		})
	}
}
