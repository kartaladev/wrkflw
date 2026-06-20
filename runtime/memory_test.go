package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestMemStateStoreRoundTrip(t *testing.T) {
	s := runtime.NewMemStateStore()

	_, err := s.Load("missing")
	require.ErrorIs(t, err, runtime.ErrInstanceNotFound, "Load of unknown id must return ErrInstanceNotFound")

	st := engine.InstanceState{InstanceID: "i1", Status: engine.StatusRunning}
	require.NoError(t, s.Save(st))

	got, err := s.Load("i1")
	require.NoError(t, err)
	assert.Equal(t, "i1", got.InstanceID)
	assert.Equal(t, engine.StatusRunning, got.Status)
}

func TestMemJournalAppendAndEntries(t *testing.T) {
	j := runtime.NewMemJournal()
	assert.Empty(t, j.Entries("i1"))

	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	require.NoError(t, j.Append("i1", engine.NewStartInstance(at, nil)))
	require.NoError(t, j.Append("i1", engine.NewActionCompleted(at, "i1-c1", nil)))

	entries := j.Entries("i1")
	require.Len(t, entries, 2)
	assert.Equal(t, at, entries[0].OccurredAt())
}
