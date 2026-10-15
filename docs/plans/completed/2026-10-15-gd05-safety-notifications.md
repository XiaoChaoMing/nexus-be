# Completed — GĐ5: An toàn & Thông báo

| Working Plan | TSD | Ngày |
| --- | --- | --- |
| `docs/plans/working/2026-10-15-gd05-safety-notifications.md` | `docs/plans/technical/2026-10-15-gd05-safety-notifications.md` v1.1 | 2026-10-16 |

## 1. Đã giao

**Backend (`nexuslive-be`) — phần A (kiểm duyệt, báo cáo, Admin, HLS có chữ ký):**

- **`pkg`:** sự kiện GĐ5 (chỉ thêm), `eventbus.DeliverNew` / `DeleteConsumer`, `pkg/hlssign` (token `secure_link`).
- **chat-svc:** xóa / ghim tin, timeout / ban (và gỡ), danh sách hạn chế, thẻ người dùng, từ khóa chặn + chặn link
  (`CHAT_CONTENT_BLOCKED`), vai trò Mod / VIP (thay `dev-grant` ngoài dữ liệu seed), `/v1/me/channel-roles`; realtime `delete`,
  `user_cleared`, `user_restored`, `pin`, `unpin`, snapshot `me.canModerate` / `me.restriction`; `user.locked` đóng socket `4003`
  trên mọi instance; API nội bộ bằng chứng; sự kiện mod / settings / role / audit qua outbox.
- **safety-svc (mới, :8099):** `POST /v1/reports` (trùng → `200`, 10 / giờ, bằng chứng ≤ 300 ms), hàng đợi Admin, chi tiết,
  nhận / xử lý / bỏ qua (`applyToSameTarget`), khóa / mở tài khoản, khóa / mở kênh, gỡ buổi phát (`202 {actionId}`), tra cứu
  người dùng / kênh trên bản sao, audit log chỉ-thêm; bản sao user / channel / stream + backfill từ snapshot nội bộ.
- **identity:** chặn người dùng, khóa Admin (`lock_kind`), `GET /v1/admin/users/{id}`, snapshot nội bộ, `SECURITY_LOCK`,
  `audit.recorded`. **channel-svc:** khóa / mở kênh, Studio ghi → `403 CHANNEL_SUSPENDED`, snapshot nội bộ. **stream-svc:**
  gỡ buổi phát (`admin_takedown`, xóa VOD), kênh bị khóa (authorize / playback / Studio), URL HLS có chữ ký.
- Nginx (route GĐ5, `^~ /hls/t/` + `secure_link`, `NXL_HLS_ALLOW_UNSIGNED`), compose, Prometheus, Swagger.

**Backend — phần B (thông báo):** notification-svc (:8097): bản sao user / channel / follow / block (durable
`notification-replica` DeliverAll + backfill), nguồn thông báo (durable `notification-svc` DeliverNew) với fan-out theo lô,
cooldown live, gộp `MENTION` / `NEW_FOLLOWER`, giới hạn email / ngày, chỉ email địa chỉ đã xác minh; REST trung tâm thông
báo + cài đặt + hủy nhận (HMAC); email outbox + worker; email tài khoản chuyển từ identity (`IDENTITY_MAILER_ENABLED`,
`NOTIFICATION_AUTH_MAILER_ENABLED`, `make nxl-mailer-cutover` / `nxl-mailer-rollback`); API catalog v1.7.

**Web (`nexuslive-web`):** công cụ Mod trong chat (menu, thẻ người dùng, tin ghim, trạng thái bị hạn chế đếm ngược), báo
cáo, chặn / ẩn, `/moderation/:channel`, Studio → Cộng đồng, chuông + `/notifications` + cài đặt + hủy nhận, màn khóa tài
khoản / kênh tạm ngưng / buổi phát bị gỡ, phát HLS có chữ ký (403 → mở lại phiên). Chi tiết:
`../nexuslive-web/docs/plans/completed/2026-10-15-gd05-safety-notifications.md`.

**Admin (`nexuslive-admin`):** Báo cáo (hàng đợi, lọc, chi tiết + bằng chứng + lịch sử, Nhận xử lý, hộp thoại Xử lý gồm
hành động), Người dùng (khóa / mở), Kênh (tạm ngưng / mở lại), Buổi phát (gỡ), Nhật ký hệ thống; E2E `moderation`.

**Close-out (bản này, Lightweight — sửa lỗi phía web / admin báo, hợp đồng chỉ thêm trừ refresh):**

| # | Sửa | Nơi |
| --- | --- | --- |
| 1 | `NEW_FOLLOWER.link` = `/studio` | notification |
| 2 | `403 CHANNEL_SUSPENDED` của hồ sơ kèm `details {handle, displayName, avatarUrl?}` → web hiện tên + ảnh | channel, web |
| 3 | Refresh của tài khoản bị khóa → `403 ACCOUNT_LOCKED` (+ `lockedUntil`) thay `401 SESSION_EXPIRED`; khóa Admin cho phiên **hết hạn** thay vì xóa; `Rotate` không xoay phiên đã hết hạn → web giữ màn khóa sau khi tải lại | identity, web |
| 4 | `UserCard.createdAt` từ `Nxl-Occurred-At` của `user.registered.v1` (bản sao chat, không migration) | chat |
| 5 | `evidence[].username` / `displayName` | safety, admin |
| 6 | `AdminReportDetail.channelId` → admin không cần gọi `GET /v1/streams/{id}` để tìm kênh | safety, admin |
| 7 | `actions[]` gồm `CHAT_TIMEOUT` / `CHAT_UNTIMEOUT` / `CHAT_BAN` / `CHAT_UNBAN` từ audit log (+ `actorRole`, `channelId`) | safety, admin |
| 8 | `GET /v1/admin/audit-log?actor=<username>` (admin vẫn tự tra id để báo "không có người này") | safety |
| 9 | `sameTargetCount` gồm cả chính report — giữ, ghi TSD §16 | — |

## 2. Lệch so với plan / TSD

TSD v1.1 §16. Tóm tắt:

- **Phần A:** khóa Admin ban đầu xóa phiên (refresh → `401`), close-out đổi thành phiên hết hạn + refresh `403 ACCOUNT_LOCKED`
  (lệch NXL-00 §7/§13, NXL-D E-D05, NXL-F BR-F20 / AF-F11 — cần errata ở `docs`); compose để chữ ký HLS **tắt** mặc định
  (`STREAM_HLS_SIGNING=false`, `NXL_HLS_ALLOW_UNSIGNED=1`); khi bật, `manifestUrl` công khai = `""`.
- **Phần B:** `email_outbox` unique `(user_id, source_event_id)`; worker lease 5 phút (`FOR UPDATE SKIP LOCKED`); bản sao ghi
  trong lúc backfill với `synced_at`; `NOTIFICATION_VERIFY_TTL` / `NOTIFICATION_RESET_TTL`; `MENTION` đã gộp không gửi email lần
  hai; thông báo gỡ buổi phát lấy `title` từ payload `safety.stream_takedown.v1`; sự kiện tới trước bản sao (seed) → bỏ người
  nhận + `warn` (E-E11).
- **Web:** toast `sonner` toàn cục thay vì neo cột chat; "⋯" thông báo trên mobile là dropdown; Timeout tùy chỉnh là popover;
  "Tài khoản & bảo mật" là anchor `#security`; ngày trên màn khóa theo `Intl`; hồ sơ tạm ngưng trước close-out chỉ có handle
  (đã sửa); `NEW_FOLLOWER` cũ (`/studio/dashboard`) vẫn được web đổi hướng sang `/studio`.
- **Admin:** không có frame design trên pen.dev — dựng theo brief GĐ5 + token; lịch sử hạn chế chat lấy từ mọi kênh của người
  bị báo cáo (dòng có `channelId`), không chỉ kênh của report.
- `NEW_FOLLOWER` link `/studio` khác NXL-E §7.4 (`/studio/dashboard`) — cần errata.

## 3. Sự cố

- Web / admin dựa vào workaround: tra kênh qua stream (admin), chỉ hiện handle khi kênh tạm ngưng, mất màn khóa sau tải lại
  (refresh `401` → Khách) — các workaround này được gỡ / giữ làm dự phòng trong close-out.
- `Audit.List` vượt `gocyclo` sau khi thêm bộ lọc `Actions` → tách `auditWhere`.
- Bản sao `chat.users.created_at` trước đây là thời điểm chat nhận sự kiện; dòng cũ giữ nguyên (upsert lấy giá trị sớm nhất,
  sự kiện phát lại có `Nxl-Occurred-At` sẽ sửa).

## 4. Kiểm thử

| Lệnh | Kết quả |
| --- | --- |
| `golangci-lint run` (`make nxl-lint`) — root + 7 service | 0 lỗi |
| `golangci-lint fmt` (`make format`) | sạch |
| `go test -race -covermode atomic ./...` mọi module (PG18 + Redis thật: `NXL_TEST_PG_URL_*`, `NXL_TEST_REDIS_ADDR`) | pass |
| `swag init` identity, channel, safety (chat không đổi) | đã sinh lại, diff chỉ ở phần đã sửa |
| Migration | không có migration mới (không chạy up → down → up) |
| Web `pnpm lint`, `tsc -b`, `vitest`, `pnpm build` | pass — 352 test |
| Admin `pnpm lint`, `tsc -b`, `vitest`, `pnpm build` | pass — 99 test |
| Stack cục bộ `FRESH=1 up.sh` (9 healthz `200`, `STREAM_HLS_SIGNING=true`) | pass |
| Admin E2E `pnpm e2e moderation` | **3/3 pass** |
| Web E2E toàn bộ (`npx playwright test`) | **15/15 pass** (16 phút) ở lượt thứ hai; lượt đầu 14/15 — `m3` hụt bước chế độ chậm (tin "Slow 1" không hiện trong 20 s, máy 2 CPU tải cao), chạy riêng pass, không liên quan thay đổi; `m4-moderator` kiểm thêm tải lại trang khi bị khóa |
| Probe qua edge: khóa → refresh `403 ACCOUNT_LOCKED` (lặp lại vẫn `403`), chi tiết report có `LOCK_USER` + `CHAT_TIMEOUT` (`actorRole OWNER`, `channelId`), report kênh có `channelId`, hồ sơ kênh tạm ngưng có `details`, mở lại → `200`, `UserCard.createdAt` có giá trị, audit `actor=@admin` / người không tồn tại → trang rỗng | pass |
| `secure_link`: URL ký hợp lệ → `200`; sửa md5 → `403`; ký với hạn đã qua → `403`; URL không ký `/hls/streams/...m3u8` → `403` | pass (`200` / `403` / `403` / `403`, VOD qua `POST /v1/vods/{id}/playback`) |

Luồng cũ chạy lại qua edge: đăng ký / đăng nhập / refresh (xoay vòng), khóa → đăng nhập `403`, hồ sơ kênh, follow, chat,
timeout / gỡ timeout, báo cáo, Admin khóa / tạm ngưng / mở lại / gỡ (E2E admin + web).

Chưa chạy: Docker compose trên máy ông chủ (`make nxl-up-all`), `make nxl-swag` bằng Docker (đã chạy `swag` cục bộ cùng
tham số), `make deps-audit`, k6.

## 5. Việc tiếp theo

- Ông chủ: `make deps` (+ `make deps-audit`); đặt `NXL_HLS_SECRET` thật rồi bật chữ ký theo TSD §9.3
  (`STREAM_HLS_SIGNING=true` → `NXL_HLS_ALLOW_UNSIGNED=0`); đặt `NOTIFICATION_UNSUBSCRIBE_SECRET` thật; `make nxl-mailer-cutover`
  trên stack compose; chạy k6 trên máy demo.
- `docs`: errata cho refresh `403 ACCOUNT_LOCKED` khi tài khoản bị khóa (NXL-00 §7 / §13, NXL-D E-D05, NXL-F BR-F20 / AF-F11),
  `details` của `CHANNEL_SUSPENDED` (NXL-D §13), link `NEW_FOLLOWER` (NXL-E §7.4), trường mới của `AdminReportDetail` (NXL-F §13).
- GĐ6: billing-svc + SePay sandbox (NXL-H).
