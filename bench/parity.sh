#!/usr/bin/env bash
# Run the black-box parity suite against the production Go server and, as the compatibility
# reference, the original Python server. Both must pass the same table of requests.
# Needs go and python3; run from anywhere. Uses loopback ports 18765 and 18766.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
pids=()
trap 'kill "${pids[@]}" 2>/dev/null; wait 2>/dev/null; rm -rf "$work"' EXIT

go build -C "$root" -o "$work/ai-watch-server" ./cmd/ai-watch-server
go test -C "$root/bench" -c -o "$work/parity.test" ./goserver

token=$(head -c 30 /dev/urandom | base64 | tr -d '=+/\n' | cut -c1-32)
mkdir "$work/go" "$work/py"
printf '{"token":"%s","port":18765}' "$token" > "$work/go/config.local.json"
printf '{"token":"%s","port":18766}' "$token" > "$work/py/config.local.json"
cp "$root/bridge/server.py" "$work/py/server.py"

(cd "$work/go" && MONITOR_BIND=127.0.0.1 exec "$work/ai-watch-server" serve --config config.local.json) >"$work/go.log" 2>&1 &
pids+=($!)
(cd "$work/py" && MONITOR_BIND=127.0.0.1 exec python3 server.py) >"$work/py.log" 2>&1 &
pids+=($!)
for port in 18765 18766; do
    for _ in $(seq 100); do curl -fs -m 1 "http://127.0.0.1:$port/health" >/dev/null && break; sleep 0.1; done
    curl -fs -m 1 "http://127.0.0.1:$port/health" >/dev/null || { echo "server on $port did not start"; cat "$work"/*.log; exit 1; }
done

for target in "go 18765" "python 18766"; do
    set -- $target
    echo "== parity: $1"
    "$work/parity.test" -test.run Parity -url "http://127.0.0.1:$2" -token "$token"
done
echo "parity passed on both servers"
