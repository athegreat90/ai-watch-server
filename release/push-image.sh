#!/usr/bin/env bash
set -euo pipefail
image="ghcr.io/${GITHUB_REPOSITORY,,}:$RELEASE_TAG"
printf '%s' "$GH_TOKEN" | docker login ghcr.io -u "$GITHUB_ACTOR" --password-stdin
error=$(mktemp)
trap 'rm -f "$error"' EXIT
if manifest=$(docker buildx imagetools inspect --raw "$image" 2>"$error"); then
    jq -e --arg sha "$TESTED_SHA" '
      .annotations["org.opencontainers.image.revision"] == $sha
      and ([.manifests[].platform | select(.os == "linux") | .architecture]
           | (index("amd64") != null and index("arm64") != null))' <<< "$manifest" >/dev/null
    echo "Reusing the image already published for this commit."
    exit 0
fi
if ! grep -Eiq 'manifest unknown|manifest_unknown|(^|: )[^ ]+: not found$' "$error"; then
    cat "$error" >&2
    exit 1
fi
docker buildx create --use
docker buildx build --platform linux/amd64,linux/arm64 \
    --annotation "index:org.opencontainers.image.revision=$TESTED_SHA" \
    --label "org.opencontainers.image.revision=$TESTED_SHA" \
    --label "org.opencontainers.image.source=https://github.com/${GITHUB_REPOSITORY}" \
    --tag "$image" --push .
