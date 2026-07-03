package rest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// fakeLineageAdmin is a configurable service.LineageAdmin test double.
type fakeLineageAdmin struct {
	lineage kernel.InstanceLineage
	err     error
}

func (f *fakeLineageAdmin) Lineage(_ context.Context, instanceID string) (kernel.InstanceLineage, error) {
	return f.lineage, f.err
}

func TestAdminInstanceLineage(t *testing.T) {
	t.Parallel()

	parentRef := &kernel.CallLinkRef{
		InstanceID: "parent-inst",
		DefID:      "parent-def",
		DefVersion: 1,
		Depth:      0,
	}
	childRef := kernel.CallLinkRef{
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
				// Top-level instance_id (snake_case).
				assert.Equal(t, "test-inst-1", body["instance_id"])

				// call_parent must be an object with snake_case keys.
				parent, ok := body["call_parent"].(map[string]any)
				require.True(t, ok, "call_parent must be an object, got %T", body["call_parent"])
				assert.Equal(t, "parent-inst", parent["instance_id"])
				assert.Equal(t, "parent-def", parent["def_id"])
				assert.Equal(t, float64(1), parent["def_version"])
				assert.Equal(t, float64(0), parent["depth"])

				// call_children must be a non-null array (even when empty in other cases).
				children, ok := body["call_children"].([]any)
				require.True(t, ok, "call_children must be a list")
				require.Len(t, children, 1)
				child, ok := children[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "child-inst", child["instance_id"])

				// chain_predecessor must be absent (nil → omitempty).
				_, hasPred := body["chain_predecessor"]
				assert.False(t, hasPred, "chain_predecessor must be absent when nil")

				// chain_successors must be an empty array (not null).
				successors, ok := body["chain_successors"].([]any)
				require.True(t, ok, "chain_successors must be a list (never null)")
				assert.Empty(t, successors)
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
					lineage: kernel.InstanceLineage{
						InstanceID:       "test-inst-1",
						CallParent:       parentRef,
						CallChildren:     []kernel.CallLinkRef{childRef},
						ChainPredecessor: nil,
						ChainSuccessors:  []kernel.ChainLinkRef{},
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
