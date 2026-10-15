package chat_test

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

// world is an in-memory chat: one room, its channel and the Redis state.
type world struct {
	mu sync.Mutex

	now      time.Time
	room     *entity.Room
	settings entity.ChatSettings
	ownerID  string
	roles    map[string]entity.ChannelRole
	banned   map[string]bool
	timeouts map[string]time.Time
	users    map[string]entity.User
	follows  map[string]time.Time
	tiers    map[string]int

	inserted  []entity.Message
	accepted  map[string]string
	lastBody  map[string]string
	buckets   map[string]int
	slowUntil map[string]time.Time
	published []published
	openRooms []string

	settingsEvents []events.ChatSettingsChanged
}

type published struct {
	topic, typ string
	data       any
}

func newWorld() *world {
	return &world{
		now:      time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
		room:     &entity.Room{StreamID: streamID, ChannelID: channelID, Status: entity.RoomOpen},
		settings: entity.ChatSettings{ChannelID: channelID},
		roles:    map[string]entity.ChannelRole{}, banned: map[string]bool{}, timeouts: map[string]time.Time{},
		users: map[string]entity.User{}, follows: map[string]time.Time{}, tiers: map[string]int{},
		accepted: map[string]string{}, lastBody: map[string]string{}, buckets: map[string]int{},
		slowUntil: map[string]time.Time{},
	}
}

func (w *world) clock() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.now
}

func (w *world) advance(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.now = w.now.Add(d)
}

// ---- RoomRepo ----

type fakeRooms struct {
	repo.RoomRepo
	w *world
}

func (f fakeRooms) SendContext(_ context.Context, streamID, userID string) (entity.SendContext, error) {
	w := f.w
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.room == nil || w.room.StreamID != streamID {
		return entity.SendContext{}, entity.ErrNotFound
	}

	sc := entity.SendContext{Room: *w.room, Settings: w.settings, OwnerID: w.ownerID}
	if r, ok := w.roles[userID]; ok {
		sc.Role = &r
	}

	sc.Restriction.Banned = w.banned[userID]
	if until, ok := w.timeouts[userID]; ok {
		sc.Restriction.TimedOutUntil = &until
	}

	if u, ok := w.users[userID]; ok {
		sc.User = &u
	}

	return sc, nil
}

func (f fakeRooms) OpenRoomsOfChannel(context.Context, string) ([]string, error) {
	return f.w.openRooms, nil
}

// ---- MessageRepo ----

type fakeMessages struct {
	repo.MessageRepo
	w *world
}

func (f fakeMessages) InsertMessage(_ context.Context, m entity.Message) (entity.Message, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	m.ID = "m" + strconv.Itoa(len(f.w.inserted))
	m.CreatedAt = f.w.now
	f.w.inserted = append(f.w.inserted, m)

	return m, nil
}

func (f fakeMessages) History(_ context.Context, _ string, _ *string, limit int) ([]entity.Message, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	out := []entity.Message{}
	for i := len(f.w.inserted) - 1; i >= 0 && len(out) < limit; i-- {
		m := f.w.inserted[i]
		m.User = entity.Author{ID: m.User.ID, Username: m.User.Username, DisplayName: m.User.DisplayName}
		out = append(out, m)
	}

	return out, nil
}

// ---- SettingsRepo, RoleRepo, EmoteRepo, UserRepo ----

type fakeSettings struct {
	repo.SettingsRepo
	w *world
}

func (f fakeSettings) Settings(context.Context, string) (entity.ChatSettings, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	return f.w.settings, nil
}

func (f fakeSettings) SaveSettings(_ context.Context, s entity.ChatSettings, change *events.ChatSettingsChanged) (entity.ChatSettings, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	if change != nil {
		f.w.settingsEvents = append(f.w.settingsEvents, *change)
	}

	at := f.w.now
	s.UpdatedAt = &at
	f.w.settings = s

	return s, nil
}

type fakeRoles struct {
	repo.RoleRepo
	w *world
}

func (f fakeRoles) Roles(_ context.Context, _ string, ids []string) (map[string]entity.ChannelRole, error) {
	out := map[string]entity.ChannelRole{}

	for _, id := range ids {
		if r, ok := f.w.roles[id]; ok {
			out[id] = r
		}
	}

	return out, nil
}

type fakeEmotes struct{}

func (fakeEmotes) GlobalEmotes(context.Context) ([]entity.Emote, error) {
	return []entity.Emote{{ID: "e1", Code: "nxGG", ImageURL: "/emotes/nxGG.webp", Scope: "GLOBAL"}}, nil
}

type fakeUsers struct {
	repo.UserRepo
	w *world
}

func (f fakeUsers) UserIDsByUsername(_ context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}

	for _, u := range f.w.users {
		for _, n := range names {
			if n == u.Username {
				out[n] = u.ID
			}
		}
	}

	return out, nil
}

func (f fakeUsers) Channel(_ context.Context, channelID string) (entity.Channel, error) {
	if f.w.ownerID == "" || channelID != f.w.room.ChannelID {
		return entity.Channel{}, entity.ErrNotFound
	}

	return entity.Channel{ID: channelID, OwnerID: f.w.ownerID}, nil
}

// ---- Redis ----

type fakeGuard struct {
	repo.SendGuard
	w *world
}

func (f fakeGuard) AcceptedMessage(_ context.Context, userID, clientMsgID string) (id string, found bool, err error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	id, found = f.w.accepted[userID+clientMsgID]

	return id, found, nil
}

func (f fakeGuard) RememberMessage(_ context.Context, userID, streamID, clientMsgID, messageID, hash string) error {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	f.w.accepted[userID+clientMsgID] = messageID
	f.w.lastBody[userID+streamID] = hash

	return nil
}

func (f fakeGuard) LastBodyHash(_ context.Context, userID, streamID string) (string, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	return f.w.lastBody[userID+streamID], nil
}

// Take is a counter per key: capacity takes, then refused for 1.5 s.
func (f fakeGuard) Take(_ context.Context, key string, capacity int, _ time.Duration) (bool, time.Duration, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	if f.w.buckets[key] >= capacity {
		return false, 1500 * time.Millisecond, nil
	}

	f.w.buckets[key]++

	return true, 0, nil
}

func (f fakeGuard) StartSlow(_ context.Context, streamID, userID string, d time.Duration) (bool, time.Duration, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	key := streamID + userID
	if until := f.w.slowUntil[key]; until.After(f.w.now) {
		return false, until.Sub(f.w.now), nil
	}

	f.w.slowUntil[key] = f.w.now.Add(d)

	return true, 0, nil
}

func (f fakeGuard) SlowLeft(_ context.Context, streamID, userID string) (time.Duration, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	return max(f.w.slowUntil[streamID+userID].Sub(f.w.now), 0), nil
}

type fakeAudience struct {
	repo.AudienceStore
	w *world
}

func (f fakeAudience) Audience(_ context.Context, _, userID string) (*time.Time, int, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	var at *time.Time
	if t, ok := f.w.follows[userID]; ok {
		at = &t
	}

	return at, f.w.tiers[userID], nil
}

func (f fakeAudience) SubTiers(_ context.Context, _ string, ids []string) (map[string]int, error) {
	out := map[string]int{}

	for _, id := range ids {
		if t := f.w.tiers[id]; t > 0 {
			out[id] = t
		}
	}

	return out, nil
}

type fakePub struct{ w *world }

func (f fakePub) Publish(_ context.Context, topic entity.Topic, typ string, data any) (entity.Event, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	f.w.published = append(f.w.published, published{topic: topic.String(), typ: typ, data: data})

	return entity.Event{Topic: topic.String(), Seq: int64(len(f.w.published)), Type: typ}, nil
}
