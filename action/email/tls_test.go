package email_test

// Implicit-TLS (tlsSender) integration test.
//
// We spin up an in-process TLS listener that immediately speaks SMTP (no
// plaintext preamble) to test tlsSender.send() — tls.Dial → smtp.NewClient →
// EHLO → MAIL/RCPT/DATA. generateSelfSignedCert (below) is shared with the
// STARTTLS test in starttls_test.go.

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/action/email"
)

// generateSelfSignedCert creates a self-signed TLS certificate for localhost.
// It is shared by the implicit-TLS and STARTTLS sender tests.
func generateSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return cert
}

// startImplicitTLSStub starts an in-process TLS SMTP server (implicit TLS, like port 465).
// The TLS handshake happens immediately on connection; then the server speaks plain SMTP.
func startImplicitTLSStub(t *testing.T, cert tls.Certificate) (addr string, received *[]byte) {
	t.Helper()
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}} //nolint:gosec
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("implicit TLS stub listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

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
						write("250 SIZE 10240000")

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
						write(fmt.Sprintf("502 unrecognized: %s", line))
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String(), received
}

// TestImplicitTLSSenderRoundTrip verifies the implicit TLS (port-465 style) positive path:
// tls.Dial → smtp.NewClient → EHLO → MAIL/RCPT/DATA → message delivered.
// Uses an in-process TLS stub with a self-signed cert and InsecureSkipVerify.
func TestImplicitTLSSenderRoundTrip(t *testing.T) {
	cert := generateSelfSignedCert(t)
	addr, received := startImplicitTLSStub(t, cert)

	a := email.NewEmail(
		email.WithSMTPAddr(addr),
		email.WithFrom("sender@example.com"),
		email.WithTo("recipient@example.com"),
		email.WithSubjectTemplate("Implicit TLS subject"),
		email.WithBodyTemplate("Implicit TLS round-trip body"),
		email.WithTLS(),
		email.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec // self-signed in test
	)

	out, err := a.Do(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if out["emailSent"] != true {
		t.Fatalf("emailSent = %v, want true", out["emailSent"])
	}

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
	if !strings.Contains(body, "Implicit TLS round-trip body") {
		t.Fatalf("message body not found in received data: %q", body)
	}
}
