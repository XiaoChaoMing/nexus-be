# Completed Plan — Migration và database NexusLive trên Docker

| | |
| --- | --- |
| **Track** | Full |
| **Working Plan** | `../working/2026-10-10-nexuslive-db-migrations.md` |
| **TSD** | `../technical/2026-10-10-nexuslive-db-migrations.md` |
| **Ngày** | 2026-10-10 |

## 1. Đã bàn giao

| Phần | File |
| --- | --- |
| Tạo database | `deploy/postgres/init-databases.sql` |
| Migration (14 cặp up/down) | `services/{identity,channel,stream,chat,safety,notification,billing}/migrations/` |
| Compose | `docker-compose.yml`: healthcheck cho `db`; service `db-init`, `db-migrate` (profile `nxl`) |
| Makefile | `compose-up`, `compose-up-all` bật profile `nxl`; thêm `nxl-db-init`, `nxl-migrate-up`, `nxl-migrate-down`, `nxl-migrate-create` |
| Cấu hình | `.env.example`: 7 biến `NXL_PG_URL_<SVC>` |
| Tài liệu | `README.md` (Local development, Check services); TSD |

Kết quả: 7 database, 38 bảng nghiệp vụ + 14 bảng chung (`outbox`, `processed_events`).

## 2. Lệch so với kế hoạch

| Lệch | Lý do |
| --- | --- |
| TSD viết cùng lúc với triển khai, không dừng chờ duyệt riêng | Yêu cầu "thực hiện kế hoạch" được hiểu là duyệt toàn bộ plan |
| `db-init`, `db-migrate` nằm trong profile `nxl` | `compose-up-integration-test` dùng `--abort-on-container-exit`; container chạy một lần không có profile sẽ dừng cả bộ test |
| `search_documents.title_unaccent`, `tsv` là generated column | Không để app tự tính sai; ERD đã ghi chú |
| `chat_messages`: không có unique (`user_id`, `client_msg_id`) và "một tin ghim mỗi phòng" | Bảng phân vùng chỉ cho unique index chứa khóa phân vùng; chat-svc tự đảm bảo (TSD §4) |
| Thêm trigger chỉ-thêm cho `audit_logs`, `transactions`, `ledger_entries` | ERD ghi "chỉ thêm" — đưa xuống database để không thể vi phạm |

## 3. Vấn đề gặp phải

- Môi trường của Claude không chạy được Docker và không tải được golang-migrate (proxy chặn `proxy.golang.org`). SQL được kiểm bằng PostgreSQL 18.4 thật, chạy mỗi file trong một transaction bằng `psql -1` — tương đương cách golang-migrate gửi cả file trong một lệnh.

## 4. Kiểm tra

| Kiểm tra | Kết quả |
| --- | --- |
| `init-databases.sql` chạy 2 lần liên tiếp | Lần 1 tạo 7 database, lần 2 không tạo gì |
| `up` cả 7 database (PostgreSQL 18.4) | Pass — số bảng: identity 8, channel 9, stream 8, chat 9, safety 5, notification 5, billing 8 |
| `down` cả 7 | Pass — không còn bảng, hàm, extension nào |
| `up` lần 2 sau `down` | Pass |
| 26 phép thử ràng buộc (username trùng hoa/thường, hai đơn PENDING, hai stream on-air, ENDED thiếu lý do, playback thiếu nguồn, hai stream key, slow_sec lạ, TIMEOUT thiếu hạn, emote trùng, xóa audit, trùng Idempotency-Key, webhook trùng, sửa giao dịch, hai sub còn hiệu lực, tier 4…) | Pass — 9 thao tác hợp lệ thành công, 17 thao tác vi phạm bị từ chối |
| Tìm không dấu "lien minh" → "Liên Minh Huyền Thoại" | Pass |
| Script lặp trong `db-migrate` (mô phỏng shell, `migrate` giả) | Pass — đúng 7 URL `nxl_*` |
| `make -n` các target mới | Pass |
| `docker compose up db db-init db-migrate` thật trên Windows | **Chưa chạy** — cần bạn chạy (§5) |
| `make compose-up-all`, `make compose-up-integration-test` | **Chưa chạy** — không có Docker |
| `make linter-golangci`, `make test` | **Không chạy** — không đổi code Go; máy chạy kiểm tra không có Go 1.27 |

Luồng hiện có bị ảnh hưởng: `compose-up`, `compose-up-all` (thêm profile và service), `db` (thêm healthcheck). Database `db` và migration mẫu trong `migrations/` không đổi.

## 5. Việc tiếp theo

- [ ] Chạy trên Windows: `make compose-up` (hoặc `docker compose --profile nxl up -d db db-init db-migrate`), rồi chạy lại lần 2 để xác nhận idempotent; báo lại log của `db-migrate`.
- [ ] Chạy `make compose-up-integration-test` để chắc profile `nxl` không làm hỏng bộ test.
- [ ] (Ngoài phạm vi) `minio-init` mới thêm vào compose không có profile — có thể làm `compose-up-integration-test` dừng sớm vì `--abort-on-container-exit`; nên kiểm tra.
- [ ] Role PostgreSQL riêng cho từng service (least privilege).
- [ ] Job tạo phân vùng ngày cho `chat_messages` (thuộc chat-svc, GĐ3).
