package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

const _smtpTimeout = 10 * time.Second

var errHeaderInjection = errors.New("line break in a mail header")

// SMTP sends plain SMTP without authentication or TLS: the dev target is Mailpit inside the compose
// network. A production relay needs STARTTLS and auth, which this sender does not do.
type SMTP struct {
	addr string
	from string
	now  func() time.Time
}

// NewSMTP -.
func NewSMTP(addr, from string) (*SMTP, error) {
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("mail - NewSMTP - from address: %w", err)
	}

	return &SMTP{addr: addr, from: from, now: time.Now}, nil
}

// Send -.
func (s *SMTP) Send(ctx context.Context, msg entity.Message) error {
	from, err := mail.ParseAddress(s.from)
	if err != nil {
		return fmt.Errorf("mail - SMTP - from: %w", err)
	}

	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("mail - SMTP - to: %w", err)
	}

	body, err := buildMIME(from, to, msg, s.now())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, _smtpTimeout)
	defer cancel()

	var d net.Dialer

	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mail - SMTP - dial: %w", err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline) //nolint:errcheck // best effort; the dial already succeeded
	}

	return deliver(conn, s.addr, from.Address, to.Address, body)
}

func deliver(conn net.Conn, addr, from, to string, body []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close() // already failing; the client error is returned

		return fmt.Errorf("mail - SMTP - client: %w", err)
	}
	defer c.Close()

	if err = c.Mail(from); err != nil {
		return fmt.Errorf("mail - SMTP - MAIL FROM: %w", err)
	}

	if err = c.Rcpt(to); err != nil {
		return fmt.Errorf("mail - SMTP - RCPT TO: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail - SMTP - DATA: %w", err)
	}

	if _, err = w.Write(body); err != nil {
		return fmt.Errorf("mail - SMTP - write: %w", err)
	}

	if err = w.Close(); err != nil {
		return fmt.Errorf("mail - SMTP - end of data: %w", err)
	}

	if err = c.Quit(); err != nil {
		return fmt.Errorf("mail - SMTP - QUIT: %w", err)
	}

	return nil
}

// buildMIME renders a multipart/alternative message (text first, HTML preferred by clients).
func buildMIME(from, to *mail.Address, msg entity.Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return nil, errHeaderInjection
	}

	var parts bytes.Buffer

	mw := multipart.NewWriter(&parts)

	for _, p := range []struct{ contentType, body string }{
		{"text/plain; charset=UTF-8", msg.Text},
		{"text/html; charset=UTF-8", msg.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, fmt.Errorf("mail - buildMIME - part: %w", err)
		}

		qp := quotedprintable.NewWriter(w)
		if _, err = qp.Write([]byte(p.body)); err != nil {
			return nil, fmt.Errorf("mail - buildMIME - write: %w", err)
		}

		if err = qp.Close(); err != nil {
			return nil, fmt.Errorf("mail - buildMIME - close part: %w", err)
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("mail - buildMIME - close: %w", err)
	}

	var out bytes.Buffer

	fmt.Fprintf(&out, "From: %s\r\n", from.String())
	fmt.Fprintf(&out, "To: %s\r\n", to.String())
	fmt.Fprintf(&out, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", msg.Subject))
	fmt.Fprintf(&out, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&out, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&out, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mw.Boundary())
	out.Write(parts.Bytes())

	return out.Bytes(), nil
}
