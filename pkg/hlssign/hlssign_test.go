package hlssign_test

import (
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/hlssign"
	"github.com/stretchr/testify/require"
)

// The expected token was computed with:
// printf '%s' '1760572800streams/0199d4a2-0000-7000-8000-000000000001/ testsecret' |
// openssl md5 -binary | openssl base64 | tr +/ -_ | tr -d '='.
func TestToken(t *testing.T) {
	t.Parallel()

	exp := time.Unix(1760572800, 0)
	prefix := "streams/0199d4a2-0000-7000-8000-000000000001/"

	require.Equal(t, "I41xc5zXmaWT_GPG3tdBNA", hlssign.Token("testsecret", prefix, exp))
	require.NotEqual(t, hlssign.Token("testsecret", prefix, exp), hlssign.Token("other", prefix, exp))
	require.NotEqual(t, hlssign.Token("testsecret", prefix, exp), hlssign.Token("testsecret", prefix, exp.Add(time.Second)))
}

func TestPath(t *testing.T) {
	t.Parallel()

	exp := time.Unix(1760572800, 0)
	got := hlssign.Path("/hls/", "testsecret", "streams/0199d4a2-0000-7000-8000-000000000001/", "master.m3u8", exp)

	require.Equal(t, "/hls/t/1760572800/I41xc5zXmaWT_GPG3tdBNA/streams/0199d4a2-0000-7000-8000-000000000001/master.m3u8", got)
}
