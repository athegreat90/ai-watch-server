#!/usr/bin/env zsh
emulate -L zsh
setopt errexit nounset pipefail
source "${0:A:h}/common.zsh"

cd -- "$repo_root"
"$python_command" bridge/init_config.py
docker compose config --quiet
docker compose build
docker compose up -d --wait --wait-timeout 90
docker compose ps
"$python_command" bridge/check_api.py
