package gocron

import (
	"context"

	"github.com/go-co-op/gocron/v2"
)

// NeutralLock mirrors the public scheduling.Lock shape (ctx-in/error-out, no
// vendor type). AdaptLocker wraps a value producing these into a gocron.Locker so
// the public scheduling façade can hand its neutral Locker down without importing
// gocron.
type NeutralLock interface {
	Unlock(ctx context.Context) error
}

// NeutralLocker mirrors the public scheduling.Locker shape. Its method set is
// identical to gocron.Locker except that it returns a NeutralLock instead of a
// gocron.Lock, so it needs a one-hop adapter (AdaptLocker) rather than structural
// satisfaction.
type NeutralLocker interface {
	Lock(ctx context.Context, key string) (NeutralLock, error)
}

// NeutralElector mirrors the public scheduling.Elector shape. Its single method
// matches gocron.Elector exactly, so AdaptElector is a trivial 1:1 wrapper kept
// only to avoid leaking gocron into the public façade signatures.
type NeutralElector interface {
	IsLeader(ctx context.Context) error
}

// AdaptLocker adapts a NeutralLocker to a gocron.Locker: Lock delegates to the
// neutral locker and wraps its NeutralLock in a gocron.Lock whose Unlock delegates
// straight back. A nil locker yields a nil gocron.Locker (so callers can gate on
// it). This is how the public scheduling façade plumbs its neutral Locker down to
// gocron without importing gocron.
func AdaptLocker(l NeutralLocker) gocron.Locker {
	if l == nil {
		return nil
	}
	return neutralLockerAdapter{inner: l}
}

// AdaptElector adapts a NeutralElector to a gocron.Elector. Because their method
// sets are identical, the adapter is a trivial pass-through; a nil elector yields
// a nil gocron.Elector.
func AdaptElector(e NeutralElector) gocron.Elector {
	if e == nil {
		return nil
	}
	return neutralElectorAdapter{inner: e}
}

type neutralLockerAdapter struct{ inner NeutralLocker }

func (a neutralLockerAdapter) Lock(ctx context.Context, key string) (gocron.Lock, error) {
	l, err := a.inner.Lock(ctx, key)
	if err != nil {
		return nil, err
	}
	return neutralLockAdapter{inner: l}, nil
}

type neutralLockAdapter struct{ inner NeutralLock }

func (a neutralLockAdapter) Unlock(ctx context.Context) error { return a.inner.Unlock(ctx) }

type neutralElectorAdapter struct{ inner NeutralElector }

func (a neutralElectorAdapter) IsLeader(ctx context.Context) error { return a.inner.IsLeader(ctx) }
