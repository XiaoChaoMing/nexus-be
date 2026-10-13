# Working Plan — Làm lại giao diện web theo design Obsidian Pulse

| Track | Trạng thái | Ngày |
| --- | --- | --- |
| Standard (chỉ `nexuslive-web`; không đổi API) | Approved — đang làm R1 | 2026-10-12 |

## 1. Task Brief

- **Mục tiêu:** đưa toàn bộ giao diện hiện có về design Obsidian Pulse (thiết kế trên pen.dev + mẫu Stitch `stitch_document_design_interface`), dựng trên shadcn/ui đã custom, để mọi màn hình sau này dùng chung một bộ component.
- **Nguồn thiết kế:**
  - Link pen.dev (`app.pen.dev/s/Dj-bf…`) chỉ mở được trong trình duyệt, Claude không đọc được nội dung.
  - Bản làm việc dùng: `obsidian_pulse/DESIGN.md` + 5 ảnh/HTML Stitch (Home, Browse, Live, Live sắp phát, Creator profile).
  - Nếu file pen.dev khác mẫu Stitch: export `.pen` hoặc ảnh từng frame vào `DATN/design/` để Claude đối chiếu (Q1).
- **Trong phạm vi:**
  - Khung app: top bar, sidebar thu gọn được, bottom nav mobile.
  - Trang chủ, trang xem live, Studio, đăng nhập/đăng ký/tài khoản.
  - Trang VOD: làm thẳng theo design mới khi tới GĐ-L4.
  - Trạng thái trống/lỗi/đang tải, toast cho các thao tác.
- **Ngoài phạm vi** (màn hình có trong design nhưng chưa có backend):
  - Browse/Khám phá và danh mục, Creator profile, Đang theo dõi, Bảng xếp hạng: làm khi có channel-svc (GĐ4).
  - Live "sắp diễn ra" + đếm ngược: cần lịch phát (GĐ4).
  - Chat, viewer count: GĐ3.
  - Đăng ký kênh / donate / Hội viên: GĐ6.
  - Clip: sau đồ án.
- **Nguyên tắc dữ liệu:** chỉ hiển thị dữ liệu thật. Phần design cần dữ liệu chưa có thì **ẩn**, hoặc để khung có nhãn giai đoạn (ví dụ cột chat "Chat — GĐ3"). Không dùng số giả (viewers, followers) trên màn hình thật. Số giả chỉ dùng trong `/dev/ui`.

## 2. Đã làm (bước 0 — nền design system)

| Phần | Kết quả |
| --- | --- |
| Thư viện | shadcn/ui (style new-york, `radix-ui`), `lucide-react`, `sonner`, `tw-animate-css`, font tự host `@fontsource-variable/inter` + `plus-jakarta-sans` |
| Token | `src/styles/tokens.css`: biến shadcn ánh xạ Obsidian Pulse (background `#0F0F10`, surface `#18181B`, card `#202024`, hover `#28282D`, border `#2D2D32`, primary `#00F59B`, cyan `#06B6D4`, live `#EF4444`, success, warning); thang chữ headline/body/label; bo góc 6/8/10/12px. Giữ alias cũ (`bg-deep`, `text-fg`…) cho màn hình chưa làm lại |
| Component đã custom | `button` (primary/secondary/outline/ghost/destructive/live/link, `loading`), `badge` (live có chấm, cyan, tag, overlay…), `input`, `textarea`, `card`, `dialog`, `alert-dialog`, `dropdown-menu`, `popover`, `sheet`, `tabs` (mặc định gạch chân Electric Pulse), `toggle`/`toggle-group` (biến thể `chip` cho bộ lọc danh mục), `avatar` (cỡ `xl`, vòng `live`), `slider` (scrubber), `checkbox`, `switch`, `tooltip`, `skeleton`, `sonner`, `empty`, `kbd`, `spinner`, `scroll-area`, `separator`, `label` |
| Component của app | `TextField` dựng lại trên `Input` + `Label`; `ConfirmDialog` trên `AlertDialog`; `Toaster` gắn vào providers |
| Sửa kèm | Trước đây `accent` = màu thương hiệu; nay `accent` = nền hover theo shadcn → đổi các chỗ dùng sang `primary` trong player, Studio, auth |
| Trang tham chiếu | `/dev/ui` (chỉ bản dev): màu, chữ, nút, badge/chip, form, thẻ stream, tabs, HUD player, menu/tooltip/dialog/toast, skeleton/empty |
| Kiểm tra | `pnpm typecheck`, `pnpm lint` (0 cảnh báo), `pnpm test` 12/12, `pnpm build`; chụp `/dev/ui` và `/login` |

## 3. Các giai đoạn làm lại

| GĐ | Nội dung | Component chính | Dữ liệu |
| --- | --- | --- | --- |
| **R1 — Khung app** | Top bar: logo, nút **Phát trực tiếp** → Studio, avatar + DropdownMenu (Studio, Tài khoản, Đăng xuất). Sidebar 240/64px: Trang chủ, Studio (khi đăng nhập), Cài đặt tài khoản ở đáy. Ô tìm kiếm, Khám phá, Đang theo dõi, Bảng xếp hạng, thông báo chưa tạo (Q4). Bottom nav khi < 768px | Button, Input, Kbd, Avatar, DropdownMenu, Tooltip (sidebar thu gọn), Sheet (menu mobile), Separator | auth store |
| **R2 — Trang chủ** | **Hero** = stream LIVE mới nhất (thumbnail lớn, tag, tiêu đề, nút "Xem ngay"). Lưới "Kênh trực tiếp đề xuất" với StreamCard mới (thumbnail 16:9, badge TRỰC TIẾP, thời gian phát, avatar chữ cái đầu, tiêu đề, `channelId`, tag). "Tiếp tục xem" từ vị trí VOD lưu cục bộ (khi có L4). Banner "Trở thành Creator" → Studio. Trạng thái trống bằng `Empty` | Card, Badge, Avatar, Skeleton, Empty, Button | `GET /v1/streams?status=live` |
| **R3 — Trang xem live** | HUD player theo design: icon lucide, gradient đáy, scrubber Slider, badge TRỰC TIẾP + thời gian, menu chất lượng = DropdownMenu, Tooltip cho nút. Khối thông tin: avatar, `channelId`, tiêu đề, tag; nút Theo dõi/Đăng ký kênh **ẩn** tới GĐ4/GĐ6; nút Chia sẻ. Cột chat 340px giữ khung + ô nhập khóa "Chat có ở GĐ3". Overlay trạng thái dựng lại bằng component mới | Slider, DropdownMenu, Tooltip, Badge, Avatar, Card, Empty | `GET /v1/streams/{id}` |
| **R4 — Studio** | Bố cục 2 cột theo design: preview player, Card trạng thái + 4 ô health, Card stream key (CopyField), form cài đặt (Input, chip tag, Switch 18+), hướng dẫn OBS dạng Accordion/Collapsible; toast khi lưu / tạo key / kết thúc | Card, Input, Toggle chip, Switch, ConfirmDialog, sonner | studio API |
| **R5 — Tài khoản & VOD** | Login/Register/Account theo Card mới (đã gần đúng). Trang VOD (L4) dựng theo design từ đầu | Card, TextField, Tabs | identity, `GET /v1/vods/{id}` |
| **R6 — Dọn dẹp** | Xóa alias token cũ và class `text-fg`/`bg-surface-1`…; responsive 1440 / 1024 / 768 / 390; a11y (focus, aria, contrast); cập nhật selector E2E | — | — |

Thứ tự: R1 → R2 → R3 → R4 → R5 → R6. Có thể dừng cho bạn xem sau R1 và sau R3.

## 4. Câu hỏi cần bạn quyết

| # | Câu hỏi | Đề xuất |
| --- | --- | --- |
| Q1 | Thiết kế trên pen.dev có khác mẫu Stitch không? Claude không mở được link chia sẻ | Export frames (PNG) hoặc file `.pen` vào `DATN/design/`; trong lúc chờ, bám mẫu Stitch |
| Q2 | Design hiển thị **tên + avatar kênh** ở mọi thẻ stream, nhưng API stream chỉ có `channelId` | (a) stream-svc giữ bản sao `display_name` từ sự kiện `user.registered`/`user.updated` (đúng nguyên tắc DB-per-service; cần 1 migration + consumer — backend, track Full); (b) tạm hiện chữ cái đầu, chờ channel-svc GĐ4. Đề xuất **(a)**, nhỏ và không phải làm lại |
| Q3 | Tông nền: DESIGN.md mô tả **xám trung tính** (`#0F0F10`/`#18181B`/`#202024`), ảnh Stitch lại ánh **xanh lục** (`#0c150f`/`#151e17`) | Đang dùng trung tính theo DESIGN.md; đổi sang ánh xanh chỉ cần sửa 5 biến trong `tokens.css` |
| Q4 | Mục sidebar/top bar chưa có trang (Khám phá, Đang theo dõi, Bảng xếp hạng, thông báo, tin nhắn) | **Ẩn** cho tới khi có; phương án khác là hiện xám + tooltip "Sắp có" |

### Quyết định (2026-10-12)

- **Q1:** chưa có export pen.dev → bám mẫu Stitch.
- **Q2:** hiển thị `channelId` thay cho tên kênh; avatar dùng ký tự đầu. Bổ sung tên/avatar kênh sẽ làm sau (GĐ4 hoặc snapshot ở stream-svc).
- **Q3:** giữ tông xám trung tính.
- **Q4:** chưa tạo các mục/trang chưa có backend (kể cả ô tìm kiếm).

## 5. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Đổi giao diện làm hỏng luồng đã chạy (Studio, player, E2E) | Giữ nguyên logic/hook, chỉ thay lớp hiển thị. Sau mỗi GĐ chạy lại `pnpm test` + E2E. Giữ `data-testid` |
| Bundle tăng do Radix + font | Trang player đã tải lười; font tự host chỉ 2 họ, dạng variable |
| Design đầy dữ liệu chưa có → trang trống trơn | Hero + lưới + Empty có hướng dẫn hành động ("Bắt đầu phát") |

## 6. Kiểm tra mỗi giai đoạn

- [ ] `pnpm typecheck` · `pnpm lint` · `pnpm test` · `pnpm build`
- [ ] E2E livestream (Creator → Viewer → Kết thúc) vẫn đạt
- [ ] Ảnh chụp 1440 / 768 / 390 của màn hình vừa làm, đặt cạnh mẫu
- [ ] Bàn phím: Tab đi hết top bar → nội dung; focus thấy rõ; Esc đóng menu/dialog

## 7. Tiêu chí hoàn thành

- Given người dùng mở bất kỳ màn hình trong phạm vi, Then màu, chữ, bo góc, viền theo token Obsidian Pulse, không còn class alias cũ.
- Given màn hình hẹp 390px, Then không cuộn ngang, sidebar thành bottom nav, chat xếp dưới player.
- Given dữ liệu chưa có (viewers, chat, follow), Then phần đó ẩn hoặc có nhãn giai đoạn, không có số giả.

## 8. Ước lượng

R1: 0,5 ngày · R2: 1 ngày · R3: 1 ngày · R4: 0,5 ngày · R5: 0,5 ngày · R6: 0,5 ngày. 
