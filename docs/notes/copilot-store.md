# Copilot's store

*Verified against GitHub Copilot CLI 1.0.98 in October 2026 — six sessions, two
with an event stream — and matching `pkg/session/copilot.go`. Dated on purpose:
this is a record of a third-party layout, not a contract.*

Copilot splits a session across a database and a file, and they do not agree:

| Path | Holds |
|---|---|
| `~/.copilot/session-store.db` | `sessions` — `id`, `cwd`, `repository`, `host_type`, `branch`, `summary`, `created_at`, `updated_at` (ISO 8601 strings). Also `turns` — `user_message` and `assistant_response` per turn, and nothing in between. |
| `~/.copilot/session-state/<id>/events.jsonl` | The real transcript: one JSON object per line, `{type, timestamp, id, parentId, data}`. Also `rewind-file-snapshots/` and `files/`, neither read yet. |

**The `turns` table lags.** The live session on this machine had 25 events in
its stream and **zero** rows in `turns`. So the event stream wins wherever
there is one. `turns` is the fallback for a run hosted by GitHub rather than by
this machine (`host_type: "github"`), which never writes a state directory at
all — four of the six sessions here.

## Event types worth reading

Of the 25 types seen, four carry anything:

| Type | Becomes |
|---|---|
| `session.start` | the cwd, from `data.context.cwd` |
| `user.message` | the prompt, from `data.content` |
| `assistant.message` | the reply, from `data.content`, with `data.model` |
| `session.usage_record` | tokens and cost, from `data.usage` |

The rest are machinery: turn boundaries, model-call bookkeeping, permission
notices, mode changes.

**`model.response` and `model.message` are NOT the reply.** They look like it —
same shape, `role: "assistant"` — and they are the side model call that writes
the session's own title. Their content is literally
`<session-title>Check Loaded MCP Server</session-title>`, which is where the
database's `summary` comes from. Reading them puts markup on screen where the
answer belongs.

**`transformedContent` is not what the user typed.** Every prompt is also
stored with a `<current_datetime>` preamble stapled on for the model. Showing
that puts machinery in front of every prompt.

**A failed turn raises two events.** `session.error` (with `errorType`,
`message` and `statusCode` — all rate limits here) *and* `model.turn_failed`.
Counting both reported five failures for three failed prompts, so only
`session.error` is counted.

**`session.usage_record` is the richest cost record of any source.** A full
breakdown *and* a price: `inputTokens`, `outputTokens`, `cacheReadTokens`,
`cacheWriteTokens`, `reasoningTokens`, `cost`, `duration`. Claude Code records
tokens and leaves cost to be estimated; Codex records one number and no cost;
OpenCode records a cost. Copilot records both.

## Metadata traps

**`summary` can be a whole issue body.** A GitHub-hosted run gets the ticket
text rather than a generated title, newlines and all, and a title with a
newline in it costs the session list a row it did not count. First line only.

**Half the turns recorded no reply.** Five of the seven `turns` rows here have
an empty `assistant_response`, so an empty one is skipped rather than drawn as
a blank assistant message.

## What is not read

**Tool calls.** Not one session on this machine has ever made one — zero events
matching `tool`, `file`, `patch` or `command` across both streams — so there is
nothing to map and nothing to test a mapping against. The CLI plainly supports
them, and the event names are not recoverable from the binary either: it is a
compressed bundle, and the types that *are* known (`session.usage_record`,
`assistant.turn_start`) do not appear in `strings` output. This needs one real
tool-using session before it can be written honestly.

**Deleting.** `copilot sessions` offers only `import`. `DeleteSession` refuses.

## Resuming

`copilot --resume <id>` to carry on, and `--interactive "<reply>"` to send the
first message with it. **Not `--prompt`**: that runs the reply non-interactively
and exits, which is the opposite of continuing a session. There is no bare
prompt argument — the usage line is `copilot [OPTIONS] [COMMAND]`.
