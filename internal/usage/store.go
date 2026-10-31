package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultDirs returns the Claude Code transcript roots that exist on this
// machine. CLAUDE_CONFIG_DIR (comma separated) takes precedence.
func DefaultDirs() []string {
	var cands []string
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		for _, d := range strings.Split(env, ",") {
			if d = strings.TrimSpace(d); d != "" {
				cands = append(cands, filepath.Join(d, "projects"))
			}
		}
	} else {
		home, _ := os.UserHomeDir()
		cands = append(cands,
			filepath.Join(home, ".claude", "projects"),
			filepath.Join(home, ".config", "claude", "projects"),
		)
	}
	var out []string
	for _, d := range cands {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

type fileState struct {
	offset    int64
	sessionID string
	pending   string // summary seen before we knew the session id
}

// Store incrementally tails every transcript under its roots.
type Store struct {
	mu       sync.RWMutex
	roots    []string
	files    map[string]*fileState
	seen     map[string]struct{}
	entries  []Entry
	sessions map[string]*Session
	sorted   bool
}

func NewStore(roots []string) *Store {
	return &Store{
		roots:    roots,
		files:    map[string]*fileState{},
		seen:     map[string]struct{}{},
		sessions: map[string]*Session{},
	}
}

// Refresh reads any bytes appended since the last call.
func (s *Store) Refresh() error {
	var paths []string
	for _, root := range s.roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
				paths = append(paths, p)
			}
			return nil
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range paths {
		st := s.files[p]
		if st == nil {
			st = &fileState{}
			s.files[p] = st
		}
		s.readFile(p, st)
	}
	if !s.sorted {
		sort.Slice(s.entries, func(i, j int) bool { return s.entries[i].Time.Before(s.entries[j].Time) })
		s.sorted = true
	}
	return nil
}

func (s *Store) readFile(path string, st *fileState) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return
	}
	if info.Size() < st.offset { // truncated / rewritten
		st.offset = 0
	}
	if info.Size() == st.offset {
		return
	}
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return
	}
	// Only consume complete lines; a partial trailing line is re-read next time.
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return
	}
	data = data[:end+1]
	st.offset += int64(len(data))
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		line := data[:i]
		data = data[i+1:]
		if len(bytes.TrimSpace(line)) > 0 {
			s.ingest(line, st)
		}
	}
}

type rawUsage struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	CacheCreate   int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation *struct {
		Eph1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type rawLine struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Timestamp   string `json:"timestamp"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	RequestID   string `json:"requestId"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Summary     string `json:"summary"`
	AITitle     string `json:"aiTitle"`
	CustomTitle string `json:"customTitle"`
	Message     *struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *rawUsage       `json:"usage"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (s *Store) session(id string) *Session {
	ss := s.sessions[id]
	if ss == nil {
		ss = &Session{ID: id, Models: map[string]int64{}}
		s.sessions[id] = ss
	}
	return ss
}

func (s *Store) ingest(line []byte, st *fileState) {
	var r rawLine
	if json.Unmarshal(line, &r) != nil {
		return
	}
	if r.SessionID != "" && st.sessionID == "" {
		st.sessionID = r.SessionID
	}
	sid := r.SessionID
	if sid == "" {
		sid = st.sessionID
	}

	switch r.Type {
	case "summary", "ai-title", "custom-title":
		title := firstNonEmpty(r.CustomTitle, r.AITitle, r.Summary)
		if title == "" {
			return
		}
		if sid == "" {
			st.pending = title
			return
		}
		ss := s.session(sid)
		// a custom title set by the user always wins
		if r.Type == "custom-title" || ss.Title == "" || r.Type == "ai-title" {
			ss.Title = oneLine(title)
		}
		return
	case "user", "assistant":
	default:
		return
	}
	if sid == "" {
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		return
	}
	ss := s.session(sid)
	if st.pending != "" && ss.Title == "" {
		ss.Title = oneLine(st.pending)
		st.pending = ""
	}
	if ss.Start.IsZero() || ts.Before(ss.Start) {
		ss.Start = ts
	}
	if ts.After(ss.End) {
		ss.End = ts
	}
	if r.Cwd != "" && !r.IsSidechain {
		ss.Project = r.Cwd
	} else if ss.Project == "" {
		ss.Project = r.Cwd
	}
	if r.GitBranch != "" && r.GitBranch != "HEAD" {
		ss.Branch = r.GitBranch
	} else if ss.Branch == "" {
		ss.Branch = r.GitBranch
	}
	if r.Message == nil {
		return
	}

	if r.Type == "user" {
		if r.IsMeta || r.IsSidechain {
			return
		}
		if p := humanPrompt(r.Message.Content); p != "" {
			if ss.FirstAsk == "" {
				ss.FirstAsk = p
			}
			ss.LastPrompt = p
			ss.Prompts++
		}
		return
	}

	// assistant
	if tool := lastTool(r.Message.Content); tool != "" && !r.IsSidechain {
		ss.LastTool = tool
	}
	u := r.Message.Usage
	if u == nil || r.Message.Model == "<synthetic>" {
		return
	}
	key := r.Message.ID + ":" + r.RequestID
	if r.Message.ID != "" || r.RequestID != "" {
		if _, dup := s.seen[key]; dup {
			return
		}
		s.seen[key] = struct{}{}
	}
	t := Tokens{In: u.InputTokens, Out: u.OutputTokens, CacheW: u.CacheCreate, CacheR: u.CacheRead}
	if t.Total() == 0 {
		return
	}
	var w1h int64
	if u.CacheCreation != nil {
		w1h = u.CacheCreation.Eph1h
	}
	e := Entry{Time: ts, SessionID: sid, Model: r.Message.Model, Tokens: t, Cost: Cost(r.Message.Model, t, w1h)}
	if n := len(s.entries); n > 0 && ts.Before(s.entries[n-1].Time) {
		s.sorted = false
	}
	s.entries = append(s.entries, e)
	ss.Tokens.Add(t)
	ss.Cost += e.Cost
	ss.Calls++
	ss.Models[e.Model] += t.Total()
}

func humanPrompt(raw json.RawMessage) string {
	var text string
	if len(raw) > 0 && raw[0] == '"' {
		_ = json.Unmarshal(raw, &text)
	} else {
		var blocks []contentBlock
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return ""
			}
			if b.Type == "text" && text == "" {
				text = b.Text
			}
		}
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "Caveat:") ||
		strings.HasPrefix(text, "[Request interrupted") {
		return ""
	}
	return oneLine(text)
}

func lastTool(raw json.RawMessage) string {
	var blocks []contentBlock
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	out := ""
	for _, b := range blocks {
		if b.Type == "tool_use" {
			out = describeTool(b.Name, b.Input)
		}
	}
	return out
}

func describeTool(name string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string {
		v, _ := in[k].(string)
		return oneLine(v)
	}
	arg := ""
	switch name {
	case "Bash":
		arg = firstNonEmpty(str("description"), str("command"))
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		arg = shortPath(firstNonEmpty(str("file_path"), str("notebook_path")))
	case "Grep", "Glob":
		arg = str("pattern")
	case "Task", "Agent":
		arg = str("description")
	case "WebFetch":
		arg = str("url")
	case "WebSearch":
		arg = str("query")
	case "TodoWrite", "TaskCreate", "TaskUpdate":
		arg = "updating tasks"
	default:
		arg = firstNonEmpty(str("description"), str("file_path"), str("path"), str("query"))
	}
	if arg == "" {
		return name
	}
	return name + " " + arg
}

func shortPath(p string) string {
	parts := strings.Split(filepath.ToSlash(p), "/")
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	return strings.Join(parts, "/")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Snapshot is an immutable copy handed to the UI.
type Snapshot struct {
	Entries  []Entry
	Sessions []*Session // most recent activity first
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{Entries: append([]Entry(nil), s.entries...)}
	for _, ss := range s.sessions {
		if ss.Calls == 0 && ss.Prompts == 0 {
			continue
		}
		c := *ss
		c.Models = make(map[string]int64, len(ss.Models))
		for k, v := range ss.Models {
			c.Models[k] = v
		}
		snap.Sessions = append(snap.Sessions, &c)
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].End.After(snap.Sessions[j].End) })
	return snap
}

// NewSnapshot builds a snapshot from pre-made data (used by demo mode).
func NewSnapshot(entries []Entry, sessions []*Session) Snapshot {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Time.Before(entries[j].Time) })
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].End.After(sessions[j].End) })
	return Snapshot{Entries: entries, Sessions: sessions}
}
