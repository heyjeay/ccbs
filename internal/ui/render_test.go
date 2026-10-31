package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/heyjeay/ccbs/internal/usage"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Every frame must be exactly w×h cells at any size, so the terminal never
// wraps or scrolls.
func TestRenderFillsExactly(t *testing.T) {
	now := time.Date(2026, 10, 31, 19, 5, 0, 0, time.UTC)
	snap, lim := usage.Demo(now)
	for _, sz := range [][2]int{{30, 10}, {40, 16}, {56, 50}, {80, 24}, {99, 40}, {100, 30}, {130, 42}, {220, 60}} {
		for sel := 0; sel < 3; sel++ {
			st := &State{Now: now, Snap: snap, Live: lim, Demo: true, Sel: sel, Sort: SortMode(sel)}
			out := ansi.ReplaceAllString(Render(st, sz[0], sz[1], BtopDefault), "")
			lines := strings.Split(out, "\n")
			if len(lines) != sz[1] {
				t.Fatalf("%dx%d: %d lines", sz[0], sz[1], len(lines))
			}
			for i, l := range lines {
				if w := runewidth.StringWidth(l); w != sz[0] {
					t.Fatalf("%dx%d line %d width %d: %q", sz[0], sz[1], i, w, l)
				}
			}
		}
	}
}

func TestRenderWithoutData(t *testing.T) {
	st := &State{Now: time.Now()}
	out := ansi.ReplaceAllString(Render(st, 120, 40, BtopDefault), "")
	for _, want := range []string{"5h window", "weekly", "now", "history", "no active sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty render missing %q", want)
		}
	}
}
