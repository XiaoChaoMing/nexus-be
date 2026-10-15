package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase/notify"
	"github.com/stretchr/testify/require"
)

func TestBackoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempts int
		wait     time.Duration
		retry    bool
	}{
		{1, time.Minute, true},
		{2, 5 * time.Minute, true},
		{3, 15 * time.Minute, true},
		{4, time.Hour, true},
		{5, 0, false},
		{0, 0, false},
	}

	for _, tc := range tests {
		wait, retry := notify.Backoff(tc.attempts)
		require.Equal(t, tc.wait, wait, tc.attempts)
		require.Equal(t, tc.retry, retry, tc.attempts)
	}
}

func TestSendDueEmails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		attempts  int
		sendErr   error
		renderErr error
		sent      bool
		failed    bool
		next      time.Duration
		final     bool
	}{
		{name: "sent", sent: true},
		{name: "first failure retries in 1 min", sendErr: errSMTP, failed: true, next: time.Minute},
		{name: "fourth failure retries in 60 min", attempts: 3, sendErr: errSMTP, failed: true, next: time.Hour},
		{name: "fifth failure is final", attempts: 4, sendErr: errSMTP, failed: true, final: true},
		{name: "template error fails at once", renderErr: errBroken, failed: true, final: true},
		{name: "mail disabled marks sent", sendErr: entity.ErrMailDisabled, sent: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			e.mailer.sendErr, e.mailer.renderErr = tc.sendErr, tc.renderErr
			e.store.due = []entity.OutboxEmail{{ID: "m1", To: "an@x.dev", Template: "live_started", Locale: "vi", Attempts: tc.attempts}}

			n, err := e.uc.SendDueEmails(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.sent, n == 1)
			require.Equal(t, tc.sent, len(e.store.sent) == 1)
			require.Equal(t, tc.failed, len(e.store.failed) == 1)

			if !tc.failed {
				return
			}

			f := e.store.failed[0]
			require.Equal(t, tc.attempts+1, f.attempts)
			require.NotContains(t, f.text, "an@x.dev")

			if tc.final {
				require.Nil(t, f.next)
			} else {
				require.Equal(t, now.Add(tc.next), *f.next)
			}
		})
	}
}

func TestAuthMail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		purpose string
		subject string
		link    string
		err     error
	}{
		{events.EmailPurposeVerify, "verify_email.en", "http://web.test/verify-email?token=t%2B1", nil},
		{events.EmailPurposeReset, "reset_password.en", "http://web.test/reset-password?token=t%2B1", nil},
		{events.EmailPurposeSecurityLock, "security_lock.en", "http://web.test/forgot-password", nil},
		{"OTHER", "", "", entity.ErrUnknownPurpose},
	}

	for _, tc := range tests {
		t.Run(tc.purpose, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			err := e.uc.Deliver(context.Background(), events.EmailRequested{
				UserID: viewerID, Email: "an@x.dev", Username: "an", Purpose: tc.purpose, Token: "t+1", Locale: "en-US",
			})

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.subject, e.mailer.sent[0].Subject)
			require.Equal(t, tc.link, e.mailer.data[0]["Link"])
		})
	}

	t.Run("SMTP down is returned (redelivered)", func(t *testing.T) {
		t.Parallel()

		e := newEnv()
		e.mailer.sendErr = errSMTP

		err := e.uc.Deliver(context.Background(), events.EmailRequested{
			UserID: viewerID, Email: "an@x.dev", Purpose: events.EmailPurposeVerify, Token: "t",
		})
		require.ErrorIs(t, err, errSMTP)
	})
}
