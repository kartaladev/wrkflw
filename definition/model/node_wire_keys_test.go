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

// syntheticTestKinds are the kinds THIS PACKAGE'S OWN test files register, which
// exist to exercise the registry mechanism rather than to carry a wire format.
// They are named by their declaring constant so the list cannot drift from the
// registrations: adding a synthetic kind without listing it here makes
// TestEveryRegisteredKindIsPinned fail, which is the direction that is safe.
var syntheticTestKinds = map[NodeKind]bool{
	kindSynthRecorded:   true, // foreign_node_type_test.go — has BOTH FromWire and ToWire
	kindSynthNoFromWire: true, // foreign_node_type_test.go
	NodeKind(9998):      true, // registry_test.go — ToWire only
}

// realSpecs returns every registered kind that carries a full wire spec and is
// not one of this package's own synthetics.
//
// It is deliberately driven by the REGISTRY, never by realKindReadSets. An
// earlier version filtered on the pinned table, which made the whole suite
// register-and-forget: an eighteenth kind with no pinned entry simply dropped
// out of every property — including TestNoWireOutputIsRefusedByTheKindGate, the
// proof this BREAKING change ships on — while the gate happily refused its legal
// keys and every test stayed green. Deriving the FIELD axis from the specs and
// leaving the KIND axis tabulated defeated the design's own justification.
//
// It keys off NodeSpec.Name rather than the nodeKindNames mirror: RegisterKind
// fills that mirror FROM s.Name, so reading the spec keeps the test coupled to
// the thing it is testing instead of to a derived index.
func realSpecs(t *testing.T) map[NodeKind]NodeSpec {
	t.Helper()
	requireRealKindsRegistered(t)
	out := map[NodeKind]NodeSpec{}
	for k, s := range nodeRegistry {
		if s.FromWire == nil || s.ToWire == nil || syntheticTestKinds[k] {
			continue
		}
		out[k] = s
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
// below: realSpecs is the registry, so this is what makes "every registered kind
// is pinned" an assertion rather than a filter. A kind registered without a
// realKindReadSets entry fails HERE, by name, instead of quietly leaving the
// domain of every other property in this file.
//
// 17 is the count at time of writing (7 activity + 6 event + 4 gateway), held as
// a floor — the same shape definition/kinds/nodetype_guard_test.go uses — so
// coverage going backwards is caught without a second exact number to keep in
// sync with the table.
func TestEveryRegisteredKindIsPinned(t *testing.T) {
	t.Parallel()

	specs := realSpecs(t)
	for _, s := range specs {
		assert.Contains(t, realKindReadSets, s.Name,
			"kind %q is registered with a full wire spec but has no pinned read set, so it sits outside every property in this file", s.Name)
	}
	assert.Len(t, realKindReadSets, len(specs), "a pinned kind is no longer registered")
	assert.GreaterOrEqual(t, len(specs), 17, "kind coverage went backwards")
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
// the node, projects it back through toWire, and puts THAT through the gate. It
// catches an UNCONDITIONAL ToWire emission outside the read set, which is the
// realistic shape of the failure.
//
// Be exact about what it does NOT reach, because an earlier version of this
// comment claimed more: widestLegalWire is built FROM the derived read set, so
// the domain here is image(FromWire), not every node a consumer can build. The
// shipped claim ranges wider than that — PublishDefinition marshals whatever
// *ProcessDefinition it is handed, which need not have come from a decode. That
// half is TestNoGoConstructedNodeIsRefusedByTheKindGate below; neither test
// alone establishes the claim.
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

// TestNoGoConstructedNodeIsRefusedByTheKindGate closes the half the property
// above cannot reach. The safety claim is "no definition produced by this
// library is affected", and PublishDefinition marshals any *ProcessDefinition a
// consumer hands it — a value built through the Go API, never decoded, whose
// field combinations FromWire may be unable to produce.
//
// So this approaches from the other side: build each kind's concrete node type
// with EVERY settable exported field non-zero — a node no decode could produce —
// project it through toWire, and gate that. Nothing here consults the derived
// read set, which is what makes it a real check on the claim rather than a
// restatement of the derivation.
func TestNoGoConstructedNodeIsRefusedByTheKindGate(t *testing.T) {
	t.Parallel()

	for kind, spec := range realSpecs(t) {
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			n, ok := widenedNode(kind)
			require.True(t, ok, "kind records no concrete type, so this property cannot run for it")

			w := toWire(n)
			w.ID, w.Kind, w.Name = "n1", kind, "N1"
			assert.NoError(t, checkNodeKeys(w, spec), "the gate refused a Go-constructed node this library would marshal")

			// The known-positive control. A widening that produced nothing would
			// make "zero refusals" vacuous, and it silently would for any kind
			// whose ToWire stopped writing. The four gateways are the deliberate
			// exception: their ToWire is a no-op and their read set is empty, so
			// zero keys is the correct answer there and any key would be a defect.
			keys := nonZeroGatedKeys(w)
			if len(realKindReadSets[spec.Name]) == 0 {
				assert.Zero(t, keys, "a kind that reads nothing must also write nothing")
				return
			}
			assert.Positive(t, keys, "the widener produced no gated key, so this case asserted nothing")
		})
	}
}

// widenNodeValue sets every settable exported field of v non-zero, recursively.
// Interface, func and channel fields are left zero: a ValidationStrategy has no
// synthesisable value, and a non-describable one never reaches the wire anyway
// (MarshalJSON fails closed on it first). Unexported fields are unreachable by
// construction, which is what leaves Base's id/name alone.
func widenNodeValue(v reflect.Value, depth int) {
	if depth > 4 || !v.CanSet() {
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("wrkflw-widen")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		widenNodeValue(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		widenNodeValue(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Struct:
		for i := range v.NumField() {
			widenNodeValue(v.Field(i), depth+1)
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		key := reflect.New(v.Type().Key()).Elem()
		widenNodeValue(key, depth+1)
		val := reflect.New(v.Type().Elem()).Elem()
		widenNodeValue(val, depth+1)
		m.SetMapIndex(key, val)
		v.Set(m)
	}
}

// widenedNode builds a node of the kind's registered concrete type with every
// settable exported field non-zero. nodeTypeFor is the same recording
// RegisterKind makes for the foreign-node gate, so this needs no per-kind
// knowledge of its own.
func widenedNode(kind NodeKind) (Node, bool) {
	rt, ok := nodeTypeFor(kind)
	if !ok {
		return nil, false
	}
	rv := reflect.New(rt).Elem()
	widenNodeValue(rv, 0)
	n, ok := rv.Interface().(Node)
	return n, ok
}

// nonZeroGatedKeys counts the gated keys a wire carries — what checkNodeKeys
// actually has to judge, as opposed to how many fields exist.
func nonZeroGatedKeys(w NodeWire) int {
	rv := reflect.ValueOf(w)
	n := 0
	for _, f := range nodeWireFields {
		if !rv.Field(f.Index[0]).IsZero() {
			n++
		}
	}
	return n
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
	subjects := map[string]struct {
		kind string
		// genericAlsoReveals marks an override that does NOT fix a blindness
		// present today: the generic probe reveals the field too, and the entry
		// exists to stop the read set DEPENDING on that. Asserting it in this
		// direction is what makes the dependency loud — the day the generic value
		// stops working, this row fails here instead of the field silently
		// leaving the read set and every stored definition of that kind failing
		// to load.
		genericAlsoReveals bool
	}{
		"EndBehavior":        {kind: "endEvent"},
		"TerminationReason":  {kind: "endEvent"},
		"TerminationOutcome": {kind: "endEvent"},
		"ErrorCode":          {kind: "endEvent"},
		"TimerTrigger":       {kind: "intermediateCatchEvent"},
		"DefRef":             {kind: "callActivity", genericAlsoReveals: true},
	}
	for name := range closedVocabularyProbes {
		require.Contains(t, subjects, name, "a new probe override has no kind pinning it")
	}

	for name, subject := range subjects {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, ok := fields[name]
			require.True(t, ok, "probe names a NodeWire field that no longer exists")
			spec, ok := byName[subject.kind]
			require.True(t, ok)

			// NECESSARY, or — for DefRef — the accident this entry insures
			// against. The value a generic probe would have used is a zero-valued
			// allocation for a pointer field (a *TriggerWire with an empty Kind,
			// exactly the blind spot) and the probe string otherwise.
			var bare, naive NodeWire
			generic := reflect.ValueOf(probeString)
			if f.Type.Kind() == reflect.Pointer {
				generic = reflect.New(f.Type.Elem())
			}
			reflect.ValueOf(&naive).Elem().FieldByIndex(f.Index).Set(generic)
			if subject.genericAlsoReveals {
				assert.NotEqual(t, spec.FromWire(Base{}, bare), spec.FromWire(Base{}, naive),
					"the generic probe no longer reveals this field; the read set no longer depends on that accident, so drop genericAlsoReveals — but check first that nothing else depended on it")
			} else {
				assert.Equal(t, spec.FromWire(Base{}, bare), spec.FromWire(Base{}, naive),
					"the generic probe already reveals this field; the override is dead weight")
			}

			// SUFFICIENT, for every entry: the override, through the same
			// construction the gate itself uses, must reveal the field.
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
	}
}

// TestEveryNodeWireFieldHasAWireKey guards the OTHER hole nodeWireFields opens,
// and it has to read the reflect type rather than nodeWireFields: the builder
// SKIPS a field with no json tag or with `json:"-"`, so an assertion over the
// resulting slice can never fire. That was the previous version of this check,
// and it was vacuous.
//
// The direction matters. encoding/json encodes an untagged exported field under
// its Go field name, so such a field is authorable, decodes, and — being absent
// from nodeWireFields — is never gated. That is precisely the silent discard
// this PR exists to close, reintroduced by omission.
func TestEveryNodeWireFieldHasAWireKey(t *testing.T) {
	t.Parallel()

	rt := reflect.TypeOf(NodeWire{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		key := jsonKey(f)
		assert.NotEmpty(t, key, "NodeWire.%s has no json tag, so it is authorable under its Go field name and ungated", f.Name)
		assert.NotEqual(t, "-", key, "NodeWire.%s is json:\"-\" and therefore ungated", f.Name)
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

// TestZeroValuedMisplacedKeyIsAsymmetric writes the documented limit into a test
// as well as into the source, INCLUDING the half that is easy to state wrongly.
// The gate reads a wire STRUCT via reflect.IsZero, so a scalar zero is
// indistinguishable from an absent key and is accepted; an empty composite is
// not a Go zero value and is refused.
//
// An earlier version pinned only a string and a bool, which could not tell the
// two readings of "zero value" apart — and two careful readers of the same
// sentence split on exactly that. The composite rows below are what make the
// wording checkable rather than merely agreeable.
func TestZeroValuedMisplacedKeyIsAsymmetric(t *testing.T) {
	t.Parallel()

	spec, ok := specFor(KindExclusiveGateway)
	requireRealKindsRegistered(t)
	require.True(t, ok)

	type testCase struct {
		name   string
		wire   NodeWire
		assert func(t *testing.T, err error)
	}
	accepted := func(t *testing.T, err error) {
		t.Helper()
		assert.NoError(t, err, "a scalar zero carries no information, so dropping it discards nothing")
	}
	refused := func(t *testing.T, err error) {
		t.Helper()
		assert.ErrorIs(t, err, ErrKeyNotOnKind)
	}

	cases := []testCase{
		{name: "empty string", wire: NodeWire{Action: ""}, assert: accepted},
		{name: "false bool", wire: NodeWire{Manual: false}, assert: accepted},
		{name: "nil pointer", wire: NodeWire{Rule: nil}, assert: accepted},
		{name: "non-zero string", wire: NodeWire{Action: "charge"}, assert: refused},
		// The asymmetric half. Neither is a Go zero value, so both are refused
		// even though an author would read them as "nothing here". It fails
		// closed, and omitempty means ToWire can never emit either.
		{name: "empty slice", wire: NodeWire{Outcomes: []string{}}, assert: refused},
		{name: "pointer to zero struct", wire: NodeWire{RetryPolicy: &RetryPolicy{}}, assert: refused},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.wire
			w.ID, w.Kind = "g", KindExclusiveGateway
			tc.assert(t, checkNodeKeys(w, spec))
		})
	}
}
