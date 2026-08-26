package httpcore

// ⚠ This is the ONLY in-package test file in httpcore — resolveRequestActor and its
// helpers are unexported. Every other test file here is httpcore_test. Keep it that way.

import (
	"context"
	"net/http"

	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

// blockUntilDeadline waits for the caller's deadline and reports it — but gives up
// after a bounded wait and SUCCEEDS instead.
//
// ⚠ The fallback is not padding. Without it, a resolver that waits on a deadline that
// never arrives blocks forever, so removing the timeout composition makes these tests
// HANG rather than fail — observed as `go test` EXIT=124 during the mutation check.
// A test whose failure mode is a hang tells CI nothing and stalls it; this one fails
// in ~2s with a readable assertion instead.
func blockUntilDeadline(ctx context.Context) (authz.Actor, error) {
	select {
	case <-ctx.Done():
		return authz.Actor{}, ctx.Err()
	case <-time.After(2 * time.Second):
		return authz.Actor{ID: "no-deadline-arrived"}, nil
	}
}

func nestDeep(d int) map[string]any {
	m := map[string]any{"leaf": 1}
	for range d {
		m = map[string]any{"a": m}
	}
	return m
}

func TestResolveRequestActor(t *testing.T) {
	t.Parallel()

	boom := errors.New("directory unreachable")

	tests := map[string]struct {
		resolve RequestActorFunc
		// ctx modifies the per-case context; nil means t.Context() unchanged.
		// Required by the project table-test skill for context-sensitive components,
		// and resolveRequestActor is one: it hands ctx to the consumer's resolver.
		ctx    func(context.Context) context.Context
		assert func(t *testing.T, got authz.Actor, err error)
	}{
		// ⚠ The lifecycle case the skill mandates. A resolver that honours ctx sees an
		// already-cancelled context and must surface it as an identity OUTAGE (503),
		// never as a silent success with the zero actor.
		"a cancelled context reaches the resolver and becomes 503": {
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			resolve: func(ctx context.Context) (authz.Actor, error) {
				if err := ctx.Err(); err != nil {
					return authz.Actor{}, err
				}
				return authz.Actor{ID: "should-not-be-reached"}, nil
			},
			assert: func(t *testing.T, got authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrIdentityUnavailable)
				assert.ErrorIs(t, err, context.Canceled, "the cause must survive for the 5xx log")
				assert.Equal(t, authz.Actor{}, got)
			},
		},
		"nil resolver fails CLOSED with 401, never a zero actor": {
			resolve: nil,
			assert: func(t *testing.T, got authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrUnauthenticated)
				assert.Equal(t, authz.Actor{}, got)
			},
		},
		"resolver reporting unauthenticated passes through": {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{}, ErrUnauthenticated
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrUnauthenticated)
				assert.NotErrorIs(t, err, ErrIdentityUnavailable,
					"an absent credential is not an outage")
			},
		},
		"resolver error becomes ErrIdentityUnavailable and keeps the cause": {
			resolve: func(context.Context) (authz.Actor, error) { return authz.Actor{}, boom },
			assert: func(t *testing.T, got authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrIdentityUnavailable)
				assert.ErrorIs(t, err, boom, "the cause must stay in the chain for the 5xx log")
				assert.Equal(t, authz.Actor{}, got)
			},
		},
		// ⚠ REGRESSION GUARD vs round 2, which accepted the zero actor. This targets
		// exactly one bug: `actor, _ := authenticate(r)` yields Actor{} with a nil error.
		"the ZERO actor is refused": {
			resolve: func(context.Context) (authz.Actor, error) { return authz.Actor{}, nil },
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrUnauthenticated)
			},
		},
		// strings.Split("", ",") returns [""] — what the canonical header middleware
		// produces for a header-less request. An empty string is not a role.
		`{Roles:[""]} — the strings.Split artifact — is refused`: {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{Roles: []string{""}}, nil
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrUnauthenticated)
			},
		},
		"{Roles:[]string{}} is refused, alike with the row above": {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{Roles: []string{}}, nil
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrUnauthenticated)
			},
		},
		// ⚠ REGRESSION GUARD vs round 1, which refused this. humantask/validate.go:24
		// blesses it: "anonymous but carrying roles".
		`{ID:"", Roles:["kiosk"]} — the kiosk claimant — PASSES`: {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{Roles: []string{"kiosk"}}, nil
			},
			assert: func(t *testing.T, got authz.Actor, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"kiosk"}, got.Roles)
			},
		},
		"a chan attribute is refused as 503, not 400": {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{ID: "a", Attributes: map[string]any{"c": make(chan int)}}, nil
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrIdentityUnavailable)
				assert.NotErrorIs(t, err, ErrBadInput,
					"the fault is the consumer's resolver, which the caller cannot correct")
			},
		},
		"attributes 65 deep are refused — the bound is 64": {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{ID: "a", Attributes: map[string]any{"n": nestDeep(64)}}, nil
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrIdentityUnavailable)
			},
		},
		// ⭐⭐ THE LOAD-BEARING FIXTURE. json.Marshal alone PASSES this (round 2), and so
		// does an Attributes-only round trip (round 3, which admitted 9999 where the
		// store admits 9998). Only an explicit depth bound catches it.
		"attributes 9999 deep are refused — both earlier guards passed this": {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{ID: "a", Attributes: map[string]any{"n": nestDeep(9998)}}, nil
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrIdentityUnavailable)
			},
		},
		"attributes above the size bound are refused": {
			resolve: func(context.Context) (authz.Actor, error) {
				big := make([]any, 0, 4096)
				for range 4096 {
					big = append(big, "0123456789abcdef")
				}
				return authz.Actor{ID: "a", Attributes: map[string]any{"big": big}}, nil
			},
			assert: func(t *testing.T, _ authz.Actor, err error) {
				assert.ErrorIs(t, err, ErrIdentityUnavailable)
			},
		},
		"a whole actor arrives whole, attributes included": {
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{
					ID:         "alice",
					Roles:      []string{"manager"},
					Attributes: map[string]any{"dept": "finance"},
				}, nil
			},
			assert: func(t *testing.T, got authz.Actor, err error) {
				require.NoError(t, err)
				assert.Equal(t, "alice", got.ID)
				assert.Equal(t, []string{"manager"}, got.Roles)
				// FAILS ON TODAY'S SHAPE: the three endpoints rebuild the actor as
				// authz.Actor{ID:…, Roles:…} and DROP Attributes entirely.
				assert.Equal(t, "finance", got.Attributes["dept"])
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}
			got, err := resolveRequestActor(ctx, tc.resolve)
			tc.assert(t, got, err)
		})
	}
}

// TestResolveRequestActorDeepCopies pins the fix for an UNCATCHABLE process crash:
// Actor.Clone is one level deep, so a consumer's nested attribute map stays shared,
// and marshalling it per request iterates a map they may be writing —
// "fatal error: concurrent map iteration and map write", which recover() cannot catch.
//
// FAILS WITHOUT THE DEEP COPY: the mutation below becomes visible in the resolved actor.
func TestResolveRequestActorDeepCopies(t *testing.T) {
	t.Parallel()

	nested := map[string]any{"team": "finance"}
	got, err := resolveRequestActor(t.Context(), func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "alice", Attributes: map[string]any{"profile": nested}}, nil
	})
	require.NoError(t, err)

	nested["team"] = "hacked" // the consumer keeps writing to the map it handed over

	assert.Equal(t, "finance", got.Attributes["profile"].(map[string]any)["team"],
		"nested attribute values must be DEEP-copied at the seam")
}

// TestResolveRequestActorCopiesTypedContainers is a REGRESSION GUARD.
//
// The first version of deepCopyBounded switched on map[string]any and []any only, so
// every other container fell through and was returned BY REFERENCE — measured, and
// caught at the delivery gate, not by three rounds of design audit. That silently
// falsified the isolation guarantee the copy exists to provide.
//
// FAILS WITHOUT THE REFLECT WALK: each mutation below becomes visible in the resolved
// actor.
func TestResolveRequestActorCopiesTypedContainers(t *testing.T) {
	t.Parallel()

	strMap := map[string]string{"team": "finance"}
	strSlice := []string{"manager"}
	deep := []map[string]any{{"k": "v"}}

	got, err := resolveRequestActor(t.Context(), func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "alice", Attributes: map[string]any{
			"profile": strMap, "roles": strSlice, "deep": deep,
		}}, nil
	})
	require.NoError(t, err)

	strMap["team"] = "hacked"
	strSlice[0] = "hacked"
	deep[0]["k"] = "hacked"

	assert.Equal(t, "finance", got.Attributes["profile"].(map[string]string)["team"],
		"map[string]string must be deep-copied")
	assert.Equal(t, "manager", got.Attributes["roles"].([]string)[0],
		"[]string must be deep-copied")
	assert.Equal(t, "v", got.Attributes["deep"].([]map[string]any)[0]["k"],
		"[]map[string]any must be deep-copied")
}

// TestResolveRequestActorCopiesArraysAsArrays is a REGRESSION GUARD for a latent panic.
//
// The Slice/Array branch used to build a SLICE unconditionally, so copying an array
// nested in a map produced a []T and writing it back panicked with
// "reflect.Value.SetMapIndex: value of type []int is not assignable to type [3]int" —
// unrecovered on the request path under a bare fiber.New()/gin.New().
//
// Not attacker-reachable (attribute values come from the consumer's resolver, and
// JSON-decoded claims never yield a Go array type), but a walk that claims to be total
// must not panic on a shape it accepts.
func TestResolveRequestActorCopiesArraysAsArrays(t *testing.T) {
	t.Parallel()

	got, err := resolveRequestActor(t.Context(), func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "a", Attributes: map[string]any{
			"nested": map[string][3]int{"k": {1, 2, 3}},
			"direct": [2]string{"x", "y"},
		}}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, [3]int{1, 2, 3}, got.Attributes["nested"].(map[string][3]int)["k"])
	assert.Equal(t, [2]string{"x", "y"}, got.Attributes["direct"].([2]string))
}

// TestResolveRequestActorRefusesUncopyableAttributes pins the fail-closed half: a
// pointer cannot be copied without aliasing its target or changing identity, and a
// chan or func cannot be marshalled at all.
func TestResolveRequestActorRefusesUncopyableAttributes(t *testing.T) {
	t.Parallel()

	n := 42
	for name, attr := range map[string]any{
		"pointer": &n,
		"channel": make(chan int),
		"func":    func() {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveRequestActor(t.Context(), func(context.Context) (authz.Actor, error) {
				return authz.Actor{ID: "a", Attributes: map[string]any{"x": attr}}, nil
			})
			assert.ErrorIs(t, err, ErrIdentityUnavailable)
		})
	}
}

// TestResolveRequestActorCopyIsTyped pins that the copy is a typed walk, NOT a
// marshal/unmarshal round trip. A round trip converts int→float64 and
// time.Time→string, silently changing what the expr authorizer evaluates.
func TestResolveRequestActorCopyIsTyped(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	got, err := resolveRequestActor(t.Context(), func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "a", Attributes: map[string]any{"count": 5, "joined": when}}, nil
	})
	require.NoError(t, err)

	assert.IsType(t, int(0), got.Attributes["count"], "int must not become float64")
	assert.IsType(t, time.Time{}, got.Attributes["joined"], "time.Time must not become string")
}

// TestResolveConfigComposesTheResolverTimeout pins BOTH halves of the bound —
// including the half it does NOT close.
//
// ⚠ It goes through ResolveConfig on purpose. The bound is composed into the resolver
// there, not passed to resolveRequestActor, because the endpoints have no sight of the
// config. An earlier revision carried RequestActorTimeout on the config and never read
// it, leaving WithRequestActorTimeout inert at every adapter — a test written against
// resolveRequestActor directly could not have caught that.
func TestResolveConfigComposesTheResolverTimeout(t *testing.T) {
	t.Parallel()

	t.Run("a ctx-honouring resolver is bounded", func(t *testing.T) {
		t.Parallel()
		cfg := ResolveConfig(
			WithRequestActor[*http.ServeMux](blockUntilDeadline),
			WithRequestActorTimeout[*http.ServeMux](50*time.Millisecond),
		)
		_, err := resolveRequestActor(t.Context(), cfg.RequestActor)
		assert.ErrorIs(t, err, ErrIdentityUnavailable)
	})

	t.Run("the option order does not matter", func(t *testing.T) {
		t.Parallel()
		cfg := ResolveConfig(
			WithRequestActorTimeout[*http.ServeMux](50*time.Millisecond),
			WithRequestActor[*http.ServeMux](blockUntilDeadline),
		)
		_, err := resolveRequestActor(t.Context(), cfg.RequestActor)
		assert.ErrorIs(t, err, ErrIdentityUnavailable, "composition must happen after the option loop")
	})

	// ⚠ Pinned as STILL SUCCEEDING. MEASURED: a ctx-ignoring resolver ran 1.5s against a
	// 200ms bound and returned nil. The bound narrows the hang; it does not close it, and
	// WithRequestActorTimeout's godoc says so.
	t.Run("a ctx-IGNORING resolver is NOT bounded — documented, not fixed", func(t *testing.T) {
		t.Parallel()
		cfg := ResolveConfig(
			WithRequestActor[*http.ServeMux](func(context.Context) (authz.Actor, error) {
				time.Sleep(120 * time.Millisecond)
				return authz.Actor{ID: "late"}, nil
			}),
			WithRequestActorTimeout[*http.ServeMux](20*time.Millisecond),
		)
		start := time.Now()
		got, err := resolveRequestActor(t.Context(), cfg.RequestActor)
		require.NoError(t, err, "the bound does not stop a resolver that ignores ctx")
		assert.Equal(t, "late", got.ID)
		assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	})

	// The default must actually be armed, not merely stored.
	t.Run("the 10s default is composed, not just recorded", func(t *testing.T) {
		t.Parallel()
		var sawDeadline bool
		cfg := ResolveConfig(WithRequestActor[*http.ServeMux](func(ctx context.Context) (authz.Actor, error) {
			_, sawDeadline = ctx.Deadline()
			return authz.Actor{ID: "a"}, nil
		}))
		_, err := resolveRequestActor(t.Context(), cfg.RequestActor)
		require.NoError(t, err)
		assert.True(t, sawDeadline, "the default bound must reach the consumer's resolver")
	})
}
