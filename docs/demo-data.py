#!/usr/bin/env python3
"""Build a synthetic HOME for the verbose demo recordings.

Five stores, because verbose reads five agents and the demo is meant to show
that: Claude Code's JSONL transcripts, Codex's two SQLite databases, OpenCode's
one, Copilot's database plus event stream, and pi's file per session.

Everything here is invented. The repo is public, so the demo must not show real
session titles, client project names, file paths, code or costs.
"""
import base64
import json, os, shutil, sqlite3
from datetime import datetime, timedelta, timezone

HOME = "/tmp/devdemo"
# The cwd must really exist for the tree to have anything to read, and the
# directory name is what the project is called, so: no hyphens to decode badly.
CWD = "/tmp/devdemo/notestore"
PROJECT_DIR = os.path.join(HOME, ".claude", "projects", "-tmp-devdemo-notestore")
SESSION = "7c3f1a90-2b4d-4e8a-9f61-0d5c8e2a4b77"
AGENT = "a7f2c91e4b6d80a35"

# Relative to now: a recording made months later must not show "209d ago".
NOW = datetime.now(timezone.utc)
t0 = NOW - timedelta(hours=2, minutes=40)
clock = {"n": 0}


def ts():
    clock["n"] += 1
    return (t0 + timedelta(seconds=clock["n"] * 7)).isoformat().replace("+00:00", "Z")


USAGE = {"input_tokens": 4120, "output_tokens": 310,
         "cache_read_input_tokens": 38000, "cache_creation_input_tokens": 900}

lines = []


def user(text):
    lines.append({"type": "user", "uuid": f"u{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "gitBranch": "main", "timestamp": ts(),
                  "message": {"role": "user", "content": text}})


def say(text):
    lines.append({"type": "assistant", "uuid": f"a{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "gitBranch": "main", "timestamp": ts(),
                  "message": {"id": f"msg_{clock['n']}", "role": "assistant",
                              "model": "claude-opus-4-5", "usage": USAGE,
                              "content": [{"type": "text", "text": text}]}})


def tool(tid, name, inp):
    lines.append({"type": "assistant", "uuid": f"a{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "gitBranch": "main", "timestamp": ts(),
                  "message": {"id": f"msg_{clock['n']}", "role": "assistant",
                              "model": "claude-opus-4-5", "usage": USAGE,
                              "content": [{"type": "tool_use", "id": tid,
                                           "name": name, "input": inp}]}})


def result(tid, payload, text="ok"):
    lines.append({"type": "user", "uuid": f"u{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "timestamp": ts(), "toolUseResult": payload,
                  "message": {"role": "user", "content": [
                      {"type": "tool_result", "tool_use_id": tid, "content": text}]}})


def b64url(raw):
    """base64url with no padding, which is what a JWT uses."""
    return base64.urlsafe_b64encode(raw).decode().rstrip("=")


def hunk(os_, ol, ns, nl, ls):
    return {"oldStart": os_, "oldLines": ol, "newStart": ns, "newLines": nl, "lines": ls}


# ----------------------------------------------------------------- the session
user("The notes API returns 500 when a note is missing instead of 404, and there "
     "are no tests around the store. Can you fix the status code and add coverage?")

say("I'll look at how the handler reports a missing note before changing anything.")

tool("t1", "Read", {"file_path": f"{CWD}/internal/http/handler.go"})
result("t1", {"file": {"filePath": f"{CWD}/internal/http/handler.go",
                       "numLines": 84, "startLine": 1, "totalLines": 84}})

tool("t2", "Grep", {"pattern": "ErrNotFound", "path": "internal"})
result("t2", {"stdout": "internal/store/store.go:17:var ErrNotFound = errors.New(\"note not found\")\n",
              "stderr": "", "interrupted": False})

# A plan the way an agent actually writes one: markdown. The replay renders it
# — heading, table, numbered list, `code`, **bold** — which is the whole point
# of having a step like this on camera.
say("""## The fix

`store.Get` already returns a typed `ErrNotFound`. `getNote` never checks for
it, so every miss falls through to the generic 500 branch.

| File | Change |
|---|---|
| `internal/http/handler.go` | Map `ErrNotFound` to **404** |
| `internal/http/router.go` | Register `/notes/{id}` |
| `internal/http/handler_test.go` | Cover the missing-note path |

1. Check the typed error in `getNote`, before the generic 500
2. Add a failing test for a note that does not exist
3. Register the route that test calls, then re-run

The store's own error text stays as it is — *that* is a separate change.""")

tool("t3", "Edit", {"file_path": f"{CWD}/internal/http/handler.go"})
result("t3", {"filePath": f"{CWD}/internal/http/handler.go", "structuredPatch": [
    hunk(41, 7, 41, 11, [
        " func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {",
        " \tnote, err := s.store.Get(r.Context(), chi.URLParam(r, \"id\"))",
        " \tif err != nil {",
        "-\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)",
        "+\t\tif errors.Is(err, store.ErrNotFound) {",
        "+\t\t\thttp.Error(w, \"note not found\", http.StatusNotFound)",
        "+\t\t\treturn",
        "+\t\t}",
        "+\t\thttp.Error(w, \"internal error\", http.StatusInternalServerError)",
        " \t\treturn",
        " \t}",
    ])]})

say("Now the tests. I'll cover the missing-note path and a round trip.")

tool("t4", "Write", {"file_path": f"{CWD}/internal/http/handler_test.go",
                     "content": "package http\n\nimport (\n\t\"net/http\"\n\t\"testing\"\n)\n\n"
                                "func TestGetMissingNoteIs404(t *testing.T) {\n"
                                "\tsrv := newTestServer(t)\n"
                                "\tres := srv.get(\"/notes/does-not-exist\")\n"
                                "\tif res.Code != http.StatusNotFound {\n"
                                "\t\tt.Errorf(\"status = %d, want 404\", res.Code)\n\t}\n}\n"})
result("t4", {"filePath": f"{CWD}/internal/http/handler_test.go", "structuredPatch": []})

tool("t5", "Bash", {"command": "go test ./internal/... 2>&1 | tail -20",
                    "description": "Run the package tests"})
result("t5", {"stdout": "--- FAIL: TestGetMissingNoteIs404 (0.00s)\n"
                        "    handler_test.go:11: status = 500, want 404\n"
                        "FAIL\tnotes-api/internal/http\t0.112s\n",
              "stderr": "", "interrupted": False},
       text="Error: Exit code 1")

say("Failing for the right reason — the router wasn't wired to the new path. "
    "Fixing that through the shell.")

# A shell edit, which records its diff in bashEditDiff rather than structuredPatch.
tool("t6", "Bash", {"command": "python3 - <<'PY'\n"
                    "p='internal/http/router.go'\n"
                    "s=open(p).read()\n"
                    "s=s.replace('r.Get(\"/notes\", s.listNotes)',\n"
                    "            's.Get(\"/notes\", s.listNotes)\\n\\tr.Get(\"/notes/{id}\", s.getNote)')\n"
                    "open(p,'w').write(s)\nPY",
                    "description": "Register the single-note route"})
result("t6", {"stdout": "", "stderr": "", "interrupted": False,
              "bashEditDiff": {"moreFiles": 0, "shared": True, "files": [
                  {"filePath": f"{CWD}/internal/http/router.go", "hunks": [
                      hunk(22, 4, 22, 5, [
                          " func (s *Server) routes(r chi.Router) {",
                          " \tr.Get(\"/notes\", s.listNotes)",
                          "+\tr.Get(\"/notes/{id}\", s.getNote)",
                          " \tr.Post(\"/notes\", s.createNote)",
                      ])]}]}})

tool("t7", "Bash", {"command": "go test ./internal/... 2>&1 | tail -5",
                    "description": "Re-run the tests"})
result("t7", {"stdout": "ok  \tnotes-api/internal/http\t0.139s\n"
                        "ok  \tnotes-api/internal/store\t0.084s\n",
              "stderr": "", "interrupted": False})

say("Green. I'll hand the store coverage to an agent while I tidy the error text.")

tool("t8", "Agent", {"subagent_type": "general-purpose",
                     "description": "Add store package tests",
                     "prompt": "Add table-driven tests for internal/store covering Get, "
                               "Put and the ErrNotFound path."})
result("t8", {"stdout": "Async agent launched successfully.\n"
                        f"agentId: {AGENT} (internal ID)\n", "stderr": "", "interrupted": False},
       text=f"Async agent launched successfully.\nagentId: {AGENT} (internal ID)")

tool("t9", "Edit", {"file_path": f"{CWD}/internal/store/store.go"})
result("t9", {"filePath": f"{CWD}/internal/store/store.go", "structuredPatch": [
    hunk(15, 5, 15, 5, [
        " // ErrNotFound is returned when no note exists for the given id.",
        "-var ErrNotFound = errors.New(\"note not found\")",
        "+var ErrNotFound = errors.New(\"store: note not found\")",
        " ",
        " type Store struct {",
    ])]})

say("Done. The handler now maps `store.ErrNotFound` to 404, the route is "
    "registered, and both packages pass.")

# ----------------------------------------------------------------- the subagent
agent_lines = []


def a_entry(obj):
    obj.update({"isSidechain": True, "agentId": AGENT, "attributionAgent": "general-purpose",
                "sessionId": SESSION, "cwd": CWD, "timestamp": ts()})
    agent_lines.append(obj)


a_entry({"type": "user", "uuid": "s1",
         "message": {"role": "user", "content": "Add table-driven tests for internal/store "
                                                "covering Get, Put and the ErrNotFound path."}})
a_entry({"type": "assistant", "uuid": "s2",
         "message": {"id": "msg_s2", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "text", "text": "Reading the store first."}]}})
a_entry({"type": "assistant", "uuid": "s3",
         "message": {"id": "msg_s3", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "tool_use", "id": "s_t1", "name": "Read",
                                  "input": {"file_path": f"{CWD}/internal/store/store.go"}}]}})
a_entry({"type": "user", "uuid": "s4",
         "toolUseResult": {"file": {"filePath": f"{CWD}/internal/store/store.go",
                                    "numLines": 62, "startLine": 1, "totalLines": 62}},
         "message": {"role": "user", "content": [
             {"type": "tool_result", "tool_use_id": "s_t1", "content": "ok"}]}})
a_entry({"type": "assistant", "uuid": "s5",
         "message": {"id": "msg_s5", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "tool_use", "id": "s_t2", "name": "Write",
                                  "input": {"file_path": f"{CWD}/internal/store/store_test.go",
                                            "content": "package store\n\nimport (\n\t\"context\"\n"
                                                       "\t\"errors\"\n\t\"testing\"\n)\n\n"
                                                       "func TestGetMissing(t *testing.T) {\n"
                                                       "\ts := New()\n"
                                                       "\t_, err := s.Get(context.Background(), \"nope\")\n"
                                                       "\tif !errors.Is(err, ErrNotFound) {\n"
                                                       "\t\tt.Fatalf(\"err = %v, want ErrNotFound\", err)\n\t}\n}\n"}}]}})
a_entry({"type": "user", "uuid": "s6",
         "toolUseResult": {"filePath": f"{CWD}/internal/store/store_test.go",
                           "structuredPatch": []},
         "message": {"role": "user", "content": [
             {"type": "tool_result", "tool_use_id": "s_t2", "content": "ok"}]}})
a_entry({"type": "assistant", "uuid": "s7",
         "message": {"id": "msg_s7", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "text",
                                  "text": "Added store_test.go covering the ErrNotFound path."}]}})

# ------------------------------------------------- a few more, to fill the list
#
# A one-row session list says nothing about what the tool is for. These are
# short and invented, across a couple of projects, with ages spread out.
# Three of them carry an invented credential, because `verbose secrets` has
# nothing to report otherwise and the whole point of that command is the
# TRIAGE: one to revoke, one already dead, one that was never a secret.
#
# Every value is assembled from parts rather than written out. A literal of
# any of these shapes is a secret as far as a scanner is concerned, and
# GitHub's push protection rejected a branch of this repository over a fake
# Slack token in a test fixture — which is the system working.
FAKE_GH = "gh" + "p_" + "D3m0" + "Tok3nF0rTh3R3c0rd1ng0nly00"

# An access token that died three days ago. Built here, with a real payload,
# because verbose reads the expiry out of the token itself — that is what
# lets it say "already dead" instead of "a token, good luck".
_exp = int((NOW - timedelta(days=3)).timestamp())
_jwt_head = b64url(json.dumps({"alg": "RS256", "typ": "JWT"}).encode())
_jwt_body = b64url(json.dumps({
    "iss": "https://login.microsoftonline.com/demo-tenant/v2.0",
    "aud": "https://storage.azure.com", "exp": _exp, "sub": "demo",
}).encode())
FAKE_JWT = f"{_jwt_head}.{_jwt_body}.ZGVtby1zaWduYXR1cmU"

# The azurite emulator key. This one is genuinely public — Microsoft prints it
# in its own documentation — and it is here because it looks exactly like a
# production storage key and verbose has to say that it is not one.
AZURITE = ("Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq"
           "/K1SZFPTOtr/KBHBeksoGMGw==")

EXTRAS = [
    ("notestore", CWD, "2f8b6c14-9ae3-4d27-b05f-61c7d9a3e8f2", 5,
     "Add a /healthz endpoint that reports the store connection.", None),
    ("notestore", CWD, "9d41e7b2-6c08-4a15-83fe-2b7a0f9c5d31", 31,
     "Swap the in-memory store for SQLite behind the same interface.",
     ("az storage blob list --connection-string "
      "'DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;"
      f"AccountKey={AZURITE};BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1'",
      "[]\n")),
    ("webhooks", "/tmp/devdemo/webhooks", "4a92c5d7-1f36-4b80-a7e2-8c0d3f6b9a14", 50,
     "Retry failed webhook deliveries with exponential backoff.",
     (f"curl -s -H 'Authorization: Bearer {FAKE_JWT}' https://api.internal/deliveries",
      '{"pending":3}\n')),
    ("webhooks", "/tmp/devdemo/webhooks", "b8e03f14-7d25-4c69-9a31-5f8c2e7d0b46", 125,
     "Why is the delivery worker holding connections open after a 504?", None),
    ("dotfiles", "/tmp/devdemo/dotfiles", "61c7a284-3e9b-42d7-85f0-9a4e1c6b8d53", 1510,
     "Tidy up the zsh prompt so it stops wrapping on narrow terminals.",
     (f"git push https://{FAKE_GH}@github.com/demo/dotfiles.git main",
      "Everything up-to-date\n")),
]

extra_sessions = []
for name, cwd, sid, mins_ago, prompt, planted in EXTRAS:
    base = NOW - timedelta(minutes=mins_ago)
    command, stdout = planted or ("go test ./...", "ok\tall packages\n")
    rows = [
        {"type": "user", "uuid": "x1", "sessionId": sid, "cwd": cwd,
         "timestamp": base.isoformat().replace("+00:00", "Z"),
         "message": {"role": "user", "content": prompt}},
        {"type": "assistant", "uuid": "x2", "sessionId": sid, "cwd": cwd,
         "timestamp": (base + timedelta(seconds=14)).isoformat().replace("+00:00", "Z"),
         "message": {"id": "mx2", "role": "assistant", "model": "claude-opus-4-5",
                     "usage": USAGE,
                     "content": [{"type": "text", "text": "Taking a look."}]}},
        {"type": "assistant", "uuid": "x3", "sessionId": sid, "cwd": cwd,
         "timestamp": (base + timedelta(seconds=40)).isoformat().replace("+00:00", "Z"),
         "message": {"id": "mx3", "role": "assistant", "model": "claude-opus-4-5",
                     "usage": USAGE,
                     "content": [{"type": "tool_use", "id": "xt1", "name": "Bash",
                                  "input": {"command": command,
                                            "description": "Run it"}}]}},
        {"type": "user", "uuid": "x4", "sessionId": sid, "cwd": cwd,
         "timestamp": (base + timedelta(seconds=52)).isoformat().replace("+00:00", "Z"),
         "toolUseResult": {"stdout": stdout, "stderr": "", "interrupted": False},
         "message": {"role": "user", "content": [
             {"type": "tool_result", "tool_use_id": "xt1", "content": "ok"}]}},
    ]
    extra_sessions.append((name, cwd, sid, rows))

# ----------------------------------------------------------------- write it out
if os.path.isdir(HOME):
    shutil.rmtree(HOME)
sub = os.path.join(PROJECT_DIR, SESSION, "subagents")
os.makedirs(sub, exist_ok=True)


def dump(path, rows):
    with open(path, "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")


dump(os.path.join(PROJECT_DIR, f"{SESSION}.jsonl"), lines)
for name, cwd, sid, rows in extra_sessions:
    d = os.path.join(HOME, ".claude", "projects", cwd.replace("/", "-"))
    os.makedirs(d, exist_ok=True)
    dump(os.path.join(d, f"{sid}.jsonl"), rows)
    os.makedirs(cwd, exist_ok=True)
    open(os.path.join(cwd, "README.md"), "w").write("# demo\n")
dump(os.path.join(sub, f"agent-{AGENT}.jsonl"), agent_lines)
with open(os.path.join(sub, f"agent-{AGENT}.meta.json"), "w") as f:
    json.dump({"agentType": "general-purpose", "description": "Add store package tests",
               "toolUseId": "t8", "spawnDepth": 1}, f)

# A real project directory, so the tree has something to show.
#
# The files carry plausible content rather than a one-line stub, because `e`
# opens one in an editor on camera. handler.go in particular must be long
# enough to reach line 41: that is where the session's diff hunk starts, so it
# is where the editor is asked to open, and a four-line file would land at the
# top and quietly make the jump look broken.
HANDLER_GO = """package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"notestore/internal/store"
)

// Server serves the notes API over an injected store.
type Server struct {
	store store.Store
	mux   *chi.Mux
}

// NewServer wires the routes onto a fresh router.
func NewServer(s store.Store) *Server {
	srv := &Server{store: s, mux: chi.NewRouter()}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.Get("/notes/{id}", s.getNote)
	s.mux.Post("/notes", s.createNote)
	s.mux.Get("/healthz", s.healthz)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// getNote returns one note, or 404 when the store has never heard of it.
func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	note, err := s.store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "note not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var note store.Note
	if err := json.NewDecoder(r.Body).Decode(&note); err != nil {
		http.Error(w, "malformed body", http.StatusBadRequest)
		return
	}
	if err := s.store.Put(r.Context(), note); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, note)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
"""

# The hunk in the transcript starts at line 41, and `e` opens there. If the
# file is ever reworded this assertion is what stops the demo from silently
# opening on the wrong function.
_lines = HANDLER_GO.split("\n")
assert _lines[40].strip().startswith("func (s *Server) getNote"), \
    "handler.go line 41 is %r, but the demo diff hunk starts there" % _lines[40]

HANDLER_TEST_GO = """package http

import (
	"net/http"
	"testing"
)

func TestGetMissingNoteIs404(t *testing.T) {
	srv := newTestServer(t)
	res := srv.get("/notes/does-not-exist")
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}
}
"""

STORE_GO = """package store

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a note id is not in the store.
var ErrNotFound = errors.New("note not found")

// Note is one stored note.
type Note struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

// Store is the persistence boundary the HTTP layer depends on.
type Store interface {
	Get(ctx context.Context, id string) (Note, error)
	Put(ctx context.Context, n Note) error
}
"""

# THREE OF THESE DELIBERATELY DISAGREE WITH THE TRANSCRIPT, and the `u` beat in
# demo.tape is recording that disagreement: router.go has no `/notes/{id}` route,
# store.go still says "note not found" rather than "store: note not found", and
# store_test.go is a placeholder rather than what the subagent wrote. Each is
# work the session recorded that is not in the file now, which is what the
# lost-work filter exists to find. "Fixing" them to match the transcript would
# empty that filter and the beat would record an empty screen.
PROJECT_FILES = {
    "go.mod": "module notestore\n\ngo 1.25\n",
    "README.md": "# notestore\n\nA small notes API used to demonstrate verbose.\n",
    "cmd/server/main.go": """package main

import (
	"log"
	"net/http"

	"notestore/internal/http"
	"notestore/internal/store"
)

func main() {
	srv := http.NewServer(store.NewMemory())
	log.Fatal(http.ListenAndServe(":8080", srv))
}
""",
    "internal/http/handler.go": HANDLER_GO,
    "internal/http/handler_test.go": HANDLER_TEST_GO,
    "internal/http/router.go": "package http\n\n// Routing lives on the Server; this file is a placeholder.\n",
    "internal/store/store.go": STORE_GO,
    "internal/store/store_test.go": "package store\n\nimport \"testing\"\n\nfunc TestNothingYet(t *testing.T) {}\n",
    "internal/store/memory.go": """package store

import (
	"context"
	"sync"
)

type memory struct {
	mu    sync.RWMutex
	notes map[string]Note
}

// NewMemory returns an in-memory Store, for tests and local runs.
func NewMemory() Store {
	return &memory{notes: map[string]Note{}}
}

func (m *memory) Get(ctx context.Context, id string) (Note, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.notes[id]
	if !ok {
		return Note{}, ErrNotFound
	}
	return n, nil
}

func (m *memory) Put(ctx context.Context, n Note) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes[n.ID] = n
	return nil
}
""",
    "docs/api.md": "# API\n\n- `GET /notes/{id}` — 200, or 404 when missing\n- `POST /notes` — 201\n",
}

for rel, body in PROJECT_FILES.items():
    path = os.path.join(CWD, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w").write(body)

# A throwaway nvim config inside the synthetic HOME. The recording must not
# load the real one: it would pull in personal plugins, and a public GIF is the
# last place to find out what they are. This one only turns on the things that
# make a screenshot readable.
NVIM_CONFIG = """vim.opt.number = true
vim.opt.termguicolors = true
vim.opt.laststatus = 2
vim.opt.cursorline = true
vim.cmd("syntax on")
vim.cmd("colorscheme habamax")
vim.cmd("highlight Normal guibg=NONE ctermbg=NONE")

-- Centre the line verbose jumped to. Without this nvim leaves it on the last
-- row of the screen, so the screenshot shows everything above the change and
-- none of the change itself.
vim.api.nvim_create_autocmd("VimEnter", { command = "normal! zz" })
"""
nvim_dir = os.path.join(HOME, ".config", "nvim")
os.makedirs(nvim_dir, exist_ok=True)
open(os.path.join(nvim_dir, "init.lua"), "w").write(NVIM_CONFIG)

# ------------------------------------------------- the other three agents
#
# Each writes its own store, and verbose reads all of them from this HOME. Two
# of these deliberately work in a directory another agent has already worked
# in: a project is a DIRECTORY, so the project view shows a marker per tool and
# the demo should show that happening.
#
# Ages are all OLDER than the main session (2h40m), which keeps that session at
# the same row in the list. The tape navigates by counting rows, so a session
# inserted above it moves the target silently.


def ms(mins_ago):
    """Unix milliseconds, the way Codex and OpenCode record time."""
    return int((NOW - timedelta(minutes=mins_ago)).timestamp() * 1000)


def iso(mins_ago):
    """ISO 8601 with a Z, the way Copilot records time."""
    return (NOW - timedelta(minutes=mins_ago)).isoformat().replace("+00:00", "Z")


# -- Codex: state_N.sqlite is the metadata, thread_history_N.sqlite the content.
CODEX_HOME = os.path.join(HOME, ".codex")
os.makedirs(CODEX_HOME, exist_ok=True)
CX_THREAD = "01a0f3c7-5b82-7d14-9e36-4a7c0b8d2f51"

state = sqlite3.connect(os.path.join(CODEX_HOME, "state_1.sqlite"))
state.executescript("""
CREATE TABLE threads (
    id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', name TEXT,
    model TEXT, model_provider TEXT NOT NULL DEFAULT '', git_branch TEXT,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER, updated_at_ms INTEGER,
    source TEXT NOT NULL DEFAULT 'vscode', thread_source TEXT);
CREATE TABLE thread_spawn_edges (
    parent_thread_id TEXT NOT NULL, child_thread_id TEXT NOT NULL PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'completed');
""")
state.execute(
    "INSERT INTO threads (id, cwd, title, name, model, model_provider, git_branch,"
    " tokens_used, created_at_ms, updated_at_ms, thread_source) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
    (CX_THREAD, CWD,
     "# Pasted context\n\nthe whole issue body goes here, verbatim and very long",
     "Generate the OpenAPI spec", "gpt-6-astra", "openai", "main",
     128_400, ms(205), ms(198), "user"))
state.commit()
state.close()

hist = sqlite3.connect(os.path.join(CODEX_HOME, "thread_history_1.sqlite"))
hist.executescript("""
CREATE TABLE thread_items (
    thread_id TEXT NOT NULL, turn_id TEXT NOT NULL, item_id TEXT NOT NULL,
    rollout_ordinal INTEGER NOT NULL, created_at_ms INTEGER NOT NULL,
    item_json TEXT NOT NULL, item_type TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (thread_id, turn_id, item_id));
CREATE TABLE thread_turns (
    thread_id TEXT NOT NULL, turn_id TEXT NOT NULL,
    rollout_ordinal INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'completed', duration_ms INTEGER,
    PRIMARY KEY (thread_id, turn_id));
""")

# An "add" carries the file's whole content in its diff field; an "update"
# carries unified hunks. Both are on camera: the replay types the new file out
# and pairs the patched lines side by side.
OPENAPI_YAML = """openapi: 3.1.0
info:
  title: notestore
  version: 1.0.0
paths:
  /notes/{id}:
    get:
      responses:
        '200': { description: the note }
        '404': { description: no such note }
"""

CX_ITEMS = [
    ("userMessage", {"type": "userMessage", "content": [
        {"type": "text", "text": "Generate an OpenAPI spec from the handlers and "
                                 "check it covers the 404 path."}]}),
    ("reasoning", {"type": "reasoning", "summary": ["**Reading the route table**"]}),
    ("agentMessage", {"type": "agentMessage", "text":
        "The router registers two paths. I'll write the spec from those and the "
        "status codes the handler actually returns."}),
    ("commandExecution", {"type": "commandExecution", "status": "completed",
                          "command": "rg -n 'http.Status' internal/http",
                          "cwd": CWD, "exitCode": 0, "durationMs": 240,
                          "aggregatedOutput":
                              "internal/http/handler.go:44:\t\t\thttp.Error(w, \"note not found\", http.StatusNotFound)\n"
                              "internal/http/handler.go:47:\t\thttp.Error(w, \"internal error\", http.StatusInternalServerError)\n"}),
    ("fileChange", {"type": "fileChange", "status": "completed", "changes": [
        {"path": f"{CWD}/docs/openapi.yaml", "kind": {"type": "add"},
         "diff": OPENAPI_YAML}]}),
    ("fileChange", {"type": "fileChange", "status": "completed", "changes": [
        {"path": f"{CWD}/docs/api.md", "kind": {"type": "update"},
         "diff": "@@ -1,4 +1,5 @@\n # API\n \n"
                 "-- `GET /notes/{id}` — 200, or 404 when missing\n"
                 "+- `GET /notes/{id}` — 200, or 404 when missing\n"
                 "+- The machine-readable spec lives in `openapi.yaml`\n"
                 " - `POST /notes` — 201\n"}]}),
    ("agentMessage", {"type": "agentMessage", "text":
        "Spec written to `docs/openapi.yaml`, and `docs/api.md` now points at it. "
        "The 404 response is covered."}),
]
for i, (kind, payload) in enumerate(CX_ITEMS, start=1):
    hist.execute(
        "INSERT INTO thread_items (thread_id, turn_id, item_id, rollout_ordinal,"
        " created_at_ms, item_json, item_type) VALUES (?,?,?,?,?,?,?)",
        (CX_THREAD, "turn-1", f"item-{i}", i, ms(205) + i * 4000,
         json.dumps(payload), kind))
hist.execute("INSERT INTO thread_turns (thread_id, turn_id, duration_ms) VALUES (?,?,?)",
             (CX_THREAD, "turn-1", 412_000))
hist.commit()
hist.close()

# -- OpenCode: one database, a row per message and a row per content part.
OC_DIR = os.path.join(HOME, ".local", "share", "opencode")
os.makedirs(OC_DIR, exist_ok=True)
OC_SESSION = "ses_4d9f2a7c1b83e05"
WEBHOOKS = "/tmp/devdemo/webhooks"

oc = sqlite3.connect(os.path.join(OC_DIR, "opencode.db"))
oc.executescript("""
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT);
CREATE TABLE session (
    id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, title TEXT,
    directory TEXT, model TEXT, agent TEXT, cost REAL DEFAULT 0,
    tokens_input INTEGER DEFAULT 0, tokens_output INTEGER DEFAULT 0,
    tokens_cache_read INTEGER DEFAULT 0, tokens_cache_write INTEGER DEFAULT 0,
    time_created INTEGER, time_updated INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, position INTEGER);
""")
oc.execute("INSERT INTO project (id, worktree) VALUES (?,?)", ("prj_webhooks", WEBHOOKS))
oc.execute(
    "INSERT INTO session (id, project_id, title, directory, model, agent, cost,"
    " tokens_input, tokens_output, tokens_cache_read, tokens_cache_write,"
    " time_created, time_updated) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
    (OC_SESSION, "prj_webhooks", "Cap the delivery retry backoff", WEBHOOKS,
     json.dumps({"id": "claude-sonnet-4-5", "providerID": "anthropic"}), "build",
     0.1842, 18_400, 2_100, 61_000, 3_400, ms(262), ms(255)))

# A diff OpenCode's edit tool would attach: one file, unified, with the Index
# header its multi-file patches use.
OC_DIFF = """Index: delivery.go
===================================================================
--- delivery.go
+++ delivery.go
@@ -22,6 +22,9 @@
 func backoff(attempt int) time.Duration {
 \td := time.Duration(1<<attempt) * time.Second
+\tif d > maxBackoff {
+\t\treturn maxBackoff
+\t}
 \treturn d
 }
"""

OC_PARTS = [
    ("user", [{"type": "text", "text": "Deliveries that keep failing back off for "
                                       "hours. Cap it at a minute."}]),
    ("assistant", [
        {"type": "text", "text": "The backoff doubles without a ceiling. I'll clamp it."},
        {"type": "tool", "tool": "read", "callID": "oc1",
         "state": {"status": "completed",
                   "input": {"filePath": f"{WEBHOOKS}/delivery.go"},
                   "output": "22 lines"}},
        {"type": "tool", "tool": "edit", "callID": "oc2",
         "state": {"status": "completed",
                   "input": {"filePath": f"{WEBHOOKS}/delivery.go"},
                   "output": "applied", "metadata": {"diff": OC_DIFF}}},
        {"type": "text", "text": "Capped at `maxBackoff`. A delivery now retries "
                                 "every minute instead of every few hours."},
    ]),
]
pid = 0
for n, (role, parts) in enumerate(OC_PARTS, start=1):
    mid = f"msg_{n}"
    oc.execute("INSERT INTO message (id, session_id, time_created, data) VALUES (?,?,?,?)",
               (mid, OC_SESSION, ms(262) + n * 20_000,
                json.dumps({"role": role, "modelID": "claude-sonnet-4-5",
                            "providerID": "anthropic"})))
    for part in parts:
        pid += 1
        oc.execute("INSERT INTO part (id, message_id, time_created, data) VALUES (?,?,?,?)",
                   (f"prt_{pid:04d}", mid, ms(262) + n * 20_000, json.dumps(part)))
oc.commit()
oc.close()

# -- Copilot: the database holds the metadata, events.jsonl the conversation.
# The `turns` table lags behind a live session, so verbose reads the stream.
CP_HOME = os.path.join(HOME, ".copilot")
CP_SESSION = "7b2e91a4-0d63-4f85-ae27-1c9b5d3086fa"
os.makedirs(os.path.join(CP_HOME, "session-state", CP_SESSION), exist_ok=True)

cp = sqlite3.connect(os.path.join(CP_HOME, "session-store.db"))
cp.executescript("""
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, cwd TEXT, repository TEXT, host_type TEXT,
    branch TEXT, summary TEXT, created_at TEXT, updated_at TEXT);
CREATE TABLE turns (
    id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
    turn_index INTEGER NOT NULL, user_message TEXT, assistant_response TEXT,
    timestamp TEXT);
""")
cp.execute(
    "INSERT INTO sessions (id, cwd, branch, summary, created_at, updated_at)"
    " VALUES (?,?,?,?,?,?)",
    (CP_SESSION, WEBHOOKS, "main", "Explain the 504 connection leak",
     iso(322), iso(318)))
cp.commit()
cp.close()

CP_EVENTS = [
    ("session.start", {"sessionId": CP_SESSION, "copilotVersion": "1.0.98",
                       "context": {"cwd": WEBHOOKS}}),
    ("user.message", {"content": "Why does the delivery worker hold connections "
                                 "open after a 504?",
                      "transformedContent": "<current_datetime/>\n\nWhy does the "
                                            "delivery worker hold connections open "
                                            "after a 504?"}),
    ("assistant.message", {"model": "claude-haiku-4.5", "content":
        "Because the response body is only closed on the success path.\n\n"
        "`deliver` defers `resp.Body.Close()` inside the `if resp.StatusCode < 500` "
        "branch, so a 504 returns before the defer is registered and the connection "
        "is never released back to the pool. Moving the defer above the status check "
        "fixes it."}),
    ("session.usage_record", {"usage": {"model": "claude-haiku-4.5",
                                        "inputTokens": 9_140, "outputTokens": 288,
                                        "cacheReadTokens": 24_000, "cacheWriteTokens": 0,
                                        "cost": 0.0094, "duration": 6_210}}),
]
with open(os.path.join(CP_HOME, "session-state", CP_SESSION, "events.jsonl"), "w") as f:
    for n, (kind, data) in enumerate(CP_EVENTS):
        f.write(json.dumps({"type": kind, "timestamp": iso(322 - n),
                            "id": f"ev{n}", "parentId": None, "data": data}) + "\n")

# -- pi: a file per session under its own home, the closest layout to Claude
# Code's. It is also the only source that keeps its reasoning, so this session
# has a thinking block with something in it — which the replay shows as a
# "pi's reasoning" step, the one step no other agent here can produce.
PI_DIR = os.path.join(HOME, ".pi", "agent", "sessions", "--tmp-devdemo-notestore--")
os.makedirs(PI_DIR, exist_ok=True)
PI_SESSION = "3c8e1d60-74ab-4f29-9b15-6d20af7e5c83"

PI_DIFF = "\n".join([
    "     ...",
    "  14   // Note is a single stored note.",
    "  15   type Note struct {",
    "- 16   \tID   string",
    "+ 16   \tID        string",
    "+ 17   \tUpdatedAt time.Time",
    "  18   \tBody string",
    "  19   }",
])

PI_ENTRIES = [
    {"type": "session", "version": 3, "id": PI_SESSION,
     "timestamp": iso(372), "cwd": CWD},
    {"type": "model_change", "id": "m1", "parentId": None, "timestamp": iso(372),
     "provider": "anthropic", "modelId": "claude-sonnet-4-6"},
    {"type": "message", "id": "u1", "parentId": "m1", "timestamp": iso(371),
     "message": {"role": "user", "timestamp": int((NOW - timedelta(minutes=371)).timestamp() * 1000),
                 "content": [{"type": "text",
                              "text": "Notes come back without any sense of when they changed. "
                                      "Add an UpdatedAt and set it on write."}]}},
    {"type": "message", "id": "a1", "parentId": "u1", "timestamp": iso(370),
     "message": {"role": "assistant", "model": "claude-sonnet-4-6", "provider": "anthropic",
                 "timestamp": int((NOW - timedelta(minutes=370)).timestamp() * 1000),
                 "usage": {"input": 8420, "output": 310, "cacheRead": 21000,
                           "cacheWrite": 1200, "cost": {"total": 0.0418}},
                 "content": [
                     {"type": "thinking",
                      "text": "", "thinking":
                          "The Note struct is the only place the shape is defined, so adding the "
                          "field there is enough for the API to carry it.\n\nThe memory store "
                          "writes through Put, which is the single write path — setting the "
                          "timestamp anywhere else would miss the ones that go straight to the "
                          "map. Put it in Put."},
                     {"type": "text",
                      "text": "`Put` is the only write path, so the timestamp belongs there "
                              "rather than in each caller."},
                     {"type": "toolCall", "id": "pi-edit-1", "name": "edit",
                      "arguments": {"path": f"{CWD}/internal/store/store.go",
                                    "oldText": "\tID   string",
                                    "newText": "\tID        string\n\tUpdatedAt time.Time"}},
                 ]}},
    {"type": "message", "id": "r1", "parentId": "a1", "timestamp": iso(369),
     "message": {"role": "toolResult", "toolCallId": "pi-edit-1", "toolName": "edit",
                 "isError": False,
                 "timestamp": int((NOW - timedelta(minutes=369)).timestamp() * 1000),
                 "details": {"diff": PI_DIFF, "firstChangedLine": 16},
                 "content": [{"type": "text",
                              "text": f"Successfully replaced text in {CWD}/internal/store/store.go"}]}},
    {"type": "message", "id": "a2", "parentId": "r1", "timestamp": iso(368),
     "message": {"role": "assistant", "model": "claude-sonnet-4-6", "provider": "anthropic",
                 "timestamp": int((NOW - timedelta(minutes=368)).timestamp() * 1000),
                 "usage": {"input": 9100, "output": 120, "cacheRead": 24000,
                           "cacheWrite": 0, "cost": {"total": 0.0231}},
                 "content": [{"type": "text",
                              "text": "`Note` now carries `UpdatedAt`, stamped in `Put`. The "
                                      "handler needs no change — it marshals the struct whole."}]}},
]
with open(os.path.join(PI_DIR, f"2026-10-19T19-36-36-239Z_{PI_SESSION}.jsonl"), "w") as f:
    for e in PI_ENTRIES:
        f.write(json.dumps(e) + "\n")

# The two directories the other agents worked in have to exist, or the tree has
# nothing to read when one of their sessions is opened.
os.makedirs(WEBHOOKS, exist_ok=True)
open(os.path.join(WEBHOOKS, "delivery.go"), "w").write(
    """package webhooks

import "time"

const maxBackoff = time.Minute

// backoff is how long to wait before retrying a failed delivery.
func backoff(attempt int) time.Duration {
\td := time.Duration(1<<attempt) * time.Second
\tif d > maxBackoff {
\t\treturn maxBackoff
\t}
\treturn d
}
""")

print("wrote", PROJECT_DIR)
print("session lines:", len(lines), "| agent lines:", len(agent_lines))
print("codex:", CODEX_HOME, "| opencode:", OC_DIR, "| copilot:", CP_HOME,
      "| pi:", PI_DIR)
