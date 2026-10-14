// Package studio implements the creator's channel settings (profile, images, handle), the channel half of
// the Studio dashboard, and the category catalog with its placeholder covers.
package studio

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
)

const (
	_imageCacheControl = "public, max-age=31536000, immutable"
	_coverCacheControl = "public, max-age=86400"
	_coverWidth        = 600
	_coverHeight       = 800
	_jpegType          = "image/jpeg"

	// Cover hues by group (degrees).
	_hueGames    = 260
	_hueEsports  = 200
	_hueMusic    = 320
	_hueIRL      = 20
	_hueCreative = 140
)

// Imager turns an upload into the stored JPEG (imaging.Processor) and draws category covers.
type Imager interface {
	Process(data []byte, spec entity.ImageSpec) ([]byte, error)
	Cover(seed string, baseHue, w, h int) ([]byte, error)
}

// Repos groups the storage the use case needs.
type Repos struct {
	Channels   repo.ChannelRepo
	Follows    repo.FollowRepo
	Schedules  repo.ScheduleRepo
	Reminders  repo.ReminderRepo
	Categories repo.CategoryRepo
	Objects    repo.ObjectStore
}

// Config -.
type Config struct {
	HandleChangeInterval time.Duration
	AvatarMaxBytes       int
	BannerMaxBytes       int
	// MediaBase is the URL prefix of the public bucket at the edge ("/media").
	MediaBase string
	// Horizon is how far ahead schedules may be (the dashboard's next occurrence is searched within it).
	Horizon time.Duration
}

// UseCase implements usecase.Studio and usecase.Catalog.
type UseCase struct {
	r      Repos
	img    Imager
	enrich *enrich.Enricher
	cfg    Config
	l      logger.Interface
	now    func() time.Time
}

var (
	_ usecase.Studio  = (*UseCase)(nil)
	_ usecase.Catalog = (*UseCase)(nil)
)

// New -.
func New(r Repos, img Imager, e *enrich.Enricher, cfg Config, l logger.Interface) *UseCase {
	return &UseCase{r: r, img: img, enrich: e, cfg: cfg, l: l, now: time.Now}
}

// WithClock -.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

// Channel -.
func (uc *UseCase) Channel(ctx context.Context, ownerID string) (entity.Channel, error) {
	ch, err := uc.r.Channels.ByOwner(ctx, ownerID)
	if errors.Is(err, entity.ErrNotFound) {
		return entity.Channel{}, entity.ErrNotCreator
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("StudioUseCase - Channel: %w", err)
	}

	return ch, nil
}

// NextHandleChangeAt -.
func (uc *UseCase) NextHandleChangeAt(ch entity.Channel) *time.Time {
	return ch.NextHandleChangeAt(uc.cfg.HandleChangeInterval, uc.now())
}

// UpdateProfile -.
func (uc *UseCase) UpdateProfile(ctx context.Context, ownerID string, p entity.ProfilePatch) (entity.Channel, error) {
	p = p.Normalize()
	if errs := p.Validate(); len(errs) > 0 {
		return entity.Channel{}, entity.Invalid(errs)
	}

	ch, err := uc.Channel(ctx, ownerID)
	if err != nil {
		return entity.Channel{}, err
	}

	now := uc.now()

	if p.Handle != nil {
		changes, next := ch.HandleChange(*p.Handle, uc.cfg.HandleChangeInterval, now)

		switch {
		case !changes:
			p.Handle = nil
		case next != nil:
			return entity.Channel{}, entity.Reject(entity.ErrHandleTooSoon, "nextChangeAt", next.UTC().Format(time.RFC3339))
		}
	}

	if p.Empty() {
		return ch, nil
	}

	out, err := uc.r.Channels.UpdateProfile(ctx, ch.ID, p, now)
	if errors.Is(err, entity.ErrHandleTaken) {
		return entity.Channel{}, entity.Reject(entity.ErrHandleTaken, "handle", entity.DetailTaken)
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("StudioUseCase - UpdateProfile: %w", err)
	}

	if out.Handle != ch.Handle {
		uc.l.Info("audit channel_handle_changed channel=%s from=%s to=%s", ch.ID, ch.Handle, out.Handle)
	}

	return out, nil
}

// SetImage stores the new image before pointing the channel at it, then removes the previous one, so the
// channel never references a missing object.
func (uc *UseCase) SetImage(ctx context.Context, ownerID string, kind entity.ImageKind, data []byte) (string, error) {
	ch, err := uc.Channel(ctx, ownerID)
	if err != nil {
		return "", err
	}

	maxBytes := uc.cfg.AvatarMaxBytes
	if kind == entity.ImageBanner {
		maxBytes = uc.cfg.BannerMaxBytes
	}

	jpg, err := uc.img.Process(data, kind.Spec(maxBytes))
	if err != nil {
		return "", imageError(err)
	}

	key := fmt.Sprintf("channels/%s/%s-%d.jpg", ch.ID, kind, uc.now().UnixMilli())
	if err = uc.r.Objects.Put(ctx, key, jpg, _jpegType, _imageCacheControl); err != nil {
		return "", fmt.Errorf("StudioUseCase - SetImage - put: %w", err)
	}

	url := uc.url(key)

	old, err := uc.r.Channels.SetImage(ctx, ch.ID, kind, url)
	if err != nil {
		uc.removeObject(ctx, key)

		return "", fmt.Errorf("StudioUseCase - SetImage - save: %w", err)
	}

	if old != nil {
		if oldKey, ok := uc.key(*old); ok && strings.HasPrefix(oldKey, "channels/"+ch.ID+"/") {
			uc.removeObject(ctx, oldKey)
		}
	}

	return url, nil
}

// imageError adds the VALIDATION_FAILED detail of a refused image.
func imageError(err error) error {
	switch {
	case errors.Is(err, entity.ErrImageFormat):
		return entity.Reject(entity.ErrImageFormat, "file", entity.DetailFormat)
	case errors.Is(err, entity.ErrImageTooSmall):
		return entity.Reject(entity.ErrImageTooSmall, "file", entity.DetailMin)
	}

	return fmt.Errorf("StudioUseCase - SetImage: %w", err)
}

func (uc *UseCase) url(key string) string {
	return strings.TrimSuffix(uc.cfg.MediaBase, "/") + "/" + key
}

// key is the object key of a URL built by url.
func (uc *UseCase) key(url string) (string, bool) {
	return strings.CutPrefix(url, strings.TrimSuffix(uc.cfg.MediaBase, "/")+"/")
}

func (uc *UseCase) removeObject(ctx context.Context, key string) {
	if err := uc.r.Objects.Remove(ctx, key); err != nil {
		uc.l.Warn("studio - remove image %s: %v", key, err)
	}
}

// Stats -.
func (uc *UseCase) Stats(ctx context.Context, ownerID string, r entity.StatsRange) (entity.ChannelStats, error) {
	d, ok := r.Duration()
	if !ok {
		return entity.ChannelStats{}, entity.Reject(entity.ErrValidation, "range", entity.DetailEnum)
	}

	ch, err := uc.Channel(ctx, ownerID)
	if err != nil {
		return entity.ChannelStats{}, err
	}

	now := uc.now()
	out := entity.ChannelStats{Range: r, FollowerCount: ch.FollowerCount}

	if out.NewFollowers, err = uc.r.Follows.NewFollowers(ctx, ch.ID, now.Add(-d)); err != nil {
		return entity.ChannelStats{}, fmt.Errorf("StudioUseCase - Stats - followers: %w", err)
	}

	if out.Upcoming, err = uc.upcoming(ctx, ch.ID, now); err != nil {
		return entity.ChannelStats{}, err
	}

	return out, nil
}

// upcoming is the channel's next occurrence (nil when none) with its reminder count.
func (uc *UseCase) upcoming(ctx context.Context, channelID string, now time.Time) (*entity.UpcomingOccurrence, error) {
	list, err := uc.r.Schedules.ListByChannel(ctx, channelID, entity.ScheduleActive)
	if err != nil {
		return nil, fmt.Errorf("StudioUseCase - Stats - schedules: %w", err)
	}

	var next *entity.UpcomingOccurrence

	for _, s := range list {
		occ := s.Occurrences(now, now.Add(uc.cfg.Horizon), 1)
		if len(occ) == 1 && (next == nil || occ[0].Before(next.OccurrenceAt)) {
			next = &entity.UpcomingOccurrence{ScheduleID: s.ID, OccurrenceAt: occ[0], Title: s.Title}
		}
	}

	if next == nil {
		return nil, nil //nolint:nilnil // no occurrence to come is not an error
	}

	counts, err := uc.r.Reminders.Counts(ctx, []string{next.ScheduleID}, next.OccurrenceAt)
	if err != nil {
		return nil, fmt.Errorf("StudioUseCase - Stats - reminders: %w", err)
	}

	next.ReminderCount = counts[entity.KeyOf(next.ScheduleID, next.OccurrenceAt)]

	return next, nil
}

// ---- Catalog --------------------------------------------------------------------------------------

// Categories -.
func (uc *UseCase) Categories(ctx context.Context, group *entity.CategoryGroup, s entity.CategorySort) ([]entity.Category, error) {
	list, err := uc.r.Categories.Categories(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("CatalogUseCase - Categories: %w", err)
	}

	uc.attachViewers(ctx, list)

	if s == entity.CategoryByViewers {
		sort.SliceStable(list, func(i, j int) bool { return viewers(list[i]) > viewers(list[j]) })
	}

	return list, nil
}

// Category -.
func (uc *UseCase) Category(ctx context.Context, slug string) (entity.Category, error) {
	c, err := uc.r.Categories.BySlug(ctx, slug)
	if err != nil {
		return entity.Category{}, fmt.Errorf("CatalogUseCase - Category: %w", err)
	}

	list := []entity.Category{c}
	uc.attachViewers(ctx, list)

	return list[0], nil
}

func viewers(c entity.Category) int {
	if c.ViewerCount == nil {
		return 0
	}

	return *c.ViewerCount
}

// attachViewers sums the live viewers of each category; counts stay nil when unavailable.
func (uc *UseCase) attachViewers(ctx context.Context, list []entity.Category) {
	if !uc.enrich.Available() {
		return
	}

	byCat, err := uc.r.Categories.LiveStreamIDsByCategory(ctx)
	if err != nil {
		uc.l.Warn("catalog - live streams by category: %v", err)

		return
	}

	ids := []string{}
	for _, streams := range byCat {
		ids = append(ids, streams...)
	}

	counts := uc.enrich.Counts(ctx, ids)
	if counts == nil {
		return
	}

	for i, c := range list {
		n := sum(byCat[c.ID], counts)
		list[i].ViewerCount = &n
	}
}

func sum(ids []string, counts map[string]int) int {
	n := 0
	for _, id := range ids {
		n += counts[id]
	}

	return n
}

// groupHue tints the generated covers by group.
func groupHue(g entity.CategoryGroup) int {
	switch g {
	case entity.GroupGames:
		return _hueGames
	case entity.GroupEsports:
		return _hueEsports
	case entity.GroupMusic:
		return _hueMusic
	case entity.GroupIRL:
		return _hueIRL
	case entity.GroupCreative:
		return _hueCreative
	}

	return 0
}

// EnsureCategoryCovers uploads a generated cover for every category without one (categories/<slug>.jpg).
// Real artwork replaces the object in the bucket without a code change.
func (uc *UseCase) EnsureCategoryCovers(ctx context.Context) (int, error) {
	list, err := uc.r.Categories.Categories(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("CatalogUseCase - covers: %w", err)
	}

	n := 0

	for _, c := range list {
		key := "categories/" + c.Slug + ".jpg"

		exists, xerr := uc.r.Objects.Exists(ctx, key)
		if xerr != nil {
			return n, fmt.Errorf("CatalogUseCase - covers - exists: %w", xerr)
		}

		if exists {
			continue
		}

		img, cerr := uc.img.Cover(c.Slug, groupHue(c.Group), _coverWidth, _coverHeight)
		if cerr != nil {
			return n, fmt.Errorf("CatalogUseCase - covers - draw: %w", cerr)
		}

		if err = uc.r.Objects.Put(ctx, key, img, _jpegType, _coverCacheControl); err != nil {
			return n, fmt.Errorf("CatalogUseCase - covers - put: %w", err)
		}

		n++
	}

	return n, nil
}
