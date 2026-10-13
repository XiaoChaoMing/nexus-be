package jwt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

const (
	_jwksMinRefresh = 10 * time.Second
	_jwksMaxAge     = 10 * time.Minute
	_jwksTimeout    = 3 * time.Second
)

// ErrUnknownKey is returned when the token's kid is not in the key set, even after a refresh.
var ErrUnknownKey = errors.New("unknown signing key")

// ErrJWKSUnavailable is returned when the JWKS endpoint does not answer 200.
var ErrJWKSUnavailable = errors.New("jwks endpoint unavailable")

// JWKSVerifier verifies tokens with public keys fetched from identity-svc's JWKS endpoint.
// Keys are cached; an unknown kid (identity restarted with a new dev key, or key rotation)
// triggers a refresh, at most once per 10 seconds.
type JWKSVerifier struct {
	url    string
	issuer string
	client *http.Client

	mu        sync.Mutex
	keys      map[string]*EdDSA
	fetchedAt time.Time
}

// NewJWKSVerifier -.
func NewJWKSVerifier(url, issuer string) *JWKSVerifier {
	return &JWKSVerifier{
		url:    url,
		issuer: issuer,
		client: &http.Client{Timeout: _jwksTimeout},
		keys:   map[string]*EdDSA{},
	}
}

// Verify checks the token against the key named by its kid header.
func (v *JWKSVerifier) Verify(tokenString string) (*Claims, error) {
	kid, err := tokenKID(tokenString)
	if err != nil {
		return nil, err
	}

	key, err := v.key(kid)
	if err != nil {
		return nil, err
	}

	return key.Verify(tokenString)
}

func (v *JWKSVerifier) key(kid string) (*EdDSA, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if k, ok := v.keys[kid]; ok && time.Since(v.fetchedAt) < _jwksMaxAge {
		return k, nil
	}

	if time.Since(v.fetchedAt) >= _jwksMinRefresh {
		if err := v.refreshLocked(); err != nil {
			if k, ok := v.keys[kid]; ok { // keep serving a known key while identity is unreachable
				return k, nil
			}

			return nil, err
		}
	}

	if k, ok := v.keys[kid]; ok {
		return k, nil
	}

	return nil, fmt.Errorf("jwt - JWKSVerifier: %w", ErrUnknownKey)
}

func (v *JWKSVerifier) refreshLocked() error {
	v.fetchedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), _jwksTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, http.NoBody)
	if err != nil {
		return fmt.Errorf("jwt - JWKS request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwt - JWKS fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwt - JWKS fetch: status %d: %w", resp.StatusCode, ErrJWKSUnavailable)
	}

	var set JWKS
	if err = json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("jwt - JWKS decode: %w", err)
	}

	keys := make(map[string]*EdDSA, len(set.Keys))

	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}

		ver, kerr := NewEdDSAVerifier(k.X, v.issuer)
		if kerr != nil {
			continue
		}

		keys[k.Kid] = ver
	}

	v.keys = keys

	return nil
}

func tokenKID(tokenString string) (string, error) {
	// Only the header is read here; the signature is verified with the selected key afterwards.
	tok, _, err := jwtlib.NewParser().ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return "", fmt.Errorf("jwt - parse header: %w", err)
	}

	kid, ok := tok.Header["kid"].(string)
	if !ok || strings.TrimSpace(kid) == "" {
		return "", fmt.Errorf("jwt - missing kid: %w", ErrUnknownKey)
	}

	return kid, nil
}
