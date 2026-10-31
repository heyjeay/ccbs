package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is ccbs's small settings file (~/.config/ccbs/ccbs.conf), written
// in btop's key = "value" style.
type Config map[string]string

func ConfigPath() string {
	if d := os.Getenv("CCBS_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "ccbs.conf")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ccbs", "ccbs.conf")
}

func LoadConfig() Config {
	cfg := Config{}
	f, err := os.Open(ConfigPath())
	if err != nil {
		return cfg
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cfg[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return cfg
}

func (c Config) Save() error {
	p := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# ccbs settings — edited automatically when you press t / T\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %q\n", k, c[k])
	}
	return os.WriteFile(p, []byte(b.String()), 0o644)
}
