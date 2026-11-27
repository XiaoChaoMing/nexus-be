// Package events consumes the user / channel / stream / VOD events safety-svc keeps copies of and the
// moderation and audit events it writes to the audit log.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// Durable is the JetStream consumer name of safety-svc (DeliverAll).
const Durable = "safety-svc"

// Types consumed by safety-svc (TSD §3.2).
var Types = []string{ //nolint:gochecknoglobals // static list
	events.TypeUserRegistered, events.TypeUserProfileUpdated, events.TypeUserLocked, events.TypeUserUnlocked,
	events.TypeChannelCreated, events.TypeChannelUpdated, events.TypeChannelSuspended, events.TypeChannelRestored,
	events.TypeStreamScheduled, events.TypeStreamStarted, events.TypeStreamStatusChanged, events.TypeStreamEnded,
	events.TypeStreamVODReady, events.TypeVODPublished, events.TypeVODUpdated, events.TypeVODDeleted,
	events.TypeChatModAction, events.TypeChatSettingsChanged, events.TypeChannelRoleChanged, events.TypeAuditRecorded,
}

// Handler routes one event to the copies or the audit log.
func Handler(replica usecase.Replica, audit usecase.Audit) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if _, err := uuid.Parse(e.ID); err != nil {
			return fmt.Errorf("%w: %s: no event id", eventbus.ErrSkip, e.Type)
		}

		at := e.OccurredAt
		if at.IsZero() {
			at = time.Now()
		}

		switch {
		case strings.HasPrefix(e.Type, "user."):
			return onUser(ctx, replica, e, at)
		case strings.HasPrefix(e.Type, "channel.") && e.Type != events.TypeChannelRoleChanged:
			return onChannel(ctx, replica, e, at)
		case e.Type == events.TypeStreamVODReady || strings.HasPrefix(e.Type, "vod."):
			return onVOD(ctx, replica, e, at)
		case strings.HasPrefix(e.Type, "stream."):
			return onStream(ctx, replica, e, at)
		}

		entry, promote, err := auditEntry(e)
		if err != nil {
			return err
		}

		return audit.Record(ctx, entry, promote)
	}
}

// decode unmarshals the payload and checks that each id is a uuid; anything else is skipped.
func decode(e eventbus.Event, into any, ids ...*string) error {
	if err := json.Unmarshal(e.Payload, into); err != nil {
		return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
	}

	for _, id := range ids {
		if _, err := uuid.Parse(*id); err != nil {
			return fmt.Errorf("%w: %s: bad id %q", eventbus.ErrSkip, e.Type, *id)
		}
	}

	return nil
}

func onUser(ctx context.Context, replica usecase.Replica, e eventbus.Event, at time.Time) error {
	p, err := userPatch(e, at)
	if err != nil {
		return err
	}

	return replica.PatchUser(ctx, e.ID, at, p)
}

func userPatch(e eventbus.Event, at time.Time) (repo.UserPatch, error) {
	switch e.Type {
	case events.TypeUserRegistered:
		var p events.UserRegistered

		err := decode(e, &p, &p.UserID)

		return repo.UserPatch{ID: p.UserID, Username: &p.Username, DisplayName: &p.DisplayName, CreatedAt: &at}, err
	case events.TypeUserProfileUpdated:
		var p events.UserProfileUpdated

		err := decode(e, &p, &p.UserID)

		return repo.UserPatch{ID: p.UserID, DisplayName: &p.DisplayName}, err
	case events.TypeUserLocked:
		var p events.UserLocked

		err := decode(e, &p, &p.UserID)
		status := entity.UserLocked

		return repo.UserPatch{ID: p.UserID, Status: &status, LockedUntil: p.LockedUntil}, err
	}

	var p events.UserUnlocked

	err := decode(e, &p, &p.UserID)
	status := entity.UserActive

	return repo.UserPatch{ID: p.UserID, Status: &status}, err
}

func onChannel(ctx context.Context, replica usecase.Replica, e eventbus.Event, at time.Time) error {
	var p repo.ChannelPatch

	switch e.Type {
	case events.TypeChannelCreated, events.TypeChannelUpdated:
		var c events.ChannelUpdated
		if err := decode(e, &c, &c.ChannelID, &c.OwnerID); err != nil {
			return err
		}

		p = repo.ChannelPatch{
			ID: c.ChannelID, OwnerID: &c.OwnerID, Handle: &c.Handle, DisplayName: &c.DisplayName,
			AvatarURL: c.AvatarURL, SetAvatar: e.Type == events.TypeChannelUpdated,
		}
	default:
		var c events.ChannelSuspended
		if err := decode(e, &c, &c.ChannelID); err != nil {
			return err
		}

		status, since := entity.ChannelActive, (*time.Time)(nil)
		if e.Type == events.TypeChannelSuspended {
			status, since = entity.ChannelSuspended, &at
		}

		p = repo.ChannelPatch{ID: c.ChannelID, Status: &status, SuspendedAt: since}
	}

	return replica.PatchChannel(ctx, e.ID, at, p)
}

// _streamStatuses are the statuses the copy accepts.
var _streamStatuses = map[string]bool{ //nolint:gochecknoglobals // static set
	entity.StreamScheduled: true, entity.StreamLive: true, entity.StreamInterrupt: true, entity.StreamEnded: true,
	entity.StreamCancelled: true,
}

func onStream(ctx context.Context, replica usecase.Replica, e eventbus.Event, at time.Time) error {
	p, err := streamPatch(e, at)
	if err != nil {
		return err
	}

	if p.Status != nil && !_streamStatuses[*p.Status] {
		return fmt.Errorf("%w: %s: status %q", eventbus.ErrSkip, e.Type, *p.Status)
	}

	return replica.PatchStream(ctx, e.ID, at, p)
}

func str(s string) *string { return &s }

func streamPatch(e eventbus.Event, at time.Time) (repo.StreamPatch, error) {
	switch e.Type {
	case events.TypeStreamScheduled:
		var p events.StreamScheduled

		err := decode(e, &p, &p.StreamID, &p.ChannelID)

		return repo.StreamPatch{ID: p.StreamID, ChannelID: p.ChannelID, Status: str(entity.StreamScheduled)}, err
	case events.TypeStreamStarted:
		var p events.StreamStarted

		err := decode(e, &p, &p.StreamID, &p.ChannelID)

		return repo.StreamPatch{
			ID: p.StreamID, ChannelID: p.ChannelID, Title: &p.Title, Status: str(entity.StreamLive),
			StartedAt: &p.StartedAt,
		}, err
	case events.TypeStreamStatusChanged:
		var p events.StreamStatusChanged

		err := decode(e, &p, &p.StreamID, &p.ChannelID)

		return repo.StreamPatch{ID: p.StreamID, ChannelID: p.ChannelID, Status: &p.Status}, err
	}

	var p events.StreamEnded

	err := decode(e, &p, &p.StreamID, &p.ChannelID)

	return repo.StreamPatch{
		ID: p.StreamID, ChannelID: p.ChannelID, Status: str(entity.StreamEnded),
		EndReason: &p.EndReason, EndedAt: &at,
	}, err
}

func onVOD(ctx context.Context, replica usecase.Replica, e eventbus.Event, at time.Time) error {
	var (
		p   repo.VODPatch
		err error
	)

	switch e.Type {
	case events.TypeStreamVODReady:
		p, err = vodReady(e)
	case events.TypeVODPublished:
		p, err = vodPublished(e)
	case events.TypeVODUpdated:
		p, err = vodUpdated(e)
	case events.TypeVODDeleted:
		p, err = vodDeleted(e)
	default:
		return fmt.Errorf("%w: %s: not a VOD event", eventbus.ErrSkip, e.Type)
	}

	if err != nil {
		return err
	}

	return replica.PatchVOD(ctx, e.ID, at, p)
}

// vodReady: a broadcast recording; the stream copy learns its VOD in the same transaction.
func vodReady(e eventbus.Event) (repo.VODPatch, error) {
	var p events.StreamVODReady
	if err := decode(e, &p, &p.StreamID, &p.ChannelID, &p.VODID); err != nil {
		return repo.VODPatch{}, err
	}

	return repo.VODPatch{
		ID: p.VODID, ChannelID: p.ChannelID, StreamID: &p.StreamID, Source: str(entity.VODSourceLive), Title: &p.Title,
		Stream: &repo.StreamPatch{ID: p.StreamID, ChannelID: p.ChannelID, Title: &p.Title, VODID: &p.VODID},
	}, nil
}

func vodPublished(e eventbus.Event) (repo.VODPatch, error) {
	var p events.VODPublished
	if err := decode(e, &p, &p.VODID, &p.ChannelID); err != nil {
		return repo.VODPatch{}, err
	}

	source, err := vodSource(e, p.Source, entity.VODSourceUpload)
	if err != nil {
		return repo.VODPatch{}, err
	}

	return repo.VODPatch{ID: p.VODID, ChannelID: p.ChannelID, Source: source, Title: &p.Title}, nil
}

func vodUpdated(e eventbus.Event) (repo.VODPatch, error) {
	var p events.VODUpdated
	if err := decode(e, &p, &p.VODID, &p.ChannelID); err != nil {
		return repo.VODPatch{}, err
	}

	streamID, source, err := vodOrigin(e, p.StreamID, p.Source)
	if err != nil {
		return repo.VODPatch{}, err
	}

	return repo.VODPatch{ID: p.VODID, ChannelID: p.ChannelID, StreamID: streamID, Source: source, Title: &p.Title}, nil
}

// vodDeleted marks the VOD copy deleted and, for a broadcast recording, the stream copy's VOD too.
func vodDeleted(e eventbus.Event) (repo.VODPatch, error) {
	var p events.VODDeleted
	if err := decode(e, &p, &p.VODID, &p.ChannelID); err != nil {
		return repo.VODPatch{}, err
	}

	streamID, source, err := vodOrigin(e, p.StreamID, p.Source)
	if err != nil {
		return repo.VODPatch{}, err
	}

	out := repo.VODPatch{ID: p.VODID, ChannelID: p.ChannelID, StreamID: streamID, Source: source, Deleted: true}
	if streamID != nil {
		out.Stream = &repo.StreamPatch{ID: *streamID, ChannelID: p.ChannelID, VODID: &p.VODID, VODDeleted: true}
	}

	return out, nil
}

// vodOrigin checks the optional stream id (empty for an uploaded video) and resolves the source; events sent
// before source existed are recordings, which always have a stream id.
func vodOrigin(e eventbus.Event, streamID, source string) (stream, src *string, err error) {
	if streamID == "" {
		src, err = vodSource(e, source, entity.VODSourceUpload)

		return nil, src, err
	}

	if _, err := uuid.Parse(streamID); err != nil {
		return nil, nil, fmt.Errorf("%w: %s: bad id %q", eventbus.ErrSkip, e.Type, streamID)
	}

	src, err = vodSource(e, source, entity.VODSourceLive)

	return &streamID, src, err
}

// vodSource returns source, or fallback when it is empty; an unknown source is skipped.
func vodSource(e eventbus.Event, source, fallback string) (*string, error) {
	if source == "" {
		source = fallback
	}

	if source == entity.VODSourceLive || source == entity.VODSourceUpload {
		return &source, nil
	}

	return nil, fmt.Errorf("%w: %s: source %q", eventbus.ErrSkip, e.Type, source)
}
