package email_test

import (
	"context"
	"fmt"
	"net/smtp"

	"github.com/kartaladev/wrkflw/action/email"
)

func ExampleNewEmail() {
	// Inject a fake sender so the example is hermetic.
	a := email.NewEmail(
		email.WithFrom("no-reply@example.com"),
		email.WithTo("user@example.com"),
		email.WithSubjectTemplate("Welcome {{.name}}"),
		email.WithBodyTemplate("Hello {{.name}}!"),
		email.WithSender(email.SenderFunc(func(string, smtp.Auth, string, []string, []byte) error { return nil })),
	)
	out, _ := a.Do(context.Background(), map[string]any{"name": "Ada"})
	fmt.Println(out["emailSent"])
	// Output: true
}
