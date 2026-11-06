# The store and its watcher

Three bugs lived here at once, all silent. If you change `Store.Watch` or
`Store.reload` in `pkg/session/store.go`, these are what the tests in
`splice_test.go` are guarding.

## A fresh parse drops spliced subagent turns

`ParseSessionFile` returns only the events in *that* file. A background
subagent's turns live in their own file and are spliced into the parent by
`linkSubagents`.

So re-parsing a parent after an append threw away every subagent turn that had
been folded in — measurably two to zero in the fixture — and re-parsing a
subagent left its parent holding a stale copy.

`reload` now rebuilds the affected parent from disk and re-splices all of its
agents. Bounded (one parent and its children) and idempotent, because
`alreadySpliced` prevents duplication.

## One debounce timer was shared by every file

A write to transcript B called `Stop()` on the timer pending for transcript A,
so A never reloaded. With two sessions being written at once — or a subagent
writing alongside its parent, which is the normal case — only the last file
touched ever reloaded.

It is a `map[string]*time.Timer` now.

## Sustained writing starved the reload

Every write reset the 500ms timer, so an agent writing faster than that could
postpone its own reload indefinitely. `watchMaxDelay` (2s) forces a reload once
a file has been dirty that long.

## A reload re-parsed the whole file

Every write to a live transcript re-parsed it from the first byte. A long
session's file reaches hundreds of megabytes — one here was 380MB, about five
seconds of CPU to parse — and `watchMaxDelay` reloads a busy file every two
seconds, in every process holding a `Store` (each verbose window, and pidash's
reporter). A live session kept a core busy in each of them.

`parseLive` (`live.go`) keeps the parse of each recently written transcript and
reads only what was appended: about 80ms for that file instead of four seconds.
The traps:

- **One parser, not two.** `ParseSessionFile` is `transcriptParser` fed the
  whole file, so a full parse and an incremental one cannot disagree.
  `TestLiveParseMatchesFullParseAsTheFileGrows` grows a transcript a few bytes
  at a time and compares after every read; the real-transcript test does the
  same with files on the machine.
- **Only whole lines are consumed.** A last line without its newline may still
  be being written, and is taken only if it already parses. A fragment never
  parses, so the next read sees it whole.
- **What is handed out is a copy.** Pairing calls with results, relinking and
  redaction all write into events in place — the tool input map, the
  `*ToolResult`. Done to the kept parse, the next reload would do them again,
  and relinking is not idempotent: a folder moved into itself relinks
  `/repo/a.go` to `/repo/moved/a.go`, then `/repo/moved/moved/a.go`.
  `cloneEvents` copies everything those passes touch, and keeps the one
  `*ToolResult` an entry's blocks share as one.
- **A shorter file has been replaced** and is read from the start. Claude Code
  only appends.
- **Only recent transcripts keep a parse** (`liveIdle`, 15 minutes), so the
  whole history is not held in memory twice. A session resumed after that
  pays one full parse, which is what every write used to cost.
- A subagent writing rebuilds its parent, and that parent rebuild is
  incremental too, which matters because the parent is usually the large one.

## What was fine

The watcher itself. It handles `fsnotify.Write|Create` on `*.jsonl`, so appends
to an existing transcript do fire — that was never the problem.

## Index stability

`linkSubagents` splices a subagent's turns into the **middle** of its parent's
timeline, so one new background agent shifts every index after it. Anything
holding a position across a reload must re-anchor on `Event.UUID`, captured
*before* the session pointer is swapped.

Claude Code events always carry a UUID; OpenCode events never do (1068 of 1068
blanks were OpenCode), but OpenCode ordering is fixed at parse time, so the
index is a safe fallback there. `Model.rebuildReplay` does exactly this.
