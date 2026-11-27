package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

func topup(t *testing.T, e env, user string, amount int64) entity.Order {
	t.Helper()

	o, _, created, err := e.uc.CreateTopup(context.Background(), user, usecase.TopupInput{Amount: &amount, IdempotencyKey: "k"})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, entity.OrderTypeTopup, o.Type())

	return o
}

func TestTopup(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()

	for _, a := range []*int64{nil, ptr(int64(9999)), ptr(int64(5000001))} {
		bad, _, _, err := e.uc.CreateTopup(ctx, viewerID, usecase.TopupInput{Amount: a})
		require.ErrorIs(t, err, entity.ErrValidation)
		require.Empty(t, bad.ID)
	}

	o := topup(t, e, viewerID, 100000)
	require.Empty(t, o.ChannelID)
	require.Empty(t, o.TierID)

	// The same amount again is the pending order; another amount is ORDER_PENDING.
	again, _, created, err := e.uc.CreateTopup(ctx, viewerID, usecase.TopupInput{Amount: ptr(int64(100000))})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, o.ID, again.ID)

	other, _, created, err := e.uc.CreateTopup(ctx, viewerID, usecase.TopupInput{Amount: ptr(int64(200000))})
	require.Equal(t, entity.ReasonOrderPending, entity.DetailsOf(err)["reason"])
	require.False(t, created)
	require.Empty(t, other.ID)

	// A pending top-up does not block a subscription checkout.
	create(t, e, viewerID, tier1ID)

	// Paid → wallet credited once (the IPN twice), no channel ledger, payment.succeeded says TOPUP.
	for range 2 {
		_, err = e.uc.Notify(ctx, header(ipnSecret), ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t-"+o.ID, 100000, nil))
		require.NoError(t, err)
	}

	bal, err := e.uc.WalletBalance(ctx, viewerID)
	require.NoError(t, err)
	require.Equal(t, int64(100000), bal)
	require.Empty(t, e.m.ledger)

	var paid *events.PaymentSucceeded

	for _, ob := range e.m.outbox {
		if p, ok := ob.Payload.(events.PaymentSucceeded); ok {
			paid = &p
		}
	}

	require.NotNil(t, paid)
	require.Equal(t, events.OrderTypeTopup, paid.OrderType)
	require.Equal(t, events.PaymentAppliedTopup, paid.Applied)

	// Void → wallet debited.
	_, err = e.uc.Notify(ctx, header(ipnSecret), ipn(t, sepay.TypeTransactionVoid, o.InvoiceNumber, "t", 100000, nil))
	require.NoError(t, err)

	bal, err = e.uc.WalletBalance(ctx, viewerID)
	require.NoError(t, err)
	require.Zero(t, bal)
}

func TestSendGift(t *testing.T) { //nolint:funlen // one story
	t.Parallel()

	e := newEnv()
	ctx := context.Background()

	rose, err := e.uc.CreateGiftItem(ctx, adminID, usecase.GiftItemInput{
		Name: ptr(" Hoa  hồng "), Price: ptr(int64(10000)), Icon: ptr("rose"),
	})
	require.NoError(t, err)
	require.Equal(t, "Hoa hồng", rose.Name)
	require.True(t, rose.Active)

	for _, in := range []usecase.GiftItemInput{
		{Price: ptr(int64(10000)), Icon: ptr("rose")},
		{Name: ptr("X"), Icon: ptr("rose")},
		{Name: ptr("X"), Price: ptr(int64(10000)), Icon: ptr("unicorn")},
		{Name: ptr("X"), Price: ptr(int64(10000)), Icon: ptr("rose"), SortOrder: ptr(-1)},
	} {
		_, err := e.uc.CreateGiftItem(ctx, adminID, in)
		require.ErrorIs(t, err, entity.ErrValidation)
	}

	o := topup(t, e, viewerID, 100000)
	_, err = e.uc.Notify(ctx, header(ipnSecret), ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t-"+o.ID, 100000, nil))
	require.NoError(t, err)

	give := func(user, channel string, qty int64, msg string) (entity.GiftSend, int64, error) {
		return e.uc.SendGift(ctx, user, channel, usecase.GiftInput{ItemID: rose.ID, Quantity: &qty, Message: msg})
	}

	_, _, err = give(ownerID, channelID, 1, "")
	require.ErrorIs(t, err, entity.ErrGiftSelf)

	for _, q := range []int64{0, 100} {
		_, _, err = give(viewerID, channelID, q, "")
		require.ErrorIs(t, err, entity.ErrValidation)
	}

	_, _, err = give(viewerID, channelID, 11, "")
	require.Equal(t, entity.ReasonInsufficientFunds, entity.DetailsOf(err)["reason"])
	require.Equal(t, "100000", entity.DetailsOf(err)["balance"])

	g, bal, err := give(viewerID, channelID, 5, "  Chúc stream vui! ")
	require.NoError(t, err)
	require.Equal(t, int64(50000), g.Total)
	require.Equal(t, int64(2500), g.Fee)
	require.Equal(t, "Chúc stream vui!", *g.Message)
	require.Equal(t, int64(50000), bal)
	require.Equal(t, 1, e.m.countEvents(events.TypeGiftSent))

	sum, err := e.uc.Summary(ctx, ownerID, "7d", time.UTC)
	require.NoError(t, err)
	require.Equal(t, int64(50000), sum.Gifts)
	require.Equal(t, int64(47500), sum.Available)

	// A price change does not touch the gift already given; a disabled item cannot be given.
	_, err = e.uc.UpdateGiftItem(ctx, adminID, rose.ID, usecase.GiftItemInput{Price: ptr(int64(20000)), Active: ptr(false)})
	require.NoError(t, err)
	require.Equal(t, int64(10000), e.m.gifts[0].UnitPrice)

	_, _, err = give(viewerID, channelID, 1, "")
	require.Equal(t, entity.ReasonGiftInactive, entity.DetailsOf(err)["reason"])

	items, err := e.uc.GiftItems(ctx)
	require.NoError(t, err)
	require.Empty(t, items)

	_, err = e.uc.UpdateGiftItem(ctx, adminID, missingID, usecase.GiftItemInput{Active: ptr(true)})
	require.ErrorIs(t, err, entity.ErrNotFound)
}
