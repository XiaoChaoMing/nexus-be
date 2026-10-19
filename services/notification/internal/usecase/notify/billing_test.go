package notify_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	"github.com/stretchr/testify/require"
)

const (
	orderID = "0199d4a2-0000-7000-8000-000000000040"
	subID   = "0199d4a2-0000-7000-8000-000000000041"
)

func paid() events.PaymentSucceeded {
	method, end := "BANK_TRANSFER", now.Add(30*24*time.Hour)

	return events.PaymentSucceeded{
		OrderID: orderID, InvoiceNumber: "NXL7Q2K9M4X1C", UserID: viewerID, ChannelID: channelID, Tier: 1, Kind: "NEW",
		Amount: 59000, PaymentMethod: &method, PaidAt: now, Applied: events.PaymentAppliedActivated, SubscriptionID: ptr(subID),
		CurrentPeriodEnd: &end, Source: events.PaymentSourceIPN,
	}
}

func ptr[T any](v T) *T { return &v }

func payload(t *testing.T, it repo.Pending) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(it.Notification.Payload, &out))

	return out
}

func TestPaymentSucceeded(t *testing.T) { //nolint:funlen // one table of variants
	t.Parallel()

	tests := []struct {
		name     string
		change   func(e *events.PaymentSucceeded)
		verified bool
		override *entity.Setting
		wantKind string
		wantLink string
		wantMail bool
		review   string
	}{
		{name: "activated", verified: true, wantKind: "PAYMENT_SUCCEEDED", wantLink: "/kenh", wantMail: true},
		{
			name: "late", change: func(e *events.PaymentSucceeded) { e.Late = true }, verified: true,
			wantKind: "PAYMENT_CONFIRMED_LATE", wantLink: "/kenh", wantMail: true,
		},
		{
			name: "under review", change: func(e *events.PaymentSucceeded) {
				e.Applied, e.SubscriptionID, e.CurrentPeriodEnd = events.PaymentAppliedNeedsReview, nil, nil
			},
			verified: true, wantKind: "PAYMENT_UNDER_REVIEW", wantLink: "/settings/payments", wantMail: true, review: "1",
		},
		{
			name: "settings off: the receipt still goes out", verified: true, wantKind: "PAYMENT_SUCCEEDED", wantLink: "/kenh", wantMail: true,
			override: &entity.Setting{Type: entity.TypeSubscription, InApp: true},
		},
		{name: "unverified: in-app only", wantKind: "PAYMENT_SUCCEEDED", wantLink: "/kenh"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			e.store.addUsers(recipient(viewerID, tc.verified))
			e.throttle.today[viewerID] = 50

			if tc.override != nil {
				e.store.overrides[viewerID] = map[entity.Type]entity.Setting{entity.TypeSubscription: *tc.override}
			}

			ev := paid()
			if tc.change != nil {
				tc.change(&ev)
			}

			require.NoError(t, e.uc.PaymentSucceeded(context.Background(), eventID, ev))
			require.NoError(t, e.uc.PaymentSucceeded(context.Background(), eventID, ev), "redelivery")
			require.Len(t, e.store.upserts, 1)

			it := e.store.upserts[0][0]
			require.Equal(t, entity.TypeSubscription, it.Notification.Type)
			require.Equal(t, tc.wantLink, *it.Notification.Link)

			p := payload(t, it)
			require.Equal(t, tc.wantKind, p["kind"])
			require.Equal(t, "NXL7Q2K9M4X1C", p["invoiceNumber"])
			require.Equal(t, "NEW", p["subAction"])
			require.InDelta(t, 59000, p["amount"], 0)
			require.Contains(t, string(it.Notification.Payload), `"handle":"kenh"`)

			if !tc.wantMail {
				require.Nil(t, it.Email)

				return
			}

			require.NotNil(t, it.Email)
			require.Equal(t, "payment_receipt", it.Email.Template)
			require.True(t, it.Email.Mandatory)
			require.NotContains(t, it.Email.Data, "UnsubscribeURL")
			require.Equal(t, "59.000 ₫", it.Email.Data["Amount"])
			require.Equal(t, "Chuyển khoản ngân hàng", it.Email.Data["Method"])
			require.Equal(t, tc.review, it.Email.Data["Review"])
			require.Equal(t, "http://web.test/kenh", it.Email.Data["Link"])
			require.Empty(t, e.throttle.counted, "mandatory e-mails are not counted")
		})
	}
}

func TestPaymentFailedAndVoided(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(viewerID, true))

	ctx := context.Background()

	require.NoError(t, e.uc.PaymentFailed(ctx, eventID, events.PaymentFailed{
		OrderID: orderID, InvoiceNumber: "NXL1", UserID: viewerID, ChannelID: channelID, Tier: 2, Status: "CANCELLED",
		FailureReason: "CANCELLED_BY_USER",
	}))

	failed := e.store.upserts[0][0]
	require.Nil(t, failed.Email, "in-app only")
	require.Equal(t, "/settings/payments", *failed.Notification.Link)
	require.Equal(t, "PAYMENT_FAILED", payload(t, failed)["kind"])
	require.Equal(t, "CANCELLED_BY_USER", payload(t, failed)["failureReason"])

	status := "EXPIRED"
	require.NoError(t, e.uc.PaymentVoided(ctx, otherEvent, events.PaymentVoided{
		OrderID: orderID, InvoiceNumber: "NXL1", UserID: viewerID, ChannelID: channelID, Tier: 1, Amount: 59000,
		SubscriptionID: ptr(subID), SubscriptionStatus: &status, CurrentPeriodEnd: &now,
	}))

	voided := e.store.upserts[1][0]
	require.Equal(t, "PAYMENT_VOIDED", payload(t, voided)["kind"])
	require.Equal(t, "EXPIRED", payload(t, voided)["subscriptionStatus"])
	require.NotNil(t, voided.Email)
	require.Equal(t, "payment_voided", voided.Email.Template)
	require.True(t, voided.Email.Mandatory)
	require.Equal(t, "1", voided.Email.Data["Ended"])
}

func TestSubExpiringFollowsSettings(t *testing.T) {
	t.Parallel()

	ev := events.SubscriptionExpiring{
		SubscriptionID: subID, UserID: viewerID, ChannelID: channelID, Tier: 1, CurrentPeriodEnd: now.Add(72 * time.Hour),
		Price: 119000, Renewable: true,
	}

	e := newEnv()
	e.store.addUsers(recipient(viewerID, true))
	require.NoError(t, e.uc.SubExpiring(context.Background(), eventID, ev))

	it := e.store.upserts[0][0]
	require.Equal(t, "/kenh?subscribe=renew", *it.Notification.Link)
	require.Equal(t, "SUB_EXPIRING", payload(t, it)["kind"])
	require.NotNil(t, it.Email)
	require.False(t, it.Email.Mandatory)
	require.Equal(t, "subscription_expiring", it.Email.Template)
	require.Equal(t, "119.000 ₫", it.Email.Data["Price"])
	require.Equal(t, "1", it.Email.Data["Renewable"])
	require.Contains(t, it.Email.Data["UnsubscribeURL"], "/notifications/unsubscribe?token=")

	// E-mail turned off for SUBSCRIPTION: the reminder stays in-app.
	off := newEnv()
	off.store.addUsers(recipient(viewerID, true))
	off.store.overrides[viewerID] = map[entity.Type]entity.Setting{entity.TypeSubscription: {Type: entity.TypeSubscription, InApp: true}}
	require.NoError(t, off.uc.SubExpiring(context.Background(), eventID, ev))
	require.Nil(t, off.store.upserts[0][0].Email)

	// Expired: in-app only, back to the channel.
	require.NoError(t, e.uc.SubExpired(context.Background(), otherEvent, events.SubscriptionExpired{
		SubscriptionID: subID, UserID: viewerID, ChannelID: channelID, Tier: 1, Reason: events.SubExpiredPeriodEnded,
	}))

	expired := e.store.upserts[1][0]
	require.Nil(t, expired.Email)
	require.Equal(t, "/kenh", *expired.Notification.Link)
	require.Equal(t, "PERIOD_ENDED", payload(t, expired)["reason"])
}

func TestNewSubscriber(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(ownerID, true), recipient(viewerID, true))

	ctx := context.Background()
	ev := events.SubscriptionActivated{SubscriptionID: subID, UserID: viewerID, ChannelID: channelID, Tier: 2, OrderID: ptr(orderID)}

	require.NoError(t, e.uc.NewSubscriber(ctx, eventID, ev))

	it := e.store.upserts[0][0]
	require.Equal(t, ownerID, it.Notification.UserID)
	require.Equal(t, "sub-new:"+channelID, *it.Notification.GroupKey)
	require.Equal(t, time.Hour, it.Window)
	require.Equal(t, "/studio/monetization", *it.Notification.Link)
	require.Nil(t, it.Email)
	require.Equal(t, "NEW_SUBSCRIBER", payload(t, it)["kind"])
	require.Contains(t, string(it.Notification.Payload), `"lastSubscriber":{"id":"`+viewerID+`","username":"u02"`)

	// republish-entitlements replays are not new subscribers.
	ev.Replay = true
	require.NoError(t, e.uc.NewSubscriber(ctx, otherEvent, ev))
	require.Len(t, e.store.upserts, 1)
}

func TestBillingSkipsUnknownChannel(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(viewerID, true))

	ev := paid()
	ev.ChannelID = "0199d4a2-0000-7000-8000-0000000000ff"

	require.ErrorIs(t, e.uc.PaymentSucceeded(context.Background(), eventID, ev), entity.ErrNotFound)
	require.Empty(t, e.store.upserts)
}
