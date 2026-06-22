package rest_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// allowAdmin is a pass-through admin middleware used in tests to grant access.
func allowAdmin(next http.Handler) http.Handler {
	return next
}

// denyAdmin is an admin middleware that always returns 403 Forbidden.
func denyAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}

// doJSON performs a request against handler and returns the decoded JSON body as map[string]any.
// If the response is not JSON (e.g. plain-text error), it returns nil and the status code is still
// available from the recorder; callers should check the recorder's Code directly in gate tests.
func doJSON(t *testing.T, h http.Handler, method, target string) (map[string]any, int) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		var v map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
			t.Fatalf("doJSON decode: %v — body: %s", err, rec.Body.String())
		}
		return v, rec.Code
	}
	// Try to decode JSON for non-200 too (error bodies are JSON).
	var v map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&v)
	return v, rec.Code
}

// seedInstances starts n linear process instances and returns their instance IDs.
func seedInstances(t *testing.T, svc service.Service, n int) []string {
	t.Helper()
	def := linearProcess()
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("seed-inst-%02d", i+1)
		_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
			DefRef:     def.ID,
			InstanceID: id,
			Vars:       map[string]any{"name": fmt.Sprintf("user%d", i)},
		})
		if err != nil {
			t.Fatalf("seedInstances: StartInstance[%d]: %v", i, err)
		}
		ids[i] = id
	}
	return ids
}

// --- Pagination tests ---

// TestAdminListPaginationTwoPages asserts the two-page no-overlap walk:
// Page 1: limit=2 → 2 items, has_more=true, non-empty next_cursor.
// Page 2: cursor=<next> → remaining items, has_more=false; no ID overlap with page 1.
func TestAdminListPaginationTwoPages(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	_ = seedInstances(t, svc, 3)

	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	// Page 1.
	body1, code1 := doJSON(t, h, http.MethodGet, "/admin/instances?limit=2")
	if code1 != http.StatusOK {
		t.Fatalf("page1: want 200, got %d", code1)
	}
	items1, ok := body1["items"].([]any)
	if !ok {
		t.Fatalf("page1: items not a list: %v", body1["items"])
	}
	if len(items1) != 2 {
		t.Fatalf("page1: want 2 items, got %d", len(items1))
	}
	if body1["has_more"] != true {
		t.Fatalf("page1: want has_more=true, got %v", body1["has_more"])
	}
	cursor, ok := body1["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("page1: want non-empty next_cursor, got %v", body1["next_cursor"])
	}

	// Collect page-1 IDs.
	page1IDs := make(map[string]bool, len(items1))
	for _, item := range items1 {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("page1 item is not a map: %T", item)
		}
		id, _ := m["instance_id"].(string)
		page1IDs[id] = true
	}

	// Page 2.
	body2, code2 := doJSON(t, h, http.MethodGet, "/admin/instances?limit=2&cursor="+cursor)
	if code2 != http.StatusOK {
		t.Fatalf("page2: want 200, got %d", code2)
	}
	items2, ok := body2["items"].([]any)
	if !ok {
		t.Fatalf("page2: items not a list: %v", body2["items"])
	}
	if len(items2) == 0 {
		t.Fatal("page2: want at least 1 item")
	}
	if body2["has_more"] != false {
		t.Fatalf("page2: want has_more=false, got %v", body2["has_more"])
	}

	// Assert no ID overlap between pages.
	for _, item := range items2 {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("page2 item is not a map: %T", item)
		}
		id, _ := m["instance_id"].(string)
		if page1IDs[id] {
			t.Fatalf("overlap: instance %q appeared in both page 1 and page 2", id)
		}
	}
}

// --- Status filter tests ---

// TestAdminListStatusFilter asserts that ?status=completed returns only completed instances
// and that ?status=bogus returns 400.
func TestAdminListStatusFilter(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	// linearProcess() completes synchronously (greet + end). All seeded instances will be "completed".
	_ = seedInstances(t, svc, 2)

	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	t.Run("known status returns matching instances", func(t *testing.T) {
		body, code := doJSON(t, h, http.MethodGet, "/admin/instances?status=completed")
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d", code)
		}
		items, ok := body["items"].([]any)
		if !ok {
			t.Fatalf("items is not a list: %v", body["items"])
		}
		for _, item := range items {
			m, _ := item.(map[string]any)
			if m["status"] != "completed" {
				t.Errorf("want all status=completed, got %v", m["status"])
			}
		}
	})

	t.Run("unknown status returns 400", func(t *testing.T) {
		_, code := doJSON(t, h, http.MethodGet, "/admin/instances?status=bogus")
		if code != http.StatusBadRequest {
			t.Fatalf("want 400 for unknown status, got %d", code)
		}
	})
}

// --- Admin middleware gate tests ---

// TestAdminGateDefaultDeny asserts that when no WithAdminMiddleware option is supplied,
// GET /admin/instances returns 403 (default-deny — the endpoint is NOT openly readable).
func TestAdminGateDefaultDeny(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc) // no WithAdminMiddleware

	_, code := doJSON(t, h, http.MethodGet, "/admin/instances")
	if code != http.StatusForbidden {
		t.Fatalf("default-deny: want 403, got %d", code)
	}
}

// TestAdminGateDenyingMiddleware asserts that a denying admin middleware blocks the request
// and the lister is never called (checked indirectly: no items in body, 403 returned).
func TestAdminGateDenyingMiddleware(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc, rest.WithAdminMiddleware(denyAdmin))

	req := httptest.NewRequest(http.MethodGet, "/admin/instances", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("denying middleware: want 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminGatePassthroughMiddleware asserts that an allow-all middleware grants access.
func TestAdminGatePassthroughMiddleware(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	_ = seedInstances(t, svc, 1)
	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	body, code := doJSON(t, h, http.MethodGet, "/admin/instances")
	if code != http.StatusOK {
		t.Fatalf("passthrough middleware: want 200, got %d", code)
	}
	if _, ok := body["items"]; !ok {
		t.Fatal("passthrough middleware: want items field in response")
	}
}

// --- Malformed cursor test ---

// TestAdminListMalformedCursor asserts that a malformed cursor string returns 400.
func TestAdminListMalformedCursor(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	_, code := doJSON(t, h, http.MethodGet, "/admin/instances?cursor=!!!not-valid-base64!!!")
	if code != http.StatusBadRequest {
		t.Fatalf("malformed cursor: want 400, got %d", code)
	}
}

// --- Non-admin routes unaffected ---

// TestAdminMiddlewareDoesNotAffectNonAdminRoutes asserts that adding WithAdminMiddleware
// does not break non-admin routes (e.g. GET /instances/{id} still returns 200).
func TestAdminMiddlewareDoesNotAffectNonAdminRoutes(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: def.ID, InstanceID: "admin-nonaffect-1", Vars: map[string]any{"name": "z"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}

	// Both denyAdmin middleware (admin routes blocked) and a regular non-admin get should
	// still succeed because admin mw only applies to /admin/... routes.
	h := rest.NewHandler(svc, rest.WithAdminMiddleware(denyAdmin))

	req := httptest.NewRequest(http.MethodGet, "/instances/admin-nonaffect-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("non-admin route: want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestWithAdminMiddlewareNilPanics asserts that passing nil to WithAdminMiddleware panics immediately.
func TestWithAdminMiddlewareNilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil middleware but none occurred")
		}
	}()
	rest.WithAdminMiddleware(nil)
}

// --- Total count tests ---

// TestAdminListTotalCountTrue asserts that ?total=true returns total_count in the response body.
func TestAdminListTotalCountTrue(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	_ = seedInstances(t, svc, 3)

	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	body, code := doJSON(t, h, http.MethodGet, "/admin/instances?limit=1&total=true")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	// total_count should be present and equal 3 (all instances)
	rawCount, ok := body["total_count"]
	if !ok {
		t.Fatalf("want total_count in response body, got keys: %v", body)
	}
	// JSON numbers decode as float64
	count, ok := rawCount.(float64)
	if !ok {
		t.Fatalf("want total_count as number, got %T: %v", rawCount, rawCount)
	}
	if int(count) != 3 {
		t.Fatalf("want total_count=3, got %v", count)
	}
	// items are limited to 1
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items not a list: %v", body["items"])
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item (limit=1), got %d", len(items))
	}
}

// TestAdminListTotalCountFalse asserts that without ?total=true, total_count is 0.
func TestAdminListTotalCountFalse(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	_ = seedInstances(t, svc, 2)

	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	body, code := doJSON(t, h, http.MethodGet, "/admin/instances")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	rawCount, ok := body["total_count"]
	if !ok {
		t.Fatalf("want total_count field always present, got keys: %v", body)
	}
	count, ok := rawCount.(float64)
	if !ok {
		t.Fatalf("want total_count as number, got %T", rawCount)
	}
	if int(count) != 0 {
		t.Fatalf("want total_count=0 when not requested, got %v", count)
	}
}

// TestAdminListTotalCountOne asserts that ?total=1 also enables the count (alternate form).
func TestAdminListTotalCountOne(t *testing.T) {
	def := linearProcess()
	_, svc := newTestHarness(t, def)
	_ = seedInstances(t, svc, 2)

	h := rest.NewHandler(svc, rest.WithAdminMiddleware(allowAdmin))

	body, code := doJSON(t, h, http.MethodGet, "/admin/instances?total=1")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	rawCount, ok := body["total_count"]
	if !ok {
		t.Fatalf("want total_count in response body")
	}
	count, _ := rawCount.(float64)
	if int(count) != 2 {
		t.Fatalf("want total_count=2, got %v", count)
	}
}
