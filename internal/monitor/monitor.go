// Package monitor holds the in-memory session and usage-limit state of the AI Watch server.
//
// It is a faithful port of the original Python bridge: the same validation, per-turn timing,
// eviction and time-to-live rules. All state lives in a [Store], which is safe for concurrent
// use and reads time from an injectable [Clock] so behavior can be tested deterministically.
package monitor

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limits shared with the original server and documented in docs/API.md.
const (
	// MaxSessions is the number of sessions kept before the least recently updated one is evicted.
	MaxSessions = 40
	// StatusSessions is the number of sessions returned by the compact status snapshot.
	StatusSessions = 8
	// DoneTTL is how long, in seconds, a finished session (done, idle, notice, error) stays visible.
	DoneTTL = 120
	// ActiveTTL is how long, in seconds, a working or waiting session stays visible without updates.
	ActiveTTL = 3600
	// LimitsTTL is how long, in seconds, a usage-limit reading stays visible.
	LimitsTTL = 900
	// LongTaskSeconds is the minimum duration, in seconds, that marks a finished task as long.
	LongTaskSeconds = 300
	// futureSkew is how far, in seconds, an event timestamp may be ahead of the server clock.
	futureSkew = 60
)

// ErrInvalid is returned when an event or limits payload fails validation.
var ErrInvalid = errors.New("invalid event")

var (
	// validStatus lists the accepted session statuses.
	validStatus = map[string]bool{"working": true, "needs_attention": true, "done": true, "idle": true, "notice": true, "error": true}
	// priority orders statuses in snapshots; lower values come first.
	priority = map[string]int{"needs_attention": 0, "error": 1, "working": 2, "notice": 3, "done": 4, "idle": 5}
	// newlines replaces line breaks so clients never see multi-line fields.
	newlines = strings.NewReplacer("\n", " ", "\r", " ")
)

// Clock supplies the current time. It exists so tests can control time.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// SystemClock is the [Clock] backed by the operating system.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// Session is one tracked agent session as stored and reported by the server.
type Session struct {
	SessionID string  `json:"session_id"`
	Agent     string  `json:"agent"`
	Project   string  `json:"project"`
	Status    string  `json:"status"`
	Detail    string  `json:"detail"`
	Updated   float64 `json:"updated"`
	Started   float64 `json:"started"`
	Since     float64 `json:"since"`
}

// SessionView is a [Session] with the values computed at snapshot time.
type SessionView struct {
	Session
	Duration     int  `json:"duration"`
	FinishedLong bool `json:"finished_long"`
	Waiting      int  `json:"waiting"`
}

// Window is one usage-limit window as reported by an agent.
type Window struct {
	UsedPct  float64 `json:"used_pct"`
	ResetsAt float64 `json:"resets_at"`
}

// WindowView is a [Window] plus the seconds remaining until it resets.
type WindowView struct {
	Window
	ResetsIn int `json:"resets_in"`
}

// Snapshot is the response body of the status and sessions endpoints.
type Snapshot struct {
	Sessions       []SessionView                    `json:"sessions"`
	Limits         map[string]map[string]WindowView `json:"limits"`
	ActiveCount    int                              `json:"active_count"`
	AttentionCount int                              `json:"attention_count"`
	TotalCount     int                              `json:"total_count"`
}

// sessionKey identifies a session: ids are only unique per agent.
type sessionKey struct{ agent, id string }

// entry is a stored session plus its first-insertion sequence number, which breaks ties
// deterministically (the Python dict kept first-insertion order).
type entry struct {
	Session
	seq uint64
}

// limitRecord is the latest usage reading of one agent.
type limitRecord struct {
	updated          float64
	fiveHour, weekly Window
}

// Store is the thread-safe session and limits state. Create it with [NewStore].
type Store struct {
	clock  Clock
	mu     sync.Mutex
	next   uint64
	state  map[sessionKey]*entry
	limits map[string]*limitRecord
}

// NewStore returns an empty Store that reads time from clock, or from the system clock when nil.
func NewStore(clock Clock) *Store {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Store{clock: clock, state: map[sessionKey]*entry{}, limits: map[string]*limitRecord{}}
}

// now returns the clock time as Unix seconds with sub-second precision.
func (s *Store) now() float64 { return float64(s.clock.Now().UnixNano()) / 1e9 }

// clean replaces line breaks with spaces and cuts the text to maxLen characters (not bytes).
func clean(value string, maxLen int) string {
	value = newlines.Replace(value)
	if r := []rune(value); len(r) > maxLen {
		return string(r[:maxLen])
	}
	return value
}

// stringField returns data[name] when it is a JSON string.
func stringField(data map[string]any, name string) (string, bool) {
	v, ok := data[name].(string)
	return v, ok
}

// optionalString returns data[name], or "" when absent. It reports false when the field is
// present but is not a string.
func optionalString(data map[string]any, name string) (string, bool) {
	v, present := data[name]
	if !present {
		return "", true
	}
	str, ok := v.(string)
	return str, ok
}

// PostEvent validates an event payload and applies it to the session state.
//
// Events older than or equal to the stored update are ignored without error, which makes
// duplicates and out-of-order deliveries harmless. It returns [ErrInvalid] for bad payloads.
func (s *Store) PostEvent(event map[string]any) error {
	sessionID, ok1 := stringField(event, "session_id")
	agent, ok2 := stringField(event, "agent")
	statusRaw, ok3 := stringField(event, "status")
	project, ok4 := optionalString(event, "project")
	detail, ok5 := optionalString(event, "detail")
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return ErrInvalid
	}
	sid := clean(sessionID, 80)
	if strings.TrimSpace(sid) == "" || strings.TrimSpace(agent) == "" {
		return ErrInvalid
	}
	status := clean(statusRaw, 20)
	if !validStatus[status] {
		return ErrInvalid
	}
	now := s.now()
	timestamp := now
	if raw, present := event["timestamp"]; present {
		f, ok := raw.(float64)
		if !ok || f < now-ActiveTTL || f > now+futureSkew {
			return ErrInvalid
		}
		timestamp = f
	}
	if project == "" {
		project = "Unknown project"
	}
	record := Session{SessionID: sid, Agent: clean(agent, 24), Project: clean(project, 40), Status: status,
		Detail: clean(detail, 66), Updated: timestamp}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey{record.Agent, sid}
	previous := s.state[key]
	if previous != nil && previous.Updated >= timestamp {
		return nil
	}
	// "started" is per turn: a new turn begins when work resumes from a non-working state.
	newTurn := previous == nil || (status == "working" && previous.Status != "working" && previous.Status != "needs_attention")
	if newTurn {
		record.Started = timestamp
	} else {
		record.Started = previous.Started
	}
	if previous != nil && previous.Status == status {
		record.Since = previous.Since
	} else {
		record.Since = timestamp
	}
	seq := s.next
	if previous != nil {
		seq = previous.seq
	} else {
		s.next++
	}
	s.state[key] = &entry{Session: record, seq: seq}
	if len(s.state) > MaxSessions {
		s.evictOldest()
	}
	return nil
}

// evictOldest removes the least recently updated session, preferring the first inserted on ties.
// The caller must hold s.mu.
func (s *Store) evictOldest() {
	var oldestKey sessionKey
	var oldest *entry
	for k, e := range s.state {
		if oldest == nil || e.Updated < oldest.Updated || (e.Updated == oldest.Updated && e.seq < oldest.seq) {
			oldestKey, oldest = k, e
		}
	}
	delete(s.state, oldestKey)
}

// PostLimits validates a usage-limits payload and stores it for the reporting agent.
// It returns [ErrInvalid] for unknown agents, missing windows or out-of-range numbers.
func (s *Store) PostLimits(data map[string]any) error {
	agent, _ := data["agent"].(string)
	if agent != "Claude Code" && agent != "Codex" && agent != "Antigravity" {
		return ErrInvalid
	}
	record := &limitRecord{updated: s.now()}
	for _, name := range []string{"five_hour", "weekly"} {
		item, ok := data[name].(map[string]any)
		if !ok {
			return ErrInvalid
		}
		used, ok := item["used_pct"].(float64)
		if !ok || used < 0 || used > 100 {
			return ErrInvalid
		}
		resets, ok := item["resets_at"].(float64)
		if !ok || resets < 0 {
			return ErrInvalid
		}
		// FormatFloat rounds the exact binary value like Python's round(x, 1).
		rounded, _ := strconv.ParseFloat(strconv.FormatFloat(used, 'f', 1, 64), 64)
		window := Window{UsedPct: rounded, ResetsAt: resets}
		if name == "five_hour" {
			record.fiveHour = window
		} else {
			record.weekly = window
		}
	}
	s.mu.Lock()
	s.limits[agent] = record
	s.mu.Unlock()
	return nil
}

// limitsView returns the fresh limit readings and drops stale ones. The caller must hold s.mu.
func (s *Store) limitsView(now float64) map[string]map[string]WindowView {
	out := map[string]map[string]WindowView{}
	for agent, r := range s.limits {
		if now-r.updated > LimitsTTL {
			delete(s.limits, agent)
			continue
		}
		out[agent] = map[string]WindowView{
			"five_hour": {r.fiveHour, max(0, int(r.fiveHour.ResetsAt-now))},
			"weekly":    {r.weekly, max(0, int(r.weekly.ResetsAt-now))},
		}
	}
	return out
}

// Snapshot expires old sessions and returns up to limit sessions ordered by status priority
// and recency, with counters computed over every live session.
func (s *Store) Snapshot(limit int) Snapshot {
	now := s.now()
	s.mu.Lock()
	for key, e := range s.state {
		ttl := float64(ActiveTTL)
		switch e.Status {
		case "done", "idle", "notice", "error":
			ttl = DoneTTL
		}
		if now-e.Updated > ttl {
			delete(s.state, key)
		}
	}
	all := make([]entry, 0, len(s.state))
	for _, e := range s.state {
		all = append(all, *e)
	}
	limits := s.limitsView(now)
	s.mu.Unlock()

	snap := Snapshot{Limits: limits, TotalCount: len(all)}
	for _, e := range all {
		switch e.Status {
		case "working":
			snap.ActiveCount++
		case "needs_attention":
			snap.AttentionCount++
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if priority[a.Status] != priority[b.Status] {
			return priority[a.Status] < priority[b.Status]
		}
		if a.Updated != b.Updated {
			return a.Updated > b.Updated
		}
		return a.seq < b.seq
	})
	if len(all) > limit {
		all = all[:limit]
	}
	snap.Sessions = make([]SessionView, 0, len(all))
	for _, e := range all {
		end := e.Updated
		if e.Status == "working" || e.Status == "needs_attention" {
			end = now
		}
		duration := max(0, int(end-e.Started))
		waiting := 0
		if e.Status == "needs_attention" {
			waiting = max(0, int(now-e.Since))
		}
		snap.Sessions = append(snap.Sessions, SessionView{Session: e.Session, Duration: duration,
			FinishedLong: e.Status == "done" && duration >= LongTaskSeconds, Waiting: waiting})
	}
	return snap
}
