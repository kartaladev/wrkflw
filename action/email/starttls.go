package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
)

// startTLSSender dials plaintext SMTP, performs EHLO, REQUIRES the STARTTLS
// extension (errors if absent — no silent plaintext fallback), upgrades to TLS
// via StartTLS, then sends the message. Selected by [WithStartTLS].
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
