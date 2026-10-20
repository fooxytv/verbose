# Replaying a session

`R` on any session plays it back one step at a time, at reading speed.


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
once, because stopping to look means you want to read it, not watch it. Play
carries on from the character it stopped at, not from the top of the step.

**The replay starts where you are.** `R` opens on the step the timeline cursor
is pointing at, so a search hit you followed into a session, or a row you
scrolled to, is the step the replay begins on rather than something to fast
forward to. Leaving a replay with `Esc` or `t` leaves that cursor on the step
that was showing, which is what makes pausing to look at something safe:
coming back with `R` carries on from there instead of starting the session
again. Opening a session and pressing `R` straight away still starts at step 1
— the cursor is tracking the end of a possibly-live transcript then, not
pointing at anything — and `0` is always the way back to the beginning.

## Reading a plan

A `PLAN` step is the agent's own prose, and an agent writes markdown. Across one
machine's 2327 assistant text blocks, 53% carry inline code, 34% bold, 17% a
heading, 15% a fenced code block, 11% bullets and 10% a table — and a third of
them are more than one line.

Those blocks are **rendered**: headings in colour, bullets and numbered lists
with their text hanging under themselves, tables in aligned columns, fenced code
syntax highlighted the same way a diff is, and `code`, **bold** and *emphasis*
shown as themselves rather than as asterisks and backticks.

Before this they were flattened. Every newline was deleted, so a heading, the
list under it and a table all ran together into one unbroken paragraph.

Markdown's own rules apply: lines broken in the source are re-flowed into one
paragraph, so a plan wraps to your terminal rather than to the width the agent
happened to write it at. Shell text is left alone — `rm *.go *.md`,
`snake_case_names` and `2*3*4` are not emphasis, and a plan that reads like a
command is still one you can retype.

## Searching a replay

Sessions get long — the replays here reach four figures of steps — and stepping
to the operation you remember is scrolling, not reading. `/` takes a needle as
well as a step number: type `24` to go to step 24, or type anything that is not
a number to find it. `n` and `N` move between the matches, wrapping at both
ends, and the header says which match is on screen.

**It searches more than the step shows.** A headline is a sentence verbose
wrote — "Ran go test ./..." — but what a reader remembers is the command, the
file, or what the command *printed*, and the output is the only place an
outcome lives. So the needle is matched against the step's headline and
explanation, the code it wrote, the file it touched, the prompt or reply behind
it, the command, its output, and the error string a failed shell command leaves
instead of a result. Searching `connection refused` or `exit code 127` finds
the step that failed.

A search that matches nothing leaves the replay where it is and says so,
rather than clearing the search that was working. Digits are always a step
number, which is the one thing the prompt cannot do both of — the number is by
far the likelier intent.

## Finding the code

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
| `bashEditDiff` | A **shell command** that changed files — a real diff, one per file, since one command can change several |
| The `Write` tool's `content` input | A **new** file — Claude Code records an *empty* patch, since there is no "before" |
| A shell heredoc body | A shell write that recorded no diff |

On one session those last two rows took the visible-code steps from 1 to 173.
Machine-wide, from 6.4% of steps to 21.3%.

`bashEditDiff` is the one most easily missed: a shell command **does** record a
real diff, in a different field from the edit tools. Reading it took the steps
carrying a usable diff from 411 to 746 — more recorded diffs came from the shell
than from `Edit` and `Write` together.

A heredoc piped to an interpreter (`python3 - <<'PY'`) is shown as code but
labelled without a filename, because it writes no file — it is the script that
did the editing, not the result.

Streamed code is **syntax highlighted**, with the language taken from the
filename, or analysed from the content when a heredoc has no target. The colour
depth matches the terminal: truecolor where there is truecolor, 256 colours
otherwise. Highlighted files are cached, so a step costs about a millisecond to
re-render regardless of size.

## Did the work survive?

A transcript records an intent at a moment. Nothing in it says whether the code
it put in a file is still there, so an old replay shows every step as equally
alive: an edit that is still load-bearing reads exactly like one a later
session quietly replaced.

Press `u` to see only the steps whose work is gone. The header says `lost work`,
and each step carries a line naming what it lost:

```
▐ Not in internal/ui/replay.go any more — something replaced it after this session
▐ Not on disk any more — environments/dev.tfvars is gone, so this work is too
```

The unit of comparison is a **block** — one contiguous run of added lines —
because that is what either survived or did not. A whole hunk is the wrong
unit: two runs separated by a context line are two separate pieces of work.

Four rules keep the answer worth trusting, and each of them was measured
against one machine's history rather than guessed at:

| Rule | Why |
|---|---|
| A majority of a block's lines still present counts as **surviving**, in changed form | A later edit to one line inside a ten-line block has not undone the block. Lines are compared without their indentation, so moving code into a loop is not a loss either |
| Work the session itself changed again afterwards is **not** lost | An agent that edits one file eight times would otherwise report seven losses every time. 809 of 4202 blocks fall here |
| Work outside the project directory is **not checked** | 140 of 235 files holding "lost" work were scratchpads, `/tmp` and `/dev`. A temp file is *meant* to vanish, and flagging it buries the ones in the project |
| A project directory that is gone checks **nothing** | A removed or renamed folder is one fact about the session, not a loss in every step of it — and a rename is what [`verbose relink`](#after-renaming-or-moving-a-folder) is for |

What is left is sharp: across 10,385 steps on the machine this was built on,
94 steps in 12 of 28 sessions had work that is no longer in the files.

Pressing `u` with nothing to show says so and leaves the list alone, rather
than opening an empty screen. "Nothing was checked" and "nothing was lost" are
different answers, and it does not give the second when the first is true.

The comparison reads the files, so it happens when you press the key — never
while drawing a frame. It takes about 140ms on the longest session here, and
it is a snapshot: nothing watches the files afterwards.

## Subagents in a replay

A parent's replay **already contains** its subagents' turns, spliced in at the
call that launched them and marked `│ subagent`. On one real session that was
270 of 347 steps, and 193 of the 194 steps that touched a file — the agents did
nearly all the work.

What a parent's timeline cannot show is each run as its own thing: its own
cost, its own tokens, its own step numbers. **`a` swaps which session the
replay is playing** — parent, each agent in turn, back to the parent — without
leaving the view. `A` goes the other way. The header says which you are on
(`◦ 1 of 2 · scout`), and coming back returns you to the step you left rather
than to the beginning.

The tree follows the swap, so it marks the files that session touched rather
than keeping the previous one's.

## Following a live session

Replay follows a session that is still being written, and so does the tree: when
the transcript grows its steps are rebuilt and the tree re-reads both the
session's activity and the project directory, so a file the agent has just
created appears. The step count updates on
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

## Where the work happened

Both the replay and the tree carry a line with the session's directory and git
branch. Every transcript records both; neither was on screen while reading one.

Long paths give up their **middle**, not their end — `~/…/team-skills-matrix-poc`
— because the last segment is the project and the first says which checkout,
and those are what tell two clones of the same repository apart. A long branch
gives up its front for the same reason: `…dev-mdevops-pool` says more than
`feature/allow-jf…`. Nothing is shortened while it all fits.

A session with no recorded branch shows none, and a detached checkout says
`detached` rather than printing `HEAD` as though it were a branch name.

---

[← back to the README](../README.md)
