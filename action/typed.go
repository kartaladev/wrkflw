package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ErrDecodeInput reports that an action's input map could not be decoded into the
// action's In type. It is classified NON-RETRYABLE (see [NonRetryable]): the input
// is fixed by the engine's snapshot for the lifetime of the invocation, so the same
// decode would fail identically on every retry.
var ErrDecodeInput = errors.New("workflow-action: decode typed input")

// TypedOption configures a [Typed] action.
type TypedOption func(*typedConfig)

// typedConfig carries the options a [Typed] action was constructed with.
type typedConfig struct {
	strict bool
}

// WithStrictInput rejects an input map carrying any key the In type does not
// declare. The default is lenient, which rejects nothing — see [Typed] for what
// lenient does and does not ignore.
//
// Matching is EXACT, byte for byte, against the JSON names In declares, so a key
// that only FOLDS onto a declared name — "_idempotencykey" against
// IdempotencyKey — is rejected rather than bound to it. That exact-key check is
// the guard that actually holds: [json.Decoder.DisallowUnknownFields] matches
// object keys to struct fields through encoding/json's foldName and so never
// fires on such a key, and because [json.Marshal] sorts map keys byte-wise, a
// lowercase twin would otherwise be applied after — and therefore win over — the
// value it shadows.
//
// Read "folds" wider than "differs only in case". foldName also folds a few
// non-ASCII runes onto ASCII — U+017F LATIN SMALL LETTER LONG S onto "s" and
// U+212A KELVIN SIGN onto "k" — so a key beginning with either binds to a field
// tagged "sKey" or "kKey" while looking nothing like a case variant of it.
//
// Both guards run, but their reach differs:
//
//   - the exact-key check covers the TOP-LEVEL object;
//   - DisallowUnknownFields rejects genuinely-unknown keys inside NESTED objects;
//   - a nested FOLD-EQUAL key is caught by NEITHER, because
//     DisallowUnknownFields folds. Strict rejects one at the top level only.
//
// That last point is a known, documented limit, not an oversight: recursive
// exact-key checking for nested objects is tracked as a follow-up.
//
// Strict REQUIRES a struct In, optionally behind a single pointer, and [Typed]
// panics otherwise. DisallowUnknownFields is silently a no-op for map and
// interface destinations, so strict on such an In would promise a check it could
// never perform.
//
// Strict does NOT imply presence. JSON has no required-field concept, and a nil or
// empty input marshals to null, which has no members to reject, so it decodes to
// the zero In with no error. Strict constrains which keys MAY appear, never which
// keys MUST.
//
// Use it only for envelopes whose every key is known. It is NOT appropriate for a
// primary service task: the engine stamps an "_idempotencyKey" entry into that
// input (see engine/step_state.go), and strict decoding rejects the invocation with
// [ErrDecodeInput] unless In declares the field itself:
//
//	type ApproveIn struct {
//	    Ref            string `json:"ref"`
//	    IdempotencyKey string `json:"_idempotencyKey"`
//	}
//
// The limit fails closed: a loud [ErrDecodeInput] naming the offending key.
func WithStrictInput() TypedOption {
	return func(c *typedConfig) { c.strict = true }
}

// typedAction adapts a consumer function written against its own Go types to the
// map-based [Action] interface by JSON round trip.
//
// It deliberately does NOT implement Unwrap() Action: a typed action is the
// innermost BARE action, so [Wrap], [Unwrap] and [ResolvePolicy] treat it as the
// end of the chain.
type typedAction[In, Out any] struct {
	fn  func(context.Context, In) (Out, error)
	cfg typedConfig
	// names is the set of JSON names In declares, computed once at construction.
	// It is nil unless cfg.strict is set.
	names map[string]struct{}
}

// Do decodes in into In, invokes fn, and encodes its result back to a map.
func (a typedAction[In, Out]) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	if a.cfg.strict {
		if err := rejectUnknownKeys(in, a.names); err != nil {
			return nil, NonRetryable(fmt.Errorf("%w: %w", ErrDecodeInput, err))
		}
	}
	arg, err := decodeInto[In](in, a.cfg.strict)
	if err != nil {
		// Two %w verbs: errors.Is(err, ErrDecodeInput) holds for classification
		// while the underlying json error stays in the chain for diagnosis.
		return nil, NonRetryable(fmt.Errorf("%w: %w", ErrDecodeInput, err))
	}
	res, err := a.fn(ctx, arg)
	if err != nil {
		return nil, err
	}
	return encodeOut(res)
}

// Typed adapts fn, written against the consumer's own In and Out types, to the
// map-based [Action] interface. The engine, runtime and persistence layers never
// see In or Out: identity stays the string action name.
//
//	reg.Register("approve", action.Typed(approve))
//
// The input map is decoded into In by JSON round trip, and fn's result is encoded
// back to a map[string]any the same way.
//
// Input decoding is LENIENT by default, because a service task receives the whole
// variables map plus the engine's "_idempotencyKey" stamp.
//
// Lenient ignores keys that match nothing — but that is NOT every undeclared key.
// encoding/json matches through foldName, so a key that folds onto a declared
// name BINDS to that field instead of being ignored, and because [json.Marshal]
// emits map keys byte-sorted, a lowercase twin of a camelCase name is applied
// last and WINS. Folding is wider than case: U+017F folds onto "s" and U+212A
// onto "k".
// A workflow variable named "_idempotencykey" therefore overrides the engine's
// "_idempotencyKey" stamp for an In that declares it. [WithStrictInput] is the
// only mode that rejects such a key, and only at the top level.
//
// A failure to decode the input into In is reported as [ErrDecodeInput] and is
// non-retryable. An error returned by fn itself is passed through unchanged, so fn
// keeps control of its own retry classification.
//
// Typed PANICS if fn is nil, if Out is not a struct or a map (optionally behind a
// single pointer), or if [WithStrictInput] is combined with an In that is not a
// struct (same one-pointer allowance). All three are programming errors fixed at
// construction: no runtime input can trigger them and no retry can clear them,
// which is the same reasoning behind [Registry.MustRegister].
//
// In carries NO constraint in lenient mode; only strict mode requires a struct.
func Typed[In, Out any](fn func(context.Context, In) (Out, error), opts ...TypedOption) Action {
	if fn == nil {
		panic(errors.New("action.Typed: fn must not be nil"))
	}
	if rt := reflect.TypeFor[Out](); !validOutType(rt) {
		panic(fmt.Errorf(
			"action.Typed: invalid Out type %s: "+
				"Out must be a struct or a map, optionally behind a single pointer", rt))
	}

	var cfg typedConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	var names map[string]struct{}
	if cfg.strict {
		rt := reflect.TypeFor[In]()
		st := rt
		if st.Kind() == reflect.Pointer {
			st = st.Elem()
		}
		if st.Kind() != reflect.Struct {
			panic(fmt.Errorf(
				"action.Typed: invalid In type %s: "+
					"WithStrictInput requires a struct In, optionally behind a single pointer", rt))
		}
		names = jsonNames(st)
	}
	return typedAction[In, Out]{fn: fn, cfg: cfg, names: names}
}

// rejectUnknownKeys reports an error naming every key of in that is not an exact
// byte-for-byte member of names. Unknown keys are sorted so the message — which is
// persisted on the resulting ActionFailed — is deterministic.
//
// # The blast radius of this message, measured (#145)
//
// This message does not stay here. It becomes ErrDecodeInput, the runtime records
// err.Error() as ActionFailed.Err, and from there it reaches, at least: the
// "_errorMessage" instance variable on a catch-flow (durable); an error boundary's
// ErrorExpr as "_error"; Incident.Error (durable); FailInstance.Err; and a slog
// record in findDirectBoundary. So every byte below reaches durable state, an
// expression evaluator and a telemetry sink — and the key names in it are
// CALLER-CHOSEN: the engine merges StartInstance variables, action output and
// message payloads into the instance variables wholesale, keys included.
//
// Two properties of that were measured against the pre-exact-match tree
// (11037351, which used DisallowUnknownFields) and this one, with the same probe:
//
//   - VOLUME grew, and this is the real delta. DisallowUnknownFields names only
//     the FIRST offending key, so its message was 65 bytes for 1 unknown key and
//     65 bytes for 50. This one names them all: 95, 106, 136, 186, 586 bytes for
//     1, 2, 5, 10, 50 keys. That series is an 85-byte frame (86 once "key"
//     becomes "keys") plus len(strconv.Quote(key))+2 per key. So the message is
//     UNBOUNDED in the NUMBER of keys where the old one was constant in it.
//
//     Read that as what it is. It is linear over data the instance ALREADY
//     stores: every key named here came from the input map, which is a copy of
//     s.Variables, so the persisted copy carries the NAMES again and never the
//     values. For a printable-ASCII name that is a duplication; strconv.Quote
//     renders a byte that is not valid printable UTF-8 as \xNN, so a hostile
//     name costs up to 4x its own length. Bounded, still linear, and measured
//     non-superlinear in both dimensions: the message-to-input ratio falls
//     monotonically and tends to (L+4)/L for keys of length L — so at most ~4x
//     when every byte has to be escaped, and towards 1x as keys grow. Stated as
//     the closed form rather than as sampled constants, because a constant read
//     off one key width reads as a floor and is not one. It is not a
//     resource-exhaustion vector. It matters because the copy lands somewhere
//     the original does not: an expression evaluator.
//
//   - The SIZE of any one key was already unbounded and is unchanged: a 10 KiB key
//     produced a 10 059-byte message before and a 10 089-byte one now. Arbitrary
//     caller-chosen text of arbitrary length did not newly become reachable here.
//
// Escaping is unchanged in both directions: strconv.Quote and encoding/json's %q
// produce byte-identical output, so the exact-match change neither added nor
// removed a mitigation.
//
// ⚠ What that escaping is worth is a question this comment previously answered
// WRONGLY, in the fail-open direction, so the correction is stated rather than
// silently dropped. It does NOT stop a key name being read as expression source.
// Quote escapes quotes INSIDE the key, but the two " delimiters it wraps them in
// go into the message unescaped, and expr accepts single-quoted literals that
// Quote never touches: a key named `== 'x' or true or` walks straight through.
// The earlier claim rested on one payload that happened to contain a ", which
// Quote renders \" and expr's lexer rejects — a property of that payload, not of
// the escaping. What Quote IS worth is measured and narrow: it keeps LF, CR, ESC,
// NUL and U+2028 out of the message, so a hostile key name cannot forge a log
// record or reach a terminal as an ANSI escape. The barriers that actually stop
// injection are named on env["_error"] in engine/step_errors.go.
//
// The one KIND of key that newly reaches this message is one that encoding/json's
// foldName treats as equal to a declared JSON name. That is wider than "a case
// variant": foldName folds a few non-ASCII runes onto ASCII — two measured
// members, U+017F LATIN SMALL LETTER LONG S onto "s" and U+212A KELVIN SIGN onto
// "k" — so a key beginning with either binds to a field tagged "sKey" or "kKey"
// while looking nothing like a case variant. Before, such a key folded into the
// matching field and no error was produced at all.
// Genuinely-unknown keys already reached "_errorMessage" as
// `json: unknown field "…"`.
func rejectUnknownKeys(in map[string]any, names map[string]struct{}) error {
	var unknown []string
	for k := range in {
		if _, ok := names[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)

	quoted := make([]string, len(unknown))
	for i, k := range unknown {
		quoted[i] = strconv.Quote(k)
	}
	noun := "key"
	if len(unknown) > 1 {
		noun = "keys"
	}
	return fmt.Errorf("strict input: unknown %s %s (exact JSON names only)",
		noun, strings.Join(quoted, ", "))
}

// jsonNames returns the set of JSON object keys the struct type st declares,
// mirroring encoding/json's own field-collection rules.
func jsonNames(st reflect.Type) map[string]struct{} {
	names := make(map[string]struct{})
	collectJSONNames(st, names, make(map[reflect.Type]bool))
	return names
}

// collectJSONNames walks the struct type st, following embedded structs, which
// flatten into the parent JSON object. seen breaks the recursion on a type that
// embeds itself. Every caller dereferences a pointer and checks for Struct before
// calling, so st is always a struct here.
func collectJSONNames(st reflect.Type, names map[string]struct{}, seen map[reflect.Type]bool) {
	if seen[st] {
		return
	}
	seen[st] = true

	for i := range st.NumField() {
		f := st.Field(i)
		tag, _ := f.Tag.Lookup("json")
		if tag == "-" {
			// Excluded entirely. A tag of `json:"-,"` names the field "-" and is
			// deliberately not caught here.
			continue
		}
		name, _, _ := strings.Cut(tag, ",")

		// An embedded field with no tag name flattens into the parent object —
		// even when its own type is unexported, since its EXPORTED fields are
		// still promoted. An embedded non-struct of unexported type is ignored.
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectJSONNames(ft, names, seen)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names[name] = struct{}{}
	}
}

// validOutType reports whether rt can be encoded to the map[string]any envelope
// the engine carries: a struct or a map, optionally behind exactly ONE pointer.
//
// Scalars, slices and arrays are rejected because they cannot produce a map. An
// interface — including any — is rejected too: it can hold a scalar at runtime, so
// accepting it would defer a construction-time failure to invocation time.
func validOutType(rt reflect.Type) bool {
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	switch rt.Kind() {
	case reflect.Struct, reflect.Map:
		return true
	default:
		return false
	}
}

// decodeInto decodes the input map into a fresh In by JSON round trip.
//
// Under strict, DisallowUnknownFields is the SECOND guard, and it is LOAD-BEARING.
// Do NOT remove it as redundant with the exact-key check:
//
//   - it rejects genuinely-unknown keys inside NESTED objects, which
//     rejectUnknownKeys does not descend into;
//   - it is the ONLY guard that catches an embedded-name CONFLICT — two embedded
//     structs declaring the same JSON name. encoding/json drops both conflicting
//     fields, so that name is unknown to the decoder, while collectJSONNames still
//     adds it. The name set over-accepts there, and this guard is what turns the
//     composite back into a rejection.
//
// It folds through foldName, so it cannot replace the top-level exact-key check,
// and a nested fold-equal key is caught by neither (a known limit; see
// [WithStrictInput]).
//
// A nil in marshals to JSON null, which has no members: even a strict decode
// succeeds and leaves dst at its zero value.
func decodeInto[In any](in map[string]any, strict bool) (In, error) {
	var dst In
	raw, err := json.Marshal(in)
	if err != nil {
		return dst, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&dst); err != nil {
		return dst, err
	}
	return dst, nil
}

// encodeOut encodes v back to the map envelope the engine carries.
func encodeOut[Out any](v Out) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("workflow-action: encode typed output: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("workflow-action: encode typed output: %w", err)
	}
	return out, nil
}
