package moderation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/moderation"
	"github.com/stretchr/testify/require"
)

const (
	channelID = "0199d4a2-0000-7000-8000-0000000000cc"
	streamID  = "0199d4a2-0000-7000-8000-0000000000aa"
	owner     = "0199d4a2-0000-7000-8000-000000000001"
	mod       = "0199d4a2-0000-7000-8000-000000000002"
	mod2      = "0199d4a2-0000-7000-8000-000000000003"
	viewer    = "0199d4a2-0000-7000-8000-000000000004"
	vip       = "0199d4a2-0000-7000-8000-000000000005"
	admin     = "0199d4a2-0000-7000-8000-000000000006"
	ghost     = "0199d4a2-0000-7000-8000-000000000404"
	msgID     = "0199d4a2-0000-7000-8000-0000000000ee"
)

// world is one channel with an open room; restrictions and pins are recorded.
type world struct {
	mu        sync.Mutex
	roles     map[string]entity.ChannelRole
	author    string
	deleted   bool
	actions   []entity.ModAction
	banned    map[string]bool
	published []string
	terms     []entity.BlockedTerm
}

func newWorld() *world {
	return &world{
		roles:  map[string]entity.ChannelRole{mod: entity.ChannelRoleModerator, mod2: entity.ChannelRoleModerator, vip: entity.ChannelRoleVIP},
		author: viewer, banned: map[string]bool{},
	}
}

type fakeRooms struct {
	repo.RoomRepo
	w *world
}

func (f fakeRooms) SendContext(_ context.Context, id, _ string) (entity.SendContext, error) {
	if id != streamID {
		return entity.SendContext{}, entity.ErrNotFound
	}

	return entity.SendContext{Room: entity.Room{StreamID: streamID, ChannelID: channelID, Status: entity.RoomOpen}, OwnerID: owner}, nil
}

type fakeUsers struct {
	repo.UserRepo
	w *world
}

func (fakeUsers) Channel(_ context.Context, id string) (entity.Channel, error) {
	if id != channelID {
		return entity.Channel{}, entity.ErrNotFound
	}

	return entity.Channel{ID: channelID, OwnerID: owner}, nil
}

// _registeredAt is the registration time every fake user carries.
var _registeredAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) //nolint:gochecknoglobals // test fixture

func (fakeUsers) Users(_ context.Context, ids []string) (map[string]entity.User, error) {
	out := map[string]entity.User{}

	for _, id := range ids {
		if id != ghost {
			out[id] = entity.User{ID: id, Username: "u" + id[len(id)-3:], CreatedAt: _registeredAt}
		}
	}

	return out, nil
}

func (fakeUsers) UserIDsByUsername(_ context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}

	for _, n := range names {
		switch n {
		case "owner":
			out[n] = owner
		case "viewer":
			out[n] = viewer
		}
	}

	return out, nil
}

func (fakeUsers) ChannelInfo(_ context.Context, ids []string) (map[string]entity.ChannelInfo, error) {
	out := map[string]entity.ChannelInfo{}

	for i, id := range ids {
		out[id] = entity.ChannelInfo{ID: id, DisplayName: []string{"Zeta", "alpha", "Beta"}[i%3]}
	}

	return out, nil
}

type fakeRoles struct {
	repo.RoleRepo
	w *world
}

func (f fakeRoles) Roles(_ context.Context, _ string, ids []string) (map[string]entity.ChannelRole, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	out := map[string]entity.ChannelRole{}

	for _, id := range ids {
		if r, ok := f.w.roles[id]; ok {
			out[id] = r
		}
	}

	return out, nil
}

func (f fakeRoles) Assign(_ context.Context, g entity.RoleGrant, maxPerRole int, _ entity.ActorRole) (entity.RoleGrant, bool, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	if maxPerRole != entity.ChannelRoleMax {
		return entity.RoleGrant{}, false, entity.ErrLimitReached
	}

	_, had := f.w.roles[g.User.ID]
	f.w.roles[g.User.ID] = g.Role

	return g, !had, nil
}

func (f fakeRoles) RolesOfUser(context.Context, string) ([]entity.RoleGrant, error) {
	return []entity.RoleGrant{{ChannelID: "c1"}, {ChannelID: "c2"}, {ChannelID: "c3"}}, nil
}

type fakeMod struct {
	repo.ModerationRepo
	w *world
}

func (f fakeMod) Message(_ context.Context, _, id string) (entity.Message, error) {
	if id != msgID {
		return entity.Message{}, entity.ErrNotFound
	}

	return entity.Message{ID: msgID, StreamID: streamID, User: entity.Author{ID: f.w.author}, Deleted: f.w.deleted}, nil
}

func (f fakeMod) DeleteMessage(_ context.Context, a entity.ModAction) (deleted, pinned bool, err error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	f.w.actions = append(f.w.actions, a)

	return true, true, nil
}

func (f fakeMod) Restrict(_ context.Context, a entity.ModAction) (rooms, unpinned []string, err error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	if a.Type == entity.ModTimeout && f.w.banned[*a.TargetUserID] {
		return nil, nil, entity.ErrInvalidState
	}

	if a.Type == entity.ModBan {
		f.w.banned[*a.TargetUserID] = true
	}

	f.w.actions = append(f.w.actions, a)

	return []string{streamID}, nil, nil
}

func (f fakeMod) Lift(_ context.Context, a entity.ModAction) (lifted bool, rooms []string, err error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	was := f.w.banned[*a.TargetUserID]
	delete(f.w.banned, *a.TargetUserID)

	return was, []string{streamID}, nil
}

func (f fakeMod) Restriction(_ context.Context, _, userID string) (*entity.ActiveRestriction, error) {
	return &entity.ActiveRestriction{User: entity.UserRef{ID: userID}, Type: entity.RestrictBan}, nil
}

type fakeTerms struct {
	repo.TermRepo
	w *world
}

func (f fakeTerms) AddTerm(_ context.Context, t entity.BlockedTerm, maxTerms int, a events.AuditRecorded) (entity.BlockedTerm, bool, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	for _, e := range f.w.terms {
		if e.Term == t.Term {
			return e, false, nil
		}
	}

	if len(f.w.terms) >= maxTerms || a.Action != "BLOCKED_TERM_ADDED" {
		return entity.BlockedTerm{}, false, entity.ErrLimitReached
	}

	f.w.terms = append(f.w.terms, t)

	return t, true, nil
}

func (f fakeTerms) Terms(context.Context, string) ([]entity.BlockedTerm, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	return append([]entity.BlockedTerm(nil), f.w.terms...), nil
}

type fakeAudience struct{ repo.AudienceStore }

func (fakeAudience) Audience(context.Context, string, string) (*time.Time, int, error) {
	return nil, 2, nil
}

type fakeLive struct{ repo.LiveStore }

func (fakeLive) Live(_ context.Context, _ []string) (map[string]entity.LiveInfo, error) {
	return map[string]entity.LiveInfo{"c3": {StreamID: "s3"}}, nil
}

type fakePub struct{ w *world }

func (f fakePub) Publish(_ context.Context, topic entity.Topic, typ string, _ any) (entity.Event, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()

	f.w.published = append(f.w.published, topic.String()+" "+typ)

	return entity.Event{}, nil
}

func newUC(w *world) *moderation.UseCase {
	return moderation.New(moderation.Repos{
		Rooms: fakeRooms{w: w}, Mod: fakeMod{w: w}, Terms: fakeTerms{w: w}, Roles: fakeRoles{w: w}, Users: fakeUsers{w: w},
		Audience: fakeAudience{}, Live: fakeLive{},
	}, fakePub{w: w}, moderation.Config{TimeoutMax: 14 * 24 * time.Hour}, logger.New("error"))
}

func as(id string) entity.Principal {
	p := entity.Principal{UserID: id}
	if id == admin {
		p.GlobalRole = entity.GlobalRoleAdmin
	}

	return p
}

func TestTimeoutPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		actor, target string
		d             time.Duration
		want          error
		details       map[string]string
	}{
		{name: "mod on viewer", actor: mod, target: viewer, d: time.Minute},
		{name: "owner on mod (remove the role first)", actor: owner, target: mod, d: time.Minute, want: entity.ErrProtectedTarget},
		{name: "admin on mod", actor: admin, target: mod, d: time.Minute},
		{name: "mod on vip", actor: mod, target: vip, d: time.Minute},
		{name: "mod on other mod", actor: mod, target: mod2, d: time.Minute, want: entity.ErrProtectedTarget, details: map[string]string{"target": "PROTECTED"}},
		{name: "mod on owner", actor: mod, target: owner, d: time.Minute, want: entity.ErrProtectedTarget},
		{name: "admin on owner", actor: admin, target: owner, d: time.Minute, want: entity.ErrProtectedTarget},
		{name: "self", actor: mod, target: mod, d: time.Minute, want: entity.ErrSelfTarget, details: map[string]string{"userId": "INVALID"}},
		{name: "viewer cannot moderate", actor: viewer, target: vip, d: time.Minute, want: entity.ErrForbidden},
		{name: "vip cannot moderate", actor: vip, target: viewer, d: time.Minute, want: entity.ErrForbidden},
		{name: "guest", actor: "", target: viewer, d: time.Minute, want: entity.ErrUnauthenticated},
		{name: "unknown user", actor: mod, target: ghost, d: time.Minute, want: entity.ErrNotFound},
		{name: "too long", actor: mod, target: viewer, d: 15 * 24 * time.Hour, want: entity.ErrValidation, details: map[string]string{"durationSec": "MAX"}},
		{name: "too short", actor: mod, target: viewer, d: time.Millisecond, want: entity.ErrValidation, details: map[string]string{"durationSec": "MIN"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			_, err := newUC(w).Timeout(context.Background(), as(tc.actor), channelID, tc.target, tc.d, nil)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				if tc.details != nil {
					require.Equal(t, tc.details, entity.DetailsOf(err))
				}

				require.Empty(t, w.actions)

				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{"chat:" + streamID + " user_cleared"}, w.published)
		})
	}
}

func TestBanThenTimeoutAndUnban(t *testing.T) {
	t.Parallel()

	w := newWorld()
	uc := newUC(w)
	ctx := context.Background()

	_, err := uc.Ban(ctx, as(mod), channelID, viewer, nil)
	require.NoError(t, err)

	_, err = uc.Timeout(ctx, as(mod), channelID, viewer, time.Minute, nil)
	require.ErrorIs(t, err, entity.ErrInvalidState)

	require.NoError(t, uc.Unban(ctx, as(mod), channelID, viewer))
	require.NoError(t, uc.Unban(ctx, as(mod), channelID, viewer), "nothing to lift")
	require.Equal(t, []string{"chat:" + streamID + " user_cleared", "chat:" + streamID + " user_restored"}, w.published)

	long := string(make([]byte, 501))
	_, err = uc.Ban(ctx, as(mod), channelID, viewer, &long)
	require.ErrorIs(t, err, entity.ErrValidation)
}

func TestDeleteMessageRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		actor, author string
		want          error
	}{
		{name: "mod deletes viewer", actor: mod, author: viewer},
		{name: "mod deletes own", actor: mod, author: mod},
		{name: "mod deletes other mod", actor: mod, author: mod2, want: entity.ErrProtectedTarget},
		{name: "mod deletes owner", actor: mod, author: owner, want: entity.ErrProtectedTarget},
		{name: "owner deletes mod", actor: owner, author: mod},
		{name: "admin deletes owner", actor: admin, author: owner},
		{name: "viewer deletes", actor: viewer, author: viewer, want: entity.ErrForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.author = tc.author
			err := newUC(w).DeleteMessage(context.Background(), as(tc.actor), streamID, msgID)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{"chat:" + streamID + " delete", "chat:" + streamID + " unpin"}, w.published)
		})
	}

	w := newWorld()
	w.deleted = true
	require.NoError(t, newUC(w).DeleteMessage(context.Background(), as(mod), streamID, msgID), "already deleted")
	require.Empty(t, w.published)

	require.ErrorIs(t, newUC(newWorld()).DeleteMessage(context.Background(), as(mod), streamID, ghost), entity.ErrNotFound)
	require.ErrorIs(t, newUC(newWorld()).DeleteMessage(context.Background(), as(mod), ghost, msgID), entity.ErrNotFound)
}

func TestTerms(t *testing.T) {
	t.Parallel()

	w := newWorld()
	uc := newUC(w)
	ctx := context.Background()

	got, created, err := uc.AddTerm(ctx, as(mod), channelID, "Spam")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "spam", got.Term)

	_, created, err = uc.AddTerm(ctx, as(mod), channelID, "SPẢM")
	require.NoError(t, err)
	require.False(t, created, "same after normalization")

	_, _, err = uc.AddTerm(ctx, as(viewer), channelID, "other")
	require.ErrorIs(t, err, entity.ErrForbidden)

	_, _, err = uc.AddTerm(ctx, as(mod), channelID, "x")
	require.ErrorIs(t, err, entity.ErrValidation)

	terms, err := uc.BlockedTerms(ctx, channelID)
	require.NoError(t, err)
	require.Equal(t, []string{"spam"}, terms)

	w.terms = make([]entity.BlockedTerm, entity.BlockedTermsMax)
	for i := range w.terms {
		w.terms[i].Term = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}

	_, _, err = uc.AddTerm(ctx, as(mod), channelID, "brand new")
	require.ErrorIs(t, err, entity.ErrLimitReached)
	require.Equal(t, map[string]string{"limit": "100"}, entity.DetailsOf(err))
}

func TestRoles(t *testing.T) {
	t.Parallel()

	w := newWorld()
	uc := newUC(w)
	ctx := context.Background()

	_, created, err := uc.Assign(ctx, as(owner), channelID, usecase.RoleTarget{Username: "viewer"}, entity.ChannelRoleModerator)
	require.NoError(t, err)
	require.True(t, created)

	_, _, err = uc.Assign(ctx, as(owner), channelID, usecase.RoleTarget{Username: "owner"}, entity.ChannelRoleVIP)
	require.Equal(t, map[string]string{"username": "INVALID"}, entity.DetailsOf(err))

	_, _, err = uc.Assign(ctx, as(owner), channelID, usecase.RoleTarget{Username: "nobody"}, entity.ChannelRoleVIP)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, _, err = uc.Assign(ctx, as(mod), channelID, usecase.RoleTarget{Username: "viewer"}, entity.ChannelRoleVIP)
	require.ErrorIs(t, err, entity.ErrForbidden, "moderators do not manage roles")

	_, created, err = uc.Assign(ctx, as(admin), channelID, usecase.RoleTarget{UserID: viewer}, entity.ChannelRoleVIP)
	require.NoError(t, err)
	require.False(t, created, "changed")

	mine, err := uc.Mine(ctx, viewer)
	require.NoError(t, err)
	require.Len(t, mine, 3)
	require.Equal(t, "c3", mine[0].Channel.ID, "live first")
	require.Equal(t, []string{"alpha", "Zeta"}, []string{mine[1].Channel.DisplayName, mine[2].Channel.DisplayName})
}

func TestCard(t *testing.T) {
	t.Parallel()

	uc := newUC(newWorld())
	ctx := context.Background()

	c, err := uc.Card(ctx, entity.Principal{}, channelID, mod)
	require.NoError(t, err)
	require.Equal(t, "MODERATOR", c.Role)
	require.Equal(t, []string{"MODERATOR", "SUB_T2"}, c.Badges)
	require.Nil(t, c.Restriction, "guests do not see restrictions")
	require.Equal(t, _registeredAt, *c.CreatedAt)

	c, err = uc.Card(ctx, as(mod), channelID, viewer)
	require.NoError(t, err)
	require.NotNil(t, c.Restriction)

	c, err = uc.Card(ctx, as(viewer), channelID, owner)
	require.NoError(t, err)
	require.Equal(t, "OWNER", c.Role)
	require.Nil(t, c.Restriction)

	_, err = uc.Card(ctx, as(viewer), channelID, ghost)
	require.ErrorIs(t, err, entity.ErrNotFound)
}
