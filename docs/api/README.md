# NexusLive — Danh mục API

| | |
| --- | --- |
| **Phiên bản** | 1.11 |
| **Cập nhật** | 2026-10-21 |
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
| channel-svc | 8094 | `http://localhost:8094/v1` | <http://localhost:8094/swagger/index.html> | GĐ1 · GĐ4 🔧 |
| chat-svc (realtime gateway + chat), 2 instance | 8095 (`chat-1`), 8096 (`chat-2`) | `ws://localhost:8080/v1/ws` (qua cổng vào), `http://localhost:8095/v1` | <http://localhost:8095/swagger/index.html> | GĐ3 🔧 |
| safety-svc | 8099 | `http://localhost:8099/v1` | <http://localhost:8099/swagger/index.html> | GĐ5 🔧 |
| notification-svc | 8097 | `http://localhost:8097/v1` | <http://localhost:8097/swagger/index.html> | GĐ5 🔧 |
| billing-svc | 8098 | `http://localhost:8098/v1`; ⛔ `http://localhost:8098/dev/fake-psp` (không qua cổng vào) | <http://localhost:8098/swagger/index.html> | GĐ6 🔧 |
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
| `= /v1/webhooks/sepay` | billing-svc :8098 | GĐ6: chỉ `POST` (khác → `403`), body ≤ 64 KB (`413`), vùng `nxl_webhook` (`NXL_EDGE_WEBHOOK_RATE`, mặc định `60r/m`, burst 20); không JWT / CORS |
| `~ ^/v1/studio/uploads(/\|$)` | stream-svc :8092 | Upload: đặt trước route Studio của stream; `client_max_body_size 9m`, `proxy_request_buffering off`, timeout đọc/gửi 120 s, vùng `nxl_upload` (`NXL_EDGE_UPLOAD_RATE`, mặc định `300r/m`, burst 20) |
| `/v1/ws` | chat-svc (upstream `nxl_chat`: `chat-1`, `chat-2`) | WebSocket (`Upgrade`), `proxy_read_timeout 75s`, không rate limit theo request (GĐ3) |
| `/v1/chat/*`, `/v1/channels/{id}/(chat-settings\|timeouts\|bans\|restrictions\|blocked-terms\|roles\|users)*` | chat-svc | Đặt trước route channel-svc (GĐ3; GĐ5 thêm hành động mod, từ khóa chặn, vai trò, thẻ người dùng) |
| `/v1/me/channel-roles` | chat-svc | Exact match (GĐ5) |
| `/v1/channels/{id}/subscription-tiers` | billing-svc | GĐ6, trước route channel-svc |
| `/v1/me/(subscriptions\|payments)*`, `/v1/checkout*` | billing-svc | GĐ6; `/v1/me` còn lại về identity |
| `/v1/admin/(payouts\|checkout-orders)*` | billing-svc | GĐ6, trước route admin của identity / safety |
| `/v1/studio/monetization*` | billing-svc | GĐ6, trước route Studio của channel-svc / stream-svc |
| `/v1/me/notification-settings`, `/v1/notifications*` | notification-svc :8097 | GĐ5 (`/v1/notifications/unsubscribe` không cần token); service dừng → `503 SERVICE_UNAVAILABLE` của cổng vào |
| `/v1/reports` | safety-svc :8099 | Exact match (GĐ5) |
| `/v1/admin/creator-applications*`, `GET /v1/admin/users/{id}` | identity-svc | GĐ5: `…/users/{id}/lock` không khớp, đi safety |
| `/v1/admin/*` còn lại | safety-svc :8099 | Report, khóa / mở, kênh, stream, audit log, tra cứu (GĐ5) |
| `/emotes/*` | file tĩnh `deploy/emotes/*.webp` | `Cache-Control: public, max-age=604800` (GĐ3) |
| `/v1/channels/{id}/vods` | stream-svc :8092 | Trước route channel-svc (GĐ4) |
| `/v1/channels/*` | channel-svc :8094 | GĐ1; GĐ4: follow, lịch của kênh |
| `/v1/me/following*`, `/v1/me/search-history*` | channel-svc | GĐ4; phần còn lại của `/v1/me` về identity |
| `/v1/me/watch-progress*`, `/v1/me/continue-watching` | stream-svc | GĐ2 |
| `/v1/studio/channel/avatar`, `/v1/studio/channel/banner` | channel-svc | `client_max_body_size 6m` (GĐ4) |
| `/v1/studio/channel*`, `/v1/studio/schedules*` | channel-svc | Trước route Studio của stream-svc (GĐ4) |
| `/v1/categories*`, `/v1/search*`, `/v1/schedules*` | channel-svc | GĐ4 |
| `/v1/studio/*` còn lại, `/v1/streams/*`, `/v1/vods/*` | stream-svc :8092 | |
| `/v1/*` còn lại | identity-svc :8090 | Gồm `/v1/auth/*`, `/v1/me*` (cả `/v1/me/blocks*`), `/v1/creator-applications*` |
| `/hls/t/{expires}/{md5}/{streams\|videos}/{id}/*` | MinIO, bucket `nxl-media` | URL có chữ ký (`secure_link`, GĐ5; Upload thêm prefix `videos/{vodId}/`): `md5 = base64url(MD5("{expires}{prefix} {NXL_HLS_SECRET}"))` với `prefix` = `streams/{id}/` hoặc `videos/{id}/`; sai / hết hạn → `403`; `.m3u8` `no-cache`, `.ts` `private, max-age=3600` |
| `/hls/*` | MinIO, bucket `nxl-media` | Chỉ `GET`/`HEAD`; `.jpg` công khai; `.m3u8` / `.ts` chỉ khi `NXL_HLS_ALLOW_UNSIGNED=1` (mặc định), ngược lại `403`; không rate limit |
| `/media/*` | MinIO, bucket `nxl-public` | Ảnh đại diện, ảnh bìa, ảnh danh mục; chỉ `GET`/`HEAD`, `Cache-Control: public, max-age=86400` (GĐ4) |
| `/internal/*`, `/metrics`, `/swagger`, còn lại | — | `404 NOT_FOUND` |

Lỗi do chính cổng vào sinh ra dùng cùng envelope: `503 SERVICE_UNAVAILABLE` (service không phản hồi), `413 PAYLOAD_TOO_LARGE` (body > 1 MB), `429 RATE_LIMITED`, `403 FORBIDDEN`, `404 NOT_FOUND`. Mỗi response có `X-Request-Id` (lấy từ request nếu client gửi). URL HLS có chữ ký: GĐ5 (bật bằng `STREAM_HLS_SIGNING=true` rồi `NXL_HLS_ALLOW_UNSIGNED=0`, TSD GĐ5 §9.3).

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
| 422 | `UNDERAGE`; GĐ6: `CANNOT_SUBSCRIBE_SELF`, `IDEMPOTENCY_KEY_REUSED` |
| 429 | `RATE_LIMITED` — cổng vào (§1.1) hoặc identity (gửi lại email) |
| 500 | `INTERNAL_ERROR` — chi tiết chỉ nằm trong log |

### 3.6 Idempotency (GĐ6 🔧)

- `POST /v1/checkout/orders`, `POST /v1/me/subscriptions/{id}/cancel`, `POST /v1/studio/monetization/payouts` bắt buộc header
  `Idempotency-Key` (UUID do client sinh; thiếu → `400 details.idempotencyKey = REQUIRED`, sai dạng → `FORMAT`).
- Cùng key + cùng body (so sánh JSON đã chuẩn hóa) → trả lại đúng response `2xx` đã lưu, header `Idempotent-Replay: true`; cùng
  key, body khác → `422 IDEMPOTENCY_KEY_REUSED`; request đầu chưa xong → `409 CONFLICT` (treo quá 60 s thì được chạy lại).
  Chỉ lưu `2xx`: lỗi được tính lại khi gửi lại cùng key. Key giữ 24 giờ, theo (người dùng, method, đường dẫn).
- Webhook SePay xử lý trùng an toàn: unique `(psp_ref, type)` + khóa dòng đơn (§6E).

## 4. identity-svc — tài khoản và phiên

Hợp đồng đầy đủ: TSD GĐ1 §4.1 (`../plans/technical/2026-10-13-gd01-identity-web-shell.md`) = NXL-D v1.2 §13.1.
Auth: `—` không cần · `Bearer` · `cookie+H` = cookie `nxl_rt` + header `X-NXL-Client` · `Admin` = Bearer có `role = ADMIN`.

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| POST | `/v1/auth/register` | — | `{username, displayName?, email, password, acceptTerms}` → `201 {accessToken, expiresIn, user}` + cookie. GĐ1: `acceptTerms` bắt buộc, `details.<field> = TAKEN` khi trùng | ✅ · GĐ1 🔧 |
| POST | `/v1/auth/login` | — | `{login, password}` (email hoặc username). GĐ1: sai 10 lần / 15 phút → khóa tạm 15 phút, `403 ACCOUNT_LOCKED` + `details.lockedUntil` | ✅ · GĐ1 🔧 |
| POST | `/v1/auth/refresh` | cookie+H | Cấp access token mới, xoay vòng refresh token (phát hiện dùng lại → thu hồi mọi phiên). GĐ5: tài khoản đang bị khóa (Admin hoặc khóa tạm) → `403 ACCOUNT_LOCKED` như đăng nhập (`details.lockedUntil` khi có hạn, cookie giữ nguyên) thay cho `401 SESSION_EXPIRED` | ✅ · GĐ1 🔧 header · GĐ5 🔧 |
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
| GET | `/v1/me/blocks?fields=ids&cursor=&limit=` | Bearer | `{items: [{user, createdAt}], nextCursor}`; `fields=ids` → `{ids[]}` (tối đa 1.000) | GĐ5 🔧 |
| PUT · DELETE | `/v1/me/blocks/{userId}` | Bearer | Block (`204`, lặp lại → `204`) / bỏ block; chính mình → `userId = INVALID`; quá 1.000 → `409 CONFLICT details.limit` | GĐ5 🔧 |
| GET | `/v1/admin/users/{id}` | Admin | `AdminUser {…, status, lockedUntil, lockKind (FAILED_LOGIN\|ADMIN), lockReason, channelId}` | GĐ5 🔧 |

Mã lỗi: `VALIDATION_FAILED`, `USERNAME_TAKEN`, `EMAIL_TAKEN`, `INVALID_CREDENTIALS`, `ACCOUNT_LOCKED`, `SESSION_EXPIRED`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `TOKEN_INVALID`, `WRONG_PASSWORD`, `EMAIL_NOT_VERIFIED`, `APPLICATION_PENDING`, `ALREADY_CREATOR`, `UNDERAGE`, `INVALID_STATE`, `INTERNAL_ERROR` (NXL-00 §5).

**Ngoài REST:** lệnh `identity promote-admin <email>` (cùng image identity); seed dev `admin@nexuslive.dev`, `creator@nexuslive.dev` khi `NXL_ENV=dev` và `IDENTITY_DEV_SEED=true` (mật khẩu `IDENTITY_DEV_SEED_PASSWORD`). Email xác minh / đặt lại mật khẩu do consumer `identity-mailer` gửi qua SMTP (Mailpit khi dev, UI `http://localhost:8025`).

## 5. stream-svc — buổi phát, Studio, VOD

### 5.1 Công khai

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| POST | `/v1/studio/stream-key` | Creator | Tạo/tạo lại stream key — key chỉ trả **một lần**, key cũ hết hiệu lực ngay | ✅ |
| GET | `/v1/studio/stream-key` | Creator | Đã có key chưa, tạo lúc nào (không bao giờ trả key) | ✅ |
| GET | `/v1/studio/stream` | Creator | Cài đặt buổi phát + buổi phát hiện tại + sức khỏe luồng (5s); GĐ1 thêm `channel` | ✅ · GĐ1 🔧 |
| PUT | `/v1/studio/stream` | Creator | Sửa tiêu đề, tag, nhãn mature — áp cho buổi kế tiếp và buổi đang phát; GĐ4 thêm `categoryId: uuid \| null` (vắng = bỏ danh mục; sai → `categoryId = INVALID`) | ✅ · GĐ4 🔧 |
| GET | `/v1/studio/dashboard?range=7d\|28d` | Creator | `{current, recentStreams[≤5], stats {range, streamCount, liveSeconds, vodReadyPublic, vodReadyHidden}}` | GĐ4 🔧 |
| POST | `/v1/studio/stream/end` | Creator | Kết thúc buổi phát → media-svc ngắt OBS, tạo VOD | ✅ |
| GET | `/v1/streams?status=live&category=&language=&tags=&sort=&cursor=&limit=` | — | Danh sách đang phát (LIVE + INTERRUPTED, gồm 18+ có `isMature`); GĐ1 thêm `channel`; GĐ3 `viewerCount`; GĐ4: `category` = 1–30 id (dấu phẩy), `language` `^[a-z]{2}$`, `tags` ≤ 5 (khớp tất cả), `sort` = `viewers` (mặc định, = `trending`) / `newest`; `VALIDATION_FAILED` (`category = INVALID`, `language = FORMAT`, `tags = TAG_COUNT`, `sort = ENUM`, `cursor = INVALID`) | ✅ · GĐ4 🔧 |
| GET | `/v1/streams/recommended?limit=` | — | Hero / Live đề xuất / sidebar Guest: chỉ `LIVE`, không 18+, theo người xem (1–24, mặc định 12) | GĐ4 🔧 |
| GET | `/v1/streams/{id}` | — / Bearer | Chi tiết; GĐ4: `status` có thể `SCHEDULED` / `CANCELLED`, thêm `category {id} \| null`, `scheduleId`, `scheduledStartAt`; `vodId = null` khi VOD ẩn và người gọi không phải chủ kênh | ✅ · GĐ4 🔧 |
| GET | `/v1/vods?category=&cursor=&limit=` · `/v1/vods/recommended?limit=` | — | VOD `READY` + `PUBLIC` còn hạn (video tải lên không hết hạn), mới nhất trước; recommended bỏ 18+, không phân trang | GĐ4 🔧 · Upload 🔧 |
| GET | `/v1/channels/{id}/vods?source=&cursor=&limit=` | — | Tab Video của kênh; `source = LIVE\|UPLOAD` (sai → `source = ENUM`); kênh lạ → `404` | GĐ4 🔧 · Upload 🔧 |
| GET | `/v1/vods/{id}` | — / Bearer | VOD đang READY và còn hạn; GĐ2: có token → `viewer.positionSec`; GĐ4 thêm `visibility`, `isMature`, `category`; VOD `HIDDEN` → `404` trừ chủ kênh (kể cả `/playback`). Upload: `VOD` (mọi danh sách công khai, continue-watching) thêm `source: LIVE\|UPLOAD`, `description`, `publishedAt`; `streamId`, `expiresAt` = `null` với video tải lên | ✅ · GĐ4 · Upload 🔧 |
| POST | `/v1/streams/{id}/playback` · `/v1/vods/{id}/playback` | — / Bearer | Mở phiên xem: `{acknowledgeMature?}`, header `X-NXL-Anon-Id` (Guest) → `{sessionId, manifestUrl, expiresAt, anonId?, mature{required, acknowledged}}`; 18+ chưa xác nhận → ba trường đầu `null`; tài khoản xác nhận một lần/kênh (TSD GĐ2 §4). GĐ5: khi `STREAM_HLS_SIGNING=true`, `manifestUrl` = `/hls/t/{expires}/{md5}/streams/{id}/master.m3u8` (hạn = `expiresAt`, 4 h), DTO công khai trả `manifestUrl = ""`; kênh bị khóa → `403 CHANNEL_SUSPENDED`. Upload: video tải lên ký theo `videos/{vodId}/`; cổng 18+ đọc `isMature` của chính VOD | GĐ2 · GĐ5 · Upload 🔧 |
| PUT | `/v1/me/watch-progress/{vodId}` | Bearer | `{positionSec, keepFurther?}` → `204`; `VALIDATION_FAILED` (`positionSec: REQUIRED/RANGE`), `NOT_FOUND` (A5, UC-A07) | GĐ2 🔧 |
| GET | `/v1/me/continue-watching?limit=` | Bearer | VOD xem dở (≥ 5 s, chưa tới 10 s cuối, còn hạn), mới nhất trước: `{items: [{vod, positionSec, updatedAt}]}` (C2) | GĐ2 🔧 |
| GET | `/v1/studio/vods?visibility=&status=&source=&cursor=&limit=` | Creator | VOD `READY` + `EXPIRED` + `PROCESSING` + `FAILED` của kênh (gồm ẩn): `StudioVod {id, streamId\|null, source, title, description, durationSec, thumbnailUrl, manifestUrl, status, visibility, isMature, category, createdAt, publishedAt\|null, expiresAt\|null, processing {percent, stage\|null}\|null, failureReason\|null, retryable}`; `status` thêm `PROCESSING\|FAILED`, `source = LIVE\|UPLOAD`; `processing` chỉ khi `PROCESSING`, `manifestUrl` rỗng khi chưa `READY`; web hỏi lại mỗi 3 s khi có video đang xử lý | GĐ4 · Upload 🔧 |
| PATCH | `/v1/studio/vods/{id}` | Creator | `{title?, visibility?, description?, categoryId?, isMature?}` → `200 StudioVod`; `body = REQUIRED`, `title = LENGTH`, `visibility = ENUM`; `EXPIRED` → `409 INVALID_STATE`. Upload: `description` (≤ 5000, `LENGTH`), `categoryId` (uuid, `""` = bỏ, `INVALID`), `isMature` chỉ cho video tải lên (VOD live → `409`); video tải lên sửa được khi `READY` / `PROCESSING`, `FAILED` → `409`; `vod.updated.v1` chỉ khi VOD `READY` | GĐ4 · Upload 🔧 |
| DELETE | `/v1/studio/vods/{id}` | Creator | Xóa mềm `READY` / `EXPIRED` / `PROCESSING` / `FAILED` → `204` + `vod.deleted.v1 {…, source, playlistPrefix}` (media-svc xóa file); đã xóa / kênh khác → `404`; file gốc của video tải lên do stream-svc xóa (job `STREAM_UPLOAD_EXPIRY_TICK`) | GĐ4 · Upload 🔧 |
| POST | `/v1/studio/vods/{id}/retry` | Creator | Video tải lên `FAILED` có `retryable = true`, file gốc còn (≤ 3 ngày) → `202 StudioVod` (`PROCESSING`, phát lại `upload.completed.v1`); khác → `409 INVALID_STATE`; kênh khóa → `403 CHANNEL_SUSPENDED`; MinIO lỗi / tắt tải lên → `503 STORAGE_UNAVAILABLE` | Upload 🔧 |
| POST | `/v1/studio/uploads` | Creator | `{fileName 1–255 (.mp4 .mov .mkv .webm), sizeBytes ≥ 1, contentType?, title 1–140, description? ≤ 5000, categoryId?, isMature, visibility? = PUBLIC}` → `201 Upload {id, status, fileName, sizeBytes, partSize, partCount, uploadedParts [], vodId\|null, createdAt, expiresAt}`; `400 VALIDATION_FAILED`, `413 FILE_TOO_LARGE` (`details.maxBytes`), `422 UNSUPPORTED_FORMAT`, `429 UPLOAD_LIMIT_REACHED` (`details.limit = ACTIVE\|DAILY`, `details.retryAfterSec`, header `Retry-After`; 3 đang tải + đang xử lý / kênh, 10 phiên / 24 h), `403 CHANNEL_SUSPENDED`, `503 STORAGE_UNAVAILABLE` | Upload 🔧 |
| PUT | `/v1/studio/uploads/{id}/parts/{n}` | Creator | Body nhị phân `application/octet-stream`; phần `n < partCount` đúng `partSize` (8 MiB), phần cuối = phần còn lại; gửi lại cùng `n` thì ghi đè → `200 {partNumber, receivedBytes}`; `400 INVALID_PART` (`details.expectedBytes` khi sai kích thước), `404`, `409 INVALID_STATE` (`COMPLETED`), `410 UPLOAD_EXPIRED` (`EXPIRED\|ABORTED` hoặc quá `expiresAt`), `403 CHANNEL_SUSPENDED`, `503 STORAGE_UNAVAILABLE` | Upload 🔧 |
| GET | `/v1/studio/uploads/{id}` | Creator | `200 Upload` (`uploadedParts` tăng dần, để gửi tiếp phần thiếu); phiên kênh khác → `404` | Upload 🔧 |
| POST | `/v1/studio/uploads/{id}/complete` | Creator | Ghép phần → VOD `PROCESSING` + `upload.completed.v1` → `202 {vodId, upload}`; gọi lại khi đã `COMPLETED` trả cùng `vodId`; `409 PARTS_MISSING` (`details.missing: [n…]`), `410 UPLOAD_EXPIRED`, `403 CHANNEL_SUSPENDED`, `503 STORAGE_UNAVAILABLE` | Upload 🔧 |
| DELETE | `/v1/studio/uploads/{id}` | Creator | Hủy phiên → `204` (đã `ABORTED` / `EXPIRED` cũng `204`); `COMPLETED` → `409 INVALID_STATE` | Upload 🔧 |
| GET | `/metrics` (không có tiền tố `/v1`, không qua cổng vào) | — | Prometheus: `stream_live_streams`, `stream_transitions_total{to}`, `stream_authorize_total{result}`, `http_requests_total`, `http_request_duration_seconds` | ✅ L4 |

Auth `Creator` = Bearer của tài khoản đã có kênh; chưa có kênh → `403 NOT_CREATOR` (GĐ1 🔧). Đặc tả nghiệp vụ: NXL-G1.

Mã lỗi: `VALIDATION_FAILED`, `UNAUTHORIZED`, `NOT_CREATOR`, `NOT_FOUND`, `NOT_LIVE`, `INTERNAL_ERROR`; tải video lên: `CHANNEL_SUSPENDED`, `INVALID_STATE`, `FILE_TOO_LARGE`, `UNSUPPORTED_FORMAT`, `UPLOAD_LIMIT_REACHED`, `UPLOAD_EXPIRED`, `PARTS_MISSING`, `INVALID_PART`, `STORAGE_UNAVAILABLE`. `error.details` thường là chuỗi; các mã tải lên trả số / mảng (`maxBytes`, `expectedBytes`, `retryAfterSec`, `missing`).

Tải video lên (TSD `docs/plans/technical/2026-11-02-video-upload-abr.md`): `STREAM_UPLOAD_ENABLED=false` → mọi route `/v1/studio/uploads*` trả `503 STORAGE_UNAVAILABLE` (không cần S3). Body tối đa của stream-svc 9 MiB, read timeout 60 s. File gốc nằm ở bucket riêng tư `nxl-uploads` (`sources/{uploadId}`), xóa khi video `READY`; HLS ở `nxl-media/videos/{vodId}/`.

### 5.2 Nội bộ (media-svc, safety-svc gọi)

| Method | Path | Mô tả | Trạng thái |
| --- | --- | --- | --- |
| POST | `/internal/v1/ingest/authorize` | `{streamKey, clientIp}` → `200 {streamId, channelId, resumed}` · `403 INVALID_STREAM_KEY` · `409 ALREADY_LIVE` · `409 RECENTLY_ENDED` (Creator vừa kết thúc, trong `STREAM_END_COOLDOWN`) · `401 UNAUTHORIZED` (sai secret) | ✅ |
| GET | `/internal/v1/vods/{id}` | safety-svc tra VOD chưa có trong bản sao → `200 {"data": {id, channelId, streamId\|null, source, title, status, visibility}}` (kể cả `DELETED`) · `404 NOT_FOUND` | Upload 🔧 |

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
| HTTP | `GET :8091/metrics` | Prometheus: `media_active_streams`, `media_ffmpeg_speed{stream}`, `media_dropped_frames_total{stream}`, `media_upload_seconds`, `media_upload_errors_total`, `media_publish_rejected_total{reason}`; GĐ4 `media_vod_objects_deleted_total` | ✅ L4 |

Object trong MinIO:

- Đường dẫn: `nxl-media/streams/{streamId}/…` (live, VOD live) và `nxl-media/videos/{vodId}/…` (video tải lên: playlist `VOD`, segment 4 s, không lifecycle). File gốc tải lên: bucket riêng tư `nxl-uploads/sources/{uploadId}` (stream-svc). Trình duyệt truy cập qua cổng vào `/hls/...` (hoặc Vite proxy `/hls` → MinIO khi không chạy cổng vào).
- Cache-Control theo TSD §5.4.
- Đọc công khai ở dev; URL có chữ ký ở L4.

## 6A. channel-svc — kênh, follow, danh mục, tìm kiếm, lịch phát (GĐ1 · GĐ4 🔧)

Cổng 8094, Swagger `/swagger/index.html`, `/healthz`, `/metrics` (thêm `channel_job_failures_total{job}`). Hợp đồng đầy
đủ: TSD `docs/plans/technical/2026-10-14-gd04-channel-discovery.md` §5. Auth: `—` không cần · `O` token tùy chọn ·
`B` Bearer · `C` Bearer của Creator (không có kênh → `403 NOT_CREATOR`).

| Method | Path | Auth | Mô tả |
| --- | --- | --- | --- |
| GET | `/v1/channels/{handle}` | O | `ChannelProfile`: hồ sơ đủ trường, `currentStream` khi live, `viewer {following, notifyLevel, isOwner}` (`null` khi Guest); `404`, `403 CHANNEL_SUSPENDED` (GĐ5: `details {handle, displayName, avatarUrl?}` để trang tạm ngưng hiện tên + ảnh) |
| POST · PUT · DELETE | `/v1/channels/{id}/follow` | B | Follow (lặp → như cũ), đổi chuông `{notifyLevel: ALL\|LIVE_ONLY\|OFF}` (chưa follow → `404`), bỏ follow (`204`); kênh của mình → `422 CANNOT_FOLLOW_SELF` |
| GET | `/v1/channels/{id}/schedule?from=&to=` | O | Lần phát trong `[from, to)` (mặc định 14 ngày, ≤ 31 ngày, ≤ now + 90 ngày); `streamId` khi đã có dòng `SCHEDULED`; `reminded` (`null` khi Guest) |
| GET | `/v1/me/following?status=all\|live\|offline&cursor=&limit=` · `/v1/me/following/sidebar?limit=` | B | Kênh đang theo dõi; sidebar: live trước (theo người xem), lấp bằng offline, `totalFollowing` |
| GET | `/v1/categories?group=&sort=position\|viewers` · `/v1/categories/{slug}` | — | 24 danh mục seed, `viewerCount` = tổng người xem live (`null` khi không đọc được Redis), `posterUrl` `/media/categories/<slug>.jpg` |
| GET | `/v1/search/suggest?q=&types=&limit=` · `/v1/search?q=&type=&cursor=&limit=` | — | Có / không dấu, tiền tố; nhóm `channel → stream → category → vod`; `q = LENGTH` (2–100), `types`/`type = ENUM` |
| GET · POST · DELETE | `/v1/me/search-history[/{id}]` | B | ≤ 10, trùng không phân biệt hoa thường → lên đầu, cùng `id` |
| POST · DELETE | `/v1/schedules/{id}/reminders` | B | `{occurrenceAt}` → `201` (đã có → `200`); lần đã qua / lịch hủy → `409 INVALID_STATE`; không phải lần phát → `404`. DELETE `?occurrenceAt=` → `204` |
| GET · PATCH | `/v1/studio/channel` | C | `StudioChannel` (+ `handleChangedAt`, `nextHandleChangeAt`); PATCH `{displayName?, bio?\|null, socialLinks?, matureDefault?, handle?}`: `HANDLE_TAKEN`, đổi lần hai trong 30 ngày → `409 CONFLICT` + `details.nextChangeAt` |
| PUT | `/v1/studio/channel/avatar` · `/banner` | C | multipart `file` (JPEG/PNG/WebP kiểm theo nội dung) ≤ 2 MB / ≤ 5 MB → JPEG 256² / 1600×500 → `{url}`; `413 PAYLOAD_TOO_LARGE`, `file = REQUIRED\|FORMAT\|MIN` |
| GET | `/v1/studio/channel/stats?range=7d\|28d` | C | `{range, followerCount, newFollowers, upcoming}` |
| GET · POST | `/v1/studio/schedules[?status=ACTIVE\|CANCELLED]` | C | Lịch một lần hoặc `FREQ=WEEKLY;BYDAY=…[;COUNT\|UNTIL]`, `timezone` IANA; ≤ 20 lịch hoạt động (`409 CONFLICT details.limit`), ≤ 90 ngày, `durationMin` 15–720 |
| PUT · DELETE | `/v1/studio/schedules/{id}` | C | Sửa (đổi giờ / quy tắc → hủy các lần đã báo) / hủy cả lịch; lịch đã hủy → `409 INVALID_STATE` |
| PUT · DELETE | `/v1/studio/schedules/{id}/occurrences/{occurrenceAt}` | C | Dời (→ lịch một lần mới, `parentId`) / hủy một lần phát; lần đã qua hoặc đang live → `409 INVALID_STATE` |

Sự kiện: nghe `creator.approved`, `stream.started/updated/ended`, `stream.scheduled`, `stream.vod_ready`, `vod.updated`,
`vod.deleted`; phát `channel.created/updated`, `follow.created/updated/deleted`, `schedule.*` (outbox). Job: báo lần
phát ≤ 24 h (`CHANNEL_SCHEDULE_TICK`), nhắc lịch 10 phút trước, số người xem vào bảng tìm kiếm (30 s), ảnh danh mục khi
khởi động.

## 6B. chat-svc — realtime gateway và chat (GĐ3 🔧)

Hợp đồng đầy đủ (khung WebSocket, snapshot, sự kiện, khóa Redis): TSD `docs/plans/technical/2026-10-14-gd03-chat-realtime.md` §5.

| Method | Path | Auth | Mô tả | Trạng thái |
| --- | --- | --- | --- | --- |
| WS | `/v1/ws` | Tin đầu `{"op":"auth","token":<JWT>\|null,"anonId"?}` trong 5 s | Một kết nối cho mọi chủ đề: `chat:{streamId}`, `stream:{streamId}`, `user:{userId}` (chỉ khung), GĐ4 `sidebar:{userId}` (chỉ chính user: `channel_live`, `channel_offline`, `viewer_count_batch` mỗi 5 s không có `seq`; `stream:` thêm `status_changed` `CANCELLED` / `SCHEDULED` + `scheduledStartAt`). `subscribe` kèm `lastSeq` để resume; quá bộ đệm → `resync`. Mã đóng `1012` (instance tắt), `4001`, `4008`, `4029` (NXL-00 §10) | GĐ3 🔧 |
| GET | `/v1/chat/rooms/{streamId}/messages?before=&limit=` | — | Lịch sử, mới nhất trước: `{items: Message[], nextCursor}`; `Message` cùng dạng `data` của event `message` | GĐ3 🔧 |
| GET | `/v1/chat/emotes?channelId=` | — | `{items: [{id, code, imageUrl, animated, scope (GLOBAL\|CHANNEL), channelId \| null, tierRequired (1–3) \| null}]}`: có `channelId` hợp lệ → emote kênh trước (theo `tierRequired`, `code`) rồi emote toàn cầu; kênh không có emote / không biết / `channelId` sai dạng → chỉ toàn cầu. Emote kênh **khóa** với người có `me.emoteTier < tierRequired` (gửi đi thành chữ, không lỗi). `Cache-Control: public, max-age=60` | GĐ3 🔧 · GĐ6 |
| GET | `/v1/channels/{channelId}/chat-settings` | — | `{slowSec, followersMin, subsOnly, updatedAt}`; kênh chưa biết → `404` | GĐ3 🔧 |
| PUT | `/v1/channels/{channelId}/chat-settings` | Bearer (chủ kênh, Mod, Admin) | Đổi chế độ; mọi phòng đang mở của kênh nhận `mode_change` + `system`. `VALIDATION_FAILED` (`ENUM`), `FORBIDDEN`, `UNAUTHORIZED` | GĐ3 🔧 |
| DELETE | `/v1/chat/rooms/{streamId}/messages/{messageId}` | Bearer (chủ kênh, Mod, Admin) | Xóa tin → `chat:` nhận `message_deleted`; `FORBIDDEN` (`details.target = PROTECTED`) | GĐ5 🔧 |
| PUT · DELETE | `/v1/chat/rooms/{streamId}/pin` | Bearer (mod) | Ghim `{messageId}` / bỏ ghim; tin `SYSTEM` / đã xóa → `409 INVALID_STATE`; phòng `READ_ONLY` → `409 CHAT_READ_ONLY` | GĐ5 🔧 |
| PUT · DELETE | `/v1/channels/{channelId}/timeouts/{userId}` · `/bans/{userId}` | Bearer (mod) | `{durationSec, reason?}` / `{reason?}`; chính mình → `userId = INVALID`; chủ kênh / Mod (người làm không phải Admin) → `403 FORBIDDEN details.target = PROTECTED` | GĐ5 🔧 |
| GET | `/v1/channels/{channelId}/restrictions?type=&cursor=&limit=` | Bearer (mod) | `{items: Restriction[], nextCursor}` | GĐ5 🔧 |
| GET | `/v1/channels/{channelId}/users/{userId}/card` | — / Bearer | `UserCard` (`restriction` chỉ cho người có quyền mod; `createdAt` = thời điểm đăng ký, từ `Nxl-Occurred-At` của `user.registered.v1`) | GĐ5 🔧 |
| GET · POST · DELETE | `/v1/channels/{channelId}/blocked-terms[/{termId}]` | Bearer (mod) | ≤ 100 từ (`409 CONFLICT details.limit`); tin vi phạm → frame `error` `CHAT_CONTENT_BLOCKED details.reason = TERM\|LINK` (Mod miễn trừ) | GĐ5 🔧 |
| GET · POST · PUT · DELETE | `/v1/channels/{channelId}/roles[/{userId}]` | Bearer (chủ kênh; Admin) | Gán / gỡ `MODERATOR` / `VIP` (≤ 50 mỗi vai trò) | GĐ5 🔧 |
| GET | `/v1/me/channel-roles` | Bearer | `{items: ModChannel[]}` — kênh mình là Mod / VIP | GĐ5 🔧 |
| GET | `/metrics` (không qua cổng vào) | — | `chat_ws_connections`, `chat_ws_subscriptions`, `chat_messages_sent_total`, `chat_events_delivered_total`, `chat_fanout_latency_seconds`, `chat_send_rejects_total{code}`, `chat_ws_resyncs_total`, `http_*` | GĐ3 🔧 |

**GĐ6 — quyền lợi Sub** (TSD `docs/plans/technical/2026-10-16-gd06-billing-subscriptions.md` §5.6, §6.11):

- Snapshot `chat:` `me` thêm `subTier` (0–3, đã xét hạn) và `emoteTier` (0–3: chủ kênh 3; Mod / VIP / Admin `max(subTier, 1)`;
  còn lại `subTier`). Badge `SUB_T1..3` trong `me.badges` và `user.badges` của tin (tối đa 3 badge, sau STREAMER / MODERATOR / VIP).
- Subs-only: qua khi `subTier > 0` (sub còn hạn) hoặc chủ kênh / Mod / Admin; sub hết hạn → `CHAT_SUBS_ONLY` ngay, không chờ sự kiện.
- `chat:` `system` thêm `data.type` `SUB_NEW` / `SUB_RENEW`: `{type, text: "@an vừa subscribe Tier 1" | "@an đã gia hạn Tier 1", meta:
  {userId, username, tier}}` — một lần mỗi sự kiện, vào mọi phòng `OPEN` của kênh; không có khi `republish-entitlements`
  (`replay`), không có cho kênh không phát; không lưu vào lịch sử.
- `user:{userId}` thêm `payment_updated`: `{orderId, invoiceNumber, status (PAID|FAILED|CANCELLED|EXPIRED|VOIDED), failureReason |
  null, needsReview, subscription: {id, channelId, tier, status, currentPeriodEnd} | null}` (chỉ khi người mua đang có kết nối;
  đơn `needsReview` → `subscription = null`).
- Bản sao Redis `sub:{channelId}` field `userId` = `"{tier}:{untilUnix}"` (consumer `subscription.activated|renewed|updated` ghi đè,
  `expired` xóa bằng Lua chỉ khi `until ≤ currentPeriodEnd`); giá trị cũ `"{tier}"` vẫn đọc được (không hạn).
- CLI: `chat emote add --channel <uuid> --tier 1-3 --code <code> --image </emotes/channel/x.webp|/media/...> [--animated]`,
  `chat emote remove --channel --code`, `chat emote seed-demo --channel <uuid>` (5 emote `kaelenGG`, `kaelenHi` T1, `kaelenHype`,
  `kaelenClutch` T2, `kaelenGOAT` T3; ảnh `deploy/emotes/channel/*.webp`, cổng vào phục vụ ở `/emotes/channel/`). `chat dev-grant
  sub --tier N [--minutes M]` ghi `"{N}:{now + M phút}"` (M = 0 → 30 ngày).

Mã lỗi (REST và frame `error`): `VALIDATION_FAILED`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `RATE_LIMITED`, `EMAIL_NOT_VERIFIED`, `CHAT_CLOSED`, `CHAT_READ_ONLY`, `CHAT_DISABLED`, `CHAT_DUPLICATE`, `CHAT_SLOW_MODE`, `CHAT_FOLLOWERS_ONLY`, `CHAT_SUBS_ONLY`, `CHAT_TIMED_OUT`, `CHAT_BANNED`, `INTERNAL_ERROR`. `details` là chuỗi (`retryAfterSec: "5"`).

**Ngoài REST:** `chat dev-grant follow|sub|mod|vip|owner --channel <id> --user <id> [--minutes N] [--tier N] [--revoke]` (`make nxl-chat-dev-grant`, chỉ `NXL_ENV=dev`) ghi dữ liệu demo cho Followers-only / Subs-only / Mod / VIP.

## 6C. safety-svc — báo cáo, quản trị, audit log (GĐ5 🔧)

Cổng 8099, Swagger `/swagger/index.html`, `/healthz`, `/metrics` (`safety_reports_total{target_type,reason}`). Hợp đồng
đầy đủ: TSD `docs/plans/technical/2026-10-15-gd05-safety-notifications.md` §5.4. Auth: `B` Bearer · `A` Bearer có
`role = ADMIN` (khác → `403 FORBIDDEN`). Thành công bọc trong `{data}` như các service khác.

| Method | Path | Auth | Mô tả |
| --- | --- | --- | --- |
| POST | `/v1/reports` | B | `{targetType: USER\|CHANNEL\|STREAM\|MESSAGE\|VOD, targetId, reason, note?, streamId?}` → `201 {id, status, createdAt}`; report mở trùng → `200` cùng `id`; > 10/giờ → `429 RATE_LIMITED details.retryAfterSec` + `Retry-After`; chính mình → `targetId = INVALID`; `MESSAGE` cần `streamId`, bằng chứng ≤ 10 tin (chat chậm > 300 ms → `evidenceStatus = UNAVAILABLE`); `VOD` (`targetId` = vodId, bản ghi live hoặc video tải lên): không có / đã xóa → `404 NOT_FOUND`, người bị báo cáo là chủ kênh, bản sao chưa có thì hỏi `GET /internal/v1/vods/{id}` của stream-svc (≤ 300 ms) |
| GET | `/v1/admin/reports?status=&targetType=&reason=&cursor=&limit=` · `/open-count` · `/{id}` | A | Hàng đợi cũ nhất trước (`status` CSV, mặc định `OPEN,IN_REVIEW`); `{open, inReview}`; `AdminReportDetail` (bằng chứng có `username` / `displayName` của người viết, `channelId` của kênh / buổi phát / VOD / phòng, report liên quan ≤ 20, hành động ≤ 20 gồm cả `CHAT_TIMEOUT` / `CHAT_UNTIMEOUT` / `CHAT_BAN` / `CHAT_UNBAN` của người bị báo cáo từ audit log, kèm `actorRole`, `channelId`); `sameTargetCount` đếm cả chính report |
| POST | `/v1/admin/reports/{id}/claim` · `/resolve` | A | Nhận (`IN_REVIEW`, nhận lại được); `{status: RESOLVED\|DISMISSED, note 10–500, applyToSameTarget?}` → `{report, closedCount}`; đã đóng → `409 INVALID_STATE` |
| POST | `/v1/admin/users/{id}/lock` · `/unlock` | A | `{durationHours: 24\|168\|720\|null, reason 10–500, reportId?}` → `202 {actionId}`; identity áp dụng (≤ 2 s, socket đóng `4003`); Admin khác → `403`, chính mình → `userId = INVALID` |
| GET | `/v1/admin/users?q=&status=` · `/v1/admin/channels?q=&status=` · `/v1/admin/channels/{id}` | A | Tra cứu trên bản sao (UUID → id; còn lại tiền tố username / handle, tên kênh chứa `q`) |
| POST | `/v1/admin/channels/{id}/suspend` · `/restore` | A | `{reason, reportId?}` → `202`; channel-svc khóa kênh, stream-svc kết thúc buổi phát + hủy lịch, chat đóng phòng |
| POST | `/v1/admin/streams/{id}/takedown` | A | `202`; đang phát → kết thúc `admin_takedown`; đã kết thúc còn VOD → VOD bị xóa; `SCHEDULED` / `CANCELLED` / hết VOD → `409 INVALID_STATE` |
| POST | `/v1/admin/vods/{id}/takedown` | A | `{reason 10–500, reportId?}` → `202 {actionId}` (`TAKEDOWN_VOD`, audit `VOD_TAKEDOWN`, outbox `safety.vod_takedown.v1`); stream-svc xóa VOD (bản ghi live hoặc video tải lên); không có → `404`; đã xóa → `409 INVALID_STATE` |
| GET | `/v1/admin/audit-log?actorId=&actor=&action=&resourceType=&resourceId=&from=&to=&cursor=&limit=` | A | Mới nhất trước; chỉ thêm (trigger chặn sửa / xóa); `actor` = username (bỏ `@`, không phân biệt hoa thường) khi không có `actorId`, không có người đó → trang rỗng |

Sự kiện: nghe `user.*`, `channel.*`, `stream.*`, `vod.published`, `vod.updated`, `vod.deleted` (bản sao), `chat.mod_action`,
`chat.settings_changed`, `channel.role_changed`, `audit.recorded` (audit); phát `safety.user_actioned`, `safety.channel_actioned`,
`safety.stream_takedown`, `safety.vod_takedown`, `safety.report_resolved` (outbox).

## 6D. notification-svc — trung tâm thông báo, cài đặt, email (GĐ5 🔧)

Cổng 8097, Swagger `/swagger/index.html`, `/healthz`, `/metrics` (`notification_created_total{type,grouped}`,
`notification_suppressed_total{type,reason}`, `notification_emails_total{template,status}`,
`notification_email_send_seconds`, `notification_reads_total{action}`, `notification_event_lag_seconds{source}`). Hợp đồng
đầy đủ: TSD GĐ5 §5.5, §5.8, §6.7–§6.8; câu chữ in-app dựng ở web theo `type` + `payload` (NXL-E §7.4).

| Method | Path | Auth | Mô tả |
| --- | --- | --- | --- |
| GET | `/v1/notifications?unread=&cursor=&limit=` | B | `{items: Notification[], nextCursor}` — `updatedAt` mới nhất trước (mục gộp lên đầu); `limit` 1–50 (20); `unread = FORMAT`, `cursor = INVALID` |
| GET | `/v1/notifications/unread-count` | B | `{count}` |
| POST | `/v1/notifications/{id}/read` | B | `204`; đã đọc → `204`; của người khác / đã xóa / không có → `404` |
| POST | `/v1/notifications/read-all` | B | `{before?}` (RFC 3339, mặc định `now`; chỉ mục `updatedAt ≤ before`) → `204`; `before = FORMAT` |
| DELETE | `/v1/notifications/{id}` | B | Xóa mềm → `204` (đã xóa → `204`); của người khác → `404` |
| GET · PUT | `/v1/me/notification-settings` | B | `{items: [{type, inApp, email, inAppLocked, emailLocked, emailAvailable}] (9 loại, thứ tự BR-E01, GĐ6 thêm `SUBSCRIPTION` ở cuối ✓/✓ không khóa), emailVerified}`; `PUT {items: [{type, inApp?, email?}]}` (1–9 dòng, chỉ dòng đổi) → bảng đầy đủ; tắt `SYSTEM` / `CREATOR_APPLICATION` hoặc bật email `NEW_FOLLOWER` → `400 details.<TYPE> = INVALID`; `type = ENUM`, `items = REQUIRED` |
| POST | `/v1/notifications/unsubscribe` | — | `{token}` (từ liên kết `{WEB}/notifications/unsubscribe?token=…` trong email) → `{type}`; sai / sửa / loại không có email → `400 TOKEN_INVALID`; thiếu → `token = REQUIRED` |

`Notification` = `{id, type, payload, link | null, groupCount, read, createdAt, updatedAt}`; `payload` theo loại:
`CREATOR_LIVE {channel{id,handle,displayName,avatarUrl}, streamId, title}` · `STREAM_STARTING {channel, scheduleId,
occurrenceAt, title}` · `SCHEDULE {channel, scheduleId, title, occurrenceAt, newOccurrenceAt, reason
(CANCELLED|MOVED|SERIES_CHANGED|CREATED)}` · `MENTION {channel, streamId, lastSender{id,username,displayName}, excerpt}` ·
`NEW_FOLLOWER {channelId, lastFollower{id,username,displayName}}` (link `/studio`) · `CREATOR_APPLICATION {applicationId, result, reason}` ·
`REPORT_RESULT {reportId, targetType, result (RESOLVED|DISMISSED)}` · `SYSTEM {kind (CHANNEL_SUSPENDED|CHANNEL_RESTORED|
STREAM_TAKEDOWN), channelId, reason, streamId?, title?}`.

GĐ6 — `SUBSCRIPTION` (`payload.kind`; `channel` = `{id, handle, displayName, avatarUrl}` từ bản sao kênh; TSD GĐ6 §6.12):

| `kind` | Nguồn | Người nhận | `payload` | `link` | Email |
| --- | --- | --- | --- | --- | --- |
| `PAYMENT_SUCCEEDED` · `PAYMENT_CONFIRMED_LATE` (`late`) | `payment.succeeded` | người mua | `{kind, orderId, invoiceNumber, channel, tier, subAction (NEW\|RENEW), applied, amount, currentPeriodEnd}` | `/{handle}` | `payment_receipt` (bắt buộc) |
| `PAYMENT_UNDER_REVIEW` (`applied = NEEDS_REVIEW`) | `payment.succeeded` | người mua | như trên, `currentPeriodEnd = null` | `/settings/payments` | `payment_receipt` (bắt buộc, "đang được xem xét") |
| `PAYMENT_FAILED` | `payment.failed` | người mua | `{kind, orderId, invoiceNumber, channel, tier, status, failureReason}` | `/settings/payments` | — |
| `PAYMENT_VOIDED` | `payment.voided` | người mua | `{kind, orderId, invoiceNumber, channel, tier, amount, subscriptionStatus, currentPeriodEnd}` | `/settings/payments` | `payment_voided` (bắt buộc) |
| `SUB_EXPIRING` | `subscription.expiring` | người mua | `{kind, subscriptionId, channel, tier, currentPeriodEnd, price, renewable}` | `/{handle}?subscribe=renew` | `subscription_expiring` (theo cài đặt, có link hủy nhận) |
| `SUB_EXPIRED` | `subscription.expired` | người mua | `{kind, subscriptionId, channel, tier, reason (PERIOD_ENDED\|VOIDED)}` | `/{handle}` | — |
| `NEW_SUBSCRIBER` | `subscription.activated` (không `replay`) | chủ kênh | `{kind, channelId, lastSubscriber {id, username, displayName}, tier}`; gộp theo kênh 60 phút (`groupCount`) | `/studio/monetization` | — |

Email bắt buộc (`payment_receipt`, `payment_voided`) bỏ qua công tắc email và trần 10 / ngày, không có link hủy nhận, nhưng vẫn
chỉ gửi tới địa chỉ đã xác minh. Tiền hiển thị `59.000 ₫` (vi) / `59,000 ₫` (en). In-app `SUBSCRIPTION` theo công tắc `inApp`.

Realtime: chat-svc chuyển `notification.created.v1` thành frame `notification` (`data` = `Notification`, client upsert theo
`id`) rồi `unread_count {count}` trên `user:{userId}`; `notification.unread_changed.v1` → `unread_count`. Chỉ người đang có
kết nối nhận; người offline lấy qua REST.

Quy tắc: cooldown `CREATOR_LIVE` 30 phút in-app / 12 giờ email mỗi (người nhận, kênh) (Redis `notify:live:*`); gộp
`MENTION` theo buổi phát 5 phút, `NEW_FOLLOWER` theo kênh 60 phút; ≤ 10 email không bắt buộc / ngày theo múi giờ tài
khoản; chỉ email tới địa chỉ đã xác minh (trừ `account_locked` và email xác thực); fan-out lô 500, unique
`(user_id, source_event_id)`; email qua `email_outbox` + worker (thử lại 1 · 5 · 15 · 60 phút, tối đa 5 lần); lưu 90 ngày.

**Email xác thực (chuyển mailer, TSD §9.2):** consumer `notification-auth-mailer` (DeliverNew) gửi `verify_email`,
`reset_password`, `security_lock` ngay trong handler (không lưu outbox, không log token). Thứ tự chuyển: bật
`NOTIFICATION_AUTH_MAILER_ENABLED=true` + khởi động lại notification **trước**, rồi `IDENTITY_MAILER_ENABLED=false` +
khởi động lại identity (identity xóa durable `identity-mailer`) — `make nxl-mailer-cutover`; vài giây giữa hai bước có
thể gửi trùng, không mất. Rollback: `make nxl-mailer-rollback` (identity bật lại trước, durable tạo lại DeliverNew). Compose
và `.env.example` đã ở trạng thái sau chuyển giao; mặc định trong code: identity `true`, notification `false`.

## 6E. billing-svc — tier, checkout, IPN SePay, subscription, Thu nhập, rút tiền (GĐ6 🔧)

Cổng 8098, Swagger `/swagger/index.html`, `/healthz`, `/metrics` (`billing_ipn_total{type,result}`,
`billing_ipn_duration_seconds`, `billing_checkout_orders_total{status,method,reason}`, `billing_provider_requests_total{op,result}`,
`billing_provider_duration_seconds{op}`, `billing_reconcile_runs_total{job,result}`, `billing_reconcile_mismatches_total{kind}`,
`billing_orders_needs_review`, `billing_subscriptions_active{tier}`, `billing_voids_total`, `billing_payouts_total{status}`,
`billing_idempotency_total{result}`). Hợp đồng đầy đủ (DTO, ví dụ JSON, state machine): TSD
`docs/plans/technical/2026-10-16-gd06-billing-subscriptions.md` §5. Auth: `—` không cần · `B` Bearer · `C` Bearer, kênh của
người gọi (không có → `403 NOT_CREATOR`) · `A` Bearer `role = ADMIN` (khác → `403 FORBIDDEN`) · `IK` bắt buộc `Idempotency-Key`
(§3.6). Phân trang `limit` 1–50 (20), `cursor` mờ, `nextCursor`. Tiền: `Money {amount: int64, currency: "VND"}`.

| Method | Path | Auth | Mô tả |
| --- | --- | --- | --- |
| GET | `/v1/channels/{id}/subscription-tiers` | — | `{items: Tier[], nextCursor: null}` — tier đang bật theo level; kênh không có → `404`, bị khóa → `403 CHANNEL_SUSPENDED` |
| GET | `/v1/checkout/config` | — | `{provider: SEPAY\|FAKE, paymentMethods, periodDays: 30, renewReminderDays: 3, maxPrepaidDays: 365, orderTtlSec: 1800, maxPending: 5}` |
| GET | `/v1/checkout/tiers/{tierId}` | — | `{tierId, channel: ChannelRef + ownerId + status, tiers}` cho `/checkout?tierId=` (tier tắt vẫn trả) |
| POST | `/v1/checkout/orders` | B, IK | `{tierId, paymentMethod?}` → `201 {order, checkout: {actionUrl, method: POST, fields[]}}` (form ký HMAC, POST đúng thứ tự `fields`); đơn chờ cùng tier → `200` đơn đó; `422 CANNOT_SUBSCRIBE_SELF`; `409 INVALID_STATE details.reason` `TIER_INACTIVE` / `TIER_CHANGE` / `MAX_PREPAID` / `ORDER_PENDING`; `409 CONFLICT details.limit = 5` |
| GET | `/v1/checkout/orders/{id}` | B | `{order, subscription \| null}`; của người khác → `404` |
| POST | `/v1/checkout/orders/{id}/sync` | B | `{outcome: SUCCESS\|ERROR\|CANCEL}` — hỏi SePay: đã trả → `PAID`; chưa trả: `CANCEL` → `CANCELLED`, `ERROR` → `FAILED` (`DECLINED` / `PROVIDER_ERROR` / `UNKNOWN`), `SUCCESS` → giữ `PENDING`; > 10 / phút → `429` |
| POST | `/v1/checkout/orders/{id}/cancel` | B | Chỉ đơn `PENDING` (khác → `409`) |
| GET | `/v1/me/subscriptions?status=current\|expired&channelId=` | B | Có `channelId`, không `status` → ≤ 1 dòng (đang có, không thì hết hạn gần nhất) |
| POST | `/v1/me/subscriptions/{id}/cancel` · `/resume` | B (+ IK cho cancel) | Tắt / bật nhắc gia hạn (`ACTIVE` ↔ `CANCELLED_PENDING_END`), sai trạng thái → `409` |
| GET | `/v1/me/payments?status=` | B | Đơn của người gọi, mới nhất trước (`status` CSV) |
| GET · PATCH | `/v1/studio/monetization/tiers` · `/tiers/{level}` | C | Mọi tier + `activeSubscribers`; `PATCH {active?, description?}` (Tier 1 không tắt được → `active = INVALID`; mô tả ≤ 200) |
| GET | `/v1/studio/monetization/summary?range=7d\|28d&tz=` | C | `gross, platformFee (5 %, làm tròn xuống), net, todayNet, balance, available, pendingPayout, subscribers{tier1..3,total}, feeBps, payoutMin` |
| GET | `/v1/studio/monetization/ledger` | C | Sổ cái chỉ-thêm: `SUB_REVENUE` / `PLATFORM_FEE` / `PAYOUT`, `source` `CHARGE` / `VOID` / `PAYOUT` |
| GET · POST | `/v1/studio/monetization/payouts` | C (+ IK cho POST) | `{amount, bankCode, accountNumber, accountName}` → `201 Payout` (`code PO-0001`, số tài khoản mã hóa AES-256-GCM, chỉ trả `last4`); `MIN` 100.000 / `MAX`; `409 INSUFFICIENT_BALANCE details.available` / `PAYOUT_PENDING` |
| GET | `/v1/admin/payouts?status=` · `/pending-count` · `/{id}` | A | Hàng đợi (`REQUESTED` cũ trước; `APPROVED` / `REJECTED` / `ALL` mới trước), kèm số dư kênh |
| GET | `/v1/admin/payouts/{id}/bank-account` | A | Số tài khoản đầy đủ, `Cache-Control: no-store`, mỗi lần xem ghi audit `PAYOUT_BANK_INFO_VIEWED` |
| POST | `/v1/admin/payouts/{id}/approve` · `/reject` | A | `APPROVED` (ghi `DEBIT PAYOUT`, không chuyển tiền thật) / `REJECTED {reason 10–500}`; đã duyệt → `409` |
| GET | `/v1/admin/checkout-orders?needsReview=&status=&failureReason=&q=` | A | Đơn (mới nhất trước); `status`, `failureReason` là CSV (AND); `q` không phân biệt hoa thường: đủ mã hóa đơn (`NXL` + 10 ký tự) → khớp chính xác, ngắn hơn → tiền tố |
| GET | `/v1/admin/checkout-orders/{id}` | A | Một đơn bất kỳ theo id hoặc mã hóa đơn (không phân biệt hoa thường) → `AdminOrder`; không có → `404 NOT_FOUND` |
| POST | `/v1/webhooks/sepay` | `X-Secret-Key` | IPN SePay (không envelope): `200 {"success": true}` (đã áp, trùng, bỏ qua, chưa duyệt, hóa đơn lạ, lệch tiền → đơn `FAILED AMOUNT_MISMATCH`), `401` sai secret, `400` hỏng, `500` lỗi tạm |

Dev (⛔, `BILLING_PAYMENT_PROVIDER=fake`, `NXL_ENV=dev`): `POST http://localhost:8098/dev/fake-psp` nhận form checkout (kiểm chữ ký)
và hiện 3 nút; `/dev/fake-psp/{invoice}/pay` gửi IPN `ORDER_PAID` đúng định dạng SePay tới webhook rồi `303` về
`success_url`; `/cancel`, `/fail` → `303` `cancel_url` / `error_url` (không IPN). CLI cùng binary (`make nxl-billing CMD=…`):
`backfill-channels`, `republish-entitlements [--channel]`, `dev-ipn replay --transaction <id> [--times N]`,
`dev-ipn synth --invoice <NXL…> [--type ORDER_PAID|TRANSACTION_VOID]`, `dev-void --invoice` (chỉ sepay).

Job (khóa Redis, một instance chạy): `reconcile-pending` mỗi `BILLING_RECONCILE_INTERVAL` (đơn chờ > 2 phút: tra cứu → `PAID`;
quá hạn → `EXPIRED`), `subscription-expiry` mỗi phút (nhắc một lần mỗi kỳ khi còn ≤ 3 ngày, hết kỳ → `EXPIRED`),
`reconcile-daily` sau 02:00 giờ VN (đối soát ngày trước), dọn `idempotency_keys` mỗi giờ.

Sự kiện: nghe `channel.created/updated/suspended/restored` (durable `billing-svc`, DeliverAll; bản sao kênh + 3 tier mặc định);
phát qua outbox `subscription.*`, `payment.*` (§8), `audit.recorded` (nguồn billing).

## 7. Dịch vụ dự kiến

Đường dẫn dưới đây là **bản nháp** để giữ chỗ và kiểm tra trùng lặp. Spec tương ứng chốt chi tiết trước khi làm (roadmap §4).

### 7.2 channel-svc

Đã chạy ở GĐ4 (§6A). Không làm: `GET /v1/home` (trang chủ gọi API theo từng khối, G4-Q3), `GET /v1/search/trending`,
`GET /v1/channels/recommended` (sidebar Guest dùng `GET /v1/streams/recommended`), follow danh mục.

### 7.5 billing-svc

Đã chạy ở GĐ6 (§6E); bản nháp cũ (`/v1/channels/{id}/tiers`, `/v1/checkout`, `/v1/me/transactions`, `/v1/studio/balance`) thay
bằng đường dẫn của TSD GĐ6 §5.2.

## 8. Sự kiện (NATS JetStream)

**Cách truyền (hiện thực):**

- JetStream stream `NXL`, subject `nxl.<type>` (ví dụ `nxl.stream.ended.v1`), giữ 7 ngày.
- Header `Nats-Msg-Id` là id sự kiện (UUID): JetStream bỏ bản trùng trong 2 phút; bên nhận ghi `processed_events` để idempotent.
- Payload là JSON của chính sự kiện. Hợp đồng ở `pkg/events/events.go`.
- Bên phát có DB đi qua **outbox** (`pkg/outbox`); media-svc không có DB nên publish trực tiếp.

- Header (GĐ1 🔧): `Nxl-Occurred-At` (RFC 3339; outbox: thời điểm ghi dòng outbox) và `Nxl-Producer` (tên service). Errata E-11 đã chốt: metadata ở header, body là payload (NXL-00 §11; kiến trúc §5.2 đã sửa).

| Subject (`nxl.` +) | Bên phát | Bên nhận | Payload chính | Trạng thái |
| --- | --- | --- | --- | --- |
| `user.registered.v1` | identity (outbox) | chat (bản sao `users`, GĐ3); notification (bản sao, GĐ5) | `userId, username, displayName, email` | ✅ phát |
| `auth.email_requested.v1` | identity (outbox) | notification (`notification-auth-mailer`, GĐ5); identity (`identity-mailer`, chỉ khi `IDENTITY_MAILER_ENABLED=true`) | `userId, email, purpose (VERIFY\|RESET\|SECURITY_LOCK), token, locale, username` — token gốc, không log | 🔧 GĐ1 · GĐ5 |
| `user.email_verified.v1` | identity (outbox) | chat (GĐ3) | `userId` | 🔧 GĐ1 |
| `user.profile_updated.v1` | identity (outbox) | chat (GĐ3); GĐ4: channel | `userId, displayName` | 🔧 GĐ1 |
| `creator.application_submitted.v1` | identity (outbox) | — | `applicationId, userId` | 🔧 GĐ1 |
| `creator.approved.v1` | identity (outbox) | channel (tạo kênh) | `applicationId, userId, username, displayName` | 🔧 GĐ1 |
| `creator.rejected.v1` | identity (outbox) | notification (`CREATOR_APPLICATION`, GĐ5) | `applicationId, userId, reason` | 🔧 GĐ1 |
| `channel.created.v1` | channel (outbox) | stream (`creator_channels`), identity (`user_channels`), chat (`channels`: badge Streamer, quyền đổi chế độ, GĐ3) | `channelId, ownerId, handle, displayName` | 🔧 GĐ1 |
| `ingest.started.v1` | media | stream | `streamId, channelId, renditions[], sourceWidth, sourceHeight, resumed` | ✅ |
| `ingest.stopped.v1` | media | stream | `streamId, reason` (`publisher_disconnected` / `transcoder_failed` / `kicked` / `shutdown` / `recovered`) | ✅ |
| `ingest.health.v1` | media (mỗi 5s) | stream (chỉ giữ trong bộ nhớ) | `streamId, fps, bitrateKbps, droppedFrames, speed, uploadErrors` | ✅ |
| `vod.processed.v1` | media | stream | `streamId, durationSec, playlistPath, thumbnailPath` | ✅ |
| `stream.started.v1` | stream (outbox) | channel (`is_live`, `live_streams`, tìm kiếm); chat (mở phòng, `channel_live`) | `streamId, channelId, title`; GĐ4 + `categoryId, tags, language, isMature, startedAt, scheduleId` | ✅ · GĐ4 🔧 |
| `stream.ended.v1` | stream (outbox) | media (kick + chốt VOD), channel; chat (phòng chỉ đọc, `channel_offline`) | `streamId, channelId, endReason` | ✅ |
| `stream.status_changed.v1` | stream (outbox) | chat (đẩy `stream:{id}` `status_changed`) | `streamId, channelId, status (LIVE\|INTERRUPTED\|CANCELLED\|SCHEDULED), occurredAt, scheduledStartAt` | 🔧 GĐ3 · GĐ4 |
| `stream.updated.v1` | stream (outbox, sửa cài đặt khi đang phát) | chat (`title_changed`), channel | `streamId, channelId, title, tags[]`; GĐ4 + `categoryId, language, isMature` | 🔧 GĐ3 · GĐ4 |
| `stream.vod_ready.v1` | stream (outbox, VOD `READY`) | chat (`vod_ready`), channel (tìm kiếm) | `streamId, channelId, vodId`; GĐ4 + `title, thumbnailUrl, durationSec, categoryId, isMature, expiresAt` | 🔧 GĐ3 · GĐ4 |
| `stream.scheduled.v1` | stream (outbox) | channel (`occurrence_streams`) | `streamId, channelId, scheduleId, scheduledStartAt` | 🔧 GĐ4 |
| `vod.updated.v1` · `vod.deleted.v1` | stream (outbox) | channel (tìm kiếm), safety (bản sao); media xóa file khi `deleted` | `vodId, streamId, channelId, title, visibility, status, …` · `vodId, streamId, channelId`; Upload (chỉ thêm): `streamId` omitempty, + `source`; `updated` + `description`, `expiresAt` null với video tải lên; `deleted` + `playlistPrefix` (`streams/{streamId}/` \| `videos/{vodId}/`). Thiếu `source` = `LIVE` | 🔧 GĐ4 · Upload |
| `upload.completed.v1` | stream (outbox; cả khi retry) | media (durable `media-transcode`) | `uploadId, vodId, channelId, bucket, objectKey, sizeBytes` | 🔧 Upload |
| `upload.progress.v1` · `upload.processed.v1` · `upload.failed.v1` | media | stream (không qua `processed_events`: idempotent theo trạng thái VOD) | `vodId, percent, stage` · `vodId, durationSec, playlistPath, thumbnailPath, renditions[], sourceWidth, sourceHeight` · `vodId, reason, retryable` | 🔧 Upload |
| `vod.published.v1` | stream (outbox, video tải lên `READY`) | channel (tìm kiếm), safety (bản sao) | `vodId, channelId, source, title, description, thumbnailUrl, durationSec, categoryId, isMature, visibility, publishedAt` | 🔧 Upload |
| `stream.peak_viewers.v1` | chat (outbox, khi phòng chuyển `READ_ONLY`) | stream (`streams.peak_viewers`) | `streamId, peakViewers` | 🔧 GĐ3 |
| `channel.updated.v1` | channel (outbox) | stream (`creator_channels`), chat (`channels`) | `channelId, ownerId, handle, displayName, avatarUrl` | 🔧 GĐ4 |
| `user.locked.v1` · `user.unlocked.v1` | identity | chat, stream | | 📋 GĐ5 |
| `follow.created.v1` · `follow.updated.v1` · `follow.deleted.v1` | channel (outbox) | chat (bản sao Redis `follow:`, `following:`, `follow:level:`), (GĐ5: notification) | `userId, channelId, createdAt, notifyLevel` · `userId, channelId, notifyLevel, updatedAt` · `userId, channelId` | 🔧 GĐ4 |
| `schedule.created/updated.v1` · `schedule.occurrence_upcoming/changed.v1` · `schedule.reminder_due.v1` | channel (outbox) | stream (dòng `SCHEDULED`), (GĐ5: notification) | TSD GĐ4 §5.5 | 🔧 GĐ4 |
| `chat.message_sent.v1` · `chat.mention.v1` · `chat.mod_action.v1` | chat | notification, safety | | 📋 GĐ3 |
| `safety.stream_takedown.v1` · `safety.vod_takedown.v1` · `safety.user_actioned.v1` · `safety.report_resolved.v1` | safety | stream, identity, notification | | 📋 GĐ5 (`vod_takedown`: video upload) |
| `notification.created.v1` · `notification.unread_changed.v1` | notification (outbox) | chat (đẩy `user:{id}` `notification` + `unread_count`) | TSD GĐ5 §5.9 (`unreadCount` sau thay đổi) | 🔧 GĐ5 |
| `subscription.activated.v1` | billing (outbox) | chat (`sub:` + `SUB_NEW`), notification (`NEW_SUBSCRIBER`) | `subscriptionId, userId, channelId, tier, currentPeriodStart, currentPeriodEnd, orderId \| null, replay` (`replay` = `republish-entitlements`: chỉ dựng lại bản sao) | 🔧 GĐ6 |
| `subscription.renewed.v1` | billing (outbox) | chat | `subscriptionId, userId, channelId, tier, currentPeriodEnd, orderId` | 🔧 GĐ6 |
| `subscription.updated.v1` | billing (outbox) | chat | `subscriptionId, userId, channelId, tier, status, currentPeriodEnd, change (CANCELLED\|RESUMED\|PERIOD_REDUCED)` | 🔧 GĐ6 |
| `subscription.expiring.v1` | billing (outbox) | notification | `subscriptionId, userId, channelId, tier, currentPeriodEnd, price, renewable` — một lần mỗi kỳ | 🔧 GĐ6 |
| `subscription.expired.v1` | billing (outbox) | chat, notification | `subscriptionId, userId, channelId, tier, reason (PERIOD_ENDED\|VOIDED), currentPeriodEnd` | 🔧 GĐ6 |
| `payment.succeeded.v1` | billing (outbox) | chat (`payment_updated`), notification | `orderId, invoiceNumber, userId, channelId, tier, kind, amount, paymentMethod, paidAt, applied (ACTIVATED\|RENEWED\|NEEDS_REVIEW), subscriptionId, currentPeriodEnd, late, source (IPN\|RECONCILE)` | 🔧 GĐ6 |
| `payment.failed.v1` | billing (outbox) | chat, notification | `orderId, invoiceNumber, userId, channelId, tier, status (FAILED\|CANCELLED\|EXPIRED), failureReason` | 🔧 GĐ6 |
| `payment.voided.v1` | billing (outbox) | chat, notification | `orderId, invoiceNumber, userId, channelId, tier, amount, subscriptionId, subscriptionStatus, currentPeriodEnd` | 🔧 GĐ6 |

Consumer bền hiện có:

- `stream-svc`: `ingest.*`, `vod.processed`; GĐ1: `channel.created`.
- `media-svc`: `stream.ended`; GĐ4 `vod.deleted`.
- GĐ4 🔧: `stream-svc` thêm `channel.updated`, `schedule.updated`, `schedule.occurrence_upcoming/changed`; `channel-svc` thêm
  `stream.updated`, `stream.scheduled`, `stream.vod_ready`, `vod.updated`, `vod.deleted`; `chat-svc` thêm
  `channel.updated`, `follow.updated`.
- GĐ1 🔧: `identity-svc` (`channel.created`), `identity-mailer` (`auth.email_requested`), `channel-svc` (`creator.approved`, `stream.started`, `stream.ended`).
- GĐ5 🔧: `notification-replica` (DeliverAll: `user.*`, `channel.*`, `follow.*` → bản sao, kèm backfill một lần qua API
  snapshot nội bộ), `notification-svc` (**DeliverNew**: nguồn thông báo TSD §3.2), `notification-auth-mailer` (**DeliverNew**:
  `auth.email_requested`); `identity-mailer` bị xóa khi `IDENTITY_MAILER_ENABLED=false`, tạo lại với DeliverNew khi bật.

- GĐ6 🔧: `billing-svc` (DeliverAll: `channel.created/updated/suspended/restored`; kênh có trước lấy bằng `billing backfill-channels`);
  `chat-svc` thêm `subscription.activated/renewed/updated/expired`, `payment.succeeded/failed/voided`; `notification-svc` thêm
  `payment.succeeded/failed/voided`, `subscription.expiring/expired/activated`. Triển khai chat mới **trước** khi billing phát
  sự kiện (chat cũ đọc `"{tier}:{until}"` thành không sub).

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
| 2026-10-21 | v1.11 — GĐ6 close-out (chỉ thêm): `GET /v1/admin/checkout-orders/{id}` (id hoặc mã hóa đơn), lọc `failureReason` trên danh sách đơn Admin, `q` khớp chính xác khi là mã hóa đơn đầy đủ (ngắn hơn vẫn là tiền tố) |
| 2026-10-20 | v1.10 — GĐ6 phần B: chat (§6B: emote kênh theo tier trong `GET /v1/chat/emotes?channelId=`, snapshot `me.subTier` / `me.emoteTier`, `system` `SUB_NEW` / `SUB_RENEW`, `user:` `payment_updated`, `sub:` có hạn, CLI `chat emote`); notification (§6D: loại thứ 9 `SUBSCRIPTION`, 8 dạng payload, email `payment_receipt` / `payment_voided` bắt buộc, `subscription_expiring`) |
| 2026-10-20 | v1.9 — GĐ6 phần A: billing-svc (§6E: tier, checkout + `Idempotency-Key`, IPN SePay / trang thanh toán giả, sync, subscription, Thu nhập Studio, rút tiền, Admin), route cổng vào + vùng `nxl_webhook`, 8 sự kiện `subscription.*` / `payment.*` (§8), mã `CANNOT_SUBSCRIBE_SELF`, `IDEMPOTENCY_KEY_REUSED` |
| 2026-10-16 | v1.8 — GĐ5 close-out (chỉ thêm, trừ refresh): refresh của tài khoản bị khóa → `403 ACCOUNT_LOCKED`; `CHANNEL_SUSPENDED` của hồ sơ kèm `details`; `UserCard.createdAt`; `AdminReportDetail.channelId`, `evidence[].username/displayName`, `actions[]` có hạn chế chat + `actorRole`/`channelId`; audit `actor=`; `NEW_FOLLOWER` link `/studio` |
| 2026-10-15 | v1.7 — GĐ5 phần B: notification-svc (§6D: trung tâm thông báo, cài đặt, hủy nhận email, `user:` `notification` / `unread_count`), chuyển email xác thực từ identity (`IDENTITY_MAILER_ENABLED`, `make nxl-mailer-cutover`) |
| 2026-10-15 | v1.6 — GĐ4 backend: channel-svc (follow, hồ sơ + ảnh, danh mục, tìm kiếm, lịch sử tìm kiếm, lịch phát, nhắc lịch, Studio kênh); stream-svc (lọc / sắp xếp danh sách, gợi ý, VOD theo danh mục và kênh, `SCHEDULED`/`CANCELLED`, Dashboard, Studio VOD, VOD ẩn); chat `sidebar:{userId}`; media xóa file VOD; Nginx tách route + `/media/`; sự kiện mới §8 |
| 2026-10-14 | v1.4 — GĐ2 backend: phiên xem + cổng 18+ phía server, tiến độ xem theo tài khoản, continue-watching; Nginx chuyển `/v1/me/(watch-progress|continue-watching)` sang stream-svc, CORS thêm `X-NXL-Anon-Id` |
| 2026-10-13 | v1.3 — GĐ1: toàn bộ endpoint identity của TSD GĐ1 §4.1 (cột Auth), header `X-NXL-Client`, rate limit / CORS / header bảo mật ở cổng vào (§1.1), channel-svc cổng 8094 (§6A, chat-svc dời 8095), stream-svc `NOT_CREATOR` + `channel`, 8 sự kiện mới + header `Nxl-Occurred-At`/`Nxl-Producer`; hai web dev 5173/5174; đơn Creator ở GĐ1 (errata E-14) |
| 2026-10-12 | L4: `409 RECENTLY_ENDED` ở authorize nội bộ; `/metrics` của stream-svc và media-svc; `/healthz` media thêm `rejectedPublishes`; `ingest.stopped` thêm lý do `recovered`; web có trang `/vod/:vodId` |
| 2026-10-11 | Middleware xác thực chấp nhận cả `Authorization: <jwt>` không có `Bearer ` (dán token trong Swagger) |
| 2026-10-11 | Bản đầu: identity-svc (6 endpoint), stream-svc (8 công khai + 1 nội bộ), media-svc (RTMP, HLS, dev), 7 sự kiện đang chạy; nháp API của chat/channel/safety/notification/billing |
