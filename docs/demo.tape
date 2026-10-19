# VHS tape for the README recordings:  vhs docs/demo.tape
#
# Runs against a synthetic HOME built by docs/demo-data.py — five stores, one
# per agent verbose reads — and never the real one. This repository is public,
# and a real transcript carries project names, file paths, prompts and code
# that have no business in a demo.

Output docs/demo.gif

Set Shell bash
Set FontSize 13
Set Width 1300
Set Height 620
Set Padding 12
Set Theme "GitHub Dark"
Set TypingSpeed 60ms

# Point verbose at the synthetic tree, off camera.
#
# TMUX and ITERM_SESSION_ID have to go with it. vhs inherits the environment it
# was launched from, and `e` asks launchInTerminal where to put the editor: with
# either of those set it opens a pane in the REAL terminal running the
# recording, so the GIF shows a key that appears to do nothing. Unset, it falls
# through to in-place and nvim takes over the recorded screen, which is what
# should be on camera.
#
# HOME is already redirected, so nvim finds the throwaway config under
# /tmp/devdemo and never loads a personal one.
Hide
# CODEX_HOME and XDG_DATA_HOME go with HOME. Each one overrides where verbose
# looks for a store, so leaving them set would point the recording at the real
# Codex or OpenCode history on whichever machine made it.
Type "export HOME=/tmp/devdemo EDITOR=nvim PS1='$ ' && unset TMUX ITERM_SESSION_ID TERM_PROGRAM CODEX_HOME XDG_DATA_HOME && clear"
Enter
Sleep 500ms
Show

Type "verbose"
Sleep 400ms
Enter
Sleep 2.5s
Screenshot docs/img/sessions.png

# The marker in the first column says which agent did the work: `●` Claude
# Code, `✦` Codex, `◈` OpenCode, `▪` Copilot, `▴` pi, `◦` a subagent under its
# parent. All five are in this list on purpose — that is the thing to see.
#
# The shapes have to differ at THIS size, not just as code points: the first
# Copilot marker was a hexagon, which renders beside ● as a slightly flattened
# circle and was the same marker to anyone reading the frame.

# A project is a DIRECTORY, so more than one agent can work in it. Row 3 is in
# the webhooks directory, which three of them have now touched, and the project
# view says so above its stats.
Down
Sleep 500ms
Down
Sleep 700ms
Type "p"
Sleep 2.5s
Screenshot docs/img/project.png
Sleep 2s
Escape
Sleep 800ms

# On to the session that did the work: row 5, the one that is 2h38m old. Two
# more downs from row 3.
#
# The count is FOUR from the top, not five. The subagent sits directly under
# its parent in the list (`└─◦`), so a fifth lands on the agent's own run —
# five was right before grouping existed and silently wrong after it.
Down
Sleep 500ms
Down
Sleep 900ms
Enter
Sleep 2s

# Replay it, one step at a time.
Type "R"
Sleep 1.5s

# Step 5 is a plan, and a plan is the agent's own markdown: a heading, a table,
# a numbered list, inline code and bold, rendered rather than flattened into
# one paragraph.
#
# Jumped to by number rather than waited for, so the frame does not depend on
# playback timing — and then `0` puts the replay back at step 1, so everything
# after this point is recorded exactly as it was before.
Type "/"
Sleep 400ms
Type "5"
Sleep 500ms
Enter
Sleep 3s
Screenshot docs/img/plan.png
Sleep 2s
Type "0"
Sleep 800ms

Space
Sleep 7s
Screenshot docs/img/replay.png
Sleep 3s

# The tree beside it, lighting up as files are written.
Type "T"
Sleep 5s
Screenshot docs/img/tree-split.png
Sleep 3s

# Move up one row to handler.go, then open its diff side by side.
#
# The tree is following the replay, which has just written handler_test.go — a
# NEW file, so its diff is all additions with nothing to pair against. handler.go
# is the one that was patched, and it has the removed line beside the one that
# replaced it, which is what this view is for.
Type "["
Sleep 1.2s
Type "d"
Sleep 3.5s
Screenshot docs/img/diff.png
Sleep 2.5s

# Close the diff and open that same file in $EDITOR. It opens on the change at
# line 41 rather than at the top.
Escape
Sleep 1s
Type "e"
Sleep 3.5s
Screenshot docs/img/editor.png
Sleep 2s

# Back to verbose, exactly where it was left.
Type ":q"
Enter
Sleep 2.5s

# Back out to the list, and into a thread another agent wrote. Codex records a
# new file as content rather than as a diff, so the replay types the whole
# OpenAPI document out — step 5, and `/` jumps straight to it rather than
# waiting for playback to arrive.
#
# This beat is last on purpose: if a row count goes stale it costs this one
# frame, not the five before it.
Escape
Sleep 800ms
Escape
Sleep 1.2s
Down
Sleep 500ms
Down
Sleep 900ms
Enter
Sleep 1.5s
Type "R"
Sleep 1.2s
Type "/"
Sleep 400ms
Type "5"
Sleep 500ms
Enter
Sleep 3.5s
Screenshot docs/img/codex.png
Sleep 2s

Type "q"
Sleep 1s
