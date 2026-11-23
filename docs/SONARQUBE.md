# SonarQube

Code quality and security analysis for this repository, modeled on the setup of the Minecraft ModSync project. A passing SonarQube Quality Gate is a **required check for publishing**: neither the `vX.Y.Z` image tag nor `latest` is published from a commit whose analysis failed.

## How it works

| Item | Value |
|---|---|
| Server | `https://sonarqube.alexandermora.de` (reachable only inside the tailnet) |
| Project key | `athegreat90_ai-watch-server` |
| Runs on | Pushes to `main` and manual runs of `CI` on `main`. Never on pull requests. |
| Network | GitHub-hosted runner joins the tailnet as an ephemeral `tag:ci` node using OIDC workload identity federation. No long-lived Tailscale key. SonarQube is not exposed publicly; TLS verification is never disabled. |
| Inputs | `coverage.out` from the `go` job (`go test -race -covermode=atomic`), `sonar-project.properties` |
| Gate | `sonar.qualitygate.wait=true`, `sonar.qualitygate.timeout=300`. A failed gate fails the `sonar` job, so CI fails and releases are blocked. |

`bench/**`, `bridge/**` and `tests/**` (reference and benchmark code) are excluded from analysis and coverage; tests under `cmd` and `internal` are classified as tests.

## One-time setup (needs admin access)

1. **SonarQube project.** Create `athegreat90_ai-watch-server` (manually, "Other CI"). Generate a project analysis token. Never put it in a file or on a command line.
2. **Quality Gate.** Create or assign a gate whose conditions apply to new code: bugs = 0, vulnerabilities = 0, code smells = 0, security hotspots reviewed = 100%, coverage >= 80%, duplicated lines <= 3%, reliability, security and maintainability ratings = A. A gate without conditions always passes and enforces nothing.
3. **Tailscale ACL** (Admin console, Access controls). Add `"tagOwners": {"tag:ci": ["autogroup:admin"]}` and a grant `{"src": ["tag:ci"], "dst": ["<SonarQube tailnet IP>"], "ip": ["tcp:9000"]}`. An existing identical grant (for example from ModSync) is enough.
4. **Tailscale trust credential** (Settings, Trust credentials, Credential, OpenID Connect). Issuer: GitHub. Subject: `repo:athegreat90@6455700/ai-watch-server@1465961918:ref:refs/heads/main`. This repository uses GitHub's immutable subject format (owner and repository IDs are part of the subject); the short form `repo:athegreat90/ai-watch-server:ref:refs/heads/main` is rejected with HTTP 403. Check the format with `gh api repos/athegreat90/ai-watch-server/actions/oidc/customization/sub`. Scope: Auth Keys, Write. Tag: `tag:ci`. Create a new credential for this repository; the ModSync one is bound to another repository. Copy the Client ID and the Audience (neither is secret).
5. **GitHub secrets and variable** (repository `athegreat90/ai-watch-server`):
   ```sh
   gh secret set TS_OAUTH_CLIENT_ID -R athegreat90/ai-watch-server
   gh secret set TS_AUDIENCE        -R athegreat90/ai-watch-server
   gh secret set SONAR_TOKEN        -R athegreat90/ai-watch-server   # paste at the prompt
   gh variable set SONAR_HOST_URL   -R athegreat90/ai-watch-server --body "<URL the runner can reach>"
   ```
6. **Host URL.** The job runs with `--accept-dns=false`, so the public DNS name may not reach the tailnet. Use the SonarQube tailnet IP over plain HTTP (it only travels inside the WireGuard tunnel), for example `http://100.x.y.z:9000`, unless the hostname resolves to that IP from public DNS.
7. **First run.** Push to `main`, or run `CI` manually from `main` (other refs fail the OIDC subject by design). The job prints `tailscale status`, checks `$SONAR_HOST_URL/api/system/status`, then scans. Expect 10 to 18 minutes.

Until steps 1 to 6 are done the `sonar` job fails and **automatic releases are paused**. That is intended: nothing is published without a passing gate.

## Local scan

```sh
go test -race -covermode=atomic -coverprofile=coverage.out ./...
SONAR_TOKEN=<token> sonar-scanner -Dsonar.host.url=https://sonarqube.alexandermora.de
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Tailscale step fails with `token exchange failed with status 403` | The credential subject does not match the OIDC token. Use the immutable subject from step 4. Then check the audience, the Client ID secret and that the credential allows `tag:ci` |
| Tailscale step fails to authenticate | The credential subject must match the running ref exactly (`refs/heads/main`); `tag:ci` must exist in the ACL; the credential needs `auth_keys` write |
| `curl` to `/api/system/status` times out | The ACL grant is missing or wrong, or the SonarQube node is offline |
| Analysis passes but nothing is enforced | The assigned Quality Gate has no conditions (step 2) |
| Coverage is 0% | `coverage.out` was not produced, or the path in `sonar-project.properties` is wrong |
| Gate fails on new code only | Expected: fix the new issues, or review the hotspots in the SonarQube UI |
