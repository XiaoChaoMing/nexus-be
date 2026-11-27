# Completed — Rút tiền cho streamer: vòng đời chi trả, sổ cái hai pha, PayoutProvider

Track **Full**. Working Plan: `docs/plans/working/2026-11-27-payout-settlement.md`. TSD:
`docs/plans/technical/2026-11-27-payout-settlement.md` (v1.1 — đồng bộ theo bản ship). Client:
`../nexuslive-web/docs/plans/working/2026-11-27-payout-settlement.md`.

## 1. Delivered

- **WP1 — migration + entity:** `services/billing/migrations/20261127000001_payout_settlement.{up,down}.sql` (6 trạng thái, 6 cột,
  4 ràng buộc, `one_open_idx` 3 trạng thái, kind `PAYOUT` → `PAYOUT_HOLD`, backfill hold cho `REQUESTED` cũ, `APPROVED` cũ → `PAID`
  `MANUAL/LEGACY`, unique `ledger_entries_payout_kind_idx`). Entity: `PayoutProcessing/Paid/Failed`, `LedgerPayoutHold/Reversal`,
  `Payout.CanMoveTo`, `PayoutStatus.Open/Reversed`, `PayoutReference`, `ReasonPayoutState`.
- **WP2 — sổ cái hai pha:** `CreatePayout` ghi `DEBIT PAYOUT_HOLD` cùng transaction; `payoutAllowed` dùng `Σ(CREDIT−DEBIT)`;
  `Balance` trả `(available, held)`; `Review` thay bằng `Move` (FOR UPDATE + `CanMoveTo` + reversal cho REJECTED/FAILED).
- **WP3 — PayoutProvider:** interface trong `internal/repo/provider`, bản `provider/manual`, tiêm qua `billing.Repos.Disburser`;
  `BILLING_PAYOUT_PROVIDER=manual` (`config.go`, `.env.example`, `docker-compose.yml`).
- **WP4 — REST:** `POST /v1/admin/payouts/{id}/mark-paid`, `/mark-failed`; approve → `PROCESSING`; filter 6 trạng thái; response thêm
  `provider`, `providerRef`, `failureReason`, `paidAt`, `settledAt`, `settledBy`; Swagger billing sinh lại. Route nằm dưới prefix
  `/v1/admin/payouts` có sẵn trong Nginx — không đổi edge.
- **WP5 — sự kiện + thông báo:** `pkg/events` `payout.paid|failed|rejected.v1` + `PayoutClosed`; notification-svc route 3 sự kiện →
  `PayoutClosed` (type `SUBSCRIPTION`, kind `PAYOUT_*`, link `/studio/monetization`, e-mail bắt buộc `payout_update` vi/en).
- **WP6 — web:** Studio → Monetization: 6 trạng thái, mã tham chiếu / lý do, khóa form khi có payout mở, "Rút lại", nhãn ledger
  hold/reversal, 3 kind thông báo, i18n vi/en, E2E `e2e/payout.spec.ts` mới.
- **WP7 — admin:** nút theo trạng thái (Approve/Reject, Approve lại/Mark Failed, Mark Paid/Mark Failed), dialog Mark Paid, filter 6
  trạng thái, chi tiết chi trả, xử lý 409 `PAYOUT_STATE`, nhãn audit mới, E2E `e2e/payouts.spec.ts` viết lại.
- **WP8 — tài liệu:** NXL-H v1.1 (BR-H37…H40, E-H31/H32, AC-H28/H29, §7.3, §13.5, §13.8), NXL-00 v1.7 (reason `PAYOUT_STATE`,
  sự kiện §11.1), decision-log DL-23, errata E-37, requirements-matrix, ERD-billing v1.2 (chỉ text), ADR-015, data-model v1.4,
  system-architecture v1.3, features.md v1.3, api/README v1.12.

## 2. Deviations from the plan

Đã duyệt trong TSD §8 (1–6): `INVALID_STATE` + `reason: PAYOUT_STATE` thay cho code mới, validation 400 thay 422; reference 4–64;
bấm hai lần → 409; approve lại từ `APPROVED`; dùng type thông báo `SUBSCRIPTION`; `provider_ref` "có khi PAID".

Phát sinh khi làm (TSD bump v1.1):

- **Không map 503 `PROVIDER_UNAVAILABLE`** — TSD 1.0 ghi nhầm là đã có. Thêm mapping sẽ đổi mã lỗi các luồng checkout hiện có;
  provider `manual` không bao giờ lỗi. Lỗi provider khi approve → 500, payout ở lại `APPROVED`. Admin UI có sẵn nhánh 503 (vô hại).
- **Payout `REJECTED` của GĐ6** không có hold/reversal (net 0) — bất biến "1 hold + 1 reversal" chỉ áp cho payout mới.

## 3. Issues encountered

- Python trên Windows ghi file CRLF; đã chuẩn hóa lại LF (repo `eol=lf`).
- `make nxl-swag` hỏng trên Git Bash/Windows (Docker `-w /src` bị đổi path) — chạy đúng lệnh `go tool swag init …` của target cho
  billing. Không phải lỗi của task.
- `make nxl-test` fail `TestFakeDefaults`/`TestFailFast` của billing config vì `.env` local đặt `BILLING_PAYMENT_PROVIDER=sepay`
  (lỗi có sẵn, độc lập task). Chạy lại từng module không nạp `.env`: xanh.

## 4. Validation

| Command | Result |
| --- | --- |
| `make format` | pass |
| `make nxl-lint` | 2 issue **có sẵn** (`SA1019` fasthttp `VisitAll` ở `devpsp.go`, `webhook.go` — file không đổi); 0 issue mới. Các module trước billing: 0 |
| `go -C <module> test -covermode atomic ./...` × 9 module (không `-race`: máy không có gcc) | pass (84 package ok, 0 FAIL) |
| `make nxl-test` (nạp `.env`) | fail 2 test config có sẵn — xem §3 |
| Repo test PG: `NXL_TEST_PG_URL_BILLING=…/nxl_billing_repotest go test ./internal/repo/persistent/` (DB trống riêng, **không** trỏ DB thật) | pass 10/10, gồm `TestPayouts` (hold, reversal, không double-reversal, bất biến) |
| Migration up → down → up trên DB trống | pass |
| Migration up → down → up trên **bản sao** `nxl_billing` (+1 REQUESTED, +1 REJECTED giả) | pass; khả dụng mỗi kênh không đổi ở cả 4 bước (11.100 ₫) |
| Trigger append-only sau migration; unique hold | `UPDATE` bị chặn; hold thứ hai → `23505` |
| `go tool swag init` (billing) | khác biệt ngữ nghĩa chỉ ở payout (2 path mới, 3 path + 3 definition đổi) |
| `make nxl-migrate-up` (dev) + rebuild `billing`, `notification` | pass; log không có error/warn |
| Smoke qua edge `http://localhost:8080` (chỉ đọc + đường lỗi) | 23/23 pass |
| nexuslive-web `pnpm lint && pnpm typecheck && pnpm test && pnpm build` | pass (418 test) |
| nexuslive-admin `pnpm lint && pnpm typecheck && pnpm test && pnpm build` | pass (144 test) |

| Existing / new flow re-tested | Transport | Result |
| --- | --- | --- |
| Payout legacy `APPROVED` → `PAID/MANUAL/LEGACY`, có `paidAt`, `settledAt` | REST via edge | pass |
| Filter 6 trạng thái + `ALL`; trạng thái lạ → 400 | REST via edge | pass |
| mark-paid / mark-failed / approve trên `PAID` → 409 `PAYOUT_STATE` | REST via edge | pass |
| reference sai → 400 `FORMAT`; reason ngắn → 400 `LENGTH`; id lạ → 404 | REST via edge | pass |
| Không token / token rác → 401; token creator → 403 | REST via edge | pass |
| Studio summary `balance = available + pendingPayout`; ledger không còn `PAYOUT`; payouts có trường mới | REST via edge | pass |
| Vòng đời REQUESTED→APPROVED→PROCESSING→PAID, →FAILED, →REJECTED, số dư trước/sau | repo (PG) + usecase | pass |
| Phát `payout.*.v1` + thông báo owner + e-mail `payout_update` | usecase (billing, notification) + consumer | pass |

**E2E** (stack dev, billing tạm chạy cấu hình E2E rồi trả về `.env`: `BILLING_PAYMENT_PROVIDER=fake`,
`BILLING_WEB_BASE_URL=http://localhost:5173`, `BILLING_SEPAY_METHODS=CARD,BANK_TRANSFER,NAPAS_BANK_TRANSFER`; edge tạm
`NXL_EDGE_AUTH_RATE=120r/m`, `NXL_EDGE_WRITE_RATE=1200r/m`; web dùng `E2E_CHAT_CLI="docker exec -i chat-1 /chat"`,
`E2E_BILLING_CLI="docker exec -i billing /billing"`, `E2E_BILLING_SQL="docker exec -i db psql -U user -d nxl_billing -At -c"`;
admin test order-review dùng `E2E_SEPAY_IPN_SECRET` từ `.env`):

| Spec | Result |
| --- | --- |
| web `e2e/payout.spec.ts` (request → approve → failed + thông báo in-app → Rút lại → approve → paid + mã → 409 lần hai → ledger hold/reversal) | pass |
| web `e2e/m5-subscribe.spec.ts` (hồi quy GĐ6, tới bước rút tiền Studio) | pass |
| web `e2e/m5-failed.spec.ts`, `e2e/m5-duplicate-ipn.spec.ts` (hồi quy GĐ6) | pass |
| admin `e2e/payouts.spec.ts` — reject, held → processing → paid, processing → failed, orders to review | pass 4/4 |

Sửa trong lúc chạy E2E: `nexuslive-web/e2e/gd6-helpers.ts` — admin đăng nhập một lần mỗi lượt (`asAdmin`) thay vì mỗi thao tác,
vì edge chỉ cho ~10 login/phút (`nxl_auth`) và spec bị 429.

**Not tested (and why):**

- E2E ngoài phạm vi billing (livestream, upload, moderation…): không liên quan thay đổi.
- `-race`: không có gcc trên máy; CI vẫn chạy `-race`.

## 5. Follow-ups

- E2E helper mặc định `docker compose exec chat chat` / `billing billing` không chạy được trên stack hiện tại (service `chat-1`/
  `chat-2`, binary ở `/chat`, `/billing`): sửa mặc định trong `gd6-helpers.ts`. Cảnh báo React `<div>` trong `<p>` ở admin
  `payout-detail.tsx` (`Decision` + `UserHandle` skeleton) có từ trước.
- Map 503 `PROVIDER_UNAVAILABLE` khi có rail chi trả thật (cùng lúc rà các luồng checkout).
- Webhook / job đối soát `PROCESSING → PAID/FAILED` (seam `PayoutResult` đã có).
- Tài liệu chưa cập nhật (ngoài danh sách WP8): `docs/ba/analysis/erd/README.md` (số cột billing 77 → 83, ref
  `payout_requests.settled_by`), NXL-E (kind `PAYOUT_*`, template `payout_update`), UC-H11; vẽ lại ảnh ERD billing.
- Sửa `make nxl-swag` trên Windows (`MSYS_NO_PATHCONV=1`) và 2 cảnh báo `SA1019` có sẵn.
