package usage

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

type demoTask struct {
	project, branch, title, model string
	prompts, tools                []string
}

var demoTasks = []demoTask{
	{"ccbs", "main", "btop-style usage dashboard", "claude-opus-5-5",
		[]string{"build a btop-like TUI for claude code usage", "add braille graph for the 5h window", "make the history table sortable"},
		[]string{"Edit internal/ui/render.go", "Bash go build ./...", "Read internal/usage/store.go", "Write internal/ui/graph.go"}},
	{"api-gateway", "feat/rate-limit", "Token-bucket rate limiter for gateway", "claude-opus-5-5",
		[]string{"add per-tenant rate limiting with redis", "write load tests for the limiter", "fix flaky TestBurst"},
		[]string{"Edit pkg/limiter/bucket.go", "Bash go test ./pkg/limiter/...", "Grep TenantID", "Read cmd/gateway/main.go"}},
	{"web-dashboard", "fix/hydration", "Fix React hydration mismatch on /billing", "claude-sonnet-5-5",
		[]string{"why does /billing flash on load?", "move date formatting to the client", "add a playwright test"},
		[]string{"Edit src/pages/billing.tsx", "Bash pnpm test", "Read src/lib/format.ts", "Bash pnpm exec playwright test"}},
	{"infra", "main", "Terraform module for preview envs", "claude-sonnet-5-5",
		[]string{"create a terraform module for per-PR preview environments", "wire it into the github action"},
		[]string{"Write modules/preview/main.tf", "Bash terraform validate", "Edit .github/workflows/preview.yml"}},
	{"ml-pipeline", "exp/dedupe", "Near-duplicate filter for training shards", "claude-opus-5-5",
		[]string{"implement minhash dedupe over the parquet shards", "benchmark against the exact-match baseline", "plot the threshold sweep"},
		[]string{"Edit dedupe/minhash.py", "Bash pytest -q tests/test_minhash.py", "Bash python bench.py --shards 8", "Agent profile memory usage"}},
	{"docs-site", "main", "Migrate docs from Docusaurus to Astro", "claude-haiku-5-5",
		[]string{"migrate the docs site to astro starlight", "fix broken internal links"},
		[]string{"Bash npx astro build", "Grep ](/docs/", "Edit astro.config.mjs"}},
	{"mobile-app", "feat/offline", "Offline queue for mobile sync", "claude-opus-5-5",
		[]string{"add an offline mutation queue to the sync layer", "handle conflict resolution", "write the migration"},
		[]string{"Edit lib/sync/queue.dart", "Bash flutter test", "Read lib/sync/client.dart"}},
}

// Demo builds a believable week of activity ending at now, plus matching
// live limits, so ccbs can be explored without any local history.
func Demo(now time.Time) (Snapshot, *Limits) {
	rng := rand.New(rand.NewSource(42))
	var entries []Entry
	var sessions []*Session

	add := func(t demoTask, start time.Time, dur time.Duration, intensity float64, ongoing bool) {
		id := fmt.Sprintf("demo-%04x", rng.Intn(1<<16))
		s := &Session{
			ID: id, Project: "/home/dev/" + t.project, Branch: t.branch, Title: t.title,
			Start: start, Models: map[string]int64{},
		}
		s.FirstAsk = t.prompts[0]
		ctx := 18_000.0 + rng.Float64()*12_000
		ts := start
		end := start.Add(dur)
		prompt := 0
		for ts.Before(end) {
			// a few prompts spread over the session
			if p := int(float64(len(t.prompts)) * float64(ts.Sub(start)) / float64(dur)); p >= prompt {
				prompt = p + 1
				s.Prompts++
				s.LastPrompt = t.prompts[min(p, len(t.prompts)-1)]
			}
			ctx = math.Min(ctx+rng.Float64()*4_000, 190_000)
			if rng.Float64() < 0.03 { // compaction
				ctx = 40_000
			}
			model := t.model
			if rng.Float64() < 0.12 {
				model = "claude-haiku-5-5" // subagents / background calls
			}
			tok := Tokens{
				In:     int64(2 + rng.Intn(40)),
				Out:    int64(200 + rng.ExpFloat64()*900*intensity),
				CacheW: int64(400 + rng.ExpFloat64()*3_000),
				CacheR: int64(ctx * (0.85 + rng.Float64()*0.15)),
			}
			e := Entry{Time: ts, SessionID: id, Model: model, Tokens: tok, Cost: Cost(model, tok, tok.CacheW)}
			entries = append(entries, e)
			s.Tokens.Add(tok)
			s.Cost += e.Cost
			s.Calls++
			s.Models[model] += tok.Total()
			s.LastTool = t.tools[rng.Intn(len(t.tools))]
			s.End = ts
			gap := time.Duration((8 + rng.ExpFloat64()*35/intensity) * float64(time.Second))
			if rng.Float64() < 0.04 && !ongoing { // coffee break
				gap += time.Duration(rng.Intn(20)) * time.Minute
			}
			ts = ts.Add(gap)
		}
		sessions = append(sessions, s)
	}

	day := func(d int, h, m int) time.Time {
		y, mo, dd := now.AddDate(0, 0, -d).Date()
		return time.Date(y, mo, dd, h, m, 0, 0, now.Location())
	}
	// past week
	plan := []struct {
		d, h, m int
		task    int
		dur     time.Duration
		k       float64
	}{
		{6, 9, 12, 3, 95 * time.Minute, 0.8}, {6, 14, 3, 5, 50 * time.Minute, 0.6},
		{5, 10, 40, 1, 160 * time.Minute, 1.2}, {5, 16, 5, 2, 70 * time.Minute, 0.9},
		{4, 8, 55, 4, 210 * time.Minute, 1.4}, {4, 20, 30, 6, 60 * time.Minute, 0.7},
		{3, 11, 20, 1, 120 * time.Minute, 1.0},
		{2, 9, 30, 6, 140 * time.Minute, 1.1}, {2, 13, 45, 2, 90 * time.Minute, 0.9}, {2, 19, 10, 3, 45 * time.Minute, 0.5},
		{1, 10, 0, 4, 180 * time.Minute, 1.3}, {1, 15, 25, 5, 40 * time.Minute, 0.6}, {1, 21, 0, 0, 75 * time.Minute, 1.0},
	}
	for _, p := range plan {
		add(demoTasks[p.task], day(p.d, p.h, p.m), p.dur, p.k, false)
	}
	// today: two finished, two still running
	add(demoTasks[2], now.Add(-3*time.Hour-10*time.Minute), 48*time.Minute, 0.9, false)
	add(demoTasks[3], now.Add(-2*time.Hour-5*time.Minute), 35*time.Minute, 0.7, false)
	add(demoTasks[1], now.Add(-95*time.Minute), 95*time.Minute-40*time.Second, 1.3, true)
	add(demoTasks[0], now.Add(-52*time.Minute), 52*time.Minute-6*time.Second, 1.1, true)

	reset5 := now.Add(time.Hour + 47*time.Minute).Truncate(time.Minute)
	resetW := now.Add(3*24*time.Hour + 14*time.Hour).Truncate(time.Hour)
	lim := &Limits{
		FiveHour:       &LimitWindow{Utilization: 63, ResetsAt: reset5},
		SevenDay:       &LimitWindow{Utilization: 41, ResetsAt: resetW},
		SevenDaySonnet: &LimitWindow{Utilization: 12, ResetsAt: resetW},
		Fetched:        now,
	}
	return NewSnapshot(entries, sessions), lim
}
