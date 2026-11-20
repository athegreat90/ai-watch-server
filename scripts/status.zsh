#!/usr/bin/env zsh
emulate -L zsh
setopt errexit nounset pipefail
source "${0:A:h}/common.zsh"

cd -- "$repo_root"
docker compose ps
"$python_command" bridge/check_api.py
