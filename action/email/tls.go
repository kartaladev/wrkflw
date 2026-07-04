package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
)

// tlsSender dials implicit TLS (e.g. port 465) via tls.Dial, wraps the connection
// in smtp.NewClient, then sends the message. Selected by [WithTLS].
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
