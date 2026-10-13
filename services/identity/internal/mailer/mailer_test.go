package mailer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/mailer"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _token = "tok_SECRET-abc_123"

var errSMTPDown = errors.New("smtp down")

// recLogger records every formatted message.
type recLogger struct {
	mu    sync.Mutex
	lines []string
}

func (r *recLogger) add(message any, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lines = append(r.lines, fmt.Sprintf(fmt.Sprint(message), args...))
}

func (r *recLogger) Debug(message any, args ...any)   { r.add(message, args...) }
func (r *recLogger) Info(message string, args ...any) { r.add(message, args...) }
func (r *recLogger) Warn(message string, args ...any) { r.add(message, args...) }
func (r *recLogger) Error(message any, args ...any)   { r.add(message, args...) }
func (r *recLogger) Fatal(message any, args ...any)   { r.add(message, args...) }

func (r *recLogger) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return strings.Join(r.lines, "\n")
}

type fakeSender struct {
	err  error
	sent []mailer.Message
}

func (f *fakeSender) Send(_ context.Context, msg mailer.Message) error {
	if f.err != nil {
		return f.err
	}

	f.sent = append(f.sent, msg)

	return nil
}

func newMailer(t *testing.T, sender mailer.Sender, l *recLogger) *mailer.Mailer {
	t.Helper()

	m, err := mailer.New(mailer.Config{
		WebBaseURL: "http://localhost:5173/", VerifyTTL: 24 * time.Hour, ResetTTL: 30 * time.Minute,
	}, sender, l)
	require.NoError(t, err)

	return m
}

func request(purpose entity.TokenPurpose, locale string) usecase.MailRequest {
	return usecase.MailRequest{
		UserID: "u1", Email: "viewer@example.com", Username: "viewer_1", Purpose: purpose, Token: _token, Locale: locale,
	}
}

func TestRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		purpose     entity.TokenPurpose
		locale      string
		wantSubject string
		wantLink    string
		wantText    string
	}{
		{"verify vi", entity.TokenVerify, "vi", "Xác minh email tài khoản NexusLive", "http://localhost:5173/verify-email?token=" + _token, "24 giờ"},
		{"verify en", entity.TokenVerify, "en", "Verify your NexusLive e-mail", "http://localhost:5173/verify-email?token=" + _token, "24 hours"},
		{"reset vi", entity.TokenReset, "vi", "Đặt lại mật khẩu NexusLive", "http://localhost:5173/reset-password?token=" + _token, "30 phút"},
		{"reset en-US", entity.TokenReset, "en-US", "Reset your NexusLive password", "http://localhost:5173/reset-password?token=" + _token, "30 minutes"},
		{"unknown locale falls back to vi", entity.TokenVerify, "fr", "Xác minh email tài khoản NexusLive", "http://localhost:5173/verify-email?token=" + _token, "24 giờ"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newMailer(t, nil, &recLogger{})

			msg, err := m.Render(request(tc.purpose, tc.locale))
			require.NoError(t, err)
			assert.Equal(t, tc.wantSubject, msg.Subject)
			assert.Equal(t, "viewer@example.com", msg.To)
			assert.Contains(t, msg.Text, tc.wantLink)
			assert.Contains(t, msg.Text, tc.wantText)
			assert.Contains(t, msg.Text, "viewer_1")
			assert.Contains(t, msg.HTML, `href="`+tc.wantLink+`"`)
		})
	}
}

func TestRender_EscapesHTML(t *testing.T) {
	t.Parallel()

	m := newMailer(t, nil, &recLogger{})
	req := request(entity.TokenVerify, "en")
	req.Username = "<script>x</script>"

	msg, err := m.Render(req)
	require.NoError(t, err)
	assert.NotContains(t, msg.HTML, "<script>")
}

func TestDeliver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sender   *fakeSender
		purpose  entity.TokenPurpose
		wantErr  error
		wantSent int
		wantLog  string
	}{
		{name: "sent", sender: &fakeSender{}, purpose: entity.TokenVerify, wantSent: 1, wantLog: "sent"},
		{name: "disabled", purpose: entity.TokenReset, wantLog: "mail disabled"},
		{name: "smtp error is returned for redelivery", sender: &fakeSender{err: errSMTPDown}, purpose: entity.TokenVerify, wantErr: errSMTPDown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := &recLogger{}

			var sender mailer.Sender
			if tc.sender != nil {
				sender = tc.sender
			}

			err := newMailer(t, sender, l).Deliver(context.Background(), request(tc.purpose, "vi"))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.NotContains(t, err.Error(), _token)
			} else {
				require.NoError(t, err)
			}

			if tc.sender != nil {
				assert.Len(t, tc.sender.sent, tc.wantSent)
			}

			assert.Contains(t, l.all(), tc.wantLog)
			assert.NotContains(t, l.all(), _token, "the token is never logged")
		})
	}
}

func TestBuildMIME(t *testing.T) {
	t.Parallel()

	m := newMailer(t, nil, &recLogger{})
	msg, err := m.Render(request(entity.TokenVerify, "vi"))
	require.NoError(t, err)

	raw, err := mailer.BuildMIMEForTest("NexusLive <no-reply@nexuslive.dev>", msg)
	require.NoError(t, err)

	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	assert.Equal(t, msg.Subject, subject)

	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", mediaType)

	mr := multipart.NewReader(parsed.Body, params["boundary"])

	var types []string

	for {
		p, perr := mr.NextPart() // decodes quoted-printable
		if errors.Is(perr, io.EOF) {
			break
		}

		require.NoError(t, perr)

		body, rerr := io.ReadAll(p)
		require.NoError(t, rerr)
		assert.Contains(t, string(body), "verify-email?token="+_token)

		types = append(types, p.Header.Get("Content-Type"))
	}

	assert.Equal(t, []string{"text/plain; charset=UTF-8", "text/html; charset=UTF-8"}, types)
}
