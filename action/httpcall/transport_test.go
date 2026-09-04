package httpcall_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action/httpcall"
)

// connCountingServer starts an httptest server that counts the TCP connections
// it accepts, and returns the server plus a func reading the count.
//
// Counting accepted connections is what makes the pool observable from outside
// the client: a reused idle connection produces no new StateNew, so the count
// is exactly "how many handshakes did this cost".
func connCountingServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()

	var mu sync.Mutex
	var opened int

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			mu.Lock()
			opened++
			mu.Unlock()
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return opened
	}
}

// TestPoolSizingOptions covers the two pool knobs and, more importantly, the
// boundary between them and an injected client.
//
// Each case drives real requests through a real server and counts accepted
// connections, because the pool is only observable through its effect: the
// fields live on an unexported Transport inside an unexported struct, and a
// test that reached in to assert on them would pass whether or not the client
// ever used them.
func TestPoolSizingOptions(t *testing.T) {
	t.Parallel()

	const (
		rounds      = 4
		concurrency = 8
	)

	cases := []struct {
		name string
		// opts builds the options under test; srv is the counting server.
		opts   func(srv *httptest.Server) []httpcall.Option
		assert func(t *testing.T, opened int)
	}{
		{
			name: "the default keeps every connection of a concurrent round warm",
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(srv.URL), httpcall.WithMethod(http.MethodGet)}
			},
			assert: func(t *testing.T, opened int) {
				assert.LessOrEqual(t, opened, concurrency,
					"the default per-host idle cap must not throw warm connections away")
			},
		},
		{
			name: "a per-host idle cap below the concurrency reopens the excess",
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(srv.URL),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithMaxIdleConnsPerHost(2),
				}
			},
			assert: func(t *testing.T, opened int) {
				// This is the behaviour Go's default imposes, asserted here so
				// the option is shown to actually reach the Transport: a lower
				// cap must cost MORE connections, not fewer.
				assert.Greater(t, opened, concurrency,
					"a cap of 2 must force reconnections once a round exceeds it")
			},
		},
		{
			name: "a non-positive idle cap restores Go's default",
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(srv.URL),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithMaxIdleConnsPerHost(0),
				}
			},
			assert: func(t *testing.T, opened int) {
				assert.Greater(t, opened, concurrency,
					"0 means DefaultMaxIdleConnsPerHost (2), not unlimited")
			},
		},
		{
			name: "a negative idle cap restores Go's default rather than leaking through",
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(srv.URL),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithMaxIdleConnsPerHost(-1),
				}
			},
			assert: func(t *testing.T, opened int) {
				// http.Transport.maxIdleConnsPerHost() falls back to the default
				// only on exactly 0, so a negative would otherwise be carried
				// through into the pool. This pins the normalisation.
				assert.Greater(t, opened, concurrency,
					"a negative cap must behave as the default (2), not as some "+
						"other value the standard library happens to derive from it")
			},
		},
		{
			name: "a total cap of 1 serialises the round onto one connection",
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(srv.URL),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithMaxConnsPerHost(1),
				}
			},
			assert: func(t *testing.T, opened int) {
				assert.Equal(t, 1, opened,
					"a total cap of 1 must block the round onto a single connection "+
						"rather than opening more")
			},
		},
		{
			name: "an injected client keeps its own transport",
			opts: func(srv *httptest.Server) []httpcall.Option {
				// A client whose Transport pins the pool to 2. If the pool
				// options were applied to an injected client, or if the
				// injected client were replaced, this would reuse everything
				// and open at most `concurrency`.
				tr := http.DefaultTransport.(*http.Transport).Clone()
				tr.MaxIdleConnsPerHost = 2
				return []httpcall.Option{
					httpcall.WithBaseURL(srv.URL),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithHTTPClient(&http.Client{Transport: tr}),
					// Deliberately contradicts the injected client; must be ignored.
					httpcall.WithMaxIdleConnsPerHost(concurrency),
				}
			},
			assert: func(t *testing.T, opened int) {
				assert.Greater(t, opened, concurrency,
					"the injected client's own Transport must win; the pool options "+
						"must not reach into a consumer-owned client")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, opened := connCountingServer(t)
			a := httpcall.NewHTTPCall(tc.opts(srv)...)

			for range rounds {
				var wg sync.WaitGroup
				for range concurrency {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := a.Do(t.Context(), map[string]any{})
						assert.NoError(t, err)
					}()
				}
				wg.Wait()
			}

			tc.assert(t, opened())
		})
	}
}

// wrappedRoundTripper stands in for the instrumentation wrappers consumers
// routinely install — otelhttp.NewTransport, an APM agent, a replay harness.
// What matters is only that it is a RoundTripper and NOT an *http.Transport.
type wrappedRoundTripper struct{ inner http.RoundTripper }

func (w wrappedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return w.inner.RoundTrip(r)
}

// TestNewHTTPCallSurvivesWrappedDefaultTransport pins that constructing an
// action does not depend on http.DefaultTransport being an *http.Transport.
//
// It is a package-level variable, and replacing it with a wrapper is a
// mainstream pattern:
//
//	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
//
// An earlier version of the pool change type-asserted DefaultTransport back to
// *http.Transport in order to Clone it. In any process that had wrapped it, that
// assertion panicked inside NewHTTPCall — during wiring at startup, where a
// consumer has no error to handle and no obvious culprit. The transport is now
// constructed outright, so this test both proves the panic is gone and pins the
// reason the code does not reach for DefaultTransport at all.
//
// Not parallel: it swaps a process-global.
func TestNewHTTPCallSurvivesWrappedDefaultTransport(t *testing.T) {
	prev := http.DefaultTransport
	http.DefaultTransport = wrappedRoundTripper{inner: prev}
	t.Cleanup(func() { http.DefaultTransport = prev })

	srv, _ := connCountingServer(t)

	require.NotPanics(t, func() {
		a := httpcall.NewHTTPCall(
			httpcall.WithBaseURL(srv.URL),
			httpcall.WithMethod(http.MethodGet),
		)
		out, err := a.Do(t.Context(), map[string]any{})
		require.NoError(t, err, "the action must still work, not merely construct")
		assert.Equal(t, 200, out["httpStatus"])
	}, "constructing an action must not depend on the type behind http.DefaultTransport")
}
