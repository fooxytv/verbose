# verbose

A terminal UI for browsing and analyzing [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
and [OpenCode](https://opencode.ai) session transcripts.

Verbose reads the session data both agents store locally and presents it in a single
interactive, color-coded viewer with real-time updates.

## Features

- Browse all Claude Code and OpenCode sessions across projects, side by side
- Subagent runs appear inline in their parent's timeline, at the call that
  launched them — and separately, so you can see what one run cost
- Sessions are listed by what they are about, not by ID — OpenCode's own title,
  or the opening prompt for Claude Code
- Event timeline with color-coded entries (prompts, tool calls, thinking, results)
- Detailed event drill-down with diff highlighting for file edits
- Session summary with token usage breakdown and activity stats
- Cost reconstructed per model from published rates, rather than one flat rate
- Live auto-follow mode — watch sessions update in real time
- Mouse scroll support
- Filter by project name
- Resume any session in a new tmux pane or terminal tab, with the right CLI for its source
- Delete sessions from the machine, with a confirmation step

## Requirements

At least one of:

- **Claude Code** — session transcripts are read from `~/.claude/projects/`
- **OpenCode** — sessions are read from its database, normally
  `~/.local/share/opencode/opencode.db`

## Install

### Download a release (recommended)

Grab the latest pre-built binary for your platform from the
[Releases page](https://github.com/fooxytv/verbose/releases).

Unzip it and move the binary somewhere on your PATH:

**macOS / Linux:**
```bash
sudo mv verbose /usr/local/bin/
```

**Windows:** move `verbose.exe` to a directory on your PATH, or add its location to your PATH.

### With `go install`

Requires Go 1.23+.

```bash
go install github.com/fooxytv/verbose@latest
```

### From source

Requires Go 1.23+.

```bash
git clone https://github.com/fooxytv/verbose.git
cd verbose
go build -o verbose .
```

## Usage

```bash
# View all sessions
verbose

# Filter to a specific project
verbose -project myapp
verbose -project /path/to/project

# Point at an OpenCode database in a non-standard location
verbose -opencode /path/to/opencode.db
```

### JSON output

`-json` answers one query on stdout and exits, so other tools can use what
verbose parses without reimplementing the reader:

```bash
verbose -json projects                  # every project, with spend and model mix
verbose -json sessions                  # every session, newest first
verbose -json sessions -project myapp   # just one project's
verbose -json sessions -limit 20        # cap the list
verbose -json session <id>              # one session's detail and todos
verbose -json session -events <id>      # ...including its event timeline
```

Each session carries a `source` (`claude` or `opencode`) and a `costBasis`
saying where its cost came from: `recorded` for OpenCode, which stores real
spend, or `estimated` for Claude Code, which stores only tokens. A subagent run
also carries `isAgent`, its `agentType`, and the `parentSessionId` it belongs
to.

OpenCode sessions are marked `◈` in the session list, Claude Code sessions `●`.

## Keybindings

| Key | Action |
|-----|--------|
| `j` / `Down` | Move down |
| `k` / `Up` | Move up |
| `Enter` / `Right` / `Space` | Open session / expand event |
| `Esc` / `Left` | Go back |
| `g` / `Home` | Jump to top |
| `G` / `End` | Jump to bottom |
| `PgUp` / `PgDn` | Page up / down |
| `d` | Delete session (asks to confirm) |
| `s` | Toggle session summary |
| `f` | Toggle auto-follow (timeline view) |
| `r` | Refresh session list |
| `q` / `Ctrl+C` | Quit |

Mouse scroll is also supported in all views.

## How it works

Claude Code stores session transcripts as `.jsonl` files in `~/.claude/projects/<project>/`.
Verbose scans this directory, parses each session into structured events, and watches for
file changes to provide live updates.

Each subagent a session spawns gets its own transcript, one level down, at
`<project>/<session-id>/subagents/agent-<id>.jsonl`. Those are read as sessions
in their own right — the agent's prompt, every tool call it made, what it cost —
keyed by their own agent id, since the file records the *parent's* session id
and would otherwise collide with it.

Their turns are then **spliced into the parent's timeline** at the Task call
that launched them, marked with a `│` gutter, so one session reads as one piece
of work instead of a Task call that disappears into nothing. A background
agent's result carries no output at all, so the only thing tying it back to its
caller is the agent id named in that result; that is what the splice anchors on,
falling back to timestamp order if it is missing. Costs are not copied into the
parent — each run is counted once, in its own row.

OpenCode instead keeps every project's sessions in one SQLite database, with a row per
message and a row per content part. Verbose reads it read-only, reassembles each session's
transcript, and polls for changes. Tool calls are mapped onto the same vocabulary as Claude
Code's — an OpenCode `read` is shown as a `Read`, `apply_patch` as the file edits it
performed — so both kinds of session share one timeline, one set of filters, and one diff
viewer. Subagent runs, which OpenCode records as separate child sessions, are folded into
the parent transcript at the point they were launched.

## Deleting sessions

Press `d` on a session and confirm with `y` — any other key cancels.

Claude Code transcripts are files, so they are **moved to the Trash** along with any
todo lists belonging to the session, and can be restored from there.

OpenCode sessions live as rows in one shared SQLite database, which is unsafe to write
to underneath a running OpenCode. Deletion is handed to `opencode session delete`, so it
needs the `opencode` CLI on your PATH and is **permanent**. The confirmation prompt says
which of the two applies before you commit.

## License

MIT
