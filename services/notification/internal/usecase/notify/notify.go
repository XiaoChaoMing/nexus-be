// Package notify implements the notification-svc use cases: sources, the center, settings, the copies,
// account e-mails and the e-mail / retention jobs.
package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

const (
	// NOTIFY_FANOUT_BATCH.
	_fanoutBatch = 500
	// NOTIFY_MENTION_GROUP_WINDOW, NOTIFY_FOLLOWER_GROUP_WINDOW.
	_mentionWindow  = 5 * time.Minute
	_followerWindow = 60 * time.Minute
	_defaultLimit   = 20
	_maxLimit       = 50
	_backfillPage   = 500
	_dayLayout      = "20060102"
	_unsubscribe    = "/notifications/unsubscribe?token="
)

// Config are the NXL-E values of the use cases.
type Config struct {
	// WebBaseURL prefixes every link of an e-mail.
	WebBaseURL        string
	UnsubscribeSecret string
	LiveCooldown      time.Duration
	LiveEmailCooldown time.Duration
	EmailDailyMax     int
	Retention         time.Duration
	// VerifyTTL, ResetTTL and LockDuration are only shown in the account e-mails (identity's values).
	VerifyTTL    time.Duration
	ResetTTL     time.Duration
	LockDuration time.Duration
}

// Repos are the stores and clients the use cases need.
type Repos struct {
	Notifications repo.NotificationRepo
	Settings      repo.SettingRepo
	Recipients    repo.RecipientRepo
	Replica       repo.ReplicaRepo
	Snapshot      repo.SnapshotClient
	Emails        repo.EmailRepo
	Throttle      repo.Throttle
	Mailer        repo.Mailer
}

// UseCase implements every notification-svc use case.
type UseCase struct {
	r   Repos
	cfg Config
	l   logger.Interface
	obs usecase.Observer
	now func() time.Time

	mu        sync.Mutex
	locations map[string]*time.Location
}

var (
	_ usecase.Center   = (*UseCase)(nil)
	_ usecase.Settings = (*UseCase)(nil)
	_ usecase.Sources  = (*UseCase)(nil)
	_ usecase.Replica  = (*UseCase)(nil)
	_ usecase.AuthMail = (*UseCase)(nil)
	_ usecase.Jobs     = (*UseCase)(nil)
)

// Option -.
type Option func(*UseCase)

// WithObserver -.
func WithObserver(o usecase.Observer) Option { return func(uc *UseCase) { uc.obs = o } }

// Clock replaces time.Now (tests).
func Clock(now func() time.Time) Option { return func(uc *UseCase) { uc.now = now } }

// New -.
func New(r Repos, cfg Config, l logger.Interface, opts ...Option) *UseCase {
	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")

	uc := &UseCase{r: r, cfg: cfg, l: l, obs: nopObserver{}, now: time.Now, locations: map[string]*time.Location{}}
	for _, o := range opts {
		o(uc)
	}

	return uc
}

type nopObserver struct{}

func (nopObserver) Created(entity.Type, bool)               {}
func (nopObserver) Suppressed(entity.Type, string)          {}
func (nopObserver) EmailSent(string, string, time.Duration) {}
func (nopObserver) Read(string)                             {}
func (nopObserver) EventLag(string, time.Duration)          {}

// location returns the recipient's time zone (the platform's when unknown); loaded zones are cached.
func (uc *UseCase) location(tz string) *time.Location {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	if loc, ok := uc.locations[tz]; ok {
		return loc
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		if loc, err = time.LoadLocation(entity.DefaultTimezone); err != nil {
			loc = time.UTC
		}
	}

	uc.locations[tz] = loc

	return loc
}

// dayKey is the recipient's calendar day for the daily e-mail cap (BR-E13).
func (uc *UseCase) dayKey(r entity.Recipient) string {
	return uc.now().In(uc.location(r.Timezone)).Format(_dayLayout)
}

// formatTime renders t in the recipient's zone and language.
func (uc *UseCase) formatTime(t time.Time, r entity.Recipient) string {
	local := t.In(uc.location(r.Timezone))
	if locale(r.Locale) == "en" {
		return local.Format("Jan 2, 2006 3:04 PM (MST)")
	}

	return local.Format("15:04 02/01/2006 (MST)")
}

func locale(l string) string {
	l = strings.ToLower(l)
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}

	if l == "en" {
		return "en"
	}

	return entity.DefaultLocale
}

// absolute turns a web path into the link of an e-mail.
func (uc *UseCase) absolute(path *string) string {
	if path == nil {
		return uc.cfg.WebBaseURL + "/notifications"
	}

	return uc.cfg.WebBaseURL + *path
}

func (uc *UseCase) unsubscribeURL(userID string, t entity.Type) string {
	token, ok := entity.UnsubscribeToken(uc.cfg.UnsubscribeSecret, userID, t)
	if !ok {
		return ""
	}

	return uc.cfg.WebBaseURL + _unsubscribe + token
}

func marshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("notify - payload: %w", err)
	}

	return b, nil
}

func ptr[T any](v T) *T { return &v }

// channelRef is the channel object of a payload (NXL-E §7.4).
type channelRef struct {
	ID          string  `json:"id"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

func refOf(c entity.ChannelCopy) channelRef {
	return channelRef{ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL}
}
