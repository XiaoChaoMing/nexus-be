// Package events consumes the JetStream events identity-svc reacts to.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// Durable consumer names.
const (
	// DurableChannels keeps user_channels in sync (channel.created.v1).
	DurableChannels = "identity-svc"
	// DurableMailer sends account e-mails (auth.email_requested.v1).
	DurableMailer = "identity-mailer"
)

var errBadPayload = errors.New("bad payload")

// ChannelTypes are the channel events consumed by DurableChannels.
func ChannelTypes() []string {
	return []string{events.TypeChannelCreated}
}

// Types are every event consumed by DurableChannels: channels and safety-svc's account actions.
func Types() []string {
	return []string{events.TypeChannelCreated, events.TypeUserActioned}
}

// Handler routes the events of DurableChannels.
func Handler(sync usecase.ChannelSync, mod usecase.Moderation) eventbus.Handler {
	channels, safety := ChannelHandler(sync), SafetyHandler(mod)

	return func(ctx context.Context, e eventbus.Event) error {
		if e.Type == events.TypeUserActioned {
			return safety(ctx, e)
		}

		return channels(ctx, e)
	}
}

// SafetyHandler applies safety.user_actioned.v1 (lock / unlock).
func SafetyHandler(mod usecase.Moderation) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if e.Type != events.TypeUserActioned {
			return nil
		}

		var p events.UserActioned
		if err := decode(e, &p); err != nil {
			return err
		}

		// processed_events needs the event id to apply the action once.
		if !isUUID(e.ID) || !isUUID(p.UserID) || !isUUID(p.ActionID) {
			return skip(e, errBadPayload)
		}

		if p.Action != events.ActionLock && p.Action != events.ActionUnlock {
			return skip(e, errBadPayload)
		}

		if p.LockedUntil != nil && p.LockedUntil.IsZero() {
			p.LockedUntil = nil
		}

		return mod.Apply(ctx, e.ID, p)
	}
}

// MailTypes are consumed by DurableMailer.
func MailTypes() []string {
	return []string{events.TypeEmailRequested}
}

// ChannelHandler stores the channels channel-svc creates; redeliveries are ignored by event id.
func ChannelHandler(sync usecase.ChannelSync) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if e.Type != events.TypeChannelCreated {
			return nil
		}

		var p events.ChannelCreated
		if err := decode(e, &p); err != nil {
			return err
		}

		if !isUUID(p.ChannelID) || !isUUID(p.OwnerID) || p.Handle == "" || p.DisplayName == "" {
			return skip(e, errBadPayload)
		}

		err := sync.ChannelCreated(ctx, e.ID, entity.UserChannel{
			UserID: p.OwnerID, ChannelID: p.ChannelID, Handle: p.Handle, DisplayName: p.DisplayName,
		})
		if errors.Is(err, entity.ErrUserNotFound) || errors.Is(err, entity.ErrChannelTaken) {
			return skip(e, err)
		}

		return err
	}
}

// MailHandler sends the requested e-mail; an SMTP failure returns an error so JetStream redelivers it. A
// purpose it has no template for (SECURITY_LOCK, sent by notification-svc) is skipped.
func MailHandler(mail usecase.Mail) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if e.Type != events.TypeEmailRequested {
			return nil
		}

		var p events.EmailRequested
		if err := decode(e, &p); err != nil {
			return err
		}

		purpose := entity.TokenPurpose(p.Purpose)
		if purpose != entity.TokenVerify && purpose != entity.TokenReset {
			return skip(e, errBadPayload)
		}

		if p.Token == "" || !strings.Contains(p.Email, "@") || strings.ContainsAny(p.Email, "\r\n") {
			return skip(e, errBadPayload)
		}

		return mail.Deliver(ctx, usecase.MailRequest{
			UserID: p.UserID, Email: p.Email, Username: p.Username, Purpose: purpose, Token: p.Token, Locale: p.Locale,
		})
	}
}

// decode rejects payloads that can never be processed so they are not redelivered forever.
func decode(e eventbus.Event, into any) error {
	if err := json.Unmarshal(e.Payload, into); err != nil {
		return skip(e, err)
	}

	if e.ID != "" && !isUUID(e.ID) {
		return skip(e, errBadPayload)
	}

	return nil
}

func skip(e eventbus.Event, err error) error {
	return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)

	return err == nil
}
