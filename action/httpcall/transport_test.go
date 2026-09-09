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

// park registers this request in the current round and returns the channel it
// must wait on, or nil when the barrier has already given up.
//
// THE INVARIANT THE WHOLE BARRIER RESTS ON: b.release is swapped and the old
// channel closed TOGETHER under b.mu, on every path that closes — here and in
// giveUp. A channel that is still installed has therefore never been closed,
// which is what makes the identity check in giveUp a sufficient close-once
// guard. An earlier version of this code closed without swapping in the timeout
// path and this comment described the invariant anyway; every goroutine already
// committed to that path then closed an already-closed channel.
func (b *roundBarrier) park() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.assembled {
		return nil
	}
	b.arrived++
	rel := b.release
	if b.arrived == b.inFlight {
		b.arrived = 0
		b.release = make(chan struct{})
		close(rel)
	}
	return rel
}

// giveUp latches the barrier open after a round failed to assemble, releasing
// every request parked on rel.
//
// It is idempotent and it is reached CONCURRENTLY. The deadline fires once per
// PARKED GOROUTINE, not once per barrier: Go commits a goroutine to the
// time.After case when its timer fires, before the branch body runs, and every
// request in a failing round armed its timer within microseconds of the others.
// So 2..N goroutines arrive here for the same rel, and all but the first must do
// nothing. The identity check is what makes that safe, via the invariant on park.
//
// Latching — rather than re-arming per round — is deliberate: paying the
// deadline again for each remaining request turns one readable failure into
// minutes of wall clock (measured at ~160s before it was latched). The first
// round that cannot assemble is the whole diagnosis; the rest of the run only
// has to end.
func (b *roundBarrier) giveUp(rel chan struct{}) {
	b.mu.Lock()
	// Deferred, not a bare Unlock at the end. A panic under this lock would
	// otherwise leave b.mu held for good, and every later park and ok would block
	// forever — ending the run in `panic: test timed out` with no assertion
	// messages, which is the exact failure this watchdog exists to prevent.
	//
	// This is stated here rather than tested because NO TEST CAN OBSERVE IT.
	// `defer` guarantees the unlock structurally, and with the swap above in
	// place there is no panic to unwind from, so a test written against it passes
	// whether the Unlock is deferred or not — verified by removing the defer and
	// watching the check still pass. A test that cannot fail is not coverage.
	defer b.mu.Unlock()

	if b.release != rel {
		// Either the round assembled after this goroutine's timer fired, or
		// another goroutine already gave up on this round. Both closed rel.
		return
	}
	b.arrived = 0
	b.release = make(chan struct{})
	b.assembled = false
	close(rel)
}

// arrive blocks until inFlight requests are simultaneously parked here.
//
// The deadline is a fixture fallback in the sense of docs/agents/test-deadlines.md:
// it never fires on a passing run, and its only job is to turn a rendezvous that
// cannot assemble into a readable failure instead of `panic: test timed out` at
// the binary's 600s limit, which would print no assertion messages at all.
func (b *roundBarrier) arrive() {
	rel := b.park()
	if rel == nil {
		return
	}
	select {
	case <-rel:
	case <-time.After(5 * time.Second):
		b.giveUp(rel)
	}
}

// ok reports whether every round assembled within the deadline.
func (b *roundBarrier) ok() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.assembled
}

// TestRoundBarrierGiveUpUnderManyWaiters covers the one shape the connection-count
// cases structurally cannot: 2..N requests parked in a round that CANNOT assemble,
// all reaching the deadline together.
//
// Every test above either assembles its round or, when the overlap is removed,
// leaves exactly ONE request parked — and a single closer can never double-close.
// The multi-waiter timeout path is reached in a real run whenever any one of the 8
// requests fails to arrive at the handler, which is not hypothetical: a round of
// this test has been observed losing a request to
// `dial tcp 127.0.0.1:…: connect: can't assign requested address` under load.
//
// It drives giveUp directly instead of waiting on the 5s deadline. That deadline
// is a fixture fallback (see arrive): it is never paid on a passing run, and
// making a green run sit through it every time would convert it into the
// "paid on every green run" shape docs/agents/test-deadlines.md tells us to keep
// short. What needs testing is the logic the deadline guards, and that is
// reachable without it.
func TestRoundBarrierGiveUpUnderManyWaiters(t *testing.T) {
	t.Parallel()

	// A barrier that can never assemble: one more request is required than will
	// ever arrive, which is exactly the situation the deadline exists for.
	const (
		waiters  = 8
		attempts = 50
	)

	for range attempts {
		b := newRoundBarrier(waiters + 1)
		rel := b.release

		start := make(chan struct{})
		var wg sync.WaitGroup
		for range waiters {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				b.giveUp(rel)
			}()
		}
		close(start)
		wg.Wait()

		select {
		case <-rel:
		default:
			t.Fatal("giving up must release every request parked on the round, " +
				"or they wait out the binary's own timeout instead")
		}
		assert.False(t, b.ok(),
			"a round that could not assemble must latch the barrier, so the failure "+
				"is reported once instead of once per remaining request")
	}
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
// [roundBarrier]), which is what makes a count worth asserting at all. Every case
// asserts an exact number, and the low numbers are pinned beside the high ones: a
// client that reused nothing would give 32 where 8 is asserted, and one that
// reused everything would give 8 where 26 is.
//
// Those numbers are the MINIMUM of a range, not a value the test forces — the
// barrier forces overlap, not reuse. See the ⚠ block on the constants below
// before diagnosing a count that comes out high.
func TestPoolSizingOptions(t *testing.T) {
	t.Parallel()

	const (
		rounds      = 4
		concurrency = 8

		// cappedAtTwo is what `rounds` overlapping rounds of `concurrency`
		// requests cost when only Go's default number of connections stays idle
		// between rounds: the first round pays for all `concurrency` handshakes
		// because the whole round is in flight together, and each later round
		// reuses the warm ones and reopens the rest. It comes out at 26.
		//
		// Written as the derivation rather than as 26 so that changing `rounds`
		// or `concurrency` moves it with them. As a literal it would leave four
		// cases failing with a number mismatch that points at the pool instead
		// of at a stale constant.
		cappedAtTwo = concurrency + (rounds-1)*(concurrency-http.DefaultMaxIdleConnsPerHost)
	)

	// ⚠ READ BEFORE DIAGNOSING A FAILURE HERE — a documented limit, and it fails
	// CLOSED.
	//
	// The barrier forces OVERLAP. It does not force REUSE, and the two are not
	// the same claim. net/http returns a connection to the idle list from the
	// transport's readLoop goroutine, after the response body is closed, with no
	// happens-before edge to RoundTrip returning — therefore none to a.Do
	// returning, therefore none to wg.Wait() below. Nothing orders round k's
	// connections into the idle pool before round k+1 starts.
	//
	// So every count asserted below is the MINIMUM of a range: the outcome where
	// every readLoop won that race. `concurrency` is the low end of [8, 32] and
	// cappedAtTwo the low end of [26, 32]; the high end of both is
	// rounds*concurrency, which is "nothing was ever reused".
	//
	// That is why the assertions stay exact instead of being widened to a range.
	// The unforced direction can only make `opened` HIGHER, so this limit fails
	// closed: a failure reading `expected: 8, actual: 9` IS this, it needs no
	// further diagnosis, and widening the assertion to accept it would give back
	// the vacuity #156 exists to remove. Measured at 840 iterations across varied
	// GOMAXPROCS and CPU load with zero deviations, on darwin/arm64 — which is a
	// failed falsification on one machine, not a proof, and CI is Linux.

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
			name: "a total cap of 1 serialises the round onto one connection",
			// 1, not `concurrency`: WithMaxConnsPerHost(1) permits exactly one
			// connection, so an 8-way rendezvous under it could never assemble
			// and the round would deadlock into the watchdog.
			//
			// The consequence, stated rather than left to be inferred: with
			// inFlight 1 the first arrival satisfies the rendezvous immediately,
			// the deadline is never reached, and `assembled` can never go false.
			// The require.True guard below is structurally incapable of failing
			// on THIS row. It is not coverage here; it is coverage on the five
			// rows that rendezvous 8-way.
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

	// inFlight 1: this test issues a single sequential request and is not about
	// the pool at all, so the rendezvous must be satisfied by the first arrival.
	// The barrier is inert here; the argument exists only because the helper is
	// shared with TestPoolSizingOptions.
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
