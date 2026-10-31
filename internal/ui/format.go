package ui

import (
	"fmt"
	"math"
	"time"
)

func fmtTok(n float64) string {
	a := math.Abs(n)
	switch {
	case a >= 1e9:
		return trimF(n/1e9) + "B"
	case a >= 1e6:
		return trimF(n/1e6) + "M"
	case a >= 1e3:
		return trimF(n/1e3) + "k"
	}
	return fmt.Sprintf("%.0f", n)
}

func trimF(v float64) string {
	switch {
	case math.Abs(v) >= 100:
		return fmt.Sprintf("%.0f", v)
	case math.Abs(v) >= 10:
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func fmtCost(c float64) string {
	switch {
	case c >= 1000:
		return fmt.Sprintf("$%.1fk", c/1000)
	case c >= 100:
		return fmt.Sprintf("$%.0f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func fmtAgo(now, t time.Time) string {
	d := now.Sub(t)
	if d < 5*time.Second {
		return "now"
	}
	return fmtDur(d) + " ago"
}

// fmtWhen is compact: "14:03" today, "Thu 14:03" this week, "Oct 21" older.
func fmtWhen(now, t time.Time) string {
	t = t.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 02")
}

// fmtWhenShort fits in 5 columns: "14:03", "Thu", "10/21".
func fmtWhenShort(now, t time.Time) string {
	t = t.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	}
	return t.Format("01/02")
}

func fmtPct(v float64) string {
	if v < 0 {
		return "  —"
	}
	return fmt.Sprintf("%3.0f%%", v)
}
