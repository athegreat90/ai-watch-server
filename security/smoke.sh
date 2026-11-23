#!/usr/bin/env bash
# Smoke test of a built image under the same restrictions as production: non-root, read-only
# root filesystem, no capabilities, no-new-privileges, token file mounted read-only.
set -euo pipefail
export MSYS_NO_PATHCONV=1 # Git Bash on Windows must not rewrite container paths
image=$1
arch=$2
token=example-fixture-token-only-000
fixture=$(mktemp)
chmod 644 "$fixture"
printf '{"token":"%s"}' "$token" > "$fixture"
source=$fixture
# Git Bash on Windows: Docker Desktop needs a Windows-style path for the bind mount source.
if command -v cygpath >/dev/null 2>&1; then source=$(cygpath -m "$fixture"); fi
trap 'docker stop ai-watch-smoke >/dev/null 2>&1 || true; rm -f "$fixture"' EXIT
docker run --rm --platform "linux/$arch" -d --name ai-watch-smoke -p 127.0.0.1::8765 -e MONITOR_BIND=0.0.0.0 \
    --user 1000:1000 --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --mount "type=bind,source=$source,target=/app/config.local.json,readonly" "$image" >/dev/null
port=$(docker port ai-watch-smoke 8765/tcp | awk -F: 'NR == 1 { print $NF }')
curl -fsS --retry 12 --retry-all-errors --retry-delay 1 "http://127.0.0.1:$port/health"
echo
test "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/status")" = 401
curl -fsS -H "X-Monitor-Key: $token" "http://127.0.0.1:$port/api/status"
echo
# The image has no shell or curl, so its own health check is the binary's subcommand.
docker exec ai-watch-smoke /app/ai-watch-server healthcheck
echo "smoke ok ($arch)"
