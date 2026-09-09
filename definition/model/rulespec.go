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
	// Inline is the rule document as authored, held verbatim as JSON. Set when
	// the key was authored as an object. wrkflw never parses its contents.
	//
	// A YAML-authored document is converted to JSON on decode, so its KEY ORDER
	// is the JSON canonical (sorted) order rather than the order it was written
	// in. Verbatim means the document's CONTENT is untouched, not its byte
	// framing; nothing downstream depends on key order.
	Inline json.RawMessage
}

// IsZero reports whether the spec carries no rule at all — the state of a
// businessRuleTask authored without the key, which is the only state Validate
// currently accepts.
func (r RuleSpec) IsZero() bool { return r.Name == "" && len(r.Inline) == 0 }

// MarshalJSON re-emits the spec in the shape it was authored in: a string for a
// catalog name, the document itself for an inline rule.
//
// A zero RuleSpec is an error rather than `{}` or `null`, because a marshalled
// definition claiming an empty rule would be a lie about what was authored. It is
// unreachable through a node — see NodeWire.Rule for why that field is a pointer —
// and Validate refuses a non-nil zero spec (ErrInvalidRule) so that "validates"
// and "can be marshalled" stay the same set.
func (r RuleSpec) MarshalJSON() ([]byte, error) {
	switch {
	case r.Name != "":
		return json.Marshal(r.Name)
	case len(r.Inline) > 0:
		return r.Inline, nil
	default:
		return nil, fmt.Errorf("%w: nothing to encode", ErrInvalidRule)
	}
}

// UnmarshalJSON accepts a string (a catalog name) or an object (an inline rule
// document) and refuses every other JSON type — number, bool, array and null —
// rather than coercing it. Silence is never the right answer for a reserved key:
// an author who writes `rule: 42` has made a mistake and must be told.
func (r *RuleSpec) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '"':
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRule, err)
		}
		if strings.TrimSpace(name) == "" {
			// Fails CLOSED. A blank name is indistinguishable from an absent rule
			// once decoded, so accepting it would let the author's mistake through
			// as "no rule" and Validate would never report it.
			return fmt.Errorf("%w: the catalog name is blank", ErrInvalidRule)
		}
		*r = RuleSpec{Name: name}
		return nil
	case len(trimmed) > 0 && trimmed[0] == '{':
		if !json.Valid(trimmed) {
			return fmt.Errorf("%w: the inline document is not valid JSON", ErrInvalidRule)
		}
		*r = RuleSpec{Inline: json.RawMessage(bytes.Clone(trimmed))}
		return nil
	default:
		return fmt.Errorf("%w: got %s", ErrInvalidRule, describeJSONShape(trimmed))
	}
}

// UnmarshalYAML is the YAML mirror of UnmarshalJSON. It discriminates on the
// node's resolved TAG rather than a leading byte, so `rule: "true"` is a catalog
// name while `rule: true` is a bool and refused. A mapping is converted to JSON
// so that Inline holds one representation whichever format authored it.
func (r *RuleSpec) UnmarshalYAML(value *yaml.Node) error {
	switch {
	case value.Kind == yaml.ScalarNode && value.Tag == "!!str":
		if strings.TrimSpace(value.Value) == "" {
			return fmt.Errorf("%w: the catalog name is blank", ErrInvalidRule)
		}
		*r = RuleSpec{Name: value.Value}
		return nil
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
		*r = RuleSpec{Inline: inline}
		return nil
	default:
		return fmt.Errorf("%w: got a %s value", ErrInvalidRule, strings.TrimPrefix(value.Tag, "!!"))
	}
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
		return "null"
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
