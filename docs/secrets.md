# Secrets in transcripts

What verbose can mask, what it can tell you to revoke, and what neither of those fixes.


## `verbose secrets` — what to revoke

```console
$ verbose secrets
1 distinct credential (136 occurrences) across 65 sessions

REVOKE THESE — long-lived, and nothing expires them on their own

  github token               dbdc3e07   136× in 2 sessions, last 3 days ago
      2026-10-17  dotfiles       8ba54895-fbb5-4511-8546-dbdbe0d28880
        I need help running my capture.sh script which I've had a agent he…
      2026-10-17  dotfiles       d45507ed-022e-43e3-a978-1b53cf0cf4d9
      revoke: https://github.com/settings/tokens
```

**This is the half that reduces risk.** Masking a view stops a credential
reaching a *new* audience; it does nothing about the copy already on disk,
which outlives the session and is read by every backup. Only rotating the
credential does that, and you cannot rotate what you do not know is there.

**It never prints a value.** A credential becomes eight hex characters of its
SHA-256 — enough to tell two apart, to recognise the same one in two places,
and to count how many distinct things there are to revoke. The output is safe
to paste into a ticket, which is where it is going.

### The triage is the point

A list of things that look like secrets is a worry. A list that separates them
is a task:

| | |
|---|---|
| **REVOKE THESE** | Long-lived, and nothing expires them on its own |
| **WORTH A LOOK** | A literal assigned to a secret-looking name — a judgement, not a shape. Hidden unless you pass `-all` |
| **ALREADY DEAD** | The token's own claims say so |
| **NOT SECRETS** | Published on purpose, and only look alarming |

**A JWT says when it dies, so verbose reads it.** The payload is base64url of
plain JSON by design — no key needed — so an access token that expired three
days ago is reported as expired, with its issuer, rather than as one more
thing to panic about. Most of the tokens an agent handles are these.

**The azurite emulator key is not a secret.** Microsoft prints it in its own
documentation and every local Azure Storage emulator uses it. On one real
machine it accounted for 18 of 21 "Azure keys" found — and without that
exception the three that mattered were buried.

**A storage key is named with its account**, from the same connection string
line, because "rotate a key" is a worry and "rotate the key on `stprod01`" is
a job. Only a storage key gets one: searching the whole text put
`storage account stprod01` on a Slack token that merely shared a transcript
with it.

`-project NAME` narrows, `-all` shows the quiet categories, `-json` is
machine-readable. Nothing to revoke exits 0 and says so — it is the best
answer the command has.

## `verbose --redact` — masking the screen

`verbose --redact` masks what looks like a credential — GitHub, GitLab and
Slack tokens, Azure account keys and SAS signatures, AWS key ids, JWTs,
private key blocks, and a password or secret assigned a literal value. The
footer says `redacted` for as long as it is on, on the left where the
keybindings are, because both halves of that row give way on a narrow terminal
and an indicator that disappears is worse than none.

**It is a display control, not a security control.** Everything in a
transcript has already been through a model, so masking the local copy cannot
un-send it. What it stops is the *second* audience: a screen share, a
screenshot in a channel, a `-json` dump pasted into a ticket. **If a secret
reached a transcript, rotate it** — and remember the plaintext copy lives in
`~/.claude/projects` until Claude Code's `cleanupPeriodDays` removes it, and
is copied by every backup in the meantime.

It is deliberately shy. A reference to a secret is not a secret, so
`client_secret = $ARM_CLIENT_SECRET`, `var.client_secret`,
`${{ secrets.AZURE_CLIENT_ID }}`, `os.Getenv("API_KEY")` and
`password = "<your-password>"` are all left alone — on a terraform or Actions
transcript that is most of them, and masking them would destroy the only
useful fact on the line while protecting nothing. Every pattern is anchored to
a word boundary for the same reason: an unanchored `sk-` matched 255 times
across one real history and every hit was the word `task-`.

Paths are never masked: a redacted path breaks the tree, the relinks and the
lost-work survey, and a directory name is not a credential.

## What neither of them fixes

Nothing here un-sends a credential. If it is in a transcript it was in a
model's context, and the plaintext copy on disk outlives the session — until
Claude Code's `cleanupPeriodDays` removes it, and copied by every backup in
the meantime. The list is short:

1. **Rotate** anything `verbose secrets` puts under REVOKE THESE.
2. Keep credentials out of the agent's hands where you can — a keychain, an
   env var the shell exports, a CLI that holds its own auth.
3. Shorten `cleanupPeriodDays` if transcripts are living longer than the work
   they describe.

verbose deliberately does **not** rewrite transcripts to scrub them. They
belong to the agent that wrote them, `--resume` reads them, and a scrubber
that corrupts a session to protect a key that has already leaked has made
things worse twice over.

---

[← back to the README](../README.md)
