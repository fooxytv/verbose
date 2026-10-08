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
- Replay a session one step at a time, at reading speed, to follow work that
  originally scrolled past in seconds
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
| `R` | Replay the session step by step |
| `T` | Project tree, marked with what the session changed (works inside a replay too) |
| `f` | Toggle auto-follow (timeline view) |
| `r` | Refresh session list |
| `q` / `Ctrl+C` | Quit |

In the replay view:

| Key | Action |
|-----|--------|
| `Space` | Play / pause |
| `Right` | Finish typing this step, or move to the next |
| `Left` | Previous step |
| `Up` / `Down` | Scroll — takes manual control and pauses |
| `+` / `-` | Faster / slower (0.5s to 15s per step) |
| `/` | Go to a step by number — type `24`, press enter |
| `Tab` | Code only — just the steps that wrote something |
| `f` | Follow a running session (on by default) |
| `0` | Back to the first step |
| `t` | Jump to this point in the timeline |
| `↑`/`↓`, `j`/`k` | Move the tree selection (full-screen tree) |
| `]` / `[` | Move the tree selection (with the sidebar open) |
| `}` / `{` | Open / close a directory in the sidebar |
| `d` | Open the selected file's diff (`d` or `Esc` closes) |
| `Ctrl`+`f` | Hand the tree's follow back to the replay |
| `Esc` | Back |

Mouse scroll works in every view. With the tree beside the replay, the pointer
decides which pane moves: over the tree it moves the selection, over the code it
scrolls the step.

The footer's right-hand side shows how long ago the open session was written,
the time now, and the version — so a live session visibly ticking apart from
one that stopped four minutes ago. On a narrow terminal both halves give way
gracefully: keybindings drop from the least useful end (never `q`), and the
status drops the version before the clock and the clock before the session age.

## Replay

Agents work faster than anyone can read. Replay gives one operation the whole
screen — what it did, in plain English, and the real evidence underneath: the
diff hunks Claude Code recorded, the command and what it actually printed.
Press `R` on any session, then `Space` to let it run.

**Code is typed out, not pasted in.** While playing, a diff is written a
character at a time at about 100 a second, with the viewport following the
cursor so the code arrives in front of you rather than off-screen. `+` and `-`
change the whole pace, typing and holds together. Command output is never
typed — a diff is worth following keystroke by keystroke, nine kilobytes of log
output is not.

Pausing or scrolling hands control back to you and reveals the whole step at
once, because stopping to look means you want to read it, not watch it.

### Finding the code

Most of what an agent does is not writing code. Across one machine's
transcripts there were 2592 shell commands against 549 edits, so the steps that
actually produced something are easily buried. **`Tab` narrows the replay to
the steps that wrote code** — a recorded diff, a new file, or a shell heredoc —
and the header says `code only` while it is on.

Verbose looks for that code in three places, because a diff alone finds very
little of it:

| Where the code is | When |
|---|---|
| `structuredPatch` | An `Edit` to a file that already existed |
| The `Write` tool's `content` input | A **new** file — Claude Code records an *empty* patch, since there is no "before" |
| A shell heredoc body | The agent wrote the file through `Bash` — no patch is recorded at all |

On one session those last two rows took the visible-code steps from 1 to 173.
Machine-wide, from 6.4% of steps to 21.3%.

A heredoc piped to an interpreter (`python3 - <<'PY'`) is shown as code but
labelled without a filename, because it writes no file — it is the script that
did the editing, not the result.

Streamed code is **syntax highlighted**, with the language taken from the
filename, or analysed from the content when a heredoc has no target. The colour
depth matches the terminal: truecolor where there is truecolor, 256 colours
otherwise. Highlighted files are cached, so a step costs about a millisecond to
re-render regardless of size.

### Following a live session

Replay follows a session that is still being written. The step count updates on
its own as the transcript grows — `300/300` becomes `300/312` without any
keypress — and when playback reaches the last recorded step it holds, `◴ live`,
then plays the new ones as they arrive. It reads at human speed while the agent works
at its own, so it falls behind; the gap is shown as `N behind` rather than
skipped. `f` turns following off.

It keeps the steps that carry the narrative — prompts, the agent's own
"here's what I'll do next", and every operation — and drops the bookkeeping
(hook progress, turn timings, bash keepalives). Subagent steps are marked, so
you can see where work was handed off and follow what the agent did with it.

If you want this *live* — an agent typing into your real editor while you
steer, which a transcript viewer cannot do — see
[AI Pair](https://github.com/faiface/ai-pair), a VS Code extension that gives
your existing agent a second cursor over MCP.

One honest limitation: **a replay can tell you what happened, not why.**
Extended thinking is signed but never written to the transcript, so every
thinking block on disk has an empty body. What a step shows is derived from
what was recorded — files, line counts, exit behaviour — and the agent's own
narration where it exists. Generated explanations are a separate layer, not
yet built.

## Tree

`T` works from the sessions list, the timeline and from inside a replay. It
shows the project as a tree, marked with what the open session did to each
file: **new** files in green, **changed** in orange with their churn, **removed**
in red, everything the session never touched dimmed. A change flashes as the
replay reaches it and then keeps its colour, so the tree accumulates a record of
the session rather than settling back to neutral. `Tab` narrows it to just the
files that changed. `Enter` on a file jumps the replay to the change.

Two different kinds of truth sit in that one view, and the header says so:

- **The structure comes from disk**, so it is the project as it is *now*. For a
  session from three weeks ago that is today's shape with three-week-old marks
  painted on. Files the session touched that have since gone are grafted back in
  and marked `(gone)`.
- **The marks come from the transcript**, which is exact however old the session
  is.

The tree follows the replay, and **scrolls itself to whatever is being worked
on** — a project tree is far longer than the pane showing it, so without
that the file being written is usually below the fold and its flash is never
seen. It stays on the most recent change rather than snapping back between
steps, and ignores changes with no row in the tree, such as scratch files a
session writes outside its own project.

`Space` plays, and files light up as they are written, settling over about a
second into their colour — so the project assembles itself in front of you.
Step back in the replay and later work disappears again. Moving the cursor
yourself takes over from the follow; `Space` hands it back. The sidebar has no
cursor of its own, so it always follows.

### Diffs

With a file selected in the tree, **`d` opens its diff** in the right-hand
pane — old on the left, new on the right, with line numbers, pairing each
removed line against the one that replaced it. `[` and `]` step to the next
file and the diff follows, so a run of changes can be read without closing it.
`d` or `Esc` closes it.

Opening a diff pauses playback: reading a diff and watching code stream are not
things anyone does at once, and the pane the diff needs is the one the replay
was using. Two columns need **86 columns of pane**; below that it falls back to
a unified single column and says so.

Like the tree's colours, the diff is bounded by where the replay has reached —
it shows what had happened by that point, not what is still to come. For a file
whose changes come later it says so and tells you how many.

A **new** file has no previous version to compare against, so it is shown as
all-additions rather than a two-sided diff. The same goes for a file written
through the shell, where no diff is recorded at all.

### Side by side

`T` inside a replay puts the tree **beside** the code rather than instead of it:
the tree on the left, the code streaming on the right, the tree lighting up as
each file is written. All keys stay with the replay and the tree follows, so
there is no focus to switch and no key that means one thing on the left and
another on the right — navigating the tree is what the full-screen view is for.

In the split the plain arrows stay with the replay, so nothing changes meaning
when the sidebar opens. The tree gets its own keys: **`]` and `[` move the
selection**, `}` and `{` open and close directories.

They are bare characters on purpose. `Ctrl` with the arrows is unusable on
macOS, where the system takes all four for Mission Control, Application Windows
and moving between Spaces, so the terminal never sees them; `Shift` with the
arrows is already page-scroll here and some terminals keep it for text
selection. A plain character has no modifier to be intercepted. The `Ctrl`
bindings are still accepted as aliases if you have remapped your system
shortcuts.

Driving the tree by hand stops it following the replay — being pulled away
mid-read is worse than having to ask for the follow back — and `Ctrl`+`f` hands
it back. The footer shows which of the two is in charge.

It needs **100 columns**. Below that the code pane has too little room to read
code in, so the split is refused and says how many columns it wants rather than
showing two unusable halves. The tree takes a third of the width, up to 40
columns.

Build output and version control are skipped (`node_modules`, `.git`, `dist`,
`target`, `.terraform` and the rest), unless the session changed something
inside, in which case the directory is opened anyway. The scan is capped at
4000 entries and 8 levels so a large repository cannot stall the view.

Because half of what an agent does goes through the shell, the tree also credits
files that only appear in a command — `sed -n '1,80p' main.go`,
`grep -n x pkg/a.go`, `cat README.md`. Measured over one machine's transcripts,
1515 of 2992 shell commands named a real file in the project and 202 files
appeared *only* that way, so a tree built from tool calls alone sits still
through most of a Bash-heavy session. Those mentions are resolved against the
tree already read from disk, so a version number or a Go module path that
happens to look like a path does not register, and they are marked inferred: a
command naming a file is good evidence it was read and no evidence of anything
more.

One honest limitation: **deletions are inferred, not recorded.** Claude Code has
no delete tool, so the only evidence is an `rm` inside a shell command. Simple
forms are picked up; anything with a glob, a variable or a substitution is
skipped rather than guessed at, because naming the wrong file as deleted is
worse than naming none. For the same reason a file written through the shell is
reported as *written* rather than created or edited — whether it existed
beforehand is nowhere in the transcript.

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
