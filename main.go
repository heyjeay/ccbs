// ccbs — Claude Code better status: a btop-style terminal dashboard for your
// Claude Code usage.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/heyjeay/ccbs/internal/hook"
	"github.com/heyjeay/ccbs/internal/ui"
	"github.com/heyjeay/ccbs/internal/usage"
)

var version = "dev"

type demoSource struct{ snap usage.Snapshot }

func (d demoSource) Refresh() usage.Snapshot { return d.snap }

func main() {
	// subcommands: `ccbs statusline` (run by Claude Code) and `ccbs setup`
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "statusline":
			runStatusline()
			return
		case "setup":
			runSetup(len(os.Args) > 2 && (os.Args[2] == "--remove" || os.Args[2] == "remove"))
			return
		}
	}
	var (
		demo       = flag.Bool("demo", false, "show generated sample data instead of your own")
		offline    = flag.Bool("offline", false, "don't query Anthropic for live 5h/weekly limits; estimate locally")
		dirs       = flag.String("dir", "", "comma-separated transcript roots (default: ~/.claude/projects)")
		interval   = flag.Duration("interval", 2*time.Second, "refresh interval")
		themeName  = flag.String("theme", "", "color theme name or path to a btop .theme file (default: last used)")
		listThemes = flag.Bool("list-themes", false, "list available themes and exit")
		snapshot   = flag.Bool("snapshot", false, "render a single frame to stdout and exit")
		width      = flag.Int("width", 120, "frame width for --snapshot")
		height     = flag.Int("height", 40, "frame height for --snapshot")
		at         = flag.String("at", "", "with --demo --snapshot: pretend the clock reads this RFC3339 time")
		showVer    = flag.Bool("version", false, "print version")
		doctor     = flag.Bool("doctor", false, "check why live 5h/weekly limits do or don't work, then exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ccbs %s — Claude Code better status\n\nusage: ccbs [flags]\n\n", version)
		flag.PrintDefaults()
		fmt.Fprint(os.Stderr, "\ncommands:\n  ccbs setup            show live 5h/weekly limits via Claude Code's status line (recommended)\n  ccbs setup --remove   undo that\n")
		fmt.Fprint(os.Stderr, "\nkeys: ↑/↓ j/k select · s sort · t/T theme · +/- interval · r refresh · q quit\n")
	}
	flag.Parse()
	if *showVer {
		fmt.Println("ccbs", version)
		return
	}
	if *doctor {
		installed, cmd := hook.Status()
		usage.Diagnose(context.Background(), os.Stdout, installed, cmd)
		return
	}
	if *listThemes {
		for _, t := range ui.AllThemes() {
			fmt.Println(t.Name)
		}
		return
	}

	cfg := ui.LoadConfig()
	theme := ui.BtopDefault
	name := *themeName
	if name == "" {
		name = os.Getenv("CCBS_THEME")
	}
	if name == "" {
		name = cfg["theme"]
	}
	if name != "" {
		t, err := ui.FindTheme(name)
		if err != nil && *themeName != "" {
			fatal(err)
		}
		if err == nil {
			theme = t
		}
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
		st := &ui.State{Now: now, Snap: src.Refresh(), Interval: *interval, Demo: *demo, Live: demoLimits, Offline: *offline}
		if !*demo && !*offline {
			hooked, _ := hook.Status()
			st.HookInstalled = hooked
			if usage.NeedsAPI(now) {
				_, next, err := usage.PollLimits(context.Background())
				if err != nil {
					st.LiveErr = err.Error()
				}
				st.LiveNext = next
			}
			st.Live = usage.Extrapolate(usage.LatestReading(now), st.Snap.Entries, now)
		}
		fmt.Println(ui.Render(st, *width, *height, theme))
		return
	}

	err := ui.Run(ui.Options{
		Source:        src,
		Live:          !*demo && !*offline,
		Demo:          demoLimits,
		Interval:      *interval,
		Theme:         theme,
		HookInstalled: func() bool { ok, _ := hook.Status(); return ok }(),
		OnTheme: func(t ui.Theme) {
			cfg["theme"] = t.Name
			_ = cfg.Save()
		},
	})
	if err != nil {
		fatal(fmt.Errorf("needs an interactive terminal (try --snapshot): %w", err))
	}
}

// runStatusline is what Claude Code runs as its status line. It records the
// live limits Claude Code passes in, then prints a status line: the user's
// own one if they had one, otherwise a short summary. It must always exit 0
// quickly, or Claude Code shows a blank line.
func runStatusline() {
	input, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	l, _ := usage.RecordStatusline(input, time.Now())
	if out, ok := hook.Chained(input); ok {
		fmt.Println(out)
		return
	}
	if l == nil {
		l = usage.StatuslineLimits(time.Now())
	}
	fmt.Println(usage.StatuslineSummary(l, usage.ModelName(input), time.Now()))
}

func runSetup(remove bool) {
	if remove {
		msg, err := hook.Remove()
		if err != nil {
			fatal(err)
		}
		fmt.Println("ccbs:", msg)
		return
	}
	msg, err := hook.Install()
	if err != nil {
		fatal(err)
	}
	fmt.Println("ccbs: Claude Code's status line now runs ccbs (" + hook.SettingsPath() + ")")
	if msg != "" {
		fmt.Println("ccbs:", msg)
	}
	fmt.Println("ccbs: your live 5h and weekly limits appear after Claude Code's next reply.")
	fmt.Println("      Claude Code sends them to its status line itself: no extra requests, no rate limits.")
	fmt.Println("      Undo any time with: ccbs setup --remove")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ccbs:", err)
	os.Exit(1)
}
