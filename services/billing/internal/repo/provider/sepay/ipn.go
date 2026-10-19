package sepay

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
)

// HeaderSecret carries the IPN secret when the IPN auth type is SECRET_KEY.
const HeaderSecret = "X-Secret-Key"

// Notification types billing acts on.
const (
	TypeOrderPaid       = "ORDER_PAID"
	TypeTransactionVoid = "TRANSACTION_VOID"
)

var _amountRe = regexp.MustCompile(`^(\d{1,15})(?:\.(\d{1,2}))?$`)

// ParseAmount reads "59000" or "59000.00" (BR-H25); a non-zero fraction is invalid.
func ParseAmount(s string) provider.Amount {
	s = strings.TrimSpace(s)
	if s == "" {
		return provider.Amount{}
	}

	m := _amountRe.FindStringSubmatch(s)
	if len(m) < 3 || strings.Trim(m[2], "0") != "" {
		return provider.Amount{Present: true}
	}

	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return provider.Amount{Present: true}
	}

	return provider.Amount{Value: v, Present: true, Valid: true}
}

// SecretMatches compares in constant time; a length mismatch still does one comparison.
func SecretMatches(got, want string) bool {
	if want == "" {
		return false
	}

	if len(got) != len(want) {
		subtle.ConstantTimeCompare([]byte(want), []byte(want))

		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ipnBody is the part of the IPN billing reads.
type ipnBody struct {
	Timestamp        int64  `json:"timestamp"`
	NotificationType string `json:"notification_type"`
	Order            struct {
		ID            string `json:"id"`
		OrderStatus   string `json:"order_status"`
		Currency      string `json:"order_currency"`
		Amount        any    `json:"order_amount"`
		InvoiceNumber string `json:"order_invoice_number"`
	} `json:"order"`
	Transaction struct {
		ID       string `json:"id"`
		Method   string `json:"payment_method"`
		Status   string `json:"transaction_status"`
		Amount   any    `json:"transaction_amount"`
		Currency string `json:"transaction_currency"`
	} `json:"transaction"`
	Customer struct {
		CustomerID string `json:"customer_id"`
	} `json:"customer"`
}

func decodeIPN(body []byte) (ipnBody, error) {
	var b ipnBody

	if len(body) > entity.WebhookBodyMax {
		return b, fmt.Errorf("%w: body too large", entity.ErrMalformed)
	}

	if err := json.Unmarshal(body, &b); err != nil {
		return b, fmt.Errorf("%w: %w", entity.ErrMalformed, err)
	}

	if b.NotificationType == "" {
		return b, fmt.Errorf("%w: no notification_type", entity.ErrMalformed)
	}

	return b, nil
}

// ParseIPN checks the secret header and parses an IPN body.
func ParseIPN(header http.Header, body []byte, secret string) (provider.Notification, error) {
	if !SecretMatches(header.Get(HeaderSecret), secret) {
		return provider.Notification{}, entity.ErrUnauthorized
	}

	b, err := decodeIPN(body)
	if err != nil {
		return provider.Notification{}, err
	}

	n := provider.Notification{Kind: provider.KindIgnored, Type: b.NotificationType}

	switch b.NotificationType {
	case TypeOrderPaid:
		n.Kind = provider.KindPaid
	case TypeTransactionVoid:
		n.Kind = provider.KindVoid
	default:
		return n, nil
	}

	if b.Order.InvoiceNumber == "" {
		return provider.Notification{}, fmt.Errorf("%w: no order_invoice_number", entity.ErrMalformed)
	}

	masked, err := Mask(body)
	if err != nil {
		return provider.Notification{}, fmt.Errorf("%w: %w", entity.ErrMalformed, err)
	}

	return fill(n, b, masked), nil
}

func fill(n provider.Notification, b ipnBody, masked json.RawMessage) provider.Notification {
	n.Invoice, n.ProviderOrderID, n.TxnID = b.Order.InvoiceNumber, b.Order.ID, b.Transaction.ID
	n.OrderAmount, n.TxnAmount = ParseAmount(text(b.Order.Amount)), ParseAmount(text(b.Transaction.Amount))
	n.Currency = currency(b.Order.Currency, b.Transaction.Currency)
	n.OrderStatus, n.TxnStatus = b.Order.OrderStatus, b.Transaction.Status
	n.Method, n.CustomerID, n.RawMasked = b.Transaction.Method, b.Customer.CustomerID, masked

	if b.Timestamp > 0 {
		n.SentAt = time.Unix(b.Timestamp, 0).UTC()
	}

	return n
}

// currency prefers the order currency; an IPN without one is taken as VND only when the transaction says so.
func currency(order, txn string) string {
	if order != "" {
		return order
	}

	return txn
}

// text renders a JSON string or number field as its text.
func text(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	}

	return fmt.Sprint(v)
}

// Mask drops card details (keeping the brand and the last 4 digits), the card holder, the payer's IP and
// user agent at any depth before a body is stored or logged (BR-H36).
func Mask(body []byte) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}

	maskValue(v)

	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}

	return out, nil
}

func maskValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		maskObject(t)

		for _, child := range t {
			maskValue(child)
		}
	case []any:
		for _, child := range t {
			maskValue(child)
		}
	}
}

func maskObject(m map[string]any) {
	const last = 4

	number, _ := m["card_number"].(string) //nolint:errcheck // absent or not a string: no last4

	for k := range m {
		if k == "ip_address" || k == "user_agent" || (strings.HasPrefix(k, "card_") && k != "card_brand" && k != "card_last4") {
			delete(m, k)
		}
	}

	if digits := strings.Map(keepDigit, number); len(digits) >= last {
		m["card_last4"] = digits[len(digits)-last:]
	}
}

func keepDigit(r rune) rune {
	if r >= '0' && r <= '9' {
		return r
	}

	return -1
}
