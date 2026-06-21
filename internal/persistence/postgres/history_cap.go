package postgres

import "github.com/zakyalvan/krtlwrkflw/engine"

// capHistory returns a copy of st whose History retains every OPEN visit
// (LeftAt == nil) plus at most the most recent n CLOSED visits, preserving the
// original relative order. n <= 0 means "no cap" and returns st unchanged.
//
// Safety (ADR-0021): engine.Step reads History only via setVisitActor and
// closeVisit, both of which match ONLY open visits. Open visits are never
// dropped, so a capped snapshot drives identical execution on reload; closed
// visits are pure audit (the wrkflw_journal table remains the full record).
func capHistory(st engine.InstanceState, n int) engine.InstanceState {
	if n <= 0 {
		return st
	}
	// Count closed visits to compute the keep-threshold for the most-recent n.
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
