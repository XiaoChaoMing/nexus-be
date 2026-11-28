package entity

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// Nickname length of a guest, in runes.
const (
	GuestNameMinRunes = 3
	GuestNameMaxRunes = 20
)

// Detail values of a refused nickname: taken by an account, the channel or a reserved name; or blocked (term, link).
const (
	DetailTaken   = "TAKEN"
	DetailBlocked = "BLOCKED"
)

const (
	_guestKeyLabel = "nxl-guest-chat:v1:"
	// UUID layout: version 8 (custom) in the high nibble of byte 6, RFC 4122 variant in the top bits of byte 8.
	_uuidVersionByte, _uuidVersionMask, _uuidVersion8 = 6, 0x0f, 0x80
	_uuidVariantByte, _uuidVariantMask, _uuidVariant  = 8, 0x3f, 0x80
)

// GuestKey derives a guest's public id from the browser's anonId: HMAC-SHA256(secret, "nxl-guest-chat:v1:"+anonID),
// truncated to 128 bits and laid out as a UUID (version 8). The fixed label keeps this use of the secret separate
// from its other uses. The client cannot choose it, so it never equals a users.id.
func GuestKey(secret []byte, anonID string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(_guestKeyLabel + anonID)) // hash.Hash never returns an error

	var u uuid.UUID

	copy(u[:], mac.Sum(nil))
	u[_uuidVersionByte] = u[_uuidVersionByte]&_uuidVersionMask | _uuidVersion8
	u[_uuidVariantByte] = u[_uuidVariantByte]&_uuidVariantMask | _uuidVariant

	return u.String()
}

// reservedGuestNames are refused as whole words of a nickname (normalized form), so a guest cannot pass for staff.
func reservedGuestNames() []string {
	return []string{
		"admin", "administrator", "mod", "moderator", "nexuslive", "nexus live", "streamer", "system", "support", "guest",
		"khach", "he thong", "quan tri", "quan tri vien", "ho tro",
	}
}

// GuestNameInput applies the nickname rules that need no data: NFC, spaces collapsed, 3–20 runes, letters / digits /
// space / _ - . only, at least one letter or digit, no link, not a reserved name. Errors are ErrValidation with
// details guestName = REQUIRED | LENGTH | FORMAT | TAKEN | BLOCKED.
func GuestNameInput(raw string) (string, error) {
	name := collapseSpaces(norm.NFC.String(raw))

	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return "", Reject(ErrValidation, "guestName", DetailRequired)
	case n < GuestNameMinRunes || n > GuestNameMaxRunes:
		return "", Reject(ErrValidation, "guestName", DetailLength)
	case !guestNameRunes(name):
		return "", Reject(ErrValidation, "guestName", DetailFormat)
	case HasLink(name):
		return "", Reject(ErrValidation, "guestName", DetailBlocked)
	}

	normalized := Normalize(name)
	for _, r := range reservedGuestNames() {
		if MatchTerm(normalized, r) {
			return "", Reject(ErrValidation, "guestName", DetailTaken)
		}
	}

	return name, nil
}

// guestNameRunes: only letters, digits, space, _ - . and at least one letter or digit.
func guestNameRunes(name string) bool {
	alnum := false

	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			alnum = true
		case !strings.ContainsRune(" _-.", r):
			return false
		}
	}

	return alnum
}
