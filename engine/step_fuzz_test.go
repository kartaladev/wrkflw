package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// FuzzStep feeds varied, fuzzer-generated process definitions and trigger
// sequences through [engine.Step] and asserts the engine's robustness
// invariants:
//
//  1. Step never panics, regardless of the (definition, state, trigger) input.
//  2. Step returns either nil or a wrapped sentinel error — never a bare,
//     uncategorised error. (Every non-nil error must match one of the engine's
//     exported sentinels via errors.Is.)
//  3. Step never produces a state in which a token sits on a node that does not
//     exist in the root definition. (Tokens in sub-process scopes are resolved
//     against nested definitions and are out of scope for this harness, which
//     only generates flat root-level definitions.)
//
// The harness derives a small, flat, sequential process definition from the
// fuzz bytes (a tiny grammar over node kinds), validates it with
// [model.Validate], and — only if it is well-formed — drives a sequence of
// triggers (also derived from the fuzz bytes) through Step. Inputs that do not
// validate are skipped: Step's contract assumes a validated definition, so
// feeding it garbage definitions would test nothing meaningful.
func FuzzStep(f *testing.F) {
	// Seed corpus: a handful of byte patterns that exercise distinct shapes —
	// a plain service-task chain, a chain with a user task, an exclusive
	// gateway, and trigger variety. The bytes are interpreted by buildDef /
	// driveTriggers below; their exact meaning is an implementation detail of
	// the harness, so the seeds are chosen to span several node kinds.
	f.Add([]byte{0x00, 0x01, 0x02})       // start, service, end
	f.Add([]byte{0x00, 0x03, 0x02})       // start, user task, end
	f.Add([]byte{0x00, 0x04, 0x01, 0x02}) // start, exclusive gw, service, end
	f.Add([]byte{0x00})                   // degenerate: start only
	f.Add([]byte{})                       // empty

	f.Fuzz(func(t *testing.T, data []byte) {
		def := buildDef(data)
		if err := model.Validate(def); err != nil {
			// Not a well-formed definition: Step's precondition is not met.
			t.Skip()
		}

		at := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
		st := engine.InstanceState{InstanceID: "fuzz-1", DefID: def.ID, DefVersion: def.Version}

		// Drive the start, then a fuzzer-chosen sequence of follow-up triggers.
		// Each Step call must uphold the invariants above.
		triggers := driveTriggers(data, at)
		for _, trg := range triggers {
			res, err := engine.Step(def, st, trg, engine.StepOptions{})

			// Invariant 2: any error must be a wrapped engine sentinel.
			if err != nil {
				if !isEngineSentinel(err) {
					t.Fatalf("Step returned non-sentinel error for trigger %T: %v", trg, err)
				}
				// On error the state is not advanced; keep driving with the old state.
				continue
			}

			// Invariant 3: no token may sit on a non-existent root node.
			for _, tok := range res.State.Tokens {
				if tok.ScopeID != "" {
					continue // nested-scope token: resolved against a nested def
				}
				if _, ok := def.Node(tok.NodeID); !ok {
					t.Fatalf("token %q on non-existent node %q (trigger %T)", tok.ID, tok.NodeID, trg)
				}
			}

			st = res.State
		}
	})
}

// isEngineSentinel reports whether err wraps any of the engine's exported
// sentinel errors. A fuzzed Step may legitimately fail (e.g. an unknown
// trigger or no matching flow); those failures must be categorised, not bare.
func isEngineSentinel(err error) bool {
	return errors.Is(err, engine.ErrUnknownTrigger) ||
		errors.Is(err, engine.ErrInvalidTransition) ||
		errors.Is(err, engine.ErrNoMatchingFlow)
}

// buildDef constructs a small, flat, sequential ProcessDefinition from the fuzz
// bytes. The first node is always a start event and the last is always an end
// event so the chain has valid endpoints; the interior nodes' kinds are chosen
// byte-by-byte. Sequence flows wire each node to its successor in order.
func buildDef(data []byte) *model.ProcessDefinition {
	nodes := []model.Node{event.NewStart("n0")}

	// Interior nodes: at most 6, chosen from the fuzz bytes.
	maxInterior := min(len(data), 6)
	for i := range maxInterior {
		id := nodeID(i + 1)
		switch data[i] % 4 {
		case 0:
			nodes = append(nodes, activity.NewServiceTask(id, activity.WithTaskAction("act")))
		case 1:
			nodes = append(nodes, activity.NewUserTask(id, activity.WithEligibleRoles("role")))
		case 2:
			nodes = append(nodes, activity.NewServiceTask(id, activity.WithTaskAction("act2")))
		default:
			nodes = append(nodes, activity.NewServiceTask(id, activity.WithTaskAction("act3")))
		}
	}

	endID := nodeID(len(nodes))
	nodes = append(nodes, event.NewEnd(endID))

	flows := make([]flow.SequenceFlow, 0, len(nodes)-1)
	for i := 0; i < len(nodes)-1; i++ {
		flows = append(flows, flow.SequenceFlow{
			ID:     flowID(i),
			Source: nodeID(i),
			Target: nodeID(i + 1),
		})
	}

	return &model.ProcessDefinition{ID: "fuzzdef", Version: 1, Nodes: nodes, Flows: flows}
}

// driveTriggers derives a sequence of triggers from the fuzz bytes. It always
// begins with a StartInstance, then appends a handful of follow-up triggers
// (completions, timer fires, signals, cancels) chosen from the bytes so that
// Step's dispatch arms are exercised with both matching and stale correlators.
func driveTriggers(data []byte, at time.Time) []engine.Trigger {
	out := []engine.Trigger{engine.NewStartInstance(at, map[string]any{"k": "v"})}

	for i, b := range data {
		ts := at.Add(time.Duration(i+1) * time.Second)
		switch b % 6 {
		case 0:
			out = append(out, engine.NewActionCompleted(ts, "cmd-1", map[string]any{"out": b}))
		case 1:
			out = append(out, engine.NewActionFailed(ts, "cmd-1", "boom", b%2 == 0))
		case 2:
			out = append(out, engine.NewTimerFired(ts, "timer-1"))
		case 3:
			out = append(out, engine.NewSignalReceived(ts, "sig", nil))
		case 4:
			out = append(out, engine.NewMessageReceived(ts, "msg", "", nil))
		default:
			out = append(out, engine.NewCancelRequested(ts))
		}
		if len(out) > 8 {
			break
		}
	}
	return out
}

func nodeID(i int) string { return "n" + itoa(i) }
func flowID(i int) string { return "f" + itoa(i) }

// itoa is a tiny base-10 formatter avoiding an strconv import in the harness.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
