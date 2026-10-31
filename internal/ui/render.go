package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/heyjeay/ccbm/internal/usage"
)

type SortMode int

const (
	SortRecent SortMode = iota
	SortTokens
	SortCost
	SortDuration
	sortModes
)

func (s SortMode) String() string {
	return [...]string{"recent", "tokens", "cost", "duration"}[s]
}

// State is everything a frame needs.
type State struct {
	Now      time.Time
	Snap     usage.Snapshot
	Live     *usage.Limits
	LiveErr  string
	Demo     bool
	Sel      int
	Sort     SortMode
	Frame    int
	Interval time.Duration
	ActiveFor time.Duration // a session counts as "now" if touched within this
}

// derived per-frame data
type frame struct {
	*State
	th       Theme
	five     usage.Window
	fiveOn   bool
	week     usage.Window
	burn     usage.Burn
	history  []*usage.Session
	active   []*usage.Session
}

func Render(st *State, w, h int, th Theme) string {
	c := NewCanvas(w, h, th.MainBG)
	if w < 40 || h < 16 {
		c.Text(1, 1, "ccbm: terminal too small", Style{FG: th.HiFG}, 0)
		c.Text(1, 2, fmt.Sprintf("need 40x16, have %dx%d", w, h), Style{FG: th.MainFG}, 0)
		return c.String()
	}
	f := &frame{State: st, th: th}
	f.five, f.fiveOn = usage.FiveHourWindow(st.Snap, st.Now, st.Live)
	f.week = usage.WeeklyWindow(st.Snap, st.Now, st.Live)
	f.burn = usage.BurnRate(st.Snap.Entries, st.Now, 15*time.Minute)
	f.history = sortedSessions(st.Snap.Sessions, st.Sort, st.Now)
	act := st.ActiveFor
	if act == 0 {
		act = 15 * time.Minute
	}
	for _, s := range st.Snap.Sessions {
		if st.Now.Sub(s.End) <= act {
			f.active = append(f.active, s)
		}
	}
	if st.Sel >= len(f.history) {
		st.Sel = len(f.history) - 1
	}
	if st.Sel < 0 {
		st.Sel = 0
	}

	if w >= 100 && h >= 30 {
		topH := clampI(h*2/5, 12, 17)
		lw := clampI(w*2/5, 46, 66)
		weekH := clampI(h-topH-8, 8, 14)
		f.drawFive(c, Rect{0, 0, w, topH}, true)
		f.drawWeek(c, Rect{0, topH, lw, weekH})
		f.drawNow(c, Rect{0, topH + weekH, lw, h - topH - weekH})
		f.drawHistory(c, Rect{lw, topH, w - lw, h - topH}, true)
	} else {
		// stacked layout for phones / narrow splits
		fiveH, weekH := 13, 12
		nowH := clampI(2+4*len(f.active), 4, 10)
		if len(f.active) == 0 {
			nowH = 3
		}
		rest := h - fiveH - weekH - nowH
		if rest < 8 {
			weekH = 5
			rest = h - fiveH - weekH - nowH
		}
		if rest < 6 {
			nowH = 0
			rest = h - fiveH - weekH
		}
		y := 0
		f.drawFive(c, Rect{0, y, w, fiveH}, false)
		y += fiveH
		f.drawWeek(c, Rect{0, y, w, weekH})
		y += weekH
		if nowH > 0 {
			f.drawNow(c, Rect{0, y, w, nowH})
			y += nowH
		}
		f.drawHistory(c, Rect{0, y, w, h - y}, w >= 70)
	}
	return c.String()
}

func sortedSessions(in []*usage.Session, mode SortMode, now time.Time) []*usage.Session {
	out := append([]*usage.Session(nil), in...)
	key := func(s *usage.Session) float64 {
		switch mode {
		case SortTokens:
			return float64(s.Tokens.Total())
		case SortCost:
			return s.Cost
		case SortDuration:
			return float64(s.End.Sub(s.Start))
		}
		return float64(s.End.UnixNano())
	}
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) > key(out[j]) })
	return out
}

// ───────────────────────────── 5h window ─────────────────────────────

func (f *frame) sourceTag() []Tag {
	th := f.th
	switch {
	case f.Demo:
		return []Tag{{Text: "● ", FG: th.WeekEnd}, {Text: "demo"}}
	case f.Live != nil:
		return []Tag{{Text: "● ", FG: th.Ok}, {Text: "live"}}
	}
	return []Tag{{Text: "● ", FG: th.Warn}, {Text: "local est."}}
}

func (f *frame) drawFive(c *Canvas, r Rect, wide bool) {
	th := f.th
	col := th.BoxFive
	c.Box(r, 1, "5h window", col, th)
	if r.W >= 60 {
		c.CenterTag(r, r.Y, col, th, Tag{Text: "ccbm ", Hi: true}, Tag{Text: f.Now.Format("15:04:05")})
	}
	c.BorderTag(r.X+r.W-2, r.Y, col, th, f.sourceTag()...)
	in := r.Inner()

	var graph, stats Rect
	if wide {
		sw := 38
		graph = Rect{in.X, in.Y, in.W - sw - 1, in.H - 1}
		stats = Rect{in.X + in.W - sw, in.Y, sw, in.H}
	} else {
		lines := 6
		graph = Rect{in.X, in.Y, in.W, in.H - lines - 1}
		stats = Rect{in.X, in.Y + graph.H + 1, in.W, lines}
	}
	f.drawFiveGraph(c, graph)
	if wide {
		c.Box(stats, -1, "usage", th.DivLine, th)
		f.drawFiveStats(c, stats.Inner(), true)
	} else {
		f.drawFiveStats(c, stats, false)
	}
	interval := f.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}
	c.BorderTag(r.X+r.W-2, r.Y+r.H-1, col, th,
		Tag{Text: "-", Hi: true}, Tag{Text: fmt.Sprintf(" %dms ", interval.Milliseconds())}, Tag{Text: "+", Hi: true})
}

func (f *frame) drawFiveGraph(c *Canvas, g Rect) {
	th := f.th
	if g.W < 4 || g.H < 1 {
		return
	}
	w := f.five
	n := g.W * 2
	raw := usage.Series(f.Snap.Entries, w.Start, w.End, n)
	bucket := w.End.Sub(w.Start) / time.Duration(n)
	// normalise against the busiest bucket so far
	peak := 0.0
	nowIdx := int(float64(n) * float64(f.Now.Sub(w.Start)) / float64(w.End.Sub(w.Start)))
	for i, v := range raw {
		if i <= nowIdx {
			peak = math.Max(peak, v)
		}
	}
	vals := make([]float64, n)
	for i := range vals {
		switch {
		case i > nowIdx:
			vals[i] = math.NaN()
		case peak > 0:
			// sqrt keeps quiet minutes visible next to bursts
			vals[i] = math.Sqrt(raw[i] / peak)
		}
	}
	c.Graph(g, vals, th.Heat, th.DivLine)

	// scale label, btop-style faint overlay
	if peak > 0 && g.H >= 3 {
		perMin := peak / bucket.Minutes()
		c.Text(g.X, g.Y, "▲ "+fmtTok(perMin)+"/min", Style{FG: th.GraphText}, 0)
	}
	if !f.fiveOn && f.Live == nil {
		msg := "idle — next window opens with your next message"
		c.Text(g.X+max(0, (g.W-Width(msg))/2), g.Y+g.H/2, Truncate(msg, g.W), Style{FG: th.GraphText}, 0)
	}

	// time axis
	ay := g.Y + g.H
	start := w.Start
	span := w.End.Sub(w.Start)
	t := start.Truncate(time.Hour)
	if t.Before(start) {
		t = t.Add(time.Hour)
	}
	for ; !t.After(w.End); t = t.Add(time.Hour) {
		x := g.X + int(float64(g.W)*float64(t.Sub(start))/float64(span))
		lbl := t.In(f.Now.Location()).Format("15:04")
		if x+Width(lbl) > g.X+g.W {
			x = g.X + g.W - Width(lbl)
		}
		c.Text(x, ay, lbl, Style{FG: th.GraphText}, 0)
	}
	if nowIdx >= 0 && nowIdx < n {
		if x := g.X + nowIdx/2; c.Free(x, ay) {
			c.Set(x, ay, '▲', Style{FG: th.HiFG, Bold: true})
		}
	}
}

func (f *frame) drawFiveStats(c *Canvas, r Rect, boxed bool) {
	th := f.th
	w := f.five
	lbl := Style{FG: th.GraphText}
	val := Style{FG: th.MainFG}
	hi := Style{FG: th.Title, Bold: true}
	x := r.X + 1
	right := r.X + r.W - 1
	y := r.Y
	line := func(fn func(y int)) {
		if y < r.Y+r.H {
			fn(y)
		}
		y++
	}

	util := w.Utilization
	line(func(y int) {
		c.Text(x, y, "used", lbl, 0)
		mw := r.W - 13
		if util >= 0 {
			c.Meter(x+5, y, mw, util/100, th, th.Heat)
			c.TextRight(right, y, fmtPct(util), Style{FG: th.Heat(util / 100), Bold: true})
		} else {
			c.Meter(x+5, y, mw, 0, th, th.Heat)
			c.TextRight(right, y, "  —", val)
		}
	})
	line(func(y int) {
		c.Text(x, y, "resets", lbl, 0)
		c.Text(x+7, y, "in "+fmtDur(w.Remaining(f.Now)), hi, 0)
		c.TextRight(right, y, w.End.In(f.Now.Location()).Format("15:04"), val)
	})
	if boxed {
		line(func(y int) {
			c.Text(x, y, "elapsed", lbl, 0)
			c.Text(x+8, y, fmtDur(w.Elapsed(f.Now))+" of 5h", val, 0)
			c.TextRight(right, y, fmt.Sprintf("%d calls", w.Calls), lbl)
		})
		line(func(y int) {
			for i := r.X; i < r.X+r.W; i++ {
				c.Set(i, y, '─', Style{FG: th.DivLine})
			}
		})
	}
	line(func(y int) {
		c.Text(x, y, "tokens", lbl, 0)
		c.Text(x+7, y, fmtTok(float64(w.Tokens.Total())), hi, 0)
		c.TextRight(right, y, fmtCost(w.Cost)+" api-eq", Style{FG: th.ProcMisc})
	})
	t := w.Tokens
	if boxed {
		line(func(y int) {
			c.Text(x+1, y, Truncate(fmt.Sprintf("input %s · output %s", fmtTok(float64(t.In)), fmtTok(float64(t.Out))), r.W-3), lbl, 0)
		})
		line(func(y int) {
			c.Text(x+1, y, Truncate(fmt.Sprintf("cache read %s · write %s", fmtTok(float64(t.CacheR)), fmtTok(float64(t.CacheW))), r.W-3), lbl, 0)
		})
	} else {
		line(func(y int) {
			s := fmt.Sprintf("in %s · out %s · cache r %s w %s", fmtTok(float64(t.In)), fmtTok(float64(t.Out)),
				fmtTok(float64(t.CacheR)), fmtTok(float64(t.CacheW)))
			c.Text(x+1, y, Truncate(s, r.W-3), lbl, 0)
		})
	}
	line(func(y int) {
		c.Text(x, y, "burn", lbl, 0)
		c.Text(x+7, y, fmtTok(f.burn.TokensPerMin)+"/min", val, 0)
		c.TextRight(right, y, fmtCost(f.burn.CostPerMin)+"/min", lbl)
	})
	line(func(y int) {
		at, hits := usage.ProjectedHit(w, f.burn, f.Now)
		c.Text(x, y, "pace", lbl, 0)
		switch {
		case util < 0:
			c.Text(x+7, y, "no limit data", lbl, 0)
		case hits:
			c.Text(x+7, y, "⚠ limit ~"+at.In(f.Now.Location()).Format("15:04"), Style{FG: th.Bad, Bold: true}, 0)
			c.TextRight(right, y, "in "+fmtDur(at.Sub(f.Now)), Style{FG: th.Bad})
		default:
			c.Text(x+7, y, "✓ safe until reset", Style{FG: th.Ok}, 0)
		}
	})
	if boxed && f.LiveErr != "" && f.Live == nil && !f.Demo {
		line(func(y int) { c.Text(x, y, Truncate("live: "+f.LiveErr, r.W-2), Style{FG: th.InactiveFG}, 0) })
	}
	if !boxed || y >= r.Y+r.H-1 {
		return
	}
	// where this window's tokens went
	line(func(y int) {
		for i := r.X; i < r.X+r.W; i++ {
			c.Set(i, y, '─', Style{FG: th.DivLine})
		}
		c.Text(r.X+1, y, " by project ", lbl, 0)
	})
	for _, p := range topProjects(f.Snap, w, r.Y+r.H-y) {
		p := p
		line(func(y int) {
			c.Text(x, y, Truncate(p.name, 14), Style{FG: th.Title}, 0)
			bw := r.W - 26
			c.HBar(x+15, y, bw, p.share, th.Heat(p.share), th.DivLine)
			c.TextRight(right, y, fmtTok(float64(p.tokens)), val)
		})
	}
}

type projUse struct {
	name   string
	tokens int64
	share  float64
}

func topProjects(snap usage.Snapshot, w usage.Window, n int) []projUse {
	if n <= 0 {
		return nil
	}
	proj := map[string]string{}
	for _, s := range snap.Sessions {
		proj[s.ID] = s.ProjectName()
	}
	by := map[string]int64{}
	var total int64
	for _, e := range snap.Entries {
		if e.Time.Before(w.Start) || !e.Time.Before(w.End) {
			continue
		}
		by[proj[e.SessionID]] += e.Tokens.Total()
		total += e.Tokens.Total()
	}
	var out []projUse
	for k, v := range by {
		out = append(out, projUse{k, v, float64(v) / float64(max(total, 1))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].tokens > out[j].tokens })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ───────────────────────────── weekly ─────────────────────────────

func (f *frame) drawWeek(c *Canvas, r Rect) {
	th := f.th
	col := th.BoxWeek
	c.Box(r, 2, "weekly", col, th)
	in := r.Inner()
	x, right := in.X+1, in.X+in.W-1
	y := in.Y
	lbl := Style{FG: th.GraphText}
	grad := func(v float64) Color { return Gradient3(th.WeekStart, th.WeekMid, th.WeekEnd, v) }
	w := f.week
	bottom := in.Y + in.H

	meter := func(name string, v float64) {
		if y >= bottom {
			return
		}
		c.Text(x, y, name, lbl, 0)
		mw := in.W - 15
		c.Meter(x+8, y, mw, math.Max(v, 0)/100, th, grad)
		c.TextRight(right, y, fmtPct(v), Style{FG: grad(math.Max(v, 0) / 100), Bold: true})
		y++
	}
	meter("all", w.Utilization)
	if f.Live != nil && f.Live.SevenDayOpus != nil && in.H >= 10 {
		meter("opus", f.Live.SevenDayOpus.Utilization)
	}
	if f.Live != nil && f.Live.SevenDaySonnet != nil && in.H >= 10 {
		meter("sonnet", f.Live.SevenDaySonnet.Utilization)
	}
	if y < bottom {
		if w.Estimated {
			c.Text(x, y, "rolling 7 days", lbl, 0)
		} else {
			c.Text(x, y, "resets", lbl, 0)
			c.Text(x+7, y, w.End.In(f.Now.Location()).Format("Mon 15:04"), Style{FG: th.Title, Bold: true}, 0)
		}
		if !w.Estimated {
			c.TextRight(right, y, "in "+fmtDur(w.Remaining(f.Now)), Style{FG: th.MainFG})
		}
		y++
	}
	c.BorderTag(r.X+r.W-2, r.Y, col, th, Tag{Text: fmtTok(float64(w.Tokens.Total())) + " · " + fmtCost(w.Cost)})

	// per-day bars, newest at the bottom
	rows := bottom - y
	if rows <= 0 {
		return
	}
	if rows > 1 {
		for i := in.X; i < in.X+in.W; i++ {
			c.Set(i, y, '─', Style{FG: th.DivLine})
		}
		y++
		rows--
	}
	nd := min(rows, 7)
	days := usage.Days(f.Snap.Entries, f.Now, nd)
	mx := 0.0
	for _, d := range days {
		mx = math.Max(mx, float64(d.Tokens.Total()))
	}
	barW := in.W - 24
	for i, d := range days {
		today := i == len(days)-1
		ls := lbl
		if today {
			ls = Style{FG: th.Title, Bold: true}
		}
		c.Text(x, y, d.Date.Format("Mon 02"), ls, 0)
		v := 0.0
		if mx > 0 {
			v = float64(d.Tokens.Total()) / mx
		}
		c.HBar(x+7, y, barW, v, grad(v), th.DivLine)
		c.TextRight(right-7, y, fmtTok(float64(d.Tokens.Total())), Style{FG: th.MainFG})
		c.TextRight(right, y, fmtCost(d.Cost), lbl)
		y++
	}
}

// ───────────────────────────── now ─────────────────────────────

var spinner = []rune("⣾⣽⣻⢿⡿⣟⣯⣷")

func (f *frame) drawNow(c *Canvas, r Rect) {
	th := f.th
	col := th.BoxNow
	if r.H < 3 {
		return
	}
	c.Box(r, 3, "now", col, th)
	in := r.Inner()
	c.BorderTag(r.X+r.W-2, r.Y, col, th, Tag{Text: fmt.Sprintf("%d active", len(f.active))})
	x, right := in.X+1, in.X+in.W-1
	y := in.Y
	if len(f.active) == 0 {
		msg := "no active sessions"
		if len(f.Snap.Sessions) > 0 {
			s := f.Snap.Sessions[0]
			msg = "idle · last: " + s.ProjectName() + " " + fmtAgo(f.Now, s.End)
		}
		c.Text(x, y, Truncate(msg, in.W-2), Style{FG: th.GraphText}, 0)
		return
	}
	for _, s := range f.active {
		if y+1 >= in.Y+in.H {
			break
		}
		idle := f.Now.Sub(s.End)
		dot, dc := spinner[f.Frame%len(spinner)], th.Ok
		if idle > 2*time.Minute {
			dot, dc = '●', th.Warn
		}
		c.Set(x, y, dot, Style{FG: dc, Bold: true})
		cx := x + 2
		cx += c.Text(cx, y, s.ProjectName(), Style{FG: th.Title, Bold: true}, 0) + 1
		if s.Branch != "" {
			cx += c.Text(cx, y, "⎇ "+Truncate(s.Branch, 18), Style{FG: th.WeekMid}, 0) + 1
		}
		ago := fmtAgo(f.Now, s.End)
		mdl := usage.ShortModel(s.MainModel())
		if cx+Width(mdl)+Width(ago)+3 < right {
			c.Text(cx, y, mdl, Style{FG: th.GradStart}, 0)
		}
		c.TextRight(right, y, ago, Style{FG: th.GraphText})
		y++
		rows := []struct {
			icon string
			text string
			st   Style
		}{
			{"❯", s.LastPrompt, Style{FG: th.MainFG}},
			{"⚙", s.LastTool, Style{FG: th.GraphText}},
			{"▸", fmt.Sprintf("%s · %s tok · %s · %d prompts", fmtDur(s.End.Sub(s.Start)),
				fmtTok(float64(s.Tokens.Total())), fmtCost(s.Cost), s.Prompts), Style{FG: th.ProcMisc}},
		}
		for _, row := range rows {
			if y >= in.Y+in.H || row.text == "" {
				continue
			}
			c.Text(x+2, y, row.icon, Style{FG: th.HiFG}, 0)
			c.Text(x+4, y, Truncate(row.text, in.W-6), row.st, 0)
			y++
		}
		if y < in.Y+in.H && len(f.active) > 1 {
			y++ // spacer
		}
	}
}

// ───────────────────────────── history ─────────────────────────────

type column struct {
	name  string
	w     int
	right bool
	sort  SortMode
}

func (f *frame) drawHistory(c *Canvas, r Rect, wide bool) {
	th := f.th
	col := th.BoxHist
	if r.H < 4 {
		return
	}
	c.Box(r, 4, "history", col, th)
	in := r.Inner()
	c.BorderTag(r.X+r.W-2, r.Y, col, th,
		Tag{Text: "s", Hi: true}, Tag{Text: "ort: "}, Tag{Text: f.Sort.String(), FG: th.HiFG})
	if len(f.history) > 0 {
		c.BorderTag(r.X+r.W-2, r.Y+r.H-1, col, th, Tag{Text: fmt.Sprintf("%d/%d", f.Sel+1, len(f.history))})
	}
	keys := []Tag{{Text: "↑↓", Hi: true}, {Text: " select "}, {Text: "q", Hi: true}, {Text: " quit"}}
	if r.W > 50 {
		c.drawTag(r.X+2, r.Y+r.H-1, col, th, keys)
	}

	detailH := 0
	if in.H >= 12 {
		detailH = 5
	}
	listH := in.H - 1 - detailH

	var cols []column
	if wide {
		cols = []column{
			{"Started", 9, false, SortRecent},
			{"Dur", 6, true, SortDuration},
			{"Project", 13, false, -1},
			{"Task", 0, false, -1},
			{"Model", 10, false, -1},
			{"Tokens", 6, true, SortTokens},
			{"Cost", 6, true, SortCost},
			{"", 5, false, -1},
		}
	} else {
		cols = []column{
			{"When", 5, false, SortRecent},
			{"Project", 11, false, -1},
			{"Task", 0, false, -1},
			{"Tokens", 6, true, SortTokens},
		}
	}
	fixedW := func() int {
		n := 0
		for _, cl := range cols {
			n += cl.w + 1
		}
		return n
	}
	// give the task title room: drop the share bar, then the model column
	showBar, showModel := wide, wide
	if wide && in.W-1-fixedW() < 28 {
		cols, showBar = cols[:len(cols)-1], false
	}
	if wide && in.W-1-fixedW() < 24 {
		cols = append(cols[:4], cols[5:]...)
		showModel = false
	}
	flex := max(6, in.W-1-fixedW())
	for i := range cols {
		if cols[i].w == 0 {
			cols[i].w = flex
		}
	}

	// header
	x := in.X + 1
	for _, cl := range cols {
		st := Style{FG: th.Title, Bold: true}
		if cl.sort == f.Sort {
			st = Style{FG: th.HiFG, Bold: true}
		}
		if cl.right {
			c.TextRight(x+cl.w, in.Y, cl.name, st)
		} else {
			c.Text(x, in.Y, cl.name, st, cl.w)
		}
		x += cl.w + 1
	}

	// scroll so the selection stays visible
	top := 0
	if f.Sel >= listH {
		top = f.Sel - listH + 1
	}
	maxTok := 1.0
	for _, s := range f.history {
		maxTok = math.Max(maxTok, float64(s.Tokens.Total()))
	}
	for row := 0; row < listH && top+row < len(f.history); row++ {
		i := top + row
		s := f.history[i]
		y := in.Y + 1 + row
		sel := i == f.Sel
		base := Style{FG: th.MainFG}
		if sel {
			base = Style{FG: th.SelectedFG, BG: th.SelectedBG, Bold: true}
			c.Fill(in.X, y, in.W, 1, base)
		}
		live := f.Now.Sub(s.End) <= 2*time.Minute
		cells := []tc{}
		if wide {
			cells = append(cells,
				tc{fmtWhen(f.Now, s.Start), dim(base, th.GraphText, sel)},
				tc{fmtDur(s.End.Sub(s.Start)), dim(base, th.GraphText, sel)},
			)
		} else {
			cells = append(cells, tc{fmtWhenShort(f.Now, s.Start), dim(base, th.GraphText, sel)})
		}
		proj := s.ProjectName()
		if live {
			proj = "● " + proj
		}
		cells = append(cells,
			tc{proj, base.Fg(pick(sel, th.SelectedFG, th.Title))},
			tc{s.DisplayTitle(), base},
		)
		if showModel {
			cells = append(cells, tc{usage.ShortModel(s.MainModel()), dim(base, th.GradStart, sel)})
		}
		share := float64(s.Tokens.Total()) / maxTok
		cells = append(cells, tc{fmtTok(float64(s.Tokens.Total())), base.Fg(pick(sel, th.SelectedFG, th.Heat(share)))})
		if wide {
			cells = append(cells, tc{fmtCost(s.Cost), dim(base, th.ProcMisc, sel)})
		}
		x := in.X + 1
		for k, cl := range cols {
			if k >= len(cells) {
				if !showBar {
					break
				}
				// mini share bar (btop's per-process cpu graph)
				n := int(math.Round(share * float64(cl.w)))
				for j := 0; j < cl.w; j++ {
					st := Style{FG: th.MeterBG, BG: base.BG}
					if j < max(n, 1) {
						st.FG = th.Heat(float64(j) / float64(cl.w))
					}
					c.Set(x+j, y, '▮', st)
				}
				break
			}
			txt := Truncate(cells[k].text, cl.w)
			st := cells[k].st
			if cl.right {
				c.TextRight(x+cl.w, y, txt, st)
			} else {
				c.Text(x, y, txt, st, cl.w)
			}
			x += cl.w + 1
		}
	}

	if detailH > 0 && len(f.history) > 0 {
		f.drawDetail(c, Rect{in.X, in.Y + in.H - detailH, in.W, detailH}, f.history[f.Sel])
	}
}

func (f *frame) drawDetail(c *Canvas, r Rect, s *usage.Session) {
	th := f.th
	for i := r.X; i < r.X+r.W; i++ {
		c.Set(i, r.Y, '─', Style{FG: th.DivLine})
	}
	c.Text(r.X+1, r.Y, " "+Truncate(s.DisplayTitle(), r.W-4)+" ", Style{FG: th.Title, Bold: true}, 0)
	x := r.X + 1
	w := r.W - 2
	lbl := Style{FG: th.GraphText}
	val := Style{FG: th.MainFG}

	y := r.Y + 1
	where := s.Project
	if s.Branch != "" {
		where = "⎇ " + s.Branch + "  " + where
	}
	c.Text(x, y, Truncate(where, w), Style{FG: th.WeekMid}, 0)
	c.TextRight(x+w, y, fmtWhen(f.Now, s.Start)+" → "+fmtWhen(f.Now, s.End), lbl)
	y++
	if s.LastPrompt != "" {
		c.Text(x, y, "❯", Style{FG: th.HiFG}, 0)
		c.Text(x+2, y, Truncate(s.LastPrompt, w-2), val, 0)
	}
	y++
	t := s.Tokens
	parts := []string{
		"in " + fmtTok(float64(t.In)),
		"out " + fmtTok(float64(t.Out)),
		"cache r " + fmtTok(float64(t.CacheR)) + " w " + fmtTok(float64(t.CacheW)),
	}
	c.Text(x, y, Truncate(strings.Join(parts, " · "), w-16), val, 0)
	c.TextRight(x+w, y, fmtCost(s.Cost)+" api-eq", Style{FG: th.ProcMisc, Bold: true})
	y++
	// model mix
	type mm struct {
		m string
		n int64
	}
	var mix []mm
	for m, n := range s.Models {
		mix = append(mix, mm{m, n})
	}
	sort.Slice(mix, func(i, j int) bool { return mix[i].n > mix[j].n })
	var sb []string
	tot := float64(t.Total())
	for _, m := range mix {
		sb = append(sb, fmt.Sprintf("%s %.0f%%", usage.ShortModel(m.m), 100*float64(m.n)/math.Max(tot, 1)))
	}
	c.Text(x, y, Truncate(fmt.Sprintf("%d prompts · %d calls · ", s.Prompts, s.Calls)+strings.Join(sb, " · "), w), lbl, 0)
}

type tc struct {
	text string
	st   Style
}

func dim(base Style, c Color, sel bool) Style {
	if sel {
		return base
	}
	return base.Fg(c)
}

func pick(b bool, x, y Color) Color {
	if b {
		return x
	}
	return y
}


func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
