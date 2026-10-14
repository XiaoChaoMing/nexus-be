# Completed — GĐ4: Kênh & Khám phá

| Working Plan | TSD | Ngày |
| --- | --- | --- |
| `docs/plans/working/2026-10-14-gd04-channel-discovery.md` | `docs/plans/technical/2026-10-14-gd04-channel-discovery.md` v1.1 | 2026-10-15 |

## 1. Đã giao

**Backend (`nexuslive-be`):**

- **channel-svc:** xác thực JWT (JWKS); follow + chuông (`ALL` / `LIVE_ONLY` / `OFF`), danh sách Đang theo dõi + sidebar;
  hồ sơ kênh công khai + Studio → Kênh (bio, liên kết, `matureDefault`, đổi handle 30 ngày / lần); upload ảnh đại diện /
  ảnh bìa (`golang.org/x/image`, xuất JPEG, kiểm magic bytes, giới hạn byte + 40 MP) vào bucket `nxl-public` phục vụ qua
  `/media/`; 24 danh mục (5 nhóm, ảnh bìa dải màu sinh khi khởi động); tìm kiếm FTS + `unaccent` + tiền tố (gợi ý theo
  nhóm + trang kết quả), lịch sử tìm kiếm; lịch phát (một lần / hằng tuần, múi giờ, giới hạn 20, chân trời 90 ngày, sửa /
  hủy một lần phát), nhắc lịch; job `occurrence_upcoming` / `reminder_due`; thống kê Studio; sự kiện qua outbox.
- **stream-svc:** danh mục trong Stream Manager, `GET /v1/streams` lọc / sắp / cursor, danh sách đề xuất, VOD công khai
  theo kênh / danh mục, dòng `SCHEDULED` từ lịch, Go Live trong −30 / +60 phút gắn đúng `streamId`, tự `CANCELLED`,
  Dashboard, Studio → Nội dung (đổi tên, ẩn / hiện, xóa mềm), VOD ẩn biến khỏi mọi đường công khai trừ chủ kênh.
- **chat-svc:** chủ đề `sidebar:{userId}` (`channel_live`, `channel_offline`, `viewer_count_batch` 5 s); bản sao follow có
  `notifyLevel`; Followers-only chạy bằng follow thật.
- **media-svc:** `vod.deleted.v1` xóa file của VOD.
- `pkg/events` (sự kiện GĐ4, chỉ thêm), Nginx (tách route theo TSD §3.2, `/media/`, giới hạn upload 6 MB), compose,
  `.env.example`, API catalog v1.6, README, Swagger channel + stream.

**Web (`nexuslive-web`):** nút Follow + menu chuông (Guest → đăng nhập rồi chạy tiếp), sidebar "Kênh đang theo dõi"
realtime, `/following`; hồ sơ `/:handle` và `/c/:handle` 4 tab; Khám phá (Danh mục / Kênh live, nhóm, sắp xếp, ngôn ngữ),
trang danh mục; Ctrl+K + trang kết quả + tìm kiếm mobile; Trang chủ thêm Kênh theo dõi đang live, Danh mục phổ biến,
Video đề xuất (Hero do server xếp); trạng thái "Sắp diễn ra" (đếm ngược, Đặt nhắc, tự chuyển Live, Đã hủy); Studio
`/studio` (Tổng quan, Phát trực tiếp, Nội dung, Lịch phát, Kênh) + chọn danh mục; theo frame pen.dev GĐ4 (Studio 16–19
dựng theo brief vì file chưa có frame).

**Tài liệu (`DATN/docs`):** NXL-C v1.2, NXL-D v1.3, NXL-G2 v1.1, NXL-A v1.2, NXL-00 v1.4, **NXL-H v1.0** (SePay), brief
design GĐ4, errata v1.5 (đóng E-17, E-20, E-21; thêm E-22…E-27), DL-19, DL-20, ma trận truy vết v1.6.

## 2. Lệch so với plan / TSD

TSD v1.1 §16. Ngoài ra: Trending trong tìm kiếm (PRD FR-SE-02) chưa làm (E-25); đổi lịch chỉ báo người đặt nhắc, không
báo mọi người theo dõi (E-27); trang hồ sơ dùng ảnh thumbnail thay mini-player tự phát; upload ảnh có xem trước nhưng
không có hộp cắt (server cắt giữa).

## 3. Sự cố

- Handle của Creator seed là `creator`, trùng route `/creator` của ứng dụng → `/c/creator` báo "không tìm thấy" mà không
  hỏi server → `/c/` chấp nhận cả từ dành riêng.
- E2E cũ tìm thẻ stream trên Trang chủ, nhưng stream đầu bảng giờ là Hero và không lặp lại ở hàng Live → helper
  `openLiveFromHome`.
- Toast đổi chế độ chat thỉnh thoảng không hiện: `mode_change` qua WebSocket tới trước phản hồi HTTP nên so sánh với chế độ
  đã cập nhật → mang chế độ cũ theo request.

## 4. Kiểm thử

| Lệnh | Kết quả |
| --- | --- |
| `go test -race` channel, stream, chat, media, pkg (PG + Redis thật) | pass |
| `golangci-lint` mọi module | 0 lỗi |
| Migration channel, stream up → down → up | pass |
| `swag init` channel (19 path), stream (17 path) | đã sinh lại |
| Web `pnpm lint`, `tsc -b`, `vitest`, `build` | pass — 305 test |
| Smoke qua edge (follow, sidebar WS, Followers-only, hồ sơ + ảnh, handle 409, danh mục, tìm "lien minh", lịch → SCHEDULED → gắn Go Live → CANCELLED, nhắc lịch, Dashboard, ẩn / xóa VOD — 56 object bị xóa) + hostile (401, 403, 400, 404, 409) | pass |
| E2E web trên stack cục bộ | **12/12 pass**: m4 discovery ×2, m4 creator, guest-modal ×4, livestream, m1, m2 ×2, m3 |

Chưa chạy: Docker compose trên máy ông chủ; nhánh "dependency down"; consumer bền của media khi cập nhật consumer NATS
đã có (smoke chạy NATS mới).

## 5. Việc tiếp theo

- Ông chủ: `make deps` (+ `make deps-audit`) — `go.sum` chưa có hash `golang.org/x/image` (sandbox không tải được
  golang.org/x); `make nxl-up-all`; `make nxl-swag` để so Swagger.
- Design: frame Studio 16–19 và bộ chọn danh mục còn thiếu trên pen.dev.
- GĐ5: notification-svc gửi nhắc lịch / kênh live (sự kiện đã phát), kiểm duyệt (NXL-F), kênh bị khóa, URL HLS có chữ ký.
- GĐ6: billing-svc + SePay sandbox (NXL-H); ông chủ đăng ký SePay sớm.
