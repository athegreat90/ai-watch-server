package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// env returns a getenv function backed by a map.
func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// freePort returns a TCP port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// writeConfig creates a valid config on a free port and returns its path and port.
func writeConfig(t *testing.T) (string, int) {
	t.Helper()
	port := freePort(t)
	path := filepath.Join(t.TempDir(), "config.local.json")
	body := fmt.Sprintf(`{"token":"%s","port":%d}`, strings.Repeat("k", 32), port)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, port
}

// runCmd runs the command and captures its output.
func runCmd(ctx context.Context, e map[string]string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(ctx, args, env(e), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionAndUnknownCommand(t *testing.T) {
	if code, out, _ := runCmd(context.Background(), nil, "version"); code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("version: %d %q", code, out)
	}
	if code, _, errOut := runCmd(context.Background(), nil, "bogus"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown: %d %q", code, errOut)
	}
}

func TestInitConfigCreatesOnceAndNeverPrintsToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	code, out, _ := runCmd(context.Background(), nil, "init-config", "--config", path)
	if code != 0 || !strings.HasPrefix(out, "created ") {
		t.Fatalf("first: %d %q", code, out)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(out, string(raw[strings.Index(string(raw), `"token":"`)+9:][:20])) {
		t.Fatal("token printed")
	}
	if code, out, _ = runCmd(context.Background(), map[string]string{"AI_WATCH_CONFIG": path}, "init-config"); code != 0 || !strings.HasPrefix(out, "kept existing ") {
		t.Fatalf("second: %d %q", code, out)
	}
}

func TestInitConfigFailureAndBadFlag(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	if code, _, errOut := runCmd(context.Background(), nil, "init-config", "--config", filepath.Join(blocker, "c.json")); code != 1 || errOut == "" {
		t.Fatalf("failure: %d %q", code, errOut)
	}
	for _, sub := range []string{"init-config", "serve", "healthcheck"} {
		if code, _, _ := runCmd(context.Background(), nil, sub, "--nope"); code != 2 {
			t.Fatalf("%s bad flag: %d", sub, code)
		}
	}
}

func TestServeConfigurationErrors(t *testing.T) {
	path, _ := writeConfig(t)
	tests := []struct {
		name string
		env  map[string]string
		args []string
	}{
		{"missing config", nil, []string{"--config", filepath.Join(t.TempDir(), "none.json")}},
		{"bad read timeout", map[string]string{"MONITOR_READ_TIMEOUT": "soon"}, []string{"--config", path}},
		{"negative write timeout", map[string]string{"MONITOR_WRITE_TIMEOUT": "-1s"}, []string{"--config", path}},
		{"unusable bind", map[string]string{"MONITOR_BIND": "256.256.256.256"}, []string{"--config", path}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := runCmd(context.Background(), tc.env, append([]string{"serve"}, tc.args...)...)
			if code != 1 || !strings.Contains(errOut, `"level":"ERROR"`) {
				t.Fatalf("%d %q", code, errOut)
			}
			if strings.Contains(errOut, strings.Repeat("k", 32)) {
				t.Fatal("log leaks the token")
			}
		})
	}
}

func TestServeHealthcheckAndGracefulShutdown(t *testing.T) {
	path, _ := writeConfig(t)
	if code, _, _ := runCmd(context.Background(), nil, "healthcheck", "--config", path); code != 1 {
		t.Fatalf("healthcheck must fail while nothing listens, got %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		code, _, _ := runCmd(ctx, map[string]string{"MONITOR_READ_TIMEOUT": "2s", "MONITOR_WRITE_TIMEOUT": "2s"}, "--config", path)
		done <- code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if code, _, _ := runCmd(context.Background(), nil, "healthcheck", "--config", path); code == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never became healthy")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop on cancel")
	}
}

func TestHealthcheckRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "c.json")
	_ = os.WriteFile(path, []byte(fmt.Sprintf(`{"port":%d}`, srv.Listener.Addr().(*net.TCPAddr).Port)), 0o600)
	if code, _, errOut := runCmd(context.Background(), nil, "healthcheck", "--config", path); code != 1 || !strings.Contains(errOut, "503") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestFirstError(t *testing.T) {
	if firstError(nil, nil) != nil {
		t.Fatal("want nil")
	}
	want := fmt.Errorf("x")
	if firstError(nil, want, fmt.Errorf("y")) != want {
		t.Fatal("want first error")
	}
}
