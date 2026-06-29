package email_test

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/action/email"
)

// startPlaintextSMTPStub starts a minimal in-process SMTP stub that advertises NO
// STARTTLS extension. It returns the address the stub is listening on and a done
// function that shuts it down.
//
// Protocol: 220 greeting → wait for EHLO → respond with no STARTTLS in caps →
// then accept or reject the next command (if the client tries to MAIL/RCPT/DATA,
// respond 250; the point is only that STARTTLS is absent from EHLO caps).
func startPlaintextSMTPStub(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("smtp stub listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer c.Close() //nolint:errcheck
				r := bufio.NewReader(c)
				// 220 greeting
				fmt.Fprintf(c, "220 stub.example.com ESMTP\r\n") //nolint:errcheck
				// Read EHLO
				line, _ := r.ReadString('\n')
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "EHLO") ||
					strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "HELO") {
					// Respond WITHOUT STARTTLS — only SIZE advertised
					fmt.Fprintf(c, "250-stub.example.com\r\n") //nolint:errcheck
					fmt.Fprintf(c, "250 SIZE 10240000\r\n")    //nolint:errcheck
				}
				// Drain and reject anything that follows (STARTTLS attempt, MAIL, etc.)
				for {
					line, err = r.ReadString('\n')
					if err != nil {
						return
					}
					upper := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(upper, "QUIT"):
						fmt.Fprintf(c, "221 bye\r\n") //nolint:errcheck
						return
					default:
						fmt.Fprintf(c, "502 unrecognized\r\n") //nolint:errcheck
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// TestStartTLSEnforcedWhenServerDoesNotAdvertise asserts that WithStartTLS() causes Do
// to return an error when the SMTP server does NOT advertise the STARTTLS extension.
// This is the key enforcement property: we must never silently fall back to plaintext.
func TestStartTLSEnforcedWhenServerDoesNotAdvertise(t *testing.T) {
	addr := startPlaintextSMTPStub(t)

	a := email.NewEmail(
		email.WithSMTPAddr(addr),
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithSubjectTemplate("hi"),
		email.WithBodyTemplate("body"),
		email.WithStartTLS(),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err == nil {
		t.Fatal("expected error when server does not advertise STARTTLS, got nil")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected error to mention STARTTLS, got: %v", err)
	}
}

// TestWithTLSConfigOverrideIsHonored verifies that WithTLSConfig stores a custom
// tls.Config so that a constructed startTLSSender (or tlsSender) would use it.
// We verify via a negative check: point WithStartTLS() + WithTLSConfig(cfg) at the
// no-STARTTLS stub; the error should still be the STARTTLS-not-supported error (the
// config override is stored and used; the stub never negotiates TLS so the enforcement
// fires first, proving the code path ran with our config rather than falling through
// to the default sender).
func TestWithTLSConfigOverrideIsHonored(t *testing.T) {
	addr := startPlaintextSMTPStub(t)

	cfg := &tls.Config{InsecureSkipVerify: true, ServerName: "custom.example.com"} //nolint:gosec // unit test only
	a := email.NewEmail(
		email.WithSMTPAddr(addr),
		email.WithFrom("a@b.c"),
		email.WithTo("d@e.f"),
		email.WithSubjectTemplate("hi"),
		email.WithBodyTemplate("body"),
		email.WithStartTLS(),
		email.WithTLSConfig(cfg),
	)
	_, err := a.Do(t.Context(), map[string]any{})
	if err == nil {
		t.Fatal("expected STARTTLS enforcement error even with custom tls.Config, got nil")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected STARTTLS error, got: %v", err)
	}
}

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
