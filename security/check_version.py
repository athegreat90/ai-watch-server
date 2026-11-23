"""Fail when the Go toolchain pins drift apart or a base image is not pinned by digest."""
import re
import subprocess
import sys
from pathlib import Path

dockerfile = Path("Dockerfile").read_text()
go_mod = re.search(r"^go (\d+\.\d+\.\d+)$", Path("go.mod").read_text(), re.M)
builder = re.search(r"^FROM --platform=\$BUILDPLATFORM golang:(\d+\.\d+\.\d+)-bookworm@sha256:[0-9a-f]{64} AS build$",
                    dockerfile, re.M)
runtime = re.search(r"^FROM gcr\.io/distroless/static-debian13:nonroot@sha256:[0-9a-f]{64}$", dockerfile, re.M)
if not go_mod:
    sys.exit("go.mod must declare a full toolchain version such as 'go 1.27.4'.")
if not builder:
    sys.exit("Dockerfile build stage must be golang:X.Y.Z-bookworm pinned by digest.")
if not runtime:
    sys.exit("Dockerfile runtime stage must be gcr.io/distroless/static-debian13:nonroot pinned by digest.")
if go_mod[1] != builder[1]:
    sys.exit(f"go.mod ({go_mod[1]}) and the Dockerfile builder ({builder[1]}) differ.")
installed = subprocess.run(["go", "env", "GOVERSION"], capture_output=True, text=True).stdout.strip()
if installed != f"go{go_mod[1]}":
    sys.exit(f"Expected {'go' + go_mod[1]} in CI, found {installed or 'no Go toolchain'}.")
print(f"Go {go_mod[1]} is consistent across go.mod, the Dockerfile and CI.")
