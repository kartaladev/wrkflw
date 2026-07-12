package email_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/kartaladev/wrkflw/action/email"
)

func TestEmailSendsViaMailpit(t *testing.T) {
	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("1025/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(t.Context()) })

	host, err := c.Host(t.Context())
	require.NoError(t, err)
	smtpPort, err := c.MappedPort(t.Context(), "1025")
	require.NoError(t, err)
	apiPort, err := c.MappedPort(t.Context(), "8025")
	require.NoError(t, err)

	a := email.NewEmail(
		email.WithSMTPAddr(host+":"+smtpPort.Port()),
		email.WithFrom("no-reply@example.com"),
		email.WithTo("user@example.com"),
		email.WithSubjectTemplate("Order {{.orderID}}"),
		email.WithBodyTemplate("Hi {{.name}}"),
	)
	out, err := a.Do(t.Context(), map[string]any{"orderID": "A-1", "name": "Ada"})
	require.NoError(t, err)
	require.Equal(t, true, out["emailSent"])

	// Poll mailpit's HTTP API for the delivered message.
	var total int
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + host + ":" + apiPort.Port() + "/api/v1/messages")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct {
			Total int `json:"total"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		total = body.Total
		return total >= 1
	}, 10*time.Second, 200*time.Millisecond)
	require.GreaterOrEqual(t, total, 1)
}
