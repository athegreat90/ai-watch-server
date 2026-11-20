import json
import os
import secrets
from pathlib import Path

path = Path(__file__).with_name("config.local.json")
try:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    pass
else:
    with os.fdopen(fd, "w", encoding="utf-8") as config:
        json.dump({"token": secrets.token_urlsafe(32), "url": "http://127.0.0.1:8765"}, config)
