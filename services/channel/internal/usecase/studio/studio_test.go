package studio_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/imaging"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/studio"
	"github.com/stretchr/testify/require"
)

var errS3 = errors.New("s3 down")

type fakeRepo struct {
	repo.ChannelRepo
	repo.FollowRepo
	repo.ScheduleRepo
	repo.ReminderRepo
	repo.CategoryRepo

	ch       entity.Channel
	patched  *entity.ProfilePatch
	imageURL *string
	saveErr  error
}

func (f *fakeRepo) ByOwner(_ context.Context, owner string) (entity.Channel, error) {
	if owner != f.ch.OwnerID {
		return entity.Channel{}, entity.ErrNotFound
	}

	return f.ch, nil
}

func (f *fakeRepo) UpdateProfile(_ context.Context, _ string, p entity.ProfilePatch, now time.Time) (entity.Channel, error) {
	f.patched = &p

	if p.Handle != nil {
		if *p.Handle == "taken" {
			return entity.Channel{}, entity.ErrHandleTaken
		}

		f.ch.Handle, f.ch.HandleChangedAt = *p.Handle, &now
	}

	return f.ch, nil
}

func (f *fakeRepo) SetImage(_ context.Context, _ string, _ entity.ImageKind, url string) (*string, error) {
	if f.saveErr != nil {
		return nil, f.saveErr
	}

	old := f.imageURL
	f.imageURL = &url

	return old, nil
}

type fakeObjects struct {
	objects map[string][]byte
	putErr  error
	removed []string
}

func (o *fakeObjects) Put(_ context.Context, key string, body []byte, _, _ string) error {
	if o.putErr != nil {
		return o.putErr
	}

	o.objects[key] = body

	return nil
}

func (o *fakeObjects) Remove(_ context.Context, key string) error {
	o.removed = append(o.removed, key)
	delete(o.objects, key)

	return nil
}

func (o *fakeObjects) Exists(_ context.Context, key string) (bool, error) {
	_, ok := o.objects[key]

	return ok, nil
}

func newUC(f *fakeRepo, o *fakeObjects, now time.Time) *studio.UseCase {
	l := logger.New("error")

	return studio.New(studio.Repos{Channels: f, Follows: f, Schedules: f, Reminders: f, Categories: f, Objects: o},
		imaging.New(), enrich.New(f, nil, l), studio.Config{
			HandleChangeInterval: 30 * 24 * time.Hour, AvatarMaxBytes: 2 << 20, BannerMaxBytes: 5 << 20, MediaBase: "/media",
			Horizon: 90 * 24 * time.Hour,
		}, l).WithClock(func() time.Time { return now })
}

func ptr[T any](v T) *T { return &v }

func TestUpdateProfile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour)

	tests := []struct {
		name      string
		owner     string
		changedAt *time.Time
		patch     entity.ProfilePatch
		wantErr   error
		details   map[string]string
		noWrite   bool
	}{
		{name: "not a creator", owner: "x", patch: entity.ProfilePatch{Bio: ptr("hi")}, wantErr: entity.ErrNotCreator},
		{
			name: "invalid", owner: "u1", patch: entity.ProfilePatch{Bio: ptr(string(make([]rune, 301)))}, wantErr: entity.ErrValidation,
			details: map[string]string{"bio": entity.DetailLength},
		},
		{
			name: "handle taken", owner: "u1", patch: entity.ProfilePatch{Handle: ptr("taken")}, wantErr: entity.ErrHandleTaken,
			details: map[string]string{"handle": entity.DetailTaken},
		},
		{
			name: "second change inside 30 days", owner: "u1", changedAt: &recent, patch: entity.ProfilePatch{Handle: ptr("other")},
			wantErr: entity.ErrHandleTooSoon, details: map[string]string{"nextChangeAt": "2026-11-13T00:00:00Z"},
		},
		{name: "same handle is not a change", owner: "u1", changedAt: &recent, patch: entity.ProfilePatch{Handle: ptr("minhnn")}, noWrite: true},
		{name: "new handle", owner: "u1", patch: entity.ProfilePatch{Handle: ptr("minh_2026")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeRepo{ch: entity.Channel{ID: "c1", OwnerID: "u1", Handle: "minhnn", HandleChangedAt: tc.changedAt}}
			ch, err := newUC(f, &fakeObjects{objects: map[string][]byte{}}, now).UpdateProfile(context.Background(), tc.owner, tc.patch)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, tc.details, entity.DetailsOf(err))

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.noWrite, f.patched == nil)

			if !tc.noWrite {
				require.Equal(t, *tc.patch.Handle, ch.Handle)
			}
		})
	}
}

// pngOf is a 300×300 PNG: big enough for an avatar, too small for a banner.
func pngOf(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 300, 300))))

	return buf.Bytes()
}

func TestSetImage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	f := &fakeRepo{ch: entity.Channel{ID: "c1", OwnerID: "u1"}}
	objs := &fakeObjects{objects: map[string][]byte{}}
	uc := newUC(f, objs, now)
	ctx := context.Background()

	url, err := uc.SetImage(ctx, "u1", entity.ImageAvatar, pngOf(t))
	require.NoError(t, err)
	require.Equal(t, "/media/channels/c1/avatar-1792022400000.jpg", url)
	require.Contains(t, objs.objects, "channels/c1/avatar-1792022400000.jpg")

	uc = newUC(f, objs, now.Add(time.Second))
	_, err = uc.SetImage(ctx, "u1", entity.ImageAvatar, pngOf(t))
	require.NoError(t, err)
	require.Equal(t, []string{"channels/c1/avatar-1792022400000.jpg"}, objs.removed, "the previous image is removed")

	_, err = uc.SetImage(ctx, "u1", entity.ImageAvatar, []byte("GIF89a not supported"))
	require.ErrorIs(t, err, entity.ErrImageFormat)
	require.Equal(t, map[string]string{"file": entity.DetailFormat}, entity.DetailsOf(err))

	_, err = uc.SetImage(ctx, "u1", entity.ImageBanner, pngOf(t))
	require.ErrorIs(t, err, entity.ErrImageTooSmall)
	require.Equal(t, map[string]string{"file": entity.DetailMin}, entity.DetailsOf(err))

	_, err = uc.SetImage(ctx, "x", entity.ImageAvatar, pngOf(t))
	require.ErrorIs(t, err, entity.ErrNotCreator)

	// The database write fails: the new object is removed again, the old one stays.
	f.saveErr = errS3
	uc = newUC(f, objs, now.Add(2*time.Second))
	_, err = uc.SetImage(ctx, "u1", entity.ImageAvatar, pngOf(t))
	require.ErrorIs(t, err, errS3)
	require.Len(t, objs.objects, 1)

	objs.putErr = errS3
	_, err = uc.SetImage(ctx, "u1", entity.ImageAvatar, pngOf(t))
	require.ErrorIs(t, err, errS3)
}

func TestStatsRange(t *testing.T) {
	t.Parallel()

	_, err := newUC(&fakeRepo{}, &fakeObjects{}, time.Now()).Stats(context.Background(), "u1", "1y")
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"range": entity.DetailEnum}, entity.DetailsOf(err))
}
