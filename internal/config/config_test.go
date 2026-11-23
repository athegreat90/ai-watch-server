package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// write stores content in a temp file and returns its path.
func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.local.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	token := strings.Repeat("t", MinTokenLength)
	tests := []struct {
		name, content string
		wantPort      int
		wantErr       string
	}{
		{"defaults port", `{"token":"` + token + `"}`, DefaultPort, ""},
		{"explicit port", `{"token":"` + token + `","port":9000}`, 9000, ""},
		{"extra url ignored", `{"url":"http://x","token":"` + token + `"}`, DefaultPort, ""},
		{"short token", `{"token":"short"}`, 0, "weak token"},
		{"empty token", `{"url":"x","token":""}`, 0, "weak token"},
		{"missing token", `{}`, 0, "weak token"},
		{"token not string", `{"token":5}`, 0, "invalid JSON"},
		{"not json", `nope`, 0, "invalid JSON"},
		{"port zero", `{"token":"` + token + `","port":0}`, 0, "invalid port"},
		{"port too big", `{"token":"` + token + `","port":70000}`, 0, "invalid port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(write(t, tc.content))
			if tc.wantErr == "" {
				if err != nil || cfg.Port != tc.wantPort || cfg.Token != token {
					t.Fatalf("got %+v, %v", cfg, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err %v, want %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatal("error leaks the token")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil || !strings.Contains(err.Error(), "init-config") {
		t.Fatalf("err %v", err)
	}
}

func TestPort(t *testing.T) {
	tests := []struct {
		name, content string
		want          int
	}{
		{"explicit", `{"port":9100}`, 9100},
		{"absent", `{}`, DefaultPort},
		{"invalid", `{"port":-1}`, DefaultPort},
		{"not json", `x`, DefaultPort},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Port(write(t, tc.content)); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
	if Port(filepath.Join(t.TempDir(), "absent.json")) != DefaultPort {
		t.Fatal("missing file must give the default port")
	}
}

func TestNewTokenIsRandomAndLongEnough(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b || len(a) != 43 || strings.ContainsAny(a, "+/=") {
		t.Fatalf("tokens %q %q", a, b)
	}
}

func TestInitCreatesLoadableConfigWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.local.json")
	created, err := Init(path)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	cfg, err := Load(path)
	if err != nil || len(cfg.Token) < MinTokenLength || cfg.Port != DefaultPort {
		t.Fatalf("loaded %+v %v", cfg, err)
	}
	var doc map[string]string
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &doc); err != nil || doc["url"] != DefaultURL || doc["token"] != cfg.Token {
		t.Fatalf("document %s", raw)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", info.Mode().Perm())
		}
	}
	created, err = Init(path)
	if err != nil || created {
		t.Fatalf("second Init: created=%v err=%v", created, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(raw) {
		t.Fatal("existing config was modified")
	}
}

func TestInitReportsUnwritableLocation(t *testing.T) {
	blocker := write(t, "x") // a regular file where a directory is needed
	if _, err := Init(filepath.Join(blocker, "config.json")); err == nil {
		t.Fatal("expected an error")
	}
}
