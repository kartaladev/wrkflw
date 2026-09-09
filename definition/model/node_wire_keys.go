package model

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// This file holds the kind-key gate: the decode-side refusal of a wire key that
// the node's kind never reads.
//
// NodeWire is a FLAT UNION over every node kind, so `action` on an
// exclusiveGateway, `rule` on a serviceTask and a `validation:` block on a
// serviceTask are all KNOWN fields. Strict decoding (DisallowUnknownFields /
// KnownFields(true)) therefore cannot see them: they decode, the kind's FromWire
// never looks at them, and they vanish without a word. The author gets no error
// and no effect.
//
// WHY THE DECODER AND NOT Validate. The comment this change replaces
// (strict_decoding_test.go) held that kind-appropriateness was model.Validate's
// concern. It structurally cannot be: Validate receives []Node, and by the time
// it runs the misplaced key has already been dropped by fromWire. The decoder is
// the only place where the original NodeWire and the reconstructed node coexist,
// so it is the only place the drop is observable.
//
// WHY THE READ SET IS DERIVED AND NOT WRITTEN DOWN. Seventeen registered kinds
// times ~forty NodeWire fields is a table nobody can keep true; it would pass
// green while drifting from the specs it mirrors. Instead each kind's read set
// is measured FROM ITS OWN SPEC, once, by probing: set one field, reconstruct,
// and see whether the node changed. A field that changes the node is read; one
// that changes nothing is dropped, and dropping it silently is the defect.
//
// Probing by observed EFFECT, rather than by comparing the wire round trip field
// by field, is what makes the legacy flat forms survive. `timer_duration` on an
// intermediateCatchEvent does not reappear in ToWire's output — ReadTrigger
// normalises it into the canonical nested `timer_trigger` — so a field-equality
// round trip would call it lost and refuse every legacy definition. It is
// RELOCATED, not lost, and the probe sees the relocation because the
// reconstructed node differs.
//
// THE DOCUMENTED LIMIT, AND IT IS ASYMMETRIC. The gate tests the decoded wire
// STRUCT with reflect.IsZero, so a key present with a SCALAR zero —
// `"action":""`, `"manual":false`, `"rule":null` — is indistinguishable from an
// absent key and is accepted and dropped. An EMPTY COMPOSITE is not:
// `"outcomes":[]` decodes to a non-nil empty slice and `"retry_policy":{}` to a
// non-nil pointer, neither of which is a Go zero value, so both ARE refused.
//
// Name the asymmetry rather than round it off. "A zero-valued key is accepted"
// invites the AUTHOR-INTENT reading, under which `[]` carries exactly as little
// information as `""` and should behave the same way — and it does not. Two
// careful readers of an earlier wording of this paragraph reached opposite
// conclusions about it, which is why the word "scalar" is load-bearing here.
//
// The accepted half is the reasoning this package already carries for
// `"rule":null` (see NodeWire.Rule: a literal null "loses nothing: there is no
// rule in it to lose"). The refused half fails CLOSED, and costs nothing this
// library writes: omitempty drops a nil slice and a nil pointer, so ToWire never
// emits `[]` or `{}` in the first place. The exposure is hand-authored input and
// templated emitters only.
//
// OUT OF SCOPE, deliberately, and tracked separately: a key that is legal on the
// kind but dropped in the wrong COMBINATION (`error_code` without
// `end_behavior:"error"`), and an invalid VALUE inside a closed vocabulary
// (`end_behavior:"probeval"`). Both are intra-kind consistency checks that
// Validate can see, because the reconstructed node carries the inconsistency.
// This gate is kind-level only.

// keyProbe overrides the generic probe for a field whose meaning is not carried
// by an arbitrary non-zero value.
//
// It exists because probing with one arbitrary value per field type is blind to
// a CLOSED VOCABULARY: `end_behavior:"probeval"` matches no case in EndEvent's
// switch, changes nothing, and would make the derivation conclude that an
// endEvent reads no fields at all — after which the gate would refuse a
// perfectly legal `end_behavior: terminate`. The same blindness applies to a
// CONDITIONAL field, one read only inside a branch some other key selects.
//
// This is a hand-maintained table and therefore the one drift risk in the
// design. It is kept to the smallest shape that removes the blindness — FIVE
// field entries here, plus ONE type-level entry for *TriggerWire in probeValue
// (a zero TriggerWire.Kind matches no arm of ReadTrigger's switch, the same
// blindness one level down) — rather than a kind-by-field matrix. Be exact
// about the count: it is six overrides in two shapes, not five in one, and
// TestClosedVocabularyProbesStayValid pins BOTH shapes, failing if any probe
// value falls out of its vocabulary or if the generic probe would have done.
//
// The two shapes are two different blindnesses and the distinction is worth
// keeping: `value` is VALUE blindness, a property of the field's domain, while
// `companion` is BRANCH blindness — the field is read only under an arm that
// some other key selects, which the derivation cannot see because it probes
// each field independently.
type keyProbe struct {
	// value replaces the generic probe value for this field. A nil value keeps
	// the generic one.
	value any
	// companion sets the other keys this field is only read alongside. It is
	// applied to BOTH the baseline and the probe wire, so what the comparison
	// isolates is still this field alone.
	companion func(w *NodeWire)
}

// closedVocabularyProbes carries the probe overrides, keyed by NodeWire field
// name. Four trace to definition/event/event.go's EndEvent.FromWire, the only
// leaf spec that reads a field conditionally or by an inline vocabulary; the
// fifth traces to a PARSER, which is a vocabulary spelled differently.
var closedVocabularyProbes = map[string]keyProbe{
	// "terminate" and "error" are the whole vocabulary; anything else falls
	// through the switch and means "a normal end".
	"EndBehavior":        {value: "terminate"},
	"TerminationReason":  {companion: endTerminate},
	"TerminationOutcome": {value: "abort", companion: endTerminate},
	"ErrorCode":          {companion: endError},
	// DefRef is listed even though the GENERIC probe currently reveals it, which
	// makes this the one entry that removes a LATENT dependency rather than a
	// present blindness. callActivity.FromWire reads def_ref through
	// ParseQualifier (definition/activity/activity.go: parseOrZero), and a bare
	// string with no colon parses as an unpinned id — so today the read set holds
	// def_ref only because "wrkflw-key-probe" happens to be a legal qualifier.
	// Tighten that parser (require an explicit version, add a charset rule) and
	// def_ref would silently leave callActivity's read set, at which point every
	// stored callActivity definition stops loading. An explicitly legal qualifier
	// deletes the dependency on that accident, and
	// TestClosedVocabularyProbesStayValid pins the accident itself so a tightened
	// parser fails there loudly instead of here silently.
	"DefRef": {value: "other-process:1"},
}

func endTerminate(w *NodeWire) { w.EndBehavior = "terminate" }
func endError(w *NodeWire)     { w.EndBehavior = "error" }

// probeTrigger is the probe value for every *TriggerWire field. A zero
// TriggerWire has an empty Kind, which matches no case in ReadTrigger's switch
// and decodes to an unset TriggerSpec — the same closed-vocabulary blindness as
// EndBehavior, one level down. "expr" is a real trigger kind (see kindString).
func probeTrigger() *TriggerWire { return &TriggerWire{Kind: "expr", Expr: "PT1H"} }

var triggerWireType = reflect.TypeOf(TriggerWire{})

const probeString = "wrkflw-key-probe"

// gatedField pairs a NodeWire field with the wire key it is authored under. The
// key is resolved once, here, rather than re-parsed from the struct tag on every
// decoded node.
type gatedField struct {
	reflect.StructField

	// key is the field's json tag minus its options. nodeYAML mirrors these
	// strings in its yaml tags, so one name serves both decoders' diagnostics.
	key string
	// probe is the field's entry in closedVocabularyProbes, resolved once here so
	// neither probeValue nor probeWire has to look the table up again. nil for
	// the great majority of fields, which the generic probe covers.
	probe *keyProbe
}

// nodeWireFields is the decode-gate's view of NodeWire: every field except the
// three that are not kind-specific. ID and Name reach every node through Base
// and Kind is the discriminator itself, so none of them is a key a kind can
// fail to carry.
var nodeWireFields = func() []gatedField {
	t := reflect.TypeOf(NodeWire{})
	out := make([]gatedField, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		switch f.Name {
		case "ID", "Kind", "Name":
			continue
		}
		// A field tagged `json:"-"` or with no tag at all has no wire key, so
		// there is no key for a kind to fail to carry. None exists today.
		key := jsonKey(f)
		if key == "" || key == "-" {
			continue
		}
		gf := gatedField{StructField: f, key: key}
		if p, ok := closedVocabularyProbes[f.Name]; ok {
			gf.probe = &p
		}
		out = append(out, gf)
	}
	return out
}()

// jsonKey is the wire key for a struct field, i.e. its json tag minus options.
func jsonKey(f reflect.StructField) string {
	key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return key
}

// probeValue returns the value to set a field to when measuring whether its
// kind reads it, and whether the field is probeable at all.
func probeValue(f gatedField) (reflect.Value, bool) {
	if f.probe != nil && f.probe.value != nil {
		return reflect.ValueOf(f.probe.value), true
	}
	switch f.Type.Kind() {
	case reflect.String:
		return reflect.ValueOf(probeString), true
	case reflect.Bool:
		return reflect.ValueOf(true), true
	case reflect.Slice:
		if f.Type.Elem().Kind() != reflect.String {
			return reflect.Value{}, false
		}
		s := reflect.MakeSlice(f.Type, 1, 1)
		s.Index(0).SetString(probeString)
		return s, true
	case reflect.Pointer:
		if f.Type.Elem() == triggerWireType {
			return reflect.ValueOf(probeTrigger()), true
		}
		return reflect.New(f.Type.Elem()), true
	default:
		return reflect.Value{}, false
	}
}

// refusedField is one field a kind never reads, reduced to what the gate needs
// on the decode path: the index to test on the wire and the key to name in the
// diagnostic. Storing the REFUSALS rather than the read set is what keeps the
// per-node cost proportional to what is wrong rather than to NodeWire's width —
// there is no map to hash and no field to walk past for a key the kind does read.
type refusedField struct {
	index int
	key   string
}

// kindRefusals caches the refusal list per kind. It is a sync.Map rather than a
// mutex-guarded map because the table is written once per kind and read on every
// decoded node: a plain mutex serialises concurrent decodes of unrelated kinds
// behind one lock, and would hold that lock across arbitrary leaf FromWire code
// while the first decoder of a kind derives its set.
//
// Deriving twice under a race is harmless — the derivation is pure and both
// results are equal — so this stores rather than LoadOrStores a winner.
var kindRefusals sync.Map // NodeKind -> []refusedField

// refusalsFor returns the keys k refuses, deriving them from the spec on first
// use. Registration happens in leaf init functions, so deriving lazily rather
// than at init keeps this file independent of package initialisation order —
// and, critically, keeps every probe call OUT of RegisterKind, which already
// calls FromWire once and would take down every binary importing the leaf if a
// probe made one panic.
func refusalsFor(k NodeKind, s NodeSpec) []refusedField {
	if got, ok := kindRefusals.Load(k); ok {
		return got.([]refusedField)
	}
	read := deriveKeysReadBy(s)
	var out []refusedField
	for _, f := range nodeWireFields {
		if !read[f.key] {
			out = append(out, refusedField{index: f.Index[0], key: f.key})
		}
	}
	kindRefusals.Store(k, out)
	return out
}

// probeWire builds the pair of wires that isolate one field: a baseline
// carrying only the field's companion context, and the same wire with the field
// itself set. Reconstructing both and comparing is what decides whether the kind
// reads the field. It reports false for a field type no probe value exists for.
//
// It is one function rather than an inlined sequence because the test that pins
// closedVocabularyProbes must exercise the same construction the gate uses — a
// pin built from its own copy of the sequence would keep passing after the
// production one changed.
func probeWire(f gatedField) (base, probe NodeWire, ok bool) {
	v, ok := probeValue(f)
	if !ok {
		return base, probe, false
	}
	if f.probe != nil && f.probe.companion != nil {
		f.probe.companion(&base)
	}
	probe = base
	reflect.ValueOf(&probe).Elem().FieldByIndex(f.Index).Set(v)
	return base, probe, true
}

// deriveKeysReadBy measures the read set of one spec: for each field,
// reconstruct the baseline and the probe, and record the field when the two
// nodes differ.
func deriveKeysReadBy(s NodeSpec) map[string]bool {
	read := make(map[string]bool, len(nodeWireFields))
	// The baseline is the zero reconstruction for every field without a
	// companion, which is all but four of them, so it is built once here rather
	// than re-derived per field.
	zeroNode := s.FromWire(Base{}, NodeWire{})
	for _, f := range nodeWireFields {
		base, probe, ok := probeWire(f)
		if !ok {
			// An unprobeable field type cannot be measured, so it is treated as
			// read: the gate stays silent rather than refusing a key it failed to
			// understand. No NodeWire field is in this state today and
			// TestEveryNodeWireFieldIsProbeable fails if one appears.
			read[f.key] = true
			continue
		}
		baseNode := zeroNode
		if f.probe != nil && f.probe.companion != nil {
			baseNode = s.FromWire(Base{}, base)
		}
		if !reflect.DeepEqual(baseNode, s.FromWire(Base{}, probe)) {
			read[f.key] = true
		}
	}
	return read
}

// checkNodeKeys refuses w if it carries a non-zero key that its kind never
// reads. It reports the FIRST such key in NodeWire field order, so the
// diagnostic is stable across runs rather than dependent on map iteration.
//
// This runs on every decoded node, including the durable reload path
// (internal/persistence/store/definitions.go unmarshals a stored definition), so
// it walks a precomputed per-kind list and does no string hashing.
func checkNodeKeys(w NodeWire, s NodeSpec) error {
	rv := reflect.ValueOf(w)
	for _, r := range refusalsFor(w.Kind, s) {
		if rv.Field(r.index).IsZero() {
			continue
		}
		return fmt.Errorf("%w: node %q declares kind %q, which does not carry key %q",
			ErrKeyNotOnKind, w.ID, w.Kind, r.key)
	}
	return nil
}
