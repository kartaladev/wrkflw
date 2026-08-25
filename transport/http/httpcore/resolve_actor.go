package httpcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/kartaladev/wrkflw/authz"
)

const (
	// maxActorAttributeDepth bounds attribute nesting.
	//
	// ⚠ This is a DEPTH bound rather than a round trip, and the distinction is the
	// whole point. encoding/json's ENCODER has no nesting limit while its DECODER caps
	// the WHOLE stored document at 10000 — and attributes are nested inside an Actor,
	// inside a claim, inside a task row, with THREE different stored shapes
	// (wrkflw_human_task.claim_actor marshals an Actor; .candidates marshals
	// []authz.Actor; the instance snapshot is deeper still). Validating "the" stored
	// shape is therefore unfixable; bounding the attributes themselves is
	// shape-independent.
	//
	// MEASURED: 64 leaves 10000-64 = 9936 levels of headroom, and a 64-deep attribute
	// survives a real store round trip at wrapper depths of 1, 10, 100 and 1000.
	// Two earlier guards admitted values the store then rejected forever: json.Marshal
	// alone admitted 20000, and an Attributes-only round trip admitted 9999 where the
	// store admits 9998.
	maxActorAttributeDepth = 64
	// maxActorAttributeBytes bounds the marshalled attributes. 16 KiB is generous for a
	// principal's profile and far below any request-body cap.
	//
	// ⚠ Load-bearing, not decoration: the walk and marshal below run OUTSIDE
	// RequestActorTimeout, because they inspect the actor the resolver already
	// returned. Without this bound they would be unbounded work on the request path.
	maxActorAttributeBytes = 16 << 10
)

// isZeroActor reports whether a carries no identity at all.
//
// ⚠ An empty string is not a role: strings.Split("", ",") returns [""] (length 1),
// which is what the canonical header middleware produces for a header-less request.
func isZeroActor(a authz.Actor) bool {
	if a.ID != "" || len(a.Attributes) > 0 {
		return false
	}
	for _, r := range a.Roles {
		if r != "" {
			return false
		}
	}
	return true
}

// deepCopyBounded returns a TYPED deep copy of v, reporting false when nesting
// exceeds budget or when v holds something this transport will not copy.
//
// ⚠ It walks by REFLECTION, not by a type switch over map[string]any / []any.
// An earlier version switched on those two shapes only, so EVERY other container —
// map[string]string, []string, []map[string]any — fell through and was returned **by
// reference**. Measured: mutating a nested map[string]string after the resolver
// returned was visible in the resolved actor. That silently falsified the guarantee
// this copy exists to provide, because the marshal below would still iterate a map the
// consumer owned.
//
// What each kind does:
//
//   - Map, Slice, Array — deep-copied, preserving the concrete type, budget charged.
//   - Chan, Func, Ptr, UnsafePointer — REFUSED. A pointer cannot be copied without
//     either aliasing its target or changing identity, and chan/func cannot be
//     marshalled at all, so refusing is both safe and honest.
//   - everything else (scalars, and structs such as time.Time) — copied BY VALUE.
//
// ⚠ The remaining hole is stated rather than hidden: a STRUCT is copied by value, so a
// mutable field *inside* a struct is still shared. Structs reaching actor attributes
// are value types in practice (time.Time), and RequestActorFunc's godoc carries the
// don't-mutate-concurrently contract that covers the rest.
//
// ⚠ Typed rather than marshal/unmarshal: a JSON round trip converts int to float64 and
// time.Time to string, silently changing what the expr authorizer evaluates.
//
// Bailing at the budget also makes this terminate on a cyclic structure.
func deepCopyBounded(v any, budget int) (any, bool) {
	if budget < 0 {
		return nil, false
	}
	if v == nil {
		return nil, true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Pointer, reflect.UnsafePointer:
		return nil, false

	case reflect.Map:
		if rv.IsNil() {
			return v, true
		}
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			c, ok := deepCopyBounded(iter.Value().Interface(), budget-1)
			if !ok {
				return nil, false
			}
			cv := reflect.ValueOf(c)
			if c == nil {
				cv = reflect.Zero(rv.Type().Elem())
			}
			if !cv.Type().AssignableTo(rv.Type().Elem()) {
				return nil, false // refuse rather than panic — see the array note below
			}
			out.SetMapIndex(iter.Key(), cv)
		}
		return out.Interface(), true

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return v, true
		}
		// ⚠ An ARRAY must be copied as an array, not as a slice. An earlier version
		// always built reflect.MakeSlice, so copying map[string][3]int produced a []int
		// and writing it back panicked:
		//   reflect.Value.SetMapIndex: value of type []int is not assignable to [3]int
		// Not attacker-reachable — attribute values come from the consumer's resolver and
		// JSON-decoded claims never yield a Go array type — but it was a live panic on
		// the request path, and this walk must be total over what it accepts.
		var out reflect.Value
		if rv.Kind() == reflect.Array {
			out = reflect.New(rv.Type()).Elem()
		} else {
			out = reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		}
		for i := range rv.Len() {
			c, ok := deepCopyBounded(rv.Index(i).Interface(), budget-1)
			if !ok {
				return nil, false
			}
			if c == nil {
				continue // leave the element's zero value in place
			}
			cv := reflect.ValueOf(c)
			if !cv.Type().AssignableTo(out.Index(i).Type()) {
				return nil, false // refuse rather than panic
			}
			out.Index(i).Set(cv)
		}
		return out.Interface(), true

	default:
		return v, true
	}
}

// resolveRequestActor turns the configured resolver into an AUTHENTICATED actor, or
// into an error that ClassifyError maps to 401 or 503.
//
// ⚠ It never returns a zero actor with a nil error. An unresolved identity is a
// refusal, never a downgrade — that is the whole of ADR-0189.
// RequestActor resolves the AUTHENTICATED principal for one request, or returns an
// error that ClassifyError maps to 401 or 503.
//
// ⚠ ADAPTERS MUST CALL THIS BEFORE READING THE REQUEST BODY. Resolution used to happen
// inside the endpoint, which meant an unauthenticated caller could force a full
// MaxBodyBytes read (1 MiB by default) and hold a handler for BodyReadTimeout (30s)
// before receiving its 401 — an unauthenticated resource-consumption primitive on the
// only routes that authenticate. Calling this first closes that window.
//
// The 401/503 decision itself lives HERE and nowhere else, so the nine adapter call
// sites duplicate a two-line call, never the policy.
func RequestActor(ctx context.Context, resolve RequestActorFunc) (authz.Actor, error) {
	return resolveRequestActor(ctx, resolve)
}

// ⚠ It takes no timeout: the bound is composed INTO the resolver by ResolveConfig, so
// that the endpoints need only the one added argument. See the note there.
func resolveRequestActor(ctx context.Context, resolve RequestActorFunc) (authz.Actor, error) {
	// A nil resolver means a consumer built a CustomizeConfig by hand without
	// ResolveConfig. Refusing is the fail-closed reading of "the seam was forgotten".
	if resolve == nil {
		return authz.Actor{}, ErrUnauthenticated
	}

	a, err := resolve(ctx)
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return authz.Actor{}, err
	case err != nil:
		// %w on BOTH so the cause survives for the adapter's 5xx log while the sentinel
		// drives classification. See ClassifyError's first arms for why wrapping
		// arbitrary consumer errors forces that arm to the top.
		return authz.Actor{}, fmt.Errorf("%w: %w", ErrIdentityUnavailable, err)
	}

	// Refuse the ZERO actor — and nothing else. This targets exactly one bug:
	// `actor, _ := authenticate(r)` yields Actor{} with a nil error and the request
	// would proceed as though authenticated.
	//
	// ⚠ It does NOT make the actor attributable and does NOT close the attribute
	// fail-open; the blessed kiosk claimant {ID:"", Roles:["kiosk"]} is equally
	// unattributable and is deliberately admitted (humantask/validate.go:24).
	if isZeroActor(a) {
		return authz.Actor{}, fmt.Errorf("%w: the resolver returned the zero actor", ErrUnauthenticated)
	}

	if len(a.Attributes) > 0 {
		// One walk: bound the depth AND take the deep copy. Copying first means the
		// marshal below touches only our own map, which is what stops
		// "fatal error: concurrent map iteration and map write" — a Go runtime fatal
		// that recover() does NOT catch — when a consumer keeps writing to a nested
		// map it handed over.
		safe, ok := deepCopyBounded(a.Attributes, maxActorAttributeDepth)
		if !ok {
			return authz.Actor{}, fmt.Errorf(
				"%w: actor attributes nest deeper than %d, or hold a pointer, channel or func",
				ErrIdentityUnavailable, maxActorAttributeDepth)
		}
		copied, _ := safe.(map[string]any)
		blob, mErr := json.Marshal(copied)
		if mErr != nil {
			return authz.Actor{}, fmt.Errorf("%w: actor attributes are not JSON-serialisable: %w",
				ErrIdentityUnavailable, mErr)
		}
		if len(blob) > maxActorAttributeBytes {
			return authz.Actor{}, fmt.Errorf("%w: actor attributes exceed %d bytes",
				ErrIdentityUnavailable, maxActorAttributeBytes)
		}
		a.Attributes = copied
	}
	return a, nil
}
