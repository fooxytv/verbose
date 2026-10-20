# The project tree and diffs

`T` opens the project as it is on disk, marked with what the session did to it.


`T` works from the sessions list, the timeline and from inside a replay. It
shows the project as a tree, marked with what the open session did to each
file: **new** files in green, **changed** in orange with their churn, **removed**
in red, everything the session never touched dimmed. A change flashes as the
replay reaches it and then keeps its colour, so the tree accumulates a record of
the session rather than settling back to neutral. `Tab` narrows it to just the
files that changed. `Enter` on a file jumps the replay to the change.

Two different kinds of truth sit in that one view, and the header says so:

- **The structure comes from disk**, so it is the project as it is *now*. For a
  session from three weeks ago that is today's shape with three-week-old marks
  painted on. Files the session touched that have since gone are grafted back in
  and marked `(gone)`.
- **The marks come from the transcript**, which is exact however old the session
  is.

The tree follows the replay, and **scrolls itself to whatever is being worked
on** — a project tree is far longer than the pane showing it, so without
that the file being written is usually below the fold and its flash is never
seen. It stays on the most recent change rather than snapping back between
steps, and ignores changes with no row in the tree, such as scratch files a
session writes outside its own project.

`Space` plays, and files light up as they are written, settling over about a
second into their colour — so the project assembles itself in front of you.
Step back in the replay and later work disappears again. Moving the cursor
yourself takes over from the follow; `Space` hands it back. The sidebar has no
cursor of its own, so it always follows.

## Diffs

With a file selected in the tree, **`d` opens its diff** in the right-hand
pane — old on the left, new on the right, with line numbers, pairing each
removed line against the one that replaced it. `[` and `]` step to the next
file and the diff follows, so a run of changes can be read without closing it.
`d` or `Esc` closes it, and so does any replay control — `Space` closes the
panel and plays on, `←`/`→` close it and step — since reaching for those means
you are done reading.

Opening a diff pauses playback: reading a diff and watching code stream are not
things anyone does at once, and the pane the diff needs is the one the replay
was using. Two columns need **86 columns of pane**; below that it falls back to
a unified single column and says so.

Like the tree's colours, the diff is bounded by where the replay has reached —
it shows what had happened by that point, not what is still to come. For a file
whose changes come later it says so and tells you how many.

Not every change can be shown as a diff, and the panel says which reason
applies rather than appearing to have found nothing:

| Change | What the panel shows |
|---|---|
| An edit to an existing file | The recorded diff, side by side |
| A **new** file | All of it as additions — there is no previous version |
| Written through the shell | Its recorded diff where there is one, otherwise the whole body it was given |
| **Removed** | An explanation only. Claude Code has no delete tool, so the removal is inferred from the command line and the contents were never kept |
| Changed by **you**, outside Claude | The snippet the transcript keeps, which is all it keeps |

## Side by side

`T` inside a replay puts the tree **beside** the code rather than instead of it:
the tree on the left, the code streaming on the right, the tree lighting up as
each file is written. All keys stay with the replay and the tree follows, so
there is no focus to switch and no key that means one thing on the left and
another on the right — navigating the tree is what the full-screen view is for.

In the split the plain arrows stay with the replay, so nothing changes meaning
when the sidebar opens. The tree gets its own keys: **`]` and `[` move the
selection**, `}` and `{` open and close directories.

They are bare characters on purpose. `Ctrl` with the arrows is unusable on
macOS, where the system takes all four for Mission Control, Application Windows
and moving between Spaces, so the terminal never sees them; `Shift` with the
arrows is already page-scroll here and some terminals keep it for text
selection. A plain character has no modifier to be intercepted. The `Ctrl`
bindings are still accepted as aliases if you have remapped your system
shortcuts.

Driving the tree by hand stops it following the replay — being pulled away
mid-read is worse than having to ask for the follow back. **Pressing play hands
it back**, since that is an explicit "watch this again"; so does `Ctrl`+`f`, and
the footer shows which of the two is in charge. Scrolling the tree while the
replay is running does not stop the replay: the two panes are independent, and
only `Ctrl`+`f` re-arms the follow in that case.

It needs **100 columns**. Below that the code pane has too little room to read
code in, so the split is refused and says how many columns it wants rather than
showing two unusable halves. The tree takes a third of the width, up to 40
columns.

Build output and version control are skipped (`node_modules`, `.git`, `dist`,
`target`, `.terraform` and the rest), unless the session changed something
inside, in which case the directory is opened anyway. The scan is capped at
4000 entries and 8 levels so a large repository cannot stall the view.

Because half of what an agent does goes through the shell, the tree also credits
files that only appear in a command — `sed -n '1,80p' main.go`,
`grep -n x pkg/a.go`, `cat README.md`. Measured over one machine's transcripts,
1515 of 2992 shell commands named a real file in the project and 202 files
appeared *only* that way, so a tree built from tool calls alone sits still
through most of a Bash-heavy session. Those mentions are resolved against the
tree already read from disk, so a version number or a Go module path that
happens to look like a path does not register, and they are marked inferred: a
command naming a file is good evidence it was read and no evidence of anything
more.

One honest limitation: **deletions are inferred, not recorded.** Claude Code has
no delete tool, so the only evidence is an `rm` inside a shell command. Simple
forms are picked up; anything with a glob, a variable or a substitution is
skipped rather than guessed at, because naming the wrong file as deleted is
worse than naming none. For the same reason a file written through the shell is
reported as *written* rather than created or edited — whether it existed
beforehand is nowhere in the transcript.

---

[← back to the README](../README.md)
