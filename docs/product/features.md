# NexusLive — Danh mục tính năng

| | |
| --- | --- |
| **Phiên bản** | 1.1 |
| **Cập nhật** | 2026-10-11 |
| **Nguồn** | PRD v2.0 §4, Core Features & Roadmap §1 |

## Cách đọc

- **Ưu tiên PRD:** P0 = MVP sản phẩm · P1 = ngay sau MVP · P2 = về sau (giữ nguyên theo PRD).
- **Đồ án:**
  - ✅ **Làm đủ** — đúng như PRD/spec.
  - ◐ **Rút gọn** — làm phần cốt lõi, phần còn lại ghi ở cột "Ghi chú".
  - ⬜ **Sau đồ án** — không triển khai, chỉ chừa chỗ trong kiến trúc.
- **Service:** service sở hữu logic nghiệp vụ (xem [system-architecture.md](../architecture/system-architecture.md) §4).
- **Spec:** tài liệu đặc tả chi tiết; "—" là chưa có.
- **Giai đoạn:** GĐ0–GĐ8 trong [roadmap.md](roadmap.md).

## A. Xem & phát video

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A1 | Video Player: play/pause, âm lượng, chất lượng, Theater, Fullscreen, phím tắt | P0 | ◐ | web | NXL-A | GĐ2 | Chất lượng: Auto, 720p, 480p, 360p (1080p nếu nguồn đủ); bỏ 1440p |
| A2 | Trang xem Live: thông tin kênh, tiêu đề, category, viewers, nút Follow/Subscribe/Share | P0 | ✅ | stream, web | NXL-A | GĐ2 | Nút Follow/Subscribe chỉ là UI ở GĐ2, logic nối ở GĐ4/GĐ6 |
| A3 | Trạng thái stream: Upcoming, Live, Buffering, Reconnecting, Interrupted, Offline, Ended, Error, Restricted | P0 | ✅ | stream, web | NXL-A | GĐ2 | |
| A4 | Độ trễ thấp | P0 | ◐ | media | NXL-A, NXL-G1 | GĐ2 | HLS chuẩn, mục tiêu ≤ 6s thay vì < 1,5s (ADR-004) |
| A5 | VOD: xem lại, resume vị trí, thumbnail preview | P0 | ◐ | media, stream | NXL-A | GĐ2 | Bỏ thumbnail preview khi hover thanh tiến trình |
| A6 | PiP, mini-player, tốc độ phát, phụ đề, chỉ nghe | P1 | ⬜ | web | — | — | PiP của trình duyệt vẫn dùng được |
| A7 | Clips, Chat Replay | P1 | ⬜ | media, chat | — | — | |
| A8 | Ultra-low latency (WebRTC) | P1 | ⬜ | media | — | — | |
| A9 | Tua lại khi đang xem live (DVR) — toàn bộ buổi phát, tạm dừng, "Về trực tiếp" | — (bổ sung) | ✅ | media, web | NXL-A v1.1 | GĐ2 | Quyết định DL-10; thay NXL-A BR05 (errata E-10); không tốn thêm hạ tầng vì playlist EVENT giữ mọi segment |

## B. Cộng đồng & Chat

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| B1 | Live chat realtime, badge vai trò, màu username, tin ghim, system message | P0 | ✅ | chat | NXL-B | GĐ3 | |
| B2 | Emote toàn cầu + theo kênh (khóa theo Sub tier), @mention | P0 | ✅ | chat | NXL-B | GĐ3 | Emote khóa theo tier mở ở GĐ6 |
| B3 | Slow, Followers-only, Subs-only | P0 | ✅ | chat | NXL-B | GĐ3 | Subs-only có hiệu lực thật từ GĐ6 |
| B4 | Virtualized list, reconnect + resume, sampling, optimistic UI | P0 | ✅ | chat, web | NXL-B | GĐ3 | Là nội dung chính của tiêu chí S3, S4 |
| B5 | Reply, cài đặt chat cá nhân, chat rules | P1 | ⬜ | chat | — | — | |
| B6 | Poll, Prediction, Hype Train, Raid | P2 | ⬜ | chat | — | — | |

## C. Khám phá & Điều hướng

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| C1 | Global Shell: top bar, sidebar followed live/offline, bottom nav mobile | P0 | ✅ | web, channel | NXL-C | GĐ1 (khung), GĐ4 (dữ liệu) | |
| C2 | Home: Hero, Continue Watching, Followed Live, Live đề xuất, Categories… | P0 | ◐ | channel, stream | NXL-C | GĐ4 | Hero chọn theo viewers cao nhất; bỏ "VOD đề xuất" |
| C3 | Browse & Category: lọc, sắp xếp, infinite scroll | P0 | ✅ | channel, stream | NXL-C | GĐ4 | "Trending" = viewers tăng trong 30 phút (cấu hình) |
| C4 | Search (Ctrl+K): Kênh, Streams, Category, VOD; có/không dấu | P0 | ◐ | channel | NXL-C | GĐ4 | PostgreSQL full-text + `unaccent`; bỏ nhóm Tags |
| C5 | Following page (Live trước) | P0 | ✅ | channel | NXL-C | GĐ4 | |
| C6 | Leaderboard | P1 | ⬜ | channel | — | — | Q7 |
| C7 | Gợi ý cá nhân hóa nâng cao | P2 | ⬜ | — | — | — | |

## D. Tài khoản & Creator Identity

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| D1 | Đăng ký/đăng nhập, Guest mode, đăng ký Creator | P0 | ◐ | identity | NXL-D | GĐ1 | Email + mật khẩu + xác minh email; Google OAuth nếu kịp ở GĐ7. Bỏ SMS OTP, Apple. KYC: khai báo + Admin duyệt (Q3) |
| D2 | Creator Profile: banner, bio, tabs Home/Videos/About/Schedule | P0 | ◐ | channel | NXL-D | GĐ4 | Bỏ tab Clips |
| D3 | Follow + chuông thông báo, Schedule + Set Reminder | P0 | ✅ | channel | NXL-D | GĐ4 | Tạo/sửa lịch thuộc G2 của Studio (NXL-G2) |
| D4 | Settings cơ bản | P0 | ◐ | identity, web | NXL-D | GĐ1, GĐ7 | Tài khoản, chất lượng mặc định, thông báo |
| D5 | 2FA, Onboarding, quản lý thiết bị | P1 | ⬜ | identity | — | — | |

## E. Thông báo

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| E1 | Creator live, stream sắp bắt đầu, mention, hệ thống — in-app + email | P0 | ✅ | notification | NXL-E | GĐ5 | Email gửi qua SMTP (Mailpit khi dev) |
| E2 | Notification center + cài đặt chi tiết, push | P1 | ◐ | notification, web | NXL-E | GĐ5 | Làm **center cơ bản** (PRD FR-NT-02 là P0, lệch với Roadmap — xem errata); bỏ push, giờ yên lặng |
| E3 | Messages (Whispers) | P1 | ⬜ | — | — | — | |

## F. Kiểm duyệt & An toàn

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| F1 | Delete, Timeout, Ban, Pin | P0 | ✅ | chat | NXL-F | GĐ5 | Hiển thị phía chat đã có trong NXL-B |
| F2 | Auto-mod, từ khóa chặn, hàng đợi duyệt | P0 | ◐ | chat | NXL-F | GĐ5 | Từ khóa chặn + chặn link/spam; bỏ cấp độ 0–4 và hàng đợi duyệt |
| F3 | Report, Block, Mute; Age gate mature | P0 | ✅ | safety, stream | NXL-F | GĐ5 | Age gate kiểm ở stream-svc khi cấp playback session |
| F4 | Admin tools: xử lý report, khóa kênh/stream | P0 | ◐ | safety | NXL-F | GĐ5 | API đủ; giao diện Admin tối giản |
| F5 | Appeal, mod log, bản quyền | P1 | ⬜ | safety | — | — | Audit log vẫn ghi từ GĐ5 |

## G. Creator Studio

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| G1 | Go Live / Stream Manager: stream key, tiêu đề, category, preview, sức khỏe stream | P0 | ✅ | stream, media | NXL-G1 | GĐ2 | Sức khỏe: bitrate, fps nhận được từ ffmpeg |
| G2 | Dashboard, Content (VOD), Schedule, Community (Mod/VIP) | P0 | ◐ | stream, channel, chat | NXL-G2 | GĐ4, GĐ5 | Dashboard: viewers hiện tại, follower mới; Content: ẩn/xóa VOD |
| G3 | Analytics | P1 | ⬜ | — | — | — | |
| G4 | Alerts/overlay, quản lý emote & badge | P1 | ◐ | chat | — | GĐ6 | Chỉ upload emote kênh cho tier |

## H. Thu nhập

| # | Tính năng | PRD | Đồ án | Service | Spec | Giai đoạn | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| H1 | Subscription 3 tier + Checkout + quản lý/hủy | P0 | ✅ | billing | NXL-H | GĐ6 | Giao dịch thật trên sandbox của PSP; gói tháng, tự gia hạn mô phỏng bằng job |
| H2 | Payout cho Creator (số dư, lịch chi trả, KYC) | P0 | ◐ | billing | NXL-H | GĐ6 | Sổ cái số dư + yêu cầu rút tiền ở trạng thái chờ; không chi trả thật |
| H3 | Gift Sub, Donate, Goals | P1 | ⬜ | billing | — | — | |
| H4 | Lịch sử giao dịch, hóa đơn, hoàn tiền | P1 | ◐ | billing | NXL-H | GĐ6 | Chỉ lịch sử giao dịch |
| H5 | Nexus Coins, quảng cáo | P2 | ⬜ | — | — | — | Q6 |

## I. Nền tảng & Vận hành (xuyên suốt)

Nhóm I không có spec riêng: yêu cầu được ghi vào từng spec (Responsive, Trợ năng) và vào
[system-architecture.md](../architecture/system-architecture.md) (hạ tầng, quan sát).

| # | Tính năng | PRD | Đồ án | Nơi đặt yêu cầu | Ghi chú |
| --- | --- | --- | --- | --- | --- |
| I1 | Responsive desktop / tablet / mobile | P0 | ◐ | Mỗi spec, mục UI/UX | Desktop đầy đủ; mobile cơ bản (bottom nav, chat bottom sheet) |
| I2 | i18n vi/en, WCAG 2.2 AA, dark theme | P0 | ◐ | Mỗi spec | Tiếng Việt đủ, khóa i18n sẵn; WCAG cơ bản (contrast, focus, phím) |
| I3 | CI/CD, IaC, monitoring, alerting, DR | P0 | ◐ | Kiến trúc §11–§12 | GitHub Actions, Docker Compose, OpenTelemetry + Prometheus/Grafana |
| I4 | Analytics events & KPI dashboard | P0 | ◐ | Kiến trúc §11 | Ghi các sự kiện cốt lõi; không có dashboard KPI |
| I5 | Native app, TV app, Public API | P2 | ⬜ | — | |

## Tổng hợp phạm vi đồ án

| Nhóm | ✅ Làm đủ | ◐ Rút gọn | ⬜ Sau đồ án |
| --- | --- | --- | --- |
| A | 3 | 3 | 3 |
| B | 4 | 0 | 2 |
| C | 3 | 2 | 2 |
| D | 1 | 3 | 1 |
| E | 1 | 1 | 1 |
| F | 2 | 2 | 1 |
| G | 1 | 2 | 1 |
| H | 1 | 2 | 2 |
| I | 0 | 4 | 1 |
| **Tổng** | **16** | **19** | **14** |

Mọi tính năng P0 của PRD đều có mặt trong đồ án ở mức ✅ hoặc ◐.

## Hành trình demo (PRD §15)

| Hành trình | Các bước | Tính năng dùng | Sẵn sàng sau giai đoạn |
| --- | --- | --- | --- |
| Creator | Đăng ký Creator → tạo lịch → Go Live từ OBS → chat với fan → kết thúc → VOD | D1, G1, G2, A3, A5, B1 | GĐ4 |
| Viewer | Home → mở stream → xem → chat → Follow → nhận thông báo lần phát sau | C2, A1–A3, B1–B3, D3, E1 | GĐ5 |
| Discovery | Browse → Category → Stream card → Stream → Profile → Follow | C3, C4, A2, D2, D3 | GĐ4 |
| Moderator | Được gán Mod → Slow mode → Timeout/Ban → Report → Admin gỡ stream | B3, F1–F4 | GĐ5 |
| Thanh toán | Subscribe → chọn tier → Checkout sandbox → badge/emote mở khóa → hủy | H1, B2, B3 | GĐ6 |
