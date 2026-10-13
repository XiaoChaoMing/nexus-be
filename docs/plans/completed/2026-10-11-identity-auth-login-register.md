# Completed Plan — Đăng ký / đăng nhập: identity-svc + giao diện web

| | |
| --- | --- |
| **Track** | Full |
| **Working Plan** | `../working/2026-10-11-identity-auth-login-register.md` |
| **TSD** | `../technical/2026-10-11-identity-auth-login-register.md` |
| **Ngày** | 2026-10-11 |

## 1. Đã bàn giao

**Backend (`go-clean-template`)**

| Phần | File |
| --- | --- |
| Workspace | `go.work` (root + `services/identity`); `Dockerfile` gốc build với `GOWORK=off` |
| JWT EdDSA dùng chung | `pkg/jwt/eddsa.go` + `eddsa_test.go` (giữ nguyên `Manager` HS256 của app mẫu) |
| identity-svc | `services/identity/`: `cmd`, `config`, `internal/{entity, repo, repo/persistent, usecase, usecase/auth, controller/restapi}`, `Dockerfile` |
| Compose | Service `identity` (profile `nxl`, cổng 8090, chờ `db-migrate`) |
| Makefile | `nxl-up`, `nxl-identity-run`, `nxl-identity-test` |
| Cấu hình | `.env.example`: `NXL_ENV`, `IDENTITY_*` |
| Tài liệu | `README.md` (bảng endpoint), TSD |

**Frontend (`nexuslive-web`)**

| Phần | File |
| --- | --- |
| Dependency | `react-router` 8.4, `react-hook-form` 7.89, `zod` 4.6, `@hookform/resolvers` 5.9 |
| Lớp API | `src/lib/api-client.ts`, `src/lib/api-error.ts` |
| Auth | `src/features/auth/` (api, store, session bootstrap, hooks, schemas, copy, trang Login/Register/Account, guard, user menu) |
| Khung app | `src/app/{router, app-shell, home-page}.tsx`, `src/components/ui/{button, text-field}.tsx`; xóa `src/App.tsx` |
| Cấu hình | Vite proxy `/v1` → `http://localhost:8090`; `.env` và `.env.example` cập nhật cổng |

## 2. Lệch so với kế hoạch

| Lệch | Lý do |
| --- | --- |
| Usecase chưa có `tracing.go` (decorator OpenTelemetry) như quy ước go-clean-template | Môi trường kiểm tra không tải được module OpenTelemetry để biên dịch; thêm ở task sau cùng lúc với tracing cho cả service |
| Test usecase dùng repository giả viết tay (trong bộ nhớ) thay cho mock sinh bằng `mockgen` | Không chạy được `mockgen` ở môi trường kiểm tra; repository giả áp dụng cùng quy tắc unique/thu hồi như schema SQL nên kiểm được luồng xoay vòng thật |
| Không có trang chủ thật — `/` là placeholder lời chào | Ngoài phạm vi (GĐ4) |
| Sau đăng nhập/đăng ký, điều hướng do `GuestOnly` làm (không gọi `navigate` trong trang) | Gọi cả hai làm `GuestOnly` chuyển về `/` trước, mất trang đích — phát hiện qua kiểm tra đầu–cuối |

## 3. Kiểm tra

| Kiểm tra | Môi trường | Kết quả |
| --- | --- | --- |
| `go build`, `go vet` identity-svc + `pkg/*` | Go 1.24, thư viện cùng API nhưng bản cũ hơn (không tải được bản đúng) | Pass |
| `go test -race` `pkg/jwt` (ký/xác minh, JWKS, hết hạn, sai khóa, `alg=none`, HS256 dùng khóa công khai, seed sai) | như trên | Pass |
| `go test -race` usecase (đăng ký trùng hoa/thường, đăng nhập email/username, sai mật khẩu, không tồn tại, khóa, xóa, xoay vòng, dùng lại token → thu hồi tất cả, token rỗng/lạ/hết hạn, logout, argon2) + entity | như trên | Pass |
| API thật với PostgreSQL 18.4 + migration `nxl_identity` (15 lời gọi curl: đăng ký, trùng username/email, validation, sai mật khẩu, không tồn tại, đăng nhập, `/me` có/không token, refresh xoay vòng, dùng lại token cũ, token mới bị thu hồi theo, logout, JWKS) | Binary identity-svc chạy local | Pass — DB: mật khẩu `$argon2id$v=19$m=19456,t=2,p=1`, outbox có `user.registered.v1` |
| `pnpm build` (tsc + vite), `oxlint` | Node 22 | Pass, 0 cảnh báo |
| Đầu–cuối trên Chromium qua Vite proxy (10 bước: lỗi validation, đăng ký, không có token trong `localStorage`, tải lại vẫn đăng nhập, trang Account gọi `/me`, đăng xuất, `/account` khi chưa đăng nhập về `/login`, sai mật khẩu, đăng nhập quay lại `/account`, trùng username báo đúng ô và focus, giao diện mobile 375px) | Playwright | Pass; không có lỗi JavaScript (chỉ log 401 mạng như dự kiến) |
| `go build ./... ./services/identity/...` | Go 1.27.1 (Windows) | Pass |
| `go test -race` identity-svc + `pkg/jwt` (`make nxl-identity-test` tương đương) | Container `golang:1.27` (Windows không có cgo cho `-race`) | Pass sau khi sửa 1 lỗi vet: `uc.l.Warn` truyền key/value cho logger kiểu printf (`usecase/auth/auth.go`) |
| `golangci-lint` root | Container `golang:1.27` | 3 `gci` chỉ do CRLF ở working copy Windows (repo lưu LF), 1 `errcheck` ở `pkg/jwt/eddsa.go:88` |
| `golangci-lint ./services/identity/...` | Container `golang:1.27` | **25 cảnh báo** (18 `gocritic hugeParam/rangeValCopy`, `gocyclo` Refresh = 13, `gochecknoglobals`, `nolintlint` ×2, `gosec G101` false positive, `misspell`, `unparam`) — xem §5 |
| `make nxl-up` (build image `identity`, `db-init`, `db-migrate`) | Docker Desktop 29.4, GNU Make 4.4.1 | Pass — `/healthz` 200 |
| API thật qua Docker (curl: đăng ký 201, trùng 409, validation 400, sai mật khẩu/không tồn tại 401, đăng nhập bằng email 200, `/me` có/không/sai token, refresh xoay vòng, dùng lại token cũ → 401 + thu hồi cả token mới, logout 204) | identity container + PostgreSQL 18.6 | Pass — `credentials.password_hash` là argon2id, outbox có `user.registered.v1`, log reuse in đúng `userID` |
| Đầu–cuối trên Chrome headless qua Vite proxy (11 bước như dòng Playwright ở trên + cookie `nxl_rt` `path=/v1/auth`, `HttpOnly`, `SameSite=Strict`) | Chrome + DevTools Protocol | Pass 11/11, không có lỗi JavaScript |
| App mẫu `make compose-up-all` | — | **Chưa chạy** (ngoài phạm vi kiểm tra đăng ký/đăng nhập) |

## 4. Rủi ro còn lại

- Biên dịch thật với Go 1.27 và bản thư viện trong `go.mod` chưa được chạy; khả năng lỗi thấp (chỉ dùng API ổn định của fiber v2, pgx v5, validator v10, golang-jwt v5) nhưng cần xác nhận.
- `go.work` mới: `make test`, `make linter-golangci` ở root chạy trong workspace mode; nếu lint báo lỗi ở `services/identity`, chạy riêng trong thư mục đó.

## 5. Việc tiếp theo

- [x] Bạn chạy: `make nxl-up` → mở `http://127.0.0.1:8090/healthz`; `cd ../nexuslive-web && pnpm install && pnpm dev` → đăng ký trên `http://localhost:5173/register`.
- [x] Nếu có Go 1.27: `make nxl-identity-test` và `make linter-golangci`; gửi lại lỗi nếu có.
- [ ] Tracing (OpenTelemetry) cho identity-svc; `mockgen` cho repo contracts.
- [ ] Dọn 25 cảnh báo lint của identity-svc (đổi `entity.User`/`Session` sang con trỏ ở contract → track Full; tách `Refresh` để giảm `gocyclo`) và `errcheck` ở `pkg/jwt/eddsa.go`.
- [ ] UI: ô có lỗi server (vd. trùng username) đang focus nên viền hiện màu focus (xanh) thay vì màu lỗi.
- [ ] Xác minh email + quên mật khẩu (Mailpit) — task tiếp theo của GĐ1.
- [ ] Rate limit đăng nhập (theo IP/tài khoản) — hiện chưa có, cần trước khi demo công khai.
- [ ] Worker đẩy `outbox` lên NATS (dùng chung `pkg/`).
- [ ] Viết NXL-D từ §2–§3 của Working Plan và TSD này.
