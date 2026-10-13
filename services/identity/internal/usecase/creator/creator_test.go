package creator_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/creator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUsers serves GetByID only; the other UserRepo methods are not used by the creator use case.
type fakeUsers struct {
	repo.UserRepo

	users map[string]entity.User
}

func (f fakeUsers) GetByID(_ context.Context, id string) (entity.User, error) {
	u, ok := f.users[id]
	if !ok {
		return entity.User{}, entity.ErrUserNotFound
	}

	return u, nil
}

// fakeApps mirrors the SQL rules: one PENDING per user, conditional status updates.
type fakeApps struct {
	mu       sync.Mutex
	apps     map[string]entity.Application
	channels map[string]bool
	events   []string
}

func (f *fakeApps) CreatorState(_ context.Context, userID string) (repo.CreatorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := repo.CreatorState{HasChannel: f.channels[userID]}

	for _, a := range f.apps {
		if a.UserID != userID {
			continue
		}

		s.HasApproved = s.HasApproved || a.Status == entity.ApplicationApproved
		s.HasPending = s.HasPending || a.Status == entity.ApplicationPending
	}

	return s, nil
}

func (f *fakeApps) Create(_ context.Context, a entity.Application) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, x := range f.apps {
		if x.UserID == a.UserID && x.Status == entity.ApplicationPending {
			return entity.ErrApplicationPending
		}
	}

	f.apps[a.ID] = a
	f.events = append(f.events, "creator.application_submitted.v1")

	return nil
}

func (f *fakeApps) Latest(_ context.Context, userID string) (*entity.Application, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var latest *entity.Application

	for _, a := range f.apps {
		if a.UserID == userID && (latest == nil || a.CreatedAt.After(latest.CreatedAt)) {
			x := a
			latest = &x
		}
	}

	return latest, nil
}

func (f *fakeApps) Cancel(_ context.Context, userID, id string, now time.Time) error {
	return f.transition(id, func(a *entity.Application) error {
		if a.UserID != userID {
			return entity.ErrApplicationNotFound
		}

		a.Status, a.UpdatedAt = entity.ApplicationCancelled, now

		return nil
	})
}

func (f *fakeApps) List(_ context.Context, status entity.ApplicationStatus, after *repo.ApplicationCursor, limit int) ([]entity.Application, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]entity.Application, 0, len(f.apps))

	for _, a := range f.apps {
		if status != "" && a.Status != status {
			continue
		}

		if after != nil && (a.CreatedAt.Before(after.CreatedAt) || (a.CreatedAt.Equal(after.CreatedAt) && a.ID <= after.ID)) {
			continue
		}

		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}

		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})

	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

func (f *fakeApps) Get(_ context.Context, id string) (entity.Application, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.apps[id]
	if !ok {
		return entity.Application{}, entity.ErrApplicationNotFound
	}

	return a, nil
}

func (f *fakeApps) Approve(_ context.Context, adminID, id string, now time.Time) error {
	return f.review(id, adminID, entity.ApplicationApproved, "", now)
}

func (f *fakeApps) Reject(_ context.Context, adminID, id, reason string, now time.Time) error {
	return f.review(id, adminID, entity.ApplicationRejected, reason, now)
}

func (f *fakeApps) review(id, adminID string, to entity.ApplicationStatus, reason string, now time.Time) error {
	err := f.transition(id, func(a *entity.Application) error {
		a.Status, a.RejectReason, a.ReviewedBy, a.ReviewedAt = to, reason, &adminID, &now

		return nil
	})
	if err == nil {
		f.mu.Lock()
		f.events = append(f.events, "creator."+map[entity.ApplicationStatus]string{
			entity.ApplicationApproved: "approved", entity.ApplicationRejected: "rejected",
		}[to]+".v1")
		f.mu.Unlock()
	}

	return err
}

func (f *fakeApps) transition(id string, fn func(a *entity.Application) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.apps[id]
	if !ok {
		return entity.ErrApplicationNotFound
	}

	if a.Status != entity.ApplicationPending {
		return entity.ErrInvalidState
	}

	if err := fn(&a); err != nil {
		return err
	}

	f.apps[id] = a

	return nil
}

type fakeChannels struct {
	mu   sync.Mutex
	seen map[string]bool
	rows map[string]entity.UserChannel
}

func (f *fakeChannels) UpsertChannel(_ context.Context, eventID string, ch entity.UserChannel) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.seen[eventID] {
		return nil
	}

	f.seen[eventID] = true
	f.rows[ch.UserID] = ch

	return nil
}

const (
	_viewer   = "0190a000-0000-7000-8000-000000000001"
	_admin    = "0190a000-0000-7000-8000-000000000002"
	_creator  = "0190a000-0000-7000-8000-000000000003"
	_unverify = "0190a000-0000-7000-8000-000000000004"
)

type fixture struct {
	uc    *creator.UseCase
	apps  *fakeApps
	chans *fakeChannels
	clock *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	verified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	channelID := uuid.NewString()
	users := fakeUsers{users: map[string]entity.User{
		_viewer:   {ID: _viewer, EmailVerifiedAt: &verified},
		_admin:    {ID: _admin, EmailVerifiedAt: &verified, GlobalRole: entity.RoleAdmin},
		_creator:  {ID: _creator, EmailVerifiedAt: &verified, ChannelID: &channelID},
		_unverify: {ID: _unverify},
	}}

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)

	apps := &fakeApps{apps: map[string]entity.Application{}, channels: map[string]bool{}}
	chans := &fakeChannels{seen: map[string]bool{}, rows: map[string]entity.UserChannel{}}
	// 2026-10-13 18:00 UTC is already 2026-10-14 in Ho Chi Minh City.
	clock := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	f := &fixture{apps: apps, chans: chans, clock: &clock}

	uc := creator.New(apps, users, chans, creator.Config{RequireVerifiedEmail: true, MinAge: 18, Location: loc}, logger.New("error"))
	f.uc = uc.WithClock(func() time.Time { return *f.clock })

	return f
}

func (f *fixture) submit(t *testing.T, userID string) entity.Application {
	t.Helper()

	a, err := f.uc.Submit(context.Background(), usecase.SubmitInput{UserID: userID, LegalName: "  Nguyen Van A ", BirthDate: "2000-01-31"})
	require.NoError(t, err)

	return a
}

func TestSubmit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		userID  string
		birth   string
		prepare func(t *testing.T, f *fixture)
		wantErr error
	}{
		{name: "ok", userID: _viewer, birth: "2000-01-31"},
		{name: "turns 18 today in Ho Chi Minh City", userID: _viewer, birth: "2008-10-14"},
		{name: "underage by one day", userID: _viewer, birth: "2008-10-15", wantErr: entity.ErrUnderage},
		{name: "birth date in the future", userID: _viewer, birth: "2030-01-01", wantErr: entity.ErrInvalidBirthDate},
		{name: "malformed birth date", userID: _viewer, birth: "31/01/2000", wantErr: entity.ErrInvalidBirthDate},
		{name: "e-mail not verified", userID: _unverify, birth: "2000-01-31", wantErr: entity.ErrEmailNotVerified},
		{name: "already owns a channel", userID: _creator, birth: "2000-01-31", wantErr: entity.ErrAlreadyCreator},
		{name: "already pending", userID: _viewer, birth: "2000-01-31", prepare: func(t *testing.T, f *fixture) {
			t.Helper()
			f.submit(t, _viewer)
		}, wantErr: entity.ErrApplicationPending},
		{name: "already approved, channel not created yet", userID: _viewer, birth: "2000-01-31", prepare: func(t *testing.T, f *fixture) {
			t.Helper()

			a := f.submit(t, _viewer)
			_, err := f.uc.Approve(context.Background(), _admin, a.ID)
			require.NoError(t, err)
		}, wantErr: entity.ErrAlreadyCreator},
		{name: "again after a rejection", userID: _viewer, birth: "2000-01-31", prepare: func(t *testing.T, f *fixture) {
			t.Helper()

			a := f.submit(t, _viewer)
			_, err := f.uc.Reject(context.Background(), _admin, a.ID, "missing information")
			require.NoError(t, err)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, f)
			}

			a, err := f.uc.Submit(context.Background(), usecase.SubmitInput{UserID: tc.userID, LegalName: " Nguyen Van A ", BirthDate: tc.birth})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, entity.ApplicationPending, a.Status)
			assert.Equal(t, "Nguyen Van A", a.LegalName)
			assert.Contains(t, f.apps.events, "creator.application_submitted.v1")
		})
	}
}

func TestSubmit_UnverifiedAllowedWhenNotRequired(t *testing.T) {
	t.Parallel()

	users := fakeUsers{users: map[string]entity.User{_unverify: {ID: _unverify}}}
	apps := &fakeApps{apps: map[string]entity.Application{}, channels: map[string]bool{}}
	uc := creator.New(apps, users, &fakeChannels{}, creator.Config{}, logger.New("error"))

	_, err := uc.Submit(context.Background(), usecase.SubmitInput{UserID: _unverify, LegalName: "Nguyen Van A", BirthDate: "1990-05-05"})
	require.NoError(t, err)
}

func TestReview(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	approve := func(f *fixture, adminID, id string) error {
		_, err := f.uc.Approve(context.Background(), adminID, id)

		return err
	}
	reject := func(f *fixture, adminID, id string) error {
		_, err := f.uc.Reject(context.Background(), adminID, id, "  not enough content  ")

		return err
	}

	tests := []struct {
		name       string
		applicant  string
		act        func(f *fixture, adminID, id string) error
		adminID    string
		prepare    func(t *testing.T, f *fixture, id string)
		missing    bool
		wantErr    error
		wantStatus entity.ApplicationStatus
	}{
		{name: "approve", applicant: _viewer, act: approve, adminID: _admin, wantStatus: entity.ApplicationApproved},
		{name: "reject", applicant: _viewer, act: reject, adminID: _admin, wantStatus: entity.ApplicationRejected},
		{name: "own application", applicant: _admin, act: approve, adminID: _admin, wantErr: entity.ErrSelfReview},
		{name: "not found", applicant: _viewer, act: approve, adminID: _admin, missing: true, wantErr: entity.ErrApplicationNotFound},
		{name: "already approved", applicant: _viewer, act: reject, adminID: _admin, prepare: func(t *testing.T, f *fixture, id string) {
			t.Helper()
			require.NoError(t, approve(f, _admin, id))
		}, wantErr: entity.ErrInvalidState},
		{name: "withdrawn by the applicant", applicant: _viewer, act: approve, adminID: _admin, prepare: func(t *testing.T, f *fixture, id string) {
			t.Helper()
			require.NoError(t, f.uc.Cancel(context.Background(), _viewer, id))
		}, wantErr: entity.ErrInvalidState},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			a := f.submit(t, tc.applicant)

			if tc.prepare != nil {
				tc.prepare(t, f, a.ID)
			}

			id := a.ID
			if tc.missing {
				id = uuid.NewString()
			}

			err := tc.act(f, tc.adminID, id)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)

			got, err := f.uc.Get(context.Background(), a.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, got.Status)
			assert.Equal(t, 26, got.Age, "age on 2026-10-14 for a 2000-01-31 birth date")

			if tc.wantStatus == entity.ApplicationRejected {
				assert.Equal(t, "not enough content", got.RejectReason)
			}
		})
	}
}

func TestCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		userID  string
		twice   bool
		wantErr error
	}{
		{name: "own pending", userID: _viewer},
		{name: "someone else's", userID: _admin, wantErr: entity.ErrApplicationNotFound},
		{name: "already withdrawn", userID: _viewer, twice: true, wantErr: entity.ErrInvalidState},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			a := f.submit(t, _viewer)

			if tc.twice {
				require.NoError(t, f.uc.Cancel(context.Background(), _viewer, a.ID))
			}

			err := f.uc.Cancel(context.Background(), tc.userID, a.ID)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)

			mine, err := f.uc.Mine(context.Background(), _viewer)
			require.NoError(t, err)
			require.NotNil(t, mine)
			assert.Equal(t, entity.ApplicationCancelled, mine.Status)
		})
	}
}

func TestMine_None(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	a, err := f.uc.Mine(context.Background(), _viewer)
	require.NoError(t, err)
	assert.Nil(t, a)
}

func TestList_Pagination(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	for i := range 5 {
		id := uuid.NewString()
		f.apps.apps[id] = entity.Application{
			ID: id, UserID: uuid.NewString(), Status: entity.ApplicationPending,
			BirthDate: time.Date(2000, 1, 31, 0, 0, 0, 0, time.UTC), CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
	}

	var (
		seen   []time.Time
		cursor string
	)

	for range 3 {
		page, err := f.uc.List(context.Background(), "PENDING", cursor, 2)
		require.NoError(t, err)

		for _, a := range page.Items {
			seen = append(seen, a.CreatedAt)
		}

		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}

	require.Len(t, seen, 5)
	assert.True(t, sort.SliceIsSorted(seen, func(i, j int) bool { return seen[i].Before(seen[j]) }), "oldest first")
	assert.Empty(t, cursor, "last page has no cursor")

	_, err := f.uc.List(context.Background(), "ALL", "not-a-cursor", 2)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)
}

func TestChannelCreated_Idempotent(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ev := uuid.NewString()
	ch := entity.UserChannel{UserID: _viewer, ChannelID: uuid.NewString(), Handle: "viewer", DisplayName: "Viewer"}

	require.NoError(t, f.uc.ChannelCreated(context.Background(), ev, ch))
	require.NoError(t, f.uc.ChannelCreated(context.Background(), ev, entity.UserChannel{UserID: _viewer, ChannelID: uuid.NewString()}))
	assert.Equal(t, ch, f.chans.rows[_viewer], "a redelivery does not overwrite")
}
