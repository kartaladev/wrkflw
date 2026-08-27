package authz_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/authz"
)

// TestZeroDisclosureSet_DisclosesNothing pins the closed posture on the ZERO value.
//
// This is the property the whole ADR-0190 allow-list rests on: a category nobody thought
// about is withheld rather than exposed. Revision 1 of that design used the opposite
// polarity (a deny-list), where the zero value disclosed everything.
func TestZeroDisclosureSet_DisclosesNothing(t *testing.T) {
	t.Parallel()

	var zero authz.DisclosureSet
	for _, c := range []authz.DisclosureCategory{
		authz.DiscloseVariables, authz.DiscloseActors,
		authz.DiscloseNotes, authz.DisclosePolicy,
	} {
		if zero.Has(c) {
			t.Errorf("zero DisclosureSet must not disclose %q", c)
		}
	}
}

func TestNewDisclosureSet_WidensExplicitly(t *testing.T) {
	t.Parallel()

	s := authz.NewDisclosureSet(authz.DiscloseVariables)
	if !s.Has(authz.DiscloseVariables) {
		t.Error("explicitly requested category not disclosed")
	}
	if s.Has(authz.DiscloseActors) {
		t.Error("unrequested category disclosed — the set must widen, never default open")
	}
}

// TestNewDisclosureSet_EmptyCallIsStillClosed distinguishes "never configured" from
// "configured with no categories". Both are the closed posture here, which is why the
// transport can carry a plain map rather than the pointer/flag dance MaxBodyBytes needs.
func TestNewDisclosureSet_EmptyCallIsStillClosed(t *testing.T) {
	t.Parallel()

	if authz.NewDisclosureSet().Has(authz.DiscloseVariables) {
		t.Error("NewDisclosureSet() with no categories must disclose nothing")
	}
}
