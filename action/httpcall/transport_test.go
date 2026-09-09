package httpcall_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action/httpcall"
)

// roundBarrier holds every request the server is serving until inFlight of them
// are in flight at the same moment, then releases them together and resets for
// the next round.
//
// It exists because every assertion below is about what a CONCURRENT round
// costs, and a `go func()` fan-out does not establish concurrency — it only
// makes it possible. A scheduler that lets early requests finish before later
// goroutines start hands each of them the same warm connection, the accepted
// count collapses to 1, and every "a lower cap must cost MORE connections"
// assertion fails against a round that was never concurrent. That is #156: a
// test asserting a property nothing in it forces to hold. The barrier forces
// it, so the counts stop depending on how the scheduler happens to interleave.
type roundBarrier struct {
	inFlight int

	mu        sync.Mutex
	arrived   int
	release   chan struct{}
	assembled bool
}

func newRoundBarrier(inFlight int) *roundBarrier {
	return &roundBarrier{
		inFlight:  inFlight,
		release:   make(chan struct{}),
		assembled: true,
	}
}

// arrive blocks until inFlight requests are simultaneously parked here.
//
// The deadline is a fixture fallback in the sense of docs/agents/test-deadlines.md:
// it never fires on a passing run, and its only job is to turn a rendezvous that
// cannot assemble into a readable failure instead of `panic: test timed out` at
// the binary's 600s limit, which would print no assertion messages at all.
//
// It fires at most once per barrier. A timeout latches the barrier open —
// every later arrival returns immediately — because the alternative is to pay
// the deadline again for each of the remaining requests, which turns one
// readable failure into minutes of wall clock. The first round that cannot
// assemble is the whole diagnosis; the rest of the run only has to end.
func (b *roundBarrier) arrive() {
	b.mu.Lock()
	if !b.assembled {
		b.mu.Unlock()
		return
	}
	b.arrived++
	rel := b.release
	if b.arrived == b.inFlight {
		b.arrived = 0
		b.release = make(chan struct{})
		close(rel)
	}
	b.mu.Unlock()

	select {
	case <-rel:
	case <-time.After(5 * time.Second):
		b.mu.Lock()
		// Only the goroutine that still sees its own round's channel closes it;
		// swapping and closing happen together under the lock, so a channel that
		// is still installed has not been closed.
		if b.release == rel {
			b.assembled = false
			close(rel)
		}
		b.mu.Unlock()
	}
}

// ok reports whether every round assembled within the deadline.
func (b *roundBarrier) ok() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.assembled
}

// connCountingServer starts an httptest server that counts the TCP connections
// it accepts, holds each round of requests open until inFlight of them overlap,
// and returns the server plus a func reading the count and a func reporting
// whether every rendezvous assembled.
//
// Counting accepted connections is what makes the pool observable from outside
// the client: a reused idle connection produces no new StateNew, so the count
// is exactly "how many handshakes did this cost".
func connCountingServer(t *testing.T, inFlight int) (*httptest.Server, func() int, func() bool) {
	t.Helper()

	var mu sync.Mutex
	var opened int

	barrier := newRoundBarrier(inFlight)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		barrier.arrive()
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
	}, barrier.ok
}

// TestPoolSizingOptions covers the two pool knobs and, more importantly, the
// boundary between them and an injected client.
//
// Each case drives real requests through a real server and counts accepted
// connections, because the pool is only observable through its effect: the
// fields live on an unexported Transport inside an unexported struct, and a
// test that reached in to assert on them would pass whether or not the client
// ever used them.
//
// The server holds each round open until the whole round overlaps (see
// [roundBarrier]), so the counts are exact rather than a lower bound the
// scheduler may or may not deliver. Every case therefore asserts an exact
// number, and the low numbers are pinned beside the high ones: a client that
// reused nothing would give 32 where 8 is asserted, and one that reused
// everything would give 8 where 26 is.
func TestPoolSizingOptions(t *testing.T) {
	t.Parallel()

	const (
		rounds      = 4
		concurrency = 8
		// cappedAtTwo is what `rounds` overlapping rounds of `concurrency`
		// requests cost when the pool keeps only 2 connections idle between
		// rounds: the first round pays 8 handshakes because all 8 requests are
		// in flight together, and each later round reuses the 2 warm
		// connections and pays for the other 6. 8 + 3*6.
		//
		// The number is exact only because the barrier forces the overlap. It
		// is the whole point of #156: without it the round may serialise onto
		// one warm connection and the same option produces 1.
		cappedAtTwo = 26
	)

	cases := []struct {
		name string
		// inFlight is how many requests the server holds open at once. It is
		// per case, not global: WithMaxConnsPerHost(1) permits exactly one
		// connection, so an 8-way rendezvous under it could never assemble.
		inFlight int
		// opts builds the options under test; srv is the counting server.
		opts   func(srv *httptest.Server) []httpcall.Option
		assert func(t *testing.T, opened int)
	}{
		{
			name:     "the default keeps every connection of a concurrent round warm",
			inFlight: concurrency,
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{httpcall.WithBaseURL(srv.URL), httpcall.WithMethod(http.MethodGet)}
			},
			assert: func(t *testing.T, opened int) {
				assert.Equal(t, concurrency, opened,
					"the default per-host idle cap (100) must keep all %d connections of "+
						"the first round warm, so the %d later rounds cost no handshakes",
					concurrency, rounds-1)
			},
		},
		{
			name:     "a per-host idle cap below the concurrency reopens the excess",
			inFlight: concurrency,
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
				assert.Equal(t, cappedAtTwo, opened,
					"a cap of 2 must force reconnections once a round exceeds it")
			},
		},
		{
			name:     "a non-positive idle cap restores Go's default",
			inFlight: concurrency,
			opts: func(srv *httptest.Server) []httpcall.Option {
				return []httpcall.Option{
					httpcall.WithBaseURL(srv.URL),
					httpcall.WithMethod(http.MethodGet),
					httpcall.WithMaxIdleConnsPerHost(0),
				}
			},
			assert: func(t *testing.T, opened int) {
				assert.Equal(t, cappedAtTwo, opened,
					"0 means DefaultMaxIdleConnsPerHost (2), not unlimited")
			},
		},
		{
			name:     "a negative idle cap restores Go's default rather than leaking through",
			inFlight: concurrency,
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
				assert.Equal(t, cappedAtTwo, opened,
					"a negative cap must behave as the default (2), not as some "+
						"other value the standard library happens to derive from it")
			},
		},
		{
			name:     "a total cap of 1 serialises the round onto one connection",
			inFlight: 1,
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
			name:     "an injected client keeps its own transport",
			inFlight: concurrency,
			opts: func(srv *httptest.Server) []httpcall.Option {
				// A client whose Transport pins the pool to 2. If the pool
				// options were applied to an injected client, or if the
				// injected client were replaced, this would reuse everything
				// and open exactly `concurrency`.
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
				assert.Equal(t, cappedAtTwo, opened,
					"the injected client's own Transport must win; the pool options "+
						"must not reach into a consumer-owned client")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, opened, assembled := connCountingServer(t, tc.inFlight)
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

			require.True(t, assembled(),
				"a round never reached %d simultaneous in-flight requests within the "+
					"barrier deadline; the connection counts below would be measuring "+
					"a round that was not concurrent", tc.inFlight)
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

	srv, _, _ := connCountingServer(t, 1)

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
