package stdlib_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
)

// TestBaseline_AdminRoutes_UnidentifiedCallerListsInstance characterizes
// openspec/specs/http-transport "Admin routes registered without
// authentication": with no identity configured, an unidentified caller's
// GET /admin/instances answers 200 and the body lists the one instance that
// exists.
func TestBaseline_AdminRoutes_UnidentifiedCallerListsInstance(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
	inst, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	require.NoError(t, err)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc) // no WithRequestActor: no identity configured
	stdlib.AdminRoutes{Svc: svc}.Customize(mux)

	rr := do(mux, newGetRequest(t, "/admin/instances"))

	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body)
	var body struct {
		Items []struct {
			InstanceID string `json:"instance_id"`
		} `json:"items"`
	}
	decodeJSON(t, rr.Body, &body)
	require.Len(t, body.Items, 1, "exactly one instance exists")
	assert.Equal(t, inst.State().InstanceID, body.Items[0].InstanceID)
}
