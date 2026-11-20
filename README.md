# AI Watch server

Run `python bridge/init_config.py`, then set `MONITOR_HOST_BIND` in a local .env and set the matching private origin in bridge/config.local.json. Run `./scripts/deploy-server.ps1` in PowerShell for development. For releases, set `AI_WATCH_SERVER_IMAGE=ghcr.io/<owner>/ai-watch-server:v1.0.0` and use `docker compose -f compose.release.yaml up -d --wait`. Authenticate Docker to GHCR for private images. Roll back by selecting the previous tagged image.

GET /health is public. GET /api/status and GET /api/sessions require X-Monitor-Key. POST /api/events also requires the header and accepts agent, session_id, status and optional project, detail and timestamp fields. Use `python bridge/check_api.py` for a health and authentication smoke test. View logs with `docker compose logs --tail 100`. Keep the port private and use the same token in all clients.

## Security

CI scans images and Python source. Daily remediation validates pinned Python image updates before opening a PR. Set repository variable SECURITY_APP_ID and secret SECURITY_APP_PRIVATE_KEY using a GitHub App installed on this repository with Contents, Pull requests and Issues write permissions. Require passing checks and human review on main. Review findings without a patch in the daily tracking issue.

Related repositories: [hooks](https://github.com/athegreat90/ai-watch-hooks) [firmware](https://github.com/athegreat90/ai-watch-firmware)

## Zsh on Linux and macOS

Run `zsh scripts/deploy-server.zsh` to build, start, and check the server.
Run `zsh scripts/status.zsh` to check container and API health.
Set `AI_WATCH_PYTHON` to an interpreter path if needed.
