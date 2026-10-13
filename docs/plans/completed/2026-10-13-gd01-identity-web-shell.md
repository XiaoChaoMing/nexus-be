# Completed — GĐ1: Identity, Creator, kênh tối thiểu, Web shell, Web quản trị

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full | Đã triển khai, kiểm thử trên stack cục bộ — chờ ông chủ chạy `make nxl-up-all` trên máy | 2026-10-13 |

Working Plan v1.1 và TSD v1.1 cùng basename. Ông chủ duyệt plan ngày 13/10 và yêu cầu làm luôn; G1-Q1…Q6 lấy
theo đề xuất (TSD đầu trang).

## 1. Đã giao

| Gói | Kết quả |
| --- | --- |
| Chung | `pkg/eventbus`: header `Nxl-Occurred-At`, `Nxl-Producer`; `Bus.PublishAt`; outbox dùng `created_at` làm thời điểm. `pkg/events`: 7 sự kiện mới + payload |
| WP1 identity | Migration `20261014000001_gd01_account`; `acceptTerms`; xác minh email + gửi lại (60 s, 5/giờ); quên/đặt lại mật khẩu; khóa tạm 10 lần/15 phút; `GET/PATCH /v1/me`; `POST /v1/me/password`; CSRF `X-NXL-Client` cho refresh/logout; consumer `identity-mailer` gửi email (template vi/en nhúng) qua SMTP Mailpit; seed dev `admin` + `creator`; lệnh `identity promote-admin`; log `audit=…` |
| WP2 | Đơn Creator (nộp, xem, hủy) + API Admin (danh sách cursor, chi tiết, duyệt, từ chối) với `RequireAdmin`; `user_channels` từ `channel.created.v1` → `isCreator`, `channelId`, `roles` |
| WP3 channel-svc | Service mới cổng 8094: consumer `creator.approved.v1` tạo kênh idempotent (handle = username, trùng thêm số), `stream.started/ended` cập nhật `isLive`; `GET /v1/channels/{handle}`; Dockerfile, `/metrics`, Swagger |
| WP4 stream-svc | Bảng `creator_channels` từ `channel.created.v1`; Studio `403 NOT_CREATOR`; `channel {id, handle, displayName, avatarUrl}` trên stream, VOD, Studio |
| WP5 cổng vào | Rate limit auth/ghi/đọc → `429 RATE_LIMITED` + `Retry-After`; CORS 2 origin, `/v1/admin` chỉ origin admin; header bảo mật; route `/v1/channels`; cấu hình qua template envsubst (`NXL_*`) |
| Hạ tầng | Compose (channel, env identity, nginx templates), Makefile (`nxl-channel-test`, `nxl-promote-admin`), CI (module channel), Prometheus, `.env.example`, README |
| WP6 web | Modal đăng nhập/đăng ký + `pendingAction` chạy đúng 1 lần; Shell theo NXL-C (top bar Guest, sidebar Kênh đề xuất, bottom nav 5 mục); trang xác minh, quên/đặt lại mật khẩu, cài đặt tài khoản; trang hệ thống (404, 500, phiên hết hạn, tài khoản khóa, offline, 429); `/creator/apply`; tên/avatar kênh; liên kết "Trang quản trị" cho Admin; i18n vi + en |
| WP6b `nexuslive-admin` | Project mới (cổng 5174): đăng nhập Admin, chặn tài khoản không phải Admin, hàng đợi đơn Creator (lọc, cursor, chi tiết, duyệt/từ chối có xác nhận), i18n vi + en, CI, E2E |
| WP7 tài liệu | PRD v2.1, NXL-A/B/C v1.1, NXL-00 v1.2, NXL-D v1.2, NXL-G1 v1.0 (mới), errata v1.3, ma trận v1.2, decision log v1.3 (DL-15, DL-16), ERD, ADR-013, kiến trúc v1.1, API catalog v1.3, roadmap v1.1 |

## 2. Lệch so với plan / TSD

| # | Lệch | Lý do |
| --- | --- | --- |
| 1 | Đổi/đặt lại mật khẩu **xóa** phiên cũ thay vì đánh dấu thu hồi | Phiên bị thu hồi mà thiết bị cũ refresh lại sẽ bị coi là dùng lại token và thu hồi luôn phiên vừa đổi mật khẩu |
| 2 | `details.retryAfterSec` là chuỗi (`"42"`) | `details` là `map[string]string` theo NXL-00 §4; header `Retry-After` vẫn là số |
| 3 | Tuổi và "không ở tương lai" tính theo ngày `Asia/Ho_Chi_Minh` | Người dùng Việt Nam; dự phòng UTC |
| 4 | Biến seed là `IDENTITY_DEV_SEED` + `IDENTITY_DEV_SEED_PASSWORD` (mặc định `nexuslive123`), không phải `IDENTITY_DEV_ADMIN_PASSWORD` | Một mật khẩu cho cả hai tài khoản seed |
| 5 | Studio tra kênh một lần ở middleware nhóm `/studio` | Mọi route Studio cần cùng kiểm tra |
| 6 | `/v1/auth/verify-email` cũng nằm trong vùng rate limit auth | Chống dò token |
| 7 | Chat-svc dời sang cổng 8095 trong tài liệu | channel-svc dùng 8094 |
| 8 | Web: `/browse`, `/search` là trang tạm (GĐ2); chưa có Ctrl+K | Ngoài phạm vi GĐ1 |
| 9 | R5 (design Auth/Settings) theo token Obsidian Pulse | Chưa có frame pen.dev (G1-Q4) |

## 3. Vấn đề gặp

- Trang `/creator/apply` không tự mở Studio khi kênh có quá nhanh (component chờ bị gỡ trước khi điều hướng) — E2E
  M1 bắt được, đã sửa.
- Hai dev server Vite dùng chung `node_modules/.vite` gây `504 Outdated Optimize Dep` — chỉ là môi trường kiểm thử.
- `channel.created.v1` trỏ kênh đã thuộc user khác bị giao lại 20 lần → identity bỏ qua bằng `ErrSkip`.

## 4. Kiểm thử

Docker không chạy được trong môi trường của em, nên stack được dựng **không dùng Docker**: PostgreSQL 18, NATS
JetStream, Mailpit, S3 giả lập nhỏ (thay MinIO), 4 service build từ mã, Nginx với cấu hình render từ template
(chỉ đổi host upstream sang `127.0.0.1`), Vite web :5173 và admin :5174 qua cổng vào :8080.

| Lệnh | Kết quả |
| --- | --- |
| `golangci-lint run` (root + 4 service) | 0 issues |
| `go test -race` (root + channel, identity, stream, media) | pass |
| Repo test PG: identity (7), channel, stream (8) | pass |
| Migration up → down → up: identity, stream, channel | pass |
| `swag init` identity, stream, channel | pass |
| Web: `tsc -b`, `oxlint`, `vitest` (105), `vite build` | pass |
| Admin: `tsc -b`, `oxlint`, `vitest` (51), `vite build` | pass |
| E2E web (stack mới, DB trống): guest modal ×4, livestream, M1 | 6/6 pass |
| E2E admin: duyệt đơn, tài khoản thường bị chặn | 2/2 pass |
| `nginx -t` với cấu hình render | pass |
| `docker compose --profile nxl --profile obs config -q` | pass |

| Luồng / hostile path | Kết quả |
| --- | --- |
| M1: đăng ký trong modal → email Mailpit → xác minh → nộp đơn → Admin duyệt → kênh → Studio → phát ffmpeg → tên kênh trên trang xem | pass |
| Livestream cũ (Creator seed): phát, xem, kết thúc, VOD | pass |
| Token xác minh dùng lại | `400 TOKEN_INVALID` |
| Sai mật khẩu 10 lần | lần 10 `403 ACCOUNT_LOCKED` + `lockedUntil`; đúng mật khẩu vẫn bị khóa |
| Refresh thiếu `X-NXL-Client` | `403 FORBIDDEN` |
| Viewer gọi `/v1/admin/*` / Studio | `403 FORBIDDEN` / `403 NOT_CREATOR` |
| Duyệt đơn lần 2 | `409 INVALID_STATE` |
| channel-svc tắt khi duyệt → bật lại | kênh được tạo, `/v1/me` có `CREATOR` |
| Login qua cổng vào 12 lần/phút | 6 × 401 rồi `429` + `Retry-After: 60` |
| CORS: origin lạ / web gọi `/v1/admin` / admin gọi `/v1/admin` | không có ACAO / không có / có |
| `/metrics`, `/internal/*` qua cổng vào | `404` |

**Chưa chạy (cần ông chủ chạy trên máy có Docker):**

- `make nxl-up-all`, build image, Nginx thật trong container (envsubst của image).
- `make nxl-swag` bản Docker (em chạy swag trực tiếp).
- `go generate` mock (máy em không có mockgen).

## 5. Việc tiếp theo

- Ông chủ: `make nxl-down && make nxl-up-all`, chạy E2E web và admin (`pnpm install` trong `nexuslive-admin`
  lần đầu), `make deps`.
- E2E chạy nhiều lần liền có thể chạm giới hạn 10 lần đăng nhập/phút: đặt `NXL_EDGE_AUTH_RATE=600r/m` khi chạy E2E.
- Khi có frame pen.dev cho Auth/Settings/Admin: chỉnh giao diện (R5).
- GĐ5: chuyển `identity-mailer` sang notification-svc; xem lại package UI dùng chung web/admin.

## 6. Bổ sung sau khi giao (13/10)

| Việc | Kết quả |
| --- | --- |
| Giới hạn gửi lại email / quên mật khẩu kiểm tra rồi mới ghi (hai request cùng lúc có thể cùng lọt) | `TokenRepo.IssueLimited`: khóa dòng `users` (`FOR UPDATE`) rồi kiểm tra + ghi trong một transaction. Repo test 8 request đồng thời → đúng 1 token (test fail khi bỏ khóa); chạy thật: 3 lần "quên mật khẩu" cùng lúc → 1 email |
| Gói seed dev chưa có unit test | `bootstrap_test.go`: tạo 2 tài khoản + đơn duyệt một lần, chạy lại không đổi, lỗi hash/DB, `promote-admin` |
| stream-svc trả thời gian theo múi giờ máy chủ | Mọi mốc thời gian của API stream (stream, VOD, Studio, key, health) đổi sang UTC; channel-svc cũng vậy |
| Tài liệu còn cũ | `data-model.md` v1.2; UC-* trỏ PRD v2.1, NXL-D v1.2, NXL-G1 v1.0; xuất lại ảnh ERD identity/stream/channel/tổng quan/bảng chứng; đánh dấu xong việc trong tổng kết GĐ0 |

Kiểm tra lại: `golangci-lint` 0 issues, `go test -race` identity + stream, repo test PG identity (`-count=3`) + stream.
