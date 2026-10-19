package notify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

var _ usecase.BillingSources = (*UseCase)(nil)

// Kinds of SUBSCRIPTION notifications (payload.kind).
const (
	KindPaymentSucceeded     = "PAYMENT_SUCCEEDED"
	KindPaymentConfirmedLate = "PAYMENT_CONFIRMED_LATE"
	KindPaymentUnderReview   = "PAYMENT_UNDER_REVIEW"
	KindPaymentFailed        = "PAYMENT_FAILED"
	KindPaymentVoided        = "PAYMENT_VOIDED"
	KindSubExpiring          = "SUB_EXPIRING"
	KindSubExpired           = "SUB_EXPIRED"
	KindNewSubscriber        = "NEW_SUBSCRIBER"
)

const (
	_linkPayments     = "/settings/payments"
	_linkMonetization = "/studio/monetization"
	_renewQuery       = "?subscribe=renew"
	_subStatusExpired = "EXPIRED"
	_flagOn           = "1"
	_localeEN         = "en"
	_thousandDigits   = 3
)

func channelLink(handle string) *string { return ptr("/" + handle) }

type paymentPayload struct {
	Kind             string     `json:"kind"`
	OrderID          string     `json:"orderId"`
	InvoiceNumber    string     `json:"invoiceNumber"`
	Channel          channelRef `json:"channel"`
	Tier             int        `json:"tier"`
	SubAction        string     `json:"subAction"`
	Applied          string     `json:"applied"`
	Amount           int64      `json:"amount"`
	CurrentPeriodEnd *time.Time `json:"currentPeriodEnd"`
}

// PaymentSucceeded tells the buyer and queues the mandatory receipt (BR-H27, NE-Q4: verified address only).
func (uc *UseCase) PaymentSucceeded(ctx context.Context, eventID string, e events.PaymentSucceeded) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	review := e.Applied == events.PaymentAppliedNeedsReview
	kind, link := KindPaymentSucceeded, channelLink(ch.Handle)

	switch {
	case review:
		kind, link = KindPaymentUnderReview, ptr(_linkPayments)
	case e.Late:
		kind = KindPaymentConfirmedLate
	}

	payload, err := marshal(paymentPayload{
		Kind: kind, OrderID: e.OrderID, InvoiceNumber: e.InvoiceNumber, Channel: refOf(ch), Tier: e.Tier, SubAction: e.Kind,
		Applied: e.Applied, Amount: e.Amount, CurrentPeriodEnd: utc(e.CurrentPeriodEnd),
	})
	if err != nil {
		return err
	}

	return uc.one(ctx, eventID, e.UserID, spec{
		n:         entity.Notification{Type: entity.TypeSubscription, Payload: payload, Link: link},
		template:  "payment_receipt",
		mandatory: true,
		data: func(r entity.Recipient) map[string]string {
			return map[string]string{
				"Invoice": e.InvoiceNumber, "When": uc.formatTime(e.PaidAt, r), keyChannel: ch.DisplayName,
				"Tier": strconv.Itoa(e.Tier), "SubAction": e.Kind, "Until": uc.formatOptional(e.CurrentPeriodEnd, r),
				"Method": methodName(e.PaymentMethod, r.Locale), "Amount": formatVND(e.Amount, r.Locale), "Review": flag(review),
				keyLink: uc.absolute(channelLink(ch.Handle)),
			}
		},
	})
}

// PaymentFailed tells the buyer that the order was cancelled, failed or expired (in-app only).
func (uc *UseCase) PaymentFailed(ctx context.Context, eventID string, e events.PaymentFailed) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Kind          string     `json:"kind"`
		OrderID       string     `json:"orderId"`
		InvoiceNumber string     `json:"invoiceNumber"`
		Channel       channelRef `json:"channel"`
		Tier          int        `json:"tier"`
		Status        string     `json:"status"`
		FailureReason string     `json:"failureReason"`
	}{KindPaymentFailed, e.OrderID, e.InvoiceNumber, refOf(ch), e.Tier, e.Status, e.FailureReason})
	if err != nil {
		return err
	}

	return uc.one(ctx, eventID, e.UserID, spec{
		n: entity.Notification{Type: entity.TypeSubscription, Payload: payload, Link: ptr(_linkPayments)},
	})
}

// PaymentVoided tells the buyer and queues the mandatory payment_voided e-mail.
func (uc *UseCase) PaymentVoided(ctx context.Context, eventID string, e events.PaymentVoided) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Kind               string     `json:"kind"`
		OrderID            string     `json:"orderId"`
		InvoiceNumber      string     `json:"invoiceNumber"`
		Channel            channelRef `json:"channel"`
		Tier               int        `json:"tier"`
		Amount             int64      `json:"amount"`
		SubscriptionStatus *string    `json:"subscriptionStatus"`
		CurrentPeriodEnd   *time.Time `json:"currentPeriodEnd"`
	}{KindPaymentVoided, e.OrderID, e.InvoiceNumber, refOf(ch), e.Tier, e.Amount, e.SubscriptionStatus, utc(e.CurrentPeriodEnd)})
	if err != nil {
		return err
	}

	ended := e.SubscriptionStatus == nil || *e.SubscriptionStatus == _subStatusExpired
	link := ptr(_linkPayments)

	return uc.one(ctx, eventID, e.UserID, spec{
		n:         entity.Notification{Type: entity.TypeSubscription, Payload: payload, Link: link},
		template:  "payment_voided",
		mandatory: true,
		data: func(r entity.Recipient) map[string]string {
			return map[string]string{
				"Invoice": e.InvoiceNumber, keyChannel: ch.DisplayName, "Tier": strconv.Itoa(e.Tier),
				"Amount": formatVND(e.Amount, r.Locale), "Ended": flag(ended), "Until": uc.formatOptional(e.CurrentPeriodEnd, r),
				keyLink: uc.absolute(link),
			}
		},
	})
}

// SubExpiring reminds the buyer once per period (in-app + e-mail by the settings, with an unsubscribe link).
func (uc *UseCase) SubExpiring(ctx context.Context, eventID string, e events.SubscriptionExpiring) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Kind             string     `json:"kind"`
		SubscriptionID   string     `json:"subscriptionId"`
		Channel          channelRef `json:"channel"`
		Tier             int        `json:"tier"`
		CurrentPeriodEnd time.Time  `json:"currentPeriodEnd"`
		Price            int64      `json:"price"`
		Renewable        bool       `json:"renewable"`
	}{KindSubExpiring, e.SubscriptionID, refOf(ch), e.Tier, e.CurrentPeriodEnd.UTC(), e.Price, e.Renewable})
	if err != nil {
		return err
	}

	link := ptr("/" + ch.Handle + _renewQuery)

	return uc.one(ctx, eventID, e.UserID, spec{
		n:        entity.Notification{Type: entity.TypeSubscription, Payload: payload, Link: link},
		template: "subscription_expiring",
		data: func(r entity.Recipient) map[string]string {
			return map[string]string{
				keyChannel: ch.DisplayName, "Tier": strconv.Itoa(e.Tier), "When": uc.formatTime(e.CurrentPeriodEnd, r),
				"Price": formatVND(e.Price, r.Locale), "Renewable": flag(e.Renewable), keyLink: uc.absolute(link),
			}
		},
	})
}

// SubExpired tells the buyer that the subscription ended (in-app only).
func (uc *UseCase) SubExpired(ctx context.Context, eventID string, e events.SubscriptionExpired) error {
	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	payload, err := marshal(struct {
		Kind           string     `json:"kind"`
		SubscriptionID string     `json:"subscriptionId"`
		Channel        channelRef `json:"channel"`
		Tier           int        `json:"tier"`
		Reason         string     `json:"reason"`
	}{KindSubExpired, e.SubscriptionID, refOf(ch), e.Tier, e.Reason})
	if err != nil {
		return err
	}

	return uc.one(ctx, eventID, e.UserID, spec{
		n: entity.Notification{Type: entity.TypeSubscription, Payload: payload, Link: channelLink(ch.Handle)},
	})
}

// NewSubscriber tells the channel owner, grouped per channel for 60 min (in-app only). A replayed activation
// (republish-entitlements) is not a new subscriber.
func (uc *UseCase) NewSubscriber(ctx context.Context, eventID string, e events.SubscriptionActivated) error {
	if e.Replay {
		return nil
	}

	ch, err := uc.channel(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	if ch.OwnerID == e.UserID {
		return nil
	}

	found, err := uc.r.Recipients.Recipients(ctx, []string{ch.OwnerID, e.UserID})
	if err != nil {
		return fmt.Errorf("notify - new subscriber: %w", err)
	}

	owner, ok := found[ch.OwnerID]
	if !ok {
		uc.l.Warn("notify - NEW_SUBSCRIBER %s: no copy of the owner", eventID)
		uc.obs.Suppressed(entity.TypeSubscription, usecase.SuppressNoCopy)

		return nil
	}

	sub := found[e.UserID] // a subscriber without a copy is shown by id only

	payload, err := marshal(struct {
		Kind           string    `json:"kind"`
		ChannelID      string    `json:"channelId"`
		LastSubscriber personRef `json:"lastSubscriber"`
		Tier           int       `json:"tier"`
	}{KindNewSubscriber, ch.ID, personRef{ID: e.UserID, Username: sub.Username, DisplayName: sub.DisplayName}, e.Tier})
	if err != nil {
		return err
	}

	s := spec{n: entity.Notification{
		Type: entity.TypeSubscription, Payload: payload, Link: ptr(_linkMonetization), GroupKey: ptr("sub-new:" + ch.ID),
	}}

	return uc.deliverSmall(ctx, eventID, s, _followerWindow, []entity.Recipient{owner})
}

// formatOptional renders t in the recipient's zone, "" when nil.
func (uc *UseCase) formatOptional(t *time.Time, r entity.Recipient) string {
	if t == nil {
		return ""
	}

	return uc.formatTime(*t, r)
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	return ptr(t.UTC())
}

func flag(on bool) string {
	if on {
		return _flagOn
	}

	return ""
}

// formatVND renders "59.000 ₫" (vi) or "59,000 ₫" (en).
func formatVND(amount int64, loc string) string {
	sep := "."
	if locale(loc) == _localeEN {
		sep = ","
	}

	sign := ""
	if amount < 0 {
		sign, amount = "-", -amount
	}

	digits := strconv.FormatInt(amount, 10)

	var b strings.Builder

	for i, d := range digits {
		if i > 0 && (len(digits)-i)%_thousandDigits == 0 {
			b.WriteString(sep)
		}

		b.WriteRune(d)
	}

	return sign + b.String() + " ₫"
}

// methodName names the payment method in the recipient's language; unknown or none → "—".
func methodName(method *string, loc string) string {
	if method == nil {
		return "—"
	}

	en := locale(loc) == _localeEN

	switch *method {
	case "CARD":
		return pick(en, "Card", "Thẻ")
	case "BANK_TRANSFER":
		return pick(en, "Bank transfer", "Chuyển khoản ngân hàng")
	case "NAPAS_BANK_TRANSFER":
		return pick(en, "NAPAS bank transfer", "Chuyển khoản NAPAS")
	}

	return "—"
}

func pick(en bool, english, vietnamese string) string {
	if en {
		return english
	}

	return vietnamese
}
