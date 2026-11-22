# Changelog

## [Unreleased]

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
