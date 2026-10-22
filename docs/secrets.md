# Secrets in transcripts

What verbose can mask, what it can tell you to revoke, and what neither of those fixes.


## `verbose secrets` — what to revoke

```console
$ verbose secrets
67 sessions scanned · 3 to deal with · 12 acknowledged · expired, public and possible hidden (-all)

  KIND          ID        WHERE                  SEEN  LAST         WHAT TO DO
  bearer token  bd74b455  ProjectMilo               8  today        identify the issuer, then revoke
  github token  dbdc3e07  dotfiles (2 sessions)   136  4 days ago   revoke and re-issue
  jwt           7dfeea01  artifactory               2  21 days ago  identify the issuer, then revoke

MITIGATIONS

  github token
      revoke   https://github.com/settings/tokens
      prevent  A token in a remote URL is written to .git/config too.
               Reset the remote to plain https and let `gh auth login`
               or a credential helper hold it, so it is never in a URL
               an agent reads.
```

**This is the half that reduces risk.** Masking a view stops a credential
reaching a *new* audience; it does nothing about the copy already on disk,
which outlives the session and is read by every backup. Only rotating the
credential does that, and you cannot rotate what you do not know is there.

**It never prints a value.** A credential becomes eight hex characters of its
SHA-256 — enough to tell two apart, to recognise the same one in two places,
and to count how many distinct things there are to revoke. The output is safe
to paste into a ticket, which is where it is going.

**A table, because the question is a comparison**: which of these matters
most, which is oldest, which is in work you remember. `-details` lists the
sessions each one appears in, for going back to it with the replay.

### Every finding carries both halves of the fix

Revoking ends *this* credential. It reached a transcript because it passed
through the agent's hands — a command line, a file it read, a URL — and unless
that changes the next one will too. So the report says both:

| | |
|---|---|
| **revoke** | Where this kind is revoked, when there is one place |
| **prevent** | What stops it happening again — a credential helper instead of a token in a remote URL, Entra ID instead of a storage key, an expiry and a scope instead of neither |

The advice is grouped by kind, so four GitHub tokens get it once.

### The triage

A list of things that look like secrets is a worry. A list that separates them
is a task:

| | |
|---|---|
| **rotate** | Long-lived, and nothing expires it on its own |
| **possible** | A literal assigned to a secret-looking name — a judgement, not a shape |
| **expired** | The token's own claims say so |
| **inert** | The shape of a credential with nothing behind it |
| **public** | Published on purpose, and only looks alarming |

Only `rotate` shows by default; `-all` shows the rest, with a STATUS column.

**A JWT says when it dies, so verbose reads it.** The payload is base64url of
plain JSON by design — no key needed — so an access token that expired three
days ago is reported as expired, with its issuer, rather than as one more
thing to panic about. Most of the tokens an agent handles are these.

**An unsigned token is not a credential.** `alg: none` means nothing vouches
for the claims, so it authenticates nobody. On one real machine the two of
these were forged *on purpose*, by a command whose own output reads
`forged jwt header (must be 403)` — reporting a test that something is
**rejected** as a leak is the opposite of useful.

**Published examples are not secrets.** The azurite emulator key is in
Microsoft's documentation; `AKIAIOSFODNN7EXAMPLE` is in AWS's. They are held
by fingerprint, so this code contains no key-shaped literal of its own. On one
real machine the azurite key alone was 18 of 21 "Azure keys" found — and the
three that mattered were buried under it.

**A storage key is named with its account**, from the same connection string
line, because "rotate a key" is a worry and "rotate the key on `stprod01`" is
a job. Only a storage key gets one: searching the whole text put
`storage account stprod01` on a Slack token that merely shared a transcript
with it.

### Acknowledging what you have dealt with

```console
$ verbose secrets -ignore dbdc3e07 -note "rotated 22 Oct"
$ verbose secrets -ignored        # what has been acknowledged, and when
$ verbose secrets -unignore dbdc3e07
```

A report that says the same thing every week is one nobody reads twice. Once a
finding is dealt with — rotated, or confirmed to be a fixture — acknowledge it
and the list shows what is **left**. It is stored by fingerprint in
`~/.config/verbose/secrets-ignored.json`, so that file holds no secret either,
and the count of hidden findings is always printed: a report that quietly
drops rows cannot be trusted to be complete.

Two kinds of finding are worth acknowledging rather than chasing: a test
fixture (verbose's own source contains token-shaped literals, by necessity, to
test the detector), and a credential you have already rotated.

`-project NAME` narrows, `-json` is machine-readable. Nothing to deal with
exits 0 and says so — it is the best answer the command has.

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
