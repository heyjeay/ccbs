// Package usage reads Claude Code transcripts and turns them into
// token/cost aggregates: 5-hour windows, weekly windows, sessions.
package usage

import (
	"strings"
	"time"
)

// Tokens is a breakdown of token usage for one or more API calls.
type Tokens struct {
	In     int64 // uncached input
	Out    int64 // output (incl. thinking)
	CacheW int64 // cache creation
	CacheR int64 // cache read
}

func (t Tokens) Total() int64 { return t.In + t.Out + t.CacheW + t.CacheR }

func (t *Tokens) Add(o Tokens) {
	t.In += o.In
	t.Out += o.Out
	t.CacheW += o.CacheW
	t.CacheR += o.CacheR
}

// Entry is one deduplicated assistant API response.
type Entry struct {
	Time      time.Time
	SessionID string
	Model     string
	Tokens    Tokens
	Cost      float64
}

// Session is everything ccbs knows about one Claude Code conversation.
type Session struct {
	ID         string
	Project    string // working directory
	Branch     string
	Title      string // ai-title / summary / custom title
	FirstAsk   string // first human prompt
	LastPrompt string // most recent human prompt ("what we're working on")
	LastTool   string // most recent tool call, human readable
	Start      time.Time
	End        time.Time // last activity of any kind
	Tokens     Tokens
	Cost       float64
	Calls      int
	Prompts    int
	Models     map[string]int64 // model -> total tokens

	// bookkeeping so records can be merged in any order
	firstAskAt, lastPromptAt, lastToolAt time.Time
	titleRank                            int
}

// MainModel is the model that consumed the most tokens in the session.
func (s *Session) MainModel() string {
	best, bestN := "", int64(-1)
	for m, n := range s.Models {
		if n > bestN || (n == bestN && m < best) {
			best, bestN = m, n
		}
	}
	return best
}

// DisplayTitle picks the most descriptive label available.
func (s *Session) DisplayTitle() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.FirstAsk != "":
		return s.FirstAsk
	case s.LastPrompt != "":
		return s.LastPrompt
	}
	return "(untitled)"
}

// ProjectName is the last path element of the working directory.
func (s *Session) ProjectName() string { return ProjectName(s.Project) }

func ProjectName(p string) string {
	p = strings.TrimRight(p, "/\\")
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "?"
	}
	return p
}

// ShortModel turns "claude-opus-5-5" into "opus-5.5" and
// "claude-sonnet-4-5-20250929" into "sonnet-4.5".
func ShortModel(m string) string {
	m = strings.TrimPrefix(m, "claude-")
	parts := strings.Split(m, "-")
	// drop trailing date stamp
	if n := len(parts); n > 1 && len(parts[n-1]) == 8 {
		parts = parts[:n-1]
	}
	if len(parts) == 0 {
		return m
	}
	name := parts[0]
	nums := parts[1:]
	// legacy form: claude-3-5-sonnet
	if len(parts) >= 2 && isDigits(parts[0]) {
		name = parts[len(parts)-1]
		nums = parts[:len(parts)-1]
	}
	if len(nums) == 0 {
		return name
	}
	return name + "-" + strings.Join(nums, ".")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Window is a usage limit window (5h or 7d).
type Window struct {
	Start, End  time.Time
	Utilization float64 // 0-100, -1 when unknown
	Estimated   bool    // utilization derived locally rather than from the API
	NoReset     bool    // live utilization, but Anthropic gave no reset time
	Tokens      Tokens
	Cost        float64
	Calls       int
}

func (w Window) Remaining(now time.Time) time.Duration {
	if now.After(w.End) {
		return 0
	}
	return w.End.Sub(now)
}

func (w Window) Elapsed(now time.Time) time.Duration {
	if now.Before(w.Start) {
		return 0
	}
	if now.After(w.End) {
		return w.End.Sub(w.Start)
	}
	return now.Sub(w.Start)
}
