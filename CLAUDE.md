# verbose

A terminal UI for reading Claude Code, Codex, Copilot, OpenCode and pi session
transcripts. Go, bubbletea, lipgloss. No CGo anywhere — `modernc.org/sqlite`
for the Codex, Copilot and OpenCode databases, `chroma` for syntax
highlighting.

`AGENTS.md` is a symlink to this file, so Codex and anything else following the
cross-tool convention reads the same document. Claude Code reads `CLAUDE.md`
directly and only falls back to `AGENTS.md` when no CLAUDE.md exists, so one
file serves both — edit this one.

Longer notes that would bloat this file live in [docs/notes](docs/notes):
the [OpenCode database schema](docs/notes/opencode-database.md), the
[Codex databases](docs/notes/codex-databases.md), the
[Copilot store](docs/notes/copilot-store.md), the
[pi sessions](docs/notes/pi-sessions.md), the
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
with `must be a collaborator` and `gh pr merge` with `does not have the correct
permissions` — confusing errors for what is only the wrong active account.

**The switch does not persist between shell invocations.** Each command runs in
a fresh shell, so it has to be in the *same* one as the `gh` call that needs it:

```bash
gh auth switch --hostname github.com --user fooxytv >/dev/null && gh pr merge 7 --squash
```

**Never pipe a `gh` command whose exit code matters.** `gh pr merge … | tail -1`
exits 0 even when the merge was refused. Chaining a tag onto that with `&&`
tagged a commit that did not carry the version bump — the release then built
from a tree whose `Makefile` still said the old number. Print `exit=$?` instead,
or let the command's status stand on its own.

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

**Windows is tested. Six things cost a CI round each:**

- **A panic truncates the whole package.** A nil dereference in one test
  aborted the `pkg/session` binary, so every test after it never ran and the
  failure list was never complete — four rounds of "fixed it" were each reading
  a prefix. Assert and `t.Fatal`; never dereference a map lookup in a test.
- **`$HOME` does not redirect the home directory.** Everything resolves home
  through `os.UserHomeDir()`, which reads `%USERPROFILE%` on Windows. A test
  that sets only `HOME` isolates nothing — it reads the real profile. Set both;
  `isolatedHome` and `deleteFixture` do.
- **A file cannot be removed while a handle is open.** `copyThenRemove` closes
  the source explicitly before `os.Remove`; a `defer` alone runs too late and
  left the original in place with a copy already in the trash. This one was a
  real bug, not a test problem — deleting a session could not work.
- **`/repo` is not an absolute path.** `filepath.IsAbs` wants a drive letter,
  so a unix-style root in a fixture gets joined onto the cwd and nothing
  matches. Build test paths from `testRoot()`. The production code is fine — a
  real Windows transcript records `C:\...`.
- **Path output carries the platform's separator.** `filepath.Join`, `Clean`
  and `Rel` all return `\` here, so an expectation with a literal `/` fails.
  Build the expectation the same way the code does. `Dir` and `Base` are safe:
  they accept `/` on Windows.
- **Short paths are full of tildes** (`C:\Users\RUNNER~1\...`). A tilde only
  expands at the start of a word, so `shellRemovals` rejects a leading one and
  nothing else. Treating them all as expansions meant no deletion was ever
  recognised — on Windows, and on `rm /tmp/build~2/old.go` anywhere.

Two things have no Windows equivalent and are skipped rather than faked: a
`#!/bin/sh` CLI stub, and the Recycle Bin. The trash is
`%USERPROFILE%\.local\share\Trash\files`, which the README now says.

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
- **`claude --resume <id>` works from any folder** (verified on 2.1.302). After
  a rename it resumes the same conversation, appends to the original file under
  the OLD project directory, and records the new `cwd` on the lines it adds.
  So a renamed folder needs nothing moved; only verbose's reading of the paths
  is stale, which is what `verbose relink` fixes.

## Reading a Codex thread

`pkg/session/codex.go`. Full schema and counts in
[docs/notes/codex-databases.md](docs/notes/codex-databases.md); these are the
ones that bite.

- **Two databases, and the number in the name is a schema generation.**
  `state_N.sqlite` has the metadata, `thread_history_N.sqlite` the content;
  both are globbed and the highest `N` wins, because `state_4` beside `state_5`
  is the schema Codex migrated away from. A home with one of the pair is
  unreadable, not half-readable.
- **`name` is the title, `title` is the opening message** — verbatim, often
  thousands of characters of pasted context or an XML delegation wrapper.
- **A `fileChange`'s `diff` is content for an `add` and hunks for an
  `update`**, which is Claude Code's Write-input-versus-structuredPatch split
  in a single field. An add has to arrive as a `Write` carrying `content` or
  `stepCode` has nothing to type.
- **Reasoning is a headline at best.** `content` is empty on every item on
  disk and `summary` is set on 39% of them. Drop the empty ones.
- **A failed command is knowable only from `status`** — it writes its reason
  into the same `aggregatedOutput` a successful one uses.
- **`source` is a string or JSON.** `{"subagent":{"other":"guardian"}}` is what
  makes a thread an agent; `thread_source` says how a thread was *started*
  (`voice_chat`, `chatgpt_handoff`) and is NOT an agent type.
- **A threads row exists before anything happens in it**, so a row with no
  items is not a session.
- **`tokens_used` is a single number**, which is why `SessionInfo.TokensTotal`
  and `TotalTokens()` exist. Never fake it into the input column: the
  overview's bar would claim it was all input.
- **Codex sessions cannot be deleted**, and `d` says so rather than asking.
- Resume is `codex resume <id> [prompt]` — a subcommand, with the id as a bare
  argument.

## Relinking a renamed folder

`verbose relink` keeps old → new links in `~/.config/verbose/relinks.json`;
`Relinks.Apply` rewrites a session's paths as the store loads it, in
`load`, `reload` and both OpenCode paths. Nothing downstream knows links exist.

- **Never edit the transcripts.** They are Claude Code's, and it does not need
  them changed (above).
- **Match whole path segments.** `C:\work\app` must not rewrite
  `C:\work\app-old`; `replacePathPrefix` checks the next character cannot
  continue a name. Windows also needs the forward-slash spelling a shell
  command uses, and case-insensitive matching.
- **Enter is only a default with evidence**: at least half the files the
  sessions touched must exist here. Run from the wrong folder, one shared
  README made an unrelated folder the default at 1 of 9.

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

## Keybindings across platforms

**Never bind ctrl+arrow.** macOS takes all four for Mission Control,
Application Windows and Spaces, so the terminal never sees them — a binding
there is silently dead. Prefer bare characters (`[` `]` `{` `}`); they have no
modifier to intercept. Verifying that bubbletea *decodes* a sequence is not
verifying that anything *sends* it.

**Never match space by `msg.Type == tea.KeySpace`.** bubbletea decodes the
space bar as `KeySpace` on unix but as `KeyRunes{' '}` on Windows, so every
space binding was dead there while working on macOS. Use `isSpace(msg)`.

## Grouping subagents in the list

`GetSessions` sorts newest-first, which scattered a session's agents among
unrelated work. `GroupSubagents` then pulls each subagent under its parent.

- **Children are ordered oldest-first**, against the list's newest-first rule,
  so a parent's runs read as the sequence of work.
- **An orphan keeps its place.** A subagent whose parent is filtered out or
  deleted still has its own cost and its own work; hiding it or hoisting it to
  the end loses a real run.
- **The connector lives inside the four-wide status column**, not as an indent:
  `fmt` pads `%s` by runes, so `└─◦` and `●` both occupy four columns and every
  later column stays put. Indenting the row instead shifts them all.
- **Measure alignment in runes.** `●` is three bytes and `└─` is six, so
  `strings.Index` reports a shift the terminal never draws — the same
  byte-versus-rune trap that cut rows short before `truncateRunes`.
- **Totals stay per row.** A parent is not the sum of its children; the project
  view sums the column and would double-count.

### Switching between a parent and its agents

`a` swaps the session under the replay; `RunGroup` builds the ring.

- **Any member returns the same group**, so it does not matter whether the
  parent or an agent was opened first.
- **Remember the step per session** (`replayMarks`), or switching back drops
  you at step 1 and stops being worth doing.
- **The tree must be reloaded** on the swap, or it keeps marking files with the
  previous session's activity.
- **The header carries the position**, not just a status message: the message
  clears after a few seconds and then nothing on screen says whose work it is.

## Rendering a plan's markdown

`renderMarkdown` in `internal/ui/markdown.go` lays out a `StepPlan`'s text.
Nothing else goes through it: every other `Detail` is a sentence
`DescribeEvent` built, and a markdown parser can only misread generated prose.

- **`collapseSpaces` was the bug.** It is `strings.Fields` joined by a space,
  so it deleted every newline in the block — a heading, its list and a table
  ran together into one paragraph. `DescribeEvent` now returns an `EventText`
  body untouched; the renderer is what shapes it.
- **Consecutive lines are ONE paragraph.** That is markdown, and it is also
  what `wrapProse` gets backwards in the other direction: it treats every
  newline as a paragraph break, so a soft-wrapped sentence came out as a column
  of short lines.
- **The viewport must not follow a step that types nothing.** `renderReplay`
  jumps to the bottom while `typed >= 0` so a diff arrives on screen. A plan
  types nothing, so a long one opened at its LAST line until the follow was
  gated on `TypedLength(step, sess) > 0`.
- **Underscores are never emphasis**, and an asterisk only opens it at a word
  boundary with no space after, closing on one with no space before. That is
  what keeps `snake_case`, `rm *.go *.md` and `2*3*4` literal. Getting it wrong
  deletes characters from a command the reader is meant to be able to retype.
- **One style per line, or none.** A heading and a table's header strip their
  inline markers and apply a single style; nesting lipgloss styles ends the
  outer colour at the first inner reset and the rest of the line loses it. Code
  inside bold (24% of blocks) needs its own style rather than nesting.
- **Every glyph is measured.** `╌` U+254C, `▐` U+2590, `▪` U+25AA, `◦` U+25E6
  and `‣` U+2023 are all width 1 and non-ambiguous; `•`, `·`, `─` and `│` are
  ambiguous and are not used here. `TestMarkdownGlyphsAreNotAmbiguousWidth`
  enforces it with `runewidth`, which is why that module is a direct
  dependency.
- **Tables have no vertical rules.** Two spaces and a bold header read as a
  table, and every box-drawing glyph that would draw the rules is
  ambiguous-width. A header of empty cells gets no bold row and no divider —
  the pipes were there to align two columns, not to label them.
- **Nothing is clamped on the way out.** The width contract is `wrapProse`'s —
  prefix plus at most `maxWidth` — and a blind truncate at the end would make
  `TestMarkdownFitsItsWidth` pass while the producers stayed broken.
  `clampWidth` in `Model.View` is still the net.
- Costs about 1ms for a real plan and 4.6ms for a 60KB one, against a 30ms
  tick, so there is no cache. `highlightCode` already caches the fenced blocks.

## Reading a Copilot session

`pkg/session/copilot.go`. Full shapes in
[docs/notes/copilot-store.md](docs/notes/copilot-store.md).

- **The database lags the event stream.** `session-store.db` has the metadata
  and a `turns` table; `session-state/<id>/events.jsonl` has what was actually
  said. The live session here had 25 events and ZERO turns, so the stream wins
  wherever there is one. `turns` is the fallback for a GitHub-hosted run,
  which writes no state directory.
- **`model.response` and `model.message` are not the reply.** They are the
  side call that writes the session's title, and their content is a
  `<session-title>` tag. Only `assistant.message` is the answer.
- **`transformedContent` is not what was typed** — it carries a
  `<current_datetime>` preamble meant for the model.
- **A failed turn raises `session.error` AND `model.turn_failed`**, so
  counting both reported five failures for three rate-limited prompts.
- **`summary` can be a whole GitHub issue body**, newlines included, and a
  title with a newline costs the list a row it did not count.
- **`session.usage_record` is the best cost record of any source**: a full
  token breakdown AND a real price, per turn.
- **Tool calls are unimplemented** because no session on this machine has ever
  made one, and the event names are not recoverable from the binary (it is a
  compressed bundle). It needs one real tool-using session, not a guess.
- Resume is `copilot --resume <id>`, and `--interactive "<reply>"` to send a
  first message with it. **Not `--prompt`**, which runs the reply and exits.

## Reading a pi session

`pkg/session/pi.go`. Full format in
[docs/notes/pi-sessions.md](docs/notes/pi-sessions.md).

- **One JSONL file per session**, under `~/.pi/agent/sessions/<encoded cwd>/`.
  The encoded name has the same hyphen-versus-separator ambiguity Claude
  Code's does, so the `session` entry's `cwd` is what is used.
- **It is the only source that keeps its reasoning.** 47 of 47 thinking
  blocks have their full text, where Claude Code's are always empty and Codex
  keeps a headline on 39% of them. That is what fills a `pi's reasoning` step.
- **The edit diff is the one pi DISPLAYED**, not a unified one: numbered
  lines, no `@@`, runs separated by an elision. The number is right-aligned in
  a field that follows the file, so a three-digit line has no space after the
  marker (`-100`) where a two-digit one does (`- 86`) — a pattern expecting a
  separator drops every line past 99.
- **A result is a separate message**, keyed by `toolCallId`, and is folded
  back into its call so one operation stays one event.
- **Timestamps are milliseconds on the message and ISO 8601 on the entry**
  around it, on the same line.
- `usage` is a full breakdown AND a price, per message.
- Resume is `pi --session <id>`, with messages as bare arguments. Deleting
  needs nothing special: one file, trashed like Claude Code's.

## Searching the whole history

`pkg/session/search.go`, `/` in the sessions list.

- **There is no index and does not need to be one.** Every session is already
  parsed and in memory; 18,000 events across 62 sessions scan in 12ms, well
  inside a 30ms frame. An index would be a cache to invalidate for no gain.
- **A match has to say WHY.** A result list that only narrows is a list of
  sessions with no stated reason for being there, so each row shows the kind
  (`prompt`, `reply`, `file`, `command`) and the line the needle was on.
- **A Claude session's title IS its first prompt**, so a needle there matches
  as `title`, not `prompt`. A `prompt` match means a LATER prompt — which is
  the case the search exists for.
- **The snippet is bounded around the match**, not the first N characters of
  the line: a pasted log or a minified file is one enormous line and the
  needle is usually nowhere near its start.
- **A query that matches nothing restores the list** and says so. An empty
  screen looks like the sessions have gone.
- Opening a result carries the query into the timeline and points the cursor
  at the hit, so finding and reading are one move rather than two searches.

## Did the work survive

`pkg/session/survival.go`, `u` in a replay. Compares the blocks a session
recorded against the files now, and the filter keeps only the steps whose work
is gone.

- **The unit is a BLOCK, not a hunk.** One contiguous run of added lines is
  what either survived or did not; two runs separated by a context line are
  two separate pieces of work, and joining them reports both as missing the
  moment anything lands between them.
- **Bias towards saying it survived.** A verbatim match is the only certainty;
  everything else is a guess from the text, and a filter that cries wolf is
  one nobody presses twice. A MAJORITY of a block's lines still present counts
  as surviving in changed form, and lines are matched without indentation —
  moving code into a loop reindents every line of it and undoes none.
- **Work the session changed again is not lost.** Without that rule an agent
  that edits one file eight times reports seven losses: 809 of 4202 blocks on
  this machine are this case, against 29 genuinely replaced.
- **Work outside the project is not checked at all.** 140 of 235 files holding
  "lost" work were scratchpads, `/tmp` and `/dev` — a temp file is MEANT to
  vanish, and flagging it buried the 95 that were in the project. `underDir`
  matches whole segments, so `/work/app` does not take in `/work/app-old`.
- **A project directory that is gone checks nothing.** One rename would
  otherwise report every step of an old session as lost work, and a rename is
  what `relink` fixes.
- **"Nothing was checked" is not "nothing was lost."** `WorkLoss.Checked` is
  what separates them, and claiming the second when the first is true asserts
  knowledge there is none of.
- **The reason line costs a row, and `replayBodyRows` counts it.** The follow
  and the pause freeze both go through that function, so a row it does not
  know about shifts the frame by one on every pause. The test renders the same
  step with and without the line and demands the same height, which is not
  circular the way comparing against the function would be.
- **The marker is a bar, `▐` U+2590, and that took three tries.** `✗` and `⊘`
  are the timeline's error and denial and this is neither — the step SUCCEEDED
  and the work is simply not there now. `⌫` U+232B says "erased" and passed
  every rule, then drew as an unreadable blob at 13px in the recording; `▌`
  U+258C is ambiguous-width. Check a glyph in a frame, not in a table.
- **Pressing `u` with nothing to show restores the list** and says which of the
  two answers it is, for the same reason a search that matches nothing does.
- Reading files does not belong on a 30ms tick, so a `Survey` is built when the
  key is pressed and thrown away after — about 140ms on the longest session
  here. `applyReplayFilters` is the one place both replay filters are applied,
  because `startReplay` and `rebuildReplay` had each been forgotten once.

## Source markers

`sourceMarker` in `internal/ui/sessions.go` is the single place the glyph for a
source lives, because the session list and the project header have to agree.

- **The SHAPES have to differ, not just the code points**, and that is checked
  by looking at a recording. Twice now a glyph passed every rule — width 1,
  not ambiguous — and was still the same marker as its neighbour: `⬢` U+2B22
  renders as a slightly flattened circle beside `●`, and `✚` U+271A is a
  four-armed cross beside `✦`, another four-armed glyph. A unit test cannot
  see either.
- `●` claude, `✦` codex, `◈` opencode, `▪` copilot, `▴` pi, `◦` a subagent —
  five different shapes: circle, star, diamond, square, triangle.
- **A project is a DIRECTORY**, so it carries a SET of markers. One directory
  on this machine already holds OpenCode and Copilot sessions, and the demo's
  synthetic tree has one with three agents in it.
- **The tree belongs to the session being replayed.** `startReplay` reloads it
  when one is on screen; the split toggle only loads when there is none, so
  opening a different session left the sidebar marking the previous session's
  files.

## The path and branch line

`contextLine` draws the session's directory and branch under the replay's
progress bar and at the top of the tree.

- **Shorten the middle of a path, the front of a branch.** Both keep their
  tail: the last path segment is the project, and a branch's tail is what it is
  for.
- **Bound the branch before measuring the path.** Real names reach 47
  characters, and an unbounded one pushed the row past the pane however short
  the path was. When there is not room for both, the branch goes and the path
  takes the line — a minimum applied to each *without* dropping one is what
  overflowed.
- **The tree writes the row even when it is empty**, because `treeVisibleRows`
  must agree with `renderTree` exactly. A conditional row makes the count wrong
  by one and the follow scrolls to the wrong place.
- **Derive a row-count test from the chrome, never from `treeVisibleRows`.**
  The renderer uses that function too, so comparing against it is circular: the
  first version of that test passed at `height-5`, `height-6` and `height-7`
  alike. It also needs a pane SHORTER than the row list, or the "… N more" note
  never appears and the over-draw case cannot happen.
- `⎇` (U+2387) is width 1 and **not** ambiguous-width, unlike `●`, `│` and `└`
  which are already on screen here. `visibleLen` counts runes, not columns, so
  an ambiguous glyph is a layout bug waiting for the wrong terminal.

## Opening an editor

`e` hands the file under the cursor to `$VISUAL`, then `$EDITOR`, then the first
of `nvim`/`vim`/`vi`/`nano` on `PATH` (`notepad` on Windows). **An editor is
never a build or install dependency** — nothing in `editor.go` is reached until
the key is pressed.

**`e` is in place, `E` is beside.** The first version used `launchInTerminal`
for both, reusing the `c` key's ladder — and that was wrong, because the two
keys are pressed at different rates. `c` opens a CLI once per session; `e` gets
pressed on file after file while reading, and `launchInTmux` splits a NEW pane
every time with no reuse. Four files, four panes. Beside also buys less than it
looks: playback is paused before the editor opens, so there is nothing moving
to watch, and `tmux send-keys` is fire-and-forget so verbose never learns when
you quit. `editModeFor` is the single place the mapping lives — a `tea.Cmd` is
opaque, so a handler test cannot tell the two apart and the mapping is tested
directly instead.

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
- **They sample the corpus, because it only grows.** `smokeSample` takes at
  most 12 sessions per source: the heaviest, the newest, and a stride through
  the rest. The heaviest is where layout arithmetic has the most chances to be
  wrong and dropping it would make the sweep cheap by skipping the hard case;
  the newest is the one most likely to carry a shape the parser has never
  seen. A stride rather than a prefix, because transcripts are discovered in
  sorted order and the first twelve are a couple of projects rather than a
  cross-section. `VERBOSE_SMOKE_ALL=1` sweeps everything, which is what to do
  when a rendering bug is suspected and the sample is not showing it.
- **A test about that variable must clear it first** (`t.Setenv(…, "")`).
  Without that, running the suite with `VERBOSE_SMOKE_ALL=1` — the whole point
  of having it — fails the two tests that check the sampling, and nothing
  else.
- Benchmarks guard the per-frame cost: `BenchmarkReplayFrame`,
  `BenchmarkTreeFrame`. A frame must stay far under the 30ms tick.
- When a test is meant to catch a specific bug, **check it fails without the
  fix** before trusting it.

## Demo recordings

`docs/demo.tape` (VHS) produces `docs/demo.gif` and `docs/img/*.png` for the
README. It runs against a synthetic HOME built by `docs/demo-data.py` — four
stores, one per agent — never the real one: **this repository is public and a
real transcript carries project names, paths, prompts and code.**

**`CODEX_HOME` and `XDG_DATA_HOME` must be unset along with `HOME`.** Each
redirects where verbose looks for a store, so leaving one set points the
recording at the real Codex or OpenCode history on whichever machine made it.

```bash
brew install vhs
python3 docs/demo-data.py && vhs docs/demo.tape
```

`verbose` honours `$HOME`, which is what makes the synthetic tree possible.

**The tape navigates by counting rows, and nothing tells it when that count is
wrong.** Grouping subagents put an extra row under the parent, so the five
`Down`s that used to reach the session that did the work started landing on its
agent instead — the recording still produced five perfectly good screenshots of
the wrong run. Check the frames after any change to the list or the replay,
and prefer an addressed jump (`/` then a step number) over waiting a fixed
number of seconds for playback to arrive somewhere.

**Three files in `PROJECT_FILES` disagree with the transcript on purpose** —
`router.go`, `store.go` and `store_test.go` — because that disagreement is
what the `u` beat records. Making them consistent would empty the lost-work
filter and the beat would film an empty screen.

The step a screenshot wants is worth naming in a comment: `docs/img/plan.png`
is step 5 because that is the first markdown plan, and a step inserted before
it moves the target silently.
