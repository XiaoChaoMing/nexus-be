package overlay_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/overlay"
	"github.com/stretchr/testify/require"
)

const (
	channelID = "0199d4a2-0000-7000-8000-0000000000c1"
	ownerID   = "0199d4a2-0000-7000-8000-000000000001"
	otherID   = "0199d4a2-0000-7000-8000-000000000002"
)

var errDown = errors.New("down")

type store struct {
	repo.OverlayRepo
	mu    sync.Mutex
	saved map[string]entity.OverlaySettings
	err   error
}

func (s *store) Overlay(_ context.Context, id string) (entity.OverlaySettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if v, ok := s.saved[id]; ok {
		return v, nil
	}

	return entity.DefaultOverlaySettings(id), nil
}

func (s *store) SaveOverlay(_ context.Context, v entity.OverlaySettings) (entity.OverlaySettings, error) {
	if s.err != nil {
		return entity.OverlaySettings{}, s.err
	}

	at := time.Unix(1_700_000_000, 0)
	v.UpdatedAt = &at

	s.mu.Lock()
	s.saved[v.ChannelID] = v
	s.mu.Unlock()

	return v, nil
}

type users struct{ repo.UserRepo }

func (users) Channel(_ context.Context, id string) (entity.Channel, error) {
	if id != channelID {
		return entity.Channel{}, entity.ErrNotFound
	}

	return entity.Channel{ID: id, OwnerID: ownerID}, nil
}

type published struct {
	topic entity.Topic
	typ   string
	data  any
}

type bus struct {
	mu   sync.Mutex
	sent []published
	err  error
}

func (b *bus) Publish(_ context.Context, topic entity.Topic, typ string, data any) (entity.Event, error) {
	if b.err != nil {
		return entity.Event{}, b.err
	}

	b.mu.Lock()
	b.sent = append(b.sent, published{topic, typ, data})
	b.mu.Unlock()

	return entity.Event{}, nil
}

func newUC(t *testing.T, now *time.Time) (*overlay.UseCase, *store, *bus) {
	t.Helper()

	st, b := &store{saved: map[string]entity.OverlaySettings{}}, &bus{}
	uc := overlay.New(overlay.Repos{Overlay: st, Users: users{}}, b, logger.New("error"),
		overlay.WithClock(func() time.Time { return *now }))

	return uc, st, b
}

func owner() entity.Principal { return entity.Principal{UserID: ownerID} }

func TestGetDefaultsAndUnknownChannel(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, _, _ := newUC(t, &now)

	got, err := uc.Get(context.Background(), channelID)
	require.NoError(t, err)
	require.Nil(t, got.UpdatedAt)
	require.NoError(t, got.Validate())

	_, err = uc.Get(context.Background(), "0199d4a2-0000-7000-8000-0000000000ff")
	require.ErrorIs(t, err, entity.ErrNotFound)
}

func TestUpdateStoresAndPushesToOverlayTopic(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, st, b := newUC(t, &now)

	in := entity.DefaultOverlaySettings(channelID)
	in.Chat.Preset = entity.OverlayPresetNeon

	got, err := uc.Update(context.Background(), owner(), in)
	require.NoError(t, err)
	require.NotNil(t, got.UpdatedAt)
	require.Equal(t, entity.OverlayPresetNeon, st.saved[channelID].Chat.Preset)
	require.Equal(t, ownerID, *st.saved[channelID].UpdatedBy)

	require.Len(t, b.sent, 1)
	require.Equal(t, entity.OverlayTopic(channelID), b.sent[0].topic)
	require.Equal(t, entity.EventOverlaySettings, b.sent[0].typ)
}

func TestUpdateRules(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	valid := entity.DefaultOverlaySettings(channelID)
	bad := entity.DefaultOverlaySettings(channelID)
	bad.Chat.FontSize = 99

	tests := []struct {
		name string
		p    entity.Principal
		in   entity.OverlaySettings
		want error
	}{
		{"guest", entity.Principal{}, valid, entity.ErrUnauthenticated},
		{"someone else", entity.Principal{UserID: otherID}, valid, entity.ErrForbidden},
		{"invalid values", owner(), bad, entity.ErrValidation},
		{"admin of another channel's owner", entity.Principal{UserID: otherID, GlobalRole: entity.GlobalRoleAdmin}, valid, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, st, b := newUC(t, &now)

			_, err := uc.Update(context.Background(), tc.p, tc.in)
			if tc.want == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.want)
			require.Empty(t, st.saved, "nothing is stored on a refusal")
			require.Empty(t, b.sent, "nothing is pushed on a refusal")
		})
	}
}

func TestUpdateKeepsSavedWhenPushFails(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, st, b := newUC(t, &now)
	b.err = errDown

	_, err := uc.Update(context.Background(), owner(), entity.DefaultOverlaySettings(channelID))
	require.NoError(t, err, "the options are saved; open pages restyle on their next reload")
	require.Contains(t, st.saved, channelID)
}

func TestUpdateStoreFailureIsNotPushed(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, st, b := newUC(t, &now)
	st.err = errDown

	_, err := uc.Update(context.Background(), owner(), entity.DefaultOverlaySettings(channelID))
	require.ErrorIs(t, err, errDown)
	require.Empty(t, b.sent)
}

func TestTestGiftFillsDemoValuesAndStaysOffChat(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, _, b := newUC(t, &now)

	require.NoError(t, uc.TestGift(context.Background(), owner(), channelID, entity.GiftMeta{}))
	require.Len(t, b.sent, 1)
	require.Equal(t, entity.OverlayTopic(channelID), b.sent[0].topic, "never a chat topic")
	require.Equal(t, entity.EventOverlayTestGift, b.sent[0].typ)

	g, ok := b.sent[0].data.(entity.GiftMeta)
	require.True(t, ok)
	require.Equal(t, "viewer_demo", g.Username)
	require.Equal(t, "Hoa hồng", g.ItemName)
	require.Equal(t, 1, g.Quantity)
	require.Positive(t, g.Total)
	require.NotNil(t, g.Message)
}

func TestTestGiftKeepsWhatTheStreamerChose(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, _, b := newUC(t, &now)
	msg := "cảm ơn!"

	in := entity.GiftMeta{Username: "an", ItemName: "Vương miện", Icon: "crown", Quantity: 3, Total: 300_000, Message: &msg}
	require.NoError(t, uc.TestGift(context.Background(), owner(), channelID, in))

	g, ok := b.sent[0].data.(entity.GiftMeta)
	require.True(t, ok)
	require.Equal(t, "an", g.Username)
	require.Equal(t, "crown", g.Icon)
	require.Equal(t, int64(300_000), g.Total)
	require.Equal(t, "cảm ơn!", *g.Message)
}

func TestTestGiftRules(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	uc, _, b := newUC(t, &now)
	ctx := context.Background()

	require.ErrorIs(t, uc.TestGift(ctx, entity.Principal{}, channelID, entity.GiftMeta{}), entity.ErrUnauthenticated)
	require.ErrorIs(t, uc.TestGift(ctx, entity.Principal{UserID: otherID}, channelID, entity.GiftMeta{}), entity.ErrForbidden)
	require.ErrorIs(t, uc.TestGift(ctx, owner(), "0199d4a2-0000-7000-8000-0000000000ff", entity.GiftMeta{}), entity.ErrNotFound)
	require.Empty(t, b.sent)

	require.NoError(t, uc.TestGift(ctx, owner(), channelID, entity.GiftMeta{}))
	require.ErrorIs(t, uc.TestGift(ctx, owner(), channelID, entity.GiftMeta{}), entity.ErrRateLimited, "two inside a second")

	now = now.Add(1100 * time.Millisecond)
	require.NoError(t, uc.TestGift(ctx, owner(), channelID, entity.GiftMeta{}))
	require.Len(t, b.sent, 2)
}
