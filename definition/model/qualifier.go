package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidQualifier is returned by ParseQualifier for a malformed reference.
var ErrInvalidQualifier = errors.New("workflow-model: invalid qualifier")

// Qualifier references a process definition: a specific version when Version >= 1,
// or the latest registered version when Version == 0.
type Qualifier struct {
	ID      string
	Version int
}

// Latest returns a Qualifier that resolves the newest registered version of id.
func Latest(id string) Qualifier { return Qualifier{ID: id} }

// Version returns a Qualifier pinned to (id, v).
func Version(id string, v int) Qualifier { return Qualifier{ID: id, Version: v} }

// IsLatest reports whether q resolves the newest version (Version == 0).
func (q Qualifier) IsLatest() bool { return q.Version == 0 }

// String renders q as "id" (latest) or "id:version" (pinned). It is the inverse
// of ParseQualifier for all valid qualifiers.
func (q Qualifier) String() string {
	if q.IsLatest() {
		return q.ID
	}
	return q.ID + ":" + strconv.Itoa(q.Version)
}

// ParseQualifier is the inverse of String. "id" -> latest; "id:version" -> pinned.
// It rejects an empty id, an empty/non-numeric/negative version, and ":0"
// (Version 0 is the reserved latest sentinel; express latest as bare "id").
func ParseQualifier(s string) (Qualifier, error) {
	id, verStr, hasColon := strings.Cut(s, ":")
	if id == "" {
		return Qualifier{}, fmt.Errorf("%w: empty id in %q", ErrInvalidQualifier, s)
	}
	if !hasColon {
		return Qualifier{ID: id}, nil
	}
	v, err := strconv.Atoi(verStr)
	if err != nil {
		return Qualifier{}, fmt.Errorf("%w: bad version in %q: %w", ErrInvalidQualifier, s, err)
	}
	if v < 1 {
		return Qualifier{}, fmt.Errorf("%w: version must be >= 1 in %q", ErrInvalidQualifier, s)
	}
	return Qualifier{ID: id, Version: v}, nil
}

// MarshalJSON emits the String form (a JSON string), keeping the wire byte-identical.
func (q Qualifier) MarshalJSON() ([]byte, error) { return json.Marshal(q.String()) }

// UnmarshalJSON parses a JSON string via ParseQualifier.
func (q *Qualifier) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := ParseQualifier(s)
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}

// MarshalYAML emits the String form.
func (q Qualifier) MarshalYAML() (any, error) { return q.String(), nil }

// UnmarshalYAML parses a YAML scalar string via ParseQualifier.
func (q *Qualifier) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	parsed, err := ParseQualifier(s)
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}
