package validate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
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
