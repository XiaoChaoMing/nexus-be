#!/usr/bin/env bash
# Transcode load test for media-svc: N synthetic publishers (ffmpeg plays OBS), then CPU/RAM of
# media-svc + its ffmpeg children, ffmpeg speed, dropped frames and the server-side HLS lag.
# Runs on Linux, macOS and Git Bash on Windows. Needs: bash, curl, awk, ffmpeg; docker for CPU mode
# "docker". Usage and the reading guide: scripts/measure-latency.md §6.
set -Eeuo pipefail
trap 'echo "bench: stopped at line $LINENO (exit $?)" >&2' ERR
trap 'echo "bench: interrupted" >&2; exit 130' INT

API=${API:-http://localhost:8080}             # Nginx edge (make nxl-up-all)
RTMP=${RTMP:-rtmp://localhost:1935/live}
METRICS=${METRICS:-http://localhost:8091/metrics}
MAILPIT=${MAILPIT:-http://localhost:8025}      # verification mails of the bench creators
ADMIN_LOGIN=${ADMIN_LOGIN:-admin@nexuslive.dev} # dev seed admin approves the bench creators
ADMIN_PASSWORD=${ADMIN_PASSWORD:-${IDENTITY_DEV_SEED_PASSWORD:-nexuslive123}}
COUNTS=${COUNTS:-"1 2 3"}
DURATION=${DURATION:-120}                     # seconds sampled per round
WARMUP=${WARMUP:-20}                          # seconds after every publisher is live
INTERVAL=${INTERVAL:-5}
SIZE=${SIZE:-1280x720}
FPS=${FPS:-30}
BITRATE=${BITRATE:-4500k}
CPU_MODE=${CPU_MODE:-docker}                  # docker | proc (media-svc runs as a local process)
CONTAINER=${CONTAINER:-media}
OUT=${OUT:-bench-out}

usage() {
	cat <<EOF
Usage: scripts/bench-transcode.sh [options]
  -n "1 2 3"    publisher counts, one round each         (COUNTS=$COUNTS)
  -d SECONDS    sampling time per round                  (DURATION=$DURATION)
  -s WxH        source size of every publisher           (SIZE=$SIZE)
  -b RATE       source video bitrate                     (BITRATE=$BITRATE)
  -m MODE       CPU source: docker | proc                (CPU_MODE=$CPU_MODE)
  -o DIR        output directory                         (OUT=$OUT)
Environment: API, RTMP, METRICS, MAILPIT, ADMIN_LOGIN, ADMIN_PASSWORD, WARMUP, INTERVAL, FPS, CONTAINER.\nEvery publisher is a fresh Creator: register, verify the email (Mailpit), apply, approved by the dev seed admin.
MEDIA_MAX_CONCURRENT_STREAMS must be >= the largest count, or the extra publishers are refused.
EOF
}

while getopts "n:d:s:b:m:o:h" opt; do
	case $opt in
	n) COUNTS=$OPTARG ;;
	d) DURATION=$OPTARG ;;
	s) SIZE=$OPTARG ;;
	b) BITRATE=$OPTARG ;;
	m) CPU_MODE=$OPTARG ;;
	o) OUT=$OPTARG ;;
	*) usage; exit 0 ;;
	esac
done

for tool in curl awk ffmpeg; do
	command -v "$tool" >/dev/null || { echo "missing: $tool" >&2; exit 1; }
done
[ "$CPU_MODE" = docker ] && { command -v docker >/dev/null || { echo "missing: docker (or use -m proc)" >&2; exit 1; }; }

mkdir -p "$OUT"
RUN=$(date +%s)
SAMPLES="$OUT/samples-$RUN.csv"
LAGS="$OUT/lag-$RUN.csv"
SUMMARY="$OUT/summary-$RUN.md"
echo "count,t,cpu_pct,mem_mb,active,speed_avg,speed_min,dropped" >"$SAMPLES"
echo "count,segment,lag_s" >"$LAGS"

PUB_PIDS=()
TOKENS=()

cleanup() {
	# shellcheck disable=SC2015 # best effort: nothing to do when a publisher is already gone
	for pid in "${PUB_PIDS[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; done
	for tok in "${TOKENS[@]:-}"; do
		# shellcheck disable=SC2015 # best effort, like above
		[ -n "$tok" ] && curl -s -o /dev/null -X POST "$API/v1/studio/stream/end" -H "Authorization: Bearer $tok" || true
	done
	PUB_PIDS=()
	TOKENS=()
}
trap cleanup EXIT

die() { echo "bench: $*" >&2; exit 1; }

# Fails fast with a readable reason instead of a bare curl exit code.
preflight() {
	curl -sf -o /dev/null --max-time 5 "$API/healthz" ||
		die "cannot reach the edge at $API/healthz. Start the stack with 'make nxl-up-all' (or set API=http://host:port)."
	curl -sf -o /dev/null --max-time 5 "$METRICS" ||
		die "cannot read $METRICS. Is media-svc running and port 8091 published? ('docker compose ps media')"
	if [ "$CPU_MODE" = docker ]; then
		docker inspect "$CONTAINER" >/dev/null 2>&1 ||
			die "container '$CONTAINER' not found. Set CONTAINER=<name> or use -m proc."
	fi
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -X POST "$API/v1/auth/login" \
		-H 'Content-Type: application/json' -d '{"login":"x","password":"y"}')
	[ "$code" = 401 ] || [ "$code" = 400 ] ||
		die "identity-svc through the edge answered $code to /v1/auth/login (expected 401). Check 'docker compose logs identity nginx'."
}

json_field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" | head -n1; }
json_first() { { grep -o "\"$1\":\"[^\"]*\"" || true; } | head -n1 | cut -d'"' -f4; } # first occurrence (json_field takes the last)

# POST/GET with JSON through the edge; waits out the auth rate limit (429) instead of failing.
# Prints the body, then the status code on the last line.
api() { # method path [json] [token]
	local method=$1 path=$2 body=${3:-} token=${4:-} out tries=0
	local args=(-s -w '\n%{http_code}' -X "$method" "$API$path" -H 'X-NXL-Client: web')
	[ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
	[ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
	while :; do
		out=$(curl "${args[@]}")
		[ "${out##*$'\n'}" = 429 ] && [ $tries -lt 12 ] || { printf '%s\n' "$out"; return 0; }
		tries=$((tries + 1))
		sleep 7
	done
}
status_of() { printf '%s\n' "$1" | tail -n1; }
body_of() { printf '%s\n' "$1" | sed '$d'; }

ADMIN_TOKEN=""
admin_token() {
	if [ -z "$ADMIN_TOKEN" ]; then
		local res
		res=$(api POST /v1/auth/login "{\"login\":\"$ADMIN_LOGIN\",\"password\":\"$ADMIN_PASSWORD\"}")
		[ "$(status_of "$res")" = 200 ] || die "admin login ($ADMIN_LOGIN) failed: $(body_of "$res"). Is IDENTITY_DEV_SEED on? Set ADMIN_PASSWORD if the seed password differs."
		ADMIN_TOKEN=$(body_of "$res" | json_field accessToken)
	fi
	printf '%s' "$ADMIN_TOKEN"
}

verify_token_of() { # email -> verification token from the newest mail in Mailpit
	local id i
	for i in $(seq 1 20); do
		id=$(curl -s "$MAILPIT/api/v1/search?query=to:$1" | json_first ID)
		if [ -n "$id" ]; then
			curl -s "$MAILPIT/api/v1/message/$id" | grep -o 'verify-email?token=[A-Za-z0-9_-]*' | head -n1 | cut -d= -f2
			return 0
		fi
		sleep 1
	done
}

creator() { # name -> access token of a new Creator with a channel
	local name=$1 email="$1@bench.local" res tok vtok app i
	res=$(api POST /v1/auth/register "{\"username\":\"$name\",\"displayName\":\"$name\",\"email\":\"$email\",\"password\":\"Bench12345\",\"acceptTerms\":true}")
	[ "$(status_of "$res")" = 201 ] || die "register $name failed: $(body_of "$res")"
	tok=$(body_of "$res" | json_field accessToken)

	vtok=$(verify_token_of "$email")
	[ -n "$vtok" ] || die "no verification mail for $email in $MAILPIT (identity SMTP → Mailpit? 'docker compose logs identity')."
	res=$(api POST /v1/auth/verify-email "{\"token\":\"$vtok\"}")
	[ "$(status_of "$res")" = 200 ] || die "verify-email for $email failed: $(body_of "$res")"

	res=$(api POST /v1/creator-applications '{"legalName":"Bench Creator","birthDate":"1995-01-01","acceptCreatorTerms":true}' "$tok")
	[ "$(status_of "$res")" = 201 ] || die "creator application of $name failed: $(body_of "$res")"
	app=$(body_of "$res" | json_first id)
	res=$(api POST "/v1/admin/creator-applications/$app/approve" "" "$(admin_token)")
	[ "$(status_of "$res")" = 200 ] || die "approving $name failed: $(body_of "$res")"

	# The channel reaches stream-svc through events; the Studio opens a moment later.
	for i in $(seq 1 30); do
		[ "$(status_of "$(api GET /v1/studio/stream "" "$tok")")" = 200 ] && { printf '%s' "$tok"; return 0; }
		sleep 1
	done
	die "the Studio of $name did not open after approval (channel-svc/stream-svc events? 'docker compose logs channel stream')."
}

stream_key() { curl -sf -X POST "$API/v1/studio/stream-key" -H "Authorization: Bearer $1" | json_field streamKey; }

current_manifest() { # manifestUrl of the creator's current stream, e.g. /hls/streams/<id>/master.m3u8
	curl -sf "$API/v1/studio/stream" -H "Authorization: Bearer $1" | json_field manifestUrl
}

publish() { # key, log file
	ffmpeg -hide_banner -loglevel error -re \
		-f lavfi -i "testsrc2=size=$SIZE:rate=$FPS" -f lavfi -i "sine=frequency=440:sample_rate=48000" \
		-c:v libx264 -preset ultrafast -tune zerolatency -b:v "$BITRATE" -maxrate "$BITRATE" -bufsize "$BITRATE" \
		-g $((FPS * 2)) -keyint_min $((FPS * 2)) -pix_fmt yuv420p -c:a aac -b:a 128k \
		-f flv "$RTMP/$1" >"$2" 2>&1 &
	PUB_PIDS+=($!)
}

metrics() { # -> "active speed_avg speed_min dropped"
	curl -sf "$METRICS" | awk '
		/^media_active_streams / { active = $2 }
		/^media_ffmpeg_speed\{/ { n++; s += $2; if (min == "" || $2 < min) min = $2 }
		/^media_dropped_frames_total\{/ { d += $2 }
		END { printf "%d %.3f %.3f %d\n", active, (n ? s / n : 0), (min == "" ? 0 : min), d }'
}

# ---- CPU / RAM ---------------------------------------------------------------------------------

cpu_docker() { # -> "cpu_pct mem_mb"; docker reports 100% per core
	docker stats --no-stream --format '{{.CPUPerc}};{{.MemUsage}}' "$CONTAINER" | awk -F';' '{
		cpu = $1; sub(/%/, "", cpu)
		split($2, m, " "); v = m[1]; mb = v + 0
		if (v ~ /GiB/) mb *= 1024; else if (v ~ /KiB/) mb /= 1024
		printf "%.1f %.0f\n", cpu, mb }'
}

media_pids() { # media-svc and its ffmpeg children
	local root
	root=$(pgrep -x media-svc || pgrep -x media || true)
	[ -z "$root" ] && return
	echo "$root"
	pgrep -P "$root" || true
}

proc_ticks() { # summed utime+stime of the media process tree, in clock ticks
	local t=0 pid
	for pid in $(media_pids); do
		[ -r "/proc/$pid/stat" ] || continue
		t=$((t + $(awk '{ print $14 + $15 }' "/proc/$pid/stat")))
	done
	echo "$t"
}

proc_rss_mb() {
	local kb=0 pid
	for pid in $(media_pids); do
		[ -r "/proc/$pid/status" ] || continue
		kb=$((kb + $(awk '/^VmRSS/ { print $2 }' "/proc/$pid/status")))
	done
	echo $((kb / 1024))
}

# ---- HLS lag ----------------------------------------------------------------------------------

# Publishers run with -re and a 2 s GOP, so segment k (counted from the first one in the EVENT
# playlist) holds the frames sent during [t0 + 2(k-1), t0 + 2k). Its lag is the time it becomes
# visible through the edge minus t0 + 2k: publisher encode + RTMP + media-svc + transcode + upload.
watch_lag() { # count, manifest path, t0 (epoch seconds when the first publisher started)
	local count=$1 base=${2%/*} t0=$3 master variant last="" end=$((SECONDS + DURATION))
	master=$(curl -sf "$API$2") || return 0
	variant=$(printf '%s\n' "$master" | awk '!/^#/ && NF { print; exit }')
	while [ $SECONDS -lt $end ]; do
		local pl seg now
		pl=$(curl -sf "$API$base/$variant" || true)
		now=$(date +%s.%N)
		seg=$(printf '%s\n' "$pl" | awk -F'[_.]' '/^seg_/ { n = $2 + 0; if (first == "") first = n; last = n }
			END { if (last != "") print first, last }')
		if [ -n "$seg" ]; then
			# shellcheck disable=SC2086 # split "first last" into $1 $2
			set -- $seg
			if [ "$2" != "$last" ] && [ -n "$last" ]; then
				awk -v c="$count" -v k=$(($2 - $1 + 1)) -v now="$now" -v t0="$t0" -v seg="$2" \
					'BEGIN { printf "%s,%s,%.2f\n", c, seg, now - (t0 + 2 * k) }' >>"$LAGS"
			fi
			last=$2
		fi
		sleep 0.5
	done
}

# ---- rounds ------------------------------------------------------------------------------------

round() {
	local n=$1 i tok key t0 active=0 waited=0
	echo "== $n publisher(s)"
	ADMIN_TOKEN=$(admin_token) # once per round: creator() runs in a subshell and cannot cache it
	for i in $(seq 1 "$n"); do
		tok=$(creator "b${RUN: -8}_${n}_$i")
		key=$(stream_key "$tok" || true)
		[ -n "$key" ] || die "POST /v1/studio/stream-key failed for a new user (stream-svc up? 'docker compose logs stream')."
		TOKENS+=("$tok")
		[ "$i" = 1 ] && t0=$(date +%s.%N)
		publish "$key" "$OUT/publisher-$RUN-$n-$i.log"
	done

	while [ "$active" -lt "$n" ] && [ $waited -lt 45 ]; do
		sleep 1
		waited=$((waited + 1))
		read -r active _ _ _ < <(metrics || echo "0 0 0 0") || active=0
	done
	[ "$active" -lt "$n" ] && echo "warning: only $active/$n transcoding (MEDIA_MAX_CONCURRENT_STREAMS?)" >&2
	sleep "$WARMUP"

	local manifest
	manifest=$(current_manifest "${TOKENS[0]}")
	[ -n "$manifest" ] && watch_lag "$n" "$manifest" "$t0" &
	local lag_pid=$!

	local t=0 prev_ticks prev_t ticks now cpu mem hz
	hz=$(getconf CLK_TCK 2>/dev/null || echo 100)
	prev_ticks=$( [ "$CPU_MODE" = proc ] && proc_ticks || echo 0)
	prev_t=$(date +%s.%N)
	while [ $t -lt "$DURATION" ]; do
		sleep "$INTERVAL"
		t=$((t + INTERVAL))
		if [ "$CPU_MODE" = proc ]; then
			ticks=$(proc_ticks)
			now=$(date +%s.%N)
			cpu=$(awk -v a="$prev_ticks" -v b="$ticks" -v t0="$prev_t" -v t1="$now" -v hz="$hz" \
				'BEGIN { printf "%.1f", (b - a) / hz / (t1 - t0) * 100 }')
			mem=$(proc_rss_mb)
			prev_ticks=$ticks
			prev_t=$now
		else
			read -r cpu mem < <(cpu_docker || true) || { cpu=0; mem=0; }
		fi
		read -r active savg smin dropped < <(metrics || true) || { active=0; savg=0; smin=0; dropped=0; }
		echo "$n,$t,$cpu,$mem,$active,$savg,$smin,$dropped" | tee -a "$SAMPLES"
	done
	wait "$lag_pid" 2>/dev/null || true

	cleanup
	sleep 15 # VOD finalization and uploads of this round must not count in the next one
}

cores=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo "?")
[ "$CPU_MODE" = docker ] && cores=$(docker info --format '{{.NCPU}}' 2>/dev/null || echo "$cores")

preflight
for n in $COUNTS; do round "$n"; done

{
	echo "# Bench transcode — $(date '+%Y-%m-%d %H:%M')"
	echo
	echo "Source ${SIZE}@${FPS} ${BITRATE}, CPU mode \`$CPU_MODE\`, ${cores} cores (100% = 1 core), ${DURATION}s per round."
	echo
	echo "| Streams | CPU avg (%) | CPU peak (%) | CPU avg / cores | RAM avg (MB) | speed at end | HLS lag median (s) | HLS lag max (s) | Lag trend (s/min) | Keeps up? |"
	echo "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |"
	for n in $COUNTS; do
		# median, max, and slope of the lag: first vs last third of the round
		lag=$(awk -F, -v n="$n" 'NR > 1 && $1 == n { v[++k] = $3 }
			END {
				if (!k) { print "- - -"; exit }
				for (i = 1; i <= k; i++) s[i] = v[i]
				for (i = 1; i <= k; i++) for (j = i + 1; j <= k; j++) if (s[j] < s[i]) { t = s[i]; s[i] = s[j]; s[j] = t }
				third = int(k / 3); if (third < 1) third = 1
				for (i = 1; i <= third; i++) { a += v[i]; b += v[k - i + 1] }
				span = (k - third) * 2 / 60   # segments are 2 s apart
				printf "%.2f %.2f %.2f", s[int((k + 1) / 2)], s[k], (span > 0 ? (b - a) / third / span : 0)
			}' "$LAGS")
		awk -F, -v n="$n" -v cores="$cores" -v lag="$lag" 'NR > 1 && $1 == n {
			k++; c += $3; if ($3 > p) p = $3; m += $4; last = $7 }
			END {
				if (!k) exit
				split(lag, L, " ")
				per = (cores + 0 > 0) ? sprintf("%.0f%%", c / k / cores) : "-"
				ok = (L[3] != "-" && L[3] + 0 < 0.5 && last + 0 >= 0.97) ? "yes" : "NO"
				printf "| %d | %.0f | %.0f | %s | %.0f | %.2f | %s | %s | %s | %s |\n", n, c / k, p, per, m / k, last, L[1], L[2], L[3], ok
			}' "$SAMPLES"
	done
	echo
	echo "HLS lag = time a segment shows up in the playlist through the edge − time its last frame left the"
	echo "publisher (first stream of each round). Viewer latency ≈ HLS lag + player buffer (2 segments ≈ 4 s) + playlist polling."
	echo "ffmpeg speed is an average since the transcoder started, so it starts low and climbs; judge by the end value"
	echo "and the lag trend. Keeps up = lag trend < 0.5 s/min and speed at end >= 0.97."
	echo "Raw: $(basename "$SAMPLES"), $(basename "$LAGS")."
} | tee "$SUMMARY"
