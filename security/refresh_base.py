"""Select the newest stable patch in the pinned Python series and resolve its image index."""
import json
import re
import urllib.request
from pathlib import Path

dockerfile = Path("Dockerfile")
source = dockerfile.read_text()
current = re.search(r"^FROM python:(\d+\.\d+\.\d+)-slim-bookworm@sha256:[0-9a-f]{64}$", source, re.M)
series = current[1].rsplit(".", 1)[0]
tag = f"{series}-slim-bookworm"
def fetch(url, **headers):
    return urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30)

with fetch("https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/python:pull") as response:
    token = json.load(response)["token"]
def manifest(tag):
    with fetch("https://registry-1.docker.io/v2/library/python/manifests/" + tag,
               Authorization=f"Bearer {token}",
               Accept="application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json") as response:
        return json.load(response), response.headers["Docker-Content-Digest"]
with fetch(f"https://hub.docker.com/v2/repositories/library/python/tags/{tag}") as response:
    digest = json.load(response)["digest"]
with fetch(f"https://hub.docker.com/v2/repositories/library/python/tags?page_size=100&name={series}") as response:
    listing = json.load(response)["results"]
versions = [row["name"].removesuffix("-slim-bookworm") for row in listing
            if row.get("digest") == digest and re.fullmatch(re.escape(series) + r"\.\d+-slim-bookworm", row["name"])]
version = max(versions, key=lambda value: tuple(map(int, value.split(".")))) if versions else current[1]
if tuple(map(int, version.split("."))) < tuple(map(int, current[1].split("."))):
    version = current[1]
image, digest = manifest(f"{version}-slim-bookworm")
platforms = {(row.get("platform", {}).get("os"), row.get("platform", {}).get("architecture")) for row in image["manifests"]}
if not {("linux", "amd64"), ("linux", "arm64")} <= platforms:
    raise SystemExit("Candidate lacks a required architecture.")
dockerfile.write_text(source.replace(current[0], f"FROM python:{version}-slim-bookworm@{digest}"))
Path(".python-version").write_text(version + "\n")
