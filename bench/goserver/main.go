// Command goserver is a benchmark-only Go port of bridge/server.py. It mirrors the Python server's
// behaviour function by function so the two can be compared fairly. It is not meant for production.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxEvents       = 40
	doneTTL         = 120
	activeTTL       = 3600
	limitsTTL       = 900
	longTaskSeconds = 300
	maxConnections  = 16
	maxBody         = 4096
)

type key struct{ agent, sessionID string }

type record struct {
	SessionID string  `json:"session_id"`
	Agent     string  `json:"agent"`
	Project   string  `json:"project"`
	Status    string  `json:"status"`
	Detail    string  `json:"detail"`
	Updated   float64 `json:"updated"`
	Started   float64 `json:"started"`
	Since     float64 `json:"since"`
}

type sessionOut struct {
	record
	Duration     int  `json:"duration"`
	FinishedLong bool `json:"finished_long"`
	Waiting      int  `json:"waiting"`
}

type window struct {
	UsedPct  float64 `json:"used_pct"`
	ResetsAt float64 `json:"resets_at"`
}

type windowOut struct {
	window
	ResetsIn int `json:"resets_in"` // always present, even when 0, like the Python server
}

type limitRecord struct {
	updated          float64
	fiveHour, weekly window
}

var (
	mu     sync.Mutex // mirrors LOCK
	state  = map[key]*record{}
	limits = map[string]*limitRecord{}

	validStatus = map[string]bool{"working": true, "needs_attention": true, "done": true, "idle": true, "notice": true, "error": true}
	priority    = map[string]int{"needs_attention": 0, "error": 1, "working": 2, "notice": 3, "done": 4, "idle": 5}
)

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// clean mirrors clean(): newlines become spaces and the text is cut by characters, not bytes.
func clean(value string, maxLen int) string {
	value = strings.NewReplacer("\n", " ", "\r", " ").Replace(value)
	if r := []rune(value); len(r) > maxLen {
		return string(r[:maxLen])
	}
	return value
}

// number mirrors `type(x) in (int, float)`: JSON numbers only, never booleans, null or strings.
func number(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func str(v any) (string, bool) { s, ok := v.(string); return s, ok }

type errInvalid struct{}

func (errInvalid) Error() string { return "invalid event" }

var invalid = errInvalid{}

func postEvent(event map[string]any) error {
	for _, field := range []string{"session_id", "agent", "status"} {
		if _, ok := str(event[field]); !ok {
			return invalid
		}
	}
	for _, field := range []string{"project", "detail"} {
		if v, present := event[field]; present {
			if _, ok := str(v); !ok {
				return invalid
			}
		}
	}
	sessionID, _ := str(event["session_id"])
	agent, _ := str(event["agent"])
	sid := clean(sessionID, 80)
	if strings.TrimSpace(sid) == "" || strings.TrimSpace(agent) == "" {
		return invalid
	}
	statusRaw, _ := str(event["status"])
	status := clean(statusRaw, 20)
	if !validStatus[status] {
		return invalid
	}
	t := now()
	timestamp := t
	if v, present := event["timestamp"]; present {
		f, ok := number(v)
		if !ok || !(t-activeTTL <= f && f <= t+60) {
			return invalid
		}
		timestamp = f
	}
	project, _ := str(event["project"])
	if project == "" {
		project = "Unknown project"
	}
	detail, _ := str(event["detail"])
	rec := &record{SessionID: sid, Agent: clean(agent, 24), Project: clean(project, 40), Status: status,
		Detail: clean(detail, 66), Updated: timestamp}

	mu.Lock()
	defer mu.Unlock()
	k := key{rec.Agent, sid}
	previous := state[k]
	if previous != nil && previous.Updated >= timestamp {
		return nil
	}
	// "started" is per turn: a new turn begins when work resumes from a non-working state.
	newTurn := previous == nil || (status == "working" && previous.Status != "working" && previous.Status != "needs_attention")
	if newTurn {
		rec.Started = timestamp
	} else {
		rec.Started = previous.Started
	}
	if previous != nil && previous.Status == status {
		rec.Since = previous.Since
	} else {
		rec.Since = timestamp
	}
	state[k] = rec
	if len(state) > maxEvents {
		var oldest key
		first := true
		for k, r := range state {
			if first || r.Updated < state[oldest].Updated {
				oldest, first = k, false
			}
		}
		delete(state, oldest)
	}
	return nil
}

func postLimits(data map[string]any) error {
	agent, _ := str(data["agent"])
	if agent != "Claude Code" && agent != "Codex" && agent != "Antigravity" {
		return invalid
	}
	rec := &limitRecord{updated: now()}
	for _, name := range []string{"five_hour", "weekly"} {
		item, ok := data[name].(map[string]any)
		if !ok {
			return invalid
		}
		used, ok := number(item["used_pct"])
		if !ok || used < 0 || used > 100 {
			return invalid
		}
		resets, ok := number(item["resets_at"])
		if !ok || resets < 0 {
			return invalid
		}
		// FormatFloat rounds the exact binary value like Python's round(x, 1).
		rounded, _ := strconv.ParseFloat(strconv.FormatFloat(used, 'f', 1, 64), 64)
		w := window{UsedPct: rounded, ResetsAt: resets}
		if name == "five_hour" {
			rec.fiveHour = w
		} else {
			rec.weekly = w
		}
	}
	mu.Lock()
	limits[agent] = rec
	mu.Unlock()
	return nil
}

// limitsSnapshot: caller holds mu. Stale readings are dropped.
func limitsSnapshot(t float64) map[string]map[string]windowOut {
	out := map[string]map[string]windowOut{}
	for agent, r := range limits {
		if t-r.updated > limitsTTL {
			delete(limits, agent)
			continue
		}
		out[agent] = map[string]windowOut{
			"five_hour": {r.fiveHour, max(0, int(r.fiveHour.ResetsAt-t))},
			"weekly":    {r.weekly, max(0, int(r.weekly.ResetsAt-t))},
		}
	}
	return out
}

func snapshot(limit int) map[string]any {
	t := now()
	mu.Lock()
	for k, r := range state {
		ttl := float64(activeTTL)
		switch r.Status {
		case "done", "idle", "notice", "error":
			ttl = doneTTL
		}
		if t-r.Updated > ttl {
			delete(state, k)
		}
	}
	all := make([]record, 0, len(state))
	for _, r := range state {
		all = append(all, *r)
	}
	lim := limitsSnapshot(t)
	mu.Unlock()

	active, attention := 0, 0
	for _, r := range all {
		switch r.Status {
		case "working":
			active++
		case "needs_attention":
			attention++
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if pi, pj := priority[all[i].Status], priority[all[j].Status]; pi != pj {
			return pi < pj
		}
		return all[i].Updated > all[j].Updated
	})
	total := len(all)
	if len(all) > limit {
		all = all[:limit]
	}
	sessions := make([]sessionOut, 0, len(all))
	for _, r := range all {
		end := r.Updated
		if r.Status == "working" || r.Status == "needs_attention" {
			end = t
		}
		d := max(0, int(end-r.Started))
		waiting := 0
		if r.Status == "needs_attention" {
			waiting = max(0, int(t-r.Since))
		}
		sessions = append(sessions, sessionOut{record: r, Duration: d,
			FinishedLong: r.Status == "done" && d >= longTaskSeconds, Waiting: waiting})
	}
	return map[string]any{"sessions": sessions, "limits": lim, "active_count": active,
		"attention_count": attention, "total_count": total}
}

func send(w http.ResponseWriter, code int, obj any) {
	payload, _ := json.Marshal(obj)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(payload)))
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "close")
	w.WriteHeader(code)
	w.Write(payload)
}

func handler(token string) http.Handler {
	authorized := func(r *http.Request) bool {
		return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Monitor-Key")), []byte(token)) == 1
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.RequestURI // like Python's self.path, the query string is part of it
		switch r.Method {
		case http.MethodGet:
			if path == "/health" {
				send(w, 200, map[string]bool{"ok": true})
				return
			}
			if !authorized(r) {
				send(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			switch path {
			case "/api/sessions":
				send(w, 200, snapshot(maxEvents))
			case "/api/status":
				send(w, 200, snapshot(8))
			default:
				send(w, 404, map[string]string{"error": "not found"})
			}
		case http.MethodPost:
			if !authorized(r) {
				send(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			if path != "/api/event" && path != "/api/events" && path != "/api/limits" {
				send(w, 404, map[string]string{"error": "not found"})
				return
			}
			length := 0
			if raw := r.Header.Get("Content-Length"); raw != "" {
				n, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil {
					send(w, 400, map[string]string{"error": "invalid event"})
					return
				}
				length = n
			}
			if length <= 0 || length > maxBody {
				send(w, 413, map[string]string{"error": "body must be 1-4096 bytes"})
				return
			}
			body := make([]byte, length)
			var data map[string]any
			if _, err := io.ReadFull(r.Body, body); err != nil || json.Unmarshal(body, &data) != nil || data == nil {
				send(w, 400, map[string]string{"error": "invalid event"})
				return
			}
			post := postEvent
			if path == "/api/limits" {
				post = postLimits
			}
			if post(data) != nil {
				send(w, 400, map[string]string{"error": "invalid event"})
				return
			}
			send(w, 200, map[string]bool{"ok": true})
		default:
			// BaseHTTPRequestHandler answers methods without a do_ handler with 501.
			http.Error(w, "Unsupported method", http.StatusNotImplemented)
		}
	})
}

// capListener keeps at most maxConnections connections alive, like MonitorServer.slots. Unlike the Python
// server, which accepts and then closes the excess without a response, the excess here waits in the kernel
// backlog until a slot frees (what Python's slow accept loop effectively does under load), so a benchmark
// does not count turned-away clients as failures.
type capListener struct {
	net.Listener
	slots chan struct{}
}

type slotConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *slotConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

func (l *capListener) Accept() (net.Conn, error) {
	l.slots <- struct{}{} // wait for a free slot first: extra clients queue in the kernel backlog
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &slotConn{Conn: c, release: func() { <-l.slots }}, nil
}

func loadConfig(path string) (token string, port int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("missing %s", path)
	}
	var cfg struct {
		Token string `json:"token"`
		Port  *int   `json:"port"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Token) < 24 {
		return "", 0, fmt.Errorf("invalid or weak token in %s", path)
	}
	port = 8765
	if cfg.Port != nil {
		port = *cfg.Port
	}
	return cfg.Token, port, nil
}

func main() {
	token, port, err := loadConfig("config.local.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bind := os.Getenv("MONITOR_BIND")
	if bind == "" {
		bind = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("AI monitor bridge (Go port) listening on %s:%d\n", bind, port)
	srv := &http.Server{Handler: handler(token), ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second}
	srv.SetKeepAlivesEnabled(false)
	if err := srv.Serve(&capListener{Listener: ln, slots: make(chan struct{}, maxConnections)}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
