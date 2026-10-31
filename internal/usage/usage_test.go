package usage

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const transcript = `{"type":"user","sessionId":"s1","timestamp":"2026-10-31T10:00:00Z","cwd":"/home/me/proj","gitBranch":"main","message":{"role":"user","content":"add a cache layer"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-10-31T10:00:05Z","cwd":"/home/me/proj","requestId":"r1","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/home/me/proj/internal/cache/lru.go"}}],"usage":{"input_tokens":10,"output_tokens":100,"cache_creation_input_tokens":1000,"cache_read_input_tokens":5000}}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-10-31T10:00:05Z","cwd":"/home/me/proj","requestId":"r1","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"output_tokens":100,"cache_creation_input_tokens":1000,"cache_read_input_tokens":5000}}}
{"type":"user","sessionId":"s1","timestamp":"2026-10-31T10:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]}}
{"type":"user","sessionId":"s1","timestamp":"2026-10-31T10:00:07Z","isMeta":true,"message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"ai-title","aiTitle":"Add LRU cache","sessionId":"s1"}
{"type":"user","sessionId":"s1","timestamp":"2026-10-31T10:05:00Z","message":{"role":"user","content":"now add   eviction\nmetrics"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-10-31T10:05:10Z","requestId":"r2","message":{"id":"m2","model":"<synthetic>","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}
`

func writeTranscript(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "-home-me-proj", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStoreParsesAndDedupes(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, transcript)
	s := NewStore([]string{dir})
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("want 1 deduped entry, got %d", len(snap.Entries))
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(snap.Sessions))
	}
	ss := snap.Sessions[0]
	checks := map[string][2]string{
		"title":      {ss.Title, "Add LRU cache"},
		"firstAsk":   {ss.FirstAsk, "add a cache layer"},
		"lastPrompt": {ss.LastPrompt, "now add eviction metrics"},
		"lastTool":   {ss.LastTool, "Edit internal/cache/lru.go"},
		"project":    {ss.ProjectName(), "proj"},
		"branch":     {ss.Branch, "main"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
	if ss.Prompts != 2 {
		t.Errorf("prompts = %d, want 2 (tool results and meta excluded)", ss.Prompts)
	}
	if got := ss.Tokens.Total(); got != 6110 {
		t.Errorf("tokens = %d, want 6110", got)
	}
}

func TestStoreIsIncremental(t *testing.T) {
	dir := t.TempDir()
	p := writeTranscript(t, dir, transcript)
	s := NewStore([]string{dir})
	s.Refresh()
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	// a partial line must not be consumed until it is complete
	f.WriteString(`{"type":"assistant","sessionId":"s1","timestamp":"2026-10-31T10:06:00Z","requestId":"r3","message":{"id":"m3","model":"claude-sonnet-5-5","content":[],`)
	s.Refresh()
	if n := len(s.Snapshot().Entries); n != 1 {
		t.Fatalf("partial line consumed: %d entries", n)
	}
	f.WriteString(`"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n")
	f.Close()
	s.Refresh()
	if n := len(s.Snapshot().Entries); n != 2 {
		t.Fatalf("want 2 entries after append, got %d", n)
	}
}

func TestShortModel(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5":            "opus-5.5",
		"claude-sonnet-4-5-20250929": "sonnet-4.5",
		"claude-haiku-5-5":           "haiku-5.5",
		"claude-3-5-sonnet-20241022": "sonnet-3.5",
		"claude-fable-5-1":           "fable-5.1",
	} {
		if got := ShortModel(in); got != want {
			t.Errorf("ShortModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCost(t *testing.T) {
	// 1M output tokens on Opus 5.5 = $20
	if got := Cost("claude-opus-5-5", Tokens{Out: 1_000_000}, 0); math.Abs(got-20) > 1e-9 {
		t.Errorf("opus out cost = %v", got)
	}
	// 1h cache writes are 2x input
	if got := Cost("claude-sonnet-5-5", Tokens{CacheW: 1_000_000}, 1_000_000); math.Abs(got-4) > 1e-9 {
		t.Errorf("sonnet 1h write cost = %v", got)
	}
	if got := Cost("unknown-model", Tokens{Out: 1}, 0); got != 0 {
		t.Errorf("unknown model should cost 0, got %v", got)
	}
}

func TestBlocks(t *testing.T) {
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	e := func(s string) Entry { return Entry{Time: at(s), Tokens: Tokens{Out: 1}} }
	bs := Blocks([]Entry{
		e("2026-10-31T09:20:00Z"), e("2026-10-31T13:59:00Z"), // same block (09:00-14:00)
		e("2026-10-31T14:01:00Z"), // past end → new block at 14:00
		e("2026-10-31T23:30:00Z"), // after a long gap
	})
	if len(bs) != 3 {
		t.Fatalf("want 3 blocks, got %d", len(bs))
	}
	if !bs[0].Start.Equal(at("2026-10-31T09:00:00Z")) || bs[0].Calls != 2 {
		t.Errorf("block 0 = %+v", bs[0])
	}
	if !bs[1].Start.Equal(at("2026-10-31T14:00:00Z")) {
		t.Errorf("block 1 start = %v", bs[1].Start)
	}
}

func TestProjectedHit(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	w := Window{Start: now.Add(-2 * time.Hour), End: now.Add(3 * time.Hour), Utilization: 50, Cost: 10}
	// 50% per $10 → remaining 50% = $10; at $0.10/min that's 100 min
	at, ok := ProjectedHit(w, Burn{CostPerMin: 0.10}, now)
	if !ok || at.Sub(now).Round(time.Minute) != 100*time.Minute {
		t.Errorf("projected = %v ok=%v", at.Sub(now), ok)
	}
	if _, ok := ProjectedHit(w, Burn{CostPerMin: 0.01}, now); ok {
		t.Error("slow burn should not hit before reset")
	}
}
