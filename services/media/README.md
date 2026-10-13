# media-svc

RTMP ingest → ffmpeg ABR (720p/480p/360p, 2 s segments) → HLS `EVENT` playlists in MinIO.
The same files serve live, DVR (rewind while live) and the VOD after the stream ends.

Design: `docs/plans/technical/2026-10-11-core-livestream-rtmp-hls.md`.

Two modes:

- **With stream-svc** (`MEDIA_STREAM_SVC_URL` set, compose default): each publish is authorised by
  `POST /internal/v1/ingest/authorize`; `ingest.*` / `vod.processed` go to NATS JetStream; `stream.ended`
  kicks OBS and finalises the VOD.
- **Standalone** (phase L1): one static key `MEDIA_STATIC_STREAM_KEY`, events are only logged.

## Run (Docker)

```bash
make nxl-media-up            # db, nats, identity, stream-svc, minio, media-svc
```

Get a key: sign in on the web (or `POST /v1/auth/login`), then `POST http://localhost:8092/v1/studio/stream-key`
with the access token. OBS → *Settings → Stream*: Service **Custom**, Server `rtmp://localhost:1935/live`,
Stream key = that key. Output: x264, keyframe interval 2 s, AAC.

Test page: <http://localhost:8091/dev/player> — lists sessions, plays, switches quality, rewinds,
shows PDT latency.

Without OBS:

```bash
make nxl-media-publish KEY=live_...   # ffmpeg test pattern for 60 s (needs ffmpeg on PATH)
```

## Layout

| Package | Role |
| --- | --- |
| `rtmpserver` | go-rtmp handler: key check, onMetaData, codec check (H.264 + AAC), tag forwarding |
| `session` | live → interrupted (grace) → resume or VOD; capacity; shutdown |
| `pipeline` | ffmpeg process, FLV writer on stdin, `-progress` parser |
| `ffmpegcmd`, `ladder` | ffmpeg arguments and quality ladder (pure) |
| `uploader` | mirrors the stream dir to S3: segments first, playlists only when complete |
| `hlsfs` | playlist helpers: segments, ENDLIST, duration |
| `authz` | stream-svc HTTP client, or the static key (standalone) |
| `events` | publisher: NATS JetStream or log |
| `storage` | MinIO client (bucket policy + 14-day lifecycle) and in-memory fake |
| `httpapi` | `/healthz`, dev player, `/dev/sessions`, `/hls/*` proxy to the bucket |

Object layout: `nxl-media/streams/{streamId}/{master.m3u8, thumb.jpg, 720p/index.m3u8, 720p/seg_00001.ts …}`.
