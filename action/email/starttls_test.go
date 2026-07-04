package email_test

// STARTTLS sender integration test.
//
// Rather than configuring mailpit to use TLS (which requires injecting
// self-signed cert/key files into the container image at runtime — a fragile
// testcontainers setup that proved unreliable), we spin up an in-process
// STARTTLS-capable SMTP stub:
//
//   - Listens on a plain TCP port.
//   - Advertises STARTTLS in EHLO capabilities.
//   - On STARTTLS command: upgrades the connection using tls.Server with a
//     freshly generated self-signed cert/key pair (see generateSelfSignedCert
//     in tls_test.go).
//   - Accepts MAIL / RCPT / DATA and captures the received message bytes.
//
// This exercises the full startTLSSender.send() code path — EHLO, extension
// check, TLS handshake, auth (skipped in this test), MAIL/RCPT/DATA — without
// needing Docker. The client uses WithTLSConfig(&tls.Config{InsecureSkipVerify: true})
// because the cert is self-signed.

import (
	"bufio"
	"crypto/tls"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/action/email"
)

// startSTARTTLSStub starts an in-process SMTP stub that:
//   - Advertises STARTTLS in EHLO.
//   - Upgrades the connection to TLS when the client sends STARTTLS.
//   - Accepts the full MAIL/RCPT/DATA sequence and records the received message.
//
// Returns the listening address and a pointer to a []byte that will be
// populated with the raw DATA payload once a message is received.
func startSTARTTLSStub(t *testing.T, cert tls.Certificate) (addr string, received *[]byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("starttls stub listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}} //nolint:gosec

	var mu sync.Mutex
	var msg []byte
	received = &msg

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //nolint:errcheck
				rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))

				write := func(s string) {
					_, _ = rw.WriteString(s + "\r\n")
					_ = rw.Flush()
				}

				write("220 stub.local ESMTP")

				for {
					line, err := rw.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					upper := strings.ToUpper(line)

					switch {
					case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
						write("250-stub.local")
						write("250-SIZE 10240000")
						write("250 STARTTLS")

					case upper == "STARTTLS":
						write("220 Ready to start TLS")
						_ = rw.Flush()
						// Upgrade to TLS.
						tlsConn := tls.Server(c, tlsCfg)
						if err := tlsConn.Handshake(); err != nil {
							return
						}
						// Replace reader/writer to use the encrypted connection.
						rw = bufio.NewReadWriter(bufio.NewReader(tlsConn), bufio.NewWriter(tlsConn))
						c = tlsConn

					case strings.HasPrefix(upper, "AUTH"):
						write("235 2.7.0 Accepted")

					case strings.HasPrefix(upper, "MAIL FROM"):
						write("250 OK")

					case strings.HasPrefix(upper, "RCPT TO"):
						write("250 OK")

					case upper == "DATA":
						write("354 Start input, end with <CRLF>.<CRLF>")
						var body []byte
						for {
							dataLine, err := rw.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimRight(dataLine, "\r\n") == "." {
								break
							}
							body = append(body, []byte(dataLine)...)
						}
						mu.Lock()
						msg = body
						mu.Unlock()
						write("250 OK: message accepted")

					case upper == "QUIT":
						write("221 bye")
						return

					default:
						write("502 unrecognized")
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String(), received
}

// TestStartTLSSenderRoundTrip verifies the full STARTTLS positive path:
// EHLO advertises STARTTLS → client negotiates TLS → message delivered.
// Uses an in-process stub with a self-signed cert and InsecureSkipVerify.
func TestStartTLSSenderRoundTrip(t *testing.T) {
	cert := generateSelfSignedCert(t)
	addr, received := startSTARTTLSStub(t, cert)

	a := email.NewEmail(
		email.WithSMTPAddr(addr),
		email.WithFrom("sender@example.com"),
		email.WithTo("recipient@example.com"),
		email.WithSubjectTemplate("Hello from TLS"),
		email.WithBodyTemplate("STARTTLS round-trip body"),
		email.WithStartTLS(),
		email.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec // self-signed in test
	)

	out, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if out["emailSent"] != true {
		t.Fatalf("emailSent = %v, want true", out["emailSent"])
	}

	// Give the stub goroutine a moment to finish writing the captured message.
	var body string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(*received) > 0 {
			body = string(*received)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if body == "" {
		t.Fatal("stub did not receive any message data")
	}
	if !strings.Contains(body, "STARTTLS round-trip body") {
		t.Fatalf("message body not found in received data: %q", body)
	}
	if !strings.Contains(body, "Subject: Hello from TLS") {
		t.Fatalf("subject not found in received data: %q", body)
	}
}
