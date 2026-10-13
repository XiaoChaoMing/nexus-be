package jwt_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/stretchr/testify/require"
)

func TestJWKSVerifier(t *testing.T) {
	t.Parallel()

	signer, err := jwt.GenerateEdDSA("nexuslive-identity", time.Minute)
	require.NoError(t, err)

	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)

		if err := json.NewEncoder(w).Encode(signer.JWKS()); err != nil {
			t.Errorf("encode jwks: %v", err)
		}
	}))
	defer srv.Close()

	v := jwt.NewJWKSVerifier(srv.URL, "nexuslive-identity")

	tok, err := signer.Sign("user-1", "viewer", "sess-1", time.Now())
	require.NoError(t, err)

	claims, err := v.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.Subject)

	_, err = v.Verify(tok)
	require.NoError(t, err)
	require.EqualValues(t, 1, hits.Load(), "keys are cached")

	other, err := jwt.GenerateEdDSA("nexuslive-identity", time.Minute)
	require.NoError(t, err)

	forged, err := other.Sign("user-1", "admin", "x", time.Now())
	require.NoError(t, err)

	_, err = v.Verify(forged)
	require.ErrorIs(t, err, jwt.ErrUnknownKey)

	wrongIssuer, err := jwt.GenerateEdDSA("someone-else", time.Minute)
	require.NoError(t, err)

	_, err = jwt.NewJWKSVerifier(srv.URL, "nexuslive-identity").Verify(mustSign(t, wrongIssuer))
	require.Error(t, err)
}

func mustSign(t *testing.T, s *jwt.EdDSA) string {
	t.Helper()

	tok, err := s.Sign("u", "viewer", "s", time.Now())
	require.NoError(t, err)

	return tok
}
