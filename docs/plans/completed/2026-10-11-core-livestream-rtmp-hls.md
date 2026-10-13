# Completed Plan — Core livestream (theo giai đoạn)

| | |
| --- | --- |
| **Track** | Full |
| **Working Plan** | `../working/2026-10-11-core-livestream-rtmp-hls.md` (v2) |
| **TSD** | `../technical/2026-10-11-core-livestream-rtmp-hls.md` (v1.4) |

---

## GĐ-L1 — media-svc độc lập (2026-10-11)

**Trạng thái:** code xong, test tự động đạt. Bạn cho làm tiếp L2 trước khi đo OBS thật — phiếu đo `DATN/docs/report/measurements/2026-10-L1-media.md` vẫn trống, đo được ở bất kỳ lúc nào (từ L2, lấy key qua Studio API).

### 1. Đã bàn giao

| Phần | File |
| --- | --- |
| media-svc | `services/media/`: `cmd`, `config`, `internal/{entity, ladder, ffmpegcmd, hlsfs, pipeline, session, rtmpserver, uploader, storage, authz, events, httpapi, app}`, `Dockerfile`, `README.md` |
| Trang thử | `GET /dev/player` (hls.js 1.7.3): danh sách phiên, đổi chất lượng, tua lùi + **Về trực tiếp**, hiển thị độ trễ theo `PROGRAM-DATE-TIME`, bộ đệm, băng thông |
| API dev | `GET /healthz`, `GET /dev/sessions` (trạng thái, bậc, tốc độ ffmpeg, frame rơi, lỗi upload), `GET /hls/streams/...` (proxy tới bucket) |
| Workspace | `go.work` thêm `./services/media`; `services/identity/Dockerfile` copy cả `services/` (go.work cần đủ module) |
| Compose | Service `media` (profile `nxl`, cổng 1935 + 8091, volume `media_work`, `stop_grace_period: 30s`) |
| Makefile | `nxl-media-up`, `nxl-media-logs`, `nxl-media-publish` (ffmpeg giả lập OBS 60s, có đồng hồ trên hình), `nxl-media-test` |
| Cấu hình | `.env.example`: `MEDIA_STATIC_STREAM_KEY`, `MEDIA_MAX_CONCURRENT_STREAMS`, `MEDIA_GRACE_PERIOD`, `MEDIA_X264_PRESET` |
| Dependency mới | `yutopp/go-rtmp` v0.0.7, `yutopp/go-flv` v0.3.1, `minio/minio-go/v7` v7.0.100, `sirupsen/logrus` v1.9.3 (logger mà go-rtmp yêu cầu) |
| Sửa kèm | identity-svc: các lời gọi `l.Fatal(err, "...")` / `l.Error(err, ...)` dùng sai kiểu printf của `pkg/logger` → `l.Fatal("...: %v", err)` |

### 2. Kiểm thử đã chạy (Claude, Go 1.27.1 build từ source, ffmpeg 6.1, S3 giả lập bằng moto)

| Kịch bản | Kết quả |
| --- | --- |
| Unit: `ladder`, `ffmpegcmd`, `hlsfs`, `uploader` (thứ tự segment → playlist, bỏ `.tmp`, retry, ẩn ENDLIST tới lần cuối), `pipeline` (ghi FLV, parse `-progress`), `authz` | Đạt, `-race` |
| Key sai | OBS/ffmpeg nhận `Publish failed`, log không chứa key |
| Phát 720p30 + AAC | 3 bậc 720p/480p/360p, segment 2s, `master.m3u8` có `CODECS`, thumbnail; Cache-Control đúng bảng §5.4 |
| Phát lần hai cùng key khi đang live | Từ chối `channel already live` |
| Rớt rồi nối lại trong grace | Playlist: `MEDIA-SEQUENCE:1` giữ nguyên, `seg_00001…00007`, `#EXT-X-DISCONTINUITY`, `seg_00008…00012` — không ghi đè, không nhảy số; bản trên S3 trong lúc chờ không có ENDLIST |
| Hết grace | ENDLIST trên cả 3 bậc, `max-age=3600`, thư mục tạm bị xóa, sự kiện `nxl.vod.processed.v1` (thời lượng 20,07s); `ffprobe` đọc VOD từ `/hls` đủ thời lượng |
| Nguồn 640×480 không audio | Chỉ 480p/360p, master không có `mp4a` |
| Đọc live như một player (`ffmpeg -live_start_index -2`) | Giải mã được |
| SIGTERM khi đang phát | Kick OBS, chốt VOD ngay, flush, thoát sạch |
| MinIO chưa sẵn sàng khi khởi động | Thử lại mỗi 2s (30 lần) rồi chạy bình thường |
| Tải: 1 luồng 720p30 → 3 bậc (`veryfast`) | ffmpeg ≈ 1,1 core, `speed` 1,05x, 0 frame rơi (máy ảo của Claude, không đại diện máy demo) |

### 3. Lệch so với kế hoạch / TSD

| Lệch | Lý do |
| --- | --- |
| Nối lại dùng lại `start_number = 1` thay vì `max + 1` | Phát hiện khi test: ffmpeg cộng dồn và đổi `MEDIA-SEQUENCE`. TSD đã sửa (v1.1 §5.5) |
| Playlist trong lúc INTERRUPTED được đẩy lên **không có** ENDLIST | ffmpeg luôn đóng playlist khi stdin đóng; nếu để nguyên, người xem sẽ tưởng buổi phát đã kết thúc. TSD v1.1 §5.4 |
| Ghi FLV trực tiếp thay vì encoder `go-flv` | Thân message RTMP đã là thân tag FLV; tránh giải mã/mã hóa lại. TSD v1.1 §5.2 |
| Chưa có metric Prometheus (`/metrics`) | Số liệu tạm xem qua `/dev/sessions`; thêm ở L4 cùng phần đo đạc |
| Chưa phát thử trên trình duyệt trong môi trường của Claude | Chromium của Playwright không có giải mã H.264 và CDN hls.js bị chặn ở sandbox → **bạn kiểm tra trên trình duyệt thật** (bước 4). Playlist/segment đã được kiểm bằng ffprobe/ffmpeg |
| MinIO dùng tài khoản root trong compose | Chỉ dev; user riêng chỉ-ghi cho media-svc làm ở L4 cùng URL có chữ ký |
| `go.sum` của media-svc ghép từ `go.sum` gốc + hash tính trực tiếp từ mã nguồn | Môi trường của Claude không vào được proxy.golang.org. Nếu Docker build báo `missing go.sum entry`, chạy `go mod tidy` trong `services/media` (cần Go 1.27) hoặc báo lại cho Claude |

### 4. Bạn kiểm tra (điểm dừng L1)

1. `make nxl-media-up` → `docker compose logs media` thấy `RTMP on [::]:1935`.
2. OBS → *Cài đặt → Stream*: Dịch vụ **Custom**, Server `rtmp://localhost:1935/live`, Stream key `live_dev_key`. *Output*: x264, CBR ~4500 kbps, **Keyframe interval 2s**, AAC 160k. Thêm nguồn **Text (GDI+)** với đồng hồ (hoặc trang time.is) để đo độ trễ.
3. Bấm *Start Streaming* → mở <http://localhost:8091/dev/player> → bấm **Xem** ở phiên đang live.
4. Kiểm: đổi 720p/480p/360p; kéo lùi vài chục giây rồi **Về trực tiếp**; tắt/bật stream trong < 60s → phát tiếp cùng phiên; dừng > 60s → phiên kết thúc, mở lại `?stream=<id>` xem lại được từ đầu.
5. Đo và ghi vào `DATN/docs/report/measurements/2026-10-L1-media.md`: độ trễ glass-to-glass (chụp màn hình OBS + player cùng lúc, 5 lần), CPU của container `media` (`docker stats`) với 1 rồi 2 luồng (luồng thứ 2: `make nxl-media-publish` với key khác sẽ bị từ chối ở L1 vì chỉ có một kênh — đo 2 luồng để sau L2, hoặc tạm thời chỉ đo 1 luồng).
6. Nếu OBS kết nối được nhưng không ra hình: gửi `docker compose logs media` cho Claude (phương án lùi theo TSD §9 là `gortmplib`).

### 5. Việc tiếp theo

- **GĐ-L2** (sau khi bạn duyệt L1): stream-svc + `POST /internal/v1/ingest/authorize`, stream key thật, NATS JetStream cho `ingest.*` / `vod.processed`, ERD `stream_settings`.

---

## GĐ-L2 — stream-svc + NATS (2026-10-11)

**Trạng thái:** code xong, test tự động + end-to-end (5 tiến trình thật) đạt — **chờ bạn chạy bằng Docker**.

### 1. Đã bàn giao

| Phần | File |
| --- | --- |
| Hạ tầng dùng chung | `pkg/eventbus` (JetStream), `pkg/events` (hợp đồng sự kiện), `pkg/outbox` (worker), `pkg/jwt/jwks.go` (xác minh bằng JWKS, có test) |
| stream-svc | `services/stream/`: `cmd`, `config`, `internal/{entity, repo, repo/persistent, usecase/streams, controller/{restapi, internalapi, events}, app}`, `Dockerfile` |
| Migration | `20261011000001_create_stream_settings` (+ ERD `stream` v1.1, ảnh `ERD-stream.png`, data-model) |
| API công khai (:8092) | `POST/GET /v1/studio/stream-key`, `GET/PUT /v1/studio/stream`, `POST /v1/studio/stream/end`, `GET /v1/streams?status=live&cursor=&limit=`, `GET /v1/streams/{id}`, `GET /v1/vods/{id}` |
| API nội bộ (:8093, chỉ trong mạng Docker) | `POST /internal/v1/ingest/authorize` + `X-Internal-Secret` |
| Sự kiện | stream-svc phát `stream.started` / `stream.ended` qua outbox; tiêu thụ `ingest.started/stopped/health`, `vod.processed`; job hết hạn INTERRUPTED mỗi 5s |
| media-svc | Chế độ có stream-svc: `authz/http.go`, publisher NATS, tiêu thụ `stream.ended` (kick + chốt VOD), `ingest.health` mỗi 5s, kiểm tra giới hạn trước khi authorize; chế độ standalone L1 giữ nguyên |
| identity-svc | Worker outbox publish `user.registered.v1` (NATS bắt buộc khi khởi động); test `auth_test` hết phụ thuộc ngày cố định |
| Workspace / Docker | `go.work` thêm `services/stream`; Dockerfile các service copy thêm `go.work.sum` |
| Compose | `nats` bật `-js`; service `stream` (profile `nxl`, cổng 8092); `identity` + `media` nối NATS; `media` dùng stream-svc thay key tĩnh |
| Makefile | `nxl-up` gồm `nats` + `stream`; `nxl-media-up` chạy cả chuỗi; `nxl-media-publish KEY=live_…`; `nxl-stream-test` |
| Cấu hình | `.env.example`: `NXL_INTERNAL_SECRET`, `STREAM_INGEST_GRACE_PERIOD`, `MEDIA_GRACE_PERIOD=90s` |

### 2. Kiểm thử đã chạy

| Kịch bản | Kết quả |
| --- | --- |
| Unit: usecase stream (key chỉ lưu băm, key cũ bị thu hồi, chuẩn hóa tag, cursor), `JWKSVerifier` (cache, kid lạ, sai issuer), `authz.HTTP` | Đạt, `-race` |
| Repository trên PostgreSQL 18.4 thật (`NXL_TEST_PG_URL_STREAM`) | Tạo LIVE theo settings → 409 khi đang live → INTERRUPTED (sự kiện trùng bị bỏ qua) → nối lại trong grace `resumed=true` → LIVE → sửa settings áp vào stream đang phát → hết grace ENDED `ingest_timeout` → sự kiện muộn không hồi sinh stream → VOD tạo một lần → stream mới → Creator kết thúc → `NOT_LIVE`; outbox đúng thứ tự `started, ended, started, ended`; phân trang cursor |
| End-to-end: identity + stream + media + NATS JetStream (nats-server 2.12.9) + PostgreSQL 18 + S3 giả lập, ffmpeg giả lập OBS | Đăng ký → tạo key (401 khi không có token) → lưu tiêu đề/tag → key sai bị từ chối → phát: stream LIVE trong danh sách, có health → rớt: INTERRUPTED → nối lại: cùng `streamId`, LIVE → luồng thứ hai cùng key bị từ chối → **Kết thúc từ Studio: OBS bị ngắt, VOD READY** (`/v1/vods/{id}` đủ manifest/thumbnail/hạn 14 ngày) → phát lại rồi bỏ đi: hết grace → ENDED `ingest_timeout`, media chốt VOD theo `stream.ended` |
| JetStream sau kịch bản | Consumer `stream-svc` và `media-svc`: 0 tồn, 0 gửi lại; outbox stream-svc 4/4, identity 1/1 đã publish |

### 3. Lệch / ghi chú

| Nội dung | Lý do |
| --- | --- |
| identity-svc giờ cần NATS lúc khởi động (`make nxl-up` đã gồm `nats`) | Outbox phải được publish; chờ tối đa 60s rồi dừng |
| Chưa có `mockgen`/tracing cho stream-svc (giống identity) | Môi trường của Claude không chạy được công cụ sinh mock; test usecase dùng repo giả viết tay, repository test trên PG thật |
| `go.sum` của stream-svc = bản sao `go.sum` gốc | Mọi dependency của stream-svc đã có trong module gốc |
| Chưa có API Gateway: web gọi thẳng 8092 qua Vite proxy (L3) | Theo lộ trình |
| Known Debt: media-svc khởi động lại giữa buổi phát → phần trước rời khỏi playlist mới | Xem TSD §6.4 |

### 4. Bạn kiểm tra (điểm dừng L2)

1. `make nxl-media-up` (lần đầu build 4 image). Kiểm tra `docker compose ps`: db-migrate đã chạy xong; `identity`, `stream`, `media`, `nats`, `minio` đang chạy.
2. Lấy access token: đăng nhập trên web (`pnpm dev`, DevTools → response của `/v1/auth/login`), hoặc gọi:
   `curl -X POST localhost:8090/v1/auth/login -H "Content-Type: application/json" -d '{"login":"<email>","password":"<mật khẩu>"}'`
3. Tạo key: `curl -X POST localhost:8092/v1/studio/stream-key -H "Authorization: Bearer <token>"`.
4. OBS: Server `rtmp://localhost:1935/live`, Stream key vừa tạo (hoặc `make nxl-media-publish KEY=live_…`).
5. Theo dõi:
   - `curl localhost:8092/v1/studio/stream -H "Authorization: Bearer <token>"` — trạng thái và health.
   - `curl "localhost:8092/v1/streams?status=live"`.
   - Xem trên `http://localhost:8091/dev/player`.
6. Thử:
   - Tắt OBS < 60s rồi bật lại: cùng `streamId`.
   - `curl -X POST localhost:8092/v1/studio/stream/end -H "Authorization: Bearer <token>"`: OBS bị ngắt, `GET /v1/streams/{id}` có `vodId`.
7. Nếu được, đo luôn độ trễ/CPU cho phiếu L1.

### 4b. Swagger (bổ sung theo yêu cầu, 2026-10-11)

- **Swagger UI:**
  - identity-svc: http://localhost:8090/swagger/index.html
  - stream-svc: http://localhost:8092/swagger/index.html
  - Theo cách của go-clean-template: chú thích swaggo trên handler, spec sinh vào `services/<svc>/docs`, phục vụ bằng `gofiber/swagger`.
  - Bật/tắt bằng `NXL_SWAGGER_ENABLED` (mặc định bật).
- Sinh lại spec sau khi đổi API: `make nxl-swag` (chạy `go tool swag` trong container `golang:1.27`).
- Đã kiểm bằng Chromium: đăng ký qua Try it out ở identity, dán token vào **Authorize** ở stream, rồi `POST /studio/stream-key` (201) và `GET /studio/stream` (200).
- API nội bộ `/internal/v1/ingest/authorize` (cổng 8093) không có Swagger vì không mở ra ngoài Docker.

### 5. Việc tiếp theo

- **GĐ-L3 — Web:**
  - Studio: key, tiêu đề, trạng thái, Kết thúc.
  - Trang xem `/live/:id`: hls.js, DVR, các trạng thái.
  - Danh sách đang phát.
  - Vite proxy `/v1/studio|streams|vods` → 8092, `/hls` → MinIO.

---

## GĐ-L3 — Web: trang xem, player, Studio (2026-10-12)

**Trạng thái:** code xong; typecheck, lint, unit, E2E đạt — **chờ bạn kiểm tra trên Chrome + OBS thật** (điểm dừng L3).

### 1. Đã bàn giao (`nexuslive-web`)

| Phần | File |
| --- | --- |
| Dependency | `hls.js` 1.7.3; dev: `vitest` 5, `@playwright/test` 1.64 (đã duyệt) |
| Proxy dev | `vite.config.ts`: `/v1/studio|streams|vods` → 8092, `/v1` → 8090, `/hls` → MinIO `nxl-media`; `.env.example` (`VITE_DEV_IDENTITY_URL`, `VITE_DEV_STREAM_URL`, `VITE_DEV_HLS_URL`) thay `VITE_DEV_API_PROXY` |
| Dữ liệu | `features/streams/` (types, API, hooks TanStack Query: poll 5s khi on air, 3s ở Studio, 15s danh sách) |
| Player | `features/player/`: `HlsPlayer`, `SeekBar` (DVR), `QualityMenu`, `PlayerOverlay`, `useHls` (retry 1/2/4s, chờ manifest 20s khi vừa Go Live, recoverMediaError), `usePlayerShortcuts`, store Zustand riêng mỗi player, `lib/live-edge.ts`, `lib/display-state.ts` |
| Trang | `/` lưới "Đang phát" (`features/home`), `/live/:streamId` (`features/watch`), `/studio/stream` (`features/studio`: key, cài đặt, trạng thái + health + cảnh báo, preview, Kết thúc, hướng dẫn OBS); trang player tải lười |
| UI dùng chung | `components/ui/{dialog, badge, copy-field}.tsx`; menu người dùng thêm "Studio" |
| Test | `pnpm test` (12 unit), `pnpm e2e` (`e2e/livestream.spec.ts`), README mục Livestream / E2E |

**Backend (sửa kèm, phát hiện khi test L3):** media-svc khi OBS ngắt **trước khi** ffmpeg chạy (sai codec, rớt ngay) giờ phát `ingest.stopped`. Trước đó stream-svc giữ stream ở LIVE mãi mãi (`services/media/internal/session/session.go`). Trang xem: stream ENDED quá 2 phút không có VOD → "Buổi phát này không có bản xem lại" (hết poll).

### 2. Kiểm thử

| Lệnh / kịch bản | Kết quả |
| --- | --- |
| `pnpm typecheck`, `pnpm lint` (0 cảnh báo), `pnpm build` | Đạt; chunk player 592 kB tách riêng |
| `pnpm test` | 12/12 (live edge, map trạng thái hiển thị, schema Studio) |
| `pnpm e2e` trên stack thật (identity + stream + media + NATS + PG 18 + S3 giả, ffmpeg giả lập OBS) | Đạt: đăng ký → Studio tạo key (một lần) → lưu tiêu đề/tag (tag trùng bị gộp) → phát → Studio LIVE + health → trang chủ có thẻ → trang xem tải được manifest qua proxy `/hls` → ngắt OBS → Studio "Gián đoạn", trang xem "Tạm gián đoạn" → phát lại: cùng URL, LIVE → Kết thúc (dialog) → Studio Offline → khách mới mở trang thấy "Buổi phát đã kết thúc" + **Xem lại** |
| OBS sai codec (flv1) → ngắt trước khi transcode | Stream chuyển INTERRUPTED (trước đây kẹt LIVE) |

**Chưa kiểm được trong môi trường của Claude:** phát hình thật (Chromium của Playwright không giải mã H.264), nên các bước đổi chất lượng, tua lùi / Về trực tiếp của E2E tự bỏ qua. Chạy được trên máy bạn bằng Chrome thật: `E2E_CHROMIUM_PATH="C:/Program Files/Google/Chrome/Application/chrome.exe" pnpm e2e`.

### 3. Lệch so với TSD v1.3

| Lệch | Lý do |
| --- | --- |
| Tùy chọn chất lượng lưu theo **chiều cao** (720/480/360) thay vì chỉ số | Bậc khác nhau giữa các stream (nguồn 480p chỉ có 2 bậc) |
| Nhập tag bằng ô văn bản ngăn cách dấu phẩy, chưa dùng chip | Đơn giản, đủ cho L3; đổi sang chip khi làm Stream Manager đầy đủ (GĐ4) |
| Trang VOD chưa có (thuộc L4) — nút "Xem lại" dẫn tới `/vod/:id` sẽ 404 cho tới L4 | Theo phạm vi đã duyệt |

### 4. Bạn kiểm tra (điểm dừng L3)

1. Backend: `make nxl-media-up` (build lại media-svc vì có bản sửa).
2. Web: `cd nexuslive-web && pnpm install && pnpm dev` → http://localhost:5173.
3. Đăng nhập → **Studio** → Tạo stream key → dán vào OBS → Start Streaming → Studio chuyển LIVE, có health, preview phát.
4. Mở trang chủ ở cửa sổ khác (hoặc ẩn danh) → bấm thẻ → thử: đổi 720p/480p/360p; kéo thanh thời gian về trước → nhãn `-mm:ss` + "Về trực tiếp"; phím `←`/`→`, `Space`, `M`, `F`, `T`.
5. Tắt OBS < 60s rồi bật lại → trang xem hiện "Streamer sẽ quay lại ngay" khi ở điểm live, rồi tự phát tiếp.
6. Studio → Kết thúc → trang xem hết video thì hiện "Buổi phát đã kết thúc" + Xem lại (trang VOD có ở L4).
7. (Tùy chọn) `pnpm exec playwright install chromium` rồi `E2E_CHROMIUM_PATH=... pnpm e2e`.

---

## GĐ-L4 — VOD và ổn định (2026-10-12)

**Trạng thái:** code xong; unit, tích hợp PG, kịch bản trên stack thật và E2E đạt. Còn phần đo bằng OBS thật (bạn).

### 1. Đã bàn giao

| Phần | Nội dung |
| --- | --- |
| stream-svc — cooldown | `STREAM_END_COOLDOWN=30s`: sau khi Creator kết thúc, authorize của kênh trả `409 RECENTLY_ENDED` (OBS auto-reconnect không tạo buổi phát mới). Hết grace (`ingest_timeout`) không áp. Repo `EndedByCreatorSince`, lỗi `entity.ErrRecentlyEnded` |
| stream-svc — VOD hết hạn | Job `STREAM_VOD_EXPIRY_TICK=10m`: `READY` quá `expires_at` → `EXPIRED` (`Repo.ExpireVODs`) |
| stream-svc — metrics | `GET :8092/metrics`: `stream_live_streams`, `stream_transitions_total{to}`, `stream_authorize_total{result}` (package `internal/metrics`, use case chỉ biết interface `usecase.Observer`) |
| media-svc — khôi phục | `session.Manager.Recover` trước khi mở RTMP: chốt stream sót sau crash (ENDLIST, upload, `ingest.stopped{recovered}` + `vod.processed`), xóa thư mục rỗng, giữ thư mục khi upload lỗi |
| media-svc — từ chối & metrics | `/healthz` thêm `rejectedPublishes`; `GET :8091/metrics`: active streams, speed và frame rơi theo stream, thời gian/lỗi upload, từ chối theo lý do. Authz phân biệt `RECENTLY_ENDED` với `ALREADY_LIVE` |
| Dùng chung | `pkg/events.ReasonRecovered`; dependency `prometheus/client_golang v1.24.1` cho stream và media (đã duyệt, cùng phiên bản module gốc; `go.sum` từng service bổ sung checksum) |
| Web | Trang `/vod/:vodId` (player VOD, thông tin, hết hạn sau N ngày, hỏi xem tiếp, xem hết → xem lại, 404 → "Bản xem lại không còn"); `features/vod/lib/progress.ts` (localStorage `nxl.vod.<id>`); i18n namespace `vod` (vi/en); `HlsPlayer` nhận `endedLabel` |
| E2E | Thêm: kết thúc → OBS phát lại ngay bị từ chối (Studio vẫn Offline) → Xem lại → trang VOD; nếu trình duyệt giải mã H.264: xem, tải lại → hỏi xem tiếp. Sửa selector cũ (dialog → `alertdialog`, toast) |
| Tài liệu | TSD v1.4 §7A.10; `docs/api/README.md`; `.env.example`; `scripts/measure-latency.md` |

### 2. Kiểm thử đã chạy

| Lệnh / kịch bản | Kết quả |
| --- | --- |
| `go build`, `go vet`, `gofmt -l` (stream, media, pkg/events) | Đạt |
| `go test -race ./...` stream-svc (có `NXL_TEST_PG_URL_STREAM`, PG 18) | Đạt — mới: `TestAuthorizeCooldownAfterCreatorEnd` (4 ca), `TestExpireVODs` (use case + repo), `TestEndedByCreatorSince` (creator / ingest_timeout / ngoài cửa sổ) |
| `go test -race ./...` media-svc | Đạt — mới: `TestRecoverClosesCrashedStreams`, `TestRecoverKeepsDirWhenUploadFails`, `TestRejectionsCounted`, authz `RECENTLY_ENDED` |
| Stack thật (identity + stream + media + NATS + PG + S3 giả, ffmpeg giả OBS): Kết thúc rồi phát lại ngay | Studio vẫn Offline; ffmpeg bị từ chối; `rejectedPublishes.recently_ended = 1`; `stream_authorize_total{result="recently_ended"} 1` |
| `kill -9` media khi đang phát → khởi động lại | Log `recovered 1 stream(s)`; stream ENDED (`ingest_timeout`) có VOD READY 14s; playlist trên S3 có `#EXT-X-ENDLIST` |
| `MEDIA_MAX_CONCURRENT_STREAMS=1`, hai kênh | Kênh 2 Offline; `rejectedPublishes.capacity = 1`; `media_publish_rejected_total{reason="capacity"} 1`; `media_ffmpeg_speed` có số |
| `pnpm typecheck`, `pnpm lint`, `pnpm test` (42), `pnpm build` | Đạt |
| `pnpm e2e` trên stack thật | Đạt (phần phát hình bỏ qua vì Chromium của Playwright không giải mã H.264) |
| Chụp trang VOD (hỏi xem tiếp, đang phát, 404 tiếng Anh) | Hiển thị đúng |

**Chưa chạy:** `make linter-golangci` (không có golangci-lint trong môi trường của Claude); build Docker; đo độ trễ/CPU bằng OBS thật.

### 3. Lệch so với TSD

Ghi ở TSD §7A.10. Chính: giữ nhánh đánh số segment theo thời gian làm lưới an toàn (TSD đề xuất bỏ); job VOD chạy riêng 10 phút; metric INTERRUPTED đếm sau khi đọc lại trạng thái.

### 4. Bạn kiểm tra

1. `make nxl-media-up` (build lại stream-svc và media-svc).
2. `cd nexuslive-web && pnpm install && pnpm dev`.
3. Chạy 4 kịch bản trong `scripts/measure-latency.md` §5, rồi đo độ trễ/CPU (§2–§4) và điền phiếu đo.

### 5. Việc tiếp theo

- URL HLS có chữ ký / playback session: GĐ5 (age gate) hoặc GĐ7 (Gateway + Nginx).
- Tiến độ xem theo tài khoản (`PUT /v1/vods/{id}/progress`): GĐ4.
- User MinIO chỉ-ghi cho media-svc (tùy chọn).

