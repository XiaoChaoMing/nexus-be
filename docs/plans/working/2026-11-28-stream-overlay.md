# Overlay OBS cho Creator — chat trong suốt có theme và alert donate

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| **Full** (chat-svc: migration, topic realtime mới, REST; Nginx; nexuslive-web: Studio + trang overlay; tài liệu) | **Draft — chờ duyệt** | 2026-11-28 |

Nhánh làm việc: `feat/stream-overlay` (cả `nexuslive-be` và `nexuslive-web`, tách từ `feat/payout-settlement-and-gifts`).
Web có Working Plan cùng basename, trỏ về đây. TSD (viết sau khi plan này được duyệt): `docs/plans/technical/2026-11-28-stream-overlay.md`.

Lý do chọn Full: có migration (`services/chat/migrations/`), thêm loại topic cho gateway realtime (`entity.TopicKind`,
`gateway.Join`), thêm route Nginx.

## 1. Task Brief

- **Tên:** `2026-11-28-stream-overlay`.
- **Mục đích:** dùng cho **buổi demo đồ án**: Creator phát bằng OBS, chat và quà tặng hiện thẳng lên hình stream; Creator tự
  chỉnh giao diện trong Studio và bấm "Gửi thử" để có cảnh quay ngay, không cần chờ người tặng thật.
- **Mục tiêu:** Creator có **hai link OBS Browser Source**:
  1. **Overlay chat**: khung chat nổi trên stream, **nền trong suốt** (hoặc nền màu có độ mờ), Creator tùy chỉnh **theme**
     (preset, màu chữ, nền, cỡ chữ, font, huy hiệu, tự ẩn tin cũ…).
  2. **Overlay donate**: khi có người tặng quà, hộp alert hiện lên (tên, vật phẩm, số lượng, tổng tiền, lời nhắn), xếp hàng nếu
     nhiều quà, Creator chỉnh màu nhấn, thời lượng, vị trí, hiệu ứng, mức tiền tối thiểu.
  Trang **Studio → Overlay** để lấy link, chỉnh theme, **xem trước** (đúng trang overlay thật, nền caro) và **Gửi thử donate**.
- **Trong phạm vi:**
  - Bảng `overlay_settings` theo kênh (chat-svc), REST đọc / ghi / gửi thử.
  - Topic realtime `overlay:{channelId}`: đẩy cài đặt mới tới OBS ngay khi Creator lưu (không phải tải lại) và phát alert thử.
  - Hai trang overlay trên web (không đăng nhập, không khung app, nền trong suốt).
  - Alert **thật** lấy từ dòng hệ thống `GIFT` đã có trong phòng chat (đi qua AutoMod, idempotent theo event id).
  - Trang Studio → Overlay, i18n vi / en, hướng dẫn OBS (kích thước Browser Source).
- **Ngoài phạm vi:** âm thanh / TTS cho alert, ảnh / GIF alert tùy chỉnh, CSS tự viết, lưu nhiều theme, alert Follow / Sub / Raid,
  goal bar, quà tặng khi kênh offline lên overlay, nexuslive-admin, mobile.
- **Giả định (đây là các quyết định của mình, bạn có thể đổi):**
  - A1. **Không dùng token riêng cho overlay.** Dữ liệu overlay hiển thị (chat, quà) vốn công khai với mọi người xem, nên link
    chỉ cần `channelId`. Việc duy nhất cần quyền Creator là **ghi cài đặt** và **gửi thử**, cả hai đi qua REST có đăng nhập.
    (Khác với bản mình nêu lúc đầu có token chỉ-đọc: cách này ít việc hơn mà không mất tính năng nào.)
  - A2. Overlay nối `/v1/ws` như **khách ẩn danh** (đã được hỗ trợ), đăng ký `chat:{streamId}` và `overlay:{channelId}`.
  - A3. `streamId` hiện tại lấy bằng `GET /v1/channels/{handle}` (`isLive`, `currentStreamId`), hỏi lại mỗi 5 giây khi chưa live, nên bật
    OBS trước khi bấm phát vẫn chạy. (TSD kiểm tra API này nhận `id` hay chỉ `handle`; nếu chỉ `handle` thì URL dùng handle.)
  - A4. Theme là **tập giá trị hữu hạn** (preset + vài trường có khoảng hợp lệ) để kiểm tra phía server và luôn đọc được trên nền
    video; không cho CSS tự do.
  - A5. Chỉ **chủ kênh và admin** đổi cài đặt / gửi thử (như `chat-settings`, Mod không được).
  - A6. Tin ẩn danh tính của overlay **không tính vào số người xem** (xem R2).

## 2. Khu vực bị ảnh hưởng

| Layer | Package / file | Thay đổi |
| --- | --- | --- |
| migration | `services/chat/migrations/20261129000001_overlay_settings.{up,down}.sql` | Bảng `overlay_settings(channel_id PK, chat jsonb, alert jsonb, updated_by, updated_at)` |
| entity | `internal/entity/` | `OverlaySettings` + `Validate()` (enum, khoảng số, `#RRGGBB`), mặc định; `TopicOverlay`; sự kiện `overlay_settings`, `overlay_test_gift` |
| repo | `internal/repo/contracts.go`, `persistent/` | `OverlayRepo.Get/Upsert` |
| usecase | `internal/usecase/contracts.go`, mới `overlay/` | `Overlay.Get`, `Update` (quyền chủ kênh, kiểm tra, lưu, phát `overlay_settings`), `TestGift` (quyền, giới hạn tần suất, phát `overlay_test_gift`) |
| gateway | `usecase/gateway.Join`, `entity/topic.go` | `overlay:{uuid}` đăng ký được cho mọi người, không snapshot; kết nối overlay không `viewers.Join` |
| REST | `controller/restapi/v1/` | `GET` (công khai) / `PUT` `/v1/channels/{channelId}/overlay-settings`, `POST /v1/channels/{channelId}/overlay-test` |
| Nginx | `deploy/nginx/nginx.conf` | Thêm `overlay-settings|overlay-test` vào route chat-svc của `chat-settings` |
| web | `src/features/overlay/` (mới), `studio/overlay-page.tsx`, `app/router.tsx`, locales | Hai route `/overlay/chat/:handle`, `/overlay/alerts/:handle`; Studio tab Overlay; tái dùng `parseChatEvent`, `chatReducer`, `GiftItemIcon` |

## 3. Thay đổi hợp đồng

- `GET /v1/channels/{channelId}/overlay-settings` → `{chat, alert, updatedAt}` (mặc định nếu chưa lưu).
- `PUT` cùng đường dẫn → thay toàn bộ; trả bản đã chuẩn hóa.
- `POST /v1/channels/{channelId}/overlay-test` `{item?: {name, icon, quantity, total}, message?}` → 204; phát `overlay_test_gift`
  tới người đang mở overlay của kênh đó, **không** vào chat thật.
- Realtime: topic `overlay:{channelId}`, sự kiện tạm thời (không seq, không replay): `overlay_settings`, `overlay_test_gift`.
- Cài đặt gồm (đề xuất, TSD chốt giá trị):
  - `chat`: `preset` (GLASS / DARK / NEON / MINIMAL), `background` (TRANSPARENT / SOLID), `bgColor`, `bgOpacity` 0–100,
    `textColor`, `fontFamily` (danh sách cố định), `fontSize` 12–32, `showBadges`, `usernameColor` (PALETTE / UNIFORM),
    `maxMessages` 5–30, `fadeAfterSec` (0 = không ẩn, 5–120).
  - `alert`: `accentColor`, `durationSec` 3–15, `position` (TOP / CENTER / BOTTOM), `animation` (SLIDE / POP / FADE),
    `minTotal`, `showMessage`.

## 4. Ánh xạ lỗi

Dùng lại `VALIDATION_FAILED` (kèm `details`), `UNAUTHORIZED`, `FORBIDDEN` (không phải chủ kênh), `NOT_FOUND`, `RATE_LIMITED`
(gửi thử quá 1 lần / giây). Không thêm mã lỗi mới.

## 5. Sinh code, cấu hình, tài liệu

Swagger (`make` tương ứng), `docs/api/README.md`, spec NXL-B / NXL-00 (topic mới), `features.md`. Không thêm biến môi trường bắt buộc.

## 6. Các bước triển khai

1. TSD (sau khi plan này duyệt) → duyệt.
2. Migration + entity + validation + test đơn vị.
3. Repo + usecase `overlay` + REST + test repo (PG) + test handler.
4. Topic `overlay` trong gateway, loại kết nối overlay khỏi số người xem, test WS.
5. Nginx, Swagger, tài liệu.
6. Web: client API + types, trang overlay chat, trang overlay alert (hàng đợi, thời lượng), hook nối realtime ẩn danh.
7. Web: Studio → Overlay (link + copy, form theme, xem trước bằng iframe, Gửi thử), i18n, hướng dẫn OBS.
8. E2E Playwright, thử với OBS thật, Completed Plan.

**Nếu thiếu thời gian, cắt theo thứ tự:** hiệu ứng alert → font tùy chọn → `fadeAfterSec` → preset còn lại. **Không cắt** xem trước và
Gửi thử.

## 7. Rủi ro

| # | Rủi ro | Biện pháp |
| --- | --- | --- |
| R1 | Actor của hub quen có `seq` (Redis) cho mỗi topic, mà sự kiện overlay là tạm thời | **Đã chốt:** phát qua `fanout.Publisher` như mọi topic (có seq); client chỉ cần sự kiện mới nhất nên không ảnh hưởng. Đã bỏ ý tưởng "không seq" |
| R2 | Overlay vào `chat:{streamId}` như người xem ẩn danh làm số người xem tăng | Kết nối overlay (anonId có tiền tố riêng) bỏ qua `viewers.Join`; test số người xem không đổi |
| R3 | Chat đông làm sampling của topic chat bỏ bớt tin trên overlay | Chấp nhận cho demo (giống người xem); ghi vào Known Debt |
| R4 | Kế hoạch "khách chat" đang soạn cũng đụng Snapshot / WS / `chat_settings` | Tách nhánh, không sửa bảng `chat_settings`, rebase sau khi bên kia vào; migration đặt timestamp riêng |
| R5 | Theme xấu làm chữ khó đọc trên video | Preset đã kiểm tương phản; trường tự do có giới hạn; xem trước trên nền caro và nền video mẫu |
| R6 | Link công khai nên ai cũng mở được overlay của kênh | Chấp nhận: chỉ hiện dữ liệu công khai; ghi quyết định vào TSD |
| R7 | Alert thử bị nhầm là thật khi quay demo | Alert thử có nhãn nhỏ "Thử" trên overlay, không vào chat, không ghi sổ |

## 8. Kiểm tra

- Đơn vị: `OverlaySettings.Validate`, mặc định, hàng đợi alert, lọc `minTotal`.
- Repo (PG `_test`): Get mặc định, Upsert, ghi đè.
- Handler / usecase: chủ kênh ghi được, Mod và người lạ nhận `FORBIDDEN`, khoảng giá trị sai nhận `VALIDATION_FAILED`, gửi thử bị giới hạn tần suất.
- WS: khách ẩn danh đăng ký `overlay:{id}` được; `overlay_test_gift` chỉ tới người đăng ký topic overlay, không tới `chat:*`; overlay không làm tăng số người xem.
- E2E (provider `fake`): đổi theme trong Studio → iframe xem trước và overlay đổi theo, không tải lại; Gửi thử → alert hiện rồi biến mất; tặng quà thật → alert hiện, lời nhắn bị AutoMod chặn thì ẩn; nền overlay chat `transparent`.
- Hồi quy: chat-settings, chat thường, tin hệ thống `GIFT` trong chat, số người xem, `make pre-commit`, E2E hiện có.

## 9. Tiêu chí hoàn thành

- Given Creator mở Studio → Overlay, When bấm "Gửi thử", Then OBS đang mở link alert hiện alert trong ≤ 1 giây và alert tự tắt sau `durationSec`.
- Given Creator đổi màu chữ hoặc preset và bấm Lưu, Then overlay chat đang mở trong OBS đổi theo trong ≤ 2 giây, không tải lại.
- Given nền Transparent, Then Browser Source trong OBS thấy video phía sau khung chat.
- Given một người tặng quà khi kênh đang live, Then alert hiện trên overlay donate, đúng một lần, với tên, vật phẩm, tổng tiền và lời nhắn đã qua AutoMod.
- Given OBS mở overlay trước khi bấm phát, When kênh lên live, Then overlay tự nối vào buổi phát, không cần làm mới.
- Given người không phải chủ kênh gọi `PUT` hoặc `POST overlay-test`, Then `403 FORBIDDEN`.
- Given overlay đang mở, Then số người xem của buổi phát không tăng.

## 10. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| Q1 | Link dùng `channelId` (UUID) hay handle? | **Đã chốt: handle.** `GET /v1/channels/{x}` chỉ nhận handle, nên link dùng handle; overlay tự đổi sang `channelId` từ profile. Hệ quả: đổi handle thì phải copy lại link (Studio luôn hiện link mới) |
| Q2 | Có âm thanh cho alert không? | Không (ngoài phạm vi); thêm sau nếu còn thời gian |
| Q3 | Số preset | 4 preset + chỉnh tay các trường trong §3 |
| Q4 | Alert hiện cho mọi mức tiền? | Mặc định có, Creator đặt `minTotal` nếu muốn lọc |
| Q5 | Kênh offline nhận quà thì sao? | Không hiện (không có phòng chat mở), thông báo in-app có sẵn vẫn chạy |
