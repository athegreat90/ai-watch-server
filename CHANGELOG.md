# Changelog

## [Unreleased]

- Rewrite the server in Go (standard library only) with the same HTTP API, limits and wire behavior. Static binary in a distroless nonroot image for `linux/amd64` and `linux/arm64`; read-only root filesystem and dropped capabilities; native `healthcheck` and `init-config` subcommands; graceful SIGINT/SIGTERM shutdown; configurable timeouts.
- Add the standalone `ai-watch-check-api` checker (release assets for Windows, Linux and macOS, and inside the image) and a standalone deployment bundle with `install.sh` and `install.ps1`. No git, Go or Python needed on the target machine.
- Publish an immutable `vX.Y.Z` image tag and a `latest` tag for the newest stable release, both on the same multi-platform digest, only after CI (tests, race detector, security scans, image smoke tests and the SonarQube Quality Gate) succeeded.
- Release assets: deployment bundle, checker binaries and `SHA256SUMS`.
- CI: Go formatting, vet, race tests with coverage, govulncheck, gosec, Python/Go parity, SonarQube.
- The Python server stays only as the parity and benchmark reference.

## [1.1.0]

- Session `duration` now measures the current turn: it restarts when work resumes after done, idle or error.
- Add `finished_long` to each session row: true when a turn finished after 5 minutes or more.
- Add `waiting` (seconds since the session started needing attention) to session rows.
- Add `POST /api/limits` and a `limits` object in `/api/status` with the 5-hour and weekly usage of Claude Code, Codex and Antigravity, including `resets_in` seconds. Readings older than 15 minutes are dropped.
- Add a Linux deployment guide (`docs/DEPLOY-LINUX.md`) covering Docker, firewall and systemd.
- Documentation: add an API reference (`docs/API.md`) and `docs/MAINTAINING.md`; restructure the README with configuration, security model and troubleshooting.
- Update Trivy to 0.76.2 and add Zsh equivalents for the PowerShell scripts.

## [1.0.0]

- Initial standalone server release.
