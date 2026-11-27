// Package repo declares the storage and gateway contracts the billing use cases call.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
)

type (
	// Outbox is one event row written in the same transaction as the change it announces.
	Outbox struct {
		AggregateType string
		AggregateID   string
		EventType     string
		Payload       any
	}

	// Cursor is a keyset position: the sort time and the id of the last row of the previous page.
	Cursor struct {
		At time.Time
		ID string
	}

	// TierPatch changes a tier; nil fields stay.
	TierPatch struct {
		Active      *bool
		Description *string
	}

	// CreateRules are the §6.2.1 checks 7–11 that run under the per-user advisory lock.
	CreateRules struct {
		Now        time.Time
		MaxPending int
		MaxPrepaid time.Duration
		// NewInvoice draws an invoice number; a collision draws again (at most 3 times).
		NewInvoice func() (string, error)
		// Expired builds the events of a stale pending order the check moves to EXPIRED.
		Expired func(o entity.Order) []Outbox
	}

	// MarkPaid are the columns a payment sets.
	MarkPaid struct {
		PaidAt          time.Time
		ProviderOrderID *string
		Method          *entity.PaymentMethod
		NeedsReview     bool
		SubscriptionID  *string
	}

	// PayoutMove is one payout state change; the repo sets the columns of the target status.
	PayoutMove struct {
		To      entity.PayoutStatus
		ActorID string
		At      time.Time
		// Reason of REJECTED / FAILED.
		Reason *string
		// Provider of PROCESSING (and of PAID / FAILED reported by a synchronous provider).
		Provider *string
		// Ref is the provider reference; required for PAID.
		Ref *string
	}

	// GiftItemPatch changes a catalog item; nil fields stay.
	GiftItemPatch struct {
		Name      *string
		Price     *int64
		Icon      *string
		Active    *bool
		SortOrder *int
	}

	// GiftRules are the settings a gift is checked against inside the transaction.
	GiftRules struct {
		FeeBps   int
		MaxTotal int64
	}

	// SubFilter selects the caller's subscriptions.
	SubFilter struct {
		Expired   bool
		ChannelID *string
		Now       time.Time
	}

	// AdminOrderFilter selects orders for the admin review page. Invoice is upper case; InvoiceExact compares it
	// with "=", otherwise it is a prefix.
	AdminOrderFilter struct {
		NeedsReview    *bool
		Statuses       []entity.OrderStatus
		FailureReasons []entity.FailureReason
		Invoice        string
		InvoiceExact   bool
	}

	// IdemKey identifies an Idempotency-Key per caller and route.
	IdemKey struct {
		UserID, Method, Path, Key string
	}

	// IdemState is what Begin found.
	IdemState struct {
		Outcome IdemOutcome
		Status  int
		Body    []byte
	}

	// IdemOutcome -.
	IdemOutcome int
)

// Outcomes of IdempotencyRepo.Begin.
const (
	IdemNew IdemOutcome = iota
	IdemInProgress
	IdemDone
	IdemMismatch
)

type (
	// ChannelRepo is the channel copy.
	ChannelRepo interface {
		Channel(ctx context.Context, id string) (entity.Channel, error)
		// ByOwner returns entity.ErrNotCreator when the user owns no channel.
		ByOwner(ctx context.Context, ownerID string) (entity.Channel, error)
		// UpsertChannel records processed_events(eventID) (empty = backfill, no record) and skips a row whose
		// synced_at is newer than at; tiers are inserted ON CONFLICT DO NOTHING in the same transaction.
		UpsertChannel(ctx context.Context, eventID string, at time.Time, c entity.Channel, setAvatar bool, tiers []entity.Tier) error
		SetStatus(ctx context.Context, eventID string, at time.Time, channelID, status string) error
	}

	// TierRepo -.
	TierRepo interface {
		Tier(ctx context.Context, id string) (entity.Tier, error)
		ByChannel(ctx context.Context, channelID string, activeOnly bool) ([]entity.Tier, error)
		// ActiveSubscribers counts live subscriptions per tier level.
		ActiveSubscribers(ctx context.Context, channelID string, now time.Time) (map[int]int, error)
		// UpdateTier applies patch to the tier of level and writes out(before, after) in the same transaction;
		// an empty out means nothing changed.
		UpdateTier(ctx context.Context, channelID string, level int, patch TierPatch,
			out func(before, after entity.Tier) []Outbox) (entity.Tier, error)
	}

	// OrderRepo -.
	OrderRepo interface {
		// CreateOrReuse returns the inserted order (true) or the caller's pending order of the same tier (false);
		// it fails with an entity.StateError or entity.ErrPendingLimit.
		CreateOrReuse(ctx context.Context, o entity.Order, rules CreateRules) (entity.Order, bool, error)
		UserOrder(ctx context.Context, userID, id string) (entity.Order, error)
		ByInvoice(ctx context.Context, invoice string) (entity.Order, error)
		// ByID is any user's order (admin).
		ByID(ctx context.Context, id string) (entity.Order, error)
		ForUser(ctx context.Context, userID string, statuses []entity.OrderStatus, after *Cursor, limit int) ([]entity.Order, error)
		Admin(ctx context.Context, f AdminOrderFilter, after *Cursor, limit int) ([]entity.Order, error)
		// Transition is UPDATE … WHERE status = ANY(from) plus out(updated) in one transaction; false = the state
		// moved on and nothing was written.
		Transition(ctx context.Context, id string, from []entity.OrderStatus, to entity.OrderStatus,
			reason *entity.FailureReason, out func(entity.Order) []Outbox) (entity.Order, bool, error)
		DuePending(ctx context.Context, createdBefore time.Time, limit int) ([]entity.Order, error)
		PaidBetween(ctx context.Context, from, to time.Time) ([]entity.Order, error)
		CountNeedsReview(ctx context.Context) (int, error)
		// WithLockedOrder runs fn in one transaction holding SELECT … FOR UPDATE on the order of invoice
		// (entity.ErrNotFound). An error from fn rolls everything back.
		WithLockedOrder(ctx context.Context, invoice string, fn func(tx PaymentTx, o entity.Order) error) error
	}

	// PaymentTx writes inside the transaction of WithLockedOrder. Lock order is always order → subscription.
	PaymentTx interface {
		// InsertTransaction returns inserted = false when (psp_ref, type) already exists.
		InsertTransaction(ctx context.Context, t entity.Transaction) (id string, inserted bool, err error)
		MarkPaid(ctx context.Context, orderID string, p MarkPaid) (bool, error)
		MarkVoided(ctx context.Context, orderID string) (bool, error)
		MarkMismatch(ctx context.Context, orderID string) (bool, error)
		// LiveSub locks the live (ACTIVE / CANCELLED_PENDING_END) subscription of user and channel, or nil.
		LiveSub(ctx context.Context, userID, channelID string) (*entity.Subscription, error)
		// Sub locks a subscription by id, or nil.
		Sub(ctx context.Context, id string) (*entity.Subscription, error)
		InsertSub(ctx context.Context, s entity.Subscription) (entity.Subscription, error)
		// ExtendSub sets the new end, status ACTIVE, auto_renew and clears cancelled_at.
		ExtendSub(ctx context.Context, id string, end time.Time) (entity.Subscription, error)
		ReduceSub(ctx context.Context, id string, end time.Time) error
		ExpireSub(ctx context.Context, id string, end time.Time, reason string) error
		// ChargeFee is the platform fee recorded when the order was paid.
		ChargeFee(ctx context.Context, orderID string) (int64, error)
		InsertLedger(ctx context.Context, e []entity.LedgerEntry) error
		InsertWallet(ctx context.Context, e []entity.WalletEntry) error
		InsertOutbox(ctx context.Context, rows []Outbox) error
	}

	// SubRepo -.
	SubRepo interface {
		Mine(ctx context.Context, userID string, f SubFilter, after *Cursor, limit int) ([]entity.Subscription, error)
		// Latest is the live subscription of user and channel, else the most recently ended one, or nil.
		Latest(ctx context.Context, userID, channelID string, now time.Time) (*entity.Subscription, error)
		Get(ctx context.Context, id string) (*entity.Subscription, error)
		// Cancel / Resume are conditional updates; entity.ErrNotFound or entity.ErrInvalidState otherwise.
		Cancel(ctx context.Context, userID, subID string, now time.Time, out func(entity.Subscription) []Outbox) (entity.Subscription, error)
		Resume(ctx context.Context, userID, subID string, now time.Time, out func(entity.Subscription) []Outbox) (entity.Subscription, error)
		DueReminders(ctx context.Context, now time.Time, window time.Duration, limit int) ([]entity.Subscription, error)
		MarkReminded(ctx context.Context, s entity.Subscription, out []Outbox) (bool, error)
		DueExpiry(ctx context.Context, now time.Time, limit int) ([]entity.Subscription, error)
		Expire(ctx context.Context, s entity.Subscription, out []Outbox) (bool, error)
		// Live pages live subscriptions by id (republish); channelID narrows to one channel.
		Live(ctx context.Context, channelID *string, afterID string, now time.Time, limit int) ([]entity.Subscription, error)
		CountActive(ctx context.Context, now time.Time) (map[int]int, error)
	}

	// LedgerRepo -.
	LedgerRepo interface {
		Totals(ctx context.Context, channelID string, from, to time.Time) (entity.Totals, error)
		// Balance: available is Σ CREDIT − Σ DEBIT (payout holds included); held is the sum of open payouts.
		Balance(ctx context.Context, channelID string) (available, held int64, err error)
		Entries(ctx context.Context, channelID string, after *Cursor, limit int) ([]entity.LedgerEntry, error)
	}

	// PayoutRepo -.
	PayoutRepo interface {
		// CreatePayout checks PAYOUT_PENDING and INSUFFICIENT_BALANCE under a per-channel lock and holds the
		// amount (DEBIT PAYOUT_HOLD).
		CreatePayout(ctx context.Context, p entity.Payout, out func(entity.Payout) []Outbox) (entity.Payout, error)
		ForChannel(ctx context.Context, channelID string, after *Cursor, limit int) ([]entity.Payout, error)
		ListPayouts(ctx context.Context, status *entity.PayoutStatus, after *Cursor, limit int) ([]entity.Payout, error)
		Payout(ctx context.Context, id string) (entity.Payout, error)
		// Move applies one state change under a row lock: a move CanMoveTo refuses is StateError(PAYOUT_STATE);
		// REJECTED and FAILED give the hold back (CREDIT PAYOUT_REVERSAL).
		Move(ctx context.Context, id string, m PayoutMove, out func(entity.Payout) []Outbox) (entity.Payout, error)
		CountRequested(ctx context.Context) (int, error)
	}

	// WalletRepo reads the users' wallets.
	WalletRepo interface {
		// Balance is Σ CREDIT − Σ DEBIT of the wallet; it is negative after a spent top-up was voided.
		WalletBalance(ctx context.Context, userID string) (int64, error)
		WalletEntries(ctx context.Context, userID string, after *Cursor, limit int) ([]entity.WalletEntry, error)
	}

	// GiftRepo is the gift catalog and the gifts.
	GiftRepo interface {
		// ActiveGiftItems is the catalog viewers pick from; GiftItems is the admin list (active nil = all).
		ActiveGiftItems(ctx context.Context) ([]entity.GiftItem, error)
		GiftItems(ctx context.Context, active *bool) ([]entity.GiftItem, error)
		CreateGiftItem(ctx context.Context, it entity.GiftItem, out func(entity.GiftItem) []Outbox) (entity.GiftItem, error)
		UpdateGiftItem(ctx context.Context, id string, p GiftItemPatch,
			out func(before, after entity.GiftItem) []Outbox) (entity.GiftItem, error)
		// SendGift locks the sender's wallet and the item, refuses an inactive item (GIFT_INACTIVE), a total above
		// rules.MaxTotal (quantity MAX) or above the balance (INSUFFICIENT_FUNDS), then writes the gift, the wallet
		// debit, the channel ledger and out() in one transaction. It returns the balance after the gift.
		SendGift(ctx context.Context, g entity.GiftSend, rules GiftRules, out func(entity.GiftSend) []Outbox,
		) (entity.GiftSend, int64, error)
	}

	// AuditRepo writes event rows that change nothing else (audit-only actions).
	AuditRepo interface {
		Record(ctx context.Context, rows []Outbox) error
	}

	// TransactionRepo reads recorded provider transactions (dev CLI).
	TransactionRepo interface {
		Transaction(ctx context.Context, id string) (entity.Transaction, error)
		ChargeOf(ctx context.Context, orderID string) (entity.Transaction, error)
	}

	// IdempotencyRepo stores Idempotency-Key results (NXL-00 §8).
	IdempotencyRepo interface {
		Begin(ctx context.Context, k IdemKey, hash []byte, stale time.Duration) (IdemState, error)
		Complete(ctx context.Context, k IdemKey, status int, body []byte) error
		Abort(ctx context.Context, k IdemKey) error
		Purge(ctx context.Context, before time.Time) (int64, error)
		PurgeProcessed(ctx context.Context, before time.Time) (int64, error)
	}

	// JobLock lets one instance run a job (Redis SET NX PX).
	JobLock interface {
		TryLock(ctx context.Context, name string, ttl time.Duration) (release func(), ok bool, err error)
		LastRun(ctx context.Context, name string) (string, error)
		SetLastRun(ctx context.Context, name, value string) error
	}

	// SyncLimiter counts sync calls per order and minute.
	SyncLimiter interface {
		Hit(ctx context.Context, orderID string) (n int, ttl time.Duration, err error)
	}

	// Cipher seals bank accounts with AES-256-GCM.
	Cipher interface {
		Seal(plain, aad []byte) ([]byte, error)
		Open(blob, aad []byte) ([]byte, error)
	}

	// ChannelSnapshot pages channel-svc's internal snapshot (backfill only).
	ChannelSnapshot interface {
		Channels(ctx context.Context, after string, limit int) ([]SnapshotChannel, string, error)
	}

	// SnapshotChannel is one channel of the snapshot.
	SnapshotChannel struct {
		Channel   entity.Channel
		UpdatedAt time.Time
	}
)
