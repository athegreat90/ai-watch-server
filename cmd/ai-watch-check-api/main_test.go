package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/athegreat90/ai-watch-server/internal/checkapi"
	"github.com/athegreat90/ai-watch-server/internal/httpapi"
	"github.com/athegreat90/ai-watch-server/internal/monitor"
)

const token = "0123456789abcdef0123456789abcdef"

// env returns a getenv function backed by a map.
func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// runCmd runs the checker and captures its output.
func runCmd(e map[string]string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, env(e), &out, &errOut)
	return code, out.String(), errOut.String()
}

// writeFile stores content in a temp file and returns its path.
func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// server starts a real handler and returns its URL.
func server(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(httpapi.NewHandler(monitor.NewStore(nil), token))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTokenFileJSONProvidesURLAndToken(t *testing.T) {
	url := server(t)
	path := writeFile(t, `{"url":"`+url+`","token":"`+token+`"}`)
	code, out, _ := runCmd(nil, "--token-file", path)
	if code != checkapi.ExitOK || !strings.Contains(out, "OK: all 5 checks passed") || strings.Contains(out, token) {
		t.Fatalf("%d %q", code, out)
	}
}

func TestRawTokenFileAndVerbose(t *testing.T) {
	url := server(t)
	code, out, _ := runCmd(nil, "--url", url, "--token-file", writeFile(t, token+"\n"), "--verbose")
	if code != 0 || !strings.Contains(out, "PASS  health: HTTP 200 (") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestEnvironmentToken(t *testing.T) {
	url := server(t)
	if code, out, _ := runCmd(map[string]string{"AI_WATCH_TOKEN": token}, "--url", url); code != 0 {
		t.Fatalf("%d %q", code, out)
	}
}

func TestJSONOutput(t *testing.T) {
	url := server(t)
	code, out, _ := runCmd(map[string]string{"AI_WATCH_TOKEN": token}, "--url", url, "--json")
	var report checkapi.Report
	if code != 0 || json.Unmarshal([]byte(out), &report) != nil || !report.OK || len(report.Checks) != 5 {
		t.Fatalf("%d %q", code, out)
	}
	if strings.Contains(out, token) {
		t.Fatal("JSON leaks the token")
	}
}

func TestWrongTokenExitsOne(t *testing.T) {
	url := server(t)
	code, out, _ := runCmd(map[string]string{"AI_WATCH_TOKEN": "x" + token}, "--url", url)
	if code != checkapi.ExitFailed || !strings.Contains(out, "FAIL  status") || !strings.Contains(out, "FAILED: 2 of 5") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestUnreachableExitsThree(t *testing.T) {
	srv := httptest.NewServer(nil)
	url := srv.URL
	srv.Close()
	code, out, _ := runCmd(map[string]string{"AI_WATCH_TOKEN": token}, "--url", url, "--timeout", "1s")
	if code != checkapi.ExitUnreachable || !strings.Contains(out, "FAILED: server unreachable") || !strings.Contains(out, "SKIP") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	empty := writeFile(t, "  \n")
	badJSON := writeFile(t, `{"url":"x"}`)
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"no token", nil, nil, "no token"},
		{"missing file", nil, []string{"--token-file", filepath.Join(t.TempDir(), "none")}, "cannot read"},
		{"empty file", nil, []string{"--token-file", empty}, "is empty"},
		{"json without token", nil, []string{"--token-file", badJSON}, "no \"token\""},
		{"bad timeout", map[string]string{"AI_WATCH_TOKEN": token}, []string{"--timeout", "0s"}, "Usage"},
		{"extra argument", map[string]string{"AI_WATCH_TOKEN": token}, []string{"oops"}, "Usage"},
		{"unknown flag", nil, []string{"--token", "secret"}, "not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := runCmd(tc.env, tc.args...)
			if code != checkapi.ExitUsage || !strings.Contains(errOut, tc.want) {
				t.Fatalf("%d %q", code, errOut)
			}
		})
	}
}

func TestHelpAndVersion(t *testing.T) {
	if code, _, errOut := runCmd(nil, "--help"); code != 0 || !strings.Contains(errOut, "Exit codes") {
		t.Fatalf("help %d %q", code, errOut)
	}
	if code, out, _ := runCmd(nil, "--version"); code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("version %d %q", code, out)
	}
}

func TestDefaultURLWhenNothingElseGiven(t *testing.T) {
	// Nothing listens on the default port in tests, so the run ends unreachable; what matters is
	// that the default URL is the one reported.
	code, out, _ := runCmd(map[string]string{"AI_WATCH_TOKEN": token}, "--timeout", "200ms")
	if !strings.Contains(out, "Checking "+defaultURL) || (code != checkapi.ExitUnreachable && code != checkapi.ExitOK) {
		t.Fatalf("%d %q", code, out)
	}
}
