# Completed — GĐ2: Đóng Streaming core

| Working Plan | TSD | Ngày |
| --- | --- | --- |
| `docs/plans/working/2026-10-14-gd02-streaming-close.md` | `docs/plans/technical/2026-10-14-gd02-streaming-close.md` v1.1 | 2026-10-14 |

## 1. Đã giao

**Backend (`nexuslive-be`, WP1–WP2):**

- `POST /v1/streams/{id}/playback`, `POST /v1/vods/{id}/playback`: phiên xem, cổng 18+ phía server, nhớ xác nhận
  theo kênh (`mature_consents`), Guest dùng `X-NXL-Anon-Id`.
- `PUT /v1/me/watch-progress/{vodId}` (có `keepFurther`), `GET /v1/me/continue-watching`, trường `viewer` trong
  `GET /v1/vods/{id}`; xóa phiên hết hạn quá 24 giờ trong job VOD.
- Nginx: route `/v1/me/(watch-progress|continue-watching)` → stream-svc, CORS cho `X-NXL-Anon-Id`.

**Web (`nexuslive-web`, WP3–WP5, theo frame pen.dev):**

- Player: mở qua phiên xem (`use-playback`, `anon-id`), overlay 18+ gọi lại API, overlay trạng thái, thanh điều khiển,
  menu chất lượng; trạng thái "Buổi phát đã kết thúc" thay cho lỗi/tải lại khi stream đã ENDED.
- R2 Trang chủ: Hero stream nổi bật, "Tiếp tục xem" (tài khoản hoặc localStorage cho Guest), lưới "Kênh trực tiếp đề
  xuất", skeleton/rỗng/lỗi.
- R3 Trang xem live: ChannelInfo, tab Giới thiệu, cột Chat giữ chỗ (desktop) / tab Chat (mobile), Follow + Chia sẻ.
- Trang VOD: tiến độ theo tài khoản, gộp tiến độ local lên tài khoản khi đăng nhập (giữ vị trí xa hơn).
- R6: dọn token màu, Banner dùng chung, responsive 375 / 1440, nhãn a11y.
- E2E `e2e/m2.spec.ts`: hành trình M2 đầy đủ + cổng 18+.

## 2. Lệch so với plan

- Spec NXL-G2 v1.0 (Creator Studio) viết sau cùng ngày, xong WP6.
- E2E M2: lần publish thứ hai thử lại một lần nếu rơi vào thời gian chờ 30 s sau khi kết thúc (hành vi cố ý của
  stream-svc).

## 3. Sự cố

- Sau timeout tải, player hiện lỗi thay vì "đã kết thúc" khi stream ENDED → sửa luật `display-state` (ENDED thắng
  lỗi/timeout/reconnect khi không còn phát) và thêm unit test.
- Hai agent dùng chung `node_modules/.vite` gây lỗi 504 Vite → mỗi project một thư mục symlink riêng.

## 4. Kiểm thử

| Lệnh | Kết quả |
| --- | --- |
| `make format`, `make nxl-lint`, `make nxl-test` (`-race`) | pass |
| Repo test PG (`TestViewer`) | pass |
| `make nxl-swag` | đã sinh lại |
| Web `pnpm lint`, `tsc -b`, `vitest` | pass — 21 file, 137 test |
| Web `pnpm build` | pass |
| Web E2E trên stack cục bộ (không Docker) | 8/8 pass: guest-modal ×4, livestream, m1, m2 hành trình, m2 18+ |

| Luồng cũ chạy lại | Kết quả |
| --- | --- |
| Đăng ký → đơn Creator → Admin duyệt → Studio → publish ffmpeg → xem → kết thúc → VOD | pass |
| Modal Guest + `pendingAction` | pass |

Chưa chạy: `make nxl-up-all` (Docker) trên máy ông chủ; đo độ trễ bằng OBS thật.

## 5. Việc tiếp theo

- Đưa sự kiện/hằng số mới của NXL-G2 vào NXL-00 khi làm GĐ4; errata ST-Q2, ST-Q4.
- GĐ4: Category, trạng thái "Sắp diễn ra"; GĐ5: chữ ký `secure_link` cho `manifestUrl`, kiểm tra kênh bị khóa.
- GĐ3: thay cột Chat giữ chỗ bằng chat thật.
