package alert

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func emailCertificate(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}, roots
}

type smtpScenario struct {
	implicit, noSTARTTLS                       bool
	startTLSCode, authCode, mailCode, quitCode int
}

type smtpResult struct {
	commands           []string
	plaintextSensitive bool
	authenticated      bool
	message            []byte
}

// pipeSMTP implements only the commands used by the sender.
func pipeSMTP(t *testing.T, cert tls.Certificate, scenario smtpScenario) (func(context.Context, string, string) (net.Conn, error), <-chan smtpResult) {
	t.Helper()
	client, server := net.Pipe()
	result := make(chan smtpResult, 1)
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() {
		var got smtpResult
		defer func() { server.Close(); result <- got }()
		server.SetDeadline(time.Now().Add(5 * time.Second))
		conn := server
		secured := false
		upgrade := func() bool {
			conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
			if err := conn.(*tls.Conn).Handshake(); err != nil {
				return false
			}
			secured = true
			return true
		}
		if scenario.implicit && !upgrade() {
			return
		}
		reader := textproto.NewReader(bufio.NewReader(conn))
		reply := func(value string) bool { _, err := io.WriteString(conn, value+"\r\n"); return err == nil }
		if !reply("220 smtp.example.com ready") {
			return
		}
		for {
			line, err := reader.ReadLine()
			if err != nil {
				return
			}
			verb, arg, _ := strings.Cut(line, " ")
			got.commands = append(got.commands, verb)
			if !secured && (verb == "AUTH" || verb == "MAIL" || verb == "RCPT" || verb == "DATA") {
				got.plaintextSensitive = true
			}
			switch verb {
			case "EHLO", "HELO":
				if !secured && !scenario.noSTARTTLS {
					if !reply("250-smtp.example.com\r\n250 STARTTLS") {
						return
					}
				} else if !reply("250-smtp.example.com\r\n250 AUTH PLAIN") {
					return
				}
			case "STARTTLS":
				if scenario.startTLSCode != 0 {
					reply(fmt.Sprintf("%d refused", scenario.startTLSCode))
					continue
				}
				if !reply("220 begin TLS") || !upgrade() {
					return
				}
				reader = textproto.NewReader(bufio.NewReader(conn))
			case "AUTH":
				raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(arg, "PLAIN "))
				got.authenticated = string(raw) == "\x00alerts@example.com\x00test-password"
				if scenario.authCode != 0 {
					// A hostile server can echo the password in its reply.
					if !reply(fmt.Sprintf("%d test-password", scenario.authCode)) {
						return
					}
				} else if !reply("235 authenticated") {
					return
				}
			case "MAIL":
				if scenario.mailCode != 0 {
					if !reply(fmt.Sprintf("%d test-password", scenario.mailCode)) {
						return
					}
				} else if !reply("250 accepted") {
					return
				}
			case "RCPT":
				if !reply("250 accepted") {
					return
				}
			case "DATA":
				if !reply("354 send message") {
					return
				}
				got.message, err = reader.ReadDotBytes()
				if err != nil || !reply("250 queued") {
					return
				}
			case "QUIT":
				if scenario.quitCode != 0 {
					reply(fmt.Sprintf("%d closing", scenario.quitCode))
					return
				}
				reply("221 bye")
				io.Copy(io.Discard, conn)
				return
			default:
				reply("500 unknown command")
			}
		}
	}()
	return func(context.Context, string, string) (net.Conn, error) { return client, nil }, result
}

func testEmail() EmailOptions {
	return EmailOptions{Host: "smtp.example.com", Port: 587, Username: "alerts@example.com", Password: "test-password", From: "alerts@example.com", To: []string{"me@example.com"}}
}

func TestEmailSMTP(t *testing.T) {
	cert, roots := emailCertificate(t, "smtp.example.com")
	wrongCert, wrongRoots := emailCertificate(t, "other.example.com")
	for _, tc := range []struct {
		name                                                       string
		scenario                                                   smtpScenario
		wrongHost, noAuth, wantError, retry, wantAuth, wantMessage bool
	}{
		{name: "STARTTLS missing", scenario: smtpScenario{noSTARTTLS: true}, wantError: true},
		{name: "STARTTLS refused", scenario: smtpScenario{startTLSCode: 454}, wantError: true},
		{name: "wrong hostname", wrongHost: true, wantError: true, retry: true},
		{name: "implicit wrong hostname", scenario: smtpScenario{implicit: true}, wrongHost: true, wantError: true, retry: true},
		{name: "STARTTLS", wantAuth: true, wantMessage: true},
		{name: "no authentication", noAuth: true, wantMessage: true},
		{name: "implicit TLS", scenario: smtpScenario{implicit: true}, wantAuth: true, wantMessage: true},
		{name: "rejected password", scenario: smtpScenario{authCode: 535}, wantError: true, wantAuth: true},
		{name: "temporary MAIL", scenario: smtpScenario{mailCode: 451}, wantError: true, retry: true, wantAuth: true},
		{name: "failed QUIT after acceptance", scenario: smtpScenario{quitCode: 451}, wantAuth: true, wantMessage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, ca := cert, roots
			if tc.wrongHost {
				certificate, ca = wrongCert, wrongRoots
			}
			dial, results := pipeSMTP(t, certificate, tc.scenario)
			opts := testEmail()
			if tc.noAuth {
				opts.Username, opts.Password = "", ""
			}
			if tc.scenario.implicit {
				opts.Port = 465
			}
			sender := NewEmail(opts).(*emailSender)
			sender.dial, sender.roots = dial, ca
			b := &Batch{Alerts: []Alert{{Severity: Warning, Summary: "line\r\nBcc: x@example.com"}}, Text: "first\n.dot line\nlast\n2 alerts dropped"}
			retry, err := sender.Send(context.Background(), b)
			if (err != nil) != tc.wantError || retry != tc.retry {
				t.Fatalf("Send = %v, %v", retry, err)
			}
			if err != nil && strings.Contains(err.Error(), "test-password") {
				t.Fatalf("password exposed: %v", err)
			}
			got := <-results
			if got.plaintextSensitive {
				t.Fatal("credentials or envelope sent before TLS")
			}
			if got.authenticated != tc.wantAuth {
				t.Fatalf("authenticated=%v, commands=%v", got.authenticated, got.commands)
			}
			if !tc.wantAuth && !tc.wantMessage {
				for _, command := range got.commands {
					if command == "AUTH" || command == "MAIL" || command == "DATA" {
						t.Fatalf("unexpected command %s", command)
					}
				}
			}
			if tc.wantMessage {
				message, err := mail.ReadMessage(strings.NewReader(string(got.message)))
				if err != nil {
					t.Fatal(err)
				}
				if message.Header.Get("Bcc") != "" {
					t.Fatal("injected Bcc header")
				}
				if len(message.Header) != 9 {
					t.Fatalf("headers: %v", message.Header)
				}
				for _, key := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding", "Auto-Submitted"} {
					if message.Header.Get(key) == "" {
						t.Errorf("missing %s", key)
					}
				}
				if !strings.HasSuffix(message.Header.Get("Message-ID"), "@example.com>") {
					t.Fatal("wrong Message-ID domain")
				}
				body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
				if err != nil || strings.TrimSuffix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") != b.Text {
					t.Fatalf("body=%q, err=%v", body, err)
				}
			}
		})
	}
}

func TestEmailConnectionError(t *testing.T) {
	sender := NewEmail(testEmail()).(*emailSender)
	sender.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("connection refused") }
	retry, err := sender.Send(context.Background(), &Batch{})
	if !retry || err == nil || !strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "test-password") {
		t.Fatalf("Send = %v, %v", retry, err)
	}
}

func TestEmailSubject(t *testing.T) {
	b := &Batch{Alerts: []Alert{{Summary: strings.Repeat("한글", 100) + "\r\n\x00"}}}
	message, err := emailMessage(testEmail(), b)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(message)))
	if err != nil {
		t.Fatal(err)
	}
	encoded := parsed.Header.Get("Subject")
	subject, err := new(mime.WordDecoder).DecodeHeader(encoded)
	if err != nil || !strings.Contains(encoded, "=?utf-8?q?") || !utf8.ValidString(subject) || len([]rune(subject)) != 120 || strings.ContainsFunc(subject, unicode.IsControl) {
		t.Fatalf("subject=%q, encoded=%q, err=%v", subject, encoded, err)
	}
	b.Alerts = []Alert{{Cluster: "b", Severity: Info}, {Cluster: "a", Severity: Warning}, {Cluster: "a", Severity: Warning}}
	if got := emailSubject(b); got != "[opsgate] 2 warnings, 1 changes in a, b" {
		t.Fatal(got)
	}
	if got := emailSubject(&Batch{Dropped: 3}); got != "[opsgate] 3 alerts dropped" {
		t.Fatal(got)
	}
}

func TestEmailContextClosesConversation(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	s := NewEmail(testEmail()).(*emailSender)
	s.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
			t.Error("dial lacks deadline")
		}
		return client, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retry, err := s.Send(ctx, &Batch{})
	if !retry || err == nil {
		t.Fatalf("Send = %v, %v", retry, err)
	}
	if _, err := client.Write([]byte("x")); err == nil {
		t.Fatal("connection left open")
	}
}

func TestEmailBoundsServerReply(t *testing.T) {
	client, server := net.Pipe()
	const chunk = 64 << 10
	// The server stops by itself, so a missing limit fails this test instead
	// of exhausting the memory of the machine running it.
	const sendLimit = 4 * maxSMTPRead
	written := make(chan int, 1)
	go func() {
		defer server.Close()
		total := 0
		data := []byte("220 " + strings.Repeat("A", chunk))
		for total < sendLimit {
			n, err := server.Write(data)
			total += n
			if err != nil {
				break
			}
		}
		written <- total
	}()
	s := NewEmail(testEmail()).(*emailSender)
	s.dial = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	retry, err := s.Send(context.Background(), &Batch{})
	if err == nil || retry {
		t.Fatalf("Send = retry %v, err %v; want a failure without retry", retry, err)
	}
	if total := <-written; total > maxSMTPRead+chunk+4 {
		t.Fatalf("server wrote %d bytes before the sender stopped reading", total)
	}
}

func TestSMTPFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		stage string
		err   error
		retry bool
	}{
		{"AUTH", fmt.Errorf("wrapped: %w", &textproto.Error{Code: 535, Msg: "test-password"}), false},
		{"MAIL", fmt.Errorf("wrapped: %w", &textproto.Error{Code: 451, Msg: "test-password"}), true},
		{"AUTH", errors.New("smtp: server doesn't support AUTH test-password"), false},
		{"body", io.EOF, true},
		{"body", &net.OpError{Op: "write", Net: "tcp", Err: errors.New("test-password")}, true},
		{"TLS", errors.New("x509: certificate is valid for other.example.com"), true},
	} {
		retry, err := smtpFailure(tc.stage, tc.err)
		if retry != tc.retry || err == nil || strings.Contains(err.Error(), "test-password") {
			t.Fatalf("smtpFailure(%s) = %v, %v", tc.stage, retry, err)
		}
	}
	if _, err := smtpFailure("TLS", errors.New("x509: bad certificate")); !strings.Contains(err.Error(), "x509: bad certificate") {
		t.Fatalf("TLS cause lost: %v", err)
	}
}
