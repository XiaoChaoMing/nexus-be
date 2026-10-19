package entity

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Fixed values of the billing rules (NXL-00 §12).
const (
	SubPeriodDays      = 30
	MaxPrepaidDays     = 365
	MaxPendingOrders   = 5
	TierDescriptionMax = 200
	PayoutMax          = 1_000_000_000
	AdminReasonMin     = 10
	AdminReasonMax     = 500
	SyncPerMinute      = 10
	WebhookBodyMax     = 64 << 10
	IPNMaxAge          = 7 * 24 * time.Hour
	IdempotencyTTL     = 24 * time.Hour
	IdempotencyStale   = 60 * time.Second
	TierLevels         = 3

	_bpsDenominator = 10_000
	_hoursPerDay    = 24
	_invoicePrefix  = "NXL"
	_invoiceLen     = 10
	_crockford      = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	_crockfordMask  = 31
	_accountNameMin = 2
	_accountNameMax = 100
	_descriptionMax = 100
	_asciiFirst     = 0x20
	_asciiLast      = 0x7e
)

// Days is n periods of 24 h (BR-H06: periods are counted in seconds, not calendar days).
func Days(n int) time.Duration { return time.Duration(n) * _hoursPerDay * time.Hour }

// Fee is the platform fee of amount at bps basis points, rounded down.
func Fee(amount int64, bps int) int64 { return amount * int64(bps) / _bpsDenominator }

// RenewedEnd is the end of a live subscription after one more paid period (AC-H15).
func RenewedEnd(end, paidAt time.Time, period time.Duration) time.Time {
	if paidAt.After(end) {
		end = paidAt
	}

	return end.Add(period)
}

// NewInvoiceNumber returns "NXL" + 10 Crockford base32 characters read from r (crypto/rand in production).
func NewInvoiceNumber(r io.Reader) (string, error) {
	b := make([]byte, _invoiceLen)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", fmt.Errorf("invoice number: %w", err)
	}

	out := make([]byte, 0, len(_invoicePrefix)+_invoiceLen)
	out = append(out, _invoicePrefix...)

	for _, c := range b {
		out = append(out, _crockford[c&_crockfordMask])
	}

	return string(out), nil
}

// Bank is one of the banks a payout can go to.
type Bank struct {
	Code string
	Name string
}

// Banks lists the payout banks in display order (G6-Q10).
func Banks() []Bank {
	return []Bank{
		{"VCB", "Vietcombank"},
		{"TCB", "Techcombank"},
		{"BIDV", "BIDV"},
		{"VTB", "VietinBank"},
		{"ACB", "ACB"},
		{"MB", "MB Bank"},
		{"VPB", "VPBank"},
		{"TPB", "TPBank"},
		{"STB", "Sacombank"},
		{"VIB", "VIB"},
		{"SHB", "SHB"},
		{"HDB", "HDBank"},
	}
}

// BankName returns the name of a bank code, or "" for an unknown one.
func BankName(code string) string {
	for _, b := range Banks() {
		if b.Code == code {
			return b.Name
		}
	}

	return ""
}

var (
	_accountNumberRe = regexp.MustCompile(`^\d{6,20}$`)
	_accountNameRe   = regexp.MustCompile(`^[A-Z ]+$`)
)

// PayoutRequest is a validated payout input.
type PayoutRequest struct {
	Amount  int64
	Account BankAccount
}

// NewPayoutRequest validates the Studio payout form (§5.2 field rules) and normalizes the account name.
func NewPayoutRequest(amount *int64, bankCode, accountNumber, accountName string, minAmount int64) (PayoutRequest, error) {
	details := map[string]string{}

	switch {
	case amount == nil:
		details["amount"] = DetailRequired
	case *amount < minAmount:
		details["amount"] = DetailMin
	case *amount > PayoutMax:
		details["amount"] = DetailMax
	}

	if BankName(bankCode) == "" {
		details["bankCode"] = DetailEnum
	}

	if !_accountNumberRe.MatchString(accountNumber) {
		details["accountNumber"] = DetailFormat
	}

	name := strings.ToUpper(strings.Join(strings.Fields(accountName), " "))

	switch n := utf8.RuneCountInString(name); {
	case n < _accountNameMin || n > _accountNameMax:
		details["accountName"] = DetailLength
	case !_accountNameRe.MatchString(name):
		details["accountName"] = DetailFormat
	}

	if len(details) > 0 {
		return PayoutRequest{}, &RejectError{Err: ErrValidation, Details: details}
	}

	return PayoutRequest{Amount: *amount, Account: BankAccount{Code: bankCode, Number: accountNumber, Name: name}}, nil
}

// Last4 is the last four digits of an account number.
func (a BankAccount) Last4() string {
	const n = 4

	if len(a.Number) < n {
		return a.Number
	}

	return a.Number[len(a.Number)-n:]
}

// AdminReason trims and checks an admin reason (10–500 characters).
func AdminReason(s string) (string, error) {
	s = strings.TrimSpace(s)

	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "", Invalid("reason", DetailRequired)
	case n < AdminReasonMin || n > AdminReasonMax:
		return "", Invalid("reason", DetailLength)
	}

	return s, nil
}

// TierDescription trims and checks a tier description (≤ 200 characters, empty allowed).
func TierDescription(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > TierDescriptionMax {
		return "", Invalid("description", DetailLength)
	}

	return s, nil
}

// DefaultTierDescription is the description a new channel's tier gets (§6.3).
func DefaultTierDescription(level int) string {
	switch level {
	case 1:
		return "Badge Sub T1, emote kênh Tier 1, chat khi bật chế độ chỉ Subscriber."
	case 2: //nolint:mnd // tier level
		return "Mọi quyền lợi Tier 1, badge Sub T2 và thêm emote Tier 2."
	}

	return "Mọi quyền lợi Tier 2, badge Sub T3 và emote Tier 3 độc quyền."
}

// OrderDescription is the order_description of a subscription order: printable ASCII only (the provider
// page rejects other bytes), at most 100 characters.
func OrderDescription(level int, handle string, periodDays int) string {
	s := fmt.Sprintf("NexusLive Sub Tier %d @%s %d ngay", level, handle, periodDays)

	var b strings.Builder

	for _, r := range s {
		if r >= _asciiFirst && r <= _asciiLast {
			b.WriteRune(r)
		}
	}

	out := b.String()
	if len(out) > _descriptionMax {
		out = out[:_descriptionMax]
	}

	return out
}
