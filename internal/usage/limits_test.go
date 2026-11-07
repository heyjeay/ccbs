package usage

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLimitsTolerant(t *testing.T) {
	body := `{"five_hour":{"utilization":41.5,"resets_at":"2026-11-05T21:00:00.512+00:00"},
	          "seven_day":{"utilization":23,"resets_at":null},
	          "seven_day_opus":null,
	          "seven_day_oauth_apps":{"utilization":0,"resets_at":null},
	          "extra_usage":{"is_enabled":false}}`
	l, all, err := parseLimits([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if l.FiveHour == nil || l.FiveHour.Utilization != 41.5 || l.FiveHour.ResetsAt.IsZero() {
		t.Errorf("five_hour = %+v", l.FiveHour)
	}
	if l.SevenDay == nil || l.SevenDay.Utilization != 23 || !l.SevenDay.ResetsAt.IsZero() {
		t.Errorf("seven_day with null resets_at = %+v", l.SevenDay)
	}
	if l.SevenDayOpus != nil {
		t.Error("null window should stay nil")
	}
	if _, ok := all["seven_day_oauth_apps"]; !ok {
		t.Error("unknown windows should still be reported (for --doctor)")
	}
}

func TestWeeklyUsesLiveWithoutResetTime(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	live := &Limits{SevenDay: &LimitWindow{Utilization: 37}}
	w := WeeklyWindow(Snapshot{}, now, live)
	if w.Utilization != 37 || w.Estimated || !w.NoReset {
		t.Errorf("weekly = %+v", w)
	}
	five, _ := FiveHourWindow(Snapshot{}, now, &Limits{FiveHour: &LimitWindow{Utilization: 12}})
	if five.Utilization != 12 || five.Estimated {
		t.Errorf("5h = %+v", five)
	}
}

func fakeUsage(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("anthropic-beta") == "" {
			http.Error(w, "bad auth", 401)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := usageURL
	usageURL = srv.URL
	t.Cleanup(func() { usageURL = old })
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-token")
}

func TestFetchLimitsErrorsCarryRetry(t *testing.T) {
	for status, want := range map[int]string{401: "login expired", 429: "rate limited", 503: "Anthropic error"} {
		fakeUsage(t, status, `{}`)
		_, err := FetchLimits(context.Background())
		var le *LimitError
		if !errors.As(err, &le) || !strings.Contains(le.Msg, want) || le.Retry < time.Minute {
			t.Errorf("%d: got %v", status, err)
		}
	}
	fakeUsage(t, 200, `{"five_hour":{"utilization":5,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":9,"resets_at":"2030-01-02T00:00:00Z"}}`)
	l, err := FetchLimits(context.Background())
	if err != nil || l.SevenDay.Utilization != 9 {
		t.Fatalf("ok fetch: %v %+v", err, l)
	}
}

func TestExpiredLookingTokenIsStillTried(t *testing.T) {
	// the old code refused to call the API when expiresAt was in the past
	// (or written in seconds); now Anthropic decides.
	dir := t.TempDir()
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"test-token","expiresAt":1700000000}}`), 0o600)
	c, err := readCredentials()
	if err != nil || c.token != "test-token" || c.expires.Year() != 2023 {
		t.Fatalf("creds = %+v, %v", c, err)
	}
}

func TestDiagnoseNeverPrintsToken(t *testing.T) {
	fakeUsage(t, 200, `{"five_hour":{"utilization":5,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":9,"resets_at":null}}`)
	var buf bytes.Buffer
	Diagnose(context.Background(), &buf, false, "")
	out := buf.String()
	if strings.Contains(out, "test-token") {
		t.Fatal("doctor output leaked the token")
	}
	for _, want := range []string{"200 OK", "seven_day", "weekly limit: 9% used", "no reset time given"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
	fakeUsage(t, 401, `{"error":"invalid token"}`)
	buf.Reset()
	Diagnose(context.Background(), &buf, false, "")
	if !strings.Contains(buf.String(), "401") || !strings.Contains(buf.String(), "login expired") || !strings.Contains(buf.String(), "ccbs setup") {
		t.Errorf("401 not explained:\n%s", buf.String())
	}
}

func TestPollLimitsSharesCacheAndBacksOff(t *testing.T) {
	t.Setenv("CCBS_CACHE_DIR", t.TempDir())
	calls := 0
	status := 429
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if status == 429 {
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"five_hour":{"utilization":5,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":9,"resets_at":"2030-01-02T00:00:00Z"}}`))
	}))
	defer srv.Close()
	old := usageURL
	usageURL = srv.URL
	defer func() { usageURL = old }()
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-token")

	// rate limited: honours Retry-After (10 min) and doesn't ask again meanwhile
	_, next, err := PollLimits(context.Background())
	var le *LimitError
	if !errors.As(err, &le) || !le.RateLimited || time.Until(next) < 9*time.Minute {
		t.Fatalf("429: next in %s, err %v", time.Until(next), err)
	}
	for i := 0; i < 5; i++ { // other windows / restarts
		if _, _, err := PollLimits(context.Background()); err == nil {
			t.Fatal("expected the remembered error while backing off")
		}
	}
	if calls != 1 {
		t.Fatalf("made %d requests while backing off, want 1", calls)
	}

	// back-off over: one request, then everyone shares the cached result
	c := loadLimitCache()
	c.NotBefore = time.Now().Add(-time.Second)
	c.save()
	status = 200
	l, next, err := PollLimits(context.Background())
	if err != nil || l.SevenDay.Utilization != 9 || time.Until(next) < PollEvery-time.Second {
		t.Fatalf("ok poll: %v %+v next %s", err, l, time.Until(next))
	}
	for i := 0; i < 5; i++ {
		PollLimits(context.Background())
	}
	if calls != 2 {
		t.Fatalf("made %d requests, want 2 (cache should be shared)", calls)
	}
	if got := CachedLimits(); got == nil || got.SevenDay.Utilization != 9 {
		t.Fatal("restart should see the cached limits")
	}
}

func TestRecordStatusline(t *testing.T) {
	t.Setenv("CCBS_CACHE_DIR", t.TempDir())
	now := time.Unix(1_800_000_000, 0)
	in := `{"version":"2.1.300","model":{"display_name":"Opus"},
	  "rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1800003600},
	                 "seven_day":{"used_percentage":41.2,"resets_at":1800300000}}}`
	l, err := RecordStatusline([]byte(in), now)
	if err != nil || l == nil || l.SevenDay.Utilization != 41.2 || l.FiveHour.ResetsAt.Unix() != 1800003600 {
		t.Fatalf("record: %+v %v", l, err)
	}
	// a later update without rate_limits (new session, before its first reply)
	// keeps what we had
	if l, _ := RecordStatusline([]byte(`{"model":{"display_name":"Opus"}}`), now.Add(time.Minute)); l != nil {
		t.Error("an update without limits shouldn't produce limits")
	}
	got := StatuslineLimits(now.Add(2 * time.Minute))
	if got == nil || got.Source != SourceStatusline || got.SevenDay.Utilization != 41.2 {
		t.Fatalf("cached = %+v", got)
	}
	// once the 5h window resets it's dropped, the weekly one stays
	got = StatuslineLimits(now.Add(2 * time.Hour))
	if got == nil || got.FiveHour != nil || got.SevenDay == nil {
		t.Fatalf("after 5h reset = %+v", got)
	}
	if s := StatuslineSummary(got, "Opus", now); !strings.Contains(s, "week 41%") {
		t.Errorf("summary = %q", s)
	}
	if _, err := RecordStatusline([]byte(`not json`), now); err == nil {
		t.Error("bad input should error (and the caller still prints a line)")
	}
}
