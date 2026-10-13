// Package mailer renders and sends the account e-mails requested by auth.email_requested.v1.
package mailer

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"net/url"
	"path"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

//go:embed templates/*.txt templates/*.html
var _templates embed.FS

const (
	_localeVI       = "vi"
	_localeEN       = "en"
	_defaultLocale  = _localeVI
	_verifyPath     = "/verify-email"
	_resetPath      = "/reset-password"
	_minutesPerHour = 60
)

var errUnknownPurpose = errors.New("unknown e-mail purpose")

// Message is one rendered e-mail.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers a rendered message; *SMTP implements it.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Config -.
type Config struct {
	// WebBaseURL prefixes the links, e.g. http://localhost:5173.
	WebBaseURL string
	// VerifyTTL and ResetTTL are only shown in the text ("valid for 24 hours").
	VerifyTTL time.Duration
	ResetTTL  time.Duration
}

// Mailer -.
type Mailer struct {
	cfg    Config
	sender Sender
	// text holds one template set per file: every .txt file defines its own "subject" and "body".
	text map[string]*texttemplate.Template
	html *htmltemplate.Template
	l    logger.Interface
}

var _ usecase.Mail = (*Mailer)(nil)

// New parses the embedded templates. A nil sender disables sending: Deliver only logs.
func New(cfg Config, sender Sender, l logger.Interface) (*Mailer, error) {
	names, err := fs.Glob(_templates, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("mailer - New - glob: %w", err)
	}

	text := make(map[string]*texttemplate.Template, len(names))

	for _, n := range names {
		t, perr := texttemplate.ParseFS(_templates, n)
		if perr != nil {
			return nil, fmt.Errorf("mailer - New - text template %s: %w", n, perr)
		}

		text[path.Base(n)] = t
	}

	html, err := htmltemplate.ParseFS(_templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("mailer - New - html templates: %w", err)
	}

	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")

	return &Mailer{cfg: cfg, sender: sender, text: text, html: html, l: l}, nil
}

// Deliver renders and sends one e-mail. The token is part of the message only: it is never logged.
func (m *Mailer) Deliver(ctx context.Context, req usecase.MailRequest) error {
	msg, err := m.Render(req)
	if err != nil {
		return err
	}

	if m.sender == nil {
		m.l.Info("mailer - mail disabled (IDENTITY_SMTP_ADDR empty), not sent: purpose=%s userID=%s", req.Purpose, req.UserID)

		return nil
	}

	if err = m.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer - Deliver - send %s: %w", req.Purpose, err)
	}

	m.l.Info("mailer - sent: purpose=%s userID=%s", req.Purpose, req.UserID)

	return nil
}

type templateData struct {
	Username string
	Link     string
	ValidFor string
}

// Render builds the message in the request's locale (vi when unsupported).
func (m *Mailer) Render(req usecase.MailRequest) (Message, error) {
	var (
		name, page string
		ttl        time.Duration
	)

	switch req.Purpose {
	case entity.TokenVerify:
		name, page, ttl = "verify", _verifyPath, m.cfg.VerifyTTL
	case entity.TokenReset:
		name, page, ttl = "reset", _resetPath, m.cfg.ResetTTL
	default:
		return Message{}, fmt.Errorf("mailer - Render: %w: %q", errUnknownPurpose, req.Purpose)
	}

	locale := normalizeLocale(req.Locale)
	data := templateData{
		Username: req.Username,
		Link:     m.cfg.WebBaseURL + page + "?token=" + url.QueryEscape(req.Token),
		ValidFor: humanDuration(ttl, locale),
	}

	base := name + "." + locale

	subject, err := execText(m.text[base+".txt"], "subject", data)
	if err != nil {
		return Message{}, err
	}

	text, err := execText(m.text[base+".txt"], "body", data)
	if err != nil {
		return Message{}, err
	}

	var html bytes.Buffer
	if err = m.html.ExecuteTemplate(&html, base+".html", data); err != nil {
		return Message{}, fmt.Errorf("mailer - Render - html %s: %w", base, err)
	}

	return Message{To: req.Email, Subject: strings.TrimSpace(subject), Text: text, HTML: html.String()}, nil
}

func execText(t *texttemplate.Template, name string, data templateData) (string, error) {
	if t == nil {
		return "", fmt.Errorf("mailer - template %s: %w", name, errUnknownPurpose)
	}

	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("mailer - template %s: %w", name, err)
	}

	return b.String(), nil
}

func normalizeLocale(locale string) string {
	l := strings.ToLower(locale)
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}

	if l == _localeEN {
		return _localeEN
	}

	return _defaultLocale
}

func humanDuration(d time.Duration, locale string) string {
	if d <= 0 {
		return ""
	}

	unitHour, unitMinute := " giờ", " phút"
	if locale == _localeEN {
		unitHour, unitMinute = " hours", " minutes"
	}

	if minutes := int(d / time.Minute); minutes%_minutesPerHour != 0 {
		return fmt.Sprintf("%d%s", minutes, unitMinute)
	}

	return fmt.Sprintf("%d%s", int(d/time.Hour), unitHour)
}
