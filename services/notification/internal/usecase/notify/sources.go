package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// Kinds of SYSTEM notifications (payload.kind).
const (
	KindChannelSuspended = "CHANNEL_SUSPENDED"
	KindChannelRestored  = "CHANNEL_RESTORED"
	KindStreamTakedown   = "STREAM_TAKEDOWN"
)

// Results of CREATOR_APPLICATION.
const (
	ResultApproved = "APPROVED"
	ResultRejected = "REJECTED"
)

// ReasonCreated marks a SCHEDULE notification about a new schedule (BR-E06, in-app only).
const ReasonCreated = "CREATED"

const (
	_linkStudio       = "/studio/stream"
	_linkApply        = "/creator/apply"
	_linkDashboard    = "/studio"
	_mentionNotifyMax = 5
)

func liveLink(streamID string) *string { return ptr("/live/" + streamID) }

func scheduleLink(handle string) *string { return ptr("/" + handle + "?tab=schedule") }

// channel reads the channel copy; a missing copy is entity.ErrNotFound (the event is skipped).
func (uc *UseCase) channel(ctx context.Context, id string) (entity.ChannelCopy, error) {
	c, err := uc.r.Recipients.Channel(ctx, id)
	if err != nil {
		return c, fmt.Errorf("notify - channel %s: %w", id, err)
	}

	return c, nil
}

// fanOutFollowers pages the active followers of ch with one of levels, batch by batch.
func (uc *UseCase) fanOutFollowers(ctx context.Context, eventID string, ch entity.ChannelCopy, levels []string, s spec) error {
	after := ""

	for {
		batch, err := uc.r.Recipients.Followers(ctx, ch.ID, levels, ch.OwnerID, after, _fanoutBatch)
		if err != nil {
			return fmt.Errorf("notify - followers: %w", err)
		}

		if err := uc.deliverBatch(ctx, eventID, s, batch); err != nil {
			return err
		}

		if len(batch) < _fanoutBatch {
			return nil
		}

		after = batch[len(batch)-1].UserID
	}
}

// recipients reads the copies of ids in order; missing copies are dropped (E-E11), inactive ones too when
// activeOnly.
func (uc *UseCase) recipients(ctx context.Context, t entity.Type, ids []string, activeOnly bool) ([]entity.Recipient, error) {
	found, err := uc.r.Recipients.Recipients(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("notify - recipients: %w", err)
	}

	out := make([]entity.Recipient, 0, len(ids))
	seen := make(map[string]bool, len(ids))

	for _, id := range ids {
		if seen[id] {
			continue
		}

		seen[id] = true

		r, ok := found[id]

		switch {
		case !ok:
			uc.l.Warn("notify - %s: no copy of recipient %s", t, id)
			uc.obs.Suppressed(t, usecase.SuppressNoCopy)
		case activeOnly && !r.Active:
			uc.obs.Suppressed(t, usecase.SuppressInactive)
		default:
			out = append(out, r)
		}
	}

	return out, nil
}

// CreatorLive fans CREATOR_LIVE out to the followers with the bell on ALL or LIVE_ONLY (BR-E04, BR-E11).
func (uc *UseCase) CreatorLive(ctx context.Context, eventID string, e events.StreamStarted) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Channel  channelRef `json:"channel"`
		StreamID string     `json:"streamId"`
		Title    string     `json:"title"`
	}{refOf(ch), e.StreamID, e.Title})
	if err != nil {
		return err
	}

	link := liveLink(e.StreamID)
	s := spec{
		n:        entity.Notification{Type: entity.TypeCreatorLive, Payload: payload, Link: link},
		template: "live_started",
		data: func(entity.Recipient) map[string]string {
			return map[string]string{keyChannel: ch.DisplayName, keyTitle: e.Title, keyLink: uc.absolute(link)}
		},
		liveChannel: ch.ID,
	}

	return uc.fanOutFollowers(ctx, eventID, ch, []string{events.NotifyAll, events.NotifyLiveOnly}, s)
}

// StreamStarting reminds the viewers who asked for it (schedule.reminder_due.v1, ≤ 500 per event).
func (uc *UseCase) StreamStarting(ctx context.Context, eventID string, e events.ScheduleReminderDue) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Channel      channelRef `json:"channel"`
		ScheduleID   string     `json:"scheduleId"`
		OccurrenceAt time.Time  `json:"occurrenceAt"`
		Title        string     `json:"title"`
	}{refOf(ch), e.ScheduleID, e.OccurrenceAt.UTC(), e.Title})
	if err != nil {
		return err
	}

	link := scheduleLink(ch.Handle)
	s := spec{
		n:        entity.Notification{Type: entity.TypeStreamStarting, Payload: payload, Link: link},
		template: "stream_starting",
		data: func(r entity.Recipient) map[string]string {
			return map[string]string{
				keyChannel: ch.DisplayName, keyTitle: e.Title, "When": uc.formatTime(e.OccurrenceAt, r), keyLink: uc.absolute(link),
			}
		},
	}

	return uc.deliverUsers(ctx, eventID, s, e.UserIDs)
}

// deliverUsers delivers a batch spec to an explicit list of users, 500 at a time.
func (uc *UseCase) deliverUsers(ctx context.Context, eventID string, s spec, ids []string) error {
	for chunk := range slices.Chunk(ids, _fanoutBatch) {
		rs, err := uc.recipients(ctx, s.n.Type, chunk, true)
		if err != nil {
			return err
		}

		if err := uc.deliverBatch(ctx, eventID, s, rs); err != nil {
			return err
		}
	}

	return nil
}

type schedulePayload struct {
	Channel         channelRef `json:"channel"`
	ScheduleID      string     `json:"scheduleId"`
	Title           string     `json:"title"`
	OccurrenceAt    time.Time  `json:"occurrenceAt"`
	NewOccurrenceAt *time.Time `json:"newOccurrenceAt"`
	Reason          string     `json:"reason"`
}

// ScheduleCreated tells the followers with the bell on ALL about a new schedule (BR-E06, in-app only).
func (uc *UseCase) ScheduleCreated(ctx context.Context, eventID string, e events.ScheduleCreated) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(schedulePayload{
		Channel: refOf(ch), ScheduleID: e.ScheduleID, Title: e.Title, OccurrenceAt: e.StartAt.UTC(), Reason: ReasonCreated,
	})
	if err != nil {
		return err
	}

	s := spec{n: entity.Notification{Type: entity.TypeSchedule, Payload: payload, Link: scheduleLink(ch.Handle)}}

	return uc.fanOutFollowers(ctx, eventID, ch, []string{events.NotifyAll}, s)
}

// ScheduleChanged tells the viewers with a reminder that an occurrence was cancelled, moved or changed.
func (uc *UseCase) ScheduleChanged(ctx context.Context, eventID string, e events.ScheduleOccurrenceChanged) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	var moved *time.Time
	if e.NewOccurrenceAt != nil {
		moved = ptr(e.NewOccurrenceAt.UTC())
	}

	payload, err := marshal(schedulePayload{
		Channel: refOf(ch), ScheduleID: e.ScheduleID, Title: e.Title, OccurrenceAt: e.OccurrenceAt.UTC(),
		NewOccurrenceAt: moved, Reason: e.Reason,
	})
	if err != nil {
		return err
	}

	link := scheduleLink(ch.Handle)
	s := spec{
		n:        entity.Notification{Type: entity.TypeSchedule, Payload: payload, Link: link},
		template: "schedule_changed",
		data: func(r entity.Recipient) map[string]string {
			newWhen := ""
			if e.NewOccurrenceAt != nil {
				newWhen = uc.formatTime(*e.NewOccurrenceAt, r)
			}

			return map[string]string{
				keyChannel: ch.DisplayName, keyTitle: e.Title, "When": uc.formatTime(e.OccurrenceAt, r), "NewWhen": newWhen,
				keyReason: e.Reason, keyLink: uc.absolute(link),
			}
		},
	}

	return uc.deliverUsers(ctx, eventID, s, e.UserIDs)
}

type personRef struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

// Mention notifies the mentioned users, minus those who blocked the sender, grouped per stream for 5 min.
func (uc *UseCase) Mention(ctx context.Context, eventID string, e events.ChatMention) error {
	ids, err := uc.mentioned(ctx, e)
	if err != nil {
		return err
	}

	rs, err := uc.recipients(ctx, entity.TypeMention, ids, false)
	if err != nil || len(rs) == 0 {
		return err
	}

	ref, sender, err := uc.mentionRefs(ctx, e)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Channel    channelRef `json:"channel"`
		StreamID   string     `json:"streamId"`
		LastSender personRef  `json:"lastSender"`
		Excerpt    string     `json:"excerpt"`
	}{ref, e.StreamID, sender, e.Excerpt})
	if err != nil {
		return err
	}

	name := sender.DisplayName
	if name == "" {
		name = sender.Username
	}

	link := liveLink(e.StreamID)
	s := spec{
		n: entity.Notification{
			Type: entity.TypeMention, Payload: payload, Link: link, GroupKey: ptr("MENTION:" + e.StreamID),
		},
		template: "mention",
		data: func(entity.Recipient) map[string]string {
			return map[string]string{"Sender": name, keyChannel: ref.DisplayName, "Excerpt": e.Excerpt, keyLink: uc.absolute(link)}
		},
	}

	return uc.deliverSmall(ctx, eventID, s, _mentionWindow, rs)
}

// mentioned returns up to 5 distinct mentioned users other than the sender, minus those who blocked the
// sender (BR-F18).
func (uc *UseCase) mentioned(ctx context.Context, e events.ChatMention) ([]string, error) {
	ids := make([]string, 0, len(e.MentionedUserIDs))

	for _, id := range e.MentionedUserIDs {
		if id != e.SenderID && !slices.Contains(ids, id) && len(ids) < _mentionNotifyMax {
			ids = append(ids, id)
		}
	}

	blocked, err := uc.r.Recipients.BlockedBy(ctx, e.SenderID, ids)
	if err != nil {
		return nil, fmt.Errorf("notify - mention - blocks: %w", err)
	}

	return slices.DeleteFunc(ids, func(id string) bool {
		if blocked[id] {
			uc.obs.Suppressed(entity.TypeMention, usecase.SuppressBlocked)
		}

		return blocked[id]
	}), nil
}

// mentionRefs reads the channel and the sender from the copies; missing copies leave only the ids.
func (uc *UseCase) mentionRefs(ctx context.Context, e events.ChatMention) (channelRef, personRef, error) {
	ref, sender := channelRef{ID: e.ChannelID}, personRef{ID: e.SenderID, Username: e.SenderUsername}

	ch, err := uc.r.Recipients.Channel(ctx, e.ChannelID)

	switch {
	case err == nil:
		ref = refOf(ch)
	case !errors.Is(err, entity.ErrNotFound):
		return ref, sender, fmt.Errorf("notify - mention - channel: %w", err)
	}

	found, err := uc.r.Recipients.Recipients(ctx, []string{e.SenderID})
	if err != nil {
		return ref, sender, fmt.Errorf("notify - mention - sender: %w", err)
	}

	sender.DisplayName = found[e.SenderID].DisplayName

	return ref, sender, nil
}

// NewFollower tells the channel owner, grouped per channel for 60 min (in-app only).
func (uc *UseCase) NewFollower(ctx context.Context, eventID string, e events.FollowCreated) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	if ch.OwnerID == e.UserID {
		return nil
	}

	found, err := uc.r.Recipients.Recipients(ctx, []string{ch.OwnerID, e.UserID})
	if err != nil {
		return fmt.Errorf("notify - new follower: %w", err)
	}

	owner, okOwner := found[ch.OwnerID]
	follower, okFollower := found[e.UserID]

	if !okOwner || !okFollower {
		uc.l.Warn("notify - NEW_FOLLOWER %s: no copy of the owner or the follower", eventID)
		uc.obs.Suppressed(entity.TypeNewFollower, usecase.SuppressNoCopy)

		return nil
	}

	payload, err := marshal(struct {
		ChannelID    string    `json:"channelId"`
		LastFollower personRef `json:"lastFollower"`
	}{ch.ID, personRef{ID: follower.UserID, Username: follower.Username, DisplayName: follower.DisplayName}})
	if err != nil {
		return err
	}

	s := spec{n: entity.Notification{
		Type: entity.TypeNewFollower, Payload: payload, Link: ptr(_linkDashboard), GroupKey: ptr("NEW_FOLLOWER:" + ch.ID),
	}}

	return uc.deliverSmall(ctx, eventID, s, _followerWindow, []entity.Recipient{owner})
}

// one delivers a notification to a single user (no grouping).
func (uc *UseCase) one(ctx context.Context, eventID, userID string, s spec) error {
	rs, err := uc.recipients(ctx, s.n.Type, []string{userID}, false)
	if err != nil {
		return err
	}

	return uc.deliverSmall(ctx, eventID, s, 0, rs)
}

// CreatorResult tells the applicant (locked type, mandatory e-mail).
func (uc *UseCase) CreatorResult(ctx context.Context, eventID string, approved bool, applicationID, userID string,
	reason *string,
) error {
	result, link := ResultRejected, _linkApply
	if approved {
		result, link, reason = ResultApproved, _linkStudio, nil
	}

	payload, err := marshal(struct {
		ApplicationID string  `json:"applicationId"`
		Result        string  `json:"result"`
		Reason        *string `json:"reason"`
	}{applicationID, result, reason})
	if err != nil {
		return err
	}

	text := ""
	if reason != nil {
		text = *reason
	}

	return uc.one(ctx, eventID, userID, spec{
		n:        entity.Notification{Type: entity.TypeCreatorApplication, Payload: payload, Link: &link},
		template: "creator_application_result",
		data: func(entity.Recipient) map[string]string {
			return map[string]string{"Result": result, keyReason: text, keyLink: uc.absolute(&link)}
		},
	})
}

// ReportResult tells the reporter that the report was closed.
func (uc *UseCase) ReportResult(ctx context.Context, eventID string, e events.ReportResolved) error {
	payload, err := marshal(struct {
		ReportID   string `json:"reportId"`
		TargetType string `json:"targetType"`
		Result     string `json:"result"`
	}{e.ReportID, e.TargetType, e.Status})
	if err != nil {
		return err
	}

	return uc.one(ctx, eventID, e.ReporterID, spec{
		n:        entity.Notification{Type: entity.TypeReportResult, Payload: payload},
		template: "report_result",
		data: func(entity.Recipient) map[string]string {
			return map[string]string{"Result": e.Status, keyLink: uc.absolute(nil)}
		},
	})
}

// AccountLocked queues the mandatory account_locked e-mail, even to an unverified address (BR-E09).
func (uc *UseCase) AccountLocked(ctx context.Context, eventID string, e events.UserLocked) error {
	found, err := uc.r.Recipients.Recipients(ctx, []string{e.UserID})
	if err != nil {
		return fmt.Errorf("notify - account locked: %w", err)
	}

	r, ok := found[e.UserID]
	if !ok || r.Email == "" {
		uc.l.Warn("notify - account_locked %s: no copy of the user", eventID)

		return nil
	}

	until := ""
	if e.LockedUntil != nil {
		until = uc.formatTime(*e.LockedUntil, r)
	}

	_, err = uc.r.Notifications.QueueEmail(ctx, eventID, entity.Email{
		UserID: r.UserID, To: r.Email, Template: "account_locked", Locale: locale(r.Locale), Mandatory: true,
		Data: map[string]string{"Name": r.Name(), keyReason: e.Reason, "Until": until},
	})
	if err != nil {
		return fmt.Errorf("notify - account locked: %w", err)
	}

	return nil
}

type systemPayload struct {
	Kind      string  `json:"kind"`
	ChannelID string  `json:"channelId"`
	StreamID  *string `json:"streamId,omitempty"`
	Title     *string `json:"title,omitempty"`
	Reason    string  `json:"reason"`
}

// ChannelStatus tells the owner that the channel was suspended or restored (SYSTEM, mandatory e-mail).
func (uc *UseCase) ChannelStatus(ctx context.Context, eventID string, suspended bool, e events.ChannelSuspended) error {
	kind, template := KindChannelRestored, "channel_restored"
	if suspended {
		kind, template = KindChannelSuspended, "channel_suspended"
	}

	name := ""
	if ch, err := uc.r.Recipients.Channel(ctx, e.ChannelID); err == nil {
		name = ch.DisplayName
	}

	payload, err := marshal(systemPayload{Kind: kind, ChannelID: e.ChannelID, Reason: e.Reason})
	if err != nil {
		return err
	}

	link := _linkStudio

	return uc.one(ctx, eventID, e.OwnerID, spec{
		n:        entity.Notification{Type: entity.TypeSystem, Payload: payload, Link: &link},
		template: template,
		data: func(entity.Recipient) map[string]string {
			return map[string]string{keyChannel: name, keyReason: e.Reason, keyLink: uc.absolute(&link)}
		},
	})
}

// StreamTakedown tells the owner that an admin took the stream down (SYSTEM, mandatory e-mail).
func (uc *UseCase) StreamTakedown(ctx context.Context, eventID string, e events.StreamTakedown) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(systemPayload{
		Kind: KindStreamTakedown, ChannelID: ch.ID, StreamID: &e.StreamID, Title: &e.Title, Reason: e.Reason,
	})
	if err != nil {
		return err
	}

	link := _linkStudio

	return uc.one(ctx, eventID, ch.OwnerID, spec{
		n:        entity.Notification{Type: entity.TypeSystem, Payload: payload, Link: &link},
		template: "stream_taken_down",
		data: func(entity.Recipient) map[string]string {
			return map[string]string{keyChannel: ch.DisplayName, keyTitle: e.Title, keyReason: e.Reason, keyLink: uc.absolute(&link)}
		},
	})
}
