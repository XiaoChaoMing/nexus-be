// Package fake is the dev payment provider: the same signed form and IPN format as SePay, a local
// payment page (/dev/fake-psp) and order states kept in Redis. Refused outside NXL_ENV=dev.
package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
)

const (
	_merchant   = "FAKE"
	_ipnTimeout = 5 * time.Second
	_maxBody    = 1 << 10

	statusPaid      = "PAID"
	statusCancelled = "CANCELLED"
	statusDeclined  = "DECLINED"
)

// Store keeps the fake order states (billing:fakepsp:{invoice}, 7 days).
type Store interface {
	Get(ctx context.Context, invoice string) (map[string]string, error)
	Set(ctx context.Context, invoice string, fields map[string]string) error
	Invoices(ctx context.Context) ([]string, error)
}

// Config of the fake.
type Config struct {
	PSPURL    string
	IPNURL    string
	SecretKey string
	IPNSecret string
}

// Provider implements provider.PaymentProvider.
type Provider struct {
	cfg   Config
	store Store
	http  *http.Client
	now   func() time.Time
}

var _ provider.PaymentProvider = (*Provider)(nil)

// New -.
func New(cfg Config, store Store) *Provider {
	return &Provider{cfg: cfg, store: store, http: &http.Client{Timeout: _ipnTimeout}, now: time.Now}
}

// Name -.
func (*Provider) Name() string { return provider.NameFake }

// BuildCheckout signs the form exactly like SePay; the action is the local fake page.
func (p *Provider) BuildCheckout(_ context.Context, o provider.CheckoutOrder) (entity.Checkout, error) {
	method := ""
	if o.Method != nil {
		method = string(*o.Method)
	}

	fields := sepay.SignedFields(p.cfg.SecretKey, sepay.FormInput{
		Amount: o.Amount, Merchant: _merchant, Description: o.Description, Invoice: o.Invoice, CustomerID: o.UserID,
		Method: method, SuccessURL: o.SuccessURL, ErrorURL: o.ErrorURL, CancelURL: o.CancelURL,
	})

	return entity.Checkout{ActionURL: p.cfg.PSPURL, Method: http.MethodPost, Fields: fields}, nil
}

// ParseNotification uses the SePay parser: the fake page sends SePay-format IPNs.
func (p *Provider) ParseNotification(header http.Header, body []byte) (provider.Notification, error) {
	return sepay.ParseIPN(header, body, p.cfg.IPNSecret)
}

// QueryOrder reads the fake state; an invoice never acted on is unknown.
func (p *Provider) QueryOrder(ctx context.Context, invoice string) (provider.OrderState, error) {
	st, err := p.store.Get(ctx, invoice)
	if err != nil {
		return provider.OrderState{}, fmt.Errorf("fake - query: %w", &entity.ProviderError{})
	}

	if len(st) == 0 {
		return provider.OrderState{}, entity.ErrProviderNotFound
	}

	return toState(invoice, st), nil
}

// ListOrders returns the fake orders acted on in [from, to).
func (p *Provider) ListOrders(ctx context.Context, from, to time.Time) ([]provider.OrderState, error) {
	invoices, err := p.store.Invoices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fake - list: %w", &entity.ProviderError{})
	}

	var out []provider.OrderState

	for _, inv := range invoices {
		st, err := p.store.Get(ctx, inv)
		if err != nil {
			return nil, fmt.Errorf("fake - list: %w", &entity.ProviderError{})
		}

		at, perr := strconv.ParseInt(st["at"], 10, 64)
		if perr != nil {
			continue
		}

		if t := time.Unix(at, 0); !t.Before(from) && t.Before(to) {
			out = append(out, toState(inv, st))
		}
	}

	return out, nil
}

// CancelOrder marks an unpaid fake order cancelled.
func (p *Provider) CancelOrder(ctx context.Context, invoice string) error {
	st, err := p.store.Get(ctx, invoice)
	if err != nil {
		return fmt.Errorf("fake - cancel: %w", err)
	}

	if st["status"] == statusPaid {
		return nil
	}

	return p.set(ctx, invoice, statusCancelled, nil)
}

// VerifyForm checks the signature of a posted checkout form.
func (p *Provider) VerifyForm(fields []entity.Field) bool {
	return sepay.VerifyFields(p.cfg.SecretKey, fields)
}

// Pay records the payment and sends the ORDER_PAID IPN to the billing webhook with the IPN secret.
func (p *Provider) Pay(ctx context.Context, in provider.FakePayment) error {
	now := p.now().UTC()
	txnID, orderID := uuid.NewString(), uuid.NewString()

	if in.Method == "" {
		in.Method = string(entity.MethodBankTransfer)
	}

	if err := p.set(ctx, in.Invoice, statusPaid, map[string]string{
		"txnId": txnID, "orderId": orderID, "amount": strconv.FormatInt(in.Amount, 10), "method": in.Method,
	}); err != nil {
		return err
	}

	body, err := sepay.BuildIPN(sepay.IPNInput{
		Type: sepay.TypeOrderPaid, Invoice: in.Invoice, ProviderOrderID: orderID, TxnID: txnID, Amount: in.Amount,
		Method: in.Method, CustomerID: in.CustomerID, Description: in.Description, At: now,
	})
	if err != nil {
		return fmt.Errorf("fake - pay: %w", err)
	}

	return p.sendIPN(ctx, body)
}

// Cancel marks the order cancelled on the payment page (no IPN, like SePay).
func (p *Provider) Cancel(ctx context.Context, invoice string) error {
	return p.set(ctx, invoice, statusCancelled, nil)
}

// Decline marks the order declined (no IPN).
func (p *Provider) Decline(ctx context.Context, invoice string) error {
	return p.set(ctx, invoice, statusDeclined, nil)
}

func (p *Provider) set(ctx context.Context, invoice, status string, extra map[string]string) error {
	fields := map[string]string{"status": status, "at": strconv.FormatInt(p.now().Unix(), 10)}
	for k, v := range extra {
		fields[k] = v
	}

	if err := p.store.Set(ctx, invoice, fields); err != nil {
		return fmt.Errorf("fake - set %s: %w", status, err)
	}

	return nil
}

// ErrIPNRejected is a non-200 answer of the webhook.
var ErrIPNRejected = errors.New("fake - ipn rejected")

func (p *Provider) sendIPN(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.IPNURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fake - ipn request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sepay.HeaderSecret, p.cfg.IPNSecret)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("fake - ipn: %w", err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, _maxBody)) //nolint:errcheck // drain only

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", ErrIPNRejected, resp.StatusCode)
	}

	return nil
}

func toState(invoice string, st map[string]string) provider.OrderState {
	s := provider.OrderState{Invoice: invoice, Status: provider.StatusPending, Currency: entity.Currency}

	switch st["status"] {
	case statusPaid:
		s.Status, s.ProviderOrderID, s.TxnID, s.Method = provider.StatusPaid, st["orderId"], st["txnId"], st["method"]
		s.Amount = sepay.ParseAmount(st["amount"])
	case statusCancelled:
		s.Status = provider.StatusCancelled
	case statusDeclined:
		s.Status, s.Failure = provider.StatusFailed, entity.FailDeclined
	}

	return s
}
