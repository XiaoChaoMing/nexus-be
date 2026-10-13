# Working Plan — Migration và database NexusLive trên Docker

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| **Full** (tạo migration, đổi `docker-compose.yml`, nhiều service) | Approved — đã triển khai | 2026-10-10 |

## 1. Task Brief

- **Mục tiêu:**
  - Sau `make compose-up`, PostgreSQL trong Docker có đủ 7 database của NexusLive, mỗi database đã chạy migration tạo đúng các bảng trong ERD.
  - Migration chạy lại nhiều lần không lỗi, và `down` về rỗng được.
- **Nguồn yêu cầu:**
  - ERD v1.0 tại `DATN/docs/ba/analysis/erd/` (38 bảng nghiệp vụ + 2 bảng chung mỗi database).
  - `docs/architecture/data-model.md` v1.1.
  - ADR-005 (database-per-service).
- **Trong phạm vi:**
  - Migration SQL (up/down) cho 7 service: identity, channel, stream, chat, safety, notification, billing.
  - Tạo 7 database trong container `db` hiện có, chạy được trên volume đã có dữ liệu.
  - Một service compose chạy migration cho cả 7 database, không cần cài `migrate` trên Windows.
  - Target Makefile tương ứng.
  - Cập nhật `.env.example` và `README.md`.
- **Ngoài phạm vi:**
  - Code Go của các service (entity, repo, usecase).
  - Chuyển toàn bộ repo sang monorepo.
  - Dữ liệu mẫu (seed).
  - Role PostgreSQL riêng cho từng service.
  - Đổi hoặc xóa database `db` và migration mẫu của template trong `migrations/`.
- **Các thư mục và file sẽ đụng tới:**
  - `services/<svc>/migrations/` (mới)
  - `deploy/postgres/init-databases.sql` (mới)
  - `docker-compose.yml`
  - `Makefile`
  - `.env.example`
  - `README.md`
- **Giả định:**
  - **G1 — Vị trí migration:** đặt migration ở `services/<svc>/migrations/`, đúng bố cục monorepo đã chốt ở kiến trúc §13. Phần code Go của service chuyển sang sau, trong task monorepo của GĐ0.
  - **G2 — Tên database:** đặt tiền tố `nxl_` (`nxl_identity`, `nxl_channel`, …) để không trùng database `db` của template.
  - **G3 — User PostgreSQL:** dùng chung user `user` của container hiện có cho cả 7 database khi dev.
  - **G4 — Khóa chính:** sinh bằng `uuidv7()` có sẵn trong PostgreSQL 18 (image hiện tại là `postgres:18.6-alpine`), không cần extension.
  - **G5 — Bản golang-migrate:** dùng image `migrate/migrate` bản cố định, cùng dòng v4 mà template đang dùng.

## 2. Phạm vi thay đổi

| Phần | File | Thay đổi |
| --- | --- | --- |
| Migration | `services/{identity,channel,stream,chat,safety,notification,billing}/migrations/*.sql` | Mới — xem §3 |
| Tạo database | `deploy/postgres/init-databases.sql` | Mới — tạo 7 database nếu chưa có (idempotent) |
| Compose | `docker-compose.yml` | Thêm 2 service chạy một lần: `db-init` (tạo database) và `db-migrate` (chạy migration cả 7); `db` thêm healthcheck |
| Makefile | `Makefile` | Thêm `nxl-db-init`, `nxl-migrate-up`, `nxl-migrate-down SVC=…`, `nxl-migrate-create SVC=… NAME=…`; `compose-up` gọi thêm `db-init db-migrate` |
| Cấu hình | `.env.example` | Thêm `NXL_PG_URL_<SVC>` cho 7 database |
| Tài liệu | `README.md` | Mục "Local development" và "Check services" thêm 7 database |

Database `db` và migration mẫu trong `migrations/` (users, tasks, translation history) **giữ nguyên**; app mẫu vẫn chạy như cũ.

## 3. Migration theo service

Mỗi service 2 migration, đặt tên theo định dạng hiện có của template (`YYYYMMDDHHMMSS_<name>`):

| Service | Database | Migration 1 — bảng nghiệp vụ | Migration 2 |
| --- | --- | --- | --- |
| identity | `nxl_identity` | `citext`; users, credentials, sessions, email_tokens, creator_applications, user_blocks | `outbox`, `processed_events` |
| channel | `nxl_channel` | `citext`, `unaccent` + hàm `immutable_unaccent`; channels, categories, follows, schedules, reminders, search_documents, search_history | như trên |
| stream | `nxl_stream` | stream_keys, streams, vods, playback_sessions, watch_progress, mature_consents | như trên |
| chat | `nxl_chat` | chat_rooms, chat_settings, chat_messages (phân vùng theo ngày), channel_roles, mod_actions, blocked_terms, emotes | như trên |
| safety | `nxl_safety` | reports, admin_actions, audit_logs | như trên |
| notification | `nxl_notification` | `citext`; notifications, notification_settings, email_outbox | như trên |
| billing | `nxl_billing` | subscription_tiers, subscriptions, checkout_orders, transactions, ledger_entries, payout_requests | như trên |

Quy tắc chung cho mọi migration:

- Cột, kiểu, NOT NULL, PK/FK/UNIQUE đúng ERD.
- Có thêm:
  - CHECK cho mọi cột trạng thái và enum;
  - unique index một phần như ghi chú trong ERD;
  - index cho các cột tham chiếu chéo service mà có truy vấn theo.
- Không có khóa ngoại xuyên database (không thể và không nên).
- `.down.sql` xóa đúng những gì `.up.sql` tạo, theo thứ tự ngược.
- Bảng `chat_messages` phân vùng theo ngày:
  - Migration tạo bảng cha và phân vùng `DEFAULT`.
  - Phân vùng theo ngày do job của chat-svc tạo sau (ngoài phạm vi).
  - Lý do giữ phân vùng `DEFAULT`: không có nó, insert sẽ lỗi khi chưa có phân vùng ngày.

## 4. Cách tạo database trên volume đã có

Script trong `/docker-entrypoint-initdb.d` **chỉ chạy khi volume rỗng**. Máy bạn đã có volume `db_data` nên cách đó không tác dụng. Thay vào đó:

- `db-init` dùng chính image `postgres:18.6-alpine`, chạy `psql` vào `db`. Mỗi database được tạo bằng mẫu `SELECT 'CREATE DATABASE …' WHERE NOT EXISTS (…) \gexec`, nên chạy lại không lỗi.
- `db-migrate` dùng image `migrate/migrate`, mount `./services`, lần lượt chạy `up` cho 7 database. Nó chỉ chạy khi `db-init` đã xong (`depends_on: condition: service_completed_successfully`).
- Không cần xóa volume, không mất dữ liệu của database `db` hiện có.

## 5. Các bước thực hiện

1. **Viết TSD** (Full track) gồm:
   - DDL rút gọn;
   - danh sách CHECK, index, unique index một phần cho từng database;
   - sơ đồ luồng `compose-up` → `db-init` → `db-migrate`;
   - cách rollback.

   **Chờ duyệt.**
2. Tạo `deploy/postgres/init-databases.sql`.
3. Viết migration cho 7 service theo §3.
4. Sửa `docker-compose.yml`, `Makefile`, `.env.example`, `README.md`.
5. Kiểm tra: xem §8.
6. Viết Completed Plan.

## 6. Rủi ro

| Rủi ro | Tác động | Giảm thiểu |
| --- | --- | --- |
| ERD còn thay đổi khi viết spec NXL-D…H | Phải sửa schema | Migration sau là migration mới; không sửa migration đã chạy (đúng quy tắc AGENTS.md) |
| `uuidv7()` chỉ có từ PostgreSQL 18 | Lỗi nếu ai đó chạy bản cũ hơn | Ghi rõ yêu cầu PG 18 trong README; compose đã cố định `postgres:18.6-alpine` |
| `unaccent` không IMMUTABLE nên không dùng trực tiếp trong index | Lỗi tạo index | Bọc trong hàm `immutable_unaccent` khai báo IMMUTABLE |
| `docker compose up` của app chạy trước khi DB sẵn sàng | Migration lỗi kết nối | Healthcheck `pg_isready` cho `db`; `db-init` phụ thuộc `condition: service_healthy` |
| Thay đổi `compose-up` ảnh hưởng luồng dev hiện có | App mẫu không lên | Chỉ thêm service, không sửa `db`/`app` ngoài healthcheck; kiểm tra lại `make compose-up-all` |
| Docker không chạy được trong môi trường của Claude | Không tự kiểm tra trên Docker được | Kiểm tra SQL trên PostgreSQL 18 chạy trong môi trường của Claude; bước chạy compose do bạn chạy trên Windows và báo kết quả |

## 7. Câu hỏi mở

Không có câu hỏi chặn. Nếu bạn không đồng ý với giả định G1–G5 ở §1, nói lúc duyệt plan.

## 8. Checklist kiểm tra

- [ ] Mỗi database: `up` → `down` về rỗng → `up` lại không lỗi (chạy trên PostgreSQL 18)
- [ ] Số bảng sau `up` khớp ERD (38 nghiệp vụ + 14 bảng chung)
- [ ] Thử ràng buộc chính:
  - CHECK trạng thái từ chối giá trị lạ;
  - unique một phần: một stream `LIVE` mỗi kênh, một subscription còn hiệu lực mỗi cặp user–kênh;
  - unique (`psp_ref`, `type`) chặn webhook trùng.
- [ ] `docker compose up db db-init db-migrate` chạy hai lần liên tiếp không lỗi (bạn chạy trên Windows)
- [ ] `make compose-up-all`: app mẫu vẫn lên, database `db` còn nguyên
- [ ] `make linter-golangci` và `make test` vẫn qua (không đổi code Go, kiểm tra để chắc)

## 9. Tiêu chí hoàn thành

- Given volume `db_data` đã có dữ liệu, When chạy `make compose-up`, Then có 7 database `nxl_*` với đủ bảng theo ERD và database `db` không đổi.
- Given đã migrate xong, When chạy lại `make compose-up`, Then `db-init` và `db-migrate` kết thúc thành công mà không thay đổi gì.
- Given một database đã migrate, When chạy `make nxl-migrate-down SVC=<svc>`, Then database đó trở về rỗng.
