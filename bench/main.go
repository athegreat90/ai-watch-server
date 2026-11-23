// Command bench load-tests an AI Watch server (Python or Go) and saves the results to a file.
// With -compare it instead merges two saved result files into a side-by-side report.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Step struct {
	Scenario    string   `json:"scenario"`
	Concurrency int      `json:"concurrency"`
	Seconds     float64  `json:"seconds"`
	Requests    int      `json:"requests"`
	RPS         float64  `json:"rps"`
	OK          int      `json:"ok"`
	HTTPErrors  int      `json:"http_errors"`
	Dropped     int      `json:"dropped"`
	Timeouts    int      `json:"timeouts"`
	ConnErrors  int      `json:"conn_errors"`
	ErrorSample []string `json:"error_samples,omitempty"`
	P50ms       float64  `json:"p50_ms"`
	P95ms       float64  `json:"p95_ms"`
	P99ms       float64  `json:"p99_ms"`
	MaxMs       float64  `json:"max_ms"`
}

type Report struct {
	Label  string            `json:"label,omitempty"`
	Date   string            `json:"date"`
	Go     string            `json:"go"`
	Commit string            `json:"commit,omitempty"`
	Flags  map[string]string `json:"flags"`
	Steps  []Step            `json:"steps"`
}

const (
	outOK = iota
	outHTTP
	outDropped
	outTimeout
	outConnErr
)

var (
	base, token string
	client      *http.Client
	sampleMu    sync.Mutex
	samples     = map[string]bool{}
)

// sampleError keeps up to 3 distinct connect-error messages per step for the report.
func sampleError(msg string) {
	sampleMu.Lock()
	defer sampleMu.Unlock()
	if len(samples) < 3 {
		samples[msg] = true
	}
}

func newClient() *http.Client {
	// The server answers every request with "Connection: close", so keep-alive would not be real.
	// Windows runs out of ephemeral ports after ~12k such connections per 2 min (TIME_WAIT): run this on Linux.
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
}

func do(method, path string, body []byte, auth bool) int {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	if auth {
		req.Header.Set("X-Monitor-Key", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return outTimeout
		}
		// EOF/reset after connecting is the server's 16-connection cap closing the socket silently.
		// Anything else (refused, no free local port) is a client/environment failure, not a drop.
		msg := err.Error()
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) ||
			strings.Contains(msg, "forcibly closed") || strings.Contains(msg, "connection reset") {
			return outDropped
		}
		sampleError(msg)
		return outConnErr
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 == 2 {
		return outOK
	}
	return outHTTP
}

func event(i int) []byte {
	// A fresh timestamp matters: stale events are ignored with 200 and would flatter the numbers.
	b, _ := json.Marshal(map[string]any{
		"session_id": "bench-" + strconv.Itoa(i%40), "agent": "Codex", "status": "working",
		"project": "bench", "timestamp": float64(time.Now().UnixNano()) / 1e9,
	})
	return b
}

func seed() error {
	for i := 0; i < 40; i++ {
		if do("POST", "/api/events", event(i), true) != outOK {
			return fmt.Errorf("seeding event %d failed", i)
		}
	}
	return nil
}

func request(scenario string, n int, rng *rand.Rand) int {
	switch scenario {
	case "health":
		return do("GET", "/health", nil, false)
	case "status":
		return do("GET", "/api/status", nil, true)
	case "sessions":
		return do("GET", "/api/sessions", nil, true)
	case "events":
		return do("POST", "/api/events", event(n), true)
	default: // mixed: 90% events, 10% status
		if rng.Intn(10) == 0 {
			return do("GET", "/api/status", nil, true)
		}
		return do("POST", "/api/events", event(n), true)
	}
}

func runStep(scenario string, workers int, d time.Duration) Step {
	var wg sync.WaitGroup
	counts := make([][5]int, workers)
	sampleMu.Lock()
	clear(samples)
	sampleMu.Unlock()
	latencies := make([][]float64, workers)
	start := time.Now()
	deadline := start.Add(d)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for n := w; time.Now().Before(deadline); n += workers {
				t := time.Now()
				result := request(scenario, n, rng)
				counts[w][result]++
				if result == outOK {
					latencies[w] = append(latencies[w], float64(time.Since(t).Microseconds())/1000)
				}
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	step := Step{Scenario: scenario, Concurrency: workers, Seconds: elapsed}
	var all []float64
	for w := range counts {
		step.OK += counts[w][outOK]
		step.HTTPErrors += counts[w][outHTTP]
		step.Dropped += counts[w][outDropped]
		step.Timeouts += counts[w][outTimeout]
		step.ConnErrors += counts[w][outConnErr]
		all = append(all, latencies[w]...)
	}
	sampleMu.Lock()
	for msg := range samples {
		step.ErrorSample = append(step.ErrorSample, msg)
	}
	sampleMu.Unlock()
	step.Requests = step.OK + step.HTTPErrors + step.Dropped + step.Timeouts + step.ConnErrors
	step.RPS = float64(step.OK) / elapsed
	sort.Float64s(all)
	pick := func(p float64) float64 {
		if len(all) == 0 {
			return 0
		}
		return all[min(len(all)-1, int(p*float64(len(all))))]
	}
	step.P50ms, step.P95ms, step.P99ms = pick(0.50), pick(0.95), pick(0.99)
	if len(all) > 0 {
		step.MaxMs = all[len(all)-1]
	}
	return step
}

func isLoopback(raw string) (bool, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false, fmt.Errorf("invalid url %q", raw)
	}
	if u.Hostname() == "localhost" {
		return true, nil
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback(), nil
}

func markdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# AI Watch server benchmark %s\n\n%s | %s | commit %s\n\n", r.Label, r.Date, r.Go, r.Commit)
	b.WriteString("| scenario | conc | req/s | ok | http err | dropped | timeouts | conn err | p50 ms | p95 ms | p99 ms | max ms |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range r.Steps {
		fmt.Fprintf(&b, "| %s | %d | %.0f | %d | %d | %d | %d | %d | %.2f | %.2f | %.2f | %.2f |\n",
			s.Scenario, s.Concurrency, s.RPS, s.OK, s.HTTPErrors, s.Dropped, s.Timeouts, s.ConnErrors, s.P50ms, s.P95ms, s.P99ms, s.MaxMs)
	}
	return b.String()
}

func load(path string) (Report, error) {
	var r Report
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &r)
	}
	return r, err
}

// compare renders two saved reports side by side. Ratio is B/A (>1 means B is faster).
func compare(paths string) (string, error) {
	parts := strings.Split(paths, ",")
	if len(parts) != 2 {
		return "", fmt.Errorf("-compare wants two comma-separated result files: a.json,b.json")
	}
	a, err := load(strings.TrimSpace(parts[0]))
	if err != nil {
		return "", err
	}
	b, err := load(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", err
	}
	key := func(s Step) string { return s.Scenario + "/" + strconv.Itoa(s.Concurrency) }
	byKey := map[string]Step{}
	for _, s := range b.Steps {
		byKey[key(s)] = s
	}
	fails := func(s Step) int { return s.HTTPErrors + s.Dropped + s.Timeouts + s.ConnErrors }
	var out strings.Builder
	fmt.Fprintf(&out, "# Benchmark comparison: %s (A) vs %s (B)\n\n", a.Label, b.Label)
	fmt.Fprintf(&out, "- A: %s, %s, started %s\n- B: %s, %s, started %s\n- Ratio is B/A of req/s (above 1 means B is faster); latencies in ms; fail = http errors + dropped + timeouts + connect errors.\n\n",
		a.Label, a.Flags["d"], a.Date, b.Label, b.Flags["d"], b.Date)
	fmt.Fprintf(&out, "| scenario | conc | A req/s | B req/s | B/A | A p50 | B p50 | A p99 | B p99 | A fail | B fail |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, sa := range a.Steps {
		sb, ok := byKey[key(sa)]
		if !ok {
			continue
		}
		ratio := 0.0
		if sa.RPS > 0 {
			ratio = sb.RPS / sa.RPS
		}
		fmt.Fprintf(&out, "| %s | %d | %.0f | %.0f | %.2fx | %.2f | %.2f | %.2f | %.2f | %d | %d |\n",
			sa.Scenario, sa.Concurrency, sa.RPS, sb.RPS, ratio, sa.P50ms, sb.P50ms, sa.P99ms, sb.P99ms, fails(sa), fails(sb))
	}
	return out.String(), nil
}

func saveFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func main() {
	rawURL := flag.String("url", "http://127.0.0.1:8765", "server base URL")
	flag.StringVar(&token, "token", os.Getenv("MONITOR_TOKEN"), "X-Monitor-Key token (default: $MONITOR_TOKEN)")
	conc := flag.String("c", "1,4,8,16,32", "comma-separated concurrency levels")
	dur := flag.Duration("d", 5*time.Second, "duration of each step")
	scenarios := flag.String("scenario", "health,status,sessions,events,mixed", "comma-separated scenarios")
	label := flag.String("label", "", "name stored in the report and used in the default file name (e.g. python, go)")
	out := flag.String("out", "", "results file (default results/<timestamp>[-label].json; a .md is written next to it)")
	cmp := flag.String("compare", "", "merge two result files (a.json,b.json) into a side-by-side .md instead of benchmarking")
	allowRemote := flag.Bool("allow-remote", false, "allow a non-loopback target (writes events into that server's state!)")
	flag.Parse()

	if *cmp != "" {
		text, err := compare(*cmp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		path := *out
		if path == "" {
			path = filepath.Join("results", "compare-"+time.Now().Format("20060102-150405")+".md")
		}
		if err := saveFile(path, []byte(text)); err != nil {
			fmt.Fprintln(os.Stderr, "could not save comparison:", err)
			os.Exit(1)
		}
		fmt.Print(text)
		fmt.Println("Comparison saved to", path)
		return
	}

	loop, err := isLoopback(*rawURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !loop && !*allowRemote {
		fmt.Fprintln(os.Stderr, "refusing non-loopback target without -allow-remote: events would overwrite real session state")
		os.Exit(2)
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "missing -token (or $MONITOR_TOKEN)")
		os.Exit(2)
	}
	base = strings.TrimRight(*rawURL, "/")
	client = newClient()
	if do("GET", "/health", nil, false) != outOK {
		fmt.Fprintln(os.Stderr, "server is not reachable at", base)
		os.Exit(1)
	}

	report := Report{Label: *label, Date: time.Now().Format(time.RFC3339), Go: runtime.Version(), Flags: map[string]string{
		"url": base, "c": *conc, "d": dur.String(), "scenario": *scenarios}}
	if sha, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		report.Commit = strings.TrimSpace(string(sha))
	}
	fmt.Printf("%-9s %5s %8s %7s %6s %7s %5s %7s %8s %8s %8s\n", "scenario", "conc", "req/s", "ok", "httpE", "dropped", "tmo", "connE", "p50ms", "p95ms", "p99ms")
	for _, scenario := range strings.Split(*scenarios, ",") {
		scenario = strings.TrimSpace(scenario)
		if scenario != "events" && scenario != "health" {
			if err := seed(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		for _, c := range strings.Split(*conc, ",") {
			workers, err := strconv.Atoi(strings.TrimSpace(c))
			if err != nil || workers < 1 {
				fmt.Fprintf(os.Stderr, "invalid concurrency %q\n", c)
				os.Exit(2)
			}
			s := runStep(scenario, workers, *dur)
			report.Steps = append(report.Steps, s)
			fmt.Printf("%-9s %5d %8.0f %7d %6d %7d %5d %7d %8.2f %8.2f %8.2f\n", s.Scenario, s.Concurrency, s.RPS, s.OK, s.HTTPErrors, s.Dropped, s.Timeouts, s.ConnErrors, s.P50ms, s.P95ms, s.P99ms)
			if len(s.ErrorSample) > 0 {
				fmt.Println("   connect errors, e.g.:", s.ErrorSample[0])
			}
		}
	}

	path := *out
	if path == "" {
		name := time.Now().Format("20060102-150405")
		if *label != "" {
			name += "-" + *label
		}
		path = filepath.Join("results", name+".json")
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := saveFile(path, append(data, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "could not save results:", err)
		os.Exit(1)
	}
	if err := saveFile(strings.TrimSuffix(path, filepath.Ext(path))+".md", []byte(markdown(report))); err != nil {
		fmt.Fprintln(os.Stderr, "could not save results:", err)
		os.Exit(1)
	}
	fmt.Println("Results saved to", path)
}
