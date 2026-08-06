package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/authz"
)

// TestTriggerTerminalPolicies is the readable form of ADR-0165's classification:
// one row per concrete Trigger, naming the policy that trigger declares for an
// already-terminal instance. It is a white-box test (package engine) because
// terminalPolicy is unexported by design — the policy is an engine-internal
// dispatch concern, not part of the consumer API.
//
// This table does NOT by itself force a new trigger to be classified: a 16th
// variant simply omitted here compiles and leaves the table green. The length
// assertion at the bottom is what closes that hole, by driving the count off
// allTriggerVariants — the same list TestValidateTriggerKindsAreExhaustive uses.
// One list, two exhaustiveness properties. Without it this would become the
// third hand-maintained 15-item trigger list in the repo, alongside
// store.AllTriggerKinds.
//
// The compiler is still the primary enforcement: baseTrigger deliberately does
// NOT implement terminalPolicy, so a new trigger type does not satisfy Trigger
// until its author has decided. This table pins WHICH decision each one made.
func TestTriggerTerminalPolicies(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	type testCase struct {
		name    string
		trigger Trigger
		assert  func(t *testing.T, got terminalPolicy)
	}

	rejectsSilently := func(t *testing.T, got terminalPolicy) {
		t.Helper()
		assert.Equal(t, rejectSilently, got)
	}
	rejectsWithError := func(t *testing.T, got terminalPolicy) {
		t.Helper()
		assert.Equal(t, rejectWithError, got)
	}
	allowsOnTerminal := func(t *testing.T, got terminalPolicy) {
		t.Helper()
		assert.Equal(t, allowOnTerminal, got)
	}

	cases := []testCase{
		// rejectWithError — the trigger originates in a synchronous external API
		// call whose caller must not be told it succeeded.
		{name: "StartInstance", trigger: NewStartInstance(at, nil), assert: rejectsWithError},
		{name: "HumanClaimed", trigger: NewHumanClaimed(at, "h", authz.Actor{}), assert: rejectsWithError},
		{name: "HumanReassigned", trigger: NewHumanReassigned(at, "h", "a", "b", authz.Actor{}), assert: rejectsWithError},
		{name: "HumanCompleted", trigger: NewHumanCompleted(at, "h", CompletionInput{}, authz.Actor{}), assert: rejectsWithError},
		{name: "ResolveIncident", trigger: NewResolveIncident(at, "i", 0), assert: rejectsWithError},

		// rejectSilently — the trigger is delivered asynchronously by the engine's
		// own machinery, whose caller cannot tell a no-op from success and must not
		// retry.
		{name: "ActionCompleted", trigger: NewActionCompleted(at, "c", nil), assert: rejectsSilently},
		{name: "ActionFailed", trigger: NewActionFailed(at, "c", "e", false), assert: rejectsSilently},
		{name: "HumanCandidatesResolved", trigger: NewHumanCandidatesResolved(at, "h", nil), assert: rejectsSilently},
		{name: "TimerFired", trigger: NewTimerFired(at, "tm"), assert: rejectsSilently},
		{name: "SignalReceived", trigger: NewSignalReceived(at, "s", nil), assert: rejectsSilently},
		{name: "MessageReceived", trigger: NewMessageReceived(at, "m", "", nil), assert: rejectsSilently},
		{name: "SubInstanceCompleted", trigger: NewSubInstanceCompleted(at, "c", nil), assert: rejectsSilently},
		{name: "SubInstanceFailed", trigger: NewSubInstanceFailed(at, "c", "e"), assert: rejectsSilently},
		// CancelRequested was reclassified from allowOnTerminal to rejectSilently by
		// the rule-#9 audit: left tolerant it re-entered beginCompensation on a
		// force-terminated instance whose RootCompensations survive, re-firing every
		// compensation InvokeAction and publishing a second terminal event.
		{name: "CancelRequested", trigger: NewCancelRequested(at), assert: rejectsSilently},

		// CompensateRequested is the one payload-dependent policy, which is the
		// property that decided the mechanism: a per-type table cannot express it.
		{name: "CompensateRequested/plain_full_rollback", trigger: NewCompensateRequested(at, ""), assert: allowsOnTerminal},
		{name: "CompensateRequested/partial_rollback", trigger: NewCompensateRequested(at, "svc"), assert: rejectsWithError},
		{name: "CompensateRequested/reverse_to_start", trigger: NewReverseToStart(at, "start"), assert: rejectsWithError},
		{name: "CompensateRequested/reverse_to_node", trigger: NewReverseToNode(at, "svc"), assert: rejectsWithError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.trigger.terminalPolicy())
		})
	}

	// Exhaustiveness: every variant of the sealed interface must appear above.
	// Driven off the same list TestValidateTriggerKindsAreExhaustive consumes, so
	// adding a 16th trigger fails here without anyone remembering this file.
	classified := make(map[string]bool, len(cases))
	for _, tc := range cases {
		classified[triggerTypeName(tc.trigger)] = true
	}
	for _, trg := range allTriggerVariants(at) {
		assert.True(t, classified[triggerTypeName(trg)],
			"trigger %s declares a terminalPolicy but no row in this table pins which one", triggerTypeName(trg))
	}
	assert.Len(t, classified, len(allTriggerVariants(at)),
		"every Trigger variant must be classified exactly once in this table")
}
