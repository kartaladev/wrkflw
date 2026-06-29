package httpcall_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/zakyalvan/krtlwrkflw/action/httpcall"
)

func ExampleNewHTTPCall() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	a := httpcall.NewHTTPCall(httpcall.WithBaseURL(srv.URL), httpcall.WithMethod(http.MethodGet))
	out, _ := a.Do(context.Background(), nil)
	fmt.Println(out["httpStatus"])
	// Output: 200
}

// ExampleWithBodyValidator demonstrates plugging in a stdlib-only required-field
// validator for the JSON request body. No schema-engine dependency is needed;
// callers supply any check they need as a plain function.
func ExampleWithBodyValidator() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// requiredFields returns an error if any of the named keys are absent from the
	// JSON object body. This is a self-contained stdlib validator — no schema library
	// is imported.
	requiredFields := func(fields ...string) httpcall.BodyValidator {
		return func(_ context.Context, body []byte) error {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(body, &m); err != nil {
				return fmt.Errorf("body is not a JSON object: %w", err)
			}
			for _, f := range fields {
				if _, ok := m[f]; !ok {
					return fmt.Errorf("required field %q is missing", f)
				}
			}
			return nil
		}
	}

	a := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL),
		httpcall.WithBodyKey("order"),
		httpcall.WithBodyValidator(requiredFields("id", "amount")),
	)

	// Valid payload — both required fields present.
	out, err := a.Do(context.Background(), map[string]any{
		"order": map[string]any{"id": "ord-1", "amount": 99},
	})
	fmt.Println(out["httpStatus"], err)

	// Invalid payload — "amount" is missing.
	_, err = a.Do(context.Background(), map[string]any{
		"order": map[string]any{"id": "ord-2"},
	})
	fmt.Println(err != nil)
	// Output:
	// 201 <nil>
	// true
}
