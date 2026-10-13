# Working Plan — Đăng ký / đăng nhập: identity-svc + giao diện web

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| **Full** (service mới, xác thực, hợp đồng API mới, hai repo) | Approved — đã triển khai | 2026-10-11 |

## 1. Task Brief

- **Mục tiêu:** người dùng mở web, đăng ký bằng username + email + mật khẩu, đăng nhập, được giữ phiên khi tải lại trang, và đăng xuất. Đây là phần đầu của GĐ1 (roadmap) và UC-D01, UC-D03, UC-D06.
- **Nguồn yêu cầu:**
  - Mô tả trong chat.
  - PRD FR-AU-01, FR-AU-04.
  - Use case UC-D01/D03/D06.
  - ERD `identity` (bảng `users`, `credentials`, `sessions` — đã migrate).
  - ADR-008 (JWT ký bất đối xứng, refresh token xoay vòng).
  - Spec NXL-D **chưa viết**, nên plan này là nguồn yêu cầu tạm và sẽ được chép vào NXL-D sau.
- **Trong phạm vi:**
  - **Backend — identity-svc:** 5 endpoint register / login / refresh / logout / me, JWKS, middleware xác minh JWT.
  - **Frontend — nexuslive-web:** router, trang Đăng ký, trang Đăng nhập, lớp gọi API, giữ phiên, header hiển thị người dùng, đăng xuất.
  - **Ghép nối:** chạy FE qua Vite proxy tới identity-svc.
- **Ngoài phạm vi (task sau):**
  - Xác minh email và quên mật khẩu (cần SMTP/Mailpit).
  - Guest mode quay lại ngữ cảnh sau khi đăng nhập (FR-AU-04), đăng ký Creator, Google OAuth.
  - Rate limit ở Gateway.
  - Chuyển cả repo sang monorepo.
- **Thư mục sẽ đụng tới:**
  - `go-clean-template/services/identity/` (code mới), `go.work`, `docker-compose.yml`, `.env.example`, `Makefile`
  - `nexuslive-web/src/`, `nexuslive-web/package.json`

## 2. Hợp đồng API (identity-svc, tiền tố `/v1/auth`)

| Method | Path | Body | Thành công | Lỗi |
| --- | --- | --- | --- | --- |
| POST | `/v1/auth/register` | `{username, displayName?, email, password}` | 201 `{user}` + đăng nhập luôn (access token + cookie refresh) | 400 `VALIDATION_FAILED`, 409 `USERNAME_TAKEN` / `EMAIL_TAKEN` |
| POST | `/v1/auth/login` | `{login, password}` — `login` là email hoặc username | 200 `{accessToken, expiresIn, user}` + cookie refresh | 400, 401 `INVALID_CREDENTIALS`, 403 `ACCOUNT_LOCKED` |
| POST | `/v1/auth/refresh` | — (đọc cookie) | 200 `{accessToken, expiresIn, user}` + cookie refresh **mới** | 401 `SESSION_EXPIRED` |
| POST | `/v1/auth/logout` | — | 204, xóa cookie, thu hồi session | — |
| GET | `/v1/auth/me` | — (Bearer) | 200 `{user}` | 401 `UNAUTHORIZED` |
| GET | `/v1/auth/.well-known/jwks.json` | — | 200 JWKS (khóa công khai Ed25519) | — |

- **Envelope:**
  - Thành công: `{ "data": … }`.
  - Lỗi: `{ "error": { "code", "message", "details"? } }`.
  - Mã lỗi `UPPER_SNAKE_CASE` (errata E-02, E-03). Đây là đề xuất đầu tiên cho NXL-00.
- **`user` trả về:** `id, username, displayName, email, emailVerified, globalRole, createdAt`. Không bao giờ trả `password_hash`.
- **Kiểm tra dữ liệu:**
  - `username`: 3–25 ký tự `[A-Za-z0-9_]`.
  - `email`: hợp lệ, tối đa 254 ký tự.
  - `password`: 8–72 ký tự, có chữ và số.
  - `displayName`: tối đa 50 ký tự, mặc định bằng `username`.

## 3. Thiết kế

### Token và phiên (ADR-008)

- **Access token:** JWT EdDSA (Ed25519), sống 15 phút. Claims: `sub` (user id), `role` (`USER`/`ADMIN`), `sid` (session id), `iss`, `exp`, `iat`. Trình duyệt chỉ giữ trong bộ nhớ, không ghi `localStorage`.
- **Refresh token:**
  - 32 byte ngẫu nhiên, sống 30 ngày.
  - Gửi qua cookie `nxl_rt`: `HttpOnly; SameSite=Strict; Path=/v1/auth`, thêm `Secure` khi không phải dev.
  - Database chỉ lưu bản băm SHA-256 trong `sessions.refresh_token_hash`.
- **Xoay vòng:**
  - Mỗi lần `refresh` thu hồi session cũ và tạo session mới.
  - Nếu một refresh token đã bị thu hồi mà vẫn được dùng lại (dấu hiệu bị đánh cắp), thu hồi mọi session của user đó.
- **Mật khẩu:** argon2id, lưu trong `credentials` (tách khỏi `users` theo ERD).
- **Khóa ký:**
  - Dev: tự sinh khi khởi động nếu thiếu `IDENTITY_JWT_PRIVATE_KEY`, kèm cảnh báo trong log.
  - Môi trường khác: bắt buộc phải có khóa.
- **Chống dò tài khoản:** login sai email và sai mật khẩu trả cùng một lỗi `INVALID_CREDENTIALS`, thời gian phản hồi tương đương nhau.

### Backend — cấu trúc

- **Vị trí:** `services/identity/` là một Go module mới theo khuôn go-clean-template (ADR-002):
  - `cmd/identity`, `config`
  - `internal/{entity, usecase, repo/persistent, controller/restapi/v1}`, `internal/app`
- **Phần dùng chung:** dùng lại `pkg/` của repo gốc (logger, postgres, httpserver, tracing) qua `go.work`.
- **Gói mới:** `pkg/jwt` thêm kiểu `EdDSA` (sign + verify + JWKS) và giữ nguyên `Manager` HS256 cũ cho app mẫu.
- **Chỉ có REST:** gRPC, AMQP và NATS chưa cần cho task này.
- **Dữ liệu:** `users` + `credentials` ghi trong cùng một transaction. Sự kiện `user.registered` được ghi vào `outbox` (chưa có worker đẩy lên NATS — task sau).
- **Docker:** service `identity` (cổng 8090) trong `docker-compose.yml`, profile `nxl`, phụ thuộc `db-migrate`.

### Frontend — cấu trúc (nexuslive-web)

| Phần | File |
| --- | --- |
| Router | `src/app/router.tsx` — `/`, `/login`, `/register`; trang cần đăng nhập bọc `RequireAuth` |
| Gọi API | `src/lib/api-client.ts` — `fetch` + gắn Bearer, tự gọi `refresh` một lần khi gặp 401 rồi thử lại, chuẩn hóa lỗi theo envelope |
| Auth | `src/features/auth/` — `api/`, `hooks/` (TanStack Query mutation), `stores/auth-store.ts` (Zustand: access token + user, chỉ trong bộ nhớ), `components/` (form) |
| Khởi động | Gọi `refresh` một lần khi mở app để khôi phục phiên từ cookie |
| Form | Validation phía client giống hệt server; hiển thị lỗi theo trường (409 gắn vào ô username/email) |
| Giao diện | Token Obsidian Pulse, dark, tiếng Việt, đủ trạng thái loading / lỗi / disabled, focus-visible, label cho mọi ô nhập |
| Copy | `src/features/auth/constants/auth-content.ts` |

## 4. Các bước

1. Duyệt plan và chốt các quyết định ở §7. Viết TSD (sequence diagram cho đăng ký, đăng nhập, refresh xoay vòng, phát hiện dùng lại token). **Chờ duyệt.**
2. **Backend:**
   - `pkg/jwt` EdDSA + test.
   - Module `services/identity`: entity → repo → usecase (+ traced) → controller.
   - Test bảng cho usecase.
3. **Docker và cấu hình:** compose, `.env.example`, Makefile (`nxl-identity-run`).
4. **Frontend:** cài dependency đã duyệt; router, api-client, auth store và hook; trang Login và Register; header.
5. **Ghép nối:** Vite proxy `/v1` → identity-svc; kiểm tra đầu–cuối.
6. **Kiểm tra (§6), Completed Plan, cập nhật NXL-D bằng nội dung §2–§3.**

## 5. Rủi ro

| Rủi ro | Tác động | Giảm thiểu |
| --- | --- | --- |
| **Môi trường của Claude không biên dịch được Go**: Go 1.27 và module bị chặn tải, máy ảo trên máy bạn không có Go | Không tự chạy được `go build`, `make test`, lint | Kiểm tra bằng `docker compose build identity` trên máy bạn (biên dịch trong image `golang:1.27`). Bạn gửi log lỗi, tôi sửa. Nếu máy bạn có Go 1.27 thì chạy thêm `make test` |
| `go.work` ảnh hưởng build app mẫu và Dockerfile cũ | App mẫu không build | Dockerfile cũ dùng `GOWORK=off`; module root không đổi |
| Cookie `SameSite=Strict` khi FE và API khác origin | Mất phiên | Dev đi qua Vite proxy (cùng origin); production đi qua Nginx cùng domain (kiến trúc §3) |
| Thêm dependency | Theo quy tắc phải hỏi | Liệt kê ở §7 |

## 6. Checklist kiểm tra

- [ ] Usecase test: đăng ký trùng username/email; login sai; refresh hợp lệ; dùng lại refresh token cũ → thu hồi mọi session; tài khoản `LOCKED`.
- [ ] `pkg/jwt` test: ký/xác minh, token hết hạn, sai khóa, sai thuật toán (chặn `alg: none` / HS256).
- [ ] `docker compose build identity` + `docker compose --profile nxl up -d identity` chạy (bạn chạy).
- [ ] FE: `pnpm build`, `pnpm lint` (tôi chạy được).
- [ ] Đầu–cuối:
  - đăng ký → vào trang chủ thấy tên;
  - tải lại trang vẫn đăng nhập;
  - đăng xuất → về trang đăng nhập;
  - đăng nhập sai → thông báo đúng;
  - đăng ký trùng → lỗi gắn đúng ô.
- [ ] App mẫu (`make compose-up-all`) vẫn chạy; `migrations/` mẫu không đổi.

## 7. Quyết định cần bạn chốt

1. **Vị trí code backend:**
   - Đề xuất: module mới `services/identity` (đúng ADR-002, mở đầu monorepo).
   - Phương án khác: sửa thẳng domain `user` sẵn có của app mẫu, nhanh hơn nhưng phải làm lại khi tách service.
2. **Dependency FE** (theo quy tắc phải hỏi): `react-router` (điều hướng), `react-hook-form` + `zod` + `@hookform/resolvers` (form và validation).
3. **Dependency Go:** `golang.org/x/crypto` đã có sẵn, dùng cho argon2id. Không thêm thư viện JWT mới — `golang-jwt/jwt/v5` đã có và hỗ trợ EdDSA.
4. **Đăng nhập trước khi xác minh email:**
   - Đề xuất: cho phép, chỉ hiển thị nhắc nhở.
   - Lý do: xác minh email làm ở task sau khi có Mailpit.

## 8. Tiêu chí hoàn thành

- Given chưa có tài khoản, When đăng ký hợp lệ, Then tài khoản được tạo, người dùng đã đăng nhập và thấy tên trên header.
- Given đã đăng nhập, When tải lại trang, Then vẫn đăng nhập (khôi phục từ cookie refresh), không lộ token trong `localStorage`.
- Given refresh token cũ đã xoay vòng, When dùng lại, Then bị từ chối và mọi phiên của user bị thu hồi.
- Given sai mật khẩu hoặc email không tồn tại, When đăng nhập, Then cùng thông báo "Email/username hoặc mật khẩu không đúng".
