# Completed — Overlay OBS (chat trong suốt có theme + alert donate)

Nhánh `feat/stream-overlay` (nexuslive-be, nexuslive-web), đã rebase lên `main` (có guest chat). Xem Working Plan và TSD cùng tên.

## Đã làm
- BE chat-svc: bảng `overlay_settings`, REST đọc/ghi/gửi thử, topic `overlay:{channelId}`, `role:"overlay"` (không tính viewer, không gửi), route Nginx.
- Web: hai trang overlay cho OBS (nền trong suốt), hàng đợi alert, theme chat (4 kiểu, nền trong suốt/màu, phông, cỡ, màu tên, huy hiệu, số tin, tự ẩn), trang Studio → Overlay OBS (link, form, preview nền caro, Gửi thử), i18n vi/en, Vite proxy.

## Kết quả kiểm tra
| Hạng mục | Kết quả |
|---|---|
| Web: `tsc`, `oxlint` (396 file), vitest (44 file, 442 test) | Đạt (chạy trên bản cài tạm, vì `node_modules` mac không chạy được trong VM) |
| Web: `vite build`, Playwright e2e | **Chưa chạy** |
| BE: entity + usecase/overlay test, `go vet`, gofmt | Đạt (module tạm vì VM không tải được module đầy đủ) |
| BE: `go build ./...`, test gateway/ws/restapi/persistent | Đã chạy — xem mục dưới | — chạy `go test ./services/chat/...` và `make pre-commit` (PG: `NXL_TEST_PG_URL_CHAT`) trên máy |
| Swagger (`services/chat/docs`), `docs/api/README.md` | Đã cập nhật — xem mục dưới | (`make swag` rồi cập nhật README) |

## Kiểm tra lại trên máy dev (2026-11-28, sau khi rebase lên `main`)
| Hạng mục | Kết quả |
|---|---|
| Rebase lên `main` (guest chat) | Không xung đột văn bản; `go build`, `go vet` chat: đạt |
| `make format` | Không đổi file |
| `make nxl-lint` | Lần đầu 11 lỗi (dupl, goconst, misspell ×3, mnd ×4, wsl) — đã sửa: helper `readChannelDoc` dùng chung cho GET `chat-settings` / `overlay-settings`, hằng `_detailMax`, hằng mặc định overlay, chính tả US; sau sửa 0 lỗi |
| Migration `20261129000001` | `up → down → up` (cùng `20261128100001` guest chat) trên DB test `nxl_chat_merge_test`: đạt |
| `make nxl-test NXL_TEST_FLAGS=` + repo test chat (`NXL_TEST_PG_URL_CHAT`) | 84 gói đạt; 2 test `billing/config` lỗi vì Makefile nạp `.env` máy dev (`BILLING_PAYMENT_PROVIDER=sepay`) — không liên quan overlay, chạy riêng không có biến đó thì đạt |
| `make nxl-swag` | Đã sinh lại `services/chat/docs`; hoàn tác thay đổi không liên quan ở channel / identity / notification |
| `docs/api/README.md` | Đã thêm 3 endpoint overlay và route edge |

## Việc còn lại
1. Chạy các kiểm tra "chưa chạy" ở trên; `TestOverlayRoleNeedsNoAccount` giả định loại sự kiện `ack` chưa kiểm chứng.
2. Chạy migration `20261129000001`.
3. Thử thật với OBS: Browser Source 450×700 (chat) và 1280×720 (alert), bấm "Gửi thử".
