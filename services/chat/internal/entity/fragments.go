package entity

import (
	"regexp"
	"strings"
	"unicode"
)

// _mentionRe matches "@username" (identity usernames: 3-25 letters, digits, underscore) followed by
// optional trailing punctuation.
var _mentionRe = regexp.MustCompile(`^@([A-Za-z0-9_]{3,25})([.,!?:;]*)$`)

// MentionCandidates returns the lower-cased usernames written as @username in body, without duplicates.
func MentionCandidates(body string) []string {
	seen := map[string]bool{}
	out := []string{}

	for _, w := range strings.FieldsFunc(body, unicode.IsSpace) {
		m := _mentionRe.FindStringSubmatch(w)
		if m == nil {
			continue
		}

		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true

			out = append(out, name)
		}
	}

	return out
}

// BuildFragments splits body into text, emote and mention fragments (NXL-B BR05, BR17, BR19).
// emotes is keyed by code (case-sensitive); users maps a lower-cased username to its user id.
// Unknown codes and usernames stay text.
func BuildFragments(body string, emotes map[string]Emote, users map[string]string) []Fragment {
	b := fragmentBuilder{}

	for _, tok := range splitKeepSpace(body) {
		if e, ok := emotes[tok]; ok {
			b.add(Fragment{Type: FragmentEmote, Text: tok, Emote: &EmoteRef{ID: e.ID, Code: e.Code, ImageURL: e.ImageURL}})

			continue
		}

		if m := _mentionRe.FindStringSubmatch(tok); m != nil {
			if id, ok := users[strings.ToLower(m[1])]; ok {
				b.add(Fragment{Type: FragmentMention, Text: "@" + m[1], UserID: id})
				b.text(m[2])

				continue
			}
		}

		b.text(tok)
	}

	return b.out
}

type fragmentBuilder struct{ out []Fragment }

func (b *fragmentBuilder) add(f Fragment) { b.out = append(b.out, f) }

// text appends to the previous text fragment so the body never becomes a run of tiny fragments.
func (b *fragmentBuilder) text(s string) {
	if s == "" {
		return
	}

	if n := len(b.out); n > 0 && b.out[n-1].Type == FragmentText {
		b.out[n-1].Text += s

		return
	}

	b.add(Fragment{Type: FragmentText, Text: s})
}

// splitKeepSpace cuts s into words and whitespace runs; concatenating the parts gives s back.
func splitKeepSpace(s string) []string {
	var (
		parts []string
		start int
		space bool
	)

	for i, r := range s {
		isSpace := unicode.IsSpace(r)
		if i > 0 && isSpace != space {
			parts = append(parts, s[start:i])
			start = i
		}

		space = isSpace
	}

	if start < len(s) {
		parts = append(parts, s[start:])
	}

	return parts
}
