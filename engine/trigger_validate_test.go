package engine

// trigger_validate_test.go — Step rejects a trigger whose identity key
// is empty, rather than letting it reach the state lookups.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

func TestValidateTriggerKey(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	type testCase struct {
		name    string
		trigger Trigger
		assert  func(t *testing.T, err error)
	}

	rejects := func(t *testing.T, err error) {
		require.ErrorIs(t, err, ErrEmptyTriggerKey)
	}
	accepts := func(t *testing.T, err error) {
		require.NoError(t, err)
	}

	cases := []testCase{
		{name: "empty ActionCompleted CommandID", trigger: NewActionCompleted(at, "", nil), assert: rejects},
		{name: "empty ActionFailed CommandID", trigger: NewActionFailed(at, "", "boom", true), assert: rejects},
		{name: "empty SubInstanceCompleted CommandID", trigger: NewSubInstanceCompleted(at, "", nil), assert: rejects},
		{name: "empty SubInstanceFailed CommandID", trigger: NewSubInstanceFailed(at, "", "boom"), assert: rejects},
		{name: "empty HumanCompleted TaskID", trigger: NewHumanCompleted(at, "", CompletionInput{}, authz.Actor{ID: "a"}), assert: rejects},
		{name: "empty HumanClaimed TaskID", trigger: NewHumanClaimed(at, "", authz.Actor{ID: "a"}), assert: rejects},
		{name: "empty HumanReassigned TaskID", trigger: NewHumanReassigned(at, "", "a", "b", authz.Actor{ID: "c"}), assert: rejects},
		{name: "empty HumanCandidatesResolved TaskID", trigger: NewHumanCandidatesResolved(at, "", nil), assert: rejects},
		{name: "empty SignalReceived Name", trigger: NewSignalReceived(at, "", nil), assert: rejects},
		{name: "empty MessageReceived Name", trigger: NewMessageReceived(at, "", "k", nil), assert: rejects},
		{name: "empty ResolveIncident IncidentID", trigger: NewResolveIncident(at, "", 0), assert: rejects},

		{name: "populated SignalReceived is accepted", trigger: NewSignalReceived(at, "sig", nil), assert: accepts},
		{name: "populated HumanClaimed is accepted", trigger: NewHumanClaimed(at, "h1", authz.Actor{ID: "a"}), assert: accepts},
		{name: "populated ActionCompleted is accepted", trigger: NewActionCompleted(at, "c1", nil), assert: accepts},
		{name: "populated ActionFailed is accepted", trigger: NewActionFailed(at, "c1", "boom", true), assert: accepts},
		{name: "populated SubInstanceCompleted is accepted", trigger: NewSubInstanceCompleted(at, "c1", nil), assert: accepts},
		{name: "populated SubInstanceFailed is accepted", trigger: NewSubInstanceFailed(at, "c1", "boom"), assert: accepts},
		{name: "populated HumanCompleted is accepted", trigger: NewHumanCompleted(at, "h1", CompletionInput{}, authz.Actor{ID: "a"}), assert: accepts},
		{name: "populated HumanReassigned is accepted", trigger: NewHumanReassigned(at, "h1", "a", "b", authz.Actor{ID: "c"}), assert: accepts},
		{name: "populated HumanCandidatesResolved is accepted", trigger: NewHumanCandidatesResolved(at, "h1", nil), assert: accepts},
		{name: "populated MessageReceived is accepted", trigger: NewMessageReceived(at, "msg", "k1", nil), assert: accepts},
		{name: "populated ResolveIncident is accepted", trigger: NewResolveIncident(at, "i1", 0), assert: accepts},

		// EXEMPTIONS — empty here is documented, meaningful, and must be accepted.
		// TimerFired is exempt because TestTimerFiredStaleTokenIsNoop pins an
		// empty TimerID as a clean no-op.
		{name: "TimerFired with empty TimerID stays a clean no-op", trigger: NewTimerFired(at, ""), assert: accepts},
		{name: "StartInstance with empty StartNodeID resolves the manual start", trigger: NewStartInstance(at, nil), assert: accepts},
		{name: "CompensateRequested with empty ToNode means full rollback", trigger: NewCompensateRequested(at, ""), assert: accepts},
		{name: "CancelRequested carries no key", trigger: NewCancelRequested(at), assert: accepts},
		{name: "MessageReceived with empty CorrelationKey is uncorrelated", trigger: NewMessageReceived(at, "msg", "", nil), assert: accepts},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, validateTriggerKey(tc.trigger))
		})
	}
}

// allTriggerVariants returns one value of every variant of the sealed Trigger
// interface, all stamped at. It is the single list that drives every
// exhaustiveness property the engine asserts over triggers: this file's
// classification check and TestTriggerTerminalPolicies' policy table.
// Adding a variant without adding it here fails the codec's own exhaustiveness
// test too.
func allTriggerVariants(at time.Time) []Trigger {
	return []Trigger{
		NewStartInstance(at, nil),
		NewActionCompleted(at, "c", nil),
		NewActionFailed(at, "c", "e", false),
		NewHumanCompleted(at, "h", CompletionInput{}, authz.Actor{}),
		NewHumanClaimed(at, "h", authz.Actor{}),
		NewHumanCandidatesResolved(at, "h", nil),
		NewHumanReassigned(at, "h", "a", "b", authz.Actor{}),
		NewTimerFired(at, "tm"),
		NewSignalReceived(at, "s", nil),
		NewMessageReceived(at, "m", "", nil),
		NewSubInstanceCompleted(at, "c", nil),
		NewSubInstanceFailed(at, "c", "e"),
		NewCompensateRequested(at, ""),
		NewCancelRequested(at),
		NewResolveIncident(at, "i", 0),
		NewSkipStalledCompensation(at, "c", ""),
	}
}

// TestValidateTriggerKindsAreExhaustive fails when a Trigger variant is added and
// classified neither as validated nor exempt, so a new variant cannot silently
// fall through validateTriggerKey's default arm. Modelled on AllTriggerKinds in
// internal/persistence/store/trigger_codec_test.go.
func TestValidateTriggerKindsAreExhaustive(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	all := allTriggerVariants(at)

	// assertKeyRowSound exercises a row's extractor against the variant its key
	// names. The bare type assertion inside read is not checkable by the
	// compiler, so a row paired with the wrong concrete type would otherwise
	// panic inside Step on the engine's hot path. Fail here instead.
	//
	// It also pins logKey, which nothing else does: an accessor with no log
	// attribute name would make dispatch's terminal guard emit the identity value
	// under the empty key.
	assertKeyRowSound := func(t *testing.T, set, name string, k triggerKey, trg Trigger) {
		t.Helper()
		assert.NotPanics(t, func() { k.read(trg) },
			"trigger %s: %s[%q].read asserts a different concrete type than its key names", name, set, name)
		assert.NotEmpty(t, k.logKey,
			"trigger %s: %s[%q] registers a read accessor but no logKey, so its identity would be "+
				"logged under the empty attribute name", name, set, name)
	}

	for _, trg := range all {
		name := triggerTypeName(trg)
		k, validated := validatedTriggerKinds[name]
		ex, exempt := exemptTriggerKinds[name]
		assert.True(t, validated != exempt,
			"trigger %s must be classified exactly once: validated=%v exempt=%v", name, validated, exempt)

		switch {
		case validated:
			assertKeyRowSound(t, "validatedTriggerKinds", name, k, trg)
		case exempt && ex.key.read != nil:
			// ⚠ Exempt rows are NOT exempt from this check. They gained optional
			// identity accessors so dispatch's terminal guard can log
			// timer_id and friends, and those accessors carry the SAME unchecked
			// type assertion — but they were unreachable here while this loop did
			// `if !validated { continue }`, which is how a mis-paired exempt row
			// (StartInstance's accessor reading t.(TimerFired).TimerID) passed the
			// whole suite green.
			//
			// That mis-pairing was NOT latent. dispatch logs both rejectSilently
			// and rejectWithError, and calls triggerIdentityAttr on both arms, so a
			// StartInstance — which is rejectWithError — delivered to a terminal
			// instance would have panicked inside Step on the bad assertion.
			// Executed, not reasoned: that delivery emits
			// `outcome=errored start_node_id=…`, which is the accessor running.
			// (An earlier version of this comment claimed the opposite, that
			// rejectWithError "is never logged".) This loop is the only thing
			// standing between such a row and a hot-path panic.
			assertKeyRowSound(t, "exemptTriggerKinds", name, ex.key, trg)
		}
	}
	assert.Len(t, all, len(validatedTriggerKinds)+len(exemptTriggerKinds),
		"every Trigger variant must appear in exactly one classification set")
}

// TestStepRejectsEmptyTriggerKey proves the validator is wired into Step and that
// a rejected trigger drives nothing.
func TestStepRejectsEmptyTriggerKey(t *testing.T) {
	t.Parallel()

	// A token parked on a signal WOULD be resumed by an empty-name broadcast
	// without the guard. Assert it did not move.
	before := InstanceState{
		Status: StatusRunning,
		Tokens: []Token{{ID: "tokActive", State: TokenActive, NodeID: "n1"}},
	}

	res, err := Step(t.Context(), nil, before, NewSignalReceived(time.Unix(0, 0).UTC(), "", nil), StepOptions{})

	require.ErrorIs(t, err, ErrEmptyTriggerKey)
	assert.Equal(t, InstanceState{}, res.State,
		"a rejected trigger must return the zero StepResult, not a partially driven state")
}
