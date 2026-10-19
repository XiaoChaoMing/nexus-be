package sepay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
)

const (
	_perPage      = 100
	_maxPages     = 100
	_timeLayout   = "2006-01-02 15:04:05"
	_maxRespBytes = 4 << 20
)

// Observer receives one sample per API call (billing_provider_requests_total / _duration_seconds).
type Observer interface {
	ProviderCall(op, result string, d time.Duration)
}

// Logger is the part of pkg/logger the adapter uses.
type Logger interface {
	Warn(message string, args ...any)
	Error(message any, args ...any)
}

// Config of the adapter.
type Config struct {
	CheckoutURL string
	APIURL      string
	MerchantID  string
	SecretKey   string
	IPNSecret   string
	Timeout     time.Duration
	// Location of transaction_date and the list filters (SePay answers in Vietnam time).
	Location *time.Location
}

// Adapter implements provider.PaymentProvider and provider.DevVoider against SePay.
type Adapter struct {
	cfg  Config
	http *http.Client
	obs  Observer
	l    Logger
}

var (
	_ provider.PaymentProvider = (*Adapter)(nil)
	_ provider.DevVoider       = (*Adapter)(nil)
)

// New -.
func New(cfg Config, obs Observer, l Logger) *Adapter {
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}

	return &Adapter{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}, obs: obs, l: l}
}

// Name -.
func (*Adapter) Name() string { return provider.NameSePay }

// BuildCheckout signs the form (no network call); the result URLs come with the order.
func (a *Adapter) BuildCheckout(_ context.Context, o provider.CheckoutOrder) (entity.Checkout, error) {
	return entity.Checkout{ActionURL: a.cfg.CheckoutURL, Method: http.MethodPost, Fields: SignedFields(a.cfg.SecretKey,
		formInput(a.cfg.MerchantID, o))}, nil
}

// ParseNotification -.
func (a *Adapter) ParseNotification(header http.Header, body []byte) (provider.Notification, error) {
	return ParseIPN(header, body, a.cfg.IPNSecret)
}

// QueryOrder -: GET /v1/order/detail/{invoice}.
func (a *Adapter) QueryOrder(ctx context.Context, invoice string) (provider.OrderState, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}

	raw, err := a.call(ctx, "detail", http.MethodGet, "/v1/order/detail/"+url.PathEscape(invoice), nil, &env)
	if err != nil {
		return provider.OrderState{}, err
	}

	body := raw
	if len(env.Data) > 0 && env.Data[0] == '{' {
		body = env.Data
	}

	var o apiOrder
	if err := json.Unmarshal(body, &o); err != nil {
		return provider.OrderState{}, fmt.Errorf("sepay - detail - decode: %w", err)
	}

	return a.state(o, body), nil
}

// ListOrders -: GET /v1/order?from_created_at&to_created_at&per_page=100&page=n until meta.has_more is false.
func (a *Adapter) ListOrders(ctx context.Context, from, to time.Time) ([]provider.OrderState, error) {
	var out []provider.OrderState

	for page := 1; page <= _maxPages; page++ {
		q := url.Values{
			"from_created_at": {from.In(a.cfg.Location).Format(_timeLayout)},
			"to_created_at":   {to.In(a.cfg.Location).Format(_timeLayout)},
			"per_page":        {strconv.Itoa(_perPage)}, "page": {strconv.Itoa(page)},
		}

		var env struct {
			Data []json.RawMessage `json:"data"`
			Meta struct {
				HasMore bool `json:"has_more"`
			} `json:"meta"`
		}

		if _, err := a.call(ctx, "list", http.MethodGet, "/v1/order?"+q.Encode(), nil, &env); err != nil {
			return nil, err
		}

		for _, raw := range env.Data {
			var o apiOrder
			if err := json.Unmarshal(raw, &o); err != nil {
				return nil, fmt.Errorf("sepay - list - decode: %w", err)
			}

			out = append(out, a.state(o, raw))
		}

		if !env.Meta.HasMore || len(env.Data) == 0 {
			return out, nil
		}
	}

	return out, nil
}

// CancelOrder -: POST /v1/order/cancel.
func (a *Adapter) CancelOrder(ctx context.Context, invoice string) error {
	_, err := a.call(ctx, "cancel", http.MethodPost, "/v1/order/cancel", map[string]string{FieldInvoice: invoice}, nil)

	return err
}

// VoidTransaction -: POST /v1/order/voidTransaction (dev CLI only).
func (a *Adapter) VoidTransaction(ctx context.Context, invoice string) error {
	_, err := a.call(ctx, "void", http.MethodPost, "/v1/order/voidTransaction",
		map[string]string{FieldInvoice: invoice}, nil)

	return err
}

// call sends one request; neither headers nor bodies are logged (they carry credentials and payer data).
func (a *Adapter) call(ctx context.Context, op, method, path string, in, out any) ([]byte, error) {
	start := time.Now()
	result := "error"

	defer func() { a.obs.ProviderCall(op, result, time.Since(start)) }()

	req, err := a.request(ctx, method, path, in)
	if err != nil {
		return nil, fmt.Errorf("sepay - %s: %w", op, err)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		result = "timeout"

		return nil, fmt.Errorf("sepay - %s: %w", op, &entity.ProviderError{})
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, _maxRespBytes))
	if err != nil {
		return nil, fmt.Errorf("sepay - %s - read: %w", op, &entity.ProviderError{})
	}

	if err := a.statusError(op, path, resp.StatusCode); err != nil {
		if errors.Is(err, entity.ErrProviderNotFound) {
			result = "not_found"
		}

		return nil, fmt.Errorf("sepay - %s: %w", op, err)
	}

	result = "ok"

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			result = "error"

			return nil, fmt.Errorf("sepay - %s - decode: %w", op, err)
		}
	}

	return raw, nil
}

func (a *Adapter) request(ctx context.Context, method, path string, in any) (*http.Request, error) {
	var body io.Reader = http.NoBody

	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}

		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(a.cfg.APIURL, "/")+path, body)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}

	req.SetBasicAuth(a.cfg.MerchantID, a.cfg.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return req, nil
}

// statusError maps an HTTP status: 404 → not found; 401/403, 429, 5xx and other errors → unavailable.
func (a *Adapter) statusError(op, path string, code int) error {
	switch {
	case code == http.StatusNotFound:
		return entity.ErrProviderNotFound
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		a.l.Error("sepay - %s %s: credentials rejected (%d)", op, path, code)

		return &entity.ProviderError{Status: code}
	case code >= http.StatusInternalServerError || code == http.StatusTooManyRequests:
		return &entity.ProviderError{Server: true, Status: code}
	case code >= http.StatusMultipleChoices:
		return &entity.ProviderError{Status: code}
	}

	return nil
}

type apiTxn struct {
	ID       string `json:"id"`
	Status   string `json:"transaction_status"`
	Method   string `json:"payment_method"`
	Amount   any    `json:"transaction_amount"`
	Currency string `json:"transaction_currency"`
}

type apiOrder struct {
	ID            string   `json:"id"`
	InvoiceNumber string   `json:"order_invoice_number"`
	OrderStatus   string   `json:"order_status"`
	Amount        any      `json:"order_amount"`
	Currency      string   `json:"order_currency"`
	Transactions  []apiTxn `json:"transactions"`
}

// latest picks the approved transaction if there is one, else the last listed.
func (o apiOrder) latest() *apiTxn {
	var last *apiTxn

	for i := range o.Transactions {
		t := &o.Transactions[i]
		if t.Status == "APPROVED" {
			return t
		}

		last = t
	}

	return last
}

func (a *Adapter) state(o apiOrder, raw []byte) provider.OrderState {
	s := provider.OrderState{
		Invoice: o.InvoiceNumber, ProviderOrderID: o.ID, Amount: ParseAmount(text(o.Amount)), Currency: o.Currency,
	}

	txnStatus := ""
	if t := o.latest(); t != nil {
		s.TxnID, s.Method, txnStatus = t.ID, t.Method, t.Status

		if s.Currency == "" {
			s.Currency = t.Currency
		}
	}

	var known bool

	s.Status, s.Failure, known = MapStatus(o.OrderStatus, txnStatus)
	if !known {
		a.l.Warn("sepay - unknown status order_status=%q transaction_status=%q invoice=%s", o.OrderStatus, txnStatus,
			o.InvoiceNumber)
	}

	if masked, err := Mask(raw); err == nil {
		s.Raw = masked
	}

	return s
}

// MapStatus maps SePay's order and latest transaction status (§6.7); the transaction wins.
func MapStatus(orderStatus, txnStatus string) (provider.OrderStatusKind, entity.FailureReason, bool) {
	switch strings.ToUpper(txnStatus) {
	case "APPROVED":
		return provider.StatusPaid, "", true
	case "DECLINED", "FAILED", "REJECTED":
		return provider.StatusFailed, entity.FailDeclined, true
	}

	switch strings.ToUpper(orderStatus) {
	case "CAPTURED", "COMPLETED", "PAID":
		return provider.StatusPaid, "", true
	case "CANCELLED", "CANCELED":
		return provider.StatusCancelled, "", true
	case "FAILED", "DECLINED", "REJECTED":
		return provider.StatusFailed, entity.FailDeclined, true
	case "PENDING", "CREATED", "PROCESSING", "AUTHORIZED":
		return provider.StatusPending, "", true
	}

	return provider.StatusUnknown, entity.FailUnknown, false
}

func formInput(merchant string, o provider.CheckoutOrder) FormInput {
	method := ""
	if o.Method != nil {
		method = string(*o.Method)
	}

	return FormInput{
		Amount: o.Amount, Merchant: merchant, Description: o.Description, Invoice: o.Invoice, CustomerID: o.UserID,
		Method: method, SuccessURL: o.SuccessURL, ErrorURL: o.ErrorURL, CancelURL: o.CancelURL,
	}
}
