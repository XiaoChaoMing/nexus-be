# Completed — Overlay OBS (chat trong suốt có theme + alert donate)

Nhánh `feat/stream-overlay` (nexuslive-be, nexuslive-web). **Chưa commit.** Xem Working Plan và TSD cùng tên.

## Đã làm
- BE chat-svc: bảng `overlay_settings`, REST đọc/ghi/gửi thử, topic `overlay:{channelId}`, `role:"overlay"` (không tính viewer, không gửi), route Nginx.
- Web: hai trang overlay cho OBS (nền trong suốt), hàng đợi alert, theme chat (4 kiểu, nền trong suốt/màu, phông, cỡ, màu tên, huy hiệu, số tin, tự ẩn), trang Studio → Overlay OBS (link, form, preview nền caro, Gửi thử), i18n vi/en, Vite proxy.

## Kết quả kiểm tra
| Hạng mục | Kết quả |
|---|---|
| Web: `tsc`, `oxlint` (396 file), vitest (44 file, 442 test) | Đạt (chạy trên bản cài tạm, vì `node_modules` mac không chạy được trong VM) |
| Web: `vite build`, Playwright e2e | **Chưa chạy** |
| BE: entity + usecase/overlay test, `go vet`, gofmt | Đạt (module tạm vì VM không tải được module đầy đủ) |
| BE: `go build ./...`, test gateway/ws/restapi/persistent | **Chưa chạy** — chạy `go test ./services/chat/...` và `make pre-commit` (PG: `NXL_TEST_PG_URL_CHAT`) trên máy |
| Swagger (`services/chat/docs`), `docs/api/README.md` | **Chưa cập nhật** (`make swag` rồi cập nhật README) |

## Việc còn lại
1. Chạy các kiểm tra "chưa chạy" ở trên; `TestOverlayRoleNeedsNoAccount` giả định loại sự kiện `ack` chưa kiểm chứng.
2. Chạy migration `20261129000001`.
3. Thử thật với OBS: Browser Source 450×700 (chat) và 1280×720 (alert), bấm "Gửi thử".
