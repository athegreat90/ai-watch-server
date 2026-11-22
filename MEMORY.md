# Project memory

Retain port 8765 and a read-only config mount. Default host bind is loopback and deployment uses the host private IPv4. State lives in memory. Python 3.15.2 uses a pinned official Debian Bookworm image index. The original deployment passed physical HTTP 200 and authentication checks. Validate each new image before cutover.

Docs decisions (2026-11-21): doc changes are folded into 1.1.0 (no new version). API detail lives in docs/API.md, maintainer and CI content in docs/MAINTAINING.md, and the README holds the quick start, configuration, security model and troubleshooting. Each repo keeps its own docs with no cross-repo files. Always record decisions here. `.env.example` was not verified against the README variable table.

Trivy 0.76.2 is pinned by multi-platform image digest. The Zsh scripts resolve the repo from the script location and support overriding the interpreter with AI_WATCH_PYTHON.
