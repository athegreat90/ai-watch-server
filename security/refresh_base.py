"""Refresh the digest-pinned base images: newest Go patch of the pinned minor series, newest distroless digest."""
import json
import re
import urllib.request
from pathlib import Path

REQUIRED = {("linux", "amd64"), ("linux", "arm64")}
ACCEPT = ("application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json")


def fetch(url, **headers):
    return urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30)


def platforms(index):
    return {(row.get("platform", {}).get("os"), row.get("platform", {}).get("architecture"))
            for row in index["manifests"]}


def manifest(host, repo, tag, token=None):
    """Return (index, digest) of a tag on a registry; raise when a required architecture is missing."""
    headers = {"Accept": ACCEPT}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with fetch(f"https://{host}/v2/{repo}/manifests/{tag}", **headers) as response:
        index, digest = json.load(response), response.headers["Docker-Content-Digest"]
    if not REQUIRED <= platforms(index):
        raise SystemExit(f"{repo}:{tag} lacks a required architecture.")
    return index, digest


def hub_token(repo):
    with fetch(f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{repo}:pull") as response:
        return json.load(response)["token"]


def newest_go(current):
    series = current.rsplit(".", 1)[0]
    with fetch(f"https://hub.docker.com/v2/repositories/library/golang/tags?page_size=100&name={series}.") as response:
        names = [row["name"] for row in json.load(response)["results"]]
    versions = [n.removesuffix("-bookworm") for n in names if re.fullmatch(re.escape(series) + r"\.\d+-bookworm", n)]
    key = lambda value: tuple(map(int, value.split(".")))  # noqa: E731
    return max(versions + [current], key=key)


dockerfile = Path("Dockerfile")
source = dockerfile.read_text()
builder = re.search(r"^FROM --platform=\$BUILDPLATFORM golang:(\d+\.\d+\.\d+)-bookworm@sha256:[0-9a-f]{64} AS build$",
                    source, re.M)
runtime = re.search(r"^FROM gcr\.io/distroless/static-debian13:nonroot@sha256:[0-9a-f]{64}$", source, re.M)
version = newest_go(builder[1])
_, go_digest = manifest("registry-1.docker.io", "library/golang", f"{version}-bookworm", hub_token("library/golang"))
_, static_digest = manifest("gcr.io", "distroless/static-debian13", "nonroot")
source = source.replace(builder[0], f"FROM --platform=$BUILDPLATFORM golang:{version}-bookworm@{go_digest} AS build")
source = source.replace(runtime[0], f"FROM gcr.io/distroless/static-debian13:nonroot@{static_digest}")
dockerfile.write_text(source, newline="\n")
go_mod = Path("go.mod")
go_mod.write_text(re.sub(r"^go \d+\.\d+\.\d+$", f"go {version}", go_mod.read_text(), flags=re.M), newline="\n")
