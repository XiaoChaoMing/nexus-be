// Package httpapi serves health, a dev session list, a dev HLS player and a /hls proxy to object storage.
package httpapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/minhnn/nexuslive-be/services/media/internal/session"
)

//go:embed player.html
var _playerHTML []byte

// Options -.
type Options struct {
	DevPlayer bool
	// S3 bucket base, e.g. http://minio:9000/nxl-media; /hls/<key> is proxied to <base>/<key>.
	HLSOrigin     string
	RTMPPublicURL string
	// Metrics is served at /metrics when set.
	Metrics http.Handler
}

// New -.
func New(mgr *session.Manager, o Options) (http.Handler, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"status": "ok", "activeStreams": mgr.Active(), "rejectedPublishes": mgr.Rejections()})
	})

	if o.Metrics != nil {
		mux.Handle("GET /metrics", o.Metrics)
	}

	if !o.DevPlayer {
		return mux, nil
	}

	origin, err := url.Parse(strings.TrimSuffix(o.HLSOrigin, "/"))
	if err != nil {
		return nil, err
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = origin.Scheme
			r.Out.URL.Host = origin.Host
			r.Out.URL.Path = origin.Path + strings.TrimPrefix(r.In.URL.Path, "/hls")
			r.Out.URL.RawPath = ""
			r.Out.Host = origin.Host
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del("Authorization")
		},
	}

	mux.Handle("GET /hls/streams/", proxy)

	mux.HandleFunc("GET /dev/sessions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"rtmpUrl": o.RTMPPublicURL, "sessions": mgr.List()})
	})

	mux.HandleFunc("GET /dev/player", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(_playerHTML) //nolint:errcheck // client gone
	})

	return mux, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // client gone
}
