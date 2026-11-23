#!/bin/bash
# Benchmark the Python server and the Go port one after the other and write a side-by-side report.
# Run inside Linux/WSL (Windows runs out of ephemeral ports, see README.md). Usage:
#
#   BIN=<dir with bench, goserver and parity.test, built with GOOS=linux> bench/run-compare.sh [all|1core] [bench flags]
#
#   all    - no CPU pinning
#   1core  - servers pinned to CPU 0 (closer to production's cpus: 0.5), load generator on the other CPUs
set -u
BIN=${BIN:?set BIN to the directory with the linux binaries bench, goserver and parity.test}
PYTHON=${PYTHON:-python3} # interpreter that runs bridge/server.py; production uses 3.15.2 (see ../Dockerfile)
MODE=${1:-all}; [ $# -gt 0 ] && shift
HERE=$(cd "$(dirname "$0")" && pwd)
case "$MODE" in
  all)   PIN_SRV=""; PIN_LOAD="" ;;
  1core) PIN_SRV="taskset -c 0"; PIN_LOAD="taskset -c 1-$(($(nproc) - 1))" ;;
  *) echo "mode must be all or 1core"; exit 2 ;;
esac
RUN=$(date +%Y%m%d-%H%M%S)
cd "$HERE" && mkdir -p results
SRV=""; TMP=""
trap '[ -n "$SRV" ] && kill $SRV 2>/dev/null; [ -n "$TMP" ] && rm -rf "$TMP"' EXIT

run() { # label port
  local label=$1 port=$2 token url=http://127.0.0.1:$2
  shift 2 # what is left are the extra flags for bench
  TMP=$(mktemp -d)
  token=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
  echo "{\"token\":\"$token\",\"port\":$port}" > "$TMP/config.local.json"
  if [ "$label" = python ]; then
    mkdir "$TMP/bridge" && cp "$HERE/../bridge/server.py" "$TMP/bridge/" && cp "$TMP/config.local.json" "$TMP/bridge/"
    (cd "$TMP" && MONITOR_BIND=127.0.0.1 exec $PIN_SRV "$PYTHON" bridge/server.py) >"$TMP/server.log" 2>&1 &
  else
    (cd "$TMP" && MONITOR_BIND=127.0.0.1 exec $PIN_SRV "$BIN/goserver") >"$TMP/server.log" 2>&1 &
  fi
  SRV=$!
  for _ in $(seq 50); do curl -s -m 1 "$url/health" >/dev/null && break; sleep 0.1; done
  curl -s -m 1 "$url/health" >/dev/null || { echo "$label server did not start:"; cat "$TMP/server.log"; return 1; }
  echo "== $label ($MODE): parity"
  $PIN_LOAD "$BIN/parity.test" -test.run Parity -url "$url" -token "$token" || { echo "PARITY FAILED for $label: its numbers are not trustworthy"; return 1; }
  echo "== $label ($MODE): benchmark"
  $PIN_LOAD "$BIN/bench" -url "$url" -token "$token" -label "$label-$MODE" -out "results/$RUN-$label-$MODE.json" "$@" || return 1
  kill $SRV 2>/dev/null; wait $SRV 2>/dev/null; SRV=""; rm -rf "$TMP"; TMP=""
}

run python 18765 "$@" || exit 1
run go 18766 "$@" || exit 1
"$BIN/bench" -compare "results/$RUN-python-$MODE.json,results/$RUN-go-$MODE.json" -out "results/$RUN-compare-$MODE.md"
{
  printf '\n## Environment\n\n- mode: %s (cpus: %s)\n- python: %s\n- kernel: %s\n- parity table: passed on both servers before benchmarking\n' \
    "$MODE" "$(nproc)" "$("$PYTHON" --version)" "$(uname -r)"
} >> "results/$RUN-compare-$MODE.md"
