import concurrent.futures
import http.client
import json
import threading
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch
from http.server import ThreadingHTTPServer

from bridge import server


class BridgeTests(unittest.TestCase):
    def setUp(self):
        server.STATE.clear()
        self.api = ThreadingHTTPServer(("127.0.0.1", 0), server.make_handler("test-token"))
        self.thread = threading.Thread(target=self.api.serve_forever)
        self.thread.start()

    def tearDown(self):
        self.api.shutdown()
        self.thread.join()
        self.api.server_close()

    def request(self, path, body=None, token="test-token"):
        connection = http.client.HTTPConnection("127.0.0.1", self.api.server_port, timeout=3)
        try:
            connection.request("GET" if body is None else "POST", path, body,
                               {"X-Monitor-Key": token})
            response = connection.getresponse()
            return response.status, json.loads(response.read())
        finally:
            connection.close()

    def event(self, agent="Codex", state="working", timestamp=None):
        return json.dumps({"agent": agent, "session_id": "test", "status": state,
                           "timestamp": time.time() if timestamp is None else timestamp})

    def test_authentication(self):
        self.assertEqual(self.request("/health", token="")[0], 200)
        self.assertEqual(self.request("/api/status", token="wrong")[0], 401)
        self.assertEqual(self.request("/api/sessions", token="")[0], 401)
        self.assertEqual(self.request("/api/events", self.event(), "")[0], 401)

    def test_invalid_payloads(self):
        for body in ("{", "[]", "{}", '{"session_id": 42}', "{" * 4097):
            self.assertIn(self.request("/api/events", body)[0], (400, 413))
        self.assertEqual(self.request("/api/status")[1]["sessions"], [])

    def test_transitions_and_concurrency(self):
        for state in ("working", "needs_attention", "done"):
            self.assertEqual(self.request("/api/events", self.event(state=state))[0], 200)
            self.assertEqual(self.request("/api/status")[1]["sessions"][0]["status"], state)
        with concurrent.futures.ThreadPoolExecutor() as executor:
            jobs = [executor.submit(self.request, "/api/events", self.event(agent, state))
                    for agent, state in (("Claude Code", "working"), ("Gemini CLI", "error"))]
            self.assertEqual([job.result()[0] for job in jobs], [200, 200])
        self.assertEqual([s["status"] for s in self.request("/api/sessions")[1]["sessions"]],
                         ["error", "working", "done"])
        self.assertEqual(self.request("/api/events", self.event("Claude Code", "done"))[0], 200)

    def test_duplicate_out_of_order_and_stale(self):
        now = time.time()
        for state, stamp in (("working", now - 5), ("done", now), ("error", now), ("working", now - 1)):
            self.assertEqual(self.request("/api/events", self.event(state=state, timestamp=stamp))[0], 200)
        self.assertEqual(self.request("/api/status")[1]["sessions"][0]["status"], "done")
        self.request("/api/events", self.event("Claude Code", "done", now - server.DONE_TTL - 5))
        self.assertEqual(self.request("/api/sessions")[1]["total_count"], 1)
