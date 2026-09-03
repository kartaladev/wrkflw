package processtest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

// recorderT captures what a conformance check reports instead of failing the
// enclosing test. It is how the negative legs below assert "a non-conforming
// store IS caught" without crashing this suite: [checkTaskStoreConformance]
// reports through the narrow [conformanceReporter] surface (testify's
// assert.TestingT plus Helper), which *testing.T satisfies and so does this.
//
// A fake testing.TB is impossible — the interface has an unexported method — and
// this repository had no prior recorder to copy, so the reporter seam exists
// precisely to make the negative leg assertable.
type recorderT struct{ failures []string }

func (r *recorderT) Helper() {}

func (r *recorderT) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// listAssignedTo answers AssignedTo over rows with the semantics
// [humantask.TaskStore] documents: the tasks whose claim names actorID, and
// nothing at all for an empty actorID (it identifies no actor, it is not a
// wildcard). The stand-ins below share it so their inboxes report what a real
// store's would — a stub returning nil could not show a leaked row.
func listAssignedTo(rows []humantask.HumanTask, actorID string) []humantask.HumanTask {
	if actorID == "" {
		return nil
	}
	var out []humantask.HumanTask
	for _, row := range rows {
		if row.Claim != nil && row.Claim.Actor.ID == actorID {
			out = append(out, row)
		}
	}
	return out
}

// listClaimableBy answers ClaimableBy over rows: Unclaimed tasks the actor is
// eligible for, by candidacy or by sharing a role with Eligibility.Roles.
func listClaimableBy(rows []humantask.HumanTask, actor authz.Actor) []humantask.HumanTask {
	var out []humantask.HumanTask
	for _, row := range rows {
		if row.State != humantask.Unclaimed {
			continue
		}
		candidate := slices.ContainsFunc(row.Candidates, func(a authz.Actor) bool {
			return a.ID != "" && a.ID == actor.ID
		})
		eligible := slices.ContainsFunc(row.Eligibility.Roles, func(r string) bool {
			return slices.Contains(actor.Roles, r)
		})
		if candidate || eligible {
			out = append(out, row)
		}
	}
	return out
}

// permissiveTaskStore validates nothing: it stores and returns whatever it is
// given. It stands in for a consumer's store written before the claim
// invariant existed — the SILENT break the conformance suite exists to
// surface.
type permissiveTaskStore struct {
	mu sync.Mutex
	m  map[string]humantask.HumanTask
}

func newPermissiveTaskStore() *permissiveTaskStore {
	return &permissiveTaskStore{m: make(map[string]humantask.HumanTask)}
}

// rows returns a snapshot of everything stored, cloned.
func (s *permissiveTaskStore) rows() []humantask.HumanTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]humantask.HumanTask, 0, len(s.m))
	for _, t := range s.m {
		out = append(out, t.Clone())
	}
	return out
}

func (s *permissiveTaskStore) Upsert(_ context.Context, t humantask.HumanTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[t.TaskID] = t.Clone()
	return nil
}

func (s *permissiveTaskStore) Get(_ context.Context, taskID string) (humantask.HumanTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[taskID]
	if !ok {
		return humantask.HumanTask{}, humantask.ErrTaskNotFound
	}
	return t.Clone(), nil
}

// AssignedTo and ClaimableBy answer from the stored rows rather than returning
// nil: a legacy store lists what it accepted, and that is exactly how the
// contradictory row it accepted becomes double-listed.
func (s *permissiveTaskStore) AssignedTo(_ context.Context, actorID string) ([]humantask.HumanTask, error) {
	return listAssignedTo(s.rows(), actorID), nil
}

func (s *permissiveTaskStore) ClaimableBy(_ context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	return listClaimableBy(s.rows(), actor), nil
}

// leakyRollbackTaskStore is the failure mode a Get-only check cannot see: it
// writes first and validates afterwards, so a rejected Upsert does return
// ErrInvalidTask and Get does miss — while the row it never rolled back is still
// returned by the inbox queries. That is the double-listing shape the claim
// invariant exists to close, wearing a conforming store's error behaviour.
type leakyRollbackTaskStore struct {
	*humantask.MemTaskStore

	mu     sync.Mutex
	leaked []humantask.HumanTask
}

func newLeakyRollbackTaskStore() *leakyRollbackTaskStore {
	return &leakyRollbackTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
}

func (s *leakyRollbackTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	if err := humantask.Validate(t); err != nil {
		s.mu.Lock()
		s.leaked = append(s.leaked, t.Clone())
		s.mu.Unlock()
		return err
	}
	return s.MemTaskStore.Upsert(ctx, t)
}

func (s *leakyRollbackTaskStore) rows() []humantask.HumanTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.leaked)
}

func (s *leakyRollbackTaskStore) AssignedTo(ctx context.Context, actorID string) ([]humantask.HumanTask, error) {
	kept, err := s.MemTaskStore.AssignedTo(ctx, actorID)
	if err != nil {
		return nil, err
	}
	return append(kept, listAssignedTo(s.rows(), actorID)...), nil
}

func (s *leakyRollbackTaskStore) ClaimableBy(ctx context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	kept, err := s.MemTaskStore.ClaimableBy(ctx, actor)
	if err != nil {
		return nil, err
	}
	return append(kept, listClaimableBy(s.rows(), actor)...), nil
}

// errInboxUnavailable is what inboxFailingTaskStore's list queries return.
var errInboxUnavailable = errors.New("processtest: the inbox query is unavailable")

// inboxFailingTaskStore upholds the write contract but cannot answer either inbox
// query. It is what makes the checks' "the query must still answer" assertions
// discriminating: a store whose list queries error returns no rows, so a
// not-listed assertion alone would pass it while the store is unusable.
type inboxFailingTaskStore struct{ *humantask.MemTaskStore }

func (inboxFailingTaskStore) AssignedTo(context.Context, string) ([]humantask.HumanTask, error) {
	return nil, errInboxUnavailable
}

func (inboxFailingTaskStore) ClaimableBy(context.Context, authz.Actor) ([]humantask.HumanTask, error) {
	return nil, errInboxUnavailable
}

// blindInboxTaskStore is conforming on the write path and answers BOTH inbox
// queries with nothing. It is the store that motivated the positive half of
// the legal leg: every not-listed assertion on the rejected leg holds
// vacuously for it, so before the legal leg gained a positive expectation
// this store passed the whole suite.
type blindInboxTaskStore struct{ *humantask.MemTaskStore }

func (blindInboxTaskStore) AssignedTo(context.Context, string) ([]humantask.HumanTask, error) {
	return nil, nil
}

func (blindInboxTaskStore) ClaimableBy(context.Context, authz.Actor) ([]humantask.HumanTask, error) {
	return nil, nil
}

// rejectingTaskStore refuses every write. It is the opposite failure mode: a
// store that "passes" the invalid-shape cases by rejecting everything must still
// fail the legal-shape controls.
type rejectingTaskStore struct{}

func (rejectingTaskStore) Upsert(context.Context, humantask.HumanTask) error {
	return fmt.Errorf("%w: this store refuses everything", humantask.ErrInvalidTask)
}

func (rejectingTaskStore) Get(context.Context, string) (humantask.HumanTask, error) {
	return humantask.HumanTask{}, humantask.ErrTaskNotFound
}

// Both inboxes are empty because this store holds nothing — that is the truth
// about it, not a stubbed-out method.
func (rejectingTaskStore) AssignedTo(context.Context, string) ([]humantask.HumanTask, error) {
	return nil, nil
}

func (rejectingTaskStore) ClaimableBy(context.Context, authz.Actor) ([]humantask.HumanTask, error) {
	return nil, nil
}

// writeOnlyTaskStore validates correctly and reports success, but never persists
// anything: every Get misses. It stands in for a store whose Upsert silently
// no-ops — accepted writes that cannot be read back are still a broken store.
type writeOnlyTaskStore struct{}

func (writeOnlyTaskStore) Upsert(_ context.Context, t humantask.HumanTask) error {
	return humantask.Validate(t)
}

func (writeOnlyTaskStore) Get(context.Context, string) (humantask.HumanTask, error) {
	return humantask.HumanTask{}, humantask.ErrTaskNotFound
}

// Nothing was ever persisted, so both inboxes are genuinely empty.
func (writeOnlyTaskStore) AssignedTo(context.Context, string) ([]humantask.HumanTask, error) {
	return nil, nil
}

func (writeOnlyTaskStore) ClaimableBy(context.Context, authz.Actor) ([]humantask.HumanTask, error) {
	return nil, nil
}

// kioskHostileTaskStore is conforming except that it also refuses a claim whose
// claimant has no ID — a clause that was proposed during design and then
// REVERSED, because an anonymous kiosk claim carrying roles is a legal shape.
type kioskHostileTaskStore struct{ *humantask.MemTaskStore }

func (s kioskHostileTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	if t.Claim != nil && t.Claim.Actor.ID == "" {
		return fmt.Errorf("%w: empty claimant", humantask.ErrInvalidTask)
	}
	return s.MemTaskStore.Upsert(ctx, t)
}

// kioskCase returns the kiosk control from the case set: the only legal case
// whose claim carries an empty claimant. Selecting it by shape rather than by
// name keeps the assertion honest if a case is renamed.
func kioskCase(t *testing.T, cases []taskStoreConformanceCase) taskStoreConformanceCase {
	t.Helper()
	found := slices.Collect(func(yield func(taskStoreConformanceCase) bool) {
		for _, c := range cases {
			if c.legal && c.task.Claim != nil && c.task.Claim.Actor.ID == "" {
				if !yield(c) {
					return
				}
			}
		}
	})
	require.Len(t, found, 1, "the case set must carry exactly one legal empty-claimant (kiosk) control")
	return found[0]
}

// inboxLeaks returns how many of the two inbox queries would return an invalid
// case's task if a store persisted it anyway. It is hand-derived per shape from
// the semantics [humantask.TaskStore] documents (AssignedTo: the claim names the
// actor; ClaimableBy: state is Unclaimed AND the actor is eligible), against the
// probe actor the checks use.
//
// ⚠ The reach is NOT uniform, and the asymmetry is the point: no single query
// covers every shape, and one shape is covered by neither.
func inboxLeaks(t *testing.T, c taskStoreConformanceCase) int {
	t.Helper()

	require.False(t, c.legal, "inboxLeaks is about rejected writes; %q is a legal shape", c.name)

	switch c.name {
	case "claimed_without_a_claim_is_rejected":
		// Claimed with no claim: AssignedTo has no claimant to match on, and
		// ClaimableBy only ever returns Unclaimed rows. NEITHER inbox reaches this
		// shape even when it is persisted — Get is its sole discriminator.
		return 0
	case "unclaimed_with_a_claim_is_rejected":
		// The double-listing shape the claim invariant exists to close:
		// AssignedTo matches the claim's alice, and ClaimableBy matches an
		// Unclaimed row whose eligibility names the probe's "manager" role.
		// BOTH fire, on the same row.
		return 2
	case "out_of_range_state_is_rejected":
		// The claim makes AssignedTo match; ClaimableBy is restricted to Unclaimed
		// and state 99 is not that, so only ONE fires.
		return 1
	default:
		t.Fatalf("inboxLeaks: unknown invalid case %q — derive its inbox reach before pinning a count", c.name)
		return 0
	}
}

// TestInboxExpectationString pins inboxExpectation.String() per value, including
// an out-of-range value falling through to the same "UNSET" the zero value
// reports: it is formatted only on assertion failure, so a swapped arm would
// otherwise surface as nothing worse than a misleading message inside a
// consumer's own failing suite.
func TestInboxExpectationString(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		value  inboxExpectation
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:  "inboxAssigned names AssignedTo",
			value: inboxAssigned,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "AssignedTo", got)
			},
		},
		{
			name:  "inboxClaimable names ClaimableBy",
			value: inboxClaimable,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "ClaimableBy", got)
			},
		},
		{
			name:  "inboxNone reports none",
			value: inboxNone,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "none", got)
			},
		},
		{
			name:  "inboxUnset — the zero value — reports UNSET",
			value: inboxUnset,
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "UNSET", got)
			},
		},
		{
			name:  "an out-of-range value falls through to UNSET",
			value: inboxExpectation(99),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "UNSET", got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.value.String())
		})
	}
}

func TestTaskStoreConformanceCasesCoverBothSides(t *testing.T) {
	t.Parallel()

	cases := taskStoreConformanceCases()

	legal := 0
	invalid := 0
	names := make(map[string]struct{}, len(cases))
	for _, c := range cases {
		if c.legal {
			legal++
		} else {
			invalid++
		}
		names[c.name] = struct{}{}
		assert.NotEmptyf(t, c.why, "case %q must explain itself: consumers read these", c.name)
		assert.NotEmptyf(t, c.task.TaskID, "case %q must carry a TaskID", c.name)
		assert.NotEqualf(t, inboxUnset, c.listedBy,
			"case %q must DECIDE its inbox expectation; inboxNone is the explicit \"neither query returns it\"", c.name)
		assert.Truef(t, c.legal || c.listedBy == inboxNone,
			"case %q is rejected, so listedBy=%v is a silent no-op — the check runs only on the legal leg", c.name, c.listedBy)
		if c.listedBy == inboxAssigned {
			if assert.NotNilf(t, c.task.Claim,
				"case %q declares inboxAssigned, so it MUST carry the Claim naming the claimant", c.name) {
				assert.NotEmptyf(t, c.task.Claim.Actor.ID,
					"case %q declares inboxAssigned, but an empty actor id identifies no actor: AssignedTo(\"\") returns nothing", c.name)
			}
		}
		if c.listedBy == inboxClaimable {
			assert.Equalf(t, humantask.Unclaimed, c.task.State,
				"case %q declares inboxClaimable, so it MUST be Unclaimed — ClaimableBy only ever returns Unclaimed rows", c.name)
			candidate := slices.ContainsFunc(c.task.Candidates, func(a authz.Actor) bool {
				return a.ID != "" && a.ID == taskStoreConformanceProbe.ID
			})
			eligible := slices.ContainsFunc(c.task.Eligibility.Roles, func(r string) bool {
				return slices.Contains(taskStoreConformanceProbe.Roles, r)
			})
			assert.Truef(t, candidate || eligible,
				"case %q declares inboxClaimable, but the probe actor %+v is named by neither its Candidates nor its Eligibility.Roles, so ClaimableBy(probe) could never return it", c.name, taskStoreConformanceProbe)
		}
	}

	assert.Len(t, names, len(cases), "case names must be unique: %v", slices.Sorted(maps.Keys(names)))
	assert.GreaterOrEqual(t, invalid, 3, "one case per invalid shape: Claimed+nil, Unclaimed+claim, bad state")
	assert.Positive(t, legal, "without legal controls a store that rejects everything would pass")
	_ = kioskCase(t, cases)
}

// TestCheckTaskStoreConformanceCatchesNonConformingStores is the negative leg.
// For each stand-in store it asserts, per conformance case, whether that case
// must report a failure — proving the suite discriminates rather than merely
// running.
func TestCheckTaskStoreConformanceCatchesNonConformingStores(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		newStore func() humantask.TaskStore
		assert   func(t *testing.T, c taskStoreConformanceCase, failures []string)
	}

	cases := []testCase{
		{
			name:     "a conforming store passes every case",
			newStore: func() humantask.TaskStore { return humantask.NewMemTaskStore() },
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				assert.Emptyf(t, failures, "MemTaskStore conforms, so %q must not report: %v", c.name, failures)
			},
		},
		{
			name:     "a store that validates nothing fails every invalid-shape case",
			newStore: func() humantask.TaskStore { return newPermissiveTaskStore() },
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal {
					assert.Emptyf(t, failures, "a permissive store still accepts legal shapes, so %q must pass: %v", c.name, failures)
					return
				}
				// Two fixed failures — the write was not rejected, AND the unrejected
				// write persisted — plus one per inbox that now lists the row it
				// accepted. Pinning the count keeps every MUST live: a check that
				// dropped the "persists nothing" half, or either inbox half, would
				// still be NotEmpty.
				want := 2 + inboxLeaks(t, c)
				assert.Lenf(t, failures, want,
					"%q must FAIL for a store that validates nothing, on the rejection, the persistence check, and %d inbox quer(y/ies); got %v",
					c.name, inboxLeaks(t, c), failures)
			},
		},
		{
			name:     "a store that rejects everything fails every legal-shape case",
			newStore: func() humantask.TaskStore { return rejectingTaskStore{} },
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal {
					assert.NotEmptyf(t, failures, "%q must FAIL for a store that rejects everything", c.name)
					return
				}
				assert.Emptyf(t, failures, "a rejecting store does reject invalid shapes, so %q must pass: %v", c.name, failures)
			},
		},
		{
			name:     "a store whose accepted writes never persist fails every legal-shape case",
			newStore: func() humantask.TaskStore { return writeOnlyTaskStore{} },
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal {
					assert.Lenf(t, failures, 1,
						"%q must FAIL on the read-back for a store that accepts but never persists; got %v", c.name, failures)
					return
				}
				assert.Emptyf(t, failures, "this store validates correctly, so %q must pass: %v", c.name, failures)
			},
		},
		{
			name: "a store that refuses an empty claimant fails the kiosk control",
			newStore: func() humantask.TaskStore {
				return kioskHostileTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
			},
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				kiosk := kioskCase(t, taskStoreConformanceCases())
				if c.name == kiosk.name {
					assert.NotEmptyf(t, failures, "%q must FAIL: the kiosk claim shape is LEGAL", c.name)
					return
				}
				assert.Emptyf(t, failures, "only the kiosk control may fail here, not %q: %v", c.name, failures)
			},
		},
		{
			// The row that proves the inbox assertions discriminate. This store
			// rejects every invalid shape AND hides it from Get, so it passes both
			// checks a Get-only suite makes — while the row it failed to roll back
			// is still listed. Only a shape an inbox query can actually reach fails
			// here, which is why the expectation is inboxLeaks and not a constant.
			name:     "a store whose rejected write leaks into the inboxes fails only where an inbox can see it",
			newStore: func() humantask.TaskStore { return newLeakyRollbackTaskStore() },
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal {
					assert.Emptyf(t, failures, "legal shapes go to the conforming store underneath, so %q must pass: %v", c.name, failures)
					return
				}
				assert.Lenf(t, failures, inboxLeaks(t, c),
					"%q must FAIL once per inbox query that can reach the leaked row — and Get catches none of it; got %v",
					c.name, failures)
			},
		},
		{
			// Pins the "the query must still answer" halves: without them a store
			// whose inboxes error would pass every rejected-write case, since an
			// errored query returns no rows and "not listed" holds vacuously.
			name:     "a store whose inbox queries error fails every invalid-shape case",
			newStore: func() humantask.TaskStore { return inboxFailingTaskStore{MemTaskStore: humantask.NewMemTaskStore()} },
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal {
					// The legal leg DOES ask an inbox, but only for a shape
					// declaring one; those report the unanswerable query once.
					want := 0
					if c.listedBy == inboxAssigned || c.listedBy == inboxClaimable {
						want = 1
					}
					assert.Lenf(t, failures, want,
						"%q must FAIL once iff it declares an inbox (%v) this store cannot answer; got %v",
						c.name, c.listedBy, failures)
					return
				}
				assert.Lenf(t, failures, 2,
					"%q must FAIL once for each unanswerable inbox query; got %v", c.name, failures)
			},
		},
		{
			// The vacuity the positive inbox expectation closes. This store
			// validates correctly, persists correctly and reads back correctly —
			// it simply never lists anything.
			// Only the legal shapes carrying a positive inbox expectation can catch
			// it; the rejected leg cannot, because "not listed" is exactly what a
			// store that lists nothing does.
			name: "a store whose inboxes never list anything fails only the legal shapes that must be listed",
			newStore: func() humantask.TaskStore {
				return blindInboxTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
			},
			assert: func(t *testing.T, c taskStoreConformanceCase, failures []string) {
				if c.legal && (c.listedBy == inboxAssigned || c.listedBy == inboxClaimable) {
					assert.Lenf(t, failures, 1,
						"%q declares listedBy=%v, so a store that lists nothing must FAIL exactly once; got %v",
						c.name, c.listedBy, failures)
					return
				}
				assert.Emptyf(t, failures,
					"%q has no positive inbox expectation, so a blind-inbox store must pass it: %v", c.name, failures)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, c := range taskStoreConformanceCases() {
				t.Run(c.name, func(t *testing.T) {
					t.Parallel()

					rec := &recorderT{}
					checkTaskStoreConformance(t.Context(), rec, tc.newStore(), c)
					tc.assert(t, c, rec.failures)
				})
			}
		})
	}
}
