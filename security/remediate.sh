#!/usr/bin/env bash
set -euo pipefail
report=$(mktemp)
baseline=$(mktemp)
for arch in amd64 arm64; do
  bash security/check-image.sh "$arch" >>"$report" 2>&1 || true
  test -s "security-results/$arch.json" || { tail -n 30 "$report"; exit 1; }
  python security/check_findings.py "security-results/$arch.json" >>"$baseline" || true
done
if [ -s "$baseline" ]; then
  jq -Rs '{title:"Daily container vulnerability findings",body:.}' "$baseline" >"$report"
  issue=$(gh issue list --state open --search '"Daily container vulnerability findings" in:title' --json number,title --jq '.[] | select(.title=="Daily container vulnerability findings") | .number' | head -1)
  if [ -n "$issue" ]; then gh issue edit "$issue" --body-file "$baseline"
  else gh issue create --title 'Daily container vulnerability findings' --body-file "$baseline"; fi
fi
python security/refresh_base.py
git diff --quiet -- Dockerfile .python-version && exit 0
mkdir -p security-results
for arch in amd64 arm64; do
  if ! bash security/check-image.sh "$arch"; then
    echo 'Candidate rejected by image smoke test or rescan.' >> "$GITHUB_STEP_SUMMARY"
    exit 1
  fi
done
echo 'candidate=true' >> "$GITHUB_OUTPUT"
