package httpcore_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestValidate(t *testing.T) {
	if err := httpcore.Validate(httpcore.StartInput{DefRef: "o", InstanceID: "o-1"}); err != nil {
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

func TestValidateStructWithNoTags(t *testing.T) {
	// ClaimInput has no required tags — any value (including zero) is valid.
	if err := httpcore.Validate(httpcore.ClaimInput{}); err != nil {
		t.Fatalf("ClaimInput with no validate tags should always pass: %v", err)
	}
}
