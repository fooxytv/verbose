package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPlanStart(t *testing.T) {
	now := time.Date(2026, 11, 28, 14, 5, 0, 0, time.UTC)
	dir := t.TempDir()
	repo := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := startOptions{dir: dir, agent: "claude", mode: "agent", in: "auto"}

	// Plain Claude, opening where the machine says.
	p, err := planStart(base, "linux", now)
	if err != nil || p.cli != "claude" || len(p.args) != 0 || p.in != "tmux" || p.cwd != dir {
		t.Errorf("plain: %+v, %v", p, err)
	}
	if p, _ := planStart(base, "windows", now); p.in != "wt" {
		t.Errorf("windows opens in %q", p.in)
	}

	// Remote Control always carries a name, ahead of the prompt.
	o := base
	o.remote, o.prompt, o.title = true, "fix the login bug", "login fix"
	p, err = planStart(o, "linux", now)
	if want := []string{"--remote-control", "login-fix", "fix the login bug"}; err != nil || !reflect.DeepEqual(p.args, want) {
		t.Errorf("remote: %q, %v; want %q", p.args, err, want)
	}

	// Claude makes its own worktree; verbose makes one for the others.
	o = base
	o.dir, o.worktree = repo, true
	p, err = planStart(o, "linux", now)
	if want := []string{"--worktree", "claude-1128-1405"}; err != nil || !reflect.DeepEqual(p.args, want) || p.root != "" {
		t.Errorf("claude worktree: %+v, %v", p, err)
	}
	o.agent, o.treeName = "codex", "spike"
	p, err = planStart(o, "linux", now)
	if err != nil || len(p.args) != 0 || p.root != repo || p.tree != "spike" || p.name != "app-spike" {
		t.Errorf("codex worktree: %+v, %v", p, err)
	}

	for name, bad := range map[string]startOptions{
		"unknown agent":        {dir: dir, agent: "gpt", mode: "agent", in: "auto"},
		"codex orchestrator":   {dir: dir, agent: "codex", mode: "orchestrator", in: "auto"},
		"codex remote control": {dir: dir, agent: "codex", mode: "agent", in: "auto", remote: true},
		"missing folder":       {dir: filepath.Join(dir, "nope"), agent: "claude", mode: "agent", in: "auto"},
		"worktree outside git": {dir: dir, agent: "claude", mode: "agent", in: "auto", worktree: true},
		"bad worktree name":    {dir: repo, agent: "claude", mode: "agent", in: "auto", worktree: true, treeName: "../x"},
		"unknown target":       {dir: dir, agent: "claude", mode: "agent", in: "kitty"},
	} {
		if _, err := planStart(bad, "linux", now); err == nil {
			t.Errorf("%s: planned", name)
		}
	}
	// Nothing was made while planning.
	if _, err := os.Stat(filepath.Join(dir, "nope")); err == nil {
		t.Error("planning made the missing folder")
	}
}

func TestStartName(t *testing.T) {
	now := time.Date(2026, 11, 28, 14, 5, 0, 0, time.UTC)
	for _, c := range []struct{ title, folder, tree, want string }{
		{"", "verbose", "", "verbose-1128-1405"},
		{"", "verbose", "claude-1128-1405", "verbose-claude-1128-1405"},
		{"fix: the login.bug", "x", "", "fix-the-login-bug"},
		{"", "my.site", "", "my-site-1128-1405"},
		{"...", "x", "", "agent-1128-1405"},
		{strings.Repeat("a", 90), "x", "", strings.Repeat("a", 60)},
	} {
		if got := startName(c.title, c.folder, c.tree, now); got != c.want {
			t.Errorf("startName(%q, %q, %q) = %q, want %q", c.title, c.folder, c.tree, got, c.want)
		}
	}
}

// The prompt reaches the CLI as an argument, never as shell text.
func TestTmuxStartArgs(t *testing.T) {
	got := tmuxStartArgs("app-1", "/w/app", "/bin/bash", "claude", []string{"fix it; rm -rf ~ $(whoami)"})
	want := []string{"new-session", "-d", "-s", "app-1", "-c", "/w/app", "--", "/bin/bash", "-lc", `exec "$0" "$@"`, "claude", "fix it; rm -rf ~ $(whoami)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := tmuxStartArgs("a", "/", "", "codex", nil); got[7] != "/bin/sh" {
		t.Errorf("no SHELL: %q", got)
	}
}

func TestRunStartRefusesBeforeStarting(t *testing.T) {
	var out, errb bytes.Buffer
	if code := RunStart([]string{"-dir", filepath.Join(t.TempDir(), "gone"), "hello"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "is not a folder") || out.Len() != 0 {
		t.Errorf("code %d, out %q, err %q", code, out.String(), errb.String())
	}
}
