package processtest

import (
	"net/smtp"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/action/email"
)

// SentEmail is one message captured by a [CaptureSender].
type SentEmail struct {
	// Addr is the SMTP server address the action would have dialed.
	Addr string
	// From is the envelope sender.
	From string
	// To is the recipient list for this message (one per message under the email
	// action's per-recipient fan-out).
	To []string
	// Msg is the fully rendered RFC 5322 message (headers + body).
	Msg []byte
}

// CaptureSender records the emails an [email] action would have sent, instead of
// dialing SMTP. Wire it into the real action with
// email.WithSender(cap.SenderFunc()) so a consumer can assert on rendered
// subjects/bodies, recipients, and per-recipient fan-out without a mail server.
//
// CaptureSender uses the exported email.SenderFunc seam — action/email needs no
// change. Safe for concurrent use.
type CaptureSender struct {
	mu   sync.Mutex
	sent []SentEmail
}

// NewCaptureSender returns an empty CaptureSender.
func NewCaptureSender() *CaptureSender {
	return &CaptureSender{}
}

// SenderFunc returns an [email.SenderFunc] that records each send and reports
// success. Pass it to email.WithSender.
func (c *CaptureSender) SenderFunc() email.SenderFunc {
	return func(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.sent = append(c.sent, SentEmail{
			Addr: addr,
			From: from,
			To:   append([]string(nil), to...),
			Msg:  append([]byte(nil), msg...),
		})
		return nil
	}
}

// Sent returns a copy of all captured messages in send order.
func (c *CaptureSender) Sent() []SentEmail {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SentEmail(nil), c.sent...)
}

// Last returns the most recently captured message and true, or a zero value and
// false when nothing has been sent.
func (c *CaptureSender) Last() (SentEmail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		return SentEmail{}, false
	}
	return c.sent[len(c.sent)-1], true
}
