package usage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// PollEvery is how often ccbs asks Anthropic's usage API. The endpoint rate
// limits eagerly, and between readings ccbs estimates from local usage
// (see Extrapolate), so there is no reason to ask more often.
const PollEvery = 10 * time.Minute

// MaxLimitAge is how long a remembered result is still worth showing.
const MaxLimitAge = 12 * time.Hour

// limitCache is shared by every ccbs process on the machine, so several
// windows (and restarts) make one request between them, and all of them
// respect the same back-off after Anthropic says "slow down".
type limitCache struct {
	Limits    *Limits   `json:"limits,omitempty"`
	NotBefore time.Time `json:"not_before"`
	Strikes   int       `json:"strikes"` // consecutive rate-limit replies
	LastError string    `json:"last_error,omitempty"`
}

func limitCachePath() string {
	if d := os.Getenv("CCBS_CACHE_DIR"); d != "" {
		return filepath.Join(d, "limits.json")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "ccbs", "limits.json")
}

func loadLimitCache() limitCache {
	var c limitCache
	if b, err := os.ReadFile(limitCachePath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func (c limitCache) save() {
	p := limitCachePath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	b, _ := json.Marshal(c)
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}

// CachedLimits returns the last result any ccbs process got, if recent
// enough to show. Used to fill the screen instantly on start.
func CachedLimits() *Limits {
	c := loadLimitCache()
	if c.Limits != nil && time.Since(c.Limits.Fetched) < MaxLimitAge {
		return c.Limits
	}
	return nil
}

// PollLimits returns the freshest limits it can without overloading the
// endpoint: the shared cached result while it's fresh or while backing off,
// otherwise a new request. next is when it's worth calling again; err is
// the last failure (limits may still be non-nil: the last good result).
func PollLimits(ctx context.Context) (l *Limits, next time.Time, err error) {
	c := loadLimitCache()
	now := time.Now()
	shown := func() *Limits {
		if c.Limits != nil && now.Sub(c.Limits.Fetched) < MaxLimitAge {
			return c.Limits
		}
		return nil
	}
	if c.Limits != nil && now.Sub(c.Limits.Fetched) < PollEvery && !now.Before(c.Limits.Fetched) {
		return c.Limits, c.Limits.Fetched.Add(PollEvery), nil
	}
	if now.Before(c.NotBefore) {
		var e error
		if c.LastError != "" {
			e = &LimitError{Msg: c.LastError, Retry: c.NotBefore.Sub(now)}
		}
		return shown(), c.NotBefore, e
	}

	fresh, ferr := FetchLimits(ctx)
	if ferr == nil {
		c = limitCache{Limits: fresh, NotBefore: now.Add(PollEvery)}
		c.save()
		return fresh, c.NotBefore, nil
	}
	wait := time.Minute
	var le *LimitError
	if errors.As(ferr, &le) {
		wait = le.Retry
		if le.RateLimited {
			// back off harder each time it happens again: 5, 10, 20, 30 min
			c.Strikes++
			wait = max(wait, min(5*time.Minute<<(c.Strikes-1), 30*time.Minute))
		}
	}
	if le == nil || !le.RateLimited {
		c.Strikes = 0
	}
	c.NotBefore, c.LastError = now.Add(wait), ferr.Error()
	c.save()
	return shown(), c.NotBefore, ferr
}
