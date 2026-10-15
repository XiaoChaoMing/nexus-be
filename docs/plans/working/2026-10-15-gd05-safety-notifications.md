# GĐ5 — An toàn & Thông báo (kiểm duyệt, báo cáo, Admin, thông báo, mốc M4)

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (2 service mới safety + notification, migration, sự kiện mới, chuyển mailer, Nginx `secure_link`, cross-repo web + admin) | Draft — chờ duyệt §10 | 2026-10-15 |

Lịch lộ trình GĐ5: 11/01 → 22/01 (2 tuần), mốc **M4**. GĐ4 xong sớm ~11 tuần nên bắt đầu ngay được. Cơ sở dữ liệu
`nxl_safety`, `nxl_notification` đã có migration từ GĐ0; chat-svc đã có bảng `mod_actions`, `blocked_terms`,
`channel_roles` (mới đọc ban/timeout, gán vai trò chỉ qua CLI dev); identity đã có `users.status/locked_until`,
`user_blocks` (chưa có code); channel-svc đã phát `follow.*`, `schedule.*` cho thông báo.

## 1. Task Brief

**Mục tiêu (M4 — lộ trình §3):**

- **Hành trình Moderator:** Creator gán Mod → Mod xóa tin, timeout, ban, ghim tin, thêm từ khóa chặn / chặn link →
  người xem bị chặn thấy đúng thông báo; người xem báo cáo tin nhắn (kèm 10 tin gần nhất làm bằng chứng) → Admin nhận
  trong `nexuslive-admin`, xử lý (khóa tài khoản / tạm ngưng kênh / gỡ buổi phát) → người báo cáo nhận kết quả.
- **Thông báo:** người xem theo dõi kênh → Creator Go Live → nhận thông báo trong app (realtime, chuông + trung tâm
  thông báo) và email (Mailpit); nhắc lịch, @mention, kết quả đơn Creator / báo cáo.
- **Độ bền:** tắt notification-svc → xem và chat vẫn chạy → bật lại → thông báo tồn đọng được gửi (AC-E14).
- **Cổng 18+ thật:** URL HLS có chữ ký (`secure_link`) gắn với phiên xem.

**Nguồn:**

| Nguồn | Phần dùng |
| --- | --- |
| Lộ trình §3 GĐ5, §5 (phương án gộp service) | Phạm vi, kịch bản M4 |
| NXL-F v1.0 | Kiểm duyệt chat, Report, Block/Mute, Admin (khóa, tạm ngưng, gỡ stream), audit log, cổng tuổi |
| NXL-E v1.0 | Loại thông báo, quy tắc (cooldown, gộp, giới hạn email), trung tâm thông báo, cài đặt, email + chuyển mailer |
| NXL-G2 §3.6–3.7, §13.3 | Gán Mod / VIP, Studio → Cộng đồng (ST-Q10) |
| NXL-00 v1.4, NXL-B, NXL-D, NXL-A | Sự kiện, mã đóng `4003`, `CHAT_CONTENT_BLOCKED`, kênh bị tạm ngưng, `admin_takedown`, `secure_link` |
| Errata E-18, E-19, E-21 (phần `locale/timezone`), E-22, E-24 | Catalog API, bảng sự kiện kiến trúc, ERD |

**Trong phạm vi:** WP1–WP10 ở §2.

**Ngoài phạm vi (§9):** auto-mod "giữ chờ duyệt" (held for review), push / quiet hours / digest (NE-Q11), báo cáo
VOD theo mốc thời gian, Admin xem lại chat cũ ngoài bằng chứng, thanh toán (GĐ6).

**Giả định:** không thêm thư viện Go (template `html/template`, `net/smtp`, HMAC stdlib, `x/text` đã có); Nginx
`nginx:1.31.3-alpine` có sẵn module `secure_link`; email dev qua Mailpit.

**Câu hỏi mở:** §10.

## 2. Gói việc

### WP1 — Nền: 2 service mới + sự kiện + Nginx

- `services/safety` (cổng 8099) và `services/notification` (cổng 8097) theo mẫu `services/stream`: config, app, auth JWKS,
  outbox, consumer bền, `/metrics`, Swagger; thêm `go.work`, Makefile, CI, compose, Prometheus.
- `pkg/events`: `chat.mod_action`, `chat.settings_changed`, `chat.mention`, `channel.role_changed`, `audit.recorded`,
  `safety.report_resolved | user_actioned | channel_actioned | stream_takedown`, `user.locked | unlocked | blocked |
  unblocked`, `channel.suspended | restored`, `notification.created | unread_changed`; `user.*` thêm `locale`, `timezone`
  (E-21).
- Nginx: `/v1/reports`, `/v1/admin/*` tách theo tiền tố (creator-applications + `GET users/{id}` → identity, còn lại →
  safety), `/v1/notifications*`, `/v1/me/notification-settings`, `/v1/me/blocks*` → identity, `/v1/me/channel-roles`,
  `/v1/channels/*/(timeouts|bans|restrictions|blocked-terms|roles|users)` → chat (đặt trước route channel).

### WP2 — Kiểm duyệt chat (chat-svc, NXL-F §3.1–3.6)

- Xóa tin (mềm), Timeout (60 / 600 / 3600 s, tối đa 14 ngày) / gỡ timeout, Ban / Unban, Ghim / bỏ ghim (1 tin / phòng),
  thẻ người dùng (user card), danh sách hạn chế; sự kiện realtime `delete`, `user_cleared`, `user_restored`, `pin`,
  `unpin`; snapshot thêm `room.pinned`, `me.restriction`.
- Từ khóa chặn (≤ 100 / kênh; NFC + chữ thường + bỏ dấu, khớp nguyên từ) và chặn link (danh sách TLD) →
  `CHAT_CONTENT_BLOCKED`; miễn trừ Owner / Mod (cả hai), VIP (chỉ link).
- Vai trò: `GET|POST|PUT|DELETE /v1/channels/{cid}/roles`, `GET /v1/me/channel-roles` (thay `dev-grant`), ≤ 50 / kênh;
  `channel.role_changed.v1`.
- `chat.mention.v1` (≤ 5 người / tin), API nội bộ bằng chứng `GET /internal/v1/chat/evidence` (≤ 300 ms).
- Consumer `user.locked` (đóng kết nối `4003`, không gửi được), `channel.suspended` (phòng chỉ đọc), `user.blocked`
  (ẩn tin của người bị chặn với người chặn — phía client theo danh sách chặn).
- Migration: `UNTIMEOUT`, `mod_actions.stream_id`, `chat_settings.block_links`.

### WP3 — Báo cáo + Admin (safety-svc, NXL-F §3.7–3.13)

- `POST /v1/reports` (tin nhắn / người dùng / stream / kênh; 10 / giờ / người) → bằng chứng từ chat (lỗi → `UNAVAILABLE`).
- Admin: hàng đợi báo cáo (nhận xử lý, giải quyết / bỏ qua kèm ghi chú), khóa / mở khóa tài khoản (24 h / 7 ngày /
  30 ngày / vĩnh viễn), tạm ngưng / khôi phục kênh, gỡ buổi phát (`202 {actionId}`); audit log chỉ ghi thêm
  (`audit.recorded` từ mọi service).
- Bản sao `users`, `streams`, `channels` từ sự kiện; phát `safety.*`.

### WP4 — Thực thi phía service khác

- **identity:** chặn người dùng `GET /v1/me/blocks`, `PUT|DELETE /v1/me/blocks/{userId}` (≤ 1000) → `user.blocked |
  unblocked`; consumer `safety.user_actioned` → khóa / mở khóa (`users.status`), thu hồi refresh token, `user.locked |
  unlocked`; đăng nhập khi bị khóa → mã có sẵn; `GET /v1/admin/users/{id}`.
- **channel-svc:** `safety.channel_actioned` → `SUSPENDED` / `ACTIVE`, hủy lịch, `channel.suspended | restored`; Studio
  ghi → 403 `CHANNEL_SUSPENDED`.
- **stream-svc:** `safety.stream_takedown` → kết thúc `admin_takedown`, VOD → `DELETED` (kể cả VOD tới sau); kênh bị tạm
  ngưng → không Authorize được, đang live thì kết thúc, hủy dòng `SCHEDULED`.

### WP5 — Thông báo (notification-svc, NXL-E)

- Bản sao `users`, `channels`, `follows`, `user_blocks` từ sự kiện; consumer: `stream.started` (CREATOR_LIVE theo
  `notifyLevel`), `schedule.reminder_due` (STREAM_STARTING), `schedule.created | occurrence_changed` (SCHEDULE),
  `chat.mention` (MENTION), `follow.created` (NEW_FOLLOWER, gộp 60 phút), `creator.approved | rejected`,
  `safety.report_resolved`, `user.locked`, `channel.suspended | restored`, `safety.stream_takedown`.
- Quy tắc: mặc định + loại khóa (BR-E02/E07), cooldown live 30 phút in-app / 12 giờ email (Redis), gộp mention 5 phút,
  ≤ 10 email / ngày, fan-out lô 500 với khóa duy nhất `(user_id, source_event_id)`, chỉ email địa chỉ đã xác minh, lưu 90
  ngày.
- API: `GET /v1/notifications`, `/unread-count`, `POST /{id}/read`, `/read-all`, `DELETE /{id}`,
  `GET|PUT /v1/me/notification-settings`, `POST /v1/notifications/unsubscribe` (token HMAC).
- Realtime: `notification.created | unread_changed` → chat-svc chuyển tiếp lên `user:{userId}` (`notification`,
  `unread_count`).
- Email outbox + worker (thử lại 1 / 5 / 15 / 60 phút, tối đa 5 lần), ~10 mẫu vi / en (HTML + text).
- **Chuyển mailer:** consumer `notification-auth-mailer` nhận `auth.email_requested.v1`; identity tắt mailer bằng
  `IDENTITY_MAILER_ENABLED=false` trong cùng một lần khởi động lại (AC-E17).

### WP6 — URL HLS có chữ ký (`secure_link`)

- `manifestURL()` (điểm duy nhất từ GĐ2) gắn `?md5=…&expires=…` theo phiên xem (hạn = `PlaybackTTL`); Nginx `/hls/*`
  kiểm `secure_link_md5` (bí mật dùng chung `NXL_HLS_SECRET`); playlist con / segment dùng cùng chữ ký qua đường dẫn
  thư mục (`secure_link` theo tiền tố `streams/{id}/`).
- Bỏ `manifestUrl` khỏi `GET /v1/streams/{id}` công khai (đã báo trước ở TSD GĐ2) sau khi web chỉ dùng phiên xem;
  `/dev/player` của media-svc dùng URL nội bộ.

### WP7 — Web người dùng

- Chat: menu Mod (xóa, timeout, ban, ghim), thẻ người dùng, tin ghim, trạng thái bị hạn chế, lỗi nội dung bị chặn;
  báo cáo (modal), chặn / tắt tiếng (mute chỉ phía client); trang `/moderation/:channel` (danh sách hạn chế cho Mod).
- Studio → **Cộng đồng**: Mod / VIP (thêm theo username, gỡ), từ khóa chặn, bật chặn link.
- Thông báo: chuông + popover (số chưa đọc realtime), trang `/notifications`, toast khi có thông báo live, cài đặt
  `/settings/notifications`, trang hủy đăng ký email; `/settings/privacy` (danh sách đã chặn).
- Tài khoản bị khóa → đóng `4003` → màn hình "Tài khoản bị khóa" (đã có trang hệ thống GĐ1); kênh tạm ngưng → hồ sơ
  / trang xem hiện trạng thái tương ứng.

### WP8 — `nexuslive-admin`

- Bật các mục đang tắt: **Báo cáo** (hàng đợi, chi tiết + bằng chứng, nhận xử lý, giải quyết), **Người dùng** (tra cứu,
  khóa / mở khóa), **Kênh** (tạm ngưng / khôi phục), **Buổi phát** (danh sách đang live từ `GET /v1/streams`, gỡ),
  **Nhật ký** (audit log, lọc); hỏi lại mỗi 30 s (NF-Q9).

### WP9 — Tài liệu

- NXL-F v1.1, NXL-E v1.1 (đóng câu hỏi theo §10, cổng safety 8099), NXL-00 v1.5, NXL-G2 (ST-Q10), API catalog +
  kiến trúc §4 + ERD (E-18, E-19, E-22, E-24), ADR mới cho email (notification sở hữu) và `secure_link`.
- Brief design GĐ5 (G5-Q8). TSD + Completed Plan.

### WP10 — Kiểm thử và M4

- Unit + repo test PG mọi service, lint, Swagger, migration up → down → up.
- E2E: **M4 Moderator** (gán Mod → xóa / timeout / ban / ghim / từ khóa chặn → người xem thấy đúng → báo cáo → Admin
  khóa → người bị khóa bị ngắt `4003` → người báo cáo nhận kết quả), **M4 Thông báo** (follow → Go Live → chuông +
  email Mailpit; tắt notification-svc → live + chat vẫn chạy → bật lại → thông báo tồn đọng tới), admin E2E, `secure_link`
  (URL hết hạn / sửa chữ ký → 403); hồi quy M1–M4 GĐ4.

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Gói |
| --- | --- | --- |
| Service mới | `services/safety`, `services/notification` | WP1, WP3, WP5 |
| chat-svc | mod actions, auto-mod, roles API, evidence API, mention, consumer lock / suspend, chuyển tiếp `user:` | WP2, WP5 |
| identity | blocks API, consumer khóa, `user.*` thêm trường, `GET /v1/admin/users/{id}`, cờ tắt mailer | WP4, WP5 |
| channel / stream / media | tạm ngưng kênh, gỡ stream, `admin_takedown`, `manifestURL` ký | WP4, WP6 |
| `pkg/events` | ~20 loại sự kiện mới, thêm trường (chỉ thêm) | WP1 |
| Hạ tầng | compose (2 service), Nginx (route + `secure_link`), Prometheus, `.env.example`, CI | WP1, WP6 |
| Web, admin | `features/chat` (mod), `features/notifications` (mới), `features/studio/community` (mới), settings; admin 5 trang | WP7, WP8 |

| Transport | Có | Ghi chú |
| --- | --- | --- |
| REST công khai | Có | ~35 endpoint (NXL-F §13, NXL-E §13, NXL-G2 §13.3) |
| Internal HTTP | Có | `GET /internal/v1/chat/evidence` |
| WebSocket | Có | `chat:` thêm 5 sự kiện; `user:` có nguồn thật |
| NATS | Có | ~20 sự kiện mới; `user.*` thêm `locale`, `timezone` |

Luồng cũ có thể vỡ: route `/v1/admin/*` (đang về identity), `/v1/channels/*` (thêm nhánh chat), email xác minh / đặt lại
mật khẩu (chuyển mailer), phát HLS (thêm chữ ký — mọi player, E2E M2/M3), `dev-grant` (vai trò qua API), snapshot chat.

## 4. Mã lỗi

Dùng mã có sẵn: `CHAT_TIMED_OUT`, `CHAT_BANNED`, `CHAT_CONTENT_BLOCKED` (✅ GĐ5), `FORBIDDEN`, `NOT_FOUND`,
`INVALID_STATE`, `CONFLICT` + `details.limit`, `RATE_LIMITED`, `ACCOUNT_LOCKED`, `CHANNEL_SUSPENDED`. Không thêm mã mới
(NF-Q14).

## 5. Sinh code / cấu hình / tài liệu

- TSD `docs/plans/technical/2026-10-15-gd05-safety-notifications.md`.
- `make nxl-swag` (chat, identity, safety, notification, channel, stream), mock, migration up → down → up.
- Env mới: `SAFETY_*`, `NOTIFICATION_*` (SMTP, MAIL_FROM, WEB_BASE_URL, UNSUBSCRIBE_SECRET, cooldown, giới hạn),
  `IDENTITY_MAILER_ENABLED`, `NXL_HLS_SECRET`, `STREAM_HLS_SECRET`.

## 6. Thứ tự

| # | Việc | Phụ thuộc |
| --- | --- | --- |
| 1 | Chốt §10, TSD, brief design GĐ5 | Plan duyệt |
| 2 | WP1 khung 2 service + sự kiện + Nginx | 1 |
| 3 | WP2 kiểm duyệt chat, WP4 identity blocks (song song) | 2 |
| 4 | WP3 safety + WP4 thực thi khóa / tạm ngưng / gỡ | 2, 3 |
| 5 | WP5 notification + chuyển mailer | 2 |
| 6 | WP6 `secure_link` | 2 |
| 7 | WP7 web, WP8 admin (song song từ bước 3 khi hợp đồng đã chốt) | 3–5 |
| 8 | WP9 tài liệu, WP10 E2E M4, Completed Plan | 1–7 |

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Phạm vi ~26–30 ngày công (2 service mới) | Còn dư ~11 tuần; phương án dự phòng của lộ trình: gộp safety + notification thành một tiến trình (G5-Q1) |
| Chuyển mailer làm mất email xác minh | Cắt chuyển trong một lần khởi động lại, consumer `DeliverPolicy new`, E2E M1 chạy lại |
| `secure_link` làm vỡ phát HLS (playlist con, segment, VOD) | Ký theo tiền tố thư mục; E2E M2 / M3 / M4 + kiểm tra hết hạn |
| Fan-out thông báo lớn khi kênh nhiều người theo dõi | Lô 500, khóa duy nhất, consumer bền |
| Route `/v1/admin/*` và `/v1/channels/*` chồng nhau | Test định tuyến từng tiền tố như GĐ3 / GĐ4 |
| Thiếu design (mod menu, report, trung tâm thông báo, 5 trang admin) | Brief GĐ5 ngay bước 1; dựng theo token + spec trước |

## 8. Kiểm tra

- [ ] `make format`, `make nxl-lint`, `make nxl-test`, repo test PG mọi service, migration up → down → up.
- [ ] Stack cục bộ đủ 8 service + E2E M4 Moderator, M4 Thông báo, admin, hồi quy M1–M3 + GĐ4.
- [ ] Hostile: Mod hành động trên Owner / Mod khác, người thường gọi API Mod, ban chính mình, báo cáo quá giới hạn, Admin
  tự khóa mình, khóa người đang live, gỡ stream đã kết thúc, chặn quá 1000, email chưa xác minh, token hủy đăng ký
  sai / hết hạn, URL HLS hết hạn / sai chữ ký, notification-svc tắt.

## 9. Lệch so với lộ trình / spec

| Nguồn | Yêu cầu | Làm | Lý do |
| --- | --- | --- | --- |
| NXL-F §12/§17 | safety cổng 8096 | 8099 | 8096 đã dùng cho `chat-2` (GĐ3) |
| NXL-B | Auto-mod "giữ chờ duyệt" | Không | Ngoài phạm vi đồ án |
| NXL-E | Push, quiet hours, digest | Không (NE-Q11) | Ngoài phạm vi |
| NXL-G1 GL-Q2 | `secure_link` tùy chọn GĐ5 / GĐ7 | Làm ở GĐ5 (G5-Q3) | Còn dư thời gian; cổng 18+ chỉ thật khi có chữ ký |

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| G5-Q1 | Tách safety-svc và notification-svc (ADR-001) hay gộp thành một tiến trình (phương án dự phòng của lộ trình)? | **Tách** — đang sớm ~11 tuần, giữ đúng kiến trúc 8 service; safety cổng 8099, notification 8097 |
| G5-Q2 | Gán Mod / VIP qua API + Studio → Cộng đồng ngay trong GĐ5 (ST-Q10)? | Có; `dev-grant` chỉ còn cho dữ liệu demo |
| G5-Q3 | URL HLS có chữ ký (`secure_link`) làm ở GĐ5 hay để GĐ7? | **GĐ5** — điểm gắn duy nhất đã có từ GĐ2; không cần image Nginx mới |
| G5-Q4 | Chấp nhận đề xuất NF-Q1…Q14 của NXL-F? (Mod được ban; Owner phải gỡ vai trò Mod trước khi xử lý Mod; khớp nguyên từ bỏ dấu; bằng chứng qua API nội bộ, lỗi → `UNAVAILABLE`; gỡ stream luôn xóa VOD; tạm ngưng kênh không thời hạn; Mod không xem nội dung gốc đã xóa; báo cáo STREAM / CHANNEL không kèm chat; Admin hỏi lại 30 s; thời lượng khóa cố định; không thêm mã lỗi) | Chấp nhận tất cả |
| G5-Q5 | Chấp nhận đề xuất NE-Q1…Q10 của NXL-E? (số chưa đọc lấy từ notification-svc; chuông `ALL` nhận cả lịch mới; không thông báo VOD mới; chỉ email địa chỉ đã xác minh; chuyển mailer một lần khởi động lại; thêm `SECURITY_LOCK`; `user.*` thêm `locale` / `timezone`; toast live tối đa 1 / 10 s) | Chấp nhận tất cả |
| G5-Q6 | Trang Admin "Buổi phát": spec chưa có API danh sách riêng | Dùng `GET /v1/streams` (đang live) + nút gỡ; không làm lịch sử buổi phát |
| G5-Q7 | Email thật hay Mailpit? | Mailpit cho dev / demo (như GĐ1); SMTP thật cấu hình bằng biến môi trường khi triển khai |
| G5-Q8 | Design GĐ5 (mod menu, user card, report modal, trung tâm thông báo, cài đặt, Studio Cộng đồng, 5 trang admin) chưa có | Em viết brief GĐ5 cho agent design; dựng theo token + spec trong lúc chờ |

## 11. Tiêu chí hoàn thành (M4)

- Given Creator gán A làm Mod, When A xóa tin / timeout 60 s / ban B, Then mọi người xem thấy tin biến mất, B thấy "Bạn bị
  tạm khóa chat đến …" / "Bạn bị cấm chat trong kênh này" trong ≤ 1 s; ghim tin hiện ở đầu khung chat của mọi người.
- Given từ khóa chặn "spam" và bật chặn link, When người xem gửi "SPẢM" / "xem tại abc.xyz", Then `CHAT_CONTENT_BLOCKED`;
  Mod gửi được.
- Given người xem báo cáo một tin, Then Admin thấy báo cáo kèm 10 tin gần nhất; khóa tài khoản 24 h → người bị khóa bị ngắt
  `4003`, không đăng nhập được; người báo cáo nhận thông báo "Báo cáo của bạn đã được xử lý".
- Given Admin gỡ buổi phát đang live, Then trang xem hiện "Buổi phát đã bị gỡ", VOD không có; Creator nhận email.
- Given người xem theo dõi kênh (chuông Tất cả), When Creator Go Live, Then chuông tăng số chưa đọc trong ≤ 5 s, toast hiện,
  email tới Mailpit; Go Live lại trong 30 phút không gửi lại.
- Given tắt notification-svc, Then xem live + chat bình thường; bật lại → thông báo tồn đọng tới đủ, không trùng.
- Given URL HLS hết hạn hoặc sửa chữ ký, Then 403; người chưa xác nhận 18+ không có URL hợp lệ.
- Email xác minh / đặt lại mật khẩu vẫn tới sau khi chuyển mailer (E2E M1 pass).
