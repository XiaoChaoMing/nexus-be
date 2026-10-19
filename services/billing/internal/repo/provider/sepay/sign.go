// Package sepay is the SePay Payment Gateway adapter: the signed checkout form, IPN parsing and the
// order API (detail, list, cancel) with Basic auth.
package sepay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
)

// FieldInvoice is the form and API field carrying billing's invoice number.
const FieldInvoice = "order_invoice_number"

// signedFieldOrder is the signing order of the checkout form (BR-H24). The browser must post the inputs in
// this same order: the PHP sample signs this fixed list, the Node SDK signs the form order.
func signedFieldOrder() []string {
	return []string{
		"order_amount", "merchant", "currency", "operation", "order_description", FieldInvoice,
		"customer_id", "payment_method", "success_url", "error_url", "cancel_url",
	}
}

// FormInput are the values of one checkout form.
type FormInput struct {
	Amount      int64
	Merchant    string
	Description string
	Invoice     string
	CustomerID  string
	Method      string
	SuccessURL  string
	ErrorURL    string
	CancelURL   string
}

// SignedFields returns the form inputs in signing order, empty values dropped, then "signature".
func SignedFields(secret string, in FormInput) []entity.Field {
	values := map[string]string{
		"order_amount": strconv.FormatInt(in.Amount, 10), "merchant": in.Merchant, "currency": entity.Currency,
		"operation": "PURCHASE", "order_description": in.Description, FieldInvoice: in.Invoice,
		"customer_id": in.CustomerID, "payment_method": in.Method, "success_url": in.SuccessURL,
		"error_url": in.ErrorURL, "cancel_url": in.CancelURL,
	}

	fields := make([]entity.Field, 0, len(values)+1)

	for _, name := range signedFieldOrder() {
		if v := values[name]; v != "" {
			fields = append(fields, entity.Field{Name: name, Value: v})
		}
	}

	return append(fields, entity.Field{Name: "signature", Value: Sign(secret, fields)})
}

// Sign is base64(HMAC-SHA256(secret, "name=value,name=value,…")) over fields in their order; values are
// used verbatim (no URL encoding).
func Sign(secret string, fields []entity.Field) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f.Name+"="+f.Value)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join(parts, ",")))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyFields recomputes the signature of posted form fields (fake PSP); signature must be last.
func VerifyFields(secret string, fields []entity.Field) bool {
	if len(fields) == 0 || fields[len(fields)-1].Name != "signature" {
		return false
	}

	want := Sign(secret, fields[:len(fields)-1])

	return hmac.Equal([]byte(want), []byte(fields[len(fields)-1].Value))
}
