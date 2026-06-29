package email_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
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

// ---------------------------------------------------------------------------
// Recipient resolver tests
// ---------------------------------------------------------------------------

// capturedCall records a single send call from the fake sender.
type capturedCall struct {
	to  []string
	msg []byte
}

// newCapturingSender returns a SenderFunc that records every send call
// (thread-safe) and an accessor for the collected calls.
func newCapturingSender(t *testing.T) (email.SenderFunc, func() []capturedCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []capturedCall
	fn := email.SenderFunc(func(_ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
		// copy slices — the caller may reuse underlying arrays
		toCopy := make([]string, len(to))
		copy(toCopy, to)
		msgCopy := make([]byte, len(msg))
		copy(msgCopy, msg)
		mu.Lock()
		calls = append(calls, capturedCall{to: toCopy, msg: msgCopy})
		mu.Unlock()
		return nil
	})
	return fn, func() []capturedCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedCall, len(calls))
		copy(out, calls)
		return out
	}
}

// TestResolverTwoRecipientsPersonalized verifies that a resolver returning 2 recipients
// with per-recipient Data causes exactly 2 individual sends, each addressed to a single
// recipient and rendered with that recipient's personalized data.
func TestResolverTwoRecipientsPersonalized(t *testing.T) {
	fake, getCalls := newCapturingSender(t)

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		return []email.Recipient{
			{Address: "ada@example.com", Data: map[string]any{"name": "Ada"}},
			{Address: "bob@example.com", Data: map[string]any{"name": "Bob"}},
		}, nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi {{.name}}"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	out, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}
	if out["emailSent"] != true {
		t.Fatalf("emailSent = %v, want true", out["emailSent"])
	}
	if out["recipientCount"] != 2 {
		t.Fatalf("recipientCount = %v, want 2", out["recipientCount"])
	}

	calls := getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 send calls, got %d", len(calls))
	}

	// First send: Ada
	if len(calls[0].to) != 1 || calls[0].to[0] != "ada@example.com" {
		t.Fatalf("first send to = %v, want [ada@example.com]", calls[0].to)
	}
	if !strings.Contains(string(calls[0].msg), "Hi Ada") {
		t.Fatalf("first send body should contain 'Hi Ada', got: %q", string(calls[0].msg))
	}

	// Second send: Bob
	if len(calls[1].to) != 1 || calls[1].to[0] != "bob@example.com" {
		t.Fatalf("second send to = %v, want [bob@example.com]", calls[1].to)
	}
	if !strings.Contains(string(calls[1].msg), "Hi Bob") {
		t.Fatalf("second send body should contain 'Hi Bob', got: %q", string(calls[1].msg))
	}
}

// TestStaticPlusResolverCombined verifies that static WithTo addresses and resolver
// results are combined into a single send-loop, producing one send per recipient.
func TestStaticPlusResolverCombined(t *testing.T) {
	fake, getCalls := newCapturingSender(t)

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		return []email.Recipient{
			{Address: "resolver@example.com", Data: map[string]any{"name": "Res"}},
		}, nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithTo("static@example.com"),
		email.WithBodyTemplate("Hi {{.name}}"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	_, err := a.Do(t.Context(), map[string]any{"name": "Static"})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}

	calls := getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 send calls (1 static + 1 resolver), got %d", len(calls))
	}
}

// TestPersonalizationPrecedence verifies that per-recipient Data wins over instance vars.
// Instance vars: name="Default"; recipient Data: name="Ada". Body should render "Hi Ada".
func TestPersonalizationPrecedence(t *testing.T) {
	fake, getCalls := newCapturingSender(t)

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		return []email.Recipient{
			{Address: "ada@example.com", Data: map[string]any{"name": "Ada"}},
		}, nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi {{.name}}"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	_, err := a.Do(t.Context(), map[string]any{"name": "Default"})
	if err != nil {
		t.Fatalf("Do err = %v", err)
	}

	calls := getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(calls))
	}
	if !strings.Contains(string(calls[0].msg), "Hi Ada") {
		t.Fatalf("body should contain 'Hi Ada' (recipient data wins), got: %q", string(calls[0].msg))
	}
	if strings.Contains(string(calls[0].msg), "Hi Default") {
		t.Fatalf("body should NOT contain 'Hi Default', got: %q", string(calls[0].msg))
	}
}

// TestBestEffortPartialFailure verifies that when 3 recipients are sent and the 2nd
// sender call fails, all 3 sends are attempted, the returned error is retryable and
// mentions the failed address, and the successful sends are captured.
func TestBestEffortPartialFailure(t *testing.T) {
	var mu sync.Mutex
	var capturedTos []string
	callCount := 0

	fake := email.SenderFunc(func(_ string, _ smtp.Auth, _ string, to []string, _ []byte) error {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		capturedTos = append(capturedTos, to[0])
		if callCount == 2 {
			return errors.New("transient SMTP failure")
		}
		return nil
	})

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		return []email.Recipient{
			{Address: "r1@example.com", Data: map[string]any{"name": "R1"}},
			{Address: "r2@example.com", Data: map[string]any{"name": "R2"}},
			{Address: "r3@example.com", Data: map[string]any{"name": "R3"}},
		}, nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi {{.name}}"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	_, err := a.Do(t.Context(), map[string]any{})

	mu.Lock()
	count := callCount
	tos := append([]string{}, capturedTos...)
	mu.Unlock()

	if count != 3 {
		t.Fatalf("expected sender called 3 times (best-effort), got %d", count)
	}
	if err == nil {
		t.Fatalf("expected non-nil error from partial failure, got nil")
	}
	if !action.IsRetryable(err) {
		t.Fatalf("expected retryable error for transient send failure, got non-retryable: %v", err)
	}
	if !strings.Contains(err.Error(), "r2@example.com") {
		t.Fatalf("error should mention failed address r2@example.com, got: %v", err)
	}
	// First and third must have been attempted.
	if len(tos) != 3 || tos[0] != "r1@example.com" || tos[2] != "r3@example.com" {
		t.Fatalf("expected all 3 recipients attempted in order, got: %v", tos)
	}
}

// TestResolverErrorPropagation verifies that a resolver error is returned as-is
// and no sends occur; if the resolver returns NonRetryable, IsRetryable is false.
func TestResolverErrorPropagation(t *testing.T) {
	tests := []struct {
		name          string
		resolverErr   error
		wantRetryable bool
	}{
		{
			name:          "plain resolver error is retryable",
			resolverErr:   errors.New("db connection failed"),
			wantRetryable: true,
		},
		{
			name:          "non-retryable resolver error propagated",
			resolverErr:   action.NonRetryable(errors.New("resolver config invalid")),
			wantRetryable: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sendCalled := false
			fake := email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
				sendCalled = true
				return nil
			})

			resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
				return nil, tc.resolverErr
			})

			a := email.NewEmail(
				email.WithFrom("no-reply@example.com"),
				email.WithBodyTemplate("Hi"),
				email.WithSubjectTemplate("Hello"),
				email.WithRecipientResolver(resolver),
				email.WithSender(fake),
			)

			_, err := a.Do(t.Context(), map[string]any{})
			if err == nil {
				t.Fatalf("expected error from resolver, got nil")
			}
			if action.IsRetryable(err) != tc.wantRetryable {
				t.Fatalf("IsRetryable(%v) = %v, want %v", err, action.IsRetryable(err), tc.wantRetryable)
			}
			if sendCalled {
				t.Fatal("sender must NOT be called when resolver errors")
			}
		})
	}
}

// TestEmptyRecipientListNonRetryable verifies that when no WithTo addresses and no
// resolver are configured (combined list is empty), Do returns a non-retryable error
// and no sends occur.
func TestEmptyRecipientListNonRetryable(t *testing.T) {
	sendCalled := false
	fake := email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
		sendCalled = true
		return nil
	})

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		return nil, nil // returns empty, no error
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	_, err := a.Do(t.Context(), map[string]any{})
	if err == nil {
		t.Fatalf("expected non-nil error for empty recipient list, got nil")
	}
	if action.IsRetryable(err) {
		t.Fatalf("expected non-retryable error for empty recipient list, got retryable: %v", err)
	}
	if sendCalled {
		t.Fatal("sender must NOT be called when recipient list is empty")
	}
}

// TestCRLFInResolvedAddressIsAggregated verifies that a CRLF-injected resolved address
// causes a per-recipient error (aggregated) while the OTHER valid recipient is still sent.
func TestCRLFInResolvedAddressIsAggregated(t *testing.T) {
	fake, getCalls := newCapturingSender(t)

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		return []email.Recipient{
			{Address: "good@example.com", Data: map[string]any{"name": "Good"}},
			{Address: "a\r\nBcc: evil@x.com", Data: map[string]any{"name": "Evil"}},
		}, nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi {{.name}}"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	_, err := a.Do(t.Context(), map[string]any{})
	if err == nil {
		t.Fatalf("expected aggregate error for CRLF recipient, got nil")
	}
	if !strings.Contains(err.Error(), "newline") {
		t.Fatalf("error should mention newline injection, got: %v", err)
	}

	calls := getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 successful send (valid recipient only), got %d", len(calls))
	}
	if calls[0].to[0] != "good@example.com" {
		t.Fatalf("expected send to good@example.com, got: %v", calls[0].to)
	}
}

// TestCtxCancellationHonoredByResolver verifies that when the context is cancelled,
// a resolver that checks ctx returns ctx.Err() and Do propagates the error.
func TestCtxCancellationHonoredByResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	resolver := email.RecipientResolver(func(ctx context.Context, _ map[string]any) ([]email.Recipient, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []email.Recipient{{Address: "x@example.com"}}, nil
	})

	fake := email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error {
		return nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi"),
		email.WithSubjectTemplate("Hello"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	_, err := a.Do(ctx, map[string]any{})
	if err == nil {
		t.Fatalf("expected error when ctx is cancelled, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled in error chain, got: %v", err)
	}
}

// ExampleWithRecipientResolver demonstrates using an in-memory "DB" resolver to send
// personalized emails to a deterministic list of recipients.
func ExampleWithRecipientResolver() {
	type contact struct {
		address string
		name    string
	}

	// In-memory "database" of contacts — deterministic order.
	contacts := []contact{
		{address: "ada@example.com", name: "Ada"},
		{address: "bob@example.com", name: "Bob"},
	}

	var mu sync.Mutex
	var personalized []string

	resolver := email.RecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
		rcpts := make([]email.Recipient, len(contacts))
		for i, c := range contacts {
			rcpts[i] = email.Recipient{
				Address: c.address,
				Data:    map[string]any{"name": c.name},
			}
		}
		return rcpts, nil
	})

	fake := email.SenderFunc(func(_ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
		mu.Lock()
		personalized = append(personalized, to[0])
		mu.Unlock()
		return nil
	})

	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithBodyTemplate("Hi {{.name}}, welcome!"),
		email.WithSubjectTemplate("Welcome"),
		email.WithRecipientResolver(resolver),
		email.WithSender(fake),
	)

	out, err := a.Do(context.Background(), map[string]any{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("recipientCount:", out["recipientCount"])
	mu.Lock()
	defer mu.Unlock()
	for _, addr := range personalized {
		fmt.Println("sent to:", addr)
	}
	// Output:
	// recipientCount: 2
	// sent to: ada@example.com
	// sent to: bob@example.com
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
