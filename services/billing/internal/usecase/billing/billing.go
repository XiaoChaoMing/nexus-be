// Package billing implements the billing use cases: tiers, checkout, payments, subscriptions, the Studio
// monetization area, the admin payout queue and the periodic jobs.
package billing

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const (
	_cancelParallel = 4
	_cancelTimeout  = 3 * time.Second
	_cursorSep      = "|"
)

// Repos are the stores and gateways the use cases need.
type Repos struct {
	Channels repo.ChannelRepo
	Tiers    repo.TierRepo
	Orders   repo.OrderRepo
	Subs     repo.SubRepo
	Ledger   repo.LedgerRepo
	Payouts  repo.PayoutRepo
	Audit    repo.AuditRepo
	Txns     repo.TransactionRepo
	Idem     repo.IdempotencyRepo
	Lock     repo.JobLock
	Sync     repo.SyncLimiter
	Cipher   repo.Cipher
	Snapshot repo.ChannelSnapshot
}

// Settings are the BILLING_* values.
type Settings struct {
	TierPrices        [entity.TierLevels]int64
	FeeBps            int
	PayoutMin         int64
	OrderTTL          time.Duration
	ReconcileAfter    time.Duration
	ReconcileInterval time.Duration
	ReconcileGiveUp   time.Duration
	RenewReminderDays int
	Methods           []entity.PaymentMethod
	WebBaseURL        string
	VerifyViaAPI      bool
	// Location is the business day of reconcile-daily (Asia/Ho_Chi_Minh).
	Location *time.Location
}

// UseCase implements every billing contract.
type UseCase struct {
	r    Repos
	p    provider.PaymentProvider
	s    Settings
	l    logger.Interface
	obs  usecase.Observer
	now  func() time.Time
	rand io.Reader
	fake FakeControl

	sem chan struct{}
	wg  sync.WaitGroup
}

var (
	_ usecase.Catalog       = (*UseCase)(nil)
	_ usecase.Checkout      = (*UseCase)(nil)
	_ usecase.Payments      = (*UseCase)(nil)
	_ usecase.Subscriptions = (*UseCase)(nil)
	_ usecase.Studio        = (*UseCase)(nil)
	_ usecase.Admin         = (*UseCase)(nil)
	_ usecase.Replica       = (*UseCase)(nil)
	_ usecase.Jobs          = (*UseCase)(nil)
	_ usecase.Idempotency   = (*UseCase)(nil)
	_ usecase.DevPSP        = (*UseCase)(nil)
)

// FakeControl is the dev fake provider's payment page.
type FakeControl interface {
	VerifyForm(fields []entity.Field) bool
	Pay(ctx context.Context, in provider.FakePayment) error
	Cancel(ctx context.Context, invoice string) error
	Decline(ctx context.Context, invoice string) error
}

// Option -.
type Option func(*UseCase)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(uc *UseCase) { uc.now = now } }

// WithRand replaces crypto/rand (tests).
func WithRand(r io.Reader) Option { return func(uc *UseCase) { uc.rand = r } }

// WithFake enables the dev payment page.
func WithFake(f FakeControl) Option { return func(uc *UseCase) { uc.fake = f } }

// New -.
func New(r Repos, p provider.PaymentProvider, s Settings, l logger.Interface, obs usecase.Observer, opts ...Option) *UseCase {
	uc := &UseCase{
		r: r, p: p, s: s, l: l, obs: obs, now: time.Now, rand: rand.Reader,
		sem: make(chan struct{}, _cancelParallel),
	}

	for _, o := range opts {
		o(uc)
	}

	if uc.s.Location == nil {
		uc.s.Location = time.UTC
	}

	return uc
}

// Wait blocks until the background provider calls are done (each is bounded by _cancelTimeout).
func (uc *UseCase) Wait() { uc.wg.Wait() }

// cancelAtProvider cancels an unpaid order at the provider after commit (G6-Q5): best effort, bounded, never
// blocking the caller. The call outlives the request (WithoutCancel) but not _cancelTimeout.
func (uc *UseCase) cancelAtProvider(ctx context.Context, invoice string) {
	select {
	case uc.sem <- struct{}{}:
	default:
		uc.l.Warn("billing - cancel %s at provider skipped: too many in flight", invoice)

		return
	}

	uc.wg.Add(1)

	go func() {
		defer uc.wg.Done()
		defer func() { <-uc.sem }()

		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), _cancelTimeout)
		defer cancel()

		if err := uc.p.CancelOrder(ctx, invoice); err != nil && !errors.Is(err, entity.ErrProviderNotFound) {
			uc.l.Warn("billing - cancel %s at provider: %v", invoice, err)
		}
	}()
}

// encodeCursor is base64url("RFC3339Nano|id").
func encodeCursor(at time.Time, id string) *string {
	s := base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + _cursorSep + id))

	return &s
}

func decodeCursor(s string) (*repo.Cursor, error) {
	if s == "" {
		return nil, nil
	}

	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, entity.Invalid("cursor", entity.DetailInvalid)
	}

	at, id, ok := strings.Cut(string(b), _cursorSep)
	t, terr := time.Parse(time.RFC3339Nano, at)

	if !ok || terr != nil || uuid.Validate(id) != nil {
		return nil, entity.Invalid("cursor", entity.DetailInvalid)
	}

	return &repo.Cursor{At: t, ID: id}, nil
}

// next returns the cursor of the page after items when the page is full.
func next[T any](items []T, limit int, key func(T) (time.Time, string)) *string {
	if len(items) < limit || len(items) == 0 {
		return nil
	}

	return encodeCursor(key(items[len(items)-1]))
}

func validID(id string) bool { return uuid.Validate(id) == nil }

func ptr[T any](v T) *T { return &v }
