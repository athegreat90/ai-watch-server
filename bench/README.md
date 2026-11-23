# Benchmark: Python server vs Go port

- `main.go` - load tool (stdlib only). Prints a table and saves `results/<timestamp>[-label].json` + `.md`. `-compare a.json,b.json` merges two saved runs into a side-by-side `.md`.
- `goserver/main.go` - **benchmark-only** Go port of `bridge/server.py` (same routes, validation, limits, 16-connection cap, `Connection: close`). Not production code.
- `goserver/parity_test.go` - black-box table of requests that runs against any server (`-url`, `-token`). It must pass on both servers before their numbers mean anything.
- `run-compare.sh` - starts each server in turn, runs parity, then the benchmark, then writes `results/<ts>-compare-<mode>.md`.

Never point any of this at the live server: `events`/`mixed` and the parity test overwrite its sessions. Non-loopback URLs are refused unless you pass `-allow-remote`.

## Run it (Linux or WSL, not native Windows)

The server answers every request with `Connection: close`, so the client opens one connection per request. Windows runs out of ephemeral ports after roughly 12k of those per 2 minutes and the run fills with `connE` errors. Linux does not have this limit and is what production runs on.

```sh
# from Windows, cross-compile into a scratch dir (BIN), then run inside WSL
GOOS=linux go build -o $BIN/bench . && GOOS=linux go build -o $BIN/goserver ./goserver && GOOS=linux go test -c -o $BIN/parity.test ./goserver
BIN=<dir as seen from Linux> bash bench/run-compare.sh all      # no pinning
BIN=<dir as seen from Linux> bash bench/run-compare.sh 1core    # servers pinned to CPU 0 (closer to production's cpus: 0.5)
```

Single server by hand: copy `bridge/server.py` (or run `goserver`) into a scratch dir with a `config.local.json` of `{"token": "<24+ chars>", "port": 18765}`, start it with `MONITOR_BIND=127.0.0.1`, then `MONITOR_TOKEN=<token> go run . -url http://127.0.0.1:18765`.

Flags: `-url`, `-token` (or `$MONITOR_TOKEN`), `-c` (default `1,4,8,16,32`), `-d` (default `5s`), `-scenario health,status,sessions,events,mixed`, `-label`, `-out`, `-compare`, `-allow-remote`.

## Reading the numbers

- `dropped`: reset/EOF after connecting. Both servers close connections silently when 16 are already open.
- `connE`: could not connect at all (refused, no free local port). A client/environment limit, not a server result; the first error text is printed under the row.
- `tmo`: no response within 5 s. Latency percentiles cover successful requests only.
- **Past 16 clients the servers queue.** Python's single accept loop leaves excess clients waiting in the kernel backlog. The Go port copies that on purpose: it takes a free slot before accepting, so extra clients wait instead of being closed (an earlier version dropped them, which made the 16/32-client rows incomparable). Waiting shows up as higher latency, not as failures.
- **Use the same Python the image uses.** `PYTHON=<interpreter> bash run-compare.sh ...` picks the interpreter for `server.py` (default `python3`). Production runs 3.15.2 (see `../Dockerfile`); a standalone Linux build can be unpacked into a scratch dir without installing anything. The version used is written at the bottom of each compare report.
