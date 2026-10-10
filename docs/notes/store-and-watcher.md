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
