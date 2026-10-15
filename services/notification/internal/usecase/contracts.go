// Package usecase declares the notification-svc business contracts the controllers call.
package usecase

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

type (
	// Page is one page of a user's notifications.
	Page struct {
		Items      []entity.Notification
		NextCursor *string
	}

	// SettingView is one row of the settings screen.
	SettingView struct {
		Setting entity.Setting
		// Default carries the locks and whether the type has e-mail at all.
		Default entity.Default
	}

	// SettingsView is GET /v1/me/notification-settings.
	SettingsView struct {
		Items         []SettingView
		EmailVerified bool
	}

	// SettingPatch changes one type; nil keeps the current value.
	SettingPatch struct {
		Type  string
		InApp *bool
		Email *bool
	}

	// Center is the notification center of the signed-in user.
	Center interface {
		List(ctx context.Context, userID string, unreadOnly bool, cursor string, limit int) (Page, error)
		UnreadCount(ctx context.Context, userID string) (int, error)
		Read(ctx context.Context, userID, id string) error
		ReadAll(ctx context.Context, userID string, before time.Time) error
		Delete(ctx context.Context, userID, id string) error
	}

	// Settings are the per-type in-app / e-mail choices.
	Settings interface {
		Get(ctx context.Context, userID string) (SettingsView, error)
		Update(ctx context.Context, userID string, items []SettingPatch) (SettingsView, error)
		Unsubscribe(ctx context.Context, token string) (entity.Type, error)
	}

	// Sources turns the events of the durable notification-svc into notifications; eventID is the
	// idempotency key (source_event_id / processed_events).
	Sources interface {
		CreatorLive(ctx context.Context, eventID string, e events.StreamStarted) error
		StreamStarting(ctx context.Context, eventID string, e events.ScheduleReminderDue) error
		ScheduleCreated(ctx context.Context, eventID string, e events.ScheduleCreated) error
		ScheduleChanged(ctx context.Context, eventID string, e events.ScheduleOccurrenceChanged) error
		Mention(ctx context.Context, eventID string, e events.ChatMention) error
		NewFollower(ctx context.Context, eventID string, e events.FollowCreated) error
		CreatorResult(ctx context.Context, eventID string, approved bool, applicationID, userID string, reason *string) error
		ReportResult(ctx context.Context, eventID string, e events.ReportResolved) error
		AccountLocked(ctx context.Context, eventID string, e events.UserLocked) error
		ChannelStatus(ctx context.Context, eventID string, suspended bool, e events.ChannelSuspended) error
		StreamTakedown(ctx context.Context, eventID string, e events.StreamTakedown) error
	}

	// Replica keeps the copies of users, channels, follows and blocks (durable notification-replica).
	Replica interface {
		PatchUser(ctx context.Context, at time.Time, u entity.UserCopy) error
		PatchChannel(ctx context.Context, at time.Time, c entity.ChannelPatch) error
		SetFollow(ctx context.Context, at time.Time, f entity.Follow) error
		RemoveFollow(ctx context.Context, at time.Time, userID, channelID string) error
		SetBlock(ctx context.Context, blockerID, blockedID string, blocked bool) error
		Backfill(ctx context.Context) error
	}

	// AuthMail sends the account e-mails of auth.email_requested.v1 inside the handler; the token is never
	// stored or logged.
	AuthMail interface {
		Deliver(ctx context.Context, e events.EmailRequested) error
	}

	// Jobs are the periodic workers.
	Jobs interface {
		SendDueEmails(ctx context.Context) (int, error)
		Purge(ctx context.Context) error
	}

	// Observer receives counters for /metrics; use cases never depend on a metrics library.
	Observer interface {
		Created(t entity.Type, grouped bool)
		Suppressed(t entity.Type, reason string)
		EmailSent(template, status string, took time.Duration)
		Read(action string)
		EventLag(source string, lag time.Duration)
	}
)

// Reasons of Observer.Suppressed.
const (
	SuppressSettings = "settings"
	SuppressCooldown = "cooldown"
	SuppressBlocked  = "blocked"
	SuppressDailyCap = "daily_cap"
	SuppressNoCopy   = "no_copy"
	SuppressInactive = "inactive"
	SuppressNoEmail  = "unverified"
)

// E-mail statuses of Observer.EmailSent.
const (
	EmailSent   = "sent"
	EmailRetry  = "retry"
	EmailFailed = "failed"
	EmailOff    = "disabled"
)
