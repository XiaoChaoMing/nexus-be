package entity

import (
	"strconv"
	"strings"
	"time"
)

// Sub tiers (NXL-H): 1..3.
const (
	MinSubTier = 1
	MaxSubTier = 3
	// SubPeriod is billing-svc's period (SUB_PERIOD_DAYS): one paid order adds or voids exactly this much.
	SubPeriod = 30 * 24 * time.Hour
)

// SubGrant is the copy of a subscription in sub:{channelId}: the tier and the end of the paid period.
// A zero Until is a legacy "{tier}" value written before periods existed: it never expires.
type SubGrant struct {
	Tier  int
	Until time.Time
}

// ParseSub reads "{tier}" or "{tier}:{untilUnix}"; anything else is the zero grant (not subscribed).
func ParseSub(v string) (SubGrant, bool) {
	tierPart, untilPart, hasUntil := strings.Cut(v, ":")

	tier, err := strconv.Atoi(tierPart)
	if err != nil || tier < MinSubTier || tier > MaxSubTier {
		return SubGrant{}, false
	}

	if !hasUntil {
		return SubGrant{Tier: tier}, true
	}

	secs, err := strconv.ParseInt(untilPart, 10, 64)
	if err != nil || secs <= 0 {
		return SubGrant{}, false
	}

	return SubGrant{Tier: tier, Until: time.Unix(secs, 0).UTC()}, true
}

// Active reports a tier 1..3 whose period has not ended (BR-H13: until ≤ now is no longer subscribed).
func (g SubGrant) Active(now time.Time) bool {
	return g.Tier >= MinSubTier && g.Tier <= MaxSubTier && (g.Until.IsZero() || g.Until.After(now))
}

// TierAt is the tier in force at now (0 when none).
func (g SubGrant) TierAt(now time.Time) int {
	if g.Active(now) {
		return g.Tier
	}

	return 0
}

// Encode is the stored form "{tier}:{untilUnix}".
func (g SubGrant) Encode() string {
	return strconv.Itoa(g.Tier) + ":" + strconv.FormatInt(g.Until.Unix(), 10)
}

// ValidSubTier -.
func ValidSubTier(tier int) bool { return tier >= MinSubTier && tier <= MaxSubTier }

// System message kinds of a subscription (event `system`, NXL-B BR21).
const (
	SystemSubNew   = "SUB_NEW"
	SystemSubRenew = "SUB_RENEW"
)

// SubAnnouncement is the `system` payload of a new or renewed subscription.
type SubAnnouncement struct {
	Type string  `json:"type"`
	Text string  `json:"text"`
	Meta SubMeta `json:"meta"`
}

// SubMeta -. Username is empty when chat-svc has no copy of the account.
type SubMeta struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Tier     int    `json:"tier"`
}

// _unknownViewer names a subscriber chat-svc has no copy of.
const _unknownViewer = "Một người xem"

// NewSubAnnouncement builds the system message "@an vừa subscribe Tier 1" / "@an đã gia hạn Tier 1".
func NewSubAnnouncement(kind, userID, username string, tier int) SubAnnouncement {
	who := _unknownViewer
	if username != "" {
		who = "@" + username
	}

	verb := "vừa subscribe"
	if kind == SystemSubRenew {
		verb = "đã gia hạn"
	}

	return SubAnnouncement{
		Type: kind,
		Text: who + " " + verb + " Tier " + strconv.Itoa(tier),
		Meta: SubMeta{UserID: userID, Username: username, Tier: tier},
	}
}

// Payment statuses relayed in payment_updated.
const (
	PaymentPaid   = "PAID"
	PaymentVoided = "VOIDED"
)

// PaymentUpdate is the payment_updated payload on user:{userId} (checkout result page).
type PaymentUpdate struct {
	OrderID       string           `json:"orderId"`
	InvoiceNumber string           `json:"invoiceNumber"`
	Status        string           `json:"status"`
	FailureReason *string          `json:"failureReason"`
	NeedsReview   bool             `json:"needsReview"`
	Subscription  *PaymentSubState `json:"subscription"`
}

// PaymentSubState is the subscription the payment left behind.
type PaymentSubState struct {
	ID               string    `json:"id"`
	ChannelID        string    `json:"channelId"`
	Tier             int       `json:"tier"`
	Status           string    `json:"status"`
	CurrentPeriodEnd time.Time `json:"currentPeriodEnd"`
}
