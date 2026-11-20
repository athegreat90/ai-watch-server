#!/usr/bin/env bash
set -euo pipefail
arch=${1:?architecture required}
mkdir -p security-results
rm -f "security-results/$arch.json"
image="ai-watch-ci:$arch"
docker build --platform "linux/$arch" -t "$image" .
bash security/smoke.sh "$image" "$arch"
docker save "$image" -o "security-results/$arch.tar"
docker run --rm -v "$PWD/security-results:/scan" aquasec/trivy:0.76.2@sha256:8f6bb947dbc528902e11f2f5be52f7b519f6a39d5d148f41166f2040499768fb   image --input "/scan/$arch.tar" --scanners vuln --severity HIGH,CRITICAL   --format json --output "/scan/$arch.json"
python security/check_findings.py "security-results/$arch.json"
