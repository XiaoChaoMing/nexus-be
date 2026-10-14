package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBearerToken(t *testing.T) {
	t.Parallel()

	const jwt = "aaa.bbb.ccc"

	for _, tc := range []struct {
		header string
		ok     bool
	}{
		{"Bearer " + jwt, true},
		{"bearer " + jwt, true},
		{"  Bearer   " + jwt + "  ", true},
		{jwt, true}, // pasted without the scheme (Swagger UI)
		{"", false},
		{"Bearer ", false},
		{"Basic dXNlcjpwYXNz", false},
		{"Bearer a.b", false},
		{"Bearer aaa.bbb.ccc extra", false},
	} {
		got, ok := bearerToken(tc.header)
		require.Equal(t, tc.ok, ok, tc.header)

		if ok {
			require.Equal(t, jwt, got)
		}
	}
}
