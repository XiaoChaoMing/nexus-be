package entity

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// WalletKind of a wallet entry.
type WalletKind string

// Wallet entry kinds.
const (
	WalletTopup     WalletKind = "TOPUP"
	WalletTopupVoid WalletKind = "TOPUP_VOID"
	WalletGiftSpend WalletKind = "GIFT_SPEND"
)

// Gift rules (DL-24).
const (
	GiftNameMax     = 40
	GiftPriceMin    = 1_000
	GiftPriceMax    = 5_000_000
	GiftQuantityMax = 99
	GiftMessageMax  = 200
	GiftSortMax     = 9999
)

// Reasons of INVALID_STATE for wallets and gifts.
const (
	ReasonInsufficientFunds = "INSUFFICIENT_FUNDS"
	ReasonGiftInactive      = "GIFT_INACTIVE"
)

// ErrGiftSelf -.
var ErrGiftSelf = errors.New("cannot gift own channel")

// WalletEntry is an append-only movement of a user's wallet; exactly one of TransactionID and GiftSendID is set.
type WalletEntry struct {
	ID            string
	UserID        string
	Direction     Direction
	Kind          WalletKind
	Amount        int64
	TransactionID *string
	GiftSendID    *string
	CreatedAt     time.Time

	// Read-side joins.
	InvoiceNumber *string
	Gift          *GiftSend
}

// GiftItem is an entry of the gift catalog.
type GiftItem struct {
	ID        string
	Name      string
	Price     int64
	Icon      string
	Active    bool
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GiftSend is one gift; the item is copied so later catalog edits do not change it.
type GiftSend struct {
	ID        string
	UserID    string
	ChannelID string
	ItemID    string
	ItemName  string
	Icon      string
	UnitPrice int64
	Quantity  int
	Total     int64
	Fee       int64
	Message   *string
	CreatedAt time.Time

	Channel Channel
}

// GiftIcons are the icon keys the web renders; a catalog item uses one of them.
func GiftIcons() []string {
	return []string{"heart", "rose", "star", "fire", "clap", "cake", "diamond", "crown", "trophy", "rocket", "car", "castle"}
}

// NewTopup checks a top-up amount.
func NewTopup(amount *int64, minAmount, maxAmount int64) (int64, error) {
	switch {
	case amount == nil:
		return 0, Invalid("amount", DetailRequired)
	case *amount < minAmount:
		return 0, Invalid("amount", DetailMin)
	case *amount > maxAmount:
		return 0, Invalid("amount", DetailMax)
	}

	return *amount, nil
}

// GiftItemName trims and checks a catalog item name (1–40 characters).
func GiftItemName(s string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")

	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "", Invalid("name", DetailRequired)
	case n > GiftNameMax:
		return "", Invalid("name", DetailLength)
	}

	return s, nil
}

// GiftItemPrice checks a catalog price.
func GiftItemPrice(p *int64) (int64, error) {
	switch {
	case p == nil:
		return 0, Invalid("price", DetailRequired)
	case *p < GiftPriceMin:
		return 0, Invalid("price", DetailMin)
	case *p > GiftPriceMax:
		return 0, Invalid("price", DetailMax)
	}

	return *p, nil
}

// GiftItemIcon checks an icon key.
func GiftItemIcon(s string) (string, error) {
	if !slices.Contains(GiftIcons(), s) {
		return "", Invalid("icon", DetailEnum)
	}

	return s, nil
}

// GiftMessage trims a gift message (≤ 200 characters, no control characters); empty is nil.
func GiftMessage(s string) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	if utf8.RuneCountInString(s) > GiftMessageMax {
		return nil, Invalid("message", DetailLength)
	}

	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return nil, Invalid("message", DetailFormat)
	}

	return &s, nil
}

// GiftQuantity checks a quantity (1–99) and the total against maxTotal.
func GiftQuantity(q *int64, unitPrice, maxTotal int64) (int, error) {
	switch {
	case q == nil:
		return 0, Invalid("quantity", DetailRequired)
	case *q < 1:
		return 0, Invalid("quantity", DetailMin)
	case *q > GiftQuantityMax || unitPrice**q > maxTotal:
		return 0, Invalid("quantity", DetailMax)
	}

	return int(*q), nil
}
