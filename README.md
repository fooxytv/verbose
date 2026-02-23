# verbose

A terminal UI for browsing and analyzing [Claude Code](https://docs.anthropic.com/en/docs/claude-code) session transcripts.

Verbose reads the `.jsonl` session files that Claude Code stores locally and presents them in an interactive, color-coded viewer with real-time updates.

## Features

- Browse all Claude Code sessions across projects
- Event timeline with color-coded entries (prompts, tool calls, thinking, results)
- Detailed event drill-down with diff highlighting for file edits
- Session summary with token usage breakdown and activity stats
- Live auto-follow mode — watch sessions update in real time
- Mouse scroll support
- Filter by project name

## Requirements

- **Go 1.23+**
- **Claude Code** — verbose reads session data from `~/.claude/projects/`

### Installing Go

**macOS** (Homebrew):
```bash
brew install go
```

**Windows** (winget):
```powershell
winget install -e --id GoLang.Go
```

**Linux** (apt):
```bash
sudo apt update
sudo apt install golang-go
```

You can verify your installation with `go version`.

## Install

### From source

```bash
git clone https://github.com/fooxytv/verbose.git
cd verbose
go build -o verbose .
```

Then move the binary somewhere on your `PATH`:

**macOS / Linux:**
```bash
mv verbose /usr/local/bin/
```

**Windows (PowerShell):**
```powershell
move verbose.exe $env:GOPATH\bin\
```

### With `go install`

```bash
go install github.com/fooxytv/verbose@latest
```

> Note: the module is currently named `verbose` locally. `go install` from the remote will work once the module path in `go.mod` matches the GitHub repo path.

## Usage

```bash
# View all sessions
verbose

# Filter to a specific project
verbose -project myapp
verbose -project /path/to/project
```

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
| `s` | Toggle session summary |
| `f` | Toggle auto-follow (timeline view) |
| `r` | Refresh session list |
| `q` / `Ctrl+C` | Quit |

Mouse scroll is also supported in all views.

## How it works

Claude Code stores session transcripts as `.jsonl` files in `~/.claude/projects/<project>/`. Verbose scans this directory, parses each session into structured events, and watches for file changes to provide live updates.

## License

MIT
