package store

import (
	"encoding/json"

	"github.com/kartaladev/wrkflw/engine"
)

// marshalSnapshot encodes the durable `snapshot` column for one step: the whole
// InstanceState as a single JSON document, history-capped per n.
//
// Create and Commit both persist the snapshot this way and previously spelled
// the two-call sequence out separately. Naming it once keeps the encoding in a
// single place and, more to the point, gives the benchmarks something real to
// measure: BenchmarkSnapshotMarshal calls THIS function, so it cannot drift
// away from what the store actually does the way a hand-copied
// json.Marshal(capHistory(...)) in a test would.
//
// Cost note, since it is the whole subject of the benchmarks: this is O(size of
// the entire instance state), paid on EVERY step. History, Tasks,
// RootCompensations and ArchivedCompensations all grow without bound unless n
// caps the first of them, so an instance's total marshalling cost over its life
// is quadratic in the number of transitions it makes.
func marshalSnapshot(st engine.InstanceState, n int) ([]byte, error) {
	return json.Marshal(capHistory(st, n))
}

// capHistory returns a copy of st whose History retains every OPEN visit
// (LeftAt == nil) plus at most the most recent n CLOSED visits, preserving the
// original relative order. n <= 0 means "no cap" and returns st unchanged.
//
// Safety: engine.Step reads History only via setVisitActor and
// closeVisit, both of which match ONLY open visits. Open visits are never
// dropped, so a capped snapshot drives identical execution on reload; closed
// visits are pure audit (the wrkflw_journal table remains the full record).
func capHistory(st engine.InstanceState, n int) engine.InstanceState {
	if n <= 0 {
		return st
	}
	// Count closed visits to compute the keep-threshold for the most-recent n.
	// History is append-only on node entry, so append position == chronological order.
	closedTotal := 0
	for i := range st.History {
		if st.History[i].LeftAt != nil {
			closedTotal++
		}
	}
	if closedTotal <= n {
		return st // nothing to trim
	}
	dropClosed := closedTotal - n // number of oldest closed visits to drop
	kept := make([]engine.NodeVisit, 0, len(st.History)-dropClosed)
	dropped := 0
	for i := range st.History {
		v := st.History[i]
		if v.LeftAt != nil && dropped < dropClosed {
			dropped++
			continue // skip an old closed visit
		}
		kept = append(kept, v)
	}
	// Return a copy with the trimmed slice; the input's History (a freshly
	// allocated `kept` never aliases st.History's backing array) is left intact.
	result := st
	result.History = kept
	return result
}
