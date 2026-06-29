// Package email provides a service action that sends an email over SMTP, rendering
// the subject and body as text/templates over the instance variables.
//
// # Recipient resolution
//
// Recipients may be configured statically via [WithTo] or resolved at send time via
// [WithRecipientResolver]. Both sources are combined; each recipient receives an
// INDIVIDUAL message (they do not see each other's addresses).
//
// A [RecipientResolver] may carry per-recipient [Recipient.Data] that is overlaid
// over the instance variables before template rendering, enabling personalized
// subject/body per recipient. Recipient.Data takes precedence over instance vars.
//
// # Best-effort delivery and at-least-once semantics
//
// [Do] iterates recipients in order and continues on per-recipient failures
// (best-effort). If any recipient fails, the aggregated error is returned as a
// plain (retryable) error so the runtime can retry the whole action. Retries may
// resend to already-notified recipients — at-least-once is the guarantee.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
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

// smtpSender is the default plain-SMTP sender wrapping smtp.SendMail.
type smtpSender struct{}

func (smtpSender) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

// startTLSSender dials plaintext SMTP, performs EHLO, REQUIRES the STARTTLS
// extension (errors if absent — no silent plaintext fallback), upgrades to TLS
// via StartTLS, then sends the message.
type startTLSSender struct {
	cfg *tls.Config
}

func (s startTLSSender) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Fallback for bare hostnames without port.
		host = addr
	}

	// Derive effective TLS config: clone caller's config (or start from scratch)
	// and fill in ServerName from the SMTP host if the caller left it blank.
	cfg := s.cfg
	if cfg == nil {
		cfg = &tls.Config{ServerName: host} //nolint:gosec // ServerName set from addr
	} else if cfg.ServerName == "" {
		// Clone so we don't mutate the caller's config.
		c := cfg.Clone()
		c.ServerName = host
		cfg = c
	}

	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("workflow-email: dial %s: %w", addr, err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("workflow-email: EHLO: %w", err)
	}

	// ENFORCE STARTTLS: reject the session if the server does not advertise it.
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("workflow-email: server does not support STARTTLS")
	}

	if err := c.StartTLS(cfg); err != nil {
		return fmt.Errorf("workflow-email: STARTTLS: %w", err)
	}

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("workflow-email: auth: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("workflow-email: MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("workflow-email: RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("workflow-email: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("workflow-email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("workflow-email: close DATA: %w", err)
	}
	return c.Quit()
}

// tlsSender dials implicit TLS (e.g. port 465) via tls.Dial, wraps the connection
// in smtp.NewClient, then sends the message.
type tlsSender struct {
	cfg *tls.Config
}

func (s tlsSender) send(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	cfg := s.cfg
	if cfg == nil {
		cfg = &tls.Config{ServerName: host} //nolint:gosec // ServerName set from addr
	} else if cfg.ServerName == "" {
		c := cfg.Clone()
		c.ServerName = host
		cfg = c
	}

	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("workflow-email: TLS dial %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("workflow-email: SMTP client: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("workflow-email: EHLO: %w", err)
	}

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("workflow-email: auth: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("workflow-email: MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("workflow-email: RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("workflow-email: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("workflow-email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("workflow-email: close DATA: %w", err)
	}
	return c.Quit()
}

// Recipient is a single destination address together with optional per-recipient
// template data. When [Recipient.Data] is non-nil its entries are overlaid over
// the instance variables before rendering the subject and body for THIS recipient,
// so individual messages can be personalized (e.g. greeting by name). Recipient
// data wins over instance variables on key conflicts.
type Recipient struct {
	Address string
	Data    map[string]any
}

// RecipientResolver loads recipients at send time. It may perform I/O (e.g. a DB
// lookup) and MUST honour ctx — if the context is cancelled, return its error.
// The returned error is propagated as-is from [Do], so wrapping it with
// [action.NonRetryable] prevents the runtime from retrying.
type RecipientResolver func(ctx context.Context, vars map[string]any) ([]Recipient, error)

// tlsMode selects which TLS sender is constructed by NewEmail.
type tlsMode int

const (
	tlsModeNone     tlsMode = iota // default: use smtpSender (smtp.SendMail)
	tlsModeStartTLS                // STARTTLS (ENFORCED) — fails if server does not advertise it
	tlsModeImplicit                // Implicit TLS (tls.Dial, e.g. port 465)
)

// Option configures an email action.
type Option func(*emailAction)

type emailAction struct {
	addr        string
	authUser    string
	authPass    string
	hasAuth     bool
	from        string
	to          []string
	resolver    RecipientResolver // optional; called at Do time to append recipients
	subjectTmpl string
	bodyTmpl    string
	html        bool
	snd         sender // nil → auto-selected in NewEmail by tlsMode
	explicitSnd bool   // true when caller used WithSender (highest precedence)
	mode        tlsMode
	tlsCfg      *tls.Config
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

// WithTLS selects implicit-TLS mode (port 465): the sender opens a tls.Dial connection
// and then uses smtp.NewClient over it. This enforces TLS at the transport layer.
//
// WithTLS and WithStartTLS are mutually exclusive. If both are supplied, last-wins: the
// final option in the call to NewEmail determines the mode. Use WithTLSConfig to supply
// a custom *tls.Config; the default derives ServerName from the SMTP address.
func WithTLS() Option { return func(a *emailAction) { a.mode = tlsModeImplicit } }

// WithStartTLS selects STARTTLS mode: the sender dials plaintext SMTP, performs EHLO,
// and REQUIRES the server to advertise the STARTTLS extension. If the server does NOT
// advertise STARTTLS, Do returns an error — the sender never falls back to plaintext.
// Once STARTTLS is confirmed and negotiated, auth (if configured) and the message are
// sent over the encrypted channel.
//
// WithTLS and WithStartTLS are mutually exclusive. Last-wins if both appear. Use
// WithTLSConfig to override the *tls.Config used during the TLS handshake.
func WithStartTLS() Option { return func(a *emailAction) { a.mode = tlsModeStartTLS } }

// WithTLSConfig overrides the *tls.Config used by the TLS senders (WithTLS and
// WithStartTLS). If ServerName is empty in the supplied config, it is derived from
// the SMTP address at send time. The config is cloned before ServerName is filled in,
// so the caller's value is never mutated.
//
// Typical use: supply &tls.Config{InsecureSkipVerify: true} in tests that use
// self-signed certificates.
func WithTLSConfig(cfg *tls.Config) Option { return func(a *emailAction) { a.tlsCfg = cfg } }

// WithFrom sets the envelope/From address.
func WithFrom(addr string) Option { return func(a *emailAction) { a.from = addr } }

// WithTo sets static recipient addresses. Each address receives an INDIVIDUAL
// message — recipients do not see each other's addresses in the To header.
// Static addresses are combined with any [WithRecipientResolver] results.
func WithTo(addrs ...string) Option { return func(a *emailAction) { a.to = addrs } }

// WithRecipientResolver registers a [RecipientResolver] that is called at send time
// to produce additional recipients. Resolver results are appended after any static
// [WithTo] addresses. Per-recipient [Recipient.Data] is merged over instance vars
// (recipient data wins) before rendering the subject and body for that recipient.
func WithRecipientResolver(r RecipientResolver) Option {
	return func(a *emailAction) { a.resolver = r }
}

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

// WithSender overrides the SMTP sender (test seam). WithSender takes highest
// precedence: it is always used regardless of WithTLS or WithStartTLS.
func WithSender(s sender) Option {
	return func(a *emailAction) {
		a.snd = s
		a.explicitSnd = true
	}
}

// NewEmail returns a service action that sends one email per recipient per Do
// invocation. Recipients are resolved from [WithTo] static addresses and any
// [WithRecipientResolver] results (combined, in that order). Each recipient
// receives an individual message rendered from their own template environment
// (instance vars overlaid with per-recipient [Recipient.Data]).
//
// TLS sender selection (precedence, highest first):
//  1. [WithSender] — explicit override, always wins.
//  2. [WithStartTLS] — selects startTLSSender (STARTTLS enforced).
//  3. [WithTLS] — selects tlsSender (implicit TLS via tls.Dial).
//  4. Default — smtpSender (smtp.SendMail, plaintext).
//
// When both [WithTLS] and [WithStartTLS] appear, last-wins determines the mode.
func NewEmail(opts ...Option) action.ServiceAction {
	a := &emailAction{}
	for _, o := range opts {
		o(a)
	}
	// Resolve sender: explicit WithSender wins; otherwise select by TLS mode.
	if !a.explicitSnd {
		switch a.mode {
		case tlsModeStartTLS:
			a.snd = startTLSSender{cfg: a.tlsCfg}
		case tlsModeImplicit:
			a.snd = tlsSender{cfg: a.tlsCfg}
		default:
			a.snd = smtpSender{}
		}
	}
	return a
}

// Do resolves recipients, renders per-recipient templates, and sends an individual
// message to each recipient. It honours context cancellation between sends.
//
// Recipient resolution: static [WithTo] addresses come first, followed by
// [WithRecipientResolver] results. If the resolver errors, Do returns that error
// as-is (preserving any [action.NonRetryable] wrapping) without sending.
//
// Personalization: per-recipient [Recipient.Data] is shallow-merged over instance
// vars before rendering subject and body; recipient data wins on key conflicts.
//
// Best-effort delivery: send failures are collected and the loop continues to the
// next recipient. After the loop, any collected errors are joined and returned as a
// plain (retryable) error so the runtime can retry. Retries may re-send to already-
// notified recipients — at-least-once is the guarantee.
//
// Returns map[string]any{"emailSent":true,"recipientCount":<n>} on full success.
func (a *emailAction) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	// --- Build effective recipient list ---
	// Start with static addresses converted to Recipient values.
	recipients := make([]Recipient, 0, len(a.to))
	for _, addr := range a.to {
		recipients = append(recipients, Recipient{Address: addr})
	}
	// Append resolver results; propagate resolver errors immediately (no sends).
	if a.resolver != nil {
		resolved, err := a.resolver(ctx, in)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, resolved...)
	}

	if len(recipients) == 0 {
		return nil, action.NonRetryable(fmt.Errorf("workflow-email: no recipients"))
	}

	// --- Validate from address (static config — validate once) ---
	if err := validateHeader("from", a.from); err != nil {
		return nil, err
	}

	// --- Build SMTP auth (unchanged from original) ---
	var auth smtp.Auth
	if a.hasAuth {
		host := a.addr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		auth = smtp.PlainAuth("", a.authUser, a.authPass, host)
	}

	contentType := "text/plain"
	if a.html {
		contentType = "text/html"
	}

	// --- Per-recipient send loop (best-effort) ---
	var errs []error
	sent := 0

	for _, rcpt := range recipients {
		// Check for cancellation between sends.
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}

		// Build per-recipient template environment: shallow copy of in, then overlay
		// recipient.Data so recipient-specific values win over instance vars.
		env := make(map[string]any, len(in)+len(rcpt.Data))
		for k, v := range in {
			env[k] = v
		}
		for k, v := range rcpt.Data {
			env[k] = v
		}

		// Render subject and body with per-recipient env.
		subject, err := render(a.subjectTmpl, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("workflow-email: recipient %s: subject template: %w", rcpt.Address, err))
			continue
		}
		body, err := render(a.bodyTmpl, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("workflow-email: recipient %s: body template: %w", rcpt.Address, err))
			continue
		}

		// Validate headers — resolved addresses are untrusted; check all per-send.
		if err := validateHeader("subject", subject); err != nil {
			errs = append(errs, fmt.Errorf("workflow-email: recipient %s: %w", rcpt.Address, err))
			continue
		}
		if err := validateHeader("to", rcpt.Address); err != nil {
			errs = append(errs, fmt.Errorf("workflow-email: recipient %s: %w", rcpt.Address, err))
			continue
		}

		// Assemble individual MIME message (To: just this one address).
		var msg bytes.Buffer
		fmt.Fprintf(&msg, "From: %s\r\n", a.from)
		fmt.Fprintf(&msg, "To: %s\r\n", rcpt.Address)
		fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
		fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
		fmt.Fprintf(&msg, "Content-Type: %s; charset=UTF-8\r\n\r\n", contentType)
		msg.WriteString(body)

		if err := a.snd.send(a.addr, auth, a.from, []string{rcpt.Address}, msg.Bytes()); err != nil {
			errs = append(errs, fmt.Errorf("workflow-email: recipient %s: send: %w", rcpt.Address, err))
			continue
		}
		sent++
	}

	if len(errs) > 0 {
		// Return as a plain (retryable) error so transient failures are retried.
		// Note: retries may re-send to already-notified recipients (at-least-once).
		return nil, errors.Join(errs...)
	}
	return map[string]any{"emailSent": true, "recipientCount": sent}, nil
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
