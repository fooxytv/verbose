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
Hide
Type "export HOME=/tmp/devdemo PS1='$ ' && clear"
Enter
Sleep 500ms
Show

Type "verbose"
Sleep 400ms
Enter
Sleep 2.5s
Screenshot docs/img/sessions.png

# Into the session that did the work.
Down
Sleep 500ms
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
Space
Sleep 7s
Screenshot docs/img/replay.png
Sleep 3s

# The tree beside it, lighting up as files are written.
Type "T"
Sleep 5s
Screenshot docs/img/tree-split.png
Sleep 3s

# A file's diff, side by side.
Type "d"
Sleep 3.5s
Screenshot docs/img/diff.png
Sleep 2.5s

Escape
Sleep 800ms
Type "q"
Sleep 1s
