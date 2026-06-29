package httpcall_test

import (
	"context"
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
