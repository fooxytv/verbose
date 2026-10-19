# Codex's databases

*Verified against the Codex home on one machine in October 2026 — `state_5`,
`thread_history_1`, 10 threads, 212 items — and matching
`pkg/session/codex.go`. Dated on purpose: this is a record of a third-party
schema, not a contract.*

Codex splits a thread across **two** SQLite databases under `~/.codex`
(`$CODEX_HOME` wins when set), and neither is usable on its own:

| File | Holds |
|---|---|
| `state_N.sqlite` | `threads` — one row per session: `cwd`, `title`, `name`, `model`, `model_provider`, `git_branch`, `tokens_used`, `created_at`/`updated_at` (+ `_ms`), `source`, `thread_source`, `rollout_path`, `archived`. Also `thread_spawn_edges` (parent → child) and `projects`/`project_roots`, both empty here. |
| `thread_history_N.sqlite` | `thread_items` — the content, as `item_json` tagged by `item_type`, ordered by `rollout_ordinal`. Also `thread_turns` with a real `duration_ms` per turn. |

**`N` is a schema generation, not a shard.** The pair with the highest number
is the live one; `state_4.sqlite` sitting beside `state_5.sqlite` is the
previous schema and reading it would resurrect threads Codex has migrated away
from. `CodexDBs` globs and takes the highest, and treats a home with only one
of the two as unreadable rather than half-loading it.

## Item types

Counted over 212 real items:

| `item_type` | n | Becomes |
|---|---|---|
| `agentMessage` | 80 | `EventText` from `text` |
| `reasoning` | 49 | `EventThinking` from `summary` — see below |
| `userMessage` | 31 | `EventUserPrompt` from `content[].text` |
| `commandExecution` | 23 | a `Bash` call: `command`, `cwd`, `aggregatedOutput`, `exitCode`, `durationMs` |
| `fileChange` | 19 | one `Write` or `Edit` per entry in `changes[]` |
| `mcpToolCall` | 7 | a call named `mcp__<server>__<tool>`, as Claude Code names them |
| `imageGeneration` | 3 | a call carrying `revisedPrompt` |

**Reasoning is not recoverable.** `content` is an empty array on every one of
the 49 items; `summary` holds one bold headline (`**Planning the change**`) on
19 of them and nothing on the rest. That is more than a Claude Code transcript
keeps, which is nothing at all, but it is a headline and not the thinking — an
item with neither is dropped rather than drawn as a blank row.

**A `fileChange` is Claude Code's split in one field.** Each entry in
`changes[]` has a `path`, a `kind.type` and a `diff`, and the diff means two
different things: for `add` it is the file's whole content (there is no
before-state), for `update` it is unified `@@` hunks with no `---`/`+++`
headers. So an add is handed over as a `Write` whose input carries `content`,
which is what `stepCode` types out in the replay, and an update as an `Edit`
with a real `StructuredPatch`. 16 adds and 7 updates here.

**A failed command is only knowable from `status`.** It writes its reason into
`aggregatedOutput`, the same field a successful one uses — 14 of the 23
commands on disk failed.

## Metadata traps

**`name` is the title; `title` is the opening message.** `name` is the label
Codex generates ("Create QR code PNG"), while `title` is the first user message
verbatim — on real threads that is a 4,000-character paste or an XML
`<realtime_delegation>` wrapper. Prefer `name`, fall back to the *first line*
of `title`, then to `deriveTitle`.

**A threads row exists before anything happens in it.** Four of the ten here
have no items at all: abandoned voice chats and forks, with `tokens_used` 0. A
row with no items is not a session.

**`tokens_used` is one number.** There is no input/output/cache split and no
cost, so `SessionInfo.TokensTotal` carries it and `TotalTokens()` falls back to
it. Putting a total in the input column would make the overview draw a bar
claiming it was all input, and `costBasis` is `none` rather than an estimate
from a price table that has no Codex models in it.

**`source` is overloaded.** Usually a plain string (`"vscode"`), but on a
thread Codex owns as a subagent it is JSON: `{"subagent":{"other":"guardian"}}`.
That is what sets `IsAgent` and `AgentType`. `thread_source` separately records
how a thread was *started* (`user`, `voice_chat`, `chatgpt_handoff`,
`guardian_review`, `avatar_quick_chat`) — which is not an agent type, and
reporting it as one made every voice chat look like a subagent run.

**`thread_spawn_edges` was empty here**, so the parent link is implemented from
the schema and tested against a fixture, not against real data. Verbose only
sets `ParentSessionID` (prefixed `cx-`, or grouping cannot match it against the
list); it does **not** splice a child's events into the parent the way the
Claude Code and OpenCode readers do, because there is no real pair on disk to
verify that against.

## What is not read

**The rollout files.** `sessions/<y>/<m>/<d>/rollout-*.jsonl.zst` and
`archived_sessions/` hold the same conversation as zstd-compressed JSONL, and
`threads.rollout_path` points at it. Every thread with any content had it in
`thread_items` too, so nothing needs decompressing — a pure-Go zstd reader
would be the cost of covering a case that did not arise.

**Deletion.** Codex's CLI has no delete, and the thread lives across two
databases Codex holds open. `DeleteSession` refuses rather than editing them.

## Resuming

`codex resume <SESSION_ID> [PROMPT]` — the id is a bare argument, not a flag,
and an optional prompt follows it. Verified against `codex resume --help` on
the installed CLI.
