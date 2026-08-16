package engine_test

// state_recent_compensation_cmd_ids_test.go — ADR-0179 Decision 5: the bounded
// ring of recently dispatched compensation command ids.
//
// Three properties are pinned here, none of them covered by any pre-existing
// test:
//
//   - cloneState must deep-copy the ring. Measured before this file existed:
//     with the field added and deliberately left aliased,
//     TestStepDoesNotMutateInput passes and so does the whole ./engine package —
//     its fixture builds no compensation cursor and asserts nothing about
//     Compensating, so it is structurally incapable of observing the aliasing.
//     TestCloneStateDeepCopiesRecentCompensationCmdIDs is therefore the ONLY
//     gate, and it is mutation-verified.
//
//   - Every compensationInvoke dispatch site must append to the ring. The site
//     set is DERIVED from the package's own sources rather than compared against
//     a literal: four sites exist today and ADR-0179's automatic retry adds a
//     fifth, so any integer written here would be stale on arrival. This exact
//     counting failure has already shipped twice in this decision's history.
//
//   - The ring is BOUNDED at K = 16 and evicts the OLDEST. Mutation-verified:
//     with the eviction block deleted, a 20-record walk leaves 20 ids.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// TestCloneStateDeepCopiesRecentCompensationCmdIDs asserts that Clone (via
// cloneState) produces an independently allocated RecentCompensationCmdIDs ring.
//
// The aliasing this prevents is real and was executed: two clones of one base
// append into the same backing slot, one dispatched id silently vanishes, and
// the ErrTokenNotFound → 422 this decision closes returns non-deterministically.
// The second assertion below reproduces exactly that two-clone shape, which a
// plain "mutate index 0" check would miss when the copy line is present but the
// capacity is shared.
func TestCloneStateDeepCopiesRecentCompensationCmdIDs(t *testing.T) {
	st := engine.InstanceState{
		InstanceID:               "rc-1",
		RecentCompensationCmdIDs: []string{"c1"},
	}

	clone := st.Clone()
	require.Len(t, clone.RecentCompensationCmdIDs, 1,
		"control: the clone must carry the ring at all, or both assertions below are vacuous")

	// In-place write through the clone must not reach the original.
	clone.RecentCompensationCmdIDs[0] = "MUTATED"
	assert.Equal(t, "c1", st.RecentCompensationCmdIDs[0],
		"RecentCompensationCmdIDs aliased — cloneState must deep-copy it")

	// Two clones of ONE base must not append into the same backing slot. This is
	// the production shape: cloneState runs per Step, and a shared spare capacity
	// makes the second append overwrite the first.
	base := engine.InstanceState{
		InstanceID:               "rc-2",
		RecentCompensationCmdIDs: make([]string, 1, 4),
	}
	base.RecentCompensationCmdIDs[0] = "c1"

	a := base.Clone()
	b := base.Clone()
	a.RecentCompensationCmdIDs = append(a.RecentCompensationCmdIDs, "fromA")
	b.RecentCompensationCmdIDs = append(b.RecentCompensationCmdIDs, "fromB")

	assert.Equal(t, []string{"c1", "fromA"}, a.RecentCompensationCmdIDs,
		"clone A lost its append — the two clones share a backing array")
	assert.Equal(t, []string{"c1", "fromB"}, b.RecentCompensationCmdIDs)
}

// TestDispatchedCmdIDsAreDerivedFromEverySite asserts that every function in the
// engine package that dispatches a compensation action (i.e. calls
// compensationInvoke) also records that dispatch in the ring.
//
// The site set is DERIVED by parsing the package's own non-test sources. No
// count is written down: the assertion is a per-site implication, so the fifth
// dispatch site ADR-0179 adds is covered the moment it is written, and this test
// fails if it is added without its ring append.
func TestDispatchedCmdIDsAreDerivedFromEverySite(t *testing.T) {
	const (
		dispatchFunc = "compensationInvoke"
		recordFunc   = "recordCompensationDispatch"
	)

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, perr, "parsing %s", name)
		files = append(files, f)
	}
	require.NotEmpty(t, files, "control: the engine package's own sources must be parseable")

	// dispatchers maps "<file>:<func>" → whether that function also records the
	// dispatch. Derived, never enumerated.
	dispatchers := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == dispatchFunc {
				continue // skip compensationInvoke's own declaration
			}
			var dispatches, records bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := call.Fun.(type) {
				case *ast.Ident:
					if callee.Name == dispatchFunc {
						dispatches = true
					}
				case *ast.SelectorExpr:
					if callee.Sel.Name == recordFunc {
						records = true
					}
				}
				return true
			})
			if dispatches {
				pos := fset.Position(fn.Pos())
				dispatchers[filepath.Base(pos.Filename)+":"+fn.Name.Name] = records
			}
		}
	}

	require.NotEmpty(t, dispatchers,
		"control: no %s call site was derived — the scan is broken, not the code", dispatchFunc)

	for site, records := range dispatchers {
		assert.True(t, records,
			"%s dispatches a compensation action but never calls %s — a late reply to "+
				"that command id will still be rejected as ErrTokenNotFound", site, recordFunc)
	}
}

// manyCompensableDef returns a linear process with n compensable service tasks
// followed by a user task that keeps the instance alive, so a CancelRequested
// has an n-record compensation walk to drain.
func manyCompensableDef(n int) *model.ProcessDefinition {
	def := &model.ProcessDefinition{ID: "p-ring-bound", Version: 1}
	def.Nodes = append(def.Nodes, event.NewStart("start"))
	prev := "start"
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("svc%02d", i)
		def.Nodes = append(def.Nodes, activity.NewServiceTask(id,
			activity.WithTaskAction("do"+id), activity.WithCompensateAction("undo"+id)))
		def.Flows = append(def.Flows, flow.SequenceFlow{
			ID: "f-" + prev, Source: prev, Target: id,
		})
		prev = id
	}
	def.Nodes = append(def.Nodes, activity.NewUserTask("park"), event.NewEnd("end"))
	def.Flows = append(def.Flows,
		flow.SequenceFlow{ID: "f-park", Source: prev, Target: "park"},
		flow.SequenceFlow{ID: "f-end", Source: "park", Target: "end"},
	)
	return def
}

// TestRecentCompensationCmdIDsRingIsBounded pins the K = 16 bound as OBSERVABLE
// behaviour, not just a constant.
//
// The bound is not cosmetic. ADR-0175's operator verb retryStalledCompensation
// sets a fresh ActiveCmdID per invocation with no cap, and the whole instance
// state is re-marshalled every Step, so an unbounded slice grows without limit
// under repeated operator retries. Measured with the eviction block deleted, this
// walk of 20 records leaves 20 ids in the ring; with it, 16.
//
// It also covers the eviction branch of recordCompensationDispatch, which no
// other test in the package reaches — every other fixture drains at most two
// records.
func TestRecentCompensationCmdIDsRingIsBounded(t *testing.T) {
	const records = 20 // > K, so eviction must happen

	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	def := manyCompensableDef(records)

	// Complete every forward activity.
	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-ring"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	for i := 1; i <= records; i++ {
		cmdID := invokeIDForAction(res.Commands, fmt.Sprintf("dosvc%02d", i))
		require.NotEmpty(t, cmdID, "control: svc%02d must have been dispatched", i)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i)*time.Second), cmdID, nil),
			engine.StepOptions{})
		require.NoError(t, err)
	}
	require.Equal(t, engine.StatusRunning, res.State.Status,
		"control: the user task must keep the instance alive")

	// Cancel, then drain the whole compensation walk.
	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCancelRequested(at.Add(time.Hour)), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status,
		"control: the cancel must have started a compensation walk")

	var dispatched []string
	for step := 0; step < records; step++ {
		cmdID := firstCompensationCmdID(res.Commands)
		if cmdID == "" {
			break
		}
		dispatched = append(dispatched, cmdID)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(2*time.Hour+time.Duration(step)*time.Second), cmdID, nil),
			engine.StepOptions{})
		require.NoError(t, err)
	}
	require.Len(t, dispatched, records,
		"control: the walk must have dispatched every record, or the ring never overflows")

	ring := res.State.RecentCompensationCmdIDs
	assert.Len(t, ring, 16,
		"the ring must keep the last maxRecentCompensationCmdIDs ids and no more")
	assert.Equal(t, dispatched[records-16:], ring,
		"the ring must hold the MOST RECENT ids — evicting the newest instead of "+
			"the oldest would drop exactly the replies most likely to be redelivered")
	assert.NotContains(t, ring, dispatched[0],
		"the oldest dispatch must have been evicted")
}

// firstCompensationCmdID returns the CommandID of the first pending
// InvokeAction whose Name starts with "undo", or "" when none is pending.
func firstCompensationCmdID(cmds []engine.Command) string {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && !ia.FireAndForget &&
			strings.HasPrefix(ia.Name, "undo") {
			return ia.CommandID
		}
	}
	return ""
}
