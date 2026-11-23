#!/usr/bin/env zsh
emulate -L zsh
setopt errexit nounset pipefail
source "${0:A:h}/common.zsh"

cd -- "$repo_root"
docker compose ps
docker compose exec -T ai-monitor-server /app/ai-watch-check-api --url http://127.0.0.1:8765 --token-file /app/config.local.json
