// ccbm — a btop-style terminal dashboard for Claude Code usage.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/heyjeay/ccbm/internal/ui"
	"github.com/heyjeay/ccbm/internal/usage"
)

var version = "dev"

type demoSource struct{ snap usage.Snapshot }

func (d demoSource) Refresh() usage.Snapshot { return d.snap }

func main() {
	var (
		demo     = flag.Bool("demo", false, "show generated sample data instead of your own")
		offline  = flag.Bool("offline", false, "don't query Anthropic for live 5h/weekly limits; estimate locally")
		dirs     = flag.String("dir", "", "comma-separated transcript roots (default: ~/.claude/projects)")
		interval = flag.Duration("interval", 2*time.Second, "refresh interval")
		snapshot = flag.Bool("snapshot", false, "render a single frame to stdout and exit")
		width    = flag.Int("width", 120, "frame width for --snapshot")
		height   = flag.Int("height", 40, "frame height for --snapshot")
		at       = flag.String("at", "", "with --demo --snapshot: pretend the clock reads this RFC3339 time")
		showVer  = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ccbm %s — Claude Code usage, btop style\n\nusage: ccbm [flags]\n\n", version)
		flag.PrintDefaults()
		fmt.Fprint(os.Stderr, "\nkeys: ↑/↓ j/k select · s sort · +/- interval · r refresh · q quit\n")
	}
	flag.Parse()
	if *showVer {
		fmt.Println("ccbm", version)
		return
	}

	now := time.Now()
	if *at != "" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fatal(err)
		}
		now = t
	}

	var src ui.Source
	var demoLimits *usage.Limits
	if *demo {
		snap, lim := usage.Demo(now)
		src, demoLimits = demoSource{snap}, lim
	} else {
		roots := usage.DefaultDirs()
		if *dirs != "" {
			roots = strings.Split(*dirs, ",")
		}
		if len(roots) == 0 {
			fatal(fmt.Errorf("no Claude Code transcripts found (looked in ~/.claude/projects); try --demo"))
		}
		src = ui.StoreSource{Store: usage.NewStore(roots)}
	}

	if *snapshot {
		st := &ui.State{Now: now, Snap: src.Refresh(), Interval: *interval, Demo: *demo, Live: demoLimits}
		if !*demo && !*offline {
			l, err := usage.FetchLimits(context.Background())
			if err != nil {
				st.LiveErr = err.Error()
			}
			st.Live = l
		}
		fmt.Println(ui.Render(st, *width, *height, ui.BtopDefault))
		return
	}

	err := ui.Run(ui.Options{
		Source:   src,
		Live:     !*demo && !*offline,
		Demo:     demoLimits,
		Interval: *interval,
		Theme:    ui.BtopDefault,
	})
	if err != nil {
		fatal(fmt.Errorf("needs an interactive terminal (try --snapshot): %w", err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ccbm:", err)
	os.Exit(1)
}
