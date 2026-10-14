// Package channels implements channel creation, live state, the public channel page and follows.
package channels

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
)

// _scheduleTabMax bounds the occurrences listed on a channel's Schedule tab (31 days of a daily series and more).
const _scheduleTabMax = 200

// Repos groups the storage the use case needs.
type Repos struct {
	Channels  repo.ChannelRepo
	Follows   repo.FollowRepo
	Schedules repo.ScheduleRepo
	Reminders repo.ReminderRepo
	Search    repo.SearchRepo
}

// UseCase implements usecase.Directory, usecase.Follows and usecase.Sync.
type UseCase struct {
	r      Repos
	enrich *enrich.Enricher
	l      logger.Interface
}

var (
	_ usecase.Directory = (*UseCase)(nil)
	_ usecase.Follows   = (*UseCase)(nil)
	_ usecase.Sync      = (*UseCase)(nil)
)

// New -.
func New(r Repos, e *enrich.Enricher, l logger.Interface) *UseCase {
	return &UseCase{r: r, enrich: e, l: l}
}

// activeChannel returns the channel, entity.ErrNotFound or entity.ErrSuspended.
func (uc *UseCase) activeChannel(ctx context.Context, channelID string) (entity.Channel, error) {
	ch, err := uc.r.Channels.ByID(ctx, channelID)
	if err != nil {
		return entity.Channel{}, fmt.Errorf("ChannelUseCase - channel: %w", err)
	}

	if ch.Suspended() {
		return entity.Channel{}, entity.ErrSuspended
	}

	return ch, nil
}

// Profile -.
func (uc *UseCase) Profile(ctx context.Context, handle, viewerID string) (entity.Profile, error) {
	if !entity.ValidHandle(handle) {
		return entity.Profile{}, entity.ErrNotFound
	}

	ch, err := uc.r.Channels.ByHandle(ctx, handle)
	if err != nil {
		return entity.Profile{}, fmt.Errorf("ChannelUseCase - Profile: %w", err)
	}

	if ch.Suspended() {
		return entity.Profile{}, entity.ErrSuspended
	}

	p := entity.Profile{Channel: ch}

	if ch.IsLive {
		live, lerr := uc.r.Channels.LiveStreams(ctx, []string{ch.ID})
		if lerr != nil {
			return entity.Profile{}, fmt.Errorf("ChannelUseCase - Profile - live: %w", lerr)
		}

		if s, ok := live[ch.ID]; ok {
			uc.enrich.Live(ctx, []*entity.LiveStream{&s})
			p.CurrentStream = &s
		}
	}

	if viewerID == "" {
		return p, nil
	}

	f, err := uc.r.Follows.GetFollow(ctx, viewerID, ch.ID)
	if err != nil {
		return entity.Profile{}, fmt.Errorf("ChannelUseCase - Profile - follow: %w", err)
	}

	p.Viewer = &entity.ProfileViewer{Following: f != nil, IsOwner: ch.OwnerID == viewerID}
	if f != nil {
		p.Viewer.NotifyLevel = &f.NotifyLevel
	}

	return p, nil
}

// ChannelSchedule -.
func (uc *UseCase) ChannelSchedule(ctx context.Context, channelID, viewerID string, from, to time.Time) ([]entity.OccurrenceView, error) {
	if _, err := uc.activeChannel(ctx, channelID); err != nil {
		return nil, err
	}

	list, err := uc.r.Schedules.ListByChannel(ctx, channelID, entity.ScheduleActive)
	if err != nil {
		return nil, fmt.Errorf("ChannelUseCase - ChannelSchedule: %w", err)
	}

	ids := make([]string, 0, len(list))
	catIDs := []string{}

	for _, s := range list {
		ids = append(ids, s.ID)

		if s.CategoryID != nil {
			catIDs = append(catIDs, *s.CategoryID)
		}
	}

	ctxs, err := uc.occurrenceContext(ctx, ids, catIDs, viewerID)
	if err != nil {
		return nil, err
	}

	out := []entity.OccurrenceView{}

	for _, s := range list {
		for _, at := range s.Occurrences(from, to, _scheduleTabMax) {
			out = append(out, ctxs.view(s, at, viewerID != ""))
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].OccurrenceAt.Before(out[j].OccurrenceAt) })

	if len(out) > _scheduleTabMax {
		out = out[:_scheduleTabMax]
	}

	return out, nil
}

type occurrenceContext struct {
	streams  map[entity.OccurrenceKey]string
	reminded map[entity.OccurrenceKey]bool
	cats     map[string]entity.Category
}

func (uc *UseCase) occurrenceContext(ctx context.Context, ids, catIDs []string, viewerID string) (occurrenceContext, error) {
	var (
		c   occurrenceContext
		err error
	)

	if c.streams, err = uc.r.Schedules.OccurrenceStreams(ctx, ids); err != nil {
		return c, fmt.Errorf("ChannelUseCase - ChannelSchedule - streams: %w", err)
	}

	c.cats = uc.enrich.Categories(ctx, catIDs)
	c.reminded = map[entity.OccurrenceKey]bool{}

	if viewerID != "" {
		if c.reminded, err = uc.r.Reminders.Reminded(ctx, viewerID, ids); err != nil {
			return c, fmt.Errorf("ChannelUseCase - ChannelSchedule - reminders: %w", err)
		}
	}

	return c, nil
}

func (c occurrenceContext) view(s entity.Schedule, at time.Time, signedIn bool) entity.OccurrenceView {
	v := entity.OccurrenceView{ScheduleID: s.ID, OccurrenceAt: at, Title: s.Title, DurationMin: s.DurationMin}
	key := entity.KeyOf(s.ID, at)

	if id, ok := c.streams[key]; ok {
		v.StreamID = &id
	}

	if s.CategoryID != nil {
		if cat, ok := c.cats[*s.CategoryID]; ok {
			v.Category = &cat
		}
	}

	if signedIn {
		r := c.reminded[key]
		v.Reminded = &r
	}

	return v
}

// ---- Sync -----------------------------------------------------------------------------------------

// CreatorApproved -.
func (uc *UseCase) CreatorApproved(ctx context.Context, eventID string, in usecase.CreatorApproved) error {
	base := entity.BaseHandle(in.Username)

	ch, created, err := uc.r.Channels.CreateForOwner(ctx, eventID, repo.NewChannel{
		OwnerID:     in.UserID,
		BaseHandle:  base,
		DisplayName: entity.DisplayName(in.DisplayName, in.Username),
	})
	if err != nil {
		return fmt.Errorf("ChannelUseCase - CreatorApproved: %w", err)
	}

	if !created {
		uc.l.Info("channel - creator %s already has a channel or the event was processed: skipped", in.UserID)

		return nil
	}

	if ch.Handle != base {
		uc.l.Info("channel - handle %q taken, channel %s created as %q", base, ch.ID, ch.Handle)
	}

	uc.l.Info("channel - created %s (%s) for creator %s", ch.ID, ch.Handle, in.UserID)

	return nil
}

// StreamStarted -.
func (uc *UseCase) StreamStarted(ctx context.Context, eventID string, s entity.LiveStream) error {
	if err := uc.r.Channels.SetLive(ctx, eventID, s); err != nil {
		return fmt.Errorf("ChannelUseCase - StreamStarted: %w", err)
	}

	return nil
}

// StreamUpdated -.
func (uc *UseCase) StreamUpdated(ctx context.Context, eventID string, s entity.LiveStream) error {
	if err := uc.r.Channels.UpdateLive(ctx, eventID, s); err != nil {
		return fmt.Errorf("ChannelUseCase - StreamUpdated: %w", err)
	}

	return nil
}

// StreamEnded -.
func (uc *UseCase) StreamEnded(ctx context.Context, eventID, channelID, streamID string) error {
	if err := uc.r.Channels.SetOffline(ctx, eventID, channelID, streamID); err != nil {
		return fmt.Errorf("ChannelUseCase - StreamEnded: %w", err)
	}

	return nil
}

// StreamScheduled -.
func (uc *UseCase) StreamScheduled(ctx context.Context, eventID string, e events.StreamScheduled) error {
	if err := uc.r.Schedules.UpsertOccurrenceStream(ctx, eventID, e); err != nil {
		return fmt.Errorf("ChannelUseCase - StreamScheduled: %w", err)
	}

	return nil
}

// VODChanged -.
func (uc *UseCase) VODChanged(ctx context.Context, eventID string, v entity.VODDoc) error {
	if err := uc.r.Search.UpsertVOD(ctx, eventID, v); err != nil {
		return fmt.Errorf("ChannelUseCase - VODChanged: %w", err)
	}

	return nil
}

// VODDeleted -.
func (uc *UseCase) VODDeleted(ctx context.Context, eventID, vodID string) error {
	if err := uc.r.Search.DeleteVOD(ctx, eventID, vodID); err != nil {
		return fmt.Errorf("ChannelUseCase - VODDeleted: %w", err)
	}

	return nil
}
