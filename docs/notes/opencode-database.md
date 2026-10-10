# OpenCode's database

*Verified against OpenCode v1.18.29 (September 2026) and still matching
`pkg/session/opencode.go` as of October 2026. Dated on purpose — this is a
record of a third-party schema, not a contract.*

OpenCode stores **one global SQLite database** at
`~/.local/share/opencode/opencode.db`, covering every project. There is no
per-project `.opencode/opencode.db`; that was an older `opencode-ai/opencode`
layout, and code written against it silently finds nothing at all. Verbose also
checks `~/Library/Application Support/opencode/opencode.db` and accepts
`--opencode <path>`.

## Tables that matter

| Table | Holds |
|---|---|
| `session` | `id`, `project_id`, `parent_id` (non-null means a subagent session), `directory` (the cwd), `title`, `model` (JSON `{"id","providerID"}`), `agent`, `cost`, `tokens_input/output/cache_read/cache_write`, `time_created/updated`. **Times are Unix milliseconds.** |
| `message` | `id`, `session_id`, `data` JSON — the envelope only (role, model, tokens). **No content.** |
| `part` | `id`, `message_id`, `data` JSON. **All content lives here.** Types: `text`, `reasoning`, `tool`, `step-start`, `step-finish`, `patch`, `agent`. |
| `project` | `id`, `worktree`. The `global` project has worktree `/`. |
| `todo` | `session_id`, `content`, `status`, `position`. |

## Ordering

Part ids are monotonic, so `ORDER BY message.time_created, part.id` reconstructs
the transcript exactly — verified as zero mismatches against `time_created`
ordering.

## Tool parts carry both halves

A `tool` part holds the call *and* its result in `state`: `status`
(`completed` / `error`), `input`, `output`, `error`, `metadata`,
`time.start/end`.

That already matches verbose's folded one-event-per-operation model, so the
correlation step Claude Code transcripts need is not required here.

- `metadata.diff` on an editing tool is a real unified diff. Multi-file patches
  separate files with `Index: <path>` headers.
- `metadata.sessionId` on a `task` call names the child session it spawned.

## Two traps

- **`cost` is 0 for every session** in practice. Do not substitute a
  token-based estimate: Claude pricing applied to a gpt-5.x or local Ollama
  model fabricates numbers. Verbose reports OpenCode cost as recorded, and
  marks Claude Code costs as `estimated`.
- **Polling, not fsnotify.** SQLite does not notify usefully, so verbose polls
  the databases every 5 seconds.

Opening the live database read-only while OpenCode is running works with
`modernc.org/sqlite` (no CGo):

```
file:<path>?mode=ro&_pragma=busy_timeout(5000)
```

## CLI

Resume is `opencode --session <id>` — **not** `--resume`. Fork is
`--session <id> --fork`. An opening message goes through `--prompt`.
