package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

// Store incrementally tails every transcript under its roots. Files are
// parsed in parallel; results are merged in a fixed order so the outcome
// doesn't depend on scheduling.
type Store struct {
	refresh  sync.Mutex // one Refresh at a time
	mu       sync.RWMutex
	roots    []string
	files    map[string]*fileState
	seen     map[string]struct{}
	entries  []Entry
	sessions map[string]*Session
	sorted   bool
	version  uint64

	snap    Snapshot
	snapVer uint64

	done, total atomic.Int64 // bytes, for the loading screen
}

func NewStore(roots []string) *Store {
	return &Store{
		roots:    roots,
		files:    map[string]*fileState{},
		seen:     map[string]struct{}{},
		sessions: map[string]*Session{},
		snapVer:  ^uint64(0),
	}
}

// Progress reports how many bytes of the current refresh have been read.
func (s *Store) Progress() (done, total int64) { return s.done.Load(), s.total.Load() }

type job struct {
	i    int
	path string
	st   *fileState
	size int64
}

// Refresh reads any bytes appended since the last call.
func (s *Store) Refresh() error {
	s.refresh.Lock()
	defer s.refresh.Unlock()

	var paths []string
	for _, root := range s.roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
				paths = append(paths, p)
			}
			return nil
		})
	}
	sort.Strings(paths)

	// Only files that grew (or were rewritten) need work.
	var jobs []job
	var total int64
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		st := s.files[p]
		if st == nil {
			st = &fileState{}
			s.files[p] = st
		}
		if info.Size() < st.offset {
			*st = fileState{} // truncated / rewritten
		}
		if info.Size() == st.offset {
			continue
		}
		jobs = append(jobs, job{i: len(jobs), path: p, st: st, size: info.Size()})
		total += info.Size() - st.offset
	}
	if len(jobs) == 0 {
		return nil
	}
	s.done.Store(0)
	s.total.Store(total)

	results := make([][]rec, len(jobs))
	ch := make(chan job)
	var wg sync.WaitGroup
	workers := min(runtime.NumCPU(), len(jobs))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				results[j.i] = parseFile(j.path, j.st, j.size, &s.done)
			}
		}()
	}
	// biggest first keeps all workers busy until the end
	order := append([]job(nil), jobs...)
	sort.Slice(order, func(a, b int) bool { return order[a].size-order[a].st.offset > order[b].size-order[b].st.offset })
	for _, j := range order {
		ch <- j
	}
	close(ch)
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rs := range results {
		for i := range rs {
			s.apply(&rs[i])
		}
	}
	if !s.sorted {
		sort.Slice(s.entries, func(i, j int) bool { return s.entries[i].Time.Before(s.entries[j].Time) })
		s.sorted = true
	}
	s.version++
	return nil
}

type recKind uint8

const (
	recTitle recKind = iota + 1
	recUser
	recAssistant
)

// rec is everything one transcript line contributes, extracted in parallel
// and applied to the store afterwards.
type rec struct {
	kind      recKind
	sid       string
	ts        time.Time
	cwd       string
	branch    string
	sidechain bool
	title     string
	titleRank int // 1 summary, 2 ai-title, 3 custom title
	prompt    string
	tool      string
	key       string
	model     string
	tokens    Tokens
	cost      float64
	usage     bool
}

var (
	kAssistant  = []byte(`"type":"assistant"`)
	kUser       = []byte(`"type":"user"`)
	kToolResult = []byte(`"tool_result"`)
	kToolUse    = []byte(`"tool_use"`)
	kSpaced     = []byte(`"type": "`)
	kTitles     = [][]byte{[]byte(`"type":"ai-title"`), []byte(`"type":"summary"`), []byte(`"type":"custom-title"`)}
)

type lineClass uint8

const (
	skipLine   lineClass = iota
	decodeLine           // worth a full JSON decode
	toolResult           // only its timestamp matters
)

// classify is a cheap byte scan that decides how much work a line deserves
// before paying for JSON decoding. The bulk of a transcript is tool output
// (file contents, command output) in user tool_result lines; ccbs only needs
// their timestamp. Inside JSON strings quotes are escaped, so these patterns
// can only match real JSON structure.
func classify(line []byte) lineClass {
	if bytes.Contains(line, kAssistant) {
		return decodeLine
	}
	if bytes.Contains(line, kUser) {
		if bytes.Contains(line, kToolResult) {
			return toolResult
		}
		return decodeLine
	}
	for _, k := range kTitles {
		if bytes.Contains(line, k) {
			return decodeLine
		}
	}
	// not compact JSON: decode and let the type field decide
	if bytes.Contains(line, kSpaced) {
		return decodeLine
	}
	return skipLine
}

var kTimestamp = []byte(`"timestamp":"`)

// lineTime finds a line's top-level timestamp without decoding it. Escaped
// quotes inside the tool output can't match, so the first hit is the real one.
func lineTime(line []byte) (time.Time, bool) {
	i := bytes.Index(line, kTimestamp)
	if i < 0 {
		return time.Time{}, false
	}
	v := line[i+len(kTimestamp):]
	j := bytes.IndexByte(v, '"')
	if j < 0 || j > 40 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, string(v[:j]))
	return t, err == nil
}

func parseFile(path string, st *fileState, size int64, done *atomic.Int64) []rec {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// one exact-size read; growing a buffer would copy big files many times
	data := make([]byte, size-st.offset)
	n, err := f.ReadAt(data, st.offset)
	if err != nil && err != io.EOF {
		return nil
	}
	data = data[:n]
	// Only consume complete lines; a partial trailing line is re-read next time.
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil
	}
	data = data[:end+1]
	st.offset += int64(len(data))

	var out []rec
	var pendingDone int64
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		line := data[:i]
		data = data[i+1:]
		if pendingDone += int64(i + 1); pendingDone > 4<<20 {
			done.Add(pendingDone)
			pendingDone = 0
		}
		if len(line) < 2 {
			continue
		}
		switch classify(line) {
		case decodeLine:
			if r, ok := parseLine(line, st); ok {
				out = append(out, r)
			}
		case toolResult:
			// still counts as activity (keeps durations and "now" accurate)
			if ts, ok := lineTime(line); ok && st.sessionID != "" {
				out = append(out, rec{kind: recUser, sid: st.sessionID, ts: ts})
			}
		}
	}
	done.Add(pendingDone)
	return out
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

func parseLine(line []byte, st *fileState) (rec, bool) {
	var r rawLine
	if json.Unmarshal(line, &r) != nil {
		return rec{}, false
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
			return rec{}, false
		}
		rank := map[string]int{"summary": 1, "ai-title": 2, "custom-title": 3}[r.Type]
		if sid == "" {
			st.pending = title
			return rec{}, false
		}
		return rec{kind: recTitle, sid: sid, title: oneLine(title), titleRank: rank}, true
	case "user", "assistant":
	default:
		return rec{}, false
	}
	if sid == "" {
		return rec{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		return rec{}, false
	}
	out := rec{sid: sid, ts: ts, cwd: r.Cwd, branch: r.GitBranch, sidechain: r.IsSidechain}
	if st.pending != "" {
		// a summary that appeared before any line named the session
		out.title, out.titleRank = oneLine(st.pending), 1
		st.pending = ""
	}
	if r.Type == "user" {
		out.kind = recUser
		if r.Message != nil && !r.IsMeta && !r.IsSidechain {
			out.prompt = humanPrompt(r.Message.Content)
		}
		return out, true
	}
	out.kind = recAssistant
	if r.Message == nil {
		return out, true
	}
	if !r.IsSidechain && bytes.Contains(line, kToolUse) {
		out.tool = lastTool(r.Message.Content)
	}
	u := r.Message.Usage
	if u == nil || r.Message.Model == "<synthetic>" {
		return out, true
	}
	t := Tokens{In: u.InputTokens, Out: u.OutputTokens, CacheW: u.CacheCreate, CacheR: u.CacheRead}
	if t.Total() == 0 {
		return out, true
	}
	var w1h int64
	if u.CacheCreation != nil {
		w1h = u.CacheCreation.Eph1h
	}
	if r.Message.ID != "" || r.RequestID != "" {
		out.key = r.Message.ID + ":" + r.RequestID
	}
	out.usage, out.model, out.tokens = true, r.Message.Model, t
	out.cost = Cost(r.Message.Model, t, w1h)
	return out, true
}

func (s *Store) session(id string) *Session {
	ss := s.sessions[id]
	if ss == nil {
		ss = &Session{ID: id, Models: map[string]int64{}}
		s.sessions[id] = ss
	}
	return ss
}

// apply merges one record. It only compares timestamps, never relies on
// arrival order, so parallel parsing gives the same result every time.
func (s *Store) apply(r *rec) {
	ss := s.session(r.sid)
	if r.title != "" && r.titleRank >= ss.titleRank {
		ss.Title, ss.titleRank = r.title, r.titleRank
	}
	if r.kind == recTitle {
		return
	}
	if ss.Start.IsZero() || r.ts.Before(ss.Start) {
		ss.Start = r.ts
	}
	if r.ts.After(ss.End) {
		ss.End = r.ts
	}
	if r.cwd != "" && (!r.sidechain || ss.Project == "") {
		ss.Project = r.cwd
	}
	if r.branch != "" && (r.branch != "HEAD" || ss.Branch == "") {
		ss.Branch = r.branch
	}
	if r.prompt != "" {
		if ss.FirstAsk == "" || r.ts.Before(ss.firstAskAt) {
			ss.FirstAsk, ss.firstAskAt = r.prompt, r.ts
		}
		if !r.ts.Before(ss.lastPromptAt) {
			ss.LastPrompt, ss.lastPromptAt = r.prompt, r.ts
		}
		ss.Prompts++
	}
	if r.tool != "" && !r.ts.Before(ss.lastToolAt) {
		ss.LastTool, ss.lastToolAt = r.tool, r.ts
	}
	if !r.usage {
		return
	}
	if r.key != "" {
		if _, dup := s.seen[r.key]; dup {
			return
		}
		s.seen[r.key] = struct{}{}
	}
	e := Entry{Time: r.ts, SessionID: r.sid, Model: r.model, Tokens: r.tokens, Cost: r.cost}
	if n := len(s.entries); n > 0 && r.ts.Before(s.entries[n-1].Time) {
		s.sorted = false
	}
	s.entries = append(s.entries, e)
	ss.Tokens.Add(r.tokens)
	ss.Cost += e.Cost
	ss.Calls++
	ss.Models[e.Model] += r.tokens.Total()
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

// Snapshot is an immutable view handed to the UI. Callers must not modify it.
type Snapshot struct {
	Entries  []Entry
	Sessions []*Session // most recent activity first
}

// Snapshot returns the current data, rebuilt only when something changed.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapVer == s.version {
		return s.snap
	}
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
	s.snap, s.snapVer = snap, s.version
	return snap
}

// NewSnapshot builds a snapshot from pre-made data (used by demo mode).
func NewSnapshot(entries []Entry, sessions []*Session) Snapshot {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Time.Before(entries[j].Time) })
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].End.After(sessions[j].End) })
	return Snapshot{Entries: entries, Sessions: sessions}
}
