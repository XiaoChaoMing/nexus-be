# GĐ6 — Thanh toán & Subscription (billing-svc, SePay sandbox, mốc M5)

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (service mới billing, migration, sự kiện mới, env bắt buộc mới, Nginx, tích hợp bên thứ ba, cross-repo web + admin) | **Approved** (19/10: G6-Q1…Q11 theo đề xuất; design GĐ6 đã có trên pen.dev) | 2026-10-16 |

Lịch lộ trình GĐ6: 25/01 → 05/02 (tuần 16–17), mốc **M5**. GĐ5 xong 16/10 nên bắt đầu sớm ~14 tuần. Spec chính:
NXL-H v1.0 (PSP đã chốt: SePay Payment Gateway sandbox — DL-19). `nxl_billing` đã có migration GĐ0 (`init_billing`:
tier, sub, đơn, giao dịch, sổ cái chỉ-thêm, rút tiền; `outbox`, `processed_events`) nhưng **chưa có code**. chat-svc đã có
bản sao `sub:{channelId}` (Redis, giá trị chỉ là `tier`, nguồn duy nhất là `chat dev-grant sub`), `CHAT_SUBS_ONLY`, cột
`emotes.tier_required` (chưa đọc), chủ đề `user:`; notification-svc đã có loại `SUBSCRIPTION` trong CHECK; channel-svc có
snapshot nội bộ `/internal/v1/channels`. Chủ đồ án đã tạo tài khoản SePay sandbox.

## 1. Task Brief

**Mục tiêu (M5 — lộ trình §3 GĐ6):**

- **Hành trình Thanh toán trên sandbox:** viewer bấm Subscribe → chọn tier → xem lại → phương thức → xác nhận → trang SePay
  sandbox → quay về trang kết quả → badge Sub theo tier, emote kênh mở khóa, chat được ở Subs-only → quản lý / hủy gia hạn →
  Creator thấy doanh thu ở Studio, yêu cầu rút tiền, Admin duyệt.
- **Idempotent:** gửi lại cùng IPN hai lần → chỉ một giao dịch, hai bút toán, một sự kiện (AC-H05).
- **Thất bại có lý do (S6):** hủy / lỗi / hết hạn hiện đúng câu theo `failureReason` + Thử lại.
- **Trả trước theo kỳ:** 30 ngày / kỳ, VND; gia hạn = nhắc trước 3 ngày + checkout mới (không tự trừ tiền — BR-H10).

**Nguồn:**

| Nguồn | Phần dùng |
| --- | --- |
| Lộ trình §3 GĐ6, M5; features H1, H2, H4, B2, B3 | Phạm vi, kịch bản M5 |
| NXL-H v1.0 | Toàn bộ: §3 luồng, §6 BR-H01…H36, §7 state machine + migration §7.5, §10 AC, §13 API + SePay, §17 rollout, §18 NH-Q |
| NXL-00 v1.5 | Envelope, `Idempotency-Key` §8, `user:` §10.3, outbox, sự kiện §11, mã lỗi §5 |
| NXL-B v1.2 BR11, BR15, BR17, BR21 | Subs-only thật, badge tier, emote khóa theo tier, system message |
| NXL-E v1.1 | Loại `SUBSCRIPTION` (BR-E01/E02), email biên nhận / nhắc gia hạn |
| NXL-G2 v1.2 | Studio → "Thu nhập", ô "Doanh thu hôm nay" trên Dashboard |
| NXL-F v1.1 | Audit (`audit.recorded.v1` → safety), kênh tạm ngưng |
| Tài liệu SePay đọc lại 16/10 (§9.1) | Form, IPN, API tra cứu / hủy, sandbox |

**Trong phạm vi:** WP1–WP12 ở §2.

**Ngoài phạm vi (NXL-H §11):** tự trừ tiền định kỳ, gói 3 tháng / năm, đổi tier khi còn hiệu lực, Gift Sub / Donate, hoàn
tiền, chi trả thật, Creator tự đặt giá hay tải emote, SePay production, phương án dự phòng webhook chuyển khoản (chỉ dùng
nếu sandbox Payment Gateway hỏng — không cần vì tài khoản đã có).

**Giả định:** không thêm thư viện Go (HMAC, AES-GCM, `net/http` stdlib; base32 Crockford tự viết); web / admin không thêm
gói; billing dùng cùng Dockerfile như service khác (không image mới); số tiền là `int64` VND; email qua Mailpit.

**Câu hỏi mở:** §10.

## 2. Gói việc

### WP1 — Nền billing-svc + bản sao kênh + tier

- `services/billing` (cổng 8098, G6-Q4) theo mẫu `services/stream`: config, app, auth JWKS, outbox worker, consumer bền,
  `/metrics`, Swagger, `/healthz`; `go.work`, Makefile, CI matrix (sửa workflow có sẵn), compose, Prometheus.
- Migration mới theo NXL-H §7.5 (`checkout_orders` thêm cột / trạng thái `CANCELLED`, `VOIDED`; `transactions.type` thêm
  `VOID`, `source`; `subscriptions.expiring_notified_for`, `expired_reason`; `payout_requests.reject_reason`, `account_last4`;
  bảng `channels` (thêm `avatar_url`), `idempotency_keys`) — không sửa migration GĐ0.
- `pkg/events`: `subscription.activated | renewed | updated | expiring | expired`, `payment.succeeded | failed | voided`
  (payload NXL-H §13.8); billing thêm người nghe `channel.created | updated | suspended | restored`, nguồn `audit.recorded`.
- Bản sao kênh + 3 tier mặc định (59.000 / 119.000 / 299.000 ₫) khi nhận `channel.created.v1`; lệnh
  `billing backfill-channels` từ snapshot channel-svc cho kênh cũ.
- Middleware `Idempotency-Key` (NXL-00 §8, BR-H23): cùng body → response đã lưu, khác body → `IDEMPOTENCY_KEY_REUSED`, đang
  chạy → `CONFLICT`; job xóa sau 24 giờ.
- Nginx (đặt trước route cũ, test từng tiền tố): `/v1/channels/{id}/subscription-tiers`, `/v1/checkout/`,
  `/v1/me/(subscriptions|payments)`, `/v1/studio/monetization/`, `/v1/admin/(payouts|checkout-orders)` (trước `/v1/admin` →
  safety), `/v1/webhooks/sepay` (không JWT / CORS, body ≤ 64 KB, 60 / phút / IP).

### WP2 — Checkout + `PaymentProvider` (SePay, Fake) + IPN

- `PaymentProvider` (ADR-009) sửa theo tài liệu mới (§9.1): `BuildCheckout` (ký form, không gọi mạng), `ParseNotification`,
  `QueryOrder` (`GET /v1/order/detail/{invoice}`), `ListOrders(from, to)` (`GET /v1/order`, `per_page` ≤ 100) thay
  `ListTransactions`, `CancelOrder` (`POST /v1/order/cancel`, G6-Q5). Hiện thực `sepay` (Basic auth, timeout 3 s) và `fake`.
- `POST /v1/checkout/orders` (BR-H05…H09, H18, H19, AF-H02/H03): đơn `PENDING`, `invoiceNumber` `NXL` + 10 ký tự, ký form
  BR-H24 (`fields[]` cùng thứ tự với chuỗi ký, `order_description` ASCII); `GET …/{id}`, `POST …/{id}/sync`,
  `POST …/{id}/cancel`; URL trả về dựng từ `BILLING_WEB_BASE_URL`.
- `POST /v1/webhooks/sepay`: `X-Secret-Key` so sánh thời gian hằng → `401`; bỏ qua loại khác; đối chiếu BR-H25 (số tiền
  dạng `"59000.00"`); một transaction DB: `transactions` (unique `(psp_ref, type)` → trùng = `200`), đơn (update có điều
  kiện, BR-H20 tiền thắng), sub, sổ cái, outbox; `TRANSACTION_VOID` (AF-H09/H10); che dữ liệu thẻ (BR-H36).
- Provider `fake` + trang `/dev/fake-psp` (Trả / Hủy / Lỗi, gửi IPN đúng định dạng SePay) — chỉ `NXL_ENV=dev`, dùng cho CI,
  E2E và dự phòng khi demo.
- Fail fast khi `BILLING_PAYMENT_PROVIDER=sepay` thiếu credential hoặc lệch môi trường (E-H17).

### WP3 — Subscription + job

- Áp thanh toán (NXL-H §3.5): mới / gia hạn nối kỳ / khác tier → `needs_review` + audit.
- `GET /v1/me/subscriptions`, `POST …/{id}/cancel` (IK) → `CANCELLED_PENDING_END`, `POST …/{id}/resume`,
  `GET /v1/me/payments`.
- Job (khóa Redis để chạy một bản): `reconcile-pending` 1 phút (đơn > 2 phút: tra cứu → `PAID`; quá 30 phút → `EXPIRED` +
  `CancelOrder` best-effort), `subscription-expiry` 1 phút (nhắc 3 ngày một lần mỗi kỳ, hết kỳ → `EXPIRED`, ≤ 500 dòng),
  `reconcile-daily` 02:00 `Asia/Ho_Chi_Minh` (liệt kê đơn ngày trước, áp đơn thiếu, lệch → metric + audit), dọn
  `idempotency_keys`.
- Lệnh: `billing republish-entitlements` (dựng lại bản sao chat), `billing dev-ipn replay|synth` (G6-Q3, chỉ dev).

### WP4 — Sổ cái, Studio Monetization, rút tiền, API Admin

- Sổ cái khi `PAID` / `VOIDED` (BR-H29…H31, phí 5% `floor`); số dư / khả dụng tính từ sổ cái (BR-H32).
- Studio (tra kênh theo `owner_id = token.sub`, `NOT_CREATOR`): `GET|PATCH /v1/studio/monetization/tiers`, `summary?range=&tz=`
  (`todayNet` cho Dashboard), `ledger`, `GET|POST payouts` (IK, `PAYOUT_MIN` 100.000 ₫, một `REQUESTED` / kênh, thông tin
  ngân hàng AES-256-GCM, danh sách mã ngân hàng cố định — G6-Q10).
- Admin: `GET /v1/admin/payouts`, `POST …/{id}/approve | reject` (bút toán `DEBIT PAYOUT`; không chuyển tiền thật),
  `GET /v1/admin/checkout-orders?needsReview=true`; audit `PAYOUT_*`, `PAYOUT_BANK_INFO_VIEWED`.

### WP5 — chat-svc: quyền lợi Sub thật

- Consumer `subscription.activated | renewed | updated | expired` → `sub:{channelId}` = `{tier, until}` (G6-Q6); `until ≤
  now` coi như hết quyền (BR-H13); Subs-only, badge "Sub T1/T2/T3" (BR15), emote kênh `tier_required ≤ tier`; Owner mọi emote,
  Mod / VIP / Admin emote Tier 1 (BR-H16, NH-Q8).
- Emote kênh: `GET /v1/chat/emotes?channelId=` trả `tierRequired`; nạp emote kênh bằng lệnh `chat emote add --channel --tier`
  + seed demo (G6-Q7).
- System message `SUB_NEW | SUB_RENEW` khi phòng `OPEN` (BR21); `payment.succeeded | failed | voided` → `payment_updated` trên
  `user:{userId}`.
- `dev-grant sub` ghi định dạng mới (giữ cho dữ liệu demo).

### WP6 — notification-svc: loại `SUBSCRIPTION`

- Consumer `payment.succeeded` (in-app + email biên nhận, luôn gửi), `payment.failed` (in-app), `payment.voided` (in-app +
  email), `subscription.expiring` (in-app + email), `subscription.expired` (in-app), `subscription.activated` → chủ kênh
  (in-app, gộp 60 phút) (BR-H27); bản sao kênh đã có cho tên kênh.
- `SUBSCRIPTION` vào cài đặt (BR-E02 ✓ / ✓, biên nhận không tắt được); mẫu email vi / en: biên nhận, sắp hết hạn, giao dịch bị
  hủy.

### WP7 — Web người dùng (`nexuslive-web`)

- Nút Subscribe (Secondary, trạng thái BR-H12) trên trang xem + hồ sơ kênh; Guest → đăng nhập rồi mở lại modal.
- Modal Subscribe 4 bước (tier → xem lại → phương thức → xác nhận), `/checkout?tierId=` cho mobile, màn chuyển sang SePay
  (form tự submit đúng thứ tự `fields[]`, nút dự phòng), `/checkout/result` (sync + `payment_updated` + poll 3 s / 2 phút).
- `/settings/subscriptions` (hiện tại / đã hết hạn, hủy / bật lại gia hạn, gia hạn), `/settings/payments`.
- Studio → **Thu nhập** `/studio/monetization` (Tier · Tổng quan · Sổ cái · Rút tiền), ô "Doanh thu hôm nay" trên Dashboard;
  chat: badge tier, emote khóa, system message sub; namespace i18n `billing`, `studio.monetization`.

### WP8 — `nexuslive-admin`

- Mục mới **Rút tiền** (`/payouts`: danh sách theo trạng thái, xem thông tin ngân hàng đầy đủ có audit, duyệt / từ chối kèm lý
  do) và **Đơn cần xem lại** (`/payments/review`, chỉ đọc); hỏi lại 30 s như GĐ5.

### WP9 — SePay sandbox: cấu hình và kiểm chứng (cùng chủ đồ án)

- **A. Credential (ông chủ tự làm):** lấy MERCHANT ID, SECRET KEY sandbox trên my.sepay.vn → tự điền `.env`:
  `BILLING_SEPAY_MERCHANT_ID`, `BILLING_SEPAY_SECRET_KEY`, `BILLING_SEPAY_IPN_SECRET`. Em **không** gõ, đọc hay chụp màn hình
  chứa mật khẩu / secret; trang thông tin tích hợp hiện secret thì ông chủ mở và che trước khi em xem.
- **B. Dò dashboard (em điều hướng trình duyệt, ông chủ xác nhận):** chỗ cấu hình IPN URL + kiểu xác thực `SECRET_KEY` (giá trị
  `X-Secret-Key` là SECRET KEY hay khóa riêng), phương thức đang bật (thẻ / VietQR / NAPAS), cách sandbox giả lập thành công /
  thất bại / hủy, có cho `success_url` là `localhost` không.
- **C. Đặt IPN URL** = `https://<tunnel>/v1/webhooks/sepay` khi có tunnel (G6-Q2); không có tunnel → chỉ `sync` + reconcile.
- **D. Kiểm chứng sandbox (ghi lại):** AC-H03, H04, H08, H09, H12 trên `pay-sandbox.sepay.vn`; lưu IPN thật + response
  `order/detail` (đã che) làm fixture unit test; chốt bảng giá trị `order_status` / `transaction_status` vào TSD.

### WP10 — Brief design GĐ6 (chạy song song)

- Viết `nexuslive-design-brief-gd06` ngay bước 1 cho pen.dev: nút Subscribe (3 trạng thái), modal 4 bước + bản `/checkout`
  mobile, màn chuyển sang SePay, `/checkout/result` (Đang xác nhận · Thành công · Chưa thanh toán được, 6 lý do),
  `/settings/subscriptions`, `/settings/payments` (+ trống), Studio Thu nhập 4 tab + ô Dashboard, badge Sub T1–T3, emote
  khóa, system message sub, thông báo `SUBSCRIPTION`, email biên nhận, admin Rút tiền + Đơn cần xem lại, trang
  `/dev/fake-psp` (đơn giản). Dựng theo token + spec trong lúc chờ frame.

### WP11 — Tài liệu

- NXL-H v1.1 (đóng NH-Q theo §10, §13.6 theo §9.1, cổng 8098), NXL-00 v1.6 (sự kiện, `CANNOT_SUBSCRIBE_SELF`,
  `PAYMENT_*` → bỏ, hằng số §17), NXL-B (BR11/15/17 GĐ6, Q12), NXL-E (`SUBSCRIPTION`), NXL-G2 (Thu nhập), ADR-009 (đã chọn
  SePay), API catalog, kiến trúc §4, ERD billing, errata (FR-MN-01, roadmap "chữ ký webhook", §9.1).
- TSD `docs/plans/technical/2026-10-16-gd06-billing-subscriptions.md` + Completed Plan; README / `.env.example` (cách lấy
  credential, chạy có / không tunnel).

### WP12 — Kiểm thử và M5

- Unit (vector chữ ký cố định, parse số tiền, phí `floor`, nối kỳ / trần 365 ngày, ánh xạ trạng thái SePay), use case (mọi
  nhánh E-H / AF-H), repo test PG (IPN trùng song song → 1 dòng, trigger chỉ-thêm, idempotency), lint, Swagger, migration
  up → down → up.
- E2E (provider `fake`): **M5 Thanh toán** (subscribe → kết quả → badge / emote / Subs-only → hủy gia hạn → Studio +56.050 ₫
  → rút tiền → Admin duyệt), **M5 IPN trùng**, **M5 thất bại** (hủy / lỗi / hết hạn); hồi quy M1–M4.
- Sandbox thật (WP9 D) chạy tay, ghi kết quả vào Completed Plan.

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Gói |
| --- | --- | --- |
| Service mới | `services/billing` (code đầu tiên; migration GĐ0 giữ nguyên) | WP1–WP4 |
| chat-svc | consumer `subscription.*` / `payment.*`, `sub:` đổi giá trị, emote kênh + tier, badge, system message, `dev-grant` | WP5 |
| notification-svc | nguồn `SUBSCRIPTION`, mẫu email, cài đặt | WP6 |
| `pkg/events` | 8 sự kiện mới (chỉ thêm) | WP1 |
| Hạ tầng | compose (billing), Nginx (6 tiền tố + webhook), Prometheus, `.env.example`, CI matrix | WP1 |
| Web, admin | `features/billing` (mới), `features/studio/monetization` (mới), settings, chat, watch / channel; admin 2 trang | WP7, WP8 |

| Transport | Có | Ghi chú |
| --- | --- | --- |
| REST công khai | Có | ~22 endpoint (NXL-H §13.1–§13.5) |
| Webhook bên ngoài | Có | `POST /v1/webhooks/sepay` (không envelope NXL-00) |
| Gọi ra bên ngoài | Có | `pgapi-sandbox.sepay.vn` (tra cứu, liệt kê, hủy đơn) |
| WebSocket | Có | `user:` thêm `payment_updated`; `chat:` system message sub |
| NATS | Có | 8 sự kiện mới; billing nghe `channel.*`, phát `audit.recorded` |

Luồng cũ có thể vỡ: route `/v1/me/*`, `/v1/channels/*`, `/v1/studio/*`, `/v1/admin/*` (thêm nhánh billing trước nhánh cũ);
Subs-only và badge (đổi định dạng `sub:`, dữ liệu `dev-grant` cũ); danh sách emote (thêm emote kênh); E2E M3 / M4 dựa vào
`dev-grant sub`; cài đặt thông báo (thêm loại thứ 9).

## 4. Mã lỗi

Mới: `CANNOT_SUBSCRIBE_SELF` (422). Dùng lần đầu: `IDEMPOTENCY_KEY_REUSED` (422, đã có trong NXL-00 §5). Có sẵn:
`VALIDATION_FAILED` (`details.idempotencyKey = REQUIRED`), `CONFLICT` (+ `details.limit`), `NOT_FOUND`, `INVALID_STATE` +
`details.reason` (`TIER_INACTIVE`, `TIER_CHANGE`, `MAX_PREPAID`, `ORDER_PENDING`, `INSUFFICIENT_BALANCE`, `PAYOUT_PENDING`),
`CHANNEL_SUSPENDED`, `NOT_CREATOR`, `FORBIDDEN`, `RATE_LIMITED`. Bỏ dòng `PAYMENT_*` dự phòng ở NXL-00. Webhook trả mã
HTTP thuần (`200 / 400 / 401 / 500`).

## 5. Sinh code / cấu hình / tài liệu

- TSD `docs/plans/technical/2026-10-16-gd06-billing-subscriptions.md`.
- `make nxl-swag` (billing, chat, notification), mock, migration up → down → up (billing, chat nếu cần, notification).
- Env mới: `BILLING_HTTP_PORT`, `BILLING_PAYMENT_PROVIDER` (`sepay` | `fake`), `BILLING_SEPAY_ENV` (`sandbox`),
  `BILLING_SEPAY_CHECKOUT_URL`, `BILLING_SEPAY_API_URL`, `BILLING_SEPAY_MERCHANT_ID`, `BILLING_SEPAY_SECRET_KEY`,
  `BILLING_SEPAY_IPN_SECRET`, `BILLING_SEPAY_IPN_VERIFY_VIA_API`, `BILLING_SEPAY_METHODS` (G6-Q9), `BILLING_SEPAY_API_TIMEOUT`, `BILLING_WEB_BASE_URL`,
  `BILLING_PAYOUT_ENC_KEY` (bắt buộc), `BILLING_TIER_PRICES`, `BILLING_PLATFORM_FEE_BPS`, `BILLING_PAYOUT_MIN`,
  `BILLING_ORDER_TTL`, `BILLING_RECONCILE_AFTER`, `BILLING_RECONCILE_INTERVAL`, `BILLING_RENEW_REMINDER_DAYS`. Secret chỉ ông
  chủ điền vào `.env`; `.env.example` để trống.

## 6. Thứ tự

| # | Việc | Phụ thuộc |
| --- | --- | --- |
| 1 | Chốt §10, TSD, WP10 brief design, WP9 A–B (credential + dò dashboard) | Plan duyệt |
| 2 | WP1 khung billing + sự kiện + Nginx + bản sao kênh / tier | 1 |
| 3 | WP2 checkout / IPN / provider, WP3 subscription + job | 2 |
| 4 | WP4 sổ cái, Studio, rút tiền, API Admin | 3 |
| 5 | WP5 chat, WP6 notification (song song) | 2 (hợp đồng sự kiện) |
| 6 | WP7 web, WP8 admin (từ bước 3 khi hợp đồng đã chốt) | 3–5 |
| 7 | WP9 C–D sandbox thật | 3, 6 |
| 8 | WP11 tài liệu, WP12 E2E M5, Completed Plan | 1–7 |

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| IPN và (theo tài liệu) cả URL chuyển hướng cần địa chỉ công khai; stack dev chạy `localhost` | `sync` + reconcile không cần IPN (AC-H12); `billing dev-ipn`; tunnel chỉ khi ông chủ duyệt (G6-Q2); WP9 B kiểm `localhost` |
| Sandbox không có tài liệu giả lập thất bại / từ chối thẻ → AC-H09 có thể không tái hiện được | Dò ở WP9 B; không được thì chứng minh bằng provider `fake` + ghi Completed Plan |
| Bảng giá trị `order_status` / `transaction_status` chưa đủ (`CAPTURED` ở IPN, `COMPLETED` ở API) | Ánh xạ trong adapter, giá trị lạ → `UNKNOWN` + log; chốt bằng fixture WP9 D |
| Lộ secret khi em thao tác dashboard | Ông chủ tự nhập / che màn hình chứa secret; không `get_page_text` trang credential; không log `X-Secret-Key`, `signature` (AC-H27) |
| Sai tiền / áp hai lần khi IPN, `sync`, reconcile chạy cùng lúc | Một transaction DB, unique `(psp_ref, type)`, update có điều kiện, `SELECT … FOR UPDATE` sub; test song song trên PG thật |
| Đổi định dạng `sub:` làm vỡ Subs-only / badge và E2E M3–M4 | Đọc được cả định dạng cũ; `dev-grant` ghi định dạng mới; `republish-entitlements`; chạy lại E2E M3 / M4 |
| Route billing chồng `/v1/me`, `/v1/channels`, `/v1/studio`, `/v1/admin` | Location cụ thể đặt trước; test định tuyến từng tiền tố như GĐ4 / GĐ5 |
| Phạm vi ~24–28 ngày công (service mới + 2 app) | Còn dư ~14 tuần; cắt `reconcile-daily` và trang `/payments/review` nếu thiếu thời gian |
| Thiếu design (modal, kết quả, Studio Thu nhập, admin) | Brief GĐ6 ngay bước 1; dựng theo token + spec |

## 8. Kiểm tra

- [ ] `make format`, `make nxl-lint`, `make nxl-test`, repo test PG billing / chat / notification, migration up → down → up.
- [ ] Stack cục bộ đủ 9 service + E2E M5 (fake), hồi quy M1–M4; sandbox SePay theo WP9 D.
- [ ] Hostile: IPN thiếu / sai secret, JSON hỏng, số tiền lệch, hóa đơn lạ, IPN trùng song song, IPN tới sau `EXPIRED`,
  VOID trùng / cho đơn chưa trả, cùng key khác body, subscribe kênh mình, đổi tier khi còn hạn, vượt 365 ngày, đơn của người
  khác (IDOR), `sync` khi SePay timeout, rút tiền vượt số dư / dưới mức tối thiểu / số dư âm, Admin duyệt hai lần, người thường
  gọi API Studio / Admin, kênh bị tạm ngưng, log / DB không chứa secret hay số thẻ.

## 9. Lệch so với lộ trình / spec

| Nguồn | Yêu cầu | Làm | Lý do |
| --- | --- | --- | --- |
| Lộ trình GĐ6 | "Webhook có xác minh chữ ký" | `X-Secret-Key` + đối chiếu đơn + tra cứu API | IPN của SePay không có chữ ký (tài liệu 16/10) |
| Lộ trình GĐ6, features H1 | "Gia hạn mô phỏng bằng job" / "tự gia hạn" | Job nhắc gia hạn + checkout mới; không trừ tiền | BR-H10, NH-Q1; SePay chưa có thanh toán định kỳ |
| NXL-H §13.6 | `ListTransactions(from, to)` | `ListOrders(from, to)` + `CancelOrder` | API chỉ có liệt kê đơn và hủy đơn (§9.1) |
| NXL-H §7.5 | Bản sao `channels` không có ảnh | Thêm `avatar_url` | `Sub.channel.avatarUrl` (§13.3) cần |
| NXL-H BR-H17 | CLI emote "Admin vận hành" | `chat emote add` chỉ CLI + seed demo | Không có UI tải emote (UC-G10) |

### 9.1 SePay — khác so với NXL-H §13.6 (đọc lại 16/10, đề xuất errata E-32…E-37)

| # | NXL-H §13.6 | Tài liệu / SDK hiện tại | Xử lý |
| --- | --- | --- | --- |
| E-32 | Đường dẫn API tra cứu "xác nhận khi tích hợp" (NH-Q3) | `pgapi-sandbox.sepay.vn/v1`: `GET order/detail/{invoice}`, `GET order` (lọc `order_status`, `from_created_at`, `to_created_at`, `customer_id`, `q`, `per_page` ≤ 100, `page`), `POST order/cancel` (QR), `POST order/voidTransaction` (thẻ) — theo SDK Node chính thức; **không có** API liệt kê giao dịch | Đóng NH-Q3; đổi interface (§9); `reconcile-daily` dùng `GET order` |
| E-33 | `order_status = CAPTURED` (xác nhận khi tích hợp) | IPN ví dụ `CAPTURED`; ví dụ liệt kê đơn `COMPLETED`; không có bảng giá trị đầy đủ | Chấp nhận cả hai là "đã trả"; chốt bằng fixture WP9 D |
| E-34 | Chỉ IPN cần URL công khai | Trang form ghi `success_url` / `error_url` / `cancel_url` "chỉ hoạt động khi ứng dụng chạy trên domain hoặc IP công khai" (gợi ý ngrok / localtunnel) | Kiểm ở WP9 B; nếu đúng, `BILLING_WEB_BASE_URL` = URL tunnel khi kiểm sandbox |
| E-35 | "SePay không có thanh toán định kỳ trong tài liệu" | Trang luồng thanh toán ghi định kỳ "sắp ra mắt"; SDK có trường `agreement_*` chưa có tài liệu | Giữ trả trước (NH-Q1); ghi chú ở NXL-H §11 |
| E-36 | Sandbox "có luồng demo" | Không tài liệu thẻ thử / cách ép thất bại; Bắt đầu nhanh: tạo đơn → quay lại màn hướng dẫn tích hợp để xem kết quả | WP9 B; AC-H09 có thể dùng `fake` |
| E-37 | Thứ tự ký = danh sách cố định | PHP mẫu duyệt danh sách cố định, SDK Node duyệt theo thứ tự trường của form; tài liệu yêu cầu giữ thứ tự input như form mẫu. README SDK ghi host `sandbox.pay.sepay.vn`, code và tài liệu dùng `pay-sandbox.sepay.vn`. `X-Secret-Key` chỉ gửi khi IPN chọn kiểu xác thực `SECRET_KEY`. Số tiền IPN dạng `"50000.00"` | BR-H24 giữ nguyên: `fields[]` cùng thứ tự với chuỗi ký (thỏa cả hai); host qua env; WP9 B chọn `SECRET_KEY`; BR-H25 đã xử lý phần lẻ |

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| G6-Q1 | Chấp nhận đề xuất NH-Q1, Q4–Q9, Q11–Q13 của NXL-H? (trả trước + nhắc gia hạn, errata FR-MN-01; chỉ gói 30 ngày; không đổi tier khi còn hạn; `order_description` ASCII ≤ 100; một trạng thái `PENDING`; Mod / VIP / Admin dùng emote Tier 1; không giữ tiền, số dư âm thì không rút; không Gift Sub; phí 5%; tier tạo từ `channel.created.v1`) | Chấp nhận tất cả; NH-Q2 đã xong (có tài khoản), NH-Q3 đóng theo E-32, NH-Q10 → G6-Q2 |
| G6-Q2 | Tunnel HTTPS cho IPN (cloudflared / ngrok = công cụ hoặc image Docker mới)? | Không đưa vào compose. Ông chủ cho phép chạy **cloudflared quick tunnel** thủ công (không tài khoản) chỉ khi kiểm IPN sandbox (WP9 D) và buổi demo; ngày thường dùng `sync` + reconcile + `billing dev-ipn` |
| G6-Q3 | Giả lập IPN cục bộ: lệnh CLI hay endpoint HTTP dev? | Lệnh `billing dev-ipn replay --transaction <id> --times N` (gửi lại IPN đã lưu) và `synth --invoice` (dựng IPN từ `order/detail` thật), gửi kèm `X-Secret-Key` tới `localhost`; chỉ `NXL_ENV=dev`; không mở endpoint mới |
| G6-Q4 | Cổng billing? | 8098 (NXL-H §17; chưa service nào dùng) |
| G6-Q5 | Gọi `POST order/cancel` của SePay khi người dùng hủy đơn / đơn hết hạn? | Có, best-effort (lỗi chỉ log): giảm trường hợp trả muộn; BR-H20 vẫn áp nếu tiền vẫn tới. Không dùng `voidTransaction` ngoài lệnh dev để kiểm AC-H21 |
| G6-Q6 | Giá trị `sub:{channelId}` ở chat: giữ `tier` hay `{tier, until}`? | `"{tier}:{untilUnix}"`, đọc được cả `"{tier}"` cũ (coi như không hạn); `dev-grant sub --minutes` ghi định dạng mới |
| G6-Q7 | Emote kênh có tier lấy từ đâu? | Lệnh `chat emote add --channel --tier --code --file` (ảnh vào MinIO `nxl-public`) + seed 3 emote cho kênh demo; không UI Studio |
| G6-Q8 | Admin: làm trang Rút tiền + Đơn cần xem lại trong `nexuslive-admin`? | Có cả hai (Đơn cần xem lại chỉ đọc); cắt trang này đầu tiên nếu thiếu thời gian |
| G6-Q9 | Phương thức trên modal khi sandbox chưa bật thẻ / NAPAS? | Chỉ hiện phương thức `BILLING_SEPAY_METHODS` (cấu hình, mặc định theo WP9 B) + "Chọn trên trang SePay" |
| G6-Q10 | Danh sách ngân hàng rút tiền | ~12 mã ngân hàng phổ biến (VCB, TCB, BIDV, VTB, ACB, MB, VPB, TPB, STB, VIB, SHB, HDB) hằng trong code; `BILLING_PAYOUT_ENC_KEY` 32 byte base64 bắt buộc |
| G6-Q11 | Design GĐ6 chưa có | Em viết brief GĐ6 (WP10) cho agent design trên pen.dev; dựng theo token + spec trong lúc chờ |

## 11. Tiêu chí hoàn thành (M5)

- Given kênh mới (`channel.created.v1`), Then modal Subscribe có 3 tier 59.000 / 119.000 / 299.000 ₫; Creator mở kênh mình → chỉ
  xem, "Bạn không thể subscribe kênh của mình".
- Given viewer tạo đơn Tier 1, Then `201`, `fields[]` đúng thứ tự BR-H24, chữ ký khớp HMAC tính lại; trình duyệt sang
  `pay-sandbox.sepay.vn`; bấm lại cùng key → cùng đơn; cùng key khác tier → `422 IDEMPOTENCY_KEY_REUSED`.
- Given thanh toán thành công trên sandbox, Then trang kết quả hiện Thành công (≤ 5 s sau IPN, hoặc ≤ 3 phút khi không có tunnel
  qua `sync` / reconcile); sub `ACTIVE` hết hạn `paidAt + 30 ngày`; tin chat kế tiếp có badge Sub T1, emote Tier 1 hiển thị, chat
  được khi Subs-only; email biên nhận tới Mailpit; Creator thấy system message + thông báo.
- Given cùng IPN gửi lại 2 lần, Then cả hai `200 {"success": true}`; `transactions` 1 dòng, `ledger_entries` 2 dòng,
  `subscription.activated.v1` 1 lần.
- Given hủy trên trang SePay / lỗi / đóng tab 30 phút, Then "Bạn đã hủy thanh toán" / lý do `DECLINED` hoặc `UNKNOWN` / "Đơn đã
  hết hạn sau 30 phút" + Thử lại; IPN tới muộn cho đơn `EXPIRED` → `PAID`.
- Given IPN sai `X-Secret-Key` → `401`, DB không đổi; số tiền lệch → `FAILED AMOUNT_MISMATCH`, không có sub.
- Given gia hạn ngày 10/02 sub hết hạn 20/02, Then hết hạn mới 22/03; Hủy gia hạn → giữ quyền tới hết kỳ, hủy lần 2 → `409`;
  còn 3 ngày → đúng 1 nhắc (in-app + email); hết kỳ → ≤ 1 phút `EXPIRED`, mất badge, Subs-only → `CHAT_SUBS_ONLY`.
- Given thanh toán 59.000 ₫, Then Studio: gộp 59.000, phí 2.950, thực nhận 56.050, Dashboard "Doanh thu hôm nay" 56.050 ₫; rút
  50.000 → `MIN`, 100.000 → `INSUFFICIENT_BALANCE`; Admin duyệt yêu cầu hợp lệ → `APPROVED`, số dư giảm, duyệt lại → `409`.
- Không có secret, `X-Secret-Key`, `signature`, số thẻ đầy đủ hay số tài khoản trong log và `raw_webhook`.
