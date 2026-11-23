// Package config reads and creates the JSON configuration file shared by the server, the
// hooks and the API checker. The file keeps the format of the original bridge:
// {"url": "...", "token": "...", "port": 8765}.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// DefaultPort is the port the server listens on when the file does not set one.
	DefaultPort = 8765
	// MinTokenLength is the shortest token the server accepts.
	MinTokenLength = 24
	// DefaultURL is the URL written to a freshly generated configuration.
	DefaultURL = "http://127.0.0.1:8765"
	// tokenBytes is the amount of randomness in a generated token (256 bits).
	tokenBytes = 32
)

// Config is the validated server configuration.
type Config struct {
	// Token is the shared secret clients send in the X-Monitor-Key header.
	Token string
	// Port is the TCP port to listen on.
	Port int
}

// file mirrors the JSON document on disk.
type file struct {
	URL   string `json:"url,omitempty"`
	Token string `json:"token"`
	Port  *int   `json:"port,omitempty"`
}

// Load reads and validates the configuration at path. The error never contains the token.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is chosen by the operator on the command line, never by a remote caller
	if err != nil {
		return Config{}, fmt.Errorf("missing %s: run \"ai-watch-server init-config\"", path)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return Config{}, fmt.Errorf("invalid JSON in %s", path)
	}
	if len(f.Token) < MinTokenLength {
		return Config{}, fmt.Errorf("invalid or weak token in %s (need at least %d characters)", path, MinTokenLength)
	}
	port := DefaultPort
	if f.Port != nil {
		port = *f.Port
	}
	if port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("invalid port %d in %s", port, path)
	}
	return Config{Token: f.Token, Port: port}, nil
}

// Port returns the configured port, or [DefaultPort] when the file is missing or unreadable.
// It ignores the token so health checks work with any readable file.
func Port(path string) int {
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is chosen by the operator on the command line, never by a remote caller
	if err != nil {
		return DefaultPort
	}
	var f file
	if json.Unmarshal(raw, &f) != nil || f.Port == nil || *f.Port < 1 || *f.Port > 65535 {
		return DefaultPort
	}
	return *f.Port
}

// NewToken returns a URL-safe token with 256 bits from the operating system's secure generator.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Init creates a configuration file with a fresh random token at path.
//
// It never overwrites: when the file already exists it returns created=false and leaves it
// untouched. New files are created exclusively with mode 0600 (owner only on POSIX systems).
// The token is never printed or returned.
func Init(path string) (created bool, err error) {
	token, err := NewToken()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the path is chosen by the operator on the command line, never by a remote caller
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.NewEncoder(f).Encode(file{URL: DefaultURL, Token: token}); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return false, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
}
