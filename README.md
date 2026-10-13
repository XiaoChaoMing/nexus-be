# nexuslive-be

Backend of **NexusLive**, a Twitch-like live streaming platform (graduation thesis project). Go
microservices in one monorepo; the web client lives next to it in `../nexuslive-web` and the admin
web app in `../nexuslive-admin`.

| Service | What it does | Ports |
| --- | --- | --- |
| `services/identity` | Accounts, sessions (EdDSA JWT + rotating refresh tokens), JWKS, e-mail verification and password reset, creator applications and their admin review | 8090 |
| `services/channel` | Creator channels: created when an application is approved (`creator.approved.v1` → `channel.created.v1`), live flag, public channel page `GET /v1/channels/{handle}` | 8094 |
| `services/stream` | Stream keys, stream lifecycle (LIVE → INTERRUPTED → ENDED), live catalog, VODs | 8092 public, 8093 internal |
| `services/media` | RTMP ingest, ffmpeg ABR transcode (720p/480p/360p), HLS to MinIO, thumbnails, crash recovery | 1935 RTMP, 8091 |

Services share `pkg/` (event bus, transactional outbox, JWT, HTTP server and metrics, logger,
Postgres) through `go.work`, and talk over NATS JetStream events and internal HTTP. More services
(chat, safety, notification, billing) join phase by phase — see `docs/product/roadmap.md`.

## Quick start

Requirements: Docker with Compose v2, GNU make, Go 1.27 (for tests and lint), ffmpeg (optional, to
publish a test stream without OBS).

```bash
cp .env.example .env      # optional: make falls back to .env.example
make nxl-up-all           # everything: infra, services, Nginx edge, Prometheus, Grafana
```

| URL | What |
| --- | --- |
| http://localhost:8080 | Nginx edge — the API (`/v1/*`) and HLS (`/hls/*`) for the browser; rate limits, CORS for the web and admin apps |
| rtmp://localhost:1935/live | OBS server; the stream key comes from Studio (`POST /v1/studio/stream-key`) |
| http://localhost:8090/swagger/index.html · http://localhost:8094/swagger/index.html · http://localhost:8092/swagger/index.html | Swagger of identity, channel and stream |
| http://localhost:8091/dev/player | media-svc test player |
| http://localhost:3000 | Grafana — dashboard "NexusLive — Livestream" (anonymous view, admin/admin) |
| http://localhost:9090 · http://localhost:16686 | Prometheus · Jaeger |
| http://localhost:8025 | Mailpit (every e-mail sent in dev: verification, password reset) |
| http://localhost:5050 | pgAdmin (`make nxl-pgadmin`) |

Then start the web client with `VITE_DEV_GATEWAY_URL=http://localhost:8080` (see its README) and
open http://localhost:5173. The admin web app (`../nexuslive-admin`, `pnpm dev`) runs on
http://localhost:5174 and reviews creator applications.

Dev accounts (seeded by identity when `NXL_ENV=dev`, password `IDENTITY_DEV_SEED_PASSWORD`, default
`nexuslive123`):

| Account | Role |
| --- | --- |
| `admin@nexuslive.dev` | ADMIN — signs in to the admin app |
| `creator@nexuslive.dev` | approved creator with a channel — can open the Studio and go live |

Only creators reach the Studio (`403 NOT_CREATOR` otherwise): register, verify the e-mail (link in
Mailpit), apply at `/creator/apply`, approve the application in the admin app; channel-svc then
creates the channel. Make any account an admin with `make nxl-promote-admin EMAIL=you@example.com`
(the role is in the next access token, after a refresh or a new login).

No OBS at hand: `make nxl-media-publish KEY=live_...` streams a 60 s test pattern.

Compose profiles: none = infrastructure (`make nxl-infra-up`), `nxl` = services + edge, `obs` =
Prometheus, Grafana, Jaeger (`make nxl-obs-up`). `make nxl-down` stops everything; volumes are kept.

## Development

| Command | Does |
| --- | --- |
| `make nxl-lint` | golangci-lint on every Go module |
| `make nxl-test` | unit tests of every module (`-race`; on Windows without gcc: `make nxl-test NXL_TEST_FLAGS=`) |
| `make format` | gofumpt + gci + goimports |
| `make nxl-swag` | regenerate Swagger of identity, channel and stream (runs in Docker) |
| `make nxl-channel-test` · `make nxl-stream-test` | one service's tests |
| `make nxl-promote-admin EMAIL=…` | give an existing account the ADMIN role |
| `make nxl-migrate-create SVC=stream NAME=add_x` | new migration for one service |
| `make deps` | tidy and verify the root module |
| `make help` | every target |

Repository tests need Postgres with the service's migrations applied:
`NXL_TEST_PG_URL_STREAM=postgres://...nxl_stream NXL_TEST_PG_URL_CHANNEL=postgres://...nxl_channel make nxl-test`.
CI (`.github/workflows/ci.yml`) runs lint and tests per module and builds each service image.

## Layout

```
services/<svc>/   cmd/, config/, internal/{app,entity,usecase,repo,controller}, migrations/, Dockerfile
pkg/              shared infrastructure (eventbus, events, outbox, jwt, httpserver, httpmetrics, logger, postgres, tracing)
deploy/           nginx/, prometheus/, grafana/, postgres/, pgadmin/
docs/             api/, architecture/ (+ ADRs), product/, plans/ (working → technical → completed)
scripts/          measurement guides
```

## Documentation

- API catalog: `docs/api/README.md`
- Architecture and decisions: `docs/architecture/system-architecture.md`, `docs/architecture/decisions.md`
- Roadmap and features: `docs/product/`
- Requirements, use cases, specs: `../docs/ba/`
- Contributor and agent rules (plans, lint, tests): `AGENTS.md`

## License

MIT — see `LICENSE`. The service layout started from
[evrone/go-clean-template](https://github.com/evrone/go-clean-template).
