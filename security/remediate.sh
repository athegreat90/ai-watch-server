#!/usr/bin/env bash
set -euo pipefail
report=$(mktemp)
baseline=$(mktemp)
for arch in amd64 arm64; do
  bash security/check-image.sh "$arch" >>"$report" 2>&1 || true
  test -s "security-results/$arch.json" || { tail -n 30 "$report"; exit 1; }
  python3 security/check_findings.py "security-results/$arch.json" >>"$baseline" || true
done
sort -u -o "$baseline" "$baseline"  # the amd64 and arm64 reports repeat most findings
if [ -s "$baseline" ]; then
  jq -Rs '{title:"Daily container vulnerability findings",body:.}' "$baseline" >"$report"
  bash security/upsert-issue.sh "$baseline"
fi
python3 security/refresh_base.py
git diff --quiet -- Dockerfile go.mod && exit 0
mkdir -p security-results
for arch in amd64 arm64; do
  if ! bash security/check-image.sh "$arch"; then
    echo 'Candidate rejected by image smoke test or rescan.' >> "$GITHUB_STEP_SUMMARY"
    exit 1
  fi
done
echo 'candidate=true' >> "$GITHUB_OUTPUT"
