# Completed — GĐ0 Nền móng

| Trạng thái | Ngày | Tài liệu |
| --- | --- | --- |
| WP1–WP5 xong (13/10) · còn: số đo OBS thật (ông chủ), duyệt NXL-00/NXL-D | 2026-10-13 | Working Plan `working/2026-10-13-gd00-foundation.md` · TSD `technical/2026-10-13-gd00-infra.md` v1.1 |

## 1. Đã bàn giao

### WP1 — nexuslive-be

- Gỡ toàn bộ app mẫu go-clean-template:
  - `cmd/app`, `internal/`, `config/`, `migrations/`, `integration-test/`, `Dockerfile` gốc;
  - `pkg/rabbitmq`, `pkg/nats`;
  - Swagger, proto và ảnh mẫu trong `docs/`;
  - service `app`, `rabbitmq`, `minio-init`.
- Module đổi thành `github.com/minhnn/nexuslive-be` (`.../services/<svc>`) ở mọi import, `go.mod` và Swagger sinh ra.
- `AGENTS.md` viết lại cho monorepo:
  - đường dẫn theo service;
  - lệnh `nxl-*`, bảng cổng;
  - quy tắc service không import nhau, sự kiện qua outbox;
  - checklist thêm service mới.
- `README.md` mới thay README của app mẫu.

### WP2 — Hạ tầng

- `docker-compose.yml` với ba profile:
  - mặc định: PG, NATS, MinIO, **Redis**, **Mailpit**;
  - `nxl`: các service + **Nginx edge :8080**;
  - `obs`: **Prometheus**, **Grafana**, Jaeger.
- `deploy/nginx/nginx.conf`:
  - định tuyến `/v1/*` theo service, `/hls/*` tới MinIO;
  - chặn `/internal`, `/metrics`, `/swagger`;
  - lỗi của cổng vào dùng chung envelope JSON;
  - `X-Request-Id`, log JSON;
  - resolver động, nên cổng vào vẫn chạy khi một service chết.
- `deploy/prometheus/prometheus.yml`: scrape 3 service mỗi 5s.
- `deploy/grafana/`: datasource và dashboard "NexusLive — Livestream" (18 panel) được provision sẵn, mặc định mở khi vào Grafana.
- `pkg/httpmetrics` (mới): `http_requests_total`, `http_request_duration_seconds`, gắn nhãn theo mẫu route.
- identity-svc có `/metrics`; stream-svc thêm metric HTTP vào registry đang có.
- Makefile: `nxl-infra-up`, `nxl-obs-up`, `nxl-up-all`, `nxl-down`, `nxl-build`, `nxl-edge-logs`, `nxl-lint`, `nxl-test`.
- Bỏ các target của app mẫu; `linter-golangci` và `test` thành alias.
- Web:
  - `VITE_DEV_GATEWAY_URL` cho Vite proxy đi qua cổng vào;
  - cập nhật `.env.example` và README.

### WP3 — Chất lượng

- Lint về **0 lỗi** ở cả 4 module (gốc, identity, stream, media). Trước đó còn khoảng 128 lỗi: identity 26, stream 28, media 73.
- Việc dọn lint gồm:
  - sentinel error thay lỗi động;
  - tách hàm cho các chỗ quá phức tạp:
    - `Repo.Authorize` → `onAirGrant`;
    - events handler;
    - `Manager.Begin` → `claimLocked`;
    - `Publication.Start` → `replaceStreamLocked` + `ffmpegOptionsLocked`;
    - `Publication.Close` → `dropUnstartedAndUnlock` + `stopReasonLocked`;
    - rtmp `sourceFromMeta`;
    - authz `statusError`;
    - media `connect` + `recoverStreams`;
    - progress parser `set`;
  - hằng số có tên thay số trần, sửa chính tả US, đổi `net.Listen` → `ListenConfig`.
- `.github/workflows/ci.yml`:
  - lint và test theo ma trận 4 module (Postgres service container cho repo test của stream);
  - `docker build` 3 service;
  - yamllint, hadolint, dotenv-linter.
- `nexuslive-web/.github/workflows/ci.yml`: lint, typecheck, test, build.

## 2. Khác với plan

Chi tiết ở TSD §10:

- Giữ tên profile `nxl` thay cho `app`.
- Gỡ `minio-init`, vì media-svc tự tạo bucket.
- Tắt `gocritic.hugeParam` và `rangeValCopy`.
- `make deps` chỉ chạy cho module gốc.
- Chưa chạy `go mod tidy` ở module gốc: proxy trong môi trường của Claude chặn tải module.

## 3. Vấn đề gặp phải

- **Proxy chặn `proxy.golang.org`** với nhiều module, nên không chạy được `go mod tidy`.
  - Cách xử lý: kiểm tra trong một workspace riêng, có `go.mod` rút gọn, dùng module cache sẵn có.
  - `go.mod` gốc thật vẫn còn các require cũ.
- **Không truy cập được Docker Hub.**
  - Tag Redis và Grafana dựa trên bản đã biết, chưa kiểm tra được.
  - `docker compose config` chưa chạy được. Thay bằng kiểm tra file compose theo schema `compose-spec` (0 lỗi) và kiểm tra `depends_on` + profile.
- Sau khi đổi tên module, `gci` đổi thứ tự import (`github.com/minhnn` đứng sau `github.com/gofiber`). Đã chạy `golangci-lint fmt` trên toàn repo.

## 4. Kiểm tra

| Lệnh / kịch bản | Kết quả |
| --- | --- |
| `go build` + `go vet` cả 4 module | pass |
| golangci-lint v2.13.2 (cấu hình repo) cho 4 module | **0 issues** |
| `go test -race` cả 4 module, có repo test stream trên PG18 thật (7 test) | pass |
| `promtool check config` | pass |
| JSON dashboard: 18 truy vấn PromQL chạy trên Prometheus thật | 0 lỗi |
| Compose: kiểm theo schema `compose-spec`, `depends_on` và profile | 0 lỗi |
| `actionlint` cho 2 workflow; `yamllint` cho `.github`, compose, `deploy` | pass |
| Web: `pnpm lint`, `typecheck`, `test` (42), `build` | pass |

| Luồng chạy lại | Đường đi | Kết quả |
| --- | --- | --- |
| Nginx (cùng file cấu hình, upstream trỏ về máy) | `/healthz`, JWKS, `/v1/streams`, `/v1/studio/*` (401), `/v1/vods/*` (404 của service), `/v1/ws` (503), `/internal`, `/metrics`, `/swagger`, `/nope` (404 JSON) | pass |
| S3: dừng stream-svc | `/v1/streams` → `503 SERVICE_UNAVAILABLE` trong < 1 ms; identity vẫn 200; bật lại → 200 | pass |
| Body 2 MB → 413 JSON; POST `/hls` → 403; `X-Request-Id` được giữ | Qua edge | pass |
| S4: đăng ký → tạo key → phát ffmpeg | Qua edge + RTMP. Prometheus: `stream_live_streams=1`, `media_active_streams=1`, `media_ffmpeg_speed≈1.05`, `http_requests_total` theo route; `/hls/.../master.m3u8` 200 + `no-cache` | pass |
| S5: Mailpit nhận thư SMTP | `localhost:1025` → API 8025 | pass |
| E2E web (phát → xem → gián đoạn → kết thúc → cooldown → VOD) qua `VITE_DEV_GATEWAY_URL` | Playwright | pass (31s) |
| Media bị `kill -9` khi đang phát → khởi động lại | Log `recovered 1 stream(s)`; stream không còn on-air | pass |

**Chưa kiểm tra (cần ông chủ chạy trên máy):**

- `make deps`: dọn `go.mod`/`go.sum` gốc, cần mạng.
- `make nxl-up-all` bằng Docker thật. Cần xác nhận:
  - các image pull được;
  - Grafana hiện dashboard;
  - đăng nhập qua `http://localhost:8080`.
- `make nxl-lint` và `make nxl-test` trên Windows. Không có gcc thì dùng `NXL_TEST_FLAGS=`.
- CI chạy trên GitHub (cần remote mới).

## 5. Việc tiếp theo

- Ông chủ đo OBS thật + `make nxl-bench` trên máy demo, điền phần B của `DATN/docs/report/measurements/2026-10-livestream.md`.
- Duyệt NXL-00 và NXL-D (câu hỏi mở 00-Q1…Q4, D-Q1…D-Q5) trước khi mở Working Plan GĐ1 (26/10).
- `pkg/eventbus`: thêm header `Nxl-Occurred-At`, `Nxl-Producer` (NXL-00 §11) ở GĐ1.
- Dọn volume `rabbitmq_data` mồ côi trên máy cũ: `docker volume ls | grep rabbitmq` rồi `docker volume rm <tên>`.
- `git remote add origin <repo mới>` và push để CI chạy.

---

## Đợt 2 — WP4 (đo media) và WP5 (spec), 13/10

### Đã bàn giao

- **WP4:**
  - `scripts/bench-transcode.sh` + `make nxl-bench`: N publisher ffmpeg qua cổng vào; đo CPU/RAM của
    media-svc + ffmpeg con (chế độ `docker` hoặc `proc`), `speed`, frame rơi, độ trễ HLS phía server; xuất
    bảng tổng hợp + CSV. `shellcheck` sạch.
  - `scripts/measure-latency.md` §6 hướng dẫn chạy và cách đọc.
  - Số liệu trên máy của Claude (2 vCPU): ~0,7 core/luồng 720p, ~0,9 core/luồng 1080p, độ trễ phía server
    0,26–0,29 s khi máy theo kịp; quá tải → trễ tăng dần, không rơi frame.
  - **ADR-012** chốt chi tiết pipeline (trả lời Q5) và quy tắc chọn `MEDIA_MAX_CONCURRENT_STREAMS` (mặc định 2).
  - Phiếu `DATN/docs/report/measurements/2026-10-livestream.md` (phần A có số, phần B cho OBS thật),
    `measurement-plan.md`, dữ liệu thô `raw/2026-10-13-bench-*.csv`.
- **WP5:**
  - `DATN/docs/ba/specs/NXL-00-platform-foundation.md` v1.0: vai trò + ma trận quyền, quy ước REST, envelope,
    danh mục mã lỗi, cursor, JWT/phiên, idempotency, rate limit, giao thức `/v1/ws`, định dạng + danh mục sự
    kiện, bảng hằng số, trạng thái hệ thống, log/audit, AI rules.
  - `DATN/docs/ba/specs/NXL-D-account-and-channel.md` v1.0: đủ 19 mục + AI rules; 24 BR, 22 AC; phân giai
    đoạn GĐ1 / GĐ4 / GĐ7.
  - Errata v1.2: chốt E-01…E-03, thêm E-11…E-15, **bản sửa PRD v2.1** (PRD gốc nằm trên Drive, chưa chép vào
    `ba/requirements/`, nên errata ghi nội dung sửa để áp dụng khi chép).
  - Ma trận truy vết v1.1: 11 dòng NXL-D "Đã đối chiếu" (BR/AC), tổng hợp tính lại từ bảng (90 yêu cầu, 21
    chưa có spec).
  - ERD identity v1.1 (`CANCELLED`), nhật ký quyết định v1.2 (DL-11…DL-13, đóng Q5), kiến trúc §5.2 và
    `AGENTS.md` sửa mô tả header sự kiện cho đúng code, `docs/api/README.md` trỏ về NXL-00.

### Khác với plan

- Spec đặt tên theo quy ước `DATN/docs/README.md` (tiếng Anh): `NXL-00-platform-foundation.md`,
  `NXL-D-account-and-channel.md`.
- Không sửa được PRD v2.1 trực tiếp (file gốc không có trong repo/DATN) → errata ghi bản sửa.
- Đo trên máy 2 vCPU, không có Docker, S3 giả lập thay MinIO; độ trễ glass-to-glass thật chưa đo (cần OBS).

### Kiểm tra

| Việc | Kết quả |
| --- | --- |
| `bash -n`, `shellcheck` cho `bench-transcode.sh` | pass |
| Chạy thật 720p × {1,2,3} × 120 s và 1080p × {1,2} × 60 s qua Nginx edge | xong, số ở phiếu đo |
| Đối chiếu NXL-00 với code: mã lỗi, envelope, cursor, JWT claims, cookie, header sự kiện, hằng số mặc định | khớp (phần đã chạy đánh dấu ✅) |
| Ma trận truy vết tính lại từ bảng | 90 dòng, 21 "Chưa có spec" |
