package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuleSpec is the reserved rule-engine reference carried by a businessRuleTask's
// `rule` key. It holds EITHER a catalog name — the same shape as `action`, so a
// consumer learns one rule, "names into catalogs" — OR an inline rule document
// carried verbatim for the rule engine to compile.
//
// Nothing in wrkflw interprets either form. The key exists now so that the
// rule-engine adapter can land after the first release without a
// persisted-format change: it round-trips through JSON, YAML and the golden
// file, and Validate refuses any non-empty rule until the adapter exists (see
// ErrRuleNotSupported). That refusal is also what keeps this shape revisable —
// no published definition can be carrying one yet.
//
// Exactly one of the two fields is ever set; a RuleSpec with neither is not a
// valid authored value and is refused by both codecs (see IsZero).
type RuleSpec struct {
	// Name is the rule-catalog name, e.g. "pricing.v3". Set when the key was
	// authored as a string.
	Name string
	// Inline is the rule document, held as JSON. Set when the key was authored as
	// an object. wrkflw never parses its contents.
	//
	// What "as authored" means differs by FORMAT, and the difference is measured,
	// not assumed. An earlier version of this comment claimed key order was the
	// only casualty and the CONTENT was untouched. That was false, so the limit is
	// written out here instead:
	//
	//   - JSON: byte-preserved apart from framing. encoding/json compacts whatever
	//     MarshalJSON returns and HTML-escapes < > &. Nothing is added or dropped.
	//   - YAML: CONVERTED, not copied. The mapping is decoded into map[string]any
	//     and re-encoded as JSON, so yaml.v3's scalar resolution is applied on the
	//     way through. Every consequence listed here has a row in
	//     TestRuleSpecYAMLInlineConversionIsLossy, and only those do: keys are
	//     reordered (JSON canonical order); a null key (`~: v`) is DROPPED
	//     ENTIRELY; an integer too wide for float64 loses precision (a 30-digit
	//     literal becomes 1.23e+29); a date becomes an RFC3339 string; !!binary is
	//     base64-decoded to raw bytes; `0o17` becomes 15; and a non-string key is
	//     stringified (`1:` becomes "1"). The list is not exhaustive — it is the
	//     part that is pinned, which is the part that cannot silently stop being
	//     true.
	//
	// So a YAML-authored rule is NOT byte-recoverable, and for the dropped-null-key
	// and wide-integer cases not information-preserving either. wrkflw cannot warn
	// about it, because it never interprets the document: the rule engine will
	// receive what is recorded here, not what was typed. A consumer needing byte
	// fidelity should author the document in JSON, or name it with WithRule and keep
	// it out of the definition entirely.
	//
	// This is a documented LIMIT, not a defect deferred. Changing yaml.v3's scalar
	// resolution is out of scope and would itself be a wire-format change; the
	// honest move is to say what the conversion does. Pinned by
	// TestRuleSpecYAMLInlineConversionIsLossy.
	Inline json.RawMessage
}

// IsZero reports whether the spec carries no rule at all — the state of a
// businessRuleTask authored without the key, which is the only state Validate
// currently accepts.
//
// IsZero is NOT the well-formedness test. It says nothing about whether Inline is
// a JSON object, so a spec can be non-zero and still un-encodable; shapeErr is the
// predicate for that, and Validate uses shapeErr. Discriminating on IsZero alone
// is precisely the defect two reviews measured independently.
func (r RuleSpec) IsZero() bool { return r.Name == "" && len(r.Inline) == 0 }

// shapeErr reports whether the spec is one of the two shapes a rule may take — a
// non-blank catalog NAME, or an INLINE document that is a valid JSON object — and
// returns an ErrInvalidRule naming the defect otherwise. Exactly one of the two,
// never both and never neither.
//
// It is the SINGLE domain, and that is the point. Both decoders run it, so no
// decoded spec can violate it; MarshalJSON runs it, so an ill-formed spec is
// refused rather than emitted; and model.Validate runs it, which is what makes the
// invariant hold:
//
//	if Validate does not report ErrInvalidRule, the definition marshals AND the
//	marshalled bytes decode back.
//
// Before this was factored out, Validate discriminated on IsZero alone and that
// invariant was false in three measured ways: inline bytes that are not JSON
// validated and then failed in MarshalJSON, while an inline array and a blank name
// validated, marshalled, and produced bytes the decoder refuses. A Go caller can
// still reach all three through WithInlineRule/WithRule, so the check belongs
// somewhere both doors pass through, and Validate is that door.
func (r RuleSpec) shapeErr() error {
	switch {
	case r.Name != "" && len(r.Inline) > 0:
		return fmt.Errorf("%w: a catalog name and an inline document are exclusive", ErrInvalidRule)
	case r.Name != "":
		if strings.TrimSpace(r.Name) == "" {
			// Fails CLOSED. A blank name is indistinguishable from an absent rule
			// once decoded, so accepting it would let the author's mistake through
			// as "no rule" and nothing would ever report it.
			return fmt.Errorf("%w: the catalog name is blank", ErrInvalidRule)
		}
		return nil
	case len(r.Inline) > 0:
		trimmed := bytes.TrimSpace(r.Inline)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return fmt.Errorf("%w: the inline document must be a JSON object, got %s",
				ErrInvalidRule, describeJSONShape(trimmed))
		}
		if !json.Valid(trimmed) {
			return fmt.Errorf("%w: the inline document is not valid JSON", ErrInvalidRule)
		}
		return nil
	default:
		return fmt.Errorf("%w: neither a catalog name nor an inline document", ErrInvalidRule)
	}
}

// MarshalJSON re-emits the spec in the shape it was authored in: a string for a
// catalog name, the document itself for an inline rule.
//
// An ill-formed spec is an error rather than a lie about what was authored: the
// accept/refuse decision is shapeErr's, not this method's.
func (r RuleSpec) MarshalJSON() ([]byte, error) {
	if err := r.shapeErr(); err != nil {
		return nil, err
	}
	if r.Name != "" {
		return json.Marshal(r.Name)
	}
	return r.Inline, nil
}

// UnmarshalJSON accepts a string (a catalog name) or an object (an inline rule
// document) and refuses every other JSON type — number, bool, array and null —
// rather than coercing it. Silence is never the right answer for a reserved key:
// an author who writes `rule: 42` has made a mistake and must be told.
func (r *RuleSpec) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	var spec RuleSpec
	switch {
	case len(trimmed) > 0 && trimmed[0] == '"':
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRule, err)
		}
		spec = RuleSpec{Name: name}
	case len(trimmed) > 0 && trimmed[0] == '{':
		spec = RuleSpec{Inline: json.RawMessage(bytes.Clone(trimmed))}
	default:
		return fmt.Errorf("%w: got %s", ErrInvalidRule, describeJSONShape(trimmed))
	}
	// The leading byte picks the ARM; shapeErr is what accepts or refuses, so the
	// codec and Validate cannot disagree about what a well-formed rule is.
	if err := spec.shapeErr(); err != nil {
		return err
	}
	*r = spec
	return nil
}

// UnmarshalYAML is the YAML mirror of UnmarshalJSON. It discriminates on the
// node's resolved TAG rather than a leading byte, so `rule: "true"` is a catalog
// name while `rule: true` is a bool and refused. A mapping is converted to JSON
// so that Inline holds one representation whichever format authored it.
func (r *RuleSpec) UnmarshalYAML(value *yaml.Node) error {
	var spec RuleSpec
	switch {
	case value.Kind == yaml.ScalarNode && value.Tag == "!!str":
		spec = RuleSpec{Name: value.Value}
	case value.Kind == yaml.MappingNode:
		// map[string]any, not any: a key yaml.v3 cannot render as a string — a
		// COMPLEX key, i.e. a sequence or mapping used as a key — errors here with a
		// message about the document. Decoding into `any` would instead yield a
		// map[any]any that json.Marshal rejects with a message about Go types. Note
		// the boundary precisely: an INT key does not reach either path, because
		// yaml.v3 coerces `1:` to the string "1". A nested complex or int-keyed
		// mapping still lands in json.Marshal below, which is why that error is
		// wrapped too rather than assumed unreachable.
		var doc map[string]any
		if err := value.Decode(&doc); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRule, err)
		}
		inline, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRule, err)
		}
		spec = RuleSpec{Inline: inline}
	default:
		return fmt.Errorf("%w: got a %s value", ErrInvalidRule, strings.TrimPrefix(value.Tag, "!!"))
	}
	// One call site, as in UnmarshalJSON: the switch picks the ARM, shapeErr
	// accepts or refuses. Checking inside each arm instead would leave the mapping
	// arm's error branch unreachable — json.Marshal of a map[string]any always
	// yields a valid object — and unreachable error branches are what this file has
	// already had to justify once.
	if err := spec.shapeErr(); err != nil {
		return err
	}
	*r = spec
	return nil
}

// describeJSONShape names the JSON type of data for an ErrInvalidRule message,
// so a rejected rule says what was written rather than only what was wanted.
func describeJSONShape(data []byte) string {
	if len(data) == 0 {
		return "nothing"
	}
	switch c := data[0]; {
	case c == '[':
		return "an array"
	case c == 'n':
		// The 4-byte token only. Naming every n-initial byte "null" reported `not`,
		// `nan`, `nil`, `none` and `no` as null, which is confidently wrong.
		if string(data) == "null" {
			return "null"
		}
		return "an unrecognised value"
	case c == 't' || c == 'f':
		return "a bool"
	case c == '-' || (c >= '0' && c <= '9'):
		return "a number"
	default:
		// UnmarshalJSON is normally handed a valid JSON value, so nothing else can
		// arrive through a decoder — but it is exported behaviour on an exported
		// type and a direct caller can pass anything. Naming an unrecognised token
		// "a number" would be a confidently wrong message.
		return "an unrecognised value"
	}
}
