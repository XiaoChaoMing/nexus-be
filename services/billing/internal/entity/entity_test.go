package entity_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestFee(t *testing.T) {
	t.Parallel()

	tests := []struct {
		amount, fee int64
	}{{59000, 2950}, {119000, 5950}, {299000, 14950}, {1, 0}, {19, 0}, {20, 1}, {39, 1}}

	for _, tt := range tests {
		require.Equal(t, tt.fee, entity.Fee(tt.amount, 500), tt.amount)
	}

	require.Equal(t, int64(0), entity.Fee(59000, 0))
	require.Equal(t, int64(59000), entity.Fee(59000, 10000))
}

func TestRenewedEnd(t *testing.T) {
	t.Parallel()

	period := entity.Days(30)
	end := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		paidAt time.Time
		want   time.Time
	}{
		{
			"before the end: appended (AC-H15)", time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC),
		},
		{
			"after the end: from the payment", time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC),
		},
		{"at the end", end, end.Add(period)},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, entity.RenewedEnd(end, tt.paidAt, period), tt.name)
	}
}

func TestNewInvoiceNumber(t *testing.T) {
	t.Parallel()

	inv, err := entity.NewInvoiceNumber(bytes.NewReader([]byte{0, 1, 31, 32, 255, 10, 18, 20, 27, 22}))
	require.NoError(t, err)
	require.Equal(t, "NXL01Z0ZAJMVP", inv)
	require.Len(t, inv, 13)

	for range 50 {
		inv, err := entity.NewInvoiceNumber(strings.NewReader(strings.Repeat("\xa7", 10)))
		require.NoError(t, err)
		require.NotContains(t, inv[3:], "I")
		require.NotContains(t, inv[3:], "L")
		require.NotContains(t, inv[3:], "O")
		require.NotContains(t, inv[3:], "U")
	}

	_, err = entity.NewInvoiceNumber(strings.NewReader("short"))
	require.Error(t, err)

	require.True(t, entity.IsInvoiceNumber("NXL01Z0ZAJMVP"))

	for _, s := range []string{"nxl01z0zajmvp", "NXL01Z0ZAJMV", "NXL01Z0ZAJMVPQ", "NXL01Z0ZAJMVU", "ABC01Z0ZAJMVP", ""} {
		require.False(t, entity.IsInvoiceNumber(s), s)
	}
}

func TestNewPayoutRequest(t *testing.T) {
	t.Parallel()

	amount := func(v int64) *int64 { return &v }

	tests := []struct {
		name   string
		amount *int64
		bank   string
		number string
		holder string
		field  string
		code   string
	}{
		{"ok", amount(100000), "VCB", "0123456789", " nguyen  van   an ", "", ""},
		{"min", amount(99999), "VCB", "0123456789", "AN", "amount", "MIN"},
		{"max", amount(1_000_000_001), "VCB", "0123456789", "AN", "amount", "MAX"},
		{"required", nil, "VCB", "0123456789", "AN", "amount", "REQUIRED"},
		{"bank", amount(100000), "ABC", "0123456789", "AN", "bankCode", "ENUM"},
		{"short number", amount(100000), "VCB", "12345", "AN", "accountNumber", "FORMAT"},
		{"long number", amount(100000), "VCB", strings.Repeat("1", 21), "AN", "accountNumber", "FORMAT"},
		{"accents", amount(100000), "VCB", "0123456789", "Trần An", "accountName", "FORMAT"},
		{"digits in name", amount(100000), "VCB", "0123456789", "AN 2", "accountName", "FORMAT"},
		{"one letter", amount(100000), "VCB", "0123456789", "a", "accountName", "LENGTH"},
		{"too long", amount(100000), "VCB", "0123456789", strings.Repeat("A", 101), "accountName", "LENGTH"},
	}

	for _, tt := range tests {
		req, err := entity.NewPayoutRequest(tt.amount, tt.bank, tt.number, tt.holder, 100000)
		if tt.field == "" {
			require.NoError(t, err, tt.name)
			require.Equal(t, "NGUYEN VAN AN", req.Account.Name)
			require.Equal(t, "6789", req.Account.Last4())

			continue
		}

		require.ErrorIs(t, err, entity.ErrValidation, tt.name)
		require.Equal(t, tt.code, entity.DetailsOf(err)[tt.field], tt.name)
	}
}

func TestTextRules(t *testing.T) {
	t.Parallel()

	_, err := entity.AdminReason(" short ")
	require.Equal(t, "LENGTH", entity.DetailsOf(err)["reason"])

	_, err = entity.AdminReason("   ")
	require.Equal(t, "REQUIRED", entity.DetailsOf(err)["reason"])

	r, err := entity.AdminReason("  ten chars!  ")
	require.NoError(t, err)
	require.Equal(t, "ten chars!", r)

	_, err = entity.TierDescription(strings.Repeat("ạ", 201))
	require.Equal(t, "LENGTH", entity.DetailsOf(err)["description"])

	d, err := entity.TierDescription(strings.Repeat("ạ", 200))
	require.NoError(t, err)
	require.Len(t, []rune(d), 200)

	require.Equal(t, "NexusLive Sub Tier 1 @kaelen 30 ngay", entity.OrderDescription(1, "kaelen", 30))
	require.Equal(t, "NexusLive Sub Tier 2 @ 30 ngay", entity.OrderDescription(2, "đ", 30))
	require.Len(t, entity.OrderDescription(3, strings.Repeat("x", 200), 30), 100)

	require.Equal(t, "PO-0012", entity.PayoutCode(12))
	require.Equal(t, "PO-12345", entity.PayoutCode(12345))
	require.Equal(t, "Vietcombank", entity.BankName("VCB"))
	require.Empty(t, entity.BankName("XXX"))
	require.Len(t, entity.Banks(), 12)
}

func TestOrderAndSubscriptionRules(t *testing.T) {
	t.Parallel()

	mismatch, cancelled := entity.FailAmountMismatch, entity.FailCancelledByUser

	for status, want := range map[entity.OrderStatus]bool{
		entity.OrderPending: true, entity.OrderCancelled: true, entity.OrderExpired: true, entity.OrderPaid: false,
		entity.OrderVoided: false,
	} {
		require.Equal(t, want, entity.Order{Status: status}.Payable(), status)
	}

	require.True(t, entity.Order{Status: entity.OrderFailed, FailureReason: &cancelled}.Payable())
	require.False(t, entity.Order{Status: entity.OrderFailed, FailureReason: &mismatch}.Payable())

	now := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	s := entity.Subscription{Status: entity.SubActive, CurrentPeriodEnd: now.Add(time.Hour), Tier: entity.Tier{Active: true}}
	require.True(t, s.Live(now))
	require.True(t, s.Renewable(now))

	s.CurrentPeriodEnd = now.Add(entity.Days(336))
	require.False(t, s.Renewable(now), "beyond 365 prepaid days")

	s.CurrentPeriodEnd, s.Tier.Active = now.Add(time.Hour), false
	require.False(t, s.Renewable(now), "tier turned off")

	s.Status = entity.SubExpired
	require.False(t, s.Live(now))
	require.Equal(t, "Tier 2", entity.Tier{Level: 2}.Name())
}

func TestPayoutCanMoveTo(t *testing.T) {
	t.Parallel()

	all := []entity.PayoutStatus{
		entity.PayoutRequested, entity.PayoutApproved, entity.PayoutProcessing,
		entity.PayoutPaid, entity.PayoutRejected, entity.PayoutFailed,
	}
	allowed := map[entity.PayoutStatus][]entity.PayoutStatus{
		entity.PayoutRequested:  {entity.PayoutApproved, entity.PayoutRejected},
		entity.PayoutApproved:   {entity.PayoutProcessing, entity.PayoutPaid, entity.PayoutFailed},
		entity.PayoutProcessing: {entity.PayoutPaid, entity.PayoutFailed},
	}

	for _, from := range all {
		for _, to := range all {
			want := slices.Contains(allowed[from], to)
			require.Equal(t, want, entity.Payout{Status: from}.CanMoveTo(to), "%s → %s", from, to)
		}

		require.Equal(t, len(allowed[from]) > 0, from.Open(), "%s open", from)
	}

	require.True(t, entity.PayoutFailed.Reversed())
	require.True(t, entity.PayoutRejected.Reversed())
	require.False(t, entity.PayoutPaid.Reversed())
}

func TestPayoutReference(t *testing.T) {
	t.Parallel()

	r, err := entity.PayoutReference("  FT123  ")
	require.NoError(t, err)
	require.Equal(t, "FT123", r)

	for in, code := range map[string]string{
		"   ": "REQUIRED", "FT1": "FORMAT", "FT 123": "FORMAT", strings.Repeat("A", 65): "FORMAT", "FT123ạ": "FORMAT",
	} {
		_, err := entity.PayoutReference(in)
		require.ErrorIs(t, err, entity.ErrValidation, in)
		require.Equal(t, code, entity.DetailsOf(err)["reference"], in)
	}

	_, err = entity.PayoutReference("a1._/-" + strings.Repeat("Z", 58))
	require.NoError(t, err)
}

func TestGiftRules(t *testing.T) {
	t.Parallel()

	detail := func(err error, field string) string { return entity.DetailsOf(err)[field] }
	i64 := func(v int64) *int64 { return &v }

	_, err := entity.NewTopup(nil, 10000, 5000000)
	require.Equal(t, "REQUIRED", detail(err, "amount"))
	_, err = entity.NewTopup(i64(9999), 10000, 5000000)
	require.Equal(t, "MIN", detail(err, "amount"))
	_, err = entity.NewTopup(i64(5000001), 10000, 5000000)
	require.Equal(t, "MAX", detail(err, "amount"))

	a, err := entity.NewTopup(i64(100000), 10000, 5000000)
	require.NoError(t, err)
	require.Equal(t, int64(100000), a)

	n, err := entity.GiftItemName("  Hoa   hồng ")
	require.NoError(t, err)
	require.Equal(t, "Hoa hồng", n)

	_, err = entity.GiftItemName("   ")
	require.Equal(t, "REQUIRED", detail(err, "name"))
	_, err = entity.GiftItemName(strings.Repeat("ạ", 41))
	require.Equal(t, "LENGTH", detail(err, "name"))

	_, err = entity.GiftItemPrice(i64(999))
	require.Equal(t, "MIN", detail(err, "price"))
	_, err = entity.GiftItemPrice(i64(5000001))
	require.Equal(t, "MAX", detail(err, "price"))
	_, err = entity.GiftItemIcon("unicorn")
	require.Equal(t, "ENUM", detail(err, "icon"))
	_, err = entity.GiftItemIcon("rose")
	require.NoError(t, err)
}

func TestGiftMessageAndQuantity(t *testing.T) {
	t.Parallel()

	detail := func(err error, field string) string { return entity.DetailsOf(err)[field] }
	qty := func(v int64) *int64 { return &v }

	m, err := entity.GiftMessage("   ")
	require.NoError(t, err)
	require.Nil(t, m)
	m, err = entity.GiftMessage("  Chúc stream vui! ")
	require.NoError(t, err)
	require.Equal(t, "Chúc stream vui!", *m)

	_, err = entity.GiftMessage(strings.Repeat("ạ", 201))
	require.Equal(t, "LENGTH", detail(err, "message"))
	_, err = entity.GiftMessage("hi\u0007there")
	require.Equal(t, "FORMAT", detail(err, "message"))

	_, err = entity.GiftQuantity(nil, 1000, 5000000)
	require.Equal(t, "REQUIRED", detail(err, "quantity"))
	_, err = entity.GiftQuantity(qty(0), 1000, 5000000)
	require.Equal(t, "MIN", detail(err, "quantity"))
	_, err = entity.GiftQuantity(qty(100), 1000, 5000000)
	require.Equal(t, "MAX", detail(err, "quantity"))
	_, err = entity.GiftQuantity(qty(11), 500000, 5000000)
	require.Equal(t, "MAX", detail(err, "quantity"), "total above the cap")

	q, err := entity.GiftQuantity(qty(10), 500000, 5000000)
	require.NoError(t, err)
	require.Equal(t, 10, q)

	require.Equal(t, entity.OrderTypeSubscription, entity.Order{}.Type())
	require.Equal(t, entity.OrderTypeTopup, entity.Order{OrderType: entity.OrderTypeTopup}.Type())
}
