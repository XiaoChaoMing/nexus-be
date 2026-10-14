# GĐ2 — Đóng Streaming core (playback, tiến độ xem, giao diện R2/R3/R6, NXL-G2)

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Full (nhiều gói, stream-svc + web, cross-repo) | Approved 14/10 (G2-Q1…Q4 theo đề xuất) — WP1–WP6 xong 14/10 (NXL-G2 v1.0) | 2026-10-14 |

Lịch lộ trình GĐ2: 16/11 → 04/12, mốc **M2**. Lõi GĐ2 (stream key, state machine, media-svc, VOD, Studio, trang
xem 8/9 trạng thái) đã làm ở L1–L4; GĐ1 đã nối Creator/kênh thật. Plan này gom **phần còn lại** để đóng GĐ2 sớm
(~4 tuần trước lịch), rồi chuyển sang GĐ3 Chat.

## 1. Task Brief

**Mục tiêu (M2 — lộ trình §3):** Creator mở OBS → Go Live → người xem vào trang xem theo design mới, đổi
720p/480p/360p → tắt OBS → "Streamer sẽ quay lại ngay" → hết thời gian chờ → "Buổi phát đã kết thúc" → xem VOD
tiếp tục đúng chỗ đã xem, kể cả khi đổi máy (theo tài khoản).

**Nguồn:**

| Nguồn | Phần dùng |
| --- | --- |
| Lộ trình §3 GĐ2 | Playback session, kiểm tra mature; WatchProgress; trang xem đủ trạng thái; NXL-G2 |
| NXL-A v1.1 | §7A–§7C, §9A–§9C (bố cục, overlay, trạng thái), BR mature/age gate, API playback, E11/E12 |
| NXL-C v1.1 | Home (Hero, Tiếp tục xem, Live đề xuất), API "continue watching" |
| NXL-G1 v1.0 | Studio, vòng đời stream |
| Design `DATN/stitch_document_design_interface` | `home_discovery_page` (R2), `live_stream_page` (R3), `obsidian_pulse/DESIGN.md` (token) |
| ERD stream v1.2 | Bảng `playback_sessions`, `watch_progress`, `mature_consents` (đã có, chưa dùng) |

**Trong phạm vi:** WP1–WP6 ở §2.

**Ngoài phạm vi (ghi ở §9):**

- URL HLS có chữ ký (`secure_link`), chặn xem kênh bị khóa → GĐ5.
- Category trong Stream Manager, trạng thái "Sắp diễn ra" (frame `live_stream_page_upcoming_state`), lịch phát → GĐ4.
- Chat, viewer count thật → GĐ3 (trang xem chừa sẵn cột chat).
- Follow thật, Subscribe, Clips, Bảng huy hiệu, "Danh mục phổ biến" → GĐ4/GĐ6.

**Giả định:**

- Không migration mới: dùng 3 bảng sẵn có ở `nxl_stream`. Nếu cần chỉ mục thêm thì viết migration bổ sung
  (không sửa migration cũ).
- Không thêm thư viện Go hay web.
- Số người xem trên design hiện "—" hoặc ẩn tới GĐ3.

**Câu hỏi mở:** §10.

## 2. Gói việc

### WP1 — Playback session và cổng 18+ phía server (stream-svc)

| Việc | Chi tiết | Nguồn |
| --- | --- | --- |
| Mở phiên xem | `POST /v1/streams/{id}/playback`, `POST /v1/vods/{id}/playback` (Guest được gọi) → `{sessionId, manifestUrl, expiresAt, mature: {required, acknowledged}}`; ghi `playback_sessions` (user hoặc `anonId` từ header `X-NXL-Anon-Id` do web sinh) | NXL-A API, ERD |
| Cổng 18+ | Stream/VOD `isMature` và chưa xác nhận → `manifestUrl` rỗng, `mature.required = true`; gửi lại với `{acknowledgeMature: true}` → mở. Đã đăng nhập: lưu `mature_consents (user, channel)`, lần sau không hỏi; `preferences.matureVisible` (GĐ1) cũng cho qua | NXL-A BR mature, FR-ST-12 |
| Web | Player lấy `manifestUrl` từ phiên xem thay vì tự ghép; overlay 18+ gọi lại API | NXL-A §9B |
| Chuẩn bị GĐ5 | `manifestUrl` đi qua một hàm duy nhất để GĐ5 gắn chữ ký `secure_link` mà không đổi hợp đồng | — |

### WP2 — Tiến độ xem VOD theo tài khoản (stream-svc + web)

- `PUT /v1/me/watch-progress/{vodId}` `{positionSec, durationSec}` (web gửi tối đa 15 s/lần và khi rời trang).
- `GET /v1/me/continue-watching?limit=12` → VOD xem dở (bỏ VOD đã xem gần hết hoặc hết hạn), mới nhất trước.
- `GET /v1/vods/{id}` thêm `viewer.positionSec` khi có token.
- Web: đã đăng nhập dùng API; Guest giữ localStorage như hiện tại; **khi đăng nhập, gộp dữ liệu localStorage lên
  tài khoản** (giữ vị trí xa hơn).

### WP3 — R2 Trang chủ theo `home_discovery_page`

| Khối | GĐ2 làm | Ghi chú |
| --- | --- | --- |
| Hero | Stream nổi bật = stream đang live mới nhất (sau này theo đề xuất); nút "Xem ngay", Follow (gated, GĐ4) | Ẩn số người xem tới GĐ3 |
| Tiếp tục xem | Từ WP2 (hoặc localStorage cho Guest); ẩn khi rỗng | NXL-C |
| Kênh trực tiếp đề xuất | Lưới live hiện có, thẻ theo design (thumbnail, badge TRỰC TIẾP, avatar, tên kênh, tag) | Bộ lọc nhóm danh mục → GĐ4 |
| Danh mục phổ biến | Không làm | GĐ4 |
| Creator CTA | Giữ (GĐ1), làm lại theo design | — |
| Trạng thái | Skeleton, rỗng, lỗi + Thử lại | PRD §6 |

### WP4 — R3 Trang xem live theo `live_stream_page`

- Bố cục desktop ≥ 1280: player + thẻ thông tin kênh (avatar, tên, tiêu đề, tag, ngôn ngữ, "bắt đầu N giờ trước",
  Follow) + tab (Giới thiệu = bio kênh; Lịch phát/Clips/Huy hiệu ẩn) + **cột chat bên phải giữ chỗ** ("Chat sẽ có ở
  GĐ3", thu gọn được).
- Theater mode, tablet, mobile (player trên, thông tin dưới, chat thành tab).
- Giữ đủ 8 trạng thái hiện có; trạng thái thứ 9 "Sắp diễn ra" để GĐ4.
- Trang VOD dùng cùng khung (không có cột chat).

### WP5 — R6 Dọn token, responsive, a11y

- Chuyển các token cũ còn sót sang Obsidian Pulse (`DESIGN.md`), xóa biến không dùng.
- Kiểm tra 360 / 768 / 1280 / 1920 px cho Home, Live, VOD, Studio, Auth.
- a11y: thứ tự tab, focus nhìn thấy, nhãn ARIA cho player và nút icon, tương phản (WCAG AA theo PRD §7.5), phím
  tắt player không cướp phím khi đang gõ.
- Playwright: kiểm tra bàn phím + kích thước màn hình cho 3 trang chính.

### WP6 — Tài liệu và M2

- **NXL-G2 v1.0 — Creator Studio** (lộ trình §4 tuần 7): Stream Manager, sức khỏe stream, quản lý key, VOD của
  kênh (danh sách/ẩn/xóa — làm ở GĐ4), Dashboard tối thiểu; cùng khung với NXL-G1.
- NXL-A v1.2: hợp đồng playback + tiến độ xem như đã làm; NXL-C v1.2: Home GĐ2.
- API catalog, ERD stream (đánh dấu 3 bảng đã dùng), README web.
- **Kịch bản demo M2** + E2E M2 (Playwright + ffmpeg) chạy đúng chuỗi ở §1.
- Phiếu đo: ông chủ đo độ trễ OBS thật khi tiện (không chặn đóng GĐ2).

## 3. Khu vực bị ảnh hưởng

| Lớp | File / package | Gói |
| --- | --- | --- |
| stream entity, repo, usecase | `PlaybackSession`, `WatchProgress`, `MatureConsent`; repo mới (không đổi interface cũ) | WP1, WP2 |
| stream REST | `/v1/streams/{id}/playback`, `/v1/vods/{id}/playback`, `/v1/me/watch-progress/*`, `/v1/me/continue-watching` | WP1, WP2 |
| Nginx | `/v1/me/watch-progress`, `/v1/me/continue-watching` → stream (còn lại `/v1/me` → identity) | WP2 |
| Web | `features/player`, `features/watch`, `features/vod`, `features/home`, `app/shell`, `styles/tokens.css` | WP1–WP5 |
| Tài liệu | `DATN/docs/ba/specs` (NXL-G2 mới, NXL-A, NXL-C), `docs/api`, ERD | WP6 |

| Transport | Có | Endpoint |
| --- | --- | --- |
| REST công khai | Có | §3 |
| NATS | Không mới | — |

**Luồng có thể hỏng:** player (đổi nguồn manifest), trang VOD (resume), Home (lưới live), route `/v1/me*` ở Nginx
(chia giữa identity và stream), E2E livestream.

## 4. Mã lỗi

`NOT_FOUND` (stream/VOD không tồn tại hoặc VOD hết hạn), `AGE_RESTRICTED` 403 khi gọi lại không kèm xác nhận
trong ngữ cảnh bắt buộc (NXL-00 §5), `VALIDATION_FAILED` (vị trí âm, lớn hơn thời lượng), `UNAUTHORIZED`
(watch-progress cần token).

## 5. Sinh code / tài liệu

- [ ] `make nxl-swag` (stream). [ ] TSD `docs/plans/technical/2026-10-14-gd02-streaming-close.md` (hợp đồng WP1, WP2).

## 6. Thứ tự

| # | Việc | Phụ thuộc |
| --- | --- | --- |
| 1 | TSD (WP1, WP2) | Plan duyệt |
| 2 | WP1 + WP2 backend | 1 |
| 3 | WP5 dọn token (làm trước để R2/R3 dùng token sạch) | Plan duyệt |
| 4 | WP3 R2, WP4 R3 + nối WP1/WP2 | 2, 3 |
| 5 | WP5 responsive + a11y | 4 |
| 6 | WP6 tài liệu, E2E M2, Completed Plan | 1–5 |

Ước lượng: ~6–7 ngày công.

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Design có nhiều khối chưa có dữ liệu (chat, viewer, category, clip) | Ẩn hoặc giữ chỗ có ghi chú giai đoạn; không dựng dữ liệu giả trên UI thật |
| Đổi nguồn manifest làm vỡ player/E2E | Giữ fallback ghép URL cũ sau cờ cho tới khi E2E qua |
| `/v1/me*` chia hai service ở Nginx | Route cụ thể đặt trước route mặc định; test định tuyến như GĐ1 |
| Gộp tiến độ localStorage lên tài khoản ghi đè sai | Chỉ ghi khi vị trí local lớn hơn; test đơn vị |

## 8. Kiểm tra

- [ ] Lint, `go test -race`, repo test PG (stream); web `tsc`, `oxlint`, `vitest`, build.
- [ ] E2E: M2, livestream, guest modal, M1 (hồi quy); admin.
- [ ] Hostile: playback stream không tồn tại, VOD hết hạn, mature không xác nhận, watch-progress không token,
  vị trí âm.

## 9. Lệch so với lộ trình / spec

| Nguồn | Yêu cầu | Làm | Lý do |
| --- | --- | --- | --- |
| Lộ trình GĐ2 | Playback session **có chữ ký** | Phiên xem không chữ ký; chữ ký ở GĐ5 | `secure_link` gắn cùng Admin/khóa kênh (GĐ5) |
| NXL-A §9C | 9 trạng thái | 8 (thiếu "Sắp diễn ra") | Cần Schedule (GĐ4) |
| Lộ trình GĐ2 | Stream Manager có category | Không | Category ở GĐ4 |
| Design R2/R3 | Chat, viewer count, category, clip, subscribe | Ẩn / giữ chỗ | Tính năng thuộc GĐ3–GĐ6 |

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| G2-Q1 | Làm playback session (chưa chữ ký) ngay, hay dời cả sang GĐ5? | Làm ngay: cổng 18+ phía server và tiến độ xem theo tài khoản cần nó; GĐ5 chỉ thêm chữ ký |
| G2-Q2 | Frame trong `stitch_document_design_interface` là design chính cho R2/R3 (thay pen.dev)? | Dùng các frame này + `obsidian_pulse/DESIGN.md` |
| G2-Q3 | Khối chưa có dữ liệu trên design: ẩn hay giữ chỗ? | Chat: giữ chỗ (GĐ3 lắp vào). Còn lại: ẩn |
| G2-Q4 | Khi đăng nhập có gộp tiến độ xem của trình duyệt lên tài khoản? | Có, giữ vị trí xa hơn |

## 11. Tiêu chí hoàn thành (M2)

- Given Creator seed phát bằng OBS/ffmpeg, When người xem mở trang chủ, Then Hero hiện buổi phát và vào được trang
  xem theo design mới, đổi được 720p/480p/360p.
- Given tắt OBS, Then "Streamer sẽ quay lại ngay"; hết thời gian chờ → "Buổi phát đã kết thúc" + nút xem lại.
- Given đã xem VOD tới phút 3 trên trình duyệt A (đã đăng nhập), When mở VOD trên trình duyệt B cùng tài khoản,
  Then phát tiếp từ phút 3 và VOD nằm trong "Tiếp tục xem".
- Given stream 18+, When Guest mở, Then không tải manifest trước khi xác nhận; tài khoản đã xác nhận một lần thì lần
  sau không hỏi lại với kênh đó.
- Given màn hình 360 px, Then trang xem dùng được (player, thông tin, tab chat giữ chỗ), không cuộn ngang.
- NXL-G2 v1.0 có trong `DATN/docs/ba/specs`.
