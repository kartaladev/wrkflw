// Package idgen mints process-instance identifiers behind a pluggable strategy.
// It is the ID-generation counterpart to the clock package: a small, injectable
// seam with a sensible default (xid) that consumers override via
// runtime.WithIDGenerator / service.WithIDGenerator.
package idgen

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/xid"
)

// Generator mints a unique process-instance identifier. NewID returns an error
// so a rare entropy failure (e.g. from UUID v7) surfaces as a clean caller error
// rather than a panic; the xid generator never errors.
type Generator interface {
	NewID() (string, error)
}

// XID returns the default generator, backed by github.com/rs/xid. The returned
// IDs are ~20-char lowercase base32hex with no hyphens, k-sortable, and need no
// external coordination. NewID always returns a nil error.
func XID() Generator { return xidGen{} }

type xidGen struct{}

func (xidGen) NewID() (string, error) { return xid.New().String(), nil }

// UUIDv7 returns a generator backed by github.com/google/uuid NewV7
// (chronologically sortable, RFC 9562). NewID propagates the rare entropy error.
func UUIDv7() Generator { return uuidV7Gen{} }

type uuidV7Gen struct{}

func (uuidV7Gen) NewID() (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("workflow-idgen: uuidv7: %w", err)
	}
	return u.String(), nil
}

// Func adapts a plain function into a Generator. Use it in tests to inject a
// deterministic sequence via WithIDGenerator.
func Func(fn func() (string, error)) Generator { return funcGen(fn) }

type funcGen func() (string, error)

func (f funcGen) NewID() (string, error) { return f() }
