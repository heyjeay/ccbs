package ui

import (
	"testing"
	"time"

	"github.com/heyjeay/ccbs/internal/usage"
)

func TestAllBundledThemesParse(t *testing.T) {
	ts := Themes()
	if len(ts) < 42 {
		t.Fatalf("want all btop themes + Default, got %d", len(ts))
	}
	now := time.Date(2026, 10, 31, 19, 5, 0, 0, time.UTC)
	snap, lim := usage.Demo(now)
	for _, th := range ts {
		for name, c := range map[string]Color{
			"main_fg": th.MainFG, "title": th.Title, "hi_fg": th.HiFG,
			"cpu_box": th.BoxFive, "proc_box": th.BoxHist, "cpu_start": th.CPU.Start,
		} {
			if !c.Set {
				t.Errorf("%s: %s unset", th.Name, name)
			}
		}
		st := &State{Now: now, Snap: snap, Live: lim, Demo: true}
		if out := Render(st, 120, 40, th); len(out) == 0 {
			t.Errorf("%s: empty render", th.Name)
		}
	}
}

func TestParseThemeRules(t *testing.T) {
	th := ParseTheme("x", `
# comment
theme[main_bg]=""
theme[main_fg]="#D8DEE9"
theme[title]="200 100 50"
theme[cpu_start]="#000000"
theme[cpu_mid]=""
theme[cpu_end]="#ffffff"
`)
	if th.MainBG.Set {
		t.Error("empty main_bg should mean terminal default")
	}
	if th.MainFG != Hex("#D8DEE9") {
		t.Errorf("main_fg = %+v", th.MainFG)
	}
	if th.Title != (Color{200, 100, 50, true}) {
		t.Errorf("decimal rgb title = %+v", th.Title)
	}
	if th.CPU.Mid.Set {
		t.Error("empty cpu_mid should make a two-point gradient")
	}
	if mid := th.CPU.At(0.5); mid.R < 120 || mid.R > 135 {
		t.Errorf("two-point midpoint = %+v", mid)
	}
	// missing keys fall back to btop's Default theme; graph_text → inactive_fg
	if th.HiFG != BtopDefault.HiFG || th.GraphText != th.InactiveFG {
		t.Error("fallbacks not applied")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("CCBS_CONFIG_DIR", t.TempDir())
	c := Config{"theme": "nord"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if got := LoadConfig()["theme"]; got != "nord" {
		t.Errorf("theme = %q", got)
	}
}
