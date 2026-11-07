package usage

import "time"

// StatuslineQuiet is how long the status line may go without delivering
// limits before ccbs also asks the usage API. The Claude desktop app never
// runs status lines, so desktop-only users rely on the API and on estimates.
const StatuslineQuiet = 30 * time.Minute

// NeedsAPI reports whether the usage API should be consulted: the status
// line hasn't delivered limits recently (or ever).
func NeedsAPI(now time.Time) bool {
	r := loadStatuslineCache().Received
	return r.IsZero() || now.Sub(r) > StatuslineQuiet
}

// LatestReading merges what the status line and the usage API last
// reported, keeping the newest reading of each window that hasn't reset.
// Nil when neither has anything current.
func LatestReading(now time.Time) *Limits {
	sl := loadStatuslineCache()
	api := loadLimitCache().Limits

	type cand struct {
		w      *LimitWindow
		at     time.Time
		source string
	}
	pick := func(cs ...cand) (*LimitWindow, time.Time, string) {
		var best cand
		for _, c := range cs {
			if c.w == nil {
				continue
			}
			// a window is usable until it resets; one without a reset time
			// only for a while
			if c.w.ResetsAt.IsZero() {
				if now.Sub(c.at) > MaxLimitAge {
					continue
				}
			} else if !c.w.ResetsAt.After(now) {
				continue
			}
			if best.w == nil || c.at.After(best.at) {
				best = c
			}
		}
		if best.w == nil {
			return nil, time.Time{}, ""
		}
		w := *best.w
		w.AsOf = best.at
		return &w, best.at, best.source
	}
	var apiAt time.Time
	var apiFive, apiWeek, apiOpus, apiSonnet *LimitWindow
	if api != nil {
		apiAt = api.Fetched
		apiFive, apiWeek, apiOpus, apiSonnet = api.FiveHour, api.SevenDay, api.SevenDayOpus, api.SevenDaySonnet
	}
	l := &Limits{}
	var t1, t2 time.Time
	var s1, s2 string
	l.FiveHour, t1, s1 = pick(cand{sl.FiveHour, sl.Received, SourceStatusline}, cand{apiFive, apiAt, ""})
	l.SevenDay, t2, s2 = pick(cand{sl.SevenDay, sl.Received, SourceStatusline}, cand{apiWeek, apiAt, ""})
	// per-model weekly limits only come from the API
	l.SevenDayOpus, _, _ = pick(cand{apiOpus, apiAt, ""})
	l.SevenDaySonnet, _, _ = pick(cand{apiSonnet, apiAt, ""})
	if l.FiveHour == nil && l.SevenDay == nil {
		return nil
	}
	l.Fetched = t1
	if t2.After(l.Fetched) {
		l.Fetched = t2
	}
	if s1 == SourceStatusline || s2 == SourceStatusline {
		l.Source = SourceStatusline
	}
	return l
}

// Extrapolate carries each window's last reading forward to now using the
// API-equivalent cost of what Claude Code logged since. The ratio comes from
// the reading itself: % used per dollar spent in that window so far. Usage
// that isn't in the local logs (claude.ai in a browser, another computer)
// isn't seen, so estimates can run low until the next real reading.
func Extrapolate(l *Limits, entries []Entry, now time.Time) *Limits {
	if l == nil {
		return nil
	}
	out := *l
	carry := func(w *LimitWindow, length time.Duration) *LimitWindow {
		if w == nil || w.ResetsAt.IsZero() || w.AsOf.IsZero() {
			return w
		}
		if now.Sub(w.AsOf) < 2*time.Minute {
			return w // fresh enough to show as is
		}
		start := w.ResetsAt.Add(-length)
		_, base, _ := sum(entries, start, w.AsOf)
		if base < 0.5 || w.Utilization <= 0 {
			return w // too little local activity to calibrate against
		}
		_, since, _ := sum(entries, w.AsOf, now.Add(time.Second))
		add := since * w.Utilization / base
		if add < 0.5 {
			return w // nothing worth a "~"
		}
		c := *w
		c.Utilization = min(w.Utilization+add, 100)
		c.Approx = true
		return &c
	}
	out.FiveHour = carry(l.FiveHour, FiveHours)
	out.SevenDay = carry(l.SevenDay, Week)
	return &out
}
