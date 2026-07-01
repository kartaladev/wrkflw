package rest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// dlaStub is a configurable service.DeadLetterAdmin test double.
type dlaStub struct {
	listFn    func(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	redriveFn func(ctx context.Context, ids ...int64) (int, error)
	gotLimit  int
	gotIDs    []int64
}

func (s *dlaStub) ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error) {
	s.gotLimit = limit
	return s.listFn(ctx, limit)
}

func (s *dlaStub) Redrive(ctx context.Context, ids ...int64) (int, error) {
	s.gotIDs = ids
	return s.redriveFn(ctx, ids...)
}

// dlqStubService is a no-op service.Service; the DLQ routes never touch it.
type dlqStubService struct{ service.Service }

// doReq issues an in-memory request against h and returns the recorder.
func doReq(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRESTListDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow returns items, normalizes limit", func(t *testing.T) {
		t.Parallel()
		created := time.Now()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
			return []runtime.DeadLetter{{ID: 7, InstanceID: "p1", Topic: "t", RetryCount: 5, LastError: "boom", CreatedAt: created}}, nil
		}}
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"id":7`)
		assert.Equal(t, 50, dla.gotLimit)
	})

	t.Run("default-deny without admin middleware -> 403", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) { return nil, nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("bad limit -> 400", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) { return nil, nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodGet, "/admin/dead-letters?limit=abc", "")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("admin error -> 500", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
			return nil, errors.New("workflow-postgres: relay: list dead-lettered: boom")
		}}
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodGet, "/admin/dead-letters", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestRESTRedriveDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired returns count", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, ids ...int64) (int, error) { return len(ids), nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[1,2,3]}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"redriven":3`)
		assert.Equal(t, []int64{1, 2, 3}, dla.gotIDs)
	})

	t.Run("empty ids -> 0", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, ids ...int64) (int, error) { return len(ids), nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[]}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"redriven":0`)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[1]}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("admin error -> 500", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, _ ...int64) (int, error) {
			return 0, errors.New("workflow-postgres: relay: redrive: boom")
		}}
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
		rec := doReq(t, h, http.MethodPost, "/admin/dead-letters/redrive", `{"ids":[1]}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestRESTWithDeadLetterAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { rest.WithDeadLetterAdmin(nil) })
}

// TestRESTDeadLetterViewCategory asserts that the dead-letter list view includes a
// "category" field populated by runtime.ClassifyDeadLetter for each item.
func TestRESTDeadLetterViewCategory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		lastError    string
		wantCategory string
	}{
		{"context deadline exceeded", "timeout"},
		{"connection refused", "connection"},
		{"validation failed for field x", "validation"},
		{"some unknown error", "unknown"},
	}

	for _, tc := range cases {
		t.Run("lastError="+tc.lastError, func(t *testing.T) {
			t.Parallel()
			created := time.Now()
			dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
				return []runtime.DeadLetter{{
					ID: 1, InstanceID: "p1", Topic: "t", RetryCount: 1,
					LastError: tc.lastError, CreatedAt: created,
				}}, nil
			}}
			h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithDeadLetterAdmin(dla))
			rec := doReq(t, h, http.MethodGet, "/admin/dead-letters", "")
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), `"category":"`+tc.wantCategory+`"`)
		})
	}
}
