package monitor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
)

// stubCallLineageReader is an in-test stub for CallLineageReader.
type stubCallLineageReader struct {
	parentOf   func(ctx context.Context, childID string) (*kernel.CallLink, error)
	childrenOf func(ctx context.Context, parentID string) ([]kernel.CallLink, error)
}

func (s *stubCallLineageReader) ParentOf(ctx context.Context, childID string) (*kernel.CallLink, error) {
	return s.parentOf(ctx, childID)
}
func (s *stubCallLineageReader) ChildrenOf(ctx context.Context, parentID string) ([]kernel.CallLink, error) {
	return s.childrenOf(ctx, parentID)
}

// stubChainLineageReader is an in-test stub for ChainLineageReader.
type stubChainLineageReader struct {
	predecessorOf func(ctx context.Context, successorID string) (*kernel.ChainLink, error)
	successorsOf  func(ctx context.Context, predecessorID string) ([]kernel.ChainLink, error)
}

func (s *stubChainLineageReader) PredecessorOf(ctx context.Context, successorID string) (*kernel.ChainLink, error) {
	return s.predecessorOf(ctx, successorID)
}
func (s *stubChainLineageReader) SuccessorsOf(ctx context.Context, predecessorID string) ([]kernel.ChainLink, error) {
	return s.successorsOf(ctx, predecessorID)
}

// TestNewLineageReaderFailsFast asserts the constructor rejects nil calls or
// chains with ErrNilDependency, and succeeds when both are non-nil.
func TestNewLineageReaderFailsFast(t *testing.T) {
	t.Parallel()

	validCalls := &stubCallLineageReader{
		parentOf:   func(_ context.Context, _ string) (*kernel.CallLink, error) { return nil, nil },
		childrenOf: func(_ context.Context, _ string) ([]kernel.CallLink, error) { return nil, nil },
	}
	validChains := &stubChainLineageReader{
		predecessorOf: func(_ context.Context, _ string) (*kernel.ChainLink, error) { return nil, nil },
		successorsOf:  func(_ context.Context, _ string) ([]kernel.ChainLink, error) { return nil, nil },
	}

	type testCase struct {
		name   string
		calls  kernel.CallLineageReader
		chains kernel.ChainLineageReader
		assert func(t *testing.T, r *monitor.LineageReader, err error)
	}
	cases := []testCase{
		{
			name:   "nil calls",
			calls:  nil,
			chains: validChains,
			assert: func(t *testing.T, r *monitor.LineageReader, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, r)
			},
		},
		{
			name:   "nil chains",
			calls:  validCalls,
			chains: nil,
			assert: func(t *testing.T, r *monitor.LineageReader, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, r)
			},
		},
		{
			name:   "valid args",
			calls:  validCalls,
			chains: validChains,
			assert: func(t *testing.T, r *monitor.LineageReader, err error) {
				require.NoError(t, err)
				require.NotNil(t, r)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := monitor.NewLineageReader(tc.calls, tc.chains)
			tc.assert(t, r, err)
		})
	}
}

// TestLineageReader_Lineage exercises the LineageReader assembler.
func TestLineageReader_Lineage(t *testing.T) {
	t.Parallel()

	t.Run("full lineage: parent + children + predecessor + successors", func(t *testing.T) {
		t.Parallel()
		now := time.Now().UTC()

		calls := &stubCallLineageReader{
			parentOf: func(_ context.Context, childID string) (*kernel.CallLink, error) {
				if childID == "inst-A" {
					return &kernel.CallLink{
						ChildInstanceID:  "inst-A",
						ParentInstanceID: "inst-parent",
						ParentDefID:      "def-parent",
						ParentDefVersion: 2,
						Depth:            1,
					}, nil
				}
				return nil, nil
			},
			childrenOf: func(_ context.Context, parentID string) ([]kernel.CallLink, error) {
				if parentID == "inst-A" {
					return []kernel.CallLink{
						{
							ChildInstanceID:  "inst-child-1",
							ParentInstanceID: "inst-A",
							ParentDefID:      "def-A",
							ParentDefVersion: 1,
							Depth:            2,
						},
					}, nil
				}
				return []kernel.CallLink{}, nil
			},
		}
		chains := &stubChainLineageReader{
			predecessorOf: func(_ context.Context, successorID string) (*kernel.ChainLink, error) {
				if successorID == "inst-A" {
					return &kernel.ChainLink{
						PredecessorID:            "inst-pred",
						SuccessorID:              "inst-A",
						Outcome:                  kernel.OutcomeCompleted,
						SuccessorDefinitionRef:   "def-A:1",
						PredecessorDefinitionRef: "def-pred:1",
						CreatedAt:                now,
					}, nil
				}
				return nil, nil
			},
			successorsOf: func(_ context.Context, predecessorID string) ([]kernel.ChainLink, error) {
				if predecessorID == "inst-A" {
					return []kernel.ChainLink{
						{
							PredecessorID:            "inst-A",
							SuccessorID:              "inst-succ-1",
							Outcome:                  kernel.OutcomeCompleted,
							SuccessorDefinitionRef:   "def-succ:1",
							PredecessorDefinitionRef: "def-A:1",
							CreatedAt:                now,
						},
					}, nil
				}
				return []kernel.ChainLink{}, nil
			},
		}

		reader := mustLineageReader(t, calls, chains)
		lin, err := reader.Lineage(t.Context(), "inst-A")
		require.NoError(t, err)

		assert.Equal(t, "inst-A", lin.InstanceID)

		// Call parent.
		require.NotNil(t, lin.CallParent)
		assert.Equal(t, "inst-parent", lin.CallParent.InstanceID)
		assert.Equal(t, "def-parent", lin.CallParent.DefID)
		assert.Equal(t, 2, lin.CallParent.DefVersion)
		assert.Equal(t, 1, lin.CallParent.Depth)

		// Call children: InstanceID and Depth are set; DefID/DefVersion are
		// intentionally empty because wrkflw_call_links only records the parent
		// definition — the child's own def is not stored there. An operator must
		// fetch the child's snapshot to learn its definition.
		require.Len(t, lin.CallChildren, 1)
		assert.Equal(t, "inst-child-1", lin.CallChildren[0].InstanceID)
		assert.Empty(t, lin.CallChildren[0].DefID,
			"child ref DefID must be empty: call_links only records the parent def")
		assert.Zero(t, lin.CallChildren[0].DefVersion,
			"child ref DefVersion must be zero: call_links only records the parent def")
		assert.Equal(t, 2, lin.CallChildren[0].Depth)

		// Chain predecessor.
		require.NotNil(t, lin.ChainPredecessor)
		assert.Equal(t, "inst-pred", lin.ChainPredecessor.InstanceID)
		assert.Equal(t, "def-pred:1", lin.ChainPredecessor.DefinitionRef)
		assert.Equal(t, string(kernel.OutcomeCompleted), lin.ChainPredecessor.Outcome)

		// Chain successors.
		require.Len(t, lin.ChainSuccessors, 1)
		assert.Equal(t, "inst-succ-1", lin.ChainSuccessors[0].InstanceID)
		assert.Equal(t, "def-succ:1", lin.ChainSuccessors[0].DefinitionRef)
		assert.Equal(t, string(kernel.OutcomeCompleted), lin.ChainSuccessors[0].Outcome)
	})

	t.Run("top-level instance: nil call parent and nil chain predecessor", func(t *testing.T) {
		t.Parallel()
		calls := &stubCallLineageReader{
			parentOf:   func(_ context.Context, _ string) (*kernel.CallLink, error) { return nil, nil },
			childrenOf: func(_ context.Context, _ string) ([]kernel.CallLink, error) { return []kernel.CallLink{}, nil },
		}
		chains := &stubChainLineageReader{
			predecessorOf: func(_ context.Context, _ string) (*kernel.ChainLink, error) { return nil, nil },
			successorsOf:  func(_ context.Context, _ string) ([]kernel.ChainLink, error) { return []kernel.ChainLink{}, nil },
		}

		reader := mustLineageReader(t, calls, chains)
		lin, err := reader.Lineage(t.Context(), "root-inst")
		require.NoError(t, err)

		assert.Equal(t, "root-inst", lin.InstanceID)
		assert.Nil(t, lin.CallParent)
		assert.Nil(t, lin.ChainPredecessor)
		assert.NotNil(t, lin.CallChildren)
		assert.Empty(t, lin.CallChildren)
		assert.NotNil(t, lin.ChainSuccessors)
		assert.Empty(t, lin.ChainSuccessors)
	})

	t.Run("ParentOf read error propagates", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("db is down")
		calls := &stubCallLineageReader{
			parentOf:   func(_ context.Context, _ string) (*kernel.CallLink, error) { return nil, boom },
			childrenOf: func(_ context.Context, _ string) ([]kernel.CallLink, error) { return nil, nil },
		}
		chains := &stubChainLineageReader{
			predecessorOf: func(_ context.Context, _ string) (*kernel.ChainLink, error) { return nil, nil },
			successorsOf:  func(_ context.Context, _ string) ([]kernel.ChainLink, error) { return nil, nil },
		}

		reader := mustLineageReader(t, calls, chains)
		_, err := reader.Lineage(t.Context(), "any")
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("ChildrenOf read error propagates", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("children query failed")
		calls := &stubCallLineageReader{
			parentOf:   func(_ context.Context, _ string) (*kernel.CallLink, error) { return nil, nil },
			childrenOf: func(_ context.Context, _ string) ([]kernel.CallLink, error) { return nil, boom },
		}
		chains := &stubChainLineageReader{
			predecessorOf: func(_ context.Context, _ string) (*kernel.ChainLink, error) { return nil, nil },
			successorsOf:  func(_ context.Context, _ string) ([]kernel.ChainLink, error) { return nil, nil },
		}

		reader := mustLineageReader(t, calls, chains)
		_, err := reader.Lineage(t.Context(), "any")
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("PredecessorOf read error propagates", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("predecessor query failed")
		calls := &stubCallLineageReader{
			parentOf:   func(_ context.Context, _ string) (*kernel.CallLink, error) { return nil, nil },
			childrenOf: func(_ context.Context, _ string) ([]kernel.CallLink, error) { return []kernel.CallLink{}, nil },
		}
		chains := &stubChainLineageReader{
			predecessorOf: func(_ context.Context, _ string) (*kernel.ChainLink, error) { return nil, boom },
			successorsOf:  func(_ context.Context, _ string) ([]kernel.ChainLink, error) { return nil, nil },
		}

		reader := mustLineageReader(t, calls, chains)
		_, err := reader.Lineage(t.Context(), "any")
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("SuccessorsOf read error propagates", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("successors query failed")
		calls := &stubCallLineageReader{
			parentOf:   func(_ context.Context, _ string) (*kernel.CallLink, error) { return nil, nil },
			childrenOf: func(_ context.Context, _ string) ([]kernel.CallLink, error) { return []kernel.CallLink{}, nil },
		}
		chains := &stubChainLineageReader{
			predecessorOf: func(_ context.Context, _ string) (*kernel.ChainLink, error) { return nil, nil },
			successorsOf:  func(_ context.Context, _ string) ([]kernel.ChainLink, error) { return nil, boom },
		}

		reader := mustLineageReader(t, calls, chains)
		_, err := reader.Lineage(t.Context(), "any")
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
	})
}
