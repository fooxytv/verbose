# How it works

Where the data comes from and what verbose does with it.


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

Codex splits a thread across **two** SQLite databases under `~/.codex`, and
neither is usable alone: `state_N.sqlite` holds a row per thread — the cwd, the
model, the git branch, the token total, the generated title — and
`thread_history_N.sqlite` holds the content as typed items and the per-turn
timing. The `N` is a schema generation, so both are found by glob and the
newest pair wins. Items map onto the same vocabulary as everything else: a
`commandExecution` is shown as a `Bash` call, a `fileChange` as one edit per
file it touched, an `mcpToolCall` as `mcp__<server>__<tool>`. A thread's
reasoning is **not** recoverable — the content array is empty on every item on
disk, and only some keep a one-line summary.

pi keeps one JSONL file per session, the closest layout to Claude Code's, and
is the only source that **keeps its reasoning**: every thinking block has its
full text, where Claude Code's are signed but always empty and Codex keeps a
one-line summary at best. Its edit diffs are the ones pi displayed rather than
unified ones — numbered lines, no `@@` headers — and are parsed back into
hunks so the diff view works the same.

Copilot splits a session the other way about: `session-store.db` holds the
metadata and one row per turn, while `session-state/<id>/events.jsonl` holds
the conversation as a typed event stream. The database's `turns` table lags —
a live session can hold a whole conversation in its events file while `turns`
still reports nothing — so the stream wins wherever there is one, and `turns`
is the fallback for a run hosted by GitHub, which never writes a state
directory. Copilot is the only source here that records both a full token
breakdown and a real price, in a `session.usage_record` per turn.

OpenCode instead keeps every project's sessions in one SQLite database, with a row per
message and a row per content part. Verbose reads it read-only, reassembles each session's
transcript, and polls for changes. Tool calls are mapped onto the same vocabulary as Claude
Code's — an OpenCode `read` is shown as a `Read`, `apply_patch` as the file edits it
performed — so both kinds of session share one timeline, one set of filters, and one diff
viewer. Subagent runs, which OpenCode records as separate child sessions, are folded into
the parent transcript at the point they were launched.

## Pointing at a store somewhere else

```bash
verbose -opencode /path/to/opencode.db
verbose -codex /path/to/codex-home
verbose -copilot /path/to/copilot-home
verbose -pi /path/to/pi-home
```

## JSON output

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

Each session carries a `source` (`claude`, `codex`, `copilot`, `opencode` or
`pi`)
and a `costBasis` saying where its cost came from: `recorded` for OpenCode and
Copilot, which both store real spend, `estimated` for Claude Code, which stores
only tokens, and `none` for Codex, which stores neither a cost nor a token
breakdown to derive one from. `totalTokens` is always filled: the breakdown
summed, or the single number Codex reports. A subagent run also carries
`isAgent`, its `agentType`, and the `parentSessionId` it belongs to.

A project carries `sources`, every tool whose sessions are in that directory.

In the session list Claude Code sessions are marked `●`, Codex `✦`, OpenCode
`◈`, Copilot `▪` and pi `▴`.

---

[← back to the README](../README.md)
