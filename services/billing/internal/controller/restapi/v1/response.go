package v1

// Response bodies (TSD §5.1); fields without a value are sent as null.

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

type (
	// Data is the success envelope.
	Data[T any] struct {
		Data T `json:"data"`
	}

	// Page is a list with an opaque cursor.
	Page[T any] struct {
		Items      []T     `json:"items"`
		NextCursor *string `json:"nextCursor"`
	}

	// ErrorBody -.
	ErrorBody struct {
		Code    string            `json:"code"    example:"INVALID_STATE"`
		Message string            `json:"message" example:"invalid state"`
		Details map[string]string `json:"details,omitempty"`
	}

	// Error is the NXL-00 §4 error envelope.
	Error struct {
		Error ErrorBody `json:"error"`
	}

	// Money is an amount in VND.
	Money struct {
		Amount   int64  `json:"amount"   example:"59000"`
		Currency string `json:"currency" example:"VND"`
	}

	// ChannelRef -.
	ChannelRef struct {
		ID          string  `json:"id"`
		Handle      string  `json:"handle"`
		DisplayName string  `json:"displayName"`
		AvatarURL   *string `json:"avatarUrl"`
	}

	// TierRef -.
	TierRef struct {
		ID     string `json:"id"`
		Level  int    `json:"level"  example:"1"`
		Name   string `json:"name"   example:"Tier 1"`
		Price  Money  `json:"price"`
		Active bool   `json:"active"`
	}

	// Tier -.
	Tier struct {
		ID          string `json:"id"`
		ChannelID   string `json:"channelId"`
		Level       int    `json:"level"       example:"1"`
		Name        string `json:"name"        example:"Tier 1"`
		Price       Money  `json:"price"`
		Description string `json:"description"`
		Active      bool   `json:"active"`
		PeriodDays  int    `json:"periodDays"  example:"30"`
	}

	// StudioTier -.
	StudioTier struct {
		Tier

		ActiveSubscribers int       `json:"activeSubscribers"`
		UpdatedAt         time.Time `json:"updatedAt"`
	}

	// CheckoutConfig -.
	CheckoutConfig struct {
		Provider          string   `json:"provider"          example:"SEPAY"`
		PaymentMethods    []string `json:"paymentMethods"`
		PeriodDays        int      `json:"periodDays"        example:"30"`
		RenewReminderDays int      `json:"renewReminderDays" example:"3"`
		MaxPrepaidDays    int      `json:"maxPrepaidDays"    example:"365"`
		OrderTTLSec       int      `json:"orderTtlSec"       example:"1800"`
		MaxPending        int      `json:"maxPending"        example:"5"`
	}

	// TierChannel is the channel of GET /checkout/tiers/{tierId}.
	TierChannel struct {
		ChannelRef

		OwnerID string `json:"ownerId"`
		Status  string `json:"status" example:"ACTIVE"`
	}

	// TierContext -.
	TierContext struct {
		TierID  string      `json:"tierId"`
		Channel TierChannel `json:"channel"`
		Tiers   []Tier      `json:"tiers"`
	}

	// Order -.
	Order struct {
		ID            string      `json:"id"`
		InvoiceNumber string      `json:"invoiceNumber" example:"NXL7Q2K9M4X1C"`
		OrderType     string      `json:"orderType"     example:"SUBSCRIPTION"`
		ChannelID     *string     `json:"channelId"`
		Channel       *ChannelRef `json:"channel"`
		Tier          *TierRef    `json:"tier"`
		Kind          *string     `json:"kind"          example:"NEW"`
		Amount        Money       `json:"amount"`
		PaymentMethod *string     `json:"paymentMethod" example:"BANK_TRANSFER"`
		Status        string      `json:"status"        example:"PENDING"`
		FailureReason *string     `json:"failureReason"`
		NeedsReview   bool        `json:"needsReview"`
		PeriodDays    int         `json:"periodDays"    example:"30"`
		ExpiresAt     time.Time   `json:"expiresAt"`
		CreatedAt     time.Time   `json:"createdAt"`
		PaidAt        *time.Time  `json:"paidAt"`
		UpdatedAt     time.Time   `json:"updatedAt"`
	}

	// Field is one checkout form input.
	Field struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	// Checkout is the signed form; post the fields in this order.
	Checkout struct {
		ActionURL string  `json:"actionUrl" example:"https://pay-sandbox.sepay.vn/v1/checkout/init"`
		Method    string  `json:"method"    example:"POST"`
		Fields    []Field `json:"fields"`
	}

	// OrderCheckout is POST /checkout/orders.
	OrderCheckout struct {
		Order    Order    `json:"order"`
		Checkout Checkout `json:"checkout"`
	}

	// Sub is a subscription.
	Sub struct {
		ID                 string     `json:"id"`
		Channel            ChannelRef `json:"channel"`
		Tier               TierRef    `json:"tier"`
		Status             string     `json:"status"             example:"ACTIVE"`
		CurrentPeriodStart time.Time  `json:"currentPeriodStart"`
		CurrentPeriodEnd   time.Time  `json:"currentPeriodEnd"`
		RemindRenewal      bool       `json:"remindRenewal"`
		CancelledAt        *time.Time `json:"cancelledAt"`
		ExpiredReason      *string    `json:"expiredReason"`
		Renewable          bool       `json:"renewable"`
		CreatedAt          time.Time  `json:"createdAt"`
	}

	// OrderView is an order with its subscription.
	OrderView struct {
		Order        Order `json:"order"`
		Subscription *Sub  `json:"subscription"`
	}

	// OrderOnly is POST /checkout/orders/{id}/cancel.
	OrderOnly struct {
		Order Order `json:"order"`
	}

	// LedgerEntry -.
	LedgerEntry struct {
		ID            string    `json:"id"`
		CreatedAt     time.Time `json:"createdAt"`
		Kind          string    `json:"kind"          example:"SUB_REVENUE"`
		Direction     string    `json:"direction"     example:"CREDIT"`
		Amount        Money     `json:"amount"`
		Source        string    `json:"source"        example:"CHARGE"`
		InvoiceNumber *string   `json:"invoiceNumber"`
		PayoutID      *string   `json:"payoutId"`
		PayoutCode    *string   `json:"payoutCode"`
	}

	// Bank is the bank of a payout.
	Bank struct {
		Code        string  `json:"code"        example:"VCB"`
		Name        string  `json:"name"        example:"Vietcombank"`
		Last4       string  `json:"last4"       example:"6789"`
		AccountName *string `json:"accountName" example:"NGUYEN VAN AN"`
	}

	// Payout -.
	Payout struct {
		ID            string     `json:"id"`
		Code          string     `json:"code"         example:"PO-0001"`
		Amount        Money      `json:"amount"`
		Bank          Bank       `json:"bank"`
		Status        string     `json:"status"        example:"PROCESSING"`
		RejectReason  *string    `json:"rejectReason"`
		CreatedAt     time.Time  `json:"createdAt"`
		ReviewedAt    *time.Time `json:"reviewedAt"`
		Provider      *string    `json:"provider"      example:"MANUAL"`
		ProviderRef   *string    `json:"providerRef"   example:"FT24123456789"`
		FailureReason *string    `json:"failureReason"`
		PaidAt        *time.Time `json:"paidAt"`
		SettledAt     *time.Time `json:"settledAt"`
	}

	// PayoutChannel -.
	PayoutChannel struct {
		ChannelRef

		OwnerID string `json:"ownerId"`
	}

	// Ref is a user reference.
	Ref struct {
		ID string `json:"id"`
	}

	// AdminPayout -.
	AdminPayout struct {
		Payout

		Channel    PayoutChannel `json:"channel"`
		Balance    Money         `json:"balance"`
		Available  Money         `json:"available"`
		ReviewedBy *Ref          `json:"reviewedBy"`
		SettledBy  *Ref          `json:"settledBy"`
	}

	// PendingCount -.
	PendingCount struct {
		Requested int `json:"requested"`
	}

	// BankAccount is the decrypted account (admin, audited).
	BankAccount struct {
		BankCode      string `json:"bankCode"      example:"VCB"`
		BankName      string `json:"bankName"      example:"Vietcombank"`
		AccountNumber string `json:"accountNumber" example:"0123456789"`
		AccountName   string `json:"accountName"   example:"NGUYEN VAN AN"`
	}

	// AdminOrder -.
	AdminOrder struct {
		Order

		UserID         string  `json:"userId"`
		SubscriptionID *string `json:"subscriptionId"`
		PSPRef         *string `json:"pspRef"`
	}

	// Subscribers counts live subscriptions per tier.
	Subscribers struct {
		Tier1 int `json:"tier1"`
		Tier2 int `json:"tier2"`
		Tier3 int `json:"tier3"`
		Total int `json:"total"`
	}

	// Summary is the Studio monetization overview.
	Summary struct {
		Range         string      `json:"range" example:"7d"`
		TZ            string      `json:"tz"    example:"Asia/Ho_Chi_Minh"`
		From          time.Time   `json:"from"`
		To            time.Time   `json:"to"`
		Subscribers   Subscribers `json:"subscribers"`
		Gross         Money       `json:"gross"`
		PlatformFee   Money       `json:"platformFee"`
		Net           Money       `json:"net"`
		TodayNet      Money       `json:"todayNet"`
		Balance       Money       `json:"balance"`
		Available     Money       `json:"available"`
		PendingPayout Money       `json:"pendingPayout"`
		Gifts         Money       `json:"gifts"`
		FeeBps        int         `json:"feeBps"   example:"500"`
		PayoutMin     Money       `json:"payoutMin"`
	}
)

// VND -.
func VND(amount int64) Money { return Money{Amount: amount, Currency: entity.Currency} }

func channelRef(c entity.Channel) ChannelRef {
	return ChannelRef{ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL}
}

func tierRef(t entity.Tier) TierRef {
	return TierRef{ID: t.ID, Level: t.Level, Name: t.Name(), Price: VND(t.Price), Active: t.Active}
}

// NewTier -.
func NewTier(t entity.Tier) Tier {
	return Tier{
		ID: t.ID, ChannelID: t.ChannelID, Level: t.Level, Name: t.Name(), Price: VND(t.Price), Description: t.Description,
		Active: t.Active, PeriodDays: entity.SubPeriodDays,
	}
}

// NewTiers -.
func NewTiers(ts []entity.Tier) []Tier {
	out := make([]Tier, 0, len(ts))
	for _, t := range ts {
		out = append(out, NewTier(t))
	}

	return out
}

// NewStudioTier -.
func NewStudioTier(t usecase.StudioTier) StudioTier {
	return StudioTier{Tier: NewTier(t.Tier), ActiveSubscribers: t.ActiveSubscribers, UpdatedAt: t.Tier.UpdatedAt}
}

// NewCheckoutConfig -.
func NewCheckoutConfig(c usecase.CheckoutConfig) CheckoutConfig {
	methods := make([]string, 0, len(c.PaymentMethods))
	for _, m := range c.PaymentMethods {
		methods = append(methods, string(m))
	}

	return CheckoutConfig{
		Provider: c.Provider, PaymentMethods: methods, PeriodDays: c.PeriodDays, RenewReminderDays: c.RenewReminderDays,
		MaxPrepaidDays: c.MaxPrepaidDays, OrderTTLSec: int(c.OrderTTL.Seconds()), MaxPending: c.MaxPending,
	}
}

// NewTierContext -.
func NewTierContext(tierID string, c entity.Channel, ts []entity.Tier) TierContext {
	return TierContext{
		TierID: tierID, Channel: TierChannel{ChannelRef: channelRef(c), OwnerID: c.OwnerID, Status: c.Status},
		Tiers: NewTiers(ts),
	}
}

func str[T ~string](v *T) *string {
	if v == nil {
		return nil
	}

	s := string(*v)

	return &s
}

// NewOrder -.
// NewOrder -: a top-up has no channel, tier or kind (null).
func NewOrder(o entity.Order) Order {
	out := Order{
		ID: o.ID, InvoiceNumber: o.InvoiceNumber, OrderType: string(o.Type()), Amount: VND(o.Amount),
		PaymentMethod: str(o.PaymentMethod), Status: string(o.Status), FailureReason: str(o.FailureReason),
		NeedsReview: o.NeedsReview, PeriodDays: o.PeriodDays, ExpiresAt: o.ExpiresAt, CreatedAt: o.CreatedAt,
		PaidAt: o.PaidAt, UpdatedAt: o.UpdatedAt,
	}

	if o.Type() == entity.OrderTypeSubscription {
		ch, tier, kind := channelRef(o.Channel), tierRef(o.Tier), string(o.SubAction)
		out.ChannelID, out.Channel, out.Tier, out.Kind = &o.ChannelID, &ch, &tier, &kind
	}

	return out
}

// NewCheckout -.
func NewCheckout(c entity.Checkout) Checkout {
	fields := make([]Field, 0, len(c.Fields))
	for _, f := range c.Fields {
		fields = append(fields, Field{Name: f.Name, Value: f.Value})
	}

	return Checkout{ActionURL: c.ActionURL, Method: c.Method, Fields: fields}
}

// NewSub -.
func NewSub(s entity.Subscription, now time.Time) Sub {
	return Sub{
		ID: s.ID, Channel: channelRef(s.Channel), Tier: tierRef(s.Tier), Status: string(s.Status),
		CurrentPeriodStart: s.CurrentPeriodStart, CurrentPeriodEnd: s.CurrentPeriodEnd, RemindRenewal: s.AutoRenew,
		CancelledAt: s.CancelledAt, ExpiredReason: s.ExpiredReason, Renewable: s.Renewable(now), CreatedAt: s.CreatedAt,
	}
}

// NewSubs -.
func NewSubs(ss []entity.Subscription, now time.Time) []Sub {
	out := make([]Sub, 0, len(ss))
	for _, s := range ss {
		out = append(out, NewSub(s, now))
	}

	return out
}

// NewOrderView -.
func NewOrderView(v usecase.OrderView, now time.Time) OrderView {
	out := OrderView{Order: NewOrder(v.Order)}
	if v.Subscription != nil {
		s := NewSub(*v.Subscription, now)
		out.Subscription = &s
	}

	return out
}

// NewOrders -.
func NewOrders(os []entity.Order) []Order {
	out := make([]Order, 0, len(os))
	for _, o := range os {
		out = append(out, NewOrder(o))
	}

	return out
}

// NewAdminOrder -.
func NewAdminOrder(o entity.Order) AdminOrder {
	return AdminOrder{Order: NewOrder(o), UserID: o.UserID, SubscriptionID: o.SubscriptionID, PSPRef: o.PSPRef}
}

// NewAdminOrders -.
func NewAdminOrders(os []entity.Order) []AdminOrder {
	out := make([]AdminOrder, 0, len(os))
	for _, o := range os {
		out = append(out, NewAdminOrder(o))
	}

	return out
}

// NewLedger -.
func NewLedger(es []entity.LedgerEntry) []LedgerEntry {
	out := make([]LedgerEntry, 0, len(es))

	for _, e := range es {
		var code *string

		if e.PayoutNumber != nil {
			c := entity.PayoutCode(*e.PayoutNumber)
			code = &c
		}

		out = append(out, LedgerEntry{
			ID: e.ID, CreatedAt: e.CreatedAt, Kind: string(e.Kind), Direction: string(e.Direction), Amount: VND(e.Amount),
			Source: e.Source, InvoiceNumber: e.InvoiceNumber, PayoutID: e.PayoutID, PayoutCode: code,
		})
	}

	return out
}

// NewPayout -.
func NewPayout(v usecase.PayoutView) Payout {
	p := v.Payout

	return Payout{
		ID: p.ID, Code: p.Code(), Amount: VND(p.Amount), Status: string(p.Status), RejectReason: p.RejectReason,
		CreatedAt: p.CreatedAt, ReviewedAt: p.ReviewedAt, Provider: p.Provider, ProviderRef: p.ProviderRef,
		FailureReason: p.FailureReason, PaidAt: p.PaidAt, SettledAt: p.SettledAt,
		Bank: Bank{Code: p.BankCode, Name: entity.BankName(p.BankCode), Last4: p.AccountLast4, AccountName: v.AccountName},
	}
}

// NewPayouts -.
func NewPayouts(vs []usecase.PayoutView) []Payout {
	out := make([]Payout, 0, len(vs))
	for _, v := range vs {
		out = append(out, NewPayout(v))
	}

	return out
}

// NewAdminPayout -.
func NewAdminPayout(a usecase.AdminPayout) AdminPayout {
	out := AdminPayout{
		Payout: NewPayout(a.PayoutView), Channel: PayoutChannel{ChannelRef: channelRef(a.Channel), OwnerID: a.Channel.OwnerID},
		Balance: VND(a.Balance), Available: VND(a.Available),
	}

	if a.Payout.ReviewedBy != nil {
		out.ReviewedBy = &Ref{ID: *a.Payout.ReviewedBy}
	}

	if a.Payout.SettledBy != nil {
		out.SettledBy = &Ref{ID: *a.Payout.SettledBy}
	}

	return out
}

// NewAdminPayouts -.
func NewAdminPayouts(as []usecase.AdminPayout) []AdminPayout {
	out := make([]AdminPayout, 0, len(as))
	for _, a := range as {
		out = append(out, NewAdminPayout(a))
	}

	return out
}

// NewBankAccount -.
func NewBankAccount(a entity.BankAccount) BankAccount {
	return BankAccount{BankCode: a.Code, BankName: entity.BankName(a.Code), AccountNumber: a.Number, AccountName: a.Name}
}

// NewSummary -.
func NewSummary(s entity.Summary) Summary {
	subs := Subscribers{Tier1: s.Subscribers[1], Tier2: s.Subscribers[2], Tier3: s.Subscribers[3]}
	subs.Total = subs.Tier1 + subs.Tier2 + subs.Tier3

	return Summary{
		Range: s.Range, TZ: s.TZ, From: s.From, To: s.To, Subscribers: subs, Gross: VND(s.Gross),
		PlatformFee: VND(s.PlatformFee), Net: VND(s.Net), TodayNet: VND(s.TodayNet), Balance: VND(s.Balance),
		Available: VND(s.Available), PendingPayout: VND(s.PendingPayout), FeeBps: s.FeeBps, PayoutMin: VND(s.PayoutMin),
		Gifts: VND(s.Gifts),
	}
}
