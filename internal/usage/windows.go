package usage

import (
	"sort"
	"time"
)

const (
	FiveHours = 5 * time.Hour
	Week      = 7 * 24 * time.Hour
)

// Block is a ccusage-style 5h billing block reconstructed from transcripts:
// it starts at the top of the hour of the first message and lasts 5h.
type Block struct {
	Start, End time.Time
	Last       time.Time
	Tokens     Tokens
	Cost       float64
	Calls      int
}

func Blocks(entries []Entry) []Block {
	var out []Block
	for _, e := range entries {
		n := len(out)
		if n == 0 || !e.Time.Before(out[n-1].End) || e.Time.Sub(out[n-1].Last) >= FiveHours {
			st := e.Time.Truncate(time.Hour)
			out = append(out, Block{Start: st, End: st.Add(FiveHours)})
			n++
		}
		b := &out[n-1]
		b.Last = e.Time
		b.Tokens.Add(e.Tokens)
		b.Cost += e.Cost
		b.Calls++
	}
	return out
}

// sum totals entries in [start, end).
func sum(entries []Entry, start, end time.Time) (t Tokens, cost float64, calls int) {
	i := sort.Search(len(entries), func(i int) bool { return !entries[i].Time.Before(start) })
	for ; i < len(entries) && entries[i].Time.Before(end); i++ {
		t.Add(entries[i].Tokens)
		cost += entries[i].Cost
		calls++
	}
	return
}

// FiveHourWindow returns the current 5h window. Live limits are preferred;
// otherwise the window is reconstructed and utilization is estimated against
// the most expensive previous block (your observed peak).
func FiveHourWindow(snap Snapshot, now time.Time, live *Limits) (w Window, active bool) {
	if live != nil && live.FiveHour != nil && live.FiveHour.ResetsAt.After(now) {
		w.End = live.FiveHour.ResetsAt
		w.Start = w.End.Add(-FiveHours)
		w.Utilization = live.FiveHour.Utilization
		w.Tokens, w.Cost, w.Calls = sum(snap.Entries, w.Start, w.End)
		return w, true
	}
	blocks := Blocks(snap.Entries)
	w.Utilization = -1
	w.Estimated = true
	if n := len(blocks); n > 0 && now.Before(blocks[n-1].End) {
		b := blocks[n-1]
		w.Start, w.End = b.Start, b.End
		w.Tokens, w.Cost, w.Calls = b.Tokens, b.Cost, b.Calls
		active = true
		peak := 0.0
		for _, pb := range blocks[:n-1] {
			if pb.Cost > peak {
				peak = pb.Cost
			}
		}
		if peak > 0 {
			w.Utilization = min(100*w.Cost/peak, 999)
		}
	} else {
		w.Start = now.Truncate(time.Hour)
		w.End = w.Start.Add(FiveHours)
	}
	return w, active
}

// WeeklyWindow returns the 7-day window; rolling when no live data.
func WeeklyWindow(snap Snapshot, now time.Time, live *Limits) Window {
	var w Window
	if live != nil && live.SevenDay != nil && live.SevenDay.ResetsAt.After(now) {
		w.End = live.SevenDay.ResetsAt
		w.Start = w.End.Add(-Week)
		w.Utilization = live.SevenDay.Utilization
	} else {
		w.End = now
		w.Start = now.Add(-Week)
		w.Utilization = -1
		w.Estimated = true
	}
	w.Tokens, w.Cost, w.Calls = sum(snap.Entries, w.Start, now.Add(time.Second))
	return w
}

// Series buckets total tokens over [start, end) into n buckets.
func Series(entries []Entry, start, end time.Time, n int) []float64 {
	out := make([]float64, n)
	if n == 0 || !end.After(start) {
		return out
	}
	span := end.Sub(start)
	i := sort.Search(len(entries), func(i int) bool { return !entries[i].Time.Before(start) })
	for ; i < len(entries) && entries[i].Time.Before(end); i++ {
		k := int(int64(entries[i].Time.Sub(start)) * int64(n) / int64(span))
		if k >= 0 && k < n {
			out[k] += float64(entries[i].Tokens.Total())
		}
	}
	return out
}

// Day is usage for one local calendar day.
type Day struct {
	Date   time.Time
	Tokens Tokens
	Cost   float64
	Calls  int
}

// Days returns the last n local days, oldest first.
func Days(entries []Entry, now time.Time, n int) []Day {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	out := make([]Day, n)
	for i := 0; i < n; i++ {
		day := today.AddDate(0, 0, i-n+1)
		out[i].Date = day
		out[i].Tokens, out[i].Cost, out[i].Calls = sum(entries, day, day.AddDate(0, 0, 1))
	}
	return out
}

// Burn is the recent consumption rate.
type Burn struct {
	TokensPerMin float64
	CostPerMin   float64
}

func BurnRate(entries []Entry, now time.Time, over time.Duration) Burn {
	t, c, _ := sum(entries, now.Add(-over), now.Add(time.Second))
	mins := over.Minutes()
	return Burn{TokensPerMin: float64(t.Total()) / mins, CostPerMin: c / mins}
}

// ProjectedHit estimates when utilization will reach 100% at the current
// burn rate. ok=false means it won't before the window resets.
func ProjectedHit(w Window, burn Burn, now time.Time) (at time.Time, ok bool) {
	if w.Utilization <= 0 || w.Cost <= 0 || burn.CostPerMin <= 0 {
		return time.Time{}, false
	}
	if w.Utilization >= 100 {
		return now, true
	}
	utilPerDollar := w.Utilization / w.Cost
	mins := (100 - w.Utilization) / (utilPerDollar * burn.CostPerMin)
	at = now.Add(time.Duration(mins * float64(time.Minute)))
	return at, at.Before(w.End)
}
