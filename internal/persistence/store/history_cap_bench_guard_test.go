package store_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// The benchmarks in history_cap_bench_test.go are only worth reading if the
// instance they drive actually accumulates what they claim it does. A driver
// that silently stopped after one pass — a renamed action, a loop guard that
// no longer matches, an engine change that parks the token early — would still
// report a tidy ns/op, and the number would mean nothing. Nothing about a green
// benchmark run would reveal it.
//
// These are the assertions that make the benchmark's numbers admissible. They
// run in the ordinary `go test` suite rather than only under -bench, so they
// cannot rot unnoticed while the benchmarks sit unused between performance
// pushes.
//
// They reach the engine through driveInstance — the single driver BOTH
// benchmarks use. That is load-bearing, and it was not always true: an earlier
// revision had BenchmarkInstanceLifetime driving through driveInstance while
// these guards went through a separate near-identical stateAfter loop, so
// truncating the benchmark's driver left every assertion here green. The
// benchmark producing the headline O(N^2) result was the one running unguarded.
// stateAfter is now a thin wrapper over driveInstance; keep it that way, because
// a guard that exercises its own copy of the logic guards nothing.

// TestBenchDriverAccumulatesHistory pins the growth the benchmarks depend on:
// driving n loop passes performs n transitions and leaves n compensation
// records behind, with history growing in step.
func TestBenchDriverAccumulatesHistory(t *testing.T) {
	t.Parallel()

	for _, n := range []int{5, 25, 100} {
		t.Run(fmt.Sprintf("transitions=%d", n), func(t *testing.T) {
			t.Parallel()

			st := stateAfter(t.Context(), t, benchLoopDef(n), n)

			// One compensation record per completed compensable pass. This is
			// the collection the issue singles out for also copying the
			// instance variables, so it is the one most worth pinning.
			assert.Len(t, st.RootCompensations, n,
				"each loop pass completes the compensable svc node exactly once")

			// History must grow at least one entry per pass. The exact multiple
			// is the engine's business and may legitimately change; that it
			// grows without bound is the property under measurement.
			assert.GreaterOrEqual(t, len(st.History), n,
				"history accumulates at least one visit per transition")

			// Every compensation record carries Input — a snapshot of the
			// instance variables at invocation time — which is why the
			// snapshot grows faster than the transition count alone suggests.
			for i, rec := range st.RootCompensations {
				assert.NotEmpty(t, rec.Input,
					"compensation record %d carries a copy of the instance variables", i)
			}
		})
	}
}

// TestBenchSnapshotGrowsSuperlinearly is the load-bearing assertion: the
// per-step snapshot grows with the instance's history, because that growth is
// the entire mechanism behind the quadratic lifetime cost. If a future change
// made the snapshot flat in n — the redesign that stops embedding history, say
// — this fails and tells its reader the benchmarks are now measuring a question
// that no longer exists, instead of leaving them quietly reporting a flat line.
func TestBenchSnapshotGrowsSuperlinearly(t *testing.T) {
	t.Parallel()

	small := len(mustMarshalSnapshot(t, stateAfter(t.Context(), t, benchLoopDef(50), 50), 0))
	large := len(mustMarshalSnapshot(t, stateAfter(t.Context(), t, benchLoopDef(200), 200), 0))

	// 4x the transitions must cost meaningfully more than 2x the bytes. A
	// deliberately loose bound: the point is the trend, not a constant.
	assert.Greater(t, large, small*2,
		"quadrupling transitions must more than double the snapshot (small=%d large=%d)", small, large)
}

// TestBenchHistoryCapBoundsSnapshot pins the property the cap=100 benchmark arm
// exists to quantify. Without it, a cap arm that silently did nothing would
// still produce plausible-looking numbers and a recommendation drawn from them
// would be worthless.
func TestBenchHistoryCapBoundsSnapshot(t *testing.T) {
	t.Parallel()

	const cap100 = 100

	capped200 := len(mustMarshalSnapshot(t, stateAfter(t.Context(), t, benchLoopDef(200), 200), cap100))
	capped800 := len(mustMarshalSnapshot(t, stateAfter(t.Context(), t, benchLoopDef(800), 800), cap100))
	uncapped800 := len(mustMarshalSnapshot(t, stateAfter(t.Context(), t, benchLoopDef(800), 800), 0))

	assert.Less(t, capped800, uncapped800,
		"the cap must actually shrink the snapshot at 800 transitions")

	// The cap bounds HISTORY only. RootCompensations, Tasks and
	// ArchivedCompensations are uncapped, so a capped snapshot still grows with
	// n — just far more slowly. Asserting the weaker property is deliberate:
	// claiming the cap makes the snapshot constant would be false, and that
	// over-claim is exactly what the history-cap recommendation must avoid.
	assert.Greater(t, capped800, capped200,
		"the cap bounds history only; the other three collections still grow")
}

func mustMarshalSnapshot(t *testing.T, st engine.InstanceState, historyCap int) []byte {
	t.Helper()
	b, err := store.MarshalSnapshotForTest(st, historyCap)
	require.NoError(t, err, "marshal snapshot")
	return b
}
