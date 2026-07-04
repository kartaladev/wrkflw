package processtest_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action/email"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

// ExampleCaptureSender demonstrates capturing emails sent by the real
// action/email action without a live SMTP server: the CaptureSender's
// SenderFunc is plugged in via email.WithSender, so every "send" is recorded
// in memory instead of hitting the network.
func ExampleCaptureSender() {
	capture := processtest.NewCaptureSender()

	act := email.NewEmail(
		email.WithSender(capture.SenderFunc()),
		email.WithSMTPAddr("smtp.example.com:25"),
		email.WithFrom("ops@example.com"),
		email.WithTo("alice@example.com"),
		email.WithSubjectTemplate("Welcome"),
		email.WithBodyTemplate("Hello {{.name}}"),
	)

	if _, err := act.Do(context.Background(), map[string]any{"name": "Alice"}); err != nil {
		fmt.Println("send error:", err)
		return
	}

	sent := capture.Sent()
	fmt.Println("captured:", len(sent))
	fmt.Println("to:", sent[0].To[0])

	last, ok := capture.Last()
	fmt.Println("last ok:", ok)
	fmt.Println("addr:", last.Addr)
	fmt.Println("from:", last.From)

	// Output:
	// captured: 1
	// to: alice@example.com
	// last ok: true
	// addr: smtp.example.com:25
	// from: ops@example.com
}
