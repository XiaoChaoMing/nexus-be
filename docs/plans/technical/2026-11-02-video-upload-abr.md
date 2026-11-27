# TSD — Tải video lên kênh và chuyển mã adaptive bitrate (HLS)

Completed Plan: `docs/plans/completed/2026-11-02-video-upload-abr.md`. Working Plan: `docs/plans/working/2026-11-02-video-upload-abr.md` (Approved 2026-11-04, §10 chốt theo cột "Đề xuất").
Web: `../nexuslive-web/docs/plans/working/2026-11-02-video-upload-abr.md`. Thiết kế: pen.dev (Studio → Nội dung, hộp thoại tải lên, thanh tải mini, trang VOD).

## 1. Mục tiêu

Creator tải một file video (≤ 2 GiB, ≤ 2 giờ, MP4/MOV/MKV/WebM) lên Studio theo từng phần 8 MiB. stream-svc ghi thẳng từng phần
vào S3 multipart của MinIO. Khi đủ phần, stream-svc tạo VOD `PROCESSING` và phát `upload.completed.v1`; media-svc chuyển mã
720p/480p/360p (theo cạnh ngắn, tối đa 30 fps) ra `nxl-media/videos/{vodId}/`, rồi báo `upload.processed.v1` → VOD `READY`.
Live luôn được ưu tiên: tối đa 1 job, chỉ chạy khi còn chỗ live, `nice 10`.

## 2. Phạm vi

Trong: WP1–WP12 của Working Plan. Ngoài: như Working Plan §1. Bổ sung so với Working Plan (xem §7.3):

- safety-svc **chưa có** target báo cáo `VOD` → thêm target `VOD`, hành động `TAKEDOWN_VOD`, sự kiện `safety.vod_takedown.v1`.
- stream-svc thêm `GET /internal/v1/vods/{id}` để safety-svc tra VOD chưa có trong bản sao.
- Bucket `nxl-uploads` do **stream-svc** tạo và đặt lifecycle (stream-svc sở hữu phiên tải); media-svc chỉ đọc.

## 3. Kịch bản

### S1 — Tải lên thành công

- Given Creator có kênh không bị khóa, When `POST /v1/studio/uploads` rồi `PUT …/parts/1..N` rồi `POST …/complete`,
  Then `202 {vodId}`, VOD `PROCESSING`; sau khi media xử lý xong VOD `READY`, `publishedAt` có giá trị, xuất hiện ở
  `/v1/channels/:id/vods` nếu `PUBLIC`.

```mermaid
sequenceDiagram
  participant W as Web (upload-client)
  participant E as Nginx edge
  participant S as stream-svc
  participant M as MinIO
  participant N as NATS
  participant MD as media-svc
  W->>E: POST /v1/studio/uploads
  E->>S: (auth, requireCreator)
  S->>M: NewMultipartUpload(nxl-uploads, sources/{uploadId})
  S-->>W: 201 {upload}
  loop từng phần n
    W->>E: PUT /v1/studio/uploads/{id}/parts/{n} (≤ 8 MiB)
    E->>S: proxy_request_buffering off
    S->>M: PutObjectPart(n)
    S->>S: parts[n] = {etag,size}
    S-->>W: 200 {partNumber, receivedBytes}
  end
  W->>S: POST /v1/studio/uploads/{id}/complete
  S->>M: CompleteMultipartUpload(parts)
  S->>S: TX: INSERT vods PROCESSING + upload COMPLETED + outbox upload.completed.v1
  S-->>W: 202 {vodId, upload}
  S-)N: upload.completed.v1 (outbox relay)
  N-)MD: durable media-transcode (MaxAckPending 1)
  MD->>M: GetObject (stream to work dir)
  MD->>MD: ffprobe → ffmpeg BuildVOD (nice 10)
  MD-)N: upload.progress.v1 (mỗi 5 s)
  N-)S: progress → vods.processing_percent
  MD->>M: PUT videos/{vodId}/… (segment trước, playlist sau)
  MD-)N: upload.processed.v1
  N-)S: TX: vods READY + outbox vod.published.v1; xóa sources/{uploadId}
  W->>S: GET /v1/studio/vods?status=PROCESSING (poll 3 s)
```

### S2 — Gửi lại phần / mạng chập chờn

- Given phần `n` đã nhận, When gửi lại `PUT …/parts/n`, Then MinIO ghi đè phần, `parts[n]` cập nhật, `200`.
- Given client mất kết nối, When `GET /v1/studio/uploads/{id}`, Then nhận `uploadedParts` để chỉ gửi phần thiếu.

### S3 — Thất bại khi chuyển mã

- Given file không có luồng video, When media ffprobe, Then `upload.failed.v1 {reason: NO_VIDEO_STREAM, retryable: false}` →
  VOD `FAILED`, `failureReason` hiện trong Studio, không hiện công khai. Creator xóa được.
- Lỗi tạm (MinIO) → `retryable: true`; Creator `POST /v1/studio/vods/{id}/retry` (khi file gốc còn) → `PROCESSING` +
  `upload.completed.v1` mới.

### S4 — Ưu tiên live

- Given số live đang chạy ≥ `MEDIA_MAX_CONCURRENT_STREAMS`, When nhận job, Then `NakWithDelay(30s)`; live không bao giờ bị từ chối
  vì job upload (đếm chỗ live không tính job).

### S5 — Hết hạn / hủy / khóa kênh

- Phiên `UPLOADING` quá `expires_at` → job dọn `AbortMultipartUpload`, `EXPIRED`. `DELETE` → `ABORTED`.
- `channel.suspended.v1` → hủy mọi phiên `UPLOADING` của kênh (S3 abort sau commit, best effort; lifecycle 1 ngày là lưới an
  toàn).

### S6 — Admin gỡ video

- Given report target `VOD`, When admin `TAKEDOWN_VOD`, Then safety-svc outbox `safety.vod_takedown.v1` → stream-svc VOD
  `DELETED` + `vod.deleted.v1 {playlistPrefix}` → media xóa file. Idempotent theo event id.

## 4. Hợp đồng

### 4.1 Sự kiện (`pkg/events`) — đã thêm

| Type | Phát | Nghe | Payload |
| --- | --- | --- | --- |
| `upload.completed.v1` | stream (outbox) | media (durable `media-transcode`) | `UploadCompleted{uploadId, vodId, channelId, bucket, objectKey, sizeBytes}` |
| `upload.progress.v1` | media (trực tiếp, best effort) | stream | `UploadProgress{vodId, percent, stage}` — stage `PROBING\|TRANSCODING\|UPLOADING` |
| `upload.processed.v1` | media (Nats-Msg-Id `upload-processed-{vodId}`) | stream | `UploadProcessed{vodId, durationSec, playlistPath, thumbnailPath, renditions[], sourceWidth, sourceHeight}` |
| `upload.failed.v1` | media (Nats-Msg-Id `upload-failed-{vodId}-{attempt}`) | stream | `UploadFailed{vodId, reason, retryable}` |
| `vod.published.v1` | stream (outbox) | channel (search index) | `VODPublished{vodId, channelId, source, title, description, thumbnailUrl, durationSec, categoryId, isMature, visibility, publishedAt}` |
| `vod.updated.v1` (đổi, chỉ thêm) | stream | channel | `streamId` omitempty; thêm `source`, `description`; `expiresAt` → `*time.Time` (null với UPLOAD) |
| `vod.deleted.v1` (đổi, chỉ thêm) | stream | channel, safety, media | `streamId` omitempty; thêm `source`, `playlistPrefix` (`streams/{streamId}/` hoặc `videos/{vodId}/`) |
| `safety.vod_takedown.v1` | safety (outbox) | stream | `VODTakedown{actionId, vodId, channelId, title, reason, adminId}` |

`stream.vod_ready.v1` vẫn chỉ dành cho bản ghi live (chat-svc không đổi).

Bên nghe cũ: thiếu `source` = `LIVE`; thiếu `playlistPrefix` = `streams/{streamId}/`. media-svc chỉ chấp nhận prefix khớp
`^(streams|videos)/[0-9a-f-]{36}/$` (uuid hợp lệ) — khác thì `ErrSkip`.

### 4.2 REST công khai (stream-svc, qua edge)

Mọi route `/v1/studio/*`: `auth.Required` + `requireCreator` (`403 NOT_CREATOR`). Kênh bị khóa → `403 CHANNEL_SUSPENDED` cho
tạo phiên, gửi phần, hoàn tất, retry.

**`POST /v1/studio/uploads`** — body JSON:

```json
{ "fileName": "hoc-go-1.mp4", "sizeBytes": 314572800, "contentType": "video/mp4",
  "title": "Học Go buổi 1", "description": "", "categoryId": null, "isMature": false, "visibility": "PUBLIC" }
```

Kiểm: `fileName` 1–255 ký tự, đuôi ∈ `.mp4 .mov .mkv .webm` (không phân biệt hoa thường); `contentType` ∈ `video/mp4,
video/quicktime, video/x-matroska, video/webm` hoặc rỗng/`application/octet-stream` (khi đó dựa vào đuôi);
`sizeBytes` 1…`STREAM_UPLOAD_MAX_BYTES`; `title` 1–140 sau trim; `description` ≤ 5000; `categoryId` uuid hoặc null;
`visibility` `PUBLIC|HIDDEN`.

`201`:

```json
{ "data": { "id": "…", "status": "UPLOADING", "fileName": "hoc-go-1.mp4", "sizeBytes": 314572800,
  "partSize": 8388608, "partCount": 38, "uploadedParts": [], "vodId": null,
  "createdAt": "…", "expiresAt": "…" } }
```

Lỗi: `400 VALIDATION_FAILED`, `413 FILE_TOO_LARGE` (`details.maxBytes`), `422 UNSUPPORTED_FORMAT`,
`429 UPLOAD_LIMIT_REACHED` (`details.limit` = `ACTIVE|DAILY`, `details.retryAfterSec`), `503 STORAGE_UNAVAILABLE` (MinIO lỗi).

**`PUT /v1/studio/uploads/{id}/parts/{n}`** — body nhị phân `application/octet-stream`. Phần `n < partCount` phải đúng
`partSize`, phần cuối đúng `sizeBytes − (partCount−1)·partSize`. `200 {"data": {"partNumber": n, "receivedBytes": 8388608}}`.
Lỗi: `400 INVALID_PART` (`details.expectedBytes` khi sai kích thước), `404 NOT_FOUND` (không phải của kênh này),
`410 UPLOAD_EXPIRED` (`EXPIRED|ABORTED`), `409 INVALID_STATE` (`COMPLETED`), `503 STORAGE_UNAVAILABLE`.

**`GET /v1/studio/uploads/{id}`** → `200 {data: Upload}` (`uploadedParts` tăng dần). `404 NOT_FOUND`.

**`POST /v1/studio/uploads/{id}/complete`** → `202 {"data": {"vodId": "…", "upload": Upload}}`. Gọi lại khi đã `COMPLETED` trả
lại cùng `vodId` (idempotent). `409 PARTS_MISSING` (`details.missing: [n…]`), `410 UPLOAD_EXPIRED`, `503 STORAGE_UNAVAILABLE`.

**`DELETE /v1/studio/uploads/{id}`** → `204`. Phiên `COMPLETED` → `409 INVALID_STATE`. Đã `ABORTED|EXPIRED` → `204`.

**`GET /v1/studio/vods`** — thêm query `source=LIVE|UPLOAD`, `status` nhận thêm `PROCESSING|FAILED`. Không có `status` →
mọi `READY, EXPIRED, PROCESSING, FAILED`.

`StudioVOD` (chỉ thêm trường; `streamId`, `expiresAt` thành nullable):

```json
{ "id": "…", "streamId": null, "source": "UPLOAD", "title": "…", "description": "…", "durationSec": 0,
  "thumbnailUrl": "", "manifestUrl": "", "status": "PROCESSING", "visibility": "PUBLIC", "isMature": false,
  "category": null, "createdAt": "…", "publishedAt": null, "expiresAt": null,
  "processing": { "percent": 42, "stage": "TRANSCODING" }, "failureReason": null, "retryable": false }
```

`processing` khác null chỉ khi `PROCESSING`; `failureReason` + `retryable` chỉ có nghĩa khi `FAILED`. `manifestUrl` rỗng khi
chưa `READY`.

**`PATCH /v1/studio/vods/{id}`** — thêm `description` (≤ 5000), `categoryId` (uuid; chuỗi rỗng = bỏ danh mục), `isMature`.
Ba trường này chỉ cho VOD `UPLOAD` (VOD live → `409 INVALID_STATE`). VOD `UPLOAD` sửa được khi `READY` hoặc `PROCESSING`;
`FAILED`/`EXPIRED` → `409 INVALID_STATE`. Đổi trên VOD `READY` phát `vod.updated.v1`.

**`DELETE /v1/studio/vods/{id}`** — nhận thêm `PROCESSING`, `FAILED`. `vod.deleted.v1` mang `playlistPrefix`.

**`POST /v1/studio/vods/{id}/retry`** — VOD `UPLOAD` `FAILED`, `retryable = true`, file gốc còn → `202 {data: StudioVOD}`
(`PROCESSING`). Khác → `409 INVALID_STATE`.

**Công khai** `GET /v1/vods/:id`, `/v1/vods`, `/v1/vods/recommended`, `/v1/channels/:id/vods`, `/v1/me/continue-watching`:
`VOD` thêm `source`, `description`, `publishedAt`; `streamId`, `expiresAt` nullable. `GET /v1/channels/:id/vods` thêm
`source=LIVE|UPLOAD`. Điều kiện công khai: `status = 'READY' AND visibility = 'PUBLIC' AND (expires_at IS NULL OR expires_at > now())`.

### 4.3 HTTP nội bộ (stream-svc, `X-Internal-Secret`)

`GET /internal/v1/vods/{id}` → `200 {"data": {"id", "channelId", "streamId": null|"…", "source", "title", "status",
"visibility"}}`, kể cả `DELETED`; `404` khi không có.

### 4.4 SQL (`services/stream/migrations/20261102000001_video_upload.{up,down}.sql`)

```sql
-- up
ALTER TABLE vods ALTER COLUMN stream_id DROP NOT NULL, ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE vods
    ADD COLUMN source             varchar(8)  NOT NULL DEFAULT 'LIVE' CHECK (source IN ('LIVE', 'UPLOAD')),
    ADD COLUMN description        text        NOT NULL DEFAULT '',
    ADD COLUMN published_at       timestamptz,
    ADD COLUMN failure_reason     varchar(32),
    ADD COLUMN failure_retryable  boolean     NOT NULL DEFAULT false,
    ADD COLUMN processing_percent smallint    NOT NULL DEFAULT 0 CHECK (processing_percent BETWEEN 0 AND 100),
    ADD COLUMN processing_stage   varchar(12);
ALTER TABLE vods ADD CONSTRAINT vods_source_stream CHECK ((source = 'LIVE') = (stream_id IS NOT NULL));
ALTER TABLE vods ADD CONSTRAINT vods_live_expiry CHECK (source = 'UPLOAD' OR expires_at IS NOT NULL);
ALTER TABLE vods DROP CONSTRAINT vods_status_check;
ALTER TABLE vods ADD CONSTRAINT vods_status_check
    CHECK (status IN ('PROCESSING', 'READY', 'EXPIRED', 'DELETED', 'FAILED'));
UPDATE vods SET published_at = created_at WHERE status <> 'PROCESSING';
DROP INDEX vods_studio_idx;
CREATE INDEX vods_studio_idx ON vods (channel_id, created_at DESC, id DESC)
    WHERE status IN ('READY', 'EXPIRED', 'PROCESSING', 'FAILED');
CREATE INDEX vods_processing_idx ON vods (channel_id) WHERE status IN ('PROCESSING', 'FAILED');

CREATE TABLE video_uploads (
    id                uuid         PRIMARY KEY DEFAULT uuidv7(),
    channel_id        uuid         NOT NULL,
    user_id           uuid         NOT NULL,
    vod_id            uuid         REFERENCES vods (id),
    file_name         varchar(255) NOT NULL,
    content_type      varchar(100) NOT NULL,
    size_bytes        bigint       NOT NULL CHECK (size_bytes > 0),
    part_size         integer      NOT NULL CHECK (part_size > 0),
    part_count        integer      NOT NULL CHECK (part_count BETWEEN 1 AND 10000),
    s3_upload_id      text         NOT NULL,
    object_key        text         NOT NULL,
    title             varchar(140) NOT NULL,
    description       text         NOT NULL DEFAULT '',
    category_id       uuid,
    is_mature         boolean      NOT NULL DEFAULT false,
    visibility        varchar(10)  NOT NULL CHECK (visibility IN ('PUBLIC', 'HIDDEN')),
    status            varchar(10)  NOT NULL DEFAULT 'UPLOADING'
                                   CHECK (status IN ('UPLOADING', 'COMPLETED', 'ABORTED', 'EXPIRED')),
    parts             jsonb        NOT NULL DEFAULT '{}',
    source_deleted_at timestamptz,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    expires_at        timestamptz  NOT NULL,
    CHECK ((status = 'COMPLETED') = (vod_id IS NOT NULL))
);
CREATE INDEX video_uploads_channel_idx ON video_uploads (channel_id, created_at DESC);
CREATE INDEX video_uploads_expiry_idx ON video_uploads (expires_at) WHERE status = 'UPLOADING';
CREATE UNIQUE INDEX video_uploads_vod_idx ON video_uploads (vod_id) WHERE vod_id IS NOT NULL;
```

Down: xóa `playback_sessions` / `watch_progress` của VOD `UPLOAD`, `DROP TABLE video_uploads`, xóa hàng `vods` `UPLOAD` và
`FAILED`, đảo các ràng buộc và index, `stream_id`/`expires_at` `SET NOT NULL`, bỏ cột mới. Tên ràng buộc `vods_status_check`
là tên Postgres tự sinh cho CHECK cột `status` của migration init — kiểm bằng `\d vods` trước khi chạy.

`parts`: `{"1": {"etag": "…", "size": 8388608}, …}`.

### 4.5 Khóa đối tượng (MinIO)

| Bucket | Khóa | Quyền | Lifecycle |
| --- | --- | --- | --- |
| `nxl-uploads` (stream-svc tạo) | `sources/{uploadId}` | riêng tư | `AbortIncompleteMultipartUpload` 1 ngày; xóa `sources/` sau 3 ngày |
| `nxl-media` | `videos/{vodId}/master.m3u8`, `videos/{vodId}/{720p,480p,360p}/index.m3u8` + `seg_*.ts`, `videos/{vodId}/thumb.jpg` | `.jpg` công khai, còn lại qua `/hls` (có ký khi `STREAM_HLS_SIGNING`) | **không** |

### 4.6 Cấu hình

| Service | Biến | Mặc định |
| --- | --- | --- |
| stream | `STREAM_S3_ENDPOINT`, `STREAM_S3_ACCESS_KEY`, `STREAM_S3_SECRET_KEY`, `STREAM_S3_USE_SSL` | `minio:9000`, —, —, `false` |
| stream | `STREAM_UPLOAD_ENABLED` | `true` (false → route `/v1/studio/uploads` trả `503 STORAGE_UNAVAILABLE`, không cần S3) |
| stream | `STREAM_UPLOAD_BUCKET` / `_MAX_BYTES` / `_PART_SIZE` / `_TTL` / `_MAX_ACTIVE` / `_DAILY_MAX` / `_EXPIRY_TICK` | `nxl-uploads` / `2147483648` / `8388608` / `24h` / `3` / `10` / `10m` |
| media | `MEDIA_MAX_UPLOAD_TRANSCODES` | `1` (0 = không nhận job) |
| media | `MEDIA_UPLOAD_X264_PRESET` / `_MAX_DURATION` / `_BUCKET` / `_SEGMENT_SEC` | `veryfast` / `2h` / `nxl-uploads` / `4` |
| edge | `NXL_EDGE_UPLOAD_RATE` | `300r/m` |

stream-svc: `httpserver` body limit 9 MiB (`9437184`), read timeout 60 s.

### 4.7 Edge (Nginx)

- `location ~ ^/v1/studio/uploads(/|$)` → stream, đặt trước `^/v1/(studio|streams|vods)`: `client_max_body_size 9m;
  proxy_request_buffering off; proxy_read_timeout 120s; proxy_send_timeout 120s; limit_req zone=nxl_upload burst=20 nodelay`.
- `/hls/` và `/hls/t/`: chấp nhận `(streams|videos)/<uuid>/`. `videos/<uuid>/thumb.jpg` công khai như `streams/`.

### 4.8 safety-svc

- `TargetVOD = "VOD"` (`targetId` = vodId). Bản sao VOD (`vods` của safety: id, channel_id, stream_id, source, title, deleted)
  nạp từ `stream.vod_ready.v1`, `vod.published.v1`, `vod.updated.v1`, `vod.deleted.v1`; thiếu thì gọi
  `GET /internal/v1/vods/{id}` của stream-svc.
- Hành động admin `TAKEDOWN_VOD` (báo cáo target VOD, VOD chưa xóa) → outbox `safety.vod_takedown.v1` + audit log; VOD đã xóa →
  `409 INVALID_STATE`.

## 5. Chi tiết kỹ thuật

- **Ghi phần:** handler đọc body (≤ 9 MiB, Fiber đệm trong RAM, chặn bởi body limit) → `minio.Core.PutObjectPart`. Cập nhật
  `parts` bằng `UPDATE … SET parts = parts || jsonb_build_object($n, …) WHERE status = 'UPLOADING'` (nhiều phần cùng lúc không
  mất nhau).
- **Hoàn tất:** khóa hàng `FOR UPDATE`, kiểm đủ phần, `CompleteMultipartUpload` (ngoài TX, trước khi commit), rồi TX: insert VOD
  `PROCESSING` + upload `COMPLETED` + outbox. Nếu complete S3 thành công mà TX lỗi, gọi lại complete sẽ thấy object đã có —
  stream-svc coi `NoSuchUpload` + object tồn tại là đã hoàn tất.
- **Hạn mức:** `count(UPLOADING phiên) + count(vods PROCESSING)` của kênh < `MAX_ACTIVE`; `count(phiên tạo trong 24 h)` < `DAILY_MAX`.
- **Sự kiện vào stream:** `upload.progress` chỉ cập nhật khi `PROCESSING` và percent tăng; `upload.processed` trên VOD `DELETED`
  → phát lại `vod.deleted.v1` (media xóa file vừa tạo); trên `READY` → bỏ qua (idempotent). Sau commit READY: xóa
  `sources/{uploadId}` (best effort, ghi `source_deleted_at`).
- **media-svc:** job tải object về `MEDIA_WORK_DIR/uploads/{vodId}/source` theo luồng; ffprobe; `ffmpeg -protocol_whitelist
  file,pipe`; `msg.InProgress()` mỗi 30 s; AckWait 2 phút, MaxDeliver 3; giới hạn thời gian job `3 × thời lượng + 10 phút`.
- **Playback:** `playback.media()` đọc `IsMature` từ VOD; `streamDir` nhận `videos/{uuid}/`.

## 6. Mã lỗi

| Trường hợp | Sentinel | HTTP | Code |
| --- | --- | --- | --- |
| File quá lớn | `ErrFileTooLarge` | 413 | `FILE_TOO_LARGE` |
| Đuôi / MIME sai | `ErrUnsupportedFormat` | 422 | `UNSUPPORTED_FORMAT` |
| Vượt hạn mức | `ErrUploadLimit` (`UploadLimitError{Limit, RetryAfter}`) | 429 | `UPLOAD_LIMIT_REACHED` |
| Phiên hết hạn / đã hủy | `ErrUploadExpired` | 410 | `UPLOAD_EXPIRED` |
| Thiếu phần | `ErrPartsMissing` (`PartsMissingError{Missing}`) | 409 | `PARTS_MISSING` |
| Phần sai số / kích thước | `ErrInvalidPart` | 400 | `INVALID_PART` |
| MinIO lỗi / tắt tải lên | `ErrStorageUnavailable` | 503 | `STORAGE_UNAVAILABLE` |
| Kênh bị khóa | `ErrChannelSuspended` | 403 | `CHANNEL_SUSPENDED` |
| Trạng thái không cho phép | `ErrInvalidState` | 409 | `INVALID_STATE` |

## 7. Tương thích và rollback

### 7.1 Máy trạng thái

Phiên: `UPLOADING → COMPLETED | ABORTED | EXPIRED`. VOD `UPLOAD`: `PROCESSING → READY | FAILED`; `FAILED → PROCESSING` (retry);
`PROCESSING | READY | FAILED → DELETED`. VOD `LIVE` không đổi (`READY → EXPIRED`, `→ DELETED`).

### 7.2 Thứ tự triển khai

1. Migration stream (cộng thêm, hàng cũ thỏa CHECK mới).
2. Bên nghe (channel, safety, media) trước bên phát mới; trường mới đều tùy chọn.
3. stream-svc + media-svc + edge. Rollback: tắt `STREAM_UPLOAD_ENABLED=false`, `MEDIA_MAX_UPLOAD_TRANSCODES=0`; migration down
   xóa video tải lên.

### 7.3 Lệch so với Working Plan

| Working Plan | TSD | Lý do |
| --- | --- | --- |
| WP8 "Report VOD dùng target VOD có sẵn" | Thêm target `VOD`, `TAKEDOWN_VOD`, `safety.vod_takedown.v1`, `GET /internal/v1/vods/{id}` | safety-svc chỉ có USER/CHANNEL/STREAM/MESSAGE |
| WP4 media tạo bucket `nxl-uploads` | stream-svc tạo bucket + lifecycle | stream-svc mở multipart trước khi media chạy job |
| `source=live\|upload` (chữ thường) | `source=LIVE\|UPLOAD` | Nhất quán với `status`, `visibility` |
| — | `STREAM_UPLOAD_ENABLED`, mã `STORAGE_UNAVAILABLE` | Tắt tính năng khi demo / MinIO tắt |
| Lifecycle `AbortIncompleteMultipartUpload` 1 ngày | Chỉ xóa `sources/` sau 3 ngày | MinIO từ chối rule này; MinIO tự dọn multipart dở sau 24 h, job hết hạn vẫn `AbortMultipartUpload` |
| Policy công khai `videos/*.jpg` | `videos/*` | Edge đọc MinIO ẩn danh (cả `/hls/t/`); giống `streams/*` |
| `NakWithDelay(30s)` khi hết chỗ live | Consumer kéo, chỉ lấy job khi còn chỗ; trả lại 30 s nếu live chiếm chỗ lúc lấy; lần giao cuối (3) vẫn chạy | Không giao lại liên tục; video không treo `PROCESSING` |
| — | `MEDIA_FFPROBE_PATH`; `-format_whitelist mov,matroska`; header `Retry-After` khi 429 | Vận hành, an toàn |
| — | Bucket của stream-svc thiết lập nền (thử lại 30 s) | MinIO tắt không làm sập livestream |
| Xóa file gốc khi xóa video `UPLOAD` | Ticker `STREAM_UPLOAD_EXPIRY_TICK` xóa (READY vẫn xóa ngay) | Một đường dọn có thử lại |
| — | `POST /v1/admin/vods/{vodId}/takedown` `{reason, reportId?}` → `202 {actionId}` | Hình dạng như gỡ stream |

## 8. Ước lượng

Như Working Plan §6 (~15 ngày công).

## 9. Tham chiếu

ADR-004, ADR-005, ADR-012; NXL-00 §11; NXL-A §7C; NXL-G2 §3.3.

## 10. Document control

| Version | Date | Change |
| --- | --- | --- |
| 1.0 | 2026-11-04 | Initial |
| 1.1 | 2026-11-04 | Đồng bộ với bản đã giao: §7.3 thêm các lệch khi làm (lifecycle, policy, lập lịch job, takedown VOD) |
