# verbose

A terminal UI for reading Claude Code and OpenCode session transcripts. Go,
bubbletea, lipgloss. No CGo anywhere — `modernc.org/sqlite` for OpenCode's
database, `chroma` for syntax highlighting.

`AGENTS.md` is a symlink to this file, so Codex and anything else following the
cross-tool convention reads the same document. Claude Code reads `CLAUDE.md`
directly and only falls back to `AGENTS.md` when no CLAUDE.md exists, so one
file serves both — edit this one.

Longer notes that would bloat this file live in [docs/notes](docs/notes):
the [OpenCode database schema](docs/notes/opencode-database.md), the
[store and watcher traps](docs/notes/store-and-watcher.md), and
[why the replay and tree are shaped as they are](docs/notes/replay-and-tree.md).

## Layout

| Package | Holds |
|---|---|
| `pkg/session` | Parsing, the store and its watcher, replay steps, file activity, diffs. **Exported on purpose** — `harness` imports nothing but still shells out to `verbose -json`, and the parser is meant to be reusable. |
| `internal/ui` | Every view. bubbletea model in `model.go`; one file per view. |
| `internal/jsonout` | `-json` headless output: `sessions`, `session`, `project`. |

Views: sessions list → timeline (`detail.go`) → event drill-down → overview →
project → replay → tree → diff panel → continue panel.

## Building, versioning, releasing

**`VERSION` in the `Makefile` is the source of truth.** The binary reports it
through `-ldflags -X main.version=...`; a plain `go build` reports `dev`.

```bash
make install          # builds with the ldflag into ~/go/bin/verbose
verbose --version     # must match the Makefile
```

### Trunk-based: main is protected

**`main` cannot be pushed to directly.** Every change goes through a short-lived
branch and a pull request, and CI must pass before it can merge. Reviews from
someone else are not required — this is a one-person repository, so you approve
and merge your own — but the checks are not optional.

```bash
git switch -c fix/thing          # short-lived, one change
git push -u origin fix/thing
gh pr create --fill
gh pr merge --squash --auto      # lands itself once CI is green
```

Branches are meant to be hours old, not weeks. The point of the trunk is that
everything is merged into it continuously; a branch that lives long enough to
need a rebase has already lost most of the benefit.

`gh` must be acting as the **`fooxytv`** account. Two accounts are configured on
this machine and the other one is not a collaborator, so `gh pr create` fails
with `must be a collaborator` — a confusing error for what is only the wrong
active account. `gh auth switch --hostname github.com --user fooxytv` fixes it.

### Shipping a release

1. Bump `VERSION` in the `Makefile`.
2. `gofmt -l .` (silent), `go vet ./...`, `go test ./...`.
3. `make install` and check `verbose --version`.
4. Branch, PR, merge to `main` as above.
5. `git tag vX.Y.Z && git push origin vX.Y.Z` — from `main`, after the merge.

The tag is what publishes. `.github/workflows/release.yml` fires on `v*`, runs
goreleaser, and builds linux/windows/darwin × amd64/arm64. The workflow takes
its Go version from `go.mod` (`go-version-file`) — do not pin it separately,
that drift once broke the build silently. `.goreleaser.yml` sets the same
`main.version` ldflag, so a released binary reports its real version.

`go install github.com/fooxytv/verbose@latest` works: the module path and the
repository path match.

**Version without a tag is not released.** Bumping the Makefile only changes
the local install.

### What CI checks

`.github/workflows/ci.yml` runs on every PR and every push to `main`: `gofmt`,
`go vet` and `go test` on linux, macOS and windows, plus a cross-compile of all
six release targets.

**Windows is tested, and three traps there are worth not re-learning:**

- **`$HOME` does not redirect the home directory.** Everything resolves home
  through `os.UserHomeDir()`, which reads `%USERPROFILE%` on Windows. A test
  that sets only `HOME` isolates nothing there — it reads the real profile and
  fails on a path it never meant to touch. `isolatedHome` and `deleteFixture`
  set both.
- **A file cannot be removed while a handle is open.** `copyThenRemove` closes
  the source explicitly before `os.Remove`; a `defer` alone runs too late and
  leaves the original in place with a copy already in the trash.
- **Short paths are full of tildes** (`C:\Users\RUNNER~1\...`). A tilde only
  expands at the start of a word, so `shellRemovals` rejects a leading one and
  nothing else — treating them all as expansions meant no deletion was ever
  recognised on Windows.

The trash is not the Recycle Bin there, which needs a shell API call verbose
does not make. Files go to `%USERPROFILE%\.local\share\Trash\files`, which
the README now says.

That last job exists because **a tag used to be the first time those targets
were built**. goreleaser runs *after* the tag exists, so a target that does not
compile cannot be fixed in place — it needs a whole new version number. The
cross-compile job moves that failure to the PR, where it costs nothing.

## Transcript format — facts worth not re-deriving

Verified against real data; each of these cost a bug to learn.

- **One JSONL line per content block.** Every block of one assistant message
  shares `message.id` and repeats `usage` verbatim. Never dedupe by message id
  to pick "the latest" — that drops 30–55% of tool calls. Accumulate usage once
  per id.
- **The subagent tool is named `Agent`, not `Task`.** Zero `"name":"Task"` on
  disk. Match both; OpenCode normalises its `task` to `Task`.
- **Background subagents write their own file** at
  `<project>/<session-id>/subagents/agent-<id>.jsonl`, with a `.meta.json`
  beside it. The only link back is `agentId: <hex>` in the otherwise-empty
  tool result. Foreground sidechains stay inline with `isSidechain: true`.
- **Diffs live in two different fields.** `toolUseResult.structuredPatch` for
  the edit tools (one file), `toolUseResult.bashEditDiff` for a shell command
  (`{files: [{filePath, hunks}]}`, several). Over one machine's history the
  shell recorded *more* of them. Always go through
  `ToolResult.ChangedFiles()`.
- **A new file records an EMPTY `structuredPatch`** — there is no "before". The
  content is in the `Write` call's `content` input instead.
- **Deletion is not recorded.** No delete tool exists; an `rm` in a command
  line is the only evidence, and it is inferred, never certain.
- **`toolUseResult` is an object on success and a bare string on failure**
  ("Error: Exit code 127"). That string is the reliable failure signal —
  `is_error` is false for a failing shell command.
- **Extended thinking is not retained.** Every thinking block on disk has an
  empty body, so a replay can say what happened and not why.
- **`cwd` beats the directory name.** The encoded project directory cannot tell
  a path separator from a hyphen.

## Rendering rules — all of these were bugs

The terminal is unforgiving and the failure mode is identical every time: a row
that is too wide wraps, the frame takes more rows than the renderer counted, the
terminal scrolls, and rows are stranded on screen. It reads as the view
duplicating or freezing, and stepping away and back "fixes" it by forcing a
repaint.

- **Never write to the last column.** A row of exactly `width` leaves the cursor
  in pending-wrap and the next newline costs a row. `usableWidth()` is width-1.
- **Expand tabs before measuring anything.** `visibleLen` counts a tab as one
  column; a terminal draws up to eight. Tab-indented code measured 94 and drew
  179. `expandTabs()` runs before any width maths.
- **`clampFrame` in `Model.View` is the chokepoint.** Every view passes through
  it. Trust it rather than every producer.
- **`wrapLines` does not wrap** — it truncates each line, which is right for
  command output and wrong for prose. `wrapProse` is the word-wrapper.
- **ANSI-aware everything.** `visibleLen` / `truncateVisible` for measuring and
  cutting coloured text; a cut must close the style or colour bleeds.
- Keep the invariant tests honest: they must measure with `terminalColumns`
  (which expands tabs), not `visibleLen`, or they will pass while the screen
  breaks.

## Keybindings on macOS

**Never bind ctrl+arrow.** macOS takes all four for Mission Control,
Application Windows and Spaces, so the terminal never sees them — a binding
there is silently dead. Prefer bare characters (`[` `]` `{` `}`); they have no
modifier to intercept. Verifying that bubbletea *decodes* a sequence is not
verifying that anything *sends* it.

## Opening an editor

`e` hands the file under the cursor to `$VISUAL`, then `$EDITOR`, then the first
of `nvim`/`vim`/`vi`/`nano` on `PATH` (`notepad` on Windows). **An editor is
never a build or install dependency** — nothing in `editor.go` is reached until
the key is pressed.

- **Only vi-family editors get `+N`.** An editor that does not understand the
  flag treats it as a second filename and opens an empty buffer called `+12`.
  `lineArg` returns nothing for anything not on its list; losing the jump beats
  a bogus buffer.
- **`$EDITOR` may carry arguments** (`code -w`), so it is split into fields, not
  taken whole as a binary name.
- **Check the file is still on disk.** The tree deliberately grafts back deleted
  files, so a path on screen is not proof of a file.
- **Stop playback and bump `replayGen`** before launching, or the ticks
  scheduled during the edit all arrive at once on return.
- `ReplayStep.CodePath` is display text and cannot be turned back into a path:
  `relPath` abbreviates to `…/dir/file`, and a step with a real diff has none at
  all. `ReplayStep.FilePath` is the absolute one.

## Testing

- `go test ./...` should stay under ~20s. The replay smoke sweep strides
  through at most 40 steps a session for that reason; an exhaustive sweep
  reached 54s and a suite that slow stops being run.
- Smoke tests render every view against the **real transcripts on this
  machine** and skip when there are none. They catch panics and layout breaks
  that synthetic fixtures do not.
- Benchmarks guard the per-frame cost: `BenchmarkReplayFrame`,
  `BenchmarkTreeFrame`. A frame must stay far under the 30ms tick.
- When a test is meant to catch a specific bug, **check it fails without the
  fix** before trusting it.

## Demo recordings

`docs/demo.tape` (VHS) produces `docs/demo.gif` and `docs/img/*.png` for the
README. It runs against a synthetic tree built by `docs/demo-data.py` under
`HOME=/tmp/devdemo`, never the real one — **this repository is public and a real
transcript carries project names, paths, prompts and code.**

```bash
brew install vhs
python3 docs/demo-data.py && vhs docs/demo.tape
```

`verbose` honours `$HOME`, which is what makes the synthetic tree possible.
