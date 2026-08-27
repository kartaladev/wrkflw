package httpcore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

func discloseState() engine.InstanceState {
	return engine.InstanceState{
		InstanceID: "i1",
		Variables:  map[string]any{"ssn": "111-22-3333"},
	}
}

// TestDisclosingMapper_KeysOnTheRESOLVER_notTheContext is the sharp one.
//
// ⚠ Nothing in transport/http ever calls authz.ContextWithActor: ADR-0189 passes the actor
// to the endpoints as an ARGUMENT. A decision keyed on authz.ActorFromContext is therefore
// ALWAYS false, and would project for authenticated callers too — the "everyone blind"
// configuration this design exists to avoid. The signal must be the configured resolver,
// which is what a consumer using WithRequestActor actually populates.
func TestDisclosingMapper_KeysOnTheRESOLVER_notTheContext(t *testing.T) {
	t.Parallel()

	// A header-style resolver: identity reaches the transport WITHOUT touching the context.
	resolve := func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "alice", Roles: []string{"manager"}}, nil
	}

	m := httpcore.DisclosingMapper(t.Context(), resolve, nil, nil)
	got, ok := m(discloseState()).(httpcore.InstanceView)
	if !ok {
		t.Fatalf("want InstanceView, got %T", m(discloseState()))
	}
	if got.Variables["ssn"] != "111-22-3333" {
		t.Error("an identified caller must receive full fidelity; the resolver returned " +
			"an actor but the mapper projected anyway")
	}
}

func TestDisclosingMapper_ProjectsWhenUnidentified(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		resolve httpcore.RequestActorFunc
	}{
		{"nil resolver", nil},
		{"no credential", func(context.Context) (authz.Actor, error) {
			return authz.Actor{}, httpcore.ErrUnauthenticated
		}},
		{"identity source failed", func(context.Context) (authz.Actor, error) {
			return authz.Actor{}, errors.New("upstream down")
		}},
		{
			// ⚠ ADR-0189 refuses the zero actor three lines from its own resolver, and an
			// empty AuthzSpec ALLOWS it. "Present" must mean IDENTIFIED, not non-nil.
			name: "zero actor returned with a nil error",
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{}, nil
			},
		},
		{
			// The kiosk claimant ADR-0189 blesses: no ID, only a role.
			name: "kiosk claimant carries no identity",
			resolve: func(context.Context) (authz.Actor, error) {
				return authz.Actor{Roles: []string{"kiosk"}}, nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := httpcore.DisclosingMapper(t.Context(), tc.resolve, nil, nil)
			got := m(discloseState()).(httpcore.InstanceView)
			if got.Variables != nil {
				t.Errorf("unidentified caller received variables: %v", got.Variables)
			}
			if got.InstanceID != "i1" {
				t.Error("structural fields must survive the projection")
			}
		})
	}
}

func TestDisclosingMapper_HonoursTheDisclosureSet(t *testing.T) {
	t.Parallel()

	unidentified := func(context.Context) (authz.Actor, error) {
		return authz.Actor{}, httpcore.ErrUnauthenticated
	}

	m := httpcore.DisclosingMapper(t.Context(), unidentified,
		authz.NewDisclosureSet(authz.DiscloseVariables), nil)
	got := m(discloseState()).(httpcore.InstanceView)
	if got.Variables["ssn"] != "111-22-3333" {
		t.Error("DiscloseVariables must restore variables even for an unidentified caller")
	}
}

// TestDisclosingMapper_WrapsAConsumerMapper pins that a custom InstanceMapper receives the
// PROJECTION, not the full state. Filtering a custom mapper's OUTPUT is impossible — it may
// render any field — so it must never be handed the withheld data in the first place.
func TestDisclosingMapper_WrapsAConsumerMapper(t *testing.T) {
	t.Parallel()

	var seen engine.InstanceState
	custom := func(st engine.InstanceState) any { seen = st; return st }
	unidentified := func(context.Context) (authz.Actor, error) {
		return authz.Actor{}, httpcore.ErrUnauthenticated
	}

	m := httpcore.DisclosingMapper(t.Context(), unidentified, nil, custom)
	_ = m(discloseState())

	if seen.Variables != nil {
		t.Errorf("a custom mapper received unredacted variables (%v) — filtering its "+
			"OUTPUT cannot fix this", seen.Variables)
	}
	if seen.InstanceID != "i1" {
		t.Error("the custom mapper must still receive the structural fields")
	}
}

// TestDiscloseAll_SkipsTheProjectionEntirely pins the sentinel.
//
// ⚠ DiscloseAll is NOT the union of the four categories. Those restore only the fields some
// category names — 20 of InstanceState's 31 are restorable by none of them, including
// `incidents` and `compensating`, the projection that makes a WEDGED instance findable
// (ADR-0175). A union would silently break the operator escape hatch, so the opt-out has to
// mean "do not project at all".
func TestDiscloseAll_SkipsTheProjectionEntirely(t *testing.T) {
	t.Parallel()

	unidentified := func(context.Context) (authz.Actor, error) {
		return authz.Actor{}, httpcore.ErrUnauthenticated
	}
	st := discloseState()
	st.Incidents = []engine.Incident{{ID: "inc1", Error: "boom"}}

	m := httpcore.DisclosingMapper(t.Context(), unidentified,
		authz.NewDisclosureSet(authz.DiscloseAll), nil)
	got := m(st).(httpcore.InstanceView)
	if got.Variables["ssn"] != "111-22-3333" {
		t.Error("DiscloseAll must restore variables")
	}

	// The union of the four categories cannot reach Incidents; the sentinel must.
	if proj := httpcore.DisclosingProjection(t.Context(), unidentified,
		authz.NewDisclosureSet(authz.DiscloseAll)); proj != nil {
		t.Error("DiscloseAll must yield NO projection at all, not a permissive one")
	}
	union := authz.NewDisclosureSet(authz.DiscloseVariables, authz.DiscloseActors,
		authz.DiscloseNotes, authz.DisclosePolicy)
	if proj := httpcore.DisclosingProjection(t.Context(), unidentified, union); proj == nil {
		t.Fatal("the four-category union must still project")
	} else if proj(st).Incidents != nil {
		t.Error("precondition: the union is not expected to restore Incidents")
	}
}

func TestWithholdDefinition(t *testing.T) {
	t.Parallel()

	unidentified := func(context.Context) (authz.Actor, error) {
		return authz.Actor{}, httpcore.ErrUnauthenticated
	}
	identifiedFn := func(context.Context) (authz.Actor, error) {
		return authz.Actor{ID: "alice"}, nil
	}

	cases := []struct {
		name    string
		resolve httpcore.RequestActorFunc
		set     authz.DisclosureSet
		want    bool
	}{
		{"unidentified, closed", unidentified, nil, true},
		{"unidentified, policy disclosed", unidentified, authz.NewDisclosureSet(authz.DisclosePolicy), false},
		{"identified", identifiedFn, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := httpcore.WithholdDefinition(t.Context(), tc.resolve, tc.set); got != tc.want {
				t.Errorf("WithholdDefinition = %v, want %v", got, tc.want)
			}
		})
	}
}
