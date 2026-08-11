// Package service_test is the black-box test suite for the service facade.
package service_test

// ADR-0175 — the operator-facing surface for a stalled compensation walk.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/service"
)

// TestProcessInstanceProjectsTheCompensationCursor covers ADR-0175 decision 5.
//
// Without this projection an instance that was ALREADY stalled before the
// delivery shipped is undetectable: both arm sites are DISPATCH sites, and a
// stalled walk never dispatches again, so a consumer who upgrades *because* they
// have wedged instances would see zero incidents. The projection is also the
// only way an operator can read the CommandID the three verbs require.
//
// ⚠ Surfacing CommandID is a deliberate trade: it exposes an internal
// "<instance>-cN" sequence oracle.
func TestProcessInstanceProjectsTheCompensationCursor(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "i-stalled",
		DefID:      "d1",
		DefVersion: 1,
		Status:     engine.StatusCompensating,
		StartedAt:  started,
		Incidents: []engine.Incident{{
			ID:        "inc1",
			Kind:      engine.IncidentCompensationStall,
			CommandID: "i-stalled-c4",
			NodeID:    "b",
			Error:     "compensation action stalled",
			CreatedAt: started.Add(time.Hour),
		}},
	}
	// compensationCursor is unexported, but its FIELDS are exported and reachable
	// through the exported Compensating field — no test shim needed.
	st.Compensating.ActiveCmdID = "i-stalled-c4"
	st.Compensating.StartedAt = started.Add(30 * time.Minute)

	data, err := json.Marshal(service.NewProcessInstance(nil, st))
	require.NoError(t, err)

	var doc struct {
		Compensating *struct {
			ActiveCommandID string     `json:"active_command_id"`
			Since           *time.Time `json:"since"`
		} `json:"compensating"`
		Incidents []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"incidents"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	require.NotNil(t, doc.Compensating, "a compensating instance must project its cursor")
	assert.Equal(t, "i-stalled-c4", doc.Compensating.ActiveCommandID,
		"the operator needs this id to name the stalled dispatch")
	require.NotNil(t, doc.Compensating.Since,
		"and a since-timestamp to tell a wedged walk from a healthy one")
	assert.Equal(t, started.Add(30*time.Minute), *doc.Compensating.Since)

	require.Len(t, doc.Incidents, 1)
	assert.Equal(t, "IncidentCompensationStall", doc.Incidents[0].Kind,
		"the kind must be visible, or a stall is indistinguishable from a failed action")
}

// TestProcessInstanceOmitsSinceForAPreUpgradeWalk covers the population this
// projection exists for (found by /code-review).
//
// StartedAt is stamped only at WALK START, so a walk already in flight when this
// build is deployed carries the zero time — and those are exactly the instances
// the projection is meant to make findable: they raise no incident, because both
// arm sites are DISPATCH sites and a stalled walk never dispatches again.
//
// Rendering year 1 as a timestamp would read as data, not as "unknown". The
// field is a pointer so it is OMITTED instead.
func TestProcessInstanceOmitsSinceForAPreUpgradeWalk(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{
		InstanceID: "i-preupgrade", DefID: "d1", DefVersion: 1,
		Status: engine.StatusCompensating, StartedAt: time.Now().UTC(),
	}
	st.Compensating.ActiveCmdID = "i-preupgrade-c4" // no StartedAt: pre-upgrade cursor

	data, err := json.Marshal(service.NewProcessInstance(nil, st))
	require.NoError(t, err)

	var doc struct {
		Compensating map[string]any `json:"compensating"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	require.NotNil(t, doc.Compensating, "the cursor still projects — that is the point")
	assert.Equal(t, "i-preupgrade-c4", doc.Compensating["active_command_id"])
	_, present := doc.Compensating["since"]
	assert.False(t, present,
		"an unknown walk-start must be ABSENT, not rendered as year 1")
}

// TestProcessInstanceOmitsTheCursorWhenNotCompensating pins that the projection
// is absent — not an empty object — on an ordinary instance.
func TestProcessInstanceOmitsTheCursorWhenNotCompensating(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(service.NewProcessInstance(nil, engine.InstanceState{
		InstanceID: "i-running", DefID: "d1", DefVersion: 1,
		Status: engine.StatusRunning, StartedAt: time.Now().UTC(),
	}))
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	_, present := doc["compensating"]
	assert.False(t, present, "a running instance must not carry a compensation cursor")
}

// TestResolveCompensationStallReachesTheDriver covers the service method: it
// must resolve the definition, delegate, and propagate the engine's refusal
// rather than swallowing it.
//
// An operator escape that reported success while doing nothing would be the same
// defect ADR-0175 documents for CancelInstance, one layer up.
func TestResolveCompensationStallReachesTheDriver(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		instanceID string
		assert     func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:       "an instance with no walk in flight is refused, not silently accepted",
			instanceID: "svc-stall-1",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrNoCompensationWalk,
					"the engine's refusal must survive the service layer")
			},
		},
		{
			name:       "an unknown instance fails definition resolution",
			instanceID: "no-such-instance",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.NotErrorIs(t, err, engine.ErrNoCompensationWalk,
					"a missing instance is a lookup failure, not a walk-state refusal")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, svc := transporttest.NewHarness(t, transporttest.ApprovalProcess())
			transporttest.StartedApprovalInstance(t, h, "svc-stall-1")

			_, err := svc.ResolveCompensationStall(t.Context(), service.ResolveCompensationStallRequest{
				InstanceID:  tc.instanceID,
				CommandID:   "svc-stall-1-c4",
				Disposition: engine.CompensationSkip,
			})
			tc.assert(t, err)
		})
	}
}
