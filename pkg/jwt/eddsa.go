package jwt

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// ErrInvalidKey is returned when an Ed25519 key cannot be decoded.
var ErrInvalidKey = errors.New("invalid ed25519 key")

// Claims are the access-token claims shared by every NexusLive service.
type Claims struct {
	Role      string `json:"role"`
	SessionID string `json:"sid"`
	jwtlib.RegisteredClaims
}

// JWK is one public key in a JSON Web Key Set (RFC 8037, OKP / Ed25519).
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// JWKS is the document served at the JWKS endpoint.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// EdDSA signs and verifies access tokens with an Ed25519 key pair.
type EdDSA struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	kid     string
	issuer  string
	ttl     time.Duration
}

// NewEdDSA builds a signer from a base64 (std) encoded 32-byte Ed25519 seed.
func NewEdDSA(seedB64, issuer string, ttl time.Duration) (*EdDSA, error) {
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil {
		return nil, fmt.Errorf("jwt - NewEdDSA - base64.DecodeString: %w", err)
	}

	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("jwt - NewEdDSA - seed length %d: %w", len(seed), ErrInvalidKey)
	}

	return newEdDSA(ed25519.NewKeyFromSeed(seed), issuer, ttl)
}

// GenerateEdDSA builds a signer with a fresh random key; tokens do not survive a restart.
func GenerateEdDSA(issuer string, ttl time.Duration) (*EdDSA, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("jwt - GenerateEdDSA - ed25519.GenerateKey: %w", err)
	}

	return newEdDSA(priv, issuer, ttl)
}

// NewEdDSAVerifier builds a verify-only instance from a base64url (raw) encoded public key, as published in JWKS.
func NewEdDSAVerifier(publicB64URL, issuer string) (*EdDSA, error) {
	pub, err := base64.RawURLEncoding.DecodeString(publicB64URL)
	if err != nil {
		return nil, fmt.Errorf("jwt - NewEdDSAVerifier - base64.DecodeString: %w", err)
	}

	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("jwt - NewEdDSAVerifier - key length %d: %w", len(pub), ErrInvalidKey)
	}

	return &EdDSA{public: pub, kid: keyID(pub), issuer: issuer}, nil
}

func newEdDSA(priv ed25519.PrivateKey, issuer string, ttl time.Duration) (*EdDSA, error) {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("jwt - newEdDSA - public key: %w", ErrInvalidKey)
	}

	return &EdDSA{private: priv, public: pub, kid: keyID(pub), issuer: issuer, ttl: ttl}, nil
}

func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)

	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// TTL is the lifetime of issued tokens.
func (e *EdDSA) TTL() time.Duration {
	return e.ttl
}

// Sign issues an access token for the user and session.
func (e *EdDSA) Sign(userID, role, sessionID string, now time.Time) (string, error) {
	if e.private == nil {
		return "", fmt.Errorf("jwt - Sign: %w", ErrInvalidKey)
	}

	claims := Claims{
		Role:      role,
		SessionID: sessionID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Subject:   userID,
			Issuer:    e.issuer,
			IssuedAt:  jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(now.Add(e.ttl)),
		},
	}

	token := jwtlib.NewWithClaims(jwtlib.SigningMethodEdDSA, claims)
	token.Header["kid"] = e.kid

	signed, err := token.SignedString(e.private)
	if err != nil {
		return "", fmt.Errorf("jwt - Sign - token.SignedString: %w", err)
	}

	return signed, nil
}

// Verify checks signature, algorithm, issuer and expiry, and returns the claims.
func (e *EdDSA) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}

	// Pinning the algorithm list rejects "none" and HMAC tokens signed with the public key.
	_, err := jwtlib.ParseWithClaims(tokenString, claims, func(*jwtlib.Token) (any, error) {
		return e.public, nil
	}, jwtlib.WithValidMethods([]string{jwtlib.SigningMethodEdDSA.Alg()}), jwtlib.WithIssuer(e.issuer), jwtlib.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("jwt - Verify - jwtlib.ParseWithClaims: %w", err)
	}

	return claims, nil
}

// JWKS returns the public key set.
func (e *EdDSA) JWKS() JWKS {
	return JWKS{Keys: []JWK{{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(e.public),
		Kid: e.kid,
		Alg: jwtlib.SigningMethodEdDSA.Alg(),
		Use: "sig",
	}}}
}
