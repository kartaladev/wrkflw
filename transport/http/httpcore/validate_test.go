package httpcore_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestValidate(t *testing.T) {
	if err := httpcore.Validate(httpcore.StartInput{DefRef: "o"}); err != nil {
		t.Fatalf("valid struct should pass: %v", err)
	}
	err := httpcore.Validate(httpcore.StartInput{DefRef: ""}) // missing required fields
	if err == nil || !errors.Is(err, httpcore.ErrBadInput) {
		t.Fatalf("missing required must wrap ErrBadInput, got %v", err)
	}
}

func TestValidateSignalInput(t *testing.T) {
	if err := httpcore.Validate(httpcore.SignalInput{Signal: "approved"}); err != nil {
		t.Fatalf("valid SignalInput should pass: %v", err)
	}
	if err := httpcore.Validate(httpcore.SignalInput{}); err == nil || !errors.Is(err, httpcore.ErrBadInput) {
		t.Fatalf("empty signal must wrap ErrBadInput, got %v", err)
	}
}

func TestValidateMessageInput(t *testing.T) {
	if err := httpcore.Validate(httpcore.MessageInput{DefRef: "order", Name: "payment"}); err != nil {
		t.Fatalf("valid MessageInput should pass: %v", err)
	}
	if err := httpcore.Validate(httpcore.MessageInput{Name: "payment"}); err == nil || !errors.Is(err, httpcore.ErrBadInput) {
		t.Fatalf("missing def_ref must wrap ErrBadInput, got %v", err)
	}
	if err := httpcore.Validate(httpcore.MessageInput{DefRef: "order"}); err == nil || !errors.Is(err, httpcore.ErrBadInput) {
		t.Fatalf("missing name must wrap ErrBadInput, got %v", err)
	}
}

// TestValidateUsesJSONFieldNames guards RegisterTagNameFunc: validation errors
// must reference the JSON wire name (def_ref) the client sends, never the Go
// struct field name (DefRef), which would leak internal identifiers and confuse
// the caller.
func TestValidateUsesJSONFieldNames(t *testing.T) {
	err := httpcore.Validate(httpcore.StartInput{})
	if err == nil {
		t.Fatal("want validation error for empty StartInput")
	}
	if strings.Contains(err.Error(), "DefRef") {
		t.Fatalf("400 message leaks Go field name DefRef: %v", err)
	}
	if !strings.Contains(err.Error(), "def_ref") {
		t.Fatalf("400 message should reference json name def_ref: %v", err)
	}
}

func TestValidateStructWithNoTags(t *testing.T) {
	// ClaimInput has no required tags — any value (including zero) is valid.
	if err := httpcore.Validate(httpcore.ClaimInput{}); err != nil {
		t.Fatalf("ClaimInput with no validate tags should always pass: %v", err)
	}
}
