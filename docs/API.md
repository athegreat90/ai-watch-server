# API reference

All responses are JSON with `Cache-Control: no-store`. Every endpoint except `GET /health` requires the header `X-Monitor-Key: <token>` (the `token` in `config.local.json`). A wrong or missing key returns `401 {"error":"unauthorized"}`.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/health` | no | Liveness: `{"ok":true}` |
| GET | `/api/status` | yes | Up to 8 sessions plus limits and counters (what the watch polls) |
| GET | `/api/sessions` | yes | Same shape, up to 40 sessions |
| POST | `/api/events` | yes | Report one session event (`/api/event` is an alias) |
| POST | `/api/limits` | yes | Report 5-hour and weekly usage of one agent |

Any other path returns `404 {"error":"not found"}`.

## POST /api/events

Body: one JSON object, 1 to 4096 bytes.

| Field | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | yes | Truncated to 80 characters |
| `agent` | string | yes | Truncated to 24 characters (for example `Codex`) |
| `status` | string | yes | `working`, `needs_attention`, `done`, `idle`, `notice` or `error` |
| `project` | string | no | Truncated to 40 characters, default `Unknown project` |
| `detail` | string | no | Truncated to 66 characters |
| `timestamp` | number | no | Unix seconds. Defaults to now. Must be within the last hour and at most 60 seconds ahead |

A session is identified by `agent` + `session_id`. An event that is not newer than the stored one is ignored (still `200`).

```sh
curl -s -X POST http://192.168.1.50:8765/api/events \
  -H "X-Monitor-Key: $TOKEN" -H "Content-Type: application/json" \
  -d '{"session_id":"demo","agent":"Codex","status":"working","project":"my-repo"}'
# {"ok":true}
```

## POST /api/limits

Accepted `agent` values: `Claude Code` and `Codex`.

```json
{
  "agent": "Codex",
  "five_hour": {"used_pct": 31, "resets_at": 1790000000},
  "weekly": {"used_pct": 98, "resets_at": 1790500000}
}
```

`used_pct` is 0 to 100. `resets_at` is Unix seconds and must not be negative.

## GET /api/status and /api/sessions

```json
{
  "sessions": [{
    "session_id": "demo", "agent": "Codex", "project": "my-repo",
    "status": "working", "detail": "", "updated": 1790000000.0,
    "started": 1790000000.0, "since": 1790000000.0,
    "duration": 42, "finished_long": false, "waiting": 0
  }],
  "limits": {"Codex": {"five_hour": {"used_pct": 31.0, "resets_at": 1790000000, "resets_in": 3600},
                        "weekly": {"used_pct": 98.0, "resets_at": 1790500000, "resets_in": 400000}}},
  "active_count": 1,
  "attention_count": 0,
  "total_count": 1
}
```

- `duration`: seconds of the current turn. It keeps growing while `working` or `needs_attention` and freezes at the last update otherwise. A new turn starts when work resumes after `done`, `idle`, `notice` or `error`.
- `finished_long`: `true` when the status is `done` and the turn lasted 5 minutes or more.
- `waiting`: seconds since the session started needing attention (0 for other statuses).
- Sessions are ordered `needs_attention`, `error`, `working`, `notice`, `done`, `idle`, then newest first.
- `limits` only lists agents with a reading newer than 15 minutes. `resets_in` is seconds until reset.

## Errors

| Code | Body | Cause |
|---|---|---|
| 400 | `{"error":"invalid event"}` | Invalid JSON, non-object body, unknown status, bad types, bad timestamp, bad limits values |
| 401 | `{"error":"unauthorized"}` | Missing or wrong `X-Monitor-Key` |
| 404 | `{"error":"not found"}` | Unknown path |
| 413 | `{"error":"body must be 1-4096 bytes"}` | Empty body or larger than 4096 bytes |

## Behavior and limits

| Setting | Value |
|---|---|
| Stored sessions | 40 (the oldest is dropped first) |
| `done`, `idle`, `notice`, `error` sessions expire after | 120 s |
| `working` and `needs_attention` sessions expire after | 3600 s |
| Limits readings expire after | 900 s |
| Long task threshold | 300 s |
| Concurrent connections | 16 (extra clients wait until a slot frees) |
| Request timeout | 3 s |
| Storage | Memory only; cleared on restart |

## Compatibility notes (Go server)

The Go server answers like the Python server of v1.1.0 for every request in the parity suite (`bench/parity.sh`). Differences that do not affect the watch, hooks or firmware:

- Past 16 concurrent connections the server queues extra clients instead of accepting and closing them.
- JSON is compact ASCII with `\uXXXX` escapes, as before; a number such as `1700000000.0` may be printed as `1700000000`.
- A malformed `Content-Length` header is rejected by Go's HTTP stack with a plain `400` before the handler runs; unsupported methods answer `501`.
