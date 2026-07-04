package processtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action/email"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

func TestCaptureSender(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		to     []string
		assert func(t *testing.T, cap *processtest.CaptureSender, err error)
	}

	cases := []testCase{
		{
			name: "captures a single rendered message",
			to:   []string{"alice@example.com"},
			assert: func(t *testing.T, cap *processtest.CaptureSender, err error) {
				require.NoError(t, err)
				sent := cap.Sent()
				require.Len(t, sent, 1)
				assert.Equal(t, "ops@example.com", sent[0].From)
				assert.Equal(t, []string{"alice@example.com"}, sent[0].To)
				assert.Contains(t, string(sent[0].Msg), "Hello alice@example.com")

				last, ok := cap.Last()
				require.True(t, ok)
				assert.Equal(t, sent[0].To, last.To)
			},
		},
		{
			name: "captures one message per recipient (fan-out)",
			to:   []string{"a@example.com", "b@example.com"},
			assert: func(t *testing.T, cap *processtest.CaptureSender, err error) {
				require.NoError(t, err)
				require.Len(t, cap.Sent(), 2)
			},
		},
	}

	t.Run("Last on an empty sender reports false", func(t *testing.T) {
		t.Parallel()
		_, ok := processtest.NewCaptureSender().Last()
		assert.False(t, ok)
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cap := processtest.NewCaptureSender()
			act := email.NewEmail(
				email.WithSender(cap.SenderFunc()),
				email.WithSMTPAddr("smtp.example.com:25"),
				email.WithFrom("ops@example.com"),
				email.WithTo(tc.to...),
				email.WithSubjectTemplate("Notice"),
				email.WithBodyTemplate("Hello {{.recipient}}"),
			)
			// Render body per recipient using recipient data; the email action overlays
			// Recipient.Data over vars, but for this test the static body references a
			// var we set to the address via a resolver-free path: use the recipient's
			// own address through the To fan-out by templating on a shared var.
			_, err := act.Do(context.Background(), map[string]any{"recipient": strings.Join(tc.to, ",")})
			tc.assert(t, cap, err)
		})
	}
}
