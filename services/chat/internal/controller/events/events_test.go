package events_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	pkgevents "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/stretchr/testify/require"
)

type rec struct {
	usecase.Rooms
	usecase.Grants
	usecase.Safety
	usecase.Entitlements
	calls   []string
	payment entity.PaymentUpdate
}

func (r *rec) Activated(_ context.Context, _ string, e pkgevents.SubscriptionActivated) error {
	r.calls = append(r.calls, fmt.Sprintf("activated %d replay=%t", e.Tier, e.Replay))

	return nil
}

func (r *rec) Renewed(_ context.Context, _ string, e pkgevents.SubscriptionRenewed) error {
	r.calls = append(r.calls, fmt.Sprintf("renewed %d", e.Tier))

	return nil
}

func (r *rec) Updated(_ context.Context, e pkgevents.SubscriptionUpdated) error {
	r.calls = append(r.calls, "updated "+e.Change)

	return nil
}

func (r *rec) Expired(_ context.Context, e pkgevents.SubscriptionExpired) error {
	r.calls = append(r.calls, "expired "+e.Reason)

	return nil
}

func (r *rec) PaymentUpdated(_ context.Context, _ string, data entity.PaymentUpdate) error {
	r.calls = append(r.calls, "payment "+data.Status)
	r.payment = data

	return nil
}

func (r *rec) StreamStarted(context.Context, pkgevents.StreamStarted) error {
	r.calls = append(r.calls, "started")

	return nil
}

func (r *rec) StreamEnded(context.Context, pkgevents.StreamEnded) error {
	r.calls = append(r.calls, "ended")

	return nil
}

func (r *rec) StreamStatusChanged(_ context.Context, e pkgevents.StreamStatusChanged) error {
	r.calls = append(r.calls, "status "+e.Status)

	return nil
}

func (r *rec) UserRegistered(_ context.Context, _ string, u entity.User) error {
	r.calls = append(r.calls, "user "+u.Username)

	return nil
}

func (r *rec) ChannelCreated(_ context.Context, _ string, c entity.Channel) error {
	r.calls = append(r.calls, "channel "+c.OwnerID)

	return nil
}

func (r *rec) SetNotifyLevel(_ context.Context, _, _, level string) error {
	r.calls = append(r.calls, "bell "+level)

	return nil
}

func (r *rec) Follow(_ context.Context, _, _ string, at time.Time, level string) error {
	r.calls = append(r.calls, "follow "+at.UTC().Format(time.RFC3339)+" "+level)

	return nil
}

func TestHandler(t *testing.T) { //nolint:funlen // one table of event types
	t.Parallel()

	stream, ch, user := uuid.NewString(), uuid.NewString(), uuid.NewString()

	tests := []struct {
		name     string
		typ      string
		payload  string
		wantCall string
		wantSkip bool
	}{
		{name: "started", typ: pkgevents.TypeStreamStarted, payload: `{"streamId":"` + stream + `","channelId":"` + ch + `"}`, wantCall: "started"},
		{name: "started without channel", typ: pkgevents.TypeStreamStarted, payload: `{"streamId":"` + stream + `"}`, wantSkip: true},
		{name: "ended", typ: pkgevents.TypeStreamEnded, payload: `{"streamId":"` + stream + `","endReason":"creator"}`, wantCall: "ended"},
		{name: "interrupted", typ: pkgevents.TypeStreamStatusChanged, payload: `{"streamId":"` + stream + `","status":"INTERRUPTED"}`, wantCall: "status INTERRUPTED"},
		{name: "unknown status", typ: pkgevents.TypeStreamStatusChanged, payload: `{"streamId":"` + stream + `","status":"ENDED"}`, wantSkip: true},
		{name: "user", typ: pkgevents.TypeUserRegistered, payload: `{"userId":"` + user + `","username":"minhnn"}`, wantCall: "user minhnn"},
		{name: "user without name", typ: pkgevents.TypeUserRegistered, payload: `{"userId":"` + user + `"}`, wantSkip: true},
		{name: "channel", typ: pkgevents.TypeChannelCreated, payload: `{"channelId":"` + ch + `","ownerId":"` + user + `"}`, wantCall: "channel " + user},
		{
			name: "follow", typ: pkgevents.TypeFollowCreated,
			payload:  `{"userId":"` + user + `","channelId":"` + ch + `","createdAt":"2026-10-14T10:00:00Z"}`,
			wantCall: "follow 2026-10-14T10:00:00Z ALL",
		},
		{
			name: "follow with bell", typ: pkgevents.TypeFollowCreated,
			payload:  `{"userId":"` + user + `","channelId":"` + ch + `","createdAt":"2026-10-14T10:00:00Z","notifyLevel":"OFF"}`,
			wantCall: "follow 2026-10-14T10:00:00Z OFF",
		},
		{
			name: "bell", typ: pkgevents.TypeFollowUpdated, payload: `{"userId":"` + user + `","channelId":"` + ch + `","notifyLevel":"LIVE_ONLY"}`,
			wantCall: "bell LIVE_ONLY",
		},
		{
			name: "bad bell", typ: pkgevents.TypeFollowUpdated, payload: `{"userId":"` + user + `","channelId":"` + ch + `","notifyLevel":"NONE"}`,
			wantSkip: true,
		},
		{
			name: "channel updated", typ: pkgevents.TypeChannelUpdated, payload: `{"channelId":"` + ch + `","ownerId":"` + user + `","handle":"x"}`,
			wantCall: "channel " + user,
		},
		{
			name: "cancelled", typ: pkgevents.TypeStreamStatusChanged, payload: `{"streamId":"` + stream + `","status":"CANCELLED"}`,
			wantCall: "status CANCELLED",
		},
		{name: "not json", typ: pkgevents.TypeStreamEnded, payload: `{`, wantSkip: true},
		{name: "ignored type", typ: "ingest.health.v1", payload: `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &rec{}
			err := events.Handler(r, r, r, r)(context.Background(), eventbus.Event{ID: uuid.NewString(), Type: tc.typ, Payload: []byte(tc.payload)})

			if tc.wantSkip {
				require.ErrorIs(t, err, eventbus.ErrSkip)
				require.Empty(t, r.calls)

				return
			}

			require.NoError(t, err)

			if tc.wantCall != "" {
				require.Equal(t, []string{tc.wantCall}, r.calls)
			}
		})
	}

	require.Contains(t, events.Types, pkgevents.TypeFollowDeleted)
}

func TestHandlerRejectsBadEventID(t *testing.T) {
	t.Parallel()

	r := &rec{}
	err := events.Handler(r, r, r, r)(context.Background(), eventbus.Event{
		ID: "x", Type: pkgevents.TypeStreamEnded, Payload: []byte(`{"streamId":"` + uuid.NewString() + `"}`),
	})
	require.ErrorIs(t, err, eventbus.ErrSkip)
}

func (r *rec) UserLocked(_ context.Context, e pkgevents.UserLocked) error {
	r.calls = append(r.calls, "locked "+e.UserID)

	return nil
}

func (r *rec) ChannelSuspended(_ context.Context, _ string, e pkgevents.ChannelSuspended) error {
	r.calls = append(r.calls, "suspended "+e.ChannelID)

	return nil
}

func (r *rec) RelayNotification(_ context.Context, e pkgevents.NotificationCreated) error {
	r.calls = append(r.calls, "notification "+e.Type)

	return nil
}

func TestSafetyEvents(t *testing.T) {
	t.Parallel()

	r := &rec{}
	h := events.Handler(r, r, r, r)
	ctx := context.Background()
	user, ch, n := uuid.NewString(), uuid.NewString(), uuid.NewString()

	require.NoError(t, h(ctx, eventbus.Event{ID: uuid.NewString(), Type: pkgevents.TypeUserLocked, Payload: []byte(`{"userId":"` + user + `"}`)}))
	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeChannelSuspended,
		Payload: []byte(`{"channelId":"` + ch + `","ownerId":"` + user + `"}`),
	}))
	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeNotificationCreated,
		Payload: []byte(`{"notificationId":"` + n + `","userId":"` + user + `","type":"CREATOR_LIVE","unreadCount":3}`),
	}))
	require.Equal(t, []string{"locked " + user, "suspended " + ch, "notification CREATOR_LIVE"}, r.calls)

	err := h(ctx, eventbus.Event{ID: uuid.NewString(), Type: pkgevents.TypeUserLocked, Payload: []byte(`{"userId":"x"}`)})
	require.ErrorIs(t, err, eventbus.ErrSkip)
}

func TestBillingEvents(t *testing.T) { //nolint:funlen // one table of event types
	t.Parallel()

	ch, user, order, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ids := `"userId":"` + user + `","channelId":"` + ch + `"`
	pay := `"orderId":"` + order + `","invoiceNumber":"NXL1",` + ids
	end := "2026-11-20T10:00:00Z"

	tests := []struct {
		name        string
		typ         string
		payload     string
		wantCall    string
		wantSkip    bool
		wantPayment *entity.PaymentUpdate
	}{
		{name: "activated", typ: pkgevents.TypeSubscriptionActivated, payload: `{` + ids + `,"tier":1}`, wantCall: "activated 1 replay=false"},
		{name: "activated replay", typ: pkgevents.TypeSubscriptionActivated, payload: `{` + ids + `,"tier":2,"replay":true}`, wantCall: "activated 2 replay=true"},
		{name: "activated tier 4", typ: pkgevents.TypeSubscriptionActivated, payload: `{` + ids + `,"tier":4}`, wantSkip: true},
		{name: "activated bad channel", typ: pkgevents.TypeSubscriptionActivated, payload: `{"userId":"` + user + `","channelId":"x","tier":1}`, wantSkip: true},
		{name: "renewed", typ: pkgevents.TypeSubscriptionRenewed, payload: `{` + ids + `,"tier":3}`, wantCall: "renewed 3"},
		{name: "renewed tier 0", typ: pkgevents.TypeSubscriptionRenewed, payload: `{` + ids + `,"tier":0}`, wantSkip: true},
		{name: "updated", typ: pkgevents.TypeSubscriptionUpdated, payload: `{` + ids + `,"tier":1,"change":"PERIOD_REDUCED"}`, wantCall: "updated PERIOD_REDUCED"},
		{name: "expired", typ: pkgevents.TypeSubscriptionExpired, payload: `{` + ids + `,"tier":1,"reason":"VOIDED"}`, wantCall: "expired VOIDED"},
		{
			name: "succeeded", typ: pkgevents.TypePaymentSucceeded,
			payload:  `{` + pay + `,"tier":1,"applied":"ACTIVATED","subscriptionId":"` + sub + `","currentPeriodEnd":"` + end + `"}`,
			wantCall: "payment PAID",
			wantPayment: &entity.PaymentUpdate{OrderID: order, InvoiceNumber: "NXL1", Status: "PAID", Subscription: &entity.PaymentSubState{
				ID: sub, ChannelID: ch, Tier: 1, Status: "ACTIVE", CurrentPeriodEnd: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
			}},
		},
		{
			name: "succeeded under review", typ: pkgevents.TypePaymentSucceeded,
			payload:     `{` + pay + `,"tier":2,"applied":"NEEDS_REVIEW","subscriptionId":null}`,
			wantCall:    "payment PAID",
			wantPayment: &entity.PaymentUpdate{OrderID: order, InvoiceNumber: "NXL1", Status: "PAID", NeedsReview: true},
		},
		{
			name: "failed", typ: pkgevents.TypePaymentFailed, payload: `{` + pay + `,"tier":1,"status":"CANCELLED","failureReason":"CANCELLED_BY_USER"}`,
			wantCall:    "payment CANCELLED",
			wantPayment: &entity.PaymentUpdate{OrderID: order, InvoiceNumber: "NXL1", Status: "CANCELLED", FailureReason: ptr("CANCELLED_BY_USER")},
		},
		{
			name: "voided", typ: pkgevents.TypePaymentVoided,
			payload:  `{` + pay + `,"tier":1,"subscriptionId":"` + sub + `","subscriptionStatus":"EXPIRED","currentPeriodEnd":"` + end + `"}`,
			wantCall: "payment VOIDED",
			wantPayment: &entity.PaymentUpdate{OrderID: order, InvoiceNumber: "NXL1", Status: "VOIDED", Subscription: &entity.PaymentSubState{
				ID: sub, ChannelID: ch, Tier: 1, Status: "EXPIRED", CurrentPeriodEnd: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
			}},
		},
		{name: "payment bad order", typ: pkgevents.TypePaymentFailed, payload: `{"orderId":"x",` + ids + `}`, wantSkip: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &rec{}
			err := events.Handler(r, r, r, r)(context.Background(), eventbus.Event{ID: uuid.NewString(), Type: tc.typ, Payload: []byte(tc.payload)})

			if tc.wantSkip {
				require.ErrorIs(t, err, eventbus.ErrSkip)
				require.Empty(t, r.calls)

				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{tc.wantCall}, r.calls)

			if tc.wantPayment != nil {
				require.Equal(t, *tc.wantPayment, r.payment)
			}
		})
	}

	require.Contains(t, events.Types, pkgevents.TypePaymentVoided)
}

func ptr[T any](v T) *T { return &v }
