package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T, settings string) string {
	t.Helper()
	claude, cfg := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	old := executable
	executable = func() (string, error) { return "/opt/ccbs/ccbs", nil }
	t.Cleanup(func() { executable = old })
	t.Setenv("CCBS_CONFIG_DIR", cfg)
	p := filepath.Join(claude, "settings.json")
	if settings != "" {
		os.WriteFile(p, []byte(settings), 0o644)
	}
	return p
}

func TestInstallKeepsSettingsAndOrder(t *testing.T) {
	p := setup(t, `{
  "model": "opus",
  "permissions": {"allow": ["Bash(go test:*)"]},
  "env": {"FOO": "1"}
}`)
	if _, err := Install(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !(strings.Index(s, `"model"`) < strings.Index(s, `"permissions"`) && strings.Index(s, `"permissions"`) < strings.Index(s, `"env"`)) {
		t.Errorf("key order changed:\n%s", s)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v["model"] != "opus" || v["env"].(map[string]any)["FOO"] != "1" {
		t.Errorf("other settings lost: %v", v)
	}
	if ok, cmd := Status(); !ok || !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("status = %v %q", ok, cmd)
	}
	if _, err := os.Stat(p + ".ccbs-backup"); err != nil {
		t.Error("no backup written")
	}
	// running it twice is harmless
	if msg, err := Install(); err != nil || msg != "already set up" {
		t.Errorf("second install: %q %v", msg, err)
	}
}

func TestInstallChainsAndRemoveRestoresExistingStatusLine(t *testing.T) {
	p := setup(t, `{"statusLine": {"type": "command", "command": "echo mine", "padding": 1}}`)
	msg, err := Install()
	if err != nil || !strings.Contains(msg, "echo mine") {
		t.Fatalf("install: %q %v", msg, err)
	}
	out, ok := Chained([]byte(`{}`))
	if !ok || out != "mine" {
		t.Errorf("chained output = %q %v", out, ok)
	}
	if _, err := Remove(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var v struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	json.Unmarshal(b, &v)
	if v.StatusLine["command"] != "echo mine" || v.StatusLine["padding"] != float64(1) {
		t.Errorf("previous status line not restored: %s", b)
	}
	if ok, _ := Status(); ok {
		t.Error("still installed after remove")
	}
}

func TestInstallWithoutSettingsFile(t *testing.T) {
	p := setup(t, "")
	if _, err := Install(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Status(); !ok {
		t.Fatalf("not installed; file:\n%s", func() string { b, _ := os.ReadFile(p); return string(b) }())
	}
	if _, ok := Chained([]byte(`{}`)); ok {
		t.Error("nothing to chain when there was no status line")
	}
}

func TestRefusesBrokenSettings(t *testing.T) {
	p := setup(t, `{"model": "opus",`)
	if _, err := Install(); err == nil {
		t.Fatal("should refuse to rewrite invalid JSON")
	}
	if b, _ := os.ReadFile(p); string(b) != `{"model": "opus",` {
		t.Error("broken settings file was modified")
	}
}
