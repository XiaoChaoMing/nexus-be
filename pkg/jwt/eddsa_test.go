package jwt_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _issuer = "nexuslive-identity"

func newSigner(t *testing.T) *jwt.EdDSA {
	t.Helper()

	seed := make([]byte, ed25519.SeedSize)
	_, err := rand.Read(seed)
	require.NoError(t, err)

	s, err := jwt.NewEdDSA(base64.StdEncoding.EncodeToString(seed), _issuer, 15*time.Minute)
	require.NoError(t, err)

	return s
}

func TestEdDSA_SignVerify(t *testing.T) {
	t.Parallel()

	s := newSigner(t)

	token, err := s.Sign("user-1", "USER", "session-1", time.Now())
	require.NoError(t, err)

	claims, err := s.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.Subject)
	assert.Equal(t, "USER", claims.Role)
	assert.Equal(t, "session-1", claims.SessionID)
}

func TestEdDSA_VerifyWithPublishedKey(t *testing.T) {
	t.Parallel()

	s := newSigner(t)

	token, err := s.Sign("user-1", "ADMIN", "session-1", time.Now())
	require.NoError(t, err)

	v, err := jwt.NewEdDSAVerifier(s.JWKS().Keys[0].X, _issuer)
	require.NoError(t, err)

	claims, err := v.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "ADMIN", claims.Role)
}

func TestEdDSA_Rejects(t *testing.T) {
	t.Parallel()

	s := newSigner(t)
	other := newSigner(t)

	expired, err := s.Sign("u", "USER", "s", time.Now().Add(-time.Hour))
	require.NoError(t, err)

	foreign, err := other.Sign("u", "USER", "s", time.Now())
	require.NoError(t, err)

	hmac, err := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwtlib.RegisteredClaims{
		Subject:   "u",
		Issuer:    _issuer,
		ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte(s.JWKS().Keys[0].X))
	require.NoError(t, err)

	none, err := jwtlib.NewWithClaims(jwtlib.SigningMethodNone, jwtlib.RegisteredClaims{
		Subject:   "u",
		Issuer:    _issuer,
		ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(jwtlib.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{name: "expired", token: expired},
		{name: "signed by another key", token: foreign},
		{name: "HS256 with public key as secret", token: hmac},
		{name: "alg none", token: none},
		{name: "garbage", token: "not-a-token"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := s.Verify(tc.token)
			assert.Error(t, err)
		})
	}
}

func TestNewEdDSA_InvalidSeed(t *testing.T) {
	t.Parallel()

	_, err := jwt.NewEdDSA(base64.StdEncoding.EncodeToString([]byte("short")), _issuer, time.Minute)
	require.ErrorIs(t, err, jwt.ErrInvalidKey)
}
