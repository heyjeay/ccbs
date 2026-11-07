// Package hook connects ccbs to Claude Code's status line, which is how ccbs
// receives the live 5-hour and weekly limits that Claude Code already knows.
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SettingsPath is Claude Code's user settings file.
func SettingsPath() string {
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		return filepath.Join(strings.TrimSpace(strings.Split(env, ",")[0]), "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// previousPath keeps the user's own status line config so ccbs can keep
// showing it, and put it back on `ccbs setup --remove`.
func previousPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	if d := os.Getenv("CCBS_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "statusline-previous.json")
	}
	return filepath.Join(base, "ccbs", "statusline-previous.json")
}

type statusLine struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func isCcbs(cmd string) bool {
	f := strings.Fields(cmd)
	return len(f) >= 2 && strings.Contains(strings.ToLower(f[0]), "ccbs") && f[len(f)-1] == "statusline"
}

// Command is what Claude Code should run. Forward slashes and no quotes, so
// it works whether Claude Code uses Git Bash or PowerShell on Windows; a
// path with spaces falls back to the PATH lookup the installer sets up.
func Command() string {
	exe, err := executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		exe = filepath.ToSlash(exe)
		if !strings.ContainsAny(exe, " \t\"'") {
			return exe + " statusline"
		}
	}
	return "ccbs statusline"
}

// Status reports whether Claude Code's status line runs ccbs.
func Status() (installed bool, current string) {
	b, err := os.ReadFile(SettingsPath())
	if err != nil {
		return false, ""
	}
	var s struct {
		StatusLine *statusLine `json:"statusLine"`
	}
	if json.Unmarshal(b, &s) != nil || s.StatusLine == nil {
		return false, ""
	}
	return isCcbs(s.StatusLine.Command), s.StatusLine.Command
}

// Install points Claude Code's status line at ccbs. An existing status
// line is saved and keeps being shown (ccbs runs it and prints its output).
func Install() (msg string, err error) {
	path := SettingsPath()
	orig, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	pairs, err := parseObject(orig)
	if err != nil {
		return "", fmt.Errorf("%s isn't valid JSON (%v); fix it or add the status line by hand", path, err)
	}
	if prev, ok := pairs.get("statusLine"); ok {
		var sl statusLine
		_ = json.Unmarshal(prev, &sl)
		if isCcbs(sl.Command) {
			msg = "already set up"
		} else {
			if err := writeFile(previousPath(), prev); err != nil {
				return "", err
			}
			msg = fmt.Sprintf("kept your status line (%s); ccbs will keep showing it", sl.Command)
		}
	}
	val, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Padding int    `json:"padding"`
	}{"command", Command(), 0})
	pairs.set("statusLine", val)
	out, err := pairs.marshal()
	if err != nil {
		return "", err
	}
	if len(orig) > 0 {
		if err := writeFile(path+".ccbs-backup", orig); err != nil {
			return "", err
		}
	}
	if err := writeFile(path, out); err != nil {
		return "", err
	}
	return msg, nil
}

// Remove takes ccbs out of the status line and restores the previous one.
func Remove() (string, error) {
	path := SettingsPath()
	orig, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	pairs, err := parseObject(orig)
	if err != nil {
		return "", err
	}
	cur, ok := pairs.get("statusLine")
	var sl statusLine
	_ = json.Unmarshal(cur, &sl)
	if !ok || !isCcbs(sl.Command) {
		return "ccbs isn't your status line; nothing to do", nil
	}
	msg := "removed the ccbs status line"
	if prev, err := os.ReadFile(previousPath()); err == nil && json.Valid(prev) {
		pairs.set("statusLine", prev)
		msg = "restored your previous status line"
	} else {
		pairs.del("statusLine")
	}
	out, err := pairs.marshal()
	if err != nil {
		return "", err
	}
	if err := writeFile(path, out); err != nil {
		return "", err
	}
	os.Remove(previousPath())
	return msg, nil
}

// Chained runs the user's previous status line command (if any) with the
// same input, and returns its output so ccbs can show it unchanged.
func Chained(input []byte) (out string, ok bool) {
	prev, err := os.ReadFile(previousPath())
	if err != nil {
		return "", false
	}
	var sl statusLine
	if json.Unmarshal(prev, &sl) != nil || sl.Command == "" || isCcbs(sl.Command) {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS != "windows":
		cmd = exec.CommandContext(ctx, "sh", "-c", sl.Command)
	case lookPath("bash"):
		cmd = exec.CommandContext(ctx, "bash", "-c", sl.Command)
	default:
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", sl.Command)
	}
	cmd.Stdin = bytes.NewReader(input)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\r\n"), true
}

// executable is swapped out in tests (where the binary is hook.test).
var executable = os.Executable

func lookPath(name string) bool { _, err := exec.LookPath(name); return err == nil }

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ── an order-preserving edit of a JSON object's top level ──

type kv struct {
	key string
	val json.RawMessage
}

type object []kv

func parseObject(b []byte) (object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var o object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, kv{key, v})
	}
	return o, nil
}

func (o object) get(k string) (json.RawMessage, bool) {
	for _, p := range o {
		if p.key == k {
			return p.val, true
		}
	}
	return nil, false
}

func (o *object) set(k string, v json.RawMessage) {
	for i := range *o {
		if (*o)[i].key == k {
			(*o)[i].val = v
			return
		}
	}
	*o = append(*o, kv{k, v})
}

func (o *object) del(k string) {
	for i := range *o {
		if (*o)[i].key == k {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}

func (o object) marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{")
	for i, p := range o {
		if i > 0 {
			b.WriteString(",")
		}
		k, _ := json.Marshal(p.key)
		b.WriteString("\n  ")
		b.Write(k)
		b.WriteString(": ")
		var v bytes.Buffer
		if err := json.Indent(&v, p.val, "  ", "  "); err != nil {
			return nil, err
		}
		b.Write(v.Bytes())
	}
	if len(o) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}
