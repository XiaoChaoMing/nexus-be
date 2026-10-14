# Completed — GĐ3: Chat realtime, realtime gateway, viewer count (M3)

| Working Plan | TSD | Ngày |
| --- | --- | --- |
| `docs/plans/working/2026-10-14-gd03-chat-realtime.md` | `docs/plans/technical/2026-10-14-gd03-chat-realtime.md` v1.1 | 2026-10-14 |

## 1. Đã giao

**Backend (`nexuslive-be`):**

- **chat-svc** (`services/chat`, 2 instance `chat-1` :8095, `chat-2` :8096):
  - Gateway `/v1/ws` theo NXL-00 §10: `auth` (Guest có `anonId`), subscribe/unsubscribe, `seq` theo chủ đề (Redis), bộ đệm resume 200 sự kiện / 5 phút, `resync`, heartbeat, mã đóng `1012/4001/4003/4008/4029`, giới hạn 20 chủ đề, 5 kết nối/người, 20 frame/s.
  - Fan-out giữa instance qua NATS core `rt.<topic>`; sự kiện miền qua JetStream, mỗi sự kiện một instance xử lý.
  - Chat B1–B4: chuẩn hóa nội dung, fragments (emote, mention), badge (Streamer, Mod, VIP, Sub tier), màu tên 12 màu, chống trùng (`clientMsgId` + 30 s), rate limit theo user/IP (mức trần riêng Streamer/Mod), Slow / Followers-only / Subs-only + miễn trừ, ban/timeout (đọc), sampling, kill switch `CHAT_DISABLED`, phòng `OPEN → READ_ONLY` theo vòng đời stream.
  - REST: lịch sử, emote, đọc/sửa chế độ chat (chủ kênh, Mod, Admin).
  - Viewer count: một người = 1 view dù nhiều tab/kết nối (G3-Q6), cập nhật 10 s, đỉnh người xem ghi về stream-svc.
  - Bản sao user/kênh/follow/sub; CLI `chat dev-grant`; job partition `chat_messages`; `/metrics` + dashboard Grafana "NexusLive — Chat".
- **stream-svc:** sự kiện `stream.status_changed/updated/vod_ready.v1` qua outbox, nhận `stream.peak_viewers.v1`, `viewerCount` trong DTO stream.
- `pkg/events` (4 sự kiện mới + payload follow), `pkg/redis`; Nginx (`/v1/ws`, chat REST, `/emotes/`, 8.192 kết nối/worker), compose, Prometheus, CI, Makefile, `.env.example`, README, API catalog v1.5.
- 12 emote WebP theo design pen.dev; k6 `deploy/k6/chat.js`.

**Web (`nexuslive-web`):**

- Realtime client dùng chung cả app (`src/lib/realtime`): auth/re-auth khi refresh token, subscribe đếm tham chiếu, `lastSeq`, bỏ `seq` trùng, backoff 1→30 s ± 20 %, xử lý mã đóng.
- Chat panel theo design GĐ3 (cột desktop, tab tablet/mobile): danh sách ảo hóa (`@tanstack/react-virtual`), tối đa 150 tin trong DOM, cuộn thông minh + "Có tin mới", optimistic send + gửi lại, emote picker, gợi ý @mention, cài đặt chế độ chat (Streamer/Mod), mode bar, mọi trạng thái lỗi/khóa ở brief GĐ3, Guest → modal đăng nhập.
- Viewer count trên player, ChannelInfo, thẻ live và Hero; trạng thái stream qua `stream:` (polling chỉ làm dự phòng).
- E2E `e2e/m3.spec.ts`.

**Tài liệu (`DATN/docs`):** NXL-00 v1.3, NXL-B v1.2, **NXL-F v1.0** (kiểm duyệt), **NXL-E v1.0** (thông báo), DL-17/DL-18,
errata E-17…E-21, ma trận truy vết v1.4, brief design GĐ3, phiếu đo tải `report/measurements/2026-10-chat-load.md`.

## 2. Lệch so với plan / TSD

TSD v1.1 §12. Chính: subject `rt.<topic>`; thêm bảng `chat.channels`; không thêm migration `peak_viewers` (cột đã
có); thêm biến rate limit; `chat-2` dùng cổng 8096; emote dùng ảnh design; chưa có badge Verified.

## 3. Sự cố

- `pkg/httpmetrics`: `/metrics` trả 500 ở mọi service khi một route có cả GET và PUT (nhãn trỏ vào buffer Fiber dùng lại) → sửa + test hồi quy.
- Nginx 1.024 kết nối/worker chỉ chịu ~500 WebSocket → nâng 8.192.
- E2E M3 lỗi ngắt quãng: mở tab thứ hai lâu hơn cửa sổ Slow mode 10 s → mở tab trước khi gửi tin đầu.
- Script k6 dùng chung một tài khoản người gửi → chọn theo iteration của scenario.

## 4. Kiểm thử

| Lệnh | Kết quả |
| --- | --- |
| `go test -race` 6 module (root, identity, channel, stream, media, chat) với PG + Redis thật | pass (chat use case 87 %) |
| `golangci-lint` 6 module | 0 lỗi |
| `swag init` chat, stream | khớp bản commit |
| Migration chat up → down → up | pass |
| Web `pnpm lint`, `tsc -b`, `vitest`, `build` | pass — 253 test |
| E2E web trên stack cục bộ (2 chat-svc, Redis, NATS, Nginx) | 9/9 pass (guest-modal ×4, livestream, m1, m2 ×2, **m3**); m3 chạy lại 3 lần liên tiếp pass |
| Smoke WebSocket qua edge (`/tmp/claude-0/smoke/*.mjs`) | 3 script, mọi kiểm tra pass: gửi/nhận ~13 ms, Slow mode, 403 người xem sửa chế độ, viewer_count = 3 (một người 2 kết nối tính 1), tắt :8095 → nối lại :8096 + resume không thiếu `seq`, kết thúc → READ_ONLY, `peak_viewers`, INTERRUPTED/LIVE, `vod_ready`, Followers-only + `dev-grant` |
| Hostile | không `auth` → 4008, token sai → 4001, Origin lạ → 403, GET thường → 426, `/metrics` qua edge → 404 |
| k6 1.000 kết nối (sandbox 2 vCPU, chung ffmpeg) | 0 kết nối lỗi, đủ 340.000 lượt giao; fan-out phía server 100 % < 250 ms; P95 đo ở k6 5,4 s do thiếu CPU → **chưa đạt, cần đo lại trên máy demo** |

| Luồng cũ chạy lại | Kết quả |
| --- | --- |
| M1 (đăng ký → đơn Creator → Admin duyệt → Studio) | pass |
| M2 (Go Live → xem → gián đoạn → kết thúc → VOD + tiến độ) | pass |

Chưa chạy: Docker compose (`make nxl-up-all`) và k6 trên máy ông chủ; `go.sum` cho module mới viết tay — ông chủ chạy
`make deps` một lần.

## 5. Việc tiếp theo

- Ông chủ: `make deps`, `make nxl-up-all`, chạy k6 trên máy demo (k6 trên máy khác) và điền phiếu đo.
- Chốt: bảng 12 màu tên (server hiện dùng bảng của chat-svc, design có bảng khác); Shift+Enter (NXL-B BR03 cho xuống
  dòng, design muốn 1 dòng).
- GĐ4: follow thật (thay `dev-grant`), hồ sơ kênh, category, lịch phát; badge Verified.
- GĐ5: thao tác Mod (NXL-F), notification-svc + chủ đề `user:` (NXL-E), errata E-17…E-21.
