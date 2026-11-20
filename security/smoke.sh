#!/usr/bin/env bash
set -euo pipefail
image=$1
arch=$2
fixture=$(mktemp)
chmod 644 "$fixture"
printf '%s' '{"token":"example-fixture-token-only-000"}' > "$fixture"
trap 'docker stop ai-watch-smoke >/dev/null 2>&1 || true; rm -f "$fixture"' EXIT
docker run --rm --platform "linux/$arch" -d --name ai-watch-smoke -p 127.0.0.1::8765 -e MONITOR_BIND=0.0.0.0   --mount "type=bind,source=$fixture,target=/app/config.local.json,readonly" "$image" >/dev/null
port=$(docker port ai-watch-smoke 8765/tcp | cut -d: -f2)
curl -fsS --retry 12 --retry-all-errors --retry-delay 1 "http://127.0.0.1:$port/health"
test "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/status")" = 401
curl -fsS -H 'X-Monitor-Key: example-fixture-token-only-000' "http://127.0.0.1:$port/api/status"
