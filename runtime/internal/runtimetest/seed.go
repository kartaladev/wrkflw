package runtimetest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// SeedCallLink records a running (non-terminal) call link into cl by driving the
// public MemInstanceStore.Create path — the same path production uses when a parent
// starts a child. It lets tests in packages other than kernel pre-populate the
// reference call-link store without reaching its unexported internals (which is
// why the former MemCallLinkStore.Seed test-only method could be removed from the
// shipped API; ADR-0087 follow-up).
func SeedCallLink(t *testing.T, cl *kernel.MemCallLinkStore, link kernel.CallLink) {
	t.Helper()
	s, err := kernel.NewMemInstanceStore(kernel.WithCallLinks(cl))
	require.NoError(t, err)
	_, err = s.Create(context.Background(), kernel.AppliedStep{
		State:       engine.InstanceState{InstanceID: link.ChildInstanceID},
		NewCallLink: &link,
	})
	require.NoError(t, err)
}

// SeedTerminalCallLink records link and then flips it to terminal with out, by
// driving the public MemInstanceStore Create+Commit path (record + markTerminal — the
// same calls production makes when a child instance completes). It replaces the
// former MemCallLinkStore.Seed/SeedTerminal test-only methods, keeping seeding
// off the shipped production API.
func SeedTerminalCallLink(t *testing.T, cl *kernel.MemCallLinkStore, link kernel.CallLink, out kernel.CallOutcome) {
	t.Helper()
	s, err := kernel.NewMemInstanceStore(kernel.WithCallLinks(cl))
	require.NoError(t, err)
	tok, err := s.Create(context.Background(), kernel.AppliedStep{
		State:       engine.InstanceState{InstanceID: link.ChildInstanceID},
		NewCallLink: &link,
	})
	require.NoError(t, err)
	_, err = s.Commit(context.Background(), tok, kernel.AppliedStep{
		State:       engine.InstanceState{InstanceID: link.ChildInstanceID},
		CallOutcome: &out,
	})
	require.NoError(t, err)
}
