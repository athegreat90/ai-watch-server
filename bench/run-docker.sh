#!/usr/bin/env bash
# Benchmark the Python server, the experimental Go port and the production Go image under the
# production container limits, from one command, on any machine with Docker (Linux, WSL, Docker
# Desktop). Everything runs in Linux containers on a private network, so Windows' ephemeral-port
# limit does not matter and every server gets the same CPU/memory limits.
#
#   bash bench/run-docker.sh [reps] [duration]      defaults: 3 reps, 4s per step
#
# Needs: docker, curl, jq, awk. Builds the production image from ../Dockerfile as ai-watch-bench:prod.
# Parity runs first against every server; a parity failure aborts, because its numbers would mean nothing.
set -euo pipefail
export MSYS_NO_PATHCONV=1 # Git Bash on Windows must not rewrite container paths

REPS=${1:-3}
DURATION=${2:-4s}
SCENARIOS=${SCENARIOS:-status,events,mixed}
CONCURRENCY=${CONCURRENCY:-1,8,32}
CPUS=${CPUS:-0.5}      # production limit in compose.yaml
MEMORY=${MEMORY:-128m} # production limit in compose.yaml
GO_IMAGE=${GO_IMAGE:-golang:1.27.4-bookworm}
PY_IMAGE=${PY_IMAGE:-python:3.15.2-slim-bookworm}
STATIC_IMAGE=${STATIC_IMAGE:-gcr.io/distroless/static-debian13:nonroot}

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
RUN=$(date +%Y%m%d-%H%M%S)
OUT="$HERE/results/$RUN-docker"
WORK=$(mktemp -d)
NET=aiwatch-bench
mkdir -p "$OUT"

# Docker Desktop on Windows needs Windows-style host paths for bind mounts.
host() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else echo "$1"; fi; }
cleanup() { docker rm -f aiw-bench-srv >/dev/null 2>&1 || true; docker network rm "$NET" >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT

echo "== building linux tools and the production image"
mkdir -p "$WORK/bin"
docker run --rm -e CGO_ENABLED=0 -v "$(host "$HERE"):/src" -v "$(host "$WORK/bin"):/out" -w /src "$GO_IMAGE" sh -c \
  'go build -o /out/bench . && go build -o /out/goserver ./goserver && go test -c -o /out/parity.test ./goserver' >/dev/null
docker build -q -t ai-watch-bench:prod "$(host "$ROOT")" >/dev/null
docker pull -q "$PY_IMAGE" >/dev/null
docker network create "$NET" >/dev/null 2>&1 || true

# Wait for health; prints the elapsed milliseconds from $1 (a start timestamp in ms).
now_ms() { date +%s%3N; }

# sample_stats <seconds-file> : appends "cpu% memMiB" lines until the file /tmp flag vanishes.
sample() { # <flagfile> <outfile>
  while [ -e "$1" ]; do
    docker stats aiw-bench-srv --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' 2>/dev/null \
      | awk '{ gsub("%","",$1); m=$2; v=m+0; if (m ~ /GiB/) v*=1024; else if (m ~ /KiB/) v/=1024; print $1, v }' >> "$2" || true
  done
}

start_server() { # label token -> sets PORT
  local label=$1 token=$2 cfg="$WORK/config.$1.json"
  printf '{"token":"%s","port":8765}' "$token" > "$cfg"; chmod 644 "$cfg"
  local common=(-d --name aiw-bench-srv --network "$NET" -p 127.0.0.1::8765 --cpus "$CPUS" --memory "$MEMORY" --pids-limit 64
    --user 1000:1000 --read-only --cap-drop ALL --security-opt no-new-privileges:true -e MONITOR_BIND=0.0.0.0)
  case "$label" in
    python) docker run "${common[@]}" --mount "type=bind,source=$(host "$cfg"),target=/app/config.local.json,readonly" \
              --mount "type=bind,source=$(host "$ROOT/bridge/server.py"),target=/app/server.py,readonly" -w /app \
              "$PY_IMAGE" python /app/server.py >/dev/null ;;
    go-experimental) docker run "${common[@]}" --mount "type=bind,source=$(host "$cfg"),target=/app/config.local.json,readonly" \
              --mount "type=bind,source=$(host "$WORK/bin/goserver"),target=/app/goserver,readonly" -w /app \
              --entrypoint /app/goserver "$STATIC_IMAGE" >/dev/null ;;
    go-production) docker run "${common[@]}" --mount "type=bind,source=$(host "$cfg"),target=/app/config.local.json,readonly" \
              ai-watch-bench:prod >/dev/null ;;
  esac
  PORT=$(docker port aiw-bench-srv 8765/tcp | awk -F: 'NR == 1 { print $NF }')
}

run_one() { # label rep
  local label=$1 rep=$2 token t0 t1 idle
  token=$(head -c 30 /dev/urandom | base64 | tr -d '=+/\n' | cut -c1-32)
  t0=$(now_ms)
  start_server "$label" "$token"
  for _ in $(seq 200); do curl -fs -m 1 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break; sleep 0.05; done
  curl -fs -m 1 "http://127.0.0.1:$PORT/health" >/dev/null || { echo "$label did not start"; docker logs aiw-bench-srv; exit 1; }
  t1=$(now_ms)
  sleep 2
  idle=$(docker stats aiw-bench-srv --no-stream --format '{{.MemUsage}}' | awk '{ m=$1; v=m+0; if (m ~ /GiB/) v*=1024; else if (m ~ /KiB/) v/=1024; print v }')
  local loadgen=(docker run --rm --user 0 --network "$NET" -v "$(host "$WORK/bin"):/bin:ro" -v "$(host "$OUT"):/out" --entrypoint)
  echo "== $label rep $rep: parity"
  "${loadgen[@]}" /bin/parity.test "$STATIC_IMAGE" -test.run Parity -url http://aiw-bench-srv:8765 -token "$token" -allow-remote >/dev/null \
    || { echo "PARITY FAILED for $label"; exit 1; }
  echo "== $label rep $rep: benchmark"
  : > "$WORK/stats.txt"; touch "$WORK/flag"
  sample "$WORK/flag" "$WORK/stats.txt" &
  local sampler=$!
  "${loadgen[@]}" /bin/bench "$STATIC_IMAGE" -url http://aiw-bench-srv:8765 -token "$token" -allow-remote \
    -scenario "$SCENARIOS" -c "$CONCURRENCY" -d "$DURATION" -label "$label" -out "/out/$label-$rep.json" >/dev/null 2>&1 || { echo "bench failed for $label"; exit 1; }
  rm -f "$WORK/flag"; wait "$sampler" || true
  echo "{\"label\":\"$label\",\"rep\":$rep,\"startup_ms\":$((t1 - t0)),\"idle_mib\":$idle,\"peak_mib\":$(awk 'BEGIN{m=0} $2>m{m=$2} END{print m}' "$WORK/stats.txt"),\"avg_cpu_pct\":$(awk '{s+=$1;n++} END{printf "%.1f", (n?s/n:0)}' "$WORK/stats.txt")}" > "$OUT/$label-$rep.meta.json"
  docker rm -f aiw-bench-srv >/dev/null
}

echo "== image sizes"
docker image ls --format '{{.Repository}}:{{.Tag}} {{.Size}}' | grep -E "^(ai-watch-bench:prod|${PY_IMAGE}) " | tee "$OUT/image-sizes.txt"

for rep in $(seq "$REPS"); do
  for label in python go-experimental go-production; do run_one "$label" "$rep"; done
done

# ---- summary: median over the repetitions -------------------------------------------------------
cd "$OUT" # jq on Windows is a native binary: give it relative paths
SUMMARY="$HERE/results/$RUN-docker-compare.md"
LABELS=(python go-experimental go-production)
{
  echo "# Docker benchmark: Python vs experimental Go vs production Go"
  echo
  echo "- date: $RUN, repetitions: $REPS (median shown), step duration: $DURATION, scenarios: $SCENARIOS, clients: $CONCURRENCY"
  echo "- every server ran in a container with --cpus $CPUS --memory $MEMORY --read-only --cap-drop ALL on a private network; the load generator ran in another container"
  echo "- python: $(docker run --rm "$PY_IMAGE" python --version), go: $(docker run --rm "$GO_IMAGE" go version | cut -d' ' -f3)"
  echo "- host: $(docker info --format '{{.OperatingSystem}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes RAM')"
  echo "- parity table passed on every server before every benchmark run"
  echo
  echo "## Throughput and latency (median of $REPS runs)"
  echo
  echo "| scenario | clients | python rps | go-exp rps | go-prod rps | python p50/p95/p99 ms | go-exp p50/p95/p99 ms | go-prod p50/p95/p99 ms | errors py/exp/prod |"
  echo "|---|---|---|---|---|---|---|---|---|"
  jq -s -r '
    def med: sort | .[(length/2|floor)];
    [.[] | .label as $l | .steps[] | {l:$l, s:.scenario, c:.concurrency, rps, p50:.p50_ms, p95:.p95_ms, p99:.p99_ms, err:(.http_errors+.dropped+.timeouts+.conn_errors)}]
    | group_by([.s,.c])[]
    | . as $g
    | def by($l): [$g[] | select(.l == $l)];
      def f($l; $k): (by($l) | map(.[$k]) | med | . * 10 | round / 10);
      def lat($l): "\(f($l;"p50"))/\(f($l;"p95"))/\(f($l;"p99"))";
      def err($l): (by($l) | map(.err) | add);
      "| \($g[0].s) | \($g[0].c) | \(f("python";"rps") | floor) | \(f("go-experimental";"rps") | floor) | \(f("go-production";"rps") | floor) | \(lat("python")) | \(lat("go-experimental")) | \(lat("go-production")) | \(err("python"))/\(err("go-experimental"))/\(err("go-production")) |"
  ' *-[0-9].json
  echo
  echo "## Resources (median of $REPS runs, server container under the limits above)"
  echo
  echo "| server | startup to healthy (ms, includes docker run) | idle memory (MiB) | peak memory under load (MiB) | mean CPU during load (% of one core) | image size |"
  echo "|---|---|---|---|---|---|"
  for l in "${LABELS[@]}"; do
    case "$l" in python) size=$(awk -v i="$PY_IMAGE" '$1 == i {print $2}' image-sizes.txt) ;; go-production) size=$(awk '$1 == "ai-watch-bench:prod" {print $2}' image-sizes.txt) ;; *) size='n/a (same binary class as go-production)' ;; esac
    jq -s -r --arg l "$l" --arg size "$size" 'def med: sort | .[(length/2|floor)];
      map(select(.label == $l)) | "| \($l) | \(map(.startup_ms)|med) | \(map(.idle_mib)|med) | \(map(.peak_mib)|med) | \(map(.avg_cpu_pct)|med) | \($size) |"' $l-*.meta.json
  done
  echo
  echo "Notes: single host, shared by the load generator and Docker Desktop's VM; treat ratios as indicative, not absolute. Startup includes container creation. CPU% is docker stats' sample (can exceed the limit in a sample)."
} > "$SUMMARY"
echo "summary: $SUMMARY"
