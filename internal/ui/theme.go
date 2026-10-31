package ui

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Grad is a btop start→mid→end gradient. A missing mid makes it two-point;
// a missing end makes it a solid start color.
type Grad struct {
	Start, Mid, End Color
}

func (g Grad) At(t float64) Color {
	switch {
	case !g.End.Set:
		return g.Start
	case !g.Mid.Set:
		return Lerp(g.Start, g.End, t)
	}
	return Gradient3(g.Start, g.Mid, g.End, t)
}

// Theme maps btop theme keys onto ccbs's boxes:
// cpu→5h window, mem→weekly, net→now, proc→history.
type Theme struct {
	Name string

	MainBG, MainFG, Title, HiFG    Color
	SelectedBG, SelectedFG         Color
	InactiveFG, GraphText, MeterBG Color
	DivLine, ProcMisc              Color

	BoxFive, BoxWeek, BoxNow, BoxHist Color

	CPU  Grad // 5h graph and usage meter (cpu_*)
	Temp Grad // weekly meters and day bars (temp_*)
	Proc Grad // history token heat (process_*)
}

// Heat colors a 0..1 utilization along the cpu gradient.
func (t Theme) Heat(v float64) Color { return t.CPU.At(v) }

func (t Theme) Ok() Color   { return t.CPU.At(0) }
func (t Theme) Warn() Color { return t.CPU.At(0.5) }
func (t Theme) Bad() Color  { return t.CPU.At(1) }

// defaultTheme is btop's built-in "Default" theme (src/btop_theme.cpp).
var defaultTheme = map[string]string{
	"main_bg": "#00", "main_fg": "#cc", "title": "#ee", "hi_fg": "#b54040",
	"selected_bg": "#6a2f2f", "selected_fg": "#ee", "inactive_fg": "#40",
	"graph_text": "#60", "meter_bg": "#40", "proc_misc": "#0de756",
	"cpu_box": "#556d59", "mem_box": "#6c6c4b", "net_box": "#5c588d", "proc_box": "#805252",
	"div_line":   "#30",
	"temp_start": "#4897d4", "temp_mid": "#5474e8", "temp_end": "#ff40b6",
	"cpu_start": "#77ca9b", "cpu_mid": "#cbc06c", "cpu_end": "#dc4c4c",
	"process_start": "#80d0a3", "process_mid": "#dcd179", "process_end": "#d45454",
}

//go:embed themes/*.theme
var themeFS embed.FS

var themeLine = regexp.MustCompile(`^\s*theme\[([a-z_]+)\]\s*=\s*"([^"]*)"`)

// ParseTheme reads btop theme syntax: theme[key]="#rrggbb" | "#gg" | "r g b" | "".
func ParseTheme(name string, src string) Theme {
	raw := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(src))
	for sc.Scan() {
		if m := themeLine.FindStringSubmatch(sc.Text()); m != nil {
			raw[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return buildTheme(name, raw)
}

func parseColor(v string) (Color, bool) {
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) != 2 && len(h) != 6 {
			return Color{}, false
		}
		if _, err := strconv.ParseUint(h, 16, 32); err != nil {
			return Color{}, false
		}
		return Hex(v), true
	}
	f := strings.Fields(v)
	if len(f) != 3 {
		return Color{}, false
	}
	var rgb [3]uint8
	for i, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 255 {
			return Color{}, false
		}
		rgb[i] = uint8(n)
	}
	return Color{rgb[0], rgb[1], rgb[2], true}, true
}

func buildTheme(name string, raw map[string]string) Theme {
	present := func(k string) bool { _, ok := raw[k]; return ok }
	// get applies btop's rules: missing → Default theme value; empty → unset.
	get := func(k string) Color {
		if v, ok := raw[k]; ok {
			if c, ok := parseColor(v); ok {
				return c
			}
			if v == "" {
				return Color{}
			}
		}
		c, _ := parseColor(defaultTheme[k])
		return c
	}
	grad := func(prefix string) Grad {
		// a mid/end left empty on purpose means "two-point" / "solid"
		return Grad{Start: get(prefix + "_start"), Mid: get(prefix + "_mid"), End: get(prefix + "_end")}
	}
	t := Theme{
		Name:       name,
		MainBG:     get("main_bg"),
		MainFG:     get("main_fg"),
		Title:      get("title"),
		HiFG:       get("hi_fg"),
		SelectedBG: get("selected_bg"),
		SelectedFG: get("selected_fg"),
		InactiveFG: get("inactive_fg"),
		DivLine:    get("div_line"),
		ProcMisc:   get("proc_misc"),
		BoxFive:    get("cpu_box"),
		BoxWeek:    get("mem_box"),
		BoxNow:     get("net_box"),
		BoxHist:    get("proc_box"),
		CPU:        grad("cpu"),
		Temp:       grad("temp"),
	}
	t.GraphText, t.MeterBG = t.InactiveFG, t.InactiveFG
	if present("graph_text") {
		t.GraphText = get("graph_text")
	}
	if present("meter_bg") {
		t.MeterBG = get("meter_bg")
	}
	t.Proc = t.CPU
	if present("process_start") {
		t.Proc = grad("process")
	}
	if name == "Default" {
		t.GraphText, t.MeterBG, t.Proc = get("graph_text"), get("meter_bg"), grad("process")
	}
	// a theme may leave main_fg-ish keys empty; never render invisible text
	if !t.MainFG.Set {
		t.MainFG = Hex("cc")
	}
	return t
}

// BtopDefault is btop's built-in theme.
var BtopDefault = buildTheme("Default", map[string]string{})

// Themes lists every bundled theme, Default first.
func Themes() []Theme {
	out := []Theme{BtopDefault}
	ents, _ := fs.ReadDir(themeFS, "themes")
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	for _, n := range names {
		b, err := themeFS.ReadFile("themes/" + n)
		if err == nil {
			out = append(out, ParseTheme(strings.TrimSuffix(n, ".theme"), string(b)))
		}
	}
	return out
}

// UserThemeDirs are searched for extra .theme files, so any btop theme you
// already have works in ccbs too.
func UserThemeDirs() []string {
	var dirs []string
	cfg, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	for _, base := range []string{cfg, filepath.Join(home, ".config")} {
		if base == "" {
			continue
		}
		dirs = append(dirs, filepath.Join(base, "ccbs", "themes"), filepath.Join(base, "btop", "themes"))
	}
	dirs = append(dirs, "/usr/local/share/btop/themes", "/usr/share/btop/themes")
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// AllThemes is the bundled set plus user themes (user files override).
func AllThemes() []Theme {
	all := Themes()
	idx := map[string]int{}
	for i, t := range all {
		idx[strings.ToLower(t.Name)] = i
	}
	for _, d := range UserThemeDirs() {
		files, _ := filepath.Glob(filepath.Join(d, "*.theme"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			t := ParseTheme(strings.TrimSuffix(filepath.Base(f), ".theme"), string(b))
			if i, ok := idx[strings.ToLower(t.Name)]; ok {
				all[i] = t
			} else {
				idx[strings.ToLower(t.Name)] = len(all)
				all = append(all, t)
			}
		}
	}
	return all
}

// FindTheme looks a theme up by name (case-insensitive) or by file path.
func FindTheme(name string) (Theme, error) {
	if strings.HasSuffix(name, ".theme") {
		if b, err := os.ReadFile(name); err == nil {
			return ParseTheme(strings.TrimSuffix(filepath.Base(name), ".theme"), string(b)), nil
		}
	}
	for _, t := range AllThemes() {
		if strings.EqualFold(t.Name, name) {
			return t, nil
		}
	}
	return Theme{}, fmt.Errorf("unknown theme %q (see ccbs --list-themes)", name)
}
