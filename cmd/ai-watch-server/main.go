// Command ai-watch-server runs the AI Watch event bridge and its maintenance subcommands.
//
// Usage:
//
//	ai-watch-server [serve] [--config FILE]   run the server (default)
//	ai-watch-server init-config [--config FILE]  create a config with a random token
//	ai-watch-server healthcheck [--config FILE]  exit 0 when the local server is healthy
//	ai-watch-server version
//
// Environment: AI_WATCH_CONFIG (config path), MONITOR_BIND (listen address, default
// 127.0.0.1), MONITOR_READ_TIMEOUT and MONITOR_WRITE_TIMEOUT (Go durations, default 3s).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/athegreat90/ai-watch-server/internal/config"
	"github.com/athegreat90/ai-watch-server/internal/httpapi"
	"github.com/athegreat90/ai-watch-server/internal/monitor"
)

// version is the release version; the release build overrides it with -ldflags "-X main.version=...".
var version = "dev"

// defaultConfig is the config path used when neither --config nor AI_WATCH_CONFIG is given.
// The container runs in /app, so this resolves to the documented /app/config.local.json mount.
const defaultConfig = "config.local.json"

// healthTimeout is how long the health check waits for the local server.
const healthTimeout = 2 * time.Second

// main runs the command until it finishes or SIGINT/SIGTERM arrives, then exits with its status.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches the subcommand named by args[0] (serve when absent) and returns the process
// exit status. It is separate from main so tests can drive it without exiting.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	switch command {
	case "serve":
		return serve(ctx, args, getenv, stderr)
	case "init-config":
		return initConfig(args, getenv, stdout, stderr)
	case "healthcheck":
		return healthcheck(ctx, args, getenv, stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q (use serve, init-config, healthcheck or version)\n", command)
		return 2
	}
}

// configFlag registers --config on a new FlagSet, defaulting to AI_WATCH_CONFIG or the built-in path.
func configFlag(name string, getenv func(string) string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	def := defaultConfig
	if v := getenv("AI_WATCH_CONFIG"); v != "" {
		def = v
	}
	return fs, fs.String("config", def, "path of the JSON config file")
}

// durationEnv reads a Go duration from the environment, returning 0 (use the default) when unset.
func durationEnv(getenv func(string) string, name string) (time.Duration, error) {
	raw := getenv(name)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 3s", name)
	}
	return d, nil
}

// serve runs the HTTP server until ctx is cancelled and returns the exit status.
func serve(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	fs, path := configFlag("serve", getenv, stderr)
	if fs.Parse(args) != nil {
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	cfg, err := config.Load(*path)
	if err != nil {
		logger.Error("configuration error", "error", err.Error())
		return 1
	}
	readTimeout, err1 := durationEnv(getenv, "MONITOR_READ_TIMEOUT")
	writeTimeout, err2 := durationEnv(getenv, "MONITOR_WRITE_TIMEOUT")
	if err := firstError(err1, err2); err != nil {
		logger.Error("configuration error", "error", err.Error())
		return 1
	}
	bind := getenv("MONITOR_BIND")
	if bind == "" {
		bind = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(cfg.Port)))
	if err != nil {
		logger.Error("cannot listen", "error", err.Error())
		return 1
	}
	err = httpapi.Serve(ctx, ln, httpapi.Options{
		Handler:      httpapi.NewHandler(monitor.NewStore(nil), cfg.Token),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("server failed", "error", err.Error())
		return 1
	}
	return 0
}

// firstError returns the first non-nil error, or nil.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// initConfig creates the config file with a random token unless it already exists.
// It prints only the path and outcome, never the token.
func initConfig(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs, path := configFlag("init-config", getenv, stderr)
	if fs.Parse(args) != nil {
		return 2
	}
	created, err := config.Init(*path)
	if err != nil {
		fmt.Fprintf(stderr, "cannot create %s: %v\n", *path, err)
		return 1
	}
	if created {
		fmt.Fprintf(stdout, "created %s\n", *path)
	} else {
		fmt.Fprintf(stdout, "kept existing %s\n", *path)
	}
	return 0
}

// healthcheck asks the local server for /health and returns 0 only on an HTTP 200.
// It is the container HEALTHCHECK: the image has no shell, curl or Python.
func healthcheck(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	fs, path := configFlag("healthcheck", getenv, stderr)
	if fs.Parse(args) != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port(*path))) + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// No proxy: the check always targets the local server.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "unhealthy:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "unhealthy: status", resp.StatusCode)
		return 1
	}
	return 0
}
