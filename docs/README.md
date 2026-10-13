# NexusLive — Tài liệu dự án

NexusLive là nền tảng livestream và cộng đồng tương tác thời gian thực cho thị trường Việt Nam,
được xây dựng trong khuôn khổ đồ án tốt nghiệp (1 người, ~4,5 tháng).

## Bản đồ tài liệu

| Tài liệu | Trả lời câu hỏi | Đọc khi |
| --- | --- | --- |
| [product/overview.md](product/overview.md) | Sản phẩm là gì, cho ai, phạm vi đồ án tới đâu, quyết định nào đã chốt | Bắt đầu tìm hiểu dự án |
| [product/features.md](product/features.md) | Có những tính năng gì, ưu tiên ra sao, cái nào làm trong đồ án, service nào sở hữu, spec nào đặc tả | Chọn việc tiếp theo, viết Working Plan |
| [product/roadmap.md](product/roadmap.md) | Làm gì, theo thứ tự nào, xong lúc nào, demo được gì ở mỗi mốc | Lập kế hoạch tuần, báo cáo tiến độ |
| [architecture/system-architecture.md](architecture/system-architecture.md) | Hệ thống gồm những thành phần nào, giao tiếp ra sao, các luồng chính chạy thế nào | Trước khi thiết kế hoặc code bất kỳ service nào |
| [architecture/decisions.md](architecture/decisions.md) | Vì sao chọn microservices, NATS, RTMP + ffmpeg + HLS, React SPA… | Khi muốn đổi một lựa chọn kiến trúc |
| [architecture/data-model.md](architecture/data-model.md) | Mỗi service sở hữu dữ liệu nào, các entity liên hệ thế nào | Khi viết migration, API, sự kiện |
| [api/README.md](api/README.md) | Có những API và sự kiện nào, ở cổng nào, trạng thái ra sao, quy ước chung (envelope, lỗi, xác thực, phân trang), Swagger ở đâu | Trước khi gọi hoặc thêm một API / sự kiện |
| `../../docs/ba/specs/` | Đặc tả chi tiết từng nhóm tính năng (NXL-*), nằm ngoài repo | Khi triển khai một tính năng cụ thể |
| `plans/` | Working Plan, TSD, Completed Plan của từng task (xem `AGENTS.md`) | Khi bắt đầu hoặc kết thúc một task |

## Thứ bậc nguồn sự thật

Khi các tài liệu mâu thuẫn, ưu tiên theo thứ tự:

1. Spec chi tiết của tính năng (`../../docs/ba/specs/NXL-*.md`): Business Rules → State Machine → Acceptance Criteria → API.
2. Kiến trúc (`architecture/*`) — cho các câu hỏi kỹ thuật xuyên suốt (giao tiếp, dữ liệu, bảo mật).
3. Tổng quan và tính năng (`product/*`) — cho phạm vi và ưu tiên của đồ án.
4. PRD v2.0 — tầm nhìn sản phẩm đầy đủ; phần nào bị `product/*` cắt giảm cho đồ án thì theo `product/*`.

Mâu thuẫn phát hiện được ghi vào `../../docs/ba/traceability/errata.md` và xử lý ở đợt đồng bộ, không sửa lẻ.

## Tài liệu nghiệp vụ nằm ngoài repo

PRD, spec NXL-*, ma trận truy vết, errata, nhật ký quyết định và báo cáo đồ án nằm ở `DATN/docs/`
(từ thư mục này: `../../docs/`), tách khỏi tài liệu kỹ thuật (ADR-011). Repo chỉ dẫn chiếu sang đó.

| Cần | Đường dẫn từ `nexuslive-be/docs/` |
| --- | --- |
| PRD, lộ trình gốc | `../../docs/ba/requirements/` |
| Spec NXL-* | `../../docs/ba/specs/` |
| Errata, ma trận truy vết | `../../docs/ba/traceability/` |
| Nhật ký quyết định | `../../docs/ba/analysis/decision-log.md` |

## Phân biệt với file sinh tự động

`docs/docs.go`, `docs/swagger.*` và `docs/proto/` do `make swag-v1` / `make proto-v1` sinh ra (app mẫu);
spec Swagger của các service NexusLive nằm ở `services/<svc>/docs/` do `make nxl-swag` sinh ra.
`docs/product/`, `docs/architecture/`, `docs/api/`, `docs/plans/` viết tay — không target nào ghi đè.
