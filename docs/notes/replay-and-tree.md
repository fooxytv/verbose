# Why the replay and the tree are shaped like this

Both views exist for one purpose: to follow work that originally scrolled past
in seconds, so you learn from it rather than reviewing a finished diff. That
purpose decided most of the design, and several obvious-looking alternatives
were rejected for reasons worth recording.

## The replay

**Steps, not events.** `BuildReplay` keeps prompts, the agent's own narration
and every operation, and drops the bookkeeping — hook progress, turn timings,
bash keepalives. Thinking is dropped too, because extended thinking is signed
but never retained: every thinking block on disk has an empty body.

**It says what happened, not why.** `ReplayStep.Explanation` exists and renders
under a "Why" heading, and nothing fills it. The reasoning genuinely is not in
the transcript, so generating it needs a model call. That layer was deliberately
deferred, not forgotten.

**Code is typed out, the viewport follows the cursor.** The first version typed
correctly and kept the window at the top of the diff, so the code arrived
off-screen. Following the cursor is the whole feature; without it there is no
point.

**Command output is never typed.** A diff rewards following keystroke by
keystroke; nine kilobytes of log output does not.

**Pausing or scrolling reveals the whole step.** Stopping to look means you want
to read it, not watch it.

## The tree

**Structure from disk, marks from the transcript.** Two different kinds of
truth, which is why the header says so. For an old session the tree is today's
directory shape with old marks painted on; files the session touched that have
since gone are grafted back and marked `(gone)`.

**Driven by the replay position.** The tree has no notion of "where we are" of
its own — it reads `replayIndex`. That is what makes the split cheap: no second
cursor, no focus model, no state to keep in sync. A file is dim until the step
that touched it, so the tree reads as a timeline of the project.

**It scrolls itself to the change.** A project tree is far longer than the pane
showing it (122 rows into 22). Measured before the fix: of the 90 steps that lit
a file, 74 lit one below the fold. Following is not a nicety.

**Ties must break deterministically.** `treeFocusRow` walks a map, and one shell
command can touch several files at the same event index. "Whichever came last"
returns a different row every frame and jitters the tree thirty times a second.
A change outranks a read, then the earlier row wins.

## The split

`T` inside a replay puts the tree beside the code. **Every key stays with the
replay; the tree follows.** No focus to switch, no key meaning one thing on the
left and another on the right. The tree gets its own bare-character bindings
(`]` `[` `}` `{`) — see CLAUDE.md on why not ctrl+arrow.

It needs 100 columns and refuses below that rather than showing two unreadable
halves.

## What was deliberately not built

- **A live pair-programming mode.** Verbose reads transcripts after the fact; it
  can never let you steer. [AI Pair](https://github.com/faiface/ai-pair) does
  that properly, as a VS Code extension driving your agent over MCP.
- **Running a session inside verbose.** The continue panel (`c`) hands the reply
  to the CLI in a pane beside verbose instead. Running it in-process would mean
  carrying a PTY and an ANSI parser to end up with a worse tmux.
- **Cross-session diffs per file.** Only 23 of 234 files were touched by more
  than one session, and hunks cannot reconstruct a file's past state, so a true
  "end of session A vs end of session B" needs git rather than transcripts.
