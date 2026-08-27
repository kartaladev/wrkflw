package authz

// DisclosureCategory names a class of field an HTTP mount may CHOOSE to disclose to a
// caller the transport could not identify (ADR-0190).
//
// ⚠ This is an ALLOW-list, and the polarity is load-bearing. The zero [DisclosureSet]
// discloses nothing beyond the structural baseline, so a category nobody thought about is
// withheld rather than exposed. An earlier design used the opposite polarity — a deny-list
// of sensitive categories — and it failed open on four separate snapshots of the process
// variables that nobody remembered to list.
//
// Categories are consumed by runtime/view.PublicState, which withholds by CONSTRUCTION:
// it builds a fresh state naming only allow-listed fields, so a field added to the engine
// tomorrow is absent until someone classifies it.
type DisclosureCategory string

const (
	// DiscloseVariables permits process variables and every snapshot of them — the
	// instance variables, the start variables, each token's payload, each task's
	// variable snapshot, and each compensation record's input.
	DiscloseVariables DisclosureCategory = "variables"
	// DiscloseActors permits actor identity and attributes: task candidates, and the
	// actor recorded on a claim or a completion.
	DiscloseActors DisclosureCategory = "actors"
	// DiscloseNotes permits the free-text completion note.
	//
	// ⚠ It is independent of [DiscloseActors] even though the note lives inside the same
	// Completion struct: disclosing who completed a task is not the same decision as
	// disclosing what they wrote about it.
	DiscloseNotes DisclosureCategory = "notes"
	// DisclosePolicy permits authorization policy and routing expressions: the embedded
	// process definition, which carries every node's eligibility spec, and the flow
	// conditions rendered as a task's allowed actions.
	DisclosePolicy DisclosureCategory = "policy"
)

// DisclosureSet is a membership test over categories. Its ZERO VALUE is the closed
// posture, so a nil set is safe and means "disclose nothing".
type DisclosureSet map[DisclosureCategory]struct{}

// NewDisclosureSet widens disclosure to exactly cats.
//
// Calling it with no categories yields the same closed posture as the zero value. The two
// are deliberately indistinguishable: unlike a size or a duration, there is no meaningful
// "unset" that differs from "nothing disclosed", so this needs none of the pointer-or-flag
// machinery that CustomizeConfig.MaxBodyBytes requires.
func NewDisclosureSet(cats ...DisclosureCategory) DisclosureSet {
	s := make(DisclosureSet, len(cats))
	for _, c := range cats {
		s[c] = struct{}{}
	}
	return s
}

// Has reports whether c may be disclosed. A nil receiver discloses nothing.
func (s DisclosureSet) Has(c DisclosureCategory) bool {
	_, ok := s[c]
	return ok
}
