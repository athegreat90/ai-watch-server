#!/usr/bin/env bash
# AI Watch Server standalone installer for Linux, Raspberry Pi and macOS (Git Bash/WSL on Windows work too).
# Needs only Docker with the Compose plugin: no git, Go or Python.
#
#   ./install.sh                         install or verify the newest stable release (image :latest)
#   ./install.sh --version v1.2.0        install a specific immutable version
#   ./install.sh --update                pull the current tag again and recreate the container
#   ./install.sh --rollback v1.1.0       go back to a previous version (without a version: the previously deployed image)
#   Options: --dir DIR  --bind IP  --port N  --no-pull  --no-check  --force  --repo owner/name
#
# An existing configuration (config.local.json) and .env are never overwritten. A running
# deployment is left untouched unless you pass --update, --version, --rollback or --force.
set -euo pipefail

repo=${AI_WATCH_REPO:-athegreat90/ai-watch-server}
dir=$(cd "$(dirname "$0")" && pwd)
version="" rollback="" update=0 force=0 pull=1 check=1 bind="" port=""

usage() { sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; }
die() { echo "error: $*" >&2; exit 1; }
note() { echo "==> $*"; }

while [ $# -gt 0 ]; do
    case $1 in
        --dir) dir=$2; shift ;;
        --version) version=$2; shift ;;
        --rollback) rollback=previous; if [ $# -gt 1 ] && [[ $2 == v* ]]; then rollback=$2; shift; fi ;;
        --update) update=1 ;;
        --bind) bind=$2; shift ;;
        --port) port=$2; shift ;;
        --repo) repo=$2; shift ;;
        --no-pull) pull=0 ;;
        --no-check) check=0 ;;
        --force) force=1 ;;
        -h | --help) usage; exit 0 ;;
        *) usage >&2; die "unknown option $1" ;;
    esac
    shift
done

image_repo=${AI_WATCH_IMAGE_REPO:-ghcr.io/${repo,,}}
release_api=${AI_WATCH_RELEASE_API:-https://api.github.com/repos/$repo}
release_base=${AI_WATCH_RELEASE_BASE:-https://github.com/$repo/releases/download}
tag_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'
[ -z "$version" ] || [ "$version" = latest ] || [[ $version =~ $tag_re ]] || die "--version must be 'latest' or vX.Y.Z"
[ "$rollback" = "" ] || [ "$rollback" = previous ] || [[ $rollback =~ $tag_re ]] || die "--rollback needs vX.Y.Z"
[ -z "$port" ] || [[ $port =~ ^[0-9]+$ ]] || die "--port must be a number"

command -v docker >/dev/null || die "Docker is not installed or not on PATH."
docker compose version >/dev/null 2>&1 || die "The Docker Compose plugin is required ('docker compose')."
docker info >/dev/null 2>&1 || die "The Docker daemon is not reachable (is it running, and do you have permission?)."

download() { # url dest
    if command -v curl >/dev/null; then curl -fsSL --retry 3 -o "$2" "$1"
    elif command -v wget >/dev/null; then wget -q -O "$2" "$1"
    else die "curl or wget is required to download release files."; fi
}
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# fetch_verified <tag> <asset> <dest>: download a release asset and check it against the release's SHA256SUMS.
fetch_verified() {
    local tag=$1 asset=$2 dest=$3 sums expected
    sums=$(mktemp)
    download "$release_base/$tag/SHA256SUMS" "$sums" || { rm -f "$sums"; return 1; }
    download "$release_base/$tag/$asset" "$dest" || { rm -f "$sums" "$dest"; return 1; }
    expected=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$sums")
    rm -f "$sums"
    [ -n "$expected" ] && [ "$(sha256 "$dest")" = "$expected" ] || { rm -f "$dest"; die "checksum mismatch for $asset: refusing to use it."; }
}

mkdir -p "$dir"
cd "$dir"
if [ ! -f compose.yaml ]; then
    # Installer run on its own (for example downloaded with curl): fetch and verify the bundle first.
    note "compose.yaml not found next to the installer; downloading the deployment bundle"
    if [ -n "$version" ] && [ "$version" != latest ]; then bundle_tag=$version
    else bundle_tag=$(download "$release_api/releases/latest" /dev/stdout | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1); fi
    [ -n "$bundle_tag" ] || die "could not determine the latest release; pass --version vX.Y.Z"
    zip=$(mktemp)
    fetch_verified "$bundle_tag" "ai-watch-deploy-$bundle_tag.zip" "$zip" || die "could not download the deployment bundle for $bundle_tag"
    extract=$(mktemp -d)
    if command -v unzip >/dev/null; then unzip -q "$zip" -d "$extract"; else tar -xf "$zip" -C "$extract"; fi
    cp -Rn "$extract"/ai-watch-deploy/. . && rm -rf "$zip" "$extract"
fi

[ -f .env ] || { [ -f .env.example ] && cp .env.example .env || : > .env; chmod 600 .env 2>/dev/null || true; }
env_get() { sed -n "s/^$1=//p" .env | tail -1; }
env_set() { # key value: replace an active line, else append
    local tmp; tmp=$(mktemp)
    if grep -q "^$1=" .env; then sed "s|^$1=.*|$1=$2|" .env > "$tmp"; else cat .env > "$tmp"; echo "$1=$2" >> "$tmp"; fi
    cat "$tmp" > .env; rm -f "$tmp"
}

# Explicit intent only: an existing .env keeps its image unless a version flag was given.
history=.deploy-history
if [ -n "$rollback" ]; then
    if [ "$rollback" = previous ]; then
        target=$(tail -1 "$history" 2>/dev/null | awk '{ print $2 }')
        [ -n "$target" ] || die "no previous deployment recorded in $history; pass --rollback vX.Y.Z"
    else target="$image_repo:$rollback"; fi
    env_set AI_WATCH_SERVER_IMAGE "$target"; explicit=1
elif [ -n "$version" ]; then
    env_set AI_WATCH_SERVER_IMAGE "$image_repo:$version"; explicit=1
else
    [ -n "$(env_get AI_WATCH_SERVER_IMAGE)" ] || env_set AI_WATCH_SERVER_IMAGE "$image_repo:latest"
    explicit=0
fi
[ -z "$bind" ] || { env_set MONITOR_HOST_BIND "$bind"; explicit=1; }
[ -z "$port" ] || { env_set MONITOR_HOST_PORT "$port"; explicit=1; }
case $(uname -s) in
    MINGW* | MSYS* | CYGWIN*) ;; # Docker Desktop maps file ownership itself
    *) [ -n "$(env_get PUID)" ] || { env_set PUID "$(id -u)"; env_set PGID "$(id -g)"; } ;;
esac
image=$(env_get AI_WATCH_SERVER_IMAGE)
host_bind=$(env_get MONITOR_HOST_BIND); host_bind=${host_bind:-127.0.0.1}
host_port=$(env_get MONITOR_HOST_PORT); host_port=${host_port:-8765}
check_host=$host_bind; [ "$check_host" = 0.0.0.0 ] && check_host=127.0.0.1

running=$(docker compose ps -q ai-watch-server 2>/dev/null | head -1 || true)
if [ -n "$running" ] && [ "$update" = 0 ] && [ "$explicit" = 0 ] && [ "$force" = 0 ]; then
    note "AI Watch Server is already running; leaving it untouched (use --update, --version or --rollback to change it)."
    skip_deploy=1
else
    skip_deploy=0
fi

if [ "$skip_deploy" = 0 ]; then
    if [ -n "$running" ]; then
        prev_id=$(docker inspect --format '{{.Image}}' "$running")
        prev_ref=$(docker inspect --format '{{.Config.Image}}' "$running")
        prev_digest=$(docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' "$prev_id" 2>/dev/null || true)
        echo "$(date -u +%FT%TZ) $prev_ref ${prev_digest:-unknown}" >> "$history"
    fi
    if [ "$pull" = 1 ]; then note "Pulling $image"; docker compose pull; fi
    if [ ! -f config.local.json ]; then
        note "Generating config.local.json with a new random token (the image's own init-config)"
        mount=$PWD; init_user=(--user "$(id -u):$(id -g)")
        if command -v cygpath >/dev/null 2>&1; then mount=$(cygpath -m "$PWD"); init_user=(); fi
        MSYS_NO_PATHCONV=1 docker run --rm "${init_user[@]}" --entrypoint /app/ai-watch-server \
            -v "$mount:/cfg" "$image" init-config --config /cfg/config.local.json
    else
        note "Keeping the existing config.local.json and its token"
    fi
    note "Starting the container"
    up=(docker compose up -d --wait --wait-timeout 90)
    [ "$force" = 0 ] || up+=(--force-recreate)
    "${up[@]}" || { docker compose logs --tail 30 ai-watch-server >&2 || true; die "the container did not become healthy"; }
fi

note "Waiting for the health endpoint on $check_host:$host_port"
for _ in $(seq 30); do
    curl -fsS -m 2 "http://$check_host:$host_port/health" >/dev/null 2>&1 && break
    sleep 1
done
curl -fsS -m 2 "http://$check_host:$host_port/health" >/dev/null || die "http://$check_host:$host_port/health did not answer"

cid=$(docker compose ps -q ai-watch-server | head -1)
image_id=$(docker inspect --format '{{.Image}}' "$cid")
resolved_version=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image_id" 2>/dev/null || true)
resolved_digest=$(docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' "$image_id" 2>/dev/null || true)

if [ "$check" = 1 ]; then
    note "Running the standalone API checker"
    tag=${resolved_version:-}; [[ $tag =~ $tag_re ]] || tag=""
    os=$(uname -s | tr '[:upper:]' '[:lower:]'); arch=$(uname -m); ext=""
    case $os in mingw* | msys* | cygwin*) os=windows; ext=.exe ;; esac
    case $arch in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; esac
    checker=bin/ai-watch-check-api$ext
    mkdir -p bin
    if [ -n "$tag" ] && { [ -x "$checker" ] && [ "$("$checker" --version)" = "$tag" ] || fetch_verified "$tag" "ai-watch-check-api_${tag}_${os}_${arch}$ext" "$checker"; }; then
        chmod +x "$checker"
        "$checker" --url "http://$check_host:$host_port" --token-file config.local.json
    else
        note "Standalone checker not available for this release; using the one inside the image"
        docker compose exec -T ai-watch-server /app/ai-watch-check-api --url http://127.0.0.1:8765 --token-file /app/config.local.json
    fi
fi

cat <<EOF

AI Watch Server is running.
  requested image : $image
  deployed version: ${resolved_version:-unknown}
  image digest    : ${resolved_digest:-not available (local image)}
  URL             : http://$host_bind:$host_port   (token: $dir/config.local.json)
  update          : ./install.sh --update       roll back: ./install.sh --rollback [vX.Y.Z]
EOF
if [ "$host_bind" = 127.0.0.1 ]; then
    echo "  Note: it listens on this machine only. For the watch/hooks on other machines run: ./install.sh --bind <this host's LAN IP>"
fi
