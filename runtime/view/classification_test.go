package view_test

import (
	"reflect"
	"testing"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/view"
)

// disposition is what PublicState does with one field.
type disposition int

const (
	// public: copied into the closed projection unconditionally. Structural.
	public disposition = iota
	// withheld: never copied, under any disclosure category.
	withheld
	// gated: copied only when the named category is disclosed.
	gatedVariables
	gatedActors
	gatedPolicy
	gatedNotes
	gatedOperations
)

// The classification. THIS TABLE IS THE DESIGN — see PublicState's doc comment.
//
// ⚠ TestClassification_IsTotal fails on any exported field that is absent here, so a field
// added to the engine tomorrow cannot be silently disclosed OR silently withheld: someone
// must classify it. That is the invariant replacing the hand-maintained list of sensitive
// fields that failed four times in a row.
var classification = map[string]map[string]disposition{
	"InstanceState": {
		"InstanceID": public, "DefID": public, "DefVersion": public, "Status": public,
		"StartedAt": public, "EndedAt": public, "PendingCancel": public,
		"PendingFinalStatus": public, "Tokens": public, "Tasks": public, "History": public,

		"Variables": gatedVariables, "StartVariables": gatedVariables,
		"RootCompensations": gatedVariables, "Scopes": gatedVariables,
		"ArchivedCompensations": gatedVariables,

		// Withheld under every category. The sequence counters and internal bookkeeping
		// have no consumer-facing meaning; PendingFinalErr and Incidents carry consumer
		// error text, which may embed variables.
		"Incidents": gatedOperations, "Compensating": gatedOperations,
		"PendingFinalErr": withheld,
		"Timers":          withheld, "ArmedEvents": withheld, "Boundaries": withheld,
		"EventTriggeredSubprocesses": withheld, "DeferredCompensationThrows": withheld,
		"RecentCompensationCmdIDs": withheld,
		"CmdSeq":                   withheld, "TokenSeq": withheld, "TaskSeq": withheld,
		"TimerSeq": withheld, "ScopeSeq": withheld, "IncidentSeq": withheld,
	},
	"Token": {
		"ID": public, "NodeID": public, "ScopeID": public, "State": public,
		"EnteredAt": public, "RetryAttempts": public, "RetryStartedAt": public,

		"Payload": gatedVariables,

		// A correlation key or signal name is a business identifier, not a variable,
		// an actor or a policy — so no category restores these.
		"AwaitCommand": withheld, "AwaitSignal": withheld, "AwaitMessage": withheld,
		"AwaitMessageKey": withheld, "AwaitTimer": withheld,
	},
	"HumanTask": {
		"TaskID": public, "NodeID": public, "InstanceID": public, "State": public,
		"CreatedAt": public, "DueAt": public,

		"Vars":        gatedVariables,
		"Eligibility": gatedPolicy,
		"Candidates":  gatedActors, "Claim": gatedActors, "Completion": gatedActors,
	},
	"NodeVisit": {
		// Wholly structural; recorded explicitly so the guard has an answer for each.
		"NodeID": public, "TokenID": public, "EnteredAt": public, "LeftAt": public,
		"TaskID": public, "CloseKind": public,
	},
	// ⚠ The three types below are copied WHOLESALE when their gating category is set, so
	// "withheld by construction" does NOT protect them: a field added to Completion rides
	// out under DiscloseActors with no code change. Classifying them is the only guard.
	"Claim": {
		"Actor": gatedActors, "At": gatedActors,
	},
	"Completion": {
		"Actor": gatedActors, "At": gatedActors, "Outcome": gatedActors,
		// ⚠ The note has its OWN category, and the struct is restored by EITHER — who
		// completed a task and what they wrote are independent disclosures.
		"Note": gatedNotes,
	},
	// ⚠ The compensation cursor is copied WHOLESALE (its type is unexported, so
	// runtime/view cannot build a fresh one), which is exactly the hazard the three
	// types above carry. It was unclassified until a security review found
	// Records[].Input — a fifth process-variable snapshot — and FinalErr riding out
	// under DiscloseOperations alone.
	"compensationCursor": {
		// Operator-facing execution position: what makes a wedged instance recoverable.
		"ScopeID": gatedOperations, "ArchiveKey": gatedOperations,
		"ResumeNode": gatedOperations, "ResumeScope": gatedOperations,
		"ToNode": gatedOperations, "ReverseNode": gatedOperations,
		"ReverseResetVars": gatedOperations, "RestoreTargetVars": gatedOperations,
		"StartRecordCount": gatedOperations, "TeardownArchiveKey": gatedOperations,
		"TeardownArchiveOffset": gatedOperations, "TeardownArchiveCount": gatedOperations,
		"NextIndex": gatedOperations, "StartedAt": gatedOperations,
		"ActiveCmdID": gatedOperations, "RetryAttempts": gatedOperations,
		"RetryTimerID": gatedOperations, "FinalStatus": gatedOperations,
		// ⚠ Records[].Input is a process-variable snapshot; FinalErr is the same string
		// as PendingFinalErr and Incident.Error. Both need DiscloseVariables.
		"Records": gatedVariables, "FinalErr": gatedVariables,
	},
	"CompensationRecord": {
		"NodeID": gatedOperations, "Action": gatedOperations, "CompletedAt": gatedOperations,
		"Input": gatedVariables,
	},
	"Incident": {
		// The operator-facing fields: enough to call ResolveIncident.
		"ID": gatedOperations, "Kind": gatedOperations, "TokenID": gatedOperations,
		"NodeID": gatedOperations, "ScopeID": gatedOperations,
		"CommandID": gatedOperations, "Attempts": gatedOperations,
		"CreatedAt": gatedOperations,
		// ⚠ err.Error() from the consumer's action, verbatim; may embed variables.
		"Error": gatedVariables,
	},
	"Actor": {
		"ID": gatedActors, "Roles": gatedActors, "Attributes": gatedActors,
	},
}

var classifiedTypes = []struct {
	name string
	typ  reflect.Type
}{
	{"InstanceState", reflect.TypeOf(engine.InstanceState{})},
	{"Token", reflect.TypeOf(engine.Token{})},
	{"HumanTask", reflect.TypeOf(humantask.HumanTask{})},
	{"NodeVisit", reflect.TypeOf(engine.NodeVisit{})},
	{"Claim", reflect.TypeOf(humantask.Claim{})},
	{"Completion", reflect.TypeOf(humantask.Completion{})},
	{"Actor", reflect.TypeOf(authz.Actor{})},
	{"Incident", reflect.TypeOf(engine.Incident{})},
	// ⚠ Reached through the exported FIELD, since the cursor's own type is unexported.
	{"compensationCursor", reflect.TypeOf(engine.InstanceState{}).Field(fieldIndex("Compensating")).Type},
	{"CompensationRecord", reflect.TypeOf(engine.CompensationRecord{})},
}

// fieldIndex returns the index of an exported InstanceState field by name.
func fieldIndex(name string) int {
	f, ok := reflect.TypeOf(engine.InstanceState{}).FieldByName(name)
	if !ok {
		panic("InstanceState has no field " + name)
	}
	return f.Index[0]
}

// TestClassification_IsTotal is the invariant the whole disclosure posture rests on.
//
// It fails on a field NOBODY THOUGHT ABOUT, rather than on one someone forgot to add to a
// list. Its predecessor enumerated known render paths and could only fail on omissions its
// author already knew to look for; this one fails on the unknown.
//
// Ablate it by ADDING A FIELD to any classified struct — not by editing this table.
func TestClassification_IsTotal(t *testing.T) {
	t.Parallel()

	for _, tc := range classifiedTypes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			declared := classification[tc.name]
			if declared == nil {
				t.Fatalf("%s has no classification entry at all", tc.name)
			}

			seen := map[string]bool{}
			for i := range tc.typ.NumField() {
				f := tc.typ.Field(i)
				if !f.IsExported() {
					continue // unexported state is unreachable from another package
				}
				seen[f.Name] = true
				if _, ok := declared[f.Name]; !ok {
					t.Errorf("%s.%s is UNCLASSIFIED.\n"+
						"Add it to the `classification` table in this file, and — if it is "+
						"public or gated — copy it in PublicState. Until then it is withheld "+
						"by construction, which is safe but undeclared.", tc.name, f.Name)
				}
			}

			// A removed field must not leave a lingering entry: a stale classification
			// makes the table over-report its own coverage.
			for name := range declared {
				if !seen[name] {
					t.Errorf("%s.%s is classified but no longer exists — stale entry",
						tc.name, name)
				}
			}
		})
	}
}

// TestClassification_MatchesPublicState binds the table to the implementation.
//
// Without this the two could drift: a field marked public that PublicState never copies, or
// one marked withheld that it copies anyway. The table would keep passing while describing
// something the code does not do.
func TestClassification_MatchesPublicState(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t)
	got := view.PublicState(st, authz.DisclosureSet{}) // closed: only `public` may survive

	src := reflect.ValueOf(st)
	rv := reflect.ValueOf(got)
	for name, disp := range classification["InstanceState"] {
		// ANTI-VACUITY, checked first and reported distinctly: a field the fixture leaves
		// zero cannot police EITHER disposition — a dropped public field still compares
		// zero, and a leaked withheld one still compares zero. This arm is why the fixture
		// sets Status to StatusCompleted rather than StatusRunning, which is iota 0.
		if src.FieldByName(name).IsZero() {
			if why, known := unpoliceableByFixture[name]; known {
				t.Logf("UNPOLICED (declared): InstanceState.%s — %s. Covered by "+
					"TestClassification_IsTotal and withheld by construction, but this "+
					"drift check cannot prove it.", name, why)
				continue
			}
			t.Errorf("FIXTURE GAP: InstanceState.%s is zero in stateWithSecretEverywhere, "+
				"so this test cannot police its disposition (%v). Populate it, or declare "+
				"it in unpoliceableByFixture with the reason.", name, disp)
			continue
		}

		f := rv.FieldByName(name)
		switch disp {
		case public:
			if f.IsZero() {
				t.Errorf("InstanceState.%s is classified public but PublicState left it "+
					"zero in the closed projection", name)
			}
		case withheld, gatedVariables, gatedActors, gatedPolicy, gatedNotes, gatedOperations:
			if !f.IsZero() {
				t.Errorf("InstanceState.%s is classified %v but survived the CLOSED "+
					"projection", name, disp)
			}
		}
	}
}
