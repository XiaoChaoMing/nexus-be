# NexusLive — Tổng quan sản phẩm

| | |
| --- | --- |
| **Phiên bản** | 1.0 |
| **Cập nhật** | 2026-10-10 |
| **Nguồn** | `DATN/docs/ba/requirements/NexusLive-PRD-v2.0.md` (tầm nhìn đầy đủ) — tài liệu này là phạm vi **đồ án** |

## 1. Sản phẩm

NexusLive là nền tảng livestream và cộng đồng video tương tác thời gian thực cho Creator, Gamer,
Esports và nội dung văn hóa số tại Việt Nam. Lấy cảm hứng trải nghiệm từ Twitch nhưng không sao chép
giao diện hay nhận diện thương hiệu.

**Ba trụ cột**

1. **Video First** — nội dung phát sóng luôn là tâm điểm.
2. **Community-Driven** — chat mượt, phân cấp vai trò rõ, văn hóa fan.
3. **Creator Ecosystem** — công cụ phát sóng, lịch phát, và nguồn thu (Subscription).

**Nguyên tắc trải nghiệm:** người xem phải cảm thấy đang hiện diện trong buổi phát, không phải đang
duyệt một dashboard.

## 2. Người dùng

| Persona | Nhu cầu chính | Vai trò hệ thống |
| --- | --- | --- |
| **Guest** | Xem thử, đọc chat không cần tài khoản | `guest` |
| **Viewer / Fan** | Khám phá nhanh, xem mượt, chat, follow, đăng ký ủng hộ | `viewer`, `subscriber`, `vip` (theo kênh) |
| **Creator** | Phát sóng bằng OBS, quản lý lịch, cộng đồng và thu nhập | `creator` (chủ kênh) |
| **Moderator** | Kiểm duyệt chat nhanh trong kênh được giao | `moderator` (theo kênh) |
| **Admin nền tảng** | Xử lý báo cáo, gỡ nội dung, khóa kênh/tài khoản | `admin` |

Vai trò `subscriber`, `vip`, `moderator` là **theo từng kênh**, không phải thuộc tính toàn cục của
tài khoản. Ma trận quyền đầy đủ ở PRD §8.3.

## 3. Phạm vi đồ án

Đồ án do **1 người** thực hiện trong **~4,5 tháng** (12/10/2026 → đầu 03/2027). PRD v2.0 được viết
cho đội ~12 người với MVP 20 tuần, nên phạm vi đồ án được cắt theo nguyên tắc:

> **Mọi hành trình chính trong PRD §15 phải chạy được từ đầu đến cuối**, nhưng mỗi bước chỉ làm ở
> mức tối thiểu đủ chứng minh kiến trúc. Chiều sâu (nhiều phương thức đăng nhập, ML auto-mod,
> analytics, độ trễ dưới 1,5s) được dời sang "sau đồ án".

### Trong phạm vi

- Web SPA responsive (desktop đầy đủ, mobile cơ bản), dark theme, tiếng Việt (khóa i18n sẵn cho tiếng Anh).
- Đăng ký/đăng nhập email, Guest mode, đăng ký trở thành Creator.
- **Phát sóng thật:** Creator phát từ OBS qua RTMP → transcode bằng ffmpeg → phát HLS nhiều mức chất lượng; tự lưu VOD.
- Trang xem với đầy đủ trạng thái stream (Upcoming, Live, Interrupted, Ended, Offline, Error, Restricted).
- Chat realtime: badge, emote, mention, Slow / Followers-only / Subs-only, reconnect + resume.
- Khám phá: Home, Browse/Category, Search (có/không dấu), Following, Creator Profile + Schedule.
- Kiểm duyệt: Delete/Timeout/Ban/Pin, chặn từ khóa, Report/Block, age gate, công cụ Admin tối thiểu.
- Thông báo: in-app realtime + email cho "Creator đang phát".
- **Thanh toán thật trên môi trường sandbox** của một cổng thanh toán: Subscription 3 tier, Checkout, webhook.

### Ngoài phạm vi đồ án

App native, TV app, WebRTC/ultra-low latency, Clips, Chat Replay, Poll/Prediction/Hype Train/Raid,
Gift Sub, Donate, Nexus Coins, quảng cáo, Analytics chi tiết cho Creator, 2FA, OTP qua SMS, đăng nhập
Apple, payout thật cho Creator, quy trình bản quyền, light theme.

Danh sách chi tiết từng tính năng và mức cắt giảm: [features.md](features.md).

## 4. Tiêu chí thành công của đồ án

Các KPI kinh doanh trong PRD §1.2 (retention, watch time, chuyển đổi) cần người dùng thật nên **không
dùng làm tiêu chí đồ án**. Đồ án được đánh giá bằng các tiêu chí đo được trong buổi demo:

| # | Tiêu chí | Cách chứng minh |
| --- | --- | --- |
| S1 | Phát sóng end-to-end: OBS → RTMP → ffmpeg → HLS → trình duyệt | Demo trực tiếp, đổi chất lượng 720p/480p/360p |
| S2 | Độ trễ glass-to-glass **≤ 6 giây** (P95) | Đồng hồ trong khung hình OBS so với player |
| S3 | Chat realtime ổn định với **≥ 1.000 kết nối** và **≥ 10.000 tin/phút** trên một phòng | Load test (k6), quay lại kết quả |
| S4 | Độ trễ chat gửi → nhận **P95 < 500ms** ở tải S3 | Số đo từ load test |
| S5 | Các hành trình Viewer, Discovery, Creator, Moderator, Thanh toán chạy trọn | Kịch bản demo theo PRD §15 |
| S6 | Thanh toán sandbox: thành công, thất bại, webhook trùng lặp đều xử lý đúng | Demo + test tự động |
| S7 | Hệ thống chịu được một service lỗi mà không sập toàn bộ (ví dụ notification-svc dừng, xem và chat vẫn chạy) | Demo dừng container |
| S8 | Kiến trúc có tài liệu, test tự động, CI | Repo + báo cáo |

S2 thay cho mục tiêu P95 < 1,5s của PRD: HLS chuẩn tự xây bằng ffmpeg có độ trễ vài giây là đặc tính
của giao thức; LL-HLS/WebRTC để sau đồ án (xem [ADR-004](../architecture/decisions.md#adr-004)).

## 5. Quyết định đã chốt

| # | Quyết định | Nguồn |
| --- | --- | --- |
| D1′ | Độ trễ mục tiêu đồ án ≤ 6s (HLS chuẩn); PRD D1 (< 1,5s LL-HLS) dời sau đồ án | ADR-004 |
| D2 | Chỉ dark theme | PRD |
| D3 | Tiếng Việt trước, i18n từ đầu | PRD (rút gọn: tiếng Anh sau) |
| D4 | Web trước, native app sau | PRD |
| D5 | Không quảng cáo | PRD |
| D6 | Module thông số game là tùy chọn theo category | PRD (ngoài phạm vi đồ án) |
| D7 | Màu Live/Success/Error tách khỏi Brand Accent | PRD |
| D8 | Backend theo **microservices**, viết bằng Go, mỗi service theo khuôn go-clean-template | ADR-001, ADR-002 |
| D9 | Frontend là **React SPA (Vite)**, chấp nhận không có SSR/SEO | ADR-007 |
| D10 | **Tự xây media server**: ingest RTMP, transcode ffmpeg, phát HLS | ADR-004 |
| D11 | Thanh toán **thật trên sandbox** của một cổng thanh toán; cổng cụ thể chốt sau | ADR-009 |
| D12 | PRD, spec và tài liệu BA/báo cáo lưu ở `DATN/docs/`, tách khỏi tài liệu kỹ thuật trong repo; Drive chỉ để chia sẻ | ADR-011 |

## 6. Câu hỏi còn mở

| # | Câu hỏi (PRD §2.2) | Trạng thái với đồ án | Hạn chốt |
| --- | --- | --- | --- |
| Q1 | Cổng thanh toán? | Đã chốt hướng sandbox (D11); **chưa chọn cổng** | Trước tuần 12 |
| Q2 | Tỷ lệ chia sẻ doanh thu | Dùng giá trị cấu hình (đề xuất 70/30), chỉ hiển thị | Trước tuần 16 |
| Q3 | Tuổi tối thiểu, KYC Creator | Rút gọn: khai báo ngày sinh + Admin duyệt thủ công | Trước tuần 3 |
| Q4 | Bản quyền, thời hạn lưu VOD | VOD lưu 14 ngày (cấu hình); quy trình bản quyền ngoài phạm vi | Trước tuần 6 |
| Q5 | Hạ tầng video | Đã chốt hướng tự xây (D10); **chi tiết pipeline làm rõ sau** | Trước tuần 4 |
| Q6 | Nexus Coins ở MVP? | Không (ngoài phạm vi) | Đã chốt |
| Q7 | Leaderboard ở MVP? | Không (ngoài phạm vi) | Đã chốt |

## 7. Thuật ngữ

**VOD** video đã phát lại · **CCU** người xem đồng thời · **ABR** adaptive bitrate (tự đổi chất
lượng theo băng thông) · **HLS** HTTP Live Streaming · **RTMP** giao thức ingest từ OBS ·
**Ingest** luồng video Creator gửi lên · **Rendition** một mức chất lượng sau transcode ·
**Stream key** khóa bí mật để Creator phát vào kênh của mình · **PSP** cổng thanh toán ·
**Webhook** lời gọi từ PSP báo kết quả giao dịch · **Fan-out** phát một tin nhắn tới mọi người
trong phòng chat.
