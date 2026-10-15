// Package hlssign signs HLS URLs for the nginx secure_link module (secure_link_md5
// "$secure_link_expires$prefix $secret"). The token signs a directory prefix, so a master playlist, its
// variant playlists and their segments, fetched by relative URI, share one token.
package hlssign

import (
	"crypto/md5" //nolint:gosec // the nginx secure_link module only supports MD5
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// Token returns base64url(MD5("<expires><prefix> <secret>")) without padding.
func Token(secret, prefix string, expires time.Time) string {
	sum := md5.Sum([]byte(strconv.FormatInt(expires.Unix(), 10) + prefix + " " + secret)) //nolint:gosec // see import

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Path returns "<base>/t/<expires>/<token>/<prefix><file>", e.g. /hls/t/1760572800/Q2x…/streams/{id}/master.m3u8.
func Path(base, secret, prefix, file string, expires time.Time) string {
	return strings.TrimSuffix(base, "/") + "/t/" + strconv.FormatInt(expires.Unix(), 10) + "/" +
		Token(secret, prefix, expires) + "/" + prefix + strings.TrimPrefix(file, "/")
}
