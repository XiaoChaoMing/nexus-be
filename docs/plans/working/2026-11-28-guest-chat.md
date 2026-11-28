# Khách (chưa đăng nhập) chat trên kênh

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| **Full** (chat-svc: migration, `config.go`, entity, repo, usecase, WS + REST; `pkg/events`; nexuslive-web; tài liệu) | **Approved** (2026-11-28) | 2026-11-28 |

TSD: `docs/plans/technical/2026-11-28-guest-chat.md` (hợp đồng, SQL, kịch bản). Phía web:
`../nexuslive-web/docs/plans/working/2026-11-28-guest-chat.md` (trỏ về TSD, không chép hợp đồng).

Lý do chọn Full: có migration (`services/chat/migrations/`), thêm biến trong `services/chat/config/config.go`, đổi payload
`pkg/events.ChatModes`, đổi hành vi của method có sẵn trong `usecase/contracts.go` (`Chat.Send`, `Chat.Snapshot`,
`Moderation.Timeout/Ban`).

## 1. Task Brief

- **Tên:** `2026-11-28-guest-chat`.
- **Mục tiêu:** Creator bật **"Cho phép khách chat"** cho kênh, mặc định tắt. Khi bật, khách chưa đăng nhập tự đặt biệt danh
  rồi gửi tin trong phòng chat của kênh. Tin của khách luôn mang huy hiệu **Khách**. Khách bị giới hạn chặt hơn người dùng
  thường. Mod hoặc streamer xóa tin, timeout, ban khách được như với người dùng.
- **Trong phạm vi (phạm vi tối thiểu):**
  - Cờ kênh `guestChat` trong `chat_settings`, mặc định `false`. Chỉ chủ kênh và admin được đổi. Đọc và ghi qua
    `GET/PUT /v1/channels/{channelId}/chat-settings`. Cờ có trong `snapshot.room.mode` và trong sự kiện `mode_change`.
  - **Định danh khách phía server** `guestKey`. `guestKey` là một UUID dẫn xuất bằng HMAC từ `anonId` (xem §3.1). Nó là
    `user.id` của tin khách, `target_user_id` của `mod_actions` và khóa Redis của bộ chặn gửi tin. `anonId` thô không bao
    giờ bị lộ.
  - Biệt danh đi kèm từng tin trong frame `send` (`data.guestName`) và được lưu trên dòng tin (`chat_messages.guest_name`).
    Không có frame mới, không lưu biệt danh trong Redis.
  - Kiểm tra biệt danh: dài 3–20 ký tự; chỉ gồm chữ, số, khoảng trắng và `_ - .`; không chứa link; không dính từ cấm của
    kênh; không thuộc danh sách tên dành riêng; không trùng username thật (bảng `users` sẵn có trong chat-svc); không trùng
    handle hoặc tên hiển thị của kênh (bản sao `channels`; username của chủ kênh và mod đã nằm trong `users`).
  - Chống spam chặt hơn: mỗi khách tối thiểu 10 s/tin trong một phòng (`CHAT_GUEST_SLOW`, không được miễn), một bucket
    riêng cho **mọi khách cùng IP** (`CHAT_GUEST_RATE_LIMIT_IP` = 10 tin / `CHAT_RATE_WINDOW`), tối đa 200 ký tự
    (`CHAT_GUEST_MAX_CHARS`), **luôn chặn link**, không tạo mention. Hai bucket sẵn có (theo người, theo IP) vẫn áp dụng.
  - Followers-only và Subs-only vẫn chặn khách như hiện nay. `CHAT_REQUIRE_VERIFIED_EMAIL=true` cũng chặn khách.
  - Moderation: Delete, Pin, Timeout, Untimeout, Ban, Unban dùng `guestKey` trên các route sẵn có. Danh sách bị cấm trong
    Studio hiện khách kèm biệt danh và cờ `guest`.
  - Bản sửa hiển thị: History, tin ghim và sampling ưu tiên xử lý đúng tin của khách.
  - Tài liệu: `docs/api/README.md`, Swagger, `.env.example`, `docker-compose.yml`, spec NXL-B / NXL-00 / NXL-F,
    decision log.
- **Ngoài phạm vi:**
  - Số người xem: giữ nguyên, khách đã được đếm theo `a:<anonId>`.
  - Ban theo IP (xem Open questions).
  - Giới hạn đổi biệt danh: khách đổi tự do, vì moderation tính theo `guestKey` chứ không theo tên.
  - Khách báo cáo vi phạm: vẫn cần đăng nhập.
  - Thẻ người dùng (user card) cho khách.
  - Hiện biệt danh khách trong admin web và trong nhãn báo cáo của safety-svc.
  - Cấu hình giới hạn khách theo từng kênh.
- **Giả định:**
  - A1. Toàn bộ thay đổi backend nằm trong chat-svc và `pkg/events`. safety-svc, notification-svc, identity, Nginx: không đổi
    code.
  - A2. Khóa HMAC là `NXL_INTERNAL_SECRET`, đã có trong config chat-svc. Thông điệp được tách miền bằng nhãn
    `nxl-guest-key:`. Không thêm biến bắt buộc. Đổi secret này thì mọi `guestKey` đổi theo, nên timeout và ban của khách mất
    hiệu lực. Chấp nhận được vì khách vốn tạm thời.
  - A3. Moderator vẫn đổi được các chế độ khác. Moderator gửi `guestChat` khác giá trị hiện tại thì nhận `403 FORBIDDEN`
    (quyết định 3: "Creator bật/tắt"). Moderator bỏ trống trường này thì giá trị giữ nguyên.
  - A4. Biệt danh không trùng username thật thì đủ. Trùng `display_name` của người khác vẫn được, vì đã có huy hiệu Khách bắt
    buộc và màu theo `guestKey`.
  - A5. Khách không có `anonId` hợp lệ không chat được (`UNAUTHORIZED`), như hiện nay.
  - A6. Khi chủ kênh tắt guest chat, tin cũ của khách vẫn giữ. Lần gửi tiếp theo của khách nhận `UNAUTHORIZED`, web quay về
    nút "Đăng nhập để trò chuyện".
- **Open questions — đã chốt 2026-11-28:** (1) **không** ban theo IP trong task này; (2) biệt danh khách ở admin web / nhãn báo cáo **để Follow-up**. Nội dung câu hỏi gốc giữ lại bên dưới:
  1. **Có cần ban khách theo IP không?** Xóa `localStorage` là có `anonId` mới, tức `guestKey` mới. Ban theo `guestKey` vì
     vậy chỉ cản người không cố tình lách. Phương án tối thiểu dựa vào bucket theo IP và nút tắt guest chat của kênh. Nếu cần
     ban theo IP, ước tính thêm **+6–8 h**: lưu `ip_hash` trên tin khách và `mod_actions.target_ip_hash`, tra trong
     `SendContext`, sửa UI Studio, cân nhắc NAT/CGNAT chặn nhầm cả trường học.
  2. **Có cần hiện biệt danh khách trong admin web và nhãn báo cáo không?** Hiện tại nhãn báo cáo tin của khách là
     `@?: <trích đoạn>`. Muốn hiện tên thì phải thêm `guestName` vào evidence nội bộ, safety-svc lưu và trả trường đó, admin
     web hiển thị: **+4–5 h**, chạm thêm safety-svc và nexuslive-admin.

## 2. Impacted areas

Đã đọc mọi tầng của chat-svc. Mục "Hiện trạng" ghi những gì code đang làm, có chỗ khác mô tả ban đầu.

**Hiện trạng (đã kiểm tra trong code):**

- `anonId` do web sinh: UUID v4 lưu trong **`localStorage`** (`nxl.anonId`, `nexuslive-web/src/features/streams/lib/anon-id.ts`),
  không phải cookie. Web gửi nó trong frame `auth` và trong header `X-NXL-Anon-Id` khi gọi playback. Server **không ký**
  `anonId`: `gateway.Authorize` chỉ `uuid.Parse`.
- Mọi cột định danh đều là `uuid`: `chat_messages.user_id uuid NOT NULL`, `mod_actions.target_user_id uuid`,
  `actor_id uuid NOT NULL`. Tên tác giả **không lưu trên tin**: History, Message, Pinned lấy tên qua
  `LEFT JOIN users u ON u.id = m.user_id`. Nếu dùng thẳng `anonId` (client tự chọn) làm `user_id`, khách có thể gửi
  `anonId` = id của một người dùng thật (id lộ trong mọi payload tin) để mạo danh họ trong History, hoặc để mod ban nhầm
  người thật. `guestKey` (HMAC) giải quyết điều này mà vẫn vừa cột `uuid`, nên `user_id` và `target_user_id` không cần đổi
  kiểu hay ràng buộc.
- Moderation **không** chặn mod tác động lên khách ở tầng schema. Chỗ chặn là
  `restrict → knownUser → entity.ErrNotFound`: id không có trong bảng `users` thì Timeout/Ban trả 404. `lift`
  (Untimeout/Unban) không kiểm tra `knownUser`. `DeleteMessage` và `Pin` không kiểm tra tác giả.
- `SendContext(streamID, userID)` đã chịu được `userID` là UUID bất kỳ: tra hạn chế bằng
  `mod_actions.target_user_id = me.id`. Truyền `guestKey` vào là timeout/ban của khách tự có hiệu lực.
- Các khóa Redis của `SendGuard` (`chat:dedupe:`, `chat:last:`, `chat:slow:`, bucket `chat:rate:`) là chuỗi, nên dùng
  `guestKey` được ngay mà không phải đổi repo.
- Báo cáo tin của khách **đã chạy được**. safety-svc `checkMessage` lấy evidence qua `/internal/v1/chat/evidence`
  (`user_id` = `guestKey`, kiểu uuid hợp lệ). `reports.target_user_id` nhận `guestKey`, nhãn admin hiện `@?`. Audit
  `chat.mod_action.v1` cũng nhận `guestKey` như một `resource_id` uuid. Không có gì vỡ.
- `decorate` (History) thay tên trống bằng `user-xxxxxxxx`. Tin của khách vì vậy phải được xử lý riêng.
- Sampling ưu tiên (`ws/actor.go priority`) so tác giả với `principal().UserID`. Với khách giá trị này rỗng, nên phải đổi
  sang `AuthorID()`.
- Web: `chat-message.tsx` chỉ mở thẻ người dùng khi `user.username` khác rỗng. Tin khách có `username: ""` nên tự nhiên
  không có thẻ.

| Layer | Package / file | Thay đổi |
| --- | --- | --- |
| migration | `services/chat/migrations/20261128000001_guest_chat.{up,down}.sql` | `chat_settings.guest_chat boolean NOT NULL DEFAULT false`; `chat_messages.guest_name varchar(20)` (NULL = tin của người dùng); `mod_actions.target_guest_name varchar(20)` |
| config | `services/chat/config/config.go` | `CHAT_GUEST_SLOW` (10s), `CHAT_GUEST_RATE_LIMIT_IP` (10), `CHAT_GUEST_MAX_CHARS` (200); đều có default, không bắt buộc |
| entity | `internal/entity/entity.go` | `Principal.GuestKey`, `Principal.AuthorID()`; `BadgeGuest = "GUEST"`; `Author.IsGuest()`; `SendInput.GuestName` |
| entity | `internal/entity/settings.go` | `ChatSettings.GuestChat`, `Mode.GuestChat` (`json:"guestChat"`), `SameModes` tính cả cờ này |
| entity | `internal/entity/guest.go` (mới) | `GuestKey(secret, anonID) string`; `GuestNameInput(raw) (string, error)` cho các luật cú pháp, tên dành riêng và link |
| entity | `internal/entity/moderation.go` | `ModAction.TargetGuestName`, `ActiveRestriction.Guest` |
| repo contract | `internal/repo/contracts.go` | **thêm** `ModerationRepo.GuestName(ctx, channelID, guestKey) (string, error)`; các method khác giữ chữ ký |
| repo impl | `internal/repo/persistent/persistent.go` | `_sendContextSQL`, `Settings`, `SaveSettings` đọc và ghi `guest_chat` |
| repo impl | `internal/repo/persistent/messages.go` | `InsertMessage` ghi `guest_name`; `_messageCols` / `scanMessage` đọc `guest_name` → tên hiển thị + huy hiệu `GUEST` |
| repo impl | `internal/repo/persistent/moderation.go` | `insertAction` ghi `target_guest_name`; `_activeRestrictions` lấy tên khách khi không có bản sao user; thêm `GuestName` |
| usecase | `internal/usecase/gateway/gateway.go` | `Authorize` tính `GuestKey` khi `anonId` hợp lệ; `gateway.New` nhận thêm secret |
| usecase | `internal/usecase/chat/chat.go` | `precheck` / `admit` / `load` / `rates` / `slow` / `autoMod` / `store` / `author` theo `AuthorID()` + các luật cho khách; kiểm tra biệt danh |
| usecase | `internal/usecase/chat/read.go` | `me()` cho khách; `Snapshot` dùng `AuthorID()`; `decorate` bỏ qua tin khách; `UpdateSettings` chặn moderator đổi `GuestChat`; `modesOf` |
| usecase | `internal/usecase/moderation/actions.go` | `restrict`: id không có trong `users` → `GuestName` → `NOT_FOUND` nếu không phải khách từng chat; `decorate` (pin) giữ huy hiệu `GUEST` |
| controller WS | `internal/controller/ws/client.go`, `actor.go` | `sendData.GuestName`; `priority` dùng `AuthorID()` |
| controller REST | `restapi/v1/request`, `response`, `v1.go`, `moderation.go` | `UpdateChatSettings.GuestChat *bool` (bỏ trống = giữ nguyên); `ChatSettings.guestChat`; `Restriction.guest`; Swagger |
| events | `pkg/events` (`ChatModes`) | thêm `GuestChat bool json:"guestChat"` (additive; safety-svc lưu vào audit) |
| wiring | `internal/app/app.go` | truyền 3 giá trị config mới vào `chat.Config`, secret vào `gateway.New` |
| tests | `usecase/chat/*_test.go`, `usecase/gateway/*_test.go`, `usecase/moderation/*_test.go`, `entity/*_test.go`, `controller/ws/ws_test.go`, `restapi/v1/*_test.go`, `repo/persistent/persistent_test.go` | xem §8 |
| docs | `docs/api/README.md`, `.env.example`, `docker-compose.yml`, `../docs/ba/specs/NXL-B-live-chat.md` (BR01, AF10, §9B, ma trận quyền), `NXL-00` §10.1–10.3 + §12, `NXL-F` ma trận quyền, `../docs/ba/analysis/decision-log.md` (DL mới) | |

| Transport | Có lộ ra? | Endpoint / RPC / route key |
| --- | --- | --- |
| WebSocket (public, qua edge) | có | `/v1/ws`: `send` thêm `data.guestName`; snapshot `chat:` có `room.mode.guestChat`, `me` của khách; `mode_change` có `guestChat`; tin của khách có huy hiệu `GUEST` |
| REST (public, qua edge) | có | `GET/PUT /v1/channels/{channelId}/chat-settings` (`guestChat`); `GET /v1/channels/{channelId}/restrictions` (`guest`, tên khách); `PUT/DELETE .../timeouts/{userId}`, `.../bans/{userId}` nhận `guestKey`; `GET /v1/chat/rooms/{streamId}/messages` trả tin khách |
| Internal HTTP | không đổi | `/internal/v1/chat/evidence` trả `userId` = `guestKey` cho tin khách (đã như vậy về mặt hình dạng) |
| NATS event | có (additive) | `nxl.chat.settings_changed.v1` (`before/after.guestChat`); `nxl.chat.mod_action.v1` có thể mang `targetUserId` = `guestKey` |
| Nginx | không đổi | không có route mới |

**Luồng hiện có có thể vỡ:**

- **Gửi tin của người dùng thường:** `precheck`, `admit`, `limit` và `store` được sửa. Chỗ dễ vỡ là đổi `p.UserID` sang
  `p.AuthorID()` ở dedupe, slow mode và bucket. Với người dùng hai giá trị này bằng nhau. Test bảng phải giữ mọi ca cũ.
- **Snapshot của khách khi guest chat tắt:** phải ra đúng như cũ (`role GUEST`, `canSend false`, `reason UNAUTHORIZED`).
  E2E `m3.spec.ts` kiểm tra điều này.
- **`PUT chat-settings` từ client cũ** (web hiện gửi `{slowSec, followersMin, subsOnly, blockLinks}`): thiếu `guestChat`
  thì giữ nguyên. Moderator gửi qua popover chế độ chat không bị 403.
- **Moderation lên người dùng thật:** `restrict` vẫn gọi `knownUser` trước. Chỉ khi `NOT_FOUND` mới thử `GuestName`. Thông
  báo lỗi 404 cho uuid lạ giữ nguyên.
- **Danh sách bị cấm:** query `_activeRestrictions` đổi cột tên. Dòng của người dùng phải giữ nguyên `username` /
  `displayName`.
- **History / pin / evidence:** `_messageCols` thêm cột. Mọi `scanMessage` (History, `Message`, `pinnable`, `Pinned`) đổi
  cùng lúc.
- **Partition:** `createPartition` dùng `LIKE chat_messages` và `INSERT … SELECT *`. Cột mới thêm vào bảng cha nên được thêm
  ở mọi partition theo cùng thứ tự. Cần chạy `EnsurePartitions` sau migration trong test repo.
- **Audit (safety-svc):** `ChatModes` thêm trường. Consumer cũ decode bỏ qua trường lạ, nên không vỡ.
- **Viewer count:** không chạm (`ViewerKey` giữ `a:<anonId>`).
- **E2E web:** `m3.spec.ts` (ô khách hiện "Đăng nhập để trò chuyện"), `m4-moderator.spec.ts`, `m4-creator.spec.ts`
  (chat-settings, danh sách bị cấm), `takedown.spec.ts`, `gift.spec.ts`.

## 3. Contract changes

Chi tiết và kiểu dữ liệu ở TSD §4. Tóm tắt:

### 3.1 `guestKey`

`guestKey = UUID(HMAC-SHA256(NXL_INTERNAL_SECRET, "nxl-guest-key:" + anonId)[:16])`, trong đó bit version = 8 và variant =
RFC 4122. Hàm được tính một lần trong `gateway.Authorize`. Nó không đảo ngược được, client không chọn được, và với cùng một
`anonId` thì luôn ra cùng một giá trị.

### 3.2 Go

- `entity.Principal`: thêm `GuestKey string`; `func (p Principal) AuthorID() string` trả `UserID`, hoặc `GuestKey` khi là
  khách.
- `entity.SendInput`: thêm `GuestName string`.
- `entity.ChatSettings` / `entity.Mode`: thêm `GuestChat bool`.
- `repo.ModerationRepo`: **thêm** `GuestName(ctx, channelID, guestKey string) (string, error)`, trả `entity.ErrNotFound` khi
  `guestKey` chưa từng gửi tin trong kênh.
- `usecase.MeSnapshot`: **không thêm trường**. Khách nhận `role "GUEST"`, `badges ["GUEST"]`, `color` theo `guestKey`;
  `canSend` và `reason` theo luật mới.
- Chữ ký của `usecase.Chat`, `usecase.Moderation`, `usecase.Gateway` giữ nguyên. Chỉ hành vi đổi: `gateway.New` thêm tham số
  secret, và `Send`/`Snapshot`/`Timeout`/`Ban` có thêm luật cho khách.

### 3.3 Wire

- WS `send`: `{"op":"send","topic":"chat:{id}","type":"message","clientMsgId":"…","data":{"body":"…","guestName":"…"}}`.
  Khách bắt buộc có `guestName`. Người dùng gửi kèm thì server bỏ qua.
- Tin (`message` event, History, pin): khách có `user.id` = `guestKey`, `username ""`, `displayName` = biệt danh,
  `badges ["GUEST"]`. **Không** thêm trường `guest` vào `Author`: huy hiệu do server gắn là dấu hiệu duy nhất.
- `Mode` (`snapshot.room.mode`, `mode_change`) và REST `ChatSettings`: thêm `guestChat: boolean`.
- REST `PUT chat-settings`: `guestChat?: boolean`, bỏ trống = giữ nguyên.
- REST `Restriction`: thêm `guest: boolean`. Với khách, `user.username = ""` và `user.displayName` = biệt danh lúc bị xử lý.
- `pkg/events.ChatModes`: thêm `guestChat`.

## 4. Error mapping

**Không thêm mã lỗi hay sentinel mới.** Mọi trường hợp dùng lại mã sẵn có, phân biệt bằng `details`.

| Trường hợp | Sentinel | WS `error.code` | HTTP | `details` |
| --- | --- | --- | --- | --- |
| Khách gửi khi kênh tắt guest chat / không có `anonId` / `CHAT_REQUIRE_VERIFIED_EMAIL` bật | `entity.ErrUnauthenticated` | `UNAUTHORIZED` | — | — (như hiện nay) |
| Khách thiếu biệt danh | `entity.ErrValidation` | `VALIDATION_FAILED` | — | `guestName: REQUIRED` |
| Biệt danh sai độ dài / ký tự | `entity.ErrValidation` | `VALIDATION_FAILED` | — | `guestName: LENGTH` \| `FORMAT` |
| Biệt danh trùng username thật, trùng handle/tên kênh, hoặc là tên dành riêng | `entity.ErrValidation` | `VALIDATION_FAILED` | — | `guestName: TAKEN` |
| Biệt danh chứa từ cấm của kênh hoặc link | `entity.ErrValidation` | `VALIDATION_FAILED` | — | `guestName: BLOCKED` |
| Tin của khách dài hơn `CHAT_GUEST_MAX_CHARS` | `entity.ErrValidation` | `VALIDATION_FAILED` | — | `body: LENGTH` |
| Tin của khách có link (dù kênh không bật chặn link) | `entity.ErrContentBlocked` | `CHAT_CONTENT_BLOCKED` | — | `reason: LINK` |
| Khách gửi trước khi hết 10 s | `entity.ErrSlowMode` | `CHAT_SLOW_MODE` | — | `retryAfterSec` |
| Bucket khách theo IP cạn | `entity.ErrRateLimited` | `RATE_LIMITED` | — | `retryAfterSec` |
| Followers-only / Subs-only | `ErrFollowersOnly` / `ErrSubsOnly` | `CHAT_FOLLOWERS_ONLY` / `CHAT_SUBS_ONLY` | — | như hiện nay |
| Khách bị timeout / ban | `ErrTimedOut` / `ErrBanned` | `CHAT_TIMED_OUT` / `CHAT_BANNED` | — | `until` |
| Moderator đổi `guestChat` | `entity.ErrForbidden` | — | 403 | `FORBIDDEN` |
| Timeout/Ban một uuid không phải user và chưa từng chat như khách trong kênh | `entity.ErrNotFound` | — | 404 | `NOT_FOUND` (như hiện nay) |

Event consumer: không có consumer mới. safety-svc decode `ChatModes` có thêm trường, không đổi cách skip hay retry.

## 5. Generated artifacts, config & docs

- [ ] mocks: `go generate ./...` trong `services/chat`, vì `repo.ModerationRepo` có method mới
- [ ] `make nxl-swag`: Swagger của chat-svc (`UpdateChatSettings`, `ChatSettings`, `Restriction`)
- Biến môi trường (không bắt buộc, có default; thêm vào `.env.example` và khối `environment` của chat-svc trong
  `docker-compose.yml`):

| Biến | Default | Ý nghĩa |
| --- | --- | --- |
| `CHAT_GUEST_SLOW` | `10s` | Khoảng cách tối thiểu giữa hai tin của một khách trong một phòng (`max` với slow mode của kênh) |
| `CHAT_GUEST_RATE_LIMIT_IP` | `10` | Số tin trên mỗi `CHAT_RATE_WINDOW` cho mọi khách chung một IP |
| `CHAT_GUEST_MAX_CHARS` | `200` | Độ dài tối đa (rune) của tin khách |

- Nginx: không đổi. Không thêm phụ thuộc module.
- Docs: `docs/api/README.md` (dòng WS và chat-settings); spec NXL-B v1.x (BR01, AF10, §9B, ma trận §15/§16), NXL-00
  (§10.1 `auth`, §10.2 `send`, §10.3 snapshot, §12 hằng số), NXL-F (ma trận quyền: Mod timeout/ban khách); `decision-log.md`
  ghi DL mới cho bốn quyết định đã chốt.

## 6. Implementation steps

| # | Bước | Giờ |
| --- | --- | --- |
| 1 | Migration up/down; chạy `up → down → up`; repo test `EnsurePartitions` sau migration | 1.0 |
| 2 | Entity: `GuestKey`, `AuthorID`, `BadgeGuest`, `IsGuest`, `GuestNameInput`, `GuestChat` trong settings/mode/`SameModes`; unit test | 2.0 |
| 3 | Config + wiring (`config.go`, `app.go`, `.env.example`, compose) | 0.5 |
| 4 | Gateway: `Authorize` tính `GuestKey`; test HMAC ổn định, `anonId` sai → không có key | 0.5 |
| 5 | Repo: settings + `SendContext` (`guest_chat`); `InsertMessage`/`scanMessage` (`guest_name`); `insertAction` + `_activeRestrictions` + `GuestName`; mocks; repo tests | 3.0 |
| 6 | Usecase chat: `AuthorID()` thay `UserID` ở dedupe/slow/bucket/`SendContext`; `admit` cho khách; `limit` khách (slow cưỡng bức, bucket IP khách, link, độ dài); `store` (tên, huy hiệu, không mention); kiểm tra biệt danh (từ cấm, username, tên kênh); `me()`; `decorate`; `UpdateSettings` chặn moderator | 4.0 |
| 7 | Usecase moderation: `restrict` cho khách; `decorate` pin | 1.0 |
| 8 | Controller: WS `sendData.guestName`, `priority`; REST request/response, `settingsOf`, `restrictionOf`; Swagger | 1.5 |
| 9 | `pkg/events.ChatModes.GuestChat` + `modesOf` | 0.5 |
| 10 | Test bảng usecase (chat, moderation), ws, restapi, theo §8 | 3.5 |
| 11 | Tài liệu API, spec, decision log | 1.5 |
| 12 | Kiểm tra: `make format/nxl-lint/nxl-test`, stack + luồng cũ (§8) | 2.0 |
| | **Tổng backend** | **≈ 21 h** |

Web (Working Plan riêng): ≈ 14 h. **Tổng ≈ 35 h.**

Thứ tự: backend tới Completed Plan, hoặc hai bên chốt hợp đồng TSD v1.0 làm mock, rồi web mới tích hợp.

**Nếu cần cắt thêm** (mỗi mục tiết kiệm ≈ 1 h, đánh đổi ghi kèm):

- Bỏ kiểm tra trùng username và tên kênh, chỉ dựa vào huy hiệu Khách. Mạo danh vẫn bị huy hiệu bóc trần nhưng dễ gây nhầm
  hơn.
- Bỏ luật "moderator không được đổi `guestChat`". Mod sẽ bật/tắt được, lệch quyết định 3.

## 7. Risks

- **Lách moderation bằng `anonId` mới.** Xóa `localStorage` hoặc mở cửa sổ ẩn danh là có danh tính khách mới. Giảm nhẹ:
  bucket theo IP, 10 s/tin, chặn link, và nút tắt guest chat của chủ kênh. Ban theo IP: Open question 1.
- **NAT / CGNAT:** nhiều khách thật chung một IP (trường học, nhà mạng di động) chia chung 10 tin/30 s. Có thể nâng
  `CHAT_GUEST_RATE_LIMIT_IP` mà không phải deploy code.
- **Đổi `NXL_INTERNAL_SECRET`** làm mọi `guestKey` đổi, nên timeout và ban của khách mất hiệu lực (A2).
- **Mạo danh bằng `display_name`:** chỉ kiểm tra username (gồm cả chủ kênh và mod) cùng handle/tên hiển thị của kênh. Huy hiệu Khách và màu cố định theo
  `guestKey` là lớp bảo vệ còn lại (A4).
- **Admin xử lý báo cáo tin của khách:** nhãn hiện `@?`. Nếu admin chọn khóa tài khoản `targetUserId`, identity không có user
  đó nên hành động không có tác dụng. Admin vẫn xóa được tin. Ghi vào Follow-ups (Open question 2).
- **Hiệu năng:** mỗi tin của khách thêm 1 query `UserIDsByUsername` (dùng index `users_username_idx`) và 1 query
  `Users.Channel`. Nhờ 10 s/tin, tải thêm không đáng kể.
- **Controller `updateSettings`** điền trường bỏ trống bằng `Settings()`. Nếu đọc lỗi thì giá trị thành `false`. Với
  `guestChat` thì `false` là giá trị an toàn, nên vẫn giữ pattern có sẵn.

## 8. Validation checklist

- [ ] `make format` · [ ] `make nxl-lint` · [ ] `make nxl-test` (Windows không cgo: `NXL_TEST_FLAGS=`)
- [ ] `NXL_TEST_PG_URL_CHAT=… make nxl-test`: repo tests (settings, tin khách, restrictions khách, `GuestName`, partition)
- [ ] Migration `up → down → up` (Git Bash)
- [ ] `make nxl-swag && git diff --exit-code` chỉ khác ở các DTO đã nêu
- [ ] `make nxl-up-all`, sau đó chạy các luồng qua edge `http://localhost:8080`:
  - khách khi guest chat **tắt** → `UNAUTHORIZED`, snapshot như cũ;
  - creator bật → khách gửi có biệt danh → người dùng thấy tin có huy hiệu `GUEST`;
  - khách gửi lần hai trong 10 s → `CHAT_SLOW_MODE`; gửi link → `CHAT_CONTENT_BLOCKED LINK`; biệt danh = username có thật
    → `VALIDATION_FAILED guestName TAKEN`;
  - mod timeout khách → `user_cleared`, khách nhận `CHAT_TIMED_OUT`; untimeout → `user_restored`; ban/unban;
    `GET restrictions` hiện khách;
  - followers-only / subs-only bật → khách bị chặn;
  - moderator `PUT chat-settings` có `guestChat` khác giá trị hiện tại → 403; không gửi `guestChat` → 200.
- [ ] Hostile paths: `anonId` sai định dạng, `guestName` 10 000 ký tự, `guestName` toàn khoảng trắng, timeout một uuid ngẫu
  nhiên (404), Redis down (bucket lỗi → lỗi nội bộ như hiện nay), token hết hạn.
- [ ] Luồng cũ chạy lại: người dùng gửi tin, slow mode, followers-only, subs-only, từ cấm, chặn link, xóa/ghim/timeout/ban
  người dùng, user card, báo cáo tin (cả tin khách), viewer count, gift/sub announce, `pnpm e2e` (`m3`, `m4-moderator`,
  `m4-creator`, `takedown`, `gift` + spec mới `guest-chat`).

## 9. Acceptance criteria

- Given kênh chưa bật guest chat, When khách bấm ô chat, Then vẫn hiện "Đăng nhập để trò chuyện" và server trả
  `UNAUTHORIZED` nếu khách gửi tin.
- Given chủ kênh bật "Cho phép khách chat", When khách nhập biệt danh hợp lệ và gửi tin, Then mọi người trong phòng thấy tin
  với tên đó và huy hiệu Khách, còn History tải lại vẫn hiện đúng như vậy.
- Given khách vừa gửi một tin, When khách gửi tin thứ hai trong vòng 10 s, Then nhận `CHAT_SLOW_MODE` kèm `retryAfterSec`.
- Given khách chọn biệt danh trùng username của một tài khoản, When gửi, Then nhận `VALIDATION_FAILED` với
  `guestName = TAKEN`.
- Given mod mở menu tin của khách, When chọn Timeout 10 phút, Then tin của khách trong phòng bị ẩn (`user_cleared`), khách
  thấy ô chat khóa có đồng hồ đếm, và Studio → Bị cấm hiện khách với biệt danh và nhãn Khách.
- Given Followers-only hoặc Subs-only đang bật, When khách gửi, Then bị chặn như hiện nay.
- Given một moderator, When đổi `guestChat`, Then nhận 403. Khi đổi các chế độ khác thì vẫn thành công.
- Given kênh đang bật guest chat, When chủ kênh tắt, Then mọi người nhận `mode_change` có `guestChat: false` và khách quay
  về nút đăng nhập.

## Follow-ups

- Ban khách theo IP (Open question 1).
- `guestName` trong evidence, nhãn báo cáo safety-svc và admin web (Open question 2).
- Giới hạn tần suất đổi biệt danh.
- Cho phép khách @mention người dùng (hiện không resolve mention, `@tên` chỉ là chữ).
- Giới hạn khách cấu hình theo kênh (slow riêng, độ dài riêng).
- Sửa pattern `updateSettings` để đọc settings lỗi thì trả lỗi thay vì reset trường bỏ trống về `false` (áp dụng cho cả
  `blockLinks`).
