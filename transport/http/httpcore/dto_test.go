package httpcore_test

import (
	"encoding/json"
	"testing"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/stretchr/testify/require"
)

func TestStartInputJSONTags(t *testing.T) {
	const in = `{"def_ref":"order","vars":{"amount":42}}`
	var got httpcore.StartInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.DefRef != model.Latest("order") || got.Vars["amount"].(float64) != 42 {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestSignalInputJSONTags(t *testing.T) {
	const in = `{"signal":"approved","payload":{"note":"ok"}}`
	var got httpcore.SignalInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.Signal != "approved" || got.Payload["note"].(string) != "ok" {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestMessageInputJSONTags(t *testing.T) {
	const in = `{"name":"payment","correlation_key":"ord-1","payload":{"amt":10}}`
	var got httpcore.MessageInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "payment" || got.CorrelationKey != "ord-1" {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

// TestTaskDTOsIgnoreAStaleActorField pins the migration contract: the actor
// fields are GONE from the three task DTOs, and a body still carrying the legacy
// "actor"/"by" keys must DECODE CLEANLY with the value simply unread.
//
// A 400 here would buy no security — nothing reads the value — and would break
// consumers' rollout windows. Executed on all three adapters: no DisallowUnknownFields
// exists anywhere in transport/ or internal/.
func TestTaskDTOsIgnoreAStaleActorField(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		var got httpcore.ClaimInput
		if err := json.Unmarshal([]byte(`{"actor":{"id":"u1","roles":["reviewer"]}}`), &got); err != nil {
			t.Fatal(err)
		}
		if got != (httpcore.ClaimInput{}) {
			t.Fatalf("stale actor must not be read: %+v", got)
		}
	})

	t.Run("complete keeps its own fields", func(t *testing.T) {
		const in = `{"actor":{"id":"u1","roles":[]},"outcome":"approve","output":{"approved":true}}`
		var got httpcore.CompleteInput
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Fatal(err)
		}
		if got.Outcome != "approve" || got.Output["approved"].(bool) != true {
			t.Fatalf("wire tags mismatch: %+v", got)
		}
	})

	t.Run("reassign keeps from/to, drops by", func(t *testing.T) {
		const in = `{"from":"alice","to":"bob","by":{"id":"mgr","roles":["manager"]}}`
		var got httpcore.ReassignInput
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Fatal(err)
		}
		if got.From != "alice" || got.To != "bob" {
			t.Fatalf("from/to are task PARTICIPANTS and must survive: %+v", got)
		}
	})
}

// Admin DTOs.

func TestPolicyRuleInputJSONTags(t *testing.T) {
	const in = `{"subject":"alice","object":"/instances","action":"read"}`
	var got httpcore.PolicyRuleInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "alice" || got.Object != "/instances" || got.Action != "read" {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestRoleBindingInputJSONTags(t *testing.T) {
	const in = `{"user":"alice","role":"admin"}`
	var got httpcore.RoleBindingInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.User != "alice" || got.Role != "admin" {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestRedriveInputJSONTags(t *testing.T) {
	const in = `{"ids":[1,2,3]}`
	var got httpcore.RedriveInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.IDs) != 3 || got.IDs[0] != 1 || got.IDs[2] != 3 {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestResolveIncidentInputJSONTags(t *testing.T) {
	const in = `{"add_attempts":3}`
	var got httpcore.ResolveIncidentInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.AddAttempts != 3 {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

// TestCompleteInputCarriesOutcomeAndNote pins the completion request body's wire
// contract: the outcome and note travel beside the output variables,
// so an actor's business disposition and remark are first-class rather than
// smuggled through output by convention. Both are optional.
func TestCompleteInputCarriesOutcomeAndNote(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		body   string
		assert func(t *testing.T, in httpcore.CompleteInput)
	}

	cases := []testCase{
		{
			name: "outcome and note decode alongside output",
			// ⚠ The body still carries "actor": that is the legacy shape, kept here
			// deliberately to pin that a lagging client's body still decodes cleanly.
			body: `{"actor":{"id":"u-jane"},"outcome":"approve","note":"budget confirmed","output":{"amount":100}}`,
			assert: func(t *testing.T, in httpcore.CompleteInput) {
				require.Equal(t, "approve", in.Outcome)
				require.Equal(t, "budget confirmed", in.Note)
				require.Equal(t, map[string]any{"amount": float64(100)}, in.Output)
			},
		},
		{
			name: "a body without an outcome decodes to empty strings",
			body: `{"actor":{"id":"u-jane"},"output":{}}`,
			assert: func(t *testing.T, in httpcore.CompleteInput) {
				require.Empty(t, in.Outcome)
				require.Empty(t, in.Note)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var in httpcore.CompleteInput
			require.NoError(t, json.Unmarshal([]byte(tc.body), &in))
			tc.assert(t, in)
		})
	}
}
