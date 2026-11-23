// Package checkapi verifies a running AI Watch server from the outside using only read-only
// requests: connectivity, the public health endpoint, authentication behavior and the shape of
// the authenticated JSON responses. It backs the standalone ai-watch-check-api executable.
package checkapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Check outcomes.
const (
	// Pass means the check succeeded.
	Pass = "pass"
	// Fail means the server answered but not as expected.
	Fail = "fail"
	// Skip means the check was not run because the server was unreachable.
	Skip = "skip"
)

// Exit codes of the executable.
const (
	// ExitOK means every check passed.
	ExitOK = 0
	// ExitFailed means the server answered but at least one check failed.
	ExitFailed = 1
	// ExitUsage means the command line or credentials were invalid.
	ExitUsage = 2
	// ExitUnreachable means the server could not be reached before the timeout.
	ExitUnreachable = 3
)

// maxResponse caps how much of a response is read, so a wrong endpoint cannot exhaust memory.
const maxResponse = 1 << 20

// invalidToken is sent to prove that wrong credentials are rejected. It can never equal a
// real token because real tokens are at least 24 characters of generated data.
const invalidToken = "ai-watch-check-api-invalid"

// Options configures [Run].
type Options struct {
	// URL is the server base URL, for example http://192.168.1.50:8765.
	URL string
	// Token is the shared secret. It is sent only in the X-Monitor-Key header.
	Token string
	// Timeout bounds each request. Zero means 5 seconds.
	Timeout time.Duration
	// Client overrides the HTTP client, mainly for tests. Nil uses a proxy-free client.
	Client *http.Client
}

// CheckResult is the outcome of one check.
type CheckResult struct {
	// Name identifies the check, for example "health".
	Name string `json:"name"`
	// Status is [Pass], [Fail] or [Skip].
	Status string `json:"status"`
	// Detail explains the outcome in one line. It never contains the token.
	Detail string `json:"detail"`
	// Millis is the time the check took.
	Millis int64 `json:"millis"`
}

// Report is the result of a complete run.
type Report struct {
	// OK is true only when every check passed.
	OK bool `json:"ok"`
	// URL is the server that was checked.
	URL string `json:"url"`
	// ExitCode is the process exit status the report maps to.
	ExitCode int `json:"exit_code"`
	// Checks lists every check in execution order.
	Checks []CheckResult `json:"checks"`
}

// response is what one request returned.
type response struct {
	status int
	body   []byte
}

// runner carries the state shared by the checks of one run.
type runner struct {
	opts   Options
	client *http.Client
	base   string
}

// Run executes every check against the server and returns the report. It never panics and
// never sends anything but GET requests. When the health request cannot connect, the remaining
// checks are skipped and the report maps to [ExitUnreachable].
func Run(ctx context.Context, opts Options) Report {
	opts.Timeout = orDefault(opts.Timeout, 5*time.Second)
	client := opts.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: nil}}
	}
	base := strings.TrimRight(opts.URL, "/")
	// Redirects would resend credentials to another place; treat them as failures instead.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r := &runner{opts: opts, client: &noRedirect, base: base}
	report := Report{URL: base, OK: true}

	type step struct {
		name string
		run  func(context.Context) (string, bool, error)
	}
	steps := []step{
		{"health", r.health},
		{"anonymous-rejected", r.anonymousRejected},
		{"invalid-token-rejected", r.invalidRejected},
		{"status", func(c context.Context) (string, bool, error) { return r.snapshot(c, "/api/status") }},
		{"sessions", func(c context.Context) (string, bool, error) { return r.snapshot(c, "/api/sessions") }},
	}
	unreachable := false
	for _, s := range steps {
		if unreachable {
			report.Checks = append(report.Checks, CheckResult{Name: s.name, Status: Skip, Detail: "server unreachable"})
			continue
		}
		start := time.Now()
		detail, network, err := s.run(ctx)
		result := CheckResult{Name: s.name, Status: Pass, Detail: detail, Millis: time.Since(start).Milliseconds()}
		if err != nil {
			result.Status, result.Detail, report.OK = Fail, err.Error(), false
			unreachable = network
		}
		report.Checks = append(report.Checks, result)
	}
	switch {
	case unreachable:
		report.ExitCode = ExitUnreachable
	case !report.OK:
		report.ExitCode = ExitFailed
	}
	return report
}

// orDefault returns v, or def when v is not positive.
func orDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

// get sends one GET request and reads a bounded response. The boolean reports a network-level
// failure (no HTTP response at all), as opposed to an unexpected HTTP answer.
func (r *runner) get(ctx context.Context, path, token string) (response, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, nil)
	if err != nil {
		return response{}, false, fmt.Errorf("invalid URL: %w", unwrapURL(err))
	}
	if token != "" {
		req.Header.Set("X-Monitor-Key", token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return response{}, true, fmt.Errorf("request failed: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return response{}, true, fmt.Errorf("reading response: %w", err)
	}
	return response{status: resp.StatusCode, body: body}, false, nil
}

// expectStatus returns an error unless got equals want.
func expectStatus(got, want int) error {
	if got != want {
		return fmt.Errorf("expected HTTP %d, got %d", want, got)
	}
	return nil
}

// health checks the public endpoint: HTTP 200 and {"ok": true}.
func (r *runner) health(ctx context.Context) (string, bool, error) {
	resp, network, err := r.get(ctx, "/health", "")
	if err != nil {
		return "", network, err
	}
	if err := expectStatus(resp.status, 200); err != nil {
		return "", false, err
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(resp.body, &body) != nil || !body.OK {
		return "", false, errors.New(`body is not {"ok":true}`)
	}
	return "HTTP 200", false, nil
}

// anonymousRejected checks that a request without credentials gets HTTP 401.
func (r *runner) anonymousRejected(ctx context.Context) (string, bool, error) {
	return r.rejected(ctx, "")
}

// invalidRejected checks that a request with a wrong token gets HTTP 401.
func (r *runner) invalidRejected(ctx context.Context) (string, bool, error) {
	return r.rejected(ctx, invalidToken)
}

// rejected checks that /api/status answers HTTP 401 for the given (missing or wrong) token.
func (r *runner) rejected(ctx context.Context, token string) (string, bool, error) {
	resp, network, err := r.get(ctx, "/api/status", token)
	if err != nil {
		return "", network, err
	}
	if err := expectStatus(resp.status, 401); err != nil {
		return "", false, err
	}
	return "HTTP 401", false, nil
}

// snapshot checks an authenticated snapshot endpoint: HTTP 200 and a valid body shape.
func (r *runner) snapshot(ctx context.Context, path string) (string, bool, error) {
	resp, network, err := r.get(ctx, path, r.opts.Token)
	if err != nil {
		return "", network, err
	}
	if err := expectStatus(resp.status, 200); err != nil {
		return "", false, err
	}
	count, err := validateSnapshot(resp.body)
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("HTTP 200, %d sessions", count), false, nil
}

// validateSnapshot checks the required fields and types of a snapshot body and returns the
// number of sessions it lists.
func validateSnapshot(body []byte) (int, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, errors.New("body is not a JSON object")
	}
	for _, name := range []string{"active_count", "attention_count", "total_count"} {
		var n int
		if raw, ok := doc[name]; !ok || json.Unmarshal(raw, &n) != nil || n < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
	}
	var limits map[string]json.RawMessage
	if raw, ok := doc["limits"]; !ok || json.Unmarshal(raw, &limits) != nil || limits == nil {
		return 0, errors.New(`field "limits" must be an object`)
	}
	var sessions []struct {
		SessionID    *string  `json:"session_id"`
		Agent        *string  `json:"agent"`
		Project      *string  `json:"project"`
		Status       *string  `json:"status"`
		Detail       *string  `json:"detail"`
		Updated      *float64 `json:"updated"`
		Started      *float64 `json:"started"`
		Since        *float64 `json:"since"`
		Duration     *int     `json:"duration"`
		FinishedLong *bool    `json:"finished_long"`
		Waiting      *int     `json:"waiting"`
	}
	if raw, ok := doc["sessions"]; !ok || json.Unmarshal(raw, &sessions) != nil || sessions == nil {
		return 0, errors.New(`field "sessions" must be a list of session objects`)
	}
	for i, s := range sessions {
		if s.SessionID == nil || s.Agent == nil || s.Project == nil || s.Status == nil || s.Detail == nil ||
			s.Updated == nil || s.Started == nil || s.Since == nil || s.Duration == nil ||
			s.FinishedLong == nil || s.Waiting == nil {
			return 0, fmt.Errorf("session %d is missing a required field or has a wrong type", i)
		}
	}
	return len(sessions), nil
}

// unwrapURL strips the request URL from a *url.Error so messages stay short and never echo
// anything the caller typed into the URL.
func unwrapURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
