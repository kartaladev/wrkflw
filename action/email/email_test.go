package email_test

import (
	"net/smtp"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
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

// TestEmailMissingKeyError locks in the missingkey=error template option: a body
// template referencing a key absent from the variable map must cause Do to return
// a non-nil error. This guards against accidental removal of Option("missingkey=error").
func TestEmailMissingKeyError(t *testing.T) {
	a := email.NewEmail(
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithBodyTemplate("Hi {{.name}}"),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err == nil {
		t.Fatalf("expected error for missing template key, got nil")
	}
}

// TestEmailRejectsHeaderInjection verifies that a rendered header value containing
// carriage return or newline characters is rejected with a non-retryable error and
// that the underlying sender is never called (no message is transmitted).
func TestEmailRejectsHeaderInjection(t *testing.T) {
	tests := map[string]struct {
		opts func(spy *bool) []email.Option
	}{
		"subject with CRLF injection": {
			func(spy *bool) []email.Option {
				return []email.Option{
					email.WithFrom("a@b.c"),
					email.WithTo("d@e.f"),
					// The template renders to a value containing a CRLF header injection.
					email.WithSubjectTemplate("Legit\r\nBcc: evil@x.com"),
					email.WithBodyTemplate("body"),
					email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
						*spy = true
						return nil
					})),
				}
			},
		},
		"subject via template variable injection": {
			func(spy *bool) []email.Option {
				return []email.Option{
					email.WithFrom("a@b.c"),
					email.WithTo("d@e.f"),
					email.WithSubjectTemplate("Order {{.subject}}"),
					email.WithBodyTemplate("body"),
					email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
						*spy = true
						return nil
					})),
				}
			},
		},
		"from with newline": {
			func(spy *bool) []email.Option {
				return []email.Option{
					email.WithFrom("a@b.c\nX-Extra: injected"),
					email.WithTo("d@e.f"),
					email.WithSubjectTemplate("hi"),
					email.WithBodyTemplate("body"),
					email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
						*spy = true
						return nil
					})),
				}
			},
		},
		"to address with CRLF": {
			func(spy *bool) []email.Option {
				return []email.Option{
					email.WithFrom("a@b.c"),
					email.WithTo("d@e.f\r\nBcc: evil@x.com"),
					email.WithSubjectTemplate("hi"),
					email.WithBodyTemplate("body"),
					email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
						*spy = true
						return nil
					})),
				}
			},
		},
		"clean subject still sends": {
			func(spy *bool) []email.Option {
				return []email.Option{
					email.WithFrom("a@b.c"),
					email.WithTo("d@e.f"),
					email.WithSubjectTemplate("Clean subject"),
					email.WithBodyTemplate("body"),
					email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
						*spy = true
						return nil
					})),
				}
			},
		},
	}

	injectionCases := map[string]bool{
		"subject with CRLF injection":             true,
		"subject via template variable injection": true,
		"from with newline":                       true,
		"to address with CRLF":                    true,
		"clean subject still sends":               false,
	}

	injectionVars := map[string]map[string]any{
		"subject with CRLF injection":             {},
		"subject via template variable injection": {"subject": "legit\r\nBcc: evil@x.com"},
		"from with newline":                       {},
		"to address with CRLF":                    {},
		"clean subject still sends":               {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var senderCalled bool
			a := email.NewEmail(tc.opts(&senderCalled)...)
			vars := injectionVars[name]
			_, err := a.Do(t.Context(), vars)

			expectsInjection := injectionCases[name]
			if expectsInjection {
				if err == nil {
					t.Fatalf("expected non-nil error for injection case, got nil")
				}
				if action.IsRetryable(err) {
					t.Fatalf("expected non-retryable error, got retryable: %v", err)
				}
				if senderCalled {
					t.Fatalf("sender must NOT be called when injection is detected")
				}
			} else {
				if err != nil {
					t.Fatalf("clean case: unexpected error: %v", err)
				}
				if !senderCalled {
					t.Fatalf("clean case: sender was NOT called but should have been")
				}
			}
		})
	}
}

// TestEmailWithAuthOrderIndependent verifies that WithAuth works correctly regardless
// of whether it is called before or after WithSMTPAddr. The smtp.Auth passed to the
// sender must be non-nil in both orderings.
func TestEmailWithAuthOrderIndependent(t *testing.T) {
	cases := []struct {
		name string
		opts []email.Option
	}{
		{
			name: "auth_before_addr",
			opts: []email.Option{
				email.WithAuth("user@example.com", "s3cr3t"),
				email.WithSMTPAddr("smtp.example.com:587"),
				email.WithFrom("a@b.c"),
				email.WithTo("d@e.f"),
				email.WithBodyTemplate("Hi"),
			},
		},
		{
			name: "addr_before_auth",
			opts: []email.Option{
				email.WithSMTPAddr("smtp.example.com:587"),
				email.WithAuth("user@example.com", "s3cr3t"),
				email.WithFrom("a@b.c"),
				email.WithTo("d@e.f"),
				email.WithBodyTemplate("Hi"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth smtp.Auth
			opts := append(tc.opts, email.WithSender(email.SenderFunc(func(_ string, auth smtp.Auth, _ string, _ []string, _ []byte) error {
				gotAuth = auth
				return nil
			})))
			a := email.NewEmail(opts...)
			_, err := a.Do(t.Context(), map[string]any{})
			if err != nil {
				t.Fatalf("Do err = %v", err)
			}
			if gotAuth == nil {
				t.Fatalf("expected non-nil smtp.Auth, got nil (auth order dependency bug)")
			}
		})
	}
}
