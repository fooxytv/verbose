# pi's sessions

*Verified against the pi home on one machine in October 2026 — two sessions,
310 entries — and matching `pkg/session/pi.go`. Dated on purpose: this is a
record of a third-party format, not a contract.*

pi keeps **one JSONL file per session**, at
`~/.pi/agent/sessions/<encoded cwd>/<timestamp>_<uuid>.jsonl` — the closest
layout to Claude Code's of any source here, and read the same way.

The directory name encodes the working directory (`--Users-me-work-repo--`),
and like Claude Code's it cannot tell a path separator from a hyphen. It is
not decoded: the `session` entry carries the real `cwd`.

## Entries

Every line is `{type, id, parentId, timestamp}` plus a payload. The entries
chain through `parentId`, for pi's own branching; file order is time order and
is what verbose reads.

| `type` | Carries |
|---|---|
| `session` | `version`, `id`, `cwd` — the header |
| `model_change` | `provider`, `modelId`; a session can change model part way through |
| `thinking_level_change` | the reasoning budget; nothing to show |
| `message` | everything else, under `message` |

A `message` has a `role`, and the role decides the shape:

| `role` | n | Shape |
|---|---|---|
| `assistant` | 133 | `content[]` of `text`, `thinking` and `toolCall` parts, plus `model`, `provider`, `usage`, `stopReason` |
| `toolResult` | 136 | `toolCallId`, `toolName`, `content[]`, `isError`, sometimes `details` |
| `user` | 32 | `content[]` of `text` parts |
| `bashExecution` | 1 | `command`, `output`, `exitCode` — run from pi's own shell, not by the model |

Timestamps on a message are Unix **milliseconds**; on the entry around it they
are ISO 8601 strings. Both appear on the same line.

## It keeps its reasoning

**47 of 47 thinking blocks have their full text.** Claude Code's are signed but
always empty, Codex keeps a one-line summary on 39% of them and OpenCode has
none. pi is the only source on this machine where a replay can say *why*, and
it shows up as a `pi's reasoning` step.

## The diff is the one pi displayed

An `edit` result carries `details.diff`, and it is **not** a unified diff:
there are no `@@` headers, each line is numbered, and runs are separated by an
elision.

```
     ...
  82   depends_on = [module.dmlz_vnet]
- 86 # VNet Data Gateway subnet
+ 86 # Data gateway subnet
-100     vnet_data_gateway = {
```

**The line number is right-aligned in a field whose width follows the file**,
so a three-digit line has *no* space after the marker (`-100`) while a
two-digit one has one (`- 86`). A pattern that expects a separator silently
drops every line from 100 onward — which in a real file is most of them.
`piDiffLine` is `^([-+ ]) *(\d+) (.*)$`: zero or more spaces, and the single
space after the number is the separator, so the content keeps its indentation.

A run of consecutive lines becomes one hunk and the elision ends it. The first
line number in a run is its start on both sides, which is exact for the first
change in a file and is what the gutter needs.

## Tool calls

`toolCall` parts carry `{id, name, arguments}`, and the result arrives later as
its own message keyed by `toolCallId` — so verbose folds the result back into
the call and emits **one event per operation**, the shape every other view
expects. The gap between the two timestamps is the call's duration.

| pi | arguments | becomes |
|---|---|---|
| `read` | `path` | `Read` |
| `bash` | `command` | `Bash` |
| `write` | `path`, `content` | `Write` — content on the call, so the replay types it |
| `edit` | `path`, `oldText`, `newText` | `Edit` — hunks from `details.diff` |

`path` → `file_path`, `oldText` → `old_string`, `newText` → `new_string`, so
the existing renderers apply unchanged.

## Cost

`usage` on each assistant message is a full breakdown **and** a price:
`{input, output, cacheRead, cacheWrite, totalTokens, cost{...,total}}`. The
cost is zero when the model came through a subscription (Copilot-provided
models here) and real otherwise — one session on this machine totals $8.15.

## Resuming and deleting

`pi --session <id>` resumes, and messages are bare arguments, so a reply goes
on the end: `pi --session <id> "<reply>"`. `--continue` takes the most recent.

Deleting works like Claude Code's, because the shape is the same: one file,
moved to the trash. Nothing special is needed.
