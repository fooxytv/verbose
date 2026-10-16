# VHS tape for the README recordings:  vhs docs/demo.tape
#
# Runs against a synthetic ~/.claude tree built by docs/demo-data.py, never the
# real one. This repository is public, and a real transcript carries project
# names, file paths, prompts and code that have no business in a demo.

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
Type "export HOME=/tmp/devdemo EDITOR=nvim PS1='$ ' && unset TMUX ITERM_SESSION_ID TERM_PROGRAM && clear"
Enter
Sleep 500ms
Show

Type "verbose"
Sleep 400ms
Enter
Sleep 2.5s
Screenshot docs/img/sessions.png

# Into the session that did the work: row 5, the one that is 2h38m old.
#
# FOUR downs, not five. The subagent now sits directly under its parent in the
# list (`└─◦`), so a fifth lands on the agent's own run instead — five was
# right before grouping existed and silently wrong after it.
Down
Sleep 500ms
Down
Sleep 500ms
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

Type "q"
Sleep 1s
