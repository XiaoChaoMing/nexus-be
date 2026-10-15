package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

const (
	// Each tick sends at most this many e-mails (NOTIFY_EMAIL_TICK batch).
	_emailBatch = 20
	// A leased row is offered again after this when the worker died while sending it.
	_emailLease    = 5 * time.Minute
	_errTextMax    = 300
	_purgeBatch    = 1000
	_doneKeep      = 30 * 24 * time.Hour
	_processedKeep = 30 * 24 * time.Hour
)

// E-mail statuses of Observer.EmailSent.
const (
	EmailStatusSent   = usecase.EmailSent
	EmailStatusRetry  = usecase.EmailRetry
	EmailStatusFailed = usecase.EmailFailed
	EmailStatusOff    = usecase.EmailOff
)

// Backoff returns the wait after the attempts-th failed attempt, or false once the row must be FAILED
// (NOTIFY_EMAIL_MAX_ATTEMPTS = 5: retries after 1, 5, 15 and 60 minutes).
func Backoff(attempts int) (time.Duration, bool) {
	steps := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
	if attempts < 1 || attempts > len(steps) {
		return 0, false
	}

	return steps[attempts-1], true
}

// SendDueEmails sends the due rows of email_outbox one by one.
func (uc *UseCase) SendDueEmails(ctx context.Context) (int, error) {
	due, err := uc.r.Emails.Due(ctx, _emailBatch, _emailLease)
	if err != nil {
		return 0, fmt.Errorf("notify - SendDueEmails: %w", err)
	}

	sent := 0

	for _, e := range due {
		if ctx.Err() != nil {
			break
		}

		ok, err := uc.sendOne(ctx, e)
		if err != nil {
			return sent, err
		}

		if ok {
			sent++
		}
	}

	return sent, nil
}

// sendOne renders and sends one row and records the outcome; a template error never heals, so it fails
// the row at once.
func (uc *UseCase) sendOne(ctx context.Context, e entity.OutboxEmail) (bool, error) {
	msg, err := uc.r.Mailer.Render(e.Template, e.Locale, e.To, e.Data)
	if err != nil {
		uc.obs.EmailSent(e.Template, EmailStatusFailed, 0)
		uc.l.Error("notify - email %s - render %s: %v", e.ID, e.Template, err)

		return false, uc.markFailed(ctx, e, e.Attempts+1, nil, "render: "+err.Error())
	}

	start := uc.now()
	err = uc.r.Mailer.Send(ctx, msg)

	if errors.Is(err, entity.ErrMailDisabled) {
		uc.l.Info("notify - mail disabled, not sent: email %s template=%s", e.ID, e.Template)
		uc.obs.EmailSent(e.Template, EmailStatusOff, 0)

		err = nil
	} else {
		uc.obs.EmailSent(e.Template, sendStatus(err, e.Attempts+1), uc.now().Sub(start))
	}

	if err == nil {
		if merr := uc.r.Emails.MarkSent(ctx, e.ID); merr != nil {
			return false, fmt.Errorf("notify - email %s - mark sent: %w", e.ID, merr)
		}

		return true, nil
	}

	attempts := e.Attempts + 1

	var next *time.Time
	if wait, retry := Backoff(attempts); retry {
		next = ptr(uc.now().Add(wait))
	}

	uc.l.Warn("notify - email %s attempt %d failed (retry: %t): %v", e.ID, attempts, next != nil, err)

	return false, uc.markFailed(ctx, e, attempts, next, err.Error())
}

func sendStatus(err error, attempts int) string {
	if err == nil {
		return EmailStatusSent
	}

	if _, retry := Backoff(attempts); retry {
		return EmailStatusRetry
	}

	return EmailStatusFailed
}

func (uc *UseCase) markFailed(ctx context.Context, e entity.OutboxEmail, attempts int, next *time.Time, text string) error {
	// last_error never keeps the recipient's address (SMTP errors may echo it).
	text = strings.ReplaceAll(text, e.To, "<recipient>")
	if len(text) > _errTextMax {
		text = text[:_errTextMax]
	}

	if err := uc.r.Emails.MarkFailed(ctx, e.ID, attempts, next, text); err != nil {
		return fmt.Errorf("notify - email %s - mark failed: %w", e.ID, err)
	}

	return nil
}

// Purge deletes notifications older than NOTIFY_RETENTION, sent / failed e-mails and processed events
// older than 30 days (BR-E18).
func (uc *UseCase) Purge(ctx context.Context) error {
	now := uc.now()

	n, err := uc.r.Notifications.Purge(ctx, now.Add(-uc.cfg.Retention), _purgeBatch)
	if err != nil {
		return fmt.Errorf("notify - Purge notifications: %w", err)
	}

	m, err := uc.r.Emails.PurgeDone(ctx, now.Add(-_doneKeep))
	if err != nil {
		return fmt.Errorf("notify - Purge emails: %w", err)
	}

	p, err := uc.r.Notifications.PurgeProcessed(ctx, now.Add(-_processedKeep))
	if err != nil {
		return fmt.Errorf("notify - Purge processed: %w", err)
	}

	if n+m+p > 0 {
		uc.l.Info("notify - purged %d notifications, %d e-mails, %d processed events", n, m, p)
	}

	return nil
}
