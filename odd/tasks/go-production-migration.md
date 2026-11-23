# Go production migration (feat/bench-python-vs-go)
Objective: Go server replaces Python; standalone deploy; SonarQube gate; `vX.Y.Z` + `latest` GHCR tags (one digest). Branch: feat/bench-python-vs-go. Delivery: ask-on-risk, no push/merge/release without authorization. Runner: `go test -race ./...`.
Plan: C:\Users\alexa\.claude\plans\cosmic-juggling-sun-of-fe9d9997-e4c5-4107-8660-ea259202a39d.md

## Specs
S1 Preserve API: endpoints /health, /api/status, /api/sessions, POST /api/events, /api/event, /api/limits; header `X-Monitor-Key`; errors `{"error":"unauthorized"}`, `{"error":"not found"}`, `{"error":"body must be 1-4096 bytes"}`, `{"error":"invalid event"}`; limits 8765 / 40 sessions / 4096 B / 16 conns / TTL 3600 / 120 / 900 / long task 300.
S2 stdlib only; no mutable global state; injectable clock; graceful SIGINT/SIGTERM; configurable timeouts; slog without tokens/payloads.
S3 Every function/type/package has a Go doc comment (internal/doccheck enforces).
S4 Dockerfile: multi-stage, distroless static nonroot, CGO_ENABLED=0 -trimpath -ldflags="-s -w", amd64+arm64, health-check subcommand, compose limits kept.
S5 Stable release publishes immutable `vX.Y.Z` and `latest` = same index digest; never for prerelease/failed gates/older versions; idempotent.
S6 Standalone deploy (Docker run, compose bundle, install.sh/install.ps1) and standalone Go `ai-watch-check-api`; native `init-config`.
S7 SonarQube (Tailscale OIDC) gates release; manual Tailscale/Sonar steps given to user BEFORE the unit-8 commit.
S8 Benchmarks Python vs experimental Go vs production Go, real numbers only.

## Tasks
- [x] T1 server core (S1,S2,S3) inline
- [x] T2 init-config + healthcheck (S6) inline
- [x] T3 check-api (S6) inline
- [x] T4 Dockerfile + compose (S4) inline
- [x] T5 parity + bench (S8) inline
- [x] T6 release tooling (S5) inline
- [ ] T7 deploy bundle + installers (S6) inline
- [ ] T8 Sonar + CI (S7) inline, BLOCKED on user setup confirmation before commit
- [ ] T9 docs (all) inline

## Log
L1 (2026-11-23, user): full request in the /plan command args (Go migration, standalone Docker deploy, standalone check_api, SonarQube like minecraft-modsync, dual tags vX.Y.Z + latest). Corrections: "add comments to each function ... according to the Go comment standard"; "step-by-step of the tailscale part when you need it before the commit".
