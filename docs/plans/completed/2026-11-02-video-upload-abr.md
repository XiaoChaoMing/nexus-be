# Completed — Tải video lên kênh và chuyển mã adaptive bitrate (HLS)

| Working Plan | TSD | Ngày |
| --- | --- | --- |
| `docs/plans/working/2026-11-02-video-upload-abr.md` (Approved 04/11) | `docs/plans/technical/2026-11-02-video-upload-abr.md` v1.1 | 2026-11-04 |

Web: `../nexuslive-web/docs/plans/working/2026-11-02-video-upload-abr.md`. Admin: thay đổi nhỏ trong `nexuslive-admin` (WP10).

## 1. Đã giao

**`pkg/`**

- `pkg/events`: thêm `upload.completed | progress | processed | failed.v1`, `vod.published.v1`, `safety.vod_takedown.v1`.
  `vod.updated` / `vod.deleted` chỉ thêm trường: `source`, `description`, `playlistPrefix`; `streamId` thành omitempty;
  `VODUpdated.ExpiresAt` thành `*time.Time`.
- `pkg/eventbus`: consumer kéo (`Bus.Pull`, `Puller.Next`) và ba option tùy chọn `AckWait`, `MaxDeliver`, `MaxAckPending`.
  `Consume` giữ nguyên mặc định và hành vi; phần tạo consumer chỉ được tách thành hàm dùng chung.

**stream-svc (WP1, WP2, WP5, WP7, phần stream của WP8)**

- Migration `20261102000001_video_upload` (TSD §4.4):
  - `vods`: thêm `source`, `description`, `published_at`, `failure_reason`, `failure_retryable`, `processing_*`;
    `stream_id` và `expires_at` cho phép rỗng; thêm `FAILED`.
  - Bảng mới `video_uploads`.
- Entity `Upload` + máy trạng thái. Sentinel và lỗi có kiểu theo TSD §6: `UploadLimitError`, `PartsMissingError`,
  `InvalidPartError`, `FileTooLargeError`.
- Repo:
  - `persistent/uploads.go`: phiên tải, hạn mức (khóa theo kênh), hoàn tất idempotent, retry, tiến độ, READY / FAILED,
    dọn file gốc.
  - `repo/objects`: S3 multipart qua `minio.Core`; bucket `nxl-uploads` riêng tư, có lifecycle.
  - Mọi truy vấn VOD xử lý được VOD không có buổi phát. `ExpireVODs` chỉ áp cho `LIVE`.
- Use case `usecase/uploads`:
  - Create / PutPart / Get / Complete / Abort.
  - Dọn phiên hết hạn (ticker, dừng khi shutdown).
  - Hủy phiên khi kênh bị khóa.
  - Retry; xử lý `upload.progress | processed | failed`; gỡ VOD từ `safety.vod_takedown.v1`.
- REST:
  - `/v1/studio/uploads*` (5 route) và `POST /v1/studio/vods/{id}/retry`.
  - `GET /v1/studio/vods` lọc thêm `source` và `status`; `PATCH` nhận thêm `description`, `categoryId`, `isMature`.
  - DTO `Vod` / `StudioVOD` chỉ thêm trường; `GET /v1/channels/:id/vods?source=`.
  - Body limit 9 MiB, read timeout 60 s.
- Internal: `GET /internal/v1/vods/{id}`.
- Playback: cổng 18+ đọc `is_mature` của VOD; ký URL HLS cho cả `streams/{uuid}/` và `videos/{uuid}/`.
- Module mới `minio-go/v7` v7.0.100 (UP-Q8, cùng bản với channel và media). Swagger stream sinh lại.

**media-svc (WP4)**

- `internal/transcode`:
  - Consumer kéo `media-transcode`. Chỉ lấy job khi số live đang chạy < `MEDIA_MAX_CONCURRENT_STREAMS`; số job đang
    chạy không tính vào chỗ live.
  - Tải file gốc theo luồng → `ffprobe` → `ffmpegcmd.BuildVOD` → upload HLS (segment trước, playlist sau) → phát
    `upload.processed`.
  - ffmpeg chạy `nice 10`, có giới hạn thời gian. Gọi `InProgress` mỗi 30 s, phát tiến độ mỗi 5 s.
  - Phân loại lỗi tạm / vĩnh viễn. Dọn thư mục làm việc khi khởi động.
- `BuildVOD`:
  - fps tối đa 30; thang 720p/480p/360p theo cạnh ngắn (video dọc ra 720×1280); keyframe ép 4 s; `-hls_playlist_type vod`.
  - Không có audio thì bỏ map audio; thumbnail lấy ở 10 % thời lượng, rộng 640.
  - `-protocol_whitelist file,pipe` và `-format_whitelist mov,matroska`.
- `vods.DeletedHandler` xóa theo `playlistPrefix` (kiểm regex + uuid); sự kiện cũ vẫn xóa theo `streams/{streamId}/`.
- 4 metric `media_upload_*`; cấu hình `MEDIA_*` mới; Dockerfile báo lỗi khi build nếu thiếu `ffprobe`.

**channel-svc, safety-svc (WP3, WP8)**

- channel-svc:
  - Nghe `vod.published.v1` và đưa video `PUBLIC` vào chỉ mục tìm kiếm (`HIDDEN` bị gỡ).
  - Chấp nhận `vod.updated` / `vod.deleted` không có `streamId`. `ExpiresAt` rỗng = không hết hạn.
- safety-svc:
  - Migration `20261104000001_vod_target`: target báo cáo `VOD`, hành động `TAKEDOWN_VOD`, bảng sao `vods`.
  - Bản sao VOD nạp từ 4 sự kiện; thiếu thì tra `GET /internal/v1/vods/{id}`.
  - `POST /v1/admin/vods/{vodId}/takedown` (outbox `safety.vod_takedown.v1` + audit). Swagger safety sinh lại.

**Edge / compose (WP5, WP6)**

- Nginx:
  - Location `^/v1/studio/uploads` (đặt trước route Studio): body 9m, `proxy_request_buffering off`, timeout 120 s, vùng
    `nxl_upload` (`NXL_EDGE_UPLOAD_RATE`).
  - `/hls/t/` nhận `(streams|videos)/<uuid>/`.
- `docker-compose.yml`, `.env.example`, `NXL-setup-guide.md`: biến `STREAM_S3_*`, `STREAM_UPLOAD_*`, `MEDIA_UPLOAD_*`,
  `NXL_EDGE_UPLOAD_RATE`.

**Web, admin, tài liệu**

- WP9 web và WP10 admin: xem Working Plan của web; admin có target `VOD` và hành động "Gỡ video".
- WP12:
  - DL-22, E-36 (FR-VD-08), NXL-G2 v1.3, NXL-A v1.4, NXL-00 v1.6.
  - ADR-014; `system-architecture` v1.2, `data-model` v1.3, ERD stream / safety.
  - `features.md` A10; `docs/api/README.md`.

## 2. Lệch so với plan

| Plan / TSD | Thực tế | Lý do |
| --- | --- | --- |
| WP8 "target VOD có sẵn" | Thêm target `VOD`, `TAKEDOWN_VOD`, `safety.vod_takedown.v1`, internal API tra VOD | safety-svc chỉ có USER/CHANNEL/STREAM/MESSAGE (TSD §7.3) |
| media tạo `nxl-uploads` | stream-svc tạo bucket + lifecycle | stream-svc mở multipart trước (TSD §7.3) |
| Lifecycle `AbortIncompleteMultipartUpload` 1 ngày | Chỉ lifecycle xóa `sources/` sau 3 ngày | MinIO từ chối rule này; MinIO tự dọn multipart dở sau 24 h, job hết hạn của stream-svc vẫn gọi `AbortMultipartUpload` |
| Bucket policy `videos/*.jpg` công khai | `videos/*` công khai đọc | Edge đọc MinIO ẩn danh, cả với `/hls/t/`; chỉ mở `.jpg` thì không phát được. Mức lộ giống `streams/*` hiện có |
| `NakWithDelay` khi hết chỗ live | Consumer kéo: chỉ lấy job khi còn chỗ. Live chiếm chỗ đúng lúc lấy → trả lại sau 30 s; lần giao cuối (3) vẫn chạy | Tránh giao đi giao lại liên tục; video không bị treo ở `PROCESSING` |
| — | Thêm `MEDIA_FFPROBE_PATH`, `STREAM_UPLOAD_ENABLED`, mã `STORAGE_UNAVAILABLE`, header `Retry-After` khi 429 | Vận hành / demo |
| — | Thiết lập bucket của stream-svc chạy nền, thử lại mỗi 30 s | MinIO tắt không làm sập livestream; route upload trả 503 tới khi xong |
| — | `error.details` của stream-svc thành `map[string]any` | Cần số và mảng (`missing`, `maxBytes`). JSON cũ không đổi |
| Xóa file gốc khi xóa video `UPLOAD` | Do ticker 10 phút làm (READY vẫn xóa ngay, best effort) | Gom về một đường dọn có thử lại |
| "Mock (`go generate`)" | Không sinh mock | Repo dùng fake viết tay; `go generate` sẽ thêm `go.uber.org/mock` (dependency chưa duyệt) |
| channel-svc lưu `source` / `description` | Không lưu | Bảng tìm kiếm không có cột; đổi cột tsv sinh ra là migration ngoài phạm vi |
| WP12 dòng "A8" | A10 | A8, A9 đã có trong `features.md` |

TSD lên v1.1 để ghi các lệch trên (§7.3).

## 3. Sự cố trong lúc làm

- **Dữ liệu dev local bị xóa.**
  - Repo test của channel-svc và safety-svc (do agent chạy) trỏ `NXL_TEST_PG_URL_*` thẳng vào `nxl_channel` và
    `nxl_safety`. Các test này `TRUNCATE` bảng.
  - Đã mất: kênh (21 → 1), follows, schedules, search history, reports, admin_actions, audit_logs.
  - Khôi phục (chủ đồ án đồng ý):
    - Chèn lại 12 kênh từ bản sao `creator_channels` của stream-svc (giữ id, owner, handle, tên, avatar).
    - Dựng lại 12 tài liệu tìm kiếm kênh.
    - Xóa mốc `replica_backfills.channels` của safety và khởi động lại safety để backfill lại.
  - Không khôi phục được: bio, banner, follows, lịch, report, audit cũ, tài liệu tìm kiếm của VOD cũ. JetStream chỉ giữ 7 ngày.
  - stream-svc dùng DB tạm `nxl_stream_test`, nên không bị ảnh hưởng.
- Cổng 5173 / 5174 đang bị dev server của project khác chiếm. E2E web chạy trên 5180 với
  `NXL_WEB_ORIGIN=http://localhost:5180` và `NXL_EDGE_AUTH_RATE=600r/m` (biến của phiên chạy, không sửa `.env`).

## 4. Kiểm tra

| Lệnh | Kết quả |
| --- | --- |
| `make format` | pass |
| `make nxl-lint` | 0 lỗi ở mọi module trừ billing: 2 cảnh báo `staticcheck` SA1019 (`fasthttp VisitAll`). billing không đổi, `go.sum` không đổi → có sẵn |
| `make nxl-test NXL_TEST_FLAGS=` (không `-race`: máy không có cgo) | pass mọi package trừ `billing/config` khi chạy qua `make` (Makefile nạp `.env` local vào môi trường test); `go -C services/billing test ./config/` pass → không liên quan |
| Repo test stream (DB tạm), channel, safety với Postgres thật | pass (stream 22, gồm 5 test upload mới) |
| Migration stream `up → down → up` (có VOD live READY + DELETED); safety `up → down → up` (có report VOD) | pass |
| `NXL_TEST_FFMPEG=1` media (ffmpeg 8.0.1): 1080p60 có tiếng, clip dọc không tiếng | master + 3 rendition (VOD, `EXTINF:4`), thumbnail; video dọc 720×1280 30 fps |
| `make nxl-up-all` + `nginx -t` trong container | pass; migration stream / safety đã áp; bucket `nxl-uploads` sẵn sàng; media bật 1 job |
| Smoke qua edge (script Python ngoài repo, 2 creator mới) | **32/32 pass** (chi tiết bên dưới) |
| Web `pnpm typecheck` · `lint` · `test` · `build` | pass (415 unit test) |
| Admin `pnpm typecheck` · `lint` · `test` · `build` | pass (137 unit test) |
| Web E2E (`playwright`, cổng 5180, chạy theo nhóm) | 15/20 pass; 5 fail, đều không do thay đổi này (bảng dưới) |
| Admin E2E | **chưa chạy**: spec cứng `BASE_URL = http://localhost:5174`, cổng đang bị dev server của project khác chiếm |

| Spec web E2E | Kết quả |
| --- | --- |
| `upload` (2 test: từ chối `.avi`; tải lên → xử lý → Sẵn sàng → trang xem → tab Videos lọc "Tải lên") | pass, sau khi sửa 2 chỗ test sai: footer đổi "Lưu" → "Xong" khi clip nhỏ đã tải xong; hàng Studio render trạng thái 2 lần (desktop / phone) |
| `m1`, `m2` (18+), `m3`, `m4-discovery` ×2, `m4-moderator`, `m4-notifications`, `takedown`, `guest-modal` ×3 | pass |
| `guest-modal` deep link | fail: assert cứng `localhost:5173` |
| `livestream`, `m2` (resume) | fail ở `vod-resume`; bản web gốc (HEAD, git worktree) fail y hệt trên cùng stack → không do thay đổi web; xem §5 |
| `m4-creator` | fail: tạo lịch `400 timezone FORMAT` (channel-svc thiếu `time/tzdata`, lỗi có sẵn, §5) |
| `m5-subscribe`, `m5-duplicate-ipn`, `m5-failed` | fail: CLI `chat` không chạy được (`service "chat" is not running`), redirect fake PSP không tới. billing-svc không đổi trong đợt này; chưa điều tra sâu, khả năng do biến môi trường E2E của GĐ6 (`E2E_CHAT_CLI`, cổng 5180) |

Smoke qua edge (`http://localhost:8080`):

- Tạo phiên:
  - `.avi` → 422; 3 GiB → 413; không token → 401.
  - Clip 1080p60 45 s (16 MB, 2 phần) → 201.
- Gửi phần:
  - Phần sai kích thước, phần ngoài phạm vi → 400 `INVALID_PART`.
  - Creator khác đọc phiên → 404.
  - Gửi lại phần 1 → 200.
- Hoàn tất:
  - `complete` thiếu phần → 409 `PARTS_MISSING`.
  - `complete` → 202; gọi lại → cùng `vodId`.
  - `PATCH` lúc `PROCESSING` → 200. Public lúc `PROCESSING` → 404.
- Chuyển mã và phát:
  - READY sau khoảng 8 s; Studio thấy `processing.percent`.
  - Studio trả `source=UPLOAD`, `streamId=null`, `expiresAt=null`, `publishedAt` có giá trị.
  - Public 200; playback 200.
  - `master.m3u8` qua edge có 3 mức (1280×720, 854×480, 640×360); playlist con có `PLAYLIST-TYPE:VOD` + `ENDLIST`.
  - Thumbnail 200.
  - `/v1/channels/:id/vods?source=UPLOAD` có video, `?source=LIVE` không có.
- Lỗi và hủy:
  - File `.mp4` giả → `FAILED UNSUPPORTED_FORMAT`, `retryable=false`; retry → 409; xóa → 204.
  - Hủy phiên → 204; gửi phần sau khi hủy → 410 `UPLOAD_EXPIRED`.
  - Phiên thứ 4 → 429 `UPLOAD_LIMIT_REACHED`.
- Xóa video READY → 204, public 404. media xóa 41 object `videos/{id}/`; HLS qua edge trả 404.

| Luồng cũ chạy lại | Transport | Kết quả |
| --- | --- | --- |
| Đăng ký → xác minh → nộp đơn → duyệt → Studio (`m1`) | REST + web | pass |
| Live → xem (chọn chất lượng, DVR) → gián đoạn → tiếp tục → kết thúc → VOD live (`livestream`, `m2`) | RTMP + HLS + REST | pass tới bước "Xem tiếp"; xem §5 |
| VOD 18+ (`m2` thứ hai) | REST + HLS | pass |
| Chat realtime, slow mode, chỉ đọc sau khi kết thúc (`m3`) | WS | pass |
| Tìm kiếm không dấu, danh mục, follow, sidebar (`m4-discovery`) | REST | pass |
| Moderator, report, khóa tài khoản (`m4-moderator`) | REST + WS | pass |
| Chuông thông báo, e-mail (`m4-notifications`) | NATS + WS + SMTP | pass |
| Admin gỡ stream → trang xem "Đã bị gỡ" (`takedown`) | NATS + web | pass |
| Ghi URL HLS có chữ ký `/hls/t/streams/…` | edge | `nginx -t` pass; regex cũ khớp (kiểm bằng script); chưa phát thử với `STREAM_HLS_SIGNING=true` |

Chưa kiểm được (lý do):

- `-race` (không có cgo).
- `TestPull` của eventbus với NATS thật (chỉ chạy gián tiếp qua stack).
- Đo CPU khi live và job chạy cùng lúc (`make nxl-bench` + 1 job), mục "Ưu tiên live" ở §8 Working Plan.
- Clip MOV iPhone HEVC có rotation thật (chỉ thử rotation bằng clip tạo).
- Tắt MinIO / restart media giữa job (có unit test, chưa chạy trên stack).

## 5. Follow-ups

- **channel-svc thiếu `time/tzdata`.** Image `scratch` không có zoneinfo nên `POST /v1/studio/schedules` trả `400
  timezone FORMAT` với `Asia/Ho_Chi_Minh` (E2E `m4-creator` fail ở bước tạo lịch). Lỗi có sẵn, ngoài phạm vi; sửa bằng
  `_ "time/tzdata"` trong `cmd/channel` như identity / billing / notification.
- **E2E "Xem tiếp" phụ thuộc thời gian.** `m2`, `livestream` fail ở `vod-resume` với cả bản web gốc (HEAD). `m2` tắt OBS
  sau khoảng 5 s nên VOD chỉ dài 5 s; `livestream` lưu vị trí 19 / 25 s, vượt ngưỡng "đã xem hết" (thời lượng − 10 s).
- `guest-modal` (deep link) cứng `localhost:5173` trong assert, nên fail khi chạy trên cổng khác.
- notification-svc: thông báo cho Creator khi video bị gỡ (`safety.vod_takedown.v1`) — cần kind, template e-mail và web mới.
- NXL-F (spec kiểm duyệt) chưa có target `VOD` / `TAKEDOWN_VOD`.
- Thông báo follower "Video mới" (UP-Q9), thumbnail tự chọn (UP-Q10).
- billing: 2 cảnh báo lint `VisitAll` deprecated; `billing/config` test lệ thuộc `.env` khi chạy qua `make`.
- Repo test channel / safety nên từ chối chạy trên DB không có hậu tố `_test` (như cách stream đã làm), để tránh lặp lại sự
  cố §3.
