# Working Plan — Hoàn thiện bộ spec NexusLive còn thiếu

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Standard (tài liệu, không đụng code) | Đã chốt quyết định §8 · **Đợt 1 xong 13/10** (NXL-00 v1.1, NXL-D v1.1, errata v1.2, ma trận v1.1) — đợt 2 (NXL-G1, NXL-F) theo roadmap §4 | 2026-10-10 |

## 1. Task Brief

- **Mục tiêu:** mọi yêu cầu **P0** trong PRD v2.0 đều thuộc đúng **một** spec chi tiết, viết theo cùng khuôn với NXL-A/B/C. Sau bước này có thể viết Working Plan/TSD cho backend Go mà không phải đoán yêu cầu.
- **Đang có:** PRD v2.0, Core Features & Roadmap, NXL-A (Player & Stream Page), NXL-B (Live Chat), NXL-C (Discovery & Navigation).
- **Trong phạm vi:**
  - Spec nền tảng chung (NXL-00).
  - Spec cho các nhóm D, E, F, G, H ở mức P0.
  - Bản errata PRD và đồng bộ A/B/C.
  - Ma trận truy vết FR → spec.
- **Ngoài phạm vi:**
  - Các tính năng P1/P2. Chỉ ghi vào mục Out of Scope của từng spec, không đặc tả.
  - Thiết kế Figma, OpenAPI chi tiết, code.
  - Nhóm I (nền tảng & vận hành). Nhóm này là yêu cầu xuyên suốt, không phải feature: I1, I2 được ghi trong từng spec (Responsive, Trợ năng); I3, I4 thuộc TSD hạ tầng khi bắt đầu code.
- **Giả định:**
  - Khuôn spec giữ đúng 19 mục và phần "AI Implementation Rules" như NXL-A/B/C.
  - Nhãn `[đề xuất — Qn]` dùng khi chưa có quyết định, kèm giá trị cấu hình mặc định.
  - Viết bằng tiếng Việt, phiên bản v1.0, trạng thái Draft.
- **Câu hỏi mở:** xem §8.

## 2. Hiện trạng và khoảng trống

Các yêu cầu P0 trong PRD chưa có spec chi tiết:

| Nhóm | Yêu cầu P0 chưa có spec | Đang chặn |
| --- | --- | --- |
| D — Tài khoản & Creator | FR-AU-01, 02, 04, 06 · FR-PF-01, 02, 03, 06, 07 · Settings P0 (§4.16) | Mọi hành động cần đăng nhập trong A/B/C (Follow, Chat, Continue Watching) |
| G — Creator Studio | Go Live / Stream Manager, Dashboard, Content, Schedule, Community (§4.15) | **NXL-A không demo được**: chưa có cách tạo stream `LIVE` |
| F — Kiểm duyệt | FR-MD-01, 03, 05, 07, 08, 10 | NXL-B đã phát sự kiện `delete`, `user_cleared`, `pin` nhưng chưa có spec cho hành động sinh ra chúng |
| E — Thông báo | FR-NT-01, 02, 03, 05 | Thông báo "Creator đang phát", mention (NXL-B BR19 đẩy sang đây) |
| H — Thu nhập | FR-MN-01, 02, 07, 09 · Payout (§4.15) | Badge Sub và emote theo tier trong NXL-B; Subs-only mode |

Khoảng trống xuyên suốt, chưa tài liệu nào sở hữu:

- **Quy ước API và mã lỗi:**
  - PRD §8.4 ghi `{code, message, details}`, còn A/B/C dùng `{success, data, error}`.
  - Mã lỗi lẫn chữ thường và chữ hoa (`rate_limited` cạnh `INVALID_REQUEST`).
- **Gateway realtime:**
  - NXL-A dùng kênh `stream:{id}` (WebSocket/SSE), NXL-B dùng `wss://.../v1/chat/ws`.
  - PRD §8.5 lại ghi `/chat`.
- **Danh mục entity, ERD và bảng hằng số cấu hình:** `INGEST_GRACE_PERIOD`, `CHAT_RATE_LIMIT`… đang rải rác ở từng spec.

## 3. Danh sách tài liệu sẽ viết

| Mã | Tên | Bao phủ | Phụ thuộc quyết định |
| --- | --- | --- | --- |
| **NXL-00** | Nền tảng chung: quy ước API, mã lỗi, phân trang, idempotency, realtime gateway, entity/ERD, bảng cấu hình, RBAC | PRD §8.2–§8.5, §6 (trạng thái hệ thống 404/500/bảo trì) | — |
| **NXL-D** | Tài khoản, Danh tính & Kênh: đăng ký/đăng nhập, Guest, phiên, đăng ký Creator, Creator Profile, Follow + chuông, xem Schedule + Set Reminder, Settings P0 | D1–D4 · FR-AU · FR-PF · §4.16 | Q3 (tuổi, KYC) |
| **NXL-G1** | Go Live & vòng đời Stream: stream key, ingest webhook, Stream Manager, sức khỏe stream, kết thúc → VOD | G1 · §4.15 (Go Live) · §8.7 | **Q5** (hạ tầng video) |
| **NXL-F** | Kiểm duyệt & An toàn: Delete/Timeout/Ban/Pin, Auto-mod + hàng đợi, Report/Block/Mute, Age gate, Admin tools | F1–F4 · FR-MD | Q4 (bản quyền) |
| **NXL-E** | Thông báo: các loại thông báo P0, Notification center, in-app + email | E1 · FR-NT | — |
| **NXL-G2** | Creator Studio còn lại: Dashboard, Content (VOD), tạo/dời/hủy lịch phát, Community (Mod/VIP, từ khóa chặn) | G2 · §4.15 | — |
| **NXL-H** | Thu nhập: Subscription 3 tier, Checkout, trạng thái Sub, Payout + KYC | H1–H2 · FR-MN | **Q1, Q2, Q3, Q6** |
| **Errata** | PRD v2.1: sửa mâu thuẫn; A/B/C lên v1.1 trỏ về NXL-00 | — | — |
| **Traceability** | Ma trận FR-ID P0 → spec → mục BR/AC | Toàn bộ | — |

### Ranh giới sở hữu (tránh hai spec cùng đặc tả một thứ)

| Hành vi | Spec sở hữu | Spec khác chỉ tham chiếu |
| --- | --- | --- |
| Nút Follow/Subscribe trên Stream Page | Logic: D (Follow), H (Subscribe) | A chỉ đặt UI (A-BR17) |
| Tạo, dời, hủy lịch phát | G2 | D hiển thị Schedule + Set Reminder; A hiển thị Upcoming |
| Chuyển trạng thái Stream (`SCHEDULED → LIVE…`) | G1 (phía Creator/ingest) | A giữ state machine phía người xem; G1 **không được thêm** transition ngoài A-§7A |
| Hành động mod trong chat | F | B chỉ nhận và hiển thị sự kiện |
| Gửi thông báo ngoài chat (mention, live, report) | E | B, D, F, G2 chỉ phát domain event |
| Badge Sub, emote theo tier | H (quyền lợi) | B hiển thị |
| Envelope, mã lỗi, phân trang, WebSocket | NXL-00 | Mọi spec |

## 4. Khuôn của mỗi spec

Giữ nguyên 19 mục như NXL-A/B/C:

1. Header (Feature ID, Priority, Version, Tham chiếu)
2. §1 Tổng quan
3. §2 Pre/Post-conditions
4. §3 Happy Path + Mermaid sequence
5. §4 Luồng phụ (AF)
6. §5 Luồng ngoại lệ (E)
7. §6 Business Rules
8. §7 State Machine
9. §8 Display Fields
10. §9 UI/UX
11. §10 Acceptance Criteria
12. §11 Out of Scope
13. §12 Dependencies
14. §13 API Contract
15. §14 Performance
16. §15 Security & Permission
17. §16 Logging & Audit
18. §17 Rollout
19. §18 Open Questions
20. §19 Changelog
21. AI Implementation Rules

Bổ sung bắt buộc cho các spec mới:

- Header ghi danh sách **FR-ID** mà spec bao phủ, phục vụ ma trận truy vết.
- §13 tuân theo NXL-00: envelope, mã lỗi `UPPER_SNAKE_CASE`, cursor pagination, idempotency-key cho thanh toán.
- §7 liệt kê entity mới và chỉ rõ entity nào **dùng chung** với spec khác, không định nghĩa lại.
- §16 ghi rõ sự kiện nào cần audit (PRD NFR-BE-07: mod, admin, thanh toán, đổi thông tin nhạy cảm).

Các entity chính dự kiến:

| Spec | Entity / state machine |
| --- | --- |
| D | `User` (ACTIVE / LOCKED / DELETED), `Session`, `CreatorApplication` (PENDING → APPROVED / REJECTED), `Follow`, `Reminder` |
| G1 | `StreamKey` (ACTIVE / REVOKED), `IngestSession` |
| F | `ModAction` (Timeout có `expiresAt`), `HeldMessage` (HELD → APPROVED / REJECTED / EXPIRED), `Report` (OPEN → IN_REVIEW → RESOLVED / DISMISSED), `Block` |
| E | `Notification` (UNREAD → READ → DELETED) |
| G2 | `Schedule` (rrule) |
| H | `Subscription` (PENDING → ACTIVE → CANCELLED_PENDING_END → EXPIRED; GIFTED), `CheckoutOrder`, `Transaction`, `PayoutAccount` (KYC: UNVERIFIED → PENDING → VERIFIED / REJECTED), `Payout` |

## 5. Các bước thực hiện

Mỗi tài liệu đi qua 4 bước:

1. Nháp.
2. Bạn review.
3. Sửa và chốt v1.0.
4. Cập nhật ma trận truy vết.

Không viết tài liệu kế tiếp khi tài liệu trước còn câu hỏi chặn chưa trả lời.

| Đợt | Tài liệu | Lý do thứ tự | Kết quả kiểm chứng |
| --- | --- | --- | --- |
| 1 — Nền | NXL-00 → NXL-D | Mọi spec cần quy ước chung và khái niệm user/phiên/quyền | Envelope + bảng mã lỗi thống nhất; A/B/C không còn mâu thuẫn với NXL-00 |
| 2 — Mở khóa demo | NXL-G1 → NXL-F | G1 tạo được stream `LIVE` cho A; F hoàn thiện vòng chat của B | Luồng Creator Go Live → Viewer xem → chat → Mod timeout đi trọn qua các spec |
| 3 — Gắn kết | NXL-E → NXL-G2 | Thông báo và Studio dùng sự kiện từ D, F, G1 | Luồng Schedule → Reminder → Live → Notification khép kín |
| 4 — Doanh thu | NXL-H | Phụ thuộc pháp lý nhiều nhất (Q1–Q3) | Checkout bằng PSP sandbox, Sub mở khóa emote/badge |
| 5 — Đồng bộ | Errata PRD v2.1, A/B/C v1.1, ma trận truy vết | Chỉ làm khi các spec mới đã ổn định | 100% FR P0 có đúng một spec sở hữu |

Errata đã ghi nhận sẵn, sẽ xử lý ở đợt 5:

| # | Mâu thuẫn | Đề xuất |
| --- | --- | --- |
| 1 | WebSocket chat: PRD `/chat`, NXL-B `/v1/chat/ws` | Theo NXL-00 |
| 2 | Mã lỗi chữ thường (PRD §8.5, NXL-B) và chữ hoa | Một kiểu `UPPER_SNAKE_CASE` |
| 3 | Envelope lỗi: PRD `{code,message,details}`, spec `{success,data,error}` | Theo NXL-00 |
| 4 | Lưới 768–1023px: PRD "2–3 cột", NXL-C BR08 "2 cột" | Chốt một giá trị |
| 5 | NXL-C dẫn "PRD §74", "§17, §67" (không tồn tại) | Sửa về mục đúng |
| 6 | PRD §4.7 "Thịnh vượng (trending)" | "Thịnh hành" |
| 7 | PRD "Paused (creator)" và NXL-A `INTERRUPTED` | PRD dùng tên `INTERRUPTED` |

## 6. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Q1–Q7 chưa chốt nên spec phải đoán | Ghi `[đề xuất — Qn]` + giá trị cấu hình mặc định, đưa vào §18; không chặn viết spec trừ H |
| Hai spec cùng định nghĩa một hành vi | Bảng ranh giới sở hữu ở §3 + ma trận truy vết |
| Spec quá lớn so với sức một người làm đồ án | Chỉ đặc tả P0; tách G thành G1/G2 |
| G1 phụ thuộc hạ tầng video chưa chọn | Đặc tả qua giao diện trung lập (webhook `ingest_started`, `ingest_stopped`), không gắn vào một nhà cung cấp |
| Đổi NXL-00 làm A/B/C lệch | Đồng bộ A/B/C ở đợt 5, ghi Changelog v1.1 |

## 7. Checklist kiểm tra mỗi spec

- [ ] Đủ 19 mục + AI Implementation Rules; xóa dòng không áp dụng thay vì ghi N/A
- [ ] Header liệt kê FR-ID bao phủ; mọi FR P0 trong phạm vi có BR hoặc AC tương ứng
- [ ] State machine có bảng transition + định nghĩa trạng thái + Mermaid; không có transition mâu thuẫn với spec khác
- [ ] Mọi mã lỗi ở §5/§13 có trong bảng mã lỗi của NXL-00
- [ ] Mọi hành động có phân quyền đều có dòng trong §15 và kiểm tra ở backend
- [ ] Out of Scope ghi rõ phần P1/P2 bị loại
- [ ] Open Questions có owner và deadline

## 8. Quyết định (đã trả lời 2026-10-10)

1. **Nơi lưu spec:** bản chính ở `DATN/docs/ba/specs/`, ngoài repo (ADR-011, thay cho quyết định ban đầu lưu trong `docs/specs/`); errata chuyển sang `DATN/docs/ba/traceability/errata.md`.
2. **NXL-H:** thanh toán **thật trên sandbox** của một PSP; chi tiết làm rõ sau, chọn PSP trước tuần 12.
3. **NXL-G1:** **tự xây** media server — ingest RTMP, transcode ffmpeg, phát HLS; chi tiết làm rõ trước tuần 4 (ADR-004).

Lịch viết từng spec đã chuyển sang `docs/product/roadmap.md` §4 và thay cho thứ tự đợt ở §5 của plan này.

## 9. Tiêu chí hoàn thành

- [ ] 7 tài liệu (NXL-00, D, G1, F, E, G2, H) ở trạng thái v1.0 đã duyệt
- [ ] PRD v2.1 và NXL-A/B/C v1.1 không còn mâu thuẫn ở bảng errata
- [ ] Ma trận truy vết: 100% FR P0 có đúng một spec sở hữu
- [ ] Bốn hành trình P0 trong PRD §15 (Viewer, Discovery, Creator, Moderator) đi được trọn qua các spec mà không gặp chỗ "chưa đặc tả". Hành trình Thanh toán phụ thuộc NXL-H.
