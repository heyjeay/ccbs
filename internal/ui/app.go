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

func (s StoreSource) Progress() (done, total int64) { return s.Store.Progress() }

// progresser is implemented by sources that can report loading progress.
type progresser interface {
	Progress() (done, total int64)
}

type Options struct {
	Source   Source
	Live     bool // poll Anthropic for real 5h/weekly utilization
	Demo     *usage.Limits
	Interval time.Duration
	Theme    Theme
	// OnTheme is called after the user switches theme (to persist it).
	OnTheme func(Theme)
	// HookInstalled: Claude Code's status line runs ccbs (see `ccbs setup`).
	HookInstalled bool
}

type liveResult struct {
	l    *usage.Limits
	next time.Time
	err  error
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

	st := &State{Interval: opt.Interval, Now: time.Now(), Offline: !opt.Live && opt.Demo == nil, HookInstalled: opt.HookInstalled}
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
	var nextFetch time.Time // when the shared cache says it's worth asking again
	fetch := func() {
		if !opt.Live || fetching || time.Now().Before(nextFetch) {
			return
		}
		fetching = true
		go func() {
			l, next, err := usage.PollLimits(context.Background())
			live <- liveResult{l, next, err}
		}()
	}

	// Draw straight away and read history in the background: a big
	// ~/.claude/projects can take a moment, and a blank screen looks hung.
	st.Loading = true
	// Live limits: the newest real reading from either source (Claude Code's
	// status line, or the usage API), carried forward from local usage since.
	// The API is only asked while the status line is quiet, which is always
	// the case for the Claude desktop app (it doesn't run status lines).
	refreshLive := func(mayFetch bool) {
		if !opt.Live {
			return
		}
		now := time.Now()
		st.Live = usage.Extrapolate(usage.LatestReading(now), st.Snap.Entries, now)
		if !usage.NeedsAPI(now) {
			st.LiveErr = ""
		} else if mayFetch {
			fetch() // no-op until the shared cache allows another request
		}
	}
	refreshLive(true)

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
	loading := true
	load()
	draw()

	tick := time.NewTicker(opt.Interval)
	defer tick.Stop()
	resize := time.NewTicker(200 * time.Millisecond)
	defer resize.Stop()
	progress := time.NewTicker(100 * time.Millisecond)
	defer progress.Stop()

	for {
		select {
		case <-sig:
			return nil
		case <-progress.C:
			if st.Loading {
				if p, ok := opt.Source.(progresser); ok {
					st.LoadDone, st.LoadTotal = p.Progress()
				}
				st.Now = time.Now()
				st.Frame++
				draw()
			}
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
			if !st.Demo {
				refreshLive(true)
			}
			draw()
		case snap := <-data:
			loading, st.Loading = false, false
			st.Snap = snap
			if !st.Demo {
				refreshLive(false) // new local usage moves the estimate
			}
			draw()
		case r := <-live:
			fetching = false
			nextFetch, st.LiveNext = r.next, r.next
			st.LiveErr = ""
			if r.err != nil {
				st.LiveErr = r.err.Error()
			}
			refreshLive(false) // pick up what that request stored
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
				nextFetch = time.Time{} // re-check the shared cache now (its back-off still applies)
				refreshLive(true)
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
