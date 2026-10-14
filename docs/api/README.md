# NexusLive — Danh mục API

| | |
| --- | --- |
| **Phiên bản** | 1.5 |
| **Cập nhật** | 2026-10-14 |
| **Phạm vi** | REST công khai, API nội bộ giữa service, RTMP/HLS, sự kiện NATS. Gồm API đã chạy và API dự kiến theo lộ trình |

Tài liệu này là **bản đồ** của mọi API: ở đâu, ai sở hữu, trạng thái nào, quy ước chung là gì.

- Chi tiết request/response của API đã chạy nằm trong **Swagger** của từng service, sinh từ code nên luôn khớp code (§2).
- Chi tiết nghiệp vụ của API dự kiến nằm trong spec NXL-* (`../../../docs/ba/specs/`).
- Khi tài liệu này lệch với Swagger, Swagger đúng; sửa tài liệu này theo §9.

**Trạng thái:**

- ✅ đã chạy, có test.
- 🔧 đang làm.
- 📋 dự kiến: đường dẫn có thể đổi khi viết spec.
- ⛔ chỉ dùng khi dev.

## 1. Môi trường dev

| Service | Cổng | Base URL | Swagger | Có từ |
| --- | --- | --- | --- | --- |
| **Nginx edge (cổng vào)** | 8080 (`NXL_EDGE_PORT`) | `http://localhost:8080/v1`, `http://localhost:8080/hls` | — | GĐ0 ✅ |
| identity-svc | 8090 | `http://localhost:8090/v1` | <http://localhost:8090/swagger/index.html> | GĐ1 ✅ |
| stream-svc (công khai) | 8092 | `http://localhost:8092/v1` | <http://localhost:8092/swagger/index.html> | GĐ2 ✅ |
| stream-svc (nội bộ) | 8093 | `http://stream:8093/internal/v1` — chỉ trong mạng Docker | — | GĐ2 ✅ |
| media-svc | 1935 (RTMP), 8091 (HTTP) | `rtmp://localhost:1935/live`, `http://localhost:8091` | — | GĐ2 ✅ |
| channel-svc | 8094 | `http://localhost:8094/v1` | <http://localhost:8094/swagger/index.html> | GĐ1 🔧 (tối thiểu) |
| chat-svc (realtime gateway + chat), 2 instance | 8095 (`chat-1`), 8096 (`chat-2`) | `ws://localhost:8080/v1/ws` (qua cổng vào), `http://localhost:8095/v1` | <http://localhost:8095/swagger/index.html> | GĐ3 🔧 |
| safety-svc | 8099 📋 | | 📋 | GĐ5 |
| notification-svc | 8097 📋 | | 📋 | GĐ5 |
| billing-svc | 8098 📋 | | 📋 | GĐ6 |
| NATS JetStream | 4222 (client), 8222 (monitor) | `nats://guest:guest@localhost:4222` | — | ✅ |
| Web người dùng `nexuslive-web` | 5173 | Vite proxy `/v1`, `/hls` → cổng vào | — | ✅ |
| Web quản trị `nexuslive-admin` | 5174 | Vite proxy `/v1` → cổng vào | — | GĐ1 🔧 |

- Chạy: `make nxl-up-all` (toàn bộ, qua cổng vào), `make nxl-media-up` (chuỗi livestream) hoặc `make nxl-up` (chỉ identity + stream).
- Cổng của các service chưa có là đề xuất, giữ để không đụng nhau. v1.3: channel-svc nhận 8094 (đã chạy ở GĐ1), chat-svc dời sang 8095. v1.5: instance thứ hai của chat-svc dùng 8096, safety-svc dời sang 8099.
- Trình duyệt chỉ gọi **cổng vào Nginx** (`deploy/nginx/nginx.conf`). Cổng riêng từng service chỉ dùng khi dev (Swagger, curl).

**Định tuyến tại cổng vào (GĐ1):**

| Đường dẫn | Tới | Ghi chú |
| --- | --- | --- |
| `/v1/auth/{login,register,password/forgot,password/reset,verify-email,verify-email/resend}` | identity-svc :8090 | Vùng rate limit chặt (§1.1) |
| `/v1/ws` | chat-svc (upstream `nxl_chat`: `chat-1`, `chat-2`) | WebSocket (`Upgrade`), `proxy_read_timeout 75s`, không rate limit theo request (GĐ3) |
| `/v1/chat/*`, `/v1/channels/{id}/chat-settings` | chat-svc | Đặt trước route channel-svc (GĐ3) |
| `/emotes/*` | file tĩnh `deploy/emotes/*.svg` | `Cache-Control: public, max-age=604800` (GĐ3) |
| `/v1/channels/*` | channel-svc :8094 | GĐ1 |
| `/v1/studio/*`, `/v1/streams/*`, `/v1/vods/*` | stream-svc :8092 | |
| `/v1/*` còn lại | identity-svc :8090 | Gồm `/v1/auth/*`, `/v1/me*`, `/v1/creator-applications*`, `/v1/admin/*` |
| `/hls/*` | MinIO, bucket `nxl-media` | Chỉ `GET`/`HEAD`; `.m3u8` có `Cache-Control: no-cache`; không rate limit |
| `/internal/*`, `/metrics`, `/swagger`, còn lại | — | `404 NOT_FOUND` |

Lỗi do chính cổng vào sinh ra dùng cùng envelope: `503 SERVICE_UNAVAILABLE` (service không phản hồi), `413 PAYLOAD_TOO_LARGE` (body > 1 MB), `429 RATE_LIMITED`, `403 FORBIDDEN`, `404 NOT_FOUND`. Mỗi response có `X-Request-Id` (lấy từ request nếu client gửi). URL HLS có chữ ký: GĐ5/GĐ7.

### 1.1 Rate limit, CORS, header bảo mật (GĐ1 🔧)

Cấu hình: `deploy/nginx/templates/nxl-edge.conf.template` (envsubst khi container khởi động). Chi tiết: NXL-00 §9.

| Nhóm | Phạm vi | Khóa | Mặc định | Biến |
| --- | --- | --- | --- | --- |
| Xác thực | 6 đường dẫn `/v1/auth/*` ở bảng trên | IP | 10/phút, burst 5 | `NXL_EDGE_AUTH_RATE` |
| Ghi | `POST`/`PUT`/`PATCH`/`DELETE` dưới `/v1/` | IP | 60/phút, burst 10 | `NXL_EDGE_WRITE_RATE` |
| Đọc | `GET`/`HEAD` dưới `/v1/` | IP | 600/phút, burst 100 | `NXL_EDGE_READ_RATE` |

- Vượt → `429 {"error":{"code":"RATE_LIMITED","details":{"retryAfterSec":"60"}}}` + `Retry-After: 60`.
- **CORS:** chỉ `NXL_WEB_ORIGIN` (dev `http://localhost:5173`) và `NXL_ADMIN_ORIGIN` (dev `http://localhost:5174`), có
  credentials; `/v1/admin/*` chỉ nhận origin admin. Preflight cho phép header `Authorization, Content-Type,
  X-NXL-Client, X-NXL-Anon-Id, X-Request-Id, Idempotency-Key`. Dev qua Vite proxy là cùng origin nên không cần CORS.
- **Header mọi response:** `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`,
  `X-Frame-Options: DENY`, `X-Request-Id`.

## 2. Swagger

- Mỗi service REST tự phục vụ Swagger UI tại `/swagger/index.html`. Bật/tắt bằng `NXL_SWAGGER_ENABLED` (mặc định bật).
- Spec sinh từ chú thích swaggo trên handler vào `services/<svc>/docs/`. Sinh lại: `make nxl-swag` (chạy trong Docker, không cần Go trên máy).
- **Thử API cần đăng nhập:**
  1. Swagger identity → `POST /auth/login` → copy `data.accessToken`.
  2. Swagger service khác → **Authorize** → dán token. Có hoặc không có tiền tố `Bearer ` đều được: server chấp nhận cả JWT trần, vì ô Authorize của Swagger 2.0 gửi nguyên văn những gì bạn dán.
  3. Token sống 15 phút. Hết hạn thì gọi `POST /auth/refresh` ở Swagger identity; cookie đã có sẵn trong trình duyệt.
- API nội bộ (cổng 8093) không có Swagger vì không mở ra ngoài Docker.

## 3. Quy ước chung

Bản chính thức của các quy ước dưới đây (kèm danh mục mã lỗi, giao thức WebSocket, sự kiện, hằng số) là **NXL-00** (`../../../docs/ba/specs/NXL-00-platform-foundation.md`). Mục này tóm tắt phần đã chạy.

### 3.1 Đường dẫn và phiên bản

- **Công khai:** `/v1/<tài nguyên>`.
  - Phá vỡ tương thích (đổi/xóa trường, đổi nghĩa) → `/v2` cho endpoint đó, giữ `/v1` tới khi web chuyển xong.
  - Thêm trường **không** cần đổi phiên bản.
- **Nội bộ:** `/internal/v1/...` trên cổng riêng, không bao giờ đi qua cổng vào (Nginx trả 404).
- Tên tài nguyên: danh từ số nhiều, kebab-case (`/stream-key`, `/playback-sessions`). Tên trường JSON: camelCase.

### 3.2 Định dạng phản hồi

```json
// Thành công
{ "data": { ... } }

// Danh sách có phân trang
{ "data": { "items": [ ... ], "nextCursor": "eyJ..." } }   // nextCursor = null ở trang cuối

// Lỗi
{ "error": { "code": "VALIDATION_FAILED", "message": "invalid request body", "details": { "title": "LENGTH" } } }
```

- `code`: hằng số ổn định, web dịch sang thông báo (`nexuslive-web/src/lib/api-error.ts`). `message`: tiếng Anh, chỉ để debug.
- `details`: tên trường (camelCase) → mã lỗi của trường (`REQUIRED`, `LENGTH`, `MAX`, `EMAIL`, …).
- `204 No Content` cho thao tác không cần trả dữ liệu (logout, kết thúc stream).
- Thời gian: RFC 3339 có múi giờ. ID: UUID v7 dạng chuỗi.

### 3.3 Xác thực

| Loại | Cách gửi | Dùng cho |
| --- | --- | --- |
| Access token | `Authorization: Bearer <JWT>` (EdDSA, 15 phút, `sub` = userId, `role`, `sid`) | Mọi API cần đăng nhập |
| Refresh token | Cookie `nxl_rt` (HttpOnly, SameSite=Strict, Path=`/v1/auth`), xoay vòng mỗi lần refresh | `POST /v1/auth/refresh`, `/logout` |
| Header chống CSRF (GĐ1 🔧) | `X-NXL-Client: web` (nexuslive-web) hoặc `admin` (nexuslive-admin), **bắt buộc** ở refresh/logout; thiếu → `403 FORBIDDEN` (`details.header = REQUIRED`). Swagger UI không gọi được refresh | `POST /v1/auth/refresh`, `/logout` |
| Admin | Access token có claim `role = ADMIN`; identity kiểm ở `/v1/admin/*` (vai trò mới có hiệu lực sau lần refresh kế tiếp) | `/v1/admin/*` |
| Xác minh token ở service khác | JWKS `GET /v1/auth/.well-known/jwks.json` của identity-svc, cache theo `kid` | stream-svc, các service sau |
| Nội bộ | Header `X-Internal-Secret` = `NXL_INTERNAL_SECRET` | `/internal/v1/*` |
| Stream key | Tên publish RTMP `rtmp://host:1935/live/<streamKey>` | OBS → media-svc |

Từ GĐ1 bỏ giả định `channelId = userId`: kênh do channel-svc tạo khi đơn Creator được duyệt; stream-svc tra `owner → channel` ở bảng `creator_channels`; chưa có kênh → `/v1/studio/*` trả `403 NOT_CREATOR`. Tài khoản dev `creator@nexuslive.dev` (seed, đã duyệt) dùng để thử Studio.

### 3.4 Phân trang, lọc

- Cursor (keyset), không dùng offset.
  - Tham số: `cursor` (giá trị `nextCursor` của trang trước) và `limit` (mặc định 24, tối đa 50).
  - Cursor là chuỗi mờ; client không tự tạo.
- Lọc bằng query string (`?status=live`). Giá trị không hỗ trợ → 400 `VALIDATION_FAILED`.

### 3.5 Mã HTTP

| Mã | Khi nào |
| --- | --- |
| 200 / 201 / 204 | Thành công / tạo mới / không có nội dung |
| 400 | `VALIDATION_FAILED` — body/query sai; `TOKEN_INVALID`, `WRONG_PASSWORD` |
| 401 | `UNAUTHORIZED` (thiếu/sai token), `INVALID_CREDENTIALS`, `SESSION_EXPIRED` |
| 403 | Đã đăng nhập nhưng không được phép (`FORBIDDEN`, `ACCOUNT_LOCKED`, `EMAIL_NOT_VERIFIED`, `NOT_CREATOR`, `INVALID_STREAM_KEY` nội bộ) |
| 404 | `NOT_FOUND` — ID sai định dạng cũng trả 404, không 400 |
| 409 | Xung đột trạng thái (`USERNAME_TAKEN`, `EMAIL_TAKEN`, `CONFLICT`, `APPLICATION_PENDING`, `ALREADY_CREATOR`, `INVALID_STATE`, `ALREADY_LIVE`, `RECENTLY_ENDED`, `NOT_LIVE`) |
| 422 | `UNDERAGE` |
| 429 | `RATE_LIMITED` — cổng vào (§1.1) hoặc identity (gửi lại email) |
| 500 | `INTERNAL_ERROR` — chi tiết chỉ nằm trong log |

### 3.6 Idempotency 📋

- Thao tác tạo giao dịch tiền (billing) bắt buộc header `Idempotency-Key` (UUID do client sinh). Gửi lại cùng key → cùng kết quả (kiến trúc §6.4).
- Webhook từ cổng thanh toán xử lý trùng an toàn.

## 4. identity-svc — tài khoản và phiên

Hợp đồng đầy đủ: TSD GĐ1 §4.1 (`../plans/technical/2026-10-13-gd01-identity-web-shell.md`) = NXL-D v1.2 §13.1.
Auth: `—` không cần · `Bearer` · `cookie+H` = cookie `nxl_rt` + header `X-NXL-Client` · `Admin` = Bearer có `role = ADMIN`.

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| POST | `/v1/auth/register` | — | `{username, displayName?, email, password, acceptTerms}` → `201 {accessToken, expiresIn, user}` + cookie. GĐ1: `acceptTerms` bắt buộc, `details.<field> = TAKEN` khi trùng | ✅ · GĐ1 🔧 |
| POST | `/v1/auth/login` | — | `{login, password}` (email hoặc username). GĐ1: sai 10 lần / 15 phút → khóa tạm 15 phút, `403 ACCOUNT_LOCKED` + `details.lockedUntil` | ✅ · GĐ1 🔧 |
| POST | `/v1/auth/refresh` | cookie+H | Cấp access token mới, xoay vòng refresh token (phát hiện dùng lại → thu hồi mọi phiên) | ✅ · GĐ1 🔧 header |
| POST | `/v1/auth/logout` | cookie+H | Thu hồi phiên hiện tại → `204` | ✅ · GĐ1 🔧 header |
| GET | `/v1/auth/me` | Bearer | `{user}` — giữ nguyên, Shell dùng `/v1/me` | ✅ |
| GET | `/v1/auth/.well-known/jwks.json` | — | Khóa công khai để service khác xác minh token | ✅ |
| POST | `/v1/auth/verify-email` | — | `{token}` → `200 {user}`; `TOKEN_INVALID` | 🔧 GĐ1 |
| POST | `/v1/auth/verify-email/resend` | Bearer | `202`; `CONFLICT` (đã xác minh), `RATE_LIMITED` (≥ 60s/lần, ≤ 5/giờ; `details.retryAfterSec`) | 🔧 GĐ1 |
| POST | `/v1/auth/password/forgot` | — | `{email}` → luôn `202` | 🔧 GĐ1 |
| POST | `/v1/auth/password/reset` | — | `{token, password}` → `204`, thu hồi mọi phiên, gỡ khóa tạm; `TOKEN_INVALID` | 🔧 GĐ1 |
| GET | `/v1/me` | Bearer | `{user, roles[], isCreator, channelId, unreadNotifications, preferences {sidebarCollapsed, matureVisible}}` — dữ liệu Shell (NXL-00 §7.1) | 🔧 GĐ1 |
| PATCH | `/v1/me` | Bearer | `{displayName?, birthDate?, locale?, timezone?, preferences?}` → như `GET /v1/me` | 🔧 GĐ1 |
| POST | `/v1/me/password` | Bearer | `{currentPassword, newPassword}` → `200 {accessToken, expiresIn, user}` + cookie mới, thu hồi phiên khác; `WRONG_PASSWORD` | 🔧 GĐ1 |
| POST | `/v1/creator-applications` | Bearer | `{legalName, birthDate, note?, acceptCreatorTerms}` → `201 Application`; `EMAIL_NOT_VERIFIED`, `APPLICATION_PENDING`, `ALREADY_CREATOR`, `UNDERAGE` | 🔧 GĐ1 |
| GET | `/v1/creator-applications/me` | Bearer | Đơn mới nhất hoặc `null` | 🔧 GĐ1 |
| DELETE | `/v1/creator-applications/{id}` | Bearer | Hủy đơn chờ → `204` (`CANCELLED`); `NOT_FOUND`, `INVALID_STATE` | 🔧 GĐ1 |
| GET | `/v1/admin/creator-applications?status=&cursor=&limit=` | Admin | Hàng đợi, cũ nhất trước; `status` ∈ `PENDING` (mặc định), `APPROVED`, `REJECTED`, `CANCELLED`, `ALL`; `limit` 1–50 (20) | 🔧 GĐ1 |
| GET | `/v1/admin/creator-applications/{id}` | Admin | `AdminApplication` (thêm `age`, `user`) | 🔧 GĐ1 |
| POST | `/v1/admin/creator-applications/{id}/approve` | Admin | → `creator.approved.v1` → channel-svc tạo kênh; `INVALID_STATE`, `FORBIDDEN` (tự duyệt) | 🔧 GĐ1 |
| POST | `/v1/admin/creator-applications/{id}/reject` | Admin | `{reason}` 10–500 ký tự; `INVALID_STATE` | 🔧 GĐ1 |
| GET | `/metrics` (không có tiền tố `/v1`, không qua cổng vào) | — | Prometheus: `http_requests_total`, `http_request_duration_seconds`, Go/process | ✅ GĐ0 |
| POST / DELETE | `/v1/me/blocks/{userId}` | Bearer | Block người dùng (F3) | 📋 GĐ5 |

Mã lỗi: `VALIDATION_FAILED`, `USERNAME_TAKEN`, `EMAIL_TAKEN`, `INVALID_CREDENTIALS`, `ACCOUNT_LOCKED`, `SESSION_EXPIRED`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `TOKEN_INVALID`, `WRONG_PASSWORD`, `EMAIL_NOT_VERIFIED`, `APPLICATION_PENDING`, `ALREADY_CREATOR`, `UNDERAGE`, `INVALID_STATE`, `INTERNAL_ERROR` (NXL-00 §5).

**Ngoài REST:** lệnh `identity promote-admin <email>` (cùng image identity); seed dev `admin@nexuslive.dev`, `creator@nexuslive.dev` khi `NXL_ENV=dev` và `IDENTITY_DEV_SEED=true` (mật khẩu `IDENTITY_DEV_SEED_PASSWORD`). Email xác minh / đặt lại mật khẩu do consumer `identity-mailer` gửi qua SMTP (Mailpit khi dev, UI `http://localhost:8025`).

## 5. stream-svc — buổi phát, Studio, VOD

### 5.1 Công khai

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| POST | `/v1/studio/stream-key` | Creator | Tạo/tạo lại stream key — key chỉ trả **một lần**, key cũ hết hiệu lực ngay | ✅ |
| GET | `/v1/studio/stream-key` | Creator | Đã có key chưa, tạo lúc nào (không bao giờ trả key) | ✅ |
| GET | `/v1/studio/stream` | Creator | Cài đặt buổi phát + buổi phát hiện tại + sức khỏe luồng (5s); GĐ1 thêm `channel` | ✅ · GĐ1 🔧 |
| PUT | `/v1/studio/stream` | Creator | Sửa tiêu đề, tag, nhãn mature — áp cho buổi kế tiếp và buổi đang phát | ✅ |
| POST | `/v1/studio/stream/end` | Creator | Kết thúc buổi phát → media-svc ngắt OBS, tạo VOD | ✅ |
| GET | `/v1/streams?status=live&cursor=&limit=` | — | Danh sách đang phát (LIVE + INTERRUPTED), mới nhất trước; GĐ1 thêm `channel {id, handle, displayName, avatarUrl} \| null`; GĐ3 thêm `viewerCount: number \| null` (chat-svc đếm, đọc Redis `viewers:{streamId}`) | ✅ · GĐ3 🔧 |
| GET | `/v1/streams/{id}` | — | Chi tiết, trạng thái, `manifestUrl`, `thumbnailUrl`, `vodId`; GĐ1 thêm `channel` (giữ `channelId`); GĐ3 thêm `viewerCount` | ✅ · GĐ3 🔧 |
| GET | `/v1/vods/{id}` | — / Bearer | VOD đang READY và còn hạn (hết hạn → `404`; job chuyển EXPIRED mỗi 10 phút); GĐ1 thêm `channel`; GĐ2: có token → `viewer.positionSec` | ✅ · GĐ2 🔧 |
| POST | `/v1/streams/{id}/playback` · `/v1/vods/{id}/playback` | — / Bearer | Mở phiên xem: `{acknowledgeMature?}`, header `X-NXL-Anon-Id` (Guest) → `{sessionId, manifestUrl, expiresAt, anonId?, mature{required, acknowledged}}`; 18+ chưa xác nhận → ba trường đầu `null`; tài khoản xác nhận một lần/kênh (TSD GĐ2 §4). Chữ ký URL ở GĐ5 | GĐ2 🔧 (chữ ký 📋 GĐ5) |
| PUT | `/v1/me/watch-progress/{vodId}` | Bearer | `{positionSec, keepFurther?}` → `204`; `VALIDATION_FAILED` (`positionSec: REQUIRED/RANGE`), `NOT_FOUND` (A5, UC-A07) | GĐ2 🔧 |
| GET | `/v1/me/continue-watching?limit=` | Bearer | VOD xem dở (≥ 5 s, chưa tới 10 s cuối, còn hạn), mới nhất trước: `{items: [{vod, positionSec, updatedAt}]}` (C2) | GĐ2 🔧 |
| GET / PATCH / DELETE | `/v1/studio/vods`, `/v1/studio/vods/{id}` | Bearer | Studio Content: danh sách, ẩn, xóa VOD (G2) | 📋 GĐ4 |
| GET | `/v1/channels/{channelId}/vods` | — | VOD của một kênh (tab Videos, D2) | 📋 GĐ4 |
| GET | `/metrics` (không có tiền tố `/v1`, không qua cổng vào) | — | Prometheus: `stream_live_streams`, `stream_transitions_total{to}`, `stream_authorize_total{result}`, `http_requests_total`, `http_request_duration_seconds` | ✅ L4 |

Auth `Creator` = Bearer của tài khoản đã có kênh; chưa có kênh → `403 NOT_CREATOR` (GĐ1 🔧). Đặc tả nghiệp vụ: NXL-G1.

Mã lỗi: `VALIDATION_FAILED`, `UNAUTHORIZED`, `NOT_CREATOR`, `NOT_FOUND`, `NOT_LIVE`, `INTERNAL_ERROR`.

### 5.2 Nội bộ (chỉ media-svc gọi)

| Method | Path | Mô tả | Trạng thái |
| --- | --- | --- | --- |
| POST | `/internal/v1/ingest/authorize` | `{streamKey, clientIp}` → `200 {streamId, channelId, resumed}` · `403 INVALID_STREAM_KEY` · `409 ALREADY_LIVE` · `409 RECENTLY_ENDED` (Creator vừa kết thúc, trong `STREAM_END_COOLDOWN`) · `401 UNAUTHORIZED` (sai secret) | ✅ |

Ghi chú:

- Kiến trúc §5.1 dự kiến gRPC cho lời gọi này. Đang dùng HTTP nội bộ vì chưa có toolchain protoc (TSD §6.4); hợp đồng giữ nguyên ở GĐ1.
- `channelId` trả về từ GĐ1 là id kênh thật (bảng `creator_channels`), không còn bằng `userId`.
- Lỗi `CAPACITY` do media-svc tự kiểm tra trước khi gọi.

## 6. media-svc — ingest và phát

| Giao thức | Địa chỉ | Mô tả | Trạng thái |
| --- | --- | --- | --- |
| RTMP | `rtmp://localhost:1935/live/{streamKey}` | OBS publish; chỉ H.264 + AAC; sai key / đang live / quá tải / vừa kết thúc (30s) → `Publish failed` | ✅ |
| HLS | `/hls/streams/{streamId}/master.m3u8` → `{720p,480p,360p}/index.m3u8` → `seg_NNNNN.ts` | Playlist EVENT: live, tua lại khi live, VOD dùng chung file; `thumb.jpg` cập nhật ≤ 30s | ✅ |
| HTTP | `GET :8091/healthz` | `{status, activeStreams, rejectedPublishes}` | ✅ |
| HTTP | `GET :8091/dev/sessions` | Phiên đang xử lý: trạng thái, bậc, tốc độ ffmpeg, frame rơi, lỗi upload | ⛔ ✅ |
| HTTP | `GET :8091/dev/player` | Trang thử hls.js (đổi chất lượng, tua, độ trễ) | ⛔ ✅ |
| HTTP | `GET :8091/hls/streams/*` | Proxy tới bucket MinIO `nxl-media` | ⛔ ✅ |
| HTTP | `GET :8091/metrics` | Prometheus: `media_active_streams`, `media_ffmpeg_speed{stream}`, `media_dropped_frames_total{stream}`, `media_upload_seconds`, `media_upload_errors_total`, `media_publish_rejected_total{reason}` | ✅ L4 |

Object trong MinIO:

- Đường dẫn: `nxl-media/streams/{streamId}/…`. Trình duyệt truy cập qua cổng vào `/hls/...` (hoặc Vite proxy `/hls` → MinIO khi không chạy cổng vào).
- Cache-Control theo TSD §5.4.
- Đọc công khai ở dev; URL có chữ ký ở L4.

## 6A. channel-svc — kênh (GĐ1 tối thiểu 🔧)

Cổng 8094, profile compose `nxl`, Swagger `/swagger/index.html`, `/healthz`, `/metrics`. Dùng database `nxl_channel`
(bảng `channels`, `outbox`, `processed_events`), không migration mới ở GĐ1.

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| GET | `/v1/channels/{handle}` | — | `200 {id, handle, displayName, bio, avatarUrl, isLive, currentStreamId, createdAt}`; `404 NOT_FOUND` | 🔧 GĐ1 |
| GET | `/metrics` (không qua cổng vào) | — | Prometheus | 🔧 GĐ1 |

Sự kiện:

- Nghe `creator.approved.v1` → tạo kênh idempotent theo `owner_id`; handle = `lower(username)`, trùng thì thêm hậu tố số.
- Phát `channel.created.v1` (outbox).
- Nghe `stream.started.v1` / `stream.ended.v1` → `is_live`, `current_stream_id`.

Phần còn lại (follow, lịch, hồ sơ đầy đủ, khám phá): §7.2, GĐ4.

## 6B. chat-svc — realtime gateway và chat (GĐ3 🔧)

Hợp đồng đầy đủ (khung WebSocket, snapshot, sự kiện, khóa Redis): TSD `docs/plans/technical/2026-10-14-gd03-chat-realtime.md` §5.

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| WS | `/v1/ws` | Tin đầu `{"op":"auth","token":<JWT>\|null,"anonId"?}` trong 5 s | Một kết nối cho mọi chủ đề: `chat:{streamId}`, `stream:{streamId}`, `user:{userId}` (chỉ khung). `subscribe` kèm `lastSeq` để resume; quá bộ đệm → `resync`. Mã đóng `1012` (instance tắt), `4001`, `4008`, `4029` (NXL-00 §10) | GĐ3 🔧 |
| GET | `/v1/chat/rooms/{streamId}/messages?before=&limit=` | — | Lịch sử, mới nhất trước: `{items: Message[], nextCursor}`; `Message` cùng dạng `data` của event `message` | GĐ3 🔧 |
| GET | `/v1/chat/emotes?channelId=` | — | Emote toàn cầu `{items: [{id, code, imageUrl, animated, scope}]}` (`channelId` bỏ qua tới GĐ6) | GĐ3 🔧 |
| GET | `/v1/channels/{channelId}/chat-settings` | — | `{slowSec, followersMin, subsOnly, updatedAt}`; kênh chưa biết → `404` | GĐ3 🔧 |
| PUT | `/v1/channels/{channelId}/chat-settings` | Bearer (chủ kênh, Mod, Admin) | Đổi chế độ; mọi phòng đang mở của kênh nhận `mode_change` + `system`. `VALIDATION_FAILED` (`ENUM`), `FORBIDDEN`, `UNAUTHORIZED` | GĐ3 🔧 |
| GET | `/metrics` (không qua cổng vào) | — | `chat_ws_connections`, `chat_ws_subscriptions`, `chat_messages_sent_total`, `chat_events_delivered_total`, `chat_fanout_latency_seconds`, `chat_send_rejects_total{code}`, `chat_ws_resyncs_total`, `http_*` | GĐ3 🔧 |

Mã lỗi (REST và frame `error`): `VALIDATION_FAILED`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `RATE_LIMITED`, `EMAIL_NOT_VERIFIED`, `CHAT_CLOSED`, `CHAT_READ_ONLY`, `CHAT_DISABLED`, `CHAT_DUPLICATE`, `CHAT_SLOW_MODE`, `CHAT_FOLLOWERS_ONLY`, `CHAT_SUBS_ONLY`, `CHAT_TIMED_OUT`, `CHAT_BANNED`, `INTERNAL_ERROR`. `details` là chuỗi (`retryAfterSec: "5"`).

**Ngoài REST:** `chat dev-grant follow|sub|mod|vip|owner --channel <id> --user <id> [--minutes N] [--tier N] [--revoke]` (`make nxl-chat-dev-grant`, chỉ `NXL_ENV=dev`) ghi dữ liệu demo cho Followers-only / Subs-only / Mod / VIP.

## 7. Dịch vụ dự kiến

Đường dẫn dưới đây là **bản nháp** để giữ chỗ và kiểm tra trùng lặp. Spec tương ứng chốt chi tiết trước khi làm (roadmap §4).

### 7.1 chat-svc — phần còn dự kiến (GĐ5)

Phần đã chạy ở GĐ3: §6B. Còn lại:

| Method | Path | Mô tả |
| --- | --- | --- |
| POST | `/v1/streams/{id}/chat/messages/{msgId}/delete` · `/pin` | Hành động mod (F1) |
| POST / DELETE | `/v1/channels/{id}/bans/{userId}` · `/timeouts/{userId}` | Ban, timeout (F1) |
| GET / PUT / DELETE | `/v1/channels/{id}/roles/{userId}` | Gán Mod/VIP (G2 Community) |

### 7.2 channel-svc — kênh, follow, lịch, khám phá (GĐ1 tối thiểu, GĐ4 đầy đủ; NXL-C, NXL-D)

GĐ1 đã có phần tối thiểu (§6A); cổng vào đã định tuyến `/v1/channels`. Hợp đồng chi tiết phần NXL-D: NXL-D §13.2.
Khi làm GĐ4, thêm tiền tố `/v1/schedules`, `/v1/studio/channel`, `/v1/me/following`, `/v1/categories`, `/v1/home`,
`/v1/search` vào `deploy/nginx/nginx.conf` (hiện `/v1/studio/*` thuộc stream-svc, `/v1/me*` thuộc identity — cần tách
route khi đó).

| Method | Path | Mô tả |
| --- | --- | --- |
| GET | `/v1/channels/{handle}` | Bản đầy đủ: hồ sơ kênh + trạng thái follow của người xem (D2); GĐ1 đã có bản rút gọn (§6A) |
| GET | `/v1/channels/{id}/vods?cursor=` | Tab Videos (dữ liệu từ stream-svc) |
| GET | `/v1/channels/{id}/schedule?from=&to=` | Lịch phát theo cửa sổ 14 ngày (D3) |
| PATCH · PUT | `/v1/studio/channel` · `/v1/studio/channel/avatar` · `/banner` | Sửa hồ sơ kênh, tải ảnh |
| POST · PUT · DELETE | `/v1/channels/{id}/follow` | Follow, đổi mức chuông (`ALL`/`LIVE_ONLY`/`OFF`), bỏ follow (D3) |
| GET | `/v1/me/following?live=` | Trang Following, sidebar (C1, C5) |
| POST · DELETE | `/v1/schedules/{id}/reminders` | Đặt / bỏ nhắc một lần phát (`occurrenceAt`) |
| POST/PUT/DELETE | `/v1/studio/schedules` | Tạo/sửa/hủy lịch (NXL-G2) |
| GET | `/v1/categories` · `/v1/categories/{slug}` | Browse & Category (C3) |
| GET | `/v1/home` | Dữ liệu trang chủ (C2) |
| GET | `/v1/search?q=&type=` | Tìm kiếm có/không dấu (C4) |

### 7.3 safety-svc — báo cáo và quản trị (GĐ5, NXL-F)

| Method | Path | Mô tả |
| --- | --- | --- |
| POST | `/v1/reports` | Báo cáo stream / tin nhắn / kênh (F3) |
| GET | `/v1/admin/reports?status=` | Hàng đợi báo cáo (Admin) |
| POST | `/v1/admin/reports/{id}/resolve` | Xử lý báo cáo |
| POST | `/v1/admin/users/{id}/lock` · `/v1/admin/streams/{id}/takedown` | Khóa tài khoản, gỡ stream (F4) |
| GET | `/v1/admin/audit-log` | Nhật ký thao tác Admin |

### 7.4 notification-svc (GĐ5, NXL-E)

| Method | Path | Mô tả |
| --- | --- | --- |
| GET | `/v1/notifications?cursor=` | Notification center (E2) |
| POST | `/v1/notifications/{id}/read` · `/v1/notifications/read-all` | Đánh dấu đã đọc |
| GET / PUT | `/v1/me/notification-settings` | Cài đặt nhận thông báo |

### 7.5 billing-svc — subscription, thanh toán sandbox (GĐ6, NXL-H)

| Method | Path | Mô tả |
| --- | --- | --- |
| GET | `/v1/channels/{id}/tiers` | 3 tier subscription của kênh (H1) |
| POST | `/v1/checkout` | Tạo đơn, trả URL thanh toán PSP — bắt buộc `Idempotency-Key` |
| GET | `/v1/checkout/{id}` | Trạng thái đơn sau khi PSP redirect về |
| POST | `/v1/webhooks/{psp}` | Webhook PSP (xác minh chữ ký, chống trùng) |
| GET | `/v1/me/subscriptions` · POST `/v1/me/subscriptions/{id}/cancel` | Quản lý / hủy (H1) |
| GET | `/v1/me/transactions` | Lịch sử giao dịch (H4) |
| GET | `/v1/studio/balance` · POST `/v1/studio/payout-requests` | Số dư Creator, yêu cầu rút tiền (H2) |

## 8. Sự kiện (NATS JetStream)

**Cách truyền (hiện thực):**

- JetStream stream `NXL`, subject `nxl.<type>` (ví dụ `nxl.stream.ended.v1`), giữ 7 ngày.
- Header `Nats-Msg-Id` là id sự kiện (UUID): JetStream bỏ bản trùng trong 2 phút; bên nhận ghi `processed_events` để idempotent.
- Payload là JSON của chính sự kiện. Hợp đồng ở `pkg/events/events.go`.
- Bên phát có DB đi qua **outbox** (`pkg/outbox`); media-svc không có DB nên publish trực tiếp.

- Header (GĐ1 🔧): `Nxl-Occurred-At` (RFC 3339; outbox: thời điểm ghi dòng outbox) và `Nxl-Producer` (tên service). Errata E-11 đã chốt: metadata ở header, body là payload (NXL-00 §11; kiến trúc §5.2 đã sửa).

| Subject (`nxl.` +) | Bên phát | Bên nhận | Payload chính | Trạng thái |
| --- | --- | --- | --- | --- |
| `user.registered.v1` | identity (outbox) | chat (bản sao `users`, GĐ3); (notification — GĐ5) | `userId, username, displayName, email` | ✅ phát |
| `auth.email_requested.v1` | identity (outbox) | identity (`identity-mailer`, gửi SMTP); GĐ5: notification | `userId, email, purpose (VERIFY\|RESET), token, locale, username` — token gốc, không log | 🔧 GĐ1 |
| `user.email_verified.v1` | identity (outbox) | chat (GĐ3) | `userId` | 🔧 GĐ1 |
| `user.profile_updated.v1` | identity (outbox) | chat (GĐ3); GĐ4: channel | `userId, displayName` | 🔧 GĐ1 |
| `creator.application_submitted.v1` | identity (outbox) | — (GĐ5: notification) | `applicationId, userId` | 🔧 GĐ1 |
| `creator.approved.v1` | identity (outbox) | channel (tạo kênh) | `applicationId, userId, username, displayName` | 🔧 GĐ1 |
| `creator.rejected.v1` | identity (outbox) | — (GĐ5: notification) | `applicationId, userId, reason` | 🔧 GĐ1 |
| `channel.created.v1` | channel (outbox) | stream (`creator_channels`), identity (`user_channels`), chat (`channels`: badge Streamer, quyền đổi chế độ, GĐ3) | `channelId, ownerId, handle, displayName` | 🔧 GĐ1 |
| `ingest.started.v1` | media | stream | `streamId, channelId, renditions[], sourceWidth, sourceHeight, resumed` | ✅ |
| `ingest.stopped.v1` | media | stream | `streamId, reason` (`publisher_disconnected` / `transcoder_failed` / `kicked` / `shutdown` / `recovered`) | ✅ |
| `ingest.health.v1` | media (mỗi 5s) | stream (chỉ giữ trong bộ nhớ) | `streamId, fps, bitrateKbps, droppedFrames, speed, uploadErrors` | ✅ |
| `vod.processed.v1` | media | stream | `streamId, durationSec, playlistPath, thumbnailPath` | ✅ |
| `stream.started.v1` | stream (outbox) | channel (`is_live`, GĐ1); chat (mở phòng, GĐ3) | `streamId, channelId, title` | ✅ |
| `stream.ended.v1` | stream (outbox) | media (kick + chốt VOD), channel (GĐ1); chat (phòng chỉ đọc, GĐ3) | `streamId, channelId, endReason` | ✅ |
| `stream.status_changed.v1` | stream (outbox, cùng transaction `LIVE`↔`INTERRUPTED`) | chat (đẩy `stream:{id}` `status_changed`) | `streamId, channelId, status (LIVE\|INTERRUPTED), occurredAt` | 🔧 GĐ3 |
| `stream.updated.v1` | stream (outbox, sửa cài đặt khi đang phát) | chat (`title_changed`) | `streamId, channelId, title, tags[]` | 🔧 GĐ3 |
| `stream.vod_ready.v1` | stream (outbox, VOD `READY`) | chat (`vod_ready`) | `streamId, channelId, vodId` | 🔧 GĐ3 |
| `stream.peak_viewers.v1` | chat (outbox, khi phòng chuyển `READ_ONLY`) | stream (`streams.peak_viewers`) | `streamId, peakViewers` | 🔧 GĐ3 |
| `user.locked.v1` · `user.unlocked.v1` · `channel.updated.v1` | identity, channel | chat, stream, search | | 📋 |
| `follow.created.v1` · `follow.deleted.v1` | channel | chat (bản sao Redis `follow:{channelId}`, consumer sẵn từ GĐ3), notification | `userId, channelId, createdAt` | 📋 GĐ4 (chat nghe 🔧) |
| `schedule.*` · `reminder.due.v1` | channel | notification | | 📋 GĐ4 |
| `chat.message_sent.v1` · `chat.mention.v1` · `chat.mod_action.v1` | chat | notification, safety | | 📋 GĐ3 |
| `safety.stream_takedown.v1` · `safety.user_actioned.v1` · `safety.report_resolved.v1` | safety | stream, identity, notification | | 📋 GĐ5 |
| `notification.created.v1` | notification | chat (đẩy `user:{id}`) | | 📋 GĐ5 |
| `subscription.activated/renewed/expired/cancelled.v1` | billing (outbox) | chat, notification | | 📋 GĐ6 |

Consumer bền hiện có:

- `stream-svc`: `ingest.*`, `vod.processed`; GĐ1: `channel.created`.
- `media-svc`: `stream.ended`.
- GĐ1 🔧: `identity-svc` (`channel.created`), `identity-mailer` (`auth.email_requested`), `channel-svc` (`creator.approved`, `stream.started`, `stream.ended`).

Xem tồn đọng: `http://localhost:8222/jsz?consumers=1`.

## 9. Quy trình thay đổi API

Mỗi thay đổi API (thêm, sửa, xóa endpoint hoặc sự kiện) làm đủ các bước sau trong **cùng commit** với code:

1. Sửa handler và chú thích swaggo (`@Summary`, `@Param`, `@Success`, `@Failure`, `@Router`, `@Security`).
2. `make nxl-swag` → commit `services/<svc>/docs/`.
3. Cập nhật bảng ở tài liệu này: trạng thái 📋 → 🔧 → ✅, đường dẫn thật, mã lỗi mới.
4. Sự kiện: sửa `pkg/events/events.go` và bảng §8. Đổi payload theo cách phá vỡ tương thích → tăng hậu tố `.v2`, phát song song tới khi mọi bên nhận chuyển xong.
5. Web: cập nhật hàm gọi API và `src/lib/api-error.ts` nếu có mã lỗi mới.
6. Ghi một dòng vào §10.

Phá vỡ tương thích REST: xem §3.1.

## 10. Lịch sử thay đổi

| Ngày | Thay đổi |
| --- | --- |
| 2026-10-14 | v1.4 — GĐ2 backend: phiên xem + cổng 18+ phía server, tiến độ xem theo tài khoản, continue-watching; Nginx chuyển `/v1/me/(watch-progress|continue-watching)` sang stream-svc, CORS thêm `X-NXL-Anon-Id` |
| 2026-10-13 | v1.3 — GĐ1: toàn bộ endpoint identity của TSD GĐ1 §4.1 (cột Auth), header `X-NXL-Client`, rate limit / CORS / header bảo mật ở cổng vào (§1.1), channel-svc cổng 8094 (§6A, chat-svc dời 8095), stream-svc `NOT_CREATOR` + `channel`, 8 sự kiện mới + header `Nxl-Occurred-At`/`Nxl-Producer`; hai web dev 5173/5174; đơn Creator ở GĐ1 (errata E-14) |
| 2026-10-12 | L4: `409 RECENTLY_ENDED` ở authorize nội bộ; `/metrics` của stream-svc và media-svc; `/healthz` media thêm `rejectedPublishes`; `ingest.stopped` thêm lý do `recovered`; web có trang `/vod/:vodId` |
| 2026-10-11 | Middleware xác thực chấp nhận cả `Authorization: <jwt>` không có `Bearer ` (dán token trong Swagger) |
| 2026-10-11 | Bản đầu: identity-svc (6 endpoint), stream-svc (8 công khai + 1 nội bộ), media-svc (RTMP, HLS, dev), 7 sự kiện đang chạy; nháp API của chat/channel/safety/notification/billing |
