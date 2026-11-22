# Deploy the AI Watch server on Linux

Tested layout: a Debian/Ubuntu host on your private LAN (a Raspberry Pi, a mini PC or a VM). The server keeps sessions in memory and has no TLS, so it must only be reachable from a network you trust.

## 1. Prerequisites

- Docker Engine with the Compose plugin: `docker --version` and `docker compose version`
- `git` and Python 3 (only to generate the token and run the health check)
- The host's LAN IPv4 address: `ip -4 addr show | grep inet`

## 2. Get the code and create the token

```sh
git clone https://github.com/athegreat90/ai-watch-server.git
cd ai-watch-server
python3 bridge/init_config.py      # creates bridge/config.local.json (mode 0600) with a random token
cp .env.example .env
```

Edit `.env`:

| Variable | Value |
|---|---|
| `MONITOR_HOST_BIND` | The host's LAN IPv4, for example `192.168.1.50`. Never `0.0.0.0` on a host with a public interface. |
| `AI_WATCH_SERVER_IMAGE` | The release tag to run, for example `ghcr.io/athegreat90/ai-watch-server:v1.1.0`. |
| `MONITOR_HOST_PORT` | Optional, defaults to `8765`. |
| `PUID` / `PGID` | Optional, default `1000`. Set them to `id -u` / `id -g` of the user that owns `bridge/config.local.json`, because the container reads it as that user. |

Edit `bridge/config.local.json` and set `url` to the same origin, for example `http://192.168.1.50:8765`. Keep the generated `token`: the hooks and the watch need the same value.

## 3. Start it

```sh
docker compose -f compose.release.yaml pull
docker compose -f compose.release.yaml up -d --wait
docker compose -f compose.release.yaml ps
python3 bridge/check_api.py
curl -s http://192.168.1.50:8765/health      # {"ok":true}
```

`restart: unless-stopped` is already set in `compose.release.yaml`, so the container comes back after a reboot as long as the Docker service is enabled: `sudo systemctl enable docker`.

## 4. Open the port to your LAN only

```sh
sudo ufw allow from 192.168.1.0/24 to any port 8765 proto tcp
sudo ufw status
```

Use your own subnet. Do not forward this port on your router.

## 5. Verify the API (see [API reference](API.md))

```sh
TOKEN=$(python3 -c "import json;print(json.load(open('bridge/config.local.json'))['token'])")
curl -s -H "X-Monitor-Key: $TOKEN" http://192.168.1.50:8765/api/status
```

The response includes `sessions` (each with `duration` per turn and `finished_long`) and `limits` (5-hour and weekly usage per agent, once the hooks report it).

## 6. Operate

```sh
docker compose -f compose.release.yaml logs --tail 100 -f   # logs
# upgrade or roll back: change AI_WATCH_SERVER_IMAGE in .env, then
docker compose -f compose.release.yaml pull && docker compose -f compose.release.yaml up -d --wait
```

Sessions and limits are held in memory and clear on restart. The next agent event repopulates them.

## Alternative: run without Docker (systemd)

Use the Python version in `.python-version`, the same one the image runs.

```sh
sudo useradd --system --home /opt/ai-watch-server --shell /usr/sbin/nologin aiwatch
sudo git clone https://github.com/athegreat90/ai-watch-server.git /opt/ai-watch-server
sudo -u aiwatch python3 /opt/ai-watch-server/bridge/init_config.py
```

Create `/etc/systemd/system/ai-watch-server.service` (replace the bind address):

```ini
[Unit]
Description=AI Watch server
After=network-online.target
Wants=network-online.target

[Service]
User=aiwatch
WorkingDirectory=/opt/ai-watch-server
Environment=MONITOR_BIND=192.168.1.50
ExecStart=/usr/bin/python3 /opt/ai-watch-server/bridge/server.py
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now ai-watch-server
journalctl -u ai-watch-server -f
```

The port comes from `port` in `bridge/config.local.json` (default `8765`).

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Watch shows "Backend: check token" or hooks get HTTP 401 | The token differs. Copy it from `bridge/config.local.json` to the hooks' `config.local.json` and the watch's `config.h`. |
| Watch shows "Backend offline" | Wrong `MONITOR_HOST_BIND`, firewall rule, or the watch is on a different subnet. Test `curl http://<host>:8765/health` from another LAN machine. |
| Container restarts with a permission error on `config.local.json` | Set `PUID` and `PGID` in `.env` to the file owner's ids. |
| `/api/status` has an empty `limits` | The hooks have not reported yet, or the reading is older than 15 minutes. Run a short Claude Code or Codex turn. |
| Sessions vanish after a reboot | Expected: state is in memory. |
