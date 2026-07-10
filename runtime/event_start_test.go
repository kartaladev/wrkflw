package runtime

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// eventStartTestDefSeq gives each test-built definition a unique ID so cases
// running under t.Parallel() (and any package-global registries they might
// incidentally touch) never collide.
var eventStartTestDefSeq atomic.Int64

// defWithSignalStart builds a minimal *model.ProcessDefinition with defID and
// a single start node ("start") carrying a signal-start on signalName.
func defWithSignalStart(t *testing.T, defID, signalName string) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:    defID,
		Nodes: []model.Node{event.NewStart("start", event.WithSignalName(signalName))},
	}
}

// defWithMessageStart builds a minimal *model.ProcessDefinition with defID and
// a single start node ("start") carrying a message-start on msgName.
func defWithMessageStart(t *testing.T, defID, msgName string) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:    defID,
		Nodes: []model.Node{event.NewStart("start", event.WithMessageCorrelator(msgName, ""))},
	}
}

// defWithTimerStart builds a minimal *model.ProcessDefinition with defID and a
// single start node ("start") carrying a timer-start on trig.
func defWithTimerStart(t *testing.T, defID string, trig schedule.TriggerSpec) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:    defID,
		Nodes: []model.Node{event.NewStart("start", event.WithStartTimer(trig))},
	}
}

// defWithoutStart builds a *model.ProcessDefinition with defID and no start
// node at all (only an end event), for miss-path cases.
func defWithoutStart(t *testing.T, defID string) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:    defID,
		Nodes: []model.Node{event.NewEnd("e")},
	}
}

// versioned returns def with Version set, for building two versions of the same
// def id in latest-per-id enumeration tests.
func versioned(def *model.ProcessDefinition, version int) *model.ProcessDefinition {
	def.Version = version
	return def
}

// TestLatestPerID verifies latestPerID keeps only the highest-Version definition
// per def id, dropping superseded versions while leaving distinct ids intact.
func TestLatestPerID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		defs   []*model.ProcessDefinition
		assert func(t *testing.T, out []*model.ProcessDefinition)
	}

	cases := []testCase{
		{
			name: "nil input yields nil",
			defs: nil,
			assert: func(t *testing.T, out []*model.ProcessDefinition) {
				assert.Empty(t, out)
			},
		},
		{
			name: "two versions of same id collapse to the highest",
			defs: []*model.ProcessDefinition{
				versioned(defWithMessageStart(t, "order", "m1"), 1),
				versioned(defWithMessageStart(t, "order", "m2"), 2),
			},
			assert: func(t *testing.T, out []*model.ProcessDefinition) {
				if assert.Len(t, out, 1) {
					assert.Equal(t, "order", out[0].ID)
					assert.Equal(t, 2, out[0].Version)
				}
			},
		},
		{
			name: "highest wins regardless of input order",
			defs: []*model.ProcessDefinition{
				versioned(defWithMessageStart(t, "order", "m2"), 2),
				versioned(defWithMessageStart(t, "order", "m1"), 1),
			},
			assert: func(t *testing.T, out []*model.ProcessDefinition) {
				if assert.Len(t, out, 1) {
					assert.Equal(t, 2, out[0].Version)
				}
			},
		},
		{
			name: "distinct ids are all retained",
			defs: []*model.ProcessDefinition{
				versioned(defWithMessageStart(t, "a", "m"), 1),
				versioned(defWithMessageStart(t, "b", "m"), 1),
			},
			assert: func(t *testing.T, out []*model.ProcessDefinition) {
				ids := []string{}
				for _, d := range out {
					ids = append(ids, d.ID)
				}
				assert.ElementsMatch(t, []string{"a", "b"}, ids)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := latestPerID(tc.defs)
			tc.assert(t, out)
		})
	}
}

// TestEventStartEnumerationLatestVersionOnly verifies the enumeration helpers
// consider only the latest version per def id: a superseded version's event
// start must neither add duplicate hits (signal/timer) nor create ambiguity
// (message).
func TestEventStartEnumerationLatestVersionOnly(t *testing.T) {
	t.Parallel()

	t.Run("signal enumeration ignores superseded version", func(t *testing.T) {
		t.Parallel()
		defs := []*model.ProcessDefinition{
			versioned(defWithSignalStart(t, "sig-def", "s"), 1),
			versioned(defWithSignalStart(t, "sig-def", "s"), 2),
		}
		hits := signalStartDefs(defs, "s")
		if assert.Len(t, hits, 1, "only the latest version must produce a signal-start hit") {
			assert.Equal(t, 2, hits[0].Def.Version)
		}
	})

	t.Run("timer enumeration ignores superseded version", func(t *testing.T) {
		t.Parallel()
		trig := schedule.AfterDuration(time.Hour)
		defs := []*model.ProcessDefinition{
			versioned(defWithTimerStart(t, "timer-def", trig), 1),
			versioned(defWithTimerStart(t, "timer-def", trig), 2),
		}
		hits := timerStartDefs(defs)
		if assert.Len(t, hits, 1, "only the latest version must produce a timer-start hit") {
			assert.Equal(t, 2, hits[0].Def.Version)
		}
	})

	t.Run("message enumeration is unambiguous across versions", func(t *testing.T) {
		t.Parallel()
		defs := []*model.ProcessDefinition{
			versioned(defWithMessageStart(t, "msg-def", "m"), 1),
			versioned(defWithMessageStart(t, "msg-def", "m"), 2),
		}
		def, _, count := uniqueMessageStartDef(defs, "m")
		if assert.Equal(t, 1, count, "same id across versions must resolve to a unique latest match, not ambiguity") {
			assert.Equal(t, 2, def.Version)
		}
	})
}

func uniqueDefID(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, eventStartTestDefSeq.Add(1))
}

// TestSignalStartDefsFindsAllMatches verifies that signalStartDefs returns
// every def+node whose signal-start name matches, across multiple defs, and
// excludes defs whose signal-start name does not match.
func TestSignalStartDefsFindsAllMatches(t *testing.T) {
	t.Parallel()

	pay := defWithSignalStart(t, "payment", "order.completed")
	ship := defWithSignalStart(t, "shipment", "order.completed")
	other := defWithSignalStart(t, "audit", "unrelated")

	hits := signalStartDefs([]*model.ProcessDefinition{pay, ship, other}, "order.completed")

	ids := []string{}
	for _, h := range hits {
		ids = append(ids, h.Def.ID)
	}
	assert.ElementsMatch(t, []string{"payment", "shipment"}, ids)
}

// TestUniqueMessageStartDefCounts verifies uniqueMessageStartDef's match-count
// contract: 0 on miss, 1 on a unique match, >=2 on ambiguity across defs.
func TestUniqueMessageStartDefCounts(t *testing.T) {
	t.Parallel()

	defA := defWithMessageStart(t, "A", "m")
	defB := defWithMessageStart(t, "B", "m")

	type testCase struct {
		name   string
		defs   []*model.ProcessDefinition
		msg    string
		assert func(t *testing.T, def *model.ProcessDefinition, nodeID string, count int)
	}

	cases := []testCase{
		{
			name: "miss returns count 0 and no def",
			defs: nil,
			msg:  "x",
			assert: func(t *testing.T, def *model.ProcessDefinition, nodeID string, count int) {
				assert.Equal(t, 0, count)
				assert.Nil(t, def)
				assert.Empty(t, nodeID)
			},
		},
		{
			name: "unique match returns count 1 with the matching def and node",
			defs: []*model.ProcessDefinition{defA},
			msg:  "m",
			assert: func(t *testing.T, def *model.ProcessDefinition, nodeID string, count int) {
				assert.Equal(t, 1, count)
				if assert.NotNil(t, def) {
					assert.Equal(t, "A", def.ID)
				}
				assert.Equal(t, "start", nodeID)
			},
		},
		{
			name: "ambiguous match across two defs returns count 2",
			defs: []*model.ProcessDefinition{defA, defB},
			msg:  "m",
			assert: func(t *testing.T, def *model.ProcessDefinition, nodeID string, count int) {
				assert.Equal(t, 2, count)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, nodeID, count := uniqueMessageStartDef(tc.defs, tc.msg)
			tc.assert(t, def, nodeID, count)
		})
	}
}

// TestMessageStartNode verifies messageStartNode locates the message-start
// node on a single definition by message name, and reports ok=false on a
// name mismatch or when the definition has no start nodes at all.
func TestMessageStartNode(t *testing.T) {
	t.Parallel()

	found := defWithMessageStart(t, uniqueDefID(t, "found"), "order.placed")
	wrongName := defWithMessageStart(t, uniqueDefID(t, "wrong"), "other")
	noStart := defWithoutStart(t, uniqueDefID(t, "none"))

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		msg    string
		assert func(t *testing.T, nodeID string, ok bool)
	}

	cases := []testCase{
		{
			name: "matching message name resolves the start node",
			def:  found,
			msg:  "order.placed",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "start", nodeID)
			},
		},
		{
			name: "message name mismatch reports ok=false",
			def:  wrongName,
			msg:  "order.placed",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.False(t, ok)
				assert.Empty(t, nodeID)
			},
		},
		{
			name: "definition without any start node reports ok=false",
			def:  noStart,
			msg:  "order.placed",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.False(t, ok)
				assert.Empty(t, nodeID)
			},
		},
		{
			name: "nil definition reports ok=false",
			def:  nil,
			msg:  "order.placed",
			assert: func(t *testing.T, nodeID string, ok bool) {
				assert.False(t, ok)
				assert.Empty(t, nodeID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nodeID, ok := messageStartNode(tc.def, tc.msg)
			tc.assert(t, nodeID, ok)
		})
	}
}

// TestTimerStartDefs verifies timerStartDefs collects every def+node with a
// timer-start (carrying its Trigger), skipping defs whose start node has no
// timer configured.
func TestTimerStartDefs(t *testing.T) {
	t.Parallel()

	trig := schedule.AfterDuration(time.Hour)

	type testCase struct {
		name   string
		defs   []*model.ProcessDefinition
		assert func(t *testing.T, hits []timerStartHit)
	}

	cases := []testCase{
		{
			name: "single timer-start def is matched with its trigger",
			defs: []*model.ProcessDefinition{defWithTimerStart(t, "T1", trig)},
			assert: func(t *testing.T, hits []timerStartHit) {
				if assert.Len(t, hits, 1) {
					assert.Equal(t, "T1", hits[0].Def.ID)
					assert.Equal(t, "start", hits[0].NodeID)
					assert.Equal(t, trig, hits[0].Trigger)
				}
			},
		},
		{
			name: "signal-start def without a timer is excluded",
			defs: []*model.ProcessDefinition{defWithSignalStart(t, "S1", "sig")},
			assert: func(t *testing.T, hits []timerStartHit) {
				assert.Empty(t, hits)
			},
		},
		{
			name: "mixed defs return only the timer-start ones",
			defs: []*model.ProcessDefinition{
				defWithTimerStart(t, "T2", trig),
				defWithSignalStart(t, "S2", "sig"),
			},
			assert: func(t *testing.T, hits []timerStartHit) {
				if assert.Len(t, hits, 1) {
					assert.Equal(t, "T2", hits[0].Def.ID)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hits := timerStartDefs(tc.defs)
			tc.assert(t, hits)
		})
	}
}
