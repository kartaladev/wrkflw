package email_test

import (
	"net/smtp"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action/email"
)

type capturedSend struct {
	from string
	to   []string
	msg  []byte
}

func TestEmailRendersAndSends(t *testing.T) {
	var got capturedSend
	fake := email.SenderFunc(func(_ string, _ smtp.Auth, from string, to []string, msg []byte) error {
		got = capturedSend{from, to, msg}
		return nil
	})

	a := email.NewEmail(
		email.WithSMTPAddr("smtp.example.com:587"),
		email.WithFrom("no-reply@example.com"),
		email.WithTo("user@example.com"),
		email.WithSubjectTemplate("Order {{.orderID}} confirmed"),
		email.WithBodyTemplate("Hi {{.name}}, your order {{.orderID}} is confirmed."),
		email.WithSender(fake),
	)

	out, err := a.Do(t.Context(), map[string]any{"orderID": "A-1", "name": "Ada"})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if out["emailSent"] != true {
		t.Fatalf("emailSent = %v, want true", out["emailSent"])
	}
	if got.from != "no-reply@example.com" || len(got.to) != 1 || got.to[0] != "user@example.com" {
		t.Fatalf("envelope wrong: from=%q to=%v", got.from, got.to)
	}
	msg := string(got.msg)
	if !strings.Contains(msg, "Subject: Order A-1 confirmed") {
		t.Fatalf("subject not rendered in message: %q", msg)
	}
	if !strings.Contains(msg, "Hi Ada, your order A-1 is confirmed.") {
		t.Fatalf("body not rendered in message: %q", msg)
	}
}

func TestEmailTemplateError(t *testing.T) {
	a := email.NewEmail(
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithBodyTemplate("{{.unclosed"),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	if _, err := a.Do(t.Context(), map[string]any{}); err == nil {
		t.Fatalf("expected template parse error, got nil")
	}
}

// TestEmailSubjectTemplateError ensures a bad subject template is caught and returned as a
// non-retryable error (exercises the subject-render error path in Do).
func TestEmailSubjectTemplateError(t *testing.T) {
	a := email.NewEmail(
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithSubjectTemplate("{{.unclosed"),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	if _, err := a.Do(t.Context(), map[string]any{}); err == nil {
		t.Fatalf("expected subject template parse error, got nil")
	}
}

// TestEmailWithHTML verifies that WithHTML sets Content-Type text/html in the outgoing message.
func TestEmailWithHTML(t *testing.T) {
	var gotMsg []byte
	a := email.NewEmail(
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithBodyTemplate("Hello"),
		email.WithHTML(),
		email.WithSender(email.SenderFunc(func(_ string, _ smtp.Auth, _ string, _ []string, msg []byte) error {
			gotMsg = msg
			return nil
		})),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if !strings.Contains(string(gotMsg), "Content-Type: text/html") {
		t.Fatalf("expected text/html Content-Type in message, got: %q", string(gotMsg))
	}
}

// TestEmailWithAuth verifies that WithAuth sets a non-nil smtp.Auth on the action.
// It uses a fake sender that captures the auth value passed through.
func TestEmailWithAuth(t *testing.T) {
	var gotAuth smtp.Auth
	a := email.NewEmail(
		email.WithSMTPAddr("smtp.example.com:587"),
		email.WithAuth("user@example.com", "s3cr3t"),
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithBodyTemplate("Hi"),
		email.WithSender(email.SenderFunc(func(_ string, auth smtp.Auth, _ string, _ []string, _ []byte) error {
			gotAuth = auth
			return nil
		})),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if gotAuth == nil {
		t.Fatalf("expected non-nil smtp.Auth when WithAuth is used")
	}
}

// TestEmailWithTLSAndStartTLS verifies that WithTLS and WithStartTLS are accepted
// without panic (they are informational no-ops in the current default sender).
func TestEmailWithTLSAndStartTLS(t *testing.T) {
	a := email.NewEmail(
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithBodyTemplate("Hi"),
		email.WithTLS(),
		email.WithStartTLS(),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	if _, err := a.Do(t.Context(), map[string]any{}); err != nil {
		t.Fatalf("Do err = %v", err)
	}
}
