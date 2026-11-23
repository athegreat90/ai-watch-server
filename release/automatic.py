"""Reserve one stable version per tested commit, decide the `latest` image tag and publish the release."""
import json
import os
import re
import subprocess
import sys
from pathlib import Path

VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")
# Stable tags only match VERSION; manual prereleases look like v1.2.0-rc.1.
RELEASE_TAG = re.compile(VERSION.pattern + r"(-[0-9A-Za-z.-]+)?")
SHIPPED = ("cmd/", "internal/", "deploy/", "scripts/", "go.mod", "go.sum", "Dockerfile",
           "compose.yaml", "compose.release.yaml", ".dockerignore")


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def version(tag):
    return tuple(map(int, VERSION.fullmatch(tag).groups()))


def qualifies(paths):
    return any(path.startswith(prefix) if prefix.endswith("/") else path == prefix
               for path in paths for prefix in SHIPPED
               if not path.lower().endswith((".md", ".zip")) and not path.endswith("_test.go"))


def choose_tag(tags, sha):
    reserved = sorted((tag for tag, commit in tags.items() if commit == sha), key=version)
    if reserved:
        if len(reserved) != 1:
            raise SystemExit("Multiple stable tags already point to this commit.")
        return reserved[0]
    major, minor, patch = max(map(version, tags), default=(1, 0, 0))
    return f"v{major}.{minor}.{patch + 1}"


def should_update_latest(tag, published):
    """`latest` follows the highest published stable version; prereleases and older versions never move it."""
    if not VERSION.fullmatch(tag):
        return False
    return version(tag) >= max(map(version, (t for t in published if VERSION.fullmatch(t))), default=(0, 0, 0))


def published_stable(repo):
    """Tags of published, non-draft, non-prerelease GitHub releases. AIW_PUBLISHED_TAGS overrides it for tests."""
    override = os.environ.get("AIW_PUBLISHED_TAGS")
    if override is not None:
        return override.split()
    pages = json.loads(command("gh", "api", "--paginate", "--slurp", f"repos/{repo}/releases"))
    return [entry["tag_name"] for page in pages for entry in page
            if not entry["draft"] and not entry["prerelease"] and VERSION.fullmatch(entry["tag_name"])]


def update_latest():
    """Print `true` when the image `latest` tag must move to RELEASE_TAG, otherwise `false`."""
    tag = os.environ["RELEASE_TAG"]
    if not RELEASE_TAG.fullmatch(tag):
        raise SystemExit(f"Unexpected release tag: {tag}")
    print("true" if should_update_latest(tag, published_stable(os.environ.get("GITHUB_REPOSITORY", ""))) else "false")


def ancestor(a, b):
    result = subprocess.run(["git", "merge-base", "--is-ancestor", a, b]).returncode
    if result not in (0, 1):
        raise SystemExit("Unable to check commit ancestry.")
    return result == 0


def eligibility():
    before = os.environ["BEFORE_SHA"]
    if not re.fullmatch(r"[0-9a-f]{40}", before):
        raise SystemExit("Unexpected push base SHA.")
    paths = (command("git", "diff", "--name-only", before, "HEAD")
             if before != "0" * 40 else command("git", "ls-tree", "-r", "--name-only", "HEAD"))
    if qualifies(paths.splitlines()):
        with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as output:
            output.write("qualifies=true\n")


def reserve():
    sha = os.environ["TESTED_SHA"]
    repo = os.environ["GITHUB_REPOSITORY"]
    if not re.fullmatch(r"[0-9a-f]{40}", sha) or command("git", "rev-parse", "HEAD") != sha:
        raise SystemExit("Checkout differs from the tested SHA.")
    if not ancestor(sha, "origin/main"):
        raise SystemExit("The tested commit is no longer on main.")
    run = os.environ["CI_RUN_ID"]
    pages = json.loads(command("gh", "api", "--paginate", "--slurp", f"repos/{repo}/actions/runs/{run}/jobs"))
    if not any(job["name"] == "release-eligibility" and any(
            step["name"] == "Mark qualifying changes" and step["conclusion"] == "success"
            for step in job["steps"]) for page in pages for job in page["jobs"]):
        print("The tested push did not change shipped files.")
        return
    tags = {tag: command("git", "rev-list", "-n", "1", tag)
            for tag in command("git", "tag", "--list").splitlines() if VERSION.fullmatch(tag)}
    published = published_stable(repo)
    for tag in published:
        commit = tags[tag]
        if sha == commit or ancestor(sha, commit):
            print(f"Already covered by {tag}.")
            return
    previous = max(published, key=version, default="")
    paths = (command("git", "diff", "--name-only", tags[previous], sha) if previous
             else command("git", "ls-tree", "-r", "--name-only", sha))
    if not qualifies(paths.splitlines()):
        print("No shipped changes since the previous stable release.")
        return
    tag = choose_tag(tags, sha)
    if tag not in tags:
        command("gh", "api", "--method", "POST", f"repos/{repo}/git/refs",
                "-f", f"ref=refs/tags/{tag}", "-f", f"sha={sha}")
    with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as output:
        output.write(f"tag={tag}\nprevious={previous}\n")


def publish():
    """Create the GitHub release with its assets. A rerun never replaces anything: it only adds missing assets."""
    repo = os.environ["GITHUB_REPOSITORY"]
    tag = os.environ["RELEASE_TAG"]
    previous = os.environ.get("PREVIOUS_TAG")
    assets_dir = Path(os.environ.get("ASSETS_DIR", "dist"))
    assets = sorted(path for path in assets_dir.glob("*") if path.is_file())
    exists = subprocess.run(["gh", "release", "view", tag, "--repo", repo],
                            capture_output=True).returncode == 0
    if not exists:
        argv = ["gh", "release", "create", tag, *map(str, assets), "--repo", repo, "--verify-tag",
                "--title", tag, "--generate-notes"]
        if previous:
            argv += ["--notes-start-tag", previous]
        subprocess.run(argv, check=True)
        return
    have = {asset["name"] for asset in json.loads(
        command("gh", "release", "view", tag, "--repo", repo, "--json", "assets"))["assets"]}
    missing = [str(path) for path in assets if path.name not in have]
    print(f"Release {tag} already exists; uploading {len(missing)} missing asset(s).")
    if missing:
        subprocess.run(["gh", "release", "upload", tag, *missing, "--repo", repo], check=True)


COMMANDS = {"reserve": reserve, "publish": publish, "eligibility": eligibility, "update-latest": update_latest}

if __name__ == "__main__":
    COMMANDS[sys.argv[1]]()
