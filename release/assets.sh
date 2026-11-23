#!/usr/bin/env bash
# Build the versioned release assets into dist/: standalone ai-watch-check-api binaries for every
# supported platform, the standalone deployment bundle, and SHA256SUMS covering all of them.
# Usage: RELEASE_TAG=v1.2.3 bash release/assets.sh   (needs Go and zip or python3)
set -euo pipefail
tag=${RELEASE_TAG:?RELEASE_TAG is required}
root=$(cd "$(dirname "$0")/.." && pwd)
dist=${ASSETS_DIR:-$root/dist}
rm -rf "$dist" && mkdir -p "$dist"
cd "$root"

# Reproducible: no VCS stamping, no host paths, fixed timestamps in the archive.
for target in windows/amd64 linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
    os=${target%/*}; arch=${target#*/}
    ext=""; [ "$os" = windows ] && ext=.exe
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.version=$tag" \
        -o "$dist/ai-watch-check-api_${tag}_${os}_${arch}${ext}" ./cmd/ai-watch-check-api
done

# The bundle is the deploy/ai-watch-deploy directory, with the image tag pinned in its .env.example.
bundle=$(mktemp -d)
trap 'rm -rf "$bundle"' EXIT
cp -R deploy/ai-watch-deploy "$bundle/ai-watch-deploy"
echo "$tag" > "$bundle/ai-watch-deploy/VERSION"
find "$bundle" -exec touch -d '2000-01-01 00:00:00' {} +
if command -v zip >/dev/null 2>&1; then
    (cd "$bundle" && zip -qrX "$dist/ai-watch-deploy-$tag.zip" ai-watch-deploy)
else
    (cd "$bundle" && python3 -m zipfile -c "$dist/ai-watch-deploy-$tag.zip" ai-watch-deploy)
fi

(cd "$dist" && sha256sum -- * > SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS)
ls -l "$dist"
