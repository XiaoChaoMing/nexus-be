# Đo độ trễ và CPU của livestream

Dùng cho phiếu `DATN/docs/report/measurements/2026-10-livestream.md` (TSD core livestream §7A.7).

## 1. Chuẩn bị

- `make nxl-media-up`, web `pnpm dev` (http://localhost:5173), Chrome thật.
- OBS: Output 1280×720 hoặc 1920×1080, 30 FPS, x264 CBR 4500–6000 kbps, Keyframe Interval **2 s**.
- Trong OBS thêm nguồn **Text (GDI+)** hoặc **Browser** hiển thị đồng hồ có mili giây, ví dụ Browser source với URL:
  `data:text/html,<body style="margin:0;background:#000;color:#0f0;font:72px monospace"><div id=t></div><script>setInterval(()=>t.textContent=new Date().toISOString().slice(11,23),16)</script>`

## 2. Độ trễ glass-to-glass

1. Mở trang xem `/live/<id>` ở chế độ **Auto**, để player ở điểm live (nhãn LIVE màu đỏ, không có `-mm:ss`).
2. Đặt cửa sổ OBS (preview) và trình duyệt cạnh nhau trên cùng màn hình.
3. Chụp màn hình (Win + Shift + S) 10 lần, cách nhau ~10 giây.
4. Mỗi ảnh: độ trễ = giờ trên preview OBS − giờ trên player.
5. Ghi trung vị và lớn nhất. Lặp lại với 720p / 480p / 360p cố định nếu cần.

Mục tiêu (roadmap GĐ0, tiêu chí S2): ≤ 6 s.

## 3. CPU khi transcode

Trong lúc phát, chạy (PowerShell hoặc Git Bash) để ghi CPU/RAM của container media mỗi 5 s ra CSV:

```bash
echo "time,cpu,mem" > cpu-media.csv
while true; do
  docker stats --no-stream --format "{{.CPUPerc}},{{.MemUsage}}" media | sed "s/^/$(date +%H:%M:%S),/" >> cpu-media.csv
  sleep 5
done
```

Đo 3 trường hợp, mỗi trường hợp ≥ 2 phút: 1 stream, 2 stream (`MEDIA_MAX_CONCURRENT_STREAMS=2`), và nguồn 1080p so với 720p.

## 4. Số liệu từ /metrics

```bash
curl -s localhost:8091/metrics | grep -E '^media_(active_streams|ffmpeg_speed|dropped_frames_total|upload_seconds_(sum|count)|upload_errors_total|publish_rejected_total)'
curl -s localhost:8092/metrics | grep -E '^stream_'
```

- `media_ffmpeg_speed` phải ≥ 1.0 (thấp hơn → máy không theo kịp thời gian thực).
- Thời gian upload trung bình = `media_upload_seconds_sum / media_upload_seconds_count`.

## 5. Kịch bản kiểm thử thủ công L4

| # | Kịch bản | Kỳ vọng |
| --- | --- | --- |
| 1 | OBS bật Auto-reconnect, đang phát → Studio bấm **Kết thúc** | OBS báo lỗi kết nối vài lần rồi dừng; Studio vẫn Offline, **không** có buổi phát mới; sau 30 s phát lại được |
| 2 | Đang phát → `docker kill -s KILL media` → `docker start media` | Log `recovered 1 stream(s)`; stream ENDED, có VOD; OBS nối lại tạo buổi phát mới |
| 3 | `MEDIA_MAX_CONCURRENT_STREAMS=1`, hai tài khoản cùng phát | Kênh thứ hai bị từ chối; `/healthz` → `rejectedPublishes.capacity` tăng |
| 4 | Kết thúc → trang xem → **Xem lại** → xem 1 phút → tải lại trang | Hỏi "Xem tiếp từ 1:00?"; chọn Xem tiếp phát từ đúng vị trí |

## 6. Đo tải transcode tự động (`scripts/bench-transcode.sh`)

Script đóng vai N OBS bằng ffmpeg (test pattern, x264 `ultrafast`, GOP 2 s), đi qua cổng vào, và ghi mỗi 5 s:
CPU/RAM của media-svc + các tiến trình ffmpeg con, tốc độ ffmpeg, frame rơi, và **độ trễ HLS phía server**
(từ lúc frame cuối của một segment rời publisher tới lúc segment xuất hiện trong playlist qua cổng vào).

Từ GĐ1 chỉ Creator có kênh mới phát được, nên mỗi publisher là một Creator mới: script đăng ký, lấy link xác minh
email từ Mailpit (`MAILPIT`, mặc định `http://localhost:8025`), nộp đơn Creator và để Admin seed duyệt
(`ADMIN_LOGIN` / `ADMIN_PASSWORD`, mặc định `admin@nexuslive.dev` / `IDENTITY_DEV_SEED_PASSWORD`). Cần
`IDENTITY_DEV_SEED=true` và identity gửi mail tới Mailpit (mặc định của `make nxl-up-all`). Gặp giới hạn `/v1/auth`
của cổng vào (10 lần/phút) script tự chờ rồi thử lại, nên vòng 3 publisher mất thêm khoảng 1 phút để chuẩn bị.

```bash
# Stack đầy đủ, cho phép đủ số luồng cần đo
MEDIA_MAX_CONCURRENT_STREAMS=3 make nxl-up-all
make nxl-bench COUNTS="1 2 3"                                   # hoặc: bash scripts/bench-transcode.sh -n "1 2 3"
bash scripts/bench-transcode.sh -n "1 2" -s 1920x1080 -b 6000k  # nguồn 1080p
```

| Tùy chọn | Mặc định | Ý nghĩa |
| --- | --- | --- |
| `-n` | `1 2 3` | Số publisher của từng vòng |
| `-d` | 120 | Số giây lấy mẫu mỗi vòng (sau khi mọi luồng đã chạy + 20 s khởi động) |
| `-s` / `-b` | `1280x720` / `4500k` | Độ phân giải và bitrate nguồn |
| `-m` | `docker` | `docker`: `docker stats media` · `proc`: đọc `/proc` (media-svc chạy trực tiếp trên Linux) |
| `API`, `RTMP`, `METRICS` | edge `:8080`, `:1935`, `:8091/metrics` | Đổi khi chạy ngoài compose |

Kết quả: `bench-out/summary-<run>.md` (bảng tổng hợp) và CSV thô `samples-*.csv`, `lag-*.csv`.

**Cách đọc:**

- **Cột quyết định là "Lag trend" (giây/phút):** độ trễ HLS phía server ở cuối vòng so với đầu vòng. Gần 0 → máy
  theo kịp; dương rõ (vài giây/phút) → segment ra chậm dần, độ trễ tăng không giới hạn.
- `speed` của ffmpeg là **trung bình từ lúc transcoder khởi động**, nên đầu vòng thấp (0,8–0,9) rồi tăng dần
  về 1,0 nếu máy theo kịp. Đọc giá trị cuối vòng ("speed at end"), không đọc giá trị nhỏ nhất.
- "Keeps up = yes" khi lag trend < 0,5 s/phút và speed cuối vòng ≥ 0,97.
- CPU tính 100% = 1 core. Publisher cũng chạy trên cùng máy (~16% core mỗi luồng 720p): khi đo trên máy demo
  có OBS thật, cộng thêm CPU của OBS.
- Độ trễ người xem ≈ HLS lag + bộ đệm player (hls.js `liveSyncDurationCount = 2` → ~4 s) + chu kỳ tải
  playlist (≤ 2 s). Số chính thức của tiêu chí S2 vẫn đo bằng OBS + đồng hồ (§2).
- Giá trị `MEDIA_MAX_CONCURRENT_STREAMS` chọn theo ADR-012: số luồng lớn nhất có "Keeps up = yes" và CPU
  media ≤ 75% tổng số core Docker được cấp.
- Script dừng giữa chừng sẽ in `bench: stopped at line N (exit X)`; gửi dòng đó kèm `bench-out/` để tra.
