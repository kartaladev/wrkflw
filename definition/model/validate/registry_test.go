package validate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model/validate"
)

func TestRegistry_RegisterAndResolve(t *testing.T) {
	t.Parallel()
	r := validate.NewRegistry()
	r.Register("stub", func(schema string) (validate.ValidationStrategy, error) {
		return funcStrategy{v: funcValidator(func(_ context.Context, _ map[string]any) error { return nil })}, nil
	})

	tests := map[string]struct {
		desc   validate.ValidationDescriptor
		assert func(t *testing.T, s validate.ValidationStrategy, err error)
	}{
		"known kind resolves": {
			desc: validate.ValidationDescriptor{Kind: "stub", Schema: "x"},
			assert: func(t *testing.T, s validate.ValidationStrategy, err error) {
				if err != nil || s == nil {
					t.Fatalf("want strategy, got s=%v err=%v", s, err)
				}
			},
		},
		"unknown kind errors": {
			desc: validate.ValidationDescriptor{Kind: "nope"},
			assert: func(t *testing.T, s validate.ValidationStrategy, err error) {
				if !errors.Is(err, validate.ErrUnknownKind) {
					t.Fatalf("want ErrUnknownKind, got %v", err)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, err := r.Strategy(tc.desc)
			tc.assert(t, s, err)
		})
	}
}

// TestDefaultRegistry_RegisterAndResolve covers the process-global convenience
// entry points Register / DefaultRegistry directly (adapters self-register into
// them via init()). A unique test-only kind is used so this does not perturb any
// kind other tests rely on.
func TestDefaultRegistry_RegisterAndResolve(t *testing.T) {
	t.Parallel()

	const kind = "wrkflw-test-only-kind-fix5"
	validate.Register(kind, func(schema string) (validate.ValidationStrategy, error) {
		return funcStrategy{v: funcValidator(func(_ context.Context, _ map[string]any) error { return nil })}, nil
	})

	tests := map[string]struct {
		desc   validate.ValidationDescriptor
		assert func(t *testing.T, s validate.ValidationStrategy, err error)
	}{
		"registered kind resolves via DefaultRegistry": {
			desc: validate.ValidationDescriptor{Kind: kind, Schema: "x"},
			assert: func(t *testing.T, s validate.ValidationStrategy, err error) {
				require.NoError(t, err)
				require.NotNil(t, s)
			},
		},
		"unknown kind errors": {
			desc: validate.ValidationDescriptor{Kind: "wrkflw-unregistered-kind-fix5"},
			assert: func(t *testing.T, s validate.ValidationStrategy, err error) {
				require.ErrorIs(t, err, validate.ErrUnknownKind)
				require.Nil(t, s)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, err := validate.DefaultRegistry().Strategy(tc.desc)
			tc.assert(t, s, err)
		})
	}
}
