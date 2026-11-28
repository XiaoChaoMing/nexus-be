# Completed — Khách (chưa đăng nhập) chat trên kênh

Track: **Full**. Working Plan: `docs/plans/working/2026-11-28-guest-chat.md` (Approved). TSD:
`docs/plans/technical/2026-11-28-guest-chat.md` (v1.0 Approved → **v1.1** đồng bộ as-shipped). Nhánh `feat/guest-chat`.

Hai câu hỏi mở đã chốt và được tôn trọng: **không** ban theo IP; biệt danh khách trong admin web / nhãn báo cáo safety-svc
để Follow-up.

## 1. Delivered

- **Migration** `services/chat/migrations/20261128100001_guest_chat.{up,down}.sql`: `chat_settings.guest_chat boolean NOT NULL
  DEFAULT false`, `chat_messages.guest_name varchar(20)`, `mod_actions.target_guest_name varchar(20)`. `down` xóa mềm (và bỏ
  ghim) tin khách còn hiện trước khi bỏ cột, như TSD §4.1.
- **Entity**:
  - `Principal.GuestKey`, `Principal.AuthorID()`; `BadgeGuest = "GUEST"`, `Author.IsGuest()`, `GuestAuthor(key, name)`;
    `SendInput.GuestName`.
  - `guest.go`: `GuestKey(secret, anonID)` = HMAC-SHA256(secret, `"nxl-guest-chat:v1:"+anonID`) cắt 128 bit, layout UUID
    version 8 / variant RFC 4122. `GuestNameInput(raw)` kiểm tra NFC, gộp khoảng trắng, 3–20 rune, chỉ chữ/số/space/`_ - .`,
    ít nhất một chữ hoặc số, không link, không tên dành riêng (so theo nguyên từ sau `Normalize`). Hằng `DetailTaken`,
    `DetailBlocked`.
  - `ChatSettings.GuestChat`, `Mode.GuestChat` (`json:"guestChat"`), `SameModes` so cả cờ này; `ModAction.TargetGuestName`,
    `ActiveRestriction.Guest`.
- **Config** (không bắt buộc, có default): `CHAT_GUEST_SLOW=10s`, `CHAT_GUEST_RATE_LIMIT_IP=10`, `CHAT_GUEST_MAX_CHARS=200`
  trong `config.go`, `.env.example`, khối `x-chat` của `docker-compose.yml`. Khóa HMAC = `NXL_INTERNAL_SECRET` (đã có), truyền qua
  `gateway.Deps.GuestSecret` trong `app.go`.
- **Gateway**: `Authorize` không token + `anonId` hợp lệ → `GuestKey`. Tài khoản không bao giờ có `GuestKey`.
- **Repo**:
  - `SendContext`, `Settings`, `SaveSettings` đọc/ghi `guest_chat`. `InsertMessage` ghi `guest_name` cho tin có huy hiệu
    `GUEST`.
  - `_messageCols` / `scanMessage` (History, `Message`, `Pin`, `Pinned`) trả tin khách dạng `GuestAuthor`.
  - `insertAction` ghi `target_guest_name`; `_activeRestrictions` lấy tên khách khi không có `users`, cờ `Guest`.
  - Method mới `ModerationRepo.GuestName(ctx, channelID, guestKey)` (contract chỉ **thêm**).
- **Use case chat**:
  - Dedupe, `SendContext`, bucket, slow, `RememberMessage` dùng `AuthorID()`.
  - `admit`: khách bị chặn nếu thiếu key, kênh tắt guest chat, hoặc `CHAT_REQUIRE_VERIFIED_EMAIL` bật.
  - Biệt danh: cú pháp → từ cấm (`BLOCKED`) → trùng username (`TAKEN`) → trùng handle/tên kênh sau `Normalize` (`TAKEN`).
  - Tin khách: tối đa 200 rune (`body: LENGTH`), luôn chặn link, bucket `chat:rate:gip:{ip}` chung mọi khách cùng IP,
    slow `max(slowSec, 10s)` không miễn, không resolve mention, tác giả `GuestAuthor`.
  - `Snapshot`/`me`: khách được phép → `role GUEST`, `badges [GUEST]`, `color ColorOf(guestKey)`, `canSend` theo luật. Khách
    không được phép → hình dạng cũ (`badges []`, `reason UNAUTHORIZED`).
  - `decorate` (History) bỏ qua tin khách; `UpdateSettings` trả `ErrForbidden` khi moderator đổi `guestChat`; `modesOf`
    có `GuestChat`.
- **Use case moderation**: `restrict` → `target()`: tài khoản như cũ; không phải tài khoản thì `GuestName` → ghi
  `TargetGuestName`; không phải khách đã chat trong kênh → `NOT_FOUND` như cũ. `decorate` (pin) giữ huy hiệu `GUEST`.
  Untimeout/Unban nhận `guestKey` như mọi uuid.
- **Controller**:
  - WS: `sendData.guestName` → `SendInput.GuestName`; sampling `priority` dùng `AuthorID()`.
  - REST: `UpdateChatSettings.guestChat *bool` (bỏ trống = giữ nguyên, qua helper `settingsFrom`), `ChatSettings.guestChat`,
    `Restriction.guest`. Swagger annotations cập nhật.
- **Events**: `pkg/events.ChatModes.GuestChat` (`json:"guestChat"`, additive).
- **Tài liệu**: `docs/api/README.md` (WS, chat-settings, timeouts/bans, restrictions); Swagger chat-svc regenerate.
- **Tests**:
  - `entity/guest_test.go`: `GuestKey`, `AuthorID`, bảng `GuestNameInput` (mọi detail code), `GuestAuthor`, `SameModes`.
  - `usecase/chat/guest_test.go`:
    - `TestSendGuestRules` (bảng): tắt/bật guest chat, thiếu key, email bắt buộc; biệt danh `REQUIRED`/`LENGTH`/`FORMAT`/link
      `BLOCKED`/từ cấm `BLOCKED`/dành riêng `TAKEN`/trùng username `TAKEN`/trùng handle và tên kênh `TAKEN`; `body LENGTH`;
      link luôn chặn; slow 10 s và slow kênh dài hơn; bucket IP khách (tài khoản cùng IP không bị ảnh hưởng);
      followers-only / subs-only; timeout / ban theo `guestKey`.
    - `TestSendGuestMessage`: tác giả, không mention, dedupe theo key, History.
    - `TestSnapshotGuest`, `TestUpdateSettingsGuestChat`: owner/admin đổi được; mod bật/tắt → 403; mod đổi chế độ khác vẫn
      được; viewer 403.
  - `usecase/moderation`: `TestRestrictGuest`, `TestRestrictAccountHasNoGuestName`.
  - `usecase/gateway`: `TestAuthorizeGuestKey`.
  - `controller/ws`: guestName tới use case.
  - `restapi/v1`: `guestChat` giữ nguyên khi bỏ trống / đổi khi gửi.
  - `repo/persistent/guest_test.go`: settings, `SendContext` với key, insert/History/Message/Pin/Pinned tin khách,
    `GuestName`, Restrict/Restrictions/Lift theo key, `EnsurePartitions` sau migration.
  - Toàn bộ test cũ của người dùng thường giữ nguyên và pass.

## 2. Deviations from the plan

- **Tên migration** `20261128100001_guest_chat` (TSD v1.0 ghi `20261128000001`): tránh trùng với migration chat-svc của nhánh
  `feat/stream-overlay` làm song song cùng ngày. **Khi merge cần kiểm thứ tự migration chat-svc với nhánh `feat/stream-overlay`**
  (golang-migrate chạy theo timestamp; nếu overlay có migration cùng ngày với timestamp lớn hơn đã apply ở môi trường nào đó
  thì phải đổi lại số cho đúng thứ tự). TSD đã sync (v1.1).
- **Thứ tự kiểm tra biệt danh**: TSD v1.0 §4.4 bước 1 chạy `GuestNameInput` trong `precheck`. Thực tế chạy **sau `admit`**,
  để kênh tắt guest chat luôn trả `UNAUTHORIZED` (S4, bảng lỗi WP §4) kể cả khi web cũ gửi không có `guestName`. Đổi lại,
  biệt danh sai vẫn tốn một query `SendContext` (không tốn token rate). TSD v1.1 cập nhật §4.4 và sơ đồ S1.
- **Nhãn HMAC**: WP §A2/§3.1 ghi `nxl-guest-key:`, TSD ghi `nxl-guest-chat:v1:`. Dùng **TSD** (`nxl-guest-chat:v1:`) theo chỉ
  đạo của chủ dự án. Khóa HMAC truyền qua `gateway.Deps.GuestSecret` (theo TSD §4.4), không phải tham số mới của
  `gateway.New` như WP §2 viết.
- `Restriction.guest` tính bằng `users.id IS NULL AND target_guest_name IS NOT NULL` (TSD: `target_guest_name IS NOT NULL`),
  tương đương trong thực tế, chặt hơn nếu sau này một key trùng tài khoản.
- **Mocks**: chat-svc không có `//go:generate mockgen` (test dùng fake viết tay). `go generate ./...` trong `services/chat`
  không sinh gì; đã thêm `GuestName` vào `fakeMod` của test moderation.

Không có thay đổi hợp đồng wire so với TSD: frame `send.data.guestName`, `Author` khách
`{id: guestKey, username: "", displayName, color, badges: ["GUEST"]}`, `Mode.guestChat`, REST `guestChat` / `guest`,
`ChatModes.guestChat`, mã lỗi và `details` đúng bảng TSD §6.

## 3. Issues encountered

- `make nxl-swag` trong Git Bash hỏng do MSYS đổi đường dẫn `-w /src` → chạy `MSYS_NO_PATHCONV=1 make nxl-swag`.
- Swag (golang:1.27 trong Docker) xếp lại khối `security` trong `swagger.json`/`docs.go` của **mọi** service (identity,
  channel, notification cũng đổi dù không sửa gì). Diff của identity/channel/notification đã revert (ngoài phạm vi). Diff
  `services/chat/docs/{docs.go,swagger.json}` vì vậy có thêm phần dời chỗ `security` không liên quan; `swagger.yaml` (có sắp xếp)
  chỉ khác đúng các trường của task (`guestChat`, `guest`, mô tả `userId`, mô tả `PUT chat-settings` / 403).
- Fake bucket của test không tự nạp lại token theo thời gian: case slow-mode khách nâng `GuestRateLimitIP` để tin bị từ chối vì
  slow không làm cạn bucket IP.

## 4. Validation

Chạy trong worktree `nexuslive-be-guest-chat`, Windows, không có gcc (nên `-race` tắt). DB test riêng **`nxl_chat_guest_test`**
(tên kết thúc `_test`; không dùng `nxl_chat_test` để tránh đụng phiên overlay có thể migrate DB test cùng tên với bộ migration
khác). Không chạy migration nào lên `nxl_chat`.

| Command | Result |
| --- | --- |
| `docker exec db psql … CREATE DATABASE nxl_chat_guest_test` | ok |
| `migrate -path services/chat/migrations -database …/nxl_chat_guest_test up` | pass (6 migration, version `20261128100001`) |
| `migrate … down 1` → `up` (lần 1, sau repo test có dữ liệu khách) | pass; sau `down` không còn cột `guest_chat`/`guest_name`/`target_guest_name` |
| `migrate … down 1` → `up` (lần 2, có 1 tin khách đang hiện + ghim, 1 tin tài khoản) | pass: tin khách `deleted_at` set, `pinned=false`; tin tài khoản giữ nguyên; `up` lại ok |
| `make format` | pass (không đổi thêm file) |
| `make nxl-lint` | pass (0 issues ở cả 9 module) |
| `NXL_TEST_PG_URL_CHAT=…/nxl_chat_guest_test make nxl-test NXL_TEST_FLAGS=` | pass mọi module, không `FAIL` (chat: usecase/chat 85.9 %, repo/persistent 80.7 % có chạy repo test thật, entity 79.6 %, ws 83.1 %) |
| `go -C services/chat test -count=1 ./internal/repo/persistent/` với DB test, sau `up → down → up` | pass |
| `go -C services/chat generate ./...` | không có directive, không đổi gì |
| `MSYS_NO_PATHCONV=1 make nxl-swag` | ok; chỉ giữ diff của chat-svc (xem §3) |
| `make nxl-up-all` + E2E qua edge | **chưa chạy** — Docker stack đang chạy từ thư mục chính cho phiên khác; chạy compose từ worktree sẽ thay container đang dùng |

| Existing flow re-tested | Transport | Result |
| --- | --- | --- |
| Gửi tin tài khoản: mọi rule (kill switch, body, room, ban/timeout, email, followers/subs, slow, duplicate, rate) | unit (use case) | pass, không sửa test cũ |
| Snapshot khách không có key / kênh tắt guest chat | unit | pass, hình dạng như cũ |
| `PUT chat-settings` không có `guestChat` | unit REST | pass, giữ giá trị hiện tại |
| Timeout/Ban tài khoản, uuid lạ → 404, quyền mod | unit moderation | pass |
| SendContext / History / Restrict / Lift / Pin / Evidence / partitions | repo test (Postgres thật) | pass |
| Luồng qua edge (đăng nhập, WS, mod, báo cáo, viewer count, gift) | REST/WS qua edge | **chưa chạy** (lý do như trên) |

Not tested (and why):

- Toàn bộ checklist end-to-end của WP §8 qua `http://localhost:8080`:
  - khách tắt/bật, slow, link, `TAKEN`;
  - mod timeout/untimeout/ban/unban khách, `GET restrictions`;
  - followers/subs-only; moderator 403;
  - hostile paths (Redis down, token hết hạn);
  - `pnpm e2e` (`m3`, `m4-moderator`, `m4-creator`, `takedown`, `gift`).
  Lý do: không được dựng stack từ worktree; sẽ chạy sau khi merge / khi stack rảnh. Phía web chưa tích hợp.
- `-race`: máy không có gcc (cgo); CI chạy `-race`.
- `make deps-audit`: không đổi dependency.

## 5. Follow-ups

- **Khi merge với `feat/stream-overlay`**: kiểm thứ tự migration chat-svc (cả hai nhánh có thể thêm migration ngày 2026-11-28).
- Chạy checklist E2E WP §8 trên stack đầy đủ + `pnpm e2e` khi web xong (Working Plan web cùng basename).
- **Tài liệu nghiệp vụ cần cập nhật** (repo `../docs`, không sửa trong task này):
  - `ba/specs/NXL-B-live-chat.md`: BR01 (khách chat khi kênh bật), AF10, §9B, ma trận quyền §15/§16.
  - `ba/specs/NXL-00-platform-foundation.md`:
    - §10.1 `auth` (`anonId` → `guestKey`);
    - §10.2 `send.data.guestName`;
    - §10.3 snapshot `me` khách và `mode.guestChat`;
    - §12 hằng số `CHAT_GUEST_*`.
  - `ba/specs/NXL-F-moderation-and-safety.md`: ma trận quyền (Mod timeout/ban khách theo `guestKey`).
  - `ba/analysis/decision-log.md`: DL mới cho các quyết định đã chốt:
    - `guestKey` HMAC;
    - biệt danh theo từng tin;
    - chỉ owner/admin đổi `guestChat`;
    - không ban theo IP;
    - biệt danh ở admin để sau.
- Ban khách theo IP (Open question 1 — đã chốt không làm).
- `guestName` trong evidence nội bộ, nhãn báo cáo safety-svc và admin web (Open question 2).
- Giới hạn tần suất đổi biệt danh; khách @mention; giới hạn khách cấu hình theo kênh.
- `updateSettings` đọc settings lỗi thì nên trả lỗi thay vì coi trường bỏ trống là `false` (áp dụng cho cả `blockLinks` và
  `guestChat`; với `guestChat`, `false` là giá trị an toàn).
- Xem lại phiên bản swag / thứ tự khối `security` để `make nxl-swag && git diff --exit-code` sạch trên mọi service.
