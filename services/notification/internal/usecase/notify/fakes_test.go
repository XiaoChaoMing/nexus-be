package notify_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase/notify"
)

const (
	ownerID    = "0199d4a2-0000-7000-8000-000000000001"
	viewerID   = "0199d4a2-0000-7000-8000-000000000002"
	viewer2ID  = "0199d4a2-0000-7000-8000-000000000003"
	senderID   = "0199d4a2-0000-7000-8000-000000000004"
	channelID  = "0199d4a2-0000-7000-8000-000000000010"
	streamID   = "0199d4a2-0000-7000-8000-000000000020"
	eventID    = "0199d4a2-0000-7000-8000-000000000030"
	otherEvent = "0199d4a2-0000-7000-8000-000000000031"
	secret     = "0123456789abcdef0123456789abcdef"
)

var (
	now        = time.Date(2026, 10, 15, 20, 30, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock
	errSMTP    = errors.New("smtp down")
	errBroken  = errors.New("template broken")
	theChannel = entity.ChannelCopy{ //nolint:gochecknoglobals // fixture
		ID: channelID, OwnerID: ownerID, Handle: "kenh", DisplayName: "Kênh K", Status: entity.ChannelActive,
	}
)

func recipient(id string, verified bool) entity.Recipient {
	return entity.Recipient{
		UserID: id, Username: "u" + id[len(id)-2:], DisplayName: "User " + id[len(id)-2:], Email: id[len(id)-2:] + "@x.dev",
		EmailVerified: verified, Locale: "vi", Timezone: entity.DefaultTimezone, Active: true,
	}
}

// fakeStore implements every repo contract the use cases call, in memory.
type fakeStore struct {
	repo.NotificationRepo
	repo.SettingRepo
	repo.ReplicaRepo
	repo.EmailRepo

	mu         sync.Mutex
	channels   map[string]entity.ChannelCopy
	followers  []entity.Recipient
	levels     [][]string
	users      map[string]entity.Recipient
	blocked    map[string]bool
	overrides  map[string]map[entity.Type]entity.Setting
	batches    []batch
	upserts    [][]repo.Pending
	queued     []entity.Email
	processed  map[string]bool
	saved      []entity.Setting
	due        []entity.OutboxEmail
	sent       []string
	failed     []failure
	notifyRows map[string]bool // (user|event) of InsertBatch, to emulate the unique key
}

type batch struct {
	n      entity.Notification
	users  []string
	emails []entity.Email
}

type failure struct {
	id       string
	attempts int
	next     *time.Time
	text     string
}

func newStore() *fakeStore {
	return &fakeStore{
		channels:   map[string]entity.ChannelCopy{channelID: theChannel},
		users:      map[string]entity.Recipient{},
		blocked:    map[string]bool{},
		overrides:  map[string]map[entity.Type]entity.Setting{},
		processed:  map[string]bool{},
		notifyRows: map[string]bool{},
	}
}

func (f *fakeStore) addUsers(rs ...entity.Recipient) {
	for _, r := range rs {
		f.users[r.UserID] = r
	}
}

func (f *fakeStore) Channel(_ context.Context, id string) (entity.ChannelCopy, error) {
	c, ok := f.channels[id]
	if !ok {
		return c, entity.ErrNotFound
	}

	return c, nil
}

func (f *fakeStore) Followers(_ context.Context, _ string, levels []string, _, after string, limit int,
) ([]entity.Recipient, error) {
	f.levels = append(f.levels, levels)

	var out []entity.Recipient

	for _, r := range f.followers {
		if r.UserID > after && len(out) < limit {
			out = append(out, r)
		}
	}

	return out, nil
}

func (f *fakeStore) Recipients(_ context.Context, ids []string) (map[string]entity.Recipient, error) {
	out := map[string]entity.Recipient{}

	for _, id := range ids {
		if r, ok := f.users[id]; ok {
			out[id] = r
		}
	}

	return out, nil
}

func (f *fakeStore) BlockedBy(_ context.Context, _ string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}

	for _, id := range ids {
		if f.blocked[id] {
			out[id] = true
		}
	}

	return out, nil
}

func (f *fakeStore) Overrides(_ context.Context, ids []string) (map[string]map[entity.Type]entity.Setting, error) {
	out := map[string]map[entity.Type]entity.Setting{}

	for _, id := range ids {
		if o, ok := f.overrides[id]; ok {
			out[id] = o
		}
	}

	return out, nil
}

func (f *fakeStore) Save(_ context.Context, userID string, s []entity.Setting) error {
	f.saved = append(f.saved, s...)

	for _, x := range s {
		if f.overrides[userID] == nil {
			f.overrides[userID] = map[entity.Type]entity.Setting{}
		}

		f.overrides[userID][x.Type] = x
	}

	return nil
}

func (f *fakeStore) InsertBatch(_ context.Context, ev string, n entity.Notification, ids []string, emails []entity.Email,
) (repo.BatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.batches = append(f.batches, batch{n: n, users: ids, emails: emails})

	var res repo.BatchResult

	for _, id := range ids {
		if !f.notifyRows[id+"|"+ev] {
			f.notifyRows[id+"|"+ev] = true
			res.Notified = append(res.Notified, id)
		}
	}

	for _, e := range emails {
		if !f.notifyRows["mail|"+e.UserID+"|"+ev] {
			f.notifyRows["mail|"+e.UserID+"|"+ev] = true
			res.Emailed = append(res.Emailed, e.UserID)
		}
	}

	return res, nil
}

func (f *fakeStore) Upsert(_ context.Context, ev string, items []repo.Pending) ([]repo.Upserted, error) {
	if f.processed[ev] {
		return nil, nil
	}

	f.processed[ev] = true
	f.upserts = append(f.upserts, items)

	out := make([]repo.Upserted, 0, len(items))
	for _, it := range items {
		n := it.Notification
		if !it.EmailOnly {
			n.ID = "n-" + n.UserID
		}

		out = append(out, repo.Upserted{Notification: n, Emailed: it.Email != nil})
	}

	return out, nil
}

func (f *fakeStore) QueueEmail(_ context.Context, ev string, e entity.Email) (bool, error) {
	if f.processed[ev] {
		return false, nil
	}

	f.processed[ev] = true
	f.queued = append(f.queued, e)

	return true, nil
}

func (f *fakeStore) Due(context.Context, int, time.Duration) ([]entity.OutboxEmail, error) {
	return f.due, nil
}

func (f *fakeStore) MarkSent(_ context.Context, id string) error {
	f.sent = append(f.sent, id)

	return nil
}

func (f *fakeStore) MarkFailed(_ context.Context, id string, attempts int, next *time.Time, text string) error {
	f.failed = append(f.failed, failure{id: id, attempts: attempts, next: next, text: text})

	return nil
}

// fakeThrottle keeps notify:* in maps.
type fakeThrottle struct {
	inApp, email map[string]string
	today        map[string]int
	counted      map[string]string
	set          []string
}

func newThrottle() *fakeThrottle {
	return &fakeThrottle{inApp: map[string]string{}, email: map[string]string{}, today: map[string]int{}, counted: map[string]string{}}
}

func (t *fakeThrottle) LiveCooldowns(_ context.Context, _ string, ids []string) (inApp, email map[string]string, err error) {
	inApp, email = map[string]string{}, map[string]string{}

	for _, id := range ids {
		if v, ok := t.inApp[id]; ok {
			inApp[id] = v
		}

		if v, ok := t.email[id]; ok {
			email[id] = v
		}
	}

	return inApp, email, nil
}

func (t *fakeThrottle) SetLiveCooldowns(_ context.Context, _, ev string, inApp, email []string, _, _ time.Duration) error {
	for _, id := range inApp {
		t.inApp[id] = ev
		t.set = append(t.set, "app:"+id)
	}

	for _, id := range email {
		t.email[id] = ev
		t.set = append(t.set, "mail:"+id)
	}

	return nil
}

func (t *fakeThrottle) EmailsToday(_ context.Context, days map[string]string) (map[string]int, error) {
	out := map[string]int{}
	for id := range days {
		out[id] = t.today[id]
	}

	return out, nil
}

func (t *fakeThrottle) CountEmails(_ context.Context, days map[string]string) error {
	for id, d := range days {
		t.counted[id] = d
		t.today[id]++
	}

	return nil
}

// fakeMailer renders "<template>.<locale>" and records sends.
type fakeMailer struct {
	renderErr error
	sendErr   error
	sent      []entity.Message
	data      []map[string]string
}

func (m *fakeMailer) Render(template, locale, to string, data map[string]string) (entity.Message, error) {
	if m.renderErr != nil {
		return entity.Message{}, m.renderErr
	}

	m.data = append(m.data, data)

	return entity.Message{To: to, Subject: template + "." + locale}, nil
}

func (m *fakeMailer) Send(_ context.Context, msg entity.Message) error {
	if m.sendErr != nil {
		return m.sendErr
	}

	m.sent = append(m.sent, msg)

	return nil
}

// fakeObserver counts suppressions by reason.
type fakeObserver struct {
	mu         sync.Mutex
	suppressed map[string]int
	emails     map[string]int
}

func newObserver() *fakeObserver {
	return &fakeObserver{suppressed: map[string]int{}, emails: map[string]int{}}
}

func (o *fakeObserver) Created(entity.Type, bool) {}
func (o *fakeObserver) Suppressed(_ entity.Type, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.suppressed[reason]++
}

func (o *fakeObserver) EmailSent(_, status string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.emails[status]++
}
func (o *fakeObserver) Read(string)                    {}
func (o *fakeObserver) EventLag(string, time.Duration) {}

type env struct {
	store    *fakeStore
	throttle *fakeThrottle
	mailer   *fakeMailer
	obs      *fakeObserver
	uc       *notify.UseCase
}

func newEnv() *env {
	e := &env{store: newStore(), throttle: newThrottle(), mailer: &fakeMailer{}, obs: newObserver()}
	e.uc = notify.New(notify.Repos{
		Notifications: e.store, Settings: e.store, Recipients: e.store, Replica: e.store, Emails: e.store,
		Throttle: e.throttle, Mailer: e.mailer,
	}, notify.Config{
		WebBaseURL: "http://web.test/", UnsubscribeSecret: secret, LiveCooldown: 30 * time.Minute,
		LiveEmailCooldown: 12 * time.Hour, EmailDailyMax: 10, Retention: 90 * 24 * time.Hour,
		VerifyTTL: 24 * time.Hour, ResetTTL: 30 * time.Minute, LockDuration: 15 * time.Minute,
	}, logger.New("error"), notify.WithObserver(e.obs), notify.Clock(func() time.Time { return now }))

	return e
}
