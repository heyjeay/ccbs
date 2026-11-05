package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// LimitWindow is one bucket of the subscription rate limit.
type LimitWindow struct {
	Utilization float64   // percent
	ResetsAt    time.Time // zero when Anthropic didn't say
}

// Limits is the account's live plan usage, as reported by Anthropic (the
// same numbers Claude Code's /usage screen shows).
type Limits struct {
	FiveHour       *LimitWindow
	SevenDay       *LimitWindow
	SevenDayOpus   *LimitWindow
	SevenDaySonnet *LimitWindow
	Fetched        time.Time
}

var usageURL = "https://api.anthropic.com/api/oauth/usage"

var ErrNoCredentials = errors.New("no Claude Code login found")

// LimitError is a failed fetch, with how long to wait before trying again.
type LimitError struct {
	Msg   string
	Retry time.Duration
}

func (e *LimitError) Error() string { return e.Msg }

type credentials struct {
	token   string
	source  string    // where the login was found (never the token itself)
	expires time.Time // as written in the file; informational only
}

// readCredentials finds the Claude Code OAuth login. The token never leaves
// this process except as the Authorization header to api.anthropic.com.
func readCredentials() (credentials, error) {
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		return credentials{token: t, source: "CLAUDE_CODE_OAUTH_TOKEN"}, nil
	}
	var blob []byte
	source := ""
	var dirs []string
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		for _, d := range strings.Split(env, ",") {
			dirs = append(dirs, strings.TrimSpace(d))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude"), filepath.Join(home, ".config", "claude"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, ".credentials.json")
		if b, err := os.ReadFile(p); err == nil {
			blob, source = b, p
			break
		}
	}
	if blob == nil && runtime.GOOS == "darwin" {
		out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		if err == nil {
			blob, source = out, "macOS keychain"
		}
	}
	if blob == nil {
		return credentials{}, ErrNoCredentials
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(blob, &creds); err != nil || creds.ClaudeAiOauth.AccessToken == "" {
		return credentials{source: source}, fmt.Errorf("no Claude Code login in %s", source)
	}
	c := credentials{token: creds.ClaudeAiOauth.AccessToken, source: source}
	if exp := creds.ClaudeAiOauth.ExpiresAt; exp > 0 {
		if exp < 1e12 { // seconds, not milliseconds
			exp *= 1000
		}
		c.expires = time.UnixMilli(exp)
	}
	return c, nil
}

// rawResponse is the endpoint's reply plus how long it took.
type rawResponse struct {
	status  int
	body    []byte
	elapsed time.Duration
}

func callUsage(ctx context.Context, token string) (rawResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ccbs")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rawResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return rawResponse{status: resp.StatusCode, body: body, elapsed: time.Since(start)}, err
}

func statusError(status int) *LimitError {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &LimitError{"login expired; open Claude Code once to refresh it", 2 * time.Minute}
	case status == http.StatusTooManyRequests:
		return &LimitError{"Anthropic is rate limiting; retrying in 5m", 5 * time.Minute}
	case status >= 500:
		return &LimitError{fmt.Sprintf("Anthropic error %d; retrying", status), time.Minute}
	}
	return &LimitError{fmt.Sprintf("usage endpoint returned %d", status), 5 * time.Minute}
}

// parseLimits reads whatever windows the reply contains. It tolerates null
// windows, a missing or null resets_at, and fields it doesn't know.
func parseLimits(body []byte) (*Limits, map[string]*LimitWindow, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, fmt.Errorf("unexpected reply from usage endpoint")
	}
	all := map[string]*LimitWindow{}
	for k, v := range raw {
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    *string  `json:"resets_at"`
		}
		if json.Unmarshal(v, &w) != nil || w.Utilization == nil {
			continue
		}
		lw := &LimitWindow{Utilization: *w.Utilization}
		if w.ResetsAt != nil && *w.ResetsAt != "" {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
				if t, err := time.Parse(layout, *w.ResetsAt); err == nil {
					lw.ResetsAt = t
					break
				}
			}
		}
		all[k] = lw
	}
	l := &Limits{
		FiveHour:       all["five_hour"],
		SevenDay:       all["seven_day"],
		SevenDayOpus:   all["seven_day_opus"],
		SevenDaySonnet: all["seven_day_sonnet"],
		Fetched:        time.Now(),
	}
	if l.FiveHour == nil && l.SevenDay == nil {
		return l, all, fmt.Errorf("usage endpoint had no 5h or weekly limits")
	}
	return l, all, nil
}

// FetchLimits asks Anthropic for the live 5h / weekly utilization.
func FetchLimits(ctx context.Context) (*Limits, error) {
	c, err := readCredentials()
	if err != nil {
		return nil, &LimitError{err.Error() + "; sign in to Claude Code", 2 * time.Minute}
	}
	r, err := callUsage(ctx, c.token)
	if err != nil {
		return nil, &LimitError{"can't reach api.anthropic.com", time.Minute}
	}
	if r.status != http.StatusOK {
		return nil, statusError(r.status)
	}
	l, _, err := parseLimits(r.body)
	if err != nil {
		return nil, &LimitError{err.Error(), 5 * time.Minute}
	}
	return l, nil
}

// Diagnose explains, step by step, whether live limits work on this machine.
// It is what `ccbs --doctor` prints. It never prints the token.
func Diagnose(ctx context.Context, w io.Writer) {
	ok := func(f string, a ...any) { fmt.Fprintf(w, "  \x1b[32m✓\x1b[0m "+f+"\n", a...) }
	bad := func(f string, a ...any) { fmt.Fprintf(w, "  \x1b[31m✗\x1b[0m "+f+"\n", a...) }
	info := func(f string, a ...any) { fmt.Fprintf(w, "      "+f+"\n", a...) }

	fmt.Fprintln(w, "ccbs doctor")
	fmt.Fprintln(w)
	dirs := DefaultDirs()
	if len(dirs) == 0 {
		bad("no Claude Code transcripts found (looked for ~/.claude/projects)")
	}
	for _, d := range dirs {
		var files int
		var size int64
		_ = filepath.Walk(d, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".jsonl") {
				files++
				size += fi.Size()
			}
			return nil
		})
		ok("transcripts: %s (%d files, %.1f MB)", d, files, float64(size)/(1<<20))
	}

	c, err := readCredentials()
	if err != nil {
		bad("Claude Code login: %v", err)
		info("live 5h/weekly %% need a Claude Code login on this machine (Pro/Max).")
		info("run `claude`, sign in with your Claude account, then try again.")
		return
	}
	ok("Claude Code login found in %s", c.source)
	if !c.expires.IsZero() {
		if d := time.Until(c.expires); d > 0 {
			info("access token valid for another %s", fmtDuration(d))
		} else {
			info("access token says it expired %s ago (Claude Code refreshes it when it runs)", fmtDuration(-d))
		}
	}

	r, err := callUsage(ctx, c.token)
	if err != nil {
		bad("couldn't reach api.anthropic.com: %v", err)
		info("check your internet connection, VPN or proxy.")
		return
	}
	if r.status != http.StatusOK {
		bad("usage endpoint answered %d %s (%s)", r.status, http.StatusText(r.status), r.elapsed.Round(time.Millisecond))
		info("%s", statusError(r.status).Msg)
		if snippet := strings.TrimSpace(string(r.body)); snippet != "" {
			if len(snippet) > 300 {
				snippet = snippet[:300] + "…"
			}
			info("reply: %s", snippet)
		}
		return
	}
	ok("usage endpoint answered 200 OK (%s)", r.elapsed.Round(time.Millisecond))
	l, all, perr := parseLimits(r.body)
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lw := all[k]
		when := "no reset time given"
		if !lw.ResetsAt.IsZero() {
			when = "resets " + lw.ResetsAt.Local().Format("Mon 15:04") + ", in " + fmtDuration(time.Until(lw.ResetsAt))
		}
		info("%-22s %5.1f%%   %s", k, lw.Utilization, when)
	}
	if perr != nil {
		bad("%v", perr)
		return
	}
	if l.SevenDay == nil {
		bad("no weekly (seven_day) limit in the reply, so the weekly box can't show a %%")
	} else {
		ok("weekly limit: %.0f%% used", l.SevenDay.Utilization)
	}
	if l.FiveHour == nil {
		bad("no five_hour limit in the reply")
	} else {
		ok("5-hour limit: %.0f%% used", l.FiveHour.Utilization)
	}
}

func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
