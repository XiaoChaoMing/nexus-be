# Rút tiền cho streamer — Vòng đời chi trả, sổ cái hai pha và PayoutProvider

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| **Full** (billing-svc: migration, entity, repo, usecase, REST, provider mới, config; `pkg/events`; notification-svc; nexuslive-web; nexuslive-admin; tài liệu) | **Approved** 2026-11-27 — khung dung hòa A+B+C chốt tại brainstorm | 2026-11-27 |

TSD (sẽ viết): `docs/plans/technical/2026-11-27-payout-settlement.md` — DDL, hợp đồng API, sơ đồ tuần tự, bất biến sổ cái. Web (WP6) có thể tách Working Plan riêng cùng basename trong `../nexuslive-web/docs/plans/working/`.

## 1. Task Brief

**Bối cảnh / vấn đề.** Phần rút tiền GĐ6 coi "admin Duyệt" = "đã chi tiền": khi Approve, billing-svc ghi ngay `DEBIT/PAYOUT` trừ số dư, và `APPROVED` là trạng thái cuối. Thực tế việc chuyển khoản là thủ công/sandbox và có thể thất bại (sai số tài khoản, ngân hàng từ chối), nhưng hệ thống **không có** trạng thái "đang xử lý / đã chi / thất bại", **không lưu** mã tham chiếu giao dịch, và **không có cách hoàn tiền** về số dư nếu chuyển khoản hụt → tiền bị trừ nhưng chưa chắc tới creator, không đối soát được.

**Mục tiêu.** Tách "duyệt" khỏi "đã chi"; mô hình hóa việc chi trả như một vòng đời có thể thất bại và hoàn lại; ghi sổ cái theo kế toán kép; trừu tượng hóa bước chi tiền sau một interface để sau này thay bằng rail thật.

**Giải pháp đã chốt (dung hòa A+B+C):**

- **(A) Máy trạng thái:** `REQUESTED → APPROVED → PROCESSING → PAID`, với `REJECTED` (từ REQUESTED) và `FAILED` (từ APPROVED/PROCESSING) là nhánh thoát.
- **(B) Sổ cái hai pha:** giữ tiền bằng `DEBIT/PAYOUT_HOLD` ngay lúc REQUEST; hoàn lại bằng `CREDIT/PAYOUT_REVERSAL` khi REJECTED/FAILED; **không** ghi thêm khi PAID (dòng hold chính là khoản tiền đã rời sổ). Số dư khả dụng = thuần `Σ(CREDIT − DEBIT)`, bỏ truy vấn "pending".
- **(C) PayoutProvider:** interface `PayoutProvider` (bản `manual` cho đồ án) đối xứng với `PaymentProvider` (ADR-009); bước reconcile `PROCESSING → PAID/FAILED` do admin đánh dấu (demo) hoặc webhook/job (sau này — chỉ chừa seam).

**Nguồn:** PRD v2.1 (thu nhập Creator), NXL-H (spec thu nhập), ADR-009 (gateway provider thanh toán — mẫu nhân bản), GĐ6 Completed Plan `2026-10-16-gd06-billing-subscriptions.md`, mã hiện có trong `services/billing`.

**Không làm (non-goals):** rail chi trả / ngân hàng thật; KYC/thuế; phí rút riêng; rút ngoại tệ; job đối soát bất đồng bộ tự động (chỉ chừa seam).

## 2. Gói việc

### WP1 — Dữ liệu & máy trạng thái (billing-svc, migration mới)

- Migration `20261127000001_payout_settlement`:
  - `payout_requests.status` CHECK mở rộng: `REQUESTED, APPROVED, PROCESSING, PAID, REJECTED, FAILED`.
  - Thêm cột: `provider varchar(16)`, `provider_ref varchar(64)`, `paid_at timestamptz`, `settled_by uuid`, `settled_at timestamptz`, `failure_reason varchar(500)`.
  - Ràng buộc: `provider_ref` có ⇔ `PAID`; `failure_reason` có ⇔ `FAILED`; `reject_reason` có ⇔ `REJECTED` (giữ); `reviewed_*` có khi status ≠ `REQUESTED`.
  - Index "1 yêu cầu mở": `WHERE status IN ('REQUESTED','APPROVED','PROCESSING')`.
  - `ledger_entries.kind` CHECK thêm `PAYOUT_HOLD`, `PAYOUT_REVERSAL`.
  - Data-migrate dev: payout `APPROVED` cũ → `PAID` (`provider='MANUAL'`, `provider_ref='LEGACY'`, `paid_at=reviewed_at`); đổi ledger `kind='PAYOUT'` tương ứng → `PAYOUT_HOLD`; payout còn `REQUESTED` (chưa có dòng ledger) backfill một `PAYOUT_HOLD`.
- Entity: giá trị `PayoutStatus` mới + hàm chuyển trạng thái hợp lệ (`canTransition`); lỗi `ErrInvalidState` cho bước sai.
- **Done:** migration up→down→up pass trên PG có dữ liệu mẫu; unit test state machine phủ mọi cạnh hợp lệ/không hợp lệ.

### WP2 — Sổ cái hai pha (billing-svc repo + usecase)

- `RequestPayout`: trong cùng transaction với insert payout, ghi `DEBIT/PAYOUT_HOLD`. `payoutAllowed`: available = `Σ(CREDIT−DEBIT)` (bỏ nhánh pending), vẫn giữ "1 yêu cầu mở" + advisory lock.
- `balance()` đơn giản hóa: bỏ subquery `payout_requests ... REQUESTED`.
- Reject/Fail: ghi `CREDIT/PAYOUT_REVERSAL` (idempotent — chỉ ghi nếu payout chưa có reversal).
- Paid: không ghi ledger; chỉ cập nhật payout.
- **Done:** repo test (PG) chứng minh: sau request available giảm đúng; sau fail/reject available hồi đúng; sau paid không đổi thêm; không double-reversal.

### WP3 — PayoutProvider (billing-svc)

- `provider.PayoutProvider`: `Name() string`; `Disburse(ctx, Instruction) (Result, error)` với `Result{Ref string, Status}` (`PROCESSING/PAID/FAILED`). Bản `manual`: trả `PROCESSING`, không gọi mạng. Tên `NamePayoutManual = "MANUAL"`. Config `BILLING_PAYOUT_PROVIDER=manual`.
- `Approve` gọi `Disburse` → set `PROCESSING` + lưu provider.
- **Done:** unit test provider manual; usecase Approve đi REQUESTED→APPROVED→PROCESSING, lưu provider.

### WP4 — API admin & studio (billing-svc REST)

- Route mới: `POST /v1/admin/payouts/:id/mark-paid` (body `{reference}`, 10–64 ký tự) → `PAID`; `POST /v1/admin/payouts/:id/mark-failed` (body `{reason}`, 10–500) → `FAILED`. Giữ `approve`, `reject`.
- Chặn chuyển trạng thái sai (409 `PAYOUT_STATE`); `reference`/`reason` sai định dạng (422).
- Studio & admin GET trả status + provider_ref + failure_reason + mốc thời gian mới.
- **Done:** v1 handler test; smoke qua edge mọi transition + mọi lỗi.

### WP5 — Sự kiện & thông báo (pkg/events + billing + notification-svc)

- `pkg/events`: `payout.paid.v1`, `payout.failed.v1`, `payout.rejected.v1` (outbox billing).
- notification-svc: kind + template (creator): "Yêu cầu rút đã được chi" (kèm mã), "Rút tiền thất bại" (kèm lý do), "Yêu cầu rút bị từ chối" (kèm lý do); in-app + email.
- **Done:** billing phát đúng sự kiện; notification-svc tạo thông báo; E2E: fail → creator nhận in-app.

### WP6 — Web người dùng (nexuslive-web, Studio → Monetization)

- Hiển thị dòng trạng thái payout: Requested/Approved/Processing/Paid (mã tham chiếu)/Failed (lý do)/Rejected (lý do); nút **Rút lại** khi Failed/Rejected; chặn gửi khi đang có yêu cầu mở.
- i18n vi/en.
- **Done:** typecheck/lint/test/build pass; E2E `payout` (request → thấy trạng thái → admin mark-paid → thấy Paid + mã).

### WP7 — Web quản trị (nexuslive-admin)

- Trang payout: giữ Approve/Reject; thêm **Mark Paid** (nhập mã) và **Mark Failed** (nhập lý do), chỉ hiện nút đúng theo trạng thái; bộ lọc theo 6 trạng thái.
- **Done:** typecheck/lint/test/build pass; E2E admin đi hết vòng đời.

### WP8 — Tài liệu

- NXL-H: vòng đời chi trả mới + bất biến sổ cái; ADR-015 (PayoutProvider + hold/reversal); DL-23; BR-H37 (hold on request), BR-H38 (reversal on reject/fail), BR-H39 (one-open = REQUESTED∪APPROVED∪PROCESSING), BR-H40 (reference bắt buộc khi PAID); data-model + ERD billing; features.md; api/README.
- **Done:** tài liệu khớp code; swagger billing sinh lại.

## 3. Khu vực bị ảnh hưởng

billing-svc (entity, repo/persistent ledger, usecase/billing, controller/restapi, provider mới, config, migrations, docs); pkg/events; notification-svc; nexuslive-web (Studio monetization); nexuslive-admin (payouts); docs/.

## 4. Mã lỗi (NXL-00)

- Thêm `PAYOUT_STATE` (409 — chuyển trạng thái không hợp lệ).
- Giữ `INSUFFICIENT_BALANCE`, `PAYOUT_PENDING`.
- `reference`/`reason` sai định dạng → `VALIDATION` (422).
- Provider lỗi → `PROVIDER_UNAVAILABLE` (dùng `entity.ErrProviderUnavailable`).

## 5. Sinh code / cấu hình / tài liệu

- `BILLING_PAYOUT_PROVIDER=manual` vào `.env.example`, `config.go`, `docker-compose.yml`.
- `make swag` lại cho billing; **không** thêm dependency mới (manual provider thuần nội bộ).

## 6. Thứ tự

WP1 → WP2 → WP3 → WP4 → WP5 → (WP6, WP7 song song) → WP8.

## 7. Rủi ro

- **Sai bất biến sổ cái** (double-spend hoặc mất tiền): giảm bằng repo test tập trung vào available trước/sau mỗi transition + ràng buộc "chỉ một reversal / payout".
- **Data-migrate dev lệch:** chạy migration trên bản sao trước; nhắc lại incident GĐ6 — repo test không được trỏ DB thật.
- **Chuyển trạng thái tranh chấp** (admin bấm hai lần): `FOR UPDATE` + kiểm trạng thái hiện tại; mark-* idempotent theo trạng thái đích.

## 8. Kiểm tra

- Unit: state machine, provider manual, validator reason/reference.
- Repo (PG): hold/reversal, balance, one-open index, idempotent reversal, migration up→down→up.
- Smoke edge: toàn bộ transition + lỗi.
- E2E: web `payout` (vòng đời + rút lại sau fail), admin (mark-paid/mark-failed), notification (fail → in-app).
- Hồi quy: mọi E2E billing GĐ6 cũ pass; `make nxl-lint` 0 lỗi.

## 9. Lệch so với lộ trình / spec

Rút tiền vốn là mục có thể cắt (roadmap §6). Việc này nâng chất lượng phần đã có, làm trước GĐ7, không phát sinh service mới. Ghi DL-23 + errata NXL-H.

## 10. Câu hỏi cần chốt

Chốt tại brainstorm 2026-11-27 theo khung dung hòa:

- Giữ `PROCESSING` để chừa seam provider bất đồng bộ. ✔
- Có `PayoutProvider` interface + bản `manual`; chưa làm rail thật/webhook/job đối soát. ✔
- Không thu phí rút riêng; giữ mức rút tối thiểu hiện tại. ✔
- Bắn sự kiện báo creator khi paid/failed/rejected. ✔

## 11. Tiêu chí hoàn thành (DoD)

- **Chi thành công:** Given creator có số dư khả dụng ≥ số tiền, When gửi yêu cầu rút → admin Duyệt → admin "Đã chi" kèm mã `FT123`, Then payout đi `REQUESTED→APPROVED→PROCESSING→PAID`; số dư khả dụng giảm **đúng một lần** bằng số tiền; sổ cái có đúng 1 `PAYOUT_HOLD`, **không** reversal; creator thấy "Đã chi" + mã `FT123` và nhận thông báo.
- **Giữ tiền ngay khi gửi:** Given số dư khả dụng = 100k, When gửi yêu cầu rút 100k (chưa duyệt), Then số dư khả dụng = 0 ngay (có `PAYOUT_HOLD`); yêu cầu rút thứ hai bất kỳ → `INSUFFICIENT_BALANCE`.
- **Thất bại hoàn lại:** Given payout `APPROVED`/`PROCESSING`, When admin "Thất bại" kèm lý do, Then → `FAILED`; sổ cái có `PAYOUT_REVERSAL` CREDIT bằng số tiền; số dư khả dụng hồi về như trước khi gửi; creator thấy lý do, gửi lại được, nhận thông báo.
- **Từ chối hoàn lại:** Given payout `REQUESTED`, When admin Từ chối kèm lý do, Then → `REJECTED`; có reversal CREDIT; tiền hồi; creator gửi lại được.
- **Duyệt ≠ chi:** Given payout `APPROVED` (chưa mark-paid), Then chưa khoản tiền nào được coi là "đã chi" — payout vẫn có thể chuyển `FAILED` và hoàn lại **hoàn toàn**.
- **Một yêu cầu mở:** Given đã có payout ở `REQUESTED/APPROVED/PROCESSING`, When gửi yêu cầu mới, Then `PAYOUT_PENDING`.
- **Chặn sai & idempotent:** mark-paid/mark-failed trên payout không ở trạng thái hợp lệ → `PAYOUT_STATE` (409); mark-paid cần `reference` hợp lệ, mark-failed cần `reason` 10–500.
- **Bất biến sổ cái:** với mọi kênh, số dư khả dụng = `Σ(CREDIT − DEBIT)`; tổng `PAYOUT_HOLD` chưa reversal = (đã chi + đang giữ); không payout nào có > 1 reversal.
- **Không phá luồng cũ:** VOD/live/subscription/checkout không đổi; toàn bộ E2E billing GĐ6 cũ pass.
- **Chất lượng:** `make nxl-lint` 0 lỗi; unit + repo(PG) + E2E web/admin xanh; migration up→down→up pass; swagger billing sinh lại; tài liệu (NXL-H, ADR-015, data-model, features, api) cập nhật khớp code.
