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

// ChannelTypes are consumed by DurableChannels.
func ChannelTypes() []string {
	return []string{events.TypeChannelCreated}
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

// MailHandler sends the requested e-mail; an SMTP failure returns an error so JetStream redelivers it.
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
