# Working with sessions

Opening files, picking a session back up, renaming folders, deleting.


`e` hands the file under the tree cursor to your own editor — in a replay, in
the split, in the tree, or from an open diff. Without a tree beside it, `e` in a
replay opens whatever file the current step touched.

It opens **at the line that changed**, taken from the first hunk of the most
recent change up to where the replay has reached, so you land on the work rather
than on line 1. A new file has no hunks to read a line from, so that one opens
at the top.

The editor is whatever you already use: `$VISUAL`, then `$EDITOR`, then the
first of `nvim`, `vim`, `vi` or `nano` found on your PATH (`notepad` on
Windows).

**`e` takes the terminal and hands it back when you quit**, the way `$EDITOR`
behaves in `git commit` or `crontab -e`. Verbose comes back on the same step,
with the same tree row selected.

**`E` opens it beside verbose instead** — a tmux pane where there is one,
otherwise a new terminal tab. Useful when you want the diff still on screen,
but it is the explicit choice rather than the default: a new pane is created
each time, so opening four files leaves four panes, and verbose cannot tell
when you have finished with them.

Playback stops either way, so a replay is not several minutes further on when
you return.

**No editor is required to run verbose.** None of this is reached unless you
press `e`, and nothing is added to the build. Anything your editor brings — an
LSP, Copilot, your own bindings — comes with it, which is the point: verbose has
no business growing an editor of its own.

Two things it will not do. A file the session touched that has since been
deleted is **not** opened, because an editor given a missing path silently
creates an empty buffer that reads as lost work; the tree grafts those files
back in and marks them `(gone)`, so the path on screen is not proof of a file on
disk. And only vi-family editors are told which line to open — an editor that
does not understand `+41` treats it as a second filename and opens a blank
buffer called `+41`, so verbose would rather lose the jump than do that.

## Picking a session back up

`c` in a replay or the tree opens the **continue panel**: what the session last
said, and a box to reply in. Type, press enter, and the CLI opens **beside**
verbose — a tmux split where there is one, otherwise a new terminal tab — with
that session resumed and your reply already sent. The replay, the tree and your
place in them stay on screen.

An empty reply is a plain resume, which is what `c` has always done. Claude
sessions resume with `claude --resume <id> "<reply>"`; Codex with
`codex resume <id> "<reply>"`; pi with `pi --session <id> "<reply>"`;
Copilot with
`copilot --resume <id> --interactive "<reply>"` (`--prompt` would run the reply
and exit); OpenCode with `opencode --session <id> --prompt "<reply>"`.

The reply is handed to the CLI rather than run inside verbose. Verbose reads
transcripts; running a session in-process would mean carrying a PTY and an ANSI
parser to end up with a worse tmux.

A subagent's own transcript cannot be replied to — open its parent session —
and neither can one that recorded no working directory, since there would be
nowhere to start the CLI. The panel says so rather than failing quietly.

## After renaming or moving a folder

A transcript records the folder it ran in by its full path, so renaming the
folder leaves its sessions pointing at nothing: the CLI cannot be started there,
and the tree has no files to show. Run `verbose relink` in the folder's new
location:

```
$ cd ~/work/new-name
$ verbose relink
Folders that no longer exist, best match first:
  1. /Users/me/work/old-name
     3 sessions · last active 2d ago · 12 of 14 files found here
  2. /Users/me/scratch
     1 session · last active 3w ago · 0 of 2 files found here

Relink which to /Users/me/work/new-name? [1]
```

There is no need to remember the old name. Verbose lists every folder its
sessions ran in that no longer exists, and ranks them by how many of the files
those sessions touched are sitting in the current folder. A renamed folder keeps
its contents, so the right one is normally first. Enter only picks it when at
least half of its files are found; otherwise, choose by number.

```bash
verbose relink old-name        # skip the list: a folder's name or full path
verbose relink -y              # take the best match without asking
verbose relink -list           # what has been relinked
verbose relink -remove <old>   # undo one
```

The transcripts are not changed. The link is kept in
`~/.config/verbose/relinks.json` and applied as verbose reads each session, so
continuing, the tree, diffs and `e` all use the new location. Restart verbose
if it was already open. Claude Code needs no help with this, because
`claude --resume <id>` finds a session from any folder.

## Deleting sessions

Press `d` on a session and confirm with `y` — any other key cancels.

Claude Code transcripts are files, so they are **moved to the Trash** along with any
todo lists belonging to the session, and can be restored from there.

Where "the Trash" is depends on the platform: `~/.Trash` on macOS, the
freedesktop location (`$XDG_DATA_HOME/Trash/files`, or `~/.local/share/Trash/files`)
on Linux. **On Windows it is not the Recycle Bin** — the Recycle Bin needs a
shell API call that verbose does not make, so files land in
`%USERPROFILE%\.local\share\Trash\files` instead. They are recoverable, just
not through the usual restore.

Codex and Copilot sessions **cannot be deleted from verbose**. Each lives in a
database the agent itself holds open, and neither CLI has a delete to hand the
job to — `copilot sessions` offers only `import`. Pressing `d` says so rather
than asking a question whose yes could not be honoured.

OpenCode sessions live as rows in one shared SQLite database, which is unsafe to write
to underneath a running OpenCode. Deletion is handed to `opencode session delete`, so it
needs the `opencode` CLI on your PATH and is **permanent**. The confirmation prompt says
which of the two applies before you commit.

---

[← back to the README](../README.md)
