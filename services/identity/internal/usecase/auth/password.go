package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var errBadHash = errors.New("malformed password hash")

// Argon2Params are the argon2id cost parameters.
type Argon2Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}

// DefaultArgon2Params follows the OWASP minimum for argon2id (19 MiB, 2 passes, 1 lane).
var DefaultArgon2Params = Argon2Params{Memory: 19 * 1024, Time: 2, Threads: 1} //nolint:gochecknoglobals,mnd // tuned constant set

const (
	_saltLen    = 16
	_keyLen     = 32
	_hashFields = 6
)

// Argon2 hashes passwords into PHC strings: $argon2id$v=19$m=..,t=..,p=..$salt$hash.
type Argon2 struct {
	p Argon2Params
}

// NewArgon2 -.
func NewArgon2(p Argon2Params) *Argon2 {
	return &Argon2{p: p}
}

// Hash -.
func (a *Argon2) Hash(password string) (string, error) {
	salt := make([]byte, _saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("Argon2 - Hash - rand.Read: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, a.p.Time, a.p.Memory, a.p.Threads, _keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, a.p.Memory, a.p.Time, a.p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify compares in constant time using the parameters stored in the hash.
func (a *Argon2) Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != _hashFields || parts[1] != "argon2id" {
		return false, errBadHash
	}

	var (
		version int
		p       Argon2Params
	)

	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, errBadHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}

	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}

	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want))) //nolint:gosec // len is _keyLen

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
