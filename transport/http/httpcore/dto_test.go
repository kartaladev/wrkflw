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

func TestActorJSONTags(t *testing.T) {
	const in = `{"id":"u1","roles":["admin","user"]}`
	var got httpcore.Actor
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "u1" || len(got.Roles) != 2 || got.Roles[0] != "admin" {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestClaimInputJSONTags(t *testing.T) {
	const in = `{"actor":{"id":"u1","roles":["reviewer"]}}`
	var got httpcore.ClaimInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.Actor.ID != "u1" || len(got.Actor.Roles) != 1 {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestCompleteInputJSONTags(t *testing.T) {
	const in = `{"actor":{"id":"u1","roles":[]},"output":{"approved":true}}`
	var got httpcore.CompleteInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.Actor.ID != "u1" || got.Output["approved"].(bool) != true {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}

func TestReassignInputJSONTags(t *testing.T) {
	const in = `{"from":"alice","to":"bob","by":{"id":"mgr","roles":["manager"]}}`
	var got httpcore.ReassignInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.From != "alice" || got.To != "bob" || got.By.ID != "mgr" {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
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
// contract (ADR-0146): the outcome and note travel beside the output variables,
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
			body: `{"actor":{"id":"u-jane"},"outcome":"approve","note":"budget confirmed","output":{"amount":100}}`,
			assert: func(t *testing.T, in httpcore.CompleteInput) {
				require.Equal(t, "u-jane", in.Actor.ID)
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
