// Package email provides a service action that sends an email over SMTP, rendering
// the subject and body as text/templates over the instance variables.
package email

import (
	"bytes"
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"text/template"

	"github.com/zakyalvan/krtlwrkflw/action"
)

// sender abstracts the SMTP send so message assembly is testable without a server.
type sender interface {
	send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

// SenderFunc adapts a function to the sender seam (exported for tests).
type SenderFunc func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error

func (f SenderFunc) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return f(addr, auth, from, to, msg)
}

type smtpSender struct{}

func (smtpSender) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

// Option configures an email action.
type Option func(*emailAction)

type emailAction struct {
	addr        string
	authUser    string
	authPass    string
	hasAuth     bool
	from        string
	to          []string
	subjectTmpl string
	bodyTmpl    string
	html        bool
	snd         sender
}

// WithSMTPAddr sets the SMTP server address ("host:port").
func WithSMTPAddr(addr string) Option { return func(a *emailAction) { a.addr = addr } }

// WithAuth sets PLAIN SMTP auth credentials. The SMTP host is derived from the
// configured SMTP address at send time (Do), so option order does not matter.
func WithAuth(user, pass string) Option {
	return func(a *emailAction) {
		a.authUser = user
		a.authPass = pass
		a.hasAuth = true
	}
}

// WithTLS marks the connection to use implicit TLS (port 465). It is informational
// in the default sender, which uses net/smtp.SendMail and does NOT open a TLS-wrapped
// connection. To send over implicit TLS, supply a custom SenderFunc via WithSender that
// dials tls.Dial and drives the session with smtp.NewClient.
func WithTLS() Option { return func(_ *emailAction) {} }

// WithStartTLS marks the connection to negotiate STARTTLS. It is informational in the
// default sender. To enforce STARTTLS, supply a custom SenderFunc via WithSender that
// calls (*smtp.Client).StartTLS after connecting.
func WithStartTLS() Option { return func(_ *emailAction) {} }

// WithFrom sets the envelope/From address.
func WithFrom(addr string) Option { return func(a *emailAction) { a.from = addr } }

// WithTo sets recipient addresses.
func WithTo(addrs ...string) Option { return func(a *emailAction) { a.to = addrs } }

// WithSubjectTemplate sets the subject as a text/template over the variables.
// The template is rendered against the instance variables at send time; if the
// rendered value contains a carriage return or newline, Do returns a non-retryable
// error to prevent SMTP header injection (the attacker can embed "\r\n" in a
// variable and inject arbitrary headers such as Bcc).
func WithSubjectTemplate(t string) Option { return func(a *emailAction) { a.subjectTmpl = t } }

// WithBodyTemplate sets the body as a text/template over the variables.
func WithBodyTemplate(t string) Option { return func(a *emailAction) { a.bodyTmpl = t } }

// WithHTML sets the Content-Type to text/html (default text/plain).
func WithHTML() Option { return func(a *emailAction) { a.html = true } }

// WithSender overrides the SMTP sender (test seam).
func WithSender(s sender) Option { return func(a *emailAction) { a.snd = s } }

// NewEmail returns a service action that sends one email per Do invocation.
// It renders the subject and body as text/templates over the instance variables.
func NewEmail(opts ...Option) action.ServiceAction {
	a := &emailAction{snd: smtpSender{}}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Do renders the subject and body templates, constructs the MIME message, and
// sends it via the configured sender. Returns emailSent=true on success.
func (a *emailAction) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	subject, err := render(a.subjectTmpl, in)
	if err != nil {
		return nil, action.NonRetryable(fmt.Errorf("workflow-email: subject template: %w", err))
	}
	body, err := render(a.bodyTmpl, in)
	if err != nil {
		return nil, action.NonRetryable(fmt.Errorf("workflow-email: body template: %w", err))
	}

	// Validate header values before assembly to prevent SMTP header injection.
	// Subject is attacker-influenceable via template variables; from/to are static
	// config but we guard them too for defense-in-depth.
	if err := validateHeader("subject", subject); err != nil {
		return nil, err
	}
	if err := validateHeader("from", a.from); err != nil {
		return nil, err
	}
	for _, rcpt := range a.to {
		if err := validateHeader("to", rcpt); err != nil {
			return nil, err
		}
	}

	contentType := "text/plain"
	if a.html {
		contentType = "text/html"
	}
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", a.from)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(a.to, ", "))
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: %s; charset=UTF-8\r\n\r\n", contentType)
	msg.WriteString(body)

	var auth smtp.Auth
	if a.hasAuth {
		host := a.addr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		auth = smtp.PlainAuth("", a.authUser, a.authPass, host)
	}
	if err := a.snd.send(a.addr, auth, a.from, a.to, msg.Bytes()); err != nil {
		return nil, fmt.Errorf("workflow-email: send: %w", err)
	}
	return map[string]any{"emailSent": true}, nil
}

// validateHeader returns a non-retryable error if value contains a carriage return
// or newline, preventing SMTP header injection. name is included in the error
// message so callers can identify which header field is affected.
func validateHeader(name, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return action.NonRetryable(fmt.Errorf("workflow-email: %s contains illegal newline", name))
	}
	return nil
}

func render(tmpl string, vars map[string]any) (string, error) {
	t, err := template.New("email").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, vars); err != nil {
		return "", err
	}
	return b.String(), nil
}
