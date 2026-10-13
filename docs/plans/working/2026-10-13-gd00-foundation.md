# GĐ0 — Nền móng: hạ tầng, CI, số liệu media, NXL-00/NXL-D

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (nhiều gói việc, đụng compose/Makefile/CI của mọi service) | **Approved** 2026-10-13 · TSD WP1–WP3: `docs/plans/technical/2026-10-13-gd00-infra.md` · **Đã triển khai WP1–WP5** (Completed Plan cùng tên) — spec NXL-00, NXL-D chờ duyệt | 2026-10-13 |

Lịch GĐ0 theo lộ trình: **12/10 → 23/10/2026** (tuần 1–2).

## 1. Task Brief

- **Mục tiêu:** hoàn tất "đường ray" chung cho mọi service. Kết quả cần đạt:
  - `docker compose up` dựng đủ hạ tầng theo kiến trúc §12;
  - CI lint + test cho từng module Go và cho web;
  - số liệu CPU/độ trễ để chốt `MAX_CONCURRENT_TRANSCODES`;
  - NXL-00 và NXL-D đủ để mở GĐ1 ngày 26/10.
- **Đã có từ trước (không làm lại):**
  - Monorepo dạng `services/*` + `pkg/` + `go.work`; 3 service chạy được: identity, stream, media.
  - `pkg/` chung: logger, postgres, tracing, jwt (JWKS), eventbus (NATS JetStream), events, outbox, httpserver, grpcserver.
  - Compose có PostgreSQL, NATS `-js`, MinIO, Jaeger, pgAdmin.
  - PoC media đã vượt mức: L1–L4 chạy thật OBS → RTMP → ffmpeg 3 rendition → HLS/MinIO → hls.js.
  - `/metrics` Prometheus ở stream (:8092) và media (:8091).
- **Trong phạm vi:**
  - **WP1** Dọn khuôn mẫu go-clean-template, cập nhật `AGENTS.md` cho nhiều service.
  - **WP2** Hạ tầng compose: Redis, Mailpit, Prometheus, Grafana, Nginx làm cổng vào (định tuyến cơ bản).
  - **WP3** Chất lượng: `make nxl-lint` / `nxl-test` cho mọi module, dọn lint, CI theo module + CI web.
  - **WP4** Đo PoC: script tải 1/2/3 stream, phiếu đo, chốt giới hạn transcode, ghi ADR.
  - **WP5** Spec: NXL-00, NXL-D, errata PRD v2.1.
- **Ngoài phạm vi:**
  - Rate limit, CORS ở Nginx → GĐ1.
  - URL HLS có chữ ký (`secure_link`) → GĐ5.
  - channel-svc, xác minh email → GĐ1.
  - Exporter cho NATS/Postgres; dashboard cho chat → GĐ3/GĐ7.
- **Giả định:**
  - Web vẫn là repo riêng `nexuslive-web` (kiến trúc §13).
  - Định dạng lỗi giữ như đã code: `{"data": …}` / `{"error": {"code", "message", "details"}}`, mã lỗi `UPPER_SNAKE`. NXL-00 ghi lại đúng định dạng này.
- **Câu hỏi mở:** đã chốt hết ở §10 (13/10).

## 2. Hiện trạng và khoảng trống

| Hạng mục (roadmap §3 GĐ0) | Hiện trạng | Còn thiếu |
| --- | --- | --- |
| Monorepo, `AGENTS.md` nhiều service | `services/`, `pkg/`, `go.work` đã có | App mẫu còn trong root (`internal/`, `cmd/app`, `migrations/` mẫu, RabbitMQ, `app` + `nginx` trỏ vào app mẫu); `AGENTS.md` vẫn viết cho một service |
| `pkg/` chung | Đủ, trừ cấu hình | Mỗi service tự đọc env — giữ nguyên, không tạo `pkg/config` |
| Compose: Nginx, PG, Redis, NATS, MinIO, Mailpit, Prometheus, Grafana | PG, NATS, MinIO (+ Jaeger) | Redis, Mailpit, Prometheus, Grafana; Nginx đang trỏ vào app mẫu |
| CI lint + test theo service | `ci.yml` chỉ lint/test module gốc | Service chưa có trong CI; lint còn khoảng 108 lỗi (stream 32, media 76); web chưa có CI |
| PoC media + số liệu | Pipeline chạy thật | Chưa có số liệu độ trễ, CPU cho 1/2/3 stream; `MAX_CONCURRENT_TRANSCODES` chưa chốt bằng số đo |
| NXL-00, NXL-D | `docs/ba/specs` trống | Viết cả hai; errata PRD v2.1 |

## 3. Các gói việc

### WP1 — Dọn khuôn mẫu, monorepo (Standard)

1. Gỡ app mẫu:
   - `cmd/app`, `internal/`, `migrations/` mẫu, `integration-test/`, `docker-compose-integration-test.yml`, `pkg/rabbitmq`;
   - Swagger mẫu ở `docs/docs.go`, `docs/swagger.*`;
   - service `app`, `rabbitmq` trong compose;
   - target Makefile mẫu (`run`, `swag-v1`, `proto-v1`, `migrate-*`, `compose-up-integration-test`).
2. Đổi tên thành `nexuslive-be` (module, tài liệu, thư mục, remote). Module gốc chỉ còn `pkg/`. `go mod tidy`, giữ `golangci-lint` là `tool`.
3. Cập nhật `AGENTS.md`:
   - cấu trúc `services/<svc>/…`, bảng lệnh `nxl-*`;
   - quy tắc lint/test cho từng module;
   - vị trí plan, spec (`docs/ba/specs`).
4. Cập nhật `README.md` gốc: chạy stack, cổng, tài khoản dev.

### WP2 — Hạ tầng compose (Full)

| Thành phần | Image (cần duyệt) | Cổng | Ghi chú |
| --- | --- | --- | --- |
| Redis | `redis:7.4-alpine` | 6379 | Có `healthcheck`; chưa service nào dùng tới GĐ3 |
| Mailpit | `axllent/mailpit` (ghim version) | SMTP 1025, UI 8025 | Cho xác minh email ở GĐ1 |
| Prometheus | `prom/prometheus` (ghim version) | 9090 | Scrape `stream:8092`, `media:8091`, mỗi 5s |
| Grafana | `grafana/grafana` (ghim version) | 3000 | Provision sẵn datasource và dashboard "Livestream" |
| Nginx | `nginx:1.31.3-alpine` (đã có) | 80 | Cấu hình lại làm cổng vào (bảng dưới) |

Nginx (cổng vào duy nhất cho trình duyệt — kiến trúc §4):

| Đường dẫn | Chuyển tới |
| --- | --- |
| `/v1/auth/*`, `/v1/users/*`, `/.well-known/jwks.json` | identity :8090 |
| `/v1/streams/*`, `/v1/studio/*`, `/v1/vods/*` | stream :8092 |
| `/hls/*` | MinIO bucket HLS |
| `/v1/ws` | Để sẵn `upgrade` cho chat-svc (GĐ3), hiện trả 503 |
| `/internal/*`, `/metrics` | **Chặn** (chỉ trong mạng compose) |

Danh sách route chính xác lấy từ `docs/api/README.md` khi viết TSD.

Làm thêm:

- Thêm `/metrics` cho identity-svc để Prometheus scrape đủ 3 service. Thư viện đã có trong `go.work`.
- Tách compose bằng `profiles`:
  - mặc định: hạ tầng;
  - `app`: các service;
  - `obs`: Prometheus + Grafana + Jaeger.
- Make target mới:
  - `nxl-infra-up`;
  - `nxl-obs-up`;
  - `nxl-up-all` (toàn bộ, đi qua Nginx).
- Web: Vite proxy có tùy chọn `VITE_PROXY_TARGET=http://localhost` để đi qua Nginx. Mặc định vẫn như cũ.
- Cấu hình đặt trong `deploy/`:
  - `deploy/nginx/nginx.conf` (chuyển từ `nginx/`);
  - `deploy/prometheus/prometheus.yml`;
  - `deploy/grafana/provisioning/{datasources,dashboards}/`.
- `.env.example`: thêm biến mới, nếu có.

### WP3 — Lint, test, CI (Lightweight cho phần dọn lint, Standard cho CI)

1. Makefile:
   - `nxl-lint`: chạy `go tool golangci-lint run ./...` lần lượt cho module gốc và từng `services/*` có trong `go.work`.
   - `nxl-test`: chạy `go test -race ./...` cho cùng danh sách module.
2. Dọn lint stream-svc (32) và media-svc (76) về 0, không đổi hành vi. Dùng `//nolint` chỉ khi có lý do ghi kèm.
3. `.github/workflows/ci.yml`, job theo ma trận module `[., services/identity, services/stream, services/media]`:
   - `golangci-lint`;
   - `go test -race`;
   - repo test của stream dùng service container Postgres qua `NXL_TEST_PG_URL_STREAM`.
4. Job `docker build` cho từng service có `Dockerfile` (chỉ build, không push).
5. Bỏ các job của app mẫu: integration test mẫu, `nancy` trên module gốc (nếu Q1 = gỡ).
6. `nexuslive-web/.github/workflows/ci.yml`:
   - `pnpm install --frozen-lockfile`;
   - lint, typecheck, `vitest run`, `vite build`;
   - E2E không chạy trên CI vì cần cả stack và ffmpeg — ghi rõ trong README.

### WP4 — Đo PoC media (Standard)

1. `scripts/bench-transcode.sh N`:
   - phát N luồng thử bằng ffmpeg (test pattern 1080p30, 6 Mbps) vào media-svc;
   - lấy mẫu CPU/RAM bằng `docker stats` và số liệu `/metrics` mỗi 5s trong 2 phút;
   - ghi CSV vào `docs/report/measurements/raw/`.
2. Chạy với N = 1, 2, 3 trên máy dev, đặt `MAX_CONCURRENT_TRANSCODES=3` khi đo.
3. Độ trễ glass-to-glass: ông chủ đo bằng OBS thật theo `scripts/measure-latency.md` (đồng hồ trong khung hình, 10 lần, lấy P50/P95).
4. Điền `docs/report/measurements/2026-10-livestream.md` (bảng CPU theo N, độ trễ, cấu hình máy).
5. Chốt giá trị mặc định `MAX_CONCURRENT_TRANSCODES`. Ghi ADR trong `docs/architecture/decisions.md`: lưu segment trên MinIO, ladder 3 mức, giới hạn đồng thời. Đây là mốc quyết định "cuối tuần 2" của roadmap §5.

### WP5 — Spec NXL-00, NXL-D, errata (Standard, tài liệu)

Viết trong `docs/ba/specs/`, theo khuôn 19 mục + "AI Implementation Rules" của NXL-A/B/C.

| Spec | Nội dung chính | Hạn |
| --- | --- | --- |
| NXL-00 | Envelope và mã lỗi (theo code hiện tại); phân trang cursor; `Idempotency-Key`; giao thức WebSocket `/v1/ws` + chủ đề; định dạng sự kiện (theo Q3); bảng hằng số cấu hình (`INGEST_GRACE_PERIOD`, `STREAM_END_COOLDOWN`…); RBAC (Guest/Viewer/Creator/Mod/Admin); trạng thái hệ thống 404/500/bảo trì | 16/10 |
| NXL-D | Đăng ký/đăng nhập, xác minh email, quên mật khẩu, Guest + modal đăng nhập, đăng ký Creator + Admin duyệt, Channel (tên, avatar, trạng thái khóa), Profile, Follow + chuông, Settings P0 | 22/10 |
| Errata PRD v2.1 | Sửa mâu thuẫn envelope, WebSocket, mã lỗi; NXL-A/B/C lên v1.1 trỏ về NXL-00 | 23/10 |

Cập nhật `docs/ba/traceability/requirements-matrix.md` cho các FR mà NXL-D sở hữu.

## 4. Thứ tự và lịch

| Ngày | Việc |
| --- | --- |
| 13–14/10 | Duyệt plan → TSD hạ tầng (WP1–WP3) → WP1 |
| 14–16/10 | WP2 compose + Nginx + Prometheus/Grafana; viết NXL-00 song song |
| 16–19/10 | WP3: `nxl-lint`/`nxl-test`, dọn lint, CI |
| 19–20/10 | WP4: script đo + ông chủ đo OBS thật |
| 20–23/10 | NXL-D, errata, ADR, Completed Plan; demo `docker compose up` sạch |

## 5. Khu vực bị ảnh hưởng

| Lớp | File | Thay đổi |
| --- | --- | --- |
| Compose | `docker-compose.yml` | Thêm 4 service, `profiles`, bỏ `app`/`rabbitmq`, Nginx mới |
| Cấu hình hạ tầng | `deploy/{nginx,prometheus,grafana}/` | Mới (Nginx chuyển từ `nginx/`) |
| Makefile | `Makefile` | Thêm `nxl-infra-up`, `nxl-obs-up`, `nxl-up-all`, `nxl-lint`, `nxl-test`; bỏ target mẫu |
| identity-svc | `internal/app`, `go.mod` | Route `/metrics` |
| stream, media | toàn bộ | Chỉ sửa lint, không đổi hành vi |
| CI | `.github/workflows/ci.yml`, `nexuslive-web/.github/workflows/ci.yml` | Ma trận module, CI web |
| Module gốc | `cmd/`, `internal/`, `migrations/`, `pkg/rabbitmq`, `docs/` swagger mẫu | Gỡ (Q1) |
| Tài liệu | `AGENTS.md`, `README.md`, `docs/architecture/*`, `docs/ba/specs/*` | Cập nhật / mới |

Luồng có thể hỏng:

- `make nxl-up`, `nxl-media-up` (đổi profile, đường dẫn Nginx);
- E2E web (proxy);
- repo test stream cần Postgres;
- `make linter-golangci` (module gốc thu nhỏ).

Tất cả phải chạy lại sau mỗi WP.

## 6. Biến môi trường và tài liệu

- Env mới (dự kiến): `SMTP_HOST`, `SMTP_PORT` (dùng ở GĐ1, ghi sẵn vào `.env.example`); `VITE_PROXY_TARGET` (web).
- README cần cập nhật: `go-clean-template/README.md`, `services/media/README.md` (bench), `nexuslive-web/README.md` (CI, proxy).
- `docs/api/README.md`: thêm cột "qua Nginx".

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Gỡ app mẫu làm vỡ import trong `pkg/` hoặc service | `go build ./...` + `nxl-test` cho cả `go.work` trước khi xóa; git giữ lịch sử |
| Dọn lint vô tình đổi hành vi media (code nhạy thời gian) | Commit riêng cho từng loại lỗi; chạy lại unit test + kịch bản ffmpeg giả lập sau mỗi bước |
| Máy dev không đo được 3 stream (CPU yếu) | Ghi số liệu thật, chốt giới hạn theo số đo; demo tối đa 2 stream như roadmap §7 |
| Spec trễ làm GĐ1 bắt đầu không có NXL-D | NXL-D ưu tiên các mục GĐ1 cần (Auth, Creator, Channel tối thiểu); Profile/Follow có thể bổ sung ở v1.1 trước GĐ4 |
| Thêm image làm compose nặng | Dùng `profiles`: chỉ bật `obs` khi cần |

## 8. Kiểm tra trước khi báo xong

- [ ] `make nxl-lint` = 0 lỗi trên mọi module; `make nxl-test` xanh.
- [ ] `docker compose up` sạch:
  - healthcheck xanh;
  - Grafana hiện số liệu stream/media khi phát ffmpeg giả lập;
  - Mailpit nhận mail thử (`swaks` hoặc `nc`).
- [ ] Qua Nginx :80: đăng nhập, Studio tạo key, phát, xem HLS `/hls/...`; `/internal/*` và `/metrics` trả 403/404.
- [ ] E2E web chạy lại xanh (trực tiếp và qua Nginx).
- [ ] CI xanh trên một PR thử ở cả hai repo.
- [ ] Phiếu đo có số liệu 1/2/3 stream và độ trễ P50/P95.

## 9. Tiêu chí hoàn thành (theo roadmap)

- Given máy mới clone repo, When chạy `docker compose --profile app --profile obs up`, Then toàn bộ hạ tầng + 3 service lên, trình duyệt dùng được qua `http://localhost`.
- Given OBS phát vào stack, When đo 10 lần, Then độ trễ P95 ≤ 6s và có bảng CPU cho 1/2/3 stream.
- Given số đo CPU, Then `MAX_CONCURRENT_TRANSCODES` có giá trị mặc định kèm ADR.
- Given NXL-00 và NXL-D v1.0, When ông chủ duyệt, Then GĐ1 mở Working Plan từ hai spec này.

## 10. Câu hỏi đã chốt (13/10)

| # | Câu hỏi | Đề xuất → **Quyết định** |
| --- | --- | --- |
| Q1 | Gỡ hẳn app mẫu go-clean-template (`internal/`, `cmd/app`, RabbitMQ, migration mẫu, integration test mẫu)? | **Gỡ**: không service nào dùng, nó làm lint/CI/compose nặng và gây nhầm; git vẫn giữ lịch sử → **Gỡ; đổi tên repo thành `nexuslive-be`** (module `github.com/minhnn/nexuslive-be`, thư mục `DATN/nexuslive-be`, gỡ remote evrone) |
| Q2 | `docker-compose.yml` để ở gốc hay chuyển vào `deploy/` như kiến trúc §13? | **Giữ ở gốc** (lệnh `docker compose up` không đổi), chỉ chuyển cấu hình vào `deploy/`; sửa §13 cho khớp → **Theo đề xuất** |
| Q3 | Định dạng sự kiện: metadata trong NATS header (đang code) hay envelope trong body (kiến trúc §5.2)? | **Giữ header**: `Nats-Msg-Id` vốn là header và dùng để chống trùng; outbox + consumer đã chạy. Sửa §5.2 và ghi vào NXL-00 → **Theo đề xuất** |
| Q4 | Duyệt thêm 4 image: Redis, Mailpit, Prometheus, Grafana? | Duyệt (theo kiến trúc §12) → **Duyệt** |
| Q5 | File gốc PRD v2.0, NXL-A/B/C đang ở đâu? `docs/ba/specs` đang trống. | Chép vào `docs/ba/specs/` để NXL-00/NXL-D tham chiếu và để errata sửa trực tiếp → **Tham khảo tài liệu trong `docs/` của repo** (`product/`, `architecture/`) và `DATN/docs/ba` |
| Q6 | Nginx ở GĐ0 chỉ định tuyến; rate limit + CORS để GĐ1? | Đồng ý — giữ đúng roadmap → **Theo đề xuất** |
