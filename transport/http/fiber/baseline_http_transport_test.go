package fiber_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/fiber"
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

	app := newApp()
	fiber.Mount(app, svc) // no WithRequestActor: no identity configured
	fiber.AdminRoutes{Svc: svc}.Customize(app)

	var body struct {
		Items []struct {
			InstanceID string `json:"instance_id"`
		} `json:"items"`
	}
	status := appDoJSON(t, app, newGetRequest(t, "/admin/instances"), &body)

	require.Equal(t, http.StatusOK, status)
	require.Len(t, body.Items, 1, "exactly one instance exists")
	assert.Equal(t, inst.State().InstanceID, body.Items[0].InstanceID)
}
