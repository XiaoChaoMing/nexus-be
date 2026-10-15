package notify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

const _minutesPerHour = 60

// Deliver renders and sends one account e-mail inside the handler (never via email_outbox); an SMTP
// failure is returned so JetStream redelivers the event. The token is part of the message only.
func (uc *UseCase) Deliver(ctx context.Context, e events.EmailRequested) error {
	var (
		template, page string
		ttl            time.Duration
	)

	switch e.Purpose {
	case events.EmailPurposeVerify:
		template, page, ttl = "verify_email", "/verify-email?token=", uc.cfg.VerifyTTL
	case events.EmailPurposeReset:
		template, page, ttl = "reset_password", "/reset-password?token=", uc.cfg.ResetTTL
	case events.EmailPurposeSecurityLock:
		template, page, ttl = "security_lock", "/forgot-password", uc.cfg.LockDuration
	default:
		return fmt.Errorf("%w: %q", entity.ErrUnknownPurpose, e.Purpose)
	}

	lc := locale(e.Locale)
	link := uc.cfg.WebBaseURL + page

	if e.Purpose != events.EmailPurposeSecurityLock {
		link += url.QueryEscape(e.Token)
	}

	msg, err := uc.r.Mailer.Render(template, lc, e.Email, map[string]string{
		"Username": e.Username, keyLink: link, "ValidFor": humanDuration(ttl, lc),
	})
	if err != nil {
		return fmt.Errorf("notify - auth mail - render %s: %w", template, err)
	}

	start := uc.now()

	err = uc.r.Mailer.Send(ctx, msg)

	switch {
	case errors.Is(err, entity.ErrMailDisabled):
		uc.l.Info("notify - mail disabled, not sent: purpose=%s userID=%s", e.Purpose, e.UserID)
		uc.obs.EmailSent(template, EmailStatusOff, 0)

		return nil
	case err != nil:
		uc.obs.EmailSent(template, EmailStatusRetry, uc.now().Sub(start))

		return fmt.Errorf("notify - auth mail - send %s: %w", e.Purpose, err)
	}

	uc.obs.EmailSent(template, EmailStatusSent, uc.now().Sub(start))
	uc.l.Info("notify - auth mail sent: purpose=%s userID=%s", e.Purpose, e.UserID)

	return nil
}

func humanDuration(d time.Duration, lc string) string {
	if d <= 0 {
		return ""
	}

	unitHour, unitMinute := " giờ", " phút"
	if lc == "en" {
		unitHour, unitMinute = " hours", " minutes"
	}

	if minutes := int(d / time.Minute); minutes%_minutesPerHour != 0 {
		return fmt.Sprintf("%d%s", minutes, unitMinute)
	}

	return fmt.Sprintf("%d%s", int(d/time.Hour), unitHour)
}
