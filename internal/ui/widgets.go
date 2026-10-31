package ui

import (
	"math"
	"strings"
)

var superDigits = []rune("⁰¹²³⁴⁵⁶⁷⁸⁹")

type Rect struct{ X, Y, W, H int }

func (r Rect) Inner() Rect { return Rect{r.X + 1, r.Y + 1, r.W - 2, r.H - 2} }

// Box draws a btop box: rounded border, superscript hotkey + title on the
// top-left, and optional "┤ label ├" tags on the top-right / bottom.
func (c *Canvas) Box(r Rect, num int, title string, col Color, th Theme) {
	if r.W < 4 || r.H < 2 {
		return
	}
	ln := Style{FG: col}
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		c.Set(x, r.Y, '─', ln)
		c.Set(x, r.Y+r.H-1, '─', ln)
	}
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		c.Set(r.X, y, '│', ln)
		c.Set(r.X+r.W-1, y, '│', ln)
	}
	c.Set(r.X, r.Y, '╭', ln)
	c.Set(r.X+r.W-1, r.Y, '╮', ln)
	c.Set(r.X, r.Y+r.H-1, '╰', ln)
	c.Set(r.X+r.W-1, r.Y+r.H-1, '╯', ln)
	x := r.X + 2
	if num >= 0 && num <= 9 {
		c.Set(x, r.Y, superDigits[num], Style{FG: th.HiFG, Bold: true})
		x++
	}
	c.Text(x, r.Y, title, Style{FG: th.Title, Bold: true}, r.W-4)
}

// Tag is a fragment of a border label; Hi fragments use the hotkey color.
type Tag struct {
	Text string
	Hi   bool
	FG   Color
}

func tagWidth(parts []Tag) int {
	w := 2
	for _, p := range parts {
		w += Width(p.Text)
	}
	return w
}

// BorderTag draws "┤ parts ├" on row y ending at xEnd (exclusive).
// Returns its starting x.
func (c *Canvas) BorderTag(xEnd, y int, col Color, th Theme, parts ...Tag) int {
	x := xEnd - tagWidth(parts)
	c.drawTag(x, y, col, th, parts)
	return x
}

func (c *Canvas) drawTag(x, y int, col Color, th Theme, parts []Tag) int {
	c.Set(x, y, '┤', Style{FG: col})
	x++
	for _, p := range parts {
		st := Style{FG: th.Title}
		if p.FG.Set {
			st.FG = p.FG
		}
		if p.Hi {
			st = Style{FG: th.HiFG, Bold: true}
		}
		x += c.Text(x, y, p.Text, st, 0)
	}
	c.Set(x, y, '├', Style{FG: col})
	return x + 1
}

// CenterTag draws a tag centred on a border row.
func (c *Canvas) CenterTag(r Rect, y int, col Color, th Theme, parts ...Tag) {
	w := tagWidth(parts)
	c.drawTag(r.X+(r.W-w)/2, y, col, th, parts)
}

// Meter draws a btop "■■■■□□" bar of width w for v in [0,1].
func (c *Canvas) Meter(x, y, w int, v float64, th Theme, grad func(float64) Color) {
	filled := int(math.Round(clamp(v, 0, 1) * float64(w)))
	for i := 0; i < w; i++ {
		if i < filled {
			c.Set(x+i, y, '■', Style{FG: grad(float64(i) / math.Max(1, float64(w-1)))})
		} else {
			c.Set(x+i, y, '■', Style{FG: th.MeterBG})
		}
	}
}

// Braille dot masks per sub-column, filled bottom → top.
var (
	brailleLeft  = [4]rune{0x40, 0x04, 0x02, 0x01}
	brailleRight = [4]rune{0x80, 0x20, 0x10, 0x08}
)

// Graph draws a filled braille area chart: two samples per cell, four
// vertical dots per row, rows coloured by height (btop's graph style).
// vals are already normalised to [0,1]; NaN marks "future" (not drawn).
func (c *Canvas) Graph(r Rect, vals []float64, grad func(float64) Color, empty Color) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	levels := r.H * 4
	dots := func(v float64) int {
		if math.IsNaN(v) || v <= 0 {
			return 0
		}
		d := int(math.Round(clamp(v, 0, 1) * float64(levels)))
		if d == 0 {
			d = 1 // anything non-zero is visible
		}
		return d
	}
	for cx := 0; cx < r.W; cx++ {
		var l, rr float64 = math.NaN(), math.NaN()
		if i := cx * 2; i < len(vals) {
			l = vals[i]
		}
		if i := cx*2 + 1; i < len(vals) {
			rr = vals[i]
		}
		dl, dr := dots(l), dots(rr)
		future := math.IsNaN(l) && math.IsNaN(rr)
		for row := 0; row < r.H; row++ { // row 0 = bottom
			y := r.Y + r.H - 1 - row
			var mask rune
			for k := 0; k < 4; k++ {
				lvl := row*4 + k + 1
				if dl >= lvl {
					mask |= brailleLeft[k]
				}
				if dr >= lvl {
					mask |= brailleRight[k]
				}
			}
			if mask == 0 {
				if future && row == 0 {
					c.Set(r.X+cx, y, '⣀', Style{FG: empty})
				}
				continue
			}
			c.Set(r.X+cx, y, 0x2800+mask, Style{FG: grad(float64(row) / math.Max(1, float64(r.H-1)))})
		}
	}
}

// HBar draws a smooth horizontal bar using eighth blocks.
func (c *Canvas) HBar(x, y, w int, v float64, fg, bg Color) {
	eighths := []rune(" ▏▎▍▌▋▊▉█")
	total := clamp(v, 0, 1) * float64(w) * 8
	for i := 0; i < w; i++ {
		n := int(total) - i*8
		switch {
		case n >= 8:
			c.Set(x+i, y, '█', Style{FG: fg})
		case n > 0:
			c.Set(x+i, y, eighths[n], Style{FG: fg})
		default:
			c.Set(x+i, y, '·', Style{FG: bg})
		}
	}
}

// Spark draws a one-row sparkline.
func Spark(vals []float64) string {
	ticks := []rune("▁▂▃▄▅▆▇█")
	mx := 0.0
	for _, v := range vals {
		mx = math.Max(mx, v)
	}
	var b strings.Builder
	for _, v := range vals {
		if mx == 0 || v == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(ticks[int(clamp(v/mx, 0, 1)*float64(len(ticks)-1))])
	}
	return b.String()
}
