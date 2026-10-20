# Keybindings

Every key, by view. The footer shows the ones that fit; this is the whole list.


| Key | Action |
|-----|--------|
| `j` / `Down` | Move down |
| `k` / `Up` | Move up |
| `Enter` / `Right` / `Space` | Open session / expand event |
| `Esc` / `Left` | Go back |
| `g` / `Home` | Jump to top |
| `G` / `End` | Jump to bottom |
| `PgUp` / `PgDn` | Page up / down |
| `/` | Search **every** session — prompts, replies, files and commands, not just titles |
| `d` | Delete session (asks to confirm) |
| `s` | Toggle session summary |
| `R` | Replay the session step by step, from wherever the cursor is |
| `T` | Project tree, marked with what the session changed (works inside a replay too) |
| `f` | Toggle auto-follow (timeline view) |
| `r` | Refresh session list |
| `q` / `Ctrl+C` | Quit |

In the replay view:

| Key | Action |
|-----|--------|
| `Space` | Play / pause — a pause holds the line you stopped on, and play carries on from there |
| `Right` | Finish typing this step, or move to the next |
| `Left` | Previous step |
| `Up` / `Down` | Scroll — takes manual control and pauses |
| `+` / `-` | Faster / slower (0.5s to 15s per step) |
| `/` | Go to a step by number, or find one — type `24` or `connection refused` |
| `n` / `N` | Next / previous match (`n` steps forward when nothing is being searched) |
| `a` / `A` | Switch to the next / previous subagent of this run, without leaving the replay |
| `Tab` | Code only — just the steps that wrote something |
| `u` | Lost work only — just the steps whose work is no longer in the files |
| `f` | Follow a running session (on by default) |
| `0` | Back to the first step |
| `t` | Jump to this point in the timeline |
| `↑`/`↓`, `j`/`k` | Move the tree selection (full-screen tree) |
| `]` / `[` | Move the tree selection (with the sidebar open) |
| `}` / `{` | Open / close a directory in the sidebar |
| `d` | Open the selected file's diff (`d` or `Esc` closes) |
| `e` | Open the selected file in `$EDITOR`, at the line that changed |
| `E` | The same, but beside verbose — a tmux pane or a new tab |
| `c` | Continue the session: see its last output and reply |
| `Ctrl`+`f` | Hand the tree's follow back to the replay |
| `Esc` | Back |

Mouse scroll works in every view. With the tree beside the replay, the pointer
decides which pane moves: over the tree it moves the selection, over the code it
scrolls the step.

The footer's right-hand side shows how long ago the open session was written,
the time now, and the version — so a live session visibly ticking apart from
one that stopped four minutes ago. On a narrow terminal both halves give way
gracefully: keybindings drop from the least useful end (never `q`), and the
status drops the version before the clock and the clock before the session age.

---

[← back to the README](../README.md)
