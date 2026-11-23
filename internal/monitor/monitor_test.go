package monitor

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced Clock for deterministic tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// Now returns the fake time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance moves the fake time forward by d.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// unix returns the fake time as Unix seconds.
func (c *fakeClock) unix() float64 { return float64(c.Now().UnixNano()) / 1e9 }

// newTestStore returns a Store on a fake clock fixed at a round timestamp.
func newTestStore() (*Store, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return NewStore(c), c
}

// event builds a valid event payload and applies overrides.
func event(overrides map[string]any) map[string]any {
	e := map[string]any{"session_id": "s1", "agent": "Codex", "status": "working"}
	for k, v := range overrides {
		e[k] = v
	}
	return e
}

func TestPostEventValidation(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		valid bool
	}{
		{"minimal", event(nil), true},
		{"missing session", map[string]any{"agent": "a", "status": "done"}, false},
		{"missing agent", map[string]any{"session_id": "s", "status": "done"}, false},
		{"missing status", map[string]any{"session_id": "s", "agent": "a"}, false},
		{"numeric session", event(map[string]any{"session_id": 5.0}), false},
		{"blank session", event(map[string]any{"session_id": "  \n"}), false},
		{"blank agent", event(map[string]any{"agent": " "}), false},
		{"bad status", event(map[string]any{"status": "sleeping"}), false},
		{"project not string", event(map[string]any{"project": 1.0}), false},
		{"detail not string", event(map[string]any{"detail": []any{}}), false},
		{"null project", event(map[string]any{"project": nil}), false},
		{"bool timestamp", event(map[string]any{"timestamp": true}), false},
		{"string timestamp", event(map[string]any{"timestamp": "1700000000"}), false},
		{"timestamp too old", event(map[string]any{"timestamp": 1_700_000_000.0 - ActiveTTL - 1}), false},
		{"timestamp too new", event(map[string]any{"timestamp": 1_700_000_000.0 + futureSkew + 1}), false},
		{"timestamp edge old", event(map[string]any{"timestamp": 1_700_000_000.0 - ActiveTTL}), true},
		{"timestamp edge new", event(map[string]any{"timestamp": 1_700_000_000.0 + futureSkew}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore()
			err := s.PostEvent(tc.input)
			if tc.valid && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.valid {
				if err != ErrInvalid {
					t.Fatalf("want ErrInvalid, got %v", err)
				}
				if got := s.Snapshot(MaxSessions).TotalCount; got != 0 {
					t.Fatalf("rejected event left %d sessions", got)
				}
			}
		})
	}
}

func TestCleanTruncatesByCharacters(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		want     string
	}{
		{"newlines", "a\nb\rc", 10, "a b c"},
		{"ascii cut", "abcdef", 3, "abc"},
		{"unicode cut", "ñandú日本語", 4, "ñand"},
		{"emoji cut", "😀😀😀", 2, "😀😀"},
		{"short", "ok", 10, "ok"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clean(tc.in, tc.max); got != tc.want {
				t.Fatalf("clean(%q,%d)=%q want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestPostEventFieldDefaultsAndLimits(t *testing.T) {
	s, _ := newTestStore()
	if err := s.PostEvent(event(map[string]any{
		"session_id": strings.Repeat("x", 100), "agent": strings.Repeat("a", 40),
		"project": strings.Repeat("p", 60), "detail": strings.Repeat("d", 100)})); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot(MaxSessions).Sessions[0]
	if len(got.SessionID) != 80 || len(got.Agent) != 24 || len(got.Project) != 40 || len(got.Detail) != 66 {
		t.Fatalf("lengths %d/%d/%d/%d", len(got.SessionID), len(got.Agent), len(got.Project), len(got.Detail))
	}
	s2, _ := newTestStore()
	if err := s2.PostEvent(event(map[string]any{"project": ""})); err != nil {
		t.Fatal(err)
	}
	if p := s2.Snapshot(1).Sessions[0].Project; p != "Unknown project" {
		t.Fatalf("project %q", p)
	}
}

func TestStateTransitions(t *testing.T) {
	s, c := newTestStore()
	base := c.unix()
	post := func(status string, offset float64) {
		t.Helper()
		if err := s.PostEvent(event(map[string]any{"status": status, "timestamp": base + offset})); err != nil {
			t.Fatal(err)
		}
	}
	c.advance(480 * time.Second)
	base = c.unix() - 480
	post("working", 0)
	post("needs_attention", 10) // still the same turn
	post("working", 20)         // resumes from needs_attention: same turn
	post("done", 400)
	got := s.Snapshot(1).Sessions[0]
	if got.Started != base || got.Since != base+400 || got.Duration != 400 || !got.FinishedLong {
		t.Fatalf("unexpected %+v", got)
	}
	post("working", 450) // resumes from done: new turn
	got = s.Snapshot(1).Sessions[0]
	if got.Started != base+450 || got.FinishedLong {
		t.Fatalf("new turn not started: %+v", got)
	}
	post("working", 460) // same status keeps since
	if got = s.Snapshot(1).Sessions[0]; got.Since != base+450 {
		t.Fatalf("since moved: %+v", got)
	}
}

func TestDuplicateAndOutOfOrderEvents(t *testing.T) {
	s, c := newTestStore()
	ts := c.unix() - 100
	for _, step := range []struct {
		status string
		at     float64
		want   string
	}{
		{"working", ts, "working"},
		{"done", ts, "working"},      // equal timestamp ignored
		{"done", ts - 50, "working"}, // older ignored
		{"done", ts + 1, "done"},
	} {
		if err := s.PostEvent(event(map[string]any{"status": step.status, "timestamp": step.at})); err != nil {
			t.Fatal(err)
		}
		if got := s.Snapshot(1).Sessions[0].Status; got != step.want {
			t.Fatalf("after %s@%v got %s want %s", step.status, step.at, got, step.want)
		}
	}
}

func TestSessionsAreKeyedPerAgent(t *testing.T) {
	s, _ := newTestStore()
	_ = s.PostEvent(event(map[string]any{"agent": "Codex"}))
	_ = s.PostEvent(event(map[string]any{"agent": "Claude Code"}))
	if got := s.Snapshot(MaxSessions).TotalCount; got != 2 {
		t.Fatalf("total %d", got)
	}
}

func TestOrderingAndCounters(t *testing.T) {
	s, c := newTestStore()
	now := c.unix()
	for i, st := range []string{"done", "working", "needs_attention", "error", "idle", "notice", "working"} {
		err := s.PostEvent(event(map[string]any{"session_id": fmt.Sprintf("s%d", i), "status": st, "timestamp": now - float64(i)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	snap := s.Snapshot(StatusSessions)
	var order []string
	for _, v := range snap.Sessions {
		order = append(order, v.SessionID)
	}
	if got := strings.Join(order, ","); got != "s2,s3,s1,s6,s5,s0,s4" {
		t.Fatalf("order %s", got)
	}
	if snap.ActiveCount != 2 || snap.AttentionCount != 1 || snap.TotalCount != 7 {
		t.Fatalf("counters %+v", snap)
	}
	if limited := s.Snapshot(2); len(limited.Sessions) != 2 || limited.TotalCount != 7 {
		t.Fatalf("limit not applied only to list: %+v", limited)
	}
}

func TestEvictionRemovesLeastRecentlyUpdatedFirstInserted(t *testing.T) {
	s, c := newTestStore()
	now := c.unix()
	for i := 0; i < MaxSessions; i++ {
		// All share one timestamp so the first inserted session is the tie-break winner.
		if err := s.PostEvent(event(map[string]any{"session_id": fmt.Sprintf("s%02d", i), "timestamp": now - 10})); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PostEvent(event(map[string]any{"session_id": "new", "timestamp": now})); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot(MaxSessions + 5)
	if snap.TotalCount != MaxSessions {
		t.Fatalf("total %d", snap.TotalCount)
	}
	for _, v := range snap.Sessions {
		if v.SessionID == "s00" {
			t.Fatal("first inserted tie was not evicted")
		}
	}
}

func TestTTLExpiry(t *testing.T) {
	tests := []struct {
		name   string
		status string
		age    time.Duration
		alive  bool
	}{
		{"done alive", "done", DoneTTL * time.Second, true},
		{"done expired", "done", (DoneTTL + 1) * time.Second, false},
		{"error expired", "error", (DoneTTL + 1) * time.Second, false},
		{"working alive", "working", (DoneTTL + 1) * time.Second, true},
		{"working edge", "working", ActiveTTL * time.Second, true},
		{"working expired", "working", (ActiveTTL + 1) * time.Second, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore()
			if err := s.PostEvent(event(map[string]any{"status": tc.status})); err != nil {
				t.Fatal(err)
			}
			c.advance(tc.age)
			if got := s.Snapshot(1).TotalCount == 1; got != tc.alive {
				t.Fatalf("alive=%v want %v", got, tc.alive)
			}
		})
	}
}

func TestDurationAndWaiting(t *testing.T) {
	s, c := newTestStore()
	start := c.unix()
	_ = s.PostEvent(event(map[string]any{"status": "needs_attention", "timestamp": start}))
	c.advance(90 * time.Second)
	got := s.Snapshot(1).Sessions[0]
	if got.Duration != 90 || got.Waiting != 90 {
		t.Fatalf("working/attention duration must follow the clock: %+v", got)
	}
	s2, c2 := newTestStore()
	_ = s2.PostEvent(event(map[string]any{"status": "working", "timestamp": c2.unix()}))
	_ = s2.PostEvent(event(map[string]any{"status": "done", "timestamp": c2.unix() + 30}))
	c2.advance(60 * time.Second)
	if got := s2.Snapshot(1).Sessions[0]; got.Duration != 30 || got.Waiting != 0 {
		t.Fatalf("done duration must freeze: %+v", got)
	}
}

// limitsPayload builds a valid limits payload and applies overrides.
func limitsPayload(overrides map[string]any) map[string]any {
	p := map[string]any{"agent": "Codex",
		"five_hour": map[string]any{"used_pct": 31.25, "resets_at": 1_700_000_600.0},
		"weekly":    map[string]any{"used_pct": 5.0, "resets_at": 1_700_100_000.0}}
	for k, v := range overrides {
		p[k] = v
	}
	return p
}

func TestPostLimits(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		valid bool
	}{
		{"valid", limitsPayload(nil), true},
		{"antigravity", limitsPayload(map[string]any{"agent": "Antigravity"}), true},
		{"claude", limitsPayload(map[string]any{"agent": "Claude Code"}), true},
		{"unknown agent", limitsPayload(map[string]any{"agent": "Other"}), false},
		{"agent not string", limitsPayload(map[string]any{"agent": 1.0}), false},
		{"missing window", limitsPayload(map[string]any{"weekly": nil}), false},
		{"pct high", limitsPayload(map[string]any{"five_hour": map[string]any{"used_pct": 100.1, "resets_at": 1.0}}), false},
		{"pct negative", limitsPayload(map[string]any{"five_hour": map[string]any{"used_pct": -1.0, "resets_at": 1.0}}), false},
		{"pct string", limitsPayload(map[string]any{"five_hour": map[string]any{"used_pct": "5", "resets_at": 1.0}}), false},
		{"resets negative", limitsPayload(map[string]any{"five_hour": map[string]any{"used_pct": 1.0, "resets_at": -1.0}}), false},
		{"resets missing", limitsPayload(map[string]any{"five_hour": map[string]any{"used_pct": 1.0}}), false},
		{"pct bounds", limitsPayload(map[string]any{"five_hour": map[string]any{"used_pct": 100.0, "resets_at": 0.0}}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore()
			err := s.PostLimits(tc.input)
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v valid=%v", err, tc.valid)
			}
			if !tc.valid && len(s.Snapshot(1).Limits) != 0 {
				t.Fatal("rejected limits were stored")
			}
		})
	}
}

func TestLimitsRoundingResetsInAndTTL(t *testing.T) {
	s, c := newTestStore()
	if err := s.PostLimits(limitsPayload(nil)); err != nil {
		t.Fatal(err)
	}
	w := s.Snapshot(1).Limits["Codex"]
	if w["five_hour"].UsedPct != 31.2 {
		t.Fatalf("31.25 must round to 31.2 like Python, got %v", w["five_hour"].UsedPct)
	}
	if w["five_hour"].ResetsIn != 600 || w["weekly"].ResetsIn != 100000 {
		t.Fatalf("resets_in %+v", w)
	}
	c.advance(700 * time.Second)
	if got := s.Snapshot(1).Limits["Codex"]["five_hour"].ResetsIn; got != 0 {
		t.Fatalf("resets_in must clamp at zero, got %d", got)
	}
	c.advance((LimitsTTL - 700 + 1) * time.Second)
	if len(s.Snapshot(1).Limits) != 0 {
		t.Fatal("stale limits must expire")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s, c := newTestStore()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = s.PostEvent(event(map[string]any{"session_id": fmt.Sprintf("s%d", i%50), "timestamp": c.unix() - float64(200-i)}))
				_ = s.PostLimits(limitsPayload(nil))
				_ = s.Snapshot(StatusSessions)
			}
		}()
	}
	wg.Wait()
	if got := s.Snapshot(MaxSessions).TotalCount; got > MaxSessions {
		t.Fatalf("cap exceeded: %d", got)
	}
}

func TestNewStoreDefaultsToSystemClock(t *testing.T) {
	s := NewStore(nil)
	if err := s.PostEvent(event(nil)); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot(1).TotalCount != 1 {
		t.Fatal("system clock store lost the event")
	}
}
