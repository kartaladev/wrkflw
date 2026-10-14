package gin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	ginlib "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	ginadapter "github.com/kartaladev/wrkflw/transport/http/gin"
)

// TestBaseline_AdminRoutes_UnidentifiedCallerListsInstance characterizes
// openspec/specs/http-transport "Admin routes registered without
// authentication" over the real service (TestAdminRoutes_ListInstances_200
// uses a fake): with no identity configured, an unidentified caller's
// GET /admin/instances answers 200 and the body lists the one instance that
// exists.
func TestBaseline_AdminRoutes_UnidentifiedCallerListsInstance(t *testing.T) {
	t.Parallel()

	_, svc := transporttest.NewHarness(t, transporttest.LinearProcess())
	inst, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
	})
	require.NoError(t, err)

	r := ginlib.New()
	ginadapter.Mount(r, svc) // no WithRequestActor: no identity configured
	ginadapter.AdminRoutes{Svc: svc}.Customize(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/admin/instances")

	require.Equal(t, http.StatusOK, resp.StatusCode, "body=%s", resp.Body)
	var body struct {
		Items []struct {
			InstanceID string `json:"instance_id"`
		} `json:"items"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Items, 1, "exactly one instance exists")
	assert.Equal(t, inst.State().InstanceID, body.Items[0].InstanceID)
}
