package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
// declare, instead of ignoring it (the default is lenient).
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
// The limit fails closed: a loud [ErrDecodeInput] naming the offending key, never a
// silent wrong decode.
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
}

// Do decodes in into In, invokes fn, and encodes its result back to a map.
func (a typedAction[In, Out]) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
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
// Input decoding is LENIENT by default: keys the In type does not declare are
// ignored, because a service task receives the whole variables map plus the
// engine's "_idempotencyKey" stamp. Pass [WithStrictInput] to reject unknown keys.
//
// A failure to decode the input into In is reported as [ErrDecodeInput] and is
// non-retryable. An error returned by fn itself is passed through unchanged, so fn
// keeps control of its own retry classification.
//
// Typed PANICS if fn is nil, or if Out cannot carry a map envelope (see
// [validOutType]). Both are programming errors fixed at construction: no runtime
// input can trigger them and no retry can clear them, which is the same reasoning
// behind [Registry.MustRegister].
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
	return typedAction[In, Out]{fn: fn, cfg: cfg}
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
