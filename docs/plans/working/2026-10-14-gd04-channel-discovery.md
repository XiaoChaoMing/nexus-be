# GĐ4 — Kênh & Khám phá (follow, hồ sơ kênh, danh mục, tìm kiếm, lịch phát, Studio)

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (channel-svc mở rộng + auth mới, migration, sự kiện mới, thư viện mới, Nginx, cross-repo web) | Approved 14/10 (G4-Q1…Q8, Q10 theo đề xuất; G4-Q9: SePay thay VNPay) | 2026-10-14 |

Lịch lộ trình GĐ4: 28/12 → 08/01 (2 tuần), không có mốc demo riêng; "hoàn thành khi" hành trình **Discovery** và
**Creator** (PRD §15) chạy trọn. GĐ3 xong sớm ~9 tuần nên bắt đầu ngay được. Bảng dữ liệu phần lớn đã có trong
migration GĐ0 (`follows`, `schedules`, `reminders`, `categories`, `search_documents`, `search_history`;
`streams.category_id/schedule_id/scheduled_start_at`, `vods.visibility`) nhưng chưa có code.

## 1. Task Brief

**Mục tiêu:**

- Người xem: theo dõi kênh (chuông), thấy kênh đang theo dõi live ở sidebar (realtime) và trang **Đang theo dõi**,
  xem **hồ sơ Creator** (Trang chủ / Video / Giới thiệu / Lịch), duyệt **danh mục**, **tìm kiếm** (Ctrl+K, có/không
  dấu), đặt **nhắc lịch** cho buổi "Sắp diễn ra".
- Creator: chọn danh mục khi phát, sửa hồ sơ kênh (ảnh đại diện, ảnh bìa, giới thiệu, liên kết, handle), quản lý VOD
  (ẩn / xóa / đổi tên), tạo lịch phát, xem Dashboard tối thiểu.
- Followers-only của chat (GĐ3) chạy bằng follow thật.

**Nguồn:**

| Nguồn | Phần dùng |
| --- | --- |
| Lộ trình §3 GĐ4 | Phạm vi, "hoàn thành khi", viết NXL-H (chọn PSP trước tuần 14) |
| NXL-C v1.1 | Shell, Home, Browse/Category, Search, Following; API 01–09; `sidebar:{userId}` |
| NXL-D v1.2 | Follow + chuông, hồ sơ kênh, sửa hồ sơ + ảnh, handle, lịch + nhắc (BR-D14…D23) |
| NXL-G2 v1.0 | Studio: Dashboard, Content (VOD), Schedule, `SCHEDULED/CANCELLED` (ST-Q1…Q8) |
| NXL-A v1.1 | Trạng thái "Sắp diễn ra" (trạng thái thứ 9 của trang xem), AF04 Upcoming → Live |
| Errata E-18, E-19, E-21 | Catalog API, bảng sự kiện, payload `follow.*` thiếu `notifyLevel` |
| Design pen.dev | `02 Browse`, `04 Trang Creator`, `05 Sắp diễn ra`, `06/07 Stream Manager` (chỉ desktop 1440, một trạng thái) |

**Trong phạm vi:** WP1–WP10 ở §2.

**Ngoài phạm vi (§9):** gửi thông báo nhắc lịch / kênh live (notification-svc, GĐ5 — GĐ4 chỉ phát sự kiện), follow
danh mục (NXL-C Q7), Clips/Highlights, doanh thu trên Dashboard (GĐ6), kênh bị khóa (GĐ5), số lượt xem VOD (GĐ7).

**Giả định:**

- Khám phá + tìm kiếm ở **channel-svc** (ADR-001); danh sách stream theo danh mục vẫn do **stream-svc** trả (đã có
  `streams.category_id`), web ghép tên danh mục từ `GET /v1/categories`.
- channel-svc thêm xác thực JWT qua JWKS (như stream, chat).
- Sidebar realtime do **chat-svc** đẩy (đã giữ bản sao follow từ GĐ3): khi `stream.started/ended`, gửi
  `channel_live/offline` tới `sidebar:{userId}` của người theo dõi.
- Không thêm service mới.

**Câu hỏi mở:** §10.

## 2. Gói việc

### WP1 — Nền channel-svc

| Việc | Chi tiết |
| --- | --- |
| Auth | Middleware JWT (JWKS identity) bắt buộc / tùy chọn như stream-svc; `CHANNEL_JWKS_URL` |
| Ảnh công khai | Bucket `nxl-public` (MinIO, đọc công khai qua Nginx `/media/*`); upload + resize phía server (G4-Q1) |
| Sự kiện (E-21) | `follow.created.v1` thêm `notifyLevel`; mới `follow.updated.v1`, `channel.updated.v1`, `schedule.*`, `vod.updated/deleted.v1` (NXL-G2 §13.4); ghi vào NXL-00 §11.1 |
| Nginx | Tách route: `/v1/studio/channel*`, `/v1/studio/schedules*`, `/v1/me/following*`, `/v1/me/search-history`, `/v1/categories`, `/v1/search`, `/v1/schedules` → channel; `/v1/channels/{id}/vods` → stream (trước route `/v1/channels`) |

### WP2 — Follow, chuông, Đang theo dõi, sidebar

- `POST/PUT/DELETE /v1/channels/{id}/follow` (`notifyLevel` ALL / LIVE_ONLY / NONE), `CANNOT_FOLLOW_SELF`; outbox
  `follow.*`; `GET /v1/channels/{handle}` thêm `viewer {following, notifyLevel}`, `followerCount`.
- `GET /v1/me/following?status=live|all`, `GET /v1/me/following/sidebar` (≤ 10, live trước, theo số người xem).
- chat-svc: chủ đề `sidebar:{userId}` (`channel_live`, `channel_offline`, `viewer_count_batch` mỗi 5 s) từ bản sao
  follow; Followers-only chạy bằng follow thật (bỏ phụ thuộc `dev-grant`).
- Web: nút Follow + chuông thật (thay stub localStorage, giữ `pendingAction` cho Guest), sidebar "Kênh đang theo dõi",
  trang `/following` (Live / Tất cả).

### WP3 — Hồ sơ Creator và sửa kênh

- `GET /v1/channels/{handle}` đủ trường (banner, bio, liên kết, `isLive`, stream hiện tại); tab Video
  `GET /v1/channels/{id}/vods` (stream-svc), tab Lịch `GET /v1/channels/{id}/schedule`.
- Studio → Kênh: `PATCH /v1/studio/channel` (tên hiển thị, bio ≤ 300, ≤ 5 liên kết https, `matureDefault`, handle 30
  ngày / lần, từ khóa route cấm), `PUT /v1/studio/channel/avatar|banner` (≤ 2 MB → 256², ≤ 5 MB → 1600×500).
- `channel.updated.v1` → bản sao kênh ở stream-svc (`creator_channels`) và chat-svc (`channels`).
- Web: route `/:handle` (+ `/c/:handle` nếu trùng route), 4 tab, trạng thái live / offline / không tồn tại.

### WP4 — Danh mục và Browse

- Seed ~24 danh mục (5 nhóm GAMES / ESPORTS / MUSIC / IRL / CREATIVE) bằng migration; ảnh bìa trong `nxl-public`.
- `GET /v1/categories?group=`, `GET /v1/categories/{slug}` (kèm số người xem live, tính từ stream-svc qua sự kiện).
- stream-svc: `stream_settings.category_id` (migration mới), Stream Manager chọn danh mục; `GET /v1/streams` thêm
  `category`, `language`, `tags`, `sort=viewers|newest`, cursor; DTO stream có `category`.
- Web: Browse 2 chế độ (Danh mục / Kênh live) + pill nhóm, trang `/browse/:slug` (Live / Video).

### WP5 — Tìm kiếm

- `search_documents` được cập nhật từ sự kiện (`channel.created/updated`, `stream.started/ended/updated`,
  `vod.*`, seed danh mục); truy vấn `unaccent` + tiền tố + tsvector (có dấu / không dấu, BR22).
- `GET /v1/search/suggest?q&types&limit=5` (nhóm Kênh / Live / Danh mục / Video), `GET /v1/search?q&type&cursor`
  (trang kết quả), lịch sử tìm kiếm (G4-Q8).
- Web: bảng lệnh Ctrl+K (gợi ý theo nhóm, phím ↑↓ Enter, lịch sử), trang `/search?q=`.

### WP6 — Trang chủ đầy đủ

- Các khối: Hero (G4-Q4), Tiếp tục xem (đã có), **Kênh theo dõi đang live**, Live đề xuất, **Danh mục phổ biến**,
  **Video đề xuất**, Creator CTA; mỗi khối tải và lỗi độc lập (G4-Q3).

### WP7 — Lịch phát, "Sắp diễn ra", nhắc lịch

- channel-svc: `GET|POST|PUT|DELETE /v1/studio/schedules`, sửa / hủy một lần phát; rrule tập con (G4-Q5);
  `SCHEDULE_MAX_ACTIVE` 20, chân trời 90 ngày, `durationMin` 15–720.
- Job phát `schedule.occurrence_upcoming.v1` (≤ 24 h trước) → stream-svc tạo dòng `SCHEDULED`; Go Live trong
  −30 / +60 phút gắn vào dòng đó (giữ `streamId`); quá 60 phút → `CANCELLED`.
- `POST|DELETE /v1/schedules/{id}/reminders` (lưu; job phát `schedule.reminder_due.v1` — gửi thật ở GĐ5).
- Web: trạng thái "Sắp diễn ra" trên trang xem (đếm ngược, Đặt nhắc, tự chuyển sang Live ≤ 5 s qua `stream:`),
  tab Lịch trên hồ sơ, Studio → Lịch phát.

### WP8 — Studio: Dashboard, Nội dung, khung Studio

- Khung Studio `/studio` (menu: Tổng quan, Phát trực tiếp, Nội dung, Lịch phát, Kênh).
- `GET /v1/studio/dashboard?range=7d|28d` (stream-svc) + `GET /v1/studio/channel/stats` (channel-svc).
- `GET /v1/studio/vods`, `PATCH /v1/studio/vods/{id}` (tiêu đề, ẩn/hiện), `DELETE` (xóa mềm → `vod.deleted.v1` →
  media-svc xóa file); VOD ẩn biến khỏi mọi đường công khai (BR-G2-07).

### WP9 — Tài liệu

- NXL-C v1.2 (đóng câu hỏi theo §10), NXL-D / NXL-G2 / NXL-A errata (ST-Q2, ST-Q4, NXL-A Q10), NXL-00 v1.4 (sự kiện,
  hằng số, mã lỗi), API catalog (E-18), kiến trúc §4 (E-19), ERD channel/stream.
- **NXL-H v1.0** (Subscription & thanh toán) với PSP **SePay** (G4-Q9).
- Brief design GĐ4 cho các frame còn thiếu (G4-Q10). TSD + Completed Plan.

### WP10 — Kiểm thử và hành trình

- Unit + repo test PG (channel, stream), lint, Swagger; E2E: **Discovery** (Guest tìm kiếm không dấu → danh mục →
  vào live → đăng nhập follow → sidebar thấy kênh live → kết thúc thì offline) và **Creator** (sửa hồ sơ + ảnh → tạo
  lịch → người xem đặt nhắc → "Sắp diễn ra" → Go Live đúng giờ → VOD → ẩn VOD → biến mất khỏi hồ sơ); hồi quy M1–M3.

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Gói |
| --- | --- | --- |
| channel-svc | entity, repo, usecase (follow, profile, category, search, schedule, reminder, stats), restapi, events consumer/outbox, config (JWKS, S3), migration mới (cột schedule, chỉ mục) | WP1–WP7 |
| stream-svc | `stream_settings.category_id`, list filter/sort, SCHEDULED/CANCELLED, dashboard, studio VOD, consumer `channel.updated`, `schedule.occurrence_*`; migration mới | WP3, WP4, WP7, WP8 |
| media-svc | Consumer `vod.deleted.v1` (xóa object) | WP8 |
| chat-svc | `sidebar:{userId}`, `channel.updated`, `follow.updated` | WP2, WP3 |
| `pkg/events` | Payload mới / thêm trường (chỉ thêm) | WP1 |
| Hạ tầng | Nginx route, bucket `nxl-public` + `/media/`, compose env, `.env.example` | WP1 |
| Web | `features/channels`, `discover`, `search` (mới), `profile` (mới), `studio/*` (mới), `home`, `watch` (Upcoming), shell sidebar/top bar, i18n | WP2–WP8 |

| Transport | Có | Ghi chú |
| --- | --- | --- |
| REST công khai | Có | ~30 endpoint mới (NXL-C §13, NXL-D §13.2, NXL-G2 §13.1–13.2) |
| WebSocket | Có | `sidebar:{userId}` (chat-svc) |
| NATS | Có | `follow.updated`, `channel.updated`, `schedule.*`, `vod.updated/deleted`; thêm `notifyLevel` vào `follow.created` |

Luồng cũ có thể vỡ: route `/v1/channels/*` (tách vods → stream, chat-settings → chat), DTO stream (chỉ thêm
`category`), state machine stream (thêm nhánh SCHEDULED → LIVE / CANCELLED), Studio `/studio/stream`, nút Follow
stub, Followers-only (dev-grant).

## 4. Mã lỗi

Dùng mã có sẵn ở NXL-00 §5.2 và NXL-D (`HANDLE_TAKEN`, `CANNOT_FOLLOW_SELF`, `CHANNEL_SUSPENDED` 📋 GĐ5,
`INVALID_STATE`, `CONFLICT` + `details.limit`, `VALIDATION_FAILED`). Mã chưa có trong NXL-00 sẽ đưa vào NXL-00 v1.4
trước khi code (TSD liệt kê).

## 5. Sinh code / cấu hình / tài liệu

- TSD `docs/plans/technical/2026-10-14-gd04-channel-discovery.md` (hợp đồng, migration, sự kiện, sequence).
- `make nxl-swag` (channel, stream), mock, migration up → down → up.
- Env mới: `CHANNEL_JWKS_URL`, `CHANNEL_S3_*`, `CHANNEL_PUBLIC_BUCKET`, `CHANNEL_SCHEDULE_*`, `STREAM_SCHEDULE_EARLY_START`,
  `STREAM_SCHEDULE_LATE_GRACE`.

## 6. Thứ tự

| # | Việc | Phụ thuộc |
| --- | --- | --- |
| 1 | Chốt §10, TSD; brief design GĐ4 gửi agent design | Plan duyệt |
| 2 | WP1 nền channel-svc + sự kiện + Nginx | 1 |
| 3 | WP2 Follow/sidebar, WP4 danh mục (song song) | 2 |
| 4 | WP3 hồ sơ + ảnh, WP5 tìm kiếm | 2, 3 |
| 5 | WP7 lịch phát + Upcoming, WP8 Studio | 3 |
| 6 | WP6 Trang chủ đầy đủ | 3, 4 |
| 7 | WP9 tài liệu (NXL-H song song từ bước 1), WP10 E2E, Completed Plan | 1–6 |

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Phạm vi lớn nhất từ trước (~30 endpoint, 3 service) | Chia commit theo WP; WP7 (lịch phát) có thể tách sang đầu GĐ5 nếu trễ |
| Lịch phát + state machine stream (SCHEDULED) dễ sai giờ / múi giờ | Lưu UTC + `timezone` IANA; test bảng giờ (DST không có ở VN nhưng vẫn test); job idempotent |
| Tìm kiếm tiếng Việt có / không dấu | `unaccent` + chuẩn hóa NFC hai phía; test bộ từ khóa có dấu |
| Upload ảnh (kích thước, định dạng giả) | Kiểm magic bytes, giới hạn byte ở Nginx + service, decode thật trước khi lưu |
| Route Nginx chồng nhau | Test định tuyến từng tiền tố như GĐ1/GĐ3 |
| Design thiếu frame (Following, Ctrl+K, Studio, mobile Browse/Profile) | Brief design ngay bước 1; dựng theo token + spec trước, chỉnh khi có frame |

## 8. Kiểm tra

- [ ] `make format`, `make nxl-lint`, `make nxl-test`, repo test PG channel + stream, migration up → down → up.
- [ ] Stack cục bộ + E2E Discovery, Creator, hồi quy M1/M2/M3; web lint, tsc, vitest, build.
- [ ] Hostile: follow chính mình, follow kênh không có, ảnh > giới hạn / sai định dạng / giả đuôi, handle trùng / cấm /
  đổi lần 2 trong 30 ngày, lịch quá 20 / quá 90 ngày / chồng giờ, nhắc lịch đã hủy, VOD ẩn qua URL trực tiếp, token
  sai, JWKS lỗi.

## 9. Lệch so với lộ trình / spec

| Nguồn | Yêu cầu | Làm | Lý do |
| --- | --- | --- | --- |
| NXL-D AC-D20, NXL-E | Nhận nhắc lịch / thông báo kênh live | Chỉ phát sự kiện; gửi ở GĐ5 | notification-svc ở GĐ5 |
| NXL-C | Follow danh mục (Q7), Clips | Không làm | Ngoài lộ trình GĐ4 |
| NXL-C API 01 | `GET /v1/home` một endpoint | Theo G4-Q3 | — |
| NXL-C BR06 | Tiếp tục xem 5 %–95 %, ≥ 15 s | Giữ luật GĐ2 (≥ 5 s, chưa tới 10 s cuối) + errata | Đã chạy, E2E M2 dựa vào |
| NXL-G2 | Dashboard có số lượt xem VOD | Không (ST-Q9) | `playback_sessions` bị xóa khi hết hạn |

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| G4-Q1 | Thư viện mới cho ảnh: `golang.org/x/image` (đọc WebP, resize chất lượng cao); xuất JPEG (Go chuẩn không ghi WebP); bucket `nxl-public` đọc công khai qua Nginx `/media/` | Đồng ý; resize ngay trong request (ảnh nhỏ, ≤ 5 MB), không cần worker |
| G4-Q2 | Tìm kiếm: chỉ PostgreSQL FTS + `unaccent` + tiền tố, không `pg_trgm`, không nhóm Tag riêng (tag nằm trong văn bản của Live) | Đồng ý — đủ cho quy mô đồ án, không thêm extension |
| G4-Q3 | Trang chủ: một `GET /v1/home` gộp (NXL-C API 01) hay mỗi khối gọi API riêng? | Mỗi khối gọi riêng (đã làm vậy ở GĐ2), lỗi khối nào hiện ở khối đó; ghi errata NXL-C |
| G4-Q4 | Hero / Trending / Danh mục phổ biến / nội dung 18+ (NXL-C Q1, Q2, Q5, Q13) | Hero = live nhiều người xem nhất (hòa → mới nhất); Trending = sort theo người xem; Danh mục phổ biến = tổng người xem live, hòa → thứ tự seed; 18+ ẩn khỏi Hero và gợi ý, vẫn hiện ở danh mục với nhãn 18+ |
| G4-Q5 | Lịch phát: rrule đầy đủ hay tập con? Chốt ST-Q1…Q5, Q8 và NXL-A Q10 | Một lần + lặp hằng tuần (`FREQ=WEEKLY;BYDAY=…`); chấp nhận đề xuất ST-Q1…Q5, Q8; tự hủy sau 60 phút |
| G4-Q6 | Làm đổi handle trong GĐ4 (cần `channel.updated.v1` ở stream + chat)? | Có, 30 ngày / lần, handle cũ không chuyển hướng (BR-D14) |
| G4-Q7 | Sidebar realtime do chat-svc đẩy từ bản sao follow (thay vì channel-svc giữ kết nối) | Đồng ý — chat-svc đã là gateway và có bản sao follow |
| G4-Q8 | Lịch sử tìm kiếm (NXL-C Q8) | Tài khoản: lưu server (bảng `search_history`, tối đa 10); Guest: localStorage |
| G4-Q9 | PSP cho NXL-H (GĐ6) | **Chốt (14/10): SePay** — Cổng thanh toán SePay, môi trường sandbox (`pay-sandbox.sepay.vn` form checkout ký HMAC-SHA256, IPN `ORDER_PAID` có header `X-Secret-Key`, API `pgapi-sandbox.sepay.vn` Basic Auth); chi tiết trong NXL-H |
| G4-Q10 | Design còn thiếu: Đang theo dõi, Ctrl+K, kết quả tìm kiếm, Studio (Tổng quan / Nội dung / Lịch / Kênh), mobile + tablet cho Browse / Hồ sơ / Sắp diễn ra | Em viết brief GĐ4 cho agent design; trong lúc chờ dựng theo frame 02/04/05/06/07 + token |

## 11. Tiêu chí hoàn thành

- Given Guest gõ "lien minh" trong Ctrl+K, Then thấy danh mục / kênh "Liên Minh…" trong ≤ 300 ms; Enter mở trang.
- Given người xem follow kênh, When kênh Go Live, Then sidebar hiện kênh live trong ≤ 5 s không cần tải lại; kết thúc →
  offline; trang Đang theo dõi tab Live đúng.
- Given Followers-only 10 phút, When người xem vừa follow gửi tin, Then `CHAT_FOLLOWERS_ONLY` (không còn cần `dev-grant`).
- Given Creator đổi ảnh đại diện + bio + handle, Then hồ sơ mới hiện ở `/:handle`, thẻ live, trang xem và chat.
- Given Creator tạo lịch 20:00, When người xem đặt nhắc và mở trang trước giờ, Then thấy "Sắp diễn ra" + đếm ngược; Creator
  Go Live 19:50 → trang tự chuyển Live ≤ 5 s cùng `streamId`; không phát tới 21:00 → "Đã hủy".
- Given Creator ẩn một VOD, Then VOD biến khỏi hồ sơ, tìm kiếm, Tiếp tục xem; URL trực tiếp → không tìm thấy (trừ chủ kênh).
- Given Browse chọn nhóm "Games" → danh mục, Then thấy live của danh mục sort theo người xem, tab Video có VOD.
- NXL-C v1.2, NXL-H v1.0, NXL-00 v1.4 có trong `DATN/docs/ba/specs`.
