# GĐ3 — Chat realtime, realtime gateway, viewer count (mốc M3)

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (service mới `chat-svc`, transport mới WebSocket, sự kiện mới, compose/Nginx, cross-repo web) | Approved 14/10 (G3-Q1…Q5 theo đề xuất; G3-Q6: một người xem = 1 view dù mở nhiều tab) — WP1–WP8 xong 14/10 | 2026-10-14 |

Lịch lộ trình GĐ3: 07/12 → 25/12, mốc **M3**. GĐ2 đã đóng sớm (~7 tuần trước lịch), nên GĐ3 có thể bắt đầu ngay.
Bảng `nxl_chat` đã có migration từ GĐ0 nhưng chưa có code; trang xem đã chừa cột/tab Chat (GĐ2).

## 1. Task Brief

**Mục tiêu (M3 — lộ trình §3):**

- Hai trình duyệt cùng xem một buổi phát và chat với nhau; Guest đọc được, bấm gửi thì mở modal đăng nhập.
- Streamer bật Slow mode → người xem thấy hiệu lực ngay.
- Tắt một instance chat-svc → client tự nối lại sang instance còn lại, không mất tin (resume theo `seq`).
- Trang xem hiện **số người xem thật**; trạng thái stream (gián đoạn, kết thúc, VOD sẵn sàng) đẩy qua WebSocket
  thay vì polling.
- k6: 1.000 kết nối, gửi → nhận P95 < 500 ms.

**Nguồn:**

| Nguồn | Phần dùng |
| --- | --- |
| Lộ trình §3 GĐ3 | chat-svc, `/v1/ws`, chủ đề `chat:`/`stream:`/`user:`, chế độ chat, rate limit, resume, fan-out NATS, viewer count, k6, viết NXL-F + NXL-E |
| NXL-00 v1.2 §10 | Giao thức WebSocket (auth, op, seq, resume, mã đóng, giới hạn), §5.2 mã `CHAT_*`, §12 hằng số `WS_*` |
| NXL-B v1.1 | B1–B4: BR01–BR30, API 01–04, chủ đề `chat:{streamId}`, AF/E, AC, §14 hiệu năng |
| NXL-A v1.1 | Chủ đề `stream:{streamId}` (`status_changed`, `viewer_count`, `vod_ready`, `title_changed`) |
| NXL-G2 v1.0 | `chat.channel_roles` (đọc ở GĐ3; gán Mod/VIP ở GĐ5 — ST-Q10) |
| ADR-003, ADR-006 | NATS core fan-out giữa instance; một gateway WebSocket trong chat-svc |
| ERD chat (migration `services/chat/migrations`) | `chat_rooms`, `chat_settings`, `chat_messages` (partition theo ngày), `channel_roles`, `mod_actions`, `blocked_terms`, `emotes` |

**Trong phạm vi:** WP1–WP8 ở §2.

**Ngoài phạm vi (§9):** hành động kiểm duyệt (xóa tin, timeout, ban, ghim, từ khóa chặn, auto-mod) → NXL-F/GĐ5;
emote kênh và emote theo tier → GĐ6; Follow thật → GĐ4; Subscriber thật → GĐ6; thông báo `user:{id}` có nội dung
(notification-svc) → GĐ5; `sidebar:{userId}` → GĐ4; Chat Replay.

**Giả định:**

- Một chat-svc chạy **2 instance** sau Nginx (`ip_hash` không cần — client tự nối lại). Trạng thái dùng chung ở
  Redis + PostgreSQL; fan-out qua NATS core (`nxl.rt.<topic>`), không JetStream.
- Xác thực JWT qua JWKS của identity (như stream-svc); Guest = kết nối chỉ đọc.
- Follow/Sub chưa có nguồn thật: chat-svc giữ **bản sao trong Redis**, nạp từ sự kiện `follow.*` (GĐ4 mới phát) và
  CLI dev `chat dev-grant` để demo Followers-only / Subs-only.
- Mod/VIP: đọc `channel_roles`; dữ liệu demo từ seed dev (NXL-G2 ST-Q10).
- Emote toàn cầu: ~12 emote do dự án tự vẽ (SVG), seed vào bảng `emotes`, ảnh phục vụ tĩnh qua Nginx `/emotes/*`.

**Câu hỏi mở:** §10.

## 2. Gói việc

### WP1 — Khung chat-svc và realtime gateway `/v1/ws`

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Service mới | `services/chat` theo mẫu `services/stream`: config, app, entity, usecase, repo, controller (`restapi/v1`, `ws`, `events`), `/metrics`, `/healthz`, Swagger; cổng 8095; thêm vào `go.work`, Makefile, CI, compose (2 replica), Prometheus | AGENTS.md "Adding a new service" |
| Kết nối | Upgrade `GET /v1/ws`; `auth` trong `WS_AUTH_TIMEOUT` (5 s), Guest `token: null`; `ready`; gia hạn token trên cùng kết nối, đóng `4001` khi quá hạn 60 s | NXL-00 §10.1 |
| Khung message | `subscribe`/`unsubscribe`/`ping`/`send` ↔ `subscribed`/`event`/`resync`/`error`/`pong`; giới hạn 20 chủ đề/kết nối, 5 kết nối/user (`4029`) | NXL-00 §10.2, §10.4 |
| Chủ đề | `chat:{streamId}` (WP2), `stream:{streamId}` (WP4), `user:{userId}` (chỉ khung + kiểm quyền, chưa có nguồn — GĐ5) | NXL-00 §10.3 |
| `seq` + resume | `seq` tăng theo chủ đề bằng Redis `INCR`; bộ đệm `WS_RESUME_BUFFER` (200 sự kiện / 5 phút) trong Redis Stream theo chủ đề; quá bộ đệm → `resync` | NXL-00 §10.3 |
| Fan-out | Mỗi sự kiện: ghi bộ đệm + `seq` → publish NATS core `nxl.rt.<topic>`; mỗi instance chỉ giữ subscription NATS cho chủ đề có người nghe tại instance đó | ADR-003 |
| Đóng kết nối | `1012` khi tắt service (graceful), heartbeat 25 s, đóng sau 60 s im lặng | NXL-00 §10.4 |
| Nginx | `location = /v1/ws` → upstream `chat` (2 instance) với `Upgrade`/`Connection`, `proxy_read_timeout 75s`; kiểm `Origin` theo `WEB_ORIGIN`; REST `/v1/chat/*` và `/v1/channels/{id}/chat-settings` → chat | ADR-006 |

### WP2 — Chat B1–B3 (gửi, nhận, lịch sử, emote, mention, badge)

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Vòng đời phòng | `stream.started` → `chat_rooms` `OPEN`; `stream.ended` → `READ_ONLY` (`room_state` event); stream chưa có phòng → `NOT_FOUND`/`CHAT_CLOSED` | NXL-B §7A, BR02 |
| Gửi tin | `send type=message` → chuẩn hóa (trim, NFC, bỏ ký tự điều khiển, ≤ 500 ký tự, ≤ `CHAT_MAX_LINES`), chống trùng theo `clientMsgId` (Redis, 10 phút) + `DUPLICATE_WINDOW`, lưu `chat_messages`, `ack`, phát `message` | BR03–BR08, BR26 |
| Fragments | Server tách `text` / `emote` (emote toàn cầu) / `mention` (`@username` khớp bản sao user) | BR05, BR17, BR19 |
| Badge, màu | Streamer > Moderator > VIP > Subscriber > Verified (tối đa 3); màu theo `userId` từ bảng 12 màu | BR15, BR16 |
| Bản sao user | Consumer `user.registered.v1`, `user.profile_updated.v1`, `user.email_verified.v1` → bảng `chat.users` (migration mới) để có username/displayName/verified và tra `@mention` | NXL-G2 ST-Q6 |
| Lịch sử | API 01 `GET /v1/chat/rooms/{streamId}/messages?before=&limit=` (mặc định `CHAT_HISTORY_ON_JOIN` 50) | NXL-B API 01, BR24 |
| Emote | API 02 `GET /v1/chat/emotes?channelId=` (toàn cầu ở GĐ3); seed + file SVG | NXL-B API 02 |
| Partition | Job tạo partition ngày trước 3 ngày, xóa partition quá `CHAT_RETENTION` | Migration chat |

### WP3 — Chế độ chat và giới hạn (B2, B4 phần server)

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Đọc / sửa chế độ | API 03 `GET` công khai, API 04 `PUT /v1/channels/{channelId}/chat-settings` cho chủ kênh, Moderator (`channel_roles`), Admin; phát `mode_change` | NXL-B API 03–04, BR09–BR12 |
| Kiểm khi gửi | Slow (`CHAT_SLOW_MODE` + `retryAfterSec`), Followers-only (`CHAT_FOLLOWERS_ONLY`), Subs-only (`CHAT_SUBS_ONLY`), miễn trừ theo vai trò | BR09–BR14 |
| Rate limit | Token bucket Redis theo user (`CHAT_RATE_LIMIT` 20 / 30 s) và theo IP; mức trần riêng cho Streamer/Mod | BR13 |
| Ban/timeout (chỉ đọc) | Đọc `mod_actions` còn hiệu lực → `CHAT_BANNED` / `CHAT_TIMED_OUT` (dữ liệu từ seed; thao tác ở GĐ5) | BR28 |
| Sampling | Token bucket theo kết nối khi kênh vượt `SAMPLING_THRESHOLD`; tin ưu tiên không bị loại; event `sampling` | BR22 |
| Kill switch | Cờ `CHAT_DISABLED` (env) → mọi phòng chỉ đọc, `CHAT_DISABLED` | NXL-B §17 |
| Bản sao follow/sub | Redis `follow:{channelId}` (userId → createdAt), `sub:{channelId}`; consumer `follow.*` sẵn sàng cho GĐ4; CLI `chat dev-grant follow|sub` | Lộ trình GĐ3 |

### WP4 — Chủ đề `stream:` và viewer count

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Sự kiện stream mới | stream-svc phát qua outbox: `stream.status_changed.v1` (`LIVE`↔`INTERRUPTED`), `stream.updated.v1` (tiêu đề, tag), `stream.vod_ready.v1`; thêm vào `pkg/events` + NXL-00 §11.1 | NXL-A §7A, NXL-00 §17 |
| Chuyển tiếp | chat-svc nghe `stream.*` → event `status_changed`, `title_changed`, `vod_ready` trên `stream:{streamId}` | NXL-00 §10.3 |
| Viewer count | Mỗi **người xem** nghe `stream:{id}` = 1 (khử trùng theo `userId`, Guest theo `anonId` gửi trong `auth`; nhiều tab / nhiều kết nối của cùng người = 1); Redis ZSET theo stream với heartbeat, quét mỗi `VIEWER_COUNT_INTERVAL` (10 s) → event `viewer_count` + snapshot khi subscribe | NXL-A, ADR-004 |
| Ghi lại cho stream-svc | `GET /v1/streams` và `GET /v1/streams/{id}` trả `viewerCount` (stream-svc đọc Redis chỉ đọc, key `viewers:{streamId}`), dùng cho thẻ live trên Trang chủ; `peakViewers` lưu khi kết thúc (NXL-G2 Dashboard — chỉ chuẩn bị cột, không UI) | NXL-C, NXL-G2 |

### WP5 — Web: chat panel và realtime client

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Realtime client | Một kết nối `/v1/ws` cho cả app (store Zustand): auth/gia hạn khi token refresh, subscribe theo trang, backoff 1→30 s ± 20 %, resume `lastSeq`, `resync` → tải lại REST | NXL-00 §10, NXL-B AF09 |
| Chat panel | Thay khung giữ chỗ GĐ2 (cột desktop, tab mobile): danh sách ảo hóa, render theo lô mỗi frame, giữ ≤ `CHAT_DOM_LIMIT` (150), auto-scroll thông minh + nút "Tin mới", bộ đệm khi cuộn lên ≤ 500 | NXL-B §9A, BR23, BR25 |
| Ô nhập | Enter gửi / Shift+Enter xuống dòng, đếm ký tự, optimistic `PENDING` → `SENT`/`FAILED`, gửi lại thủ công khi lỗi mạng, placeholder lý do khi không gửi được, `/` để focus, chặn phím tắt player | BR26, BR29, BR30 |
| Emote, mention | Emote picker (toàn cầu), gợi ý `@` từ người đã chat gần đây; render fragments, không HTML | AF01, AF02, BR05 |
| Chế độ chat | Hộp cài đặt chế độ cho Streamer/Mod; banner đổi chế độ; lỗi `CHAT_*` theo bảng NXL-B | NXL-B §9C |
| Guest | Đọc được; ô nhập mở modal đăng nhập (`pendingAction` GĐ1) | BR01, AF10 |
| Trang xem | Viewer count trên player/ChannelInfo và thẻ live; trạng thái stream từ `stream:` (giữ polling làm dự phòng khi WS mất) | NXL-A |
| Design | Cột chat của frame gốc `03 · Phòng Live Stream` (pen.dev) + token; trạng thái còn thiếu theo NXL-B §9 và brief `DATN/docs/design/2026-10-14-design-brief-gd03.md` | G3-Q4 |

### WP6 — Hiệu năng và độ bền

- k6 (script trong `deploy/k6/`): 1.000 kết nối WebSocket, 1 phòng, 20 người gửi; đo gửi → nhận P95, tỉ lệ lỗi;
  kịch bản tắt 1 instance giữa chừng (reconnect hàng loạt). Kết quả ghi `DATN/docs/report/measurements/`.
- Metric Prometheus: số kết nối, số subscription, tin gửi/nhận, độ trễ fan-out, `RATE_LIMITED`, `resync`; panel
  Grafana "Chat".

### WP7 — Tài liệu

- **NXL-00 v1.3:** sự kiện mới (§11.1), hằng số `CHAT_*`/`WS_*`/`VIEWER_COUNT_INTERVAL` (§12), đánh dấu §10 ✅.
- **NXL-B v1.2:** đóng Q2–Q7, Q10, Q14 theo §10; phần ✅ theo hiện thực.
- **NXL-F v1.0** (Kiểm duyệt) và **NXL-E v1.0** (Thông báo) — viết theo lộ trình GĐ3 để GĐ5 có spec.
- API catalog, ERD chat (thêm `chat.users`), README (cổng 8095, 2 instance), TSD + Completed Plan.

### WP8 — E2E và demo M3

- Playwright 2 context: A (tài khoản) và B (Guest → đăng nhập) cùng xem; A gửi → B nhận; Slow mode; Guest bị mời
  đăng nhập; stream kết thúc → chat chỉ đọc.
- Kịch bản tắt 1 instance chat-svc (script) → client nối lại, không mất tin.
- Hồi quy E2E M1, M2, admin.

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Gói |
| --- | --- | --- |
| Service mới | `services/chat/**` (config, app, entity, usecase, repo, controller rest/ws/events) | WP1–WP4 |
| Migration chat | Mới: `chat.users`; partition ngày; seed emote toàn cầu (không sửa migration cũ) | WP2 |
| `pkg/events` | `stream.status_changed.v1`, `stream.updated.v1`, `stream.vod_ready.v1` (+ payload) | WP4 |
| `pkg/` mới | `pkg/redis` (client + healthcheck), có thể `pkg/rt` (khung message WS dùng chung nếu cần) | WP1 |
| stream-svc | Outbox phát 3 sự kiện mới; `viewerCount` trong DTO stream (trường mới, tùy chọn); cột `peak_viewers` (migration mới) | WP4 |
| Hạ tầng | `docker-compose.yml` (chat ×2, Redis đã có), `deploy/nginx/nginx.conf` (`/v1/ws`, `/v1/chat`, `/emotes`), `deploy/prometheus`, Grafana, `.env.example`, CI | WP1, WP6 |
| Web | `src/lib/realtime/*`, `src/features/chat/*`, `features/watch` (thay khung giữ chỗ), `features/player`/`home` (viewer count), i18n vi/en | WP5 |

| Transport | Có | Endpoint |
| --- | --- | --- |
| WebSocket (mới) | Có | `GET /v1/ws` |
| REST công khai | Có | `GET /v1/chat/rooms/{streamId}/messages`, `GET /v1/chat/emotes`, `GET`/`PUT /v1/channels/{channelId}/chat-settings` |
| NATS JetStream | Có | Mới: 3 sự kiện stream; nghe: `stream.*`, `user.*`, `follow.*` |
| NATS core | Có | `nxl.rt.<topic>` (fan-out nội bộ chat-svc) |

Luồng cũ có thể vỡ: route `/v1/` mặc định ở Nginx (thêm route chat trước nó); DTO stream (chỉ thêm trường);
outbox stream-svc (thêm loại sự kiện); trang xem dùng polling trạng thái (giữ làm dự phòng).

## 4. Mã lỗi

Dùng mã đã có ở NXL-00 §5.2 (`CHAT_*`, `RATE_LIMITED`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`,
`VALIDATION_FAILED`). Không thêm mã mới. Qua WebSocket: frame `error` với `ref = clientMsgId`.

## 5. Sinh code / cấu hình / tài liệu

- [ ] TSD `docs/plans/technical/2026-10-14-gd03-chat-realtime.md` (giao thức, sequence diagram, schema, hằng số).
- [ ] `make nxl-swag` (chat, stream), mock (`go generate`).
- [ ] Env mới: `NXL_PG_URL_CHAT` (đã có), `CHAT_REDIS_ADDR`, `CHAT_NATS_URL`, `CHAT_JWKS_URL`, `CHAT_WS_*`,
  `CHAT_RATE_*`, `CHAT_DUPLICATE_WINDOW`, `CHAT_RETENTION`, `CHAT_DISABLED`, `CHAT_REQUIRE_VERIFIED_EMAIL`,
  `STREAM_REDIS_ADDR` — vào `.env.example` + compose.

## 6. Thứ tự

| # | Việc | Phụ thuộc |
| --- | --- | --- |
| 1 | Chốt §10, TSD | Plan duyệt |
| 2 | WP1 khung chat-svc + gateway + Nginx | 1 |
| 3 | WP2 chat cơ bản, WP4 sự kiện stream + viewer count | 2 |
| 4 | WP3 chế độ chat, giới hạn, sampling | 3 |
| 5 | WP5 web (song song từ bước 3 khi giao thức đã đông cứng) | 3 |
| 6 | WP6 k6 + metric; WP8 E2E | 4, 5 |
| 7 | WP7 tài liệu (NXL-F, NXL-E viết song song bất cứ lúc nào), Completed Plan | 1–6 |

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Fan-out nhiều instance gây trùng / lệch thứ tự | `seq` cấp tập trung ở Redis trước khi publish; client bỏ qua `seq` ≤ đã thấy |
| Mất tin khi tắt instance | Tin đã lưu PG + bộ đệm Redis trước khi ack; resume theo `lastSeq` |
| Redis là điểm lỗi đơn | Chấp nhận ở quy mô đồ án; khi Redis lỗi: từ chối gửi (`INTERNAL_ERROR`), giữ kết nối đọc |
| Bảng `chat_messages` partition, không unique `client_msg_id` | Chống trùng bằng Redis (migration đã ghi chú) |
| 1.000 kết nối trên máy demo | k6 sớm; đo CPU/RAM; tách k6 sang máy khác nếu cần |
| Hai service chạy timer viewer count | Chỉ chat-svc tính; stream-svc chỉ đọc key Redis |

## 8. Kiểm tra

- [ ] `make format`, `make nxl-lint`, `make nxl-test` (`-race`), repo test PG chat + stream.
- [ ] Stack đủ (2 chat-svc) + E2E M3 + hồi quy M1/M2/admin; k6 1.000 kết nối.
- [ ] Hostile: không gửi `auth` (4008), token sai/hết hạn (4001), Guest gửi (`UNAUTHORIZED`), quá 20 chủ đề, quá
  5 kết nối (4029), tin rỗng/dài, trùng, flood, Slow/Followers/Subs, phòng `READ_ONLY`, Redis/NATS/PG tắt, origin lạ.

## 9. Lệch so với lộ trình / spec

| Nguồn | Yêu cầu | Làm | Lý do |
| --- | --- | --- | --- |
| Lộ trình GĐ3 | Bản sao follow/sub từ dữ liệu giả | Redis + CLI dev; consumer `follow.*` sẵn | Follow thật GĐ4, Sub GĐ6 |
| NXL-B B1 | Emote kênh, khóa theo tier | Chỉ emote toàn cầu | Emote kênh cần upload + Sub (GĐ6) |
| NXL-B AF06/AF07, BR20 | Xóa tin, timeout/ban, ghim | Chỉ hiển thị/kiểm tra từ dữ liệu có sẵn | Thao tác thuộc NXL-F (GĐ5) |
| NXL-00 §10.3 | `user:`, `sidebar:` | `user:` có khung, chưa nguồn; `sidebar:` chưa làm | Nguồn ở GĐ4/GĐ5 |
| NXL-B §14 | 100.000 tin/phút/kênh | Mục tiêu đồ án: 1.000 kết nối, P95 < 500 ms | Máy demo; ghi rõ trong báo cáo |
| NXL-B §17 | Feature flag từng phần | Chỉ `CHAT_DISABLED` (kill switch) | Quy mô đồ án |

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| G3-Q1 | Thư viện mới (AGENTS.md cấm tự thêm): Go `github.com/gofiber/contrib/websocket` (WebSocket trên Fiber), `github.com/redis/go-redis/v9`; web `@tanstack/react-virtual` (danh sách ảo); công cụ k6 (image `grafana/k6`, chỉ chạy tay, không vào CI) | Đồng ý cả 4 |
| G3-Q2 | Chạy 2 instance chat-svc trong compose mặc định hay chỉ khi demo (profile riêng)? | Mặc định 2 instance, để kịch bản "tắt 1 instance" luôn chạy được |
| G3-Q3 | Chốt giá trị NXL-B: chat khi `SCHEDULED` (Q2), VIP miễn Slow (Q3), link không nhấp được (Q4), `SAMPLING_THRESHOLD`/`CAP` 30/15 tin/s (Q5), `CHAT_DOM_LIMIT` 150 · lịch sử 50 · `RESUME_MAX_GAP` 200 · `PAUSED_BUFFER_MAX` 500 (Q6), `DUPLICATE_WINDOW` 30 s · 20 tin/30 s · `ACK_TIMEOUT` 5 s (Q7), `CHAT_RETENTION` 30 ngày, không Chat Replay (Q10), `CHAT_MAX_LINES` 3 (Q14) | Theo các giá trị đề xuất này (Q2: chưa mở) |
| G3-Q4 | Design chat panel: ông chủ có frame pen.dev cho chat (desktop/mobile, emote picker, cài đặt chế độ) chưa? | **Chốt (14/10):** pen.dev chưa có frame chat GĐ3; dùng cột chat của frame gốc `03 · Phòng Live Stream` + token, brief design `DATN/docs/design/2026-10-14-design-brief-gd03.md`; chỉnh khi có frame |
| G3-Q5 | Viết NXL-F, NXL-E trong GĐ3 (như lộ trình) hay để đầu GĐ5? | Trong GĐ3, sau khi chat chạy (WP7) |
| G3-Q6 | Viewer count tính Guest theo kết nối (một Guest mở 2 tab = 2) hay theo `X-NXL-Anon-Id`? | **Chốt:** một người xem mở nhiều tab chỉ tính 1 — tài khoản theo `userId`, Guest theo `anonId` (localStorage, dùng chung giữa các tab; web gửi trong `auth`) |

## 11. Tiêu chí hoàn thành (M3)

- Given hai trình duyệt mở cùng buổi phát, When A gửi "xin chào", Then B thấy trong < 500 ms (P95), có badge
  Streamer nếu A là chủ kênh; Guest thấy tin nhưng ô nhập mở modal đăng nhập.
- Given Streamer bật Slow 10 s, When người xem gửi 2 tin liên tiếp, Then tin thứ hai bị từ chối `CHAT_SLOW_MODE`
  kèm thời gian chờ, banner chế độ hiện ngay ở mọi người xem.
- Given 2 instance chat-svc, When tắt instance đang giữ kết nối, Then client nối lại trong ≤ 2 s sau khi có instance
  sống và không thiếu tin (so `seq`).
- Given đang xem, Then số người xem cập nhật mỗi ~10 s; tắt OBS → overlay gián đoạn đến qua WebSocket; kết thúc →
  chat chỉ đọc, "Buổi phát đã kết thúc".
- k6: 1.000 kết nối, P95 gửi → nhận < 500 ms, lỗi < 1 %; kết quả trong `report/measurements`.
- NXL-00 v1.3, NXL-B v1.2, NXL-F v1.0, NXL-E v1.0 có trong `DATN/docs/ba/specs`.
