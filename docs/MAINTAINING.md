# Maintaining

## Releases

A successful code-changing push to main produces one new patch release. Reruns reuse its version. Documentation-only changes do not need a new version. See [GitHub Releases](https://github.com/athegreat90/ai-watch-server/releases).

## Security automation

CI scans images and Python source. Daily remediation validates pinned Python image updates before opening a PR. Set repository variable SECURITY_APP_ID and secret SECURITY_APP_PRIVATE_KEY using a GitHub App installed on this repository with Contents, Pull requests and Issues write permissions. Require passing checks and human review on main. Review findings without a patch in the daily tracking issue.

## Notes

`MEMORY.md` records maintainer decisions (not user documentation).
