package entity

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxBodyRunes is the longest message (NXL-B BR03).
const MaxBodyRunes = 500

// NormalizeBody applies NXL-B BR03/BR04: NFC, line endings to \n, control and invisible formatting
// characters removed (the zero-width joiner stays: emoji sequences need it), trimmed; then checks the
// length and the number of lines. Errors are ErrValidation with details body = REQUIRED | LENGTH.
func NormalizeBody(body string, maxLines int) (string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = norm.NFC.String(body)

	var b strings.Builder

	b.Grow(len(body))

	for _, r := range body {
		if r == '\n' || !dropped(r) {
			b.WriteRune(r)
		}
	}

	out := strings.TrimSpace(b.String())

	switch {
	case out == "":
		return "", Reject(ErrValidation, "body", DetailRequired)
	case utf8.RuneCountInString(out) > MaxBodyRunes:
		return "", Reject(ErrValidation, "body", DetailLength)
	case maxLines > 0 && strings.Count(out, "\n") >= maxLines:
		return "", Reject(ErrValidation, "body", DetailLength)
	}

	return out, nil
}

const _zeroWidthJoiner = '‍'

// dropped: control characters, and format characters (bidi overrides, zero-width spaces, BOM) that
// can disguise text, except the zero-width joiner.
func dropped(r rune) bool {
	if r == utf8.RuneError {
		return true
	}

	if unicode.IsControl(r) {
		return true
	}

	return unicode.Is(unicode.Cf, r) && r != _zeroWidthJoiner
}
