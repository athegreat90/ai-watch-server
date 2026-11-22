"""Local-only AI agent event bridge. Standard-library Python, no dependencies."""
from __future__ import annotations

import hmac
import json
import os
import signal
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parent
CONFIG = ROOT / "config.local.json"
MAX_EVENTS = 40
DONE_TTL = 120
ACTIVE_TTL = 3600
LIMITS_TTL = 900
LONG_TASK_SECONDS = 300
STATE: dict[tuple[str, str], dict] = {}
LIMITS: dict[str, dict] = {}
LOCK = threading.Lock()


def load_config() -> dict:
    if not CONFIG.exists():
        raise SystemExit("Missing bridge/config.local.json. Run: python bridge/init_config.py")
    data = json.loads(CONFIG.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or not isinstance(data.get("token"), str) or len(data["token"]) < 24:
        raise SystemExit("Invalid or weak token in config.local.json")
    return data


def clean(value: object, max_len: int = 60) -> str:
    return str(value if value is not None else "").replace("\n", " ").replace("\r", " ")[:max_len]


def post_event(event: dict) -> None:
    if any(not isinstance(event.get(field), str) for field in ('session_id', 'agent', 'status')):
        raise ValueError('invalid identifiers')
    if any(not isinstance(event.get(field, ''), str) for field in ('project', 'detail')):
        raise ValueError('invalid metadata')
    sid = clean(event.get("session_id"), 80)
    if not sid.strip() or not event["agent"].strip():
        raise ValueError("session_id is required")
    status = clean(event.get("status"), 20)
    if status not in {"working", "needs_attention", "done", "idle", "notice", "error"}:
        raise ValueError("Invalid status")
    now = time.time()
    timestamp = event.get("timestamp", now)
    if type(timestamp) not in (float, int) or not now - ACTIVE_TTL <= timestamp <= now + 60:
        raise ValueError("invalid timestamp")
    record = {
        "session_id": sid,
        "agent": clean(event.get("agent") or "AI", 24),
        "project": clean(event.get("project") or "Unknown project", 40),
        "status": status,
        "detail": clean(event.get("detail") or "", 66),
        "updated": timestamp,
    }
    with LOCK:
        key = (record["agent"], sid)
        previous = STATE.get(key)
        if previous and previous["updated"] >= timestamp:
            return
        # "started" is per turn: a new turn begins when work resumes from a non-working state.
        new_turn = not previous or (status == "working" and previous["status"] not in ("working", "needs_attention"))
        record["started"] = timestamp if new_turn else previous["started"]
        record["since"] = previous["since"] if previous and previous["status"] == status else timestamp
        STATE[key] = record
        if len(STATE) > MAX_EVENTS:
            oldest = min(STATE, key=lambda key: STATE[key]["updated"])
            del STATE[oldest]


def post_limits(data: dict) -> None:
    agent = data.get("agent")
    if agent not in ("Claude Code", "Codex", "Antigravity"):
        raise ValueError("invalid agent")
    record = {"updated": time.time()}
    for window in ("five_hour", "weekly"):
        item = data.get(window)
        if not isinstance(item, dict):
            raise ValueError("invalid window")
        used, resets = item.get("used_pct"), item.get("resets_at")
        if type(used) not in (int, float) or not 0 <= used <= 100:
            raise ValueError("invalid percentage")
        if type(resets) not in (int, float) or resets < 0:
            raise ValueError("invalid reset time")
        record[window] = {"used_pct": round(used, 1), "resets_at": resets}
    with LOCK:
        LIMITS[agent] = record


def limits_snapshot(now: float) -> dict:
    # Caller holds LOCK. Stale readings are dropped so the watch never shows old numbers.
    for agent in [a for a, r in LIMITS.items() if now - r["updated"] > LIMITS_TTL]:
        del LIMITS[agent]
    # The watch has no wall clock, so each window also carries seconds until reset.
    return {a: {w: {**r[w], "resets_in": max(0, int(r[w]["resets_at"] - now))} for w in ("five_hour", "weekly")}
            for a, r in LIMITS.items()}


def snapshot(limit=8) -> dict:
    now = time.time()
    with LOCK:
        for key, record in list(STATE.items()):
            ttl = DONE_TTL if record["status"] in ("done", "idle", "notice", "error") else ACTIVE_TTL
            if now - record["updated"] > ttl:
                del STATE[key]
        all_sessions = list(STATE.values())
        limits = limits_snapshot(now)
    priority = {"needs_attention": 0, "error": 1, "working": 2, "notice": 3, "done": 4, "idle": 5}
    sessions = sorted(all_sessions, key=lambda s: (priority[s["status"]], -s["updated"]))[:limit]
    return {
        "sessions": [{**s, "duration": (d := max(0, int((now if s["status"] in ("working", "needs_attention") else s["updated"]) - s["started"]))),
                      "finished_long": s["status"] == "done" and d >= LONG_TASK_SECONDS,
                      "waiting": max(0, int(now - s["since"])) if s["status"] == "needs_attention" else 0} for s in sessions],
        "limits": limits,
        "active_count": sum(s["status"] == "working" for s in all_sessions),
        "attention_count": sum(s["status"] == "needs_attention" for s in all_sessions),
        "total_count": len(all_sessions),
    }


def make_handler(token: str):
    class Handler(BaseHTTPRequestHandler):
        timeout = 3
        protocol_version = "HTTP/1.1"

        def _send(self, code: int, obj: dict) -> None:
            self.close_connection = True
            payload = json.dumps(obj, ensure_ascii=True, separators=(",", ":")).encode("ascii")
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(payload)

        def _authorized(self) -> bool:
            received = self.headers.get("X-Monitor-Key", "")
            return hmac.compare_digest(received.encode("utf-8"), token.encode("utf-8"))

        def do_GET(self):
            if self.path == "/health":
                return self._send(200, {"ok": True})
            if not self._authorized():
                return self._send(401, {"error": "unauthorized"})
            if self.path == "/api/sessions":
                return self._send(200, snapshot(MAX_EVENTS))
            if self.path == "/api/status":
                return self._send(200, snapshot())
            return self._send(404, {"error": "not found"})

        def do_POST(self):
            if not self._authorized():
                return self._send(401, {"error": "unauthorized"})
            if self.path not in ("/api/event", "/api/events", "/api/limits"):
                return self._send(404, {"error": "not found"})
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if length <= 0 or length > 4096:
                    return self._send(413, {"error": "body must be 1-4096 bytes"})
                data = json.loads(self.rfile.read(length))
                if not isinstance(data, dict):
                    raise ValueError("object expected")
                (post_limits if self.path == "/api/limits" else post_event)(data)
            except (ValueError, RecursionError, TimeoutError):
                return self._send(400, {"error": "invalid event"})
            return self._send(200, {"ok": True})

        def log_message(self, fmt: str, *args) -> None:
            # Request targets can contain sensitive caller-controlled data.
            pass

    return Handler


class MonitorServer(ThreadingHTTPServer):
    slots = threading.BoundedSemaphore(16)

    def process_request(self, request, address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, address):
        try:
            super().process_request_thread(request, address)
        finally:
            self.slots.release()

    def handle_error(self, request, address):
        print("Monitor request terminated", flush=True)


def main() -> None:
    config = load_config()
    port = int(config.get("port", 8765))
    bind = os.environ.get("MONITOR_BIND", "127.0.0.1")
    app = MonitorServer((bind, port), make_handler(config["token"]))
    print(f"AI monitor bridge listening on {bind}:{port}", flush=True)
    print("Use a trusted private network only. Ctrl+C to stop.", flush=True)
    signal.signal(signal.SIGTERM, lambda *_: threading.Thread(target=app.shutdown).start())
    try:
        app.serve_forever(poll_interval=0.2)
    except KeyboardInterrupt:
        pass
    finally:
        app.server_close()


if __name__ == "__main__":
    main()
