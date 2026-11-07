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

func TestRenderLoadingOverlay(t *testing.T) {
	for _, sz := range [][2]int{{40, 16}, {56, 40}, {120, 40}} {
		st := &State{Now: time.Now(), Loading: true, LoadDone: 300 << 20, LoadTotal: 1200 << 20}
		out := ansi.ReplaceAllString(Render(st, sz[0], sz[1], BtopDefault), "")
		if !strings.Contains(out, "loading") || !strings.Contains(out, "300 MB of 1.2 GB") {
			t.Errorf("%dx%d: loading box missing", sz[0], sz[1])
		}
		for i, l := range strings.Split(out, "\n") {
			if w := runewidth.StringWidth(l); w != sz[0] {
				t.Fatalf("%dx%d line %d width %d", sz[0], sz[1], i, w)
			}
		}
	}
}

func TestRenderStaleAndBackoffLabels(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lim := &usage.Limits{
		FiveHour: &usage.LimitWindow{Utilization: 40, ResetsAt: now.Add(2 * time.Hour)},
		SevenDay: &usage.LimitWindow{Utilization: 61, ResetsAt: now.Add(50 * time.Hour)},
		Fetched:  now.Add(-20 * time.Minute),
	}
	st := &State{Now: now, Live: lim, LiveErr: "rate limited by Anthropic", LiveNext: now.Add(4 * time.Minute)}
	out := ansi.ReplaceAllString(Render(st, 120, 40, BtopDefault), "")
	if !strings.Contains(out, "live · read 20m ago") || !strings.Contains(out, "61%") {
		t.Errorf("stale live data should still show, labelled with its age")
	}
	// API failing and no reading yet: say why, and when it tries again
	st = &State{Now: now, LiveErr: "rate limited by Anthropic", LiveNext: now.Add(4 * time.Minute)}
	out = ansi.ReplaceAllString(Render(st, 120, 40, BtopDefault), "")
	if !strings.Contains(out, "no live %: rate limited") || !strings.Contains(out, "retry in 4m") {
		t.Errorf("missing error or retry countdown")
	}
}

func TestRenderWaitingAndApprox(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	st := &State{Now: now, HookInstalled: true}
	out := ansi.ReplaceAllString(Render(st, 120, 40, BtopDefault), "")
	if !strings.Contains(out, "no live % yet: asking Anthropic") {
		t.Errorf("waiting state wrong")
	}
	// a carried-forward estimate is marked with ~
	st.Live = &usage.Limits{Fetched: now.Add(-time.Hour),
		SevenDay: &usage.LimitWindow{Utilization: 64.4, ResetsAt: now.Add(48 * time.Hour), Approx: true}}
	out = ansi.ReplaceAllString(Render(st, 120, 40, BtopDefault), "")
	if !strings.Contains(out, "~64%") || !strings.Contains(out, "live · read 1h 00m ago") {
		t.Errorf("approx weekly not shown")
	}
}
