package entity

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Block reasons of CHAT_CONTENT_BLOCKED (details.reason).
const (
	BlockReasonTerm = "TERM"
	BlockReasonLink = "LINK"
)

// Term length after normalization (BR-F09).
const (
	TermMinRunes = 2
	TermMaxRunes = 100
)

// _linkRe finds a URL or a bare domain with a top-level domain of the list (BR-F10). An e-mail address is not a
// link: the character before the domain may not be "@".
var _linkRe = regexp.MustCompile(`(?:https?://|www\.)\S|(?:^|[^\p{L}\p{N}@._-])[\p{L}\p{N}-]+(?:\.[\p{L}\p{N}-]+)*\.` +
	`(?:com|net|org|vn|io|gg|tv|me|co|xyz|ly)(?:$|[^\p{L}\p{N}-])`)

// Normalize is the form blocked terms are stored and matched in: NFC, lower case, diacritics removed (đ → d),
// every rune that is not a letter or digit turned into a space, spaces collapsed and trimmed.
func Normalize(s string) string {
	s = strings.ToLower(norm.NFC.String(s))

	stripped, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err == nil {
		s = stripped
	}

	s = strings.Map(func(r rune) rune {
		switch {
		case r == 'đ':
			return 'd'
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return r
		default:
			return ' '
		}
	}, s)

	return strings.Join(strings.Fields(s), " ")
}

// MatchTerm reports whether the normalized message contains the normalized term as whole words.
func MatchTerm(normalizedMsg, term string) bool {
	return term != "" && strings.Contains(" "+normalizedMsg+" ", " "+term+" ")
}

// HasLink reports whether the raw message contains a URL or a domain (BR-F10).
func HasLink(raw string) bool {
	return _linkRe.MatchString(strings.ToLower(norm.NFC.String(raw)))
}

// TermInput validates a blocked term: it returns the display form (trimmed, spaces collapsed) and the
// normalized form; either out of 2–100 runes is ErrValidation with details.term = REQUIRED | LENGTH.
func TermInput(raw string) (display, term string, err error) {
	display = collapseSpaces(raw)
	if display == "" {
		return "", "", Reject(ErrValidation, "term", DetailRequired)
	}

	term = Normalize(display)
	if n := utf8.RuneCountInString(term); n < TermMinRunes || n > TermMaxRunes || utf8.RuneCountInString(display) > TermMaxRunes {
		return "", "", Reject(ErrValidation, "term", DetailLength)
	}

	return display, term, nil
}

// AutoModRules are a channel's content rules.
type AutoModRules struct {
	// Terms are normalized blocked terms.
	Terms      []string
	BlockLinks bool
}

// Check returns ErrContentBlocked (details.reason TERM | LINK) for a message the rules refuse. Owners,
// moderators and admins are exempt from both rules, VIPs from links only (BR-F11).
func (r AutoModRules) Check(body string, st Standing) error {
	if st.Privileged() {
		return nil
	}

	if len(r.Terms) > 0 {
		msg := Normalize(body)

		for _, t := range r.Terms {
			if MatchTerm(msg, t) {
				return Reject(ErrContentBlocked, "reason", BlockReasonTerm)
			}
		}
	}

	if r.BlockLinks && !st.VIP && HasLink(body) {
		return Reject(ErrContentBlocked, "reason", BlockReasonLink)
	}

	return nil
}

func collapseSpaces(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}
