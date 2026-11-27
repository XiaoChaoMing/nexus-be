// Package entity holds the billing domain: channels copied from channel-svc, subscription tiers, checkout
// orders, subscriptions, the append-only ledger and payout requests.
package entity

import (
	"strconv"
	"time"
)

type (
	// OrderStatus of a checkout order.
	OrderStatus string
	// FailureReason of a CANCELLED, FAILED or EXPIRED order.
	FailureReason string
	// SubAction says whether an order starts a subscription or extends a live one (DTO field "kind").
	SubAction string
	// PaymentMethod chosen on the checkout form or reported by the provider.
	PaymentMethod string
	// SubStatus of a subscription.
	SubStatus string
	// LedgerKind of a ledger entry.
	LedgerKind string
	// Direction of a ledger entry.
	Direction string
	// PayoutStatus of a payout request.
	PayoutStatus string
	// TxnType of a provider transaction row.
	TxnType string
	// OrderType selects the applier of a paid order: a subscription or a wallet top-up.
	OrderType string
)

// Order statuses.
const (
	OrderPending   OrderStatus = "PENDING"
	OrderPaid      OrderStatus = "PAID"
	OrderCancelled OrderStatus = "CANCELLED"
	OrderFailed    OrderStatus = "FAILED"
	OrderExpired   OrderStatus = "EXPIRED"
	OrderVoided    OrderStatus = "VOIDED"
)

// Failure reasons.
const (
	FailCancelledByUser FailureReason = "CANCELLED_BY_USER"
	FailDeclined        FailureReason = "DECLINED"
	FailExpired         FailureReason = "EXPIRED"
	FailAmountMismatch  FailureReason = "AMOUNT_MISMATCH"
	FailProviderError   FailureReason = "PROVIDER_ERROR"
	FailUnknown         FailureReason = "UNKNOWN"
)

// Sub actions.
const (
	SubActionNew   SubAction = "NEW"
	SubActionRenew SubAction = "RENEW"
)

// Payment methods.
const (
	MethodCard         PaymentMethod = "CARD"
	MethodBankTransfer PaymentMethod = "BANK_TRANSFER"
	MethodNapas        PaymentMethod = "NAPAS_BANK_TRANSFER"
)

// Subscription statuses.
const (
	SubActive              SubStatus = "ACTIVE"
	SubCancelledPendingEnd SubStatus = "CANCELLED_PENDING_END"
	SubExpired             SubStatus = "EXPIRED"
)

// Ledger kinds and directions.
const (
	LedgerSubRevenue     LedgerKind = "SUB_REVENUE"
	LedgerPlatformFee    LedgerKind = "PLATFORM_FEE"
	LedgerPayoutHold     LedgerKind = "PAYOUT_HOLD"
	LedgerPayoutReversal LedgerKind = "PAYOUT_REVERSAL"
	LedgerGiftRevenue    LedgerKind = "GIFT_REVENUE"

	Credit Direction = "CREDIT"
	Debit  Direction = "DEBIT"
)

// Payout statuses.
const (
	PayoutRequested  PayoutStatus = "REQUESTED"
	PayoutApproved   PayoutStatus = "APPROVED"
	PayoutProcessing PayoutStatus = "PROCESSING"
	PayoutPaid       PayoutStatus = "PAID"
	PayoutRejected   PayoutStatus = "REJECTED"
	PayoutFailed     PayoutStatus = "FAILED"
)

// Open reports whether a payout still holds money that has not left (BR-H39: one open payout per channel).
func (s PayoutStatus) Open() bool {
	return s == PayoutRequested || s == PayoutApproved || s == PayoutProcessing
}

// Reversed reports whether the hold of a payout in this status is given back (BR-H38).
func (s PayoutStatus) Reversed() bool { return s == PayoutRejected || s == PayoutFailed }

// Transaction types and sources.
const (
	TxnCharge TxnType = "CHARGE"
	TxnVoid   TxnType = "VOID"

	SourceIPN       = "IPN"
	SourceReconcile = "RECONCILE"
)

// Order types.
const (
	OrderTypeSubscription OrderType = "SUBSCRIPTION"
	OrderTypeTopup        OrderType = "TOPUP"
)

// Channel statuses.
const (
	ChannelActive    = "ACTIVE"
	ChannelSuspended = "SUSPENDED"
)

// Currency is the only currency billing accepts.
const Currency = "VND"

// Channel is billing's copy of a channel.
type Channel struct {
	ID          string
	OwnerID     string
	Handle      string
	DisplayName string
	AvatarURL   *string
	Status      string
}

// Suspended -.
func (c Channel) Suspended() bool { return c.Status == ChannelSuspended }

// Tier is one of the three subscription tiers of a channel.
type Tier struct {
	ID          string
	ChannelID   string
	Level       int
	Price       int64
	Description string
	Active      bool
	UpdatedAt   time.Time
}

// Name is the display name; it is derived from the level, not stored.
func (t Tier) Name() string { return "Tier " + strconv.Itoa(t.Level) }

// Order is a checkout order with the channel and tier it was made for.
type Order struct {
	ID             string
	InvoiceNumber  string
	UserID         string
	ChannelID      string
	TierID         string
	SubscriptionID *string
	Amount         int64
	PSP            string
	PSPRef         *string
	SubAction      SubAction
	PeriodDays     int
	PaymentMethod  *PaymentMethod
	Status         OrderStatus
	FailureReason  *FailureReason
	NeedsReview    bool
	IdempotencyKey string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	PaidAt         *time.Time
	UpdatedAt      time.Time
	// OrderType is the stored order_type; a top-up has no channel, tier or sub action (zero values).
	OrderType OrderType

	Channel Channel
	Tier    Tier
}

// Type is the order type; rows built before order_type existed are subscriptions.
func (o Order) Type() OrderType {
	if o.OrderType == "" {
		return OrderTypeSubscription
	}

	return o.OrderType
}

// Description is the provider's order description (ASCII, ≤ 100 characters).
func (o Order) Description() string {
	if o.Type() == OrderTypeTopup {
		return "NexusLive Nap vi"
	}

	return OrderDescription(o.Tier.Level, o.Channel.Handle, o.PeriodDays)
}

// Period is the length of the paid period.
func (o Order) Period() time.Duration { return Days(o.PeriodDays) }

// Payable reports whether a payment may still move the order to PAID (BR-H20: late money wins).
func (o Order) Payable() bool {
	switch o.Status {
	case OrderPending, OrderCancelled, OrderExpired:
		return true
	case OrderFailed:
		return o.FailureReason == nil || *o.FailureReason != FailAmountMismatch
	case OrderPaid, OrderVoided:
	}

	return false
}

// PayableStatuses are the statuses MarkPaid / MarkMismatch may leave.
func PayableStatuses() []OrderStatus {
	return []OrderStatus{OrderPending, OrderCancelled, OrderFailed, OrderExpired}
}

// Subscription of a viewer to a channel.
type Subscription struct {
	ID                  string
	UserID              string
	ChannelID           string
	TierID              string
	Status              SubStatus
	CurrentPeriodStart  time.Time
	CurrentPeriodEnd    time.Time
	AutoRenew           bool
	CancelledAt         *time.Time
	ExpiredReason       *string
	ExpiringNotifiedFor *time.Time
	CreatedAt           time.Time

	Channel Channel
	Tier    Tier
}

// Live reports whether the subscription still grants its benefits at now.
func (s Subscription) Live(now time.Time) bool {
	return s.Status != SubExpired && s.CurrentPeriodEnd.After(now)
}

// Renewable reports whether one more period may be bought now (live, tier active, within the prepaid cap).
func (s Subscription) Renewable(now time.Time) bool {
	return s.Live(now) && s.Tier.Active && !s.CurrentPeriodEnd.Add(Days(SubPeriodDays)).After(now.Add(Days(MaxPrepaidDays)))
}

// Transaction is a provider notification or reconciliation result recorded once per (psp_ref, type).
type Transaction struct {
	ID      string
	OrderID string
	Type    TxnType
	Amount  int64
	PSPRef  string
	Source  string
	Raw     []byte
}

// LedgerEntry is an append-only money movement of a channel.
type LedgerEntry struct {
	ID            string
	ChannelID     string
	TransactionID *string
	PayoutID      *string
	GiftSendID    *string
	Direction     Direction
	Kind          LedgerKind
	Amount        int64
	CreatedAt     time.Time

	// Read-side joins.
	Source        string
	InvoiceNumber *string
	PayoutNumber  *int64
}

// Payout is a Creator's payout request; the bank account is stored encrypted.
type Payout struct {
	ID            string
	Number        int64
	ChannelID     string
	Amount        int64
	BankCode      string
	AccountLast4  string
	BankEncrypted []byte
	Status        PayoutStatus
	RejectReason  *string
	ReviewedBy    *string
	ReviewedAt    *time.Time
	CreatedAt     time.Time
	Provider      *string
	ProviderRef   *string
	PaidAt        *time.Time
	SettledBy     *string
	SettledAt     *time.Time
	FailureReason *string
}

// CanMoveTo is the payout state machine: REQUESTED → APPROVED → PROCESSING → PAID, REJECTED from REQUESTED,
// FAILED from APPROVED or PROCESSING. PAID and FAILED may also follow APPROVED directly (a synchronous provider).
func (p Payout) CanMoveTo(to PayoutStatus) bool {
	switch p.Status {
	case PayoutRequested:
		return to == PayoutApproved || to == PayoutRejected
	case PayoutApproved:
		return to == PayoutProcessing || to == PayoutPaid || to == PayoutFailed
	case PayoutProcessing:
		return to == PayoutPaid || to == PayoutFailed
	case PayoutPaid, PayoutRejected, PayoutFailed:
	}

	return false
}

// Code is the human-readable payout code ("PO-0012").
func (p Payout) Code() string { return PayoutCode(p.Number) }

// PayoutCode formats a payout number.
func PayoutCode(n int64) string {
	const width = 4

	s := strconv.FormatInt(n, 10)
	for len(s) < width {
		s = "0" + s
	}

	return "PO-" + s
}

// BankAccount is the decrypted bank account of a payout request.
type BankAccount struct {
	Code   string `json:"bankCode"`
	Number string `json:"accountNumber"`
	Name   string `json:"accountName"`
}

// Totals are ledger sums over a time range.
type Totals struct {
	Gross       int64
	PlatformFee int64
	// Gifts is the gift part of Gross.
	Gifts int64
}

// Summary is the Studio monetization overview.
type Summary struct {
	Range         string
	TZ            string
	From, To      time.Time
	Subscribers   map[int]int
	Gross         int64
	PlatformFee   int64
	Gifts         int64
	Net           int64
	TodayNet      int64
	Balance       int64
	Available     int64
	PendingPayout int64
	FeeBps        int
	PayoutMin     int64
}

// Checkout is the signed form the browser posts to the payment page.
type Checkout struct {
	ActionURL string
	Method    string
	Fields    []Field
}

// Field is one form input; the order of Checkout.Fields is the signing order.
type Field struct {
	Name  string
	Value string
}
