package billing_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase/billing"
)

const (
	ownerID   = "0199e000-0000-7000-8000-000000000001"
	viewerID  = "0199e000-0000-7000-8000-000000000002"
	otherID   = "0199e000-0000-7000-8000-000000000003"
	adminID   = "0199e000-0000-7000-8000-000000000004"
	channelID = "0199e000-0000-7000-8000-0000000000aa"
	tier1ID   = "0199e001-0000-7000-8000-000000000001"
	tier2ID   = "0199e001-0000-7000-8000-000000000002"
	tier3ID   = "0199e001-0000-7000-8000-000000000003"
	missingID = "0199e001-0000-7000-8000-0000000000ff"
	ipnSecret = "test-ipn-secret"
)

var now = time.Date(2026, 2, 10, 10, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock

// zeroReader makes invoice numbers deterministic ("NXL0000000000"), counter-suffixed by the store.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}

// mem is an in-memory store with the semantics of persistent.Repo the use cases rely on.
type mem struct {
	repo.ChannelSnapshot

	mu       sync.Mutex
	channels map[string]entity.Channel
	tiers    map[string]entity.Tier
	orders   map[string]*entity.Order
	subs     map[string]*entity.Subscription
	txns     map[string]entity.Transaction
	ledger   []entity.LedgerEntry
	outbox   []repo.Outbox
	payouts  map[string]*entity.Payout
	seq      int
	syncHits int
	failNext error
}

func newMem() *mem {
	m := &mem{
		channels: map[string]entity.Channel{}, tiers: map[string]entity.Tier{}, orders: map[string]*entity.Order{},
		subs: map[string]*entity.Subscription{}, txns: map[string]entity.Transaction{}, payouts: map[string]*entity.Payout{},
	}
	m.channels[channelID] = entity.Channel{
		ID: channelID, OwnerID: ownerID, Handle: "kaelen", DisplayName: "Kaelen",
		Status: entity.ChannelActive,
	}

	for i, id := range []string{tier1ID, tier2ID, tier3ID} {
		m.tiers[id] = entity.Tier{
			ID: id, ChannelID: channelID, Level: i + 1, Price: []int64{59000, 119000, 299000}[i],
			Active: true,
		}
	}

	return m
}

func (m *mem) id() string {
	m.seq++

	return fmt.Sprintf("0199f000-0000-7000-8000-%012d", m.seq)
}

func (m *mem) events() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.outbox))
	for _, o := range m.outbox {
		out = append(out, o.EventType)
	}

	return out
}

func (m *mem) countEvents(typ string) int {
	n := 0

	for _, e := range m.events() {
		if e == typ {
			n++
		}
	}

	return n
}

func (m *mem) fill(o entity.Order) entity.Order {
	o.Channel, o.Tier = m.channels[o.ChannelID], m.tiers[o.TierID]

	return o
}

func (m *mem) fillSub(s entity.Subscription) entity.Subscription {
	s.Channel, s.Tier = m.channels[s.ChannelID], m.tiers[s.TierID]

	return s
}

// ---- ChannelRepo ----

func (m *mem) Channel(_ context.Context, id string) (entity.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.channels[id]
	if !ok {
		return c, entity.ErrNotFound
	}

	return c, nil
}

func (m *mem) ByOwner(_ context.Context, owner string) (entity.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.channels {
		if c.OwnerID == owner {
			return c, nil
		}
	}

	return entity.Channel{}, entity.ErrNotCreator
}

func (m *mem) UpsertChannel(_ context.Context, _ string, _ time.Time, c entity.Channel, _ bool, tiers []entity.Tier) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c.Status == "" {
		c.Status = entity.ChannelActive
	}

	m.channels[c.ID] = c

	for _, t := range tiers {
		t.ID, t.ChannelID = m.id(), c.ID
		m.tiers[t.ID] = t
	}

	return nil
}

func (m *mem) SetStatus(_ context.Context, _ string, _ time.Time, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.channels[id]
	c.Status = status
	m.channels[id] = c

	return nil
}

// ---- TierRepo ----

func (m *mem) Tier(_ context.Context, id string) (entity.Tier, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tiers[id]
	if !ok {
		return t, entity.ErrNotFound
	}

	return t, nil
}

func (m *mem) ByChannel(_ context.Context, ch string, activeOnly bool) ([]entity.Tier, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []entity.Tier

	for _, t := range m.tiers {
		if t.ChannelID == ch && (t.Active || !activeOnly) {
			out = append(out, t)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Level < out[j].Level })

	return out, nil
}

func (m *mem) ActiveSubscribers(_ context.Context, ch string, at time.Time) (map[int]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := map[int]int{}

	for _, s := range m.subs {
		if s.ChannelID == ch && s.Live(at) {
			out[m.tiers[s.TierID].Level]++
		}
	}

	return out, nil
}

func (m *mem) UpdateTier(_ context.Context, ch string, level int, p repo.TierPatch, out func(b, a entity.Tier) []repo.Outbox,
) (entity.Tier, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, t := range m.tiers {
		if t.ChannelID != ch || t.Level != level {
			continue
		}

		after := t
		if p.Active != nil {
			after.Active = *p.Active
		}

		if p.Description != nil {
			after.Description = *p.Description
		}

		m.tiers[id] = after
		m.outbox = append(m.outbox, out(t, after)...)

		return after, nil
	}

	return entity.Tier{}, entity.ErrNotFound
}

// ---- OrderRepo ----

func (m *mem) liveSub(user, ch string) *entity.Subscription {
	for _, s := range m.subs {
		if s.UserID == user && s.ChannelID == ch && s.Status != entity.SubExpired {
			return s
		}
	}

	return nil
}

func (m *mem) CreateOrReuse(_ context.Context, o entity.Order, rules repo.CreateRules) (entity.Order, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o.SubAction = entity.SubActionNew

	if s := m.liveSub(o.UserID, o.ChannelID); s != nil && s.CurrentPeriodEnd.After(rules.Now) {
		if s.TierID != o.TierID {
			return o, false, entity.StateError(entity.ReasonTierChange)
		}

		if entity.RenewedEnd(s.CurrentPeriodEnd, rules.Now, o.Period()).After(rules.Now.Add(rules.MaxPrepaid)) {
			return o, false, entity.StateError(entity.ReasonMaxPrepaid)
		}

		o.SubAction = entity.SubActionRenew
	}

	if p, err := m.samePending(o); p != nil || err != nil {
		if p != nil {
			return *p, false, nil
		}

		return o, false, err
	}

	if m.pendingElsewhere(o) >= rules.MaxPending {
		return o, false, entity.ErrPendingLimit
	}

	inv, err := rules.NewInvoice()
	if err != nil {
		return o, false, err
	}

	o.ID, o.InvoiceNumber, o.Status, o.CreatedAt, o.UpdatedAt = m.id(), inv+fmt.Sprint(m.seq), entity.OrderPending, now, now
	m.orders[o.ID] = &o

	return m.fill(o), true, nil
}

func (m *mem) samePending(o entity.Order) (*entity.Order, error) {
	for _, p := range m.orders {
		if p.UserID != o.UserID || p.Status != entity.OrderPending || p.ChannelID != o.ChannelID {
			continue
		}

		if p.TierID != o.TierID {
			return nil, entity.StateError(entity.ReasonOrderPending)
		}

		filled := m.fill(*p)

		return &filled, nil
	}

	return nil, nil
}

func (m *mem) pendingElsewhere(o entity.Order) int {
	n := 0

	for _, p := range m.orders {
		if p.UserID == o.UserID && p.Status == entity.OrderPending && p.ChannelID != o.ChannelID {
			n++
		}
	}

	return n
}

func (m *mem) UserOrder(_ context.Context, user, id string) (entity.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o, ok := m.orders[id]
	if !ok || o.UserID != user {
		return entity.Order{}, entity.ErrNotFound
	}

	return m.fill(*o), nil
}

func (m *mem) byInvoice(inv string) *entity.Order {
	for _, o := range m.orders {
		if o.InvoiceNumber == inv {
			return o
		}
	}

	return nil
}

func (m *mem) ByInvoice(_ context.Context, inv string) (entity.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o := m.byInvoice(inv)
	if o == nil {
		return entity.Order{}, entity.ErrNotFound
	}

	return m.fill(*o), nil
}

func (m *mem) list(keep func(*entity.Order) bool) []entity.Order {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []entity.Order

	for _, o := range m.orders {
		if keep(o) {
			out = append(out, m.fill(*o))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })

	return out
}

func (m *mem) ForUser(_ context.Context, user string, st []entity.OrderStatus, _ *repo.Cursor, _ int) ([]entity.Order, error) {
	return m.list(func(o *entity.Order) bool {
		return o.UserID == user && (len(st) == 0 || slices.Contains(st, o.Status))
	}), nil
}

func (m *mem) Admin(_ context.Context, f repo.AdminOrderFilter, _ *repo.Cursor, _ int) ([]entity.Order, error) {
	return m.list(func(o *entity.Order) bool { return f.NeedsReview == nil || o.NeedsReview == *f.NeedsReview }), nil
}

func (m *mem) Transition(_ context.Context, id string, from []entity.OrderStatus, to entity.OrderStatus,
	reason *entity.FailureReason, out func(entity.Order) []repo.Outbox,
) (entity.Order, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o := m.orders[id]
	if !slices.Contains(from, o.Status) {
		return m.fill(*o), false, nil
	}

	o.Status, o.FailureReason = to, reason
	m.outbox = append(m.outbox, out(m.fill(*o))...)

	return m.fill(*o), true, nil
}

func (m *mem) DuePending(_ context.Context, before time.Time, _ int) ([]entity.Order, error) {
	return m.list(func(o *entity.Order) bool { return o.Status == entity.OrderPending && o.CreatedAt.Before(before) }), nil
}

func (m *mem) PaidBetween(_ context.Context, from, to time.Time) ([]entity.Order, error) {
	return m.list(func(o *entity.Order) bool {
		return o.PaidAt != nil && !o.PaidAt.Before(from) && o.PaidAt.Before(to)
	}), nil
}

func (m *mem) CountNeedsReview(context.Context) (int, error) {
	return len(m.list(func(o *entity.Order) bool { return o.NeedsReview })), nil
}

func (m *mem) WithLockedOrder(ctx context.Context, inv string, fn func(tx repo.PaymentTx, o entity.Order) error) error {
	m.mu.Lock()
	o := m.byInvoice(inv)

	if o == nil {
		m.mu.Unlock()

		return entity.ErrNotFound
	}

	snapshot := m.fill(*o)
	m.mu.Unlock()

	tx := &memTx{m: m}
	if err := fn(tx, snapshot); err != nil {
		return err
	}

	return tx.commit(ctx)
}

// memTx buffers writes and applies them at commit (an error rolls everything back).
type memTx struct {
	m   *mem
	ops []func()
}

func (t *memTx) commit(context.Context) error {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()

	for _, op := range t.ops {
		op()
	}

	return nil
}

func (t *memTx) InsertTransaction(_ context.Context, tr entity.Transaction) (id string, inserted bool, err error) {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()

	key := tr.PSPRef + "|" + string(tr.Type)
	if _, ok := t.m.txns[key]; ok {
		return "", false, nil
	}

	tr.ID = t.m.id()
	t.ops = append(t.ops, func() { t.m.txns[key] = tr })

	return tr.ID, true, nil
}

func (t *memTx) MarkPaid(_ context.Context, id string, p repo.MarkPaid) (bool, error) {
	t.ops = append(t.ops, func() {
		o := t.m.orders[id]
		o.Status, o.PaidAt, o.FailureReason, o.NeedsReview, o.SubscriptionID = entity.OrderPaid, &p.PaidAt, nil, p.NeedsReview,
			p.SubscriptionID

		if o.PaymentMethod == nil {
			o.PaymentMethod = p.Method
		}
	})

	return true, nil
}

func (t *memTx) MarkVoided(_ context.Context, id string) (bool, error) {
	t.ops = append(t.ops, func() { t.m.orders[id].Status = entity.OrderVoided })

	return true, nil
}

func (t *memTx) MarkMismatch(_ context.Context, id string) (bool, error) {
	t.ops = append(t.ops, func() {
		r := entity.FailAmountMismatch
		t.m.orders[id].Status, t.m.orders[id].FailureReason = entity.OrderFailed, &r
	})

	return true, nil
}

func (t *memTx) LiveSub(_ context.Context, user, ch string) (*entity.Subscription, error) {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()

	if s := t.m.liveSub(user, ch); s != nil {
		c := t.m.fillSub(*s)

		return &c, nil
	}

	return nil, nil
}

func (t *memTx) Sub(_ context.Context, id string) (*entity.Subscription, error) {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()

	if s, ok := t.m.subs[id]; ok {
		c := t.m.fillSub(*s)

		return &c, nil
	}

	return nil, nil
}

func (t *memTx) InsertSub(_ context.Context, s entity.Subscription) (entity.Subscription, error) {
	t.m.mu.Lock()
	s.ID, s.Status, s.AutoRenew, s.CreatedAt = t.m.id(), entity.SubActive, true, now
	t.m.mu.Unlock()

	t.ops = append(t.ops, func() { t.m.subs[s.ID] = &s })

	return t.m.fillSub(s), nil
}

func (t *memTx) ExtendSub(_ context.Context, id string, end time.Time) (entity.Subscription, error) {
	t.m.mu.Lock()
	s := *t.m.subs[id]
	t.m.mu.Unlock()

	s.Status, s.CurrentPeriodEnd, s.AutoRenew, s.CancelledAt = entity.SubActive, end, true, nil

	t.ops = append(t.ops, func() { *t.m.subs[id] = s })

	return t.m.fillSub(s), nil
}

func (t *memTx) ReduceSub(_ context.Context, id string, end time.Time) error {
	t.ops = append(t.ops, func() { t.m.subs[id].CurrentPeriodEnd = end })

	return nil
}

func (t *memTx) ExpireSub(_ context.Context, id string, end time.Time, reason string) error {
	t.ops = append(t.ops, func() {
		s := t.m.subs[id]
		s.Status, s.CurrentPeriodEnd, s.ExpiredReason = entity.SubExpired, end, &reason
	})

	return nil
}

func (t *memTx) ChargeFee(_ context.Context, orderID string) (int64, error) {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()

	for _, tr := range t.m.txns {
		if tr.OrderID != orderID || tr.Type != entity.TxnCharge {
			continue
		}

		for _, e := range t.m.ledger {
			if e.TransactionID != nil && *e.TransactionID == tr.ID && e.Kind == entity.LedgerPlatformFee {
				return e.Amount, nil
			}
		}
	}

	return 0, nil
}

func (t *memTx) InsertLedger(_ context.Context, e []entity.LedgerEntry) error {
	t.ops = append(t.ops, func() { t.m.ledger = append(t.m.ledger, e...) })

	return nil
}

func (t *memTx) InsertOutbox(_ context.Context, rows []repo.Outbox) error {
	t.ops = append(t.ops, func() { t.m.outbox = append(t.m.outbox, rows...) })

	return nil
}

// ---- SubRepo ----

func (m *mem) allSubs(keep func(*entity.Subscription) bool) []entity.Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []entity.Subscription

	for _, s := range m.subs {
		if keep(s) {
			out = append(out, m.fillSub(*s))
		}
	}

	return out
}

func (m *mem) Mine(_ context.Context, user string, f repo.SubFilter, _ *repo.Cursor, _ int) ([]entity.Subscription, error) {
	return m.allSubs(func(s *entity.Subscription) bool { return s.UserID == user && s.Live(f.Now) != f.Expired }), nil
}

func (m *mem) Latest(_ context.Context, user, ch string, _ time.Time) (*entity.Subscription, error) {
	subs := m.allSubs(func(s *entity.Subscription) bool { return s.UserID == user && s.ChannelID == ch })
	if len(subs) == 0 {
		return nil, nil
	}

	return &subs[0], nil
}

func (m *mem) Get(_ context.Context, id string) (*entity.Subscription, error) {
	subs := m.allSubs(func(s *entity.Subscription) bool { return s.ID == id })
	if len(subs) == 0 {
		return nil, nil
	}

	return &subs[0], nil
}

func (m *mem) cond(user, id string, from entity.SubStatus, apply func(*entity.Subscription),
	out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.subs[id]
	if !ok || s.UserID != user {
		return entity.Subscription{}, entity.ErrNotFound
	}

	if s.Status != from || !s.CurrentPeriodEnd.After(now) {
		return *s, entity.ErrInvalidState
	}

	apply(s)
	m.outbox = append(m.outbox, out(m.fillSub(*s))...)

	return m.fillSub(*s), nil
}

func (m *mem) Cancel(_ context.Context, user, id string, at time.Time, out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error) {
	return m.cond(user, id, entity.SubActive, func(s *entity.Subscription) {
		s.Status, s.AutoRenew, s.CancelledAt = entity.SubCancelledPendingEnd, false, &at
	}, out)
}

func (m *mem) Resume(_ context.Context, user, id string, _ time.Time, out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error) {
	return m.cond(user, id, entity.SubCancelledPendingEnd, func(s *entity.Subscription) {
		s.Status, s.AutoRenew, s.CancelledAt = entity.SubActive, true, nil
	}, out)
}

func (m *mem) DueReminders(_ context.Context, at time.Time, window time.Duration, _ int) ([]entity.Subscription, error) {
	return m.allSubs(func(s *entity.Subscription) bool {
		return s.Status == entity.SubActive && s.AutoRenew && s.CurrentPeriodEnd.After(at) &&
			!s.CurrentPeriodEnd.After(at.Add(window)) &&
			(s.ExpiringNotifiedFor == nil || !s.ExpiringNotifiedFor.Equal(s.CurrentPeriodEnd))
	}), nil
}

func (m *mem) MarkReminded(_ context.Context, s entity.Subscription, out []repo.Outbox) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	end := s.CurrentPeriodEnd
	m.subs[s.ID].ExpiringNotifiedFor = &end
	m.outbox = append(m.outbox, out...)

	return true, nil
}

func (m *mem) DueExpiry(_ context.Context, at time.Time, _ int) ([]entity.Subscription, error) {
	return m.allSubs(func(s *entity.Subscription) bool {
		return s.Status != entity.SubExpired && !s.CurrentPeriodEnd.After(at)
	}), nil
}

func (m *mem) Expire(_ context.Context, s entity.Subscription, out []repo.Outbox) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.subs[s.ID]
	if !cur.CurrentPeriodEnd.Equal(s.CurrentPeriodEnd) || cur.Status == entity.SubExpired {
		return false, nil
	}

	reason := "PERIOD_ENDED"
	cur.Status, cur.ExpiredReason = entity.SubExpired, &reason

	m.outbox = append(m.outbox, out...)

	return true, nil
}

func (m *mem) Live(_ context.Context, _ *string, after string, at time.Time, _ int) ([]entity.Subscription, error) {
	if after != "" {
		return nil, nil
	}

	return m.allSubs(func(s *entity.Subscription) bool { return s.Live(at) }), nil
}

func (m *mem) CountActive(_ context.Context, at time.Time) (map[int]int, error) {
	return m.ActiveSubscribers(context.Background(), channelID, at)
}

// ---- LedgerRepo / PayoutRepo / AuditRepo / TransactionRepo ----

func (m *mem) Totals(_ context.Context, ch string, _, _ time.Time) (entity.Totals, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var t entity.Totals

	for _, e := range m.ledger {
		sign := int64(1)
		if e.Direction == entity.Debit {
			sign = -1
		}

		switch {
		case e.ChannelID != ch:
		case e.Kind == entity.LedgerSubRevenue:
			t.Gross += sign * e.Amount
		case e.Kind == entity.LedgerPlatformFee:
			t.PlatformFee -= sign * e.Amount
		}
	}

	return t, nil
}

func (m *mem) Balance(_ context.Context, ch string) (bal, pending int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, e := range m.ledger {
		if e.ChannelID == ch {
			if e.Direction == entity.Credit {
				bal += e.Amount
			} else {
				bal -= e.Amount
			}
		}
	}

	for _, p := range m.payouts {
		if p.ChannelID == ch && p.Status == entity.PayoutRequested {
			pending += p.Amount
		}
	}

	return bal, pending, nil
}

func (m *mem) Entries(context.Context, string, *repo.Cursor, int) ([]entity.LedgerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.ledger), nil
}

func (m *mem) CreatePayout(ctx context.Context, p entity.Payout, out func(entity.Payout) []repo.Outbox) (entity.Payout, error) {
	bal, pending, _ := m.Balance(ctx, p.ChannelID) //nolint:errcheck // never fails

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, q := range m.payouts {
		if q.ChannelID == p.ChannelID && q.Status == entity.PayoutRequested {
			return p, entity.StateError(entity.ReasonPayoutPending)
		}
	}

	if bal-pending < p.Amount {
		return p, entity.StateError(entity.ReasonInsufficientBalance, "available", fmt.Sprint(bal-pending))
	}

	p.ID, p.Status, p.CreatedAt = m.id(), entity.PayoutRequested, now
	p.Number = int64(len(m.payouts) + 1)
	m.payouts[p.ID] = &p
	m.outbox = append(m.outbox, out(p)...)

	return p, nil
}

func (m *mem) ForChannel(context.Context, string, *repo.Cursor, int) ([]entity.Payout, error) {
	return m.ListPayouts(context.Background(), nil, nil, 0)
}

func (m *mem) ListPayouts(_ context.Context, st *entity.PayoutStatus, _ *repo.Cursor, _ int) ([]entity.Payout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []entity.Payout

	for _, p := range m.payouts {
		if st == nil || p.Status == *st {
			out = append(out, *p)
		}
	}

	return out, nil
}

func (m *mem) Payout(_ context.Context, id string) (entity.Payout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.payouts[id]
	if !ok {
		return entity.Payout{}, entity.ErrNotFound
	}

	return *p, nil
}

func (m *mem) Review(_ context.Context, id, admin string, to entity.PayoutStatus, reason *string, at time.Time,
	out func(entity.Payout) []repo.Outbox,
) (entity.Payout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.payouts[id]
	if !ok {
		return entity.Payout{}, entity.ErrNotFound
	}

	if p.Status != entity.PayoutRequested {
		return *p, entity.ErrInvalidState
	}

	p.Status, p.ReviewedBy, p.ReviewedAt, p.RejectReason = to, &admin, &at, reason
	if to == entity.PayoutApproved {
		m.ledger = append(m.ledger, entity.LedgerEntry{
			ChannelID: p.ChannelID, PayoutID: &p.ID, Direction: entity.Debit,
			Kind: entity.LedgerPayout, Amount: p.Amount,
		})
	}

	m.outbox = append(m.outbox, out(*p)...)

	return *p, nil
}

func (m *mem) CountRequested(context.Context) (int, error) {
	l, _ := m.ListPayouts(context.Background(), ptr(entity.PayoutRequested), nil, 0) //nolint:errcheck // never fails

	return len(l), nil
}

func (m *mem) Record(_ context.Context, rows []repo.Outbox) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil

		return err
	}

	m.outbox = append(m.outbox, rows...)

	return nil
}

func (m *mem) Transaction(context.Context, string) (entity.Transaction, error) {
	return entity.Transaction{}, entity.ErrNotFound
}

func (m *mem) ChargeOf(context.Context, string) (entity.Transaction, error) {
	return entity.Transaction{}, entity.ErrNotFound
}

// ---- JobLock / SyncLimiter / Cipher ----

func (m *mem) TryLock(context.Context, string, time.Duration) (release func(), ok bool, err error) {
	return func() {}, true, nil
}
func (m *mem) LastRun(context.Context, string) (string, error)  { return "", nil }
func (m *mem) SetLastRun(context.Context, string, string) error { return nil }

func (m *mem) Hit(context.Context, string) (int, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.syncHits++

	return m.syncHits, 42 * time.Second, nil
}

// plainCipher prefixes the AAD so a wrong AAD fails like GCM would.
type plainCipher struct{}

func (plainCipher) Seal(plain, aad []byte) ([]byte, error) {
	return append(slices.Clone(aad), plain...), nil
}

func (plainCipher) Open(blob, aad []byte) ([]byte, error) {
	if len(blob) < len(aad) || !bytes.Equal(blob[:len(aad)], aad) {
		return nil, entity.ErrDecrypt
	}

	return blob[len(aad):], nil
}

// ---- provider ----

type fakePSP struct {
	mu        sync.Mutex
	state     map[string]provider.OrderState
	queryErr  error
	listed    []provider.OrderState
	cancelled []string
}

func (*fakePSP) Name() string { return provider.NameSePay }

func (*fakePSP) BuildCheckout(_ context.Context, o provider.CheckoutOrder) (entity.Checkout, error) {
	return entity.Checkout{
		ActionURL: "https://pay-sandbox.sepay.vn/v1/checkout/init", Method: http.MethodPost,
		Fields: sepay.SignedFields("secret", sepay.FormInput{Amount: o.Amount, Invoice: o.Invoice, SuccessURL: o.SuccessURL}),
	}, nil
}

func (*fakePSP) ParseNotification(h http.Header, body []byte) (provider.Notification, error) {
	return sepay.ParseIPN(h, body, ipnSecret)
}

func (f *fakePSP) QueryOrder(_ context.Context, inv string) (provider.OrderState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.queryErr != nil {
		return provider.OrderState{}, f.queryErr
	}

	st, ok := f.state[inv]
	if !ok {
		return st, entity.ErrProviderNotFound
	}

	return st, nil
}

func (f *fakePSP) ListOrders(context.Context, time.Time, time.Time) ([]provider.OrderState, error) {
	return f.listed, f.queryErr
}

func (f *fakePSP) CancelOrder(_ context.Context, inv string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.cancelled = append(f.cancelled, inv)

	return nil
}

// ---- observer ----

type obs struct {
	mu  sync.Mutex
	ipn []usecase.IPNResult
}

func (o *obs) IPN(_ string, r usecase.IPNResult, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.ipn = append(o.ipn, r)
}
func (*obs) OrderStatus(entity.OrderStatus, string, string) {}
func (*obs) ReconcileRun(string, string)                    {}
func (*obs) ReconcileMismatch(string)                       {}
func (*obs) NeedsReview(int)                                {}
func (*obs) ActiveSubscriptions(map[int]int)                {}
func (*obs) Voided()                                        {}
func (*obs) PayoutStatus(entity.PayoutStatus)               {}
func (*obs) Idempotency(string)                             {}

type env struct {
	m   *mem
	psp *fakePSP
	uc  *billing.UseCase
}

type settings = billing.Settings

func newEnv() env { return newEnvWith(nil) }

func newEnvWith(mod func(*settings), opts ...billing.Option) env {
	m, psp := newMem(), &fakePSP{state: map[string]provider.OrderState{}}
	s := billing.Settings{
		TierPrices: [3]int64{59000, 119000, 299000}, FeeBps: 500, PayoutMin: 100000, OrderTTL: 30 * time.Minute,
		ReconcileAfter: 2 * time.Minute, ReconcileInterval: time.Minute, ReconcileGiveUp: 30 * time.Minute,
		RenewReminderDays: 3, Methods: []entity.PaymentMethod{entity.MethodBankTransfer, entity.MethodCard},
		WebBaseURL: "http://localhost:5173/",
	}

	if mod != nil {
		mod(&s)
	}

	opts = append([]billing.Option{billing.WithClock(func() time.Time { return now }), billing.WithRand(zeroReader{})}, opts...)
	uc := billing.New(billing.Repos{
		Channels: m, Tiers: m, Orders: m, Subs: m, Ledger: m, Payouts: m, Audit: m, Txns: m, Lock: m, Sync: m,
		Cipher: plainCipher{}, Snapshot: m,
	}, psp, s, logger.New("error"), &obs{}, opts...)

	return env{m: m, psp: psp, uc: uc}
}

func ptr[T any](v T) *T { return &v }
