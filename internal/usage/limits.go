package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// LimitWindow is one bucket of the subscription rate limit.
type LimitWindow struct {
	Utilization float64   `json:"utilization"` // percent
	ResetsAt    time.Time `json:"resets_at"`
}

// Limits is the account's live plan usage, as reported by Anthropic (the
// same numbers Claude Code's /usage screen shows).
type Limits struct {
	FiveHour       *LimitWindow `json:"five_hour"`
	SevenDay       *LimitWindow `json:"seven_day"`
	SevenDayOpus   *LimitWindow `json:"seven_day_opus"`
	SevenDaySonnet *LimitWindow `json:"seven_day_sonnet"`
	Fetched        time.Time    `json:"-"`
}

const usageURL = "https://api.anthropic.com/api/oauth/usage"

var ErrNoCredentials = errors.New("no Claude Code login found")

// oauthToken reads the Claude Code OAuth access token. It never leaves this
// process except as the Authorization header to api.anthropic.com.
func oauthToken() (string, error) {
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		return t, nil
	}
	var blob []byte
	dirs := []string{}
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		dirs = append(dirs, env)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude"))
	}
	for _, d := range dirs {
		if b, err := os.ReadFile(filepath.Join(d, ".credentials.json")); err == nil {
			blob = b
			break
		}
	}
	if blob == nil && runtime.GOOS == "darwin" {
		out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		if err == nil {
			blob = out
		}
	}
	if blob == nil {
		return "", ErrNoCredentials
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(blob, &creds); err != nil || creds.ClaudeAiOauth.AccessToken == "" {
		return "", ErrNoCredentials
	}
	if exp := creds.ClaudeAiOauth.ExpiresAt; exp > 0 && time.UnixMilli(exp).Before(time.Now()) {
		return "", errors.New("Claude Code login expired — run claude once to refresh")
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}

// FetchLimits asks Anthropic for the live 5h / weekly utilization.
func FetchLimits(ctx context.Context) (*Limits, error) {
	tok, err := oauthToken()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "ccbs")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage endpoint: %s", resp.Status)
	}
	var l Limits
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return nil, err
	}
	l.Fetched = time.Now()
	return &l, nil
}
