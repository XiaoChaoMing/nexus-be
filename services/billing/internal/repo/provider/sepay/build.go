package sepay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// IPNInput are the values of a synthesized IPN (fake PSP, `billing dev-ipn synth`).
type IPNInput struct {
	Type            string
	Invoice         string
	ProviderOrderID string
	TxnID           string
	Amount          int64
	Method          string
	CustomerID      string
	Description     string
	At              time.Time
}

// BuildIPN renders an IPN body in SePay's format (§5.4).
func BuildIPN(in IPNInput) ([]byte, error) {
	status, txnType := "CAPTURED", "PAYMENT"
	if in.Type == TypeTransactionVoid {
		status, txnType = "VOIDED", "VOID"
	}

	body := map[string]any{
		"timestamp":         in.At.Unix(),
		"notification_type": in.Type,
		"order": map[string]any{
			"id": in.ProviderOrderID, "order_id": in.ProviderOrderID, "order_status": status, "order_currency": "VND",
			"order_amount": strconv.FormatInt(in.Amount, 10) + ".00", FieldInvoice: in.Invoice,
			"order_description": in.Description,
		},
		"transaction": map[string]any{
			"id": in.TxnID, "payment_method": in.Method, "transaction_id": in.TxnID, "transaction_type": txnType,
			"transaction_date": in.At.Format(_timeLayout), "transaction_status": "APPROVED",
			"transaction_amount": strconv.FormatInt(in.Amount, 10), "transaction_currency": "VND",
		},
		"customer": map[string]any{"id": in.CustomerID, "customer_id": in.CustomerID},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("sepay - build ipn: %w", err)
	}

	return b, nil
}

// WithType returns a stored IPN body with another notification_type (dev-ipn synth TRANSACTION_VOID).
func WithType(raw []byte, typ string, at time.Time) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("sepay - with type: %w", err)
	}

	m["notification_type"] = typ
	m["timestamp"] = at.Unix()

	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("sepay - with type: %w", err)
	}

	return b, nil
}
