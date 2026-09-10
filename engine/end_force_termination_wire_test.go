package engine_test

// end_force_termination_wire_test.go — the WIRE half of the force-termination
// outcome contract, and the half nothing covered.
//
// end_force_termination_test.go proves the ENGINE honours the outcome: a node
// built in Go with event.OutcomeAbort ends at StatusTerminated + FailInstance,
// one with event.OutcomeComplete at StatusCompleted + CompleteInstance. Both
// rows there hand the engine a typed TerminationOutcome, so neither can observe
// what an AUTHORED definition arrives with.
//
// This file starts one step earlier, at the text an author writes. It decodes a
// definition from JSON, runs model.Validate over it exactly as a publish would,
// and only then steps it — so the assertion covers the whole chain from
// `termination_outcome:` to the terminal status the instance reports.
//
// That chain is where the two halves disagreed. NodeWire.TerminationOutcome is
// a string and event.EndEvent.Outcome is an int whose ZERO value is
// OutcomeComplete, so a value the decoder does not recognise does not fail and
// does not stay unset: it becomes "complete". The engine then reads
// event.OutcomeComplete, ends the instance at StatusCompleted, emits
// CompleteInstance — and the author who asked to abort is told the instance
// succeeded. Re-marshalling writes "complete" back, so the stored definition no
// longer contains the value that was authored.
//
// The near-miss rows below are that defect. The four accepting rows are what
// keeps them honest: a gate that refused every termination_outcome would satisfy
// "refused" just as well as a correct one, so exact "abort" must still reach
// StatusTerminated, exact "complete" StatusCompleted, and an unauthored outcome
// must keep the default doc.go publishes ("complete" ends at StatusCompleted).

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"

	// The definition arrives as text, so every kind it names must be registered.
	// definition/kinds is the documented bundle for code that deserializes a
	// definition; engine's own dependency on the leaves is incidental and not
	// something this test should rest on.
	_ "github.com/kartaladev/wrkflw/definition/kinds"
)

// published is one authored definition taken as far down the publish path as it
// gets: the decode result, the Validate result, and — only if both accepted —
// the step that ran it. Carrying all three is what lets a single assertion say
// where a definition that should have been refused actually ended up.
type published struct {
	decodeErr   error
	validateErr error
	res         engine.StepResult
}

// terminalName names the terminal command a step emitted, for the diagnostic
// that reports where an unrefused definition landed. "none" is a real answer:
// a step that neither completed nor failed the instance emitted neither.
func terminalName(res engine.StepResult) string {
	if _, ok := firstCommand[engine.FailInstance](res.Commands); ok {
		return "FailInstance"
	}
	if _, ok := firstCommand[engine.CompleteInstance](res.Commands); ok {
		return "CompleteInstance"
	}
	return "none"
}

func TestForceTerminationOutcomeFromTheWire(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

	// publish decodes the authored JSON, validates it, and steps it. omit drops
	// the termination_outcome key entirely, which is a different authored
	// document from an empty one even though the decoded wire cannot tell them
	// apart — both are covered below precisely because they are indistinguishable
	// after decode.
	publish := func(t *testing.T, outcome string, omit bool) published {
		t.Helper()

		end := `{"id":"halt","kind":"endEvent","end_behavior":"terminate","termination_reason":"fraud"`
		if !omit {
			end += `,"termination_outcome":` + strconv.Quote(outcome)
		}
		end += `}`
		raw := `{"id":"p-force-term-wire","version":1,` +
			`"nodes":[{"id":"start","kind":"startEvent"},` + end + `],` +
			`"flows":[{"id":"f0","source":"start","target":"halt"}]}`

		var def model.ProcessDefinition
		if err := json.Unmarshal([]byte(raw), &def); err != nil {
			return published{decodeErr: err}
		}
		got := published{validateErr: model.Validate(&def)}
		if got.validateErr != nil {
			return got
		}
		res, err := engine.Step(t.Context(), &def, engine.InstanceState{InstanceID: "i-force-wire"},
			engine.NewStartInstance(at, map[string]any{"amount": 100}), engine.StepOptions{})
		require.NoError(t, err, "the step itself must not error; this test is about which terminal status it reaches")
		got.res = res
		return got
	}

	// refused is the assertion the near-miss rows share. Its message reports the
	// whole chain rather than only the missing error, because the failure this
	// test exists to show is not "decode returned nil" — it is the instance that
	// went on to report success.
	refused := func(t *testing.T, got published) {
		t.Helper()
		require.ErrorIs(t, got.decodeErr, model.ErrInvalidTerminationOutcome,
			"an unrecognised termination_outcome must be refused at decode; instead decode returned %v, "+
				"Validate returned %v, and the instance ended at %v emitting %s",
			got.decodeErr, got.validateErr, got.res.State.Status, terminalName(got.res))
	}

	// terminated and completed are the at-limit accepts. They assert the command
	// as well as the status: the two travel together in forceTerminate, and a
	// status asserted alone would not notice if they stopped doing so.
	terminated := func(t *testing.T, got published) {
		t.Helper()
		require.NoError(t, got.decodeErr, "an exact vocabulary value must decode")
		require.NoError(t, got.validateErr)
		assert.Equal(t, engine.StatusTerminated, got.res.State.Status)
		fail, ok := firstCommand[engine.FailInstance](got.res.Commands)
		require.True(t, ok, "an abort must emit FailInstance")
		assert.Equal(t, "fraud", fail.Err, "FailInstance carries the authored termination_reason")
		_, hasComplete := firstCommand[engine.CompleteInstance](got.res.Commands)
		assert.False(t, hasComplete, "an abort must not also complete the instance")
	}
	completed := func(t *testing.T, got published) {
		t.Helper()
		require.NoError(t, got.decodeErr, "an exact vocabulary value must decode")
		require.NoError(t, got.validateErr)
		assert.Equal(t, engine.StatusCompleted, got.res.State.Status)
		done, ok := firstCommand[engine.CompleteInstance](got.res.Commands)
		require.True(t, ok, "a complete outcome must emit CompleteInstance")
		assert.Equal(t, 100, done.Result["amount"], "CompleteInstance carries the instance variables")
		_, hasFail := firstCommand[engine.FailInstance](got.res.Commands)
		assert.False(t, hasFail, "a complete outcome must not also fail the instance")
	}

	type testCase struct {
		name    string
		outcome string
		omit    bool
		assert  func(t *testing.T, got published)
	}

	cases := []testCase{
		{name: `"abort" ends at StatusTerminated`, outcome: "abort", assert: terminated},
		{name: `"complete" ends at StatusCompleted`, outcome: "complete", assert: completed},
		{
			// doc.go publishes complete as the meaning of a terminate end that
			// selects no outcome, so omitting the key is legal and must stay legal.
			name: "an omitted outcome keeps the published default",
			omit: true, assert: completed,
		},
		{
			// The empty string is the wire zero, which is why it cannot be told
			// apart from the omitted key above and is accepted for the same reason.
			name:    "an empty outcome is the omitted outcome",
			outcome: "", assert: completed,
		},
		{name: `"Abort" is refused, not read as complete`, outcome: "Abort", assert: refused},
		{name: `"ABORT" is refused, not read as complete`, outcome: "ABORT", assert: refused},
		{name: `"abrot" is refused, not read as complete`, outcome: "abrot", assert: refused},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, publish(t, tc.outcome, tc.omit))
		})
	}
}
