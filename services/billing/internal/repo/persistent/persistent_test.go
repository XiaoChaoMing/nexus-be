package persistent_test

// Integration tests against a migrated nxl_billing_test database:
//   NXL_TEST_PG_URL_BILLING=postgres://user:pass@localhost:5432/nxl_billing_test?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase/billing"
	"github.com/stretchr/testify/require"
)

const ipnSecret = "test-ipn-secret"

// The tests share one database: they run one after the other and truncate first.
var dbMu sync.Mutex //nolint:gochecknoglobals // serializes the tests on the shared database

func setup(t *testing.T) (*persistent.Repo, *postgres.Postgres) {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_BILLING")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_BILLING not set")
	}

	// setup wipes every table: only a throwaway database (name ending in _test) is accepted.
	dsn, err := neturl.Parse(url)
	require.NoError(t, err)

	if !strings.HasSuffix(dsn.Path, "_test") {
		t.Fatalf("NXL_TEST_PG_URL_BILLING must name a database ending in _test (got %q)", strings.TrimPrefix(dsn.Path, "/"))
	}

	dbMu.Lock()
	t.Cleanup(dbMu.Unlock)

	pg, err := postgres.New(url, postgres.MaxPoolSize(16))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(), `TRUNCATE ledger_entries, transactions, payout_requests, checkout_orders,
		subscriptions, subscription_tiers, channels, outbox, processed_events, idempotency_keys, wallet_entries, gift_sends,
		gift_items RESTART IDENTITY`)
	require.NoError(t, err)

	return persistent.New(pg), pg
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

func count(t *testing.T, pg *postgres.Postgres, sql string, args ...any) int {
	t.Helper()

	var n int
	require.NoError(t, pg.Pool.QueryRow(context.Background(), sql, args...).Scan(&n))

	return n
}

func outboxTypes(t *testing.T, pg *postgres.Postgres) map[string]int {
	t.Helper()

	rows, err := pg.Pool.Query(context.Background(), `SELECT event_type FROM outbox`)
	require.NoError(t, err)

	defer rows.Close()

	out := map[string]int{}

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out[s]++
	}

	return out
}

func defaultTiers() []entity.Tier {
	return []entity.Tier{
		{Level: 1, Price: 59000, Description: "t1"},
		{Level: 2, Price: 119000, Description: "t2"},
		{Level: 3, Price: 299000, Description: "t3"},
	}
}

// seedChannel copies a channel with its three tiers and returns it with tier 1 and tier 2.
func seedChannel(t *testing.T, r *persistent.Repo) (ch entity.Channel, tier1, tier2 entity.Tier) {
	t.Helper()

	ctx := context.Background()
	ch = entity.Channel{ID: newID(), OwnerID: newID(), Handle: "kaelen", DisplayName: "Kaelen"}
	require.NoError(t, r.UpsertChannel(ctx, newID(), time.Now(), ch, false, defaultTiers()))

	tiers, err := r.ByChannel(ctx, ch.ID, true)
	require.NoError(t, err)
	require.Len(t, tiers, 3)

	ch.Status = entity.ChannelActive

	return ch, tiers[0], tiers[1]
}

// stubProvider answers lookups with a fixed state and parses IPNs like SePay.
type stubProvider struct {
	provider.PaymentProvider

	mu    sync.Mutex
	state map[string]provider.OrderState
}

func (*stubProvider) Name() string { return provider.NameSePay }

func (*stubProvider) BuildCheckout(context.Context, provider.CheckoutOrder) (entity.Checkout, error) {
	return entity.Checkout{}, nil
}

func (*stubProvider) ParseNotification(h http.Header, body []byte) (provider.Notification, error) {
	return sepay.ParseIPN(h, body, ipnSecret)
}

func (s *stubProvider) QueryOrder(_ context.Context, invoice string) (provider.OrderState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.state[invoice]
	if !ok {
		return st, entity.ErrProviderNotFound
	}

	return st, nil
}

func (*stubProvider) CancelOrder(context.Context, string) error { return nil }

type nopObserver struct{}

func (nopObserver) IPN(string, usecase.IPNResult, time.Duration)   {}
func (nopObserver) OrderStatus(entity.OrderStatus, string, string) {}
func (nopObserver) ReconcileRun(string, string)                    {}
func (nopObserver) ReconcileMismatch(string)                       {}
func (nopObserver) NeedsReview(int)                                {}
func (nopObserver) ActiveSubscriptions(map[int]int)                {}
func (nopObserver) Voided()                                        {}
func (nopObserver) PayoutStatus(entity.PayoutStatus)               {}
func (nopObserver) Idempotency(string)                             {}
func (nopObserver) ProviderCall(string, string, time.Duration)     {}

type alwaysLock struct{}

func (alwaysLock) TryLock(context.Context, string, time.Duration) (release func(), ok bool, err error) {
	return func() {}, true, nil
}
func (alwaysLock) LastRun(context.Context, string) (string, error)  { return "", nil }
func (alwaysLock) SetLastRun(context.Context, string, string) error { return nil }
func (alwaysLock) Hit(context.Context, string) (int, time.Duration, error) {
	return 1, time.Minute, nil
}

func newUseCase(r *persistent.Repo, p provider.PaymentProvider) *billing.UseCase {
	return billing.New(billing.Repos{
		Channels: r, Tiers: r, Orders: r, Subs: r, Ledger: r, Payouts: r, Audit: r, Txns: r, Idem: r,
		Lock: alwaysLock{}, Sync: alwaysLock{}, Wallet: r, Gifts: r,
	}, p, billing.Settings{
		TierPrices: [3]int64{59000, 119000, 299000}, FeeBps: 500, PayoutMin: 100000, OrderTTL: 30 * time.Minute,
		ReconcileAfter: -time.Minute, ReconcileInterval: time.Minute, ReconcileGiveUp: 30 * time.Minute,
		RenewReminderDays: 3, Methods: []entity.PaymentMethod{entity.MethodBankTransfer}, WebBaseURL: "http://localhost:5173",
		TopupMin: 10000, TopupMax: 5000000, GiftMaxTotal: 5000000,
	}, logger.New("error"), nopObserver{})
}
