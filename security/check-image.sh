#!/usr/bin/env bash
set -euo pipefail
arch=${1:?architecture required}
mkdir -p security-results
rm -f "security-results/$arch.json"
image="ai-watch-ci:$arch"
docker build --platform "linux/$arch" -t "$image" .
bash security/smoke.sh "$image" "$arch"
docker save "$image" -o "security-results/$arch.tar"
docker run --rm -v "$PWD/security-results:/scan" aquasec/trivy:0.69.3@sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c   image --input "/scan/$arch.tar" --scanners vuln --severity HIGH,CRITICAL   --format json --output "/scan/$arch.json"
python security/check_findings.py "security-results/$arch.json"
