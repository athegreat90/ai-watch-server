# AI Watch server: Python vs Go performance report

Date of the runs: 2026-11-23

## Summary

**The Go version is faster in every test we ran.** It handles about 3 to 8 times more requests per second than the Python server once several clients talk to it at the same time, and it answers each request faster. Neither server failed a single request.

| | Python 3.15.2 (current server) | Go (benchmark-only port) |
|---|---|---|
| Requests per second, 8 clients | about 1,800 to 4,200 | about 9,500 to 17,600 |
| Typical response time, 32 clients | about 8 to 15 ms | about 1 to 2.4 ms |
| Failed requests | 0 | 0 |

In practice the Python server is probably fast enough for this project. A watch and a few AI agents send far fewer than 2,000 requests per second. This report measures the server code only, on one computer. It does not measure your real setup (a Raspberry Pi, a reverse proxy, the internet).

## What was compared

- **Python server:** the existing `bridge/server.py`, unchanged, run with Python 3.15.2 (the version the Docker image uses).
- **Go server:** a new program, `bench/goserver/main.go`, written only for this comparison. It copies the Python server's behaviour: same endpoints, same validation rules, same limits (40 sessions, 4096-byte requests, 16 open connections at a time) and the same JSON answers. It is **not** production code.
- **Same behaviour check:** before each benchmark, both servers had to pass the same list of requests (`bench/goserver/parity_test.go`). Both passed. We also checked that this test really fails when the Go server is deliberately broken.
- **Load tool:** `bench/main.go`, a Go program that sends requests for 5 seconds per step, with 1, 4, 8, 16 and 32 clients at the same time.

## What the tests did

| Test | What it sends |
|---|---|
| health | `GET /health` (the simplest request, no login) |
| status | `GET /api/status` (what the watch asks for: up to 8 sessions) |
| sessions | `GET /api/sessions` (up to 40 sessions) |
| events | `POST /api/events` (an agent reporting its state) |
| mixed | 90% events and 10% status, close to real use |

Each test was run twice:

- **Unpinned:** both servers could use all 16 processor cores of the test computer.
- **One core:** each server was limited to a single core. This is closer to a small machine like a Raspberry Pi.

## Results

"Ratio" means how many times more requests per second Go handled than Python. Response times are in milliseconds (ms); lower is better. "Typical" is the median (half of the requests were faster), "slow" is the 99th percentile (only 1 in 100 requests was slower).

### Ratio of requests per second (Go divided by Python)

| Clients at the same time | Unpinned | One core |
|---|---|---|
| 1 | 1.2x to 1.6x | 1.2x to 3.2x |
| 4 and 8 | 3.8x to 5.6x | 3.1x to 5.7x |
| 16 and 32 | 4.5x to 7.9x | 4.7x to 6.5x |

Each range goes from the lowest to the highest result across the five tests.

### Selected numbers: unpinned (all 16 cores)

| Test | Clients | Python req/s | Go req/s | Python typical / slow | Go typical / slow |
|---|---|---|---|---|---|
| health | 1 | 2,557 | 3,986 | 0.36 / 0.71 ms | 0.23 / 0.50 ms |
| health | 8 | 2,695 | 14,990 | 2.82 / 5.12 ms | 0.42 / 1.58 ms |
| health | 32 | 2,616 | 19,661 | 11.69 / 18.35 ms | 1.26 / 6.03 ms |
| status | 1 | 2,156 | 2,921 | 0.44 / 0.80 ms | 0.33 / 0.64 ms |
| status | 8 | 2,284 | 11,883 | 3.37 / 5.83 ms | 0.59 / 1.72 ms |
| status | 32 | 2,429 | 14,282 | 12.62 / 18.96 ms | 1.96 / 6.63 ms |
| sessions | 1 | 1,911 | 2,614 | 0.50 / 0.85 ms | 0.37 / 0.72 ms |
| sessions | 8 | 1,818 | 9,772 | 4.35 / 6.69 ms | 0.76 / 1.90 ms |
| sessions | 32 | 2,064 | 11,931 | 14.90 / 21.61 ms | 2.40 / 6.97 ms |
| events | 1 | 2,341 | 2,871 | 0.40 / 0.74 ms | 0.34 / 0.64 ms |
| events | 8 | 2,576 | 10,539 | 3.00 / 4.86 ms | 0.70 / 1.69 ms |
| events | 32 | 2,542 | 17,762 | 11.97 / 18.97 ms | 1.45 / 6.07 ms |
| mixed | 1 | 2,141 | 2,897 | 0.45 / 0.76 ms | 0.33 / 0.65 ms |
| mixed | 8 | 2,492 | 9,511 | 3.11 / 5.04 ms | 0.77 / 1.96 ms |
| mixed | 32 | 2,159 | 17,081 | 14.87 / 20.55 ms | 1.55 / 6.11 ms |

### Selected numbers: each server limited to one core

| Test | Clients | Python req/s | Go req/s | Python typical / slow | Go typical / slow |
|---|---|---|---|---|---|
| health | 1 | 2,546 | 3,108 | 0.38 / 0.77 ms | 0.28 / 0.71 ms |
| health | 8 | 4,168 | 17,613 | 1.70 / 4.25 ms | 0.34 / 1.51 ms |
| health | 32 | 3,795 | 24,586 | 7.93 / 18.49 ms | 0.89 / 5.60 ms |
| status | 1 | 1,543 | 4,918 | 0.56 / 2.35 ms | 0.19 / 0.41 ms |
| status | 8 | 2,884 | 14,519 | 2.55 / 5.37 ms | 0.50 / 1.51 ms |
| status | 32 | 3,076 | 17,181 | 9.77 / 20.69 ms | 1.05 / 24.15 ms |
| sessions | 1 | 1,575 | 4,524 | 0.59 / 1.33 ms | 0.21 / 0.44 ms |
| sessions | 8 | 2,495 | 12,485 | 2.92 / 6.22 ms | 0.62 / 1.25 ms |
| sessions | 32 | 2,522 | 12,644 | 12.16 / 22.99 ms | 1.35 / 3.21 ms |
| events | 1 | 2,157 | 4,606 | 0.42 / 0.95 ms | 0.20 / 0.50 ms |
| events | 8 | 3,495 | 14,385 | 2.12 / 4.24 ms | 0.48 / 1.35 ms |
| events | 32 | 3,302 | 19,785 | 9.14 / 18.53 ms | 1.14 / 7.34 ms |
| mixed | 1 | 1,875 | 4,398 | 0.48 / 1.15 ms | 0.21 / 0.46 ms |
| mixed | 8 | 3,321 | 14,854 | 2.14 / 5.05 ms | 0.46 / 1.33 ms |
| mixed | 32 | 3,374 | 17,938 | 9.03 / 17.69 ms | 1.21 / 9.30 ms |

The complete tables (all five client counts) are in `results/20261123-143422-compare-all.md` and `results/20261123-143837-compare-1core.md`.

## What the numbers mean

1. **Python reaches a ceiling.** Adding more clients does not give it more throughput. With 4 or more clients it stays around 1,800 to 2,800 requests per second unpinned (2,500 to 4,500 on one core). The extra clients just wait longer, which is why its response time grows with the number of clients. Go reaches about 8,500 to 24,600 requests per second in the same cases.
2. **With one client the gap is small** (about 1.2 to 1.6 times unpinned). A single client spends most of its time on the network round trip, not on the server code.
3. **With 4 or more clients, Python was faster limited to one core** than with all 16 cores. A likely reason is that its threads compete less when they share a single core. We did not investigate this.
4. **Python 3.15.2 treats waiting clients more fairly than 3.14.4.** In an earlier run with Python 3.14.4, a few requests waited 1 to 4 seconds at 16 and 32 clients. With 3.15.2 the slowest request was about 21 ms. Overall throughput was about the same.

## Limits of this report (please read)

- **Each test was run once.** We do not know how much the numbers vary between runs. Small differences, for example the single result where Go's "slow" time is slightly worse (`status`, 32 clients, one core: 24 ms for Go against 21 ms for Python), may be noise.
- **Local test only.** Both servers and the load tool ran on the same Windows PC, inside WSL2 (Linux), talking over the loopback network. There was no reverse proxy, no HTTPS and no real network. Real latencies will be higher for both servers.
- **Not a real Raspberry Pi.** "One core" is an approximation (`taskset`), not real Pi hardware or a Docker CPU limit.
- **Same behaviour, not identical text.** The two servers return the same data, but the JSON text is not byte-for-byte identical (for example, Python escapes non-ASCII characters).
- **The Go server is only a test program.** It has no Dockerfile, no release process and no production hardening.
- **Connections.** Both servers close the connection after every request (as the Python server does), so every request opens a new connection. Past 16 open connections, both servers make the extra clients wait. On Windows itself this test cannot run for long, because the computer runs out of network ports, so all measurements were made in WSL2.

## How to repeat the benchmark

See `bench/README.md`. In short: build the Linux programs, then run `bench/run-compare.sh all` and `bench/run-compare.sh 1core` inside WSL or Linux. Use `PYTHON=<path to python 3.15.2>` to choose the Python version. Each run writes its results to `bench/results/`.

## Files

| File | Content |
|---|---|
| `bench/main.go` | Load tool, also merges two result files into a comparison |
| `bench/goserver/main.go` | Go version of the server (benchmark only) |
| `bench/goserver/parity_test.go` | Test that both servers behave the same |
| `bench/run-compare.sh` | Runs both servers and creates the comparison |
| `bench/results/20261123-143422-compare-all.md` | Full comparison, unpinned |
| `bench/results/20261123-143837-compare-1core.md` | Full comparison, one core |
