# AI Watch Server: standalone deployment

Everything here runs with **Docker only**. No git clone, Go, Python or source build is needed on the
target machine. The server is one static binary in a ~27 MB distroless image for `linux/amd64` and
`linux/arm64` (PC, server, Raspberry Pi 4/5, Docker Desktop).

Image: `ghcr.io/athegreat90/ai-watch-server`

| Tag | Meaning |
|---|---|
| `:vX.Y.Z` | One immutable release. It never changes. Use this for reproducible installs and rollbacks. |
| `:latest` | The newest **stable** release. A mutable tag: it moves when a newer stable release is published, **but a running container never changes by itself**. You must pull and recreate it. |

Both tags of a release point to the same multi-platform image (same digest). Prereleases never move `latest`.
Replace `v1.2.0` below with a version from the [Releases page](https://github.com/athegreat90/ai-watch-server/releases).

## Method C: installer (recommended)

Download the bundle `ai-watch-deploy-vX.Y.Z.zip` from the release, unzip it, and run the installer in that folder.
It checks Docker, creates `config.local.json` with a random token, starts the container, waits for real health,
runs the API checker and prints the deployed version and digest.

```sh
# Linux, macOS, Raspberry Pi (ARM64), WSL, Git Bash
./install.sh                       # newest stable release (:latest)
./install.sh --version v1.2.0      # a specific immutable version
./install.sh --bind 192.168.1.50   # listen on the LAN IP so the watch and hooks can reach it
./install.sh --update              # pull the current tag again and recreate the container
./install.sh --rollback            # back to the previously deployed image (or: --rollback v1.1.0)
```

```powershell
# Windows Docker Desktop (Windows PowerShell 5.1 or PowerShell 7)
.\install.ps1
.\install.ps1 -Version v1.2.0
.\install.ps1 -Bind 192.168.1.50
.\install.ps1 -Update
.\install.ps1 -Rollback previous      # or -Rollback v1.1.0
```

- Safe to rerun: an existing `config.local.json` and `.env` are **never** overwritten, and a running deployment is
  left alone unless you pass `--update`, `--version`, `--rollback` or `--force`.
- Other options: `--dir`, `--port`, `--no-pull`, `--no-check`, `--repo owner/name` (`-Dir -Port -NoPull -NoCheck -Repo` on Windows).
- If you started with `latest`, the summary shows both the requested image (`...:latest`) and what it resolved to
  (version and digest).
- The bundle can also be fetched by the installer alone: place just `install.sh` in an empty folder and run it; it
  downloads and verifies the bundle from the release.
- Every download is checked against the release's `SHA256SUMS`. Previous deployments are logged in `.deploy-history`.

## Method B: Docker Compose

```sh
cp .env.example .env          # choose the image and bind address
docker compose pull
# create config.local.json once (same command as in Method A, run in this folder)
docker run --rm --user "$(id -u):$(id -g)" --entrypoint /app/ai-watch-server -v "$PWD:/cfg" \
  "$(grep ^AI_WATCH_SERVER_IMAGE= .env | cut -d= -f2-)" init-config --config /cfg/config.local.json
docker compose up -d --wait
```

`.env` selects the image. Track the newest stable release:

```env
AI_WATCH_SERVER_IMAGE=ghcr.io/athegreat90/ai-watch-server:latest
```

or pin one immutable version:

```env
AI_WATCH_SERVER_IMAGE=ghcr.io/athegreat90/ai-watch-server:v1.2.0
```

Update or roll back by changing that line, then `docker compose pull && docker compose up -d --wait`.
`MONITOR_HOST_BIND` defaults to `127.0.0.1` (private). Set it to the host's LAN IP to serve other devices.

## Method A: plain `docker run`

```sh
docker pull ghcr.io/athegreat90/ai-watch-server:latest      # or :v1.2.0

# Create the config once (random 256-bit token, mode 0600, never overwrites)
docker run --rm --user "$(id -u):$(id -g)" --entrypoint /app/ai-watch-server -v "$PWD:/cfg" \
  ghcr.io/athegreat90/ai-watch-server:latest init-config --config /cfg/config.local.json

docker run -d --name ai-watch-server --restart unless-stopped \
  --user "$(id -u):$(id -g)" --read-only --tmpfs /tmp:rw,nosuid,nodev,size=16m \
  --cap-drop ALL --security-opt no-new-privileges:true --cpus 0.5 --memory 128m --pids-limit 64 \
  -e MONITOR_BIND=0.0.0.0 -p 127.0.0.1:8765:8765 \
  -v "$PWD/config.local.json:/app/config.local.json:ro" \
  ghcr.io/athegreat90/ai-watch-server:latest
```

`--user` must be the owner of `config.local.json` so the file can stay private (mode 0600). On Docker Desktop
for Windows omit `--user`; use `-p 192.168.1.50:8765:8765` to serve the LAN instead of only this machine.

## Verify the deployment

```sh
# read-only checks of health, authentication and response shape (the checker is also inside the image)
docker exec ai-watch-server /app/ai-watch-check-api --url http://127.0.0.1:8765 --token-file /app/config.local.json
# or the standalone release binary, from any machine that can reach the server
ai-watch-check-api --url http://192.168.1.50:8765 --token-file ./config.local.json
```

The token can also come from the `AI_WATCH_TOKEN` environment variable; it is never accepted as a command-line
argument. Exit codes: `0` ok, `1` a check failed, `2` usage error, `3` server unreachable. Add `--json` for automation.

## Updating and rolling back

`latest` does not update a running container. To move to the newest stable release run `./install.sh --update`
(or `docker compose pull && docker compose up -d --wait`). To go back, select a previous immutable version:
`./install.sh --rollback v1.1.0`. All previous versions stay available in the registry; rolling back one
deployment never moves the `latest` tag.

## Which version am I running? Is `latest` the newest?

```sh
docker image inspect ghcr.io/athegreat90/ai-watch-server:latest --format '{{index .RepoDigests 0}}'
docker inspect ai-watch-server --format '{{.Config.Image}}'
# compare the registry's two tags: the manifest digests must be identical
docker buildx imagetools inspect ghcr.io/athegreat90/ai-watch-server:latest   --format '{{json .Manifest}}'
docker buildx imagetools inspect ghcr.io/athegreat90/ai-watch-server:v1.2.0   --format '{{json .Manifest}}'
```

`latest` equals the highest published stable version when both commands print the same `digest`.

## Security defaults

Non-root user, read-only root filesystem, all Linux capabilities dropped, `no-new-privileges`, 128 MB / 0.5 CPU /
64 process limits, port bound to `127.0.0.1` unless you choose otherwise, token file mounted read-only. Use it
only on a trusted private network; the API has no TLS.
