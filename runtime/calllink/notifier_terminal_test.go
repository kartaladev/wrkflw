package calllink_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/calllink"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// ── Cross-layer pins ─────────────────────────────────────────────────────────
//
// SubInstanceCompleted and SubInstanceFailed are classified rejectSilently, so
// engine.Step returns (state, nil) — not an error — when the parent instance is
// already terminal. CallNotifier.DrainOnce's idempotency branch keys on
// engine.ErrTokenNotFound, which a nil error does not satisfy; the link is
// therefore marked notified through the SUCCESS arm, not the duplicate arm.
//
// Both arms converge on MarkNotified, so a test that only asserted "marked"
// could not tell them apart. These pins assert the delivery error itself.

// linearTerminalParentDef is a parent definition that completes in a single drive:
// start → end. Driving it yields a StatusCompleted instance, which is the
// terminal parent the notifier must still mark notified.
func linearTerminalParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "terminal-parent",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("tp-start"),
			event.NewEnd("tp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "tpf1", Source: "tp-start", Target: "tp-end"},
		},
	}
}

// stubCallLinkStore serves one pending notification and records every
// MarkNotified call. Only ClaimPending and MarkNotified are exercised by
// DrainOnce; the read-side methods are never reached from that path.
type stubCallLinkStore struct {
	pending []kernel.PendingNotify

	mu     sync.Mutex
	marked []string
}

func (s *stubCallLinkStore) ClaimPending(_ context.Context, limit int) ([]kernel.PendingNotify, error) {
	if limit < len(s.pending) {
		return s.pending[:limit], nil
	}
	return s.pending, nil
}

func (s *stubCallLinkStore) MarkNotified(_ context.Context, childInstanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.marked = append(s.marked, childInstanceID)
	return nil
}

func (s *stubCallLinkStore) markedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.marked...)
}

func (s *stubCallLinkStore) LookupChild(context.Context, string) (kernel.CallLink, bool, error) {
	return kernel.CallLink{}, false, kernel.ErrNoCallLink
}

func (s *stubCallLinkStore) ListRunningChildren(context.Context, string) ([]kernel.CallLink, error) {
	return nil, nil
}

// TestCallNotifierMarksLinkNotifiedWhenParentIsTerminal pins the cross-layer
// consequence of classifying SubInstanceCompleted / SubInstanceFailed as
// rejectSilently: a terminal parent absorbs the resume trigger with a
// nil error, so DrainOnce takes its SUCCESS arm and the durable link stops being
// re-claimed. Were either trigger reclassified to rejectWithError, delivery
// would return engine.ErrInstanceTerminal — not engine.ErrTokenNotFound — and
// the notifier would skip the link forever, redelivering on every poll.
//
// The "transient delivery failure" case is the discriminator: it proves the
// notified/marked assertions genuinely distinguish a delivered link from a
// skipped one, rather than passing for any outcome.
func TestCallNotifierMarksLinkNotifiedWhenParentIsTerminal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// outcome is the child's terminal result: Completed selects
		// SubInstanceCompleted, otherwise SubInstanceFailed.
		outcome kernel.CallOutcome
		// deliverErr, when non-nil, short-circuits delivery with that error
		// instead of applying the trigger to the real driver.
		deliverErr error
		assert     func(t *testing.T, notified int, err error, deliveryErrs []error, marked []string)
	}

	cases := []testCase{
		{
			name:    "completed child resumes a terminal parent silently",
			outcome: kernel.CallOutcome{Completed: true, Output: map[string]any{"ok": true}},
			assert: func(t *testing.T, notified int, err error, deliveryErrs []error, marked []string) {
				require.NoError(t, err)
				assert.Equal(t, 1, notified, "the link must be counted as notified")
				assert.Equal(t, []string{"child-1"}, marked, "the link must be marked notified")
				require.Len(t, deliveryErrs, 1)
				require.NoError(t, deliveryErrs[0],
					"SubInstanceCompleted is rejectSilently: a terminal parent must absorb it with a nil error")
				assert.False(t, errors.Is(deliveryErrs[0], engine.ErrTokenNotFound),
					"the link must be marked via the SUCCESS arm, not the ErrTokenNotFound idempotency arm")
			},
		},
		{
			name:    "failed child resumes a terminal parent silently",
			outcome: kernel.CallOutcome{Completed: false, Err: "child blew up"},
			assert: func(t *testing.T, notified int, err error, deliveryErrs []error, marked []string) {
				require.NoError(t, err)
				assert.Equal(t, 1, notified)
				assert.Equal(t, []string{"child-1"}, marked)
				require.Len(t, deliveryErrs, 1)
				require.NoError(t, deliveryErrs[0],
					"SubInstanceFailed is rejectSilently: a terminal parent must absorb it with a nil error")
				assert.False(t, errors.Is(deliveryErrs[0], engine.ErrTokenNotFound))
			},
		},
		{
			name:       "transient delivery failure leaves the link claimable",
			outcome:    kernel.CallOutcome{Completed: true},
			deliverErr: errors.New("database unreachable"),
			assert: func(t *testing.T, notified int, err error, _ []error, marked []string) {
				require.NoError(t, err)
				assert.Equal(t, 0, notified, "a skipped link must not be counted")
				assert.Empty(t, marked, "a skipped link must stay claimable for the next drain")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := linearTerminalParentDef()

			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			reg := kernel.NewMapDefinitionRegistry(def)

			driver, err := runtime.NewProcessDriver(
				runtime.WithInstanceStore(store),
				runtime.WithDefinitions(reg),
				runtime.WithClock(clockwork.NewFakeClock()),
			)
			require.NoError(t, err)

			// Drive the parent to a terminal status BEFORE the notifier runs.
			parent, err := driver.Drive(ctx, def, "parent-1", nil)
			require.NoError(t, err)
			require.True(t, parent.Status.IsTerminal(),
				"precondition: the parent must be terminal, got %v", parent.Status)

			cl := &stubCallLinkStore{
				pending: []kernel.PendingNotify{{
					Link: kernel.CallLink{
						ChildInstanceID:  "child-1",
						ParentInstanceID: "parent-1",
						ParentCommandID:  "parent-1-c1",
						ParentDefID:      def.ID,
						ParentDefVersion: def.Version,
					},
					Outcome: tc.outcome,
				}},
			}

			var (
				mu           sync.Mutex
				deliveryErrs []error
			)
			deliver := calllink.CallDeliverFunc(func(ctx context.Context, d *model.ProcessDefinition, id string, trg engine.Trigger) error {
				if tc.deliverErr != nil {
					return tc.deliverErr
				}
				_, applyErr := driver.ApplyTrigger(ctx, d, id, trg)
				mu.Lock()
				deliveryErrs = append(deliveryErrs, applyErr)
				mu.Unlock()
				return applyErr
			})

			n, err := calllink.NewCallNotifier(cl, deliver, reg)
			require.NoError(t, err)

			notified, drainErr := n.DrainOnce(ctx)

			mu.Lock()
			seen := append([]error(nil), deliveryErrs...)
			mu.Unlock()

			tc.assert(t, notified, drainErr, seen, cl.markedIDs())
		})
	}
}

// TestTerminalSentinelsAreSiblingsNotNested pins the shape of the two terminal
// sentinels relative to the one CallNotifier.DrainOnce keys on.
//
// engine.ErrInstanceTerminal, engine.ErrTaskNotOpen and engine.ErrTokenNotFound
// all wrap engine.ErrInvalidTransition as SIBLINGS. If a later refactor made
// ErrInstanceTerminal wrap ErrTokenNotFound (or the reverse), DrainOnce's
// idempotency branch at notifier.go's `!errors.Is(derr, engine.ErrTokenNotFound)`
// would silently change meaning: a terminal-parent refusal would start being
// treated as "already resumed" and the link would be marked notified on an error
// path. This lives in calllink_test because calllink is the consumer whose
// behaviour depends on the distinction.
func TestTerminalSentinelsAreSiblingsNotNested(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		err    error
		target error
		assert func(t *testing.T, matched bool)
	}

	mustNotMatch := func(reason string) func(*testing.T, bool) {
		return func(t *testing.T, matched bool) {
			assert.False(t, matched, reason)
		}
	}
	mustMatch := func(reason string) func(*testing.T, bool) {
		return func(t *testing.T, matched bool) {
			assert.True(t, matched, reason)
		}
	}

	cases := []testCase{
		{
			name:   "ErrInstanceTerminal does not wrap ErrTokenNotFound",
			err:    engine.ErrInstanceTerminal,
			target: engine.ErrTokenNotFound,
			assert: mustNotMatch("a terminal-instance refusal must never be mistaken for the notifier's already-resumed duplicate"),
		},
		{
			name:   "ErrTokenNotFound does not wrap ErrInstanceTerminal",
			err:    engine.ErrTokenNotFound,
			target: engine.ErrInstanceTerminal,
			assert: mustNotMatch("the two sentinels are siblings; neither nests the other"),
		},
		{
			name:   "ErrTaskNotOpen does not wrap ErrTokenNotFound",
			err:    engine.ErrTaskNotOpen,
			target: engine.ErrTokenNotFound,
			assert: mustNotMatch("a closed-task refusal must not be mistaken for a missing token"),
		},
		{
			name:   "ErrInstanceTerminal does not wrap ErrTaskNotOpen",
			err:    engine.ErrInstanceTerminal,
			target: engine.ErrTaskNotOpen,
			assert: mustNotMatch("the two terminal sentinels are siblings of each other too"),
		},
		{
			name:   "ErrInstanceTerminal wraps ErrInvalidTransition",
			err:    engine.ErrInstanceTerminal,
			target: engine.ErrInvalidTransition,
			assert: mustMatch("service/ and transport/ classify on ErrInvalidTransition; losing this arm drops the 422 to a 500"),
		},
		{
			name:   "ErrTaskNotOpen wraps ErrInvalidTransition",
			err:    engine.ErrTaskNotOpen,
			target: engine.ErrInvalidTransition,
			assert: mustMatch("runtime/task aliases this sentinel; losing the wrap drops RefreshTaskCandidates to a 500"),
		},
		{
			name:   "ErrTokenNotFound wraps ErrInvalidTransition",
			err:    engine.ErrTokenNotFound,
			target: engine.ErrInvalidTransition,
			assert: mustMatch("the pre-existing sibling shares the same classification parent"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, errors.Is(tc.err, tc.target))
		})
	}
}
