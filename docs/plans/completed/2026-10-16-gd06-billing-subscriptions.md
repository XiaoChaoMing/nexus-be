# Completed — GĐ6: Thanh toán & Subscription (billing-svc, SePay sandbox, mốc M5)

| Working Plan | TSD | Ngày |
| --- | --- | --- |
| `docs/plans/working/2026-10-16-gd06-billing-subscriptions.md` (Approved 19/10) | `docs/plans/technical/2026-10-16-gd06-billing-subscriptions.md` v1.2 | 2026-10-21 |

## 1. Đã giao

**Backend (`nexuslive-be`) — phần A (billing-svc, :8098):**

- **`pkg/events`:** 8 sự kiện mới (`subscription.activated | renewed | updated | expiring | expired`,
  `payment.succeeded | failed | voided`), chỉ thêm.
- **Migration** `20261016100001_gd6_billing` (không sửa migration GĐ0): trạng thái đơn `CANCELLED` / `VOIDED`, cột
  `sub_action`, `failure_reason` + CHECK, `needs_review`, `transactions.type VOID` + `source`, bản sao `channels`
  (`avatar_url`, `synced_at`), `idempotency_keys`, `payout_requests.number` / `bank_code` / `reject_reason`, unique một đơn
  `PENDING` / (user, kênh) và một payout `REQUESTED` / kênh.
- **Domain + lưu trữ:** tier (3 tier mặc định từ `channel.created.v1`, `backfill-channels` cho kênh cũ), đơn (invoice `NXL` + 10
  ký tự Crockford), sổ cái chỉ-thêm (phí 5 % `floor`), payout (tài khoản ngân hàng AES-256-GCM, 12 mã ngân hàng cố định).
- **`PaymentProvider`** `sepay` (ký form BR-H24, `X-Secret-Key`, tra cứu / liệt kê / hủy đơn theo E-32) và `fake` (trang
  `/dev/fake-psp` Trả / Hủy / Lỗi, chỉ `NXL_ENV=dev`).
- **REST:** tier công khai, `GET /v1/checkout/config`, `GET /v1/checkout/tiers/{tierId}`, tạo đơn (`Idempotency-Key`), xem /
  `sync` / hủy đơn, webhook IPN SePay (trùng → `200`, lệch tiền → `FAILED AMOUNT_MISMATCH`, VOID), subscription của tôi (hủy /
  tiếp tục gia hạn), lịch sử giao dịch, Studio Thu nhập (tier, summary, sổ cái, rút tiền), Admin (hàng đợi rút tiền,
  pending-count, chi tiết, số tài khoản đầy đủ có audit, duyệt / từ chối, danh sách đơn cần xem lại).
- **Áp thanh toán** dùng chung cho IPN, `sync` và reconcile (khóa đơn → sub, một lần); job `reconcile-pending`,
  `subscription-expiry` (nhắc 3 ngày, hết kỳ), `reconcile-daily`, dọn `idempotency_keys` (khóa Redis để chạy một bản).
- **CLI:** `backfill-channels`, `republish-entitlements`, `dev-ipn replay|synth`, `dev-void`.
- Nginx (route billing trước route cũ, webhook `limit_req nxl_webhook`, body ≤ 64 KB), compose (provider mặc định `fake`),
  Prometheus, Makefile (`nxl-billing-test`, `nxl-billing`, `nxl-tunnel`), CI matrix, Swagger, API catalog v1.9.

**Backend — phần B (chat, notification):**

- **chat-svc:** consumer `subscription.*` / `payment.*`; `sub:{channelId}` = `"{tier}:{untilUnix}"` (đọc được `"{tier}"` cũ);
  `expired` xóa bản sao bằng Lua so `until`; badge Sub T1–T3, Subs-only theo kỳ; snapshot `me.subTier`, `me.emoteTier`; emote kênh
  theo tier trong `GET /v1/chat/emotes?channelId=` (`tierRequired`, `Cache-Control: max-age=60`); system message `SUB_NEW` /
  `SUB_RENEW` (một lần, bỏ qua `replay`); `payment_updated` trên `user:{userId}`; CLI `chat emote add | remove | seed-demo`,
  `chat dev-grant sub --minutes`.
- **notification-svc:** loại thứ 9 `SUBSCRIPTION` (thêm cuối, token hủy nhận cũ vẫn đúng), 8 dạng payload, email
  `payment_receipt` / `payment_voided` bắt buộc (chỉ địa chỉ đã xác minh), `subscription_expiring` theo cài đặt; mẫu vi / en.
  API catalog v1.10.

**Web (`nexuslive-web`):** nút Subscribe, modal 4 bước + `/checkout`, `/checkout/redirect`, `/checkout/result` (sync + poll +
`payment_updated`), chat (emote kênh khóa theo tier, Subs-only, `SUB_NEW` / `SUB_RENEW`), `/settings/subscriptions`,
`/settings/payments`, thông báo `SUBSCRIPTION`, Studio → Thu nhập (4 tab) + ô "Doanh thu hôm nay"; E2E `m5-subscribe`,
`m5-duplicate-ipn`, `m5-failed`. Chi tiết: `../nexuslive-web/docs/plans/completed/2026-10-16-gd06-billing-subscriptions.md`.

**Admin (`nexuslive-admin`):** nhóm THANH TOÁN — Rút tiền (tab, badge chờ duyệt hỏi lại 30 s, panel, xem số tài khoản có
audit, Duyệt = đã chi trả tay, Từ chối có lý do, `409` → "Yêu cầu đã được xử lý") và Đơn cần xem lại (chỉ đọc: trả muộn cho
tier khác + lệch số tiền); nhãn audit billing; E2E `payouts`.

**Close-out (bản này, Lightweight — khoảng trống hợp đồng admin báo, chỉ thêm):**

| # | Sửa | Nơi |
| --- | --- | --- |
| 1 | `GET /v1/admin/checkout-orders/{id}`: `id` là id đơn hoặc mã hóa đơn (không phân biệt hoa thường) → `AdminOrder`; khác → `404 NOT_FOUND`. Admin dùng cho panel, giữ dự phòng tra qua danh sách khi `404` / `405` | billing (`OrderRepo.ByID`, `Admin.AdminOrder`), admin |
| 2 | Lọc `failureReason` (CSV, kiểm enum → `400 failureReason = ENUM`) trên `GET /v1/admin/checkout-orders`, AND với `status`. Admin đọc đơn lệch bằng `status=FAILED&failureReason=AMOUNT_MISMATCH`, bỏ lọc ở client + đọc trang 50 / bỏ qua tối đa 4 trang | billing, admin |
| 3 | `q`: **khớp chính xác khi là mã hóa đơn đầy đủ** (`NXL` + 10 ký tự Crockford, sau khi IN HOA), ngắn hơn vẫn là **tiền tố**. Mọi mã dài 13 ký tự nên mã đầy đủ chỉ có thể khớp chính nó — "chính xác trước" không cần sắp xếp riêng; dùng `=` thay `LIKE` | billing |
| 4 | Swagger billing, API catalog v1.11, TSD v1.2 | docs |
| 5 | E2E admin: chờ bản sao safety / stream (báo cáo ngay sau đăng ký → `404`, Studio ngay sau duyệt → `403 NOT_CREATOR`), handle trùng username trong panel rút tiền | admin `e2e/` |

## 2. Lệch so với plan / TSD

TSD v1.2 §13 + §15. Tóm tắt:

- **Ảnh emote kênh** nằm ở `deploy/emotes/channel/` do edge phục vụ, không lên MinIO `nxl-public` như G6-Q7 — tránh thêm
  `minio-go` vào module chat (không thêm dependency khi chưa duyệt). `chat emote add --image` nhận đường dẫn edge.
- **Cột `sub_action`** (NEW / RENEW) thay `kind` của NXL-H §7.5 (DTO vẫn `kind`) — để Donate sau này thêm `checkout_orders.kind`
  không đụng tên.
- **Endpoint thêm** cho design: `GET /v1/checkout/config`, `GET /v1/checkout/tiers/{tierId}`, `GET /v1/admin/payouts/pending-count`,
  `GET /v1/admin/payouts/{id}`, `GET /v1/admin/payouts/{id}/bank-account`; close-out thêm `GET /v1/admin/checkout-orders/{id}` và
  lọc `failureReason`.
- **VOID trong chat:** `subscription.expired` (`VOIDED`) xóa `sub:` khi `until ≤ currentPeriodEnd + 30 ngày` — VOID gửi kỳ đã rút
  ngắn nên so trực tiếp không bao giờ xóa (TSD v1.1).
- **Seed demo 5 emote** (`chat emote seed-demo --channel`): `kaelenHi`, `kaelenGG` (T1), `kaelenHype`, `kaelenClutch` (T2),
  `kaelenGOAT` (T3).
- **Web:** mã emote bị khóa gõ tay hiện như chữ (cả dòng lạc quan); "Điều khoản sử dụng" là chữ đậm (chưa có trang); chi tiết giao
  dịch / menu ⋯ trên mobile không dùng bottom sheet; Rút tiền xếp dọc dưới 1536px; các trang checkout / cài đặt tải lười.
- **Admin:** trước close-out không có endpoint một đơn (panel tìm qua `q` và giữ khi khớp đủ mã) và không lọc được
  `failureReason` (đọc trang `FAILED` rồi lọc ở client) — **đã sửa ở close-out**; billing chỉ gửi id người dùng → admin tra
  `@username` qua identity `GET /v1/admin/users/{id}` (cache 10 phút).
- Idempotency chỉ lưu `2xx`, chiếm lại khóa treo sau 60 s; `BILLING_RECONCILE_GIVEUP` 30 phút; `paid_at` = giờ billing lúc áp
  (TSD §13).

## 3. Sự cố

- E2E admin `moderation` hỏng 2 lần liền trên stack mới (`FRESH=1`): `POST /v1/reports` ngay sau khi đăng ký → `404` (safety chưa
  nhận `user.registered`), Studio ngay sau khi duyệt → `403 NOT_CREATOR` (stream chưa nhận kênh). Là tính nhất quán sau của bản
  sao, không phải lỗi service; spec nay thử lại tới khi sự kiện tới (≤ 15 s).
- E2E admin `payouts`: handle kênh trùng username chủ kênh → `getByText` khớp 2 phần tử; lấy phần tử đầu.
- Test giả của billing sinh mã hóa đơn 14 ký tự (`NXL0000000000` + bộ đếm) — sai hình dạng thật; sửa để giữ 13 ký tự (bộ đếm thay
  đuôi) vì tra theo mã hóa đơn giờ kiểm hình dạng.
- Không phát hiện lỗi backend khi chạy E2E web / admin.

## 4. Kiểm thử

| Lệnh | Kết quả |
| --- | --- |
| `golangci-lint run` (`make nxl-lint`) — root + 8 service | 0 lỗi |
| `golangci-lint fmt` (`make format`) | sạch |
| `go test -race -count=1 -covermode atomic ./...` mọi module (PG18 + Redis thật: `NXL_TEST_PG_URL_*` gồm `NXL_TEST_PG_URL_BILLING`, `NXL_TEST_REDIS_ADDR`; chạy lại sau khi tắt stack) | pass — 80 gói `ok`, không có race |
| Test mới: `TestAdminOrders` / `TestAdminOrder` (use case), `TestAdminOrderRoutes` (controller), `TestAdminOrderFilters` (repo, PG), `IsInvoiceNumber` (entity) | pass |
| `swag init` billing (cùng tham số `make nxl-swag`) | đã sinh lại, diff chỉ ở route / tham số mới |
| Migration | không có migration mới ở close-out |
| Admin `pnpm lint`, `tsc -b`, `vitest`, `pnpm build` | pass — 15 file, 135 test |
| Web | không đổi ở close-out (lần cuối: lint, `tsc -b`, 387 test, build pass) |
| Stack `FRESH=1 BILLING_ORDER_TTL=1m BILLING_RECONCILE_AFTER=20s BILLING_RECONCILE_INTERVAL=20s up.sh` (provider `fake`) | 10 healthz `200` |
| Probe qua edge (Admin): `/checkout-orders/nxl…` chữ thường → `200`; `/NXL0000000000`, `/zzz` → `404 NOT_FOUND`; `?failureReason=LOST` → `400 failureReason = ENUM`; `?q=<mã chữ thường>` → 1 đơn; `?q=NXLQ` → tiền tố; không token → `401`; người thường → `403` (controller test) | pass |
| Admin E2E `pnpm e2e payouts` (`E2E_BILLING_URL=http://localhost:8098`, `E2E_SEPAY_IPN_SECRET=dev-ipn-secret`) | **3/3 pass**; access log edge có `/v1/admin/checkout-orders/{invoice}` và `status=FAILED&failureReason=AMOUNT_MISMATCH` `200` |
| Admin E2E `pnpm e2e moderation` | **3/3 pass** (sau khi sửa chờ bản sao; trước đó 0/3 và 2/3) |
| Admin E2E `pnpm e2e approve` | 2/2 pass |
| Web E2E toàn bộ (`pnpm e2e`, 18 test, `E2E_CHAT_CLI`, `E2E_BILLING_CLI`, `E2E_BILLING_SQL`) | **18/18 pass** (19,2 phút, `VITE_DEV_GATEWAY_URL=http://localhost:8080`). Lượt trước 11/18 do quên `VITE_DEV_GATEWAY_URL` (Vite trả `Cannot GET` / manifest `404` cho route không proxy) — lỗi môi trường chạy, không phải code |

Luồng cũ chạy lại qua edge: đăng ký / đăng nhập, duyệt đơn Creator, báo cáo + khóa, tạm ngưng / mở kênh, gỡ buổi phát, mua
Tier (fake PSP), IPN trùng, đơn thất bại / hết hạn, rút tiền duyệt / từ chối (E2E web + admin).

Chưa chạy:

- **SePay sandbox thật (WP9)** — cần credential của chủ đồ án và tunnel (`scripts/tunnel.ps1`, cloudflared quick tunnel) để SePay
  gọi IPN; mọi E2E dùng provider `fake`.
- Docker compose (`make nxl-up-all`), CI GitHub, `make nxl-swag` bằng Docker (đã chạy `swag` cục bộ cùng tham số),
  `make deps-audit` (không đổi dependency).

## 5. Việc tiếp theo

- **Chủ đồ án:**
  - Đặt secret SePay trong `.env` (`BILLING_PAYMENT_PROVIDER=sepay`, merchant / secret key / API credential sandbox) và cấu hình
    IPN URL trên SePay với kiểu xác thực **SECRET_KEY** (`https://<tunnel>/v1/webhooks/sepay`); chạy WP9 theo TSD §6.15.
  - Đặt `BILLING_PAYOUT_ENC_KEY` thật (32 byte base64) — không dùng khóa dev.
  - `chat emote seed-demo --channel <id>` cho kênh demo.
  - Khi triển khai: **chat-svc trước billing-svc** (TSD §9.2) — chat cũ đọc giá trị `sub:` mới thành "không sub".
- Quyết định **E-30** (refresh khi tài khoản bị khóa, từ GĐ5) vẫn chờ chốt.
- Donate: đã có bản nháp kế hoạch (`claude/nexuslive-donate-plan.md` trong project) — dùng seam `sub_action` / `OrderApplier`.
- `docs`: errata TSD §12 (NXL-H §7.5 / §13, NXL-00, NXL-B, NXL-E), thêm endpoint một đơn + `failureReason` vào NXL-H §13.
