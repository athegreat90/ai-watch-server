#!/usr/bin/env zsh
# Build the server from this checkout and start it. The image carries its own init-config and API
# checker, so neither Python nor Go is needed on the host.
emulate -L zsh
setopt errexit nounset pipefail
source "${0:A:h}/common.zsh"

cd -- "$repo_root"
docker compose config --quiet
docker compose build
if [[ ! -f bridge/config.local.json ]]; then
  docker run --rm --user "$(id -u):$(id -g)" --entrypoint /app/ai-watch-server \
    -v "$repo_root/bridge:/cfg" ai-monitor-server:local init-config --config /cfg/config.local.json
fi
docker compose up -d --wait --wait-timeout 90
docker compose ps
docker compose exec -T ai-monitor-server /app/ai-watch-check-api --url http://127.0.0.1:8765 --token-file /app/config.local.json
