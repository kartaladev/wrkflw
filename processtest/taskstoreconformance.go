package processtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

// conformanceReporter is the slice of *testing.T that a conformance check needs:
// testify's assert.TestingT plus the Helper it probes for. Keeping the check
// behind this interface — rather than *testing.T — is what lets the package's own
// tests assert that a NON-conforming store is caught, by passing a recorder
// instead of a real T. A fake testing.TB is impossible (the interface has an
// unexported method), so this narrower surface is the seam.
//
// Only non-fatal assertions are used behind it: there is no FailNow. That is
// necessary but not sufficient for reporting everything a case finds — the
// REJECTED leg does report every break it finds, but the LEGAL leg stops at
// the first one via a plain early `return` (backlog 47); see
// checkTaskStoreConformance.
type conformanceReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// inboxExpectation names the inbox query that MUST return a legal shape.
//
// It is what stops the not-listed assertions on the rejected leg from passing
// vacuously: a store whose AssignedTo and ClaimableBy always return nothing
// satisfies every one of them for the wrong reason, and passed this entire
// suite before this existed (ADR-0184).
type inboxExpectation int

const (
	// inboxUnset is the zero value and is NEVER correct: it means the author did
	// not decide. TestTaskStoreConformanceCasesCoverBothSides rejects it, so a
	// new legal case cannot silently inherit the weakest contract — which would
	// be this ADR's own vacuity, reintroduced one layer up.
	inboxUnset inboxExpectation = iota
	// inboxNone declares, DELIBERATELY, that neither query is contracted to
	// return this shape: the terminal shapes and the anonymous kiosk claimant,
	// and every rejected shape, where the legal-leg check does not run at all.
	inboxNone
	// inboxAssigned means AssignedTo(the claimant) must return the task. The case
	// MUST carry a Claim with a non-empty Actor.ID; the invariant test pins it.
	inboxAssigned
	// inboxClaimable means ClaimableBy(the probe actor) must return the task.
	inboxClaimable
)

// String makes a failure message name the query rather than an integer.
func (e inboxExpectation) String() string {
	switch e {
	case inboxAssigned:
		return "AssignedTo"
	case inboxClaimable:
		return "ClaimableBy"
	case inboxNone:
		return "none"
	default:
		return "UNSET"
	}
}

// taskStoreConformanceCase is one shape put through a store, together with the
// verdict the [humantask.TaskStore] contract demands for it.
type taskStoreConformanceCase struct {
	// name is the subtest name; it is part of a consumer's test output, so it is
	// stable and descriptive.
	name string
	// why explains the verdict in the failure message.
	why string
	// task is the shape written through Upsert.
	task humantask.HumanTask
	// legal reports whether the shape must be ACCEPTED (and then readable) rather
	// than rejected with ErrInvalidTask.
	legal bool
	// listedBy names the inbox query that MUST return this shape. Only legal
	// shapes carry one; see [inboxExpectation].
	listedBy inboxExpectation
}

// taskStoreConformanceCreatedAt is a fixed timestamp: the suite asserts nothing
// about it, and a fixed value keeps a failure diff free of run-to-run noise.
var taskStoreConformanceCreatedAt = time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)

// taskStoreConformanceProbe is the actor the inbox queries are asked about after
// a rejected write. It is deliberately the actor TWO OF the rejected shapes name
// as their claimant, holding the role their Eligibility grants: a store that
// leaked the row would genuinely return it to THIS actor, so absence is evidence
// rather than a foregone conclusion. (The third rejected shape,
// claimed_without_a_claim_is_rejected, names no claimant at all — see
// checkTaskStoreRejectedTaskIsNotListed's per-shape note.)
var taskStoreConformanceProbe = authz.Actor{ID: "alice", Roles: []string{"manager"}}

// taskStoreConformanceCases returns a fresh case set — one per invalid shape
// [humantask.Validate] rejects, plus the legal controls that stop a store which
// rejects everything from passing. It rebuilds the slice on every call so no two
// runs share a *Claim.
func taskStoreConformanceCases() []taskStoreConformanceCase {
	claim := func(actor authz.Actor) *humantask.Claim {
		return &humantask.Claim{Actor: actor, At: taskStoreConformanceCreatedAt}
	}
	// probeClaim is a claim held by the actor the inbox assertions ask about, so a
	// leaked row would actually be returned to them.
	probeClaim := func() *humantask.Claim { return claim(authz.Actor{ID: taskStoreConformanceProbe.ID}) }
	task := func(id string, state humantask.TaskState, c *humantask.Claim) humantask.HumanTask {
		return humantask.HumanTask{
			TaskID:      id,
			InstanceID:  "wrkflw-conformance-instance",
			NodeID:      "approve",
			State:       state,
			Claim:       c,
			Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
			CreatedAt:   taskStoreConformanceCreatedAt,
		}
	}

	return []taskStoreConformanceCase{
		{
			name:     "claimed_without_a_claim_is_rejected",
			why:      "a Claimed task must carry the Claim naming who holds it",
			task:     task("wrkflw-conformance-claimed-nil-claim", humantask.Claimed, nil),
			legal:    false,
			listedBy: inboxNone, // rejected shapes are covered by the not-listed assertions
		},
		{
			name:     "unclaimed_with_a_claim_is_rejected",
			why:      "an Unclaimed task must not carry a Claim (Unclaimed is also the zero value, so a decode that dropped State lands here)",
			task:     task("wrkflw-conformance-unclaimed-with-claim", humantask.Unclaimed, probeClaim()),
			legal:    false,
			listedBy: inboxNone, // rejected shapes are covered by the not-listed assertions
		},
		{
			name: "out_of_range_state_is_rejected",
			why:  "State must be one of the four declared humantask constants",
			// The claim is what gives this shape an inbox to leak into: a persisted
			// out-of-range row is invisible to ClaimableBy (which returns only
			// Unclaimed rows) but WOULD be returned by AssignedTo.
			task:     task("wrkflw-conformance-unknown-state", humantask.TaskState(99), probeClaim()),
			legal:    false,
			listedBy: inboxNone, // rejected shapes are covered by the not-listed assertions
		},
		{
			name:     "unclaimed_without_a_claim_is_accepted",
			why:      "the shape every task starts in",
			task:     task("wrkflw-conformance-unclaimed", humantask.Unclaimed, nil),
			legal:    true,
			listedBy: inboxClaimable, // Unclaimed + Eligibility.Roles ["manager"], which the probe holds
		},
		{
			name:     "claimed_with_a_claim_is_accepted",
			why:      "the ordinary held shape",
			task:     task("wrkflw-conformance-claimed", humantask.Claimed, claim(authz.Actor{ID: "alice", Roles: []string{"manager"}})),
			legal:    true,
			listedBy: inboxAssigned, // the claimant IS taskStoreConformanceProbe
		},
		{
			name:     "claimed_by_an_empty_kiosk_claimant_is_accepted",
			why:      "ADR-0148 amendment 1 §4's kiosk claimant is anonymous but carries roles; rejecting it would break a blessed shape",
			task:     task("wrkflw-conformance-kiosk", humantask.Claimed, claim(authz.Actor{Roles: []string{"kiosk"}})),
			legal:    true,
			listedBy: inboxNone, // the claimant has no ID; there is no inbox to ask about
		},
		{
			name:     "completed_without_a_claim_is_accepted",
			why:      "Completed is deliberately unconstrained on the claim axis: an immediate manual task completes without one",
			task:     task("wrkflw-conformance-completed", humantask.Completed, nil),
			legal:    true,
			listedBy: inboxNone, // no claim to match AssignedTo, and Completed isn't Unclaimed so ClaimableBy excludes it
		},
		{
			name:     "cancelled_retaining_its_claim_is_accepted",
			why:      "a task cancelled while held keeps its claim as audit",
			task:     task("wrkflw-conformance-cancelled", humantask.Cancelled, claim(authz.Actor{ID: "bob"})),
			legal:    true,
			listedBy: inboxNone, // terminal listing is not contracted; this case makes no assertion about presence in either inbox
		},
	}
}

// checkTaskStoreConformance runs one case against store and reports its
// deviations through t. On the REJECTED leg it never stops early: a store gets
// told about all of its contract breaks in one run. On the LEGAL leg it DOES
// stop at the first break — a plain `return` after the Upsert and the Get
// checks — so a store that fails both sees only the first (backlog 47).
func checkTaskStoreConformance(ctx context.Context, t conformanceReporter, store humantask.TaskStore, c taskStoreConformanceCase) {
	t.Helper()

	err := store.Upsert(ctx, c.task)
	if !c.legal {
		assert.ErrorIsf(t, err, humantask.ErrInvalidTask,
			"Upsert(%s): %s — the task must be rejected with an error wrapping humantask.ErrInvalidTask; got %v",
			c.name, c.why, err)
		_, getErr := store.Get(ctx, c.task.TaskID)
		assert.ErrorIsf(t, getErr, humantask.ErrTaskNotFound,
			"Get(%s) after a rejected Upsert: a rejected write must persist nothing, so Get must return humantask.ErrTaskNotFound; got %v",
			c.name, getErr)
		checkTaskStoreRejectedTaskIsNotListed(ctx, t, store, c)
		return
	}

	if !assert.NoErrorf(t, err, "Upsert(%s): %s — this shape is legal and must be accepted", c.name, c.why) {
		return
	}
	got, getErr := store.Get(ctx, c.task.TaskID)
	if !assert.NoErrorf(t, getErr, "Get(%s) after an accepted Upsert: the task must be readable", c.name) {
		return
	}
	assert.Equalf(t, c.task.State, got.State, "Get(%s): State must round-trip", c.name)
	assert.Equalf(t, c.task.Claim != nil, got.Claim != nil,
		"Get(%s): the presence of a Claim must round-trip — the claim invariant is stated over it", c.name)
	checkTaskStoreAcceptedTaskIsListed(ctx, t, store, c)
}

// checkTaskStoreRejectedTaskIsNotListed asserts that a rejected write reached
// neither inbox query. Get alone cannot establish that: a store that writes
// first and validates afterwards can hide the row from Get — or filter Get
// differently from its list queries — while AssignedTo and ClaimableBy still
// return it. That leaked row is the double-listing shape ADR-0183 exists to
// close, where one task is offered to a claimant AND to everyone eligible to
// claim it.
//
// ⚠ Which query can discriminate differs per shape, and neither is redundant:
//   - Unclaimed carrying a claim → BOTH would list it (the double listing);
//   - an out-of-range state carrying a claim → AssignedTo only, since
//     ClaimableBy returns Unclaimed rows;
//   - Claimed with no claim → NEITHER, there being no claimant to match and the
//     row not being Unclaimed; for that shape the Get assertion is the sole
//     discriminator.
//
// Both queries are still asked for every shape: a store is free to filter more
// loosely than the contract, and that is precisely what this catches.
func checkTaskStoreRejectedTaskIsNotListed(ctx context.Context, t conformanceReporter, store humantask.TaskStore, c taskStoreConformanceCase) {
	t.Helper()

	assigned, err := store.AssignedTo(ctx, taskStoreConformanceProbe.ID)
	if assert.NoErrorf(t, err, "AssignedTo(%s) after a rejected Upsert: the query must still answer; got %v", c.name, err) {
		assert.NotContainsf(t, taskStoreConformanceIDs(assigned), c.task.TaskID,
			"AssignedTo(%s) after a rejected Upsert: a rejected write must persist nothing, so the task must not reach the claimant's inbox either", c.name)
	}

	claimable, err := store.ClaimableBy(ctx, taskStoreConformanceProbe)
	if assert.NoErrorf(t, err, "ClaimableBy(%s) after a rejected Upsert: the query must still answer; got %v", c.name, err) {
		assert.NotContainsf(t, taskStoreConformanceIDs(claimable), c.task.TaskID,
			"ClaimableBy(%s) after a rejected Upsert: a rejected write must persist nothing, so the task must not reach the claimable inbox either", c.name)
	}
}

// checkTaskStoreAcceptedTaskIsListed asserts that an accepted task reaches the
// inbox its shape belongs in.
//
// Without it the not-listed assertions on the rejected leg are VACUOUS: a store
// whose AssignedTo and ClaimableBy always return nothing satisfies every one of
// them, and passed this entire suite before ADR-0184. This is the check that
// establishes the queries answer at all, so the negative ones mean something.
//
// Shapes declaring inboxNone are skipped rather than asserted absent: a store may
// list them more loosely than the contract without this check noticing. For
// example MemTaskStore.AssignedTo(bob) DOES return the accepted
// cancelled_retaining_its_claim_is_accepted row, an over-listing this suite makes
// no assertion about either way — that is a deliberate non-goal of this check,
// not coverage that exists elsewhere.
func checkTaskStoreAcceptedTaskIsListed(ctx context.Context, t conformanceReporter, store humantask.TaskStore, c taskStoreConformanceCase) {
	t.Helper()

	switch c.listedBy {
	case inboxAssigned:
		// Guard, not an invariant restated: a nil dereference here would surface as
		// a panic naming neither the case nor the misuse. taskStoreConformanceCase
		// is unexported and the case set is fixed at compile time, so no consumer
		// of this EXPORTED API can ever supply a bad declaration — the case-set
		// invariant test already catches one when it is written. This guard is
		// defence in depth against a future in-repo edit, not consumer misuse.
		if !assert.NotNilf(t, c.task.Claim,
			"case %q declares listedBy=inboxAssigned but carries no Claim: there is no claimant to ask AssignedTo about", c.name) {
			return
		}
		if !assert.NotEmptyf(t, c.task.Claim.Actor.ID,
			"case %q declares listedBy=inboxAssigned but the Claim carries an empty Actor.ID: there is no claimant to ask AssignedTo about", c.name) {
			return
		}
		assigned, err := store.AssignedTo(ctx, c.task.Claim.Actor.ID)
		if assert.NoErrorf(t, err, "AssignedTo(%s) on an accepted task: the query must answer; got %v", c.name, err) {
			assert.Containsf(t, taskStoreConformanceIDs(assigned), c.task.TaskID,
				"AssignedTo(%s): an accepted task must reach its claimant's inbox — a store that lists nothing "+
					"would otherwise satisfy every not-listed assertion vacuously", c.name)
		}
	case inboxClaimable:
		claimable, err := store.ClaimableBy(ctx, taskStoreConformanceProbe)
		if assert.NoErrorf(t, err, "ClaimableBy(%s) on an accepted task: the query must answer; got %v", c.name, err) {
			assert.Containsf(t, taskStoreConformanceIDs(claimable), c.task.TaskID,
				"ClaimableBy(%s): an accepted Unclaimed task the actor is eligible for must reach the claimable "+
					"inbox — a store that lists nothing would otherwise satisfy every not-listed assertion vacuously", c.name)
		}
	case inboxUnset, inboxNone:
	}
}

// taskStoreConformanceIDs projects tasks to their IDs, so a failure prints the
// listed IDs rather than whole records.
func taskStoreConformanceIDs(tasks []humantask.HumanTask) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.TaskID)
	}
	return ids
}

// RunTaskStoreConformance verifies that a [humantask.TaskStore] implementation
// upholds the contract documented on TaskStore.Upsert (ADR-0183): every task
// failing [humantask.Validate] is rejected with [humantask.ErrInvalidTask], and a
// rejected write persists nothing — neither readable through Get nor listed by
// AssignedTo or ClaimableBy, the pair of queries that would otherwise offer one
// contradictory row to its claimant and to every eligible claimant at once.
// AssignedTo and ClaimableBy must ANSWER WITHOUT ERROR for a rejected shape too
// — a store whose list queries error fails the rejected leg twice, once per
// query, because "the query must still answer" is asserted before "and it must
// not list this row".
//
// It also asserts the other half of that contract — the shapes a store must
// ACCEPT and read back, including ADR-0148 amendment 1 §4's kiosk claim (Claimed
// with a claimant carrying roles but no ID), and the Completed/Cancelled shapes
// that are deliberately unconstrained on the claim axis. A store that rejects
// everything therefore cannot pass.
//
// Since ADR-0184, acceptance is not the whole story: two of the accepted
// controls must also reach the inbox their shape belongs in. The Unclaimed
// control must be returned by ClaimableBy for an actor holding the "manager"
// role (the role its Eligibility grants), and the Claimed control by
// AssignedTo(its claimant). A store whose list queries always return nothing —
// conforming on every other check, including the rejected leg's not-listed
// assertions, which such a store satisfies vacuously — now FAILS these two
// controls, where before ADR-0184 it passed the whole suite. Over-listing —
// returning a shape from a broader query than the contract requires — is a
// deliberate NON-GOAL: this helper asserts presence where the contract demands
// it and absence only for a rejected write, never absence for an accepted shape
// that simply isn't required to reach a given inbox.
//
// Adopting ADR-0183 is a SILENT break for a consumer's own TaskStore: the
// interface signature is unchanged, so nothing recompiles differently and a
// non-conforming store keeps accepting contradictory rows. This helper is how a
// consumer finds out.
//
// newStore must return a fresh, empty store on each call; it is invoked once per
// case so no case can observe another's rows. Consumers embedding wrkflw with
// their own TaskStore should call this from their own test suite:
//
//	func TestMyStoreConformance(t *testing.T) {
//		processtest.RunTaskStoreConformance(t, func(t *testing.T) humantask.TaskStore {
//			return mystore.New(newTestDB(t))
//		})
//	}
//
// The *testing.T handed to newStore is the CASE's, not the one passed here: a
// factory that cannot provision its store reports that failure against the case
// it broke, and any t.Cleanup its provisioning registers (a database handle, a
// temporary directory) is released when that case ends rather than when the whole
// suite does. A factory closing over the outer T instead would call FailNow on it
// from the case's goroutine, which the testing package does not support — the
// setup error is replaced by "test executed panic(nil) or runtime.Goexit" and the
// remaining shapes never run.
//
// Each case runs as a named subtest, so a failure names the shape that broke.
func RunTaskStoreConformance(t *testing.T, newStore func(t *testing.T) humantask.TaskStore) {
	t.Helper()

	if newStore == nil {
		t.Fatal("processtest: RunTaskStoreConformance requires a non-nil newStore")
	}

	for _, c := range taskStoreConformanceCases() {
		t.Run(c.name, func(t *testing.T) {
			store := newStore(t)
			if store == nil {
				t.Fatal("processtest: newStore returned a nil humantask.TaskStore")
			}
			checkTaskStoreConformance(t.Context(), t, store, c)
		})
	}
}
