// Package mail renders the notification-svc e-mail templates (vi / en, text + HTML) and sends them over SMTP.
package mail

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"path"
	"strings"
	texttemplate "text/template"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
)

//go:embed templates/*.txt templates/*.html
var _templates embed.FS

// ErrUnknownTemplate: no template of that name and locale.
var ErrUnknownTemplate = errors.New("unknown e-mail template")

// _missingKey makes a template fail instead of printing "<no value>" when its data lacks a key.
const _missingKey = "missingkey=error"

// Templates are the names of every template (13), each in vi and en.
func Templates() []string {
	return []string{
		"verify_email", "reset_password", "security_lock", "live_started", "stream_starting", "schedule_changed",
		"mention", "creator_application_result", "report_result", "account_locked", "channel_suspended",
		"channel_restored", "stream_taken_down",
	}
}

// Locales of every template.
func Locales() []string { return []string{"vi", "en"} }

// Sender delivers a rendered message; *SMTP implements it.
type Sender interface {
	Send(ctx context.Context, msg entity.Message) error
}

// Mailer implements repo.Mailer.
type Mailer struct {
	sender Sender
	// text holds one template set per file: every .txt file defines its own "subject" and "body".
	text map[string]*texttemplate.Template
	html *htmltemplate.Template
}

var _ repo.Mailer = (*Mailer)(nil)

// New parses the embedded templates. A nil sender disables sending: Send returns entity.ErrMailDisabled.
func New(sender Sender) (*Mailer, error) {
	names, err := fs.Glob(_templates, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("mail - New - glob: %w", err)
	}

	text := make(map[string]*texttemplate.Template, len(names))

	for _, n := range names {
		t, perr := texttemplate.New(path.Base(n)).Option(_missingKey).ParseFS(_templates, n)
		if perr != nil {
			return nil, fmt.Errorf("mail - New - text template %s: %w", n, perr)
		}

		text[strings.TrimSuffix(path.Base(n), ".txt")] = t
	}

	html, err := htmltemplate.New("html").Option(_missingKey).ParseFS(_templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("mail - New - html templates: %w", err)
	}

	return &Mailer{sender: sender, text: text, html: html}, nil
}

// Render builds the message of a template in a locale (vi when unsupported).
func (m *Mailer) Render(template, locale, to string, data map[string]string) (entity.Message, error) {
	if locale != "en" {
		locale = "vi"
	}

	base := template + "." + locale

	t, ok := m.text[base]
	if !ok {
		return entity.Message{}, fmt.Errorf("mail - Render: %w: %s", ErrUnknownTemplate, base)
	}

	subject, err := exec(t, "subject", data)
	if err != nil {
		return entity.Message{}, err
	}

	body, err := exec(t, "body", data)
	if err != nil {
		return entity.Message{}, err
	}

	var html bytes.Buffer
	if err := m.html.ExecuteTemplate(&html, base+".html", data); err != nil {
		return entity.Message{}, fmt.Errorf("mail - Render - html %s: %w", base, err)
	}

	return entity.Message{To: to, Subject: strings.TrimSpace(subject), Text: body, HTML: html.String()}, nil
}

func exec(t *texttemplate.Template, name string, data map[string]string) (string, error) {
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("mail - template %s: %w", name, err)
	}

	return b.String(), nil
}

// Send -.
func (m *Mailer) Send(ctx context.Context, msg entity.Message) error {
	if m.sender == nil {
		return entity.ErrMailDisabled
	}

	if err := m.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("mail - Send: %w", err)
	}

	return nil
}
