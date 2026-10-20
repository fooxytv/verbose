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

# What of this session's work is still in the files. `u` keeps only the steps
# whose work is gone, which here is three of twenty-one: the route the shell
# registered, the test file the subagent wrote, and the error text the last
# edit tidied. All three are back to what they were, and each step says so
# under its headline.
#
# The subagent's one is worth having on camera: its turns are spliced into the
# parent's timeline, so the filter reaches work the parent did not do itself.
#
# The sidebar closes first, so the step and its reason line have the whole
# screen. The right-arrow moves one step on so the bar reads 2/3 rather than
# 1/3 — the counter is what the frame is for, and any of the three steps says
# the same thing, so which one it lands on does not matter.
Type "T"
Sleep 1.2s
Type "u"
Sleep 3s
Right
Sleep 2.5s
Screenshot docs/img/lost-work.png
Sleep 2.5s

# Back to every step, so nothing after this depends on the filter being open.
Type "u"
Sleep 1.2s

# Find a step by what it PRINTED. "want 404" is in two of this session's
# twenty-one steps: step 8 wrote the test that asserts it, and step 9 is the
# run where it failed — and the failure text exists nowhere but that step's
# output, which is the half of a session no headline can show.
#
# `n` moves to the second, which is the frame worth having: the counter reads
# match 2/2 and the FAIL is on screen under it.
#
# The same prompt as "go to step" — a number jumps, anything else searches —
# so this types a needle into the key the tape already used for step 5.
Type "/"
Sleep 500ms
Type "want 404"
Sleep 700ms
Enter
Sleep 2.5s
Type "n"
Sleep 2.5s
Screenshot docs/img/find.png
Sleep 2.5s

# Clear the search, so the beats after this are unchanged by it.
Type "/"
Sleep 400ms
Enter
Sleep 1.2s

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
Sleep 1.5s

# Out of the UI and back at a prompt: what those transcripts are still
# holding. The triage is the point — one token to revoke, one that died
# three days ago and says so in its own claims, and one that only looks
# alarming because it is the azurite key Microsoft publishes.
#
# The values are invented and assembled from parts in demo-data.py; a
# literal of any of these shapes is a secret as far as a scanner is
# concerned, and push protection has already rejected one branch of this
# repository over a fake.
Type "clear && verbose secrets -all"
Sleep 600ms
Enter
Sleep 3.5s
Screenshot docs/img/secrets.png
Sleep 3s

