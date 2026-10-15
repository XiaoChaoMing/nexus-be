package entity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const (
	_tokenMACLen  = 16
	_tokenIDLen   = 16
	_tokenBodyLen = _tokenIDLen + 1 // user id + type byte
	_tokenSep     = "."
)

// UnsubscribeToken is base64url(userId ‖ type byte) + "." + base64url(HMAC-SHA256(secret, "unsub:userId:type")[:16])
// (NXL-E BR-E10). It never expires; changing the secret voids every token.
func UnsubscribeToken(secret, userID string, t Type) (string, bool) {
	id, err := uuid.Parse(userID)

	idx := slices.Index(Types(), t)
	if err != nil || idx < 0 {
		return "", false
	}

	body := append(id[:], byte(idx)) //nolint:gocritic,gosec // id is an array copy; idx < len(Types())

	return base64.RawURLEncoding.EncodeToString(body) + _tokenSep +
		base64.RawURLEncoding.EncodeToString(tokenMAC(secret, id.String(), t)), true
}

// ParseUnsubscribeToken checks a token and returns the user id and type it names; anything wrong →
// ErrTokenInvalid.
func ParseUnsubscribeToken(secret, token string) (string, Type, error) {
	body64, mac64, ok := strings.Cut(token, _tokenSep)
	if !ok {
		return "", "", ErrTokenInvalid
	}

	body, err := base64.RawURLEncoding.DecodeString(body64)
	if err != nil || len(body) != _tokenBodyLen {
		return "", "", ErrTokenInvalid
	}

	mac, err := base64.RawURLEncoding.DecodeString(mac64)
	if err != nil || len(mac) != _tokenMACLen {
		return "", "", ErrTokenInvalid
	}

	id, err := uuid.FromBytes(body[:_tokenIDLen])
	types := Types()

	if err != nil || int(body[_tokenIDLen]) >= len(types) {
		return "", "", ErrTokenInvalid
	}

	t := types[body[_tokenIDLen]]
	if !hmac.Equal(mac, tokenMAC(secret, id.String(), t)) {
		return "", "", ErrTokenInvalid
	}

	return id.String(), t, nil
}

func tokenMAC(secret, userID string, t Type) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("unsub:" + userID + ":" + string(t)))

	return h.Sum(nil)[:_tokenMACLen]
}
