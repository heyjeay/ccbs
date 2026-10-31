// sitegen builds the ccbs website: it copies site/ and renders the live
// dashboard preview in every bundled theme, using ccbs's own renderer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heyjeay/ccbm/internal/ui"
	"github.com/heyjeay/ccbm/internal/usage"
)

type swatch struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars"`
}

type frames struct {
	swatch
	Wide   string `json:"wide"`
	Narrow string `json:"narrow"`
}

func hex(c ui.Color, fallback string) string {
	if !c.Set {
		return fallback
	}
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func luminance(c ui.Color) float64 {
	return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
}

// vars turns a theme into CSS custom properties so the whole page can
// take on the theme, not just the preview.
func vars(t ui.Theme) map[string]string {
	bg := hex(t.MainBG, "#0c0c0c")
	light := t.MainBG.Set && luminance(t.MainBG) > 140
	if !t.MainBG.Set && luminance(t.MainFG) < 110 {
		bg, light = "#f4f4f4", true // theme meant for light terminals
	}
	scheme := "dark"
	if light {
		scheme = "light"
	}
	return map[string]string{
		"--bg": bg, "--fg": hex(t.MainFG, "#cccccc"), "--title": hex(t.Title, "#eeeeee"),
		"--hi": hex(t.HiFG, "#b54040"), "--sel-bg": hex(t.SelectedBG, "#6a2f2f"), "--sel-fg": hex(t.SelectedFG, "#eeeeee"),
		"--dim": hex(t.InactiveFG, "#404040"), "--text2": hex(t.GraphText, "#606060"), "--div": hex(t.DivLine, "#303030"),
		"--misc": hex(t.ProcMisc, "#0de756"),
		"--box1": hex(t.BoxFive, "#556d59"), "--box2": hex(t.BoxWeek, "#6c6c4b"), "--box3": hex(t.BoxNow, "#5c588d"), "--box4": hex(t.BoxHist, "#805252"),
		"--g1": hex(t.CPU.At(0), "#77ca9b"), "--g2": hex(t.CPU.At(0.5), "#cbc06c"), "--g3": hex(t.CPU.At(1), "#dc4c4c"),
		"--t1": hex(t.Temp.At(0), "#4897d4"), "--t2": hex(t.Temp.At(0.5), "#5474e8"), "--t3": hex(t.Temp.At(1), "#ff40b6"),
		"color-scheme": scheme,
	}
}

func slug(name string) string { return strings.ToLower(name) }

func main() {
	src := flag.String("src", "site", "static site sources")
	out := flag.String("out", "_site", "output directory")
	flag.Parse()

	must(os.RemoveAll(*out))
	must(copyTree(*src, *out))
	must(os.MkdirAll(filepath.Join(*out, "themes"), 0o755))

	// The page keeps the frame's clock ticking from the build time.
	now := time.Now().UTC().Truncate(time.Minute)
	snap, lim := usage.Demo(now)
	var index []swatch
	for _, t := range ui.Themes() {
		st := &ui.State{Now: now, Snap: snap, Live: lim, Demo: true, Frame: 3}
		f := frames{
			swatch: swatch{Name: t.Name, Vars: vars(t)},
			Wide:   ui.RenderCanvas(st, 118, 38, t).HTML(),
			Narrow: ui.RenderCanvas(st, 56, 58, t).HTML(),
		}
		index = append(index, f.swatch)
		writeJSON(filepath.Join(*out, "themes", slug(t.Name)+".json"), f)
	}
	writeJSON(filepath.Join(*out, "themes", "index.json"), index)
	fmt.Printf("site: %d themes → %s\n", len(index), *out)
}

func writeJSON(path string, v any) {
	b, err := json.Marshal(v)
	must(err)
	must(os.WriteFile(path, b, 0o644))
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}
