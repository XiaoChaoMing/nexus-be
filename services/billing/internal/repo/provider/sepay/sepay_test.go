package sepay_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	vectorOrder = "0199f0a3-0000-7000-8000-000000000001"
	vectorBase  = "http://localhost:5173/checkout/result?orderId=" + vectorOrder
)

func vectorInput(method string) sepay.FormInput {
	return sepay.FormInput{
		Amount: 59000, Merchant: "MERCHANT_TEST", Description: "NexusLive Sub Tier 1 @kaelen 30 ngay",
		Invoice: "NXL7Q2K9M4X1C", CustomerID: "0199f0a2-7c1e-7b3a-9d4e-1a2b3c4d5e6f", Method: method,
		SuccessURL: vectorBase + "&r=success", ErrorURL: vectorBase + "&r=error", CancelURL: vectorBase + "&r=cancel",
	}
}

// TestSignatureVectors: the TSD §6.7 vectors (computed with openssl dgst -sha256 -hmac … -binary | base64).
func TestSignatureVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, signature string
		fields            int
	}{
		{"BANK_TRANSFER", "btG/4ERe420k0wZr3SdUK8MaqPQM4ZKLG28ReRqoI3I=", 12},
		{"", "wVSTuaopJdijPBfDhCcFeREP7rnBUma5bsMioF4WZlQ=", 11},
	}

	for _, tt := range tests {
		fields := sepay.SignedFields("nxl-test-secret", vectorInput(tt.method))
		require.Len(t, fields, tt.fields)
		require.Equal(t, entity.Field{Name: "signature", Value: tt.signature}, fields[len(fields)-1])
		require.True(t, sepay.VerifyFields("nxl-test-secret", fields))
		require.False(t, sepay.VerifyFields("other", fields))
	}

	fields := sepay.SignedFields("nxl-test-secret", vectorInput("BANK_TRANSFER"))
	names := make([]string, 0, len(fields))

	for _, f := range fields {
		names = append(names, f.Name)
	}

	require.Equal(t, []string{
		"order_amount", "merchant", "currency", "operation", "order_description", "order_invoice_number", "customer_id",
		"payment_method", "success_url", "error_url", "cancel_url", "signature",
	}, names)

	// Reordering the posted fields breaks the signature.
	fields[0], fields[1] = fields[1], fields[0]
	require.False(t, sepay.VerifyFields("nxl-test-secret", fields))
	require.False(t, sepay.VerifyFields("nxl-test-secret", nil))
}

func TestParseAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in             string
		value          int64
		present, valid bool
	}{
		{"59000.00", 59000, true, true},
		{"59000", 59000, true, true},
		{"59000.0", 59000, true, true},
		{"59000.50", 0, true, false},
		{"5,9000", 0, true, false},
		{"", 0, false, false},
		{"-59000", 0, true, false},
		{"1234567890123456", 0, true, false},
		{"59000.001", 0, true, false},
	}

	for _, tt := range tests {
		got := sepay.ParseAmount(tt.in)
		require.Equal(t, provider.Amount{Value: tt.value, Present: tt.present, Valid: tt.valid}, got, tt.in)
	}
}

func TestSecretMatches(t *testing.T) {
	t.Parallel()

	require.True(t, sepay.SecretMatches("s3cret", "s3cret"))
	require.False(t, sepay.SecretMatches("s3creT", "s3cret"))
	require.False(t, sepay.SecretMatches("s3", "s3cret"))
	require.False(t, sepay.SecretMatches("", "s3cret"))
	require.False(t, sepay.SecretMatches("", ""), "no configured secret accepts nothing")
}

const ipnSample = `{"timestamp":1757058220,"notification_type":"ORDER_PAID",
 "order":{"id":"e2c195be","order_id":"NPSETVI","order_status":"CAPTURED","order_currency":"VND","order_amount":"59000.00",
   "order_invoice_number":"NXL7Q2K9M4X1C","ip_address":"1.2.3.4","user_agent":"UA"},
 "transaction":{"id":"384c66dd","payment_method":"CARD","transaction_status":"APPROVED","transaction_amount":"59000",
   "transaction_currency":"VND","card_number":"4111XXXXXXXX1111","card_brand":"VISA","card_holder_name":"NGUYEN AN",
   "card_expiry":"12/30"},
 "customer":{"id":"c1","customer_id":"0199f0a2"}}`

func TestParseIPN(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set(sepay.HeaderSecret, "ipn")

	n, err := sepay.ParseIPN(h, []byte(ipnSample), "ipn")
	require.NoError(t, err)
	require.Equal(t, provider.KindPaid, n.Kind)
	require.Equal(t, "NXL7Q2K9M4X1C", n.Invoice)
	require.Equal(t, "384c66dd", n.TxnID)
	require.Equal(t, "e2c195be", n.ProviderOrderID)
	require.Equal(t, provider.Amount{Value: 59000, Present: true, Valid: true}, n.OrderAmount)
	require.True(t, n.Approved())
	require.Equal(t, time.Unix(1757058220, 0).UTC(), n.SentAt)

	raw := string(n.RawMasked)
	for _, secret := range []string{"4111", "NGUYEN AN", "1.2.3.4", "UA", "12/30"} {
		require.NotContains(t, raw, secret)
	}

	require.Contains(t, raw, `"card_last4":"1111"`)
	require.Contains(t, raw, `"card_brand":"VISA"`)

	_, err = sepay.ParseIPN(http.Header{}, []byte(ipnSample), "ipn")
	require.ErrorIs(t, err, entity.ErrUnauthorized)

	_, err = sepay.ParseIPN(h, []byte(`{`), "ipn")
	require.ErrorIs(t, err, entity.ErrMalformed)

	_, err = sepay.ParseIPN(h, []byte(strings.Repeat(" ", entity.WebhookBodyMax+1)), "ipn")
	require.ErrorIs(t, err, entity.ErrMalformed)

	n, err = sepay.ParseIPN(h, []byte(`{"notification_type":"SOMETHING"}`), "ipn")
	require.NoError(t, err)
	require.Equal(t, provider.KindIgnored, n.Kind)

	n, err = sepay.ParseIPN(h, []byte(`{"notification_type":"TRANSACTION_VOID","order":{"order_invoice_number":"NXL1"},
		"transaction":{"id":"t"}}`), "ipn")
	require.NoError(t, err)
	require.Equal(t, provider.KindVoid, n.Kind)

	// Amounts may come as JSON numbers too.
	n, err = sepay.ParseIPN(h, []byte(`{"notification_type":"ORDER_PAID","order":{"order_invoice_number":"NXL1",
		"order_amount":59000}}`), "ipn")
	require.NoError(t, err)
	require.Equal(t, int64(59000), n.OrderAmount.Value)
	require.False(t, n.TxnAmount.Present)
}

func TestMapStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		order, txn string
		kind       provider.OrderStatusKind
		failure    entity.FailureReason
		known      bool
	}{
		{"CAPTURED", "", provider.StatusPaid, "", true},
		{"COMPLETED", "", provider.StatusPaid, "", true},
		{"PAID", "", provider.StatusPaid, "", true},
		{"PENDING", "APPROVED", provider.StatusPaid, "", true},
		{"CANCELLED", "", provider.StatusCancelled, "", true},
		{"CANCELED", "", provider.StatusCancelled, "", true},
		{"FAILED", "", provider.StatusFailed, entity.FailDeclined, true},
		{"DECLINED", "", provider.StatusFailed, entity.FailDeclined, true},
		{"REJECTED", "", provider.StatusFailed, entity.FailDeclined, true},
		{"CAPTURED", "DECLINED", provider.StatusFailed, entity.FailDeclined, true},
		{"PENDING", "FAILED", provider.StatusFailed, entity.FailDeclined, true},
		{"CREATED", "", provider.StatusPending, "", true},
		{"PROCESSING", "", provider.StatusPending, "", true},
		{"AUTHORIZED", "", provider.StatusPending, "", true},
		{"pending", "", provider.StatusPending, "", true},
		{"WEIRD", "", provider.StatusUnknown, entity.FailUnknown, false},
	}

	for _, tt := range tests {
		kind, failure, known := sepay.MapStatus(tt.order, tt.txn)
		require.Equal(t, tt.kind, kind, tt.order+"/"+tt.txn)
		require.Equal(t, tt.failure, failure, tt.order+"/"+tt.txn)
		require.Equal(t, tt.known, known, tt.order+"/"+tt.txn)
	}
}

type calls struct {
	mu  sync.Mutex
	ops []string
}

func (c *calls) ProviderCall(op, result string, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ops = append(c.ops, op+":"+result)
}

func adapter(t *testing.T, h http.HandlerFunc, timeout time.Duration) (*sepay.Adapter, *calls) {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := &calls{}

	return sepay.New(sepay.Config{
		APIURL: srv.URL, MerchantID: "M", SecretKey: "S", IPNSecret: "I", Timeout: timeout,
		CheckoutURL: "https://pay-sandbox.sepay.vn/v1/checkout/init",
	}, c, logger.New("error")), c
}

func TestQueryOrder(t *testing.T) {
	t.Parallel()

	a, c := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "M" || pass != "S" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		switch r.URL.Path {
		case "/v1/order/detail/NXLPAID":
			_, err := io.WriteString(w, `{"data":{"id":"o1","order_invoice_number":"NXLPAID","order_status":"COMPLETED",
				"order_amount":"59000.00","order_currency":"VND","transactions":[{"id":"t0","transaction_status":"DECLINED"},
				{"id":"t1","transaction_status":"APPROVED","payment_method":"CARD","card_number":"4111111111111111"}]}}`)
			assert.NoError(t, err)
		case "/v1/order/detail/NXLDOWN":
			w.WriteHeader(http.StatusBadGateway)
		case "/v1/order/detail/NXLSLOW":
			time.Sleep(200 * time.Millisecond)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, 100*time.Millisecond)

	ctx := context.Background()

	st, err := a.QueryOrder(ctx, "NXLPAID")
	require.NoError(t, err)
	require.Equal(t, provider.StatusPaid, st.Status)
	require.Equal(t, "t1", st.TxnID)
	require.Equal(t, "CARD", st.Method)
	require.Equal(t, int64(59000), st.Amount.Value)
	require.NotContains(t, string(st.Raw), "4111111111111111")

	_, err = a.QueryOrder(ctx, "NXLNONE")
	require.ErrorIs(t, err, entity.ErrProviderNotFound)

	_, err = a.QueryOrder(ctx, "NXLDOWN")

	var perr *entity.ProviderError
	require.ErrorAs(t, err, &perr)
	require.True(t, perr.Server)

	_, err = a.QueryOrder(ctx, "NXLSLOW")
	require.ErrorIs(t, err, entity.ErrProviderUnavailable)

	bad := sepay.New(sepay.Config{APIURL: "http://127.0.0.1:1", Timeout: time.Second}, c, logger.New("error"))
	_, err = bad.QueryOrder(ctx, "NXLPAID")
	require.ErrorIs(t, err, entity.ErrProviderUnavailable)
	require.Contains(t, c.ops, "detail:ok")
	require.Contains(t, c.ops, "detail:not_found")
}

func TestListAndCancel(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		canceled []string
		pages    []string
	)

	a, _ := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/v1/order":
			pages = append(pages, r.URL.Query().Get("page")+"/"+r.URL.Query().Get("per_page"))
			more := r.URL.Query().Get("page") == "1"
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"order_invoice_number": "NXL" + r.URL.Query().Get("page"), "order_status": "CAPTURED",
					"order_amount": "59000.00",
				}},
				"meta": map[string]any{"has_more": more},
			}))
		case "/v1/order/cancel":
			var body map[string]string
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			canceled = append(canceled, body["order_invoice_number"])
		}
	}, time.Second)

	states, err := a.ListOrders(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	require.NoError(t, err)
	require.Len(t, states, 2)
	require.Equal(t, []string{"1/100", "2/100"}, pages)
	require.Equal(t, provider.StatusPaid, states[1].Status)

	require.NoError(t, a.CancelOrder(context.Background(), "NXLX"))
	require.NoError(t, a.VoidTransaction(context.Background(), "NXLV"))
	require.Equal(t, []string{"NXLX"}, canceled)

	c, err := a.BuildCheckout(context.Background(), provider.CheckoutOrder{Invoice: "NXL1", Amount: 59000, UserID: "u"})
	require.NoError(t, err)
	require.Equal(t, "https://pay-sandbox.sepay.vn/v1/checkout/init", c.ActionURL)
	require.Equal(t, provider.NameSePay, a.Name())
}

func TestBuildIPNRoundTrip(t *testing.T) {
	t.Parallel()

	body, err := sepay.BuildIPN(sepay.IPNInput{
		Type: sepay.TypeOrderPaid, Invoice: "NXL1", TxnID: "t", Amount: 59000,
		At: time.Unix(1, 0),
	})
	require.NoError(t, err)

	h := http.Header{}
	h.Set(sepay.HeaderSecret, "s")

	n, err := sepay.ParseIPN(h, body, "s")
	require.NoError(t, err)
	require.True(t, n.Approved())
	require.Equal(t, int64(59000), n.TxnAmount.Value)

	void, err := sepay.WithType(n.RawMasked, sepay.TypeTransactionVoid, time.Unix(2, 0))
	require.NoError(t, err)

	n, err = sepay.ParseIPN(h, void, "s")
	require.NoError(t, err)
	require.Equal(t, provider.KindVoid, n.Kind)
	require.Equal(t, "t", n.TxnID)
}
