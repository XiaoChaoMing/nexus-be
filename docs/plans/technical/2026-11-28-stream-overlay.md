# TSD — Overlay OBS (chat trong suốt có theme + alert donate)

Working Plan: `docs/plans/working/2026-11-28-stream-overlay.md`. Phạm vi: chat-svc (BE) + nexuslive-web. Nhánh: `feat/stream-overlay` (cả hai repo).

## 1. Quyết định
| # | Quyết định | Lý do |
|---|---|---|
| D1 | Overlay là trang web `/overlay/chat/:handle`, `/overlay/alerts/:handle`, nạp bằng OBS Browser Source; không tài khoản | Demo đơn giản, nền trong suốt bằng CSS |
| D2 | Kết nối `/v1/ws` với frame `auth {token:null, anonId, role:"overlay"}`; server đặt `Principal.Overlay` | Overlay chỉ đọc, **không tính viewer**, không gửi được |
| D3 | Cài đặt lưu server (`overlay_settings`, jsonb `chat`/`alert`), công khai khi đọc, chỉ owner/admin ghi | OBS không có phiên đăng nhập; đổi theme áp dụng ngay |
| D4 | Lưu → publish `overlay_settings` trên topic `overlay:{channelId}`; "Gửi thử" → `overlay_test_gift` cùng topic | Không cần tải lại nguồn trong OBS |
| D5 | Alert thật lấy từ sự kiện `system` kind `gift` của `chat:{streamId}` (đã có từ donate) | Không đổi donate-svc |
| D6 | Link dùng **handle**; overlay poll profile 5 s (30 s khi đã live) để lấy `currentStreamId` | `GET /v1/channels/{x}` chỉ nhận handle |
| D7 | Preview trong Studio render cùng component với dữ liệu mẫu (không iframe) | Chạy được khi chưa live |

## 2. Backend (chat-svc)
- Migration `20261129000001_overlay_settings` (`channel_id` PK, `chat`, `alert`, `updated_by`, `updated_at`).
- `entity/overlay.go`: enum/giới hạn + `Validate()` (details `chat.fontSize`…); `topic.go`: `TopicOverlay`; `Principal.Overlay`.
- `usecase/overlay`: `Get`, `Update` (ErrUnauthenticated cho khách, owner/admin, lưu rồi publish; lỗi publish chỉ cảnh báo), `TestGift` (owner/admin, 1 lần/giây/kênh → `RATE_LIMITED`/429).
- `usecase/gateway`: topic `overlay` công khai; overlay không `viewers.Join/Leave`. `ws/client.go`: `role:"overlay"` chỉ khi không có token; `send` bị `FORBIDDEN`.
- REST: `GET|PUT /v1/channels/{channelId}/overlay-settings`, `POST /v1/channels/{channelId}/overlay-test` (204); Nginx regex route thêm `overlay-settings|overlay-test`.

## 3. Web
- `lib/realtime`: `RealtimeAuth.role`, `ClientFrame.auth.role`. `main.tsx`: đường dẫn `/overlay/*` bỏ `initAuth`, cấu hình realtime khách `role:"overlay"`.
- `features/overlay`: types/parse (giá trị sai → mặc định, kẹp theo giới hạn server), `theme.ts`, hooks (`useOverlayChannel`, `useOverlaySettings`, `useAlertQueue`, `useTransparentPage`), `OverlayChatView`, `OverlayAlertView`, 2 trang.
- `studio/overlay-page.tsx` + mục nav "Overlay OBS" + i18n vi/en (`studio:overlay.*`); Vite proxy `overlay-settings|overlay-test` → chat-svc.

## 4. Kiểm thử
- BE: `entity/overlay_test.go`, `usecase/overlay/overlay_test.go`, `persistent/overlay_test.go` (cần PG), `ws_test.go`, `gateway_test.go`.
- Web: `overlay-lib.test.ts`; toàn bộ vitest, tsc, oxlint.
