package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/heyjeay/ccbs/internal/usage"
)

// Source supplies data to the app.
type Source interface {
	Refresh() usage.Snapshot
}

// StoreSource tails transcripts on disk.
type StoreSource struct{ Store *usage.Store }

func (s StoreSource) Refresh() usage.Snapshot {
	_ = s.Store.Refresh()
	return s.Store.Snapshot()
}

type Options struct {
	Source   Source
	Live     bool // poll Anthropic for real 5h/weekly utilization
	Demo     *usage.Limits
	Interval time.Duration
	Theme    Theme
	// OnTheme is called after the user switches theme (to persist it).
	OnTheme func(Theme)
}

type liveResult struct {
	l   *usage.Limits
	err error
}

// Run takes over the terminal (alt screen, raw mode) until the user quits.
// Like btop, it owns its render loop rather than using a TUI framework.
func Run(opt Options) error {
	in, out := int(os.Stdin.Fd()), os.Stdout
	if !term.IsTerminal(in) {
		return io.ErrUnexpectedEOF
	}
	if opt.Interval == 0 {
		opt.Interval = 2 * time.Second
	}
	enableVT()
	old, err := term.MakeRaw(in)
	if err != nil {
		return err
	}
	// alt screen, hide cursor, disable line wrap
	io.WriteString(out, "\x1b[?1049h\x1b[?25l\x1b[?7l")
	defer func() {
		io.WriteString(out, "\x1b[?7h\x1b[?25h\x1b[?1049l")
		term.Restore(in, old)
	}()

	st := &State{Interval: opt.Interval, Now: time.Now()}
	if opt.Demo != nil {
		st.Demo, st.Live = true, opt.Demo
	}

	keys := make(chan string, 16)
	go readKeys(os.Stdin, keys)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)

	data := make(chan usage.Snapshot, 1)
	load := func() {
		go func() { data <- opt.Source.Refresh() }()
	}
	live := make(chan liveResult, 1)
	fetching := false
	fetch := func() {
		if !opt.Live || fetching {
			return
		}
		fetching = true
		go func() {
			l, err := usage.FetchLimits(context.Background())
			live <- liveResult{l, err}
		}()
	}

	st.Snap = opt.Source.Refresh()
	fetch()

	w, h, _ := term.GetSize(int(out.Fd()))
	draw := func() {
		// synchronized update avoids tearing on terminals that support it
		frame := "\x1b[?2026h\x1b[H" + Render(st, w, h, opt.Theme) + "\x1b[?2026l"
		io.WriteString(out, frame)
	}
	themes := AllThemes()
	themeIdx := 0
	for i, t := range themes {
		if strings.EqualFold(t.Name, opt.Theme.Name) {
			themeIdx = i
		}
	}
	draw()

	tick := time.NewTicker(opt.Interval)
	defer tick.Stop()
	resize := time.NewTicker(200 * time.Millisecond)
	defer resize.Stop()
	loading := false

	for {
		select {
		case <-sig:
			return nil
		case <-resize.C:
			if nw, nh, err := term.GetSize(int(out.Fd())); err == nil && (nw != w || nh != h) {
				w, h = nw, nh
				io.WriteString(out, "\x1b[2J")
				draw()
			}
		case <-tick.C:
			st.Now = time.Now()
			st.Frame++
			if !loading {
				loading = true
				load()
			}
			if st.Live == nil || time.Since(st.Live.Fetched) > 60*time.Second {
				if !st.Demo {
					fetch()
				}
			}
			draw()
		case snap := <-data:
			loading = false
			st.Snap = snap
			draw()
		case r := <-live:
			fetching = false
			if r.err != nil {
				st.LiveErr = r.err.Error()
				if st.Live != nil && time.Since(st.Live.Fetched) > 10*time.Minute {
					st.Live = nil // too stale to trust
				}
			} else {
				st.Live, st.LiveErr = r.l, ""
			}
			draw()
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			switch k {
			case "q", "ctrl+c":
				return nil
			case "up", "k":
				st.Sel--
			case "down", "j":
				st.Sel++
			case "pgup":
				st.Sel -= 10
			case "pgdown":
				st.Sel += 10
			case "home", "g":
				st.Sel = 0
			case "end", "G":
				st.Sel = 1 << 30
			case "s", "tab":
				st.Sort = (st.Sort + 1) % sortModes
				st.Sel = 0
			case "+", "=", "-":
				d := 500 * time.Millisecond
				if k == "-" {
					d = -d
				}
				opt.Interval = min(max(opt.Interval+d, 500*time.Millisecond), 10*time.Second)
				st.Interval = opt.Interval
				tick.Reset(opt.Interval)
			case "t", "T":
				if k == "t" {
					themeIdx = (themeIdx + 1) % len(themes)
				} else {
					themeIdx = (themeIdx - 1 + len(themes)) % len(themes)
				}
				opt.Theme = themes[themeIdx]
				st.Toast = fmt.Sprintf("theme %d/%d: %s", themeIdx+1, len(themes), opt.Theme.Name)
				st.ToastTill = time.Now().Add(3 * time.Second)
				if opt.OnTheme != nil {
					opt.OnTheme(opt.Theme)
				}
				io.WriteString(out, "\x1b[0m\x1b[2J")
			case "r":
				if !loading {
					loading = true
					load()
				}
				fetch()
			}
			draw()
		}
	}
}

// readKeys decodes raw terminal input into key names.
func readKeys(r io.Reader, out chan<- string) {
	defer close(out)
	br := bufio.NewReader(r)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return
		}
		switch {
		case b == 3:
			out <- "ctrl+c"
		case b == '\t':
			out <- "tab"
		case b == 0x1b:
			// CSI sequence (arrows, paging) or a lone escape
			if br.Buffered() == 0 {
				out <- "esc"
				continue
			}
			nb, _ := br.ReadByte()
			if nb != '[' && nb != 'O' {
				continue
			}
			seq := []byte{}
			for {
				c, err := br.ReadByte()
				if err != nil {
					return
				}
				seq = append(seq, c)
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			if k, ok := csiKeys[string(seq)]; ok {
				out <- k
			}
		case b < 0x80:
			out <- string(rune(b))
		}
	}
}

var csiKeys = map[string]string{
	"A": "up", "B": "down", "H": "home", "F": "end",
	"5~": "pgup", "6~": "pgdown", "1~": "home", "4~": "end",
}
