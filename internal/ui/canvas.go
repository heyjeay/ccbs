// Package ui renders the ccbm dashboard onto a cell grid, btop style.
package ui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Color is a 24-bit color; the zero value means "terminal default".
type Color struct {
	R, G, B uint8
	Set     bool
}

func Hex(s string) Color {
	var r, g, b uint8
	s = strings.TrimPrefix(s, "#")
	if len(s) == 2 { // btop grayscale shorthand
		fmt.Sscanf(s, "%02x", &r)
		return Color{r, r, r, true}
	}
	fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b)
	return Color{r, g, b, true}
}

// Lerp blends a→b by t in [0,1].
func Lerp(a, b Color, t float64) Color {
	t = clamp(t, 0, 1)
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return Color{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), true}
}

// Gradient3 is btop's start→mid→end gradient.
func Gradient3(start, mid, end Color, t float64) Color {
	if t < 0.5 {
		return Lerp(start, mid, t*2)
	}
	return Lerp(mid, end, (t-0.5)*2)
}

type Style struct {
	FG, BG Color
	Bold   bool
}

func (s Style) Fg(c Color) Style { s.FG = c; return s }
func (s Style) B() Style         { s.Bold = true; return s }

type cell struct {
	r    rune
	st   Style
	cont bool // right half of a wide rune
}

type Canvas struct {
	W, H  int
	cells []cell
	bg    Color
}

func NewCanvas(w, h int, bg Color) *Canvas {
	c := &Canvas{W: w, H: h, cells: make([]cell, w*h), bg: bg}
	for i := range c.cells {
		c.cells[i] = cell{r: ' ', st: Style{BG: bg}}
	}
	return c
}

func (c *Canvas) Set(x, y int, r rune, st Style) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	if !st.BG.Set {
		st.BG = c.bg
	}
	c.cells[y*c.W+x] = cell{r: r, st: st}
}

// Text writes s at (x,y), clipped to maxW columns (<=0: to edge).
// Returns columns written.
func (c *Canvas) Text(x, y int, s string, st Style, maxW int) int {
	if maxW <= 0 || x+maxW > c.W {
		maxW = c.W - x
	}
	col := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}
		if col+w > maxW {
			break
		}
		c.Set(x+col, y, r, st)
		if w == 2 && x+col+1 < c.W && y < c.H && y >= 0 {
			if !st.BG.Set {
				st.BG = c.bg
			}
			c.cells[y*c.W+x+col+1] = cell{cont: true, st: st}
		}
		col += w
	}
	return col
}

// TextRight right-aligns s so it ends at column xEnd (exclusive).
func (c *Canvas) TextRight(xEnd, y int, s string, st Style) int {
	w := runewidth.StringWidth(s)
	return c.Text(xEnd-w, y, s, st, w)
}

// Free reports whether a cell holds only a blank.
func (c *Canvas) Free(x, y int) bool {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return false
	}
	ce := c.cells[y*c.W+x]
	return !ce.cont && ce.r == ' '
}

func (c *Canvas) Fill(x, y, w, h int, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.Set(i, j, ' ', st)
		}
	}
}

// String renders the canvas with truecolor escapes.
func (c *Canvas) String() string {
	var b strings.Builder
	b.Grow(c.W * c.H * 4)
	for y := 0; y < c.H; y++ {
		var cur Style
		first := true
		for x := 0; x < c.W; x++ {
			ce := c.cells[y*c.W+x]
			if ce.cont {
				continue
			}
			if first || ce.st != cur {
				b.WriteString(sgr(ce.st))
				cur = ce.st
				first = false
			}
			b.WriteRune(ce.r)
		}
		b.WriteString("\x1b[0m")
		if y < c.H-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func sgr(s Style) string {
	var b strings.Builder
	b.WriteString("\x1b[0")
	if s.Bold {
		b.WriteString(";1")
	}
	if s.FG.Set {
		fmt.Fprintf(&b, ";38;2;%d;%d;%d", s.FG.R, s.FG.G, s.FG.B)
	}
	if s.BG.Set {
		fmt.Fprintf(&b, ";48;2;%d;%d;%d", s.BG.R, s.BG.G, s.BG.B)
	}
	b.WriteByte('m')
	return b.String()
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Truncate shortens s to w columns with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

func Width(s string) int { return runewidth.StringWidth(s) }
