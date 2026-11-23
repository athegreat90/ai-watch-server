package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/athegreat90/ai-watch-server/internal/monitor"
)

const testToken = "0123456789abcdef0123456789abcdef"

// newTestServer starts the handler on an httptest server backed by a fresh store.
func newTestServer(t *testing.T) (*httptest.Server, *monitor.Store) {
	t.Helper()
	store := monitor.NewStore(nil)
	srv := httptest.NewServer(NewHandler(store, testToken))
	t.Cleanup(srv.Close)
	return srv, store
}

// do sends one request and returns the status code and body.
func do(t *testing.T, method, url, token, body string) (int, string, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(AuthHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data), resp.Header
}

func TestRoutingAndAuthentication(t *testing.T) {
	srv, _ := newTestServer(t)
	valid := `{"session_id":"s","agent":"Codex","status":"working"}`
	tests := []struct {
		name, method, path, token, body string
		code                            int
		wantBody                        string
	}{
		{"health is public", "GET", "/health", "", "", 200, `{"ok":true}`},
		{"health ignores token", "GET", "/health", "wrong", "", 200, `{"ok":true}`},
		{"health with query needs auth", "GET", "/health?x=1", "", "", 401, `{"error":"unauthorized"}`},
		{"status anonymous", "GET", "/api/status", "", "", 401, `{"error":"unauthorized"}`},
		{"status wrong token", "GET", "/api/status", "nope", "", 401, `{"error":"unauthorized"}`},
		{"sessions anonymous", "GET", "/api/sessions", "", "", 401, `{"error":"unauthorized"}`},
		{"unknown GET", "GET", "/api/nope", testToken, "", 404, `{"error":"not found"}`},
		{"unknown GET anonymous is 401", "GET", "/api/nope", "", "", 401, `{"error":"unauthorized"}`},
		{"unknown POST", "POST", "/api/nope", testToken, valid, 404, `{"error":"not found"}`},
		{"POST anonymous", "POST", "/api/events", "", valid, 401, `{"error":"unauthorized"}`},
		{"POST anonymous wrong path is 401", "POST", "/api/nope", "", valid, 401, `{"error":"unauthorized"}`},
		{"events ok", "POST", "/api/events", testToken, valid, 200, `{"ok":true}`},
		{"event alias ok", "POST", "/api/event", testToken, valid, 200, `{"ok":true}`},
		{"empty body", "POST", "/api/events", testToken, "", 413, `{"error":"body must be 1-4096 bytes"}`},
		{"oversize body", "POST", "/api/events", testToken, strings.Repeat("x", MaxBody+1), 413, `{"error":"body must be 1-4096 bytes"}`},
		{"max body but invalid json", "POST", "/api/events", testToken, strings.Repeat("x", MaxBody), 400, `{"error":"invalid event"}`},
		{"invalid json", "POST", "/api/events", testToken, "{", 400, `{"error":"invalid event"}`},
		{"json null", "POST", "/api/events", testToken, "null", 400, `{"error":"invalid event"}`},
		{"json array", "POST", "/api/events", testToken, "[]", 400, `{"error":"invalid event"}`},
		{"invalid status", "POST", "/api/events", testToken, `{"session_id":"s","agent":"a","status":"x"}`, 400, `{"error":"invalid event"}`},
		{"limits invalid", "POST", "/api/limits", testToken, `{"agent":"Codex"}`, 400, `{"error":"invalid event"}`},
		{"limits ok", "POST", "/api/limits", testToken,
			`{"agent":"Codex","five_hour":{"used_pct":1,"resets_at":0},"weekly":{"used_pct":2,"resets_at":0}}`, 200, `{"ok":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body, header := do(t, tc.method, srv.URL+tc.path, tc.token, tc.body)
			if code != tc.code || body != tc.wantBody {
				t.Fatalf("got %d %q want %d %q", code, body, tc.code, tc.wantBody)
			}
			if header.Get("Cache-Control") != "no-store" || header.Get("Content-Type") != "application/json" {
				t.Fatalf("headers %v", header)
			}
		})
	}
}

func TestUnsupportedMethodIs501(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, method := range []string{"PUT", "DELETE", "PATCH"} {
		if code, _, _ := do(t, method, srv.URL+"/api/status", testToken, ""); code != http.StatusNotImplemented {
			t.Fatalf("%s: %d", method, code)
		}
	}
}

func TestSnapshotEndpoints(t *testing.T) {
	srv, store := newTestServer(t)
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		if err := store.PostEvent(map[string]any{"session_id": id, "agent": "Codex", "status": "working"}); err != nil {
			t.Fatal(err)
		}
	}
	type reply struct {
		Sessions []map[string]any `json:"sessions"`
		Total    int              `json:"total_count"`
	}
	for path, want := range map[string]int{"/api/status": 8, "/api/sessions": 10} {
		_, body, _ := do(t, "GET", srv.URL+path, testToken, "")
		var r reply
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Sessions) != want || r.Total != 10 {
			t.Fatalf("%s: %d sessions, total %d", path, len(r.Sessions), r.Total)
		}
	}
	_, body, _ := do(t, "GET", srv.URL+"/api/status", testToken, "")
	for _, key := range []string{`"sessions":[`, `"limits":{}`, `"active_count":10`, `"attention_count":0`, `"total_count":10`,
		`"duration":`, `"finished_long":false`, `"waiting":0`} {
		if !strings.Contains(body, key) {
			t.Fatalf("missing %s in %s", key, body)
		}
	}
}

func TestEmptySnapshotSerialisesAsEmptyList(t *testing.T) {
	srv, _ := newTestServer(t)
	_, body, _ := do(t, "GET", srv.URL+"/api/status", testToken, "")
	if body != `{"sessions":[],"limits":{},"active_count":0,"attention_count":0,"total_count":0}` {
		t.Fatal(body)
	}
}

func TestResponseIsASCIIJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	event := `{"session_id":"s","agent":"Codex","status":"working","project":"ñ<日>&😀\u007f","detail":"a\tb"}`
	if code, body, _ := do(t, "POST", srv.URL+"/api/events", testToken, event); code != 200 {
		t.Fatal(body)
	}
	_, body, _ := do(t, "GET", srv.URL+"/api/status", testToken, "")
	for i := 0; i < len(body); i++ {
		if body[i] < 0x20 || body[i] >= 0x7f {
			t.Fatalf("non-ASCII byte %#x in %s", body[i], body)
		}
	}
	// Built with %c-free string concatenation so the expected escapes are literal backslash sequences.
	bs := string(rune(92))
	escaped := bs + "u00f1<" + bs + "u65e5>&" + bs + "ud83d" + bs + "ude00" + bs + "u007f"
	for _, want := range []string{`"project":"` + escaped + `"`, `"detail":"a\tb"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	var decoded struct{ Sessions []struct{ Project string } }
	if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.Sessions[0].Project != "ñ<日>&😀\x7f" {
		t.Fatalf("round trip: %v %+v", err, decoded)
	}
}

func TestEncodeJSONError(t *testing.T) {
	if _, err := encodeJSON(make(chan int)); err == nil {
		t.Fatal("expected error")
	}
}

func TestSendFallsBackTo500OnEncodeFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	send(rec, 200, make(chan int))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestTruncatedBodyIsInvalid(t *testing.T) {
	srv, _ := newTestServer(t)
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Declare 50 bytes but send 5, then half-close: the handler sees a short body.
	_, _ = conn.Write([]byte("POST /api/events HTTP/1.1\r\nHost: x\r\nX-Monitor-Key: " + testToken + "\r\nContent-Length: 50\r\n\r\n{\"a\":"))
	conn.(*net.TCPConn).CloseWrite()
	reply, _ := io.ReadAll(conn)
	if !bytes.Contains(reply, []byte("400")) || !bytes.Contains(reply, []byte(`{"error":"invalid event"}`)) {
		t.Fatalf("reply %q", reply)
	}
}

// startServe runs Serve on a loopback listener and returns its address, a cancel function and
// a channel that yields Serve's result.
func startServe(t *testing.T, opts Options) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, opts) }()
	t.Cleanup(cancel)
	return ln.Addr().String(), cancel, done
}

func TestServeGracefulShutdown(t *testing.T) {
	addr, cancel, done := startServe(t, Options{Handler: NewHandler(monitor.NewStore(nil), testToken)})
	if code, _, _ := do(t, "GET", "http://"+addr+"/health", "", ""); code != 200 {
		t.Fatalf("health %d", code)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	if _, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		t.Fatal("listener still open after shutdown")
	}
}

func TestServeWaitsForInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		send(w, 200, map[string]bool{"ok": true})
	})
	addr, cancel, done := startServe(t, Options{Handler: slow})
	result := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			result <- -1
			return
		}
		resp.Body.Close()
		result <- resp.StatusCode
	}()
	<-started
	cancel()
	select {
	case <-done:
		t.Fatal("Serve returned before the in-flight request finished")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if code := <-result; code != 200 {
		t.Fatalf("in-flight request got %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServeReturnsListenerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close() // Serving on a closed listener fails immediately.
	if err := Serve(context.Background(), ln, Options{Handler: http.NotFoundHandler()}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestConnectionCap(t *testing.T) {
	addr, _, _ := startServe(t, Options{Handler: NewHandler(monitor.NewStore(nil), testToken), MaxConns: 2, ReadTimeout: 10 * time.Second})
	var held []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	// Wait until the server has accepted both held connections.
	time.Sleep(100 * time.Millisecond)
	third := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/health")
		if err != nil {
			third <- -1
			return
		}
		resp.Body.Close()
		third <- resp.StatusCode
	}()
	select {
	case code := <-third:
		t.Fatalf("third client was served (%d) while the cap was reached", code)
	case <-time.After(300 * time.Millisecond):
	}
	held[0].Close() // free one slot: the queued client must now be served
	select {
	case code := <-third:
		if code != 200 {
			t.Fatalf("queued client got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued client never served after a slot was freed")
	}
	held[1].Close()
}

func TestCapListenerCloseUnblocksAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cl := newCapListener(ln, 1)
	cl.slots <- struct{}{} // no slot left, Accept blocks
	errc := make(chan error, 1)
	go func() { _, err := cl.Accept(); errc <- err }()
	time.Sleep(50 * time.Millisecond)
	_ = cl.Close()
	if err := <-errc; err != net.ErrClosed {
		t.Fatalf("got %v", err)
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault(0, 5) != 5 || orDefault(-1, 5) != 5 || orDefault(3, 5) != 3 || orDefault(time.Second, time.Minute) != time.Second {
		t.Fatal("orDefault")
	}
}
