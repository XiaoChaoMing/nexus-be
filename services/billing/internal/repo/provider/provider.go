// Package provider is the payment provider gateway (ADR-009), adjusted to the SePay Payment Gateway API:
// a signed form for checkout, IPN notifications, order lookup / listing / cancel.
package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
)

// Provider names (checkout_orders.psp).
const (
	NameSePay = "SEPAY"
	NameFake  = "FAKE"
)

// PaymentProvider is implemented by the sepay adapter and the dev fake.
type PaymentProvider interface {
	Name() string
	// BuildCheckout signs the form; no network call.
	BuildCheckout(ctx context.Context, o CheckoutOrder) (entity.Checkout, error)
	// ParseNotification checks X-Secret-Key in constant time (entity.ErrUnauthorized) and parses the body
	// (entity.ErrMalformed).
	ParseNotification(header http.Header, body []byte) (Notification, error)
	// QueryOrder looks an invoice up; entity.ErrProviderNotFound, entity.ErrProviderUnavailable.
	QueryOrder(ctx context.Context, invoice string) (OrderState, error)
	// ListOrders returns every order created in [from, to).
	ListOrders(ctx context.Context, from, to time.Time) ([]OrderState, error)
	// CancelOrder cancels an unpaid order; best effort, callers only log errors.
	CancelOrder(ctx context.Context, invoice string) error
}

// DevVoider voids a captured transaction (sepay only, dev CLI).
type DevVoider interface {
	VoidTransaction(ctx context.Context, invoice string) error
}

// CheckoutOrder is what the form needs.
type CheckoutOrder struct {
	ID          string
	Invoice     string
	UserID      string
	Description string
	Amount      int64
	Method      *entity.PaymentMethod
	SuccessURL  string
	ErrorURL    string
	CancelURL   string
}

// NotificationKind -.
type NotificationKind string

// Notification kinds.
const (
	KindPaid    NotificationKind = "PAID"
	KindVoid    NotificationKind = "VOID"
	KindIgnored NotificationKind = "IGNORED"
)

// Amount is a provider amount string parsed to whole VND (BR-H25).
type Amount struct {
	Value int64
	// Present is false when the field is absent.
	Present bool
	// Valid is false for a malformed value or a non-zero fraction.
	Valid bool
}

// Notification is a parsed IPN.
type Notification struct {
	Kind            NotificationKind
	Type            string
	Invoice         string
	ProviderOrderID string
	TxnID           string
	OrderAmount     Amount
	TxnAmount       Amount
	Currency        string
	OrderStatus     string
	TxnStatus       string
	Method          string
	CustomerID      string
	SentAt          time.Time
	// RawMasked is the body without card details, IP or user agent (BR-H36).
	RawMasked json.RawMessage
}

// Approved reports whether an ORDER_PAID notification carries an approved transaction.
func (n Notification) Approved() bool { return n.TxnStatus == "APPROVED" }

// OrderStatusKind is the provider status mapped to billing's view.
type OrderStatusKind string

// Order status kinds.
const (
	StatusPaid      OrderStatusKind = "PAID"
	StatusPending   OrderStatusKind = "PENDING"
	StatusCancelled OrderStatusKind = "CANCELLED"
	StatusFailed    OrderStatusKind = "FAILED"
	StatusUnknown   OrderStatusKind = "UNKNOWN"
)

// OrderState is the provider's view of an order.
type OrderState struct {
	Invoice         string
	ProviderOrderID string
	Status          OrderStatusKind
	Failure         entity.FailureReason
	Amount          Amount
	Currency        string
	Method          string
	TxnID           string
	Raw             json.RawMessage
}

// PaidOrder is a payment to apply, from an IPN or a lookup.
type PaidOrder struct {
	Invoice         string
	ProviderOrderID string
	TxnID           string
	Method          string
	OrderAmount     Amount
	TxnAmount       Amount
	Currency        string
	Raw             json.RawMessage
}

// VoidedOrder is a voided transaction to apply.
type VoidedOrder struct {
	Invoice string
	TxnID   string
	Raw     json.RawMessage
}

// Paid converts a PAID notification.
func (n Notification) Paid() PaidOrder {
	return PaidOrder{
		Invoice: n.Invoice, ProviderOrderID: n.ProviderOrderID, TxnID: n.TxnID, Method: n.Method,
		OrderAmount: n.OrderAmount, TxnAmount: n.TxnAmount, Currency: n.Currency, Raw: n.RawMasked,
	}
}

// Voided converts a VOID notification.
func (n Notification) Voided() VoidedOrder {
	return VoidedOrder{Invoice: n.Invoice, TxnID: n.TxnID, Raw: n.RawMasked}
}

// Paid converts a PAID lookup.
func (s OrderState) Paid() PaidOrder {
	return PaidOrder{
		Invoice: s.Invoice, ProviderOrderID: s.ProviderOrderID, TxnID: s.TxnID, Method: s.Method,
		OrderAmount: s.Amount, Currency: s.Currency, Raw: s.Raw,
	}
}

// FakePayment describes the order the dev fake payment page pays.
type FakePayment struct {
	Invoice     string
	Amount      int64
	Method      string
	CustomerID  string
	Description string
}

// NamePayoutManual is the manual payout provider (payout_requests.provider).
const NamePayoutManual = "MANUAL"

// PayoutProvider moves the money of an approved payout (ADR-015). Disburse must be idempotent on PayoutID: an
// admin may retry an approval after entity.ErrProviderUnavailable.
type PayoutProvider interface {
	Name() string
	Disburse(ctx context.Context, in PayoutInstruction) (PayoutResult, error)
}

// PayoutInstruction is the transfer to make.
type PayoutInstruction struct {
	PayoutID string
	// Code is the transfer description ("PO-0012").
	Code    string
	Amount  int64
	Account entity.BankAccount
}

// PayoutResult is PROCESSING (settled later), PAID (Ref required) or FAILED (Reason required).
type PayoutResult struct {
	Status entity.PayoutStatus
	Ref    *string
	Reason *string
}
