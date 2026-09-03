package engine_test

// terminal_sites_test.go — every terminal transition routes through
// InstanceState.endInstance. That fact was once recorded as a COUNT: "all eight
// sites route through it". The count was 8 when written and is 10 today
// (re-derived from source 2026-08-20), so the prose was false in three places
// and nothing noticed.
//
// A number in prose rots. The invariant does not, so this file pins the
// invariant instead: endInstance is the ONLY function that assigns a terminal
// Status. That is what makes "every terminal site routes through endInstance"
// true, and unlike the count it stays true as sites are added.
//
// ⚠ HONEST FRAMING: this is a PIN, not a red-green fix. It passes the moment it
// is written, because the property already holds. It fails the day someone adds
// a terminal-status assignment outside endInstance — which is exactly the event
// the stale count failed to catch. Mutation-verified (see the delivery's
// evidence file): planting one such assignment turns it red.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

// terminalStatusIdents are the Status constants for which Status.IsTerminal()
// reports true (engine/state.go). Kept as a literal set on purpose: if a fourth
// terminal status is introduced without being added here, that is a review
// question, and a source-derived set would hide it.
var terminalStatusIdents = map[string]bool{
	"StatusCompleted":  true,
	"StatusFailed":     true,
	"StatusTerminated": true,
}

// TestEndInstanceIsTheSoleTerminalStatusWriter asserts no function other than
// endInstance assigns a terminal Status constant to a .Status field anywhere in
// the non-test engine core.
//
// Scope limit, stated rather than glossed: it matches assignments whose
// right-hand side is one of the terminal CONSTANTS by name. endInstance's own
// `s.Status = status` assigns a parameter, which no AST-only check can resolve —
// that is precisely why endInstance must remain the single choke point, and why
// this test brackets it rather than replacing review of it.
func TestEndInstanceIsTheSoleTerminalStatusWriter(t *testing.T) {
	t.Parallel()

	for _, path := range nonTestGoFiles(t, ".") {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name == "endInstance" {
				continue
			}
			for _, pos := range terminalStatusAssignments(fn) {
				t.Errorf("%s:%d: %s assigns a terminal Status directly; every terminal "+
					"transition must route through endInstance",
					path, fset.Position(pos).Line, fn.Name.Name)
			}
		}
	}
}

// TestTerminalStatusWriterDetectorFires is the liveness guard on the check
// above: an assertion that scans real files and finds nothing is
// indistinguishable from a detector that finds nothing ever. It runs the same
// matcher over an in-test source string that DOES assign a terminal status.
func TestTerminalStatusWriterDetectorFires(t *testing.T) {
	t.Parallel()

	const src = `package fixture

func rogue(s *InstanceState) { s.Status = StatusFailed }

func innocent(s *InstanceState) { s.Status = StatusRunning }
`
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	got := map[string]int{}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			got[fn.Name.Name] = len(terminalStatusAssignments(fn))
		}
	}

	assert.Equal(t, 1, got["rogue"], "a direct terminal-status assignment must be reported")
	assert.Equal(t, 0, got["innocent"], "a non-terminal status assignment must not be reported")
}

// terminalStatusAssignments returns the positions of every `<expr>.Status =
// <terminal constant>` assignment in fn.
func terminalStatusAssignments(fn *ast.FuncDecl) []token.Pos {
	var found []token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Status" || i >= len(assign.Rhs) {
				continue
			}
			if rhs, ok := assign.Rhs[i].(*ast.Ident); ok && terminalStatusIdents[rhs.Name] {
				found = append(found, assign.Pos())
			}
		}
		return true
	})
	return found
}
