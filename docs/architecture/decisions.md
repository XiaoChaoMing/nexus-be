# NexusLive — Quyết định kiến trúc (ADR)

Mỗi quyết định ghi bối cảnh, lựa chọn, lý do và cái giá phải trả. Muốn đổi một quyết định thì thêm
ADR mới với trạng thái *Thay thế ADR-xxx*, không sửa nội dung ADR cũ.

| ADR | Quyết định | Trạng thái |
| --- | --- | --- |
| [ADR-001](#adr-001) | Microservices theo domain, số lượng ít, đưa vào theo giai đoạn | Chấp nhận |
| [ADR-002](#adr-002) | Go + khuôn go-clean-template cho mọi service, trong một monorepo | Chấp nhận |
| [ADR-003](#adr-003) | NATS JetStream cho sự kiện và fan-out chat; gRPC cho lời gọi đồng bộ nội bộ | Chấp nhận |
| [ADR-004](#adr-004) | Tự xây media server: RTMP ingest, transcode ffmpeg, phát HLS | Chấp nhận — chi tiết ở ADR-012 |
| [ADR-005](#adr-005) | PostgreSQL database-per-service, Redis cho trạng thái nóng, MinIO cho media | Chấp nhận |
| [ADR-006](#adr-006) | Một realtime gateway WebSocket đặt trong chat-svc | Chấp nhận |
| [ADR-007](#adr-007) | Frontend React SPA (Vite), không SSR | Chấp nhận |
| [ADR-008](#adr-008) | JWT ký bất đối xứng, xác minh cục bộ qua JWKS | Chấp nhận |
| [ADR-009](#adr-009) | Thanh toán thật trên sandbox của một PSP, bọc sau interface | Chấp nhận — chưa chọn PSP |
| [ADR-010](#adr-010) | Tài liệu và spec lưu trong repo | Thay thế bởi ADR-011 |
| [ADR-011](#adr-011) | Tài liệu nghiệp vụ và báo cáo lưu ở `DATN/docs/`, tách khỏi repo | Chấp nhận |
| [ADR-012](#adr-012) | Chi tiết media pipeline và giới hạn transcode đồng thời (kết quả PoC GĐ0) | Chấp nhận |
| [ADR-013](#adr-013) | Ứng dụng quản trị tách khỏi web người dùng (`nexuslive-admin`) | Chấp nhận |

---

## ADR-001

**Microservices theo domain, số lượng ít, đưa vào theo giai đoạn**

- **Bối cảnh:** chủ đồ án chọn microservices. Hệ thống có các phần với đặc tính tải rất khác nhau
  (media dùng CPU, chat giữ hàng nghìn kết nối, billing cần đúng tuyệt đối). Nhân lực là 1 người trong ~4,5 tháng.
- **Quyết định:**
  - 8 service theo domain: identity, channel, stream, media, chat, safety, notification, billing.
  - Không chia nhỏ hơn: ví dụ không tách discovery/search khỏi channel, không tách moderation-in-chat khỏi chat.
  - Service chỉ được dựng khi giai đoạn của nó bắt đầu (identity ở GĐ1, billing ở GĐ6…).
- **Lý do:**
  - Tách được phần nặng CPU (media) và phần giữ kết nối (chat) để co giãn riêng.
  - Một service lỗi không kéo sập hệ thống (tiêu chí S7).
  - Ranh giới domain rõ ràng là nội dung bảo vệ được của đồ án.
- **Cái giá:**
  - Chi phí vận hành và debug phân tán cao hơn nhiều so với một monolith.
  - Dữ liệu chéo phải đồng bộ bằng sự kiện (eventual consistency).
  - Viết test tích hợp khó hơn.
- **Giảm thiểu:**
  - Dùng chung `pkg/` và một khuôn code cho mọi service.
  - Chạy toàn bộ bằng một lệnh compose; tracing xuyên service từ ngày đầu.
  - Nếu tới cuối GĐ3 chậm hơn 2 tuần so với lộ trình, gộp safety và notification vào cùng một tiến trình (vẫn giữ ranh giới package).
- **Phương án đã cân nhắc:** modular monolith — ít rủi ro tiến độ hơn, nhưng không được chọn.

## ADR-002

**Go + khuôn go-clean-template cho mọi service, trong một monorepo**

- **Quyết định:** mỗi service là một module Go theo cấu trúc entity → usecase → repo → controller của go-clean-template, đặt trong `services/<name>/`. Phần hạ tầng dùng chung (logger, JWT, NATS + outbox, postgres, tracing, server) nằm ở `pkg/`. Dùng `go.work` để phát triển nhiều module cùng lúc.
- **Lý do:** giữ quy ước, Makefile, linter và test đã có; agent và người đọc chỉ cần học một khuôn; monorepo giúp một người đổi hợp đồng và mọi nơi dùng nó trong một commit.
- **Cái giá:** template hiện có 4 transport (REST, gRPC, AMQP, NATS RPC) — mỗi service chỉ giữ transport nó cần; AMQP RPC bị bỏ. `AGENTS.md` phải cập nhật cho bố cục nhiều service.

## ADR-003

**NATS JetStream cho sự kiện và fan-out chat; gRPC cho lời gọi đồng bộ nội bộ**

- **Quyết định:**
  - Sự kiện miền đi qua NATS JetStream (bền, có replay).
  - Fan-out chat giữa các instance chat-svc dùng NATS core (nhanh, không cần bền vì tin nhắn đã lưu PostgreSQL).
  - Lời gọi đồng bộ hiếm hoi dùng gRPC.
  - Bỏ RabbitMQ.
- **Lý do:**
  - Một broker cho cả hai nhu cầu.
  - Template đã có NATS.
  - Độ trễ thấp phù hợp chat (S4).
- **Cái giá:**
  - JetStream cần cấu hình stream/consumer cẩn thận.
  - Bắt buộc dùng outbox để không mất sự kiện khi DB commit nhưng publish lỗi.

## ADR-004

**Tự xây media server: RTMP ingest, transcode ffmpeg, phát HLS**

- **Bối cảnh:** PRD đặt câu hỏi Q5 (thuê dịch vụ hay tự xây). Chủ đồ án chọn tự xây để làm chủ toàn bộ pipeline.
- **Quyết định:**
  - media-svc (Go) nhận RTMP và xác thực stream key qua stream-svc.
  - Mỗi stream chạy một tiến trình ffmpeg tạo HLS nhiều rendition.
  - Nginx phục vụ segment với URL có chữ ký.
  - Chi tiết (thư viện RTMP, nơi ghi segment, số rendition, GPU) làm rõ trong NXL-G1.
- **Hệ quả:**
  - Độ trễ HLS chuẩn khoảng 4–8 giây, nên mục tiêu đồ án là **≤ 6s** thay cho P95 < 1,5s của PRD.
  - LL-HLS và WebRTC để sau đồ án.
  - Số stream đồng thời bị giới hạn bởi CPU máy demo.
- **Lý do:** đây là phần kỹ thuật có giá trị nhất của đồ án; không phụ thuộc chi phí dịch vụ bên ngoài.
- **Cái giá:** rủi ro kỹ thuật lớn nhất của dự án nên phải làm PoC ngay GĐ0.

## ADR-005

**PostgreSQL database-per-service, Redis cho trạng thái nóng, MinIO cho media**

- **Quyết định:**
  - Mỗi service có database riêng, khi dev/demo nằm chung một instance PostgreSQL.
  - Redis cho rate limit, presence, viewer count, bản sao quyền chat.
  - MinIO (S3-compatible) cho segment, VOD, ảnh.
- **Lý do:**
  - Giữ đúng nguyên tắc sở hữu dữ liệu của microservices mà không phải vận hành nhiều instance.
  - MinIO cho phép chuyển sang S3 thật mà không đổi code.
- **Cái giá:**
  - Không có join và transaction xuyên service.
  - Báo cáo tổng hợp phải dựng từ sự kiện.

## ADR-006

**Một realtime gateway WebSocket đặt trong chat-svc**

- **Bối cảnh:** NXL-A dùng kênh `stream:{id}` (WebSocket/SSE), NXL-B dùng `/v1/chat/ws`, PRD §8.5 dùng `/chat`.
- **Quyết định:**
  - Trình duyệt mở **một** kết nối `/v1/ws` tới chat-svc và subscribe các chủ đề `chat:`, `stream:`, `user:`, `sidebar:`.
  - Các service khác phát sự kiện lên NATS, chat-svc chuyển tiếp.
- **Lý do:**
  - Một kết nối cho mỗi tab.
  - Một chỗ xử lý xác thực, reconnect, resume.
  - Tránh dựng gateway thứ hai.
- **Cái giá:**
  - chat-svc gánh thêm vai trò gateway.
  - Nếu tách gateway sau này, giao thức chủ đề giữ nguyên nên client không đổi.
- **Việc kéo theo:** NXL-00 chốt giao thức; NXL-A, NXL-B cập nhật v1.1.

## ADR-007

**Frontend React SPA (Vite), không SSR**

- **Quyết định:** React + Vite + TypeScript, TanStack Query cho dữ liệu server, store riêng cho player và chat, hls.js cho phát video, i18n từ đầu.
- **Lý do:** đơn giản để một người làm; build và dev nhanh; không cần server Node riêng.
- **Cái giá:** mất SEO cho trang kênh/category/VOD mà PRD §7.1 mong muốn — chấp nhận vì không phải tiêu chí đồ án. Có thể thêm prerender cho vài trang công khai sau.

## ADR-008

**JWT ký bất đối xứng, xác minh cục bộ qua JWKS**

- **Quyết định:** identity-svc ký access token bằng EdDSA (15 phút) và công bố khóa công khai qua JWKS; các service xác minh cục bộ. Refresh token xoay vòng, lưu bản băm, thu hồi được.
- **Lý do:** không service nào phải gọi identity-svc cho mỗi request; khóa riêng chỉ nằm ở identity-svc.
- **Cái giá:** access token không thu hồi tức thì — chấp nhận độ trễ tối đa 15 phút; khóa tài khoản gấp được đẩy qua sự kiện `user.locked` để chat-svc ngắt kết nối ngay.

## ADR-009

**Thanh toán thật trên sandbox của một PSP, bọc sau interface**

- **Quyết định:**
  - billing-svc tích hợp **một** cổng thanh toán ở môi trường sandbox qua interface `PaymentProvider` (tạo giao dịch, xác minh webhook, truy vấn trạng thái).
  - Có thêm một provider giả lập cho test tự động.
  - Cổng cụ thể chọn trước GĐ6.
- **Lý do:** chứng minh luồng thanh toán thật (redirect, webhook, idempotency, đối soát) mà không chịu rủi ro tiền thật.
- **Cái giá:** mỗi PSP có quy trình đăng ký sandbox và định dạng webhook riêng — đó là lý do phải chốt sớm (Q1, hạn tuần 12).

## ADR-010

**Tài liệu và spec lưu trong repo**

- **Trạng thái:** thay thế bởi [ADR-011](#adr-011).

- **Quyết định:**
  - Bản chính của PRD, spec NXL-*, tài liệu kiến trúc và kế hoạch nằm trong `docs/`.
  - Drive chỉ để chia sẻ.
- **Lý do:** tài liệu đi cùng phiên bản code; agent đọc được trực tiếp; thay đổi được review cùng commit.

## ADR-011

**Tài liệu nghiệp vụ và báo cáo lưu ở `DATN/docs/`, tách khỏi repo** — thay thế ADR-010

- **Bối cảnh:** tài liệu dành cho BA, người hướng dẫn và hội đồng (PRD, spec, truy vết, báo cáo) có người đọc khác với tài liệu kỹ thuật dành cho lập trình và agent.
- **Quyết định:**
  - `DATN/docs/` chứa PRD, spec NXL-*, phân tích, truy vết, errata, nhật ký quyết định và báo cáo đồ án.
  - `nexuslive-be/docs/` chỉ chứa tài liệu kỹ thuật: kiến trúc, ADR, mô hình dữ liệu, tổng quan tính năng, lộ trình, Working/Completed Plan.
  - Repo dẫn chiếu sang spec bằng đường dẫn `../../docs/ba/specs/`, không chép nội dung.
  - Drive chỉ để chia sẻ.
- **Cái giá:**
  - Spec không còn đi cùng phiên bản code trong git của repo.
  - Agent làm việc trong repo phải được trỏ tới thư mục ngoài repo khi cần đọc spec.

## ADR-012

**Chi tiết media pipeline và giới hạn transcode đồng thời** — kết quả PoC GĐ0, trả lời Q5 (mốc quyết định "cuối tuần 2")

- **Bối cảnh:** ADR-004 để ngỏ thư viện RTMP, nơi ghi segment, số rendition và số stream đồng thời. Pipeline
  đã chạy thật từ L1–L4; GĐ0 WP4 đo tải bằng `scripts/bench-transcode.sh`.
- **Quyết định:**
  - Ingest RTMP bằng thư viện Go `yutopp/go-rtmp` trong media-svc; chỉ nhận H.264 + AAC.
  - Mỗi stream một tiến trình ffmpeg (x264 `veryfast`), ladder **720p / 480p / 360p** (bỏ bậc cao hơn nguồn),
    segment **2 s**, playlist HLS kiểu EVENT (DVR khi live và dùng làm VOD).
  - Segment ghi đĩa tạm rồi upload lên **MinIO** (bucket `nxl-media`); trình duyệt đọc qua cổng vào Nginx
    `/hls/*`. Không phục vụ segment từ đĩa cục bộ của media-svc.
  - `MEDIA_MAX_CONCURRENT_STREAMS` mặc định **2**. Môi trường khác chọn theo quy tắc:
    số luồng lớn nhất mà độ trễ HLS phía server **không tăng dần** (lag trend < 0,5 s/phút, `speed` cuối vòng
    ≥ 0,97) **và** CPU của media-svc ≤ 75% tổng số core, đo bằng `make nxl-bench` trên chính máy đó.
- **Số liệu (13/10/2026, máy của Claude: 2 vCPU Xeon 2.1 GHz, 7 GB RAM, media-svc chạy trực tiếp, publisher
  ffmpeg chạy cùng máy ~16% core mỗi luồng):**

  | Nguồn | Luồng | CPU media (core) | `speed` min | Độ trễ HLS phía server (trung vị) |
  | --- | --- | --- | --- | --- |
  | 720p 4,5 Mbps | 1 | 0,66 | 1,01 | 0,26 s |
  | 720p | 2 | 1,37 | 0,93 | 5,1 s (tăng dần) |
  | 720p | 3 | 1,42 (máy bão hòa) | 0,64 | 27 s (tăng dần) |
  | 1080p 6 Mbps | 1 | 0,87 | 1,02 | 0,29 s |
  | 1080p | 2 | 1,24 (máy bão hòa) | 0,63 | 17 s (tăng dần) |

  Chi phí ≈ **0,7 core / luồng nguồn 720p** và **0,9 core / luồng nguồn 1080p** trên CPU loại này. Máy 2 vCPU
  chỉ chạy ổn 1 luồng; VPS demo 4 vCPU (kiến trúc §12) ước tính 3 luồng 1080p — xác nhận bằng `make nxl-bench`.
- **Hệ quả:**
  - Độ trễ phía server < 0,6 s khi máy theo kịp; độ trễ người xem chủ yếu do bộ đệm player (~4 s) → ước tính
    glass-to-glass 4,5–6 s, đạt S2 (≤ 6 s) khi chưa quá tải. Số chính thức đo bằng OBS (`scripts/measure-latency.md` §2).
  - Quá tải không làm lỗi ngay mà làm độ trễ tăng dần → giới hạn cứng ở media-svc (từ chối publish, metric
    `media_publish_rejected_total{reason="capacity"}`) tốt hơn cho phép rồi chậm.
  - Grafana theo dõi `media_ffmpeg_speed`; < 0,9 kéo dài là dấu hiệu phải giảm giới hạn.
- **Phương án nếu thiếu CPU khi demo:** preset `superfast`, bỏ bậc 480p, hoặc giảm nguồn OBS về 720p.

## ADR-013

**Ứng dụng quản trị tách khỏi web người dùng** — quyết định 13/10/2026 (Working Plan GĐ1 v1.1, DL-15)

- **Bối cảnh:**
  - GĐ1 cần màn duyệt đơn Creator; GĐ5 thêm report, khóa tài khoản/kênh, gỡ stream, audit log (NXL-F).
  - Bản đầu của NXL-D §9 đặt màn Admin ở `/admin/*` trong `nexuslive-web`.
  - Người dùng và cách dùng khác hẳn web chính: bảng dữ liệu, thao tác hàng loạt, desktop-first; không cần
    player, Shell, SEO.
- **Quyết định:**
  - Project React riêng **`DATN/nexuslive-admin`**, cạnh `nexuslive-web`; dev `http://localhost:5174`, gọi cùng
    API qua cổng vào.
  - Stack giống web chính (React, Vite, TypeScript, Tailwind, shadcn/radix, TanStack Query, react-hook-form + zod,
    react-i18next, Vitest, Playwright); không thêm thư viện mới.
  - GĐ1 **chép** token giao diện, component `ui` cần dùng và lớp `lib/api*` từ `nexuslive-web` (ghi nguồn + ngày
    chép); chưa tách package chung.
  - `nexuslive-web` không chứa route hay code quản trị; tài khoản Admin chỉ thấy liên kết "Trang quản trị".
  - Quyền thật vẫn ở backend: identity kiểm `role = ADMIN` cho `/v1/admin/*`; cổng vào chỉ cho CORS
    `/v1/admin/*` từ `NXL_ADMIN_ORIGIN`; refresh/logout của admin gửi `X-NXL-Client: admin`.
- **Lý do:**
  - Bundle web người dùng không mang code quản trị.
  - Admin deploy và giới hạn truy cập riêng (host `admin.<domain>`, sau này IP allowlist).
  - Công cụ GĐ5 thêm vào mà không làm phình `nexuslive-web`.
- **Cái giá:**
  - Thêm một project FE: CI, dependency, cập nhật phiên bản phải làm hai nơi.
  - Code UI trùng lặp; token giao diện đổi ở web phải chép lại sang admin.
  - Trên `localhost` cookie không phân biệt cổng: đăng nhập/đăng xuất ở hai app ảnh hưởng nhau khi dev (deploy
    dùng host riêng nên tách biệt).
- **Phương án đã cân nhắc:**

  | Phương án | Không chọn vì |
  | --- | --- |
  | Cùng app `nexuslive-web`, route `/admin/*` lazy-load + guard theo vai trò | Code quản trị vẫn nằm trong repo/bundle (chunk tải được), lẫn layout Shell; khó giới hạn truy cập theo host; GĐ5 làm app phình |
  | Monorepo FE (pnpm workspace) với package UI dùng chung | Chi phí dựng workspace, build, CI lớn hơn lợi ích khi code chung còn ít; một người làm |

- **Việc kéo theo:** NXL-D v1.2 §9.1 (màn quản trị), NXL-00 v1.2 §9.2 (CORS hai origin), kiến trúc §3 (container
  Admin web). Xem lại việc tách package chung ở GĐ5 khi code trùng nhiều (G1-Q5).
