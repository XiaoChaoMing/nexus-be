// Package events consumes the NATS events of notification-svc: the copies (notification-replica), the
// notification sources (notification-svc) and the account e-mails (notification-auth-mailer).
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// Durable consumer names (TSD §3.2).
const (
	// DurableReplica builds the copies from the start of the stream (DeliverAll).
	DurableReplica = "notification-replica"
	// DurableSources creates notifications; DeliverNew so the first start does not replay 7 days of history.
	DurableSources = "notification-svc"
	// DurableAuthMailer sends the account e-mails (DeliverNew).
	DurableAuthMailer = "notification-auth-mailer"
)

var errBadPayload = errors.New("bad payload")

// ReplicaTypes are consumed by DurableReplica.
func ReplicaTypes() []string {
	return []string{
		events.TypeUserRegistered, events.TypeUserProfileUpdated, events.TypeUserEmailVerified, events.TypeUserLocked,
		events.TypeUserUnlocked, events.TypeUserBlocked, events.TypeUserUnblocked,
		events.TypeChannelCreated, events.TypeChannelUpdated, events.TypeChannelSuspended, events.TypeChannelRestored,
		events.TypeFollowCreated, events.TypeFollowUpdated, events.TypeFollowDeleted,
	}
}

// SourceTypes are consumed by DurableSources.
func SourceTypes() []string {
	return []string{
		events.TypeStreamStarted, events.TypeScheduleReminderDue, events.TypeScheduleCreated,
		events.TypeScheduleOccurrenceChanged, events.TypeChatMention, events.TypeFollowCreated, events.TypeCreatorApproved,
		events.TypeCreatorRejected, events.TypeReportResolved, events.TypeUserLocked, events.TypeChannelSuspended,
		events.TypeChannelRestored, events.TypeStreamTakedown,
	}
}

// AuthMailTypes are consumed by DurableAuthMailer.
func AuthMailTypes() []string { return []string{events.TypeEmailRequested} }

func skip(e eventbus.Event, err error) error {
	return fmt.Errorf("%w: %s %s: %w", eventbus.ErrSkip, e.Type, e.ID, err)
}

// decode unmarshals the payload and checks that each id is a UUID; anything else is skipped.
func decode(e eventbus.Event, into any, ids ...*string) error {
	if err := json.Unmarshal(e.Payload, into); err != nil {
		return skip(e, err)
	}

	for _, id := range ids {
		if uuid.Validate(*id) != nil {
			return skip(e, fmt.Errorf("%w: id %q", errBadPayload, *id))
		}
	}

	return nil
}

func occurredAt(e eventbus.Event) time.Time {
	if e.OccurredAt.IsZero() {
		return time.Now()
	}

	return e.OccurredAt
}

// ReplicaHandler keeps the copies; a copy synced after the event is left alone, so replays are harmless.
func ReplicaHandler(uc usecase.Replica) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		at := occurredAt(e)

		switch {
		case strings.HasPrefix(e.Type, "user."):
			return onUser(ctx, uc, e, at)
		case strings.HasPrefix(e.Type, "channel."):
			return onChannel(ctx, uc, e, at)
		}

		return onFollow(ctx, uc, e, at)
	}
}

func onUser(ctx context.Context, uc usecase.Replica, e eventbus.Event, at time.Time) error {
	switch e.Type {
	case events.TypeUserBlocked, events.TypeUserUnblocked:
		var p events.UserBlocked
		if err := decode(e, &p, &p.BlockerID, &p.BlockedID); err != nil {
			return err
		}

		return uc.SetBlock(ctx, p.BlockerID, p.BlockedID, e.Type == events.TypeUserBlocked)
	}

	u, err := userPatch(e)
	if err != nil {
		return err
	}

	return uc.PatchUser(ctx, at, u)
}

func userPatch(e eventbus.Event) (entity.UserCopy, error) {
	switch e.Type {
	case events.TypeUserRegistered:
		var p events.UserRegistered
		if err := decode(e, &p, &p.UserID); err != nil {
			return entity.UserCopy{}, err
		}

		if p.Username == "" || !strings.Contains(p.Email, "@") {
			return entity.UserCopy{}, skip(e, errBadPayload)
		}

		return entity.UserCopy{
			ID: p.UserID, Username: &p.Username, DisplayName: &p.DisplayName, Email: &p.Email, Locale: &p.Locale,
			Timezone: &p.Timezone,
		}, nil
	case events.TypeUserProfileUpdated:
		var p events.UserProfileUpdated

		err := decode(e, &p, &p.UserID)

		return entity.UserCopy{ID: p.UserID, DisplayName: &p.DisplayName, Locale: &p.Locale, Timezone: &p.Timezone}, err
	case events.TypeUserEmailVerified:
		var p events.UserEmailVerified

		err := decode(e, &p, &p.UserID)

		return entity.UserCopy{ID: p.UserID, EmailVerified: ptr(true)}, err
	case events.TypeUserLocked:
		var p events.UserLocked

		err := decode(e, &p, &p.UserID)

		return entity.UserCopy{ID: p.UserID, Status: ptr(entity.UserLocked), LockedUntil: p.LockedUntil}, err
	}

	var p events.UserUnlocked

	err := decode(e, &p, &p.UserID)

	return entity.UserCopy{ID: p.UserID, Status: ptr(entity.UserActive)}, err
}

func ptr[T any](v T) *T { return &v }

func onChannel(ctx context.Context, uc usecase.Replica, e eventbus.Event, at time.Time) error {
	switch e.Type {
	case events.TypeChannelCreated, events.TypeChannelUpdated:
		var c events.ChannelUpdated
		if err := decode(e, &c, &c.ChannelID, &c.OwnerID); err != nil {
			return err
		}

		return uc.PatchChannel(ctx, at, entity.ChannelPatch{
			ID: c.ChannelID, OwnerID: &c.OwnerID, Handle: &c.Handle, DisplayName: &c.DisplayName, AvatarURL: c.AvatarURL,
			SetAvatar: e.Type == events.TypeChannelUpdated,
		})
	}

	var c events.ChannelSuspended
	if err := decode(e, &c, &c.ChannelID); err != nil {
		return err
	}

	status := entity.ChannelActive
	if e.Type == events.TypeChannelSuspended {
		status = entity.ChannelSuspended
	}

	return uc.PatchChannel(ctx, at, entity.ChannelPatch{ID: c.ChannelID, Status: &status})
}

func onFollow(ctx context.Context, uc usecase.Replica, e eventbus.Event, at time.Time) error {
	switch e.Type {
	case events.TypeFollowCreated:
		var p events.FollowCreated
		if err := decode(e, &p, &p.UserID, &p.ChannelID); err != nil {
			return err
		}

		return uc.SetFollow(ctx, at, entity.Follow{
			UserID: p.UserID, ChannelID: p.ChannelID, NotifyLevel: level(p.NotifyLevel), CreatedAt: p.CreatedAt,
		})
	case events.TypeFollowUpdated:
		var p events.FollowUpdated
		if err := decode(e, &p, &p.UserID, &p.ChannelID); err != nil {
			return err
		}

		return uc.SetFollow(ctx, at, entity.Follow{UserID: p.UserID, ChannelID: p.ChannelID, NotifyLevel: level(p.NotifyLevel)})
	}

	var p events.FollowDeleted
	if err := decode(e, &p, &p.UserID, &p.ChannelID); err != nil {
		return err
	}

	return uc.RemoveFollow(ctx, at, p.UserID, p.ChannelID)
}

// level maps an empty notifyLevel (events published before it existed) to ALL; unknown values to OFF.
func level(l string) string {
	switch l {
	case "":
		return events.NotifyAll
	case events.NotifyAll, events.NotifyLiveOnly, events.NotifyOff:
		return l
	}

	return events.NotifyOff
}

// SourcesHandler turns source events into notifications. A missing channel copy skips the event (E-E11).
func SourcesHandler(uc usecase.Sources, obs usecase.Observer) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if uuid.Validate(e.ID) != nil {
			return skip(e, fmt.Errorf("%w: event id", errBadPayload))
		}

		if !e.OccurredAt.IsZero() {
			obs.EventLag(e.Type, time.Since(e.OccurredAt))
		}

		err := source(ctx, uc, e)
		if errors.Is(err, entity.ErrNotFound) {
			return skip(e, err)
		}

		return err
	}
}

// route decodes one event type and calls its use case; ids lists the payload fields that must be UUIDs.
type route func(ctx context.Context, uc usecase.Sources, e eventbus.Event) error

func on[T any](ids func(p *T) []*string, call func(uc usecase.Sources, ctx context.Context, eventID string, p T) error) route {
	return func(ctx context.Context, uc usecase.Sources, e eventbus.Event) error {
		var p T
		if err := decode(e, &p, ids(&p)...); err != nil {
			return err
		}

		return call(uc, ctx, e.ID, p)
	}
}

func source(ctx context.Context, uc usecase.Sources, e eventbus.Event) error {
	r, ok := sourceRoutes()[e.Type]
	if !ok {
		return skip(e, fmt.Errorf("%w: type", errBadPayload))
	}

	return r(ctx, uc, e)
}

func sourceRoutes() map[string]route {
	return map[string]route{
		events.TypeStreamStarted: on(func(p *events.StreamStarted) []*string { return []*string{&p.StreamID, &p.ChannelID} },
			usecase.Sources.CreatorLive),
		events.TypeScheduleReminderDue: on(func(p *events.ScheduleReminderDue) []*string {
			return []*string{&p.ScheduleID, &p.ChannelID}
		}, usecase.Sources.StreamStarting),
		events.TypeScheduleCreated: on(func(p *events.ScheduleCreated) []*string {
			return []*string{&p.ScheduleID, &p.ChannelID}
		}, usecase.Sources.ScheduleCreated),
		events.TypeScheduleOccurrenceChanged: on(func(p *events.ScheduleOccurrenceChanged) []*string {
			return []*string{&p.ScheduleID, &p.ChannelID}
		}, usecase.Sources.ScheduleChanged),
		events.TypeChatMention: on(func(p *events.ChatMention) []*string {
			return []*string{&p.StreamID, &p.ChannelID, &p.SenderID}
		}, usecase.Sources.Mention),
		events.TypeFollowCreated: on(func(p *events.FollowCreated) []*string { return []*string{&p.UserID, &p.ChannelID} },
			usecase.Sources.NewFollower),
		events.TypeCreatorApproved: on(func(p *events.CreatorApproved) []*string {
			return []*string{&p.ApplicationID, &p.UserID}
		}, func(uc usecase.Sources, ctx context.Context, id string, p events.CreatorApproved) error {
			return uc.CreatorResult(ctx, id, true, p.ApplicationID, p.UserID, nil)
		}),
		events.TypeCreatorRejected: on(func(p *events.CreatorRejected) []*string {
			return []*string{&p.ApplicationID, &p.UserID}
		}, func(uc usecase.Sources, ctx context.Context, id string, p events.CreatorRejected) error {
			return uc.CreatorResult(ctx, id, false, p.ApplicationID, p.UserID, &p.Reason)
		}),
		events.TypeReportResolved: on(func(p *events.ReportResolved) []*string { return []*string{&p.ReportID, &p.ReporterID} },
			usecase.Sources.ReportResult),
		events.TypeUserLocked: on(func(p *events.UserLocked) []*string { return []*string{&p.UserID} },
			usecase.Sources.AccountLocked),
		events.TypeChannelSuspended: on(channelIDs, func(uc usecase.Sources, ctx context.Context, id string, p events.ChannelSuspended) error {
			return uc.ChannelStatus(ctx, id, true, p)
		}),
		events.TypeChannelRestored: on(channelIDs, func(uc usecase.Sources, ctx context.Context, id string, p events.ChannelSuspended) error {
			return uc.ChannelStatus(ctx, id, false, p)
		}),
		events.TypeStreamTakedown: on(func(p *events.StreamTakedown) []*string { return []*string{&p.StreamID, &p.ChannelID} },
			usecase.Sources.StreamTakedown),
	}
}

func channelIDs(p *events.ChannelSuspended) []*string { return []*string{&p.ChannelID, &p.OwnerID} }

// AuthMailHandler sends the account e-mail; an SMTP failure returns the error so JetStream redelivers it.
func AuthMailHandler(uc usecase.AuthMail) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		var p events.EmailRequested
		if err := decode(e, &p); err != nil {
			return err
		}

		if !strings.Contains(p.Email, "@") || strings.ContainsAny(p.Email, "\r\n") {
			return skip(e, errBadPayload)
		}

		if p.Token == "" && p.Purpose != events.EmailPurposeSecurityLock {
			return skip(e, errBadPayload)
		}

		err := uc.Deliver(ctx, p)
		if errors.Is(err, entity.ErrUnknownPurpose) {
			return skip(e, err)
		}

		return err
	}
}
