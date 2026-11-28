package ui

// verbose start: the launch panel (n) without the panel. It starts an agent in
// a window of its own and returns, so something with no terminal of its own —
// a script, or pidash's reporter answering a request from orchestrator.home —
// can start one the same way verbose does.
//
// launchInTerminal cannot be reused as it is: it picks a terminal from the one
// verbose is running in (TMUX, WT_SESSION, ...), and a background service has
// none of those, so it would fall through to running the agent in place. Here
// the target is chosen from the machine instead: Windows Terminal on Windows,
// Terminal.app on a Mac, and a detached tmux session anywhere else, which is
// the only thing that works on a machine with no desktop.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// startOptions are verbose start's flags.
type startOptions struct {
	dir      string
	agent    string
	mode     string
	prompt   string
	worktree bool
	treeName string
	remote   bool
	title    string
	in       string // "auto", "wt", "tmux" or "terminal"
}

// startPlan is what will run, worked out before anything is touched so that
// it can be asserted.
type startPlan struct {
	cli  string
	args []string
	cwd  string
	tree string // the worktree's name, "" for none
	root string // its repository, when verbose has to make it
	in   string
	name string // a name for the session: the tmux session, the Remote Control title
}

// RunStart is `verbose start`. It prints one line saying what was started,
// and how to reach it when that is not obvious, and returns the exit code.
func RunStart(argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o startOptions
	fs.StringVar(&o.dir, "dir", "", "folder to start in (must exist; default: the current folder)")
	fs.StringVar(&o.agent, "agent", "claude", "agent to start: "+strings.Join(launchAgents, " | "))
	fs.StringVar(&o.mode, "mode", "agent", "agent | orchestrator (orchestrator is Claude Code only)")
	fs.StringVar(&o.prompt, "prompt", "", "what it should do (optional; the remaining arguments are used when this is empty)")
	fs.BoolVar(&o.worktree, "worktree", false, "start it in a new git worktree of its own")
	fs.StringVar(&o.treeName, "worktree-name", "", "the worktree's name (default: <agent>-MMDD-HHMM)")
	fs.BoolVar(&o.remote, "remote-control", false, "Claude Code only: turn on Remote Control, so claude.ai and the app can reach it")
	fs.StringVar(&o.title, "title", "", "a name for the session (default: the folder and the time)")
	fs.StringVar(&o.in, "in", "auto", "where it opens: auto | wt | tmux | terminal")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: verbose start [flags] [prompt]")
		fmt.Fprintln(stderr, "Starts an agent in a window of its own and returns.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if o.prompt == "" {
		o.prompt = strings.Join(fs.Args(), " ")
	}
	if o.dir == "" {
		o.dir, _ = os.Getwd()
	}

	p, err := planStart(o, runtime.GOOS, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "verbose start: "+err.Error())
		return 2
	}
	if p.root != "" {
		path, err := addWorktree(p.root, p.tree)
		if err != nil {
			fmt.Fprintln(stderr, "verbose start: could not make the worktree: "+err.Error())
			return 1
		}
		p.cwd = path
	}
	if o.mode == "orchestrator" {
		brief, err := writeOrchestratorBrief()
		if err != nil {
			fmt.Fprintln(stderr, "verbose start: could not write the orchestrator brief: "+err.Error())
			return 1
		}
		p.cli, p.args = startCommand(o, brief, p.tree, p.name)
	}

	forgetParentSession()
	where, err := runStart(p)
	if err != nil {
		fmt.Fprintln(stderr, "verbose start: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "started %s in %s%s\n", o.agent, p.cwd, where)
	return 0
}

// parentSessionVars are what Claude Code sets on everything it runs. Run from
// inside a session — an agent calling verbose start — they reach the new one
// too, and it takes itself for that session's child: it says "Transcript
// saving is off - inherited CLAUDE_CODE_CHILD_SESSION marker" and writes no
// transcript and no session record, so nothing can see it. Only these
// markers go; a CLAUDE_CODE_ setting you chose yourself is kept.
var parentSessionVars = []string{
	"AI_AGENT", "CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT",
	"CLAUDE_CODE_BRIDGE_SESSION_ID", "CLAUDE_CODE_CHILD_NOTES_DIR", "CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_SSE_PORT",
}

// forgetParentSession clears them from verbose's own environment, which
// every way of starting the agent (wt, osascript, tmux) passes on.
func forgetParentSession() {
	for _, k := range parentSessionVars {
		os.Unsetenv(k)
	}
}

// startCommand is launchArgs plus Remote Control. Only Claude Code is handed
// the worktree's name: it makes its own, where verbose makes one for every
// other agent.
func startCommand(o startOptions, brief, tree, name string) (string, []string) {
	if o.agent != "claude" {
		tree = ""
	}
	cli, args := launchArgs(o.agent, o.mode, o.prompt, brief, tree)
	if o.remote {
		// Always with a name: --remote-control takes an optional value, so
		// without one the prompt after it would be taken as the name.
		args = append([]string{"--remote-control", name}, args...)
	}
	return cli, args
}

// planStart checks the options and works out the command, without starting
// anything or touching the disk beyond looking.
func planStart(o startOptions, goos string, now time.Time) (startPlan, error) {
	known := false
	for _, a := range launchAgents {
		known = known || a == o.agent
	}
	if !known {
		return startPlan{}, fmt.Errorf("unknown agent %q: one of %s", o.agent, strings.Join(launchAgents, ", "))
	}
	switch o.mode {
	case "agent":
	case "orchestrator":
		if o.agent != "claude" {
			return startPlan{}, errors.New("only Claude Code can run as an orchestrator")
		}
	default:
		return startPlan{}, fmt.Errorf("unknown mode %q: agent or orchestrator", o.mode)
	}
	if o.remote && o.agent != "claude" {
		return startPlan{}, errors.New("only Claude Code has Remote Control")
	}
	dir, err := filepath.Abs(o.dir)
	if err != nil {
		return startPlan{}, err
	}
	// Never made here: a folder is created from the panel, where you can see
	// the path, not by something asking from across the network.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return startPlan{}, fmt.Errorf("%s is not a folder", dir)
	}

	p := startPlan{cwd: dir}
	switch o.in {
	case "auto":
		p.in = startTarget(goos)
	case "wt", "tmux", "terminal":
		p.in = o.in
	default:
		return startPlan{}, fmt.Errorf("unknown -in %q: auto, wt, tmux or terminal", o.in)
	}

	if o.worktree {
		root := gitRoot(dir)
		if root == "" {
			return startPlan{}, fmt.Errorf("%s is not in a git repository, so it cannot have a worktree", dir)
		}
		if p.tree, err = worktreeName(o.treeName, o.agent, now); err != nil {
			return startPlan{}, err
		}
		if o.agent != "claude" {
			p.root = root
		}
	}

	p.name = startName(o.title, filepath.Base(dir), p.tree, now)
	// An orchestrator's brief path is filled in once the brief is written;
	// planning stays free of writes so it can be tested.
	p.cli, p.args = startCommand(o, "", p.tree, p.name)
	return p, nil
}

// startTarget is where a new session opens when nothing says otherwise.
func startTarget(goos string) string {
	switch goos {
	case "windows":
		return "wt"
	case "darwin":
		return "terminal"
	}
	return "tmux"
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// startName names the session: the title given, or the folder (and worktree)
// and the time. It becomes a tmux session name, which may not hold "." or ":",
// so it is kept to letters, digits, "_" and "-".
func startName(title, folder, tree string, now time.Time) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = folder
		if tree != "" {
			name += "-" + tree
		} else {
			name += "-" + now.Format("0102-1504")
		}
	}
	name = strings.Trim(unsafeName.ReplaceAllString(name, "-"), "-")
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	if name == "" {
		name = "agent-" + now.Format("0102-1504")
	}
	return name
}

// runStart opens the session where the plan says and returns how to reach it.
func runStart(p startPlan) (string, error) {
	switch p.in {
	case "wt":
		wt, err := exec.LookPath("wt")
		if err != nil {
			return "", errors.New("Windows Terminal (wt) is not on PATH")
		}
		c := exec.Command(wt, wtArgs(p.cli, p.args, p.cwd)...)
		if err := c.Start(); err != nil {
			return "", err
		}
		go c.Wait() // wt hands the tab to a window and exits
		return " (Windows Terminal)", nil
	case "terminal":
		if msg, ok := launchInTerminalApp(p.cli, p.args, p.cwd)().(resumeDoneMsg); ok && msg.err != nil {
			return "", msg.err
		}
		return " (Terminal)", nil
	}
	return startInTmux(p)
}

// startInTmux starts the agent in a detached tmux session, then checks it is
// still there a moment later: a CLI that is missing or refuses its arguments
// exits at once, and the session goes with it, which would otherwise look
// exactly like success.
func startInTmux(p startPlan) (string, error) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return "", errors.New("tmux is not installed, and there is nowhere else to start it")
	}
	name := p.name
	if exec.Command(tmux, "has-session", "-t", "="+name).Run() == nil {
		name += "-" + time.Now().Format("150405")
	}
	out, err := exec.Command(tmux, tmuxStartArgs(name, p.cwd, os.Getenv("SHELL"), p.cli, p.args)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
	}
	time.Sleep(1500 * time.Millisecond)
	if exec.Command(tmux, "has-session", "-t", "="+name).Run() != nil {
		return "", fmt.Errorf("%s exited as soon as it started; try it by hand in %s", p.cli, p.cwd)
	}
	return fmt.Sprintf(" (tmux session %s: tmux attach -t %s)", name, name), nil
}

// tmuxStartArgs runs the CLI through a login shell, so it has the PATH and
// environment a terminal would give it rather than a service's. The arguments
// go to the shell as $0 and $@, never into the script, so a prompt cannot be
// read as shell.
func tmuxStartArgs(name, cwd, shell, cli string, args []string) []string {
	if shell == "" {
		shell = "/bin/sh"
	}
	out := []string{"new-session", "-d", "-s", name, "-c", cwd, "--", shell, "-lc", `exec "$0" "$@"`, cli}
	return append(out, args...)
}
