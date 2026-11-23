package checkapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/athegreat90/ai-watch-server/internal/httpapi"
	"github.com/athegreat90/ai-watch-server/internal/monitor"
)

const token = "0123456789abcdef0123456789abcdef"

// realServer starts the production handler with one session and one limit reading.
func realServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := monitor.NewStore(nil)
	_ = store.PostEvent(map[string]any{"session_id": "s", "agent": "Codex", "status": "working"})
	srv := httptest.NewServer(httpapi.NewHandler(store, token))
	t.Cleanup(srv.Close)
	return srv
}

// statuses returns check name -> status for a report.
func statuses(r Report) map[string]string {
	out := map[string]string{}
	for _, c := range r.Checks {
		out[c.Name] = c.Status
	}
	return out
}

func TestRunAgainstRealServerPasses(t *testing.T) {
	srv := realServer(t)
	report := Run(context.Background(), Options{URL: srv.URL + "/", Token: token})
	if !report.OK || report.ExitCode != ExitOK || len(report.Checks) != 5 {
		t.Fatalf("%+v", report)
	}
	for _, c := range report.Checks {
		if c.Status != Pass {
			t.Fatalf("%+v", c)
		}
	}
	if report.URL != srv.URL {
		t.Fatalf("trailing slash not trimmed: %q", report.URL)
	}
}

func TestRunWrongTokenFailsAuthenticatedChecksOnly(t *testing.T) {
	srv := realServer(t)
	report := Run(context.Background(), Options{URL: srv.URL, Token: "x" + token})
	got := statuses(report)
	if report.ExitCode != ExitFailed || got["health"] != Pass || got["anonymous-rejected"] != Pass ||
		got["invalid-token-rejected"] != Pass || got["status"] != Fail || got["sessions"] != Fail {
		t.Fatalf("%+v", report)
	}
	for _, c := range report.Checks {
		if strings.Contains(c.Detail, token) {
			t.Fatal("detail leaks the token")
		}
	}
}

func TestRunUnreachableSkipsRemainingChecks(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	report := Run(context.Background(), Options{URL: url, Token: token, Timeout: time.Second})
	got := statuses(report)
	if report.ExitCode != ExitUnreachable || got["health"] != Fail || got["status"] != Skip || got["sessions"] != Skip {
		t.Fatalf("%+v", report)
	}
}

func TestRunTimeoutIsUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	report := Run(context.Background(), Options{URL: srv.URL, Token: token, Timeout: 100 * time.Millisecond})
	if report.ExitCode != ExitUnreachable || !strings.Contains(report.Checks[0].Detail, "deadline") {
		t.Fatalf("%+v", report.Checks[0])
	}
}

func TestRunInvalidURL(t *testing.T) {
	report := Run(context.Background(), Options{URL: "http://[::1", Token: token})
	if report.OK || report.ExitCode != ExitFailed || !strings.Contains(report.Checks[0].Detail, "invalid URL") {
		t.Fatalf("%+v", report)
	}
}

// stub serves fixed answers per path for negative tests.
func stub(t *testing.T, answers map[string]func(http.ResponseWriter, *http.Request)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := answers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// reply returns a handler that writes a status and body.
func reply(code int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

func TestRunDetectsBrokenServers(t *testing.T) {
	good := `{"sessions":[],"limits":{},"active_count":0,"attention_count":0,"total_count":0}`
	tests := []struct {
		name    string
		answers map[string]func(http.ResponseWriter, *http.Request)
		failing string
	}{
		{"health wrong body", map[string]func(http.ResponseWriter, *http.Request){"/health": reply(200, `{"ok":false}`)}, "health"},
		{"health wrong status", map[string]func(http.ResponseWriter, *http.Request){"/health": reply(500, ``)}, "health"},
		{"no auth enforced", map[string]func(http.ResponseWriter, *http.Request){
			"/health": reply(200, `{"ok":true}`), "/api/status": reply(200, good)}, "anonymous-rejected"},
		{"auth redirect", map[string]func(http.ResponseWriter, *http.Request){
			"/health": reply(200, `{"ok":true}`), "/api/status": func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/login", http.StatusFound)
			}}, "anonymous-rejected"},
		{"snapshot not json", map[string]func(http.ResponseWriter, *http.Request){
			"/health": reply(200, `{"ok":true}`), "/api/status": func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Monitor-Key") == token {
					reply(200, `<html>`)(w, r)
					return
				}
				reply(401, `{}`)(w, r)
			}}, "status"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := Run(context.Background(), Options{URL: stub(t, tc.answers), Token: token, Timeout: time.Second})
			if report.OK || statuses(report)[tc.failing] != Fail {
				t.Fatalf("%+v", report)
			}
		})
	}
}

func TestValidateSnapshot(t *testing.T) {
	session := `{"session_id":"s","agent":"a","project":"p","status":"working","detail":"","updated":1,"started":1,"since":1,"duration":0,"finished_long":false,"waiting":0}`
	wrap := func(sessions, extra string) string {
		return `{"sessions":` + sessions + `,"limits":{},"active_count":0,"attention_count":0,"total_count":0` + extra + `}`
	}
	tests := []struct {
		name, body string
		want       int
		wantErr    string
	}{
		{"empty", wrap(`[]`, ``), 0, ""},
		{"one session", wrap(`[`+session+`]`, ``), 1, ""},
		{"not object", `[]`, 0, "JSON object"},
		{"sessions null", wrap(`null`, ``), 0, "sessions"},
		{"sessions missing", `{"limits":{},"active_count":0,"attention_count":0,"total_count":0}`, 0, "sessions"},
		{"session missing field", wrap(`[{"session_id":"s"}]`, ``), 0, "session 0"},
		{"session wrong type", wrap(`[`+strings.Replace(session, `"duration":0`, `"duration":"0"`, 1)+`]`, ``), 0, "sessions"},
		{"limits missing", `{"sessions":[],"active_count":0,"attention_count":0,"total_count":0}`, 0, "limits"},
		{"limits array", `{"sessions":[],"limits":[],"active_count":0,"attention_count":0,"total_count":0}`, 0, "limits"},
		{"counter negative", `{"sessions":[],"limits":{},"active_count":-1,"attention_count":0,"total_count":0}`, 0, "active_count"},
		{"counter string", `{"sessions":[],"limits":{},"active_count":0,"attention_count":"0","total_count":0}`, 0, "attention_count"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, err := validateSnapshot([]byte(tc.body))
			if tc.wantErr == "" {
				if err != nil || n != tc.want {
					t.Fatalf("n=%d err=%v", n, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want %q", err, tc.wantErr)
			}
		})
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault(0, time.Second) != time.Second || orDefault(time.Minute, time.Second) != time.Minute {
		t.Fatal("orDefault")
	}
}
