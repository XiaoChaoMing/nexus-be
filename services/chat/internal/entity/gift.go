package entity

import (
	"strconv"
	"strings"
)

// SystemGift is the system message kind of a gift (event `system`).
const SystemGift = "GIFT"

// GiftAnnouncement is the `system` payload of a gift.
type GiftAnnouncement struct {
	Type string   `json:"type"`
	Text string   `json:"text"`
	Meta GiftMeta `json:"meta"`
}

// GiftMeta -. Message is nil when there was none or the channel's AutoMod refused it.
type GiftMeta struct {
	UserID   string  `json:"userId"`
	Username string  `json:"username"`
	ItemName string  `json:"itemName"`
	Icon     string  `json:"icon"`
	Quantity int     `json:"quantity"`
	Total    int64   `json:"total"`
	Message  *string `json:"message"`
}

// NewGiftAnnouncement builds "@an tặng 5 × Hoa hồng (50.000 ₫): Chúc stream vui!".
func NewGiftAnnouncement(m GiftMeta) GiftAnnouncement {
	who := _unknownViewer
	if m.Username != "" {
		who = "@" + m.Username
	}

	text := who + " tặng " + strconv.Itoa(m.Quantity) + " × " + m.ItemName + " (" + formatVND(m.Total) + ")"
	if m.Message != nil {
		text += ": " + *m.Message
	}

	return GiftAnnouncement{Type: SystemGift, Text: text, Meta: m}
}

// formatVND renders "50.000 ₫".
func formatVND(n int64) string {
	const group = 3

	digits := strconv.FormatInt(n, 10)

	var b strings.Builder

	for i, d := range digits {
		if i > 0 && (len(digits)-i)%group == 0 {
			b.WriteByte('.')
		}

		b.WriteRune(d)
	}

	return b.String() + " ₫"
}
