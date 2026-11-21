# AI Watch server


## Deploy a published image

Clone this repository and run commands from its root. Install Docker with Compose and Python. Start the server before configuring the hooks or the watch.

```powershell
git clone https://github.com/athegreat90/ai-watch-server.git
cd ai-watch-server
python bridge/init_config.py
Copy-Item .env.example .env
```

In Zsh use `python3 bridge/init_config.py` and `cp .env.example .env` after cloning. Edit `.env` and set `MONITOR_HOST_BIND` to the Docker host's private LAN IPv4 address. Set `AI_WATCH_SERVER_IMAGE` to the release tag you want to install. Set `url` in ignored `bridge/config.local.json` to the same origin, for example `http://192.168.1.50:8765`. Leave the generated token in that file and share it only with your local hooks and watch configuration.

```sh
docker compose -f compose.release.yaml pull
docker compose -f compose.release.yaml up -d --wait
docker compose -f compose.release.yaml ps
docker compose -f compose.release.yaml logs --tail 100
```

Run `python bridge/check_api.py` (or `python3`) to verify health, rejection of anonymous requests, and authenticated reads. GET `/health` is public. `/api/status`, `/api/sessions`, and `/api/events` require the `X-Monitor-Key` header. Events are posted to `/api/events` with agent, session ID, and status.

To upgrade or roll back, change the tag in `.env` and repeat the pull and up commands. Keep the same host address and token. Sessions are in memory and clear on restart.

## Build from source

Run `./scripts/deploy-server.ps1` in PowerShell or `zsh scripts/deploy-server.zsh` in Zsh. Use the matching `status` script for a local build.

## Releases

A successful code-changing push to main produces one new patch release. Reruns reuse its version. See [GitHub Releases](https://github.com/athegreat90/ai-watch-server/releases).

## Security

CI scans images and Python source. Daily remediation validates pinned Python image updates before opening a PR. Set repository variable SECURITY_APP_ID and secret SECURITY_APP_PRIVATE_KEY using a GitHub App installed on this repository with Contents, Pull requests and Issues write permissions. Require passing checks and human review on main. Review findings without a patch in the daily tracking issue.


Related repositories: [hooks](https://github.com/athegreat90/ai-watch-hooks) | [firmware](https://github.com/athegreat90/ai-watch-firmware)
