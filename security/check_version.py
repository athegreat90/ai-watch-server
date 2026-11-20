import re
import sys
from pathlib import Path
docker = Path("Dockerfile").read_text()
version = re.search(r"^FROM python:(\d+\.\d+\.\d+)-", docker, re.M)[1]
pin = Path(".python-version")
if pin.exists() and pin.read_text().strip() != version:
    raise SystemExit("Native and container Python pins differ.")
if ".".join(map(str, sys.version_info[:3])) != version:
    raise SystemExit(f"Expected Python {version} in CI.")
