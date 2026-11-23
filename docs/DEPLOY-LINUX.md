# Deploy on Linux, Raspberry Pi and Windows with Docker

You only need Docker with the Compose plugin. No git, Go or Python on the host. For the shortest path use the installer from the release bundle (see the [bundle README](../deploy/ai-watch-deploy/README.md)); this guide shows what it does and how to run each step by hand.

## 1. Prerequisites

| Host | Install |
|---|---|
| Linux (Debian, Ubuntu, Fedora, ...) | Docker Engine and the Compose plugin from [docs.docker.com/engine/install](https://docs.docker.com/engine/install/) |
| Raspberry Pi 4/5 (64-bit OS, `aarch64`) | Same as Linux. The `linux/arm64` image is pulled automatically. |
| Windows | Docker Desktop (WSL 2 backend). Run PowerShell 5.1 or 7. |

Check: `docker compose version` and `docker info`.

## 2. Install

```sh
mkdir ai-watch && cd ai-watch
# unzip ai-watch-deploy-vX.Y.Z.zip here (from the GitHub release), then:
./install.sh --bind 192.168.1.50      # your host's private LAN IPv4; omit to stay on loopback
```

Windows Docker Desktop:

```powershell
.\install.ps1 -Bind 192.168.1.50
```

Choose the version: no option installs the newest stable release (`:latest`); `--version v1.2.0` pins one immutable release.

## 3. By hand (what the installer does)

```sh
cp .env.example .env                 # set AI_WATCH_SERVER_IMAGE and MONITOR_HOST_BIND
docker compose pull
docker run --rm --user "$(id -u):$(id -g)" --entrypoint /app/ai-watch-server -v "$PWD:/cfg" \
  "$(grep ^AI_WATCH_SERVER_IMAGE= .env | cut -d= -f2-)" init-config --config /cfg/config.local.json
docker compose up -d --wait
```

| `.env` variable | Meaning |
|---|---|
| `AI_WATCH_SERVER_IMAGE` | `ghcr.io/athegreat90/ai-watch-server:latest` or `...:vX.Y.Z` |
| `MONITOR_HOST_BIND` | Host address to listen on (default `127.0.0.1`) |
| `MONITOR_HOST_PORT` | Host port (default `8765`) |
| `PUID` / `PGID` | `id -u` / `id -g` of the user that owns `config.local.json`. The container reads the private (0600) file as that user. |

`config.local.json` holds the `token` and `url`. Keep the token: the hooks and the watch need the same value.

## 4. Open the port to your LAN only

The port must not be reachable from the internet.

```sh
sudo ufw allow from 192.168.1.0/24 to any port 8765 proto tcp
sudo ufw status
```

With firewalld: `sudo firewall-cmd --permanent --add-rich-rule='rule family=ipv4 source address=192.168.1.0/24 port port=8765 protocol=tcp accept' && sudo firewall-cmd --reload`. On Windows allow the port for the Private profile only. Docker publishes ports in its own chains: bind to the LAN address (`MONITOR_HOST_BIND`) rather than relying on the host firewall alone.

## 5. Verify

```sh
docker compose ps
docker compose exec -T ai-watch-server /app/ai-watch-check-api --url http://127.0.0.1:8765 --token-file /app/config.local.json
```

Or from another machine with the standalone checker: `ai-watch-check-api --url http://192.168.1.50:8765 --token-file ./config.local.json`.

## 6. Operate

| Task | Command |
|---|---|
| Logs | `docker compose logs --tail 100 ai-watch-server` |
| Restart | `docker compose restart ai-watch-server` |
| Update to the newest `latest` | `./install.sh --update` (or `docker compose pull && docker compose up -d --wait`) |
| Pin or roll back | `./install.sh --version v1.2.0` / `./install.sh --rollback v1.1.0` |
| Stop | `docker compose down` |
| Which version and digest | `docker image inspect "$(grep ^AI_WATCH_SERVER_IMAGE= .env | cut -d= -f2-)" --format '{{index .RepoDigests 0}}'` |

`latest` is a mutable tag and a running container never updates by itself. Rolling back one deployment does not move `latest` in the registry. Sessions are in memory and clear on restart.

## Alternative: run the binary without Docker (advanced)

Build with Go on any machine (`go build -o ai-watch-server ./cmd/ai-watch-server`, `GOARCH=arm64` for a Pi) and run `./ai-watch-server init-config --config /etc/ai-watch/config.local.json`, then a systemd unit such as:

```ini
[Unit]
Description=AI Watch server
After=network-online.target

[Service]
User=aiwatch
Environment=MONITOR_BIND=192.168.1.50
Environment=AI_WATCH_CONFIG=/etc/ai-watch/config.local.json
ExecStart=/usr/local/bin/ai-watch-server serve
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

The port comes from `port` in the config file (default `8765`).

## Troubleshooting

| Symptom | Fix |
|---|---|
| Container restarts, log says `cannot read config.local.json` | Set `PUID` and `PGID` in `.env` to the owner of the file, or fix its ownership. |
| Log says `missing config.local.json` | Run the `init-config` command from step 3 in the folder that holds `compose.yaml`. |
| Watch shows "Backend: check token" or hooks get HTTP 401 | The token differs. Copy it from `config.local.json` to the hooks' `config.local.json` and the watch's `config.h`. |
| Works on the host, not from the watch | `MONITOR_HOST_BIND` is still `127.0.0.1`, or the firewall blocks the port. |
| `pull access denied` | The image name or tag is wrong; use a version from the Releases page. |
