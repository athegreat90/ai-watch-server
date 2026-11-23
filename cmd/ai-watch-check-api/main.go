// Command ai-watch-check-api verifies a running AI Watch server with read-only requests.
//
// Usage:
//
//	ai-watch-check-api --url http://192.168.1.50:8765 --token-file ./config.local.json
//
// The token comes from --token-file (the server config JSON, or a file holding only the token)
// or, when no file is given, from the AI_WATCH_TOKEN environment variable. It is never accepted
// on the command line. When --url is omitted, the "url" of the token file is used, then
// http://127.0.0.1:8765.
//
// Exit codes: 0 all checks passed, 1 a check failed, 2 usage error, 3 server unreachable.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/athegreat90/ai-watch-server/internal/checkapi"
)

// version is the release version; the release build overrides it with -ldflags "-X main.version=...".
var version = "dev"

// defaultURL is used when neither --url nor the token file provides one.
const defaultURL = "http://127.0.0.1:8765"

// main runs the checker and exits with its status.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// credentials holds what could be read from the token file or the environment.
type credentials struct {
	token, url string
}

// readCredentials loads the token (and optional URL) from path, or from AI_WATCH_TOKEN when
// path is empty. It never includes the token in an error.
func readCredentials(path string, getenv func(string) string) (credentials, error) {
	if path == "" {
		token := strings.TrimSpace(getenv("AI_WATCH_TOKEN"))
		if token == "" {
			return credentials{}, errors.New("no token: use --token-file or set AI_WATCH_TOKEN")
		}
		return credentials{token: token}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, fmt.Errorf("cannot read token file %s", path)
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "{") {
		var doc struct {
			Token string `json:"token"`
			URL   string `json:"url"`
		}
		if json.Unmarshal(raw, &doc) != nil || doc.Token == "" {
			return credentials{}, fmt.Errorf("token file %s has no \"token\" value", path)
		}
		return credentials{token: doc.Token, url: doc.URL}, nil
	}
	if text == "" {
		return credentials{}, fmt.Errorf("token file %s is empty", path)
	}
	return credentials{token: text}, nil
}

// run parses args, performs the checks and writes the report. It returns the exit status.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ai-watch-check-api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: ai-watch-check-api [--url URL] [--token-file FILE] [--timeout 5s] [--json] [--verbose]")
		fmt.Fprintln(stderr, "Read-only API check. Token: --token-file, else the AI_WATCH_TOKEN environment variable.")
		fmt.Fprintln(stderr, "Exit codes: 0 ok, 1 check failed, 2 usage error, 3 server unreachable.")
		fs.PrintDefaults()
	}
	url := fs.String("url", "", "server base URL (default: url from the token file, else "+defaultURL+")")
	tokenFile := fs.String("token-file", "", "server config JSON or a file containing only the token")
	timeout := fs.Duration("timeout", 5*time.Second, "timeout for each request")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	verbose := fs.Bool("verbose", false, "print details and timings for passing checks")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return checkapi.ExitOK
		}
		return checkapi.ExitUsage
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return checkapi.ExitOK
	}
	if fs.NArg() > 0 || *timeout <= 0 {
		fs.Usage()
		return checkapi.ExitUsage
	}
	creds, err := readCredentials(*tokenFile, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return checkapi.ExitUsage
	}
	target := *url
	if target == "" {
		target = creds.url
	}
	if target == "" {
		target = defaultURL
	}
	report := checkapi.Run(ctx, checkapi.Options{URL: target, Token: creds.token, Timeout: *timeout})
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		printReport(stdout, report, *verbose)
	}
	return report.ExitCode
}

// printReport writes the human-readable report.
func printReport(w io.Writer, report checkapi.Report, verbose bool) {
	fmt.Fprintf(w, "Checking %s\n", report.URL)
	failed := 0
	for _, c := range report.Checks {
		label := strings.ToUpper(c.Status)
		switch {
		case c.Status != checkapi.Pass:
			if c.Status == checkapi.Fail {
				failed++
			}
			fmt.Fprintf(w, "%-5s %s: %s\n", label, c.Name, c.Detail)
		case verbose:
			fmt.Fprintf(w, "%-5s %s: %s (%dms)\n", label, c.Name, c.Detail, c.Millis)
		default:
			fmt.Fprintf(w, "%-5s %s\n", label, c.Name)
		}
	}
	if report.OK {
		fmt.Fprintf(w, "OK: all %d checks passed\n", len(report.Checks))
		return
	}
	if report.ExitCode == checkapi.ExitUnreachable {
		fmt.Fprintln(w, "FAILED: server unreachable")
		return
	}
	fmt.Fprintf(w, "FAILED: %d of %d checks failed\n", failed, len(report.Checks))
}
