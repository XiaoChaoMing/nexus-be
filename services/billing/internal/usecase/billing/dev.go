package billing

import (
	"context"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
)

// Begin -.
func (uc *UseCase) Begin(ctx context.Context, k repo.IdemKey, hash []byte) (repo.IdemState, error) {
	st, err := uc.r.Idem.Begin(ctx, k, hash, entity.IdempotencyStale)
	if err != nil {
		return st, fmt.Errorf("billing - idempotency: %w", err)
	}

	switch st.Outcome {
	case repo.IdemNew:
		uc.obs.Idempotency("new")
	case repo.IdemDone:
		uc.obs.Idempotency("replay")
	case repo.IdemMismatch:
		uc.obs.Idempotency("reused")
	case repo.IdemInProgress:
		uc.obs.Idempotency("in_progress")
	}

	return st, nil
}

// Complete -.
func (uc *UseCase) Complete(ctx context.Context, k repo.IdemKey, status int, body []byte) error {
	if err := uc.r.Idem.Complete(ctx, k, status, body); err != nil {
		return fmt.Errorf("billing - idempotency: %w", err)
	}

	return nil
}

// Abort -.
func (uc *UseCase) Abort(ctx context.Context, k repo.IdemKey) error {
	if err := uc.r.Idem.Abort(ctx, k); err != nil {
		return fmt.Errorf("billing - idempotency: %w", err)
	}

	return nil
}

func (uc *UseCase) fakeOrder(ctx context.Context, invoice string) (entity.Order, error) {
	if uc.fake == nil {
		return entity.Order{}, entity.ErrNotFound
	}

	o, err := uc.r.Orders.ByInvoice(ctx, invoice)
	if err != nil {
		return o, fmt.Errorf("billing - fake psp: %w", err)
	}

	return o, nil
}

// FakeOpen checks the posted checkout form and returns its order.
func (uc *UseCase) FakeOpen(ctx context.Context, fields []entity.Field) (entity.Order, error) {
	if uc.fake == nil {
		return entity.Order{}, entity.ErrNotFound
	}

	if !uc.fake.VerifyForm(fields) {
		return entity.Order{}, entity.Invalid("signature", entity.DetailInvalid)
	}

	for _, f := range fields {
		if f.Name == "order_invoice_number" {
			return uc.fakeOrder(ctx, f.Value)
		}
	}

	return entity.Order{}, entity.Invalid("order_invoice_number", entity.DetailRequired)
}

// FakePay pays the order on the fake page: an ORDER_PAID IPN goes to the webhook, then the browser returns.
func (uc *UseCase) FakePay(ctx context.Context, invoice string) (string, error) {
	o, err := uc.fakeOrder(ctx, invoice)
	if err != nil {
		return "", err
	}

	method := ""
	if o.PaymentMethod != nil {
		method = string(*o.PaymentMethod)
	}

	if err := uc.fake.Pay(ctx, provider.FakePayment{
		Invoice: o.InvoiceNumber, Amount: o.Amount, Method: method, CustomerID: o.UserID,
		Description: o.Description(),
	}); err != nil {
		return "", fmt.Errorf("billing - FakePay: %w", err)
	}

	return uc.resultURL(o.ID, "success"), nil
}

// FakeCancel -: no IPN, like the provider's cancel button.
func (uc *UseCase) FakeCancel(ctx context.Context, invoice string) (string, error) {
	o, err := uc.fakeOrder(ctx, invoice)
	if err != nil {
		return "", err
	}

	if err := uc.fake.Cancel(ctx, invoice); err != nil {
		return "", fmt.Errorf("billing - FakeCancel: %w", err)
	}

	return uc.resultURL(o.ID, "cancel"), nil
}

// FakeFail -: a declined payment, no IPN.
func (uc *UseCase) FakeFail(ctx context.Context, invoice string) (string, error) {
	o, err := uc.fakeOrder(ctx, invoice)
	if err != nil {
		return "", err
	}

	if err := uc.fake.Decline(ctx, invoice); err != nil {
		return "", fmt.Errorf("billing - FakeFail: %w", err)
	}

	return uc.resultURL(o.ID, "error"), nil
}
