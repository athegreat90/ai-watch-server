# Project memory

Retain port 8765 and a read-only config mount at `/app/config.local.json`. Default host bind is loopback and deployment uses the host private IPv4. State lives in memory. The server is Go (module `github.com/athegreat90/ai-watch-server`), built with `CGO_ENABLED=0` into a distroless static nonroot image; both base images are pinned by digest and refreshed by `security/refresh_base.py`. The Python server in `bridge/` is only the parity and benchmark reference. The container runs as UID 1000 so the 0600 token file stays private yet readable.

Releases (decided 2026-11-23): the automatic release and the manual `publish-image.yml` path share `release/push-image.sh` and one concurrency group. Version tags are immutable; `latest` is a second tag on the same manifest list, moved only for the highest published stable version and never for prereleases. Releases are gated on CI, which includes tests with the race detector, security scans, both image architectures and the SonarQube Quality Gate. Rollback never moves `latest`.

The API checker (`ai-watch-check-api`) is shipped as release assets and inside the image; it is read-only and takes the token from a file or `AI_WATCH_TOKEN`, never argv. Every function carries a Go doc comment, enforced by `internal/doccheck`. Docs decisions: API detail lives in docs/API.md, maintainer and CI content in docs/MAINTAINING.md, standalone deployment in deploy/ai-watch-deploy/README.md. Always record decisions here.

Trivy 0.76.2 is pinned by multi-platform image digest.
