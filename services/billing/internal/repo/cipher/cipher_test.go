package cipher_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/cipher"
	"github.com/stretchr/testify/require"
)

func key(b byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(b), 32)))
}

func TestAESGCM(t *testing.T) {
	t.Parallel()

	c, err := cipher.New(key('k'))
	require.NoError(t, err)

	plain := []byte(`{"accountNumber":"0123456789"}`)

	blob, err := c.Seal(plain, []byte("payout:a"))
	require.NoError(t, err)
	require.Equal(t, byte(0x01), blob[0], "version byte")
	require.NotContains(t, string(blob), "0123456789")

	again, err := c.Seal(plain, []byte("payout:a"))
	require.NoError(t, err)
	require.NotEqual(t, blob, again, "fresh nonce per seal")

	got, err := c.Open(blob, []byte("payout:a"))
	require.NoError(t, err)
	require.Equal(t, plain, got)

	_, err = c.Open(blob, []byte("payout:b"))
	require.ErrorIs(t, err, cipher.ErrBlob, "another channel's AAD")

	other, err := cipher.New(key('o'))
	require.NoError(t, err)

	_, err = other.Open(blob, []byte("payout:a"))
	require.ErrorIs(t, err, cipher.ErrBlob, "another key")

	blob[0] = 0x02
	_, err = c.Open(blob, []byte("payout:a"))
	require.ErrorIs(t, err, cipher.ErrBlob, "unknown version")

	_, err = c.Open([]byte{1, 2}, nil)
	require.ErrorIs(t, err, cipher.ErrBlob)

	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		_, err = cipher.New(bad)
		require.ErrorIs(t, err, cipher.ErrKey, bad)
	}
}
