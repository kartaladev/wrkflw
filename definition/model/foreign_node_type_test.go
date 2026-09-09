package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/internal/kindreg"
)

// This file tests the type gate against SYNTHETIC kinds, so it pins the
// mechanism itself rather than the seventeen real kinds — definition/kinds owns
// that end of it, and cannot live here because model must not import the leaf
// packages. In particular it is the only place the nil-FromWire tolerance is
// exercised directly.

// Synthetic kinds owned by this file. 9998/9999 belong to registry_test.go.
const (
	kindSynthRecorded   = NodeKind(9990) // registered WITH a FromWire
	kindSynthNoFromWire = NodeKind(9991) // registered with ToWire only
)

// synthAlpha and synthBeta are two distinct concrete types that are otherwise
// interchangeable — the gate must separate them on dynamic type alone.
type synthAlpha struct {
	Base

	kind NodeKind
}

func (a synthAlpha) Kind() NodeKind { return a.kind }

type synthBeta struct {
	Base

	kind NodeKind
}

func (b synthBeta) Kind() NodeKind { return b.kind }

// Registration happens at init, not inside a test: nodeRegistry and nodeTypes
// are plain package-level maps, and writing them while a parallel test reads
// them through Validate is a data race the race detector would rightly fail.
func init() {
	RegisterKind(kindreg.Grant(), kindSynthRecorded, NodeSpec{
		Name:     "synthetic-recorded",
		FromWire: func(b Base, _ NodeWire) Node { return synthAlpha{Base: b, kind: kindSynthRecorded} },
		ToWire:   func(Node, *NodeWire) {},
	})
	RegisterKind(kindreg.Grant(), kindSynthNoFromWire, NodeSpec{
		Name:   "synthetic-no-fromwire",
		ToWire: func(Node, *NodeWire) {},
	})
}

// TestForeignNodeTypeGate covers the three states the gate can be in for a given
// kind: the recorded type matches, it does not, or the kind recorded no type at
// all because it has no FromWire to derive one from.
func TestForeignNodeTypeGate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   Node
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "the recorded type is accepted",
			node: synthAlpha{Base: NewBase("n", "n"), kind: kindSynthRecorded},
			assert: func(t *testing.T, err error) {
				assert.NotErrorIs(t, err, ErrForeignNodeType)
			},
		},
		{
			name: "a different type under the same kind is refused",
			node: synthBeta{Base: NewBase("n", "n"), kind: kindSynthRecorded},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrForeignNodeType)
				assert.Contains(t, err.Error(), `"n"`, "the node id locates the offender")
				assert.Contains(t, err.Error(), "synthAlpha", "the registered type is named")
				assert.Contains(t, err.Error(), "synthBeta", "the actual type is named")
			},
		},
		{
			name: "a kind with no FromWire recorded no type, so the gate stays silent",
			// P2: registering the type unconditionally would nil-panic here, and
			// gating on a missing entry is also what keeps an UNREGISTERED kind
			// the business of ErrKindNotRegistered rather than of this check.
			node: synthBeta{Base: NewBase("n", "n"), kind: kindSynthNoFromWire},
			assert: func(t *testing.T, err error) {
				assert.NotErrorIs(t, err, ErrForeignNodeType)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := Validate(&ProcessDefinition{
				ID: "p", Version: 1, Nodes: []Node{tc.node},
			})
			tc.assert(t, err)
		})
	}
}

// TestNodeTypeRecordedOnlyWithFromWire pins the recording rule itself, which the
// gate cases above can only observe indirectly.
func TestNodeTypeRecordedOnlyWithFromWire(t *testing.T) {
	t.Parallel()

	recorded, ok := nodeTypeFor(kindSynthRecorded)
	require.True(t, ok, "a kind with a FromWire must record its concrete type")
	assert.Equal(t, "model.synthAlpha", recorded.String())

	_, ok = nodeTypeFor(kindSynthNoFromWire)
	assert.False(t, ok, "a kind without a FromWire must record nothing")
}
