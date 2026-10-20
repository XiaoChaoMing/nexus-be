// Package usecase declares the billing contracts the controllers call.
package usecase

import (
	"context"
	"net/http"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

type (
	// CheckoutConfig is GET /v1/checkout/config.
	CheckoutConfig struct {
		Provider          string
		PaymentMethods    []entity.PaymentMethod
		PeriodDays        int
		RenewReminderDays int
		MaxPrepaidDays    int
		OrderTTL          time.Duration
		MaxPending        int
	}

	// Catalog answers the public tier pages.
	Catalog interface {
		// PublicTiers lists the active tiers of a channel (ErrNotFound, ErrChannelSuspended).
		PublicTiers(ctx context.Context, channelID string) ([]entity.Tier, error)
		// TierContext returns the channel of a tier and its tiers for /checkout?tierId= (the tier itself even
		// when inactive).
		TierContext(ctx context.Context, tierID string) (entity.Channel, []entity.Tier, error)
		Config() CheckoutConfig
	}

	// CreateOrderInput -. PaymentMethod nil = choose on the provider page.
	CreateOrderInput struct {
		TierID         string
		PaymentMethod  *string
		IdempotencyKey string
	}

	// OrderView is an order with the subscription it created (or the caller's current one when in review).
	OrderView struct {
		Order        entity.Order
		Subscription *entity.Subscription
	}

	// Outcome is what the provider redirect said (sync).
	Outcome string

	// Checkout -.
	Checkout interface {
		// CreateOrder returns created = false for the caller's pending order of the same tier (200, AF-H02).
		CreateOrder(ctx context.Context, userID string, in CreateOrderInput) (entity.Order, entity.Checkout, bool, error)
		GetOrder(ctx context.Context, userID, orderID string) (OrderView, error)
		SyncOrder(ctx context.Context, userID, orderID string, outcome Outcome) (OrderView, error)
		CancelOrder(ctx context.Context, userID, orderID string) (entity.Order, error)
	}

	// IPNResult is the label of billing_ipn_total{result}.
	IPNResult string

	// Payments handles provider notifications (webhook).
	Payments interface {
		// Notify parses and applies one notification; entity.ErrUnauthorized → 401, entity.ErrMalformed → 400,
		// any other error → 500.
		Notify(ctx context.Context, header http.Header, body []byte) (IPNResult, error)
	}

	// SubQuery selects the caller's subscriptions: Status "current" (default), "expired", or "" with ChannelID
	// for the one row of a channel.
	SubQuery struct {
		Status    string
		ChannelID *string
	}

	// SubPage -.
	SubPage struct {
		Items []entity.Subscription
		Next  *string
	}

	// OrderPage -.
	OrderPage struct {
		Items []entity.Order
		Next  *string
	}

	// Subscriptions is the viewer's side.
	Subscriptions interface {
		MySubscriptions(ctx context.Context, userID string, q SubQuery, cursor string, limit int) (SubPage, error)
		CancelSubscription(ctx context.Context, userID, subID string) (entity.Subscription, error)
		ResumeSubscription(ctx context.Context, userID, subID string) (entity.Subscription, error)
		MyPayments(ctx context.Context, userID string, statuses []string, cursor string, limit int) (OrderPage, error)
	}

	// StudioTier is a tier with its live subscriber count.
	StudioTier struct {
		Tier              entity.Tier
		ActiveSubscribers int
	}

	// TierPatch -.
	TierPatch struct {
		Active      *bool
		Description *string
	}

	// PayoutInput is the Studio payout form; Amount nil = missing or not an integer.
	PayoutInput struct {
		Amount        *int64
		BankCode      string
		AccountNumber string
		AccountName   string
	}

	// PayoutView is a payout with the decrypted account name (nil when it cannot be decrypted).
	PayoutView struct {
		Payout      entity.Payout
		AccountName *string
	}

	// LedgerPage -.
	LedgerPage struct {
		Items []entity.LedgerEntry
		Next  *string
	}

	// PayoutPage -.
	PayoutPage struct {
		Items []PayoutView
		Next  *string
	}

	// Studio is the Creator's monetization area.
	Studio interface {
		StudioTiers(ctx context.Context, ownerID string) ([]StudioTier, error)
		UpdateTier(ctx context.Context, ownerID string, level int, in TierPatch) (StudioTier, error)
		Summary(ctx context.Context, ownerID, rng string, loc *time.Location) (entity.Summary, error)
		Ledger(ctx context.Context, ownerID, cursor string, limit int) (LedgerPage, error)
		StudioPayouts(ctx context.Context, ownerID, cursor string, limit int) (PayoutPage, error)
		RequestPayout(ctx context.Context, ownerID string, in PayoutInput) (PayoutView, error)
	}

	// AdminPayout is a payout with its channel and the channel's balance.
	AdminPayout struct {
		PayoutView

		Channel   entity.Channel
		Balance   int64
		Available int64
	}

	// AdminPayoutPage -.
	AdminPayoutPage struct {
		Items []AdminPayout
		Next  *string
	}

	// AdminOrderQuery -.
	AdminOrderQuery struct {
		NeedsReview    *bool
		Statuses       []string
		FailureReasons []string
		Invoice        string
	}

	// Admin is the payout queue and the order review page.
	Admin interface {
		// AdminPayouts lists by status ("" = REQUESTED, "ALL").
		AdminPayouts(ctx context.Context, status, cursor string, limit int) (AdminPayoutPage, error)
		PendingCount(ctx context.Context) (int, error)
		AdminPayout(ctx context.Context, id string) (AdminPayout, error)
		// BankAccount decrypts the account and audits PAYOUT_BANK_INFO_VIEWED.
		BankAccount(ctx context.Context, adminID, id string) (entity.BankAccount, error)
		Approve(ctx context.Context, adminID, id string) (AdminPayout, error)
		Reject(ctx context.Context, adminID, id, reason string) (AdminPayout, error)
		AdminOrders(ctx context.Context, q AdminOrderQuery, cursor string, limit int) (OrderPage, error)
		// AdminOrder is one order by id or invoice number (case-insensitive).
		AdminOrder(ctx context.Context, ref string) (entity.Order, error)
	}

	// Replica keeps the channel copy (durable billing-svc and backfill).
	Replica interface {
		ChannelCreated(ctx context.Context, eventID string, at time.Time, e events.ChannelCreated) error
		ChannelUpdated(ctx context.Context, eventID string, at time.Time, e events.ChannelUpdated) error
		ChannelStatus(ctx context.Context, eventID string, at time.Time, channelID string, suspended bool) error
		Backfill(ctx context.Context) (int, error)
	}

	// ReconcileReport counts the outcomes of one reconcile-daily run.
	ReconcileReport struct {
		Listed, Applied, Mismatches int
	}

	// Jobs are the periodic tasks and CLI maintenance; the periodic ones take a Redis lock so one instance runs.
	Jobs interface {
		ReconcilePending(ctx context.Context) (int, error)
		SubscriptionExpiry(ctx context.Context) (reminded, expired int, err error)
		// ReconcileDailyIfDue reconciles yesterday once, after 02:00 business time.
		ReconcileDailyIfDue(ctx context.Context) (bool, ReconcileReport, error)
		ReconcileDaily(ctx context.Context, day time.Time) (ReconcileReport, error)
		PurgeIdempotency(ctx context.Context) (int64, error)
		RepublishEntitlements(ctx context.Context, channelID *string) (int, error)
	}

	// IdemKey identifies an Idempotency-Key per caller, method and path.
	IdemKey = repo.IdemKey
	// IdemState is what Begin found.
	IdemState = repo.IdemState

	// Idempotency backs the Idempotency-Key middleware.
	Idempotency interface {
		Begin(ctx context.Context, k IdemKey, hash []byte) (IdemState, error)
		Complete(ctx context.Context, k IdemKey, status int, body []byte) error
		Abort(ctx context.Context, k IdemKey) error
	}

	// DevPSP drives the dev fake payment page; each action returns the URL to redirect to.
	DevPSP interface {
		FakeOpen(ctx context.Context, fields []entity.Field) (entity.Order, error)
		FakePay(ctx context.Context, invoice string) (string, error)
		FakeCancel(ctx context.Context, invoice string) (string, error)
		FakeFail(ctx context.Context, invoice string) (string, error)
	}

	// Observer receives billing metrics.
	Observer interface {
		IPN(typ string, result IPNResult, d time.Duration)
		OrderStatus(status entity.OrderStatus, method, reason string)
		ReconcileRun(job, result string)
		ReconcileMismatch(kind string)
		NeedsReview(n int)
		ActiveSubscriptions(byTier map[int]int)
		Voided()
		PayoutStatus(status entity.PayoutStatus)
		Idempotency(result string)
	}
)

// Outcomes of Idempotency.Begin.
const (
	IdemNew        = repo.IdemNew
	IdemInProgress = repo.IdemInProgress
	IdemDone       = repo.IdemDone
	IdemMismatch   = repo.IdemMismatch
)

// Outcomes of sync.
const (
	OutcomeSuccess Outcome = "SUCCESS"
	OutcomeError   Outcome = "ERROR"
	OutcomeCancel  Outcome = "CANCEL"
)

// IPN results.
const (
	IPNApplied        IPNResult = "applied"
	IPNDuplicate      IPNResult = "duplicate"
	IPNUnknownInvoice IPNResult = "unknown_invoice"
	IPNMismatch       IPNResult = "mismatch"
	IPNIgnored        IPNResult = "ignored"
	IPNNotApproved    IPNResult = "not_approved"
	IPNUnverified     IPNResult = "unverified"
	IPNVoidIgnored    IPNResult = "void_ignored"
	IPNUnauthorized   IPNResult = "unauthorized"
	IPNMalformed      IPNResult = "malformed"
	IPNError          IPNResult = "error"
)
