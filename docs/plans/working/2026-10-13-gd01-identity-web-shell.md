# GĐ1 — Identity, Creator, kênh tối thiểu và Web shell

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (service mới channel-svc, project web mới `nexuslive-admin`, migration identity/stream, cổng vào, nhiều domain) | Approved 13/10 (v1.1: tách trang Admin thành project riêng) — đã triển khai, xem Completed Plan | 2026-10-13 |

Lịch lộ trình: **26/10 → 13/11/2026** (tuần 3–5), mốc **M1**. GĐ0 xong sớm ~10 ngày làm việc, nên plan đề
xuất **bắt đầu ngay khi được duyệt** (§8).

## 1. Task Brief

**Mục tiêu (M1 — lộ trình §3):** đăng ký → nhận email xác minh → đăng nhập → gửi đơn Creator → Admin duyệt →
kênh được tạo tự động → Studio mở được. Kèm theo: Guest mode với modal "đăng nhập để tiếp tục" quay lại đúng ngữ
cảnh, Global Shell đúng NXL-C, cổng vào có rate limit + CORS.

**Nguồn yêu cầu** (đã chép về `DATN/docs/ba/` ngày 13/10):

| Nguồn | Phần dùng cho GĐ1 |
| --- | --- |
| PRD v2.0 | §4.1 FR-SH-01…06 · §4.2 FR-AU-01, 02, 04, 06 · §4.16 Settings (Account) · §5 Responsive · §6 trạng thái hệ thống · §7.5 WCAG · §8.3 RBAC · §8.4 API · NFR-BE-06/07 · §9.3 correlation-id |
| NXL-C v1.0 | BR27–BR33, BR36 (top bar, Phát trực tiếp, sidebar, Guest sidebar, mobile bottom nav, Creator CTA) · AF03, AF11, AF12 · §7A trạng thái sidebar · §9A Global Shell · API 06 `GET /v1/me` · E12, E16 |
| NXL-A v1.0 | Guest được xem; Follow/Subscribe/Report cần đăng nhập → modal → quay lại đúng trang; E12 phiên hết hạn khi đang xem |
| NXL-B v1.0 | AF10: Guest bấm ô chat → modal, sau đăng nhập giữ bản nháp + focus (áp dụng khi có chat ở GĐ3; GĐ1 dựng cơ chế `pendingAction`) |
| NXL-00 v1.1, NXL-D v1.1 | Hợp đồng chính thức (envelope, mã lỗi, JWT, sự kiện, hằng số; §3.1–§3.5, §13.1 của NXL-D) |
| Core Features & Roadmap | Phase 1 M1 "Auth + Guest, Global Shell, Design System code" |

**Trong phạm vi:**

- identity-svc: xác minh email, quên/đặt lại mật khẩu, khóa tạm khi sai mật khẩu nhiều lần, `acceptTerms`,
  `GET/PATCH /v1/me` (hợp đồng Shell của NXL-C API 06), đổi mật khẩu, gửi email qua SMTP, seed Admin + CLI.
- Đơn Creator + Admin duyệt (identity) → **channel-svc tối thiểu** tạo kênh → stream-svc dùng kênh thật
  (`NOT_CREATOR`, tên/handle kênh trên stream).
- Cổng vào: rate limit, CORS, header bảo mật, route mới.
- Web người dùng (`nexuslive-web`): Guest mode + modal đăng nhập/đăng ký có `pendingAction`, Shell theo NXL-C,
  trang xác minh/quên mật khẩu, cài đặt tài khoản, trang hệ thống, đơn Creator, R5 Auth.
- **Web quản trị riêng (`nexuslive-admin`, project mới):** đăng nhập Admin, hàng đợi + duyệt/từ chối đơn Creator.
  Các công cụ Admin của GĐ5 (report, khóa tài khoản/kênh, gỡ stream, audit log) cũng sẽ nằm ở project này.
- Tài liệu: áp errata vào PRD v2.1 + NXL-A/B/C v1.1 (file gốc đã có), viết **NXL-G1** (lộ trình tuần 3–4).

**Ngoài phạm vi** (ghi lệch PRD ở §9):

- Đăng nhập điện thoại OTP, Google, Apple (DL-09); 2FA, quản lý thiết bị, onboarding (P1).
- Đổi email/username, xóa tài khoản.
- Follow, danh sách kênh theo dõi, Following thật, hồ sơ kênh đầy đủ (GĐ4).
- notification-svc, chuông có dữ liệu (GĐ5).
- Realtime `me:following` (GĐ3/GĐ4).

**Giả định:**

- Volume dev đã làm mới khi đổi tên project compose (13/10), nên **không cần script chuyển dữ liệu**
  `channelId = userId` cũ.
- Không thêm dependency Go mới: `net/smtp`, `html/template`, `embed`, `flag` của thư viện chuẩn. Web không thêm
  thư viện.
- Chat chưa có (GĐ3): cơ chế `pendingAction` được dựng và dùng cho Follow (UI), Go Live, Following; NXL-B AF10
  dùng lại ở GĐ3.

**Câu hỏi mở:** §10.

## 2. Gói việc

### WP1 — identity-svc: hoàn thiện tài khoản (Full)

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Migration `0003` | `users`: `failed_logins smallint`, `last_failed_login_at`, `preferences jsonb DEFAULT '{}'`; `creator_applications.status` thêm `CANCELLED` (đổi CHECK) | NXL-D §7.2, BR-D09; NXL-C API 06 |
| Đăng ký | Thêm `acceptTerms` bắt buộc (13+ tự khai); `details` lỗi trùng dùng `TAKEN` (giữ đọc được mã cũ ở web) | NXL-D BR-D01–D05, 00-Q4 |
| Xác minh email | `POST /v1/auth/verify-email`, `POST /v1/auth/verify-email/resend` (cooldown 60s, tối đa 5/giờ) | NXL-D §3.1, AF-D02 |
| Quên / đặt lại mật khẩu | `POST /v1/auth/password/forgot` (luôn 202), `POST /v1/auth/password/reset` (thu hồi mọi phiên) | NXL-D §3.3, BR-D07–D08 |
| Khóa tạm | 10 lần sai / 15 phút → `LOCKED` + `lockedUntil` 15 phút; `403 ACCOUNT_LOCKED` có `details.lockedUntil` | NXL-D BR-D09 |
| `/v1/me` | `GET /v1/me` → `{user, roles[], isCreator, channelId, unreadNotifications: 0, preferences{sidebarCollapsed, matureVisible}}`; `PATCH /v1/me` (displayName, birthDate, locale, timezone, preferences); giữ `/v1/auth/me` | NXL-C API 06, NXL-D §13.1 |
| Đổi mật khẩu | `POST /v1/me/password` → phiên mới, thu hồi phiên khác | NXL-D BR-D08 |
| Gửi email | Gói `internal/mailer`: SMTP (`IDENTITY_SMTP_ADDR`, `IDENTITY_MAIL_FROM`, `IDENTITY_WEB_BASE_URL`), template HTML + text vi/en nhúng bằng `embed`; gửi sau commit, lỗi gửi chỉ log (người dùng bấm gửi lại); vẫn phát `auth.email_requested.v1` | D-Q4 |
| Admin | Seed `admin@nexuslive.dev` khi `NXL_ENV=dev` (mật khẩu từ `IDENTITY_DEV_SEED_PASSWORD`); lệnh `identity promote-admin <email>` | D-Q5 |
| CSRF | `/v1/auth/refresh`, `/v1/auth/logout`: cookie SameSite=Strict + bắt buộc header `X-NXL-Client: web` (trình duyệt không gửi được header tùy ý cross-site mà không qua CORS) | NFR-BE-06 |
| Sự kiện | `user.email_verified.v1`, `user.profile_updated.v1`, `auth.email_requested.v1`; `pkg/eventbus` thêm header `Nxl-Occurred-At`, `Nxl-Producer` | NXL-00 §11 |
| Audit | Log có cấu trúc `audit=…` cho đổi/đặt lại mật khẩu, khóa tạm, phát hiện token dùng lại (bảng audit ở GĐ5) | NXL-D §16 |

### WP2 — Đơn Creator và Admin duyệt (Full)

- identity:
  - `POST/GET/DELETE /v1/creator-applications[/me|/{id}]`;
  - `GET /v1/admin/creator-applications`, `POST …/{id}/approve|reject`;
  - middleware `RequireAdmin` (claim `role`).
- Trạng thái + outbox trong cùng transaction: `creator.application_submitted.v1`, `creator.approved.v1`
  (payload `applicationId, userId, username, displayName`), `creator.rejected.v1`.
- Khi duyệt: ghi `birth_date` từ đơn vào `users`.
- Quy tắc NXL-D BR-D11–D15, BR-D24; mã lỗi `EMAIL_NOT_VERIFIED`, `APPLICATION_PENDING`, `ALREADY_CREATOR`,
  `UNDERAGE`, `INVALID_STATE`.
- `isCreator`/`channelId` trong `/v1/me`: identity giữ bản sao từ `channel.created.v1` (bảng `user_channels`) —
  identity không gọi channel-svc.

### WP3 — channel-svc tối thiểu (Full, service mới)

- Khung `services/channel` theo `services/stream`: config, app, entity, repo, usecase, controller events + REST,
  Dockerfile, `/metrics`, `/healthz`.
- Dùng migration `nxl_channel` đã có (bảng `channels`).
- Consumer `creator.approved.v1`:
  - `INSERT channels` idempotent theo `owner_id`;
  - handle = `lower(username)`, trùng thì thêm hậu tố số (E-D13);
  - outbox `channel.created.v1` (`channelId, ownerId, handle, displayName`).
- REST công khai tối thiểu: `GET /v1/channels/{handle}` (id, handle, displayName, avatarUrl, bio, isLive=false,
  createdAt). Phần follow/lịch/ảnh thuộc GĐ4.
- Đưa vào: `go.work`, `NXL_GO_MODULES`, CI matrix (lint/test/docker), compose (profile `nxl`, cổng 8094),
  Nginx `/v1/channels`, Prometheus, README, `docs/api`.

### WP4 — stream-svc dùng kênh thật (Full)

- Migration: bảng `creator_channels (owner_id PK, channel_id UNIQUE, handle, display_name)`, cập nhật qua consumer
  `channel.created.v1` (và `channel.updated.v1` sau này).
- Studio: `userId → channelId` qua bảng trên. Không có → `403 NOT_CREATOR` (web chuyển sang luồng đăng ký Creator).
- Danh sách live, chi tiết stream, VOD: thêm `channel {id, handle, displayName, avatarUrl}`, bỏ hiển thị `channelId`
  thô.
- Hợp đồng `/internal/v1/ingest/authorize` giữ nguyên.

### WP5 — Cổng vào Nginx (Full)

- **Rate limit** (`limit_req`), vượt → `429 RATE_LIMITED` + `Retry-After`:
  - `auth`: 10/phút/IP, burst 5, cho login/register/forgot/reset/verify-resend;
  - ghi: 60/phút theo IP (GĐ1 chưa có userId ở Nginx);
  - đọc: 600/phút/IP.
- **CORS:** chỉ hai origin `NXL_WEB_ORIGIN` (dev `http://localhost:5173`) và `NXL_ADMIN_ORIGIN`
  (dev `http://localhost:5174`), có `credentials`. Dev qua Vite proxy vẫn cùng origin.
- `/v1/admin/*` chỉ chấp nhận CORS từ `NXL_ADMIN_ORIGIN` (web người dùng không gọi được API quản trị từ trình duyệt);
  quyền thật vẫn do service kiểm.
- **Header bảo mật:** `X-Content-Type-Options`, `Referrer-Policy: strict-origin-when-cross-origin`,
  `X-Frame-Options: DENY`.
- **Route mới:**
  - `/v1/channels` → channel-svc;
  - `/v1/admin/creator-applications` và `/v1/creator-applications` → identity (đi qua mặc định `/v1/`).

### WP6 — Web (`nexuslive-web`, cùng basename)

| Hạng mục | Chi tiết | Nguồn |
| --- | --- | --- |
| Phiên | Header `X-NXL-Client: web` cho refresh/logout; `GET /v1/me` thay `/auth/me` cho Shell; 401 → refresh một lần → Guest + modal | NXL-00 §7, NXL-A E12, NXL-B E03 |
| Modal đăng nhập/đăng ký | Một modal hai tab, mở từ mọi hành động bị chặn; lưu `pendingAction {type, params}` + vị trí; sau đăng nhập/đăng ký thực hiện đúng 1 lần; player không dừng; bottom sheet trên mobile; focus trap | FR-AU-04, NXL-D BR-D10 |
| Hành động gắn modal ở GĐ1 | Phát trực tiếp (Guest), Follow trên trang xem (lưu ý định, logic follow ở GĐ4), mục Following (trang intro), Creator CTA | NXL-C BR28, AF03, BR36 |
| Top bar | Guest: nút **Đăng nhập / Đăng ký** thay avatar; **Phát trực tiếp**: Creator → Studio, Viewer → `/creator/apply`, Guest → modal; chuông (số 0 tới GĐ5); ẩn Messages | NXL-C BR27, BR28 |
| Sidebar | Guest: **Kênh đề xuất** (GĐ1 lấy từ danh sách đang live, tối đa 10) + lời mời đăng nhập; đã đăng nhập: khối "Kênh đang theo dõi" ở trạng thái rỗng tới GĐ4; trạng thái thu gọn lưu `preferences.sidebarCollapsed` (Guest: localStorage) | NXL-C BR29–BR32 |
| Mobile | Bottom nav 5 mục Home, Browse, Following, Search, Profile; Profile của Guest mở modal; ẩn khi fullscreen/bàn phím mở | NXL-C BR33, AF12 |
| Trang tài khoản | `/verify-email`, `/forgot-password`, `/reset-password` (xóa token khỏi URL), `/settings/account` (hồ sơ, mật khẩu, ngôn ngữ, múi giờ), banner xác minh email | NXL-D §3.1–§3.3, §3.9 |
| Trang hệ thống | 404, 500 + Thử lại, phiên hết hạn, tài khoản bị khóa (hiện `lockedUntil`), banner "Bạn đang offline", 429 "Bạn thao tác quá nhanh" | PRD §6, NXL-A E11, NXL-C E16 |
| Creator | `/creator/apply` (trạng thái đơn, form, lý do từ chối, nộp lại), Creator CTA đóng được (30 ngày) | NXL-D §3.4, NXL-C AF11 |
| Không có Admin | Web người dùng **không** chứa route hay code Admin; menu avatar của tài khoản Admin chỉ có liên kết "Trang quản trị" mở `nexuslive-admin` | Tách theo quyết định 13/10 |
| Kênh | Tên + avatar kênh (chữ cái đầu nếu chưa có ảnh) trên thẻ live, trang xem, Studio | NXL-D §3.5 |
| R5 | Đăng nhập/đăng ký/tài khoản theo design mới (frame pen.dev nếu có; nếu không, theo token Obsidian Pulse hiện có) | Plan UI redesign |
| i18n | Mọi chuỗi mới vi + en (namespace `auth`, `account`, `creator`, `system`; chuỗi quản trị nằm ở `nexuslive-admin`); mã lỗi mới trong `common:errors` | PRD D3 |

### WP6b — Web quản trị `nexuslive-admin` (project mới, cùng basename)

**Vì sao tách:**

- Người dùng và mục đích khác hẳn web chính (bảng dữ liệu, thao tác hàng loạt, không cần player/Shell/SEO).
- Bundle của web người dùng không chứa code quản trị.
- Admin có thể deploy và giới hạn truy cập riêng (domain/cổng riêng, sau này thêm IP allowlist).
- GĐ5 bổ sung report, khóa tài khoản/kênh, gỡ stream, audit log mà không làm phình `nexuslive-web`.

| Hạng mục | Chi tiết |
| --- | --- |
| Vị trí | `DATN/nexuslive-admin` — repo riêng, cạnh `nexuslive-web` |
| Stack | Giống web chính để không phải học lại: React 19, Vite 8, TypeScript, Tailwind v4, shadcn/radix, lucide, TanStack Query, react-hook-form + zod, react-i18next (vi mặc định, en), Vitest, Playwright. **Không thêm thư viện mới** ngoài những gì web đang dùng |
| Giao diện dùng chung | **Chép** `src/styles/tokens.css`, các component `components/ui` cần dùng và `lib/api*` từ `nexuslive-web` (ghi nguồn + ngày chép trong README). Chưa tách package chung ở GĐ1 (chi phí monorepo FE lớn hơn lợi ích); xem lại ở GĐ5 khi có nhiều code trùng |
| Bố cục | Khung quản trị riêng: sidebar trái cố định (Đơn Creator; GĐ5: Báo cáo, Người dùng, Kênh, Stream, Nhật ký), top bar có tên Admin + đăng xuất; desktop-first (≥ 1280), tablet dùng được, không làm mobile |
| Đăng nhập | Trang `/login` riêng, gọi cùng `POST /v1/auth/login`; sau đăng nhập kiểm tra `/v1/me` có `roles` chứa `ADMIN`, không có → màn "Tài khoản không có quyền quản trị" + đăng xuất. Mọi request vẫn bị identity kiểm `RequireAdmin` (UI chỉ là lớp phụ) |
| GĐ1 màn hình | `/creator-applications`: bảng (lọc trạng thái, cũ nhất trước, cursor), panel chi tiết (username, email + đã xác minh, ngày tạo tài khoản, họ tên pháp lý, ngày sinh + tuổi, ghi chú), **Duyệt** / **Từ chối** (lý do 10–500 ký tự) có hộp xác nhận; `409 INVALID_STATE` → tải lại + toast "Đơn đã được xử lý" |
| Dev | `pnpm dev` ở `http://localhost:5174`; Vite proxy `/v1` → cổng vào `http://localhost:8080` (cùng origin, không cần CORS khi dev) |
| Phiên | Cùng cơ chế web chính (access token trong bộ nhớ, refresh cookie `nxl_rt`, header `X-NXL-Client: admin`). Lưu ý: trên `localhost` cookie không phân biệt cổng nên đăng nhập/đăng xuất ở web chính và admin ảnh hưởng nhau khi dev; khi deploy dùng host riêng (`admin.<domain>`) nên tách biệt |
| CI | `.github/workflows/ci.yml` như web: lint, typecheck, test, build |
| E2E | Playwright: Admin (seed) đăng nhập → duyệt đơn do E2E web tạo; Viewer đăng nhập admin → bị từ chối |

### WP7 — Tài liệu

- **Áp errata** vào bản đã chép:
  - `ba/requirements/NexusLive-PRD-v2.0.md` → **v2.1**: E-01…E-09, ghi lệch phạm vi đồ án ở FR-AU-01;
  - NXL-A/B/C → **v1.1**: §13 trỏ NXL-00, NXL-A thêm DVR (E-10), NXL-B đổi mã lỗi chữ thường (E-15) và đóng Q1
    theo DL-14.
- **NXL-00 v1.2:** CSRF header, hợp đồng `/v1/me` + `preferences`, ma trận quyền Creator được Follow (sửa chỗ "—"
  của PRD §8.3), bảng ánh xạ mã lỗi NXL-B → NXL-00.
- **NXL-D v1.2:** `GET /v1/me` (Shell), trang "phiên hết hạn" / "tài khoản bị khóa"; §9 chuyển màn duyệt đơn sang
  ứng dụng `nexuslive-admin`.
- **ADR-013** "Ứng dụng quản trị tách khỏi web người dùng" + kiến trúc §3 (thêm container Admin web), README DATN.
- **NXL-G1** (Go Live & vòng đời Stream): viết theo pipeline đã chạy + TSD core livestream; lộ trình tuần 3–4.
- Cập nhật API catalog, ma trận truy vết (FR-SH, FR-AU-04/06 → BR/AC), README, kiến trúc §4 (channel-svc đã có).

### WP8 — Kiểm thử và demo M1

- Unit (`-race`):
  - identity: lockout, token email, đơn Creator, CSRF;
  - channel: idempotent tạo kênh, hậu tố handle;
  - stream: `NOT_CREATOR`, ánh xạ kênh.
- Repo test PostgreSQL cho identity (mới) và channel.
- **E2E M1** (Playwright, đọc email qua API Mailpit):
  - đăng ký → xác minh → nộp đơn (web) → Admin (seed) duyệt **trên `nexuslive-admin`** → Studio mở → tạo key →
    phát ffmpeg → trang xem hiện tên kênh;
  - Guest bấm Phát trực tiếp → modal → đăng nhập → về đúng chỗ.
- E2E livestream cũ chạy lại (người phát giờ phải là Creator).
- Hostile path:
  - token email sai/hết hạn/dùng lại;
  - sai mật khẩu 11 lần;
  - refresh không có header CSRF;
  - Viewer gọi `/v1/admin/*`, gọi Studio;
  - `creator.approved.v1` giao 2 lần;
  - channel-svc chết khi duyệt (kênh tạo sau khi service lên lại).

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Thay đổi |
| --- | --- | --- |
| identity | `migrations/0003`, `entity`, `repo`, `usecase/auth`, `usecase/creator` (mới), `controller/restapi/v1`, `internal/mailer` (mới), `cmd/identity` (lệnh CLI), `config` | WP1, WP2 |
| channel (mới) | `services/channel/**` | WP3 |
| stream | `migrations/0004`, `controller/events`, `usecase/streams`, `controller/restapi/v1` | WP4 |
| pkg | `eventbus` (header), `events` (type + payload mới) | WP1–WP4 |
| Hạ tầng | `deploy/nginx/nginx.conf`, `docker-compose.yml`, `deploy/prometheus/prometheus.yml`, `go.work`, `Makefile`, `.github/workflows/ci.yml`, `.env.example` | WP3, WP5 |
| Web | `src/app/shell/*`, `src/features/auth/*`, mới `account`, `creator`, `system`; `src/lib/api*`; locales | WP6 |
| Web quản trị (mới) | `DATN/nexuslive-admin/**` | WP6b |

| Transport | Có | Endpoint / sự kiện |
| --- | --- | --- |
| REST công khai | Có | §2 WP1, WP2, WP3 |
| Nội bộ HTTP | Không mới | — |
| NATS | Có | `auth.email_requested.v1`, `user.email_verified.v1`, `user.profile_updated.v1`, `creator.application_submitted.v1`, `creator.approved.v1`, `creator.rejected.v1`, `channel.created.v1` |

**Luồng hiện có có thể hỏng:**

- Đăng ký (thêm `acceptTerms`, đổi mã `details`).
- Refresh/logout (header CSRF — web phải gửi trước khi bật kiểm tra).
- Studio (`NOT_CREATOR` — mọi tài khoản thử phải qua duyệt).
- E2E livestream.
- `/v1/auth/me` (giữ nguyên, thêm `/v1/me`).

## 4. Mã lỗi, sự kiện, cấu hình

- **Mã lỗi:** dùng danh mục NXL-00 §5 (đã có đủ: `TOKEN_INVALID`, `WRONG_PASSWORD`, `EMAIL_NOT_VERIFIED`,
  `APPLICATION_PENDING`, `ALREADY_CREATOR`, `UNDERAGE`, `INVALID_STATE`, `NOT_CREATOR`, `RATE_LIMITED`).
- **Sự kiện:** NXL-00 §11.1.
- **Biến môi trường mới:**
  - `IDENTITY_SMTP_ADDR`, `IDENTITY_MAIL_FROM`, `IDENTITY_WEB_BASE_URL`;
  - `IDENTITY_EMAIL_VERIFY_TTL`, `IDENTITY_PASSWORD_RESET_TTL`;
  - `IDENTITY_LOGIN_MAX_FAILURES`, `IDENTITY_REQUIRE_VERIFIED_FOR_CREATOR`, `IDENTITY_DEV_SEED`, `IDENTITY_DEV_SEED_PASSWORD`;
  - `CHANNEL_*` (cổng, PG, NATS);
  - `NXL_WEB_ORIGIN`, `NXL_ADMIN_ORIGIN` (Nginx CORS — chỉ cần khi web/admin gọi cổng vào khác origin, ví dụ khi deploy).

  Đều có mặc định cho dev, ghi vào `.env.example` và compose.

## 5. Phát sinh tài liệu / sinh code

- [ ] `make nxl-swag` (identity, stream, channel).
- [ ] Mock (`go generate`).
- [ ] TSD: `docs/plans/technical/2026-10-13-gd01-identity-web-shell.md` gồm sequence diagram cho xác minh email,
  quên mật khẩu, đơn Creator → kênh, Guest modal.
- [ ] Plan web cùng basename trong `nexuslive-web/docs/` (hoặc §WP6 ở đây nếu web chưa có thư mục plan).

## 6. Thứ tự thực hiện

| # | Việc | Phụ thuộc |
| --- | --- | --- |
| 1 | TSD GĐ1 → duyệt | Plan này duyệt |
| 2 | `pkg/eventbus` header + `pkg/events` mới; migration identity `0003` | 1 |
| 3 | WP1 identity (xác minh, quên mật khẩu, lockout, `/v1/me`, SMTP, seed Admin, CSRF) | 2 |
| 4 | WP6 phần phiên + modal + trang tài khoản/hệ thống (song song với 5–6) | 3 |
| 5 | WP2 đơn Creator + Admin | 3 |
| 6 | WP3 channel-svc + WP4 stream-svc | 5 |
| 7 | WP5 Nginx | 3, 6 |
| 8 | WP6 phần Shell, Creator, R5, tên kênh | 5, 6 |
| 8b | WP6b dựng `nexuslive-admin` (khung, đăng nhập, màn đơn Creator, CI) | 5 |
| 9 | WP8 E2E M1 + hồi quy; WP7 tài liệu (NXL-G1 làm song song từ bước 3) | 1–8 |
| 10 | Completed Plan, cập nhật tiến độ, tổng kết GĐ1 | 9 |

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Bật CSRF header làm phiên cũ trên web lỗi | Web gửi header trước; backend bật kiểm tra sau cùng commit web; E2E chạy lại |
| `NOT_CREATOR` làm vỡ E2E livestream và thao tác thử hằng ngày | Seed dev thêm `creator@nexuslive.dev` đã duyệt (cờ dev); helper E2E tạo Creator qua API Admin |
| Email lỗi SMTP khi demo | Mailpit trong compose; nút "Gửi lại"; cờ `IDENTITY_REQUIRE_VERIFIED_FOR_CREATOR=false` để demo khẩn |
| Rate limit chặn E2E / load test | Ngưỡng cấu hình qua biến; E2E dùng IP riêng hoặc tắt bằng cờ dev |
| channel-svc chưa lên khi Admin duyệt | Outbox + JetStream giữ sự kiện; kênh tạo khi service lên lại (AC hostile) |
| Phạm vi web lớn (Shell + 10 trang) | Ưu tiên luồng M1; R5 làm cuối, có thể dời một phần sang GĐ2-đóng |
| Thêm một project FE (`nexuslive-admin`): code UI trùng, hai nơi phải cập nhật token | GĐ1 chỉ chép phần cần; ghi nguồn + ngày chép; xem lại việc tách package chung ở GĐ5 |
| Cookie refresh dùng chung giữa web và admin trên `localhost` | Chấp nhận khi dev; deploy dùng host riêng cho admin |

## 8. Lịch đề xuất

GĐ0 xong 13/10 nên có thể bắt đầu sớm:

| Tuần | Thời gian | Nội dung |
| --- | --- | --- |
| 0 (sớm) | 14/10 – 24/10 | TSD; WP1; WP6 phiên + modal + trang tài khoản; NXL-G1 nháp |
| 1 | 26/10 – 30/10 | WP2, WP3, WP4; WP5; dựng khung `nexuslive-admin` (WP6b) |
| 2 | 02/11 – 06/11 | WP6 Shell/Creator/kênh; WP6b màn đơn Creator; WP7 errata PRD v2.1, A/B/C v1.1 |
| 3 | 09/11 – 13/11 | R5, E2E M1, hồi quy, tài liệu, demo M1 |

## 9. Lệch so với PRD / spec gốc (ghi để báo cáo)

| Nguồn | Yêu cầu gốc | GĐ1 làm | Lý do |
| --- | --- | --- | --- |
| PRD FR-AU-01, §8.4 `/auth/otp/verify` | Email, điện thoại OTP, Google, Apple | Email + mật khẩu; đăng nhập bằng email **hoặc username** | DL-09 |
| PRD FR-AU-06, Q3 | KYC | Khai họ tên + ngày sinh, Admin duyệt tay (≥ 18) | DL-08, DL-14 |
| PRD §7.1 | SPA/SSR (Next.js) cho SEO | Vite SPA | ADR-007 (DL-03) |
| PRD §3 | Bảng màu `bg-deep #07100A`, accent `#00F59B`… | Token Obsidian Pulse theo design pen.dev (cùng accent `#00F59B`, nền trung tính `#0F0F10`) | Design mới ông chủ cung cấp (R1/R4) |
| PRD FR-SH-01, FR-SH-06 | Messages, History/Favorites ở Shell | Ẩn (NXL-C BR27, BR29) | Tính năng chưa có |
| PRD §8.3 | Creator/Admin "—" ở cột Follow | Creator/Admin follow được kênh khác | Ô "—" là lỗi bảng (errata mới E-16) |
| NXL-A/B/C §13 | `{success, data}` / `{success, error}`, mã chữ thường ở NXL-B | Envelope + mã NXL-00 | E-02, E-03 |
| NXL-C BR31 | Kênh đề xuất do server trả | GĐ1 lấy từ danh sách đang live | Chưa có channel-svc đầy đủ (GĐ4) |

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| G1-Q1 | Bắt đầu GĐ1 ngay (14/10) hay đúng lịch 26/10? | Ngay, để dồn thời gian dự phòng cho GĐ3 chat |
| G1-Q2 | Bảo vệ CSRF cho cookie refresh | SameSite=Strict + header `X-NXL-Client` bắt buộc ở refresh/logout |
| G1-Q3 | Tài khoản dev seed sẵn | `admin@nexuslive.dev` + `creator@nexuslive.dev` (đã duyệt) khi `NXL_ENV=dev` |
| G1-Q4 | Có frame pen.dev cho màn Auth/Settings và giao diện quản trị không? | Nếu chưa có, làm theo token hiện có rồi chỉnh khi có design |
| G1-Q5 | `nexuslive-admin` chép UI từ web hay tách package dùng chung ngay? | Chép ở GĐ1 (§WP6b); xem lại ở GĐ5 |
| G1-Q6 | `nexuslive-admin` có cần tiếng Anh không? | Có khung i18n, chỉ dịch tiếng Việt ở GĐ1 |

## 11. Kiểm tra trước khi báo xong

- [ ] `make format`, `make nxl-lint` (5 module), `make nxl-test` (`-race`, repo test identity/channel/stream trên PG).
- [ ] `make nxl-up-all` sạch; E2E M1 (web + admin) + E2E livestream + Guest modal qua cổng vào.
- [ ] `nexuslive-admin`: lint, typecheck, test, build; tài khoản không phải Admin bị chặn ở UI **và** ở API.
- [ ] Hostile path ở WP8; rate limit trả `429` + `Retry-After`; CORS từ origin lạ bị chặn.
- [ ] Swagger + `docs/api` + ma trận truy vết cập nhật.

## 12. Tiêu chí hoàn thành (M1)

- Given Guest ở trang chủ, When bấm **Phát trực tiếp** → đăng ký trong modal, Then được đưa tới `/creator/apply`
  (đã đăng nhập, không mất trang).
- Given vừa đăng ký, When mở Mailpit và bấm liên kết, Then email được xác minh trong ≤ 10 giây.
- Given đơn Creator hợp lệ, When Admin duyệt, Then trong ≤ 5 giây kênh có handle = username, `/v1/me` có
  `isCreator = true`, Studio mở, tạo key và phát được; trang xem hiện **tên kênh**.
- Given Viewer chưa là Creator, When gọi API Studio, Then `403 NOT_CREATOR`.
- Given tài khoản Admin seed, When đăng nhập `nexuslive-admin` và duyệt một đơn, Then đơn `APPROVED` và người nộp
  thấy trạng thái mới trên web người dùng; Given tài khoản thường, When đăng nhập `nexuslive-admin`, Then thấy
  "Tài khoản không có quyền quản trị" và mọi `/v1/admin/*` trả `403`.
- Given sai mật khẩu 11 lần, Then `403 ACCOUNT_LOCKED` + màn "Tài khoản tạm khóa" có thời điểm mở khóa.
- Given mobile < 768px, Then bottom nav 5 mục, Profile của Guest mở modal đăng nhập.
- Given gọi `/v1/auth/login` quá 10 lần/phút từ một IP, Then `429 RATE_LIMITED` có `Retry-After`.
