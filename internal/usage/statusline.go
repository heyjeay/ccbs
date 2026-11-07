package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Claude Code passes a JSON document to its status line command on every
// update. For Pro/Max/Ultra subscribers it includes rate_limits:
//
//	"rate_limits": {
//	  "five_hour": {"used_percentage": 23.5, "resets_at": 1738425600},
//	  "seven_day": {"used_percentage": 41.2, "resets_at": 1738857600}
//	}
//
// `ccbs statusline` records that into a small cache file. The dashboard then
// shows exactly what Claude Code itself knows, with no network calls, no
// login handling and no rate limits. (The % only changes when Claude Code is
// used, and every use refreshes the status line, so the cache stays current.)

type slWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

type slInput struct {
	Version   string `json:"version"`
	SessionID string `json:"session_id"`
	Model     struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	RateLimits *struct {
		FiveHour *slWindow `json:"five_hour"`
		SevenDay *slWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

// statuslineCache is what `ccbs statusline` writes.
type statuslineCache struct {
	Received time.Time    `json:"received"` // last time Claude Code reported limits
	Version  string       `json:"version,omitempty"`
	FiveHour *LimitWindow `json:"five_hour,omitempty"`
	SevenDay *LimitWindow `json:"seven_day,omitempty"`
	// LastCall is the last time Claude Code ran `ccbs statusline` at all,
	// with or without limits; --doctor uses it to tell "not being called"
	// apart from "called, but no limits in the input".
	LastCall time.Time `json:"last_call"`
}

// StatuslineState says what the status line route has seen so far.
type StatuslineState struct {
	LastCall, LastLimits time.Time
	Version              string
}

func GetStatuslineState() StatuslineState {
	c := loadStatuslineCache()
	return StatuslineState{LastCall: c.LastCall, LastLimits: c.Received, Version: c.Version}
}

func statuslineCachePath() string {
	return filepath.Join(filepath.Dir(limitCachePath()), "statusline.json")
}

func (w *slWindow) limit() *LimitWindow {
	if w == nil || w.UsedPercentage == nil {
		return nil
	}
	lw := &LimitWindow{Utilization: *w.UsedPercentage}
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		lw.ResetsAt = time.Unix(int64(*w.ResetsAt), 0)
	}
	return lw
}

// RecordStatusline stores the rate limits from one status line update and
// returns them (nil when this update carried none). Windows missing from an
// update keep their previous value until that value's reset time passes.
func RecordStatusline(input []byte, now time.Time) (*Limits, error) {
	var in slInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("status line input isn't JSON: %w", err)
	}
	c := loadStatuslineCache()
	keep := func(w *LimitWindow) *LimitWindow {
		if w != nil && w.ResetsAt.After(now) {
			return w
		}
		return nil
	}
	c.FiveHour, c.SevenDay = keep(c.FiveHour), keep(c.SevenDay)
	got := false
	if rl := in.RateLimits; rl != nil {
		if w := rl.FiveHour.limit(); w != nil {
			c.FiveHour, got = w, true
		}
		if w := rl.SevenDay.limit(); w != nil {
			c.SevenDay, got = w, true
		}
	}
	c.LastCall = now
	if in.Version != "" {
		c.Version = in.Version
	}
	if got {
		c.Received = now
	}
	p := statuslineCachePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(c)
	tmp := fmt.Sprintf("%s.%d.tmp", p, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if !got {
		return nil, nil
	}
	return c.limits(), nil
}

func loadStatuslineCache() statuslineCache {
	var c statuslineCache
	if b, err := os.ReadFile(statuslineCachePath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func (c statuslineCache) limits() *Limits {
	return &Limits{FiveHour: c.FiveHour, SevenDay: c.SevenDay, Fetched: c.Received, Source: SourceStatusline}
}

// StatuslineLimits returns the limits Claude Code last reported through the
// status line, keeping only windows that haven't reset yet. Nil when there
// is nothing current.
func StatuslineLimits(now time.Time) *Limits {
	c := loadStatuslineCache()
	l := c.limits()
	if l.FiveHour != nil && !l.FiveHour.ResetsAt.After(now) {
		l.FiveHour = nil
	}
	if l.SevenDay != nil && !l.SevenDay.ResetsAt.After(now) {
		l.SevenDay = nil
	}
	if l.FiveHour == nil && l.SevenDay == nil {
		return nil
	}
	return l
}

// StatuslineSummary is the short line ccbs prints when it's the status line.
func StatuslineSummary(l *Limits, model string, now time.Time) string {
	var parts []string
	if model != "" {
		parts = append(parts, model)
	}
	if l != nil && l.FiveHour != nil {
		s := fmt.Sprintf("5h %.0f%%", l.FiveHour.Utilization)
		if !l.FiveHour.ResetsAt.IsZero() {
			s += " (resets " + l.FiveHour.ResetsAt.Local().Format("15:04") + ")"
		}
		parts = append(parts, s)
	}
	if l != nil && l.SevenDay != nil {
		s := fmt.Sprintf("week %.0f%%", l.SevenDay.Utilization)
		if !l.SevenDay.ResetsAt.IsZero() {
			s += " (resets " + l.SevenDay.ResetsAt.Local().Format("Mon 15:04") + ")"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "ccbs"
	}
	return strings.Join(parts, " · ")
}

// ModelName pulls the model's display name from status line input.
func ModelName(input []byte) string {
	var in slInput
	_ = json.Unmarshal(input, &in)
	return in.Model.DisplayName
}
