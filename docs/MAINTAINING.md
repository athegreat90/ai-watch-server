# Maintaining

## Quick facts

| Topic | Rule |
|---|---|
| Language and layout | Go, standard library only: `cmd/` (two executables), `internal/` (packages). The Python server in `bridge/` is only the parity and benchmark reference. |
| Required checks | `go` (gofmt, vet, race tests, coverage), `static` (govulncheck, gosec), `legacy-parity`, `images` (amd64 and arm64: build, smoke test, Trivy), `sonar` (Quality Gate, see [SONARQUBE.md](SONARQUBE.md)) |
| Releases | Automatic: one new patch version per qualifying push to `main`. Minor and major versions: push the `vX.Y.0` tag yourself, automation continues from it. |
| Docker tags | Immutable `vX.Y.Z` plus `latest` for the newest stable release, same digest, only after every required check passed. |
| Documentation | Every Go function, type and package has a doc comment (enforced by `internal/doccheck` in `go test`). |

## CI (`.github/workflows/ci.yml`)

| Job | What it proves |
|---|---|
| `go` | `gofmt`, `go vet`, `go test -race` with coverage (`coverage.out` is uploaded for SonarQube), release tooling tests, toolchain pins agree (`security/check_version.py`) |
| `static` | `govulncheck` (known vulnerabilities) and `gosec` (security static analysis) |
| `legacy-parity` | The same request suite passes against the Go server and the original Python server (`bench/parity.sh`) |
| `images` | For each architecture: build, smoke test under the production restrictions, Trivy scan of the image tarball (`security/check-image.sh`) |
| `sonar` | SonarQube analysis and Quality Gate over the Tailscale tunnel. Never runs for pull requests. |
| `release-eligibility` | Marks pushes that changed shipped files (`release/automatic.py`, `SHIPPED`): `cmd/`, `internal/`, `deploy/`, `scripts/`, `go.mod`, `Dockerfile`, compose files. Docs, tests and CI changes do not release. |

Pull requests run everything except `sonar`: untrusted code gets no credentials and no tailnet access.

## Release and tagging

```
push to main -> CI (all required jobs) -> automatic-release.yml
   reserve vX.Y.Z -> image smoke tests -> assets (checker binaries, bundle, SHA256SUMS)
   -> release/push-image.sh (version tag, latest) -> GitHub release
```

`release/push-image.sh` is the only publisher. The manual path (`publish-image.yml`, for releases created in the GitHub UI) runs the same script after `release/require-ci.sh` confirms a successful CI run on `main` for the tagged commit. Both workflows share the concurrency group `publish-release`, so publications run one at a time.

| Case | Result |
|---|---|
| New stable version | Builds `linux/amd64` + `linux/arm64`, pushes `:vX.Y.Z`, then points `:latest` at the same manifest list (no second build) |
| Version tag already exists | Verified and reused. Never rebuilt or overwritten. A different commit fails the job. |
| Older stable version published later | Its version tag is created, `latest` is not moved (semantic comparison, not lexicographic) |
| Prerelease (`vX.Y.Z-rc.1`) | Version tag only; `latest` untouched |
| Failure after the version tag, before `latest` | The job fails. Rerun it: the version tag is reused and `latest` is retried. |
| Verification after every publication | Both digests are compared and both architectures must be present, otherwise the job fails |

Previous versions are never deleted from GHCR, so rollback is always possible. To check by hand:
`docker buildx imagetools inspect ghcr.io/athegreat90/ai-watch-server:latest --format '{{json .Manifest}}'` and the same for the version; the two `digest` values must match.

Release assets per version: `ai-watch-deploy-vX.Y.Z.zip`, `ai-watch-check-api_vX.Y.Z_{windows_amd64.exe,linux_amd64,linux_arm64,darwin_amd64,darwin_arm64}` and `SHA256SUMS`. Rerunning never replaces an asset; it only adds missing ones.

## Security automation

- Trivy (`HIGH,CRITICAL`, pinned by digest) scans both image architectures in CI and daily. Fixable findings fail the check; unfixed ones are tracked in the daily issue.
- `govulncheck` and `gosec` run in CI. `#nosec G304` is used only where the path is an operator-chosen CLI option.
- Daily remediation (`remediate.yml`) refreshes the digest-pinned `golang` and `distroless` base images (`security/refresh_base.py`), validates them with tests and both image checks, and opens a PR from `security/go-base`. It needs the repository variable `SECURITY_APP_ID` and the secret `SECURITY_APP_PRIVATE_KEY` (a GitHub App with Contents, Pull requests and Issues write access). Until they exist the daily run fails at the token step.
- Require passing checks and human review on `main` (repository settings; not enforced from code).

## Benchmarks and parity

`bash bench/parity.sh` runs the request suite against both servers. `bash bench/run-docker.sh` benchmarks Python, the experimental Go port and the production image under the production container limits; results and method are in [bench/REPORT.md](../bench/REPORT.md).

## Notes

`MEMORY.md` records maintainer decisions (not user documentation).
