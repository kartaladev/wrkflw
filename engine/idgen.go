package engine

import "fmt"

// IDGenerator mints the opaque identifiers the engine stamps on the objects it
// creates while stepping: tokens, human tasks, action commands, timers,
// incidents, and sub-process scopes.
//
// It is a SEAM, not a policy: the engine core is deterministic and
// wall-clock-free by design, so it never picks an id strategy of its own. With
// no generator supplied ([StepOptions.IDGenerator] nil) the engine falls back to its
// built-in per-instance counter ("<instance id>-t3", "<instance id>-h1", …),
// which keeps Step byte-for-byte reproducible for replay. The runtime/service
// layer injects a real generator (xid by default) so product instances carry
// opaque, globally-unique ids instead of instance-derived ones.
//
// The interface is deliberately shaped to match runtime/idgen.Generator, so a
// consumer passes that value straight through without an adapter — and the
// engine core keeps its zero-dependency layering (no runtime import).
//
// NewID returns an error so a rare entropy failure surfaces as a clean step
// error rather than a panic or a blank id; implementations must be safe for
// concurrent use.
type IDGenerator interface {
	NewID() (string, error)
}

// idSource is the per-Step id-generation seam carried on [InstanceState] so
// every mint site — most of which sit in helpers that cannot return an error —
// can reach the generator. It is unexported and transient: Step installs it on
// the working clone, checks err before returning, and scrubs it from the
// returned state, so it is never persisted and never observed by a caller.
type idSource struct {
	gen IDGenerator
	err error
}

// nextID mints the next id for a given object kind. It always advances seq (the
// per-kind counter is part of the instance's durable bookkeeping regardless of
// which strategy names the object) and then either delegates to the injected
// generator or falls back to the deterministic "<instance>-<prefix><seq>" form.
//
// A generator failure is recorded on the state and returned as an empty id;
// [Step] converts the recorded failure into the step's error, so no caller has
// to thread an error through the mint helpers.
func (s *InstanceState) nextID(prefix string, seq *int) string {
	*seq++
	if s.ids.gen == nil {
		return fmt.Sprintf("%s-%s%d", s.InstanceID, prefix, *seq)
	}
	id, err := s.ids.gen.NewID()
	if err != nil {
		if s.ids.err == nil {
			s.ids.err = fmt.Errorf("workflow-engine: generate %s id: %w", idKindName(prefix), err)
		}
		return ""
	}
	return id
}

// idKindName maps an id prefix to a human-readable object kind for error text.
func idKindName(prefix string) string {
	switch prefix {
	case "t":
		return "token"
	case "h":
		return "task"
	case "c":
		return "command"
	case "tm":
		return "timer"
	case "inc":
		return "incident"
	case "s":
		return "scope"
	default:
		return prefix
	}
}
