package main

// Black-box parity test. It talks HTTP to any running server (Python or Go) given by -url/-token, so the
// identical table of requests proves both servers behave the same. It mutates the server state: run it
// only against a throwaway instance with a fresh state.
//
//	go test -c -o parity.test ./goserver && ./parity.test -test.run Parity -url http://127.0.0.1:18765 -token T

import (
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	urlFlag     = flag.String("url", "http://127.0.0.1:8765", "server under test")
	tokenFlag   = flag.String("token", "", "X-Monitor-Key of the server under test")
	skipCap     = flag.Bool("skip-cap", false, "skip the 16-connection cap check")
	allowRemote = flag.Bool("allow-remote", false, "allow a non-loopback server (it will be modified!)")
)

type resp struct {
	status int
	body   map[string]any
}

func call(t *testing.T, method, path, body, tok string) resp {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
	req, err := http.NewRequest(method, strings.TrimRight(*urlFlag, "/")+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("X-Monitor-Key", tok)
	}
	r, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	out := resp{status: r.StatusCode}
	json.Unmarshal(raw, &out.body) // non-JSON bodies (the 501 page) leave body nil
	return out
}

func ts() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func ev(agent, sid, status string, stamp float64, extra map[string]any) string {
	m := map[string]any{"agent": agent, "session_id": sid, "status": status, "timestamp": stamp}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func sessions(body map[string]any) []map[string]any {
	var out []map[string]any
	list, _ := body["sessions"].([]any)
	for _, s := range list {
		out = append(out, s.(map[string]any))
	}
	return out
}

func find(list []map[string]any, sid string) map[string]any {
	for _, s := range list {
		if s["session_id"] == sid {
			return s
		}
	}
	return nil
}

func ids(list []map[string]any) []string {
	var out []string
	for _, s := range list {
		out = append(out, s["session_id"].(string))
	}
	return out
}

func TestParity(t *testing.T) {
	u, err := url.Parse(*urlFlag)
	if err != nil || *tokenFlag == "" {
		t.Fatal("need -url and -token")
	}
	if ip := net.ParseIP(u.Hostname()); !*allowRemote && u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatal("refusing non-loopback server without -allow-remote: this test rewrites its state")
	}
	tok := *tokenFlag
	expect := func(name string, r resp, status int, errText string) {
		t.Helper()
		if r.status != status {
			t.Errorf("%s: status %d, want %d", name, r.status, status)
		}
		if errText != "" && r.body["error"] != errText {
			t.Errorf("%s: error %v, want %q", name, r.body["error"], errText)
		}
	}
	post := func(name, body string, status int) {
		t.Helper()
		expect(name, call(t, "POST", "/api/events", body, tok), status, "")
	}
	status := func() map[string]any { return call(t, "GET", "/api/status", "", tok).body }
	all := func() []map[string]any { return sessions(call(t, "GET", "/api/sessions", "", tok).body) }

	// 1-2. health and authentication.
	if r := call(t, "GET", "/health", "", ""); r.status != 200 || r.body["ok"] != true {
		t.Errorf("health: %+v", r)
	}
	expect("wrong key", call(t, "GET", "/api/status", "", "wrong"), 401, "unauthorized")
	expect("no key sessions", call(t, "GET", "/api/sessions", "", ""), 401, "unauthorized")
	expect("no key post", call(t, "POST", "/api/events", ev("Codex", "x", "working", ts(), nil), ""), 401, "unauthorized")
	expect("auth before path", call(t, "POST", "/nope", "{}", ""), 401, "unauthorized")
	expect("query string is part of the path", call(t, "GET", "/health?x=1", "", ""), 401, "unauthorized")
	expect("unknown GET", call(t, "GET", "/nope", "", tok), 404, "not found")
	expect("unknown POST", call(t, "POST", "/nope", "{}", tok), 404, "not found")
	expect("unsupported method", call(t, "PUT", "/api/events", "{}", tok), 501, "")

	// 3. invalid payloads change nothing.
	now := ts()
	for name, body := range map[string]string{
		"broken json": "{", "array": "[]", "null": "null", "empty object": "{}", "int session": `{"session_id":42}`,
		"bad status":      ev("Codex", "a", "bogus", now, nil),
		"missing agent":   `{"session_id":"a","status":"working"}`,
		"blank agent":     ev("   ", "a", "working", now, nil),
		"blank session":   ev("Codex", "   ", "working", now, nil),
		"project number":  ev("Codex", "a", "working", now, map[string]any{"project": 123}),
		"detail null":     ev("Codex", "a", "working", now, map[string]any{"detail": nil}),
		"timestamp bool":  ev("Codex", "a", "working", 0, map[string]any{"timestamp": true}),
		"timestamp text":  ev("Codex", "a", "working", 0, map[string]any{"timestamp": "123"}),
		"timestamp null":  ev("Codex", "a", "working", 0, map[string]any{"timestamp": nil}),
		"timestamp stale": ev("Codex", "a", "working", now-3601, nil),
		"timestamp early": ev("Codex", "a", "working", now+61, nil),
	} {
		post(name, body, 400)
	}
	expect("oversize", call(t, "POST", "/api/events", strings.Repeat("{", 4097), tok), 413, "body must be 1-4096 bytes")
	expect("empty body", call(t, "POST", "/api/events", "", tok), 413, "body must be 1-4096 bytes")
	if r := status(); r["total_count"] != 0.0 {
		t.Errorf("rejected requests left state behind: %v", r)
	} else if list, ok := r["sessions"].([]any); !ok || len(list) != 0 {
		t.Errorf("empty store must serialise sessions as [], got %v", r["sessions"])
	}

	// 4. fixtures: one session per status, ordering, counters and the response schema.
	tA := now - 10
	post("A", ev("Codex", "s1", "working", tA, nil), 200)
	r := call(t, "POST", "/api/event", ev("Claude Code", "s2", "needs_attention", now-5, nil), tok)
	expect("alias /api/event", r, 200, "")
	post("C", ev("Gemini CLI", "s3", "error", now-4, nil), 200)
	post("D", ev("Codex", "s4", "done", now-3, nil), 200)
	post("E", ev("Codex", "s5", "idle", now-2, nil), 200)
	post("F", ev("Codex", "s6", "notice", now-1, nil), 200)
	st := status()
	if got := strings.Join(ids(sessions(st)), ","); got != "s2,s3,s1,s6,s4,s5" {
		t.Errorf("order %s, want s2,s3,s1,s6,s4,s5", got)
	}
	if st["active_count"] != 1.0 || st["attention_count"] != 1.0 || st["total_count"] != 6.0 {
		t.Errorf("counters %v/%v/%v, want 1/1/6", st["active_count"], st["attention_count"], st["total_count"])
	}
	var topKeys []string
	for k := range st {
		topKeys = append(topKeys, k)
	}
	sort.Strings(topKeys)
	if got := strings.Join(topKeys, ","); got != "active_count,attention_count,limits,sessions,total_count" {
		t.Errorf("top-level keys %s", got)
	}
	first := sessions(st)[0]
	var keys []string
	for k := range first {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "agent,detail,duration,finished_long,project,session_id,since,started,status,updated,waiting" {
		t.Errorf("session keys %s", got)
	}
	cur := all()
	if a := find(cur, "s1"); a["project"] != "Unknown project" || a["detail"] != "" || a["duration"].(float64) < 10 || a["duration"].(float64) > 13 {
		t.Errorf("A defaults/duration wrong: %v", a)
	}
	if b := find(cur, "s2"); b["waiting"].(float64) < 5 || b["waiting"].(float64) > 8 {
		t.Errorf("B waiting wrong: %v", b)
	}
	if d := find(cur, "s4"); d["duration"] != 0.0 || d["finished_long"] != false {
		t.Errorf("D duration/finished_long wrong: %v", d)
	}

	// 5. an event that is not newer is ignored (still 200); turns and per-status "since".
	post("same timestamp", ev("Codex", "s1", "done", tA, nil), 200)
	post("older", ev("Codex", "s1", "done", tA-5, nil), 200)
	if a := find(all(), "s1"); a["status"] != "working" || a["updated"] != tA {
		t.Errorf("old events must be ignored: %v", a)
	}
	post("same turn", ev("Codex", "s1", "working", tA+3, nil), 200)
	if a := find(all(), "s1"); a["started"] != tA || a["since"] != tA {
		t.Errorf("same status keeps started/since: %v", a)
	}
	post("finish", ev("Codex", "s1", "done", tA+4, nil), 200)
	if a := find(all(), "s1"); a["started"] != tA || a["since"] != tA+4 || a["duration"] != 4.0 {
		t.Errorf("done keeps started, resets since, freezes duration: %v", a)
	}
	post("new turn", ev("Codex", "s1", "working", tA+5, nil), 200)
	if a := find(all(), "s1"); a["started"] != tA+5 || a["since"] != tA+5 {
		t.Errorf("working after done starts a new turn: %v", a)
	}

	// 6. long tasks.
	post("G start", ev("Codex", "s7", "working", ts()-400, nil), 200)
	post("G end", ev("Codex", "s7", "done", ts()-1, nil), 200)
	if g := find(all(), "s7"); g["finished_long"] != true || g["duration"].(float64) < 398 || g["duration"].(float64) > 400 {
		t.Errorf("G should be a long finished task: %v", g)
	}

	// 7. truncation counts characters, not bytes; newlines become spaces.
	longSID := "s" + strings.Repeat("é", 99)
	post("truncation", ev(strings.Repeat("É", 30), longSID, "working", ts()-6, map[string]any{
		"project": strings.Repeat("p", 50), "detail": "line1\nline2" + strings.Repeat("d", 100)}), 200)
	wantSID := string([]rune(longSID)[:80])
	if p := find(all(), wantSID); p == nil {
		t.Errorf("session id was not cut to 80 characters")
	} else {
		if p["agent"] != string([]rune(strings.Repeat("É", 30))[:24]) || p["project"] != strings.Repeat("p", 40) {
			t.Errorf("agent/project truncation wrong: %v / %v", p["agent"], p["project"])
		}
		if d, _ := p["detail"].(string); len([]rune(d)) != 66 || !strings.HasPrefix(d, "line1 line2d") {
			t.Errorf("detail truncation/newline wrong: %q", d)
		}
	}

	// 8. limits.
	reset := float64(time.Now().Unix())
	lim := func(agent string, used any, resets any, weekly any) string {
		b, _ := json.Marshal(map[string]any{"agent": agent,
			"five_hour": map[string]any{"used_pct": used, "resets_at": resets}, "weekly": weekly})
		return string(b)
	}
	okWeekly := map[string]any{"used_pct": 98, "resets_at": reset + 400000}
	limit := func(name, body string, want int) {
		t.Helper()
		expect(name, call(t, "POST", "/api/limits", body, tok), want, "")
	}
	limit("valid", lim("Codex", 31.25, reset+3600, okWeekly), 200)
	limit("Antigravity is accepted by the code", lim("Antigravity", 0, 0, okWeekly), 200)
	limit("upper bound", lim("Claude Code", 100, reset, okWeekly), 200)
	limit("unknown agent", lim("Gemini CLI", 1, reset, okWeekly), 400)
	limit("over 100", lim("Codex", 101, reset, okWeekly), 400)
	limit("negative", lim("Codex", -1, reset, okWeekly), 400)
	limit("text percentage", lim("Codex", "x", reset, okWeekly), 400)
	limit("bool percentage", lim("Codex", true, reset, okWeekly), 400)
	limit("negative reset", lim("Codex", 1, -1, okWeekly), 400)
	limit("missing weekly", lim("Codex", 1, reset, nil), 400)
	limit("weekly not an object", lim("Codex", 1, reset, "x"), 400)
	limits, _ := status()["limits"].(map[string]any)
	codex, _ := limits["Codex"].(map[string]any)
	five, _ := codex["five_hour"].(map[string]any)
	week, _ := codex["weekly"].(map[string]any)
	if five["used_pct"] != 31.2 || week["used_pct"] != 98.0 {
		t.Errorf("used_pct must be rounded like Python round(x, 1): %v / %v", five["used_pct"], week["used_pct"])
	}
	if in, _ := five["resets_in"].(float64); in < 3590 || in > 3600 {
		t.Errorf("resets_in %v, want about 3600", five["resets_in"])
	}
	if _, ok := limits["Antigravity"].(map[string]any)["five_hour"].(map[string]any)["resets_in"]; !ok {
		t.Errorf("resets_in must be present even when 0")
	}

	// 9. store cap: the 40 most recently updated sessions survive, whatever the insertion order.
	model := map[string]float64{"s1": tA + 5, "s2": now - 5, "s3": now - 4, "s4": now - 3, "s5": now - 2, "s6": now - 1, wantSID: 0}
	for _, s := range all() {
		model[s["session_id"].(string)] = s["updated"].(float64)
	}
	oldStamp := ts() - 3000
	post("old", ev("Codex", "old-1", "working", oldStamp, nil), 200)
	model["old-1"] = oldStamp
	base := ts() - 100
	for i := 0; i < 50; i++ {
		stamp := base + float64(i)*0.01
		post("ev", ev("Codex", "ev-"+strconv.Itoa(i), "working", stamp, nil), 200)
		model["ev-"+strconv.Itoa(i)] = stamp
	}
	type pair struct {
		sid string
		at  float64
	}
	var ranked []pair
	for sid, at := range model {
		ranked = append(ranked, pair{sid, at})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].at > ranked[j].at })
	want := map[string]bool{}
	for _, p := range ranked[:40] {
		want[p.sid] = true
	}
	list := all()
	if len(list) != 40 || status()["total_count"] != 40.0 {
		t.Errorf("store holds %d sessions, want 40", len(list))
	}
	for _, s := range list {
		if !want[s["session_id"].(string)] {
			t.Errorf("session %v should have been evicted", s["session_id"])
		}
	}
	if find(list, "old-1") != nil {
		t.Errorf("the oldest session must be evicted first")
	}
	if n := len(sessions(status())); n != 8 {
		t.Errorf("/api/status returns %d sessions, want 8", n)
	}

	// 10. at most 16 concurrent connections: while 16 are held open, a 17th gets no response (Python closes
	// it, the Go port leaves it waiting) and the server serves again once they are released.
	if *skipCap {
		return
	}
	var held []net.Conn
	for i := 0; i < 16; i++ {
		c, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	time.Sleep(300 * time.Millisecond)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
	if r, err := client.Get(*urlFlag + "/health"); err == nil {
		r.Body.Close()
		t.Errorf("connection #17 got HTTP %d, want no response while 16 connections are open", r.StatusCode)
	}
	for _, c := range held {
		c.Close()
	}
	recovered := false
	for i := 0; i < 30 && !recovered; i++ {
		time.Sleep(100 * time.Millisecond)
		if r, err := client.Get(*urlFlag + "/health"); err == nil {
			r.Body.Close()
			recovered = r.StatusCode == 200
		}
	}
	if !recovered {
		t.Errorf("server did not accept connections again after the idle ones closed")
	}
}
