package alert

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// maxSMTPRead bounds the bytes read from a server in one exchange, because
// net/smtp buffers reply lines without a limit.
const maxSMTPRead = 1 << 20

// EmailOptions configures TLS-only SMTP delivery.
type EmailOptions struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
}

type emailSender struct {
	opts  EmailOptions
	dial  func(context.Context, string, string) (net.Conn, error)
	roots *x509.CertPool
}

// NewEmail returns an SMTP sender. Settings must already be validated.
func NewEmail(opts EmailOptions) Sender {
	return &emailSender{opts: opts, dial: (&net.Dialer{}).DialContext}
}

func (s *emailSender) Name() string { return "email" }

func (s *emailSender) Send(ctx context.Context, b *Batch) (bool, error) {
	message, err := emailMessage(s.opts, b)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := s.dial(ctx, "tcp", net.JoinHostPort(s.opts.Host, strconv.Itoa(s.opts.Port)))
	if err != nil {
		return smtpFailure("connect", err)
	}
	defer conn.Close()
	conn = &limitedConn{Conn: conn, left: maxSMTPRead}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return smtpFailure("deadline", err)
	}
	tlsConfig := &tls.Config{ServerName: s.opts.Host, MinVersion: tls.VersionTLS12, RootCAs: s.roots}
	transport := conn
	if s.opts.Port == 465 {
		secured := tls.Client(conn, tlsConfig)
		if err := secured.HandshakeContext(ctx); err != nil {
			return smtpFailure("TLS", err)
		}
		transport = secured
	}
	c, err := smtp.NewClient(transport, s.opts.Host)
	if err != nil {
		return smtpFailure("greeting", err)
	}
	defer c.Close()
	if s.opts.Port != 465 {
		if err := c.Hello("localhost"); err != nil {
			return smtpFailure("hello", err)
		}
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return false, fmt.Errorf("smtp requires STARTTLS")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			var reply *textproto.Error
			if errors.As(err, &reply) {
				return false, fmt.Errorf("smtp STARTTLS refused (code %d)", reply.Code)
			}
			return smtpFailure("TLS", err)
		}
	}
	if s.opts.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.opts.Username, s.opts.Password, s.opts.Host)); err != nil {
			return smtpFailure("AUTH", err)
		}
	}
	if err := c.Mail(s.opts.From); err != nil {
		return smtpFailure("MAIL", err)
	}
	for _, to := range s.opts.To {
		if err := c.Rcpt(to); err != nil {
			return smtpFailure("RCPT", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return smtpFailure("DATA", err)
	}
	if _, err := w.Write(message); err != nil {
		return smtpFailure("body", err)
	}
	if err := w.Close(); err != nil {
		return smtpFailure("body", err)
	}
	// The server accepted the message; retrying a failed QUIT would send it twice.
	_ = c.Quit()
	return false, nil
}

// limitedConn fails reads after left bytes.
type limitedConn struct {
	net.Conn
	left int64
}

func (c *limitedConn) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("smtp reply too large")
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.Conn.Read(p)
	c.left -= int64(n)
	return n, err
}

// smtpFailure omits server text, which may echo credentials. Connection and
// TLS errors keep their cause: nothing has been authenticated yet.
func smtpFailure(stage string, err error) (bool, error) {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		return reply.Code >= 400 && reply.Code < 500, fmt.Errorf("smtp %s failed (code %d)", stage, reply.Code)
	}
	if stage == "connect" || stage == "TLS" {
		return true, fmt.Errorf("smtp %s failed: %w", stage, err)
	}
	var network net.Error
	retry := errors.As(err, &network) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	return retry, fmt.Errorf("smtp %s failed", stage)
}

func emailMessage(opts EmailOptions, b *Batch) ([]byte, error) {
	from, err := mail.ParseAddress(opts.From)
	if err != nil || from.Name != "" || from.Address != opts.From || strings.ContainsAny(opts.From, "\r\n") {
		return nil, fmt.Errorf("invalid email from address")
	}
	var recipients []string
	for _, value := range opts.To {
		to, err := mail.ParseAddress(value)
		if err != nil || to.Name != "" || to.Address != value || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("invalid email to address")
		}
		recipients = append(recipients, to.String())
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("email Message-ID generation failed")
	}
	domain := opts.From[strings.LastIndexByte(opts.From, '@')+1:]
	date := b.SentAt
	if date.IsZero() {
		date = time.Now()
	}
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\n", from.String(), strings.Join(recipients, ", "), mime.QEncoding.Encode("utf-8", emailSubject(b)), date.Format(time.RFC1123Z), hex.EncodeToString(id[:]), domain)
	message.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\nAuto-Submitted: auto-generated\r\n\r\n")
	w := quotedprintable.NewWriter(&message)
	if _, err := w.Write([]byte(b.Text)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return message.Bytes(), nil
}

func emailSubject(b *Batch) string {
	subject := "[opsgate] "
	switch len(b.Alerts) {
	case 0:
		subject += fmt.Sprintf("%d alerts dropped", b.Dropped)
	case 1:
		subject += renderAlerts(&Batch{Alerts: b.Alerts}, false)
	default:
		warnings := 0
		var clusters []string
		for _, a := range b.Alerts {
			if a.Severity == Warning {
				warnings++
			}
			if !slices.Contains(clusters, a.Cluster) {
				clusters = append(clusters, a.Cluster)
			}
		}
		slices.Sort(clusters)
		subject += fmt.Sprintf("%d warnings, %d changes in %s", warnings, len(b.Alerts)-warnings, strings.Join(clusters, ", "))
	}
	subject = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, subject)
	runes := []rune(subject)
	if len(runes) > 120 {
		subject = string(runes[:120])
	}
	return subject
}
