import json
import urllib.error
import urllib.request
from pathlib import Path

config = json.loads(Path(__file__).with_name("config.local.json").read_text(encoding="utf-8-sig"))
client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
origin = config["url"].rstrip("/")
with client.open(origin + "/health", timeout=3) as response:
    if json.load(response).get("ok") is not True:
        raise SystemExit("Health check failed")
try:
    client.open(origin + "/api/status", timeout=3).close()
except urllib.error.HTTPError as error:
    if error.code != 401:
        raise
else:
    raise SystemExit("Anonymous status unexpectedly succeeded")
for endpoint in ("/api/status", "/api/sessions"):
    request = urllib.request.Request(origin + endpoint, headers={"X-Monitor-Key": config["token"]})
    with client.open(request, timeout=3) as response:
        if not isinstance(json.load(response).get("sessions"), list):
            raise SystemExit("Invalid sessions response")
print("Health, anonymous rejection and authenticated session reads passed.")
