package rooms_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/rooms"
	"github.com/stretchr/testify/require"
)

const (
	buyer   = "0199d4a2-0000-7000-8000-0000000000b1"
	stream2 = "0199d4a2-0000-7000-8000-0000000000ab"
)

type subStore struct {
	repo.AudienceStore
	grants  map[string]entity.SubGrant
	removed []time.Time
}

func (s *subStore) SetSub(_ context.Context, _, userID string, g entity.SubGrant) error {
	s.grants[userID] = g

	return nil
}

func (s *subStore) RemoveSubIfNotNewer(_ context.Context, _, userID string, until time.Time) error {
	s.removed = append(s.removed, until)
	if g, ok := s.grants[userID]; ok && !g.Until.After(until) {
		delete(s.grants, userID)
	}

	return nil
}

type openRooms struct {
	repo.RoomRepo
	streams []string
}

func (o openRooms) OpenRoomsOfChannel(context.Context, string) ([]string, error) {
	return o.streams, nil
}

type claims struct{ seen map[string]bool }

func (c *claims) Claim(_ context.Context, eventID, consumer string) (bool, error) {
	key := consumer + "/" + eventID
	if c.seen[key] {
		return false, nil
	}

	c.seen[key] = true

	return true, nil
}

type users struct{ repo.UserRepo }

func (users) Users(_ context.Context, ids []string) (map[string]entity.User, error) {
	out := map[string]entity.User{}

	for _, id := range ids {
		if id == buyer {
			out[id] = entity.User{ID: id, Username: "an"}
		}
	}

	return out, nil
}

type entFixture struct {
	uc       *rooms.UseCase
	subs     *subStore
	presence *fakePresence
	pub      *pub
}

func newEntFixture(streams ...string) entFixture {
	f := entFixture{
		subs:     &subStore{grants: map[string]entity.SubGrant{}},
		presence: &fakePresence{online: map[string]bool{}},
		pub:      &pub{},
	}
	f.uc = rooms.New(rooms.Repos{
		Rooms: openRooms{streams: streams}, Users: users{}, Audience: f.subs, Presence: f.presence,
		Events: &claims{seen: map[string]bool{}},
	}, f.pub, time.Hour, logger.New("error"))

	return f
}

func TestActivatedAnnouncesOnce(t *testing.T) {
	t.Parallel()

	f := newEntFixture(stream, stream2)
	ctx := context.Background()
	end := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	e := events.SubscriptionActivated{UserID: buyer, ChannelID: channel, Tier: 1, CurrentPeriodEnd: end}

	require.NoError(t, f.uc.Activated(ctx, "ev1", e))
	require.NoError(t, f.uc.Activated(ctx, "ev1", e), "redelivery")
	require.Equal(t, entity.SubGrant{Tier: 1, Until: end}, f.subs.grants[buyer])

	msg := ` system {"type":"SUB_NEW","text":"@an vừa subscribe Tier 1","meta":{"userId":"` + buyer + `","username":"an","tier":1}}`
	require.Equal(t, []string{"chat:" + stream + msg, "chat:" + stream2 + msg}, f.pub.got)

	// republish-entitlements rebuilds the copy without a message.
	e.Tier, e.Replay = 2, true
	require.NoError(t, f.uc.Activated(ctx, "ev2", e))
	require.Equal(t, 2, f.subs.grants[buyer].Tier)
	require.Len(t, f.pub.got, 2)
}

func TestRenewedAndUnknownBuyer(t *testing.T) {
	t.Parallel()

	f := newEntFixture(stream)
	ctx := context.Background()
	end := time.Date(2026, 12, 20, 10, 0, 0, 0, time.UTC)
	other := "0199d4a2-0000-7000-8000-0000000000b2"

	require.NoError(t, f.uc.Renewed(ctx, "ev1", events.SubscriptionRenewed{UserID: other, ChannelID: channel, Tier: 3, CurrentPeriodEnd: end}))
	require.Equal(t, []string{"chat:" + stream + ` system {"type":"SUB_RENEW","text":"Một người xem đã gia hạn Tier 3",` +
		`"meta":{"userId":"` + other + `","username":"","tier":3}}`}, f.pub.got)

	// No open room: the grant is stored, nothing is announced (and the event is not claimed).
	quiet := newEntFixture()
	require.NoError(t, quiet.uc.Renewed(ctx, "ev1", events.SubscriptionRenewed{UserID: buyer, ChannelID: channel, Tier: 1, CurrentPeriodEnd: end}))
	require.Empty(t, quiet.pub.got)
	require.Equal(t, 1, quiet.subs.grants[buyer].Tier)
}

func TestUpdatedAndExpired(t *testing.T) {
	t.Parallel()

	f := newEntFixture(stream)
	ctx := context.Background()
	end := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	reduced := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

	require.NoError(t, f.uc.Updated(ctx, events.SubscriptionUpdated{UserID: buyer, ChannelID: channel, Tier: 1, CurrentPeriodEnd: end}))
	require.NoError(t, f.uc.Updated(ctx, events.SubscriptionUpdated{
		UserID: buyer, ChannelID: channel, Tier: 1, CurrentPeriodEnd: reduced, Change: events.SubChangePeriodReduced,
	}))
	require.Equal(t, reduced, f.subs.grants[buyer].Until)

	// An expired event of an older period leaves the stored grant.
	require.NoError(t, f.uc.Expired(ctx, events.SubscriptionExpired{UserID: buyer, ChannelID: channel, CurrentPeriodEnd: reduced.Add(-time.Hour)}))
	require.Contains(t, f.subs.grants, buyer)

	require.NoError(t, f.uc.Expired(ctx, events.SubscriptionExpired{UserID: buyer, ChannelID: channel, CurrentPeriodEnd: reduced}))
	require.NotContains(t, f.subs.grants, buyer)
	require.Empty(t, f.pub.got)
}

// A void that ends the period sends the shortened end (now): the copy still holds the end before the void.
func TestExpiredByVoid(t *testing.T) {
	t.Parallel()

	f := newEntFixture()
	ctx := context.Background()
	paidAt := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	voidedAt := paidAt.Add(time.Hour)

	require.NoError(t, f.uc.Activated(ctx, "ev1", events.SubscriptionActivated{
		UserID: buyer, ChannelID: channel, Tier: 1, CurrentPeriodEnd: paidAt.Add(entity.SubPeriod),
	}))

	voided := events.SubscriptionExpired{UserID: buyer, ChannelID: channel, Tier: 1, Reason: events.SubExpiredVoided, CurrentPeriodEnd: voidedAt}

	// A newer subscription bought after the void is kept when the void is delivered late.
	newer := voidedAt.Add(time.Minute).Add(entity.SubPeriod)
	f.subs.grants[buyer] = entity.SubGrant{Tier: 2, Until: newer}
	require.NoError(t, f.uc.Expired(ctx, voided))
	require.Equal(t, newer, f.subs.grants[buyer].Until)

	f.subs.grants[buyer] = entity.SubGrant{Tier: 1, Until: paidAt.Add(entity.SubPeriod)}
	require.NoError(t, f.uc.Expired(ctx, voided))
	require.NotContains(t, f.subs.grants, buyer)
}

func TestPaymentUpdatedOnlyOnline(t *testing.T) {
	t.Parallel()

	f := newEntFixture()
	ctx := context.Background()
	data := entity.PaymentUpdate{OrderID: "o1", InvoiceNumber: "NXL1", Status: entity.PaymentPaid}

	require.NoError(t, f.uc.PaymentUpdated(ctx, buyer, data))
	require.Empty(t, f.pub.got)

	f.presence.online[buyer] = true
	require.NoError(t, f.uc.PaymentUpdated(ctx, buyer, data))
	require.Equal(t, []string{"user:" + buyer + ` payment_updated {"orderId":"o1","invoiceNumber":"NXL1","status":"PAID",` +
		`"failureReason":null,"needsReview":false,"subscription":null}`}, f.pub.got)
}

type settingsRepo struct {
	repo.SettingsRepo
	blockLinks bool
}

func (s settingsRepo) Settings(context.Context, string) (entity.ChatSettings, error) {
	return entity.ChatSettings{BlockLinks: s.blockLinks}, nil
}

type terms []string

func (t terms) BlockedTerms(context.Context, string) ([]string, error) { return t, nil }

func TestGiftSentAnnouncesOnceAndScreens(t *testing.T) {
	t.Parallel()

	pb := &pub{}
	uc := rooms.New(rooms.Repos{
		Rooms: openRooms{streams: []string{stream}}, Users: users{}, Events: &claims{seen: map[string]bool{}},
		Settings: settingsRepo{blockLinks: true}, Terms: terms{entity.Normalize("đồ ngốc")},
	}, pb, time.Hour, logger.New("error"))
	ctx := context.Background()
	msg := "Chúc stream vui!"
	e := events.GiftSent{
		GiftSendID: "g1", UserID: buyer, ChannelID: channel, ItemName: "Hoa hồng", Icon: "rose", Quantity: 5,
		Total: 50000, Message: &msg,
	}

	require.NoError(t, uc.GiftSent(ctx, "ev1", e))
	require.NoError(t, uc.GiftSent(ctx, "ev1", e), "redelivery")
	require.Equal(t, []string{"chat:" + stream + ` system {"type":"GIFT","text":"@an tặng 5 × Hoa hồng (50.000 ₫): Chúc stream vui!",` +
		`"meta":{"userId":"` + buyer + `","username":"an","itemName":"Hoa hồng","icon":"rose","quantity":5,"total":50000,` +
		`"message":"Chúc stream vui!"}}`}, pb.got)

	for i, bad := range []string{"Đồ   NGỐC", "xem tại abc.com nhé"} {
		e.Message = &bad
		require.NoError(t, uc.GiftSent(ctx, "ev-bad-"+string(rune('a'+i)), e))
		require.Contains(t, pb.got[len(pb.got)-1], `"text":"@an tặng 5 × Hoa hồng (50.000 ₫)","meta"`)
		require.Contains(t, pb.got[len(pb.got)-1], `"message":null`)
	}
}
