# Tải video lên kênh và chuyển mã adaptive bitrate (HLS)

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| **Full** (migration `nxl_stream`; `config.go` của stream và media; sự kiện mới trong `pkg/events`; route Nginx mới; `docker-compose.yml`; thêm module MinIO vào stream-svc; nhiều service: stream, media, channel, safety, notification; web) | **Approved** 2026-11-04 — §10 chốt theo cột "Đề xuất"; TSD `docs/plans/technical/2026-11-02-video-upload-abr.md` | 2026-11-02 |

**Bối cảnh.** GĐ0–GĐ6 đã xong. Đến nay video trên kênh chỉ có một nguồn: bản ghi lại của buổi live
(`vod.processed.v1` → `stream.vods`). Tính năng này cho Creator **tải một file video lên** trong Studio. Server chuyển mã file
thành HLS nhiều mức chất lượng (720p/480p/360p, cùng thang bitrate với live). Sau đó video hiện ở tab Videos của kênh và phát
bằng player VOD hiện có, có chọn chất lượng tự động (ABR).

**Đây là phạm vi mới, chưa có trong PRD v2.1.** PRD §4.10 chỉ có VOD sinh từ buổi live và Clip. Brief GĐ4 (dòng 54) và
NXL-G2 §11 đều ghi rõ "tải video lên" nằm ngoài phạm vi. Vì vậy cần một quyết định DL-22 (UP-Q1) trước khi làm. Lộ trình vẫn còn
chỗ: GĐ7 bắt đầu 15/02/2027.

Đây là **kế hoạch**, chưa có code. Sau khi duyệt, bước tiếp theo theo `AGENTS.md` là:

- TSD `docs/plans/technical/2026-11-02-video-upload-abr.md` (hợp đồng API, DDL, sơ đồ tuần tự cho từng transport);
- Working Plan phía web, cùng basename, trong `../nexuslive-web/docs/plans/working/`.

## 1. Task Brief

- **Mục tiêu:**
  - Creator chọn một file video (MP4 / MOV / MKV / WebM, tối đa 2 GiB, tối đa 2 giờ — UP-Q4), nhập tiêu đề, mô tả, danh mục,
    18+, chế độ hiển thị, rồi tải lên.
  - File tải lên theo từng phần (chunk). Mạng chập chờn thì phần lỗi được gửi lại, không phải gửi lại cả file. Người dùng thấy
    phần trăm tải.
  - Tải xong, video vào trạng thái **Đang xử lý**. Studio thấy tiến độ chuyển mã. Chuyển mã xong, video thành **Sẵn sàng**,
    xuất hiện ở tab Videos và trang `/vod/:id`, phát được 720p/480p/360p + Tự động.
  - **Live luôn được ưu tiên.** Chuyển mã video tải lên không được làm hỏng hay từ chối buổi live đang phát (ADR-012:
    ~0,7–0,9 core cho mỗi luồng).
  - Video tải lên **không tự hết hạn** như bản ghi live 14 ngày (UP-Q3). Creator đổi thông tin, ẩn hoặc xóa được.
- **Nguồn:**

  | Nguồn | Phần dùng |
  | --- | --- |
  | PRD v2.1 §4.10 (FR-VD-01, FR-VD-05), §4.12 Studio "Content" | Trang VOD, nhãn nội dung, quản lý nội dung |
  | NXL-A §7C (VOD: `PROCESSING → AVAILABLE / FAILED`) | Máy trạng thái có sẵn; thêm trigger "Creator tải lên" |
  | NXL-G2 §3.3, §7.2, §13.1 | Quản lý VOD trong Studio; thêm §3.8 "Tải video lên" |
  | ADR-004, ADR-005, ADR-012 | Media pipeline tự làm, MinIO, giới hạn luồng đồng thời |
  | `services/media/internal/{ffmpegcmd,ladder,uploader,storage}` | Thang bitrate, cách ghi HLS lên MinIO |

- **Trong phạm vi:** WP1–WP12 ở §2.
- **Ngoài phạm vi:**
  - Cắt / ghép video, chỉnh sửa trong trình duyệt.
  - Phụ đề.
  - Lên lịch công khai (premiere).
  - Clip, Highlight.
  - Chương (chapters).
  - Ảnh thumbnail tự chọn (UP-Q10 — làm P1 nếu kịp).
  - Thông báo "Video mới" cho follower (UP-Q9).
  - Tải nhiều file cùng lúc trong một hộp thoại.
  - 1080p (UP-Q6).
  - DRM, watermark.
  - Nhận dạng bản quyền.
  - Tải lên từ URL.
  - Chunk song song.
  - Tải tiếp sau khi đóng tab hay đổi máy.
- **Giả định:**
  - **G1 — Đường tải:** client gửi từng phần 8 MiB qua API của stream-svc (`PUT …/parts/{n}`). stream-svc chuyển thẳng phần đó
    vào **S3 multipart upload** của MinIO. Không cấp presigned URL cho trình duyệt (UP-Q2): cùng một đường `/v1` chạy được qua
    Vite proxy, edge và tunnel, xác thực bằng Bearer như mọi API khác. Giới hạn body chỉ cần nới cho một route.
  - **G2 — Ai làm gì:**
    - stream-svc giữ phiên tải (`video_uploads`) và VOD.
    - media-svc chuyển mã, vì nó đã có ffmpeg, thang bitrate và uploader HLS.
    - Hai bên nói chuyện qua sự kiện NATS (outbox).
  - **G3 — Nơi lưu:**
    - File gốc lưu ở bucket riêng tư mới `nxl-uploads`, khóa `sources/{uploadId}`.
    - HLS lưu ở bucket `nxl-media`, prefix **mới** `videos/{vodId}/`. Prefix `streams/` có lifecycle xóa sau 14 ngày nên không
      dùng được.
  - **G4 — Mô hình dữ liệu:** `vods.stream_id` được phép rỗng, thêm cột `source` (`LIVE | UPLOAD`) (UP-Q12). Không tạo hàng
    `streams` giả.
  - **G5 — Hàng đợi:** dùng chính NATS JetStream (durable consumer của media-svc, `MaxAckPending = 1`). Không thêm Redis queue
    hay thư viện job.
  - **G6 — Thang bitrate:** dùng `ladder.For(cạnh ngắn)` như live: 720p/480p/360p, tối đa 720p (quy tắc GĐ2 số 4). Video dọc
    tính theo cạnh ngắn.
  - **G7 — Tiến độ:** Studio hỏi lại mỗi 3 giây (polling) trong lúc có video đang xử lý. Không đẩy qua WebSocket (UP-Q11).
- **Câu hỏi mở:** §10. Chặn công việc: **UP-Q1** (nhận phạm vi mới), **UP-Q8** (thêm module `minio-go` vào stream-svc —
  Prohibition 1).

## 2. Gói việc

### WP1 — Dữ liệu và trạng thái (stream-svc, migration mới)

Migration `services/stream/migrations/<ts>_video_upload.{up,down}.sql`. Không sửa migration cũ.

- **`vods`:**
  - `stream_id` bỏ `NOT NULL`, giữ `UNIQUE`.
  - Thêm `source varchar(8) NOT NULL DEFAULT 'LIVE' CHECK (source IN ('LIVE','UPLOAD'))`.
  - `CHECK ((source = 'LIVE') = (stream_id IS NOT NULL))`.
  - `description text` (≤ 5.000 ký tự, kiểm ở DTO).
  - `expires_at` bỏ `NOT NULL`, kèm `CHECK (source = 'UPLOAD' OR expires_at IS NOT NULL)`.
  - `published_at timestamptz` (lúc chuyển `READY`).
  - `failure_reason varchar(32)`.
  - `processing_percent smallint` (0–100).
  - Mở rộng CHECK `status`: thêm `FAILED`.
  - Index `vods_processing_idx (channel_id) WHERE status IN ('PROCESSING','FAILED')`.
- **Bảng mới `video_uploads`:** `id`, `channel_id`, `user_id`, `vod_id` (FK, NULL tới khi hoàn tất), `file_name`,
  `content_type`, `size_bytes`, `part_size`, `part_count`, `s3_upload_id`, `object_key`, `status`
  (`UPLOADING | COMPLETED | ABORTED | EXPIRED`), `parts jsonb` (số phần → ETag, kích thước), `created_at`, `updated_at`,
  `expires_at` (+24 h).
- **Entity:**
  - Thêm `SourceLive/SourceUpload`, `VODProcessing`, `VODFailed`.
  - `VOD` thêm `Source`, `Description`, `PublishedAt`, `FailureReason`, `ProcessingPercent`; `StreamID` thành con trỏ.
  - Entity `Upload` + máy trạng thái (§7.1 của TSD).
- **Repo:**
  - `vods`: tạo `PROCESSING`, cập nhật tiến độ, chuyển `READY` / `FAILED`.
  - `uploads`: tạo, ghi phần, hoàn tất, hủy, quét hết hạn.
  - `ExpireVODs` chỉ chạy với `source = 'LIVE'`.
- **Playback:** `playback.media()` đọc `IsMature` **từ VOD**, không tải hàng `streams` nữa (GĐ4 đã có `vods.is_mature`). Nhờ
  vậy mọi VOD không có `stream_id` đều phát được.

### WP2 — API tải lên (stream-svc, REST qua edge)

Nhóm `/v1/studio/uploads`, cùng middleware `auth.Required` + `requireCreator`. Hợp đồng đầy đủ nằm trong TSD.

| Method | Path | Mô tả |
| --- | --- | --- |
| POST | `/v1/studio/uploads` | `{fileName, sizeBytes, contentType, title, description?, categoryId?, isMature, visibility}` → `201 {upload: {id, status, partSize, partCount, uploadedParts: [], expiresAt}}`. Kiểm đuôi / MIME, kích thước, hạn mức; mở S3 multipart |
| PUT | `/v1/studio/uploads/{id}/parts/{n}` | Body nhị phân (`application/octet-stream`), đúng `partSize` (phần cuối nhỏ hơn) → `200 {partNumber, receivedBytes}`. Gửi lại cùng `n` thì ghi đè (idempotent) |
| GET | `/v1/studio/uploads/{id}` | Trạng thái + `uploadedParts[]` (để client gửi tiếp phần còn thiếu) |
| POST | `/v1/studio/uploads/{id}/complete` | Đủ phần thì `CompleteMultipartUpload` → tạo VOD `PROCESSING` + outbox `upload.completed.v1` (cùng giao dịch) → `202 {vodId}`. Thiếu phần → `409 PARTS_MISSING` (`details.missing`) |
| DELETE | `/v1/studio/uploads/{id}` | Hủy, `AbortMultipartUpload` → `204` |

Studio VOD có sẵn (`GET/PATCH/DELETE /v1/studio/vods…`), mở rộng thêm:

- `GET` lọc thêm `source=live|upload` và `status=processing|failed`; trả `source`, `processing {percent}`, `failureReason`.
- `PATCH` nhận thêm `description`, `categoryId`, `isMature` với video `UPLOAD`. Chỉ đổi được khi `READY`, hoặc `PROCESSING` cho
  phần metadata.
- `POST /v1/studio/vods/{id}/retry` — video `FAILED` do lỗi tạm thời, khi file gốc còn giữ (UP-Q5). P1.

API công khai `Vod` (`/v1/vods/:id`, `/v1/channels/:id/vods`, `/v1/vods`) thêm `source`, `description`, `publishedAt`;
`streamId` và `expiresAt` thành nullable. Chỉ trả video `READY` + `PUBLIC`, như hiện nay.

Cấu hình mới của stream-svc:

| Biến | Mặc định |
| --- | --- |
| `STREAM_S3_ENDPOINT` · `_ACCESS_KEY` · `_SECRET_KEY` · `_USE_SSL` | MinIO, như channel-svc |
| `STREAM_UPLOAD_BUCKET` | `nxl-uploads` |
| `STREAM_UPLOAD_MAX_BYTES` | 2 GiB |
| `STREAM_UPLOAD_PART_SIZE` | 8 MiB |
| `STREAM_UPLOAD_TTL` | 24h |
| `STREAM_UPLOAD_MAX_ACTIVE` | 3 (đang tải + đang xử lý / kênh) |
| `STREAM_UPLOAD_DAILY_MAX` | 10 |
| `STREAM_UPLOAD_EXPIRY_TICK` | 10m |

`pkg/httpserver` đã có option `BodyLimit` và `ReadTimeout`. stream-svc đặt body 9 MiB, read timeout 60 s.

### WP3 — Sự kiện (`pkg/events`, NXL-00 §11)

| Sự kiện | Phát | Nghe | Payload |
| --- | --- | --- | --- |
| `upload.completed.v1` (mới) | stream (outbox) | media (durable `media-transcode`, `MaxAckPending = 1`) | `uploadId, vodId, channelId, bucket, objectKey, sizeBytes` |
| `upload.progress.v1` (mới) | media (mỗi 5 s, không bắt buộc tới nơi) | stream | `vodId, percent, stage (PROBING\|TRANSCODING\|UPLOADING)` |
| `upload.processed.v1` (mới) | media | stream | `vodId, durationSec, playlistPath (videos/{vodId}/master.m3u8), thumbnailPath, renditions[], sourceWidth, sourceHeight` |
| `upload.failed.v1` (mới) | media | stream | `vodId, reason (UNSUPPORTED_FORMAT\|NO_VIDEO_STREAM\|DURATION_TOO_LONG\|FILE_CORRUPT\|TRANSCODE_FAILED\|STORAGE_ERROR), retryable` |
| `vod.published.v1` (mới) | stream (outbox, khi `UPLOAD` → `READY`) | channel (chỉ mục tìm kiếm), notification (UP-Q9) | như `StreamVODReady` + `source`, không có `streamId` |
| `vod.updated.v1` · `vod.deleted.v1` (đổi, chỉ thêm) | stream | channel, safety, media | `streamId` thành `omitempty`; thêm `source`, `playlistPrefix` |

Bên nghe phải sửa cùng thay đổi (Prohibition 16):

- **channel-svc:** `VODDoc.StreamID` thành tùy chọn; khóa chống trùng dùng `vodId`.
- **safety-svc:** bỏ kiểm `streamId` phải là UUID khi `source = UPLOAD`.
- **media-svc:** `vods.DeletedHandler` xóa theo `playlistPrefix` (`streams/{id}/` hoặc `videos/{id}/`) thay vì tự ghép
  `streams/`.
- **chat-svc:** không đổi. Nó chỉ nghe `stream.vod_ready.v1`, sự kiện này vẫn dành riêng cho live.

### WP4 — Chuyển mã video tải lên (media-svc)

- **Package `internal/transcode`:**
  1. Nhận `upload.completed.v1`.
  2. Kiểm còn chỗ (xem bên dưới); không còn thì `NakWithDelay(30s)`.
  3. Tải file gốc từ MinIO **theo luồng** (stream) xuống `MEDIA_WORK_DIR/uploads/{vodId}/source` — không đọc cả file vào RAM.
  4. `ffprobe`: kiểm có luồng video, thời lượng, codec giải mã được, chiều quay (rotation).
  5. Chạy ffmpeg.
  6. Upload HLS.
  7. Phát `upload.processed.v1`.
  8. Ack.
  - Trong lúc chạy, gọi `msg.InProgress()` mỗi 30 s để JetStream không giao lại.
- **`ffmpegcmd.BuildVOD(Options)`** (hàm mới, không đổi `Build` của live):
  - Input là file; giữ fps nguồn nhưng tối đa 30; `fps=…,split`, `scale=-2:H` theo `ladder.For(cạnh ngắn)`.
  - x264 `-preset ${MEDIA_UPLOAD_X264_PRESET}` (mặc định `veryfast`), không `-tune zerolatency`.
  - GOP và keyframe ép đúng `-hls_time 4`.
  - `-hls_playlist_type vod`, `-hls_flags independent_segments`, segment `.ts`.
  - Không có audio thì bỏ map audio (builder đã có `HasAudio`).
  - Thumbnail lấy 1 khung ở 10% thời lượng, `scale=640:-2` (PRD: thumbnail ≥ 640×360).
- **Uploader:**
  - Tái dùng `internal/uploader` (thứ tự segment trước, playlist sau; Cache-Control bất biến).
  - Thêm `Put` dạng luồng (`PutObject` với `io.Reader`) cho file lớn.
  - Prefix `videos/{vodId}/`.
- **Chỗ chạy và ưu tiên live** (UP-Q7):
  - Mỗi lúc tối đa `MEDIA_MAX_UPLOAD_TRANSCODES = 1` job.
  - Chỉ bắt đầu job khi `live đang chạy < MEDIA_MAX_CONCURRENT_STREAMS`.
  - Đếm chỗ của live **không** tính job upload, nên live không bao giờ bị từ chối vì job upload.
  - ffmpeg của job chạy `nice 10` để nhường CPU cho live.
  - Live bắt đầu khi đang có job: job vẫn chạy tiếp nhưng chậm hơn, và metric `media_upload_transcode_speed` ghi lại.
- **Lỗi và khôi phục:**
  - Lỗi tạm (MinIO, mất kết nối) thử lại tối đa 2 lần; sau đó phát `upload.failed.v1` với `retryable = true`.
  - Lỗi vĩnh viễn (định dạng, thời lượng) thì `retryable = false`.
  - Khởi động lại media-svc: xóa `work/uploads/*` dở dang; JetStream giao lại tin chưa ack nên job chạy lại từ đầu.
- **`storage.EnsureBucket`:**
  - Policy đọc công khai thêm `nxl-media/videos/*.jpg`.
  - **Không** thêm lifecycle cho `videos/`.
  - Tạo bucket `nxl-uploads` riêng tư, lifecycle `AbortIncompleteMultipartUpload` sau 1 ngày và xóa `sources/` sau 3 ngày.
- **Cấu hình mới:** `MEDIA_MAX_UPLOAD_TRANSCODES`, `MEDIA_UPLOAD_X264_PRESET`, `MEDIA_UPLOAD_MAX_DURATION` (2h),
  `MEDIA_UPLOAD_BUCKET`, `MEDIA_UPLOAD_SEGMENT_SEC` (4).
- **Metric:** `media_upload_jobs_total{result}`, `media_upload_transcode_seconds`, `media_upload_transcode_speed`,
  `media_upload_queue_wait_seconds`.

### WP5 — Phát và ký URL cho prefix mới (stream-svc + edge)

- **stream-svc:** `viewer.go` `streamDir` mở rộng nhận `videos/{uuid}/` ngoài `streams/{uuid}/`; `pkg/hlssign` ký theo prefix
  thư mục như cũ.
- **Nginx:**
  - Regex của `location ^~ /hls/t/` và `/hls/` chấp nhận `(streams|videos)/[0-9a-f-]{36}/`.
  - Ảnh `.jpg` dưới `videos/` công khai như `streams/`.
- **VOD tải lên phát bằng `mode = vod`** của `HlsPlayer`. Player đã đọc danh sách mức từ master playlist nên không phải sửa.

### WP6 — Cổng vào cho tải lên (Nginx edge)

- Location mới `~ ^/v1/studio/uploads(/|$)` → stream, đặt **trước** `^/v1/(studio|streams|vods)`:
  - `client_max_body_size 9m`;
  - `proxy_request_buffering off` (không đệm 8 MiB vào đĩa của Nginx);
  - `proxy_read_timeout 120s`, `proxy_send_timeout 120s`.
- Vùng rate mới `nxl_upload`: `NXL_EDGE_UPLOAD_RATE=300r/m`, burst 20. Vùng `nxl_write` 60 r/m sẽ chặn một file 2 GiB
  (256 phần).
- `413` → `PAYLOAD_TOO_LARGE` như hiện có.

### WP7 — Dọn dẹp và hạn mức (stream-svc)

- Job `STREAM_UPLOAD_EXPIRY_TICK`: phiên `UPLOADING` quá `expires_at` → `AbortMultipartUpload`, `EXPIRED`.
- Hạn mức: tối đa `STREAM_UPLOAD_MAX_ACTIVE` phiên tải + video đang xử lý mỗi kênh; `STREAM_UPLOAD_DAILY_MAX` lần tạo mỗi ngày
  → `429 UPLOAD_LIMIT_REACHED`.
- Kênh bị khóa (`CHANNEL_SUSPENDED`) không tạo phiên tải được; phiên đang tải bị hủy khi nhận `channel.suspended`.
- Xóa video `UPLOAD` → `vod.deleted.v1` → media xóa `videos/{vodId}/`. File gốc (nếu còn) do stream-svc xóa.

### WP8 — An toàn và kiểm duyệt

- **safety-svc:**
  - Report VOD dùng `target = VOD` có sẵn.
  - Admin gỡ video tải lên bằng hành động gỡ VOD theo `vodId` (`deleteVODsOf` hiện chỉ đi theo `streamId` — thêm nhánh theo
    VOD).
  - Audit log như gỡ stream.
- **Kiểm định dạng hai lớp:** lớp 1 ở API (đuôi + MIME + kích thước); lớp 2 ở media (`ffprobe` là nguồn đúng). Tên file chỉ dùng
  để hiển thị, không bao giờ dùng làm đường dẫn.
- Không chạy file người dùng gửi như script. ffmpeg chạy với `-protocol_whitelist file` để file playlist độc hại không gọi ra
  mạng.

### WP9 — Web người dùng (`nexuslive-web`, Working Plan riêng cùng basename)

- **Studio → Nội dung:**
  - Nút **Tải video lên** ở `StudioPageHeader`.
  - Bộ lọc thêm "Đang xử lý" / "Lỗi" và nguồn "Phát trực tiếp" / "Tải lên".
  - Hàng video có trạng thái Đang tải X% / Đang xử lý X% / Lỗi + lý do / Sẵn sàng.
- **Hộp thoại tải lên:**
  1. Vùng kéo thả.
  2. Kiểm phía client (đuôi, ≤ 2 GiB).
  3. Form chi tiết (tiêu đề lấy sẵn từ tên file, mô tả, danh mục, 18+, hiển thị).
  4. Thanh tiến độ.
  5. Đóng hộp thoại được — việc tải tiếp tục ở thanh mini cuối màn hình trong Studio.
- **`lib/upload-client.ts`:**
  - Gửi tuần tự từng phần bằng `XMLHttpRequest` để có `upload.onprogress`.
  - Thử lại phần lỗi 3 lần (backoff).
  - Hết hạn token thì refresh rồi gửi lại **chỉ phần đó**. `api-client` hiện gửi lại cả body sau 401 nên không dùng nó cho phần
    nhị phân.
  - Rời trang khi đang tải → cảnh báo `beforeunload`.
- **Kiểu dữ liệu:**
  - `StudioVod`, `Vod` thêm `source`, `description`, `publishedAt`, `processing`, `failureReason`.
  - `streamId`, `expiresAt` nullable.
  - Trạng thái thêm `PROCESSING | FAILED`.
- **Trang VOD:**
  - Video tải lên hiện nhãn **"Video"** thay cho "Xem lại".
  - "Đăng ngày …" thay cho "Phát ngày …".
  - Hiện mô tả; không có dòng "hết hạn sau N ngày".
- **Kênh → Videos:** lọc "Tất cả / Phát trực tiếp / Tải lên". `VodCard` nhận nhãn theo `source`.
- **Component mới:** `Progress` (chưa có trong `components/ui`), `UploadDropzone` (phát triển từ `ImageField`),
  `UploadTray`.
- **i18n:** `vi`/`en` cho `studio.upload.*`, `vod.*`. Sửa câu trống ở trang Nội dung ("VOD chỉ đến từ buổi phát…").

### WP10 — Web quản trị (`nexuslive-admin`)

- Chi tiết report có target VOD hiện được video tải lên (không có buổi phát gốc) và hành động "Gỡ video". Thay đổi nhỏ, không
  có trang mới.

### WP11 — Thiết kế

- Brief cho agent design: `DATN/docs/design/2026-11-02-design-brief-video-upload.md` (đã soạn cùng plan này, chờ duyệt).

### WP12 — Tài liệu

| Tài liệu | Thay đổi |
| --- | --- |
| `DATN/docs/ba/analysis/decision-log.md` | **DL-22** nhận phạm vi "Tải video lên" + chốt UP-Q1…Q12 |
| PRD v2.1 → errata `E-36` | FR-VD-08 "Creator tải video lên" (P1 → làm trong đồ án) |
| NXL-G2 → v1.3 | §3.8 "Tải video lên"; bỏ dòng "tải VOD lên" ở §11; §7 trạng thái phiên tải; §13.1 API; §13.4 sự kiện; ST-Q11… |
| NXL-A → v1.4 | §7C thêm trigger "Creator tải lên → PROCESSING", `FAILED`; API 03 thêm `source`, `description` |
| NXL-00 → v1.6 | §5 mã lỗi mới; §11 sự kiện `upload.*`, `vod.published.v1`; §12 hằng số |
| `docs/architecture/decisions.md` | **ADR-014** Tải lên theo phần qua API + hàng đợi chuyển mã JetStream, live được ưu tiên |
| `docs/architecture/system-architecture.md` | §7 media pipeline thêm luồng upload; sửa danh sách bucket cũ (`hls-live`, `vod`… → `nxl-media`, `nxl-public`, `nxl-uploads`) |
| `docs/architecture/data-model.md`, ERD | `vods` mới, `video_uploads` |
| `docs/api/README.md`, Swagger | Endpoint mới |
| `docs/product/features.md` | Dòng mới A8 "Tải video lên + ABR" |
| `README.md`, `.env.example`, `NXL-setup-guide.md` | Biến mới, bucket mới |

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Gói |
| --- | --- | --- |
| migration | `services/stream/migrations/<ts>_video_upload.*` | WP1 |
| entity | `services/stream/internal/entity/entity.go` (`VOD`, `Upload`) | WP1 |
| repo | `services/stream/internal/repo/{contracts.go,persistent/}`, `repo/objects` (mới) | WP1, WP2 |
| use case | `services/stream/internal/usecase/{vods,playback,uploads (mới)}` | WP1, WP2, WP7 |
| controller | `services/stream/internal/controller/restapi/v1/{studio_uploads.go (mới), studio_vods.go, viewer.go, v1.go}`, `controller/events` | WP2, WP3, WP5 |
| config / wiring | `services/stream/config/config.go`, `internal/app/app.go` | WP2 |
| media | `services/media/internal/{transcode (mới), ffmpegcmd, uploader, storage, vods, app, config}` | WP4 |
| sự kiện | `pkg/events/events.go` | WP3 |
| bên nghe | `services/channel/internal/controller/events`, `services/safety/internal/controller/events`, `services/safety/internal/usecase` | WP3, WP8 |
| edge | `deploy/nginx/nginx.conf`, `templates/nxl-edge.conf.template` | WP5, WP6 |
| compose | `docker-compose.yml` (env S3 cho stream, env upload cho media, `NXL_EDGE_UPLOAD_RATE`) | WP2, WP4, WP6 |
| web | `nexuslive-web/src/features/{studio,vod,channels,discover,streams}`, `lib/upload-client.ts`, `components/ui/progress.tsx` | WP9 |
| admin | `nexuslive-admin/src/features/moderation` | WP10 |

| Transport | Có | Ghi chú |
| --- | --- | --- |
| REST công khai qua edge | Có | `/v1/studio/uploads/*` mới; DTO `Vod` / `StudioVod` thêm trường (không đổi tên, không bỏ) |
| HTTP nội bộ | Không | Không thêm `/internal` |
| NATS | Có | 5 sự kiện mới; `vod.updated/deleted` thêm trường, `streamId` thành tùy chọn |
| HLS (edge → MinIO) | Có | Prefix `videos/` |

**Luồng cũ có thể vỡ:**

- **Live → VOD** (`vod.processed` → `OnVODProcessed`): migration đổi ràng buộc `vods`. Hàng cũ có `source` mặc định `LIVE`,
  `stream_id` khác rỗng nên thỏa CHECK mới.
- **Hết hạn VOD:** `ExpireVODs` phải lọc `source = 'LIVE'`, nếu không video tải lên (`expires_at` rỗng) có thể bị xử lý sai.
- **Phát VOD:** `playback.media()` bỏ phụ thuộc `streams.Get`. Kiểm lại cổng 18+ với VOD live (`vods.is_mature` sao từ stream ở
  GĐ4).
- **Ký URL HLS:** sửa regex Nginx có thể làm hỏng `/hls/t/streams/...`. Chạy lại E2E livestream.
- **Gỡ nội dung của Admin:** `deleteVODsOf` theo `streamId`.
- **Chỉ mục tìm kiếm** của channel-svc và bản sao VOD của safety-svc: khi `streamId` rỗng.
- **Xóa file HLS** của media-svc: `vods.DeletedHandler` hiện bắt buộc `streamId` là UUID.

## 4. Mã lỗi

| Mã | HTTP | Khi nào |
| --- | --- | --- |
| `FILE_TOO_LARGE` (mới) | 413 | `sizeBytes > STREAM_UPLOAD_MAX_BYTES` |
| `UNSUPPORTED_FORMAT` (mới) | 422 | Đuôi / MIME không thuộc MP4, MOV, MKV, WebM |
| `UPLOAD_LIMIT_REACHED` (mới) | 429 | Vượt `STREAM_UPLOAD_MAX_ACTIVE` hoặc `STREAM_UPLOAD_DAILY_MAX`; `details.limit`, `details.retryAfterSec` |
| `UPLOAD_EXPIRED` (mới) | 410 | Gửi phần / hoàn tất khi phiên đã `EXPIRED` / `ABORTED` |
| `PARTS_MISSING` (mới) | 409 | `complete` khi còn thiếu phần; `details.missing: [n…]` |
| `INVALID_PART` (mới) | 400 | Số phần ngoài `1…partCount` hoặc kích thước sai |
| `PAYLOAD_TOO_LARGE` | 413 | Có sẵn (edge) |
| `NOT_CREATOR`, `CHANNEL_SUSPENDED`, `NOT_FOUND`, `INVALID_STATE`, `VALIDATION_FAILED` | — | Có sẵn |

Lý do thất bại của chuyển mã (`failureReason`, không phải mã HTTP): `UNSUPPORTED_FORMAT`, `NO_VIDEO_STREAM`,
`DURATION_TOO_LONG`, `FILE_CORRUPT`, `TRANSCODE_FAILED`, `STORAGE_ERROR`.

## 5. Sinh code, cấu hình, tài liệu

- [ ] TSD `docs/plans/technical/2026-11-02-video-upload-abr.md` trước khi code.
- [ ] Mock (`go generate`) cho `contracts.go` của stream; `make nxl-swag`.
- [ ] Migration `up → down → up`.
- [ ] Biến mới vào `.env.example` + `docker-compose.yml`:
  - stream: `STREAM_S3_*`, `STREAM_UPLOAD_*`
  - media: `MEDIA_MAX_UPLOAD_TRANSCODES`, `MEDIA_UPLOAD_*`
  - edge: `NXL_EDGE_UPLOAD_RATE`
- [ ] Module mới: `github.com/minio/minio-go/v7` cho module `services/stream`. Đã có trong `go.work` (channel, media) nhưng
  vẫn là dependency mới của module này, cần duyệt (UP-Q8). Sau đó `make deps` + `make deps-audit`.

## 6. Thứ tự

| # | Việc | Phụ thuộc | Ước lượng |
| --- | --- | --- | --- |
| 1 | Chốt §10, ghi DL-22 | — | 0,5 ngày |
| 2 | TSD + cập nhật spec (WP12 phần BA) | 1 | 1,5 ngày |
| 3 | Design (WP11), chạy song song | 1 | — (agent design) |
| 4 | WP1 migration + entity + repo; playback đọc `is_mature` từ VOD | 2 | 1,5 ngày |
| 5 | WP3 sự kiện + sửa bên nghe (channel, safety, media delete) | 4 | 1 ngày |
| 6 | WP2 API tải lên + WP6 edge + WP7 dọn dẹp | 4 | 2 ngày |
| 7 | WP4 chuyển mã media-svc | 5 | 2,5 ngày |
| 8 | WP5 ký URL prefix mới | 7 | 0,5 ngày |
| 9 | WP8 an toàn, WP10 admin | 5 | 1 ngày |
| 10 | WP9 web | 6, 7, design | 3 ngày |
| 11 | Kiểm tra §8, E2E, đo CPU khi live + job chạy cùng lúc | 8–10 | 1,5 ngày |
| 12 | Completed Plan, đồng bộ tài liệu | 11 | 0,5 ngày |

Tổng khoảng **15 ngày làm việc** cho một người. Kết thúc trước GĐ7 (15/02/2027) với nhiều dư địa.

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Chuyển mã chiếm CPU, live bị trễ hoặc giật (máy demo 2 vCPU chỉ giữ 1 luồng live) | 1 job / lúc; chỉ chạy khi còn chỗ live; `nice 10`; đo lại bằng `make nxl-bench` + 1 job (§8); có thể đặt `MEDIA_MAX_UPLOAD_TRANSCODES=0` trong buổi demo |
| File lớn qua proxy làm đầy RAM / đĩa của Nginx hoặc stream-svc | Chunk 8 MiB; `proxy_request_buffering off`; stream-svc chuyển phần thẳng sang `PutObjectPart` theo luồng (không đọc cả 2 GiB) |
| Đổi `vods.stream_id` thành nullable làm vỡ bên nghe sự kiện | Chỉ thêm trường; sửa channel, safety, media cùng thay đổi; test hợp đồng sự kiện ở từng service |
| Regex Nginx mới làm hỏng HLS có chữ ký của live | E2E livestream + kiểm thủ công `/hls/t/streams/…` và `/hls/t/videos/…` |
| File độc hại (ffmpeg đọc playlist trỏ ra mạng, file "zip bomb" về thời lượng) | `-protocol_whitelist file`; `ffprobe` kiểm thời lượng / kích thước khung trước khi chuyển mã; giới hạn thời gian chạy job (3 × thời lượng) |
| JetStream giao lại job đang chạy (job dài hơn AckWait) | `msg.InProgress()` mỗi 30 s; AckWait 2 phút; `MaxDeliver 3` |
| Dung lượng MinIO tăng vì video không hết hạn | Hạn mức mỗi kênh; xóa file gốc khi `READY`; theo dõi dung lượng bucket trên Grafana |
| Phạm vi mới làm trễ GĐ7 | Cắt theo thứ tự: bỏ retry (P1) → bỏ lọc nguồn ở trang kênh → bỏ thanh tải mini |

## 8. Kiểm tra

- [ ] `make format` · `make nxl-lint` · `make nxl-test` (stream, media, channel, safety).
- [ ] Migration `up → down → up` trên `nxl_stream` có dữ liệu VOD live cũ.
- [ ] `make nxl-up-all`, qua edge:
  - tải file MP4 720p 2 phút → `READY` → phát Tự động / 720p / 480p / 360p;
  - file dọc 1080×1920;
  - file không có audio;
  - file MOV iPhone (HEVC, có rotation);
  - file 1080p 60fps → 720p 30fps.
- [ ] Gửi lại một phần (cùng `n`) → không hỏng file. Tắt mạng giữa chừng rồi tiếp tục → đủ phần → `complete` thành công.
- [ ] **Hostile:**
  - token hết hạn giữa lúc tải;
  - phần sai kích thước;
  - `complete` thiếu phần;
  - file `.mp4` thực ra là ảnh / text → `FAILED UNSUPPORTED_FORMAT`;
  - file > 2 GiB;
  - quá 3 phiên;
  - kênh bị khóa giữa lúc tải;
  - creator khác gọi upload của người khác (`NOT_FOUND`);
  - MinIO tắt;
  - media-svc khởi động lại giữa job (job chạy lại, không treo `PROCESSING`).
- [ ] **Ưu tiên live:**
  - đang có 1 live, tải và chuyển mã 1 video → live vẫn `speed ≥ 1`, độ trễ HLS không tăng dần;
  - đủ chỗ live (`MEDIA_MAX_CONCURRENT_STREAMS` live) → job chờ, không chạy.
- [ ] **Luồng cũ chạy lại:**
  - live → kết thúc → VOD → phát → hết hạn;
  - ẩn / xóa VOD live;
  - gỡ stream của Admin;
  - HLS có chữ ký với `STREAM_HLS_SIGNING=true`;
  - tìm kiếm thấy VOD;
  - E2E web 18 test, admin 8 test.

## 9. Lệch so với lộ trình / spec

| Nguồn | Yêu cầu | Làm | Lý do |
| --- | --- | --- | --- |
| PRD v2.1 §4.10, NXL-G2 §11, brief GĐ4 | Không có "tải video lên" | Thêm FR-VD-08 + NXL-G2 §3.8 | Yêu cầu mới của chủ đồ án (02/11); chờ DL-22 |
| NXL-A §7C | `PROCESSING` chỉ từ "Stream ENDED" | Thêm trigger "Creator tải lên"; dùng `FAILED` đã có trên sơ đồ | Không thêm trạng thái mới ngoài bảng (luật NXL-A) |
| `system-architecture.md` §7 | VOD "xóa sau 14 ngày" | Chỉ đúng với `source = LIVE` | Video tải lên do Creator quản lý |
| Quy tắc GĐ2 số 4 | Tối đa 720p | Giữ | Cùng thang với live, CPU máy demo |

## 10. Câu hỏi cần chốt

Chốt 2026-11-04: chủ đồ án duyệt toàn bộ cột "Đề xuất" (UP-Q1…UP-Q12).

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| UP-Q1 | Nhận "Tải video lên" vào phạm vi đồ án (ngoài PRD v2.1)? | **Có**, mức P1, làm trước GĐ7; ghi DL-22 + errata PRD |
| UP-Q2 | Đường tải: (a) chunk 8 MiB qua API stream-svc → S3 multipart, hay (b) presigned URL tải thẳng MinIO | **(a)**: một origin (Vite, edge, tunnel đều chạy), không lộ MinIO, auth như API khác. (b) nhanh hơn nhưng phải mở MinIO qua edge và ký URL theo Host — khó với tunnel |
| UP-Q3 | Video tải lên có hết hạn không? | **Không hết hạn**; Creator tự xóa. Bản ghi live vẫn 14 ngày |
| UP-Q4 | Giới hạn | **2 GiB, 2 giờ, MP4 / MOV / MKV / WebM; 3 phiên đang tải hoặc xử lý / kênh; 10 lần / ngày** |
| UP-Q5 | Giữ file gốc sau khi chuyển mã? | **Xóa khi `READY`**; giữ 3 ngày nếu `FAILED` (cho retry) |
| UP-Q6 | Thang chất lượng | **720p / 480p / 360p** như live; không 1080p |
| UP-Q7 | Lập lịch CPU | **1 job / lúc, chỉ khi còn chỗ live, `nice 10`**; live không bao giờ bị từ chối vì job |
| UP-Q8 | Thêm `minio-go/v7` vào module `services/stream` (Prohibition 1) | **Đồng ý** — cùng bản đang dùng ở channel và media |
| UP-Q9 | Thông báo follower khi có video mới (FR-NT-01 "VOD mới") | **Chưa làm** đợt này; phát sẵn `vod.published.v1` để notification nghe sau |
| UP-Q10 | Creator tự chọn thumbnail? | **Đợt này tự lấy khung ở 10%**; ảnh tự chọn là P1 (dùng lại xử lý ảnh của channel-svc) |
| UP-Q11 | Cách Studio nhận tiến độ xử lý | **Polling 3 s** khi có video `PROCESSING`; không qua WebSocket |
| UP-Q12 | Mô hình dữ liệu | **`vods.stream_id` nullable + cột `source`**; không tạo hàng `streams` giả (sẽ lẫn vào lịch sử buổi phát, dashboard, đỉnh người xem) |

## 11. Tiêu chí hoàn thành

- **Tải lên và phát:** Given Creator `dev_khoa` đang ở Studio → Nội dung, When tải lên file MP4 1080p 3 phút với tiêu đề "Học Go
  buổi 1", Then thanh tiến độ chạy tới 100%, video hiện "Đang xử lý X%", và trong ≤ 3 × thời lượng video thành "Sẵn sàng"; trang
  `/vod/:id` phát được, menu chất lượng có Tự động / 720p / 480p / 360p.
- **Mạng chập chờn:** Given đang tải được 40%, When mất mạng 20 giây rồi có lại, Then client tự gửi tiếp từ phần còn thiếu,
  không bắt đầu lại từ 0%.
- **File hỏng:** Given file `.mp4` không chứa video, When tải xong, Then video thành "Lỗi — Định dạng không được hỗ trợ", không
  hiện ở kênh, Creator xóa được.
- **Ưu tiên live:** Given `linh_gaming` đang live trên máy 2 vCPU, When `dev_khoa` tải lên một video, Then buổi live không bị ngắt,
  không bị từ chối, `media_ffmpeg_speed` của live ≥ 1; job chuyển mã chờ hoặc chạy chậm hơn.
- **Hiển thị:** Given video `PUBLIC` đã `READY`, Then nó xuất hiện ở tab Videos của kênh với nhãn "Video", trong tìm kiếm, và
  không có dòng "hết hạn sau N ngày".
- **Không ảnh hưởng luồng cũ:** Given một buổi live kết thúc, Then VOD live vẫn được tạo, hết hạn sau 14 ngày, và mọi E2E cũ đều
  qua.
