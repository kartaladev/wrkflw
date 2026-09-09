package model

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is INTERNAL (package model) because the properties it pins are
// about toWire and the derived read set, neither of which is exported. It
// cannot import definition/kinds — that would be an import cycle — but it does
// not need to: the external test files in this same binary blank-import it, so
// all seventeen real kinds are registered by the time these run.
//
// requireRealKindsRegistered is the known-positive control for that. Without it
// a run in which registration never happened would iterate an almost-empty
// registry and report a green over nothing at all.

func requireRealKindsRegistered(t *testing.T) {
	t.Helper()
	require.GreaterOrEqual(t, len(nodeRegistry), len(realKindReadSets),
		"the real kinds are not registered in this test binary; the properties below would pass over nothing")
}

// realSpecs returns the registered specs excluding the synthetic kinds that
// registry_test.go and foreign_node_type_test.go install, keyed by kind.
//
// It keys off NodeSpec.Name rather than the nodeKindNames mirror: RegisterKind
// fills that mirror FROM s.Name, so reading the spec keeps the test coupled to
// the thing it is testing instead of to a derived index.
func realSpecs(t *testing.T) map[NodeKind]NodeSpec {
	t.Helper()
	requireRealKindsRegistered(t)
	out := map[NodeKind]NodeSpec{}
	for k, s := range nodeRegistry {
		if s.FromWire == nil || s.ToWire == nil {
			continue // synthetic half-specs; not a wire-carrying kind
		}
		if _, real := realKindReadSets[s.Name]; real {
			out[k] = s
		}
	}
	return out
}

// realKindReadSets pins the derived table for all seventeen kinds. It is NOT the
// gate's source of truth — the gate derives its own from each spec at run time —
// it is the drift alarm: a change to any leaf FromWire that adds or removes a
// key it reads shows up here as a diff, in the PR that made it, instead of
// silently widening or narrowing what the wire accepts.
//
// The 0-key gateways are the point of the whole issue: an exclusiveGateway reads
// nothing, so every key on one is a silent drop. And `rule` appears against
// exactly one kind, businessRuleTask — the #47 promise ("cannot be published in
// this release") held only there before this gate, because a rule on any other
// kind was discarded before Validate could refuse it.
var realKindReadSets = map[string][]string{
	"boundaryEvent":          {"attached_to", "boundary_action", "boundary_error_expr", "correlation_key", "error_code", "message_name", "non_interrupting", "signal_name", "timer_duration", "timer_trigger"},
	"businessRuleTask":       {"action", "cancel_action", "compensate_action", "completion_action", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "recovery_flow", "retry_policy", "rule", "wait_action", "wait_every", "wait_trigger"},
	"callActivity":           {"cancel_action", "compensate_action", "completion_action", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "def_ref", "recovery_flow", "retry_policy", "wait_action", "wait_every", "wait_trigger"},
	"compensationThrowEvent": {"compensate_ref", "compensate_scope_local"},
	"endEvent":               {"end_behavior", "error_code", "termination_outcome", "termination_reason"},
	"eventBasedGateway":      {},
	"exclusiveGateway":       {},
	"inclusiveGateway":       {},
	"intermediateCatchEvent": {"correlation_key", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "message_name", "signal_name", "timer_duration", "timer_trigger", "validation", "wait_action", "wait_every", "wait_trigger"},
	"intermediateThrowEvent": {"signal_name"},
	"parallelGateway":        {},
	"receiveTask":            {"cancel_action", "compensate_action", "completion_action", "correlation_key", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "message_name", "recovery_flow", "retry_policy", "validation", "wait_action", "wait_every", "wait_trigger"},
	"sendTask":               {"cancel_action", "compensate_action", "completion_action", "correlation_key", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "message_name", "recovery_flow", "retry_policy", "wait_action", "wait_every", "wait_trigger"},
	"serviceTask":            {"action", "cancel_action", "compensate_action", "completion_action", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "recovery_flow", "retry_policy", "wait_action", "wait_every", "wait_trigger"},
	"startEvent":             {"correlation_key", "message_name", "message_start_singleton", "non_interrupting", "signal_name", "timer_duration", "timer_trigger", "validation"},
	"subProcess":             {"cancel_action", "compensate_action", "completion_action", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "recovery_flow", "retry_policy", "subprocess", "wait_action", "wait_every", "wait_trigger"},
	"userTask":               {"cancel_action", "compensate_action", "completion_action", "deadline_action", "deadline_duration", "deadline_flow", "deadline_trigger", "eligible_expr", "eligible_privileges", "eligible_roles", "expose_outcome", "manual", "manual_immediate", "outcome_variable", "outcomes", "recovery_flow", "retry_policy", "validation", "wait_action", "wait_every", "wait_trigger"},
}

// TestDerivedReadSetsCoverEveryRegisteredKind is the control for every property
// below: they all iterate realSpecs, which is registered ∩ pinned, so a pinned
// kind that stopped being registered would silently shrink the domain instead of
// failing. 17 is the count at time of writing (7 activity + 6 event + 4 gateway),
// the same floor definition/kinds/nodetype_guard_test.go holds.
func TestDerivedReadSetsCoverEveryRegisteredKind(t *testing.T) {
	t.Parallel()

	assert.Len(t, realKindReadSets, 17, "the pinned table must cover every real kind")
	assert.Len(t, realSpecs(t), len(realKindReadSets), "a pinned kind is no longer registered")
}

func TestDerivedReadSetMatchesThePinnedTable(t *testing.T) {
	t.Parallel()

	for _, spec := range realSpecs(t) {
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()
			// ElementsMatch, not Equal: an empty read set derives as a nil slice
			// while the table writes it as {}, and the pinned order is for the
			// reader rather than part of the property.
			assert.ElementsMatch(t, realKindReadSets[spec.Name], slices.Sorted(maps.Keys(deriveKeysReadBy(spec))))
		})
	}
}

// TestNoWireOutputIsRefusedByTheKindGate is the safety half of the change, and
// the reason it can ship without a migration: nothing this library WRITES can be
// refused by what it now reads.
//
// For each kind it builds the widest legal wire that kind accepts, reconstructs
// the node, projects it back through toWire, and puts THAT through the gate. The
// assertion is on toWire's output, which the derivation never consulted — so a
// ToWire that emits a key its own FromWire ignores fails here rather than in a
// consumer's stored definition.
func TestNoWireOutputIsRefusedByTheKindGate(t *testing.T) {
	t.Parallel()

	for kind, spec := range realSpecs(t) {
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			w := widestLegalWire(spec)
			w.ID, w.Name, w.Kind = "n1", "N1", kind

			require.NoError(t, checkNodeKeys(w, spec), "the widest legal wire must itself be accepted")

			back := toWire(spec.FromWire(Base{id: w.ID, name: w.Name}, w))
			back.ID, back.Kind, back.Name = w.ID, w.Kind, w.Name
			require.NoError(t, checkNodeKeys(back, spec), "the gate refused a wire this library produced")

			// And it is a FIXED POINT: re-decoding and re-encoding changes nothing,
			// so a stored definition survives any number of round trips.
			again := toWire(spec.FromWire(Base{id: w.ID, name: w.Name}, back))
			again.ID, again.Kind, again.Name = w.ID, w.Kind, w.Name
			assert.Equal(t, back, again, "toWire(fromWire(toWire(n))) != toWire(n)")
		})
	}
}

// widestLegalWire sets every key the spec reads, using the same probe values the
// derivation uses, so the resulting wire is the largest one the gate accepts for
// that kind.
//
// Companions are deliberately NOT applied here. A companion sets a key the field
// is read alongside, and if the kind reads that key too it is set by its own
// probe anyway; if it does not — a boundaryEvent reads error_code but not
// end_behavior — applying it would put a key OUTSIDE the read set into a wire
// this test then asserts is legal. The require.NoError on checkNodeKeys at the
// call site is what holds that reasoning to account.
func widestLegalWire(s NodeSpec) NodeWire {
	read := deriveKeysReadBy(s)
	var w NodeWire
	for _, f := range nodeWireFields {
		v, ok := probeValue(f)
		if !read[f.key] || !ok {
			continue
		}
		reflect.ValueOf(&w).Elem().FieldByIndex(f.Index).Set(v)
	}
	return w
}

// TestGoldenDefinitionSurvivesTheKindGate widens the proof from the synthesised
// wires above to the repo's real corpus: twenty nodes written by this library.
//
// definition/kinds/kinds_test.go's TestGoldenRoundTrip already decodes the same
// file and would fail if the gate refused a node in it. This asserts the
// property directly rather than as that test's side effect, because "no stored
// definition is refused" is the claim the change ships on and it should fail
// here, by name, rather than as a byte-comparison mismatch in another package.
func TestGoldenDefinitionSurvivesTheKindGate(t *testing.T) {
	t.Parallel()
	requireRealKindsRegistered(t)

	raw, err := os.ReadFile("../testdata/golden_definition.json")
	require.NoError(t, err)

	var def ProcessDefinition
	require.NoError(t, json.Unmarshal(raw, &def))
	require.NotEmpty(t, def.Nodes, "golden corpus must not be empty")

	for _, n := range def.Nodes {
		s, ok := specFor(n.Kind())
		require.True(t, ok)
		require.NoError(t, checkNodeKeys(toWire(n), s), "node %q", n.ID())
	}
}

// TestClosedVocabularyProbesStayValid pins the one hand-maintained table in the
// design. Each entry must be BOTH necessary and sufficient: the generic probe
// must fail to reveal the field (otherwise the entry is dead weight) and the
// override must reveal it (otherwise the vocabulary moved under us and the gate
// would start refusing a legal key).
//
// It covers BOTH shapes of override, which is the point: four entries live in
// closedVocabularyProbes and a fifth, for *TriggerWire, lives inside probeValue
// as a type-level branch. A test that iterated only the map would leave the
// TriggerWire vocabulary — nine kinds in ReadTrigger's switch — unpinned.
func TestClosedVocabularyProbesStayValid(t *testing.T) {
	t.Parallel()

	fields := map[string]gatedField{}
	for _, f := range nodeWireFields {
		fields[f.Name] = f
	}
	byName := map[string]NodeSpec{}
	for _, s := range realSpecs(t) {
		byName[s.Name] = s
	}

	// Each override is pinned against a kind that actually reads the field.
	subjects := map[string]string{
		"EndBehavior":        "endEvent",
		"TerminationReason":  "endEvent",
		"TerminationOutcome": "endEvent",
		"ErrorCode":          "endEvent",
		"TimerTrigger":       "intermediateCatchEvent",
	}
	for name := range closedVocabularyProbes {
		require.Contains(t, subjects, name, "a new probe override has no kind pinning it")
	}

	for name, kindName := range subjects {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, ok := fields[name]
			require.True(t, ok, "probe names a NodeWire field that no longer exists")
			spec, ok := byName[kindName]
			require.True(t, ok)

			// NECESSARY: the value a generic probe would have used must NOT reveal
			// the field, or the override is dead weight. For a pointer field that
			// generic value is a zero-valued allocation — a *TriggerWire with an
			// empty Kind, which is exactly the blind spot.
			var bare, naive NodeWire
			generic := reflect.ValueOf(probeString)
			if f.Type.Kind() == reflect.Pointer {
				generic = reflect.New(f.Type.Elem())
			}
			reflect.ValueOf(&naive).Elem().FieldByIndex(f.Index).Set(generic)
			assert.Equal(t, spec.FromWire(Base{}, bare), spec.FromWire(Base{}, naive),
				"the generic probe already reveals this field; the override is dead weight")

			// SUFFICIENT: the override, through the same construction the gate
			// itself uses, must reveal it.
			base, probe, ok := probeWire(f)
			require.True(t, ok)
			assert.NotEqual(t, spec.FromWire(Base{}, base), spec.FromWire(Base{}, probe),
				"the override no longer reveals this field; its value has fallen out of the vocabulary")
		})
	}
}

// TestEveryNodeWireFieldIsProbeable guards the one place the gate deliberately
// fails OPEN: a field whose type probeValue cannot synthesise is treated as read
// by every kind, so it is never refused. No field is in that state today, and
// this fails if one is added.
func TestEveryNodeWireFieldIsProbeable(t *testing.T) {
	t.Parallel()

	for _, f := range nodeWireFields {
		_, ok := probeValue(f)
		assert.True(t, ok, "NodeWire.%s has no probe value, so the gate cannot see keys on it", f.Name)
		assert.NotEmpty(t, f.key, "NodeWire.%s has no json tag", f.Name)
	}
}

// TestFromWireIsDeterministic underwrites the derivation itself. It compares two
// reconstructions with reflect.DeepEqual, so a FromWire returning something
// DeepEqual cannot match against itself — a non-nil func field, say — would make
// every field look read and the gate would silently accept everything for that
// kind.
func TestFromWireIsDeterministic(t *testing.T) {
	t.Parallel()

	for _, spec := range realSpecs(t) {
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, spec.FromWire(Base{}, NodeWire{}), spec.FromWire(Base{}, NodeWire{}),
				"FromWire is not DeepEqual-stable, so the read-set derivation is meaningless for this kind")
		})
	}
}

// TestZeroValuedMisplacedKeyIsAccepted writes the documented limit into a test
// as well as into the source: the gate reads a wire STRUCT, where an absent key
// and a key present with its zero value are the same thing. It is the same
// reasoning NodeWire.Rule already carries for a literal `"rule":null` — there is
// nothing in it to lose.
func TestZeroValuedMisplacedKeyIsAccepted(t *testing.T) {
	t.Parallel()

	spec, ok := specFor(KindExclusiveGateway)
	requireRealKindsRegistered(t)
	require.True(t, ok)

	zero := NodeWire{ID: "g", Kind: KindExclusiveGateway, Action: "", Manual: false}
	assert.NoError(t, checkNodeKeys(zero, spec), "a zero-valued misplaced key carries no information")

	nonZero := NodeWire{ID: "g", Kind: KindExclusiveGateway, Action: "charge"}
	assert.ErrorIs(t, checkNodeKeys(nonZero, spec), ErrKeyNotOnKind, "a non-zero misplaced key is refused")
}
