package rest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// fakeLineageAdmin is a configurable service.LineageAdmin test double.
type fakeLineageAdmin struct {
	lineage runtime.InstanceLineage
	err     error
}

func (f *fakeLineageAdmin) Lineage(_ context.Context, instanceID string) (runtime.InstanceLineage, error) {
	return f.lineage, f.err
}

func TestAdminInstanceLineage(t *testing.T) {
	t.Parallel()

	parentRef := &runtime.CallLinkRef{
		InstanceID: "parent-inst",
		DefID:      "parent-def",
		DefVersion: 1,
		Depth:      0,
	}
	childRef := runtime.CallLinkRef{
		InstanceID: "child-inst",
		DefID:      "",
		DefVersion: 0,
		Depth:      1,
	}

	cases := []struct {
		name     string
		withMW   bool
		wired    bool
		wantCode int
		check    func(t *testing.T, body map[string]any)
	}{
		{
			name:     "default-deny without admin middleware -> 403",
			withMW:   false,
			wired:    true,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "not wired -> 404",
			withMW:   true,
			wired:    false,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "wired + admin-allow -> 200 with lineage shape",
			withMW:   true,
			wired:    true,
			wantCode: http.StatusOK,
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				assert.Equal(t, "test-inst-1", body["InstanceID"])
				parent, ok := body["CallParent"].(map[string]any)
				require.True(t, ok, "CallParent must be an object, got %T", body["CallParent"])
				assert.Equal(t, "parent-inst", parent["InstanceID"])
				assert.Equal(t, "parent-def", parent["DefID"])

				children, ok := body["CallChildren"].([]any)
				require.True(t, ok, "CallChildren must be a list")
				require.Len(t, children, 1)
				child, ok := children[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "child-inst", child["InstanceID"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := []rest.Option{}
			if tc.withMW {
				opts = append(opts, rest.WithAdminMiddleware(allowAdmin))
			}
			if tc.wired {
				opts = append(opts, rest.WithLineageAdmin(&fakeLineageAdmin{
					lineage: runtime.InstanceLineage{
						InstanceID:       "test-inst-1",
						CallParent:       parentRef,
						CallChildren:     []runtime.CallLinkRef{childRef},
						ChainPredecessor: nil,
						ChainSuccessors:  []runtime.ChainLinkRef{},
					},
				}))
			}
			h := rest.NewHandler(&dlqStubService{}, opts...)
			rec := doReq(t, h, http.MethodGet, "/admin/instances/test-inst-1/lineage", "")
			assert.Equal(t, tc.wantCode, rec.Code)
			if tc.check != nil {
				tc.check(t, decodeRec(t, rec))
			}
		})
	}
}

func TestWithLineageAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { rest.WithLineageAdmin(nil) })
}
