package view_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/view"
)

// secret is the canary. Every assertion below looks for THIS string, so a site the fixture
// forgets to populate is a site the test cannot police.
const secret = "111-22-3333"

// stateWithSecretEverywhere populates ALL SEVEN measured snapshots of the process variables
// plus the actor, note and policy sites.
//
// ⚠ The seven variables sites are load-bearing and were derived by execution, not by
// reading: an earlier design leaked four of them while its test passed, because the fixture
// only populated the top-level Variables map.
func stateWithSecretEverywhere(t *testing.T) engine.InstanceState {
	t.Helper()

	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	vars := func() map[string]any { return map[string]any{"ssn": secret} }
	rec := func() engine.CompensationRecord {
		return engine.CompensationRecord{NodeID: "n", Action: "a", CompletedAt: now, Input: vars()}
	}

	ended := now.Add(time.Hour)

	st := engine.InstanceState{
		InstanceID: "i1",
		DefID:      "d1",
		DefVersion: 1,
		// ⚠ NOT StatusRunning: it is iota 0, so a projection that DROPPED Status would
		// still compare equal to a fixture using it. Every public field here must be
		// non-zero or TestClassification_MatchesPublicState cannot police it.
		Status:             engine.StatusCompleted,
		StartedAt:          now,
		EndedAt:            &ended,
		PendingCancel:      true,
		PendingFinalStatus: engine.StatusTerminated,

		Variables:      vars(), // site 1
		StartVariables: vars(), // site 2
		Tokens: []engine.Token{{
			ID: "tok1", NodeID: "approve", State: engine.TokenWaiting,
			EnteredAt: now,
			Payload:   vars(), // site 3
			// Not a variables site, but a business identifier the classification withholds.
			AwaitMessageKey: secret,
		}},
		Tasks: []humantask.HumanTask{{
			TaskID: "t1", NodeID: "approve", InstanceID: "i1",
			State: humantask.Claimed, CreatedAt: now,
			Vars:        vars(), // site 4
			Eligibility: authz.AuthzSpec{Roles: []string{secret}},
			Candidates:  []authz.Actor{{ID: secret}},
			Claim:       &humantask.Claim{Actor: authz.Actor{ID: secret}, At: now},
			Completion: &humantask.Completion{
				Actor: authz.Actor{ID: secret}, At: now, Note: secret,
			},
		}},
		RootCompensations: []engine.CompensationRecord{rec()}, // site 5
		Scopes: []engine.Scope{{
			ID: "s1", NodeID: "sub",
			Compensations: []engine.CompensationRecord{rec()}, // site 6
		}},
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"s0": {rec()}, // site 7
		},
		Incidents:       []engine.Incident{{ID: "inc1", Error: secret}},
		PendingFinalErr: secret,
		History:         []engine.NodeVisit{{NodeID: "start", TokenID: "tok1", EnteredAt: now}},

		// Withheld scalars and string slices, populated so the drift guard can actually
		// police them. A zero value here would make its assertion vacuous for that field.
		DeferredCompensationThrows: []string{secret},
		RecentCompensationCmdIDs:   []string{secret},
		CmdSeq:                     7, TokenSeq: 7, TaskSeq: 7,
		TimerSeq: 7, ScopeSeq: 7, IncidentSeq: 7,
	}
	// ⚠ The compensation cursor's TYPE is unexported, but every one of its fields is
	// exported — so this package can both populate it and project it. It was previously
	// listed as unpoliceable, which understated what the guard could reach.
	//
	// Records[].Input is a FIFTH snapshot of the process variables, and FinalErr is the
	// same string as PendingFinalErr, which is withheld under every category.
	st.Compensating.ActiveCmdID = "cmd-1"
	st.Compensating.ScopeID = "s1"
	st.Compensating.StartedAt = now
	st.Compensating.NextIndex = 1
	st.Compensating.Records = []engine.CompensationRecord{rec()}
	st.Compensating.FinalErr = secret
	return st
}

// unpoliceableByFixture lists withheld InstanceState fields this external test package
// CANNOT populate, because their element types are unexported in engine.
//
// ⚠ They are not exempt from classification — TestClassification_IsTotal still covers them,
// and PublicState still omits them by construction. What is impossible is the DRIFT check:
// `view_test` cannot build a timerRecord, so it cannot prove a leak of one. Listing them
// keeps that blindness declared instead of silent; a field leaving this list because engine
// exported its type will start being policed automatically.
var unpoliceableByFixture = map[string]string{
	"Timers":                     "[]timerRecord — element type unexported",
	"ArmedEvents":                "[]armedEvent — element type unexported",
	"Boundaries":                 "[]boundaryArm — element type unexported",
	"EventTriggeredSubprocesses": "[]eventTriggeredSubprocessArm — element type unexported",
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestPublicState_WithholdsEverySecret is the headline assertion: nothing the fixture
// planted survives the closed projection.
func TestPublicState_WithholdsEverySecret(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t)

	// Sanity: the fixture really does carry the canary, so a green result below means the
	// projection removed it rather than the fixture never having had it.
	if n := strings.Count(marshal(t, st), secret); n == 0 {
		t.Fatal("fixture plants no secret — every assertion in this file would be vacuous")
	}

	got := view.PublicState(st, authz.DisclosureSet{})
	if body := marshal(t, got); strings.Contains(body, secret) {
		t.Errorf("closed projection leaks %q\nbody=%s", secret, body)
	}
}

func TestPublicState_KeepsStructuralFields(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t)
	got := view.PublicState(st, authz.DisclosureSet{})

	if got.InstanceID != st.InstanceID || got.DefID != st.DefID || got.Status != st.Status {
		t.Error("instance identity and status are structural and must survive")
	}
	if len(got.Tokens) != len(st.Tokens) || len(got.Tasks) != len(st.Tasks) {
		t.Errorf("token/task COUNTS are structural: got %d/%d want %d/%d",
			len(got.Tokens), len(got.Tasks), len(st.Tokens), len(st.Tasks))
	}
	if got.Tasks[0].State != st.Tasks[0].State {
		t.Error("task State is the claimed/completed discriminator and must survive")
	}
	if got.Tasks[0].TaskID != "t1" || got.Tokens[0].NodeID != "approve" {
		t.Error("task and token identity are structural and must survive")
	}
}

// TestPublicState_DoesNotMutateInput guards the property that makes this safe to call on
// state the engine still owns.
//
// ⚠ It exercises the DiscloseActors branch deliberately. An earlier draft called only with
// the zero set, which never enters the branch that copies Completion — so the prescribed
// no-mutation test could not have caught the shared-pointer write it was written to prevent.
func TestPublicState_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t)

	for _, d := range []authz.DisclosureSet{
		{},
		authz.NewDisclosureSet(authz.DiscloseActors),
		authz.NewDisclosureSet(authz.DiscloseActors, authz.DiscloseVariables),
	} {
		_ = view.PublicState(st, d)
	}

	if st.Variables["ssn"] != secret {
		t.Error("input Variables mutated")
	}
	if st.Tasks[0].Vars == nil {
		t.Error("input task Vars mutated")
	}
	if st.Tasks[0].Completion.Note != secret {
		t.Errorf("input Completion.Note mutated to %q — the projection wrote through a "+
			"shared *Completion pointer", st.Tasks[0].Completion.Note)
	}
	if st.Tasks[0].Claim.Actor.ID != secret {
		t.Error("input Claim.Actor mutated")
	}
}

// TestPublicState_CategoriesAreIndependent pins that widening one category does not widen
// another. The Actors/Notes pair is the sharp case: the note lives INSIDE the Completion
// struct that DiscloseActors restores.
func TestPublicState_CategoriesAreIndependent(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t)

	cases := []struct {
		name   string
		set    authz.DisclosureSet
		assert func(t *testing.T, got engine.InstanceState)
	}{
		{
			name: "actors alone restores the claim actor but NOT the note",
			set:  authz.NewDisclosureSet(authz.DiscloseActors),
			assert: func(t *testing.T, got engine.InstanceState) {
				if got.Tasks[0].Claim == nil || got.Tasks[0].Claim.Actor.ID != secret {
					t.Error("DiscloseActors must restore the claim actor")
				}
				if got.Tasks[0].Completion.Note != "" {
					t.Errorf("DiscloseActors must NOT restore the note, got %q",
						got.Tasks[0].Completion.Note)
				}
			},
		},
		{
			name: "actors+notes restores both",
			set:  authz.NewDisclosureSet(authz.DiscloseActors, authz.DiscloseNotes),
			assert: func(t *testing.T, got engine.InstanceState) {
				if got.Tasks[0].Completion.Note != secret {
					t.Error("DiscloseActors+DiscloseNotes must restore the note")
				}
			},
		},
		{
			name: "variables alone restores variables but NOT actors",
			set:  authz.NewDisclosureSet(authz.DiscloseVariables),
			assert: func(t *testing.T, got engine.InstanceState) {
				if got.Variables["ssn"] != secret || got.Tasks[0].Vars["ssn"] != secret {
					t.Error("DiscloseVariables must restore every variables snapshot")
				}
				if got.Tasks[0].Claim != nil {
					t.Error("DiscloseVariables must not restore actors")
				}
			},
		},
		{
			// ⚠ THE CASE THAT WAS MISSING. The table tested actors, actors+notes,
			// variables and policy — never notes ALONE, which is exactly the
			// configuration that silently produced nothing while both godocs promised
			// independence.
			name: "notes alone restores the note without the actor",
			set:  authz.NewDisclosureSet(authz.DiscloseNotes),
			assert: func(t *testing.T, got engine.InstanceState) {
				if got.Tasks[0].Completion == nil {
					t.Fatal("DiscloseNotes alone must restore the Completion carrying the note")
				}
				if got.Tasks[0].Completion.Note != secret {
					t.Errorf("note not restored, got %q", got.Tasks[0].Completion.Note)
				}
				if got.Tasks[0].Completion.Actor.ID != "" {
					t.Error("DiscloseNotes must NOT restore the completing actor")
				}
				if got.Tasks[0].Claim != nil {
					t.Error("DiscloseNotes must not restore the claim")
				}
			},
		},
		{
			// The operator escape hatch: active_command_id and incident ids reach the
			// wire on /snapshot alone, and no category restored them.
			name: "operations restores the compensation cursor and incident ids",
			set:  authz.NewDisclosureSet(authz.DiscloseOperations),
			assert: func(t *testing.T, got engine.InstanceState) {
				if len(got.Incidents) == 0 {
					t.Fatal("DiscloseOperations must restore incidents so ResolveIncident is reachable")
				}
				if got.Incidents[0].ID != "inc1" {
					t.Errorf("incident id not restored, got %q", got.Incidents[0].ID)
				}
				if got.Incidents[0].Error != "" {
					t.Errorf("incident ERROR TEXT must stay withheld — it is consumer "+
						"error text and may embed variables; got %q", got.Incidents[0].Error)
				}
				if got.Variables != nil {
					t.Error("DiscloseOperations must not restore variables")
				}
				if got.Compensating.ActiveCmdID != "cmd-1" {
					t.Error("DiscloseOperations must restore the compensation cursor id — " +
						"it is what makes a wedged instance recoverable")
				}
				// ⚠ Records[].Input is a FIFTH variables snapshot; FinalErr is the same
				// string as PendingFinalErr, which is withheld under every category.
				if got.Compensating.Records != nil {
					t.Errorf("DiscloseOperations leaked compensation record inputs: %v",
						got.Compensating.Records)
				}
				if got.Compensating.FinalErr != "" {
					t.Errorf("DiscloseOperations leaked terminal error text: %q",
						got.Compensating.FinalErr)
				}
			},
		},
		{
			name: "policy alone restores eligibility but NOT variables",
			set:  authz.NewDisclosureSet(authz.DisclosePolicy),
			assert: func(t *testing.T, got engine.InstanceState) {
				if len(got.Tasks[0].Eligibility.Roles) == 0 {
					t.Error("DisclosePolicy must restore the eligibility spec")
				}
				if got.Variables != nil {
					t.Error("DisclosePolicy must not restore variables")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, view.PublicState(st, tc.set))
		})
	}
}
